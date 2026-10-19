/**
 * The writes that keep the read model current.
 *
 * Every method takes the transaction client rather than the pool: a projection
 * update and the activity row that de-duplicates it must commit together or not
 * at all, or a crash between the two would let the same event be applied twice.
 *
 * Two patterns repeat and are worth reading once:
 *
 *   - `ON CONFLICT ... DO UPDATE ... WHERE EXCLUDED.occurred_at >= <table>.occurred_at`
 *     is the out-of-order guard. Postgres evaluates the WHERE after choosing to
 *     update, so a late event simply does nothing rather than resurrecting a
 *     state the tenant has already left. `>=` rather than `>` because two facts
 *     can share a millisecond, and a genuine redelivery has already been
 *     stopped by the activity gate.
 *
 *   - `COALESCE(NULLIF(EXCLUDED.<col>, ''), <table>.<col>)` keeps a partial
 *     event from blanking a field it simply did not carry. `company.suspended`
 *     says nothing about the company's name, and it must not erase it.
 */
import { Inject, Injectable } from '@nestjs/common';
import type { Envelope } from '@karmarat/nestshared';
import type { Pool, PoolClient } from 'pg';

import { PG_POOL } from '../common/tokens';
import { splitSubject, uuid } from './decode';

export interface CompanyProjection {
  companyId: string;
  slug: string;
  name: string;
  state: string;
  contactEmail: string;
  country: string;
  industry: string;
  registeredAt: Date | null;
  approvedAt: Date | null;
  suspendedAt: Date | null;
  occurredAt: Date;
}

export interface SubscriptionProjection {
  companyId: string;
  subscriptionId: string;
  planId: string;
  planName: string;
  intervalMonths: number;
  priceMinor: number;
  currency: string;
  state: string;
  startedAt: Date | null;
  expiresAt: Date | null;
  cancelledAt: Date | null;
  occurredAt: Date;
}

export interface PaymentProjection {
  id: string;
  companyId: string;
  subscriptionId: string;
  amountMinor: number;
  currency: string;
  status: string;
  failureReason: string;
  paidAt: Date;
}

export interface TicketProjection {
  id: string;
  companyId: string;
  subject: string;
  status: string;
  priority: string;
  openedAt: Date | null;
  lastReplyAt: Date | null;
  closedAt: Date | null;
  occurredAt: Date;
}

export interface JobProjection {
  id: string;
  companyId: string;
  title: string;
  publishedAt: Date;
}

@Injectable()
export class ProjectionRepository {
  constructor(@Inject(PG_POOL) private readonly db: Pool) {}

  /** The pool, so the consumer can open the transaction its writes share. */
  get pool(): Pool {
    return this.db;
  }

  /**
   * Records an event and reports whether it is new.
   *
   * `false` means this event id has already been applied, and the caller must
   * do nothing else in the transaction. This single gate is what lets the
   * counters be plain increments: no payload needs a natural key, because the
   * event id is one.
   */
  async recordActivity(client: PoolClient, envelope: Envelope): Promise<boolean> {
    const { domain, action } = splitSubject(envelope.subject);

    // The tenant id is normalised here, not just in the consumer's branches.
    // `activity.company_id` is a uuid column and this insert is the FIRST
    // statement of every consumer transaction, so an envelope carrying an id in
    // some other shape — an older publisher, a fixture, a service that has not
    // adopted uuids — fails the cast before any branch gets to defend itself.
    // That throw naks a message that can never succeed, and JetStream then
    // redelivers it ahead of everything behind it until it gives up: one
    // malformed event stalls the whole projection. Storing NULL keeps the event
    // in the audit feed (the payload still has whatever was sent) and attributes
    // it to no tenant, which is exactly what the column already means.
    const companyId = uuid(envelope.companyId);

    const result = await client.query(
      `INSERT INTO activity (
         event_id, subject, domain, action, company_id, actor_id, correlation_id, occurred_at, payload)
       VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9::jsonb)
       ON CONFLICT (event_id) DO NOTHING`,
      [
        envelope.id,
        envelope.subject,
        domain,
        action,
        companyId,
        envelope.actorId ?? '',
        envelope.correlationId ?? '',
        envelope.occurredAt,
        JSON.stringify(envelope.payload ?? {}),
      ],
    );

    return (result.rowCount ?? 0) > 0;
  }

