package store

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/karmarat/platform/packages/goshared/idgen"
	"github.com/karmarat/platform/packages/goshared/postgres"
	"github.com/karmarat/platform/services/messaging/internal/domain"
	"github.com/karmarat/platform/services/messaging/migrations"
)

// The tests below need a real Postgres: the rules they cover — the tenant
// predicate, the participant join, the unique index that settles two recruiters
// clicking "Message" at once — live in SQL and in constraints, and a fake would
// only prove that the fake agrees with itself.
//
// Point TEST_DATABASE_URL at a scratch database to run them:
//
//	TEST_DATABASE_URL=postgres://karmarat:karmarat@localhost:5432/messaging_test?sslmode=disable \
//	  go test ./services/messaging/...
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
// in for eight random bytes.
func newCompany() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic("store: no entropy for a test tenant: " + err.Error())
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80

	return fmt.Sprintf("%x-%x-%x-%x-%x", raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:16])
}

func newAccount() string { return idgen.New("acct") }

func openThread(t *testing.T, st *Store, ctx context.Context, in NewConversation) domain.Conversation {
	t.Helper()

	conversation, _, err := st.OpenConversation(ctx, in)
	if err != nil {
		t.Fatalf("OpenConversation() error = %v", err)
	}
	return conversation
}

func TestOneTenantCannotReadAnother(t *testing.T) {
	st, ctx := testStore(t)

	mine, theirs := newCompany(), newCompany()
	candidate := newAccount()

	ours := openThread(t, st, ctx, NewConversation{
		CompanyID: mine, CandidateAccountID: candidate,
		Origin: domain.OriginRecruiter, OpenedByAccountID: "usr_mine",
	})
	openThread(t, st, ctx, NewConversation{
		CompanyID: theirs, CandidateAccountID: candidate,
		Origin: domain.OriginRecruiter, OpenedByAccountID: "usr_theirs",
	})

	scope := CompanyScope{CompanyID: mine, ActorAccountID: "usr_mine", All: true}
	page, err := domain.NewPage(0, "")
	if err != nil {
		t.Fatalf("NewPage() error = %v", err)
	}

	list, err := st.ListForCompany(ctx, scope, page)
	if err != nil {
		t.Fatalf("ListForCompany() error = %v", err)
	}
	if len(list) != 1 || list[0].ID != ours.ID {
		t.Fatalf("list = %d rows, want only this tenant's thread", len(list))
	}

	// The other tenant's id is a real id, and it still has to look absent: a 403
	// here would confirm the thread exists.
	otherScope := CompanyScope{CompanyID: theirs, ActorAccountID: "usr_theirs", All: true}
	theirThread, err := st.ListForCompany(ctx, otherScope, page)
	if err != nil {
		t.Fatalf("ListForCompany() error = %v", err)
	}
	if _, err := st.FindForCompany(ctx, scope, theirThread[0].ID); !errors.Is(err, domain.ErrConversationNotFound) {
		t.Fatalf("FindForCompany() error = %v, want ErrConversationNotFound", err)
	}
}

