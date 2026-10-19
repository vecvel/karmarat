import "server-only";

import { ProblemError } from "@karmarat/ui";
import { gatewayRequest } from "@karmarat/ui/server";

/**
 * The server half of this app's only connection to the platform.
 *
 * Every read and write in this portal goes through the gateway, which is the
 * one component that verifies a token and resolves a tenant. There is no
 * database client in this app and there is not meant to be one: an app that
 * could read a table could read another tenant's rows, and the boundary that
 * stops that lives at the gateway, not here.
 */

export const GATEWAY_URL = process.env.GATEWAY_URL ?? "http://localhost:8080";

/**
 * The hostname this portal answers on.
 *
 * The gateway derives the portal — and therefore which routes exist at all —
 * from the Host header. A browser reaching this app at `localhost:3001` would
 * otherwise send `Host: localhost:3001`, which resolves to no portal, so every
 * server-side call states the portal hostname explicitly.
 */
export const PORTAL_HOST = process.env.PORTAL_HOST ?? "jobs.karmarat.local";

export interface GatewayRequest {
  method?: string;
  body?: unknown;
  /** The caller's access token, when the call is made on their behalf. */
  accessToken?: string;
  headers?: Record<string, string>;
  /** Seconds the framework may reuse this response for. Omit for no caching. */
  revalidate?: number;
  signal?: AbortSignal;
}

/**
 * Performs one call and hands back the raw response, errors included.
 *
 * The transport is `node:http`, not `fetch`. Node's fetch implements the WHATWG
 * forbidden-header list, so the `Host` header above was silently dropped and
 * replaced with the gateway's own address: the gateway then resolved the public
 * portal and answered 404 for every candidate route. The result is still a
 * `Response` so the handlers reading `.ok` and `.json()` are unchanged.
 *
 * `revalidate` no longer caches. Next's fetch cache only applies to `fetch`,
 * and correctness here outranks re-reading the public board from memory.
 */
export async function gatewayFetch(
  path: string,
  request: GatewayRequest = {},
): Promise<Response> {
  const result = await gatewayRequest({
    gatewayUrl: GATEWAY_URL,
    portalHost: PORTAL_HOST,
    path,
    method: request.method ?? "GET",
    ...(request.body !== undefined ? { body: JSON.stringify(request.body) } : {}),
    ...(request.accessToken ? { accessToken: request.accessToken } : {}),
    ...(request.headers ? { headers: request.headers } : {}),
  });

  // A 204 or 304 may not carry a body; constructing a Response with one throws.
  const bodyless = result.status === 204 || result.status === 205 || result.status === 304;
  return new Response(bodyless || result.body === "" ? null : result.body, {
    status: result.status,
    headers: result.headers,
  });
}

/**
 * Calls the gateway and parses the result, throwing a ProblemError on failure.
 *
 * Both backend runtimes answer in RFC 9457 problem+json, so a caller can catch
 * one type and branch on `.status` or `.code` rather than inspecting a body at
 * every call site.
 */
export async function gatewayJson<T>(
  path: string,
  request: GatewayRequest = {},
): Promise<T> {
  let response: Response;
  try {
    response = await gatewayFetch(path, request);
  } catch {
    // The gateway being down is a state this app has to render, not a crash.
    throw new ProblemError({
      type: "about:blank",
      title: "Service Unavailable",
      status: 0,
      detail: "We could not reach Karmarat just now. Please try again shortly.",
      code: "network_error",
    });
  }

  if (!response.ok) {
    throw await ProblemError.fromResponse(response);
  }
  if (response.status === 204) {
    return undefined as T;
  }
  return (await response.json()) as T;
}
