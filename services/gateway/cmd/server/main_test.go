package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/karmarat/platform/packages/goshared/httpx"
	"github.com/karmarat/platform/packages/goshared/redisx"
	"github.com/karmarat/platform/packages/goshared/tenancy"
	"github.com/karmarat/platform/packages/goshared/tokens"
	"github.com/karmarat/platform/services/gateway/internal/proxy"
	"github.com/karmarat/platform/services/gateway/internal/routing"
	"github.com/karmarat/platform/services/gateway/internal/tenant"
)

// These tests exercise the gateway's security chain end to end — a real signed
// token, a real routing table, a real reverse proxy to a recording upstream —
// because the chain is the platform's only tenant boundary. A unit test of any
// one step would not catch the ordering mistake that matters: a check that runs
// after the request has already been forwarded protects nothing.

const (
	testHostname = "karmarat.test"

	acmeID   = "11111111-1111-4111-8111-111111111111"
	globexID = "22222222-2222-4222-8222-222222222222"
	lapsedID = "33333333-3333-4333-8333-333333333333"
)

/* --------------------------------------------------------------- fixtures -- */

// testKeys mints one RSA keypair for the whole package: 2048-bit generation is
// slow enough that doing it per test would dominate the run.
var testKeys = sync.OnceValues(func() (privatePEM, publicPEM []byte) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic("test keypair: " + err.Error())
	}

	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		panic("test public key: " + err.Error())
	}

	return pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}),
		pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
})

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// recordedRequest is what the upstream saw, which is the only way to prove the
// gateway forwarded or withheld what it should have.
type recordedRequest struct {
	Method string
	Path   string
	Header http.Header
	Host   string
}

type fakeUpstream struct {
	*httptest.Server

	mu       sync.Mutex
	requests []recordedRequest
}

func startFakeUpstream(t *testing.T) *fakeUpstream {
	t.Helper()

	up := &fakeUpstream{}
	up.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		up.mu.Lock()
		up.requests = append(up.requests, recordedRequest{
			Method: r.Method,
			Path:   r.URL.Path,
			Header: r.Header.Clone(),
			Host:   r.Host,
		})
		up.mu.Unlock()

		httpx.WriteJSON(w, http.StatusOK, map[string]string{"upstream": "reached"})
	}))
	t.Cleanup(up.Close)

	return up
}

func (u *fakeUpstream) count() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return len(u.requests)
}

func (u *fakeUpstream) last(t *testing.T) recordedRequest {
	t.Helper()

	u.mu.Lock()
	defer u.mu.Unlock()
	if len(u.requests) == 0 {
		t.Fatal("upstream was never reached")
	}
	return u.requests[len(u.requests)-1]
}

// startFakeIdentity answers the internal tenant-resolution endpoint.
func startFakeIdentity(t *testing.T, tenants map[string]tenant.Company) *httptest.Server {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		company, known := tenants[r.URL.Query().Get("slug")]
		if !known {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(company)
	}))
	t.Cleanup(server.Close)

	return server
}

// stubLimiter replaces the Redis-backed limiter. Allowing by default keeps the
// limit out of every other test's way.
type stubLimiter struct {
	allowed    bool
	err        error
	retryAfter time.Duration

	mu   sync.Mutex
	keys []string
}

func (s *stubLimiter) Allow(_ context.Context, key string, limit int, _ time.Duration) (redisx.RateLimitResult, error) {
	s.mu.Lock()
	s.keys = append(s.keys, key)
	s.mu.Unlock()

	if s.err != nil {
		return redisx.RateLimitResult{}, s.err
	}
	remaining := limit - 1
	if !s.allowed {
		remaining = 0
	}
	return redisx.RateLimitResult{
		Allowed:    s.allowed,
		Limit:      limit,
		Remaining:  remaining,
		RetryAfter: s.retryAfter,
	}, nil
}

func (s *stubLimiter) seen() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.keys...)
}

/* ---------------------------------------------------------------- harness -- */

type harness struct {
	handler  http.Handler
	upstream *fakeUpstream
	issuer   *tokens.Issuer
	limiter  *stubLimiter
}

