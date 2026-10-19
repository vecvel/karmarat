package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/karmarat/platform/packages/goshared/idgen"
	"github.com/karmarat/platform/packages/goshared/tenancy"
	"github.com/karmarat/platform/services/notifications/internal/domain"
)

const notificationColumns = `
	id, principal_type, account_id, coalesce(company_id::text, ''), type,
	title, body, link, payload, read_at, event_id, created_at, updated_at`

func scanNotification(row pgx.Row) (domain.Notification, error) {
	var n domain.Notification
	err := row.Scan(
		&n.ID, &n.PrincipalType, &n.AccountID, &n.CompanyID, &n.Type,
		&n.Title, &n.Body, &n.Link, &n.Payload, &n.ReadAt, &n.EventID,
		&n.CreatedAt, &n.UpdatedAt,
	)
	if err != nil {
		return domain.Notification{}, err
	}
	if n.Payload == nil {
		n.Payload = map[string]any{}
	}
	return n, nil
}

// CreateInput is one notification to deliver.
type CreateInput struct {
	Recipient domain.Recipient
	// CompanyID is the tenant the notification is *about*. For a company
	// recipient it is their own tenant and is enforced as such below. For a
	// candidate it is context — which company's pipeline moved — and their
	// inbox is not filtered by it, because a candidate belongs to no tenant.
	CompanyID string
	Type      domain.Type
	Title     string
	Body      string
	Link      string
	Payload   map[string]any
	// EventID is the platform event this came from. It is the de-duplication
	// key, so it must be carried whenever there is one.
	EventID string
}

// Create inserts a notification, or reports that this event already reached
// this recipient.
//
// The uniqueness is enforced by an index rather than by a prior SELECT: two
// replicas can be handed the same redelivered event at the same instant, and
// only the database can settle which of them wins.
func (s *Store) Create(ctx context.Context, in CreateInput) (domain.Notification, bool, error) {
	if in.Payload == nil {
		in.Payload = map[string]any{}
	}

	// A company notification is filed under the recipient's own tenant, never
	// under whatever tenant the event named. The two are the same on every path
	// that exists today; pinning it here means a future caller that gets the
	// pairing wrong writes an unreachable row rather than one that shows up in
	// the wrong company's inbox.
	companyID := in.CompanyID
	if in.Recipient.PrincipalType == tenancy.PrincipalCompany {
		companyID = in.Recipient.CompanyID
	}

	row := s.pool.QueryRow(ctx, `
		INSERT INTO notifications (
			id, principal_type, account_id, company_id, type, title, body, link, payload, event_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (event_id, principal_type, account_id) WHERE event_id <> '' DO NOTHING
		RETURNING `+notificationColumns,
		idgen.New("ntf"),
		string(in.Recipient.PrincipalType),
		in.Recipient.AccountID,
		nullableUUID(companyID),
		string(in.Type),
		in.Title, in.Body, in.Link, in.Payload, in.EventID,
	)

	created, err := scanNotification(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// DO NOTHING returns no row: the event has already been delivered.
			return domain.Notification{}, false, nil
		}
		return domain.Notification{}, false, fmt.Errorf("store: create notification: %w", err)
	}
	return created, true, nil
}

// Listing is one page of an inbox plus the unread badge count.
type Listing struct {
	Notifications []domain.Notification
	NextCursor    string
	UnreadCount   int
}

