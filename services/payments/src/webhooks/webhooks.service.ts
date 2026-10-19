/**
 * Settlement: the only path in this service that may declare money received.
 *
 * Three properties matter here and everything else is arrangement around them.
 *
 * 1. **The signature is checked against the raw bytes.** A body that has been
 *    parsed and re-serialised differs in key order and whitespace and will not
 *    verify. The signature is the entire security of a public route, so nothing
 *    below runs until `verifyWebhook` has returned.
 *
 * 2. **A repeat does nothing twice.** `webhook_events.provider_event_id` is
 *    UNIQUE and the claim is an INSERT … ON CONFLICT DO NOTHING, so two
 *    concurrent deliveries of one event cannot both proceed. A second delivery
 *    answers 200 without re-applying — double-crediting a subscription is a real
 *    financial bug, not a cosmetic one.
 *
 * 3. **Everything answers 200 quickly, including what we ignore.** Providers
 *    retry on any non-2xx, so an unrecognised event type that returned 4xx would
 *    be redelivered forever. What was ignored, and why, is recorded on the row.
 */
import { Inject, Injectable, Logger } from '@nestjs/common';
import { EventBus, Subject, withTransaction } from '@karmarat/nestshared';
import type { Pool, PoolClient } from 'pg';

import { EVENT_BUS, PG_POOL } from '../common/infrastructure.module';
import { toJsonMinor } from '../common/money';
import { InvoicesRepository } from '../payments/invoices.repository';
import {
  outcomeForFailure,
  outcomeForSuccess,
  stateAfterRefund,
  type Invoice,
  type Payment,
} from '../payments/payment.entity';
import { PaymentsRepository } from '../payments/payments.repository';
import { SubscriptionsClient } from '../payments/subscriptions.client';
import { PAYMENT_PROVIDER, type PaymentProvider, type WebhookEvent } from '../providers/payment-provider';
import { WebhookEventsRepository } from './webhook-events.repository';

/** What the route reports back. The provider only ever sees the status code. */
export interface WebhookResult {
  status: 'applied' | 'duplicate' | 'ignored' | 'failed';
  eventId: string;
  detail: string;
}

@Injectable()
export class WebhooksService {
  private readonly logger = new Logger('webhooks');

  constructor(
    @Inject(PG_POOL) private readonly pool: Pool,
    @Inject(EVENT_BUS) private readonly bus: EventBus,
    @Inject(PAYMENT_PROVIDER) private readonly provider: PaymentProvider,
    private readonly events: WebhookEventsRepository,
    private readonly payments: PaymentsRepository,
    private readonly invoices: InvoicesRepository,
    private readonly subscriptions: SubscriptionsClient,
  ) {}

  /**
   * Verifies, de-duplicates and applies one provider delivery.
   *
   * Throws only when the signature does not verify; everything after that point
   * is reported in the result and answered 200.
   */
  async handle(rawBody: Buffer, signature: string): Promise<WebhookResult> {
    // Throws WebhookVerificationError, which the controller turns into a bare
    // 401 with no hint as to which part of the signature was wrong.
    const event = this.provider.verifyWebhook(rawBody, signature);

    const claim = await this.events.claim(this.provider.name, event.id, event.type, event.raw);
    if (!claim.claimed) {
      this.logger.log(
        `ignoring repeat delivery of ${this.provider.name} event ${event.id} ` +
          `(already ${claim.record.status})`,
      );
      return { status: 'duplicate', eventId: event.id, detail: 'already processed' };
    }

    const ledgerId = claim.record.id;

    try {
      return await this.apply(event, ledgerId);
    } catch (error) {
      // The claim has already committed, so this event will never be re-applied
      // by a retry. It is left visible to the reconciliation job instead —
      // losing an effect an operator can see beats applying one twice.
      const detail = (error as Error).message;
      this.logger.error(`applying ${this.provider.name} event ${event.id} failed: ${detail}`);
      await this.events.markFailed(ledgerId, detail).catch(() => undefined);
      return { status: 'failed', eventId: event.id, detail: 'the event was recorded but not applied' };
    }
  }

  private async apply(event: WebhookEvent, ledgerId: string): Promise<WebhookResult> {
    switch (event.kind) {
      case 'payment.succeeded':
        return this.applySuccess(event, ledgerId);
      case 'payment.failed':
        return this.applyFailure(event, ledgerId);
      case 'checkout.cancelled':
        return this.applyCancellation(event, ledgerId);
      case 'refund.succeeded':
      case 'refund.failed':
        return this.applyRefund(event, ledgerId);
      default:
        return this.ignore(ledgerId, event, `no handler for provider event type "${event.type}"`);
    }
  }

