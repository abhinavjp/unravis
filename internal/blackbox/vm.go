package blackbox

import (
	"errors"
	"math"
	"time"
)

// BL-113 VMware memory balloon. One fixed English PDH path, read as a gauge.
// On a host that has no such counter object (physical, Hyper-V, cloud, VMware
// without the Tools performance library) the group is "not applicable": silent,
// nothing in Missing, looked at again every notApplicableRecheck.
//
// UNVERIFIED on a real VMware guest: the path comes from a 2020 VMware
// document, and the unit (MB, treated as MiB) is an assumption. The vm-probe
// command exists to capture the real names and values.

func init() { RegisterGroup("vm", true, newVMGroup) }

const vmBalloonPath = `\VM Memory\Memory Ballooned in MB`

// vmMaxBalloonMB caps a plausible reading (16 TiB in MiB). Above it the value is
// a broken provider, not a balloon.
const vmMaxBalloonMB = 16 << 20

// newVMSource is the test seam for the factory's own PDH source, like
// discoverSQLInstances elsewhere.
var newVMSource = func(paths []string) (CounterSource, error) {
	p, err := NewPDHSourceTolerant(paths)
	if err != nil {
		return nil, err // never a typed-nil CounterSource
	}
	return p, nil
}

// vmGroup reads the balloon gauge. stealPct is never written here.
type vmGroup struct {
	src    CounterSource
	engine *Engine
	owned  bool // factory created the source: Close owns it
}

func newVMGroup(d GroupDeps) (Group, error) {
	src, owned := d.Source, false
	if src == nil {
		s, err := newVMSource([]string{vmBalloonPath})
		if err != nil {
			if errors.Is(err, errPDHAbsent) {
				return nil, errNotApplicable
			}
			return nil, err
		}
		if s == nil {
			return nil, errGroupUnavailable
		}
		src, owned = s, true
	}
	return &vmGroup{
		src: src,
		engine: NewEngine([]CounterSpec{
			{Path: vmBalloonPath, Field: "balloonMB", Kind: KindGauge},
		}, d.Cores),
		owned: owned,
	}, nil
}

func (g *vmGroup) Name() string   { return "vm" }
func (g *vmGroup) Optional() bool { return true }

func (g *vmGroup) Collect(now time.Time, _ float64) (any, error) {
	readings, err := g.src.Collect()
	if err != nil {
		return nil, err
	}
	out := g.engine.Process(now, readings)
	v, ok := out["balloonMB"]
	if !ok || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > vmMaxBalloonMB {
		return nil, nil // not read or implausible: never a zero for missing
	}
	b := uint64(math.Round(v)) << 20
	return &VMGroup{BalloonBytes: &b}, nil
}

// Close releases the source only when the factory created it.
func (g *vmGroup) Close() error {
	if g.owned {
		return g.src.Close()
	}
	return nil
}
