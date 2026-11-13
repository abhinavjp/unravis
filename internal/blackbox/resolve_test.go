package blackbox

import (
	"errors"
	"reflect"
	"testing"
)

// TestClassifyRuntime guards rule: runtime is detected from loaded MODULE base
// names only (clr.dll => 1, coreclr/hostfxr => 2, both => 2, neither => 0),
// case-insensitive, full paths reduced to base name, exact match (no substring).
func TestClassifyRuntime(t *testing.T) {
	cases := []struct {
		name    string
		modules []string
		want    uint8
	}{
		{"framework", []string{"clr.dll"}, 1},
		{"coreclr", []string{"coreclr.dll"}, 2},
		{"hostfxr", []string{"hostfxr.dll"}, 2},
		{"both prefer 2", []string{"clr.dll", "coreclr.dll"}, 2},
		{"both hostfxr prefer 2", []string{"clr.dll", "hostfxr.dll"}, 2},
		{"neither", []string{"kernel32.dll", "ntdll.dll"}, 0},
		{"empty", nil, 0},
		{"case insensitive framework", []string{"CLR.DLL"}, 1},
		{"case insensitive core", []string{"CoreClr.Dll"}, 2},
		{"full path framework", []string{`C:\Windows\Microsoft.NET\Framework64\v4.0.30319\clr.dll`}, 1},
		{"full path core", []string{`C:\Program Files\dotnet\shared\Microsoft.NETCore.App\10.0.0\coreclr.dll`}, 2},
		{"forward slash path", []string{"C:/Windows/clr.dll"}, 1},
		{"substring must not match", []string{"clrjit.dll"}, 0},
		{"coreclr is not clr", []string{"coreclr.dll"}, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := classifyRuntime(c.modules); got != c.want {
				t.Fatalf("classifyRuntime(%v) = %d, want %d", c.modules, got, c.want)
			}
		})
	}
}

// TestProcessSetChanged guards identity = (PID, creation time): order does not
// matter, but an added/removed key or a recycled PID (same PID, new start) is a
// change.
func TestProcessSetChanged(t *testing.T) {
	a := procKey{pid: 1, start: 100}
	b := procKey{pid: 2, start: 200}
	recycled := procKey{pid: 1, start: 999} // same PID as a, new start

	cases := []struct {
		name      string
		prev, cur []procKey
		want      bool
	}{
		{"identical", []procKey{a, b}, []procKey{a, b}, false},
		{"reordered", []procKey{a, b}, []procKey{b, a}, false},
		{"added", []procKey{a}, []procKey{a, b}, true},
		{"removed", []procKey{a, b}, []procKey{a}, true},
		{"recycled same pid new start", []procKey{a}, []procKey{recycled}, true},
		{"empty to empty", nil, nil, false},
		{"empty to some", nil, []procKey{a}, true},
		{"some to empty", []procKey{a}, nil, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := processSetChanged(c.prev, c.cur); got != c.want {
				t.Fatalf("processSetChanged(%v, %v) = %v, want %v", c.prev, c.cur, got, c.want)
			}
		})
	}
}

// TestDotnetChildren picks runtime==2 children of the given parent and ignores
// every other process (out-of-process .NET 10 under a w3wp parent).
func TestDotnetChildren(t *testing.T) {
	procs := []ProcInfo{
		{PID: 10, ParentPID: 4, Runtime: 2, Name: "dotnet.exe"}, // child of 4, .NET 10 -> pick
		{PID: 11, ParentPID: 4, Runtime: 1, Name: "w3wp.exe"},   // child of 4, Framework -> ignore
		{PID: 12, ParentPID: 4, Runtime: 0, Name: "other.exe"},  // child of 4, unknown -> ignore
		{PID: 13, ParentPID: 5, Runtime: 2, Name: "dotnet.exe"}, // other parent -> ignore
		{PID: 14, ParentPID: 4, Runtime: 2, Name: "app.exe"},    // child of 4, .NET 10 -> pick
	}
	got := dotnetChildren(procs, 4)
	want := []ProcInfo{procs[0], procs[4]}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("dotnetChildren(procs, 4) = %v, want %v", got, want)
	}
	if got := dotnetChildren(procs, 99); len(got) != 0 {
		t.Fatalf("expected no children for parent 99, got %v", got)
	}
}

