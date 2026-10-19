package store

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/karmarat/platform/packages/goshared/events"
	"github.com/karmarat/platform/packages/goshared/idgen"
	"github.com/karmarat/platform/services/interviews/internal/domain"
)

// The panel and the scorecard existence flag are correlated subqueries rather
// than a second round trip: a schedule renders both on every row, and fetching
// them per interview is the N+1 that makes a week view slow the moment a company
// runs more than a handful of loops.
//
// has_scorecard is deliberately only a boolean here. Whether feedback exists is
// something anyone who may read the round may know; what it says is decided
// separately, in domain.MayReadScorecard.
const interviewSelect = `
	SELECT i.id, i.company_id, i.application_id, i.candidate_id,
	       i.candidate_name, i.job_title,
	       i.round_title, i.round_type, i.scheduled_start, i.duration_minutes,
	       i.format, coalesce(i.meeting_link, ''), coalesce(i.notes, ''),
	       i.status, coalesce(i.outcome_note, ''), coalesce(i.cancellation_reason, ''),
	       i.completed_at, i.cancelled_at,
	       coalesce(i.created_by, ''), i.created_at, i.updated_at,
	       coalesce((SELECT array_agg(p.account_id ORDER BY p.account_id)
	                 FROM interview_panel p WHERE p.interview_id = i.id), '{}') AS panel,
	       EXISTS (SELECT 1 FROM interview_scorecards c WHERE c.interview_id = i.id) AS has_scorecard
	FROM interviews i`

func scanInterview(row pgx.Row) (domain.Interview, error) {
	var i domain.Interview
	err := row.Scan(
		&i.ID, &i.CompanyID, &i.ApplicationID, &i.CandidateID,
		&i.CandidateName, &i.JobTitle,
		&i.RoundTitle, &i.RoundType, &i.ScheduledStart, &i.DurationMinutes,
		&i.Format, &i.MeetingLink, &i.Notes,
		&i.Status, &i.OutcomeNote, &i.CancelReason,
		&i.CompletedAt, &i.CancelledAt,
		&i.CreatedBy, &i.CreatedAt, &i.UpdatedAt,
		&i.PanelMemberIDs, &i.HasScorecard,
	)
	if i.PanelMemberIDs == nil {
		// A JSON null here would make the portal branch on it; an empty panel is
		// an empty list.
		i.PanelMemberIDs = []string{}
	}
	return i, err
}

/* -------------------------------------------------------------------------- */
/* Create                                                                     */
/* -------------------------------------------------------------------------- */

// CreateInterviewInput is one booked round.
type CreateInterviewInput struct {
	CompanyID       string
	ApplicationID   string
	CandidateID     string
	CandidateName   string
	JobTitle        string
	RoundTitle      string
	RoundType       string
	ScheduledStart  time.Time
	DurationMinutes int
	Format          domain.Format
	MeetingLink     string
	Notes           string
	PanelMemberIDs  []string
	ActorID         string
	// IdempotencyKey, when supplied, makes a retried create return the round it
	// already booked instead of booking a second one.
	IdempotencyKey string
}

// CreateInterview books a round and announces it in the same transaction.
// CreateInterview books a round.
//
// The second return value reports whether this call created it. A retry that
// presents the same idempotency key gets `false` and the round already booked:
// scheduling publishes `interview.scheduled`, which notifications turns into an
// invitation to the candidate and the panel, so a create retried after a timeout
// would otherwise put a duplicate round on the calendar and invite everyone to
// it twice.
func (s *Store) CreateInterview(ctx context.Context, in CreateInterviewInput) (domain.Interview, bool, error) {
	var (
		created domain.Interview
		isNew   = true
	)

	err := s.InTx(ctx, func(tx pgx.Tx) error {
		// Asked before the insert so the common retry is a plain read rather
		// than a constraint violation that aborts the transaction.
		if in.IdempotencyKey != "" {
			var existingID string
			err := tx.QueryRow(ctx, `
				SELECT id FROM interviews
				WHERE company_id = $1 AND idempotency_key = $2`,
				in.CompanyID, in.IdempotencyKey).Scan(&existingID)
			switch {
			case err == nil:
				isNew = false
				created, err = s.findInterviewTx(ctx, tx, in.CompanyID, existingID)
				return err
			case !errors.Is(err, pgx.ErrNoRows):
				return fmt.Errorf("store: look up idempotency key: %w", err)
			}
		}

		id := idgen.New("itv")

		if _, err := tx.Exec(ctx, `
			INSERT INTO interviews (
				id, company_id, application_id, candidate_id,
				candidate_name, job_title,
				round_title, round_type, scheduled_start, duration_minutes,
				format, meeting_link, notes, status, created_by, idempotency_key
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, 'scheduled', $14, $15)`,
			id, in.CompanyID, in.ApplicationID, in.CandidateID,
			in.CandidateName, in.JobTitle,
			in.RoundTitle, in.RoundType, in.ScheduledStart, in.DurationMinutes,
			in.Format, nullable(in.MeetingLink), nullable(in.Notes), nullable(in.ActorID),
			nullable(in.IdempotencyKey),
		); err != nil {
			// Two retries that raced past the read above. The other one booked
			// the round, which is the outcome the caller wanted.
			if isUniqueViolation(err) && in.IdempotencyKey != "" {
				return domain.ErrIdempotencyRace
			}
			return fmt.Errorf("store: create interview: %w", err)
		}

		if err := s.replacePanel(ctx, tx, in.CompanyID, id, in.PanelMemberIDs); err != nil {
			return err
		}

		var err error
		if created, err = s.findInterviewTx(ctx, tx, in.CompanyID, id); err != nil {
			return err
		}

		return s.enqueueEvent(ctx, tx, outboxEntry{
			Subject:   events.SubjectInterviewScheduled,
			CompanyID: in.CompanyID,
			ActorID:   in.ActorID,
			Payload:   interviewPayload(created),
		})
	})

	return created, isNew, err
}

