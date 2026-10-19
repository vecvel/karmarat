/**
 * Payments configuration.
 *
 * Extends the platform's `loadBase` rather than reading its own POSTGRES_/NATS_
 * variables, so one .env drives every service and an operator never has to
 * remember which runtime a service happens to be written in.
 */
import { bool, int, loadBase, str, type BaseConfig } from '@karmarat/nestshared';

export const SERVICE_NAME = 'payments';
export const DEFAULT_PORT = 8084;

export type ProviderName = 'stripe' | 'manual';

export interface PaymentsConfig extends BaseConfig {
  /** The provider this process will actually use — see `resolveProvider`. */
  provider: ProviderName;
  /** What PAYMENT_PROVIDER asked for, which may differ when Stripe is unconfigured. */
  requestedProvider: ProviderName;
  stripe: {
    secretKey: string;
    webhookSecret: string;
  };
  manual: {
    webhookSecret: string;
    /** Development only: accept unsigned manual webhooks. Never true in production. */
    allowUnsigned: boolean;
  };
  subscriptionsUrl: string;
  /** Deadline on a call to subscriptions; a hung peer must not hold a webhook open. */
  subscriptionsTimeoutMs: number;
  /** Scheme used to build a tenant's return URL. */
  urlScheme: string;
  /** Path on the company portal a finished checkout returns to. */
  checkoutReturnPath: string;
  defaultCurrency: string;
}

export function loadConfig(): PaymentsConfig {
  const base = loadBase(SERVICE_NAME, DEFAULT_PORT);

  const requested = normaliseProvider(str('PAYMENT_PROVIDER', 'manual'));
  const stripeSecret = str('STRIPE_SECRET_KEY');
  const stripeWebhookSecret = str('STRIPE_WEBHOOK_SECRET');

  // Stripe asked for but not configured: in production this is a deployment
  // mistake that would take every upgrade attempt down, so fail at boot rather
  // than at the first checkout. Outside production, fall back to the manual
  // provider — a developer with no Stripe account must still be able to walk
  // the whole billing flow, which is the entire reason the abstraction exists.
  let provider = requested;
  if (requested === 'stripe' && (stripeSecret === '' || stripeWebhookSecret === '')) {
    if (base.environment === 'production') {
      throw new Error(
        'config: PAYMENT_PROVIDER=stripe requires STRIPE_SECRET_KEY and STRIPE_WEBHOOK_SECRET',
      );
    }
    provider = 'manual';
  }

  const manualSecret = str('MANUAL_WEBHOOK_SECRET');
  if (provider === 'manual' && manualSecret === '' && base.environment === 'production') {
    // A manual provider with no secret would accept any body that reached the
    // public webhook route as a settled payment.
    throw new Error('config: PAYMENT_PROVIDER=manual requires MANUAL_WEBHOOK_SECRET outside development');
  }

  return {
    ...base,
    // loadBase falls back to a shared DATABASE_URL, which in a monorepo where
    // the web app also reads DATABASE_URL would point this service's migrations
    // at somebody else's database. A service-specific override wins so that
    // cannot happen by accident.
    postgresUrl: str('PAYMENTS_DATABASE_URL', base.postgresUrl),
    provider,
    requestedProvider: requested,
    stripe: { secretKey: stripeSecret, webhookSecret: stripeWebhookSecret },
    manual: {
      webhookSecret: manualSecret,
      allowUnsigned: manualSecret === '' && base.environment !== 'production',
    },
    subscriptionsUrl: str('SUBSCRIPTIONS_URL', 'http://localhost:8083'),
    subscriptionsTimeoutMs: int('SUBSCRIPTIONS_TIMEOUT_MS', 5_000),
    urlScheme: str('PLATFORM_URL_SCHEME', base.environment === 'development' ? 'http' : 'https'),
    checkoutReturnPath: str('PAYMENT_RETURN_PATH', '/settings/billing'),
    defaultCurrency: str('PAYMENT_DEFAULT_CURRENCY', 'USD').toUpperCase(),
  };
}

function normaliseProvider(raw: string): ProviderName {
  const value = raw.trim().toLowerCase();
  if (value === 'stripe' || value === 'manual') {
    return value;
  }
  throw new Error(`config: PAYMENT_PROVIDER must be "stripe" or "manual", got "${raw}"`);
}

/** DI token — the config object is a value, not a class Nest can construct. */
export const PAYMENTS_CONFIG = Symbol('PAYMENTS_CONFIG');

/** Exported for tests that want a config without touching process.env. */
export const isProduction = (config: PaymentsConfig): boolean => config.environment === 'production';

/** Kept for symmetry with the Go services' config helpers. */
export const debugEnabled = (): boolean => bool('PAYMENTS_DEBUG', false);
