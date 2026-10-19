package api

import (
	"encoding/csv"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/karmarat/platform/packages/goshared/httpx"
	"github.com/karmarat/platform/services/jobs/internal/domain"
)

// exportColumns is the header row, and the order every row is written in.
var exportColumns = []string{
	"id", "slug", "title", "department", "locations", "work_mode", "employment_type",
	"seniority", "status", "headcount", "salary_min", "salary_max", "salary_currency",
	"salary_is_public", "visible_on_portal", "visible_on_network",
	"hiring_manager_id", "recruiter_id", "opened_at", "closed_at", "created_at", "updated_at",
}

// handleExport streams the requisition list as CSV.
//
// The same filters as the list endpoint apply, so "export what I am looking at"
// is one request rather than a client paging through and stitching the result.
func (a *API) handleExport(w http.ResponseWriter, r *http.Request) {
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

	jobs, err := a.store.ExportAll(r.Context(), companyID, filter)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition",
		`attachment; filename="requisitions-`+time.Now().UTC().Format("2006-01-02")+`.csv"`)
	w.WriteHeader(http.StatusOK)

	writer := csv.NewWriter(w)
	defer writer.Flush()

	// Headers are already sent, so a failure from here cannot become a problem
	// document — the export simply ends short, and the reason is logged.
	if err := writer.Write(exportColumns); err != nil {
		a.logger.Error("jobs export failed", slog.Any("error", err))
		return
	}

	for _, job := range jobs {
		if err := writer.Write(exportRow(job)); err != nil {
			a.logger.Error("jobs export failed",
				slog.String("job_id", job.ID), slog.Any("error", err))
			return
		}
	}
}

func exportRow(job domain.Job) []string {
	return []string{
		job.ID,
		job.Slug,
		job.Title,
		job.Department,
		strings.Join(job.Locations, "; "),
		string(job.WorkMode),
		string(job.EmploymentType),
		string(job.Seniority),
		string(job.Status),
		strconv.Itoa(job.Headcount),
		formatMinorUnits(job.Salary.Min),
		formatMinorUnits(job.Salary.Max),
		job.Salary.Currency,
		strconv.FormatBool(job.Salary.Public),
		strconv.FormatBool(job.VisibleOnPortal),
		strconv.FormatBool(job.VisibleOnNetwork),
		job.HiringManagerID,
		job.RecruiterID,
		formatTime(job.OpenedAt),
		formatTime(job.ClosedAt),
		job.CreatedAt.UTC().Format(time.RFC3339),
		job.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

// formatMinorUnits writes the stored integer, not a decimal.
//
// A spreadsheet that reads "4500000" and a service that reads 4500000 agree;
// one that reads "45,000.00" depends on the locale of whoever opens it.
func formatMinorUnits(amount *int64) string {
	if amount == nil {
		return ""
	}
	return strconv.FormatInt(*amount, 10)
}

func formatTime(at *time.Time) string {
	if at == nil {
		return ""
	}
	return at.UTC().Format(time.RFC3339)
}
