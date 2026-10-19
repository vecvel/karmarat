"use server";

import { revalidatePath } from "next/cache";
import { ProblemError } from "@karmarat/ui";

import { gatewayFetch, gatewayRead } from "@/lib/gateway/client";
import { unwrap } from "@/lib/gateway/list";
import type {
  PlatformApplication,
  PlatformApplicationEvent,
  PlatformJob,
  PlatformRejectionReason,
  PlatformStage,
} from "@/lib/gateway/types";
import { requirePermission } from "@/lib/rbac/guard";
import { redactCompensationAll } from "@/lib/rbac/redact";
import { recordAuditLog } from "@/lib/security/audit";

/**
 * The hiring pipeline, served by the applications service.
 *
 * Two differences from the table this used to read matter.
 *
 * First, stages are per-tenant rows rather than an enum. The screens still
 * render a fixed set of columns, so an application carries its stage *key*
 * under the `stage` field they already read; a tenant that renames or adds a
 * stage sees it through `getPipelineStages`. Moving an application resolves the
 * key against the live stage list and says so plainly when the tenant has no
 * such stage, instead of sending an id the service will reject.
 *
 * Second, the candidate behind an application is the applicant's own answers,
 * not a row in a local candidates table. Expected salary, notice period,
 * rating and the rest were local columns with no platform equivalent; they are
 * returned empty so the detail drawer renders, and listed as gaps in the report.
 */

export type ApplicationStage =
  | "applied"
  | "screening"
  | "shortlisted"
  | "interview"
  | "evaluation"
  | "selected"
  | "offer"
  | "hired"
  | "rejected";

function answer(application: PlatformApplication, key: string): string {
  const value = application.answers?.[key];
  return typeof value === "string" ? value : "";
}

/** The row shape the applications screens already read. */
function mapApplication(application: PlatformApplication, job?: PlatformJob) {
  const locations = job?.locations ?? [];

  return {
    id: application.id,
    // The stage *key*, so the existing kanban columns keep matching.
    stage: (application.stageType || application.stageName || "applied").toLowerCase(),
    stageId: application.stageId,
    stageName: application.stageName ?? "",
    stageColor: application.stageColor ?? "",
    // Fit scoring was a local column; nothing on the platform computes one.
    fitScore: null as number | null,
    source: application.source ?? "",
    answers: application.answers ?? {},
    rejectedReason: application.rejectionNote ?? "",
    hiredAt: null as Date | null,
    createdAt: new Date(application.createdAt),
    updatedAt: new Date(application.updatedAt),
    status: application.status,

    jobId: application.jobId,
    jobTitle: application.jobTitle ?? "",
    reqCode: job?.slug ?? "",
    locationText: locations.join(", "),
    workMode: job?.workMode ?? "",
    employmentType: job?.employmentType ?? "",
    customQuestions: [] as unknown[],
    departmentName: job?.department ?? null,

    candidateId: application.candidateId,
    candidateName: application.candidateName || answer(application, "full_name"),
    candidateEmail: application.candidateEmail || answer(application, "email"),
    candidatePhone: answer(application, "phone"),
    candidateCity: answer(application, "location"),
    candidateCountry: "",
    currentDesignation: "",
    currentCompany: "",
    experienceYears: null as number | null,
    totalExperienceText: "",
    expectedSalary: null as number | null,
    expectedSalaryText: "",
    noticePeriodDays: null as number | null,
    noticePeriodText: "",
    rating: null as number | null,
    skills: [] as string[],
    resumeUrl: answer(application, "resume"),
    resumeFileName: "",
    portfolioUrl: answer(application, "portfolio_url"),
    linkedInUrl: answer(application, "linkedin_url"),
    coverLetter: answer(application, "cover_letter"),
    notes: "",
    inTalentPool: false,
  };
}

export type ApplicationRow = ReturnType<typeof mapApplication>;

/* -------------------------------------------------------------------------- */
/* Pipeline configuration                                                     */
/* -------------------------------------------------------------------------- */