func defaultTenants() map[string]tenant.Company {
	return map[string]tenant.Company{
		"acme": {
			CompanyID: acmeID, Slug: "acme", Name: "Acme Inc",
			State: "active", SubscriptionState: "active", PortalAvailable: true,
		},
		"globex": {
			CompanyID: globexID, Slug: "globex", Name: "Globex",
			State: "active", SubscriptionState: "active", PortalAvailable: true,
		},
		// A tenant whose subscription has lapsed: the portal is closed, but
		// billing must stay reachable or the customer cannot pay to reopen it.
		"lapsed": {
			CompanyID: lapsedID, Slug: "lapsed", Name: "Lapsed Ltd",
			State: "active", SubscriptionState: "past_due", PortalAvailable: false,
		},
	}
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	upstream := startFakeUpstream(t)
	identity := startFakeIdentity(t, defaultTenants())

	// The routing table reads upstream addresses from the environment, so every
	// route is pointed at the one recording upstream.
	for _, key := range []string{
		"IDENTITY_URL", "COMPANIES_URL", "JOBS_URL", "APPLICATIONS_URL",
		"CANDIDATES_URL", "MESSAGING_URL", "NOTIFICATIONS_URL",
		"SUBSCRIPTIONS_URL", "PAYMENTS_URL", "SUPPORT_URL", "ADMIN_URL",
	} {
		t.Setenv(key, upstream.URL)
	}

	privatePEM, publicPEM := testKeys()

	issuer, err := tokens.NewIssuer(tokens.IssuerConfig{
		PrivateKeyPEM: privatePEM,
		Issuer:        "karmarat-identity",
		Audience:      testHostname,
		TTL:           15 * time.Minute,
	})
	if err != nil {
		t.Fatalf("issuer: %v", err)
	}

	verifier, err := tokens.NewVerifier(tokens.VerifierConfig{
		PublicKeyPEM: publicPEM,
		Issuer:       "karmarat-identity",
		Audience:     testHostname,
	})
	if err != nil {
		t.Fatalf("verifier: %v", err)
	}

	logger := discardLogger()
	limiter := &stubLimiter{allowed: true}

	gw := &gateway{
		hostname: testHostname,
		table:    routing.NewTable(),
		proxies:  proxy.NewPool(logger),
		verifier: verifier,
		// No cache: the resolver's own tests cover caching, and a live lookup
		// keeps these tests independent of it.
		resolver: tenant.NewResolver(tenant.Config{
			IdentityURL: identity.URL,
			Logger:      logger,
		}),
		limiter:         limiter,
		logger:          logger,
		rateLimit:       300,
		rateLimitWindow: time.Minute,
	}

	return &harness{
		// RequestID mirrors production wiring: the gateway forwards the id it
		// attaches, so a request can be followed across the hop.
		handler:  httpx.RequestID(gw),
		upstream: upstream,
		issuer:   issuer,
		limiter:  limiter,
	}
}

// token mints a signed access token for a principal.
func (h *harness) token(t *testing.T, in tokens.IssueInput) string {
	t.Helper()

	signed, _, err := h.issuer.Issue(in)
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	return signed
}

func (h *harness) companyToken(t *testing.T, companyID string) string {
	t.Helper()
	return h.token(t, tokens.IssueInput{
		Subject:       "usr_recruiter",
		PrincipalType: tenancy.PrincipalCompany,
		CompanyID:     companyID,
		Email:         "recruiter@example.test",
		Roles:         []string{"company_admin"},
		Permissions:   []string{"jobs.read", "jobs.update", "billing.read"},
		SessionID:     "ses_1",
	})
}

type request struct {
	method string
	host   string
	path   string
	token  string
	header http.Header
}

func (h *harness) do(t *testing.T, req request) *httptest.ResponseRecorder {
	t.Helper()

	if req.method == "" {
		req.method = http.MethodGet
	}

	r := httptest.NewRequest(req.method, "http://"+req.host+req.path, nil)
	r.Host = req.host
	for key, values := range req.header {
		for _, value := range values {
			r.Header.Add(key, value)
		}
	}
	if req.token != "" {
		r.Header.Set("Authorization", "Bearer "+req.token)
	}

	recorder := httptest.NewRecorder()
	h.handler.ServeHTTP(recorder, r)

	return recorder
}

