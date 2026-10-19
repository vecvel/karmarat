/**
 * Applies platform events to the console's read model.
 *
 * The shape of every branch is the same and it is the shape that makes this
 * safe under at-least-once, out-of-order delivery:
 *
 *     withTransaction(pool, async (tx) => {
 *       if (!await repository.recordActivity(tx, envelope)) return;  // already applied
 *       await apply(tx, envelope);                                   // ...so apply it once
 *     });
 *
 * The activity insert is both the audit feed and the de-duplication ledger.
 * Because it shares the transaction with the projection write, a crash between
 * the two is impossible: either the event is recorded *and* applied, or neither.
 * A redelivery then finds the event id already present and does nothing, which
 * is what lets `application.submitted` be a plain counter increment rather than
 * a projected row.
 *
 * Ordering is handled separately, by the `occurred_at` watermark each upsert
 * compares against (see projection.repository.ts). De-duplication stops the
 * *same* event twice; the watermark stops an *older* event landing after a
 * newer one — a suspension redelivered after a reinstatement must not suspend
 * the tenant again.
 *
 * A branch that throws nak's the message and JetStream redelivers it. A branch
 * that cannot make sense of a payload does *not* throw: it records the event for
 * the audit feed and returns, because a message that can never succeed would
 * otherwise be retried until it was dropped, delaying every event behind it.
 */
import { Injectable, Logger } from '@nestjs/common';
import { Subject, withTransaction, type Envelope } from '@karmarat/nestshared';
import type { PoolClient } from 'pg';

import { intervalToMonths, UNNORMALISABLE } from '../overview/mrr';
import { asRecord, currency, firstOf, minorUnits, text, timestamp, uuid } from './decode';
import { ProjectionRepository } from './projection.repository';

/** Every subject this service projects. Also what the durable consumer filters on. */
export function subscribedSubjects(): string[] {
  return [
    Subject.CompanyRegistered,
    Subject.CompanyApproved,
    Subject.CompanySuspended,
    Subject.CompanyUpdated,

    Subject.SubscriptionActivated,
    Subject.SubscriptionRenewed,
    Subject.SubscriptionExpired,
    Subject.SubscriptionCancelled,

    Subject.PaymentSucceeded,
    Subject.PaymentFailed,

    Subject.SupportTicketCreated,
    Subject.SupportTicketReplied,

    Subject.CandidateRegistered,
    Subject.JobPublished,
    Subject.ApplicationSubmitted,
  ];
}

export const CANDIDATES_TOTAL = 'candidates_total';

@Injectable()
export class ProjectionConsumer {
  private readonly logger = new Logger(ProjectionConsumer.name);

  constructor(private readonly repository: ProjectionRepository) {}

  async handle(envelope: Envelope): Promise<void> {
    await withTransaction(this.repository.pool, async (tx) => {
      const isNew = await this.repository.recordActivity(tx, envelope);
      if (!isNew) {
        return;
      }
      await this.apply(tx, envelope);
    });
  }

  private async apply(tx: PoolClient, envelope: Envelope): Promise<void> {
    switch (envelope.subject) {
      case Subject.CompanyRegistered:
      case Subject.CompanyApproved:
      case Subject.CompanySuspended:
      case Subject.CompanyUpdated:
        return this.applyCompany(tx, envelope);

      case Subject.SubscriptionActivated:
      case Subject.SubscriptionRenewed:
      case Subject.SubscriptionExpired:
      case Subject.SubscriptionCancelled:
        return this.applySubscription(tx, envelope);

      case Subject.PaymentSucceeded:
      case Subject.PaymentFailed:
        return this.applyPayment(tx, envelope);

      case Subject.SupportTicketCreated:
      case Subject.SupportTicketReplied:
        return this.applyTicket(tx, envelope);

      case Subject.JobPublished:
        return this.applyJob(tx, envelope);

      case Subject.ApplicationSubmitted:
        return this.applyApplication(tx, envelope);

      case Subject.CandidateRegistered:
        return this.repository.incrementPlatformCounter(tx, CANDIDATES_TOTAL);

      default:
        // Recorded in the audit feed by the caller, projected nowhere. A new
        // subject arriving here is not an error.
        return;
    }
  }

  // ------------------------------------------------------------- companies --

