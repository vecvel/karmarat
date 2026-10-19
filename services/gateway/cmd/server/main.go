// Command server runs the Karmarat API gateway.
//
// The gateway is the platform's single front door. It decides which portal a
// request arrived at, which tenant that portal belongs to, and who is calling —
// then hands the backend services a request they can trust without repeating any
// of that work.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/karmarat/platform/packages/goshared/config"
	"github.com/karmarat/platform/packages/goshared/httpx"
	"github.com/karmarat/platform/packages/goshared/logging"
	"github.com/karmarat/platform/packages/goshared/observability"
	"github.com/karmarat/platform/packages/goshared/redisx"
	"github.com/karmarat/platform/packages/goshared/tenancy"
	"github.com/karmarat/platform/packages/goshared/tokens"
	"github.com/karmarat/platform/services/gateway/internal/proxy"
	"github.com/karmarat/platform/services/gateway/internal/routing"
	"github.com/karmarat/platform/services/gateway/internal/tenant"
)

const serviceName = "gateway"

func main() {
	if err := run(); err != nil {
		slog.Default().Error("gateway stopped", slog.Any("error", err))
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

	redisClient, err := redisx.Connect(ctx, cfg.RedisURL, logger)
	if err != nil {
		return err
	}
	defer func() { _ = redisClient.Close() }()

	// The gateway verifies but never signs: it holds the public key only.
	_, publicPEM, err := tokens.LoadKeyPair(
		config.String("JWT_PRIVATE_KEY_PATH", "./deploy/keys/jwt-private.pem"),
		config.String("JWT_PUBLIC_KEY_PATH", "./deploy/keys/jwt-public.pem"),
	)
	if err != nil {
		return errors.Join(err, errors.New("run `make keys` to generate a signing keypair"))
	}

	verifier, err := tokens.NewVerifier(tokens.VerifierConfig{
		PublicKeyPEM: publicPEM,
		Issuer:       "karmarat-identity",
		Audience:     cfg.Hostname,
	})
	if err != nil {
		return err
	}

	gw := &gateway{
		hostname: cfg.Hostname,
		table:    routing.NewTable(),
		proxies:  proxy.NewPool(logger),
		verifier: verifier,
		limiter:  redisx.NewRateLimiter(redisClient, "gateway"),
		logger:   logger,
		resolver: tenant.NewResolver(tenant.Config{
			IdentityURL:   config.String("IDENTITY_URL", "http://localhost:8081"),
			InternalToken: config.String("INTERNAL_SERVICE_TOKEN", ""),
			Cache:         redisClient,
			Logger:        logger,
		}),
		rateLimit:       config.Int("GATEWAY_RATE_LIMIT", 300),
		rateLimitWindow: config.Duration("GATEWAY_RATE_WINDOW", time.Minute),
	}

	mux := http.NewServeMux()
	mux.Handle("/healthz", httpx.Health(nil))
	mux.Handle("/readyz", httpx.Health(map[string]func(context.Context) error{
		"redis": redisx.HealthCheck(redisClient),
	}))
	mux.Handle("/", gw)

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

	logger.Info("gateway ready",
		slog.String("hostname", cfg.Hostname),
		slog.Int("routes", len(gw.table.Routes())),
	)

	return httpx.Serve(ctx, httpx.ServerConfig{
		Addr:    ":" + config.String("GATEWAY_HTTP_PORT", "8080"),
		Handler: handler,
		Logger:  logger,
		// Long enough for file uploads and streamed notification channels.
		WriteTimeout: 2 * time.Minute,
	})
}

type gateway struct {
	hostname        string
	table           *routing.Table
	proxies         *proxy.Pool
	verifier        *tokens.Verifier
	resolver        *tenant.Resolver
	limiter         rateLimiter
	logger          *slog.Logger
	rateLimit       int
	rateLimitWindow time.Duration
}

// rateLimiter is the slice of redisx.RateLimiter the gateway depends on.
//
// Narrowed to an interface so the security chain — header stripping, the tenant
// boundary, the subscription gate — can be exercised in tests without standing
// up Redis to run a Lua script. Production still passes the real limiter.
type rateLimiter interface {
	Allow(ctx context.Context, key string, limit int, window time.Duration) (redisx.RateLimitResult, error)
}

func (g *gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// A client must never be able to assert its own identity. Anything that
	// looks like a trust header is removed before anything else runs.
	stripTrustHeaders(r)

	host := tenancy.ResolveHost(r.Host, g.hostname)

	route, matched := g.table.Match(r.URL.Path)
	if !matched {
		httpx.WriteProblem(w, r, httpx.NotFound("No API route matches this path."))
		return
	}

	if !route.AllowsPortal(host.Portal) {
		// Reported as "not found" rather than "forbidden": which endpoints exist
		// on which portal is not something an unauthenticated caller should learn.
		httpx.WriteProblem(w, r, httpx.NotFound("No API route matches this path."))
		return
	}

	// Resolve the tenant before authenticating, so a request to a suspended or
	// unknown company is rejected without touching its data.
	var company *tenant.Company
	if host.Portal == tenancy.PortalCompany {
		resolved, err := g.resolver.Resolve(r.Context(), host.CompanySlug)
		if err != nil {
			if errors.Is(err, tenant.ErrNotFound) {
				httpx.WriteProblem(w, r, httpx.NotFound("This company portal does not exist."))
				return
			}
			g.logger.Error("tenant resolution failed",
				slog.String("slug", host.CompanySlug), slog.Any("error", err))
			httpx.WriteProblem(w, r, httpx.NewProblem(http.StatusBadGateway, "tenant_unavailable",
				"Bad Gateway", "The company portal could not be resolved. Please try again."))
			return
		}
		company = &resolved
	}

	principal := tenancy.Anonymous()
	if raw := bearerToken(r); raw != "" {
		claims, err := g.verifier.Verify(raw)
		if err != nil {
			if errors.Is(err, tokens.ErrExpiredToken) {
				httpx.WriteProblem(w, r, httpx.NewProblem(http.StatusUnauthorized, "token_expired",
					"Unauthorized", "Your session has expired. Please sign in again."))
				return
			}
			httpx.WriteProblem(w, r, httpx.Unauthorized("The supplied credentials are not valid."))
			return
		}
		principal = claims.Principal()
	}

	if !route.Public && !principal.IsAuthenticated() {
		httpx.WriteProblem(w, r, httpx.Unauthorized("You must be signed in to perform this action."))
		return
	}

	if principal.IsAuthenticated() && !route.AllowsPrincipal(principal.Type) {
		httpx.WriteProblem(w, r, httpx.Forbidden("This endpoint is not available to your account type."))
		return
	}

	// The tenant boundary. A company token is only valid on its own subdomain,
	// so a recruiter cannot reach another company's data by changing the host.
	if company != nil && principal.Type == tenancy.PrincipalCompany {
		if principal.CompanyID != company.CompanyID {
			g.logger.Warn("cross-tenant request rejected",
				slog.String("token_company", principal.CompanyID),
				slog.String("host_company", company.CompanyID),
				slog.String("path", r.URL.Path),
			)
			httpx.WriteProblem(w, r, httpx.Forbidden("Your session does not belong to this company portal."))
			return
		}

		// An owner may still reach billing while the subscription is lapsed;
		// everything else in the portal is closed until it is resolved.
		if !company.PortalAvailable && !isBillingPath(r.URL.Path) {
			httpx.WriteProblem(w, r, httpx.NewProblem(http.StatusPaymentRequired, "subscription_required",
				"Payment Required",
				"This company portal is inactive. An active subscription is required to continue."))
			return
		}
	}

	if route.RequireCompany && company == nil {
		httpx.WriteProblem(w, r, httpx.BadRequest("This endpoint must be reached through a company portal."))
		return
	}

	if !g.allow(w, r, principal) {
		return
	}

	g.injectTrustHeaders(r, principal, host, company)

	reverse, err := g.proxies.For(route.Target)
	if err != nil {
		g.logger.Error("invalid upstream target",
			slog.String("service", route.Service), slog.Any("error", err))
		httpx.WriteProblem(w, r, httpx.Internal("The gateway is misconfigured for this route."))
		return
	}

	proxy.StripAPIPrefix(r)
	reverse.ServeHTTP(w, r)
}

// allow applies the gateway's coarse rate limit.
//
// Authenticated callers are keyed by account so one noisy office network does
// not throttle every colleague behind the same address.
func (g *gateway) allow(w http.ResponseWriter, r *http.Request, principal tenancy.Principal) bool {
	key := "ip:" + clientIP(r)
	if principal.IsAuthenticated() {
		key = "sub:" + principal.Subject
	}

	result, err := g.limiter.Allow(r.Context(), key, g.rateLimit, g.rateLimitWindow)
	if err != nil {
		// Failing open: a cache outage should not take the platform offline.
		g.logger.Warn("rate limiter unavailable, allowing request", slog.Any("error", err))
		return true
	}

	w.Header().Set("X-RateLimit-Limit", strconv.Itoa(result.Limit))
	w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(result.Remaining))

	if !result.Allowed {
		w.Header().Set("Retry-After", strconv.Itoa(int(result.RetryAfter.Seconds())+1))
		httpx.WriteProblem(w, r, httpx.TooManyRequests("Too many requests. Please slow down."))
		return false
	}

	return true
}