/* -------------------------------------------------------------------------- */
/* Read                                                                       */
/* -------------------------------------------------------------------------- */

// FindInterview resolves one round within a tenant.
func (s *Store) FindInterview(ctx context.Context, companyID, id string) (domain.Interview, error) {
	return s.findInterviewTx(ctx, nil, companyID, id)
}

func (s *Store) findInterviewTx(ctx context.Context, tx pgx.Tx, companyID, id string) (domain.Interview, error) {
	query := interviewSelect + ` WHERE i.id = $1 AND i.company_id = $2`

	interview, err := scanInterview(s.queryRow(ctx, tx, query, id, companyID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Interview{}, domain.ErrInterviewNotFound
		}
		return domain.Interview{}, fmt.Errorf("store: find interview: %w", err)
	}
	return interview, nil
}

// ListFilter narrows a schedule listing.
type ListFilter struct {
	Status        string
	ApplicationID string
	CandidateID   string
	From          *time.Time
	To            *time.Time
	Limit         int
	Cursor        string
}

// Page is one page of results plus the cursor that continues it.
type Page struct {
	Interviews []domain.Interview
	NextCursor string
}

// ListInterviews returns one page of a company's schedule.
//
// Ordering is by start time, not by id, because this list is a calendar: a
// recruiter opening it wants the next round, not the most recently created one.
// That makes the id alone useless as a cursor, so the cursor carries both the
// start time and the id and the predicate compares the pair — two rounds booked
// for the same minute stay on opposite sides of a page boundary instead of one
// of them being skipped.
func (s *Store) ListInterviews(ctx context.Context, companyID string, filter ListFilter) (Page, error) {
	where := []string{"i.company_id = $1"}
	args := []any{companyID}

	appendCondition := func(clause string, value any) {
		args = append(args, value)
		where = append(where, fmt.Sprintf(clause, len(args)))
	}

	if filter.Status != "" {
		appendCondition("i.status = $%d", filter.Status)
	}
	if filter.ApplicationID != "" {
		appendCondition("i.application_id = $%d", filter.ApplicationID)
	}
	if filter.CandidateID != "" {
		appendCondition("i.candidate_id = $%d", filter.CandidateID)
	}
	if filter.From != nil {
		appendCondition("i.scheduled_start >= $%d", *filter.From)
	}
	if filter.To != nil {
		appendCondition("i.scheduled_start <= $%d", *filter.To)
	}

	if filter.Cursor != "" {
		start, id, err := decodeCursor(filter.Cursor)
		if err != nil {
			return Page{}, err
		}
		args = append(args, start, id)
		where = append(where, fmt.Sprintf("(i.scheduled_start, i.id) < ($%d, $%d)", len(args)-1, len(args)))
	}

	limit := normalizeLimit(filter.Limit)
	args = append(args, limit+1)

	query := interviewSelect +
		" WHERE " + strings.Join(where, " AND ") +
		" ORDER BY i.scheduled_start DESC, i.id DESC LIMIT $" + strconv.Itoa(len(args))

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return Page{}, fmt.Errorf("store: list interviews: %w", err)
	}
	defer rows.Close()

	interviews := make([]domain.Interview, 0, limit)
	for rows.Next() {
		interview, scanErr := scanInterview(rows)
		if scanErr != nil {
			return Page{}, fmt.Errorf("store: scan interview: %w", scanErr)
		}
		interviews = append(interviews, interview)
	}
	if err := rows.Err(); err != nil {
		return Page{}, fmt.Errorf("store: read interviews: %w", err)
	}

	page := Page{Interviews: interviews}
	// One row beyond the page was requested purely to learn whether another page
	// exists, which is cheaper than a second count query.
	if len(interviews) > limit {
		page.Interviews = interviews[:limit]
		last := page.Interviews[limit-1]
		page.NextCursor = encodeCursor(last.ScheduledStart, last.ID)
	}
	return page, nil
}

