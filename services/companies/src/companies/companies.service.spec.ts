/**
 * The orchestration that a pure-domain test cannot reach: the registration
 * saga, the public 404, and the tenant check on asset keys.
 *
 * Fakes rather than a database, because what is under test here is *ordering* —
 * which write happens first, what is undone when the second fails, and what is
 * published only after a commit. None of that needs Postgres, and a test that
 * needed it would be skipped on the machines where this matters most.
 */
import { Logger } from '@nestjs/common';
import { Problem, Subject } from '@karmarat/nestshared';

import type { CompaniesConfig } from '../config';
import type { IdentityClient } from '../identity/identity.client';
import type { Presigner } from '../storage/presigner';
import type { CompaniesRepository } from './companies.repository';
import { CompaniesService, type EventPublisher } from './companies.service';
import { CompanyState, type Company } from './domain';
import type { RegisterCompanyDto } from './dto/register.dto';

/* -------------------------------------------------------------------------- */
/* Fakes                                                                      */
/* -------------------------------------------------------------------------- */

function stubCompany(overrides: Partial<Company> = {}): Company {
  return {
    id: 'c-1',
    slug: 'acme',
    legalName: 'Acme',
    displayName: 'Acme',
    state: CompanyState.Active,
    description: '',
    logoKey: '',
    website: '',
    industry: '',
    size: '',
    foundedYear: null,
    headquarters: '',
    locations: [],
    socialLinks: {},
    contactEmail: '',
    contactPhone: '',
    country: '',
    brandColor: '',
    heroImageKey: '',
    tagline: '',
    aboutMarkdown: '',
    benefits: [],
    customDomain: '',
    customDomainVerified: false,
    portalPublished: true,
    ownerEmail: '',
    ownerName: '',
    ownerAccountId: '',
    internalNotes: '',
    suspensionReason: '',
    approvedAt: null,
    suspendedAt: null,
    deletedAt: null,
    createdAt: new Date('2024-01-01T00:00:00Z'),
    updatedAt: new Date('2024-01-01T00:00:00Z'),
    ...overrides,
  };
}

const registration: RegisterCompanyDto = {
  companyName: 'Acme',
  slug: 'acme',
  ownerEmail: 'owner@acme.test',
  ownerName: 'Ada Owner',
  ownerPassword: 'correct-horse-battery',
  industry: 'Manufacturing',
  size: '51-200',
  country: 'Ireland',
};

type RepositoryMethod =
  | 'create'
  | 'hardDelete'
  | 'setOwnerAccount'
  | 'findBySlug'
  | 'findById'
  | 'updateProfile'
  | 'slugTaken'
  | 'setState';

interface Harness {
  service: CompaniesService;
  // Plain `jest.Mock` rather than `jest.Mocked<CompaniesRepository>`: the
  // service is constructed with the fake cast to the real type, so the compiler
  // still checks the calls it makes, and the test keeps the freedom to hand a
  // single call a different answer.
  repository: Record<RepositoryMethod, jest.Mock>;
  identity: { provisionCompany: jest.Mock };
  publish: jest.Mock;
}

function harness(options: { presigner?: Presigner | null } = {}): Harness {
  const repository = {
    create: jest.fn(async () => stubCompany({ state: CompanyState.PendingReview })),
    hardDelete: jest.fn(async () => undefined),
    setOwnerAccount: jest.fn(async () => undefined),
    findBySlug: jest.fn(async () => stubCompany()),
    findById: jest.fn(async () => stubCompany()),
    updateProfile: jest.fn(async () => stubCompany()),
    slugTaken: jest.fn(async () => false),
    setState: jest.fn(async () => stubCompany()),
  };

  const identity = {
    provisionCompany: jest.fn(async () => ({
      companyId: 'c-1',
      slug: 'acme',
      ownerAccountId: 'acc_1',
      membershipId: 'mem_1',
      ownerCreated: true,
    })),
  };

  const publish = jest.fn(async () => undefined);
  const events: EventPublisher = { publish };

  const config = {
    storage: { uploadTtlSeconds: 600 },
  } as unknown as CompaniesConfig;

  const service = new CompaniesService(
    repository as unknown as CompaniesRepository,
    identity as unknown as IdentityClient,
    options.presigner ?? null,
    events,
    config,
  );

  return { service, repository, identity, publish };
}

beforeAll(() => {
  // The service logs the failures it swallows on purpose; a green run should
  // not look like a broken one.
  jest.spyOn(Logger.prototype, 'error').mockImplementation(() => undefined);
  jest.spyOn(Logger.prototype, 'log').mockImplementation(() => undefined);
});

afterAll(() => jest.restoreAllMocks());

/* -------------------------------------------------------------------------- */
/* Registration                                                               */
/* -------------------------------------------------------------------------- */

