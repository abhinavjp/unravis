package sqlite

import (
	"context"
	"database/sql"
	"sort"
	"strings"

	"api-audit-explorer/internal/evidence"

	"api-audit-explorer/internal/casestore"
)

// eventsByIDChunk is how many events one row-fetch query reads by evidence ID.
const eventsByIDChunk = 500

type eventKey struct {
	ts int64
	id string
}

// eventsBySource answers an Events query over several sources (no attribute
// filter) in two steps, so the index ev_src_ts (source, ts_ms, import_id) can be
// used. A single "source IN (a, b) ORDER BY ts_ms, evidence_id" cannot be read
// in order from it, and SQLite would then read and sort every row of the window
// before it could stop at the limit.
//
//  1. Per source, the oldest `limit` keys (ts_ms, evidence_id) in order. The query
//     is answered from the index alone when no other column is filtered.
//  2. The keys of all sources are merged by (ts_ms, evidence_id), cut to `limit`
//     (the same events, in the same order, as the single query), and only those
//     events are read, by evidence ID, in key order.
//
// A time budget that passes in step 1 returns nothing; one that passes in step 2
// returns the oldest events read so far (stopped maps the error).
func (c *Case) eventsBySource(ctx context.Context, db *sql.DB, q casestore.EventQuery, limit int, stopped func(error) error) ([]evidence.Event, error) {
	var keys []eventKey
	for _, src := range q.Sources {
		sq := q
		sq.Sources = []string{src}
		where, args, err := eventFilter(sq)
		if err != nil {
			return nil, err
		}
		query := "SELECT ts_ms, evidence_id FROM events" + where + " ORDER BY ts_ms, evidence_id LIMIT ?"
		args = append(args, limit)
		if queryHook != nil {
			queryHook(query, args)
		}
		rows, err := db.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, stopped(err)
		}
		for rows.Next() {
			var k eventKey
			if err := rows.Scan(&k.ts, &k.id); err != nil {
				_ = rows.Close()
				return nil, stopped(err)
			}
			keys = append(keys, k)
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return nil, stopped(err)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].ts != keys[j].ts {
			return keys[i].ts < keys[j].ts
		}
		return keys[i].id < keys[j].id
	})
	// A source listed twice would give its keys twice.
	uniq := keys[:0]
	for i, k := range keys {
		if i == 0 || k != keys[i-1] {
			uniq = append(uniq, k)
		}
	}
	keys = uniq
	if len(keys) > limit {
		keys = keys[:limit]
	}

	// nil, not an empty slice, when nothing matches: the same as the single query.
	var out []evidence.Event
	if len(keys) > 0 {
		out = make([]evidence.Event, 0, len(keys))
	}
	for start := 0; start < len(keys); start += eventsByIDChunk {
		end := min(start+eventsByIDChunk, len(keys))
		chunk := keys[start:end]
		args := make([]any, len(chunk))
		for i, k := range chunk {
			args[i] = k.id
		}
		query := selectEvents + " WHERE evidence_id IN (" + strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",") + ")"
		rows, err := db.QueryContext(ctx, query, args...)
		if err != nil {
			return out, stopped(err)
		}
		got := make(map[string]evidence.Event, len(chunk))
		for rows.Next() {
			e, err := scanEvent(rows)
			if err != nil {
				_ = rows.Close()
				return out, stopped(err)
			}
			got[e.EvidenceID] = e
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return out, stopped(err)
		}
		for _, k := range chunk {
			if e, ok := got[k.id]; ok { // a row deleted in between is skipped
				out = append(out, e)
				if eventRowHook != nil {
					eventRowHook(len(out))
				}
			}
		}
	}
	return out, nil
}
