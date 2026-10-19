// Package api exposes the notifications service over HTTP.
//
// Every other service in this platform has one kind of caller. This one has
// three: the gateway proxies /api/v1/notifications from the company portal, the
// candidate job portal and the platform console alike, because an inbox is an
// inbox whoever owns it. So no route here is bound to a principal type, and no
// handler assumes one — the recipient is derived from the verified principal
// and every query is filtered by it.
//
// There is no permission check on these routes, and that is deliberate rather
// than an omission. The RBAC registry has no notifications feature because
// reading your own inbox is not a capability a role grants; it is what having
// an account means. Permissions do appear in this service, but on the other
// side: they decide who at a company is worth telling about an event, which is
// a fan-out question, not an access one.
package api

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/karmarat/platform/packages/goshared/httpx"
	"github.com/karmarat/platform/packages/goshared/tenancy"
	"github.com/karmarat/platform/services/notifications/internal/domain"
	"github.com/karmarat/platform/services/notifications/internal/realtime"
	"github.com/karmarat/platform/services/notifications/internal/store"
)

// maxBodyBytes bounds a preferences document. The largest legitimate one is a
// few hundred bytes; the limit is there so a handler never reads an unbounded
// body into memory.
const maxBodyBytes = 64 << 10

// API wires the notifications handlers.
type API struct {
	store  *store.Store
	hub    *realtime.Hub
	logger *slog.Logger
	// heartbeat is how often an idle SSE stream writes a comment.
	heartbeat time.Duration
	// streamLifetime bounds one SSE connection; see handleStream.
	streamLifetime time.Duration
}

// Config configures the API.
type Config struct {
	Store          *store.Store
	Hub            *realtime.Hub
	Logger         *slog.Logger
	Heartbeat      time.Duration
	StreamLifetime time.Duration
}

// New builds the API.
func New(cfg Config) *API {
	if cfg.Heartbeat <= 0 {
		cfg.Heartbeat = 20 * time.Second
	}
	if cfg.StreamLifetime <= 0 {
		cfg.StreamLifetime = 4 * time.Minute
	}
	return &API{
		store:          cfg.Store,
		hub:            cfg.Hub,
		logger:         cfg.Logger,
		heartbeat:      cfg.Heartbeat,
		streamLifetime: cfg.StreamLifetime,
	}
}

// Routes returns the service's HTTP handler.
//
// The gateway strips only the /api prefix, so /api/v1/notifications/... arrives
// here as /v1/notifications/..., which is what the contract names. Nothing has
// to be registered twice.
func (a *API) Routes() http.Handler {
	mux := http.NewServeMux()

	// One wrapper for every route: reconstruct the principal from the gateway's
	// headers, then insist there is one. Which principal it is decides what the
	// handler can see, and that is resolved per request rather than per route.
	authenticated := func(handler http.HandlerFunc) http.Handler {
		return httpx.TrustGatewayHeaders(httpx.RequireAuth(a.seen(handler)))
	}

	mux.Handle("GET /v1/notifications", authenticated(a.handleList))
	mux.Handle("GET /v1/notifications/stream", authenticated(a.handleStream))
	mux.Handle("GET /v1/notifications/preferences", authenticated(a.handleGetPreferences))
	mux.Handle("PUT /v1/notifications/preferences", authenticated(a.handlePutPreferences))
	mux.Handle("POST /v1/notifications/read-all", authenticated(a.handleReadAll))
	mux.Handle("POST /v1/notifications/{id}/read", authenticated(a.handleRead))

	return mux
}

// seen records the principal in the recipient directory.
//
// This is how a company-side event finds anyone to notify: identity publishes
// no roster of a company's members and exposes no internal endpoint to list
// them, so the service learns who exists from the gateway's verified headers on
// ordinary traffic. A failure is logged and swallowed — a directory refresh is
// not worth failing somebody's inbox over.
func (a *API) seen(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal := tenancy.MustFromContext(r.Context())
		if principal.IsAuthenticated() && principal.Subject != "" {
			if err := a.store.TouchRecipient(r.Context(), principal); err != nil {
				a.logger.Warn("could not record recipient",
					slog.String("principal_type", string(principal.Type)),
					slog.Any("error", err))
			}
		}
		next.ServeHTTP(w, r)
	})
}

