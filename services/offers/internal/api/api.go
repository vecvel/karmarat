// Package api exposes the offers service over HTTP.
package api

import (
	"bytes"
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
	"github.com/karmarat/platform/services/offers/internal/domain"
	"github.com/karmarat/platform/services/offers/internal/store"
)

// Permission keys, all of which already exist in the identity RBAC registry.
//
// The split is the point of the feature: `offers.approve` and `offers.send` are
// separate from `offers.create`, so approval authority can be delegated without
// also handing over the ability to put a letter in front of a candidate.
//
// `offers.view_compensation` is different in kind from the rest. It does not
// gate an endpoint — it gates the money inside the response, and is resolved
// once per request into the boolean that domain.NewOfferView redacts on.
const (
	permRead             = "offers.read"
	permCreate           = "offers.create"
	permUpdate           = "offers.update"
	permDelete           = "offers.delete"
	permApprove          = "offers.approve"
	permSend             = "offers.send"
	permViewCompensation = "offers.view_compensation"

	idempotenceH = "Idempotency-Key"
)

// API wires the offers handlers.
type API struct {
	store  *store.Store
	logger *slog.Logger
	// defaultCurrency is used when a draft names no currency. The portal always
	// sends one; a client that does not gets the platform's configured default
	// rather than an offer with a blank currency, which the char(3) column
	// cannot hold anyway.
	defaultCurrency string
}

// Config configures the API.
type Config struct {
	Store           *store.Store
	Logger          *slog.Logger
	DefaultCurrency string
}

// New builds the API.
func New(cfg Config) *API {
	currency, err := domain.NormalizeCurrency(cfg.DefaultCurrency)
	if err != nil {
		currency = "USD"
	}
	return &API{store: cfg.Store, logger: cfg.Logger, defaultCurrency: currency}
}

// Routes returns the service's HTTP handler.
//
// Guard order is the platform's: reconstruct the principal from the gateway's
// headers, check it is the right kind, check the permission, and only then run a
// handler that filters by tenant anyway. The permission answers "may this role
// do this"; the tenant filter answers "to whose data".
func (a *API) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.Handle("GET /v1/offers", a.company(permRead, a.handleList))
	mux.Handle("POST /v1/offers", a.company(permCreate, a.handleCreate))
	mux.Handle("GET /v1/offers/{id}", a.company(permRead, a.handleGet))
	mux.Handle("PATCH /v1/offers/{id}", a.company(permUpdate, a.handlePatch))
	mux.Handle("DELETE /v1/offers/{id}", a.company(permDelete, a.handleDelete))

	// Submitting for approval is the author's own act, so it rides on
	// `offers.create` rather than on a permission of its own: somebody who may
	// draft a package may ask for it to be signed off. What they may not do is
	// sign it off, which is `offers.approve` below.
	mux.Handle("POST /v1/offers/{id}/submit", a.company(permCreate, a.handleSubmit))
	mux.Handle("POST /v1/offers/{id}/approve", a.company(permApprove, a.handleApprove))
	mux.Handle("POST /v1/offers/{id}/send", a.company(permSend, a.handleSend))
	// Recording the candidate's answer rides on `offers.send`, not on
	// `offers.update`.
	//
	// `offers.update` is the generic, non-sensitive CRUD key, and in this service
	// it otherwise only edits a draft — the store refuses anything else. An
	// administrator who grants it so somebody can fix a typo would not expect
	// that person to be able to mark a live offer accepted: that is a terminal
	// state with no way back, and it publishes `offer.accepted` to every
	// downstream consumer, so a candidate who never answered is recorded as
	// having said yes.
	//
	// `offers.send` is the capability that owns the candidate-facing leg of the
	// exchange and the registry marks it sensitive. Whoever may dispatch the
	// letter may record the reply to it. A dedicated `offers.record_response`
	// key would be narrower still, but it would mean a new permission in the
	// platform registry and a reseed of every tenant's roles to hand it out —
	// worth doing when somebody actually needs to separate the two.
	mux.Handle("POST /v1/offers/{id}/respond", a.company(permSend, a.handleRespond))

	return httpx.TrustGatewayHeaders(mux)
}

func (a *API) company(permission string, handler http.HandlerFunc) http.Handler {
	return httpx.RequirePrincipal(tenancy.PrincipalCompany)(
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

// canViewCompensation resolves the money permission once per request.
//
// Every response in this service is written through writeOffer or writeOffers,
// which take this boolean and hand it to domain.NewOfferView. There is no other
// path from an offer to a response body, which is what makes the redaction a
// property of the service rather than a habit of whoever wrote the handler.
func canViewCompensation(principal tenancy.Principal) bool {
	return principal.Can(permViewCompensation)
}

func writeOffer(w http.ResponseWriter, status int, offer domain.Offer, principal tenancy.Principal) {
	httpx.WriteJSON(w, status, domain.NewOfferView(offer, canViewCompensation(principal)))
}

func writeOffers(w http.ResponseWriter, page store.Page, principal tenancy.Principal) {
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"offers":     domain.NewOfferViews(page.Offers, canViewCompensation(principal)),
		"nextCursor": page.NextCursor,
	})
}