describe('register', () => {
  it('claims the slug before it provisions the tenant', async () => {
    const { service, repository, identity } = harness();

    await service.register(registration);

    const created = repository.create.mock.invocationCallOrder[0]!;
    const provisioned = identity.provisionCompany.mock.invocationCallOrder[0]!;
    // The unique index is the only thing that can decide a slug race, so the
    // insert must be the first irreversible act.
    expect(created).toBeLessThan(provisioned);
  });

  it('mints the company id itself and hands the same one to identity', async () => {
    const { service, repository, identity } = harness();

    const result = await service.register(registration);

    expect(result.companyId).toMatch(/^[0-9a-f-]{36}$/);
    expect(repository.create.mock.calls[0]![0].id).toBe(result.companyId);
    expect(identity.provisionCompany.mock.calls[0]![0].companyId).toBe(result.companyId);
  });

  it('records the owner account identity created', async () => {
    const { service, repository } = harness();

    await service.register(registration);

    expect(repository.setOwnerAccount).toHaveBeenCalledWith(expect.any(String), 'acc_1');
  });

  it('publishes company.registered with the tenant on the envelope', async () => {
    const { service, publish } = harness();

    await service.register(registration);

    expect(publish).toHaveBeenCalledTimes(1);
    const [subject, payload, options] = publish.mock.calls[0]!;
    expect(subject).toBe(Subject.CompanyRegistered);
    expect(payload).toMatchObject({ slug: 'acme', ownerAccountId: 'acc_1' });
    // A deterministic id means a republish after a broker blip delivers the
    // fact once rather than twice.
    expect(options.id).toBeDefined();
    expect(options.companyId).toBe(payload.companyId);
  });

  it('rejects an unusable slug before writing anything', async () => {
    const { service, repository, identity } = harness();

    await expect(service.register({ ...registration, slug: 'root' })).rejects.toBeInstanceOf(Problem);

    expect(repository.create).not.toHaveBeenCalled();
    expect(identity.provisionCompany).not.toHaveBeenCalled();
  });

  it('deletes the company again when identity refuses to provision it', async () => {
    const { service, repository, identity, publish } = harness();
    const rejection = new Problem(409, 'email_taken', 'Conflict', 'That email is already registered.');
    identity.provisionCompany.mockRejectedValueOnce(rejection);

    await expect(service.register(registration)).rejects.toBe(rejection);

    // Compensation, not a transaction: the two writes are in different
    // services. It is safe because the row is seconds old and unreferenced.
    expect(repository.hardDelete).toHaveBeenCalledTimes(1);
    expect(publish).not.toHaveBeenCalled();
  });

  it('still reports identity\'s failure when the compensating delete also fails', async () => {
    const { service, repository, identity } = harness();
    const rejection = new Problem(422, 'validation_failed', 'Validation Failed', 'bad slug');
    identity.provisionCompany.mockRejectedValueOnce(rejection);
    repository.hardDelete.mockRejectedValueOnce(new Error('connection reset'));

    // The caller learns why their registration failed; the orphaned row is a
    // logged operational problem, not a second error thrown at a visitor.
    await expect(service.register(registration)).rejects.toBe(rejection);
  });

  it('does not fail a committed registration because the broker is down', async () => {
    const { service, publish } = harness();
    publish.mockRejectedValueOnce(new Error('nats unreachable'));

    await expect(service.register(registration)).resolves.toMatchObject({ slug: 'acme' });
  });
});

describe('slugAvailability', () => {
  it('answers an unusable slug without touching the database', async () => {
    const { service, repository } = harness();

    const result = await service.slugAvailability('ROOT');

    expect(result).toMatchObject({ slug: 'root', available: false });
    expect(result.reason).toContain('reserved');
    expect(repository.slugTaken).not.toHaveBeenCalled();
  });

  it('reports a claimed slug as unavailable', async () => {
    const { service, repository } = harness();
    repository.slugTaken.mockResolvedValueOnce(true);

    await expect(service.slugAvailability('acme')).resolves.toMatchObject({ available: false });
  });

  it('reports a free, valid slug as available', async () => {
    const { service } = harness();

    await expect(service.slugAvailability('acme')).resolves.toEqual({ slug: 'acme', available: true });
  });
});

/* -------------------------------------------------------------------------- */
/* Public careers portal                                                      */
/* -------------------------------------------------------------------------- */

describe('publicProfile', () => {
  it.each([
    ['no such company', null],
    ['awaiting review', stubCompany({ state: CompanyState.PendingReview })],
    ['suspended', stubCompany({ state: CompanyState.Suspended })],
    ['closed', stubCompany({ state: CompanyState.Closed })],
    ['portal not published', stubCompany({ portalPublished: false })],
  ])('answers 404 for %s, so the endpoint is not a registration oracle', async (_name, row) => {
    const { service, repository } = harness();
    repository.findBySlug.mockResolvedValueOnce(row as Company);

    expect.assertions(1);
    await service.publicProfile('acme').catch((error: Problem) => {
      expect(error.getStatus()).toBe(404);
    });
  });

  it('serves an active, published company', async () => {
    const { service } = harness();

    await expect(service.publicProfile('ACME ')).resolves.toMatchObject({ slug: 'acme' });
  });
});

/* -------------------------------------------------------------------------- */
/* Profile                                                                    */
/* -------------------------------------------------------------------------- */

