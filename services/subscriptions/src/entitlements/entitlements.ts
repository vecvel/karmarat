/**
 * The entitlement document.
 *
 * Other services read this map to decide whether a gated action is allowed, so
 * its shape is a platform contract, not an internal detail. It is validated on
 * every write — into a plan and into a subscription snapshot — because a typo
 * in an admin form ("maxJob") would otherwise be stored happily and then read
 * as "no limit configured", which fails open. Validating at the boundary means
 * a missing limit is impossible rather than merely unlikely.
 */
import { validationFailed } from '@karmarat/nestshared';

/** `null` on a numeric limit means unlimited; 0 means none allowed. */
export interface Entitlements {
  maxJobs: number | null;
  maxRecruiters: number | null;
  maxApplicationsPerMonth: number | null;
  canPublishToNetwork: boolean;
  canUseTalentSearch: boolean;
  canUseMessaging: boolean;
  supportTier: SupportTier;
  storageGb: number;
}

export const SUPPORT_TIERS = ['community', 'standard', 'priority', 'dedicated'] as const;
export type SupportTier = (typeof SUPPORT_TIERS)[number];

const NUMERIC_LIMITS = ['maxJobs', 'maxRecruiters', 'maxApplicationsPerMonth'] as const;
const FLAGS = ['canPublishToNetwork', 'canUseTalentSearch', 'canUseMessaging'] as const;

const KNOWN_KEYS = new Set<string>([...NUMERIC_LIMITS, ...FLAGS, 'supportTier', 'storageGb']);

/**
 * What a company with no subscription at all may do.
 *
 * Every flag is off and every limit is zero: the absence of a plan must never
 * read as unlimited access.
 */
export const NO_ENTITLEMENTS: Entitlements = Object.freeze({
  maxJobs: 0,
  maxRecruiters: 0,
  maxApplicationsPerMonth: 0,
  canPublishToNetwork: false,
  canUseTalentSearch: false,
  canUseMessaging: false,
  supportTier: 'community',
  storageGb: 0,
});

/**
 * Parses and validates an entitlement map.
 *
 * Unknown keys are rejected rather than dropped. A key nothing reads is either
 * a typo — in which case silence hides a real limit being unset — or a feature
 * somebody expected this service to enforce and it never will.
 */
export function parseEntitlements(raw: unknown, field = 'entitlements'): Entitlements {
  const errors: Record<string, string[]> = {};
  const add = (key: string, message: string) => {
    (errors[`${field}.${key}`] ??= []).push(message);
  };

  if (typeof raw !== 'object' || raw === null || Array.isArray(raw)) {
    throw validationFailed({ [field]: ['must be an object of entitlement limits.'] });
  }

  const source = raw as Record<string, unknown>;

  for (const key of Object.keys(source)) {
    if (!KNOWN_KEYS.has(key)) {
      add(key, 'is not a recognised entitlement.');
    }
  }

  const limits: Record<string, number | null> = {};
  for (const key of NUMERIC_LIMITS) {
    const value = source[key];
    if (value === null) {
      limits[key] = null; // explicit "unlimited"
    } else if (value === undefined) {
      add(key, 'is required (use null for unlimited).');
    } else if (typeof value !== 'number' || !Number.isInteger(value) || value < 0) {
      add(key, 'must be a non-negative integer, or null for unlimited.');
    } else {
      limits[key] = value;
    }
  }

  const flags: Record<string, boolean> = {};
  for (const key of FLAGS) {
    const value = source[key];
    if (typeof value !== 'boolean') {
      add(key, 'must be true or false.');
    } else {
      flags[key] = value;
    }
  }

  const tier = source['supportTier'];
  if (typeof tier !== 'string' || !(SUPPORT_TIERS as readonly string[]).includes(tier)) {
    add('supportTier', `must be one of ${SUPPORT_TIERS.join(', ')}.`);
  }

  const storage = source['storageGb'];
  if (typeof storage !== 'number' || !Number.isInteger(storage) || storage < 0) {
    add('storageGb', 'must be a non-negative integer number of gigabytes.');
  }

  if (Object.keys(errors).length > 0) {
    throw validationFailed(errors);
  }

  return {
    maxJobs: limits['maxJobs'] ?? null,
    maxRecruiters: limits['maxRecruiters'] ?? null,
    maxApplicationsPerMonth: limits['maxApplicationsPerMonth'] ?? null,
    canPublishToNetwork: flags['canPublishToNetwork']!,
    canUseTalentSearch: flags['canUseTalentSearch']!,
    canUseMessaging: flags['canUseMessaging']!,
    supportTier: tier as SupportTier,
    storageGb: storage as number,
  };
}

/**
 * Reads a map back out of JSONB.
 *
 * A stored document predates any later validation change, so a row that no
 * longer parses must not take the read path down with it — the caller gets the
 * safe floor instead, which denies rather than grants.
 */
export function readStoredEntitlements(raw: unknown): Entitlements {
  try {
    return parseEntitlements(raw);
  } catch {
    return { ...NO_ENTITLEMENTS };
  }
}

/* -------------------------------------------------------------------------- */
/* Usage metrics                                                              */
/* -------------------------------------------------------------------------- */

/**
 * The metrics a caller may report, and the limit each one is checked against.
 *
 * `monthly` metrics reset with the calendar month; the rest are running totals
 * that go up and down as records are created and deleted.
 */
export const USAGE_METRICS = {
  jobs: { limit: 'maxJobs', monthly: false },
  recruiters: { limit: 'maxRecruiters', monthly: false },
  applications: { limit: 'maxApplicationsPerMonth', monthly: true },
  storage_gb: { limit: 'storageGb', monthly: false },
} as const satisfies Record<string, { limit: keyof Entitlements; monthly: boolean }>;

export type UsageMetric = keyof typeof USAGE_METRICS;

export function isUsageMetric(value: string): value is UsageMetric {
  return Object.prototype.hasOwnProperty.call(USAGE_METRICS, value);
}

/** The ceiling a metric is measured against; `null` is unlimited. */
export function limitFor(entitlements: Entitlements, metric: UsageMetric): number | null {
  const value = entitlements[USAGE_METRICS[metric].limit];
  return typeof value === 'number' ? value : null;
}

export type UsageCounts = Partial<Record<UsageMetric, number>>;

/**
 * Whether each metric is still inside its limit.
 *
 * Reported per metric rather than as one boolean: a caller about to publish a
 * job needs to know that *jobs* are at the cap, and refusing it because storage
 * is full would be both wrong and baffling.
 */
export function withinLimits(
  entitlements: Entitlements,
  usage: UsageCounts,
): Record<UsageMetric, boolean> {
  const result = {} as Record<UsageMetric, boolean>;
  for (const metric of Object.keys(USAGE_METRICS) as UsageMetric[]) {
    const limit = limitFor(entitlements, metric);
    result[metric] = limit === null || (usage[metric] ?? 0) <= limit;
  }
  return result;
}
