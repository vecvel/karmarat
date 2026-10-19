package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"log/slog"
	"path"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Migration is a single versioned SQL file.
type Migration struct {
	Version  string
	Name     string
	SQL      string
	Checksum string
}

// LoadMigrations reads `NNN_name.sql` files from an embedded filesystem.
func LoadMigrations(fsys fs.FS, dir string) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("migrate: read %s: %w", dir, err)
	}

	migrations := make([]Migration, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}

		content, err := fs.ReadFile(fsys, path.Join(dir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("migrate: read %s: %w", entry.Name(), err)
		}

		base := strings.TrimSuffix(entry.Name(), ".sql")
		version, name, found := strings.Cut(base, "_")
		if !found {
			return nil, fmt.Errorf("migrate: %q must be named <version>_<name>.sql", entry.Name())
		}

		sum := sha256.Sum256(content)
		migrations = append(migrations, Migration{
			Version:  version,
			Name:     name,
			SQL:      string(content),
			Checksum: hex.EncodeToString(sum[:]),
		})
	}

	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].Version < migrations[j].Version
	})

	return migrations, nil
}

// Migrate applies any migrations the database has not yet run.
//
// Each migration runs inside a transaction together with the bookkeeping row, so
// a failure leaves the schema exactly as it was rather than half-applied. An
// advisory lock keeps concurrent deploys from racing each other.
func Migrate(ctx context.Context, pool *pgxpool.Pool, migrations []Migration, logger *slog.Logger) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("migrate: acquire connection: %w", err)
	}
	defer conn.Release()

	const lockID = 4_827_113_905
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", lockID); err != nil {
		return fmt.Errorf("migrate: acquire advisory lock: %w", err)
	}
	defer func() {
		_, _ = conn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", lockID)
	}()

	if _, err := conn.Exec(ctx, `
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
		END $$;`); err != nil {
		return fmt.Errorf("migrate: create bookkeeping table: %w", err)
	}

	rows, err := conn.Query(ctx, "SELECT version, name, checksum FROM schema_migrations")
	if err != nil {
		return fmt.Errorf("migrate: read applied migrations: %w", err)
	}

	applied := make(map[string]string)
	for rows.Next() {
		var version, name, checksum string
		if err := rows.Scan(&version, &name, &checksum); err != nil {
			rows.Close()
			return fmt.Errorf("migrate: scan applied migration: %w", err)
		}
		applied[version+"_"+name] = checksum
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("migrate: read applied migrations: %w", err)
	}

	for _, migration := range migrations {
		migrationKey := migration.Version + "_" + migration.Name
		if checksum, done := applied[migrationKey]; done {
			// An edited migration would silently diverge environments; refuse it.
			if checksum != migration.Checksum {
				return fmt.Errorf(
					"migrate: migration %s_%s was modified after it was applied (expected checksum %s, found %s)",
					migration.Version, migration.Name, checksum, migration.Checksum,
				)
			}
			continue
		}

		tx, err := conn.Begin(ctx)
		if err != nil {
			return fmt.Errorf("migrate: begin %s: %w", migration.Version, err)
		}

		if _, err := tx.Exec(ctx, migration.SQL); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("migrate: apply %s_%s: %w", migration.Version, migration.Name, err)
		}

		if _, err := tx.Exec(ctx,
			"INSERT INTO schema_migrations (version, name, checksum) VALUES ($1, $2, $3)",
			migration.Version, migration.Name, migration.Checksum,
		); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("migrate: record %s: %w", migration.Version, err)
		}

		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("migrate: commit %s: %w", migration.Version, err)
		}

		if logger != nil {
			logger.Info("migration applied",
				slog.String("version", migration.Version),
				slog.String("name", migration.Name),
			)
		}
	}

	return nil
}
