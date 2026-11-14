package blackbox

import (
	"errors"
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

// sqlHostGroup samples every discovered SQL instance over one reused PDH query.
// Instances are re-discovered only when the process set changes, never per tick.
type sqlHostGroup struct {
	src      CounterSource
	engine   *Engine
	targets  map[string]sqlHostTarget
	discover func() ([]string, error)
	changed  func() bool
	cores    int
}

func (g *sqlHostGroup) Name() string   { return "sqlHost" }
func (g *sqlHostGroup) Optional() bool { return true }

// newSQLHostGroup is the factory: discover instances, build the PDH source and
// engine. A PDH error (e.g. non-Windows) returns the error so BuildGroups skips
// the group. No SQL login is opened — host counters only.
func newSQLHostGroup(d GroupDeps) (Group, error) {
	g := &sqlHostGroup{discover: discoverSQLInstances, changed: d.ProcessSetChanged, cores: d.Cores}
	prefixes, err := g.discover()
	if err != nil {
		return nil, err
	}
	if err := g.rebuild(prefixes); err != nil {
		return nil, err
	}
	return g, nil
}

// sqlHostNewSource builds the counter source; a seam so tests inject a fake and
// the group never depends on PDH directly. Defaults to the real PDH source.
var sqlHostNewSource = func(paths []string) (CounterSource, error) { return NewPDHSource(paths) }

// rebuild (re)builds the source, engine and target map for a prefix list. It
// closes any prior source so handles are reused, never leaked.
func (g *sqlHostGroup) rebuild(prefixes []string) error {
	paths, specs, targets := buildSQLHostTargets(prefixes)
	src, err := sqlHostNewSource(paths)
	if err != nil {
		return err
	}
	if g.src != nil {
		_ = g.src.Close()
	}
	g.src = src
	g.engine = NewEngine(specs, g.cores)
	g.targets = targets
	return nil
}

// Collect samples one tick. It re-discovers instances only on a process-set
// change, then folds valid emitted fields into per-instance rows. An instance
// with no valid counter is omitted; no valid instance yields a nil group.
func (g *sqlHostGroup) Collect(now time.Time, dt float64) (any, error) {
	if g.changed != nil && g.changed() {
		prefixes, err := g.discover()
		if err != nil {
			return nil, err
		}
		if err := g.rebuild(prefixes); err != nil {
			return nil, err
		}
	}
	readings, err := g.src.Collect()
	if err != nil {
		return nil, err
	}
	grp := g.build(g.engine.Process(now, readings))
	if grp == nil {
		return nil, nil // untyped nil: no valid instance, group omitted
	}
	return grp, nil
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