// Command server runs the audit service.
//
// The service writes nothing of its own. It subscribes to every subject on the
// platform stream, records what it sees, and serves two read surfaces over it:
// a company's own trail and, for platform staff, the trail across all of them.
//
// Because the recorder is the only writer, a consumer that has stopped is not a
// degraded feature — it is a trail that is quietly missing the period nobody has
// looked at yet. That is why readiness checks the bus as well as the database.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/karmarat/platform/packages/goshared/config"
	"github.com/karmarat/platform/packages/goshared/events"
	"github.com/karmarat/platform/packages/goshared/httpx"
	"github.com/karmarat/platform/packages/goshared/logging"
	"github.com/karmarat/platform/packages/goshared/observability"
	"github.com/karmarat/platform/packages/goshared/postgres"
	"github.com/karmarat/platform/services/audit/internal/api"
	auditevents "github.com/karmarat/platform/services/audit/internal/events"
	"github.com/karmarat/platform/services/audit/internal/store"
	"github.com/karmarat/platform/services/audit/migrations"
)

const serviceName = "audit"

func main() {
	if err := run(); err != nil {
		slog.Default().Error("audit service stopped", slog.Any("error", err))
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

	/* ---------------------------------------------------------- consuming --- */

	// One durable, shared by every replica, filtering on the whole namespace.
	// The consumer is started before the HTTP server so a deploy does not serve
	// a trail it is not yet filling.
	recorder := auditevents.NewRecorder(st, logger)
	subscription, err := bus.Subscribe(ctx, events.SubscribeOptions{
		Durable:  auditevents.DurableName,
		Subjects: auditevents.Subjects(),
	}, recorder.Handle)
	if err != nil {
		return err
	}
	defer subscription.Stop()

	/* ---------------------------------------------------------------- http -- */

	restAPI := api.New(api.Config{
		Store:  st,
		Logger: logger,
	})

	mux := http.NewServeMux()
	mux.Handle("/", restAPI.Routes())
	mux.Handle("/healthz", httpx.Health(nil))
	mux.Handle("/readyz", httpx.Health(map[string]func(context.Context) error{
		"postgres": postgres.HealthCheck(pool),
		// The bus is a readiness dependency here in a way it is not elsewhere: a
		// service that can still answer queries but has stopped consuming keeps
		// returning a trail that looks complete while it silently stops growing.
		// Failing readiness takes it out of rotation instead.
		"nats": bus.HealthCheck(),
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
		Addr:    ":" + config.String("AUDIT_HTTP_PORT", "8094"),
		Handler: handler,
		Logger:  logger,
	})
}
