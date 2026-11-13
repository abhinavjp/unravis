package blackbox

import (
	"testing"
	"time"
)

// proc10FakeSource is a fake CounterSource with expansion bookkeeping: Paths
// feeds the spec build, RefreshInstances records re-expansion calls.
type proc10FakeSource struct {
	paths     []string
	readings  []CounterReading
	refreshes int
	closed    bool
}

func (f *proc10FakeSource) Collect() ([]CounterReading, error) { return f.readings, nil }
func (f *proc10FakeSource) Close() error                       { f.closed = true; return nil }
func (f *proc10FakeSource) Paths() []string                    { return f.paths }
func (f *proc10FakeSource) RefreshInstances() error            { f.refreshes++; return nil }

func proc10Path(inst, counter string) string { return `\Process(` + inst + `)\` + counter }

func proc10Reading(inst, counter string, v float64) CounterReading {
	return CounterReading{Path: proc10Path(inst, counter), Value: v, OK: true}
}

// proc10Instance adds the four expanded paths and one valid reading per counter
// for a single Process instance (the #N suffix is part of the instance name).
func proc10Instance(src *proc10FakeSource, inst string, pid, cpu, ws, pb float64) {
	for _, c := range []string{"ID Process", "% Processor Time", "Working Set", "Private Bytes"} {
		src.paths = append(src.paths, proc10Path(inst, c))
	}
	src.readings = append(src.readings,
		proc10Reading(inst, "ID Process", pid),
		proc10Reading(inst, "% Processor Time", cpu),
		proc10Reading(inst, "Working Set", ws),
		proc10Reading(inst, "Private Bytes", pb),
	)
}

func newProc10TestGroup(t *testing.T, src *proc10FakeSource, targets []ResolvedTarget, changed func() bool) *proc10Group {
	t.Helper()
	g, err := newProc10Group(GroupDeps{
		Source:            src,
		Cores:             4,
		Targets:           func() []ResolvedTarget { return targets },
		ProcessSetChanged: changed,
	})
	if err != nil {
		t.Fatalf("newProc10Group: %v", err)
	}
	return g.(*proc10Group)
}

// proc10Collect runs n ticks 15 s apart and returns the last group.
func proc10Collect(t *testing.T, g *proc10Group, n int) *Proc10Group {
	t.Helper()
	now := time.Unix(1000, 0)
	var grp *Proc10Group
	for i := 0; i < n; i++ {
		v, err := g.Collect(now.Add(time.Duration(i)*15*time.Second), 15)
		if err != nil {
			t.Fatalf("tick %d: %v", i+1, err)
		}
		grp, _ = v.(*Proc10Group)
	}
	return grp
}

func TestProc10PIDMap(t *testing.T) {
	src := &proc10FakeSource{}
	proc10Instance(src, "w3wp", 4321, 10, 1000, 2000)
	proc10Instance(src, "w3wp#1", 7777, 20, 3000, 4000)
	proc10Instance(src, "dotnet", 900, 30, 5000, 6000) // unselected: must not appear
	targets := []ResolvedTarget{
		{Key: "pool:a", PID: 7777, Start: 1000, Runtime: 2},
		{Key: "pool:b", PID: 4321, Start: 1000, Runtime: 2},
		{Key: "pool:stale", PID: 4321, Start: 0, Runtime: 2}, // Start unconfirmed: omit
	}
	g := newProc10TestGroup(t, src, targets, nil)
	grp := proc10Collect(t, g, 3) // two baselines, then the CPU rate emits
	if grp == nil || len(grp.Targets) != 2 {
		t.Fatalf("targets = %+v, want 2 (selected, Start confirmed)", grp)
	}
	a, b := grp.Targets[0], grp.Targets[1] // sorted by key
	if a.Target != "pool:a" || b.Target != "pool:b" {
		t.Fatalf("keys = %q,%q; want pool:a,pool:b (instance→PID matched the right target)", a.Target, b.Target)
	}
	check := func(name string, got *float64, want float64) {
		t.Helper()
		if got == nil || *got != want {
			t.Errorf("%s = %v, want %v", name, got, want)
		}
	}
	checkU := func(name string, got *uint64, want uint64) {
		t.Helper()
		if got == nil || *got != want {
			t.Errorf("%s = %v, want %v", name, got, want)
		}
	}
	check("a.CPUPct", a.CPUPct, 20) // w3wp#1 → PID 7777 → pool:a
	checkU("a.WorkingSet", a.WorkingSet, 3000)
	checkU("a.PrivateBytes", a.PrivateBytes, 4000)
	check("b.CPUPct", b.CPUPct, 10) // w3wp → PID 4321 → pool:b
	checkU("b.WorkingSet", b.WorkingSet, 1000)
	checkU("b.PrivateBytes", b.PrivateBytes, 2000)
}

func TestProc10Net10Only(t *testing.T) {
	src := &proc10FakeSource{}
	proc10Instance(src, "w3wp", 4321, 10, 1000, 2000)   // runtime 1: skip
	proc10Instance(src, "dotnet", 7777, 20, 3000, 4000) // runtime 2: keep
	proc10Instance(src, "svc", 900, 30, 5000, 6000)     // runtime 0: skip
	targets := []ResolvedTarget{
		{Key: "pool:fw", PID: 4321, Start: 1000, Runtime: 1},
		{Key: "pool:net10", PID: 7777, Start: 1000, Runtime: 2},
		{Key: "svc:unk", PID: 900, Start: 1000, Runtime: 0},
	}
	g := newProc10TestGroup(t, src, targets, nil)
	grp := proc10Collect(t, g, 3)
	if grp == nil || len(grp.Targets) != 1 {
		t.Fatalf("targets = %+v, want only the runtime-2 target", grp)
	}
	if grp.Targets[0].Target != "pool:net10" {
		t.Fatalf("target = %q, want pool:net10 (runtime 1 and 0 skipped)", grp.Targets[0].Target)
	}
}

func TestProc10MultiCoreNotCapped(t *testing.T) {
	src := &proc10FakeSource{}
	proc10Instance(src, "dotnet", 7777, 250, 1000, 2000)
	targets := []ResolvedTarget{{Key: "pool:a", PID: 7777, Start: 1000, Runtime: 2}}
	g := newProc10TestGroup(t, src, targets, nil)
	grp := proc10Collect(t, g, 3)
	if grp == nil || len(grp.Targets) != 1 {
		t.Fatalf("targets = %+v, want 1", grp)
	}
	if got := grp.Targets[0].CPUPct; got == nil || *got != 250 {
		t.Fatalf("CPUPct = %v, want 250 (multi-core, never capped at 100)", got)
	}
}

func TestProc10MissingOmitted(t *testing.T) {
	src := &proc10FakeSource{}
	// w3wp: Working Set invalid, the rest valid.
	for _, c := range []string{"ID Process", "% Processor Time", "Working Set", "Private Bytes"} {
		src.paths = append(src.paths, proc10Path("w3wp", c))
	}
	src.readings = append(src.readings,
		proc10Reading("w3wp", "ID Process", 4321),
		proc10Reading("w3wp", "% Processor Time", 10),
		CounterReading{Path: proc10Path("w3wp", "Working Set"), Value: 99, Status: 1, OK: false},
		proc10Reading("w3wp", "Private Bytes", 2000),
	)
	// dotnet: instance present (valid ID Process) but every metric invalid.
	for _, c := range []string{"ID Process", "% Processor Time", "Working Set", "Private Bytes"} {
		src.paths = append(src.paths, proc10Path("dotnet", c))
	}
	src.readings = append(src.readings,
		proc10Reading("dotnet", "ID Process", 7777),
		CounterReading{Path: proc10Path("dotnet", "% Processor Time"), Value: 1, Status: 1, OK: false},
		CounterReading{Path: proc10Path("dotnet", "Working Set"), Value: 2, Status: 1, OK: false},
		CounterReading{Path: proc10Path("dotnet", "Private Bytes"), Value: 3, Status: 1, OK: false},
	)
	targets := []ResolvedTarget{
		{Key: "pool:a", PID: 4321, Start: 1000, Runtime: 2},
		{Key: "pool:b", PID: 7777, Start: 1000, Runtime: 2},      // no valid metric: omit
		{Key: "pool:noinst", PID: 5555, Start: 1000, Runtime: 2}, // no instance: omit
	}
	g := newProc10TestGroup(t, src, targets, nil)
	grp := proc10Collect(t, g, 3)
	if grp == nil || len(grp.Targets) != 1 {
		t.Fatalf("targets = %+v, want only pool:a", grp)
	}
	row := grp.Targets[0]
	if row.Target != "pool:a" {
		t.Fatalf("target = %q, want pool:a", row.Target)
	}
	if row.WorkingSet != nil {
		t.Fatalf("invalid Working Set emitted %v; missing must stay nil, never 0", *row.WorkingSet)
	}
	if row.CPUPct == nil || *row.CPUPct != 10 {
		t.Fatalf("CPUPct = %v, want 10", row.CPUPct)
	}
	if row.PrivateBytes == nil || *row.PrivateBytes != 2000 {
		t.Fatalf("PrivateBytes = %v, want 2000", row.PrivateBytes)
	}

	// Nothing valid anywhere: the group is nil, never zero-filled.
	src2 := &proc10FakeSource{}
	proc10Instance(src2, "dotnet", 7777, 1, 2, 3)
	for i := range src2.readings {
		src2.readings[i].OK = false
		src2.readings[i].Status = 1
	}
	g2 := newProc10TestGroup(t, src2, []ResolvedTarget{{Key: "pool:a", PID: 7777, Start: 1, Runtime: 2}}, nil)
	if v, err := g2.Collect(time.Unix(1000, 0), 0); err != nil || v != nil {
		t.Fatalf("all-invalid: (%v, %v), want (nil, nil)", v, err)
	}
}

func TestProc10RateGated(t *testing.T) {
	src := &proc10FakeSource{}
	proc10Instance(src, "dotnet", 7777, 50, 1000, 2000)
	targets := []ResolvedTarget{{Key: "pool:a", PID: 7777, Start: 1000, Runtime: 2}}
	g := newProc10TestGroup(t, src, targets, nil)
	now := time.Unix(1000, 0)
	for i := 0; i < 2; i++ { // two baselines: CPU rate absent, gauges present
		v, err := g.Collect(now.Add(time.Duration(i)*15*time.Second), 15)
		if err != nil {
			t.Fatalf("tick %d: %v", i+1, err)
		}
		row := v.(*Proc10Group).Targets[0]
		if row.CPUPct != nil {
			t.Fatalf("tick %d: CPU rate emitted before two baselines", i+1)
		}
		if row.WorkingSet == nil || row.PrivateBytes == nil {
			t.Fatalf("tick %d: gauges absent", i+1)
		}
	}
	v, err := g.Collect(now.Add(30*time.Second), 15)
	if err != nil {
		t.Fatalf("tick 3: %v", err)
	}
	row := v.(*Proc10Group).Targets[0]
	if row.CPUPct == nil || *row.CPUPct != 50 {
		t.Fatalf("CPUPct = %v, want 50 after two baselines", row.CPUPct)
	}
}

func TestProc10RefreshOnChange(t *testing.T) {
	src := &proc10FakeSource{}
	proc10Instance(src, "w3wp", 4321, 10, 1000, 2000)
	targets := []ResolvedTarget{
		{Key: "pool:a", PID: 4321, Start: 1000, Runtime: 2},
		{Key: "pool:b", PID: 7777, Start: 1000, Runtime: 2},
	}
	changed := false
	g := newProc10TestGroup(t, src, targets, func() bool { return changed })
	now := time.Unix(1000, 0)
	if _, err := g.Collect(now, 0); err != nil {
		t.Fatal(err)
	}
	if src.refreshes != 0 {
		t.Fatalf("refreshes = %d, want 0 without a process-set change", src.refreshes)
	}
	// The process set changes: a new instance appears; re-expand once, pick it up.
	changed = true
	proc10Instance(src, "dotnet", 7777, 20, 3000, 4000)
	v, err := g.Collect(now.Add(15*time.Second), 15)
	if err != nil {
		t.Fatal(err)
	}
	if src.refreshes != 1 {
		t.Fatalf("refreshes = %d, want 1 on change", src.refreshes)
	}
	if grp := v.(*Proc10Group); len(grp.Targets) != 2 {
		t.Fatalf("targets = %d, want 2 after re-expansion", len(grp.Targets))
	}
	changed = false
	if _, err := g.Collect(now.Add(30*time.Second), 15); err != nil {
		t.Fatal(err)
	}
	if src.refreshes != 1 {
		t.Fatalf("refreshes = %d, want no re-expand without a change", src.refreshes)
	}
}

func TestProc10Registered(t *testing.T) {
	registryMu.Lock()
	reg, ok := registry["proc10"]
	registryMu.Unlock()
	if !ok {
		t.Fatal("proc10 group not registered from init()")
	}
	if !reg.optional {
		t.Fatal("proc10 group must be optional (ships off when it breaks budget)")
	}
}
