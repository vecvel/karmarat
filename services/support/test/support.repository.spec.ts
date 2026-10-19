/**
 * The two boundaries this service exists to hold, proved against a real
 * database.
 *
 * The first is the tenant filter: a company sees its own tickets and nobody
 * else's. The second, and the one that would be a breach rather than a bug, is
 * the internal note — triage chatter written by platform staff that the customer
 * must never read. It is excluded in SQL, by a view that does not project the
 * `internal` column at all, so this suite asserts the behaviour *and* asserts
 * that the predicate is still in the relation the company side reads. Remove
 * `WHERE internal = false` from the view, or point the company repository at
 * the base table, and these tests fail.
 *
 * Postgres is required, so the suite skips cleanly when TEST_DATABASE_URL is
 * unset — `pnpm test` stays green on a laptop with nothing running, and CI,
 * which sets it, still gets the proofs:
 *
 *   TEST_DATABASE_URL=postgres://karmarat:karmarat@localhost:5432/support_test pnpm test
 */
import { createPool, migrate } from '@karmarat/nestshared';
import { randomUUID } from 'node:crypto';
import { join } from 'node:path';
import type { Pool } from 'pg';

import { CompanyThreadRepository } from '../src/support/company-thread.repository';
import { PlatformThreadRepository } from '../src/support/platform-thread.repository';
import { TicketRepository, type NewTicket } from '../src/support/tickets.repository';

const DATABASE_URL = process.env.TEST_DATABASE_URL ?? '';

// describe.skip rather than an early return, so a run without a database
// reports the suite as skipped instead of silently passing an empty file.
const describeWithDatabase = DATABASE_URL ? describe : describe.skip;

