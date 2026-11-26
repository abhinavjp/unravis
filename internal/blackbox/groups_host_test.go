package blackbox

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// withHostBudget shortens the host budget for one test and restores it.
func withHostBudget(t *testing.T, d time.Duration) {
	t.Helper()
	old := hostBudget
	hostBudget = d
	t.Cleanup(func() { hostBudget = old })
}

// A slow host read must cost only the host fields. The group is still read, the
// CPU stays nil (never 0) and Missing says why (RV-BB-1).
func TestSlowHostReadKeepsTheGroupsAndLeavesHostFieldsAbsent(t *testing.T) {
	withHostBudget(t, 20*time.Millisecond)
	release := make(chan struct{})
	defer close(release)
	tcp := &deadlineGroup{name: "tcp", result: &TCPGroup{RetransmitsPerSec: fp64(3)}}
	c := NewGroupCollector(&blockingHost{release: release}, []Group{tcp}, NewBudget(15))

	s, err := c.Collect(time.Unix(1000, 0))
	if err != nil {
		t.Fatalf("a slow host read with a good group must not drop the sample: %v", err)
	}
	if s.TCP == nil || s.TCP.RetransmitsPerSec == nil || *s.TCP.RetransmitsPerSec != 3 {
		t.Fatalf("the group was not kept: %+v", s.TCP)
	}
	if s.CPU != nil || s.MemAvail != nil {
		t.Fatalf("host fields must stay absent, never 0: cpu %v mem %v", s.CPU, s.MemAvail)
	}
	if !s.HostSkipped() || !missingHas(s, MissingHostSlow) || hostBusyWord(s) {
		t.Fatalf("Missing = %v, want %q", s.Missing, MissingHostSlow)
	}

	// The next round finds the host read still running: busy, same outcome.
	s, err = c.Collect(time.Unix(1015, 0))
	if err != nil {
		t.Fatalf("busy round: %v", err)
	}
	if !s.HostSkipped() || !hostBusyWord(s) || !missingHas(s, MissingHostBusy) || s.CPU != nil {
		t.Fatalf("busy round Missing = %v cpu %v, want %q and no CPU", s.Missing, s.CPU, MissingHostBusy)
	}
}

// With no group giving a reading either, the round is still dropped, as before.
func TestSlowHostReadWithNoGroupReadingStillDropsTheRound(t *testing.T) {
	withHostBudget(t, 20*time.Millisecond)
	release := make(chan struct{})
	defer close(release)
	empty := &deadlineGroup{name: "tcp"} // returns a nil payload
	c := NewGroupCollector(&blockingHost{release: release}, []Group{empty}, NewBudget(15))

	if _, err := c.Collect(time.Unix(1000, 0)); !errors.Is(err, errHostSlow) {
		t.Fatalf("err = %v, want errHostSlow", err)
	}
	if _, err := c.Collect(time.Unix(1015, 0)); !errors.Is(err, errHostBusy) {
		t.Fatalf("second err = %v, want errHostBusy", err)
	}
}

// A real host error is returned as it always was; it is not a slow read.
func TestHostErrorStillFailsTheRound(t *testing.T) {
	boom := errors.New("boom")
	tcp := &deadlineGroup{name: "tcp", result: &TCPGroup{}}
	c := NewGroupCollector(&stubCollector{err: boom}, []Group{tcp}, NewBudget(15))
	if _, err := c.Collect(time.Unix(1000, 0)); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the host error", err)
	}
}

// Three slow or busy rounds in a row double the host budget; a quick read in
// between starts the count again.
func TestHostBudgetStepsUpAfterThreeSlowRounds(t *testing.T) {
	withHostBudget(t, 20*time.Millisecond)
	release := make(chan struct{})
	defer close(release)
	tcp := &deadlineGroup{name: "tcp", result: &TCPGroup{}}
	c := NewGroupCollector(&blockingHost{release: release}, []Group{tcp}, NewBudget(60)).(*groupCollector)

	if got := c.hostLimit(); got != hostBudget {
		t.Fatalf("starting limit %v, want %v", got, hostBudget)
	}
	now := time.Unix(1000, 0)
	for i := 1; i <= hostStepUpAfter; i++ {
		if c.hostStep != 0 {
			t.Fatalf("stepped up after only %d slow rounds", i-1)
		}
		if _, err := c.Collect(now); err != nil {
			t.Fatalf("round %d: %v", i, err)
		}
		now = now.Add(60 * time.Second)
	}
	if c.hostStep != 1 || c.hostLimit() != 2*hostBudget {
		t.Fatalf("after %d slow rounds step %d limit %v, want step 1 and %v", hostStepUpAfter, c.hostStep, c.hostLimit(), 2*hostBudget)
	}
	// Three more (all busy now) go to the top step and no further.
	for i := 0; i < 3*hostStepUpAfter; i++ {
		if _, err := c.Collect(now); err != nil {
			t.Fatalf("round: %v", err)
		}
		now = now.Add(60 * time.Second)
	}
	if c.hostStep != hostMaxStep || c.hostLimit() != 4*hostBudget {
		t.Fatalf("step %d limit %v, want the top step %d and %v", c.hostStep, c.hostLimit(), hostMaxStep, 4*hostBudget)
	}
}

