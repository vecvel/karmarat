package store

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/karmarat/platform/packages/goshared/idgen"
	"github.com/karmarat/platform/packages/goshared/postgres"
	"github.com/karmarat/platform/services/offers/internal/domain"
	"github.com/karmarat/platform/services/offers/migrations"
)

// The tests below need a real Postgres: the rules they cover — the tenant
// predicate, the row lock that settles two concurrent sends, the unique index
// behind the Idempotency-Key — live in SQL and in constraints, and a fake would
// only prove that the fake agrees with itself.
//
// Point TEST_DATABASE_URL at a scratch database to run them:
//
//	TEST_DATABASE_URL=postgres://karmarat:karmarat@localhost:5432/offers_test?sslmode=disable \
//	  go test ./services/offers/...
//
// Without it they skip, so `go test ./...` passes on a machine with no database.
func testStore(t *testing.T) (*Store, context.Context) {
	t.Helper()

	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set; skipping the database-backed tests")
	}

	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("connect to the test database: %v", err)
	}
	t.Cleanup(pool.Close)

	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("ping the test database: %v", err)
	}

	files, err := postgres.LoadMigrations(migrations.FS, ".")
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	if err := postgres.Migrate(ctx, pool, files, logger); err != nil {
		t.Fatalf("migrate the test database: %v", err)
	}

	return New(pool), ctx
}

// Each test invents its own tenant so runs cannot see one another's rows and the
// database needs no truncation between them.
//
// company_id is a uuid column, and the uuid package is not a direct dependency
// of this module, so the value is assembled here rather than pulling a library
// in for sixteen random bytes.
func newCompany() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic("store: no entropy for a test tenant: " + err.Error())
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80

	return fmt.Sprintf("%x-%x-%x-%x-%x", raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:16])
}

func draftInput() OfferInput {
	return OfferInput{
		ApplicationID:  idgen.New("app"),
		CandidateID:    idgen.New("cnd"),
		CandidateName:  "Priya Raman",
		JobTitle:       "Staff Engineer",
		Designation:    "Staff Engineer",
		DepartmentName: "Platform",
		GradeLevel:     "L6",
		Compensation: domain.Compensation{
			Currency: "USD", BaseSalary: 17_500_000, SignOnBonus: 2_500_000,
			PayFrequency: "annual", AnnualBonus: "15% of base", EquityShares: "4,000 RSUs",
		},
		JoiningDate:  time.Date(2026, 4, 6, 0, 0, 0, 0, time.UTC),
		WorkLocation: "Bengaluru",
		CustomFields: []domain.CustomField{{Key: "Relocation", Value: "Covered"}},
	}
}

func draft(t *testing.T, st *Store, ctx context.Context, companyID, actorID string, in OfferInput) domain.Offer {
	t.Helper()

	offer, err := st.CreateOffer(ctx, companyID, actorID, in)
	if err != nil {
		t.Fatalf("CreateOffer() error = %v", err)
	}
	return offer
}

// approved walks an offer to the state the send endpoint expects, through the
// same calls a client would make. Seeding the status directly would test a state
// the service cannot actually reach.
func approved(t *testing.T, st *Store, ctx context.Context, companyID string) domain.Offer {
	t.Helper()

	offer := draft(t, st, ctx, companyID, "usr_author", draftInput())
	if _, err := st.SubmitOffer(ctx, companyID, offer.ID, "usr_author"); err != nil {
		t.Fatalf("SubmitOffer() error = %v", err)
	}
	signed, err := st.ApproveOffer(ctx, companyID, offer.ID, "usr_approver", false)
	if err != nil {
		t.Fatalf("ApproveOffer() error = %v", err)
	}
	return signed
}

