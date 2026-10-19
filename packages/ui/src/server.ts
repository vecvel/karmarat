import http from 'node:http';
import https from 'node:https';

/**
 * The server-side transport every portal uses to reach the gateway.
 *
 * It exists because of one fact about Node: `fetch` (undici) implements the
 * WHATWG forbidden-header list, and `Host` is on it. A `Host` header set on a
 * fetch is silently dropped and replaced with the URL's authority — no error,
 * no warning. The gateway resolves the portal, and therefore which routes exist
 * at all, from that header, so every server-side call made with fetch arrived
 * claiming to be `localhost:8080`, resolved the public portal, and was answered
 * 404 for every route the portal actually needed.
 *
 * That failure is close to invisible from the outside. The 404 is indis-
 * tinguishable from an endpoint that does not exist yet, and a portal whose
 * reads are wrapped in "return an empty list on failure" simply renders empty
 * screens: a dashboard of zeros, a roster with nobody on it, a settings page
 * with no roles. Three of the four portals shipped that way.
 *
 * `node:http` sends the header as written. GATEWAY_URL stays a connection
 * address — 127.0.0.1:8080 in development, the cluster service in production —
 * while the portal host stays the identity the gateway routes on. They are
 * genuinely different things, and keeping them separate is what lets a portal
 * be reached over localhost while still being addressed as itself.
 *
 * This module is Node-only. It is exported from `@karmarat/ui/server` rather
 * than the package root so a browser bundle can never pull `node:http` in.
 */

/** Headers a caller must never be able to influence on an upstream call. */
const STRIPPED_REQUEST_HEADERS = new Set([
  'host',
  'cookie',
  'connection',
  'content-length',
  'transfer-encoding',
  'accept-encoding',
  // The gateway strips client-supplied trust headers itself. Forwarding them
  // would still be a portal volunteering to look like an attacker.
  'x-principal-type',
  'x-principal-id',
  'x-principal-email',
  'x-company-id',
  'x-company-slug',
  'x-permissions',
  'x-roles',
  'x-session-id',
]);

/** Hop-by-hop headers that must not be copied onto the response we return. */
const STRIPPED_RESPONSE_HEADERS = new Set([
  'connection',
  'keep-alive',
  'transfer-encoding',
  'content-encoding',
  'content-length',
  // Upstream never sets cookies, but if it ever did they would be scoped to the
  // gateway's host and meaningless — or dangerous — on a portal's origin.
  'set-cookie',
]);

export interface GatewayRequest {
  /** The gateway's connection address, e.g. http://localhost:8080. */
  gatewayUrl: string;
  /** The hostname the gateway must see, e.g. acme.karmarat.local. */
  portalHost: string;
  path: string;
  method?: string;
  body?: string | undefined;
  accessToken?: string | undefined;
  /** Extra headers; trust and transport headers are removed. */
  headers?: Headers | Record<string, string> | undefined;
  timeoutMs?: number;
}

export interface GatewayResponse {
  status: number;
  headers: Headers;
  body: string;
}

/** True when the body looks like a problem document or any other JSON. */
export function isJson(headers: Headers): boolean {
  return (headers.get('content-type') ?? '').includes('json');
}

/** Accepts either a Headers instance or a plain object, so callers need not convert. */
function eachHeader(
  headers: Headers | Record<string, string>,
  visit: (name: string, value: string) => void,
): void {
  // Headers.forEach yields (value, name); Object.entries yields (name, value).
  if (typeof (headers as Headers).get === 'function') {
    (headers as Headers).forEach((value, name) => visit(name, value));
    return;
  }
  for (const [name, value] of Object.entries(headers as Record<string, string>)) {
    visit(name, value);
  }
}

/**
 * Calls the gateway and returns the raw status, headers and body.
 *
 * Deliberately does not throw on a non-2xx: callers differ in what they do with
 * one. A proxy streams the problem document straight back to the browser so
 * ProblemError can parse it; an auth handler turns it into a cookie decision.
 */
