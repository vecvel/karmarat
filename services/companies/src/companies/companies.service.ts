/**
 * Company registration, profile and lifecycle.
 *
 * The one thing worth reading closely is `register`: it is the write that turns
 * a visitor into a tenant, and it spans two services that cannot share a
 * transaction.
 */
import { Inject, Injectable, Logger } from '@nestjs/common';
import { randomUUID } from 'node:crypto';

import {
  Problem,
  Subject,
  conflict,
  notFound,
  parsePageRequest,
  type Page,
  type PageRequest,
} from '@karmarat/nestshared';

import type { CompaniesConfig } from '../config';
import { IdentityClient } from '../identity/identity.client';
import { Presigner, type PresignedUpload } from '../storage/presigner';
import {
  CompanyState,
  assertUploadAllowed,
  assertValidSlug,
  assetObjectKey,
  checkSlug,
  normaliseSlug,
  slugRejectionMessage,
  toPublicCompany,
  toSummary,
  type AssetKind,
  type Company,
  type CompanySummary,
  type PublicCompany,
} from './domain';
import { CompaniesRepository, type ListFilter, type PlatformPatch, type ProfilePatch } from './companies.repository';
import type { RegisterCompanyDto } from './dto/register.dto';
import type { UpdateProfileDto, UploadUrlDto } from './dto/profile.dto';
import type { ListCompaniesQueryDto, PlatformUpdateCompanyDto } from './dto/platform.dto';
import { toPlatformView, toProfileView, type CompanyProfileView, type PlatformCompanyView } from './views';

/** The slice of the event bus this service uses; narrowed so tests can fake it. */
export interface EventPublisher {
  publish(subject: string, payload: unknown, options?: { companyId?: string; actorId?: string; id?: string }): Promise<void>;
}

export const EVENT_PUBLISHER = Symbol('EVENT_PUBLISHER');
export const SERVICE_CONFIG = Symbol('SERVICE_CONFIG');

export interface RegistrationResult {
  companyId: string;
  slug: string;
  state: CompanyState;
  ownerAccountId: string;
  ownerCreated: boolean;
}

export interface SlugAvailability {
  slug: string;
  available: boolean;
  reason?: string;
}

@Injectable()
export class CompaniesService {
  private readonly logger = new Logger(CompaniesService.name);

  constructor(
    private readonly repository: CompaniesRepository,
    private readonly identity: IdentityClient,
    // Explicitly injected because the provider may resolve to null: a union
    // type emits no usable design:paramtypes metadata for Nest to resolve by.
    @Inject(Presigner) private readonly presigner: Presigner | null,
    @Inject(EVENT_PUBLISHER) private readonly events: EventPublisher,
    @Inject(SERVICE_CONFIG) private readonly config: CompaniesConfig,
  ) {}

  /* ---------------------------------------------------------------- register */

