// Package events publishes the candidates service's facts and consumes the ones
// it reacts to.
package events

import (
	"context"
	"log/slog"

	"github.com/karmarat/platform/packages/goshared/events"
	"github.com/karmarat/platform/services/candidates/internal/domain"
)

// Publisher emits this service's domain events.
type Publisher struct {
	bus    *events.Bus
	logger *slog.Logger
}

// NewPublisher builds a publisher.
func NewPublisher(bus *events.Bus, logger *slog.Logger) *Publisher {
	return &Publisher{bus: bus, logger: logger}
}

// ProfileUpdated announces a change to a candidate's profile.
//
// The version travels with the payload so a consumer that receives two
// deliveries out of order can discard the older one rather than overwrite its
// projection with a stale copy.
func (p *Publisher) ProfileUpdated(ctx context.Context, profile domain.Profile, deleted bool) {
	p.publish(ctx, events.SubjectCandidateProfileUpdated, map[string]any{
		"candidateId":  profile.ID,
		"accountId":    profile.AccountID,
		"fullName":     profile.FullName,
		"headline":     profile.Headline,
		"location":     profile.Location,
		"discoverable": profile.Visibility.Discoverable,
		"deleted":      deleted,
		"version":      profile.Version,
	}, events.PublishOptions{ActorID: profile.AccountID})
}

// VisibilityChanged announces that a candidate changed who may find them.
//
// It is a separate subject from a profile update because the consumers differ:
// a search index must drop the profile immediately, while a projection of the
// candidate's name does not care.
func (p *Publisher) VisibilityChanged(ctx context.Context, profile domain.Profile) {
	p.publish(ctx, events.SubjectCandidateVisibilityChanged, map[string]any{
		"candidateId":         profile.ID,
		"accountId":           profile.AccountID,
		"discoverable":        profile.Visibility.Discoverable,
		"hideCurrentEmployer": profile.Visibility.HideCurrentEmployer,
		"hideFromCompanies":   profile.Visibility.HideFromCompanies,
		"version":             profile.Version,
	}, events.PublishOptions{ActorID: profile.AccountID})
}

// Approached announces that a company reached out to a candidate, which is what
// messaging and notifications act on.
func (p *Publisher) Approached(ctx context.Context, approach domain.Approach, accountID string) {
	p.publish(ctx, events.SubjectCandidateApproached, map[string]any{
		"approachId":   approach.ID,
		"candidateId":  approach.CandidateID,
		"accountId":    accountID,
		"companyId":    approach.CompanyID,
		"jobId":        approach.JobID,
		"subject":      approach.Subject,
		"message":      approach.Message,
		"approachedAt": approach.CreatedAt,
	}, events.PublishOptions{
		CompanyID: approach.CompanyID,
		ActorID:   approach.ActorAccountID,
	})
}

// publish logs a failure instead of returning it.
//
// The write the caller made has already committed; failing their request
// because the bus hiccuped would ask them to repeat a change that took effect.
// Redelivery of a missed event is a repair job, not the caller's problem.
func (p *Publisher) publish(ctx context.Context, subject string, payload any, opts events.PublishOptions) {
	if p.bus == nil {
		return
	}
	if err := p.bus.Publish(ctx, subject, payload, opts); err != nil {
		p.logger.Error("failed to publish event",
			slog.String("subject", subject),
			slog.Any("error", err),
		)
	}
}
