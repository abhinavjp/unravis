package sqlite

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"api-audit-explorer/internal/casestore"

	"api-audit-explorer/internal/sealedfile"
)

var _ casestore.RetryStore = (*Case)(nil)

// The "needs another try" list lives in the encrypted case database (table
// case_retries), so it travels with the case file and is restored with it. Like
// workspace_blob the table is created on first write with IF NOT EXISTS: a case
// made before this feature needs no migration and reads as an empty list. No schema
// version is added. The list is about the case, not evidence in it, so writing it
// is allowed on a Closed case.
const retriesSQL = `CREATE TABLE IF NOT EXISTS case_retries(
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 kind TEXT NOT NULL, key TEXT NOT NULL, label TEXT NOT NULL, reason TEXT NOT NULL,
 payload BLOB, created TEXT NOT NULL)`

// retryGate is tagGate without the closed-case refusal.
func (c *Case) retryGate(ctx context.Context) (*sql.DB, func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	db, err := c.handle()
	if err != nil {
		return nil, nil, err
	}
	release, err := sealedfile.Lock(c.dir)
	if err != nil {
		return nil, nil, err
	}
	if err := c.syncManifest(ctx, db); err != nil {
		release()
		return nil, nil, err
	}
	return db, release, nil
}

func (c *Case) Retries(ctx context.Context) ([]casestore.RetryItem, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	db, err := c.handle()
	if err != nil {
		return nil, err
	}
	return readRetries(ctx, db)
}

func readRetries(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}) ([]casestore.RetryItem, error) {
	rows, err := q.QueryContext(ctx, "SELECT id, kind, key, label, reason, payload, created FROM case_retries ORDER BY id")
	if err != nil {
		if strings.Contains(err.Error(), "no such table") {
			return []casestore.RetryItem{}, nil
		}
		return nil, err
	}
	defer rows.Close()
	out := []casestore.RetryItem{}
	for rows.Next() {
		var it casestore.RetryItem
		var payload []byte
		var created string
		if err := rows.Scan(&it.ID, &it.Kind, &it.Key, &it.Label, &it.Reason, &payload, &created); err != nil {
			return nil, err
		}
		if len(payload) > 0 {
			it.Payload = payload
		}
		it.Created, _ = time.Parse(time.RFC3339Nano, created)
		out = append(out, it)
	}
	return out, rows.Err()
}

func (c *Case) ReplaceRetries(ctx context.Context, items []casestore.RetryItem) ([]casestore.RetryItem, error) {
	clean, err := casestore.CleanRetries(items)
	if err != nil {
		return nil, err
	}
	db, release, err := c.retryGate(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, c.mapDBErr(err)
	}
	if _, err := tx.ExecContext(ctx, retriesSQL); err != nil {
		_ = tx.Rollback()
		return nil, c.mapDBErr(err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM case_retries"); err != nil {
		_ = tx.Rollback()
		return nil, c.mapDBErr(err)
	}
	now := c.s.now().UTC().Truncate(time.Millisecond).Format(time.RFC3339Nano)
	for _, it := range clean {
		var payload any
		if len(it.Payload) > 0 {
			payload = it.Payload
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO case_retries(kind, key, label, reason, payload, created) VALUES(?,?,?,?,?,?)",
			it.Kind, it.Key, it.Label, it.Reason, payload, now); err != nil {
			_ = tx.Rollback()
			return nil, c.mapDBErr(err)
		}
	}
	out, err := readRetries(ctx, tx)
	if err != nil {
		_ = tx.Rollback()
		return nil, c.mapDBErr(err)
	}
	if err := tx.Commit(); err != nil {
		return nil, c.mapDBErr(err)
	}
	return out, nil
}

// NoteRetry writes one item, replacing the one with the same kind and key.
func (c *Case) NoteRetry(ctx context.Context, item casestore.RetryItem) (casestore.RetryItem, error) {
	clean, err := casestore.CleanRetries([]casestore.RetryItem{item})
	if err != nil {
		return casestore.RetryItem{}, err
	}
	db, release, err := c.retryGate(ctx)
	if err != nil {
		return casestore.RetryItem{}, err
	}
	defer release()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return casestore.RetryItem{}, c.mapDBErr(err)
	}
	if _, err := tx.ExecContext(ctx, retriesSQL); err != nil {
		_ = tx.Rollback()
		return casestore.RetryItem{}, c.mapDBErr(err)
	}
	it := clean[0]
	if _, err := tx.ExecContext(ctx, "DELETE FROM case_retries WHERE kind = ? AND lower(key) = lower(?)", it.Kind, it.Key); err != nil {
		_ = tx.Rollback()
		return casestore.RetryItem{}, c.mapDBErr(err)
	}
	var n int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM case_retries").Scan(&n); err != nil {
		_ = tx.Rollback()
		return casestore.RetryItem{}, c.mapDBErr(err)
	}
	if n >= casestore.MaxRetries {
		_ = tx.Rollback()
		return casestore.RetryItem{}, casestore.ErrCapExceeded
	}
	var payload any
	if len(it.Payload) > 0 {
		payload = it.Payload
	}
	now := c.s.now().UTC().Truncate(time.Millisecond)
	res, err := tx.ExecContext(ctx, "INSERT INTO case_retries(kind, key, label, reason, payload, created) VALUES(?,?,?,?,?,?)",
		it.Kind, it.Key, it.Label, it.Reason, payload, now.Format(time.RFC3339Nano))
	if err != nil {
		_ = tx.Rollback()
		return casestore.RetryItem{}, c.mapDBErr(err)
	}
	if err := tx.Commit(); err != nil {
		return casestore.RetryItem{}, c.mapDBErr(err)
	}
	it.ID, _ = res.LastInsertId()
	it.Created = now
	return it, nil
}

func (c *Case) RemoveRetry(ctx context.Context, id int64) error {
	db, release, err := c.retryGate(ctx)
	if err != nil {
		return err
	}
	defer release()
	if _, err := db.ExecContext(ctx, "DELETE FROM case_retries WHERE id = ?", id); err != nil {
		if strings.Contains(err.Error(), "no such table") {
			return nil
		}
		return c.mapDBErr(err)
	}
	return nil
}
