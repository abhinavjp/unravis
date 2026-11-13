package blackbox

import (
	"errors"
	"reflect"
	"testing"
)

// newMapTestResolver wires a Resolver with a fake snapshot, module loader and
// (caller-set) instance/service enumerators so mapping tests need no Windows.
func newMapTestResolver(procs func() []ProcInfo, modules map[uint32][]string) *Resolver {
	r := NewResolver()
	r.snapshotFn = func() ([]ProcInfo, error) {
		src := procs()
		out := make([]ProcInfo, len(src))
		copy(out, src)
		return out, nil
	}
	r.loadModulesFn = func(pid uint32) ([]string, error) {
		if m, ok := modules[pid]; ok {
			return m, nil
		}
		return nil, errors.New("access denied")
	}
	return r
}

// TestParseW3WPInstance guards the instance grammar "<PID>_<AppPoolName>":
// split on the FIRST underscore, pid = leading digits, pool = the remainder
// (spaces and further underscores allowed). No underscore, non-numeric pid,
// pid 0 or an empty pool is rejected.
func TestParseW3WPInstance(t *testing.T) {
	cases := []struct {
		inst string
		pid  uint32
		pool string
		ok   bool
	}{
		{"4321_Payroll API", 4321, "Payroll API", true},
		{"1234_My_Pool", 1234, "My_Pool", true},
		{"nopid", 0, "", false},
		{"_x", 0, "", false},
		{"", 0, "", false},
		{"abc_pool", 0, "", false},
		{"12x_pool", 0, "", false},
		{"123_", 0, "", false},
		{"0_pool", 0, "", false},
		{"99999999999999_pool", 0, "", false}, // overflows uint32
	}
	for _, c := range cases {
		t.Run(c.inst, func(t *testing.T) {
			pid, pool, ok := parseW3WPInstance(c.inst)
			if pid != c.pid || pool != c.pool || ok != c.ok {
				t.Fatalf("parseW3WPInstance(%q) = (%d,%q,%v), want (%d,%q,%v)",
					c.inst, pid, pool, ok, c.pid, c.pool, c.ok)
			}
		})
	}
}

