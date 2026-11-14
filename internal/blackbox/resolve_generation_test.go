package blackbox

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// This file covers the wave-3 resolver fixes:
//   W3-02 pending runtime retried on a bounded schedule (empty->Framework/CoreCLR,
//         denied->readable) independent of process-set change;
//   W3-03 monotonic generation delivered to every group across skip/change/stable/
//         resume cycles, retained through failed refreshes, acked only on success;
//   W3-04 lossless same-second identity invalidates the cache and refreshes;
//   W3-05 one resolver process snapshot per tick regardless of consumer count.
// All tests use the injected snapshot/module/enumerator seams; no Windows needed.

// TestSameSecondPIDReuseInvalidatesCache guards W3-04: two lifetimes with the
// SAME PID and the SAME unix second but different FILETIME ticks are distinct
// identities. The cache must invalidate (re-read modules), the resolved runtime
// must refresh, the process set must report a change, and the generation must
// advance — none of which a seconds-only key would do.
func TestSameSecondPIDReuseInvalidatesCache(t *testing.T) {
	lifeA := ProcInfo{PID: 4321, Start: 1000, StartTicks: 13_000_000_000_000_000, Name: "w3wp.exe"}
	lifeB := ProcInfo{PID: 4321, Start: 1000, StartTicks: 13_000_000_000_005_000, Name: "w3wp.exe"}
	if lifeA.Start != lifeB.Start {
		t.Fatalf("fixture must share a unix second: %d vs %d", lifeA.Start, lifeB.Start)
	}
	if identityTicks(lifeA) == identityTicks(lifeB) {
		t.Fatal("same-second lifetimes alias: identity must use lossless ticks")
	}
	if !processSetChanged(keysOf([]ProcInfo{lifeA}), keysOf([]ProcInfo{lifeB})) {
		t.Fatal("a same-second recycle must be a process-set change")
	}

	cur := lifeA
	var loads []uint32
	r := NewResolver()
	r.snapshotFn = func() ([]ProcInfo, error) { return []ProcInfo{cur}, nil }
	r.loadModulesFn = func(pid uint32) ([]string, error) {
		loads = append(loads, pid)
		if cur.StartTicks == lifeA.StartTicks {
			return []string{"clr.dll"}, nil
		}
		return []string{"coreclr.dll"}, nil
	}
	r.instancesFn = func() ([]string, error) { return []string{"4321_Pool"}, nil }
	r.servicesFn = func() ([]svcProc, error) { return nil, nil }
	targets := []Target{{Kind: "appPool", Name: "Pool"}}

	first, missing := r.ResolveTargets(targets)
	if len(first) != 1 || len(missing) != 0 || first[0].Runtime != 1 {
		t.Fatalf("first resolve = %+v / %v, want one Framework target", first, missing)
	}
	gen1 := r.Generation()

	cur = lifeB // recycled within the same second
	loads = nil
	second, missing := r.ResolveTargets(targets)
	if len(second) != 1 || len(missing) != 0 {
		t.Fatalf("second resolve = %+v / %v, want one target", second, missing)
	}
	if second[0].Runtime != 2 {
		t.Fatalf("same-second recycle kept stale runtime %d, want 2 (cache must invalidate)", second[0].Runtime)
	}
	if len(loads) != 1 || loads[0] != 4321 {
		t.Fatalf("same-second recycle must re-read modules once, got %v", loads)
	}
	if first[0].Start != second[0].Start {
		t.Fatalf("Start stays unix seconds for consumers: %d vs %d", first[0].Start, second[0].Start)
	}
	if r.Generation() <= gen1 {
		t.Fatalf("generation did not advance on same-second recycle: %d -> %d", gen1, r.Generation())
	}
}

