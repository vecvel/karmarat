/**
 * Subscription lifecycle.
 *
 * The gateway refuses every company route when a tenant has no live
 * entitlement, so what this service decides is literally whether a customer's
 * portal opens. Two rules are therefore enforced here rather than left to
 * callers:
 *
 *   - entitlements are snapshotted at purchase, so editing a plan never changes
 *     what an existing customer already bought;
 *   - a company has at most one live subscription, enforced by a partial unique
 *     index rather than a check, because two concurrent activations would both
 *     pass a read-then-write.
 */
import { Inject, Injectable, Logger } from '@nestjs/common';
import {
  EventBus,
  Subject,
  conflict,
  notFound,
  validationFailed,
  withTransaction,
  type Page,
} from '@karmarat/nestshared';
import { Pool } from 'pg';

import { EVENT_BUS, PG_POOL } from '../common/infrastructure.module';
import { CONFIG, type SubscriptionsConfig } from '../config';
import { addDays, periodEnd } from '../entitlements/duration';
import { NO_ENTITLEMENTS, type Entitlements } from '../entitlements/entitlements';
import { PlansRepository } from '../plans/plans.repository';
import type { Subscription, SubscriptionState } from './subscription.entity';
import {
  SubscriptionsRepository,
  type ListSubscriptionsFilter,
  type SubscriptionPatch,
} from './subscriptions.repository';

/** States that keep a portal open. */
const LIVE_STATES: readonly SubscriptionState[] = ['trialing', 'active', 'past_due'];

export interface CreateSubscriptionCommand {
  companyId: string;
  planId: string;
  state?: SubscriptionState;
  startTrial?: boolean;
  idempotencyKey?: string;
}

export interface OverrideCommand {
  state?: SubscriptionState;
  expiresAt?: Date | null;
  entitlements?: Entitlements;
  reason: string;
  actorId: string;
}

@Injectable()
export class SubscriptionsService {
  private readonly logger = new Logger(SubscriptionsService.name);

  constructor(
    private readonly subscriptions: SubscriptionsRepository,
    private readonly plans: PlansRepository,
    @Inject(PG_POOL) private readonly pool: Pool,
    @Inject(EVENT_BUS) private readonly bus: EventBus,
    @Inject(CONFIG) private readonly config: SubscriptionsConfig,
  ) {}

  /**
   * Starts a subscription for a company.
   *
   * The plan's price, interval and entitlements are copied onto the row. The
   * plan is a catalogue entry that the platform will edit; the subscription is
   * what the customer agreed to, and those must be allowed to diverge.
   */
  async create(command: CreateSubscriptionCommand): Promise<Subscription> {
    const plan = await this.plans.findById(command.planId);
    if (!plan) {
      throw validationFailed({ planId: ['That plan does not exist.'] });
    }
    if (!plan.publishedAt || plan.retiredAt) {
      throw validationFailed({
        planId: ['That plan is not available for new subscriptions.'],
      });
    }

    if (command.idempotencyKey) {
      // A retried checkout must not create a second subscription; the caller's
      // key is what lets us recognize the repeat.
      const existing = await this.subscriptions.findByIdempotencyKey(
        command.companyId,
        command.idempotencyKey,
      );
      if (existing) {
        return existing;
      }
    }

    const live = await this.subscriptions.findLiveForCompany(command.companyId);
    if (live) {
      throw conflict(
        'subscription_exists',
        'This company already has a live subscription. Cancel or change the existing one instead.',
      );
    }

    const now = new Date();
    const trialing = command.startTrial === true && plan.trialDays > 0;
    const state: SubscriptionState = command.state ?? (trialing ? 'trialing' : 'active');

    const periodStart = now;
    const end = periodEnd(periodStart, plan.interval, plan.intervalCount);

    const created = await this.subscriptions.create({
      companyId: command.companyId,
      planId: plan.id,
      state,
      startedAt: now,
      currentPeriodStart: periodStart,
      currentPeriodEnd: end,
      // A lifetime plan has no expiry at all, which is why periodEnd returns
      // null for it rather than a date far in the future — a sentinel date
      // eventually arrives.
      expiresAt: end,
      trialEndsAt: trialing ? addDays(now, plan.trialDays) : null,
      entitlements: plan.entitlements,
      priceAmount: plan.priceAmount,
      priceCurrency: plan.priceCurrency,
      planInterval: plan.interval,
      planIntervalCount: plan.intervalCount,
      idempotencyKey: command.idempotencyKey ?? null,
    });

    await this.announce(created, Subject.SubscriptionActivated);
    return created;
  }

