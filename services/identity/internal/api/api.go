// Package api exposes the identity service over HTTP.
package api

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/karmarat/platform/packages/goshared/httpx"
	"github.com/karmarat/platform/packages/goshared/redisx"
	"github.com/karmarat/platform/packages/goshared/tenancy"
	"github.com/karmarat/platform/packages/goshared/tokens"
	"github.com/karmarat/platform/services/identity/internal/auth"
	"github.com/karmarat/platform/services/identity/internal/domain"
	"github.com/karmarat/platform/services/identity/internal/provisioning"
	"github.com/karmarat/platform/services/identity/internal/rbac"
	"github.com/karmarat/platform/services/identity/internal/store"
	"github.com/karmarat/platform/services/identity/internal/team"
)

// API wires the identity handlers.
type API struct {
	service       *auth.Service
	provisioner   *provisioning.Provisioner
	team          *team.Service
	store         *store.Store
	issuer        *tokens.Issuer
	verifier      *tokens.Verifier
	limiter       *redisx.RateLimiter
	logger        *slog.Logger
	internalToken string
}

// Config configures the API.
type Config struct {
	Service     *auth.Service
	Provisioner *provisioning.Provisioner
	Team        *team.Service
	Store       *store.Store
	Issuer      *tokens.Issuer
	Verifier    *tokens.Verifier
	Limiter     *redisx.RateLimiter
	Logger      *slog.Logger
	// InternalToken authenticates calls from other platform services.
	InternalToken string
}

// New builds the API.
func New(cfg Config) *API {
	return &API{
		service:       cfg.Service,
		provisioner:   cfg.Provisioner,
		team:          cfg.Team,
		store:         cfg.Store,
		issuer:        cfg.Issuer,
		verifier:      cfg.Verifier,
		limiter:       cfg.Limiter,
		logger:        cfg.Logger,
		internalToken: cfg.InternalToken,
	}
}

