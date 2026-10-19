package api

import (
	"net/http"
	"strings"

	"github.com/karmarat/platform/packages/goshared/httpx"
	"github.com/karmarat/platform/services/applications/internal/domain"
	"github.com/karmarat/platform/services/applications/internal/store"
)

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

	page, err := a.store.ListApplications(r.Context(), companyID, filter)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"applications": page.Applications,
		"nextCursor":   page.NextCursor,
	})
}

func listFilter(r *http.Request) (store.ListFilter, error) {
	limit, cursor, err := pagination(r)
	if err != nil {
		return store.ListFilter{}, err
	}

	query := r.URL.Query()
	filter := store.ListFilter{
		JobID:   strings.TrimSpace(query.Get("jobId")),
		StageID: strings.TrimSpace(query.Get("stageId")),
		Status:  strings.TrimSpace(query.Get("status")),
		Source:  strings.TrimSpace(query.Get("source")),
		Query:   strings.TrimSpace(query.Get("q")),
		Limit:   limit,
		Cursor:  cursor,
	}

	// The enum columns are compared against the filter directly, so an unknown
	// value would reach Postgres as an invalid enum literal and surface as a
	// 500. Rejecting it here makes it a 422 that names the field.
	if filter.Status != "" && !domain.Status(filter.Status).Valid() {
		return store.ListFilter{}, httpx.ValidationFailed(map[string][]string{
			"status": {"Status must be one of: active, rejected, withdrawn, hired."}})
	}
	if filter.Source != "" && !domain.Source(filter.Source).Valid() {
		return store.ListFilter{}, httpx.ValidationFailed(map[string][]string{
			"source": {"Source must be one of: " + strings.Join(domain.Sources(), ", ") + "."}})
	}

	if filter.DateFrom, err = parseDate(query.Get("dateFrom"), "dateFrom"); err != nil {
		return store.ListFilter{}, err
	}
	if filter.DateTo, err = parseDate(query.Get("dateTo"), "dateTo"); err != nil {
		return store.ListFilter{}, err
	}

	return filter, nil
}

func (a *API) handleGet(w http.ResponseWriter, r *http.Request) {
	companyID, _, err := tenantOf(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	application, err := a.store.FindApplication(r.Context(), companyID, r.PathValue("id"))
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	httpx.WriteJSON(w, http.StatusOK, application)
}

func (a *API) handleHistory(w http.ResponseWriter, r *http.Request) {
	companyID, _, err := tenantOf(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	// Reading the application first is what scopes the history: an id alone
	// would otherwise return another tenant's audit trail.
	application, err := a.store.FindApplication(r.Context(), companyID, r.PathValue("id"))
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	history, err := a.store.ListApplicationEvents(r.Context(), companyID, application.ID)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"events": history})
}

type patchApplicationRequest struct {
	Source    *string `json:"source"`
	Rating    *int    `json:"rating"`
	ResumeKey *string `json:"resumeKey"`
}

