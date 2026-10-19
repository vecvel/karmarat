package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/karmarat/platform/packages/goshared/idgen"
	"github.com/karmarat/platform/services/candidates/internal/domain"
)

// Enum arrays are read as text[] and written through an explicit cast: pgx has
// no knowledge of this service's enum types, and a text round-trip keeps the
// scan code free of driver-specific plumbing.
const profileColumns = `
	id, account_id, email, full_name, headline, summary, location,
	years_experience, current_title, current_employer, phone,
	skills, languages, website_url, linkedin_url, github_url,
	desired_salary_minor, desired_salary_currency,
	open_to_types::text[], open_to_remote, work_authorisation::text,
	discoverable, hide_current_employer, hide_from_companies::text[],
	version, deleted_at, created_at, updated_at`

func scanProfile(row pgx.Row) (domain.Profile, error) {
	var p domain.Profile
	var currency *string
	var openTo []string
	var workAuth string

	err := row.Scan(
		&p.ID, &p.AccountID, &p.Email, &p.FullName, &p.Headline, &p.Summary, &p.Location,
		&p.YearsExperience, &p.CurrentTitle, &p.CurrentEmployer, &p.Phone,
		&p.Skills, &p.Languages, &p.WebsiteURL, &p.LinkedInURL, &p.GitHubURL,
		&p.DesiredSalaryMinor, &currency,
		&openTo, &p.OpenToRemote, &workAuth,
		&p.Visibility.Discoverable, &p.Visibility.HideCurrentEmployer, &p.Visibility.HideFromCompanies,
		&p.Version, &p.DeletedAt, &p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		return domain.Profile{}, err
	}

	if currency != nil {
		p.DesiredSalaryCurrency = *currency
	}
	p.WorkAuthorisation = domain.WorkAuthorisation(workAuth)
	p.OpenToTypes = make([]domain.EmploymentType, 0, len(openTo))
	for _, t := range openTo {
		p.OpenToTypes = append(p.OpenToTypes, domain.EmploymentType(t))
	}
	p.Skills = orEmpty(p.Skills)
	p.Languages = orEmpty(p.Languages)
	p.Visibility.HideFromCompanies = orEmpty(p.Visibility.HideFromCompanies)

	return p, nil
}

// EnsureProfile creates the shell a newly registered candidate needs.
//
// It reports whether the row was created so the registration consumer can stay
// quiet on a redelivery instead of publishing a second "profile updated".
func (s *Store) EnsureProfile(ctx context.Context, accountID, email, fullName string) (domain.Profile, bool, error) {
	// ON CONFLICT DO NOTHING would return no row, so the update is a no-op write
	// that still returns one — cheaper than a second round trip, and it lets a
	// late-arriving name or address fill a shell that was created without them.
	query := `
		INSERT INTO candidate_profiles (id, account_id, email, full_name)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (account_id) DO UPDATE
		SET email     = CASE WHEN candidate_profiles.email = '' THEN excluded.email ELSE candidate_profiles.email END,
		    full_name = CASE WHEN candidate_profiles.full_name = '' THEN excluded.full_name ELSE candidate_profiles.full_name END
		RETURNING ` + profileColumns + `, (xmax = 0) AS inserted`

	var p domain.Profile
	var created bool

	row := s.pool.QueryRow(ctx, query, idgen.New("cand"), accountID, email, fullName)
	p, err := scanProfileWithFlag(row, &created)
	if err != nil {
		return domain.Profile{}, false, fmt.Errorf("store: ensure profile: %w", err)
	}
	return p, created, nil
}

// scanProfileWithFlag scans the profile columns plus one trailing boolean.
func scanProfileWithFlag(row pgx.Row, flag *bool) (domain.Profile, error) {
	return scanProfile(rowWithExtra{row: row, extra: []any{flag}})
}

// rowWithExtra appends destinations to a Scan so the profile column list can be
// reused by a query that returns one more column.
type rowWithExtra struct {
	row   pgx.Row
	extra []any
}

func (r rowWithExtra) Scan(dest ...any) error {
	return r.row.Scan(append(dest, r.extra...)...)
}

// FindProfileByAccount returns a candidate's own profile.
func (s *Store) FindProfileByAccount(ctx context.Context, accountID string) (domain.Profile, error) {
	query := `SELECT ` + profileColumns + ` FROM candidate_profiles WHERE account_id = $1`

	profile, err := scanProfile(s.pool.QueryRow(ctx, query, accountID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Profile{}, domain.ErrProfileNotFound
		}
		return domain.Profile{}, fmt.Errorf("store: find profile by account: %w", err)
	}
	if profile.DeletedAt != nil {
		return domain.Profile{}, domain.ErrProfileDeleted
	}
	return profile, nil
}

// AccountIDForProfile maps a profile id onto the account behind it.
//
// The account id is never in a search result — a company learns that a suitable
// candidate exists, not who they are. But an approach has to be delivered to
// someone, so this lookup stays inside the service and its answer goes only into
// the event, never into a response.
func (s *Store) AccountIDForProfile(ctx context.Context, profileID string) (string, error) {
	var accountID string
	err := s.pool.QueryRow(ctx,
		`SELECT account_id FROM candidate_profiles WHERE id = $1 AND deleted_at IS NULL`,
		profileID).Scan(&accountID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", domain.ErrProfileNotFound
		}
		return "", fmt.Errorf("store: account for profile: %w", err)
	}
	return accountID, nil
}

