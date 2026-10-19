package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/karmarat/platform/packages/goshared/idgen"
	"github.com/karmarat/platform/services/identity/internal/domain"
)

/* -------------------------------------------------------------------------- */
/* Sessions                                                                   */
/* -------------------------------------------------------------------------- */

// CreateSessionInput describes a new refresh-token session.
type CreateSessionInput struct {
	AccountID        string
	MembershipID     string
	CompanyID        string
	RefreshTokenHash string
	IPAddress        string
	UserAgent        string
	ExpiresAt        time.Time
}

// CreateSession records a session.
func (s *Store) CreateSession(ctx context.Context, tx pgx.Tx, in CreateSessionInput) (domain.Session, error) {
	id := idgen.New("ses")

	row := s.queryRow(ctx, tx, `
		INSERT INTO sessions (id, account_id, membership_id, company_id,
		                      refresh_token_hash, ip_address, user_agent, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, account_id, coalesce(membership_id, ''), coalesce(company_id, ''),
		          expires_at, revoked_at, coalesce(ip_address, ''), coalesce(user_agent, ''),
		          created_at, last_used_at`,
		id, in.AccountID, nullable(in.MembershipID), nullable(in.CompanyID),
		in.RefreshTokenHash, nullable(in.IPAddress), nullable(in.UserAgent), in.ExpiresAt)

	return scanSession(row)
}

func scanSession(row pgx.Row) (domain.Session, error) {
	var s domain.Session
	if err := row.Scan(&s.ID, &s.AccountID, &s.MembershipID, &s.CompanyID,
		&s.ExpiresAt, &s.RevokedAt, &s.IPAddress, &s.UserAgent,
		&s.CreatedAt, &s.LastUsedAt); err != nil {
		return domain.Session{}, err
	}
	return s, nil
}

// SessionByRefreshHash looks a session up by the hash of its refresh token.
func (s *Store) SessionByRefreshHash(ctx context.Context, tx pgx.Tx, hash string) (domain.Session, error) {
	row := s.queryRow(ctx, tx, `
		SELECT id, account_id, coalesce(membership_id, ''), coalesce(company_id, ''),
		       expires_at, revoked_at, coalesce(ip_address, ''), coalesce(user_agent, ''),
		       created_at, last_used_at
		FROM sessions WHERE refresh_token_hash = $1`, hash)

	session, err := scanSession(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Session{}, domain.ErrSessionNotFound
		}
		return domain.Session{}, fmt.Errorf("store: find session: %w", err)
	}
	return session, nil
}

// RotateSession revokes the presented session and links it to its replacement.
//
// Rotation-on-use with a recorded successor is what makes refresh-token theft
// detectable: if an old token is presented again, its `rotated_to` is already
// set, which means someone is replaying a token that was used.
func (s *Store) RotateSession(ctx context.Context, tx pgx.Tx, oldSessionID, newSessionID string) error {
	_, err := s.exec(ctx, tx, `
		UPDATE sessions
		SET revoked_at = now(), revoked_reason = 'rotated', rotated_to = $2
		WHERE id = $1`, oldSessionID, newSessionID)
	if err != nil {
		return fmt.Errorf("store: rotate session: %w", err)
	}
	return nil
}

// WasRotated reports whether a session has already been exchanged.
func (s *Store) WasRotated(ctx context.Context, tx pgx.Tx, sessionID string) (bool, error) {
	var rotated bool
	err := s.queryRow(ctx, tx,
		`SELECT rotated_to IS NOT NULL FROM sessions WHERE id = $1`, sessionID).Scan(&rotated)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, domain.ErrSessionNotFound
		}
		return false, fmt.Errorf("store: check session rotation: %w", err)
	}
	return rotated, nil
}

// TouchSession records that a session was just used.
func (s *Store) TouchSession(ctx context.Context, sessionID string) error {
	_, err := s.pool.Exec(ctx, `UPDATE sessions SET last_used_at = now() WHERE id = $1`, sessionID)
	if err != nil {
		return fmt.Errorf("store: touch session: %w", err)
	}
	return nil
}

// RevokeSession ends one session.
func (s *Store) RevokeSession(ctx context.Context, sessionID, reason string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE sessions SET revoked_at = now(), revoked_reason = $2
		WHERE id = $1 AND revoked_at IS NULL`, sessionID, reason)
	if err != nil {
		return fmt.Errorf("store: revoke session: %w", err)
	}
	return nil
}

// RevokeAccountSessions ends every live session for an account.
//
// Called on password reset, suspension, and suspected token replay — the point
// where a short-lived access token is not enough and access must end now.
func (s *Store) RevokeAccountSessions(ctx context.Context, tx pgx.Tx, accountID, reason string) (int64, error) {
	tag, err := s.exec(ctx, tx, `
		UPDATE sessions SET revoked_at = now(), revoked_reason = $2
		WHERE account_id = $1 AND revoked_at IS NULL`, accountID, reason)
	if err != nil {
		return 0, fmt.Errorf("store: revoke account sessions: %w", err)
	}
	return tag.RowsAffected(), nil
}

// RevokeCompanySessions ends every live session inside one company.
func (s *Store) RevokeCompanySessions(ctx context.Context, companyID, reason string) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE sessions SET revoked_at = now(), revoked_reason = $2
		WHERE company_id = $1 AND revoked_at IS NULL`, companyID, reason)
	if err != nil {
		return 0, fmt.Errorf("store: revoke company sessions: %w", err)
	}
	return tag.RowsAffected(), nil
}