// ErrBadCursor means the caller sent a cursor this service did not issue.
var ErrBadCursor = errors.New("the supplied cursor is not valid")

func encodeCursor(start time.Time, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(start.UTC().Format(time.RFC3339Nano) + "|" + id))
}

func decodeCursor(cursor string) (time.Time, string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, "", ErrBadCursor
	}
	stamp, id, found := strings.Cut(string(raw), "|")
	if !found || id == "" {
		return time.Time{}, "", ErrBadCursor
	}
	start, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil {
		return time.Time{}, "", ErrBadCursor
	}
	return start, id, nil
}

/* -------------------------------------------------------------------------- */
/* Update                                                                     */
/* -------------------------------------------------------------------------- */

// InterviewPatch carries the fields a recruiter may edit.
//
// Status is here rather than only on the cancel and complete routes because the
// portal drives it from a single control; it still goes through the same
// transition check, so no path can reach an illegal status.
type InterviewPatch struct {
	RoundTitle      *string
	RoundType       *string
	ScheduledStart  *time.Time
	DurationMinutes *int
	Format          *string
	MeetingLink     *string
	Notes           *string
	PanelMemberIDs  *[]string
	Status          *domain.Status
	Note            string
}

// UpdateInterview applies a patch to one of a company's rounds.
//
// The row is locked for the length of the transaction because a status change is
// read-modify-write: without the lock two concurrent requests both read
// "scheduled", both find their transition legal, and the second silently
// overwrites the first's cancellation reason.
func (s *Store) UpdateInterview(
	ctx context.Context, companyID, id string, patch InterviewPatch, actorID string,
) (domain.Interview, error) {
	var updated domain.Interview

	err := s.InTx(ctx, func(tx pgx.Tx) error {
		current, err := s.lockInterview(ctx, tx, companyID, id)
		if err != nil {
			return err
		}

		if patch.Status != nil && !current.CanTransitionTo(*patch.Status) {
			return &domain.TransitionError{From: current, To: *patch.Status}
		}

		// A completed or cancelled round is a record of something that happened.
		// Gating only the status column left every other one writable, so the
		// time and title of a round that already ran — with scorecards filed
		// against it — could be rewritten underneath that feedback.
		if !current.Open() && patch.Status == nil {
			return &domain.TransitionError{From: current, To: current}
		}

		// The enum columns are driven by a plain *string so the parameter's type
		// is the one Postgres infers from the cast in the statement, not one
		// derived from a Go named type.
		var statusArg *string
		if patch.Status != nil {
			value := string(*patch.Status)
			statusArg = &value
		}

		if _, err := tx.Exec(ctx, `
			UPDATE interviews SET
				round_title      = coalesce($3, round_title),
				round_type       = coalesce($4, round_type),
				scheduled_start  = coalesce($5, scheduled_start),
				duration_minutes = coalesce($6, duration_minutes),
				format           = coalesce($7::interview_format, format),
				meeting_link     = coalesce($8::text, meeting_link),
				notes            = coalesce($9::text, notes),
				status           = coalesce($10::interview_status, status),
				-- The three outcome columns are only touched by the transition
				-- that produces them, so re-scheduling a no-show does not carry
				-- its old completion timestamp forward.
				completed_at        = CASE WHEN $10::interview_status = 'completed' THEN now()
				                           WHEN $10::interview_status IS NOT NULL THEN NULL
				                           ELSE completed_at END,
				cancelled_at        = CASE WHEN $10::interview_status = 'cancelled' THEN now()
				                           WHEN $10::interview_status IS NOT NULL THEN NULL
				                           ELSE cancelled_at END,
				outcome_note        = CASE WHEN $10::interview_status = 'completed' THEN $11::text
				                           WHEN $10::interview_status IS NOT NULL THEN NULL
				                           ELSE outcome_note END,
				cancellation_reason = CASE WHEN $10::interview_status = 'cancelled' THEN $11::text
				                           WHEN $10::interview_status IS NOT NULL THEN NULL
				                           ELSE cancellation_reason END
			WHERE id = $1 AND company_id = $2`,
			id, companyID,
			patch.RoundTitle, patch.RoundType, patch.ScheduledStart, patch.DurationMinutes,
			patch.Format, patch.MeetingLink, patch.Notes, statusArg, nullable(patch.Note),
		); err != nil {
			return fmt.Errorf("store: update interview: %w", err)
		}

		if patch.PanelMemberIDs != nil {
			if err := s.replacePanel(ctx, tx, companyID, id, *patch.PanelMemberIDs); err != nil {
				return err
			}
		}

		if updated, err = s.findInterviewTx(ctx, tx, companyID, id); err != nil {
			return err
		}

		if subject := subjectForStatus(patch.Status); subject != "" {
			return s.enqueueEvent(ctx, tx, outboxEntry{
				Subject:   subject,
				CompanyID: companyID,
				ActorID:   actorID,
				Payload:   interviewPayload(updated),
			})
		}
		return nil
	})

	return updated, err
}

