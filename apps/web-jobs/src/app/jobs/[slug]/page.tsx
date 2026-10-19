import Link from "next/link";
import { notFound } from "next/navigation";
import { ArrowLeft, Building2, Clock, MapPin } from "lucide-react";
import { isProblem } from "@karmarat/ui";

import { ApplyCallout } from "@/components/jobs/apply-callout";
import { Badge } from "@/components/ui/badge";
import { Separator } from "@/components/ui/separator";
import type { JobDetail } from "@/lib/api-types";
import {
  employmentTypeLabel,
  formatRelative,
  formatSalaryRange,
  seniorityLabel,
  workModeLabel,
} from "@/lib/format";
import { gatewayJson } from "@/lib/gateway";
import { renderMarkdown } from "@/lib/markdown";

export const revalidate = 60;

type Params = Promise<{ slug: string }>;

async function loadJob(slug: string): Promise<JobDetail> {
  try {
    return await gatewayJson<JobDetail>(
      `/api/v1/public/jobs/${encodeURIComponent(slug)}`,
      { revalidate: 60 },
    );
  } catch (error) {
    // The service answers 404 both for a job that never existed and for one
    // that is not listed on this board, deliberately — so this page cannot tell
    // the difference either, and should not try.
    if (isProblem(error) && error.status === 404) notFound();
    throw error;
  }
}

export async function generateMetadata({ params }: { params: Params }) {
  try {
    const job = await loadJob((await params).slug);
    return {
      title: job.title,
      description: job.description.slice(0, 200),
    };
  } catch {
    return { title: "Role" };
  }
}

export default async function JobDetailPage({ params }: { params: Params }) {
  const { slug } = await params;
  const job = await loadJob(slug);

  const salary = formatSalaryRange(job.salary);
  const locations = job.locations.filter(Boolean);

  return (
    <div className="page">
      <Link
        href="/"
        className="inline-flex w-fit items-center gap-1.5 text-sm text-muted-foreground hover:text-foreground"
      >
        <ArrowLeft aria-hidden="true" className="size-3.5" />
        All roles
      </Link>

      <div className="grid gap-6 lg:grid-cols-[1fr_20rem]">
        <div className="min-w-0">
          <h1 className="text-2xl font-semibold tracking-tight">{job.title}</h1>

          <div className="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-sm text-muted-foreground">
            {job.department ? (
              <span className="inline-flex items-center gap-1.5">
                <Building2 aria-hidden="true" className="size-3.5" />
                {job.department}
              </span>
            ) : null}
            {locations.length > 0 ? (
              <span className="inline-flex items-center gap-1.5">
                <MapPin aria-hidden="true" className="size-3.5" />
                {locations.join(", ")}
              </span>
            ) : null}
            {job.publishedAt ? (
              <span className="inline-flex items-center gap-1.5">
                <Clock aria-hidden="true" className="size-3.5" />
                Posted {formatRelative(job.publishedAt)}
              </span>
            ) : null}
          </div>

          <div className="mt-3 flex flex-wrap gap-1.5">
            <Badge variant="soft-neutral">{workModeLabel(job.workMode)}</Badge>
            <Badge variant="soft-neutral">
              {employmentTypeLabel(job.employmentType)}
            </Badge>
            <Badge variant="soft-neutral">{seniorityLabel(job.seniority)}</Badge>
            {salary ? <Badge variant="soft-accent">{salary}</Badge> : null}
          </div>

          <Separator className="my-6" />

          <section aria-labelledby="about-the-role">
            <h2 id="about-the-role" className="section-title">
              About the role
            </h2>
            <div
              className="prose-job mt-3"
              // The renderer escapes every byte of the source before it emits
              // any markup, so this cannot introduce a tag the recruiter did
              // not literally type as text.
              dangerouslySetInnerHTML={{ __html: renderMarkdown(job.description) }}
            />
          </section>

          {job.requirements ? (
            <section aria-labelledby="requirements" className="mt-8">
              <h2 id="requirements" className="section-title">
                What we are looking for
              </h2>
              <div
                className="prose-job mt-3"
                dangerouslySetInnerHTML={{
                  __html: renderMarkdown(job.requirements),
                }}
              />
            </section>
          ) : null}
        </div>

        <aside className="lg:sticky lg:top-20 lg:self-start">
          <ApplyCallout jobId={job.id} slug={job.slug} title={job.title} />
        </aside>
      </div>
    </div>
  );
}
