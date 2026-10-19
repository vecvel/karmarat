// Package store is the messaging service's persistence layer.
//
// Two ownership rules are enforced here rather than in handlers, because a
// handler is where they get forgotten:
//
//	company side    every statement carries "AND company_id = $n"
//	candidate side  every statement carries "AND candidate_account_id = $n"
//
// Both are written as predicates rather than as a load followed by a comparison.
// A forgotten predicate then returns nothing instead of returning somebody
// else's thread, and the failure shows up as an empty inbox in a test rather
// than as a breach in production.
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
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

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// nullable turns an empty string into a SQL NULL.
func nullable(v string) any {
	if v == "" {
		return nil
	}
	return v
}

// nullableUUID keeps an empty tenant out of a uuid column.
func nullableUUID(v string) any {
	if v == "" {
		return nil
	}
	return v
}

// cursorTime returns the timestamp a keyset predicate compares against.
//
// It always yields a concrete value so the predicate can be one parameterised
// expression guarded by a companion boolean, rather than two query strings that
// could drift apart.
func cursorTime(set bool, at time.Time) any {
	if !set {
		// Any value works because the companion boolean parameter disables the
		// comparison; a concrete timestamp keeps the parameter's type unambiguous.
		return time.Unix(0, 0).UTC()
	}
	return at.UTC()
}
