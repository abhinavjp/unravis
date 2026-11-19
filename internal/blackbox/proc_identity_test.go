package blackbox

import (
	"strings"
	"testing"
	"time"
)

// This file holds the wave-5 identity adversarial tests (W5-01..W5-04). They use
// the existing fake-source seams (no Windows, no live process/service ops) and
// assert on internal engine state / call counts, not only emitted rows.

// identityTestBase is the tick clock (unix seconds) the identity fixtures are
// written against: the first tick of every test collects at time.Unix(1000, 0).
const identityTestBase int64 = 1000

// elapsedFor returns the Elapsed Time reading (seconds) of a process that started
// at startUnix, as seen by the first tick. The group derives the start as tick
// time minus Elapsed Time, so this yields exactly startUnix on that tick.
func elapsedFor(startUnix int64) float64 {
	return float64(identityTestBase - startUnix)
}

// ageElapsed advances every Elapsed Time reading by secs, as the wall clock
// would between two ticks. Call it before each tick after the first so the
// derived start stays put.
func ageElapsed(rs []CounterReading, secs float64) {
	for i := range rs {
		if strings.HasSuffix(rs[i].Path, `\Elapsed Time`) {
			rs[i].Value += secs
		}
	}
}

// --- procIO (W5-04): precise creation identity, no age tolerance ---

// A different creation more than the 3 s slack away must be rejected. The start
// is derived from Elapsed Time (Windows has no Creation Time counter), so the
// slack only absorbs the rounding of that counter.
func TestProcIORejectsCreationBeyondSlack(t *testing.T) {
	for _, delta := range []int64{-60, -9, -4, 4, 9, 60} {
		src := &procIOFakeSource{}
		procIOInstance(src, "w3wp")
		procIOIdent(src, "w3wp", 100, 900+delta) // creation differs from Start 900
		src.readings = append(src.readings, procIOReading("w3wp", "Page File Bytes", 1000))
		g := newProcIOTestGroup(t, src, func() []ResolvedTarget {
			return []ResolvedTarget{{Key: "pool:a", PID: 100, Start: 900}}
		}, nil)
		if grp := collectProcIO(t, g, src, 1); grp != nil {
			t.Fatalf("delta %d: group = %+v, want nil (a different creation beyond the slack must be rejected)", delta, grp)
		}
	}
}

// A valid delayed read of the SAME generation must still be accepted: identity
// is independent of the collection clock, so a late source read (large Elapsed
// Time, late now) does not drop the target. The old now-minus-elapsed estimate
// rejected this once the delay exceeded 3s.
func TestProcIOAcceptsDelayedRead(t *testing.T) {
	src := &procIOFakeSource{}
	procIOInstance(src, "w3wp")
	// Created at 900; the tick clock is 6000, so Elapsed Time is 5100 s and the
	// derived start is 900. A late tick clock does not break identity as long as
	// Elapsed Time is read in the same tick.
	src.paths = append(src.paths, procIOPath("w3wp", "ID Process"), procIOPath("w3wp", "Elapsed Time"))
	src.readings = append(src.readings,
		procIOReading("w3wp", "ID Process", 100),
		procIOReading("w3wp", "Elapsed Time", 5100),
	)
	src.readings = append(src.readings,
		procIOReading("w3wp", "Page File Bytes", 1000),
	)
	g := newProcIOTestGroup(t, src, func() []ResolvedTarget {
		return []ResolvedTarget{{Key: "pool:a", PID: 100, Start: 900}}
	}, nil)
	// Collect far later than the creation; the delayed read must still confirm.
	v, err := g.Collect(time.Unix(6000, 0), 15)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	grp, _ := v.(*ProcIOGroup)
	if grp == nil || len(grp.Targets) != 1 || grp.Targets[0].Target != "pool:a" {
		t.Fatalf("group = %+v, want the delayed same-generation read accepted", grp)
	}
	if grp.Targets[0].PageFileBytes == nil || *grp.Targets[0].PageFileBytes != 1000 {
		t.Fatalf("PageFileBytes = %v, want 1000", grp.Targets[0].PageFileBytes)
	}
}