  /**
   * Activates the subscription a settled payment paid for.
   *
   * Called by payments the moment a provider confirms, so the portal opens
   * while the customer is still looking at it rather than whenever a consumer
   * gets round to the event.
   *
   * Idempotent on purpose: a provider retries webhooks, and payments calls this
   * on every delivery it has not already applied. A company that is already
   * live on this plan gets its existing subscription back rather than a second
   * one or a conflict.
   */
  async activateForPayment(input: {
    companyId: string;
    planId: string;
    paymentId: string;
  }): Promise<Subscription> {
    const live = await this.subscriptions.findLiveForCompany(input.companyId);

    if (live) {
      // Already on the plan that was paid for: the webhook is a redelivery, or
      // the company-side subscribe already ran. Nothing to change.
      if (live.planId === input.planId && live.state === 'active') {
        return live;
      }

      // A pending row is the one POST /v1/billing/subscribe created while the
      // customer was sent to the provider. Settling the payment is what turns
      // it live.
      const plan = await this.plans.findById(input.planId);
      if (!plan) {
        throw validationFailed({ planId: ['That plan does not exist.'] });
      }

      const now = new Date();
      const end = periodEnd(now, plan.interval, plan.intervalCount);

      const activated = await this.subscriptions.update(live.id, {
        planId: plan.id,
        state: 'active',
        startedAt: live.startedAt ?? now,
        currentPeriodStart: now,
        currentPeriodEnd: end,
        expiresAt: end,
        // Re-snapshot: the customer is paying for the plan as it stands now,
        // and the pending row may have been created against an older version.
        entitlements: plan.entitlements,
        priceAmount: plan.priceAmount,
        priceCurrency: plan.priceCurrency,
        planInterval: plan.interval,
        planIntervalCount: plan.intervalCount,
      });
      if (!activated) {
        throw notFound('That subscription no longer exists.');
      }

      this.logger.log(
        `activated subscription ${activated.id} for ${input.companyId} on payment ${input.paymentId}`,
      );
      await this.announce(activated, Subject.SubscriptionActivated);
      return activated;
    }

    // No row at all — a checkout that bypassed /v1/billing/subscribe, or one
    // whose pending row was swept. The payment settled either way, so the
    // company gets what it paid for.
    const created = await this.create({
      companyId: input.companyId,
      planId: input.planId,
      state: 'active',
      idempotencyKey: `payment:${input.paymentId}`,
    });

    this.logger.log(
      `created and activated subscription ${created.id} for ${input.companyId} on payment ${input.paymentId}`,
    );
    return created;
  }

  async get(id: string): Promise<Subscription> {
    const subscription = await this.subscriptions.findById(id);
    if (!subscription) {
      throw notFound('That subscription does not exist.');
    }
    return subscription;
  }

  async list(filter: ListSubscriptionsFilter): Promise<Page<Subscription>> {
    return this.subscriptions.list(filter);
  }

  async update(id: string, patch: SubscriptionPatch): Promise<Subscription> {
    const updated = await this.subscriptions.update(id, patch);
    if (!updated) {
      throw notFound('That subscription does not exist.');
    }

    await this.announce(updated, subjectForState(updated.state));
    return updated;
  }

  /**
   * A platform grant or extension, applied by hand.
   *
   * Recorded with who did it and why: this is the one path that can give a
   * company entitlements it never paid for, so it needs to be answerable later.
   */
  async override(id: string, command: OverrideCommand): Promise<Subscription> {
    if (!command.reason.trim()) {
      throw validationFailed({ reason: ['A reason is required for an override.'] });
    }

    const patch: SubscriptionPatch = {};
    if (command.state) patch.state = command.state;
    if (command.expiresAt !== undefined) patch.expiresAt = command.expiresAt;
    if (command.entitlements) patch.entitlements = command.entitlements;

    const updated = await this.subscriptions.update(id, patch);
    if (!updated) {
      throw notFound('That subscription does not exist.');
    }

    this.logger.warn(
      `subscription ${id} overridden by ${command.actorId}: ${command.reason.trim()}`,
    );

    await this.announce(updated, subjectForState(updated.state), command.actorId);
    return updated;
  }

