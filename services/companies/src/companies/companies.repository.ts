/**
 * SQL for the companies aggregate.
 *
 * Two rules hold everywhere in this file:
 *
 *  1. A company-scoped statement names the tenant in its WHERE clause. The
 *     tenant key of this table is its primary key, so `WHERE id = $1` *is* the
 *     tenant filter — but it is always written from the verified principal's
 *     company id, never from a path parameter.
 *  2. A soft-deleted row is invisible to everything except the platform
 *     endpoints that explicitly ask for it. `deleted_at IS NULL` is part of the
 *     predicate rather than something a caller remembers to add.
 */
import { Inject, Injectable } from '@nestjs/common';
import type { Pool, PoolClient } from 'pg';

import { buildPage, conflict, type Page, type PageRequest } from '@karmarat/nestshared';

import {
  type Company,
  type CompanyLocation,
  type CompanyState,
  type SocialLinks,
} from './domain';

export const PG_POOL = Symbol('PG_POOL');

/** Columns a company may change about itself. */
export interface ProfilePatch {
  legalName?: string;
  displayName?: string;
  description?: string;
  logoKey?: string;
  website?: string;
  industry?: string;
  size?: string;
  foundedYear?: number | null;
  headquarters?: string;
  locations?: CompanyLocation[];
  socialLinks?: SocialLinks;
  contactEmail?: string;
  contactPhone?: string;
  brandColor?: string;
  heroImageKey?: string;
  tagline?: string;
  aboutMarkdown?: string;
  benefits?: string[];
  customDomain?: string;
  portalPublished?: boolean;
}

/**
 * Columns only platform staff may change.
 *
 * Deliberately no `state`. The lifecycle moves through `setState`, which is
 * reached only by approve, suspend and delete — and each of those publishes the
 * event that the identity projection and every other service depend on. A state
 * writable through the generic patch path would change the row and tell nobody.
 */
export interface PlatformPatch extends ProfilePatch {
  internalNotes?: string;
}

export interface CreateCompanyInput {
  id: string;
  slug: string;
  legalName: string;
  displayName: string;
  state: CompanyState;
  industry: string;
  size: string;
  country: string;
  ownerEmail: string;
  ownerName: string;
}

export interface ListFilter {
  state?: CompanyState;
  /** Case-insensitive prefix match on slug or display name. */
  search?: string;
  includeDeleted?: boolean;
}

const COLUMNS = `
  id, slug, legal_name, display_name, state,
  description, logo_key, website, industry, size, founded_year, headquarters, country,
  locations, social_links, contact_email, contact_phone,
  brand_color, hero_image_key, tagline, about_markdown, benefits,
  custom_domain, custom_domain_verified, portal_published,
  owner_email, owner_name, owner_account_id,
  internal_notes, suspension_reason, approved_at, suspended_at, deleted_at,
  created_at, updated_at`;

/**
 * Maps a patch field onto its column.
 *
 * An explicit map, not a camelCase-to-snake_case conversion: a derived column
 * name means any property that reaches the patch object becomes writable, which
 * is how `state` or `deleted_at` ends up updatable from a profile request.
 */
const PROFILE_COLUMNS: Record<keyof ProfilePatch, string> = {
  legalName: 'legal_name',
  displayName: 'display_name',
  description: 'description',
  logoKey: 'logo_key',
  website: 'website',
  industry: 'industry',
  size: 'size',
  foundedYear: 'founded_year',
  headquarters: 'headquarters',
  locations: 'locations',
  socialLinks: 'social_links',
  contactEmail: 'contact_email',
  contactPhone: 'contact_phone',
  brandColor: 'brand_color',
  heroImageKey: 'hero_image_key',
  tagline: 'tagline',
  aboutMarkdown: 'about_markdown',
  benefits: 'benefits',
  customDomain: 'custom_domain',
  portalPublished: 'portal_published',
};

const PLATFORM_COLUMNS: Record<string, string> = {
  ...PROFILE_COLUMNS,
  internalNotes: 'internal_notes',
};

/** Columns whose value must be handed to pg as JSON rather than as an array. */
const JSON_COLUMNS = new Set(['locations', 'social_links', 'benefits']);

@Injectable()
export class CompaniesRepository {
  constructor(@Inject(PG_POOL) private readonly pool: Pool) {}

