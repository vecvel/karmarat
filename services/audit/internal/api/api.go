// Package api exposes the audit service's two read surfaces over HTTP.
//
// There is no write surface. Entries arrive from the event stream, so every
// handler here is a query, and the only thing a handler can get wrong is which
// rows it returns.
package api

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/karmarat/platform/packages/goshared/httpx"
	"github.com/karmarat/platform/packages/goshared/tenancy"
	"github.com/karmarat/platform/services/audit/internal/domain"
	"github.com/karmarat/platform/services/audit/internal/store"
)

// API wires the audit handlers.
type API struct {
	store  *store.Store
	logger *slog.Logger
}

// Config configures the API.
type Config struct {
	Store  *store.Store
	Logger *slog.Logger
}

// New builds the API.
func New(cfg Config) *API {
	return &API{store: cfg.Store, logger: cfg.Logger}
}

// Routes returns the service's HTTP handler.
//
// Guard order is the platform's: reconstruct the principal from the gateway's
// headers, check it is the right kind, check the permission, and only then run a
// handler that filters by tenant anyway. The permission answers "may this role
// do this"; the tenant filter answers "to whose data".
//
// The company trail and the platform trail are separate routes rather than one
// route that widens for platform staff. One handler serving both would be a
// single forgotten branch away from showing a company every other company's
// activity, and that mistake is not one the trail itself would record.
func (a *API) Routes() http.Handler {
	mux := http.NewServeMux()

	/* ------------------------------------------------------------ company -- */

	mux.Handle("GET /v1/company-audit", a.company("company_audit.read", a.handleCompanyList))
	mux.Handle("GET /v1/company-audit/export", a.company("company_audit.export", a.handleCompanyExport))

	/* ----------------------------------------------------------- platform -- */

	mux.Handle("GET /v1/platform/audit", a.platform("platform_audit.read", a.handlePlatformList))

	return httpx.TrustGatewayHeaders(mux)
}

func (a *API) company(permission string, handler http.HandlerFunc) http.Handler {
	return httpx.RequirePrincipal(tenancy.PrincipalCompany)(
		httpx.RequirePermission(permission)(handler))
}

// platform guards the cross-tenant feed.
//
// The gateway already exposes /api/v1/platform/audit on the root portal only,
// and that is a real boundary — but it decides which routes a portal may reach,
// not which actions a person may take. A call arriving here from anywhere else,
// including another service, must still be refused, so the principal type is
// checked at this door too.
func (a *API) platform(permission string, handler http.HandlerFunc) http.Handler {
	return httpx.RequirePrincipal(tenancy.PrincipalPlatform)(
		httpx.RequirePermission(permission)(handler))
}

// tenantOf returns the company the caller is acting in.
//
// It is the only source of a tenant in this service. Nothing reads a company
// from a path, a query or a body — the filter type has no field for one — so
// there is no handler in which the wrong tenant can be supplied.
func tenantOf(r *http.Request) (string, error) {
	principal := tenancy.MustFromContext(r.Context())
	companyID, err := principal.RequireCompany()
	if err != nil {
		return "", httpx.Forbidden("This endpoint requires a company context.")
	}
	return companyID, nil
}

/* -------------------------------------------------------------------------- */
/* Helpers                                                                    */
/* -------------------------------------------------------------------------- */

// filterFrom reads the query string every trail accepts.
//
// All three endpoints share it, so the export cannot interpret `?from=` one way
// while the list it was downloaded from interpreted it another.
func filterFrom(r *http.Request) (domain.Filter, error) {
	limit, cursor, err := pagination(r)
	if err != nil {
		return domain.Filter{}, err
	}

	page, err := domain.NewPage(limit, cursor)
	if err != nil {
		return domain.Filter{}, mapError(err)
	}

	query := r.URL.Query()
	filter, err := domain.NewFilter(
		query.Get("action"),
		query.Get("entityType"),
		query.Get("entityId"),
		query.Get("actorId"),
		query.Get("from"),
		query.Get("to"),
		page,
	)
	if err != nil {
		return domain.Filter{}, mapError(err)
	}
	return filter, nil
}

// pagination reads the platform's ?limit=&cursor= convention.
func pagination(r *http.Request) (int, string, error) {
	limit := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			return 0, "", httpx.ValidationFailed(map[string][]string{
				"limit": {"Limit must be a positive whole number."}})
		}
		if parsed > domain.MaxPageLimit {
			return 0, "", httpx.ValidationFailed(map[string][]string{
				"limit": {"Limit may not exceed 100."}})
		}
		limit = parsed
	}
	return limit, strings.TrimSpace(r.URL.Query().Get("cursor")), nil
}

// mapError turns a domain error into the right HTTP problem.
//
// Anything unrecognized falls through to httpx, which reports a generic 500 and
// logs the detail — an internal message never reaches a client.
func mapError(err error) error {
	var validationErr *domain.ValidationError
	if errors.As(err, &validationErr) {
		field := validationErr.Field
		if field == "" {
			field = "request"
		}
		return httpx.ValidationFailed(map[string][]string{field: {validationErr.Message}})
	}

	switch {
	case errors.Is(err, domain.ErrInvalidCursor):
		return httpx.ValidationFailed(map[string][]string{
			"cursor": {domain.ErrInvalidCursor.Error()}})

	case errors.Is(err, tenancy.ErrNotCompanyScoped), errors.Is(err, tenancy.ErrCrossTenant):
		return httpx.Forbidden("This endpoint requires a company context.")

	default:
		return err
	}
}