// injectTrustHeaders passes the verified identity to the upstream service.
func (g *gateway) injectTrustHeaders(r *http.Request, principal tenancy.Principal, host tenancy.HostContext, company *tenant.Company) {
	r.Header.Set("X-Portal", string(host.Portal))
	r.Header.Set(httpx.RequestIDHeader, httpx.RequestIDFromContext(r.Context()))

	if company != nil {
		r.Header.Set(httpx.HeaderCompanyID, company.CompanyID)
		r.Header.Set(httpx.HeaderCompanySlug, company.Slug)
	}

	if !principal.IsAuthenticated() {
		return
	}

	r.Header.Set(httpx.HeaderPrincipalType, string(principal.Type))
	r.Header.Set(httpx.HeaderPrincipalID, principal.Subject)
	r.Header.Set(httpx.HeaderSessionID, principal.SessionID)
	r.Header.Set(httpx.HeaderPrincipalMail, principal.Email)

	if len(principal.Roles) > 0 {
		r.Header.Set(httpx.HeaderRoles, strings.Join(principal.Roles, ","))
	}
	if len(principal.Permissions) > 0 {
		r.Header.Set(httpx.HeaderPermissions, strings.Join(principal.Permissions, ","))
	}

	// For a company principal the tenant always comes from the token, never from
	// the host, so the two can never disagree downstream.
	if principal.Type == tenancy.PrincipalCompany && principal.CompanyID != "" {
		r.Header.Set(httpx.HeaderCompanyID, principal.CompanyID)
	}
}

// stripTrustHeaders removes anything a client sent that the gateway alone may set.
func stripTrustHeaders(r *http.Request) {
	for _, header := range []string{
		httpx.HeaderPrincipalType,
		httpx.HeaderPrincipalID,
		httpx.HeaderCompanyID,
		httpx.HeaderCompanySlug,
		httpx.HeaderPermissions,
		httpx.HeaderRoles,
		httpx.HeaderSessionID,
		httpx.HeaderPrincipalMail,
		"X-Portal",
		"X-Internal-Token",
		"X-Forwarded-For",
		"X-Real-IP",
	} {
		r.Header.Del(header)
	}
}

func isBillingPath(path string) bool {
	return strings.HasPrefix(path, "/api/v1/billing") ||
		strings.HasPrefix(path, "/api/v1/payments") ||
		strings.HasPrefix(path, "/api/v1/auth")
}

func bearerToken(r *http.Request) string {
	header := r.Header.Get("Authorization")
	scheme, token, found := strings.Cut(header, " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return ""
	}
	return strings.TrimSpace(token)
}

func clientIP(r *http.Request) string {
	host, _, found := strings.Cut(r.RemoteAddr, ":")
	if !found {
		return r.RemoteAddr
	}
	return host
}
