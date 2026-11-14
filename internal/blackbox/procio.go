package blackbox

import (
	"sort"
	"strings"
	"time"
)

// BBX-3-03 per-process I/O, pagefile, virtual bytes and elapsed time for the
// owner-selected targets (any runtime). One reused PDH query over the Process
// wildcards; #N instances map to PIDs via ID Process and to a creation time via
// Creation Time, confirmed against the shared per-tick view's (PID, Start).
// English paths only; names UNVERIFIED pending live check. No text, no
// attach/dump, no goroutine/ticker.

func init() { RegisterGroup("procIO", true, newProcIOGroup) }

// procIOPaths is the ID Process + Creation Time mapping plus the five
// per-process detail counters. Creation Time is the process creation FILETIME
// (100-ns ticks since 1601): it is the identity discriminator, replacing the old
// now-minus-Elapsed-Time estimate (W5-04). Elapsed Time stays a metric only.
func procIOPaths() []string {
	return []string{
		`\Process(*)\ID Process`,
		`\Process(*)\Creation Time`,
		`\Process(*)\IO Read Bytes/sec`,
		`\Process(*)\IO Write Bytes/sec`,
		`\Process(*)\Page File Bytes`,
		`\Process(*)\Virtual Bytes`,
		`\Process(*)\Elapsed Time`,
	}
}

const procIOPrefix = `\Process(`

// filetimeUnixEpochDiff is the FILETIME tick count of the unix epoch
// (1970-01-01) relative to the FILETIME epoch (1601-01-01). Shared by every
// per-process group's creation-time join.
const filetimeUnixEpochDiff int64 = 11644473600 * filetimeTicksPerSecond

// pdhCreationSlack bounds the float64 rounding of a PDH Creation Time value.
// PDH formats the FILETIME as a float64; near the current epoch one ulp is 16
// ticks, so the formatted value can differ from the true ticks by up to half an
// ulp. The slack keeps a valid creation sitting on a second boundary from being
// dropped, and it can never bridge two whole seconds (the windows are 10^7 ticks
// apart), so a different generation a second or more away is always rejected.
const pdhCreationSlack = 16

// creationWindow returns the [lo, hi) FILETIME tick range that the resolver's
// unix-second Start represents. The resolver truncates StartTicks to Start, so
// the true creation lies somewhere in this window.
func creationWindow(startUnix int64) (lo, hi int64) {
	lo = startUnix*filetimeTicksPerSecond + filetimeUnixEpochDiff
	return lo, lo + filetimeTicksPerSecond
}

// creationConfirmed reports whether a PDH Creation Time reading (FILETIME ticks
// as float64) confirms the target's unix-second Start. Identity is the creation
// time itself, never an age: the check is independent of the collection clock,
// so a delayed read of the same generation still confirms (W5-04). A Start of 0
// (unreadable creation) never confirms.
func creationConfirmed(creationTicks float64, startUnix int64) bool {
	if startUnix <= 0 {
		return false
	}
	lo, hi := creationWindow(startUnix)
	return creationTicks >= float64(lo-pdhCreationSlack) && creationTicks < float64(hi+pdhCreationSlack)
}

// procIOCounter binds one expanded detail path to its instance and counter name;
// key is the engine output key (NUL cannot occur in an instance name).
type procIOCounter struct {
	key   string
	inst  string
	field string
}

type procIOGroup struct {
	src      CounterSource
	engine   *Engine
	counters []procIOCounter
	targets  func() []ResolvedTarget
	changed  func() bool
	cores    int
	owned    bool // factory created the source: Close owns it
}

func newProcIOGroup(d GroupDeps) (Group, error) {
	src, owned := d.Source, false
	if src == nil {
		if d.Targets == nil {
			// Nothing selected: do not create a machine-wide detail query at all
			// (W2-04). The per-tick short-circuit alone still expanded, added and
			// primed every Process instance before filtering the output.
			return nil, errNoTargets
		}
		p, err := NewPDHSource(procIOPaths())
		if err != nil {
			return nil, err // no PDH (non-Windows): group skipped
		}
		src, owned = p, true
	}
	g := &procIOGroup{src: src, targets: d.Targets, changed: d.ProcessSetChanged, cores: d.Cores, owned: owned}
	g.rebuild(procIOPathsOf(src))
	return g, nil
}