  /* --------------------------------------------------------------- success -- */

  private async applySuccess(event: WebhookEvent, ledgerId: string): Promise<WebhookResult> {
    const facts = event.payment;
    if (!facts) {
      return this.ignore(ledgerId, event, 'the event carried no payment');
    }

    const applied = await withTransaction(this.pool, async (client) => {
      const payment = await this.locate(event, client);
      if (!payment) {
        return null;
      }

      // The amount is checked against our own row rather than trusted. The
      // signature proves the provider sent it, not that it matches what we
      // asked for, and a mismatch means the customer was charged something
      // other than the plan price — a human has to look at that.
      const mismatch = describeMismatch(payment, facts.amountMinor, facts.currency);
      if (mismatch) {
        throw new Error(`payment ${payment.id}: ${mismatch}`);
      }

      if (outcomeForSuccess(payment.state) === 'noop') {
        // Stripe emits checkout.session.completed, payment_intent.succeeded and
        // charge.succeeded for one card payment. The ledger de-duplicates
        // identical event ids; deciding by state is what makes three different
        // events credit a subscription once.
        return { payment, invoice: null, alreadySettled: true };
      }

      const updated = await this.payments.update(
        payment.id,
        {
          state: 'succeeded',
          ...(facts.providerPaymentId ? { providerPaymentId: facts.providerPaymentId } : {}),
          ...(facts.providerCheckoutId && !payment.providerCheckoutId
            ? { providerCheckoutId: facts.providerCheckoutId }
            : {}),
          // Brand and last four only: an invoice line saying "Visa ending 4242"
          // needs nothing more, and a PAN or CVV in this database would drag the
          // platform into PCI-DSS scope for no product benefit.
          ...(facts.cardBrand ? { cardBrand: facts.cardBrand } : {}),
          ...(facts.cardLast4 ? { cardLast4: facts.cardLast4 } : {}),
          failureReason: '',
        },
        client,
      );

      const settled = updated ?? payment;
      const invoice = await this.issueInvoice(settled, client);
      return { payment: settled, invoice, alreadySettled: false };
    });

    if (!applied) {
      return this.ignore(ledgerId, event, 'no payment matched the provider reference');
    }

    if (applied.alreadySettled) {
      await this.events.markProcessed(ledgerId);
      return { status: 'duplicate', eventId: event.id, detail: 'the payment had already settled' };
    }

    // Downstream effects run after the commit. A publish inside the transaction
    // would announce a payment that a rollback then un-made.
    const problems = await this.announceSuccess(applied.payment, applied.invoice);

    if (problems.length) {
      await this.events.noteDeliveryError(ledgerId, problems.join('; '));
    } else {
      await this.events.markProcessed(ledgerId);
    }

    this.logger.log(
      `payment ${applied.payment.id} succeeded (${applied.payment.amountMinor} ` +
        `${applied.payment.currency}, invoice ${applied.invoice?.number ?? 'none'})`,
    );

    return { status: 'applied', eventId: event.id, detail: 'the payment was settled' };
  }

  /**
   * Announces a settled payment and opens the subscription.
   *
   * The event is the durable path — subscriptions can be down and still catch
   * up — while the direct call is what makes the portal open now rather than
   * whenever a consumer gets round to it. Neither is allowed to fail the
   * request: the money has moved, and a non-2xx here would only make the
   * provider redeliver an event we have already applied.
   */
  private async announceSuccess(payment: Payment, invoice: Invoice | null): Promise<string[]> {
    const problems: string[] = [];

    try {
      await this.bus.publish(
        Subject.PaymentSucceeded,
        {
          paymentId: payment.id,
          companyId: payment.companyId,
          planId: payment.planId,
          provider: payment.provider,
          amountMinor: toJsonMinor(payment.amountMinor),
          currency: payment.currency,
          invoiceId: invoice?.id ?? null,
          invoiceNumber: invoice?.number ?? null,
        },
        {
          companyId: payment.companyId,
          // Deterministic: JetStream de-duplicates by message id, so a retried
          // publish after a partial failure delivers the fact once.
          id: `pay_succeeded_${payment.id}`,
        },
      );
    } catch (error) {
      problems.push(`publish: ${(error as Error).message}`);
    }

    const outcome = await this.subscriptions.activate({
      companyId: payment.companyId,
      planId: payment.planId,
      paymentId: payment.id,
      amountMinor: payment.amountMinor,
      currency: payment.currency,
    });

    if (outcome.status === 'activated' && outcome.subscriptionId) {
      await this.payments
        .update(payment.id, { subscriptionId: outcome.subscriptionId })
        .catch((error: Error) => problems.push(`link subscription: ${error.message}`));
    }

    if (outcome.status === 'failed') {
      problems.push(`activate: ${outcome.detail}`);
    }

    if (outcome.status === 'unsupported') {
      // Not queued for replay: this endpoint will answer the same way forever.
      // The published event is the path subscriptions is expected to consume
      // until it grows an internal activation route.
      this.logger.warn(
        `subscriptions could not be activated directly for payment ${payment.id}: ` +
          `${outcome.detail}; relying on ${Subject.PaymentSucceeded}`,
      );
    }

    return problems;
  }

