package blackbox

import (
	"sort"
	"strings"
	"time"
)

// BBX-3-03 per-process I/O, pagefile, virtual bytes and elapsed time for the
// owner-selected targets (any runtime). One reused PDH query over the Process
// wildcards; #N instances map to PIDs via ID Process, confirmed by (PID,
// creation time). English paths only; names UNVERIFIED pending live check.
// No text, no attach/dump, no goroutine/ticker.

func init() { RegisterGroup("procIO", true, newProcIOGroup) }

// procIOPaths is the ID Process mapping plus the five per-process counters.
func procIOPaths() []string {
	return []string{
		`\Process(*)\ID Process`,
		`\Process(*)\IO Read Bytes/sec`,
		`\Process(*)\IO Write Bytes/sec`,
		`\Process(*)\Page File Bytes`,
		`\Process(*)\Virtual Bytes`,
		`\Process(*)\Elapsed Time`,
	}
}

const procIOPrefix = `\Process(`

// procIOStartSkew bounds the (PID, creation) confirmation: Elapsed Time is
// whole seconds and is sampled slightly before the tick time.
const procIOStartSkew = 3 // seconds

// procIOCounter binds one expanded path to its instance and counter name;
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
	case "ID Process", "Page File Bytes", "Virtual Bytes", "Elapsed Time":
		return KindGauge, true
	case "IO Read Bytes/sec", "IO Write Bytes/sec":
		return KindPDHRate, true
	}
	return 0, false
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

// rebuild derives per-(instance,counter) specs and a fresh engine from the
// expanded paths: new instances start their two-baseline gate again.
func (g *procIOGroup) rebuild(paths []string) {
	counters := make([]procIOCounter, 0, len(paths))
	specs := make([]CounterSpec, 0, len(paths))
	for _, path := range paths {
		inst, counter, ok := parseProcIOPath(path)
		if !ok || inst == "_Total" {
			continue // the aggregate is not a process row
		}
		kind, ok := procIOKind(counter)
		if !ok {
			continue
		}
		key := inst + "\x00" + counter
		specs = append(specs, CounterSpec{Path: path, Field: key, Kind: kind})
		counters = append(counters, procIOCounter{key: key, inst: inst, field: counter})
	}
	g.counters = counters
	g.engine = NewEngine(specs, g.cores)
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
	if g.targets == nil || len(g.targets()) == 0 {
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
	out := g.engine.Process(now, readings)
	if len(out) == 0 {
		return nil, nil // nothing valid: group omitted, never zero-filled
	}
	// Fold emitted values per instance; ID Process builds instance->PID.
	byInst := make(map[string]map[string]float64)
	pids := make(map[string]uint32)
	for _, c := range g.counters {
		v, ok := out[c.key]
		if !ok {
			continue // missing stays nil, never 0
		}
		if c.field == "ID Process" {
			if v > 0 {
				pids[c.inst] = uint32(v)
			}
			continue
		}
		m := byInst[c.inst]
		if m == nil {
			m = make(map[string]float64)
			byInst[c.inst] = m
		}
		m[c.field] = v
	}
	rows := g.match(now, byInst, pids)
	if len(rows) == 0 {
		return nil, nil
	}
	return &ProcIOGroup{Targets: rows}, nil
}

// match keeps only owner-selected targets (any runtime): match by PID, then
// confirm Start via Elapsed Time (creation = now - elapsed). No instance or
// an unconfirmed creation time omits the target.
func (g *procIOGroup) match(now time.Time, byInst map[string]map[string]float64, pids map[string]uint32) []ProcIOTarget {
	if g.targets == nil {
		return nil
	}
	insts := make([]string, 0, len(byInst))
	for inst := range byInst {
		insts = append(insts, inst)
	}
	sort.Strings(insts) // deterministic instance order
	var rows []ProcIOTarget
	for _, t := range g.targets() {
		for _, inst := range insts {
			if pid := pids[inst]; pid == 0 || pid != t.PID {
				continue // no mapped PID, or a different process
			}
			vals := byInst[inst]
			elapsed, ok := vals["Elapsed Time"]
			if !ok {
				continue // creation time unreadable: identity unconfirmed
			}
			if d := now.Unix() - int64(elapsed) - t.Start; d < -procIOStartSkew || d > procIOStartSkew {
				continue // PID reused by a different process
			}
			rows = append(rows, procIOTarget(t.Key, vals))
			break
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Target < rows[j].Target })
	return rows
}

// procIOTarget folds one instance's values into a row: absent fields stay
// nil (missing, never 0); byte gauges must be non-negative.
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