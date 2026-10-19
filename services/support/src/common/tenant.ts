/**
 * The tenant id, checked before it reaches SQL.
 *
 * `company_id` is a uuid per docs/contracts.md §3. The gateway hands the value
 * over as an opaque header string, so a malformed one would otherwise surface as
 * a Postgres cast error — a 500 whose message carries a type name and the
 * offending literal straight into the response body. Checking the shape here
 * turns that into a deliberate 403 with nothing internal in it.
 *
 * This is not a substitute for the tenant filter; it only guarantees that the
 * filter's argument is a value the column can hold.
 */
import { forbidden } from '@karmarat/nestshared';

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

export function assertTenantId(companyId: string): string {
  if (!UUID.test(companyId)) {
    throw forbidden('This endpoint requires a company context.');
  }
  return companyId;
}

export function isTenantId(value: string): boolean {
  return UUID.test(value);
}
