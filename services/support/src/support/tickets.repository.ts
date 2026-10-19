/**
 * Ticket persistence.
 *
 * Two rules are visible in every statement below.
 *
 * The first is the tenant filter. Company-side reads are written as
 * `WHERE id = $1 AND company_id = $2` rather than as a load followed by an
 * ownership check, because the predicate cannot be forgotten at a later call
 * site and because it returns "not found" instead of confirming that another
 * tenant holds that id.
 *
 * The second is that this class writes messages but never reads them. The
 * company- and desk-facing reads live in their own repositories against their
 * own relations, so the audience split is a property of which relation a caller
 * can reach rather than of a flag it must remember to check.
 */
import { Inject, Injectable } from '@nestjs/common';
import { buildPage, withTransaction, type Page, type PageRequest } from '@karmarat/nestshared';
import type { Pool, PoolClient } from 'pg';

import { PG_POOL } from '../tokens';
import { newId } from '../common/idgen';
import type {
  AuthorKind,
  Ticket,
  TicketAttachment,
  TicketCategory,
  TicketPriority,
  TicketState,
} from './domain';

const TICKET_COLUMNS = `
  id, company_id, company_slug, subject, category, priority, state,
  opened_by_account_id, opened_by_email, assigned_agent_id,
  last_activity_at, resolved_at, closed_at, created_at, updated_at`;

export interface TicketFilters {
  state?: TicketState;
  priority?: TicketPriority;
  category?: TicketCategory;
}

export interface NewTicket {
  companyId: string;
  companySlug: string;
  subject: string;
  body: string;
  category: TicketCategory;
  priority: TicketPriority;
  openedByAccountId: string;
  openedByEmail: string;
  attachments: NewAttachment[];
  idempotencyKey: string | null;
}

export interface NewMessage {
  ticketId: string;
  companyId: string;
  authorKind: AuthorKind;
  authorAccountId: string;
  authorEmail: string;
  body: string;
  internal: boolean;
  attachments: NewAttachment[];
  idempotencyKey: string | null;
}

export interface NewAttachment {
  objectKey: string;
  fileName: string;
  contentType: string;
  sizeBytes: number;
}

export interface CreatedMessage {
  id: string;
  createdAt: Date;
  /** False when an idempotent retry matched a message we had already stored. */
  created: boolean;
}

@Injectable()
export class TicketRepository {
  constructor(@Inject(PG_POOL) private readonly pool: Pool) {}

  /**
   * Opens a ticket and its first message together.
   *
   * One transaction, because a ticket with no body is a row the UI cannot
   * render and the desk cannot action.
   */
  async createTicket(input: NewTicket): Promise<{ ticket: Ticket; created: boolean }> {
    return withTransaction(this.pool, async (tx) => {
      const ticketId = newId('tkt');

      const inserted = await tx.query<TicketRow>(
        `INSERT INTO support_tickets (
           id, company_id, company_slug, subject, category, priority, state,
           opened_by_account_id, opened_by_email, idempotency_key
         ) VALUES ($1, $2, $3, $4, $5, $6, 'open', $7, $8, $9)
         ON CONFLICT (company_id, idempotency_key) WHERE idempotency_key IS NOT NULL DO NOTHING
         RETURNING ${TICKET_COLUMNS}`,
        [
          ticketId,
          input.companyId,
          input.companySlug,
          input.subject,
          input.category,
          input.priority,
          input.openedByAccountId,
          input.openedByEmail,
          input.idempotencyKey,
        ],
      );

      const row = inserted.rows[0];
      if (!row) {
        // The key was seen before: this is a retry of a create that already
        // landed, so return what it produced rather than opening a second
        // ticket for the same request.
        const existing = await tx.query<TicketRow>(
          `SELECT ${TICKET_COLUMNS} FROM support_tickets
           WHERE company_id = $1 AND idempotency_key = $2`,
          [input.companyId, input.idempotencyKey],
        );
        const previous = existing.rows[0];
        if (!previous) {
          // Only reachable if the conflicting row was deleted between the two
          // statements, which nothing in this service does.
          throw new Error('support: idempotent create conflicted with a row that no longer exists');
        }
        return { ticket: mapTicket(previous), created: false };
      }

      await this.insertMessage(tx, {
        ticketId,
        companyId: input.companyId,
        authorKind: 'company',
        authorAccountId: input.openedByAccountId,
        authorEmail: input.openedByEmail,
        body: input.body,
        internal: false,
        attachments: input.attachments,
        idempotencyKey: null,
      });

      return { ticket: mapTicket(row), created: true };
    });
  }

