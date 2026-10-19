// Command server runs the offers service.
//
// This service owns the one record in the platform that is both a commitment and
// a secret: what a company offered a candidate. Two rules shape everything here.
// The money is gated by `offers.view_compensation` rather than by the offer's
// own permission, so a coordinator can run the process without seeing salaries;
// and the person who submits a package is not the person who approves it, so an
// approval records an actual second opinion.
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
	"github.com/karmarat/platform/services/offers/internal/api"
	offerevents "github.com/karmarat/platform/services/offers/internal/events"
	"github.com/karmarat/platform/services/offers/internal/expiry"
	"github.com/karmarat/platform/services/offers/internal/store"
	"github.com/karmarat/platform/services/offers/migrations"
)

const serviceName = "offers"

func main() {
	if err := run(); err != nil {
		slog.Default().Error("offers service stopped", slog.Any("error", err))
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
	// transaction would either announce an offer that rolled back or lose the
	// acceptance of one that committed.
	publisher := offerevents.NewPublisher(st, bus, logger)
	go publisher.Run(ctx, config.Duration("OUTBOX_POLL_INTERVAL", 2*time.Second))

	/* ---------------------------------------------------------- consuming --- */

	// The applications service owns the candidate's name and the job title; this
	// service keeps a copy so a list renders in one query, and this consumer is
	// what stops that copy from going stale. It also closes offers left
	// outstanding on an application that ended.
	consumer := offerevents.NewConsumer(st, logger)
	subscription, err := bus.Subscribe(ctx, events.SubscribeOptions{
		Durable:  "offers-projection",
		Subjects: offerevents.Subjects(),
	}, consumer.Handle)
	if err != nil {
		return err
	}
	defer subscription.Stop()

	/* ------------------------------------------------------------- expiry --- */

	go expiry.New(st, logger).Run(ctx, config.Duration("OFFER_EXPIRY_INTERVAL", 5*time.Minute))

	/* ---------------------------------------------------------------- http -- */

	restAPI := api.New(api.Config{
		Store:           st,
		Logger:          logger,
		DefaultCurrency: config.String("OFFERS_DEFAULT_CURRENCY", "USD"),
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
		Addr:    ":" + config.String("OFFERS_HTTP_PORT", "8093"),
		Handler: handler,
		Logger:  logger,
	})
}
