package blackbox

import (
	"testing"
	"time"

	"api-audit-explorer/internal/sampler"
)

// Tests for the per-tick CPU work (owner finding 2026-11-18): one shared process
// read per tick, and no PDH object re-enumeration on unrelated process churn.

func TestSnapshotProcessesConvertsIdentity(t *testing.T) {
	start := time.Date(2026, 11, 18, 9, 30, 15, 123456700, time.UTC)
	src := &fakeSource{cores: 2, procs: [][]sampler.ProcSample{{
		{PID: 10, Parent: 4, Image: "a.exe", Start: start, Have: sampler.HaveTimes},
		{PID: 11, Image: "noTimes.exe"}, // unreadable creation time
	}}}
	hc := NewHostCollector(src, nil, true)
	got, err := hc.SnapshotProcesses()
	if err != nil || len(got) != 2 {
		t.Fatalf("got %v, %v", got, err)
	}
	if got[0].PID != 10 || got[0].ParentPID != 4 || got[0].Name != "a.exe" {
		t.Fatalf("identity fields wrong: %+v", got[0])
	}
	if got[0].Start != start.Unix() {
		t.Fatalf("start seconds %d, want %d", got[0].Start, start.Unix())
	}
	if want := start.UnixNano()/100 + filetimeUnixEpochDiff; got[0].StartTicks != want {
		t.Fatalf("start ticks %d, want %d", got[0].StartTicks, want)
	}
	if got[1].Start != 0 || got[1].StartTicks != 0 {
		t.Fatalf("unreadable start must stay zero: %+v", got[1])
	}
	// The result is the caller's: a second read must not overwrite it.
	src.procs = [][]sampler.ProcSample{{{PID: 99, Image: "z.exe"}}}
	src.pi = 0
	if _, err := hc.SnapshotProcesses(); err != nil {
		t.Fatal(err)
	}
	if got[0].PID != 10 {
		t.Fatalf("an earlier snapshot was overwritten: %+v", got[0])
	}
}

func TestWireResolverSnapshotUsesHostCollector(t *testing.T) {
	hc := NewHostCollector(&fakeSource{cores: 1, procs: [][]sampler.ProcSample{{{PID: 1, Image: "x.exe"}}}}, nil, true)
	r := NewResolver()
	wireResolverSnapshot(r, hc)
	if r.snapshotFn == nil || r.liteFn == nil {
		t.Fatal("the resolver must read through the host collector")
	}
	procs, err := r.liteFn()
	if err != nil || len(procs) != 1 || procs[0].PID != 1 {
		t.Fatalf("lite read: %v %v", procs, err)
	}
	// A collector that is not the host collector keeps the lightweight enumeration.
	r2 := NewResolver()
	wireResolverSnapshot(r2, nil)
	if r2.liteFn == nil {
		t.Fatal("a non-host collector must keep a lightweight enumeration")
	}
}

func TestSQLProcessSig(t *testing.T) {
	sql := ProcInfo{PID: 100, StartTicks: 5000, Name: "sqlservr.exe"}
	sql2 := ProcInfo{PID: 200, StartTicks: 7000, Name: "SQLSERVR.EXE"}
	other := ProcInfo{PID: 300, StartTicks: 9000, Name: "chrome.exe"}

	if sqlProcessSig([]ProcInfo{other}) != 0 {
		t.Fatal("no engine process must give 0")
	}
	base := sqlProcessSig([]ProcInfo{sql, other})
	if base == 0 {
		t.Fatal("an engine process must give a signature")
	}
	if got := sqlProcessSig([]ProcInfo{other, sql, {PID: 400, StartTicks: 1, Name: "x.exe"}}); got != base {
		t.Fatal("unrelated processes must not change the signature")
	}
	if a, b := sqlProcessSig([]ProcInfo{sql, sql2}), sqlProcessSig([]ProcInfo{sql2, sql}); a != b || a == base {
		t.Fatalf("two engines: order must not matter and the set must differ (%x %x %x)", a, b, base)
	}
	restarted := sql
	restarted.StartTicks = 6000
	if sqlProcessSig([]ProcInfo{restarted}) == sqlProcessSig([]ProcInfo{sql}) {
		t.Fatal("a restarted engine (same PID, new start) must change the signature")
	}
}

