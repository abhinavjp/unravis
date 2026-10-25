package sqlite

import (
	"context"
	"database/sql"

	"api-audit-explorer/internal/casestore"
)

// scanShare is the share of a case's events (1 in scanShare) at which an
// unordered query is read in storage order instead of through the kind and time
// index. The events table is WITHOUT ROWID with a random hash as its key, so each
// event reached through an index costs a random page read and a decrypt (about
// 8 us on the benchmark machine), while reading the table end to end costs about
// 1 us per event of the whole case plus the decoding of the events it keeps.
// The two meet near one event in seven; one in five leaves margin for a cold
// cache.
const scanShare = 5

// evStats is the event count of the case and per kind. It is valid for one value
// of the newest import id: events only arrive with an import, in one transaction
// with its imports row.
type evStats struct {
	gen   int64
	total int64
	kinds map[string]int64
}

// eventStats returns the counts, recounting only after an import. The count reads
// the (kind, ts_ms) index alone. A failure returns nil: the caller then reads by
// index, as it always did.
func (c *Case) eventStats(ctx context.Context, db *sql.DB) *evStats {
	var gen int64
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(id), 0) FROM imports`).Scan(&gen); err != nil {
		return nil
	}
	c.statsMu.Lock()
	defer c.statsMu.Unlock()
	if c.stats != nil && c.stats.gen == gen {
		return c.stats
	}
	rows, err := db.QueryContext(ctx, `SELECT kind, count(*) FROM events GROUP BY kind`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	st := &evStats{gen: gen, kinds: map[string]int64{}}
	for rows.Next() {
		var kind sql.NullString
		var n int64
		if err := rows.Scan(&kind, &n); err != nil {
			return nil
		}
		st.kinds[kind.String] += n
		st.total += n
	}
	if rows.Err() != nil {
		return nil
	}
	c.stats = st
	return st
}

// preferScan reports whether an unordered query should read the table in storage
// order. It does for a query on kinds and time alone that matches at least
// 1 in scanShare of the case's events; a query with a source, host or attribute
// filter keeps the index path, which the filters narrow.
func (c *Case) preferScan(ctx context.Context, db *sql.DB, q casestore.EventQuery) bool {
	if !q.Unordered || len(q.Kinds) == 0 || len(q.Sources) > 0 || len(q.Hosts) > 0 || len(q.Attrs) > 0 {
		return false
	}
	st := c.eventStats(ctx, db)
	if st == nil || st.total == 0 {
		return false
	}
	var kinds int64
	for _, k := range q.Kinds {
		kinds += st.kinds[k]
	}
	if kinds*scanShare < st.total {
		return false
	}
	matched := kinds
	if !q.Window.From.IsZero() || !q.Window.To.IsZero() {
		where, args, err := eventFilter(casestore.EventQuery{Window: q.Window, Kinds: q.Kinds})
		if err != nil {
			return false
		}
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM events`+where, args...).Scan(&matched); err != nil {
			return false
		}
	}
	return matched*scanShare >= st.total
}