// TestResolveTargetsPoolMatch guards: a selected pool resolves via the counter
// instance to the w3wp with PID/Start/Runtime confirmed from the inventory,
// case-insensitive, and unselected pools are never emitted.
func TestResolveTargetsPoolMatch(t *testing.T) {
	procs := []ProcInfo{
		{PID: 4321, ParentPID: 4, Start: 1000, Name: "w3wp.exe"},
		{PID: 7777, ParentPID: 4, Start: 1100, Name: "w3wp.exe"},
	}
	r := newMapTestResolver(func() []ProcInfo { return procs },
		map[uint32][]string{4321: {"clr.dll"}, 7777: {"clr.dll"}})
	r.instancesFn = func() ([]string, error) { return []string{"4321_Payroll API", "7777_Other"}, nil }
	r.servicesFn = func() ([]svcProc, error) { return nil, nil }

	got, missing := r.ResolveTargets([]Target{{Kind: "appPool", Name: "payroll api"}})
	want := []ResolvedTarget{{Key: "pool:payroll api", PID: 4321, Start: 1000, Runtime: 1, Pool: "payroll api"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ResolveTargets = %+v, want %+v (owner-selected only)", got, want)
	}
	if len(missing) != 0 {
		t.Fatalf("missing = %v, want none", missing)
	}
}

// TestResolveTargetsNotRunning guards: unreadable means missing — a selected
// name with no live match goes to the "not running" list and NO zeroed target
// is emitted (absent name, stale instance PID, unreadable Start, PID 0).
func TestResolveTargetsNotRunning(t *testing.T) {
	cases := []struct {
		name    string
		procs   []ProcInfo
		inst    []string
		svc     []svcProc
		targets []Target
	}{
		{
			name:    "absent pool and service",
			procs:   []ProcInfo{{PID: 4321, Start: 1000, Name: "w3wp.exe"}},
			inst:    []string{"4321_Payroll API"},
			svc:     []svcProc{{name: "Spooler", pid: 500}},
			targets: []Target{{Kind: "appPool", Name: "Gone"}, {Kind: "service", Name: "Missing"}},
		},
		{
			name:    "stale instance pid not in inventory",
			procs:   []ProcInfo{{PID: 4321, Start: 1000, Name: "w3wp.exe"}},
			inst:    []string{"9999_Stale"},
			targets: []Target{{Kind: "appPool", Name: "Stale"}},
		},
		{
			name:    "creation time unreadable is not running",
			procs:   []ProcInfo{{PID: 4321, Start: 0, Name: "w3wp.exe"}},
			inst:    []string{"4321_Zero"},
			targets: []Target{{Kind: "appPool", Name: "Zero"}},
		},
		{
			name:    "service pid zero",
			procs:   []ProcInfo{{PID: 500, Start: 5000, Name: "svchost.exe"}},
			svc:     []svcProc{{name: "Zero", pid: 0}},
			targets: []Target{{Kind: "service", Name: "Zero"}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			procs := c.procs
			r := newMapTestResolver(func() []ProcInfo { return procs },
				map[uint32][]string{4321: {"clr.dll"}, 500: {"clr.dll"}})
			r.instancesFn = func() ([]string, error) { return c.inst, nil }
			r.servicesFn = func() ([]svcProc, error) { return c.svc, nil }
			got, missing := r.ResolveTargets(c.targets)
			if len(got) != 0 {
				t.Fatalf("got %+v, want no targets (never a zeroed target)", got)
			}
			var want []string
			for _, tg := range c.targets {
				want = append(want, tg.Name)
			}
			if !reflect.DeepEqual(missing, want) {
				t.Fatalf("missing = %v, want %v", missing, want)
			}
		})
	}
}

// TestResolveTargetsPIDReuse guards identity = (PID, creation time): after a
// recycle the same PID with a new Start resolves to a NEW target instance —
// the Start must differ, never the stale one.
func TestResolveTargetsPIDReuse(t *testing.T) {
	procs := []ProcInfo{{PID: 4321, ParentPID: 4, Start: 1000, Name: "w3wp.exe"}}
	r := newMapTestResolver(func() []ProcInfo { return procs },
		map[uint32][]string{4321: {"clr.dll"}})
	r.instancesFn = func() ([]string, error) { return []string{"4321_Pool"}, nil }
	r.servicesFn = func() ([]svcProc, error) { return nil, nil }
	targets := []Target{{Kind: "appPool", Name: "Pool"}}

	first, missing := r.ResolveTargets(targets)
	if len(first) != 1 || len(missing) != 0 {
		t.Fatalf("first resolve = %+v / %v, want one target", first, missing)
	}
	procs[0].Start = 2000 // recycled: same PID, new creation time
	second, missing := r.ResolveTargets(targets)
	if len(second) != 1 || len(missing) != 0 {
		t.Fatalf("second resolve = %+v / %v, want one target", second, missing)
	}
	if first[0].PID != second[0].PID || first[0].Key != second[0].Key {
		t.Fatalf("identity drift: %+v vs %+v", first[0], second[0])
	}
	if second[0].Start != 2000 || first[0].Start == second[0].Start {
		t.Fatalf("recycled PID must resolve to the new Start: first %+v, second %+v", first[0], second[0])
	}
}

// TestResolveTargetsOutOfProcess guards: a runtime-0 w3wp with a runtime-2
// child targets the CHILD (out-of-process .NET 10), key stays the pool's.
// In-process runtimes (1 or 2 on the w3wp itself) keep targeting the w3wp.
func TestResolveTargetsOutOfProcess(t *testing.T) {
	cases := []struct {
		name    string
		modules map[uint32][]string
		wantPID uint32
		wantRT  uint8
	}{
		{"runtime 0 parent redirects to child", map[uint32][]string{200: {"coreclr.dll"}}, 200, 2},
		{"in-process framework keeps w3wp", map[uint32][]string{100: {"clr.dll"}, 200: {"coreclr.dll"}}, 100, 1},
		{"in-process net10 keeps w3wp", map[uint32][]string{100: {"coreclr.dll"}, 200: {"coreclr.dll"}}, 100, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			procs := []ProcInfo{
				{PID: 100, ParentPID: 4, Start: 1000, Name: "w3wp.exe"},
				{PID: 200, ParentPID: 100, Start: 1100, Name: "dotnet.exe"},
			}
			r := newMapTestResolver(func() []ProcInfo { return procs }, c.modules)
			r.instancesFn = func() ([]string, error) { return []string{"100_Pool"}, nil }
			r.servicesFn = func() ([]svcProc, error) { return nil, nil }
			got, missing := r.ResolveTargets([]Target{{Kind: "appPool", Name: "Pool"}})
			want := []ResolvedTarget{{Key: "pool:Pool", PID: c.wantPID, Start: 1000, Runtime: c.wantRT, Pool: "Pool"}}
			if c.wantPID == 200 {
				want[0].Start = 1100
			}
			if !reflect.DeepEqual(got, want) || len(missing) != 0 {
				t.Fatalf("got %+v / %v, want %+v", got, missing, want)
			}
		})
	}
}

