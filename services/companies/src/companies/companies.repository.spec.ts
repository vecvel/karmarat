/**
 * The tenant boundary, proved against a real database.
 *
 * This is the test the contract says must exist even when time is short,
 * because the regression it catches is a breach rather than a bug: two
 * companies are seeded, every operation is performed as one of them, and the
 * other's row is asserted to be untouched and invisible.
 *
 * It needs Postgres, so it skips cleanly when TEST_DATABASE_URL is unset —
 * `pnpm test` stays green on a laptop with no database, and CI still runs it:
 *
 *   TEST_DATABASE_URL=postgres://karmarat:karmarat@localhost:5432/companies_test pnpm test
 */
import { createPool, migrate } from '@karmarat/nestshared';
import { randomUUID } from 'node:crypto';
import { join } from 'node:path';
import type { Pool } from 'pg';

import { CompaniesRepository, type CreateCompanyInput } from './companies.repository';
import { CompanyState } from './domain';

const DATABASE_URL = process.env.TEST_DATABASE_URL ?? '';

// `describe.skip` rather than an early return, so the run reports the suite as
// skipped instead of silently passing an empty file.
const describeWithDatabase = DATABASE_URL ? describe : describe.skip;

describeWithDatabase('CompaniesRepository (postgres)', () => {
  let pool: Pool;
  let repository: CompaniesRepository;

  // Every row this suite writes carries the same marker, so cleanup cannot
  // delete a row that belongs to something else sharing the database.
  const run = randomUUID().slice(0, 8);
  const created: string[] = [];

  const seed = async (overrides: Partial<CreateCompanyInput> = {}) => {
    const input: CreateCompanyInput = {
      id: randomUUID(),
      slug: `t-${run}-${randomUUID().slice(0, 8)}`,
      legalName: 'Tenant Ltd',
      displayName: 'Tenant',
      state: CompanyState.PendingReview,
      industry: 'Testing',
      size: '1-10',
      country: 'Ireland',
      ownerEmail: `owner-${randomUUID().slice(0, 8)}@test.invalid`,
      ownerName: 'Owner',
      ...overrides,
    };
    created.push(input.id);
    return repository.create(input);
  };

  beforeAll(async () => {
    pool = createPool({ url: DATABASE_URL });
    await migrate(pool, join(__dirname, '..', '..', 'migrations'), { log: () => undefined });
    repository = new CompaniesRepository(pool);
  });

  afterAll(async () => {
    if (created.length > 0) {
      await pool.query('DELETE FROM companies WHERE id = ANY($1::uuid[])', [created]);
    }
    await pool.end();
  });

  /* ------------------------------------------------------------- isolation */

  describe('the tenant boundary', () => {
    it('never lets one company\'s update reach another\'s row', async () => {
      const a = await seed({ displayName: 'Alpha' });
      const b = await seed({ displayName: 'Beta' });

      await repository.updateProfile(a.id, { displayName: 'Alpha Renamed', tagline: 'ours' });

      const untouched = await repository.findById(b.id);
      expect(untouched?.displayName).toBe('Beta');
      expect(untouched?.tagline).toBe('');
    });

    it('never lets one company\'s state change reach another\'s row', async () => {
      const a = await seed();
      const b = await seed();

      await repository.setState(a.id, CompanyState.Suspended, 'Payment dispute');

      expect((await repository.findById(b.id))?.state).toBe(CompanyState.PendingReview);
      expect((await repository.findById(b.id))?.suspensionReason).toBe('');
    });

    it('never lets one company\'s deletion reach another\'s row', async () => {
      const a = await seed();
      const b = await seed();

      await repository.softDelete(a.id);

      expect(await repository.findById(a.id)).toBeNull();
      expect(await repository.findById(b.id)).not.toBeNull();
    });

    it('returns null for an id that is not a company, rather than anything else', async () => {
      await seed();

      expect(await repository.findById(randomUUID())).toBeNull();
    });
  });

  /* ------------------------------------------------------------------ slug */

  describe('slug uniqueness', () => {
    it('is decided by the constraint, not by a pre-check', async () => {
      const first = await seed();

      // The second insert reaches the database with a slug that a SELECT would
      // also have called free a microsecond earlier; only the unique index can
      // settle it.
      await expect(seed({ slug: first.slug })).rejects.toMatchObject({ code: 'slug_taken' });
    });

    it('keeps a soft-deleted company\'s slug claimed, because a slug is a hostname', async () => {
      const company = await seed();
      await repository.softDelete(company.id);

      expect(await repository.slugTaken(company.slug)).toBe(true);
      // ...while the company itself is gone from every lookup that serves it.
      expect(await repository.findBySlug(company.slug)).toBeNull();
    });

    it('reports an unclaimed slug as free', async () => {
      expect(await repository.slugTaken(`never-${run}`)).toBe(false);
    });
  });

  /* --------------------------------------------------------------- columns */

  describe('what a patch may write', () => {
    it('applies the profile fields a company owns', async () => {
      const company = await seed();

      const updated = await repository.updateProfile(company.id, {
        description: 'We make things.',
        foundedYear: 1998,
        locations: [{ city: 'Dublin', country: 'IE' }],
        socialLinks: { linkedin: 'https://linkedin.test/acme' },
        benefits: ['Remote', 'Learning budget'],
        portalPublished: true,
      });

      expect(updated).toMatchObject({
        description: 'We make things.',
        foundedYear: 1998,
        benefits: ['Remote', 'Learning budget'],
        portalPublished: true,
      });
      expect(updated?.locations[0]).toEqual({ city: 'Dublin', country: 'IE' });
      expect(updated?.socialLinks.linkedin).toBe('https://linkedin.test/acme');
    });

    /**
     * The column map is the gate, not the patch object's keys.
     *
     * A derived snake_case conversion would make any property that reached the
     * patch writable — which is exactly how `state` or `deleted_at` becomes
     * settable from a profile request.
     */
    it('drops a field the role may not write instead of writing it', async () => {
      const company = await seed();

      const updated = await repository.updateProfile(company.id, {
        ...({ state: CompanyState.Active, internalNotes: 'sneaked in' } as object),
        tagline: 'legitimate',
      });

      expect(updated?.state).toBe(CompanyState.PendingReview);
      expect(updated?.internalNotes).toBe('');
      expect(updated?.tagline).toBe('legitimate');
    });

    it('lets platform staff write the notes a company cannot', async () => {
      const company = await seed();

      const updated = await repository.updateAsPlatform(company.id, { internalNotes: 'Called the owner.' });

      expect(updated?.internalNotes).toBe('Called the owner.');
    });

    it('refuses to move the lifecycle through the patch path at all', async () => {
      const company = await seed();

      const updated = await repository.updateAsPlatform(company.id, {
        ...({ state: CompanyState.Active } as object),
      });

      // Approve and suspend publish events; a state written here would change
      // the row and tell no one.
      expect(updated?.state).toBe(CompanyState.PendingReview);
    });

    it('returns the row unchanged when the patch is empty', async () => {
      const company = await seed();

      expect((await repository.updateProfile(company.id, {}))?.id).toBe(company.id);
    });

    it('touches updated_at on every write', async () => {
      const company = await seed();

      const updated = await repository.updateProfile(company.id, { tagline: 'changed' });

      expect(updated!.updatedAt.getTime()).toBeGreaterThanOrEqual(company.updatedAt.getTime());
    });
  });

  /* ------------------------------------------------------------- lifecycle */

  describe('lifecycle', () => {
    it('records when a company was approved and keeps the first timestamp', async () => {
      const company = await seed();

      const approved = await repository.setState(company.id, CompanyState.Active);
      expect(approved?.state).toBe(CompanyState.Active);
      expect(approved?.approvedAt).not.toBeNull();

      const reapproved = await repository.setState(company.id, CompanyState.Active);
      expect(reapproved?.approvedAt?.getTime()).toBe(approved?.approvedAt?.getTime());
    });

    it('stores the reason with the suspension and clears it on the way back', async () => {
      const company = await seed();

      const suspended = await repository.setState(company.id, CompanyState.Suspended, 'Payment dispute');
      expect(suspended?.suspensionReason).toBe('Payment dispute');
      expect(suspended?.suspendedAt).not.toBeNull();

      const restored = await repository.setState(company.id, CompanyState.Active);
      expect(restored?.suspensionReason).toBe('');
      expect(restored?.suspendedAt).toBeNull();
    });

    it('closes and unpublishes in the same statement as the soft delete', async () => {
      const company = await seed();
      await repository.updateProfile(company.id, { portalPublished: true });

      const deleted = await repository.softDelete(company.id);

      // Identity's projection knows nothing about deleted_at, so the state has
      // to carry the fact for it.
      expect(deleted?.state).toBe(CompanyState.Closed);
      expect(deleted?.portalPublished).toBe(false);
    });

    it('will not delete the same company twice', async () => {
      const company = await seed();

      expect(await repository.softDelete(company.id)).not.toBeNull();
      expect(await repository.softDelete(company.id)).toBeNull();
    });

    it('shows platform staff a closed account when they ask for it', async () => {
      const company = await seed();
      await repository.softDelete(company.id);

      expect(await repository.findById(company.id)).toBeNull();
      expect(await repository.findById(company.id, { includeDeleted: true })).not.toBeNull();
    });

    it('hard-deletes a compensated registration outright', async () => {
      const company = await seed();

      await repository.hardDelete(company.id);

      expect(await repository.findById(company.id, { includeDeleted: true })).toBeNull();
      // The slug is genuinely free again: the row is gone, not hidden.
      expect(await repository.slugTaken(company.slug)).toBe(false);
    });

    it('records the owner account identity provisioned', async () => {
      const company = await seed();

      await repository.setOwnerAccount(company.id, 'acc_owner');

      expect((await repository.findById(company.id))?.ownerAccountId).toBe('acc_owner');
    });
  });

  /* -------------------------------------------------------------- paginate */

  describe('listing', () => {
    it('pages without repeating or skipping a row', async () => {
      const ids = [await seed(), await seed(), await seed()].map((company) => company.id);

      const seen: string[] = [];
      let cursor = null as null | string;

      for (let page = 0; page < 10; page += 1) {
        const result = await repository.list(
          {},
          { limit: 2, cursor: cursor ? decode(cursor) : null },
        );
        seen.push(...result.items.map((company) => company.id));
        cursor = result.nextCursor;
        if (!cursor) break;
      }

      for (const id of ids) {
        expect(seen.filter((candidate) => candidate === id)).toHaveLength(1);
      }
    });

    it('returns newest first', async () => {
      await seed();
      await seed();

      const { items } = await repository.list({}, { limit: 100, cursor: null });
      const times = items.map((company) => company.createdAt.getTime());

      expect([...times].sort((a, b) => b - a)).toEqual(times);
    });

    it('filters by state', async () => {
      const active = await seed();
      await repository.setState(active.id, CompanyState.Active);
      const pending = await seed();

      const { items } = await repository.list({ state: CompanyState.Active }, { limit: 100, cursor: null });
      const ids = items.map((company) => company.id);

      expect(ids).toContain(active.id);
      expect(ids).not.toContain(pending.id);
    });

    it('hides soft-deleted companies unless asked', async () => {
      const company = await seed();
      await repository.softDelete(company.id);

      const hidden = await repository.list({}, { limit: 100, cursor: null });
      expect(hidden.items.map((row) => row.id)).not.toContain(company.id);

      const shown = await repository.list({ includeDeleted: true }, { limit: 100, cursor: null });
      expect(shown.items.map((row) => row.id)).toContain(company.id);
    });

    it('matches a search on the slug prefix', async () => {
      const company = await seed();

      const { items } = await repository.list(
        { search: company.slug.slice(0, 10) },
        { limit: 100, cursor: null },
      );

      expect(items.map((row) => row.id)).toContain(company.id);
    });

    it('reports no next page when the results fit', async () => {
      await seed();

      expect((await repository.list({}, { limit: 100, cursor: null })).nextCursor).toBeNull();
    });
  });
});

/** Mirrors the cursor encoding in nestshared, so the test can page by hand. */
function decode(cursor: string): { createdAt: string; id: string } {
  const decoded = Buffer.from(cursor, 'base64url').toString('utf8');
  const separator = decoded.lastIndexOf('|');
  return { createdAt: decoded.slice(0, separator), id: decoded.slice(separator + 1) };
}
