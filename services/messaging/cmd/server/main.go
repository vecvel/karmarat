// Command server runs the messaging service.
//
// One process serves two audiences that must never see each other's view of a
// thread: a recruiter on the company portal and the candidate they are writing
// to on the jobs portal. They are separated by principal type at the route, by a
// different filter in every query — tenant on one side, account id on the other
// — and by a different response shape, so no single forgotten check exposes one
// side to the other.
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
	"github.com/karmarat/platform/packages/goshared/redisx"
	"github.com/karmarat/platform/services/messaging/internal/api"
	"github.com/karmarat/platform/services/messaging/internal/candidates"
	messagingevents "github.com/karmarat/platform/services/messaging/internal/events"
	"github.com/karmarat/platform/services/messaging/internal/store"
	"github.com/karmarat/platform/services/messaging/migrations"
)

const serviceName = "messaging"

func main() {
	if err := run(); err != nil {
		slog.Default().Error("messaging service stopped", slog.Any("error", err))
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

	// Migrating at boot keeps a deployment from serving traffic against a schema
	// it does not expect.
	files, err := postgres.LoadMigrations(migrations.FS, ".")
	if err != nil {
		return err
	}
	if err := postgres.Migrate(ctx, pool, files, logger); err != nil {
		return err
	}

	redisClient, err := redisx.Connect(ctx, cfg.RedisURL, logger)
	if err != nil {
		return err
	}
	defer func() { _ = redisClient.Close() }()

	bus, err := events.Connect(ctx, cfg.NATSURL, serviceName, logger)
	if err != nil {
		return err
	}
	defer func() { _ = bus.Close() }()

	st := store.New(pool)
	internalToken := config.String("INTERNAL_SERVICE_TOKEN", "")

	/* ---------------------------------------------------------- publishing -- */

	publisher := messagingevents.NewPublisher(st, bus, logger)
	go publisher.Run(ctx, config.Duration("OUTBOX_POLL_INTERVAL", 2*time.Second))
	go publisher.Purge(ctx, config.Duration("OUTBOX_PURGE_INTERVAL", 6*time.Hour),
		config.Int("OUTBOX_RETAIN_DAYS", 14))

	/* ----------------------------------------------------------- consuming -- */

	consumer := messagingevents.NewConsumer(st, logger)
	subscription, err := bus.Subscribe(ctx, events.SubscribeOptions{
		Durable:  "messaging-projection",
		Subjects: messagingevents.Subjects(),
	}, consumer.Handle)
	if err != nil {
		return err
	}
	defer subscription.Stop()

	/* ---------------------------------------------------------------- http -- */

	restAPI := api.New(api.Config{
		Store: st,
		Candidates: candidates.New(candidates.Config{
			BaseURL: config.String("CANDIDATES_URL", "http://localhost:8087"),
			Token:   internalToken,
			Timeout: config.Duration("INTERNAL_CALL_TIMEOUT", 5*time.Second),
		}),
		Limiter:        redisx.NewRateLimiter(redisClient, serviceName),
		Logger:         logger,
		InternalToken:  internalToken,
		DailyOpenLimit: config.Int("MESSAGING_DAILY_OPEN_LIMIT", 100),
	})

	mux := http.NewServeMux()
	mux.Handle("/", restAPI.Routes())
	mux.Handle("/healthz", httpx.Health(nil))
	mux.Handle("/readyz", httpx.Health(map[string]func(context.Context) error{
		"postgres": postgres.HealthCheck(pool),
		"redis":    redisx.HealthCheck(redisClient),
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
		Addr:    ":" + config.String("MESSAGING_HTTP_PORT", "8088"),
		Handler: handler,
		Logger:  logger,
	})
}