// problem decodes an error response and fails if the body is not problem+json.
func problem(t *testing.T, recorder *httptest.ResponseRecorder) httpx.Problem {
	t.Helper()

	if contentType := recorder.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "application/problem+json") {
		t.Fatalf("content type = %q, want application/problem+json", contentType)
	}

	var decoded httpx.Problem
	if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode problem: %v (body %q)", err, recorder.Body.String())
	}
	return decoded
}

/* -------------------------------------------------------- header stripping -- */

// spoofedHeaders is everything a client might send to assert an identity of its
// own choosing.
func spoofedHeaders() http.Header {
	return http.Header{
		httpx.HeaderPrincipalType: {string(tenancy.PrincipalPlatform)},
		httpx.HeaderPrincipalID:   {"usr_attacker"},
		httpx.HeaderCompanyID:     {globexID},
		httpx.HeaderCompanySlug:   {"globex"},
		httpx.HeaderPermissions:   {"platform_companies.delete,jobs.delete"},
		httpx.HeaderRoles:         {"super_admin"},
		httpx.HeaderSessionID:     {"ses_forged"},
		httpx.HeaderPrincipalMail: {"attacker@example.test"},
		"X-Portal":                {"root"},
		"X-Internal-Token":        {"guessed-internal-token"},
		"X-Real-Ip":               {"10.0.0.1"},
	}
}

// TestGatewayStripsClientSuppliedTrustHeaders is the single most important test
// here: every downstream service authorizes from these headers without
// re-verifying anything, so one that survives the gateway is a full bypass.
func TestGatewayStripsClientSuppliedTrustHeaders(t *testing.T) {
	h := newHarness(t)

	recorder := h.do(t, request{
		host:   "jobs." + testHostname,
		path:   "/api/v1/public/jobs",
		header: spoofedHeaders(),
	})

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", recorder.Code, recorder.Body.String())
	}

	forwarded := h.upstream.last(t).Header

	for _, header := range []string{
		httpx.HeaderPrincipalType,
		httpx.HeaderPrincipalID,
		httpx.HeaderCompanyID,
		httpx.HeaderCompanySlug,
		httpx.HeaderPermissions,
		httpx.HeaderRoles,
		httpx.HeaderSessionID,
		httpx.HeaderPrincipalMail,
		"X-Internal-Token",
	} {
		if got := forwarded.Get(header); got != "" {
			t.Errorf("%s reached the upstream as %q; it must be stripped from an anonymous request", header, got)
		}
	}

	// X-Portal is gateway-set, not client-set: the value must be the portal the
	// request actually arrived at, not the one the client claimed.
	if got := forwarded.Get("X-Portal"); got != string(tenancy.PortalJobs) {
		t.Errorf("X-Portal = %q, want %q", got, tenancy.PortalJobs)
	}
}

// TestGatewayOverridesSpoofedHeadersWithVerifiedIdentity covers the harder
// case: the client sends a real token *and* forged headers claiming a different
// tenant and richer permissions.
func TestGatewayOverridesSpoofedHeadersWithVerifiedIdentity(t *testing.T) {
	h := newHarness(t)

	recorder := h.do(t, request{
		host:   "acme." + testHostname,
		path:   "/api/v1/jobs",
		token:  h.companyToken(t, acmeID),
		header: spoofedHeaders(),
	})

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", recorder.Code, recorder.Body.String())
	}

	forwarded := h.upstream.last(t).Header

	checks := map[string]string{
		httpx.HeaderPrincipalType: string(tenancy.PrincipalCompany),
		httpx.HeaderPrincipalID:   "usr_recruiter",
		httpx.HeaderCompanyID:     acmeID,
		httpx.HeaderCompanySlug:   "acme",
		httpx.HeaderRoles:         "company_admin",
		httpx.HeaderPermissions:   "jobs.read,jobs.update,billing.read",
		httpx.HeaderSessionID:     "ses_1",
		httpx.HeaderPrincipalMail: "recruiter@example.test",
		"X-Portal":                string(tenancy.PortalCompany),
	}

	for header, want := range checks {
		if got := forwarded.Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}

	if got := forwarded.Get("X-Internal-Token"); got != "" {
		t.Errorf("X-Internal-Token = %q, want it stripped", got)
	}
	if got := forwarded.Get(httpx.RequestIDHeader); got == "" {
		t.Error("request id was not forwarded to the upstream")
	}
}

