package blackbox

import (
	"sort"
	"strings"
	"time"
)

// BBX-3-01 CLR counters for owner-selected Framework (runtime 1) targets.
// Instances are process names (#N), not PIDs: Process ID maps instance -> PID;
// targets match by PID, identity (PID, creation) is the resolver's. No text.

func init() { RegisterGroup("clr", true, newCLRGroup) }

// clrPaths are the wildcard PDH paths; Process ID is the instance->PID map.
var clrPaths = []string{
	`\.NET CLR Memory(*)\Process ID`,
	`\.NET CLR Memory(*)\Gen 0 heap size`,
	`\.NET CLR Memory(*)\Gen 1 heap size`,
	`\.NET CLR Memory(*)\Gen 2 heap size`,
	`\.NET CLR Memory(*)\Large Object Heap size`,
	`\.NET CLR Memory(*)\% Time in GC`,
	`\.NET CLR Memory(*)\Allocated Bytes/sec`,
	`\.NET CLR LocksAndThreads(*)\Contention Rate/sec`,
	`\.NET CLR LocksAndThreads(*)\# of current physical threads`,
	`\.NET CLR Exceptions(*)\# of Exceps Thrown/sec`,
}

// clrCounters binds counter names to kinds: rates are KindPDHRate (two
// baselines); % Time in GC is an uncapped gauge (multi-core GC exceeds 100).
var clrCounters = []struct {
	counter string
	kind    CounterKind
}{
	{"Process ID", KindGauge},
	{"Gen 0 heap size", KindGauge},
	{"Gen 1 heap size", KindGauge},
	{"Gen 2 heap size", KindGauge},
	{"Large Object Heap size", KindGauge},
	{"% Time in GC", KindGauge},
	{"Allocated Bytes/sec", KindPDHRate},
	{"Contention Rate/sec", KindPDHRate},
	{"# of current physical threads", KindGauge},
	{"# of Exceps Thrown/sec", KindPDHRate},
}

var clrPrefixes = []string{`\.NET CLR Memory(`, `\.NET CLR LocksAndThreads(`, `\.NET CLR Exceptions(`}

// clrCounter binds one expanded path to its instance and counter; key is the
// engine output key (NUL cannot occur in a PDH instance name).
type clrCounter struct {
	key   string
	inst  string
	field string
}

type clrGroup struct {
	src      CounterSource
	engine   *Engine
	counters []clrCounter
	targets  func() []ResolvedTarget
	changed  func() bool
	cores    int
	owned    bool // factory created the source: Close owns it
}

func newCLRGroup(d GroupDeps) (Group, error) {
	src, owned := d.Source, false
	if src == nil {
		p, err := NewPDHSource(clrPaths)
		if err != nil {
			return nil, err // no PDH (non-Windows) or no CLR objects: skipped
		}
		src, owned = p, true
	}
	g := &clrGroup{src: src, targets: d.Targets, changed: d.ProcessSetChanged, cores: d.Cores, owned: owned}
	g.rebuild(clrPathsOf(src))
	return g, nil
}

func (g *clrGroup) Name() string   { return "clr" }
func (g *clrGroup) Optional() bool { return true }

// clrPathsOf reads the expanded paths when the source exposes them.
func clrPathsOf(src CounterSource) []string {
	if p, ok := src.(interface{ Paths() []string }); ok {
		return p.Paths()
	}
	return nil
}

// rebuild derives specs from expanded paths plus a fresh engine: new
// instances start their two-baseline gate again.
func (g *clrGroup) rebuild(paths []string) {
	counters := make([]clrCounter, 0, len(paths))
	specs := make([]CounterSpec, 0, len(paths))
	for _, path := range paths {
		inst, counter, kind, ok := parseCLRPath(path)
		if !ok {
			continue
		}
		key := inst + "\x00" + counter
		specs = append(specs, CounterSpec{Path: path, Field: key, Kind: kind})
		counters = append(counters, clrCounter{key: key, inst: inst, field: counter})
	}
	g.counters = counters
	g.engine = NewEngine(specs, g.cores)
}

// parseCLRPath extracts the instance and matches the counter suffix.
func parseCLRPath(path string) (inst, counter string, kind CounterKind, ok bool) {
	for _, prefix := range clrPrefixes {
		if !strings.HasPrefix(path, prefix) {
			continue
		}
		rest := path[len(prefix):]
		end := strings.Index(rest, `)\`)
		if end < 0 {
			return "", "", 0, false
		}
		inst, counter = rest[:end], rest[end+2:]
		for _, c := range clrCounters {
			if c.counter == counter {
				return inst, counter, c.kind, true
			}
		}
		return "", "", 0, false
	}
	return "", "", 0, false
}

// refresh re-expands wildcards on the same query handle, only on a
// process-set change, never per tick.
func (g *clrGroup) refresh() error {
	if r, ok := g.src.(interface{ RefreshInstances() error }); ok {
		if err := r.RefreshInstances(); err != nil {
			return err
		}
	}
	g.rebuild(clrPathsOf(g.src))
	return nil
}

func (g *clrGroup) Collect(now time.Time, _ float64) (any, error) {
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

	// Fold values per instance; Process ID maps instance -> PID (path order:
	// first instance wins a duplicate PID).
	vals := make(map[string]map[string]float64)
	instOf := make(map[uint32]string)
	for _, c := range g.counters {
		v, ok := out[c.key]
		if !ok {
			continue // missing stays absent, never 0
		}
		if c.field == "Process ID" {
			if _, dup := instOf[uint32(v)]; !dup {
				instOf[uint32(v)] = c.inst
			}
			continue
		}
		if vals[c.inst] == nil {
			vals[c.inst] = make(map[string]float64)
		}
		vals[c.inst][c.field] = v
	}

	var rows []CLRTarget
	if g.targets != nil {
		for _, t := range g.targets() {
			if t.Runtime != 1 {
				continue // Framework only; runtime 2 is BBX-3-02/3-04
			}
			inst, ok := instOf[t.PID]
			if !ok {
				continue // no CLR instance for this target: omit
			}
			rows = append(rows, clrTargetRow(t.Key, vals[inst]))
		}
	}
	if len(rows) == 0 {
		return nil, nil // nothing selected/matched: group omitted
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Target < rows[j].Target })
	return &CLRGroup{Targets: rows}, nil
}

// clrTargetRow folds one instance's values into a row; missing stays nil.
func clrTargetRow(key string, m map[string]float64) CLRTarget {
	return CLRTarget{
		Target:           key,
		Gen0Bytes:        clrU64(m, "Gen 0 heap size"),
		Gen1Bytes:        clrU64(m, "Gen 1 heap size"),
		Gen2Bytes:        clrU64(m, "Gen 2 heap size"),
		LOHBytes:         clrU64(m, "Large Object Heap size"),
		PctTimeInGC:      clrF64(m, "% Time in GC"), // never capped at 100
		AllocBytesPerSec: clrF64(m, "Allocated Bytes/sec"),
		ContentionPerSec: clrF64(m, "Contention Rate/sec"),
		Threads:          clrF64(m, "# of current physical threads"),
		ExceptionsPerSec: clrF64(m, "# of Exceps Thrown/sec"),
	}
}

func clrF64(m map[string]float64, name string) *float64 {
	if v, ok := m[name]; ok {
		return &v
	}
	return nil
}

func clrU64(m map[string]float64, name string) *uint64 {
	if v, ok := m[name]; ok && v >= 0 {
		u := uint64(v)
		return &u
	}
	return nil
}

func (g *clrGroup) Close() error {
	if g.owned {
		return g.src.Close()
	}
	return nil
}