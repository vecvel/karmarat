package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/karmarat/platform/packages/goshared/events"
	"github.com/karmarat/platform/packages/goshared/idgen"
	"github.com/karmarat/platform/services/messaging/internal/domain"
)

// conversationColumns is the one projection every read of a thread uses, so a
// column added later cannot appear in one response shape and not another.
const conversationColumns = `
	c.id, c.company_id::text, c.candidate_account_id,
	coalesce(c.application_id, ''), coalesce(c.job_id, ''),
	c.subject, c.origin::text, coalesce(c.origin_ref, ''), c.opened_by_account_id,
	c.last_activity_at, c.last_message_preview, coalesce(c.last_message_sender::text, ''),
	c.company_unread_count, c.candidate_unread_count,
	c.closed_at, c.created_at, c.updated_at`

func scanConversation(row pgx.Row) (domain.Conversation, error) {
	var c domain.Conversation
	var origin, lastSender string

	if err := row.Scan(
		&c.ID, &c.CompanyID, &c.CandidateAccountID,
		&c.ApplicationID, &c.JobID,
		&c.Subject, &origin, &c.OriginRef, &c.OpenedByAccountID,
		&c.LastActivityAt, &c.LastMessagePreview, &lastSender,
		&c.CompanyUnread, &c.CandidateUnread,
		&c.ClosedAt, &c.CreatedAt, &c.UpdatedAt,
	); err != nil {
		return domain.Conversation{}, err
	}

	c.Origin = domain.Origin(origin)
	c.LastMessageSender = domain.SenderType(lastSender)
	return c, nil
}

/* -------------------------------------------------------------------------- */
/* Company side                                                               */
/* -------------------------------------------------------------------------- */

// CompanyScope is how much of a company's messaging a recruiter may see.
//
// It is derived from the principal's permissions by the caller and then decides
// which SQL runs — not which rows are filtered out afterwards. `messaging.read`
// joins through conversation_participants; `messaging.read_all` does not. A
// recruiter without the wide permission never has another recruiter's thread in
// a result set, so there is no later step that could forget to drop it.
type CompanyScope struct {
	// CompanyID is the tenant, always from the verified principal.
	CompanyID string
	// ActorAccountID is the recruiter making the request.
	ActorAccountID string
	// All is true when the principal holds messaging.read_all.
	All bool
}

// participantJoin is empty for a read_all scope and a join otherwise.
//
// $2 is always the acting recruiter, so the two variants take identical
// parameters and cannot be called with the wrong argument list.
func (s CompanyScope) participantJoin() string {
	if s.All {
		return ""
	}
	return `
		JOIN conversation_participants p
		  ON p.conversation_id = c.id
		 AND p.company_id = c.company_id
		 AND p.account_id = $2`
}

// actorPredicate keeps $2 typed when the participant join does not use it.
//
// Both scopes take the same parameter list on purpose — two variants with
// different argument orders is a bug waiting to be introduced — but Postgres
// cannot infer the type of a parameter no part of the statement mentions, and
// refuses the query outright. The wide scope therefore names the actor in a
// predicate that is always true: the cast is the point, not the comparison.
func (s CompanyScope) actorPredicate() string {
	if !s.All {
		return ""
	}
	return " AND $2::text IS NOT NULL"
}

// ListForCompany returns a page of the company's conversations.
func (s *Store) ListForCompany(
	ctx context.Context, scope CompanyScope, page domain.Page,
) ([]domain.Conversation, error) {
	query := `
		SELECT ` + conversationColumns + `
		FROM conversations c` + scope.participantJoin() + `
		WHERE c.company_id = $1` + scope.actorPredicate() + `
		  AND ($3::boolean IS FALSE OR (c.last_activity_at, c.id) < ($4::timestamptz, $5))
		ORDER BY c.last_activity_at DESC, c.id DESC
		LIMIT $6`

	rows, err := s.pool.Query(ctx, query,
		scope.CompanyID, scope.ActorAccountID,
		page.Cursor.Set(), cursorTime(page.Cursor.Set(), page.Cursor.At), page.Cursor.ID,
		page.Limit)
	if err != nil {
		return nil, fmt.Errorf("store: list conversations: %w", err)
	}
	defer rows.Close()

	return collectConversations(rows, page.Limit)
}

// FindForCompany resolves one conversation inside the caller's scope.
//
// A thread that exists but belongs to another recruiter is reported as absent
// rather than as forbidden, because "not yours" and "not there" must look the
// same to a caller probing ids.
func (s *Store) FindForCompany(ctx context.Context, scope CompanyScope, id string) (domain.Conversation, error) {
	return s.findForCompanyTx(ctx, nil, scope, id, false)
}

// LockForCompany is FindForCompany with a row lock, used by the writes that read
// the thread and then update its counters.
func (s *Store) LockForCompany(
	ctx context.Context, tx pgx.Tx, scope CompanyScope, id string,
) (domain.Conversation, error) {
	return s.findForCompanyTx(ctx, tx, scope, id, true)
}