  /**
   * Registers a company and provisions its tenant.
   *
   * Ordering: local row first, identity second, compensating delete on failure.
   *
   * The slug race is the reason. Two visitors submitting "acme" at the same
   * instant both pass any pre-check; only the UNIQUE index can pick a winner,
   * so the insert has to happen before anything irreversible does. Asking
   * identity first would mean provisioning two tenants and then discovering
   * that one of them cannot have the address it was created for.
   *
   * The two writes cannot share a transaction — one is a network call — so the
   * flow is a saga with one compensation: if identity rejects the tenant, the
   * company row is deleted again. That is safe precisely because the row is
   * seconds old and nothing references it yet.
   *
   * The failure this cannot undo is a compensating delete that itself fails.
   * That leaves a `pending_review` row holding a slug with no tenant behind it;
   * it is logged with the company id and the slug so it can be cleared, and it
   * is strictly better than the opposite orphan — an identity tenant with
   * accounts and roles that no company record knows about.
   */
  async register(dto: RegisterCompanyDto): Promise<RegistrationResult> {
    const slug = assertValidSlug(dto.slug);

    // Minted here: identity's provisioning API expects the caller to supply the
    // id, so the company record and the tenant it becomes share one identifier
    // from the first write onwards.
    const companyId = randomUUID();

    const company = await this.repository.create({
      id: companyId,
      slug,
      legalName: dto.companyName,
      displayName: dto.companyName,
      state: CompanyState.PendingReview,
      industry: dto.industry,
      size: dto.size,
      country: dto.country,
      ownerEmail: dto.ownerEmail,
      ownerName: dto.ownerName,
    });

    let provisioned;
    try {
      provisioned = await this.identity.provisionCompany({
        companyId,
        slug,
        name: dto.companyName,
        ownerEmail: dto.ownerEmail,
        ownerName: dto.ownerName,
        ownerPassword: dto.ownerPassword,
        state: CompanyState.PendingReview,
      });
    } catch (error) {
      await this.compensate(companyId, slug);
      throw error;
    }

    await this.repository.setOwnerAccount(companyId, provisioned.ownerAccountId);

    // Deterministic id: a retry of this publish after a broker blip carries the
    // same id and JetStream delivers the fact once.
    await this.publish(Subject.CompanyRegistered, {
      companyId,
      slug,
      name: company.displayName,
      state: company.state,
      ownerAccountId: provisioned.ownerAccountId,
      ownerEmail: dto.ownerEmail,
    }, { companyId, actorId: provisioned.ownerAccountId, id: eventId(companyId, 'registered') });

    this.logger.log(`registered company ${slug} (${companyId})`);

    return {
      companyId,
      slug,
      state: company.state,
      ownerAccountId: provisioned.ownerAccountId,
      ownerCreated: provisioned.ownerCreated,
    };
  }

  /**
   * Reports whether a slug can be claimed, for live form feedback.
   *
   * An unusable slug and a taken slug are both reported as unavailable with a
   * reason, because the form has to say something either way. The endpoint is
   * rate limited at the controller: without that it enumerates every tenant on
   * the platform, and tenant names are not public.
   */
  async slugAvailability(raw: string): Promise<SlugAvailability> {
    const slug = normaliseSlug(raw);

    const rejection = checkSlug(slug);
    if (rejection) {
      return { slug, available: false, reason: slugRejectionMessage(rejection, slug) };
    }

    const taken = await this.repository.slugTaken(slug);
    return taken
      ? { slug, available: false, reason: 'That company address is already in use.' }
      : { slug, available: true };
  }

  /* ----------------------------------------------------------------- profile */

  async profile(companyId: string): Promise<CompanyProfileView> {
    return toProfileView(await this.require(companyId));
  }

  /**
   * Applies a company's changes to its own record.
   *
   * `companyId` comes from the verified principal; nothing in the body can name
   * a different tenant, and the UPDATE carries the id as a predicate rather
   * than loading the row and checking it afterwards.
   */
  async updateProfile(companyId: string, dto: UpdateProfileDto): Promise<CompanyProfileView> {
    const patch = this.toProfilePatch(companyId, dto);

    const updated = await this.repository.updateProfile(companyId, patch);
    if (!updated) {
      throw notFound('This company no longer exists.');
    }

    // Identity keeps a projection of the display name for its sign-in screens,
    // so a rename has to reach it. Only publish when it actually changed:
    // a consumer that receives an event per keystroke learns to ignore them.
    if (dto.displayName !== undefined) {
      await this.publish(Subject.CompanyUpdated, {
        companyId: updated.id,
        slug: updated.slug,
        name: updated.displayName,
        state: updated.state,
      }, { companyId: updated.id });
    }

    return toProfileView(updated);
  }

