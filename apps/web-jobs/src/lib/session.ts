import "server-only";

import { cookies } from "next/headers";
import type { Session } from "@karmarat/ui";

import { gatewayFetch } from "./gateway";

/**
 * How a session is held, and why it is held that way.
 *
 * Two cookies, with different jobs:
 *
 * `rb_refresh` is the credential. It is httpOnly, so script cannot read it, and
 * only the route handlers under /api/auth ever exchange it. The access token it
 * buys is handed to the browser and kept in memory — never in localStorage —
 * so an XSS on this portal is a defacement rather than the theft of a long-
 * lived credential.
 *
 * `rb_identity` is not a credential at all: it carries the account's name,
 * email, roles and permission keys and no token whatsoever. It exists so a
 * server component can decide whether to render a page or redirect to sign-in
 * without exchanging the refresh token, which rotates on every use. A server
 * component cannot set a cookie, so a refresh performed during a render would
 * spend the token and leave the browser holding a spent one — and identity
 * treats a replayed refresh as theft and revokes every session the account has.
 *
 * Because `rb_identity` proves nothing, forging it buys nothing. It decides
 * what this app renders; the gateway decides what the account may actually do,
 * and re-checks on every single call.
 */
export const REFRESH_COOKIE = "rb_refresh";
export const IDENTITY_COOKIE = "rb_identity";

/** The refresh token's lifetime at the identity service, in seconds. */
const REFRESH_MAX_AGE = 30 * 24 * 60 * 60;

export interface Identity {
  accountId: string;
  email: string;
  fullName: string;
  roles: string[];
  permissions: string[];
}

/** The gateway's answer to a login, a registration or a refresh. */
export interface AuthResult {
  tokens: {
    accessToken: string;
    refreshToken: string;
    expiresAt: string;
    tokenType: string;
  };
  identity: {
    accountId: string;
    email: string;
    fullName: string;
    realm: string;
    roles: string[];
    roleNames?: string[];
    permissions: string[];
    isSuperAdmin?: boolean;
  };
}

function cookieOptions() {
  const domain = process.env.COOKIE_DOMAIN?.trim();
  return {
    httpOnly: true,
    // Lax rather than Strict: a candidate following a job link from an email
    // into this portal must still arrive signed in, and Lax is enough to stop
    // a cross-site POST from carrying the cookie.
    sameSite: "lax" as const,
    secure: process.env.COOKIE_SECURE === "1",
    path: "/",
    ...(domain ? { domain } : {}),
  };
}

/** Writes both cookies after a successful authentication. */
export async function writeSessionCookies(result: AuthResult): Promise<void> {
  const jar = await cookies();
  const options = cookieOptions();

  jar.set(REFRESH_COOKIE, result.tokens.refreshToken, {
    ...options,
    maxAge: REFRESH_MAX_AGE,
  });
  jar.set(IDENTITY_COOKIE, encodeIdentity(result.identity), {
    ...options,
    maxAge: REFRESH_MAX_AGE,
  });
}

export async function clearSessionCookies(): Promise<void> {
  const jar = await cookies();
  const options = cookieOptions();
  jar.set(REFRESH_COOKIE, "", { ...options, maxAge: 0 });
  jar.set(IDENTITY_COOKIE, "", { ...options, maxAge: 0 });
}

export async function readRefreshToken(): Promise<string | null> {
  const jar = await cookies();
  return jar.get(REFRESH_COOKIE)?.value ?? null;
}

/**
 * The identity a server component renders against.
 *
 * Null means "show them the signed-out view". It does not mean "they are not
 * signed in" with any authority — that answer only ever comes from the API.
 */
export async function readIdentity(): Promise<Identity | null> {
  const jar = await cookies();
  const raw = jar.get(IDENTITY_COOKIE)?.value;
  if (!raw) return null;
  return decodeIdentity(raw);
}

/** Turns an auth result into the session shape the browser client holds. */
export function toSession(result: AuthResult): Session {
  return {
    accessToken: result.tokens.accessToken,
    expiresAt: Date.parse(result.tokens.expiresAt),
    principalType: "candidate",
    accountId: result.identity.accountId,
    email: result.identity.email,
    fullName: result.identity.fullName,
    roles: result.identity.roles ?? [],
    permissions: result.identity.permissions ?? [],
  };
}

/**
 * Exchanges the refresh cookie for a new session.
 *
 * Only route handlers call this, because only they can write the rotated token
 * back. The identity service rotates on every use and treats a second
 * presentation of a spent token as theft.
 */
export async function exchangeRefreshToken(
  refreshToken: string,
): Promise<AuthResult | null> {
  const response = await gatewayFetch("/api/v1/auth/refresh", {
    method: "POST",
    body: { refreshToken },
  });
  if (!response.ok) return null;
  return (await response.json()) as AuthResult;
}

/* -------------------------------------------------------------------------- */
/* Identity cookie encoding                                                   */
/* -------------------------------------------------------------------------- */

// base64url rather than raw JSON: a cookie value may not contain a comma, a
// semicolon or whitespace, and a full name very well might.
function encodeIdentity(identity: AuthResult["identity"]): string {
  const slim: Identity = {
    accountId: identity.accountId,
    email: identity.email,
    fullName: identity.fullName,
    roles: identity.roles ?? [],
    permissions: identity.permissions ?? [],
  };
  return Buffer.from(JSON.stringify(slim), "utf8").toString("base64url");
}

function decodeIdentity(raw: string): Identity | null {
  try {
    const parsed = JSON.parse(
      Buffer.from(raw, "base64url").toString("utf8"),
    ) as Partial<Identity>;

    if (typeof parsed.accountId !== "string" || !parsed.accountId) return null;

    return {
      accountId: parsed.accountId,
      email: typeof parsed.email === "string" ? parsed.email : "",
      fullName: typeof parsed.fullName === "string" ? parsed.fullName : "",
      roles: Array.isArray(parsed.roles) ? parsed.roles : [],
      permissions: Array.isArray(parsed.permissions) ? parsed.permissions : [],
    };
  } catch {
    // A cookie we cannot read is a cookie we do not have. The bootstrap refresh
    // will replace it, or the guard will send them to sign in.
    return null;
  }
}
