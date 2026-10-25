package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"strconv"
	"time"

	"api-audit-explorer/internal/casestore"
)

// discoverSQL is the schema-2 addition: the host discovery tables. It is written
// with IF NOT EXISTS so a new case, an old case being migrated and a repeat of
// the migration all run the same text. The tables hold no path: a file is named
// by volume serial, file index and an HMAC of its path (see casestore.Discoverer).
const discoverSQL = `
CREATE TABLE IF NOT EXISTS discover_watermarks(
  volume_serial INTEGER NOT NULL, file_index INTEGER NOT NULL, path_hmac BLOB NOT NULL,
  off INTEGER NOT NULL, last_line_ms INTEGER, head_len INTEGER NOT NULL, head_sha BLOB NOT NULL,
  PRIMARY KEY(volume_serial, file_index, path_hmac)) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS discover_reads(
  id INTEGER PRIMARY KEY, at_ms INTEGER NOT NULL, detector TEXT, importer TEXT, kind TEXT, display TEXT,
  path_hmac TEXT, range_start INTEGER, range_end INTEGER, file_size INTEGER, file_mod_ms INTEGER,
  bytes_sha256 TEXT, elevated INTEGER NOT NULL, window_from_ms INTEGER, window_to_ms INTEGER, result TEXT);
`

var _ casestore.Discoverer = (*Case)(nil)

