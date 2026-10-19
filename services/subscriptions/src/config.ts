/**
 * Service configuration.
 *
 * Everything shared with the rest of the platform comes from `loadBase`, so one
 * .env drives Go and Node services alike. Only the settings this service alone
 * has are declared here.
 */
import { int, loadBase, str, type BaseConfig } from '@karmarat/nestshared';

export const SERVICE_NAME = 'subscriptions';
export const DEFAULT_PORT = 8083;

export interface SubscriptionsConfig extends BaseConfig {
  /** How often the expiry sweep runs. */
  sweepIntervalMs: number;
  /**
   * How long a past_due subscription keeps the portal open.
   *
   * A failed card should not lock a recruiter out mid-interview; it should give
   * finance a few days to fix it and then close.
   */
  pastDueGraceDays: number;
}

export function loadConfig(): SubscriptionsConfig {
  const base = loadBase(SERVICE_NAME, DEFAULT_PORT);

  return {
    ...base,
    // loadBase falls back to a shared DATABASE_URL, which in a monorepo where
    // the web app also reads DATABASE_URL would point this service's migrations
    // at somebody else's database. A service-specific override wins so that
    // cannot happen by accident.
    postgresUrl: str('SUBSCRIPTIONS_DATABASE_URL', base.postgresUrl),
    sweepIntervalMs: int('SUBSCRIPTIONS_SWEEP_INTERVAL_MS', 60_000),
    pastDueGraceDays: int('SUBSCRIPTIONS_PAST_DUE_GRACE_DAYS', 7),
  };
}

export const CONFIG = 'SUBSCRIPTIONS_CONFIG';
