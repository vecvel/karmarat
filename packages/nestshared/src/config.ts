/**
 * Environment configuration, matching `packages/goshared/config`.
 *
 * Connection strings are derived from the same `POSTGRES_` and `REDIS_`
 * variables the Go services read, so one .env drives the whole platform rather
 * than each runtime inventing its own names.
 */
export function str(key: string, fallback = ''): string {
  const value = process.env[key];
  return value === undefined || value === '' ? fallback : value;
}

export function int(key: string, fallback: number): number {
  const value = process.env[key];
  if (value === undefined || value === '') return fallback;
  const parsed = Number(value);
  return Number.isInteger(parsed) ? parsed : fallback;
}

export function bool(key: string, fallback: boolean): boolean {
  const value = process.env[key];
  if (value === undefined || value === '') return fallback;
  return value === 'true' || value === '1' || value === 'yes';
}

export function mustStr(key: string): string {
  const value = process.env[key];
  if (value === undefined || value === '') {
    throw new Error(`config: ${key} is required`);
  }
  return value;
}

export interface BaseConfig {
  serviceName: string;
  environment: string;
  hostname: string;
  logLevel: string;
  postgresUrl: string;
  redisUrl: string;
  natsUrl: string;
  otlpEndpoint: string;
  internalToken: string;
  port: number;
}

export function loadBase(serviceName: string, defaultPort: number): BaseConfig {
  const environment = str('PLATFORM_ENV', 'development');

  const config: BaseConfig = {
    serviceName,
    environment,
    hostname: str('PLATFORM_HOSTNAME', 'karmarat.local'),
    logLevel: str('LOG_LEVEL', 'info'),
    postgresUrl: str('DATABASE_URL', postgresUrl(str('POSTGRES_DB', serviceName))),
    redisUrl: str('REDIS_URL', redisUrl()),
    natsUrl: str('NATS_URL', 'nats://localhost:4222'),
    otlpEndpoint: str('OTEL_EXPORTER_OTLP_ENDPOINT'),
    internalToken: str('INTERNAL_SERVICE_TOKEN'),
    port: int(`${serviceName.toUpperCase()}_HTTP_PORT`, defaultPort),
  };

  if (environment === 'production') {
    // Failing at boot beats discovering at 3am that a service has been running
    // against a default password.
    const missing: string[] = [];
    if (!process.env.DATABASE_URL && !process.env.POSTGRES_PASSWORD) missing.push('DATABASE_URL or POSTGRES_PASSWORD');
    if (!process.env.REDIS_URL && !process.env.REDIS_PASSWORD) missing.push('REDIS_URL or REDIS_PASSWORD');
    if (!process.env.INTERNAL_SERVICE_TOKEN) missing.push('INTERNAL_SERVICE_TOKEN');
    if (missing.length) {
      throw new Error(`config: missing required production settings: ${missing.join(', ')}`);
    }
  }

  return config;
}

function postgresUrl(database: string): string {
  const user = str('POSTGRES_USER', 'karmarat');
  const password = str('POSTGRES_PASSWORD', 'karmarat');
  const host = str('POSTGRES_HOST', 'localhost');
  const port = str('POSTGRES_PORT', '5432');
  return `postgres://${user}:${encodeURIComponent(password)}@${host}:${port}/${database}?sslmode=disable`;
}

function redisUrl(): string {
  const password = str('REDIS_PASSWORD');
  const host = str('REDIS_HOST', 'localhost');
  const port = str('REDIS_PORT', '6379');
  return password ? `redis://:${encodeURIComponent(password)}@${host}:${port}` : `redis://${host}:${port}`;
}
