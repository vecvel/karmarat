package api

import (
	"net/http"

	"github.com/karmarat/platform/packages/goshared/httpx"
	"github.com/karmarat/platform/services/messaging/internal/domain"
	"github.com/karmarat/platform/services/messaging/internal/store"
)

// The candidate side of the service.
//
// A candidate is not a tenant: they may be talking to a dozen companies at once,
// so company_id is meaningless as a filter here. The boundary that does mean
// something is their own account id, and it comes from the verified principal in
// every one of these handlers. No path, body or query names a candidate.
//
// There is deliberately no "open a conversation" endpoint. A candidate reaches a
// company by applying to a job; letting them start an arbitrary thread would
// turn every recruiter's inbox into an open mailbox.

func (a *API) handleListMyConversations(w http.ResponseWriter, r *http.Request) {
	accountID, ok := accountOf(w, r)
	if !ok {
		return
	}

	page, err := pageOf(r)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	conversations, err := a.store.ListForCandidate(r.Context(), accountID, page)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	views := make([]conversationView, 0, len(conversations))
	for _, conversation := range conversations {
		views = append(views, candidateView(conversation))
	}

	body := listResponse{Data: views}
	if count := len(conversations); count > 0 {
		last := conversations[count-1]
		body.NextCursor = nextCursor(count, page.Limit, last.LastActivityAt, last.ID)
	}

	httpx.WriteJSON(w, http.StatusOK, body)
}

func (a *API) handleGetMyConversation(w http.ResponseWriter, r *http.Request) {
	accountID, ok := accountOf(w, r)
	if !ok {
		return
	}

	conversation, err := a.store.FindForCandidate(r.Context(), accountID, r.PathValue("id"))
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	httpx.WriteJSON(w, http.StatusOK, candidateView(conversation))
}

func (a *API) handleListMyMessages(w http.ResponseWriter, r *http.Request) {
	accountID, ok := accountOf(w, r)
	if !ok {
		return
	}

	page, err := pageOf(r)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	conversation, err := a.store.FindForCandidate(r.Context(), accountID, r.PathValue("id"))
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	// accountID rather than conversation.CandidateAccountID, so the predicate
	// carries the value the principal proved rather than one read back out of a
	// row this request located.
	messages, err := a.store.ListMessages(r.Context(),
		conversation.ID, conversation.CompanyID, accountID, page)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	views := make([]messageView, 0, len(messages))
	for _, message := range messages {
		views = append(views, candidateMessageView(message))
	}

	body := listResponse{Data: views}
	if count := len(messages); count > 0 {
		last := messages[count-1]
		body.NextCursor = nextCursor(count, page.Limit, last.SentAt, last.ID)
	}

	httpx.WriteJSON(w, http.StatusOK, body)
}

func (a *API) handleSendMyMessage(w http.ResponseWriter, r *http.Request) {
	accountID, ok := accountOf(w, r)
	if !ok {
		return
	}

	var req sendMessageRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	conversation, err := a.store.FindForCandidate(r.Context(), accountID, r.PathValue("id"))
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}
	if conversation.ClosedAt != nil {
		httpx.WriteProblem(w, r, mapError(domain.ErrConversationClosed))
		return
	}

	validated, err := domain.ValidateMessage(req.Body, req.Attachments,
		domain.AttachmentPrefix(domain.SenderCandidate, conversation.CompanyID, accountID))
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	message, replayed, err := a.store.AppendMessage(r.Context(), conversation, store.NewMessage{
		SenderType:      domain.SenderCandidate,
		SenderAccountID: accountID,
		Body:            validated.Body,
		Attachments:     validated.Attachments,
		IdempotencyKey:  idempotencyKey(r),
	})
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	status := http.StatusCreated
	if replayed {
		status = http.StatusOK
	}
	httpx.WriteJSON(w, status, candidateMessageView(message))
}

func (a *API) handleMarkMyRead(w http.ResponseWriter, r *http.Request) {
	accountID, ok := accountOf(w, r)
	if !ok {
		return
	}

	conversation, err := a.store.FindForCandidate(r.Context(), accountID, r.PathValue("id"))
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	// The candidate has no participant row to stamp — the thread's own
	// candidate_account_id is their side of it — so no reader account is passed.
	if _, err := a.store.MarkRead(r.Context(), conversation, domain.SenderCandidate, ""); err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	httpx.NoContent(w)
}
