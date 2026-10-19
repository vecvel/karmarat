"use server";

import { revalidatePath } from "next/cache";
import { ProblemError } from "@karmarat/ui";

import { gatewayFetch, gatewayRead } from "@/lib/gateway/client";
import { unwrap } from "@/lib/gateway/list";
import type {
  PlatformApplication,
  PlatformPoolCandidate,
  PlatformTalentProfile,
} from "@/lib/gateway/types";
import { requirePermission } from "@/lib/rbac/guard";
import { recordAuditLog } from "@/lib/security/audit";

/**
 * The company's candidate pool, served by the candidates service.
 *
 * The platform separates two things this app kept in one table: a *pool
 * candidate* is a record the company owns, and a *talent profile* is a
 * candidate's own profile that they have made discoverable. The pool is what
 * the candidates screens read; talent discovery is a separate search that
 * respects the candidate's visibility switch, which is why it has its own
 * permission and its own route.
 *
 * Expected salary, notice period and rating were local columns and have no
 * platform equivalent. They are returned as null rather than omitted so the
 * existing table and detail views render; the report lists them as gaps.
 */

function mapCandidate(candidate: PlatformPoolCandidate) {
  const [city = "", country = ""] = (candidate.location ?? "").split(",").map((p) => p.trim());

  return {
    id: candidate.id,
    orgId: candidate.companyId,
    fullName: candidate.fullName,
    email: candidate.email,
    phone: candidate.phone ?? "",
    city,
    country,
    currentDesignation: candidate.currentTitle ?? "",
    currentCompany: candidate.currentEmployer ?? "",
    totalExperienceYears: candidate.yearsExperience ?? null,
    totalExperienceText: candidate.yearsExperience ? `${candidate.yearsExperience} years` : "",
    expectedSalary: null as number | null,
    expectedSalaryText: "",
    noticePeriodDays: null as number | null,
    noticePeriodText: "",
    rating: null as string | null,
    skills: candidate.skills ?? [],
    tags: candidate.tags ?? [],
    headline: candidate.headline ?? "",
    source: candidate.source ?? "",
    // The object key, not a URL. A download URL is presigned on demand by
    // getCandidateResumeUrl so a link cannot outlive the permission check.
    resumeUrl: candidate.resumeKey ?? "",
    resumeFileName: "",
    portfolioUrl: "",
    linkedInUrl: "",
    coverLetter: "",
    notes: candidate.notes ?? "",
    // The pool *is* the talent pool now; there is no per-row flag.
    inTalentPool: true,
    createdAt: new Date(candidate.createdAt),
    updatedAt: new Date(candidate.updatedAt),
  };
}

export type CandidateRow = ReturnType<typeof mapCandidate>;

/* -------------------------------------------------------------------------- */
/* Queries                                                                    */
/* -------------------------------------------------------------------------- */

export async function getCandidates(params?: { search?: string; inTalentPool?: boolean }) {
  await requirePermission("candidates.read");

  const payload = await gatewayFetch<unknown>("/api/v1/candidates", {
    query: { limit: 100, ...(params?.search ? { q: params.search } : {}) },
  });

  return unwrap<PlatformPoolCandidate>(payload, "data").map(mapCandidate);
}

export async function getCandidateById(id: string) {
  const { access } = await requirePermission("candidates.read");

  let candidate: PlatformPoolCandidate;
  try {
    candidate = await gatewayFetch<PlatformPoolCandidate>(
      `/api/v1/candidates/${encodeURIComponent(id)}`,
    );
  } catch (error) {
    if (error instanceof ProblemError && error.status === 404) return null;
    throw error;
  }

  // The applications service keys on the candidate's identity account, not on a
  // pool record, so the two can only be joined when the pool row was linked to
  // a real profile. Matching on email is the honest approximation, and a
  // failure here leaves the history empty rather than losing the profile.
  const applications = access.can("applications.read")
    ? await gatewayRead(async () => {
        const payload = await gatewayFetch<unknown>("/api/v1/applications", {
          query: { limit: 100, q: candidate.email },
        });
        return unwrap<PlatformApplication>(payload, "applications").filter(
          (application) =>
            application.candidateEmail?.toLowerCase() === candidate.email.toLowerCase(),
        );
      }, [] as PlatformApplication[])
    : [];

  return {
    ...mapCandidate(candidate),
    applications: applications.map((application) => ({
      id: application.id,
      jobId: application.jobId,
      jobTitle: application.jobTitle,
      stage: application.stageType ?? "",
      stageName: application.stageName ?? "",
      status: application.status,
      createdAt: new Date(application.createdAt),
    })),
    // Neither service exists; the detail view renders these as empty sections.
    interviews: [] as unknown[],
    offers: [] as unknown[],
  };
}

/** A short-lived presigned download link, issued only after the permission check. */
export async function getCandidateResumeUrl(id: string) {
  await requirePermission("candidates.download_resume");
  return gatewayFetch<{ url: string; expiresAt?: string }>(
    `/api/v1/candidates/${encodeURIComponent(id)}/resume/download-url`,
  );
}

/* -------------------------------------------------------------------------- */
/* Talent discovery                                                           */
/* -------------------------------------------------------------------------- */