func TestReadAllWidensWhatARecruiterSees(t *testing.T) {
	st, ctx := testStore(t)

	company := newCompany()
	page, err := domain.NewPage(0, "")
	if err != nil {
		t.Fatalf("NewPage() error = %v", err)
	}

	mine := openThread(t, st, ctx, NewConversation{
		CompanyID: company, CandidateAccountID: newAccount(),
		Origin: domain.OriginRecruiter, OpenedByAccountID: "usr_alice",
	})
	hers := openThread(t, st, ctx, NewConversation{
		CompanyID: company, CandidateAccountID: newAccount(),
		Origin: domain.OriginRecruiter, OpenedByAccountID: "usr_blake",
	})

	tests := []struct {
		name  string
		scope CompanyScope
		want  int
	}{
		{
			name:  "messaging.read shows only the threads the recruiter is in",
			scope: CompanyScope{CompanyID: company, ActorAccountID: "usr_alice"},
			want:  1,
		},
		{
			name:  "messaging.read_all shows the whole company",
			scope: CompanyScope{CompanyID: company, ActorAccountID: "usr_alice", All: true},
			want:  2,
		},
		{
			name:  "a recruiter in neither thread sees nothing",
			scope: CompanyScope{CompanyID: company, ActorAccountID: "usr_casey"},
			want:  0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			list, err := st.ListForCompany(ctx, tc.scope, page)
			if err != nil {
				t.Fatalf("ListForCompany() error = %v", err)
			}
			if len(list) != tc.want {
				t.Fatalf("list = %d rows, want %d", len(list), tc.want)
			}
		})
	}

	// And the narrow scope cannot reach the colleague's thread by id either.
	narrow := CompanyScope{CompanyID: company, ActorAccountID: "usr_alice"}
	if _, err := st.FindForCompany(ctx, narrow, hers.ID); !errors.Is(err, domain.ErrConversationNotFound) {
		t.Fatalf("FindForCompany() error = %v, want ErrConversationNotFound", err)
	}
	if _, err := st.FindForCompany(ctx, narrow, mine.ID); err != nil {
		t.Fatalf("FindForCompany() on the recruiter's own thread error = %v", err)
	}
}

func TestACandidateSeesOnlyTheirOwnThreads(t *testing.T) {
	st, ctx := testStore(t)

	company := newCompany()
	mine := newAccount()
	theirs := newAccount()

	ours := openThread(t, st, ctx, NewConversation{
		CompanyID: company, CandidateAccountID: mine,
		Origin: domain.OriginRecruiter, OpenedByAccountID: "usr_1",
	})
	other := openThread(t, st, ctx, NewConversation{
		CompanyID: company, CandidateAccountID: theirs,
		Origin: domain.OriginRecruiter, OpenedByAccountID: "usr_1",
	})

	page, err := domain.NewPage(0, "")
	if err != nil {
		t.Fatalf("NewPage() error = %v", err)
	}

	list, err := st.ListForCandidate(ctx, mine, page)
	if err != nil {
		t.Fatalf("ListForCandidate() error = %v", err)
	}
	if len(list) != 1 || list[0].ID != ours.ID {
		t.Fatalf("list = %d rows, want only this candidate's thread", len(list))
	}

	if _, err := st.FindForCandidate(ctx, mine, other.ID); !errors.Is(err, domain.ErrConversationNotFound) {
		t.Fatalf("FindForCandidate() error = %v, want ErrConversationNotFound", err)
	}
}

func TestOpeningTheSameThreadTwiceConflicts(t *testing.T) {
	st, ctx := testStore(t)

	company := newCompany()
	candidate := newAccount()
	in := NewConversation{
		CompanyID: company, CandidateAccountID: candidate,
		Origin: domain.OriginRecruiter, OpenedByAccountID: "usr_1",
	}

	first := openThread(t, st, ctx, in)

	// Two recruiters clicking "Message" at the same moment is a race only the
	// database can settle, so the second attempt must be refused rather than
	// produce a second thread.
	if _, _, err := st.OpenConversation(ctx, in); !errors.Is(err, domain.ErrConversationExists) {
		t.Fatalf("OpenConversation() error = %v, want ErrConversationExists", err)
	}

	existing, err := st.FindExisting(ctx, company, candidate, "")
	if err != nil {
		t.Fatalf("FindExisting() error = %v", err)
	}
	if existing.ID != first.ID {
		t.Errorf("existing = %q, want %q", existing.ID, first.ID)
	}

	// An application-scoped thread with the same candidate is a different
	// conversation and is allowed alongside it.
	withApplication := in
	withApplication.ApplicationID = idgen.New("app")
	withApplication.Origin = domain.OriginApplication
	if _, _, err := st.OpenConversation(ctx, withApplication); err != nil {
		t.Fatalf("OpenConversation() with an application context error = %v", err)
	}
}

