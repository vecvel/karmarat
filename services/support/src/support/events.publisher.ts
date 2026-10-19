/**
 * Domain events, so notifications can fan out without this service knowing how
 * anyone is reached.
 *
 * Two deliberate omissions.
 *
 * No payload carries a message body. A notification pipeline ends in an email,
 * and an event on a 30-day retained stream is the wrong place for a customer's
 * account details; a consumer that needs the text calls back for it with the
 * caller's own permissions.
 *
 * Internal notes publish nothing at all. Marking the event `internal: true` and
 * trusting every present and future consumer to honour the flag is the same
 * mistake as filtering notes in a serializer — one naive consumer mails the note
 * to the customer. A note is desk bookkeeping, not a reply, so there is no fact
 * here for anyone outside the desk to react to.
 */
import { Inject, Injectable, Logger } from '@nestjs/common';
import { EventBus, Subject } from '@karmarat/nestshared';

import { EVENT_BUS } from '../tokens';
import type { AuthorKind, Ticket } from './domain';

@Injectable()
export class SupportEventPublisher {
  private readonly logger = new Logger(SupportEventPublisher.name);

  constructor(@Inject(EVENT_BUS) private readonly bus: EventBus) {}

  async ticketCreated(ticket: Ticket, actorId: string): Promise<void> {
    await this.publish(
      Subject.SupportTicketCreated,
      {
        ticketId: ticket.id,
        companyId: ticket.companyId,
        subject: ticket.subject,
        category: ticket.category,
        priority: ticket.priority,
        state: ticket.state,
        openedByAccountId: ticket.openedByAccountId,
        createdAt: ticket.createdAt.toISOString(),
      },
      ticket.companyId,
      actorId,
      // Derived from the message, so a retried publish after a broker blip
      // de-duplicates instead of notifying the desk twice.
      `evt_support_created_${ticket.id}`,
    );
  }

  async ticketReplied(
    ticket: Ticket,
    messageId: string,
    authorKind: AuthorKind,
    actorId: string,
  ): Promise<void> {
    await this.publish(
      Subject.SupportTicketReplied,
      {
        ticketId: ticket.id,
        companyId: ticket.companyId,
        messageId,
        authorKind,
        // Who should hear about this: a company reply is news for the desk, a
        // desk reply is news for the company.
        audience: authorKind === 'company' ? 'platform' : 'company',
        subject: ticket.subject,
        state: ticket.state,
        priority: ticket.priority,
      },
      ticket.companyId,
      actorId,
      `evt_support_replied_${messageId}`,
    );
  }

  /**
   * Publishes after the transaction has committed.
   *
   * A failure is logged and swallowed: the ticket is already durable, and
   * turning a broker outage into a 500 would have the client retry a write that
   * succeeded. The lost notification is the smaller harm, and the event id makes
   * a replay safe if one is ever run.
   */
  private async publish(
    subject: string,
    payload: Record<string, unknown>,
    companyId: string,
    actorId: string,
    id: string,
  ): Promise<void> {
    try {
      await this.bus.publish(subject, payload, { companyId, actorId, id });
    } catch (error) {
      this.logger.error(`failed to publish ${subject}: ${(error as Error).message}`);
    }
  }
}
