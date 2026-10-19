/**
 * Stripe, behind the platform's provider interface.
 *
 * This is the only file in the service that imports the Stripe SDK, and
 * `abstraction-boundary.spec.ts` fails the build if that stops being true. The
 * value of the interface is entirely in that constraint: an abstraction whose
 * types leak out of its directory is documentation, not a seam.
 *
 * Everything Stripe-specific is translated here — event names, object shapes,
 * the fact that a "payment" is sometimes a Checkout Session and sometimes a
 * PaymentIntent — so that the rest of the service only ever sees "this tenant's
 * payment succeeded, for this amount".
 */
import { Logger } from '@nestjs/common';
import Stripe from 'stripe';

import { toProviderAmount } from '../common/money';
import { ProviderError, WebhookVerificationError } from './provider-error';
import type {
  CheckoutSession,
  CreateCheckoutInput,
  PaymentProvider,
  ProviderPayment,
  ProviderPaymentState,
  ProviderRefund,
  WebhookEvent,
  WebhookPaymentFacts,
  WebhookRefundFacts,
} from './payment-provider';

export interface StripeProviderOptions {
  secretKey: string;
  webhookSecret: string;
}

/** Metadata key carrying our payment id through Stripe and back. */
const REFERENCE_KEY = 'karmaratPaymentId';

export class StripeProvider implements PaymentProvider {
  readonly name = 'stripe';

  private readonly logger = new Logger('stripe');
  private readonly stripe: Stripe;

  constructor(private readonly options: StripeProviderOptions) {
    this.stripe = new Stripe(options.secretKey, {
      // No apiVersion is pinned here on purpose. Each SDK release is generated
      // against one version and its types describe that version; naming a
      // different string compiles only with a cast and then silently changes
      // response shapes at runtime. Upgrading the API version is a deliberate
      // dependency bump, not a configuration value.
      maxNetworkRetries: 2,
      timeout: 15_000,
      appInfo: { name: 'karmarat-payments' },
    });
  }

  isConfigured(): boolean {
    return this.options.secretKey !== '' && this.options.webhookSecret !== '';
  }

  async createCheckout(input: CreateCheckoutInput): Promise<CheckoutSession> {
    try {
      const session = await this.stripe.checkout.sessions.create(
        {
          mode: 'payment',
          success_url: input.successUrl,
          cancel_url: input.cancelUrl,
          // Echoed back on every event this checkout produces. The webhook
          // resolves the tenant from the payment row this points at, never from
          // a company id in the payload.
          client_reference_id: input.paymentId,
          metadata: { [REFERENCE_KEY]: input.paymentId },
          // Repeated on the PaymentIntent because `payment_intent.*` events
          // carry the intent's metadata, not the session's.
          payment_intent_data: { metadata: { [REFERENCE_KEY]: input.paymentId } },
          ...(input.customerEmail ? { customer_email: input.customerEmail } : {}),
          line_items: [
            {
              quantity: 1,
              price_data: {
                currency: input.currency.toLowerCase(),
                unit_amount: toProviderAmount(input.amountMinor),
                product_data: { name: input.planName },
              },
            },
          ],
        },
        // Our payment id is unique per checkout attempt, so a retried create
        // returns the session we already opened rather than a second one.
        { idempotencyKey: `checkout:${input.paymentId}` },
      );

      if (!session.url) {
        throw new ProviderError('checkout session has no redirect url', 'The checkout could not be started.');
      }

      return { checkoutId: session.id, redirectUrl: session.url };
    } catch (error) {
      throw this.wrap(error, 'The checkout could not be started.');
    }
  }

  verifyWebhook(rawBody: Buffer, signature: string): WebhookEvent {
    if (this.options.webhookSecret === '') {
      // Fail closed: an unconfigured secret must not mean "accept anything".
      throw new WebhookVerificationError('stripe webhook secret is not configured');
    }

    let event: Stripe.Event;
    try {
      event = this.stripe.webhooks.constructEvent(rawBody, signature, this.options.webhookSecret);
    } catch (error) {
      // The SDK's message names the mismatch and echoes part of the header;
      // neither is something an unauthenticated caller should be told.
      throw new WebhookVerificationError(`stripe signature verification failed: ${describe(error)}`);
    }

    return this.translate(event);
  }

  async refund(providerPaymentId: string, amountMinor: bigint): Promise<ProviderRefund> {
    try {
      // Deliberately no idempotency key: two legitimate partial refunds of the
      // same amount against the same payment are a real thing a platform
      // operator does, and a deterministic key would silently collapse the
      // second into the first. The guard against an accidental replay is the
      // Idempotency-Key the endpoint records in our own database, where we can
      // tell a retry from a second decision.
      const refund = await this.stripe.refunds.create({
        payment_intent: providerPaymentId,
        amount: toProviderAmount(amountMinor),
      });

      return {
        providerRefundId: refund.id,
        providerPaymentId,
        amountMinor: BigInt(refund.amount),
        currency: (refund.currency ?? '').toUpperCase(),
        state: refundState(refund.status),
      };
    } catch (error) {
      throw this.wrap(error, 'The refund could not be issued.');
    }
  }

  async getPayment(providerPaymentId: string): Promise<ProviderPayment> {
    try {
      const intent = await this.stripe.paymentIntents.retrieve(providerPaymentId, {
        expand: ['latest_charge'],
      });

      const charge = typeof intent.latest_charge === 'object' ? intent.latest_charge : null;
      const card = charge?.payment_method_details?.card;

      return {
        providerPaymentId: intent.id,
        amountMinor: BigInt(intent.amount_received || intent.amount),
        currency: intent.currency.toUpperCase(),
        state: intentState(intent.status),
        reference: intent.metadata?.[REFERENCE_KEY] ?? '',
        cardBrand: card?.brand ?? '',
        cardLast4: card?.last4 ?? '',
      };
    } catch (error) {
      throw this.wrap(error, 'The payment could not be read from the provider.');
    }
  }

