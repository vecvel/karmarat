/**
 * Everything this service connects to: Postgres, the event bus, object storage,
 * and the health registry that reports on the first two.
 *
 * Global, so a repository injects the pool without every feature module
 * re-importing plumbing, and so the process holds exactly one pool and one bus
 * connection rather than one per importing module.
 */
import {
  Global,
  Inject,
  Logger,
  Module,
  type OnApplicationShutdown,
  type Type,
} from '@nestjs/common';
import {
  EventBus,
  HealthRegistry,
  createPool,
  healthCheck,
  healthController,
} from '@karmarat/nestshared';
import { Pool } from 'pg';

import { loadConfig, type SupportConfig } from '../config';
import { EVENT_BUS, HEALTH_REGISTRY, PG_POOL, STORAGE_PRESIGNER, SUPPORT_CONFIG } from '../tokens';
import { StoragePresigner } from './storage';

// Module level rather than a provider: @Module's `controllers` is evaluated when
// the decorator runs, which is before any factory has executed, and the health
// controller needs the registry instance at that moment. The factories below
// then register their checks against this same object as they come up, so
// /readyz reports on whatever actually connected.
const registry = new HealthRegistry();

const HealthController = healthController(registry) as Type<unknown>;

@Global()
@Module({
  controllers: [HealthController],
  providers: [
    {
      provide: SUPPORT_CONFIG,
      useFactory: loadConfig,
    },
    {
      provide: PG_POOL,
      inject: [SUPPORT_CONFIG],
      useFactory: (config: SupportConfig): Pool => {
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
      inject: [SUPPORT_CONFIG],
      useFactory: async (config: SupportConfig): Promise<EventBus> => {
        const logger = new Logger('events');
        const bus = await EventBus.connect(config.natsUrl, config.serviceName, {
          log: (message) => logger.log(message),
          error: (message) => logger.error(message),
        });
        registry.register('bus', bus.healthCheck());
        return bus;
      },
    },
    {
      provide: STORAGE_PRESIGNER,
      inject: [SUPPORT_CONFIG],
      useFactory: (config: SupportConfig): StoragePresigner => new StoragePresigner(config.storage),
    },
    {
      provide: HEALTH_REGISTRY,
      useValue: registry,
    },
  ],
  exports: [SUPPORT_CONFIG, PG_POOL, EVENT_BUS, STORAGE_PRESIGNER, HEALTH_REGISTRY],
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
   * still being served.
   *
   * Each teardown is guarded: a bus that never connected must not stop the pool
   * from closing, or a deploy hangs until the orchestrator kills it.
   */
  async onApplicationShutdown(): Promise<void> {
    await this.bus.close().catch((error: Error) => this.logger.warn(`bus close: ${error.message}`));
    await this.pool.end().catch((error: Error) => this.logger.warn(`pool close: ${error.message}`));
  }
}
