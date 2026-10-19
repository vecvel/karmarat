// Command server runs the candidates service.
//
// The service has two faces that must not meet: a candidate's own profile, and
// the talent pool a company keeps. Discovery is the bridge between them, and it
// only ever crosses in one direction — a company can learn that a discoverable
// candidate exists, never who they are, until the candidate answers.
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
	"github.com/karmarat/platform/services/candidates/internal/api"
	candidateevents "github.com/karmarat/platform/services/candidates/internal/events"
	"github.com/karmarat/platform/services/candidates/internal/storage"
	"github.com/karmarat/platform/services/candidates/internal/store"
	"github.com/karmarat/platform/services/candidates/migrations"
)

const serviceName = "candidates"

func main() {
	if err := run(); err != nil {
		slog.Default().Error("candidates service stopped", slog.Any("error", err))
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

	/* ------------------------------------------------------ object storage -- */

	// Resumes never pass through this process: it signs a URL and records the
	// key. Proxying the bytes would put every upload's throughput on the service
	// and give it a credential broad enough to read the whole bucket.
	presigner, err := storage.New(storage.Config{
		Endpoint:  config.String("S3_ENDPOINT", "http://localhost:9000"),
		Region:    config.String("S3_REGION", "us-east-1"),
		AccessKey: config.String("S3_ACCESS_KEY", ""),
		SecretKey: config.String("S3_SECRET_KEY", ""),
		Bucket:    config.String("S3_BUCKET_RESUMES", "resumes"),
		PathStyle: config.Bool("S3_FORCE_PATH_STYLE", true),
	})
	if err != nil {
		return err
	}

	st := store.New(pool)

	/* ---------------------------------------------------------- projection -- */

	consumer := candidateevents.NewConsumer(st, logger)
	subscription, err := bus.Subscribe(ctx, events.SubscribeOptions{
		Durable:  "candidates-projection",
		Subjects: candidateevents.Subjects(),
	}, consumer.Handle)
	if err != nil {
		return err
	}
	defer subscription.Stop()

	/* ---------------------------------------------------------------- http -- */

	restAPI := api.New(api.Config{
		Store:         st,
		Publisher:     candidateevents.NewPublisher(bus, logger),
		Presigner:     presigner,
		Limiter:       redisx.NewRateLimiter(redisClient, serviceName),
		Logger:        logger,
		InternalToken: config.String("INTERNAL_SERVICE_TOKEN", ""),
		UploadTTL:     config.Duration("S3_PRESIGN_TTL", 10*time.Minute),
		DownloadTTL:   config.Duration("S3_PRESIGN_TTL", 5*time.Minute),
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
		Addr:    ":" + config.String("CANDIDATES_HTTP_PORT", "8087"),
		Handler: handler,
		Logger:  logger,
	})
}
