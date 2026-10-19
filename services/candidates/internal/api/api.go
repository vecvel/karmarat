// Package api exposes the candidates service over HTTP.
//
// The service has two faces on one port and they are kept apart by principal
// type, not by convention: /v1/me is reachable only by a candidate principal and
// /v1/candidates and /v1/talent only by a company one. A candidate token that
// reached a company route would be a cross-population read, so the guard is
// attached to every route rather than checked inside handlers.
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
	"github.com/karmarat/platform/services/candidates/internal/domain"
	"github.com/karmarat/platform/services/candidates/internal/events"
	"github.com/karmarat/platform/services/candidates/internal/storage"
	"github.com/karmarat/platform/services/candidates/internal/store"
)

// API wires the candidates handlers.
type API struct {
	store         *store.Store
	publisher     *events.Publisher
	presigner     *storage.Presigner
	limiter       *redisx.RateLimiter
	logger        *slog.Logger
	internalToken string
	uploadTTL     time.Duration
	downloadTTL   time.Duration
}

// Config configures the API.
type Config struct {
	Store     *store.Store
	Publisher *events.Publisher
	Presigner *storage.Presigner
	Limiter   *redisx.RateLimiter
	Logger    *slog.Logger
	// InternalToken authenticates calls from other platform services.
	InternalToken string
	// UploadTTL and DownloadTTL keep signed URLs short: a URL is a bearer
	// capability for one object, and a long-lived one outlives the permission
	// check that produced it.
	UploadTTL   time.Duration
	DownloadTTL time.Duration
}

// New builds the API.
func New(cfg Config) *API {
	if cfg.UploadTTL == 0 {
		cfg.UploadTTL = 10 * time.Minute
	}
	if cfg.DownloadTTL == 0 {
		cfg.DownloadTTL = 5 * time.Minute
	}

	return &API{
		store:         cfg.Store,
		publisher:     cfg.Publisher,
		presigner:     cfg.Presigner,
		limiter:       cfg.Limiter,
		logger:        cfg.Logger,
		internalToken: cfg.InternalToken,
		uploadTTL:     cfg.UploadTTL,
		downloadTTL:   cfg.DownloadTTL,
	}
}

// Routes returns the service's HTTP handler.
func (a *API) Routes() http.Handler {
	mux := http.NewServeMux()

	/* ------------------------------------------------- the candidate's own -- */

	candidate := func(permission string, handler http.HandlerFunc) http.Handler {
		return httpx.RequirePrincipal(tenancy.PrincipalCandidate)(
			httpx.RequirePermission(permission)(handler))
	}

	mux.Handle("GET /v1/me", candidate("candidate_profile.read", a.handleGetProfile))
	mux.Handle("PATCH /v1/me", candidate("candidate_profile.update", a.handleUpdateProfile))
	mux.Handle("DELETE /v1/me", candidate("candidate_profile.delete", a.handleDeleteProfile))
	mux.Handle("PUT /v1/me/visibility", candidate("candidate_profile.manage_visibility", a.handleUpdateVisibility))

	mux.Handle("GET /v1/me/experience", candidate("candidate_profile.read", a.handleListExperience))
	mux.Handle("POST /v1/me/experience", candidate("candidate_profile.update", a.handleCreateExperience))
	mux.Handle("PUT /v1/me/experience/{id}", candidate("candidate_profile.update", a.handleUpdateExperience))
	mux.Handle("DELETE /v1/me/experience/{id}", candidate("candidate_profile.update", a.handleDeleteExperience))

	mux.Handle("GET /v1/me/education", candidate("candidate_profile.read", a.handleListEducation))
	mux.Handle("POST /v1/me/education", candidate("candidate_profile.update", a.handleCreateEducation))
	mux.Handle("PUT /v1/me/education/{id}", candidate("candidate_profile.update", a.handleUpdateEducation))
	mux.Handle("DELETE /v1/me/education/{id}", candidate("candidate_profile.update", a.handleDeleteEducation))

	mux.Handle("GET /v1/me/certifications", candidate("candidate_profile.read", a.handleListCertifications))
	mux.Handle("POST /v1/me/certifications", candidate("candidate_profile.update", a.handleCreateCertification))
	mux.Handle("PUT /v1/me/certifications/{id}", candidate("candidate_profile.update", a.handleUpdateCertification))
	mux.Handle("DELETE /v1/me/certifications/{id}", candidate("candidate_profile.update", a.handleDeleteCertification))

	mux.Handle("GET /v1/me/resumes", candidate("candidate_profile.read", a.handleListResumes))
	mux.Handle("POST /v1/me/resumes/upload-url", candidate("candidate_profile.update", a.handleResumeUploadURL))
	mux.Handle("DELETE /v1/me/resumes/{id}", candidate("candidate_profile.update", a.handleDeleteResume))
	mux.Handle("POST /v1/me/resumes/{id}/primary", candidate("candidate_profile.update", a.handleSetPrimaryResume))

	/* ------------------------------------------------ the company's own pool -- */

	company := func(permission string, handler http.HandlerFunc) http.Handler {
		return httpx.RequirePrincipal(tenancy.PrincipalCompany)(
			httpx.RequirePermission(permission)(handler))
	}

	// The literal path wins over the {id} pattern, so an export is never read as
	// a candidate whose id happens to be "export".
	mux.Handle("GET /v1/candidates/export", company("candidates.export", a.handleExportPool))
	mux.Handle("GET /v1/candidates", company("candidates.read", a.handleListPool))
	mux.Handle("POST /v1/candidates", company("candidates.create", a.handleCreatePoolCandidate))
	mux.Handle("GET /v1/candidates/{id}", company("candidates.read", a.handleGetPoolCandidate))
	mux.Handle("PUT /v1/candidates/{id}", company("candidates.update", a.handleUpdatePoolCandidate))
	mux.Handle("DELETE /v1/candidates/{id}", company("candidates.delete", a.handleDeletePoolCandidate))
	mux.Handle("POST /v1/candidates/{id}/resume/upload-url", company("candidates.update", a.handlePoolResumeUploadURL))
	mux.Handle("GET /v1/candidates/{id}/resume/download-url",
		company("candidates.download_resume", a.handlePoolResumeDownloadURL))

	/* ------------------------------------------------------- talent discovery -- */

	mux.Handle("GET /v1/talent/search", company("talent_search.search", a.handleTalentSearch))
	mux.Handle("GET /v1/talent/approaches", company("talent_search.search", a.handleListApproaches))
	mux.Handle("POST /v1/talent/{candidateId}/approach", company("talent_search.approach", a.handleApproach))

	/* -------------------------------------------------------------- internal -- */

	mux.Handle("GET /internal/candidates/{accountId}", a.internal(http.HandlerFunc(a.handleInternalProfile)))

	// The principal is reconstructed once, at the edge of the service; no handler
	// below reads a trust header itself.
	return httpx.TrustGatewayHeaders(mux)
}

