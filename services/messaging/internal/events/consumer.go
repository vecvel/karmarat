package events

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	platformevents "github.com/karmarat/platform/packages/goshared/events"
	"github.com/karmarat/platform/services/messaging/internal/domain"
	"github.com/karmarat/platform/services/messaging/internal/store"
)

// Consumer applies other services' facts to this service's data.
//
// Three of the four subjects maintain projections that the "may this company
// open a thread?" decision reads from. The fourth, an approach, has a side
// effect the candidate can see: it opens the conversation their reply will land
// in. That is the reason the approach is consumed here rather than left to the
// recruiter to start by hand — a candidate who answers a sourcing message must
// have somewhere to answer it.
type Consumer struct {
	store  *store.Store
	logger *slog.Logger
}

// NewConsumer builds the event consumer.
func NewConsumer(st *store.Store, logger *slog.Logger) *Consumer {
	return &Consumer{store: st, logger: logger}
}

// Subjects are the events this consumer reacts to.
func Subjects() []string {
	return []string{
		platformevents.SubjectCandidateApproached,
		platformevents.SubjectApplicationSubmitted,
		platformevents.SubjectCandidateVisibilityChanged,
		platformevents.SubjectCandidateProfileUpdated,
	}
}

type candidateApproached struct {
	ApproachID   string    `json:"approachId"`
	CandidateID  string    `json:"candidateId"`
	AccountID    string    `json:"accountId"`
	CompanyID    string    `json:"companyId"`
	JobID        string    `json:"jobId"`
	Subject      string    `json:"subject"`
	Message      string    `json:"message"`
	ApproachedAt time.Time `json:"approachedAt"`
}

type applicationSubmitted struct {
	ApplicationID string    `json:"applicationId"`
	CompanyID     string    `json:"companyId"`
	JobID         string    `json:"jobId"`
	JobTitle      string    `json:"jobTitle"`
	CandidateID   string    `json:"candidateId"`
	SubmittedAt   time.Time `json:"submittedAt"`
}

type visibilityChanged struct {
	AccountID         string   `json:"accountId"`
	Discoverable      bool     `json:"discoverable"`
	HideFromCompanies []string `json:"hideFromCompanies"`
	Version           int64    `json:"version"`
}

type profileUpdated struct {
	AccountID    string `json:"accountId"`
	Discoverable bool   `json:"discoverable"`
	Deleted      bool   `json:"deleted"`
	Version      int64  `json:"version"`
}

// Handle applies one event.
//
// Returning an error nak's the message so JetStream redelivers it; returning nil
// for an event this service cannot use retires it, because a redelivery of the
// same malformed payload would fail in exactly the same way. Every branch is
// idempotent: the projections upsert under a version guard, and the approach
// branch is keyed on the approach id.
func (c *Consumer) Handle(ctx context.Context, envelope platformevents.Envelope) error {
	switch envelope.Subject {
	case platformevents.SubjectCandidateApproached:
		return c.handleApproached(ctx, envelope)

	case platformevents.SubjectApplicationSubmitted:
		payload, err := platformevents.Decode[applicationSubmitted](envelope)
		if err != nil {
			return err
		}

		companyID := firstNonEmpty(payload.CompanyID, envelope.CompanyID)
		if companyID == "" || payload.ApplicationID == "" || payload.CandidateID == "" {
			c.logger.Warn("application event was missing its identifiers",
				slog.String("event_id", envelope.ID))
			return nil
		}

		// An application is standing permission to be contacted about it, and
		// this row is what lets that be decided without a network hop.
		return c.store.RecordApplication(ctx, store.ApplicationLink{
			CompanyID:          companyID,
			CandidateAccountID: payload.CandidateID,
			ApplicationID:      payload.ApplicationID,
			JobID:              payload.JobID,
			JobTitle:           payload.JobTitle,
			SubmittedAt:        payload.SubmittedAt,
		})

	case platformevents.SubjectCandidateVisibilityChanged:
		payload, err := platformevents.Decode[visibilityChanged](envelope)
		if err != nil {
			return err
		}
		if payload.AccountID == "" {
			c.logger.Warn("visibility event carried no account id",
				slog.String("event_id", envelope.ID))
			return nil
		}

		return c.store.UpsertVisibility(ctx, payload.AccountID,
			payload.Discoverable, payload.HideFromCompanies, payload.Version)

	case platformevents.SubjectCandidateProfileUpdated:
		payload, err := platformevents.Decode[profileUpdated](envelope)
		if err != nil {
			return err
		}
		if payload.AccountID == "" {
			c.logger.Warn("profile event carried no account id",
				slog.String("event_id", envelope.ID))
			return nil
		}

		// Deliberately not the block list: a profile update does not carry it,
		// and writing a default here would silently un-block every company the
		// candidate had excluded.
		return c.store.UpsertDiscoverability(ctx, payload.AccountID,
			payload.Discoverable, payload.Deleted, payload.Version)

	default:
		return nil
	}
}

