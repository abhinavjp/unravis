package blackbox

import (
	"sort"
	"strings"
	"time"
)

// proc10Paths are the English PDH wildcards this group samples. ID Process maps
// each #N instance to its PID; the three metrics are per-process. UNVERIFIED
// against a live .NET 10 pool (see packet live-check).
var proc10Paths = []string{
	`\Process(*)\ID Process`,
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
	end := strings.IndexByte(rest, ')')
	if end < 0 || !strings.HasPrefix(rest[end+1:], `\`) {
		return "", "", 0, false
	}
	inst, counter = rest[:end], rest[end+2:]
	switch counter {
	case "% Processor Time":
		kind = KindPDHRate
	case "ID Process", "Working Set", "Private Bytes":
		kind = KindGauge
	default:
		return "", "", 0, false
	}
	return inst, counter, kind, true
}

// rebuild derives per-(instance,counter) specs and a fresh engine: new instances
// start their two-baseline gate again. NUL cannot occur in a PDH instance name.
func (g *proc10Group) rebuild(paths []string) {
	specs := make([]CounterSpec, 0, len(paths))
	for _, p := range paths {
		inst, counter, kind, ok := parseProc10Path(p)
		if !ok {
			continue
		}
		specs = append(specs, CounterSpec{Path: p, Field: inst + "\x00" + counter, Kind: kind})
	}
	g.engine = NewEngine(specs, g.cores)
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
	if len(out) == 0 || g.targets == nil {
		return nil, nil // nothing valid / no selection: group omitted
	}
	// instance → PID from the ID Process gauge; _Total/Idle and PID 0 never match.
	pidOf := make(map[string]uint32)
	for field, v := range out {
		inst, counter, ok := splitProc10Key(field)
		if !ok || counter != "ID Process" || inst == "_Total" || inst == "Idle" || v <= 0 {
			continue
		}
		pidOf[inst] = uint32(v)
	}
	insts := make([]string, 0, len(pidOf))
	for inst := range pidOf {
		insts = append(insts, inst)
	}
	sort.Strings(insts) // deterministic when two instances report one PID
	instOf := make(map[uint32]string, len(pidOf))
	for _, inst := range insts {
		if _, dup := instOf[pidOf[inst]]; !dup {
			instOf[pidOf[inst]] = inst
		}
	}
	var grp Proc10Group
	for _, t := range g.targets() {
		if t.Runtime != 2 || t.Start == 0 {
			continue // .NET 10 only; identity (PID, creation time) must be confirmed
		}
		inst, ok := instOf[t.PID]
		if !ok {
			continue // no instance for this PID: omit, never zero-fill
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

func splitProc10Key(field string) (inst, counter string, ok bool) {
	i := strings.IndexByte(field, 0)
	if i < 0 {
		return "", "", false
	}
	return field[:i], field[i+1:], true
}

// Close releases the query handle only when the factory created the source.
func (g *proc10Group) Close() error {
	if g.owned {
		return g.src.Close()
	}
	return nil
}
