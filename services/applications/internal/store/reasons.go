package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/karmarat/platform/packages/goshared/idgen"
	"github.com/karmarat/platform/services/applications/internal/domain"
)

const reasonColumns = `id, company_id, label, sort_order, is_active, created_at, updated_at`

func scanReason(row pgx.Row) (domain.RejectionReason, error) {
	var r domain.RejectionReason
	err := row.Scan(&r.ID, &r.CompanyID, &r.Label, &r.Order, &r.IsActive, &r.CreatedAt, &r.UpdatedAt)
	return r, err
}

// ListRejectionReasons returns a company's reasons, optionally only the active
// ones — the reject form offers active reasons, the pipeline view still has to
// render the retired reason an old rejection cites.
func (s *Store) ListRejectionReasons(ctx context.Context, companyID string, activeOnly bool) ([]domain.RejectionReason, error) {
	query := `SELECT ` + reasonColumns + `
		FROM rejection_reasons
		WHERE company_id = $1 AND ($2 = false OR is_active)
		ORDER BY sort_order, id`

	rows, err := s.pool.Query(ctx, query, companyID, activeOnly)
	if err != nil {
		return nil, fmt.Errorf("store: list rejection reasons: %w", err)
	}
	defer rows.Close()

	reasons := make([]domain.RejectionReason, 0, 8)
	for rows.Next() {
		reason, err := scanReason(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan rejection reason: %w", err)
		}
		reasons = append(reasons, reason)
	}
	return reasons, rows.Err()
}

// FindRejectionReason resolves one reason within a tenant.
func (s *Store) FindRejectionReason(ctx context.Context, tx pgx.Tx, companyID, id string) (domain.RejectionReason, error) {
	query := `SELECT ` + reasonColumns + ` FROM rejection_reasons WHERE id = $1 AND company_id = $2`

	reason, err := scanReason(s.queryRow(ctx, tx, query, id, companyID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.RejectionReason{}, domain.ErrReasonNotFound
		}
		return domain.RejectionReason{}, fmt.Errorf("store: find rejection reason: %w", err)
	}
	return reason, nil
}

// CreateRejectionReason adds a reason to a company's list.
func (s *Store) CreateRejectionReason(ctx context.Context, companyID, label string, order int) (domain.RejectionReason, error) {
	query := `
		INSERT INTO rejection_reasons (id, company_id, label, sort_order)
		VALUES ($1, $2, $3, $4)
		RETURNING ` + reasonColumns

	reason, err := scanReason(s.pool.QueryRow(ctx, query, idgen.New("rsn"), companyID, label, order))
	if err != nil {
		if isUniqueViolation(err) {
			return domain.RejectionReason{}, domain.ErrReasonLabelTaken
		}
		return domain.RejectionReason{}, fmt.Errorf("store: create rejection reason: %w", err)
	}
	return reason, nil
}

// ReasonPatch carries the fields a company may change on a reason.
type ReasonPatch struct {
	Label    *string
	Order    *int
	IsActive *bool
}

// UpdateRejectionReason applies a patch to one of a company's reasons.
func (s *Store) UpdateRejectionReason(ctx context.Context, companyID, id string, patch ReasonPatch) (domain.RejectionReason, error) {
	query := `
		UPDATE rejection_reasons SET
			label      = coalesce($3, label),
			sort_order = coalesce($4, sort_order),
			is_active  = coalesce($5, is_active)
		WHERE id = $1 AND company_id = $2
		RETURNING ` + reasonColumns

	reason, err := scanReason(s.pool.QueryRow(ctx, query, id, companyID, patch.Label, patch.Order, patch.IsActive))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.RejectionReason{}, domain.ErrReasonNotFound
		}
		if isUniqueViolation(err) {
			return domain.RejectionReason{}, domain.ErrReasonLabelTaken
		}
		return domain.RejectionReason{}, fmt.Errorf("store: update rejection reason: %w", err)
	}
	return reason, nil
}

// DeleteRejectionReason removes a reason that no application cites.
//
// A reason already recorded against a rejection is part of that decision's
// record, so it is refused rather than deleted; deactivating takes it off the
// form without rewriting history.
func (s *Store) DeleteRejectionReason(ctx context.Context, companyID, id string) error {
	return s.InTx(ctx, func(tx pgx.Tx) error {
		if _, err := s.FindRejectionReason(ctx, tx, companyID, id); err != nil {
			return err
		}

		var used int64
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM applications WHERE company_id = $1 AND rejection_reason_id = $2`,
			companyID, id).Scan(&used); err != nil {
			return fmt.Errorf("store: count reason usage: %w", err)
		}
		if used > 0 {
			return domain.ErrReasonInUse
		}

		if _, err := tx.Exec(ctx,
			`DELETE FROM rejection_reasons WHERE id = $1 AND company_id = $2`, id, companyID); err != nil {
			if isForeignKeyViolation(err) {
				return domain.ErrReasonInUse
			}
			return fmt.Errorf("store: delete rejection reason: %w", err)
		}
		return nil
	})
}
