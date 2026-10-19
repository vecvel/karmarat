/**
 * Settlement, with everything below the service replaced by fakes that keep the
 * one property that matters: `provider_event_id` is unique, and the claim is an
 * insert that either wins or loses.
 *
 * The real provider is used rather than a stub, because the signature check is
 * half of what is being tested — a fake provider that "verifies" anything would
 * make the security test vacuous.
 */
import { WebhookVerificationError } from '../providers/provider-error';
import { ManualProvider, signManualWebhook } from '../providers/manual.provider';
import type { Invoice, Payment, Refund } from '../payments/payment.entity';
import { WebhooksService, describeMismatch } from './webhooks.service';

const SECRET = 'a-manual-webhook-secret';
const COMPANY = '11111111-1111-4111-8111-111111111111';

/* -------------------------------------------------------------------------- */
/* Fakes                                                                      */
/* -------------------------------------------------------------------------- */

/** withTransaction only needs BEGIN/COMMIT/ROLLBACK to be accepted. */
function fakePool() {
  const client = {
    query: async () => ({ rows: [] }),
    release: () => undefined,
  };
  return { connect: async () => client };
}

interface LedgerRow {
  id: string;
  status: string;
  ignoredReason: string;
  deliveryError: string;
}

class FakeEvents {
  /** The UNIQUE constraint, in a Map. A second claim of an id always loses. */
  readonly byProviderEventId = new Map<string, LedgerRow>();
  private sequence = 0;

  async claim(_provider: string, providerEventId: string, _type: string, _payload: unknown) {
    const existing = this.byProviderEventId.get(providerEventId);
    if (existing) {
      return { claimed: false, record: { ...existing, providerEventId } };
    }
    this.sequence += 1;
    const record: LedgerRow = {
      id: `whk_${this.sequence}`,
      status: 'received',
      ignoredReason: '',
      deliveryError: '',
    };
    this.byProviderEventId.set(providerEventId, record);
    return { claimed: true, record: { ...record, providerEventId } };
  }

  private find(id: string): LedgerRow | undefined {
    return [...this.byProviderEventId.values()].find((row) => row.id === id);
  }

  async markProcessed(id: string) {
    const row = this.find(id);
    if (row) row.status = 'processed';
  }

  async markIgnored(id: string, reason: string) {
    const row = this.find(id);
    if (row) {
      row.status = 'ignored';
      row.ignoredReason = reason;
    }
  }

  async markFailed(id: string, error: string) {
    const row = this.find(id);
    if (row) {
      row.status = 'failed';
      row.deliveryError = error;
    }
  }

  async noteDeliveryError(id: string, error: string) {
    const row = this.find(id);
    if (row) {
      row.status = 'processed';
      row.deliveryError = error;
    }
  }
}

function basePayment(overrides: Partial<Payment> = {}): Payment {
  return {
    id: 'pay_1',
    companyId: COMPANY,
    provider: 'manual',
    providerCheckoutId: 'mco_1',
    providerPaymentId: null,
    planId: 'plan_1',
    subscriptionId: null,
    amountMinor: 5_000n,
    currency: 'USD',
    refundedMinor: 0n,
    state: 'pending',
    failureReason: '',
    cardBrand: '',
    cardLast4: '',
    idempotencyKey: '',
    metadata: { planName: 'Growth' },
    createdAt: new Date(),
    updatedAt: new Date(),
    ...overrides,
  };
}

class FakePayments {
  readonly updates: Array<Record<string, unknown>> = [];
  readonly refunds: Refund[] = [];

  constructor(private payment: Payment | null) {}

  current(): Payment | null {
    return this.payment;
  }

  async findForProviderEvent() {
    return this.payment;
  }

  async findByIdForUpdate() {
    return this.payment;
  }

  async findRefundByProviderId() {
    return this.refunds[0] ?? null;
  }

  async findPendingRefund() {
    return this.refunds.find((refund) => refund.state === 'pending') ?? null;
  }

