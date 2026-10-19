import Link from "next/link";
import { Check, Minus } from "lucide-react";
import { ProblemError } from "@karmarat/ui";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { gatewayFetch } from "@/lib/gateway";
import {
  type PublicPlan,
  type PublicPlansResponse,
  formatInterval,
  formatMoney,
  planFeatures,
} from "@/lib/plans";

/**
 * Published plans, as the subscriptions service currently has them.
 *
 * Cached for a minute rather than per-request: the catalogue changes when a
 * platform operator edits it, which is rare, and a pricing page that re-fetches
 * for every visitor turns a marketing page into load on a service. A minute is
 * short enough that a published plan appears while the operator is still
 * looking at the page.
 */
export async function PlanGrid() {
  let plans: PublicPlan[];
  try {
    const response = await gatewayFetch<PublicPlansResponse>("/api/v1/public/plans", {
      revalidate: 60,
    });
    plans = response.items ?? [];
  } catch (error) {
    // Whatever the server said is what the visitor is told. A pricing page that
    // says "something went wrong" when the gateway said "we are rate limiting
    // you" has thrown away the only useful part of the answer.
    const problem =
      error instanceof ProblemError
        ? error
        : new ProblemError({
            type: "about:blank",
            title: "Pricing unavailable",
            status: 500,
            detail: "We could not load our plans just now. Please try again shortly.",
            code: "internal_error",
          });

    return (
      <Alert variant="destructive">
        <AlertTitle>We could not load our plans</AlertTitle>
        <AlertDescription>
          <p>{problem.detail}</p>
          <p className="mt-3">
            You can still{" "}
            <Link href="/signup/company" className="underline underline-offset-4">
              create a company account
            </Link>{" "}
            — you choose a plan from inside your own portal.
          </p>
        </AlertDescription>
      </Alert>
    );
  }

  if (plans.length === 0) {
    return (
      <div className="rounded-xs border border-border bg-card p-8 text-center">
        <h2 className="text-lg">No plans are published yet</h2>
        <p className="mx-auto mt-2 max-w-md text-sm text-muted-foreground">
          Our plans are not on sale at this moment. Rather than show you a price we
          would not honour, this page shows nothing. You can still create a company
          account and choose a plan when one is available.
        </p>
        <Button asChild variant="accent" className="mt-6">
          <Link href="/signup/company">Create a company account</Link>
        </Button>
      </div>
    );
  }

  return (
    <div
      className={
        // One, two or three plans each want a different column count; more than
        // three wraps rather than shrinking every card into a strip.
        plans.length === 1
          ? "grid max-w-md gap-6"
          : plans.length === 2
            ? "grid gap-6 sm:grid-cols-2"
            : "grid gap-6 sm:grid-cols-2 lg:grid-cols-3"
      }
    >
      {plans.map((plan) => (
        <PlanCard key={plan.id} plan={plan} />
      ))}
    </div>
  );
}

function PlanCard({ plan }: { plan: PublicPlan }) {
  const features = planFeatures(plan.entitlements);

  return (
    <article className="flex flex-col rounded-xs border border-border bg-card p-6">
      <header>
        <div className="flex items-start justify-between gap-3">
          <h2 className="text-lg">{plan.name}</h2>
          {plan.trialDays > 0 ? (
            <Badge variant="soft-accent">{plan.trialDays}-day trial</Badge>
          ) : null}
        </div>
        {plan.description ? (
          <p className="mt-2 text-sm leading-relaxed text-muted-foreground">
            {plan.description}
          </p>
        ) : null}
      </header>

      <p className="mt-6 flex flex-wrap items-baseline gap-x-2">
        <span className="text-3xl font-semibold tabular">
          {formatMoney(plan.price.amount, plan.price.currency)}
        </span>
        <span className="text-sm text-muted-foreground">
          {formatInterval(plan.interval, plan.intervalCount)}
        </span>
      </p>

      <ul className="mt-6 flex flex-1 flex-col gap-2.5 text-sm">
        {features.map((feature) => (
          <li key={feature.label} className="flex items-start gap-2.5">
            {feature.included ? (
              <Check aria-hidden="true" className="mt-0.5 size-4 shrink-0 text-sage-deep" />
            ) : (
              <Minus aria-hidden="true" className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
            )}
            <span className={feature.included ? undefined : "text-muted-foreground"}>
              {feature.label}
              {/* The icon carries the meaning visually; this carries it for a
                  screen reader, which would otherwise hear two identical lists. */}
              <span className="sr-only">{feature.included ? " — included" : " — not included"}</span>
            </span>
          </li>
        ))}
      </ul>

      <Button asChild variant="accent" className="mt-7 w-full">
        <Link href={`/signup/company?plan=${encodeURIComponent(plan.key)}`}>
          Start with {plan.name}
        </Link>
      </Button>
    </article>
  );
}

/** What the grid looks like while the gateway is answering. */
export function PlanGridSkeleton() {
  return (
    <div className="grid gap-6 sm:grid-cols-2 lg:grid-cols-3" aria-busy="true">
      <span className="sr-only">Loading plans…</span>
      {[0, 1, 2].map((index) => (
        <div key={index} className="rounded-xs border border-border bg-card p-6">
          <Skeleton className="h-5 w-28" />
          <Skeleton className="mt-3 h-4 w-full" />
          <Skeleton className="mt-1.5 h-4 w-4/5" />
          <Skeleton className="mt-6 h-9 w-36" />
          <div className="mt-6 flex flex-col gap-2.5">
            {[0, 1, 2, 3, 4, 5].map((line) => (
              <Skeleton key={line} className="h-4 w-full" />
            ))}
          </div>
          <Skeleton className="mt-7 h-9 w-full" />
        </div>
      ))}
    </div>
  );
}