// TestPendingRuntimeRecoversOnBoundedRetry guards W3-02: a successful module
// read with no CLR module yet is PENDING, not a permanent runtime 0. With a
// stable identity it is retried on a bounded schedule (not every tick) and
// recovers when the runtime loads — empty->Framework and empty->CoreCLR.
func TestPendingRuntimeRecoversOnBoundedRetry(t *testing.T) {
	for _, tc := range []struct {
		name string
		mods []string
		want uint8
	}{
		{"empty to Framework", []string{"clr.dll"}, 1},
		{"empty to CoreCLR", []string{"coreclr.dll"}, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Unix(1000, 0)
			loaded := false
			var loads int
			r := NewResolver()
			r.nowFn = func() time.Time { return now }
			r.snapshotFn = func() ([]ProcInfo, error) {
				return []ProcInfo{{PID: 7, Start: 50, StartTicks: 500_000_000, Name: "app.exe"}}, nil
			}
			r.loadModulesFn = func(uint32) ([]string, error) {
				loads++
				if !loaded {
					return []string{"kernel32.dll", "ntdll.dll"}, nil // no CLR yet
				}
				return tc.mods, nil
			}

			got, err := r.Inventory()
			if err != nil {
				t.Fatal(err)
			}
			if got[0].Runtime != 0 || loads != 1 {
				t.Fatalf("first read: runtime %d loads %d, want 0 / 1 (pending)", got[0].Runtime, loads)
			}
			// Stable identity, retry not due: no re-read, still pending.
			now = now.Add(runtimeRetryEvery - time.Second)
			if got, err = r.Inventory(); err != nil {
				t.Fatal(err)
			}
			if loads != 1 || got[0].Runtime != 0 {
				t.Fatalf("before retry due: loads %d runtime %d, want 1 / 0 (no per-tick scan)", loads, got[0].Runtime)
			}
			// Runtime loads and the retry comes due: recovered within the window.
			loaded = true
			now = now.Add(2 * time.Second)
			if got, err = r.Inventory(); err != nil {
				t.Fatal(err)
			}
			if got[0].Runtime != tc.want || loads != 2 {
				t.Fatalf("recovery: runtime %d loads %d, want %d / 2", got[0].Runtime, loads, tc.want)
			}
			// Confirmed runtime is cached: a later stable inventory reads nothing.
			now = now.Add(time.Hour)
			if got, err = r.Inventory(); err != nil {
				t.Fatal(err)
			}
			if loads != 2 || got[0].Runtime != tc.want {
				t.Fatalf("confirmed runtime re-read: loads %d runtime %d, want 2 / %d", loads, got[0].Runtime, tc.want)
			}
		})
	}
}

// TestDeniedRuntimeRecoversOnBoundedRetry guards W3-02: an access-denied module
// read leaves runtime 0 (pending) and recovers on the bounded retry once the
// process becomes readable, without any process-set change.
func TestDeniedRuntimeRecoversOnBoundedRetry(t *testing.T) {
	now := time.Unix(2000, 0)
	denied := true
	var loads int
	r := NewResolver()
	r.nowFn = func() time.Time { return now }
	r.snapshotFn = func() ([]ProcInfo, error) {
		return []ProcInfo{{PID: 9, Start: 60, StartTicks: 600_000_000, Name: "protected.exe"}}, nil
	}
	r.loadModulesFn = func(uint32) ([]string, error) {
		loads++
		if denied {
			return nil, errors.New("access denied")
		}
		return []string{"coreclr.dll"}, nil
	}
	got, err := r.Inventory()
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Runtime != 0 || loads != 1 {
		t.Fatalf("denied: runtime %d loads %d, want 0 / 1", got[0].Runtime, loads)
	}
	now = now.Add(runtimeRetryEvery - time.Second)
	if _, err = r.Inventory(); err != nil {
		t.Fatal(err)
	}
	if loads != 1 {
		t.Fatalf("denied retry before due: loads %d, want 1", loads)
	}
	denied = false
	now = now.Add(2 * time.Second)
	if got, err = r.Inventory(); err != nil {
		t.Fatal(err)
	}
	if got[0].Runtime != 2 || loads != 2 {
		t.Fatalf("denied->readable recovery: runtime %d loads %d, want 2 / 2", got[0].Runtime, loads)
	}
}

