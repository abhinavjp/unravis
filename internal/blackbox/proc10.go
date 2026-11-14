package blackbox

import (
	"sort"
	"strings"
	"time"
)

// proc10Paths are the English PDH wildcards this group samples. ID Process maps
// each #N instance to its PID and Creation Time to its creation FILETIME; the
// three metrics are per-process. Identity is (PID, creation) confirmed against
// the shared per-tick view, never PID alone (W5-01). UNVERIFIED against a live
// .NET 10 pool (see packet live-check).
var proc10Paths = []string{
	`\Process(*)\ID Process`,
	`\Process(*)\Creation Time`,
	`\Process(*)\% Processor Time`,
	`\Process(*)\Working Set`,
	`\Process(*)\Private Bytes`,
}

const proc10Prefix = `\Process(`

func init() { RegisterGroup("proc10", true, newProc10Group) }

type proc10Group struct {
	src     CounterSource
	engine  *Engine
	targets func() []ResolvedTarget
	changed func() bool
	cores   int
	owned   bool // factory created the source: Close owns it
}

func newProc10Group(d GroupDeps) (Group, error) {
	src, owned := d.Source, false
	if src == nil {
		if d.Targets == nil {
			// Nothing selected: do not create a machine-wide detail query at all
			// (W2-04). The per-tick short-circuit alone still expanded, added and
			// primed every Process instance before filtering the output.
			return nil, errNoTargets
		}
		p, err := NewPDHSource(proc10Paths)
		if err != nil {
			return nil, err // no PDH (non-Windows): BuildGroups skips the group
		}
		src, owned = p, true
	}
	g := &proc10Group{src: src, targets: d.Targets, changed: d.ProcessSetChanged, cores: d.Cores, owned: owned}
	g.rebuild(proc10PathsOf(src))
	return g, nil
}

func (g *proc10Group) Name() string   { return "proc10" }
func (g *proc10Group) Optional() bool { return true }

func proc10PathsOf(src CounterSource) []string {
	if p, ok := src.(interface{ Paths() []string }); ok {
		return p.Paths()
	}
	return nil
}

// parseProc10Path extracts the instance and counter; CPU is a rate (no 100 cap),
// the rest are gauges. Anything else is ignored.
func parseProc10Path(path string) (inst, counter string, kind CounterKind, ok bool) {
	if !strings.HasPrefix(path, proc10Prefix) {
		return "", "", 0, false
	}
	rest := path[len(proc10Prefix):]
	// Split on ")\", not the first ')': an image name may contain ')'
	// (e.g. "Svc (x86)"), same separator pattern as parseCLRPath.
	end := strings.Index(rest, `)\`)
	if end < 0 {
		return "", "", 0, false
	}
	inst, counter = rest[:end], rest[end+2:]
	switch counter {
	case "% Processor Time":
		kind = KindPDHRate
	case "ID Process", "Creation Time", "Working Set", "Private Bytes":
		kind = KindGauge
	default:
		return "", "", 0, false
	}
	return inst, counter, kind, true
}

// proc10Mapping reports whether a counter is an identity-mapping counter read
// straight from the raw sample (never engine detail).
func proc10Mapping(counter string) bool {
	return counter == "ID Process" || counter == "Creation Time"
}

// rebuild derives per-(instance,counter) DETAIL specs and a fresh engine. The
// identity-mapping counters (ID Process, Creation Time) are read from the raw
// sample, not the engine, so unselected instances never enter engine history
// (W5-03). New instances start their two-baseline gate again. NUL cannot occur
// in a PDH instance name.
func (g *proc10Group) rebuild(paths []string) {
	specs := make([]CounterSpec, 0, len(paths))
	for _, p := range paths {
		inst, counter, kind, ok := parseProc10Path(p)
		if !ok || inst == "_Total" || inst == "Idle" {
			continue
		}
		if proc10Mapping(counter) {
			continue // mapping is consumed from the raw sample, not engine detail
		}
		// Volatile: PDH names Process instances by enumeration order, so the same
		// name can mean another process after a refresh.
		specs = append(specs, CounterSpec{Path: p, Field: inst + "\x00" + counter, Kind: kind, Volatile: true})
	}
	g.engine = reconcileEngine(g.engine, specs, g.cores)
}

// refresh re-expands the wildcards on the same query handle — only when the
// process set changed, never per tick.
func (g *proc10Group) refresh() error {
	if r, ok := g.src.(interface{ RefreshInstances() error }); ok {
		if err := r.RefreshInstances(); err != nil {
			return err
		}
	}
	g.rebuild(proc10PathsOf(g.src))
	return nil
}

func (g *proc10Group) Collect(now time.Time, _ float64) (any, error) {
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
	// Identity mapping from the raw sample (shared Process-object reader). A PID
	// reported by more than one instance is ambiguous and fails closed (W5-01).
	instPID, instCreation := procIOIdentity(readings)
	pidCount := make(map[uint32]int, len(instPID))
	for _, pid := range instPID {
		pidCount[pid]++
	}
	instOfTarget := make(map[string]string)
	selected := make(map[string]bool)
	for _, t := range targets {
		if t.Runtime != 2 || t.Start == 0 {
			continue // .NET 10 only; creation must be confirmed
		}
		for inst, pid := range instPID {
			if pid != t.PID || pidCount[pid] != 1 {
				continue
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
		return nil, nil
	}
	// Only selected instances' detail reaches the engine (W5-03). PDH cannot
	// narrow a wildcard query, so this is the earliest possible filter.
	filtered := make([]CounterReading, 0, len(readings))
	for _, r := range readings {
		inst, counter, _, ok := parseProc10Path(r.Path)
		if !ok || !selected[inst] || proc10Mapping(counter) {
			continue
		}
		filtered = append(filtered, r)
	}
	out := g.engine.Process(now, filtered)
	if len(out) == 0 {
		return nil, nil // nothing valid: group omitted
	}
	var grp Proc10Group
	for _, t := range targets {
		inst, ok := instOfTarget[t.Key]
		if !ok {
			continue // no confirmed instance for this target: omit, never zero-fill
		}
		row := Proc10Target{Target: t.Key}
		have := false
		if v, ok := out[inst+"\x00% Processor Time"]; ok {
			row.CPUPct = &v // multi-core: never capped at 100
			have = true
		}
		if v, ok := out[inst+"\x00Working Set"]; ok && v >= 0 {
			u := uint64(v)
			row.WorkingSet = &u
			have = true
		}
		if v, ok := out[inst+"\x00Private Bytes"]; ok && v >= 0 {
			u := uint64(v)
			row.PrivateBytes = &u
			have = true
		}
		if have {
			grp.Targets = append(grp.Targets, row)
		}
	}
	if len(grp.Targets) == 0 {
		return nil, nil
	}
	sort.Slice(grp.Targets, func(i, j int) bool { return grp.Targets[i].Target < grp.Targets[j].Target })
	return &grp, nil
}

// Close releases the query handle only when the factory created the source.
func (g *proc10Group) Close() error {
	if g.owned {
		return g.src.Close()
	}
	return nil
}