  async create(input: CreateCompanyInput, client?: PoolClient): Promise<Company> {
    const executor = client ?? this.pool;

    try {
      const { rows } = await executor.query(
        `INSERT INTO companies (
           id, slug, legal_name, display_name, state, industry, size, country, owner_email, owner_name
         ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
         RETURNING ${COLUMNS}`,
        [
          input.id,
          input.slug,
          input.legalName,
          input.displayName,
          input.state,
          input.industry,
          input.size,
          input.country,
          input.ownerEmail,
          input.ownerName,
        ],
      );
      return mapCompany(rows[0]);
    } catch (error) {
      // 23505 is unique_violation: the slug index decided a race that no
      // pre-check could have decided.
      if ((error as { code?: string }).code === '23505') {
        throw conflict('slug_taken', 'That company address is already in use.');
      }
      throw error;
    }
  }

  /**
   * Records which identity account owns this company.
   *
   * A second statement rather than a column on the INSERT: the account does not
   * exist until identity has provisioned it, and identity cannot be called
   * before the slug race has been decided by the unique index. A failure here
   * is not worth undoing a successful registration — the id is bookkeeping for
   * support, and identity remains the authority on who owns the tenant.
   */
  async setOwnerAccount(companyId: string, ownerAccountId: string): Promise<void> {
    await this.pool.query('UPDATE companies SET owner_account_id = $2 WHERE id = $1', [
      companyId,
      ownerAccountId,
    ]);
  }

  /** Removes a row outright. Used only to compensate a failed registration. */
  async hardDelete(id: string): Promise<void> {
    await this.pool.query('DELETE FROM companies WHERE id = $1', [id]);
  }

  async findById(id: string, options: { includeDeleted?: boolean } = {}): Promise<Company | null> {
    const { rows } = await this.pool.query(
      `SELECT ${COLUMNS} FROM companies
        WHERE id = $1 ${options.includeDeleted ? '' : 'AND deleted_at IS NULL'}`,
      [id],
    );
    return rows[0] ? mapCompany(rows[0]) : null;
  }

  async findBySlug(slug: string): Promise<Company | null> {
    const { rows } = await this.pool.query(
      `SELECT ${COLUMNS} FROM companies WHERE slug = $1 AND deleted_at IS NULL`,
      [slug],
    );
    return rows[0] ? mapCompany(rows[0]) : null;
  }

  /**
   * Reports whether a slug is already claimed.
   *
   * Deliberately counts soft-deleted rows too: the unique index does, so a
   * "yes, available" answer that the INSERT then refuses would be a worse
   * experience than the honest one.
   */
  async slugTaken(slug: string): Promise<boolean> {
    const { rowCount } = await this.pool.query('SELECT 1 FROM companies WHERE slug = $1', [slug]);
    return (rowCount ?? 0) > 0;
  }

  /** Applies a company's own changes. Returns null when the tenant is gone. */
  async updateProfile(companyId: string, patch: ProfilePatch): Promise<Company | null> {
    return this.update(companyId, patch, PROFILE_COLUMNS);
  }

  /** Applies a platform administrator's changes, including state. */
  async updateAsPlatform(companyId: string, patch: PlatformPatch): Promise<Company | null> {
    return this.update(companyId, patch, PLATFORM_COLUMNS);
  }

  /**
   * Moves a company through its lifecycle.
   *
   * The timestamps and the reason move with the state in one statement, so a
   * suspended company always carries the reason it was suspended.
   */
  async setState(companyId: string, state: CompanyState, reason = ''): Promise<Company | null> {
    const { rows } = await this.pool.query(
      // The casts are load-bearing. $2 is assigned to a company_state column and
      // also compared against string literals; without them Postgres deduces two
      // different types for one parameter and refuses the statement outright.
      `UPDATE companies
          SET state = $2::company_state,
              suspension_reason = CASE WHEN $2::text = 'suspended' THEN $3 ELSE '' END,
              suspended_at      = CASE WHEN $2::text = 'suspended' THEN now() ELSE NULL END,
              approved_at       = CASE WHEN $2::text = 'active' THEN COALESCE(approved_at, now()) ELSE approved_at END
        WHERE id = $1 AND deleted_at IS NULL
        RETURNING ${COLUMNS}`,
      [companyId, state, reason],
    );
    return rows[0] ? mapCompany(rows[0]) : null;
  }

  /**
   * Hides a company.
   *
   * Soft, because other services hold rows that reference this tenant; a hard
   * delete would leave jobs and applications pointing at nothing. The state
   * moves to `closed` in the same statement so the identity projection, which
   * knows nothing about `deleted_at`, still stops admitting sign-ins.
   */
  async softDelete(companyId: string): Promise<Company | null> {
    const { rows } = await this.pool.query(
      `UPDATE companies
          SET deleted_at = now(), state = 'closed', portal_published = false
        WHERE id = $1 AND deleted_at IS NULL
        RETURNING ${COLUMNS}`,
      [companyId],
    );
    return rows[0] ? mapCompany(rows[0]) : null;
  }

