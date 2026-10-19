/**
 * The tenant view, assembled from this service's own projections.
 *
 * The joins below look like cross-service joins and are not: `companies`,
 * `subscriptions`, `payments` and `support_tickets` are all tables in *this*
 * database, each one a projection of an event another service published. The
 * console never reaches into another service's schema, and never writes back —
 * a console action publishes to the owning service instead.
 *
 * The lateral subqueries are what keep the list one round trip. The alternative
 * — a page of tenants, then a payment query per tenant — is the N+1 that makes
 * an admin list slow exactly when the platform is busiest.
 */
import { Inject, Injectable } from '@nestjs/common';
import { badRequest, type Cursor } from '@karmarat/nestshared';
import type { Pool } from 'pg';

import { toNumber } from '../common/numbers';
import { Params, escapeLike, whereClause } from '../common/sql';
import { PG_POOL } from '../common/tokens';

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

export interface CompanyRow {
  companyId: string;
  slug: string;
  name: string;
  state: string;
  contactEmail: string;
  country: string;
  industry: string;
  registeredAt: Date;
  approvedAt: Date | null;
  suspendedAt: Date | null;
  subscription: {
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
  } | null;
  lastPayment: {
    id: string;
    amountMinor: number;
    currency: string;
    status: string;
    failureReason: string;
    paidAt: Date;
  } | null;
  openTickets: number;
  applications: number;
  publishedJobs: number;
}

export interface CompanyFilters {
  state?: string;
  planId?: string;
  subscriptionState?: string;
  search?: string;
}

export interface PaymentRow {
  id: string;
  amountMinor: number;
  currency: string;
  status: string;
  failureReason: string;
  paidAt: Date;
}

export interface TicketRow {
  id: string;
  subject: string;
  status: string;
  priority: string;
  openedAt: Date;
  lastReplyAt: Date | null;
  closedAt: Date | null;
}

/** Everything the joined view selects, before it is reshaped. */
const SELECT_COMPANY = `
  SELECT c.company_id, c.slug, c.name, c.state, c.contact_email, c.country, c.industry,
         c.registered_at, c.approved_at, c.suspended_at,
         s.subscription_id, s.plan_id, s.plan_name, s.interval_months, s.price_minor,
         s.currency, s.state AS subscription_state, s.started_at, s.expires_at, s.cancelled_at,
         p.id AS payment_id, p.amount_minor AS payment_amount, p.currency AS payment_currency,
         p.status AS payment_status, p.failure_reason AS payment_failure_reason,
         p.paid_at AS payment_paid_at,
         t.open_tickets,
         coalesce(cc.application_count, 0) AS application_count,
         j.published_jobs
    FROM companies c
    LEFT JOIN subscriptions s ON s.company_id = c.company_id
    LEFT JOIN company_counters cc ON cc.company_id = c.company_id
    LEFT JOIN LATERAL (
      SELECT id, amount_minor, currency, status, failure_reason, paid_at
        FROM payments
       WHERE company_id = c.company_id
       ORDER BY paid_at DESC, id DESC
       LIMIT 1) p ON true
    LEFT JOIN LATERAL (
      SELECT count(*) AS open_tickets
        FROM support_tickets
       WHERE company_id = c.company_id AND status <> 'closed') t ON true
    LEFT JOIN LATERAL (
      SELECT count(*) AS published_jobs
        FROM published_jobs
       WHERE company_id = c.company_id) j ON true`;

interface RawCompany {
  company_id: string;
  slug: string;
  name: string;
  state: string;
  contact_email: string;
  country: string;
  industry: string;
  registered_at: Date;
  approved_at: Date | null;
  suspended_at: Date | null;
  subscription_id: string | null;
  plan_id: string | null;
  plan_name: string | null;
  interval_months: number | null;
  price_minor: string | null;
  currency: string | null;
  subscription_state: string | null;
  started_at: Date | null;
  expires_at: Date | null;
  cancelled_at: Date | null;
  payment_id: string | null;
  payment_amount: string | null;
  payment_currency: string | null;
  payment_status: string | null;
  payment_failure_reason: string | null;
  payment_paid_at: Date | null;
  open_tickets: string;
  application_count: string;
  published_jobs: string;
}

@Injectable()
export class CompaniesRepository {
  constructor(@Inject(PG_POOL) private readonly db: Pool) {}

