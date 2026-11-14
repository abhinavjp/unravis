package blackbox

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// This file covers the wave-4 binding fixes (W4-01, W4-02, W4-06). All tests use
// the existing injected snapshot/module/enumerator/source seams — no Windows, no
// live process/service/counter operations.
//
//   W4-01: a cached pool/service mapping is bound to the confirmed process
//          identity (PID + StartTicks) and discovery generation; a failed
//          enumeration invalidates it (fail-closed) so a reused PID never
//          inherits the selected name; retry is a bounded backoff independent
//          of unrelated churn; absence has a fixed availability code.
//   W4-02: ambiguous workers/children are never resolved first-wins; an orphan
//          child older than the (reused) parent is rejected; resolution is
//          stable under reversed enumeration order.
//   W4-06: one bad counter among healthy default/named SQL instances keeps the
//          healthy ones readable and recovers without a recorder restart;
//          absence has a fixed diagnostic (no raw provider text).

// ticks is a helper: lossless FILETIME identity from unix seconds.
func ticks(sec int64) int64 { return sec * filetimeTicksPerSecond }

// --- W4-01: identity/generation binding, fail-closed invalidation ---

// A last-good service mapping followed by PID reuse AND a failed refresh must
// emit NO target for the unverified mapping (the W4-01 exploit). The fixed
// availability code is "unavailable", never a rebound to the new process.
func TestBindingServicePIDReuseRefreshFailure(t *testing.T) {
	now := time.Unix(1000, 0)
	procs := []ProcInfo{{PID: 100, Start: 100, StartTicks: ticks(100), Name: "mail.exe"}}
	svcFail := false
	r := newMapTestResolver(func() []ProcInfo { return procs }, map[uint32][]string{100: {"clr.dll"}})
	r.nowFn = func() time.Time { return now }
	r.instancesFn = func() ([]string, error) { return nil, nil }
	r.servicesFn = func() ([]svcProc, error) {
		if svcFail {
			return nil, errors.New("scm denied")
		}
		return []svcProc{{name: "Mail", pid: 100}}, nil
	}
	targets := []Target{{Kind: "service", Name: "Mail"}}

	// Tick 1: the mapping is verified and resolves.
	got, missing := r.ResolveTargets(targets)
	if len(got) != 1 || len(missing) != 0 || got[0].PID != 100 || got[0].Start != 100 {
		t.Fatalf("tick 1: got %+v / %v, want the verified svc:Mail target", got, missing)
	}

	// Mail exits; an unrelated process reuses PID 100 with a new creation, and
	// the service enumeration now fails.
	procs[0].Start = 200
	procs[0].StartTicks = ticks(200)
	procs[0].Name = "unrelated.exe"
	svcFail = true
	now = now.Add(15 * time.Second)

	got, missing = r.ResolveTargets(targets)
	if len(got) != 0 {
		t.Fatalf("unverified mapping must emit NO target after reuse+failed refresh, got %+v", got)
	}
	if len(missing) != 1 || missing[0] != "Mail" {
		t.Fatalf("missing = %v, want [Mail]", missing)
	}
	if st := r.TargetStatus()["Mail"]; st != targetUnavailable {
		t.Fatalf("status = %q, want fixed code %q", st, targetUnavailable)
	}
}

