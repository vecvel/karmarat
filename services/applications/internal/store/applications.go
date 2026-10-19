package store

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/karmarat/platform/packages/goshared/events"
	"github.com/karmarat/platform/packages/goshared/idgen"
	"github.com/karmarat/platform/services/applications/internal/domain"
)

// The stage and reason tables live in this service's own database, so resolving
// the display name of a stage is a join rather than a call — the no-cross-service-
// join rule is about other services' data, and this is ours.
const applicationSelect = `
	SELECT a.id, a.company_id, a.job_id, a.candidate_id,
	       a.candidate_name, a.candidate_email, a.job_title, a.company_name,
	       a.answers, coalesce(a.resume_key, ''), a.source,
	       a.stage_id, a.status, a.rating,
	       coalesce(a.rejection_reason_id, ''), coalesce(a.rejection_note, ''),
	       coalesce(a.rejected_by, ''), a.rejected_at, a.withdrawn_at, a.job_closed_at,
	       a.submitted_at, a.created_at, a.updated_at,
	       s.name, s.type, s.color, coalesce(r.label, '')
	FROM applications a
	JOIN pipeline_stages s ON s.id = a.stage_id
	LEFT JOIN rejection_reasons r ON r.id = a.rejection_reason_id`

func scanApplication(row pgx.Row) (domain.Application, error) {
	var a domain.Application
	err := row.Scan(
		&a.ID, &a.CompanyID, &a.JobID, &a.CandidateID,
		&a.CandidateName, &a.CandidateEmail, &a.JobTitle, &a.CompanyName,
		&a.Answers, &a.ResumeKey, &a.Source,
		&a.StageID, &a.Status, &a.Rating,
		&a.RejectionReasonID, &a.RejectionNote,
		&a.RejectedBy, &a.RejectedAt, &a.WithdrawnAt, &a.JobClosedAt,
		&a.SubmittedAt, &a.CreatedAt, &a.UpdatedAt,
		&a.StageName, &a.StageType, &a.StageColor, &a.RejectionReasonLabel,
	)
	return a, err
}

// CreateApplicationInput is one candidate's submission to one job.
type CreateApplicationInput struct {
	CompanyID      string
	JobID          string
	CandidateID    string
	CandidateName  string
	CandidateEmail string
	JobTitle       string
	CompanyName    string
	Answers        map[string]any
	ResumeKey      string
	Source         domain.Source
}