  async updateRefund(id: string, patch: Record<string, unknown>) {
    const refund = this.refunds.find((candidate) => candidate.id === id);
    if (refund && typeof patch['state'] === 'string') {
      refund.state = patch['state'] as Refund['state'];
    }
    return refund ?? null;
  }

  async settledRefundTotal() {
    return this.refunds
      .filter((refund) => refund.state !== 'failed')
      .reduce((total, refund) => total + refund.amountMinor, 0n);
  }

  async update(_id: string, patch: Record<string, unknown>) {
    this.updates.push(patch);
    if (this.payment) {
      this.payment = { ...this.payment, ...(patch as Partial<Payment>) };
    }
    return this.payment;
  }
}

class FakeInvoices {
  readonly issued: string[] = [];

  async issue(input: { paymentId: string; currency: string }): Promise<Invoice> {
    // Mirrors the real repository's per-payment uniqueness: a second issue for
    // the same payment returns the document that already exists.
    const index = this.issued.indexOf(input.paymentId);
    const ordinal = index === -1 ? this.issued.push(input.paymentId) : index + 1;
    return {
      id: `inv_${ordinal}`,
      number: `INV-2026-00000${ordinal}`,
      companyId: COMPANY,
      paymentId: input.paymentId,
      lines: [],
      subtotalMinor: 5_000n,
      taxMinor: 0n,
      totalMinor: 5_000n,
      currency: input.currency,
      issuedAt: new Date(),
      pdfKey: '',
      createdAt: new Date(),
      updatedAt: new Date(),
    };
  }
}

interface Harness {
  service: WebhooksService;
  events: FakeEvents;
  payments: FakePayments;
  invoices: FakeInvoices;
  publish: jest.Mock;
  activate: jest.Mock;
}

function harness(payment: Payment | null = basePayment()): Harness {
  const events = new FakeEvents();
  const payments = new FakePayments(payment);
  const invoices = new FakeInvoices();
  const publish = jest.fn().mockResolvedValue(undefined);
  const activate = jest.fn().mockResolvedValue({ status: 'activated', subscriptionId: 'sub_1' });

  const service = new WebhooksService(
    fakePool() as never,
    { publish } as never,
    new ManualProvider({ webhookSecret: SECRET, allowUnsigned: false }),
    events as never,
    payments as never,
    invoices as never,
    { activate } as never,
  );

  return { service, events, payments, invoices, publish, activate };
}

function delivery(payload: Record<string, unknown>): { raw: Buffer; signature: string } {
  const raw = Buffer.from(JSON.stringify(payload), 'utf8');
  return { raw, signature: signManualWebhook(raw, SECRET) };
}

function settlement(eventId: string, data: Record<string, unknown> = {}) {
  return delivery({
    id: eventId,
    type: 'payment.succeeded',
    data: {
      reference: 'pay_1',
      providerPaymentId: 'mpay_1',
      amountMinor: 5_000,
      currency: 'USD',
      ...data,
    },
  });
}

/* -------------------------------------------------------------------------- */
/* Tests                                                                      */
/* -------------------------------------------------------------------------- */