  /**
   * Appends a message and advances the ticket in the same transaction.
   *
   * A reply that landed while the state update did not would leave a ticket
   * sitting in the wrong queue with an unanswered customer in it.
   */
  async appendMessage(message: NewMessage, nextState: TicketState): Promise<CreatedMessage> {
    return withTransaction(this.pool, async (tx) => {
      const result = await this.insertMessage(tx, message);

      if (result.created) {
        await tx.query(
          // Every use of $2 is cast to the enum. Without the casts Postgres
          // deduces one type from `SET state = $2` and another from the
          // comparison against a string literal, and refuses the statement with
          // "inconsistent types deduced for parameter $2" — at runtime, on the
          // reply path, which a plain typecheck cannot see.
          `UPDATE support_tickets
              SET state = $2::support_ticket_state,
                  last_activity_at = now(),
                  resolved_at = CASE
                    WHEN $2::support_ticket_state = 'resolved' THEN COALESCE(resolved_at, now())
                    ELSE NULL END,
                  updated_at = now()
            WHERE id = $1`,
          [message.ticketId, nextState],
        );
      }

      return result;
    });
  }

  /** Shared by create and reply so both write attachments the same way. */
  private async insertMessage(tx: PoolClient, message: NewMessage): Promise<CreatedMessage> {
    const messageId = newId('tms');

    const inserted = await tx.query<{ id: string; created_at: Date }>(
      `INSERT INTO support_ticket_messages (
         id, ticket_id, company_id, author_kind, author_account_id, author_email,
         body, internal, idempotency_key
       ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
       ON CONFLICT (ticket_id, idempotency_key) WHERE idempotency_key IS NOT NULL DO NOTHING
       RETURNING id, created_at`,
      [
        messageId,
        message.ticketId,
        message.companyId,
        message.authorKind,
        message.authorAccountId,
        message.authorEmail,
        message.body,
        message.internal,
        message.idempotencyKey,
      ],
    );

    const row = inserted.rows[0];
    if (!row) {
      const existing = await tx.query<{ id: string; created_at: Date }>(
        `SELECT id, created_at FROM support_ticket_messages
          WHERE ticket_id = $1 AND idempotency_key = $2`,
        [message.ticketId, message.idempotencyKey],
      );
      const previous = existing.rows[0];
      if (!previous) {
        throw new Error('support: idempotent reply conflicted with a row that no longer exists');
      }
      return { id: previous.id, createdAt: previous.created_at, created: false };
    }

    for (const attachment of message.attachments) {
      await tx.query(
        `INSERT INTO support_ticket_attachments (
           id, ticket_id, message_id, company_id, object_key, file_name, content_type, size_bytes
         ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
        [
          newId('tat'),
          message.ticketId,
          messageId,
          message.companyId,
          attachment.objectKey,
          attachment.fileName,
          attachment.contentType,
          attachment.sizeBytes,
        ],
      );
    }

    return { id: row.id, createdAt: row.created_at, created: true };
  }

  // -------------------------------------------------------------- company --

  /** The tenant predicate is in the statement, not in a check after it. */
  async findForCompany(ticketId: string, companyId: string): Promise<Ticket | null> {
    const { rows } = await this.pool.query<TicketRow>(
      `SELECT ${TICKET_COLUMNS} FROM support_tickets WHERE id = $1 AND company_id = $2`,
      [ticketId, companyId],
    );
    const row = rows[0];
    return row ? mapTicket(row) : null;
  }

  async listForCompany(companyId: string, page: PageRequest, filters: TicketFilters): Promise<Page<Ticket>> {
    const { rows } = await this.pool.query<TicketRow>(
      `SELECT ${TICKET_COLUMNS}
         FROM support_tickets
        WHERE company_id = $1
          AND ($2::timestamptz IS NULL OR (created_at, id) < ($2::timestamptz, $3::text))
          AND ($4::support_ticket_state IS NULL OR state = $4)
          AND ($5::support_ticket_priority IS NULL OR priority = $5)
          AND ($6::support_ticket_category IS NULL OR category = $6)
        ORDER BY created_at DESC, id DESC
        LIMIT $7`,
      [
        companyId,
        page.cursor?.createdAt ?? null,
        page.cursor?.id ?? null,
        filters.state ?? null,
        filters.priority ?? null,
        filters.category ?? null,
        // One more than asked for: the extra row is how we know a next page
        // exists without a second count query.
        page.limit + 1,
      ],
    );

    return buildPage(rows.map(mapTicket), page.limit);
  }

  // ------------------------------------------------------------- platform --

  /**
   * No tenant predicate, by design.
   *
   * The desk is cross-tenant, which is exactly why `platform_support.*` is a
   * separate permission scope from `support.*`: the authorization is what keeps
   * a company role out of here, because the query cannot.
   */
  async findForDesk(ticketId: string): Promise<Ticket | null> {
    const { rows } = await this.pool.query<TicketRow>(
      `SELECT ${TICKET_COLUMNS} FROM support_tickets WHERE id = $1`,
      [ticketId],
    );
    const row = rows[0];
    return row ? mapTicket(row) : null;
  }

  async listForDesk(
    page: PageRequest,
    filters: TicketFilters & { assignedAgentId?: string },
  ): Promise<Page<Ticket>> {
    const { rows } = await this.pool.query<TicketRow>(
      `SELECT ${TICKET_COLUMNS}
         FROM support_tickets
        WHERE ($1::timestamptz IS NULL OR (created_at, id) < ($1::timestamptz, $2::text))
          AND ($3::support_ticket_state IS NULL OR state = $3)
          AND ($4::support_ticket_priority IS NULL OR priority = $4)
          AND ($5::support_ticket_category IS NULL OR category = $5)
          AND ($6::text IS NULL OR assigned_agent_id = $6)
        ORDER BY created_at DESC, id DESC
        LIMIT $7`,
      [
        page.cursor?.createdAt ?? null,
        page.cursor?.id ?? null,
        filters.state ?? null,
        filters.priority ?? null,
        filters.category ?? null,
        filters.assignedAgentId ?? null,
        page.limit + 1,
      ],
    );

    return buildPage(rows.map(mapTicket), page.limit);
  }

  async assign(ticketId: string, agentId: string): Promise<Ticket | null> {
    const { rows } = await this.pool.query<TicketRow>(
      `UPDATE support_tickets
          SET assigned_agent_id = $2, updated_at = now()
        WHERE id = $1
        RETURNING ${TICKET_COLUMNS}`,
      [ticketId, agentId],
    );
    const row = rows[0];
    return row ? mapTicket(row) : null;
  }

  /**
   * Closes a ticket.
   *
   * The `state <> 'closed'` predicate makes the close conditional in SQL rather
   * than only in the service: two agents clicking Close at the same moment
   * otherwise race, and the second would overwrite the first's `closed_at`.
   */
  async close(ticketId: string, companyId: string | null): Promise<Ticket | null> {
    const { rows } = await this.pool.query<TicketRow>(
      `UPDATE support_tickets
          SET state = 'closed', closed_at = now(), last_activity_at = now(), updated_at = now()
        WHERE id = $1
          AND ($2::uuid IS NULL OR company_id = $2)
          AND state <> 'closed'
        RETURNING ${TICKET_COLUMNS}`,
      [ticketId, companyId],
    );
    const row = rows[0];
    return row ? mapTicket(row) : null;
  }

  /** Partial update from the desk's triage panel. */
  async patch(
    ticketId: string,
    patch: { priority?: TicketPriority; category?: TicketCategory; state?: TicketState },
  ): Promise<Ticket | null> {
    const { rows } = await this.pool.query<TicketRow>(
      `UPDATE support_tickets
          SET priority = COALESCE($2::support_ticket_priority, priority),
              category = COALESCE($3::support_ticket_category, category),
              state    = COALESCE($4::support_ticket_state, state),
              resolved_at = CASE
                WHEN $4::support_ticket_state = 'resolved' THEN COALESCE(resolved_at, now())
                WHEN $4::support_ticket_state IS NOT NULL THEN NULL
                ELSE resolved_at END,
              closed_at = CASE
                WHEN $4::support_ticket_state = 'closed' THEN COALESCE(closed_at, now())
                WHEN $4::support_ticket_state IS NOT NULL THEN NULL
                ELSE closed_at END,
              updated_at = now()
        WHERE id = $1
        RETURNING ${TICKET_COLUMNS}`,
      [ticketId, patch.priority ?? null, patch.category ?? null, patch.state ?? null],
    );
    const row = rows[0];
    return row ? mapTicket(row) : null;
  }
}

interface TicketRow {
  id: string;
  company_id: string;
  company_slug: string;
  subject: string;
  category: TicketCategory;
  priority: TicketPriority;
  state: TicketState;
  opened_by_account_id: string;
  opened_by_email: string;
  assigned_agent_id: string | null;
  last_activity_at: Date;
  resolved_at: Date | null;
  closed_at: Date | null;
  created_at: Date;
  updated_at: Date;
}

function mapTicket(row: TicketRow): Ticket {
  return {
    id: row.id,
    companyId: row.company_id,
    companySlug: row.company_slug,
    subject: row.subject,
    category: row.category,
    priority: row.priority,
    state: row.state,
    openedByAccountId: row.opened_by_account_id,
    openedByEmail: row.opened_by_email,
    assignedAgentId: row.assigned_agent_id,
    lastActivityAt: row.last_activity_at,
    resolvedAt: row.resolved_at,
    closedAt: row.closed_at,
    createdAt: row.created_at,
    updatedAt: row.updated_at,
  };
}

export function mapAttachmentRow(row: {
  id: string;
  ticket_id: string;
  message_id: string;
  company_id: string;
  object_key: string;
  file_name: string;
  content_type: string;
  size_bytes: string | number;
  created_at: Date;
}): TicketAttachment {
  return {
    id: row.id,
    ticketId: row.ticket_id,
    messageId: row.message_id,
    companyId: row.company_id,
    objectKey: row.object_key,
    fileName: row.file_name,
    contentType: row.content_type,
    // pg hands back bigint as a string so a value beyond 2^53 is not silently
    // rounded. A file size is never that large, but the conversion is explicit.
    sizeBytes: Number(row.size_bytes),
    createdAt: row.created_at,
  };
}
