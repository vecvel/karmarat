/**
 * The two faces of billing.
 *
 * Platform staff administer transactions across every tenant; a company sees
 * its own checkout and its own invoices and nothing else. They are separate
 * controllers rather than one with branching, because the difference between
 * them is who may call — and that is clearest when it is the route.
 */
import { Body, Controller, Get, Headers, Param, Post, Query } from '@nestjs/common';
import {
  CompanyId,
  CurrentPrincipal,
  Principal,
  RequirePermission,
  RequirePrincipalType,
  parsePageRequest,
  validationFailed,
} from '@karmarat/nestshared';

import { toJsonMinor } from '../common/money';
import type { Invoice, Payment, Refund } from './payment.entity';
import { PaymentsService, parseRefundAmount, type ProviderStatus } from './payments.service';

function toPaymentView(payment: Payment): Record<string, unknown> {
  return {
    id: payment.id,
    companyId: payment.companyId,
    provider: payment.provider,
    planId: payment.planId,
    subscriptionId: payment.subscriptionId,
    state: payment.state,
    amount: { minor: toJsonMinor(payment.amountMinor), currency: payment.currency },
    refunded: { minor: toJsonMinor(payment.refundedMinor), currency: payment.currency },
    failureReason: payment.failureReason,
    // The only instrument details that exist anywhere in this service. Enough
    // for a receipt to say "Visa ending 4242"; a full number or a CVV is never
    // received, stored or returned.
    card: payment.cardLast4 ? { brand: payment.cardBrand, last4: payment.cardLast4 } : null,
    // Provider identifiers are opaque handles, not secrets, and support needs
    // them to look a transaction up in the provider's own dashboard.
    providerCheckoutId: payment.providerCheckoutId,
    providerPaymentId: payment.providerPaymentId,
    createdAt: payment.createdAt,
    updatedAt: payment.updatedAt,
  };
}

function toRefundView(refund: Refund): Record<string, unknown> {
  return {
    id: refund.id,
    paymentId: refund.paymentId,
    state: refund.state,
    amount: { minor: toJsonMinor(refund.amountMinor), currency: refund.currency },
    reason: refund.reason,
    requestedBy: refund.requestedBy,
    providerRefundId: refund.providerRefundId,
    createdAt: refund.createdAt,
    updatedAt: refund.updatedAt,
  };
}

function toInvoiceView(invoice: Invoice): Record<string, unknown> {
  return {
    id: invoice.id,
    number: invoice.number,
    paymentId: invoice.paymentId,
    lines: invoice.lines,
    subtotal: { minor: toJsonMinor(invoice.subtotalMinor), currency: invoice.currency },
    tax: { minor: toJsonMinor(invoice.taxMinor), currency: invoice.currency },
    total: { minor: toJsonMinor(invoice.totalMinor), currency: invoice.currency },
    issuedAt: invoice.issuedAt,
    createdAt: invoice.createdAt,
  };
}

/* -------------------------------------------------------------------------- */
/* Platform                                                                   */
/* -------------------------------------------------------------------------- */

@Controller('v1/payments')
@RequirePrincipalType('platform')
export class PaymentsController {
  constructor(private readonly payments: PaymentsService) {}

  /**
   * Which provider is live, and whether it can actually take money.
   *
   * Declared before `:id` — Nest matches routes in declaration order, and
   * `GET /v1/payments/:id` would otherwise swallow `/config` and answer 404.
   */
  @Get('config')
  @RequirePermission('payments.read')
  config(): ProviderStatus {
    // Booleans, never keys. An operator needs to know whether billing works,
    // not what the secret is.
    return this.payments.status();
  }

  @Get()
  @RequirePermission('payments.read')
  async list(
    @Query('companyId') companyId?: string,
    @Query('limit') limit?: string,
    @Query('cursor') cursor?: string,
  ): Promise<Record<string, unknown>> {
    const page = parsePageRequest({ limit, cursor });
    const result = await this.payments.list({
      ...page,
      ...(companyId ? { companyId } : {}),
    });
    return { items: result.items.map(toPaymentView), nextCursor: result.nextCursor };
  }

  @Get(':id')
  @RequirePermission('payments.read')
  async get(@Param('id') id: string): Promise<Record<string, unknown>> {
    const payment = await this.payments.get(id);
    const refunds = await this.payments.refundsFor(payment.id);
    return { ...toPaymentView(payment), refunds: refunds.map(toRefundView) };
  }

  @Post(':id/refund')
  @RequirePermission('payments.refund')
  async refund(
    @Param('id') id: string,
    @Body() body: { amountMinor?: unknown; reason?: unknown },
    @CurrentPrincipal() principal: Principal,
    @Headers('idempotency-key') idempotencyKey?: string,
  ): Promise<Record<string, unknown>> {
    const reason = typeof body.reason === 'string' ? body.reason.trim() : '';
    if (reason.length > 500) {
      throw validationFailed({ reason: ['must be 500 characters or fewer'] });
    }

    const result = await this.payments.refund({
      paymentId: id,
      amountMinor: parseRefundAmount(body.amountMinor),
      reason,
      // Recorded because a refund is the one action in this service that moves
      // money back out, and "who authorised this" is the first question asked.
      requestedBy: principal.subject,
      idempotencyKey: (idempotencyKey ?? '').trim(),
    });

    return { payment: toPaymentView(result.payment), refund: toRefundView(result.refund) };
  }
}

/* -------------------------------------------------------------------------- */
/* The company's own billing                                                  */
/* -------------------------------------------------------------------------- */

// Mounted under v1/payments, not v1/billing. The gateway routes
// /api/v1/billing to subscriptions — which owns the company's plan and its
// invoice list — so these endpoints were unreachable there. /api/v1/payments is
// already routed here for company principals as well as platform ones, which
// also reads better: this is the payment, not the subscription it settles.
@Controller('v1/payments')
@RequirePrincipalType('company')
export class CompanyPaymentsController {
  constructor(private readonly payments: PaymentsService) {}

  @Post('checkout')
  @RequirePermission('billing.manage')
  async checkout(
    // The tenant comes from the verified principal, never from the body: a
    // recruiter must not be able to buy a plan for somebody else's company.
    @CompanyId() companyId: string,
    @CurrentPrincipal() principal: Principal,
    @Body() body: { planId?: unknown },
    @Headers('idempotency-key') idempotencyKey?: string,
    @Headers('x-forwarded-host') forwardedHost?: string,
  ): Promise<Record<string, unknown>> {
    const planId = typeof body.planId === 'string' ? body.planId.trim() : '';
    if (!planId) {
      throw validationFailed({ planId: ['A plan is required.'] });
    }

    const result = await this.payments.checkout({
      companyId,
      planId,
      idempotencyKey: (idempotencyKey ?? '').trim(),
      customerEmail: principal.email,
      returnOrigin: forwardedHost ?? '',
    });

    return {
      paymentId: result.paymentId,
      provider: result.provider,
      redirectUrl: result.redirectUrl,
      amount: { minor: toJsonMinor(result.amountMinor), currency: result.currency },
      replayed: result.replayed,
    };
  }

  @Get('invoices')
  @RequirePermission('billing.read')
  async invoices(
    @CompanyId() companyId: string,
    @Query('limit') limit?: string,
    @Query('cursor') cursor?: string,
  ): Promise<Record<string, unknown>> {
    const page = await this.payments.invoicesForCompany(companyId, parsePageRequest({ limit, cursor }));
    return { items: page.items.map(toInvoiceView), nextCursor: page.nextCursor };
  }
}
