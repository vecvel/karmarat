package events_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	platformevents "github.com/karmarat/platform/packages/goshared/events"
	"github.com/karmarat/platform/packages/goshared/postgres"
	"github.com/karmarat/platform/packages/goshared/tenancy"
	"github.com/karmarat/platform/services/notifications/internal/domain"
	notificationevents "github.com/karmarat/platform/services/notifications/internal/events"
	"github.com/karmarat/platform/services/notifications/internal/realtime"
	"github.com/karmarat/platform/services/notifications/internal/store"
	"github.com/karmarat/platform/services/notifications/migrations"
)

const companyA = "11111111-1111-1111-1111-111111111111"

func newConsumer(t *testing.T) (*notificationevents.Consumer, *store.Store, context.Context) {
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
	// A nil Redis client: the live fan-out is not what these tests are about,
	// and the durable row is written either way.
	return notificationevents.NewConsumer(st, realtime.New(nil, logger), logger), st, ctx
}

func envelope(t *testing.T, id, subject, companyID string, payload map[string]any) platformevents.Envelope {
	t.Helper()
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return platformevents.Envelope{
		ID:         id,
		Subject:    subject,
		OccurredAt: time.Now().UTC(),
		CompanyID:  companyID,
		Payload:    encoded,
	}
}

func inboxOf(t *testing.T, ctx context.Context, st *store.Store, recipient domain.Recipient) []domain.Notification {
	t.Helper()
	page, err := domain.NewPage(100, "")
	if err != nil {
		t.Fatal(err)
	}
	listing, err := st.List(ctx, recipient, page)
	if err != nil {
		t.Fatalf("could not read the inbox: %v", err)
	}
	return listing.Notifications
}

func seedRecruiter(t *testing.T, ctx context.Context, st *store.Store, account string, permissions ...string) {
	t.Helper()
	err := st.TouchRecipient(ctx, tenancy.Principal{
		Type:        tenancy.PrincipalCompany,
		Subject:     account,
		CompanyID:   companyA,
		Email:       account + "@example.com",
		Permissions: permissions,
	})
	if err != nil {
		t.Fatalf("could not seed a recruiter: %v", err)
	}
}

// An application notifies the people at the company who can act on it, and
// nobody else — not the colleague without the permission, and not the applicant.
func TestSubmittedApplicationNotifiesTheCompany(t *testing.T) {
	consumer, st, ctx := newConsumer(t)

	seedRecruiter(t, ctx, st, "acc_recruiter", "applications.read")
	seedRecruiter(t, ctx, st, "acc_coordinator", "jobs.read")

	err := consumer.Handle(ctx, envelope(t, "evt_1",
		platformevents.SubjectApplicationSubmitted, companyA, map[string]any{
			"applicationId":  "app_1",
			"companyId":      companyA,
			"jobId":          "job_1",
			"jobTitle":       "Staff Engineer",
			"candidateId":    "acc_candidate",
			"candidateName":  "Ada Lovelace",
			"candidateEmail": "ada@example.com",
		}))
	if err != nil {
		t.Fatalf("handling failed: %v", err)
	}

	recruiter := domain.Recipient{
		PrincipalType: tenancy.PrincipalCompany, AccountID: "acc_recruiter", CompanyID: companyA,
	}
	if got := inboxOf(t, ctx, st, recruiter); len(got) != 1 {
		t.Fatalf("the recruiter received %d notifications, want 1", len(got))
	}

	coordinator := domain.Recipient{
		PrincipalType: tenancy.PrincipalCompany, AccountID: "acc_coordinator", CompanyID: companyA,
	}
	if got := inboxOf(t, ctx, st, coordinator); len(got) != 0 {
		t.Errorf("a colleague without applications.read received %d notifications", len(got))
	}

	candidate := domain.Recipient{PrincipalType: tenancy.PrincipalCandidate, AccountID: "acc_candidate"}
	if got := inboxOf(t, ctx, st, candidate); len(got) != 0 {
		t.Errorf("the applicant was told about their own application %d times", len(got))
	}
}

