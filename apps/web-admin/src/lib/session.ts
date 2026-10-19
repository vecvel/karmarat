import "server-only";

import { cookies } from "next/headers";
import type { Session } from "@karmarat/ui";

import { gatewayFetch } from "./gateway";

/**
 * Where the console's session lives.
 *
 * Three cookies, all httpOnly, all scoped to this portal's hostname:
 *
 *   rb_admin_rt  the refresh token. The browser never sees it; only the route
 *                handlers in src/app/api/auth read it. That is the whole point
 *                of the httpOnly cookie — an XSS on this origin can drive the
 *                console as the operator, but it cannot walk away with a
 *                long-lived credential.
 *   rb_admin_at  the 15-minute access token. Also httpOnly, so it is likewise
 *                not script-readable; the browser gets a copy in a JSON
 *                response and holds it *in memory* in the ApiClient, never in
 *                localStorage.
 *   rb_admin_id  the identity claims — who the operator is, what they may do.
 *                Not a credential: it authorises nothing, and the API re-checks
 *                every permission on every call. It exists so a server
 *                component can act as a route guard and render the shell
 *                without a gateway round trip on every navigation.
 *
 * Why the access token is stored at all: refresh tokens rotate on use and the
 * identity service treats a replayed refresh as theft, revoking every session
 * the account has. If a server component refreshed on each render, two tabs or
 * one double-render would sign the operator out everywhere. Keeping the short
 * access token means a page load costs zero refreshes, and a refresh happens
 * only when the token has actually expired.
 */

const REFRESH_COOKIE = "rb_admin_rt";
const ACCESS_COOKIE = "rb_admin_at";
const IDENTITY_COOKIE = "rb_admin_id";

/** Matches the identity service's refresh-token lifetime. */
const REFRESH_MAX_AGE = 30 * 24 * 60 * 60;

/** What the gateway returns from /api/v1/auth/login and /api/v1/auth/refresh. */
export interface AuthResult {
  tokens: {
    accessToken: string;
    refreshToken: string;
    /** RFC 3339. */
    expiresAt: string;
    tokenType: string;
  };
  identity: {
    accountId: string;
    email: string;
    fullName: string;
    realm: "platform" | "company" | "candidate";
    roles: string[];
    roleNames?: string[];
    permissions: string[];
    isSuperAdmin?: boolean;
    companyId?: string;
    companySlug?: string;
  };
}

/** The session shape the browser and the server components share. */
export type ConsoleSession = Session & { roleNames: string[]; isSuperAdmin: boolean };

type StoredIdentity = Omit<ConsoleSession, "accessToken" | "expiresAt">;

export function toSession(result: AuthResult): ConsoleSession {
  return {
    accessToken: result.tokens.accessToken,
    expiresAt: Date.parse(result.tokens.expiresAt),
    principalType: result.identity.realm,
    accountId: result.identity.accountId,
    email: result.identity.email,
    fullName: result.identity.fullName,
    roles: result.identity.roles ?? [],
    roleNames: result.identity.roleNames ?? [],
    permissions: result.identity.permissions ?? [],
    isSuperAdmin: result.identity.isSuperAdmin ?? false,
  };
}

function cookieOptions(maxAge: number) {
  return {
    httpOnly: true,
    // Lax, not Strict: an operator following a link into the console from an
    // email should land signed in. Lax still withholds the cookie from
    // cross-site POSTs, which is where the CSRF risk actually is.
    sameSite: "lax" as const,
    // In development the console is served over plain http on localhost, and a
    // Secure cookie would simply never be stored.
    secure: process.env.NODE_ENV === "production",
    path: "/",
    maxAge,
  };
}

/** Writes the session to cookies after a successful sign-in or refresh. */
export async function persistSession(result: AuthResult): Promise<ConsoleSession> {
  const session = toSession(result);
  const jar = await cookies();

  // Expire the access cookie a few seconds early so a request that squeaks
  // through with a token the gateway has already rejected is impossible.
  const accessMaxAge = Math.max(1, Math.floor((session.expiresAt - Date.now()) / 1000) - 5);

  jar.set(REFRESH_COOKIE, result.tokens.refreshToken, cookieOptions(REFRESH_MAX_AGE));
  jar.set(ACCESS_COOKIE, session.accessToken, cookieOptions(accessMaxAge));

  const identity: StoredIdentity = {
    principalType: session.principalType,
    accountId: session.accountId,
    email: session.email,
    fullName: session.fullName,
    roles: session.roles,
    roleNames: session.roleNames,
    permissions: session.permissions,
    isSuperAdmin: session.isSuperAdmin,
  };
  jar.set(
    IDENTITY_COOKIE,
    Buffer.from(JSON.stringify(identity), "utf8").toString("base64url"),
    cookieOptions(REFRESH_MAX_AGE),
  );

  return session;
}

export async function clearSession(): Promise<void> {
  const jar = await cookies();
  for (const name of [REFRESH_COOKIE, ACCESS_COOKIE, IDENTITY_COOKIE]) {
    jar.delete(name);
  }
}

export async function readRefreshToken(): Promise<string | null> {
  return (await cookies()).get(REFRESH_COOKIE)?.value ?? null;
}

export async function readAccessToken(): Promise<string | null> {
  return (await cookies()).get(ACCESS_COOKIE)?.value ?? null;
}

/**
 * The identity a server component guards on.
 *
 * Returns null when there is nothing signed in *and nothing to restore* — a
 * present refresh cookie with an expired access cookie is still a live session,
 * and the client bootstraps it through /api/auth/session.
 */
export async function readIdentity(): Promise<StoredIdentity | null> {
  const jar = await cookies();
  const raw = jar.get(IDENTITY_COOKIE)?.value;
  if (!raw || !jar.get(REFRESH_COOKIE)) return null;

  try {
    const parsed = JSON.parse(Buffer.from(raw, "base64url").toString("utf8")) as StoredIdentity;
    // A cookie that does not describe a platform principal is not a session
    // this portal will act on, whatever else it says.
    if (parsed.principalType !== "platform") return null;
    return parsed;
  } catch {
    // A truncated or tampered cookie is treated as signed out rather than as an
    // error: the operator gets the sign-in page, which is the right next step.
    return null;
  }
}

/**
 * Exchanges the refresh cookie for a new session, rotating every cookie.
 *
 * Only ever called from a route handler. A server component cannot set cookies,
 * so a refresh from one would rotate the token upstream and leave the browser
 * holding a spent cookie — which the identity service reads as theft.
 */
export async function refreshSession(): Promise<ConsoleSession | null> {
  const refreshToken = await readRefreshToken();
  if (!refreshToken) return null;

  const response = await gatewayFetch({
    method: "POST",
    path: "/api/v1/auth/refresh",
    body: JSON.stringify({ refreshToken }),
  });

  if (response.status !== 200) {
    await clearSession();
    return null;
  }

  const result = JSON.parse(response.body) as AuthResult;
  if (result.identity.realm !== "platform") {
    // Defence in depth. The gateway already refuses platform routes to anyone
    // else, but a console that would render a shell for a company principal is
    // one bug away from leaking which tenants exist.
    await clearSession();
    return null;
  }

  return persistSession(result);
}

/**
 * The access token to call the gateway with, refreshing if the cookie lapsed.
 *
 * Route-handler only, for the same reason as refreshSession.
 */
export async function currentAccessToken(): Promise<string | null> {
  const existing = await readAccessToken();
  if (existing) return existing;
  return (await refreshSession())?.accessToken ?? null;
}
