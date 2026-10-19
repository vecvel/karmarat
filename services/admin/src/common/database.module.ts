/**
 * The connection pool and the configuration, as injectable singletons.
 *
 * Global so a repository three modules deep does not have to thread an import
 * chain back to here; there is exactly one pool in the process and pretending
 * otherwise only adds ceremony.
 */
import { Global, Inject, Logger, Module, type OnApplicationShutdown } from '@nestjs/common';
import { createPool } from '@karmarat/nestshared';
import { Pool } from 'pg';

import { loadConfig, type AdminConfig } from '../config';
import { ADMIN_CONFIG, PG_POOL } from './tokens';

@Global()
@Module({
  providers: [
    { provide: ADMIN_CONFIG, useFactory: (): AdminConfig => loadConfig() },
    {
      provide: PG_POOL,
      inject: [ADMIN_CONFIG],
      useFactory: (config: AdminConfig): Pool => {
        const pool = createPool({
          url: config.postgresUrl,
          // A console page runs several aggregates at once and an export scans;
          // the ceiling keeps one wedged query from starving the others without
          // letting a runaway report hold a connection forever.
          statementTimeoutMs: 20_000,
        });
        // An idle client erroring is routine — the server closed it, a proxy
        // recycled it — but an unhandled 'error' event on a pg Pool terminates
        // the process, which is far worse than a reconnect.
        pool.on('error', (error) => new Logger('postgres').warn(`idle client error: ${error.message}`));
        return pool;
      },
    },
  ],
  exports: [ADMIN_CONFIG, PG_POOL],
})
export class DatabaseModule implements OnApplicationShutdown {
  constructor(@Inject(PG_POOL) private readonly pool: Pool) {}

  /** Drains the pool so shutdown is not held open by an idle connection. */
  async onApplicationShutdown(): Promise<void> {
    await this.pool.end().catch(() => undefined);
  }
}
