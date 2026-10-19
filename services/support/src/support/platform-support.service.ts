/**
 * The support desk's side of a ticket.
 *
 * Nothing here takes a tenant argument, and that is the point: the desk works
 * across every company, so there is no company id to filter by and no path
 * parameter that could cross a boundary. What keeps a recruiter out is the
 * `platform_support.*` permission scope and the platform-only principal type on
 * the controller — a separate scope precisely because these queries cannot
 * defend themselves the way the company-side ones do.
 *
 * It reads the desk thread repository, which is the only code in this service
 * that can see internal notes. The company side reads views that do not contain
 * them, so the two audiences are separated by which relation a caller reaches
 * rather than by a flag someone has to remember to check.
 */
import { Injectable } from '@nestjs/common';
import { badRequest, notFound, parsePageRequest, type Page } from '@karmarat/nestshared';

import { PlatformThreadRepository } from './platform-thread.repository';
import { SupportEventPublisher } from './events.publisher';
import { TicketRepository, type TicketFilters } from './tickets.repository';
import { toFilters } from './company-support.service';
import {
  assertClosable,
  isTicketCategory,
  isTicketPriority,
  isTicketState,
  stateAfterPlatformReply,
  type Ticket,
  type TicketCategory,
  type TicketPriority,
  type TicketState,
} from './domain';
import type { AssignTicketDto, PatchTicketDto, PlatformListTicketsQueryDto, PlatformReplyDto } from './dto';
import {
  toPlatformSummary,
  toPlatformTicket,
  type PlatformTicketSummaryView,
  type PlatformTicketView,
} from './serializer';

/** The agent behind a desk request, taken from the verified principal. */
export interface Agent {
  accountId: string;
  email: string;
}

@Injectable()
export class PlatformSupportService {
  constructor(
    private readonly tickets: TicketRepository,
    private readonly thread: PlatformThreadRepository,
    private readonly events: SupportEventPublisher,
  ) {}

  async list(query: PlatformListTicketsQueryDto): Promise<Page<PlatformTicketSummaryView>> {
    const page = parsePageRequest({ limit: query.limit, cursor: query.cursor });

    const filters: TicketFilters & { assignedAgentId?: string } = toFilters(query);
    if (query.assignedAgentId) {
      filters.assignedAgentId = query.assignedAgentId;
    }

    const result = await this.tickets.listForDesk(page, filters);
    return { items: result.items.map(toPlatformSummary), nextCursor: result.nextCursor };
  }

  async get(ticketId: string): Promise<PlatformTicketView> {
    return this.render(await this.require(ticketId));
  }

  /**
   * Replies, or leaves an internal note.
   *
   * An internal note deliberately publishes no event and leaves the ticket's
   * state alone: it is desk bookkeeping, and a notification consumer that
   * received it would eventually mail it to the customer.
   */
  async reply(
    agent: Agent,
    ticketId: string,
    dto: PlatformReplyDto,
    idempotencyKey: string | null,
  ): Promise<PlatformTicketView> {
    const ticket = await this.require(ticketId);
    const internal = dto.internal === true;
    const nextState = stateAfterPlatformReply(ticket.state, internal);

    const message = await this.tickets.appendMessage(
      {
        ticketId: ticket.id,
        // Taken from the ticket, never from the request: the desk addresses a
        // ticket by id and must not be able to move a message between tenants.
        companyId: ticket.companyId,
        authorKind: 'platform',
        authorAccountId: agent.accountId,
        authorEmail: agent.email,
        body: dto.body,
        internal,
        // The desk uploads through the company's own prefix, which the company
        // presigner owns; the desk itself attaches nothing it did not receive.
        attachments: [],
        idempotencyKey,
      },
      nextState,
    );

    const updated = await this.require(ticketId);
    if (message.created && !internal) {
      await this.events.ticketReplied(updated, message.id, 'platform', agent.accountId);
    }

    return this.render(updated);
  }

  async assign(ticketId: string, dto: AssignTicketDto): Promise<PlatformTicketView> {
    const assigned = await this.tickets.assign(ticketId, dto.agentId.trim());
    if (!assigned) {
      throw notFound('That support ticket does not exist.');
    }
    return this.render(assigned);
  }

  async close(ticketId: string): Promise<PlatformTicketView> {
    const ticket = await this.require(ticketId);
    assertClosable(ticket.state);

    const closed = await this.tickets.close(ticketId, null);
    if (!closed) {
      // Lost the race with another agent's close; the ticket is closed either
      // way, so report the outcome rather than a conflict nobody caused.
      return this.render(await this.require(ticketId));
    }

    return this.render(closed);
  }

  /** Triage: priority, category and state, any subset of the three. */
  async patch(ticketId: string, dto: PatchTicketDto): Promise<PlatformTicketView> {
    const patch: { priority?: TicketPriority; category?: TicketCategory; state?: TicketState } = {};
    if (dto.priority && isTicketPriority(dto.priority)) patch.priority = dto.priority;
    if (dto.category && isTicketCategory(dto.category)) patch.category = dto.category;
    if (dto.state && isTicketState(dto.state)) patch.state = dto.state;

    if (Object.keys(patch).length === 0) {
      // An empty PATCH that answered 200 would read as a successful edit while
      // changing nothing, which is how a broken console goes unnoticed.
      throw badRequest('Supply at least one of priority, category or state.');
    }

    const updated = await this.tickets.patch(ticketId, patch);
    if (!updated) {
      throw notFound('That support ticket does not exist.');
    }
    return this.render(updated);
  }

  private async require(ticketId: string): Promise<Ticket> {
    const ticket = await this.tickets.findForDesk(ticketId);
    if (!ticket) {
      throw notFound('That support ticket does not exist.');
    }
    return ticket;
  }

  private async render(ticket: Ticket): Promise<PlatformTicketView> {
    const [messages, attachments] = await Promise.all([
      this.thread.messages(ticket.id),
      this.thread.attachments(ticket.id),
    ]);
    return toPlatformTicket(ticket, messages, attachments);
  }
}
