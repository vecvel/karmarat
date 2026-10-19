/**
 * Subdomains a company may not register.
 *
 * GENERATED FILE — do not edit. The list is owned by
 * packages/goshared/tenancy/host.go, because slug validation and host routing
 * must never disagree about what is reserved. Regenerate with:
 *
 *   scripts/gen-reserved-slugs.sh
 */
export const RESERVED_SLUGS: readonly string[] = [
  'root', 'admin', 'jobs', 'www', 'api', 'app', 'cdn', 'static', 'assets', 
  'mail', 'smtp', 'ftp', 'status', 'help', 'support', 'docs', 'blog', 
  'dashboard', 'portal', 'auth', 'login', 'signup', 'billing', 'payments', 
  'internal', 'system', 'platform', 'karmarat',
];

export function isReservedSlug(slug: string): boolean {
  return RESERVED_SLUGS.includes(slug.trim().toLowerCase());
}
