// Package tx is the only way to open a transaction. The transaction travels in
// the context, so a use case can call several repositories (even from other
// modules, through their interfaces) and have everything saved together or not at all.
package tx

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DBTX is what repositories (and sqlc's generated code) use: both the pool and
// a pgx.Tx satisfy it.
type DBTX interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type contextKey struct{}

// Runner opens transactions on the pool.
type Runner struct {
	pool *pgxpool.Pool
}

// NewRunner creates the Runner.
func NewRunner(pool *pgxpool.Pool) Runner {
	return Runner{pool: pool}
}

// InTx runs fn inside a transaction. If ctx already has one it is reused: a use
// case calling another never opens a second one. If fn returns an error or
// panics, everything is rolled back.
func (r Runner) InTx(ctx context.Context, fn func(ctx context.Context) error) (err error) {
	if _, ok := ctx.Value(contextKey{}).(pgx.Tx); ok {
		return fn(ctx)
	}
	transaction, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			_ = transaction.Rollback(ctx)
			panic(recovered)
		}
		if err != nil {
			if rollbackErr := transaction.Rollback(ctx); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
				err = errors.Join(err, fmt.Errorf("rollback: %w", rollbackErr))
			}
		}
	}()
	if err = fn(context.WithValue(ctx, contextKey{}, transaction)); err != nil {
		return err
	}
	if err = transaction.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

// DB returns the ongoing transaction if there is one, or the pool.
func (r Runner) DB(ctx context.Context) DBTX {
	if transaction, ok := ctx.Value(contextKey{}).(pgx.Tx); ok {
		return transaction
	}
	return r.pool
}
