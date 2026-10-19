package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/karmarat/platform/packages/goshared/httpx"
	"github.com/karmarat/platform/services/interviews/internal/domain"
	"github.com/karmarat/platform/services/interviews/internal/store"
)

/* -------------------------------------------------------------------------- */
/* List                                                                       */
/* -------------------------------------------------------------------------- */

func (a *API) handleList(w http.ResponseWriter, r *http.Request) {
	companyID, _, err := tenantOf(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	filter, err := listFilter(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	page, err := a.store.ListInterviews(r.Context(), companyID, filter)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"interviews": page.Interviews,
		"nextCursor": page.NextCursor,
	})
}

func listFilter(r *http.Request) (store.ListFilter, error) {
	limit, cursor, err := pagination(r)
	if err != nil {
		return store.ListFilter{}, err
	}

	query := r.URL.Query()
	filter := store.ListFilter{
		Status:        strings.TrimSpace(query.Get("status")),
		ApplicationID: strings.TrimSpace(query.Get("applicationId")),
		CandidateID:   strings.TrimSpace(query.Get("candidateId")),
		Limit:         limit,
		Cursor:        cursor,
	}

	// The status column is an enum, so an unrecognized filter value would reach
	// Postgres as an invalid enum literal and surface as a 500. Rejecting it
	// here makes it a 422 that names the field.
	if filter.Status != "" && !domain.Status(filter.Status).Valid() {
		return store.ListFilter{}, httpx.ValidationFailed(map[string][]string{
			"status": {"Status must be one of: " + strings.Join(domain.Statuses(), ", ") + "."}})
	}

	if filter.From, err = parseDate(query.Get("from"), "from", false); err != nil {
		return store.ListFilter{}, err
	}
	if filter.To, err = parseDate(query.Get("to"), "to", true); err != nil {
		return store.ListFilter{}, err
	}
	if filter.From != nil && filter.To != nil && filter.To.Before(*filter.From) {
		// Silently returning nothing for a reversed range reads as "no
		// interviews booked", which is the wrong thing for a recruiter to
		// conclude from a mistyped date.
		return store.ListFilter{}, httpx.ValidationFailed(map[string][]string{
			"to": {"The end of the range must not be before its start."}})
	}

	return filter, nil
}

/* -------------------------------------------------------------------------- */
/* Read one                                                                   */
/* -------------------------------------------------------------------------- */

