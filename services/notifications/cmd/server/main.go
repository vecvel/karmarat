// Command server runs the notifications service.
//
// It is the only service that both consumes nearly every platform event and
// holds long-lived client connections. Those two jobs meet in Redis: the
// consumer writes the durable row and publishes a frame, and whichever replica
// happens to be holding that person's open stream is the one that forwards it.
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
	"github.com/karmarat/platform/services/notifications/internal/api"
	notificationevents "github.com/karmarat/platform/services/notifications/internal/events"
	"github.com/karmarat/platform/services/notifications/internal/mail"
	"github.com/karmarat/platform/services/notifications/internal/realtime"
	"github.com/karmarat/platform/services/notifications/internal/store"
	"github.com/karmarat/platform/services/notifications/internal/worker"
	"github.com/karmarat/platform/services/notifications/migrations"
)

const serviceName = "notifications"

func main() {
	if err := run(); err != nil {
		slog.Default().Error("notifications service stopped", slog.Any("error", err))
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
	hub := realtime.New(redisClient, logger)

	/* ------------------------------------------------------------- email --- */

	mailer, err := mail.New(mail.Config{
		Host:        config.String("SMTP_HOST", ""),
		Port:        config.String("SMTP_PORT", "1025"),
		Username:    config.String("SMTP_USERNAME", ""),
		Password:    config.String("SMTP_PASSWORD", ""),
		UseTLS:      config.Bool("SMTP_TLS", false),
		FromAddress: config.String("SMTP_FROM_ADDRESS", ""),
		FromName:    config.String("SMTP_FROM_NAME", "Karmarat"),
		Timeout:     config.Duration("SMTP_TIMEOUT", 15*time.Second),
	})
	if err != nil {
		// A template that does not parse is a boot failure, not a message that
		// quietly never sends.
		return err
	}

	emailWorker := worker.NewEmail(worker.EmailConfig{
		Store:  st,
		Mailer: mailer,
		Logger: logger,
		Portals: worker.PortalURLs{
			Company: config.String("COMPANY_PORTAL_URL", "http://localhost:3000"),
			Jobs:    config.String("JOBS_PORTAL_URL", "http://localhost:3000"),
			Root:    config.String("ROOT_PORTAL_URL", "http://localhost:3000"),
		},
		Batch:       config.Int("EMAIL_BATCH_SIZE", 25),
		BaseBackoff: config.Duration("EMAIL_RETRY_BACKOFF", 30*time.Second),
	})
	go emailWorker.Run(ctx, config.Duration("EMAIL_POLL_INTERVAL", 5*time.Second))

	/* --------------------------------------------------------- consuming --- */

	consumer := notificationevents.NewConsumer(st, hub, logger)
	subscription, err := bus.Subscribe(ctx, events.SubscribeOptions{
		Durable:  "notifications-fanout",
		Subjects: notificationevents.Subjects(),
	}, consumer.Handle)
	if err != nil {
		return err
	}
	defer subscription.Stop()

	/* -------------------------------------------------------------- http --- */

	streamLifetime := config.Duration("STREAM_LIFETIME", 4*time.Minute)

	restAPI := api.New(api.Config{
		Store:          st,
		Hub:            hub,
		Logger:         logger,
		Heartbeat:      config.Duration("STREAM_HEARTBEAT", 20*time.Second),
		StreamLifetime: streamLifetime,
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
		Addr:    ":" + config.String("NOTIFICATIONS_HTTP_PORT", "8089"),
		Handler: handler,
		// The write deadline covers a whole response, and an SSE response never
		// ends. It is raised past the stream's own lifetime so the handler is
		// the thing that closes a stream, in an orderly way the client can
		// reconnect from, rather than the server cutting the socket mid-frame.
		// The ordinary endpoints here read and write single rows, so a generous
		// deadline costs them nothing.
		WriteTimeout: streamLifetime + time.Minute,
		Logger:       logger,
	})
}
