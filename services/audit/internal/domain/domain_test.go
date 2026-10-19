package domain

import "testing"

// Everything an audit entry is filtered by — the action and the entity — is
// derived at ingest and never recomputed. A row that anchored to the wrong id is
// a row that is invisible to the search that would have found it, and nothing
// about the trail says so: it just looks like the event never happened. These
// tests pin the derivation rather than the SQL, because the SQL can only be as
// right as the values it is given.

func TestActionOfStripsThePlatformNamespace(t *testing.T) {
	tests := []struct {
		name    string
		subject string
		want    string
	}{
		{
			name:    "an ordinary platform subject",
			subject: "karmarat.application.stage_changed",
			want:    "application.stage_changed",
		},
		{
			name:    "surrounding whitespace",
			subject: "  karmarat.job.published  ",
			want:    "job.published",
		},
		{
			name:    "an already-short action is left alone, so a caller may filter either way round",
			subject: "application.stage_changed",
			want:    "application.stage_changed",
		},
		{
			// A subject from outside the namespace is kept whole: shortening it
			// by guesswork would hide where it came from, which is the opposite
			// of what a trail is for.
			name:    "a subject from another namespace",
			subject: "vendor.thing.happened",
			want:    "vendor.thing.happened",
		},
		{
			name:    "the prefix appears only once, at the front",
			subject: "karmarat.karmarat.odd",
			want:    "karmarat.odd",
		},
		{
			name:    "an empty subject",
			subject: "",
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ActionOf(tt.subject); got != tt.want {
				t.Fatalf("ActionOf(%q) = %q, want %q", tt.subject, got, tt.want)
			}
		})
	}
}

func TestDomainOfReadsTheMiddleSegment(t *testing.T) {
	tests := []struct {
		subject string
		want    string
	}{
		{"karmarat.application.stage_changed", "application"},
		{"karmarat.message.conversation_opened", "message"},
		{"karmarat.company.registered", "company"},
		{"no-dots-at-all", ""},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.subject, func(t *testing.T) {
			if got := DomainOf(tt.subject); got != tt.want {
				t.Fatalf("DomainOf(%q) = %q, want %q", tt.subject, got, tt.want)
			}
		})
	}
}

// The case the whole derivation exists for: an application event names three
// records, and only one of them is what happened.
func TestEntityOfPrefersTheSubjectsOwnRecord(t *testing.T) {
	payload := map[string]any{
		"applicationId": "app_01",
		"jobId":         "job_01",
		"candidateId":   "cnd_01",
		"companyId":     "11111111-1111-1111-1111-111111111111",
	}

	entityType, entityID := EntityOf("karmarat.application.stage_changed", payload)

	if entityType != "application" || entityID != "app_01" {
		t.Fatalf("EntityOf = (%q, %q), want (application, app_01)", entityType, entityID)
	}
}

func TestEntityOfAcrossSubjects(t *testing.T) {
	tests := []struct {
		name     string
		subject  string
		payload  map[string]any
		wantType string
		wantID   string
	}{
		{
			name:     "a job event anchors to the job even though a company id is present",
			subject:  "karmarat.job.published",
			payload:  map[string]any{"jobId": "job_9", "companyId": "c-1"},
			wantType: "job",
			wantID:   "job_9",
		},
		{
			name:     "an offer event beats the application it belongs to",
			subject:  "karmarat.offer.accepted",
			payload:  map[string]any{"offerId": "ofr_2", "applicationId": "app_2"},
			wantType: "offer",
			wantID:   "ofr_2",
		},
		{
			name:     "a user event anchors to the account, not the company",
			subject:  "karmarat.user.deactivated",
			payload:  map[string]any{"accountId": "acc_7", "companyId": "c-1"},
			wantType: "account",
			wantID:   "acc_7",
		},
		{
			name:     "a company event anchors to the company",
			subject:  "karmarat.company.suspended",
			payload:  map[string]any{"companyId": "c-1"},
			wantType: "company",
			wantID:   "c-1",
		},
		{
			// The fallback: the subject's domain has no mapped key, so the
			// narrowest id in the payload wins.
			name:     "a message event falls back to the narrowest id it carries",
			subject:  "karmarat.message.sent",
			payload:  map[string]any{"applicationId": "app_3", "companyId": "c-1"},
			wantType: "application",
			wantID:   "app_3",
		},
		{
			name:     "the fallback prefers an application over the job and the candidate",
			subject:  "karmarat.notification.requested",
			payload:  map[string]any{"jobId": "job_1", "applicationId": "app_1", "candidateId": "cnd_1"},
			wantType: "application",
			wantID:   "app_1",
		},
		{
			name:     "the subject's key missing from the payload falls back rather than giving up",
			subject:  "karmarat.application.submitted",
			payload:  map[string]any{"jobId": "job_4"},
			wantType: "job",
			wantID:   "job_4",
		},
		{
			// Publishers are a mix of Go and NestJS; the snake_case spelling
			// reaches the bus and has to resolve to the same entity.
			name:     "a snake_case id from another runtime still resolves",
			subject:  "karmarat.interview.scheduled",
			payload:  map[string]any{"interview_id": "int_5"},
			wantType: "interview",
			wantID:   "int_5",
		},
		{
			name:     "an event with no ids at all records no entity",
			subject:  "karmarat.plan.published",
			payload:  map[string]any{"planCode": "growth"},
			wantType: "",
			wantID:   "",
		},
		{
			// A blank id is not an id. Storing "" as the entity would make an
			// unrelated row match `?entityId=` filters that mean "no filter".
			name:     "a blank id is ignored in favour of a real one",
			subject:  "karmarat.application.rejected",
			payload:  map[string]any{"applicationId": "   ", "jobId": "job_6"},
			wantType: "job",
			wantID:   "job_6",
		},
		{
			// A numeric id is not a string; taking it would store "%!s(float64=7)".
			name:     "a non-string id is skipped",
			subject:  "karmarat.job.closed",
			payload:  map[string]any{"jobId": float64(7), "candidateId": "cnd_8"},
			wantType: "candidate",
			wantID:   "cnd_8",
		},
		{
			name:     "a nil payload is not a panic",
			subject:  "karmarat.session.revoked",
			payload:  nil,
			wantType: "",
			wantID:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entityType, entityID := EntityOf(tt.subject, tt.payload)
			if entityType != tt.wantType || entityID != tt.wantID {
				t.Fatalf("EntityOf(%q) = (%q, %q), want (%q, %q)",
					tt.subject, entityType, entityID, tt.wantType, tt.wantID)
			}
		})
	}
}
