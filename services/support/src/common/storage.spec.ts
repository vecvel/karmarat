/**
 * Attachment keys and presigning.
 *
 * The properties under test are the ones that keep one tenant's documents away
 * from another's: a key is always built from the verified company id, a key
 * that was not is refused, and a file type that would execute in the support
 * agent's browser never gets a signature at all.
 */
import { Problem } from '@karmarat/nestshared';

import type { StorageConfig } from '../config';
import {
  ALLOWED_CONTENT_TYPES,
  StoragePresigner,
  assertOwnedKey,
  buildUploadKey,
  sanitizeFileName,
  supportKeyPrefix,
} from './storage';

const CONFIG: StorageConfig = {
  endpoint: 'http://localhost:9000',
  region: 'us-east-1',
  accessKey: 'karmarat',
  secretKey: 'karmarat-secret',
  bucket: 'documents',
  forcePathStyle: true,
  presignTtlSeconds: 900,
  maxUploadBytes: 1024 * 1024,
};

const TENANT = '11111111-1111-4111-8111-111111111111';
const OTHER = '22222222-2222-4222-8222-222222222222';

describe('key ownership', () => {
  it('accepts a key under this tenant’s own prefix', () => {
    expect(() => assertOwnedKey(TENANT, `${supportKeyPrefix(TENANT)}upl_1/report.pdf`)).not.toThrow();
  });

  const refused: Array<[string, string]> = [
    ["another tenant's prefix", `company/${OTHER}/support/upl_1/report.pdf`],
    ['a traversal out of the prefix', `company/${TENANT}/support/../../${OTHER}/support/x.pdf`],
    ['a different area of the same tenant', `company/${TENANT}/logos/logo.png`],
    ['no prefix at all', 'report.pdf'],
    ['a prefix that merely starts the same way', `company/${TENANT}xx/support/report.pdf`],
  ];

  it.each(refused)('refuses %s', (_label, key) => {
    expect(() => assertOwnedKey(TENANT, key)).toThrow(Problem);
  });

  it('refuses an absurdly long key rather than storing it', () => {
    const key = `${supportKeyPrefix(TENANT)}${'a'.repeat(1100)}`;
    expect(() => assertOwnedKey(TENANT, key)).toThrow(Problem);
  });
});

describe('sanitizeFileName', () => {
  it.each([
    ['../../etc/passwd', 'passwd'],
    ['C:\\Users\\me\\report.pdf', 'report.pdf'],
    ['my report (final).pdf', 'my_report__final_.pdf'],
    ['.hidden', 'hidden'],
    ['', 'attachment'],
    ['/', 'attachment'],
  ])('reduces %s to %s', (input, expected) => {
    expect(sanitizeFileName(input)).toBe(expected);
  });

  it('keeps a crafted name inside the tenant prefix', () => {
    const key = buildUploadKey(TENANT, 'upl_1', '../../../other/evil.pdf');
    expect(key).toBe(`company/${TENANT}/support/upl_1/evil.pdf`);
    expect(() => assertOwnedKey(TENANT, key)).not.toThrow();
  });
});

describe('StoragePresigner', () => {
  const presigner = new StoragePresigner(CONFIG);

  it('signs a PUT into the tenant’s prefix', () => {
    const result = presigner.presignUpload({
      companyId: TENANT,
      fileName: 'report.pdf',
      contentType: 'application/pdf',
      sizeBytes: 1024,
      uploadId: 'upl_1',
      now: new Date('2026-01-01T00:00:00.000Z'),
    });

    expect(result.method).toBe('PUT');
    expect(result.objectKey).toBe(`company/${TENANT}/support/upl_1/report.pdf`);
    expect(result.url).toContain('/documents/company/');
    expect(result.url).toContain('X-Amz-Signature=');
    expect(result.url).toContain('X-Amz-Expires=900');
    // The content type is signed, so a client cannot upload an HTML document
    // under a signature it obtained for a PDF.
    expect(result.url).toContain('content-type');
    expect(result.headers['Content-Type']).toBe('application/pdf');
    expect(result.expiresAt).toBe('2026-01-01T00:15:00.000Z');
  });

  it('does not leak the secret key into the URL', () => {
    const result = presigner.presignUpload({
      companyId: TENANT,
      fileName: 'report.pdf',
      contentType: 'application/pdf',
      sizeBytes: 1024,
      uploadId: 'upl_1',
    });

    expect(result.url).not.toContain(CONFIG.secretKey);
  });

  it('produces a stable signature for the same request', () => {
    const request = {
      companyId: TENANT,
      fileName: 'report.pdf',
      contentType: 'application/pdf',
      sizeBytes: 1024,
      uploadId: 'upl_1',
      now: new Date('2026-01-01T00:00:00.000Z'),
    };

    expect(presigner.presignUpload(request).url).toBe(presigner.presignUpload(request).url);
  });

  it.each(ALLOWED_CONTENT_TYPES)('accepts %s', (contentType) => {
    expect(() =>
      presigner.presignUpload({
        companyId: TENANT,
        fileName: 'file',
        contentType,
        sizeBytes: 10,
        uploadId: 'upl_1',
      }),
    ).not.toThrow();
  });

  it.each([
    ['image/svg+xml', 'scripts in an SVG run in the reviewer’s browser'],
    ['text/html', 'so does a document'],
    ['application/x-msdownload', 'an executable has no business on a ticket'],
    ['', 'an unset type must not default to allowed'],
  ])('refuses %s (%s)', (contentType) => {
    expect(() =>
      presigner.presignUpload({
        companyId: TENANT,
        fileName: 'file',
        contentType,
        sizeBytes: 10,
        uploadId: 'upl_1',
      }),
    ).toThrow(Problem);
  });

  it.each([0, -1, 1.5, CONFIG.maxUploadBytes + 1])('refuses a size of %s before signing', (sizeBytes) => {
    expect(() =>
      presigner.presignUpload({
        companyId: TENANT,
        fileName: 'file',
        contentType: 'application/pdf',
        sizeBytes,
        uploadId: 'upl_1',
      }),
    ).toThrow(Problem);
  });
});
