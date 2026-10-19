package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/karmarat/platform/packages/goshared/httpx"
	"github.com/karmarat/platform/packages/goshared/tenancy"
	"github.com/karmarat/platform/services/offers/internal/domain"
)

const testCompany = "11111111-1111-1111-1111-111111111111"

// The guard tests below drive a real router with no store behind it. That is
// deliberate: every case here must be refused before a handler reaches the
// database, so a case that got through would panic and fail loudly rather than
// pass quietly.
func testAPI() *API {
	return New(Config{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
}

type caller struct {
	principal   tenancy.PrincipalType
	subject     string
	companyID   string
	permissions string
}

func (c caller) request(method, path, body string) *http.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if c.principal == "" {
		return r
	}
	r.Header.Set(httpx.HeaderPrincipalType, string(c.principal))
	r.Header.Set(httpx.HeaderPrincipalID, c.subject)
	r.Header.Set(httpx.HeaderCompanyID, c.companyID)
	r.Header.Set(httpx.HeaderPermissions, c.permissions)
	return r
}

func TestRoutesRefuseTheWrongCaller(t *testing.T) {
	routes := testAPI().Routes()

	recruiter := caller{
		principal:   tenancy.PrincipalCompany,
		subject:     "usr_01HZX3T9QKD6M0V8B2N4C7E5FG",
		companyID:   testCompany,
		permissions: "offers.read",
	}
	candidate := caller{
		principal:   tenancy.PrincipalCandidate,
		subject:     "acct_01HZX3T9QKD6M0V8B2N4C7E5FG",
		permissions: "offers.read,offers.approve,offers.send",
	}

	tests := []struct {
		name   string
		caller caller
		method string
		path   string
		want   int
	}{
		{name: "an anonymous read", caller: caller{}, method: http.MethodGet, path: "/v1/offers",
			want: http.StatusUnauthorized},
		// A candidate token must not reach a company endpoint even carrying the
		// right permission strings: the portal boundary is enforced by type.
		{name: "a candidate reading the company's offers", caller: candidate,
			method: http.MethodGet, path: "/v1/offers", want: http.StatusForbidden},
		{name: "a candidate accepting their own offer", caller: candidate, method: http.MethodPost,
			path: "/v1/offers/ofr_1/respond", want: http.StatusForbidden},

		// Reading an offer says nothing about approving, sending or deleting
		// one; each is a permission of its own precisely so they can be held
		// separately.
		{name: "a reader approving", caller: recruiter, method: http.MethodPost,
			path: "/v1/offers/ofr_1/approve", want: http.StatusForbidden},
		{name: "a reader sending", caller: recruiter, method: http.MethodPost,
			path: "/v1/offers/ofr_1/send", want: http.StatusForbidden},
		{name: "a reader submitting", caller: recruiter, method: http.MethodPost,
			path: "/v1/offers/ofr_1/submit", want: http.StatusForbidden},
		{name: "a reader editing", caller: recruiter, method: http.MethodPatch,
			path: "/v1/offers/ofr_1", want: http.StatusForbidden},
		{name: "a reader deleting", caller: recruiter, method: http.MethodDelete,
			path: "/v1/offers/ofr_1", want: http.StatusForbidden},
		{name: "a reader creating", caller: recruiter, method: http.MethodPost,
			path: "/v1/offers", want: http.StatusForbidden},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			routes.ServeHTTP(w, tc.caller.request(tc.method, tc.path, `{}`))

			if w.Code != tc.want {
				t.Errorf("status = %d, want %d", w.Code, tc.want)
			}
		})
	}
}