// A stage change goes the other way: to the candidate, whose account id the
// event carries, without anybody at the company being told twice.
func TestStageChangeNotifiesTheCandidate(t *testing.T) {
	consumer, st, ctx := newConsumer(t)

	seedRecruiter(t, ctx, st, "acc_recruiter", "applications.read")

	err := consumer.Handle(ctx, envelope(t, "evt_1",
		platformevents.SubjectApplicationStageChanged, companyA, map[string]any{
			"applicationId":  "app_1",
			"companyId":      companyA,
			"jobTitle":       "Staff Engineer",
			"stageName":      "Interview",
			"candidateId":    "acc_candidate",
			"candidateName":  "Ada Lovelace",
			"candidateEmail": "ada@example.com",
		}))
	if err != nil {
		t.Fatalf("handling failed: %v", err)
	}

	candidate := domain.Recipient{PrincipalType: tenancy.PrincipalCandidate, AccountID: "acc_candidate"}
	inbox := inboxOf(t, ctx, st, candidate)
	if len(inbox) != 1 {
		t.Fatalf("the candidate received %d notifications, want 1", len(inbox))
	}
	if inbox[0].Type != domain.TypeApplicationStageChanged {
		t.Errorf("type = %q", inbox[0].Type)
	}
	if inbox[0].CompanyID != companyA {
		t.Errorf("the company context was lost: %q", inbox[0].CompanyID)
	}

	recruiter := domain.Recipient{
		PrincipalType: tenancy.PrincipalCompany, AccountID: "acc_recruiter", CompanyID: companyA,
	}
	if got := inboxOf(t, ctx, st, recruiter); len(got) != 0 {
		t.Errorf("the company was told about a move it made: %d notifications", len(got))
	}

	// A stage change is worth an email by default, and the address came from
	// the event rather than from a sign-in this candidate may never have made.
	claimed, err := st.ClaimDueEmails(ctx, 10, 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 1 {
		t.Fatalf("queued %d emails, want 1", len(claimed))
	}
	if claimed[0].ToAddress != "ada@example.com" {
		t.Errorf("addressed to %q", claimed[0].ToAddress)
	}
}

// The same event delivered twice must notify once. This is the everyday case:
// JetStream redelivers on any error and several replicas consume at once.
func TestRedeliveryIsIdempotent(t *testing.T) {
	consumer, st, ctx := newConsumer(t)

	seedRecruiter(t, ctx, st, "acc_recruiter", "applications.read")

	event := envelope(t, "evt_same", platformevents.SubjectApplicationSubmitted, companyA,
		map[string]any{
			"applicationId": "app_1",
			"companyId":     companyA,
			"jobTitle":      "Staff Engineer",
			"candidateId":   "acc_candidate",
		})

	for range 3 {
		if err := consumer.Handle(ctx, event); err != nil {
			t.Fatalf("handling failed: %v", err)
		}
	}

	recruiter := domain.Recipient{
		PrincipalType: tenancy.PrincipalCompany, AccountID: "acc_recruiter", CompanyID: companyA,
	}
	if got := inboxOf(t, ctx, st, recruiter); len(got) != 1 {
		t.Fatalf("three deliveries produced %d notifications, want 1", len(got))
	}

	claimed, err := st.ClaimDueEmails(ctx, 10, 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 0 {
		t.Errorf("a new application queued %d emails; it should be in-app only by default", len(claimed))
	}
}

// Preferences are consulted before anything is written, so an opt-out means no
// row at all rather than a row that is hidden.
func TestPreferencesAreRespectedBeforeWriting(t *testing.T) {
	consumer, st, ctx := newConsumer(t)

	candidate := domain.Recipient{PrincipalType: tenancy.PrincipalCandidate, AccountID: "acc_candidate"}

	prefs := domain.NewPreferences()
	prefs.Channels[domain.TypeApplicationRejected] = domain.ChannelSet{InApp: false, Email: false}
	if err := st.SavePreferences(ctx, candidate, prefs); err != nil {
		t.Fatal(err)
	}

	err := consumer.Handle(ctx, envelope(t, "evt_1",
		platformevents.SubjectApplicationRejected, companyA, map[string]any{
			"applicationId":  "app_1",
			"companyId":      companyA,
			"jobTitle":       "Staff Engineer",
			"candidateId":    "acc_candidate",
			"candidateEmail": "ada@example.com",
		}))
	if err != nil {
		t.Fatalf("handling failed: %v", err)
	}

	if got := inboxOf(t, ctx, st, candidate); len(got) != 0 {
		t.Errorf("an opted-out candidate received %d notifications", len(got))
	}
	claimed, err := st.ClaimDueEmails(ctx, 10, 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 0 {
		t.Errorf("an opted-out candidate was queued %d emails", len(claimed))
	}
}

// The two channels are independent: somebody may want the mail and not the
// bell, and turning one off must not turn the other off with it.
func TestChannelsAreIndependent(t *testing.T) {
	consumer, st, ctx := newConsumer(t)

	candidate := domain.Recipient{PrincipalType: tenancy.PrincipalCandidate, AccountID: "acc_candidate"}

	prefs := domain.NewPreferences()
	prefs.Channels[domain.TypeApplicationRejected] = domain.ChannelSet{InApp: false, Email: true}
	if err := st.SavePreferences(ctx, candidate, prefs); err != nil {
		t.Fatal(err)
	}

	err := consumer.Handle(ctx, envelope(t, "evt_1",
		platformevents.SubjectApplicationRejected, companyA, map[string]any{
			"applicationId":  "app_1",
			"companyId":      companyA,
			"jobTitle":       "Staff Engineer",
			"candidateId":    "acc_candidate",
			"candidateEmail": "ada@example.com",
		}))
	if err != nil {
		t.Fatalf("handling failed: %v", err)
	}

	if got := inboxOf(t, ctx, st, candidate); len(got) != 0 {
		t.Errorf("in-app was off but %d notifications were written", len(got))
	}
	claimed, err := st.ClaimDueEmails(ctx, 10, 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 1 {
		t.Fatalf("email was on but %d were queued", len(claimed))
	}
}

// A malformed or unroutable event is acked, not nak'd: redelivering it cannot
// supply an identifier it never carried, and nak'ing would block the subject.
func TestUnroutableEventsAreAcked(t *testing.T) {
	consumer, _, ctx := newConsumer(t)

	tests := []struct {
		name  string
		event platformevents.Envelope
	}{
		{
			name: "a subject we do not notify on",
			event: envelope(t, "evt_1", platformevents.SubjectCompanySuspended, companyA,
				map[string]any{"companyId": companyA}),
		},
		{
			name: "an event missing its identifiers",
			event: envelope(t, "evt_2", platformevents.SubjectApplicationSubmitted, "",
				map[string]any{}),
		},
		{
			name: "a payload that is not an object",
			event: platformevents.Envelope{
				ID:      "evt_3",
				Subject: platformevents.SubjectApplicationSubmitted,
				Payload: json.RawMessage(`"not an object"`),
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := consumer.Handle(ctx, tc.event); err != nil {
				t.Fatalf("an unroutable event was nak'd: %v", err)
			}
		})
	}
}

// One event, two audiences, two inboxes — and the wording differs, because a
// hire is not the same news to the person hired and to the team that hired them.
func TestHireNotifiesBothSidesDifferently(t *testing.T) {
	consumer, st, ctx := newConsumer(t)

	seedRecruiter(t, ctx, st, "acc_recruiter", "applications.read")

	err := consumer.Handle(ctx, envelope(t, "evt_1",
		platformevents.SubjectApplicationHired, companyA, map[string]any{
			"applicationId": "app_1",
			"companyId":     companyA,
			"jobTitle":      "Staff Engineer",
			"candidateId":   "acc_candidate",
			"candidateName": "Ada Lovelace",
		}))
	if err != nil {
		t.Fatalf("handling failed: %v", err)
	}

	candidateInbox := inboxOf(t, ctx, st,
		domain.Recipient{PrincipalType: tenancy.PrincipalCandidate, AccountID: "acc_candidate"})
	companyInbox := inboxOf(t, ctx, st, domain.Recipient{
		PrincipalType: tenancy.PrincipalCompany, AccountID: "acc_recruiter", CompanyID: companyA,
	})

	if len(candidateInbox) != 1 || len(companyInbox) != 1 {
		t.Fatalf("candidate got %d, company got %d; want 1 each",
			len(candidateInbox), len(companyInbox))
	}
	if candidateInbox[0].Title == companyInbox[0].Title {
		t.Errorf("both sides were sent the same title %q", candidateInbox[0].Title)
	}
}
