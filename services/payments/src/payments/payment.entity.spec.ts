/**
 * The payment state machine, table-driven.
 *
 * These are the rules a webhook applies under concurrency and out-of-order
 * delivery, so they are tested as data rather than as prose: adding a state to
 * the enum without deciding what a redelivered success does to it should fail
 * here, not in production.
 */
import { Problem } from '@karmarat/nestshared';

import {
  isOpen,
  isSettled,
  outcomeForFailure,
  outcomeForSuccess,
  resolveRefundAmount,
  stateAfterRefund,
  type Payment,
  type PaymentState,
  type SuccessOutcome,
} from './payment.entity';

const ALL_STATES: PaymentState[] = [
  'pending',
  'processing',
  'succeeded',
  'failed',
  'refunded',
  'partially_refunded',
  'cancelled',
];

function payment(overrides: Partial<Payment> = {}): Payment {
  return {
    id: 'pay_test',
    companyId: '11111111-1111-4111-8111-111111111111',
    provider: 'manual',
    providerCheckoutId: 'mco_1',
    providerPaymentId: 'mpay_1',
    planId: 'plan_1',
    subscriptionId: null,
    amountMinor: 5_000n,
    currency: 'USD',
    refundedMinor: 0n,
    state: 'succeeded',
    failureReason: '',
    cardBrand: '',
    cardLast4: '',
    idempotencyKey: '',
    metadata: {},
    createdAt: new Date(),
    updatedAt: new Date(),
    ...overrides,
  };
}

describe('payment state predicates', () => {
  const cases: Array<{ state: PaymentState; open: boolean; settled: boolean }> = [
    { state: 'pending', open: true, settled: false },
    { state: 'processing', open: true, settled: false },
    { state: 'succeeded', open: false, settled: true },
    { state: 'failed', open: false, settled: false },
    { state: 'refunded', open: false, settled: true },
    { state: 'partially_refunded', open: false, settled: true },
    { state: 'cancelled', open: false, settled: false },
  ];

  it.each(cases)('$state is open=$open settled=$settled', ({ state, open, settled }) => {
    expect(isOpen(state)).toBe(open);
    expect(isSettled(state)).toBe(settled);
  });

  it('covers every state in the union', () => {
    expect(cases.map((c) => c.state).sort()).toEqual([...ALL_STATES].sort());
  });
});

describe('outcomeForSuccess', () => {
  // The important row is `succeeded → noop`: Stripe fans three events out for a
  // single card charge, and only one of them may credit a subscription.
  const cases: Array<{ state: PaymentState; expected: SuccessOutcome }> = [
    { state: 'pending', expected: 'apply' },
    { state: 'processing', expected: 'apply' },
    { state: 'succeeded', expected: 'noop' },
    { state: 'refunded', expected: 'noop' },
    { state: 'partially_refunded', expected: 'noop' },
    // A delayed payment method really can succeed after reporting a failure.
    { state: 'failed', expected: 'apply' },
    { state: 'cancelled', expected: 'apply' },
  ];

  it.each(cases)('$state → $expected', ({ state, expected }) => {
    expect(outcomeForSuccess(state)).toBe(expected);
  });
});

describe('outcomeForFailure', () => {
  const cases: Array<{ state: PaymentState; expected: SuccessOutcome }> = [
    { state: 'pending', expected: 'apply' },
    { state: 'processing', expected: 'apply' },
    // Money that settled is only undone by a refund, never by a late failure.
    { state: 'succeeded', expected: 'conflict' },
    { state: 'refunded', expected: 'conflict' },
    { state: 'partially_refunded', expected: 'conflict' },
    { state: 'failed', expected: 'noop' },
    { state: 'cancelled', expected: 'noop' },
  ];

  it.each(cases)('$state → $expected', ({ state, expected }) => {
    expect(outcomeForFailure(state)).toBe(expected);
  });
});

describe('stateAfterRefund', () => {
  const cases: Array<{ amount: bigint; refunded: bigint; expected: PaymentState }> = [
    { amount: 5_000n, refunded: 0n, expected: 'succeeded' },
    { amount: 5_000n, refunded: 1n, expected: 'partially_refunded' },
    { amount: 5_000n, refunded: 4_999n, expected: 'partially_refunded' },
    { amount: 5_000n, refunded: 5_000n, expected: 'refunded' },
    // Over-refunding cannot happen — the database rejects it — but if it ever
    // did, the row must not claim to be only partially refunded.
    { amount: 5_000n, refunded: 6_000n, expected: 'refunded' },
  ];

  it.each(cases)('$amount minus $refunded → $expected', ({ amount, refunded, expected }) => {
    expect(stateAfterRefund(amount, refunded)).toBe(expected);
  });
});

describe('resolveRefundAmount', () => {
  it('refunds everything outstanding when no amount is given', () => {
    const subject = payment({ state: 'partially_refunded', refundedMinor: 2_000n });
    expect(resolveRefundAmount(subject, null)).toBe(3_000n);
  });

  it('accepts a partial amount within the outstanding balance', () => {
    expect(resolveRefundAmount(payment(), 1_500n)).toBe(1_500n);
  });

  it.each(['pending', 'processing', 'failed', 'cancelled'] as PaymentState[])(
    'refuses to refund a %s payment',
    (state) => {
      expect(() => resolveRefundAmount(payment({ state }), null)).toThrow(Problem);
      try {
        resolveRefundAmount(payment({ state }), null);
      } catch (error) {
        expect((error as Problem).getStatus()).toBe(409);
      }
    },
  );

  it('refuses a payment that is already fully refunded', () => {
    const subject = payment({ state: 'refunded', refundedMinor: 5_000n });
    expect(() => resolveRefundAmount(subject, null)).toThrow(Problem);
  });

  it.each([0n, -1n, 5_001n])('refuses the invalid amount %s', (requested) => {
    expect(() => resolveRefundAmount(payment(), requested)).toThrow(Problem);
    try {
      resolveRefundAmount(payment(), requested);
    } catch (error) {
      expect((error as Problem).getStatus()).toBe(422);
    }
  });

  it('will not let two partial refunds exceed the charge', () => {
    const subject = payment({ state: 'partially_refunded', refundedMinor: 4_000n });
    expect(resolveRefundAmount(subject, 1_000n)).toBe(1_000n);
    expect(() => resolveRefundAmount(subject, 1_001n)).toThrow(Problem);
  });
});