  private async applyCompany(tx: PoolClient, envelope: Envelope): Promise<void> {
    const payload = asRecord(envelope.payload);
    const companyId = this.tenantOf(envelope, payload);
    if (!companyId) return;

    const occurredAt = timestamp(envelope.occurredAt) ?? new Date();

    // The event's own name is the authority on lifecycle state, because a
    // `company.suspended` payload that happened to carry `state: "active"`
    // would otherwise undo the very fact it announces.
    const stateBySubject: Record<string, string> = {
      [Subject.CompanyRegistered]: 'pending',
      [Subject.CompanyApproved]: 'active',
      [Subject.CompanySuspended]: 'suspended',
    };
    const state = stateBySubject[envelope.subject] ?? text(firstOf(payload, 'state', 'status'));

    await this.repository.upsertCompany(tx, {
      companyId,
      slug: text(firstOf(payload, 'slug')),
      name: text(firstOf(payload, 'name', 'companyName', 'legalName')),
      state,
      contactEmail: text(firstOf(payload, 'contactEmail', 'email', 'ownerEmail')),
      country: text(firstOf(payload, 'country', 'countryCode')),
      industry: text(firstOf(payload, 'industry', 'sector')),
      registeredAt:
        envelope.subject === Subject.CompanyRegistered
          ? (timestamp(firstOf(payload, 'registeredAt', 'createdAt')) ?? occurredAt)
          : timestamp(firstOf(payload, 'registeredAt', 'createdAt')),
      approvedAt:
        envelope.subject === Subject.CompanyApproved
          ? (timestamp(firstOf(payload, 'approvedAt')) ?? occurredAt)
          : null,
      suspendedAt:
        envelope.subject === Subject.CompanySuspended
          ? (timestamp(firstOf(payload, 'suspendedAt')) ?? occurredAt)
          : null,
      occurredAt,
    });
  }

  // --------------------------------------------------------- subscriptions --

  private async applySubscription(tx: PoolClient, envelope: Envelope): Promise<void> {
    const payload = asRecord(envelope.payload);
    const companyId = this.tenantOf(envelope, payload);
    if (!companyId) return;

    const occurredAt = timestamp(envelope.occurredAt) ?? new Date();
    const plan = asRecord(firstOf(payload, 'plan') ?? {});

    const stateBySubject: Record<string, string> = {
      [Subject.SubscriptionActivated]: 'active',
      [Subject.SubscriptionRenewed]: 'active',
      [Subject.SubscriptionExpired]: 'expired',
      [Subject.SubscriptionCancelled]: 'cancelled',
    };

    const rawInterval = firstOf(
      payload,
      'intervalMonths',
      'billingIntervalMonths',
      'interval',
      'billingInterval',
      'period',
      'duration',
    ) ?? firstOf(plan, 'intervalMonths', 'interval', 'billingInterval', 'period', 'duration');

    const intervalMonths = rawInterval === undefined ? 1 : intervalToMonths(rawInterval);
    if (intervalMonths === UNNORMALISABLE) {
      // Not an error — a weekly plan is a legitimate product. It is projected
      // with the sentinel so the dashboard can report it as unnormalisable
      // rather than quietly folding it into MRR at the wrong rate.
      this.logger.warn(`subscription billing period is not a whole number of months: ${String(rawInterval)}`);
    }

    await this.repository.upsertSubscription(tx, {
      companyId,
      subscriptionId: text(firstOf(payload, 'subscriptionId', 'id')),
      planId: text(firstOf(payload, 'planId') ?? firstOf(plan, 'id', 'planId')),
      planName: text(firstOf(payload, 'planName') ?? firstOf(plan, 'name', 'title')),
      intervalMonths,
      priceMinor:
        minorUnits(
          firstOf(payload, 'priceMinor', 'amountMinor', 'price') ??
            firstOf(plan, 'priceMinor', 'amountMinor', 'price'),
        ) ?? 0,
      currency: currency(firstOf(payload, 'currency') ?? firstOf(plan, 'currency')),
      state: stateBySubject[envelope.subject] ?? text(firstOf(payload, 'state', 'status')),
      startedAt: timestamp(firstOf(payload, 'startedAt', 'startsAt', 'activatedAt')),
      expiresAt: timestamp(firstOf(payload, 'expiresAt', 'endsAt', 'currentPeriodEnd')),
      cancelledAt:
        envelope.subject === Subject.SubscriptionCancelled
          ? (timestamp(firstOf(payload, 'cancelledAt')) ?? occurredAt)
          : null,
      occurredAt,
    });
  }

  // ---------------------------------------------------------------- money --

