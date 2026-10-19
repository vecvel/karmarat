"use server";

import { ProblemError } from "@karmarat/ui";

import { PLATFORM_HOSTNAME } from "@/lib/gateway/config";

/**
 * Applying from a company's careers page.
 *
 * This used to accept an anonymous submission and write a candidate row
 * directly. The platform does not work that way, and for a defensible reason:
 * an application belongs to a candidate's account, so the candidate can see it,
 * withdraw it and be messaged about it from their own portal. One application
 * per candidate per job is enforceable only because there is an account to key
 * it on.
 *
 * `POST /api/v1/public/apply` is therefore public by *route* but requires a
 * candidate principal — an unauthenticated POST is answered 401. There is no
 * endpoint that accepts an anonymous application, and inventing one here would
 * mean writing a record no service owns.
 *
 * So the careers page collects nothing and sends the visitor to the candidate
 * portal to sign in or register, carrying the job they were looking at.
 */

/** Where a visitor goes to apply: the candidate portal, on this job. */
export async function applyUrlForJob(jobSlug: string): Promise<string> {
  const base = process.env.JOBS_PORTAL_URL || `https://jobs.${PLATFORM_HOSTNAME}`;
  return `${base.replace(/\/+$/, "")}/jobs/${encodeURIComponent(jobSlug)}/apply`;
}

export interface PortalApplicationInput {
  jobId: string;
  fullName: string;
  email: string;
  phone?: string;
  city?: string;
  country?: string;
  currentDesignation?: string;
  currentCompany?: string;
  totalExperienceYears?: number;
  totalExperienceText?: string;
  expectedSalary?: number;
  expectedSalaryText?: string;
  noticePeriodDays?: number;
  noticePeriodText?: string;
  skills?: string[];
  resumeUrl?: string;
  resumeFileName?: string;
  portfolioUrl?: string;
  linkedInUrl?: string;
  coverLetter?: string;
  answers?: Record<string, unknown>;
}

export async function submitApplicationFromPortal(_data: PortalApplicationInput) {
  throw new ProblemError({
    type: "about:blank",
    title: "Sign-in required",
    status: 401,
    detail:
      "Applications are made from a Karmarat candidate account, so that you can track and withdraw them. Sign in or create an account on the jobs portal to apply.",
    code: "candidate_account_required",
  });
}