func TestAnApproachIsOpenedOnlyOnce(t *testing.T) {
	st, ctx := testStore(t)

	company := newCompany()
	approach := idgen.New("apr")

	in := NewConversation{
		CompanyID: company, CandidateAccountID: newAccount(),
		Origin: domain.OriginApproach, OriginRef: approach, OpenedByAccountID: "usr_1",
	}

	openThread(t, st, ctx, in)

	// A redelivered approach carries the same origin ref, which the unique index
	// turns into a conflict rather than a duplicate thread. That is what makes
	// the consumer idempotent without a "seen events" table.
	if _, _, err := st.OpenConversation(ctx, in); !errors.Is(err, domain.ErrConversationExists) {
		t.Fatalf("OpenConversation() error = %v, want ErrConversationExists", err)
	}
}

func TestSendingMovesOnlyTheRecipientsCounter(t *testing.T) {
	st, ctx := testStore(t)

	company := newCompany()
	candidate := newAccount()
	conversation := openThread(t, st, ctx, NewConversation{
		CompanyID: company, CandidateAccountID: candidate,
		Origin: domain.OriginRecruiter, OpenedByAccountID: "usr_1",
	})

	if _, _, err := st.AppendMessage(ctx, conversation, NewMessage{
		SenderType: domain.SenderCompany, SenderAccountID: "usr_1", Body: "Are you free this week?",
	}); err != nil {
		t.Fatalf("AppendMessage() error = %v", err)
	}

	scope := CompanyScope{CompanyID: company, ActorAccountID: "usr_1"}
	after, err := st.FindForCompany(ctx, scope, conversation.ID)
	if err != nil {
		t.Fatalf("FindForCompany() error = %v", err)
	}

	if after.CandidateUnread != 1 {
		t.Errorf("candidate unread = %d, want 1", after.CandidateUnread)
	}
	if after.CompanyUnread != 0 {
		t.Errorf("company unread = %d, want 0; a sender does not make their own inbox unread", after.CompanyUnread)
	}
	if after.LastMessageSender != domain.SenderCompany {
		t.Errorf("last sender = %q, want company", after.LastMessageSender)
	}
	if after.LastMessagePreview == "" {
		t.Error("the inbox line should carry a preview")
	}

	// The candidate reads it: their counter clears and the message is stamped.
	read, err := st.MarkRead(ctx, after, domain.SenderCandidate, "")
	if err != nil {
		t.Fatalf("MarkRead() error = %v", err)
	}
	if read != 1 {
		t.Errorf("marked %d messages read, want 1", read)
	}

	cleared, err := st.FindForCandidate(ctx, candidate, conversation.ID)
	if err != nil {
		t.Fatalf("FindForCandidate() error = %v", err)
	}
	if cleared.CandidateUnread != 0 {
		t.Errorf("candidate unread = %d, want 0", cleared.CandidateUnread)
	}
}

func TestARetriedSendPostsOnce(t *testing.T) {
	st, ctx := testStore(t)

	company := newCompany()
	conversation := openThread(t, st, ctx, NewConversation{
		CompanyID: company, CandidateAccountID: newAccount(),
		Origin: domain.OriginRecruiter, OpenedByAccountID: "usr_1",
	})

	message := NewMessage{
		SenderType: domain.SenderCompany, SenderAccountID: "usr_1",
		Body: "Following up", IdempotencyKey: idgen.New("key"),
	}

	first, replayed, err := st.AppendMessage(ctx, conversation, message)
	if err != nil {
		t.Fatalf("AppendMessage() error = %v", err)
	}
	if replayed {
		t.Fatal("the first send should not report a replay")
	}

	second, replayed, err := st.AppendMessage(ctx, conversation, message)
	if err != nil {
		t.Fatalf("AppendMessage() on retry error = %v", err)
	}
	if !replayed {
		t.Error("a retry with the same key should report a replay")
	}
	if second.ID != first.ID {
		t.Errorf("retry produced %q, want the original %q", second.ID, first.ID)
	}

	page, err := domain.NewPage(0, "")
	if err != nil {
		t.Fatalf("NewPage() error = %v", err)
	}
	messages, err := st.ListMessages(ctx, conversation.ID, company, conversation.CandidateAccountID, page)
	if err != nil {
		t.Fatalf("ListMessages() error = %v", err)
	}
	if len(messages) != 1 {
		t.Errorf("thread holds %d messages, want 1", len(messages))
	}
}

