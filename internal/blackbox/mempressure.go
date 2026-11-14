package blackbox

import "time"

// BBX-4-04 memory pressure, machine-wide: hard page faults, kernel pool bytes
// and pagefile usage. Fixed English PDH paths, no wildcards, no per-process
// filtering, no text — numbers only. Missing counters stay nil, never 0.

func init() { RegisterGroup("memPressure", false, newMemPressureGroup) }

// memPressurePaths are the fixed English PDH paths this group samples. Every
// path is machine-wide (one instance each), so nothing is ever re-expanded.
// UNVERIFIED against a live server (see the packet's typeperf live check).
var memPressurePaths = []string{
	`\Memory\Pages Input/sec`,
	`\Memory\Pool Paged Bytes`,
	`\Memory\Pool Nonpaged Bytes`,
	`\Paging File(_Total)\% Usage`,
}

// memPressureSpecs binds each path to its output field and kind. Pages Input/sec
// is already per-second from PDH (KindPDHRate: two valid baselines, no delta);
// the pool byte counters and the pagefile percentage are gauges.
func memPressureSpecs() []CounterSpec {
	return []CounterSpec{
		{Path: `\Memory\Pages Input/sec`, Field: "hardFaultsPerSec", Kind: KindPDHRate},
		{Path: `\Memory\Pool Paged Bytes`, Field: "poolPagedBytes", Kind: KindGauge},
		{Path: `\Memory\Pool Nonpaged Bytes`, Field: "poolNonPagedBytes", Kind: KindGauge},
		{Path: `\Paging File(_Total)\% Usage`, Field: "pageFileUsagePct", Kind: KindGauge},
	}
}

// memPressureGroup samples the four counters over one reused PDH query handle.
type memPressureGroup struct {
	src    CounterSource
	engine *Engine
	owned  bool // factory created the source: Close owns it
}

func newMemPressureGroup(d GroupDeps) (Group, error) {
	src, owned := d.Source, false
	if src == nil {
		p, err := NewPDHSource(memPressurePaths)
		if err != nil {
			return nil, err // no PDH (non-Windows) or counters unavailable: skipped
		}
		src, owned = p, true
	}
	return &memPressureGroup{
		src:    src,
		engine: NewEngine(memPressureSpecs(), d.Cores),
		owned:  owned,
	}, nil
}

func (g *memPressureGroup) Name() string   { return "memPressure" }
func (g *memPressureGroup) Optional() bool { return false }

func (g *memPressureGroup) Collect(now time.Time, _ float64) (any, error) {
	readings, err := g.src.Collect()
	if err != nil {
		return nil, err
	}
	out := g.engine.Process(now, readings)
	if len(out) == 0 {
		return nil, nil // nothing valid: group omitted, never zero-filled
	}
	var grp MemPressureGroup
	if v, ok := out["hardFaultsPerSec"]; ok {
		grp.HardFaultsPerSec = &v
	}
	if v, ok := out["poolPagedBytes"]; ok && v >= 0 {
		u := uint64(v)
		grp.PoolPagedBytes = &u
	}
	if v, ok := out["poolNonPagedBytes"]; ok && v >= 0 {
		u := uint64(v)
		grp.PoolNonPagedBytes = &u
	}
	if v, ok := out["pageFileUsagePct"]; ok {
		grp.PageFileUsagePct = &v
	}
	return &grp, nil
}

// Close releases the query handle only when the factory created the source.
func (g *memPressureGroup) Close() error {
	if g.owned {
		return g.src.Close()
	}
	return nil
}