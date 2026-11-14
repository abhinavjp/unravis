package sqlite

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"api-audit-explorer/internal/casestore"
)

// timelineIndexesMissing reports whether the case has events but lacks one of
// the timeline indexes (timelineIndexSQL). Both reads are cheap (the schema
// table and one row), so it runs on the open path. A case with no events is not
// "missing" them: the first import creates every index in its own transaction.
func timelineIndexesMissing(ctx context.Context, db queryRower) bool {
	var have int
	q := `SELECT count(*) FROM sqlite_master WHERE type='index' AND name IN ('` + strings.Join(timelineIndexNames, "','") + `')`
	if err := db.QueryRowContext(ctx, q).Scan(&have); err != nil || have == len(timelineIndexNames) {
		return false
	}
	var one int
	return db.QueryRowContext(ctx, `SELECT 1 FROM events LIMIT 1`).Scan(&one) == nil
}

// queryRower is the part of *sql.DB and *sql.Tx the index checks use.
type queryRower interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// timelineBuildHook, when set (tests only), runs in the background build once it
// holds the import lock, before it builds anything.
var timelineBuildHook func()

// timelineBuildFault, when set (tests only), makes the build fail with the error it
// returns, in place of running the index statements.
var timelineBuildFault func() error

// timelineBuildRuns counts the builds that got as far as running the index
// statements (tests read the delta).
var timelineBuildRuns atomic.Int64

// timelineFailed remembers, per process and per case file (cacheKey), a timeline
// index build that failed or had no room, as attrIdx=2 does for the series-key
// indexes, except that it outlives the case's handle: requests open and close the
// case all the time, and each open would otherwise start the same failing scan again.
// It is cleared when the case is erased, freed or found damaged (verifyCache.drop).
var timelineFailed sync.Map

// timelineMinHeadroom is the least free room a build wants, however small the case.
const timelineMinHeadroom = 16 << 20

// timelineBuildStopWait bounds how long Begin/Finish Free and BeginArchive wait for
// a cancelled build to let go of the case.
var timelineBuildStopWait = 30 * time.Second

// tlBuild is the one background timeline index build of a case.
type tlBuild struct {
	stop chan struct{} // closed to cancel the build
	once sync.Once
	done chan struct{} // closed when the build has finished and released its reference
}

func (b *tlBuild) cancel() { b.once.Do(func() { close(b.stop) }) }

// timelineHeadroomOK says whether there is room for the indexes: the case's files,
// a quarter over, must fit under the per-case database cap and in the free space of
// the volume (the indexes cost about 15% of the database). Without that a build on a
// case near its cap or on a full disk would scan every row and then fail.
func (c *caseState) timelineHeadroomOK() bool {
	size := dirBytes(c.dir)
	need := max(size/4, timelineMinHeadroom)
	if c.s.opt.MaxDBBytes-size < need {
		return false
	}
	if free, err := freeBytes(c.s.root); err == nil && free < uint64(need) {
		return false
	}
	return true
}

// startTimelineBuild starts the background build for this case unless one is
// already running, the case is going away, or a build failed in this process before.
// The build holds a reference of its own: requests that open and close the case
// would otherwise shut it down (and cancel the build) long before a large case is
// indexed, and it would never finish. Erase, Store.Close, Begin/Finish Free and
// BeginArchive cancel it.
func (c *caseState) startTimelineBuild() {
	if _, failed := timelineFailed.Load(cacheKey(c.dir)); failed {
		return
	}
	c.mu.Lock()
	if c.db == nil || c.bg != nil {
		c.mu.Unlock()
		return
	}
	b := &tlBuild{stop: make(chan struct{}), done: make(chan struct{})}
	c.bg = b
	c.refs++
	c.mu.Unlock()
	go func() {
		bg := &Case{caseState: c}
		bg.ensureTimelineIndexes()
		_ = bg.Close()
		c.mu.Lock()
		if c.bg == b {
			c.bg = nil
		}
		c.mu.Unlock()
		close(b.done)
	}()
}

// stopTimelineBuild cancels the case's background build, if it has one open in this
// process, and waits for it to let go (at most timelineBuildStopWait, or until ctx
// ends). The lifecycle moves that need the case idle call it first.
func (s *Store) stopTimelineBuild(ctx context.Context, id casestore.CaseID) {
	s.mu.Lock()
	st := s.open[id]
	s.mu.Unlock()
	if st == nil {
		return
	}
	st.mu.Lock()
	b := st.bg
	st.mu.Unlock()
	if b == nil {
		return
	}
	b.cancel()
	t := time.NewTimer(timelineBuildStopWait)
	defer t.Stop()
	select {
	case <-b.done:
	case <-ctx.Done():
	case <-t.C:
	}
}

