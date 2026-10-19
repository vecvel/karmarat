// Package api exposes the messaging service over HTTP.
//
// The service has two faces on one port and they are kept apart by principal
// type, not by convention: /v1/conversations is reachable only by a company
// principal and /v1/my-conversations only by a candidate one. A candidate token
// that reached a company route would be a cross-population read, so the guard is
// attached to every route rather than checked inside handlers.
//
// Routing note: the gateway proxies /api/v1/messages/* and strips only the /api
// prefix, so a request arrives here as /v1/messages/conversations. The service
// contract names the paths without that segment. Both spellings are registered,
// against the same handlers, so the service answers whether it is called through
// the gateway or directly in a test.
package api

import (
	"crypto/subtle"
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
	"github.com/karmarat/platform/packages/goshared/redisx"
	"github.com/karmarat/platform/packages/goshared/tenancy"
	"github.com/karmarat/platform/services/messaging/internal/candidates"
	"github.com/karmarat/platform/services/messaging/internal/domain"
	"github.com/karmarat/platform/services/messaging/internal/store"
)

// Permission keys, all of which already exist in the identity RBAC registry.
//
// `messaging.read` gates the company inbox and shows the threads the recruiter
// is a participant in. `messaging.read_all` widens that to every thread in the
// company; it is an addition to `read`, which is how the default roles grant it.
const (
	permRead     = "messaging.read"
	permReadAll  = "messaging.read_all"
	permSend     = "messaging.send"
	permMyRead   = "candidate_messaging.read"
	permMySend   = "candidate_messaging.send"
	internalHdr  = "X-Internal-Token"
	idempotenceH = "Idempotency-Key"
)

// API wires the messaging handlers.
type API struct {
	store      *store.Store
	candidates *candidates.Client
	limiter    *redisx.RateLimiter
	logger     *slog.Logger

	internalToken string
	// dailyOpenLimit caps company-initiated conversations per tenant per day.
	dailyOpenLimit int
}

// Config configures the API.
type Config struct {
	Store      *store.Store
	Candidates *candidates.Client
	Limiter    *redisx.RateLimiter
	Logger     *slog.Logger
	// InternalToken authenticates calls from other platform services.
	InternalToken string
	// DailyOpenLimit is how many new conversations a company may start in a day.
	DailyOpenLimit int
}

// New builds the API.
func New(cfg Config) *API {
	if cfg.DailyOpenLimit <= 0 {
		cfg.DailyOpenLimit = 100
	}
	return &API{
		store:          cfg.Store,
		candidates:     cfg.Candidates,
		limiter:        cfg.Limiter,
		logger:         cfg.Logger,
		internalToken:  cfg.InternalToken,
		dailyOpenLimit: cfg.DailyOpenLimit,
	}
}

// Routes returns the service's HTTP handler.
func (a *API) Routes() http.Handler {
	mux := http.NewServeMux()

	/* ------------------------------------------------------- the company's -- */

	company := func(permission string, handler http.HandlerFunc) http.Handler {
		return httpx.RequirePrincipal(tenancy.PrincipalCompany)(
			httpx.RequirePermission(permission)(handler))
	}

	// register mounts a route under both the contract path and the path the
	// gateway actually forwards. One handler, two spellings; nothing branches on
	// which one was used.
	register := func(method, path string, handler http.Handler) {
		mux.Handle(method+" /v1"+path, handler)
		mux.Handle(method+" /v1/messages"+path, handler)
	}

	register("GET", "/conversations", company(permRead, a.handleListConversations))
	register("POST", "/conversations", company(permSend, a.handleOpenConversation))
	register("GET", "/conversations/{id}", company(permRead, a.handleGetConversation))
	register("GET", "/conversations/{id}/messages", company(permRead, a.handleListMessages))
	register("POST", "/conversations/{id}/messages", company(permSend, a.handleSendMessage))
	register("POST", "/conversations/{id}/read", company(permRead, a.handleMarkRead))

	/* ----------------------------------------------------- the candidate's -- */

	candidate := func(permission string, handler http.HandlerFunc) http.Handler {
		return httpx.RequirePrincipal(tenancy.PrincipalCandidate)(
			httpx.RequirePermission(permission)(handler))
	}

	register("GET", "/my-conversations", candidate(permMyRead, a.handleListMyConversations))
	register("GET", "/my-conversations/{id}", candidate(permMyRead, a.handleGetMyConversation))
	register("GET", "/my-conversations/{id}/messages", candidate(permMyRead, a.handleListMyMessages))
	register("POST", "/my-conversations/{id}/messages", candidate(permMySend, a.handleSendMyMessage))
	// Not named in the service contract, but a thread the candidate has read has
	// to stop being unread somewhere, or the recruiter's "delivered but unread"
	// signal is permanently wrong.
	register("POST", "/my-conversations/{id}/read", candidate(permMyRead, a.handleMarkMyRead))

	// The principal is reconstructed once, at the edge of the service; no handler
	// below reads a trust header itself.
	return httpx.TrustGatewayHeaders(mux)
}