// migrate brings an older case up to schemaVersion. It only ever adds tables
// and writes the version, all in one transaction; the schema-3 indexes are built
// lazily (ensureAttrIndexes), so opening a large old case stays fast. Every
// statement is IF NOT EXISTS, so running it again changes nothing. Whatever the
// starting version, it ends by loading the tag counts Info reports.
func (c *Case) migrate(ctx context.Context, from int) error {
	if from < schemaVersion {
		c.idxPending = from < 3
		tx, err := c.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, discoverSQL); err != nil {
			_ = tx.Rollback()
			return err
		}
		if _, err := tx.ExecContext(ctx, tagsSQL); err != nil {
			_ = tx.Rollback()
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE meta SET v=? WHERE k='schema_version'", strconv.Itoa(schemaVersion)); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return c.loadTagCounts(ctx)
}

// DiscoverKey is the path HMAC key for this case: derived from the pseudonym
// key, so it dies with case.key. nil after Close or Erase.
func (c *Case) DiscoverKey() []byte {
	if c.closed.Load() {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.db == nil || len(c.keys.Pseudo) != 32 || allZero(c.keys.Pseudo) {
		return nil
	}
	return casestore.DeriveDiscoverKey(c.keys.Pseudo)
}

func msOrNil(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UnixMilli()
}

func timeOrZero(n sql.NullInt64) time.Time {
	if !n.Valid {
		return time.Time{}
	}
	return time.UnixMilli(n.Int64).UTC()
}

func (c *Case) DiscoverMark(ctx context.Context, k casestore.DiscoverFileKey) (casestore.DiscoverMark, bool, error) {
	db, err := c.handle()
	if err != nil {
		return casestore.DiscoverMark{}, false, err
	}
	var m casestore.DiscoverMark
	var off, headLen int64
	var last sql.NullInt64
	var sha []byte
	err = db.QueryRowContext(ctx, `SELECT off, last_line_ms, head_len, head_sha FROM discover_watermarks
		WHERE volume_serial=? AND file_index=? AND path_hmac=?`, int64(k.VolumeSerial), int64(k.FileIndex), k.PathHMAC[:]).
		Scan(&off, &last, &headLen, &sha)
	if err == sql.ErrNoRows {
		return casestore.DiscoverMark{}, false, nil
	}
	if err != nil {
		return casestore.DiscoverMark{}, false, err
	}
	if len(sha) != 32 || off < 0 || headLen < 0 || headLen > 1<<20 {
		return casestore.DiscoverMark{}, false, ErrDamaged
	}
	m.Offset, m.HeadLen, m.LastLine = off, int(headLen), timeOrZero(last)
	copy(m.HeadSHA[:], sha)
	return m, true, nil
}

func (c *Case) PutDiscoverMark(ctx context.Context, k casestore.DiscoverFileKey, m casestore.DiscoverMark) error {
	db, err := c.handle()
	if err != nil {
		return err
	}
	if m.Offset < 0 || m.HeadLen < 0 {
		return ErrInvalid
	}
	var n int64
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM discover_watermarks").Scan(&n); err != nil {
		return err
	}
	if n >= casestore.MaxDiscoverMarks {
		var have int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM discover_watermarks WHERE volume_serial=? AND file_index=? AND path_hmac=?`,
			int64(k.VolumeSerial), int64(k.FileIndex), k.PathHMAC[:]).Scan(&have); err != nil {
			return err
		}
		if have == 0 {
			return casestore.ErrCapExceeded
		}
	}
	_, err = db.ExecContext(ctx, `INSERT INTO discover_watermarks(volume_serial, file_index, path_hmac, off, last_line_ms, head_len, head_sha)
		VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(volume_serial, file_index, path_hmac) DO UPDATE SET off=excluded.off, last_line_ms=excluded.last_line_ms,
		head_len=excluded.head_len, head_sha=excluded.head_sha`,
		int64(k.VolumeSerial), int64(k.FileIndex), k.PathHMAC[:], m.Offset, msOrNil(m.LastLine), m.HeadLen, bytes.Clone(m.HeadSHA[:]))
	return err
}

func (c *Case) DeleteDiscoverMark(ctx context.Context, k casestore.DiscoverFileKey) error {
	db, err := c.handle()
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `DELETE FROM discover_watermarks WHERE volume_serial=? AND file_index=? AND path_hmac=?`,
		int64(k.VolumeSerial), int64(k.FileIndex), k.PathHMAC[:])
	return err
}

func (c *Case) RecordDiscoverRead(ctx context.Context, r casestore.DiscoverRead) error {
	db, err := c.handle()
	if err != nil {
		return err
	}
	r, err = casestore.CleanDiscoverRead(r)
	if err != nil {
		return err
	}
	var n int64
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM discover_reads").Scan(&n); err != nil {
		return err
	}
	if n >= casestore.MaxDiscoverReads {
		return casestore.ErrCapExceeded
	}
	elevated := 0
	if r.Elevated {
		elevated = 1
	}
	_, err = db.ExecContext(ctx, `INSERT INTO discover_reads(at_ms, detector, importer, kind, display, path_hmac, range_start, range_end,
		file_size, file_mod_ms, bytes_sha256, elevated, window_from_ms, window_to_ms, result) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		r.At.UnixMilli(), r.Detector, r.Importer, r.Kind, r.Display, r.PathHMAC, r.RangeStart, r.RangeEnd,
		r.FileSize, msOrNil(r.FileModified), r.BytesSHA256, elevated, msOrNil(r.WindowFrom), msOrNil(r.WindowTo), r.Result)
	return err
}

func (c *Case) DiscoverReads(ctx context.Context) ([]casestore.DiscoverRead, error) {
	db, err := c.handle()
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT at_ms, detector, importer, kind, display, path_hmac, range_start, range_end,
		file_size, file_mod_ms, bytes_sha256, elevated, window_from_ms, window_to_ms, result FROM discover_reads ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []casestore.DiscoverRead{}
	for rows.Next() {
		var r casestore.DiscoverRead
		var at, rs, re, size, elevated int64
		var mod, wf, wt sql.NullInt64
		var det, imp, kind, disp, hm, sum, res sql.NullString
		if err := rows.Scan(&at, &det, &imp, &kind, &disp, &hm, &rs, &re, &size, &mod, &sum, &elevated, &wf, &wt, &res); err != nil {
			return nil, err
		}
		r.At = time.UnixMilli(at).UTC()
		r.Detector, r.Importer, r.Kind, r.Display, r.PathHMAC, r.BytesSHA256, r.Result =
			det.String, imp.String, kind.String, disp.String, hm.String, sum.String, res.String
		r.RangeStart, r.RangeEnd, r.FileSize = rs, re, size
		r.FileModified, r.WindowFrom, r.WindowTo = timeOrZero(mod), timeOrZero(wf), timeOrZero(wt)
		r.Elevated = elevated == 1
		out = append(out, r)
	}
	return out, rows.Err()
}
