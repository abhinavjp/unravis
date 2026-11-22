package blackbox

import (
	"errors"
	"math"
	"sync/atomic"
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

	// Re-detection (VMware Tools removed, or the VM moved, while recording). Only a
	// source the group owns is re-detected; a shared test source never is.
	failures int       // consecutive ticks without a reading
	retryAt  time.Time // earliest next attempt to open the counter again
	attempts int       // reopens in a row that did not give a reading (backoff)
	dormant  atomic.Bool
}

// vmRedetectAfter is how many ticks in a row may give no reading before the group
// opens the counter again to see whether it still exists (about a minute at the
// 15 s base interval). Until then a bad tick is named in Missing, as before.
const vmRedetectAfter = 4

// vmReopenDelay is how long to wait before the next reopen after `attempts`
// reopens in a row that still gave no reading: 1, 2, 4 minutes, then the group
// retry ceiling. A counter that exists but keeps failing is visible in Missing
// the whole time; it is only the query churn that backs off.
func vmReopenDelay(attempts int) time.Duration {
	d := time.Minute
	for i := 1; i < attempts && d < groupRetryMax; i++ {
		d *= 2
	}
	if d > groupRetryMax {
		return groupRetryMax
	}
	return d
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

// Dormant reports that the counter object is gone and the group is silent.
func (g *vmGroup) Dormant() bool { return g.dormant.Load() }

func (g *vmGroup) Collect(now time.Time, _ float64) (any, error) {
	if g.src == nil {
		return g.redetect(now)
	}
	v, err := g.read(now)
	if v != nil {
		g.failures, g.attempts = 0, 0
		return v, nil
	}
	g.failures++
	if g.owned && g.failures >= vmRedetectAfter && !now.Before(g.retryAt) {
		return g.redetect(now)
	}
	return nil, err
}

// read takes one reading. A nil payload means "not read or implausible".
func (g *vmGroup) read(now time.Time) (*VMGroup, error) {
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

// redetect drops a source that keeps failing and opens the counter again. If the
// object is gone (Tools removed, VM moved) the group becomes not applicable and
// looks again every notApplicableRecheck, like a host that never had it. Another
// failure stays visible in Missing and is retried on the normal retry delay.
func (g *vmGroup) redetect(now time.Time) (any, error) {
	if now.Before(g.retryAt) {
		if g.dormant.Load() {
			return nil, errNotApplicable
		}
		return nil, errGroupUnavailable
	}
	if g.src != nil {
		_ = g.src.Close()
		g.src = nil
	}
	s, err := newVMSource([]string{vmBalloonPath})
	switch {
	case err == nil && s != nil:
		g.src, g.failures, g.owned = s, 0, true
		g.dormant.Store(false)
		v, rerr := g.read(now)
		if v == nil {
			// The counter exists but gives nothing: wait longer before the next reopen.
			g.attempts++
			g.retryAt = now.Add(vmReopenDelay(g.attempts))
			return nil, rerr
		}
		g.attempts = 0
		return v, nil
	case err != nil && errors.Is(err, errPDHAbsent):
		g.dormant.Store(true)
		g.attempts = 0
		g.retryAt = now.Add(notApplicableRecheck)
		return nil, errNotApplicable
	default:
		g.dormant.Store(false)
		g.attempts++
		g.retryAt = now.Add(vmReopenDelay(g.attempts))
		return nil, errGroupUnavailable
	}
}

// Close releases the source only when the factory created it.
func (g *vmGroup) Close() error {
	if g.owned && g.src != nil {
		return g.src.Close()
	}
	return nil
}
