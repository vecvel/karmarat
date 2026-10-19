import "server-only";

/**
 * Where this app is, and where the gateway is.
 *
 * Both are environment, never constants: the same build runs on
 * `karmarat.local` in development and on the real hostname in production,
 * and the only difference is what it reports in the Host header.
 */

/**
 * The gateway's origin. Every read and write this app performs goes here —
 * there is no database client in this app and there must never be one.
 */
export const GATEWAY_URL = (process.env.GATEWAY_URL ?? "http://localhost:8080").replace(/\/+$/, "");

/**
 * The hostname this portal answers on.
 *
 * The gateway resolves the portal from the Host header, so a server-side call
 * has to say which portal it is calling from. In production the incoming
 * request already carries it; sending it explicitly means the value does not
 * change when the app sits behind a load balancer that rewrites Host, and it
 * means a developer running on `localhost:3003` still reaches the public
 * portal rather than whatever `localhost` happens to resolve to.
 */
export const PORTAL_HOST = process.env.PORTAL_HOST ?? "karmarat.local";

/** `http` locally, `https` everywhere a certificate exists. */
export const PORTAL_SCHEME =
  process.env.PORTAL_SCHEME ?? (process.env.NODE_ENV === "production" ? "https" : "http");

/**
 * The candidate portal.
 *
 * Overridable because in development each portal is a separate Next server on
 * its own port, so `jobs.karmarat.local` alone would not reach it.
 */
export const JOBS_PORTAL_URL =
  process.env.JOBS_PORTAL_URL ?? `${PORTAL_SCHEME}://jobs.${PORTAL_HOST}`;

/** The platform console. Linked only from the footer; staff know it exists. */
export const ROOT_PORTAL_URL =
  process.env.ROOT_PORTAL_URL ?? `${PORTAL_SCHEME}://root.${PORTAL_HOST}`;

/**
 * A tenant's own portal.
 *
 * Used to show a registering company what their address will be, and to send
 * them somewhere real once they have one.
 */
export function companyPortalUrl(slug: string): string {
  return `${PORTAL_SCHEME}://${slug}.${PORTAL_HOST}`;
}