// Routes returns the service's HTTP handler.
func (a *API) Routes() http.Handler {
	mux := http.NewServeMux()

	// Public: the signing key every service verifies tokens against.
	mux.HandleFunc("GET /.well-known/jwks.json", a.handleJWKS)

	// Authentication. Sign-in paths are rate limited per address and per account.
	mux.Handle("POST /v1/auth/login", a.rateLimited("login", 10, time.Minute, http.HandlerFunc(a.handleLogin)))
	mux.Handle("POST /v1/auth/candidate/register",
		a.rateLimited("register", 5, time.Minute, http.HandlerFunc(a.handleRegisterCandidate)))
	mux.Handle("POST /v1/auth/refresh", a.rateLimited("refresh", 60, time.Minute, http.HandlerFunc(a.handleRefresh)))
	mux.HandleFunc("POST /v1/auth/logout", a.handleLogout)

	// Authenticated.
	authenticated := httpx.Authenticate(a.verifier)
	mux.Handle("GET /v1/auth/me", authenticated(httpx.RequireAuth(http.HandlerFunc(a.handleMe))))
	mux.Handle("POST /v1/auth/switch-company",
		authenticated(httpx.RequirePrincipal(tenancy.PrincipalCompany)(http.HandlerFunc(a.handleSwitchCompany))))
	mux.Handle("POST /v1/auth/logout-everywhere",
		authenticated(httpx.RequireAuth(http.HandlerFunc(a.handleLogoutEverywhere))))
	mux.Handle("GET /v1/auth/sessions",
		authenticated(httpx.RequireAuth(http.HandlerFunc(a.handleListSessions))))

	// The permission catalogue that drives every role editor.
	//
	// Authenticated, though it holds no tenant data: the gateway decides which
	// routes a portal may reach, not whether the caller is anybody at all, and a
	// handler that assumes the gateway asked is a handler that is open the day
	// this service is reachable by anything else.
	mux.Handle("GET /v1/rbac/catalogue",
		authenticated(httpx.RequireAuth(http.HandlerFunc(a.handleCatalogue))))

	// A company administering its own team and roles. Each route carries the
	// permission it needs; the handlers are in team.go.
	mux.Handle("GET /v1/recruiters",
		a.companyRoute("recruiters.read", a.handleListRecruiters))
	mux.Handle("POST /v1/recruiters",
		a.companyRoute("recruiters.create", a.handleAddRecruiter))
	mux.Handle("PATCH /v1/recruiters/{accountID}",
		a.companyRoute("recruiters.update", a.handleUpdateRecruiter))
	mux.Handle("PUT /v1/recruiters/{accountID}/roles",
		a.companyRoute("recruiters.assign_roles", a.handleAssignRecruiterRoles))
	mux.Handle("PATCH /v1/recruiters/{accountID}/status",
		a.companyRoute("recruiters.manage_status", a.handleSetRecruiterStatus))
	mux.Handle("DELETE /v1/recruiters/{accountID}",
		a.companyRoute("recruiters.delete", a.handleRemoveRecruiter))

	mux.Handle("GET /v1/company-roles",
		a.companyRoute("company_roles.read", a.handleListCompanyRoles))
	mux.Handle("POST /v1/company-roles",
		a.companyRoute("company_roles.create", a.handleCreateCompanyRole))
	mux.Handle("PATCH /v1/company-roles/{roleID}",
		a.companyRoute("company_roles.update", a.handleUpdateCompanyRole))
	mux.Handle("DELETE /v1/company-roles/{roleID}",
		a.companyRoute("company_roles.delete", a.handleDeleteCompanyRole))

	// Internal: called by the gateway and by other services, never by a browser.
	mux.Handle("GET /internal/portal/resolve", a.internal(http.HandlerFunc(a.handleResolvePortal)))
	mux.Handle("POST /internal/companies", a.internal(http.HandlerFunc(a.handleProvisionCompany)))
	mux.Handle("GET /internal/companies/{companyID}/members",
		a.internal(http.HandlerFunc(a.handleListCompanyMembers)))
	mux.Handle("PATCH /internal/companies/{companyID}/subscription",
		a.internal(http.HandlerFunc(a.handleUpdateSubscription)))

	return mux
}

/* -------------------------------------------------------------------------- */
/* Authentication                                                             */
/* -------------------------------------------------------------------------- */

type loginRequest struct {
	Realm       string `json:"realm"`
	Email       string `json:"email"`
	Password    string `json:"password"`
	CompanySlug string `json:"companySlug,omitempty"`
}

func (a *API) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	fields := map[string][]string{}
	if strings.TrimSpace(req.Email) == "" {
		fields["email"] = []string{"Email is required."}
	}
	if req.Password == "" {
		fields["password"] = []string{"Password is required."}
	}
	realm := domain.Realm(strings.TrimSpace(req.Realm))
	if !realm.Valid() {
		fields["realm"] = []string{"Realm must be one of: platform, company, candidate."}
	}
	if realm == domain.RealmCompany && strings.TrimSpace(req.CompanySlug) == "" {
		fields["companySlug"] = []string{"A company portal is required to sign in."}
	}
	if len(fields) > 0 {
		httpx.WriteProblem(w, r, httpx.ValidationFailed(fields))
		return
	}

	result, err := a.service.Login(r.Context(), auth.LoginInput{
		Realm:       realm,
		Email:       req.Email,
		Password:    req.Password,
		CompanySlug: req.CompanySlug,
	}, requestContext(r))
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	httpx.WriteJSON(w, http.StatusOK, result)
}

type registerCandidateRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	FullName string `json:"fullName"`
}