// CreateApplication records a submission, or reports that one already exists.
//
// The duplicate is caught by the unique index rather than by a prior SELECT:
// two submits racing each other both pass a check-then-insert, and one of them
// has to lose at the point the row is written.
func (s *Store) CreateApplication(ctx context.Context, in CreateApplicationInput) (domain.Application, error) {
	var created domain.Application

	err := s.InTx(ctx, func(tx pgx.Tx) error {
		stage, err := s.FirstStage(ctx, tx, in.CompanyID)
		if err != nil {
			return err
		}

		id := idgen.New("app")
		answers := in.Answers
		if answers == nil {
			answers = map[string]any{}
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO applications (
				id, company_id, job_id, candidate_id,
				candidate_name, candidate_email, job_title, company_name,
				answers, resume_key, source, stage_id, status
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, 'active')`,
			id, in.CompanyID, in.JobID, in.CandidateID,
			in.CandidateName, in.CandidateEmail, in.JobTitle, in.CompanyName,
			answers, nullable(in.ResumeKey), in.Source, stage.ID,
		); err != nil {
			if isUniqueViolation(err) {
				return domain.ErrAlreadyApplied
			}
			return fmt.Errorf("store: create application: %w", err)
		}

		if err := s.appendEvent(ctx, tx, eventInput{
			CompanyID:     in.CompanyID,
			ApplicationID: id,
			Type:          domain.EventSubmitted,
			ActorID:       in.CandidateID,
			ActorType:     "candidate",
			ToStageID:     stage.ID,
		}); err != nil {
			return err
		}

		created, err = s.findApplicationTx(ctx, tx, in.CompanyID, id)
		if err != nil {
			return err
		}

		return s.enqueueEvent(ctx, tx, outboxEntry{
			Subject:   events.SubjectApplicationSubmitted,
			CompanyID: in.CompanyID,
			ActorID:   in.CandidateID,
			Payload:   applicationPayload(created),
		})
	})

	return created, err
}

// FindApplication resolves one application within a tenant.
func (s *Store) FindApplication(ctx context.Context, companyID, id string) (domain.Application, error) {
	return s.findApplicationTx(ctx, nil, companyID, id)
}

func (s *Store) findApplicationTx(ctx context.Context, tx pgx.Tx, companyID, id string) (domain.Application, error) {
	query := applicationSelect + ` WHERE a.id = $1 AND a.company_id = $2`

	application, err := scanApplication(s.queryRow(ctx, tx, query, id, companyID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Application{}, domain.ErrApplicationNotFound
		}
		return domain.Application{}, fmt.Errorf("store: find application: %w", err)
	}
	return application, nil
}

// FindApplicationForCandidate resolves an application the candidate owns.
//
// The filter is the authenticated account id, not a company: a candidate is not
// tenant-scoped, and their own account is the only boundary that means anything
// on the jobs portal.
func (s *Store) FindApplicationForCandidate(ctx context.Context, candidateID, id string) (domain.Application, error) {
	query := applicationSelect + ` WHERE a.id = $1 AND a.candidate_id = $2`

	application, err := scanApplication(s.pool.QueryRow(ctx, query, id, candidateID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Application{}, domain.ErrApplicationNotFound
		}
		return domain.Application{}, fmt.Errorf("store: find candidate application: %w", err)
	}
	return application, nil
}

// ListFilter narrows a pipeline listing.
type ListFilter struct {
	JobID    string
	StageID  string
	Status   string
	Source   string
	Query    string
	DateFrom *time.Time
	DateTo   *time.Time
	Limit    int
	// Cursor is the last id of the previous page; ids sort by creation time, so
	// keyset paging over them is stable while new applications arrive.
	Cursor string
}

// Page is one page of results plus the cursor that continues it.
type Page struct {
	Applications []domain.Application
	NextCursor   string
}

// ListApplications returns one page of a company's pipeline.
func (s *Store) ListApplications(ctx context.Context, companyID string, filter ListFilter) (Page, error) {
	where := []string{"a.company_id = $1"}
	args := []any{companyID}

	appendCondition := func(clause string, value any) {
		args = append(args, value)
		where = append(where, fmt.Sprintf(clause, len(args)))
	}

	if filter.JobID != "" {
		appendCondition("a.job_id = $%d", filter.JobID)
	}
	if filter.StageID != "" {
		appendCondition("a.stage_id = $%d", filter.StageID)
	}
	if filter.Status != "" {
		appendCondition("a.status = $%d", filter.Status)
	}
	if filter.Source != "" {
		appendCondition("a.source = $%d", filter.Source)
	}
	if filter.DateFrom != nil {
		appendCondition("a.submitted_at >= $%d", *filter.DateFrom)
	}
	if filter.DateTo != nil {
		appendCondition("a.submitted_at <= $%d", *filter.DateTo)
	}
	if trimmed := strings.TrimSpace(filter.Query); trimmed != "" {
		// Parameterised, so the pattern is data: a caller cannot smuggle SQL in
		// through the search box, only a wider LIKE.
		args = append(args, "%"+trimmed+"%")
		where = append(where, fmt.Sprintf(
			"(a.candidate_name ILIKE $%d OR a.candidate_email ILIKE $%[1]d OR a.job_title ILIKE $%[1]d)", len(args)))
	}
	if filter.Cursor != "" {
		appendCondition("a.id < $%d", filter.Cursor)
	}

	limit := normalizeLimit(filter.Limit)
	args = append(args, limit+1)

	query := applicationSelect +
		" WHERE " + strings.Join(where, " AND ") +
		" ORDER BY a.id DESC LIMIT $" + strconv.Itoa(len(args))

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return Page{}, fmt.Errorf("store: list applications: %w", err)
	}
	defer rows.Close()

	return collectPage(rows, limit)
}

// ListApplicationsForCandidate returns one page of the candidate's own history.
func (s *Store) ListApplicationsForCandidate(ctx context.Context, candidateID string, limit int, cursor string) (Page, error) {
	where := []string{"a.candidate_id = $1"}
	args := []any{candidateID}

	if cursor != "" {
		args = append(args, cursor)
		where = append(where, fmt.Sprintf("a.id < $%d", len(args)))
	}

	limit = normalizeLimit(limit)
	args = append(args, limit+1)

	query := applicationSelect +
		" WHERE " + strings.Join(where, " AND ") +
		" ORDER BY a.id DESC LIMIT $" + strconv.Itoa(len(args))

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return Page{}, fmt.Errorf("store: list candidate applications: %w", err)
	}
	defer rows.Close()

	return collectPage(rows, limit)
}

func collectPage(rows pgx.Rows, limit int) (Page, error) {
	applications := make([]domain.Application, 0, limit)
	for rows.Next() {
		application, err := scanApplication(rows)
		if err != nil {
			return Page{}, fmt.Errorf("store: scan application: %w", err)
		}
		applications = append(applications, application)
	}
	if err := rows.Err(); err != nil {
		return Page{}, fmt.Errorf("store: read applications: %w", err)
	}

	page := Page{Applications: applications}
	// One row beyond the page was requested purely to learn whether another page
	// exists, which is cheaper than a second count query.
	if len(applications) > limit {
		page.Applications = applications[:limit]
		page.NextCursor = page.Applications[limit-1].ID
	}
	return page, nil
}

func normalizeLimit(limit int) int {
	switch {
	case limit <= 0:
		return 25
	case limit > 100:
		return 100
	default:
		return limit
	}
}

// ApplicationPatch carries the fields a recruiter may edit directly.
//
// The stage is deliberately absent: moving an application is `advance`, which
// records who moved it. Allowing a plain PATCH to set the stage would be a way
// to move a candidate without leaving a trace.
type ApplicationPatch struct {
	Source    *string
	Rating    *int
	ResumeKey *string
}

// UpdateApplication applies a patch to one of a company's applications.
func (s *Store) UpdateApplication(ctx context.Context, companyID, id string, patch ApplicationPatch, actorID string) (domain.Application, error) {
	var updated domain.Application

	err := s.InTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE applications SET
				source     = coalesce($3, source),
				rating     = coalesce($4, rating),
				resume_key = coalesce($5, resume_key)
			WHERE id = $1 AND company_id = $2`,
			id, companyID, patch.Source, patch.Rating, patch.ResumeKey)
		if err != nil {
			return fmt.Errorf("store: update application: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return domain.ErrApplicationNotFound
		}

		if err := s.appendEvent(ctx, tx, eventInput{
			CompanyID:     companyID,
			ApplicationID: id,
			Type:          domain.EventUpdated,
			ActorID:       actorID,
			ActorType:     "company",
		}); err != nil {
			return err
		}

		updated, err = s.findApplicationTx(ctx, tx, companyID, id)
		return err
	})

	return updated, err
}

// AdvanceApplication moves an application to another stage.
//
// The move, the history entry and the outbound event are one transaction: a
// pipeline that shows a candidate in Offer while the audit trail and the
// notification say otherwise is worse than a failed request.
func (s *Store) AdvanceApplication(ctx context.Context, companyID, id, stageID, actorID, note string) (domain.Application, error) {
	var moved domain.Application

	err := s.InTx(ctx, func(tx pgx.Tx) error {
		current, err := s.lockApplication(ctx, tx, companyID, id)
		if err != nil {
			return err
		}

		target, err := s.FindStage(ctx, tx, companyID, stageID)
		if err != nil {
			return err
		}

		status := domain.StatusForStage(current.Status, target)
		// Moving out of a rejection clears the rejection with it; leaving the
		// reason behind would report the candidate as rejected forever.
		clearRejection := status != domain.StatusRejected

		if _, err := tx.Exec(ctx, `
			UPDATE applications SET
				stage_id            = $3,
				status              = $4,
				rejection_reason_id = CASE WHEN $5 THEN NULL ELSE rejection_reason_id END,
				rejection_note      = CASE WHEN $5 THEN NULL ELSE rejection_note END,
				rejected_by         = CASE WHEN $5 THEN NULL ELSE rejected_by END,
				rejected_at         = CASE WHEN $5 THEN NULL ELSE rejected_at END
			WHERE id = $1 AND company_id = $2`,
			id, companyID, target.ID, status, clearRejection); err != nil {
			return fmt.Errorf("store: advance application: %w", err)
		}

		eventType := domain.EventStageChanged
		if status == domain.StatusHired {
			eventType = domain.EventHired
		}

		if err := s.appendEvent(ctx, tx, eventInput{
			CompanyID:     companyID,
			ApplicationID: id,
			Type:          eventType,
			ActorID:       actorID,
			ActorType:     "company",
			FromStageID:   current.StageID,
			ToStageID:     target.ID,
			Note:          note,
		}); err != nil {
			return err
		}

		moved, err = s.findApplicationTx(ctx, tx, companyID, id)
		if err != nil {
			return err
		}

		payload := applicationPayload(moved)
		payload["fromStageId"] = current.StageID
		payload["fromStageName"] = current.StageName
		payload["note"] = note

		if err := s.enqueueEvent(ctx, tx, outboxEntry{
			Subject:   events.SubjectApplicationStageChanged,
			CompanyID: companyID,
			ActorID:   actorID,
			Payload:   payload,
		}); err != nil {
			return err
		}

		if status == domain.StatusHired {
			return s.enqueueEvent(ctx, tx, outboxEntry{
				Subject:   events.SubjectApplicationHired,
				CompanyID: companyID,
				ActorID:   actorID,
				Payload:   payload,
			})
		}
		return nil
	})

	return moved, err
}

// RejectApplication closes an application with a recorded reason.
func (s *Store) RejectApplication(ctx context.Context, companyID, id, reasonID, note, actorID string) (domain.Application, error) {
	var rejected domain.Application

	err := s.InTx(ctx, func(tx pgx.Tx) error {
		current, err := s.lockApplication(ctx, tx, companyID, id)
		if err != nil {
			return err
		}
		if current.Status == domain.StatusRejected {
			return domain.ErrAlreadyClosed
		}

		reason, err := s.FindRejectionReason(ctx, tx, companyID, reasonID)
		if err != nil {
			return err
		}
		if !reason.IsActive {
			return domain.ErrReasonInactive
		}

		// A rejection belongs in the stage typed "rejected" when the company has
		// one, so the board and the status cannot disagree. A company that
		// deleted that stage keeps the application where it is; the status is
		// what the candidate is told either way.
		stageID := current.StageID
		if terminal, err := s.findStageByType(ctx, tx, companyID, domain.StageRejected); err == nil {
			stageID = terminal.ID
		} else if !errors.Is(err, domain.ErrStageNotFound) {
			return err
		}

		if _, err := tx.Exec(ctx, `
			UPDATE applications SET
				status              = 'rejected',
				stage_id            = $3,
				rejection_reason_id = $4,
				rejection_note      = $5,
				rejected_by         = $6,
				rejected_at         = now()
			WHERE id = $1 AND company_id = $2`,
			id, companyID, stageID, reason.ID, nullable(note), nullable(actorID)); err != nil {
			return fmt.Errorf("store: reject application: %w", err)
		}

		if err := s.appendEvent(ctx, tx, eventInput{
			CompanyID:     companyID,
			ApplicationID: id,
			Type:          domain.EventRejected,
			ActorID:       actorID,
			ActorType:     "company",
			FromStageID:   current.StageID,
			ToStageID:     stageID,
			ReasonID:      reason.ID,
			Note:          note,
		}); err != nil {
			return err
		}

		rejected, err = s.findApplicationTx(ctx, tx, companyID, id)
		if err != nil {
			return err
		}

		payload := applicationPayload(rejected)
		payload["reasonId"] = reason.ID
		payload["reasonLabel"] = reason.Label
		// The internal note is deliberately not published: consumers fan out to
		// candidate-facing surfaces, and the note is for the company only.

		return s.enqueueEvent(ctx, tx, outboxEntry{
			Subject:   events.SubjectApplicationRejected,
			CompanyID: companyID,
			ActorID:   actorID,
			Payload:   payload,
		})
	})

	return rejected, err
}

// WithdrawApplication lets a candidate retract their own application.
func (s *Store) WithdrawApplication(ctx context.Context, candidateID, id string) (domain.Application, error) {
	var withdrawn domain.Application

	err := s.InTx(ctx, func(tx pgx.Tx) error {
		var companyID string
		var status domain.Status
		var stageID string

		err := tx.QueryRow(ctx,
			`SELECT company_id, status, stage_id FROM applications
			 WHERE id = $1 AND candidate_id = $2 FOR UPDATE`,
			id, candidateID).Scan(&companyID, &status, &stageID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return domain.ErrApplicationNotFound
			}
			return fmt.Errorf("store: lock candidate application: %w", err)
		}

		if !domain.CanWithdraw(status) {
			return domain.ErrNotWithdrawable
		}

		if _, err := tx.Exec(ctx, `
			UPDATE applications SET status = 'withdrawn', withdrawn_at = now()
			WHERE id = $1 AND candidate_id = $2`, id, candidateID); err != nil {
			return fmt.Errorf("store: withdraw application: %w", err)
		}

		if err := s.appendEvent(ctx, tx, eventInput{
			CompanyID:     companyID,
			ApplicationID: id,
			Type:          domain.EventWithdrawn,
			ActorID:       candidateID,
			ActorType:     "candidate",
			FromStageID:   stageID,
		}); err != nil {
			return err
		}

		withdrawn, err = s.findApplicationTx(ctx, tx, companyID, id)
		if err != nil {
			return err
		}

		return s.enqueueEvent(ctx, tx, outboxEntry{
			Subject:   events.SubjectApplicationWithdrawn,
			CompanyID: companyID,
			ActorID:   candidateID,
			Payload:   applicationPayload(withdrawn),
		})
	})

	return withdrawn, err
}