export async function searchTalent(params?: {
  q?: string;
  skills?: string[];
  location?: string;
  minYears?: number;
  remote?: boolean;
}) {
  await requirePermission("candidates.manage_talent_pool");

  const payload = await gatewayFetch<unknown>("/api/v1/talent/search", {
    query: {
      limit: 50,
      ...(params?.q ? { q: params.q } : {}),
      ...(params?.skills?.length ? { skills: params.skills.join(",") } : {}),
      ...(params?.location ? { location: params.location } : {}),
      ...(params?.minYears ? { minYears: params.minYears } : {}),
      ...(params?.remote ? { remote: "true" } : {}),
    },
  });

  return unwrap<PlatformTalentProfile>(payload, "data");
}

/** Opens a conversation with a discoverable candidate. */
export async function approachCandidate(candidateId: string, message: string, subject?: string) {
  const { user } = await requirePermission("candidates.manage_talent_pool");

  const result = await gatewayFetch<{ conversationId?: string }>(
    `/api/v1/talent/${encodeURIComponent(candidateId)}/approach`,
    { method: "POST", body: { message, ...(subject ? { subject } : {}) } },
  );

  await recordAuditLog({
    actorId: user.id,
    orgId: user.orgId,
    action: "talent.approached",
    entityType: "candidate",
    entityId: candidateId,
  });

  revalidatePath("/communications");
  return { success: true, conversationId: result?.conversationId };
}

/* -------------------------------------------------------------------------- */
/* Mutations                                                                  */
/* -------------------------------------------------------------------------- */

export interface CandidateInput {
  fullName: string;
  email: string;
  phone?: string;
  city?: string;
  country?: string;
  currentDesignation?: string;
  currentCompany?: string;
  totalExperienceYears?: number;
  headline?: string;
  skills?: string[];
  tags?: string[];
  source?: string;
  notes?: string;
  [key: string]: unknown;
}

function toPlatformBody(data: Partial<CandidateInput>): Record<string, unknown> {
  const location = [data.city, data.country].filter(Boolean).join(", ");
  const body: Record<string, unknown> = {};

  if (data.fullName !== undefined) body.fullName = data.fullName;
  if (data.email !== undefined) body.email = data.email;
  if (data.phone !== undefined) body.phone = data.phone ?? "";
  if (data.city !== undefined || data.country !== undefined) body.location = location;
  if (data.currentDesignation !== undefined) body.currentTitle = data.currentDesignation ?? "";
  if (data.currentCompany !== undefined) body.currentEmployer = data.currentCompany ?? "";
  if (data.totalExperienceYears !== undefined) {
    body.yearsExperience = data.totalExperienceYears ?? 0;
  }
  if (data.headline !== undefined) body.headline = data.headline ?? "";
  if (data.skills !== undefined) body.skills = data.skills ?? [];
  if (data.tags !== undefined) body.tags = data.tags ?? [];
  if (data.notes !== undefined) body.notes = data.notes ?? "";
  if (data.source !== undefined) body.source = data.source;

  return body;
}

export async function createCandidate(data: CandidateInput) {
  const { user } = await requirePermission("candidates.create");

  const candidate = await gatewayFetch<PlatformPoolCandidate>("/api/v1/candidates", {
    method: "POST",
    body: { source: "manual", ...toPlatformBody(data) },
  });

  await recordAuditLog({
    actorId: user.id,
    orgId: user.orgId,
    action: "candidates.created",
    entityType: "candidate",
    entityId: candidate.id,
    metadata: { email: data.email },
  });

  revalidatePath("/candidates");
  return { success: true, id: candidate.id };
}

export async function updateCandidate(id: string, data: Partial<CandidateInput>) {
  const { user } = await requirePermission("candidates.update");

  // The service replaces rather than merges on this route, so the current
  // record is read first and the change applied on top of it — a PUT that
  // carried only the edited fields would blank everything else.
  const current = await gatewayFetch<PlatformPoolCandidate>(
    `/api/v1/candidates/${encodeURIComponent(id)}`,
  );

  const merged: Record<string, unknown> = {
    fullName: current.fullName,
    email: current.email,
    phone: current.phone ?? "",
    headline: current.headline ?? "",
    location: current.location ?? "",
    currentTitle: current.currentTitle ?? "",
    currentEmployer: current.currentEmployer ?? "",
    yearsExperience: current.yearsExperience ?? 0,
    skills: current.skills ?? [],
    tags: current.tags ?? [],
    notes: current.notes ?? "",
    ...toPlatformBody(data),
  };

  await gatewayFetch<PlatformPoolCandidate>(`/api/v1/candidates/${encodeURIComponent(id)}`, {
    method: "PUT",
    body: merged,
  });

  await recordAuditLog({
    actorId: user.id,
    orgId: user.orgId,
    action: "candidates.updated",
    entityType: "candidate",
    entityId: id,
    metadata: { fields: Object.keys(data) },
  });

  revalidatePath("/candidates");
  revalidatePath(`/candidates/${id}`);
  return { success: true };
}

/**
 * The talent-pool flag.
 *
 * Every record in the candidates service already belongs to the company's pool
 * — the flag was a local column with nothing behind it on the platform. Kept as
 * a no-op success so the toggle does not throw, and reported as a gap.
 */
export async function toggleTalentPool(id: string, _inTalentPool: boolean) {
  await requirePermission("candidates.update");
  revalidatePath("/candidates");
  return { success: true, id };
}

export async function deleteCandidate(id: string) {
  const { user } = await requirePermission("candidates.delete");

  await gatewayFetch<void>(`/api/v1/candidates/${encodeURIComponent(id)}`, { method: "DELETE" });

  await recordAuditLog({
    actorId: user.id,
    orgId: user.orgId,
    action: "candidates.deleted",
    entityType: "candidate",
    entityId: id,
  });

  revalidatePath("/candidates");
  return { success: true };
}
