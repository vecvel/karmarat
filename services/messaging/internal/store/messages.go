package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/karmarat/platform/packages/goshared/events"
	"github.com/karmarat/platform/packages/goshared/idgen"
	"github.com/karmarat/platform/services/messaging/internal/domain"
)

const messageColumns = `
	m.id, m.conversation_id, m.company_id::text, m.candidate_account_id,
	m.sender_type::text, m.sender_account_id, m.body, m.attachments,
	m.sent_at, m.read_at`

func scanMessage(row pgx.Row) (domain.Message, error) {
	var m domain.Message
	var senderType string
	var attachments []domain.Attachment

	if err := row.Scan(
		&m.ID, &m.ConversationID, &m.CompanyID, &m.CandidateAccountID,
		&senderType, &m.SenderAccountID, &m.Body, &attachments,
		&m.SentAt, &m.ReadAt,
	); err != nil {
		return domain.Message{}, err
	}

	m.SenderType = domain.SenderType(senderType)
	if attachments == nil {
		attachments = []domain.Attachment{}
	}
	m.Attachments = attachments
	return m, nil
}

// NewMessage describes a message to append.
//
// SenderType and SenderAccountID are set from the verified principal by the
// caller; a request body never names its own sender.
type NewMessage struct {
	SenderType      domain.SenderType
	SenderAccountID string
	Body            string
	Attachments     []domain.Attachment
	// IdempotencyKey is the client's Idempotency-Key header, if it sent one.
	IdempotencyKey string
}

/* -------------------------------------------------------------------------- */
/* Reading a thread                                                           */
/* -------------------------------------------------------------------------- */

// ListMessages returns a page of a thread, newest first.
//
// Both owner columns are in the predicate even though the conversation was
// already resolved inside the caller's scope. The redundancy is the point: if a
// later change lets an unscoped conversation id reach this function, the query
// still cannot return another tenant's or another candidate's messages.
func (s *Store) ListMessages(
	ctx context.Context, conversationID, companyID, candidateAccountID string, page domain.Page,
) ([]domain.Message, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+messageColumns+`
		FROM messages m
		WHERE m.conversation_id = $1
		  AND m.company_id = $2
		  AND m.candidate_account_id = $3
		  AND ($4::boolean IS FALSE OR (m.sent_at, m.id) < ($5::timestamptz, $6))
		ORDER BY m.sent_at DESC, m.id DESC
		LIMIT $7`,
		conversationID, companyID, candidateAccountID,
		page.Cursor.Set(), cursorTime(page.Cursor.Set(), page.Cursor.At), page.Cursor.ID,
		page.Limit)
	if err != nil {
		return nil, fmt.Errorf("store: list messages: %w", err)
	}
	defer rows.Close()

	out := make([]domain.Message, 0, page.Limit)
	for rows.Next() {
		message, err := scanMessage(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan message: %w", err)
		}
		out = append(out, message)
	}
	return out, rows.Err()
}

/* -------------------------------------------------------------------------- */
/* Appending                                                                  */
/* -------------------------------------------------------------------------- */

// AppendMessage posts into an existing thread.
//
// The conversation must already have been resolved inside the caller's scope —
// that is where the tenant and participant checks happen — and it is passed in
// rather than re-loaded so this function cannot be called with an id whose
// ownership nobody established.
func (s *Store) AppendMessage(
	ctx context.Context, conversation domain.Conversation, in NewMessage,
) (domain.Message, bool, error) {
	var message domain.Message
	var replayed bool

	err := s.InTx(ctx, func(tx pgx.Tx) error {
		var err error
		message, replayed, err = s.appendMessageTx(ctx, tx, conversation, in)
		return err
	})
	if err != nil {
		return domain.Message{}, false, err
	}
	return message, replayed, nil
}

func (s *Store) appendMessageTx(
	ctx context.Context, tx pgx.Tx, conversation domain.Conversation, in NewMessage,
) (domain.Message, bool, error) {
	if in.IdempotencyKey != "" {
		// A retry after a timeout must not post twice. Looking first keeps the
		// happy path free of a caught constraint error, and the unique index
		// below still settles a genuine race.
		existing, err := s.findByIdempotencyKey(ctx, tx, conversation.ID, in.SenderAccountID, in.IdempotencyKey)
		if err == nil {
			return existing, true, nil
		}
		if !errors.Is(err, domain.ErrMessageNotFound) {
			return domain.Message{}, false, err
		}
	}

	// Aliased as `m` so the RETURNING list can be the same messageColumns every
	// read of a message uses.
	row := s.queryRow(ctx, tx, `
		INSERT INTO messages AS m (
			id, conversation_id, company_id, candidate_account_id,
			sender_type, sender_account_id, body, attachments, idempotency_key)
		VALUES ($1, $2, $3, $4, $5::message_sender_type, $6, $7, $8, $9)
		RETURNING `+messageColumns,
		idgen.New("msg"), conversation.ID, conversation.CompanyID, conversation.CandidateAccountID,
		string(in.SenderType), in.SenderAccountID, in.Body, attachmentsParam(in.Attachments),
		nullable(in.IdempotencyKey))

	created, err := scanMessage(row)
	if err != nil {
		if isUniqueViolation(err) {
			// Lost the race against a concurrent retry; the winner's row is the
			// answer both callers should get.
			existing, findErr := s.findByIdempotencyKey(ctx, tx, conversation.ID, in.SenderAccountID, in.IdempotencyKey)
			if findErr != nil {
				return domain.Message{}, false, findErr
			}
			return existing, true, nil
		}
		return domain.Message{}, false, fmt.Errorf("store: append message: %w", err)
	}

	// The recipient's unread counter moves, never the sender's: a recruiter
	// sending a message has not made their own inbox unread.
	recipient := in.SenderType.Opposite()
	if _, err := s.exec(ctx, tx, `
		UPDATE conversations
		SET last_activity_at = $3,
		    last_message_preview = $4,
		    last_message_sender = $5::message_sender_type,
		    company_unread_count = company_unread_count + CASE WHEN $6 = 'company' THEN 1 ELSE 0 END,
		    candidate_unread_count = candidate_unread_count + CASE WHEN $6 = 'candidate' THEN 1 ELSE 0 END
		WHERE id = $1 AND company_id = $2`,
		conversation.ID, conversation.CompanyID,
		created.SentAt, domain.Preview(created.Body), string(in.SenderType), string(recipient),
	); err != nil {
		return domain.Message{}, false, fmt.Errorf("store: update conversation activity: %w", err)
	}

	if err := s.enqueueEvent(ctx, tx, outboxEntry{
		Subject:   events.SubjectMessageSent,
		CompanyID: conversation.CompanyID,
		ActorID:   in.SenderAccountID,
		Payload:   messagePayload(conversation, created),
	}); err != nil {
		return domain.Message{}, false, err
	}

	return created, false, nil
}