// UpdateProfile applies a partial update.
//
// Every field is COALESCEd against its current value, so one statement serves a
// PATCH of any shape and an omitted field is never confused with a cleared one.
func (s *Store) UpdateProfile(ctx context.Context, accountID string, in domain.ProfileInput) (domain.Profile, error) {
	salaryMinor, salaryCurrency := salaryParams(in)

	query := `
		UPDATE candidate_profiles SET
			headline                = COALESCE($2, headline),
			summary                 = COALESCE($3, summary),
			location                = COALESCE($4, location),
			years_experience        = COALESCE($5, years_experience),
			current_title           = COALESCE($6, current_title),
			current_employer        = COALESCE($7, current_employer),
			phone                   = COALESCE($8, phone),
			skills                  = COALESCE($9::text[], skills),
			languages               = COALESCE($10::text[], languages),
			website_url             = COALESCE($11, website_url),
			linkedin_url            = COALESCE($12, linkedin_url),
			github_url              = COALESCE($13, github_url),
			desired_salary_minor    = CASE WHEN $14::boolean THEN $15::bigint ELSE desired_salary_minor END,
			desired_salary_currency = CASE WHEN $14::boolean THEN $16::char(3) ELSE desired_salary_currency END,
			open_to_types           = COALESCE($17::employment_type[], open_to_types),
			open_to_remote          = COALESCE($18, open_to_remote),
			work_authorisation      = COALESCE($19::work_authorisation, work_authorisation),
			version                 = version + 1
		WHERE account_id = $1 AND deleted_at IS NULL
		RETURNING ` + profileColumns

	profile, err := scanProfile(s.pool.QueryRow(ctx, query,
		accountID,
		in.Headline, in.Summary, in.Location, in.YearsExperience,
		in.CurrentTitle, in.CurrentEmployer, in.Phone,
		stringsParam(in.Skills), stringsParam(in.Languages),
		in.WebsiteURL, in.LinkedInURL, in.GitHubURL,
		in.DesiredSalaryMinor != nil || in.DesiredSalaryCurrency != nil, salaryMinor, salaryCurrency,
		employmentTypesParam(in.OpenToTypes), in.OpenToRemote,
		workAuthParam(in.WorkAuthorisation),
	))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Profile{}, domain.ErrProfileNotFound
		}
		return domain.Profile{}, fmt.Errorf("store: update profile: %w", err)
	}
	return profile, nil
}

// UpdateVisibility replaces the candidate's discoverability settings.
func (s *Store) UpdateVisibility(ctx context.Context, accountID string, v domain.Visibility) (domain.Profile, error) {
	query := `
		UPDATE candidate_profiles SET
			discoverable          = $2,
			hide_current_employer = $3,
			hide_from_companies   = $4::uuid[],
			version               = version + 1
		WHERE account_id = $1 AND deleted_at IS NULL
		RETURNING ` + profileColumns

	profile, err := scanProfile(s.pool.QueryRow(ctx, query,
		accountID, v.Discoverable, v.HideCurrentEmployer, orEmpty(v.HideFromCompanies)))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Profile{}, domain.ErrProfileNotFound
		}
		return domain.Profile{}, fmt.Errorf("store: update visibility: %w", err)
	}
	return profile, nil
}

// SoftDeleteProfile retires a profile and takes it out of discovery.
//
// The row survives because approaches and applications reference it; clearing
// discoverable in the same statement is what makes the deletion immediate as far
// as any company is concerned.
func (s *Store) SoftDeleteProfile(ctx context.Context, accountID string) (domain.Profile, error) {
	query := `
		UPDATE candidate_profiles SET
			deleted_at   = now(),
			discoverable = false,
			version      = version + 1
		WHERE account_id = $1 AND deleted_at IS NULL
		RETURNING ` + profileColumns

	profile, err := scanProfile(s.pool.QueryRow(ctx, query, accountID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Profile{}, domain.ErrProfileNotFound
		}
		return domain.Profile{}, fmt.Errorf("store: delete profile: %w", err)
	}
	return profile, nil
}

func salaryParams(in domain.ProfileInput) (any, any) {
	if in.DesiredSalaryMinor == nil && in.DesiredSalaryCurrency == nil {
		return nil, nil
	}
	// A zero amount or a blank currency is how a candidate withdraws the figure
	// they published, so both columns go back to NULL together.
	if in.DesiredSalaryMinor == nil || *in.DesiredSalaryMinor == 0 ||
		in.DesiredSalaryCurrency == nil || *in.DesiredSalaryCurrency == "" {
		return nil, nil
	}
	return *in.DesiredSalaryMinor, *in.DesiredSalaryCurrency
}

func stringsParam(values *[]string) any {
	if values == nil {
		return nil
	}
	return orEmpty(*values)
}

func employmentTypesParam(values *[]domain.EmploymentType) any {
	if values == nil {
		return nil
	}
	out := make([]string, 0, len(*values))
	for _, v := range *values {
		out = append(out, string(v))
	}
	return out
}

func workAuthParam(value *domain.WorkAuthorisation) any {
	if value == nil {
		return nil
	}
	return string(*value)
}
