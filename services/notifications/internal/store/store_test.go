package store_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"testing"

	"github.com/karmarat/platform/packages/goshared/postgres"
	"github.com/karmarat/platform/packages/goshared/tenancy"
	"github.com/karmarat/platform/services/notifications/internal/domain"
	"github.com/karmarat/platform/services/notifications/internal/store"
	"github.com/karmarat/platform/services/notifications/migrations"
)

const (
	companyA = "11111111-1111-1111-1111-111111111111"
	companyB = "22222222-2222-2222-2222-222222222222"
)

// newStore connects to the test database, or skips.
//
// `go test ./...` has to pass on a laptop with no Postgres running, so the
// database-backed tests opt in through TEST_DATABASE_URL rather than failing
// the suite for everyone who has not started the stack.
func newStore(t *testing.T) (*store.Store, context.Context) {
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

	return store.New(pool), ctx
}

func companyRecipient(account, company string) domain.Recipient {
	return domain.Recipient{
		PrincipalType: tenancy.PrincipalCompany,
		AccountID:     account,
		CompanyID:     company,
	}
}

func candidateRecipient(account string) domain.Recipient {
	return domain.Recipient{PrincipalType: tenancy.PrincipalCandidate, AccountID: account}
}

func mustCreate(t *testing.T, ctx context.Context, st *store.Store, in store.CreateInput) domain.Notification {
	t.Helper()
	created, isNew, err := st.Create(ctx, in)
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if !isNew {
		t.Fatal("create reported a duplicate for a notification that should be new")
	}
	return created
}

// The tenant filter, proved rather than asserted: two companies, one signed in,
// the other's rows invisible — including to an account id that exists in both.
func TestInboxIsScopedToTheTenant(t *testing.T) {
	st, ctx := newStore(t)

	// The same person recruits for both companies. Their account id is
	// identical; only the tenant differs.
	const shared = "acc_recruiter"

	mustCreate(t, ctx, st, store.CreateInput{
		Recipient: companyRecipient(shared, companyA),
		Type:      domain.TypeApplicationSubmitted,
		Title:     "A's application",
		EventID:   "evt_a",
	})
	mustCreate(t, ctx, st, store.CreateInput{
		Recipient: companyRecipient(shared, companyB),
		Type:      domain.TypeApplicationSubmitted,
		Title:     "B's application",
		EventID:   "evt_b",
	})

	page, err := domain.NewPage(25, "")
	if err != nil {
		t.Fatal(err)
	}

	listing, err := st.List(ctx, companyRecipient(shared, companyA), page)
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(listing.Notifications) != 1 {
		t.Fatalf("company A saw %d notifications, want 1", len(listing.Notifications))
	}
	if listing.Notifications[0].Title != "A's application" {
		t.Errorf("company A saw %q", listing.Notifications[0].Title)
	}
	if listing.UnreadCount != 1 {
		t.Errorf("unread count = %d, want 1", listing.UnreadCount)
	}
}

// A notification addressed to one person must not be readable — or markable —
// by another, and the refusal must not confirm that the id exists.
func TestAnotherRecipientCannotReadOrMark(t *testing.T) {
	st, ctx := newStore(t)

	theirs := mustCreate(t, ctx, st, store.CreateInput{
		Recipient: companyRecipient("acc_owner", companyA),
		Type:      domain.TypeApplicationSubmitted,
		Title:     "Not yours",
		EventID:   "evt_1",
	})

	intruders := map[string]domain.Recipient{
		"another company":    companyRecipient("acc_owner", companyB),
		"another account":    companyRecipient("acc_intruder", companyA),
		"a candidate":        candidateRecipient("acc_owner"),
		"the platform staff": {PrincipalType: tenancy.PrincipalPlatform, AccountID: "acc_owner"},
	}

	for name, intruder := range intruders {
		t.Run(name, func(t *testing.T) {
			err := st.MarkRead(ctx, intruder, theirs.ID)
			if !errors.Is(err, domain.ErrNotificationNotFound) {
				t.Fatalf("MarkRead error = %v, want ErrNotificationNotFound", err)
			}

			page, _ := domain.NewPage(25, "")
			listing, err := st.List(ctx, intruder, page)
			if err != nil {
				t.Fatalf("list failed: %v", err)
			}
			if len(listing.Notifications) != 0 {
				t.Errorf("%s saw %d of another recipient's notifications", name, len(listing.Notifications))
			}
		})
	}
}

