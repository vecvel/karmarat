/**
 * The consumer is the only writer in this service, and it runs against an
 * at-least-once, out-of-order stream. The cases below are the ways that bites:
 * the same event twice, an event whose payload contradicts its own name, a
 * payload missing the field the row is keyed on, and a fact that belongs to no
 * tenant at all.
 *
 * The out-of-order guard itself is a SQL predicate, so it is proved against a
 * real database in test/projection.postgres.spec.ts. What is proved here is the
 * half that lives in TypeScript: that the timestamp the guard compares is the
 * event's own, and that a redelivered event never reaches the projection at all.
 */
import { Subject, type Envelope } from '@karmarat/nestshared';
import type { Pool } from 'pg';

import { CANDIDATES_TOTAL, ProjectionConsumer } from './projection.consumer';
import type { ProjectionRepository } from './projection.repository';

const COMPANY = '11111111-2222-3333-4444-555555555555';

function envelope(over: Partial<Envelope> = {}): Envelope {
  return {
    id: `evt_${Math.random().toString(16).slice(2)}`,
    subject: Subject.CompanyRegistered,
    occurredAt: '2026-03-01T12:00:00.000Z',
    companyId: COMPANY,
    payload: {},
    ...over,
  };
}

/** A repository whose methods are spies, over a pool that pretends to transact. */
function fakeRepository(isNewEvent = true) {
  const client = { query: jest.fn().mockResolvedValue({ rowCount: 1, rows: [] }), release: jest.fn() };
  const pool = { connect: jest.fn().mockResolvedValue(client) } as unknown as Pool;

  return {
    pool,
    recordActivity: jest.fn().mockResolvedValue(isNewEvent),
    upsertCompany: jest.fn().mockResolvedValue(undefined),
    upsertSubscription: jest.fn().mockResolvedValue(undefined),
    insertPayment: jest.fn().mockResolvedValue(undefined),
    upsertTicket: jest.fn().mockResolvedValue(undefined),
    upsertPublishedJob: jest.fn().mockResolvedValue(undefined),
    incrementApplicationCount: jest.fn().mockResolvedValue(undefined),
    incrementPlatformCounter: jest.fn().mockResolvedValue(undefined),
    pruneActivity: jest.fn().mockResolvedValue(0),
  };
}

type FakeRepository = ReturnType<typeof fakeRepository>;

const consumerOver = (repository: FakeRepository): ProjectionConsumer =>
  new ProjectionConsumer(repository as unknown as ProjectionRepository);

describe('de-duplication', () => {
  it('applies an event the activity ledger has not seen', async () => {
    const repository = fakeRepository(true);
    await consumerOver(repository).handle(envelope());

    expect(repository.recordActivity).toHaveBeenCalledTimes(1);
    expect(repository.upsertCompany).toHaveBeenCalledTimes(1);
  });

  it('applies nothing when the event has already been recorded', async () => {
    // This is what makes an increment safe: a redelivered application.submitted
    // must not add to the counter a second time.
    const repository = fakeRepository(false);
    await consumerOver(repository).handle(
      envelope({ subject: Subject.ApplicationSubmitted, payload: { companyId: COMPANY } }),
    );

    expect(repository.recordActivity).toHaveBeenCalledTimes(1);
    expect(repository.incrementApplicationCount).not.toHaveBeenCalled();
  });
});

describe('ordering', () => {
  it('passes the event’s own occurredAt as the watermark, not the time it arrived', async () => {
    // The database compares this value to decide whether an event is stale. If
    // the consumer sent now() instead, every late redelivery would look newer
    // than the row it was about and would overwrite it.
    const repository = fakeRepository();
    await consumerOver(repository).handle(
      envelope({ subject: Subject.CompanyApproved, occurredAt: '2026-01-05T08:30:00.000Z' }),
    );

    const [, projected] = repository.upsertCompany.mock.calls[0]!;
    expect(projected.occurredAt.toISOString()).toBe('2026-01-05T08:30:00.000Z');
  });
});

