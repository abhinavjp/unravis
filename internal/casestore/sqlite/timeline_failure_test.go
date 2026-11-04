package sqlite

// Fix loop 3: a timeline index build that failed or had no room is remembered for
// the process (no scan, no write lock on every later open), and an Import never
// fails because the timeline indexes could not be made.

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
)

// oldCase makes a case with events and no timeline indexes, closed.
func oldCase(t *testing.T, s *Store, n int) casestore.CaseID {
	t.Helper()
	info := mustCreate(t, s, "old")
	c := mustOpen(t, s, info.ID)
	importStatuses(t, c, "imp-a", n)
	dropTimelineIndexes(t, c)
	if _, err := c.db.Exec("VACUUM"); err != nil { // hand the freed pages back: the file is as an old build left it
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	return info.ID
}

// waitNoBuild waits until the case has no background build running.
func waitNoBuild(t *testing.T, c *Case) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		b := c.bg
		c.mu.Unlock()
		if b == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the background build did not end")
}

func TestFailedTimelineBuildIsNotRepeated(t *testing.T) {
	s, _ := newTestStore(t)
	id := oldCase(t, s, 60)

	var hooks atomic.Int64
	timelineBuildHook = func() { hooks.Add(1) }
	timelineBuildFault = func() error { return errors.New("injected failure") }
	t.Cleanup(func() { timelineBuildHook, timelineBuildFault = nil, nil })

	runs := timelineBuildRuns.Load()
	c := mustOpen(t, s, id)
	waitNoBuild(t, c)
	if got := timelineBuildRuns.Load() - runs; got != 1 {
		t.Fatalf("first open: %d builds, want 1", got)
	}
	if hooks.Load() != 1 {
		t.Fatalf("first open: build lock taken %d times, want 1", hooks.Load())
	}
	_ = c.Close()

	// Later opens (each request opens the case) must not start another build, not
	// even take the import lock for one.
	for i := 0; i < 4; i++ {
		c = mustOpen(t, s, id)
		time.Sleep(20 * time.Millisecond)
		waitNoBuild(t, c)
		_ = c.Close()
	}
	if got := timelineBuildRuns.Load() - runs; got != 1 {
		t.Fatalf("after retries: %d builds, want 1", got)
	}
	if hooks.Load() != 1 {
		t.Fatalf("after retries: the build took the write lock %d times, want 1", hooks.Load())
	}

	// An import into this case still succeeds, leaves the indexes to nobody, and
	// does not start the failed build again.
	c = mustOpen(t, s, id)
	mustImport(t, c, "more", 5)
	waitNoBuild(t, c)
	if got := indexNames(t, c); len(got) != 0 {
		t.Fatalf("indexes = %v, want none after the failed build", got)
	}
	if n := scalar(t, c, "SELECT COUNT(*) FROM events"); n != 65 {
		t.Fatalf("%d events, want 65", n)
	}
	if got := timelineBuildRuns.Load() - runs; got != 1 {
		t.Fatalf("after the import: %d builds, want 1", got)
	}
	_ = c.Close()
}