func (a *API) handleRegisterCandidate(w http.ResponseWriter, r *http.Request) {
	var req registerCandidateRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	fields := map[string][]string{}
	if !looksLikeEmail(req.Email) {
		fields["email"] = []string{"A valid email address is required."}
	}
	if strings.TrimSpace(req.FullName) == "" {
		fields["fullName"] = []string{"Your name is required."}
	}
	if len(fields) > 0 {
		httpx.WriteProblem(w, r, httpx.ValidationFailed(fields))
		return
	}

	result, err := a.service.RegisterCandidate(r.Context(), auth.RegisterCandidateInput{
		Email:    req.Email,
		Password: req.Password,
		FullName: req.FullName,
	}, requestContext(r))
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	httpx.WriteJSON(w, http.StatusCreated, result)
}

type refreshRequest struct {
	RefreshToken string `json:"refreshToken"`
}

func (a *API) handleRefresh(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}
	if strings.TrimSpace(req.RefreshToken) == "" {
		httpx.WriteProblem(w, r, httpx.BadRequest("A refresh token is required."))
		return
	}

	result, err := a.service.Refresh(r.Context(), req.RefreshToken, requestContext(r))
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	httpx.WriteJSON(w, http.StatusOK, result)
}

func (a *API) handleLogout(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	if err := a.service.Logout(r.Context(), req.RefreshToken, requestContext(r)); err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	httpx.NoContent(w)
}

func (a *API) handleLogoutEverywhere(w http.ResponseWriter, r *http.Request) {
	principal := tenancy.MustFromContext(r.Context())

	if err := a.service.LogoutEverywhere(r.Context(), principal.Subject, "user_requested"); err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	httpx.NoContent(w)
}

type switchCompanyRequest struct {
	CompanySlug string `json:"companySlug"`
}

func (a *API) handleSwitchCompany(w http.ResponseWriter, r *http.Request) {
	var req switchCompanyRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	principal := tenancy.MustFromContext(r.Context())
	result, err := a.service.SwitchCompany(r.Context(), principal, req.CompanySlug, requestContext(r))
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	httpx.WriteJSON(w, http.StatusOK, result)
}

func (a *API) handleMe(w http.ResponseWriter, r *http.Request) {
	principal := tenancy.MustFromContext(r.Context())

	account, err := a.store.FindAccountByID(r.Context(), principal.Subject)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	response := map[string]any{
		"accountId":   account.ID,
		"email":       account.Email,
		"fullName":    account.FullName,
		"realm":       account.Realm,
		"status":      account.Status,
		"roles":       principal.Roles,
		"permissions": principal.Permissions,
		"companyId":   principal.CompanyID,
	}

	if account.Realm == domain.RealmCompany {
		memberships, err := a.store.ListMembershipsForAccount(r.Context(), account.ID)
		if err == nil {
			summaries := make([]auth.CompanySummary, 0, len(memberships))
			for _, cm := range memberships {
				if cm.Membership.Status != domain.MembershipActive {
					continue
				}
				summaries = append(summaries, auth.CompanySummary{
					ID:              cm.Company.ID,
					Slug:            cm.Company.Slug,
					Name:            cm.Company.Name,
					PortalAvailable: cm.Company.PortalAvailable(),
					IsOwner:         cm.Membership.IsOwner,
				})
			}
			response["companies"] = summaries
		}
	}

	httpx.WriteJSON(w, http.StatusOK, response)
}

func (a *API) handleListSessions(w http.ResponseWriter, r *http.Request) {
	principal := tenancy.MustFromContext(r.Context())

	sessions, err := a.store.ListAccountSessions(r.Context(), principal.Subject)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	type sessionView struct {
		ID        string    `json:"id"`
		Current   bool      `json:"current"`
		CompanyID string    `json:"companyId,omitempty"`
		IPAddress string    `json:"ipAddress,omitempty"`
		UserAgent string    `json:"userAgent,omitempty"`
		CreatedAt time.Time `json:"createdAt"`
		LastUsed  time.Time `json:"lastUsedAt"`
	}

	views := make([]sessionView, 0, len(sessions))
	for _, session := range sessions {
		views = append(views, sessionView{
			ID:        session.ID,
			Current:   session.ID == principal.SessionID,
			CompanyID: session.CompanyID,
			IPAddress: session.IPAddress,
			UserAgent: session.UserAgent,
			CreatedAt: session.CreatedAt,
			LastUsed:  session.LastUsedAt,
		})
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"sessions": views})
}

