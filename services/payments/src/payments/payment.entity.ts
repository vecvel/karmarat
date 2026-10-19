/**
 * The payment aggregate, and the rules about what may happen to one.
 *
 * The state machine lives here rather than in the service because it is the one
 * part of billing that must be reasoned about without a database: "may this
 * webhook change this row" is a question about two values, and a question about
 * two values should be answerable by a table-driven test.
 */
import { conflict, validationFailed } from '@karmarat/nestshared';

export type PaymentState =
  | 'pending'
  | 'processing'
  | 'succeeded'
  | 'failed'
  | 'refunded'
  | 'partially_refunded'
  | 'cancelled';

export type RefundState = 'pending' | 'succeeded' | 'failed';

export interface Payment {
  id: string;
  companyId: string;
  provider: string;
  providerCheckoutId: string | null;
  providerPaymentId: string | null;
  planId: string;
  subscriptionId: string | null;
  amountMinor: bigint;
  currency: string;
  refundedMinor: bigint;
  state: PaymentState;
  failureReason: string;
  cardBrand: string;
  cardLast4: string;
  idempotencyKey: string;
  metadata: Record<string, unknown>;
  createdAt: Date;
  updatedAt: Date;
}

export interface Refund {
  id: string;
  paymentId: string;
  companyId: string;
  provider: string;
  providerRefundId: string | null;
  amountMinor: bigint;
  currency: string;
  state: RefundState;
  reason: string;
  requestedBy: string;
  idempotencyKey: string;
  createdAt: Date;
  updatedAt: Date;
}

export interface InvoiceLine {
  description: string;
  quantity: number;
  unitAmountMinor: number;
  amountMinor: number;
}

export interface Invoice {
  id: string;
  number: string;
  companyId: string;
  paymentId: string | null;
  lines: InvoiceLine[];
  subtotalMinor: bigint;
  taxMinor: bigint;
  totalMinor: bigint;
  currency: string;
  issuedAt: Date;
  pdfKey: string;
  createdAt: Date;
  updatedAt: Date;
}

/**
 * States from which money has already moved and cannot un-move.
 *
 * A provider that redelivers `payment_intent.succeeded` after we have already
 * refunded must not drag the row back to `succeeded` — the refund is the later
 * truth, and webhook ordering is not guaranteed.
 */
const SETTLED: ReadonlySet<PaymentState> = new Set([
  'succeeded',
  'refunded',
  'partially_refunded',
]);

/** States a payment can still leave for a different outcome. */
const OPEN: ReadonlySet<PaymentState> = new Set(['pending', 'processing']);

export function isSettled(state: PaymentState): boolean {
  return SETTLED.has(state);
}

export function isOpen(state: PaymentState): boolean {
  return OPEN.has(state);
}

/**
 * What a `payment.succeeded` webhook should do to a row in this state.
 *
 * `noop` is the idempotent answer and the common one: Stripe emits
 * `checkout.session.completed`, `payment_intent.succeeded` and
 * `charge.succeeded` for a single card payment, and the event ledger only
 * de-duplicates *identical* event ids. Deciding by current state is what makes
 * three different events credit a subscription once.
 */
export type SuccessOutcome = 'apply' | 'noop' | 'conflict';

export function outcomeForSuccess(state: PaymentState): SuccessOutcome {
  if (isOpen(state)) return 'apply';
  if (SETTLED.has(state)) return 'noop';
  // failed or cancelled: the provider has changed its mind, which happens with
  // delayed payment methods. Applying it is correct; refusing it would strand a
  // customer who has genuinely paid.
  return 'apply';
}

/** A failure may only overwrite a payment that has not settled. */
export function outcomeForFailure(state: PaymentState): SuccessOutcome {
  if (isOpen(state)) return 'apply';
  if (SETTLED.has(state)) return 'conflict';
  return 'noop';
}

/**
 * The state a payment lands in once `refunded` of `amount` has been returned.
 *
 * Computed rather than stored as a flag so the row cannot disagree with its own
 * numbers — a `refunded` payment with a refunded total below its amount is the
 * kind of inconsistency that only shows up in a revenue report.
 */
export function stateAfterRefund(amountMinor: bigint, refundedMinor: bigint): PaymentState {
  if (refundedMinor <= 0n) return 'succeeded';
  return refundedMinor >= amountMinor ? 'refunded' : 'partially_refunded';
}

/**
 * Validates a refund request against the payment it names.
 *
 * Returns the amount to refund: an omitted amount means "everything still
 * outstanding", which is what an operator pressing Refund almost always wants.
 */
export function resolveRefundAmount(payment: Payment, requested: bigint | null): bigint {
  if (!isSettled(payment.state)) {
    throw conflict(
      'payment_not_refundable',
      'Only a payment that has succeeded can be refunded.',
    );
  }

  const outstanding = payment.amountMinor - payment.refundedMinor;
  if (outstanding <= 0n) {
    throw conflict('payment_fully_refunded', 'This payment has already been fully refunded.');
  }

  if (requested === null) {
    return outstanding;
  }
  if (requested <= 0n) {
    throw validationFailed({ amountMinor: ['must be greater than zero'] });
  }
  if (requested > outstanding) {
    throw validationFailed({
      amountMinor: ['cannot exceed the amount of this payment that is still outstanding'],
    });
  }
  return requested;
}