// List returns a page of one recipient's notifications, newest first.
//
// The recipient is the whole filter. A company principal is additionally
// narrowed by their tenant, so a recruiter who moves between two companies sees
// only the inbox of the one they are signed into — the same account id in
// another tenant is a different inbox, not a wider one.
func (s *Store) List(ctx context.Context, recipient domain.Recipient, page domain.Page) (Listing, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+notificationColumns+`
		FROM notifications
		WHERE principal_type = $1
		  AND account_id = $2
		  AND ($3::uuid IS NULL OR company_id = $3::uuid)
		  AND (NOT $4::boolean OR read_at IS NULL)
		  AND ($5::timestamptz IS NULL
		       OR (created_at, id) < ($5::timestamptz, $6::text))
		ORDER BY created_at DESC, id DESC
		LIMIT $7`,
		string(recipient.PrincipalType),
		recipient.AccountID,
		tenantFilter(recipient),
		page.UnreadOnly,
		cursorAt(page),
		page.Cursor.ID,
		page.Limit+1,
	)
	if err != nil {
		return Listing{}, fmt.Errorf("store: list notifications: %w", err)
	}
	defer rows.Close()

	notifications := make([]domain.Notification, 0, page.Limit)
	for rows.Next() {
		n, err := scanNotification(rows)
		if err != nil {
			return Listing{}, fmt.Errorf("store: scan notification: %w", err)
		}
		notifications = append(notifications, n)
	}
	if err := rows.Err(); err != nil {
		return Listing{}, fmt.Errorf("store: list notifications: %w", err)
	}

	listing := Listing{Notifications: notifications}

	// One row more than the page was asked for: its presence is how we know
	// there is a next page without a second COUNT over the whole inbox.
	if len(notifications) > page.Limit {
		last := notifications[page.Limit-1]
		listing.Notifications = notifications[:page.Limit]
		listing.NextCursor = domain.Cursor{At: last.CreatedAt, ID: last.ID}.Encode()
	}

	count, err := s.UnreadCount(ctx, recipient)
	if err != nil {
		return Listing{}, err
	}
	listing.UnreadCount = count

	return listing, nil
}

// UnreadCount is the badge number, filtered by the same recipient predicate.
func (s *Store) UnreadCount(ctx context.Context, recipient domain.Recipient) (int, error) {
	var count int
	err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM notifications
		WHERE principal_type = $1
		  AND account_id = $2
		  AND ($3::uuid IS NULL OR company_id = $3::uuid)
		  AND read_at IS NULL`,
		string(recipient.PrincipalType), recipient.AccountID, tenantFilter(recipient),
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("store: count unread: %w", err)
	}
	return count, nil
}

// MarkRead marks one of the recipient's own notifications as seen.
//
// Marking an already-read notification succeeds: a client that retries a tap on
// a flaky connection should not be told its second attempt was a mistake. A row
// that belongs to somebody else is reported as not found, which is the same
// answer an id that never existed gets.
func (s *Store) MarkRead(ctx context.Context, recipient domain.Recipient, id string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE notifications
		SET read_at = coalesce(read_at, now()), updated_at = now()
		WHERE id = $1
		  AND principal_type = $2
		  AND account_id = $3
		  AND ($4::uuid IS NULL OR company_id = $4::uuid)`,
		id, string(recipient.PrincipalType), recipient.AccountID, tenantFilter(recipient),
	)
	if err != nil {
		return fmt.Errorf("store: mark notification read: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotificationNotFound
	}
	return nil
}

// MarkAllRead clears the recipient's badge and reports how many it cleared.
func (s *Store) MarkAllRead(ctx context.Context, recipient domain.Recipient) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE notifications
		SET read_at = now(), updated_at = now()
		WHERE principal_type = $1
		  AND account_id = $2
		  AND ($3::uuid IS NULL OR company_id = $3::uuid)
		  AND read_at IS NULL`,
		string(recipient.PrincipalType), recipient.AccountID, tenantFilter(recipient),
	)
	if err != nil {
		return 0, fmt.Errorf("store: mark all read: %w", err)
	}
	return tag.RowsAffected(), nil
}

// tenantFilter narrows a company principal to its own tenant.
//
// A candidate has no tenant of their own — their notifications carry whichever
// company the event was about — so filtering them by company would hide their
// own mail from them. Their account id is already unique across the platform,
// and the principal type keeps the two populations apart.
//
// A company principal that somehow arrived without a tenant is filtered to the
// nil UUID, which matches nothing. The API rejects that request before it gets
// here; this is the second lock on the same door, and the failure mode it picks
// is an empty inbox rather than an unfiltered one.
func tenantFilter(recipient domain.Recipient) *string {
	if recipient.PrincipalType != tenancy.PrincipalCompany {
		return nil
	}
	tenant := recipient.TenantKey()
	return &tenant
}

// cursorAt renders the keyset position, or nil for the first page.
func cursorAt(page domain.Page) any {
	if !page.Cursor.Set() {
		return nil
	}
	return page.Cursor.At
}