// A case near its size cap: the build is skipped for lack of room (remembered), and
// a tiny import still works. Before the fix the import created the timeline indexes
// inside its own transaction and failed with the case cap.
func TestNearCapCaseSkipsTheBuildAndStillImports(t *testing.T) {
	s, root := newTestStore(t)
	id := oldCase(t, s, 3000)
	dir := filepath.Join(root, string(id))
	size := dirBytes(dir)
	// An open handle adds its WAL to the folder (about 65 KB); what is left is room for a few
	// pages but not for the indexes of 3000 events (about 200 KB).
	tight := newStoreAt(t, root, s.opt.Protector, func(o *Options) { o.MaxDBBytes = size + 100<<10 })

	var hooks atomic.Int64
	timelineBuildHook = func() { hooks.Add(1) }
	t.Cleanup(func() { timelineBuildHook = nil })
	runs := timelineBuildRuns.Load()

	ci, err := tight.Open(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	c := ci.(*Case)
	waitNoBuild(t, c)
	if got := timelineBuildRuns.Load() - runs; got != 0 {
		t.Fatalf("a build without room ran its statements %d times", got)
	}
	if _, failed := timelineFailed.Load(cacheKey(dir)); !failed {
		t.Fatal("the lack of room was not remembered")
	}
	// The import (a few rows) must not fail on the indexes it cannot afford.
	if _, err := c.Import(context.Background(), tMeta("tiny.csv"), tBody("tiny"), tRun(tEvents(c, "tiny", 3))); err != nil {
		t.Fatalf("tiny import into a near-cap case: %v", err)
	}
	if n := scalar(t, c, "SELECT COUNT(*) FROM events"); n != 3003 {
		t.Fatalf("%d events, want 3003", n)
	}
	waitNoBuild(t, c)
	_ = c.Close()

	// Reopens neither build nor take the lock again.
	seen := hooks.Load()
	for i := 0; i < 3; i++ {
		again, err := tight.Open(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(20 * time.Millisecond)
		_ = again.Close()
	}
	if hooks.Load() != seen {
		t.Fatalf("reopens took the build lock %d more times", hooks.Load()-seen)
	}
	if got := timelineBuildRuns.Load() - runs; got != 0 {
		t.Fatalf("reopens ran %d builds", got)
	}
}

// Begin/Finish Free and BeginArchive call stopTimelineBuild: it cancels the build, which holds a
// reference of its own, and waits for it to let go (before the fix: "in use" until it ended).
func TestStopTimelineBuildCancelsAndWaits(t *testing.T) {
	s, _ := newTestStore(t)
	id := oldCase(t, s, 60)

	entered := make(chan struct{})
	timelineBuildHook = func() {
		close(entered)
		time.Sleep(200 * time.Millisecond) // the build is slow to start; cancellation must not wait for it
	}
	t.Cleanup(func() { timelineBuildHook = nil })

	c := mustOpen(t, s, id)
	<-entered
	_ = c.Close() // the build's own reference is the only one left
	s.mu.Lock()
	st := s.open[id]
	s.mu.Unlock()
	if st == nil {
		t.Skip("the build ended before the case could be freed")
	}
	// The build ends by itself (cancelled); stopTimelineBuild returns once it has.
	done := make(chan struct{})
	go func() {
		s.stopTimelineBuild(context.Background(), id)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("stopTimelineBuild did not return")
	}
	st.mu.Lock()
	b, refs := st.bg, st.refs
	st.mu.Unlock()
	if b != nil || refs != 0 {
		t.Fatalf("after the stop: build %v, refs %d", b, refs)
	}
}

// An Import waiting behind the build gives up when its context ends.
func TestImportBehindTheBuildIsCancellable(t *testing.T) {
	s, _ := newTestStore(t)
	id := oldCase(t, s, 60)

	entered, release := make(chan struct{}), make(chan struct{})
	timelineBuildHook = func() {
		close(entered)
		<-release
	}
	t.Cleanup(func() { timelineBuildHook = nil })
	var once atomic.Bool
	releaseOnce := func() {
		if once.CompareAndSwap(false, true) {
			close(release)
		}
	}
	t.Cleanup(releaseOnce)

	c := mustOpen(t, s, id)
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := c.Import(ctx, tMeta("wait.csv"), tBody("wait"), tRun(tEvents(c, "wait", 3)))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Import behind the build = %v, want context.DeadlineExceeded", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("the cancelled import waited %v", time.Since(start))
	}
	releaseOnce()
	waitIndexes(t, c, 2)
	mustImport(t, c, "after", 3)
	_ = c.Close()
}

// Events over several sources answers nil, as the single query does, when nothing matches.
func TestEventsSeveralSourcesEmptyIsNil(t *testing.T) {
	s, _ := newTestStore(t)
	c := mustOpen(t, s, mustCreate(t, s, "nil").ID)
	importStatuses(t, c, "imp-a", 10)
	evs, err := c.Events(context.Background(), casestore.EventQuery{Sources: []string{"none-1", "none-2"}, Limit: 100})
	if err != nil || evs != nil {
		t.Fatalf("Events = %#v, %v; want nil, nil", evs, err)
	}
	one, err := c.Events(context.Background(), casestore.EventQuery{Sources: []string{"none-1"}, Limit: 100})
	if err != nil || one != nil {
		t.Fatalf("one-source Events = %#v, %v; want nil, nil", one, err)
	}
	_ = c.Close()
}
