package blackbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
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

// seedOld writes one-sample hour files at the given times into dir, as an
// earlier run would have, and closes that store.
func seedOld(t *testing.T, dir string, at ...time.Time) {
	t.Helper()
	c := &clock{t: at[0]}
	st := openT(t, dir, "run0", c)
	for i, a := range at {
		c.t = a
		if err := st.Append(smp("run0", uint64(i+1), a), ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Close("stopped"); err != nil {
		t.Fatal(err)
	}
}

// A reader holding the oldest file must not make a paused recorder start
// writing again onto a full drive (review F4).
func TestRecorderStaysPausedWhenHousekeepingCannotRemoveALockedFile(t *testing.T) {
	// 27 hourly files, so the oldest three are outside the keep floor (the
	// newest 25 files stay) and the free-space rule may remove them.
	var olds []time.Time
	for i := 0; i < 27; i++ {
		olds = append(olds, t0.Add(-time.Duration(60-i)*time.Hour))
	}
	old1, old2 := olds[0], olds[1]
	g := newRig(t, func(c *Config) { seedOld(t, c.Folder, olds...) })
	lockFiles(g.s, FileNameFor(old1))
	g.r.Tick(g.clk.t)
	g.disk.set(5) // below the 10% limit
	g.tick(60 * time.Second)
	// The unlocked old file went, the locked one stays, nothing else may go.
	if _, err := os.Stat(filepath.Join(g.s.Dir(), FileNameFor(old1))); err != nil {
		t.Fatal("the locked file should still be there")
	}
	if _, err := os.Stat(filepath.Join(g.s.Dir(), FileNameFor(old2))); err == nil {
		t.Fatal("the unlocked old file should have been removed")
	}
	if g.r.Status(g.clk.t).State != StateDiskLow {
		t.Fatalf("not paused: %s", g.r.Status(g.clk.t).State)
	}
	n := g.s.Manifest().SampleCount()
	for i := 0; i < 3; i++ {
		g.tick(60 * time.Second)
	}
	if g.s.Manifest().SampleCount() != n || g.r.Status(g.clk.t).State != StateDiskLow {
		t.Fatalf("recorder wrote or resumed while the disk is full: %d vs %d", g.s.Manifest().SampleCount(), n)
	}
	// Still low and the locked file still in the way: the event is logged once.
	cnt := 0
	for _, e := range g.s.Manifest().Events {
		if e.Kind == EventHousekeeping && strings.Contains(e.Detail, "could not remove") {
			cnt++
		}
	}
	if cnt != 1 {
		t.Fatalf("event logged %d times", cnt)
	}
}

// With no fresh free-space reading, an incomplete pass leaves the pause alone.
func TestRecorderIncompletePassWithoutDiskReadingKeepsPauseState(t *testing.T) {
	old1 := t0.Add(-30 * 24 * time.Hour) // older than keepDays: the age rule wants it
	g := newRig(t, func(c *Config) { seedOld(t, c.Folder, old1) })
	lockFiles(g.s, FileNameFor(old1))
	g.r.Tick(g.clk.t)
	g.disk.set(5)
	g.tick(60 * time.Second) // age rule is blocked by the lock; disk low, nothing removable: paused
	if g.r.Status(g.clk.t).State != StateDiskLow {
		t.Fatal("not paused")
	}
	g.r.o.Disk = &fakeDisk{err: errors.New("no reading")}
	g.tick(60 * time.Second)
	if g.r.Status(g.clk.t).State != StateDiskLow {
		t.Fatalf("pause lifted by an incomplete pass without a disk reading: %+v", g.s.Manifest().Events)
	}
}

// --- BBX-1-07: the budgeted tick loop ---

// TestTickFeedsBudget: a fake self-reading over budget must widen a real
// Budget's effective interval, and the tick must keep sampling.
func TestTickFeedsBudget(t *testing.T) {
	g := newRig(t, nil)
	b := NewBudget(15)
	g.r.o.Budget = b
	g.r.o.SelfUsage = func() (SelfReading, bool) { return rdOver, true }
	g.r.Tick(g.clk.t)
	// Two warm-up cycles, then two over windows of four ticks each.
	for i := 0; i < budgetWarmupCycles+budgetWindowTicks*budgetOverWindows-1; i++ {
		g.tick(15 * time.Second)
	}
	if got := b.EffectiveInterval(); got != 30 {
		t.Fatalf("effective interval = %d, want widened to 30", got)
	}
	if n := g.s.Manifest().SampleCount(); n != budgetWarmupCycles+budgetWindowTicks*budgetOverWindows {
		t.Fatalf("samples %d: the budget must not block the tick", n)
	}
	// The block of the run record: the self reading and the tick cost, and the
	// budget state that survives a restart.
	m := g.s.Manifest()
	if len(m.Runs) == 0 || m.Runs[len(m.Runs)-1].Self == nil {
		t.Fatalf("no self block in the run record: %+v", m.Runs)
	}
	if m.Budget == nil || m.Budget.Effective != 30 || m.Budget.Base != 15 {
		t.Fatalf("budget state not kept in the manifest: %+v", m.Budget)
	}
}

// TestTickBudgetNilSafe: nil Budget and nil SelfUsage leave the tick unchanged;
// a Budget with nil SelfUsage still cycles without a measurement.
func TestTickBudgetNilSafe(t *testing.T) {
	g := newRig(t, nil)
	g.r.Tick(g.clk.t)
	g.tick(15 * time.Second)
	if n := g.s.Manifest().SampleCount(); n != 2 {
		t.Fatalf("samples %d, want 2", n)
	}
	b := NewBudget(15)
	g.r.o.Budget = b // SelfUsage stays nil
	g.tick(15 * time.Second)
	if got := b.EffectiveInterval(); got != 15 {
		t.Fatalf("effective interval = %d, want base 15", got)
	}
	if n := g.s.Manifest().SampleCount(); n != 3 {
		t.Fatalf("samples %d, want 3", n)
	}
}

// TestTickMissingSelfReadingIsCalm: a missing reading (ok=false) is never
// 0-as-fact and never widens the interval; the tick keeps sampling.
func TestTickMissingSelfReadingIsCalm(t *testing.T) {
	g := newRig(t, nil)
	b := NewBudget(15)
	g.r.o.Budget = b
	g.r.o.SelfUsage = func() (SelfReading, bool) { return SelfReading{}, false }
	g.r.Tick(g.clk.t)
	for i := 0; i < 16; i++ {
		g.tick(15 * time.Second)
	}
	if got := b.EffectiveInterval(); got != 15 {
		t.Fatalf("effective interval = %d, want 15: a missing reading must count as calm", got)
	}
	if n := g.s.Manifest().SampleCount(); n != 17 {
		t.Fatalf("samples %d, want 17", n)
	}
}

// TestEffectiveInterval table-tests the helper Run uses for the ticker reset.
func TestEffectiveInterval(t *testing.T) {
	widened := NewBudget(5)
	widenBudget(widened, t0, 1) // 5 -> 15
	cases := []struct {
		name string
		b    *Budget
		base int
		want int
	}{
		{"nil budget falls back to base", nil, 15, 15},
		{"fresh budget is at base", NewBudget(30), 30, 30},
		{"widened budget wins over base", widened, 5, 15},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := effectiveInterval(tc.b, tc.base); got != tc.want {
				t.Fatalf("effectiveInterval = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestRunFollowsEffectiveInterval: with the own timer (not o.Ticks), the first
// tick is at once and the next one is a whole effective interval later, so a
// widened budget is followed at the first tick and the old cadence never fires.
// The o.Ticks injection path is untouched (TestRecorderRunStopsCleanly).
func TestRunFollowsEffectiveInterval(t *testing.T) {
	g := newRig(t, func(c *Config) { c.IntervalSeconds = 1 })
	b := NewBudget(1)
	widenBudget(b, t0, 1) // 1 -> 15
	if b.EffectiveInterval() != 15 {
		t.Fatalf("precondition: effective = %d, want 15", b.EffectiveInterval())
	}
	g.r = NewRecorder(RecorderOptions{Config: g.r.o.Config, Store: g.s, Collector: g.col, Disk: g.disk,
		RunID: "run1", Now: time.Now, Budget: b})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- g.r.Run(ctx) }()
	deadline := time.Now().Add(5 * time.Second)
	for g.s.Manifest().SampleCount() < 1 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if n := g.s.Manifest().SampleCount(); n < 1 {
		t.Fatalf("first tick never came: %d samples", n)
	}
	// The 1 s base cadence must not fire again: the interval is 15 s.
	time.Sleep(2500 * time.Millisecond)
	if n := g.s.Manifest().SampleCount(); n != 1 {
		t.Fatalf("samples %d, want 1: the timer did not follow the effective interval", n)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// Without a budget the cadence is the configured interval.
func TestRunTicksAtBaseInterval(t *testing.T) {
	g := newRig(t, func(c *Config) { c.IntervalSeconds = 1 })
	g.r = NewRecorder(RecorderOptions{Config: g.r.o.Config, Store: g.s, Collector: g.col, Disk: g.disk,
		RunID: "run1", Now: time.Now})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- g.r.Run(ctx) }()
	deadline := time.Now().Add(6 * time.Second)
	for g.s.Manifest().SampleCount() < 3 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if n := g.s.Manifest().SampleCount(); n < 3 {
		t.Fatalf("samples %d, want at least 3 at a 1 s interval", n)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestNextDeadline(t *testing.T) {
	base := time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
	iv := 15 * time.Second
	cases := []struct {
		name string
		prev time.Time
		now  time.Time
		want time.Time
	}{
		{"on time", base, base.Add(14 * time.Second), base.Add(15 * time.Second)},
		{"tick finished late but before the deadline", base, base.Add(14900 * time.Millisecond), base.Add(15 * time.Second)},
		{"at the deadline moves on", base, base.Add(15 * time.Second), base.Add(30 * time.Second)},
		{"one missed deadline is skipped", base, base.Add(20 * time.Second), base.Add(30 * time.Second)},
		{"a long sleep skips all missed deadlines", base, base.Add(10 * time.Minute), base.Add(10*time.Minute + 15*time.Second)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := nextDeadline(tc.prev, tc.now, iv); !got.Equal(tc.want) {
				t.Fatalf("nextDeadline = %v, want %v", got, tc.want)
			}
		})
	}
	// The grid keeps its phase: no drift by the tick duration.
	d := base
	for i := 0; i < 100; i++ {
		d = nextDeadline(d, d.Add(700*time.Millisecond), iv)
	}
	if want := base.Add(100 * iv); !d.Equal(want) {
		t.Fatalf("deadline drifted: %v, want %v", d, want)
	}
	// A zero interval falls back to one second.
	if got := nextDeadline(base, base, 0); !got.Equal(base.Add(time.Second)) {
		t.Fatalf("zero interval: %v", got)
	}
}

// TestWidenedBudgetNoFalseGapOrLate: with the budget widened to 60 s (base
// 15), samples 60 s apart are on time — no fabricated gap and no running-late.
// Lateness and gaps are judged against the effective interval, not the base.
func TestWidenedBudgetNoFalseGapOrLate(t *testing.T) {
	g := newRig(t, nil) // base 15
	b := NewBudget(15)
	widenBudget(b, t0, 2) // 15 -> 30 -> 60
	if b.EffectiveInterval() != 60 {
		t.Fatalf("precondition: effective = %d, want 60", b.EffectiveInterval())
	}
	g.r.o.Budget = b // SelfUsage stays nil: the widened interval holds
	g.r.Tick(g.clk.t)
	g.tick(60 * time.Second)
	g.tick(60 * time.Second)
	m := g.s.Manifest()
	if m.SampleCount() != 3 || len(m.Gaps) != 0 {
		t.Fatalf("samples %d gaps %+v, want 3 samples and no gap at the widened interval", m.SampleCount(), m.Gaps)
	}
	if st := g.r.Status(g.clk.t); st.State != StateRunning {
		t.Fatalf("state %s, want running", st.State)
	}
	// A full widened interval past the newest sample is still on time.
	if st := g.r.Status(g.clk.t.Add(60 * time.Second)); st.State != StateRunning {
		t.Fatalf("state %s at +60 s, want running", st.State)
	}
}

// TestBaseIntervalStillDetectsRealGap: without a budget (base 15), a real 60 s
// hole still reads running-late and is recorded as a gap.
func TestBaseIntervalStillDetectsRealGap(t *testing.T) {
	g := newRig(t, nil) // base 15, no budget
	g.r.Tick(g.clk.t)
	if st := g.r.Status(g.clk.t.Add(60 * time.Second)); st.State != StateRunningLate {
		t.Fatalf("state %s, want running-late at base 15 after 60 s", st.State)
	}
	g.tick(60 * time.Second)
	m := g.s.Manifest()
	if len(m.Gaps) != 1 || m.Gaps[0].Seconds != 60 || m.Gaps[0].Cause != CauseLate {
		t.Fatalf("gaps %+v, want one 60 s late gap", m.Gaps)
	}
}

// Review F6.5: the pass right after a forward clock jump keeps the history.
func TestRecorderForwardClockJumpDoesNotAgeOutHistoryInThatPass(t *testing.T) {
	old := t0.Add(-3 * 24 * time.Hour) // inside keepDays until the clock jumps
	g := newRig(t, func(c *Config) { seedOld(t, c.Folder, old) })
	g.r.Tick(g.clk.t)
	if _, err := os.Stat(filepath.Join(g.s.Dir(), FileNameFor(old))); err != nil {
		t.Fatal("file removed before the jump")
	}
	// The wall clock jumps a month while the steady clock moves 60 s.
	g.clk.t = g.clk.t.Add(60*time.Second + 30*24*time.Hour)
	g.mono += 60 * time.Second
	g.r.Tick(g.clk.t)
	if _, err := os.Stat(filepath.Join(g.s.Dir(), FileNameFor(old))); err != nil {
		t.Fatal("history removed in the pass right after the jump")
	}
	// The next due pass applies the age rule again.
	g.tick(60 * time.Second)
	if _, err := os.Stat(filepath.Join(g.s.Dir(), FileNameFor(old))); err == nil {
		t.Fatal("age rule never came back")
	}
}

// --- BBX-5-03: events carry fixed codes, never error text ---

// secretEventText stands for driver text with a path and an instance name. No
// part of it may reach the manifest: the manifest ships in snapshots.
const secretEventText = `secret driver text C:\instances\MSSQL$PROD1`

// codeFailWriter fails writes with the injected error while fail is set. The
// embedded file keeps Seek/Truncate/Sync/Close working, so rollback stays clean.
type codeFailWriter struct {
	*os.File
	fail *bool
	err  error
}

func (w codeFailWriter) Write(b []byte) (int, error) {
	if *w.fail {
		return 0, w.err
	}
	return w.File.Write(b)
}

// fixedEvents counts the events of one kind and fails unless every detail is
// exactly the fixed code want.
func fixedEvents(t *testing.T, m Manifest, kind, want string) int {
	t.Helper()
	n := 0
	for _, e := range m.Events {
		if e.Kind != kind {
			continue
		}
		n++
		if e.Detail != want {
			t.Fatalf("%s event detail = %q, want fixed code %q", kind, e.Detail, want)
		}
	}
	return n
}

func TestCollectFailureFixedCode(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"generic", errors.New(secretEventText), "collect-failed"},
		{"permission", &os.PathError{Op: "read", Path: "secret", Err: os.ErrPermission}, "permission-denied"},
		{"disk full", errors.New("There is not enough space on the disk."), "disk-full"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := newRig(t, nil)
			g.col.err = tc.err
			g.r.Tick(g.clk.t)
			g.tick(15 * time.Second)
			g.tick(15 * time.Second)
			// First failure only: one event, a fixed code, never the error text.
			if n := fixedEvents(t, g.s.Manifest(), EventCollectFailed, tc.want); n != 1 {
				t.Fatalf("collect-failed events = %d, want 1 (first-failure dedup)", n)
			}
			g.col.err = nil
			g.tick(15 * time.Second)
			if g.s.Manifest().SampleCount() != 1 {
				t.Fatal("did not recover")
			}
		})
	}
}

func TestWriteFailureFixedCode(t *testing.T) {
	g := newRig(t, nil)
	fail := true
	g.s.wrapFile = func(f *os.File) recFile {
		return codeFailWriter{File: f, fail: &fail, err: errors.New(secretEventText)}
	}
	g.r.Tick(g.clk.t)        // first write fails
	g.tick(15 * time.Second) // still failing: no second event
	if n := fixedEvents(t, g.s.Manifest(), EventWriteFailed, "write-failed"); n != 1 {
		t.Fatalf("write-failed events = %d, want 1 (first-failure dedup)", n)
	}
	if g.r.Status(g.clk.t).WriteOK {
		t.Fatal("write failure not reported")
	}
	fail = false // space comes back
	g.tick(15 * time.Second)
	if !g.r.Status(g.clk.t).WriteOK || g.s.Manifest().SampleCount() != 1 {
		t.Fatalf("no recovery: %d samples", g.s.Manifest().SampleCount())
	}
}

func TestHousekeepingFailureFixedCode(t *testing.T) {
	g := newRig(t, nil)
	orig := housekeepFn
	housekeepFn = func(s *Store, l Limits, d DiskSpace, now time.Time) (Housekeeping, error) {
		return Housekeeping{}, errors.New(secretEventText)
	}
	t.Cleanup(func() { housekeepFn = orig })
	g.r.Tick(g.clk.t)
	if n := fixedEvents(t, g.s.Manifest(), EventHousekeeping, "housekeeping-failed"); n != 1 {
		t.Fatalf("housekeeping events = %d, want 1", n)
	}
	// A failed pass must not stop the sampling.
	if g.s.Manifest().SampleCount() != 1 {
		t.Fatal("no sample after a failed housekeeping pass")
	}
}

func TestEventCodeNeverRaw(t *testing.T) {
	fixed := map[string]bool{
		"collect-failed": true, "write-failed": true, "housekeeping-failed": true,
		"disk-full": true, "permission-denied": true,
	}
	cases := []struct {
		err  error
		want string
	}{
		{errors.New("secret path X"), "collect-failed"},
		{fmt.Errorf("wrapped: %w", errors.New(secretEventText)), "collect-failed"},
		{os.ErrPermission, "permission-denied"},
		{syscall.ENOSPC, "disk-full"},
		{errors.New("There is not enough space on the disk."), "disk-full"},
	}
	for _, tc := range cases {
		got := eventCode(tc.err)
		if !fixed[got] {
			t.Fatalf("eventCode(%v) = %q, not a fixed code", tc.err, got)
		}
		if got != tc.want {
			t.Fatalf("eventCode(%v) = %q, want %q", tc.err, got, tc.want)
		}
		if got == tc.err.Error() {
			t.Fatalf("eventCode returned the raw error text %q", got)
		}
	}
}

func TestNoErrorTextInManifest(t *testing.T) {
	g := newRig(t, nil)
	// A collect failure.
	g.col.err = errors.New(secretEventText)
	g.r.Tick(g.clk.t)
	g.col.err = nil
	// A write failure through the wrapFile seam.
	fail := true
	g.s.wrapFile = func(f *os.File) recFile {
		return codeFailWriter{File: f, fail: &fail, err: errors.New(secretEventText)}
	}
	g.tick(15 * time.Second)
	fail = false
	// A housekeeping failure.
	orig := housekeepFn
	housekeepFn = func(s *Store, l Limits, d DiskSpace, now time.Time) (Housekeeping, error) {
		return Housekeeping{}, errors.New(secretEventText)
	}
	t.Cleanup(func() { housekeepFn = orig })
	g.tick(60 * time.Second)

	// The serialized manifest is what ships in snapshots: no substring of the
	// injected driver text, path or instance name may be in it.
	data, err := os.ReadFile(filepath.Join(g.s.Dir(), ManifestName))
	if err != nil {
		t.Fatal(err)
	}
	for _, frag := range []string{secretEventText, "secret driver text", `C:\instances`, "MSSQL$PROD1"} {
		if strings.Contains(string(data), frag) {
			t.Fatalf("manifest leaks error text %q:\n%s", frag, data)
		}
	}
	m := g.s.Manifest()
	fixedEvents(t, m, EventCollectFailed, "collect-failed")
	fixedEvents(t, m, EventWriteFailed, "write-failed")
	fixedEvents(t, m, EventHousekeeping, "housekeeping-failed")
}

// eventsOfKinds lists the events whose kind is one of kinds, oldest first.
func eventsOfKinds(m Manifest, kinds ...string) []Event {
	var out []Event
	for _, e := range m.Events {
		for _, k := range kinds {
			if e.Kind == k {
				out = append(out, e)
			}
		}
	}
	return out
}

// swapFailWriter fails writes with *err while it is set, so a test can change
// the cause in the middle of an outage.
type swapFailWriter struct {
	*os.File
	err *error
}

func (w swapFailWriter) Write(b []byte) (int, error) {
	if *w.err != nil {
		return 0, *w.err
	}
	return w.File.Write(b)
}

// outageRig is one failure kind seen from the outside: fail(err) makes the next
// rounds fail with err, heal() lets them work again.
type outageRig struct {
	first, again, recovered string
	fail                    func(err error)
	heal                    func()
}

func collectOutage(g *rig) outageRig {
	return outageRig{
		first: EventCollectFailed, again: EventCollectStillFailing, recovered: EventCollectRecovered,
		fail: func(err error) { g.col.mu.Lock(); g.col.err = err; g.col.mu.Unlock() },
		heal: func() { g.col.mu.Lock(); g.col.err = nil; g.col.mu.Unlock() },
	}
}

func writeOutage(g *rig) outageRig {
	var cause error
	g.s.wrapFile = func(f *os.File) recFile { return swapFailWriter{File: f, err: &cause} }
	return outageRig{
		first: EventWriteFailed, again: EventWriteStillFailing, recovered: EventWriteRecovered,
		fail: func(err error) { cause = err },
		heal: func() { cause = nil },
	}
}

var outageKinds = []struct {
	name string
	make func(g *rig) outageRig
}{{"collect", collectOutage}, {"write", writeOutage}}

// TestFailureRepeatsEvery15MinutesAndRecoveredEventCloses: a failure that lasts
// 55 minutes (rounds every 5 minutes) raises the first event, one still-failing
// event per 15 minutes with the newest code and the rounds since the last event,
// and one recovered event with the total rounds and how long it lasted (W-03).
func TestFailureRepeatsEvery15MinutesAndRecoveredEventCloses(t *testing.T) {
	for _, k := range outageKinds {
		t.Run(k.name, func(t *testing.T) {
			g := newRig(t, nil)
			t0 := g.clk.t
			o := k.make(g)
			errA := errors.New(secretEventText)
			errB := errors.New("There is not enough space on the disk.")
			o.fail(errA)
			g.r.Tick(g.clk.t) // t+0: first failure
			for i := 1; i <= 10; i++ {
				if i == 6 { // t+30: the cause changes
					o.fail(errB)
				}
				g.tick(5 * time.Minute)
			}
			o.heal()
			g.tick(5 * time.Minute) // t+55: works again

			codeA := k.name + "-failed"
			got := eventsOfKinds(g.s.Manifest(), o.first, o.again, o.recovered)
			want := []struct {
				at     time.Duration
				kind   string
				detail string
			}{
				{0, o.first, codeA},
				{15 * time.Minute, o.again, "code=" + codeA + " rounds=3"},
				{30 * time.Minute, o.again, "code=disk-full rounds=3"},
				{45 * time.Minute, o.again, "code=disk-full rounds=3"},
				{55 * time.Minute, o.recovered, "rounds=11 seconds=3300"},
			}
			if len(got) != len(want) {
				t.Fatalf("%d events, want %d: %+v", len(got), len(want), got)
			}
			for i, w := range want {
				if got[i].Kind != w.kind || got[i].Detail != w.detail || !got[i].T.Equal(t0.Add(w.at)) {
					t.Fatalf("event %d = %s %q at +%s, want %s %q at +%s", i, got[i].Kind, got[i].Detail, got[i].T.Sub(t0), w.kind, w.detail, w.at)
				}
			}
			data, err := os.ReadFile(filepath.Join(g.s.Dir(), ManifestName))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(data), "secret driver text") {
				t.Fatal("manifest leaks the error text")
			}
		})
	}
}

// TestFailureFlappingStaysUnderOneEventPer15Minutes: a failure that comes and
// goes every minute does not write a failed and a recovered event each time.
func TestFailureFlappingStaysUnderOneEventPer15Minutes(t *testing.T) {
	for _, k := range outageKinds {
		t.Run(k.name, func(t *testing.T) {
			g := newRig(t, nil)
			t0 := g.clk.t
			o := k.make(g)
			boom := errors.New(secretEventText)
			count := func() int {
				return len(eventsOfKinds(g.s.Manifest(), o.first, o.again, o.recovered))
			}
			o.fail(boom)
			g.r.Tick(g.clk.t) // +0: first event
			o.heal()
			g.tick(time.Minute) // +1: recovered event
			o.fail(boom)
			g.tick(time.Minute) // +2: inside 15 minutes of the last event: nothing
			o.heal()
			g.tick(time.Minute) // +3: that outage raised nothing, so it closes silently
			if n := count(); n != 2 {
				t.Fatalf("%d events after a quick flap, want 2 (first, recovered)", n)
			}
			o.fail(boom)
			g.tick(17 * time.Minute) // +20: more than 15 minutes since +1
			ev := eventsOfKinds(g.s.Manifest(), o.first, o.again, o.recovered)
			if len(ev) != 3 || ev[2].Kind != o.first || !ev[2].T.Equal(t0.Add(20*time.Minute)) {
				t.Fatalf("after the quiet 15 minutes: %+v", ev)
			}
		})
	}
}

func TestRecorderLogsCatalogueIncludesExtraRoots(t *testing.T) {
	conf, extra := t.TempDir(), t.TempDir()
	writeLog(t, conf, "a.log")
	writeLog(t, extra, "iis.log")
	g := newRig(t, func(c *Config) { c.Record.LogsCatalogue = true; c.LogPaths = []string{conf} })
	g.r.o.ExtraLogRoots = func() []string { return []string{extra, conf} }
	g.r.Tick(g.clk.t)
	smp := readSamples(t, g.s.Dir())
	if len(smp) != 1 || len(smp[0].Logs) != 2 {
		t.Fatalf("want both folders, got %+v", smp)
	}
}
