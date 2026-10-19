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
	"github.com/karmarat/platform/services/offers/internal/domain"
)

const offerSelect = `
	SELECT id, company_id, application_id, candidate_id,
	       candidate_name, job_title, status,
	       designation, department_name, grade_level,
	       base_salary, sign_on_bonus, currency, pay_frequency, annual_bonus, equity_shares,
	       joining_date, reporting_manager, work_location, probation_period, notice_period,
	       benefits_summary, template_type, custom_fields, offer_letter_content, expires_at,
	       created_by, coalesce(submitted_by, ''), submitted_at,
	       coalesce(approved_by, ''), approved_at, self_approved,
	       coalesce(sent_by, ''), sent_at, responded_at, decline_reason,
	       created_at, updated_at
	FROM offers`

func scanOffer(row pgx.Row) (domain.Offer, error) {
	var o domain.Offer
	err := row.Scan(
		&o.ID, &o.CompanyID, &o.ApplicationID, &o.CandidateID,
		&o.CandidateName, &o.JobTitle, &o.Status,
		&o.Designation, &o.DepartmentName, &o.GradeLevel,
		&o.BaseSalary, &o.SignOnBonus, &o.Currency, &o.PayFrequency, &o.AnnualBonus, &o.EquityShares,
		&o.JoiningDate, &o.ReportingManager, &o.WorkLocation, &o.ProbationPeriod, &o.NoticePeriod,
		&o.BenefitsSummary, &o.TemplateType, &o.CustomFields, &o.OfferLetterContent, &o.ExpiresAt,
		&o.CreatedBy, &o.SubmittedBy, &o.SubmittedAt,
		&o.ApprovedBy, &o.ApprovedAt, &o.SelfApproved,
		&o.SentBy, &o.SentAt, &o.RespondedAt, &o.DeclineReason,
		&o.CreatedAt, &o.UpdatedAt,
	)
	return o, err
}

// OfferInput is a complete offer as a recruiter drafted it.
//
// Money arrives already converted: the API layer parses the portal's major units
// once, and nothing below this line sees anything but minor units.
type OfferInput struct {
	ApplicationID string
	CandidateID   string
	CandidateName string
	JobTitle      string

	Designation    string
	DepartmentName string
	GradeLevel     string

	Compensation domain.Compensation

	JoiningDate        time.Time
	ReportingManager   string
	WorkLocation       string
	ProbationPeriod    string
	NoticePeriod       string
	BenefitsSummary    string
	TemplateType       string
	CustomFields       []domain.CustomField
	OfferLetterContent string
	ExpiresAt          *time.Time
}