func TestProcessSetChangedFastPathAndReorder(t *testing.T) {
	a := []procKey{{1, 10}, {2, 20}, {3, 30}}
	if processSetChanged(a, []procKey{{1, 10}, {2, 20}, {3, 30}}) {
		t.Fatal("identical sets are not a change")
	}
	if processSetChanged(a, []procKey{{3, 30}, {1, 10}, {2, 20}}) {
		t.Fatal("a reordered set is not a change")
	}
	if !processSetChanged(a, []procKey{{1, 10}, {2, 20}, {3, 31}}) {
		t.Fatal("a recycled PID (new start) is a change")
	}
	if !processSetChanged(a, []procKey{{1, 10}, {2, 20}}) {
		t.Fatal("a shorter set is a change")
	}
	if !processSetChanged(a, []procKey{{1, 10}, {2, 20}, {4, 40}}) {
		t.Fatal("a different member is a change")
	}
}

func TestProcessSetChangedSameOrderAllocatesNothing(t *testing.T) {
	a := make([]procKey, 500)
	b := make([]procKey, 500)
	for i := range a {
		a[i] = procKey{uint32(i), int64(i) * 7}
		b[i] = a[i]
	}
	if n := testing.AllocsPerRun(20, func() { processSetChanged(a, b) }); n != 0 {
		t.Fatalf("the unchanged case allocated %v times", n)
	}
}

func TestSQLDiscoveryWantedOnlyOnEngineChange(t *testing.T) {
	var sig uint64 = 1
	g := &sqlHostGroup{sqlSig: func() uint64 { return sig }}
	t0 := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)

	// First tick: the signature is new to the group, so it discovers, then rechecks.
	if !g.discoveryWanted(t0, false, false) {
		t.Fatal("the first tick must discover")
	}
	g.lastDiscover = t0
	for i := 0; i < sqlRecheckTicks; i++ {
		if !g.discoveryWanted(t0.Add(time.Duration(i+1)*time.Second), true, false) {
			t.Fatalf("recheck tick %d must discover", i+1)
		}
	}
	// Unrelated process churn: no discovery.
	now := t0.Add(30 * time.Second)
	for i := 0; i < 20; i++ {
		if g.discoveryWanted(now.Add(time.Duration(i)*time.Second), true, false) {
			t.Fatal("unrelated process churn must not re-enumerate the PDH objects")
		}
	}
	// A due retry for missing counters still discovers.
	if !g.discoveryWanted(now, false, true) {
		t.Fatal("a due retry must discover")
	}
	// An engine appeared or went away.
	sig = 2
	if !g.discoveryWanted(now, false, false) {
		t.Fatal("an engine change must discover")
	}
	// The slow safety net: a process change after 5 minutes discovers once.
	g.lastDiscover = t0
	g.recheck = 0
	g.seenSig = sig
	if g.discoveryWanted(t0.Add(sqlRediscoverEvery-time.Second), true, false) {
		t.Fatal("inside the safety-net window nothing is re-read")
	}
	if !g.discoveryWanted(t0.Add(sqlRediscoverEvery), true, false) {
		t.Fatal("past the safety net a process change re-reads")
	}
	if g.discoveryWanted(t0.Add(sqlRediscoverEvery), false, false) {
		t.Fatal("with no process change the safety net stays quiet")
	}
}

func TestSQLDiscoveryWantedWithoutSignatureIsLegacy(t *testing.T) {
	g := &sqlHostGroup{}
	now := time.Now()
	if g.discoveryWanted(now, false, false) {
		t.Fatal("nothing changed")
	}
	if !g.discoveryWanted(now, true, false) || !g.discoveryWanted(now, false, true) {
		t.Fatal("legacy: a change or a due retry discovers")
	}
}