  private async applyPayment(tx: PoolClient, envelope: Envelope): Promise<void> {
    const payload = asRecord(envelope.payload);
    const companyId = this.tenantOf(envelope, payload);
    if (!companyId) return;

    // A payment with no id of its own cannot be de-duplicated against a
    // republish from the provider, so the event id stands in: it is unique and
    // stable for the delivery, which is the property the primary key needs.
    const id = text(firstOf(payload, 'paymentId', 'id', 'transactionId')) || envelope.id;

    const amount = minorUnits(firstOf(payload, 'amountMinor', 'amount', 'totalMinor'));
    if (amount === null) {
      this.logger.warn(`payment ${id} carried no usable minor-unit amount; recorded in the audit feed only`);
      return;
    }

    await this.repository.insertPayment(tx, {
      id,
      companyId,
      subscriptionId: text(firstOf(payload, 'subscriptionId')),
      amountMinor: amount,
      currency: currency(firstOf(payload, 'currency')),
      status: envelope.subject === Subject.PaymentSucceeded ? 'succeeded' : 'failed',
      failureReason:
        envelope.subject === Subject.PaymentFailed
          ? text(firstOf(payload, 'failureReason', 'reason', 'declineCode'))
          : '',
      paidAt:
        timestamp(firstOf(payload, 'paidAt', 'processedAt', 'occurredAt')) ??
        timestamp(envelope.occurredAt) ??
        new Date(),
    });
  }

  // -------------------------------------------------------------- support --

  private async applyTicket(tx: PoolClient, envelope: Envelope): Promise<void> {
    const payload = asRecord(envelope.payload);
    const companyId = this.tenantOf(envelope, payload);
    if (!companyId) return;

    const id = text(firstOf(payload, 'ticketId', 'id'));
    if (!id) {
      this.logger.warn('support ticket event carried no ticket id; recorded in the audit feed only');
      return;
    }

    const occurredAt = timestamp(envelope.occurredAt) ?? new Date();
    const status = text(firstOf(payload, 'status', 'state'));
    const closedAt = timestamp(firstOf(payload, 'closedAt', 'resolvedAt'));

    await this.repository.upsertTicket(tx, {
      id,
      companyId,
      subject: text(firstOf(payload, 'subject', 'title', 'summary')),
      // A reply says nothing about status on its own, so an absent status
      // leaves whatever the ticket already had.
      status: closedAt && !status ? 'closed' : status,
      priority: text(firstOf(payload, 'priority', 'severity')),
      openedAt: timestamp(firstOf(payload, 'openedAt', 'createdAt')) ?? occurredAt,
      lastReplyAt: envelope.subject === Subject.SupportTicketReplied ? occurredAt : null,
      closedAt,
      occurredAt,
    });
  }

  // ----------------------------------------------------------- recruiting --

  private async applyJob(tx: PoolClient, envelope: Envelope): Promise<void> {
    const payload = asRecord(envelope.payload);
    const companyId = this.tenantOf(envelope, payload);
    if (!companyId) return;

    const id = text(firstOf(payload, 'jobId', 'id'));
    if (!id) {
      this.logger.warn('job event carried no job id; recorded in the audit feed only');
      return;
    }

    await this.repository.upsertPublishedJob(tx, {
      id,
      companyId,
      title: text(firstOf(payload, 'title', 'jobTitle', 'name')),
      publishedAt:
        timestamp(firstOf(payload, 'publishedAt', 'openedAt')) ?? timestamp(envelope.occurredAt) ?? new Date(),
    });
  }

  private async applyApplication(tx: PoolClient, envelope: Envelope): Promise<void> {
    const companyId = this.tenantOf(envelope, asRecord(envelope.payload));
    if (!companyId) return;
    await this.repository.incrementApplicationCount(tx, companyId);
  }

  /**
   * The tenant a fact belongs to.
   *
   * The envelope's `companyId` is authoritative — the publisher sets it from its
   * own verified principal — and the payload is only a fallback for publishers
   * that carry it there instead. A value that is not a uuid is treated as
   * absent: it cannot be stored in a uuid column, and letting the insert fail
   * would nak a message that can never succeed.
   */
  private tenantOf(envelope: Envelope, payload: Record<string, unknown>): string | null {
    const companyId = uuid(envelope.companyId) ?? uuid(firstOf(payload, 'companyId', 'tenantId'));
    if (!companyId) {
      this.logger.warn(`${envelope.subject} carried no usable company id; recorded in the audit feed only`);
      return null;
    }
    return companyId;
  }
}