/* ---------------------------------------------------------- tenant boundary -- */

// TestGatewayRejectsCrossTenantToken is the breach this whole design exists to
// prevent: a valid recruiter session used against another company's subdomain.
func TestGatewayRejectsCrossTenantToken(t *testing.T) {
	tests := []struct {
		name     string
		host     string
		path     string
		tokenFor string
	}{
		{name: "acme token on globex portal", host: "globex." + testHostname, path: "/api/v1/jobs", tokenFor: acmeID},
		{name: "globex token on acme portal", host: "acme." + testHostname, path: "/api/v1/applications", tokenFor: globexID},
		{name: "acme token on lapsed portal", host: "lapsed." + testHostname, path: "/api/v1/jobs", tokenFor: acmeID},
		// Even a public route on the wrong portal is refused for a company
		// principal: the tenant check does not depend on the route.
		{name: "acme token on globex careers page", host: "globex." + testHostname, path: "/api/v1/public/jobs", tokenFor: acmeID},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)

			recorder := h.do(t, request{
				host:  tc.host,
				path:  tc.path,
				token: h.companyToken(t, tc.tokenFor),
			})

			if recorder.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403 (body %q)", recorder.Code, recorder.Body.String())
			}
			if got := problem(t, recorder).Code; got != "forbidden" {
				t.Errorf("problem code = %q, want %q", got, "forbidden")
			}
			if h.upstream.count() != 0 {
				t.Error("a cross-tenant request reached the upstream")
			}
		})
	}
}

func TestGatewayAllowsMatchingTenant(t *testing.T) {
	h := newHarness(t)

	recorder := h.do(t, request{
		host:  "globex." + testHostname,
		path:  "/api/v1/jobs/job_1",
		token: h.companyToken(t, globexID),
	})

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", recorder.Code, recorder.Body.String())
	}
	if got := h.upstream.last(t).Header.Get(httpx.HeaderCompanyID); got != globexID {
		t.Errorf("X-Company-ID = %q, want %q", got, globexID)
	}
}

func TestGatewayUnknownCompanyPortal(t *testing.T) {
	h := newHarness(t)

	recorder := h.do(t, request{host: "nosuchcompany." + testHostname, path: "/api/v1/jobs"})

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body %q)", recorder.Code, recorder.Body.String())
	}
	if h.upstream.count() != 0 {
		t.Error("a request for an unknown tenant reached the upstream")
	}
}

/* ------------------------------------------------------------ principal type -- */

