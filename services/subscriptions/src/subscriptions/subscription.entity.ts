/**
 * The subscription aggregate.
 *
 * A subscription is the answer to "is this company's portal open, and what may
 * it do while it is". Identity keeps a projection of exactly that, fed by the
 * events this service publishes, so the two must agree on the vocabulary below.
 */
import { Subject } from '@karmarat/nestshared';

import type { Entitlements } from '../entitlements/entitlements';
import type { PlanInterval } from '../entitlements/duration';

/**
 * `pending` is local to this service: a plan has been chosen but payment has
 * not reported success. Identity's projection has no such value, and a pending
 * subscription must not open a portal, so it is never published.
 */
export type SubscriptionState =
  | 'pending'
  | 'trialing'
  | 'active'
  | 'past_due'
  | 'cancelled'
  | 'expired';

export const SUBSCRIPTION_STATES: readonly SubscriptionState[] = [
  'pending',
  'trialing',
  'active',
  'past_due',
  'cancelled',
  'expired',
];

/** States in which the gateway should let a company through. */
const OPEN_STATES: ReadonlySet<SubscriptionState> = new Set(['trialing', 'active', 'past_due']);

/** States that occupy the one-live-subscription-per-company slot. */
const LIVE_STATES: ReadonlySet<SubscriptionState> = new Set([
  'pending',
  'trialing',
  'active',
  'past_due',
]);

export function opensPortal(state: SubscriptionState): boolean {
  return OPEN_STATES.has(state);
}

export function isLive(state: SubscriptionState): boolean {
  return LIVE_STATES.has(state);
}

export interface Subscription {
  id: string;
  companyId: string;
  planId: string;
  state: SubscriptionState;
  startedAt: Date | null;
  currentPeriodStart: Date | null;
  currentPeriodEnd: Date | null;
  /** null means no expiry — a lifetime plan, or a subscription not yet started. */
  expiresAt: Date | null;
  trialEndsAt: Date | null;
  cancelAtPeriodEnd: boolean;
  cancelledAt: Date | null;
  entitlements: Entitlements;
  priceAmount: number;
  priceCurrency: string;
  planInterval: PlanInterval;
  planIntervalCount: number;
  createdAt: Date;
  updatedAt: Date;
}

/**
 * The event subject that carries a state change to the rest of the platform.
 *
 * There is no `subscription.past_due` subject in the shared constants, so a
 * lapse into past_due rides on `SubscriptionActivated` with the real state in
 * the payload. That is not cosmetic: identity's projection reads `payload.state`
 * first and only falls back to inferring from the subject, so the projection
 * lands on past_due either way, and past_due still opens the portal — which is
 * what `activated` is telling a less careful consumer. A dedicated subject
 * would be better and is worth adding to nestshared.
 */
export function subjectForState(state: SubscriptionState, isFirstStart: boolean): string | null {
  switch (state) {
    case 'pending':
      return null; // Nothing outside this service may act on a pending purchase.
    case 'trialing':
    case 'active':
      return isFirstStart ? Subject.SubscriptionActivated : Subject.SubscriptionRenewed;
    case 'past_due':
      return Subject.SubscriptionActivated;
    case 'cancelled':
      return Subject.SubscriptionCancelled;
    case 'expired':
      return Subject.SubscriptionExpired;
  }
}

/**
 * The payload identity's projection consumes.
 *
 * The field names are the contract, not a style choice: see
 * services/identity/internal/projection/projection.go, which decodes
 * companyId, state, expiresAt and entitlements from exactly these names.
 */
export function toSubscriptionEvent(subscription: Subscription) {
  return {
    companyId: subscription.companyId,
    subscriptionId: subscription.id,
    planId: subscription.planId,
    state: subscription.state,
    expiresAt: subscription.expiresAt,
    entitlements: subscription.entitlements,
  };
}

export function toSubscriptionView(subscription: Subscription) {
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
    price: { amount: subscription.priceAmount, currency: subscription.priceCurrency },
    interval: subscription.planInterval,
    intervalCount: subscription.planIntervalCount,
    entitlements: subscription.entitlements,
    createdAt: subscription.createdAt,
    updatedAt: subscription.updatedAt,
  };
}
