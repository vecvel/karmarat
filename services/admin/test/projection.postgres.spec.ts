/**
 * The two guarantees the read model rests on, proved against a real Postgres.
 *
 * They live in SQL — an `ON CONFLICT ... WHERE EXCLUDED.occurred_at >= ...`
 * predicate and a primary-key collision — so a mocked repository cannot prove
 * either one. What is checked here is exactly what the database does:
 *
 *   1. An event older than the row it would update changes nothing. Delivery is
 *      out of order, and a suspension redelivered after a reinstatement must not
 *      suspend the tenant again.
 *   2. The same event applied twice has the effect of applying it once.
 *
 * Skips cleanly without TEST_DATABASE_URL so `pnpm test` is green with no
 * database. Point it at a scratch database:
 *
 *   TEST_DATABASE_URL=postgres://karmarat:karmarat@localhost:5432/admin \
 *     pnpm --filter @karmarat/admin test
 */
import { Subject, createPool, migrate, type Envelope } from '@karmarat/nestshared';
import { randomUUID } from 'node:crypto';
import { join } from 'node:path';
import type { Pool } from 'pg';

import { ProjectionConsumer } from '../src/projection/projection.consumer';
import { ProjectionRepository } from '../src/projection/projection.repository';

const url = process.env['TEST_DATABASE_URL'];
const describeWithDatabase = url ? describe : describe.skip;

describeWithDatabase('projection against Postgres', () => {
  let pool: Pool;
  let consumer: ProjectionConsumer;
  const companyId = randomUUID();
  const eventIds: string[] = [];

  /** A distinct event id per call: the ledger de-duplicates on it. */
  function event(subject: string, occurredAt: string, payload: Record<string, unknown> = {}, id?: string): Envelope {
    const eventId = id ?? `evt_test_${randomUUID()}`;
    eventIds.push(eventId);
    return { id: eventId, subject, occurredAt, companyId, payload };
  }

  async function stateOf(): Promise<{ state: string; occurred_at: Date } | undefined> {
    const { rows } = await pool.query<{ state: string; occurred_at: Date }>(
      'SELECT state, occurred_at FROM companies WHERE company_id = $1',
      [companyId],
    );
    return rows[0];
  }

  beforeAll(async () => {
    pool = createPool({ url: url! });
    await migrate(pool, join(__dirname, '..', 'migrations'), { log: () => undefined });

    const repository = new ProjectionRepository(pool);
    consumer = new ProjectionConsumer(repository);
  });

  afterAll(async () => {
    // Scoped to this run's own rows, so the suite is safe against a database
    // that has real projections in it.
    if (eventIds.length) await pool.query('DELETE FROM activity WHERE event_id = ANY($1::text[])', [eventIds]);
    await pool.query('DELETE FROM companies WHERE company_id = $1', [companyId]);
    await pool.query('DELETE FROM company_counters WHERE company_id = $1', [companyId]);
    await pool.end();
  });

  it('applies the first event it sees', async () => {
    await consumer.handle(
      event(Subject.CompanyRegistered, '2026-01-01T00:00:00.000Z', { name: 'Ordering Test', slug: 'ordering-test' }),
    );

    expect((await stateOf())?.state).toBe('pending');
  });

  it('applies a newer event over an older row', async () => {
    await consumer.handle(event(Subject.CompanyApproved, '2026-02-01T00:00:00.000Z'));

    const row = await stateOf();
    expect(row?.state).toBe('active');
    expect(row?.occurred_at.toISOString()).toBe('2026-02-01T00:00:00.000Z');
  });

  it('ignores an event older than the row it holds', async () => {
    // The event this service was always going to receive eventually: a
    // suspension published before the approval, delivered after it. Without the
    // watermark the console would show a live tenant as suspended, and the
    // operator looking at it would suspend a real account to "fix" it.
    await consumer.handle(event(Subject.CompanySuspended, '2026-01-15T00:00:00.000Z'));

    const row = await stateOf();
    expect(row?.state).toBe('active');
    expect(row?.occurred_at.toISOString()).toBe('2026-02-01T00:00:00.000Z');
  });

  it('still applies a genuinely newer event afterwards', async () => {
    // The watermark must reject stale events, not freeze the row.
    await consumer.handle(event(Subject.CompanySuspended, '2026-03-01T00:00:00.000Z'));

    expect((await stateOf())?.state).toBe('suspended');
  });

  it('does not let a partial event blank a field it never carried', async () => {
    // company.suspended says nothing about the company's name.
    const { rows } = await pool.query<{ name: string; slug: string }>(
      'SELECT name, slug FROM companies WHERE company_id = $1',
      [companyId],
    );
    expect(rows[0]).toEqual({ name: 'Ordering Test', slug: 'ordering-test' });
  });

  it('records an event whose company id is not a uuid instead of stalling on it', async () => {
    // Observed against the live stream: an envelope carrying `co_acme` where a
    // uuid was expected. `activity.company_id` is a uuid column and this insert
    // is the first statement of every consumer transaction, so an unguarded
    // value fails the cast, naks a message that can never succeed, and blocks
    // every event behind it in the consumer's backlog until JetStream gives up.
    const eventId = `evt_test_${randomUUID()}`;
    eventIds.push(eventId);

    await expect(
      consumer.handle({
        id: eventId,
        subject: Subject.CompanyRegistered,
        occurredAt: '2026-03-03T00:00:00.000Z',
        companyId: 'co_acme',
        payload: { name: 'Acme' },
      }),
    ).resolves.toBeUndefined();

    const { rows } = await pool.query<{ company_id: string | null; payload: Record<string, unknown> }>(
      'SELECT company_id, payload FROM activity WHERE event_id = $1',
      [eventId],
    );
    // In the feed, attributed to no tenant — and the payload still carries what
    // was actually published, so the event is not lost, only unattributed.
    expect(rows[0]?.company_id).toBeNull();
    expect(rows[0]?.payload).toEqual({ name: 'Acme' });
  });

  it('counts a redelivered event once', async () => {
    // The whole reason application.submitted can be a plain increment: the
    // activity ledger refuses the second delivery inside the same transaction.
    const submitted = event(Subject.ApplicationSubmitted, '2026-03-02T00:00:00.000Z', { applicationId: 'app_1' });

    await consumer.handle(submitted);
    await consumer.handle(submitted);

    const { rows } = await pool.query<{ application_count: string }>(
      'SELECT application_count FROM company_counters WHERE company_id = $1',
      [companyId],
    );
    expect(Number(rows[0]?.application_count)).toBe(1);
  });
});