describeWithDatabase('support persistence (postgres)', () => {
  let pool: Pool;
  let tickets: TicketRepository;
  let companyThread: CompanyThreadRepository;
  let deskThread: PlatformThreadRepository;

  const tenantA = randomUUID();
  const tenantB = randomUUID();
  const opened: string[] = [];

  const openTicket = async (companyId: string, overrides: Partial<NewTicket> = {}) => {
    const { ticket } = await tickets.createTicket({
      companyId,
      companySlug: `t-${companyId.slice(0, 8)}`,
      subject: 'Cannot export applicants',
      body: 'The export button returns a 500.',
      category: 'technical',
      priority: 'normal',
      openedByAccountId: 'acc_1',
      openedByEmail: 'recruiter@example.invalid',
      attachments: [],
      idempotencyKey: null,
      ...overrides,
    });
    opened.push(ticket.id);
    return ticket;
  };

  beforeAll(async () => {
    pool = createPool({ url: DATABASE_URL });
    await migrate(pool, join(__dirname, '..', 'migrations'), { log: () => undefined });
    tickets = new TicketRepository(pool);
    companyThread = new CompanyThreadRepository(pool);
    deskThread = new PlatformThreadRepository(pool);
  });

  afterAll(async () => {
    if (opened.length > 0) {
      // Messages and attachments cascade from the ticket.
      await pool.query('DELETE FROM support_tickets WHERE id = ANY($1::text[])', [opened]);
    }
    await pool.end();
  });

  /* ------------------------------------------------------------ internal -- */

  describe('internal notes', () => {
    it('never reaches the company thread, while the desk sees it', async () => {
      const ticket = await openTicket(tenantA);

      await tickets.appendMessage(
        {
          ticketId: ticket.id,
          companyId: tenantA,
          authorKind: 'platform',
          authorAccountId: 'agent_1',
          authorEmail: 'agent@karmarat.invalid',
          body: 'This tenant is three invoices overdue — stall them.',
          internal: true,
          attachments: [],
          idempotencyKey: null,
        },
        'open',
      );

      await tickets.appendMessage(
        {
          ticketId: ticket.id,
          companyId: tenantA,
          authorKind: 'platform',
          authorAccountId: 'agent_1',
          authorEmail: 'agent@karmarat.invalid',
          body: 'We are looking into the export failure.',
          internal: false,
          attachments: [],
          idempotencyKey: null,
        },
        'awaiting_customer',
      );

      const companyView = await companyThread.messages(ticket.id, tenantA);
      const deskView = await deskThread.messages(ticket.id);

      // The note is in the record — this assertion is what stops the one below
      // from passing because nothing was ever written.
      expect(deskView.map((m) => m.body)).toContain('This tenant is three invoices overdue — stall them.');
      expect(deskView.filter((m) => m.internal)).toHaveLength(1);

      expect(companyView.map((m) => m.body)).not.toContain(
        'This tenant is three invoices overdue — stall them.',
      );
      expect(companyView.map((m) => m.body)).toEqual([
        'The export button returns a 500.',
        'We are looking into the export failure.',
      ]);
      // Not even as a field: the view does not project the column.
      expect(JSON.stringify(companyView)).not.toContain('internal');
    });

    it('hides an attachment that hangs off an internal note', async () => {
      const ticket = await openTicket(tenantA);
      const key = `company/${tenantA}/support/upl_${randomUUID().slice(0, 8)}/notes.pdf`;

      await tickets.appendMessage(
        {
          ticketId: ticket.id,
          companyId: tenantA,
          authorKind: 'platform',
          authorAccountId: 'agent_1',
          authorEmail: 'agent@karmarat.invalid',
          body: 'Internal: refund workings attached.',
          internal: true,
          attachments: [
            { objectKey: key, fileName: 'notes.pdf', contentType: 'application/pdf', sizeBytes: 12 },
          ],
          idempotencyKey: null,
        },
        'open',
      );

      expect((await deskThread.attachments(ticket.id)).map((a) => a.objectKey)).toContain(key);
      expect((await companyThread.attachments(ticket.id, tenantA)).map((a) => a.objectKey)).not.toContain(
        key,
      );
    });

    it('keeps the exclusion in SQL, not in application code', async () => {
      // A behavioural test alone would still pass if someone moved the filter
      // into a serializer, which is exactly the arrangement this service
      // refuses: it protects the one call site whose author remembered it. This
      // asserts the predicate is where it cannot be forgotten.
      const { rows } = await pool.query<{ definition: string }>(
        `SELECT pg_get_viewdef('support_company_messages'::regclass, true) AS definition`,
      );
      expect(rows[0]?.definition).toMatch(/internal\s*=\s*false|NOT\s+internal/i);

      const projected = await pool.query(
        `SELECT column_name FROM information_schema.columns WHERE table_name = 'support_company_messages'`,
      );
      expect(projected.rows.map((row: { column_name: string }) => row.column_name)).not.toContain(
        'internal',
      );
    });

    it('refuses an internal note written as the company, at the constraint', async () => {
      const ticket = await openTicket(tenantA);

      await expect(
        pool.query(
          `INSERT INTO support_ticket_messages (id, ticket_id, company_id, author_kind, author_account_id, body, internal)
           VALUES ($1, $2, $3, 'company', 'acc_1', 'hidden from myself', true)`,
          [`tms_${randomUUID()}`, ticket.id, tenantA],
        ),
      ).rejects.toThrow();
    });
  });

  /* -------------------------------------------------------------- tenancy -- */

  describe('the tenant boundary', () => {
    it('does not return another company’s ticket', async () => {
      const mine = await openTicket(tenantA);
      const theirs = await openTicket(tenantB);

      expect(await tickets.findForCompany(mine.id, tenantA)).not.toBeNull();
      // Null, not a 403 with a different shape: another tenant's ticket simply
      // does not exist from here.
      expect(await tickets.findForCompany(theirs.id, tenantA)).toBeNull();
    });

    it('lists only this company’s tickets', async () => {
      await openTicket(tenantA);
      const theirs = await openTicket(tenantB);

      const { items } = await tickets.listForCompany(tenantA, { limit: 100, cursor: null }, {});

      expect(items.length).toBeGreaterThan(0);
      expect(items.every((ticket) => ticket.companyId === tenantA)).toBe(true);
      expect(items.map((ticket) => ticket.id)).not.toContain(theirs.id);
    });

    it('does not let one company read another’s thread', async () => {
      const theirs = await openTicket(tenantB);

      expect(await companyThread.messages(theirs.id, tenantA)).toEqual([]);
      expect(await companyThread.attachments(theirs.id, tenantA)).toEqual([]);
    });

    it('does not close another company’s ticket', async () => {
      const theirs = await openTicket(tenantB);

      expect(await tickets.close(theirs.id, tenantA)).toBeNull();
      expect((await tickets.findForDesk(theirs.id))?.state).toBe('open');
    });

    it('lets the desk read across tenants, which is why its scope is separate', async () => {
      const a = await openTicket(tenantA);
      const b = await openTicket(tenantB);

      const { items } = await tickets.listForDesk({ limit: 100, cursor: null }, {});
      const ids = items.map((ticket) => ticket.id);

      expect(ids).toContain(a.id);
      expect(ids).toContain(b.id);
    });
  });

  /* ---------------------------------------------------------- idempotency -- */

  describe('replayed writes', () => {
    it('opens one ticket for a retried create', async () => {
      const key = `idem-${randomUUID()}`;

      const first = await tickets.createTicket({
        companyId: tenantA,
        companySlug: 'acme',
        subject: 'Retry me',
        body: 'First attempt.',
        category: 'other',
        priority: 'normal',
        openedByAccountId: 'acc_1',
        openedByEmail: 'recruiter@example.invalid',
        attachments: [],
        idempotencyKey: key,
      });
      opened.push(first.ticket.id);

      const second = await tickets.createTicket({
        companyId: tenantA,
        companySlug: 'acme',
        subject: 'Retry me',
        body: 'First attempt.',
        category: 'other',
        priority: 'normal',
        openedByAccountId: 'acc_1',
        openedByEmail: 'recruiter@example.invalid',
        attachments: [],
        idempotencyKey: key,
      });

      expect(second.created).toBe(false);
      expect(second.ticket.id).toBe(first.ticket.id);
      expect(await companyThread.messages(first.ticket.id, tenantA)).toHaveLength(1);
    });

    it('lets two tenants send the same key without colliding', async () => {
      const key = `shared-${randomUUID()}`;

      const mine = await openTicket(tenantA, { idempotencyKey: key });
      const theirs = await openTicket(tenantB, { idempotencyKey: key });

      expect(mine.id).not.toBe(theirs.id);
    });

    it('appends one message for a retried reply', async () => {
      const ticket = await openTicket(tenantA);
      const key = `reply-${randomUUID()}`;

      const message = {
        ticketId: ticket.id,
        companyId: tenantA,
        authorKind: 'company' as const,
        authorAccountId: 'acc_1',
        authorEmail: 'recruiter@example.invalid',
        body: 'Any update?',
        internal: false,
        attachments: [],
        idempotencyKey: key,
      };

      const first = await tickets.appendMessage(message, 'awaiting_support');
      const second = await tickets.appendMessage(message, 'awaiting_support');

      expect(second.created).toBe(false);
      expect(second.id).toBe(first.id);
      expect(await companyThread.messages(ticket.id, tenantA)).toHaveLength(2);
    });
  });

  /* ------------------------------------------------------------ desk ops -- */

  describe('the desk', () => {
    it('assigns, triages and closes', async () => {
      const ticket = await openTicket(tenantA);

      expect((await tickets.assign(ticket.id, 'agent_7'))?.assignedAgentId).toBe('agent_7');

      const triaged = await tickets.patch(ticket.id, { priority: 'urgent', state: 'resolved' });
      expect(triaged?.priority).toBe('urgent');
      expect(triaged?.state).toBe('resolved');
      expect(triaged?.resolvedAt).not.toBeNull();

      const closed = await tickets.close(ticket.id, null);
      expect(closed?.state).toBe('closed');
      expect(closed?.closedAt).not.toBeNull();

      // Conditional in SQL, so two agents clicking Close at the same moment
      // cannot overwrite each other's closed_at.
      expect(await tickets.close(ticket.id, null)).toBeNull();
    });

    it('filters its queue by agent without a tenant filter', async () => {
      const ticket = await openTicket(tenantB);
      const agent = `agent_${randomUUID().slice(0, 8)}`;
      await tickets.assign(ticket.id, agent);

      const { items } = await tickets.listForDesk(
        { limit: 100, cursor: null },
        { assignedAgentId: agent },
      );

      expect(items.map((row) => row.id)).toEqual([ticket.id]);
    });

    it('pages with a cursor rather than an offset', async () => {
      const agent = `page_${randomUUID().slice(0, 8)}`;
      for (let index = 0; index < 3; index += 1) {
        const ticket = await openTicket(tenantA);
        await tickets.assign(ticket.id, agent);
      }

      const first = await tickets.listForDesk({ limit: 2, cursor: null }, { assignedAgentId: agent });
      expect(first.items).toHaveLength(2);
      expect(first.nextCursor).not.toBeNull();

      const second = await tickets.listForDesk(
        { limit: 2, cursor: decode(first.nextCursor!) },
        { assignedAgentId: agent },
      );

      expect(second.items).toHaveLength(1);
      expect(second.nextCursor).toBeNull();
      expect(second.items.map((row) => row.id)).not.toContain(first.items[0]!.id);
    });
  });
});

/** Mirrors the cursor encoding in nestshared, so the test can page by hand. */
function decode(cursor: string): { createdAt: string; id: string } {
  const decoded = Buffer.from(cursor, 'base64url').toString('utf8');
  const separator = decoded.lastIndexOf('|');
  return { createdAt: decoded.slice(0, separator), id: decoded.slice(separator + 1) };
}
