package api

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/karmarat/platform/packages/goshared/httpx"
	"github.com/karmarat/platform/packages/goshared/tenancy"
	"github.com/karmarat/platform/services/messaging/internal/domain"
)

// The guard tests below drive a real router with no store behind it. That is
// deliberate: every case here must be refused before a handler runs, so a case
// that reached the database would panic and fail loudly rather than pass
// quietly.
func testRoutes() http.Handler {
	return New(Config{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}).Routes()
}

type caller struct {
	principal   tenancy.PrincipalType
	subject     string
	companyID   string
	permissions string
}

func (c caller) request(method, path string) *http.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(`{}`))
	if c.principal == "" {
		return r
	}
	r.Header.Set(httpx.HeaderPrincipalType, string(c.principal))
	r.Header.Set(httpx.HeaderPrincipalID, c.subject)
	r.Header.Set(httpx.HeaderCompanyID, c.companyID)
	r.Header.Set(httpx.HeaderPermissions, c.permissions)
	return r
}

const testCompany = "11111111-1111-1111-1111-111111111111"

func TestRoutesRefuseTheWrongCaller(t *testing.T) {
	recruiter := caller{
		principal:   tenancy.PrincipalCompany,
		subject:     "usr_01HZX3T9QKD6M0V8B2N4C7E5FG",
		companyID:   testCompany,
		permissions: "messaging.read,messaging.send",
	}
	candidate := caller{
		principal:   tenancy.PrincipalCandidate,
		subject:     "acct_01HZX3T9QKD6M0V8B2N4C7E5FG",
		permissions: "candidate_messaging.read,candidate_messaging.send",
	}

	tests := []struct {
		name   string
		method string
		path   string
		caller caller
		want   int
	}{
		{
			name: "anonymous callers are not told what exists",
			// No principal header at all: the gateway did not authenticate this.
			method: http.MethodGet, path: "/v1/conversations",
			caller: caller{}, want: http.StatusUnauthorized,
		},
		{
			// The portal boundary is enforced by principal type, so a candidate
			// token cannot reach the company inbox even holding a company
			// permission string.
			name:   "a candidate cannot reach the company inbox",
			method: http.MethodGet, path: "/v1/conversations",
			caller: caller{
				principal:   tenancy.PrincipalCandidate,
				subject:     "acct_01HZX3T9QKD6M0V8B2N4C7E5FG",
				permissions: "messaging.read,messaging.read_all",
			},
			want: http.StatusForbidden,
		},
		{
			name:   "a recruiter cannot read a candidate's own inbox",
			method: http.MethodGet, path: "/v1/my-conversations",
			caller: caller{
				principal:   tenancy.PrincipalCompany,
				subject:     "usr_01HZX3T9QKD6M0V8B2N4C7E5FG",
				companyID:   testCompany,
				permissions: "candidate_messaging.read",
			},
			want: http.StatusForbidden,
		},
		{
			name:   "platform staff hold no tenant permissions",
			method: http.MethodGet, path: "/v1/conversations",
			caller: caller{
				principal:   tenancy.PrincipalPlatform,
				subject:     "usr_platform",
				permissions: "platform_companies.read",
			},
			want: http.StatusForbidden,
		},
		{
			name:   "reading needs messaging.read",
			method: http.MethodGet, path: "/v1/conversations",
			caller: caller{
				principal: tenancy.PrincipalCompany, subject: "usr_1", companyID: testCompany,
				permissions: "jobs.read",
			},
			want: http.StatusForbidden,
		},
		{
			// Reading a thread and writing into one are separate grants: a
			// read-only recruiter must not be able to send.
			name:   "sending needs messaging.send, not messaging.read",
			method: http.MethodPost, path: "/v1/conversations/conv_1/messages",
			caller: caller{
				principal: tenancy.PrincipalCompany, subject: "usr_1", companyID: testCompany,
				permissions: "messaging.read",
			},
			want: http.StatusForbidden,
		},
		{
			name:   "opening a conversation needs messaging.send",
			method: http.MethodPost, path: "/v1/conversations",
			caller: caller{
				principal: tenancy.PrincipalCompany, subject: "usr_1", companyID: testCompany,
				permissions: "messaging.read,messaging.read_all",
			},
			want: http.StatusForbidden,
		},
		{
			name:   "a candidate sending needs candidate_messaging.send",
			method: http.MethodPost, path: "/v1/my-conversations/conv_1/messages",
			caller: caller{
				principal: tenancy.PrincipalCandidate, subject: "acct_1",
				permissions: "candidate_messaging.read",
			},
			want: http.StatusForbidden,
		},
		{
			// A company principal with no company on it is not a tenant, and a
			// tenant-scoped query has nothing to filter by.
			name:   "a company principal without a company is refused",
			method: http.MethodGet, path: "/v1/conversations",
			caller: caller{
				principal: tenancy.PrincipalCompany, subject: "usr_1",
				permissions: "messaging.read",
			},
			want: http.StatusForbidden,
		},
		{
			name:   "an unknown route is a 404 whoever asks",
			method: http.MethodGet, path: "/v1/conversations/conv_1/attachments",
			caller: recruiter, want: http.StatusNotFound,
		},
		{
			name:   "a candidate route is a 404 for an unknown path",
			method: http.MethodGet, path: "/v1/my-conversations/conv_1/attachments",
			caller: candidate, want: http.StatusNotFound,
		},
	}

	routes := testRoutes()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			routes.ServeHTTP(recorder, tc.caller.request(tc.method, tc.path))

			if recorder.Code != tc.want {
				t.Fatalf("status = %d, want %d (body %s)", recorder.Code, tc.want, recorder.Body.String())
			}
		})
	}
}

