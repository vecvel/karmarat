/**
 * Starts tracing before anything else in the process loads.
 *
 * `main.ts` imports this file first and imports nothing above it. The OpenTelemetry
 * SDK instruments modules as they are required, so a module that is already in
 * the require cache when `startTracing` runs is a module that will never be
 * patched — pg queries and outbound HTTP calls would silently stop appearing in
 * traces.
 *
 * That is also why this file does not `import { startTracing } from
 * '@karmarat/nestshared'`: the package's entry point re-exports `database`
 * and `events`, so importing it would pull `pg` and `nats` into the require
 * cache before the SDK had a chance to hook them. The package's `exports` map
 * publishes only `.`, so there is no subpath to import instead; resolving the
 * entry point and requiring the sibling file by absolute path reaches the one
 * module we want without loading the rest.
 *
 * REPORTED UPSTREAM: nestshared should publish an `./observability` subpath so
 * a service can import the tracer without this indirection.
 */
import { createRequire } from 'node:module';
import { dirname, join } from 'node:path';
import type { startTracing as StartTracing } from '@karmarat/nestshared';

const localRequire = createRequire(__filename);
const entryPoint = localRequire.resolve('@karmarat/nestshared');
const { startTracing } = localRequire(join(dirname(entryPoint), 'observability.js')) as {
  startTracing: typeof StartTracing;
};

export const tracing = startTracing({
  serviceName: 'admin',
  environment: process.env.PLATFORM_ENV ?? 'development',
  endpoint: process.env.OTEL_EXPORTER_OTLP_ENDPOINT ?? '',
});