/* -------------------------------------------------------------------------- */
/* Keys and catalogue                                                         */
/* -------------------------------------------------------------------------- */

func (a *API) handleJWKS(w http.ResponseWriter, r *http.Request) {
	pem, err := a.issuer.PublicKeyPEM()
	if err != nil {
		httpx.WriteProblem(w, r, httpx.Internal("Signing key is unavailable."))
		return
	}

	// Services fetch this at boot and cache it; the key rarely changes.
	w.Header().Set("Cache-Control", "public, max-age=300")
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"keyId":        a.issuer.KeyID(),
		"algorithm":    "RS256",
		"publicKeyPem": string(pem),
	})
}

// handleCatalogue returns the permission registry so a role editor can render
// itself without hard-coding a single permission name.
func (a *API) handleCatalogue(w http.ResponseWriter, r *http.Request) {
	scope := rbac.Scope(r.URL.Query().Get("scope"))
	if scope == "" {
		scope = rbac.ScopeCompany
	}

	features := rbac.FeaturesForScope(scope)
	type actionView struct {
		Key         string `json:"key"`
		Action      string `json:"action"`
		Label       string `json:"label"`
		Description string `json:"description"`
		Sensitive   bool   `json:"sensitive"`
	}
	type featureView struct {
		Key         string       `json:"key"`
		Name        string       `json:"name"`
		Description string       `json:"description"`
		Group       string       `json:"group"`
		Actions     []actionView `json:"actions"`
	}

	views := make([]featureView, 0, len(features))
	for _, feature := range features {
		actions := make([]actionView, 0, len(feature.Actions))
		for _, action := range feature.Actions {
			actions = append(actions, actionView{
				Key:         feature.Key + "." + action.Name,
				Action:      action.Name,
				Label:       action.Label,
				Description: action.Description,
				Sensitive:   action.Sensitive,
			})
		}
		views = append(views, featureView{
			Key: feature.Key, Name: feature.Name,
			Description: feature.Description, Group: feature.Group, Actions: actions,
		})
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"scope":    scope,
		"features": views,
	})
}

/* -------------------------------------------------------------------------- */
/* Internal endpoints                                                         */
/* -------------------------------------------------------------------------- */

// handleResolvePortal tells the gateway whether a company slug is routable.
func (a *API) handleResolvePortal(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimSpace(r.URL.Query().Get("slug"))
	if slug == "" {
		httpx.WriteProblem(w, r, httpx.BadRequest("A company slug is required."))
		return
	}

	company, err := a.store.FindCompanyBySlug(r.Context(), slug)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"companyId":         company.ID,
		"slug":              company.Slug,
		"name":              company.Name,
		"state":             company.State,
		"subscriptionState": company.SubscriptionState,
		"portalAvailable":   company.PortalAvailable(),
	})
}

// handleListCompanyMembers answers "who works at this company, and what may
// they do?".
//
// Notifications needs it to address a tenant-scoped event to actual people: an
// event names a company, and a company is not an inbox. Without this, a service
// can only learn a roster by watching traffic, which silently excludes anyone
// who has not signed in recently — the people most in need of being told.
func (a *API) handleListCompanyMembers(w http.ResponseWriter, r *http.Request) {
	members, err := a.store.ListCompanyMembers(r.Context(), r.PathValue("companyID"))
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	// An empty roster is an empty list, not a 404: the caller asked a question
	// with a legitimate answer, and a company with no active members is a real
	// state rather than a missing one.
	if members == nil {
		members = []store.CompanyMember{}
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"members": members})
}