// A candidate and a company user holding the same account id are different
// people as far as this service is concerned, and their inboxes must not merge.
func TestCandidateAndCompanyInboxesDoNotCross(t *testing.T) {
	st, ctx := newStore(t)

	const shared = "acc_same_id"

	// The candidate's notification carries a company as *context*, which is the
	// case most likely to leak if the filter were written on company_id alone.
	mustCreate(t, ctx, st, store.CreateInput{
		Recipient: candidateRecipient(shared),
		CompanyID: companyA,
		Type:      domain.TypeApplicationRejected,
		Title:     "Candidate's",
		EventID:   "evt_candidate",
	})
	mustCreate(t, ctx, st, store.CreateInput{
		Recipient: companyRecipient(shared, companyA),
		Type:      domain.TypeApplicationSubmitted,
		Title:     "Company's",
		EventID:   "evt_company",
	})

	page, _ := domain.NewPage(25, "")

	candidateListing, err := st.List(ctx, candidateRecipient(shared), page)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidateListing.Notifications) != 1 ||
		candidateListing.Notifications[0].Title != "Candidate's" {
		t.Fatalf("the candidate saw %d rows: %+v", len(candidateListing.Notifications),
			candidateListing.Notifications)
	}

	companyListing, err := st.List(ctx, companyRecipient(shared, companyA), page)
	if err != nil {
		t.Fatal(err)
	}
	if len(companyListing.Notifications) != 1 ||
		companyListing.Notifications[0].Title != "Company's" {
		t.Fatalf("the company user saw %d rows: %+v", len(companyListing.Notifications),
			companyListing.Notifications)
	}
}

// A redelivered event must not notify twice. JetStream redelivers on any error
// and several replicas consume at once, so this is the everyday case.
func TestRedeliveryDoesNotNotifyTwice(t *testing.T) {
	st, ctx := newStore(t)

	recipient := candidateRecipient("acc_candidate")
	in := store.CreateInput{
		Recipient: recipient,
		CompanyID: companyA,
		Type:      domain.TypeApplicationRejected,
		Title:     "An update on your application",
		EventID:   "evt_redelivered",
	}

	mustCreate(t, ctx, st, in)

	_, isNew, err := st.Create(ctx, in)
	if err != nil {
		t.Fatalf("the redelivery failed instead of being ignored: %v", err)
	}
	if isNew {
		t.Fatal("a redelivered event created a second notification")
	}

	// A different recipient is not a duplicate: one event legitimately notifies
	// several people.
	other := in
	other.Recipient = candidateRecipient("acc_other")
	mustCreate(t, ctx, st, other)
}

func TestMarkReadAndReadAll(t *testing.T) {
	st, ctx := newStore(t)

	recipient := companyRecipient("acc_1", companyA)
	for i, id := range []string{"evt_1", "evt_2", "evt_3"} {
		mustCreate(t, ctx, st, store.CreateInput{
			Recipient: recipient,
			Type:      domain.TypeApplicationSubmitted,
			Title:     "Application " + string(rune('a'+i)),
			EventID:   id,
		})
	}

	page, _ := domain.NewPage(25, "")
	listing, err := st.List(ctx, recipient, page)
	if err != nil {
		t.Fatal(err)
	}
	if listing.UnreadCount != 3 {
		t.Fatalf("unread = %d, want 3", listing.UnreadCount)
	}

	first := listing.Notifications[0].ID
	if err := st.MarkRead(ctx, recipient, first); err != nil {
		t.Fatalf("mark read failed: %v", err)
	}
	// Marking twice succeeds: a retried tap on a flaky connection is not an
	// error the client should have to handle.
	if err := st.MarkRead(ctx, recipient, first); err != nil {
		t.Fatalf("marking an already-read notification failed: %v", err)
	}

	count, err := st.UnreadCount(ctx, recipient)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("unread = %d after one read, want 2", count)
	}

	cleared, err := st.MarkAllRead(ctx, recipient)
	if err != nil {
		t.Fatal(err)
	}
	if cleared != 2 {
		t.Errorf("MarkAllRead cleared %d, want 2", cleared)
	}
	if count, _ := st.UnreadCount(ctx, recipient); count != 0 {
		t.Errorf("unread = %d after read-all, want 0", count)
	}
}

