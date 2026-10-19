/**
 * Presigned uploads for support attachments.
 *
 * LOCAL WORKAROUND: `packages/nestshared` has no object-storage helper, so the
 * AWS SigV4 query-string signing is implemented here against node:crypto rather
 * than by pulling in an SDK. It is about eighty lines, has no transitive
 * dependencies, and works against MinIO and S3 alike. It belongs in nestshared
 * next to database.ts once a second Node service needs to sign a URL.
 *
 * The service signs a URL and records the key; it never proxies the bytes. A
 * proxy would put every customer document through a Node process's heap and
 * make the upload's size limit a memory limit.
 */
import { createHash, createHmac } from 'node:crypto';

import { badRequest, forbidden } from '@karmarat/nestshared';

import type { StorageConfig } from '../config';

/**
 * What a support ticket may carry.
 *
 * An allow-list rather than a deny-list: the interesting attack is an
 * `image/svg+xml` or an `text/html` that executes in the reviewer's browser when
 * the desk opens it, and a deny-list never finishes enumerating those.
 */
export const ALLOWED_CONTENT_TYPES: readonly string[] = [
  'application/pdf',
  'image/png',
  'image/jpeg',
  'image/gif',
  'image/webp',
  'text/plain',
  'text/csv',
  'application/zip',
  'application/msword',
  'application/vnd.openxmlformats-officedocument.wordprocessingml.document',
  'application/vnd.ms-excel',
  'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet',
  'application/json',
];

/** Every support object a tenant owns lives under this prefix and nowhere else. */
export function supportKeyPrefix(companyId: string): string {
  return `company/${companyId}/support/`;
}

/**
 * Rejects a key that does not belong to this tenant.
 *
 * The client chooses which key to reference when it creates a ticket, and a key
 * is a plain string — without this check a tenant could attach another tenant's
 * document to its own ticket and read it back through the ticket view. The
 * prefix is derived from the verified principal, so there is nothing to forge.
 */
export function assertOwnedKey(companyId: string, objectKey: string): void {
  const prefix = supportKeyPrefix(companyId);
  if (!objectKey.startsWith(prefix) || objectKey.includes('..') || objectKey.length > 1024) {
    throw forbidden('That attachment does not belong to this company.');
  }
}

/**
 * Builds the key a tenant's upload will land on.
 *
 * The client's file name is never used as a path segment on its own: it is
 * sanitised and placed under a freshly minted id, so two uploads of "invoice.pdf"
 * cannot collide and a crafted name cannot escape the prefix.
 */
export function buildUploadKey(companyId: string, uploadId: string, fileName: string): string {
  return `${supportKeyPrefix(companyId)}${uploadId}/${sanitizeFileName(fileName)}`;
}

export function sanitizeFileName(fileName: string): string {
  const base = fileName.split(/[\\/]/).pop() ?? '';
  const cleaned = base.replace(/[^A-Za-z0-9._-]/g, '_').replace(/^\.+/, '').slice(0, 120);
  return cleaned === '' ? 'attachment' : cleaned;
}

export interface UploadRequest {
  companyId: string;
  fileName: string;
  contentType: string;
  sizeBytes: number;
  uploadId: string;
  /** Injectable for tests; defaults to now. */
  now?: Date;
}

export interface PresignedUpload {
  objectKey: string;
  url: string;
  method: 'PUT';
  expiresAt: string;
  /** The client must send exactly these headers or the signature will not match. */
  headers: Record<string, string>;
}

export class StoragePresigner {
  constructor(private readonly config: StorageConfig) {}

