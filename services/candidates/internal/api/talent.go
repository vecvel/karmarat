package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/karmarat/platform/packages/goshared/httpx"
	"github.com/karmarat/platform/packages/goshared/idgen"
	"github.com/karmarat/platform/packages/goshared/tenancy"
	"github.com/karmarat/platform/services/candidates/internal/domain"
	"github.com/karmarat/platform/services/candidates/internal/store"
)

// approachWindow and approachesPerWindow bound how often one company may
// contact the same candidate.
//
// Discovery is only tolerable for candidates if being discoverable does not mean
// being pestered. The limit is per company per candidate rather than global, so
// one aggressive recruiter cannot consume another company's ability to reach
// someone.
const (
	approachWindow      = 30 * 24 * time.Hour
	approachesPerWindow = 1
)

/* -------------------------------------------------------------------------- */
/* Talent search                                                              */
/* -------------------------------------------------------------------------- */

// searchView is the public shape of a discoverable candidate.
//
// There is no email, phone, name or account id here, and that is the point: a
// company learns that someone suitable exists and may approach them, but the
// contact details stay with the candidate until they answer. The store's
// discoverable predicate does the filtering; this type makes a leak impossible
// even if that predicate were ever wrong.
type searchView struct {
	ID              string   `json:"id"`
	Headline        string   `json:"headline,omitempty"`
	Summary         string   `json:"summary,omitempty"`
	Location        string   `json:"location,omitempty"`
	YearsExperience int      `json:"yearsExperience"`
	CurrentTitle    string   `json:"currentTitle,omitempty"`
	CurrentEmployer string   `json:"currentEmployer,omitempty"`
	Skills          []string `json:"skills"`
	Languages       []string `json:"languages"`
	OpenToTypes     []string `json:"openToTypes"`
	OpenToRemote    bool     `json:"openToRemote"`

	WorkAuthorisation     string    `json:"workAuthorisation,omitempty"`
	DesiredSalaryMinor    *int64    `json:"desiredSalaryMinor,omitempty"`
	DesiredSalaryCurrency string    `json:"desiredSalaryCurrency,omitempty"`
	UpdatedAt             time.Time `json:"updatedAt"`
}

func toSearchView(r domain.SearchResult) searchView {
	types := make([]string, 0, len(r.OpenToTypes))
	for _, t := range r.OpenToTypes {
		types = append(types, string(t))
	}

	return searchView{
		ID:              r.ID,
		Headline:        r.Headline,
		Summary:         r.Summary,
		Location:        r.Location,
		YearsExperience: r.YearsExperience,
		CurrentTitle:    r.CurrentTitle,
		// Empty when the candidate chose to hide it; the store already omitted it.
		CurrentEmployer:       r.CurrentEmployer,
		Skills:                orEmpty(r.Skills),
		Languages:             orEmpty(r.Languages),
		OpenToTypes:           types,
		OpenToRemote:          r.OpenToRemote,
		WorkAuthorisation:     string(r.WorkAuthorisation),
		DesiredSalaryMinor:    r.DesiredSalaryMinor,
		DesiredSalaryCurrency: r.DesiredSalaryCurrency,
		UpdatedAt:             r.UpdatedAt,
	}
}

func (a *API) handleTalentSearch(w http.ResponseWriter, r *http.Request) {
	companyID, ok := companyOf(w, r)
	if !ok {
		return
	}

	page, err := pageOf(r)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	query := r.URL.Query()
	minYears, _ := strconv.Atoi(query.Get("minYears"))
	if minYears < 0 {
		minYears = 0
	}

	filter := store.TalentFilter{
		Query:          strings.TrimSpace(query.Get("q")),
		Skills:         domain.NormalizeList(splitCSV(query.Get("skills"))),
		Location:       strings.TrimSpace(query.Get("location")),
		MinYears:       minYears,
		RemoteOnly:     strings.EqualFold(query.Get("remote"), "true"),
		EmploymentType: domain.EmploymentType(strings.TrimSpace(query.Get("employmentType"))),
	}

	// The company id is passed to the store so its predicate can exclude anyone
	// who blocked this company. It is taken from the principal, never the query.
	results, err := a.store.SearchTalent(r.Context(), companyID, filter, page)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	views := make([]searchView, 0, len(results))
	for _, result := range results {
		views = append(views, toSearchView(result))
	}

	cursor := ""
	if len(results) > 0 {
		last := results[len(results)-1]
		cursor = nextCursor(len(results), page.Limit, last.UpdatedAt, last.ID)
	}

	httpx.WriteJSON(w, http.StatusOK, listResponse{Data: views, NextCursor: cursor})
}

