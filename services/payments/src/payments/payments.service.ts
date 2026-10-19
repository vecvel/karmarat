/**
 * Billing operations: opening a checkout, reading payments back, refunding one.
 *
 * Settlement is deliberately not here — it lives in `webhooks/`, because the
 * only thing that may declare money received is a signed provider event, and
 * keeping that in its own module makes it obvious that no request-driven path
 * can mark a payment succeeded.
 */
import { Inject, Injectable, Logger } from '@nestjs/common';
import {
  type Page,
  type PageRequest,
  conflict,
  notFound,
  validationFailed,
  withTransaction,
} from '@karmarat/nestshared';
import type { Pool } from 'pg';

import { PG_POOL } from '../common/infrastructure.module';
import { normaliseCurrency, requirePositiveMinor } from '../common/money';
import { requireTenant } from '../common/tenant';
import { PAYMENTS_CONFIG, type PaymentsConfig } from '../config';
import { PAYMENT_PROVIDER, type PaymentProvider } from '../providers/payment-provider';
import { toProblem } from '../providers/provider-error';
import { InvoicesRepository } from './invoices.repository';
import {
  resolveRefundAmount,
  stateAfterRefund,
  type Invoice,
  type Payment,
  type PaymentState,
  type Refund,
} from './payment.entity';
import { PaymentsRepository, type ListPaymentsFilter } from './payments.repository';
import { SubscriptionsClient } from './subscriptions.client';

export interface CheckoutRequest {
  companyId: string;
  planId: string;
  /** Client-supplied Idempotency-Key header, or '' when the client sent none. */
  idempotencyKey: string;
  /** For the provider's receipt only. Never used for authorization. */
  customerEmail: string;
  /** The portal host the customer should come back to. */
  returnOrigin: string;
}

export interface CheckoutResult {
  paymentId: string;
  provider: string;
  redirectUrl: string;
  amountMinor: bigint;
  currency: string;
  /** True when this request replayed an Idempotency-Key we had already seen. */
  replayed: boolean;
}

export interface RefundRequest {
  paymentId: string;
  amountMinor: bigint | null;
  reason: string;
  requestedBy: string;
  idempotencyKey: string;
}

export interface ProviderStatus {
  provider: string;
  requestedProvider: string;
  configured: boolean;
  webhookConfigured: boolean;
  environment: string;
  defaultCurrency: string;
}

@Injectable()
export class PaymentsService {
  private readonly logger = new Logger(PaymentsService.name);

  constructor(
    @Inject(PG_POOL) private readonly pool: Pool,
    @Inject(PAYMENTS_CONFIG) private readonly config: PaymentsConfig,
    @Inject(PAYMENT_PROVIDER) private readonly provider: PaymentProvider,
    private readonly payments: PaymentsRepository,
    private readonly invoices: InvoicesRepository,
    private readonly subscriptions: SubscriptionsClient,
  ) {}

  /* ---------------------------------------------------------------- reads -- */

  async list(filter: ListPaymentsFilter): Promise<Page<Payment>> {
    return this.payments.list(filter);
  }

  async get(id: string): Promise<Payment> {
    const payment = await this.payments.findById(id);
    if (!payment) {
      throw notFound('That payment does not exist.');
    }
    return payment;
  }

  async refundsFor(paymentId: string): Promise<Refund[]> {
    return this.payments.listRefundsForPayment(paymentId);
  }

  async invoicesForCompany(companyId: string, page: PageRequest): Promise<Page<Invoice>> {
    return this.invoices.listForCompany({ ...page, companyId: requireTenant(companyId) });
  }

  /**
   * What an operator needs to know about billing, and nothing more.
   *
   * Booleans, never keys. This endpoint exists so somebody can tell at a glance
   * whether a deployment can actually take money; returning the credential
   * itself would make the answer a credential leak.
   */
  status(): ProviderStatus {
    return {
      provider: this.provider.name,
      requestedProvider: this.config.requestedProvider,
      configured: this.provider.isConfigured(),
      webhookConfigured:
        this.config.provider === 'stripe'
          ? this.config.stripe.webhookSecret !== ''
          : this.config.manual.webhookSecret !== '',
      environment: this.config.environment,
      defaultCurrency: this.config.defaultCurrency,
    };
  }

  /* ------------------------------------------------------------- checkout -- */

  async checkout(request: CheckoutRequest): Promise<CheckoutResult> {
    const companyId = requireTenant(request.companyId);

    // A recruiter double-clicking "Upgrade" on a flaky connection must not open
    // two checkouts and pay twice. The replay is answered before the provider
    // is touched, so a retry costs nothing at the provider either.
    if (request.idempotencyKey !== '') {
      const existing = await this.payments.findByIdempotencyKey(companyId, request.idempotencyKey);
      if (existing) {
        return this.replayCheckout(existing);
      }
    }

    const plan = await this.subscriptions.findPlan(request.planId);
    if (!plan) {
      throw notFound('That plan is not available.');
    }

    const currency = normaliseCurrency(plan.currency || this.config.defaultCurrency);
    // A zero-price plan is a free plan; sending it to a payment provider is
    // either a pricing bug or a checkout that can never complete.
    const amountMinor = requirePositiveMinor(plan.amountMinor, 'planId');

    const payment = await this.payments.create({
      companyId,
      provider: this.provider.name,
      planId: plan.id,
      amountMinor,
      currency,
      idempotencyKey: request.idempotencyKey,
      metadata: { planKey: plan.key, planName: plan.name },
    });

    const returnUrl = this.returnUrl(request.returnOrigin, payment.id);

    let session;
    try {
      session = await this.provider.createCheckout({
        paymentId: payment.id,
        companyId,
        planId: plan.id,
        planName: plan.name,
        amountMinor,
        currency,
        customerEmail: request.customerEmail,
        successUrl: `${returnUrl}&outcome=success`,
        cancelUrl: `${returnUrl}&outcome=cancelled`,
      });
    } catch (error) {
      // The row stays, marked cancelled: "the provider refused to open a
      // checkout" is a fact support will want, and deleting it would make the
      // attempt invisible.
      await this.payments.update(payment.id, { state: 'cancelled', failureReason: 'checkout_failed' });
      throw toProblem(error);
    }

    await this.payments.update(payment.id, { providerCheckoutId: session.checkoutId });

    return {
      paymentId: payment.id,
      provider: this.provider.name,
      redirectUrl: session.redirectUrl,
      amountMinor,
      currency,
      replayed: false,
    };
  }