describe('webhook idempotency', () => {
  it('applies the same provider event id exactly once', async () => {
    const h = harness();
    const { raw, signature } = settlement('mev_same');

    const first = await h.service.handle(raw, signature);
    const second = await h.service.handle(raw, signature);

    expect(first.status).toBe('applied');
    // The second delivery is the one that would double-credit a subscription.
    expect(second.status).toBe('duplicate');

    expect(h.invoices.issued).toEqual(['pay_1']);
    expect(h.publish).toHaveBeenCalledTimes(1);
    expect(h.activate).toHaveBeenCalledTimes(1);
    expect(h.payments.updates.filter((patch) => patch['state'] === 'succeeded')).toHaveLength(1);
  });

  it('survives three deliveries, which is what a provider retry storm looks like', async () => {
    const h = harness();
    const { raw, signature } = settlement('mev_storm');

    const results = await Promise.all([
      h.service.handle(raw, signature),
      h.service.handle(raw, signature),
      h.service.handle(raw, signature),
    ]);

    expect(results.filter((result) => result.status === 'applied')).toHaveLength(1);
    expect(h.publish).toHaveBeenCalledTimes(1);
    expect(h.invoices.issued).toEqual(['pay_1']);
  });

  it('does not re-settle when a different event id describes an already settled payment', async () => {
    // Stripe emits checkout.session.completed, payment_intent.succeeded and
    // charge.succeeded for one card payment. Distinct ids, so the ledger lets
    // all three through; the state machine is what stops the second effect.
    const h = harness();

    const first = settlement('mev_a');
    const second = settlement('mev_b');

    expect((await h.service.handle(first.raw, first.signature)).status).toBe('applied');
    expect((await h.service.handle(second.raw, second.signature)).status).toBe('duplicate');

    expect(h.publish).toHaveBeenCalledTimes(1);
    expect(h.activate).toHaveBeenCalledTimes(1);
    expect(h.invoices.issued).toEqual(['pay_1']);
  });

  it('records the repeat against the event that already exists', async () => {
    const h = harness();
    const { raw, signature } = settlement('mev_ledger');

    await h.service.handle(raw, signature);
    await h.service.handle(raw, signature);

    expect([...h.events.byProviderEventId.keys()]).toEqual(['mev_ledger']);
    expect(h.events.byProviderEventId.get('mev_ledger')?.status).toBe('processed');
  });
});

describe('webhook signature rejection', () => {
  const cases: Array<{ name: string; signature: (valid: string) => string }> = [
    { name: 'a missing signature', signature: () => '' },
    { name: 'a signature from the wrong secret', signature: () => 'a'.repeat(64) },
    { name: 'a truncated signature', signature: (valid) => valid.slice(0, 40) },
    { name: 'a signature for a different body', signature: () => signManualWebhook(Buffer.from('{}'), SECRET) },
  ];

  it.each(cases)('refuses %s and applies nothing', async ({ signature }) => {
    const h = harness();
    const { raw, signature: valid } = settlement('mev_forged');

    await expect(h.service.handle(raw, signature(valid))).rejects.toBeInstanceOf(
      WebhookVerificationError,
    );

    // Nothing was claimed, nothing was settled, nothing was announced. An
    // unauthenticated caller must not even be able to fill the event ledger.
    expect(h.events.byProviderEventId.size).toBe(0);
    expect(h.payments.updates).toEqual([]);
    expect(h.publish).not.toHaveBeenCalled();
  });

  it('refuses a body edited after it was signed', async () => {
    const h = harness();
    const honest = settlement('mev_edit');
    const tampered = settlement('mev_edit', { amountMinor: 1 });

    await expect(h.service.handle(tampered.raw, honest.signature)).rejects.toBeInstanceOf(
      WebhookVerificationError,
    );
    expect(h.publish).not.toHaveBeenCalled();
  });
});

