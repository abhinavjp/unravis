package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"api-audit-explorer/internal/casestore"
)

var _ casestore.LedgerBlobStore = (*Case)(nil)

// The case's theories ledger is one row in the encrypted case database (table
// ledger_blob), so it travels with the case file and is restored with it. The
// table is created on first write with IF NOT EXISTS: a case made before this
// feature needs no migration, opens as before and reads as "no ledger". Like
// health runs, it adds no schema version.
const ledgerBlobSQL = `CREATE TABLE IF NOT EXISTS ledger_blob(id INTEGER PRIMARY KEY CHECK (id = 1), body BLOB NOT NULL)`

func (c *Case) SetLedgerBlob(ctx context.Context, b []byte) error {
	if len(b) > casestore.MaxLedgerBlob {
		return casestore.ErrCapExceeded
	}
	db, release, err := c.tagGate(ctx)
	if err != nil {
		return err
	}
	defer release()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return c.mapDBErr(err)
	}
	if _, err := tx.ExecContext(ctx, ledgerBlobSQL); err != nil {
		_ = tx.Rollback()
		return c.mapDBErr(err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO ledger_blob(id, body) VALUES(1, ?) ON CONFLICT(id) DO UPDATE SET body = excluded.body", append([]byte{}, b...)); err != nil {
		_ = tx.Rollback()
		return c.mapDBErr(err)
	}
	if err := tx.Commit(); err != nil {
		return c.mapDBErr(err)
	}
	return nil
}

func (c *Case) LedgerBlob(ctx context.Context) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	db, err := c.handle()
	if err != nil {
		return nil, err
	}
	var body []byte
	err = db.QueryRowContext(ctx, "SELECT body FROM ledger_blob WHERE id = 1").Scan(&body)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, nil
	case err != nil && strings.Contains(err.Error(), "no such table"):
		return nil, nil
	case err != nil:
		return nil, err
	}
	return body, nil
}