// The gateway strips only the /api prefix, so a request it forwards arrives
// carrying /v1/messages. Both spellings have to reach the same guard, or the
// service answers only when it is called one particular way.
func TestGatewaySpellingIsRegisteredToo(t *testing.T) {
	routes := testRoutes()

	paths := []string{
		"/v1/messages/conversations",
		"/v1/messages/conversations/conv_1/messages",
		"/v1/messages/my-conversations",
		"/v1/messages/my-conversations/conv_1/messages",
	}

	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			routes.ServeHTTP(recorder, caller{}.request(http.MethodGet, path))

			// Unauthenticated rather than not-found: the route exists and the
			// guard, not the router, turned the request away.
			if recorder.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
			}
		})
	}
}

func TestPageOf(t *testing.T) {
	tests := []struct {
		name      string
		query     string
		wantLimit int
		wantErr   bool
	}{
		{name: "no query uses the default", query: "", wantLimit: domain.DefaultPageLimit},
		{name: "a limit is honoured", query: "?limit=40", wantLimit: 40},
		{name: "an excessive limit is capped", query: "?limit=999", wantLimit: domain.MaxPageLimit},
		{name: "a non-numeric limit falls back", query: "?limit=lots", wantLimit: domain.DefaultPageLimit},
		{name: "a malformed cursor is an error", query: "?cursor=%21%21%21", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			page, err := pageOf(httptest.NewRequest(http.MethodGet, "/v1/conversations"+tc.query, nil))
			if tc.wantErr {
				if err == nil {
					t.Fatal("pageOf() error = nil, want an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("pageOf() error = %v, want nil", err)
			}
			if page.Limit != tc.wantLimit {
				t.Errorf("limit = %d, want %d", page.Limit, tc.wantLimit)
			}
		})
	}
}

func TestNextCursorOnlyOnAFullPage(t *testing.T) {
	at := time.Now().UTC()

	tests := []struct {
		name  string
		count int
		limit int
		want  bool
	}{
		// A short page is the last page; emitting a cursor would cost the client
		// one more round trip to learn nothing.
		{name: "a short page ends the walk", count: 3, limit: 25, want: false},
		{name: "an empty page ends the walk", count: 0, limit: 25, want: false},
		{name: "a full page continues it", count: 25, limit: 25, want: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := nextCursor(tc.count, tc.limit, at, "conv_1")
			if (got != "") != tc.want {
				t.Errorf("nextCursor() = %q, want a cursor: %v", got, tc.want)
			}
		})
	}
}

func TestIdempotencyKeyIsBounded(t *testing.T) {
	// A key long enough to be a payload is not a key; bounding it keeps an index
	// entry from being used as storage.
	r := httptest.NewRequest(http.MethodPost, "/v1/conversations", nil)
	r.Header.Set(idempotenceH, strings.Repeat("k", 400))

	if got := idempotencyKey(r); len(got) != 128 {
		t.Errorf("key length = %d, want 128", len(got))
	}
}

func TestMapErrorNeverLeaksAnInternalError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "a missing thread", err: domain.ErrConversationNotFound, want: http.StatusNotFound},
		{name: "a duplicate thread", err: domain.ErrConversationExists, want: http.StatusConflict},
		{name: "a closed thread", err: domain.ErrConversationClosed, want: http.StatusConflict},
		{name: "an unreachable candidate", err: domain.ErrCandidateUnreachable, want: http.StatusForbidden},
		{name: "the daily cap", err: domain.ErrDailyLimitReached, want: http.StatusTooManyRequests},
		{name: "the directory being down", err: domain.ErrDirectoryUnavailable, want: http.StatusServiceUnavailable},
		{name: "a bad cursor", err: domain.ErrInvalidCursor, want: http.StatusBadRequest},
		{name: "a validation failure", err: domain.Invalid("body", "too long"), want: http.StatusUnprocessableEntity},
		{name: "no company on the principal", err: tenancy.ErrNotCompanyScoped, want: http.StatusForbidden},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			problem := httpx.AsProblem(mapError(tc.err))
			if problem.Status != tc.want {
				t.Errorf("status = %d, want %d", problem.Status, tc.want)
			}
		})
	}
}

func TestAnUnrecognisedErrorBecomesAGenericFailure(t *testing.T) {
	// A wrapped SQL error must not reach a client. mapError passes it through
	// untouched so httpx can turn it into a generic 500 and log the detail.
	leaky := &sqlishError{}

	problem := httpx.AsProblem(mapError(leaky))
	if problem.Status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", problem.Status)
	}
	if strings.Contains(problem.Detail, "pgx") || strings.Contains(problem.Detail, "SELECT") {
		t.Errorf("detail leaked internals: %q", problem.Detail)
	}
}

type sqlishError struct{}

func (e *sqlishError) Error() string {
	return `store: list conversations: pgx: ERROR relation "conversations" does not exist (SELECT c.id ...)`
}