// Mid-collect PID replacement: the view is captured BEFORE collection and never
// re-resolved afterwards. Readings belonging to the replacement generation
// (same PID, different creation) must not be emitted under the captured target.
func TestProcIOMidCollectPIDReplacement(t *testing.T) {
	src := &procIOFakeSource{}
	procIOInstance(src, "w3wp")
	// The PID was reused mid-collect: the sample now belongs to creation 950.
	procIOIdent(src, "w3wp", 100, 950)
	src.readings = append(src.readings, procIOReading("w3wp", "Page File Bytes", 1000))
	calls := 0
	targets := func() []ResolvedTarget {
		calls++
		if calls == 1 {
			return []ResolvedTarget{{Key: "pool:a", PID: 100, Start: 900}} // captured pre-collect
		}
		return []ResolvedTarget{{Key: "pool:a", PID: 100, Start: 950}} // replacement post-collect
	}
	g := newProcIOTestGroup(t, src, targets, nil)
	if grp := collectProcIO(t, g, src, 1); grp != nil {
		t.Fatalf("group = %+v, want nil (replacement readings must not match the captured identity)", grp)
	}
	if calls != 1 {
		t.Fatalf("targets called %d times, want 1 (no re-resolution after collection)", calls)
	}
}

// The captured generation's own readings ARE emitted even if a later targets()
// call would return a different generation — proving the join uses the captured
// view, not a post-collect re-resolution.
func TestProcIOUsesCapturedViewNotReResolved(t *testing.T) {
	src := &procIOFakeSource{}
	procIOInstance(src, "w3wp")
	procIOIdent(src, "w3wp", 100, 900) // matches the captured generation
	src.readings = append(src.readings, procIOReading("w3wp", "Page File Bytes", 1000))
	calls := 0
	targets := func() []ResolvedTarget {
		calls++
		if calls == 1 {
			return []ResolvedTarget{{Key: "pool:a", PID: 100, Start: 900}}
		}
		return []ResolvedTarget{{Key: "pool:a", PID: 100, Start: 950}}
	}
	g := newProcIOTestGroup(t, src, targets, nil)
	grp := collectProcIO(t, g, src, 1)
	if grp == nil || len(grp.Targets) != 1 || grp.Targets[0].PageFileBytes == nil || *grp.Targets[0].PageFileBytes != 1000 {
		t.Fatalf("group = %+v, want the captured generation's reading emitted", grp)
	}
	if calls != 1 {
		t.Fatalf("targets called %d times, want 1", calls)
	}
}

// Duplicate PID mappings fail closed: two instances reporting one PID are
// ambiguous, so neither is emitted (never first-wins).
func TestProcIODuplicatePIDFailsClosed(t *testing.T) {
	src := &procIOFakeSource{}
	procIOInstance(src, "w3wp")
	procIOInstance(src, "w3wp#1")
	procIOIdent(src, "w3wp", 100, 900)
	procIOIdent(src, "w3wp#1", 100, 900) // same PID 100
	src.readings = append(src.readings,
		procIOReading("w3wp", "Page File Bytes", 1000),
		procIOReading("w3wp#1", "Page File Bytes", 2000),
	)
	g := newProcIOTestGroup(t, src, func() []ResolvedTarget {
		return []ResolvedTarget{{Key: "pool:a", PID: 100, Start: 900}}
	}, nil)
	if grp := collectProcIO(t, g, src, 1); grp != nil {
		t.Fatalf("group = %+v, want nil (ambiguous duplicate PID must fail closed)", grp)
	}
}

