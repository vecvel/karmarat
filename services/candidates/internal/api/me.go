package api

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/karmarat/platform/packages/goshared/httpx"
	"github.com/karmarat/platform/packages/goshared/idgen"
	"github.com/karmarat/platform/services/candidates/internal/domain"
)

/* -------------------------------------------------------------------------- */
/* Views                                                                      */
/* -------------------------------------------------------------------------- */

type salaryView struct {
	Minor    int64  `json:"minor"`
	Currency string `json:"currency"`
}

type visibilityView struct {
	Discoverable        bool     `json:"discoverable"`
	HideCurrentEmployer bool     `json:"hideCurrentEmployer"`
	HideFromCompanies   []string `json:"hideFromCompanies"`
}

type profileView struct {
	ID        string `json:"id"`
	AccountID string `json:"accountId"`
	Email     string `json:"email"`
	FullName  string `json:"fullName"`

	Headline        string `json:"headline"`
	Summary         string `json:"summary"`
	Location        string `json:"location"`
	YearsExperience int    `json:"yearsExperience"`
	CurrentTitle    string `json:"currentTitle"`
	CurrentEmployer string `json:"currentEmployer"`
	Phone           string `json:"phone"`

	Skills    []string `json:"skills"`
	Languages []string `json:"languages"`

	WebsiteURL  string `json:"websiteUrl"`
	LinkedInURL string `json:"linkedinUrl"`
	GitHubURL   string `json:"githubUrl"`

	DesiredSalary *salaryView `json:"desiredSalary,omitempty"`

	OpenToTypes       []domain.EmploymentType  `json:"openToTypes"`
	OpenToRemote      bool                     `json:"openToRemote"`
	WorkAuthorisation domain.WorkAuthorisation `json:"workAuthorisation"`

	Visibility visibilityView `json:"visibility"`

	Version   int64     `json:"version"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func toProfileView(p domain.Profile) profileView {
	view := profileView{
		ID: p.ID, AccountID: p.AccountID, Email: p.Email, FullName: p.FullName,
		Headline: p.Headline, Summary: p.Summary, Location: p.Location,
		YearsExperience: p.YearsExperience, CurrentTitle: p.CurrentTitle,
		CurrentEmployer: p.CurrentEmployer, Phone: p.Phone,
		Skills: p.Skills, Languages: p.Languages,
		WebsiteURL: p.WebsiteURL, LinkedInURL: p.LinkedInURL, GitHubURL: p.GitHubURL,
		OpenToTypes: p.OpenToTypes, OpenToRemote: p.OpenToRemote,
		WorkAuthorisation: p.WorkAuthorisation,
		Visibility: visibilityView{
			Discoverable:        p.Visibility.Discoverable,
			HideCurrentEmployer: p.Visibility.HideCurrentEmployer,
			HideFromCompanies:   p.Visibility.HideFromCompanies,
		},
		Version: p.Version, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
	}
	if p.DesiredSalaryMinor != nil && p.DesiredSalaryCurrency != "" {
		view.DesiredSalary = &salaryView{Minor: *p.DesiredSalaryMinor, Currency: p.DesiredSalaryCurrency}
	}
	return view
}

/* -------------------------------------------------------------------------- */
/* Profile                                                                    */
/* -------------------------------------------------------------------------- */

func (a *API) handleGetProfile(w http.ResponseWriter, r *http.Request) {
	accountID, ok := accountOf(w, r)
	if !ok {
		return
	}

	profile, err := a.store.FindProfileByAccount(r.Context(), accountID)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	httpx.WriteJSON(w, http.StatusOK, toProfileView(profile))
}

type updateProfileRequest struct {
	Headline        *string `json:"headline"`
	Summary         *string `json:"summary"`
	Location        *string `json:"location"`
	YearsExperience *int    `json:"yearsExperience"`
	CurrentTitle    *string `json:"currentTitle"`
	CurrentEmployer *string `json:"currentEmployer"`
	Phone           *string `json:"phone"`

	Skills    *[]string `json:"skills"`
	Languages *[]string `json:"languages"`

	WebsiteURL  *string `json:"websiteUrl"`
	LinkedInURL *string `json:"linkedinUrl"`
	GitHubURL   *string `json:"githubUrl"`

	DesiredSalaryMinor    *int64  `json:"desiredSalaryMinor"`
	DesiredSalaryCurrency *string `json:"desiredSalaryCurrency"`

	OpenToTypes       *[]domain.EmploymentType  `json:"openToTypes"`
	OpenToRemote      *bool                     `json:"openToRemote"`
	WorkAuthorisation *domain.WorkAuthorisation `json:"workAuthorisation"`
}

func (a *API) handleUpdateProfile(w http.ResponseWriter, r *http.Request) {
	accountID, ok := accountOf(w, r)
	if !ok {
		return
	}

	var req updateProfileRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	input := domain.ProfileInput{
		Headline: req.Headline, Summary: req.Summary, Location: req.Location,
		YearsExperience: req.YearsExperience, CurrentTitle: req.CurrentTitle,
		CurrentEmployer: req.CurrentEmployer, Phone: req.Phone,
		Skills: req.Skills, Languages: req.Languages,
		WebsiteURL: req.WebsiteURL, LinkedInURL: req.LinkedInURL, GitHubURL: req.GitHubURL,
		DesiredSalaryMinor: req.DesiredSalaryMinor, DesiredSalaryCurrency: req.DesiredSalaryCurrency,
		OpenToTypes: req.OpenToTypes, OpenToRemote: req.OpenToRemote,
		WorkAuthorisation: req.WorkAuthorisation,
	}
	if err := input.Validate(); err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	profile, err := a.store.UpdateProfile(r.Context(), accountID, input)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	a.publisher.ProfileUpdated(r.Context(), profile, false)
	httpx.WriteJSON(w, http.StatusOK, toProfileView(profile))
}

type visibilityRequest struct {
	Discoverable        bool     `json:"discoverable"`
	HideCurrentEmployer bool     `json:"hideCurrentEmployer"`
	HideFromCompanies   []string `json:"hideFromCompanies"`
}

// handleUpdateVisibility is a PUT rather than a PATCH: discoverability and its
// exceptions are one decision, and a partial update of a block list is how a
// company ends up visible to a candidate who thought they had excluded it.
func (a *API) handleUpdateVisibility(w http.ResponseWriter, r *http.Request) {
	accountID, ok := accountOf(w, r)
	if !ok {
		return
	}

	var req visibilityRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	visibility := domain.Visibility{
		Discoverable:        req.Discoverable,
		HideCurrentEmployer: req.HideCurrentEmployer,
		HideFromCompanies:   normaliseIDs(req.HideFromCompanies),
	}
	if err := visibility.Validate(); err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	profile, err := a.store.UpdateVisibility(r.Context(), accountID, visibility)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	a.publisher.VisibilityChanged(r.Context(), profile)
	httpx.WriteJSON(w, http.StatusOK, toProfileView(profile))
}

func (a *API) handleDeleteProfile(w http.ResponseWriter, r *http.Request) {
	accountID, ok := accountOf(w, r)
	if !ok {
		return
	}

	profile, err := a.store.SoftDeleteProfile(r.Context(), accountID)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	// Other services hold projections of this candidate; the event is how their
	// copies stop being shown, since nothing here can reach into their tables.
	a.publisher.ProfileUpdated(r.Context(), profile, true)
	a.publisher.VisibilityChanged(r.Context(), profile)
	httpx.NoContent(w)
}

/* -------------------------------------------------------------------------- */
/* Work experience                                                            */
/* -------------------------------------------------------------------------- */

type experienceRequest struct {
	Title          string                 `json:"title"`
	Employer       string                 `json:"employer"`
	Location       string                 `json:"location"`
	EmploymentType *domain.EmploymentType `json:"employmentType"`
	Description    string                 `json:"description"`
	StartedOn      string                 `json:"startedOn"`
	EndedOn        string                 `json:"endedOn"`
	IsCurrent      bool                   `json:"isCurrent"`
}

type experienceView struct {
	ID             string                 `json:"id"`
	Title          string                 `json:"title"`
	Employer       string                 `json:"employer"`
	Location       string                 `json:"location"`
	EmploymentType *domain.EmploymentType `json:"employmentType,omitempty"`
	Description    string                 `json:"description"`
	StartedOn      string                 `json:"startedOn"`
	EndedOn        string                 `json:"endedOn,omitempty"`
	IsCurrent      bool                   `json:"isCurrent"`
	CreatedAt      time.Time              `json:"createdAt"`
	UpdatedAt      time.Time              `json:"updatedAt"`
}

func toExperienceView(e domain.Experience) experienceView {
	view := experienceView{
		ID: e.ID, Title: e.Title, Employer: e.Employer, Location: e.Location,
		EmploymentType: e.EmploymentType, Description: e.Description,
		StartedOn: e.StartedOn.Format("2006-01-02"), IsCurrent: e.IsCurrent,
		CreatedAt: e.CreatedAt, UpdatedAt: e.UpdatedAt,
	}
	if e.EndedOn != nil {
		view.EndedOn = e.EndedOn.Format("2006-01-02")
	}
	return view
}

// experienceFrom builds a validated entry from a request body.
func experienceFrom(req experienceRequest) (domain.Experience, error) {
	started, err := parseDate("startedOn", req.StartedOn, true)
	if err != nil {
		return domain.Experience{}, err
	}
	ended, err := parseDate("endedOn", req.EndedOn, false)
	if err != nil {
		return domain.Experience{}, err
	}

	entry := domain.Experience{
		Title: strings.TrimSpace(req.Title), Employer: strings.TrimSpace(req.Employer),
		Location: strings.TrimSpace(req.Location), EmploymentType: req.EmploymentType,
		Description: req.Description, StartedOn: *started, EndedOn: ended, IsCurrent: req.IsCurrent,
	}
	if err := entry.Validate(); err != nil {
		return domain.Experience{}, err
	}
	return entry, nil
}

func (a *API) handleCreateExperience(w http.ResponseWriter, r *http.Request) {
	accountID, ok := accountOf(w, r)
	if !ok {
		return
	}

	var req experienceRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	entry, err := experienceFrom(req)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	profile, err := a.store.FindProfileByAccount(r.Context(), accountID)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}
	entry.CandidateID, entry.AccountID = profile.ID, accountID

	created, err := a.store.CreateExperience(r.Context(), entry)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	httpx.WriteJSON(w, http.StatusCreated, toExperienceView(created))
}

func (a *API) handleListExperience(w http.ResponseWriter, r *http.Request) {
	accountID, ok := accountOf(w, r)
	if !ok {
		return
	}

	page, err := pageOf(r)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	entries, err := a.store.ListExperience(r.Context(), accountID, page)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	views := make([]experienceView, 0, len(entries))
	for _, entry := range entries {
		views = append(views, toExperienceView(entry))
	}

	response := listResponse{Data: views}
	if len(entries) > 0 {
		last := entries[len(entries)-1]
		response.NextCursor = nextCursor(len(entries), page.Limit, last.StartedOn, last.ID)
	}
	httpx.WriteJSON(w, http.StatusOK, response)
}

func (a *API) handleUpdateExperience(w http.ResponseWriter, r *http.Request) {
	accountID, ok := accountOf(w, r)
	if !ok {
		return
	}

	var req experienceRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	entry, err := experienceFrom(req)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}
	entry.ID = r.PathValue("id")

	updated, err := a.store.UpdateExperience(r.Context(), accountID, entry)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	httpx.WriteJSON(w, http.StatusOK, toExperienceView(updated))
}

func (a *API) handleDeleteExperience(w http.ResponseWriter, r *http.Request) {
	accountID, ok := accountOf(w, r)
	if !ok {
		return
	}

	if err := a.store.DeleteExperience(r.Context(), accountID, r.PathValue("id")); err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}
	httpx.NoContent(w)
}

/* -------------------------------------------------------------------------- */
/* Education                                                                  */
/* -------------------------------------------------------------------------- */

type educationRequest struct {
	Institution   string `json:"institution"`
	Qualification string `json:"qualification"`
	FieldOfStudy  string `json:"fieldOfStudy"`
	Grade         string `json:"grade"`
	StartedOn     string `json:"startedOn"`
	EndedOn       string `json:"endedOn"`
}

type educationView struct {
	ID            string    `json:"id"`
	Institution   string    `json:"institution"`
	Qualification string    `json:"qualification"`
	FieldOfStudy  string    `json:"fieldOfStudy"`
	Grade         string    `json:"grade"`
	StartedOn     string    `json:"startedOn,omitempty"`
	EndedOn       string    `json:"endedOn,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

func toEducationView(e domain.Education) educationView {
	view := educationView{
		ID: e.ID, Institution: e.Institution, Qualification: e.Qualification,
		FieldOfStudy: e.FieldOfStudy, Grade: e.Grade,
		CreatedAt: e.CreatedAt, UpdatedAt: e.UpdatedAt,
	}
	if e.StartedOn != nil {
		view.StartedOn = e.StartedOn.Format("2006-01-02")
	}
	if e.EndedOn != nil {
		view.EndedOn = e.EndedOn.Format("2006-01-02")
	}
	return view
}

func educationFrom(req educationRequest) (domain.Education, error) {
	started, err := parseDate("startedOn", req.StartedOn, false)
	if err != nil {
		return domain.Education{}, err
	}
	ended, err := parseDate("endedOn", req.EndedOn, false)
	if err != nil {
		return domain.Education{}, err
	}

	entry := domain.Education{
		Institution:   strings.TrimSpace(req.Institution),
		Qualification: strings.TrimSpace(req.Qualification),
		FieldOfStudy:  strings.TrimSpace(req.FieldOfStudy),
		Grade:         strings.TrimSpace(req.Grade),
		StartedOn:     started, EndedOn: ended,
	}
	if err := entry.Validate(); err != nil {
		return domain.Education{}, err
	}
	return entry, nil
}

func (a *API) handleCreateEducation(w http.ResponseWriter, r *http.Request) {
	accountID, ok := accountOf(w, r)
	if !ok {
		return
	}

	var req educationRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	entry, err := educationFrom(req)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	profile, err := a.store.FindProfileByAccount(r.Context(), accountID)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}
	entry.CandidateID, entry.AccountID = profile.ID, accountID

	created, err := a.store.CreateEducation(r.Context(), entry)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	httpx.WriteJSON(w, http.StatusCreated, toEducationView(created))
}

