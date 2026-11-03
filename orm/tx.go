package orm

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jmoiron/sqlx"
)

// Executor is the part of *sql.DB and *sql.Tx a repository needs.
//
// It exists so a repository can run against a transaction without a second copy of the
// query builder: the only difference between "inside a transaction" and "not" is which
// executor the statements go to.
type Executor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

var _ Executor = (*DB)(nil)

// WithTx runs fn inside a transaction and commits when it returns nil.
//
// A panic rolls back and is re-raised: swallowing it would leave the caller's deferred
// cleanup believing the work succeeded. A rolled-back commit error is reported as the
// original error plus the rollback failure, so neither is lost.
func (db *DB) WithTx(ctx context.Context, fn func(ctx context.Context, tx *Tx) error) error {
	if fn == nil {
		return fmt.Errorf("orm: nil transaction function")
	}
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("orm: begin: %w", err)
	}

	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if err := fn(ctx, &Tx{tx: tx, dialect: db.Dialect()}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("orm: commit: %w", err)
	}
	committed = true
	return nil
}

// Tx is a transaction a repository can be bound to. It is valid only for the duration of
// the WithTx callback.
type Tx struct {
	tx      *sqlx.Tx
	dialect Dialect
}

var _ Executor = (*Tx)(nil)

// ExecContext implements Executor.
func (t *Tx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return t.tx.ExecContext(ctx, query, args...)
}

// QueryContext implements Executor.
func (t *Tx) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return t.tx.QueryContext(ctx, query, args...)
}

// QueryRowContext implements Executor.
func (t *Tx) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return t.tx.QueryRowContext(ctx, query, args...)
}

// Dialect returns the dialect the transaction runs on.
func (t *Tx) Dialect() Dialect { return t.dialect }

// Repo binds T to this transaction. The tenant guard, the delete markers and Preload all
// behave as they do outside a transaction, because they are the same repository code.
func (t *Tx) Repo[T any](table string, opts ...RepoOption) (*Repo[T], error) {
	if t == nil || t.tx == nil {
		return nil, fmt.Errorf("orm: nil transaction")
	}
	return newRepo[T](t, t.dialect, table, opts...)
}
