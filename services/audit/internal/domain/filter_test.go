package domain

import (
	"encoding/base64"
	"errors"
	"testing"
	"time"
)

// The cursor and the date range are the two places where a wrong answer looks
// like an empty trail rather than like an error, so both fail loudly here.

func TestNewPageRoundTripsACursor(t *testing.T) {
	original := Cursor{
		At: time.Date(2026, 3, 4, 5, 6, 7, 890123000, time.UTC),
		ID: "evt_01HQ8",
	}

	page, err := NewPage(10, original.Encode())
	if err != nil {
		t.Fatalf("a cursor this package encoded did not decode: %v", err)
	}
	if !page.Cursor.At.Equal(original.At) || page.Cursor.ID != original.ID {
		t.Fatalf("round trip = %v/%q, want %v/%q",
			page.Cursor.At, page.Cursor.ID, original.At, original.ID)
	}
}

func TestNewPageNormalisesTheLimit(t *testing.T) {
	tests := []struct {
		name  string
		limit int
		want  int
	}{
		{name: "absent", limit: 0, want: DefaultPageLimit},
		{name: "negative", limit: -5, want: DefaultPageLimit},
		{name: "within range", limit: 40, want: 40},
		{name: "at the cap", limit: MaxPageLimit, want: MaxPageLimit},
		{name: "over the cap", limit: 5000, want: MaxPageLimit},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			page, err := NewPage(tt.limit, "")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if page.Limit != tt.want {
				t.Fatalf("limit %d normalised to %d, want %d", tt.limit, page.Limit, tt.want)
			}
		})
	}
}

// A bad cursor is rejected rather than quietly reset to the first page: a client
// that asked for page nine and got page one has lost eight pages of an audit
// trail with nothing to tell it so.
func TestNewPageRejectsAMalformedCursor(t *testing.T) {
	tests := []struct {
		name   string
		cursor string
	}{
		{name: "not base64", cursor: "!!!!not-base64!!!!"},
		{name: "base64 but not a pair", cursor: encodeRaw("no-separator-here")},
		{name: "an empty id", cursor: encodeRaw("2026-03-04T05:06:07Z|")},
		{name: "an unparseable timestamp", cursor: encodeRaw("yesterday|evt_1")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewPage(25, tt.cursor); !errors.Is(err, ErrInvalidCursor) {
				t.Fatalf("NewPage(%q) error = %v, want ErrInvalidCursor", tt.cursor, err)
			}
		})
	}
}

func TestNewFilterNormalisesTheAction(t *testing.T) {
	// A caller chasing a subject they found in a log should not have to know
	// that the stored column drops the namespace.
	filter, err := NewFilter("karmarat.application.rejected", "", "", "", "", "", Page{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if filter.Action != "application.rejected" {
		t.Fatalf("Action = %q, want application.rejected", filter.Action)
	}
}

func TestNewFilterReadsBothEndsOfTheRange(t *testing.T) {
	filter, err := NewFilter("", "", "", "", "2026-03-04", "2026-03-04", Page{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantFrom := time.Date(2026, 3, 4, 0, 0, 0, 0, time.UTC)
	if filter.From == nil || !filter.From.Equal(wantFrom) {
		t.Fatalf("From = %v, want %v", filter.From, wantFrom)
	}

	// One day asked for as a single date must return that day's activity, not
	// the single instant at its midnight.
	if filter.To == nil {
		t.Fatal("To was not parsed")
	}
	if !filter.To.After(wantFrom) {
		t.Fatalf("To = %v, want the end of the same day", filter.To)
	}
	nextMidnight := wantFrom.AddDate(0, 0, 1)
	if !filter.To.Before(nextMidnight) {
		t.Fatalf("To = %v, want it to stop short of %v", filter.To, nextMidnight)
	}

	// The bound has to cover an event published in the last second of the day.
	lastMoment := nextMidnight.Add(-time.Microsecond)
	if filter.To.Before(lastMoment) {
		t.Fatalf("To = %v would miss an event at %v", filter.To, lastMoment)
	}
}

func TestNewFilterAcceptsFullTimestamps(t *testing.T) {
	filter, err := NewFilter("", "", "", "", "2026-03-04T05:06:07Z", "2026-03-04T09:00:00+02:00", Page{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantFrom := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	if filter.From == nil || !filter.From.Equal(wantFrom) {
		t.Fatalf("From = %v, want %v", filter.From, wantFrom)
	}
	// An offset timestamp is normalised to UTC so the stored column and the
	// filter are compared in one timezone.
	wantTo := time.Date(2026, 3, 4, 7, 0, 0, 0, time.UTC)
	if filter.To == nil || !filter.To.Equal(wantTo) {
		t.Fatalf("To = %v, want %v", filter.To, wantTo)
	}
}

// An inverted range matches nothing, and an empty audit trail reads as "this
// never happened" rather than "you asked the wrong question".
func TestNewFilterRejectsAnInvertedRange(t *testing.T) {
	_, err := NewFilter("", "", "", "", "2026-03-04", "2026-03-01", Page{})

	var validationErr *ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("error = %v, want a ValidationError", err)
	}
	if validationErr.Field != "to" {
		t.Fatalf("field = %q, want to", validationErr.Field)
	}
}

func TestNewFilterRejectsAnUnparseableDate(t *testing.T) {
	for _, field := range []string{"from", "to"} {
		t.Run(field, func(t *testing.T) {
			from, to := "", ""
			if field == "from" {
				from = "last tuesday"
			} else {
				to = "04/03/2026"
			}

			_, err := NewFilter("", "", "", "", from, to, Page{})

			var validationErr *ValidationError
			if !errors.As(err, &validationErr) {
				t.Fatalf("error = %v, want a ValidationError", err)
			}
			if validationErr.Field != field {
				t.Fatalf("field = %q, want %q", validationErr.Field, field)
			}
		})
	}
}

// encodeRaw builds a cursor token by hand, so a test can present a token that
// is well-formed base64 and malformed inside it.
func encodeRaw(value string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}
