package api

import (
	"encoding/csv"
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

// poolExportLimit caps a CSV export of a company's own pool.
const poolExportLimit = 10_000

/* -------------------------------------------------------------------------- */
/* The company's own candidate pool                                           */
/* -------------------------------------------------------------------------- */

// poolView is a company-private candidate record.
//
// Unlike a talent-search result this one does carry contact details: the company
// either sourced the person themselves or received an application, so it already
// holds them. The distinction between this type and domain.SearchResult is the
// whole boundary between "our records" and "the platform's candidates".
type poolView struct {
	ID              string   `json:"id"`
	FullName        string   `json:"fullName"`
	Email           string   `json:"email,omitempty"`
	Phone           string   `json:"phone,omitempty"`
	Headline        string   `json:"headline,omitempty"`
	Location        string   `json:"location,omitempty"`
	Source          string   `json:"source"`
	CurrentTitle    string   `json:"currentTitle,omitempty"`
	CurrentEmployer string   `json:"currentEmployer,omitempty"`
	YearsExperience int      `json:"yearsExperience"`
	Skills          []string `json:"skills"`
	Tags            []string `json:"tags"`
	Notes           string   `json:"notes,omitempty"`
	LinkedProfileID string   `json:"linkedProfileId,omitempty"`

	ResumeFilename  string `json:"resumeFilename,omitempty"`
	ResumeSizeBytes int64  `json:"resumeSizeBytes,omitempty"`
	HasResume       bool   `json:"hasResume"`

	CreatedBy string    `json:"createdBy,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func toPoolView(c domain.PoolCandidate) poolView {
	return poolView{
		ID:              c.ID,
		FullName:        c.FullName,
		Email:           c.Email,
		Phone:           c.Phone,
		Headline:        c.Headline,
		Location:        c.Location,
		Source:          string(c.Source),
		CurrentTitle:    c.CurrentTitle,
		CurrentEmployer: c.CurrentEmployer,
		YearsExperience: c.YearsExperience,
		Skills:          orEmpty(c.Skills),
		Tags:            orEmpty(c.Tags),
		Notes:           c.Notes,
		LinkedProfileID: c.LinkedProfileID,
		ResumeFilename:  c.ResumeFilename,
		ResumeSizeBytes: c.ResumeSizeBytes,
		// The object key is never exposed: it is only useful for reaching the
		// bucket directly, which is exactly what presigned downloads exist to
		// prevent.
		HasResume: c.ResumeObjectKey != "",
		CreatedBy: c.CreatedBy,
		CreatedAt: c.CreatedAt,
		UpdatedAt: c.UpdatedAt,
	}
}

func orEmpty(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func poolFilterOf(r *http.Request) store.PoolFilter {
	query := r.URL.Query()
	return store.PoolFilter{
		Query:  strings.TrimSpace(query.Get("q")),
		Source: domain.Source(strings.TrimSpace(query.Get("source"))),
		Skill:  strings.TrimSpace(query.Get("skill")),
	}
}

func (a *API) handleListPool(w http.ResponseWriter, r *http.Request) {
	companyID, ok := companyOf(w, r)
	if !ok {
		return
	}

	page, err := pageOf(r)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	candidates, err := a.store.ListPoolCandidates(r.Context(), companyID, poolFilterOf(r), page)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	views := make([]poolView, 0, len(candidates))
	for _, candidate := range candidates {
		views = append(views, toPoolView(candidate))
	}

	cursor := ""
	if len(candidates) > 0 {
		last := candidates[len(candidates)-1]
		cursor = nextCursor(len(candidates), page.Limit, last.CreatedAt, last.ID)
	}

	httpx.WriteJSON(w, http.StatusOK, listResponse{Data: views, NextCursor: cursor})
}

type poolRequest struct {
	FullName        string   `json:"fullName"`
	Email           string   `json:"email"`
	Phone           string   `json:"phone"`
	Headline        string   `json:"headline"`
	Location        string   `json:"location"`
	Source          string   `json:"source"`
	CurrentTitle    string   `json:"currentTitle"`
	CurrentEmployer string   `json:"currentEmployer"`
	YearsExperience int      `json:"yearsExperience"`
	Skills          []string `json:"skills"`
	Tags            []string `json:"tags"`
	Notes           string   `json:"notes"`
}

func (req poolRequest) toDomain() (domain.PoolCandidate, error) {
	v := domain.NewValidation()

	name := strings.TrimSpace(req.FullName)
	if name == "" {
		v.Add("fullName", "A name is required.")
	} else if len(name) > 200 {
		v.Add("fullName", "Name must be 200 characters or fewer.")
	}

	email := strings.TrimSpace(strings.ToLower(req.Email))
	if email != "" && !domain.ValidEmail(email) {
		v.Add("email", "Enter a valid email address.")
	}

	source := domain.Source(strings.TrimSpace(req.Source))
	if source == "" {
		source = domain.SourceSourced
	} else if !source.Valid() {
		v.Add("source", "That is not a recognized source.")
	}

	if req.YearsExperience < 0 || req.YearsExperience > 70 {
		v.Add("yearsExperience", "Years of experience must be between 0 and 70.")
	}

	if err := v.Err(); err != nil {
		return domain.PoolCandidate{}, err
	}

	return domain.PoolCandidate{
		FullName:        name,
		Email:           email,
		Phone:           strings.TrimSpace(req.Phone),
		Headline:        strings.TrimSpace(req.Headline),
		Location:        strings.TrimSpace(req.Location),
		Source:          source,
		CurrentTitle:    strings.TrimSpace(req.CurrentTitle),
		CurrentEmployer: strings.TrimSpace(req.CurrentEmployer),
		YearsExperience: req.YearsExperience,
		Skills:          domain.NormalizeList(req.Skills),
		Tags:            domain.NormalizeList(req.Tags),
		Notes:           strings.TrimSpace(req.Notes),
	}, nil
}

func (a *API) handleCreatePoolCandidate(w http.ResponseWriter, r *http.Request) {
	companyID, ok := companyOf(w, r)
	if !ok {
		return
	}
	principal := tenancy.MustFromContext(r.Context())

	var req poolRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	candidate, err := req.toDomain()
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	candidate.ID = idgen.New("cand")
	candidate.CompanyID = companyID
	candidate.CreatedBy = principal.Subject

	created, err := a.store.CreatePoolCandidate(r.Context(), candidate)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	httpx.WriteJSON(w, http.StatusCreated, toPoolView(created))
}

func (a *API) handleGetPoolCandidate(w http.ResponseWriter, r *http.Request) {
	companyID, ok := companyOf(w, r)
	if !ok {
		return
	}

	candidate, err := a.store.FindPoolCandidate(r.Context(), companyID, r.PathValue("id"))
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	httpx.WriteJSON(w, http.StatusOK, toPoolView(candidate))
}

func (a *API) handleUpdatePoolCandidate(w http.ResponseWriter, r *http.Request) {
	companyID, ok := companyOf(w, r)
	if !ok {
		return
	}

	var req poolRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	candidate, err := req.toDomain()
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}
	candidate.ID = r.PathValue("id")

	updated, err := a.store.UpdatePoolCandidate(r.Context(), companyID, candidate)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	httpx.WriteJSON(w, http.StatusOK, toPoolView(updated))
}

func (a *API) handleDeletePoolCandidate(w http.ResponseWriter, r *http.Request) {
	companyID, ok := companyOf(w, r)
	if !ok {
		return
	}

	// Soft delete: an application or an interview may still reference this row,
	// and a hard delete would leave those pointing at nothing.
	if err := a.store.SoftDeletePoolCandidate(r.Context(), companyID, r.PathValue("id")); err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	httpx.NoContent(w)
}

/* -------------------------------------------------------------------------- */
/* Pool resumes                                                               */
/* -------------------------------------------------------------------------- */

type poolUploadRequest struct {
	Filename    string `json:"filename"`
	ContentType string `json:"contentType"`
	SizeBytes   int64  `json:"sizeBytes"`
}

func (a *API) handlePoolResumeUploadURL(w http.ResponseWriter, r *http.Request) {
	companyID, ok := companyOf(w, r)
	if !ok {
		return
	}

	var req poolUploadRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	contentType := strings.ToLower(strings.TrimSpace(req.ContentType))
	if err := domain.ValidateResumeUpload(req.Filename, contentType, req.SizeBytes); err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	// Confirm the record is ours before signing anything: a signed URL is a
	// capability that outlives this request, so the ownership check has to happen
	// before it is minted rather than when it is used.
	candidate, err := a.store.FindPoolCandidate(r.Context(), companyID, r.PathValue("id"))
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	// Keyed under the company, not the candidate: this document belongs to the
	// tenant's own records, and a bucket policy can enforce that prefix.
	key := "company/" + companyID + "/candidates/" + candidate.ID +
		domain.ResumeContentTypes[contentType]

	url, err := a.presigner.PresignPut(key, contentType, a.uploadTTL)
	if err != nil {
		a.logger.Error("could not sign an upload url", "error", err)
		httpx.WriteProblem(w, r, httpx.Internal("We could not prepare that upload just now."))
		return
	}

	if _, err := a.store.AttachPoolResume(r.Context(), companyID, candidate.ID, key,
		strings.TrimSpace(req.Filename), contentType, req.SizeBytes); err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"uploadUrl": url,
		"expiresIn": int(a.uploadTTL.Seconds()),
	})
}

func (a *API) handlePoolResumeDownloadURL(w http.ResponseWriter, r *http.Request) {
	companyID, ok := companyOf(w, r)
	if !ok {
		return
	}

	candidate, err := a.store.FindPoolCandidate(r.Context(), companyID, r.PathValue("id"))
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}
	if candidate.ResumeObjectKey == "" {
		httpx.WriteProblem(w, r, mapError(domain.ErrNoResume))
		return
	}

	filename := candidate.ResumeFilename
	if filename == "" {
		filename = "resume"
	}

	url, err := a.presigner.PresignGet(candidate.ResumeObjectKey, filename, a.downloadTTL)
	if err != nil {
		a.logger.Error("could not sign a download url", "error", err)
		httpx.WriteProblem(w, r, httpx.Internal("We could not prepare that download just now."))
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"downloadUrl": url,
		"filename":    filename,
		"expiresIn":   int(a.downloadTTL.Seconds()),
	})
}

/* -------------------------------------------------------------------------- */
/* Export                                                                     */
/* -------------------------------------------------------------------------- */

func (a *API) handleExportPool(w http.ResponseWriter, r *http.Request) {
	companyID, ok := companyOf(w, r)
	if !ok {
		return
	}

	candidates, err := a.store.ExportPoolCandidates(r.Context(), companyID, poolFilterOf(r), poolExportLimit)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition",
		`attachment; filename="candidates-`+time.Now().UTC().Format(time.DateOnly)+`.csv"`)

	writer := csv.NewWriter(w)
	defer writer.Flush()

	_ = writer.Write([]string{
		"ID", "Name", "Email", "Phone", "Headline", "Location", "Source",
		"Current title", "Current employer", "Years", "Skills", "Tags", "Added",
	})

	for _, c := range candidates {
		// Internal notes are deliberately absent: an export is routinely
		// forwarded, and a note written for colleagues should not travel with it.
		_ = writer.Write([]string{
			c.ID, c.FullName, c.Email, c.Phone, c.Headline, c.Location, string(c.Source),
			c.CurrentTitle, c.CurrentEmployer, strconv.Itoa(c.YearsExperience),
			strings.Join(c.Skills, "; "), strings.Join(c.Tags, "; "),
			c.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
}
