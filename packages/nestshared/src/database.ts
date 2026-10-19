/**
 * Postgres connection and the migration runner.
 *
 * The runner is deliberately compatible with `packages/goshared/postgres`: same
 * `schema_migrations` table, same `NNN_name.sql` convention, same SHA-256
 * checksum, same advisory lock id. A Go service and a Node service therefore
 * behave identically at boot, and an operator does not have to remember which
 * runtime a service is written in to reason about its schema.
 */
import { createHash } from 'node:crypto';
import { readFile, readdir } from 'node:fs/promises';
import { join } from 'node:path';
import { Pool, type PoolClient, type PoolConfig } from 'pg';

/** Must match `lockID` in packages/goshared/postgres/migrate.go. */
const MIGRATION_LOCK_ID = 4_827_113_905;

export interface DatabaseOptions {
  url: string;
  maxConnections?: number;
  statementTimeoutMs?: number;
}

export function createPool(options: DatabaseOptions): Pool {
  const config: PoolConfig = {
    connectionString: options.url,
    max: options.maxConnections ?? 20,
    idleTimeoutMillis: 30_000,
    connectionTimeoutMillis: 10_000,
    // A query with no ceiling can pin a connection for the life of the process
    // and take the pool down with it.
    statement_timeout: options.statementTimeoutMs ?? 30_000,
  };
  return new Pool(config);
}

export function healthCheck(pool: Pool): () => Promise<void> {
  return async () => {
    const client = await pool.connect();
    try {
      await client.query('SELECT 1');
    } finally {
      client.release();
    }
  };
}

interface Migration {
  version: string;
  name: string;
  sql: string;
  checksum: string;
}

async function loadMigrations(dir: string): Promise<Migration[]> {
  const entries = (await readdir(dir)).filter((entry) => entry.endsWith('.sql'));

  const migrations = await Promise.all(
    entries.map(async (entry): Promise<Migration> => {
      const content = await readFile(join(dir, entry), 'utf8');
      const base = entry.replace(/\.sql$/, '');
      const separator = base.indexOf('_');
      if (separator === -1) {
        throw new Error(`migrate: "${entry}" must be named <version>_<name>.sql`);
      }
      return {
        version: base.slice(0, separator),
        name: base.slice(separator + 1),
        sql: content,
        checksum: createHash('sha256').update(content).digest('hex'),
      };
    }),
  );

  return migrations.sort((a, b) => a.version.localeCompare(b.version));
}

/**
 * Applies any migration the database has not run.
 *
 * Each migration runs in a transaction alongside its bookkeeping row, so a
 * failure leaves the schema as it was rather than half-applied. The advisory
 * lock keeps two replicas starting at once from racing each other.
 */
export async function migrate(
  pool: Pool,
  migrationsDir: string,
  logger: { log: (message: string) => void } = console,
): Promise<void> {
  const migrations = await loadMigrations(migrationsDir);
  const client: PoolClient = await pool.connect();

  try {
    await client.query('SELECT pg_advisory_lock($1)', [MIGRATION_LOCK_ID]);

    await client.query(`
      CREATE TABLE IF NOT EXISTS schema_migrations (
        version     text NOT NULL,
        name        text NOT NULL,
        checksum    text NOT NULL,
        applied_at  timestamptz NOT NULL DEFAULT now(),
        PRIMARY KEY (version, name)
      );
      DO $$
      BEGIN
        ALTER TABLE schema_migrations DROP CONSTRAINT IF EXISTS schema_migrations_pkey;
        ALTER TABLE schema_migrations ADD PRIMARY KEY (version, name);
      EXCEPTION
        WHEN others THEN NULL;
      END $$;`);

    const { rows } = await client.query<{ version: string; name: string; checksum: string }>(
      'SELECT version, name, checksum FROM schema_migrations',
    );
    const applied = new Map(rows.map((row) => [`${row.version}_${row.name}`, row.checksum]));

    for (const migration of migrations) {
      const migrationKey = `${migration.version}_${migration.name}`;
      const seen = applied.get(migrationKey);
      if (seen !== undefined) {
        // An edited migration means two environments silently hold different
        // schemas; refuse to start rather than discover that in production.
        if (seen !== migration.checksum) {
          throw new Error(
            `migrate: migration ${migration.version}_${migration.name} was modified after it was applied ` +
              `(expected checksum ${seen}, found ${migration.checksum})`,
          );
        }
        continue;
      }

      await client.query('BEGIN');
      try {
        await client.query(migration.sql);
        await client.query('INSERT INTO schema_migrations (version, name, checksum) VALUES ($1, $2, $3)', [
          migration.version,
          migration.name,
          migration.checksum,
        ]);
        await client.query('COMMIT');
      } catch (error) {
        await client.query('ROLLBACK');
        throw new Error(`migrate: ${migration.version}_${migration.name} failed: ${(error as Error).message}`);
      }

      logger.log(`applied migration ${migration.version}_${migration.name}`);
    }
  } finally {
    await client.query('SELECT pg_advisory_unlock($1)', [MIGRATION_LOCK_ID]).catch(() => undefined);
    client.release();
  }
}

/**
 * Runs a function inside a transaction.
 *
 * Use it wherever two writes must both land — an application row and its
 * pipeline event, an invoice and its subscription change.
 */
export async function withTransaction<T>(pool: Pool, fn: (client: PoolClient) => Promise<T>): Promise<T> {
  const client = await pool.connect();
  try {
    await client.query('BEGIN');
    const result = await fn(client);
    await client.query('COMMIT');
    return result;
  } catch (error) {
    await client.query('ROLLBACK').catch(() => undefined);
    throw error;
  } finally {
    client.release();
  }
}
