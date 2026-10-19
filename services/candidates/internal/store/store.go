// Package store is the candidates service's persistence layer.
//
// Two ownership rules are enforced here rather than in handlers, because a
// handler is where they get forgotten:
//
//	company data   every statement carries "AND company_id = $n"
//	candidate data every statement carries "AND account_id = $n"
//
// Both are written as predicates rather than as a load followed by a comparison,
// so a missing check is a query that returns nothing instead of a query that
// returns somebody else's row.
package store

import (
	"context"
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

func (s *Store) exec(ctx context.Context, tx pgx.Tx, query string, args ...any) (pgconn.CommandTag, error) {
	if tx != nil {
		return tx.Exec(ctx, query, args...)
	}
	return s.pool.Exec(ctx, query, args...)
}

// nullable turns an empty string into a SQL NULL.
func nullable(v string) any {
	if v == "" {
		return nil
	}
	return v
}

// orEmpty normalises a nil slice so a NOT NULL array column always receives a
// value and a JSON response never carries `null` where a list is expected.
func orEmpty(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