  /**
   * Answers a replayed Idempotency-Key from the row we already have.
   *
   * There is no stored redirect url — provider checkout urls expire, so keeping
   * one would hand a customer a dead link. The portal is told where the payment
   * stands instead and decides whether to start a fresh checkout.
   */
  private replayCheckout(existing: Payment): CheckoutResult {
    return {
      paymentId: existing.id,
      provider: existing.provider,
      redirectUrl: this.returnUrl(this.config.hostname, existing.id),
      amountMinor: existing.amountMinor,
      currency: existing.currency,
      replayed: true,
    };
  }

  private returnUrl(origin: string, paymentId: string): string {
    const host = origin.replace(/^https?:\/\//, '').replace(/\/+$/, '') || this.config.hostname;
    return `${this.config.urlScheme}://${host}${this.config.checkoutReturnPath}?payment=${paymentId}`;
  }

  /* --------------------------------------------------------------- refund -- */

  /**
   * Returns money to a customer.
   *
   * The provider call happens between two writes on purpose. A refund row is
   * created first so that a crash after the provider has moved the money still
   * leaves a record to reconcile; if the provider refuses, that row is marked
   * failed rather than deleted, for the same reason a failed checkout keeps its
   * payment.
   */
  async refund(request: RefundRequest): Promise<{ payment: Payment; refund: Refund }> {
    if (request.idempotencyKey !== '') {
      const existing = await this.payments.findRefundByIdempotencyKey(
        request.paymentId,
        request.idempotencyKey,
      );
      if (existing) {
        const payment = await this.get(request.paymentId);
        return { payment, refund: existing };
      }
    }

    const { payment, refund } = await withTransaction(this.pool, async (client) => {
      const locked = await this.payments.findByIdForUpdate(request.paymentId, client);
      if (!locked) {
        throw notFound('That payment does not exist.');
      }

      // Recomputed under the lock rather than trusted from the row read a
      // moment ago: two operators pressing Refund at once would otherwise both
      // see the same outstanding balance and together return more than we took.
      const alreadyRefunded = await this.payments.settledRefundTotal(locked.id, client);
      const amount = resolveRefundAmount({ ...locked, refundedMinor: alreadyRefunded }, request.amountMinor);

      const created = await this.payments.createRefund(
        {
          paymentId: locked.id,
          companyId: locked.companyId,
          provider: locked.provider,
          amountMinor: amount,
          currency: locked.currency,
          reason: request.reason,
          requestedBy: request.requestedBy,
          idempotencyKey: request.idempotencyKey,
        },
        client,
      );

      return { payment: locked, refund: created };
    });

    if (!payment.providerPaymentId) {
      // Nothing at the provider to refund against. The row stays so the state is
      // visible, but no money can move.
      await this.payments.updateRefund(refund.id, { state: 'failed' });
      throw conflict(
        'payment_not_settled',
        'This payment has no provider transaction to refund against.',
      );
    }

    let providerRefund;
    try {
      providerRefund = await this.provider.refund(payment.providerPaymentId, refund.amountMinor);
    } catch (error) {
      await this.payments.updateRefund(refund.id, { state: 'failed' });
      this.logger.error(`refund ${refund.id} rejected by ${payment.provider}`);
      throw toProblem(error);
    }

    const settled = await withTransaction(this.pool, async (client) => {
      const updated = await this.payments.updateRefund(
        refund.id,
        {
          state: providerRefund.state,
          ...(providerRefund.providerRefundId
            ? { providerRefundId: providerRefund.providerRefundId }
            : {}),
        },
        client,
      );

      const total = await this.payments.settledRefundTotal(payment.id, client);
      const nextState: PaymentState = stateAfterRefund(payment.amountMinor, total);
      const patched = await this.payments.update(
        payment.id,
        { refundedMinor: total, state: nextState },
        client,
      );

      return { payment: patched ?? payment, refund: updated ?? refund };
    });

    this.logger.log(
      `refunded ${refund.amountMinor} ${payment.currency} of payment ${payment.id} ` +
        `(${settled.payment.state})`,
    );

    return settled;
  }
}

/** Parses the optional amount on a refund body. */
export function parseRefundAmount(raw: unknown): bigint | null {
  if (raw === undefined || raw === null) {
    return null;
  }
  if (typeof raw === 'number' && Number.isSafeInteger(raw)) {
    return BigInt(raw);
  }
  if (typeof raw === 'string' && /^-?\d+$/.test(raw.trim())) {
    return BigInt(raw.trim());
  }
  throw validationFailed({ amountMinor: ['must be an integer number of minor units'] });
}
