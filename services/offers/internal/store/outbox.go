package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/karmarat/platform/packages/goshared/idgen"
)

// outboxEntry is one event waiting to be published.
type outboxEntry struct {
	Subject   string
	CompanyID string
	ActorID   string
	Payload   map[string]any
}

// enqueueEvent writes an event in the same transaction as the change it
// describes, so the fact and its announcement commit or fail together.
func (s *Store) enqueueEvent(ctx context.Context, tx pgx.Tx, entry outboxEntry) error {
	_, err := s.exec(ctx, tx, `
		INSERT INTO event_outbox (id, subject, company_id, actor_id, payload)
		VALUES ($1, $2, $3, $4, $5)`,
		idgen.New("obx"), entry.Subject, nullableUUID(entry.CompanyID), nullable(entry.ActorID), entry.Payload)
	if err != nil {
		return fmt.Errorf("store: enqueue event: %w", err)
	}
	return nil
}

// PendingEvent is an outbox row handed to the publisher.
type PendingEvent struct {
	ID        string
	Subject   string
	CompanyID string
	ActorID   string
	Payload   map[string]any
}

// ClaimPendingEvents locks a batch of unpublished events for this worker.
//
// SKIP LOCKED is what makes the publisher horizontally safe: several instances
// can drain the outbox at once and each takes rows the others are not holding,
// so a deploy running two replicas does not announce the same offer twice.
func (s *Store) ClaimPendingEvents(ctx context.Context, tx pgx.Tx, limit int) ([]PendingEvent, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, subject, coalesce(company_id::text, ''), coalesce(actor_id, ''), payload
		FROM event_outbox
		WHERE published_at IS NULL
		ORDER BY created_at, id
		LIMIT $1
		FOR UPDATE SKIP LOCKED`, limit)
	if err != nil {
		return nil, fmt.Errorf("store: claim pending events: %w", err)
	}
	defer rows.Close()

	pending := make([]PendingEvent, 0, limit)
	for rows.Next() {
		var event PendingEvent
		if err := rows.Scan(&event.ID, &event.Subject, &event.CompanyID, &event.ActorID, &event.Payload); err != nil {
			return nil, fmt.Errorf("store: scan pending event: %w", err)
		}
		pending = append(pending, event)
	}
	return pending, rows.Err()
}

// MarkEventPublished records that an event reached the bus.
func (s *Store) MarkEventPublished(ctx context.Context, tx pgx.Tx, id string) error {
	if _, err := s.exec(ctx, tx,
		`UPDATE event_outbox SET published_at = now(), attempts = attempts + 1 WHERE id = $1`, id); err != nil {
		return fmt.Errorf("store: mark event published: %w", err)
	}
	return nil
}

// MarkEventFailed records a failed publish so a stuck event is visible.
func (s *Store) MarkEventFailed(ctx context.Context, tx pgx.Tx, id, reason string) error {
	if _, err := s.exec(ctx, tx,
		`UPDATE event_outbox SET attempts = attempts + 1, last_error = $2 WHERE id = $1`,
		id, truncate(reason, 500)); err != nil {
		return fmt.Errorf("store: mark event failed: %w", err)
	}
	return nil
}

// PurgePublishedEvents drops outbox rows old enough to be of no diagnostic use.
func (s *Store) PurgePublishedEvents(ctx context.Context, olderThanDays int) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM event_outbox
		WHERE published_at IS NOT NULL
		  AND published_at < now() - make_interval(days => $1)`, olderThanDays)
	if err != nil {
		return 0, fmt.Errorf("store: purge published events: %w", err)
	}
	return tag.RowsAffected(), nil
}

// nullableUUID keeps an empty tenant out of a uuid column: a platform-wide
// event has no company, and "" is not a uuid.
func nullableUUID(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func truncate(v string, max int) string {
	if len(v) <= max {
		return v
	}
	return v[:max]
}
