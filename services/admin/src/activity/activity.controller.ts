/**
 * The platform-wide audit feed and its export.
 *
 * Reading and exporting are separate permissions because they are separate
 * risks: reading the feed shows an operator what happened, exporting it puts
 * every tenant's activity into a file that leaves the platform. The registry
 * already draws that line (`platform_audit.read` / `platform_audit.export`) and
 * this service honours it rather than treating export as "read, but with a
 * different content type".
 */
import { Controller, Get, Header, Logger, Query, Res } from '@nestjs/common';
import { CurrentPrincipal, Principal, parsePageRequest } from '@karmarat/nestshared';
import type { Response } from 'express';

import { PlatformOnly, assertPlatform } from '../common/platform';
import { optionalText, optionalTimestamp, optionalUuid } from '../common/query';
import type { ActivityFilters, ActivityRow } from './activity.repository';
import { ActivityService } from './activity.service';

interface FeedQuery {
  domain?: string;
  action?: string;
  company?: string;
  from?: string;
  to?: string;
  limit?: string;
  cursor?: string;
}

@Controller('v1/admin/activity')
@PlatformOnly('platform_audit.read')
export class ActivityController {
  constructor(private readonly activity: ActivityService) {}

  @Get()
  async feed(@CurrentPrincipal() principal: Principal, @Query() query: FeedQuery): Promise<Record<string, unknown>> {
    assertPlatform(principal);

    const page = parsePageRequest({
      ...(query.limit !== undefined ? { limit: query.limit } : {}),
      ...(query.cursor !== undefined ? { cursor: query.cursor } : {}),
    });

    const result = await this.activity.feed(toFilters(query), page.limit, page.cursor);
    return { items: result.items.map(toActivityView), nextCursor: result.nextCursor };
  }

  /** The domains present in the feed, so a console filter offers real choices. */
  @Get('domains')
  async domains(@CurrentPrincipal() principal: Principal): Promise<Record<string, unknown>> {
    assertPlatform(principal);
    return { items: await this.activity.domains() };
  }

  /**
   * The same feed as a file.
   *
   * `platform_audit.export`, not `.read`. The route-level decorator overrides
   * the controller's, so this handler requires the export permission and not
   * both — a reviewer reading the two lines together should not have to work
   * out which one wins.
   */
  @Get('export')
  @PlatformOnly('platform_audit.export')
  @Header('Content-Type', 'text/csv; charset=utf-8')
  async exportCsv(
    @CurrentPrincipal() principal: Principal,
    @Query() query: FeedQuery,
    @Res({ passthrough: true }) response: Response,
  ): Promise<string> {
    assertPlatform(principal);

    const result = await this.activity.exportCsv(toFilters(query));

    const stamp = new Date().toISOString().replace(/[:.]/g, '-');
    response.setHeader('Content-Disposition', `attachment; filename="platform-activity-${stamp}.csv"`);
    // A CSV body has nowhere to say "this is not all of it" without corrupting
    // the file for every parser, so the truncation flag rides in a header.
    response.setHeader('X-Export-Rows', String(result.rows));
    response.setHeader('X-Export-Truncated', String(result.truncated));

    if (result.truncated) {
      new Logger(ActivityController.name).warn(
        `audit export hit the row ceiling (${result.rows}); narrow the filters to export the rest`,
      );
    }

    return result.csv;
  }
}

function toFilters(query: FeedQuery): ActivityFilters {
  const domain = optionalText(query.domain, 'domain', 64);
  const action = optionalText(query.action, 'action', 64);
  const companyId = optionalUuid(query.company, 'company');
  const from = optionalTimestamp(query.from, 'from');
  const to = optionalTimestamp(query.to, 'to');

  return {
    ...(domain ? { domain } : {}),
    ...(action ? { action } : {}),
    ...(companyId ? { companyId } : {}),
    ...(from ? { from } : {}),
    ...(to ? { to } : {}),
  };
}

export function toActivityView(row: ActivityRow): Record<string, unknown> {
  return {
    id: row.eventId,
    subject: row.subject,
    domain: row.domain,
    action: row.action,
    companyId: row.companyId,
    actorId: row.actorId,
    correlationId: row.correlationId,
    occurredAt: row.occurredAt.toISOString(),
    payload: row.payload,
  };
}