func (g *procIOGroup) Name() string   { return "procIO" }
func (g *procIOGroup) Optional() bool { return true }

func procIOPathsOf(src CounterSource) []string {
	if p, ok := src.(interface{ Paths() []string }); ok {
		return p.Paths()
	}
	return nil
}

// procIOKind: IO bytes/sec are PDH rates (two-baseline gated); rest gauges.
func procIOKind(counter string) (CounterKind, bool) {
	switch counter {
	case "ID Process", "Creation Time", "Page File Bytes", "Virtual Bytes", "Elapsed Time":
		return KindGauge, true
	case "IO Read Bytes/sec", "IO Write Bytes/sec":
		return KindPDHRate, true
	}
	return 0, false
}

// procIOMapping reports whether a counter is an identity-mapping counter read
// straight from the raw sample (never engine detail): ID Process maps the
// instance to a PID, Creation Time to its creation FILETIME.
func procIOMapping(counter string) bool {
	return counter == "ID Process" || counter == "Creation Time"
}

// parseProcIOPath splits an expanded Process path into instance and counter.
func parseProcIOPath(path string) (inst, counter string, ok bool) {
	if !strings.HasPrefix(path, procIOPrefix) {
		return "", "", false
	}
	rest := path[len(procIOPrefix):]
	// Split on ")\", not the first ')': an image name may contain ')'
	// (e.g. "Svc (x86)"), same separator pattern as parseCLRPath.
	end := strings.Index(rest, `)\`)
	if end < 0 {
		return "", "", false
	}
	return rest[:end], rest[end+2:], true
}

// rebuild derives per-(instance,counter) DETAIL specs and a fresh engine from
// the expanded paths. The identity-mapping counters (ID Process, Creation Time)
// are read from the raw sample, not the engine, so unselected instances never
// enter engine history (W5-03). New instances start their two-baseline gate
// again.
func (g *procIOGroup) rebuild(paths []string) {
	counters := make([]procIOCounter, 0, len(paths))
	specs := make([]CounterSpec, 0, len(paths))
	for _, path := range paths {
		inst, counter, ok := parseProcIOPath(path)
		if !ok || inst == "_Total" || inst == "Idle" {
			continue // the aggregate / idle pseudo-process is not a process row
		}
		if procIOMapping(counter) {
			continue // mapping is consumed from the raw sample, not engine detail
		}
		kind, ok := procIOKind(counter)
		if !ok {
			continue
		}
		key := inst + "\x00" + counter
		// Volatile: a Process instance name is not a process identity.
		specs = append(specs, CounterSpec{Path: path, Field: key, Kind: kind, Volatile: true})
		counters = append(counters, procIOCounter{key: key, inst: inst, field: counter})
	}
	g.counters = counters
	g.engine = reconcileEngine(g.engine, specs, g.cores)
}

// refresh re-expands the wildcards on the same query handle — only when the
// process set changed, never per tick.
func (g *procIOGroup) refresh() error {
	if r, ok := g.src.(interface{ RefreshInstances() error }); ok {
		if err := r.RefreshInstances(); err != nil {
			return err
		}
	}
	g.rebuild(procIOPathsOf(g.src))
	return nil
}

func (g *procIOGroup) Collect(now time.Time, _ float64) (any, error) {
	// Capture the ONE shared per-tick view before collecting and never re-resolve
	// afterwards (W5-01): a process that exits and is replaced mid-collect must
	// not have its old readings emitted under a new identity.
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
	// Identity mapping from the raw sample: instance -> PID and instance ->
	// creation FILETIME. A PID reported by more than one instance is ambiguous
	// and fails closed (W5-01), never first-wins.
	instPID, instCreation := procIOIdentity(readings)
	pidCount := make(map[uint32]int, len(instPID))
	for _, pid := range instPID {
		pidCount[pid]++
	}
	// Bind each captured target to the one instance whose (PID, creation)
	// confirms it. Creation is read from the process itself, so a delayed read
	// of the same generation still confirms (W5-04).
	instOfTarget := make(map[string]string, len(targets))
	selected := make(map[string]bool)
	for _, t := range targets {
		if t.Start == 0 {
			continue // creation unreadable: identity unconfirmed
		}
		for inst, pid := range instPID {
			if pid != t.PID || pidCount[pid] != 1 {
				continue // no mapped PID, a different process, or an ambiguous PID
			}
			cr, ok := instCreation[inst]
			if !ok || !creationConfirmed(cr, t.Start) {
				continue // creation unreadable or a different generation
			}
			instOfTarget[t.Key] = inst
			selected[inst] = true
			break
		}
	}
	if len(selected) == 0 {
		return nil, nil // nothing confirmed: group omitted, never zero-filled
	}
	// Only selected instances' detail reaches the engine: unselected detail is
	// never formatted into output nor retained in rate history (W5-03). PDH
	// cannot narrow a wildcard query, so this is the earliest possible filter.
	filtered := make([]CounterReading, 0, len(readings))
	for _, r := range readings {
		inst, counter, ok := parseProcIOPath(r.Path)
		if !ok || !selected[inst] || procIOMapping(counter) {
			continue
		}
		filtered = append(filtered, r)
	}
	out := g.engine.Process(now, filtered)
	if len(out) == 0 {
		return nil, nil // nothing valid: group omitted, never zero-filled
	}
	byInst := make(map[string]map[string]float64)
	for _, c := range g.counters {
		v, ok := out[c.key]
		if !ok {
			continue // missing stays nil, never 0
		}
		m := byInst[c.inst]
		if m == nil {
			m = make(map[string]float64)
			byInst[c.inst] = m
		}
		m[c.field] = v
	}
	var rows []ProcIOTarget
	for _, t := range targets {
		inst, ok := instOfTarget[t.Key]
		if !ok {
			continue
		}
		rows = append(rows, procIOTarget(t.Key, byInst[inst]))
	}
	if len(rows) == 0 {
		return nil, nil
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Target < rows[j].Target })
	return &ProcIOGroup{Targets: rows}, nil
}

// procIOIdentity reads the instance -> PID and instance -> creation FILETIME
// maps from the raw sample. _Total/Idle and non-positive PIDs never map.
func procIOIdentity(readings []CounterReading) (instPID map[string]uint32, instCreation map[string]float64) {
	instPID = make(map[string]uint32)
	instCreation = make(map[string]float64)
	for _, r := range readings {
		if !r.OK || !CounterStatusOK(r.Status) {
			continue
		}
		inst, counter, ok := parseProcIOPath(r.Path)
		if !ok || inst == "_Total" || inst == "Idle" {
			continue
		}
		switch counter {
		case "ID Process":
			if r.Value > 0 {
				instPID[inst] = uint32(r.Value)
			}
		case "Creation Time":
			instCreation[inst] = r.Value
		}
	}
	return instPID, instCreation
}

// procIOTarget folds one instance's values into a row: absent fields stay
// nil (missing, never 0); byte gauges must be non-negative. Elapsed Time is a
// metric here, never the identity (W5-04).
func procIOTarget(key string, vals map[string]float64) ProcIOTarget {
	row := ProcIOTarget{Target: key}
	if v, ok := vals["IO Read Bytes/sec"]; ok {
		row.IOReadBytesPerSec = &v
	}
	if v, ok := vals["IO Write Bytes/sec"]; ok {
		row.IOWriteBytesPerSec = &v
	}
	if v, ok := vals["Page File Bytes"]; ok && v >= 0 {
		u := uint64(v)
		row.PageFileBytes = &u
	}
	if v, ok := vals["Virtual Bytes"]; ok && v >= 0 {
		u := uint64(v)
		row.VirtualBytes = &u
	}
	if v, ok := vals["Elapsed Time"]; ok {
		row.ElapsedSec = &v
	}
	return row
}

// Close releases the query handle only when the factory created the source.
func (g *procIOGroup) Close() error {
	if g.owned {
		return g.src.Close()
	}
	return nil
}