/* -------------------------------------------------------------------------- */
/* The inbox                                                                  */
/* -------------------------------------------------------------------------- */

func (a *API) handleList(w http.ResponseWriter, r *http.Request) {
	recipient, err := recipientOf(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	page, err := parsePage(r)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	listing, err := a.store.List(r.Context(), recipient, page)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	views := make([]NotificationView, 0, len(listing.Notifications))
	for _, notification := range listing.Notifications {
		views = append(views, ToNotificationView(notification))
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"notifications": views,
		"nextCursor":    listing.NextCursor,
		"unreadCount":   listing.UnreadCount,
	})
}

func (a *API) handleRead(w http.ResponseWriter, r *http.Request) {
	recipient, err := recipientOf(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	if err := a.store.MarkRead(r.Context(), recipient, r.PathValue("id")); err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	count, err := a.store.UnreadCount(r.Context(), recipient)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"unreadCount": count})
}

func (a *API) handleReadAll(w http.ResponseWriter, r *http.Request) {
	recipient, err := recipientOf(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	cleared, err := a.store.MarkAllRead(r.Context(), recipient)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"markedRead":  cleared,
		"unreadCount": 0,
	})
}

/* -------------------------------------------------------------------------- */
/* Preferences                                                                */
/* -------------------------------------------------------------------------- */

func (a *API) handleGetPreferences(w http.ResponseWriter, r *http.Request) {
	recipient, err := recipientOf(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	prefs, err := a.store.LoadPreferences(r.Context(), recipient)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	httpx.WriteJSON(w, http.StatusOK,
		toPreferencesView(prefs, domain.RelevantTypes(recipient.PrincipalType)))
}

// preferencesRequest is the PUT body.
//
// It is a partial document: the client sends the switches it just changed. A
// replace would silently reset every type an older tab did not know about,
// which is precisely what happens during a rollout.
type preferencesRequest struct {
	Channels map[domain.Type]domain.ChannelSet `json:"channels"`
}

func (a *API) handlePutPreferences(w http.ResponseWriter, r *http.Request) {
	recipient, err := recipientOf(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	var req preferencesRequest
	if err := decodeBody(r, &req); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}
	if len(req.Channels) == 0 {
		httpx.WriteProblem(w, r, httpx.ValidationFailed(domain.FieldErrors{
			"channels": {"Send at least one notification type to change."},
		}))
		return
	}

	// A principal may only set preferences for types it can receive. Accepting
	// the rest would store settings that never take effect and then show them
	// back, which reads as a bug the first time somebody turns one off and
	// keeps getting nothing.
	allowed := map[domain.Type]bool{}
	for _, t := range domain.RelevantTypes(recipient.PrincipalType) {
		allowed[t] = true
	}
	problems := domain.FieldErrors{}
	for t := range req.Channels {
		if t.Valid() && !allowed[t] {
			problems["channels."+string(t)] = []string{
				"Your account does not receive this kind of notification."}
		}
	}
	if len(problems) > 0 {
		httpx.WriteProblem(w, r, httpx.ValidationFailed(problems))
		return
	}

	current, err := a.store.LoadPreferences(r.Context(), recipient)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	merged, invalid := current.Merge(req.Channels)
	if len(invalid) > 0 {
		httpx.WriteProblem(w, r, httpx.ValidationFailed(invalid))
		return
	}

	if err := a.store.SavePreferences(r.Context(), recipient, merged); err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	httpx.WriteJSON(w, http.StatusOK,
		toPreferencesView(merged, domain.RelevantTypes(recipient.PrincipalType)))
}

/* -------------------------------------------------------------------------- */
/* Request plumbing                                                           */
/* -------------------------------------------------------------------------- */

// recipientOf builds the inbox owner from the verified principal.
//
// This is the one place the recipient is decided, and it reads nothing but the
// principal. No handler takes an account id from a path, a body or a query,
// which is what makes "your own notifications only" a property of the service
// rather than a rule each handler has to remember.
func recipientOf(r *http.Request) (domain.Recipient, error) {
	principal := tenancy.MustFromContext(r.Context())
	if !principal.IsAuthenticated() {
		return domain.Recipient{}, httpx.Unauthorized("You must be signed in to perform this action.")
	}

	switch principal.Type {
	case tenancy.PrincipalCompany:
		// A company principal without a tenant cannot be given an inbox: there
		// would be nothing to scope it to, and an unscoped query is the bug
		// this whole service is arranged to prevent.
		companyID, err := principal.RequireCompany()
		if err != nil {
			return domain.Recipient{}, httpx.Forbidden("This endpoint requires a company context.")
		}
		return domain.Recipient{
			PrincipalType: tenancy.PrincipalCompany,
			AccountID:     principal.Subject,
			CompanyID:     companyID,
		}, nil

	case tenancy.PrincipalCandidate, tenancy.PrincipalPlatform:
		// Neither belongs to a tenant. Their notifications may name a company
		// as context, but their inbox is their account and nothing else.
		return domain.Recipient{
			PrincipalType: principal.Type,
			AccountID:     principal.Subject,
		}, nil

	default:
		return domain.Recipient{}, httpx.Forbidden(domain.ErrNotAddressable.Error())
	}
}

func parsePage(r *http.Request) (domain.Page, error) {
	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			return domain.Page{}, domain.Invalid("limit", "The limit must be a whole number.")
		}
		limit = parsed
	}

	page, err := domain.NewPage(limit, r.URL.Query().Get("cursor"))
	if err != nil {
		return domain.Page{}, err
	}

	page.UnreadOnly = r.URL.Query().Get("unread") == "true"
	return page, nil
}

