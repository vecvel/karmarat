import { NextResponse } from "next/server";

import { ProblemError } from "@karmarat/ui";

import { gatewayFetch } from "@/lib/gateway";
import { type SlugCheckResult, normaliseSlug, slugProblem } from "@/lib/slug";

/**
 * Live availability for the company-address field.
 *
 * It exists as a route handler rather than a direct call from the form because
 * a browser cannot set the Host header the gateway routes on, and would in any
 * case be refused by CORS in development. Same reason the auth routes are
 * handlers: the hostname question is a server-side one.
 */

interface GatewaySlugAvailability {
  slug: string;
  available: boolean;
  reason?: string;
}

export async function GET(request: Request): Promise<NextResponse<SlugCheckResult>> {
  const slug = normaliseSlug(new URL(request.url).searchParams.get("slug") ?? "");

  // Shape first, and locally. A slug with a space in it does not need a network
  // call to be wrong, and refusing it here is also what keeps the reserved-name
  // answer instant. The server checks all of this again on submit.
  const localProblem = slugProblem(slug);
  if (localProblem) {
    return NextResponse.json({ slug, status: "unavailable", reason: localProblem });
  }

  try {
    const result = await gatewayFetch<GatewaySlugAvailability>(
      `/api/v1/register/slug-available?slug=${encodeURIComponent(slug)}`,
    );
    return NextResponse.json({
      slug,
      status: result.available ? "available" : "unavailable",
      ...(result.reason ? { reason: result.reason } : {}),
    });
  } catch (error) {
    if (error instanceof ProblemError) {
      // Rate limited. The endpoint is capped on purpose — unlimited it would
      // enumerate every tenant on the platform — so backing off is correct
      // behaviour, not a failure to report.
      if (error.isRateLimited) {
        return NextResponse.json({
          slug,
          status: "unknown",
          reason: "Checking too quickly. We will confirm this address when you submit.",
          retryAfter: 30,
        });
      }

      // The companies service implements this route, but the gateway does not
      // currently expose it (only /api/v1/register/company is in the routing
      // table), so it answers 404. Reporting "available" here would be a lie
      // and reporting "taken" would block a legitimate name, so the form says
      // it does not know and the submit decides.
      if (error.status === 404) {
        return NextResponse.json({
          slug,
          status: "unknown",
          reason: "We will confirm this address when you submit.",
        });
      }
    }

    return NextResponse.json({
      slug,
      status: "unknown",
      reason: "We could not check this address just now. We will confirm it when you submit.",
    });
  }
}