// requireCompensationAccess refuses a write that sets money from somebody who
// may not read it.
//
// Writing a figure you are not allowed to see is a way to launder compensation
// data: draft an offer at a number, have a colleague read it back, and the
// permission has been bypassed without ever being checked. The portal already
// pairs the two on its create action; this is the server-side half of that.
func requireCompensationAccess(principal tenancy.Principal) error {
	if !canViewCompensation(principal) {
		return httpx.PermissionDenied([]string{permViewCompensation})
	}
	return nil
}

/* -------------------------------------------------------------------------- */
/* Helpers                                                                    */
/* -------------------------------------------------------------------------- */

func decodeJSON(r *http.Request, dst any) error {
	// A bounded reader keeps a malicious client from exhausting memory. An offer
	// carries a rendered letter, so the cap is generous but finite.
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

// decodeOptionalJSON accepts an absent body.
//
// submit, approve and send carry nothing a caller must supply — the actor comes
// from the principal and the id from the path — so requiring `{}` would be a
// 400 that teaches a client nothing.
func decodeOptionalJSON(r *http.Request, dst any) error {
	body, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, 1<<20))
	if err != nil {
		var maxBytes *http.MaxBytesError
		if errors.As(err, &maxBytes) {
			return httpx.BadRequest("The request body is too large.")
		}
		return httpx.BadRequest("The request body could not be read.")
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return nil
	}

	// Replayed through the strict decoder, so an optional body still gets the
	// same unknown-field and syntax handling as a required one.
	r.Body = io.NopCloser(bytes.NewReader(body))
	return decodeJSON(r, dst)
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

func parseTimestamp(raw, field string) (*time.Time, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}
	if parsed, err := time.Parse(time.RFC3339, trimmed); err == nil {
		return &parsed, nil
	}
	parsed, err := time.Parse(time.DateOnly, trimmed)
	if err != nil {
		return nil, domain.Invalid(field, "Use a date in YYYY-MM-DD form or a full RFC 3339 timestamp.")
	}
	return &parsed, nil
}

// parseJoiningDate reads a calendar date.
//
// It is truncated to midnight UTC rather than kept as an instant: a joining date
// is a day in the office's own calendar, and storing the moment the recruiter
// happened to pick it would put a 1 January start on 31 December for anyone west
// of the tenant.
func parseJoiningDate(raw string) (time.Time, error) {
	parsed, err := parseTimestamp(raw, "joiningDate")
	if err != nil || parsed == nil {
		if err == nil {
			err = domain.Invalid("joiningDate", "A joining date is required.")
		}
		return time.Time{}, err
	}
	return time.Date(parsed.Year(), parsed.Month(), parsed.Day(), 0, 0, 0, 0, time.UTC), nil
}

func idempotencyKey(r *http.Request) string {
	key := strings.TrimSpace(r.Header.Get(idempotenceH))
	// A key long enough to be a payload is not a key; bounding it keeps an index
	// entry from being used as storage.
	if len(key) > 128 {
		return key[:128]
	}
	return key
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

	// An illegal move is a conflict with the record's current state, not a
	// malformed request: the body was fine, the offer had simply moved on. 409
	// is also what lets a client tell "retry with different input" apart from
	// "reload, somebody else acted".
	var transitionErr *domain.TransitionError
	if errors.As(err, &transitionErr) {
		return httpx.Conflict("invalid_transition", transitionErr.Error())
	}

	switch {
	case errors.Is(err, domain.ErrNotEditable):
		return httpx.Conflict("not_editable", domain.ErrNotEditable.Error())
	case errors.Is(err, domain.ErrNotDeletable):
		return httpx.Conflict("not_deletable", domain.ErrNotDeletable.Error())
	case errors.Is(err, domain.ErrExpired):
		return httpx.Conflict("offer_expired", domain.ErrExpired.Error())
	case errors.Is(err, domain.ErrSelfApproval):
		return httpx.Conflict("self_approval", domain.ErrSelfApproval.Error())
	case errors.Is(err, domain.ErrIdempotencyConflict):
		return httpx.Conflict("idempotency_conflict", domain.ErrIdempotencyConflict.Error())

	// An offer another tenant owns is reported as missing rather than
	// forbidden: 403 would confirm that the id exists.
	case errors.Is(err, domain.ErrOfferNotFound):
		return httpx.NotFound(domain.ErrOfferNotFound.Error())

	case errors.Is(err, tenancy.ErrNotCompanyScoped), errors.Is(err, tenancy.ErrCrossTenant):
		return httpx.Forbidden("This endpoint requires a company context.")

	default:
		return err
	}
}
