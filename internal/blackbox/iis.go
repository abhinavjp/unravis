package blackbox

import (
	"sort"
	"strconv"
	"strings"
	"time"
)

// BBX-2-02 IIS worker counters for SELECTED app pools only: W3SVC_W3WP
// active requests/threads and WAS_W3WP ping latency. Instances are
// "<PID>_<AppPoolName>"; a worker is keyed to the resolver-confirmed
// (PID, Start) of its selected pool. English paths only; the WAS counter
// name is UNVERIFIED pending the live typeperf check.
var iisPaths = []string{
	`\W3SVC_W3WP(*)\Active Requests`,
	`\W3SVC_W3WP(*)\Active Threads`,
	`\WAS_W3WP(*)\Ping Response Latency`,
}

// iisCounters binds each object prefix + counter to its json field. All
// three are gauges: nothing here is a rate, so no baseline gate applies.
var iisCounters = []struct {
	prefix  string
	counter string
	field   string
}{
	{`\W3SVC_W3WP(`, "Active Requests", "activeRequests"},
	{`\W3SVC_W3WP(`, "Active Threads", "activeThreads"},
	{`\WAS_W3WP(`, "Ping Response Latency", "pingLatencyMs"},
}

func init() { RegisterGroup("iis", true, newIISGroup) }

// iisCounter binds one expanded path to its worker identity and field. The
// key is the engine output key (the full path: unique per instance+counter).
type iisCounter struct {
	key   string
	pid   uint32
	pool  string
	field string
}

type iisGroup struct {
	src      CounterSource
	engine   *Engine
	counters []iisCounter
	targets  func() []ResolvedTarget
	changed  func() bool
	cores    int
	owned    bool // factory created the source: Close owns it
}

func newIISGroup(d GroupDeps) (Group, error) {
	src, owned := d.Source, false
	if src == nil {
		p, err := NewPDHSource(iisPaths)
		if err != nil {
			return nil, err // no PDH (non-Windows) or no IIS: skipped
		}
		src, owned = p, true
	}
	g := &iisGroup{src: src, targets: d.Targets, changed: d.ProcessSetChanged, cores: d.Cores, owned: owned}
	g.rebuild(iisPathsOf(src))
	return g, nil
}

func (g *iisGroup) Name() string   { return "iis" }
func (g *iisGroup) Optional() bool { return true }

// iisPathsOf reads the expanded paths when the source exposes them.
func iisPathsOf(src CounterSource) []string {
	if p, ok := src.(interface{ Paths() []string }); ok {
		return p.Paths()
	}
	return nil
}

// rebuild derives per-instance specs from the expanded paths and a fresh
// engine: new instances start their two-baseline gate again.
func (g *iisGroup) rebuild(paths []string) {
	counters := make([]iisCounter, 0, len(paths))
	specs := make([]CounterSpec, 0, len(paths))
	for _, path := range paths {
		pid, pool, field, ok := parseIISPath(path)
		if !ok {
			continue
		}
		specs = append(specs, CounterSpec{Path: path, Field: path, Kind: KindGauge})
		counters = append(counters, iisCounter{key: path, pid: pid, pool: pool, field: field})
	}
	g.counters = counters
	g.engine = NewEngine(specs, g.cores)
}

// parseIISPath extracts the "<PID>_<AppPool>" instance and matches the
// object/counter pair; anything else (incl. _Total) is ignored.
func parseIISPath(path string) (pid uint32, pool, field string, ok bool) {
	for _, c := range iisCounters {
		if !strings.HasPrefix(path, c.prefix) {
			continue
		}
		rest := path[len(c.prefix):]
		end := strings.IndexByte(rest, ')')
		if end < 0 || rest[end+1:] != `\`+c.counter {
			continue
		}
		p, name, valid := parseW3WPInstance(rest[:end])
		if !valid {
			return 0, "", "", false
		}
		return p, name, c.field, true
	}
	return 0, "", "", false
}

// refresh re-expands the wildcards on the same query handle — only when the
// process set changed, never per tick.
func (g *iisGroup) refresh() error {
	if r, ok := g.src.(interface{ RefreshInstances() error }); ok {
		if err := r.RefreshInstances(); err != nil {
			return err
		}
	}
	g.rebuild(iisPathsOf(g.src))
	return nil
}

// selectedPools keeps only resolved appPool targets with a confirmed
// creation time — owner-selected pools only, never PID alone.
func (g *iisGroup) selectedPools() []ResolvedTarget {
	if g.targets == nil {
		return nil
	}
	var out []ResolvedTarget
	for _, t := range g.targets() {
		if strings.HasPrefix(t.Key, "pool:") && t.Pool != "" && t.Start != 0 {
			out = append(out, t)
		}
	}
	return out
}

func (g *iisGroup) Collect(now time.Time, _ float64) (any, error) {
	selected := g.selectedPools()
	if len(selected) == 0 {
		return nil, nil // nothing selected: nothing sampled, never a zero row
	}
	if g.changed != nil && g.changed() {
		if err := g.refresh(); err != nil {
			return nil, err
		}
	}
	readings, err := g.src.Collect()
	if err != nil {
		return nil, err
	}
	out := g.engine.Process(now, readings)
	if len(out) == 0 {
		return nil, nil // nothing valid: group omitted, Missing via collector
	}
	byWorker := make(map[string]*IISWorker)
	for _, c := range g.counters {
		v, ok := out[c.key]
		if !ok {
			continue // missing stays nil, never 0
		}
		if !matchIISTarget(selected, c.pool, c.pid) {
			continue // unselected pool or stale PID: omit, never a zero row
		}
		key := c.pool + "\x00" + strconv.FormatUint(uint64(c.pid), 10)
		w := byWorker[key]
		if w == nil {
			w = &IISWorker{Pool: c.pool, PID: c.pid}
			byWorker[key] = w
		}
		switch c.field {
		case "activeRequests":
			w.ActiveRequests = &v
		case "activeThreads":
			w.ActiveThreads = &v
		case "pingLatencyMs":
			w.PingLatencyMs = &v
		}
	}
	if len(byWorker) == 0 {
		return nil, nil // every instance unmatched: no zero rows
	}
	workers := make([]IISWorker, 0, len(byWorker))
	for _, w := range byWorker {
		workers = append(workers, *w)
	}
	sort.Slice(workers, func(i, j int) bool {
		if workers[i].Pool != workers[j].Pool {
			return workers[i].Pool < workers[j].Pool
		}
		return workers[i].PID < workers[j].PID
	})
	return &IISGroup{Workers: workers}, nil
}

// matchIISTarget confirms the instance belongs to a selected pool by pool
// name AND PID; the target's Start was confirmed by the resolver, so the
// identity is (PID, creation time) — never PID alone.
func matchIISTarget(targets []ResolvedTarget, pool string, pid uint32) bool {
	for _, t := range targets {
		if t.PID == pid && strings.EqualFold(t.Pool, pool) {
			return true
		}
	}
	return false
}

// Close releases the query handle only when the factory created the source.
func (g *iisGroup) Close() error {
	if g.owned {
		return g.src.Close()
	}
	return nil
}