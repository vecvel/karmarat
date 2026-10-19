// Package events carries this service's facts onto the bus and applies the
// facts other services publish.
package events

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	platformevents "github.com/karmarat/platform/packages/goshared/events"
	"github.com/karmarat/platform/services/messaging/internal/store"
)

// Publisher drains the transactional outbox onto the bus.
//
// Nothing in messaging publishes inline. The message row and the event that
// announces it are written in one transaction, and this worker carries the event
// the rest of the way, so a broker outage delays a notification instead of
// leaving a candidate with a message in their inbox and nothing to tell them it
// arrived — the one failure this domain cannot absorb, because nobody comes back
// to check an inbox they were never told about.
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
	if p.bus == nil {
		return nil
	}

	return p.store.InTx(ctx, func(tx pgx.Tx) error {
		pending, err := p.store.ClaimPendingEvents(ctx, tx, p.batch)
		if err != nil {
			return err
		}

		for _, event := range pending {
			// The outbox id would make the better de-duplication key, but
			// PublishOptions has no field for it and the bus mints its own; a
			// consumer therefore still has to be idempotent on the payload,
			// which every consumer on this platform already is.
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

// Purge drops outbox rows old enough to be of no diagnostic use.
//
// Published events are kept for a while on purpose: when a notification did not
// arrive, the first question is whether this service ever published it, and an
// immediately-deleted row cannot answer that.
func (p *Publisher) Purge(ctx context.Context, interval time.Duration, retainDays int) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			purged, err := p.store.PurgePublishedEvents(ctx, retainDays)
			if err != nil {
				p.logger.Warn("outbox purge failed", slog.Any("error", err))
				continue
			}
			if purged > 0 {
				p.logger.Info("purged published outbox rows", slog.Int64("count", purged))
			}
		}
	}
}