// CreateOffer records a new draft.
func (s *Store) CreateOffer(ctx context.Context, companyID, actorID string, in OfferInput) (domain.Offer, error) {
	id := idgen.New("ofr")

	customFields := in.CustomFields
	if customFields == nil {
		customFields = []domain.CustomField{}
	}

	var created domain.Offer
	err := s.InTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO offers (
				id, company_id, application_id, candidate_id, candidate_name, job_title, status,
				designation, department_name, grade_level,
				base_salary, sign_on_bonus, currency, pay_frequency, annual_bonus, equity_shares,
				joining_date, reporting_manager, work_location, probation_period, notice_period,
				benefits_summary, template_type, custom_fields, offer_letter_content, expires_at,
				created_by
			) VALUES (
				$1, $2, $3, $4, $5, $6, 'draft',
				$7, $8, $9,
				$10, $11, $12, $13, $14, $15,
				$16, $17, $18, $19, $20,
				$21, $22, $23, $24, $25,
				$26
			)`,
			id, companyID, in.ApplicationID, in.CandidateID, in.CandidateName, in.JobTitle,
			in.Designation, in.DepartmentName, in.GradeLevel,
			in.Compensation.BaseSalary, in.Compensation.SignOnBonus, in.Compensation.Currency,
			in.Compensation.PayFrequency, in.Compensation.AnnualBonus, in.Compensation.EquityShares,
			in.JoiningDate, in.ReportingManager, in.WorkLocation, in.ProbationPeriod, in.NoticePeriod,
			in.BenefitsSummary, in.TemplateType, customFields, in.OfferLetterContent, in.ExpiresAt,
			actorID,
		); err != nil {
			return fmt.Errorf("store: create offer: %w", err)
		}

		var err error
		created, err = s.findOfferTx(ctx, tx, companyID, id)
		return err
	})

	return created, err
}

// FindOffer resolves one offer within a tenant.
func (s *Store) FindOffer(ctx context.Context, companyID, id string) (domain.Offer, error) {
	return s.findOfferTx(ctx, nil, companyID, id)
}

func (s *Store) findOfferTx(ctx context.Context, tx pgx.Tx, companyID, id string) (domain.Offer, error) {
	offer, err := scanOffer(s.queryRow(ctx, tx, offerSelect+` WHERE id = $1 AND company_id = $2`, id, companyID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Offer{}, domain.ErrOfferNotFound
		}
		return domain.Offer{}, fmt.Errorf("store: find offer: %w", err)
	}
	return offer, nil
}

// lockOffer reads an offer for update within the caller's transaction.
//
// Every state change in this service goes through it. The row lock is what makes
// "check the current status, then write the next one" atomic: without it two
// concurrent sends both read `approved` and both dispatch a letter, and the
// candidate receives the offer twice.
func (s *Store) lockOffer(ctx context.Context, tx pgx.Tx, companyID, id string) (domain.Offer, error) {
	offer, err := scanOffer(tx.QueryRow(ctx,
		offerSelect+` WHERE id = $1 AND company_id = $2 FOR UPDATE`, id, companyID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Offer{}, domain.ErrOfferNotFound
		}
		return domain.Offer{}, fmt.Errorf("store: lock offer: %w", err)
	}
	return offer, nil
}

// ListFilter narrows a company's offer list.
type ListFilter struct {
	Status        string
	ApplicationID string
	CandidateID   string
	Limit         int
	// Cursor is the last id of the previous page; ids sort by creation time, so
	// keyset paging over them is stable while new offers are drafted.
	Cursor string
}

// Page is one page of results plus the cursor that continues it.
type Page struct {
	Offers     []domain.Offer
	NextCursor string
}

// ListOffers returns one page of a company's offers.
func (s *Store) ListOffers(ctx context.Context, companyID string, filter ListFilter) (Page, error) {
	where := []string{"company_id = $1"}
	args := []any{companyID}

	appendCondition := func(clause string, value any) {
		args = append(args, value)
		where = append(where, fmt.Sprintf(clause, len(args)))
	}

	if filter.Status != "" {
		appendCondition("status = $%d", filter.Status)
	}
	if filter.ApplicationID != "" {
		appendCondition("application_id = $%d", filter.ApplicationID)
	}
	if filter.CandidateID != "" {
		appendCondition("candidate_id = $%d", filter.CandidateID)
	}
	if filter.Cursor != "" {
		appendCondition("id < $%d", filter.Cursor)
	}

	limit := normalizeLimit(filter.Limit)
	args = append(args, limit+1)

	query := offerSelect +
		" WHERE " + strings.Join(where, " AND ") +
		" ORDER BY id DESC LIMIT $" + strconv.Itoa(len(args))

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return Page{}, fmt.Errorf("store: list offers: %w", err)
	}
	defer rows.Close()

	offers := make([]domain.Offer, 0, limit)
	for rows.Next() {
		offer, err := scanOffer(rows)
		if err != nil {
			return Page{}, fmt.Errorf("store: scan offer: %w", err)
		}
		offers = append(offers, offer)
	}
	if err := rows.Err(); err != nil {
		return Page{}, fmt.Errorf("store: read offers: %w", err)
	}

	page := Page{Offers: offers}
	// One row beyond the page was requested purely to learn whether another page
	// exists, which is cheaper than a second count query.
	if len(offers) > limit {
		page.Offers = offers[:limit]
		page.NextCursor = page.Offers[limit-1].ID
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

// OfferPatch carries the fields a recruiter may edit on a draft.
//
// The status is deliberately absent: moving an offer forward is submit, approve,
// send or respond, each of which records who did it. A PATCH that could set the
// status would be a way to mark an offer approved with nobody's name on it.
type OfferPatch struct {
	Designation    *string
	DepartmentName *string
	GradeLevel     *string

	// Replaced as a unit rather than field by field, because a base salary and a
	// currency that were never validated together is how an offer ends up
	// reading "¥175,000.00" after somebody edited only the currency.
	Compensation *domain.Compensation

	JoiningDate        *time.Time
	ReportingManager   *string
	WorkLocation       *string
	ProbationPeriod    *string
	NoticePeriod       *string
	BenefitsSummary    *string
	TemplateType       *string
	CustomFields       *[]domain.CustomField
	OfferLetterContent *string
	// ExpiresAt is a double pointer so "clear the expiry" is distinguishable
	// from "leave it alone": one is an explicit null in the request body, the
	// other is an absent key.
	ExpiresAt **time.Time
}

// UpdateOffer edits a draft offer.
func (s *Store) UpdateOffer(ctx context.Context, companyID, id string, patch OfferPatch) (domain.Offer, error) {
	var updated domain.Offer

	err := s.InTx(ctx, func(tx pgx.Tx) error {
		offer, err := s.lockOffer(ctx, tx, companyID, id)
		if err != nil {
			return err
		}
		if !offer.Status.Editable() {
			return domain.ErrNotEditable
		}

		set := []string{}
		args := []any{id, companyID}

		assign := func(column string, value any) {
			args = append(args, value)
			set = append(set, fmt.Sprintf("%s = $%d", column, len(args)))
		}

		if patch.Designation != nil {
			assign("designation", *patch.Designation)
		}
		if patch.DepartmentName != nil {
			assign("department_name", *patch.DepartmentName)
		}
		if patch.GradeLevel != nil {
			assign("grade_level", *patch.GradeLevel)
		}
		if patch.Compensation != nil {
			assign("base_salary", patch.Compensation.BaseSalary)
			assign("sign_on_bonus", patch.Compensation.SignOnBonus)
			assign("currency", patch.Compensation.Currency)
			assign("pay_frequency", patch.Compensation.PayFrequency)
			assign("annual_bonus", patch.Compensation.AnnualBonus)
			assign("equity_shares", patch.Compensation.EquityShares)
		}
		if patch.JoiningDate != nil {
			assign("joining_date", *patch.JoiningDate)
		}
		if patch.ReportingManager != nil {
			assign("reporting_manager", *patch.ReportingManager)
		}
		if patch.WorkLocation != nil {
			assign("work_location", *patch.WorkLocation)
		}
		if patch.ProbationPeriod != nil {
			assign("probation_period", *patch.ProbationPeriod)
		}
		if patch.NoticePeriod != nil {
			assign("notice_period", *patch.NoticePeriod)
		}
		if patch.BenefitsSummary != nil {
			assign("benefits_summary", *patch.BenefitsSummary)
		}
		if patch.TemplateType != nil {
			assign("template_type", *patch.TemplateType)
		}
		if patch.CustomFields != nil {
			assign("custom_fields", *patch.CustomFields)
		}
		if patch.OfferLetterContent != nil {
			assign("offer_letter_content", *patch.OfferLetterContent)
		}
		if patch.ExpiresAt != nil {
			assign("expires_at", *patch.ExpiresAt)
		}

		// An empty patch is not an error — the caller sent a body with nothing in
		// it — but an UPDATE with no SET clause is a syntax error, so skip it.
		if len(set) > 0 {
			if _, err := tx.Exec(ctx,
				`UPDATE offers SET `+strings.Join(set, ", ")+` WHERE id = $1 AND company_id = $2`,
				args...); err != nil {
				return fmt.Errorf("store: update offer: %w", err)
			}
		}

		updated, err = s.findOfferTx(ctx, tx, companyID, id)
		return err
	})

	return updated, err
}

// SubmitOffer sends a draft for approval.
func (s *Store) SubmitOffer(ctx context.Context, companyID, id, actorID string) (domain.Offer, error) {
	return s.transition(ctx, companyID, id, domain.StatusPendingApproval,
		func(_ domain.Offer) (string, []any, *outboxEntry, error) {
			return `UPDATE offers
			        SET status = 'pending_approval', submitted_by = $3, submitted_at = now()
			        WHERE id = $1 AND company_id = $2`,
				[]any{actorID}, nil, nil
		})
}

// ApproveOffer signs off on a package that is waiting for approval.
//
// selfApprove is the caller's explicit override of the separation-of-duties rule;
// domain.CanSelfApprove documents when it is honoured and why the exception
// exists at all.
func (s *Store) ApproveOffer(
	ctx context.Context, companyID, id, actorID string, selfApprove bool,
) (domain.Offer, error) {
	return s.transition(ctx, companyID, id, domain.StatusApproved,
		func(offer domain.Offer) (string, []any, *outboxEntry, error) {
			if !domain.CanSelfApprove(offer.CreatedBy, offer.SubmittedBy, actorID, selfApprove) {
				return "", nil, nil, domain.ErrSelfApproval
			}
			// True whenever the approver wrote or submitted the package, matching
			// what CanSelfApprove actually measures. Recording only the submitter
			// case left an author's self-approval looking like an ordinary one.
			selfApproved := actorID != "" &&
				(actorID == offer.CreatedBy || actorID == offer.SubmittedBy)

			return `UPDATE offers
			        SET status = 'approved', approved_by = $3, approved_at = now(), self_approved = $4
			        WHERE id = $1 AND company_id = $2`,
				[]any{actorID, selfApproved}, nil, nil
		})
}

// SendOffer dispatches an approved offer to the candidate.
//
// The bool reports a replay: the same Idempotency-Key on an offer that was
// already sent answers with the offer as it stands rather than a 409, because a
// client retrying a request whose response it never saw has done nothing wrong.
func (s *Store) SendOffer(
	ctx context.Context, companyID, id, actorID, idempotencyKey string,
) (domain.Offer, bool, error) {
	var (
		sent     domain.Offer
		replayed bool
	)

	err := s.InTx(ctx, func(tx pgx.Tx) error {
		offer, err := s.lockOffer(ctx, tx, companyID, id)
		if err != nil {
			return err
		}

		// Checked before the transition, so a retry of a send that succeeded is
		// recognized as the same request rather than rejected for being a second
		// send — which is exactly the case the key exists to cover.
		if idempotencyKey != "" && offer.Status == domain.StatusSent &&
			offer.SentAt != nil && sameKey(ctx, tx, companyID, id, idempotencyKey) {
			sent, replayed = offer, true
			return nil
		}

		if err := domain.Transition(offer.Status, domain.StatusSent); err != nil {
			return err
		}
		if offer.Expired(time.Now()) {
			return domain.ErrExpired
		}

		if _, err := tx.Exec(ctx, `
			UPDATE offers
			SET status = 'sent', sent_by = $3, sent_at = now(), send_idempotency_key = $4
			WHERE id = $1 AND company_id = $2`,
			id, companyID, actorID, nullable(idempotencyKey)); err != nil {
			if isUniqueViolation(err) {
				return domain.ErrIdempotencyConflict
			}
			return fmt.Errorf("store: send offer: %w", err)
		}

		sent, err = s.findOfferTx(ctx, tx, companyID, id)
		if err != nil {
			return err
		}

		return s.enqueueEvent(ctx, tx, outboxEntry{
			Subject:   events.SubjectOfferSent,
			CompanyID: companyID,
			ActorID:   actorID,
			Payload:   offerPayload(sent),
		})
	})

	return sent, replayed, err
}

// sameKey reports whether the stored send key matches the one presented.
//
// A separate read rather than a field on the offer: the key is an implementation
// detail of the retry protocol, not part of the offer anybody looks at, and it
// has no business travelling out through the domain entity to a view.
func sameKey(ctx context.Context, tx pgx.Tx, companyID, id, key string) bool {
	var stored *string
	if err := tx.QueryRow(ctx,
		`SELECT send_idempotency_key FROM offers WHERE id = $1 AND company_id = $2`,
		id, companyID).Scan(&stored); err != nil {
		return false
	}
	return stored != nil && *stored == key
}

// RespondToOffer records the candidate's answer.
func (s *Store) RespondToOffer(
	ctx context.Context, companyID, id string, outcome domain.Status, reason, actorID string,
) (domain.Offer, error) {
	subject := events.SubjectOfferAccepted
	if outcome == domain.StatusDeclined {
		subject = events.SubjectOfferDeclined
	}

	return s.transition(ctx, companyID, id, outcome,
		func(offer domain.Offer) (string, []any, *outboxEntry, error) {
			return `UPDATE offers
			        SET status = $3, responded_at = now(), decline_reason = $4
			        WHERE id = $1 AND company_id = $2`,
				[]any{outcome, reason},
				&outboxEntry{Subject: subject, CompanyID: companyID, ActorID: actorID},
				nil
		})
}

// transition is the one path every status change takes.
//
// It locks the row, asks the lifecycle whether the move is legal, runs the
// caller's own guard, writes, and enqueues the event — all inside one
// transaction, so an offer never changes state without its announcement or
// announces a change that rolled back.
func (s *Store) transition(
	ctx context.Context,
	companyID, id string,
	to domain.Status,
	prepare func(domain.Offer) (string, []any, *outboxEntry, error),
) (domain.Offer, error) {
	var result domain.Offer

	err := s.InTx(ctx, func(tx pgx.Tx) error {
		offer, err := s.lockOffer(ctx, tx, companyID, id)
		if err != nil {
			return err
		}
		if err := domain.Transition(offer.Status, to); err != nil {
			return err
		}

		// One expiry check for every transition, rather than one per endpoint.
		//
		// Send and respond each carried their own; submit and approve did not, so
		// in the window between a deadline passing and the sweeper's next tick an
		// offer could still be submitted and signed off. Approving a package the
		// candidate can no longer accept is not a small inconsistency: it records
		// a decision about an offer that has lapsed.
		//
		// The sweeper still writes the status. This only refuses to move a row
		// the clock has already overtaken.
		if offer.Expired(time.Now()) {
			return domain.ErrExpired
		}

		query, extra, event, err := prepare(offer)
		if err != nil {
			return err
		}

		args := append([]any{id, companyID}, extra...)
		if _, err := tx.Exec(ctx, query, args...); err != nil {
			return fmt.Errorf("store: transition offer to %s: %w", to, err)
		}

		result, err = s.findOfferTx(ctx, tx, companyID, id)
		if err != nil {
			return err
		}

		if event != nil {
			event.Payload = offerPayload(result)
			return s.enqueueEvent(ctx, tx, *event)
		}
		return nil
	})

	return result, err
}

// DeleteOffer removes an offer the candidate never saw.
//
// An offer that was sent is the record of what the company told somebody; it is
// closed by a response or by expiry, never erased. The guard is sent_at rather
// than the status, because an expired offer that had already gone out is still a
// letter the candidate is holding.
func (s *Store) DeleteOffer(ctx context.Context, companyID, id string) error {
	return s.InTx(ctx, func(tx pgx.Tx) error {
		offer, err := s.lockOffer(ctx, tx, companyID, id)
		if err != nil {
			return err
		}
		if offer.SentAt != nil {
			return domain.ErrNotDeletable
		}

		if _, err := tx.Exec(ctx, `DELETE FROM offers WHERE id = $1 AND company_id = $2`, id, companyID); err != nil {
			return fmt.Errorf("store: delete offer: %w", err)
		}
		return nil
	})
}

/* -------------------------------------------------------------------------- */
/* Expiry and projections                                                     */
/* -------------------------------------------------------------------------- */

// ExpireDueOffers marks every outstanding offer whose deadline has passed.
//
// Platform-wide by design: expiry is time passing, not a tenant acting, and
// there is no request whose company this could be scoped to. It is the one query
// in this service without a company predicate, which is why it is here, alone,
// and not reachable from any handler.
func (s *Store) ExpireDueOffers(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE offers
		SET status = 'expired'
		WHERE expires_at IS NOT NULL
		  AND expires_at <= now()
		  AND status IN ('draft', 'pending_approval', 'approved', 'sent')`)
	if err != nil {
		return 0, fmt.Errorf("store: expire due offers: %w", err)
	}
	return tag.RowsAffected(), nil
}

