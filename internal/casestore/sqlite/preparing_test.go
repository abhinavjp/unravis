package sqlite

// W-34 (RV-DAT-3): an old case builds its helper indexes in the background after
// it is opened, and says so while it does ("Preparing this case"), so the first
// charts of a large case do not look stuck.

import (
	"context"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
)

var _ casestore.Preparer = (*Case)(nil)

func attrIndexCount(t *testing.T, c *Case) int {
	t.Helper()
	var n int
	if err := c.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='index' AND name IN ('ev_kind_counter','ev_kind_metric')`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func dropAttrIndexes(t *testing.T, c *Case) {
	t.Helper()
	for _, n := range []string{"ev_kind_counter", "ev_kind_metric"} {
		if _, err := c.db.Exec("DROP INDEX IF EXISTS " + n); err != nil {
			t.Fatal(err)
		}
	}
}

func waitNotPreparing(t *testing.T, c *Case) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if !c.Preparing() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("still preparing after 15 seconds")
}

// A case that has its indexes, new or already built, is never "preparing".
func TestNewCaseIsNotPreparing(t *testing.T) {
	s, _ := newTestStore(t)
	info := mustCreate(t, s, "new")
	c := mustOpen(t, s, info.ID)
	if c.Preparing() {
		t.Fatal("an empty new case says it is preparing")
	}
	if _, err := c.Import(context.Background(), tMeta("a.csv"), tBody("a"), tRun(tEvents(c, "a", 20))); err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
	c = mustOpen(t, s, info.ID)
	if c.Preparing() {
		t.Fatal("a case with all its indexes says it is preparing")
	}
	if n := attrIndexCount(t, c); n != 2 {
		t.Fatalf("series-key indexes = %d, want 2", n)
	}
}

// The failing-before test: an older case without the series-key indexes is
// preparing from the moment it opens until the build ends, and then it is not.
func TestOlderCaseSaysPreparingUntilItsSeriesIndexesAreBuilt(t *testing.T) {
	s, _ := newTestStore(t)
	info := mustCreate(t, s, "old")
	c := mustOpen(t, s, info.ID)
	if _, err := c.Import(context.Background(), tMeta("a.csv"), tBody("a"), tRun(tEvents(c, "a", 40))); err != nil {
		t.Fatal(err)
	}
	dropAttrIndexes(t, c)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}

	entered, release := make(chan struct{}), make(chan struct{})
	attrBuildHook = func() {
		close(entered)
		<-release
	}
	t.Cleanup(func() { attrBuildHook = nil })
	released := false
	releaseOnce := func() {
		if !released {
			released = true
			close(release)
		}
	}
	t.Cleanup(releaseOnce)

	c = mustOpen(t, s, info.ID)
	if !c.Preparing() {
		t.Fatal("the case does not say it is preparing right after the open")
	}
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the background build never started")
	}
	if !c.Preparing() {
		t.Fatal("the case stopped saying it is preparing while the build runs")
	}
	releaseOnce()
	waitNotPreparing(t, c)
	if n := attrIndexCount(t, c); n != 2 {
		t.Fatalf("series-key indexes after the build = %d, want 2", n)
	}
	if got := c.attrIdx.Load(); got != 1 {
		t.Fatalf("attrIdx = %d, want 1", got)
	}
}

// A query that finds an import running goes on without the indexes; it must not
// end the "preparing" note, because the background build has not run yet.
func TestQueryBehindAnImportDoesNotEndPreparing(t *testing.T) {
	s, _ := newTestStore(t)
	info := mustCreate(t, s, "busy")
	c := mustOpen(t, s, info.ID)
	if _, err := c.Import(context.Background(), tMeta("a.csv"), tBody("a"), tRun(tEvents(c, "a", 10))); err != nil {
		t.Fatal(err)
	}
	dropAttrIndexes(t, c)
	c.attrIdx.Store(0)
	c.preparing.Store(true)
	c.importMu.Lock() // an import is running
	busy := c.ensureAttrIndexes()
	c.importMu.Unlock()
	if !busy {
		t.Fatal("a build behind a running import did not say it was busy")
	}
	if !c.Preparing() {
		t.Fatal("a query that found the import lock held ended the preparing note")
	}
}

// The background build behind a running import waits and tries again: the note
// stays up while the import runs and ends once the indexes are built.
func TestBackgroundBuildWaitsForAnImportThenEndsPreparing(t *testing.T) {
	s, _ := newTestStore(t)
	info := mustCreate(t, s, "wait")
	c := mustOpen(t, s, info.ID)
	if _, err := c.Import(context.Background(), tMeta("a.csv"), tBody("a"), tRun(tEvents(c, "a", 10))); err != nil {
		t.Fatal(err)
	}
	dropAttrIndexes(t, c)
	c.attrIdx.Store(0)
	c.importMu.Lock() // an import is running
	c.startAttrBuild()
	time.Sleep(3 * attrBuildRetry) // several turns of the retry
	if !c.Preparing() {
		t.Fatal("the note ended while an import kept the build waiting")
	}
	if n := attrIndexCount(t, c); n != 0 {
		t.Fatalf("indexes built behind a running import: %d", n)
	}
	c.importMu.Unlock()
	waitNotPreparing(t, c)
	if n := attrIndexCount(t, c); n != 2 {
		t.Fatalf("series-key indexes after the import ended = %d, want 2", n)
	}
}

// The failing-before test for the review finding: the case page opens and closes
// the case on every request. Closing every handle of the case while its series-key
// build runs must not shut the case down and cut the build off: the build holds a
// reference of its own, so it finishes, the note ends, and the next open finds the
// indexes (it would have restarted the build every time before).
func TestCloseDuringTheBuildDoesNotCutItOff(t *testing.T) {
	s, _ := newTestStore(t)
	info := mustCreate(t, s, "cut")
	c := mustOpen(t, s, info.ID)
	if _, err := c.Import(context.Background(), tMeta("a.csv"), tBody("a"), tRun(tEvents(c, "a", 40))); err != nil {
		t.Fatal(err)
	}
	dropAttrIndexes(t, c)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}

	entered, release := make(chan struct{}), make(chan struct{})
	attrBuildHook = func() {
		close(entered)
		<-release
	}
	t.Cleanup(func() { attrBuildHook = nil })
	released := false
	releaseOnce := func() {
		if !released {
			released = true
			close(release)
		}
	}
	t.Cleanup(releaseOnce)

	c = mustOpen(t, s, info.ID)
	st := c.caseState
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the background build never started")
	}
	closed := make(chan error, 1)
	go func() { closed <- c.Close() }() // the only handle a request held
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("closing the request handle blocked: it shut the case down under its running build")
	}
	st.mu.Lock()
	alive := st.db != nil
	st.mu.Unlock()
	if !alive {
		t.Fatal("closing the last request handle shut the case down under its running build")
	}
	releaseOnce()
	deadline := time.Now().Add(15 * time.Second)
	for st.preparing.Load() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if st.preparing.Load() {
		t.Fatal("still preparing after the build was released")
	}
	// The build was the last handle: it has shut the case down by now. A new open
	// finds the indexes built, so it is not preparing and starts no build.
	deadline = time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		_, open := s.open[info.ID]
		s.mu.Unlock()
		if !open {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	c = mustOpen(t, s, info.ID)
	defer c.Close()
	if c.Preparing() {
		t.Fatal("the next open still says preparing: the build was cut off")
	}
	if n := attrIndexCount(t, c); n != 2 {
		t.Fatalf("series-key indexes after the close during the build = %d, want 2", n)
	}
}

// The timeline index build is background work too, and counts as preparing.
func TestTimelineBuildCountsAsPreparing(t *testing.T) {
	s, _ := newTestStore(t)
	info := mustCreate(t, s, "timeline")
	c := mustOpen(t, s, info.ID)
	importStatuses(t, c, "imp-a", 30)
	dropTimelineIndexes(t, c)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	timelineBuildHook = func() {
		close(entered)
		<-release
	}
	t.Cleanup(func() { timelineBuildHook = nil })
	released := false
	releaseOnce := func() {
		if !released {
			released = true
			close(release)
		}
	}
	t.Cleanup(releaseOnce)
	c = mustOpen(t, s, info.ID)
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the timeline build never started")
	}
	if !c.Preparing() {
		t.Fatal("not preparing while the timeline indexes are built")
	}
	releaseOnce()
	waitIndexes(t, c, 2)
	waitNotPreparing(t, c)
}
