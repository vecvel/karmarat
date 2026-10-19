/**
 * Reading values out of an event payload defensively.
 *
 * A consumer is the one place in a service where the input is neither validated
 * by a pipe nor shaped by a type the compiler checked. The publisher may be a
 * Go service, an older deployment of it, or a replay from thirty days ago whose
 * payload predates a field. So every read here returns a usable value or a
 * documented absence, and none of them throw: a throw nak's the message, and a
 * message that can never be decoded would be redelivered until JetStream gave
 * up on it, taking every event behind it in the consumer's backlog with it.
 */

/** Postgres will not accept anything else in a uuid column. */
const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

export function isUuid(value: unknown): value is string {
  return typeof value === 'string' && UUID.test(value);
}

export function asRecord(value: unknown): Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};
}

/** A trimmed string, or '' — never null, because every text column defaults to ''. */
export function text(value: unknown): string {
  if (typeof value === 'string') return value.trim();
  if (typeof value === 'number' && Number.isFinite(value)) return String(value);
  return '';
}

/** A uuid, or null when the value is absent or malformed. */
export function uuid(value: unknown): string | null {
  return isUuid(value) ? value.toLowerCase() : null;
}

/**
 * An amount in minor units.
 *
 * Rejects a non-integer outright rather than truncating it: a float arriving
 * where minor units were promised means the publisher has a bug, and silently
 * flooring it would put a wrong number on a revenue dashboard instead of a
 * missing one.
 */
export function minorUnits(value: unknown): number | null {
  if (typeof value === 'number' && Number.isSafeInteger(value) && value >= 0) return value;
  if (typeof value === 'string' && /^\d{1,15}$/.test(value.trim())) return Number(value.trim());
  return null;
}

/** An ISO-4217 code, upper-cased; '' when absent so the column default applies. */
export function currency(value: unknown): string {
  const raw = text(value).toUpperCase();
  return /^[A-Z]{3}$/.test(raw) ? raw : '';
}

/** A timestamp, or null. Accepts ISO strings and epoch milliseconds. */
export function timestamp(value: unknown): Date | null {
  if (value instanceof Date) return Number.isNaN(value.getTime()) ? null : value;
  if (typeof value === 'number' && Number.isFinite(value)) {
    const fromEpoch = new Date(value);
    return Number.isNaN(fromEpoch.getTime()) ? null : fromEpoch;
  }
  if (typeof value === 'string' && value.trim() !== '') {
    const parsed = new Date(value.trim());
    return Number.isNaN(parsed.getTime()) ? null : parsed;
  }
  return null;
}

/** The first present value among several spellings a publisher might use. */
export function firstOf(source: Record<string, unknown>, ...keys: string[]): unknown {
  for (const key of keys) {
    const value = source[key];
    if (value !== undefined && value !== null && value !== '') return value;
  }
  return undefined;
}

/**
 * Splits `karmarat.<domain>.<action>` into its parts.
 *
 * Falls back to the whole subject as the domain so an event published on a
 * subject this service has never seen still lands in the audit feed under
 * something a human can filter on.
 */
export function splitSubject(subject: string): { domain: string; action: string } {
  const parts = subject.split('.');
  if (parts.length >= 3 && parts[0] === 'karmarat') {
    return { domain: parts[1] ?? subject, action: parts.slice(2).join('.') };
  }
  return { domain: subject, action: '' };
}
