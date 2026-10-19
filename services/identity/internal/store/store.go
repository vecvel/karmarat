// Package store is the identity service's persistence layer.
//
// Every query that touches tenant-owned data takes the company identifier as an
// explicit argument rather than reading it from ambient state, so a missing
// tenant filter is a compile-time omission rather than a silent leak.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/karmarat/platform/packages/goshared/idgen"
	"github.com/karmarat/platform/services/identity/internal/domain"
)

// Store owns the connection pool.
type Store struct {
	pool *pgxpool.Pool
}

// New builds a store over an existing pool.
func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Pool exposes the underlying pool for health checks.
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

// InTx runs fn inside a transaction, rolling back on error.
func (s *Store) InTx(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("store: begin transaction: %w", err)
	}

	if err := fn(tx); err != nil {
		// The rollback must not be cancelled along with the request.
		_ = tx.Rollback(context.WithoutCancel(ctx))
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("store: commit transaction: %w", err)
	}
	return nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// isUniqueViolationOn reports a unique violation of one named constraint, so a
// caller can map that one to a meaningful error without claiming every possible
// collision on the statement means the same thing.
func isUniqueViolationOn(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == constraint
}

/* -------------------------------------------------------------------------- */
/* Accounts                                                                   */
/* -------------------------------------------------------------------------- */

const accountColumns = `
	id, realm, email, coalesce(password_hash, ''), full_name, status,
	email_verified_at, failed_login_count, locked_until,
	last_login_at, coalesce(last_login_ip, ''), created_at, updated_at`

func scanAccount(row pgx.Row) (domain.Account, error) {
	var a domain.Account
	err := row.Scan(
		&a.ID, &a.Realm, &a.Email, &a.PasswordHash, &a.FullName, &a.Status,
		&a.EmailVerifiedAt, &a.FailedLoginCount, &a.LockedUntil,
		&a.LastLoginAt, &a.LastLoginIP, &a.CreatedAt, &a.UpdatedAt,
	)
	return a, err
}

// CreateAccountInput describes a new login identity.
type CreateAccountInput struct {
	Realm        domain.Realm
	Email        string
	PasswordHash string
	FullName     string
	Status       domain.AccountStatus
}

// CreateAccount inserts an account, or reports that the email is taken.
func (s *Store) CreateAccount(ctx context.Context, tx pgx.Tx, in CreateAccountInput) (domain.Account, error) {
	id := idgen.New("acc")
	email := strings.ToLower(strings.TrimSpace(in.Email))

	query := `
		INSERT INTO accounts (id, realm, email, password_hash, full_name, status)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING ` + accountColumns

	row := s.queryRow(ctx, tx, query, id, in.Realm, email, nullable(in.PasswordHash), in.FullName, in.Status)

	account, err := scanAccount(row)
	if err != nil {
		if isUniqueViolation(err) {
			return domain.Account{}, domain.ErrEmailTaken
		}
		return domain.Account{}, fmt.Errorf("store: create account: %w", err)
	}
	return account, nil
}

// FindAccountByEmail looks an account up within its realm.
func (s *Store) FindAccountByEmail(ctx context.Context, realm domain.Realm, email string) (domain.Account, error) {
	query := `SELECT ` + accountColumns + ` FROM accounts WHERE realm = $1 AND lower(email) = lower($2)`

	account, err := scanAccount(s.pool.QueryRow(ctx, query, realm, strings.TrimSpace(email)))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Account{}, domain.ErrAccountNotFound
		}
		return domain.Account{}, fmt.Errorf("store: find account by email: %w", err)
	}
	return account, nil
}

// FindAccountByID looks an account up by identifier.
func (s *Store) FindAccountByID(ctx context.Context, id string) (domain.Account, error) {
	query := `SELECT ` + accountColumns + ` FROM accounts WHERE id = $1`

	account, err := scanAccount(s.pool.QueryRow(ctx, query, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Account{}, domain.ErrAccountNotFound
		}
		return domain.Account{}, fmt.Errorf("store: find account by id: %w", err)
	}
	return account, nil
}

