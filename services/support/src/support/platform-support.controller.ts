/**
 * The support desk.
 *
 * These routes address a ticket by id with no tenant predicate anywhere behind
 * them, which is safe for exactly one reason: they are platform-principal only
 * and carry `platform_support.*` keys. A platform agent has no tenant of their
 * own, so there is no boundary for a path parameter to cross — and a company
 * role that somehow held one of these keys still cannot reach here, because the
 * principal type is checked first.
 *
 * That is why the desk's permissions are a separate scope from `support.*`
 * rather than the same keys read more widely: the query cannot defend itself,
 * so the authorization has to.
 */
import { Body, Controller, Get, Headers, HttpCode, Param, Patch, Post, Query } from '@nestjs/common';
import {
  CurrentPrincipal,
  Principal,
  RequirePermission,
  RequirePrincipalType,
  badRequest,
  type Page,
} from '@karmarat/nestshared';

import { AssignTicketDto, PatchTicketDto, PlatformListTicketsQueryDto, PlatformReplyDto } from './dto';
import { PlatformSupportService, type Agent } from './platform-support.service';
import type { PlatformTicketSummaryView, PlatformTicketView } from './serializer';

const TICKET_ID = /^tkt_[0-9A-HJKMNP-TV-Z]{26}$/;

function ticketId(raw: string): string {
  if (!TICKET_ID.test(raw)) {
    throw badRequest('That is not a valid ticket id.');
  }
  return raw;
}

function idempotencyKey(raw: string | undefined): string | null {
  const key = (raw ?? '').trim();
  if (key === '') return null;
  if (key.length > 200) {
    throw badRequest('Idempotency-Key must be 200 characters or fewer.');
  }
  return key;
}

@Controller('v1/platform/support')
@RequirePrincipalType('platform')
export class PlatformSupportController {
  constructor(private readonly support: PlatformSupportService) {}

  @Get('tickets')
  @RequirePermission('platform_support.read')
  async list(@Query() query: PlatformListTicketsQueryDto): Promise<Page<PlatformTicketSummaryView>> {
    return this.support.list(query);
  }

  @Get('tickets/:id')
  @RequirePermission('platform_support.read')
  async get(@Param('id') id: string): Promise<PlatformTicketView> {
    return this.support.get(ticketId(id));
  }

  @Post('tickets/:id/reply')
  @HttpCode(200)
  @RequirePermission('platform_support.reply')
  async reply(
    @CurrentPrincipal() principal: Principal,
    @Param('id') id: string,
    @Body() dto: PlatformReplyDto,
    @Headers('idempotency-key') key?: string,
  ): Promise<PlatformTicketView> {
    return this.support.reply(agentOf(principal), ticketId(id), dto, idempotencyKey(key));
  }

  @Post('tickets/:id/assign')
  @HttpCode(200)
  @RequirePermission('platform_support.assign')
  async assign(@Param('id') id: string, @Body() dto: AssignTicketDto): Promise<PlatformTicketView> {
    return this.support.assign(ticketId(id), dto);
  }

  @Post('tickets/:id/close')
  @HttpCode(200)
  @RequirePermission('platform_support.close')
  async close(@Param('id') id: string): Promise<PlatformTicketView> {
    return this.support.close(ticketId(id));
  }

  /**
   * Triage.
   *
   * Reusing `platform_support.reply` would let anyone who can answer a customer
   * also silently reclassify and reprioritise the queue, so this carries the
   * assign key — the one that already means "move work around the desk".
   */
  @Patch('tickets/:id')
  @RequirePermission('platform_support.assign')
  async patch(@Param('id') id: string, @Body() dto: PatchTicketDto): Promise<PlatformTicketView> {
    return this.support.patch(ticketId(id), dto);
  }
}

function agentOf(principal: Principal): Agent {
  return { accountId: principal.subject, email: principal.email };
}