// subjectForStatus maps a status change onto the event it announces.
//
// A move back to scheduled after a no-show publishes nothing: there is no
// "rescheduled" subject in the platform's catalogue, and reusing "scheduled"
// would make a downstream counter double-count the round.
func subjectForStatus(status *domain.Status) string {
	if status == nil {
		return ""
	}
	switch *status {
	case domain.StatusCompleted:
		return events.SubjectInterviewCompleted
	case domain.StatusCancelled:
		return events.SubjectInterviewCancelled
	default:
		return ""
	}
}

// lockInterview reads a round's current status and holds the row until commit.
func (s *Store) lockInterview(ctx context.Context, tx pgx.Tx, companyID, id string) (domain.Status, error) {
	var status domain.Status
	err := tx.QueryRow(ctx,
		`SELECT status FROM interviews WHERE id = $1 AND company_id = $2 FOR UPDATE`,
		id, companyID).Scan(&status)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", domain.ErrInterviewNotFound
		}
		return "", fmt.Errorf("store: lock interview: %w", err)
	}
	return status, nil
}

// replacePanel makes the stored panel match the supplied list exactly.
//
// Delete-then-insert rather than a diff: the panel is small, and a diff is one
// more place for a member to be left behind — which here means somebody keeping
// the right to file a scorecard for a round they were taken off.
func (s *Store) replacePanel(ctx context.Context, tx pgx.Tx, companyID, interviewID string, members []string) error {
	if _, err := tx.Exec(ctx,
		`DELETE FROM interview_panel WHERE interview_id = $1 AND company_id = $2`,
		interviewID, companyID); err != nil {
		return fmt.Errorf("store: clear panel: %w", err)
	}

	for _, member := range domain.NormalizePanel(members) {
		if _, err := tx.Exec(ctx, `
			INSERT INTO interview_panel (interview_id, company_id, account_id)
			VALUES ($1, $2, $3)
			ON CONFLICT (interview_id, account_id) DO NOTHING`,
			interviewID, companyID, member); err != nil {
			return fmt.Errorf("store: add panel member: %w", err)
		}
	}
	return nil
}

/* -------------------------------------------------------------------------- */
/* Delete                                                                     */
/* -------------------------------------------------------------------------- */

// DeleteInterview removes a round, announcing it if anyone was still expecting it.
//
// A scheduled round that disappears has the same consequence for a calendar, a
// reminder or a candidate as a cancelled one, so deleting one publishes
// `interview.cancelled`. Deleting a round that already ran announces nothing:
// there is no upcoming commitment to withdraw.
func (s *Store) DeleteInterview(ctx context.Context, companyID, id, actorID string) error {
	return s.InTx(ctx, func(tx pgx.Tx) error {
		// Locked before it is read. Under READ COMMITTED an unlocked SELECT lets a
		// second concurrent delete see the row too, and both transactions then
		// enqueue a cancellation under their own outbox id — which is the one
		// thing JetStream's id-based de-duplication cannot catch, so the
		// candidate is told twice that their interview is off.
		if _, err := s.lockInterview(ctx, tx, companyID, id); err != nil {
			return err
		}

		// The full row, for the payload the cancellation carries.
		interview, err := s.findInterviewTx(ctx, tx, companyID, id)
		if err != nil {
			return err
		}

		tag, err := tx.Exec(ctx,
			`DELETE FROM interviews WHERE id = $1 AND company_id = $2`, id, companyID)
		if err != nil {
			return fmt.Errorf("store: delete interview: %w", err)
		}
		// The lock makes this unreachable today, but the event is only honest if
		// this transaction is the one that removed the row.
		if tag.RowsAffected() == 0 {
			return domain.ErrInterviewNotFound
		}

		if !interview.Status.Open() {
			return nil
		}

		interview.Status = domain.StatusCancelled
		return s.enqueueEvent(ctx, tx, outboxEntry{
			Subject:   events.SubjectInterviewCancelled,
			CompanyID: companyID,
			ActorID:   actorID,
			Payload:   interviewPayload(interview),
		})
	})
}

