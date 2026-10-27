package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"api-audit-explorer/internal/casestore"
)

var _ casestore.PlanStore = (*Case)(nil)

// Saved views and the checklist are rows of two small tables in the encrypted
// case database, so they travel with the case file. Like the workspace note,
// each table is created on first write with IF NOT EXISTS: a case made before
// this feature needs no migration, opens as before and reads as "none saved".
// They add no schema version.
const (
	savedViewsSQL    = `CREATE TABLE IF NOT EXISTS saved_views(id TEXT PRIMARY KEY, name TEXT NOT NULL, tool TEXT NOT NULL, settings BLOB NOT NULL, saved INTEGER NOT NULL)`
	checklistItemSQL = `CREATE TABLE IF NOT EXISTS checklist_items(id TEXT PRIMARY KEY, list TEXT NOT NULL, text TEXT NOT NULL, suspect TEXT NOT NULL, ord INTEGER NOT NULL, created INTEGER NOT NULL, updated INTEGER NOT NULL)`
)

func noTable(err error) bool { return err != nil && strings.Contains(err.Error(), "no such table") }

// planTx runs fn inside one write transaction on a case that is open and not
// closed, after making sure the table exists.
func (c *Case) planTx(ctx context.Context, create string, fn func(tx *sql.Tx) error) error {
	db, release, err := c.tagGate(ctx)
	if err != nil {
		return err
	}
	defer release()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return c.mapDBErr(err)
	}
	if _, err := tx.ExecContext(ctx, create); err != nil {
		_ = tx.Rollback()
		return c.mapDBErr(err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		if errors.Is(err, casestore.ErrNotFound) || errors.Is(err, casestore.ErrCapExceeded) || errors.Is(err, casestore.ErrBadPlan) {
			return err
		}
		return c.mapDBErr(err)
	}
	if err := tx.Commit(); err != nil {
		return c.mapDBErr(err)
	}
	return nil
}

func (c *Case) SavedViews(ctx context.Context) ([]casestore.SavedView, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	db, err := c.handle()
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, "SELECT id, name, tool, settings, saved FROM saved_views ORDER BY saved DESC, id")
	if noTable(err) {
		return []casestore.SavedView{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []casestore.SavedView{}
	for rows.Next() {
		var v casestore.SavedView
		var settings []byte
		var saved int64
		if err := rows.Scan(&v.ID, &v.Name, &v.Tool, &settings, &saved); err != nil {
			return nil, err
		}
		v.Settings = json.RawMessage(settings)
		v.Saved = time.UnixMilli(saved).UTC()
		out = append(out, v)
	}
	return out, rows.Err()
}

func (c *Case) SaveView(ctx context.Context, v casestore.SavedView) (casestore.SavedView, error) {
	clean, err := casestore.CleanSavedView(v)
	if err != nil {
		return casestore.SavedView{}, err
	}
	if clean.ID != "" && !casestore.ValidPlanID(clean.ID) {
		return casestore.SavedView{}, casestore.ErrBadPlan
	}
	clean.Saved = time.Now().UTC().Truncate(time.Millisecond)
	err = c.planTx(ctx, savedViewsSQL, func(tx *sql.Tx) error {
		if clean.ID != "" {
			res, err := tx.ExecContext(ctx, "UPDATE saved_views SET name = ?, tool = ?, settings = ?, saved = ? WHERE id = ?",
				clean.Name, clean.Tool, []byte(clean.Settings), clean.Saved.UnixMilli(), clean.ID)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return casestore.ErrNotFound
			}
			return nil
		}
		var count int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM saved_views").Scan(&count); err != nil {
			return err
		}
		if count >= casestore.MaxSavedViews {
			return casestore.ErrCapExceeded
		}
		id, err := casestore.NewPlanID()
		if err != nil {
			return err
		}
		clean.ID = id
		_, err = tx.ExecContext(ctx, "INSERT INTO saved_views(id, name, tool, settings, saved) VALUES(?, ?, ?, ?, ?)",
			id, clean.Name, clean.Tool, []byte(clean.Settings), clean.Saved.UnixMilli())
		return err
	})
	if err != nil {
		return casestore.SavedView{}, err
	}
	return clean, nil
}

