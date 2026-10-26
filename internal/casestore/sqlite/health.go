package sqlite

import (
	"context"
	"strings"
	"time"

	"api-audit-explorer/internal/casestore"
)

var _ casestore.HealthStore = (*Case)(nil)

// Health runs live in the encrypted case database (table health_runs). The table is created on first use with IF NOT EXISTS, so a case made
// before this feature needs no migration and the schema version does not change.
const healthSQL = `CREATE TABLE IF NOT EXISTS health_runs(id INTEGER PRIMARY KEY, at_ms INTEGER NOT NULL, kind TEXT NOT NULL, label TEXT, status TEXT NOT NULL, report TEXT NOT NULL)`

func (c *Case) AddHealthRun(ctx context.Context, r casestore.HealthRun) (casestore.HealthRun, error) {
	clean, err := casestore.CleanHealthRun(r)
	if err != nil {
		return casestore.HealthRun{}, err
	}
	db, release, err := c.tagGate(ctx)
	if err != nil {
		return casestore.HealthRun{}, err
	}
	defer release()
	clean.At = c.s.now().UTC().Truncate(time.Millisecond)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return casestore.HealthRun{}, c.mapDBErr(err)
	}
	if _, err := tx.ExecContext(ctx, healthSQL); err != nil {
		_ = tx.Rollback()
		return casestore.HealthRun{}, c.mapDBErr(err)
	}
	res, err := tx.ExecContext(ctx, "INSERT INTO health_runs(at_ms, kind, label, status, report) VALUES(?,?,?,?,?)",
		clean.At.UnixMilli(), clean.Kind, clean.Label, clean.Status, string(clean.Report))
	if err != nil {
		_ = tx.Rollback()
		return casestore.HealthRun{}, c.mapDBErr(err)
	}
	if clean.ID, err = res.LastInsertId(); err != nil {
		_ = tx.Rollback()
		return casestore.HealthRun{}, c.mapDBErr(err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM health_runs WHERE id NOT IN (SELECT id FROM health_runs ORDER BY id DESC LIMIT ?)", casestore.MaxHealthRuns); err != nil {
		_ = tx.Rollback()
		return casestore.HealthRun{}, c.mapDBErr(err)
	}
	if err := tx.Commit(); err != nil {
		return casestore.HealthRun{}, c.mapDBErr(err)
	}
	return clean, nil
}

func (c *Case) HealthRuns(ctx context.Context) ([]casestore.HealthRun, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	db, err := c.handle()
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, "SELECT id, at_ms, kind, COALESCE(label,''), status, report FROM health_runs ORDER BY id DESC")
	if err != nil {
		if strings.Contains(err.Error(), "no such table") {
			return []casestore.HealthRun{}, nil
		}
		return nil, err
	}
	defer rows.Close()
	out := []casestore.HealthRun{}
	for rows.Next() {
		var r casestore.HealthRun
		var ms int64
		var rep string
		if err := rows.Scan(&r.ID, &ms, &r.Kind, &r.Label, &r.Status, &rep); err != nil {
			return nil, err
		}
		r.At, r.Report = time.UnixMilli(ms).UTC(), []byte(rep)
		out = append(out, r)
	}
	return out, rows.Err()
}