/* -------------------------------------------------------------------------- */
/* Projection maintenance                                                     */
/* -------------------------------------------------------------------------- */

// RefreshApplicationSnapshot updates the denormalized candidate name and job
// title for every round on an application.
//
// projection_at is the guard against out-of-order delivery. JetStream redelivers
// and does not promise order across publishers, so an older application event
// arriving after a newer one would otherwise overwrite a corrected name with the
// one it replaced. Comparing timestamps makes a late event a no-op instead, and
// makes a redelivery of the same event a no-op too.
func (s *Store) RefreshApplicationSnapshot(
	ctx context.Context, companyID, applicationID, candidateName, jobTitle string, occurredAt time.Time,
) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE interviews SET
			candidate_name = CASE WHEN $4 <> '' THEN $4 ELSE candidate_name END,
			job_title      = CASE WHEN $5 <> '' THEN $5 ELSE job_title END,
			projection_at  = $3
		WHERE company_id = $1
		  AND application_id = $2
		  AND (projection_at IS NULL OR projection_at < $3)`,
		companyID, applicationID, occurredAt, candidateName, jobTitle)
	if err != nil {
		return 0, fmt.Errorf("store: refresh interview snapshot: %w", err)
	}
	return tag.RowsAffected(), nil
}

// CancelOpenInterviews cancels the rounds still outstanding on an application.
//
// It is idempotent by predicate: only rows still in `scheduled` are touched, so
// a redelivered rejection finds nothing left to cancel and publishes nothing a
// second time. Rounds that already ran keep their outcome — a rejection does not
// unmake the conversation that produced it.
func (s *Store) CancelOpenInterviews(
	ctx context.Context, companyID, applicationID, reason string,
) (int64, error) {
	var cancelled int64

	err := s.InTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			UPDATE interviews SET
				status = 'cancelled',
				cancelled_at = now(),
				cancellation_reason = $3
			WHERE company_id = $1 AND application_id = $2 AND status = 'scheduled'
			RETURNING id`,
			companyID, applicationID, reason)
		if err != nil {
			return fmt.Errorf("store: cancel open interviews: %w", err)
		}

		ids := make([]string, 0, 4)
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return fmt.Errorf("store: scan cancelled interview: %w", err)
			}
			ids = append(ids, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return fmt.Errorf("store: read cancelled interviews: %w", err)
		}

		for _, id := range ids {
			interview, err := s.findInterviewTx(ctx, tx, companyID, id)
			if err != nil {
				return err
			}
			if err := s.enqueueEvent(ctx, tx, outboxEntry{
				Subject:   events.SubjectInterviewCancelled,
				CompanyID: companyID,
				// No actor: nobody pressed cancel. The application's own closure
				// did, and attributing it to a person would put the wrong name in
				// an audit trail.
				Payload: interviewPayload(interview),
			}); err != nil {
				return err
			}
		}

		cancelled = int64(len(ids))
		return nil
	})

	return cancelled, err
}

/* -------------------------------------------------------------------------- */
/* Event payload                                                              */
/* -------------------------------------------------------------------------- */

// interviewPayload is the shape every interview event carries.
//
// It is deliberately narrow: a consumer needs to identify the round and act on
// it — send an invite, remind a panel, count a loop — not to receive the notes a
// recruiter wrote about the candidate.
func interviewPayload(i domain.Interview) map[string]any {
	return map[string]any{
		"interviewId":     i.ID,
		"companyId":       i.CompanyID,
		"applicationId":   i.ApplicationID,
		"candidateId":     i.CandidateID,
		"candidateName":   i.CandidateName,
		"jobTitle":        i.JobTitle,
		"roundTitle":      i.RoundTitle,
		"roundType":       i.RoundType,
		"scheduledStart":  i.ScheduledStart,
		"durationMinutes": i.DurationMinutes,
		"format":          string(i.Format),
		"status":          string(i.Status),
		"panelMemberIds":  i.PanelMemberIDs,
	}
}
