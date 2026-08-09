package repository

import (
	"context"
	"database/sql"
)

// DBTX is satisfied by both *sql.DB and *sql.Tx. Every repository method takes
// one, so the usecase layer decides whether a call runs inside a transaction —
// repositories never open one themselves.
type DBTX interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

var (
	_ DBTX = (*sql.DB)(nil)
	_ DBTX = (*sql.Tx)(nil)
)
