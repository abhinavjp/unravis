package sqlite

import (
	"context"
	"database/sql"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/sealedfile"
)

var _ casestore.Tagger = (*Case)(nil)

// The environment list and the source tags live in the encrypted case database
// (tables case_envs and case_sources, schema 4). A row holds only the short words
// the user chose; see casestore.SourceTag. Writes go through the same folder lock
// and closed-case check as Import, so a CloseCase in another process is honoured.

// loadTagCounts reads the two row counts Info reports. Open calls it once, after
// the migration.
func (c *Case) loadTagCounts(ctx context.Context) error {
	return c.countTags(ctx, c.db)
}


func (c *Case) countTags(ctx context.Context, q *sql.DB) error {
	var envs, sources int64
	if err := q.QueryRowContext(ctx, "SELECT (SELECT COUNT(*) FROM case_envs), (SELECT COUNT(*) FROM case_sources)").Scan(&envs, &sources); err != nil {
		return err
	}
	c.tagEnvs.Store(envs)
	c.tagSources.Store(sources)
	return nil
}

// tagGate returns the database for a tag write, holding the folder lock until
// release is called, or an error when the handle is closed, the case is erased or
// the case has been closed.
func (c *Case) tagGate(ctx context.Context) (db *sql.DB, release func(), err error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	db, err = c.handle()
	if err != nil {
		return nil, nil, err
	}
	release, err = sealedfile.Lock(c.dir)
	if err != nil {
		return nil, nil, err
	}
	if err := c.syncManifest(ctx, db); err != nil {
		release()
		return nil, nil, err
	}
	if c.isClosed() {
		release()
		return nil, nil, casestore.ErrCaseClosed
	}
	return db, release, nil
}

func (c *Case) SetEnvs(ctx context.Context, names []string) error {
	clean, err := casestore.CleanEnvs(names)
	if err != nil {
		return err
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
	if _, err := tx.ExecContext(ctx, "DELETE FROM case_envs"); err != nil {
		_ = tx.Rollback()
		return c.mapDBErr(err)
	}
	for i, n := range clean {
		if _, err := tx.ExecContext(ctx, "INSERT INTO case_envs(name, ord) VALUES(?, ?)", n, i); err != nil {
			_ = tx.Rollback()
			return c.mapDBErr(err)
		}
	}
	if err := tx.Commit(); err != nil {
		return c.mapDBErr(err)
	}
	c.tagEnvs.Store(int64(len(clean)))
	return nil
}

func (c *Case) Envs(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	db, err := c.handle()
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, "SELECT name FROM case_envs ORDER BY ord")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (c *Case) AddSource(ctx context.Context, s casestore.SourceTag) (casestore.SourceTag, error) {
	clean, err := casestore.CleanSource(s)
	if err != nil {
		return casestore.SourceTag{}, err
	}
	db, release, err := c.tagGate(ctx)
	if err != nil {
		return casestore.SourceTag{}, err
	}
	defer release()
	clean.Created = c.s.now().Truncate(time.Millisecond)
	var importID any
	if clean.ImportID != 0 {
		importID = clean.ImportID
	}
	// The count test and the insert are one statement, so the cap holds however
	// many writers there are.
	res, err := db.ExecContext(ctx, `INSERT INTO case_sources(import_id, kind, label, env, portal, api, classification, created_ms)
		SELECT ?, ?, ?, ?, ?, ?, ?, ? WHERE (SELECT COUNT(*) FROM case_sources) < ?`,
		importID, clean.Kind, clean.Label, clean.Env, clean.Portal, clean.API, clean.Classification, clean.Created.UnixMilli(), casestore.MaxSources)
	if err != nil {
		return casestore.SourceTag{}, c.mapDBErr(err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return casestore.SourceTag{}, err
	} else if n == 0 {
		return casestore.SourceTag{}, casestore.ErrCapExceeded
	}
	if clean.ID, err = res.LastInsertId(); err != nil {
		return casestore.SourceTag{}, err
	}
	_ = c.countTags(ctx, db) // best effort: Info's counts catch up on the next write
	return clean, nil
}

func (c *Case) Sources(ctx context.Context) ([]casestore.SourceTag, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	db, err := c.handle()
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT id, import_id, kind, label, env, portal, api, classification, created_ms
		FROM case_sources ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []casestore.SourceTag{}
	for rows.Next() {
		var t casestore.SourceTag
		var imp, ms sql.NullInt64
		var label, env, portal, api, class sql.NullString
		if err := rows.Scan(&t.ID, &imp, &t.Kind, &label, &env, &portal, &api, &class, &ms); err != nil {
			return nil, err
		}
		t.ImportID = imp.Int64
		t.Label, t.Env, t.Portal, t.API, t.Classification = label.String, env.String, portal.String, api.String, class.String
		t.Created = time.UnixMilli(ms.Int64).UTC()
		out = append(out, t)
	}
	return out, rows.Err()
}

func (c *Case) RemoveSource(ctx context.Context, id int64) error {
	db, release, err := c.tagGate(ctx)
	if err != nil {
		return err
	}
	defer release()
	if _, err := db.ExecContext(ctx, "DELETE FROM case_sources WHERE id=?", id); err != nil {
		return c.mapDBErr(err)
	}
	_ = c.countTags(ctx, db)
	return nil
}
