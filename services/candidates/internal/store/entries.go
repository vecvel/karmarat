package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/karmarat/platform/packages/goshared/idgen"
	"github.com/karmarat/platform/services/candidates/internal/domain"
)

/* -------------------------------------------------------------------------- */
/* Work experience                                                            */
/* -------------------------------------------------------------------------- */

const experienceColumns = `
	id, candidate_id, account_id, title, employer, location,
	employment_type::text, description, started_on, ended_on, is_current,
	created_at, updated_at`

func scanExperience(row pgx.Row) (domain.Experience, error) {
	var e domain.Experience
	var employmentType *string

	if err := row.Scan(
		&e.ID, &e.CandidateID, &e.AccountID, &e.Title, &e.Employer, &e.Location,
		&employmentType, &e.Description, &e.StartedOn, &e.EndedOn, &e.IsCurrent,
		&e.CreatedAt, &e.UpdatedAt,
	); err != nil {
		return domain.Experience{}, err
	}
	if employmentType != nil {
		t := domain.EmploymentType(*employmentType)
		e.EmploymentType = &t
	}
	return e, nil
}

// CreateExperience adds one employment-history entry.
func (s *Store) CreateExperience(ctx context.Context, e domain.Experience) (domain.Experience, error) {
	query := `
		INSERT INTO candidate_experience
			(id, candidate_id, account_id, title, employer, location,
			 employment_type, description, started_on, ended_on, is_current)
		VALUES ($1, $2, $3, $4, $5, $6, $7::employment_type, $8, $9, $10, $11)
		RETURNING ` + experienceColumns

	created, err := scanExperience(s.pool.QueryRow(ctx, query,
		idgen.New("exp"), e.CandidateID, e.AccountID, e.Title, e.Employer, e.Location,
		employmentTypeParam(e.EmploymentType), e.Description, e.StartedOn, e.EndedOn, e.IsCurrent))
	if err != nil {
		return domain.Experience{}, fmt.Errorf("store: create experience: %w", err)
	}
	return created, nil
}