// ListAccountSessions returns an account's live sessions, newest first.
func (s *Store) ListAccountSessions(ctx context.Context, accountID string) ([]domain.Session, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, account_id, coalesce(membership_id, ''), coalesce(company_id, ''),
		       expires_at, revoked_at, coalesce(ip_address, ''), coalesce(user_agent, ''),
		       created_at, last_used_at
		FROM sessions
		WHERE account_id = $1 AND revoked_at IS NULL AND expires_at > now()
		ORDER BY last_used_at DESC`, accountID)
	if err != nil {
		return nil, fmt.Errorf("store: list sessions: %w", err)
	}
	defer rows.Close()

	var sessions []domain.Session
	for rows.Next() {
		session, err := scanSession(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan session: %w", err)
		}
		sessions = append(sessions, session)
	}
	return sessions, rows.Err()
}

// PurgeExpiredSessions deletes sessions that expired long ago.
func (s *Store) PurgeExpiredSessions(ctx context.Context, olderThan time.Duration) (int64, error) {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM sessions WHERE expires_at < now() - $1::interval`, olderThan.String())
	if err != nil {
		return 0, fmt.Errorf("store: purge expired sessions: %w", err)
	}
	return tag.RowsAffected(), nil
}

/* -------------------------------------------------------------------------- */
/* One-time tokens                                                            */
/* -------------------------------------------------------------------------- */

// TokenPurpose is why a one-time token was issued.
type TokenPurpose string

const (
	PurposeEmailVerification TokenPurpose = "email_verification"
	PurposePasswordReset     TokenPurpose = "password_reset"
	PurposeInvitation        TokenPurpose = "invitation"
)

// CreateOneTimeToken stores the hash of a single-use token.
func (s *Store) CreateOneTimeToken(
	ctx context.Context, tx pgx.Tx,
	accountID string, purpose TokenPurpose, tokenHash string,
	context map[string]any, expiresAt time.Time,
) (string, error) {
	id := idgen.New("ott")

	encoded, err := json.Marshal(orEmptyMap(context))
	if err != nil {
		return "", fmt.Errorf("store: marshal token context: %w", err)
	}

	// Only the newest token of a purpose should work, so older ones are consumed.
	if _, err := s.exec(ctx, tx, `
		UPDATE one_time_tokens SET consumed_at = now()
		WHERE account_id = $1 AND purpose = $2 AND consumed_at IS NULL`,
		accountID, purpose); err != nil {
		return "", fmt.Errorf("store: invalidate previous tokens: %w", err)
	}

	if _, err := s.exec(ctx, tx, `
		INSERT INTO one_time_tokens (id, account_id, purpose, token_hash, context, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		id, accountID, purpose, tokenHash, encoded, expiresAt); err != nil {
		return "", fmt.Errorf("store: create one-time token: %w", err)
	}

	return id, nil
}

// OneTimeToken is a stored single-use token.
type OneTimeToken struct {
	ID        string
	AccountID string
	Purpose   TokenPurpose
	Context   map[string]any
	ExpiresAt time.Time
}

// ConsumeOneTimeToken validates and marks a token as used in one statement.
//
// Doing the check and the consume atomically is what prevents two concurrent
// requests from both redeeming the same reset link.
func (s *Store) ConsumeOneTimeToken(ctx context.Context, tx pgx.Tx, purpose TokenPurpose, tokenHash string) (OneTimeToken, error) {
	row := s.queryRow(ctx, tx, `
		UPDATE one_time_tokens
		SET consumed_at = now()
		WHERE token_hash = $1
		  AND purpose = $2
		  AND consumed_at IS NULL
		  AND expires_at > now()
		RETURNING id, account_id, purpose, context, expires_at`, tokenHash, purpose)

	var token OneTimeToken
	var context []byte

	if err := row.Scan(&token.ID, &token.AccountID, &token.Purpose, &context, &token.ExpiresAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return OneTimeToken{}, domain.ErrTokenInvalid
		}
		return OneTimeToken{}, fmt.Errorf("store: consume one-time token: %w", err)
	}

	if len(context) > 0 {
		_ = json.Unmarshal(context, &token.Context)
	}
	return token, nil
}

/* -------------------------------------------------------------------------- */
/* Authentication audit trail                                                 */
/* -------------------------------------------------------------------------- */

// AuthEvent is a recorded authentication action.
type AuthEvent struct {
	AccountID string
	CompanyID string
	Event     string
	IPAddress string
	UserAgent string
	Metadata  map[string]any
}

// RecordAuthEvent appends to the authentication audit trail.
//
// Auditing must never fail the operation it is describing, so the error is
// returned for logging rather than propagated to the caller's flow.
func (s *Store) RecordAuthEvent(ctx context.Context, event AuthEvent) error {
	metadata, err := json.Marshal(orEmptyMap(event.Metadata))
	if err != nil {
		return fmt.Errorf("store: marshal auth event metadata: %w", err)
	}

	_, err = s.pool.Exec(ctx, `
		INSERT INTO auth_events (id, account_id, company_id, event, ip_address, user_agent, metadata)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		idgen.New("ae"), nullable(event.AccountID), nullable(event.CompanyID),
		event.Event, nullable(event.IPAddress), nullable(event.UserAgent), metadata)
	if err != nil {
		return fmt.Errorf("store: record auth event: %w", err)
	}
	return nil
}