/* -------------------------------------------------------------------------- */
/* Guards and helpers                                                         */
/* -------------------------------------------------------------------------- */

// internal guards service-to-service endpoints with a shared secret.
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

// subtleCompare is a constant-time string comparison for shared secrets.
func subtleCompare(a, b string) int {
	if len(a) != len(b) {
		// Still run a comparison so length alone does not shortcut the timing.
		subtle.ConstantTimeCompare([]byte(a), []byte(a))
		return 0
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b))
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

// companyOf returns the tenant this request acts in.
//
// The id comes from the verified principal and nowhere else, and it is checked
// for shape here so that a malformed tenant becomes a refusal rather than a
// database cast error surfacing as a 500.
func companyOf(w http.ResponseWriter, r *http.Request) (string, bool) {
	principal := tenancy.MustFromContext(r.Context())

	companyID, err := principal.RequireCompany()
	if err != nil || !domain.ValidUUID(companyID) {
		httpx.WriteProblem(w, r, httpx.Forbidden("This endpoint requires a company context."))
		return "", false
	}
	return companyID, true
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

func decodeJSON(r *http.Request, dst any) error {
	// A bounded reader keeps a malicious client from exhausting memory.
	limited := http.MaxBytesReader(nil, r.Body, 1<<20)
	decoder := json.NewDecoder(limited)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return httpx.BadRequest("A JSON request body is required.")
		}
		// encoding/json names the destination Go type in its errors, which hands a
		// caller a map of the server's internals. Say what is wrong without saying
		// what we are.
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
	var validationErr *domain.ValidationError
	if errors.As(err, &validationErr) {
		return httpx.ValidationFailed(validationErr.Fields)
	}

	switch {
	case errors.Is(err, domain.ErrProfileNotFound), errors.Is(err, domain.ErrEntryNotFound),
		errors.Is(err, domain.ErrResumeNotFound), errors.Is(err, domain.ErrCandidateNotFound),
		errors.Is(err, domain.ErrNoResume):
		return httpx.NotFound(err.Error())
	case errors.Is(err, domain.ErrProfileDeleted):
		return httpx.NewProblem(http.StatusGone, "profile_deleted", "Gone", err.Error())
	case errors.Is(err, domain.ErrResumeLimit):
		return httpx.Conflict("resume_limit_reached", err.Error())
	case errors.Is(err, domain.ErrApproachTooSoon):
		return httpx.NewProblem(http.StatusTooManyRequests, "approach_rate_limited",
			"Too Many Requests", err.Error())
	case errors.Is(err, domain.ErrInvalidCursor):
		return httpx.BadRequest(domain.ErrInvalidCursor.Error())
	default:
		return err
	}
}

// parseDate accepts a calendar date, which is what a CV carries: nobody knows
// what hour they started a job.
func parseDate(field, value string, required bool) (*time.Time, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		if required {
			return nil, domain.Invalid(field, "A date in YYYY-MM-DD form is required.")
		}
		return nil, nil
	}

	parsed, err := time.Parse("2006-01-02", trimmed)
	if err != nil {
		return nil, domain.Invalid(field, "Dates must be in YYYY-MM-DD form.")
	}
	return &parsed, nil
}
