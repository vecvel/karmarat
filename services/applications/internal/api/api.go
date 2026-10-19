// Package api exposes the applications service over HTTP.
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/karmarat/platform/packages/goshared/httpx"
	"github.com/karmarat/platform/packages/goshared/tenancy"
	"github.com/karmarat/platform/services/applications/internal/candidates"
	"github.com/karmarat/platform/services/applications/internal/domain"
	"github.com/karmarat/platform/services/applications/internal/jobs"
	"github.com/karmarat/platform/services/applications/internal/store"
)

// API wires the applications handlers.
type API struct {
	store      *store.Store
	jobs       *jobs.Client
	candidates *candidates.Client
	logger     *slog.Logger
}

// Config configures the API.
type Config struct {
	Store      *store.Store
	Jobs       *jobs.Client
	Candidates *candidates.Client
	Logger     *slog.Logger
}

// New builds the API.
func New(cfg Config) *API {
	return &API{store: cfg.Store, jobs: cfg.Jobs, candidates: cfg.Candidates, logger: cfg.Logger}
}

// Routes returns the service's HTTP handler.
//
// Guard order is the platform's: reconstruct the principal from the gateway's
// headers, check it is the right kind, check the permission, and only then run a
// handler that filters by tenant anyway. The permission answers "may this role
// do this"; the tenant filter answers "to whose data".
func (a *API) Routes() http.Handler {
	mux := http.NewServeMux()

	/* ------------------------------------------------------------ company -- */

	mux.Handle("GET /v1/applications", a.company("applications.read", a.handleList))
	mux.Handle("GET /v1/applications/export", a.company("applications.export", a.handleExport))
	mux.Handle("POST /v1/applications/bulk", a.company("applications.bulk_update", a.handleBulk))
	mux.Handle("GET /v1/applications/{id}", a.company("applications.read", a.handleGet))
	mux.Handle("PATCH /v1/applications/{id}", a.company("applications.update", a.handlePatch))
	mux.Handle("DELETE /v1/applications/{id}", a.company("applications.delete", a.handleDelete))
	mux.Handle("GET /v1/applications/{id}/events", a.company("applications.read", a.handleHistory))
	mux.Handle("POST /v1/applications/{id}/advance", a.company("applications.advance_stage", a.handleAdvance))
	mux.Handle("POST /v1/applications/{id}/reject", a.company("applications.reject", a.handleReject))

	// Stage and reason management is mounted twice: once at the path the service
	// contract names, and once under the applications prefix, which is the only
	// prefix the gateway currently routes to this service. Both reach the same
	// handlers, so neither becomes a second implementation.
	a.mountPipelineSettings(mux, "/v1")
	a.mountPipelineSettings(mux, "/v1/applications/settings")

	/* ---------------------------------------------------------- candidate -- */

	mux.Handle("GET /v1/my-applications", a.candidate("candidate_applications.read", a.handleMyApplications))
	mux.Handle("GET /v1/my-applications/{id}", a.candidate("candidate_applications.read", a.handleMyApplication))
	mux.Handle("POST /v1/my-applications/{id}/withdraw",
		a.candidate("candidate_applications.withdraw", a.handleWithdraw))

	/* ------------------------------------------------------------- public -- */

	// "Public" is the gateway's route name, not the guard: applying requires a
	// signed-in candidate, which is what ties an application to one person and
	// makes the one-application rule enforceable at all.
	mux.Handle("POST /v1/public/apply", a.candidate("candidate_applications.create", a.handleApply))

	return httpx.TrustGatewayHeaders(mux)
}

func (a *API) mountPipelineSettings(mux *http.ServeMux, prefix string) {
	mux.Handle("GET "+prefix+"/stages", a.company("applications.read", a.handleListStages))
	mux.Handle("POST "+prefix+"/stages", a.company("applications.manage_stages", a.handleCreateStage))
	mux.Handle("POST "+prefix+"/stages/reorder", a.company("applications.manage_stages", a.handleReorderStages))
	mux.Handle("PATCH "+prefix+"/stages/{id}", a.company("applications.manage_stages", a.handleUpdateStage))
	mux.Handle("DELETE "+prefix+"/stages/{id}", a.company("applications.manage_stages", a.handleDeleteStage))

	mux.Handle("GET "+prefix+"/rejection-reasons", a.company("applications.read", a.handleListReasons))
	mux.Handle("POST "+prefix+"/rejection-reasons", a.company("applications.manage_stages", a.handleCreateReason))
	mux.Handle("PATCH "+prefix+"/rejection-reasons/{id}", a.company("applications.manage_stages", a.handleUpdateReason))
	mux.Handle("DELETE "+prefix+"/rejection-reasons/{id}", a.company("applications.manage_stages", a.handleDeleteReason))
}