// The same fail-closed contract for a pool mapping (W4-01).
func TestBindingPoolPIDReuseRefreshFailure(t *testing.T) {
	now := time.Unix(1000, 0)
	procs := []ProcInfo{{PID: 4321, ParentPID: 4, Start: 100, StartTicks: ticks(100), Name: "w3wp.exe"}}
	instFail := false
	r := newMapTestResolver(func() []ProcInfo { return procs }, map[uint32][]string{4321: {"clr.dll"}})
	r.nowFn = func() time.Time { return now }
	r.instancesFn = func() ([]string, error) {
		if instFail {
			return nil, errors.New("pdh denied")
		}
		return []string{"4321_Payroll"}, nil
	}
	r.servicesFn = func() ([]svcProc, error) { return nil, nil }
	targets := []Target{{Kind: "appPool", Name: "Payroll"}}

	got, missing := r.ResolveTargets(targets)
	if len(got) != 1 || len(missing) != 0 || got[0].PID != 4321 {
		t.Fatalf("tick 1: got %+v / %v, want the verified pool target", got, missing)
	}

	procs[0].Start = 200
	procs[0].StartTicks = ticks(200)
	instFail = true
	now = now.Add(15 * time.Second)

	got, missing = r.ResolveTargets(targets)
	if len(got) != 0 {
		t.Fatalf("unverified pool mapping must emit NO target after reuse+failed refresh, got %+v", got)
	}
	if len(missing) != 1 || missing[0] != "Payroll" {
		t.Fatalf("missing = %v, want [Payroll]", missing)
	}
	if st := r.TargetStatus()["Payroll"]; st != targetUnavailable {
		t.Fatalf("status = %q, want fixed code %q", st, targetUnavailable)
	}
}

// A failed enumeration on a STABLE process set is retried on a bounded backoff
// independent of unrelated churn, and recovers without any process-set change
// (W4-01). Before the backoff is due there is no per-tick retry storm.
func TestBindingRefreshFailureRecoversOnBackoff(t *testing.T) {
	now := time.Unix(1000, 0)
	procs := []ProcInfo{{PID: 100, Start: 100, StartTicks: ticks(100), Name: "svc.exe"}}
	fail := true
	var calls int
	r := newMapTestResolver(func() []ProcInfo { return procs }, map[uint32][]string{100: {"clr.dll"}})
	r.nowFn = func() time.Time { return now }
	r.instancesFn = func() ([]string, error) { return nil, nil }
	r.servicesFn = func() ([]svcProc, error) {
		calls++
		if fail {
			return nil, errors.New("scm denied")
		}
		return []svcProc{{name: "Svc", pid: 100}}, nil
	}
	targets := []Target{{Kind: "service", Name: "Svc"}}

	// Tick 1: enumeration fails, no target.
	if got, _ := r.ResolveTargets(targets); len(got) != 0 {
		t.Fatalf("tick 1: got %+v, want no target on failed enumeration", got)
	}
	// Stable set, backoff not due: no retry (bounded, not per tick).
	now = now.Add(time.Second)
	if got, _ := r.ResolveTargets(targets); len(got) != 0 {
		t.Fatalf("before backoff: got %+v, want no target", got)
	}
	if calls != 1 {
		t.Fatalf("enumerated %d times before backoff due, want 1 (no retry storm)", calls)
	}
	// Recover and advance past the backoff: the retry succeeds with NO process-set
	// change (independent of unrelated churn).
	fail = false
	now = now.Add(20 * time.Second)
	got, missing := r.ResolveTargets(targets)
	if len(got) != 1 || len(missing) != 0 || got[0].PID != 100 {
		t.Fatalf("recovery: got %+v / %v, want the verified target", got, missing)
	}
}

// --- W4-02: ambiguity never first-wins; orphan child rejected; order-stable ---

// Two live workers of the selected pool (web garden / overlap) are ambiguous:
// no target is emitted, in EITHER enumeration order (never silently first-wins).
func TestBindingAmbiguousWorkersNeverFirstWins(t *testing.T) {
	for _, order := range [][]string{{"100_Pool", "200_Pool"}, {"200_Pool", "100_Pool"}} {
		procs := []ProcInfo{
			{PID: 100, ParentPID: 4, Start: 100, StartTicks: ticks(100), Name: "w3wp.exe"},
			{PID: 200, ParentPID: 4, Start: 110, StartTicks: ticks(110), Name: "w3wp.exe"},
		}
		inst := order
		r := newMapTestResolver(func() []ProcInfo { return procs },
			map[uint32][]string{100: {"clr.dll"}, 200: {"clr.dll"}})
		r.instancesFn = func() ([]string, error) { return inst, nil }
		r.servicesFn = func() ([]svcProc, error) { return nil, nil }
		got, missing := r.ResolveTargets([]Target{{Kind: "appPool", Name: "Pool"}})
		if len(got) != 0 {
			t.Fatalf("order %v: ambiguous workers must not silently pick, got %+v", order, got)
		}
		if len(missing) != 1 || missing[0] != "Pool" {
			t.Fatalf("order %v: missing = %v, want [Pool]", order, missing)
		}
		if st := r.TargetStatus()["Pool"]; st != targetAmbiguous {
			t.Fatalf("order %v: status = %q, want %q", order, st, targetAmbiguous)
		}
	}
}