func TestEveryFieldSurvivesTheRoundTrip(t *testing.T) {
	st, ctx := testStore(t)
	companyID := newCompany()

	in := draftInput()
	created := draft(t, st, ctx, companyID, "usr_author", in)

	read, err := st.FindOffer(ctx, companyID, created.ID)
	if err != nil {
		t.Fatalf("FindOffer() error = %v", err)
	}

	if read.BaseSalary != in.Compensation.BaseSalary || read.SignOnBonus != in.Compensation.SignOnBonus {
		t.Errorf("money = %d/%d, want %d/%d",
			read.BaseSalary, read.SignOnBonus, in.Compensation.BaseSalary, in.Compensation.SignOnBonus)
	}
	// char(3) pads anything shorter, so a currency that came back as "US " would
	// compare unequal to everything that was ever written.
	if read.Currency != "USD" {
		t.Errorf("currency = %q, want %q", read.Currency, "USD")
	}
	if !read.JoiningDate.Equal(in.JoiningDate) {
		t.Errorf("joiningDate = %v, want %v", read.JoiningDate, in.JoiningDate)
	}
	if len(read.CustomFields) != 1 || read.CustomFields[0].Key != "Relocation" {
		t.Errorf("customFields = %+v, want the stored clause", read.CustomFields)
	}
	if read.Status != domain.StatusDraft {
		t.Errorf("status = %q, want draft", read.Status)
	}
}

func TestOneTenantCannotReadAnother(t *testing.T) {
	st, ctx := testStore(t)

	mine, theirs := newCompany(), newCompany()
	ours := draft(t, st, ctx, mine, "usr_mine", draftInput())
	hers := draft(t, st, ctx, theirs, "usr_theirs", draftInput())

	page, err := st.ListOffers(ctx, mine, ListFilter{})
	if err != nil {
		t.Fatalf("ListOffers() error = %v", err)
	}
	if len(page.Offers) != 1 || page.Offers[0].ID != ours.ID {
		t.Fatalf("list = %d rows, want only this tenant's offer", len(page.Offers))
	}

	// The other tenant's id is a real id, and it still has to look absent: a 403
	// would confirm the offer exists, which for a compensation package is
	// already more than a stranger should learn.
	if _, err := st.FindOffer(ctx, mine, hers.ID); !errors.Is(err, domain.ErrOfferNotFound) {
		t.Errorf("FindOffer() across tenants = %v, want ErrOfferNotFound", err)
	}

	// Every mutation repeats the predicate, so none of them is a way in either.
	if _, err := st.SubmitOffer(ctx, mine, hers.ID, "usr_mine"); !errors.Is(err, domain.ErrOfferNotFound) {
		t.Errorf("SubmitOffer() across tenants = %v, want ErrOfferNotFound", err)
	}
	if _, err := st.UpdateOffer(ctx, mine, hers.ID, OfferPatch{}); !errors.Is(err, domain.ErrOfferNotFound) {
		t.Errorf("UpdateOffer() across tenants = %v, want ErrOfferNotFound", err)
	}
	if err := st.DeleteOffer(ctx, mine, hers.ID); !errors.Is(err, domain.ErrOfferNotFound) {
		t.Errorf("DeleteOffer() across tenants = %v, want ErrOfferNotFound", err)
	}

	// And the row is still there afterwards.
	if _, err := st.FindOffer(ctx, theirs, hers.ID); err != nil {
		t.Errorf("the other tenant's offer was damaged: %v", err)
	}
}

