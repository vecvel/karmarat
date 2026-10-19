// Tracing first, before any other import.
//
// The OpenTelemetry instrumentations patch `http`, `pg` and the rest as those
// modules are first required. A module loaded ahead of this line is a module
// that never reports a span, so this import must stay at the top and must stay
// free of side effects of its own beyond starting the SDK.
import { config, tracing } from './tracing';

import { Logger, ValidationPipe } from '@nestjs/common';
import { NestFactory } from '@nestjs/core';
import { ProblemFilter, migrate } from '@karmarat/nestshared';
import { join } from 'node:path';
import type { Pool } from 'pg';

import { AppModule } from './app.module';
import { PG_POOL } from './common/infrastructure.module';

async function bootstrap(): Promise<void> {
  const logger = new Logger('bootstrap');

  const app = await NestFactory.create(AppModule, { bufferLogs: false });

  // Every error leaves as RFC 9457 problem+json, including the ones Nest throws
  // before a handler runs. Without this a validation failure would come back in
  // Nest's own shape and a client would have to parse two error formats.
  app.useGlobalFilters(new ProblemFilter());

  app.useGlobalPipes(
    new ValidationPipe({
      // Strip what is not on the DTO, then refuse it. Stripping alone would let
      // a client PATCH `state` and receive 200 having changed nothing, which
      // reads as success; the 422 says plainly that the field is not theirs.
      whitelist: true,
      forbidNonWhitelisted: true,
      transform: true,
      transformOptions: { enableImplicitConversion: false },
    }),
  );

  // Lets Nest run onApplicationShutdown on SIGTERM, which is what closes the
  // pool and drains the bus after in-flight requests have finished.
  app.enableShutdownHooks();

  // Migrating before the port opens keeps a deploy from serving traffic against
  // a schema it does not expect. The files are read from disk rather than
  // compiled in, so `dist` and `src` both resolve to the same directory.
  await migrate(app.get<Pool>(PG_POOL), join(__dirname, '..', 'migrations'), {
    log: (message) => logger.log(message),
  });

  await app.listen(config.port);
  logger.log(`${config.serviceName} listening on :${config.port}`);
}

bootstrap().catch(async (error: Error) => {
  // The logger may not exist yet — a configuration error throws before Nest is
  // created — so this writes plainly and exits non-zero rather than leaving a
  // process alive with no server on it.
  console.error(`subscriptions service failed to start: ${error.message}`);
  await tracing.shutdown().catch(() => undefined);
  process.exit(1);
});
