package api

import (
	"encoding/csv"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/karmarat/platform/packages/goshared/httpx"
	"github.com/karmarat/platform/services/applications/internal/domain"
	"github.com/karmarat/platform/services/applications/internal/store"
)

// exportLimit caps a CSV export.
//
// An export is a snapshot rather than a page, but it still needs a ceiling: an
// unbounded scan turns one recruiter's click into an outage for every tenant
// sharing the database.
const exportLimit = 10_000

/* -------------------------------------------------------------------------- */
/* Rejection reasons                                                          */
/* -------------------------------------------------------------------------- */

func (a *API) handleListReasons(w http.ResponseWriter, r *http.Request) {
	companyID, _, err := tenantOf(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	activeOnly := strings.EqualFold(r.URL.Query().Get("active"), "true")

	reasons, err := a.store.ListRejectionReasons(r.Context(), companyID, activeOnly)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"rejectionReasons": reasons})
}

type reasonRequest struct {
	Label    string `json:"label"`
	Order    *int   `json:"order"`
	IsActive *bool  `json:"isActive"`
}

func (a *API) handleCreateReason(w http.ResponseWriter, r *http.Request) {
	companyID, _, err := tenantOf(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	var req reasonRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	label := strings.TrimSpace(req.Label)
	if label == "" {
		httpx.WriteProblem(w, r, httpx.ValidationFailed(map[string][]string{
			"label": {"A label is required."}}))
		return
	}

	order := 0
	if req.Order != nil {
		order = *req.Order
	}

	reason, err := a.store.CreateRejectionReason(r.Context(), companyID, label, order)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	httpx.WriteJSON(w, http.StatusCreated, reason)
}

func (a *API) handleUpdateReason(w http.ResponseWriter, r *http.Request) {
	companyID, _, err := tenantOf(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	var req reasonRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	patch := store.ReasonPatch{Order: req.Order, IsActive: req.IsActive}
	if label := strings.TrimSpace(req.Label); label != "" {
		patch.Label = &label
	}

	reason, err := a.store.UpdateRejectionReason(r.Context(), companyID, r.PathValue("id"), patch)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	httpx.WriteJSON(w, http.StatusOK, reason)
}

func (a *API) handleDeleteReason(w http.ResponseWriter, r *http.Request) {
	companyID, _, err := tenantOf(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	// A reason already recorded against an application cannot be deleted without
	// rewriting history, so the store reports ErrReasonInUse and the caller is
	// told to deactivate it instead.
	if err := a.store.DeleteRejectionReason(r.Context(), companyID, r.PathValue("id")); err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	httpx.NoContent(w)
}

/* -------------------------------------------------------------------------- */
/* CSV                                                                        */
/* -------------------------------------------------------------------------- */

// writeApplicationsCSV streams the pipeline as a spreadsheet.
//
// Stage and reason are written as names rather than ids: the file is opened by a
// person, and an id column would make them join it back by hand. Internal
// rejection notes are deliberately absent — an export is routinely forwarded,
// and a note written for colleagues should not travel with it.
func writeApplicationsCSV(
	w http.ResponseWriter,
	applications []domain.Application,
	stageNames map[string]string,
	reasonLabels map[string]string,
) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition",
		`attachment; filename="applications-`+time.Now().UTC().Format(time.DateOnly)+`.csv"`)

	writer := csv.NewWriter(w)
	defer writer.Flush()

	_ = writer.Write([]string{
		"Application ID", "Candidate", "Email", "Job", "Stage", "Status",
		"Source", "Rating", "Rejection reason", "Submitted at",
	})

	for _, application := range applications {
		rating := ""
		if application.Rating != nil {
			rating = strconv.Itoa(*application.Rating)
		}

		_ = writer.Write([]string{
			application.ID,
			application.CandidateName,
			application.CandidateEmail,
			application.JobTitle,
			stageNames[application.StageID],
			string(application.Status),
			string(application.Source),
			rating,
			reasonLabels[application.RejectionReasonID],
			application.SubmittedAt.UTC().Format(time.RFC3339),
		})
	}
}
