/**
 * Support service configuration.
 *
 * Everything general — Postgres, NATS, the port, the internal token — comes from
 * `loadBase` so this service reads the same .env the Go services do. Only the
 * settings that are genuinely this service's own are declared here.
 */
import { bool, int, loadBase, str, type BaseConfig } from '@karmarat/nestshared';

export const SERVICE_NAME = 'support';
export const DEFAULT_PORT = 8090;

export interface StorageConfig {
  endpoint: string;
  region: string;
  accessKey: string;
  secretKey: string;
  bucket: string;
  forcePathStyle: boolean;
  /** How long a presigned upload URL stays valid. */
  presignTtlSeconds: number;
  /** Largest attachment we will sign for, in bytes. */
  maxUploadBytes: number;
}

export interface SupportConfig extends BaseConfig {
  storage: StorageConfig;
}

export function loadConfig(): SupportConfig {
  const base = loadBase(SERVICE_NAME, DEFAULT_PORT);

  return {
    ...base,
    // loadBase falls back to a shared DATABASE_URL, which in a monorepo where
    // the web app also reads DATABASE_URL would point this service's migrations
    // at somebody else's database. A service-specific override wins so that
    // cannot happen by accident.
    postgresUrl: str('SUPPORT_DATABASE_URL', base.postgresUrl),
    storage: {
      endpoint: str('S3_ENDPOINT', 'http://localhost:9000'),
      region: str('S3_REGION', 'us-east-1'),
      accessKey: str('S3_ACCESS_KEY', 'karmarat'),
      secretKey: str('S3_SECRET_KEY', 'karmarat-secret'),
      // Support attachments are arbitrary customer documents, so they belong in
      // the private documents bucket rather than alongside public company assets.
      bucket: str('S3_BUCKET_DOCUMENTS', 'documents'),
      forcePathStyle: bool('S3_FORCE_PATH_STYLE', true),
      presignTtlSeconds: parseDuration(str('S3_PRESIGN_TTL', '15m'), 900),
      maxUploadBytes: int('SUPPORT_MAX_UPLOAD_BYTES', 25 * 1024 * 1024),
    },
  };
}

/**
 * Parses a Go-style duration ("15m", "30s", "1h").
 *
 * The same S3_PRESIGN_TTL value is read by the Go services, where it is a
 * time.Duration. Reading it as a bare number here would mean either a second
 * environment variable or a value that means one thing to half the platform.
 */
export function parseDuration(value: string, fallbackSeconds: number): number {
  const match = /^(\d+)(ms|s|m|h)$/.exec(value.trim());
  if (!match) {
    const plain = Number(value);
    return Number.isInteger(plain) && plain > 0 ? plain : fallbackSeconds;
  }

  const amount = Number(match[1]);
  switch (match[2]) {
    case 'ms':
      return Math.max(1, Math.round(amount / 1000));
    case 's':
      return amount;
    case 'm':
      return amount * 60;
    case 'h':
      return amount * 3600;
    default:
      return fallbackSeconds;
  }
}
