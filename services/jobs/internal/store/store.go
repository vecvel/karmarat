// Package store is the jobs service's persistence layer.
//
// Every tenant-scoped query takes the company identifier as an explicit
// argument and carries it in the WHERE clause, rather than loading a row and
// checking its owner afterwards. A load-then-check can be forgotten at a new
// call site; a missing argument here is a compile error, and a filtered query
// answers "not found" instead of confirming that another tenant's row exists.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/karmarat/platform/packages/goshared/idgen"
	"github.com/karmarat/platform/services/jobs/internal/domain"
)

// Store owns the connection pool.
type Store struct {
	pool *pgxpool.Pool
}

// New builds a store over an existing pool.
func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Pool exposes the underlying pool for health checks.
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

// InTx runs fn inside a transaction, rolling back on error.
func (s *Store) InTx(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("store: begin transaction: %w", err)
	}

	if err := fn(tx); err != nil {
		// The rollback must not be cancelled along with the request.
		_ = tx.Rollback(context.WithoutCancel(ctx))
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("store: commit transaction: %w", err)
	}
	return nil
}

// Index names the schema relies on to tell one uniqueness failure from another.
const (
	companySlugIndex = "jobs_company_slug_idx"
	networkSlugIndex = "jobs_network_slug_idx"
)

func uniqueViolation(err error) (*pgconn.PgError, bool) {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return pgErr, true
	}
	return nil, false
}

// translateUnique maps a constraint name onto the domain error a client sees.
func translateUnique(err error) error {
	pgErr, ok := uniqueViolation(err)
	if !ok {
		return err
	}
	switch pgErr.ConstraintName {
	case companySlugIndex:
		return domain.ErrSlugTaken
	case networkSlugIndex:
		return domain.ErrNetworkSlugTaken
	default:
		return err
	}
}

/* -------------------------------------------------------------------------- */
/* Reading                                                                    */
/* -------------------------------------------------------------------------- */

const jobColumns = `
	id, company_id, slug, title, department, locations, work_mode, employment_type, seniority,
	description, requirements, salary_min, salary_max, coalesce(salary_currency, ''), salary_is_public,
	headcount, hiring_manager_id, recruiter_id, internal_notes, status,
	visible_on_portal, visible_on_network, form, opened_at, closed_at,
	created_by, created_at, updated_at`

func scanJob(row pgx.Row) (domain.Job, error) {
	var (
		job     domain.Job
		rawForm []byte
	)

	err := row.Scan(
		&job.ID, &job.CompanyID, &job.Slug, &job.Title, &job.Department, &job.Locations,
		&job.WorkMode, &job.EmploymentType, &job.Seniority,
		&job.Description, &job.Requirements,
		&job.Salary.Min, &job.Salary.Max, &job.Salary.Currency, &job.Salary.Public,
		&job.Headcount, &job.HiringManagerID, &job.RecruiterID, &job.InternalNotes, &job.Status,
		&job.VisibleOnPortal, &job.VisibleOnNetwork, &rawForm, &job.OpenedAt, &job.ClosedAt,
		&job.CreatedBy, &job.CreatedAt, &job.UpdatedAt,
	)
	if err != nil {
		return domain.Job{}, err
	}

	if err := json.Unmarshal(rawForm, &job.Form); err != nil {
		return domain.Job{}, fmt.Errorf("store: decode application form: %w", err)
	}
	if job.Locations == nil {
		job.Locations = []string{}
	}
	return job, nil
}

// FindByID loads one of the company's jobs.
func (s *Store) FindByID(ctx context.Context, companyID, id string) (domain.Job, error) {
	query := `SELECT ` + jobColumns + ` FROM jobs WHERE id = $1 AND company_id = $2`

	job, err := scanJob(s.pool.QueryRow(ctx, query, id, companyID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Job{}, domain.ErrJobNotFound
		}
		return domain.Job{}, fmt.Errorf("store: find job: %w", err)
	}
	return job, nil
}

