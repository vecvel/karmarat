import { NextRequest, NextResponse } from "next/server";

import { gatewayRequest } from "@karmarat/ui/server";

import { GATEWAY_URL, resolveCompanySlug, resolvePortalHost } from "@/lib/gateway/config";
import { storeSession, type GatewayAuthResult } from "@/lib/gateway/tokens";
import { recordAuditLog } from "@/lib/security/audit";

/**
 * Sign-in, delegated to the platform.
 *
 * Everything this handler used to do itself — password verification, lockout
 * counting, rate limiting, session creation — belongs to the identity service
 * now, and doing any of it here would mean two policies that can disagree.
 * Identity applies Argon2id, a five-attempt lock, and the same response and
 * hashing cost for an unknown email so the endpoint cannot be used to discover
 * which addresses exist.
 *
 * The realm and the company slug are not taken from the request body: the slug
 * comes from the hostname this app is served on, which is what makes the portal
 * boundary a deployment fact rather than something a client can assert.
 */
export async function POST(req: NextRequest) {
  let body: { email?: unknown; password?: unknown };
  try {
    body = (await req.json()) as typeof body;
  } catch {
    return problem(400, "bad_request", "Bad Request", "The request body was not valid JSON.");
  }

  const email = typeof body.email === "string" ? body.email.trim() : "";
  const password = typeof body.password === "string" ? body.password : "";

  const fieldErrors: Record<string, string[]> = {};
  if (!email) fieldErrors.email = ["Email is required."];
  if (!password) fieldErrors.password = ["Password is required."];
  if (Object.keys(fieldErrors).length > 0) {
    return NextResponse.json(
      {
        type: "about:blank",
        title: "Validation Failed",
        status: 422,
        detail: "One or more fields are invalid.",
        code: "validation_failed",
        errors: fieldErrors,
      },
      { status: 422, headers: { "Content-Type": "application/problem+json" } },
    );
  }

  let companySlug: string;
  let portalHost: string;
  try {
    companySlug = await resolveCompanySlug();
    portalHost = await resolvePortalHost();
  } catch {
    return problem(
      500,
      "portal_unresolved",
      "Internal Server Error",
      "This portal is not configured with a company. Set COMPANY_SLUG or serve it on {slug}.{hostname}.",
    );
  }

  let response;
  try {
    response = await gatewayRequest({
      gatewayUrl: GATEWAY_URL,
      portalHost,
      path: "/api/v1/auth/login",
      method: "POST",
      // Forwarded so identity records the real client on the auth event and
      // rate-limits the browser rather than this server.
      headers: forwardedFor(req),
      body: JSON.stringify({ realm: "company", email, password, companySlug }),
    });
  } catch {
    return problem(
      502,
      "gateway_unreachable",
      "Bad Gateway",
      "We could not reach the sign-in service. Please try again shortly.",
    );
  }

  let payload: unknown = null;
  try {
    payload = JSON.parse(response.body);
  } catch {
    payload = null;
  }

  if (response.status >= 400) {
    await recordAuditLog({
      action: "auth.login_failed",
      entityType: "account",
      entityId: email,
      metadata: { companySlug, status: response.status },
    });
    // The service's problem+json is forwarded verbatim: it already says the
    // right thing about a lock, a bad credential or a rate limit, and
    // paraphrasing it here is how the two drift apart.
    return NextResponse.json(payload ?? fallbackProblem(response.status), {
      status: response.status,
      headers: { "Content-Type": "application/problem+json" },
    });
  }

  const result = payload as GatewayAuthResult;
  await storeSession(result);

  await recordAuditLog({
    actorId: result.identity.accountId,
    orgId: result.identity.companyId ?? "",
    action: "auth.login_success",
    entityType: "account",
    entityId: result.identity.accountId,
    metadata: { companySlug },
  });

  // The access token is deliberately absent from this body. It is in an
  // httpOnly cookie, and the browser obtains it — when it needs one — from
  // /api/auth/refresh, which is the only reader of the refresh cookie.
  return NextResponse.json({
    success: true,
    user: {
      id: result.identity.accountId,
      name: result.identity.fullName,
      email: result.identity.email,
      role: result.identity.roles[0] ?? "member",
      roleSlugs: result.identity.roles,
      roleNames: result.identity.roleNames,
      isSuperAdmin: result.identity.isSuperAdmin,
      organizationName: result.identity.companyName ?? "",
    },
  });
}

function forwardedFor(req: NextRequest): Record<string, string> {
  const forwarded = req.headers.get("x-forwarded-for");
  const agent = req.headers.get("user-agent");
  return {
    ...(forwarded ? { "X-Forwarded-For": forwarded } : {}),
    ...(agent ? { "User-Agent": agent } : {}),
  };
}

function fallbackProblem(status: number) {
  return {
    type: "about:blank",
    title: "Request failed",
    status,
    detail: "Sign-in could not be completed. Please try again.",
    code: "login_failed",
  };
}

function problem(status: number, code: string, title: string, detail: string) {
  return NextResponse.json(
    { type: "about:blank", title, status, detail, code },
    { status, headers: { "Content-Type": "application/problem+json" } },
  );
}