func splitCSV(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	return strings.Split(raw, ",")
}

/* -------------------------------------------------------------------------- */
/* Approaches                                                                 */
/* -------------------------------------------------------------------------- */

type approachRequest struct {
	Subject string `json:"subject"`
	Message string `json:"message"`
	JobID   string `json:"jobId"`
}

type approachView struct {
	ID          string    `json:"id"`
	CandidateID string    `json:"candidateId"`
	JobID       string    `json:"jobId,omitempty"`
	Subject     string    `json:"subject"`
	Message     string    `json:"message"`
	CreatedAt   time.Time `json:"createdAt"`
}

func toApproachView(a domain.Approach) approachView {
	return approachView{
		ID:          a.ID,
		CandidateID: a.CandidateID,
		JobID:       a.JobID,
		Subject:     a.Subject,
		Message:     a.Message,
		CreatedAt:   a.CreatedAt,
	}
}

// handleApproach records a company reaching out to a discoverable candidate.
//
// The candidate is re-resolved through the discoverable predicate rather than
// looked up by id: a candidate who turned discovery off, or blocked this
// company, between the search and the approach must be unreachable, and the
// search result the client is holding is already stale by then.
func (a *API) handleApproach(w http.ResponseWriter, r *http.Request) {
	companyID, ok := companyOf(w, r)
	if !ok {
		return
	}
	principal := tenancy.MustFromContext(r.Context())

	var req approachRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	v := domain.NewValidation()
	subject := strings.TrimSpace(req.Subject)
	message := strings.TrimSpace(req.Message)
	switch {
	case subject == "":
		v.Add("subject", "A subject is required.")
	case len(subject) > 200:
		v.Add("subject", "Subject must be 200 characters or fewer.")
	}
	switch {
	case message == "":
		v.Add("message", "A message is required.")
	case len(message) > 4000:
		v.Add("message", "Message must be 4000 characters or fewer.")
	}
	if err := v.Err(); err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	candidateID := r.PathValue("candidateId")

	profile, err := a.store.FindDiscoverableProfile(r.Context(), companyID, candidateID)
	if err != nil {
		// Not found rather than forbidden: telling a company that a candidate
		// exists but has blocked them is itself information they should not have.
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	recent, err := a.store.CountRecentApproaches(r.Context(), companyID, profile.ID, time.Now().Add(-approachWindow))
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}
	if recent >= approachesPerWindow {
		httpx.WriteProblem(w, r, mapError(domain.ErrApproachTooSoon))
		return
	}

	approach, err := a.store.CreateApproach(r.Context(), domain.Approach{
		ID:             idgen.New("apr"),
		CandidateID:    profile.ID,
		CompanyID:      companyID,
		ActorAccountID: principal.Subject,
		JobID:          strings.TrimSpace(req.JobID),
		Subject:        subject,
		Message:        message,
	})
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	// Messaging opens the conversation and notifications tells the candidate;
	// neither belongs in this request's critical path. The account id is resolved
	// here rather than returned to the company — the event needs a recipient, the
	// recruiter does not need an identity.
	accountID, err := a.store.AccountIDForProfile(r.Context(), profile.ID)
	if err != nil {
		a.logger.Error("could not resolve the account behind a profile",
			"error", err, "profile_id", profile.ID)
	} else {
		a.publisher.Approached(r.Context(), approach, accountID)
	}

	httpx.WriteJSON(w, http.StatusCreated, toApproachView(approach))
}

func (a *API) handleListApproaches(w http.ResponseWriter, r *http.Request) {
	companyID, ok := companyOf(w, r)
	if !ok {
		return
	}

	page, err := pageOf(r)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	approaches, err := a.store.ListApproaches(r.Context(), companyID, page)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	views := make([]approachView, 0, len(approaches))
	for _, approach := range approaches {
		views = append(views, toApproachView(approach))
	}

	cursor := ""
	if len(approaches) > 0 {
		last := approaches[len(approaches)-1]
		cursor = nextCursor(len(approaches), page.Limit, last.CreatedAt, last.ID)
	}

	httpx.WriteJSON(w, http.StatusOK, listResponse{Data: views, NextCursor: cursor})
}
