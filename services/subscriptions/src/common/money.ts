/**
 * Money is minor units in a bigint column plus an ISO 4217 code.
 *
 * node-postgres hands back an int8 as a string, because most int8 values do not
 * survive a JS number. Prices do: the safe integer range tops out at about
 * 9.0e15, which is ninety trillion currency units in minor form, so converting
 * is sound for this domain and the conversion asserts it rather than assuming.
 */
import { badRequest } from '@karmarat/nestshared';

export const MAX_MINOR_UNITS = Number.MAX_SAFE_INTEGER;

const CURRENCY = /^[A-Z]{3}$/;

export function toMinorUnits(raw: string | number | null | undefined): number {
  if (raw === null || raw === undefined) return 0;
  const value = typeof raw === 'number' ? raw : Number(raw);
  if (!Number.isSafeInteger(value)) {
    // Reaching here means the column holds a value this service cannot
    // represent; returning a rounded number would quietly misstate a price.
    throw new Error(`money: ${raw} is outside the representable range`);
  }
  return value;
}

export function normaliseCurrency(raw: string, field = 'currency'): string {
  const value = raw.trim().toUpperCase();
  if (!CURRENCY.test(value)) {
    throw badRequest(`${field} must be a three-letter ISO 4217 code.`);
  }
  return value;
}
