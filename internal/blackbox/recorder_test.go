package blackbox

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeCollector struct {
	mu     sync.Mutex
	err    error
	closed bool
	n      int
}

func (f *fakeCollector) Collect(now time.Time) (Sample, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return Sample{}, f.err
	}
	f.n++
	v := 10.0
	return Sample{CPU: &v}, nil
}
func (f *fakeCollector) Close() error { f.closed = true; return nil }

// pctDisk reports a fixed free percentage of a 100-byte volume.
type pctDisk struct {
	mu  sync.Mutex
	pct float64
}

func (d *pctDisk) set(p float64) { d.mu.Lock(); d.pct = p; d.mu.Unlock() }
func (d *pctDisk) Free(string) (DiskInfo, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return DiskInfo{Free: uint64(d.pct), Total: 100}, nil
}

type rig struct {
	r    *Recorder
	s    *Store
	col  *fakeCollector
	disk *pctDisk
	clk  *clock
	mono time.Duration
}

func newRig(t *testing.T, mut func(*Config)) *rig {
	t.Helper()
	cfg := Default()
	cfg.Name = "box"
	cfg.Folder = t.TempDir()
	cfg.Record.LogsCatalogue = false
	if mut != nil {
		mut(&cfg)
	}
	g := &rig{clk: &clock{t: t0}, col: &fakeCollector{}, disk: &pctDisk{pct: 80}}
	g.s = openT(t, cfg.Folder, "run1", g.clk)
	g.r = NewRecorder(RecorderOptions{Config: cfg, Store: g.s, Collector: g.col, Disk: g.disk, RunID: "run1",
		Now: g.clk.now, Mono: func() time.Duration { return g.mono }})
	return g
}

// at moves both clocks by d and ticks.
func (g *rig) tick(d time.Duration) {
	g.clk.t = g.clk.t.Add(d)
	g.mono += d
	g.r.Tick(g.clk.t)
}

func TestRecorderSamplesEveryTick(t *testing.T) {
	g := newRig(t, nil)
	g.r.Tick(g.clk.t)
	for i := 0; i < 3; i++ {
		g.tick(15 * time.Second)
	}
	m := g.s.Manifest()
	if m.SampleCount() != 4 || len(m.Gaps) != 0 {
		t.Fatalf("samples %d gaps %v", m.SampleCount(), m.Gaps)
	}
	st := g.r.Status(g.clk.t)
	if st.State != StateRunning || st.Seq != 4 || !st.FreeKnown || st.FreePercent != 80 {
		t.Fatalf("%+v", st)
	}
}

func TestRecorderRunningLate(t *testing.T) {
	g := newRig(t, nil)
	g.r.Tick(g.clk.t)
	if g.r.Status(g.clk.t.Add(45*time.Second)).State != StateRunning {
		t.Fatal("45 s is on time")
	}
	if g.r.Status(g.clk.t.Add(46*time.Second)).State != StateRunningLate {
		t.Fatal("46 s is late")
	}
}

func TestRecorderNoSampleYetIsLateAfterThreeIntervals(t *testing.T) {
	g := newRig(t, nil)
	if g.r.Status(t0.Add(30*time.Second)).State != StateRunning {
		t.Fatal("running")
	}
	if g.r.Status(t0.Add(60*time.Second)).State != StateRunningLate {
		t.Fatal("late")
	}
}

func TestRecorderDiskLowPausesAndResumesWithGapCause(t *testing.T) {
	g := newRig(t, nil)
	g.r.Tick(g.clk.t) // sample at t0
	g.disk.set(5)
	g.tick(60 * time.Second) // housekeeping sees 5% < 10% and nothing to remove: pause
	if g.r.Status(g.clk.t).State != StateDiskLow {
		t.Fatal("not paused")
	}
	g.tick(60 * time.Second)
	if g.s.Manifest().SampleCount() != 1 {
		t.Fatalf("samples written while paused: %d", g.s.Manifest().SampleCount())
	}
	g.disk.set(50)
	g.tick(60 * time.Second)
	m := g.s.Manifest()
	if m.SampleCount() != 2 {
		t.Fatalf("no sample after resume: %d", m.SampleCount())
	}
	if len(m.Gaps) != 1 || m.Gaps[0].Cause != CauseDiskLow || m.Gaps[0].Seconds != 180 {
		t.Fatalf("%+v", m.Gaps)
	}
	if !hasEvent(m, EventPausedDiskLow) || !hasEvent(m, EventResumed) {
		t.Fatalf("events %+v", m.Events)
	}
	if g.r.Status(g.clk.t).State != StateRunning {
		t.Fatal("not running again")
	}
}