export function gatewayRequest({
  gatewayUrl,
  portalHost,
  path,
  method = 'GET',
  body,
  accessToken,
  headers,
  timeoutMs = 20_000,
}: GatewayRequest): Promise<GatewayResponse> {
  const target = new URL(gatewayUrl.replace(/\/+$/, '') + path);
  const transport = target.protocol === 'https:' ? https : http;

  const outgoing: Record<string, string> = {
    Host: portalHost,
    Accept: 'application/json',
  };

  if (headers) {
    eachHeader(headers, (name, value) => {
      if (!STRIPPED_REQUEST_HEADERS.has(name.toLowerCase())) outgoing[name] = value;
    });
  }
  if (accessToken) {
    outgoing['Authorization'] = `Bearer ${accessToken}`;
  }
  if (body !== undefined) {
    outgoing['Content-Type'] = 'application/json';
    outgoing['Content-Length'] = String(Buffer.byteLength(body));
  }

  return new Promise<GatewayResponse>((resolveRaw, rejectRaw) => {
    // One deadline, cleared however the call ends, so a settled request never
    // leaves a timer holding the event loop open.
    let deadline: ReturnType<typeof setTimeout> | undefined;
    const resolve = (value: GatewayResponse) => {
      clearTimeout(deadline);
      resolveRaw(value);
    };
    const reject = (error: Error) => {
      clearTimeout(deadline);
      rejectRaw(error);
    };

    const request = transport.request(
      {
        protocol: target.protocol,
        hostname: target.hostname,
        port: target.port || (target.protocol === 'https:' ? 443 : 80),
        path: target.pathname + target.search,
        method,
        headers: outgoing,
        // TLS is negotiated against the connection address, but the certificate
        // belongs to the portal hostname the Host header names.
        ...(target.protocol === 'https:' ? { servername: portalHost } : {}),
      },
      (response) => {
        const chunks: Buffer[] = [];
        response.on('data', (chunk: Buffer) => chunks.push(chunk));
        // Without this the promise never settles when the connection drops
        // part-way through the body: 'end' does not fire, and the request
        // object's own 'error' handler does not see a response-side failure.
        // A Next.js render awaiting it would hang until the platform's own
        // timeout, with no log line to say why.
        response.on('error', reject);
        response.on('aborted', () => reject(new Error('gateway closed the connection mid-response')));
        response.on('end', () => {
          const responseHeaders = new Headers();
          for (const [name, value] of Object.entries(response.headers)) {
            if (value === undefined || STRIPPED_RESPONSE_HEADERS.has(name.toLowerCase())) continue;
            responseHeaders.set(name, Array.isArray(value) ? value.join(', ') : value);
          }
          resolve({
            status: response.statusCode ?? 502,
            headers: responseHeaders,
            body: Buffer.concat(chunks).toString('utf8'),
          });
        });
      },
    );

    // setTimeout is a socket-*inactivity* timeout, not a deadline: an upstream
    // that trickles one byte every few seconds resets it forever. It is kept
    // because it fires sooner on the common case of a dead peer, but the
    // deadline below is what actually bounds the call.
    request.setTimeout(timeoutMs, () => {
      request.destroy(new Error(`gateway did not answer within ${timeoutMs}ms`));
    });
    deadline = setTimeout(() => {
      request.destroy(new Error(`gateway did not finish within ${timeoutMs}ms`));
    }, timeoutMs);
    deadline.unref?.();

    request.on('error', reject);

    if (body !== undefined) request.write(body);
    request.end();
  });
}

/** A problem document for the cases where the gateway cannot be reached at all. */
export function unreachableProblem(instance: string, detail?: string): GatewayResponse {
  return {
    status: 502,
    headers: new Headers({ 'content-type': 'application/problem+json' }),
    body: JSON.stringify({
      type: 'about:blank',
      title: 'Bad Gateway',
      status: 502,
      detail: detail ?? 'We could not reach the platform. Please try again shortly.',
      code: 'gateway_unreachable',
      instance,
    }),
  };
}
