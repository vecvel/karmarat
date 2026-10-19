import "server-only";

import { headers } from "next/headers";

/**
 * Where the gateway is, and who we claim to be when we call it.
 *
 * The gateway decides the portal and the tenant from the Host header alone, so
 * a server-side call has to carry the portal's hostname even though Node
 * reached the gateway over `localhost`. Getting this wrong does not fail
 * loudly — it resolves a *different* tenant — so the value is configuration
 * rather than anything inferred at the call site.
 */

export const GATEWAY_URL = process.env.GATEWAY_URL?.replace(/\/+$/, "") || "http://localhost:8080";

/** The platform's base hostname; a company portal lives at `{slug}.{this}`. */
export const PLATFORM_HOSTNAME = process.env.PLATFORM_HOSTNAME || "karmarat.local";

/** Hostnames that are never a company slug, mirroring the gateway's reserved list. */
const NOT_A_SLUG = new Set(["www", "api", "root", "jobs", "localhost", "app", "admin"]);

/**
 * The company slug this deployment serves.
 *
 * In production the slug is the first label of the hostname the request
 * arrived on, which is what makes the tenant boundary a deployment fact rather
 * than a client-supplied one. In development the app is usually reached as
 * `localhost:3000`, which has no slug in it, so COMPANY_SLUG fills the gap.
 */
export function companySlugFromHost(host: string | null | undefined): string | null {
  if (!host) return null;
  const hostname = host.split(":")[0]?.toLowerCase() ?? "";
  if (!hostname || hostname === PLATFORM_HOSTNAME) return null;

  const suffix = `.${PLATFORM_HOSTNAME}`;
  const label = hostname.endsWith(suffix)
    ? hostname.slice(0, -suffix.length)
    : hostname.split(".")[0] ?? "";

  if (!label || label.includes(".") || NOT_A_SLUG.has(label)) return null;
  return label;
}

/** The slug for the current request: the hostname first, the env var as a fallback. */
export async function resolveCompanySlug(): Promise<string> {
  const headerList = await headers();
  const fromHost = companySlugFromHost(headerList.get("host"));
  const slug = fromHost ?? process.env.COMPANY_SLUG ?? "";

  if (!slug) {
    throw new Error(
      "No company slug: the app was not reached on {slug}." +
        PLATFORM_HOSTNAME +
        " and COMPANY_SLUG is unset. See apps/web-company/README.md.",
    );
  }
  return slug;
}

/** The Host header the gateway must see for this request to resolve our tenant. */
export async function resolvePortalHost(): Promise<string> {
  if (process.env.PORTAL_HOST) return process.env.PORTAL_HOST;
  return `${await resolveCompanySlug()}.${PLATFORM_HOSTNAME}`;
}
