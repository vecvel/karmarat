package store

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/karmarat/platform/services/audit/internal/domain"
)

// ExportRowLimit caps how much one CSV download may draw out of the trail.
//
// Without it, a request with no filters is a full table scan streamed to a
// browser, and an audit table is the largest one on the platform by design.
// The cap is high enough that a month of one tenant's activity fits and low
// enough that a handful of concurrent exports cannot hold the pool.
const ExportRowLimit = 50_000

const entryColumns = `
	id, subject, action, coalesce(company_id::text, ''), actor_id, correlation_id,
	entity_type, entity_id, occurred_at, recorded_at, payload`

func scanEntry(row pgx.Row) (domain.Entry, error) {
	var entry domain.Entry
	err := row.Scan(
		&entry.ID, &entry.Subject, &entry.Action, &entry.CompanyID,
		&entry.ActorID, &entry.CorrelationID,
		&entry.EntityType, &entry.EntityID,
		&entry.OccurredAt, &entry.RecordedAt, &entry.Payload,
	)
	return entry, err
}

// Record writes one event to the trail and reports whether it was new.
//
// The insert is conditional on the primary key rather than guarded by a prior
// SELECT. Two replicas consuming the same durable, or one replica handling a
// redelivery, both pass a check-then-insert; only the database can decide which
// of them wrote the row. `DO NOTHING` also means a redelivery never rewrites an
// existing entry, so the trail cannot be altered after the fact by replaying an
// event with a different payload.
func (s *Store) Record(ctx context.Context, entry domain.Entry) (bool, error) {
	payload := entry.Payload
	if payload == nil {
		payload = map[string]any{}
	}

	tag, err := s.pool.Exec(ctx, `
		INSERT INTO audit_entries (
			id, subject, action, company_id, actor_id, correlation_id,
			entity_type, entity_id, occurred_at, payload
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (id) DO NOTHING`,
		entry.ID, entry.Subject, entry.Action, nullableUUID(entry.CompanyID),
		entry.ActorID, entry.CorrelationID,
		entry.EntityType, entry.EntityID, entry.OccurredAt, payload,
	)
	if err != nil {
		return false, fmt.Errorf("store: record audit entry: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// Page is one page of a trail plus the cursor that continues it.
type Page struct {
	Entries    []domain.Entry
	NextCursor string
}

// ListForCompany returns one page of a single tenant's trail, newest first.
//
// companyID comes from the caller's verified principal and is the first
// predicate in the query. Platform-wide rows carry a NULL company_id and so can
// never satisfy it, which is the point: a tenant's trail must show what happened
// inside that account and nothing else, and an event with no tenant did not
// happen inside any account.
func (s *Store) ListForCompany(ctx context.Context, companyID string, filter domain.Filter) (Page, error) {
	where := []string{"company_id = $1"}
	args := []any{companyID}

	return s.list(ctx, "list company audit", where, args, filter)
}

// ListForPlatform returns one page of every tenant's trail, plus the rows that
// belong to no tenant.
//
// There is no company predicate here at all, which is exactly why the handler
// that reaches this method is guarded by a platform principal check as well as a
// permission: this is the one query in the service that spans tenants, so it is
// the one a mistake would be most expensive in.
func (s *Store) ListForPlatform(ctx context.Context, filter domain.Filter) (Page, error) {
	return s.list(ctx, "list platform audit", nil, nil, filter)
}

func (s *Store) list(
	ctx context.Context,
	operation string,
	where []string,
	args []any,
	filter domain.Filter,
) (Page, error) {
	where, args = appendFilters(where, args, filter)

	if filter.Page.Cursor.Set() {
		// The keyset compares the pair, not the timestamp alone: several events
		// share a timestamp routinely — one request that changed four things
		// publishes four events in the same microsecond — and a cursor on the
		// timestamp alone would either repeat them or skip them.
		args = append(args, filter.Page.Cursor.At, filter.Page.Cursor.ID)
		where = append(where, fmt.Sprintf(
			"(occurred_at, id) < ($%d::timestamptz, $%d::text)", len(args)-1, len(args)))
	}

	limit := filter.Page.Limit
	if limit <= 0 {
		limit = domain.DefaultPageLimit
	}
	// One row beyond the page, purely to learn whether another page exists —
	// cheaper than a COUNT over a table nothing ever deletes from.
	args = append(args, limit+1)

	query := `SELECT ` + entryColumns + ` FROM audit_entries` +
		whereClause(where) +
		` ORDER BY occurred_at DESC, id DESC LIMIT $` + strconv.Itoa(len(args))

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return Page{}, fmt.Errorf("store: %s: %w", operation, err)
	}
	defer rows.Close()

	entries := make([]domain.Entry, 0, limit)
	for rows.Next() {
		entry, err := scanEntry(rows)
		if err != nil {
			return Page{}, fmt.Errorf("store: scan audit entry: %w", err)
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return Page{}, fmt.Errorf("store: %s: %w", operation, err)
	}

	page := Page{Entries: entries}
	if len(entries) > limit {
		last := entries[limit-1]
		page.Entries = entries[:limit]
		page.NextCursor = domain.Cursor{At: last.OccurredAt, ID: last.ID}.Encode()
	}
	return page, nil
}

// ExportForCompany streams a tenant's trail to a callback, newest first.
//
// It hands rows out one at a time rather than returning a slice because an
// export is unbounded where a page is not: fifty thousand entries, each with a
// payload, is a memory spike that two people clicking "export" at the same
// moment multiply. The callback writes straight to the response, so the service
// holds one row at a time however large the download is.
//
// The filter is the same one the list endpoint applies, so "export what I am
// looking at" is one request rather than a client paging and stitching. The page
// cursor is ignored: an export is the whole filtered set, not a page of it.
func (s *Store) ExportForCompany(
	ctx context.Context,
	companyID string,
	filter domain.Filter,
	emit func(domain.Entry) error,
) error {
	where, args := appendFilters([]string{"company_id = $1"}, []any{companyID}, filter)

	args = append(args, ExportRowLimit)
	query := `SELECT ` + entryColumns + ` FROM audit_entries` +
		whereClause(where) +
		` ORDER BY occurred_at DESC, id DESC LIMIT $` + strconv.Itoa(len(args))

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("store: export company audit: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		entry, err := scanEntry(rows)
		if err != nil {
			return fmt.Errorf("store: scan audit entry: %w", err)
		}
		if err := emit(entry); err != nil {
			return err
		}
	}
	return rows.Err()
}

// appendFilters adds the predicates every trail shares.
//
// They are built once so the list, the export and the platform feed cannot
// interpret the same query string differently.
func appendFilters(where []string, args []any, filter domain.Filter) ([]string, []any) {
	add := func(clause string, value any) {
		args = append(args, value)
		where = append(where, fmt.Sprintf(clause, len(args)))
	}

	if filter.Action != "" {
		add("action = $%d", filter.Action)
	}
	if filter.EntityType != "" {
		add("entity_type = $%d", filter.EntityType)
	}
	if filter.EntityID != "" {
		add("entity_id = $%d", filter.EntityID)
	}
	if filter.ActorID != "" {
		add("actor_id = $%d", filter.ActorID)
	}
	if filter.From != nil {
		add("occurred_at >= $%d", *filter.From)
	}
	if filter.To != nil {
		add("occurred_at <= $%d", *filter.To)
	}

	return where, args
}

func whereClause(where []string) string {
	if len(where) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(where, " AND ")
}
