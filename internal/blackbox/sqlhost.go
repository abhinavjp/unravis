package blackbox

import (
	"errors"
	"slices"
	"sort"
	"strings"
	"time"
)

// BBX-2-04 SQL Server host performance counters (BL-100 base + BL-107 extension).
// Host PDH counters only: NO SQL login, no connection, no query text. Every
// discovered instance (default "SQLServer:" and named "MSSQL$<NAME>:") is
// sampled machine-wide. Missing counters are omitted, never 0.

func init() { RegisterGroup("sqlHost", true, newSQLHostGroup) }

// sqlHostCounter is one researched counter per instance prefix. object/instance/
// counter build the English PDH path; set writes the emitted value into the
// matching SQLHostInstance field (nil when absent).
type sqlHostCounter struct {
	object   string
	instance string // "" for no instance, "_Total" for the aggregate
	counter  string
	kind     CounterKind
	set      func(*SQLHostInstance, *float64)
}

// sqlHostCounters is the BL-100 + BL-107 set (13). Rates are KindPDHRate (already
// per-second, gated by two baselines); the rest are KindGauge. LockWaitTimeMs and
// LogFlushWaitTime are gauge milliseconds. Names are UNVERIFIED pending live check.
var sqlHostCounters = []sqlHostCounter{
	{"SQL Statistics", "", "Batch Requests/sec", KindPDHRate, func(i *SQLHostInstance, v *float64) { i.BatchRequestsPerSec = v }},
	{"General Statistics", "", "User Connections", KindGauge, func(i *SQLHostInstance, v *float64) { i.UserConnections = v }},
	{"Locks", "_Total", "Lock Waits/sec", KindPDHRate, func(i *SQLHostInstance, v *float64) { i.LockWaitsPerSec = v }},
	{"General Statistics", "", "Processes Blocked", KindGauge, func(i *SQLHostInstance, v *float64) { i.ProcessesBlocked = v }},
	{"Memory Manager", "", "Memory Grants Pending", KindGauge, func(i *SQLHostInstance, v *float64) { i.MemoryGrantsPending = v }},
	{"Buffer Manager", "", "Page life expectancy", KindGauge, func(i *SQLHostInstance, v *float64) { i.PageLifeExpectancy = v }},
	{"Buffer Manager", "", "Free list stalls/sec", KindPDHRate, func(i *SQLHostInstance, v *float64) { i.FreeListStallsPerSec = v }},
	{"SQL Statistics", "", "SQL Compilations/sec", KindPDHRate, func(i *SQLHostInstance, v *float64) { i.CompilationsPerSec = v }},
	{"SQL Statistics", "", "SQL Re-Compilations/sec", KindPDHRate, func(i *SQLHostInstance, v *float64) { i.ReCompilationsPerSec = v }},
	{"Locks", "_Total", "Number of Deadlocks/sec", KindPDHRate, func(i *SQLHostInstance, v *float64) { i.DeadlocksPerSec = v }},
	{"Locks", "_Total", "Lock Wait Time (ms)", KindGauge, func(i *SQLHostInstance, v *float64) { i.LockWaitTimeMs = v }},
	{"Databases", "_Total", "Log Flush Wait Time", KindGauge, func(i *SQLHostInstance, v *float64) { i.LogFlushWaitTimeMs = v }},
	{"Access Methods", "", "Forwarded Records/sec", KindPDHRate, func(i *SQLHostInstance, v *float64) { i.ForwardedRecordsPerSec = v }},
}

// sqlHostTarget maps one emitted PDH path back to its instance label and field.
type sqlHostTarget struct {
	label string
	set   func(*SQLHostInstance, *float64)
}