// DeleteApplication removes an application and its history.
func (s *Store) DeleteApplication(ctx context.Context, companyID, id string) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM applications WHERE id = $1 AND company_id = $2`, id, companyID)
	if err != nil {
		return fmt.Errorf("store: delete application: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrApplicationNotFound
	}
	return nil
}

// MarkJobClosed flags the applications still in flight on a closed requisition.
//
// It is idempotent by predicate — a redelivered event finds nothing left to
// mark — which is what lets the consumer be replayed safely.
func (s *Store) MarkJobClosed(ctx context.Context, companyID, jobID string, closedAt time.Time) (int64, error) {
	var affected int64

	err := s.InTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			UPDATE applications SET job_closed_at = $3
			WHERE company_id = $1 AND job_id = $2
			  AND job_closed_at IS NULL AND status = 'active'
			RETURNING id, stage_id`, companyID, jobID, closedAt)
		if err != nil {
			return fmt.Errorf("store: mark job closed: %w", err)
		}

		type affectedRow struct{ id, stageID string }
		var touched []affectedRow
		for rows.Next() {
			var row affectedRow
			if err := rows.Scan(&row.id, &row.stageID); err != nil {
				rows.Close()
				return fmt.Errorf("store: scan closed application: %w", err)
			}
			touched = append(touched, row)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return fmt.Errorf("store: mark job closed: %w", err)
		}

		for _, row := range touched {
			if err := s.appendEvent(ctx, tx, eventInput{
				CompanyID:     companyID,
				ApplicationID: row.id,
				Type:          domain.EventJobClosed,
				ActorType:     "system",
				FromStageID:   row.stageID,
				Note:          "The requisition was closed.",
			}); err != nil {
				return err
			}
		}

		affected = int64(len(touched))
		return nil
	})

	return affected, err
}