// snapshotConsumerHost reads the shared targets once per tick, like the host
// collector's selected-target listing.
type snapshotConsumerHost struct {
	targets func() []ResolvedTarget
	calls   int
}

func (h *snapshotConsumerHost) Collect(time.Time) (Sample, error) {
	h.calls++
	_ = h.targets()
	return Sample{}, nil
}
func (h *snapshotConsumerHost) Close() error { return nil }

// snapshotConsumerGroup reads the shared targets twice and the change verdict
// once per tick, like clr/proc10/procio (two target reads) sharing the gate.
type snapshotConsumerGroup struct {
	name        string
	targets     func() []ResolvedTarget
	changed     func() bool
	targetCalls int
	seen        int
}

func (g *snapshotConsumerGroup) Name() string   { return g.name }
func (g *snapshotConsumerGroup) Optional() bool { return true }
func (g *snapshotConsumerGroup) Collect(time.Time, float64) (any, error) {
	for i := 0; i < 2; i++ {
		g.targetCalls++
		g.seen = len(g.targets())
	}
	if g.changed != nil {
		_ = g.changed()
	}
	return &TCPGroup{}, nil
}
func (g *snapshotConsumerGroup) Close() error { return nil }

// TestOneResolverSnapshotPerTick guards W3-05: the composed gate + host + groups
// path takes exactly ONE resolver process snapshot per tick regardless of how
// many consumers read targets, because they all share one immutable view.
func TestOneResolverSnapshotPerTick(t *testing.T) {
	var snaps int64
	procs := []ProcInfo{{PID: 100, ParentPID: 4, Start: 1000, StartTicks: 10_000_000_000, Name: "w3wp.exe"}}
	r := NewResolver()
	r.snapshotFn = func() ([]ProcInfo, error) {
		atomic.AddInt64(&snaps, 1)
		out := make([]ProcInfo, len(procs))
		copy(out, procs)
		return out, nil
	}
	r.loadModulesFn = func(uint32) ([]string, error) { return []string{"clr.dll"}, nil }
	r.instancesFn = func() ([]string, error) { return []string{"100_Pool"}, nil }
	r.servicesFn = func() ([]svcProc, error) { return nil, nil }

	gate := &processChangeGate{resolver: r, targets: []Target{{Kind: "appPool", Name: "Pool"}}}
	gate.view.Store(r.resolveTick(gate.targets, time.Now())) // seed (one snapshot)
	targets := func() []ResolvedTarget { return gate.currentView().targets }
	changed := func() bool { return gate.currentView().gen > 0 }

	host := &snapshotConsumerHost{targets: targets}
	groups := []Group{
		&snapshotConsumerGroup{name: "clr", targets: targets, changed: changed},
		&snapshotConsumerGroup{name: "proc10", targets: targets, changed: changed},
		&snapshotConsumerGroup{name: "procIO", targets: targets, changed: changed},
		&snapshotConsumerGroup{name: "iis", targets: targets, changed: changed},
	}
	gate.inner = NewGroupCollector(host, groups, NewBudget(15))

	for tick := 0; tick < 3; tick++ {
		before := atomic.LoadInt64(&snaps)
		if _, err := gate.Collect(time.Now()); err != nil {
			t.Fatal(err)
		}
		if got := atomic.LoadInt64(&snaps) - before; got != 1 {
			t.Fatalf("tick %d: %d resolver snapshots, want 1 regardless of consumer count", tick, got)
		}
	}
	if host.calls != 3 {
		t.Fatalf("host collected %d times, want 3", host.calls)
	}
	for _, g := range groups {
		cg := g.(*snapshotConsumerGroup)
		if cg.targetCalls != 6 || cg.seen != 1 {
			t.Fatalf("%s: targetCalls %d seen %d, want 6 / 1 (shared view delivered)", cg.name, cg.targetCalls, cg.seen)
		}
	}
}

// genFakeGroup refreshes when its generation is dirty, recording the generation
// it reconciled. fail makes the refresh error so the ack must not advance.
type genFakeGroup struct {
	name      string
	optional  bool
	tracker   *changeTracker
	fail      bool
	refreshes int
	dirtySeen int
	lastGen   uint64
}

