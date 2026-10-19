package events

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"

	platformevents "github.com/karmarat/platform/packages/goshared/events"
)

// An event this service cannot act on must be retired, not nak'd: a redelivery
// of the same malformed payload fails the same way, and a message that can never
// succeed accumulates in the stream until it poisons the consumer.
//
// The consumer under test has no store behind it. Every case here must return
// before it reaches one, so a case that regressed into touching the database
// would panic rather than quietly pass.
func TestHandleRetiresEventsItCannotUse(t *testing.T) {
	consumer := NewConsumer(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	tests := []struct {
		name    string
		subject string
		payload map[string]any
	}{
		{
			name:    "an approach with no company",
			subject: platformevents.SubjectCandidateApproached,
			payload: map[string]any{"approachId": "apr_1", "accountId": "acct_1"},
		},
		{
			name:    "an approach with no candidate account",
			subject: platformevents.SubjectCandidateApproached,
			payload: map[string]any{"approachId": "apr_1", "companyId": "c1"},
		},
		{
			// Without the approach id there is no idempotency key, so a
			// redelivery could not be told apart from a second approach.
			name:    "an approach with no approach id",
			subject: platformevents.SubjectCandidateApproached,
			payload: map[string]any{"companyId": "c1", "accountId": "acct_1"},
		},
		{
			name:    "an application with no identifiers",
			subject: platformevents.SubjectApplicationSubmitted,
			payload: map[string]any{"jobTitle": "Engineer"},
		},
		{
			name:    "a visibility change with no account",
			subject: platformevents.SubjectCandidateVisibilityChanged,
			payload: map[string]any{"discoverable": true},
		},
		{
			name:    "a profile update with no account",
			subject: platformevents.SubjectCandidateProfileUpdated,
			payload: map[string]any{"discoverable": false},
		},
		{
			// The stream is subscribed with a filter, but a subject this
			// consumer does not know must still be a no-op rather than a nak.
			name:    "a subject this service does not consume",
			subject: platformevents.SubjectOfferAccepted,
			payload: map[string]any{"offerId": "off_1"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := consumer.Handle(context.Background(), envelopeFor(tc.subject, tc.payload)); err != nil {
				t.Fatalf("Handle() error = %v, want nil so the event is retired", err)
			}
		})
	}
}

func TestSubjectsAreTheOnesThisServiceReactsTo(t *testing.T) {
	want := map[string]bool{
		// Opens the thread a sourced candidate replies in.
		platformevents.SubjectCandidateApproached: true,
		// Keeps the "already applied" branch of the eligibility check local.
		platformevents.SubjectApplicationSubmitted: true,
		// Both halves of who a candidate will hear from.
		platformevents.SubjectCandidateVisibilityChanged: true,
		platformevents.SubjectCandidateProfileUpdated:    true,
	}

	got := Subjects()
	if len(got) != len(want) {
		t.Fatalf("Subjects() = %v, want %d subjects", got, len(want))
	}
	for _, subject := range got {
		if !want[subject] {
			t.Errorf("Subjects() included %q, which this consumer does not handle", subject)
		}
	}
}

func TestFirstNonEmpty(t *testing.T) {
	// A tenant-scoped event carries its company in the payload and again on the
	// envelope; either may be the one that is populated.
	tests := []struct {
		name   string
		values []string
		want   string
	}{
		{name: "the payload wins when it has one", values: []string{"payload", "envelope"}, want: "payload"},
		{name: "the envelope is the fallback", values: []string{"", "envelope"}, want: "envelope"},
		{name: "whitespace does not count", values: []string{"   ", "envelope"}, want: "envelope"},
		{name: "nothing anywhere", values: []string{"", "  "}, want: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := firstNonEmpty(tc.values...); got != tc.want {
				t.Errorf("firstNonEmpty(%q) = %q, want %q", tc.values, got, tc.want)
			}
		})
	}
}

func envelopeFor(subject string, payload map[string]any) platformevents.Envelope {
	body, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	return platformevents.Envelope{ID: "evt_1", Subject: subject, Payload: body}
}
