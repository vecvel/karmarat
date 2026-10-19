// Package api exposes the jobs service over HTTP.
//
// Three surfaces share one mux and differ in what they trust: the company
// endpoints trust the gateway's principal headers, the public endpoints trust
// nothing but the gateway's resolved tenant, and the internal endpoint trusts a
// shared secret. They are kept in one place so the difference between them is
// visible in the routing table rather than spread across packages.
package api

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/karmarat/platform/packages/goshared/httpx"
	"github.com/karmarat/platform/packages/goshared/tenancy"
	"github.com/karmarat/platform/services/jobs/internal/domain"
	"github.com/karmarat/platform/services/jobs/internal/events"
	"github.com/karmarat/platform/services/jobs/internal/store"
)

// API wires the jobs handlers.
type API struct {
	store         *store.Store
	publisher     *events.Publisher
	logger        *slog.Logger
	internalToken string
}

// Config configures the API.
type Config struct {
	Store     *store.Store
	Publisher *events.Publisher
	Logger    *slog.Logger
	// InternalToken authenticates calls from other platform services.
	InternalToken string
}

// New builds the API.
func New(cfg Config) *API {
	return &API{
		store:         cfg.Store,
		publisher:     cfg.Publisher,
		logger:        cfg.Logger,
		internalToken: cfg.InternalToken,
	}
}

// Routes returns the service's HTTP handler.
//
// Each company route is wrapped the same way: reconstruct the principal, insist
// it is a company one, then check the permission. The handler filters by tenant
// again regardless — the permission says what this role may do, the filter says
// whose data it may do it to.
func (a *API) Routes() http.Handler {
	mux := http.NewServeMux()

	company := func(permission string, handler http.HandlerFunc) http.Handler {
		return httpx.TrustGatewayHeaders(
			httpx.RequirePrincipal(tenancy.PrincipalCompany)(
				httpx.RequirePermission(permission)(handler)))
	}

	// The export route is registered before the `{id}` route so "export" is not
	// read as a job identifier.
	mux.Handle("GET /v1/jobs/export", company(domain.PermissionExport, a.handleExport))

	mux.Handle("GET /v1/jobs", company(domain.PermissionRead, a.handleList))
	mux.Handle("POST /v1/jobs", company(domain.PermissionCreate, a.handleCreate))
	mux.Handle("GET /v1/jobs/{id}", company(domain.PermissionRead, a.handleGet))
	mux.Handle("PATCH /v1/jobs/{id}", company(domain.PermissionUpdate, a.handleUpdate))
	mux.Handle("DELETE /v1/jobs/{id}", company(domain.PermissionDelete, a.handleArchive))
	mux.Handle("POST /v1/jobs/{id}/duplicate", company(domain.PermissionDuplicate, a.handleDuplicate))
	mux.Handle("GET /v1/jobs/{id}/form", company(domain.PermissionRead, a.handleGetForm))
	mux.Handle("PUT /v1/jobs/{id}/form", company(domain.PermissionManageForm, a.handleReplaceForm))
	mux.Handle("POST /v1/jobs/{id}/close", company(domain.PermissionUpdate, a.handleClose))

	// Publishing is guarded inside the handler instead of by middleware: which
	// of jobs.publish_portal and jobs.publish_network is required depends on the
	// job's current visibility, which only a read of the row can tell us.
	mux.Handle("POST /v1/jobs/{id}/publish",
		httpx.TrustGatewayHeaders(
			httpx.RequirePrincipal(tenancy.PrincipalCompany)(http.HandlerFunc(a.handlePublish))))

	// Public: no principal at all. The gateway exposes these on the jobs board
	// and on every company's careers portal.
	mux.HandleFunc("GET /v1/public/jobs", a.handlePublicList)
	mux.HandleFunc("GET /v1/public/jobs/{slug}", a.handlePublicJob)
	mux.HandleFunc("GET /v1/public/jobs/{slug}/form", a.handlePublicForm)

	// Internal: the applications service validating a submission, never a browser.
	mux.Handle("GET /internal/jobs/{id}", a.internal(http.HandlerFunc(a.handleInternalJob)))

	return mux
}

/* -------------------------------------------------------------------------- */
/* Listing and reading                                                        */
/* -------------------------------------------------------------------------- */