func TestTheLifecycleIsEnforcedInTheDatabase(t *testing.T) {
	st, ctx := testStore(t)
	companyID := newCompany()

	offer := draft(t, st, ctx, companyID, "usr_author", draftInput())

	// A draft cannot be sent: that is the whole point of the approval step.
	if _, _, err := st.SendOffer(ctx, companyID, offer.ID, "usr_sender", ""); err == nil {
		t.Fatal("SendOffer() on a draft succeeded")
	}

	submitted, err := st.SubmitOffer(ctx, companyID, offer.ID, "usr_author")
	if err != nil {
		t.Fatalf("SubmitOffer() error = %v", err)
	}
	if submitted.Status != domain.StatusPendingApproval || submitted.SubmittedAt == nil {
		t.Fatalf("after submit: status %q, submittedAt %v", submitted.Status, submitted.SubmittedAt)
	}

	// The author cannot wave their own package through without saying so.
	if _, err := st.ApproveOffer(ctx, companyID, offer.ID, "usr_author", false); !errors.Is(err, domain.ErrSelfApproval) {
		t.Fatalf("self-approval = %v, want ErrSelfApproval", err)
	}

	signed, err := st.ApproveOffer(ctx, companyID, offer.ID, "usr_approver", false)
	if err != nil {
		t.Fatalf("ApproveOffer() error = %v", err)
	}
	if signed.Status != domain.StatusApproved || signed.SelfApproved {
		t.Fatalf("after approve: status %q, selfApproved %v", signed.Status, signed.SelfApproved)
	}

	// An approved package is fixed: editing it here would launder a change past
	// the person who signed it off.
	newTitle := "Principal Engineer"
	if _, err := st.UpdateOffer(ctx, companyID, offer.ID, OfferPatch{Designation: &newTitle}); !errors.Is(err, domain.ErrNotEditable) {
		t.Fatalf("UpdateOffer() on an approved offer = %v, want ErrNotEditable", err)
	}

	sent, _, err := st.SendOffer(ctx, companyID, offer.ID, "usr_sender", "")
	if err != nil {
		t.Fatalf("SendOffer() error = %v", err)
	}
	if sent.Status != domain.StatusSent || sent.SentAt == nil {
		t.Fatalf("after send: status %q, sentAt %v", sent.Status, sent.SentAt)
	}

	accepted, err := st.RespondToOffer(ctx, companyID, offer.ID, domain.StatusAccepted, "", "usr_recruiter")
	if err != nil {
		t.Fatalf("RespondToOffer() error = %v", err)
	}
	if accepted.Status != domain.StatusAccepted || accepted.RespondedAt == nil {
		t.Fatalf("after accept: status %q, respondedAt %v", accepted.Status, accepted.RespondedAt)
	}

	// Terminal means terminal.
	var transitionErr *domain.TransitionError
	if _, err := st.RespondToOffer(ctx, companyID, offer.ID, domain.StatusDeclined, "", "usr_recruiter"); !errors.As(err, &transitionErr) {
		t.Fatalf("declining an accepted offer = %v, want a TransitionError", err)
	}
}

func TestASelfApprovalIsRecordedWhenItIsTaken(t *testing.T) {
	st, ctx := testStore(t)
	companyID := newCompany()

	offer := draft(t, st, ctx, companyID, "usr_author", draftInput())
	if _, err := st.SubmitOffer(ctx, companyID, offer.ID, "usr_author"); err != nil {
		t.Fatalf("SubmitOffer() error = %v", err)
	}

	signed, err := st.ApproveOffer(ctx, companyID, offer.ID, "usr_author", true)
	if err != nil {
		t.Fatalf("ApproveOffer() with the override = %v", err)
	}
	// Stored, not inferred: an auditor should not have to compare two account
	// ids to discover that nobody else looked at this package.
	if !signed.SelfApproved {
		t.Error("the self-approval was not recorded on the row")
	}
	if signed.ApprovedBy != "usr_author" {
		t.Errorf("approvedBy = %q, want the approver", signed.ApprovedBy)
	}
}

func TestSendingTwiceWithTheSameKeyDoesNotSendTwice(t *testing.T) {
	st, ctx := testStore(t)
	companyID := newCompany()

	offer := approved(t, st, ctx, companyID)
	key := idgen.New("key")

	first, replayed, err := st.SendOffer(ctx, companyID, offer.ID, "usr_sender", key)
	if err != nil {
		t.Fatalf("SendOffer() error = %v", err)
	}
	if replayed {
		t.Error("the first send reported itself as a replay")
	}

	second, replayed, err := st.SendOffer(ctx, companyID, offer.ID, "usr_sender", key)
	if err != nil {
		t.Fatalf("the retried send failed: %v", err)
	}
	if !replayed {
		t.Error("the retry was not recognized as a replay")
	}
	if second.SentAt == nil || !second.SentAt.Equal(*first.SentAt) {
		t.Errorf("sentAt moved on the retry: %v -> %v", first.SentAt, second.SentAt)
	}

	// One event, not two: a duplicate would put a second "offer sent"
	// notification in front of the candidate.
	if got := pendingEvents(t, st, ctx, offer.ID); got != 1 {
		t.Errorf("outbox holds %d events for this offer, want 1", got)
	}

	// A second send without the key is an ordinary illegal transition.
	if _, _, err := st.SendOffer(ctx, companyID, offer.ID, "usr_sender", ""); err == nil {
		t.Error("a keyless second send succeeded")
	}
}

