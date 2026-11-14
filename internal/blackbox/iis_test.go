package blackbox

import (
	"testing"
	"time"
)

// iisFakeSource is a fake CounterSource with expansion bookkeeping: Paths
// feeds the spec build, RefreshInstances records re-expansion calls.
type iisFakeSource struct {
	paths     []string
	readings  []CounterReading
	refreshes int
	closed    bool
}

func (f *iisFakeSource) Collect() ([]CounterReading, error) { return f.readings, nil }
func (f *iisFakeSource) Close() error                       { f.closed = true; return nil }
func (f *iisFakeSource) Paths() []string                    { return f.paths }
func (f *iisFakeSource) RefreshInstances() error            { f.refreshes++; return nil }

func iisW3WPPath(inst, counter string) string { return `\W3SVC_W3WP(` + inst + `)\` + counter }
func iisWASPath(inst, counter string) string  { return `\WAS_W3WP(` + inst + `)\` + counter }

func iisReading(path string, v float64) CounterReading {
	return CounterReading{Path: path, Value: v, OK: true}
}

// iisPoolPaths expands all three counters for one "<PID>_<pool>" instance.
func iisPoolPaths(inst string) []string {
	return []string{
		iisW3WPPath(inst, "Active Requests"),
		iisW3WPPath(inst, "Active Threads"),
		iisWASPath(inst, "Ping Response Latency"),
	}
}

func iisPoolReadings(inst string, requests, threads, ping float64) []CounterReading {
	return []CounterReading{
		iisReading(iisW3WPPath(inst, "Active Requests"), requests),
		iisReading(iisW3WPPath(inst, "Active Threads"), threads),
		iisReading(iisWASPath(inst, "Ping Response Latency"), ping),
	}
}

func newIISTestGroup(t *testing.T, src *iisFakeSource, targets []ResolvedTarget, changed func() bool) *iisGroup {
	t.Helper()
	g, err := newIISGroup(GroupDeps{
		Source:            src,
		Cores:             4,
		Targets:           func() []ResolvedTarget { return targets },
		ProcessSetChanged: changed,
	})
	if err != nil {
		t.Fatalf("newIISGroup: %v", err)
	}
	return g.(*iisGroup)
}

func collectIIS(t *testing.T, g *iisGroup, now time.Time) *IISGroup {
	t.Helper()
	v, err := g.Collect(now, 15)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if v == nil {
		return nil
	}
	grp, ok := v.(*IISGroup)
	if !ok {
		t.Fatalf("Collect returned %T, want *IISGroup", v)
	}
	return grp
}

// Three pools have live instances, but only pool-b is selected: exactly one
// worker, and never a row for an unselected pool.
func TestIISGroupSelectedOnly(t *testing.T) {
	src := &iisFakeSource{}
	for _, inst := range []string{"100_pool-a", "200_pool-b", "300_pool-c"} {
		src.paths = append(src.paths, iisPoolPaths(inst)...)
	}
	src.readings = append(src.readings, iisPoolReadings("100_pool-a", 1, 10, 5)...)
	src.readings = append(src.readings, iisPoolReadings("200_pool-b", 2, 20, 6)...)
	src.readings = append(src.readings, iisPoolReadings("300_pool-c", 3, 30, 7)...)
	targets := []ResolvedTarget{{Key: "pool:pool-b", PID: 200, Start: 10, Pool: "pool-b"}}
	g := newIISTestGroup(t, src, targets, nil)
	grp := collectIIS(t, g, time.Unix(1000, 0))
	if grp == nil || len(grp.Workers) != 1 {
		t.Fatalf("group = %+v, want exactly 1 worker (selected pool only)", grp)
	}
	w := grp.Workers[0]
	if w.Pool != "pool-b" || w.PID != 200 {
		t.Fatalf("worker = %q pid %d, want pool-b pid 200", w.Pool, w.PID)
	}
	if w.ActiveRequests == nil || *w.ActiveRequests != 2 ||
		w.ActiveThreads == nil || *w.ActiveThreads != 20 ||
		w.PingLatencyMs == nil || *w.PingLatencyMs != 6 {
		t.Fatalf("worker fields = %+v, want 2/20/6", w)
	}
	// No selection at all: nothing sampled, never a zero row.
	gNone := newIISTestGroup(t, src, nil, nil)
	if grp := collectIIS(t, gNone, time.Unix(1000, 0)); grp != nil {
		t.Fatalf("no targets: group = %+v, want nil (owner-selected only)", grp)
	}
}

// Instances are matched by POOL NAME, not by the target PID: the resolver
// may retarget the app PID to a child (out-of-process .NET 10), and the
// instance PID is the w3wp worker PID. Every live instance of a selected
// pool is emitted with its own worker PID; Start confirms selection.
func TestIISGroupPoolMatch(t *testing.T) {
	src := &iisFakeSource{}
	src.paths = append(src.paths, iisPoolPaths("100_pool-a")...)
	src.paths = append(src.paths, iisPoolPaths("200_pool-a")...)
	src.readings = append(src.readings, iisPoolReadings("100_pool-a", 1, 2, 3)...)
	src.readings = append(src.readings, iisPoolReadings("200_pool-a", 7, 8, 9)...)
	// Target PID 999 is a retargeted child: neither instance PID equals it,
	// yet both instances of the selected pool must be emitted.
	targets := []ResolvedTarget{{Key: "pool:pool-a", PID: 999, Start: 10, Pool: "pool-a"}}
	g := newIISTestGroup(t, src, targets, nil)
	grp := collectIIS(t, g, time.Unix(1000, 0))
	if grp == nil || len(grp.Workers) != 2 {
		t.Fatalf("group = %+v, want 2 workers (pool-name match, not PID)", grp)
	}
	if grp.Workers[0].PID != 100 || grp.Workers[1].PID != 200 {
		t.Fatalf("worker PIDs = %d/%d, want 100/200 (the instance w3wp PIDs)", grp.Workers[0].PID, grp.Workers[1].PID)
	}
	if grp.Workers[1].ActiveRequests == nil || *grp.Workers[1].ActiveRequests != 7 {
		t.Fatalf("ActiveRequests = %v, want 7 from instance 200_pool-a", grp.Workers[1].ActiveRequests)
	}
	// A selected pool with no instance at all emits nothing.
	gNone := newIISTestGroup(t, src, []ResolvedTarget{{Key: "pool:pool-z", PID: 300, Start: 10, Pool: "pool-z"}}, nil)
	if grp := collectIIS(t, gNone, time.Unix(1000, 0)); grp != nil {
		t.Fatalf("no instance: group = %+v, want nil", grp)
	}
	// Unconfirmed creation time (Start 0): never selected.
	gNoStart := newIISTestGroup(t, src, []ResolvedTarget{{Key: "pool:pool-a", PID: 999, Start: 0, Pool: "pool-a"}}, nil)
	if grp := collectIIS(t, gNoStart, time.Unix(1000, 0)); grp != nil {
		t.Fatalf("Start 0: group = %+v, want nil (creation time unconfirmed)", grp)
	}
}

// Out-of-process .NET 10: the resolver retargets the runtime-0 w3wp to its
// runtime-2 child, so ResolvedTarget.PID is the CHILD PID while the
// W3SVC_W3WP instance stays "<w3wpPID>_<pool>". Matching is by pool name;
// the worker carries the instance's w3wp PID, never the child PID.
func TestIISGroupOutOfProcess(t *testing.T) {
	src := &iisFakeSource{paths: iisPoolPaths("1000_api")}
	src.readings = iisPoolReadings("1000_api", 3, 4, 5)
	// Child PID 2000 != instance PID 1000: must still match by pool name.
	targets := []ResolvedTarget{{Key: "pool:api", PID: 2000, Start: 10, Runtime: 2, Pool: "api"}}
	g := newIISTestGroup(t, src, targets, nil)
	grp := collectIIS(t, g, time.Unix(1000, 0))
	if grp == nil || len(grp.Workers) != 1 {
		t.Fatalf("group = %+v, want 1 worker (out-of-process pool)", grp)
	}
	w := grp.Workers[0]
	if w.Pool != "api" || w.PID != 1000 {
		t.Fatalf("worker = %q pid %d, want api pid 1000 (the w3wp instance)", w.Pool, w.PID)
	}
	if w.ActiveRequests == nil || *w.ActiveRequests != 3 ||
		w.ActiveThreads == nil || *w.ActiveThreads != 4 ||
		w.PingLatencyMs == nil || *w.PingLatencyMs != 5 {
		t.Fatalf("worker fields = %+v, want 3/4/5", w)
	}
}

// A pool name containing ')' is legal ("MyApp (Staging)"): the instance
// terminator is the ')\' separator (clr.go pattern), not the first ')'.
func TestIISGroupPoolNameWithParen(t *testing.T) {
	pid, pool, field, ok := parseIISPath(iisW3WPPath("1234_MyApp (Staging)", "Active Requests"))
	if !ok || pid != 1234 || pool != "MyApp (Staging)" || field != "activeRequests" {
		t.Fatalf("parseIISPath = %d %q %q %v, want 1234 \"MyApp (Staging)\" activeRequests true", pid, pool, field, ok)
	}
	src := &iisFakeSource{paths: iisPoolPaths("1234_MyApp (Staging)")}
	src.readings = iisPoolReadings("1234_MyApp (Staging)", 6, 7, 8)
	targets := []ResolvedTarget{{Key: "pool:MyApp (Staging)", PID: 1234, Start: 10, Pool: "MyApp (Staging)"}}
	g := newIISTestGroup(t, src, targets, nil)
	grp := collectIIS(t, g, time.Unix(1000, 0))
	if grp == nil || len(grp.Workers) != 1 {
		t.Fatalf("group = %+v, want 1 worker for the parenthesized pool", grp)
	}
	w := grp.Workers[0]
	if w.Pool != "MyApp (Staging)" || w.PID != 1234 {
		t.Fatalf("worker = %q pid %d, want \"MyApp (Staging)\" pid 1234", w.Pool, w.PID)
	}
	if w.ActiveRequests == nil || *w.ActiveRequests != 6 ||
		w.ActiveThreads == nil || *w.ActiveThreads != 7 ||
		w.PingLatencyMs == nil || *w.PingLatencyMs != 8 {
		t.Fatalf("worker fields = %+v, want 6/7/8", w)
	}
}

// An invalid counter leaves its field nil (never 0); all invalid → nil group.
func TestIISGroupMissingOmitted(t *testing.T) {
	inst := "100_pool-a"
	src := &iisFakeSource{paths: iisPoolPaths(inst)}
	src.readings = []CounterReading{
		iisReading(iisW3WPPath(inst, "Active Requests"), 5),
		{Path: iisW3WPPath(inst, "Active Threads"), Value: 99, Status: 1, OK: false},
		iisReading(iisWASPath(inst, "Ping Response Latency"), 2),
	}
	targets := []ResolvedTarget{{Key: "pool:pool-a", PID: 100, Start: 10, Pool: "pool-a"}}
	g := newIISTestGroup(t, src, targets, nil)
	grp := collectIIS(t, g, time.Unix(1000, 0))
	if grp == nil || len(grp.Workers) != 1 {
		t.Fatalf("group = %+v, want 1 worker", grp)
	}
	w := grp.Workers[0]
	if w.ActiveRequests == nil || *w.ActiveRequests != 5 {
		t.Fatalf("ActiveRequests = %v, want 5", w.ActiveRequests)
	}
	if w.ActiveThreads != nil {
		t.Fatalf("invalid counter emitted %v; missing must stay nil, never 0", *w.ActiveThreads)
	}
	if w.PingLatencyMs == nil || *w.PingLatencyMs != 2 {
		t.Fatalf("PingLatencyMs = %v, want 2", w.PingLatencyMs)
	}
	// Every reading invalid: the whole group is omitted, never zero-filled.
	for i := range src.readings {
		src.readings[i].OK = false
		src.readings[i].Status = 1
	}
	if grp := collectIIS(t, g, time.Unix(1015, 0)); grp != nil {
		t.Fatalf("all-invalid collect = %+v, want nil group", grp)
	}
}

// A selected pool with no live instance (stopped/recycled) emits no worker
// and never a zero row; a selected pool with no instance at all → nil group.
func TestIISGroupStoppedPool(t *testing.T) {
	src := &iisFakeSource{paths: iisPoolPaths("100_pool-a")}
	src.readings = iisPoolReadings("100_pool-a", 4, 5, 6)
	targets := []ResolvedTarget{
		{Key: "pool:pool-a", PID: 100, Start: 10, Pool: "pool-a"},
		{Key: "pool:pool-b", PID: 200, Start: 20, Pool: "pool-b"}, // stopped: no instance
	}
	g := newIISTestGroup(t, src, targets, nil)
	grp := collectIIS(t, g, time.Unix(1000, 0))
	if grp == nil || len(grp.Workers) != 1 {
		t.Fatalf("group = %+v, want exactly 1 worker (stopped pool omitted)", grp)
	}
	if grp.Workers[0].Pool != "pool-a" {
		t.Fatalf("worker = %q, want pool-a only (no zero row for pool-b)", grp.Workers[0].Pool)
	}
	// The only selected pool has no instance at all: nil, never a zero row.
	gNone := newIISTestGroup(t, src, []ResolvedTarget{{Key: "pool:pool-z", PID: 900, Start: 30, Pool: "pool-z"}}, nil)
	if grp := collectIIS(t, gNone, time.Unix(1000, 0)); grp != nil {
		t.Fatalf("no instance: group = %+v, want nil (stopped pool, no zero row)", grp)
	}
}

// Wildcards re-expand only when ProcessSetChanged() reports a change.
func TestIISGroupRefreshOnChange(t *testing.T) {
	src := &iisFakeSource{paths: iisPoolPaths("100_pool-a")}
	src.readings = iisPoolReadings("100_pool-a", 1, 2, 3)
	targets := []ResolvedTarget{
		{Key: "pool:pool-a", PID: 100, Start: 10, Pool: "pool-a"},
		{Key: "pool:pool-b", PID: 200, Start: 20, Pool: "pool-b"},
	}
	changed := false
	g := newIISTestGroup(t, src, targets, func() bool { return changed })
	now := time.Unix(1000, 0)
	if grp := collectIIS(t, g, now); grp == nil || len(grp.Workers) != 1 {
		t.Fatalf("group = %+v, want 1 worker before the change", grp)
	}
	if src.refreshes != 0 {
		t.Fatalf("refreshes = %d, want 0 without a process-set change", src.refreshes)
	}
	// The process set changes: pool-b's worker starts; re-expand once, pick it up.
	changed = true
	src.paths = append(src.paths, iisPoolPaths("200_pool-b")...)
	src.readings = append(src.readings, iisPoolReadings("200_pool-b", 4, 5, 6)...)
	grp := collectIIS(t, g, now.Add(15*time.Second))
	if src.refreshes != 1 {
		t.Fatalf("refreshes = %d, want 1 on change", src.refreshes)
	}
	if grp == nil || len(grp.Workers) != 2 {
		t.Fatalf("group = %+v, want 2 workers after re-expansion", grp)
	}
	changed = false
	collectIIS(t, g, now.Add(30*time.Second))
	if src.refreshes != 1 {
		t.Fatalf("refreshes = %d, want no re-expand without a change", src.refreshes)
	}
}

// The group is registered optional (ships off when it breaks budget).
func TestIISGroupRegistered(t *testing.T) {
	registryMu.Lock()
	reg, ok := registry["iis"]
	registryMu.Unlock()
	if !ok {
		t.Fatal("iis group not registered from init()")
	}
	if !reg.optional {
		t.Fatal("iis group must be optional (true)")
	}
}
