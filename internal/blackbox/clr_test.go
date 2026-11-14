package blackbox

import (
	"testing"
	"time"
)

// clrFakeSource is a fake CounterSource with the PDH Paths/RefreshInstances
// surface; readings are canned and re-expansions are counted.
type clrFakeSource struct {
	paths     []string
	readings  []CounterReading
	collects  int
	refreshes int
	closed    bool
}

func (f *clrFakeSource) Collect() ([]CounterReading, error) { f.collects++; return f.readings, nil }
func (f *clrFakeSource) Close() error                       { f.closed = true; return nil }
func (f *clrFakeSource) Paths() []string                    { return f.paths }
func (f *clrFakeSource) RefreshInstances() error            { f.refreshes++; return nil }

func clrPath(obj, inst, counter string) string { return `\` + obj + `(` + inst + `)\` + counter }

func clrReading(obj, inst, counter string, v float64) CounterReading {
	return CounterReading{Path: clrPath(obj, inst, counter), Value: v, OK: true}
}

// clrInstancePaths expands every CLR counter path for one instance.
func clrInstancePaths(inst string) []string {
	ps := []string{
		clrPath(".NET CLR Memory", inst, "Process ID"),
		clrPath(".NET CLR Memory", inst, "Gen 0 heap size"),
		clrPath(".NET CLR Memory", inst, "Gen 1 heap size"),
		clrPath(".NET CLR Memory", inst, "Gen 2 heap size"),
		clrPath(".NET CLR Memory", inst, "Large Object Heap size"),
		clrPath(".NET CLR Memory", inst, "% Time in GC"),
		clrPath(".NET CLR Memory", inst, "Allocated Bytes/sec"),
		clrPath(".NET CLR LocksAndThreads", inst, "Contention Rate/sec"),
		clrPath(".NET CLR LocksAndThreads", inst, "# of current physical threads"),
		clrPath(".NET CLR Exceptions", inst, "# of Exceps Thrown/sec"),
	}
	return ps
}

func newCLRTestGroup(t *testing.T, src *clrFakeSource, targets func() []ResolvedTarget, changed func() bool) *clrGroup {
	t.Helper()
	g, err := newCLRGroup(GroupDeps{Source: src, Cores: 4, Targets: targets, ProcessSetChanged: changed})
	if err != nil {
		t.Fatalf("newCLRGroup: %v", err)
	}
	return g.(*clrGroup)
}

func clrCollect(t *testing.T, g *clrGroup, now time.Time) *CLRGroup {
	t.Helper()
	v, err := g.Collect(now, 15)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if v == nil {
		return nil
	}
	grp, ok := v.(*CLRGroup)
	if !ok {
		t.Fatalf("Collect returned %T, want *CLRGroup", v)
	}
	return grp
}

// Two instances named w3wp / w3wp#1 report PIDs 100 / 200 via the Process ID
// counter; targets must match by PID, never by instance name.
func TestCLRInstancePIDMap(t *testing.T) {
	src := &clrFakeSource{}
	src.paths = append(src.paths, clrInstancePaths("w3wp")...)
	src.paths = append(src.paths, clrInstancePaths("w3wp#1")...)
	src.readings = []CounterReading{
		clrReading(".NET CLR Memory", "w3wp", "Process ID", 100),
		clrReading(".NET CLR Memory", "w3wp#1", "Process ID", 200),
		clrReading(".NET CLR Memory", "w3wp", "Gen 0 heap size", 111),
		clrReading(".NET CLR Memory", "w3wp#1", "Gen 0 heap size", 222),
	}
	g := newCLRTestGroup(t, src, func() []ResolvedTarget {
		return []ResolvedTarget{
			{Key: "pool:a", PID: 200, Start: 10, Runtime: 1},
			{Key: "pool:b", PID: 100, Start: 20, Runtime: 1},
		}
	}, nil)
	grp := clrCollect(t, g, time.Unix(1000, 0))
	if grp == nil || len(grp.Targets) != 2 {
		t.Fatalf("group = %+v, want 2 targets matched by PID", grp)
	}
	a, b := grp.Targets[0], grp.Targets[1]
	if a.Target != "pool:a" || b.Target != "pool:b" {
		t.Fatalf("targets = %q,%q; want sorted pool:a,pool:b", a.Target, b.Target)
	}
	if a.Gen0Bytes == nil || *a.Gen0Bytes != 222 {
		t.Fatalf("pool:a Gen0Bytes = %v, want 222 (w3wp#1, PID 200)", a.Gen0Bytes)
	}
	if b.Gen0Bytes == nil || *b.Gen0Bytes != 111 {
		t.Fatalf("pool:b Gen0Bytes = %v, want 111 (w3wp, PID 100)", b.Gen0Bytes)
	}
}

// A runtime-2 (.NET 10) target is skipped even when its CLR instance exists.
func TestCLRFrameworkOnly(t *testing.T) {
	src := &clrFakeSource{}
	src.paths = append(src.paths, clrInstancePaths("w3wp")...)
	src.paths = append(src.paths, clrInstancePaths("dotnet")...)
	src.readings = []CounterReading{
		clrReading(".NET CLR Memory", "w3wp", "Process ID", 100),
		clrReading(".NET CLR Memory", "dotnet", "Process ID", 200),
		clrReading(".NET CLR Memory", "w3wp", "Gen 0 heap size", 1),
		clrReading(".NET CLR Memory", "dotnet", "Gen 0 heap size", 2),
	}
	g := newCLRTestGroup(t, src, func() []ResolvedTarget {
		return []ResolvedTarget{
			{Key: "pool:fw", PID: 100, Start: 1, Runtime: 1},
			{Key: "svc:core", PID: 200, Start: 2, Runtime: 2},
		}
	}, nil)
	grp := clrCollect(t, g, time.Unix(1000, 0))
	if grp == nil || len(grp.Targets) != 1 {
		t.Fatalf("group = %+v, want only the runtime-1 target", grp)
	}
	if grp.Targets[0].Target != "pool:fw" {
		t.Fatalf("target = %q, want pool:fw (runtime 2 skipped)", grp.Targets[0].Target)
	}
}

// Only owner-selected targets are emitted; an unselected process with a live
// CLR instance never appears. No selection at all yields no group.
func TestCLRSelectedOnly(t *testing.T) {
	src := &clrFakeSource{}
	src.paths = append(src.paths, clrInstancePaths("w3wp")...)
	src.paths = append(src.paths, clrInstancePaths("other")...)
	src.readings = []CounterReading{
		clrReading(".NET CLR Memory", "w3wp", "Process ID", 100),
		clrReading(".NET CLR Memory", "other", "Process ID", 200),
		clrReading(".NET CLR Memory", "w3wp", "Gen 0 heap size", 1),
		clrReading(".NET CLR Memory", "other", "Gen 0 heap size", 2),
	}
	g := newCLRTestGroup(t, src, func() []ResolvedTarget {
		return []ResolvedTarget{{Key: "pool:sel", PID: 100, Start: 1, Runtime: 1}}
	}, nil)
	grp := clrCollect(t, g, time.Unix(1000, 0))
	if grp == nil || len(grp.Targets) != 1 || grp.Targets[0].Target != "pool:sel" {
		t.Fatalf("group = %+v, want only the selected pool:sel", grp)
	}
	// No targets selected: nothing is sampled, group omitted.
	gNone := newCLRTestGroup(t, src, nil, nil)
	if grp := clrCollect(t, gNone, time.Unix(1000, 0)); grp != nil {
		t.Fatalf("group = %+v, want nil with no selected targets", grp)
	}
}

// Invalid counters stay nil (never 0); a target whose PID has no CLR instance
// is omitted; nothing valid yields a nil group.
func TestCLRMissingOmitted(t *testing.T) {
	src := &clrFakeSource{}
	src.paths = append(src.paths, clrInstancePaths("w3wp")...)
	src.readings = []CounterReading{
		clrReading(".NET CLR Memory", "w3wp", "Process ID", 100),
		clrReading(".NET CLR Memory", "w3wp", "Gen 0 heap size", 5),
		{Path: clrPath(".NET CLR Memory", "w3wp", "Gen 1 heap size"), Value: 9, Status: 1, OK: false},
	}
	g := newCLRTestGroup(t, src, func() []ResolvedTarget {
		return []ResolvedTarget{
			{Key: "pool:a", PID: 100, Start: 1, Runtime: 1},
			{Key: "pool:ghost", PID: 300, Start: 2, Runtime: 1}, // no instance
		}
	}, nil)
	grp := clrCollect(t, g, time.Unix(1000, 0))
	if grp == nil || len(grp.Targets) != 1 {
		t.Fatalf("group = %+v, want 1 target (ghost omitted)", grp)
	}
	row := grp.Targets[0]
	if row.Target != "pool:a" {
		t.Fatalf("target = %q, want pool:a", row.Target)
	}
	if row.Gen0Bytes == nil || *row.Gen0Bytes != 5 {
		t.Fatalf("Gen0Bytes = %v, want 5", row.Gen0Bytes)
	}
	if row.Gen1Bytes != nil {
		t.Fatalf("invalid counter emitted %v; missing must stay nil, never 0", *row.Gen1Bytes)
	}
	// Every reading invalid: no instance mapping, group omitted.
	for i := range src.readings {
		src.readings[i].OK = false
		src.readings[i].Status = 1
	}
	if grp := clrCollect(t, g, time.Unix(1015, 0)); grp != nil {
		t.Fatalf("group = %+v, want nil when nothing is valid", grp)
	}
}

// Rates are absent until two valid baselines; gauges emit from tick one.
func TestCLRRateGated(t *testing.T) {
	src := &clrFakeSource{}
	src.paths = append(src.paths, clrInstancePaths("w3wp")...)
	src.readings = []CounterReading{
		clrReading(".NET CLR Memory", "w3wp", "Process ID", 100),
		clrReading(".NET CLR Memory", "w3wp", "Gen 0 heap size", 1),
		clrReading(".NET CLR Memory", "w3wp", "Allocated Bytes/sec", 50),
		clrReading(".NET CLR LocksAndThreads", "w3wp", "Contention Rate/sec", 7),
		clrReading(".NET CLR Exceptions", "w3wp", "# of Exceps Thrown/sec", 3),
	}
	g := newCLRTestGroup(t, src, func() []ResolvedTarget {
		return []ResolvedTarget{{Key: "pool:a", PID: 100, Start: 1, Runtime: 1}}
	}, nil)
	now := time.Unix(1000, 0)
	for i := 0; i < 2; i++ {
		grp := clrCollect(t, g, now.Add(time.Duration(i)*15*time.Second))
		if grp == nil || len(grp.Targets) != 1 {
			t.Fatalf("tick %d: group = %+v, want one target", i+1, grp)
		}
		row := grp.Targets[0]
		if row.Gen0Bytes == nil {
			t.Fatalf("tick %d: gauge absent", i+1)
		}
		if row.AllocBytesPerSec != nil || row.ContentionPerSec != nil || row.ExceptionsPerSec != nil {
			t.Fatalf("tick %d: rate emitted before two baselines", i+1)
		}
	}
	grp := clrCollect(t, g, now.Add(30*time.Second))
	row := grp.Targets[0]
	if row.AllocBytesPerSec == nil || *row.AllocBytesPerSec != 50 {
		t.Fatalf("AllocBytesPerSec = %v, want 50 after two baselines", row.AllocBytesPerSec)
	}
	if row.ContentionPerSec == nil || *row.ContentionPerSec != 7 {
		t.Fatalf("ContentionPerSec = %v, want 7 after two baselines", row.ContentionPerSec)
	}
	if row.ExceptionsPerSec == nil || *row.ExceptionsPerSec != 3 {
		t.Fatalf("ExceptionsPerSec = %v, want 3 after two baselines", row.ExceptionsPerSec)
	}
}

// Heap sizes map to uint64 fields; % Time in GC is a gauge, never capped at
// 100 (per-process, multi-core GC can exceed it).
func TestCLRHeapSizes(t *testing.T) {
	src := &clrFakeSource{}
	src.paths = append(src.paths, clrInstancePaths("w3wp")...)
	src.readings = []CounterReading{
		clrReading(".NET CLR Memory", "w3wp", "Process ID", 100),
		clrReading(".NET CLR Memory", "w3wp", "Gen 0 heap size", 1024),
		clrReading(".NET CLR Memory", "w3wp", "Gen 1 heap size", 2048),
		clrReading(".NET CLR Memory", "w3wp", "Gen 2 heap size", 4096),
		clrReading(".NET CLR Memory", "w3wp", "Large Object Heap size", 8192),
		clrReading(".NET CLR Memory", "w3wp", "% Time in GC", 350),
		clrReading(".NET CLR LocksAndThreads", "w3wp", "# of current physical threads", 12),
	}
	g := newCLRTestGroup(t, src, func() []ResolvedTarget {
		return []ResolvedTarget{{Key: "pool:a", PID: 100, Start: 1, Runtime: 1}}
	}, nil)
	grp := clrCollect(t, g, time.Unix(1000, 0))
	if grp == nil || len(grp.Targets) != 1 {
		t.Fatalf("group = %+v, want one target", grp)
	}
	row := grp.Targets[0]
	checkU64 := func(name string, got *uint64, want uint64) {
		t.Helper()
		if got == nil || *got != want {
			t.Errorf("%s = %v, want %d", name, got, want)
		}
	}
	checkU64("Gen0Bytes", row.Gen0Bytes, 1024)
	checkU64("Gen1Bytes", row.Gen1Bytes, 2048)
	checkU64("Gen2Bytes", row.Gen2Bytes, 4096)
	checkU64("LOHBytes", row.LOHBytes, 8192)
	if row.PctTimeInGC == nil || *row.PctTimeInGC != 350 {
		t.Fatalf("PctTimeInGC = %v, want 350 (multi-core, never capped at 100)", row.PctTimeInGC)
	}
	if row.Threads == nil || *row.Threads != 12 {
		t.Fatalf("Threads = %v, want 12", row.Threads)
	}
}

// Wildcards re-expand only on ProcessSetChanged(), on the same source handle.
func TestCLRRefreshOnChange(t *testing.T) {
	src := &clrFakeSource{paths: clrInstancePaths("w3wp")}
	src.readings = []CounterReading{
		clrReading(".NET CLR Memory", "w3wp", "Process ID", 100),
		clrReading(".NET CLR Memory", "w3wp", "Gen 0 heap size", 1),
	}
	targets := []ResolvedTarget{{Key: "pool:a", PID: 100, Start: 1, Runtime: 1}}
	changed := false
	g := newCLRTestGroup(t, src, func() []ResolvedTarget { return targets }, func() bool { return changed })
	now := time.Unix(1000, 0)
	if grp := clrCollect(t, g, now); grp == nil {
		t.Fatal("first collect: nil group")
	}
	if src.refreshes != 0 {
		t.Fatalf("refreshes = %d, want 0 without a process-set change", src.refreshes)
	}
	// A new process appears: re-expand once and pick up its instance.
	changed = true
	src.paths = append(src.paths, clrInstancePaths("w3wp#1")...)
	src.readings = append(src.readings,
		clrReading(".NET CLR Memory", "w3wp#1", "Process ID", 200),
		clrReading(".NET CLR Memory", "w3wp#1", "Gen 0 heap size", 2),
	)
	targets = append(targets, ResolvedTarget{Key: "pool:b", PID: 200, Start: 2, Runtime: 1})
	grp := clrCollect(t, g, now.Add(15*time.Second))
	if src.refreshes != 1 {
		t.Fatalf("refreshes = %d, want 1 on change", src.refreshes)
	}
	if grp == nil || len(grp.Targets) != 2 {
		t.Fatalf("group = %+v, want 2 targets after re-expansion", grp)
	}
	changed = false
	if grp := clrCollect(t, g, now.Add(30*time.Second)); grp == nil {
		t.Fatal("third collect: nil group")
	}
	if src.refreshes != 1 {
		t.Fatalf("refreshes = %d, want no re-expand without a change", src.refreshes)
	}
}

// No selected targets: Collect short-circuits before sampling — the
// machine-wide PDH query is never collected and no engine work runs (BL-122).
func TestCLRNoTargetsNoSample(t *testing.T) {
	src := &clrFakeSource{paths: clrInstancePaths("w3wp")}
	src.readings = []CounterReading{
		clrReading(".NET CLR Memory", "w3wp", "Process ID", 100),
		clrReading(".NET CLR Memory", "w3wp", "Gen 0 heap size", 1),
	}
	for _, targets := range []func() []ResolvedTarget{
		nil,
		func() []ResolvedTarget { return nil },
		func() []ResolvedTarget { return []ResolvedTarget{} },
	} {
		g := newCLRTestGroup(t, src, targets, nil)
		if v, err := g.Collect(time.Unix(1000, 0), 15); err != nil || v != nil {
			t.Fatalf("no targets: (%v, %v), want (nil, nil)", v, err)
		}
		if src.collects != 0 {
			t.Fatalf("collects = %d, want 0 with no selected targets", src.collects)
		}
	}
	// With a selection sampling resumes (proves the counter is real).
	g := newCLRTestGroup(t, src, func() []ResolvedTarget {
		return []ResolvedTarget{{Key: "pool:a", PID: 100, Start: 1, Runtime: 1}}
	}, nil)
	if grp := clrCollect(t, g, time.Unix(1000, 0)); grp == nil {
		t.Fatal("selected target: nil group")
	}
	if src.collects != 1 {
		t.Fatalf("collects = %d, want 1 once a target is selected", src.collects)
	}
}

func TestCLRRegistered(t *testing.T) {
	registryMu.Lock()
	reg, ok := registry["clr"]
	registryMu.Unlock()
	if !ok {
		t.Fatal("clr group not registered from init()")
	}
	if !reg.optional {
		t.Fatal("clr group must be optional (ships off when it breaks budget)")
	}
}