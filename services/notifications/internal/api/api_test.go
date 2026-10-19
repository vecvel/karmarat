package api_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/karmarat/platform/packages/goshared/httpx"
	"github.com/karmarat/platform/packages/goshared/postgres"
	"github.com/karmarat/platform/packages/goshared/tenancy"
	"github.com/karmarat/platform/services/notifications/internal/api"
	"github.com/karmarat/platform/services/notifications/internal/domain"
	"github.com/karmarat/platform/services/notifications/internal/realtime"
	"github.com/karmarat/platform/services/notifications/internal/store"
	"github.com/karmarat/platform/services/notifications/migrations"
)

const (
	companyA = "11111111-1111-1111-1111-111111111111"
	companyB = "22222222-2222-2222-2222-222222222222"
)

// newAPI builds the HTTP surface over a real database, or skips.
//
// The handlers are worth exercising end to end because the property under test
// — that a request only ever reaches its own notifications — is split between
// the guard that builds the recipient and the SQL that filters on it. Testing
// either alone would miss the pairing.
func newAPI(t *testing.T) (http.Handler, *store.Store, context.Context) {
	t.Helper()

	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set; skipping the database-backed tests")
	}

	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	pool, err := postgres.Connect(ctx, postgres.Config{URL: url}, logger)
	if err != nil {
		t.Fatalf("could not connect to the test database: %v", err)
	}
	t.Cleanup(pool.Close)

	files, err := postgres.LoadMigrations(migrations.FS, ".")
	if err != nil {
		t.Fatalf("could not load migrations: %v", err)
	}
	if err := postgres.Migrate(ctx, pool, files, logger); err != nil {
		t.Fatalf("could not migrate the test database: %v", err)
	}
	if _, err := pool.Exec(ctx, `TRUNCATE notifications, notification_preferences,
		notification_recipients, email_outbox`); err != nil {
		t.Fatalf("could not reset the test database: %v", err)
	}

	st := store.New(pool)

	// A nil Redis client: the hub degrades to a stream that never fires, which
	// is what lets the HTTP surface be tested without a cache.
	handler := api.New(api.Config{
		Store:          st,
		Hub:            realtime.New(nil, logger),
		Logger:         logger,
		Heartbeat:      20 * time.Millisecond,
		StreamLifetime: 120 * time.Millisecond,
	}).Routes()

	return handler, st, ctx
}

// asCompany sets the headers the gateway sets after it has verified a token.
func asCompany(r *http.Request, account, company string, permissions ...string) {
	r.Header.Set(httpx.HeaderPrincipalType, string(tenancy.PrincipalCompany))
	r.Header.Set(httpx.HeaderPrincipalID, account)
	r.Header.Set(httpx.HeaderCompanyID, company)
	r.Header.Set(httpx.HeaderPermissions, strings.Join(permissions, ","))
	r.Header.Set(httpx.HeaderPrincipalMail, account+"@example.com")
}

func asCandidate(r *http.Request, account string) {
	r.Header.Set(httpx.HeaderPrincipalType, string(tenancy.PrincipalCandidate))
	r.Header.Set(httpx.HeaderPrincipalID, account)
	r.Header.Set(httpx.HeaderPrincipalMail, account+"@example.com")
}

type listResponse struct {
	Notifications []api.NotificationView `json:"notifications"`
	NextCursor    string                 `json:"nextCursor"`
	UnreadCount   int                    `json:"unreadCount"`
}

func seed(t *testing.T, ctx context.Context, st *store.Store, in store.CreateInput) domain.Notification {
	t.Helper()
	created, _, err := st.Create(ctx, in)
	if err != nil {
		t.Fatalf("could not seed a notification: %v", err)
	}
	return created
}

