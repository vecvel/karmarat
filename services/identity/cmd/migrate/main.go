// Command migrate applies the identity service's database migrations.
package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/karmarat/platform/packages/goshared/config"
	"github.com/karmarat/platform/packages/goshared/logging"
	"github.com/karmarat/platform/packages/goshared/postgres"
	"github.com/karmarat/platform/services/identity/migrations"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cfg, err := config.LoadBase("identity")
	if err != nil {
		slog.Default().Error("configuration error", slog.Any("error", err))
		os.Exit(1)
	}

	logger := logging.New("identity-migrate", cfg.Environment, cfg.LogLevel)

	pool, err := postgres.Connect(ctx, postgres.Config{URL: cfg.PostgresURL}, logger)
	if err != nil {
		logger.Error("could not connect to the database", slog.Any("error", err))
		os.Exit(1)
	}
	defer pool.Close()

	files, err := postgres.LoadMigrations(migrations.FS, ".")
	if err != nil {
		logger.Error("could not load migrations", slog.Any("error", err))
		os.Exit(1)
	}

	if err := postgres.Migrate(ctx, pool, files, logger); err != nil {
		logger.Error("migration failed", slog.Any("error", err))
		os.Exit(1)
	}

	logger.Info("identity migrations up to date", slog.Int("count", len(files)))
}