// FindForService loads a job by id alone, for the internal service endpoint.
//
// The caller is another platform service holding the shared secret, and it knows
// only the job id — it has no tenant of its own to filter by. The response
// carries company_id so the caller can check the job against the application it
// is validating; this is the one read in the service that is not tenant-scoped,
// and it is reachable only from behind the internal-token guard.
func (s *Store) FindForService(ctx context.Context, id string) (domain.Job, error) {
	query := `SELECT ` + jobColumns + ` FROM jobs WHERE id = $1`

	job, err := scanJob(s.pool.QueryRow(ctx, query, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Job{}, domain.ErrJobNotFound
		}
		return domain.Job{}, fmt.Errorf("store: find job for service: %w", err)
	}
	return job, nil
}

// VisibilityFilter narrows a listing by publication surface.
type VisibilityFilter string

const (
	VisibilityAny     VisibilityFilter = ""
	VisibilityPortal  VisibilityFilter = "portal"
	VisibilityNetwork VisibilityFilter = "network"
	VisibilityBoth    VisibilityFilter = "both"
	VisibilityNone    VisibilityFilter = "none"
)

// Valid reports whether the visibility filter is one the service accepts.
func (v VisibilityFilter) Valid() bool {
	switch v {
	case VisibilityAny, VisibilityPortal, VisibilityNetwork, VisibilityBoth, VisibilityNone:
		return true
	default:
		return false
	}
}

// ListFilter narrows a company's requisition list.
type ListFilter struct {
	Status     domain.Status
	Department string
	Visibility VisibilityFilter
	Query      string
	Limit      int
	// Cursor is the last id of the previous page; results are ordered newest
	// first and ids sort by creation time, so one column keys the whole page.
	Cursor string
	// IncludeArchived keeps soft-deleted rows out of the default view without
	// making them unreachable.
	IncludeArchived bool
}

// Page is one page of results plus the cursor that continues it.
type Page struct {
	Jobs       []domain.Job
	NextCursor string
}

// List returns a page of the company's requisitions, newest first.
func (s *Store) List(ctx context.Context, companyID string, filter ListFilter) (Page, error) {
	b := newBuilder()
	b.where("company_id = " + b.arg(companyID))

	if filter.Status != "" {
		b.where("status = " + b.arg(filter.Status))
	} else if !filter.IncludeArchived {
		b.where("status <> 'archived'")
	}
	if filter.Department != "" {
		b.where("lower(department) = lower(" + b.arg(filter.Department) + ")")
	}
	applyVisibility(b, filter.Visibility)
	applySearch(b, filter.Query)
	if filter.Cursor != "" {
		b.where("id < " + b.arg(filter.Cursor))
	}

	limit := clampLimit(filter.Limit)
	query := `SELECT ` + jobColumns + ` FROM jobs ` + b.clause() +
		` ORDER BY id DESC LIMIT ` + b.arg(limit+1)

	return s.page(ctx, query, b.args(), limit)
}

// ExportAll returns every matching requisition for a CSV download.
//
// Export is a report rather than a listing, so it is not cursor-paginated — but
// it is still capped, because an unbounded download is an easy way to turn one
// request into a database-sized response.
const exportCap = 5000

// ExportAll returns up to exportCap requisitions matching the filter.
func (s *Store) ExportAll(ctx context.Context, companyID string, filter ListFilter) ([]domain.Job, error) {
	b := newBuilder()
	b.where("company_id = " + b.arg(companyID))
	if filter.Status != "" {
		b.where("status = " + b.arg(filter.Status))
	} else if !filter.IncludeArchived {
		b.where("status <> 'archived'")
	}
	if filter.Department != "" {
		b.where("lower(department) = lower(" + b.arg(filter.Department) + ")")
	}
	applyVisibility(b, filter.Visibility)
	applySearch(b, filter.Query)

	query := `SELECT ` + jobColumns + ` FROM jobs ` + b.clause() +
		` ORDER BY id DESC LIMIT ` + b.arg(exportCap)

	page, err := s.page(ctx, query, b.args(), exportCap)
	return page.Jobs, err
}

