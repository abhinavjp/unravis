package blackbox

import (
	"errors"
	"testing"
	"time"
)

// swapRegistry gives a test its own empty registry and restores the real one.
func swapRegistry(t *testing.T) {
	t.Helper()
	registryMu.Lock()
	saved := registry
	registry = map[string]registration{}
	registryMu.Unlock()
	t.Cleanup(func() {
		registryMu.Lock()
		registry = saved
		registryMu.Unlock()
	})
}

// naFactory is a scripted factory: it counts calls and answers from answer(n),
// where n is the 1-based call number.
type naFactory struct {
	calls  int
	answer func(n int) (Group, error)
}

func (f *naFactory) build(GroupDeps) (Group, error) {
	f.calls++
	return f.answer(f.calls)
}

func balloonPayload() *VMGroup {
	b := uint64(512 << 20)
	return &VMGroup{BalloonBytes: &b}
}

// newNACollector builds a collector over the real BuildGroups for one group named
// "vm" whose factory is f.
func newNACollector(t *testing.T, f *naFactory) (*groupCollector, *Budget) {
	t.Helper()
	swapRegistry(t)
	RegisterGroup("vm", true, f.build)
	groups := BuildGroups(GroupDeps{})
	if len(groups) != 1 {
		t.Fatalf("BuildGroups returned %d groups, want 1 (a not-applicable group keeps its slot)", len(groups))
	}
	b := NewBudget(15)
	return NewGroupCollector(&stubCollector{}, groups, b).(*groupCollector), b
}

func notApplicable(int) (Group, error) { return nil, errNotApplicable }

func TestNotApplicableIsSilent(t *testing.T) {
	f := &naFactory{answer: notApplicable}
	c, b := newNACollector(t, f)
	t0 := time.Unix(1_000_000, 0)
	for i := 0; i < 3; i++ {
		s, err := c.Collect(t0.Add(time.Duration(i) * 15 * time.Second))
		if err != nil {
			t.Fatalf("tick %d: %v", i, err)
		}
		if missingHas(s, "vm") || len(s.Missing) != 0 {
			t.Fatalf("tick %d: Missing = %v, want empty for a not-applicable group", i, s.Missing)
		}
		if s.VM != nil {
			t.Fatalf("tick %d: VM = %+v, want nil", i, s.VM)
		}
		if gt := c.GroupTimings(); len(gt) != 0 {
			t.Fatalf("tick %d: GroupTimings = %v, want none", i, gt)
		}
		if sg := b.SlowGroups(); len(sg) != 0 {
			t.Fatalf("tick %d: SlowGroups = %v, want none", i, sg)
		}
	}
	if f.calls != 1 {
		t.Fatalf("factory calls = %d, want 1 (the build) inside the recheck interval", f.calls)
	}
}

func TestNotApplicableRechecksAfterInterval(t *testing.T) {
	f := &naFactory{answer: func(n int) (Group, error) {
		if n < 3 {
			return nil, errNotApplicable
		}
		return &fakeGroup{name: "vm", optional: true, result: balloonPayload()}, nil
	}}
	c, _ := newNACollector(t, f)
	t0 := time.Unix(1_000_000, 0)

	for _, at := range []time.Duration{0, 29 * time.Minute} {
		s, _ := c.Collect(t0.Add(at))
		if len(s.Missing) != 0 {
			t.Fatalf("at +%v: Missing = %v, want empty", at, s.Missing)
		}
	}
	if f.calls != 1 {
		t.Fatalf("factory calls after +29m = %d, want 1 (the build only)", f.calls)
	}
	s, _ := c.Collect(t0.Add(31 * time.Minute))
	if f.calls != 2 {
		t.Fatalf("factory calls after +31m = %d, want 2", f.calls)
	}
	if len(s.Missing) != 0 || s.VM != nil {
		t.Fatalf("second not-applicable answer left a trace: Missing=%v VM=%v", s.Missing, s.VM)
	}
	// The recheck moved the window: 29 minutes later is still silent, no call.
	c.Collect(t0.Add(31*time.Minute + 29*time.Minute))
	if f.calls != 2 {
		t.Fatalf("factory calls = %d, want 2 (window restarts at the recheck)", f.calls)
	}
	// After the window the factory succeeds and the group collects.
	s, _ = c.Collect(t0.Add(31*time.Minute + 31*time.Minute))
	if f.calls != 3 {
		t.Fatalf("factory calls = %d, want 3", f.calls)
	}
	if s.VM == nil || s.VM.BalloonBytes == nil || *s.VM.BalloonBytes != 512<<20 {
		t.Fatalf("VM = %+v, want the payload once the group applies", s.VM)
	}
	if len(s.Missing) != 0 {
		t.Fatalf("Missing = %v, want empty", s.Missing)
	}
}

