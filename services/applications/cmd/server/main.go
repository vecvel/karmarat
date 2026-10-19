// Command server runs the applications service.
//
// This service owns the rule the whole product depends on: a candidate applies
// to a given job exactly once. That rule lives in a unique index rather than in
// this process, so it survives concurrent submits and restarts alike.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/karmarat/platform/packages/goshared/config"
	"github.com/karmarat/platform/packages/goshared/events"
	"github.com/karmarat/platform/packages/goshared/httpx"
	"github.com/karmarat/platform/packages/goshared/logging"
	"github.com/karmarat/platform/packages/goshared/observability"
	"github.com/karmarat/platform/packages/goshared/postgres"
	"github.com/karmarat/platform/services/applications/internal/api"
	"github.com/karmarat/platform/services/applications/internal/candidates"
	appevents "github.com/karmarat/platform/services/applications/internal/events"
	"github.com/karmarat/platform/services/applications/internal/jobs"
	"github.com/karmarat/platform/services/applications/internal/store"
	"github.com/karmarat/platform/services/applications/migrations"
)

const serviceName = "applications"

func main() {
	if err := run(); err != nil {
		slog.Default().Error("applications service stopped", slog.Any("error", err))
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.LoadBase(serviceName)
	if err != nil {
		return err
	}

	logger := logging.New(serviceName, cfg.Environment, cfg.LogLevel)
	slog.SetDefault(logger)

	shutdownTracing, err := observability.Init(ctx, observability.Config{
		ServiceName: serviceName,
		Environment: cfg.Environment,
		Endpoint:    config.String("OTEL_EXPORTER_OTLP_ENDPOINT", ""),
	}, logger)
	if err != nil {
		return err
	}
	defer func() { _ = shutdownTracing(context.WithoutCancel(ctx)) }()

	pool, err := postgres.Connect(ctx, postgres.Config{URL: cfg.PostgresURL}, logger)
	if err != nil {
		return err
	}
	defer pool.Close()

	files, err := postgres.LoadMigrations(migrations.FS, ".")
	if err != nil {
		return err
	}
	if err := postgres.Migrate(ctx, pool, files, logger); err != nil {
		return err
	}

	bus, err := events.Connect(ctx, cfg.NATSURL, serviceName, logger)
	if err != nil {
		return err
	}
	defer func() { _ = bus.Close() }()

	st := store.New(pool)
	internalToken := config.String("INTERNAL_SERVICE_TOKEN", "")

	/* ---------------------------------------------------------- publishing -- */

	// Events are written to an outbox inside the same transaction as the change
	// that caused them, then relayed here. A publish that happened outside the
	// transaction would either announce a rejection that rolled back, or lose one
	// that committed.
	publisher := appevents.NewPublisher(st, bus, logger)
	go publisher.Run(ctx, config.Duration("OUTBOX_POLL_INTERVAL", 2*time.Second))

	/* ---------------------------------------------------------- consuming --- */

	consumer := appevents.NewConsumer(st, logger)
	subscription, err := bus.Subscribe(ctx, events.SubscribeOptions{
		Durable:  "applications-projection",
		Subjects: appevents.Subjects(),
	}, consumer.Handle)
	if err != nil {
		return err
	}
	defer subscription.Stop()

	/* ---------------------------------------------------------------- http -- */

	restAPI := api.New(api.Config{
		Store: st,
		Jobs: jobs.New(jobs.Config{
			BaseURL: config.String("JOBS_URL", "http://localhost:8085"),
			Token:   internalToken,
			Timeout: config.Duration("INTERNAL_CALL_TIMEOUT", 5*time.Second),
		}),
		Candidates: candidates.New(candidates.Config{
			BaseURL: config.String("CANDIDATES_URL", "http://localhost:8087"),
			Token:   internalToken,
		}),
		Logger: logger,
	})

	mux := http.NewServeMux()
	mux.Handle("/", restAPI.Routes())
	mux.Handle("/healthz", httpx.Health(nil))
	mux.Handle("/readyz", httpx.Health(map[string]func(context.Context) error{
		"postgres": postgres.HealthCheck(pool),
		"nats":     bus.HealthCheck(),
	}))

	handler := httpx.RequestID(
		httpx.Recoverer(
			httpx.SecurityHeaders(
				httpx.Logger(logger)(
					observability.Middleware(serviceName)(mux),
				),
			),
		),
	)

	return httpx.Serve(ctx, httpx.ServerConfig{
		Addr:    ":" + config.String("APPLICATIONS_HTTP_PORT", "8086"),
		Handler: handler,
		Logger:  logger,
	})
}