func (a *API) company(permission string, handler http.HandlerFunc) http.Handler {
	return httpx.RequirePrincipal(tenancy.PrincipalCompany)(
		httpx.RequirePermission(permission)(handler))
}

func (a *API) candidate(permission string, handler http.HandlerFunc) http.Handler {
	return httpx.RequirePrincipal(tenancy.PrincipalCandidate)(
		httpx.RequirePermission(permission)(handler))
}

// tenantOf returns the company the caller is acting in.
//
// It is the only source of a tenant in this service. Nothing reads a company
// from a path, a query or a body, so there is no handler in which the wrong one
// can be supplied.
func tenantOf(r *http.Request) (string, tenancy.Principal, error) {
	principal := tenancy.MustFromContext(r.Context())
	companyID, err := principal.RequireCompany()
	if err != nil {
		return "", principal, httpx.Forbidden("This endpoint requires a company context.")
	}
	return companyID, principal, nil
}

/* -------------------------------------------------------------------------- */
/* Helpers                                                                    */
/* -------------------------------------------------------------------------- */

func decodeJSON(r *http.Request, dst any) error {
	// A bounded reader keeps a malicious client from exhausting memory. An
	// application carries free-text answers, so the cap is generous but finite.
	limited := http.MaxBytesReader(nil, r.Body, 1<<20)
	decoder := json.NewDecoder(limited)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return httpx.BadRequest("A JSON request body is required.")
		}
		// encoding/json spells out internal type names, which hands a caller a
		// map of the server's internals. Say what is wrong, not what we are.
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

// pagination reads the platform's ?limit=&cursor= convention.
func pagination(r *http.Request) (int, string, error) {
	limit := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			return 0, "", httpx.ValidationFailed(map[string][]string{
				"limit": {"Limit must be a positive whole number."}})
		}
		if parsed > 100 {
			return 0, "", httpx.ValidationFailed(map[string][]string{
				"limit": {"Limit may not exceed 100."}})
		}
		limit = parsed
	}
	return limit, strings.TrimSpace(r.URL.Query().Get("cursor")), nil
}

func parseDate(raw, field string) (*time.Time, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}
	if parsed, err := time.Parse(time.RFC3339, trimmed); err == nil {
		return &parsed, nil
	}
	parsed, err := time.Parse(time.DateOnly, trimmed)
	if err != nil {
		return nil, httpx.ValidationFailed(map[string][]string{
			field: {"Use a date in YYYY-MM-DD form or a full RFC 3339 timestamp."}})
	}
	return &parsed, nil
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
	case errors.Is(err, domain.ErrAlreadyApplied):
		return httpx.Conflict("already_applied", domain.ErrAlreadyApplied.Error())
	case errors.Is(err, domain.ErrAlreadyClosed):
		return httpx.Conflict("already_closed", domain.ErrAlreadyClosed.Error())
	case errors.Is(err, domain.ErrNotWithdrawable):
		return httpx.Conflict("not_withdrawable", domain.ErrNotWithdrawable.Error())
	case errors.Is(err, domain.ErrStageInUse):
		return httpx.Conflict("stage_in_use", domain.ErrStageInUse.Error())
	case errors.Is(err, domain.ErrLastStage):
		return httpx.Conflict("last_stage", domain.ErrLastStage.Error())
	case errors.Is(err, domain.ErrStageKeyTaken):
		return httpx.Conflict("stage_exists", domain.ErrStageKeyTaken.Error())
	case errors.Is(err, domain.ErrReasonInUse):
		return httpx.Conflict("reason_in_use", domain.ErrReasonInUse.Error())
	case errors.Is(err, domain.ErrReasonLabelTaken):
		return httpx.Conflict("reason_exists", domain.ErrReasonLabelTaken.Error())
	case errors.Is(err, domain.ErrReasonInactive):
		return httpx.ValidationFailed(map[string][]string{"reasonId": {domain.ErrReasonInactive.Error()}})
	case errors.Is(err, domain.ErrJobNotAccepting):
		return httpx.Conflict("job_closed", domain.ErrJobNotAccepting.Error())

	// A record another tenant owns is reported as missing rather than
	// forbidden: 403 would confirm that the id exists.
	case errors.Is(err, domain.ErrApplicationNotFound),
		errors.Is(err, domain.ErrStageNotFound),
		errors.Is(err, domain.ErrReasonNotFound),
		errors.Is(err, domain.ErrJobNotFound):
		return httpx.NotFound(err.Error())

	case errors.Is(err, jobs.ErrUnavailable):
		return httpx.NewProblem(http.StatusServiceUnavailable, "upstream_unavailable",
			"Service Unavailable", "Applications cannot be accepted right now. Please try again shortly.")

	case errors.Is(err, tenancy.ErrNotCompanyScoped), errors.Is(err, tenancy.ErrCrossTenant):
		return httpx.Forbidden("This endpoint requires a company context.")

	default:
		return err
	}
}
