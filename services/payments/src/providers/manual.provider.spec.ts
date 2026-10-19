/**
 * The manual provider is the default, so its signature check is the one that
 * actually runs on every local deployment. A route that anyone on the internet
 * can POST to, that grants subscriptions, is only as safe as this file.
 */
import { ManualProvider, signManualWebhook } from './manual.provider';
import { ProviderUnsupportedError, WebhookVerificationError } from './provider-error';

const SECRET = 'a-manual-webhook-secret';

function body(payload: Record<string, unknown>): Buffer {
  return Buffer.from(JSON.stringify(payload), 'utf8');
}

function settlement(overrides: Record<string, unknown> = {}): Buffer {
  return body({
    id: 'mev_1',
    type: 'payment.succeeded',
    data: {
      reference: 'pay_1',
      providerPaymentId: 'mpay_1',
      amountMinor: 5_000,
      currency: 'usd',
      ...overrides,
    },
  });
}

describe('ManualProvider signature verification', () => {
  const provider = new ManualProvider({ webhookSecret: SECRET, allowUnsigned: false });

  it('accepts a body signed with the shared secret', () => {
    const raw = settlement();
    const event = provider.verifyWebhook(raw, signManualWebhook(raw, SECRET));

    expect(event.kind).toBe('payment.succeeded');
    expect(event.id).toBe('mev_1');
    expect(event.payment?.reference).toBe('pay_1');
    expect(event.payment?.amountMinor).toBe(5_000n);
    // Currencies are normalised at the boundary so nothing downstream compares
    // "usd" against "USD" and decides a settled payment is a mismatch.
    expect(event.payment?.currency).toBe('USD');
  });

  const rejected: Array<{ name: string; signature: (raw: Buffer) => string }> = [
    { name: 'a signature from the wrong secret', signature: (raw) => signManualWebhook(raw, 'not-it') },
    { name: 'an empty signature', signature: () => '' },
    { name: 'a truncated digest', signature: (raw) => signManualWebhook(raw, SECRET).slice(0, 32) },
    { name: 'a digest with one flipped character', signature: (raw) => flipFirst(signManualWebhook(raw, SECRET)) },
    { name: 'a non-hex signature', signature: () => 'not-a-hex-digest' },
  ];

  it.each(rejected)('rejects $name', ({ signature }) => {
    const raw = settlement();
    expect(() => provider.verifyWebhook(raw, signature(raw))).toThrow(WebhookVerificationError);
  });

  it('rejects a body that was modified after signing', () => {
    // The whole reason the route keeps raw bytes: re-serialising the payload
    // changes them, and an attacker editing the amount changes them too.
    const original = settlement();
    const signature = signManualWebhook(original, SECRET);
    const tampered = settlement({ amountMinor: 1 });

    expect(() => provider.verifyWebhook(tampered, signature)).toThrow(WebhookVerificationError);
  });

  it('accepts the sha256= prefix most tooling emits', () => {
    const raw = settlement();
    const event = provider.verifyWebhook(raw, `sha256=${signManualWebhook(raw, SECRET)}`);
    expect(event.kind).toBe('payment.succeeded');
  });

  it('fails closed when no secret is configured', () => {
    const unconfigured = new ManualProvider({ webhookSecret: '', allowUnsigned: false });
    const raw = settlement();
    expect(() => unconfigured.verifyWebhook(raw, signManualWebhook(raw, SECRET))).toThrow(
      WebhookVerificationError,
    );
  });

  it('refuses a body with no event id, because that is the idempotency key', () => {
    const raw = body({ type: 'payment.succeeded', data: {} });
    expect(() => provider.verifyWebhook(raw, signManualWebhook(raw, SECRET))).toThrow(
      WebhookVerificationError,
    );
  });

  it('refuses a body that is not json', () => {
    const raw = Buffer.from('not json at all', 'utf8');
    expect(() => provider.verifyWebhook(raw, signManualWebhook(raw, SECRET))).toThrow(
      WebhookVerificationError,
    );
  });
});

describe('ManualProvider event translation', () => {
  const provider = new ManualProvider({ webhookSecret: SECRET, allowUnsigned: false });

  const verify = (payload: Record<string, unknown>) => {
    const raw = body(payload);
    return provider.verifyWebhook(raw, signManualWebhook(raw, SECRET));
  };

  it.each([
    ['payment.succeeded', 'payment.succeeded'],
    ['payment.failed', 'payment.failed'],
    ['checkout.cancelled', 'checkout.cancelled'],
    ['refund.succeeded', 'refund.succeeded'],
    ['refund.failed', 'refund.failed'],
    ['something.else', 'unhandled'],
  ])('translates %s to %s', (type, kind) => {
    expect(verify({ id: 'mev_x', type, data: {} }).kind).toBe(kind);
  });

  it('reports an unparseable amount as zero rather than throwing', () => {
    // The handler compares the amount against the payment row and rejects a
    // mismatch with a reason; a throw here would be a 500 on a public route.
    const event = verify({ id: 'mev_y', type: 'payment.succeeded', data: { amountMinor: 'lots' } });
    expect(event.payment?.amountMinor).toBe(0n);
  });
});

describe('ManualProvider capabilities', () => {
  it('reports itself unconfigured without a secret, so /config tells the truth', () => {
    expect(new ManualProvider({ webhookSecret: '', allowUnsigned: true }).isConfigured()).toBe(false);
    expect(new ManualProvider({ webhookSecret: SECRET, allowUnsigned: false }).isConfigured()).toBe(true);
  });

  it('opens a checkout that returns straight to the portal, still pending', async () => {
    const provider = new ManualProvider({ webhookSecret: SECRET, allowUnsigned: false });
    const session = await provider.createCheckout({
      paymentId: 'pay_1',
      companyId: '11111111-1111-4111-8111-111111111111',
      planId: 'plan_1',
      planName: 'Growth',
      amountMinor: 5_000n,
      currency: 'USD',
      customerEmail: 'someone@example.test',
      successUrl: 'https://acme.karmarat.local/settings/billing?payment=pay_1',
      cancelUrl: 'https://acme.karmarat.local/settings/billing?payment=pay_1',
    });

    const url = new URL(session.redirectUrl);
    expect(url.searchParams.get('provider')).toBe('manual');
    // Nothing has been paid; claiming otherwise is how a comped account shows
    // up as collected revenue.
    expect(url.searchParams.get('status')).toBe('pending');
    expect(session.checkoutId).toMatch(/^mco_/);
  });

  it('records a refund as settled, because there is nothing to wait for', async () => {
    const provider = new ManualProvider({ webhookSecret: SECRET, allowUnsigned: false });
    const refund = await provider.refund('mpay_1', 2_500n);
    expect(refund.state).toBe('succeeded');
    expect(refund.amountMinor).toBe(2_500n);
  });

  it('reports reading a payment back as unsupported, not as a failure', async () => {
    const provider = new ManualProvider({ webhookSecret: SECRET, allowUnsigned: false });
    await expect(provider.getPayment('mpay_1')).rejects.toBeInstanceOf(ProviderUnsupportedError);
  });
});

function flipFirst(hex: string): string {
  const first = hex[0] === '0' ? '1' : '0';
  return first + hex.slice(1);
}
