import Link from "next/link";
import { notFound, redirect } from "next/navigation";
import { ArrowLeft } from "lucide-react";
import { isProblem } from "@karmarat/ui";

import { ApplyForm } from "@/components/apply/apply-form";
import type { JobFormResponse } from "@/lib/api-types";
import { gatewayJson } from "@/lib/gateway";
import { readIdentity } from "@/lib/session";

type Params = Promise<{ slug: string }>;

export const metadata = { title: "Apply" };

export default async function ApplyPage({ params }: { params: Params }) {
  const { slug } = await params;

  // The route guard. It is a courtesy, not the boundary: the apply endpoint
  // refuses an unauthenticated call regardless, and would refuse it even if
  // this check were deleted.
  const identity = await readIdentity();
  if (!identity) {
    redirect(`/sign-in?next=${encodeURIComponent(`/jobs/${slug}/apply`)}`);
  }

  let form: JobFormResponse;
  try {
    form = await gatewayJson<JobFormResponse>(
      `/api/v1/public/jobs/${encodeURIComponent(slug)}/form`,
      { revalidate: 60 },
    );
  } catch (error) {
    if (isProblem(error) && error.status === 404) notFound();
    throw error;
  }

  return (
    <div className="page max-w-3xl">
      <Link
        href={`/jobs/${slug}`}
        className="inline-flex w-fit items-center gap-1.5 text-sm text-muted-foreground hover:text-foreground"
      >
        <ArrowLeft aria-hidden="true" className="size-3.5" />
        Back to the role
      </Link>

      <div>
        <h1 className="text-xl font-semibold tracking-tight">
          Apply for {form.title}
        </h1>
        <p className="mt-1 text-sm text-muted-foreground">
          These questions are the employer&apos;s own. Fields marked with an
          asterisk are required.
        </p>
      </div>

      <ApplyForm
        jobId={form.jobId}
        slug={slug}
        title={form.title}
        form={form.form}
      />
    </div>
  );
}
