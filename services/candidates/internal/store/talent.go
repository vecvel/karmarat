package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/karmarat/platform/packages/goshared/idgen"
	"github.com/karmarat/platform/services/candidates/internal/domain"
)

// discoverablePredicate is the consent check, written once and used by every
// statement a company can reach a platform profile through.
//
// It lives in a constant because the failure mode it prevents — one query that
// forgets the block list — would not look like a bug in review. $1 is always the
// searching company.
const discoverablePredicate = `
	discoverable
	AND deleted_at IS NULL
	AND NOT ($1::uuid = ANY (hide_from_companies))`

// searchColumns deliberately omits email and phone. A company earns contact
// details by being answered, not by running a search, and a column that is never
// selected cannot be leaked by a later change to a response type.
const searchColumns = `
	id, headline, summary, location, years_experience, current_title,
	CASE WHEN hide_current_employer THEN '' ELSE current_employer END AS current_employer,
	skills, languages, open_to_types::text[], open_to_remote, work_authorisation::text,
	desired_salary_minor, desired_salary_currency, updated_at`

func scanSearchResult(row pgx.Row) (domain.SearchResult, error) {
	var r domain.SearchResult
	var openTo []string
	var workAuth string
	var currency *string

	if err := row.Scan(
		&r.ID, &r.Headline, &r.Summary, &r.Location, &r.YearsExperience, &r.CurrentTitle,
		&r.CurrentEmployer, &r.Skills, &r.Languages, &openTo, &r.OpenToRemote, &workAuth,
		&r.DesiredSalaryMinor, &currency, &r.UpdatedAt,
	); err != nil {
		return domain.SearchResult{}, err
	}

	if currency != nil {
		r.DesiredSalaryCurrency = *currency
	}
	r.WorkAuthorisation = domain.WorkAuthorisation(workAuth)
	r.OpenToTypes = make([]domain.EmploymentType, 0, len(openTo))
	for _, t := range openTo {
		r.OpenToTypes = append(r.OpenToTypes, domain.EmploymentType(t))
	}
	r.Skills = orEmpty(r.Skills)
	r.Languages = orEmpty(r.Languages)

	return r, nil
}

// TalentFilter narrows a talent search.
type TalentFilter struct {
	// Query matches headline, summary and current title.
	Query          string
	Skills         []string
	Location       string
	MinYears       int
	RemoteOnly     bool
	EmploymentType domain.EmploymentType
}

// SearchTalent returns discoverable profiles the searching company may see.
func (s *Store) SearchTalent(
	ctx context.Context, companyID string, filter TalentFilter, page domain.Page,
) ([]domain.SearchResult, error) {
	query := `
		SELECT ` + searchColumns + `
		FROM candidate_profiles
		WHERE ` + discoverablePredicate + `
		  AND ($2::boolean IS FALSE OR (updated_at, id) < ($3::timestamptz, $4))
		  AND ($5::text IS NULL OR (
		        headline ILIKE '%' || $5 || '%'
		     OR summary  ILIKE '%' || $5 || '%'
		     OR current_title ILIKE '%' || $5 || '%'))
		  AND ($6::text[] IS NULL OR skills @> $6::text[])
		  AND ($7::text IS NULL OR location ILIKE '%' || $7 || '%')
		  AND years_experience >= $8
		  AND ($9::boolean IS FALSE OR open_to_remote)
		  AND ($10::text IS NULL OR $10::employment_type = ANY (open_to_types))
		ORDER BY updated_at DESC, id DESC
		LIMIT $11`

	rows, err := s.pool.Query(ctx, query,
		companyID, page.Cursor.Set(), cursorTimeParam(page.Cursor), page.Cursor.ID,
		nullable(strings.TrimSpace(filter.Query)),
		skillsParam(filter.Skills),
		nullable(strings.TrimSpace(filter.Location)),
		filter.MinYears,
		filter.RemoteOnly,
		nullable(string(filter.EmploymentType)),
		page.Limit)
	if err != nil {
		return nil, fmt.Errorf("store: search talent: %w", err)
	}
	defer rows.Close()

	results := make([]domain.SearchResult, 0, page.Limit)
	for rows.Next() {
		result, err := scanSearchResult(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan search result: %w", err)
		}
		results = append(results, result)
	}
	return results, rows.Err()
}

