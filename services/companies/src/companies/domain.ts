/**
 * Company entities and the rules that do not need I/O.
 *
 * Everything here is pure so it can be tested without a database: slug
 * validation, the lifecycle enum, the upload allow-list, and — most importantly
 * — the projection that decides which fields a careers portal may expose to an
 * anonymous visitor.
 */
import { isReservedSlug, validationFailed } from '@karmarat/nestshared';

/**
 * Lifecycle states.
 *
 * Deliberately the same four values as `company_state` in the identity service:
 * the two are kept in step by events, and a state this service can reach but
 * identity cannot represent would silently stop propagating.
 */
export const CompanyState = {
  PendingReview: 'pending_review',
  Active: 'active',
  Suspended: 'suspended',
  Closed: 'closed',
} as const;

export type CompanyState = (typeof CompanyState)[keyof typeof CompanyState];

export const COMPANY_STATES: readonly CompanyState[] = Object.values(CompanyState);

export function isCompanyState(value: string): value is CompanyState {
  return (COMPANY_STATES as readonly string[]).includes(value);
}

/** Headcount bands. Stored as text with a check constraint rather than an enum
 * so adding a band later is a migration, not a type rewrite. */
export const COMPANY_SIZES = ['1-10', '11-50', '51-200', '201-500', '501-1000', '1001-5000', '5000+'] as const;
export type CompanySize = (typeof COMPANY_SIZES)[number];

export interface CompanyLocation {
  city: string;
  country: string;
  isHeadquarters?: boolean;
}

export interface SocialLinks {
  linkedin?: string;
  twitter?: string;
  facebook?: string;
  instagram?: string;
  github?: string;
  youtube?: string;
}

/** The full row. Only ever returned to the company itself or to platform staff. */
export interface Company {
  id: string;
  slug: string;
  legalName: string;
  displayName: string;
  state: CompanyState;

  description: string;
  logoKey: string;
  website: string;
  industry: string;
  size: string;
  foundedYear: number | null;
  headquarters: string;
  locations: CompanyLocation[];
  socialLinks: SocialLinks;
  contactEmail: string;
  contactPhone: string;
  country: string;

  // Careers portal presentation.
  brandColor: string;
  heroImageKey: string;
  tagline: string;
  aboutMarkdown: string;
  benefits: string[];
  customDomain: string;
  customDomainVerified: boolean;
  portalPublished: boolean;

  ownerEmail: string;
  ownerName: string;
  ownerAccountId: string;

  // Platform-side bookkeeping. Never leaves the platform endpoints.
  internalNotes: string;
  suspensionReason: string;
  approvedAt: Date | null;
  suspendedAt: Date | null;
  deletedAt: Date | null;

  createdAt: Date;
  updatedAt: Date;
}

/**
 * What an anonymous visitor to `{slug}.{hostname}` may see.
 *
 * This type is the enforcement, not a convenience: the public controller
 * returns this and nothing else, so adding a sensitive column to `Company`
 * cannot leak it by accident. Contact details, owner identity, internal notes
 * and lifecycle state are all absent on purpose — the first two are personal
 * data the company did not agree to publish, and the last two tell a stranger
 * how the platform runs its own review queue.
 */
export interface PublicCompany {
  slug: string;
  displayName: string;
  description: string;
  logoKey: string;
  website: string;
  industry: string;
  size: string;
  foundedYear: number | null;
  headquarters: string;
  locations: CompanyLocation[];
  socialLinks: SocialLinks;
  brand: {
    brandColor: string;
    heroImageKey: string;
    tagline: string;
    aboutMarkdown: string;
    benefits: string[];
  };
}

export function toPublicCompany(company: Company): PublicCompany {
  return {
    slug: company.slug,
    displayName: company.displayName,
    description: company.description,
    logoKey: company.logoKey,
    website: company.website,
    industry: company.industry,
    size: company.size,
    foundedYear: company.foundedYear,
    headquarters: company.headquarters,
    locations: company.locations,
    socialLinks: company.socialLinks,
    brand: {
      brandColor: company.brandColor,
      heroImageKey: company.heroImageKey,
      tagline: company.tagline,
      aboutMarkdown: company.aboutMarkdown,
      benefits: company.benefits,
    },
  };
}

/** The shape other services read over the internal API. */
export interface CompanySummary {
  id: string;
  slug: string;
  name: string;
  state: CompanyState;
  logoKey: string;
}

export function toSummary(company: Company): CompanySummary {
  return {
    id: company.id,
    slug: company.slug,
    name: company.displayName,
    state: company.state,
    logoKey: company.logoKey,
  };
}

/* -------------------------------------------------------------------------- */
/* Slugs                                                                      */
/* -------------------------------------------------------------------------- */

export const SLUG_MIN_LENGTH = 3;
export const SLUG_MAX_LENGTH = 40;

const SLUG_PATTERN = /^[a-z0-9]+(?:-[a-z0-9]+)*$/;