/* -------------------------------------------------------------------------- */
/* Guards and helpers                                                         */
/* -------------------------------------------------------------------------- */

// internal guards service-to-service endpoints with a shared secret.
//
// Unused today — messaging exposes nothing internally yet — but kept beside the
// other guards so the next service that needs a thread's existence confirmed
// does not invent its own scheme.
func (a *API) internal(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.internalToken == "" {
			httpx.WriteProblem(w, r, httpx.Internal("Internal API is not configured."))
			return
		}
		if subtleCompare(strings.TrimSpace(r.Header.Get(internalHdr)), a.internalToken) != 1 {
			httpx.WriteProblem(w, r, httpx.Unauthorized("Invalid internal credentials."))
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

// scopeOf builds the company scope this request may read within.
//
// The tenant comes from the verified principal and nowhere else, and it is
// checked for shape here so a malformed tenant becomes a refusal rather than a
// database cast error surfacing as a 500. The read_all widening is decided once,
// here, and then travels into the SQL as a different query.
func scopeOf(w http.ResponseWriter, r *http.Request) (store.CompanyScope, bool) {
	principal := tenancy.MustFromContext(r.Context())

	companyID, err := principal.RequireCompany()
	if err != nil || !domain.ValidUUID(companyID) {
		httpx.WriteProblem(w, r, httpx.Forbidden("This endpoint requires a company context."))
		return store.CompanyScope{}, false
	}

	return store.CompanyScope{
		CompanyID:      companyID,
		ActorAccountID: principal.Subject,
		All:            principal.Can(permReadAll),
	}, true
}

// accountOf returns the candidate this request acts for.
func accountOf(w http.ResponseWriter, r *http.Request) (string, bool) {
	principal := tenancy.MustFromContext(r.Context())
	if principal.Type != tenancy.PrincipalCandidate || principal.Subject == "" {
		httpx.WriteProblem(w, r, httpx.Forbidden("This endpoint is only available to candidate accounts."))
		return "", false
	}
	return principal.Subject, true
}

// pageOf reads the ?limit= and ?cursor= parameters every list endpoint accepts.
func pageOf(r *http.Request) (domain.Page, error) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	return domain.NewPage(limit, strings.TrimSpace(r.URL.Query().Get("cursor")))
}

// listResponse is the envelope every paginated endpoint returns.
type listResponse struct {
	Data       any    `json:"data"`
	NextCursor string `json:"nextCursor,omitempty"`
}

// nextCursor returns a cursor only when the page was full, so a client stops
// paging on a short page instead of making one more round trip for nothing.
func nextCursor(count, limit int, at time.Time, id string) string {
	if count < limit {
		return ""
	}
	return domain.Cursor{At: at, ID: id}.Encode()
}

// idempotencyKey reads the header every messaging mutation accepts.
//
// A key longer than this is not a key, it is a payload; bounding it keeps an
// index entry from being used as storage.
func idempotencyKey(r *http.Request) string {
	key := strings.TrimSpace(r.Header.Get(idempotenceH))
	if len(key) > 128 {
		return key[:128]
	}
	return key
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
		// encoding/json names the destination Go type in its errors, which hands
		// a caller a map of the server's internals. Say what is wrong without
		// saying what we are.
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
//
// Anything not listed falls through to the generic 500 that httpx.WriteProblem
// produces, which is what keeps a SQL error or a wrapped internal message from
// reaching a client.
func mapError(err error) error {
	var validationErr *domain.ValidationError
	if errors.As(err, &validationErr) {
		return httpx.ValidationFailed(validationErr.Fields)
	}

	switch {
	case errors.Is(err, domain.ErrConversationNotFound), errors.Is(err, domain.ErrMessageNotFound):
		return httpx.NotFound(err.Error())
	case errors.Is(err, domain.ErrCandidateNotFound):
		return httpx.NotFound(err.Error())
	case errors.Is(err, domain.ErrConversationExists):
		return httpx.Conflict("conversation_exists", err.Error())
	case errors.Is(err, domain.ErrConversationClosed):
		return httpx.Conflict("conversation_closed", err.Error())
	case errors.Is(err, domain.ErrCandidateUnreachable):
		return httpx.Forbidden(err.Error())
	case errors.Is(err, domain.ErrDailyLimitReached):
		return httpx.NewProblem(http.StatusTooManyRequests, "conversation_limit_reached",
			"Too Many Requests", err.Error())
	case errors.Is(err, domain.ErrDirectoryUnavailable):
		return httpx.NewProblem(http.StatusServiceUnavailable, "directory_unavailable",
			"Service Unavailable", err.Error())
	case errors.Is(err, domain.ErrInvalidCursor):
		return httpx.BadRequest(domain.ErrInvalidCursor.Error())
	case errors.Is(err, tenancy.ErrNotCompanyScoped), errors.Is(err, tenancy.ErrCrossTenant):
		return httpx.Forbidden("This endpoint requires a company context.")
	default:
		return err
	}
}
