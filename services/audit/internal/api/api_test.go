package api

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/karmarat/platform/packages/goshared/httpx"
)

// The guards are the whole security of a read-only service, so they are tested
// at the door rather than trusted. Every case here is a request that must be
// refused *before* a handler runs, which is why the store can be nil: if a
// refusal ever stops happening, the test panics on the nil store instead of
// quietly passing with a 200.

func newTestAPI() http.Handler {
	return New(Config{
		Store:  nil,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}).Routes()
}

// principal builds the headers the gateway sets after it has verified a token.
func principal(kind, companyID string, permissions string) http.Header {
	header := http.Header{}
	header.Set(httpx.HeaderPrincipalType, kind)
	header.Set(httpx.HeaderPrincipalID, "acc_01")
	header.Set(httpx.HeaderPermissions, permissions)
	if companyID != "" {
		header.Set(httpx.HeaderCompanyID, companyID)
	}
	return header
}

func TestRoutesRefuseTheWrongCaller(t *testing.T) {
	const tenant = "0f8fad5b-d9cb-469f-a165-70867728950e"

	tests := []struct {
		name   string
		method string
		path   string
		header http.Header
		want   int
	}{
		{
			name:   "an unauthenticated request to a company trail",
			method: http.MethodGet,
			path:   "/v1/company-audit",
			header: http.Header{},
			want:   http.StatusUnauthorized,
		},
		{
			name:   "an unauthenticated request to the platform trail",
			method: http.MethodGet,
			path:   "/v1/platform/audit",
			header: http.Header{},
			want:   http.StatusUnauthorized,
		},
		{
			// The permission answers "may this role do this" and is checked
			// separately from the principal type.
			name:   "a company principal without the read permission",
			method: http.MethodGet,
			path:   "/v1/company-audit",
			header: principal("company", tenant, "jobs.read"),
			want:   http.StatusForbidden,
		},
		{
			// Reading the trail and downloading it are separate permissions in
			// the registry, so holding one must not grant the other.
			name:   "a company principal with read but not export",
			method: http.MethodGet,
			path:   "/v1/company-audit/export",
			header: principal("company", tenant, "company_audit.read"),
			want:   http.StatusForbidden,
		},
		{
			// The case that matters most: a tenant must not reach the feed that
			// spans every tenant, whatever permission string their token
			// happens to carry.
			name:   "a company principal at the platform trail",
			method: http.MethodGet,
			path:   "/v1/platform/audit",
			header: principal("company", tenant, "platform_audit.read"),
			want:   http.StatusForbidden,
		},
		{
			name:   "a candidate at a company trail",
			method: http.MethodGet,
			path:   "/v1/company-audit",
			header: principal("candidate", "", "company_audit.read"),
			want:   http.StatusForbidden,
		},
		{
			name:   "a platform principal at a company trail",
			method: http.MethodGet,
			path:   "/v1/company-audit",
			header: principal("platform", "", "company_audit.read"),
			want:   http.StatusForbidden,
		},
		{
			name:   "a platform principal without the platform read permission",
			method: http.MethodGet,
			path:   "/v1/platform/audit",
			header: principal("platform", "", "company_audit.read"),
			want:   http.StatusForbidden,
		},
		{
			// Nothing writes to this service over HTTP.
			name:   "a write to the company trail",
			method: http.MethodPost,
			path:   "/v1/company-audit",
			header: principal("company", tenant, "company_audit.read"),
			want:   http.StatusMethodNotAllowed,
		},
	}

	routes := newTestAPI()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := httptest.NewRequest(tt.method, tt.path, nil)
			request.Header = tt.header
			recorder := httptest.NewRecorder()

			routes.ServeHTTP(recorder, request)

			if recorder.Code != tt.want {
				t.Fatalf("%s %s = %d, want %d (body: %s)",
					tt.method, tt.path, recorder.Code, tt.want, recorder.Body.String())
			}
		})
	}
}

// A company principal whose token carries no tenant is refused rather than
// falling through to a query with an empty company id, which would compare
// against no rows today and against whatever an empty string means tomorrow.
func TestCompanyTrailRefusesAPrincipalWithNoTenant(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/v1/company-audit", nil)
	request.Header = principal("company", "", "company_audit.read")
	recorder := httptest.NewRecorder()

	newTestAPI().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body: %s)", recorder.Code, recorder.Body.String())
	}
}

// Every refusal is an RFC 9457 document, and none of them names an internal
// type, a table or a SQL fragment.
func TestRefusalsAreProblemDocuments(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/v1/platform/audit", nil)
	request.Header = principal("company", "0f8fad5b-d9cb-469f-a165-70867728950e", "platform_audit.read")
	recorder := httptest.NewRecorder()

	newTestAPI().ServeHTTP(recorder, request)

	if contentType := recorder.Header().Get("Content-Type"); contentType != "application/problem+json; charset=utf-8" {
		t.Fatalf("Content-Type = %q, want a problem document", contentType)
	}
	if body := recorder.Body.String(); body == "" {
		t.Fatal("refusal carried no body")
	}
}

// A bad filter must be a 422 naming the field, not a 500 — and it must be
// rejected before the query, so the nil store stays untouched.
func TestBadFiltersAreRejectedBeforeTheQuery(t *testing.T) {
	const tenant = "0f8fad5b-d9cb-469f-a165-70867728950e"

	tests := []struct {
		name  string
		query string
	}{
		{name: "a limit over the cap", query: "?limit=500"},
		{name: "a limit that is not a number", query: "?limit=all"},
		{name: "a malformed cursor", query: "?cursor=!!!not-base64!!!"},
		{name: "an unparseable date", query: "?from=last%20tuesday"},
		{name: "an inverted range", query: "?from=2026-03-04&to=2026-03-01"},
	}

	routes := newTestAPI()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/v1/company-audit"+tt.query, nil)
			request.Header = principal("company", tenant, "company_audit.read")
			recorder := httptest.NewRecorder()

			routes.ServeHTTP(recorder, request)

			if recorder.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422 (body: %s)", recorder.Code, recorder.Body.String())
			}
		})
	}
}