func TestGatewayRejectsWrongPrincipalTypeForPortal(t *testing.T) {
	tests := []struct {
		name      string
		host      string
		path      string
		principal tokens.IssueInput
		wantCode  int
	}{
		{
			name: "candidate on a company route",
			host: "acme." + testHostname, path: "/api/v1/jobs",
			principal: tokens.IssueInput{Subject: "cnd_1", PrincipalType: tenancy.PrincipalCandidate},
			wantCode:  http.StatusForbidden,
		},
		{
			name: "platform staff on a company route",
			host: "acme." + testHostname, path: "/api/v1/candidates",
			principal: tokens.IssueInput{Subject: "usr_ops", PrincipalType: tenancy.PrincipalPlatform},
			wantCode:  http.StatusForbidden,
		},
		{
			name: "recruiter on the admin console",
			host: "root." + testHostname, path: "/api/v1/admin/companies",
			principal: tokens.IssueInput{Subject: "usr_recruiter", PrincipalType: tenancy.PrincipalCompany, CompanyID: acmeID},
			wantCode:  http.StatusForbidden,
		},
		{
			name: "candidate on the admin console",
			host: "root." + testHostname, path: "/api/v1/admin/companies",
			principal: tokens.IssueInput{Subject: "cnd_1", PrincipalType: tenancy.PrincipalCandidate},
			wantCode:  http.StatusForbidden,
		},
		{
			name: "recruiter on a candidate route",
			host: "jobs." + testHostname, path: "/api/v1/my-applications",
			principal: tokens.IssueInput{Subject: "usr_recruiter", PrincipalType: tenancy.PrincipalCompany, CompanyID: acmeID},
			wantCode:  http.StatusForbidden,
		},
		// The portal boundary is checked before the principal: a company route
		// simply does not exist on the candidate job board, and saying "403"
		// there would confirm which endpoints the company portal has.
		{
			name: "company route on the jobs portal",
			host: "jobs." + testHostname, path: "/api/v1/jobs",
			principal: tokens.IssueInput{Subject: "cnd_1", PrincipalType: tenancy.PrincipalCandidate},
			wantCode:  http.StatusNotFound,
		},
		{
			name: "admin route on a company portal",
			host: "acme." + testHostname, path: "/api/v1/admin/companies",
			principal: tokens.IssueInput{Subject: "usr_ops", PrincipalType: tenancy.PrincipalPlatform},
			wantCode:  http.StatusNotFound,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)

			recorder := h.do(t, request{
				host:  tc.host,
				path:  tc.path,
				token: h.token(t, tc.principal),
			})

			if recorder.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d (body %q)", recorder.Code, tc.wantCode, recorder.Body.String())
			}
			if h.upstream.count() != 0 {
				t.Error("the request reached the upstream")
			}
		})
	}
}

func TestGatewayAllowsCandidateOnCandidatePortal(t *testing.T) {
	h := newHarness(t)

	recorder := h.do(t, request{
		host: "jobs." + testHostname,
		path: "/api/v1/my-applications",
		token: h.token(t, tokens.IssueInput{
			Subject:       "cnd_1",
			PrincipalType: tenancy.PrincipalCandidate,
			Permissions:   []string{"candidate_applications.read"},
		}),
	})

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", recorder.Code, recorder.Body.String())
	}

	forwarded := h.upstream.last(t).Header
	if got := forwarded.Get(httpx.HeaderPrincipalType); got != string(tenancy.PrincipalCandidate) {
		t.Errorf("X-Principal-Type = %q, want %q", got, tenancy.PrincipalCandidate)
	}
	// A candidate is global, never tenant-scoped.
	if got := forwarded.Get(httpx.HeaderCompanyID); got != "" {
		t.Errorf("X-Company-ID = %q, want empty for a candidate principal", got)
	}
}

/* ------------------------------------------------------- subscription gate -- */

// TestGatewaySubscriptionGate: a lapsed tenant's portal is closed, but the way
// out of that state has to stay open or the customer cannot pay their way back.
func TestGatewaySubscriptionGate(t *testing.T) {
	tests := []struct {
		name         string
		path         string
		wantCode     int
		wantReaches  bool
		wantProblems string
	}{
		{name: "workspace is closed", path: "/api/v1/jobs", wantCode: http.StatusPaymentRequired, wantProblems: "subscription_required"},
		{name: "applications are closed", path: "/api/v1/applications", wantCode: http.StatusPaymentRequired, wantProblems: "subscription_required"},
		{name: "candidates are closed", path: "/api/v1/candidates/cnd_1", wantCode: http.StatusPaymentRequired, wantProblems: "subscription_required"},

		{name: "billing stays open", path: "/api/v1/billing/subscription", wantCode: http.StatusOK, wantReaches: true},
		{name: "payments stay open", path: "/api/v1/payments/methods", wantCode: http.StatusOK, wantReaches: true},
		{name: "auth stays open", path: "/api/v1/auth/session", wantCode: http.StatusOK, wantReaches: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)

			recorder := h.do(t, request{
				host:  "lapsed." + testHostname,
				path:  tc.path,
				token: h.companyToken(t, lapsedID),
			})

			if recorder.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d (body %q)", recorder.Code, tc.wantCode, recorder.Body.String())
			}
			if tc.wantProblems != "" {
				if got := problem(t, recorder).Code; got != tc.wantProblems {
					t.Errorf("problem code = %q, want %q", got, tc.wantProblems)
				}
			}
			if reached := h.upstream.count() > 0; reached != tc.wantReaches {
				t.Errorf("upstream reached = %v, want %v", reached, tc.wantReaches)
			}
		})
	}
}