// handleApproached opens the thread a sourced candidate can reply in.
//
// Idempotency has two layers because a redelivery can arrive after either half
// of the work. The conversation carries the approach id as its origin_ref, which
// is unique per tenant, so a second delivery cannot create a second thread; the
// first message carries the same id as its idempotency key, so a delivery that
// lands on a thread already opened by an earlier approach cannot post the same
// sourcing message twice either.
//
// The eligibility rules the API enforces are deliberately not re-run here. The
// candidates service applied them when it accepted the approach, and refusing
// now would leave a candidate who was already written to with no thread to
// answer in.
func (c *Consumer) handleApproached(ctx context.Context, envelope platformevents.Envelope) error {
	payload, err := platformevents.Decode[candidateApproached](envelope)
	if err != nil {
		return err
	}

	companyID := firstNonEmpty(payload.CompanyID, envelope.CompanyID)
	accountID := strings.TrimSpace(payload.AccountID)
	if companyID == "" || accountID == "" || payload.ApproachID == "" {
		// A redelivery cannot supply what the payload never carried.
		c.logger.Warn("approach event was missing its identifiers",
			slog.String("event_id", envelope.ID))
		return nil
	}

	subject, err := domain.ValidateSubject(payload.Subject)
	if err != nil {
		// The subject is cosmetic; an over-long one is not worth dropping a
		// candidate's thread over.
		subject = domain.Preview(payload.Subject)
	}

	// The actor is the recruiter who sent the approach. It may be absent on an
	// automated approach, in which case the thread is the company's rather than
	// any one recruiter's, and every recruiter with `messaging.read_all` sees it.
	var first *store.NewMessage
	if body := strings.TrimSpace(payload.Message); body != "" {
		first = &store.NewMessage{
			SenderType:      domain.SenderCompany,
			SenderAccountID: envelope.ActorID,
			Body:            body,
			Attachments:     []domain.Attachment{},
			IdempotencyKey:  payload.ApproachID,
		}
	}

	_, _, err = c.store.OpenConversation(ctx, store.NewConversation{
		CompanyID:          companyID,
		CandidateAccountID: accountID,
		JobID:              payload.JobID,
		Subject:            subject,
		Origin:             domain.OriginApproach,
		OriginRef:          payload.ApproachID,
		OpenedByAccountID:  envelope.ActorID,
		FirstMessage:       first,
	})
	switch {
	case err == nil:
		c.logger.Info("opened a conversation from an approach",
			slog.String("company_id", companyID),
			slog.String("approach_id", payload.ApproachID))
		return nil
	case errors.Is(err, domain.ErrConversationExists):
		// Either this exact approach was already processed, or the company and
		// candidate already have an open thread. Both mean the same thing here:
		// deliver the sourcing message into the thread that exists.
		return c.appendToExisting(ctx, companyID, accountID, first)
	default:
		return err
	}
}

func (c *Consumer) appendToExisting(
	ctx context.Context, companyID, accountID string, message *store.NewMessage,
) error {
	if message == nil {
		return nil
	}

	// An approach is never application-scoped, so the thread to reuse is the one
	// with no application context.
	conversation, err := c.store.FindExisting(ctx, companyID, accountID, "")
	if err != nil {
		if errors.Is(err, domain.ErrConversationNotFound) {
			// The conflict was on the approach id alone: this approach already
			// has its thread and there is nothing left to do.
			return nil
		}
		return err
	}

	_, replayed, err := c.store.AppendMessage(ctx, conversation, *message)
	if err != nil {
		return err
	}
	if !replayed {
		c.logger.Info("delivered an approach into an existing conversation",
			slog.String("company_id", companyID),
			slog.String("conversation_id", conversation.ID))
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