func (a *API) handleGet(w http.ResponseWriter, r *http.Request) {
	companyID, _, err := tenantOf(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	interview, err := a.store.FindInterview(r.Context(), companyID, r.PathValue("id"))
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	httpx.WriteJSON(w, http.StatusOK, interview)
}

/* -------------------------------------------------------------------------- */
/* Create                                                                     */
/* -------------------------------------------------------------------------- */

// scheduleRequest mirrors the company portal's ScheduleInterviewInput, so the
// screen that already renders this form needs a mapping rather than a rewrite.
//
// There is no companyId field and there never will be: the tenant comes from the
// verified principal, and accepting one here would be a cross-tenant write
// reachable from a form.
type scheduleRequest struct {
	ApplicationID   string   `json:"applicationId"`
	CandidateID     string   `json:"candidateId"`
	CandidateName   string   `json:"candidateName"`
	JobTitle        string   `json:"jobTitle"`
	RoundTitle      string   `json:"roundTitle"`
	RoundType       string   `json:"roundType"`
	ScheduledStart  string   `json:"scheduledStart"`
	DurationMinutes *int     `json:"durationMinutes"`
	Format          *string  `json:"format"`
	MeetingLink     string   `json:"meetingLink"`
	Notes           string   `json:"notes"`
	PanelMemberIDs  []string `json:"panelMemberIds"`
}

func (a *API) handleCreate(w http.ResponseWriter, r *http.Request) {
	companyID, principal, err := tenantOf(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	var req scheduleRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	problems := map[string][]string{}
	if strings.TrimSpace(req.ApplicationID) == "" {
		problems["applicationId"] = []string{"An application is required."}
	}
	if strings.TrimSpace(req.CandidateID) == "" {
		problems["candidateId"] = []string{"A candidate is required."}
	}

	start, badStart := parseTimestamp(req.ScheduledStart)
	if badStart != "" {
		problems["scheduledStart"] = []string{badStart}
	}

	// The shared rules live in the domain package so the same checks apply on a
	// patch, and so they can be tested without a database.
	duration := req.DurationMinutes
	if duration == nil {
		hour := 60
		duration = &hour
	}
	format := req.Format
	if format == nil || strings.TrimSpace(*format) == "" {
		video := string(domain.FormatVideo)
		format = &video
	}

	merge(problems, domain.ValidateRound(domain.RoundInput{
		RoundTitle:      &req.RoundTitle,
		RoundType:       &req.RoundType,
		ScheduledStart:  start,
		DurationMinutes: duration,
		Format:          format,
		MeetingLink:     &req.MeetingLink,
		Notes:           &req.Notes,
		PanelMemberIDs:  &req.PanelMemberIDs,
		// The required-field pass is skipped when the timestamp was malformed:
		// the parse message above already names the field, and "a start time is
		// required" alongside it reads as two separate mistakes.
	}, badStart == ""))

	if len(problems) > 0 {
		httpx.WriteProblem(w, r, httpx.ValidationFailed(problems))
		return
	}

	interview, isNew, err := a.store.CreateInterview(r.Context(), store.CreateInterviewInput{
		CompanyID:     companyID,
		ApplicationID: strings.TrimSpace(req.ApplicationID),
		CandidateID:   strings.TrimSpace(req.CandidateID),
		// Supplied by the caller and then kept current by the application
		// consumer. Seeding them from the request is what stops a freshly booked
		// round rendering as a blank row until the next application event.
		CandidateName:   strings.TrimSpace(req.CandidateName),
		JobTitle:        strings.TrimSpace(req.JobTitle),
		RoundTitle:      strings.TrimSpace(req.RoundTitle),
		RoundType:       strings.TrimSpace(req.RoundType),
		ScheduledStart:  *start,
		DurationMinutes: *duration,
		Format:          domain.Format(strings.TrimSpace(*format)),
		MeetingLink:     strings.TrimSpace(req.MeetingLink),
		Notes:           req.Notes,
		PanelMemberIDs:  req.PanelMemberIDs,
		ActorID:         principal.Subject,
		IdempotencyKey:  idempotencyKey(r),
	})
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	// 200 for a retry, 201 for the booking that actually happened. A client
	// replaying a timed-out request gets the round it asked for either way, and
	// the status says which of the two it is.
	status := http.StatusOK
	if isNew {
		status = http.StatusCreated
	}
	httpx.WriteJSON(w, status, interview)
}

/* -------------------------------------------------------------------------- */
/* Patch                                                                      */
/* -------------------------------------------------------------------------- */

// patchRequest carries only the fields a recruiter may change. A pointer means
// "supplied": an absent field is left alone rather than cleared.
type patchRequest struct {
	RoundTitle      *string   `json:"roundTitle"`
	RoundType       *string   `json:"roundType"`
	ScheduledStart  *string   `json:"scheduledStart"`
	DurationMinutes *int      `json:"durationMinutes"`
	Format          *string   `json:"format"`
	MeetingLink     *string   `json:"meetingLink"`
	Notes           *string   `json:"notes"`
	PanelMemberIDs  *[]string `json:"panelMemberIds"`
	Status          *string   `json:"status"`
	// Note is recorded as the outcome or the cancellation reason, depending on
	// which status the round is moving to.
	Note string `json:"note"`
}

func (a *API) handlePatch(w http.ResponseWriter, r *http.Request) {
	companyID, principal, err := tenantOf(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	var req patchRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	problems := map[string][]string{}

	var start *time.Time
	if req.ScheduledStart != nil {
		parsed, badStart := parseTimestamp(*req.ScheduledStart)
		if badStart != "" {
			problems["scheduledStart"] = []string{badStart}
		}
		start = parsed
	}

	var status *domain.Status
	if req.Status != nil {
		candidate := domain.Status(strings.TrimSpace(*req.Status))
		if !candidate.Valid() {
			problems["status"] = []string{"Status must be one of: " + strings.Join(domain.Statuses(), ", ") + "."}
		} else {
			status = &candidate
		}
	}

	merge(problems, domain.ValidateRound(domain.RoundInput{
		RoundTitle:      req.RoundTitle,
		RoundType:       req.RoundType,
		ScheduledStart:  start,
		DurationMinutes: req.DurationMinutes,
		Format:          req.Format,
		MeetingLink:     req.MeetingLink,
		Notes:           req.Notes,
		PanelMemberIDs:  req.PanelMemberIDs,
	}, false))

	if len(problems) > 0 {
		httpx.WriteProblem(w, r, httpx.ValidationFailed(problems))
		return
	}

	a.transition(w, r, companyID, store.InterviewPatch{
		RoundTitle:      trimPtr(req.RoundTitle),
		RoundType:       trimPtr(req.RoundType),
		ScheduledStart:  start,
		DurationMinutes: req.DurationMinutes,
		Format:          trimPtr(req.Format),
		MeetingLink:     trimPtr(req.MeetingLink),
		Notes:           req.Notes,
		PanelMemberIDs:  req.PanelMemberIDs,
		Status:          status,
		Note:            strings.TrimSpace(req.Note),
	}, principal.Subject)
}

/* -------------------------------------------------------------------------- */
/* Cancel and complete                                                        */
/* -------------------------------------------------------------------------- */

// noteRequest is the optional explanation attached to a cancellation or an
// outcome.
type noteRequest struct {
	Note   string `json:"note"`
	Reason string `json:"reason"`
}

// text prefers whichever field the caller filled in. The two names exist because
// "reason" reads right on a cancellation and "note" on a completion, and making
// a client pick the one this service happens to prefer buys nothing.
func (n noteRequest) text() string {
	if trimmed := strings.TrimSpace(n.Reason); trimmed != "" {
		return trimmed
	}
	return strings.TrimSpace(n.Note)
}

func (a *API) handleCancel(w http.ResponseWriter, r *http.Request) {
	a.handleStatusChange(w, r, domain.StatusCancelled)
}

func (a *API) handleComplete(w http.ResponseWriter, r *http.Request) {
	a.handleStatusChange(w, r, domain.StatusCompleted)
}

func (a *API) handleStatusChange(w http.ResponseWriter, r *http.Request, to domain.Status) {
	companyID, principal, err := tenantOf(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	var req noteRequest
	if err := decodeOptionalJSON(r, &req); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	a.transition(w, r, companyID, store.InterviewPatch{Status: &to, Note: req.text()}, principal.Subject)
}

// transition applies a patch and reports a refused status change as a 409.
//
// The legality check itself happens in the store, inside the transaction that
// holds the row: checking here would read a status that another request can
// change before the write lands.
func (a *API) transition(
	w http.ResponseWriter, r *http.Request, companyID string, patch store.InterviewPatch, actorID string,
) {
	interview, err := a.store.UpdateInterview(r.Context(), companyID, r.PathValue("id"), patch, actorID)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, interview)
}

/* -------------------------------------------------------------------------- */
/* Delete                                                                     */
/* -------------------------------------------------------------------------- */

func (a *API) handleDelete(w http.ResponseWriter, r *http.Request) {
	companyID, principal, err := tenantOf(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	if err := a.store.DeleteInterview(r.Context(), companyID, r.PathValue("id"), principal.Subject); err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	httpx.NoContent(w)
}

/* -------------------------------------------------------------------------- */
/* Helpers                                                                    */
/* -------------------------------------------------------------------------- */

// parseTimestamp returns the parsed time, or the message a client should see.
//
// The message is written here rather than taken from time.Parse, which quotes
// the offending input back — noise at best, and reflected caller content in an
// error body at worst.
func parseTimestamp(raw string) (*time.Time, string) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, ""
	}
	parsed, err := time.Parse(time.RFC3339, trimmed)
	if err != nil {
		return nil, "Use a full RFC 3339 timestamp, for example 2026-03-04T14:00:00Z."
	}
	return &parsed, ""
}

func merge(into, from map[string][]string) {
	for field, messages := range from {
		into[field] = append(into[field], messages...)
	}
}

func trimPtr(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	return &trimmed
}