// TestGatewaySubscriptionGateDoesNotAffectHealthyTenants guards against the gate
// being applied by path rather than by subscription state.
func TestGatewaySubscriptionGateDoesNotAffectHealthyTenants(t *testing.T) {
	h := newHarness(t)

	recorder := h.do(t, request{
		host:  "acme." + testHostname,
		path:  "/api/v1/jobs",
		token: h.companyToken(t, acmeID),
	})

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", recorder.Code, recorder.Body.String())
	}
}

/* ------------------------------------------------------------ authentication -- */

func TestGatewayRequiresAuthenticationOnProtectedRoutes(t *testing.T) {
	tests := []struct {
		name string
		host string
		path string
	}{
		{name: "company workspace", host: "acme." + testHostname, path: "/api/v1/jobs"},
		{name: "candidate profile", host: "jobs." + testHostname, path: "/api/v1/me"},
		{name: "admin console", host: "root." + testHostname, path: "/api/v1/admin/companies"},
		{name: "rbac catalogue", host: "acme." + testHostname, path: "/api/v1/rbac/catalogue"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)

			recorder := h.do(t, request{host: tc.host, path: tc.path})

			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401 (body %q)", recorder.Code, recorder.Body.String())
			}
			if got := problem(t, recorder).Code; got != "unauthenticated" {
				t.Errorf("problem code = %q, want %q", got, "unauthenticated")
			}
			if h.upstream.count() != 0 {
				t.Error("an unauthenticated request reached the upstream")
			}
		})
	}
}

func TestGatewayAllowsPublicRoutesWithoutAToken(t *testing.T) {
	tests := []struct {
		name string
		host string
		path string
	}{
		{name: "job board", host: "jobs." + testHostname, path: "/api/v1/public/jobs"},
		{name: "careers page", host: "acme." + testHostname, path: "/api/v1/public/jobs/job_1"},
		{name: "apply", host: "acme." + testHostname, path: "/api/v1/public/apply/job_1"},
		{name: "sign in", host: "root." + testHostname, path: "/api/v1/auth/login"},
		{name: "jwks", host: testHostname, path: "/.well-known/jwks.json"},
		{name: "company registration", host: testHostname, path: "/api/v1/register/company"},
		{name: "payment webhook", host: testHostname, path: "/api/v1/webhooks/payments/stripe"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)

			recorder := h.do(t, request{host: tc.host, path: tc.path})

			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body %q)", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestGatewayRejectsBadTokens(t *testing.T) {
	h := newHarness(t)

	// Signed by a different key entirely.
	otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	foreignIssuer, err := tokens.NewIssuer(tokens.IssuerConfig{
		PrivateKeyPEM: pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(otherKey)}),
		Issuer:        "karmarat-identity",
		Audience:      testHostname,
	})
	if err != nil {
		t.Fatalf("foreign issuer: %v", err)
	}
	forged, _, err := foreignIssuer.Issue(tokens.IssueInput{
		Subject: "usr_attacker", PrincipalType: tenancy.PrincipalCompany, CompanyID: acmeID,
	})
	if err != nil {
		t.Fatalf("forge token: %v", err)
	}

	privatePEM, _ := testKeys()
	expiredIssuer, err := tokens.NewIssuer(tokens.IssuerConfig{
		PrivateKeyPEM: privatePEM,
		Issuer:        "karmarat-identity",
		Audience:      testHostname,
		// Beyond the verifier's 30s leeway.
		TTL: -5 * time.Minute,
	})
	if err != nil {
		t.Fatalf("expired issuer: %v", err)
	}
	expired, _, err := expiredIssuer.Issue(tokens.IssueInput{
		Subject: "usr_recruiter", PrincipalType: tenancy.PrincipalCompany, CompanyID: acmeID,
	})
	if err != nil {
		t.Fatalf("expired token: %v", err)
	}

	// Issued for a different platform installation.
	wrongAudienceIssuer, err := tokens.NewIssuer(tokens.IssuerConfig{
		PrivateKeyPEM: privatePEM,
		Issuer:        "karmarat-identity",
		Audience:      "someone-else.test",
	})
	if err != nil {
		t.Fatalf("wrong audience issuer: %v", err)
	}
	wrongAudience, _, err := wrongAudienceIssuer.Issue(tokens.IssueInput{
		Subject: "usr_recruiter", PrincipalType: tenancy.PrincipalCompany, CompanyID: acmeID,
	})
	if err != nil {
		t.Fatalf("wrong audience token: %v", err)
	}

	tests := []struct {
		name     string
		token    string
		wantCode string
	}{
		{name: "garbage", token: "not-a-token", wantCode: "unauthenticated"},
		{name: "empty segments", token: "..", wantCode: "unauthenticated"},
		{name: "signed by another key", token: forged, wantCode: "unauthenticated"},
		{name: "wrong audience", token: wrongAudience, wantCode: "unauthenticated"},
		// Expiry is reported distinctly so a client knows to refresh rather
		// than to send the user back to sign-in.
		{name: "expired", token: expired, wantCode: "token_expired"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			before := h.upstream.count()

			recorder := h.do(t, request{
				host:  "acme." + testHostname,
				path:  "/api/v1/jobs",
				token: tc.token,
			})

			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401 (body %q)", recorder.Code, recorder.Body.String())
			}
			if got := problem(t, recorder).Code; got != tc.wantCode {
				t.Errorf("problem code = %q, want %q", got, tc.wantCode)
			}
			if h.upstream.count() != before {
				t.Error("a request with an invalid token reached the upstream")
			}
		})
	}
}