func TestListReturnsOnlyTheCallersOwn(t *testing.T) {
	handler, st, ctx := newAPI(t)

	mine := seed(t, ctx, st, store.CreateInput{
		Recipient: domain.Recipient{
			PrincipalType: tenancy.PrincipalCompany, AccountID: "acc_me", CompanyID: companyA,
		},
		Type: domain.TypeApplicationSubmitted, Title: "Mine", EventID: "evt_1",
	})
	// A colleague at the same company, and the same account id at another
	// company: both are the mistakes a missing predicate would surface as.
	seed(t, ctx, st, store.CreateInput{
		Recipient: domain.Recipient{
			PrincipalType: tenancy.PrincipalCompany, AccountID: "acc_colleague", CompanyID: companyA,
		},
		Type: domain.TypeApplicationSubmitted, Title: "A colleague's", EventID: "evt_2",
	})
	seed(t, ctx, st, store.CreateInput{
		Recipient: domain.Recipient{
			PrincipalType: tenancy.PrincipalCompany, AccountID: "acc_me", CompanyID: companyB,
		},
		Type: domain.TypeApplicationSubmitted, Title: "Another tenant's", EventID: "evt_3",
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/notifications", nil)
	asCompany(req, "acc_me", companyA, "applications.read")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}

	var body listResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("could not decode the response: %v", err)
	}
	if len(body.Notifications) != 1 {
		t.Fatalf("returned %d notifications, want 1: %+v", len(body.Notifications), body.Notifications)
	}
	if body.Notifications[0].ID != mine.ID {
		t.Errorf("returned %q, want %q", body.Notifications[0].ID, mine.ID)
	}
	if body.UnreadCount != 1 {
		t.Errorf("unreadCount = %d, want 1", body.UnreadCount)
	}
}

// The tenant is never taken from the request. A company id in the query string
// must change nothing at all.
func TestTenantCannotBeOverriddenByTheRequest(t *testing.T) {
	handler, st, ctx := newAPI(t)

	seed(t, ctx, st, store.CreateInput{
		Recipient: domain.Recipient{
			PrincipalType: tenancy.PrincipalCompany, AccountID: "acc_me", CompanyID: companyB,
		},
		Type: domain.TypeApplicationSubmitted, Title: "Another tenant's", EventID: "evt_1",
	})

	req := httptest.NewRequest(http.MethodGet,
		"/v1/notifications?companyId="+companyB+"&accountId=acc_me", nil)
	asCompany(req, "acc_me", companyA, "applications.read")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var body listResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("could not decode the response: %v", err)
	}
	if len(body.Notifications) != 0 {
		t.Fatalf("a query parameter widened the tenant: got %d rows", len(body.Notifications))
	}
}

func TestMarkingAnotherRecipientsNotificationIsNotFound(t *testing.T) {
	handler, st, ctx := newAPI(t)

	theirs := seed(t, ctx, st, store.CreateInput{
		Recipient: domain.Recipient{
			PrincipalType: tenancy.PrincipalCompany, AccountID: "acc_owner", CompanyID: companyA,
		},
		Type: domain.TypeApplicationSubmitted, Title: "Not yours", EventID: "evt_1",
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/notifications/"+theirs.ID+"/read", nil)
	asCandidate(req, "acc_owner")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// 404, not 403: the refusal must not confirm that the id exists.
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", rec.Code, rec.Body)
	}
	if contentType := rec.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "application/problem+json") {
		t.Errorf("content type = %q, want a problem document", contentType)
	}
}

func TestAnonymousIsRejected(t *testing.T) {
	handler, _, _ := newAPI(t)

	for _, path := range []string{
		"/v1/notifications",
		"/v1/notifications/preferences",
		"/v1/notifications/stream",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("GET %s status = %d, want 401", path, rec.Code)
		}
	}
}

// A company principal with no tenant has no inbox to scope: the request is
// refused rather than answered from an unfiltered query.
func TestCompanyPrincipalWithoutATenantIsRefused(t *testing.T) {
	handler, _, _ := newAPI(t)

	req := httptest.NewRequest(http.MethodGet, "/v1/notifications", nil)
	req.Header.Set(httpx.HeaderPrincipalType, string(tenancy.PrincipalCompany))
	req.Header.Set(httpx.HeaderPrincipalID, "acc_me")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", rec.Code, rec.Body)
	}
}

func TestListRejectsABadCursor(t *testing.T) {
	handler, _, _ := newAPI(t)

	req := httptest.NewRequest(http.MethodGet, "/v1/notifications?cursor=not-a-cursor", nil)
	asCandidate(req, "acc_me")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", rec.Code, rec.Body)
	}
}