// ListApplicationEvents returns an application's history, oldest first.
func (s *Store) ListApplicationEvents(ctx context.Context, companyID, applicationID string) ([]domain.Event, error) {
	query := `
		SELECT e.id, e.application_id, e.type, coalesce(e.actor_id, ''), e.actor_type,
		       coalesce(e.from_stage_id, ''), coalesce(e.to_stage_id, ''),
		       coalesce(e.reason_id, ''), coalesce(e.note, ''), e.created_at,
		       coalesce(f.name, ''), coalesce(t.name, '')
		FROM application_events e
		LEFT JOIN pipeline_stages f ON f.id = e.from_stage_id
		LEFT JOIN pipeline_stages t ON t.id = e.to_stage_id
		WHERE e.application_id = $1 AND e.company_id = $2
		ORDER BY e.created_at, e.id`

	rows, err := s.pool.Query(ctx, query, applicationID, companyID)
	if err != nil {
		return nil, fmt.Errorf("store: list application events: %w", err)
	}
	defer rows.Close()

	history := make([]domain.Event, 0, 8)
	for rows.Next() {
		var e domain.Event
		if err := rows.Scan(&e.ID, &e.ApplicationID, &e.Type, &e.ActorID, &e.ActorType,
			&e.FromStageID, &e.ToStageID, &e.ReasonID, &e.Note, &e.CreatedAt,
			&e.FromStageName, &e.ToStageName); err != nil {
			return nil, fmt.Errorf("store: scan application event: %w", err)
		}
		history = append(history, e)
	}
	return history, rows.Err()
}

