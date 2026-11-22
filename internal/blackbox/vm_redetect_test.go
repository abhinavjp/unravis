package blackbox

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

// seamSources scripts newVMSource: each call answers from answer(n), n from 1.
func seamSources(t *testing.T, answer func(n int) (CounterSource, error)) *int {
	t.Helper()
	saved := newVMSource
	t.Cleanup(func() { newVMSource = saved })
	calls := 0
	newVMSource = func([]string) (CounterSource, error) {
		calls++
		return answer(calls)
	}
	return &calls
}

func vmReading(v float64) []CounterReading { return []CounterReading{memReading(vmBalloonPath, v)} }

func absentErr() error {
	return fmt.Errorf("no counters: %w", &pdhStatusErr{call: "PdhAddEnglishCounter", path: vmBalloonPath, code: 0xC0000BB8})
}

func tickVM(t *testing.T, g Group, at time.Time) Sample {
	t.Helper()
	c := NewGroupCollector(&stubCollector{}, []Group{g}, NewBudget(15))
	s, err := c.Collect(at)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// VMware Tools removed while recording: the group says so for a short while, then
// becomes not applicable (silent), and picks the counter up again after a recheck.
func TestVMGroupToolsRemovedBecomesNotApplicable(t *testing.T) {
	first := &memPressureFakeSource{readings: vmReading(5)}
	var second *memPressureFakeSource
	calls := seamSources(t, func(n int) (CounterSource, error) {
		switch n {
		case 1: // the build
			return first, nil
		case 2: // the re-detect after the failures: the object is gone
			return nil, absentErr()
		default: // a much later recheck: Tools are back
			second = &memPressureFakeSource{readings: vmReading(7)}
			return second, nil
		}
	})
	gi, err := newVMGroup(GroupDeps{})
	if err != nil {
		t.Fatal(err)
	}
	g := gi.(*vmGroup)
	t0 := time.Unix(1_000_000, 0)
	if s := tickVM(t, g, t0); s.VM == nil || *s.VM.BalloonBytes != 5<<20 {
		t.Fatalf("first tick VM = %+v, want 5 MiB", s.VM)
	}

	first.readings = nil // Tools removed: no reading from now on
	for i := 1; i < vmRedetectAfter; i++ {
		s := tickVM(t, g, t0.Add(time.Duration(i)*15*time.Second))
		if !missingHas(s, "vm") {
			t.Fatalf("tick %d with no reading: Missing = %v, want vm named (still visible)", i, s.Missing)
		}
	}
	if *calls != 1 {
		t.Fatalf("source opened %d times before the threshold, want 1", *calls)
	}
	// The threshold tick re-detects: the object is gone, so the group goes silent.
	s := tickVM(t, g, t0.Add(time.Duration(vmRedetectAfter)*15*time.Second))
	if *calls != 2 || missingHas(s, "vm") || len(s.Missing) != 0 {
		t.Fatalf("calls=%d Missing=%v, want one re-detect and then silence", *calls, s.Missing)
	}
	if !first.closed {
		t.Fatal("the failing source was not closed")
	}
	if !g.Dormant() {
		t.Fatal("group not dormant after the object vanished")
	}
	// Silent, without opening the counter again, for the whole recheck interval.
	for _, at := range []time.Duration{time.Minute, 10 * time.Minute, 29 * time.Minute} {
		s := tickVM(t, g, t0.Add(at))
		if len(s.Missing) != 0 {
			t.Fatalf("at +%v: Missing = %v, want silent", at, s.Missing)
		}
	}
	if *calls != 2 {
		t.Fatalf("source opened %d times inside the recheck interval, want 2", *calls)
	}
	// After the interval the counter is found again and read.
	s = tickVM(t, g, t0.Add(time.Duration(vmRedetectAfter)*15*time.Second+notApplicableRecheck+time.Second))
	if *calls != 3 || s.VM == nil || *s.VM.BalloonBytes != 7<<20 || len(s.Missing) != 0 {
		t.Fatalf("calls=%d VM=%+v Missing=%v, want the counter back with 7 MiB", *calls, s.VM, s.Missing)
	}
	if g.Dormant() {
		t.Fatal("still dormant after the counter came back")
	}
	if err := g.Close(); err != nil || !second.closed {
		t.Fatalf("Close did not close the reopened source: %v", err)
	}
}

// A good reading in between resets the count: a flaky tick never triggers a re-detect.
func TestVMGroupGoodReadingResetsTheFailureCount(t *testing.T) {
	src := &memPressureFakeSource{}
	calls := seamSources(t, func(int) (CounterSource, error) { return src, nil })
	gi, _ := newVMGroup(GroupDeps{})
	g := gi.(*vmGroup)
	t0 := time.Unix(1_000_000, 0)
	for round := 0; round < 3; round++ {
		src.readings = nil
		for i := 0; i < vmRedetectAfter-1; i++ {
			tickVM(t, g, t0.Add(time.Duration(round*10+i)*15*time.Second))
		}
		src.readings = vmReading(1)
		tickVM(t, g, t0.Add(time.Duration(round*10+9)*15*time.Second))
	}
	if *calls != 1 {
		t.Fatalf("source opened %d times, want 1 (the build): flaky ticks must not re-detect", *calls)
	}
}

// The counter still exists but keeps failing: the group stays visible in Missing
// (a real failure is never hidden) and tries a fresh source each time.
func TestVMGroupRealFailureStaysMissingAfterRedetect(t *testing.T) {
	var all []*memPressureFakeSource
	seamSources(t, func(int) (CounterSource, error) {
		s := &memPressureFakeSource{err: errors.New("read failed")}
		all = append(all, s)
		return s, nil
	})
	gi, _ := newVMGroup(GroupDeps{})
	g := gi.(*vmGroup)
	t0 := time.Unix(1_000_000, 0)
	for i := 0; i < vmRedetectAfter*2+1; i++ {
		s := tickVM(t, g, t0.Add(time.Duration(i)*15*time.Second))
		if !missingHas(s, "vm") {
			t.Fatalf("tick %d: Missing = %v, want vm (never silent while the counter exists)", i, s.Missing)
		}
	}
	if g.Dormant() {
		t.Fatal("dormant although the object still exists")
	}
	if len(all) < 2 || !all[0].closed {
		t.Fatalf("sources = %d, first closed = %v; want a fresh source and the old one closed", len(all), len(all) > 0 && all[0].closed)
	}
}

// Another open failure (not "absent") is a real failure: visible, retried later.
func TestVMGroupRedetectOtherErrorIsMissingThenRetries(t *testing.T) {
	src := &memPressureFakeSource{}
	calls := seamSources(t, func(n int) (CounterSource, error) {
		switch n {
		case 1:
			return src, nil
		case 2:
			return nil, errors.New("PdhOpenQuery: 0x1")
		default:
			return &memPressureFakeSource{readings: vmReading(3)}, nil
		}
	})
	gi, _ := newVMGroup(GroupDeps{})
	g := gi.(*vmGroup)
	t0 := time.Unix(1_000_000, 0)
	var last Sample
	for i := 0; i < vmRedetectAfter; i++ {
		last = tickVM(t, g, t0.Add(time.Duration(i)*15*time.Second))
	}
	if !missingHas(last, "vm") || g.Dormant() {
		t.Fatalf("Missing=%v dormant=%v, want vm named and not dormant", last.Missing, g.Dormant())
	}
	soon := t0.Add(time.Duration(vmRedetectAfter)*15*time.Second + time.Second)
	if s := tickVM(t, g, soon); *calls != 2 || !missingHas(s, "vm") {
		t.Fatalf("calls=%d Missing=%v, want backoff respected and vm named", *calls, s.Missing)
	}
	if s := tickVM(t, g, soon.Add(groupRetryDelay(2)+time.Second)); *calls != 3 || s.VM == nil {
		t.Fatalf("calls=%d VM=%+v, want the retry to recover", *calls, s.VM)
	}
}

// A shared (test) source is never re-detected: the group does not own it.
func TestVMGroupSharedSourceIsNeverRedetected(t *testing.T) {
	calls := seamSources(t, func(int) (CounterSource, error) { return nil, absentErr() })
	src := &memPressureFakeSource{}
	g := newVMTestGroup(t, src)
	t0 := time.Unix(1_000_000, 0)
	for i := 0; i < vmRedetectAfter*3; i++ {
		if s := tickVM(t, g, t0.Add(time.Duration(i)*15*time.Second)); !missingHas(s, "vm") {
			t.Fatalf("tick %d: Missing = %v, want vm", i, s.Missing)
		}
	}
	if *calls != 0 || src.closed || g.Dormant() {
		t.Fatalf("calls=%d closed=%v dormant=%v; a shared source must be left alone", *calls, src.closed, g.Dormant())
	}
}
