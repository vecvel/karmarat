package api

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/karmarat/platform/packages/goshared/httpx"
	"github.com/karmarat/platform/packages/goshared/tenancy"
	"github.com/karmarat/platform/services/messaging/internal/domain"
	"github.com/karmarat/platform/services/messaging/internal/store"
)

/* -------------------------------------------------------------------------- */
/* Reading                                                                    */
/* -------------------------------------------------------------------------- */

func (a *API) handleListConversations(w http.ResponseWriter, r *http.Request) {
	scope, ok := scopeOf(w, r)
	if !ok {
		return
	}

	page, err := pageOf(r)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	// The read/read_all distinction is inside this query, not applied to its
	// result: a recruiter without the wide permission never has a colleague's
	// thread in the result set to begin with.
	conversations, err := a.store.ListForCompany(r.Context(), scope, page)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	views := make([]conversationView, 0, len(conversations))
	for _, conversation := range conversations {
		views = append(views, companyView(conversation))
	}

	body := listResponse{Data: views}
	if count := len(conversations); count > 0 {
		last := conversations[count-1]
		body.NextCursor = nextCursor(count, page.Limit, last.LastActivityAt, last.ID)
	}

	httpx.WriteJSON(w, http.StatusOK, body)
}

func (a *API) handleGetConversation(w http.ResponseWriter, r *http.Request) {
	scope, ok := scopeOf(w, r)
	if !ok {
		return
	}

	conversation, err := a.store.FindForCompany(r.Context(), scope, r.PathValue("id"))
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	httpx.WriteJSON(w, http.StatusOK, companyView(conversation))
}

func (a *API) handleListMessages(w http.ResponseWriter, r *http.Request) {
	scope, ok := scopeOf(w, r)
	if !ok {
		return
	}

	page, err := pageOf(r)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	// Resolving the thread inside the caller's scope first is what scopes the
	// messages: an id on its own would otherwise read a colleague's — or another
	// tenant's — correspondence.
	conversation, err := a.store.FindForCompany(r.Context(), scope, r.PathValue("id"))
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	messages, err := a.store.ListMessages(r.Context(),
		conversation.ID, conversation.CompanyID, conversation.CandidateAccountID, page)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	views := make([]messageView, 0, len(messages))
	for _, message := range messages {
		views = append(views, companyMessageView(message))
	}

	body := listResponse{Data: views}
	if count := len(messages); count > 0 {
		last := messages[count-1]
		body.NextCursor = nextCursor(count, page.Limit, last.SentAt, last.ID)
	}

	httpx.WriteJSON(w, http.StatusOK, body)
}

/* -------------------------------------------------------------------------- */
/* Opening                                                                    */
/* -------------------------------------------------------------------------- */

type openConversationRequest struct {
	CandidateAccountID string              `json:"candidateAccountId"`
	ApplicationID      string              `json:"applicationId"`
	JobID              string              `json:"jobId"`
	Subject            string              `json:"subject"`
	Body               string              `json:"body"`
	Attachments        []domain.Attachment `json:"attachments"`
}

type openConversationResponse struct {
	Conversation conversationView `json:"conversation"`
	// Message is present only when the request carried a first message.
	Message *messageView `json:"message,omitempty"`
}

