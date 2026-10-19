package api

import (
	"encoding/csv"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/karmarat/platform/packages/goshared/httpx"
	"github.com/karmarat/platform/services/audit/internal/domain"
)

// exportColumns is the header row, and the order every row is written in.
var exportColumns = []string{
	"id", "occurred_at", "recorded_at", "action", "subject",
	"entity_type", "entity_id", "actor_id", "correlation_id", "payload",
}

// handleCompanyExport streams the caller's trail as CSV.
//
// It shares the filter with the list endpoint, so the file matches the screen it
// was downloaded from. It does not share the pagination: an export is the whole
// filtered set up to the store's cap, because a CSV a reader has to stitch
// together from pages is a CSV they will stitch together wrongly.
func (a *API) handleCompanyExport(w http.ResponseWriter, r *http.Request) {
	companyID, err := tenantOf(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	filter, err := filterFrom(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition",
		`attachment; filename="audit-`+time.Now().UTC().Format(time.DateOnly)+`.csv"`)
	w.WriteHeader(http.StatusOK)

	writer := csv.NewWriter(w)
	defer writer.Flush()

	// Headers are already sent, so a failure from here cannot become a problem
	// document — the export simply ends short, and the reason is logged.
	if err := writer.Write(exportColumns); err != nil {
		a.logger.Error("audit export failed", slog.Any("error", err))
		return
	}

	err = a.store.ExportForCompany(r.Context(), companyID, filter, func(entry domain.Entry) error {
		return writer.Write(exportRow(entry))
	})
	if err != nil {
		a.logger.Error("audit export failed",
			slog.String("company_id", companyID), slog.Any("error", err))
	}
}

func exportRow(entry domain.Entry) []string {
	row := []string{
		entry.ID,
		entry.OccurredAt.UTC().Format(time.RFC3339),
		entry.RecordedAt.UTC().Format(time.RFC3339),
		entry.Action,
		entry.Subject,
		entry.EntityType,
		entry.EntityID,
		entry.ActorID,
		entry.CorrelationID,
		encodePayload(entry.Payload),
	}

	// Every cell, not only the payload: an actor id or an entity id can carry a
	// value some other service accepted from a person.
	for i, cell := range row {
		row[i] = neutralizeFormula(cell)
	}
	return row
}

// encodePayload writes the already-redacted payload back as compact JSON.
//
// A failure here is written as an empty cell rather than aborting the download:
// one unencodable payload must not cost the reader the other forty thousand
// rows, and the entry's own columns still record that the event happened.
func encodePayload(payload map[string]any) string {
	if len(payload) == 0 {
		return ""
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	return string(encoded)
}

// neutralizeFormula stops a spreadsheet from executing a cell.
//
// Excel, Numbers and Sheets all treat a cell beginning with =, +, - or @ as a
// formula, so a value that reached the trail from user input — an actor id
// copied from an integration, a payload string that survived redaction — can
// become a command that runs when an administrator opens the export. The value
// is prefixed with an apostrophe, which every one of those readers strips on
// display, so the cell still shows what was recorded.
func neutralizeFormula(value string) string {
	if value == "" {
		return value
	}
	if strings.ContainsRune("=+-@\t\r", rune(value[0])) {
		return "'" + value
	}
	return value
}
