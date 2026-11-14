package blackbox

import (
	"testing"
	"time"
)

// httpFakeSource is a fake CounterSource with expansion bookkeeping: Paths
// feeds the spec build, RefreshInstances records re-expansion calls.
type httpFakeSource struct {
	paths     []string
	readings  []CounterReading
	refreshes int
	closed    bool
}

func (f *httpFakeSource) Collect() ([]CounterReading, error) { return f.readings, nil }
func (f *httpFakeSource) Close() error                       { f.closed = true; return nil }
func (f *httpFakeSource) Paths() []string                    { return f.paths }
func (f *httpFakeSource) RefreshInstances() error            { f.refreshes++; return nil }

func httpPath(pool, counter string) string {
	return `\HTTP Service Request Queues(` + pool + `)\` + counter
}

func httpReading(pool, counter string, v float64) CounterReading {
	return CounterReading{Path: httpPath(pool, counter), Value: v, OK: true}
}

func newHTTPTestGroup(t *testing.T, src *httpFakeSource, changed func() bool) *httpGroup {
	t.Helper()
	g, err := newHTTPGroup(GroupDeps{Source: src, Cores: 4, ProcessSetChanged: changed})
	if err != nil {
		t.Fatalf("newHTTPGroup: %v", err)
	}
	return g.(*httpGroup)
}

func TestHTTPGroupMapsPools(t *testing.T) {
	src := &httpFakeSource{}
	for _, pool := range []string{"pool-a", "pool-b"} {
		for _, c := range []string{"CurrentQueueSize", "MaxQueueItemAge", "RejectedRequests", "ArrivalRate"} {
			src.paths = append(src.paths, httpPath(pool, c))
		}
	}
	src.readings = []CounterReading{
		httpReading("pool-a", "CurrentQueueSize", 3),
		httpReading("pool-a", "MaxQueueItemAge", 120),
		httpReading("pool-a", "RejectedRequests", 2),
		httpReading("pool-a", "ArrivalRate", 50),
		httpReading("pool-b", "CurrentQueueSize", 7),
		httpReading("pool-b", "MaxQueueItemAge", 40),
		httpReading("pool-b", "RejectedRequests", 1),
		httpReading("pool-b", "ArrivalRate", 90),
	}
	// Machine-wide: Targets lists only pool-a, but every queue must appear.
	gi, err := newHTTPGroup(GroupDeps{Source: src, Cores: 4, Targets: func() []ResolvedTarget {
		return []ResolvedTarget{{Key: "pool:pool-a", Pool: "pool-a"}}
	}})
	if err != nil {
		t.Fatalf("newHTTPGroup: %v", err)
	}
	g := gi.(*httpGroup)
	now := time.Unix(1000, 0)
	var grp *HTTPGroup
	for i := 0; i < 3; i++ { // two baselines, then the rates emit
		v, err := g.Collect(now.Add(time.Duration(i)*15*time.Second), 15)
		if err != nil {
			t.Fatalf("tick %d: %v", i+1, err)
		}
		grp, _ = v.(*HTTPGroup)
		if grp == nil {
			t.Fatalf("tick %d: nil group with valid readings", i+1)
		}
	}
	if len(grp.Queues) != 2 {
		t.Fatalf("queues = %d, want 2 (machine-wide, not filtered to Targets)", len(grp.Queues))
	}
	a, b := grp.Queues[0], grp.Queues[1]
	if a.Pool != "pool-a" || b.Pool != "pool-b" {
		t.Fatalf("pools = %q,%q; want pool-a,pool-b", a.Pool, b.Pool)
	}
	check := func(name string, got *float64, want float64) {
		t.Helper()
		if got == nil || *got != want {
			t.Errorf("%s = %v, want %v", name, got, want)
		}
	}
	check("a.CurrentQueueSize", a.CurrentQueueSize, 3)
	check("a.MaxQueueItemAge", a.MaxQueueItemAge, 120)
	check("a.RejectedRequests", a.RejectedRequests, 2)
	check("a.ArrivalRate", a.ArrivalRate, 50)
	check("b.CurrentQueueSize", b.CurrentQueueSize, 7)
	check("b.MaxQueueItemAge", b.MaxQueueItemAge, 40)
	check("b.RejectedRequests", b.RejectedRequests, 1)
	check("b.ArrivalRate", b.ArrivalRate, 90)
}

func TestHTTPGroupMissingOmitted(t *testing.T) {
	src := &httpFakeSource{
		paths: []string{
			httpPath("pool-a", "CurrentQueueSize"),
			httpPath("pool-a", "MaxQueueItemAge"),
			httpPath("pool-b", "CurrentQueueSize"),
		},
		readings: []CounterReading{
			httpReading("pool-a", "CurrentQueueSize", 5),
			{Path: httpPath("pool-a", "MaxQueueItemAge"), Value: 99, Status: 1, OK: false},
			{Path: httpPath("pool-b", "CurrentQueueSize"), Value: 4, Status: 1, OK: false},
		},
	}
	g := newHTTPTestGroup(t, src, nil)
	v, err := g.Collect(time.Unix(1000, 0), 0)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	grp := v.(*HTTPGroup)
	if len(grp.Queues) != 1 {
		t.Fatalf("queues = %d, want 1 (pool with no valid field omitted)", len(grp.Queues))
	}
	q := grp.Queues[0]
	if q.Pool != "pool-a" {
		t.Fatalf("pool = %q, want pool-a", q.Pool)
	}
	if q.CurrentQueueSize == nil || *q.CurrentQueueSize != 5 {
		t.Fatalf("CurrentQueueSize = %v, want 5", q.CurrentQueueSize)
	}
	if q.MaxQueueItemAge != nil {
		t.Fatalf("invalid counter emitted %v; missing must stay nil, never 0", *q.MaxQueueItemAge)
	}
}

func TestHTTPGroupRateGated(t *testing.T) {
	src := &httpFakeSource{
		paths: []string{
			httpPath("pool-a", "CurrentQueueSize"),
			httpPath("pool-a", "ArrivalRate"),
			httpPath("pool-a", "RejectedRequests"),
		},
		readings: []CounterReading{
			httpReading("pool-a", "CurrentQueueSize", 1),
			httpReading("pool-a", "ArrivalRate", 50),
			httpReading("pool-a", "RejectedRequests", 2),
		},
	}
	g := newHTTPTestGroup(t, src, nil)
	now := time.Unix(1000, 0)
	for i := 0; i < 2; i++ {
		v, err := g.Collect(now.Add(time.Duration(i)*15*time.Second), 15)
		if err != nil {
			t.Fatalf("tick %d: %v", i+1, err)
		}
		q := v.(*HTTPGroup).Queues[0]
		if q.ArrivalRate != nil || q.RejectedRequests != nil {
			t.Fatalf("tick %d: rate emitted before two baselines", i+1)
		}
		if q.CurrentQueueSize == nil {
			t.Fatalf("tick %d: gauge absent", i+1)
		}
	}
	v, err := g.Collect(now.Add(30*time.Second), 15)
	if err != nil {
		t.Fatalf("tick 3: %v", err)
	}
	q := v.(*HTTPGroup).Queues[0]
	if q.ArrivalRate == nil || *q.ArrivalRate != 50 {
		t.Fatalf("ArrivalRate = %v, want 50 after two baselines", q.ArrivalRate)
	}
	if q.RejectedRequests == nil || *q.RejectedRequests != 2 {
		t.Fatalf("RejectedRequests = %v, want 2 after two baselines", q.RejectedRequests)
	}
}

func TestHTTPGroupEmptyNil(t *testing.T) {
	src := &httpFakeSource{
		paths:    []string{httpPath("pool-a", "CurrentQueueSize")},
		readings: []CounterReading{{Path: httpPath("pool-a", "CurrentQueueSize"), Value: 5, Status: 1, OK: false}},
	}
	g := newHTTPTestGroup(t, src, nil)
	v, err := g.Collect(time.Unix(1000, 0), 0)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if v != nil {
		t.Fatalf("group = %v, want nil when nothing is valid", v)
	}
	src.readings = nil // no readings at all: still nil, nil
	v, err = g.Collect(time.Unix(1015, 0), 15)
	if err != nil || v != nil {
		t.Fatalf("empty readings: (%v, %v), want (nil, nil)", v, err)
	}
}

func TestHTTPGroupRefreshOnChange(t *testing.T) {
	src := &httpFakeSource{
		paths:    []string{httpPath("pool-a", "CurrentQueueSize")},
		readings: []CounterReading{httpReading("pool-a", "CurrentQueueSize", 1)},
	}
	changed := false
	g := newHTTPTestGroup(t, src, func() bool { return changed })
	now := time.Unix(1000, 0)
	if _, err := g.Collect(now, 0); err != nil {
		t.Fatal(err)
	}
	if src.refreshes != 0 {
		t.Fatalf("refreshes = %d, want 0 without a process-set change", src.refreshes)
	}
	// The process set changes: a new pool appears; re-expand once, pick it up.
	changed = true
	src.paths = append(src.paths, httpPath("pool-b", "CurrentQueueSize"))
	src.readings = append(src.readings, httpReading("pool-b", "CurrentQueueSize", 2))
	v, err := g.Collect(now.Add(15*time.Second), 15)
	if err != nil {
		t.Fatal(err)
	}
	if src.refreshes != 1 {
		t.Fatalf("refreshes = %d, want 1 on change", src.refreshes)
	}
	if grp := v.(*HTTPGroup); len(grp.Queues) != 2 {
		t.Fatalf("queues = %d, want 2 after re-expansion", len(grp.Queues))
	}
	changed = false
	if _, err := g.Collect(now.Add(30*time.Second), 15); err != nil {
		t.Fatal(err)
	}
	if src.refreshes != 1 {
		t.Fatalf("refreshes = %d, want no re-expand without a change", src.refreshes)
	}
}

func TestHTTPGroupRegistered(t *testing.T) {
	registryMu.Lock()
	reg, ok := registry["http"]
	registryMu.Unlock()
	if !ok {
		t.Fatal("http group not registered from init()")
	}
	if !reg.optional {
		t.Fatal("http group must be optional (ships off when it breaks budget)")
	}
}