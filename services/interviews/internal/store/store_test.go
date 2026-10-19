package store_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/karmarat/platform/packages/goshared/postgres"
	"github.com/karmarat/platform/services/interviews/internal/domain"
	"github.com/karmarat/platform/services/interviews/internal/store"
	"github.com/karmarat/platform/services/interviews/migrations"
)

// The tenant test is the one the service contract says must exist even when
// time is short, because the regression it catches is a breach rather than a
// bug: seed two companies, act as one, and assert the other's rows are
// invisible through every door this service opens.
//
// It needs a real database — the isolation lives in WHERE clauses, and a mock of
// this store would only assert that the code calls itself the way it does. Point
// INTERVIEWS_TEST_DATABASE_URL at a scratch database to run it; without one the
// file skips, so `go test ./...` stays green on a machine with no Postgres.
//
//	createdb interviews_test
//	INTERVIEWS_TEST_DATABASE_URL=postgres://karmarat:karmarat@localhost:5432/interviews_test?sslmode=disable \
//	  go test ./services/interviews/...

const (
	tenantA = "11111111-1111-1111-1111-111111111111"
	tenantB = "22222222-2222-2222-2222-222222222222"
)

func newStore(t *testing.T) (*store.Store, *pgxpool.Pool) {
	t.Helper()

	url := os.Getenv("INTERVIEWS_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set INTERVIEWS_TEST_DATABASE_URL to a scratch database to run the store tests")
	}

	ctx := context.Background()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))

	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)

	files, err := postgres.LoadMigrations(migrations.FS, ".")
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	if err := postgres.Migrate(ctx, pool, files, quiet); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Each test starts from an empty schema so one test's rows cannot satisfy
	// another's assertion. The cascade reaches the panel and the scorecards.
	if _, err := pool.Exec(ctx, `TRUNCATE interviews, event_outbox CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	return store.New(pool), pool
}

func seed(t *testing.T, st *store.Store, companyID, title string) domain.Interview {
	t.Helper()

	interview, _, err := st.CreateInterview(context.Background(), store.CreateInterviewInput{
		CompanyID:       companyID,
		ApplicationID:   "app_shared",
		CandidateID:     "cnd_shared",
		CandidateName:   "Ada Lovelace",
		JobTitle:        "Staff Engineer",
		RoundTitle:      title,
		RoundType:       "technical",
		ScheduledStart:  time.Now().UTC().Truncate(time.Second),
		DurationMinutes: 60,
		Format:          domain.FormatVideo,
		PanelMemberIDs:  []string{"usr_panel"},
		ActorID:         "usr_recruiter",
	})
	if err != nil {
		t.Fatalf("seed interview: %v", err)
	}
	return interview
}

// TestAnotherTenantsInterviewIsInvisible is the breach test.
//
// The two companies deliberately share an application id and a candidate id:
// if isolation were keyed off anything but company_id, this is the shape that
// would expose it.
func TestAnotherTenantsInterviewIsInvisible(t *testing.T) {
	st, _ := newStore(t)
	ctx := context.Background()

	mine := seed(t, st, tenantA, "Mine")
	theirs := seed(t, st, tenantB, "Theirs")

	t.Run("a list returns only the acting tenant's rows", func(t *testing.T) {
		page, err := st.ListInterviews(ctx, tenantA, store.ListFilter{})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(page.Interviews) != 1 || page.Interviews[0].ID != mine.ID {
			t.Fatalf("expected only %s, got %+v", mine.ID, page.Interviews)
		}
	})

	t.Run("a filter on the shared application does not widen the tenant", func(t *testing.T) {
		page, err := st.ListInterviews(ctx, tenantA, store.ListFilter{
			ApplicationID: "app_shared", CandidateID: "cnd_shared",
		})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(page.Interviews) != 1 || page.Interviews[0].ID != mine.ID {
			t.Fatalf("expected only %s, got %+v", mine.ID, page.Interviews)
		}
	})

	// Every one of these reads as "not found" rather than "forbidden". A 403
	// would confirm that the id exists, which is the thing being hidden.
	t.Run("a read by id", func(t *testing.T) {
		if _, err := st.FindInterview(ctx, tenantA, theirs.ID); err != domain.ErrInterviewNotFound {
			t.Fatalf("expected not found, got %v", err)
		}
	})

	t.Run("a patch by id", func(t *testing.T) {
		note := "edited"
		if _, err := st.UpdateInterview(ctx, tenantA, theirs.ID,
			store.InterviewPatch{Notes: &note}, "usr_recruiter"); err != domain.ErrInterviewNotFound {
			t.Fatalf("expected not found, got %v", err)
		}
	})

	t.Run("a status change by id", func(t *testing.T) {
		cancelled := domain.StatusCancelled
		if _, err := st.UpdateInterview(ctx, tenantA, theirs.ID,
			store.InterviewPatch{Status: &cancelled}, "usr_recruiter"); err != domain.ErrInterviewNotFound {
			t.Fatalf("expected not found, got %v", err)
		}
	})

	t.Run("a delete by id", func(t *testing.T) {
		if err := st.DeleteInterview(ctx, tenantA, theirs.ID, "usr_recruiter"); err != domain.ErrInterviewNotFound {
			t.Fatalf("expected not found, got %v", err)
		}
		if _, err := st.FindInterview(ctx, tenantB, theirs.ID); err != nil {
			t.Fatalf("the other tenant's row should be untouched: %v", err)
		}
	})

	t.Run("filing a scorecard by id", func(t *testing.T) {
		_, _, err := st.SaveScorecard(ctx, store.SaveScorecardInput{
			CompanyID: tenantA, InterviewID: theirs.ID, AuthorID: "usr_panel",
			Values: domain.ScorecardInput{OverallRating: 5, Recommendation: "strong_hire"},
		})
		if err != domain.ErrInterviewNotFound {
			t.Fatalf("expected not found, got %v", err)
		}
	})

	t.Run("reading scorecards by id", func(t *testing.T) {
		if _, _, err := st.SaveScorecard(ctx, store.SaveScorecardInput{
			CompanyID: tenantB, InterviewID: theirs.ID, AuthorID: "usr_panel",
			Values: domain.ScorecardInput{OverallRating: 5, Recommendation: "strong_hire"},
		}); err != nil {
			t.Fatalf("seed scorecard: %v", err)
		}

		cards, err := st.ListScorecards(ctx, tenantA, theirs.ID)
		if err != nil {
			t.Fatalf("list scorecards: %v", err)
		}
		if len(cards) != 0 {
			t.Fatalf("expected no scorecards across the tenant boundary, got %d", len(cards))
		}
	})

	t.Run("the consumer's writes are tenant-scoped too", func(t *testing.T) {
		// The application id is shared, so a projection keyed only on it would
		// rewrite the other tenant's row from an event about this one.
		if _, err := st.RefreshApplicationSnapshot(ctx, tenantA, "app_shared",
			"Renamed", "Retitled", time.Now().UTC()); err != nil {
			t.Fatalf("refresh: %v", err)
		}
		if _, err := st.CancelOpenInterviews(ctx, tenantA, "app_shared", "closed"); err != nil {
			t.Fatalf("cancel: %v", err)
		}

		untouched, err := st.FindInterview(ctx, tenantB, theirs.ID)
		if err != nil {
			t.Fatalf("find: %v", err)
		}
		if untouched.CandidateName != "Ada Lovelace" || untouched.Status != domain.StatusScheduled {
			t.Fatalf("the other tenant's row was modified: %+v", untouched)
		}
	})
}

// TestScorecardResubmissionReplacesOnlyTheAuthorsOwn pins the rule that makes
// the two permissions meaningful at the write end: an author corrects their own
// card and cannot reach anybody else's.
func TestScorecardResubmissionReplacesOnlyTheAuthorsOwn(t *testing.T) {
	st, _ := newStore(t)
	ctx := context.Background()

	interview := seed(t, st, tenantA, "Panel round")

	first, created, err := st.SaveScorecard(ctx, store.SaveScorecardInput{
		CompanyID: tenantA, InterviewID: interview.ID, AuthorID: "usr_one",
		Values: domain.ScorecardInput{OverallRating: 2, Recommendation: "no_hire"},
	})
	if err != nil || !created {
		t.Fatalf("first submission: created=%v err=%v", created, err)
	}

	if _, _, err := st.SaveScorecard(ctx, store.SaveScorecardInput{
		CompanyID: tenantA, InterviewID: interview.ID, AuthorID: "usr_two",
		Values: domain.ScorecardInput{OverallRating: 5, Recommendation: "strong_hire"},
	}); err != nil {
		t.Fatalf("second author: %v", err)
	}

	corrected, createdAgain, err := st.SaveScorecard(ctx, store.SaveScorecardInput{
		CompanyID: tenantA, InterviewID: interview.ID, AuthorID: "usr_one",
		Values: domain.ScorecardInput{OverallRating: 4, Recommendation: "hire"},
	})
	if err != nil {
		t.Fatalf("correction: %v", err)
	}
	if createdAgain {
		t.Fatal("expected a correction to replace the author's card, not create a second")
	}
	if corrected.ID != first.ID || corrected.OverallRating != 4 {
		t.Fatalf("expected %s updated to 4, got %+v", first.ID, corrected)
	}

	cards, err := st.ListScorecards(ctx, tenantA, interview.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(cards) != 2 {
		t.Fatalf("expected one card per author, got %d", len(cards))
	}
	for _, card := range cards {
		if card.AuthorID == "usr_two" && card.OverallRating != 5 {
			t.Fatalf("one author's correction changed another's card: %+v", card)
		}
	}
}

// TestOutOfOrderApplicationEventsDoNotRegressTheProjection pins the guard the
// consumer relies on. JetStream redelivers and does not promise order, so
// without it a stale redelivery would overwrite a corrected name.
func TestOutOfOrderApplicationEventsDoNotRegressTheProjection(t *testing.T) {
	st, _ := newStore(t)
	ctx := context.Background()

	interview := seed(t, st, tenantA, "Round")
	now := time.Now().UTC()

	if _, err := st.RefreshApplicationSnapshot(ctx, tenantA, "app_shared",
		"Ada L. Byron", "Principal Engineer", now); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	stale, err := st.RefreshApplicationSnapshot(ctx, tenantA, "app_shared",
		"Stale Name", "Stale Title", now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("stale refresh: %v", err)
	}
	if stale != 0 {
		t.Fatalf("expected a late event to touch no rows, it touched %d", stale)
	}

	current, err := st.FindInterview(ctx, tenantA, interview.ID)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if current.CandidateName != "Ada L. Byron" || current.JobTitle != "Principal Engineer" {
		t.Fatalf("a stale event overwrote the projection: %+v", current)
	}
}

// TestCancellingOpenInterviewsIsIdempotentAndSparesCompletedRounds pins what a
// redelivered rejection must and must not do.
func TestCancellingOpenInterviewsIsIdempotentAndSparesCompletedRounds(t *testing.T) {
	st, _ := newStore(t)
	ctx := context.Background()

	open := seed(t, st, tenantA, "Upcoming")
	done := seed(t, st, tenantA, "Already happened")

	completed := domain.StatusCompleted
	if _, err := st.UpdateInterview(ctx, tenantA, done.ID,
		store.InterviewPatch{Status: &completed, Note: "went well"}, "usr_recruiter"); err != nil {
		t.Fatalf("complete: %v", err)
	}

	first, err := st.CancelOpenInterviews(ctx, tenantA, "app_shared", "The application was closed.")
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if first != 1 {
		t.Fatalf("expected only the outstanding round to be cancelled, got %d", first)
	}

	again, err := st.CancelOpenInterviews(ctx, tenantA, "app_shared", "The application was closed.")
	if err != nil {
		t.Fatalf("redelivery: %v", err)
	}
	if again != 0 {
		t.Fatalf("expected a redelivered rejection to be a no-op, it cancelled %d", again)
	}

	survivor, err := st.FindInterview(ctx, tenantA, done.ID)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if survivor.Status != domain.StatusCompleted || survivor.OutcomeNote != "went well" {
		t.Fatalf("a rejection unmade a completed round: %+v", survivor)
	}

	cancelled, err := st.FindInterview(ctx, tenantA, open.ID)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if cancelled.Status != domain.StatusCancelled || cancelled.CancelReason == "" {
		t.Fatalf("expected the outstanding round cancelled with a reason: %+v", cancelled)
	}
}