func TestMessagesCannotBeReadAcrossTenants(t *testing.T) {
	st, ctx := testStore(t)

	company := newCompany()
	candidate := newAccount()
	conversation := openThread(t, st, ctx, NewConversation{
		CompanyID: company, CandidateAccountID: candidate,
		Origin: domain.OriginRecruiter, OpenedByAccountID: "usr_1",
	})
	if _, _, err := st.AppendMessage(ctx, conversation, NewMessage{
		SenderType: domain.SenderCompany, SenderAccountID: "usr_1", Body: "hello",
	}); err != nil {
		t.Fatalf("AppendMessage() error = %v", err)
	}

	page, err := domain.NewPage(0, "")
	if err != nil {
		t.Fatalf("NewPage() error = %v", err)
	}

	tests := []struct {
		name      string
		companyID string
		candidate string
		want      int
	}{
		{name: "the owning tenant and candidate", companyID: company, candidate: candidate, want: 1},
		{name: "another tenant", companyID: newCompany(), candidate: candidate, want: 0},
		{name: "another candidate", companyID: company, candidate: newAccount(), want: 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			messages, err := st.ListMessages(ctx, conversation.ID, tc.companyID, tc.candidate, page)
			if err != nil {
				t.Fatalf("ListMessages() error = %v", err)
			}
			if len(messages) != tc.want {
				t.Errorf("messages = %d, want %d", len(messages), tc.want)
			}
		})
	}
}

func TestPagingWalksAThreadWithoutRepeating(t *testing.T) {
	st, ctx := testStore(t)

	company := newCompany()
	conversation := openThread(t, st, ctx, NewConversation{
		CompanyID: company, CandidateAccountID: newAccount(),
		Origin: domain.OriginRecruiter, OpenedByAccountID: "usr_1",
	})

	const total = 7
	for i := 0; i < total; i++ {
		if _, _, err := st.AppendMessage(ctx, conversation, NewMessage{
			SenderType: domain.SenderCompany, SenderAccountID: "usr_1",
			Body: idgen.New("body"),
		}); err != nil {
			t.Fatalf("AppendMessage() error = %v", err)
		}
	}

	seen := map[string]bool{}
	cursor := ""
	for round := 0; round < total; round++ {
		page, err := domain.NewPage(3, cursor)
		if err != nil {
			t.Fatalf("NewPage() error = %v", err)
		}

		messages, err := st.ListMessages(ctx, conversation.ID, company, conversation.CandidateAccountID, page)
		if err != nil {
			t.Fatalf("ListMessages() error = %v", err)
		}
		if len(messages) == 0 {
			break
		}

		for _, message := range messages {
			if seen[message.ID] {
				t.Fatalf("message %q appeared on two pages", message.ID)
			}
			seen[message.ID] = true
		}

		last := messages[len(messages)-1]
		cursor = domain.Cursor{At: last.SentAt, ID: last.ID}.Encode()
		if len(messages) < page.Limit {
			break
		}
	}

	if len(seen) != total {
		t.Errorf("walked %d messages, want %d", len(seen), total)
	}
}

