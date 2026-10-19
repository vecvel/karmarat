/**
 * The three faces of a subscription.
 *
 * Platform staff administer them, a company sees only its own, and other
 * services ask a narrow entitlement question over the internal token. They are
 * separate controllers rather than one with branching, because the difference
 * between them is who may call — and that is clearest when it is the route.
 */
import {
  Body,
  Controller,
  Delete,
  Get,
  Param,
  Patch,
  Post,
  Query,
  UseGuards,
} from '@nestjs/common';
import {
  CompanyId,
  CurrentPrincipal,
  InternalTokenGuard,
  Principal,
  Public,
  RequirePermission,
  RequirePrincipalType,
  badRequest,
  notFound,
  parsePageRequest,
  validationFailed,
} from '@karmarat/nestshared';

import { parseEntitlements } from '../entitlements/entitlements';
import { PlansService } from '../plans/plans.service';
import {
  SUBSCRIPTION_STATES,
  type Subscription,
  type SubscriptionState,
} from './subscription.entity';
import { SubscriptionsService } from './subscriptions.service';

/** Everything a subscription row exposes. Platform staff see all of it. */
function toView(subscription: Subscription): Record<string, unknown> {
  return {
    id: subscription.id,
    companyId: subscription.companyId,
    planId: subscription.planId,
    state: subscription.state,
    startedAt: subscription.startedAt,
    currentPeriodStart: subscription.currentPeriodStart,
    currentPeriodEnd: subscription.currentPeriodEnd,
    expiresAt: subscription.expiresAt,
    trialEndsAt: subscription.trialEndsAt,
    cancelAtPeriodEnd: subscription.cancelAtPeriodEnd,
    cancelledAt: subscription.cancelledAt,
    entitlements: subscription.entitlements,
    price: {
      amount: subscription.priceAmount,
      currency: subscription.priceCurrency,
      interval: subscription.planInterval,
      intervalCount: subscription.planIntervalCount,
    },
    createdAt: subscription.createdAt,
    updatedAt: subscription.updatedAt,
  };
}

function parseState(raw: unknown, field: string): SubscriptionState {
  if (typeof raw !== 'string' || !SUBSCRIPTION_STATES.includes(raw as SubscriptionState)) {
    throw validationFailed({
      [field]: [`Must be one of: ${SUBSCRIPTION_STATES.join(', ')}.`],
    });
  }
  return raw as SubscriptionState;
}

/* -------------------------------------------------------------------------- */
/* Platform                                                                   */
/* -------------------------------------------------------------------------- */

@Controller('v1/subscriptions')
@RequirePrincipalType('platform')
export class SubscriptionsController {
  constructor(private readonly subscriptions: SubscriptionsService) {}

  @Get()
  @RequirePermission('subscriptions.read')
  async list(
    @Query('companyId') companyId?: string,
    @Query('state') state?: string,
    @Query('limit') limit?: string,
    @Query('cursor') cursor?: string,
  ): Promise<Record<string, unknown>> {
    const page = parsePageRequest({ limit, cursor });

    const result = await this.subscriptions.list({
      ...page,
      ...(companyId ? { companyId } : {}),
      ...(state ? { states: [parseState(state, 'state')] } : {}),
    });

    return { data: result.items.map(toView), nextCursor: result.nextCursor };
  }

  @Get(':id')
  @RequirePermission('subscriptions.read')
  async get(@Param('id') id: string): Promise<Record<string, unknown>> {
    return toView(await this.subscriptions.get(id));
  }

  @Post()
  @RequirePermission('subscriptions.create')
  async create(
    @Body() body: { companyId?: string; planId?: string; state?: string; startTrial?: boolean },
  ): Promise<Record<string, unknown>> {
    const fields: Record<string, string[]> = {};
    if (!body.companyId?.trim()) fields['companyId'] = ['A company is required.'];
    if (!body.planId?.trim()) fields['planId'] = ['A plan is required.'];
    if (Object.keys(fields).length) throw validationFailed(fields);

    const created = await this.subscriptions.create({
      companyId: body.companyId!.trim(),
      planId: body.planId!.trim(),
      ...(body.state ? { state: parseState(body.state, 'state') } : {}),
      ...(body.startTrial === true ? { startTrial: true } : {}),
    });

    return toView(created);
  }

  @Patch(':id')
  @RequirePermission('subscriptions.update')
  async update(
    @Param('id') id: string,
    @Body() body: { state?: string; expiresAt?: string | null; cancelAtPeriodEnd?: boolean },
  ): Promise<Record<string, unknown>> {
    return toView(
      await this.subscriptions.update(id, {
        ...(body.state ? { state: parseState(body.state, 'state') } : {}),
        ...(body.expiresAt !== undefined ? { expiresAt: parseDate(body.expiresAt) } : {}),
        ...(body.cancelAtPeriodEnd !== undefined
          ? { cancelAtPeriodEnd: body.cancelAtPeriodEnd }
          : {}),
      }),
    );
  }

  @Post(':id/override')
  @RequirePermission('subscriptions.override')
  async override(
    @Param('id') id: string,
    @Body()
    body: { state?: string; expiresAt?: string | null; entitlements?: unknown; reason?: string },
    @CurrentPrincipal() principal: Principal,
  ): Promise<Record<string, unknown>> {
    return toView(
      await this.subscriptions.override(id, {
        ...(body.state ? { state: parseState(body.state, 'state') } : {}),
        ...(body.expiresAt !== undefined ? { expiresAt: parseDate(body.expiresAt) } : {}),
        ...(body.entitlements ? { entitlements: parseEntitlements(body.entitlements) } : {}),
        reason: body.reason ?? '',
        actorId: principal.subject,
      }),
    );
  }