  /** One page of tenants, newest registration first. Fetches limit+1 to detect a next page. */
  async list(filters: CompanyFilters, limit: number, cursor: Cursor | null): Promise<CompanyRow[]> {
    const params = new Params();
    const conditions: string[] = [];

    if (filters.state) conditions.push(`c.state = ${params.add(filters.state)}`);
    if (filters.planId) conditions.push(`s.plan_id = ${params.add(filters.planId)}`);
    if (filters.subscriptionState) {
      // 'none' is a real answer to "which plan state?" — a tenant that has never
      // subscribed is precisely who an operator filtering this way is looking for.
      conditions.push(
        filters.subscriptionState === 'none'
          ? 's.company_id IS NULL'
          : `s.state = ${params.add(filters.subscriptionState)}`,
      );
    }
    if (filters.search) {
      const pattern = params.add(`%${escapeLike(filters.search)}%`);
      conditions.push(`(c.name ILIKE ${pattern} OR c.slug ILIKE ${pattern} OR c.contact_email ILIKE ${pattern})`);
    }
    if (cursor) {
      // Keyset, not OFFSET: a tenant registering mid-scan would otherwise shift
      // every later page and show an operator the same company twice.
      conditions.push(
        `(c.registered_at, c.company_id) < (${params.add(cursor.createdAt)}::timestamptz, ${params.add(
          requireUuidCursor(cursor.id),
        )}::uuid)`,
      );
    }

    const { rows } = await this.db.query<RawCompany>(
      `${SELECT_COMPANY}
       ${whereClause(conditions)}
       ORDER BY c.registered_at DESC, c.company_id DESC
       LIMIT ${params.add(limit + 1)}`,
      params.all(),
    );

    return rows.map(toCompanyRow);
  }

  async get(companyId: string): Promise<CompanyRow | null> {
    const { rows } = await this.db.query<RawCompany>(`${SELECT_COMPANY} WHERE c.company_id = $1::uuid`, [companyId]);
    const row = rows[0];
    return row ? toCompanyRow(row) : null;
  }

  async recentPayments(companyId: string, limit: number): Promise<PaymentRow[]> {
    const { rows } = await this.db.query<{
      id: string;
      amount_minor: string;
      currency: string;
      status: string;
      failure_reason: string;
      paid_at: Date;
    }>(
      `SELECT id, amount_minor, currency, status, failure_reason, paid_at
         FROM payments
        WHERE company_id = $1::uuid
        ORDER BY paid_at DESC, id DESC
        LIMIT $2`,
      [companyId, limit],
    );

    return rows.map((row) => ({
      id: row.id,
      amountMinor: toNumber(row.amount_minor),
      currency: row.currency,
      status: row.status,
      failureReason: row.failure_reason,
      paidAt: row.paid_at,
    }));
  }

  /** Open tickets first, because those are the ones an operator is looking for. */
  async tickets(companyId: string, limit: number): Promise<TicketRow[]> {
    const { rows } = await this.db.query<{
      id: string;
      subject: string;
      status: string;
      priority: string;
      opened_at: Date;
      last_reply_at: Date | null;
      closed_at: Date | null;
    }>(
      `SELECT id, subject, status, priority, opened_at, last_reply_at, closed_at
         FROM support_tickets
        WHERE company_id = $1::uuid
        ORDER BY (status <> 'closed') DESC, opened_at DESC, id DESC
        LIMIT $2`,
      [companyId, limit],
    );

    return rows.map((row) => ({
      id: row.id,
      subject: row.subject,
      status: row.status,
      priority: row.priority,
      openedAt: row.opened_at,
      lastReplyAt: row.last_reply_at,
      closedAt: row.closed_at,
    }));
  }
}

function toCompanyRow(row: RawCompany): CompanyRow {
  return {
    companyId: row.company_id,
    slug: row.slug,
    name: row.name,
    state: row.state,
    contactEmail: row.contact_email,
    country: row.country,
    industry: row.industry,
    registeredAt: row.registered_at,
    approvedAt: row.approved_at,
    suspendedAt: row.suspended_at,
    // A tenant with no subscription row has never subscribed; that is a state
    // the console must render, not a missing join to paper over.
    subscription: row.subscription_state
      ? {
          subscriptionId: row.subscription_id ?? '',
          planId: row.plan_id ?? '',
          planName: row.plan_name ?? '',
          intervalMonths: toNumber(row.interval_months),
          priceMinor: toNumber(row.price_minor),
          currency: row.currency ?? '',
          state: row.subscription_state,
          startedAt: row.started_at,
          expiresAt: row.expires_at,
          cancelledAt: row.cancelled_at,
        }
      : null,
    lastPayment:
      row.payment_id && row.payment_paid_at
        ? {
            id: row.payment_id,
            amountMinor: toNumber(row.payment_amount),
            currency: row.payment_currency ?? '',
            status: row.payment_status ?? '',
            failureReason: row.payment_failure_reason ?? '',
            paidAt: row.payment_paid_at,
          }
        : null,
    openTickets: toNumber(row.open_tickets),
    applications: toNumber(row.application_count),
    publishedJobs: toNumber(row.published_jobs),
  };
}

/**
 * A cursor's id half must be a uuid here.
 *
 * It is cast to uuid in the keyset predicate, so anything else reaches Postgres
 * as a cast error and surfaces as an opaque 500. A caller that sent a cursor
 * from a different endpoint deserves to be told which thing was wrong.
 */
function requireUuidCursor(id: string): string {
  if (!UUID.test(id)) {
    throw badRequest('The supplied cursor is not valid.');
  }
  return id;
}
