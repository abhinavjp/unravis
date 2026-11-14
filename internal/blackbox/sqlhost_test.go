package blackbox

import (
	"errors"
	"reflect"
	"runtime"
	"testing"
	"time"
)

// sqlHostFakeSource is a deterministic CounterSource: it returns the same fixed
// readings on every Collect. No PDH, no clock, no goroutine.
type sqlHostFakeSource struct {
	readings []CounterReading
	closed   bool
}

func (f *sqlHostFakeSource) Collect() ([]CounterReading, error) { return f.readings, nil }
func (f *sqlHostFakeSource) Close() error                       { f.closed = true; return nil }

// sqlHostReading builds one valid reading for a path.
func sqlHostReading(path string, v float64) CounterReading {
	return CounterReading{Path: path, Value: v, Status: 0, OK: true}
}

// newSQLHostTestGroup builds a group over an explicit prefix list and a fake
// source, exactly as the factory does after discovery but without PDH. It is the
// test seam: discovery and the source are both injected.
func newSQLHostTestGroup(t *testing.T, prefixes []string, src CounterSource) *sqlHostGroup {
	t.Helper()
	paths, specs, targets := buildSQLHostTargets(prefixes)
	_ = paths
	return &sqlHostGroup{
		src:     src,
		engine:  NewEngine(specs, 4),
		targets: targets,
		cores:   4,
	}
}

func TestSQLHostDefaultAndNamed(t *testing.T) {
	prefixes := []string{"SQLServer:", "MSSQL$PAYROLL:"}
	// One valid gauge per instance so both are emitted.
	src := &sqlHostFakeSource{readings: []CounterReading{
		sqlHostReading(sqlHostPath("SQLServer:", "General Statistics", "", "User Connections"), 11),
		sqlHostReading(sqlHostPath("MSSQL$PAYROLL:", "General Statistics", "", "User Connections"), 22),
	}}
	g := newSQLHostTestGroup(t, prefixes, src)
	v, err := g.Collect(time.Unix(1000, 0), 15)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	grp, ok := v.(*SQLHostGroup)
	if !ok || grp == nil {
		t.Fatalf("Collect returned %T, want *SQLHostGroup", v)
	}
	if len(grp.Instances) != 2 {
		t.Fatalf("got %d instances, want 2 (default + named)", len(grp.Instances))
	}
	byLabel := map[string]SQLHostInstance{}
	for _, inst := range grp.Instances {
		byLabel[inst.Instance] = inst
	}
	def, ok := byLabel["MSSQLSERVER"]
	if !ok {
		t.Fatalf("default instance label MSSQLSERVER absent; got %v", byLabel)
	}
	named, ok := byLabel["PAYROLL"]
	if !ok {
		t.Fatalf("named instance label PAYROLL absent; got %v", byLabel)
	}
	if def.UserConnections == nil || *def.UserConnections != 11 {
		t.Fatalf("default UserConnections = %v, want 11", def.UserConnections)
	}
	if named.UserConnections == nil || *named.UserConnections != 22 {
		t.Fatalf("named UserConnections = %v, want 22", named.UserConnections)
	}
}

