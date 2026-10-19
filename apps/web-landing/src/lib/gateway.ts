import "server-only";

import { ProblemError, networkProblem } from "@karmarat/ui";

import { gatewayRequest } from "@karmarat/ui/server";

import { GATEWAY_URL, PORTAL_HOST } from "./env";

/**
 * The only way this app reaches data.
 *
 * `packages/ui`'s ApiClient is built around a session — an access token in
 * memory and a refresh route to renew it. This portal has neither: every
 * endpoint it touches is public by route at the gateway. So it uses the shared
 * ProblemError, which is the part that has to be identical across portals, and
 * a much smaller fetch around it.
 *
 * Server-side only, deliberately. A browser cannot set a Host header, so a
 * fetch straight from the page would arrive at the gateway as whatever origin
 * the tab is on and be resolved to the wrong portal — or, in development, be
 * refused by CORS before it ever got that far.
 */

export interface GatewayRequest {
  method?: string;
  body?: unknown;
  /** Seconds to cache a GET for. Omit for no caching at all. */
  revalidate?: number;
  signal?: AbortSignal;
}

/**
 * One call to the gateway, parsed.
 *
 * Sent over `node:http` rather than `fetch`. The gateway decides which portal a
 * request belongs to — and therefore which routes exist — from the Host header,
 * and Node's fetch silently drops it: every call from here arrived as
 * `localhost:8080`, resolving a portal this site is not.
 *
 * `revalidate` is accepted but no longer caches: Next's fetch cache only covers
 * `fetch`. The pricing page reads the plan catalogue on each render instead,
 * which is a handful of rows.
 */
export async function gatewayFetch<T>(path: string, request: GatewayRequest = {}): Promise<T> {
  let response;
  try {
    response = await gatewayRequest({
      gatewayUrl: GATEWAY_URL,
      portalHost: PORTAL_HOST,
      path,
      method: request.method ?? "GET",
      ...(request.body === undefined ? {} : { body: JSON.stringify(request.body) }),
      headers: { Accept: "application/json, application/problem+json" },
    });
  } catch (cause) {
    // The gateway being down is not a 500 from the gateway — there is no
    // response at all — and the page still has to say something true.
    throw networkProblem(cause);
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
 * Flattens a ProblemError into something a server action can hand back.
 *
 * A class instance does not survive the boundary between a server action and
 * the client component awaiting it, and a form needs the field map intact to
 * render a message beside the input that failed.
 */
export interface SerialisedProblem {
  status: number;
  code: string;
  title: string;
  detail: string;
  fieldErrors: Record<string, string[]>;
}

export function serialiseProblem(error: unknown): SerialisedProblem {
  if (error instanceof ProblemError) {
    return {
      status: error.status,
      code: error.code,
      title: error.title,
      detail: error.detail,
      fieldErrors: error.fieldErrors,
    };
  }

  // Anything that is not a Problem is a bug in this app, not a message for a
  // visitor. It is logged with its real shape and reported generically.
  console.error("[web-landing] non-problem error crossing the action boundary", error);
  return {
    status: 500,
    code: "internal_error",
    title: "Unexpected error",
    detail: "Something went wrong on our side. Please try again shortly.",
    fieldErrors: {},
  };
}

/** The first message for a field, for rendering beside an input. */
export function fieldError(
  problem: SerialisedProblem | null,
  field: string,
): string | undefined {
  return problem?.fieldErrors[field]?.[0];
}