func TestACompanyPrincipalWithoutATenantIsRefused(t *testing.T) {
	// The tenant comes from the principal and from nowhere else, so a principal
	// that carries no company has no data to read — and must not be able to name
	// one in a query parameter instead.
	w := httptest.NewRecorder()
	testAPI().Routes().ServeHTTP(w, caller{
		principal:   tenancy.PrincipalCompany,
		subject:     "usr_01HZX3T9QKD6M0V8B2N4C7E5FG",
		permissions: "offers.read",
	}.request(http.MethodGet, "/v1/offers?companyId="+testCompany, ""))

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestWritingMoneyRequiresBeingAbleToSeeIt(t *testing.T) {
	// Writing a figure you may not read is a way to launder compensation data:
	// draft the package, have a colleague read it back, and the permission has
	// been bypassed without ever being checked.
	routes := testAPI().Routes()

	tests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "creating an offer", method: http.MethodPost, path: "/v1/offers",
			body: `{"applicationId":"app_1","candidateId":"cnd_1","designation":"Staff Engineer",
			        "departmentName":"Platform","baseSalary":175000,"currency":"USD",
			        "joiningDate":"2026-04-06"}`},
		{name: "editing the salary", method: http.MethodPatch, path: "/v1/offers/ofr_1",
			body: `{"baseSalary":195000}`},
		{name: "editing only the currency", method: http.MethodPatch, path: "/v1/offers/ofr_1",
			body: `{"currency":"EUR"}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			routes.ServeHTTP(w, caller{
				principal:   tenancy.PrincipalCompany,
				subject:     "usr_01HZX3T9QKD6M0V8B2N4C7E5FG",
				companyID:   testCompany,
				permissions: "offers.create,offers.update,offers.read",
			}.request(tc.method, tc.path, tc.body))

			if w.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want %d", w.Code, http.StatusForbidden)
			}

			var problem httpx.Problem
			if err := json.Unmarshal(w.Body.Bytes(), &problem); err != nil {
				t.Fatalf("the response was not a problem document: %v", err)
			}
			if got := strings.Join(problem.Errors["required"], ","); got != permViewCompensation {
				t.Errorf("required = %q, want %q", got, permViewCompensation)
			}
		})
	}
}

func TestAPatchWithoutMoneyDoesNotTouchCompensation(t *testing.T) {
	// The other half of the rule above: a coordinator without
	// `offers.view_compensation` must still be able to fix a work location.
	tests := []struct {
		name  string
		patch patchOfferRequest
		want  bool
	}{
		{name: "a location change", patch: patchOfferRequest{WorkLocation: stringPtr("Remote")}, want: false},
		{name: "a salary change", patch: patchOfferRequest{BaseSalary: floatPtr(1)}, want: true},
		{name: "a currency change", patch: patchOfferRequest{Currency: stringPtr("EUR")}, want: true},
		{name: "a bonus change", patch: patchOfferRequest{SignOnBonus: floatPtr(0)}, want: true},
		{name: "an equity note", patch: patchOfferRequest{EquityShares: stringPtr("none")}, want: true},
		{name: "a pay frequency change", patch: patchOfferRequest{PayFrequency: stringPtr("monthly")}, want: true},
		{name: "an empty patch", patch: patchOfferRequest{}, want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.patch.touchesCompensation(); got != tc.want {
				t.Errorf("touchesCompensation() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAPartialMoneyEditKeepsTheRestOfThePackage(t *testing.T) {
	// A currency that was never validated against the amount is how an offer
	// ends up reading "¥175,000.00". Re-parsing the whole package means the
	// combination that lands in the row is one that was checked together.
	current := domain.Offer{
		BaseSalary:   17_500_000,
		SignOnBonus:  2_500_000,
		Currency:     "USD",
		PayFrequency: "annual",
		AnnualBonus:  "15% of base",
	}

	patch, err := testAPI().offerPatch(patchOfferRequest{BaseSalary: floatPtr(195_000)}, current)
	if err != nil {
		t.Fatalf("offerPatch() error = %v", err)
	}
	if patch.Compensation == nil {
		t.Fatal("offerPatch() produced no compensation")
	}

	if patch.Compensation.BaseSalary != 19_500_000 {
		t.Errorf("BaseSalary = %d, want 19500000", patch.Compensation.BaseSalary)
	}
	if patch.Compensation.Currency != "USD" {
		t.Errorf("Currency = %q, want USD", patch.Compensation.Currency)
	}
	// Unspecified money carries over untouched — including the sign-on bonus,
	// which a naive merge would zero.
	if patch.Compensation.SignOnBonus != 2_500_000 {
		t.Errorf("SignOnBonus = %d, want 2500000", patch.Compensation.SignOnBonus)
	}
	if patch.Compensation.AnnualBonus != "15% of base" {
		t.Errorf("AnnualBonus = %q, want the stored note", patch.Compensation.AnnualBonus)
	}
}

func TestANewOfferReportsEveryMissingFieldAtOnce(t *testing.T) {
	// A recruiter who mistyped three things should be told about three things,
	// not discover them one request at a time.
	_, err := testAPI().offerInput(offerRequest{})

	problem := httpx.AsProblem(err)
	if problem.Status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", problem.Status, http.StatusUnprocessableEntity)
	}

	for _, field := range []string{"applicationId", "candidateId", "designation", "departmentName", "joiningDate"} {
		if len(problem.Errors[field]) == 0 {
			t.Errorf("no error reported for %q", field)
		}
	}
}

func TestANewOfferConvertsTheMoneyOnce(t *testing.T) {
	input, err := testAPI().offerInput(offerRequest{
		ApplicationID:  "app_01HZX3T9QKD6M0V8B2N4C7E5FG",
		CandidateID:    "cnd_01HZX3T9QKD6M0V8B2N4C7E5FG",
		Designation:    "Staff Engineer",
		DepartmentName: "Platform",
		BaseSalary:     175_000,
		SignOnBonus:    25_000,
		Currency:       "usd",
		JoiningDate:    "2026-04-06",
	})
	if err != nil {
		t.Fatalf("offerInput() error = %v", err)
	}

	if input.Compensation.BaseSalary != 17_500_000 || input.Compensation.SignOnBonus != 2_500_000 {
		t.Errorf("compensation = %+v, want minor units", input.Compensation)
	}
	if input.Compensation.Currency != "USD" {
		t.Errorf("Currency = %q, want USD", input.Compensation.Currency)
	}
	if !input.JoiningDate.Equal(time.Date(2026, 4, 6, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("JoiningDate = %v, want 2026-04-06 UTC", input.JoiningDate)
	}
}

func TestACurrencyIsSuppliedWhenTheClientOmitsOne(t *testing.T) {
	// The column is char(3) and cannot hold an empty string, so the choice is
	// between a configured default and a 422 on a field the portal's own type
	// marks optional.
	input, err := testAPI().offerInput(offerRequest{
		ApplicationID:  "app_1",
		CandidateID:    "cnd_1",
		Designation:    "Staff Engineer",
		DepartmentName: "Platform",
		BaseSalary:     100,
		JoiningDate:    "2026-04-06",
	})
	if err != nil {
		t.Fatalf("offerInput() error = %v", err)
	}
	if input.Compensation.Currency != "USD" {
		t.Errorf("Currency = %q, want the configured default", input.Compensation.Currency)
	}
}

func TestAnExpiryInThePastIsRefused(t *testing.T) {
	// It would be swept to "expired" within minutes, leaving a recruiter looking
	// at a dead offer they had just created and no explanation of why.
	past := time.Now().Add(-24 * time.Hour).Format(time.RFC3339)
	future := time.Now().Add(24 * time.Hour).Format(time.RFC3339)

	if _, err := parseExpiry(&past); err == nil {
		t.Error("a past expiry was accepted")
	}
	parsed, err := parseExpiry(&future)
	if err != nil {
		t.Fatalf("a future expiry was refused: %v", err)
	}
	if parsed == nil {
		t.Error("a future expiry produced no timestamp")
	}
	if absent, err := parseExpiry(nil); err != nil || absent != nil {
		t.Errorf("an absent expiry = (%v, %v), want (nil, nil)", absent, err)
	}
}

func TestClearingAnExpiryIsDistinguishableFromLeavingItAlone(t *testing.T) {
	// An explicit null means "no deadline"; an absent key means "unchanged". A
	// *time.Time alone cannot tell the two apart, which is why the patch holds
	// the raw message.
	cleared, err := parseRawExpiry(json.RawMessage(`null`))
	if err != nil {
		t.Fatalf("clearing the expiry failed: %v", err)
	}
	if cleared != nil {
		t.Errorf("clearing produced %v, want nil", cleared)
	}

	if _, err := parseRawExpiry(json.RawMessage(`"not-a-date"`)); err == nil {
		t.Error("a malformed expiry was accepted")
	}
}

func TestCustomFieldsAreBounded(t *testing.T) {
	tests := []struct {
		name    string
		fields  []domain.CustomField
		wantErr bool
	}{
		{name: "an ordinary clause",
			fields: []domain.CustomField{{Key: "Relocation", Value: "Covered"}}},
		{name: "a nameless clause",
			fields: []domain.CustomField{{Key: "  ", Value: "Covered"}}, wantErr: true},
		{name: "a clause used as storage",
			fields:  []domain.CustomField{{Key: "Notes", Value: strings.Repeat("x", 501)}},
			wantErr: true},
		{name: "too many clauses", fields: make([]domain.CustomField, 51), wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := validateCustomFields(tc.fields)
			if (err != nil) != tc.wantErr {
				t.Errorf("validateCustomFields() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestIdempotencyKeyIsBounded(t *testing.T) {
	// A key long enough to be a payload is not a key; bounding it keeps a unique
	// index entry from being used as storage.
	r := httptest.NewRequest(http.MethodPost, "/v1/offers/ofr_1/send", nil)
	r.Header.Set(idempotenceH, strings.Repeat("k", 400))

	if got := idempotencyKey(r); len(got) != 128 {
		t.Errorf("key length = %d, want 128", len(got))
	}
}

func TestPaginationHoldsThePlatformsLimits(t *testing.T) {
	tests := []struct {
		name      string
		query     string
		wantLimit int
		wantErr   bool
	}{
		{name: "the default", query: "", wantLimit: 0},
		{name: "an explicit page", query: "?limit=50", wantLimit: 50},
		{name: "the maximum", query: "?limit=100", wantLimit: 100},
		{name: "beyond the maximum", query: "?limit=101", wantErr: true},
		{name: "zero", query: "?limit=0", wantErr: true},
		{name: "not a number", query: "?limit=all", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			limit, _, err := pagination(httptest.NewRequest(http.MethodGet, "/v1/offers"+tc.query, nil))
			if (err != nil) != tc.wantErr {
				t.Fatalf("pagination() error = %v, wantErr %v", err, tc.wantErr)
			}
			if err == nil && limit != tc.wantLimit {
				t.Errorf("limit = %d, want %d", limit, tc.wantLimit)
			}
		})
	}
}

func TestMapErrorNeverLeaksAnInternalError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "a missing offer", err: domain.ErrOfferNotFound, want: http.StatusNotFound},
		// An illegal move is a conflict with the record's state, not a bad
		// request: the body was fine, the offer had moved on.
		{name: "an illegal transition",
			err:  domain.Transition(domain.StatusDraft, domain.StatusSent),
			want: http.StatusConflict},
		{name: "editing an approved offer", err: domain.ErrNotEditable, want: http.StatusConflict},
		{name: "deleting a sent offer", err: domain.ErrNotDeletable, want: http.StatusConflict},
		{name: "responding after expiry", err: domain.ErrExpired, want: http.StatusConflict},
		{name: "approving your own offer", err: domain.ErrSelfApproval, want: http.StatusConflict},
		{name: "a reused idempotency key", err: domain.ErrIdempotencyConflict, want: http.StatusConflict},
		{name: "a validation failure", err: domain.Invalid("baseSalary", "too big"),
			want: http.StatusUnprocessableEntity},
		{name: "no company on the principal", err: tenancy.ErrNotCompanyScoped, want: http.StatusForbidden},
		{name: "a cross-tenant read", err: tenancy.ErrCrossTenant, want: http.StatusForbidden},
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

func TestAnUnrecognizedErrorBecomesAGenericFailure(t *testing.T) {
	// Anything this service does not recognize must not reach a client: a
	// wrapped SQL error in `detail` is a map of the schema.
	problem := httpx.AsProblem(mapError(errUnknown{}))

	if problem.Status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", problem.Status, http.StatusInternalServerError)
	}
	if strings.Contains(problem.Detail, "offers_send_idempotency_idx") {
		t.Errorf("detail leaked the internal error: %q", problem.Detail)
	}
}

type errUnknown struct{}

func (errUnknown) Error() string {
	return `ERROR: duplicate key value violates unique constraint "offers_send_idempotency_idx"`
}

func stringPtr(v string) *string  { return &v }
func floatPtr(v float64) *float64 { return &v }
