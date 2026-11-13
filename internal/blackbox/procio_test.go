package blackbox

import (
	"strings"
	"testing"
	"time"
)

// procIOFakeSource is a fake CounterSource with expansion bookkeeping: Paths
// feeds the spec build, RefreshInstances records re-expansion calls.
type procIOFakeSource struct {
	paths     []string
	readings  []CounterReading
	refreshes int
	closed    bool
}

func (f *procIOFakeSource) Collect() ([]CounterReading, error) { return f.readings, nil }
func (f *procIOFakeSource) Close() error                       { f.closed = true; return nil }
func (f *procIOFakeSource) Paths() []string                    { return f.paths }
func (f *procIOFakeSource) RefreshInstances() error            { f.refreshes++; return nil }

func procIOPath(inst, counter string) string {
	return `\Process(` + inst + `)\` + counter
}

func procIOReading(inst, counter string, v float64) CounterReading {
	return CounterReading{Path: procIOPath(inst, counter), Value: v, OK: true}
}

// procIOInstance adds all six counter paths for one Process instance.
func procIOInstance(src *procIOFakeSource, inst string) {
	for _, c := range []string{"ID Process", "IO Read Bytes/sec", "IO Write Bytes/sec", "Page File Bytes", "Virtual Bytes", "Elapsed Time"} {
		src.paths = append(src.paths, procIOPath(inst, c))
	}
}

func newProcIOTestGroup(t *testing.T, src *procIOFakeSource, targets func() []ResolvedTarget, changed func() bool) *procIOGroup {
	t.Helper()
	g, err := newProcIOGroup(GroupDeps{Source: src, Cores: 4, Targets: targets, ProcessSetChanged: changed})
	if err != nil {
		t.Fatalf("newProcIOGroup: %v", err)
	}
	return g.(*procIOGroup)
}

// procIOAging advances every Elapsed Time reading: real elapsed grows with
// the wall clock, so creation time stays confirmed as the tick clock moves.
func procIOAging(src *procIOFakeSource, secs float64) {
	for i := range src.readings {
		if strings.HasSuffix(src.readings[i].Path, `\Elapsed Time`) {
			src.readings[i].Value += secs
		}
	}
}

// collectProcIO ticks the group n times (15 s apart) and returns the last
// *ProcIOGroup, or nil when every tick omitted the group.
func collectProcIO(t *testing.T, g *procIOGroup, src *procIOFakeSource, n int) *ProcIOGroup {
	t.Helper()
	now := time.Unix(1000, 0)
	var grp *ProcIOGroup
	for i := 0; i < n; i++ {
		if i > 0 {
			procIOAging(src, 15)
		}
		v, err := g.Collect(now.Add(time.Duration(i)*15*time.Second), 15)
		if err != nil {
			t.Fatalf("tick %d: %v", i+1, err)
		}
		if v == nil {
			continue
		}
		var ok bool
		grp, ok = v.(*ProcIOGroup)
		if !ok {
			t.Fatalf("tick %d: Collect returned %T, want *ProcIOGroup", i+1, v)
		}
	}
	return grp
}

// TestProcIOPIDMap guards the instance->PID mapping: #N instance names never
// resemble the PIDs, so the right target row must come from ID Process.
func TestProcIOPIDMap(t *testing.T) {
	src := &procIOFakeSource{}
	procIOInstance(src, "w3wp#1") // PID 100 -> pool:a
	procIOInstance(src, "w3wp")   // PID 200 -> svc:b
	src.readings = []CounterReading{
		procIOReading("w3wp#1", "ID Process", 100),
		procIOReading("w3wp", "ID Process", 200),
		procIOReading("w3wp#1", "Elapsed Time", 100),
		procIOReading("w3wp", "Elapsed Time", 100),
		procIOReading("w3wp#1", "Page File Bytes", 1000),
		procIOReading("w3wp", "Page File Bytes", 2000),
	}
	g := newProcIOTestGroup(t, src, func() []ResolvedTarget {
		return []ResolvedTarget{
			{Key: "pool:a", PID: 100, Start: 900, Runtime: 1},
			{Key: "svc:b", PID: 200, Start: 900, Runtime: 2},
		}
	}, nil)
	grp := collectProcIO(t, g, src, 1)
	if grp == nil || len(grp.Targets) != 2 {
		t.Fatalf("group = %+v, want two rows", grp)
	}
	a, b := grp.Targets[0], grp.Targets[1]
	if a.Target != "pool:a" || b.Target != "svc:b" {
		t.Fatalf("targets = %q,%q; want pool:a,svc:b (sorted)", a.Target, b.Target)
	}
	if a.PageFileBytes == nil || *a.PageFileBytes != 1000 {
		t.Fatalf("pool:a PageFileBytes = %v, want 1000 (PID 100 lives in w3wp#1)", a.PageFileBytes)
	}
	if b.PageFileBytes == nil || *b.PageFileBytes != 2000 {
		t.Fatalf("svc:b PageFileBytes = %v, want 2000 (PID 200 lives in w3wp)", b.PageFileBytes)
	}
}

// TestProcIOSelectedOnly guards owner-selected targets only: an unselected
// process with live counters never appears; nothing selected yields nil.
func TestProcIOSelectedOnly(t *testing.T) {
	src := &procIOFakeSource{}
	procIOInstance(src, "w3wp")  // selected
	procIOInstance(src, "other") // unselected: must never be emitted
	src.readings = []CounterReading{
		procIOReading("w3wp", "ID Process", 100),
		procIOReading("other", "ID Process", 300),
		procIOReading("w3wp", "Elapsed Time", 100),
		procIOReading("other", "Elapsed Time", 100),
		procIOReading("w3wp", "Page File Bytes", 1000),
		procIOReading("other", "Page File Bytes", 3000),
	}
	g := newProcIOTestGroup(t, src, func() []ResolvedTarget {
		return []ResolvedTarget{{Key: "pool:a", PID: 100, Start: 900}}
	}, nil)
	grp := collectProcIO(t, g, src, 1)
	if grp == nil || len(grp.Targets) != 1 || grp.Targets[0].Target != "pool:a" {
		t.Fatalf("group = %+v, want only the selected pool:a", grp)
	}
	// No targets selected: no rows at all, never machine-wide.
	gNone := newProcIOTestGroup(t, src, nil, nil)
	if v, err := gNone.Collect(time.Unix(1000, 0), 0); err != nil || v != nil {
		t.Fatalf("no targets: (%v, %v), want (nil, nil)", v, err)
	}
}

// TestProcIOMissingOmitted guards missing-never-0: an invalid counter stays
// nil, a selected target with no instance is omitted, all-invalid yields nil.
func TestProcIOMissingOmitted(t *testing.T) {
	src := &procIOFakeSource{}
	procIOInstance(src, "w3wp")
	src.readings = []CounterReading{
		procIOReading("w3wp", "ID Process", 100),
		procIOReading("w3wp", "Elapsed Time", 100),
		{Path: procIOPath("w3wp", "Page File Bytes"), Value: 99, Status: 1, OK: false},
		procIOReading("w3wp", "Virtual Bytes", 55),
	}
	g := newProcIOTestGroup(t, src, func() []ResolvedTarget {
		return []ResolvedTarget{
			{Key: "pool:a", PID: 100, Start: 900},
			{Key: "svc:ghost", PID: 999, Start: 900}, // no instance: omitted
		}
	}, nil)
	grp := collectProcIO(t, g, src, 1)
	if grp == nil || len(grp.Targets) != 1 {
		t.Fatalf("group = %+v, want one row (ghost omitted)", grp)
	}
	row := grp.Targets[0]
	if row.Target != "pool:a" {
		t.Fatalf("target = %q, want pool:a", row.Target)
	}
	if row.PageFileBytes != nil {
		t.Fatalf("invalid counter emitted %v; missing must stay nil, never 0", *row.PageFileBytes)
	}
	if row.VirtualBytes == nil || *row.VirtualBytes != 55 {
		t.Fatalf("VirtualBytes = %v, want 55", row.VirtualBytes)
	}
	// Every reading invalid: the whole group is nil, never zero-filled.
	for i := range src.readings {
		src.readings[i].OK, src.readings[i].Status = false, 1
	}
	if v, err := g.Collect(time.Unix(1015, 0), 15); err != nil || v != nil {
		t.Fatalf("all invalid: (%v, %v), want (nil, nil)", v, err)
	}
}

// TestProcIORateGated guards the two-baseline gate on IO bytes/sec: the rates
// stay absent for the first two valid samples while gauges emit immediately.
func TestProcIORateGated(t *testing.T) {
	src := &procIOFakeSource{}
	procIOInstance(src, "w3wp")
	src.readings = []CounterReading{
		procIOReading("w3wp", "ID Process", 100),
		procIOReading("w3wp", "Elapsed Time", 100),
		procIOReading("w3wp", "IO Read Bytes/sec", 10),
		procIOReading("w3wp", "IO Write Bytes/sec", 20),
		procIOReading("w3wp", "Page File Bytes", 1000),
	}
	g := newProcIOTestGroup(t, src, func() []ResolvedTarget {
		return []ResolvedTarget{{Key: "pool:a", PID: 100, Start: 900}}
	}, nil)
	now := time.Unix(1000, 0)
	for i := 0; i < 2; i++ {
		if i > 0 {
			procIOAging(src, 15)
		}
		v, err := g.Collect(now.Add(time.Duration(i)*15*time.Second), 15)
		if err != nil {
			t.Fatalf("tick %d: %v", i+1, err)
		}
		row := v.(*ProcIOGroup).Targets[0]
		if row.IOReadBytesPerSec != nil || row.IOWriteBytesPerSec != nil {
			t.Fatalf("tick %d: IO rate emitted before two baselines", i+1)
		}
		if row.PageFileBytes == nil {
			t.Fatalf("tick %d: gauge absent", i+1)
		}
	}
	procIOAging(src, 15)
	v, err := g.Collect(now.Add(30*time.Second), 15)
	if err != nil {
		t.Fatalf("tick 3: %v", err)
	}
	row := v.(*ProcIOGroup).Targets[0]
	if row.IOReadBytesPerSec == nil || *row.IOReadBytesPerSec != 10 {
		t.Fatalf("IOReadBytesPerSec = %v, want 10 after two baselines", row.IOReadBytesPerSec)
	}
	if row.IOWriteBytesPerSec == nil || *row.IOWriteBytesPerSec != 20 {
		t.Fatalf("IOWriteBytesPerSec = %v, want 20 after two baselines", row.IOWriteBytesPerSec)
	}
}

// TestProcIOFields guards the field mapping: pagefile/virtual bytes are uint64
// gauges, elapsed is seconds, IO rates land in their own fields.
func TestProcIOFields(t *testing.T) {
	src := &procIOFakeSource{}
	procIOInstance(src, "w3wp")
	src.readings = []CounterReading{
		procIOReading("w3wp", "ID Process", 100),
		procIOReading("w3wp", "Elapsed Time", 100),
		procIOReading("w3wp", "IO Read Bytes/sec", 10.5),
		procIOReading("w3wp", "IO Write Bytes/sec", 20.25),
		procIOReading("w3wp", "Page File Bytes", 4096),
		procIOReading("w3wp", "Virtual Bytes", 8192),
	}
	g := newProcIOTestGroup(t, src, func() []ResolvedTarget {
		return []ResolvedTarget{{Key: "pool:a", PID: 100, Start: 900}}
	}, nil)
	grp := collectProcIO(t, g, src, 3) // two baselines, then the rates emit
	if grp == nil || len(grp.Targets) != 1 {
		t.Fatalf("group = %+v, want one row", grp)
	}
	row := grp.Targets[0]
	if row.IOReadBytesPerSec == nil || *row.IOReadBytesPerSec != 10.5 {
		t.Fatalf("IOReadBytesPerSec = %v, want 10.5", row.IOReadBytesPerSec)
	}
	if row.IOWriteBytesPerSec == nil || *row.IOWriteBytesPerSec != 20.25 {
		t.Fatalf("IOWriteBytesPerSec = %v, want 20.25", row.IOWriteBytesPerSec)
	}
	if row.PageFileBytes == nil || *row.PageFileBytes != 4096 {
		t.Fatalf("PageFileBytes = %v, want 4096", row.PageFileBytes)
	}
	if row.VirtualBytes == nil || *row.VirtualBytes != 8192 {
		t.Fatalf("VirtualBytes = %v, want 8192", row.VirtualBytes)
	}
	if row.ElapsedSec == nil || *row.ElapsedSec != 130 {
		t.Fatalf("ElapsedSec = %v, want 130 (100 aged by two 15 s ticks)", row.ElapsedSec)
	}
}

// TestProcIOStartMismatch guards the (PID, creation) identity: a PID match
// whose Elapsed Time implies a different creation time is omitted.
func TestProcIOStartMismatch(t *testing.T) {
	src := &procIOFakeSource{}
	procIOInstance(src, "w3wp")
	src.readings = []CounterReading{
		procIOReading("w3wp", "ID Process", 100),
		procIOReading("w3wp", "Elapsed Time", 500), // created at 500, not 900
		procIOReading("w3wp", "Page File Bytes", 1000),
	}
	g := newProcIOTestGroup(t, src, func() []ResolvedTarget {
		return []ResolvedTarget{{Key: "pool:a", PID: 100, Start: 900}}
	}, nil)
	if grp := collectProcIO(t, g, src, 1); grp != nil {
		t.Fatalf("group = %+v, want nil when creation time does not confirm Start", grp)
	}
}

// TestProcIORefreshOnChange guards the footprint: re-expand only on a
// process-set change, never per tick, and pick up new instances.
func TestProcIORefreshOnChange(t *testing.T) {
	src := &procIOFakeSource{}
	procIOInstance(src, "w3wp")
	src.readings = []CounterReading{
		procIOReading("w3wp", "ID Process", 100),
		procIOReading("w3wp", "Elapsed Time", 100),
		procIOReading("w3wp", "Page File Bytes", 1000),
	}
	targets := []ResolvedTarget{{Key: "pool:a", PID: 100, Start: 900}}
	changed := false
	g := newProcIOTestGroup(t, src, func() []ResolvedTarget { return targets }, func() bool { return changed })
	now := time.Unix(1000, 0)
	if _, err := g.Collect(now, 0); err != nil {
		t.Fatal(err)
	}
	if src.refreshes != 0 {
		t.Fatalf("refreshes = %d, want 0 without a process-set change", src.refreshes)
	}
	// The process set changes: a new instance appears; re-expand once.
	changed = true
	targets = append(targets, ResolvedTarget{Key: "svc:b", PID: 200, Start: 900})
	procIOInstance(src, "w3wp#1")
	src.readings = append(src.readings,
		procIOReading("w3wp#1", "ID Process", 200),
		procIOReading("w3wp#1", "Elapsed Time", 100),
		procIOReading("w3wp#1", "Page File Bytes", 2000),
	)
	procIOAging(src, 15)
	v, err := g.Collect(now.Add(15*time.Second), 15)
	if err != nil {
		t.Fatal(err)
	}
	if src.refreshes != 1 {
		t.Fatalf("refreshes = %d, want 1 on change", src.refreshes)
	}
	if grp := v.(*ProcIOGroup); len(grp.Targets) != 2 {
		t.Fatalf("targets = %d, want 2 after re-expansion", len(grp.Targets))
	}
	changed = false
	procIOAging(src, 15)
	if _, err := g.Collect(now.Add(30*time.Second), 15); err != nil {
		t.Fatal(err)
	}
	if src.refreshes != 1 {
		t.Fatalf("refreshes = %d, want no re-expand without a change", src.refreshes)
	}
}

func TestProcIORegistered(t *testing.T) {
	registryMu.Lock()
	reg, ok := registry["procIO"]
	registryMu.Unlock()
	if !ok {
		t.Fatal("procIO group not registered from init()")
	}
	if !reg.optional {
		t.Fatal("procIO group must be optional (ships off when it breaks budget)")
	}
}