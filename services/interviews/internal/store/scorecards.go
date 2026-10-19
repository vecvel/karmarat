package store

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5"

	"github.com/karmarat/platform/packages/goshared/idgen"
	"github.com/karmarat/platform/services/interviews/internal/domain"
)

const scorecardSelect = `
	SELECT c.id, c.company_id, c.interview_id, c.author_id,
	       c.overall_rating, c.recommendation,
	       c.technical_score, c.communication_score, c.culture_score,
	       coalesce(c.strengths, ''), coalesce(c.concerns, ''), coalesce(c.feedback_notes, ''),
	       c.created_at, c.updated_at
	FROM interview_scorecards c`

func scanScorecard(row pgx.Row) (domain.Scorecard, error) {
	var c domain.Scorecard
	err := row.Scan(
		&c.ID, &c.CompanyID, &c.InterviewID, &c.AuthorID,
		&c.OverallRating, &c.Recommendation,
		&c.TechnicalScore, &c.CommunicationScore, &c.CultureScore,
		&c.Strengths, &c.Concerns, &c.FeedbackNotes,
		&c.CreatedAt, &c.UpdatedAt,
	)
	return c, err
}

// SaveScorecardInput is one interviewer's verdict on one round.
type SaveScorecardInput struct {
	// Privileged is the caller holding interviews.update — whoever runs the
	// process, who may file on a round they are not seated on.
	Privileged  bool
	CompanyID   string
	InterviewID string
	AuthorID    string
	Values      domain.ScorecardInput
}

// SaveScorecard records or replaces an author's scorecard for a round.
//
// Submitting again replaces the author's own card rather than being refused.
// Feedback is written straight after an interview and corrected minutes later —
// "I mixed up the two candidates" has to have an answer — and the unique index
// still enforces the real invariant, which is one card per interviewer per
// round. An author can only ever overwrite themselves: the predicate carries
// their account id, so nobody edits a colleague's verdict.
//
// The returned flag says whether this call created the card, so the handler can
// answer 201 on a first submission and 200 on a correction.
func (s *Store) SaveScorecard(ctx context.Context, in SaveScorecardInput) (domain.Scorecard, bool, error) {
	var (
		saved   domain.Scorecard
		created bool
	)

	err := s.InTx(ctx, func(tx pgx.Tx) error {
		// The interview is resolved inside the transaction and inside the tenant,
		// so a scorecard cannot be attached to another company's round by id.
		interview, err := s.findInterviewTx(ctx, tx, in.CompanyID, in.InterviewID)
		if err != nil {
			return err
		}

		// The panel is re-checked here, not only in the handler.
		//
		// The handler reads the panel outside any transaction, so a recruiter who
		// removes somebody between that read and this write would otherwise let
		// the removed interviewer's verdict land anyway — the exact window the
		// permission exists to close. Re-asking inside the transaction that does
		// the writing is what makes the answer binding.
		if !domain.MaySubmitScorecard(interview, in.AuthorID, in.Privileged) {
			return domain.ErrNotOnPanel
		}

		var existingID string
		err = tx.QueryRow(ctx, `
			SELECT id FROM interview_scorecards
			WHERE interview_id = $1 AND author_id = $2 AND company_id = $3
			FOR UPDATE`,
			in.InterviewID, in.AuthorID, in.CompanyID).Scan(&existingID)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			created = true
		case err != nil:
			return fmt.Errorf("store: look up scorecard: %w", err)
		}

		id := existingID
		if created {
			id = idgen.New("scr")
		}

		row := tx.QueryRow(ctx, `
			INSERT INTO interview_scorecards (
				id, company_id, interview_id, author_id,
				overall_rating, recommendation,
				technical_score, communication_score, culture_score,
				strengths, concerns, feedback_notes
			) VALUES ($1, $2, $3, $4, $5, $6::interview_recommendation, $7, $8, $9, $10, $11, $12)
			ON CONFLICT (interview_id, author_id) DO UPDATE SET
				overall_rating      = EXCLUDED.overall_rating,
				recommendation      = EXCLUDED.recommendation,
				technical_score     = EXCLUDED.technical_score,
				communication_score = EXCLUDED.communication_score,
				culture_score       = EXCLUDED.culture_score,
				strengths           = EXCLUDED.strengths,
				concerns            = EXCLUDED.concerns,
				feedback_notes      = EXCLUDED.feedback_notes
			RETURNING id`,
			id, in.CompanyID, in.InterviewID, in.AuthorID,
			in.Values.OverallRating, in.Values.Recommendation,
			in.Values.TechnicalScore, in.Values.CommunicationScore, in.Values.CultureScore,
			nullable(in.Values.Strengths), nullable(in.Values.Concerns), nullable(in.Values.FeedbackNotes),
		)

		var storedID string
		if err := row.Scan(&storedID); err != nil {
			// The FOR UPDATE above already serialises two writers on the same
			// (interview, author), and ON CONFLICT covers the row itself, so a
			// unique violation here has no benign reading left. Recovering by
			// falling back to existingID — empty on the create path — turned it
			// into a 404 telling an interviewer their feedback was not found,
			// on a transaction Postgres had already aborted so the follow-up
			// query could not have answered anyway.
			return fmt.Errorf("store: save scorecard: %w", err)
		}

		saved, err = s.findScorecardTx(ctx, tx, in.CompanyID, storedID)
		return err
	})

	return saved, created, err
}

func (s *Store) findScorecardTx(ctx context.Context, tx pgx.Tx, companyID, id string) (domain.Scorecard, error) {
	query := scorecardSelect + ` WHERE c.id = $1 AND c.company_id = $2`

	card, err := scanScorecard(s.queryRow(ctx, tx, query, id, companyID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Scorecard{}, domain.ErrScorecardNotFound
		}
		return domain.Scorecard{}, fmt.Errorf("store: find scorecard: %w", err)
	}
	return card, nil
}

// MaxScorecardsPerRound bounds one round's feedback.
//
// A panel is a handful of people, but the submit path also admits anyone holding
// interviews.update, so the row count is not structurally small — and an
// unbounded list is an unbounded response. The cap is far above any real panel,
// so reaching it means something is wrong rather than something is busy.
const MaxScorecardsPerRound = 200

// ListScorecards returns the scorecards filed against one of a company's rounds.
//
// It does not filter by who may read them. Deciding that is
// domain.MayReadScorecard's job, applied in the handler, and duplicating the
// decision as a SQL predicate would give the rule two implementations that can
// disagree — which for this rule means an interviewer reading a colleague's
// verdict because one was updated and the other was not.
//
// It is capped rather than cursor-paginated for the same reason: the handler
// has to see the whole round to answer "how many may you read", and a page
// boundary would make that answer depend on where the page fell.
func (s *Store) ListScorecards(ctx context.Context, companyID, interviewID string) ([]domain.Scorecard, error) {
	query := scorecardSelect +
		` WHERE c.company_id = $1 AND c.interview_id = $2 ORDER BY c.created_at, c.id
		  LIMIT ` + strconv.Itoa(MaxScorecardsPerRound)

	rows, err := s.query(ctx, nil, query, companyID, interviewID)
	if err != nil {
		return nil, fmt.Errorf("store: list scorecards: %w", err)
	}
	defer rows.Close()

	cards := make([]domain.Scorecard, 0, 8)
	for rows.Next() {
		card, scanErr := scanScorecard(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("store: scan scorecard: %w", scanErr)
		}
		cards = append(cards, card)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: read scorecards: %w", err)
	}
	return cards, nil
}
