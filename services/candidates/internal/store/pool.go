package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/karmarat/platform/packages/goshared/idgen"
	"github.com/karmarat/platform/services/candidates/internal/domain"
)

const poolColumns = `
	id, company_id::text, full_name, email, phone, headline, location,
	source::text, current_title, current_employer, years_experience,
	skills, tags, notes, coalesce(linked_profile_id, ''),
	resume_object_key, resume_filename, resume_content_type, resume_size_bytes,
	created_by, created_at, updated_at`

func scanPoolCandidate(row pgx.Row) (domain.PoolCandidate, error) {
	var c domain.PoolCandidate
	var source string

	if err := row.Scan(
		&c.ID, &c.CompanyID, &c.FullName, &c.Email, &c.Phone, &c.Headline, &c.Location,
		&source, &c.CurrentTitle, &c.CurrentEmployer, &c.YearsExperience,
		&c.Skills, &c.Tags, &c.Notes, &c.LinkedProfileID,
		&c.ResumeObjectKey, &c.ResumeFilename, &c.ResumeContentType, &c.ResumeSizeBytes,
		&c.CreatedBy, &c.CreatedAt, &c.UpdatedAt,
	); err != nil {
		return domain.PoolCandidate{}, err
	}

	c.Source = domain.Source(source)
	c.Skills = orEmpty(c.Skills)
	c.Tags = orEmpty(c.Tags)
	return c, nil
}

// PoolFilter narrows a talent-pool listing.
type PoolFilter struct {
	Query  string
	Source domain.Source
	Skill  string
}

// CreatePoolCandidate adds a record to one company's own pool.
func (s *Store) CreatePoolCandidate(ctx context.Context, c domain.PoolCandidate) (domain.PoolCandidate, error) {
	query := `
		INSERT INTO company_candidates
			(id, company_id, full_name, email, phone, headline, location, source,
			 current_title, current_employer, years_experience, skills, tags, notes,
			 linked_profile_id, created_by)
		VALUES ($1, $2::uuid, $3, $4, $5, $6, $7, $8::candidate_source,
		        $9, $10, $11, $12::text[], $13::text[], $14, $15, $16)
		RETURNING ` + poolColumns

	created, err := scanPoolCandidate(s.pool.QueryRow(ctx, query,
		idgen.New("pool"), c.CompanyID, c.FullName, c.Email, c.Phone, c.Headline, c.Location,
		string(c.Source), c.CurrentTitle, c.CurrentEmployer, c.YearsExperience,
		orEmpty(c.Skills), orEmpty(c.Tags), c.Notes, nullable(c.LinkedProfileID), c.CreatedBy))
	if err != nil {
		return domain.PoolCandidate{}, fmt.Errorf("store: create pool candidate: %w", err)
	}
	return created, nil
}

// FindPoolCandidate returns one record belonging to the calling company.
func (s *Store) FindPoolCandidate(ctx context.Context, companyID, id string) (domain.PoolCandidate, error) {
	query := `
		SELECT ` + poolColumns + `
		FROM company_candidates
		WHERE id = $1 AND company_id = $2::uuid AND deleted_at IS NULL`

	candidate, err := scanPoolCandidate(s.pool.QueryRow(ctx, query, id, companyID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Not found rather than forbidden: another tenant's record must not be
			// confirmed to exist by the shape of the refusal.
			return domain.PoolCandidate{}, domain.ErrCandidateNotFound
		}
		return domain.PoolCandidate{}, fmt.Errorf("store: find pool candidate: %w", err)
	}
	return candidate, nil
}

