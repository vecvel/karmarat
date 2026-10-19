/**
 * The idempotency guarantee, against a real Postgres.
 *
 * The in-memory suite proves the service *uses* the ledger correctly; only this
 * one proves the ledger itself holds, because the guarantee is a UNIQUE
 * constraint and a Map is not one. It is also the only place the concurrent
 * case is real: two claims racing on separate connections is exactly what a
 * provider's parallel retry does.
 *
 * Skips cleanly when TEST_DATABASE_URL is unset, so `pnpm test` stays green on
 * a laptop with nothing running.
 */
import { migrate } from '@karmarat/nestshared';
import { Pool } from 'pg';
import { join } from 'node:path';

import { newId } from '../common/ids';
import { WebhookEventsRepository } from './webhook-events.repository';

const url = process.env['TEST_DATABASE_URL'] ?? '';
const describeIfDatabase = url ? describe : describe.skip;

describeIfDatabase('webhook_events, against Postgres', () => {
  let pool: Pool;
  let repository: WebhookEventsRepository;

  beforeAll(async () => {
    pool = new Pool({ connectionString: url });
    await migrate(pool, join(__dirname, '..', '..', 'migrations'), { log: () => undefined });
    repository = new WebhookEventsRepository(pool);
  });

  afterAll(async () => {
    await pool?.end();
  });

  it('claims a provider event id once', async () => {
    const eventId = newId('test');

    const first = await repository.claim('manual', eventId, 'payment.succeeded', { a: 1 });
    const second = await repository.claim('manual', eventId, 'payment.succeeded', { a: 1 });

    expect(first.claimed).toBe(true);
    expect(second.claimed).toBe(false);
    // The loser is handed the winner's row, so the caller can report what
    // already happened rather than guessing.
    expect(second.record.id).toBe(first.record.id);
  });

  it('lets exactly one of several concurrent claims win', async () => {
    const eventId = newId('test');

    const results = await Promise.all(
      Array.from({ length: 5 }, () => repository.claim('manual', eventId, 'payment.succeeded', {})),
    );

    expect(results.filter((result) => result.claimed)).toHaveLength(1);
    const ids = new Set(results.map((result) => result.record.id));
    expect(ids.size).toBe(1);
  });

  it('records why an event was ignored, for the question asked days later', async () => {
    const eventId = newId('test');
    const { record } = await repository.claim('manual', eventId, 'customer.updated', {});

    await repository.markIgnored(record.id, 'no handler for provider event type "customer.updated"');

    const stored = await repository.findByProviderEventId(eventId);
    expect(stored?.status).toBe('ignored');
    expect(stored?.ignoredReason).toContain('customer.updated');
    expect(stored?.processedAt).not.toBeNull();
  });

  it('leaves a failed event without a processed_at, so reconciliation still sees it', async () => {
    const eventId = newId('test');
    const { record } = await repository.claim('manual', eventId, 'payment.succeeded', {});

    await repository.markFailed(record.id, 'amount mismatch');

    const stored = await repository.findByProviderEventId(eventId);
    expect(stored?.status).toBe('failed');
    expect(stored?.deliveryError).toBe('amount mismatch');
    expect(stored?.processedAt).toBeNull();
  });
});
