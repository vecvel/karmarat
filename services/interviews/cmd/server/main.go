// Command server runs the interviews service.
//
// This service owns the interview loop and the feedback it produces. The rule it
// exists to keep is that panel feedback stays independent: submitting a
// scorecard and reading the panel's are separate permissions, so an interviewer
// writes their verdict without having seen anyone else's.
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
	"github.com/karmarat/platform/services/interviews/internal/api"
	interviewevents "github.com/karmarat/platform/services/interviews/internal/events"
	"github.com/karmarat/platform/services/interviews/internal/store"
	"github.com/karmarat/platform/services/interviews/migrations"
)

const serviceName = "interviews"

func main() {
	if err := run(); err != nil {
		slog.Default().Error("interviews service stopped", slog.Any("error", err))
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

	/* ---------------------------------------------------------- publishing -- */

	// Events are written to an outbox inside the same transaction as the change
	// that caused them, then relayed here. A publish that happened outside the
	// transaction would either announce a round that rolled back, or lose the
	// cancellation of one that committed — and a stale calendar invite sends a
	// candidate to a meeting nobody is coming to.
	publisher := interviewevents.NewPublisher(st, bus, logger)
	go publisher.Run(ctx, config.Duration("OUTBOX_POLL_INTERVAL", 2*time.Second))

	/* ---------------------------------------------------------- consuming --- */

	consumer := interviewevents.NewConsumer(st, logger)
	subscription, err := bus.Subscribe(ctx, events.SubscribeOptions{
		Durable:  "interviews-projection",
		Subjects: interviewevents.Subjects(),
	}, consumer.Handle)
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
		Addr:    ":" + config.String("INTERVIEWS_HTTP_PORT", "8092"),
		Handler: handler,
		Logger:  logger,
	})
}
