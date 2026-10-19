package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/karmarat/platform/services/candidates/internal/domain"
)

// MaxResumesPerCandidate bounds how many documents one candidate may keep. It
// exists so a signed-upload endpoint cannot be used as free object storage.
const MaxResumesPerCandidate = 10

const resumeColumns = `
	id, candidate_id, account_id, object_key, filename, content_type,
	size_bytes, is_primary, created_at`

func scanResume(row pgx.Row) (domain.Resume, error) {
	var r domain.Resume
	err := row.Scan(
		&r.ID, &r.CandidateID, &r.AccountID, &r.ObjectKey, &r.Filename,
		&r.ContentType, &r.SizeBytes, &r.IsPrimary, &r.CreatedAt,
	)
	return r, err
}

// CreateResume records an upload the candidate has been given a URL for.
//
// The first document a candidate uploads becomes their primary one: a profile
// with resumes but no primary would be an empty attachment on every application.
func (s *Store) CreateResume(ctx context.Context, r domain.Resume) (domain.Resume, error) {
	var created domain.Resume

	err := s.InTx(ctx, func(tx pgx.Tx) error {
		var existing int
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM candidate_resumes WHERE account_id = $1`, r.AccountID,
		).Scan(&existing); err != nil {
			return fmt.Errorf("store: count resumes: %w", err)
		}
		if existing >= MaxResumesPerCandidate {
			return domain.ErrResumeLimit
		}

		query := `
			INSERT INTO candidate_resumes
				(id, candidate_id, account_id, object_key, filename, content_type,
				 size_bytes, is_primary)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			RETURNING ` + resumeColumns

		row := tx.QueryRow(ctx, query,
			r.ID, r.CandidateID, r.AccountID, r.ObjectKey, r.Filename,
			r.ContentType, r.SizeBytes, existing == 0)

		var err error
		created, err = scanResume(row)
		if err != nil {
			return fmt.Errorf("store: create resume: %w", err)
		}
		return nil
	})

	return created, err
}

// ListResumes returns a candidate's documents, newest first.
func (s *Store) ListResumes(ctx context.Context, accountID string, page domain.Page) ([]domain.Resume, error) {
	query := `
		SELECT ` + resumeColumns + `
		FROM candidate_resumes
		WHERE account_id = $1
		  AND ($2::boolean IS FALSE OR (created_at, id) < ($3::timestamptz, $4))
		ORDER BY created_at DESC, id DESC
		LIMIT $5`

	rows, err := s.pool.Query(ctx, query,
		accountID, page.Cursor.Set(), cursorTimeParam(page.Cursor), page.Cursor.ID, page.Limit)
	if err != nil {
		return nil, fmt.Errorf("store: list resumes: %w", err)
	}
	defer rows.Close()

	resumes := make([]domain.Resume, 0, page.Limit)
	for rows.Next() {
		resume, err := scanResume(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan resume: %w", err)
		}
		resumes = append(resumes, resume)
	}
	return resumes, rows.Err()
}

// DeleteResume removes one document's record and reports its object key so the
// caller can schedule the stored object for removal.
func (s *Store) DeleteResume(ctx context.Context, accountID, id string) (domain.Resume, error) {
	var deleted domain.Resume

	err := s.InTx(ctx, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`DELETE FROM candidate_resumes WHERE id = $1 AND account_id = $2 RETURNING `+resumeColumns,
			id, accountID)

		var err error
		deleted, err = scanResume(row)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return domain.ErrResumeNotFound
			}
			return fmt.Errorf("store: delete resume: %w", err)
		}

		// Deleting the primary would leave the candidate with documents and no
		// default attachment, so the next newest takes over.
		if deleted.IsPrimary {
			if _, err := tx.Exec(ctx, `
				UPDATE candidate_resumes SET is_primary = true
				WHERE id = (
					SELECT id FROM candidate_resumes
					WHERE account_id = $1
					ORDER BY created_at DESC, id DESC
					LIMIT 1
				)`, accountID); err != nil {
				return fmt.Errorf("store: promote replacement resume: %w", err)
			}
		}
		return nil
	})

	return deleted, err
}

// SetPrimaryResume makes one document the candidate's default.
func (s *Store) SetPrimaryResume(ctx context.Context, accountID, id string) (domain.Resume, error) {
	var primary domain.Resume

	err := s.InTx(ctx, func(tx pgx.Tx) error {
		// The unique partial index rejects a second primary, so the old one is
		// cleared first inside the same transaction.
		if _, err := tx.Exec(ctx,
			`UPDATE candidate_resumes SET is_primary = false WHERE account_id = $1 AND is_primary`,
			accountID); err != nil {
			return fmt.Errorf("store: clear primary resume: %w", err)
		}

		row := tx.QueryRow(ctx,
			`UPDATE candidate_resumes SET is_primary = true
			 WHERE id = $1 AND account_id = $2
			 RETURNING `+resumeColumns, id, accountID)

		var err error
		primary, err = scanResume(row)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return domain.ErrResumeNotFound
			}
			return fmt.Errorf("store: set primary resume: %w", err)
		}
		return nil
	})

	return primary, err
}

// PrimaryResumeForCandidate returns the document a company would download.
func (s *Store) PrimaryResumeForCandidate(ctx context.Context, candidateID string) (domain.Resume, error) {
	query := `SELECT ` + resumeColumns + ` FROM candidate_resumes WHERE candidate_id = $1 AND is_primary`

	resume, err := scanResume(s.pool.QueryRow(ctx, query, candidateID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Resume{}, domain.ErrNoResume
		}
		return domain.Resume{}, fmt.Errorf("store: primary resume: %w", err)
	}
	return resume, nil
}