// PublicFilter narrows the unauthenticated job boards.
type PublicFilter struct {
	// CompanyID scopes the query to one careers portal. Empty means the shared
	// network board, which spans every tenant that opted in.
	CompanyID      string
	Query          string
	Location       string
	EmploymentType domain.EmploymentType
	Department     string
	WorkMode       domain.WorkMode
	Limit          int
	Cursor         string
}

// ListPublic returns a page of the public board.
//
// The status and visibility predicates are not optional and not caller-supplied:
// an unpublished or closed requisition has no public existence, and making that
// a parameter would be one query-string edit away from leaking drafts.
func (s *Store) ListPublic(ctx context.Context, filter PublicFilter) (Page, error) {
	b := newBuilder()
	b.where("status = 'open'")

	if filter.CompanyID != "" {
		b.where("company_id = " + b.arg(filter.CompanyID))
		b.where("visible_on_portal")
	} else {
		b.where("visible_on_network")
	}

	if filter.Department != "" {
		b.where("lower(department) = lower(" + b.arg(filter.Department) + ")")
	}
	if filter.EmploymentType != "" {
		b.where("employment_type = " + b.arg(filter.EmploymentType))
	}
	if filter.WorkMode != "" {
		b.where("work_mode = " + b.arg(filter.WorkMode))
	}
	if filter.Location != "" {
		// A remote role has no location to match, yet "remote" is the single most
		// common thing a candidate types into a location box.
		arg := b.arg(filter.Location)
		b.where(`(EXISTS (SELECT 1 FROM unnest(locations) AS l WHERE l ILIKE '%' || ` + arg + ` || '%')
			OR (lower(` + arg + `) = 'remote' AND work_mode = 'remote'))`)
	}
	applySearch(b, filter.Query)
	if filter.Cursor != "" {
		b.where("id < " + b.arg(filter.Cursor))
	}

	limit := clampLimit(filter.Limit)
	query := `SELECT ` + jobColumns + ` FROM jobs ` + b.clause() +
		` ORDER BY id DESC LIMIT ` + b.arg(limit+1)

	return s.page(ctx, query, b.args(), limit)
}

// FindPublicBySlug resolves one job on a public surface.
//
// companyID empty means the shared board, where the slug is globally unique
// among network-visible jobs; otherwise it is a company's own careers portal,
// where the slug is unique within the tenant. In both cases the requirement is
// part of the predicate, so a job that is merely on the other surface answers
// "not found" rather than leaking through the wrong door.
func (s *Store) FindPublicBySlug(ctx context.Context, companyID, slug string) (domain.Job, error) {
	var (
		query string
		args  []any
	)

	if companyID == "" {
		query = `SELECT ` + jobColumns + ` FROM jobs
			WHERE slug = $1 AND visible_on_network AND status = 'open'`
		args = []any{slug}
	} else {
		query = `SELECT ` + jobColumns + ` FROM jobs
			WHERE company_id = $1 AND slug = $2 AND visible_on_portal AND status = 'open'`
		args = []any{companyID, slug}
	}

	job, err := scanJob(s.pool.QueryRow(ctx, query, args...))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Job{}, domain.ErrJobNotFound
		}
		return domain.Job{}, fmt.Errorf("store: find public job: %w", err)
	}
	return job, nil
}

