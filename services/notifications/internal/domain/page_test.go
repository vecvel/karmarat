package domain

import (
	"errors"
	"testing"
	"time"

	"github.com/karmarat/platform/packages/goshared/tenancy"
)

func TestNewPageLimits(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		limit int
		want  int
	}{
		{"absent falls back to the default", 0, DefaultPageLimit},
		{"negative falls back to the default", -5, DefaultPageLimit},
		{"a sensible limit is honoured", 10, 10},
		{"the maximum is honoured", MaxPageLimit, MaxPageLimit},
		{"above the maximum is capped", 5000, MaxPageLimit},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			page, err := NewPage(tc.limit, "")
			if err != nil {
				t.Fatalf("NewPage(%d) failed: %v", tc.limit, err)
			}
			if page.Limit != tc.want {
				t.Errorf("limit = %d, want %d", page.Limit, tc.want)
			}
		})
	}
}

func TestCursorRoundTrips(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 3, 14, 15, 9, 26, 535897932, time.UTC)
	token := Cursor{At: at, ID: "ntf_01HQ"}.Encode()

	page, err := NewPage(25, token)
	if err != nil {
		t.Fatalf("a cursor we produced was rejected: %v", err)
	}
	if !page.Cursor.At.Equal(at) {
		t.Errorf("timestamp = %s, want %s", page.Cursor.At, at)
	}
	if page.Cursor.ID != "ntf_01HQ" {
		t.Errorf("id = %q, want %q", page.Cursor.ID, "ntf_01HQ")
	}
}

func TestEmptyCursorEncodesToNothing(t *testing.T) {
	t.Parallel()

	if got := (Cursor{}).Encode(); got != "" {
		t.Errorf("an unset cursor encoded to %q", got)
	}
}

// A cursor we cannot read is an error, not a silent reset. A client that asked
// for page nine and was handed page one has quietly lost eight pages.
func TestBadCursorIsRejected(t *testing.T) {
	t.Parallel()

	for _, token := range []string{"not-base64!", "bm90LWEtY3Vyc29y", "", "|"} {
		if token == "" {
			continue
		}
		if _, err := NewPage(25, token); !errors.Is(err, ErrInvalidCursor) {
			t.Errorf("NewPage(%q) error = %v, want ErrInvalidCursor", token, err)
		}
	}
}

// The tenant key is what the directory and the preference table are keyed by.
// A candidate must resolve to the sentinel whatever company their notifications
// mention, or they would accumulate a separate inbox per employer.
func TestRecipientKeys(t *testing.T) {
	t.Parallel()

	company := Recipient{
		PrincipalType: tenancy.PrincipalCompany,
		AccountID:     "acc_1",
		CompanyID:     "11111111-1111-1111-1111-111111111111",
	}
	candidate := Recipient{PrincipalType: tenancy.PrincipalCandidate, AccountID: "acc_1"}

	if company.TenantKey() == NoCompany {
		t.Error("a company principal resolved to the tenant-less sentinel")
	}
	if candidate.TenantKey() != NoCompany {
		t.Errorf("a candidate resolved to %q, want the sentinel", candidate.TenantKey())
	}

	// Same account id, different populations: their live streams must not be
	// the same channel.
	if company.StreamKey() == candidate.StreamKey() {
		t.Error("a company principal and a candidate share a stream key")
	}
}

func TestAddressable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		email string
		want  bool
	}{
		{"ada@example.com", true},
		{"", false},
		{"not-an-address", false},
	}
	for _, tc := range tests {
		got := Recipient{Email: tc.email}.Addressable()
		if got != tc.want {
			t.Errorf("Addressable(%q) = %v, want %v", tc.email, got, tc.want)
		}
	}
}