// lockApplication reads an application FOR UPDATE so concurrent moves serialize.
func (s *Store) lockApplication(ctx context.Context, tx pgx.Tx, companyID, id string) (domain.Application, error) {
	var current domain.Application

	err := tx.QueryRow(ctx, `
		SELECT a.id, a.stage_id, a.status, s.name
		FROM applications a
		JOIN pipeline_stages s ON s.id = a.stage_id
		WHERE a.id = $1 AND a.company_id = $2
		FOR UPDATE OF a`, id, companyID).
		Scan(&current.ID, &current.StageID, &current.Status, &current.StageName)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Application{}, domain.ErrApplicationNotFound
		}
		return domain.Application{}, fmt.Errorf("store: lock application: %w", err)
	}

	current.CompanyID = companyID
	return current, nil
}

func (s *Store) findStageByType(ctx context.Context, tx pgx.Tx, companyID string, stageType domain.StageType) (domain.Stage, error) {
	query := `SELECT ` + stageColumns + `
		FROM pipeline_stages WHERE company_id = $1 AND type = $2 ORDER BY sort_order, id LIMIT 1`

	stage, err := scanStage(s.queryRow(ctx, tx, query, companyID, stageType))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Stage{}, domain.ErrStageNotFound
		}
		return domain.Stage{}, fmt.Errorf("store: find stage by type: %w", err)
	}
	return stage, nil
}

