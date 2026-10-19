/**
 * Configuration for the companies service.
 *
 * Everything shared with the rest of the platform comes from `loadBase`, so a
 * single .env drives the Go services and this one identically. Only what is
 * genuinely local to registration and company assets is read here.
 */
import { bool, int, loadBase, str, type BaseConfig } from '@karmarat/nestshared';

export const SERVICE_NAME = 'companies';
export const DEFAULT_PORT = 8082;

export interface CompaniesConfig extends BaseConfig {
  /** Base URL of the identity service, which provisions the tenant. */
  identityUrl: string;
  /** How long to wait for identity before giving up on a registration. */
  identityTimeoutMs: number;
  storage: StorageConfig;
  /** Slug-availability checks allowed per client per minute. */
  slugCheckLimit: number;
}

export interface StorageConfig {
  endpoint: string;
  region: string;
  accessKey: string;
  secretKey: string;
  bucket: string;
  pathStyle: boolean;
  /** Lifetime of a presigned upload URL. */
  uploadTtlSeconds: number;
}

export function loadConfig(): CompaniesConfig {
  const base = loadBase(SERVICE_NAME, DEFAULT_PORT);

  return {
    ...base,
    // `loadBase` falls back to DATABASE_URL, which in this monorepo is also the
    // company web app's own connection string. A service-specific override wins
    // so a stray DATABASE_URL cannot point this service at another database.
    postgresUrl: str('COMPANIES_DATABASE_URL', base.postgresUrl),
    identityUrl: str('IDENTITY_URL', 'http://localhost:8081'),
    identityTimeoutMs: int('COMPANIES_IDENTITY_TIMEOUT_MS', 10_000),
    slugCheckLimit: int('COMPANIES_SLUG_CHECK_LIMIT', 30),
    storage: {
      endpoint: str('S3_ENDPOINT', 'http://localhost:9000'),
      region: str('S3_REGION', 'us-east-1'),
      accessKey: str('S3_ACCESS_KEY'),
      secretKey: str('S3_SECRET_KEY'),
      bucket: str('S3_BUCKET_COMPANY_ASSETS', 'company-assets'),
      pathStyle: bool('S3_FORCE_PATH_STYLE', true),
      uploadTtlSeconds: int('COMPANIES_UPLOAD_TTL_SECONDS', 600),
    },
  };
}
