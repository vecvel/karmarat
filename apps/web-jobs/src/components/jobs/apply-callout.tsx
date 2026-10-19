"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
import { CheckCircle2 } from "lucide-react";
import { useApi } from "@karmarat/ui/react";

import { useSession } from "@/components/session-provider";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import type { Application, ApplicationPage } from "@/lib/api-types";
import { APPLICATION_STATUS_LABELS } from "@/lib/format";

/**
 * The one thing a candidate came to this page to do.
 *
 * Applying twice is a 409 from the service, and discovering that by filling in
 * a form and pressing submit is a bad way to find out — so an existing
 * application is looked up first and shown as a state of the job rather than
 * as an error.
 */
export function ApplyCallout({
  jobId,
  slug,
  title,
}: {
  jobId: string;
  slug: string;
  title: string;
}) {
  const api = useApi();
  const { session, ready } = useSession();
  const [existing, setExisting] = useState<Application | null>(null);
  const [checking, setChecking] = useState(false);

  useEffect(() => {
    if (!ready || !session) return;

    let cancelled = false;
    setChecking(true);

    void (async () => {
      try {
        const page = await api.get<ApplicationPage>(
          "/api/v1/my-applications?limit=100",
        );
        if (cancelled) return;
        setExisting(
          (page.applications ?? []).find((item) => item.jobId === jobId) ?? null,
        );
      } catch {
        // Not knowing is not worth blocking the page over: the apply form is
        // still reachable and the server will answer 409 if it must.
        if (!cancelled) setExisting(null);
      } finally {
        if (!cancelled) setChecking(false);
      }
    })();

    return () => {
      cancelled = true;
    };
  }, [api, jobId, ready, session]);

  if (!ready) {
    return (
      <div className="surface space-y-3 p-4" aria-busy="true">
        <Skeleton className="h-4 w-2/3" />
        <Skeleton className="h-9 w-full" />
      </div>
    );
  }

  if (!session) {
    return (
      <div className="surface space-y-3 p-4">
        <p className="text-sm font-medium">Interested in {title}?</p>
        <p className="text-sm text-muted-foreground">
          Sign in or create a free candidate account to apply and to follow the
          application afterwards.
        </p>
        <div className="flex flex-col gap-2">
          <Button asChild variant="accent">
            <Link href={`/register?next=${encodeURIComponent(`/jobs/${slug}/apply`)}`}>
              Create an account
            </Link>
          </Button>
          <Button asChild variant="outline">
            <Link href={`/sign-in?next=${encodeURIComponent(`/jobs/${slug}/apply`)}`}>
              Sign in
            </Link>
          </Button>
        </div>
      </div>
    );
  }

  if (checking) {
    return (
      <div className="surface space-y-3 p-4" aria-busy="true">
        <Skeleton className="h-4 w-2/3" />
        <Skeleton className="h-9 w-full" />
      </div>
    );
  }

  if (existing) {
    const withdrawn = existing.status === "withdrawn";
    return (
      <div className="surface space-y-3 p-4">
        <p className="inline-flex items-center gap-2 text-sm font-medium">
          <CheckCircle2 aria-hidden="true" className="size-4 text-success" />
          {withdrawn ? "You withdrew this application" : "You have applied"}
        </p>
        <p className="text-sm text-muted-foreground">
          {withdrawn
            ? "This role is no longer accepting a second application from you."
            : `Current status: ${
                existing.stageName ||
                APPLICATION_STATUS_LABELS[existing.status] ||
                existing.status
              }.`}
        </p>
        <Button asChild variant="outline" className="w-full">
          <Link href="/applications">Track this application</Link>
        </Button>
      </div>
    );
  }

  return (
    <div className="surface space-y-3 p-4">
      <p className="text-sm font-medium">Ready to apply?</p>
      <p className="text-sm text-muted-foreground">
        The employer asks a few questions of their own. It usually takes a
        couple of minutes.
      </p>
      <Button asChild variant="accent" className="w-full">
        <Link href={`/jobs/${slug}/apply`}>Apply for this role</Link>
      </Button>
    </div>
  );
}
