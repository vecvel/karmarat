// Command server runs the identity service.
//
// Identity is the first service every request touches: it authenticates people,
// resolves which tenant they are acting in, and mints the tokens the rest of the
// platform trusts.
package main

import (
	"context"
	"errors"
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
	"github.com/karmarat/platform/packages/goshared/tokens"
	"github.com/karmarat/platform/services/identity/internal/api"
	"github.com/karmarat/platform/services/identity/internal/auth"
	"github.com/karmarat/platform/services/identity/internal/projection"
	"github.com/karmarat/platform/services/identity/internal/provisioning"
	"github.com/karmarat/platform/services/identity/internal/store"
	"github.com/karmarat/platform/services/identity/internal/team"
	"github.com/karmarat/platform/services/identity/migrations"
)

const serviceName = "identity"

func main() {
	if err := run(); err != nil {
		slog.Default().Error("identity service stopped", slog.Any("error", err))
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

	/* ---------------------------------------------------------------- data -- */

	pool, err := postgres.Connect(ctx, postgres.Config{URL: cfg.PostgresURL}, logger)
	if err != nil {
		return err
	}
	defer pool.Close()

	// Running migrations at boot keeps a deployment from serving traffic against
	// a schema it does not expect.
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

	/* -------------------------------------------------------------- tokens -- */

	privatePEM, publicPEM, err := tokens.LoadKeyPair(
		config.String("JWT_PRIVATE_KEY_PATH", "./deploy/keys/jwt-private.pem"),
		config.String("JWT_PUBLIC_KEY_PATH", "./deploy/keys/jwt-public.pem"),
	)
	if err != nil {
		return errors.Join(err, errors.New("run `make keys` to generate a signing keypair"))
	}

	issuer, err := tokens.NewIssuer(tokens.IssuerConfig{
		PrivateKeyPEM: privatePEM,
		Issuer:        "karmarat-identity",
		Audience:      cfg.Hostname,
		TTL:           config.Duration("ACCESS_TOKEN_TTL", 15*time.Minute),
	})
	if err != nil {
		return err
	}

	verifier, err := tokens.NewVerifier(tokens.VerifierConfig{
		PublicKeyPEM: publicPEM,
		Issuer:       "karmarat-identity",
		Audience:     cfg.Hostname,
	})
	if err != nil {
		return err
	}

	/* ------------------------------------------------------------ services -- */

	st := store.New(pool)
	provisioner := provisioning.New(st, bus, logger)

	if err := provisioner.SeedPlatformRoles(ctx); err != nil {
		return err
	}

	// A fresh installation needs one way in; this is a no-op afterwards.
	created, err := provisioner.EnsurePlatformSuperAdmin(ctx,
		config.String("BOOTSTRAP_ADMIN_EMAIL", ""),
		config.String("BOOTSTRAP_ADMIN_NAME", "Platform Administrator"),
		config.String("BOOTSTRAP_ADMIN_PASSWORD", ""),
	)
	if err != nil {
		return err
	}
	if created {
		logger.Warn("bootstrap administrator created — sign in and change this password immediately")
	}

	service := auth.NewService(auth.Config{
		Store:      st,
		Issuer:     issuer,
		Bus:        bus,
		Logger:     logger,
		RefreshTTL: config.Duration("REFRESH_TOKEN_TTL", 30*24*time.Hour),
	})

	/* ---------------------------------------------------------- projection -- */

	consumer := projection.NewConsumer(st, provisioner, logger)
	subscription, err := bus.Subscribe(ctx, events.SubscribeOptions{
		Durable:  "identity-projection",
		Subjects: projection.Subjects(),
	}, consumer.Handle)
	if err != nil {
		return err
	}
	defer subscription.Stop()

	/* ---------------------------------------------------------- background -- */

	go purgeExpiredSessions(ctx, st, logger)

	/* ---------------------------------------------------------------- http -- */

	restAPI := api.New(api.Config{
		Service:       service,
		Provisioner:   provisioner,
		Team:          team.New(st, bus, logger),
		Store:         st,
		Issuer:        issuer,
		Verifier:      verifier,
		Limiter:       redisx.NewRateLimiter(redisClient, "identity"),
		Logger:        logger,
		InternalToken: config.String("INTERNAL_SERVICE_TOKEN", ""),
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
					httpx.CORS(httpx.CORSConfig{PlatformHostname: cfg.Hostname})(
						observability.Middleware(serviceName)(mux)),
				),
			),
		),
	)

	return httpx.Serve(ctx, httpx.ServerConfig{
		Addr:    ":" + config.String("IDENTITY_HTTP_PORT", "8081"),
		Handler: handler,
		Logger:  logger,
	})
}

// purgeExpiredSessions removes sessions that expired long enough ago to be of no
// forensic value, keeping the table from growing without bound.
func purgeExpiredSessions(ctx context.Context, st *store.Store, logger *slog.Logger) {
	ticker := time.NewTicker(6 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			removed, err := st.PurgeExpiredSessions(ctx, 30*24*time.Hour)
			if err != nil {
				logger.Error("failed to purge expired sessions", slog.Any("error", err))
				continue
			}
			if removed > 0 {
				logger.Info("purged expired sessions", slog.Int64("count", removed))
			}
		}
	}
}