func (s *Store) page(ctx context.Context, query string, args []any, limit int) (Page, error) {
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return Page{}, fmt.Errorf("store: list jobs: %w", err)
	}
	defer rows.Close()

	jobs := make([]domain.Job, 0, limit)
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return Page{}, fmt.Errorf("store: scan job: %w", err)
		}
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		return Page{}, fmt.Errorf("store: list jobs: %w", err)
	}

	// One row beyond the page proves there is a next page without a second
	// query and without a count over the whole table.
	page := Page{Jobs: jobs}
	if len(jobs) > limit {
		page.Jobs = jobs[:limit]
		page.NextCursor = page.Jobs[limit-1].ID
	}
	return page, nil
}

/* -------------------------------------------------------------------------- */
/* Writing                                                                    */
/* -------------------------------------------------------------------------- */

// CreateInput is a new requisition.
type CreateInput struct {
	Draft domain.JobDraft
	Form  domain.ApplicationForm
	// SlugExplicit distinguishes a slug the caller chose from one derived from
	// the title. A chosen slug that collides is a 409 the caller must resolve;
	// a derived one is disambiguated silently, because a recruiter opening a
	// second "Software Engineer" req did not ask to think about URLs.
	SlugExplicit bool
	CreatedBy    string
}

// Create inserts a requisition and returns it as stored.
func (s *Store) Create(ctx context.Context, companyID string, in CreateInput) (domain.Job, error) {
	rawForm, err := json.Marshal(in.Form)
	if err != nil {
		return domain.Job{}, fmt.Errorf("store: encode application form: %w", err)
	}

	id := idgen.New("job")
	query := `
		INSERT INTO jobs (
			id, company_id, slug, title, department, locations, work_mode, employment_type, seniority,
			description, requirements, salary_min, salary_max, salary_currency, salary_is_public,
			headcount, hiring_manager_id, recruiter_id, internal_notes, status, form, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,'draft',$20,$21)
		RETURNING ` + jobColumns

	slug := in.Draft.Slug
	for attempt := 0; ; attempt++ {
		job, err := scanJob(s.pool.QueryRow(ctx, query,
			id, companyID, slug, in.Draft.Title, in.Draft.Department, in.Draft.Locations,
			in.Draft.WorkMode, in.Draft.EmploymentType, in.Draft.Seniority,
			in.Draft.Description, in.Draft.Requirements,
			in.Draft.Salary.Min, in.Draft.Salary.Max, nullableCurrency(in.Draft.Salary),
			in.Draft.Salary.Public, in.Draft.Headcount,
			in.Draft.HiringManagerID, in.Draft.RecruiterID, in.Draft.InternalNotes,
			rawForm, in.CreatedBy,
		))
		if err == nil {
			return job, nil
		}

		// A new job is a draft on no board, so the network index cannot fire;
		// the only uniqueness failure possible here is the tenant's own slug.
		if !errors.Is(translateUnique(err), domain.ErrSlugTaken) {
			return domain.Job{}, fmt.Errorf("store: create job: %w", err)
		}
		if in.SlugExplicit || attempt >= slugAttempts {
			return domain.Job{}, domain.ErrSlugTaken
		}
		slug = disambiguate(in.Draft.Slug, attempt+1)
	}
}

// slugAttempts bounds silent disambiguation. Past it the caller is told the
// slug is taken rather than left in a retry loop.
const slugAttempts = 6

// withJob loads a job for update and hands it to fn inside one transaction.
//
// The row is locked so a concurrent PATCH cannot overwrite the field this one is
// reading; the tenant predicate is on the SELECT, so fn never sees a row it is
// not entitled to and cannot forget to check.
func (s *Store) withJob(
	ctx context.Context,
	companyID, id string,
	fn func(ctx context.Context, tx pgx.Tx, current domain.Job) (domain.Job, error),
) (domain.Job, error) {
	var updated domain.Job

	err := s.InTx(ctx, func(tx pgx.Tx) error {
		query := `SELECT ` + jobColumns + ` FROM jobs
			WHERE id = $1 AND company_id = $2 FOR UPDATE`

		current, err := scanJob(tx.QueryRow(ctx, query, id, companyID))
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return domain.ErrJobNotFound
			}
			return fmt.Errorf("store: load job for update: %w", err)
		}

		updated, err = fn(ctx, tx, current)
		return err
	})

	return updated, err
}

