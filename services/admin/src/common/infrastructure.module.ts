/**
 * What this service connects to, and what reports on those connections.
 *
 * The pool and the configuration come from {@link DatabaseModule}; this module
 * adds the event bus — the only thing that ever *writes* to this database — and
 * the `/healthz` and `/readyz` endpoints an orchestrator probes.
 *
 * Note the distinction between those endpoints and `GET /v1/admin/health`. The
 * ones here answer "should this process receive traffic?" and are unauthenticated
 * because a probe carries no token. The admin one answers "is the platform
 * well?" for a human operator, and is behind a platform principal like every
 * other console route.
 */
import {
  Global,
  Inject,
  Logger,
  Module,
  type OnApplicationShutdown,
  type OnModuleInit,
  type Type,
} from '@nestjs/common';
import { EventBus, HealthRegistry, healthCheck, healthController } from '@karmarat/nestshared';
import type { Pool } from 'pg';

import type { AdminConfig } from '../config';
import { DatabaseModule } from './database.module';
import { ADMIN_CONFIG, EVENT_BUS, HEALTH_REGISTRY, PG_POOL } from './tokens';

// Module level rather than a provider: @Module's `controllers` is evaluated when
// the decorator runs, which is before any factory has executed, so the
// controller needs this instance at that moment. The checks are registered
// against the same object as the dependencies come up, so /readyz reports on
// whatever actually connected.
const registry = new HealthRegistry();

const HealthController = healthController(registry) as Type<unknown>;

@Global()
@Module({
  imports: [DatabaseModule],
  controllers: [HealthController],
  providers: [
    {
      provide: EVENT_BUS,
      inject: [ADMIN_CONFIG],
      useFactory: async (config: AdminConfig): Promise<EventBus> => {
        const logger = new Logger('events');
        const bus = await EventBus.connect(config.natsUrl, config.serviceName, {
          log: (message) => logger.log(message),
          error: (message) => logger.error(message),
        });
        registry.register('bus', bus.healthCheck());
        return bus;
      },
    },
    { provide: HEALTH_REGISTRY, useValue: registry },
  ],
  exports: [DatabaseModule, EVENT_BUS, HEALTH_REGISTRY],
})
export class InfrastructureModule implements OnModuleInit, OnApplicationShutdown {
  private readonly logger = new Logger('shutdown');

  constructor(
    @Inject(PG_POOL) private readonly pool: Pool,
    @Inject(EVENT_BUS) private readonly bus: EventBus,
  ) {}

  onModuleInit(): void {
    registry.register('postgres', healthCheck(this.pool));
  }

  /**
   * Nest runs this after in-flight requests have drained.
   *
   * Only the bus is closed here — the pool belongs to {@link DatabaseModule},
   * which drains it in its own shutdown hook. Closing it twice would make the
   * second call throw on an already-ended pool and turn a clean shutdown into a
   * logged error.
   */
  async onApplicationShutdown(): Promise<void> {
    await this.bus.close().catch((error: Error) => this.logger.warn(`bus close: ${error.message}`));
  }
}
