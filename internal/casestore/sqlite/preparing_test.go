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
	time.Sleep(50 * time.Millisecond)
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

// A build that cannot run (an import holds the case) still ends the "preparing"
// state; it must never stay on for good.
func TestPreparingEndsWhenTheBuildCannotRun(t *testing.T) {
	s, _ := newTestStore(t)
	info := mustCreate(t, s, "busy")
	c := mustOpen(t, s, info.ID)
	if _, err := c.Import(context.Background(), tMeta("a.csv"), tBody("a"), tRun(tEvents(c, "a", 10))); err != nil {
		t.Fatal(err)
	}
	dropAttrIndexes(t, c)
	c.attrIdx.Store(0)
	c.preparing.Store(true)
	c.importMu.Lock() // an import is running: the build gives up at once
	c.ensureAttrIndexes()
	c.importMu.Unlock()
	if c.Preparing() {
		t.Fatal("still preparing after the build gave up")
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