// TestResolverCacheReuse injects a fake snapshot and a counting module loader:
// unchanged keys never re-load modules; a recycled PID (new start) does, and
// only that key is re-loaded (the other key is reused from cache).
func TestResolverCacheReuse(t *testing.T) {
	procs := []ProcInfo{
		{PID: 100, ParentPID: 4, Start: 1000, Name: "w3wp.exe"},
		{PID: 200, ParentPID: 4, Start: 2000, Name: "dotnet.exe"},
	}
	var loads []uint32
	r := NewResolver()
	r.snapshotFn = func() ([]ProcInfo, error) {
		out := make([]ProcInfo, len(procs))
		copy(out, procs)
		return out, nil
	}
	r.loadModulesFn = func(pid uint32) ([]string, error) {
		loads = append(loads, pid)
		switch pid {
		case 100:
			return []string{"clr.dll"}, nil
		case 200:
			return []string{"coreclr.dll"}, nil
		}
		return nil, errors.New("unexpected pid")
	}

	// First inventory: both keys new -> two module loads, runtimes classified.
	got, err := r.Inventory()
	if err != nil {
		t.Fatal(err)
	}
	if len(loads) != 2 {
		t.Fatalf("first inventory: want 2 loads, got %d (%v)", len(loads), loads)
	}
	if got[0].Runtime != 1 || got[1].Runtime != 2 {
		t.Fatalf("runtimes = %d,%d want 1,2", got[0].Runtime, got[1].Runtime)
	}

	// Same set again: cache reuse, zero module loads.
	loads = nil
	got, err = r.Inventory()
	if err != nil {
		t.Fatal(err)
	}
	if len(loads) != 0 {
		t.Fatalf("unchanged set: want 0 loads, got %d (%v)", len(loads), loads)
	}
	if got[0].Runtime != 1 || got[1].Runtime != 2 {
		t.Fatalf("cached runtimes = %d,%d want 1,2", got[0].Runtime, got[1].Runtime)
	}

	// Recycle PID 100 (same PID, new start): identity changes -> reload only 100.
	procs[0].Start = 5000
	loads = nil
	got, err = r.Inventory()
	if err != nil {
		t.Fatal(err)
	}
	if len(loads) != 1 || loads[0] != 100 {
		t.Fatalf("recycled PID: want 1 load of PID 100, got %v", loads)
	}
	if got[0].Runtime != 1 || got[1].Runtime != 2 {
		t.Fatalf("runtimes after recycle = %d,%d want 1,2", got[0].Runtime, got[1].Runtime)
	}
}

// TestResolverAccessDeniedUnknown guards rule: an access-denied module read
// leaves runtime 0 (unknown), never a guessed runtime.
func TestResolverAccessDeniedUnknown(t *testing.T) {
	r := NewResolver()
	r.snapshotFn = func() ([]ProcInfo, error) {
		return []ProcInfo{{PID: 7, Start: 1, Name: "protected.exe"}}, nil
	}
	r.loadModulesFn = func(uint32) ([]string, error) {
		return nil, errors.New("access denied")
	}
	got, err := r.Inventory()
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Runtime != 0 {
		t.Fatalf("access denied must yield runtime 0 (unknown), got %d", got[0].Runtime)
	}
}

// TestResolverNoCacheWhenStartZero guards the fix: an unopenable process has
// Start 0, so its key (pid, 0) would degenerate identity to PID alone. Such
// keys are never cached — modules are re-loaded on every Inventory even when
// the PID is unchanged, so a recycled PID that is also unopenable picks up its
// new runtime instead of inheriting a stale one. Keys with a real Start keep
// being cached.
func TestResolverNoCacheWhenStartZero(t *testing.T) {
	procs := []ProcInfo{
		{PID: 300, ParentPID: 4, Start: 0, Name: "protected.exe"}, // unopenable: Start 0
		{PID: 400, ParentPID: 4, Start: 4000, Name: "w3wp.exe"},   // real Start: cached
	}
	var loads []uint32
	modules300 := []string{"clr.dll"} // PID 300 starts as .NET Framework
	r := NewResolver()
	r.snapshotFn = func() ([]ProcInfo, error) {
		out := make([]ProcInfo, len(procs))
		copy(out, procs)
		return out, nil
	}
	r.loadModulesFn = func(pid uint32) ([]string, error) {
		loads = append(loads, pid)
		switch pid {
		case 300:
			return modules300, nil
		case 400:
			return []string{"clr.dll"}, nil
		}
		return nil, errors.New("unexpected pid")
	}

	// First inventory: both keys new -> two module loads.
	got, err := r.Inventory()
	if err != nil {
		t.Fatal(err)
	}
	if len(loads) != 2 {
		t.Fatalf("first inventory: want 2 loads, got %d (%v)", len(loads), loads)
	}
	if got[0].Runtime != 1 || got[1].Runtime != 1 {
		t.Fatalf("runtimes = %d,%d want 1,1", got[0].Runtime, got[1].Runtime)
	}

	// Same set, same PID: the Start==0 key must still re-load modules; the
	// real-Start key is reused from cache.
	loads = nil
	got, err = r.Inventory()
	if err != nil {
		t.Fatal(err)
	}
	if len(loads) != 1 || loads[0] != 300 {
		t.Fatalf("unchanged set: want 1 load of PID 300 (Start 0), got %v", loads)
	}
	if got[0].Runtime != 1 || got[1].Runtime != 1 {
		t.Fatalf("runtimes = %d,%d want 1,1", got[0].Runtime, got[1].Runtime)
	}

	// Recycle: same PID 300, still unopenable (Start 0), now a .NET 10 target.
	// The runtime change must be picked up, not masked by a stale cache entry.
	modules300 = []string{"coreclr.dll"}
	loads = nil
	got, err = r.Inventory()
	if err != nil {
		t.Fatal(err)
	}
	if len(loads) != 1 || loads[0] != 300 {
		t.Fatalf("recycled unopenable PID: want 1 load of PID 300, got %v", loads)
	}
	if got[0].Runtime != 2 {
		t.Fatalf("runtime after recycle = %d, want 2 (must not inherit stale Framework)", got[0].Runtime)
	}
	if got[1].Runtime != 1 {
		t.Fatalf("real-Start runtime = %d, want 1 (still cached)", got[1].Runtime)
	}
}
