package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/karmarat/platform/packages/goshared/idgen"
	"github.com/karmarat/platform/services/applications/internal/domain"
)

const stageColumns = `
	id, company_id, key, name, sort_order, type, is_terminal, color, created_at, updated_at`

func scanStage(row pgx.Row) (domain.Stage, error) {
	var s domain.Stage
	err := row.Scan(
		&s.ID, &s.CompanyID, &s.Key, &s.Name, &s.Order, &s.Type,
		&s.IsTerminal, &s.Color, &s.CreatedAt, &s.UpdatedAt,
	)
	return s, err
}

// ListStages returns a company's pipeline in display order.
func (s *Store) ListStages(ctx context.Context, companyID string) ([]domain.Stage, error) {
	query := `SELECT ` + stageColumns + `
		FROM pipeline_stages WHERE company_id = $1 ORDER BY sort_order, id`

	rows, err := s.pool.Query(ctx, query, companyID)
	if err != nil {
		return nil, fmt.Errorf("store: list stages: %w", err)
	}
	defer rows.Close()

	stages := make([]domain.Stage, 0, 8)
	for rows.Next() {
		stage, err := scanStage(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan stage: %w", err)
		}
		stages = append(stages, stage)
	}
	return stages, rows.Err()
}

// FindStage resolves one stage within a tenant.
func (s *Store) FindStage(ctx context.Context, tx pgx.Tx, companyID, id string) (domain.Stage, error) {
	query := `SELECT ` + stageColumns + ` FROM pipeline_stages WHERE id = $1 AND company_id = $2`

	stage, err := scanStage(s.queryRow(ctx, tx, query, id, companyID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Stage{}, domain.ErrStageNotFound
		}
		return domain.Stage{}, fmt.Errorf("store: find stage: %w", err)
	}
	return stage, nil
}

// CreateStage adds a stage to a company's pipeline.
func (s *Store) CreateStage(ctx context.Context, companyID string, in domain.Stage) (domain.Stage, error) {
	query := `
		INSERT INTO pipeline_stages (id, company_id, key, name, sort_order, type, is_terminal, color)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING ` + stageColumns

	stage, err := scanStage(s.pool.QueryRow(ctx, query,
		idgen.New("stg"), companyID, in.Key, in.Name, in.Order, in.Type, in.IsTerminal, in.Color))
	if err != nil {
		if isUniqueViolation(err) {
			return domain.Stage{}, domain.ErrStageKeyTaken
		}
		return domain.Stage{}, fmt.Errorf("store: create stage: %w", err)
	}
	return stage, nil
}

// StagePatch carries the fields a company may change on a stage.
//
// A nil pointer means "leave this alone", which is what lets one PATCH rename a
// stage without also asserting a colour and an order it never sent.
type StagePatch struct {
	Name       *string
	Order      *int
	Color      *string
	Type       *domain.StageType
	IsTerminal *bool
}

// UpdateStage applies a patch to one of a company's stages.
func (s *Store) UpdateStage(ctx context.Context, companyID, id string, patch StagePatch) (domain.Stage, error) {
	query := `
		UPDATE pipeline_stages SET
			name        = coalesce($3, name),
			sort_order  = coalesce($4, sort_order),
			color       = coalesce($5, color),
			type        = coalesce($6, type),
			is_terminal = coalesce($7, is_terminal)
		WHERE id = $1 AND company_id = $2
		RETURNING ` + stageColumns

	stage, err := scanStage(s.pool.QueryRow(ctx, query,
		id, companyID, patch.Name, patch.Order, patch.Color, patch.Type, patch.IsTerminal))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Stage{}, domain.ErrStageNotFound
		}
		return domain.Stage{}, fmt.Errorf("store: update stage: %w", err)
	}
	return stage, nil
}

