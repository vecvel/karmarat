/**
 * Calls the subscriptions service.
 *
 * Two questions live there and not here: what a plan costs, and what a settled
 * payment should do to a company's subscription. Payments deliberately keeps no
 * copy of the plan catalogue — a price duplicated into this database is a price
 * that will one day disagree with the one the customer was shown.
 */
import { Injectable, Logger } from '@nestjs/common';
import { Problem, type ProblemBody } from '@karmarat/nestshared';

import { toMinor } from '../common/money';
import type { PaymentsConfig } from '../config';

export interface PlanSummary {
  id: string;
  key: string;
  name: string;
  amountMinor: bigint;
  currency: string;
}

/**
 * What an activation attempt actually did.
 *
 * `unsupported` is separate from `failed` because they call for different
 * operator responses: a failed call is worth replaying, while an endpoint that
 * does not exist will answer the same way forever and replaying it just fills
 * the log.
 */
export type ActivationOutcome =
  | { status: 'activated'; subscriptionId: string | null }
  | { status: 'unsupported'; detail: string }
  | { status: 'failed'; detail: string };

export interface ActivateInput {
  companyId: string;
  planId: string;
  paymentId: string;
  amountMinor: bigint;
  currency: string;
}

@Injectable()
export class SubscriptionsClient {
  private readonly logger = new Logger(SubscriptionsClient.name);
  private readonly baseUrl: string;

  constructor(private readonly config: PaymentsConfig) {
    this.baseUrl = config.subscriptionsUrl.replace(/\/+$/, '');
  }

  /**
   * Reads a plan from the published catalogue.
   *
   * The public catalogue rather than the platform admin list, because a company
   * may only ever buy a plan that is actually on sale — an unpublished or
   * retired plan should be unbuyable, and reading the published list is what
   * makes that true without a second rule to keep in sync.
   */
  async findPlan(planId: string): Promise<PlanSummary | null> {
    const response = await this.get('/v1/public/plans');

    if (!response.ok) {
      this.logger.error(`subscriptions returned ${response.status} for the plan catalogue`);
      throw unavailable();
    }

    const body = (await response.json()) as { items?: unknown };
    const items = Array.isArray(body.items) ? body.items : [];

    for (const item of items) {
      const plan = item as {
        id?: unknown;
        key?: unknown;
        name?: unknown;
        price?: { amount?: unknown; currency?: unknown };
      };
      if (plan.id !== planId && plan.key !== planId) {
        continue;
      }
      return {
        id: String(plan.id ?? ''),
        key: String(plan.key ?? ''),
        name: String(plan.name ?? 'Subscription'),
        amountMinor: toMinor(readAmount(plan.price?.amount)),
        currency: String(plan.price?.currency ?? '').toUpperCase(),
      };
    }

    return null;
  }

  /**
   * Tells subscriptions that a payment settled.
   *
   * Belt and braces with the `payment.succeeded` event: the event is the
   * durable path and survives a subscriptions outage, while this call is what
   * makes the portal open *now* rather than whenever a consumer gets round to
   * it. A customer who has just paid should not watch a spinner.
   *
   * It never throws. A settled payment is a fact regardless of what the peer
   * says, and turning a peer's outage into a 500 would make the provider retry
   * a webhook we have already applied.
   */
  async activate(input: ActivateInput): Promise<ActivationOutcome> {
    let response: Response;
    try {
      response = await fetch(`${this.baseUrl}/internal/subscriptions/activate`, {
        method: 'POST',
        headers: {
          'content-type': 'application/json',
          accept: 'application/json',
          'x-internal-token': this.config.internalToken,
        },
        body: JSON.stringify({
          companyId: input.companyId,
          planId: input.planId,
          paymentId: input.paymentId,
          amountMinor: Number(input.amountMinor),
          currency: input.currency,
        }),
        // Without a deadline a hung peer holds the webhook open until the
        // provider times out and retries an event we are still applying.
        signal: AbortSignal.timeout(this.config.subscriptionsTimeoutMs),
      });
    } catch (error) {
      const detail = (error as Error).message;
      this.logger.error(`subscriptions activation call failed: ${detail}`);
      return { status: 'failed', detail: 'subscriptions could not be reached' };
    }

    if (response.ok) {
      const body = (await response.json().catch(() => ({}))) as { id?: unknown; subscriptionId?: unknown };
      const id = body.subscriptionId ?? body.id;
      return { status: 'activated', subscriptionId: typeof id === 'string' ? id : null };
    }

    // 404/405 mean subscriptions has no such route. See the note in
    // payments.service.ts: this is a known gap in the platform contract, not a
    // transient failure, so it is reported once per event and not queued for
    // replay.
    if (response.status === 404 || response.status === 405) {
      return {
        status: 'unsupported',
        detail: 'subscriptions exposes no internal activation endpoint',
      };
    }

    const problem = await readProblem(response);
    this.logger.error(
      `subscriptions refused activation with ${response.status} (${problem?.code ?? 'no code'}) ` +
        `for payment ${input.paymentId}`,
    );
    return { status: 'failed', detail: `subscriptions answered ${response.status}` };
  }

  private async get(path: string): Promise<Response> {
    try {
      return await fetch(`${this.baseUrl}${path}`, {
        method: 'GET',
        headers: { accept: 'application/json' },
        signal: AbortSignal.timeout(this.config.subscriptionsTimeoutMs),
      });
    } catch (error) {
      this.logger.error(`subscriptions call to ${path} failed: ${(error as Error).message}`);
      throw unavailable();
    }
  }
}

function readAmount(value: unknown): string | number | bigint {
  if (typeof value === 'number' || typeof value === 'bigint' || typeof value === 'string') {
    return value;
  }
  return 0;
}

function unavailable(): Problem {
  return new Problem(
    503,
    'billing_unavailable',
    'Service Unavailable',
    'Billing is temporarily unavailable. Please try again in a moment.',
  );
}

async function readProblem(response: Response): Promise<ProblemBody | null> {
  try {
    const body = (await response.json()) as ProblemBody;
    return typeof body === 'object' && body !== null ? body : null;
  } catch {
    return null;
  }
}