export type SlugRejection =
  | 'length'
  | 'charset'
  | 'hyphen_edge'
  | 'consecutive_hyphens'
  | 'reserved';

/**
 * Checks a slug against the rules the host router depends on.
 *
 * The slug becomes a hostname label, so it must be DNS-safe, and it must not
 * collide with a portal the platform already routes — `root`, `jobs`, `api` and
 * the rest. The reserved list comes from nestshared, which is generated from the
 * Go constant the gateway reads, so this service and the router cannot disagree
 * about what is claimable.
 *
 * Mirrors `provisioning.ValidateSlug` in the identity service; identity checks
 * again when it provisions, because this service is not the only caller.
 */
export function checkSlug(raw: string): SlugRejection | null {
  const slug = normaliseSlug(raw);

  if (slug.length < SLUG_MIN_LENGTH || slug.length > SLUG_MAX_LENGTH) {
    return 'length';
  }
  if (isReservedSlug(slug)) {
    return 'reserved';
  }
  if (slug.startsWith('-') || slug.endsWith('-')) {
    return 'hyphen_edge';
  }
  if (slug.includes('--')) {
    return 'consecutive_hyphens';
  }
  if (!SLUG_PATTERN.test(slug)) {
    return 'charset';
  }
  return null;
}

export function slugRejectionMessage(rejection: SlugRejection, slug: string): string {
  switch (rejection) {
    case 'length':
      return `A company address must be between ${SLUG_MIN_LENGTH} and ${SLUG_MAX_LENGTH} characters.`;
    case 'reserved':
      return `"${slug}" is reserved by the platform.`;
    case 'hyphen_edge':
      return 'A company address cannot start or end with a hyphen.';
    case 'consecutive_hyphens':
      return 'A company address cannot contain consecutive hyphens.';
    case 'charset':
      return 'A company address may contain only lowercase letters, numbers and hyphens.';
  }
}

export function normaliseSlug(raw: string): string {
  return raw.trim().toLowerCase();
}

/** Throws a 422 with a field map when the slug is unusable. */
export function assertValidSlug(raw: string): string {
  const slug = normaliseSlug(raw);
  const rejection = checkSlug(slug);
  if (rejection) {
    throw validationFailed({ slug: [slugRejectionMessage(rejection, slug)] });
  }
  return slug;
}

/* -------------------------------------------------------------------------- */
/* Uploads                                                                    */
/* -------------------------------------------------------------------------- */

/**
 * Image types a company asset may declare.
 *
 * An allow-list rather than a deny-list: the bucket serves company assets
 * publicly, so anything that a browser might execute in the bucket's origin —
 * SVG most of all — stays off it.
 */
export const ALLOWED_IMAGE_TYPES = ['image/png', 'image/jpeg', 'image/webp', 'image/gif'] as const;

/** 5 MB. A logo or hero image that exceeds this is a mistake, not a requirement. */
export const MAX_ASSET_BYTES = 5 * 1024 * 1024;

export type AssetKind = 'logo' | 'hero';

const EXTENSIONS: Record<string, string> = {
  'image/png': 'png',
  'image/jpeg': 'jpg',
  'image/webp': 'webp',
  'image/gif': 'gif',
};

export function isAllowedImageType(contentType: string): boolean {
  return (ALLOWED_IMAGE_TYPES as readonly string[]).includes(contentType.trim().toLowerCase());
}

/**
 * Builds the object key an asset is uploaded to.
 *
 * The key is derived from the verified tenant and never from the request, so a
 * company cannot be handed a signed URL that writes into another company's
 * prefix. The bucket policy enforces the same prefix, which is only possible
 * because the prefix is structural.
 */
export function assetObjectKey(companyId: string, kind: AssetKind, contentType: string, unique: string): string {
  const extension = EXTENSIONS[contentType.trim().toLowerCase()] ?? 'bin';
  return `company/${companyId}/${kind}/${unique}.${extension}`;
}

/**
 * Validates an upload request.
 *
 * Both checks happen before signing: the content type is a signed header, so
 * the browser must present exactly what was approved, and the size is refused
 * up front because a signature cannot express a byte ceiling.
 */
export function assertUploadAllowed(contentType: string, sizeBytes: number): string {
  const normalised = contentType.trim().toLowerCase();
  const fields: Record<string, string[]> = {};

  if (!isAllowedImageType(normalised)) {
    fields.contentType = [`Images must be one of: ${ALLOWED_IMAGE_TYPES.join(', ')}.`];
  }
  if (!Number.isInteger(sizeBytes) || sizeBytes <= 0) {
    fields.sizeBytes = ['A positive file size is required.'];
  } else if (sizeBytes > MAX_ASSET_BYTES) {
    fields.sizeBytes = [`Images must be ${MAX_ASSET_BYTES / (1024 * 1024)} MB or smaller.`];
  }

  if (Object.keys(fields).length > 0) {
    throw validationFailed(fields);
  }
  return normalised;
}