// W5-03 source spy: an unselected instance's detail must never be processed or
// retained by the engine. Asserted on engine history (the processing record),
// not on emitted rows. The source IS read (collects > 0) but unselected detail
// is filtered before the engine.
func TestProcIOUnselectedNotProcessed(t *testing.T) {
	src := &procIOFakeSource{}
	procIOInstance(src, "sel")
	procIOInstance(src, "unsel")
	procIOIdent(src, "sel", 100, 900)
	procIOIdent(src, "unsel", 300, 900)
	src.readings = append(src.readings,
		procIOReading("sel", "IO Read Bytes/sec", 10),
		procIOReading("unsel", "IO Read Bytes/sec", 999),
		procIOReading("sel", "Page File Bytes", 1000),
		procIOReading("unsel", "Page File Bytes", 3000),
	)
	g := newProcIOTestGroup(t, src, func() []ResolvedTarget {
		return []ResolvedTarget{{Key: "pool:a", PID: 100, Start: 900}}
	}, nil)
	collectProcIO(t, g, src, 3) // prime rates across ticks
	if src.collects == 0 {
		t.Fatal("source was never collected")
	}
	if _, ok := g.engine.history[procIOPath("unsel", "IO Read Bytes/sec")]; ok {
		t.Fatal("unselected instance rate was processed/retained in engine history (W5-03)")
	}
	if _, ok := g.engine.history[procIOPath("sel", "IO Read Bytes/sec")]; !ok {
		t.Fatal("selected instance rate missing from engine history")
	}
}

// --- proc10 (W5-01): creation join, captured view, ambiguity, selection ---

func TestProc10MidCollectPIDReplacement(t *testing.T) {
	src := &proc10FakeSource{}
	proc10Instance(src, "dotnet", 7777, 950, 20, 3000, 4000) // replacement creation 950
	calls := 0
	targets := func() []ResolvedTarget {
		calls++
		if calls == 1 {
			return []ResolvedTarget{{Key: "pool:a", PID: 7777, Start: 900, Runtime: 2}}
		}
		return []ResolvedTarget{{Key: "pool:a", PID: 7777, Start: 950, Runtime: 2}}
	}
	g := newProc10TestGroupFn(t, src, targets, nil)
	v, err := g.Collect(time.Unix(1000, 0), 15) // ONE collect: the view is captured once
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if grp, _ := v.(*Proc10Group); grp != nil {
		t.Fatalf("group = %+v, want nil (replacement creation must not match captured Start)", grp)
	}
	if calls != 1 {
		t.Fatalf("targets called %d times, want 1", calls)
	}
}

func TestProc10DuplicatePIDFailsClosed(t *testing.T) {
	src := &proc10FakeSource{}
	proc10Instance(src, "dotnet", 7777, 900, 20, 3000, 4000)
	proc10Instance(src, "dotnet#1", 7777, 900, 30, 5000, 6000) // same PID
	g := newProc10TestGroupFn(t, src, func() []ResolvedTarget {
		return []ResolvedTarget{{Key: "pool:a", PID: 7777, Start: 900, Runtime: 2}}
	}, nil)
	if grp := proc10Collect(t, g, src, 3); grp != nil {
		t.Fatalf("group = %+v, want nil (ambiguous duplicate PID)", grp)
	}
}

func TestProc10CreationMismatch(t *testing.T) {
	src := &proc10FakeSource{}
	proc10Instance(src, "dotnet", 7777, 906, 20, 3000, 4000) // creation 906 != Start 900
	g := newProc10TestGroupFn(t, src, func() []ResolvedTarget {
		return []ResolvedTarget{{Key: "pool:a", PID: 7777, Start: 900, Runtime: 2}}
	}, nil)
	if grp := proc10Collect(t, g, src, 3); grp != nil {
		t.Fatalf("group = %+v, want nil (creation does not confirm Start)", grp)
	}
}

func TestProc10UnselectedNotProcessed(t *testing.T) {
	src := &proc10FakeSource{}
	proc10Instance(src, "sel", 100, 900, 10, 1000, 2000)
	proc10Instance(src, "unsel", 300, 900, 99, 9000, 9000)
	g := newProc10TestGroupFn(t, src, func() []ResolvedTarget {
		return []ResolvedTarget{{Key: "pool:a", PID: 100, Start: 900, Runtime: 2}}
	}, nil)
	proc10Collect(t, g, src, 3)
	if _, ok := g.engine.history[proc10Path("unsel", "% Processor Time")]; ok {
		t.Fatal("unselected instance CPU rate was processed/retained (W5-03)")
	}
	if _, ok := g.engine.history[proc10Path("sel", "% Processor Time")]; !ok {
		t.Fatal("selected instance CPU rate missing from history")
	}
}