func TestPreferencesRoundTrip(t *testing.T) {
	handler, _, _ := newAPI(t)

	body := strings.NewReader(`{"channels":{"application.rejected":{"inApp":true,"email":false}}}`)
	req := httptest.NewRequest(http.MethodPut, "/v1/notifications/preferences", body)
	asCandidate(req, "acc_me")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/notifications/preferences", nil)
	asCandidate(req, "acc_me")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var view struct {
		Types []struct {
			Type  string `json:"type"`
			InApp bool   `json:"inApp"`
			Email bool   `json:"email"`
		} `json:"types"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatalf("could not decode the preferences: %v", err)
	}

	var found bool
	for _, entry := range view.Types {
		if entry.Type == string(domain.TypeApplicationRejected) {
			found = true
			if entry.Email {
				t.Error("the opt-out did not take effect")
			}
		}
		if entry.Type == string(domain.TypeApplicationSubmitted) {
			t.Error("a candidate was offered a company-only notification type")
		}
	}
	if !found {
		t.Error("the preferences screen omitted a type the candidate receives")
	}
}

// Setting a preference for a type the principal cannot receive is a 422 rather
// than a stored setting that does nothing.
func TestPreferencesRejectIrrelevantType(t *testing.T) {
	handler, _, _ := newAPI(t)

	body := strings.NewReader(`{"channels":{"application.submitted":{"inApp":true,"email":true}}}`)
	req := httptest.NewRequest(http.MethodPut, "/v1/notifications/preferences", body)
	asCandidate(req, "acc_me")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", rec.Code, rec.Body)
	}
}

func TestPreferencesRejectUnknownType(t *testing.T) {
	handler, _, _ := newAPI(t)

	body := strings.NewReader(`{"channels":{"application.invented":{"inApp":true}}}`)
	req := httptest.NewRequest(http.MethodPut, "/v1/notifications/preferences", body)
	asCandidate(req, "acc_me")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", rec.Code, rec.Body)
	}
}

// The stream has to announce itself as an event stream and tell the proxies in
// front of it not to buffer, or the first frame arrives when the response ends
// — which, for a stream, is never.
func TestStreamHeadersAndHeartbeat(t *testing.T) {
	handler, _, _ := newAPI(t)

	req := httptest.NewRequest(http.MethodGet, "/v1/notifications/stream", nil)
	asCandidate(req, "acc_me")
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		handler.ServeHTTP(rec, req)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the stream did not end within its configured lifetime")
	}

	if got := rec.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("content type = %q, want text/event-stream", got)
	}
	if got := rec.Header().Get("X-Accel-Buffering"); got != "no" {
		t.Errorf("X-Accel-Buffering = %q, want no", got)
	}
	if !strings.Contains(rec.Header().Get("Cache-Control"), "no-transform") {
		t.Errorf("Cache-Control = %q, want it to forbid transformation",
			rec.Header().Get("Cache-Control"))
	}

	body := rec.Body.String()
	if !strings.Contains(body, "retry:") {
		t.Error("the stream did not tell the client how soon to reconnect")
	}
	if !strings.Contains(body, ": keep-alive") {
		t.Error("the stream sent no heartbeat, so an idle proxy would close it")
	}
	// The stream retires itself rather than being cut mid-frame, and says so.
	if !strings.Contains(body, "event: reconnect") {
		t.Error("the stream ended without asking the client to reconnect")
	}
}

// Every authenticated request refreshes the directory, which is the only way a
// company-side event finds anybody to notify.
func TestRequestRecordsTheRecipient(t *testing.T) {
	handler, st, ctx := newAPI(t)

	req := httptest.NewRequest(http.MethodGet, "/v1/notifications", nil)
	asCompany(req, "acc_me", companyA, "applications.read")
	handler.ServeHTTP(httptest.NewRecorder(), req)

	recipients, err := st.ExpandAudience(ctx, domain.Audience{
		PrincipalType: tenancy.PrincipalCompany,
		CompanyID:     companyA,
		Permission:    "applications.read",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(recipients) != 1 || recipients[0].AccountID != "acc_me" {
		t.Fatalf("the request did not put the principal in the directory: %+v", recipients)
	}
	if recipients[0].Email != "acc_me@example.com" {
		t.Errorf("the address from the gateway header was not recorded: %q", recipients[0].Email)
	}
}