// ReorderStages rewrites the display order from a list of stage ids.
//
// The whole order is set in one transaction because a drag-and-drop board sends
// the arrangement it wants, not a sequence of swaps; applying it row by row
// would leave the board briefly showing an order nobody asked for.
func (s *Store) ReorderStages(ctx context.Context, companyID string, ids []string) ([]domain.Stage, error) {
	err := s.InTx(ctx, func(tx pgx.Tx) error {
		for position, id := range ids {
			tag, err := tx.Exec(ctx,
				`UPDATE pipeline_stages SET sort_order = $3 WHERE id = $1 AND company_id = $2`,
				id, companyID, position+1)
			if err != nil {
				return fmt.Errorf("store: reorder stages: %w", err)
			}
			if tag.RowsAffected() == 0 {
				return domain.ErrStageNotFound
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.ListStages(ctx, companyID)
}

// CountApplicationsInStage reports how many of a company's applications sit in
// a stage.
func (s *Store) CountApplicationsInStage(ctx context.Context, tx pgx.Tx, companyID, stageID string) (int64, error) {
	var count int64
	err := s.queryRow(ctx, tx,
		`SELECT count(*) FROM applications WHERE company_id = $1 AND stage_id = $2`,
		companyID, stageID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("store: count applications in stage: %w", err)
	}
	return count, nil
}

// DeleteStage removes a stage, moving any applications it holds to moveToID.
//
// Deleting a stage out from under live applications would either orphan them or
// silently drop them from the board, so the caller must say where they go. The
// count and the move happen in one transaction: checking first and deleting
// afterwards leaves a window in which a new application lands in the stage.
func (s *Store) DeleteStage(ctx context.Context, companyID, id, moveToID string) (int64, error) {
	var moved int64

	err := s.InTx(ctx, func(tx pgx.Tx) error {
		stage, err := s.FindStage(ctx, tx, companyID, id)
		if err != nil {
			return err
		}

		var remaining int64
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM pipeline_stages WHERE company_id = $1`, companyID).Scan(&remaining); err != nil {
			return fmt.Errorf("store: count stages: %w", err)
		}
		if remaining <= 1 {
			return domain.ErrLastStage
		}

		held, err := s.CountApplicationsInStage(ctx, tx, companyID, stage.ID)
		if err != nil {
			return err
		}

		if held > 0 {
			if moveToID == "" || moveToID == stage.ID {
				return domain.ErrStageInUse
			}
			target, err := s.FindStage(ctx, tx, companyID, moveToID)
			if err != nil {
				return err
			}

			// The affected ids are read before the move so each one can get its
			// own history entry: a candidate appearing in a stage they were never
			// moved to, with nothing to explain it, is how a pipeline loses trust.
			affected, err := s.applicationIDsInStage(ctx, tx, companyID, stage.ID)
			if err != nil {
				return err
			}

			tag, err := tx.Exec(ctx,
				`UPDATE applications SET stage_id = $3 WHERE company_id = $1 AND stage_id = $2`,
				companyID, stage.ID, target.ID)
			if err != nil {
				return fmt.Errorf("store: move applications off stage: %w", err)
			}
			moved = tag.RowsAffected()

			for _, applicationID := range affected {
				if err := s.appendEvent(ctx, tx, eventInput{
					CompanyID:     companyID,
					ApplicationID: applicationID,
					Type:          domain.EventStageChanged,
					ActorType:     "system",
					FromStageID:   stage.ID,
					ToStageID:     target.ID,
					Note:          "Stage removed; application moved automatically.",
				}); err != nil {
					return err
				}
			}
		}

		if _, err := tx.Exec(ctx,
			`DELETE FROM pipeline_stages WHERE id = $1 AND company_id = $2`, stage.ID, companyID); err != nil {
			return fmt.Errorf("store: delete stage: %w", err)
		}
		return nil
	})

	return moved, err
}

func (s *Store) applicationIDsInStage(ctx context.Context, tx pgx.Tx, companyID, stageID string) ([]string, error) {
	rows, err := s.query(ctx, tx,
		`SELECT id FROM applications WHERE company_id = $1 AND stage_id = $2`, companyID, stageID)
	if err != nil {
		return nil, fmt.Errorf("store: list applications in stage: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("store: scan application id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// SeedCompanyDefaults gives a company the pipeline and reasons it starts with.
//
// It is called from the company-approved consumer and lazily on first use,
// because an event can arrive before this service exists and a company that
// signed up earlier must still get a pipeline. Both paths run the same
// idempotent insert, so neither has to know whether the other already ran.
func (s *Store) SeedCompanyDefaults(ctx context.Context, companyID string) (bool, error) {
	seeded := false

	err := s.InTx(ctx, func(tx pgx.Tx) error {
		var existing int
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM pipeline_stages WHERE company_id = $1`, companyID).Scan(&existing); err != nil {
			return fmt.Errorf("store: count existing stages: %w", err)
		}

		if existing == 0 {
			for _, stage := range domain.DefaultStages() {
				if _, err := tx.Exec(ctx, `
					INSERT INTO pipeline_stages (id, company_id, key, name, sort_order, type, is_terminal, color)
					VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
					ON CONFLICT DO NOTHING`,
					idgen.New("stg"), companyID, stage.Key, stage.Name,
					stage.Order, stage.Type, stage.IsTerminal, stage.Color); err != nil {
					return fmt.Errorf("store: seed stage: %w", err)
				}
			}
			seeded = true
		}

		var reasons int
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM rejection_reasons WHERE company_id = $1`, companyID).Scan(&reasons); err != nil {
			return fmt.Errorf("store: count existing reasons: %w", err)
		}

		if reasons == 0 {
			for _, reason := range domain.DefaultRejectionReasons() {
				if _, err := tx.Exec(ctx, `
					INSERT INTO rejection_reasons (id, company_id, label, sort_order, is_active)
					VALUES ($1, $2, $3, $4, true)
					ON CONFLICT DO NOTHING`,
					idgen.New("rsn"), companyID, reason.Label, reason.Order); err != nil {
					return fmt.Errorf("store: seed rejection reason: %w", err)
				}
			}
			seeded = true
		}

		return nil
	})

	return seeded, err
}

// FirstStage returns the stage a new application lands in.
func (s *Store) FirstStage(ctx context.Context, tx pgx.Tx, companyID string) (domain.Stage, error) {
	query := `SELECT ` + stageColumns + `
		FROM pipeline_stages
		WHERE company_id = $1 AND is_terminal = false
		ORDER BY sort_order, id
		LIMIT 1`

	stage, err := scanStage(s.queryRow(ctx, tx, query, companyID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Stage{}, domain.ErrStageNotFound
		}
		return domain.Stage{}, fmt.Errorf("store: find first stage: %w", err)
	}
	return stage, nil
}
