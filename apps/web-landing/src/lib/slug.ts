/**
 * The address rules, restated for the form.
 *
 * These duplicate `services/companies/src/companies/domain.ts`, which in turn
 * mirrors `packages/goshared/tenancy/host.go` — the generated source of truth.
 * Duplicating them is a deliberate, bounded cost: the alternative is a network
 * round trip to tell someone that "Acme Corp!" contains a space, and the
 * gateway does not currently expose the availability endpoint at all (see the
 * README). Nothing here is authoritative: the server re-checks on submit and
 * its 422 is what the form finally renders.
 *
 * If this list drifts from the generated one, the failure mode is a slug the
 * form accepts and the server rejects — visible and recoverable — rather than
 * one the form rejects and the server would have allowed.
 */

export const SLUG_MIN_LENGTH = 3;
export const SLUG_MAX_LENGTH = 40;

/** Copied from packages/nestshared/src/reserved-slugs.ts (a generated file). */
const RESERVED_SLUGS: readonly string[] = [
  "root", "admin", "jobs", "www", "api", "app", "cdn", "static", "assets",
  "mail", "smtp", "ftp", "status", "help", "support", "docs", "blog",
  "dashboard", "portal", "auth", "login", "signup", "billing", "payments",
  "internal", "system", "platform", "karmarat",
];

const SLUG_PATTERN = /^[a-z0-9]+(?:-[a-z0-9]+)*$/;

export function normaliseSlug(raw: string): string {
  return raw.trim().toLowerCase();
}

/**
 * Turns a company name into a plausible first address.
 *
 * Only a suggestion — the visitor may replace it entirely, and the field is
 * theirs the moment they touch it.
 */
export function suggestSlug(companyName: string): string {
  return companyName
    .trim()
    .toLowerCase()
    .replace(/['’]/g, "")
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/-{2,}/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, SLUG_MAX_LENGTH)
    .replace(/-+$/, "");
}

/** The reason a slug cannot be claimed, or null when it looks usable. */
export function slugProblem(raw: string): string | null {
  const slug = normaliseSlug(raw);

  if (slug.length < SLUG_MIN_LENGTH || slug.length > SLUG_MAX_LENGTH) {
    return `A company address must be between ${SLUG_MIN_LENGTH} and ${SLUG_MAX_LENGTH} characters.`;
  }
  if (RESERVED_SLUGS.includes(slug)) {
    return `“${slug}” is reserved by the platform.`;
  }
  if (slug.startsWith("-") || slug.endsWith("-")) {
    return "A company address cannot start or end with a hyphen.";
  }
  if (slug.includes("--")) {
    return "A company address cannot contain consecutive hyphens.";
  }
  if (!SLUG_PATTERN.test(slug)) {
    return "A company address may contain only lowercase letters, numbers and hyphens.";
  }
  return null;
}

/* -------------------------------------------------------------------------- */
/* Availability                                                               */
/* -------------------------------------------------------------------------- */

/**
 * What the availability route answers.
 *
 * `unknown` is a first-class answer, not a failure: the gateway rate limits
 * this check and — today — does not expose it at all, and "we could not ask"
 * must never render as "yes, it is free".
 */
export type SlugStatus = "available" | "unavailable" | "unknown";

export interface SlugCheckResult {
  slug: string;
  status: SlugStatus;
  /** Why it cannot be claimed, or why we could not say. Always safe to show. */
  reason?: string;
  /** Seconds the caller should wait before asking again. */
  retryAfter?: number;
}
