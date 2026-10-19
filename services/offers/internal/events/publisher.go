// Package events publishes this service's domain facts and consumes the ones it
// reacts to.
package events

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	platformevents "github.com/karmarat/platform/packages/goshared/events"
	"github.com/karmarat/platform/services/offers/internal/store"
)

// Publisher drains the transactional outbox onto the bus.
//
// Nothing publishes inline: the offer row and its event are written in one
// transaction, and this worker is what carries the event the rest of the way. A
// broker outage therefore delays the notification of an accepted offer instead
// of losing the fact that it was accepted.
type Publisher struct {
	store  *store.Store
	bus    *platformevents.Bus
	logger *slog.Logger
	batch  int
}

// NewPublisher builds the outbox publisher.
func NewPublisher(st *store.Store, bus *platformevents.Bus, logger *slog.Logger) *Publisher {
	return &Publisher{store: st, bus: bus, logger: logger, batch: 100}
}

// Run drains the outbox until the context is cancelled.
func (p *Publisher) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := p.Drain(ctx); err != nil {
				p.logger.Error("outbox drain failed", slog.Any("error", err))
			}
		}
	}
}

// Drain publishes one batch of pending events.
func (p *Publisher) Drain(ctx context.Context) error {
	return p.store.InTx(ctx, func(tx pgx.Tx) error {
		pending, err := p.store.ClaimPendingEvents(ctx, tx, p.batch)
		if err != nil {
			return err
		}

		for _, event := range pending {
			publishErr := p.bus.Publish(ctx, event.Subject, event.Payload, platformevents.PublishOptions{
				// The outbox row id doubles as the event id, so a republish after a
				// crash between the PUBLISH and the "mark sent" is recognized by the
				// broker as the same fact rather than delivered twice.
				ID:        event.ID,
				CompanyID: event.CompanyID,
				ActorID:   event.ActorID,
			})
			if publishErr != nil {
				// Record the attempt and stop: the rest of the batch is almost
				// certainly failing for the same reason, and the rows stay
				// pending for the next tick.
				if err := p.store.MarkEventFailed(ctx, tx, event.ID, publishErr.Error()); err != nil {
					return err
				}
				p.logger.Warn("event publish failed, will retry",
					slog.String("subject", event.Subject),
					slog.String("outbox_id", event.ID),
					slog.Any("error", publishErr))
				return nil
			}

			if err := p.store.MarkEventPublished(ctx, tx, event.ID); err != nil {
				return err
			}
		}
		return nil
	})
}
