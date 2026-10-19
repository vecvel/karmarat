package events

import (
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	platformevents "github.com/karmarat/platform/packages/goshared/events"
)

// These cover the decisions the recorder makes before it touches the database:
// which row an event becomes, whether it belongs to a tenant, and what survives
// of a payload that does not parse. Each of them is a way the trail can end up
// silently wrong — a duplicated entry, an event filed under no tenant, a fact
// dropped — which is the failure this service exists to rule out.

func newTestRecorder() *Recorder {
	// The store is nil on purpose: nothing below reaches it, so a test that
	// accidentally starts calling the database fails loudly rather than
	// pretending to have written something.
	return NewRecorder(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestEntryIDIsTheEventsOwnID(t *testing.T) {
	recorder := newTestRecorder()

	envelope := platformevents.Envelope{
		ID:      "evt_01HQ8",
		Subject: "karmarat.job.published",
	}

	if got := recorder.entryID(envelope); got != "evt_01HQ8" {
		t.Fatalf("entryID = %q, want the event's own id", got)
	}
}

// The fallback has to be a function of the event's content, or a redelivery of
// an id-less message would insert a second row and the trail would show the same
// action twice.
func TestEntryIDIsStableForAnIDLessEvent(t *testing.T) {
	recorder := newTestRecorder()

	at := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	envelope := platformevents.Envelope{
		Subject:    "karmarat.job.published",
		OccurredAt: at,
		Payload:    json.RawMessage(`{"jobId":"job_1"}`),
	}

	first := recorder.entryID(envelope)
	second := recorder.entryID(envelope)

	if first != second {
		t.Fatalf("the same event hashed to %q and then %q", first, second)
	}
	if first == "" {
		t.Fatal("entryID produced nothing for an id-less event")
	}

	different := envelope
	different.Payload = json.RawMessage(`{"jobId":"job_2"}`)
	if recorder.entryID(different) == first {
		t.Fatal("two different events collapsed onto one audit entry")
	}

	later := envelope
	later.OccurredAt = at.Add(time.Second)
	if recorder.entryID(later) == first {
		t.Fatal("the same fact at two different times collapsed onto one entry")
	}
}

func TestTenantOfAcceptsOnlyAUUID(t *testing.T) {
	recorder := newTestRecorder()

	tests := []struct {
		name      string
		companyID string
		want      string
	}{
		{
			name:      "a real tenant",
			companyID: "0f8fad5b-d9cb-469f-a165-70867728950e",
			want:      "0f8fad5b-d9cb-469f-a165-70867728950e",
		},
		{
			name:      "surrounding whitespace",
			companyID: "  0f8fad5b-d9cb-469f-a165-70867728950e  ",
			want:      "0f8fad5b-d9cb-469f-a165-70867728950e",
		},
		{
			// Platform-wide facts belong to no tenant and must stay out of every
			// company's trail.
			name:      "a platform event",
			companyID: "",
			want:      "",
		},
		{
			// A malformed id would fail the insert, the insert failing would nak
			// the message, and one bad publisher would stall the whole trail.
			name:      "a company id that is not a uuid",
			companyID: "acme",
			want:      "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := recorder.tenantOf(platformevents.Envelope{
				Subject:   "karmarat.company.updated",
				CompanyID: tt.companyID,
			})
			if got != tt.want {
				t.Fatalf("tenantOf(%q) = %q, want %q", tt.companyID, got, tt.want)
			}
		})
	}
}

func TestDecodePayloadKeepsWhatItCannotParse(t *testing.T) {
	recorder := newTestRecorder()

	t.Run("an ordinary object", func(t *testing.T) {
		payload := recorder.decodePayload(platformevents.Envelope{
			Payload: json.RawMessage(`{"jobId":"job_1","headcount":3}`),
		})
		if payload["jobId"] != "job_1" {
			t.Fatalf("decoded payload lost its fields: %#v", payload)
		}
	})

	t.Run("an empty payload", func(t *testing.T) {
		payload := recorder.decodePayload(platformevents.Envelope{})
		if payload == nil || len(payload) != 0 {
			t.Fatalf("empty payload decoded to %#v, want an empty map", payload)
		}
	})

	t.Run("a payload that is not an object is still evidence", func(t *testing.T) {
		payload := recorder.decodePayload(platformevents.Envelope{
			Payload: json.RawMessage(`"a bare string"`),
		})
		raw, ok := payload["_unstructured"].(string)
		if !ok || raw != `"a bare string"` {
			t.Fatalf("unparseable payload was dropped instead of recorded: %#v", payload)
		}
	})
}

// A zero timestamp would sort to the bottom of every trail forever, where it
// would never be read again.
func TestOccurredAtFallsBackToNow(t *testing.T) {
	at := time.Date(2026, 3, 4, 5, 6, 7, 0, time.FixedZone("CET", 2*3600))

	if got := occurredAt(platformevents.Envelope{OccurredAt: at}); !got.Equal(at) {
		t.Fatalf("occurredAt = %v, want %v", got, at)
	}
	if got := occurredAt(platformevents.Envelope{OccurredAt: at}); got.Location() != time.UTC {
		t.Fatalf("occurredAt kept a non-UTC location: %v", got.Location())
	}

	before := time.Now().UTC().Add(-time.Second)
	got := occurredAt(platformevents.Envelope{})
	if got.Before(before) {
		t.Fatalf("a zero timestamp was not replaced: %v", got)
	}
}

// Subscribing to a list is subscribing to the list somebody forgets to update,
// and a subject missing from the trail looks exactly like nothing happening.
func TestSubjectsIsTheWholeNamespace(t *testing.T) {
	subjects := Subjects()

	if len(subjects) != 1 || subjects[0] != platformevents.AllSubjects {
		t.Fatalf("Subjects() = %v, want the platform wildcard", subjects)
	}
}
