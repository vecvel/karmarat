import "server-only";

import { cookies } from "next/headers";

import { gatewayRequest } from "@karmarat/ui/server";

import { GATEWAY_URL, resolvePortalHost } from "./config";
import { ACCESS_COOKIE, PROFILE_COOKIE, REFRESH_COOKIE } from "./cookie-names";

/**
 * Session tokens, and the one rule that governs how they are spent.
 *
 * Identity rotates the refresh token on every exchange and treats a replayed
 * one as theft — it revokes *every* session the account holds. So the refresh
 * token must be spent at most once per expiry, never once per render.
 *
 * That is why the access token is kept in its own httpOnly cookie rather than
 * being re-derived from the refresh token on each server request: a page with
 * four server components must make zero refresh calls, not four. Both cookies
 * are httpOnly, so neither is reachable from script — an XSS still cannot
 * exfiltrate a credential, which is the property the in-memory rule was
 * protecting. The browser's own ApiClient holds the access token in memory and
 * obtains it from /api/auth/refresh, which is the only reader of the cookie.
 */

// The names live in their own import-free module so the middleware can read
// them without pulling in `next/headers`, which its runtime does not have.
export { ACCESS_COOKIE, REFRESH_COOKIE, PROFILE_COOKIE } from "./cookie-names";

/** Refreshed this far before true expiry, so a request in flight never expires mid-call. */
const EXPIRY_SKEW_MS = 30_000;

export interface GatewayIdentity {
  accountId: string;
  email: string;
  fullName: string;
  realm: string;
  companyId?: string;
  companySlug?: string;
  companyName?: string;
  roles: string[];
  roleNames: string[];
  permissions: string[];
  isSuperAdmin: boolean;
}

export interface GatewayTokens {
  accessToken: string;
  refreshToken: string;
  expiresAt: string;
  tokenType: string;
}

export interface GatewayAuthResult {
  tokens: GatewayTokens;
  identity: GatewayIdentity;
  companies?: { id: string; slug: string; name: string; portalAvailable: boolean; isOwner: boolean }[];
}

const isProduction = process.env.NODE_ENV === "production";

function cookieOptions(expires: Date) {
  return {
    httpOnly: true,
    secure: isProduction,
    sameSite: "lax" as const,
    path: "/",
    expires,
  };
}

/** Persists a freshly issued pair. The access cookie expires with the token it holds. */
export async function storeSession(result: GatewayAuthResult): Promise<void> {
  const jar = await cookies();
  const accessExpiry = new Date(result.tokens.expiresAt);

  jar.set(ACCESS_COOKIE, result.tokens.accessToken, cookieOptions(accessExpiry));
  // The refresh token's own lifetime is 30 days; the cookie tracks it loosely
  // because identity, not the browser, is the authority on whether it is spent.
  const longLived = new Date(Date.now() + 30 * 24 * 60 * 60 * 1000);
  jar.set(REFRESH_COOKIE, result.tokens.refreshToken, cookieOptions(longLived));

  jar.set(
    PROFILE_COOKIE,
    JSON.stringify({
      companyName: result.identity.companyName ?? "",
      roleNames: result.identity.roleNames ?? [],
    }),
    cookieOptions(longLived),
  );
}

export interface SessionProfile {
  companyName: string;
  roleNames: string[];
}

export async function readProfile(): Promise<SessionProfile> {
  const raw = (await cookies()).get(PROFILE_COOKIE)?.value;
  if (!raw) return { companyName: "", roleNames: [] };
  try {
    const parsed = JSON.parse(raw) as Partial<SessionProfile>;
    return {
      companyName: typeof parsed.companyName === "string" ? parsed.companyName : "",
      roleNames: Array.isArray(parsed.roleNames)
        ? parsed.roleNames.filter((r): r is string => typeof r === "string")
        : [],
    };
  } catch {
    return { companyName: "", roleNames: [] };
  }
}

export async function clearSession(): Promise<void> {
  const jar = await cookies();
  for (const name of [ACCESS_COOKIE, REFRESH_COOKIE, PROFILE_COOKIE]) {
    jar.set(name, "", { httpOnly: true, secure: isProduction, sameSite: "lax", path: "/", maxAge: 0 });
  }
}

export async function readRefreshToken(): Promise<string | null> {
  return (await cookies()).get(REFRESH_COOKIE)?.value ?? null;
}

export async function readAccessToken(): Promise<string | null> {
  return (await cookies()).get(ACCESS_COOKIE)?.value ?? null;
}

/**
 * Exchanges a refresh token for a new pair.
 *
 * Single-flighted per token: two concurrent callers with the same refresh
 * token share one network call, because the second exchange would present a
 * token the first has already rotated and be read as a replay.
 */
const inFlight = new Map<string, Promise<GatewayAuthResult | null>>();

export async function exchangeRefreshToken(token: string): Promise<GatewayAuthResult | null> {
  const existing = inFlight.get(token);
  if (existing) return existing;

  const attempt = (async (): Promise<GatewayAuthResult | null> => {
    try {
      const response = await gatewayRequest({
        gatewayUrl: GATEWAY_URL,
        portalHost: await resolvePortalHost(),
        path: "/api/v1/auth/refresh",
        method: "POST",
        body: JSON.stringify({ refreshToken: token }),
      });
      if (response.status >= 400) return null;
      return JSON.parse(response.body) as GatewayAuthResult;
    } catch {
      return null;
    } finally {
      // Cleared on the next tick so callers that arrive during the same
      // microtask still join this exchange rather than starting another.
      setTimeout(() => inFlight.delete(token), 0);
    }
  })();

  inFlight.set(token, attempt);
  return attempt;
}

export function isExpired(expiresAt: string | number | Date): boolean {
  const ms = new Date(expiresAt).getTime();
  return !Number.isFinite(ms) || ms - EXPIRY_SKEW_MS <= Date.now();
}
