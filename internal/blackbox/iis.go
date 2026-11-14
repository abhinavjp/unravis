package blackbox

import (
	"sort"
	"strconv"
	"strings"
	"time"
)

// BBX-2-02 IIS worker counters for SELECTED app pools only: W3SVC_W3WP
// active requests/threads and WAS_W3WP ping latency. Instances are
// "<PID>_<AppPoolName>"; the instance IS the pool's w3wp worker. Each worker
// PID is verified against the Process object's ID Process + Creation Time (the
// collection-time inventory) and the selected pool before any reading is kept
// (W5-02). The resolver may retarget the app PID to an out-of-process child, so
// worker identities are kept separate from the application-child target. English
// paths only; the WAS counter name is UNVERIFIED pending the live typeperf check.
var iisPaths = []string{
	`\W3SVC_W3WP(*)\Active Requests`,
	`\W3SVC_W3WP(*)\Active Threads`,
	`\WAS_W3WP(*)\Ping Response Latency`,
	`\Process(*)\ID Process`,
	`\Process(*)\Creation Time`,
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
		if d.Targets == nil {
			// Nothing selected: do not create a machine-wide detail query at all
			// (W2-04). The per-tick short-circuit alone still expanded, added and
			// primed every Process instance before filtering the output.
			return nil, errNoTargets
		}
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

// rebuild derives per-instance DETAIL specs from the expanded paths and a fresh
// engine. The Process-object identity counters are not W3WP/WAS detail, so
// parseIISPath rejects them and they never become engine specs; they are read
// from the raw sample to verify workers (W5-02/W5-03).
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
	g.engine = reconcileEngine(g.engine, specs, g.cores)
}

// parseIISPath extracts the "<PID>_<AppPool>" instance and matches the
// object/counter pair; anything else (incl. _Total and Process paths) is ignored.
func parseIISPath(path string) (pid uint32, pool, field string, ok bool) {
	for _, c := range iisCounters {
		if !strings.HasPrefix(path, c.prefix) {
			continue
		}
		rest := path[len(c.prefix):]
		// The instance ends at the ')\' separator (clr.go pattern), not the
		// first ')': pool names may contain parentheses ("MyApp (Staging)").
		end := strings.Index(rest, `)\`)
		if end < 0 || rest[end+2:] != c.counter {
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

// selectedPoolsFrom keeps only resolved appPool targets with a confirmed
// creation identity from the captured view — owner-selected pools only, never
// PID alone. The lossless StartTicks counts as a confirmed creation too.
func selectedPoolsFrom(targets []ResolvedTarget) []ResolvedTarget {
	var out []ResolvedTarget
	for _, t := range targets {
		if strings.HasPrefix(t.Key, "pool:") && t.Pool != "" && targetHasCreation(t) {
			out = append(out, t)
		}
	}
	return out
}

func (g *iisGroup) Collect(now time.Time, _ float64) (any, error) {
	// Capture the ONE shared per-tick view before collecting; never re-resolve
	// afterwards (W5-01/W5-02).
	var captured []ResolvedTarget
	if g.targets != nil {
		captured = g.targets()
	}
	selected := selectedPoolsFrom(captured)
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
	// Verified worker set from the Process object (same atomic query): PID ->
	// creation FILETIME. A PID reported by more than one Process instance is
	// ambiguous and fails closed. This is the collection-time inventory the
	// counter-path PID is checked against (W5-02).
	procInstPID, procInstCreation := procIOIdentity(readings)
	pidCount := make(map[uint32]int, len(procInstPID))
	pidCreation := make(map[uint32]float64, len(procInstPID))
	for inst, pid := range procInstPID {
		pidCount[pid]++
		if cr, ok := procInstCreation[inst]; ok {
			pidCreation[pid] = cr
		}
	}
	// acceptWorker verifies one "<PID>_<pool>" instance: the worker PID must be a
	// live, unambiguous process with a confirmed creation, the pool must be
	// selected, and when the selected target IS this worker (in-process) its
	// creation must confirm the target's identity — losslessly on StartTicks
	// when the view carries it, so a same-second recycled worker PID is never
	// bound to the wrong lifetime. An out-of-process parent or an overlapping
	// worker (target PID differs) is accepted on verified-alive + selected-pool;
	// the view carries the child/other-worker identity, not this worker's, so
	// its own creation cannot be cross-checked here (see report).
	acceptWorker := func(pid uint32, pool string) bool {
		if pidCount[pid] != 1 {
			return false // worker PID absent from the inventory or ambiguous
		}
		cr, ok := pidCreation[pid]
		if !ok {
			return false // worker creation unreadable: identity unconfirmed
		}
		poolSelected := false
		var self *ResolvedTarget
		for i := range selected {
			if strings.EqualFold(selected[i].Pool, pool) {
				poolSelected = true
				if selected[i].PID == pid {
					self = &selected[i]
				}
			}
		}
		if !poolSelected {
			return false // unselected pool: omit, never a zero row
		}
		if self != nil {
			return targetCreationConfirmed(cr, *self) // in-process: bind the generation
		}
		return true // out-of-process parent / overlapping worker: verified alive
	}
	// Only verified workers' detail reaches the engine (W5-03). PDH cannot narrow
	// a wildcard query, so this is the earliest possible filter.
	filtered := make([]CounterReading, 0, len(readings))
	for _, r := range readings {
		pid, pool, _, ok := parseIISPath(r.Path)
		if !ok || !acceptWorker(pid, pool) {
			continue
		}
		filtered = append(filtered, r)
	}
	out := g.engine.Process(now, filtered)
	if len(out) == 0 {
		return nil, nil // nothing valid: group omitted, Missing via collector
	}
	byWorker := make(map[string]*IISWorker)
	for _, c := range g.counters {
		v, ok := out[c.key]
		if !ok {
			continue // missing stays nil, never 0 (and unselected never reach out)
		}
		key := c.pool + "\x00" + strconv.FormatUint(uint64(c.pid), 10)
		w := byWorker[key]
		if w == nil {
			w = &IISWorker{Pool: c.pool, PID: c.pid}
			// Carry the verified worker generation on the wire (additive): the
			// creation FILETIME from the same atomic query that accepted this
			// worker. A recycled worker PID is then distinguishable downstream
			// even when two lifetimes share a unix second. Unreadable creation
			// stays omitted (0), never a guessed stamp.
			if cr := pidCreation[c.pid]; cr > 0 {
				w.StartTicks = int64(cr)
			}
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

// Close releases the query handle only when the factory created the source.
func (g *iisGroup) Close() error {
	if g.owned {
		return g.src.Close()
	}
	return nil
}
