"use client";

import Link from "next/link";
import { useCallback, useEffect, useState } from "react";
import { Briefcase } from "lucide-react";
import { useApi } from "@karmarat/ui/react";

import { EmptyState, ListSkeleton, ProblemAlert } from "@/components/feedback";
import { useSession } from "@/components/session-provider";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import type { Application, ApplicationPage } from "@/lib/api-types";
import { APPLICATION_STATUS_LABELS, formatRelative } from "@/lib/format";

const CLOSED_STATES = new Set(["rejected", "withdrawn", "hired"]);

export function ApplicationsList() {
  const api = useApi();
  const { ready, session } = useSession();

  const [applications, setApplications] = useState<Application[]>([]);
  const [cursor, setCursor] = useState<string>("");
  const [loading, setLoading] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [withdrawing, setWithdrawing] = useState<string | null>(null);
  const [confirming, setConfirming] = useState<string | null>(null);

  const load = useCallback(
    async (from?: string) => {
      const query = new URLSearchParams({ limit: "25" });
      if (from) query.set("cursor", from);

      try {
        const page = await api.get<ApplicationPage>(
          `/api/v1/my-applications?${query.toString()}`,
        );
        setApplications((current) =>
          from ? [...current, ...(page.applications ?? [])] : page.applications ?? [],
        );
        setCursor(page.nextCursor ?? "");
        setError(null);
      } catch (cause) {
        setError(cause);
      } finally {
        setLoading(false);
        setLoadingMore(false);
      }
    },
    [api],
  );

  useEffect(() => {
    if (!ready || !session) return;
    void load();
  }, [ready, session, load]);

  const withdraw = async (id: string) => {
    setWithdrawing(id);
    setError(null);
    try {
      const updated = await api.post<Application>(
        `/api/v1/my-applications/${id}/withdraw`,
      );
      setApplications((current) =>
        current.map((item) => (item.id === id ? updated : item)),
      );
      setConfirming(null);
    } catch (cause) {
      setError(cause);
    } finally {
      setWithdrawing(null);
    }
  };

  if (!ready || loading) return <ListSkeleton rows={3} />;

  if (error && applications.length === 0) {
    return <ProblemAlert error={error} />;
  }

  if (applications.length === 0) {
    return (
      <EmptyState
        icon={Briefcase}
        title="No applications yet"
        description="When you apply for a role it appears here, with the stage it has reached."
        action={
          <Button asChild variant="accent" size="sm">
            <Link href="/">Browse open roles</Link>
          </Button>
        }
      />
    );
  }

  return (
    <div className="space-y-3">
      {error ? <ProblemAlert error={error} /> : null}

      <ul className="space-y-3">
        {applications.map((application) => {
          const closed = CLOSED_STATES.has(application.status);
          return (
            <li key={application.id} className="surface p-4">
              <div className="flex flex-wrap items-start justify-between gap-3">
                <div className="min-w-0">
                  <p className="font-medium">{application.jobTitle}</p>
                  <p className="text-sm text-muted-foreground">
                    {application.companyName}
                  </p>
                </div>

                <div className="flex flex-wrap items-center gap-2">
                  {/* The stage name is the company's own vocabulary and is what
                      the candidate has been told; the status is this platform's
                      and is the fallback when no stage has been set. */}
                  <Badge
                    variant={
                      application.status === "rejected"
                        ? "soft-destructive"
                        : application.status === "hired"
                          ? "soft-success"
                          : application.status === "withdrawn"
                            ? "soft-neutral"
                            : "soft-accent"
                    }
                  >
                    {application.stageName ||
                      APPLICATION_STATUS_LABELS[application.status] ||
                      application.status}
                  </Badge>
                </div>
              </div>

              {application.rejectionReason ? (
                <p className="mt-2 text-sm text-muted-foreground">
                  Reason given: {application.rejectionReason}
                </p>
              ) : null}

              <div className="mt-3 flex flex-wrap items-center justify-between gap-3">
                <p className="text-xs text-muted-foreground">
                  Applied {formatRelative(application.submittedAt)}
                  {application.withdrawnAt
                    ? ` · withdrawn ${formatRelative(application.withdrawnAt)}`
                    : ""}
                </p>

                {!closed ? (
                  confirming === application.id ? (
                    <span className="flex items-center gap-2">
                      <span className="text-xs text-muted-foreground">
                        Withdraw this application?
                      </span>
                      <Button
                        size="xs"
                        variant="destructive"
                        disabled={withdrawing === application.id}
                        onClick={() => void withdraw(application.id)}
                      >
                        {withdrawing === application.id ? "Withdrawing…" : "Yes, withdraw"}
                      </Button>
                      <Button
                        size="xs"
                        variant="ghost"
                        onClick={() => setConfirming(null)}
                      >
                        Keep it
                      </Button>
                    </span>
                  ) : (
                    <Button
                      size="xs"
                      variant="outline"
                      onClick={() => setConfirming(application.id)}
                    >
                      Withdraw
                    </Button>
                  )
                ) : null}
              </div>
            </li>
          );
        })}
      </ul>

      {cursor && applications.length % 25 === 0 ? (
        <Button
          variant="outline"
          className="w-full"
          disabled={loadingMore}
          onClick={() => {
            setLoadingMore(true);
            void load(cursor);
          }}
        >
          {loadingMore ? "Loading…" : "Load more"}
        </Button>
      ) : null}
    </div>
  );
}