func decodeBody(r *http.Request, into any) error {
	body := http.MaxBytesReader(nil, r.Body, maxBodyBytes)
	decoder := json.NewDecoder(body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(into); err != nil {
		if errors.Is(err, io.EOF) {
			return httpx.BadRequest("A request body is required.")
		}
		// The decoder's message names Go types and byte offsets; a client is
		// told the body was unreadable and nothing about our structs.
		return httpx.BadRequest("The request body could not be read as JSON.")
	}
	return nil
}

// mapError turns a domain failure into the problem document a client sees.
//
// Anything unrecognised falls through to httpx.WriteProblem, which logs it and
// answers with a generic 500 — an internal message, a SQL fragment or a type
// name never reaches the response.
func mapError(err error) error {
	var validationErr *domain.ValidationError
	if errors.As(err, &validationErr) {
		field := validationErr.Field
		if field == "" {
			field = "request"
		}
		return httpx.ValidationFailed(domain.FieldErrors{field: {validationErr.Message}})
	}

	switch {
	case errors.Is(err, domain.ErrNotificationNotFound):
		// 404 rather than 403: another recipient must not be able to learn that
		// a notification id exists from the shape of the refusal.
		return httpx.NotFound(domain.ErrNotificationNotFound.Error())
	case errors.Is(err, domain.ErrInvalidCursor):
		return httpx.ValidationFailed(domain.FieldErrors{"cursor": {domain.ErrInvalidCursor.Error()}})
	case errors.Is(err, domain.ErrNotAddressable):
		return httpx.Forbidden(domain.ErrNotAddressable.Error())
	case errors.Is(err, tenancy.ErrNotCompanyScoped), errors.Is(err, tenancy.ErrCrossTenant):
		return httpx.Forbidden("This endpoint requires a company context.")
	default:
		return err
	}
}
