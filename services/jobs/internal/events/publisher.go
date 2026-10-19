// Package events publishes the facts the jobs service owns.
//
// Everything downstream of a requisition — the search index on the shared board,
// a candidate's saved search, the company's dashboard counters — learns about a
// publication from here rather than by polling the jobs API.
package events

import (
	"context"
	"log/slog"

	"github.com/karmarat/platform/packages/goshared/events"
	"github.com/karmarat/platform/services/jobs/internal/domain"
)

// JobVisibility is the payload of every job event this service publishes.
//
// It carries the visibility flags as they stand *after* the change, so a
// consumer can act on the payload alone: "network is now false" is enough to
// drop the row from the shared board without asking what it used to be.
type JobVisibility struct {
	CompanyID        string `json:"companyId"`
	JobID            string `json:"jobId"`
	Slug             string `json:"slug"`
	Title            string `json:"title"`
	VisibleOnPortal  bool   `json:"visibleOnPortal"`
	VisibleOnNetwork bool   `json:"visibleOnNetwork"`
	Status           string `json:"status"`
}

// Publisher writes job events to the bus.
type Publisher struct {
	bus    *events.Bus
	logger *slog.Logger
}

// New builds a publisher. A nil bus makes every call a no-op, which is what
// lets a test exercise the API without a broker.
func New(bus *events.Bus, logger *slog.Logger) *Publisher {
	return &Publisher{bus: bus, logger: logger}
}

// PublishedOrUnpublished emits the event that matches the new visibility.
//
// Which subject fires is derived from the resulting state rather than from the
// caller's intent: a request that switches the portal on and the network off is
// one transition, and the fact worth publishing is where the job is now.
func (p *Publisher) PublishedOrUnpublished(ctx context.Context, job domain.Job, actorID, correlationID string) {
	subject := events.SubjectJobUnpublished
	if job.VisibleOnPortal || job.VisibleOnNetwork {
		subject = events.SubjectJobPublished
	}
	p.publish(ctx, subject, job, actorID, correlationID)
}

// Closed announces that a requisition has ended.
func (p *Publisher) Closed(ctx context.Context, job domain.Job, actorID, correlationID string) {
	p.publish(ctx, events.SubjectJobClosed, job, actorID, correlationID)
}

// Unpublished announces that a requisition left every board.
func (p *Publisher) Unpublished(ctx context.Context, job domain.Job, actorID, correlationID string) {
	p.publish(ctx, events.SubjectJobUnpublished, job, actorID, correlationID)
}

func (p *Publisher) publish(ctx context.Context, subject string, job domain.Job, actorID, correlationID string) {
	if p.bus == nil {
		return
	}

	payload := JobVisibility{
		CompanyID:        job.CompanyID,
		JobID:            job.ID,
		Slug:             job.Slug,
		Title:            job.Title,
		VisibleOnPortal:  job.VisibleOnPortal,
		VisibleOnNetwork: job.VisibleOnNetwork,
		Status:           string(job.Status),
	}

	// The database change is already committed, so the publish must not be
	// cancelled along with the request that caused it — and a broker that is
	// down must not turn a successful publication into a 500.
	if err := p.bus.Publish(context.WithoutCancel(ctx), subject, payload, events.PublishOptions{
		CompanyID:     job.CompanyID,
		ActorID:       actorID,
		CorrelationID: correlationID,
	}); err != nil {
		p.logger.Error("failed to publish event",
			slog.String("subject", subject),
			slog.String("job_id", job.ID),
			slog.Any("error", err))
	}
}