  /**
   * Signs a PUT for one attachment.
   *
   * Content type and size are validated *before* signing: a signature is a
   * capability, and handing one out for a 4GB executable and then refusing the
   * ticket afterwards leaves the object in the bucket regardless.
   */
  presignUpload(request: UploadRequest): PresignedUpload {
    if (!ALLOWED_CONTENT_TYPES.includes(request.contentType)) {
      throw badRequest('That file type cannot be attached to a support ticket.');
    }
    if (!Number.isInteger(request.sizeBytes) || request.sizeBytes <= 0) {
      throw badRequest('A positive file size is required.');
    }
    if (request.sizeBytes > this.config.maxUploadBytes) {
      const megabytes = Math.floor(this.config.maxUploadBytes / (1024 * 1024));
      throw badRequest(`Attachments must be ${megabytes}MB or smaller.`);
    }

    const objectKey = buildUploadKey(request.companyId, request.uploadId, request.fileName);
    const now = request.now ?? new Date();

    const url = this.sign('PUT', objectKey, now, {
      // Signed rather than merely advertised: an unsigned content type is one the
      // client can change at upload time, which would defeat the allow-list.
      'content-type': request.contentType,
    });

    return {
      objectKey,
      url,
      method: 'PUT',
      expiresAt: new Date(now.getTime() + this.config.presignTtlSeconds * 1000).toISOString(),
      headers: { 'Content-Type': request.contentType },
    };
  }

  /** AWS Signature Version 4, query-string ("presigned URL") flavour. */
  private sign(method: string, objectKey: string, now: Date, signedHeaders: Record<string, string>): string {
    const endpoint = new URL(this.config.endpoint);
    const host = endpoint.host;

    // Path-style for MinIO (bucket in the path); virtual-host style for S3.
    const path = this.config.forcePathStyle
      ? `/${this.config.bucket}/${encodeKey(objectKey)}`
      : `/${encodeKey(objectKey)}`;
    const requestHost = this.config.forcePathStyle ? host : `${this.config.bucket}.${host}`;

    const amzDate = now.toISOString().replace(/[:-]|\.\d{3}/g, '');
    const dateStamp = amzDate.slice(0, 8);
    const scope = `${dateStamp}/${this.config.region}/s3/aws4_request`;

    const headers: Record<string, string> = { ...signedHeaders, host: requestHost };
    const headerNames = Object.keys(headers).sort();
    const canonicalHeaders = headerNames.map((name) => `${name}:${headers[name]!.trim()}\n`).join('');
    const signedHeaderList = headerNames.join(';');

    const query: Record<string, string> = {
      'X-Amz-Algorithm': 'AWS4-HMAC-SHA256',
      'X-Amz-Credential': `${this.config.accessKey}/${scope}`,
      'X-Amz-Date': amzDate,
      'X-Amz-Expires': String(this.config.presignTtlSeconds),
      'X-Amz-SignedHeaders': signedHeaderList,
    };
    const canonicalQuery = Object.keys(query)
      .sort()
      .map((key) => `${rfc3986(key)}=${rfc3986(query[key]!)}`)
      .join('&');

    const canonicalRequest = [
      method,
      path,
      canonicalQuery,
      canonicalHeaders,
      signedHeaderList,
      // A presigned URL signs no body; the payload hash is the literal sentinel.
      'UNSIGNED-PAYLOAD',
    ].join('\n');

    const stringToSign = [
      'AWS4-HMAC-SHA256',
      amzDate,
      scope,
      createHash('sha256').update(canonicalRequest).digest('hex'),
    ].join('\n');

    const signature = hmac(this.signingKey(dateStamp), stringToSign).toString('hex');

    return `${endpoint.protocol}//${requestHost}${path}?${canonicalQuery}&X-Amz-Signature=${signature}`;
  }

  private signingKey(dateStamp: string): Buffer {
    // Derived per day and per service, which is what limits the blast radius of
    // a leaked signing key to that one day.
    const date = hmac(Buffer.from(`AWS4${this.config.secretKey}`, 'utf8'), dateStamp);
    const region = hmac(date, this.config.region);
    const service = hmac(region, 's3');
    return hmac(service, 'aws4_request');
  }
}

function hmac(key: Buffer, value: string): Buffer {
  return createHmac('sha256', key).update(value, 'utf8').digest();
}

/** RFC 3986 escaping: encodeURIComponent leaves !'()* alone and S3 does not. */
function rfc3986(value: string): string {
  return encodeURIComponent(value).replace(/[!'()*]/g, (char) => `%${char.charCodeAt(0).toString(16).toUpperCase()}`);
}

/** Each path segment is escaped, but the separators are not. */
function encodeKey(objectKey: string): string {
  return objectKey.split('/').map(rfc3986).join('/');
}