describe('lifecycle state', () => {
  const cases: Array<[string, string, string]> = [
    ['registration', Subject.CompanyRegistered, 'pending'],
    ['approval', Subject.CompanyApproved, 'active'],
    ['suspension', Subject.CompanySuspended, 'suspended'],
  ];

  it.each(cases)('takes the state from the %s event itself', async (_label, subject, expected) => {
    // The payload is not trusted for this: a company.suspended that happened to
    // carry state "active" would otherwise undo the very fact it announces.
    const repository = fakeRepository();
    await consumerOver(repository).handle(envelope({ subject, payload: { state: 'active' } }));

    expect(repository.upsertCompany.mock.calls[0]?.[1].state).toBe(expected);
  });

  it('falls back to the payload for an event with no state of its own', async () => {
    const repository = fakeRepository();
    await consumerOver(repository).handle(
      envelope({ subject: Subject.CompanyUpdated, payload: { state: 'active' } }),
    );

    expect(repository.upsertCompany.mock.calls[0]?.[1].state).toBe('active');
  });
});

describe('subscriptions', () => {
  it('normalises a yearly plan to twelve months', async () => {
    const repository = fakeRepository();
    await consumerOver(repository).handle(
      envelope({
        subject: Subject.SubscriptionActivated,
        payload: { subscriptionId: 'sub_1', planId: 'plan_1', interval: 'yearly', priceMinor: 120_000, currency: 'usd' },
      }),
    );

    const projected = repository.upsertSubscription.mock.calls[0]?.[1];
    expect(projected).toMatchObject({ intervalMonths: 12, priceMinor: 120_000, currency: 'USD', state: 'active' });
  });

  it('marks a weekly plan unnormalisable instead of guessing a month count', async () => {
    const repository = fakeRepository();
    await consumerOver(repository).handle(
      envelope({
        subject: Subject.SubscriptionActivated,
        payload: { planId: 'plan_weekly', interval: 'weekly', priceMinor: 500 },
      }),
    );

    expect(repository.upsertSubscription.mock.calls[0]?.[1].intervalMonths).toBe(-1);
  });
});

describe('payloads a publisher got wrong', () => {
  it('records a payment with no usable amount in the audit feed only', async () => {
    // Projecting it as zero would put a wrong number on a revenue page; naking
    // it would retry a message that can never succeed, stalling every event
    // behind it.
    const repository = fakeRepository();
    await consumerOver(repository).handle(
      envelope({ subject: Subject.PaymentSucceeded, payload: { paymentId: 'pay_1', amount: 12.5 } }),
    );

    expect(repository.recordActivity).toHaveBeenCalledTimes(1);
    expect(repository.insertPayment).not.toHaveBeenCalled();
  });

  it('projects nothing for a tenant-scoped event carrying no company id', async () => {
    const repository = fakeRepository();
    await consumerOver(repository).handle(
      envelope({ subject: Subject.SupportTicketCreated, companyId: undefined, payload: { ticketId: 't1' } }),
    );

    expect(repository.upsertTicket).not.toHaveBeenCalled();
  });

  it('projects nothing for a company id that is not a uuid', async () => {
    // It could not be stored in a uuid column, and letting the insert fail
    // would nak a message that can never succeed.
    const repository = fakeRepository();
    await consumerOver(repository).handle(envelope({ companyId: 'not-a-uuid', payload: {} }));

    expect(repository.upsertCompany).not.toHaveBeenCalled();
  });

  it('falls back to the event id when a payment carries none of its own', async () => {
    const repository = fakeRepository();
    await consumerOver(repository).handle(
      envelope({ id: 'evt_fallback', subject: Subject.PaymentFailed, payload: { amountMinor: 999, reason: 'declined' } }),
    );

    expect(repository.insertPayment.mock.calls[0]?.[1]).toMatchObject({
      id: 'evt_fallback',
      status: 'failed',
      failureReason: 'declined',
    });
  });
});

describe('facts that belong to no tenant', () => {
  it('counts a candidate registration on the platform counter', async () => {
    const repository = fakeRepository();
    await consumerOver(repository).handle(
      envelope({ subject: Subject.CandidateRegistered, companyId: undefined, payload: { candidateId: 'c1' } }),
    );

    expect(repository.incrementPlatformCounter).toHaveBeenCalledWith(expect.anything(), CANDIDATES_TOTAL);
  });

  it('records an unknown subject in the feed and projects nothing', async () => {
    // A new subject arriving here is not an error; it is a service that shipped
    // before this console learned about it.
    const repository = fakeRepository();
    await consumerOver(repository).handle(envelope({ subject: 'karmarat.offer.accepted' }));

    expect(repository.recordActivity).toHaveBeenCalledTimes(1);
    expect(repository.upsertCompany).not.toHaveBeenCalled();
  });
});