func TestListPaginates(t *testing.T) {
	st, ctx := newStore(t)

	recipient := candidateRecipient("acc_candidate")
	for i := range 5 {
		mustCreate(t, ctx, st, store.CreateInput{
			Recipient: recipient,
			Type:      domain.TypeMessageReceived,
			Title:     "Message",
			EventID:   "evt_" + string(rune('a'+i)),
		})
	}

	page, _ := domain.NewPage(2, "")
	seen := map[string]bool{}

	for range 5 {
		listing, err := st.List(ctx, recipient, page)
		if err != nil {
			t.Fatal(err)
		}
		if len(listing.Notifications) > 2 {
			t.Fatalf("a page of 2 returned %d rows", len(listing.Notifications))
		}
		for _, notification := range listing.Notifications {
			if seen[notification.ID] {
				t.Fatalf("%s was returned on two pages", notification.ID)
			}
			seen[notification.ID] = true
		}
		if listing.NextCursor == "" {
			break
		}
		page, err = domain.NewPage(2, listing.NextCursor)
		if err != nil {
			t.Fatalf("a cursor we produced was rejected: %v", err)
		}
	}

	if len(seen) != 5 {
		t.Errorf("paging returned %d of 5 notifications", len(seen))
	}
}

func TestPreferencesRoundTripPerTenant(t *testing.T) {
	st, ctx := newStore(t)

	const shared = "acc_recruiter"
	atA := companyRecipient(shared, companyA)
	atB := companyRecipient(shared, companyB)

	prefs := domain.NewPreferences()
	prefs.Channels[domain.TypeApplicationSubmitted] = domain.ChannelSet{InApp: true, Email: true}
	if err := st.SavePreferences(ctx, atA, prefs); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	loaded, err := st.LoadPreferences(ctx, atA)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Allows(domain.TypeApplicationSubmitted, domain.ChannelEmail) {
		t.Error("the saved preference did not come back")
	}

	// The same person at the other company is unaffected: the default for a new
	// application is in-app only.
	atOther, err := st.LoadPreferences(ctx, atB)
	if err != nil {
		t.Fatal(err)
	}
	if atOther.Allows(domain.TypeApplicationSubmitted, domain.ChannelEmail) {
		t.Error("a preference set at one company leaked into another")
	}
}

func TestLoadPreferencesForAudience(t *testing.T) {
	st, ctx := newStore(t)

	loud := companyRecipient("acc_loud", companyA)
	quiet := companyRecipient("acc_quiet", companyA)

	prefs := domain.NewPreferences()
	prefs.Channels[domain.TypeApplicationSubmitted] = domain.ChannelSet{InApp: false, Email: false}
	if err := st.SavePreferences(ctx, quiet, prefs); err != nil {
		t.Fatal(err)
	}

	loaded, err := st.LoadPreferencesFor(ctx, []domain.Recipient{loud, quiet})
	if err != nil {
		t.Fatalf("batch load failed: %v", err)
	}

	if _, present := loaded[store.PreferenceKey(loud)]; present {
		t.Error("a recipient who has set nothing came back with stored preferences")
	}
	stored, present := loaded[store.PreferenceKey(quiet)]
	if !present {
		t.Fatal("the recipient who opted out was missing from the batch")
	}
	if stored.Allows(domain.TypeApplicationSubmitted, domain.ChannelInApp) {
		t.Error("the opt-out did not come back")
	}
}

