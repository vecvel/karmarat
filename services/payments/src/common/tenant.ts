/**
 * The tenant predicate, in one place.
 *
 * `Principal.requireCompany()` already refuses a request with no company
 * context. What it cannot do is know that this service stores `company_id` as a
 * `uuid`: a company id that is not a uuid would reach Postgres, fail there, and
 * surface as a 500 carrying a type name from the database. Validating the shape
 * here turns that into an ordinary, non-leaking 403.
 */
import { forbidden } from '@karmarat/nestshared';

const UUID_PATTERN = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

export function requireTenant(companyId: string): string {
  if (!UUID_PATTERN.test(companyId)) {
    throw forbidden('This endpoint requires a company context.');
  }
  return companyId;
}

export function isTenantId(value: string): boolean {
  return UUID_PATTERN.test(value);
}
