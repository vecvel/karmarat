/**
 * The public plan catalogue, as a pricing page needs it.
 *
 * The shapes mirror `toPublicPlanView` in services/subscriptions. Nothing here
 * hard-codes a plan: the platform team edits the catalogue in the admin console
 * and this page renders whatever comes back, including nothing.
 */

export type PlanInterval = "month" | "year" | "days" | "lifetime";

export interface Entitlements {
  maxJobs: number | null;
  maxRecruiters: number | null;
  maxApplicationsPerMonth: number | null;
  canPublishToNetwork: boolean;
  canUseTalentSearch: boolean;
  canUseMessaging: boolean;
  supportTier: "community" | "standard" | "priority" | "dedicated";
  storageGb: number;
}

export interface PublicPlan {
  id: string;
  key: string;
  name: string;
  description: string;
  price: { amount: number; currency: string };
  interval: PlanInterval;
  intervalCount: number;
  trialDays: number;
  entitlements: Entitlements;
}

export interface PublicPlansResponse {
  items: PublicPlan[];
}

/* -------------------------------------------------------------------------- */
/* Money                                                                      */
/* -------------------------------------------------------------------------- */

/**
 * Renders a price held as minor units.
 *
 * The API stores amounts as integers in the currency's smallest unit, so 4900
 * USD is $49.00 and 4900 JPY is ¥4,900 — the exponent is a property of the
 * currency, not a constant 100. `Intl` already knows every exponent, so it is
 * asked rather than a table being maintained here.
 */
export function formatMoney(minorUnits: number, currency: string): string {
  const code = currency.toUpperCase();

  let exponent = 2;
  try {
    // Typed as optional because the generic NumberFormat options are, but a
    // currency-style formatter always resolves it.
    exponent =
      new Intl.NumberFormat("en", {
        style: "currency",
        currency: code,
      }).resolvedOptions().maximumFractionDigits ?? 2;
  } catch {
    // An unknown or malformed currency code should not take the pricing page
    // down; fall through to the two-decimal default and the raw code.
    return `${(minorUnits / 100).toFixed(2)} ${code}`;
  }

  const major = minorUnits / 10 ** exponent;

  try {
    return new Intl.NumberFormat("en", {
      style: "currency",
      currency: code,
      // A plan priced at a round number reads better without the decimals, but
      // 49.50 must never render as 49 or 50.
      minimumFractionDigits: Number.isInteger(major) ? 0 : exponent,
      maximumFractionDigits: exponent,
    }).format(major);
  } catch {
    return `${major} ${code}`;
  }
}

/**
 * How often the price above is charged.
 *
 * Said plainly rather than as a symbol: "one-off, lifetime access" is a
 * different promise from "per month" and a "/mo" suffix cannot express it.
 */
export function formatInterval(interval: PlanInterval, count: number): string {
  switch (interval) {
    case "lifetime":
      return "one-off, lifetime access";
    case "month":
      return count === 1 ? "per month" : `every ${count} months`;
    case "year":
      return count === 1 ? "per year" : `every ${count} years`;
    case "days":
      return count === 1 ? "for 1 day" : `for ${count} days`;
  }
}

/* -------------------------------------------------------------------------- */
/* Entitlements                                                               */
/* -------------------------------------------------------------------------- */

/**
 * One line of a plan's feature list.
 *
 * `included: false` lines are kept rather than dropped, because a limit of zero
 * is a fact about the plan. Someone comparing two plans needs to see that the
 * cheaper one has no talent search, not merely fail to see that it has one.
 */
export interface PlanFeature {
  label: string;
  included: boolean;
}

/** `null` means unlimited; `0` means none. The two must never read alike. */
function limitLine(limit: number | null, singular: string, plural: string): PlanFeature {
  if (limit === null) return { label: `Unlimited ${plural}`, included: true };
  if (limit === 0) return { label: `No ${plural}`, included: false };
  if (limit === 1) return { label: `1 ${singular}`, included: true };
  return { label: `Up to ${limit.toLocaleString("en")} ${plural}`, included: true };
}

const SUPPORT_LABELS: Record<Entitlements["supportTier"], string> = {
  community: "Community support",
  standard: "Standard support",
  priority: "Priority support",
  dedicated: "Dedicated support manager",
};

export function planFeatures(entitlements: Entitlements): PlanFeature[] {
  return [
    limitLine(entitlements.maxJobs, "job posting", "job postings"),
    limitLine(entitlements.maxRecruiters, "recruiter seat", "recruiter seats"),
    limitLine(
      entitlements.maxApplicationsPerMonth,
      "application per month",
      "applications per month",
    ),
    {
      label: "Publish to the Karmarat job network",
      included: entitlements.canPublishToNetwork,
    },
    { label: "Talent search", included: entitlements.canUseTalentSearch },
    { label: "Candidate messaging", included: entitlements.canUseMessaging },
    {
      label:
        entitlements.storageGb === 0
          ? "No file storage"
          : `${entitlements.storageGb.toLocaleString("en")} GB file storage`,
      included: entitlements.storageGb > 0,
    },
    { label: SUPPORT_LABELS[entitlements.supportTier], included: true },
  ];
}