// --- CLR (W5-01): creation join via Process object, captured view, ambiguity ---

func TestCLRMidCollectPIDReplacement(t *testing.T) {
	src := &clrFakeSource{}
	src.paths = append(src.paths, clrInstancePaths("w3wp")...)
	src.readings = append(src.readings,
		clrReading(".NET CLR Memory", "w3wp", "Process ID", 100),
		clrReading(".NET CLR Memory", "w3wp", "Gen 0 heap size", 111),
	)
	clrProcessIdent(src, "w3wp", 100, 950) // replacement creation 950
	calls := 0
	targets := func() []ResolvedTarget {
		calls++
		if calls == 1 {
			return []ResolvedTarget{{Key: "pool:a", PID: 100, Start: 900, Runtime: 1}}
		}
		return []ResolvedTarget{{Key: "pool:a", PID: 100, Start: 950, Runtime: 1}}
	}
	g := newCLRTestGroup(t, src, targets, nil)
	if grp := clrCollect(t, g, time.Unix(1000, 0)); grp != nil {
		t.Fatalf("group = %+v, want nil (replacement creation must not match captured Start)", grp)
	}
	if calls != 1 {
		t.Fatalf("targets called %d times, want 1", calls)
	}
}

func TestCLRDuplicatePIDFailsClosed(t *testing.T) {
	src := &clrFakeSource{}
	src.paths = append(src.paths, clrInstancePaths("w3wp")...)
	src.paths = append(src.paths, clrInstancePaths("w3wp#1")...)
	src.readings = append(src.readings,
		clrReading(".NET CLR Memory", "w3wp", "Process ID", 100),
		clrReading(".NET CLR Memory", "w3wp#1", "Process ID", 100), // same PID
		clrReading(".NET CLR Memory", "w3wp", "Gen 0 heap size", 111),
		clrReading(".NET CLR Memory", "w3wp#1", "Gen 0 heap size", 222),
	)
	clrProcessIdent(src, "w3wp", 100, 900)
	g := newCLRTestGroup(t, src, func() []ResolvedTarget {
		return []ResolvedTarget{{Key: "pool:a", PID: 100, Start: 900, Runtime: 1}}
	}, nil)
	if grp := clrCollect(t, g, time.Unix(1000, 0)); grp != nil {
		t.Fatalf("group = %+v, want nil (ambiguous duplicate CLR PID)", grp)
	}
}

func TestCLRCreationMismatch(t *testing.T) {
	src := &clrFakeSource{}
	src.paths = append(src.paths, clrInstancePaths("w3wp")...)
	src.readings = append(src.readings,
		clrReading(".NET CLR Memory", "w3wp", "Process ID", 100),
		clrReading(".NET CLR Memory", "w3wp", "Gen 0 heap size", 111),
	)
	clrProcessIdent(src, "w3wp", 100, 906) // creation 906 != Start 900
	g := newCLRTestGroup(t, src, func() []ResolvedTarget {
		return []ResolvedTarget{{Key: "pool:a", PID: 100, Start: 900, Runtime: 1}}
	}, nil)
	if grp := clrCollect(t, g, time.Unix(1000, 0)); grp != nil {
		t.Fatalf("group = %+v, want nil (creation does not confirm Start)", grp)
	}
}

