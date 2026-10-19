/**
 * The tenant list and one tenant's page.
 *
 * `platform_companies.read` gates both. The billing and payment blocks inside
 * the response are gated separately, per principal, by `subscriptions.read` and
 * `payments.read` — see views.ts for why that is a field-level decision rather
 * than a route-level one.
 *
 * The `{id}` in the path is a company id, which everywhere else in this
 * platform would be a cross-tenant read. It is safe here only because the
 * principal is platform staff, who have no tenant of their own: the id selects
 * a subject to read about rather than asserting who the caller is. That is
 * checked twice — see src/common/platform.ts.
 */
import { Controller, Get, Param, Query } from '@nestjs/common';
import { CurrentPrincipal, Principal, parsePageRequest } from '@karmarat/nestshared';

import { toActivityView } from '../activity/activity.controller';
import { PlatformOnly, assertPlatform } from '../common/platform';
import { optionalText, requireUuid } from '../common/query';
import { CompaniesService } from './companies.service';
import type { CompanyFilters } from './companies.repository';
import { toCompanyView, toPaymentRowView, toTicketView, type Visibility } from './views';

interface ListQuery {
  state?: string;
  plan?: string;
  subscriptionState?: string;
  q?: string;
  limit?: string;
  cursor?: string;
}

@Controller('v1/admin/companies')
@PlatformOnly('platform_companies.read')
export class AdminCompaniesController {
  constructor(private readonly companies: CompaniesService) {}

  @Get()
  async list(@CurrentPrincipal() principal: Principal, @Query() query: ListQuery): Promise<Record<string, unknown>> {
    assertPlatform(principal);

    const page = parsePageRequest({
      ...(query.limit !== undefined ? { limit: query.limit } : {}),
      ...(query.cursor !== undefined ? { cursor: query.cursor } : {}),
    });

    const result = await this.companies.list(toFilters(query), page.limit, page.cursor);
    const visibility = visibilityOf(principal);

    return {
      items: result.items.map((row) => toCompanyView(row, visibility)),
      nextCursor: result.nextCursor,
    };
  }

  @Get(':id')
  async get(
    @CurrentPrincipal() principal: Principal,
    @Param('id') id: string,
  ): Promise<Record<string, unknown>> {
    assertPlatform(principal);

    const detail = await this.companies.detail(requireUuid(id, 'id'));
    const visibility = visibilityOf(principal);

    return {
      company: toCompanyView(detail.company, visibility),
      // Withheld rather than empty: an operator who may not see payments should
      // not be left wondering whether this tenant has never paid.
      payments: visibility.payments ? detail.payments.map(toPaymentRowView) : null,
      tickets: detail.tickets.map(toTicketView),
      activity: detail.activity.map(toActivityView),
    };
  }
}

function visibilityOf(principal: Principal): Visibility {
  return {
    billing: principal.can('subscriptions.read'),
    payments: principal.can('payments.read'),
  };
}

function toFilters(query: ListQuery): CompanyFilters {
  const state = optionalText(query.state, 'state', 64);
  const planId = optionalText(query.plan, 'plan', 128);
  const subscriptionState = optionalText(query.subscriptionState, 'subscriptionState', 64);
  const search = optionalText(query.q, 'q', 200);

  return {
    ...(state ? { state } : {}),
    ...(planId ? { planId } : {}),
    ...(subscriptionState ? { subscriptionState } : {}),
    ...(search ? { search } : {}),
  };
}