  /**
   * Lists companies for the platform console.
   *
   * Keyset pagination over (created_at, id): the same sort the cursor encodes,
   * so a company registering mid-scan cannot shift a page under the reader.
   */
  async list(filter: ListFilter, page: PageRequest): Promise<Page<Company>> {
    const conditions: string[] = [];
    const values: unknown[] = [];

    if (!filter.includeDeleted) {
      conditions.push('deleted_at IS NULL');
    }
    if (filter.state) {
      values.push(filter.state);
      conditions.push(`state = $${values.length}`);
    }
    if (filter.search) {
      values.push(`${filter.search.toLowerCase()}%`);
      conditions.push(`(slug LIKE $${values.length} OR lower(display_name) LIKE $${values.length})`);
    }
    if (page.cursor) {
      values.push(page.cursor.createdAt, page.cursor.id);
      conditions.push(`(created_at, id) < ($${values.length - 1}::timestamptz, $${values.length}::uuid)`);
    }

    // One row beyond the page tells us whether a next page exists without a
    // second count query.
    values.push(page.limit + 1);

    const { rows } = await this.pool.query(
      `SELECT ${COLUMNS} FROM companies
        ${conditions.length ? `WHERE ${conditions.join(' AND ')}` : ''}
        ORDER BY created_at DESC, id DESC
        LIMIT $${values.length}`,
      values,
    );

    return buildPage(rows.map(mapCompany), page.limit);
  }

  private async update(
    companyId: string,
    patch: PlatformPatch,
    allowed: Record<string, string>,
  ): Promise<Company | null> {
    const assignments: string[] = [];
    const values: unknown[] = [companyId];

    // The cast is to iterate, not to widen what may be written: `allowed` is
    // the gate, and it is a fixed map rather than anything derived from the
    // patch's own keys.
    for (const [field, value] of Object.entries(patch as Record<string, unknown>)) {
      const column = allowed[field];
      // An unknown field is dropped rather than rejected: the DTO layer has
      // already refused anything unrecognised, so reaching here means a column
      // this role may not write.
      if (column === undefined || value === undefined) {
        continue;
      }
      values.push(JSON_COLUMNS.has(column) ? JSON.stringify(value) : value);
      assignments.push(`${column} = $${values.length}${JSON_COLUMNS.has(column) ? '::jsonb' : ''}`);
    }

    if (assignments.length === 0) {
      return this.findById(companyId);
    }

    const { rows } = await this.pool.query(
      `UPDATE companies SET ${assignments.join(', ')}
        WHERE id = $1 AND deleted_at IS NULL
        RETURNING ${COLUMNS}`,
      values,
    );
    return rows[0] ? mapCompany(rows[0]) : null;
  }
}

function mapCompany(row: Record<string, unknown>): Company {
  return {
    id: row.id as string,
    slug: row.slug as string,
    legalName: row.legal_name as string,
    displayName: row.display_name as string,
    state: row.state as CompanyState,
    description: row.description as string,
    logoKey: row.logo_key as string,
    website: row.website as string,
    industry: row.industry as string,
    size: row.size as string,
    foundedYear: (row.founded_year as number | null) ?? null,
    headquarters: row.headquarters as string,
    country: row.country as string,
    locations: (row.locations as CompanyLocation[] | null) ?? [],
    socialLinks: (row.social_links as SocialLinks | null) ?? {},
    contactEmail: row.contact_email as string,
    contactPhone: row.contact_phone as string,
    brandColor: row.brand_color as string,
    heroImageKey: row.hero_image_key as string,
    tagline: row.tagline as string,
    aboutMarkdown: row.about_markdown as string,
    benefits: (row.benefits as string[] | null) ?? [],
    customDomain: row.custom_domain as string,
    customDomainVerified: row.custom_domain_verified as boolean,
    portalPublished: row.portal_published as boolean,
    ownerEmail: row.owner_email as string,
    ownerName: row.owner_name as string,
    ownerAccountId: row.owner_account_id as string,
    internalNotes: row.internal_notes as string,
    suspensionReason: row.suspension_reason as string,
    approvedAt: (row.approved_at as Date | null) ?? null,
    suspendedAt: (row.suspended_at as Date | null) ?? null,
    deletedAt: (row.deleted_at as Date | null) ?? null,
    createdAt: row.created_at as Date,
    updatedAt: row.updated_at as Date,
  };
}
