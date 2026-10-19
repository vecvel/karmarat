package api

import (
	"time"

	"github.com/karmarat/platform/services/messaging/internal/domain"
)

// The response types are declared once and built through side-specific
// constructors. A recruiter and a candidate see the same thread from opposite
// ends, and the unread count each is shown is their own — handing a candidate
// the recruiter's counter would leak whether their message had been opened.

type conversationView struct {
	ID                 string     `json:"id"`
	CandidateAccountID string     `json:"candidateAccountId,omitempty"`
	CompanyID          string     `json:"companyId,omitempty"`
	ApplicationID      string     `json:"applicationId,omitempty"`
	JobID              string     `json:"jobId,omitempty"`
	Subject            string     `json:"subject,omitempty"`
	Origin             string     `json:"origin"`
	OpenedByAccountID  string     `json:"openedByAccountId,omitempty"`
	LastActivityAt     time.Time  `json:"lastActivityAt"`
	LastMessagePreview string     `json:"lastMessagePreview,omitempty"`
	LastMessageSender  string     `json:"lastMessageSender,omitempty"`
	UnreadCount        int        `json:"unreadCount"`
	ClosedAt           *time.Time `json:"closedAt,omitempty"`
	CreatedAt          time.Time  `json:"createdAt"`
	UpdatedAt          time.Time  `json:"updatedAt"`
}

// companyView renders a thread for a recruiter.
func companyView(c domain.Conversation) conversationView {
	return conversationView{
		ID:                 c.ID,
		CandidateAccountID: c.CandidateAccountID,
		ApplicationID:      c.ApplicationID,
		JobID:              c.JobID,
		Subject:            c.Subject,
		Origin:             string(c.Origin),
		OpenedByAccountID:  c.OpenedByAccountID,
		LastActivityAt:     c.LastActivityAt,
		LastMessagePreview: c.LastMessagePreview,
		LastMessageSender:  string(c.LastMessageSender),
		UnreadCount:        c.CompanyUnread,
		ClosedAt:           c.ClosedAt,
		CreatedAt:          c.CreatedAt,
		UpdatedAt:          c.UpdatedAt,
	}
}

// candidateView renders a thread for the candidate.
//
// It carries companyId — the candidate is entitled to know who is writing to
// them — but not the recruiter's account id or the company's unread counter,
// which are the company's internal business.
func candidateView(c domain.Conversation) conversationView {
	return conversationView{
		ID:                 c.ID,
		CompanyID:          c.CompanyID,
		ApplicationID:      c.ApplicationID,
		JobID:              c.JobID,
		Subject:            c.Subject,
		Origin:             string(c.Origin),
		LastActivityAt:     c.LastActivityAt,
		LastMessagePreview: c.LastMessagePreview,
		LastMessageSender:  string(c.LastMessageSender),
		UnreadCount:        c.CandidateUnread,
		ClosedAt:           c.ClosedAt,
		CreatedAt:          c.CreatedAt,
		UpdatedAt:          c.UpdatedAt,
	}
}

type messageView struct {
	ID             string `json:"id"`
	ConversationID string `json:"conversationId"`
	SenderType     string `json:"senderType"`
	// SenderAccountID is present only on the company side. A candidate is told
	// which company wrote to them, not which individual recruiter — the roster
	// of who works on their file is not theirs to enumerate.
	SenderAccountID string              `json:"senderAccountId,omitempty"`
	Body            string              `json:"body"`
	Attachments     []domain.Attachment `json:"attachments"`
	SentAt          time.Time           `json:"sentAt"`
	ReadAt          *time.Time          `json:"readAt,omitempty"`
}

func companyMessageView(m domain.Message) messageView {
	return messageView{
		ID:              m.ID,
		ConversationID:  m.ConversationID,
		SenderType:      string(m.SenderType),
		SenderAccountID: m.SenderAccountID,
		Body:            m.Body,
		Attachments:     m.Attachments,
		SentAt:          m.SentAt,
		ReadAt:          m.ReadAt,
	}
}

func candidateMessageView(m domain.Message) messageView {
	view := companyMessageView(m)
	if m.SenderType == domain.SenderCompany {
		view.SenderAccountID = ""
	}
	return view
}