describe('updateProfile', () => {
  it('refuses an asset key belonging to another company', async () => {
    const { service, repository } = harness();

    expect.assertions(2);
    await service.updateProfile('c-1', { logoKey: 'company/c-2/logo/a.png' }).catch((error: Problem) => {
      expect(error.getStatus()).toBe(409);
    });
    expect(repository.updateProfile).not.toHaveBeenCalled();
  });

  it('accepts a key under this company\'s own prefix', async () => {
    const { service, repository } = harness();

    await service.updateProfile('c-1', { logoKey: 'company/c-1/logo/a.png' });

    expect(repository.updateProfile).toHaveBeenCalled();
  });

  it('allows clearing a key', async () => {
    const { service, repository } = harness();

    await service.updateProfile('c-1', { heroImageKey: '' });

    expect(repository.updateProfile).toHaveBeenCalled();
  });

  it('publishes a rename so identity\'s projection keeps up, and nothing otherwise', async () => {
    const { service, publish } = harness();

    await service.updateProfile('c-1', { tagline: 'Build with us' });
    expect(publish).not.toHaveBeenCalled();

    await service.updateProfile('c-1', { displayName: 'Acme Group' });
    expect(publish).toHaveBeenCalledWith(Subject.CompanyUpdated, expect.anything(), expect.anything());
  });

  it('answers 404 when the tenant has been removed underneath the request', async () => {
    const { service, repository } = harness();
    repository.updateProfile.mockResolvedValueOnce(null);

    expect.assertions(1);
    await service.updateProfile('c-1', { tagline: 'x' }).catch((error: Problem) => {
      expect(error.getStatus()).toBe(404);
    });
  });
});

describe('uploadUrl', () => {
  it('answers 503 rather than 500 when object storage is not configured', async () => {
    const { service } = harness({ presigner: null });

    expect.assertions(2);
    await service
      .uploadUrl('c-1', 'logo', { contentType: 'image/png', sizeBytes: 1024 })
      .catch((error: Problem) => {
        expect(error.getStatus()).toBe(503);
        expect(error.detail).not.toContain('Presigner');
      });
  });

  it('signs a key under the verified tenant, not anything from the request', async () => {
    const presignPut = jest.fn(() => ({
      url: 'http://bucket.test/signed',
      method: 'PUT' as const,
      headers: { 'Content-Type': 'image/png' },
      expiresAt: new Date(),
    }));
    const { service } = harness({ presigner: { presignPut } as unknown as Presigner });

    const result = await service.uploadUrl('c-1', 'logo', { contentType: 'image/png', sizeBytes: 1024 });

    expect(result.key.startsWith('company/c-1/logo/')).toBe(true);
    expect(presignPut).toHaveBeenCalledWith(result.key, 'image/png', 600);
  });
});

/* -------------------------------------------------------------------------- */
/* Lifecycle                                                                  */
/* -------------------------------------------------------------------------- */

describe('approve', () => {
  it('is idempotent, because a double-click in the console is not an error', async () => {
    const { service, repository, publish } = harness();
    repository.findById.mockResolvedValueOnce(stubCompany({ state: CompanyState.Active }));

    await service.approve('c-1');

    expect(repository.setState).not.toHaveBeenCalled();
    expect(publish).not.toHaveBeenCalled();
  });

  it('refuses to reopen a closed company', async () => {
    const { service, repository } = harness();
    repository.findById.mockResolvedValueOnce(stubCompany({ state: CompanyState.Closed }));

    expect.assertions(1);
    await service.approve('c-1').catch((error: Problem) => {
      expect(error.getStatus()).toBe(409);
    });
  });

  it('publishes company.approved only after the state has been committed', async () => {
    const { service, repository, publish } = harness();
    repository.findById.mockResolvedValueOnce(stubCompany({ state: CompanyState.PendingReview }));

    await service.approve('c-1');

    expect(repository.setState.mock.invocationCallOrder[0]!).toBeLessThan(
      publish.mock.invocationCallOrder[0]!,
    );
    expect(publish.mock.calls[0]![0]).toBe(Subject.CompanyApproved);
  });

  it('answers 404 for a company that does not exist', async () => {
    const { service, repository } = harness();
    repository.findById.mockResolvedValueOnce(null);

    expect.assertions(1);
    await service.approve('c-9').catch((error: Problem) => {
      expect(error.getStatus()).toBe(404);
    });
  });
});

describe('suspend', () => {
  it('carries the reason on the event so other services can explain the lockout', async () => {
    const { service, publish } = harness();

    await service.suspend('c-1', 'Payment dispute');

    expect(publish.mock.calls[0]![0]).toBe(Subject.CompanySuspended);
    expect(publish.mock.calls[0]![1]).toMatchObject({ reason: 'Payment dispute' });
  });

  it('answers 404 for an unknown or already-deleted company', async () => {
    const { service, repository } = harness();
    repository.setState.mockResolvedValueOnce(null);

    expect.assertions(1);
    await service.suspend('c-9', 'gone').catch((error: Problem) => {
      expect(error.getStatus()).toBe(404);
    });
  });
});