func TestHostBudgetQuickReadInBetweenResetsTheSlowCount(t *testing.T) {
	c := &groupCollector{}
	c.noteHost(true, false)
	c.noteHost(true, false)
	c.noteHost(false, true) // quick
	c.noteHost(true, false)
	c.noteHost(true, false)
	if c.hostStep != 0 {
		t.Fatalf("step %d, want 0: the slow rounds were not in a row", c.hostStep)
	}
	c.noteHost(true, false)
	if c.hostStep != 1 {
		t.Fatalf("step %d, want 1 after three in a row", c.hostStep)
	}
}

// Twenty quick rounds in a row step the budget back down, one step at a time.
func TestHostBudgetStepsDownAfterTwentyQuickRounds(t *testing.T) {
	withHostBudget(t, time.Second)
	tcp := &deadlineGroup{name: "tcp", result: &TCPGroup{}}
	c := NewGroupCollector(&stubCollector{sample: Sample{CPU: fp64(5)}}, []Group{tcp}, NewBudget(60)).(*groupCollector)
	c.hostStep = 2

	now := time.Unix(1000, 0)
	for i := 1; i < hostStepDownAfter; i++ {
		s, err := c.Collect(now)
		if err != nil || s.HostSkipped() || s.CPU == nil {
			t.Fatalf("round %d: err %v missing %v", i, err, s.Missing)
		}
		now = now.Add(60 * time.Second)
	}
	if c.hostStep != 2 {
		t.Fatalf("step %d after %d quick rounds, want it unchanged at 2", c.hostStep, hostStepDownAfter-1)
	}
	if _, err := c.Collect(now); err != nil {
		t.Fatal(err)
	}
	if c.hostStep != 1 {
		t.Fatalf("step %d after %d quick rounds, want 1", c.hostStep, hostStepDownAfter)
	}
}

// The budget never passes half the sampling interval, and a base that is already
// above that half is kept as it is.
func TestHostBudgetIsCappedAtHalfTheInterval(t *testing.T) {
	withHostBudget(t, 2*time.Second)
	c := &groupCollector{budget: NewBudget(15), hostStep: 2}
	if got := c.hostLimit(); got != 7500*time.Millisecond {
		t.Fatalf("limit %v, want half of the 15 s interval (7.5s), not 4 x the base", got)
	}
	c = &groupCollector{budget: NewBudget(60), hostStep: 2}
	if got := c.hostLimit(); got != 8*time.Second {
		t.Fatalf("limit %v, want 8s at the top step on a 60 s interval", got)
	}
	withHostBudget(t, 40*time.Second)
	c = &groupCollector{budget: NewBudget(15), hostStep: 1}
	if got := c.hostLimit(); got != hostBudget {
		t.Fatalf("limit %v, want the base %v kept when it is above half the interval", got, hostBudget)
	}
}

// gateHost is a host collector that can be made to hang inside its read, the way
// a stalled process enumeration does. While slow is false it answers at once.
type gateHost struct {
	slow    atomic.Bool
	release chan struct{}
}

func (h *gateHost) Collect(time.Time) (Sample, error) {
	if h.slow.Load() {
		<-h.release
		return Sample{}, nil
	}
	v := 10.0
	return Sample{CPU: &v}, nil
}
func (h *gateHost) Close() error { return nil }