// FindDiscoverableProfile returns one profile a company is allowed to act on.
//
// Approaching goes through the same consent predicate as searching, so guessing
// a candidate id is worth no more than not knowing it.
func (s *Store) FindDiscoverableProfile(ctx context.Context, companyID, candidateID string) (domain.SearchResult, error) {
	query := `
		SELECT ` + searchColumns + `
		FROM candidate_profiles
		WHERE ` + discoverablePredicate + `
		  AND id = $2`

	result, err := scanSearchResult(s.pool.QueryRow(ctx, query, companyID, candidateID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.SearchResult{}, domain.ErrCandidateNotFound
		}
		return domain.SearchResult{}, fmt.Errorf("store: find discoverable profile: %w", err)
	}
	return result, nil
}

// CountRecentApproaches reports how many times this company has approached this
// candidate since a cutoff.
//
// This is the durable half of the rate limit. The Redis limiter in front of it
// fails open by design, so the guarantee has to live in the database.
func (s *Store) CountRecentApproaches(ctx context.Context, companyID, candidateID string, since time.Time) (int, error) {
	var count int
	err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM candidate_approaches
		WHERE company_id = $1::uuid AND candidate_id = $2 AND created_at >= $3`,
		companyID, candidateID, since).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("store: count recent approaches: %w", err)
	}
	return count, nil
}

// CreateApproach records one company reaching out to one candidate.
func (s *Store) CreateApproach(ctx context.Context, a domain.Approach) (domain.Approach, error) {
	query := `
		INSERT INTO candidate_approaches
			(id, candidate_id, company_id, actor_account_id, job_id, subject, message)
		VALUES ($1, $2, $3::uuid, $4, $5, $6, $7)
		RETURNING id, candidate_id, company_id::text, actor_account_id,
		          coalesce(job_id, ''), subject, message, created_at`

	var created domain.Approach
	err := s.pool.QueryRow(ctx, query,
		idgen.New("appr"), a.CandidateID, a.CompanyID, a.ActorAccountID,
		nullable(a.JobID), a.Subject, a.Message,
	).Scan(
		&created.ID, &created.CandidateID, &created.CompanyID, &created.ActorAccountID,
		&created.JobID, &created.Subject, &created.Message, &created.CreatedAt,
	)
	if err != nil {
		return domain.Approach{}, fmt.Errorf("store: create approach: %w", err)
	}
	return created, nil
}

// ListApproaches pages the approaches one company has made.
func (s *Store) ListApproaches(ctx context.Context, companyID string, page domain.Page) ([]domain.Approach, error) {
	query := `
		SELECT id, candidate_id, company_id::text, actor_account_id,
		       coalesce(job_id, ''), subject, message, created_at
		FROM candidate_approaches
		WHERE company_id = $1::uuid
		  AND ($2::boolean IS FALSE OR (created_at, id) < ($3::timestamptz, $4))
		ORDER BY created_at DESC, id DESC
		LIMIT $5`

	rows, err := s.pool.Query(ctx, query,
		companyID, page.Cursor.Set(), cursorTimeParam(page.Cursor), page.Cursor.ID, page.Limit)
	if err != nil {
		return nil, fmt.Errorf("store: list approaches: %w", err)
	}
	defer rows.Close()

	approaches := make([]domain.Approach, 0, page.Limit)
	for rows.Next() {
		var a domain.Approach
		if err := rows.Scan(
			&a.ID, &a.CandidateID, &a.CompanyID, &a.ActorAccountID,
			&a.JobID, &a.Subject, &a.Message, &a.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("store: scan approach: %w", err)
		}
		approaches = append(approaches, a)
	}
	return approaches, rows.Err()
}

func skillsParam(skills []string) any {
	cleaned := make([]string, 0, len(skills))
	for _, skill := range skills {
		if trimmed := strings.TrimSpace(skill); trimmed != "" {
			cleaned = append(cleaned, trimmed)
		}
	}
	if len(cleaned) == 0 {
		return nil
	}
	return cleaned
}
