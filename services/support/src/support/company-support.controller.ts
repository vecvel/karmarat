/**
 * What a company can do with its own support tickets.
 *
 * Every handler takes the tenant from `@CompanyId()` — the gateway-verified
 * principal — and hands it to the service as the first argument. No route reads
 * a company id from a path, a body or a query, and there is no parameter on
 * this controller through which one could be supplied.
 *
 * The thread these routes return comes from views that exclude the desk's
 * internal notes in SQL. Nothing on this side can reach the base table.
 */
import {
  Body,
  Controller,
  Get,
  Headers,
  HttpCode,
  Param,
  Post,
  Query,
} from '@nestjs/common';
import {
  CompanyId,
  CurrentPrincipal,
  Principal,
  RequirePermission,
  RequirePrincipalType,
  badRequest,
  forbidden,
  type Page,
} from '@karmarat/nestshared';

import type { PresignedUpload } from '../common/storage';
import { CompanySupportService, type Actor } from './company-support.service';
// Imported as values, not types: the ValidationPipe reads the DTO class from
// the parameter's emitted metadata, and a type-only import erases to nothing.
import { CreateTicketDto, ListTicketsQueryDto, ReplyDto, UploadUrlDto } from './dto';
import type { CompanyTicketView, TicketSummaryView } from './serializer';

/**
 * Ticket ids are minted by this service and are always `tkt_` plus 26 base32
 * characters. Rejecting anything else keeps a hostile path parameter from
 * reaching a query at all, and costs a client nothing it could legitimately do.
 */
const TICKET_ID = /^tkt_[0-9A-HJKMNP-TV-Z]{26}$/;

function ticketId(raw: string): string {
  if (!TICKET_ID.test(raw)) {
    throw badRequest('That is not a valid ticket id.');
  }
  return raw;
}

/** Trimmed and bounded: the header is echoed into a unique index. */
function idempotencyKey(raw: string | undefined): string | null {
  const key = (raw ?? '').trim();
  if (key === '') return null;
  if (key.length > 200) {
    throw badRequest('Idempotency-Key must be 200 characters or fewer.');
  }
  return key;
}

@Controller('v1/support')
@RequirePrincipalType('company')
export class CompanySupportController {
  constructor(private readonly support: CompanySupportService) {}

  @Post('tickets')
  @RequirePermission('support.create')
  async create(
    @CompanyId() companyId: string,
    @CurrentPrincipal() principal: Principal,
    @Body() dto: CreateTicketDto,
    @Headers('idempotency-key') key?: string,
  ): Promise<CompanyTicketView> {
    return this.support.create(companyId, actorOf(principal), dto, idempotencyKey(key));
  }

  @Get('tickets')
  @RequirePermission('support.read')
  async list(
    @CompanyId() companyId: string,
    @Query() query: ListTicketsQueryDto,
  ): Promise<Page<TicketSummaryView>> {
    return this.support.list(companyId, query);
  }

  @Get('tickets/:id')
  @RequirePermission('support.read')
  async get(@CompanyId() companyId: string, @Param('id') id: string): Promise<CompanyTicketView> {
    return this.support.get(companyId, ticketId(id));
  }

  // 200 rather than Nest's default 201 for a POST: a reply creates a message but
  // the resource the client addressed is the ticket, and the ticket is what
  // comes back.
  @Post('tickets/:id/reply')
  @HttpCode(200)
  @RequirePermission('support.reply')
  async reply(
    @CompanyId() companyId: string,
    @CurrentPrincipal() principal: Principal,
    @Param('id') id: string,
    @Body() dto: ReplyDto,
    @Headers('idempotency-key') key?: string,
  ): Promise<CompanyTicketView> {
    return this.support.reply(
      companyId,
      actorOf(principal),
      ticketId(id),
      dto,
      idempotencyKey(key),
    );
  }

  @Post('tickets/:id/close')
  @HttpCode(200)
  @RequirePermission('support.reply')
  async close(@CompanyId() companyId: string, @Param('id') id: string): Promise<CompanyTicketView> {
    return this.support.close(companyId, ticketId(id));
  }

  /**
   * Signs one upload into this tenant's own prefix.
   *
   * The permission check is written out rather than declared with
   * `@RequirePermission`, because that decorator requires *every* key it is
   * given and the honest rule here is a disjunction: an upload is only ever
   * consumed by a create or a reply, and a member who holds either one needs to
   * be able to attach a file. Demanding both would lock out a replier who
   * cannot open tickets; demanding only one would hand a signed URL to a member
   * who holds the other.
   */
  @Post('attachments/upload-url')
  @HttpCode(200)
  async uploadUrl(
    @CompanyId() companyId: string,
    @CurrentPrincipal() principal: Principal,
    @Body() dto: UploadUrlDto,
  ): Promise<PresignedUpload> {
    if (!principal.can('support.create') && !principal.can('support.reply')) {
      throw forbidden('You do not have permission to attach files to a support ticket.');
    }
    return this.support.uploadUrl(companyId, dto);
  }
}

/**
 * The author of a request.
 *
 * Read from the principal the gateway set, so a client cannot write a ticket
 * under somebody else's name or move its slug.
 */
function actorOf(principal: Principal): Actor {
  return {
    accountId: principal.subject,
    email: principal.email,
    companySlug: principal.companySlug,
  };
}
