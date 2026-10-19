import "server-only";

import { gatewayRequest, unreachableProblem as sharedUnreachable } from "@karmarat/ui/server";
import type { GatewayResponse } from "@karmarat/ui/server";

/**
 * The only place in this app that knows where the gateway lives.
 *
 * Every call carries `Host: PORTAL_HOST`. The gateway resolves the portal from
 * the Host header and answers 404 for a platform route that did not arrive on
 * root.{hostname} — so the portal is established by where the request lands,
 * never by a field the client sets.
 *
 * The transport lives in `@karmarat/ui/server` now. It was written here
 * first, because this console was the app that noticed Node's `fetch` silently
 * drops a `Host` header; the other three portals were still using `fetch` and
 * had been resolving the wrong portal for every server-side call. Sharing one
 * implementation is what stops that from being rediscovered a fourth time.
 */

export const GATEWAY_URL = (process.env.GATEWAY_URL ?? "http://localhost:8080").replace(/\/$/, "");
export const PORTAL_HOST = process.env.PORTAL_HOST ?? "root.karmarat.local";

export type { GatewayResponse } from "@karmarat/ui/server";
export { isJson } from "@karmarat/ui/server";

export interface GatewayRequest {
  method?: string;
  path: string;
  body?: string | undefined;
  accessToken?: string | undefined;
  /** Forwarded from the caller; trust and transport headers are removed. */
  headers?: Headers | undefined;
  timeoutMs?: number;
}

/**
 * Calls the gateway from the server and returns the raw status, headers and
 * body.
 *
 * Deliberately not throwing on a non-2xx: callers differ in what they do with
 * one. The proxy streams the problem document straight back to the browser so
 * ProblemError can parse it; the auth handlers turn it into a cookie decision.
 */
export function gatewayFetch(request: GatewayRequest): Promise<GatewayResponse> {
  return gatewayRequest({
    gatewayUrl: GATEWAY_URL,
    portalHost: PORTAL_HOST,
    ...request,
  });
}

/** A problem document for the cases where the gateway cannot be reached at all. */
export function unreachableProblem(instance: string): GatewayResponse {
  return sharedUnreachable(
    instance,
    "The console could not reach the platform API. Please try again shortly.",
  );
}