func TestNotApplicableThenTransientFailure(t *testing.T) {
	f := &naFactory{answer: func(n int) (Group, error) {
		switch n {
		case 2:
			return nil, errors.New("provider hiccup")
		default:
			return nil, errNotApplicable
		}
	}}
	c, _ := newNACollector(t, f)
	t0 := time.Unix(1_000_000, 0)
	c.Collect(t0)

	// Recheck hits a real failure: visible in Missing, normal backoff applies.
	at := t0.Add(31 * time.Minute)
	s, _ := c.Collect(at)
	if f.calls != 2 || !missingHas(s, "vm") {
		t.Fatalf("calls = %d, Missing = %v; want the failed recheck named in Missing", f.calls, s.Missing)
	}
	s, _ = c.Collect(at.Add(time.Second))
	if f.calls != 2 || !missingHas(s, "vm") {
		t.Fatalf("calls = %d, Missing = %v; want backoff respected and still named", f.calls, s.Missing)
	}
	// After the backoff the factory is asked again and says not applicable: silent.
	s, _ = c.Collect(at.Add(groupRetryDelay(2) + time.Second))
	if f.calls != 3 {
		t.Fatalf("calls = %d, want 3 after the backoff", f.calls)
	}
	if len(s.Missing) != 0 {
		t.Fatalf("Missing = %v, want silent again after not applicable", s.Missing)
	}
	// And it is back on the long interval, not the short one.
	c.Collect(at.Add(groupRetryDelay(2) + time.Minute))
	if f.calls != 3 {
		t.Fatalf("calls = %d, want 3 (long interval restored)", f.calls)
	}
}

// A built group that fails is a real failure and must stay visible.
func TestRealFailureStillMissing(t *testing.T) {
	f := &naFactory{answer: func(int) (Group, error) {
		return &fakeGroup{name: "vm", optional: true, err: errors.New("read failed")}, nil
	}}
	c, _ := newNACollector(t, f)
	s, _ := c.Collect(time.Unix(1_000_000, 0))
	if !missingHas(s, "vm") {
		t.Fatalf("Missing = %v, want vm named for a real failure", s.Missing)
	}
	// A group that returns a nil payload without an error is also Missing.
	f2 := &naFactory{answer: func(int) (Group, error) {
		return &fakeGroup{name: "vm", optional: true}, nil
	}}
	c2, _ := newNACollector(t, f2)
	s, _ = c2.Collect(time.Unix(1_000_000, 0))
	if !missingHas(s, "vm") {
		t.Fatalf("Missing = %v, want vm named for a nil payload", s.Missing)
	}
}

// A group that reports not applicable leaves no timing and no busy slot behind,
// even when its Collect took measurable time.
func TestRunGroupNotApplicableDoesNotCountSlowOrBusy(t *testing.T) {
	g := &fakeGroup{name: "vm", optional: true, err: errNotApplicable}
	b := NewBudget(15)
	c := NewGroupCollector(&stubCollector{}, []Group{g}, b).(*groupCollector)
	t0 := time.Unix(1_000_000, 0)
	for i := 0; i < 5; i++ {
		s, err := c.Collect(t0.Add(time.Duration(i) * 15 * time.Second))
		if err != nil {
			t.Fatal(err)
		}
		if len(s.Missing) != 0 {
			t.Fatalf("tick %d: Missing = %v, want empty (no busy, no slow, no name)", i, s.Missing)
		}
		if name, dur := c.slowName, c.slowDur; name != "" || dur != 0 {
			t.Fatalf("tick %d: slow = %q %v, want none", i, name, dur)
		}
		if len(c.GroupTimings()) != 0 {
			t.Fatalf("tick %d: GroupTimings = %v, want none", i, c.GroupTimings())
		}
		if c.slots[0].busy {
			t.Fatalf("tick %d: worker slot still busy", i)
		}
		if _, ok := b.groups["vm"]; ok {
			t.Fatalf("tick %d: budget has a record for the group; GroupTiming must not run", i)
		}
	}
	if g.calls != 5 {
		t.Fatalf("calls = %d, want 5 (slot freed every tick)", g.calls)
	}
}