func TestRecorderClockJumpIsAnEvent(t *testing.T) {
	g := newRig(t, nil)
	g.r.Tick(g.clk.t)
	// The steady clock says 15 s passed; the wall clock moved 20 s (5 s jump).
	g.clk.t = g.clk.t.Add(20 * time.Second)
	g.mono += 15 * time.Second
	g.r.Tick(g.clk.t)
	found := false
	for _, e := range g.s.Manifest().Events {
		if e.Kind == EventClockJump && e.Detail == "clock moved forward by 5.0 s" {
			found = true
		}
	}
	if !found {
		t.Fatalf("events %+v", g.s.Manifest().Events)
	}
	// A normal tick adds none.
	before := len(g.s.Manifest().Events)
	g.tick(15 * time.Second)
	if len(g.s.Manifest().Events) != before {
		t.Fatal("event without a jump")
	}
}

func TestRecorderClockMovedBack(t *testing.T) {
	g := newRig(t, nil)
	g.r.Tick(g.clk.t)
	g.clk.t = g.clk.t.Add(-10 * time.Second)
	g.mono += 15 * time.Second
	g.r.Tick(g.clk.t)
	m := g.s.Manifest()
	if !hasEvent(m, EventClockJump) || !hasEvent(m, EventClockBack) {
		t.Fatalf("events %+v", m.Events)
	}
	if m.SampleCount() != 2 {
		t.Fatal("sample lost")
	}
}

func TestRecorderCollectFailureLoggedOnceAndRecovers(t *testing.T) {
	g := newRig(t, nil)
	g.col.err = errors.New("counters unavailable")
	g.r.Tick(g.clk.t)
	g.tick(15 * time.Second)
	g.tick(15 * time.Second)
	n := 0
	for _, e := range g.s.Manifest().Events {
		if e.Kind == EventCollectFailed {
			n++
		}
	}
	if n != 1 || g.s.Manifest().SampleCount() != 0 {
		t.Fatalf("events %d samples %d", n, g.s.Manifest().SampleCount())
	}
	g.col.err = nil
	g.tick(15 * time.Second)
	if g.s.Manifest().SampleCount() != 1 {
		t.Fatal("did not recover")
	}
}

func TestRecorderWriteFailureKeepsGoing(t *testing.T) {
	g := newRig(t, nil)
	g.r.Tick(g.clk.t)
	// Break the open file so the next write fails.
	g.s.mu.Lock()
	_ = g.s.cur.Close()
	g.s.mu.Unlock()
	g.tick(15 * time.Second)
	if g.r.Status(g.clk.t).WriteOK {
		t.Fatal("write failure not reported")
	}
	if !hasEvent(g.s.Manifest(), EventWriteFailed) {
		t.Fatal("event")
	}
	// The store reopens the file on the next sample, once the hour changes or
	// the handle is replaced; force that.
	g.s.mu.Lock()
	g.s.cur, g.s.curN = nil, ""
	g.s.mu.Unlock()
	g.tick(15 * time.Second)
	if !g.r.Status(g.clk.t).WriteOK || g.s.Manifest().SampleCount() != 2 {
		t.Fatalf("no recovery: %d", g.s.Manifest().SampleCount())
	}
}

func TestRecorderLogsCatalogueOnFirstAndEveryFiveMinutes(t *testing.T) {
	dir := t.TempDir()
	writeLog(t, dir, "a.log")
	g := newRig(t, func(c *Config) { c.Record.LogsCatalogue = true; c.LogPaths = []string{dir} })
	g.r.Tick(g.clk.t)
	g.tick(15 * time.Second)
	g.tick(5 * time.Minute)
	smp := readSamples(t, g.s.Dir())
	withLogs := 0
	for _, s := range smp {
		if len(s.Logs) > 0 {
			withLogs++
		}
	}
	if len(smp) != 3 || withLogs != 2 {
		t.Fatalf("samples %d withLogs %d", len(smp), withLogs)
	}
}

func TestRecorderRunStopsCleanly(t *testing.T) {
	g := newRig(t, nil)
	ticks := make(chan time.Time)
	g.r.o.Ticks = ticks
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- g.r.Run(ctx) }()
	ticks <- g.clk.t
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	m := g.s.Manifest()
	if m.State != ManifestStopped || m.Runs[len(m.Runs)-1].Reason != "stopped" || !g.col.closed {
		t.Fatalf("%+v", m)
	}
	if g.r.Status(g.clk.t).State != StateStopped {
		t.Fatal("state")
	}
	if m.SampleCount() < 1 {
		t.Fatal("no first sample")
	}
}
