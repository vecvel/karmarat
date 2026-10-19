/**
 * The service's connections to the world: configuration, Postgres, the event
 * bus, object storage, and the health registry that reports on them.
 *
 * Global, so a repository can inject the pool without every feature module
 * re-importing plumbing, and so the process holds exactly one pool and one bus
 * connection rather than one per importing module.
 */
import { Global, Inject, Logger, Module, type OnApplicationShutdown, type Type } from '@nestjs/common';
import {
  EventBus,
  HealthRegistry,
  Public,
  createPool,
  healthCheck,
  healthController,
} from '@karmarat/nestshared';
import { Pool } from 'pg';

import { loadConfig, type CompaniesConfig } from '../config';
import { PG_POOL } from '../companies/companies.repository';
import { EVENT_PUBLISHER, SERVICE_CONFIG } from '../companies/companies.service';
import { Presigner } from '../storage/presigner';

export const EVENT_BUS = Symbol('EVENT_BUS');
export const HEALTH_REGISTRY = Symbol('HEALTH_REGISTRY');

// Module-level rather than a provider because @Module's `controllers` is
// evaluated when the decorator runs, which is before any factory has executed;
// the controller needs the registry instance at that moment. The factories then
// register their checks against this same object as they come up, so /readyz
// reports on whatever actually connected.
const registry = new HealthRegistry();

const HealthController = healthController(registry) as Type<unknown>;

// `AuthorizationGuard` is registered globally and denies by default, so the
// shared health controller — which knows nothing about this platform's
// authorization model and carries no `@Public()` of its own — would answer 401
// to the orchestrator's probes and the service would never be marked ready.
// Applying the decorator to the class here is the same metadata the decorator
// syntax would write, without reaching into nestshared to add it there.
Public()(HealthController);

@Global()
@Module({
  controllers: [HealthController],
  providers: [
    { provide: SERVICE_CONFIG, useFactory: loadConfig },
    {
      provide: PG_POOL,
      inject: [SERVICE_CONFIG],
      useFactory: (config: CompaniesConfig): Pool => {
        const pool = createPool({ url: config.postgresUrl });
        // An idle client erroring is routine — the server closed it, a proxy
        // recycled it — but an unhandled 'error' event on a pg Pool terminates
        // the process, which is far worse than a reconnect.
        pool.on('error', (error) => new Logger('postgres').warn(`idle client error: ${error.message}`));
        registry.register('postgres', healthCheck(pool));
        return pool;
      },
    },
    {
      provide: EVENT_BUS,
      inject: [SERVICE_CONFIG],
      useFactory: async (config: CompaniesConfig): Promise<EventBus> => {
        const logger = new Logger('events');
        const bus = await EventBus.connect(config.natsUrl, config.serviceName, {
          log: (message) => logger.log(message),
          error: (message) => logger.error(message),
        });
        registry.register('bus', bus.healthCheck());
        return bus;
      },
    },
    // The service depends on the narrow publisher interface so a test can hand
    // it a fake; the container binds that interface to the real bus here.
    { provide: EVENT_PUBLISHER, useExisting: EVENT_BUS },
    {
      provide: Presigner,
      inject: [SERVICE_CONFIG],
      useFactory: (config: CompaniesConfig): Presigner | null => {
        try {
          return new Presigner(config.storage);
        } catch (error) {
          // Deliberately not fatal. Bucket credentials are absent on a laptop
          // that only wants to run registration, and taking the whole service
          // down for that would make the missing piece harder to find, not
          // easier. The two upload routes answer 503 instead; everything else
          // works. In production `loadBase` has already refused to start
          // without the platform's required settings.
          new Logger('storage').warn(
            `object storage is not configured, asset uploads will be unavailable: ${(error as Error).message}`,
          );
          return null;
        }
      },
    },
    { provide: HEALTH_REGISTRY, useValue: registry },
  ],
  exports: [SERVICE_CONFIG, PG_POOL, EVENT_BUS, EVENT_PUBLISHER, Presigner, HEALTH_REGISTRY],
})
export class InfrastructureModule implements OnApplicationShutdown {
  private readonly logger = new Logger('shutdown');

  constructor(
    @Inject(PG_POOL) private readonly pool: Pool,
    @Inject(EVENT_BUS) private readonly bus: EventBus,
  ) {}

  /**
   * Nest runs this after the HTTP server has stopped accepting connections and
   * in-flight requests have drained, so closing these cannot cut off a request
   * that is still being served.
   *
   * Each teardown is guarded: a bus that never connected must not stop the pool
   * from closing, or a deploy hangs until the orchestrator kills it.
   */
  async onApplicationShutdown(): Promise<void> {
    await this.bus.close().catch((error: Error) => this.logger.warn(`bus close: ${error.message}`));
    await this.pool.end().catch((error: Error) => this.logger.warn(`pool close: ${error.message}`));
  }
}