func TestCLRUnselectedNotProcessed(t *testing.T) {
	src := &clrFakeSource{}
	src.paths = append(src.paths, clrInstancePaths("sel")...)
	src.paths = append(src.paths, clrInstancePaths("unsel")...)
	src.readings = append(src.readings,
		clrReading(".NET CLR Memory", "sel", "Process ID", 100),
		clrReading(".NET CLR Memory", "unsel", "Process ID", 300),
		clrReading(".NET CLR Memory", "sel", "Allocated Bytes/sec", 50),
		clrReading(".NET CLR Memory", "unsel", "Allocated Bytes/sec", 999),
	)
	clrProcessIdent(src, "sel", 100, 900)
	clrProcessIdent(src, "unsel", 300, 900)
	g := newCLRTestGroup(t, src, func() []ResolvedTarget {
		return []ResolvedTarget{{Key: "pool:a", PID: 100, Start: 900, Runtime: 1}}
	}, nil)
	for i := 0; i < 3; i++ {
		clrCollect(t, g, time.Unix(1000+int64(i)*15, 0))
	}
	if _, ok := g.engine.history[clrPath(".NET CLR Memory", "unsel", "Allocated Bytes/sec")]; ok {
		t.Fatal("unselected CLR instance rate was processed/retained (W5-03)")
	}
	if _, ok := g.engine.history[clrPath(".NET CLR Memory", "sel", "Allocated Bytes/sec")]; !ok {
		t.Fatal("selected CLR instance rate missing from history")
	}
}

// --- IIS (W5-02): verified worker identity ---

// A worker PID that is not a live process in the inventory is rejected even
// though its pool name matches.
func TestIISWrongWorkerPIDRejected(t *testing.T) {
	src := &iisFakeSource{paths: iisPoolPaths("999_pool-a")}
	src.readings = iisPoolReadings("999_pool-a", 1, 2, 3)
	// Inventory has PID 100 alive, NOT 999.
	iisWorkerIdent(src, "w3wp", 100, 10)
	g := newIISTestGroup(t, src, []ResolvedTarget{{Key: "pool:pool-a", PID: 100, Start: 10, Pool: "pool-a"}}, nil)
	if grp := collectIIS(t, g, time.Unix(1000, 0)); grp != nil {
		t.Fatalf("group = %+v, want nil (worker PID 999 not in inventory)", grp)
	}
}

// A recycled worker PID (alive but a different creation) is rejected for an
// in-process target: the creation must confirm the target's Start.
func TestIISStaleWorkerGenerationRejected(t *testing.T) {
	src := &iisFakeSource{paths: iisPoolPaths("100_pool-a")}
	src.readings = iisPoolReadings("100_pool-a", 1, 2, 3)
	iisWorkerIdent(src, "w3wp", 100, 950) // worker creation 950, target Start 10
	g := newIISTestGroup(t, src, []ResolvedTarget{{Key: "pool:pool-a", PID: 100, Start: 10, Pool: "pool-a"}}, nil)
	if grp := collectIIS(t, g, time.Unix(1000, 0)); grp != nil {
		t.Fatalf("group = %+v, want nil (stale worker generation)", grp)
	}
}

// Legitimate overlapping workers (web garden): every live worker of the selected
// pool is emitted, each verified against the inventory.
func TestIISOverlappingWorkers(t *testing.T) {
	src := &iisFakeSource{}
	src.paths = append(src.paths, iisPoolPaths("100_pool-a")...)
	src.paths = append(src.paths, iisPoolPaths("200_pool-a")...)
	src.readings = append(src.readings, iisPoolReadings("100_pool-a", 1, 2, 3)...)
	src.readings = append(src.readings, iisPoolReadings("200_pool-a", 7, 8, 9)...)
	iisWorkerIdent(src, "w3wp", 100, 10)
	iisWorkerIdent(src, "w3wp#1", 200, 20)
	// Target is worker 100 (in-process); worker 200 overlaps.
	g := newIISTestGroup(t, src, []ResolvedTarget{{Key: "pool:pool-a", PID: 100, Start: 10, Pool: "pool-a"}}, nil)
	grp := collectIIS(t, g, time.Unix(1000, 0))
	if grp == nil || len(grp.Workers) != 2 {
		t.Fatalf("group = %+v, want 2 overlapping workers", grp)
	}
	if grp.Workers[0].PID != 100 || grp.Workers[1].PID != 200 {
		t.Fatalf("worker PIDs = %d/%d, want 100/200", grp.Workers[0].PID, grp.Workers[1].PID)
	}
	if grp.Workers[1].ActiveRequests == nil || *grp.Workers[1].ActiveRequests != 7 {
		t.Fatalf("worker 200 ActiveRequests = %v, want 7", grp.Workers[1].ActiveRequests)
	}
}

