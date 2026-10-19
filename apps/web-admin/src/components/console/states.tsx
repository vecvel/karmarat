"use client";

import Link from "next/link";
import type { ReactNode } from "react";
import { AlertTriangle, CreditCard, Inbox, Lock, RefreshCw } from "lucide-react";
import type { ProblemError } from "@karmarat/ui";

import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { cn } from "@/lib/utils";

/**
 * Every error a user sees in this console comes through here.
 *
 * The rule is that the server's own words win. `detail` is written by the
 * service that refused the request and is the only description that is actually
 * true; a generic "something went wrong" throws that away and leaves the
 * operator with nothing to act on.
 */
export function ProblemView({
  problem,
  onRetry,
  className,
}: {
  problem: ProblemError;
  onRetry?: (() => void) | undefined;
  className?: string;
}) {
  // 402 is not an error state, it is a billing state, and the route that fixes
  // it has to stay reachable from wherever the operator hit it.
  if (problem.isPaymentRequired) {
    return <SubscriptionRequired problem={problem} className={className} />;
  }

  const forbidden = problem.isForbidden;

  return (
    <div
      role="alert"
      className={cn(
        "surface flex flex-col items-start gap-3 p-6",
        forbidden ? "border-warning/40" : "border-destructive/40",
        className,
      )}
    >
      <div className="flex items-center gap-2">
        {forbidden ? (
          <Lock className="size-4 text-warning" aria-hidden />
        ) : (
          <AlertTriangle className="size-4 text-destructive" aria-hidden />
        )}
        <h2 className="text-sm font-semibold">{problem.title}</h2>
      </div>

      <p className="max-w-prose text-sm text-muted-foreground">{problem.detail}</p>

      {Object.keys(problem.fieldErrors).length > 0 ? (
        <ul className="list-disc space-y-1 pl-5 text-sm text-muted-foreground">
          {Object.entries(problem.fieldErrors).map(([field, messages]) => (
            <li key={field}>
              <span className="font-medium text-foreground">{field}</span>: {messages.join(" ")}
            </li>
          ))}
        </ul>
      ) : null}

      <div className="flex items-center gap-3">
        {onRetry ? (
          <Button type="button" variant="outline" size="sm" onClick={onRetry}>
            <RefreshCw aria-hidden />
            Try again
          </Button>
        ) : null}
        {problem.requestId ? (
          // Support's first question is always "what was the request id?".
          <span className="font-mono text-[11px] text-muted-foreground">{problem.requestId}</span>
        ) : null}
      </div>
    </div>
  );
}

/** A 402 rendered as the state it is: the tenant has to pay, and here is where. */
export function SubscriptionRequired({
  problem,
  className,
}: {
  problem: ProblemError;
  className?: string;
}) {
  return (
    <div className={cn("surface flex flex-col items-start gap-3 border-accent/40 p-6", className)}>
      <div className="flex items-center gap-2">
        <CreditCard className="size-4 text-accent" aria-hidden />
        <h2 className="text-sm font-semibold">Subscription required</h2>
      </div>
      <p className="max-w-prose text-sm text-muted-foreground">{problem.detail}</p>
      <Button asChild size="sm" variant="accent">
        <Link href="/subscriptions">Review subscriptions</Link>
      </Button>
    </div>
  );
}

/** What a list renders when it succeeded and there was simply nothing there. */
export function EmptyState({
  title,
  description,
  action,
  icon,
}: {
  title: string;
  description?: string;
  action?: ReactNode;
  icon?: ReactNode;
}) {
  return (
    <div className="surface flex flex-col items-center gap-2 px-6 py-12 text-center">
      <div className="text-muted-foreground">{icon ?? <Inbox className="size-5" aria-hidden />}</div>
      <h2 className="text-sm font-semibold">{title}</h2>
      {description ? (
        <p className="max-w-md text-sm text-muted-foreground">{description}</p>
      ) : null}
      {action ? <div className="pt-2">{action}</div> : null}
    </div>
  );
}

/** A table-shaped placeholder, so the page does not jump when the rows land. */
export function TableSkeleton({ rows = 6, columns = 5 }: { rows?: number; columns?: number }) {
  return (
    <div className="surface divide-y divide-border" aria-busy="true" aria-live="polite">
      <span className="sr-only">Loading…</span>
      {Array.from({ length: rows }).map((_, rowIndex) => (
        <div
          key={rowIndex}
          className="grid items-center gap-4 px-4 py-3"
          style={{ gridTemplateColumns: `repeat(${columns}, minmax(0, 1fr))` }}
        >
          {Array.from({ length: columns }).map((__, columnIndex) => (
            <Skeleton key={columnIndex} className="h-4 w-full max-w-[12rem]" />
          ))}
        </div>
      ))}
    </div>
  );
}

export function CardSkeleton({ count = 4 }: { count?: number }) {
  return (
    <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4" aria-busy="true" aria-live="polite">
      <span className="sr-only">Loading…</span>
      {Array.from({ length: count }).map((_, index) => (
        <div key={index} className="surface space-y-3 p-4">
          <Skeleton className="h-3 w-24" />
          <Skeleton className="h-7 w-16" />
        </div>
      ))}
    </div>
  );
}
