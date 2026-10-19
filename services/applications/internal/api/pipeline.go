package api

import (
	"net/http"
	"strings"

	"github.com/karmarat/platform/packages/goshared/httpx"
	"github.com/karmarat/platform/services/applications/internal/domain"
	"github.com/karmarat/platform/services/applications/internal/store"
)

/* -------------------------------------------------------------------------- */
/* Stages                                                                     */
/* -------------------------------------------------------------------------- */

func (a *API) handleListStages(w http.ResponseWriter, r *http.Request) {
	companyID, _, err := tenantOf(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	stages, err := a.store.ListStages(r.Context(), companyID)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"stages": stages})
}

type stageRequest struct {
	Key        string `json:"key"`
	Name       string `json:"name"`
	Order      *int   `json:"order"`
	Type       string `json:"type"`
	IsTerminal *bool  `json:"isTerminal"`
	Color      string `json:"color"`
}

func (a *API) handleCreateStage(w http.ResponseWriter, r *http.Request) {
	companyID, _, err := tenantOf(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	var req stageRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	stage := domain.Stage{
		Key:   strings.TrimSpace(req.Key),
		Name:  strings.TrimSpace(req.Name),
		Type:  domain.StageType(strings.TrimSpace(req.Type)),
		Color: strings.TrimSpace(req.Color),
	}
	if req.Order != nil {
		stage.Order = *req.Order
	}
	if req.IsTerminal != nil {
		stage.IsTerminal = *req.IsTerminal
	}

	if problems := validateStage(stage); len(problems) > 0 {
		httpx.WriteProblem(w, r, httpx.ValidationFailed(problems))
		return
	}

	created, err := a.store.CreateStage(r.Context(), companyID, stage)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	httpx.WriteJSON(w, http.StatusCreated, created)
}

func (a *API) handleUpdateStage(w http.ResponseWriter, r *http.Request) {
	companyID, _, err := tenantOf(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	var req stageRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	patch := store.StagePatch{Order: req.Order, IsTerminal: req.IsTerminal}
	if name := strings.TrimSpace(req.Name); name != "" {
		patch.Name = &name
	}
	if color := strings.TrimSpace(req.Color); color != "" {
		patch.Color = &color
	}
	if raw := strings.TrimSpace(req.Type); raw != "" {
		stageType := domain.StageType(raw)
		if !stageType.Valid() {
			httpx.WriteProblem(w, r, httpx.ValidationFailed(map[string][]string{
				"type": {"Use one of: applied, screening, interview, offer, hired, rejected."}}))
			return
		}
		patch.Type = &stageType
	}

	updated, err := a.store.UpdateStage(r.Context(), companyID, r.PathValue("id"), patch)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	httpx.WriteJSON(w, http.StatusOK, updated)
}

type reorderRequest struct {
	IDs []string `json:"ids"`
}

func (a *API) handleReorderStages(w http.ResponseWriter, r *http.Request) {
	companyID, _, err := tenantOf(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	var req reorderRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}
	if len(req.IDs) == 0 {
		httpx.WriteProblem(w, r, httpx.ValidationFailed(map[string][]string{
			"ids": {"Supply the stage ids in their new order."}}))
		return
	}

	stages, err := a.store.ReorderStages(r.Context(), companyID, req.IDs)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"stages": stages})
}

func (a *API) handleDeleteStage(w http.ResponseWriter, r *http.Request) {
	companyID, _, err := tenantOf(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	// Deleting a stage that still holds applications would strand them outside
	// the pipeline, so the caller has to say where they go. The store reports
	// ErrStageInUse when this is missing, which surfaces as a 409 naming the fix.
	moveTo := strings.TrimSpace(r.URL.Query().Get("moveTo"))

	moved, err := a.store.DeleteStage(r.Context(), companyID, r.PathValue("id"), moveTo)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"movedApplications": moved})
}

func validateStage(stage domain.Stage) map[string][]string {
	problems := map[string][]string{}

	if stage.Key == "" {
		problems["key"] = []string{"A stage key is required."}
	} else if !isSlug(stage.Key) {
		problems["key"] = []string{"Use lowercase letters, numbers and underscores."}
	}
	if stage.Name == "" {
		problems["name"] = []string{"A stage name is required."}
	}
	if !stage.Type.Valid() {
		problems["type"] = []string{"Use one of: applied, screening, interview, offer, hired, rejected."}
	}
	if stage.Order < 0 {
		problems["order"] = []string{"Order may not be negative."}
	}

	return problems
}

func isSlug(value string) bool {
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
		default:
			return false
		}
	}
	return value != ""
}

/* -------------------------------------------------------------------------- */
/* Export                                                                     */
/* -------------------------------------------------------------------------- */

func (a *API) handleExport(w http.ResponseWriter, r *http.Request) {
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
	// An export is a whole-pipeline snapshot rather than a page, but it still
	// needs a ceiling: an unbounded scan is how one recruiter's click becomes an
	// outage for every tenant on the shard.
	filter.Limit = exportLimit
	filter.Cursor = ""

	page, err := a.store.ListApplications(r.Context(), companyID, filter)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	stages, err := a.store.ListStages(r.Context(), companyID)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}
	stageNames := make(map[string]string, len(stages))
	for _, stage := range stages {
		stageNames[stage.ID] = stage.Name
	}

	reasons, err := a.store.ListRejectionReasons(r.Context(), companyID, false)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}
	reasonLabels := make(map[string]string, len(reasons))
	for _, reason := range reasons {
		reasonLabels[reason.ID] = reason.Label
	}

	writeApplicationsCSV(w, page.Applications, stageNames, reasonLabels)
}