func (a *API) handleProvisionCompany(w http.ResponseWriter, r *http.Request) {
	var req provisioning.ProvisionCompanyInput
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	result, err := a.provisioner.ProvisionCompany(r.Context(), req)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	httpx.WriteJSON(w, http.StatusCreated, result)
}

type subscriptionUpdateRequest struct {
	State        string         `json:"state"`
	ExpiresAt    *time.Time     `json:"expiresAt"`
	Entitlements map[string]any `json:"entitlements"`
}

func (a *API) handleUpdateSubscription(w http.ResponseWriter, r *http.Request) {
	companyID := r.PathValue("companyID")

	var req subscriptionUpdateRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	if err := a.provisioner.UpdateSubscription(r.Context(),
		companyID, domain.SubscriptionState(req.State), req.ExpiresAt, req.Entitlements); err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	httpx.NoContent(w)
}

/* -------------------------------------------------------------------------- */
/* Middleware and helpers                                                     */
/* -------------------------------------------------------------------------- */

// internal guards service-to-service endpoints with a shared secret.
//
// These endpoints can provision tenants and change subscription state, so they
// must never be reachable from the public internet.
func (a *API) internal(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.internalToken == "" {
			httpx.WriteProblem(w, r, httpx.Internal("Internal API is not configured."))
			return
		}

		presented := strings.TrimSpace(r.Header.Get("X-Internal-Token"))
		if subtleCompare(presented, a.internalToken) != 1 {
			httpx.WriteProblem(w, r, httpx.Unauthorized("Invalid internal credentials."))
			return
		}

		next.ServeHTTP(w, r)
	})
}

// rateLimited applies a sliding-window limit keyed on the client address.
func (a *API) rateLimited(bucket string, limit int, window time.Duration, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.limiter == nil {
			next.ServeHTTP(w, r)
			return
		}

		key := bucket + ":" + clientIP(r)
		result, err := a.limiter.Allow(r.Context(), key, limit, window)
		if err != nil {
			// Redis being down must not lock everyone out of signing in.
			a.logger.Warn("rate limiter unavailable, allowing request", slog.Any("error", err))
			next.ServeHTTP(w, r)
			return
		}

		w.Header().Set("X-RateLimit-Limit", itoa(result.Limit))
		w.Header().Set("X-RateLimit-Remaining", itoa(result.Remaining))

		if !result.Allowed {
			w.Header().Set("Retry-After", itoa(int(result.RetryAfter.Seconds())+1))
			httpx.WriteProblem(w, r, httpx.TooManyRequests(
				"Too many attempts. Please wait a moment and try again."))
			return
		}

		next.ServeHTTP(w, r)
	})
}

// subtleCompare is a constant-time string comparison for shared secrets.
func subtleCompare(a, b string) int {
	if len(a) != len(b) {
		// Still run a comparison so length alone does not shortcut the timing.
		subtle.ConstantTimeCompare([]byte(a), []byte(a))
		return 0
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b))
}

func decodeJSON(r *http.Request, dst any) error {
	// A bounded reader keeps a malicious client from exhausting memory.
	limited := http.MaxBytesReader(nil, r.Body, 1<<20)
	decoder := json.NewDecoder(limited)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return httpx.BadRequest("A JSON request body is required.")
		}
		// encoding/json spells out internal type names ("cannot unmarshal string
		// into Go value of type api.loginRequest"), which hands a caller a map of
		// the server's internals. Say what is wrong without saying what we are.
		var maxBytes *http.MaxBytesError
		if errors.As(err, &maxBytes) {
			return httpx.BadRequest("The request body is too large.")
		}
		var syntax *json.SyntaxError
		if errors.As(err, &syntax) {
			return httpx.BadRequest(fmt.Sprintf("The request body is not valid JSON (at byte %d).", syntax.Offset))
		}
		return httpx.BadRequest("The request body does not match the expected shape.")
	}
	return nil
}