func TestTheDailyCountIgnoresApplicationThreads(t *testing.T) {
	st, ctx := testStore(t)

	company := newCompany()

	openThread(t, st, ctx, NewConversation{
		CompanyID: company, CandidateAccountID: newAccount(),
		Origin: domain.OriginRecruiter, OpenedByAccountID: "usr_1",
	})
	openThread(t, st, ctx, NewConversation{
		CompanyID: company, CandidateAccountID: newAccount(),
		ApplicationID: idgen.New("app"),
		Origin:        domain.OriginApplication, OpenedByAccountID: "usr_1",
	})

	// Replying to an applicant is not outreach, so it must not consume the
	// company's allowance for contacting strangers.
	count, err := st.CountConversationsOpenedSince(ctx, company, time.Now().Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("CountConversationsOpenedSince() error = %v", err)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}
}

func TestVisibilityIgnoresAStaleDelivery(t *testing.T) {
	st, ctx := testStore(t)

	account := newAccount()
	company := newCompany()

	if err := st.UpsertVisibility(ctx, account, true, []string{company}, 5); err != nil {
		t.Fatalf("UpsertVisibility() error = %v", err)
	}

	// An older delivery must not resurrect a block list the candidate has since
	// changed, or an event redelivered out of order re-opens a door they closed.
	if err := st.UpsertVisibility(ctx, account, true, nil, 2); err != nil {
		t.Fatalf("UpsertVisibility() with an older version error = %v", err)
	}

	visibility, err := st.FindVisibility(ctx, account)
	if err != nil {
		t.Fatalf("FindVisibility() error = %v", err)
	}
	if !visibility.Known {
		t.Fatal("visibility should be known after an upsert")
	}
	if !visibility.BlocksCompany(company) {
		t.Error("the stale delivery cleared a block list it should not have touched")
	}
}

func TestAnUnknownCandidateIsNotAnError(t *testing.T) {
	st, ctx := testStore(t)

	visibility, err := st.FindVisibility(ctx, newAccount())
	if err != nil {
		t.Fatalf("FindVisibility() error = %v", err)
	}
	if visibility.Known {
		t.Error("a candidate no event has arrived for should not be reported as known")
	}
}

func TestApplicationLinksAreIdempotent(t *testing.T) {
	st, ctx := testStore(t)

	company := newCompany()
	candidate := newAccount()
	link := ApplicationLink{
		CompanyID: company, CandidateAccountID: candidate,
		ApplicationID: idgen.New("app"), JobID: "job_1", JobTitle: "Engineer",
		SubmittedAt: time.Now().UTC(),
	}

	for i := 0; i < 2; i++ {
		if err := st.RecordApplication(ctx, link); err != nil {
			t.Fatalf("RecordApplication() error = %v", err)
		}
	}

	applied, err := st.HasApplied(ctx, company, candidate)
	if err != nil {
		t.Fatalf("HasApplied() error = %v", err)
	}
	if !applied {
		t.Error("the candidate applied, so the link should be found")
	}

	elsewhere, err := st.HasApplied(ctx, newCompany(), candidate)
	if err != nil {
		t.Fatalf("HasApplied() error = %v", err)
	}
	if elsewhere {
		t.Error("an application to one company is not permission for another")
	}
}

func TestOpeningAThreadEnqueuesItsEvent(t *testing.T) {
	st, ctx := testStore(t)

	company := newCompany()
	openThread(t, st, ctx, NewConversation{
		CompanyID: company, CandidateAccountID: newAccount(),
		Origin: domain.OriginRecruiter, OpenedByAccountID: "usr_1",
		FirstMessage: &NewMessage{
			SenderType: domain.SenderCompany, SenderAccountID: "usr_1", Body: "hello",
		},
	})

	// The thread, its first message and both events commit together or not at
	// all; a publish outside that transaction could announce a thread that
	// rolled back, or lose one that did not.
	var pending int
	if err := st.pool.QueryRow(ctx,
		`SELECT count(*) FROM event_outbox WHERE company_id = $1 AND published_at IS NULL`,
		company).Scan(&pending); err != nil {
		t.Fatalf("count outbox rows: %v", err)
	}
	if pending != 2 {
		t.Errorf("pending events = %d, want 2 (conversation opened, message sent)", pending)
	}
}