// Fan-out reaches the people who can act on the event and nobody else — not the
// colleague without the permission, and not the same permission in another
// tenant.
func TestExpandAudienceRespectsPermissionAndTenant(t *testing.T) {
	st, ctx := newStore(t)

	seed := func(account, company string, permissions ...string) {
		t.Helper()
		err := st.TouchRecipient(ctx, tenancy.Principal{
			Type:        tenancy.PrincipalCompany,
			Subject:     account,
			CompanyID:   company,
			Email:       account + "@example.com",
			Permissions: permissions,
		})
		if err != nil {
			t.Fatalf("could not seed a recipient: %v", err)
		}
	}

	seed("acc_recruiter", companyA, "applications.read", "jobs.read")
	seed("acc_coordinator", companyA, "jobs.read")
	seed("acc_elsewhere", companyB, "applications.read")

	recipients, err := st.ExpandAudience(ctx, domain.Audience{
		PrincipalType: tenancy.PrincipalCompany,
		CompanyID:     companyA,
		Permission:    "applications.read",
	})
	if err != nil {
		t.Fatalf("expand failed: %v", err)
	}

	if len(recipients) != 1 {
		t.Fatalf("expanded to %d recipients, want 1: %+v", len(recipients), recipients)
	}
	if recipients[0].AccountID != "acc_recruiter" {
		t.Errorf("expanded to %q", recipients[0].AccountID)
	}
	if recipients[0].Email != "acc_recruiter@example.com" {
		t.Errorf("the address was not carried: %q", recipients[0].Email)
	}
	if recipients[0].CompanyID != companyA {
		t.Errorf("the tenant was not carried: %q", recipients[0].CompanyID)
	}
}

func TestEmailQueueDeduplicates(t *testing.T) {
	st, ctx := newStore(t)

	in := store.EmailInput{
		DedupeKey: "evt_1|candidate::acc_1",
		Recipient: domain.Recipient{
			PrincipalType: tenancy.PrincipalCandidate,
			AccountID:     "acc_1",
			Email:         "ada@example.com",
		},
		Subject:  "An update on your application",
		Template: "notification",
		Data:     map[string]any{"title": "An update", "link": "/applications/app_1"},
	}

	queued, err := st.QueueEmail(ctx, in)
	if err != nil {
		t.Fatalf("queue failed: %v", err)
	}
	if !queued {
		t.Fatal("the first queue attempt reported a duplicate")
	}

	again, err := st.QueueEmail(ctx, in)
	if err != nil {
		t.Fatalf("the duplicate failed instead of being ignored: %v", err)
	}
	if again {
		t.Fatal("a redelivered event queued a second email")
	}
}

