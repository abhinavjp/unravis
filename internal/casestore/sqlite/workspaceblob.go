package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"api-audit-explorer/internal/casestore"
)

var _ casestore.WorkspaceBlobStore = (*Case)(nil)

// The case's workspace note is one row in the encrypted case database (table
// workspace_blob), so it travels with the case file and is restored with it. The
// table is created on first write with IF NOT EXISTS: a case made before this
// feature needs no migration, opens as before and reads as "no workspace". Like
// the ledger blob, it adds no schema version.
const workspaceBlobSQL = `CREATE TABLE IF NOT EXISTS workspace_blob(id INTEGER PRIMARY KEY CHECK (id = 1), body BLOB NOT NULL)`

func (c *Case) SetWorkspaceBlob(ctx context.Context, b []byte) error {
	if len(b) > casestore.MaxWorkspaceBlob {
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
	if _, err := tx.ExecContext(ctx, workspaceBlobSQL); err != nil {
		_ = tx.Rollback()
		return c.mapDBErr(err)
	}
	if len(b) == 0 {
		if _, err := tx.ExecContext(ctx, "DELETE FROM workspace_blob WHERE id = 1"); err != nil {
			_ = tx.Rollback()
			return c.mapDBErr(err)
		}
	} else if _, err := tx.ExecContext(ctx, "INSERT INTO workspace_blob(id, body) VALUES(1, ?) ON CONFLICT(id) DO UPDATE SET body = excluded.body", append([]byte{}, b...)); err != nil {
		_ = tx.Rollback()
		return c.mapDBErr(err)
	}
	if err := tx.Commit(); err != nil {
		return c.mapDBErr(err)
	}
	return nil
}

func (c *Case) WorkspaceBlob(ctx context.Context) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	db, err := c.handle()
	if err != nil {
		return nil, err
	}
	var body []byte
	err = db.QueryRowContext(ctx, "SELECT body FROM workspace_blob WHERE id = 1").Scan(&body)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, nil
	case err != nil && strings.Contains(err.Error(), "no such table"):
		return nil, nil
	case err != nil:
		return nil, err
	}
	if len(body) == 0 {
		return nil, nil
	}
	return body, nil
}