func (s *Store) findForCompanyTx(
	ctx context.Context, tx pgx.Tx, scope CompanyScope, id string, lock bool,
) (domain.Conversation, error) {
	suffix := ""
	if lock {
		// Only the conversation row is locked; locking the participant join would
		// block a colleague joining an unrelated thread.
		suffix = " FOR UPDATE OF c"
	}

	query := `
		SELECT ` + conversationColumns + `
		FROM conversations c` + scope.participantJoin() + `
		WHERE c.company_id = $1 AND c.id = $3` + scope.actorPredicate() + suffix

	conversation, err := scanConversation(s.queryRow(ctx, tx, query, scope.CompanyID, scope.ActorAccountID, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Conversation{}, domain.ErrConversationNotFound
		}
		return domain.Conversation{}, fmt.Errorf("store: find conversation: %w", err)
	}
	return conversation, nil
}

/* -------------------------------------------------------------------------- */
/* Candidate side                                                             */
/* -------------------------------------------------------------------------- */

// ListForCandidate returns a page of the candidate's own conversations.
//
// The filter is the authenticated account id and nothing else. A candidate is
// not tenant-scoped — they may be talking to a dozen companies — so company_id
// deliberately does not appear here.
func (s *Store) ListForCandidate(
	ctx context.Context, accountID string, page domain.Page,
) ([]domain.Conversation, error) {
	query := `
		SELECT ` + conversationColumns + `
		FROM conversations c
		WHERE c.candidate_account_id = $1
		  AND ($2::boolean IS FALSE OR (c.last_activity_at, c.id) < ($3::timestamptz, $4))
		ORDER BY c.last_activity_at DESC, c.id DESC
		LIMIT $5`

	rows, err := s.pool.Query(ctx, query,
		accountID,
		page.Cursor.Set(), cursorTime(page.Cursor.Set(), page.Cursor.At), page.Cursor.ID,
		page.Limit)
	if err != nil {
		return nil, fmt.Errorf("store: list candidate conversations: %w", err)
	}
	defer rows.Close()

	return collectConversations(rows, page.Limit)
}

// FindForCandidate resolves one of the candidate's own conversations.
func (s *Store) FindForCandidate(ctx context.Context, accountID, id string) (domain.Conversation, error) {
	return s.findForCandidateTx(ctx, nil, accountID, id, false)
}

// LockForCandidate is FindForCandidate with a row lock.
func (s *Store) LockForCandidate(
	ctx context.Context, tx pgx.Tx, accountID, id string,
) (domain.Conversation, error) {
	return s.findForCandidateTx(ctx, tx, accountID, id, true)
}

func (s *Store) findForCandidateTx(
	ctx context.Context, tx pgx.Tx, accountID, id string, lock bool,
) (domain.Conversation, error) {
	query := `
		SELECT ` + conversationColumns + `
		FROM conversations c
		WHERE c.candidate_account_id = $1 AND c.id = $2`
	if lock {
		query += " FOR UPDATE"
	}

	conversation, err := scanConversation(s.queryRow(ctx, tx, query, accountID, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Conversation{}, domain.ErrConversationNotFound
		}
		return domain.Conversation{}, fmt.Errorf("store: find candidate conversation: %w", err)
	}
	return conversation, nil
}

/* -------------------------------------------------------------------------- */
/* Opening                                                                    */
/* -------------------------------------------------------------------------- */

// NewConversation describes a thread to open.
//
// CompanyID and OpenedByAccountID come from the verified principal at the call
// site; nothing in this struct may be populated from a request body except the
// candidate, the context ids and the subject.
type NewConversation struct {
	CompanyID          string
	CandidateAccountID string
	ApplicationID      string
	JobID              string
	Subject            string
	Origin             domain.Origin
	// OriginRef is the id of the fact that caused an automatic open. It makes a
	// redelivered event a no-op instead of a second thread.
	OriginRef         string
	OpenedByAccountID string
	// FirstMessage is posted in the same transaction when present, so a thread is
	// never created empty and then orphaned by a failure on the next request.
	FirstMessage *NewMessage
}

