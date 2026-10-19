/**
 * Company identifiers are uuids minted by the companies service (contract §3).
 *
 * Every tenant-scoped query here binds one into a `uuid` column. Postgres
 * rejects a malformed value with 22P02, which would surface as a bare 500 and
 * put driver text in the logs for what is really a bad request — so the shape
 * is checked before the query, not by it.
 */
import { badRequest, forbidden } from '@karmarat/nestshared';

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

export function isUuid(value: string): boolean {
  return UUID.test(value);
}

/**
 * Validates a tenant id that came from the verified principal.
 *
 * A principal carrying a company id this service cannot use is a gateway or
 * provisioning fault, not a client error, so it is refused as forbidden rather
 * than reported as a malformed field the caller could fix.
 */
export function requireTenantId(companyId: string): string {
  if (!isUuid(companyId)) {
    throw forbidden('This endpoint requires a company context.');
  }
  return companyId;
}

/** Validates a company id supplied by another service on an internal route. */
export function requireCompanyIdParam(companyId: string): string {
  if (!isUuid(companyId)) {
    throw badRequest('companyId must be a uuid.');
  }
  return companyId;
}