  async upsertCompany(client: PoolClient, company: CompanyProjection): Promise<void> {
    await client.query(
      `INSERT INTO companies (
         company_id, slug, name, state, contact_email, country, industry,
         registered_at, approved_at, suspended_at, occurred_at)
       VALUES ($1, $2, $3, COALESCE(NULLIF($4, ''), 'pending'), $5, $6, $7,
               COALESCE($8, now()), $9, $10, $11)
       ON CONFLICT (company_id) DO UPDATE SET
         slug          = COALESCE(NULLIF(EXCLUDED.slug, ''), companies.slug),
         name          = COALESCE(NULLIF(EXCLUDED.name, ''), companies.name),
         state         = COALESCE(NULLIF($4, ''), companies.state),
         contact_email = COALESCE(NULLIF(EXCLUDED.contact_email, ''), companies.contact_email),
         country       = COALESCE(NULLIF(EXCLUDED.country, ''), companies.country),
         industry      = COALESCE(NULLIF(EXCLUDED.industry, ''), companies.industry),
         -- Registration is the one timestamp a later event must not move: the
         -- signups-over-time chart is built on it.
         registered_at = LEAST(companies.registered_at, EXCLUDED.registered_at),
         approved_at   = COALESCE($9, companies.approved_at),
         suspended_at  = COALESCE($10, companies.suspended_at),
         occurred_at   = EXCLUDED.occurred_at,
         updated_at    = now()
       WHERE EXCLUDED.occurred_at >= companies.occurred_at`,
      [
        company.companyId,
        company.slug,
        company.name,
        company.state,
        company.contactEmail,
        company.country,
        company.industry,
        company.registeredAt,
        company.approvedAt,
        company.suspendedAt,
        company.occurredAt,
      ],
    );
  }

  async upsertSubscription(client: PoolClient, subscription: SubscriptionProjection): Promise<void> {
    await client.query(
      `INSERT INTO subscriptions (
         company_id, subscription_id, plan_id, plan_name, interval_months,
         price_minor, currency, state, started_at, expires_at, cancelled_at, occurred_at)
       VALUES ($1, $2, $3, $4, $5, $6, COALESCE(NULLIF($7, ''), 'USD'),
               COALESCE(NULLIF($8, ''), 'inactive'), $9, $10, $11, $12)
       ON CONFLICT (company_id) DO UPDATE SET
         subscription_id = COALESCE(NULLIF(EXCLUDED.subscription_id, ''), subscriptions.subscription_id),
         plan_id         = COALESCE(NULLIF(EXCLUDED.plan_id, ''), subscriptions.plan_id),
         plan_name       = COALESCE(NULLIF(EXCLUDED.plan_name, ''), subscriptions.plan_name),
         -- A price of zero is a legitimate free plan, so the fallback keys off
         -- the plan id being absent rather than the price being falsy.
         interval_months = CASE WHEN NULLIF(EXCLUDED.plan_id, '') IS NULL
                                THEN subscriptions.interval_months ELSE EXCLUDED.interval_months END,
         price_minor     = CASE WHEN NULLIF(EXCLUDED.plan_id, '') IS NULL
                                THEN subscriptions.price_minor ELSE EXCLUDED.price_minor END,
         currency        = COALESCE(NULLIF($7, ''), subscriptions.currency),
         state           = COALESCE(NULLIF($8, ''), subscriptions.state),
         started_at      = COALESCE($9, subscriptions.started_at),
         expires_at      = COALESCE($10, subscriptions.expires_at),
         cancelled_at    = COALESCE($11, subscriptions.cancelled_at),
         occurred_at     = EXCLUDED.occurred_at,
         updated_at      = now()
       WHERE EXCLUDED.occurred_at >= subscriptions.occurred_at`,
      [
        subscription.companyId,
        subscription.subscriptionId,
        subscription.planId,
        subscription.planName,
        subscription.intervalMonths,
        subscription.priceMinor,
        subscription.currency,
        subscription.state,
        subscription.startedAt,
        subscription.expiresAt,
        subscription.cancelledAt,
        subscription.occurredAt,
      ],
    );
  }