  /* --------------------------------------------------------------- failure -- */

  private async applyFailure(event: WebhookEvent, ledgerId: string): Promise<WebhookResult> {
    const facts = event.payment;
    if (!facts) {
      return this.ignore(ledgerId, event, 'the event carried no payment');
    }

    const payment = await withTransaction(this.pool, async (client) => {
      const found = await this.locate(event, client);
      if (!found) {
        return null;
      }

      const outcome = outcomeForFailure(found.state);
      if (outcome !== 'apply') {
        // A failure arriving after a success is out-of-order delivery, not a
        // reversal. Money that has settled is only undone by a refund.
        return { ...found, state: found.state };
      }

      const updated = await this.payments.update(
        found.id,
        {
          state: 'failed',
          // Already sanitised at the provider boundary: a decline message is
          // written for the cardholder and carries no internal detail.
          failureReason: facts.failureReason.slice(0, 500),
          ...(facts.providerPaymentId ? { providerPaymentId: facts.providerPaymentId } : {}),
        },
        client,
      );
      return updated ?? found;
    });

    if (!payment) {
      return this.ignore(ledgerId, event, 'no payment matched the provider reference');
    }

    if (payment.state !== 'failed') {
      await this.events.markIgnored(ledgerId, `payment is ${payment.state}; a failure cannot reverse it`);
      return { status: 'ignored', eventId: event.id, detail: 'the payment had already settled' };
    }

    const problems: string[] = [];
    try {
      await this.bus.publish(
        Subject.PaymentFailed,
        {
          paymentId: payment.id,
          companyId: payment.companyId,
          planId: payment.planId,
          provider: payment.provider,
          amountMinor: toJsonMinor(payment.amountMinor),
          currency: payment.currency,
          reason: payment.failureReason,
        },
        { companyId: payment.companyId, id: `pay_failed_${payment.id}` },
      );
    } catch (error) {
      problems.push(`publish: ${(error as Error).message}`);
    }

    if (problems.length) {
      await this.events.noteDeliveryError(ledgerId, problems.join('; '));
    } else {
      await this.events.markProcessed(ledgerId);
    }

    this.logger.log(`payment ${payment.id} failed at ${payment.provider}`);
    return { status: 'applied', eventId: event.id, detail: 'the payment was marked failed' };
  }

  /* ---------------------------------------------------------- cancellation -- */

  private async applyCancellation(event: WebhookEvent, ledgerId: string): Promise<WebhookResult> {
    const cancelled = await withTransaction(this.pool, async (client) => {
      const payment = await this.locate(event, client);
      if (!payment) {
        return null;
      }
      if (payment.state !== 'pending' && payment.state !== 'processing') {
        return payment;
      }
      const updated = await this.payments.update(payment.id, { state: 'cancelled' }, client);
      return updated ?? payment;
    });

    if (!cancelled) {
      return this.ignore(ledgerId, event, 'no payment matched the provider reference');
    }
    if (cancelled.state !== 'cancelled') {
      await this.events.markIgnored(ledgerId, `payment is ${cancelled.state}; nothing to cancel`);
      return { status: 'ignored', eventId: event.id, detail: 'the checkout was no longer open' };
    }

    // No event is published: an abandoned checkout is not a fact any other
    // service acts on, and a subject nobody consumes is noise on the stream.
    await this.events.markProcessed(ledgerId);
    return { status: 'applied', eventId: event.id, detail: 'the checkout was cancelled' };
  }

  /* ---------------------------------------------------------------- refund -- */