// TestResolveTargetsServiceMatch guards: a selected running service resolves
// by name (case-insensitive) to its PID with Start/Runtime from the inventory.
func TestResolveTargetsServiceMatch(t *testing.T) {
	procs := []ProcInfo{{PID: 900, Start: 9000, Name: "sqlservr.exe"}}
	r := newMapTestResolver(func() []ProcInfo { return procs },
		map[uint32][]string{900: {"clr.dll"}})
	r.instancesFn = func() ([]string, error) { return nil, nil }
	r.servicesFn = func() ([]svcProc, error) { return []svcProc{{name: "MSSQLSERVER", pid: 900}}, nil }

	got, missing := r.ResolveTargets([]Target{{Kind: "service", Name: "mssqlserver"}})
	want := []ResolvedTarget{{Key: "svc:mssqlserver", PID: 900, Start: 9000, Runtime: 1}}
	if !reflect.DeepEqual(got, want) || len(missing) != 0 {
		t.Fatalf("got %+v / %v, want %+v", got, missing, want)
	}
}

// TestResolveTargetsEnumeratesOnChange guards the footprint: instance/service
// enumeration runs only when the process set changes, never every tick.
func TestResolveTargetsEnumeratesOnChange(t *testing.T) {
	procs := []ProcInfo{{PID: 4321, Start: 1000, Name: "w3wp.exe"}}
	var instCalls, svcCalls int
	r := newMapTestResolver(func() []ProcInfo { return procs },
		map[uint32][]string{4321: {"clr.dll"}})
	r.instancesFn = func() ([]string, error) { instCalls++; return []string{"4321_Pool"}, nil }
	r.servicesFn = func() ([]svcProc, error) { svcCalls++; return nil, nil }
	targets := []Target{{Kind: "appPool", Name: "Pool"}}

	for i := 0; i < 3; i++ {
		if _, m := r.ResolveTargets(targets); len(m) != 0 {
			t.Fatalf("tick %d: missing %v", i, m)
		}
	}
	if instCalls != 1 || svcCalls != 1 {
		t.Fatalf("unchanged set: enumerated %d/%d times, want 1/1", instCalls, svcCalls)
	}
	procs = append(procs, ProcInfo{PID: 5000, Start: 5000, Name: "new.exe"}) // set changed
	if _, m := r.ResolveTargets(targets); len(m) != 0 {
		t.Fatalf("after change: missing %v", m)
	}
	if instCalls != 2 || svcCalls != 2 {
		t.Fatalf("changed set: enumerated %d/%d times, want 2/2", instCalls, svcCalls)
	}
	if _, m := r.ResolveTargets(targets); len(m) != 0 {
		t.Fatalf("stable again: missing %v", m)
	}
	if instCalls != 2 || svcCalls != 2 {
		t.Fatalf("stable again: enumerated %d/%d times, want 2/2", instCalls, svcCalls)
	}
}

// TestResolveTargetsInventoryError guards: when the inventory is unreadable
// (off Windows this is errWindowsOnly), every selected name is "not found"
// and no target is emitted.
func TestResolveTargetsInventoryError(t *testing.T) {
	r := NewResolver()
	r.snapshotFn = func() ([]ProcInfo, error) { return nil, errors.New("denied") }
	got, missing := r.ResolveTargets([]Target{{Kind: "appPool", Name: "A"}, {Kind: "service", Name: "B"}})
	if len(got) != 0 || !reflect.DeepEqual(missing, []string{"A", "B"}) {
		t.Fatalf("got %+v / %v, want nil / [A B]", got, missing)
	}
}