  /** Cancels at period end by default; immediately when asked. */
  async cancel(id: string, immediate: boolean): Promise<Subscription> {
    const subscription = await this.get(id);

    const patch: SubscriptionPatch = immediate
      ? { state: 'cancelled', cancelledAt: new Date(), cancelAtPeriodEnd: false }
      : { cancelAtPeriodEnd: true };

    const updated = await this.subscriptions.update(subscription.id, patch);
    if (!updated) {
      throw notFound('That subscription does not exist.');
    }

    // Only an immediate cancellation closes the portal now. A cancel-at-period-
    // end leaves the tenant live until the sweep reaches it, which is what the
    // customer paid for.
    if (immediate) {
      await this.announce(updated, Subject.SubscriptionCancelled);
    }

    return updated;
  }

  async delete(id: string): Promise<void> {
    if (!(await this.subscriptions.delete(id))) {
      throw notFound('That subscription does not exist.');
    }
  }

  /** The company's own view of what it is entitled to. */
  async currentFor(companyId: string): Promise<Subscription | null> {
    return this.subscriptions.findCurrentForCompany(companyId);
  }

  /**
   * What the gateway and other services ask: may this tenant act?
   *
   * A company with no subscription gets the empty entitlement set rather than
   * an error — "not entitled" is a legitimate answer, and making callers handle
   * a 404 for it invites them to treat a lookup failure as permission.
   */
  async entitlementsFor(companyId: string): Promise<{
    state: SubscriptionState | 'none';
    expiresAt: Date | null;
    entitlements: Entitlements;
    live: boolean;
  }> {
    const subscription = await this.subscriptions.findLiveForCompany(companyId);
    if (!subscription) {
      return { state: 'none', expiresAt: null, entitlements: NO_ENTITLEMENTS, live: false };
    }

    return {
      state: subscription.state,
      expiresAt: subscription.expiresAt,
      entitlements: subscription.entitlements,
      live: LIVE_STATES.includes(subscription.state),
    };
  }

  /**
   * Expires everything past its date and announces each one.
   *
   * Runs on a timer in every replica, so it takes a transaction-scoped advisory
   * lock: several instances sweeping at once would each claim rows and publish
   * the same expiry twice.
   */
  async sweepExpired(now = new Date()): Promise<number> {
    return withTransaction(this.pool, async (client) => {
      const { rows } = await client.query<{ locked: boolean }>(
        'SELECT pg_try_advisory_xact_lock($1) AS locked',
        [SWEEP_LOCK_ID],
      );
      if (!rows[0]?.locked) {
        return 0;
      }

      const expired = await this.subscriptions.claimExpired(now, SWEEP_BATCH, client);
      for (const subscription of expired) {
        await this.announce(subscription, Subject.SubscriptionExpired);
      }

      // A checkout that was started and abandoned leaves a pending row holding
      // the company's one-live-subscription slot; clear those too.
      const stale = await this.subscriptions.expireStalePending(
        addDays(now, -this.config.pastDueGraceDays),
        client,
      );
      if (stale > 0) {
        this.logger.log(`cleared ${stale} abandoned pending subscription(s)`);
      }

      if (expired.length > 0) {
        this.logger.log(`expired ${expired.length} subscription(s)`);
      }
      return expired.length;
    });
  }

  /**
   * Publishes a subscription's current state.
   *
   * Identity consumes these to open and close portals, so a failure to publish
   * would leave a paid customer locked out. It is logged rather than thrown:
   * the write has already committed and failing the caller's request would ask
   * them to repeat a change that took effect.
   */
  private async announce(
    subscription: Subscription,
    subject: string,
    actorId?: string,
  ): Promise<void> {
    try {
      await this.bus.publish(
        subject,
        {
          subscriptionId: subscription.id,
          companyId: subscription.companyId,
          planId: subscription.planId,
          state: subscription.state,
          expiresAt: subscription.expiresAt,
          entitlements: subscription.entitlements,
        },
        { companyId: subscription.companyId, ...(actorId ? { actorId } : {}) },
      );
    } catch (error) {
      this.logger.error(
        `could not publish ${subject} for ${subscription.id}: ${(error as Error).message}`,
      );
    }
  }
}

/** Distinct from the migration lock so a sweep never blocks a deploy. */
const SWEEP_LOCK_ID = 4_827_113_906;
const SWEEP_BATCH = 500;

function subjectForState(state: SubscriptionState): string {
  switch (state) {
    case 'active':
      return Subject.SubscriptionActivated;
    case 'expired':
      return Subject.SubscriptionExpired;
    case 'cancelled':
      return Subject.SubscriptionCancelled;
    default:
      // trialing and past_due are still live; identity treats the renewal
      // subject as "here is the current entitlement", which is what it needs.
      return Subject.SubscriptionRenewed;
  }
}