// TestGatewayIgnoresNonBearerAuthorization: a Basic credential is not a session,
// and must leave the request anonymous rather than fail verification.
func TestGatewayIgnoresNonBearerAuthorization(t *testing.T) {
	h := newHarness(t)

	recorder := h.do(t, request{
		host:   "jobs." + testHostname,
		path:   "/api/v1/public/jobs",
		header: http.Header{"Authorization": {"Basic dXNlcjpwYXNz"}},
	})

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", recorder.Code, recorder.Body.String())
	}
	if got := h.upstream.last(t).Header.Get(httpx.HeaderPrincipalType); got != "" {
		t.Errorf("X-Principal-Type = %q, want empty", got)
	}
}

/* --------------------------------------------------------------- routing -- */

func TestGatewayUnknownRoute(t *testing.T) {
	tests := []struct {
		name string
		host string
		path string
	}{
		{name: "no such api", host: "acme." + testHostname, path: "/api/v1/nope"},
		{name: "root path", host: testHostname, path: "/"},
		{name: "traversal attempt", host: "acme." + testHostname, path: "/../etc/passwd"},
		{name: "near miss on a prefix", host: "acme." + testHostname, path: "/api/v1/jobsecret"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)

			recorder := h.do(t, request{host: tc.host, path: tc.path})

			if recorder.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404 (body %q)", recorder.Code, recorder.Body.String())
			}
			if h.upstream.count() != 0 {
				t.Error("an unrouted request reached the upstream")
			}
		})
	}
}

// TestGatewayStripsAPIPrefix: services expose /v1/..., the public surface is
// /api/v1/..., so the prefix is the gateway's and must not be forwarded.
func TestGatewayStripsAPIPrefix(t *testing.T) {
	h := newHarness(t)

	recorder := h.do(t, request{
		host:  "acme." + testHostname,
		path:  "/api/v1/jobs/job_1/applications",
		token: h.companyToken(t, acmeID),
	})

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", recorder.Code, recorder.Body.String())
	}
	if got := h.upstream.last(t).Path; got != "/v1/jobs/job_1/applications" {
		t.Errorf("upstream path = %q, want %q", got, "/v1/jobs/job_1/applications")
	}
}

/* ------------------------------------------------------------ rate limiting -- */