export async function getPipelineStages(): Promise<PlatformStage[]> {
  await requirePermission("applications.read");
  const payload = await gatewayFetch<unknown>("/api/v1/applications/settings/stages");
  return unwrap<PlatformStage>(payload, "stages");
}

export async function getRejectionReasons(): Promise<PlatformRejectionReason[]> {
  await requirePermission("applications.read");
  const payload = await gatewayFetch<unknown>("/api/v1/applications/settings/rejection-reasons");
  return unwrap<PlatformRejectionReason>(payload, "rejectionReasons");
}

/** Resolves a stage key the UI uses into the tenant's own stage id. */
async function stageIdForKey(key: string): Promise<string> {
  const stages = await getPipelineStages();
  const match =
    stages.find((stage) => stage.key === key) ??
    stages.find((stage) => stage.type === key) ??
    stages.find((stage) => stage.name.toLowerCase() === key.toLowerCase());

  if (match) return match.id;

  // Better to say which stages exist than to send an id the service will
  // refuse with a message the recruiter cannot act on.
  throw new ProblemError({
    type: "about:blank",
    title: "Stage not configured",
    status: 422,
    detail: `This company's pipeline has no "${key}" stage. Configured stages: ${stages
      .map((stage) => stage.name)
      .join(", ")}.`,
    code: "stage_not_configured",
    errors: { stage: [`"${key}" is not one of this company's pipeline stages.`] },
  });
}

/* -------------------------------------------------------------------------- */
/* Queries                                                                    */
/* -------------------------------------------------------------------------- */

/** Jobs referenced by a page of applications, fetched once and indexed. */
async function jobIndex(applications: PlatformApplication[]): Promise<Map<string, PlatformJob>> {
  const index = new Map<string, PlatformJob>();
  const wanted = [...new Set(applications.map((application) => application.jobId))];
  if (wanted.length === 0) return index;

  const payload = await gatewayRead(
    () => gatewayFetch<unknown>("/api/v1/jobs", { query: { limit: 100 } }),
    null,
  );
  for (const job of unwrap<PlatformJob>(payload, "jobs")) index.set(job.id, job);
  return index;
}

export async function getApplications(params?: { jobId?: string; stage?: string }) {
  const { access } = await requirePermission("applications.read");

  const query: Record<string, string | number> = { limit: 100 };
  if (params?.jobId && params.jobId !== "all") query.jobId = params.jobId;
  if (params?.stage && params.stage !== "all") {
    // Filtering by a stage this tenant does not have should return nothing,
    // not fail the page — the kanban renders every column regardless.
    try {
      query.stageId = await stageIdForKey(params.stage);
    } catch {
      return [];
    }
  }

  const payload = await gatewayFetch<unknown>("/api/v1/applications", { query });
  const applications = unwrap<PlatformApplication>(payload, "applications");
  const jobs = await jobIndex(applications);

  const rows = applications.map((application) =>
    mapApplication(application, jobs.get(application.jobId)),
  );

  return redactCompensationAll(rows, access.can("offers.view_compensation"));
}

export async function getApplicationDetails(applicationId: string) {
  const { access } = await requirePermission("applications.read");

  const application = await gatewayFetch<PlatformApplication>(
    `/api/v1/applications/${encodeURIComponent(applicationId)}`,
  );

  const job = await gatewayRead(
    () => gatewayFetch<PlatformJob>(`/api/v1/jobs/${encodeURIComponent(application.jobId)}`),
    undefined,
  );

  const events = await gatewayRead(
    async () =>
      unwrap<PlatformApplicationEvent>(
        await gatewayFetch<unknown>(
          `/api/v1/applications/${encodeURIComponent(applicationId)}/events`,
        ),
        "events",
      ),
    [] as PlatformApplicationEvent[],
  );

  const row = mapApplication(application, job);
  const [redacted] = redactCompensationAll([row], access.can("offers.view_compensation"));

  return { ...(redacted ?? row), events };
}

/* -------------------------------------------------------------------------- */
/* Mutations                                                                  */
/* -------------------------------------------------------------------------- */