// eventInput is one entry for the immutable history table.
type eventInput struct {
	CompanyID     string
	ApplicationID string
	Type          domain.EventType
	ActorID       string
	ActorType     string
	FromStageID   string
	ToStageID     string
	ReasonID      string
	Note          string
}

func (s *Store) appendEvent(ctx context.Context, tx pgx.Tx, in eventInput) error {
	if in.ActorType == "" {
		in.ActorType = "system"
	}

	_, err := s.exec(ctx, tx, `
		INSERT INTO application_events
			(id, company_id, application_id, type, actor_id, actor_type,
			 from_stage_id, to_stage_id, reason_id, note)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		idgen.New("aev"), in.CompanyID, in.ApplicationID, in.Type,
		nullable(in.ActorID), in.ActorType,
		nullable(in.FromStageID), nullable(in.ToStageID), nullable(in.ReasonID), nullable(in.Note))
	if err != nil {
		return fmt.Errorf("store: append application event: %w", err)
	}
	return nil
}

// applicationPayload is the shape every application event carries.
//
// It is deliberately narrow: consumers need to identify the application and
// react to it, not to receive a copy of the answers a candidate submitted.
func applicationPayload(a domain.Application) map[string]any {
	return map[string]any{
		"applicationId":  a.ID,
		"companyId":      a.CompanyID,
		"jobId":          a.JobID,
		"jobTitle":       a.JobTitle,
		"candidateId":    a.CandidateID,
		"candidateName":  a.CandidateName,
		"candidateEmail": a.CandidateEmail,
		"stageId":        a.StageID,
		"stageName":      a.StageName,
		"stageType":      string(a.StageType),
		"status":         string(a.Status),
		"source":         string(a.Source),
		"submittedAt":    a.SubmittedAt,
	}
}
