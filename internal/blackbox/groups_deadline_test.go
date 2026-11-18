package blackbox

import (
	"errors"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

// deadlineGroup blocks until released, so a test can prove the tick does not wait
// for a stalled provider. Its name is distinct from every other test double.
type deadlineGroup struct {
	name     string
	optional bool
	release  chan struct{}
	entered  chan struct{}
	calls    atomic.Int64
	closed   bool
	result   any
}

func (g *deadlineGroup) Name() string   { return g.name }
func (g *deadlineGroup) Optional() bool { return g.optional }
func (g *deadlineGroup) Collect(time.Time, float64) (any, error) {
	g.calls.Add(1)
	if g.entered != nil {
		select {
		case g.entered <- struct{}{}:
		default:
		}
	}
	if g.release != nil {
		<-g.release
	}
	return g.result, nil
}
func (g *deadlineGroup) Close() error { g.closed = true; return nil }

// blockingHost stands in for a host collector stuck inside a native enumeration.
type blockingHost struct {
	release chan struct{}
	closed  bool
}

func (b *blockingHost) Collect(time.Time) (Sample, error) {
	<-b.release
	return Sample{}, nil
}
func (b *blockingHost) Close() error { b.closed = true; return nil }

// W2-01: a stalled group must cost one group budget, not the whole recorder, and
// the other groups must still be collected in the same tick.
func TestGroupDeadlineBoundsTheTick(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	stalled := &deadlineGroup{name: "stalled", release: release}
	fast := &deadlineGroup{name: "tcp", result: &TCPGroup{RetransmitsPerSec: fp64(2)}}
	c := NewGroupCollector(&stubCollector{}, []Group{stalled, fast}, NewBudget(15))

	start := time.Now()
	s, err := c.Collect(time.Unix(1000, 0))
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if elapsed > 3*budgetMaxGroup {
		t.Fatalf("tick took %v with a stalled group, want about one group budget (%v)", elapsed, budgetMaxGroup)
	}
	if !missingHas(s, "stalled: slow") {
		t.Errorf("Missing = %v, want stalled: slow", s.Missing)
	}
	if s.TCP == nil || s.TCP.RetransmitsPerSec == nil || *s.TCP.RetransmitsPerSec != 2 {
		t.Errorf("fast sibling was not collected while another group stalled: %+v", s.TCP)
	}
}

// W2-01: a permanently stuck provider must not spawn a worker per tick, and must
// not stop the other groups or the recorder.
func TestBusyGroupDoesNotAccumulateWorkers(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	stalled := &deadlineGroup{name: "stalled", release: release}
	fast := &deadlineGroup{name: "tcp", result: &TCPGroup{}}
	c := NewGroupCollector(&stubCollector{}, []Group{stalled, fast}, NewBudget(15))

	now := time.Unix(1000, 0)
	if _, err := c.Collect(now); err != nil {
		t.Fatalf("first Collect: %v", err)
	}
	base := runtime.NumGoroutine()
	for i := 1; i <= 100; i++ {
		now = now.Add(15 * time.Second)
		s, err := c.Collect(now)
		if err != nil {
			t.Fatalf("tick %d: %v", i, err)
		}
		// After the timeout the still-outstanding worker is reported as busy.
		if !missingHas(s, "stalled: busy") {
			t.Fatalf("tick %d: Missing = %v, want stalled: busy", i, s.Missing)
		}
		if s.TCP == nil {
			t.Fatalf("tick %d: the healthy group stopped being collected", i)
		}
	}
	if got := runtime.NumGoroutine(); got > base+2 {
		t.Errorf("goroutines grew from %d to %d over 100 stalled ticks, want no growth", base, got)
	}
	if got := stalled.calls.Load(); got != 1 {
		t.Errorf("stalled group entered %d times, want exactly 1 (one outstanding worker)", got)
	}
}

// W2-01: the host read used to sit outside every per-group timer.
func TestHostReadIsBounded(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	c := NewGroupCollector(&blockingHost{release: release}, nil, NewBudget(15))

	start := time.Now()
	_, err := c.Collect(time.Unix(1000, 0))
	if !errors.Is(err, errHostSlow) {
		t.Fatalf("err = %v, want errHostSlow", err)
	}
	if elapsed := time.Since(start); elapsed > 3*budgetMaxGroup {
		t.Fatalf("host read held the tick for %v, want about one group budget", elapsed)
	}
	// The next tick must not queue another host read behind the stuck one.
	if _, err := c.Collect(time.Unix(1015, 0)); !errors.Is(err, errHostBusy) {
		t.Fatalf("second err = %v, want errHostBusy", err)
	}
}

// W2-01: Close must not close a source a worker is still inside, and must not
// wait forever for it.
func TestCloseSkipsBusySource(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	stalled := &deadlineGroup{name: "stalled", release: release}
	fast := &deadlineGroup{name: "fast"}
	inner := &stubCollector{}
	c := NewGroupCollector(inner, []Group{stalled, fast}, NewBudget(15))
	if _, err := c.Collect(time.Unix(1000, 0)); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	start := time.Now()
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if elapsed := time.Since(start); elapsed > groupCloseGrace+2*time.Second {
		t.Fatalf("Close took %v, want it bounded by the grace period", elapsed)
	}
	if stalled.closed {
		t.Error("Close closed a source whose worker is still inside it (use-after-free)")
	}
	if !fast.closed || !inner.closed {
		t.Error("Close skipped an idle source")
	}
}

// W2-05: a factory that failed at startup is retried on a bounded backoff and
// recovers without restarting the recorder.
func TestUnavailableGroupRetriesAndRecovers(t *testing.T) {
	attempts := 0
	real := &deadlineGroup{name: "late", result: &TCPGroup{RetransmitsPerSec: fp64(1)}}
	g := &lazyGroup{
		name: "late",
		factory: func(GroupDeps) (Group, error) {
			attempts++
			if attempts < 3 {
				return nil, errors.New("provider not ready")
			}
			return real, nil
		},
	}
	t0 := time.Unix(1000, 0)

	if _, err := g.Collect(t0, 15); !errors.Is(err, errGroupUnavailable) {
		t.Fatalf("first attempt err = %v, want errGroupUnavailable", err)
	}
	// Inside the backoff window: no new attempt, still a fixed reason.
	if _, err := g.Collect(t0.Add(time.Second), 15); !errors.Is(err, errGroupUnavailable) {
		t.Fatalf("early retry err = %v, want errGroupUnavailable", err)
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d after an early retry, want 1 (backoff respected)", attempts)
	}
	// After the window the factory is tried again. It still fails once more, then
	// succeeds; once built, the real group serves every later tick.
	if _, err := g.Collect(t0.Add(time.Hour), 15); !errors.Is(err, errGroupUnavailable) {
		t.Fatalf("second attempt err = %v, want errGroupUnavailable", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
	if _, err := g.Collect(t0.Add(2*time.Hour), 15); err != nil {
		t.Fatalf("third attempt should have built the group: %v", err)
	}
	if attempts != 3 || g.real == nil {
		t.Fatalf("attempts = %d, real = %v; want the group built on the third attempt", attempts, g.real)
	}
	v, err := g.Collect(t0.Add(3*time.Hour), 15)
	if err != nil || v == nil {
		t.Fatalf("built group did not serve: (%v, %v)", v, err)
	}
	if attempts != 3 {
		t.Errorf("factory called again after success: attempts = %d", attempts)
	}
	if err := g.Close(); err != nil || !real.closed {
		t.Errorf("lazyGroup.Close did not release the built group: %v", err)
	}
}

func TestGroupRetryDelayIsBounded(t *testing.T) {
	if d := groupRetryDelay(1); d != 15*time.Second {
		t.Errorf("first delay = %v, want 15s", d)
	}
	if d := groupRetryDelay(30); d != groupRetryMax {
		t.Errorf("late delay = %v, want the %v cap", d, groupRetryMax)
	}
}

// W2-04: with nothing selected the per-process factories must not create a
// machine-wide detail query at all.
func TestNoTargetsCreatesNoDetailSource(t *testing.T) {
	for _, tc := range []struct {
		name    string
		factory GroupFactory
	}{
		{"clr", newCLRGroup},
		{"proc10", newProc10Group},
		{"procIO", newProcIOGroup},
		{"iis", newIISGroup},
	} {
		g, err := tc.factory(GroupDeps{Cores: 4})
		if !errors.Is(err, errNoTargets) {
			t.Errorf("%s: err = %v, want errNoTargets when nothing is selected", tc.name, err)
		}
		if g != nil {
			t.Errorf("%s: built a group with no selected targets", tc.name)
		}
	}
}

// W2-05: a successful collect with no payload is still "no reading" and must be
// named, including a typed nil that != nil would miss.
func TestNilPayloadIsNamedMissing(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result any
	}{
		{"untyped nil", nil},
		{"typed nil", (*TCPGroup)(nil)},
	} {
		g := &deadlineGroup{name: "tcp", result: tc.result}
		c := NewGroupCollector(&stubCollector{}, []Group{g}, NewBudget(15))
		s, err := c.Collect(time.Unix(1000, 0))
		if err != nil {
			t.Fatalf("%s: Collect: %v", tc.name, err)
		}
		if !missingHas(s, "tcp") {
			t.Errorf("%s: Missing = %v, want tcp named", tc.name, s.Missing)
		}
		if s.TCP != nil {
			t.Errorf("%s: a nil payload was applied as a group", tc.name)
		}
	}
}