// UpdateContent applies a validated draft to an existing requisition.
//
// The draft is produced by fn from the current row, so a PATCH is computed
// against the row it will actually overwrite rather than against a copy read in
// an earlier request.
func (s *Store) UpdateContent(
	ctx context.Context,
	companyID, id string,
	fn func(current domain.Job) (domain.JobDraft, error),
) (domain.Job, error) {
	return s.withJob(ctx, companyID, id, func(ctx context.Context, tx pgx.Tx, current domain.Job) (domain.Job, error) {
		draft, err := fn(current)
		if err != nil {
			return domain.Job{}, err
		}

		query := `
			UPDATE jobs SET
				slug = $3, title = $4, department = $5, locations = $6, work_mode = $7,
				employment_type = $8, seniority = $9, description = $10, requirements = $11,
				salary_min = $12, salary_max = $13, salary_currency = $14, salary_is_public = $15,
				headcount = $16, hiring_manager_id = $17, recruiter_id = $18, internal_notes = $19,
				updated_at = now()
			WHERE id = $1 AND company_id = $2
			RETURNING ` + jobColumns

		job, err := scanJob(tx.QueryRow(ctx, query,
			id, companyID, draft.Slug, draft.Title, draft.Department, draft.Locations, draft.WorkMode,
			draft.EmploymentType, draft.Seniority, draft.Description, draft.Requirements,
			draft.Salary.Min, draft.Salary.Max, nullableCurrency(draft.Salary), draft.Salary.Public,
			draft.Headcount, draft.HiringManagerID, draft.RecruiterID, draft.InternalNotes,
		))
		if err != nil {
			// A slug that changes on update was always chosen deliberately, so a
			// collision is reported rather than worked around.
			translated := translateUnique(err)
			if errors.Is(translated, domain.ErrSlugTaken) || errors.Is(translated, domain.ErrNetworkSlugTaken) {
				return domain.Job{}, translated
			}
			return domain.Job{}, fmt.Errorf("store: update job: %w", err)
		}
		return job, nil
	})
}

// ReplaceForm swaps the application form and bumps its version.
func (s *Store) ReplaceForm(
	ctx context.Context,
	companyID, id string,
	fn func(current domain.Job) (domain.ApplicationForm, error),
) (domain.Job, error) {
	return s.withJob(ctx, companyID, id, func(ctx context.Context, tx pgx.Tx, current domain.Job) (domain.Job, error) {
		form, err := fn(current)
		if err != nil {
			return domain.Job{}, err
		}

		// The version is owned here rather than by the caller: an application
		// records the version it answered, and a client that could choose the
		// number could make two different forms claim to be the same one.
		form.Version = current.Form.Version + 1

		rawForm, err := json.Marshal(form)
		if err != nil {
			return domain.Job{}, fmt.Errorf("store: encode application form: %w", err)
		}

		query := `UPDATE jobs SET form = $3, updated_at = now()
			WHERE id = $1 AND company_id = $2 RETURNING ` + jobColumns

		job, err := scanJob(tx.QueryRow(ctx, query, id, companyID, rawForm))
		if err != nil {
			return domain.Job{}, fmt.Errorf("store: replace application form: %w", err)
		}
		return job, nil
	})
}

