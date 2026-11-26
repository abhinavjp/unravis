package sqlite

import "context"

// Preparing says whether this case is still building indexes in the background:
// the series-key indexes of a case from before schema 3 (started when the case is
// opened, see Store.load) or the timeline indexes (see startTimelineBuild). It
// reads two flags and takes no database lock, so the case page can ask at any time.
// It is the casestore.Preparer of an opened case.
func (c *caseState) Preparing() bool {
	if c.preparing.Load() {
		return true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.bg != nil
}

// attrIndexesMissing says whether a case that holds events lacks the series-key
// indexes (ev_kind_counter, ev_kind_metric). A case with no events is not
// missing them in any way that matters: the build would have nothing to read.
// An error reads as "not missing", so a database that cannot answer here is
// left to the verify and query paths, which report it.
func attrIndexesMissing(ctx context.Context, db queryRower) bool {
	var have int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='index' AND name IN ('ev_kind_counter','ev_kind_metric')`).Scan(&have); err != nil || have == len(attrIndexNames) {
		return false
	}
	var one int
	return db.QueryRowContext(ctx, `SELECT 1 FROM events LIMIT 1`).Scan(&one) == nil
}

// attrBuildHook, when set (tests only), runs in the series-key index build once it
// holds the import lock, before it builds anything.
var attrBuildHook func()
