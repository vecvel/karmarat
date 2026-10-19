"use server";

import { revalidatePath } from "next/cache";

import { gatewayFetch, gatewayRead } from "@/lib/gateway/client";
import { unavailable } from "@/lib/gateway/unavailable";
import { requirePermission } from "@/lib/rbac/guard";

import type { SmtpConfig } from "./smtp";

export type { SmtpConfig } from "./smtp";

/** The company profile as the companies service returns it. */
interface PlatformCompanyProfile {
  id: string;
  slug: string;
  legalName?: string;
  displayName?: string;
  description?: string;
  logoKey?: string;
  website?: string;
  industry?: string;
  size?: string;
  foundedYear?: number;
  headquarters?: string;
  contactEmail?: string;
  contactPhone?: string;
  brandColor?: string;
  tagline?: string;
  timezone?: string;
  defaultCurrency?: string;
}

/**
 * The shape this app's settings screens were written against.
 *
 * Kept so the existing components render unchanged; the fields are filled from
 * the companies service rather than a local table.
 */
export interface OrganizationSettings {
  id: string;
  name: string;
  slug: string;
  careersDomain: string;
  defaultCurrency: string;
  timezone: string;
  logoUrl: string;
  description: string;
  website: string;
  industry: string;
}

function toSettings(profile: PlatformCompanyProfile): OrganizationSettings {
  return {
    id: profile.id,
    name: profile.displayName || profile.legalName || "",
    slug: profile.slug,
    // The careers portal is reached at {slug}.{hostname}; it is derived rather
    // than stored, so it can never drift from where the portal actually is.
    careersDomain: `${profile.slug}.${process.env.NEXT_PUBLIC_PLATFORM_HOSTNAME ?? "karmarat.local"}`,
    defaultCurrency: profile.defaultCurrency ?? "USD",
    timezone: profile.timezone ?? "UTC",
    logoUrl: profile.logoKey ?? "",
    description: profile.description ?? "",
    website: profile.website ?? "",
    industry: profile.industry ?? "",
  };
}

export async function getOrganizationSettings(): Promise<OrganizationSettings | null> {
  await requirePermission("organization.read");

  return gatewayRead(async () => {
    const profile = await gatewayFetch<PlatformCompanyProfile>("/api/v1/company/profile");
    return toSettings(profile);
  }, null);
}

export async function updateOrganizationSettings(data: {
  name?: string;
  careersDomain?: string;
  defaultCurrency?: string;
  timezone?: string;
  logoUrl?: string;
}) {
  await requirePermission("organization.update");

  // careersDomain is deliberately not forwarded: it is derived from the slug,
  // and letting this screen set it would produce a link that points somewhere
  // the gateway does not route.
  await gatewayFetch("/api/v1/company/profile", {
    method: "PATCH",
    body: {
      ...(data.name !== undefined ? { displayName: data.name } : {}),
      ...(data.defaultCurrency !== undefined ? { defaultCurrency: data.defaultCurrency } : {}),
      ...(data.timezone !== undefined ? { timezone: data.timezone } : {}),
      ...(data.logoUrl !== undefined ? { logoKey: data.logoUrl } : {}),
    },
  });

  revalidatePath("/settings");
  revalidatePath("/(app)", "layout");
  return { success: true };
}

/* -------------------------------------------------------------------------- */
/* Not yet served by the platform                                             */
/* -------------------------------------------------------------------------- */

export async function getIntegrationSettings() {
  await requirePermission("organization.read");
  return { unavailable: unavailable("integrations"), hrmWebhookUrl: "" };
}

export async function updateIntegrationSettings(_data: { hrmWebhookUrl?: string }) {
  await requirePermission("organization.update");
  const feature = unavailable("integrations");
  return { success: false as const, unavailable: feature, error: feature.blockedOn };
}

export async function getSmtpConfig(): Promise<SmtpConfig> {
  await requirePermission("organization.read");
  return {} as SmtpConfig;
}

export async function saveSmtpConfig(_config: Partial<SmtpConfig>) {
  await requirePermission("organization.update");
  const feature = unavailable("email-settings");
  return { success: false as const, unavailable: feature, error: feature.blockedOn };
}

export async function testSmtpConnection(
  _targetEmail?: string,
  _config?: Partial<SmtpConfig>,
): Promise<{ success: boolean; message: string; logs: string[]; error?: string }> {
  const feature = unavailable("email-settings");
  return {
    success: false,
    message: feature.blockedOn,
    // The screen renders these as a connection transcript; one honest line
    // beats an empty box that looks like the test silently did nothing.
    logs: [`${feature.title} is not available on the platform.`, feature.blockedOn],
    error: feature.blockedOn,
  };
}
