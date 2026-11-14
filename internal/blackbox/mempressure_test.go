package blackbox

import (
	"errors"
	"testing"
	"time"
)

// The four fixed English PDH paths from the packet (UNVERIFIED pending the live
// typeperf check). The literals are asserted here so a path drift fails the test.
const (
	memHardFaultsPath   = `\Memory\Pages Input/sec`
	memPoolPagedPath    = `\Memory\Pool Paged Bytes`
	memPoolNonPagedPath = `\Memory\Pool Nonpaged Bytes`
	memPageFilePath     = `\Paging File(_Total)\% Usage`
)

// memPressureFakeSource is a fake CounterSource: canned readings, an optional
// collect error and Close bookkeeping. No PDH, so these tests run on any OS.
type memPressureFakeSource struct {
	readings []CounterReading
	err      error
	closed   bool
}

func (f *memPressureFakeSource) Collect() ([]CounterReading, error) { return f.readings, f.err }
func (f *memPressureFakeSource) Close() error                       { f.closed = true; return nil }

func memReading(path string, v float64) CounterReading {
	return CounterReading{Path: path, Value: v, Status: 0, OK: true}
}

func newMemPressureTestGroup(t *testing.T, src CounterSource) *memPressureGroup {
	t.Helper()
	g, err := newMemPressureGroup(GroupDeps{Source: src, Cores: 4})
	if err != nil {
		t.Fatalf("newMemPressureGroup: %v", err)
	}
	return g.(*memPressureGroup)
}

func collectMemPressure(t *testing.T, g *memPressureGroup, now time.Time) *MemPressureGroup {
	t.Helper()
	v, err := g.Collect(now, 15)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if v == nil {
		return nil
	}
	grp, ok := v.(*MemPressureGroup)
	if !ok {
		t.Fatalf("Collect returned %T, want *MemPressureGroup", v)
	}
	return grp
}

// Every valid counter lands in its own field; the rate is already per-second
// (no delta) and the byte gauges stay exact uint64 values. Machine-wide: a
// target selection must not filter anything.
func TestMemPressureMapsFields(t *testing.T) {
	src := &memPressureFakeSource{readings: []CounterReading{
		memReading(memHardFaultsPath, 6),
		memReading(memPoolPagedPath, 300<<20),
		memReading(memPoolNonPagedPath, 128<<20),
		memReading(memPageFilePath, 12.5),
	}}
	gi, err := newMemPressureGroup(GroupDeps{Source: src, Cores: 4, Targets: func() []ResolvedTarget {
		return []ResolvedTarget{{Key: "pool:pool-a", Pool: "pool-a"}}
	}})
	if err != nil {
		t.Fatalf("newMemPressureGroup: %v", err)
	}
	g := gi.(*memPressureGroup)
	now := time.Unix(1000, 0)
	var grp *MemPressureGroup
	for i := 0; i < 3; i++ { // two baselines, then the rate emits
		grp = collectMemPressure(t, g, now.Add(time.Duration(i)*15*time.Second))
		if grp == nil {
			t.Fatalf("tick %d: nil group with valid readings", i+1)
		}
	}
	if grp.HardFaultsPerSec == nil || *grp.HardFaultsPerSec != 6 {
		t.Fatalf("HardFaultsPerSec = %v, want 6", grp.HardFaultsPerSec)
	}
	if grp.PoolPagedBytes == nil || *grp.PoolPagedBytes != 300<<20 {
		t.Fatalf("PoolPagedBytes = %v, want %d", grp.PoolPagedBytes, 300<<20)
	}
	if grp.PoolNonPagedBytes == nil || *grp.PoolNonPagedBytes != 128<<20 {
		t.Fatalf("PoolNonPagedBytes = %v, want %d", grp.PoolNonPagedBytes, 128<<20)
	}
	if grp.PageFileUsagePct == nil || *grp.PageFileUsagePct != 12.5 {
		t.Fatalf("PageFileUsagePct = %v, want 12.5", grp.PageFileUsagePct)
	}
}

// An invalid or absent counter leaves its field nil, never 0; a VALID zero is a
// measurement and must still be emitted.
func TestMemPressureMissingOmitted(t *testing.T) {
	src := &memPressureFakeSource{readings: []CounterReading{
		memReading(memPoolPagedPath, 300<<20),
		{Path: memPoolNonPagedPath, Value: 999, Status: 1, OK: false}, // invalid
		{Path: memHardFaultsPath, Value: 42, Status: 1, OK: false},    // invalid rate
		// memPageFilePath is absent entirely.
	}}
	g := newMemPressureTestGroup(t, src)
	now := time.Unix(1000, 0)
	var grp *MemPressureGroup
	for i := 0; i < 3; i++ {
		grp = collectMemPressure(t, g, now.Add(time.Duration(i)*15*time.Second))
		if grp == nil {
			t.Fatalf("tick %d: nil group, want the one valid gauge", i+1)
		}
	}
	if grp.PoolPagedBytes == nil || *grp.PoolPagedBytes != 300<<20 {
		t.Fatalf("PoolPagedBytes = %v, want %d", grp.PoolPagedBytes, 300<<20)
	}
	if grp.PoolNonPagedBytes != nil {
		t.Fatalf("PoolNonPagedBytes = %v, want nil for an invalid counter (never 0)", *grp.PoolNonPagedBytes)
	}
	if grp.HardFaultsPerSec != nil {
		t.Fatalf("HardFaultsPerSec = %v, want nil for an invalid rate (never 0)", *grp.HardFaultsPerSec)
	}
	if grp.PageFileUsagePct != nil {
		t.Fatalf("PageFileUsagePct = %v, want nil for an absent counter (never 0)", *grp.PageFileUsagePct)
	}

	// A valid zero is present, the still-absent fields stay nil.
	src.readings = []CounterReading{memReading(memPoolPagedPath, 0)}
	grp = collectMemPressure(t, g, now.Add(45*time.Second))
	if grp == nil || grp.PoolPagedBytes == nil || *grp.PoolPagedBytes != 0 {
		t.Fatalf("valid zero gauge = %+v, want PoolPagedBytes 0 present", grp)
	}
	if grp.PoolNonPagedBytes != nil || grp.HardFaultsPerSec != nil || grp.PageFileUsagePct != nil {
		t.Fatalf("absent fields were zero-filled: %+v", grp)
	}
}