func (s *Store) findByIdempotencyKey(
	ctx context.Context, tx pgx.Tx, conversationID, senderAccountID, key string,
) (domain.Message, error) {
	message, err := scanMessage(s.queryRow(ctx, tx, `
		SELECT `+messageColumns+`
		FROM messages m
		WHERE m.conversation_id = $1 AND m.sender_account_id = $2 AND m.idempotency_key = $3`,
		conversationID, senderAccountID, key))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Message{}, domain.ErrMessageNotFound
		}
		return domain.Message{}, fmt.Errorf("store: find message by idempotency key: %w", err)
	}
	return message, nil
}

/* -------------------------------------------------------------------------- */
/* Read receipts                                                              */
/* -------------------------------------------------------------------------- */

// MarkRead stamps the unread messages a side has now seen and clears its counter.
//
// reader is the side doing the reading, so the messages it touches are the ones
// from the *other* side. Marking your own messages read would be meaningless and
// would quietly break the recipient's badge.
func (s *Store) MarkRead(
	ctx context.Context, conversation domain.Conversation, reader domain.SenderType, readerAccountID string,
) (int64, error) {
	var affected int64

	err := s.InTx(ctx, func(tx pgx.Tx) error {
		tag, err := s.exec(ctx, tx, `
			UPDATE messages
			SET read_at = now()
			WHERE conversation_id = $1
			  AND company_id = $2
			  AND candidate_account_id = $3
			  AND sender_type = $4::message_sender_type
			  AND read_at IS NULL`,
			conversation.ID, conversation.CompanyID, conversation.CandidateAccountID,
			string(reader.Opposite()))
		if err != nil {
			return fmt.Errorf("store: mark messages read: %w", err)
		}
		affected = tag.RowsAffected()

		if _, err := s.exec(ctx, tx, `
			UPDATE conversations
			SET company_unread_count = CASE WHEN $3 = 'company' THEN 0 ELSE company_unread_count END,
			    candidate_unread_count = CASE WHEN $3 = 'candidate' THEN 0 ELSE candidate_unread_count END
			WHERE id = $1 AND company_id = $2`,
			conversation.ID, conversation.CompanyID, string(reader)); err != nil {
			return fmt.Errorf("store: clear unread counter: %w", err)
		}

		// A recruiter's own last-read stamp is per participant, so one colleague
		// opening a thread does not mark it read for the whole team.
		if reader == domain.SenderCompany && readerAccountID != "" {
			if _, err := s.exec(ctx, tx, `
				UPDATE conversation_participants
				SET last_read_at = now()
				WHERE conversation_id = $1 AND company_id = $2 AND account_id = $3`,
				conversation.ID, conversation.CompanyID, readerAccountID); err != nil {
				return fmt.Errorf("store: stamp participant read: %w", err)
			}
		}

		return nil
	})
	if err != nil {
		return 0, err
	}
	return affected, nil
}

/* -------------------------------------------------------------------------- */
/* Helpers                                                                    */
/* -------------------------------------------------------------------------- */

// attachmentsParam keeps a nil slice out of a NOT NULL jsonb column.
func attachmentsParam(attachments []domain.Attachment) any {
	if attachments == nil {
		return []domain.Attachment{}
	}
	return attachments
}

// messagePayload is the fact published when a message is sent.
//
// It carries a preview rather than the body. Notifications needs enough for an
// inbox line and an email teaser; putting private correspondence on a bus with
// thirty-day retention, readable by every consumer, would be a disclosure the
// sender never agreed to.
func messagePayload(c domain.Conversation, m domain.Message) map[string]any {
	return map[string]any{
		"messageId":          m.ID,
		"conversationId":     c.ID,
		"companyId":          c.CompanyID,
		"candidateAccountId": c.CandidateAccountID,
		"applicationId":      c.ApplicationID,
		"jobId":              c.JobID,
		"subject":            c.Subject,
		"senderType":         string(m.SenderType),
		"senderAccountId":    m.SenderAccountID,
		"preview":            domain.Preview(m.Body),
		"attachmentCount":    len(m.Attachments),
		"sentAt":             m.SentAt,
	}
}