  /**
   * Signs an upload for a company asset.
   *
   * The key is built from the verified tenant and a fresh uuid, so a client
   * cannot choose where its bytes land, cannot overwrite another company's
   * object, and cannot overwrite its own previous logo — which means a
   * half-finished upload never corrupts the image a careers page is serving.
   */
  async uploadUrl(
    companyId: string,
    kind: AssetKind,
    dto: UploadUrlDto,
  ): Promise<PresignedUpload & { key: string }> {
    if (!this.presigner) {
      throw new Problem(
        503,
        'uploads_unavailable',
        'Service Unavailable',
        'Image uploads are not available right now.',
      );
    }

    const contentType = assertUploadAllowed(dto.contentType, dto.sizeBytes);
    const key = assetObjectKey(companyId, kind, contentType, randomUUID());

    const signed = this.presigner.presignPut(key, contentType, this.config.storage.uploadTtlSeconds);
    return { ...signed, key };
  }

  /* ------------------------------------------------------------------ public */

  /**
   * Resolves the careers portal for a slug.
   *
   * 404 covers three different situations — no such company, not yet approved,
   * portal not published — on purpose. A visitor has no business learning that
   * a company registered and is awaiting review, and distinguishing the cases
   * would turn this endpoint into a registration oracle.
   */
  async publicProfile(slug: string): Promise<PublicCompany> {
    const company = await this.repository.findBySlug(normaliseSlug(slug));

    if (!company || company.state !== CompanyState.Active || !company.portalPublished) {
      throw notFound('This careers page is not available.');
    }

    return toPublicCompany(company);
  }

  /* ---------------------------------------------------------------- platform */

  async list(query: ListCompaniesQueryDto): Promise<Page<PlatformCompanyView>> {
    const page: PageRequest = parsePageRequest({ limit: query.limit, cursor: query.cursor });

    const filter: ListFilter = {
      includeDeleted: query.includeDeleted === 'true',
      ...(query.state ? { state: query.state as CompanyState } : {}),
      ...(query.search ? { search: query.search } : {}),
    };

    const result = await this.repository.list(filter, page);
    return { items: result.items.map(toPlatformView), nextCursor: result.nextCursor };
  }

  async platformGet(companyId: string): Promise<PlatformCompanyView> {
    const company = await this.repository.findById(companyId, { includeDeleted: true });
    if (!company) {
      throw notFound('No such company.');
    }
    return toPlatformView(company);
  }

  /**
   * Admits a company.
   *
   * The event is what actually opens the portal: identity's projection consumes
   * `company.approved` and flips its own copy of the state, and until it does
   * no owner can sign in. Publishing after the write, not before, means a
   * consumer never sees a state this service has not committed.
   */
  async approve(companyId: string): Promise<PlatformCompanyView> {
    const company = await this.repository.findById(companyId);
    if (!company) {
      throw notFound('No such company.');
    }
    if (company.state === CompanyState.Active) {
      // Idempotent rather than a conflict: a double-click in the console should
      // not read as an error.
      return toPlatformView(company);
    }
    if (company.state === CompanyState.Closed) {
      throw conflict('company_closed', 'A closed company cannot be approved.');
    }

    const updated = await this.repository.setState(companyId, CompanyState.Active);
    if (!updated) {
      throw notFound('No such company.');
    }

    await this.publishStateChange(Subject.CompanyApproved, updated);
    return toPlatformView(updated);
  }

  /** Suspends a company. The reason is stored and shown to the company. */
  async suspend(companyId: string, reason: string): Promise<PlatformCompanyView> {
    const updated = await this.repository.setState(companyId, CompanyState.Suspended, reason);
    if (!updated) {
      throw notFound('No such company.');
    }

    await this.publishStateChange(Subject.CompanySuspended, updated, { reason });
    return toPlatformView(updated);
  }

  async platformUpdate(companyId: string, dto: PlatformUpdateCompanyDto): Promise<PlatformCompanyView> {
    const { internalNotes, ...profile } = dto;

    const patch: PlatformPatch = {
      ...this.toProfilePatch(companyId, profile),
      ...(internalNotes !== undefined ? { internalNotes } : {}),
    };

    const updated = await this.repository.updateAsPlatform(companyId, patch);
    if (!updated) {
      throw notFound('No such company.');
    }

    if (dto.displayName !== undefined) {
      await this.publish(Subject.CompanyUpdated, {
        companyId: updated.id,
        slug: updated.slug,
        name: updated.displayName,
        state: updated.state,
      }, { companyId: updated.id });
    }

    return toPlatformView(updated);
  }

