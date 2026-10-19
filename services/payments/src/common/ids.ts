/**
 * Prefixed, time-sortable identifiers.
 *
 * A local copy of `packages/goshared/idgen` because `@karmarat/nestshared`
 * does not expose one yet. The encoding is deliberately identical — Crockford
 * base32 over a 48-bit millisecond timestamp plus 80 bits of entropy — so an id
 * minted by a Node service is indistinguishable from one minted by a Go
 * service, sorts alongside it, and can be moved between them without a
 * migration.
 */
import { randomBytes } from 'node:crypto';

/** Crockford base32: no I, L, O or U, so an id cannot be misread aloud. */
const ALPHABET = '0123456789ABCDEFGHJKMNPQRSTVWXYZ';

let lastMillis = 0;
const lastEntropy = Buffer.alloc(10);

/** Returns a prefixed identifier such as "pay_01HQ8...". */
export function newId(prefix: string): string {
  return `${prefix}_${newUlid()}`;
}

export function newUlid(): string {
  const now = Date.now();

  if (now === lastMillis) {
    // Within the same millisecond, increment rather than re-randomise so two
    // ids minted back to back still sort in the order they were created.
    increment(lastEntropy);
  } else {
    lastMillis = now;
    randomBytes(10).copy(lastEntropy);
  }

  const raw = Buffer.alloc(16);
  raw.writeUIntBE(now, 0, 6);
  lastEntropy.copy(raw, 6);

  return encode(raw);
}

function increment(entropy: Buffer): void {
  for (let i = entropy.length - 1; i >= 0; i -= 1) {
    const next = (entropy[i] ?? 0) + 1;
    entropy[i] = next & 0xff;
    if (next <= 0xff) {
      return;
    }
  }
  // Overflowed a whole millisecond's worth of ids: re-seed rather than wrap to
  // zero, which would hand out a duplicate.
  randomBytes(10).copy(entropy);
}

/** 128 bits → 26 base32 characters, five bits at a time. */
function encode(raw: Buffer): string {
  let bits = 0;
  let value = 0;
  let out = '';

  for (const byte of raw) {
    value = (value << 8) | byte;
    bits += 8;
    while (bits >= 5) {
      out += ALPHABET[(value >>> (bits - 5)) & 0x1f];
      bits -= 5;
    }
  }
  if (bits > 0) {
    out += ALPHABET[(value << (5 - bits)) & 0x1f];
  }
  return out.slice(0, 26);
}

export const PaymentIdPrefix = 'pay';
export const RefundIdPrefix = 'ref';
export const InvoiceIdPrefix = 'inv';
export const WebhookIdPrefix = 'whk';
export const EventIdPrefix = 'evt';