// ListPoolCandidates pages one company's pool.
func (s *Store) ListPoolCandidates(
	ctx context.Context, companyID string, filter PoolFilter, page domain.Page,
) ([]domain.PoolCandidate, error) {
	query := `
		SELECT ` + poolColumns + `
		FROM company_candidates
		WHERE company_id = $1::uuid
		  AND deleted_at IS NULL
		  AND ($2::boolean IS FALSE OR (created_at, id) < ($3::timestamptz, $4))
		  AND ($5::text IS NULL OR (
		        full_name ILIKE '%' || $5 || '%'
		     OR headline  ILIKE '%' || $5 || '%'
		     OR current_title ILIKE '%' || $5 || '%'
		     OR current_employer ILIKE '%' || $5 || '%'))
		  AND ($6::text IS NULL OR source = $6::candidate_source)
		  AND ($7::text IS NULL OR skills @> ARRAY[$7]::text[])
		ORDER BY created_at DESC, id DESC
		LIMIT $8`

	rows, err := s.pool.Query(ctx, query,
		companyID, page.Cursor.Set(), cursorTimeParam(page.Cursor), page.Cursor.ID,
		nullable(strings.TrimSpace(filter.Query)),
		nullable(string(filter.Source)),
		nullable(strings.TrimSpace(filter.Skill)),
		page.Limit)
	if err != nil {
		return nil, fmt.Errorf("store: list pool candidates: %w", err)
	}
	defer rows.Close()

	candidates := make([]domain.PoolCandidate, 0, page.Limit)
	for rows.Next() {
		candidate, err := scanPoolCandidate(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan pool candidate: %w", err)
		}
		candidates = append(candidates, candidate)
	}
	return candidates, rows.Err()
}

// UpdatePoolCandidate edits one record belonging to the calling company.
func (s *Store) UpdatePoolCandidate(ctx context.Context, companyID string, c domain.PoolCandidate) (domain.PoolCandidate, error) {
	query := `
		UPDATE company_candidates SET
			full_name = $3, email = $4, phone = $5, headline = $6, location = $7,
			source = $8::candidate_source, current_title = $9, current_employer = $10,
			years_experience = $11, skills = $12::text[], tags = $13::text[], notes = $14
		WHERE id = $1 AND company_id = $2::uuid AND deleted_at IS NULL
		RETURNING ` + poolColumns

	updated, err := scanPoolCandidate(s.pool.QueryRow(ctx, query,
		c.ID, companyID, c.FullName, c.Email, c.Phone, c.Headline, c.Location,
		string(c.Source), c.CurrentTitle, c.CurrentEmployer, c.YearsExperience,
		orEmpty(c.Skills), orEmpty(c.Tags), c.Notes))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.PoolCandidate{}, domain.ErrCandidateNotFound
		}
		return domain.PoolCandidate{}, fmt.Errorf("store: update pool candidate: %w", err)
	}
	return updated, nil
}

// AttachPoolResume records a document uploaded against a pool record.
func (s *Store) AttachPoolResume(
	ctx context.Context, companyID, id, objectKey, filename, contentType string, sizeBytes int64,
) (domain.PoolCandidate, error) {
	query := `
		UPDATE company_candidates SET
			resume_object_key = $3, resume_filename = $4,
			resume_content_type = $5, resume_size_bytes = $6
		WHERE id = $1 AND company_id = $2::uuid AND deleted_at IS NULL
		RETURNING ` + poolColumns

	updated, err := scanPoolCandidate(s.pool.QueryRow(ctx, query,
		id, companyID, objectKey, filename, contentType, sizeBytes))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.PoolCandidate{}, domain.ErrCandidateNotFound
		}
		return domain.PoolCandidate{}, fmt.Errorf("store: attach pool resume: %w", err)
	}
	return updated, nil
}

// SoftDeletePoolCandidate retires one record belonging to the calling company.
func (s *Store) SoftDeletePoolCandidate(ctx context.Context, companyID, id string) error {
	tag, err := s.exec(ctx, nil, `
		UPDATE company_candidates SET deleted_at = now()
		WHERE id = $1 AND company_id = $2::uuid AND deleted_at IS NULL`, id, companyID)
	if err != nil {
		return fmt.Errorf("store: delete pool candidate: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrCandidateNotFound
	}
	return nil
}

// ExportPoolCandidates streams a company's pool for download.
//
// The limit is the export's own ceiling rather than a page size: an export that
// silently truncates is worse than one that refuses, so the caller is told when
// the cap was reached.
func (s *Store) ExportPoolCandidates(
	ctx context.Context, companyID string, filter PoolFilter, limit int,
) ([]domain.PoolCandidate, error) {
	return s.ListPoolCandidates(ctx, companyID, filter, domain.Page{Limit: limit})
}