func (a *API) handleListEducation(w http.ResponseWriter, r *http.Request) {
	accountID, ok := accountOf(w, r)
	if !ok {
		return
	}

	page, err := pageOf(r)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	entries, err := a.store.ListEducation(r.Context(), accountID, page)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	views := make([]educationView, 0, len(entries))
	for _, entry := range entries {
		views = append(views, toEducationView(entry))
	}

	response := listResponse{Data: views}
	if len(entries) > 0 {
		last := entries[len(entries)-1]
		response.NextCursor = nextCursor(len(entries), page.Limit, last.CreatedAt, last.ID)
	}
	httpx.WriteJSON(w, http.StatusOK, response)
}

func (a *API) handleUpdateEducation(w http.ResponseWriter, r *http.Request) {
	accountID, ok := accountOf(w, r)
	if !ok {
		return
	}

	var req educationRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	entry, err := educationFrom(req)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}
	entry.ID = r.PathValue("id")

	updated, err := a.store.UpdateEducation(r.Context(), accountID, entry)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	httpx.WriteJSON(w, http.StatusOK, toEducationView(updated))
}

func (a *API) handleDeleteEducation(w http.ResponseWriter, r *http.Request) {
	accountID, ok := accountOf(w, r)
	if !ok {
		return
	}

	if err := a.store.DeleteEducation(r.Context(), accountID, r.PathValue("id")); err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}
	httpx.NoContent(w)
}