// SetVisibility applies a publication decision.
//
// fn returns the requested surfaces and the status the job should land in; it
// runs against the locked row, so the permission the API checked was checked
// against the visibility that is actually about to change.
func (s *Store) SetVisibility(
	ctx context.Context,
	companyID, id string,
	fn func(current domain.Job) (domain.Visibility, domain.Status, error),
) (domain.Job, error) {
	return s.withJob(ctx, companyID, id, func(ctx context.Context, tx pgx.Tx, current domain.Job) (domain.Job, error) {
		visibility, status, err := fn(current)
		if err != nil {
			return domain.Job{}, err
		}

		// $5 is cast to job_status at both uses. Without the casts Postgres
		// deduces one type from `status = $5` and another from `$5 = 'open'`
		// and refuses the statement outright (SQLSTATE 42P08).
		query := `
			UPDATE jobs SET
				visible_on_portal = $3,
				visible_on_network = $4,
				status = $5::job_status,
				-- opened_at is the publication date a candidate sees; it is set
				-- once and never moved by a later re-publish.
				opened_at = CASE
					WHEN opened_at IS NULL AND $5::job_status = 'open' THEN now()
					ELSE opened_at
				END,
				updated_at = now()
			WHERE id = $1 AND company_id = $2
			RETURNING ` + jobColumns

		job, err := scanJob(tx.QueryRow(ctx, query, id, companyID, visibility.Portal, visibility.Network, status))
		if err != nil {
			// Going live on the shared board is where two tenants can collide on
			// one public link; the partial index decides, so the loser is told.
			if translated := translateUnique(err); errors.Is(translated, domain.ErrNetworkSlugTaken) {
				return domain.Job{}, translated
			}
			return domain.Job{}, fmt.Errorf("store: set job visibility: %w", err)
		}
		return job, nil
	})
}

// Close ends a requisition and takes it off both boards.
func (s *Store) Close(ctx context.Context, companyID, id string,
	check func(current domain.Job) error) (domain.Job, error) {
	return s.transition(ctx, companyID, id, domain.StatusClosed, check)
}

// Archive is the soft delete: the row stays for reporting, the job disappears.
//
// It takes no precondition — deleting something already deleted is the caller's
// intent already holding, not a conflict worth a 409.
func (s *Store) Archive(ctx context.Context, companyID, id string) (domain.Job, error) {
	return s.transition(ctx, companyID, id, domain.StatusArchived, nil)
}

func (s *Store) transition(ctx context.Context, companyID, id string, status domain.Status,
	check func(current domain.Job) error) (domain.Job, error) {
	return s.withJob(ctx, companyID, id, func(ctx context.Context, tx pgx.Tx, current domain.Job) (domain.Job, error) {
		if check != nil {
			if err := check(current); err != nil {
				return domain.Job{}, err
			}
		}

		// Visibility is cleared with the status in one statement. Leaving a
		// closed job listed would take applications for a role nobody will read,
		// and it would hold the network slug against a live requisition.
		query := `
			UPDATE jobs SET
				status = $3,
				visible_on_portal = false,
				visible_on_network = false,
				closed_at = coalesce(closed_at, now()),
				updated_at = now()
			WHERE id = $1 AND company_id = $2
			RETURNING ` + jobColumns

		job, err := scanJob(tx.QueryRow(ctx, query, id, companyID, status))
		if err != nil {
			return domain.Job{}, fmt.Errorf("store: transition job to %s: %w", status, err)
		}
		return job, nil
	})
}

// Duplicate clones a requisition into a new draft.
//
// The copy is deliberately unpublished and unslugged-from-the-original: cloning
// a live job must not put a second version of it on a board, and two rows cannot
// share a link.
func (s *Store) Duplicate(ctx context.Context, companyID, id, createdBy string) (domain.Job, error) {
	source, err := s.FindByID(ctx, companyID, id)
	if err != nil {
		return domain.Job{}, err
	}

	draft := domain.JobDraft{
		Title:           truncateTitle(source.Title + " (copy)"),
		Department:      source.Department,
		Locations:       source.Locations,
		WorkMode:        source.WorkMode,
		EmploymentType:  source.EmploymentType,
		Seniority:       source.Seniority,
		Description:     source.Description,
		Requirements:    source.Requirements,
		Salary:          source.Salary,
		Headcount:       source.Headcount,
		HiringManagerID: source.HiringManagerID,
		RecruiterID:     source.RecruiterID,
		InternalNotes:   source.InternalNotes,
	}
	draft.Normalize()

	form := source.Form
	form.Version = 1

	return s.Create(ctx, companyID, CreateInput{Draft: draft, Form: form, CreatedBy: createdBy})
}