func (g *genFakeGroup) Name() string   { return g.name }
func (g *genFakeGroup) Optional() bool { return g.optional }
func (g *genFakeGroup) Collect(time.Time, float64) (any, error) {
	if g.tracker.dirty(g.name) {
		g.dirtySeen++
		if g.fail {
			return nil, errors.New("refresh failed")
		}
		g.refreshes++
		g.lastGen = g.tracker.current()
	}
	return &TCPGroup{}, nil
}
func (g *genFakeGroup) Close() error { return nil }

// newGenRig wires a resolver-backed gate, a changeTracker and trackedGroup-wrapped
// fake groups under a real groupCollector/budget, so the generation contract is
// exercised through the same path RunHost uses. The returned *[]ProcInfo is the
// fake process set the caller mutates between ticks to drive generation changes.
func newGenRig(t *testing.T, optionalNames []string, mandatory string) (*processChangeGate, *changeTracker, *Budget, map[string]*genFakeGroup, *[]ProcInfo) {
	t.Helper()
	procs := []ProcInfo{{PID: 900, Start: 9000, StartTicks: 90_000_000_000, Name: "svc.exe"}}
	r := NewResolver()
	r.snapshotFn = func() ([]ProcInfo, error) {
		out := make([]ProcInfo, len(procs))
		copy(out, procs)
		return out, nil
	}
	r.loadModulesFn = func(uint32) ([]string, error) { return []string{"clr.dll"}, nil }
	r.instancesFn = func() ([]string, error) { return nil, nil }
	r.servicesFn = func() ([]svcProc, error) { return []svcProc{{name: "Svc", pid: 900}}, nil }
	gate := &processChangeGate{resolver: r, targets: []Target{{Kind: "service", Name: "Svc"}}}
	gate.view.Store(r.resolveTick(gate.targets, time.Now()))
	tracker := &changeTracker{current: func() uint64 { return gate.currentView().gen }, acked: make(map[string]uint64)}

	fakes := make(map[string]*genFakeGroup)
	var groups []Group
	for _, name := range optionalNames {
		f := &genFakeGroup{name: name, optional: true, tracker: tracker}
		fakes[name] = f
		groups = append(groups, &trackedGroup{name: name, tracker: tracker, inner: f})
	}
	if mandatory != "" {
		f := &genFakeGroup{name: mandatory, optional: false, tracker: tracker}
		fakes[mandatory] = f
		groups = append(groups, &trackedGroup{name: mandatory, tracker: tracker, inner: f})
	}
	b := NewBudget(15)
	gate.inner = NewGroupCollector(&stubCollector{}, groups, b)
	return gate, tracker, b, fakes, &procs
}

// TestGenerationDeliveredAcrossSkips guards W3-03: over 20 skip/change/stable/
// resume cycles, a change that happens DURING a budget skip is delivered to every
// optional group on resume — the newest generation, without a second change.
func TestGenerationDeliveredAcrossSkips(t *testing.T) {
	gate, tracker, b, fakes, procs := newGenRig(t, []string{"optA", "optB", "optC"}, "mand")
	now := time.Unix(1000, 0)
	nextPID := uint32(1000)
	optional := []*genFakeGroup{fakes["optA"], fakes["optB"], fakes["optC"]}
	all := []*genFakeGroup{fakes["optA"], fakes["optB"], fakes["optC"], fakes["mand"]}

	for round := 0; round < 20; round++ {
		// 1) Skip optional groups and change the process set during the skip.
		b.skipOptional = true
		*procs = append(*procs, ProcInfo{PID: nextPID, Start: int64(nextPID), StartTicks: int64(nextPID) * 10_000_000, Name: "extra.exe"})
		nextPID++
		now = now.Add(15 * time.Second)
		if _, err := gate.Collect(now); err != nil {
			t.Fatal(err)
		}
		genDuringSkip := gate.currentView().gen
		for _, g := range optional {
			if !tracker.dirty(g.name) {
				t.Fatalf("round %d: %s not dirty after a change during a skip", round, g.name)
			}
		}
		// 2) A stable skipped cycle: generation unchanged, optional still dirty.
		now = now.Add(15 * time.Second)
		if _, err := gate.Collect(now); err != nil {
			t.Fatal(err)
		}
		if gate.currentView().gen != genDuringSkip {
			t.Fatalf("round %d: a stable cycle advanced the generation", round)
		}
		// 3) Resume: optional groups run and must reconcile the newest generation
		//    WITHOUT any second process change.
		b.skipOptional = false
		now = now.Add(15 * time.Second)
		if _, err := gate.Collect(now); err != nil {
			t.Fatal(err)
		}
		newest := gate.currentView().gen
		for _, g := range all {
			if g.lastGen != newest {
				t.Fatalf("round %d: %s reconciled gen %d, want newest %d", round, g.name, g.lastGen, newest)
			}
			if tracker.dirty(g.name) {
				t.Fatalf("round %d: %s still dirty after a successful resume", round, g.name)
			}
		}
		for _, g := range optional {
			if g.refreshes != round+1 {
				t.Fatalf("round %d: %s refreshed %d times, want %d (one delivery per round)", round, g.name, g.refreshes, round+1)
			}
		}
	}
}

