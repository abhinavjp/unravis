package blackbox

import (
	"testing"
	"time"
)

// With nothing selected the tick takes the cheap snapshot (no creation times);
// the generation still moves when a process comes or goes, so the groups that
// follow processes keep working (PERF-04).
func TestResolveTickWithoutTargetsUsesLiteSnapshot(t *testing.T) {
	full, lite := 0, 0
	set := []ProcInfo{{PID: 10, Name: "a.exe"}}
	r := NewResolver()
	r.snapshotFn = func() ([]ProcInfo, error) {
		full++
		return []ProcInfo{{PID: 10, Name: "a.exe", StartTicks: 5}}, nil
	}
	r.liteFn = func() ([]ProcInfo, error) {
		lite++
		return append([]ProcInfo(nil), set...), nil
	}
	now := time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)

	v1 := r.resolveTick(nil, now)
	v2 := r.resolveTick(nil, now.Add(15*time.Second))
	if full != 0 || lite != 2 {
		t.Fatalf("full=%d lite=%d, want 0 and 2 without targets", full, lite)
	}
	if v2.gen != v1.gen {
		t.Fatalf("generation moved without a change: %d -> %d", v1.gen, v2.gen)
	}
	set = append(set, ProcInfo{PID: 11, Name: "b.exe"}) // a process starts
	v3 := r.resolveTick(nil, now.Add(30*time.Second))
	if v3.gen <= v2.gen {
		t.Fatalf("generation did not move when a process started: %d -> %d", v2.gen, v3.gen)
	}
	set = set[:1] // and exits
	if v4 := r.resolveTick(nil, now.Add(45*time.Second)); v4.gen <= v3.gen {
		t.Fatalf("generation did not move when a process exited: %d -> %d", v3.gen, v4.gen)
	}
}

// A tick with targets keeps the full snapshot: identity needs the creation time.
func TestResolveTickWithTargetsUsesFullSnapshot(t *testing.T) {
	full, lite := 0, 0
	r := NewResolver()
	r.snapshotFn = func() ([]ProcInfo, error) {
		full++
		return []ProcInfo{{PID: 10, Name: "svc.exe", StartTicks: 5}}, nil
	}
	r.liteFn = func() ([]ProcInfo, error) { lite++; return nil, nil }
	r.instancesFn = func() ([]string, error) { return nil, nil }
	r.servicesFn = func() ([]svcProc, error) { return nil, nil }
	r.resolveTick([]Target{{Kind: "service", Name: "Svc"}}, time.Now())
	if full != 1 || lite != 0 {
		t.Fatalf("full=%d lite=%d, want 1 and 0 with targets", full, lite)
	}
}

// Without a lite function the full snapshot is used (every existing caller).
func TestResolveTickFallsBackToFullSnapshot(t *testing.T) {
	full := 0
	r := NewResolver()
	r.snapshotFn = func() ([]ProcInfo, error) { full++; return nil, nil }
	r.resolveTick(nil, time.Now())
	if full != 1 {
		t.Fatalf("full=%d, want 1", full)
	}
}