func (a *API) handleList(w http.ResponseWriter, r *http.Request) {
	companyID, err := companyOf(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	filter, err := parseListFilter(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	page, err := a.store.List(r.Context(), companyID, filter)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	summaries := make([]jobSummary, 0, len(page.Jobs))
	for _, job := range page.Jobs {
		summaries = append(summaries, toJobSummary(job))
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"jobs":       summaries,
		"nextCursor": page.NextCursor,
	})
}

func (a *API) handleGet(w http.ResponseWriter, r *http.Request) {
	companyID, err := companyOf(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	job, err := a.store.FindByID(r.Context(), companyID, r.PathValue("id"))
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	httpx.WriteJSON(w, http.StatusOK, toJobView(job))
}

func (a *API) handleGetForm(w http.ResponseWriter, r *http.Request) {
	companyID, err := companyOf(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	job, err := a.store.FindByID(r.Context(), companyID, r.PathValue("id"))
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"jobId":            job.ID,
		"form":             job.Form,
		"allowedFileTypes": domain.AllowedFileTypes(),
	})
}

/* -------------------------------------------------------------------------- */
/* Creating and editing                                                       */
/* -------------------------------------------------------------------------- */

type salaryRequest struct {
	Min      *int64  `json:"min"`
	Max      *int64  `json:"max"`
	Currency *string `json:"currency"`
	IsPublic *bool   `json:"isPublic"`
}

type createJobRequest struct {
	Title           string         `json:"title"`
	Slug            string         `json:"slug"`
	Department      string         `json:"department"`
	Locations       []string       `json:"locations"`
	WorkMode        string         `json:"workMode"`
	EmploymentType  string         `json:"employmentType"`
	Seniority       string         `json:"seniority"`
	Description     string         `json:"description"`
	Requirements    string         `json:"requirements"`
	Salary          *salaryRequest `json:"salary"`
	Headcount       *int           `json:"headcount"`
	HiringManagerID string         `json:"hiringManagerId"`
	RecruiterID     string         `json:"recruiterId"`
	InternalNotes   string         `json:"internalNotes"`
	// Form is optional: a requisition created without one gets the default, so
	// a job is never published with nowhere to apply.
	Form *formRequest `json:"form"`
}

func (a *API) handleCreate(w http.ResponseWriter, r *http.Request) {
	principal := tenancy.MustFromContext(r.Context())
	companyID, err := companyOf(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	var req createJobRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	draft := domain.JobDraft{
		Title:           req.Title,
		Slug:            req.Slug,
		Department:      req.Department,
		Locations:       req.Locations,
		WorkMode:        domain.WorkMode(req.WorkMode),
		EmploymentType:  domain.EmploymentType(req.EmploymentType),
		Seniority:       domain.Seniority(req.Seniority),
		Description:     req.Description,
		Requirements:    req.Requirements,
		HiringManagerID: req.HiringManagerID,
		RecruiterID:     req.RecruiterID,
		InternalNotes:   req.InternalNotes,
	}
	if req.Headcount != nil {
		draft.Headcount = *req.Headcount
	}
	applySalary(&draft.Salary, req.Salary)
	draft.Normalize()

	errs := draft.Validate()

	form := domain.DefaultForm()
	if req.Form != nil {
		form = req.Form.toDomain()
		form.Normalize()
		mergeErrors(errs, "form", form.Validate())
	}

	if errs.Any() {
		httpx.WriteProblem(w, r, httpx.ValidationFailed(errs))
		return
	}

	job, err := a.store.Create(r.Context(), companyID, store.CreateInput{
		Draft:        draft,
		Form:         form,
		SlugExplicit: strings.TrimSpace(req.Slug) != "",
		CreatedBy:    principal.Subject,
	})
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	httpx.WriteJSON(w, http.StatusCreated, toJobView(job))
}

// updateJobRequest is all pointers so an absent field means "leave it alone".
//
// Without that distinction a PATCH that sends only a title would silently clear
// the description, which is the kind of data loss a recruiter notices a week
// later and cannot undo.
type updateJobRequest struct {
	Title           *string        `json:"title"`
	Slug            *string        `json:"slug"`
	Department      *string        `json:"department"`
	Locations       *[]string      `json:"locations"`
	WorkMode        *string        `json:"workMode"`
	EmploymentType  *string        `json:"employmentType"`
	Seniority       *string        `json:"seniority"`
	Description     *string        `json:"description"`
	Requirements    *string        `json:"requirements"`
	Salary          *salaryRequest `json:"salary"`
	Headcount       *int           `json:"headcount"`
	HiringManagerID *string        `json:"hiringManagerId"`
	RecruiterID     *string        `json:"recruiterId"`
	InternalNotes   *string        `json:"internalNotes"`
	Status          *string        `json:"status"`
}

func (a *API) handleUpdate(w http.ResponseWriter, r *http.Request) {
	companyID, err := companyOf(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	var req updateJobRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	// Status is a transition, not a content field: reopening or pausing goes
	// through the endpoints that also settle visibility and emit events.
	if req.Status != nil {
		httpx.WriteProblem(w, r, httpx.ValidationFailed(domain.FieldErrors{
			"status": {"Use the publish and close endpoints to change a requisition's status."},
		}))
		return
	}

	job, err := a.store.UpdateContent(r.Context(), companyID, r.PathValue("id"),
		func(current domain.Job) (domain.JobDraft, error) {
			if current.Status.Terminal() {
				return domain.JobDraft{}, domain.ErrJobTerminal
			}

			draft := draftFrom(current)
			req.applyTo(&draft)
			draft.Normalize()

			if errs := draft.Validate(); errs.Any() {
				return domain.JobDraft{}, httpx.ValidationFailed(errs)
			}
			return draft, nil
		})
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	httpx.WriteJSON(w, http.StatusOK, toJobView(job))
}

// applyTo copies the fields the caller actually sent onto the draft.
func (u updateJobRequest) applyTo(draft *domain.JobDraft) {
	assign(&draft.Title, u.Title)
	assign(&draft.Slug, u.Slug)
	assign(&draft.Department, u.Department)
	assign(&draft.Description, u.Description)
	assign(&draft.Requirements, u.Requirements)
	assign(&draft.HiringManagerID, u.HiringManagerID)
	assign(&draft.RecruiterID, u.RecruiterID)
	assign(&draft.InternalNotes, u.InternalNotes)

	if u.Locations != nil {
		draft.Locations = *u.Locations
	}
	if u.WorkMode != nil {
		draft.WorkMode = domain.WorkMode(*u.WorkMode)
	}
	if u.EmploymentType != nil {
		draft.EmploymentType = domain.EmploymentType(*u.EmploymentType)
	}
	if u.Seniority != nil {
		draft.Seniority = domain.Seniority(*u.Seniority)
	}
	if u.Headcount != nil {
		draft.Headcount = *u.Headcount
	}
	applySalary(&draft.Salary, u.Salary)
}

func (a *API) handleDuplicate(w http.ResponseWriter, r *http.Request) {
	principal := tenancy.MustFromContext(r.Context())
	companyID, err := companyOf(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	job, err := a.store.Duplicate(r.Context(), companyID, r.PathValue("id"), principal.Subject)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	httpx.WriteJSON(w, http.StatusCreated, toJobView(job))
}

/* -------------------------------------------------------------------------- */
/* The application form                                                       */
/* -------------------------------------------------------------------------- */

type formFieldRequest struct {
	Key        string                  `json:"key"`
	Label      string                  `json:"label"`
	Type       string                  `json:"type"`
	Required   bool                    `json:"required"`
	HelpText   string                  `json:"helpText"`
	Options    []domain.FieldOption    `json:"options"`
	Validation *domain.FieldValidation `json:"validation"`
}

type formRequest struct {
	Fields []formFieldRequest `json:"fields"`
}

func (f formRequest) toDomain() domain.ApplicationForm {
	fields := make([]domain.FormField, 0, len(f.Fields))
	for _, field := range f.Fields {
		fields = append(fields, domain.FormField{
			Key:        field.Key,
			Label:      field.Label,
			Type:       domain.FieldType(field.Type),
			Required:   field.Required,
			HelpText:   field.HelpText,
			Options:    field.Options,
			Validation: field.Validation,
		})
	}
	return domain.ApplicationForm{Fields: fields}
}

func (a *API) handleReplaceForm(w http.ResponseWriter, r *http.Request) {
	companyID, err := companyOf(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	var req formRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	form := req.toDomain()
	form.Normalize()

	// The form is stored as JSONB, which would accept anything. Rejecting it
	// here is the difference between a recruiter seeing a 422 now and a
	// candidate failing to submit an application later.
	if errs := form.Validate(); errs.Any() {
		httpx.WriteProblem(w, r, httpx.ValidationFailed(errs))
		return
	}

	job, err := a.store.ReplaceForm(r.Context(), companyID, r.PathValue("id"),
		func(current domain.Job) (domain.ApplicationForm, error) {
			if current.Status.Terminal() {
				return domain.ApplicationForm{}, domain.ErrJobTerminal
			}
			return form, nil
		})
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"jobId": job.ID, "form": job.Form})
}

/* -------------------------------------------------------------------------- */
/* Publishing and closing                                                     */
/* -------------------------------------------------------------------------- */

type publishRequest struct {
	Portal  bool `json:"portal"`
	Network bool `json:"network"`
}

func (a *API) handlePublish(w http.ResponseWriter, r *http.Request) {
	principal := tenancy.MustFromContext(r.Context())
	companyID, err := companyOf(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	var req publishRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	requested := domain.Visibility{Portal: req.Portal, Network: req.Network}

	job, err := a.store.SetVisibility(r.Context(), companyID, r.PathValue("id"),
		func(current domain.Job) (domain.Visibility, domain.Status, error) {
			// Both checks run against the locked row, so the permission was
			// tested against the visibility that is actually about to change
			// rather than one a concurrent request has since moved.
			for _, required := range domain.PublishPermissions(current, requested) {
				if !principal.Can(required) {
					return domain.Visibility{}, "", httpx.PermissionDenied([]string{required})
				}
			}
			if err := domain.CheckPublish(current, requested); err != nil {
				return domain.Visibility{}, "", err
			}
			return requested, domain.StatusAfterPublish(current, requested), nil
		})
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	a.publisher.PublishedOrUnpublished(r.Context(), job, principal.Subject, httpx.RequestIDFromContext(r.Context()))

	httpx.WriteJSON(w, http.StatusOK, toJobView(job))
}

func (a *API) handleClose(w http.ResponseWriter, r *http.Request) {
	principal := tenancy.MustFromContext(r.Context())
	companyID, err := companyOf(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	job, err := a.store.Close(r.Context(), companyID, r.PathValue("id"), domain.CheckClose)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	a.publisher.Closed(r.Context(), job, principal.Subject, httpx.RequestIDFromContext(r.Context()))

	httpx.WriteJSON(w, http.StatusOK, toJobView(job))
}

func (a *API) handleArchive(w http.ResponseWriter, r *http.Request) {
	principal := tenancy.MustFromContext(r.Context())
	companyID, err := companyOf(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	// Archiving takes the job off every board, so anything caching the public
	// listing has to be told even though the caller asked for a delete.
	job, err := a.store.Archive(r.Context(), companyID, r.PathValue("id"))
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	a.publisher.Unpublished(r.Context(), job, principal.Subject, httpx.RequestIDFromContext(r.Context()))

	httpx.WriteJSON(w, http.StatusOK, toJobView(job))
}

/* -------------------------------------------------------------------------- */
/* Helpers                                                                    */
/* -------------------------------------------------------------------------- */

// companyOf returns the tenant the request acts in.
//
// It comes from the verified principal and from nowhere else: a body, a path or
// a query parameter naming a company is a cross-tenant read waiting to happen.
func companyOf(r *http.Request) (string, error) {
	principal := tenancy.MustFromContext(r.Context())

	companyID, err := principal.RequireCompany()
	if err != nil {
		return "", httpx.Forbidden("This endpoint requires a company context.")
	}
	return companyID, nil
}

func parseListFilter(r *http.Request) (store.ListFilter, error) {
	query := r.URL.Query()
	errs := domain.FieldErrors{}

	filter := store.ListFilter{
		Status:     domain.Status(strings.TrimSpace(query.Get("status"))),
		Department: strings.TrimSpace(query.Get("department")),
		Visibility: store.VisibilityFilter(strings.TrimSpace(query.Get("visibility"))),
		Query:      strings.TrimSpace(query.Get("q")),
		Cursor:     strings.TrimSpace(query.Get("cursor")),
	}

	if filter.Status != "" && !filter.Status.Valid() {
		errs.Add("status", "Status must be one of: draft, open, on_hold, closed, archived.")
	}
	if !filter.Visibility.Valid() {
		errs.Add("visibility", "Visibility must be one of: portal, network, both, none.")
	}
	// Asking for archived rows explicitly is how they are reached; the default
	// list hides them because they are deleted as far as a recruiter is concerned.
	filter.IncludeArchived = filter.Status == domain.StatusArchived

	limit, err := parseLimit(query.Get("limit"))
	if err != nil {
		errs.Add("limit", err.Error())
	}
	filter.Limit = limit

	if errs.Any() {
		return store.ListFilter{}, httpx.ValidationFailed(errs)
	}
	return filter, nil
}

// parseLimit reads the page size, which the store clamps to the platform's
// maximum of 100 regardless.
func parseLimit(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 1 {
		return 0, errors.New("limit must be a positive whole number")
	}
	return limit, nil
}

func draftFrom(job domain.Job) domain.JobDraft {
	return domain.JobDraft{
		Title:           job.Title,
		Slug:            job.Slug,
		Department:      job.Department,
		Locations:       job.Locations,
		WorkMode:        job.WorkMode,
		EmploymentType:  job.EmploymentType,
		Seniority:       job.Seniority,
		Description:     job.Description,
		Requirements:    job.Requirements,
		Salary:          job.Salary,
		Headcount:       job.Headcount,
		HiringManagerID: job.HiringManagerID,
		RecruiterID:     job.RecruiterID,
		InternalNotes:   job.InternalNotes,
	}
}

func applySalary(target *domain.SalaryRange, req *salaryRequest) {
	if req == nil {
		return
	}
	// Each bound is independently clearable: sending `{"max": null}` on a band
	// that had one means the top has been removed, not that it was omitted.
	target.Min = req.Min
	target.Max = req.Max
	if req.Currency != nil {
		target.Currency = *req.Currency
	}
	if req.IsPublic != nil {
		target.Public = *req.IsPublic
	}
}

func assign(target *string, value *string) {
	if value != nil {
		*target = *value
	}
}

// mergeErrors folds a nested validation result into the outer one under a prefix.
func mergeErrors(into domain.FieldErrors, prefix string, from domain.FieldErrors) {
	for field, messages := range from {
		into[prefix+"."+field] = messages
	}
}

// internal guards service-to-service endpoints with a shared secret.
//
// This endpoint answers with any tenant's job, so it must never be reachable
// from the public internet; the gateway does not route to it.
func (a *API) internal(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.internalToken == "" {
			httpx.WriteProblem(w, r, httpx.Internal("Internal API is not configured."))
			return
		}

		presented := strings.TrimSpace(r.Header.Get("X-Internal-Token"))
		if subtleCompare(presented, a.internalToken) != 1 {
			httpx.WriteProblem(w, r, httpx.Unauthorized("Invalid internal credentials."))
			return
		}

		next.ServeHTTP(w, r)
	})
}

// subtleCompare is a constant-time string comparison for shared secrets.
func subtleCompare(a, b string) int {
	if len(a) != len(b) {
		// Still run a comparison so length alone does not shortcut the timing.
		subtle.ConstantTimeCompare([]byte(a), []byte(a))
		return 0
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b))
}

func decodeJSON(r *http.Request, dst any) error {
	// A bounded reader keeps a malicious client from exhausting memory. A job
	// description and a 60-field form fit comfortably inside a megabyte.
	limited := http.MaxBytesReader(nil, r.Body, 1<<20)
	decoder := json.NewDecoder(limited)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return httpx.BadRequest("A JSON request body is required.")
		}
		// encoding/json spells out internal type names in its errors, which
		// hands a caller a map of the server's internals. Say what is wrong
		// without saying what we are.
		var maxBytes *http.MaxBytesError
		if errors.As(err, &maxBytes) {
			return httpx.BadRequest("The request body is too large.")
		}
		var syntax *json.SyntaxError
		if errors.As(err, &syntax) {
			return httpx.BadRequest(fmt.Sprintf("The request body is not valid JSON (at byte %d).", syntax.Offset))
		}
		return httpx.BadRequest("The request body does not match the expected shape.")
	}
	return nil
}

// mapError turns a domain error into the right HTTP problem.
func mapError(err error) error {
	var validationErr *domain.ValidationError
	if errors.As(err, &validationErr) {
		field := validationErr.Field
		if field == "" {
			field = "request"
		}
		return httpx.ValidationFailed(domain.FieldErrors{field: {validationErr.Message}})
	}

	switch {
	case errors.Is(err, domain.ErrJobNotFound):
		// 404 rather than 403: another tenant must not be able to learn that a
		// job id exists by the shape of the refusal.
		return httpx.NotFound(domain.ErrJobNotFound.Error())
	case errors.Is(err, domain.ErrSlugTaken):
		return httpx.Conflict("slug_taken", domain.ErrSlugTaken.Error())
	case errors.Is(err, domain.ErrNetworkSlugTaken):
		return httpx.Conflict("network_slug_taken", domain.ErrNetworkSlugTaken.Error())
	case errors.Is(err, domain.ErrJobTerminal):
		return httpx.Conflict("job_terminal", domain.ErrJobTerminal.Error())
	case errors.Is(err, domain.ErrAlreadyClosed):
		return httpx.Conflict("already_closed", domain.ErrAlreadyClosed.Error())
	case errors.Is(err, tenancy.ErrNotCompanyScoped), errors.Is(err, tenancy.ErrCrossTenant):
		return httpx.Forbidden("This endpoint requires a company context.")
	default:
		return err
	}
}