// TestFailedRefreshRetainsGeneration guards W3-03: a failed refresh does not ack,
// so the change stays pending and is delivered on a later stable cycle without a
// second process change.
func TestFailedRefreshRetainsGeneration(t *testing.T) {
	gate, tracker, b, fakes, procs := newGenRig(t, []string{"opt"}, "")
	g := fakes["opt"]
	now := time.Unix(1000, 0)

	// Change during a skip.
	b.skipOptional = true
	*procs = append(*procs, ProcInfo{PID: 1000, Start: 1000, StartTicks: 10_000_000_000, Name: "extra.exe"})
	now = now.Add(15 * time.Second)
	if _, err := gate.Collect(now); err != nil {
		t.Fatal(err)
	}
	// Resume, but the refresh fails: no ack, still dirty.
	b.skipOptional = false
	g.fail = true
	now = now.Add(15 * time.Second)
	if _, err := gate.Collect(now); err != nil {
		t.Fatal(err)
	}
	if !tracker.dirty("opt") {
		t.Fatal("a failed refresh must retain the pending generation")
	}
	if g.refreshes != 0 {
		t.Fatalf("failed refresh counted as success: %d", g.refreshes)
	}
	// A later stable cycle (no second change) retries and delivers the generation.
	g.fail = false
	now = now.Add(15 * time.Second)
	if _, err := gate.Collect(now); err != nil {
		t.Fatal(err)
	}
	if tracker.dirty("opt") {
		t.Fatal("a successful retry must ack the generation")
	}
	if g.lastGen != gate.currentView().gen || g.refreshes != 1 {
		t.Fatalf("retry: lastGen %d refreshes %d, want gen %d / 1", g.lastGen, g.refreshes, gate.currentView().gen)
	}
}

// TestChangeTrackerAckOnlyOnSuccess guards the tracker contract: dirty until the
// newest generation is acked, ack never moves backwards, and a newer generation
// re-dirties an acked group.
func TestChangeTrackerAckOnlyOnSuccess(t *testing.T) {
	var gen uint64 = 5
	tr := &changeTracker{current: func() uint64 { return gen }, acked: make(map[string]uint64)}
	if !tr.dirty("g") {
		t.Fatal("unacked group must be dirty")
	}
	tr.ack("g", 5)
	if tr.dirty("g") {
		t.Fatal("acked generation must not be dirty")
	}
	gen = 6
	if !tr.dirty("g") {
		t.Fatal("a newer generation must re-dirty an acked group")
	}
	tr.ack("g", 4) // a late worker acking an older generation
	if !tr.dirty("g") {
		t.Fatal("an older ack must not clear a newer dirty state")
	}
	tr.ack("g", 6)
	if tr.dirty("g") {
		t.Fatal("acking the newest generation must clear dirty")
	}
}
