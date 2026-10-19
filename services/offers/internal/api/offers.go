package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/karmarat/platform/packages/goshared/httpx"
	"github.com/karmarat/platform/services/offers/internal/domain"
	"github.com/karmarat/platform/services/offers/internal/store"
)

// Limits on the free-text an offer carries. The body cap already bounds the
// request; these bound what a single field can turn into downstream — a decline
// reason is shown in a list, and a custom field becomes a row in a letter.
const (
	maxCustomFields    = 50
	maxCustomFieldLen  = 500
	maxDeclineReasonLn = 1000
)

func (a *API) handleList(w http.ResponseWriter, r *http.Request) {
	companyID, principal, err := tenantOf(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	limit, cursor, err := pagination(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	query := r.URL.Query()
	filter := store.ListFilter{
		Status:        strings.TrimSpace(query.Get("status")),
		ApplicationID: strings.TrimSpace(query.Get("applicationId")),
		CandidateID:   strings.TrimSpace(query.Get("candidateId")),
		Limit:         limit,
		Cursor:        cursor,
	}

	// The status column is an enum, so an unknown value would reach Postgres as
	// an invalid enum literal and surface as a 500. Rejecting it here makes it a
	// 422 that names the field.
	if filter.Status != "" && !domain.Status(filter.Status).Valid() {
		httpx.WriteProblem(w, r, httpx.ValidationFailed(map[string][]string{
			"status": {"Status must be one of: " + strings.Join(domain.Statuses(), ", ") + "."}}))
		return
	}

	page, err := a.store.ListOffers(r.Context(), companyID, filter)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	writeOffers(w, page, principal)
}

func (a *API) handleGet(w http.ResponseWriter, r *http.Request) {
	companyID, principal, err := tenantOf(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	offer, err := a.store.FindOffer(r.Context(), companyID, r.PathValue("id"))
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	writeOffer(w, http.StatusOK, offer, principal)
}

// offerRequest is a new offer, in the shape the company portal's OfferInput
// sends: major units for money, ISO dates for dates.
type offerRequest struct {
	ApplicationID string `json:"applicationId"`
	CandidateID   string `json:"candidateId"`

	// Accepted so a draft renders correctly before the first application event
	// arrives to refresh the snapshot. They are display values, not identity:
	// the applications service remains the source of both.
	CandidateName string `json:"candidateName"`
	JobTitle      string `json:"jobTitle"`

	Designation    string `json:"designation"`
	DepartmentName string `json:"departmentName"`
	GradeLevel     string `json:"gradeLevel"`

	BaseSalary   float64 `json:"baseSalary"`
	Currency     string  `json:"currency"`
	PayFrequency string  `json:"payFrequency"`
	SignOnBonus  float64 `json:"signOnBonus"`
	AnnualBonus  string  `json:"annualBonus"`
	EquityShares string  `json:"equityShares"`

	JoiningDate        string               `json:"joiningDate"`
	ReportingManager   string               `json:"reportingManager"`
	WorkLocation       string               `json:"workLocation"`
	ProbationPeriod    string               `json:"probationPeriod"`
	NoticePeriod       string               `json:"noticePeriod"`
	BenefitsSummary    string               `json:"benefitsSummary"`
	TemplateType       string               `json:"templateType"`
	CustomFields       []domain.CustomField `json:"customFields"`
	OfferLetterContent string               `json:"offerLetterContent"`
	ExpiresAt          *string              `json:"expiresAt"`
}

func (a *API) handleCreate(w http.ResponseWriter, r *http.Request) {
	companyID, principal, err := tenantOf(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	// Drafting a package means setting numbers, so it also requires the ability
	// to see them.
	if err := requireCompensationAccess(principal); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	var req offerRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	input, err := a.offerInput(req)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	offer, err := a.store.CreateOffer(r.Context(), companyID, principal.Subject, input)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	writeOffer(w, http.StatusCreated, offer, principal)
}

// offerInput validates a submitted offer and converts it into what the store
// holds.
//
// Every field error is collected before answering, so a recruiter who mistyped
// three things is told about three things rather than discovering them one
// request at a time.
func (a *API) offerInput(req offerRequest) (store.OfferInput, error) {
	fields := map[string][]string{}

	required := func(field, value string) string {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			fields[field] = append(fields[field], "This field is required.")
		}
		return trimmed
	}

	input := store.OfferInput{
		ApplicationID:      required("applicationId", req.ApplicationID),
		CandidateID:        required("candidateId", req.CandidateID),
		CandidateName:      strings.TrimSpace(req.CandidateName),
		JobTitle:           strings.TrimSpace(req.JobTitle),
		Designation:        required("designation", req.Designation),
		DepartmentName:     required("departmentName", req.DepartmentName),
		GradeLevel:         strings.TrimSpace(req.GradeLevel),
		ReportingManager:   strings.TrimSpace(req.ReportingManager),
		WorkLocation:       strings.TrimSpace(req.WorkLocation),
		ProbationPeriod:    strings.TrimSpace(req.ProbationPeriod),
		NoticePeriod:       strings.TrimSpace(req.NoticePeriod),
		BenefitsSummary:    strings.TrimSpace(req.BenefitsSummary),
		TemplateType:       strings.TrimSpace(req.TemplateType),
		OfferLetterContent: req.OfferLetterContent,
	}

	joiningDate, err := parseJoiningDate(req.JoiningDate)
	collect(fields, err)
	input.JoiningDate = joiningDate

	expiresAt, err := parseExpiry(req.ExpiresAt)
	collect(fields, err)
	input.ExpiresAt = expiresAt

	customFields, err := validateCustomFields(req.CustomFields)
	collect(fields, err)
	input.CustomFields = customFields

	compensation, err := domain.ParseCompensation(domain.CompensationInput{
		Currency:     a.currencyOr(req.Currency),
		BaseSalary:   req.BaseSalary,
		SignOnBonus:  req.SignOnBonus,
		PayFrequency: req.PayFrequency,
		AnnualBonus:  req.AnnualBonus,
		EquityShares: req.EquityShares,
	})
	collect(fields, err)
	input.Compensation = compensation

	if len(fields) > 0 {
		return store.OfferInput{}, httpx.ValidationFailed(fields)
	}
	return input, nil
}

// patchOfferRequest is the subset of an offer a draft may be edited through.
//
// applicationId and candidateId are absent on purpose: they are what the offer
// *is*. Repointing an existing offer at a different candidate would carry its
// approval trail across to a package nobody signed off on.
type patchOfferRequest struct {
	Designation    *string `json:"designation"`
	DepartmentName *string `json:"departmentName"`
	GradeLevel     *string `json:"gradeLevel"`

	BaseSalary   *float64 `json:"baseSalary"`
	Currency     *string  `json:"currency"`
	PayFrequency *string  `json:"payFrequency"`
	SignOnBonus  *float64 `json:"signOnBonus"`
	AnnualBonus  *string  `json:"annualBonus"`
	EquityShares *string  `json:"equityShares"`

	JoiningDate        *string               `json:"joiningDate"`
	ReportingManager   *string               `json:"reportingManager"`
	WorkLocation       *string               `json:"workLocation"`
	ProbationPeriod    *string               `json:"probationPeriod"`
	NoticePeriod       *string               `json:"noticePeriod"`
	BenefitsSummary    *string               `json:"benefitsSummary"`
	TemplateType       *string               `json:"templateType"`
	CustomFields       *[]domain.CustomField `json:"customFields"`
	OfferLetterContent *string               `json:"offerLetterContent"`
	// Raw, because an explicit null means "no expiry" and an absent key means
	// "leave it alone", and a *time.Time cannot tell the two apart.
	ExpiresAt *json.RawMessage `json:"expiresAt"`
}

// touchesCompensation reports whether a patch writes anything the
// `offers.view_compensation` permission protects.
//
// The letter and the custom fields count. They are free text, but they are where
// the numbers are written in prose — the portal composes the letter out of the
// figures, and a custom field is where a relocation allowance ends up — so a
// writer who may not see the package must not be able to rewrite the part of it
// that states the package. Listing only the six structured pointers left exactly
// that hole.
func (r patchOfferRequest) touchesCompensation() bool {
	return r.BaseSalary != nil || r.Currency != nil || r.PayFrequency != nil ||
		r.SignOnBonus != nil || r.AnnualBonus != nil || r.EquityShares != nil ||
		r.OfferLetterContent != nil || r.CustomFields != nil
}

func (a *API) handlePatch(w http.ResponseWriter, r *http.Request) {
	companyID, principal, err := tenantOf(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	var req patchOfferRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}
	if req.touchesCompensation() {
		if err := requireCompensationAccess(principal); err != nil {
			httpx.WriteProblem(w, r, err)
			return
		}
	}

	// The current offer is read first because compensation is replaced as a
	// unit: a caller changing only the base salary keeps the currency the
	// package was priced in. The read is tenant-filtered and the update repeats
	// the filter, so this is a merge, not an ownership check.
	current, err := a.store.FindOffer(r.Context(), companyID, r.PathValue("id"))
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	patch, err := a.offerPatch(req, current)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	offer, err := a.store.UpdateOffer(r.Context(), companyID, current.ID, patch)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	writeOffer(w, http.StatusOK, offer, principal)
}

func (a *API) offerPatch(req patchOfferRequest, current domain.Offer) (store.OfferPatch, error) {
	fields := map[string][]string{}

	patch := store.OfferPatch{
		Designation:        trimmed(req.Designation),
		DepartmentName:     trimmed(req.DepartmentName),
		GradeLevel:         trimmed(req.GradeLevel),
		ReportingManager:   trimmed(req.ReportingManager),
		WorkLocation:       trimmed(req.WorkLocation),
		ProbationPeriod:    trimmed(req.ProbationPeriod),
		NoticePeriod:       trimmed(req.NoticePeriod),
		BenefitsSummary:    trimmed(req.BenefitsSummary),
		TemplateType:       trimmed(req.TemplateType),
		OfferLetterContent: req.OfferLetterContent,
	}

	if req.Designation != nil && *patch.Designation == "" {
		fields["designation"] = append(fields["designation"], "This field is required.")
	}
	if req.DepartmentName != nil && *patch.DepartmentName == "" {
		fields["departmentName"] = append(fields["departmentName"], "This field is required.")
	}

	if req.JoiningDate != nil {
		joiningDate, err := parseJoiningDate(*req.JoiningDate)
		collect(fields, err)
		if err == nil {
			patch.JoiningDate = &joiningDate
		}
	}

	if req.CustomFields != nil {
		customFields, err := validateCustomFields(*req.CustomFields)
		collect(fields, err)
		if err == nil {
			patch.CustomFields = &customFields
		}
	}

	if req.ExpiresAt != nil {
		expiresAt, err := parseRawExpiry(*req.ExpiresAt)
		collect(fields, err)
		if err == nil {
			patch.ExpiresAt = &expiresAt
		}
	}

	if req.touchesCompensation() {
		// Unspecified fields fall back to what the offer already holds, so a
		// partial edit re-validates the whole package rather than producing a
		// combination that was never checked together.
		compensation, err := domain.ParseCompensation(domain.CompensationInput{
			Currency:     valueOr(req.Currency, current.Currency),
			BaseSalary:   floatOr(req.BaseSalary, domain.MinorToMajor(current.BaseSalary, current.Currency)),
			SignOnBonus:  floatOr(req.SignOnBonus, domain.MinorToMajor(current.SignOnBonus, current.Currency)),
			PayFrequency: valueOr(req.PayFrequency, current.PayFrequency),
			AnnualBonus:  valueOr(req.AnnualBonus, current.AnnualBonus),
			EquityShares: valueOr(req.EquityShares, current.EquityShares),
		})
		collect(fields, err)
		if err == nil {
			patch.Compensation = &compensation
		}
	}

	if len(fields) > 0 {
		return store.OfferPatch{}, httpx.ValidationFailed(fields)
	}
	return patch, nil
}

func (a *API) handleSubmit(w http.ResponseWriter, r *http.Request) {
	companyID, principal, err := tenantOf(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	offer, err := a.store.SubmitOffer(r.Context(), companyID, r.PathValue("id"), principal.Subject)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	writeOffer(w, http.StatusOK, offer, principal)
}

type approveRequest struct {
	// SelfApprove is the deliberate override of separation of duties; see
	// domain.CanSelfApprove for when it is honoured and why it exists.
	SelfApprove bool `json:"selfApprove"`
}

func (a *API) handleApprove(w http.ResponseWriter, r *http.Request) {
	companyID, principal, err := tenantOf(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	var req approveRequest
	if err := decodeOptionalJSON(r, &req); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	offer, err := a.store.ApproveOffer(r.Context(), companyID, r.PathValue("id"), principal.Subject, req.SelfApprove)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	if offer.SelfApproved {
		// Logged as well as stored: an approval somebody granted themselves is
		// the event a reviewer goes looking for, and finding it should not
		// require a query against the offers table.
		a.logger.Warn("offer self-approved",
			slog.String("offer_id", offer.ID),
			slog.String("company_id", companyID),
			slog.String("actor_id", principal.Subject))
	}

	writeOffer(w, http.StatusOK, offer, principal)
}

func (a *API) handleSend(w http.ResponseWriter, r *http.Request) {
	companyID, principal, err := tenantOf(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	offer, replayed, err := a.store.SendOffer(
		r.Context(), companyID, r.PathValue("id"), principal.Subject, idempotencyKey(r))
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}
	if replayed {
		a.logger.Info("send replayed from an idempotency key",
			slog.String("offer_id", offer.ID),
			slog.String("company_id", companyID))
	}

	writeOffer(w, http.StatusOK, offer, principal)
}

type respondRequest struct {
	// Outcome is the candidate's answer, recorded by whoever received it.
	Outcome string `json:"outcome"`
	Reason  string `json:"reason"`
}

func (a *API) handleRespond(w http.ResponseWriter, r *http.Request) {
	companyID, principal, err := tenantOf(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	var req respondRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	outcome := domain.Status(strings.ToLower(strings.TrimSpace(req.Outcome)))
	if outcome != domain.StatusAccepted && outcome != domain.StatusDeclined {
		httpx.WriteProblem(w, r, httpx.ValidationFailed(map[string][]string{
			"outcome": {"Outcome must be either accepted or declined."}}))
		return
	}

	reason := strings.TrimSpace(req.Reason)
	if len(reason) > maxDeclineReasonLn {
		httpx.WriteProblem(w, r, httpx.ValidationFailed(map[string][]string{
			"reason": {"A reason may not be longer than 1000 characters."}}))
		return
	}
	if outcome == domain.StatusAccepted {
		// A reason belongs to a decline. Keeping it off an acceptance stops the
		// column from becoming two different things depending on the row.
		reason = ""
	}

	offer, err := a.store.RespondToOffer(r.Context(), companyID, r.PathValue("id"), outcome, reason, principal.Subject)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	writeOffer(w, http.StatusOK, offer, principal)
}

func (a *API) handleDelete(w http.ResponseWriter, r *http.Request) {
	companyID, _, err := tenantOf(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	if err := a.store.DeleteOffer(r.Context(), companyID, r.PathValue("id")); err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	httpx.NoContent(w)
}

/* -------------------------------------------------------------------------- */
/* Request helpers                                                            */
/* -------------------------------------------------------------------------- */

// collect folds a domain validation error into the field map.
//
// Anything that is not a validation error is reported against the request as a
// whole rather than swallowed, so an unexpected failure is still visible without
// naming an internal type.
func collect(fields map[string][]string, err error) {
	if err == nil {
		return
	}
	var validationErr *domain.ValidationError
	if errors.As(err, &validationErr) {
		field := validationErr.Field
		if field == "" {
			field = "request"
		}
		fields[field] = append(fields[field], validationErr.Message)
		return
	}
	fields["request"] = append(fields["request"], "The request could not be processed.")
}

func (a *API) currencyOr(supplied string) string {
	if strings.TrimSpace(supplied) == "" {
		return a.defaultCurrency
	}
	return supplied
}

func parseExpiry(raw *string) (*time.Time, error) {
	if raw == nil {
		return nil, nil
	}
	return validateExpiry(parseTimestamp(*raw, "expiresAt"))
}

func parseRawExpiry(raw json.RawMessage) (*time.Time, error) {
	if string(bytesTrim(raw)) == "null" {
		return nil, nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, domain.Invalid("expiresAt", "Use a date in YYYY-MM-DD form or a full RFC 3339 timestamp.")
	}
	return validateExpiry(parseTimestamp(value, "expiresAt"))
}

// validateExpiry refuses a deadline that has already passed.
//
// An offer created with a past expiry would be swept to "expired" by the next
// tick of the expirer, leaving a recruiter looking at a dead offer they had just
// created and no explanation of why.
func validateExpiry(expiresAt *time.Time, err error) (*time.Time, error) {
	if err != nil || expiresAt == nil {
		return nil, err
	}
	if !expiresAt.After(time.Now()) {
		return nil, domain.Invalid("expiresAt", "An expiry date must be in the future.")
	}
	return expiresAt, nil
}

func validateCustomFields(fields []domain.CustomField) ([]domain.CustomField, error) {
	if len(fields) > maxCustomFields {
		return nil, domain.Invalid("customFields", "An offer may carry at most 50 custom fields.")
	}

	cleaned := make([]domain.CustomField, 0, len(fields))
	for _, field := range fields {
		key := strings.TrimSpace(field.Key)
		if key == "" {
			return nil, domain.Invalid("customFields", "Every custom field needs a name.")
		}
		if len(key) > maxCustomFieldLen || len(field.Value) > maxCustomFieldLen {
			return nil, domain.Invalid("customFields",
				"A custom field's name and value may each be at most 500 characters.")
		}
		cleaned = append(cleaned, domain.CustomField{Key: key, Value: strings.TrimSpace(field.Value)})
	}
	return cleaned, nil
}

func trimmed(value *string) *string {
	if value == nil {
		return nil
	}
	cleaned := strings.TrimSpace(*value)
	return &cleaned
}

func valueOr(supplied *string, fallback string) string {
	if supplied == nil {
		return fallback
	}
	return *supplied
}

func floatOr(supplied *float64, fallback float64) float64 {
	if supplied == nil {
		return fallback
	}
	return *supplied
}

func bytesTrim(raw json.RawMessage) json.RawMessage {
	return json.RawMessage(strings.TrimSpace(string(raw)))
}