// OpenConversation creates a thread and, optionally, its first message.
//
// The whole operation is one transaction: the conversation, the participant row,
// the first message and the outbox events either all land or none do.
func (s *Store) OpenConversation(ctx context.Context, in NewConversation) (domain.Conversation, domain.Message, error) {
	var conversation domain.Conversation
	var message domain.Message

	err := s.InTx(ctx, func(tx pgx.Tx) error {
		id := idgen.New("conv")

		// Aliased as `c` because conversationColumns is written against that
		// alias. One projection for the INSERT's RETURNING and for every
		// SELECT is what keeps a column added later from appearing in one
		// response shape and not another.
		row := tx.QueryRow(ctx, `
			INSERT INTO conversations AS c (
				id, company_id, candidate_account_id, application_id, job_id,
				subject, origin, origin_ref, opened_by_account_id, last_activity_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7::conversation_origin, $8, $9, now())
			RETURNING `+conversationColumns,
			id, in.CompanyID, in.CandidateAccountID,
			nullable(in.ApplicationID), nullable(in.JobID),
			in.Subject, string(in.Origin), nullable(in.OriginRef), in.OpenedByAccountID)

		created, err := scanConversation(row)
		if err != nil {
			if isUniqueViolation(err) {
				return domain.ErrConversationExists
			}
			return fmt.Errorf("store: open conversation: %w", err)
		}
		conversation = created

		// The opener is a participant from the start; otherwise a recruiter with
		// only `messaging.read` could not see the thread they just opened.
		if in.OpenedByAccountID != "" {
			if err := s.addParticipantTx(ctx, tx, conversation.ID, in.CompanyID, in.OpenedByAccountID); err != nil {
				return err
			}
		}

		if err := s.enqueueEvent(ctx, tx, outboxEntry{
			Subject:   events.SubjectConversationOpen,
			CompanyID: conversation.CompanyID,
			ActorID:   in.OpenedByAccountID,
			Payload:   conversationPayload(conversation),
		}); err != nil {
			return err
		}

		if in.FirstMessage == nil {
			return nil
		}

		posted, _, err := s.appendMessageTx(ctx, tx, conversation, *in.FirstMessage)
		if err != nil {
			return err
		}
		message = posted

		// Re-read so the caller's response carries the counters and activity
		// stamp the message just moved, rather than the values from the INSERT.
		refreshed, err := scanConversation(tx.QueryRow(ctx,
			`SELECT `+conversationColumns+` FROM conversations c WHERE c.id = $1 AND c.company_id = $2`,
			conversation.ID, in.CompanyID))
		if err != nil {
			return fmt.Errorf("store: reload conversation: %w", err)
		}
		conversation = refreshed
		return nil
	})
	if err != nil {
		return domain.Conversation{}, domain.Message{}, err
	}

	return conversation, message, nil
}

// FindExisting resolves the thread that blocked an open, so the caller's 409 can
// point at it instead of leaving a client stuck.
//
// Scoped to the tenant: the conflict is only ever with the company's own thread.
func (s *Store) FindExisting(
	ctx context.Context, companyID, candidateAccountID, applicationID string,
) (domain.Conversation, error) {
	conversation, err := scanConversation(s.pool.QueryRow(ctx, `
		SELECT `+conversationColumns+`
		FROM conversations c
		WHERE c.company_id = $1
		  AND c.candidate_account_id = $2
		  AND coalesce(c.application_id, '') = $3`,
		companyID, candidateAccountID, applicationID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Conversation{}, domain.ErrConversationNotFound
		}
		return domain.Conversation{}, fmt.Errorf("store: find existing conversation: %w", err)
	}
	return conversation, nil
}

// AddParticipant records a recruiter as a party to a thread.
func (s *Store) AddParticipant(ctx context.Context, tx pgx.Tx, conversationID, companyID, accountID string) error {
	return s.addParticipantTx(ctx, tx, conversationID, companyID, accountID)
}

func (s *Store) addParticipantTx(ctx context.Context, tx pgx.Tx, conversationID, companyID, accountID string) error {
	_, err := s.exec(ctx, tx, `
		INSERT INTO conversation_participants (conversation_id, company_id, account_id)
		VALUES ($1, $2, $3)
		ON CONFLICT (conversation_id, account_id) DO NOTHING`,
		conversationID, companyID, accountID)
	if err != nil {
		return fmt.Errorf("store: add participant: %w", err)
	}
	return nil
}

/* -------------------------------------------------------------------------- */
/* Rate-limit backstop                                                        */
/* -------------------------------------------------------------------------- */

// CountConversationsOpenedSince counts a company's outbound threads in a window.
//
// Redis carries the fast path, but this is the number that actually binds: a
// cache outage must not turn the daily cap off, because an opened conversation
// is a message in a stranger's inbox and cannot be taken back.
func (s *Store) CountConversationsOpenedSince(ctx context.Context, companyID string, since time.Time) (int, error) {
	var count int
	err := s.pool.QueryRow(ctx, `
		SELECT count(*)
		FROM conversations
		WHERE company_id = $1
		  AND created_at >= $2
		  AND origin <> 'application'`,
		companyID, since).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("store: count opened conversations: %w", err)
	}
	return count, nil
}

/* -------------------------------------------------------------------------- */
/* Helpers                                                                    */
/* -------------------------------------------------------------------------- */

func collectConversations(rows pgx.Rows, limit int) ([]domain.Conversation, error) {
	out := make([]domain.Conversation, 0, limit)
	for rows.Next() {
		conversation, err := scanConversation(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan conversation: %w", err)
		}
		out = append(out, conversation)
	}
	return out, rows.Err()
}

// conversationPayload is the fact published when a thread opens.
//
// It carries identifiers and the subject, never the message body: notifications
// needs to know a thread exists and who it belongs to, and the correspondence
// itself stays in the database this service owns.
func conversationPayload(c domain.Conversation) map[string]any {
	return map[string]any{
		"conversationId":     c.ID,
		"companyId":          c.CompanyID,
		"candidateAccountId": c.CandidateAccountID,
		"applicationId":      c.ApplicationID,
		"jobId":              c.JobID,
		"subject":            c.Subject,
		"origin":             string(c.Origin),
		"openedByAccountId":  c.OpenedByAccountID,
		"openedAt":           c.CreatedAt,
	}
}
