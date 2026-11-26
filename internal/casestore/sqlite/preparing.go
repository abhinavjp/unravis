package sqlite

import (
	"context"
	"time"
)

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

// attrBuildRetry is how long the background series-key build waits before it tries
// again after an import held the case.
const attrBuildRetry = 500 * time.Millisecond

// startAttrBuild builds the series-key indexes in the background and marks the case
// preparing until that ends. The build holds a reference of its own, like
// startTimelineBuild: the case page opens and closes the case on every request, and
// without it the last close would shut the case down (and cancel the build) before
// a large case is indexed, again at every poll. Erase and Store.Close cancel it
// (shutdown closes quit); Begin/Finish Free and BeginArchive cancel it through
// stopTimelineBuild, which they call first because they need the case idle. An
// import that holds the case makes the build wait and try again, so the note stays
// up until the indexes exist or the build is cancelled; a query that finds the
// import running does not touch the note. The caller holds the store lock
// (Store.load, via Open).
func (c *caseState) startAttrBuild() {
	b := &tlBuild{stop: make(chan struct{}), done: make(chan struct{})}
	c.mu.Lock()
	c.refs++
	c.attrBg = b
	c.mu.Unlock()
	c.preparing.Store(true) // before the goroutine runs, so the first page read already says so
	go func() {
		bg := &Case{caseState: c}
	retry:
		for bg.ensureAttrIndexes() {
			select {
			case <-c.quit:
				break retry
			case <-b.stop:
				break retry
			case <-time.After(attrBuildRetry):
			}
		}
		c.preparing.Store(false)
		_ = bg.Close()
		c.mu.Lock()
		if c.attrBg == b {
			c.attrBg = nil
		}
		c.mu.Unlock()
		close(b.done)
	}()
}