describe('webhook outcomes that are not settlements', () => {
  it('ignores an event type it has no handler for, and says why', async () => {
    const h = harness();
    const { raw, signature } = delivery({ id: 'mev_unknown', type: 'customer.updated', data: {} });

    const result = await h.service.handle(raw, signature);

    // 200 with a reason, not an error: a non-2xx would be retried forever.
    expect(result.status).toBe('ignored');
    expect(h.events.byProviderEventId.get('mev_unknown')?.ignoredReason).toContain('customer.updated');
    expect(h.publish).not.toHaveBeenCalled();
  });

  it('ignores a settlement for a payment it does not have', async () => {
    const h = harness(null);
    const { raw, signature } = settlement('mev_orphan');

    expect((await h.service.handle(raw, signature)).status).toBe('ignored');
    expect(h.publish).not.toHaveBeenCalled();
  });

  it('refuses to settle when the provider reports a different amount', async () => {
    const h = harness();
    const { raw, signature } = settlement('mev_mismatch', { amountMinor: 100 });

    const result = await h.service.handle(raw, signature);

    expect(result.status).toBe('failed');
    expect(h.events.byProviderEventId.get('mev_mismatch')?.status).toBe('failed');
    expect(h.invoices.issued).toEqual([]);
    expect(h.publish).not.toHaveBeenCalled();
  });

  it('marks a failure and announces it', async () => {
    const h = harness();
    const { raw, signature } = delivery({
      id: 'mev_declined',
      type: 'payment.failed',
      data: { reference: 'pay_1', failureReason: 'Your card was declined.' },
    });

    expect((await h.service.handle(raw, signature)).status).toBe('applied');
    expect(h.payments.current()?.state).toBe('failed');
    expect(h.publish).toHaveBeenCalledTimes(1);
    expect(h.publish.mock.calls[0]?.[0]).toBe('karmarat.payment.failed');
  });

  it('does not let a late failure reverse money that already settled', async () => {
    const h = harness(basePayment({ state: 'succeeded' }));
    const { raw, signature } = delivery({
      id: 'mev_late_failure',
      type: 'payment.failed',
      data: { reference: 'pay_1', failureReason: 'Your card was declined.' },
    });

    expect((await h.service.handle(raw, signature)).status).toBe('ignored');
    expect(h.payments.current()?.state).toBe('succeeded');
    expect(h.publish).not.toHaveBeenCalled();
  });

  it('cancels an abandoned checkout without announcing anything', async () => {
    const h = harness();
    const { raw, signature } = delivery({
      id: 'mev_expired',
      type: 'checkout.cancelled',
      data: { reference: 'pay_1' },
    });

    expect((await h.service.handle(raw, signature)).status).toBe('applied');
    expect(h.payments.current()?.state).toBe('cancelled');
    expect(h.publish).not.toHaveBeenCalled();
  });
});

describe('downstream failures do not fail the webhook', () => {
  it('still reports success when the bus is unreachable', async () => {
    const h = harness();
    h.publish.mockRejectedValue(new Error('nats: no servers available'));
    const { raw, signature } = settlement('mev_no_bus');

    // The money has moved; telling the provider to retry would re-deliver an
    // event we have already applied.
    expect((await h.service.handle(raw, signature)).status).toBe('applied');
    expect(h.events.byProviderEventId.get('mev_no_bus')?.deliveryError).toContain('publish');
  });

  it('records, but does not fail on, a subscriptions outage', async () => {
    const h = harness();
    h.activate.mockResolvedValue({ status: 'failed', detail: 'subscriptions could not be reached' });
    const { raw, signature } = settlement('mev_no_subs');

    expect((await h.service.handle(raw, signature)).status).toBe('applied');
    expect(h.events.byProviderEventId.get('mev_no_subs')?.deliveryError).toContain('activate');
  });

  it('treats a missing activation endpoint as settled, relying on the event', async () => {
    const h = harness();
    h.activate.mockResolvedValue({ status: 'unsupported', detail: 'no such route' });
    const { raw, signature } = settlement('mev_unsupported');

    expect((await h.service.handle(raw, signature)).status).toBe('applied');
    // Not queued for replay: that endpoint will answer the same way forever.
    expect(h.events.byProviderEventId.get('mev_unsupported')?.deliveryError).toBe('');
  });
});

describe('describeMismatch', () => {
  const payment = basePayment({ amountMinor: 5_000n, currency: 'USD' });

  const cases: Array<{ name: string; amount: bigint; currency: string; expected: boolean }> = [
    { name: 'an exact match', amount: 5_000n, currency: 'USD', expected: false },
    { name: 'a lower-case currency', amount: 5_000n, currency: 'usd', expected: false },
    // The manual provider settling a comped plan echoes nothing back.
    { name: 'an omitted amount and currency', amount: 0n, currency: '', expected: false },
    { name: 'a different amount', amount: 4_999n, currency: 'USD', expected: true },
    { name: 'a different currency', amount: 5_000n, currency: 'EUR', expected: true },
  ];

  it.each(cases)('$name → mismatch=$expected', ({ amount, currency, expected }) => {
    expect(describeMismatch(payment, amount, currency) !== null).toBe(expected);
  });
});
