package store

import (
	"context"
	"fmt"

	"github.com/karmarat/platform/packages/goshared/idgen"
	"github.com/karmarat/platform/services/notifications/internal/domain"
)

// EmailInput is one message to queue.
type EmailInput struct {
	// DedupeKey is derived from the source event and the recipient. A
	// redelivered event collides on it instead of sending a second copy.
	DedupeKey      string
	NotificationID string
	Recipient      domain.Recipient
	Subject        string
	Template       string
	// Data is what the template renders. It holds a title, a body and a link
	// path — never a token, a password or a signed URL.
	Data        map[string]any
	MaxAttempts int
}

// QueueEmail adds a message to the send queue.
//
// It returns whether the row was new. A duplicate is not an error: it is the
// de-duplication working.
func (s *Store) QueueEmail(ctx context.Context, in EmailInput) (bool, error) {
	if in.Data == nil {
		in.Data = map[string]any{}
	}
	if in.Template == "" {
		in.Template = "notification"
	}
	if in.MaxAttempts <= 0 {
		in.MaxAttempts = 5
	}

	tag, err := s.pool.Exec(ctx, `
		INSERT INTO email_outbox (
			id, dedupe_key, notification_id, company_id,
			to_address, to_name, subject, template, data, max_attempts)
		VALUES ($1, $2, $3, $4::uuid, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (dedupe_key) DO NOTHING`,
		idgen.New("eml"), in.DedupeKey, in.NotificationID, nullableUUID(in.Recipient.CompanyID),
		in.Recipient.Email, in.Recipient.Name, in.Subject, in.Template, in.Data, in.MaxAttempts,
	)
	if err != nil {
		return false, fmt.Errorf("store: queue email: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// PendingEmail is one queued message handed to the worker.
type PendingEmail struct {
	ID          string
	ToAddress   string
	ToName      string
	Subject     string
	Template    string
	Data        map[string]any
	Attempts    int
	MaxAttempts int
}

// Exhausted reports whether this attempt was the last one allowed.
func (e PendingEmail) Exhausted() bool { return e.Attempts >= e.MaxAttempts }

// ClaimDueEmails leases a batch of messages for this worker.
//
// The claim both takes the rows and schedules their retry: the attempt counter
// goes up and next_attempt_at moves to the backoff window before the SMTP
// conversation begins. A worker that is killed mid-send therefore leaves the
// message to be retried later rather than stuck, and a worker that succeeds
// clears the row before that window comes round.
//
// SKIP LOCKED is what makes this safe to run in every replica: each one takes
// rows the others are not holding, so two instances do not both mail the same
// candidate.
func (s *Store) ClaimDueEmails(ctx context.Context, limit int, backoffSeconds int) ([]PendingEmail, error) {
	rows, err := s.pool.Query(ctx, `
		UPDATE email_outbox o
		SET attempts = o.attempts + 1,
		    -- Exponential, capped: 30s, 90s, 4.5m, 13.5m, 40m, then flat.
		    next_attempt_at = now() + make_interval(
		        secs => $2::double precision * power(3, least(o.attempts, 4))),
		    updated_at = now()
		FROM (
			SELECT id FROM email_outbox
			WHERE sent_at IS NULL
			  AND dead_lettered_at IS NULL
			  AND next_attempt_at <= now()
			ORDER BY next_attempt_at, id
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		) due
		WHERE o.id = due.id
		RETURNING o.id, o.to_address, o.to_name, o.subject, o.template, o.data,
		          o.attempts, o.max_attempts`,
		limit, backoffSeconds,
	)
	if err != nil {
		return nil, fmt.Errorf("store: claim due emails: %w", err)
	}
	defer rows.Close()

	pending := make([]PendingEmail, 0, limit)
	for rows.Next() {
		var email PendingEmail
		if err := rows.Scan(&email.ID, &email.ToAddress, &email.ToName, &email.Subject,
			&email.Template, &email.Data, &email.Attempts, &email.MaxAttempts); err != nil {
			return nil, fmt.Errorf("store: scan due email: %w", err)
		}
		pending = append(pending, email)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: claim due emails: %w", err)
	}

	return pending, nil
}

// MarkEmailSent records a successful delivery.
func (s *Store) MarkEmailSent(ctx context.Context, id string) error {
	if _, err := s.pool.Exec(ctx,
		`UPDATE email_outbox SET sent_at = now(), last_error = '', updated_at = now() WHERE id = $1`,
		id); err != nil {
		return fmt.Errorf("store: mark email sent: %w", err)
	}
	return nil
}

// MarkEmailFailed records a failed attempt, dead-lettering when the attempts
// have run out.
//
// The row is kept rather than deleted. A queue that quietly drops undeliverable
// mail looks exactly like one that is working, and the first time anybody finds
// out is when a candidate says they never heard back.
func (s *Store) MarkEmailFailed(ctx context.Context, id, reason string, exhausted bool) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE email_outbox
		SET last_error = $2,
		    dead_lettered_at = CASE WHEN $3 THEN now() ELSE dead_lettered_at END,
		    updated_at = now()
		WHERE id = $1`,
		id, truncate(reason, 500), exhausted,
	)
	if err != nil {
		return fmt.Errorf("store: mark email failed: %w", err)
	}
	return nil
}

// DeadLetterCount reports how much mail has given up, for the readiness log.
func (s *Store) DeadLetterCount(ctx context.Context) (int, error) {
	var count int
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM email_outbox WHERE dead_lettered_at IS NOT NULL`).Scan(&count); err != nil {
		return 0, fmt.Errorf("store: count dead letters: %w", err)
	}
	return count, nil
}