  // ------------------------------------------------------------ translation --

  /**
   * Turns a Stripe event into a platform fact.
   *
   * Several Stripe events describe the same success — `checkout.session.completed`,
   * `payment_intent.succeeded` and `charge.succeeded` all arrive for one card
   * payment. All of them are translated to `payment.succeeded`; making that safe
   * is the caller's job, and it does it by only acting when the state actually
   * changes rather than by trying to pick one true event here.
   */
  private translate(event: Stripe.Event): WebhookEvent {
    const base = { id: event.id, type: event.type, raw: event as unknown };

    switch (event.type) {
      case 'checkout.session.completed':
      case 'checkout.session.async_payment_succeeded': {
        const session = event.data.object;
        // An unpaid completed session happens with delayed payment methods; the
        // money is not ours yet, so it is not a success.
        if (session.payment_status !== 'paid') {
          return { ...base, kind: 'unhandled' };
        }
        return { ...base, kind: 'payment.succeeded', payment: fromSession(session) };
      }

      case 'checkout.session.async_payment_failed': {
        return { ...base, kind: 'payment.failed', payment: fromSession(event.data.object) };
      }

      case 'checkout.session.expired': {
        return { ...base, kind: 'checkout.cancelled', payment: fromSession(event.data.object) };
      }

      case 'payment_intent.succeeded': {
        return { ...base, kind: 'payment.succeeded', payment: fromIntent(event.data.object) };
      }

      case 'payment_intent.payment_failed': {
        return { ...base, kind: 'payment.failed', payment: fromIntent(event.data.object) };
      }

      case 'charge.refund.updated':
      case 'refund.updated': {
        const refund = event.data.object as Stripe.Refund;
        const facts = fromRefund(refund);
        if (refund.status === 'succeeded') {
          return { ...base, kind: 'refund.succeeded', refund: facts };
        }
        if (refund.status === 'failed' || refund.status === 'canceled') {
          return { ...base, kind: 'refund.failed', refund: facts };
        }
        return { ...base, kind: 'unhandled' };
      }

      default:
        return { ...base, kind: 'unhandled' };
    }
  }

  /**
   * Wraps an SDK failure with a detail that is safe to return.
   *
   * A network or rate-limit error is worth retrying; a card decline or a bad
   * request is not, and telling the caller to try again would just burn their
   * time.
   */
  private wrap(error: unknown, safeDetail: string): ProviderError {
    if (error instanceof ProviderError) {
      return error;
    }

    const retryable =
      error instanceof Stripe.errors.StripeConnectionError ||
      error instanceof Stripe.errors.StripeAPIError ||
      error instanceof Stripe.errors.StripeRateLimitError;

    this.logger.error(`stripe call failed: ${describe(error)}`);
    return new ProviderError(describe(error), safeDetail, retryable);
  }
}

// ------------------------------------------------------------------ mapping --

function fromSession(session: Stripe.Checkout.Session): WebhookPaymentFacts {
  return {
    reference: session.client_reference_id ?? session.metadata?.[REFERENCE_KEY] ?? '',
    providerPaymentId: idOf(session.payment_intent),
    providerCheckoutId: session.id,
    amountMinor: BigInt(session.amount_total ?? 0),
    currency: (session.currency ?? '').toUpperCase(),
    cardBrand: '',
    cardLast4: '',
    failureReason: '',
  };
}

function fromIntent(intent: Stripe.PaymentIntent): WebhookPaymentFacts {
  const charge = typeof intent.latest_charge === 'object' ? intent.latest_charge : null;
  const card = charge?.payment_method_details?.card;

  return {
    reference: intent.metadata?.[REFERENCE_KEY] ?? '',
    providerPaymentId: intent.id,
    providerCheckoutId: '',
    amountMinor: BigInt(intent.amount_received || intent.amount),
    currency: intent.currency.toUpperCase(),
    cardBrand: card?.brand ?? '',
    cardLast4: card?.last4 ?? '',
    // Stripe's decline messages are written for the cardholder and carry no
    // internal detail, which is why this one is safe to pass through.
    failureReason: intent.last_payment_error?.message ?? '',
  };
}

function fromRefund(refund: Stripe.Refund): WebhookRefundFacts {
  return {
    providerRefundId: refund.id,
    providerPaymentId: idOf(refund.payment_intent),
    amountMinor: BigInt(refund.amount),
    currency: (refund.currency ?? '').toUpperCase(),
    failureReason: refund.failure_reason ?? '',
  };
}

/** Stripe returns either an id or an expanded object depending on the call. */
function idOf(value: string | { id: string } | null | undefined): string {
  if (!value) return '';
  return typeof value === 'string' ? value : value.id;
}

function intentState(status: Stripe.PaymentIntent.Status): ProviderPaymentState {
  switch (status) {
    case 'succeeded':
      return 'succeeded';
    case 'processing':
      return 'processing';
    case 'canceled':
      return 'cancelled';
    case 'requires_payment_method':
    case 'requires_confirmation':
    case 'requires_action':
    case 'requires_capture':
      return 'pending';
    default:
      return 'pending';
  }
}

function refundState(status: string | null): ProviderRefund['state'] {
  switch (status) {
    case 'succeeded':
      return 'succeeded';
    case 'failed':
    case 'canceled':
      return 'failed';
    default:
      return 'pending';
  }
}

/** A log-safe description. Never reaches a response body. */
function describe(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
