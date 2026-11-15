package blackbox

import (
	"sort"
	"strings"
	"time"
)

// BBX-3-01 CLR counters for owner-selected Framework (runtime 1) targets.
// Instances are process names (#N), not PIDs: the CLR Process ID counter maps
// instance -> PID, and the Process object's ID Process + Creation Time map that
// PID to its creation FILETIME. Readings join to the shared per-tick view on the
// full (PID, creation) identity, never PID alone (W5-01). No text.

func init() { RegisterGroup("clr", true, newCLRGroup) }

// clrPaths are the wildcard PDH paths. The CLR Process ID counter is the
// instance->PID map; the two Process-object counters supply the PID->creation
// identity the CLR objects do not expose themselves.
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
	`\Process(*)\ID Process`,
	`\Process(*)\Creation Time`,
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

// clrCounter binds one expanded detail path to its instance and counter; key is
// the engine output key (NUL cannot occur in a PDH instance name).
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
		if d.Targets == nil {
			// Nothing selected: do not create a machine-wide detail query at all
			// (W2-04). The per-tick short-circuit alone still expanded, added and
			// primed every Process instance before filtering the output.
			return nil, errNoTargets
		}
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

// rebuild derives DETAIL specs from the expanded CLR paths plus a fresh engine:
// new instances start their two-baseline gate again. The CLR Process ID mapping
// counter and the Process-object identity counters are read from the raw sample,
// not the engine, so unselected instances never enter engine history (W5-03).
func (g *clrGroup) rebuild(paths []string) {
	counters := make([]clrCounter, 0, len(paths))
	specs := make([]CounterSpec, 0, len(paths))
	for _, path := range paths {
		inst, counter, kind, ok := parseCLRPath(path)
		if !ok {
			continue // Process-object identity paths are not CLR detail
		}
		if counter == "Process ID" {
			continue // CLR instance->PID mapping is consumed from the raw sample
		}
		key := inst + "\x00" + counter
		// Volatile: a CLR instance name is a process label PDH can rebind to
		// another process, so it must not carry a baseline across a refresh.
		specs = append(specs, CounterSpec{Path: path, Field: key, Kind: kind, Volatile: true})
		counters = append(counters, clrCounter{key: key, inst: inst, field: counter})
	}
	g.counters = counters
	g.engine = reconcileEngine(g.engine, specs, g.cores)
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
	// Capture the ONE shared per-tick view before collecting and never re-resolve
	// afterwards (W5-01): a mid-collect PID replacement must not be emitted under
	// a new identity.
	var targets []ResolvedTarget
	if g.targets != nil {
		targets = g.targets()
	}
	if len(targets) == 0 {
		return nil, nil // nothing selected: nothing sampled, never machine-wide (BL-122)
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
	// PID -> creation FILETIME from the Process object (same atomic query). A PID
	// reported by more than one Process instance is ambiguous and fails closed.
	procInstPID, procInstCreation := procIOIdentity(readings)
	pidCount := make(map[uint32]int, len(procInstPID))
	pidCreation := make(map[uint32]float64, len(procInstPID))
	for inst, pid := range procInstPID {
		pidCount[pid]++
		if cr, ok := procInstCreation[inst]; ok {
			pidCreation[pid] = cr
		}
	}
	// CLR instance -> PID from the CLR Process ID counter. A PID claimed by more
	// than one CLR instance is likewise ambiguous and fails closed (W5-01).
	clrInstPID := make(map[string]uint32)
	clrPIDCount := make(map[uint32]int)
	for _, r := range readings {
		if !r.OK || !CounterStatusOK(r.Status) {
			continue
		}
		inst, counter, _, ok := parseCLRPath(r.Path)
		if !ok || counter != "Process ID" || r.Value <= 0 {
			continue
		}
		clrInstPID[inst] = uint32(r.Value)
	}
	for _, pid := range clrInstPID {
		clrPIDCount[pid]++
	}
	// Bind each captured Framework target to the one CLR instance whose PID and
	// creation confirm it. The join uses the lossless StartTicks identity when
	// the view carries it, so two same-PID lifetimes within one second are never
	// conflated.
	instOfTarget := make(map[string]string)
	selected := make(map[string]bool)
	for _, t := range targets {
		if t.Runtime != 1 || !targetHasCreation(t) {
			continue // Framework only; creation must be confirmed
		}
		if pidCount[t.PID] != 1 {
			continue // PID absent from the inventory or ambiguous
		}
		cr, ok := pidCreation[t.PID]
		if !ok || !targetCreationConfirmed(cr, t) {
			continue // creation unreadable or a different generation
		}
		for inst, pid := range clrInstPID {
			if pid != t.PID || clrPIDCount[pid] != 1 {
				continue
			}
			instOfTarget[t.Key] = inst
			selected[inst] = true
			break
		}
	}
	if len(selected) == 0 {
		return nil, nil
	}
	// Only selected instances' CLR detail reaches the engine (W5-03). PDH cannot
	// narrow a wildcard query, so this is the earliest possible filter.
	filtered := make([]CounterReading, 0, len(readings))
	for _, r := range readings {
		inst, counter, _, ok := parseCLRPath(r.Path)
		if !ok || counter == "Process ID" || !selected[inst] {
			continue
		}
		filtered = append(filtered, r)
	}
	out := g.engine.Process(now, filtered)

	// Fold values per instance; missing stays absent, never 0.
	vals := make(map[string]map[string]float64)
	for _, c := range g.counters {
		v, ok := out[c.key]
		if !ok {
			continue
		}
		if vals[c.inst] == nil {
			vals[c.inst] = make(map[string]float64)
		}
		vals[c.inst][c.field] = v
	}
	var rows []CLRTarget
	for _, t := range targets {
		inst, ok := instOfTarget[t.Key]
		if !ok {
			continue // no confirmed CLR instance for this target: omit
		}
		rows = append(rows, clrTargetRow(t.Key, vals[inst]))
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
