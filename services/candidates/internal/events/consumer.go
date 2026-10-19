package events

import (
	"context"
	"log/slog"

	"github.com/karmarat/platform/packages/goshared/events"
	"github.com/karmarat/platform/services/candidates/internal/store"
)

// Consumer reacts to facts published by other services.
type Consumer struct {
	store  *store.Store
	logger *slog.Logger
}

// NewConsumer builds the consumer.
func NewConsumer(st *store.Store, logger *slog.Logger) *Consumer {
	return &Consumer{store: st, logger: logger}
}

// Subjects are the events this consumer reacts to.
func Subjects() []string {
	return []string{events.SubjectCandidateRegistered}
}

// Durable names this service's consumer on the shared stream.
const Durable = "candidates-registration"

type candidateRegistered struct {
	AccountID string `json:"accountId"`
	Email     string `json:"email"`
	FullName  string `json:"fullName"`
}

// Handle applies one event.
//
// Registration is the only fact this service consumes, and it creates the
// profile shell so a candidate who signs up on the jobs portal lands on a
// profile page rather than a 404. The insert upserts on account_id, so a
// redelivery — or a replay of the whole stream — changes nothing.
func (c *Consumer) Handle(ctx context.Context, envelope events.Envelope) error {
	if envelope.Subject != events.SubjectCandidateRegistered {
		return nil
	}

	payload, err := events.Decode[candidateRegistered](envelope)
	if err != nil {
		return err
	}
	if payload.AccountID == "" {
		// Nothing to key the profile on; redelivery cannot improve the payload.
		c.logger.Warn("candidate registration event carried no account id",
			slog.String("event_id", envelope.ID))
		return nil
	}

	profile, created, err := c.store.EnsureProfile(ctx, payload.AccountID, payload.Email, payload.FullName)
	if err != nil {
		return err
	}
	if created {
		c.logger.Info("candidate profile created",
			slog.String("candidate_id", profile.ID),
			slog.String("account_id", profile.AccountID),
		)
	}
	return nil
}
