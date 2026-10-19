/**
 * The company side of the desk.
 *
 * Every method takes the tenant as its first argument and that argument always
 * originates from `@CompanyId()` — the gateway-verified principal — never from a
 * path, a body or a query. Nothing here accepts a ticket without also being told
 * whose ticket it must be.
 */
import { Inject, Injectable } from '@nestjs/common';
import { notFound, parsePageRequest, type Page } from '@karmarat/nestshared';

import { STORAGE_PRESIGNER } from '../tokens';
import { newId } from '../common/idgen';
import { StoragePresigner, assertOwnedKey, type PresignedUpload } from '../common/storage';
import { CompanyThreadRepository } from './company-thread.repository';
import { SupportEventPublisher } from './events.publisher';
import { TicketRepository, type NewAttachment, type TicketFilters } from './tickets.repository';
import {
  isTicketCategory,
  isTicketPriority,
  isTicketState,
  stateAfterCompanyReply,
  assertClosable,
  type Ticket,
  type TicketCategory,
  type TicketPriority,
} from './domain';
import type { AttachmentRefDto, CreateTicketDto, ListTicketsQueryDto, ReplyDto, UploadUrlDto } from './dto';
import {
  toCompanySummary,
  toCompanyTicket,
  type CompanyTicketView,
  type TicketSummaryView,
} from './serializer';

/** The author of a request, taken from the verified principal. */
export interface Actor {
  accountId: string;
  email: string;
  companySlug: string;
}

@Injectable()
export class CompanySupportService {
  constructor(
    private readonly tickets: TicketRepository,
    private readonly thread: CompanyThreadRepository,
    private readonly events: SupportEventPublisher,
    // The presigner is built from parsed configuration rather than constructed
    // by Nest, so it arrives under an explicit token.
    @Inject(STORAGE_PRESIGNER) private readonly presigner: StoragePresigner,
  ) {}

  async create(
    companyId: string,
    actor: Actor,
    dto: CreateTicketDto,
    idempotencyKey: string | null,
  ): Promise<CompanyTicketView> {
    const { ticket, created } = await this.tickets.createTicket({
      companyId,
      companySlug: actor.companySlug,
      subject: dto.subject.trim(),
      body: dto.body,
      category: (dto.category && isTicketCategory(dto.category) ? dto.category : 'other') as TicketCategory,
      priority: (dto.priority && isTicketPriority(dto.priority) ? dto.priority : 'normal') as TicketPriority,
      openedByAccountId: actor.accountId,
      openedByEmail: actor.email,
      attachments: this.acceptAttachments(companyId, dto.attachments),
      idempotencyKey,
    });

    // A replayed create must not re-announce a ticket the desk already has.
    if (created) {
      await this.events.ticketCreated(ticket, actor.accountId);
    }

    return this.render(companyId, ticket);
  }

  async list(companyId: string, query: ListTicketsQueryDto): Promise<Page<TicketSummaryView>> {
    const page = parsePageRequest({ limit: query.limit, cursor: query.cursor });
    const result = await this.tickets.listForCompany(companyId, page, toFilters(query));
    return { items: result.items.map(toCompanySummary), nextCursor: result.nextCursor };
  }

  async get(companyId: string, ticketId: string): Promise<CompanyTicketView> {
    return this.render(companyId, await this.require(companyId, ticketId));
  }

  async reply(
    companyId: string,
    actor: Actor,
    ticketId: string,
    dto: ReplyDto,
    idempotencyKey: string | null,
  ): Promise<CompanyTicketView> {
    const ticket = await this.require(companyId, ticketId);
    const nextState = stateAfterCompanyReply(ticket.state);

    const message = await this.tickets.appendMessage(
      {
        ticketId: ticket.id,
        companyId,
        authorKind: 'company',
        authorAccountId: actor.accountId,
        authorEmail: actor.email,
        body: dto.body,
        // A company message can never be internal. The CHECK constraint on the
        // table says the same thing, because a literal here is only as good as
        // the code path that reaches it.
        internal: false,
        attachments: this.acceptAttachments(companyId, dto.attachments),
        idempotencyKey,
      },
      nextState,
    );

    const updated = await this.require(companyId, ticketId);
    if (message.created) {
      await this.events.ticketReplied(updated, message.id, 'company', actor.accountId);
    }

    return this.render(companyId, updated);
  }

  async close(companyId: string, ticketId: string): Promise<CompanyTicketView> {
    const ticket = await this.require(companyId, ticketId);
    assertClosable(ticket.state);

    // The tenant predicate is repeated in the UPDATE rather than trusted from
    // the SELECT above: between the two, nothing guarantees the row is still the
    // one we checked.
    const closed = await this.tickets.close(ticketId, companyId);
    if (!closed) {
      // Lost the race with another close; the ticket is closed either way.
      return this.render(companyId, await this.require(companyId, ticketId));
    }

    return this.render(companyId, closed);
  }

  /**
   * Signs one upload.
   *
   * The key is built from the verified tenant, so a client cannot choose where
   * its bytes land, and `assertOwnedKey` later refuses any key that was not
   * built this way.
   */
  uploadUrl(companyId: string, dto: UploadUrlDto): PresignedUpload {
    return this.presigner.presignUpload({
      companyId,
      fileName: dto.fileName,
      contentType: dto.contentType,
      sizeBytes: dto.sizeBytes,
      uploadId: newId('upl'),
    });
  }

  /** 404 rather than 403: another tenant's ticket does not exist here. */
  private async require(companyId: string, ticketId: string): Promise<Ticket> {
    const ticket = await this.tickets.findForCompany(ticketId, companyId);
    if (!ticket) {
      throw notFound('That support ticket does not exist.');
    }
    return ticket;
  }

  private async render(companyId: string, ticket: Ticket): Promise<CompanyTicketView> {
    const [messages, attachments] = await Promise.all([
      this.thread.messages(ticket.id, companyId),
      this.thread.attachments(ticket.id, companyId),
    ]);
    return toCompanyTicket(ticket, messages, attachments);
  }

  /**
   * Accepts only keys this tenant uploaded.
   *
   * Object keys arrive from the client, and a key is guessable once its shape is
   * known. Without this a tenant could attach `company/<other>/support/...` to
   * its own ticket and read the document back through the thread.
   */
  private acceptAttachments(companyId: string, refs: AttachmentRefDto[] | undefined): NewAttachment[] {
    return (refs ?? []).map((ref) => {
      assertOwnedKey(companyId, ref.objectKey);
      return {
        objectKey: ref.objectKey,
        fileName: ref.fileName,
        contentType: ref.contentType,
        sizeBytes: ref.sizeBytes,
      };
    });
  }
}

export function toFilters(query: ListTicketsQueryDto): TicketFilters {
  const filters: TicketFilters = {};
  if (query.state && isTicketState(query.state)) filters.state = query.state;
  if (query.priority && isTicketPriority(query.priority)) filters.priority = query.priority;
  if (query.category && isTicketCategory(query.category)) filters.category = query.category;
  return filters;
}
