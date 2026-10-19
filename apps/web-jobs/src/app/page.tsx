import Link from "next/link";
import { Briefcase } from "lucide-react";
import { isProblem } from "@karmarat/ui";

import { JobCard } from "@/components/jobs/job-card";
import { JobFilters, type JobFilterValues } from "@/components/jobs/job-filters";
import { Button } from "@/components/ui/button";
import type { JobBoardPage } from "@/lib/api-types";
import { gatewayJson } from "@/lib/gateway";

export const metadata = {
  title: "Find your next role",
};

// The board is public and changes slowly, so the framework may hold a rendered
// page briefly. Nothing here carries a principal, so nothing here can leak
// between people.
export const revalidate = 60;

type SearchParams = Promise<Record<string, string | string[] | undefined>>;

function one(params: Record<string, string | string[] | undefined>, key: string): string {
  const value = params[key];
  return (Array.isArray(value) ? value[0] : value) ?? "";
}

export default async function JobBoardPageRoute({
  searchParams,
}: {
  searchParams: SearchParams;
}) {
  const params = await searchParams;
  const filters: JobFilterValues = {
    q: one(params, "q"),
    location: one(params, "location"),
    department: one(params, "department"),
    employmentType: one(params, "employmentType"),
    workMode: one(params, "workMode"),
  };
  const cursor = one(params, "cursor");

  const query = new URLSearchParams();
  for (const [key, value] of Object.entries(filters)) {
    if (value) query.set(key, value);
  }
  if (cursor) query.set("cursor", cursor);
  query.set("limit", "20");

  let page: JobBoardPage | null = null;
  let failure: string | null = null;

  try {
    page = await gatewayJson<JobBoardPage>(
      `/api/v1/public/jobs?${query.toString()}`,
      { revalidate: 60 },
    );
  } catch (error) {
    // A 422 here means a filter value the service does not recognise — which
    // can only come from a hand-edited URL, so it is shown as an empty board
    // with the reason rather than as a crash.
    failure = isProblem(error)
      ? error.detail
      : "We could not load the job board just now.";
  }

  const jobs = page?.jobs ?? [];
  const filtered = Object.values(filters).some(Boolean);

  // The next page's link carries the filters forward; the service returns a
  // cursor even on the last page, so "Next" is shown only when this page was
  // full.
  const nextQuery = new URLSearchParams(query);
  nextQuery.delete("limit");
  if (page?.nextCursor) nextQuery.set("cursor", page.nextCursor);
  const hasNext = jobs.length === 20 && Boolean(page?.nextCursor);

  const resetQuery = new URLSearchParams(query);
  resetQuery.delete("cursor");
  resetQuery.delete("limit");

  return (
    <div className="page">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">
          Every role on the network
        </h1>
        <p className="mt-1 max-w-prose text-sm text-muted-foreground">
          One profile, one résumé, every company hiring through Karmarat.
          Apply in a few minutes and follow each application from here.
        </p>
      </div>

      <JobFilters values={filters} />

      {failure ? (
        <div role="alert" className="surface border-destructive/30 bg-destructive/10 p-4 text-sm">
          {failure}
        </div>
      ) : jobs.length === 0 ? (
        <div className="surface flex flex-col items-center gap-3 px-6 py-12 text-center">
          <Briefcase aria-hidden="true" className="size-6 text-muted-foreground" />
          <div>
            <p className="text-sm font-medium">
              {filtered ? "No roles match those filters" : "No roles are open right now"}
            </p>
            <p className="mt-1 text-sm text-muted-foreground">
              {filtered
                ? "Try a broader search — fewer filters, or a wider location."
                : "New roles are posted regularly. Create an account and we will let you know."}
            </p>
          </div>
          {filtered ? (
            <Button asChild variant="outline" size="sm">
              <Link href="/">Clear filters</Link>
            </Button>
          ) : (
            <Button asChild variant="accent" size="sm">
              <Link href="/register">Create an account</Link>
            </Button>
          )}
        </div>
      ) : (
        <>
          <p className="text-sm text-muted-foreground" aria-live="polite">
            Showing {jobs.length} {jobs.length === 1 ? "role" : "roles"}
            {filtered ? " matching your search" : ""}.
          </p>

          <div className="grid gap-3">
            {jobs.map((job) => (
              <JobCard key={job.id} job={job} />
            ))}
          </div>

          {(hasNext || cursor) && (
            <nav
              aria-label="Job board pages"
              className="flex items-center justify-between gap-3 pt-2"
            >
              {cursor ? (
                <Button asChild variant="outline" size="sm">
                  <Link href={`/?${resetQuery.toString()}`}>Back to the start</Link>
                </Button>
              ) : (
                <span />
              )}
              {hasNext ? (
                <Button asChild variant="outline" size="sm">
                  <Link href={`/?${nextQuery.toString()}`}>Next page</Link>
                </Button>
              ) : null}
            </nav>
          )}
        </>
      )}
    </div>
  );
}