// ListExperience returns a candidate's history, most recent role first.
func (s *Store) ListExperience(ctx context.Context, accountID string, page domain.Page) ([]domain.Experience, error) {
	query := `
		SELECT ` + experienceColumns + `
		FROM candidate_experience
		WHERE account_id = $1
		  AND ($2::boolean IS FALSE OR (started_on, id) < ($3::date, $4))
		ORDER BY started_on DESC, id DESC
		LIMIT $5`

	rows, err := s.pool.Query(ctx, query,
		accountID, page.Cursor.Set(), cursorDateParam(page.Cursor), page.Cursor.ID, page.Limit)
	if err != nil {
		return nil, fmt.Errorf("store: list experience: %w", err)
	}
	defer rows.Close()

	entries := make([]domain.Experience, 0, page.Limit)
	for rows.Next() {
		entry, err := scanExperience(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan experience: %w", err)
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

// UpdateExperience edits one entry the candidate owns.
func (s *Store) UpdateExperience(ctx context.Context, accountID string, e domain.Experience) (domain.Experience, error) {
	query := `
		UPDATE candidate_experience SET
			title = $3, employer = $4, location = $5, employment_type = $6::employment_type,
			description = $7, started_on = $8, ended_on = $9, is_current = $10
		WHERE id = $1 AND account_id = $2
		RETURNING ` + experienceColumns

	updated, err := scanExperience(s.pool.QueryRow(ctx, query,
		e.ID, accountID, e.Title, e.Employer, e.Location,
		employmentTypeParam(e.EmploymentType), e.Description, e.StartedOn, e.EndedOn, e.IsCurrent))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Experience{}, domain.ErrEntryNotFound
		}
		return domain.Experience{}, fmt.Errorf("store: update experience: %w", err)
	}
	return updated, nil
}

// DeleteExperience removes one entry the candidate owns.
func (s *Store) DeleteExperience(ctx context.Context, accountID, id string) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM candidate_experience WHERE id = $1 AND account_id = $2`, id, accountID)
	if err != nil {
		return fmt.Errorf("store: delete experience: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrEntryNotFound
	}
	return nil
}

/* -------------------------------------------------------------------------- */
/* Education                                                                  */
/* -------------------------------------------------------------------------- */

const educationColumns = `
	id, candidate_id, account_id, institution, qualification, field_of_study,
	grade, started_on, ended_on, created_at, updated_at`

func scanEducation(row pgx.Row) (domain.Education, error) {
	var e domain.Education
	err := row.Scan(
		&e.ID, &e.CandidateID, &e.AccountID, &e.Institution, &e.Qualification,
		&e.FieldOfStudy, &e.Grade, &e.StartedOn, &e.EndedOn, &e.CreatedAt, &e.UpdatedAt,
	)
	return e, err
}

// CreateEducation adds one qualification.
func (s *Store) CreateEducation(ctx context.Context, e domain.Education) (domain.Education, error) {
	query := `
		INSERT INTO candidate_education
			(id, candidate_id, account_id, institution, qualification, field_of_study,
			 grade, started_on, ended_on)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING ` + educationColumns

	created, err := scanEducation(s.pool.QueryRow(ctx, query,
		idgen.New("edu"), e.CandidateID, e.AccountID, e.Institution, e.Qualification,
		e.FieldOfStudy, e.Grade, e.StartedOn, e.EndedOn))
	if err != nil {
		return domain.Education{}, fmt.Errorf("store: create education: %w", err)
	}
	return created, nil
}

// ListEducation returns a candidate's qualifications, most recent first.
func (s *Store) ListEducation(ctx context.Context, accountID string, page domain.Page) ([]domain.Education, error) {
	// Ordered by creation rather than by end date: the dates here are nullable,
	// and a keyset cursor over a nullable column cannot express "no date yet".
	// Clients that want a chronological CV sort the page they were given.
	query := `
		SELECT ` + educationColumns + `
		FROM candidate_education
		WHERE account_id = $1
		  AND ($2::boolean IS FALSE OR (created_at, id) < ($3::timestamptz, $4))
		ORDER BY created_at DESC, id DESC
		LIMIT $5`

	rows, err := s.pool.Query(ctx, query,
		accountID, page.Cursor.Set(), cursorTimeParam(page.Cursor), page.Cursor.ID, page.Limit)
	if err != nil {
		return nil, fmt.Errorf("store: list education: %w", err)
	}
	defer rows.Close()

	entries := make([]domain.Education, 0, page.Limit)
	for rows.Next() {
		entry, err := scanEducation(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan education: %w", err)
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

// UpdateEducation edits one qualification the candidate owns.
func (s *Store) UpdateEducation(ctx context.Context, accountID string, e domain.Education) (domain.Education, error) {
	query := `
		UPDATE candidate_education SET
			institution = $3, qualification = $4, field_of_study = $5,
			grade = $6, started_on = $7, ended_on = $8
		WHERE id = $1 AND account_id = $2
		RETURNING ` + educationColumns

	updated, err := scanEducation(s.pool.QueryRow(ctx, query,
		e.ID, accountID, e.Institution, e.Qualification, e.FieldOfStudy, e.Grade, e.StartedOn, e.EndedOn))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Education{}, domain.ErrEntryNotFound
		}
		return domain.Education{}, fmt.Errorf("store: update education: %w", err)
	}
	return updated, nil
}

// DeleteEducation removes one qualification the candidate owns.
func (s *Store) DeleteEducation(ctx context.Context, accountID, id string) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM candidate_education WHERE id = $1 AND account_id = $2`, id, accountID)
	if err != nil {
		return fmt.Errorf("store: delete education: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrEntryNotFound
	}
	return nil
}

/* -------------------------------------------------------------------------- */
/* Certifications                                                             */
/* -------------------------------------------------------------------------- */

const certificationColumns = `
	id, candidate_id, account_id, name, issuer, credential_id, credential_url,
	issued_on, expires_on, created_at, updated_at`

