package sqlite

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

// A cap is reported in its narrowest kind: the cap of one file is
// ErrFileCapExceeded, the cap of the case ErrCaseCapExceeded, the rest plain
// ErrCapExceeded. Every kind still satisfies ErrCapExceeded (PF-B-07).
func TestCapKinds(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name         string
		opt          func(*Options)
		body         func() io.Reader
		n            int
		file, caseOf bool
	}{
		{"file bytes, streamed", func(o *Options) { o.MaxFileBytes = 1000 }, func() io.Reader { return bytes.NewReader(make([]byte, 5000)) }, 3, true, false},
		{"file bytes, reported size", func(o *Options) { o.MaxFileBytes = 1000 }, func() io.Reader { return sized(50_000) }, 3, true, false},
		{"file rows", func(o *Options) { o.MaxFileRows = 5 }, func() io.Reader { return tBody("rows") }, 10, true, false},
		{"database size inside sqlite", func(o *Options) { o.MaxDBBytes = 256 << 10 }, func() io.Reader { return tBody("big") }, 6000, false, true},
		{"case bytes, reported size", func(o *Options) { o.MaxCaseBytes = 256 << 10 }, func() io.Reader { return sized(1 << 20) }, 3, false, true},
		{"all cases bytes", func(o *Options) { o.MaxTotalBytes = 1 }, func() io.Reader { return tBody("total") }, 3, false, false},
		{"free disk floor", func(o *Options) { o.MinFreeBytes = 1 << 60 }, func() io.Reader { return tBody("disk") }, 3, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := newTestStore(t, tc.opt)
			c := mustOpen(t, s, mustCreate(t, s, "Case").ID)
			_, err := c.Import(ctx, tMeta("cap.csv"), tc.body(), tRun(tEvents(c, "cap", tc.n)))
			if !errors.Is(err, casestore.ErrCapExceeded) {
				t.Fatalf("Import = %v, want ErrCapExceeded", err)
			}
			if got := errors.Is(err, casestore.ErrFileCapExceeded); got != tc.file {
				t.Errorf("is file cap = %v, want %v (%v)", got, tc.file, err)
			}
			if got := errors.Is(err, casestore.ErrCaseCapExceeded); got != tc.caseOf {
				t.Errorf("is case cap = %v, want %v (%v)", got, tc.caseOf, err)
			}
		})
	}
}

// blockingImport starts an import whose importer stops after reading the body
// until release is closed, and returns once the import holds the case. The
// answer of the import arrives on the returned channel.
func blockingImport(t *testing.T, c *Case, label string) (release chan struct{}, done chan error) {
	t.Helper()
	release, done = make(chan struct{}), make(chan error, 1)
	started := make(chan struct{})
	evs := tEvents(c, label, 3)
	run := func(r io.Reader, emit func(evidence.Event) error) (importer.Report, error) {
		close(started)
		<-release
		return tRun(evs)(r, emit)
	}
	go func() {
		_, err := c.Import(context.Background(), tMeta(label+".csv"), tBody(label), run)
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("the import did not start")
	}
	return release, done
}

// While an import holds the case, the other writers answer ErrImportRunning at
// once instead of waiting for the import's folder lock, and a second import
// after the short wait; when the import ends they work again (PF-B-06).
func TestWritersFailFastWhileAnImportRuns(t *testing.T) {
	old := importWaitMax
	importWaitMax = 100 * time.Millisecond
	t.Cleanup(func() { importWaitMax = old })

	ctx := context.Background()
	s, _ := newTestStore(t)
	info := mustCreate(t, s, "Case")
	c := mustOpen(t, s, info.ID)
	other := mustOpen(t, s, info.ID) // a second handle of the same case

	release, done := blockingImport(t, c, "long")
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()

	// A call that blocks on the import fails the test after a bound instead of
	// hanging it (the deferred release lets the import end).
	fast := func(name string, call func() error) {
		t.Helper()
		start := time.Now()
		answer := make(chan error, 1)
		go func() { answer <- call() }()
		var err error
		select {
		case err = <-answer:
		case <-time.After(10 * time.Second):
			t.Errorf("%s blocked on the running import", name)
			return
		}
		if !errors.Is(err, casestore.ErrImportRunning) {
			t.Errorf("%s = %v, want ErrImportRunning", name, err)
		}
		if d := time.Since(start); d > 1500*time.Millisecond {
			t.Errorf("%s took %v, want it to fail fast", name, d)
		}
	}
	fast("workspace save", func() error { return other.SetWorkspaceBlob(ctx, []byte(`{"version":1}`)) })
	fast("set environments", func() error { return other.SetEnvs(ctx, []string{"prod"}) })
	fast("close case", func() error { return s.CloseCase(ctx, info.ID) })
	fast("second import", func() error {
		_, err := other.Import(ctx, tMeta("second.csv"), tBody("second"), tRun(tEvents(other, "second", 2)))
		return err
	})
	// Reads are not held up.
	if _, err := other.WorkspaceBlob(ctx); err != nil {
		t.Errorf("read during the import = %v", err)
	}

	close(release)
	released = true
	if err := <-done; err != nil {
		t.Fatalf("the running import failed: %v", err)
	}
	if err := other.SetWorkspaceBlob(ctx, []byte(`{"version":1}`)); err != nil {
		t.Fatalf("workspace save after the import = %v", err)
	}
	if _, events, _ := counts(t, c); events != 3 {
		t.Errorf("events = %d, want 3 (the refused second import must store nothing)", events)
	}
	if err := s.CloseCase(ctx, info.ID); err != nil {
		t.Fatalf("close after the import = %v", err)
	}
}

// A second import that finds the first finished inside the wait goes on instead
// of failing.
func TestSecondImportWaitsForAShortFirst(t *testing.T) {
	s, _ := newTestStore(t)
	c := mustOpen(t, s, mustCreate(t, s, "Case").ID)
	release, done := blockingImport(t, c, "first")
	go func() {
		time.Sleep(50 * time.Millisecond)
		close(release)
	}()
	res, err := c.Import(context.Background(), tMeta("next.csv"), tBody("next"), tRun(tEvents(c, "next", 2)))
	if err != nil {
		t.Fatalf("second import = %v, want it to wait for the first", err)
	}
	if res.Record.Rows != 2 {
		t.Errorf("second import stored %d rows", res.Record.Rows)
	}
	if err := <-done; err != nil {
		t.Fatalf("first import: %v", err)
	}
}

// An import that finds importMu taken before the holder published importRun (the
// holder is still pending in lockImport) is bounded like any other: it answers
// ErrImportRunning after the wait instead of blocking on the mutex.
func TestLockImportBoundsTheWaitInTheGap(t *testing.T) {
	old := importWaitMax
	importWaitMax = 50 * time.Millisecond
	t.Cleanup(func() { importWaitMax = old })

	s, _ := newTestStore(t)
	c := mustOpen(t, s, mustCreate(t, s, "Case").ID)
	c.importMu.Lock() // the winner of the TryLock race ...
	c.importPend.Add(1)
	defer func() {
		c.importPend.Add(-1) // ... which has not published importRun yet
		c.importMu.Unlock()
	}()

	answer := make(chan error, 1)
	go func() { answer <- c.lockImport(context.Background()) }()
	select {
	case err := <-answer:
		if !errors.Is(err, casestore.ErrImportRunning) {
			t.Fatalf("lockImport = %v, want ErrImportRunning", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("lockImport blocked on importMu instead of answering ErrImportRunning")
	}
	if n := c.importPend.Load(); n != 1 {
		t.Errorf("importPend = %d after the refusal, want the holder's 1", n)
	}
}
