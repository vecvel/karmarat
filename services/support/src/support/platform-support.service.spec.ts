/**
 * What the desk's service does *around* a write, with the repositories faked.
 *
 * Two rules are worth pinning here because neither is visible in the schema.
 * An internal note must publish no event — a notification consumer that
 * received one would eventually mail triage chatter to the customer — and it
 * must leave the ticket's state alone, so the ticket stays in the desk's own
 * reply queue and the customer is told nothing by the state change.
 */
import { Problem } from '@karmarat/nestshared';

import type { PlatformThreadRepository } from './platform-thread.repository';
import type { SupportEventPublisher } from './events.publisher';
import type { CreatedMessage, NewMessage, TicketRepository } from './tickets.repository';
import { PlatformSupportService, type Agent } from './platform-support.service';
import type { Ticket, TicketState } from './domain';

const AGENT: Agent = { accountId: 'agent_1', email: 'agent@karmarat.invalid' };

function ticketAt(state: TicketState): Ticket {
  const now = new Date('2026-01-01T00:00:00.000Z');
  return {
    id: 'tkt_01ARZ3NDEKTSV4RRFFQ69G5FAV',
    companyId: '11111111-1111-4111-8111-111111111111',
    companySlug: 'acme',
    subject: 'Cannot export applicants',
    category: 'technical',
    priority: 'normal',
    state,
    openedByAccountId: 'acc_1',
    openedByEmail: 'recruiter@example.invalid',
    assignedAgentId: null,
    lastActivityAt: now,
    resolvedAt: null,
    closedAt: null,
    createdAt: now,
    updatedAt: now,
  };
}

interface Harness {
  service: PlatformSupportService;
  appended: Array<{ message: NewMessage; nextState: TicketState }>;
  replied: number;
}

function harness(state: TicketState): Harness {
  const appended: Array<{ message: NewMessage; nextState: TicketState }> = [];
  const state_ = { replied: 0 };

  const tickets = {
    findForDesk: async (): Promise<Ticket> => ticketAt(state),
    appendMessage: async (message: NewMessage, nextState: TicketState): Promise<CreatedMessage> => {
      appended.push({ message, nextState });
      return { id: 'tms_1', createdAt: new Date(), created: true };
    },
  } as unknown as TicketRepository;

  const thread = {
    messages: async () => [],
    attachments: async () => [],
  } as unknown as PlatformThreadRepository;

  const events = {
    ticketReplied: async (): Promise<void> => {
      state_.replied += 1;
    },
  } as unknown as SupportEventPublisher;

  const service = new PlatformSupportService(tickets, thread, events);

  return {
    service,
    appended,
    get replied(): number {
      return state_.replied;
    },
  };
}

describe('PlatformSupportService.reply', () => {
  it('publishes a reply to the customer and moves the ticket to awaiting_customer', async () => {
    const h = harness('awaiting_support');

    await h.service.reply(AGENT, 'tkt_1', { body: 'Looking into it.' }, null);

    expect(h.appended[0]?.message.internal).toBe(false);
    expect(h.appended[0]?.nextState).toBe('awaiting_customer');
    expect(h.replied).toBe(1);
  });

  it('publishes nothing for an internal note and leaves the state untouched', async () => {
    const h = harness('awaiting_support');

    await h.service.reply(AGENT, 'tkt_1', { body: 'Three invoices overdue.', internal: true }, null);

    expect(h.appended[0]?.message.internal).toBe(true);
    expect(h.appended[0]?.nextState).toBe('awaiting_support');
    // The whole point: no event, so no consumer can fan a note out to the
    // customer, whatever flag it might have been given to honour.
    expect(h.replied).toBe(0);
  });

  it('takes the tenant from the ticket, never from the request', async () => {
    const h = harness('open');

    await h.service.reply(AGENT, 'tkt_1', { body: 'Hello.' }, null);

    expect(h.appended[0]?.message.companyId).toBe(ticketAt('open').companyId);
  });

  it('refuses to reply to a closed ticket', async () => {
    const h = harness('closed');

    await expect(h.service.reply(AGENT, 'tkt_1', { body: 'Hello.' }, null)).rejects.toThrow(Problem);
    expect(h.appended).toHaveLength(0);
  });
});

describe('PlatformSupportService.patch', () => {
  it('refuses an empty patch rather than reporting a change it did not make', async () => {
    const h = harness('open');

    await expect(h.service.patch('tkt_1', {})).rejects.toThrow(Problem);
  });

  it('refuses a patch whose every field is unrecognised vocabulary', async () => {
    const h = harness('open');

    await expect(h.service.patch('tkt_1', { state: 'deleted' })).rejects.toThrow(Problem);
  });
});