/* -------------------------------------------------------------------------- */
/* Helpers                                                                    */
/* -------------------------------------------------------------------------- */

// clampLimit applies the platform's pagination bounds.
func clampLimit(limit int) int {
	switch {
	case limit <= 0:
		return 25
	case limit > 100:
		return 100
	default:
		return limit
	}
}

func nullableCurrency(salary domain.SalaryRange) any {
	if salary.Currency == "" {
		return nil
	}
	return salary.Currency
}

// disambiguate appends a suffix to a slug that is already taken.
//
// The first few attempts read naturally ("-2", "-3"); past those the collision
// is unusual enough that a random suffix is better than counting forever.
func disambiguate(base string, attempt int) string {
	var suffix string
	if attempt < 5 {
		suffix = "-" + string(rune('0'+attempt+1))
	} else {
		suffix = "-" + strings.ToLower(idgen.NewRaw()[20:])
	}

	trimmed := base
	if len(trimmed)+len(suffix) > domain.MaxSlugLength {
		trimmed = strings.Trim(trimmed[:domain.MaxSlugLength-len(suffix)], "-")
	}
	return trimmed + suffix
}

func truncateTitle(title string) string {
	runes := []rune(title)
	if len(runes) <= domain.MaxTitleLength {
		return title
	}
	return string(runes[:domain.MaxTitleLength])
}

// applyVisibility turns a visibility filter into predicates.
func applyVisibility(b *builder, filter VisibilityFilter) {
	switch filter {
	case VisibilityPortal:
		b.where("visible_on_portal")
	case VisibilityNetwork:
		b.where("visible_on_network")
	case VisibilityBoth:
		b.where("visible_on_portal AND visible_on_network")
	case VisibilityNone:
		b.where("NOT visible_on_portal AND NOT visible_on_network")
	}
}

// applySearch adds the free-text predicate.
//
// Full text handles whole words through the GIN index; the ILIKE covers the
// half-typed word a search-as-you-type box sends, which no stemmer will match.
func applySearch(b *builder, query string) {
	query = strings.TrimSpace(query)
	if query == "" {
		return
	}

	arg := b.arg(query)
	b.where(`(to_tsvector('english', title || ' ' || department || ' ' || description)
		@@ websearch_to_tsquery('english', ` + arg + `)
		OR title ILIKE '%' || ` + arg + ` || '%')`)
}

// builder assembles a parameterized WHERE clause.
//
// Predicates are fixed strings and every value goes through arg(), so a filter
// cannot become an injection however it was spelled in the query string.
type builder struct {
	conditions []string
	values     []any
}

func newBuilder() *builder { return &builder{} }

func (b *builder) arg(value any) string {
	b.values = append(b.values, value)
	return "$" + itoa(len(b.values))
}

func (b *builder) where(condition string) {
	b.conditions = append(b.conditions, condition)
}

func (b *builder) clause() string {
	if len(b.conditions) == 0 {
		return ""
	}
	return "WHERE " + strings.Join(b.conditions, " AND ")
}

func (b *builder) args() []any { return b.values }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// PurgeArchived removes archived requisitions past their retention window.
//
// An archived job is kept long enough to restore or report on, then removed:
// it holds a slug in the tenant's namespace and a copy of a form nobody will
// fill in again.
func (s *Store) PurgeArchived(ctx context.Context, olderThan time.Duration) (int64, error) {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM jobs WHERE status = 'archived' AND updated_at < now() - $1::interval`,
		olderThan.String())
	if err != nil {
		return 0, fmt.Errorf("store: purge archived jobs: %w", err)
	}
	return tag.RowsAffected(), nil
}