func (a *API) handlePatch(w http.ResponseWriter, r *http.Request) {
	companyID, principal, err := tenantOf(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	var req patchApplicationRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	fields := map[string][]string{}
	if req.Source != nil && !domain.Source(*req.Source).Valid() {
		fields["source"] = []string{"Source must be one of: " + strings.Join(domain.Sources(), ", ") + "."}
	}
	if req.Rating != nil && (*req.Rating < 1 || *req.Rating > 5) {
		fields["rating"] = []string{"Rating must be between 1 and 5."}
	}
	if req.ResumeKey != nil && !validResumeKey(*req.ResumeKey) {
		fields["resumeKey"] = []string{"A resume key must be an object key produced by the upload step."}
	}
	if len(fields) > 0 {
		httpx.WriteProblem(w, r, httpx.ValidationFailed(fields))
		return
	}

	application, err := a.store.UpdateApplication(r.Context(), companyID, r.PathValue("id"), store.ApplicationPatch{
		Source:    req.Source,
		Rating:    req.Rating,
		ResumeKey: req.ResumeKey,
	}, principal.Subject)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	httpx.WriteJSON(w, http.StatusOK, application)
}

type advanceRequest struct {
	StageID string `json:"stageId"`
	Note    string `json:"note"`
}

func (a *API) handleAdvance(w http.ResponseWriter, r *http.Request) {
	companyID, principal, err := tenantOf(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	var req advanceRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}
	if strings.TrimSpace(req.StageID) == "" {
		httpx.WriteProblem(w, r, httpx.ValidationFailed(map[string][]string{
			"stageId": {"A destination stage is required."}}))
		return
	}

	application, err := a.store.AdvanceApplication(r.Context(),
		companyID, r.PathValue("id"), strings.TrimSpace(req.StageID), principal.Subject, strings.TrimSpace(req.Note))
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	httpx.WriteJSON(w, http.StatusOK, application)
}

type rejectRequest struct {
	ReasonID string `json:"reasonId"`
	Note     string `json:"note"`
}

func (a *API) handleReject(w http.ResponseWriter, r *http.Request) {
	companyID, principal, err := tenantOf(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	var req rejectRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}
	if strings.TrimSpace(req.ReasonID) == "" {
		httpx.WriteProblem(w, r, httpx.ValidationFailed(map[string][]string{
			"reasonId": {"A rejection reason is required."}}))
		return
	}

	application, err := a.store.RejectApplication(r.Context(), companyID, r.PathValue("id"),
		strings.TrimSpace(req.ReasonID), strings.TrimSpace(req.Note), principal.Subject)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	httpx.WriteJSON(w, http.StatusOK, application)
}

func (a *API) handleDelete(w http.ResponseWriter, r *http.Request) {
	companyID, _, err := tenantOf(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	if err := a.store.DeleteApplication(r.Context(), companyID, r.PathValue("id")); err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	httpx.NoContent(w)
}

// maxBulkIDs bounds one bulk request. A recruiter selecting a page of results
// stays well inside it; an unbounded list is a way to hold a transaction open.
const maxBulkIDs = 200

type bulkRequest struct {
	IDs      []string `json:"ids"`
	Action   string   `json:"action"`
	StageID  string   `json:"stageId"`
	ReasonID string   `json:"reasonId"`
	Note     string   `json:"note"`
}

type bulkOutcome struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

func (a *API) handleBulk(w http.ResponseWriter, r *http.Request) {
	companyID, principal, err := tenantOf(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	var req bulkRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	fields := map[string][]string{}
	if len(req.IDs) == 0 {
		fields["ids"] = []string{"Select at least one application."}
	}
	if len(req.IDs) > maxBulkIDs {
		fields["ids"] = []string{"A bulk action may cover at most 200 applications at a time."}
	}

	action := strings.TrimSpace(req.Action)
	switch action {
	case "advance":
		if strings.TrimSpace(req.StageID) == "" {
			fields["stageId"] = []string{"A destination stage is required."}
		}
	case "reject":
		if strings.TrimSpace(req.ReasonID) == "" {
			fields["reasonId"] = []string{"A rejection reason is required."}
		}
	case "delete":
	default:
		fields["action"] = []string{"Action must be one of: advance, reject, delete."}
	}
	if len(fields) > 0 {
		httpx.WriteProblem(w, r, httpx.ValidationFailed(fields))
		return
	}

	// Bulk is a way of doing the same thing many times, not a way of doing
	// something a role is not allowed to do once: the route checks
	// applications.bulk_update, and the action's own permission is checked here.
	required := map[string]string{
		"advance": "applications.advance_stage",
		"reject":  "applications.reject",
		"delete":  "applications.delete",
	}[action]
	if !principal.Can(required) {
		httpx.WriteProblem(w, r, httpx.PermissionDenied([]string{required}))
		return
	}

	outcomes := make([]bulkOutcome, 0, len(req.IDs))
	succeeded := 0

	for _, id := range req.IDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}

		var actionErr error
		switch action {
		case "advance":
			_, actionErr = a.store.AdvanceApplication(r.Context(),
				companyID, id, strings.TrimSpace(req.StageID), principal.Subject, strings.TrimSpace(req.Note))
		case "reject":
			_, actionErr = a.store.RejectApplication(r.Context(),
				companyID, id, strings.TrimSpace(req.ReasonID), strings.TrimSpace(req.Note), principal.Subject)
		case "delete":
			actionErr = a.store.DeleteApplication(r.Context(), companyID, id)
		}

		// One unreachable row must not abandon the other hundred and ninety-nine,
		// so each id reports its own outcome instead of failing the request.
		if actionErr != nil {
			outcomes = append(outcomes, bulkOutcome{
				ID:     id,
				Status: "failed",
				Reason: httpx.AsProblem(mapError(actionErr)).Detail,
			})
			continue
		}

		succeeded++
		outcomes = append(outcomes, bulkOutcome{ID: id, Status: "ok"})
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"action":    action,
		"succeeded": succeeded,
		"failed":    len(outcomes) - succeeded,
		"results":   outcomes,
	})
}

// validResumeKey checks the shape of an object key before it is stored.
//
// The key names a path in the bucket, so a traversal segment or an absolute
// path in it would point at an object the tenant prefix was meant to exclude.
func validResumeKey(key string) bool {
	trimmed := strings.TrimSpace(key)
	if trimmed == "" || len(trimmed) > 512 {
		return false
	}
	if strings.HasPrefix(trimmed, "/") || strings.Contains(trimmed, "..") {
		return false
	}
	return !strings.ContainsAny(trimmed, "\\\n\r")
}
