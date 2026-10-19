/**
 * The rules that need no I/O.
 *
 * The slug table is the important one: a slug becomes a hostname label, and a
 * value that slips through here is either a portal that cannot be served or a
 * portal that shadows one of the platform's own.
 */
import { Problem } from '@karmarat/nestshared';

import {
  ALLOWED_IMAGE_TYPES,
  CompanyState,
  MAX_ASSET_BYTES,
  assertUploadAllowed,
  assertValidSlug,
  assetObjectKey,
  checkSlug,
  isAllowedImageType,
  normaliseSlug,
  slugRejectionMessage,
  toPublicCompany,
  toSummary,
  type Company,
  type SlugRejection,
} from './domain';
import { toPlatformView, toProfileView } from './views';

describe('checkSlug', () => {
  const cases: Array<{ name: string; slug: string; want: SlugRejection | null }> = [
    { name: 'plain word', slug: 'acme', want: null },
    { name: 'hyphenated', slug: 'acme-corp', want: null },
    { name: 'digits', slug: 'acme2024', want: null },
    { name: 'minimum length', slug: 'abc', want: null },
    { name: 'maximum length', slug: 'a'.repeat(40), want: null },
    { name: 'leading and trailing space is trimmed', slug: '  acme  ', want: null },
    { name: 'uppercase is folded', slug: 'ACME', want: null },

    { name: 'too short', slug: 'ab', want: 'length' },
    { name: 'too long', slug: 'a'.repeat(41), want: 'length' },
    { name: 'empty', slug: '', want: 'length' },
    { name: 'leading hyphen', slug: '-acme', want: 'hyphen_edge' },
    { name: 'trailing hyphen', slug: 'acme-', want: 'hyphen_edge' },
    { name: 'consecutive hyphens', slug: 'ac--me', want: 'consecutive_hyphens' },
    { name: 'underscore', slug: 'acme_corp', want: 'charset' },
    { name: 'dot', slug: 'acme.corp', want: 'charset' },
    { name: 'slash', slug: 'acme/corp', want: 'charset' },
    { name: 'space inside', slug: 'acme corp', want: 'charset' },
    { name: 'non-ascii', slug: 'acmé', want: 'charset' },

    // From the generated reserved list, which the gateway's router shares.
    { name: 'reserved root portal', slug: 'root', want: 'reserved' },
    { name: 'reserved jobs portal', slug: 'jobs', want: 'reserved' },
    { name: 'reserved api host', slug: 'api', want: 'reserved' },
    { name: 'reserved, differently cased', slug: 'API', want: 'reserved' },
  ];

  for (const { name, slug, want } of cases) {
    it(`${want === null ? 'accepts' : `rejects (${want})`}: ${name}`, () => {
      expect(checkSlug(slug)).toBe(want);
    });
  }

  it('produces a message for every rejection it can return', () => {
    const rejections: SlugRejection[] = [
      'length',
      'charset',
      'hyphen_edge',
      'consecutive_hyphens',
      'reserved',
    ];
    for (const rejection of rejections) {
      expect(slugRejectionMessage(rejection, 'acme')).not.toBe('');
    }
  });
});

describe('normaliseSlug', () => {
  it('trims and lowercases so one address cannot be claimed twice', () => {
    expect(normaliseSlug('  AcMe-Corp ')).toBe('acme-corp');
  });
});

describe('assertValidSlug', () => {
  it('returns the normalised slug when it is usable', () => {
    expect(assertValidSlug(' ACME ')).toBe('acme');
  });

  it('throws a 422 carrying the offending field', () => {
    expect.assertions(3);
    try {
      assertValidSlug('root');
    } catch (error) {
      const problem = error as Problem;
      expect(problem).toBeInstanceOf(Problem);
      expect(problem.getStatus()).toBe(422);
      expect(problem.fields?.slug?.[0]).toContain('reserved');
    }
  });
});

describe('uploads', () => {
  it('allows only the image types the bucket can serve safely', () => {
    for (const type of ALLOWED_IMAGE_TYPES) {
      expect(isAllowedImageType(type)).toBe(true);
    }
  });

  // SVG is the reason the list is an allow-list: the bucket serves company
  // assets publicly, and an SVG is a script that runs in the bucket's origin.
  it.each(['image/svg+xml', 'text/html', 'application/pdf', 'application/octet-stream', ''])(
    'refuses %s',
    (type) => {
      expect(isAllowedImageType(type)).toBe(false);
    },
  );

  it('accepts a valid upload and returns the normalised type', () => {
    expect(assertUploadAllowed(' IMAGE/PNG ', 1024)).toBe('image/png');
  });

  it.each([
    ['a type outside the list', 'image/svg+xml', 1024, 'contentType'],
    ['a size of zero', 'image/png', 0, 'sizeBytes'],
    ['a negative size', 'image/png', -1, 'sizeBytes'],
    ['a fractional size', 'image/png', 1.5, 'sizeBytes'],
    ['a size over the cap', 'image/png', MAX_ASSET_BYTES + 1, 'sizeBytes'],
  ])('rejects %s', (_name, type, size, field) => {
    expect.assertions(2);
    try {
      assertUploadAllowed(type as string, size as number);
    } catch (error) {
      const problem = error as Problem;
      expect(problem.getStatus()).toBe(422);
      expect(problem.fields?.[field]).toBeDefined();
    }
  });

  it('caps at exactly 5 MB', () => {
    expect(assertUploadAllowed('image/png', MAX_ASSET_BYTES)).toBe('image/png');
    expect(MAX_ASSET_BYTES).toBe(5 * 1024 * 1024);
  });

  it('derives the key from the tenant, so a client cannot choose where bytes land', () => {
    const key = assetObjectKey('company-a', 'logo', 'image/png', 'u1');
    expect(key).toBe('company/company-a/logo/u1.png');
    expect(key.startsWith('company/company-a/')).toBe(true);
  });

  it('gives each content type its own extension', () => {
    expect(assetObjectKey('c', 'hero', 'image/jpeg', 'u')).toBe('company/c/hero/u.jpg');
    expect(assetObjectKey('c', 'hero', 'image/webp', 'u')).toBe('company/c/hero/u.webp');
    expect(assetObjectKey('c', 'hero', 'image/gif', 'u')).toBe('company/c/hero/u.gif');
  });
});

