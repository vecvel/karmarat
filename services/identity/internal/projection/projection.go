// Package projection keeps the identity service's company read model current.
//
// Companies and subscriptions are owned by other services. Identity holds a
// narrow copy — slug, state, entitlement — because it must answer "may this
// person enter this portal?" on every sign-in without a synchronous call to two
// other services on the critical path.
package projection

import (
	"context"
	"log/slog"
	"time"

	"github.com/karmarat/platform/packages/goshared/events"
	"github.com/karmarat/platform/services/identity/internal/domain"
	"github.com/karmarat/platform/services/identity/internal/provisioning"
	"github.com/karmarat/platform/services/identity/internal/store"
)

// Consumer applies platform events to the identity projection.
type Consumer struct {
	store       *store.Store
	provisioner *provisioning.Provisioner
	logger      *slog.Logger
}

// NewConsumer builds the projection consumer.
func NewConsumer(st *store.Store, p *provisioning.Provisioner, logger *slog.Logger) *Consumer {
	return &Consumer{store: st, provisioner: p, logger: logger}
}

// Subjects are the events this consumer reacts to.
func Subjects() []string {
	return []string{
		events.SubjectCompanyApproved,
		events.SubjectCompanySuspended,
		events.SubjectCompanyUpdated,
		events.SubjectSubscriptionActivated,
		events.SubjectSubscriptionRenewed,
		events.SubjectSubscriptionExpired,
		events.SubjectSubscriptionCancelled,
	}
}

type companyEvent struct {
	CompanyID string `json:"companyId"`
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	State     string `json:"state"`
}

type subscriptionEvent struct {
	CompanyID    string         `json:"companyId"`
	State        string         `json:"state"`
	ExpiresAt    *time.Time     `json:"expiresAt"`
	Entitlements map[string]any `json:"entitlements"`
}

// Handle applies one event.
//
// Returning an error nak's the message so JetStream redelivers it; every branch
// is idempotent, so a redelivery is harmless.
func (c *Consumer) Handle(ctx context.Context, envelope events.Envelope) error {
	switch envelope.Subject {
	case events.SubjectCompanyApproved:
		return c.applyCompanyState(ctx, envelope, domain.CompanyActive)

	case events.SubjectCompanySuspended:
		return c.applyCompanyState(ctx, envelope, domain.CompanySuspended)

	case events.SubjectCompanyUpdated:
		payload, err := events.Decode[companyEvent](envelope)
		if err != nil {
			return err
		}
		existing, err := c.store.FindCompanyByID(ctx, payload.CompanyID)
		if err != nil {
			// The company may not be projected yet; a redelivery will catch it.
			return err
		}
		if payload.Slug != "" {
			existing.Slug = payload.Slug
		}
		if payload.Name != "" {
			existing.Name = payload.Name
		}
		return c.store.UpsertCompany(ctx, nil, existing)

	case events.SubjectSubscriptionActivated,
		events.SubjectSubscriptionRenewed,
		events.SubjectSubscriptionExpired,
		events.SubjectSubscriptionCancelled:

		payload, err := events.Decode[subscriptionEvent](envelope)
		if err != nil {
			return err
		}

		state := domain.SubscriptionState(payload.State)
		if state == "" {
			state = subscriptionStateFor(envelope.Subject)
		}

		c.logger.Info("applying subscription change",
			slog.String("company_id", payload.CompanyID),
			slog.String("state", string(state)),
		)

		return c.provisioner.UpdateSubscription(ctx,
			payload.CompanyID, state, payload.ExpiresAt, payload.Entitlements)

	default:
		return nil
	}
}

func (c *Consumer) applyCompanyState(ctx context.Context, envelope events.Envelope, state domain.CompanyState) error {
	payload, err := events.Decode[companyEvent](envelope)
	if err != nil {
		return err
	}

	c.logger.Info("applying company state change",
		slog.String("company_id", payload.CompanyID),
		slog.String("state", string(state)),
	)

	return c.provisioner.SetCompanyState(ctx, payload.CompanyID, state)
}

func subscriptionStateFor(subject string) domain.SubscriptionState {
	switch subject {
	case events.SubjectSubscriptionActivated, events.SubjectSubscriptionRenewed:
		return domain.SubscriptionActive
	case events.SubjectSubscriptionExpired:
		return domain.SubscriptionExpired
	case events.SubjectSubscriptionCancelled:
		return domain.SubscriptionCancelled
	default:
		return domain.SubscriptionNone
	}
}
