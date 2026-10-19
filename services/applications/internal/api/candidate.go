package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/karmarat/platform/packages/goshared/httpx"
	"github.com/karmarat/platform/packages/goshared/tenancy"
	"github.com/karmarat/platform/services/applications/internal/domain"
	"github.com/karmarat/platform/services/applications/internal/store"
)

// candidateView is what an applicant is allowed to see about their own
// application.
//
// It is a separate type rather than a filtered domain.Application on purpose:
// the company view carries internal rejection notes, the reviewing recruiter and
// a rating, and a field added to the domain struct later would otherwise leak
// into this response the moment someone forgot to exclude it. Building the
// candidate's view explicitly means new internal fields are invisible by
// default.
type candidateView struct {
	ID          string         `json:"id"`
	JobID       string         `json:"jobId"`
	JobTitle    string         `json:"jobTitle"`
	CompanyName string         `json:"companyName"`
	Status      domain.Status  `json:"status"`
	StageName   string         `json:"stageName,omitempty"`
	Answers     map[string]any `json:"answers,omitempty"`
	ResumeKey   string         `json:"resumeKey,omitempty"`

	// The reason's label is shown; the recruiter's private note is not.
	RejectionReason string     `json:"rejectionReason,omitempty"`
	RejectedAt      *time.Time `json:"rejectedAt,omitempty"`
	WithdrawnAt     *time.Time `json:"withdrawnAt,omitempty"`

	SubmittedAt time.Time `json:"submittedAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

func toCandidateView(application domain.Application, stageName, reasonLabel string, includeAnswers bool) candidateView {
	view := candidateView{
		ID:              application.ID,
		JobID:           application.JobID,
		JobTitle:        application.JobTitle,
		CompanyName:     application.CompanyName,
		Status:          application.Status,
		StageName:       stageName,
		ResumeKey:       application.ResumeKey,
		RejectionReason: reasonLabel,
		RejectedAt:      application.RejectedAt,
		WithdrawnAt:     application.WithdrawnAt,
		SubmittedAt:     application.SubmittedAt,
		UpdatedAt:       application.UpdatedAt,
	}
	if includeAnswers {
		view.Answers = application.Answers
	}
	return view
}

// candidateOf returns the account whose applications are being read.
//
// Like tenantOf for companies, this is the only source of a candidate identity
// in the service. No handler accepts a candidate id from a path or a body, so
// there is no request in which someone else's applications can be named.
func candidateOf(r *http.Request) (string, error) {
	principal := tenancy.MustFromContext(r.Context())
	if principal.Type != tenancy.PrincipalCandidate || principal.Subject == "" {
		return "", httpx.Forbidden("This endpoint is only available to candidate accounts.")
	}
	return principal.Subject, nil
}

func (a *API) handleMyApplications(w http.ResponseWriter, r *http.Request) {
	candidateID, err := candidateOf(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	limit, cursor, err := pagination(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	page, err := a.store.ListApplicationsForCandidate(r.Context(), candidateID, limit, cursor)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	// A candidate's applications span companies, so the stage and reason names
	// have to be resolved per company rather than from one lookup.
	views := make([]candidateView, 0, len(page.Applications))
	for _, application := range page.Applications {
		stageName, reasonLabel := a.labelsFor(r, application)
		views = append(views, toCandidateView(application, stageName, reasonLabel, false))
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"applications": views,
		"nextCursor":   page.NextCursor,
	})
}

func (a *API) handleMyApplication(w http.ResponseWriter, r *http.Request) {
	candidateID, err := candidateOf(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	application, err := a.store.FindApplicationForCandidate(r.Context(), candidateID, r.PathValue("id"))
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	stageName, reasonLabel := a.labelsFor(r, application)
	httpx.WriteJSON(w, http.StatusOK, toCandidateView(application, stageName, reasonLabel, true))
}

func (a *API) handleWithdraw(w http.ResponseWriter, r *http.Request) {
	candidateID, err := candidateOf(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	application, err := a.store.WithdrawApplication(r.Context(), candidateID, r.PathValue("id"))
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	stageName, reasonLabel := a.labelsFor(r, application)
	httpx.WriteJSON(w, http.StatusOK, toCandidateView(application, stageName, reasonLabel, false))
}

// labelsFor resolves the stage and rejection-reason names for one application.
//
// A failure here is not worth failing the request over: the candidate still gets
// their application, just without a friendly stage name, which beats a 500.
func (a *API) labelsFor(r *http.Request, application domain.Application) (stageName, reasonLabel string) {
	if application.StageID != "" {
		if stages, err := a.store.ListStages(r.Context(), application.CompanyID); err == nil {
			for _, stage := range stages {
				if stage.ID == application.StageID {
					stageName = stage.Name
					break
				}
			}
		}
	}

	if application.RejectionReasonID != "" {
		if reasons, err := a.store.ListRejectionReasons(r.Context(), application.CompanyID, false); err == nil {
			for _, reason := range reasons {
				if reason.ID == application.RejectionReasonID {
					reasonLabel = reason.Label
					break
				}
			}
		}
	}

	return stageName, reasonLabel
}

/* -------------------------------------------------------------------------- */
/* Submission                                                                 */
/* -------------------------------------------------------------------------- */

type applyRequest struct {
	JobID     string         `json:"jobId"`
	Answers   map[string]any `json:"answers"`
	ResumeKey string         `json:"resumeKey"`
	Source    string         `json:"source"`
}

// handleApply records a candidate's application to a job.
//
// The form is fetched from the jobs service and the answers are validated
// against it here, server-side. A client that validated the same form before
// posting proves nothing: the form it validated against is the one it chose to
// fetch, and the only copy that counts is the one the owning service returns
// now.
func (a *API) handleApply(w http.ResponseWriter, r *http.Request) {
	candidateID, err := candidateOf(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}
	principal := tenancy.MustFromContext(r.Context())

	var req applyRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	jobID := strings.TrimSpace(req.JobID)
	if jobID == "" {
		httpx.WriteProblem(w, r, httpx.ValidationFailed(map[string][]string{
			"jobId": {"A job is required."}}))
		return
	}

	job, err := a.jobs.Fetch(r.Context(), jobID)
	if err != nil {
		if errors.Is(err, domain.ErrJobNotFound) {
			// The same 404 whether the job never existed or is not visible: a
			// distinct "exists but hidden" answer would let anyone enumerate a
			// company's unpublished requisitions.
			httpx.WriteProblem(w, r, httpx.NotFound("That job could not be found."))
			return
		}
		a.logger.Error("could not reach the jobs service", "error", err, "job_id", jobID)
		httpx.WriteProblem(w, r, httpx.Internal("We could not verify that job just now. Please try again."))
		return
	}

	if !job.Open() {
		httpx.WriteProblem(w, r, mapError(domain.ErrJobNotAccepting))
		return
	}

	if problems := domain.ValidateAnswers(job.Form, req.Answers); len(problems) > 0 {
		httpx.WriteProblem(w, r, httpx.ValidationFailed(problems))
		return
	}

	source := domain.Source(strings.TrimSpace(req.Source))
	if !source.Valid() {
		source = domain.SourcePortal
	}

	application, err := a.store.CreateApplication(r.Context(), store.CreateApplicationInput{
		CompanyID:      job.CompanyID,
		JobID:          job.ID,
		CandidateID:    candidateID,
		CandidateName:  a.candidates.DisplayName(r.Context(), candidateID, principal.Email),
		CandidateEmail: principal.Email,
		JobTitle:       job.Title,
		CompanyName:    job.CompanyName,
		Answers:        req.Answers,
		ResumeKey:      strings.TrimSpace(req.ResumeKey),
		Source:         source,
	})
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	httpx.WriteJSON(w, http.StatusCreated, toCandidateView(application, "", "", true))
}
