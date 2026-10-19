// Package store is the applications service's persistence layer.
//
// Every query that touches tenant-owned data takes the company identifier as an
// explicit argument and puts it in the WHERE clause, rather than loading a row
// and checking it afterwards. A load-then-check can be forgotten at the next
// call site; a predicate cannot, and it answers "not found" instead of
// confirming that another tenant's record exists.
package store

import (
	"context"
	"errors"
	"fmt"

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

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}

func (s *Store) queryRow(ctx context.Context, tx pgx.Tx, query string, args ...any) pgx.Row {
	if tx != nil {
		return tx.QueryRow(ctx, query, args...)
	}
	return s.pool.QueryRow(ctx, query, args...)
}

func (s *Store) query(ctx context.Context, tx pgx.Tx, query string, args ...any) (pgx.Rows, error) {
	if tx != nil {
		return tx.Query(ctx, query, args...)
	}
	return s.pool.Query(ctx, query, args...)
}

func (s *Store) exec(ctx context.Context, tx pgx.Tx, query string, args ...any) (pgconn.CommandTag, error) {
	if tx != nil {
		return tx.Exec(ctx, query, args...)
	}
	return s.pool.Exec(ctx, query, args...)
}

func nullable(v string) any {
	if v == "" {
		return nil
	}
	return v
}