// mapError turns a domain error into the right HTTP problem.
func mapError(err error) error {
	var policyErr *auth.PasswordPolicyError
	if errors.As(err, &policyErr) {
		return httpx.ValidationFailed(map[string][]string{"password": policyErr.Problems})
	}

	var validationErr *domain.ValidationError
	if errors.As(err, &validationErr) {
		field := validationErr.Field
		if field == "" {
			field = "request"
		}
		return httpx.ValidationFailed(map[string][]string{field: {validationErr.Message}})
	}

	switch {
	case errors.Is(err, domain.ErrInvalidCredentials):
		return httpx.NewProblem(http.StatusUnauthorized, "invalid_credentials",
			"Unauthorized", domain.ErrInvalidCredentials.Error())
	case errors.Is(err, domain.ErrAccountLocked):
		return httpx.NewProblem(http.StatusTooManyRequests, "account_locked",
			"Too Many Requests", domain.ErrAccountLocked.Error())
	case errors.Is(err, domain.ErrAccountInactive):
		return httpx.NewProblem(http.StatusForbidden, "account_inactive",
			"Forbidden", domain.ErrAccountInactive.Error())
	case errors.Is(err, domain.ErrEmailTaken):
		return httpx.Conflict("email_taken", domain.ErrEmailTaken.Error())
	case errors.Is(err, domain.ErrSlugTaken):
		return httpx.Conflict("slug_taken", domain.ErrSlugTaken.Error())
	case errors.Is(err, domain.ErrAccountNotFound), errors.Is(err, domain.ErrCompanyNotFound),
		errors.Is(err, domain.ErrMemberNotFound), errors.Is(err, domain.ErrRoleNotFound):
		return httpx.NotFound(err.Error())
	case errors.Is(err, domain.ErrNoMembership), errors.Is(err, domain.ErrMembershipInactive):
		return httpx.NewProblem(http.StatusForbidden, "no_company_access", "Forbidden", err.Error())
	case errors.Is(err, domain.ErrCompanyUnavailable):
		return httpx.NewProblem(http.StatusForbidden, "portal_unavailable", "Forbidden", err.Error())
	case errors.Is(err, domain.ErrSessionNotFound), errors.Is(err, domain.ErrSessionExpired):
		return httpx.NewProblem(http.StatusUnauthorized, "session_invalid",
			"Unauthorized", "Your session is no longer valid. Please sign in again.")
	case errors.Is(err, domain.ErrPrivilegeEscalation), errors.Is(err, domain.ErrSelfModification):
		return httpx.NewProblem(http.StatusForbidden, "not_permitted", "Forbidden", err.Error())
	case errors.Is(err, domain.ErrRoleInUse), errors.Is(err, domain.ErrRoleImmutable),
		errors.Is(err, domain.ErrLastOwner):
		return httpx.Conflict("conflict", err.Error())
	default:
		return err
	}
}

func requestContext(r *http.Request) auth.RequestContext {
	return auth.RequestContext{
		IPAddress: clientIP(r),
		UserAgent: r.UserAgent(),
	}
}

func clientIP(r *http.Request) string {
	// The gateway is the only hop that may set this; it overwrites whatever the
	// client sent, so the left-most entry is trustworthy here.
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		if first, _, found := strings.Cut(forwarded, ","); found {
			return strings.TrimSpace(first)
		}
		return strings.TrimSpace(forwarded)
	}
	if realIP := r.Header.Get("X-Real-IP"); realIP != "" {
		return strings.TrimSpace(realIP)
	}
	host, _, found := strings.Cut(r.RemoteAddr, ":")
	if !found {
		return r.RemoteAddr
	}
	return host
}

func looksLikeEmail(value string) bool {
	value = strings.TrimSpace(value)
	at := strings.Index(value, "@")
	dot := strings.LastIndex(value, ".")
	return at > 0 && dot > at+1 && dot < len(value)-1 && !strings.Contains(value, " ")
}

func itoa(n int) string {
	if n <= 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