  /**
   * Soft-deletes a company.
   *
   * `company.suspended` is published rather than a delete event: nothing else on
   * the platform consumes a company deletion, and what every other service
   * needs to know is the same thing suspension tells it — stop serving this
   * tenant. The slug stays claimed; see the migration for why.
   */
  async softDelete(companyId: string): Promise<void> {
    const deleted = await this.repository.softDelete(companyId);
    if (!deleted) {
      throw notFound('No such company.');
    }

    await this.publishStateChange(Subject.CompanySuspended, deleted, { reason: 'Company closed by the platform.' });
  }

  /* ---------------------------------------------------------------- internal */

  async summaryById(companyId: string): Promise<CompanySummary> {
    return toSummary(await this.require(companyId));
  }

  async summaryBySlug(slug: string): Promise<CompanySummary> {
    const company = await this.repository.findBySlug(normaliseSlug(slug));
    if (!company) {
      throw notFound('No such company.');
    }
    return toSummary(company);
  }

  /* ----------------------------------------------------------------- helpers */

  private async require(companyId: string): Promise<Company> {
    const company = await this.repository.findById(companyId);
    if (!company) {
      throw notFound('No such company.');
    }
    return company;
  }

  /**
   * Converts a validated DTO into a column patch.
   *
   * Asset keys are checked against this tenant's prefix here rather than in the
   * DTO: the rule needs the company id, and a client that could store an
   * arbitrary key could probe for another tenant's objects by rendering them.
   */
  private toProfilePatch(companyId: string, dto: UpdateProfileDto): ProfilePatch {
    const patch: ProfilePatch = { ...dto } as ProfilePatch;

    for (const field of ['logoKey', 'heroImageKey'] as const) {
      const value = dto[field];
      if (value !== undefined && value !== '' && !value.startsWith(`company/${companyId}/`)) {
        throw conflict('invalid_asset_key', 'That asset does not belong to this company.');
      }
    }

    // A custom domain that changes has to be re-verified — and since nothing
    // verifies it yet, it simply cannot be marked verified here.
    return patch;
  }

  private async compensate(companyId: string, slug: string): Promise<void> {
    try {
      await this.repository.hardDelete(companyId);
    } catch (error) {
      this.logger.error(
        `failed to roll back company ${companyId} (${slug}) after identity rejected provisioning: ` +
          `${(error as Error).message}. The slug is held by a row with no tenant behind it.`,
      );
    }
  }

  private async publishStateChange(
    subject: string,
    company: Company,
    extra: Record<string, unknown> = {},
  ): Promise<void> {
    await this.publish(subject, {
      companyId: company.id,
      slug: company.slug,
      name: company.displayName,
      state: company.state,
      ...extra,
    }, { companyId: company.id });
  }

  /**
   * Publishes without letting a broker outage fail the request.
   *
   * The write has already committed; throwing now would tell the caller their
   * change failed when it did not. The consequence — a projection that lags
   * until the next state change — is logged loudly because it is real, and it
   * is still the lesser failure.
   */
  private async publish(
    subject: string,
    payload: unknown,
    options: { companyId?: string; actorId?: string; id?: string } = {},
  ): Promise<void> {
    try {
      await this.events.publish(subject, payload, options);
    } catch (error) {
      this.logger.error(`failed to publish ${subject}: ${(error as Error).message}`);
    }
  }
}

/** Deterministic event id, so a republish of the same fact de-duplicates. */
function eventId(companyId: string, suffix: string): string {
  return `evt_${companyId.replace(/-/g, '')}_${suffix}`;
}