func TestSQLHostFieldMapping(t *testing.T) {
	p := "SQLServer:"
	// One valid reading per field, all 13 counters, after enough baselines for
	// the rates. Gauges emit immediately; rates need two baselines first.
	readings := []CounterReading{
		sqlHostReading(sqlHostPath(p, "SQL Statistics", "", "Batch Requests/sec"), 100),
		sqlHostReading(sqlHostPath(p, "General Statistics", "", "User Connections"), 5),
		sqlHostReading(sqlHostPath(p, "Locks", "_Total", "Lock Waits/sec"), 3),
		sqlHostReading(sqlHostPath(p, "General Statistics", "", "Processes Blocked"), 2),
		sqlHostReading(sqlHostPath(p, "Memory Manager", "", "Memory Grants Pending"), 1),
		sqlHostReading(sqlHostPath(p, "Buffer Manager", "", "Page life expectancy"), 900),
		sqlHostReading(sqlHostPath(p, "Buffer Manager", "", "Free list stalls/sec"), 4),
		sqlHostReading(sqlHostPath(p, "SQL Statistics", "", "SQL Compilations/sec"), 20),
		sqlHostReading(sqlHostPath(p, "SQL Statistics", "", "SQL Re-Compilations/sec"), 7),
		sqlHostReading(sqlHostPath(p, "Locks", "_Total", "Number of Deadlocks/sec"), 0.5),
		sqlHostReading(sqlHostPath(p, "Locks", "_Total", "Lock Wait Time (ms)"), 12),
		sqlHostReading(sqlHostPath(p, "Databases", "_Total", "Log Flush Wait Time"), 8),
		sqlHostReading(sqlHostPath(p, "Access Methods", "", "Forwarded Records/sec"), 6),
	}
	g := newSQLHostTestGroup(t, []string{p}, &sqlHostFakeSource{readings: readings})
	now := time.Unix(1000, 0)
	// Two baseline ticks (rates gated), then a third that emits everything.
	for i := 0; i < 2; i++ {
		if _, err := g.Collect(now, 15); err != nil {
			t.Fatalf("baseline Collect: %v", err)
		}
		now = now.Add(15 * time.Second)
	}
	v, err := g.Collect(now, 15)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	grp := v.(*SQLHostGroup)
	if len(grp.Instances) != 1 {
		t.Fatalf("got %d instances, want 1", len(grp.Instances))
	}
	inst := grp.Instances[0]
	want := map[string]*float64{
		"BatchRequestsPerSec":    inst.BatchRequestsPerSec,
		"UserConnections":        inst.UserConnections,
		"LockWaitsPerSec":        inst.LockWaitsPerSec,
		"ProcessesBlocked":       inst.ProcessesBlocked,
		"MemoryGrantsPending":    inst.MemoryGrantsPending,
		"PageLifeExpectancy":     inst.PageLifeExpectancy,
		"FreeListStallsPerSec":   inst.FreeListStallsPerSec,
		"CompilationsPerSec":     inst.CompilationsPerSec,
		"ReCompilationsPerSec":   inst.ReCompilationsPerSec,
		"DeadlocksPerSec":        inst.DeadlocksPerSec,
		"LockWaitTimeMs":         inst.LockWaitTimeMs,
		"LogFlushWaitTimeMs":     inst.LogFlushWaitTimeMs,
		"ForwardedRecordsPerSec": inst.ForwardedRecordsPerSec,
	}
	wantVal := map[string]float64{
		"BatchRequestsPerSec": 100, "UserConnections": 5, "LockWaitsPerSec": 3,
		"ProcessesBlocked": 2, "MemoryGrantsPending": 1, "PageLifeExpectancy": 900,
		"FreeListStallsPerSec": 4, "CompilationsPerSec": 20, "ReCompilationsPerSec": 7,
		"DeadlocksPerSec": 0.5, "LockWaitTimeMs": 12, "LogFlushWaitTimeMs": 8,
		"ForwardedRecordsPerSec": 6,
	}
	for field, ptr := range want {
		if ptr == nil {
			t.Errorf("field %s is nil, want %v", field, wantVal[field])
			continue
		}
		if *ptr != wantVal[field] {
			t.Errorf("field %s = %v, want %v", field, *ptr, wantVal[field])
		}
	}
}

func TestSQLHostRateGated(t *testing.T) {
	p := "SQLServer:"
	path := sqlHostPath(p, "SQL Statistics", "", "Batch Requests/sec")
	g := newSQLHostTestGroup(t, []string{p}, &sqlHostFakeSource{readings: []CounterReading{
		sqlHostReading(path, 100),
	}})
	now := time.Unix(1000, 0)
	// First two ticks are baselines: the /sec rate must be absent (never 0).
	for i := 0; i < 2; i++ {
		v, err := g.Collect(now, 15)
		if err != nil {
			t.Fatalf("Collect: %v", err)
		}
		if grp, ok := v.(*SQLHostGroup); ok && grp != nil {
			for _, inst := range grp.Instances {
				if inst.BatchRequestsPerSec != nil {
					t.Fatalf("tick %d: rate emitted before two baselines: %v", i, *inst.BatchRequestsPerSec)
				}
			}
		}
		now = now.Add(15 * time.Second)
	}
	// Third tick: the rate is present.
	v, err := g.Collect(now, 15)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	grp := v.(*SQLHostGroup)
	if len(grp.Instances) != 1 || grp.Instances[0].BatchRequestsPerSec == nil {
		t.Fatalf("rate absent after two baselines: %+v", grp)
	}
	if *grp.Instances[0].BatchRequestsPerSec != 100 {
		t.Fatalf("rate = %v, want 100", *grp.Instances[0].BatchRequestsPerSec)
	}
}

func TestSQLHostMissingOmitted(t *testing.T) {
	p := "SQLServer:"
	// A valid gauge plus an INVALID rate (OK=false): the rate field must be nil,
	// never 0, and the instance is still present because the gauge is valid.
	src := &sqlHostFakeSource{readings: []CounterReading{
		sqlHostReading(sqlHostPath(p, "General Statistics", "", "User Connections"), 9),
		{Path: sqlHostPath(p, "SQL Statistics", "", "Batch Requests/sec"), Value: 0, Status: 1, OK: false},
	}}
	g := newSQLHostTestGroup(t, []string{p}, src)
	now := time.Unix(1000, 0)
	for i := 0; i < 3; i++ {
		v, err := g.Collect(now, 15)
		if err != nil {
			t.Fatalf("Collect: %v", err)
		}
		grp := v.(*SQLHostGroup)
		if len(grp.Instances) != 1 {
			t.Fatalf("got %d instances, want 1", len(grp.Instances))
		}
		inst := grp.Instances[0]
		if inst.BatchRequestsPerSec != nil {
			t.Fatalf("invalid counter emitted %v; missing must never be 0", *inst.BatchRequestsPerSec)
		}
		if inst.UserConnections == nil || *inst.UserConnections != 9 {
			t.Fatalf("valid gauge UserConnections = %v, want 9", inst.UserConnections)
		}
		now = now.Add(15 * time.Second)
	}

	// An instance whose every counter is invalid must be omitted entirely.
	bad := &sqlHostFakeSource{readings: []CounterReading{
		{Path: sqlHostPath(p, "General Statistics", "", "User Connections"), Value: 0, Status: 1, OK: false},
	}}
	g2 := newSQLHostTestGroup(t, []string{p}, bad)
	v, err := g2.Collect(time.Unix(2000, 0), 15)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if v != nil {
		t.Fatalf("all-invalid instance must yield nil group, got %+v", v)
	}
}

