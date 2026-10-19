package api

import (
	"net/http"
	"strings"

	"github.com/karmarat/platform/packages/goshared/httpx"
	"github.com/karmarat/platform/services/jobs/internal/domain"
	"github.com/karmarat/platform/services/jobs/internal/store"
)

/* -------------------------------------------------------------------------- */
/* Public job boards                                                          */
/* -------------------------------------------------------------------------- */

// publicTenant resolves which board an unauthenticated request is reading.
//
// There is no principal to ask, so the tenant comes from the headers the
// gateway sets after resolving the hostname — the same source, and the same
// trust, as the principal headers: the gateway strips whatever the client sent
// before setting its own. `X-Company-Slug` present means the request arrived on
// {slug}.{hostname}, a company's own careers portal; absent means the shared
// board on jobs.{hostname}, which spans every tenant that opted in.
//
// The slug is the signal and the company id is the value, because the id is
// what the gateway already resolved against its tenant cache — re-resolving a
// slug here would need a projection this service has no other reason to keep.
func publicTenant(r *http.Request) (companyID string, onCompanyPortal bool) {
	slug := strings.TrimSpace(r.Header.Get(httpx.HeaderCompanySlug))
	if slug == "" {
		return "", false
	}
	return strings.TrimSpace(r.Header.Get(httpx.HeaderCompanyID)), true
}

func (a *API) handlePublicList(w http.ResponseWriter, r *http.Request) {
	companyID, onCompanyPortal := publicTenant(r)
	if onCompanyPortal && companyID == "" {
		// A portal we cannot name is a portal with nothing on it. Falling back
		// to the network board here would put other companies' jobs on this
		// company's careers page.
		httpx.WriteProblem(w, r, httpx.NotFound("This careers portal is not available."))
		return
	}

	query := r.URL.Query()
	errs := domain.FieldErrors{}

	filter := store.PublicFilter{
		CompanyID:      companyID,
		Query:          strings.TrimSpace(query.Get("q")),
		Location:       strings.TrimSpace(query.Get("location")),
		Department:     strings.TrimSpace(query.Get("department")),
		EmploymentType: domain.EmploymentType(strings.TrimSpace(query.Get("employmentType"))),
		WorkMode:       domain.WorkMode(strings.TrimSpace(query.Get("workMode"))),
		Cursor:         strings.TrimSpace(query.Get("cursor")),
	}

	if filter.EmploymentType != "" && !filter.EmploymentType.Valid() {
		errs.Add("employmentType", "Unknown employment type.")
	}
	if filter.WorkMode != "" && !filter.WorkMode.Valid() {
		errs.Add("workMode", "Work mode must be one of: onsite, hybrid, remote.")
	}

	limit, err := parseLimit(query.Get("limit"))
	if err != nil {
		errs.Add("limit", err.Error())
	}
	filter.Limit = limit

	if errs.Any() {
		httpx.WriteProblem(w, r, httpx.ValidationFailed(errs))
		return
	}

	page, err := a.store.ListPublic(r.Context(), filter)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	summaries := make([]domain.PublicSummary, 0, len(page.Jobs))
	for _, job := range page.Jobs {
		summaries = append(summaries, job.PublicSummaryView())
	}

	// A board changes slowly and is read far more than it is written; letting a
	// CDN hold it briefly is the difference between a launch and an outage.
	w.Header().Set("Cache-Control", "public, max-age=60")

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"jobs":       summaries,
		"nextCursor": page.NextCursor,
	})
}

func (a *API) handlePublicJob(w http.ResponseWriter, r *http.Request) {
	job, err := a.resolvePublicJob(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	w.Header().Set("Cache-Control", "public, max-age=60")

	// PublicView is the only projection served here: it drops the hiring
	// manager, the recruiter, the internal notes, the headcount, and any salary
	// band the company did not mark public.
	httpx.WriteJSON(w, http.StatusOK, job.PublicView())
}

func (a *API) handlePublicForm(w http.ResponseWriter, r *http.Request) {
	job, err := a.resolvePublicJob(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"jobId":            job.ID,
		"slug":             job.Slug,
		"title":            job.Title,
		"form":             job.Form,
		"allowedFileTypes": domain.AllowedFileTypes(),
	})
}

// resolvePublicJob loads the job behind a public slug on the right surface.
func (a *API) resolvePublicJob(r *http.Request) (domain.Job, error) {
	companyID, onCompanyPortal := publicTenant(r)
	if onCompanyPortal && companyID == "" {
		return domain.Job{}, httpx.NotFound("This careers portal is not available.")
	}

	slug := strings.TrimSpace(r.PathValue("slug"))
	if slug == "" {
		return domain.Job{}, httpx.NotFound(domain.ErrJobNotFound.Error())
	}

	// The store decides which surface's flag applies: on a company portal the
	// job must be visible_on_portal, on the shared board visible_on_network. A
	// job listed only on the other one is "not found" here, not "forbidden" —
	// its existence is not the public's business.
	job, err := a.store.FindPublicBySlug(r.Context(), companyID, slug)
	if err != nil {
		return domain.Job{}, mapError(err)
	}
	return job, nil
}

/* -------------------------------------------------------------------------- */
/* Internal service endpoint                                                  */
/* -------------------------------------------------------------------------- */

// handleInternalJob answers the applications service.
//
// An application is validated against the form that is live right now, and
// applications keeps no copy of it — this endpoint is what lets it check a
// submission without a cross-service join into a database it does not own.
func (a *API) handleInternalJob(w http.ResponseWriter, r *http.Request) {
	job, err := a.store.FindForService(r.Context(), r.PathValue("id"))
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"id":               job.ID,
		"companyId":        job.CompanyID,
		"title":            job.Title,
		"slug":             job.Slug,
		"status":           job.Status,
		"form":             job.Form,
		"visibleOnPortal":  job.VisibleOnPortal,
		"visibleOnNetwork": job.VisibleOnNetwork,
	})
}