// handleOpenConversation starts a thread with a candidate.
//
// Three gates stand between a recruiter and a stranger's inbox, and they run in
// this order because each is more expensive than the last: the request has to be
// well formed, the candidate has to be reachable by this company, and the
// company has to be under its daily cap. The cap is checked after eligibility so
// a company cannot burn its own allowance discovering which candidates have
// blocked it.
func (a *API) handleOpenConversation(w http.ResponseWriter, r *http.Request) {
	scope, ok := scopeOf(w, r)
	if !ok {
		return
	}
	principal := tenancy.MustFromContext(r.Context())

	var req openConversationRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	failures := map[string][]string{}
	if !domain.ValidAccountID(req.CandidateAccountID) {
		failures["candidateAccountId"] = []string{"A candidate account is required."}
	}
	if !validRef(req.ApplicationID) {
		failures["applicationId"] = []string{"That application reference is not valid."}
	}
	if !validRef(req.JobID) {
		failures["jobId"] = []string{"That job reference is not valid."}
	}
	if len(failures) > 0 {
		httpx.WriteProblem(w, r, httpx.ValidationFailed(failures))
		return
	}

	subject, err := domain.ValidateSubject(req.Subject)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	// A first message is optional, but when one is supplied it is validated
	// before anything is written, so a rejected body cannot leave an empty thread
	// behind that still counted against the daily cap.
	var first *store.NewMessage
	if req.Body != "" || len(req.Attachments) > 0 {
		validated, err := domain.ValidateMessage(req.Body, req.Attachments,
			domain.AttachmentPrefix(domain.SenderCompany, scope.CompanyID, principal.Subject))
		if err != nil {
			httpx.WriteProblem(w, r, mapError(err))
			return
		}
		first = &store.NewMessage{
			SenderType:      domain.SenderCompany,
			SenderAccountID: principal.Subject,
			Body:            validated.Body,
			Attachments:     validated.Attachments,
			IdempotencyKey:  idempotencyKey(r),
		}
	}

	applied, err := a.mayOpenConversation(r.Context(), scope.CompanyID, req.CandidateAccountID)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	// The origin is derived from what is true, never from what was asked for. A
	// caller that named an application id for a candidate who never applied
	// would otherwise label its outreach "application" and step around the cap.
	origin := domain.OriginRecruiter
	if applied && req.ApplicationID != "" {
		origin = domain.OriginApplication
	}
	if origin.CountsTowardDailyLimit() {
		if err := a.checkDailyOpenLimit(r.Context(), scope.CompanyID); err != nil {
			httpx.WriteProblem(w, r, mapError(err))
			return
		}
	}

	conversation, message, err := a.store.OpenConversation(r.Context(), store.NewConversation{
		CompanyID:          scope.CompanyID,
		CandidateAccountID: req.CandidateAccountID,
		ApplicationID:      req.ApplicationID,
		JobID:              req.JobID,
		Subject:            subject,
		Origin:             origin,
		OpenedByAccountID:  principal.Subject,
		FirstMessage:       first,
	})
	if err != nil {
		if errors.Is(err, domain.ErrConversationExists) {
			a.writeExistingConflict(w, r, scope.CompanyID, req.CandidateAccountID, req.ApplicationID)
			return
		}
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	response := openConversationResponse{Conversation: companyView(conversation)}
	if first != nil {
		view := companyMessageView(message)
		response.Message = &view
	}

	httpx.WriteJSON(w, http.StatusCreated, response)
}

// writeExistingConflict answers a duplicate open by naming the thread that
// already exists, so a client can navigate to it instead of retrying forever.
func (a *API) writeExistingConflict(
	w http.ResponseWriter, r *http.Request, companyID, candidateAccountID, applicationID string,
) {
	existing, err := a.store.FindExisting(r.Context(), companyID, candidateAccountID, applicationID)
	if err != nil {
		// The conflicting row could not be read back — the generic conflict is
		// still the honest answer.
		httpx.WriteProblem(w, r, mapError(domain.ErrConversationExists))
		return
	}

	httpx.WriteProblem(w, r, httpx.Conflict("conversation_exists",
		fmt.Sprintf("A conversation with this candidate is already open (%s).", existing.ID)))
}

/* -------------------------------------------------------------------------- */
/* Writing                                                                    */
/* -------------------------------------------------------------------------- */

type sendMessageRequest struct {
	Body        string              `json:"body"`
	Attachments []domain.Attachment `json:"attachments"`
}

func (a *API) handleSendMessage(w http.ResponseWriter, r *http.Request) {
	scope, ok := scopeOf(w, r)
	if !ok {
		return
	}
	principal := tenancy.MustFromContext(r.Context())

	var req sendMessageRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	conversation, err := a.store.FindForCompany(r.Context(), scope, r.PathValue("id"))
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}
	if conversation.ClosedAt != nil {
		httpx.WriteProblem(w, r, mapError(domain.ErrConversationClosed))
		return
	}

	validated, err := domain.ValidateMessage(req.Body, req.Attachments,
		domain.AttachmentPrefix(domain.SenderCompany, conversation.CompanyID, principal.Subject))
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	message, replayed, err := a.store.AppendMessage(r.Context(), conversation, store.NewMessage{
		SenderType:      domain.SenderCompany,
		SenderAccountID: principal.Subject,
		Body:            validated.Body,
		Attachments:     validated.Attachments,
		IdempotencyKey:  idempotencyKey(r),
	})
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	// Writing into a thread joins it. Without this a recruiter who reached it
	// through `messaging.read_all` would reply once and then lose sight of the
	// answer, because the narrow read they use day to day goes through the
	// participant join. The message is already committed, so a failure here is
	// logged rather than turned into an error the caller cannot act on.
	if !replayed {
		if err := a.store.AddParticipant(r.Context(), nil,
			conversation.ID, conversation.CompanyID, principal.Subject); err != nil {
			a.logger.Warn("could not record the sender as a conversation participant",
				slog.String("conversation_id", conversation.ID),
				slog.Any("error", err))
		}
	}

	// A replay is not a creation: a client retrying after a timeout gets 200 and
	// the message it already sent, not a second 201 suggesting a duplicate.
	status := http.StatusCreated
	if replayed {
		status = http.StatusOK
	}
	httpx.WriteJSON(w, status, companyMessageView(message))
}

func (a *API) handleMarkRead(w http.ResponseWriter, r *http.Request) {
	scope, ok := scopeOf(w, r)
	if !ok {
		return
	}
	principal := tenancy.MustFromContext(r.Context())

	conversation, err := a.store.FindForCompany(r.Context(), scope, r.PathValue("id"))
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	if _, err := a.store.MarkRead(r.Context(), conversation, domain.SenderCompany, principal.Subject); err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	httpx.NoContent(w)
}

/* -------------------------------------------------------------------------- */
/* Helpers                                                                    */
/* -------------------------------------------------------------------------- */

// validRef bounds an optional reference to another service's record.
//
// Applications and jobs live elsewhere, so their ids are opaque here; all this
// service can usefully say is that the value has the shape of a platform
// identifier before it reaches a query.
func validRef(value string) bool {
	return value == "" || domain.ValidAccountID(value)
}