/* -------------------------------------------------------------------------- */
/* Certifications                                                             */
/* -------------------------------------------------------------------------- */

type certificationRequest struct {
	Name          string `json:"name"`
	Issuer        string `json:"issuer"`
	CredentialID  string `json:"credentialId"`
	CredentialURL string `json:"credentialUrl"`
	IssuedOn      string `json:"issuedOn"`
	ExpiresOn     string `json:"expiresOn"`
}

type certificationView struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Issuer        string    `json:"issuer"`
	CredentialID  string    `json:"credentialId"`
	CredentialURL string    `json:"credentialUrl"`
	IssuedOn      string    `json:"issuedOn,omitempty"`
	ExpiresOn     string    `json:"expiresOn,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

func toCertificationView(c domain.Certification) certificationView {
	view := certificationView{
		ID: c.ID, Name: c.Name, Issuer: c.Issuer, CredentialID: c.CredentialID,
		CredentialURL: c.CredentialURL, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
	}
	if c.IssuedOn != nil {
		view.IssuedOn = c.IssuedOn.Format("2006-01-02")
	}
	if c.ExpiresOn != nil {
		view.ExpiresOn = c.ExpiresOn.Format("2006-01-02")
	}
	return view
}

func certificationFrom(req certificationRequest) (domain.Certification, error) {
	issued, err := parseDate("issuedOn", req.IssuedOn, false)
	if err != nil {
		return domain.Certification{}, err
	}
	expires, err := parseDate("expiresOn", req.ExpiresOn, false)
	if err != nil {
		return domain.Certification{}, err
	}

	entry := domain.Certification{
		Name: strings.TrimSpace(req.Name), Issuer: strings.TrimSpace(req.Issuer),
		CredentialID:  strings.TrimSpace(req.CredentialID),
		CredentialURL: strings.TrimSpace(req.CredentialURL),
		IssuedOn:      issued, ExpiresOn: expires,
	}
	if err := entry.Validate(); err != nil {
		return domain.Certification{}, err
	}
	return entry, nil
}

func (a *API) handleCreateCertification(w http.ResponseWriter, r *http.Request) {
	accountID, ok := accountOf(w, r)
	if !ok {
		return
	}

	var req certificationRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	entry, err := certificationFrom(req)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	profile, err := a.store.FindProfileByAccount(r.Context(), accountID)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}
	entry.CandidateID, entry.AccountID = profile.ID, accountID

	created, err := a.store.CreateCertification(r.Context(), entry)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	httpx.WriteJSON(w, http.StatusCreated, toCertificationView(created))
}

func (a *API) handleListCertifications(w http.ResponseWriter, r *http.Request) {
	accountID, ok := accountOf(w, r)
	if !ok {
		return
	}

	page, err := pageOf(r)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	entries, err := a.store.ListCertifications(r.Context(), accountID, page)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	views := make([]certificationView, 0, len(entries))
	for _, entry := range entries {
		views = append(views, toCertificationView(entry))
	}

	response := listResponse{Data: views}
	if len(entries) > 0 {
		last := entries[len(entries)-1]
		response.NextCursor = nextCursor(len(entries), page.Limit, last.CreatedAt, last.ID)
	}
	httpx.WriteJSON(w, http.StatusOK, response)
}

func (a *API) handleUpdateCertification(w http.ResponseWriter, r *http.Request) {
	accountID, ok := accountOf(w, r)
	if !ok {
		return
	}

	var req certificationRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	entry, err := certificationFrom(req)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}
	entry.ID = r.PathValue("id")

	updated, err := a.store.UpdateCertification(r.Context(), accountID, entry)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	httpx.WriteJSON(w, http.StatusOK, toCertificationView(updated))
}

func (a *API) handleDeleteCertification(w http.ResponseWriter, r *http.Request) {
	accountID, ok := accountOf(w, r)
	if !ok {
		return
	}

	if err := a.store.DeleteCertification(r.Context(), accountID, r.PathValue("id")); err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}
	httpx.NoContent(w)
}

/* -------------------------------------------------------------------------- */
/* Resumes                                                                    */
/* -------------------------------------------------------------------------- */

type resumeView struct {
	ID          string    `json:"id"`
	Filename    string    `json:"filename"`
	ContentType string    `json:"contentType"`
	SizeBytes   int64     `json:"sizeBytes"`
	IsPrimary   bool      `json:"isPrimary"`
	CreatedAt   time.Time `json:"createdAt"`
}

func toResumeView(r domain.Resume) resumeView {
	return resumeView{
		ID: r.ID, Filename: r.Filename, ContentType: r.ContentType,
		SizeBytes: r.SizeBytes, IsPrimary: r.IsPrimary, CreatedAt: r.CreatedAt,
	}
}

type uploadURLRequest struct {
	Filename    string `json:"filename"`
	ContentType string `json:"contentType"`
	SizeBytes   int64  `json:"sizeBytes"`
}

type uploadURLResponse struct {
	ResumeID  string     `json:"resumeId"`
	UploadURL string     `json:"uploadUrl"`
	Method    string     `json:"method"`
	Headers   headerHint `json:"headers"`
	ExpiresAt time.Time  `json:"expiresAt"`
	Resume    resumeView `json:"resume"`
}

// headerHint tells the browser exactly what to send, because the signature
// covers Content-Type and a mismatch is rejected by the bucket, not by us.
type headerHint struct {
	ContentType string `json:"Content-Type"`
}

// handleResumeUploadURL signs a single-object PUT.
//
// The record is written before the bytes arrive: an object with no row would be
// invisible and unreclaimable, while a row with no object is a resume the
// candidate can see failed and replace.
func (a *API) handleResumeUploadURL(w http.ResponseWriter, r *http.Request) {
	accountID, ok := accountOf(w, r)
	if !ok {
		return
	}

	var req uploadURLRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	contentType := strings.ToLower(strings.TrimSpace(req.ContentType))
	if err := domain.ValidateResumeUpload(req.Filename, contentType, req.SizeBytes); err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	profile, err := a.store.FindProfileByAccount(r.Context(), accountID)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	resume := domain.Resume{
		ID:          idgen.New("res"),
		CandidateID: profile.ID,
		AccountID:   accountID,
		Filename:    strings.TrimSpace(req.Filename),
		ContentType: contentType,
		SizeBytes:   req.SizeBytes,
	}
	// The key is derived from the account, never from the request, so one
	// candidate cannot be handed a URL that writes into another's prefix.
	resume.ObjectKey = domain.ResumeObjectKey(accountID, resume.ID, contentType)

	url, err := a.presigner.PresignPut(resume.ObjectKey, contentType, a.uploadTTL)
	if err != nil {
		httpx.WriteProblem(w, r, httpx.Internal("Upload is temporarily unavailable."))
		a.logger.Error("failed to sign resume upload", slog.Any("error", err))
		return
	}

	created, err := a.store.CreateResume(r.Context(), resume)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	httpx.WriteJSON(w, http.StatusCreated, uploadURLResponse{
		ResumeID:  created.ID,
		UploadURL: url,
		Method:    http.MethodPut,
		Headers:   headerHint{ContentType: contentType},
		ExpiresAt: time.Now().UTC().Add(a.uploadTTL),
		Resume:    toResumeView(created),
	})
}

func (a *API) handleListResumes(w http.ResponseWriter, r *http.Request) {
	accountID, ok := accountOf(w, r)
	if !ok {
		return
	}

	page, err := pageOf(r)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	resumes, err := a.store.ListResumes(r.Context(), accountID, page)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	views := make([]resumeView, 0, len(resumes))
	for _, resume := range resumes {
		views = append(views, toResumeView(resume))
	}

	response := listResponse{Data: views}
	if len(resumes) > 0 {
		last := resumes[len(resumes)-1]
		response.NextCursor = nextCursor(len(resumes), page.Limit, last.CreatedAt, last.ID)
	}
	httpx.WriteJSON(w, http.StatusOK, response)
}

func (a *API) handleDeleteResume(w http.ResponseWriter, r *http.Request) {
	accountID, ok := accountOf(w, r)
	if !ok {
		return
	}

	deleted, err := a.store.DeleteResume(r.Context(), accountID, r.PathValue("id"))
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	// The stored object outlives its row until the bucket's lifecycle rule
	// collects it; this service does not hold a credential that can delete.
	a.logger.Info("resume record removed",
		slog.String("resume_id", deleted.ID),
		slog.String("object_key", deleted.ObjectKey))

	httpx.NoContent(w)
}

func (a *API) handleSetPrimaryResume(w http.ResponseWriter, r *http.Request) {
	accountID, ok := accountOf(w, r)
	if !ok {
		return
	}

	primary, err := a.store.SetPrimaryResume(r.Context(), accountID, r.PathValue("id"))
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	httpx.WriteJSON(w, http.StatusOK, toResumeView(primary))
}

// normaliseIDs trims and drops blanks from a client-supplied id list.
func normaliseIDs(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))

	for _, value := range values {
		trimmed := strings.ToLower(strings.TrimSpace(value))
		if trimmed == "" {
			continue
		}
		if _, duplicate := seen[trimmed]; duplicate {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}
