/**
 * Money handling.
 *
 * Every amount in this service is an integer count of a currency's minor unit
 * (cents, pence, paise) carried as a `bigint`, paired with an ISO-4217 code.
 * There is no float anywhere in the path: 0.1 has no exact binary
 * representation, so a float total drifts by a cent over a few thousand
 * invoices and reconciliation against the provider then fails for a reason
 * nobody can find.
 *
 * `bigint` rather than `number` because Postgres `bigint` arrives from `pg` as a
 * string, and the only lossless conversion of an arbitrary one is BigInt(). A
 * number would silently round above 2^53 — unreachable for a single invoice,
 * reachable for an aggregate in a minor unit with no decimal places.
 */
import { badRequest, validationFailed } from '@karmarat/nestshared';

/** Currencies are three uppercase letters; anything else never reaches SQL. */
const CURRENCY_PATTERN = /^[A-Z]{3}$/;

/** Reads a bigint column as returned by `pg` (string), or a literal. */
export function toMinor(value: string | number | bigint | null | undefined): bigint {
  if (value === null || value === undefined) {
    return 0n;
  }
  if (typeof value === 'bigint') {
    return value;
  }
  if (typeof value === 'number') {
    if (!Number.isSafeInteger(value)) {
      throw new Error('money: amount is not a safe integer');
    }
    return BigInt(value);
  }
  const trimmed = value.trim();
  if (!/^-?\d+$/.test(trimmed)) {
    throw new Error('money: amount is not an integer');
  }
  return BigInt(trimmed);
}

/**
 * Renders an amount for a JSON body.
 *
 * JSON has no bigint, and a string amount would push the decision about how to
 * parse it onto every client. Minor units stay well inside the safe-integer
 * range for any real charge, and an amount that does not is a bug we would
 * rather hear about than serialise.
 */
export function toJsonMinor(value: bigint): number {
  if (value > BigInt(Number.MAX_SAFE_INTEGER) || value < BigInt(Number.MIN_SAFE_INTEGER)) {
    throw new Error('money: amount exceeds the safe integer range');
  }
  return Number(value);
}

/** Providers take amounts as JS numbers; the same ceiling applies. */
export const toProviderAmount = toJsonMinor;

/** Stores a bigint in a query parameter list. `pg` binds a string cleanly. */
export function toParam(value: bigint): string {
  return value.toString();
}

export function normaliseCurrency(raw: string): string {
  const code = raw.trim().toUpperCase();
  if (!CURRENCY_PATTERN.test(code)) {
    throw badRequest('The currency must be a three-letter ISO-4217 code.');
  }
  return code;
}

/**
 * Validates an amount a client or a provider handed us.
 *
 * Zero is rejected: a zero-value checkout is either a free plan, which should
 * never reach a payment provider, or a pricing bug.
 */
export function requirePositiveMinor(value: bigint, field = 'amountMinor'): bigint {
  if (value <= 0n) {
    throw validationFailed({ [field]: ['must be greater than zero'] });
  }
  return value;
}

/** Formats minor units for a log line or an invoice line. Display only. */
export function formatMinor(value: bigint, currency: string): string {
  const negative = value < 0n;
  const absolute = negative ? -value : value;
  const units = absolute / 100n;
  const fraction = (absolute % 100n).toString().padStart(2, '0');
  return `${negative ? '-' : ''}${units}.${fraction} ${currency}`;
}