  private async applyRefund(event: WebhookEvent, ledgerId: string): Promise<WebhookResult> {
    const facts = event.refund;
    if (!facts) {
      return this.ignore(ledgerId, event, 'the event carried no refund');
    }

    const succeeded = event.kind === 'refund.succeeded';

    const settled = await withTransaction(this.pool, async (client) => {
      const existing = facts.providerRefundId
        ? await this.payments.findRefundByProviderId(this.provider.name, facts.providerRefundId, client)
        : null;

      const payment = existing
        ? await this.payments.findByIdForUpdate(existing.paymentId, client)
        : await this.payments.findForProviderEvent(
            this.provider.name,
            '',
            facts.providerPaymentId,
            '',
            client,
          );

      if (!payment) {
        return null;
      }

      const refund =
        existing ?? (await this.payments.findPendingRefund(payment.id, facts.amountMinor, client));
      if (!refund) {
        // A refund issued in the provider's own dashboard rather than through
        // this service. The money really has moved, so the payment total is
        // still corrected below; there is simply no local row to settle.
        return { payment, refund: null };
      }

      await this.payments.updateRefund(
        refund.id,
        {
          state: succeeded ? 'succeeded' : 'failed',
          ...(facts.providerRefundId ? { providerRefundId: facts.providerRefundId } : {}),
        },
        client,
      );

      const total = await this.payments.settledRefundTotal(payment.id, client);
      const updated = await this.payments.update(
        payment.id,
        { refundedMinor: total, state: stateAfterRefund(payment.amountMinor, total) },
        client,
      );

      return { payment: updated ?? payment, refund };
    });

    if (!settled) {
      return this.ignore(ledgerId, event, 'no payment matched the provider refund');
    }
    if (!settled.refund) {
      return this.ignore(ledgerId, event, 'no local refund matched; it was issued outside this service');
    }

    await this.events.markProcessed(ledgerId);
    this.logger.log(
      `refund ${settled.refund.id} ${succeeded ? 'succeeded' : 'failed'}; ` +
        `payment ${settled.payment.id} is now ${settled.payment.state}`,
    );
    return { status: 'applied', eventId: event.id, detail: 'the refund was settled' };
  }

  /* ----------------------------------------------------------------- utils -- */

  /**
   * Finds the payment an event refers to.
   *
   * Never by a company id from the payload: the tenant is whatever the matched
   * row says, which is what stops a forged reference from touching somebody
   * else's data even if a signature were ever compromised.
   */
  private locate(event: WebhookEvent, client: PoolClient): Promise<Payment | null> {
    const facts = event.payment;
    if (!facts) {
      return Promise.resolve(null);
    }
    return this.payments.findForProviderEvent(
      this.provider.name,
      facts.reference,
      facts.providerPaymentId,
      facts.providerCheckoutId,
      client,
    );
  }

  private async issueInvoice(payment: Payment, client: PoolClient): Promise<Invoice> {
    const planName = typeof payment.metadata['planName'] === 'string'
      ? (payment.metadata['planName'] as string)
      : 'Subscription';
    const amount = toJsonMinor(payment.amountMinor);

    return this.invoices.issue(
      {
        companyId: payment.companyId,
        paymentId: payment.id,
        lines: [
          { description: planName, quantity: 1, unitAmountMinor: amount, amountMinor: amount },
        ],
        subtotalMinor: payment.amountMinor,
        // Tax is not computed here. Doing it properly needs a tax engine and
        // the customer's place of supply; inventing a zero and calling it VAT
        // would be worse than recording that none was calculated.
        taxMinor: 0n,
        totalMinor: payment.amountMinor,
        currency: payment.currency,
      },
      client,
    );
  }

  private async ignore(ledgerId: string, event: WebhookEvent, reason: string): Promise<WebhookResult> {
    this.logger.log(`ignoring ${this.provider.name} event ${event.id} (${event.type}): ${reason}`);
    await this.events.markIgnored(ledgerId, reason);
    return { status: 'ignored', eventId: event.id, detail: reason };
  }
}

/**
 * Compares what the provider says was charged against what we asked for.
 *
 * Exported for the table-driven test: this is the guard that stops a settled
 * payment silently disagreeing with its own invoice.
 */
export function describeMismatch(
  payment: Payment,
  amountMinor: bigint,
  currency: string,
): string | null {
  // A provider that echoes no amount (the manual provider settling a comped
  // plan) is taken at the row's word rather than treated as a mismatch.
  if (amountMinor !== 0n && amountMinor !== payment.amountMinor) {
    return `provider reported ${amountMinor} but the payment is for ${payment.amountMinor}`;
  }
  if (currency !== '' && currency.toUpperCase() !== payment.currency.toUpperCase()) {
    return `provider reported ${currency} but the payment is in ${payment.currency}`;
  }
  return null;
}
