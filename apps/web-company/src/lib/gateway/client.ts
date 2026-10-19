import "server-only";

import { ProblemError } from "@karmarat/ui";
import { gatewayRequest } from "@karmarat/ui/server";

import { GATEWAY_URL, resolvePortalHost } from "./config";
import { currentAccessToken } from "./session";

/**
 * The company portal's one way of reaching its data.
 *
 * Everything the app knows now comes from here. There is no database client in
 * this app, and a feature that wants a record asks the gateway for it — which
 * is what makes the tenant check, the permission check and the subscription
 * check real rather than advisory.
 *
 * Calls go through `gatewayRequest` rather than `fetch`. The gateway decides
 * which portal — and therefore which routes exist — from the Host header, and
 * Node's fetch silently drops that header: every call from here used to arrive
 * claiming to be `localhost:8080`, resolve the public portal, and come back 404.
 * Because the reads below fall back to an empty value, that showed up as a
 * dashboard of zeros rather than as an error.
 */

export interface GatewayRequest {
  method?: "GET" | "POST" | "PATCH" | "PUT" | "DELETE";
  body?: unknown;
  /** Appended as a query string; undefined and empty values are dropped. */
  query?: Record<string, string | number | boolean | undefined | null>;
  /** Mutations that money or messaging depend on carry one, per the service contract. */
  idempotencyKey?: string;
  /** Next's fetch cache hints. Defaults to no-store: this is per-tenant data. */
  revalidate?: number | false;
  tags?: string[];
}

function buildQuery(query: GatewayRequest["query"]): string {
  if (!query) return "";
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(query)) {
    if (value === undefined || value === null || value === "") continue;
    params.set(key, String(value));
  }
  const encoded = params.toString();
  return encoded ? `?${encoded}` : "";
}

/**
 * Calls the gateway as the signed-in principal.
 *
 * Throws a ProblemError for anything that is not a success, so a caller
 * branches on `.status`/`.code` instead of inspecting a response body. The
 * 402 that a lapsed subscription produces is deliberately not special-cased
 * here — it reaches the page, which turns it into a billing prompt.
 */
export async function gatewayFetch<T>(path: string, options: GatewayRequest = {}): Promise<T> {
  const { method = "GET", body, query, idempotencyKey } = options;

  const token = await currentAccessToken();
  const headers: Record<string, string> = {};
  if (idempotencyKey) headers["Idempotency-Key"] = idempotencyKey;

  return send<T>({
    path: `${path}${buildQuery(query)}`,
    method,
    body,
    accessToken: token ?? undefined,
    headers,
  });
}

/** The same call without a session, for the public careers portal. */
export async function publicGatewayFetch<T>(
  path: string,
  options: Omit<GatewayRequest, "idempotencyKey"> = {},
): Promise<T> {
  const { method = "GET", body, query } = options;
  return send<T>({ path: `${path}${buildQuery(query)}`, method, body });
}

/**
 * One call, with the portal's identity attached and the result parsed.
 *
 * `revalidate` and `tags` are accepted by the callers above but no longer
 * honoured: Next's fetch cache only applies to `fetch`, and this app cannot use
 * fetch to reach the gateway. Nothing here was cached in practice anyway —
 * every read is per-tenant and ran with `no-store`.
 */
async function send<T>({
  path,
  method,
  body,
  accessToken,
  headers,
}: {
  path: string;
  method: string;
  body?: unknown;
  accessToken?: string;
  headers?: Record<string, string>;
}): Promise<T> {
  let response;
  try {
    response = await gatewayRequest({
      gatewayUrl: GATEWAY_URL,
      portalHost: await resolvePortalHost(),
      path,
      method,
      ...(body !== undefined ? { body: JSON.stringify(body) } : {}),
      ...(accessToken ? { accessToken } : {}),
      ...(headers ? { headers } : {}),
    });
  } catch (cause) {
    throw new ProblemError({
      type: "about:blank",
      title: "Network Error",
      status: 0,
      detail: "We could not reach the platform. Check your connection and try again.",
      code: "network_error",
      ...(cause instanceof Error ? { instance: cause.message } : {}),
    });
  }

  if (response.status >= 400) {
    throw ProblemError.fromBody(response.status, response.body);
  }
  if (response.status === 204 || response.body === "") {
    return undefined as T;
  }
  return JSON.parse(response.body) as T;
}

/**
 * Runs a read and returns a fallback instead of throwing.
 *
 * Dashboards compose a dozen independent reads. One feature the tenant's plan
 * does not cover should leave a single empty panel, not a blank page — but a
 * lapsed subscription (402) still has to reach the caller, because that closes
 * the whole portal and the answer is a billing prompt rather than an empty list.
 */
export async function gatewayRead<T>(fetcher: () => Promise<T>, fallback: T): Promise<T> {
  try {
    return await fetcher();
  } catch (error) {
    if (error instanceof ProblemError) {
      if (error.isPaymentRequired || error.isUnauthenticated) throw error;

      // Logged, because the whole point of this helper is that the failure does
      // not reach the screen — which means an endpoint returning 500 and a list
      // that is genuinely empty look exactly alike to whoever is looking at the
      // page. One line in the server log is the difference between "this tenant
      // has no recruiters" and "the roster endpoint has been broken since
      // Tuesday".
      console.error(
        `gateway read failed: ${error.status} ${error.code ?? "unknown"} ${error.instance ?? ""} — ${error.detail ?? ""}`,
      );
      return fallback;
    }
    throw error;
  }
}