// TestSlowHostReadEndToEndThroughTheRecorder drives a real recorder, store and
// group collector through a 55 minute slow host read and back. The samples on
// disk keep the group but never a CPU of 0, the events open, repeat and close,
// and the state shows the plain warning only while it lasts (RV-BB-1, W-03).
func TestSlowHostReadEndToEndThroughTheRecorder(t *testing.T) {
	withHostBudget(t, 20*time.Millisecond)
	host := &gateHost{release: make(chan struct{})}
	host.slow.Store(true)
	tcp := &deadlineGroup{name: "tcp", result: &TCPGroup{RetransmitsPerSec: fp64(4)}}
	gc := NewGroupCollector(host, []Group{tcp}, NewBudget(15)).(*groupCollector)

	cfg := Default()
	cfg.Name = "box"
	cfg.Folder = t.TempDir()
	cfg.Record.LogsCatalogue = false
	clk := &clock{t: t0}
	st := openT(t, cfg.Folder, "run1", clk)
	var mono time.Duration
	r := NewRecorder(RecorderOptions{Config: cfg, Store: st, Collector: gc, Disk: &pctDisk{pct: 80}, RunID: "run1",
		Now: clk.now, Mono: func() time.Duration { return mono }})
	tick := func(d time.Duration) {
		clk.t = clk.t.Add(d)
		mono += d
		r.Tick(clk.t)
	}

	r.Tick(clk.t) // +0: the host read times out
	for i := 1; i <= 10; i++ {
		tick(5 * time.Minute) // +5 .. +50: the earlier read is still running (busy)
	}
	readBack := func() []Sample {
		t.Helper()
		got, _, err := ReadSamples(context.Background(), folderSource(t, st.Dir()), st.Manifest(), t0, t0.Add(8*time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	deriveNow := func() StateInfo {
		return DeriveState(StateInput{Placed: true, Reading: stReading(st.Manifest(), clk.t, nil), Recent: readBack()})
	}
	mid := deriveNow()
	if !strings.Contains(mid.Detail, HostSlowSentence) || mid.Tone != ToneWarn {
		t.Fatalf("state during the slow read: %+v", mid)
	}

	// The stuck read ends. Wait for the recorder's own slot to notice, then read.
	close(host.release)
	host.slow.Store(false)
	deadline := time.Now().Add(10 * time.Second)
	for !gc.innerW.idle() {
		if time.Now().After(deadline) {
			t.Fatal("the stuck host read never finished")
		}
		runtime.Gosched()
	}
	tick(5 * time.Minute) // +55: a full sample again

	m := st.Manifest()
	if m.SampleCount() != 12 {
		t.Fatalf("samples %d, want 12: a slow host read must not cost the sample", m.SampleCount())
	}
	got, _, err := ReadSamples(context.Background(), folderSource(t, st.Dir()), m, t0, t0.Add(2*time.Hour))
	if err != nil || len(got) != 12 {
		t.Fatalf("read back %d samples: %v", len(got), err)
	}
	for i, s := range got {
		if s.TCP == nil || s.TCP.RetransmitsPerSec == nil || *s.TCP.RetransmitsPerSec != 4 {
			t.Fatalf("sample %d lost the group: %+v", i, s.TCP)
		}
		if i < 11 {
			word := MissingHostBusy
			if i == 0 {
				word = MissingHostSlow
			}
			if s.CPU != nil || !s.HostSkipped() || !missingHas(s, word) {
				t.Fatalf("sample %d: cpu %v missing %v, want no CPU and %q", i, s.CPU, s.Missing, word)
			}
			continue
		}
		if s.CPU == nil || *s.CPU != 10 || s.HostSkipped() {
			t.Fatalf("sample %d after the recovery: cpu %v missing %v", i, s.CPU, s.Missing)
		}
	}

	ev := eventsOfKinds(m, EventHostSlow, EventHostStillSlow, EventHostRecovered)
	want := []struct {
		at           time.Duration
		kind, detail string
	}{
		{0, EventHostSlow, codeHostSlow},
		{15 * time.Minute, EventHostStillSlow, "code=host-busy rounds=3"},
		{30 * time.Minute, EventHostStillSlow, "code=host-busy rounds=3"},
		{45 * time.Minute, EventHostStillSlow, "code=host-busy rounds=3"},
		{55 * time.Minute, EventHostRecovered, "rounds=11 seconds=3300"},
	}
	if len(ev) != len(want) {
		t.Fatalf("%d events, want %d: %+v", len(ev), len(want), ev)
	}
	for i, w := range want {
		if ev[i].Kind != w.kind || ev[i].Detail != w.detail || !ev[i].T.Equal(t0.Add(w.at)) {
			t.Fatalf("event %d = %s %q at +%s, want %s %q at +%s", i, ev[i].Kind, ev[i].Detail, ev[i].T.Sub(t0), w.kind, w.detail, w.at)
		}
	}
	// 11 of the 12 samples are still skipped, so the warning stays until the host read has been back for a while.
	if still := deriveNow(); !strings.Contains(still.Detail, HostSlowSentence) {
		t.Fatalf("one good round must not clear the warning at once: %+v", still)
	}
	for i := 0; i < 10; i++ {
		tick(5 * time.Minute) // full samples again; the newest 20 now hold 9 skipped
	}
	if after := deriveNow(); strings.Contains(after.Detail, HostSlowSentence) || after.Tone != ToneGood {
		t.Fatalf("the warning must go once the host read has been back: %+v", after)
	}
}