func TestReusingAKeyForADifferentOfferIsRefused(t *testing.T) {
	st, ctx := testStore(t)
	companyID := newCompany()

	first := approved(t, st, ctx, companyID)
	second := approved(t, st, ctx, companyID)
	key := idgen.New("key")

	if _, _, err := st.SendOffer(ctx, companyID, first.ID, "usr_sender", key); err != nil {
		t.Fatalf("SendOffer() error = %v", err)
	}
	// The same key on a different offer is a client bug, not a safe replay:
	// answering with the first offer would tell the caller it had sent something
	// it had not.
	if _, _, err := st.SendOffer(ctx, companyID, second.ID, "usr_sender", key); !errors.Is(err, domain.ErrIdempotencyConflict) {
		t.Errorf("reused key = %v, want ErrIdempotencyConflict", err)
	}
}

func TestASentOfferCannotBeDeleted(t *testing.T) {
	st, ctx := testStore(t)
	companyID := newCompany()

	unsent := draft(t, st, ctx, companyID, "usr_author", draftInput())
	if err := st.DeleteOffer(ctx, companyID, unsent.ID); err != nil {
		t.Fatalf("DeleteOffer() on a draft = %v", err)
	}
	if _, err := st.FindOffer(ctx, companyID, unsent.ID); !errors.Is(err, domain.ErrOfferNotFound) {
		t.Errorf("the draft survived the delete: %v", err)
	}

	sent := approved(t, st, ctx, companyID)
	if _, _, err := st.SendOffer(ctx, companyID, sent.ID, "usr_sender", ""); err != nil {
		t.Fatalf("SendOffer() error = %v", err)
	}
	// It is the record of what the candidate was told.
	if err := st.DeleteOffer(ctx, companyID, sent.ID); !errors.Is(err, domain.ErrNotDeletable) {
		t.Errorf("DeleteOffer() on a sent offer = %v, want ErrNotDeletable", err)
	}
}

func TestAnExpiredOfferCannotBeAccepted(t *testing.T) {
	st, ctx := testStore(t)
	companyID := newCompany()

	offer := approved(t, st, ctx, companyID)
	if _, _, err := st.SendOffer(ctx, companyID, offer.ID, "usr_sender", ""); err != nil {
		t.Fatalf("SendOffer() error = %v", err)
	}

	// Backdated directly, because the API refuses a deadline in the past and
	// waiting for one to pass is not a test.
	past := time.Now().Add(-time.Hour)
	if _, err := st.Pool().Exec(ctx,
		`UPDATE offers SET expires_at = $2 WHERE id = $1`, offer.ID, past); err != nil {
		t.Fatalf("backdating the expiry failed: %v", err)
	}

	// The status still says "sent" — the sweeper has not run — and the answer is
	// still no, because the deadline is the authority and not the column.
	if _, err := st.RespondToOffer(ctx, companyID, offer.ID, domain.StatusAccepted, "", "usr_recruiter"); !errors.Is(err, domain.ErrExpired) {
		t.Fatalf("accepting a lapsed offer = %v, want ErrExpired", err)
	}

	if _, err := st.ExpireDueOffers(ctx); err != nil {
		t.Fatalf("ExpireDueOffers() error = %v", err)
	}
	swept, err := st.FindOffer(ctx, companyID, offer.ID)
	if err != nil {
		t.Fatalf("FindOffer() error = %v", err)
	}
	if swept.Status != domain.StatusExpired {
		t.Errorf("status after the sweep = %q, want expired", swept.Status)
	}
}

