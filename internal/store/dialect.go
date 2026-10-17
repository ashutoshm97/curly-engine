package store

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
)

// dbx wraps *sql.DB so the same `?`-placeholder SQL runs on SQLite and Postgres.
type dbx struct {
	*sql.DB
	pg bool
}

type txx struct {
	*sql.Tx
	pg bool
}

// rebind rewrites `?` placeholders to `$1, $2, ...` for Postgres.
func rebind(pg bool, q string) string {
	if !pg || !strings.Contains(q, "?") {
		return q
	}
	var b strings.Builder
	n := 0
	for _, c := range q {
		if c == '?' {
			n++
			b.WriteString("$" + strconv.Itoa(n))
			continue
		}
		b.WriteRune(c)
	}
	return b.String()
}

func (d dbx) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	return d.DB.ExecContext(ctx, rebind(d.pg, q), args...)
}

func (d dbx) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	return d.DB.QueryContext(ctx, rebind(d.pg, q), args...)
}

func (d dbx) QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row {
	return d.DB.QueryRowContext(ctx, rebind(d.pg, q), args...)
}

func (d dbx) BeginTx(ctx context.Context, o *sql.TxOptions) (*txx, error) {
	tx, err := d.DB.BeginTx(ctx, o)
	return &txx{tx, d.pg}, err
}

func (t *txx) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	return t.Tx.ExecContext(ctx, rebind(t.pg, q), args...)
}

func (t *txx) QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row {
	return t.Tx.QueryRowContext(ctx, rebind(t.pg, q), args...)
}

// IsPostgres reports whether dsn selects the Postgres backend.
func IsPostgres(dsn string) bool {
	return strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://")
}

// pgSchema adapts the SQLite DDL: 64-bit millisecond timestamps and BYTEA blobs.
func pgSchema() string {
	return strings.NewReplacer(" INTEGER", " BIGINT", " BLOB", " BYTEA").Replace(schema)
}