export async function updateApplicationStage(applicationId: string, stage: string, note = "") {
  const { user } = await requirePermission("applications.advance_stage");

  const stageId = await stageIdForKey(stage);
  await gatewayFetch<PlatformApplication>(
    `/api/v1/applications/${encodeURIComponent(applicationId)}/advance`,
    { method: "POST", body: { stageId, note } },
  );

  await recordAuditLog({
    actorId: user.id,
    orgId: user.orgId,
    action: "applications.stage_changed",
    entityType: "application",
    entityId: applicationId,
    metadata: { stage },
  });

  revalidatePath("/applications");
  revalidatePath("/dashboard");
  return { success: true };
}

export async function bulkUpdateApplicationStage(applicationIds: string[], stage: string) {
  const { user } = await requirePermission("applications.bulk_update");

  const stageId = await stageIdForKey(stage);
  const result = await gatewayFetch<{ results: { id: string; status: string; reason?: string }[] }>(
    "/api/v1/applications/bulk",
    { method: "POST", body: { ids: applicationIds, action: "advance", stageId } },
  );

  await recordAuditLog({
    actorId: user.id,
    orgId: user.orgId,
    action: "applications.bulk_stage_changed",
    entityType: "application",
    entityId: applicationIds.join(","),
    metadata: { stage, count: applicationIds.length },
  });

  revalidatePath("/applications");
  return { success: true, results: result?.results ?? [] };
}

export async function rejectApplication(applicationId: string, rejectedReason: string) {
  const { user } = await requirePermission("applications.reject");

  // The screens pass a reason as free text; the service wants one of the
  // tenant's configured reason ids. Match on the label, and fall back to the
  // first configured reason with the text kept as the note so nothing the
  // recruiter wrote is lost.
  const reasons = await getRejectionReasons();
  const active = reasons.filter((reason) => reason.isActive);
  const matched = active.find(
    (reason) => reason.label.toLowerCase() === rejectedReason.trim().toLowerCase(),
  );

  if (!matched && active.length === 0) {
    throw new ProblemError({
      type: "about:blank",
      title: "No rejection reasons configured",
      status: 422,
      detail: "This company has no active rejection reasons. Add one before rejecting an application.",
      code: "no_rejection_reasons",
    });
  }

  const reasonId = (matched ?? active[0])!.id;
  const note = matched ? "" : rejectedReason;

  await gatewayFetch<PlatformApplication>(
    `/api/v1/applications/${encodeURIComponent(applicationId)}/reject`,
    { method: "POST", body: { reasonId, note } },
  );

  await recordAuditLog({
    actorId: user.id,
    orgId: user.orgId,
    action: "applications.rejected",
    entityType: "application",
    entityId: applicationId,
    metadata: { reasonId },
  });

  revalidatePath("/applications");
  revalidatePath("/dashboard");
  return { success: true };
}

export async function deleteApplication(applicationId: string) {
  const { user } = await requirePermission("applications.delete");

  await gatewayFetch<void>(`/api/v1/applications/${encodeURIComponent(applicationId)}`, {
    method: "DELETE",
  });

  await recordAuditLog({
    actorId: user.id,
    orgId: user.orgId,
    action: "applications.deleted",
    entityType: "application",
    entityId: applicationId,
  });

  revalidatePath("/applications");
  return { success: true };
}

/**
 * Adding a candidate straight into a pipeline.
 *
 * The applications service creates an application only from a candidate's own
 * submission (`POST /api/v1/public/apply`, which requires a candidate
 * principal). There is no recruiter-side "apply on behalf of" endpoint, so this
 * cannot be done through the platform today.
 */
export async function createApplicationForCandidate(_data: {
  jobId: string;
  candidateId?: string;
  [key: string]: unknown;
}) {
  await requirePermission("applications.create");

  throw new ProblemError({
    type: "about:blank",
    title: "Not available",
    status: 501,
    detail:
      "Adding a candidate to a pipeline directly is not yet available through the platform API. Applications are created when a candidate applies.",
    code: "not_implemented",
  });
}