// sqlHostPath builds the English PDH path for a prefix/object/instance/counter.
func sqlHostPath(prefix, object, instance, counter string) string {
	if instance == "" {
		return `\` + prefix + object + `\` + counter
	}
	return `\` + prefix + object + `(` + instance + `)` + `\` + counter
}

// sqlInstanceLabel maps a discovered prefix to its instance label: the default
// instance is "MSSQLSERVER"; a named "MSSQL$NAME:" prefix yields "NAME".
func sqlInstanceLabel(prefix string) string {
	if prefix == "SQLServer:" {
		return "MSSQLSERVER"
	}
	name := strings.TrimSuffix(prefix, ":")
	return strings.TrimPrefix(name, "MSSQL$")
}

// buildSQLHostTargets expands the counter set over every discovered prefix into
// PDH paths, engine specs (Field == path, unique per instance+counter) and the
// path->target map used to fold emitted values back into instances.
func buildSQLHostTargets(prefixes []string) (paths []string, specs []CounterSpec, targets map[string]sqlHostTarget) {
	targets = make(map[string]sqlHostTarget)
	for _, prefix := range prefixes {
		label := sqlInstanceLabel(prefix)
		for _, c := range sqlHostCounters {
			path := sqlHostPath(prefix, c.object, c.instance, c.counter)
			paths = append(paths, path)
			specs = append(specs, CounterSpec{Path: path, Field: path, Kind: c.kind})
			targets[path] = sqlHostTarget{label: label, set: c.set}
		}
	}
	return paths, specs, targets
}

// sqlHostMultiSource aggregates several per-instance counter sources into one
// CounterSource (W4-06). A source that fails to collect is skipped so the other
// instances stay readable; only when every source fails is an error returned.
// Close releases every underlying source.
type sqlHostMultiSource struct {
	srcs []CounterSource
}

func (m *sqlHostMultiSource) Collect() ([]CounterReading, error) {
	var out []CounterReading
	var lastErr error
	ok := 0
	for _, s := range m.srcs {
		r, err := s.Collect()
		if err != nil {
			lastErr = err
			continue // one unavailable instance must not suppress the others
		}
		ok++
		out = append(out, r...)
	}
	if ok == 0 && lastErr != nil {
		return nil, lastErr
	}
	return out, nil
}

func (m *sqlHostMultiSource) Close() error {
	var firstErr error
	for _, s := range m.srcs {
		if err := s.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// sqlHostGroup samples every discovered SQL instance. Instances are re-discovered
// only when the process set changes (or a bounded retry for unavailable counters
// comes due), never per tick. Each instance gets its own source so one bad
// counter/instance cannot suppress the rest (W4-06).
type sqlHostGroup struct {
	src      CounterSource
	engine   *Engine
	targets  map[string]sqlHostTarget
	prefixes []string // last built instance set, so an unchanged set is not rebuilt
	discover func() ([]string, error)
	changed  func() bool
	cores    int
	// sqlSig is the signature of the sqlservr.exe processes (nil: none wired). The
	// instance list is re-read when it changes, not on every process change.
	sqlSig       func() uint64
	seenSig      uint64
	haveSig      bool
	recheck      int
	lastDiscover time.Time
	// W4-06 partial availability: paths that could not be added, the fixed
	// per-instance diagnostics, and the bounded retry state for recovering them.
	missing  []string
	unavail  map[string]string
	next     time.Time
	failures int
}

func (g *sqlHostGroup) Name() string   { return "sqlHost" }
func (g *sqlHostGroup) Optional() bool { return true }

// newSQLHostGroup is the factory: discover instances, build the PDH sources and
// engine. Readable counters/instances are preserved even when some are
// unavailable (W4-06). Only when NOTHING can be read does it return a fixed
// error, so BuildGroups keeps a dormant bounded-retry (lazyGroup) slot and the
// group can recover without a recorder restart. No SQL login is opened — host
// counters only, and no raw provider text is returned.
func newSQLHostGroup(d GroupDeps) (Group, error) {
	g := &sqlHostGroup{discover: discoverSQLInstances, changed: d.ProcessSetChanged, cores: d.Cores,
		sqlSig: d.SQLProcessSig}
	if g.sqlSig != nil {
		g.seenSig, g.haveSig = g.sqlSig(), true
	}
	prefixes, err := g.discover()
	if err != nil {
		return nil, errSQLHostUnavailable
	}
	if err := g.rebuild(prefixes); err != nil {
		return nil, errSQLHostUnavailable
	}
	return g, nil
}

// sqlHostNewSource builds the counter source; a seam so tests inject a fake and
// the group never depends on PDH directly. Defaults to the real PDH source.
var sqlHostNewSource = func(paths []string) (CounterSource, error) { return NewPDHSource(paths) }

// rebuild (re)builds the sources, engine and target map for a prefix list. Each
// instance gets its own source; if an instance's full counter set cannot be
// added, its readable counters are preserved by probing per counter and building
// a source from just those (W4-06). Only when no instance yields a readable
// counter does it return a fixed error. It closes any prior source so handles
// are reused, never leaked, and reconciles the engine so unchanged counters keep
// their rate baselines.
func (g *sqlHostGroup) rebuild(prefixes []string) error {
	var srcs []CounterSource
	var readable []string
	var missing []string
	unavail := make(map[string]string)
	for _, prefix := range prefixes {
		paths, _, _ := buildSQLHostTargets([]string{prefix})
		src, err := sqlHostNewSource(paths)
		if err == nil {
			srcs = append(srcs, src)
			readable = append(readable, paths...)
			continue
		}
		// The instance's full set failed (one unsupported counter or a transient
		// provider error). Preserve its readable counters: probe each counter,
		// then build one source from the readable subset.
		var good []string
		for _, p := range paths {
			cs, cerr := sqlHostNewSource([]string{p})
			if cerr == nil {
				_ = cs.Close()
				good = append(good, p)
			} else {
				missing = append(missing, p)
			}
		}
		if len(good) > 0 {
			s, serr := sqlHostNewSource(good)
			if serr == nil {
				srcs = append(srcs, s)
				readable = append(readable, good...)
				continue
			}
			missing = append(missing, good...)
		}
		unavail[sqlInstanceLabel(prefix)] = sqlHostUnavailableCode
	}
	// No discovered instance at all is a valid empty group (SQL may be installed
	// later); it re-discovers on a process-set change. Had instances but none
	// readable is a transient failure -> fixed error -> dormant lazyGroup retry.
	if len(srcs) == 0 && len(prefixes) > 0 {
		return errSQLHostUnavailable
	}
	if g.src != nil {
		_ = g.src.Close()
	}
	// Build specs/targets for only the readable paths; an unreadable counter has
	// no spec and stays missing, never 0.
	allPaths, allSpecs, allTargets := buildSQLHostTargets(prefixes)
	readableSet := make(map[string]bool, len(readable))
	for _, p := range readable {
		readableSet[p] = true
	}
	var specs []CounterSpec
	targets := make(map[string]sqlHostTarget)
	for i, p := range allPaths {
		if readableSet[p] {
			specs = append(specs, allSpecs[i])
			targets[p] = allTargets[p]
		}
	}
	g.src = &sqlHostMultiSource{srcs: srcs}
	// Reconcile (not a fresh engine) so counters that stayed readable keep their
	// rate baselines across a rebuild (W4-04/W4-06).
	g.engine = reconcileEngine(g.engine, specs, g.cores)
	g.targets = targets
	g.prefixes = slices.Clone(prefixes)
	g.missing = missing
	g.unavail = unavail
	return nil
}

// Collect samples one tick. It re-discovers instances on a process-set change,
// and retries unavailable counters on a bounded backoff independent of unrelated
// churn (W4-06). An unrelated process arrival does not rebuild the query, so
// healthy rates are not re-primed (W4-04). It folds valid emitted fields into
// per-instance rows; an instance with no valid counter is omitted, and no valid
// instance yields a nil group. Availability failures use fixed codes, never raw
// provider text.
func (g *sqlHostGroup) Collect(now time.Time, dt float64) (any, error) {
	changed := g.changed != nil && g.changed()
	retryDue := len(g.missing) > 0 && !now.Before(g.next)
	// PdhEnumObjects with a refresh costs ~10 ms of CPU. A busy machine changes
	// its process set on nearly every tick, so asking "did a SQL Server engine
	// start or stop" first keeps that off the hot path.
	if g.discoveryWanted(now, changed, retryDue) {
		g.lastDiscover = now
		prefixes, derr := g.discover()
		if derr == nil {
			// Only a changed instance set or recovering missing counters is worth
			// new handles; an unchanged healthy set keeps its baselines.
			if !slices.Equal(prefixes, g.prefixes) || len(g.missing) > 0 {
				if rerr := g.rebuild(prefixes); rerr != nil && g.src == nil {
					return nil, errSQLHostUnavailable
				}
			}
		} else if g.src == nil {
			return nil, errSQLHostUnavailable
		}
		if len(g.missing) > 0 {
			g.failures++
			g.next = now.Add(groupRetryDelay(g.failures))
		} else {
			g.failures = 0
			g.next = time.Time{}
		}
	}
	if g.src == nil {
		return nil, errSQLHostUnavailable
	}
	readings, err := g.src.Collect()
	if err != nil {
		return nil, errSQLHostUnavailable
	}
	grp := g.build(g.engine.Process(now, readings))
	if grp == nil {
		return nil, nil // untyped nil: no valid instance, group omitted
	}
	return grp, nil
}

// sqlRediscoverEvery is the slowest a SQL instance set is re-read when a process
// changed but no SQL Server engine process did (a safety net for an engine whose
// counter object registers late).
const sqlRediscoverEvery = 5 * time.Minute

// sqlRecheckTicks is how many further ticks re-discover after an engine process
// appeared or went away: its PDH object registers a few seconds after the process.
const sqlRecheckTicks = 3

// discoveryWanted says whether this tick must re-read the PDH object list.
// Without a SQL process signature (tests, older callers) every process-set
// change re-discovers, as before. With one, only a change of the sqlservr.exe
// set, the few ticks after it, a due retry of missing counters, or the slow
// safety net does. Everything else keeps the instance set and its rate baselines.
func (g *sqlHostGroup) discoveryWanted(now time.Time, changed, retryDue bool) bool {
	if g.sqlSig == nil {
		return changed || retryDue
	}
	sig := g.sqlSig()
	if !g.haveSig || sig != g.seenSig {
		g.seenSig, g.haveSig, g.recheck = sig, true, sqlRecheckTicks
		return true
	}
	if g.recheck > 0 {
		g.recheck--
		return true
	}
	if retryDue {
		return true
	}
	return changed && (g.lastDiscover.IsZero() || now.Sub(g.lastDiscover) >= sqlRediscoverEvery)
}

// availability returns the fixed availability diagnostics for instances that
// could not be read (W4-06): instance label -> fixed code. An empty map means
// every discovered instance is readable. Never raw provider text.
func (g *sqlHostGroup) availability() map[string]string {
	out := make(map[string]string, len(g.unavail))
	for k, v := range g.unavail {
		out[k] = v
	}
	return out
}

// build folds emitted path->value pairs into instances. Only paths the engine
// emitted (valid, past any rate baseline) appear; a missing counter stays nil.
func (g *sqlHostGroup) build(out map[string]float64) *SQLHostGroup {
	instances := make(map[string]*SQLHostInstance)
	for path, value := range out {
		target, ok := g.targets[path]
		if !ok {
			continue
		}
		inst, ok := instances[target.label]
		if !ok {
			inst = &SQLHostInstance{Instance: target.label}
			instances[target.label] = inst
		}
		v := value
		target.set(inst, &v)
	}
	if len(instances) == 0 {
		return nil
	}
	labels := make([]string, 0, len(instances))
	for label := range instances {
		labels = append(labels, label)
	}
	sort.Strings(labels)
	group := &SQLHostGroup{Instances: make([]SQLHostInstance, 0, len(labels))}
	for _, label := range labels {
		group.Instances = append(group.Instances, *instances[label])
	}
	return group
}

// Close releases the PDH query once.
func (g *sqlHostGroup) Close() error {
	if g.src != nil {
		return g.src.Close()
	}
	return nil
}

// discoverSQLInstances returns the distinct PDH object prefixes that exist on
// this machine ("SQLServer:" default, "MSSQL$<NAME>:" named). It is a seam so
// tests fake it; the default enumerates PDH objects (authoritative for which
// counters exist). No SQL login is used.
var discoverSQLInstances = enumSQLInstancesPDH

// errSQLHostNoPDH is returned off Windows where PDH counters do not exist.
var errSQLHostNoPDH = errors.New("SQL host counters are Windows only")

// errSQLHostUnavailable is the fixed code for a SQL host group that could not
// read any counter (W4-06). It carries no provider text; BuildGroups turns it
// into a dormant bounded-retry slot and the collector names the group in Missing.
var errSQLHostUnavailable = errors.New("SQL host counters unavailable")

// sqlHostUnavailableCode is the fixed per-instance availability diagnostic for
// an instance whose counters could not be read (W4-06). Never provider text.
const sqlHostUnavailableCode = "unavailable"

// enumSQLInstancesPDH enumerates PDH objects and keeps the SQL instance prefixes.
func enumSQLInstancesPDH() ([]string, error) {
	objects, err := sqlEnumPDHObjects()
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool)
	var prefixes []string
	for _, obj := range objects {
		var prefix string
		switch {
		case strings.HasPrefix(obj, "SQLServer:"):
			prefix = "SQLServer:"
		case strings.HasPrefix(obj, "MSSQL$"):
			if i := strings.Index(obj, ":"); i > 0 {
				prefix = obj[:i+1]
			}
		}
		if prefix != "" && !seen[prefix] {
			seen[prefix] = true
			prefixes = append(prefixes, prefix)
		}
	}
	sort.Strings(prefixes)
	return prefixes, nil
}

// sqlEnumPDHObjects enumerates PDH object names. It is the injectable seam:
// tests replace it to fake discovery end to end. The default is the platform
// enumerator — real PdhEnumObjectsW (lazy pdh.dll) on Windows, no PDH elsewhere
// (sqlhost_windows.go / sqlhost_other.go, same pattern as NewPDHSource). Kept
// as a variable so this untagged file never references Windows-only symbols.
var sqlEnumPDHObjects = sqlEnumPDHObjectsPlatform
