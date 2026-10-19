import { NextResponse } from "next/server";

import { gatewayRequest } from "@karmarat/ui/server";

import { GATEWAY_URL, resolvePortalHost } from "@/lib/gateway/config";
import { clearSession, readRefreshToken } from "@/lib/gateway/tokens";

/**
 * Ends the session on both sides.
 *
 * Dropping the cookies alone would leave a usable refresh token in identity's
 * store for thirty days, so the token is revoked there first. The cookies are
 * cleared whether or not that call succeeds — a browser left holding a session
 * it believes is live is the worse failure.
 */
export async function POST() {
  const refreshToken = await readRefreshToken();

  if (refreshToken) {
    try {
      await gatewayRequest({
        gatewayUrl: GATEWAY_URL,
        portalHost: await resolvePortalHost(),
        path: "/api/v1/auth/logout",
        method: "POST",
        body: JSON.stringify({ refreshToken }),
      });
    } catch {
      // Best effort: the cookie is cleared regardless.
    }
  }

  await clearSession();
  return NextResponse.json({ success: true, message: "Logged out successfully" });
}