func TestGatewayRateLimit(t *testing.T) {
	h := newHarness(t)
	h.limiter.allowed = false
	h.limiter.retryAfter = 7 * time.Second

	recorder := h.do(t, request{host: "jobs." + testHostname, path: "/api/v1/public/jobs"})

	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429 (body %q)", recorder.Code, recorder.Body.String())
	}
	if got := problem(t, recorder).Code; got != "rate_limited" {
		t.Errorf("problem code = %q, want %q", got, "rate_limited")
	}
	if got := recorder.Header().Get("Retry-After"); got != "8" {
		t.Errorf("Retry-After = %q, want %q", got, "8")
	}
	if h.upstream.count() != 0 {
		t.Error("a rate-limited request reached the upstream")
	}
}

// TestGatewayRateLimiterFailsOpen: a cache outage should degrade protection,
// not take the platform offline.
func TestGatewayRateLimiterFailsOpen(t *testing.T) {
	h := newHarness(t)
	h.limiter.err = context.DeadlineExceeded

	recorder := h.do(t, request{host: "jobs." + testHostname, path: "/api/v1/public/jobs"})

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", recorder.Code, recorder.Body.String())
	}
}

// TestGatewayRateLimitKey: keying an authenticated caller by account keeps one
// busy user from throttling every colleague behind the same office address.
func TestGatewayRateLimitKey(t *testing.T) {
	h := newHarness(t)

	h.do(t, request{host: "jobs." + testHostname, path: "/api/v1/public/jobs"})
	h.do(t, request{
		host:  "acme." + testHostname,
		path:  "/api/v1/jobs",
		token: h.companyToken(t, acmeID),
	})

	keys := h.limiter.seen()
	if len(keys) != 2 {
		t.Fatalf("limiter saw %d keys, want 2", len(keys))
	}
	if !strings.HasPrefix(keys[0], "ip:") {
		t.Errorf("anonymous key = %q, want an ip: key", keys[0])
	}
	if keys[1] != "sub:usr_recruiter" {
		t.Errorf("authenticated key = %q, want %q", keys[1], "sub:usr_recruiter")
	}
}

// TestGatewayRateLimitIsAppliedAfterAuthorization proves the limiter is not the
// thing standing between an attacker and another tenant's data: a rejected
// request must not consume an allowance slot it could use to probe.
func TestGatewayRateLimitIsAppliedAfterAuthorization(t *testing.T) {
	h := newHarness(t)

	h.do(t, request{
		host:  "globex." + testHostname,
		path:  "/api/v1/jobs",
		token: h.companyToken(t, acmeID),
	})

	if got := h.limiter.seen(); len(got) != 0 {
		t.Errorf("limiter was consulted %v for a request rejected earlier in the chain", got)
	}
}

/* -------------------------------------------------------- known deviations -- */

// TestGatewaySetsCompanyHeaderForNonCompanyPrincipals documents current
// behaviour that does NOT match docs/contracts.md §1, which states X-Company-ID
// is "empty for platform and candidate principals".
//
// On a company portal the header is set from the resolved host tenant before
// the principal is considered, so a platform principal browsing a tenant's
// portal is handed that tenant's id. Nothing authorizes on it today —
// Principal.RequireCompany() refuses any principal that is not a company one —
// but a service that read the header directly would tenant-scope the wrong
// actor. This test exists to make that change visible when it is fixed.
func TestGatewaySetsCompanyHeaderForNonCompanyPrincipals(t *testing.T) {
	h := newHarness(t)

	recorder := h.do(t, request{
		host: "acme." + testHostname,
		path: "/api/v1/notifications",
		token: h.token(t, tokens.IssueInput{
			Subject:       "usr_ops",
			PrincipalType: tenancy.PrincipalPlatform,
			Permissions:   []string{"platform_companies.read"},
		}),
	})

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", recorder.Code, recorder.Body.String())
	}

	forwarded := h.upstream.last(t).Header
	if got := forwarded.Get(httpx.HeaderPrincipalType); got != string(tenancy.PrincipalPlatform) {
		t.Fatalf("X-Principal-Type = %q, want %q", got, tenancy.PrincipalPlatform)
	}
	if got := forwarded.Get(httpx.HeaderCompanyID); got != acmeID {
		t.Errorf("X-Company-ID = %q, want %q (see this test's comment)", got, acmeID)
	}
}