// The claim leases the row and schedules its retry in one statement, so a
// second worker starting a moment later finds nothing due. That is what lets
// every replica run the drain loop without two of them mailing one candidate.
func TestClaimLeasesTheMessage(t *testing.T) {
	st, ctx := newStore(t)

	if _, err := st.QueueEmail(ctx, store.EmailInput{
		DedupeKey: "evt_lease",
		Recipient: domain.Recipient{
			PrincipalType: tenancy.PrincipalCandidate,
			AccountID:     "acc_1",
			Email:         "ada@example.com",
		},
		Subject: "Leased",
	}); err != nil {
		t.Fatal(err)
	}

	first, err := st.ClaimDueEmails(ctx, 10, 30)
	if err != nil {
		t.Fatalf("claim failed: %v", err)
	}
	if len(first) != 1 {
		t.Fatalf("claimed %d messages, want 1", len(first))
	}
	if first[0].Attempts != 1 {
		t.Errorf("attempts = %d after the first claim, want 1", first[0].Attempts)
	}

	concurrent, err := st.ClaimDueEmails(ctx, 10, 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(concurrent) != 0 {
		t.Errorf("a second worker claimed %d messages that were already leased", len(concurrent))
	}
}

// A message that keeps failing is retried until its attempts run out and is
// then dead-lettered — kept, not deleted, because a queue that silently drops
// undeliverable mail looks exactly like one that works.
//
// A backoff of zero is how the test says "retry immediately"; the worker passes
// its configured base instead.
func TestFailedEmailRetriesThenDeadLetters(t *testing.T) {
	st, ctx := newStore(t)

	if _, err := st.QueueEmail(ctx, store.EmailInput{
		DedupeKey: "evt_doomed",
		Recipient: domain.Recipient{
			PrincipalType: tenancy.PrincipalCandidate,
			AccountID:     "acc_1",
			Email:         "ada@example.com",
		},
		Subject:     "Doomed",
		MaxAttempts: 2,
	}); err != nil {
		t.Fatal(err)
	}

	first, err := st.ClaimDueEmails(ctx, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 {
		t.Fatalf("claimed %d, want 1", len(first))
	}
	if first[0].Exhausted() {
		t.Fatal("the first of two allowed attempts reported itself exhausted")
	}
	if err := st.MarkEmailFailed(ctx, first[0].ID, "relay refused", false); err != nil {
		t.Fatal(err)
	}

	second, err := st.ClaimDueEmails(ctx, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 1 {
		t.Fatalf("the failed message was not retried: claimed %d", len(second))
	}
	if !second[0].Exhausted() {
		t.Error("the last allowed attempt did not report itself exhausted")
	}
	if err := st.MarkEmailFailed(ctx, second[0].ID, "relay refused again", true); err != nil {
		t.Fatal(err)
	}

	dead, err := st.DeadLetterCount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if dead != 1 {
		t.Errorf("dead-lettered = %d, want 1", dead)
	}

	// A dead-lettered message is never claimed again: it is kept for diagnosis,
	// not for another attempt.
	after, err := st.ClaimDueEmails(ctx, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 0 {
		t.Error("a dead-lettered message was claimed again")
	}
}

func TestMarkEmailSentClearsTheQueue(t *testing.T) {
	st, ctx := newStore(t)

	if _, err := st.QueueEmail(ctx, store.EmailInput{
		DedupeKey: "evt_2|candidate::acc_2",
		Recipient: domain.Recipient{PrincipalType: tenancy.PrincipalCandidate, AccountID: "acc_2", Email: "b@example.com"},
		Subject:   "Hello",
	}); err != nil {
		t.Fatal(err)
	}

	claimed, err := st.ClaimDueEmails(ctx, 10, 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed %d, want 1", len(claimed))
	}
	if err := st.MarkEmailSent(ctx, claimed[0].ID); err != nil {
		t.Fatal(err)
	}

	again, err := st.ClaimDueEmails(ctx, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Error("a sent message was claimed again")
	}
}

func TestRememberRecipientKeepsTheFirstAddress(t *testing.T) {
	st, ctx := newStore(t)

	recipient := domain.Recipient{
		PrincipalType: tenancy.PrincipalCandidate,
		AccountID:     "acc_candidate",
		Email:         "ada@example.com",
		Name:          "Ada Lovelace",
	}
	if err := st.RememberRecipient(ctx, recipient); err != nil {
		t.Fatal(err)
	}

	// A later event carrying a stale snapshot must not overwrite what we hold:
	// the directory is refreshed from verified requests, not from payloads.
	stale := recipient
	stale.Email = "old@example.com"
	stale.Name = "Old Name"
	if err := st.RememberRecipient(ctx, stale); err != nil {
		t.Fatal(err)
	}

	found, err := st.FindRecipient(ctx, domain.Recipient{
		PrincipalType: tenancy.PrincipalCandidate,
		AccountID:     "acc_candidate",
	})
	if err != nil {
		t.Fatal(err)
	}
	if found.Email != "ada@example.com" {
		t.Errorf("address = %q, want the first one recorded", found.Email)
	}
}

// A verified request refreshes the permission list, because a role change has
// to reach fan-out.
func TestTouchRecipientRefreshesPermissions(t *testing.T) {
	st, ctx := newStore(t)

	principal := tenancy.Principal{
		Type:        tenancy.PrincipalCompany,
		Subject:     "acc_1",
		CompanyID:   companyA,
		Email:       "recruiter@example.com",
		Permissions: []string{"jobs.read"},
	}
	if err := st.TouchRecipient(ctx, principal); err != nil {
		t.Fatal(err)
	}

	principal.Permissions = []string{"jobs.read", "applications.read"}
	if err := st.TouchRecipient(ctx, principal); err != nil {
		t.Fatal(err)
	}

	recipients, err := st.ExpandAudience(ctx, domain.Audience{
		PrincipalType: tenancy.PrincipalCompany,
		CompanyID:     companyA,
		Permission:    "applications.read",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(recipients) != 1 {
		t.Fatalf("the newly granted permission did not reach fan-out: %d recipients", len(recipients))
	}
}