/** A fully populated row, so a projection test can see everything it omits. */
function company(overrides: Partial<Company> = {}): Company {
  return {
    id: 'c-1',
    slug: 'acme',
    legalName: 'Acme Holdings Limited',
    displayName: 'Acme',
    state: CompanyState.Active,
    description: 'We make things.',
    logoKey: 'company/c-1/logo/a.png',
    website: 'https://acme.test',
    industry: 'Manufacturing',
    size: '51-200',
    foundedYear: 1998,
    headquarters: 'Dublin',
    locations: [{ city: 'Dublin', country: 'IE' }],
    socialLinks: { linkedin: 'https://linkedin.test/acme' },
    contactEmail: 'hr@acme.test',
    contactPhone: '+353 1 555 0100',
    country: 'Ireland',
    brandColor: '#1f6feb',
    heroImageKey: 'company/c-1/hero/b.png',
    tagline: 'Build with us',
    aboutMarkdown: '# About',
    benefits: ['Remote'],
    customDomain: 'careers.acme.test',
    customDomainVerified: false,
    portalPublished: true,
    ownerEmail: 'owner@acme.test',
    ownerName: 'Ada Owner',
    ownerAccountId: 'acc_1',
    internalNotes: 'Chased about the unpaid invoice.',
    suspensionReason: '',
    approvedAt: new Date('2024-01-02T00:00:00Z'),
    suspendedAt: null,
    deletedAt: null,
    createdAt: new Date('2024-01-01T00:00:00Z'),
    updatedAt: new Date('2024-01-03T00:00:00Z'),
    ...overrides,
  };
}

describe('toPublicCompany', () => {
  /**
   * The projection is the enforcement, so the test is written against the
   * serialised body rather than against the properties: a nested object added
   * later would slip past a key-by-key assertion.
   */
  it('exposes nothing an anonymous visitor is not entitled to', () => {
    const body = JSON.stringify(toPublicCompany(company()));

    for (const secret of [
      'hr@acme.test',
      '+353 1 555 0100',
      'owner@acme.test',
      'Ada Owner',
      'acc_1',
      'Chased about the unpaid invoice.',
      'Acme Holdings Limited',
      'careers.acme.test',
    ]) {
      expect(body).not.toContain(secret);
    }

    // Lifecycle and portal flags tell a stranger how the platform runs its
    // review queue; they are absent too.
    expect(body).not.toContain('state');
    expect(body).not.toContain('portalPublished');
  });

  it('carries what a careers page actually renders', () => {
    const view = toPublicCompany(company());
    expect(view.slug).toBe('acme');
    expect(view.displayName).toBe('Acme');
    expect(view.brand.tagline).toBe('Build with us');
    expect(view.locations).toHaveLength(1);
  });
});

describe('toProfileView', () => {
  it('shows the company its own contact details but not the platform notes', () => {
    const body = JSON.stringify(toProfileView(company()));
    expect(body).toContain('hr@acme.test');
    expect(body).not.toContain('Chased about the unpaid invoice.');
    expect(body).not.toContain('owner@acme.test');
  });

  it('never claims a custom domain is verified, because nothing verifies one', () => {
    expect(toProfileView(company()).careersPortal.customDomainVerified).toBe(false);
  });
});

describe('toPlatformView', () => {
  it('is the only view carrying operational fields', () => {
    const view = toPlatformView(company());
    expect(view.internalNotes).toBe('Chased about the unpaid invoice.');
    expect(view.ownerEmail).toBe('owner@acme.test');
    expect(view.ownerAccountId).toBe('acc_1');
  });
});

describe('toSummary', () => {
  it('gives an internal caller a name and a logo and nothing else', () => {
    expect(toSummary(company())).toEqual({
      id: 'c-1',
      slug: 'acme',
      name: 'Acme',
      state: CompanyState.Active,
      logoKey: 'company/c-1/logo/a.png',
    });
  });
});