// Nothing valid (or nothing at all) omits the group; a source error is returned.
func TestMemPressureEmptyNil(t *testing.T) {
	src := &memPressureFakeSource{readings: []CounterReading{
		{Path: memPoolPagedPath, Value: 5, Status: 1, OK: false},
		{Path: memHardFaultsPath, Value: 6, Status: 1, OK: false},
	}}
	g := newMemPressureTestGroup(t, src)
	if grp := collectMemPressure(t, g, time.Unix(1000, 0)); grp != nil {
		t.Fatalf("all-invalid collect = %+v, want nil group", grp)
	}
	src.readings = nil // no readings at all: still nil, nil
	if grp := collectMemPressure(t, g, time.Unix(1015, 0)); grp != nil {
		t.Fatalf("empty readings = %+v, want nil group", grp)
	}
	src.err = errors.New("PDH collect failed")
	v, err := g.Collect(time.Unix(1030, 0), 15)
	if err == nil || v != nil {
		t.Fatalf("source error: (%v, %v), want (nil, error)", v, err)
	}
}

// Pages Input/sec is gated behind two VALID baselines; the gauges are not.
func TestMemPressureRateGated(t *testing.T) {
	src := &memPressureFakeSource{readings: []CounterReading{
		memReading(memHardFaultsPath, 10),
		memReading(memPoolPagedPath, 300<<20),
	}}
	g := newMemPressureTestGroup(t, src)
	t0 := time.Unix(1000, 0)

	grp := collectMemPressure(t, g, t0)
	if grp == nil || grp.HardFaultsPerSec != nil {
		t.Fatalf("sample 1 = %+v, want HardFaultsPerSec nil (first baseline)", grp)
	}
	if grp.PoolPagedBytes == nil {
		t.Fatal("sample 1: the gauge must not be baseline-gated")
	}
	src.readings[0].Value = 20
	grp = collectMemPressure(t, g, t0.Add(15*time.Second))
	if grp == nil || grp.HardFaultsPerSec != nil {
		t.Fatalf("sample 2 = %+v, want HardFaultsPerSec nil (second baseline)", grp)
	}
	src.readings[0].Value = 30
	grp = collectMemPressure(t, g, t0.Add(30*time.Second))
	if grp == nil || grp.HardFaultsPerSec == nil || *grp.HardFaultsPerSec != 30 {
		t.Fatalf("sample 3 = %+v, want HardFaultsPerSec 30 (PDH rate as-is, no delta)", grp)
	}

	// An invalid sample never advances the baselines: with tick 2 invalid the
	// rate needs two valid samples (ticks 1 and 3) and first emits on tick 4.
	src2 := &memPressureFakeSource{readings: []CounterReading{memReading(memHardFaultsPath, 10)}}
	g2 := newMemPressureTestGroup(t, src2)
	for i, valid := range []bool{true, false, true, true} {
		if valid {
			src2.readings[0] = memReading(memHardFaultsPath, 10)
		} else {
			src2.readings[0] = CounterReading{Path: memHardFaultsPath, Value: 10, Status: 1, OK: false}
		}
		grp := collectMemPressure(t, g2, t0.Add(time.Duration(i)*15*time.Second))
		if i < 3 {
			if grp != nil {
				t.Fatalf("tick %d = %+v, want nil group (rate still gated)", i+1, grp)
			}
			continue
		}
		if grp == nil || grp.HardFaultsPerSec == nil || *grp.HardFaultsPerSec != 10 {
			t.Fatalf("tick 4 = %+v, want HardFaultsPerSec 10 after two valid baselines", grp)
		}
	}
}

// The group is registered mandatory (optional=false) from init().
func TestMemPressureRegisteredMandatory(t *testing.T) {
	registryMu.Lock()
	reg, ok := registry["memPressure"]
	registryMu.Unlock()
	if !ok {
		t.Fatal("memPressure not registered from init()")
	}
	if reg.optional {
		t.Fatal("memPressure registered optional, want mandatory (false)")
	}
	g := newMemPressureTestGroup(t, &memPressureFakeSource{})
	if g.Name() != "memPressure" || g.Optional() {
		t.Fatalf("Name/Optional = %q/%v, want memPressure/false", g.Name(), g.Optional())
	}
}