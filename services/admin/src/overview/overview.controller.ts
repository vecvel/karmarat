/**
 * The platform dashboard.
 *
 * `platform_companies.read` is the gate. It is the permission every console
 * operator has — the dashboard is the console's front door — and requiring a
 * billing permission on top would 403 a support agent out of the whole page
 * because one card on it shows revenue.
 *
 * The revenue card is instead redacted per principal: an operator without
 * `subscriptions.read` gets the page without it. Withholding a field is a
 * better answer than withholding a page, and it keeps the permission keys
 * meaning what they say rather than becoming a de-facto page-level role.
 */
import { Controller, Get, Query } from '@nestjs/common';
import { CurrentPrincipal, Principal, badRequest } from '@karmarat/nestshared';

import { PlatformOnly, assertPlatform } from '../common/platform';
import { DEFAULT_SIGNUP_DAYS, OverviewService, type OverviewSnapshot } from './overview.service';

/** The permission that unlocks every money-bearing figure in this service. */
export const BILLING_PERMISSION = 'subscriptions.read';

@Controller('v1/admin/overview')
@PlatformOnly('platform_companies.read')
export class OverviewController {
  constructor(private readonly overview: OverviewService) {}

  @Get()
  async get(
    @CurrentPrincipal() principal: Principal,
    @Query('signupDays') signupDays?: string,
  ): Promise<Record<string, unknown>> {
    assertPlatform(principal);

    const snapshot = await this.overview.snapshot(parseDays(signupDays));
    return redact(snapshot, principal.can(BILLING_PERMISSION));
  }
}

function parseDays(raw?: string): number {
  if (raw === undefined || raw === '') return DEFAULT_SIGNUP_DAYS;
  const parsed = Number(raw);
  if (!Number.isInteger(parsed) || parsed < 1) {
    throw badRequest('signupDays must be a positive integer.');
  }
  return parsed;
}

/**
 * Drops the revenue block for a principal that may not see money.
 *
 * `null` rather than an absent key, so a client can tell "you may not see this"
 * apart from "this version of the API has no such field".
 */
function redact(snapshot: OverviewSnapshot, maySeeBilling: boolean): Record<string, unknown> {
  if (maySeeBilling) return { ...snapshot };
  const { mrr: _mrr, subscriptions: _subscriptions, ...rest } = snapshot;
  return { ...rest, mrr: null, subscriptions: null };
}