  @Delete(':id')
  @RequirePermission('subscriptions.delete')
  async remove(@Param('id') id: string): Promise<void> {
    await this.subscriptions.delete(id);
  }
}

/* -------------------------------------------------------------------------- */
/* The company's own billing                                                  */
/* -------------------------------------------------------------------------- */

@Controller('v1/billing')
@RequirePrincipalType('company')
export class BillingController {
  constructor(
    private readonly subscriptions: SubscriptionsService,
    private readonly plans: PlansService,
  ) {}

  @Get()
  @RequirePermission('billing.read')
  async current(@CompanyId() companyId: string): Promise<Record<string, unknown>> {
    const subscription = await this.subscriptions.currentFor(companyId);
    const entitlements = await this.subscriptions.entitlementsFor(companyId);

    return {
      // A company with no subscription is a normal state, not a 404: the
      // billing page has to render something for a tenant that has not paid.
      subscription: subscription ? toView(subscription) : null,
      entitlements: entitlements.entitlements,
      state: entitlements.state,
      live: entitlements.live,
    };
  }

  @Post('subscribe')
  @RequirePermission('billing.manage')
  async subscribe(
    @CompanyId() companyId: string,
    @Body() body: { planId?: string; idempotencyKey?: string },
  ): Promise<Record<string, unknown>> {
    if (!body.planId?.trim()) {
      throw validationFailed({ planId: ['A plan is required.'] });
    }

    // The company id comes from the verified principal, never the body: a
    // recruiter must not be able to buy a plan for somebody else's tenant.
    const created = await this.subscriptions.create({
      companyId,
      planId: body.planId.trim(),
      // Pending, not active. Activation is the payments service's word, once a
      // provider has actually taken the money.
      state: 'pending',
      ...(body.idempotencyKey ? { idempotencyKey: body.idempotencyKey } : {}),
    });

    return { subscription: toView(created), checkoutRequired: true };
  }

  @Post('cancel')
  @RequirePermission('billing.manage')
  async cancel(
    @CompanyId() companyId: string,
    @Body() body: { immediate?: boolean },
  ): Promise<Record<string, unknown>> {
    const subscription = await this.subscriptions.currentFor(companyId);
    if (!subscription) {
      throw notFound('This company has no subscription to cancel.');
    }

    return toView(await this.subscriptions.cancel(subscription.id, body.immediate === true));
  }
}

/* -------------------------------------------------------------------------- */
/* Other services                                                             */
/* -------------------------------------------------------------------------- */

// @Public() is required, not optional: AuthorizationGuard is global and runs
// before route guards, so without it this route is refused for having no
// principal before InternalTokenGuard ever checks the secret. "Public" here
// means "outside the gateway's principal model" — the shared secret is what
// actually guards it.
@Controller('internal')
@Public()
@UseGuards(InternalTokenGuard)
export class InternalSubscriptionsController {
  constructor(private readonly subscriptions: SubscriptionsService) {}

  /**
   * Called by payments when a provider confirms a payment.
   *
   * Guarded by the shared secret rather than a principal: the caller is a
   * service, and the customer whose subscription this activates is not the one
   * making the request.
   */
  @Post('subscriptions/activate')
  async activate(
    @Body() body: { companyId?: string; planId?: string; paymentId?: string },
  ): Promise<Record<string, unknown>> {
    const fields: Record<string, string[]> = {};
    const companyId = (body.companyId ?? '').trim();
    const planId = (body.planId ?? '').trim();
    const paymentId = (body.paymentId ?? '').trim();

    if (!companyId) fields['companyId'] = ['A company id is required.'];
    else if (!UUID_PATTERN.test(companyId)) fields['companyId'] = ['A company id must be a UUID.'];
    if (!planId) fields['planId'] = ['A plan is required.'];
    if (!paymentId) fields['paymentId'] = ['A payment id is required.'];
    if (Object.keys(fields).length) throw validationFailed(fields);

    const subscription = await this.subscriptions.activateForPayment({ companyId, planId, paymentId });
    return { subscriptionId: subscription.id, state: subscription.state };
  }

  @Get('entitlements/:companyId')
  async entitlements(@Param('companyId') companyId: string): Promise<Record<string, unknown>> {
    const id = companyId.trim();
    if (!id) {
      throw badRequest('A company id is required.');
    }
    // company_id is a uuid column, so a malformed id reaches Postgres as a cast
    // error and surfaces as an opaque 500. A caller that sent the wrong thing
    // deserves to be told which thing was wrong.
    if (!UUID_PATTERN.test(id)) {
      throw badRequest('A company id must be a UUID.');
    }
    return this.subscriptions.entitlementsFor(id);
  }
}

const UUID_PATTERN = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

function parseDate(raw: string | null): Date | null {
  if (raw === null || raw === '') return null;
  const parsed = new Date(raw);
  if (Number.isNaN(parsed.getTime())) {
    throw validationFailed({ expiresAt: ['Use an RFC 3339 timestamp, or null for no expiry.'] });
  }
  return parsed;
}