// ensureTimelineIndexes builds the timeline indexes of a case that was written
// without them. startTimelineBuild runs it in the background, so opening a large old
// case stays fast; until it finishes the timeline queries run as before, only
// slower. It reads every event of the case once (seconds on a million rows), holds
// the import lock so an import or a shutdown waits (shutdown cancels the build),
// and never fails a query: when it cannot finish (no room, no space, busy) it logs
// and gives up for the rest of the process (timelineFailed); a cancelled build
// (closing, freeing, archiving) is tried again at the next open.
func (c *Case) ensureTimelineIndexes() {
	key := cacheKey(c.dir)
	if _, failed := timelineFailed.Load(key); failed {
		return
	}
	if !c.importMu.TryLock() {
		return
	}
	defer c.importMu.Unlock()
	db, err := c.handle()
	if err != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.mu.Lock()
	b := c.bg
	c.mu.Unlock()
	var stop chan struct{} // nil blocks forever: no build record, nothing to wait on
	if b != nil {
		stop = b.stop
	}
	go func() {
		select {
		case <-c.quit:
			cancel()
		case <-stop:
			cancel()
		case <-ctx.Done():
		}
	}()
	if timelineBuildHook != nil {
		timelineBuildHook()
	}
	if ctx.Err() != nil || c.archiveRefusal() != nil || !timelineIndexesMissing(ctx, db) {
		return
	}
	if !c.timelineHeadroomOK() {
		timelineFailed.Store(key, struct{}{})
		c.s.logf("case %s timeline indexes not built: not enough room", c.id)
		return
	}
	fail := func(err error) {
		if ctx.Err() == nil {
			timelineFailed.Store(key, struct{}{})
			c.s.logf("case %s timeline index build failed: %v", c.id, err)
		}
	}
	timelineBuildRuns.Add(1)
	if f := timelineBuildFault; f != nil {
		if err := f(); err != nil {
			fail(err)
			return
		}
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		fail(err)
		return
	}
	if _, err := tx.ExecContext(ctx, timelineIndexSQL); err != nil {
		_ = tx.Rollback()
		fail(err)
		return
	}
	if err := tx.Commit(); err != nil {
		fail(err)
		return
	}
	_, _ = db.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)")
}

// timelineLate is the import's question: must the timeline indexes be built after
// this import commits instead of inside it? They are created inside it when they
// already exist (the statement then does nothing) or the case had no events before
// (cheap: only this file's rows). A case with earlier events and no indexes is left
// to the background build, so a tiny import into a case near its size cap does not
// fail because the indexes could not be made.
func timelineLate(ctx context.Context, tx queryRower, importID int64) bool {
	var have int
	q := `SELECT count(*) FROM sqlite_master WHERE type='index' AND name IN ('` + strings.Join(timelineIndexNames, "','") + `')`
	if err := tx.QueryRowContext(ctx, q).Scan(&have); err != nil || have == len(timelineIndexNames) {
		return false
	}
	var one int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM events WHERE import_id<>? LIMIT 1`, importID).Scan(&one)
	return err == nil // a row from an earlier import: the case is not new
}

// mergeBucketClientErrors is mergeBucketErrors for the 4xx cells: it sets
// ClientErrors on the cells of counts from cl.
func mergeBucketClientErrors(counts, cl []casestore.BucketCount) {
	type cell struct {
		idx int
		src string
		imp int64
	}
	if len(cl) == 0 {
		return
	}
	at := make(map[cell]int, len(counts))
	for i, bc := range counts {
		at[cell{bc.Index, bc.Source, bc.ImportID}] = i
	}
	for _, e := range cl {
		if i, ok := at[cell{e.Index, e.Source, e.ImportID}]; ok {
			counts[i].ClientErrors = min(e.Count, counts[i].Count)
		}
	}
}

// mergeBucketErrors sets Errors on the cells of counts from errs, the cells of
// the error query (their Count is the number of errors). Both come from the same
// window and filters, so every error cell is also a count cell.
func mergeBucketErrors(counts, errs []casestore.BucketCount) {
	type cell struct {
		idx int
		src string
		imp int64
	}
	if len(errs) == 0 {
		return
	}
	at := make(map[cell]int, len(counts))
	for i, bc := range counts {
		at[cell{bc.Index, bc.Source, bc.ImportID}] = i
	}
	for _, e := range errs {
		if i, ok := at[cell{e.Index, e.Source, e.ImportID}]; ok {
			counts[i].Errors = min(e.Count, counts[i].Count) // never more errors than events in a cell
		}
	}
}