func scanCertification(row pgx.Row) (domain.Certification, error) {
	var c domain.Certification
	err := row.Scan(
		&c.ID, &c.CandidateID, &c.AccountID, &c.Name, &c.Issuer, &c.CredentialID,
		&c.CredentialURL, &c.IssuedOn, &c.ExpiresOn, &c.CreatedAt, &c.UpdatedAt,
	)
	return c, err
}

// CreateCertification adds one credential.
func (s *Store) CreateCertification(ctx context.Context, c domain.Certification) (domain.Certification, error) {
	query := `
		INSERT INTO candidate_certifications
			(id, candidate_id, account_id, name, issuer, credential_id, credential_url,
			 issued_on, expires_on)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING ` + certificationColumns

	created, err := scanCertification(s.pool.QueryRow(ctx, query,
		idgen.New("cert"), c.CandidateID, c.AccountID, c.Name, c.Issuer,
		c.CredentialID, c.CredentialURL, c.IssuedOn, c.ExpiresOn))
	if err != nil {
		return domain.Certification{}, fmt.Errorf("store: create certification: %w", err)
	}
	return created, nil
}

// ListCertifications returns a candidate's credentials, most recent first.
func (s *Store) ListCertifications(ctx context.Context, accountID string, page domain.Page) ([]domain.Certification, error) {
	query := `
		SELECT ` + certificationColumns + `
		FROM candidate_certifications
		WHERE account_id = $1
		  AND ($2::boolean IS FALSE OR (created_at, id) < ($3::timestamptz, $4))
		ORDER BY created_at DESC, id DESC
		LIMIT $5`

	rows, err := s.pool.Query(ctx, query,
		accountID, page.Cursor.Set(), cursorTimeParam(page.Cursor), page.Cursor.ID, page.Limit)
	if err != nil {
		return nil, fmt.Errorf("store: list certifications: %w", err)
	}
	defer rows.Close()

	entries := make([]domain.Certification, 0, page.Limit)
	for rows.Next() {
		entry, err := scanCertification(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan certification: %w", err)
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

// UpdateCertification edits one credential the candidate owns.
func (s *Store) UpdateCertification(ctx context.Context, accountID string, c domain.Certification) (domain.Certification, error) {
	query := `
		UPDATE candidate_certifications SET
			name = $3, issuer = $4, credential_id = $5, credential_url = $6,
			issued_on = $7, expires_on = $8
		WHERE id = $1 AND account_id = $2
		RETURNING ` + certificationColumns

	updated, err := scanCertification(s.pool.QueryRow(ctx, query,
		c.ID, accountID, c.Name, c.Issuer, c.CredentialID, c.CredentialURL, c.IssuedOn, c.ExpiresOn))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Certification{}, domain.ErrEntryNotFound
		}
		return domain.Certification{}, fmt.Errorf("store: update certification: %w", err)
	}
	return updated, nil
}

// DeleteCertification removes one credential the candidate owns.
func (s *Store) DeleteCertification(ctx context.Context, accountID, id string) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM candidate_certifications WHERE id = $1 AND account_id = $2`, id, accountID)
	if err != nil {
		return fmt.Errorf("store: delete certification: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrEntryNotFound
	}
	return nil
}

func employmentTypeParam(value *domain.EmploymentType) any {
	if value == nil {
		return nil
	}
	return string(*value)
}

// cursorDateParam renders a cursor position for a list ordered by a date column.
//
// The value goes over the wire as text rather than as a timestamp: a
// timestamptz cast to date is resolved in the session's time zone, which would
// make the same cursor land on different rows on different connections.
func cursorDateParam(c domain.Cursor) any {
	if !c.Set() {
		return nil
	}
	return c.At.UTC().Format("2006-01-02")
}

// cursorTimeParam renders a cursor position for a list ordered by a timestamp.
func cursorTimeParam(c domain.Cursor) any {
	if !c.Set() {
		return nil
	}
	return c.At
}