func TestSQLHostNoDiscoveryNil(t *testing.T) {
	// No SQL instances discovered: the group is nil, not an error.
	g := newSQLHostTestGroup(t, nil, &sqlHostFakeSource{})
	v, err := g.Collect(time.Unix(1000, 0), 15)
	if err != nil {
		t.Fatalf("Collect with no instances must not error: %v", err)
	}
	if v != nil {
		t.Fatalf("no instances must yield nil group, got %+v", v)
	}
}

// TestSQLHostDiscoveryFakeable proves discoverSQLInstances is a replaceable seam
// and that the label mapping (default -> MSSQLSERVER, named -> name) is correct.
func TestSQLHostDiscoveryFakeable(t *testing.T) {
	saved := discoverSQLInstances
	discoverSQLInstances = func() ([]string, error) {
		return []string{"SQLServer:", "MSSQL$PAYROLL:"}, nil
	}
	t.Cleanup(func() { discoverSQLInstances = saved })

	got, err := discoverSQLInstances()
	if err != nil {
		t.Fatalf("fake discovery: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"SQLServer:", "MSSQL$PAYROLL:"}) {
		t.Fatalf("discovery = %v", got)
	}
	if label := sqlInstanceLabel("SQLServer:"); label != "MSSQLSERVER" {
		t.Fatalf("default label = %q, want MSSQLSERVER", label)
	}
	if label := sqlInstanceLabel("MSSQL$PAYROLL:"); label != "PAYROLL" {
		t.Fatalf("named label = %q, want PAYROLL", label)
	}
}

// TestSQLHostDiscoveryPlatformDefault proves the discovery wiring picks the
// platform default (real PdhEnumObjectsW on Windows, no-PDH error off Windows)
// — never a no-op — and that fakes still override both seams end to end.
func TestSQLHostDiscoveryPlatformDefault(t *testing.T) {
	// The object-enumeration seam must default to the platform function.
	if got, want := reflect.ValueOf(sqlEnumPDHObjects).Pointer(), reflect.ValueOf(sqlEnumPDHObjectsPlatform).Pointer(); got != want {
		t.Fatalf("sqlEnumPDHObjects is not wired to the platform enumerator")
	}
	// Discovery must default to the PDH filtering wrapper over that seam.
	if got, want := reflect.ValueOf(discoverSQLInstances).Pointer(), reflect.ValueOf(enumSQLInstancesPDH).Pointer(); got != want {
		t.Fatalf("discoverSQLInstances default is not enumSQLInstancesPDH")
	}
	// Off Windows the platform default reports no PDH (the group is skipped).
	if runtime.GOOS != "windows" {
		if _, err := sqlEnumPDHObjectsPlatform(); !errors.Is(err, errSQLHostNoPDH) {
			t.Fatalf("non-Windows platform default err = %v, want errSQLHostNoPDH", err)
		}
	}

	// A fake object list still flows end to end through the real default
	// discovery: SQL prefixes are kept, deduped and sorted; the rest dropped.
	savedEnum := sqlEnumPDHObjects
	savedDiscover := discoverSQLInstances
	t.Cleanup(func() {
		sqlEnumPDHObjects = savedEnum
		discoverSQLInstances = savedDiscover
	})
	sqlEnumPDHObjects = func() ([]string, error) {
		return []string{
			"Processor",
			"SQLServer:SQL Statistics",
			"MSSQL$PAYROLL:General Statistics",
			"SQLServer:General Statistics",
			"Memory",
		}, nil
	}
	got, err := discoverSQLInstances()
	if err != nil {
		t.Fatalf("fake object enumeration: %v", err)
	}
	if want := []string{"MSSQL$PAYROLL:", "SQLServer:"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("discovery = %v, want %v", got, want)
	}

	// The whole discovery seam stays replaceable too.
	discoverSQLInstances = func() ([]string, error) { return []string{"SQLServer:"}, nil }
	got, err = discoverSQLInstances()
	if err != nil || !reflect.DeepEqual(got, []string{"SQLServer:"}) {
		t.Fatalf("fake discovery override = %v, %v", got, err)
	}
}