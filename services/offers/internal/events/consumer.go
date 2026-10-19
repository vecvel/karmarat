package events

import (
	"context"
	"log/slog"
	"strings"

	platformevents "github.com/karmarat/platform/packages/goshared/events"
	"github.com/karmarat/platform/services/offers/internal/store"
)

// Consumer applies the applications service's facts to this service's data.
type Consumer struct {
	store  *store.Store
	logger *slog.Logger
}

// NewConsumer builds the event consumer.
func NewConsumer(st *store.Store, logger *slog.Logger) *Consumer {
	return &Consumer{store: st, logger: logger}
}

// Subjects are the events this consumer reacts to.
//
// All five carry the candidate's name and the job title, which is what keeps the
// snapshot on an offer current; two of them also end the application, which has
// to end any offer still outstanding against it.
func Subjects() []string {
	return []string{
		platformevents.SubjectApplicationSubmitted,
		platformevents.SubjectApplicationStageChanged,
		platformevents.SubjectApplicationHired,
		platformevents.SubjectApplicationWithdrawn,
		platformevents.SubjectApplicationRejected,
	}
}

// applicationEvent is the applications service's payload, narrowed to the fields
// this service uses. Anything else in it is ignored rather than mirrored: a
// projection that copies every field becomes a second source of truth.
type applicationEvent struct {
	ApplicationID string `json:"applicationId"`
	CompanyID     string `json:"companyId"`
	CandidateName string `json:"candidateName"`
	JobTitle      string `json:"jobTitle"`
}

// Handle applies one event.
//
// Returning an error nak's the message so JetStream redelivers it. Both branches
// are idempotent — the snapshot update is a write of the same values, and expiry
// only touches offers that are still outstanding — so a redelivery is a no-op
// rather than a second effect. Out-of-order delivery is survivable for the same
// reason: the only ordering that matters is that an ended application expires
// its offers, and an expired offer that then receives an older stage-change
// event has its name refreshed and its status left alone.
func (c *Consumer) Handle(ctx context.Context, envelope platformevents.Envelope) error {
	switch envelope.Subject {
	case platformevents.SubjectApplicationSubmitted,
		platformevents.SubjectApplicationStageChanged,
		platformevents.SubjectApplicationHired,
		platformevents.SubjectApplicationWithdrawn,
		platformevents.SubjectApplicationRejected:

		payload, err := platformevents.Decode[applicationEvent](envelope)
		if err != nil {
			return err
		}

		companyID := firstNonEmpty(payload.CompanyID, envelope.CompanyID)
		if companyID == "" || payload.ApplicationID == "" {
			// Nothing to scope the update to, and redelivering cannot supply it.
			c.logger.Warn("application event was missing its identifiers",
				slog.String("subject", envelope.Subject),
				slog.String("event_id", envelope.ID))
			return nil
		}

		if _, err := c.store.SyncApplicationSnapshot(ctx, companyID, payload.ApplicationID,
			strings.TrimSpace(payload.CandidateName), strings.TrimSpace(payload.JobTitle)); err != nil {
			return err
		}

		// Withdrawn and rejected are the two that end the process. An offer left
		// outstanding against an application nobody is pursuing is an offer the
		// candidate could still accept.
		if envelope.Subject == platformevents.SubjectApplicationWithdrawn ||
			envelope.Subject == platformevents.SubjectApplicationRejected {

			expired, err := c.store.ExpireOffersForApplication(ctx, companyID, payload.ApplicationID)
			if err != nil {
				return err
			}
			if expired > 0 {
				c.logger.Info("expired offers on a closed application",
					slog.String("company_id", companyID),
					slog.String("application_id", payload.ApplicationID),
					slog.Int64("offers", expired))
			}
		}
		return nil

	default:
		return nil
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