// Out-of-process .NET 10: the target PID is the runtime-2 child, the W3WP
// instance is the w3wp parent. The parent is verified alive in the inventory and
// emitted with its own worker PID, never the child PID.
func TestIISOutOfProcessParentChild(t *testing.T) {
	src := &iisFakeSource{paths: iisPoolPaths("1000_api")}
	src.readings = iisPoolReadings("1000_api", 3, 4, 5)
	iisWorkerIdent(src, "w3wp", 1000, 5)    // parent worker alive, created at 5
	iisWorkerIdent(src, "dotnet", 2000, 10) // child alive, created at 10
	g := newIISTestGroup(t, src, []ResolvedTarget{{Key: "pool:api", PID: 2000, Start: 10, Runtime: 2, Pool: "api"}}, nil)
	grp := collectIIS(t, g, time.Unix(1000, 0))
	if grp == nil || len(grp.Workers) != 1 {
		t.Fatalf("group = %+v, want 1 worker (out-of-process parent)", grp)
	}
	w := grp.Workers[0]
	if w.Pool != "api" || w.PID != 1000 {
		t.Fatalf("worker = %q pid %d, want api pid 1000 (the parent, not child 2000)", w.Pool, w.PID)
	}
	if w.ActiveRequests == nil || *w.ActiveRequests != 3 {
		t.Fatalf("ActiveRequests = %v, want 3", w.ActiveRequests)
	}
}

// A disappeared parent with a surviving child: the parent PID is no longer in
// the inventory, so its stale W3WP instance is rejected (no zero row).
func TestIISDisappearedParentRejected(t *testing.T) {
	src := &iisFakeSource{paths: iisPoolPaths("1000_api")}
	src.readings = iisPoolReadings("1000_api", 3, 4, 5)
	// Only the child survives in the inventory; the parent 1000 is gone.
	iisWorkerIdent(src, "dotnet", 2000, 10)
	g := newIISTestGroup(t, src, []ResolvedTarget{{Key: "pool:api", PID: 2000, Start: 10, Runtime: 2, Pool: "api"}}, nil)
	if grp := collectIIS(t, g, time.Unix(1000, 0)); grp != nil {
		t.Fatalf("group = %+v, want nil (parent worker no longer in inventory)", grp)
	}
}

// W5-03 source spy: an unselected pool's worker detail must never reach the
// engine, even though the source is read.
func TestIISUnselectedNotProcessed(t *testing.T) {
	src := &iisFakeSource{}
	src.paths = append(src.paths, iisPoolPaths("100_sel")...)
	src.paths = append(src.paths, iisPoolPaths("200_unsel")...)
	src.readings = append(src.readings, iisPoolReadings("100_sel", 1, 2, 3)...)
	src.readings = append(src.readings, iisPoolReadings("200_unsel", 9, 9, 9)...)
	iisWorkerIdent(src, "w3wp", 100, 10)
	iisWorkerIdent(src, "w3wp#1", 200, 10)
	g := newIISTestGroup(t, src, []ResolvedTarget{{Key: "pool:sel", PID: 100, Start: 10, Pool: "sel"}}, nil)
	// Source spy: the fake source serves BOTH pools' detail (it is in readings),
	// and the engine built a spec for the unselected pool (it was a candidate).
	if _, ok := g.engine.byPath[iisW3WPPath("200_unsel", "Active Requests")]; !ok {
		t.Fatal("unselected pool spec was not built (cannot prove it was filtered)")
	}
	grp := collectIIS(t, g, time.Unix(1000, 0))
	if grp == nil || len(grp.Workers) != 1 || grp.Workers[0].Pool != "sel" {
		t.Fatalf("group = %+v, want only the selected pool", grp)
	}
	// IIS counters are gauges (no rate history), so processing exclusion is
	// evidenced by the candidate spec being dropped from the output: the
	// unselected pool was read and built but never processed into a worker.
	for _, w := range grp.Workers {
		if w.Pool == "unsel" {
			t.Fatal("unselected pool detail was processed into a worker (W5-03)")
		}
	}
}