func (c *Case) DeleteView(ctx context.Context, id string) error {
	if !casestore.ValidPlanID(id) {
		return casestore.ErrNotFound
	}
	return c.planTx(ctx, savedViewsSQL, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, "DELETE FROM saved_views WHERE id = ?", id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return casestore.ErrNotFound
		}
		return nil
	})
}

func (c *Case) ChecklistItems(ctx context.Context) ([]casestore.ChecklistItem, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	db, err := c.handle()
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT id, list, text, suspect, ord, created, updated FROM checklist_items
		ORDER BY CASE list WHEN 'todo' THEN 0 WHEN 'doing' THEN 1 WHEN 'learned' THEN 2 ELSE 3 END, ord, created, id`)
	if noTable(err) {
		return []casestore.ChecklistItem{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []casestore.ChecklistItem{}
	for rows.Next() {
		var it casestore.ChecklistItem
		var created, updated int64
		if err := rows.Scan(&it.ID, &it.List, &it.Text, &it.Suspect, &it.Order, &created, &updated); err != nil {
			return nil, err
		}
		it.Created, it.Updated = time.UnixMilli(created).UTC(), time.UnixMilli(updated).UTC()
		out = append(out, it)
	}
	return out, rows.Err()
}

func (c *Case) PutChecklistItem(ctx context.Context, it casestore.ChecklistItem) (casestore.ChecklistItem, error) {
	clean, err := casestore.CleanChecklistItem(it)
	if err != nil {
		return casestore.ChecklistItem{}, err
	}
	if clean.ID != "" && !casestore.ValidPlanID(clean.ID) {
		return casestore.ChecklistItem{}, casestore.ErrBadPlan
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	clean.Updated = now
	err = c.planTx(ctx, checklistItemSQL, func(tx *sql.Tx) error {
		if clean.ID != "" {
			var created int64
			err := tx.QueryRowContext(ctx, "SELECT created FROM checklist_items WHERE id = ?", clean.ID).Scan(&created)
			if errors.Is(err, sql.ErrNoRows) {
				return casestore.ErrNotFound
			}
			if err != nil {
				return err
			}
			clean.Created = time.UnixMilli(created).UTC()
			_, err = tx.ExecContext(ctx, "UPDATE checklist_items SET list = ?, text = ?, suspect = ?, ord = ?, updated = ? WHERE id = ?",
				clean.List, clean.Text, clean.Suspect, clean.Order, now.UnixMilli(), clean.ID)
			return err
		}
		var count int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM checklist_items").Scan(&count); err != nil {
			return err
		}
		if count >= casestore.MaxChecklistItems {
			return casestore.ErrCapExceeded
		}
		id, err := casestore.NewPlanID()
		if err != nil {
			return err
		}
		clean.ID, clean.Created = id, now
		_, err = tx.ExecContext(ctx, "INSERT INTO checklist_items(id, list, text, suspect, ord, created, updated) VALUES(?, ?, ?, ?, ?, ?, ?)",
			id, clean.List, clean.Text, clean.Suspect, clean.Order, now.UnixMilli(), now.UnixMilli())
		return err
	})
	if err != nil {
		return casestore.ChecklistItem{}, err
	}
	return clean, nil
}

func (c *Case) DeleteChecklistItem(ctx context.Context, id string) error {
	if !casestore.ValidPlanID(id) {
		return casestore.ErrNotFound
	}
	return c.planTx(ctx, checklistItemSQL, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, "DELETE FROM checklist_items WHERE id = ?", id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return casestore.ErrNotFound
		}
		return nil
	})
}