// A runtime-0 worker with two legitimate runtime-2 children is ambiguous: no
// target is silently picked (W4-02).
func TestBindingAmbiguousChildrenNeverFirstWins(t *testing.T) {
	procs := []ProcInfo{
		{PID: 100, ParentPID: 4, Start: 100, StartTicks: ticks(100), Name: "w3wp.exe"},
		{PID: 200, ParentPID: 100, Start: 110, StartTicks: ticks(110), Name: "dotnet.exe"},
		{PID: 300, ParentPID: 100, Start: 120, StartTicks: ticks(120), Name: "dotnet.exe"},
	}
	r := newMapTestResolver(func() []ProcInfo { return procs },
		map[uint32][]string{200: {"coreclr.dll"}, 300: {"coreclr.dll"}})
	r.instancesFn = func() ([]string, error) { return []string{"100_Pool"}, nil }
	r.servicesFn = func() ([]svcProc, error) { return nil, nil }
	got, _ := r.ResolveTargets([]Target{{Kind: "appPool", Name: "Pool"}})
	if len(got) != 0 {
		t.Fatalf("two legitimate children must be ambiguous, got %+v", got)
	}
	if st := r.TargetStatus()["Pool"]; st != targetAmbiguous {
		t.Fatalf("status = %q, want %q", st, targetAmbiguous)
	}
}

// An orphan child whose ParentPID was reused: the child was created BEFORE the
// current parent, so it is rejected and the worker itself is targeted (W4-02).
func TestBindingOrphanChildOlderThanParentRejected(t *testing.T) {
	procs := []ProcInfo{
		{PID: 100, ParentPID: 4, Start: 200, StartTicks: ticks(200), Name: "w3wp.exe"},
		{PID: 200, ParentPID: 100, Start: 100, StartTicks: ticks(100), Name: "dotnet.exe"},
	}
	r := newMapTestResolver(func() []ProcInfo { return procs },
		map[uint32][]string{200: {"coreclr.dll"}})
	r.instancesFn = func() ([]string, error) { return []string{"100_Pool"}, nil }
	r.servicesFn = func() ([]svcProc, error) { return nil, nil }
	got, missing := r.ResolveTargets([]Target{{Kind: "appPool", Name: "Pool"}})
	if len(got) != 1 || len(missing) != 0 {
		t.Fatalf("got %+v / %v, want the worker itself (orphan child rejected)", got, missing)
	}
	if got[0].PID != 100 {
		t.Fatalf("orphan child (created before the reused parent) must be rejected, got PID %d", got[0].PID)
	}
}

// A single live worker among a stale instance resolves identically under
// reversed enumeration order (W4-02 order stability).
func TestBindingSingleWorkerOrderStable(t *testing.T) {
	for _, order := range [][]string{{"100_Pool", "999_Pool"}, {"999_Pool", "100_Pool"}} {
		procs := []ProcInfo{{PID: 100, ParentPID: 4, Start: 100, StartTicks: ticks(100), Name: "w3wp.exe"}}
		inst := order
		r := newMapTestResolver(func() []ProcInfo { return procs }, map[uint32][]string{100: {"clr.dll"}})
		r.instancesFn = func() ([]string, error) { return inst, nil }
		r.servicesFn = func() ([]svcProc, error) { return nil, nil }
		got, missing := r.ResolveTargets([]Target{{Kind: "appPool", Name: "Pool"}})
		if len(got) != 1 || len(missing) != 0 || got[0].PID != 100 || got[0].Start != 100 {
			t.Fatalf("order %v: got %+v / %v, want the live worker 100 regardless of order", order, got, missing)
		}
	}
}

// --- W4-06: SQL host partial availability + bounded recovery ---

