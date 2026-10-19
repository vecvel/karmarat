/**
 * Reading query-string filters.
 *
 * Every parser here refuses a malformed value with a problem document rather
 * than passing it to Postgres. An id that is not a uuid reaches a `::uuid` cast
 * as a database error and surfaces as an opaque 500, which tells the caller
 * nothing and puts a SQL cast error in the logs of a service that is supposed
 * to be read-only and boring.
 *
 * A note on the company filters these parse. Everywhere else in this platform
 * reading a tenant id from a request is forbidden. It is correct here for one
 * reason only: a platform principal has no tenant of its own, so the parameter
 * names a subject to read *about* rather than asserting an identity. That
 * reasoning holds only while every route is platform-only — see
 * src/common/platform.ts.
 */
import { badRequest } from '@karmarat/nestshared';

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

/** A trimmed, length-capped filter value, or undefined when absent. */
export function optionalText(raw: string | undefined, field: string, maxLength = 200): string | undefined {
  if (raw === undefined) return undefined;
  const value = raw.trim();
  if (value === '') return undefined;
  if (value.length > maxLength) {
    throw badRequest(`${field} must be at most ${maxLength} characters.`);
  }
  return value;
}

export function requireUuid(raw: string, field: string): string {
  const value = raw.trim();
  if (!UUID.test(value)) {
    throw badRequest(`${field} must be a UUID.`);
  }
  return value.toLowerCase();
}

export function optionalUuid(raw: string | undefined, field: string): string | undefined {
  const value = optionalText(raw, field);
  return value === undefined ? undefined : requireUuid(value, field);
}

export function optionalTimestamp(raw: string | undefined, field: string): Date | undefined {
  const value = optionalText(raw, field, 64);
  if (value === undefined) return undefined;
  const parsed = new Date(value);
  if (Number.isNaN(parsed.getTime())) {
    throw badRequest(`${field} must be an RFC 3339 timestamp.`);
  }
  return parsed;
}
