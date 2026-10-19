"use client";

import { AlertCircle, CreditCard, Inbox, Lock, WifiOff } from "lucide-react";
import { isProblem, ProblemError } from "@karmarat/ui";
import type { ReactNode } from "react";

import { Skeleton } from "@/components/ui/skeleton";
import { cn } from "@/lib/utils";

/**
 * How this app tells a person something went wrong.
 *
 * Every service answers in RFC 9457 problem+json, and `detail` is written for
 * a reader rather than an operator — so it is what gets rendered. A generic
 * "something went wrong" over the top of a server that said exactly what was
 * wrong throws away the only useful part of the response.
 */
export function ProblemAlert({
  error,
  className,
}: {
  error: unknown;
  className?: string;
}) {
  if (!error) return null;

  const problem = isProblem(error) ? error : null;

  // A 402 is not an error the user did something to cause: the company's
  // subscription has lapsed and nothing they do here will change that. It gets
  // its own wording rather than a red alert that reads like their fault.
  if (problem?.isPaymentRequired) {
    return (
      <div
        role="status"
        className={cn(
          "flex items-start gap-3 rounded-xs border border-warning/30 bg-warning/10 p-3",
          className,
        )}
      >
        <CreditCard aria-hidden="true" className="mt-0.5 size-4 shrink-0 text-warning" />
        <div className="text-sm">
          <p className="font-medium text-foreground">This employer is not accepting applications right now</p>
          <p className="mt-1 text-muted-foreground">{problem.detail}</p>
        </div>
      </div>
    );
  }

  const Icon = problem?.isForbidden
    ? Lock
    : problem?.status === 0
      ? WifiOff
      : AlertCircle;

  const detail =
    problem?.detail ||
    (error instanceof Error ? error.message : "That request could not be completed.");

  // Field-level messages are rendered beside their inputs, so listing them here
  // too would say everything twice. Only the ones with no input to sit beside
  // are surfaced.
  const orphanFields = problem ? Object.keys(problem.fieldErrors) : [];

  return (
    <div
      role="alert"
      className={cn(
        "flex items-start gap-3 rounded-xs border border-destructive/30 bg-destructive/10 p-3",
        className,
      )}
    >
      <Icon aria-hidden="true" className="mt-0.5 size-4 shrink-0 text-destructive" />
      <div className="min-w-0 text-sm">
        <p className="text-foreground">{detail}</p>
        {problem?.isValidation && orphanFields.length > 0 ? (
          <ul className="mt-1 list-disc pl-4 text-muted-foreground">
            {orphanFields.map((field) => (
              <li key={field}>{problem.fieldError(field)}</li>
            ))}
          </ul>
        ) : null}
        {problem?.requestId ? (
          <p className="mt-1 text-xs text-muted-foreground">
            Reference {problem.requestId}
          </p>
        ) : null}
      </div>
    </div>
  );
}

/** The message that belongs directly under the input that failed. */
export function FieldError({ message }: { message?: string }) {
  if (!message) return null;
  return (
    <p className="text-xs text-destructive" role="alert">
      {message}
    </p>
  );
}

/** Pulls a field message out of whatever the caller caught. */
export function fieldErrorOf(error: unknown, field: string): string | undefined {
  return error instanceof ProblemError ? error.fieldError(field) : undefined;
}

export function EmptyState({
  title,
  description,
  action,
  icon: Icon = Inbox,
}: {
  title: string;
  description?: string;
  action?: ReactNode;
  icon?: typeof Inbox;
}) {
  return (
    <div className="surface flex flex-col items-center gap-3 px-6 py-12 text-center">
      <Icon aria-hidden="true" className="size-6 text-muted-foreground" />
      <div>
        <p className="text-sm font-medium">{title}</p>
        {description ? (
          <p className="mt-1 max-w-prose text-sm text-muted-foreground">
            {description}
          </p>
        ) : null}
      </div>
      {action}
    </div>
  );
}

/**
 * The placeholder a list shows while it is loading.
 *
 * A page that renders nothing while it waits is indistinguishable from a page
 * that is broken, and the difference matters most on the slow connection where
 * the wait is longest.
 */
export function ListSkeleton({ rows = 4 }: { rows?: number }) {
  return (
    <div className="space-y-3" aria-busy="true" aria-live="polite">
      <span className="sr-only">Loading…</span>
      {Array.from({ length: rows }).map((_, index) => (
        <div key={index} className="surface space-y-2 p-4">
          <Skeleton className="h-4 w-1/3" />
          <Skeleton className="h-3 w-2/3" />
          <Skeleton className="h-3 w-1/4" />
        </div>
      ))}
    </div>
  );
}

export function PageHeader({
  title,
  description,
  action,
}: {
  title: string;
  description?: string;
  action?: ReactNode;
}) {
  return (
    <div className="flex flex-wrap items-start justify-between gap-3">
      <div>
        <h1 className="text-xl font-semibold tracking-tight">{title}</h1>
        {description ? (
          <p className="mt-1 max-w-prose text-sm text-muted-foreground">
            {description}
          </p>
        ) : null}
      </div>
      {action}
    </div>
  );
}
