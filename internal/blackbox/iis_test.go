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

// The instance PID must match the resolved target PID: a stale instance for
// the same pool name but a different PID is ignored. Start confirms identity.
func TestIISGroupPIDMatch(t *testing.T) {
	src := &iisFakeSource{}
	src.paths = append(src.paths, iisPoolPaths("100_pool-a")...) // stale worker
	src.paths = append(src.paths, iisPoolPaths("200_pool-a")...) // live worker
	src.readings = append(src.readings, iisPoolReadings("100_pool-a", 99, 99, 99)...)
	src.readings = append(src.readings, iisPoolReadings("200_pool-a", 7, 8, 9)...)
	targets := []ResolvedTarget{{Key: "pool:pool-a", PID: 200, Start: 10, Pool: "pool-a"}}
	g := newIISTestGroup(t, src, targets, nil)
	grp := collectIIS(t, g, time.Unix(1000, 0))
	if grp == nil || len(grp.Workers) != 1 {
		t.Fatalf("group = %+v, want 1 worker (stale PID ignored)", grp)
	}
	w := grp.Workers[0]
	if w.PID != 200 {
		t.Fatalf("worker PID = %d, want 200 (the resolved target)", w.PID)
	}
	if w.ActiveRequests == nil || *w.ActiveRequests != 7 {
		t.Fatalf("ActiveRequests = %v, want 7 from the live instance (stale 99 ignored)", w.ActiveRequests)
	}
	// A target PID with no matching instance emits nothing, even though the
	// pool name matches an instance.
	gStale := newIISTestGroup(t, src, []ResolvedTarget{{Key: "pool:pool-a", PID: 300, Start: 10, Pool: "pool-a"}}, nil)
	if grp := collectIIS(t, gStale, time.Unix(1000, 0)); grp != nil {
		t.Fatalf("unmatched PID: group = %+v, want nil", grp)
	}
	// Unconfirmed creation time (Start 0): never keyed on PID alone.
	gNoStart := newIISTestGroup(t, src, []ResolvedTarget{{Key: "pool:pool-a", PID: 200, Start: 0, Pool: "pool-a"}}, nil)
	if grp := collectIIS(t, gNoStart, time.Unix(1000, 0)); grp != nil {
		t.Fatalf("Start 0: group = %+v, want nil (creation time unconfirmed)", grp)
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