func TestTheSweepLeavesSettledOffersAlone(t *testing.T) {
	st, ctx := testStore(t)
	companyID := newCompany()

	offer := approved(t, st, ctx, companyID)
	if _, _, err := st.SendOffer(ctx, companyID, offer.ID, "usr_sender", ""); err != nil {
		t.Fatalf("SendOffer() error = %v", err)
	}
	if _, err := st.RespondToOffer(ctx, companyID, offer.ID, domain.StatusAccepted, "", "usr_recruiter"); err != nil {
		t.Fatalf("RespondToOffer() error = %v", err)
	}
	if _, err := st.Pool().Exec(ctx,
		`UPDATE offers SET expires_at = $2 WHERE id = $1`, offer.ID, time.Now().Add(-time.Hour)); err != nil {
		t.Fatalf("backdating the expiry failed: %v", err)
	}

	if _, err := st.ExpireDueOffers(ctx); err != nil {
		t.Fatalf("ExpireDueOffers() error = %v", err)
	}

	// An accepted offer whose deadline happens to have passed is not expired —
	// it was answered. Sweeping it would rewrite a hire as a lapse.
	settled, err := st.FindOffer(ctx, companyID, offer.ID)
	if err != nil {
		t.Fatalf("FindOffer() error = %v", err)
	}
	if settled.Status != domain.StatusAccepted {
		t.Errorf("status = %q, want accepted", settled.Status)
	}
}

func TestAWithdrawnApplicationClosesItsOffers(t *testing.T) {
	st, ctx := testStore(t)
	companyID, other := newCompany(), newCompany()

	in := draftInput()
	live := draft(t, st, ctx, companyID, "usr_author", in)

	// A second tenant that happens to hold an offer against the same application
	// id: the consumer scopes by company, so this one must not move.
	elsewhere := draft(t, st, ctx, other, "usr_theirs", in)

	expired, err := st.ExpireOffersForApplication(ctx, companyID, in.ApplicationID)
	if err != nil {
		t.Fatalf("ExpireOffersForApplication() error = %v", err)
	}
	if expired != 1 {
		t.Errorf("expired %d offers, want 1", expired)
	}

	closed, err := st.FindOffer(ctx, companyID, live.ID)
	if err != nil {
		t.Fatalf("FindOffer() error = %v", err)
	}
	if closed.Status != domain.StatusExpired {
		t.Errorf("status = %q, want expired", closed.Status)
	}

	untouched, err := st.FindOffer(ctx, other, elsewhere.ID)
	if err != nil {
		t.Fatalf("FindOffer() error = %v", err)
	}
	if untouched.Status != domain.StatusDraft {
		t.Errorf("the other tenant's offer became %q", untouched.Status)
	}
}

func TestTheSnapshotIsRefreshedButNeverBlanked(t *testing.T) {
	st, ctx := testStore(t)
	companyID := newCompany()

	in := draftInput()
	offer := draft(t, st, ctx, companyID, "usr_author", in)

	if _, err := st.SyncApplicationSnapshot(ctx, companyID, in.ApplicationID, "Priya Raman-Iyer", ""); err != nil {
		t.Fatalf("SyncApplicationSnapshot() error = %v", err)
	}

	read, err := st.FindOffer(ctx, companyID, offer.ID)
	if err != nil {
		t.Fatalf("FindOffer() error = %v", err)
	}
	if read.CandidateName != "Priya Raman-Iyer" {
		t.Errorf("candidateName = %q, want the updated name", read.CandidateName)
	}
	// An event that carries no job title must not turn a working list into a
	// column of empty cells.
	if read.JobTitle != in.JobTitle {
		t.Errorf("jobTitle = %q, want it left alone", read.JobTitle)
	}
}

