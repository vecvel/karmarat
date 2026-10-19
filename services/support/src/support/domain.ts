/**
 * Ticket vocabulary and the state machine, with no I/O.
 *
 * The transitions live here rather than in the controllers because both sides of
 * the desk drive the same ticket: if the company controller decided its own
 * successor state and the platform controller decided its own, the two would
 * drift and a ticket would end up in a state neither side expects to answer.
 */
import { conflict } from '@karmarat/nestshared';

export const TICKET_STATES = ['open', 'awaiting_customer', 'awaiting_support', 'resolved', 'closed'] as const;
export type TicketState = (typeof TICKET_STATES)[number];

export const TICKET_PRIORITIES = ['low', 'normal', 'high', 'urgent'] as const;
export type TicketPriority = (typeof TICKET_PRIORITIES)[number];

export const TICKET_CATEGORIES = ['billing', 'technical', 'account', 'feature_request', 'other'] as const;
export type TicketCategory = (typeof TICKET_CATEGORIES)[number];

export type AuthorKind = 'company' | 'platform';

/** A closed ticket is the one terminal state; resolved is still reopenable. */
export function isClosed(state: TicketState): boolean {
  return state === 'closed';
}

/**
 * Where a ticket lands when the customer writes.
 *
 * Any reply reopens a resolved ticket: "resolved" is the desk's opinion, and the
 * customer writing back is the customer disagreeing with it.
 */
export function stateAfterCompanyReply(current: TicketState): TicketState {
  if (isClosed(current)) {
    throw conflict('ticket_closed', 'This ticket is closed. Please open a new ticket instead.');
  }
  return 'awaiting_support';
}

/**
 * Where a ticket lands when the desk writes.
 *
 * An internal note leaves the state exactly as it was. Moving a ticket to
 * `awaiting_customer` because an agent left themselves a note would tell the
 * customer, through the state alone, that something happened they cannot see —
 * and would stop the ticket appearing in the desk's own "needs a reply" queue.
 */
export function stateAfterPlatformReply(current: TicketState, internal: boolean): TicketState {
  if (isClosed(current)) {
    throw conflict('ticket_closed', 'This ticket is closed and can no longer be replied to.');
  }
  return internal ? current : 'awaiting_customer';
}

/** Closing twice is the client applying the same mutation twice, so 409. */
export function assertClosable(current: TicketState): void {
  if (isClosed(current)) {
    throw conflict('ticket_already_closed', 'This ticket is already closed.');
  }
}

export function isTicketState(value: string): value is TicketState {
  return (TICKET_STATES as readonly string[]).includes(value);
}

export function isTicketPriority(value: string): value is TicketPriority {
  return (TICKET_PRIORITIES as readonly string[]).includes(value);
}

export function isTicketCategory(value: string): value is TicketCategory {
  return (TICKET_CATEGORIES as readonly string[]).includes(value);
}

// ---------------------------------------------------------------------------
// Rows
// ---------------------------------------------------------------------------
// Shapes the repositories return. `internal` is deliberately absent from
// TicketMessage: the company-side repository reads a view that does not project
// it, so a type that carried the field would invite a call site to check it in
// TypeScript — which is precisely the "filter in the serializer" this service
// refuses to rely on.

export interface Ticket {
  id: string;
  companyId: string;
  companySlug: string;
  subject: string;
  category: TicketCategory;
  priority: TicketPriority;
  state: TicketState;
  openedByAccountId: string;
  openedByEmail: string;
  assignedAgentId: string | null;
  lastActivityAt: Date;
  resolvedAt: Date | null;
  closedAt: Date | null;
  createdAt: Date;
  updatedAt: Date;
}

export interface TicketMessage {
  id: string;
  ticketId: string;
  companyId: string;
  authorKind: AuthorKind;
  authorAccountId: string;
  authorEmail: string;
  body: string;
  createdAt: Date;
}

/** Only ever produced by the platform-side repository. */
export interface PlatformTicketMessage extends TicketMessage {
  internal: boolean;
}

export interface TicketAttachment {
  id: string;
  ticketId: string;
  messageId: string;
  companyId: string;
  objectKey: string;
  fileName: string;
  contentType: string;
  sizeBytes: number;
  createdAt: Date;
}
