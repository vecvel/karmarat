import { NextRequest } from "next/server";

import { GATEWAY_URL, PORTAL_HOST } from "@/lib/gateway";

/**
 * A same-origin pass-through to the gateway, and the only way the browser
 * reaches it.
 *
 * Two problems it solves at once. The gateway decides which portal a request
 * belongs to from the Host header, and a browser calling
 * `http://localhost:8080` directly would announce `Host: localhost:8080`, which
 * names no portal — so the candidate routes would not exist. And a direct call
 * would be cross-origin, so it would need CORS and could not carry the
 * same-origin cookies this app relies on.
 *
 * Mounting it at `/api/v1/*` means client code calls the paths the service
 * contract documents — `/api/v1/me`, `/api/v1/my-applications` — with no
 * rewriting at the call site and no second spelling to keep in step.
 *
 * This handler adds no authority of its own. It forwards the caller's own
 * Authorization header and nothing else; the gateway strips every trust header
 * a client sends before setting its own, so nothing here can assert an
 * identity even by accident.
 */

export const dynamic = "force-dynamic";
// Streaming (the notification stream) needs a real Node response, not an edge
// buffer that would hold every frame until the response ended — which for a
// stream is never.
export const runtime = "nodejs";

/**
 * Auth is not proxied.
 *
 * `/api/v1/auth/login`, `/refresh` and `/candidate/register` all answer with a
 * refresh token in the body. Letting the browser call them through here would
 * hand script the one credential this app takes care never to expose. Those
 * flows go through /api/auth/*, which keeps the token in an httpOnly cookie.
 */
const BLOCKED_PREFIXES = ["auth/"];

// Headers that describe the hop rather than the request. Forwarding them
// produces a response the browser cannot read (a doubly-encoded body, a
// content-length that no longer matches).
const HOP_BY_HOP = new Set([
  "host",
  "connection",
  "keep-alive",
  "transfer-encoding",
  "upgrade",
  "content-length",
  "accept-encoding",
]);

async function proxy(request: NextRequest, path: string[]): Promise<Response> {
  const suffix = path.join("/");

  if (BLOCKED_PREFIXES.some((prefix) => suffix.startsWith(prefix))) {
    return problem(
      404,
      "not_found",
      "Not Found",
      "That endpoint is not available from the browser.",
    );
  }

  const headers = new Headers();
  request.headers.forEach((value, key) => {
    if (!HOP_BY_HOP.has(key.toLowerCase())) headers.set(key, value);
  });
  headers.set("Host", PORTAL_HOST);

  const target = `${GATEWAY_URL}/api/v1/${suffix}${request.nextUrl.search}`;
  const hasBody = request.method !== "GET" && request.method !== "HEAD";

  let upstream: Response;
  try {
    upstream = await fetch(target, {
      method: request.method,
      headers,
      ...(hasBody ? { body: await request.arrayBuffer() } : {}),
      // Follow no redirects: the gateway does not issue any, and silently
      // following one would send the Authorization header somewhere else.
      redirect: "manual",
      signal: request.signal,
    });
  } catch {
    return problem(
      502,
      "gateway_unreachable",
      "Bad Gateway",
      "We could not reach Karmarat just now. Please try again shortly.",
    );
  }

  const responseHeaders = new Headers();
  upstream.headers.forEach((value, key) => {
    if (!HOP_BY_HOP.has(key.toLowerCase())) responseHeaders.set(key, value);
  });

  // Passing upstream.body straight through keeps the notification stream a
  // stream: the frames reach the browser as they are written.
  return new Response(upstream.body, {
    status: upstream.status,
    statusText: upstream.statusText,
    headers: responseHeaders,
  });
}

function problem(
  status: number,
  code: string,
  title: string,
  detail: string,
): Response {
  return new Response(
    JSON.stringify({ type: "about:blank", title, status, detail, code }),
    { status, headers: { "Content-Type": "application/problem+json" } },
  );
}

type Context = { params: Promise<{ path: string[] }> };

export async function GET(request: NextRequest, context: Context) {
  return proxy(request, (await context.params).path);
}
export async function POST(request: NextRequest, context: Context) {
  return proxy(request, (await context.params).path);
}
export async function PUT(request: NextRequest, context: Context) {
  return proxy(request, (await context.params).path);
}
export async function PATCH(request: NextRequest, context: Context) {
  return proxy(request, (await context.params).path);
}
export async function DELETE(request: NextRequest, context: Context) {
  return proxy(request, (await context.params).path);
}