// RecordLoginSuccess clears the lockout counters and stamps the login.
func (s *Store) RecordLoginSuccess(ctx context.Context, accountID, ip string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE accounts
		SET failed_login_count = 0,
		    locked_until = NULL,
		    last_login_at = now(),
		    last_login_ip = $2
		WHERE id = $1`, accountID, nullable(ip))
	if err != nil {
		return fmt.Errorf("store: record login success: %w", err)
	}
	return nil
}

// RecordLoginFailure increments the counter and locks the account at the threshold.
//
// The counter and the lock are updated in one statement so concurrent attempts
// cannot interleave and slip past the threshold.
func (s *Store) RecordLoginFailure(ctx context.Context, accountID string, threshold int, lockFor time.Duration) (locked bool, err error) {
	row := s.pool.QueryRow(ctx, `
		UPDATE accounts
		SET failed_login_count = failed_login_count + 1,
		    locked_until = CASE
		        WHEN failed_login_count + 1 >= $2 THEN now() + $3::interval
		        ELSE locked_until
		    END
		WHERE id = $1
		RETURNING locked_until IS NOT NULL AND locked_until > now()`,
		accountID, threshold, lockFor.String())

	if err := row.Scan(&locked); err != nil {
		return false, fmt.Errorf("store: record login failure: %w", err)
	}
	return locked, nil
}

// UpdateAccountPassword sets a new password hash.
func (s *Store) UpdateAccountPassword(ctx context.Context, tx pgx.Tx, accountID, passwordHash string) error {
	_, err := s.exec(ctx, tx, `
		UPDATE accounts
		SET password_hash = $2, failed_login_count = 0, locked_until = NULL
		WHERE id = $1`, accountID, passwordHash)
	if err != nil {
		return fmt.Errorf("store: update password: %w", err)
	}
	return nil
}

// UpdateAccountStatus changes an account's lifecycle state.
func (s *Store) UpdateAccountStatus(ctx context.Context, accountID string, status domain.AccountStatus) error {
	_, err := s.pool.Exec(ctx, `UPDATE accounts SET status = $2 WHERE id = $1`, accountID, status)
	if err != nil {
		return fmt.Errorf("store: update account status: %w", err)
	}
	return nil
}

// MarkEmailVerified records that an address has been confirmed and activates
// the account if it was still pending.
func (s *Store) MarkEmailVerified(ctx context.Context, tx pgx.Tx, accountID string) error {
	_, err := s.exec(ctx, tx, `
		UPDATE accounts
		SET email_verified_at = now(),
		    status = CASE WHEN status = 'pending' THEN 'active'::account_status ELSE status END
		WHERE id = $1`, accountID)
	if err != nil {
		return fmt.Errorf("store: mark email verified: %w", err)
	}
	return nil
}

/* -------------------------------------------------------------------------- */
/* Companies (projection)                                                     */
/* -------------------------------------------------------------------------- */

const companyColumns = `
	id, slug, name, state, subscription_state, subscription_expires_at,
	entitlements, updated_at`

func scanCompany(row pgx.Row) (domain.Company, error) {
	var c domain.Company
	var entitlements []byte

	if err := row.Scan(
		&c.ID, &c.Slug, &c.Name, &c.State, &c.SubscriptionState,
		&c.SubscriptionExpiresAt, &entitlements, &c.UpdatedAt,
	); err != nil {
		return domain.Company{}, err
	}

	if len(entitlements) > 0 {
		_ = json.Unmarshal(entitlements, &c.Entitlements)
	}
	return c, nil
}

// UpsertCompany writes the projection of a company.
//
// Called from event handlers, so it must be idempotent: the same event replayed
// must leave the row in the same state. A nil tx runs on the pool.
//
// The three subscription columns are INSERT-ONLY. On conflict they are left
// exactly as they are, and UpdateCompanySubscription is the only thing that
// writes them. That is not tidiness — provisioning is an upsert on the company
// id, and its caller has no idea what a subscription is, so carrying the struct
// field through to the update meant a retried provisioning call (precisely what
// a client does after a timeout) silently cancelled a paying customer's
// subscription and shut everyone but the owner out of the portal.
//
// Doing it in the statement rather than by reading the row first also removes
// the window between that read and this write.
func (s *Store) UpsertCompany(ctx context.Context, tx pgx.Tx, c domain.Company) error {
	entitlements, err := json.Marshal(orEmptyMap(c.Entitlements))
	if err != nil {
		return fmt.Errorf("store: marshal entitlements: %w", err)
	}

	_, err = s.exec(ctx, tx, `
		INSERT INTO companies (id, slug, name, state, subscription_state,
		                       subscription_expires_at, entitlements, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, now())
		ON CONFLICT (id) DO UPDATE SET
			slug = EXCLUDED.slug,
			name = EXCLUDED.name,
			state = EXCLUDED.state,
			updated_at = now()`,
		c.ID, strings.ToLower(c.Slug), c.Name, c.State, c.SubscriptionState,
		c.SubscriptionExpiresAt, entitlements)
	if err != nil {
		// Named rather than inferred from the error code alone: the statement
		// already absorbs a conflict on the primary key, so today the slug index
		// is the only one left — but "today" is what makes a bare code check age
		// badly the next time somebody adds a unique index here.
		if isUniqueViolationOn(err, "companies_slug_idx") {
			return domain.ErrSlugTaken
		}
		return fmt.Errorf("store: upsert company: %w", err)
	}
	return nil
}

// UpdateCompanySubscription applies a billing change to the projection.
func (s *Store) UpdateCompanySubscription(
	ctx context.Context,
	companyID string,
	state domain.SubscriptionState,
	expiresAt *time.Time,
	entitlements map[string]any,
) error {
	encoded, err := json.Marshal(orEmptyMap(entitlements))
	if err != nil {
		return fmt.Errorf("store: marshal entitlements: %w", err)
	}

	_, err = s.pool.Exec(ctx, `
		UPDATE companies
		SET subscription_state = $2,
		    subscription_expires_at = $3,
		    entitlements = $4,
		    updated_at = now()
		WHERE id = $1`, companyID, state, expiresAt, encoded)
	if err != nil {
		return fmt.Errorf("store: update company subscription: %w", err)
	}
	return nil
}

// FindCompanyBySlug resolves a portal hostname onto a tenant.
func (s *Store) FindCompanyBySlug(ctx context.Context, slug string) (domain.Company, error) {
	query := `SELECT ` + companyColumns + ` FROM companies WHERE lower(slug) = lower($1)`

	company, err := scanCompany(s.pool.QueryRow(ctx, query, strings.TrimSpace(slug)))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Company{}, domain.ErrCompanyNotFound
		}
		return domain.Company{}, fmt.Errorf("store: find company by slug: %w", err)
	}
	return company, nil
}

// FindCompanyByID resolves a tenant by identifier.
func (s *Store) FindCompanyByID(ctx context.Context, id string) (domain.Company, error) {
	query := `SELECT ` + companyColumns + ` FROM companies WHERE id = $1`

	company, err := scanCompany(s.pool.QueryRow(ctx, query, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Company{}, domain.ErrCompanyNotFound
		}
		return domain.Company{}, fmt.Errorf("store: find company by id: %w", err)
	}
	return company, nil
}

/* -------------------------------------------------------------------------- */
/* Helpers                                                                    */
/* -------------------------------------------------------------------------- */

func (s *Store) queryRow(ctx context.Context, tx pgx.Tx, query string, args ...any) pgx.Row {
	if tx != nil {
		return tx.QueryRow(ctx, query, args...)
	}
	return s.pool.QueryRow(ctx, query, args...)
}

func (s *Store) exec(ctx context.Context, tx pgx.Tx, query string, args ...any) (pgconn.CommandTag, error) {
	if tx != nil {
		return tx.Exec(ctx, query, args...)
	}
	return s.pool.Exec(ctx, query, args...)
}

func (s *Store) query(ctx context.Context, tx pgx.Tx, query string, args ...any) (pgx.Rows, error) {
	if tx != nil {
		return tx.Query(ctx, query, args...)
	}
	return s.pool.Query(ctx, query, args...)
}

func nullable(v string) any {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return v
}

func orEmptyMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}