// ExpireOffersForApplication closes the offers on an application that ended.
//
// A candidate who withdrew, or who was rejected, must not be left holding a live
// offer: the two records would disagree about whether the company still wants to
// hire them, and the offer is the one they would act on.
func (s *Store) ExpireOffersForApplication(ctx context.Context, companyID, applicationID string) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE offers
		SET status = 'expired'
		WHERE company_id = $1
		  AND application_id = $2
		  AND status IN ('draft', 'pending_approval', 'approved', 'sent')`,
		companyID, applicationID)
	if err != nil {
		return 0, fmt.Errorf("store: expire offers for application: %w", err)
	}
	return tag.RowsAffected(), nil
}

// SyncApplicationSnapshot refreshes the denormalized candidate name and job
// title an offer list renders.
//
// Only non-empty values overwrite: an event that happens not to carry a job
// title must not blank the one already on the row, which would turn a working
// list into a column of empty cells.
func (s *Store) SyncApplicationSnapshot(
	ctx context.Context, companyID, applicationID, candidateName, jobTitle string,
) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE offers
		SET candidate_name = coalesce(nullif($3, ''), candidate_name),
		    job_title      = coalesce(nullif($4, ''), job_title)
		WHERE company_id = $1
		  AND application_id = $2
		  AND (candidate_name IS DISTINCT FROM coalesce(nullif($3, ''), candidate_name)
		    OR job_title      IS DISTINCT FROM coalesce(nullif($4, ''), job_title))`,
		companyID, applicationID, candidateName, jobTitle)
	if err != nil {
		return 0, fmt.Errorf("store: sync application snapshot: %w", err)
	}
	return tag.RowsAffected(), nil
}

// offerPayload is what this service tells the rest of the platform.
//
// It deliberately carries no compensation. A subscriber cannot evaluate
// `offers.view_compensation` — the bus has no principal — so a salary on an
// event is a salary in every consumer's database, readable by anyone who can
// read that consumer. Whoever needs the numbers asks this service, which can
// check the permission.
func offerPayload(o domain.Offer) map[string]any {
	payload := map[string]any{
		"offerId":       o.ID,
		"companyId":     o.CompanyID,
		"applicationId": o.ApplicationID,
		"candidateId":   o.CandidateID,
		"candidateName": o.CandidateName,
		"jobTitle":      o.JobTitle,
		"designation":   o.Designation,
		"status":        string(o.Status),
		"joiningDate":   o.JoiningDate.Format(time.DateOnly),
	}
	if o.SentAt != nil {
		payload["sentAt"] = *o.SentAt
	}
	if o.RespondedAt != nil {
		payload["respondedAt"] = *o.RespondedAt
	}
	if o.ExpiresAt != nil {
		payload["expiresAt"] = *o.ExpiresAt
	}
	if o.DeclineReason != "" {
		payload["declineReason"] = o.DeclineReason
	}
	return payload
}
