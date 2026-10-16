package db

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Tx exposes sanitized operations within one bounded transaction. The callback
// cannot commit it; Transaction returns nil only after COMMIT succeeds.
type Tx struct {
	raw pgx.Tx
	ctx context.Context
}

func (p *Pool) Transaction(ctx context.Context, body func(*Tx) error) error {
	if body == nil {
		return ErrConfig
	}
	txCtx, cancel := context.WithTimeout(ctx, p.queryTimeout)
	defer cancel()
	raw, err := p.pool.Begin(txCtx)
	if err != nil {
		return safeError(ErrQuery, err)
	}
	defer func() {
		cleanup, cleanupCancel := context.WithTimeout(context.Background(), p.queryTimeout)
		defer cleanupCancel()
		_ = raw.Rollback(cleanup)
	}()
	if err := body(&Tx{raw: raw, ctx: txCtx}); err != nil {
		return err
	}
	return safeError(ErrQuery, raw.Commit(txCtx))
}

func (t *Tx) Context() context.Context { return t.ctx }

func (t *Tx) Exec(sql string, args ...any) (pgconn.CommandTag, error) {
	tag, err := t.raw.Exec(t.ctx, sql, args...)
	return tag, safeError(ErrQuery, err)
}

func (t *Tx) QueryRow(sql string, args ...any) *Row {
	return &Row{row: t.raw.QueryRow(t.ctx, sql, args...), cancel: func() {}}
}
