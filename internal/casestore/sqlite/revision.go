package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
)

// revisionKey is the meta row holding the case revision: a monotonic integer,
// as decimal text, that every case mutation bumps inside its own transaction
// (the storage half of the export snapshot rule). It needs no schema change:
// a case from an earlier build has no such row and reads as revision 0.
const revisionKey = "case.revision"

// bumpCaseRevision raises the case revision by one. It runs on the caller's
// transaction, so the bump commits or rolls back with the caller's own writes;
// it never begins or commits one itself.
func bumpCaseRevision(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO meta(k, v) VALUES(?, '1')
		ON CONFLICT(k) DO UPDATE SET v = CAST(CAST(v AS INTEGER) + 1 AS TEXT)`, revisionKey); err != nil {
		return fmt.Errorf("case revision: %w", err)
	}
	return nil
}

// Revision reads the case revision: 0 when it was never bumped.
func (c *Case) Revision(ctx context.Context) (int64, error) {
	db, err := c.handle()
	if err != nil {
		return 0, err
	}
	var v string
	err = db.QueryRowContext(ctx, "SELECT v FROM meta WHERE k=?", revisionKey).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%w: case revision", ErrInvalid)
	}
	return n, nil
}