  /** Payments are immutable, so a conflict is a redelivery and is discarded. */
  async insertPayment(client: PoolClient, payment: PaymentProjection): Promise<void> {
    await client.query(
      `INSERT INTO payments (
         id, company_id, subscription_id, amount_minor, currency, status, failure_reason, paid_at)
       VALUES ($1, $2, $3, $4, COALESCE(NULLIF($5, ''), 'USD'), $6, $7, $8)
       ON CONFLICT (id) DO NOTHING`,
      [
        payment.id,
        payment.companyId,
        payment.subscriptionId,
        payment.amountMinor,
        payment.currency,
        payment.status,
        payment.failureReason,
        payment.paidAt,
      ],
    );
  }

  async upsertTicket(client: PoolClient, ticket: TicketProjection): Promise<void> {
    await client.query(
      `INSERT INTO support_tickets (
         id, company_id, subject, status, priority, opened_at, last_reply_at, closed_at, occurred_at)
       VALUES ($1, $2, $3, COALESCE(NULLIF($4, ''), 'open'), COALESCE(NULLIF($5, ''), 'normal'),
               COALESCE($6, now()), $7, $8, $9)
       ON CONFLICT (id) DO UPDATE SET
         subject       = COALESCE(NULLIF(EXCLUDED.subject, ''), support_tickets.subject),
         status        = COALESCE(NULLIF($4, ''), support_tickets.status),
         priority      = COALESCE(NULLIF($5, ''), support_tickets.priority),
         last_reply_at = GREATEST(support_tickets.last_reply_at, EXCLUDED.last_reply_at),
         closed_at     = COALESCE($8, support_tickets.closed_at),
         occurred_at   = EXCLUDED.occurred_at,
         updated_at    = now()
       WHERE EXCLUDED.occurred_at >= support_tickets.occurred_at`,
      [
        ticket.id,
        ticket.companyId,
        ticket.subject,
        ticket.status,
        ticket.priority,
        ticket.openedAt,
        ticket.lastReplyAt,
        ticket.closedAt,
        ticket.occurredAt,
      ],
    );
  }

  /**
   * Records a published requisition.
   *
   * Keyed by job id, so publish → unpublish → publish counts once. The title is
   * refreshed because the console shows it and an edited title should not read
   * as stale.
   */
  async upsertPublishedJob(client: PoolClient, job: JobProjection): Promise<void> {
    await client.query(
      `INSERT INTO published_jobs (id, company_id, title, published_at)
       VALUES ($1, $2, $3, $4)
       ON CONFLICT (id) DO UPDATE SET
         title = COALESCE(NULLIF(EXCLUDED.title, ''), published_jobs.title)`,
      [job.id, job.companyId, job.title, job.publishedAt],
    );
  }

  /** Safe only behind the activity gate. See the table comment in 001_admin.sql. */
  async incrementApplicationCount(client: PoolClient, companyId: string): Promise<void> {
    await client.query(
      `INSERT INTO company_counters (company_id, application_count)
       VALUES ($1, 1)
       ON CONFLICT (company_id) DO UPDATE SET
         application_count = company_counters.application_count + 1,
         updated_at        = now()`,
      [companyId],
    );
  }

  /** Likewise. Used for facts that belong to no tenant, such as candidates. */
  async incrementPlatformCounter(client: PoolClient, metric: string): Promise<void> {
    await client.query(
      `INSERT INTO platform_counters (metric, value)
       VALUES ($1, 1)
       ON CONFLICT (metric) DO UPDATE SET
         value      = platform_counters.value + 1,
         updated_at = now()`,
      [metric],
    );
  }

  /**
   * Drops audit rows older than the retention horizon.
   *
   * Bounded per run so a first prune against years of history does not hold a
   * long transaction over the table the feed reads from.
   */
  async pruneActivity(retentionDays: number, batchSize = 10_000): Promise<number> {
    const result = await this.db.query(
      `DELETE FROM activity
        WHERE event_id IN (
          SELECT event_id FROM activity
           WHERE occurred_at < now() - ($1 || ' days')::interval
           ORDER BY occurred_at
           LIMIT $2)`,
      [String(retentionDays), batchSize],
    );
    return result.rowCount ?? 0;
  }
}