// sqlHostSeams installs a fake discovery and a fake source factory for the test
// and restores them on cleanup. The factory fails whenever any requested path
// matches failPath (empty failPath = never fail); otherwise it returns a source
// serving a fixed gauge/rate value for every requested path.
func sqlHostSeams(t *testing.T, prefixes []string, failPath string, fail *bool) {
	t.Helper()
	savedDiscover, savedSource := discoverSQLInstances, sqlHostNewSource
	discoverSQLInstances = func() ([]string, error) { return prefixes, nil }
	sqlHostNewSource = func(paths []string) (CounterSource, error) {
		for _, p := range paths {
			if failPath != "" && p == failPath && (fail == nil || *fail) {
				return nil, errors.New("raw provider text must not leak")
			}
		}
		readings := make([]CounterReading, 0, len(paths))
		for _, p := range paths {
			readings = append(readings, sqlHostReading(p, 7))
		}
		return &sqlHostFakeSource{readings: readings}, nil
	}
	t.Cleanup(func() { discoverSQLInstances, sqlHostNewSource = savedDiscover, savedSource })
}

// One bad counter among healthy default AND named instances: the healthy
// counters/instances stay readable; the bad counter is absent (never 0). The
// group builds (partial availability) rather than failing wholesale (W4-06).
func TestSQLHostOneBadCounterKeepsHealthyReadable(t *testing.T) {
	prefixes := []string{"SQLServer:", "MSSQL$PAYROLL:"}
	badPath := sqlHostPath("SQLServer:", "General Statistics", "", "User Connections")
	sqlHostSeams(t, prefixes, badPath, nil)

	g, err := newSQLHostGroup(GroupDeps{Cores: 4})
	if err != nil {
		t.Fatalf("partial availability must still build the group, got %v", err)
	}
	v, err := g.Collect(time.Unix(1000, 0), 15)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	grp, ok := v.(*SQLHostGroup)
	if !ok || grp == nil {
		t.Fatalf("Collect returned %T, want *SQLHostGroup", v)
	}
	byLabel := map[string]SQLHostInstance{}
	for _, inst := range grp.Instances {
		byLabel[inst.Instance] = inst
	}
	def, ok := byLabel["MSSQLSERVER"]
	if !ok {
		t.Fatalf("default instance absent; got %v", byLabel)
	}
	named, ok := byLabel["PAYROLL"]
	if !ok {
		t.Fatalf("named instance absent; got %v", byLabel)
	}
	// The bad counter is absent on the default instance (never 0)...
	if def.UserConnections != nil {
		t.Fatalf("bad counter emitted %v; missing must never be 0", *def.UserConnections)
	}
	// ...while its other healthy counters and the whole named instance survive.
	if def.ProcessesBlocked == nil || *def.ProcessesBlocked != 7 {
		t.Fatalf("default healthy counter ProcessesBlocked = %v, want 7", def.ProcessesBlocked)
	}
	if named.UserConnections == nil || *named.UserConnections != 7 {
		t.Fatalf("named UserConnections = %v, want 7 (unaffected instance)", named.UserConnections)
	}
	// The missing counter is tracked for bounded retry; no instance is fully down.
	sg := g.(*sqlHostGroup)
	if len(sg.missing) != 1 || sg.missing[0] != badPath {
		t.Fatalf("missing = %v, want [%s]", sg.missing, badPath)
	}
	if len(sg.availability()) != 0 {
		t.Fatalf("availability = %v, want empty (no fully-unavailable instance)", sg.availability())
	}
}

