/**
 * Starts tracing before anything else in the process.
 *
 * The OpenTelemetry instrumentations patch `http`, `pg` and the rest as they are
 * first required, so a module loaded ahead of `startTracing` is a module that
 * never reports a span. `main.ts` imports this file first for that reason
 * alone, and this file imports nothing but the config it needs.
 */
import { startTracing } from '@karmarat/nestshared';

import { SERVICE_NAME, loadConfig } from './config';

/** Loaded here so the rest of the process shares one parsed configuration. */
export const config = loadConfig();

export const tracing = startTracing({
  serviceName: SERVICE_NAME,
  environment: config.environment,
  endpoint: config.otlpEndpoint,
});
