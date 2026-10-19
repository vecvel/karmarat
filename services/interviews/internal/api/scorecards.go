package api

import (
	"net/http"
	"strings"

	"github.com/karmarat/platform/packages/goshared/httpx"
	"github.com/karmarat/platform/services/interviews/internal/domain"
	"github.com/karmarat/platform/services/interviews/internal/store"
)

// scorecardRequest mirrors the company portal's ScorecardInput.
//
// There is no authorId field. The author is the authenticated principal, always:
// accepting one from the body would let anybody holding submit_scorecard file
// feedback under a colleague's name, and then read "their own" card back.
type scorecardRequest struct {
	OverallRating      int    `json:"overallRating"`
	Recommendation     string `json:"recommendation"`
	TechnicalScore     *int   `json:"technicalScore"`
	CommunicationScore *int   `json:"communicationScore"`
	CultureScore       *int   `json:"cultureScore"`
	Strengths          string `json:"strengths"`
	Concerns           string `json:"concerns"`
	FeedbackNotes      string `json:"feedbackNotes"`
}

func (a *API) handleSubmitScorecard(w http.ResponseWriter, r *http.Request) {
	companyID, principal, err := tenantOf(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	var req scorecardRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	values := domain.ScorecardInput{
		OverallRating:      req.OverallRating,
		Recommendation:     strings.TrimSpace(req.Recommendation),
		TechnicalScore:     req.TechnicalScore,
		CommunicationScore: req.CommunicationScore,
		CultureScore:       req.CultureScore,
		Strengths:          strings.TrimSpace(req.Strengths),
		Concerns:           strings.TrimSpace(req.Concerns),
		FeedbackNotes:      strings.TrimSpace(req.FeedbackNotes),
	}

	if problems := domain.ValidateScorecard(values); len(problems) > 0 {
		httpx.WriteProblem(w, r, httpx.ValidationFailed(problems))
		return
	}

	// Resolving the round first is what scopes the panel check to this tenant:
	// an id belonging to another company reads as missing here, so the panel
	// membership question is never asked about somebody else's interview.
	interview, err := a.store.FindInterview(r.Context(), companyID, r.PathValue("id"))
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	if !domain.MaySubmitScorecard(interview, principal.Subject, principal.Can(permUpdate)) {
		httpx.WriteProblem(w, r, mapError(domain.ErrNotOnPanel))
		return
	}

	card, created, err := a.store.SaveScorecard(r.Context(), store.SaveScorecardInput{
		CompanyID:   companyID,
		InterviewID: interview.ID,
		AuthorID:    principal.Subject,
		Privileged:  principal.Can(permUpdate),
		Values:      values,
	})
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	httpx.WriteJSON(w, status, card)
}

// handleListScorecards returns the feedback on a round that the caller may read.
//
// Two permissions reach this handler and they mean different things.
// `interviews.view_scorecards` is a reading of the whole panel;
// `interviews.submit_scorecard` alone is a reading of your own card and nothing
// else. The narrow case is not an error — it returns a shorter list — because
// the point of separating the keys is that an interviewer writes their verdict
// without having seen anyone else's. A panel that reads each other first
// produces one opinion with four signatures on it.
func (a *API) handleListScorecards(w http.ResponseWriter, r *http.Request) {
	companyID, principal, err := tenantOf(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	interview, err := a.store.FindInterview(r.Context(), companyID, r.PathValue("id"))
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	// A narrow reader has to be on the panel of the round they are asking about.
	//
	// This route admits `interviews.submit_scorecard` so an interviewer can read
	// their own card back, which means it is the one interview-scoped route that
	// does not require `interviews.read`. Without this check, somebody holding
	// only the narrow key could walk ids and learn which rounds exist inside the
	// company from the difference between a 404 and an empty 200 — the schedule
	// they were deliberately not given `interviews.read` to see.
	//
	// Not found rather than forbidden, so the answer is the same either way.
	if !principal.Can(permRead) && !domain.MaySubmitScorecard(interview, principal.Subject, principal.Can(permUpdate)) {
		httpx.WriteProblem(w, r, mapError(domain.ErrInterviewNotFound))
		return
	}

	cards, err := a.store.ListScorecards(r.Context(), companyID, interview.ID)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	viewer := viewerOf(principal)
	visible := domain.VisibleScorecards(viewer, cards)

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"scorecards": visible,
		// The count is the caller's own view, not the true total. Reporting the
		// real number would tell an interviewer how many colleagues have already
		// filed — which is a weaker version of the same disclosure the two
		// permissions exist to prevent.
		"total": len(visible),
		// Stated plainly so the portal can label a partial list rather than
		// implying the panel said nothing.
		"partial": !viewer.ViewAll,
	})
}
