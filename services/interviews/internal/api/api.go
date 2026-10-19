// Package api exposes the interviews service over HTTP.
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
	"github.com/karmarat/platform/services/interviews/internal/domain"
	"github.com/karmarat/platform/services/interviews/internal/store"
)

// Permission keys this service guards with. They already exist in the identity
// service's registry; nothing here invents one.
const (
	permRead           = "interviews.read"
	permCreate         = "interviews.create"
	permUpdate         = "interviews.update"
	permDelete         = "interviews.delete"
	permSubmitScore    = "interviews.submit_scorecard"
	permViewScorecards = "interviews.view_scorecards"
)

// API wires the interviews handlers.
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
func (a *API) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.Handle("GET /v1/interviews", a.company(permRead, a.handleList))
	mux.Handle("POST /v1/interviews", a.company(permCreate, a.handleCreate))
	mux.Handle("GET /v1/interviews/{id}", a.company(permRead, a.handleGet))
	mux.Handle("PATCH /v1/interviews/{id}", a.company(permUpdate, a.handlePatch))
	mux.Handle("POST /v1/interviews/{id}/cancel", a.company(permUpdate, a.handleCancel))
	mux.Handle("POST /v1/interviews/{id}/complete", a.company(permUpdate, a.handleComplete))
	mux.Handle("DELETE /v1/interviews/{id}", a.company(permDelete, a.handleDelete))

	mux.Handle("POST /v1/interviews/{id}/scorecard", a.company(permSubmitScore, a.handleSubmitScorecard))

	// This route is the one place in the service where the guard is deliberately
	// wider than the endpoint's nominal permission.
	//
	// `interviews.view_scorecards` is what grants a reading of the whole panel's
	// feedback, and it is what this endpoint is named for. But an interviewer
	// holding only `interviews.submit_scorecard` has to be able to read their own
	// card back — feedback you cannot re-read is feedback you cannot correct —
	// and they reach it through this same URL. Refusing them here and adding a
	// second "my scorecard" route would put the visibility rule in two places,
	// which for this rule means one of them eventually leaking a colleague's
	// verdict.
	//
	// So the guard admits either key and the handler narrows the result through
	// domain.MayReadScorecard. Holding only submit_scorecard yields exactly one
	// card — your own — and no hint that the others exist.
	mux.Handle("GET /v1/interviews/{id}/scorecards",
		a.companyAny([]string{permViewScorecards, permSubmitScore}, a.handleListScorecards))

	return httpx.TrustGatewayHeaders(mux)
}

func (a *API) company(permission string, handler http.HandlerFunc) http.Handler {
	return httpx.RequirePrincipal(tenancy.PrincipalCompany)(
		httpx.RequirePermission(permission)(handler))
}

func (a *API) companyAny(permissions []string, handler http.HandlerFunc) http.Handler {
	return httpx.RequirePrincipal(tenancy.PrincipalCompany)(
		httpx.RequireAnyPermission(permissions...)(handler))
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

// viewerOf projects a principal down to the three facts scorecard access turns
// on, so the decision itself stays a pure function in the domain package.
func viewerOf(principal tenancy.Principal) domain.Viewer {
	return domain.Viewer{
		AccountID: principal.Subject,
		ViewAll:   principal.Can(permViewScorecards),
		Submit:    principal.Can(permSubmitScore),
	}
}

/* -------------------------------------------------------------------------- */
/* Helpers                                                                    */
/* -------------------------------------------------------------------------- */

func decodeJSON(r *http.Request, dst any) error {
	if err := decodeBody(r, dst); err != nil {
		if errors.Is(err, io.EOF) {
			return httpx.BadRequest("A JSON request body is required.")
		}
		return err
	}
	return nil
}

// decodeOptionalJSON accepts an empty body.
//
// The cancel and complete routes take an optional note, and a client that has
// nothing to add sends no body at all. Requiring "{}" would make the simplest
// call the awkward one.
func decodeOptionalJSON(r *http.Request, dst any) error {
	if err := decodeBody(r, dst); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// decodeBody reports an empty body as io.EOF and everything else as a problem,
// so the two wrappers above differ only in what they do about emptiness.
func decodeBody(r *http.Request, dst any) error {
	// A bounded reader keeps a malicious client from exhausting memory. A
	// scorecard carries free-text feedback, so the cap is generous but finite.
	limited := http.MaxBytesReader(nil, r.Body, 1<<20)
	decoder := json.NewDecoder(limited)
	decoder.DisallowUnknownFields()

	err := decoder.Decode(dst)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, io.EOF):
		return io.EOF
	}

	// encoding/json spells out internal type names, which hands a caller a map
	// of the server's internals. Say what is wrong, not what we are.
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

// parseDate accepts a full RFC 3339 instant or a bare calendar date.
//
// `endOfDay` is what makes a bare date on the upper bound mean the day rather
// than its first instant. The filter applies `to` inclusively, so a plain
// YYYY-MM-DD resolved to midnight excluded every round on the day it named —
// `?from=2026-03-04&to=2026-03-04` returned nothing for a day with four
// interviews booked, which reads as an empty schedule rather than a bad query.
//
// An explicit timestamp is left exactly as given: somebody who wrote the time
// meant the time.
// idempotencyKey reads the header a client uses to make a retry safe.
//
// Bounded, because a key long enough to be a payload is not a key and an index
// entry is not storage.
func idempotencyKey(r *http.Request) string {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if len(key) > 128 {
		return key[:128]
	}
	return key
}

func parseDate(raw, field string, endOfDay bool) (*time.Time, error) {
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
	if endOfDay {
		// The last representable instant of that date, so the bound covers the
		// whole day without spilling into the next one.
		parsed = parsed.AddDate(0, 0, 1).Add(-time.Nanosecond)
	}
	return &parsed, nil
}

// mapError turns a domain error into the right HTTP problem.
//
// Anything unrecognized falls through to httpx, which reports a generic 500 and
// logs the detail — an internal message never reaches a client.
func mapError(err error) error {
	var transition *domain.TransitionError
	if errors.As(err, &transition) {
		return httpx.Conflict("illegal_transition", transition.Error())
	}

	switch {
	case errors.Is(err, domain.ErrNotOnPanel):
		return httpx.Forbidden(domain.ErrNotOnPanel.Error())

	case errors.Is(err, store.ErrBadCursor):
		return httpx.ValidationFailed(map[string][]string{
			"cursor": {"The supplied cursor is not valid. Start from the first page."}})

	// A record another tenant owns is reported as missing rather than
	// forbidden: 403 would confirm that the id exists.
	case errors.Is(err, domain.ErrInterviewNotFound),
		errors.Is(err, domain.ErrScorecardNotFound):
		return httpx.NotFound(err.Error())

	case errors.Is(err, tenancy.ErrNotCompanyScoped), errors.Is(err, tenancy.ErrCrossTenant):
		return httpx.Forbidden("This endpoint requires a company context.")

	default:
		return err
	}
}