func TestListingIsFilteredAndPaged(t *testing.T) {
	st, ctx := testStore(t)
	companyID := newCompany()

	wanted := draftInput()
	first := draft(t, st, ctx, companyID, "usr_author", wanted)
	second := draft(t, st, ctx, companyID, "usr_author", draftInput())
	if _, err := st.SubmitOffer(ctx, companyID, second.ID, "usr_author"); err != nil {
		t.Fatalf("SubmitOffer() error = %v", err)
	}

	byApplication, err := st.ListOffers(ctx, companyID, ListFilter{ApplicationID: wanted.ApplicationID})
	if err != nil {
		t.Fatalf("ListOffers() error = %v", err)
	}
	if len(byApplication.Offers) != 1 || byApplication.Offers[0].ID != first.ID {
		t.Errorf("filtering by application returned %d rows", len(byApplication.Offers))
	}

	byStatus, err := st.ListOffers(ctx, companyID, ListFilter{Status: string(domain.StatusPendingApproval)})
	if err != nil {
		t.Fatalf("ListOffers() error = %v", err)
	}
	if len(byStatus.Offers) != 1 || byStatus.Offers[0].ID != second.ID {
		t.Errorf("filtering by status returned %d rows", len(byStatus.Offers))
	}

	// A full page hands back a cursor; the next page starts after it and the two
	// do not overlap.
	page, err := st.ListOffers(ctx, companyID, ListFilter{Limit: 1})
	if err != nil {
		t.Fatalf("ListOffers() error = %v", err)
	}
	if len(page.Offers) != 1 || page.NextCursor == "" {
		t.Fatalf("page = %d rows, cursor %q", len(page.Offers), page.NextCursor)
	}

	next, err := st.ListOffers(ctx, companyID, ListFilter{Limit: 1, Cursor: page.NextCursor})
	if err != nil {
		t.Fatalf("ListOffers() error = %v", err)
	}
	if len(next.Offers) != 1 || next.Offers[0].ID == page.Offers[0].ID {
		t.Errorf("the second page repeated the first")
	}
}

func TestAnEventIsEnqueuedInTheSameTransaction(t *testing.T) {
	st, ctx := testStore(t)
	companyID := newCompany()

	offer := approved(t, st, ctx, companyID)

	// Nothing is announced until something happens: approving is internal.
	if got := pendingEvents(t, st, ctx, offer.ID); got != 0 {
		t.Fatalf("outbox holds %d events before the send, want 0", got)
	}

	if _, _, err := st.SendOffer(ctx, companyID, offer.ID, "usr_sender", ""); err != nil {
		t.Fatalf("SendOffer() error = %v", err)
	}
	if _, err := st.RespondToOffer(ctx, companyID, offer.ID, domain.StatusDeclined, "Took another role", "usr_recruiter"); err != nil {
		t.Fatalf("RespondToOffer() error = %v", err)
	}

	if got := pendingEvents(t, st, ctx, offer.ID); got != 2 {
		t.Errorf("outbox holds %d events, want 2 (sent, declined)", got)
	}

	// The money stays out of the payload: a subscriber cannot evaluate
	// `offers.view_compensation`, so a salary on the bus is a salary in every
	// consumer's database.
	var payloads []byte
	if err := st.Pool().QueryRow(ctx,
		`SELECT coalesce(string_agg(payload::text, ' '), '') FROM event_outbox WHERE payload->>'offerId' = $1`,
		offer.ID).Scan(&payloads); err != nil {
		t.Fatalf("reading the outbox failed: %v", err)
	}
	for _, leaked := range []string{"baseSalary", "signOnBonus", "currency", "17500000"} {
		if strings.Contains(string(payloads), leaked) {
			t.Errorf("the event payload carries %q", leaked)
		}
	}
}

func pendingEvents(t *testing.T, st *Store, ctx context.Context, offerID string) int {
	t.Helper()

	var count int
	if err := st.Pool().QueryRow(ctx,
		`SELECT count(*) FROM event_outbox WHERE payload->>'offerId' = $1`, offerID).Scan(&count); err != nil {
		t.Fatalf("counting outbox rows failed: %v", err)
	}
	return count
}