// The bad counter recovers on the bounded retry WITHOUT a recorder restart and
// without any process-set change (W4-06). Healthy counters stay readable
// throughout.
func TestSQLHostBadCounterRecoversWithoutRestart(t *testing.T) {
	prefixes := []string{"SQLServer:", "MSSQL$PAYROLL:"}
	badPath := sqlHostPath("SQLServer:", "General Statistics", "", "User Connections")
	bad := true
	sqlHostSeams(t, prefixes, badPath, &bad)

	g, err := newSQLHostGroup(GroupDeps{Cores: 4})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	now := time.Unix(1000, 0)
	// First collect: bad counter absent, healthy readable.
	v, _ := g.Collect(now, 15)
	grp := v.(*SQLHostGroup)
	for _, inst := range grp.Instances {
		if inst.Instance == "MSSQLSERVER" && inst.UserConnections != nil {
			t.Fatalf("bad counter present before recovery: %v", *inst.UserConnections)
		}
	}
	// Recover the counter; advance past the bounded retry (no process-set change).
	bad = false
	now = now.Add(20 * time.Second)
	v, err = g.Collect(now, 15)
	if err != nil {
		t.Fatalf("Collect after recovery: %v", err)
	}
	grp = v.(*SQLHostGroup)
	var def *SQLHostInstance
	for i := range grp.Instances {
		if grp.Instances[i].Instance == "MSSQLSERVER" {
			def = &grp.Instances[i]
		}
	}
	if def == nil || def.UserConnections == nil || *def.UserConnections != 7 {
		t.Fatalf("recovered counter not readable without restart: %+v", def)
	}
	if sg := g.(*sqlHostGroup); len(sg.missing) != 0 {
		t.Fatalf("missing after recovery = %v, want none", sg.missing)
	}
}

// A fully unavailable instance is reported with a fixed diagnostic while the
// healthy instance stays readable (W4-06). No raw provider text is exposed.
func TestSQLHostUnavailableInstanceFixedDiagnostic(t *testing.T) {
	prefixes := []string{"SQLServer:", "MSSQL$PAYROLL:"}
	savedDiscover, savedSource := discoverSQLInstances, sqlHostNewSource
	discoverSQLInstances = func() ([]string, error) { return prefixes, nil }
	sqlHostNewSource = func(paths []string) (CounterSource, error) {
		for _, p := range paths {
			if strings.Contains(p, "MSSQL$PAYROLL:") {
				return nil, errors.New("raw provider text must not leak")
			}
		}
		readings := make([]CounterReading, 0, len(paths))
		for _, p := range paths {
			readings = append(readings, sqlHostReading(p, 7))
		}
		return &sqlHostFakeSource{readings: readings}, nil
	}
	t.Cleanup(func() { discoverSQLInstances, sqlHostNewSource = savedDiscover, savedSource })

	g, err := newSQLHostGroup(GroupDeps{Cores: 4})
	if err != nil {
		t.Fatalf("healthy instance must keep the group alive, got %v", err)
	}
	sg := g.(*sqlHostGroup)
	if got := sg.availability()["PAYROLL"]; got != sqlHostUnavailableCode {
		t.Fatalf("PAYROLL diagnostic = %q, want fixed code %q", got, sqlHostUnavailableCode)
	}
	v, err := g.Collect(time.Unix(1000, 0), 15)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	grp := v.(*SQLHostGroup)
	if len(grp.Instances) != 1 || grp.Instances[0].Instance != "MSSQLSERVER" {
		t.Fatalf("instances = %+v, want only the healthy MSSQLSERVER", grp.Instances)
	}
}

// When NOTHING is readable the factory returns the fixed unavailable error (no
// raw provider text), so BuildGroups keeps a dormant bounded-retry slot (W4-06).
func TestSQLHostTotalFailureFixedError(t *testing.T) {
	savedDiscover, savedSource := discoverSQLInstances, sqlHostNewSource
	discoverSQLInstances = func() ([]string, error) { return []string{"SQLServer:"}, nil }
	sqlHostNewSource = func([]string) (CounterSource, error) {
		return nil, errors.New("raw provider text must not leak")
	}
	t.Cleanup(func() { discoverSQLInstances, sqlHostNewSource = savedDiscover, savedSource })

	_, err := newSQLHostGroup(GroupDeps{Cores: 4})
	if !errors.Is(err, errSQLHostUnavailable) {
		t.Fatalf("err = %v, want fixed errSQLHostUnavailable", err)
	}
	if strings.Contains(err.Error(), "raw provider text") {
		t.Fatalf("raw provider text leaked into the error: %v", err)
	}
}
