/**
 * Configuration for the admin service.
 *
 * Everything shared with the rest of the platform comes from `loadBase`, so one
 * .env drives Go and Node services alike. What is added here is specific to an
 * aggregation console: how long a dashboard snapshot may be reused, how long an
 * operator is willing to wait on a downstream probe, and how much of the audit
 * trail one export may take.
 */
import { loadBase, int, str, type BaseConfig } from '@karmarat/nestshared';

export const SERVICE_NAME = 'admin';
const DEFAULT_PORT = 8091;

/** A service this console probes on `GET /v1/admin/health`. */
export interface DownstreamService {
  name: string;
  /** Origin only; the prober appends `/healthz`. */
  baseUrl: string;
}

export interface AdminConfig extends BaseConfig {
  /** How long a dashboard snapshot is served before it is recomputed. */
  overviewTtlMs: number;
  /** Per-service budget for a health probe. */
  healthTimeoutMs: number;
  /** Ceiling on one audit export. */
  exportMaxRows: number;
  /** How long consumed events are kept before they are pruned. */
  activityRetentionDays: number;
  /** How often the pruner runs. */
  activityPruneIntervalMs: number;
  downstream: DownstreamService[];
}

export function loadConfig(): AdminConfig {
  const base = loadBase(SERVICE_NAME, DEFAULT_PORT);

  return {
    ...base,
    // loadBase falls back to a shared DATABASE_URL, which in this monorepo — where
    // the web app reads DATABASE_URL and points it at a hosted database — would
    // aim this service's migrations at somebody else's schema. A service-specific
    // override wins so that cannot happen by accident.
    postgresUrl: str('ADMIN_DATABASE_URL', base.postgresUrl),
    overviewTtlMs: int('ADMIN_OVERVIEW_TTL_MS', 30_000),
    // Deliberately short. This endpoint exists to be useful during an incident,
    // and an operator learns more from "identity did not answer in a second"
    // than from a page that eventually loads after every dead service has timed
    // out in turn.
    healthTimeoutMs: int('ADMIN_HEALTH_TIMEOUT_MS', 1_500),
    exportMaxRows: Math.min(int('ADMIN_EXPORT_MAX_ROWS', 5_000), 50_000),
    // Must stay well above the event stream's 30-day retention: the activity
    // table is also the de-duplication ledger, and pruning a row inside the
    // window a redelivery can still arrive in would let a counter double-count.
    activityRetentionDays: Math.max(int('ADMIN_ACTIVITY_RETENTION_DAYS', 365), 90),
    activityPruneIntervalMs: int('ADMIN_ACTIVITY_PRUNE_INTERVAL_MS', 6 * 60 * 60 * 1_000),
    downstream: downstreamServices(),
  };
}

/**
 * The services the console reports on.
 *
 * Read from the same `*_URL` variables the gateway routes with, so the console
 * and the router can never disagree about where a service lives. A service that
 * is not deployed yet still appears in the report — as unreachable, which is
 * the truthful answer rather than a silent omission.
 */
function downstreamServices(): DownstreamService[] {
  const targets: Array<[string, string, string]> = [
    ['identity', 'IDENTITY_URL', 'http://localhost:8081'],
    ['companies', 'COMPANIES_URL', 'http://localhost:8082'],
    ['subscriptions', 'SUBSCRIPTIONS_URL', 'http://localhost:8083'],
    ['payments', 'PAYMENTS_URL', 'http://localhost:8084'],
    ['jobs', 'JOBS_URL', 'http://localhost:8085'],
    ['applications', 'APPLICATIONS_URL', 'http://localhost:8086'],
    ['candidates', 'CANDIDATES_URL', 'http://localhost:8087'],
    ['messaging', 'MESSAGING_URL', 'http://localhost:8088'],
    ['notifications', 'NOTIFICATIONS_URL', 'http://localhost:8089'],
    ['support', 'SUPPORT_URL', 'http://localhost:8090'],
  ];

  return targets
    .map(([name, variable, fallback]) => ({ name, baseUrl: str(variable, fallback).replace(/\/+$/, '') }))
    .filter((service) => service.baseUrl !== '');
}
