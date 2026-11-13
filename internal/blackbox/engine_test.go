package blackbox

import (
	"testing"
	"time"
)

// English PDH paths used by the fake readings below. The engine is path-agnostic;
// these are fixed identifiers, never captured text.
const (
	gaugePath = "\\Memory\\Available MBytes"
	ratePath  = "\\PhysicalDisk(_Total)\\Disk Reads/sec"
	pdhPath   = "\\Processor(_Total)\\% Processor Time"
)

func TestEngineGaugeEmitsImmediately(t *testing.T) {
	e := NewEngine([]CounterSpec{{Path: gaugePath, Field: "mem", Kind: KindGauge}}, 1)
	now := time.Unix(1000, 0)
	out := e.Process(now, []CounterReading{{Path: gaugePath, Value: 42, Status: 0, OK: true}})
	v, ok := out["mem"]
	if !ok {
		t.Fatalf("gauge field absent on first valid sample, want present")
	}
	if v != 42 {
		t.Fatalf("gauge value = %v, want 42 (unchanged)", v)
	}
}

func TestEngineMultiCoreNotCapped(t *testing.T) {
	// A 4-core process at 350% must stay 350: no clamp to 100 anywhere.
	e := NewEngine([]CounterSpec{{Path: pdhPath, Field: "cpu", Kind: KindGauge}}, 4)
	now := time.Unix(1000, 0)
	out := e.Process(now, []CounterReading{{Path: pdhPath, Value: 350, Status: 0, OK: true}})
	v, ok := out["cpu"]
	if !ok {
		t.Fatalf("multi-core gauge absent, want present")
	}
	if v != 350 {
		t.Fatalf("multi-core gauge = %v, want 350 (not capped at 100)", v)
	}
}

func TestEngineRateNeedsTwoBaselines(t *testing.T) {
	e := NewEngine([]CounterSpec{{Path: ratePath, Field: "reads", Kind: KindRate}}, 1)
	t0 := time.Unix(1000, 0)

	// Sample 1: first baseline, emits nothing.
	out1 := e.Process(t0, []CounterReading{{Path: ratePath, Value: 100, OK: true}})
	if _, ok := out1["reads"]; ok {
		t.Fatalf("sample 1 emitted a rate, want absent (first baseline)")
	}
	// Sample 2: second baseline, still emits nothing.
	out2 := e.Process(t0.Add(2*time.Second), []CounterReading{{Path: ratePath, Value: 160, OK: true}})
	if _, ok := out2["reads"]; ok {
		t.Fatalf("sample 2 emitted a rate, want absent (second baseline)")
	}
	// Sample 3: first rate = (v3 - v2) / dt = (260 - 160) / 2 = 50.
	out3 := e.Process(t0.Add(4*time.Second), []CounterReading{{Path: ratePath, Value: 260, OK: true}})
	v, ok := out3["reads"]
	if !ok {
		t.Fatalf("sample 3 absent, want first rate after two baselines")
	}
	if v != 50 {
		t.Fatalf("rate = %v, want 50 ((260-160)/2s)", v)
	}
}

func TestEnginePDHRateGated(t *testing.T) {
	e := NewEngine([]CounterSpec{{Path: pdhPath, Field: "cpu", Kind: KindPDHRate}}, 4)
	t0 := time.Unix(1000, 0)

	out1 := e.Process(t0, []CounterReading{{Path: pdhPath, Value: 10, OK: true}})
	if _, ok := out1["cpu"]; ok {
		t.Fatalf("PDH rate sample 1 emitted, want absent (first baseline)")
	}
	out2 := e.Process(t0.Add(time.Second), []CounterReading{{Path: pdhPath, Value: 20, OK: true}})
	if _, ok := out2["cpu"]; ok {
		t.Fatalf("PDH rate sample 2 emitted, want absent (second baseline)")
	}
	out3 := e.Process(t0.Add(2*time.Second), []CounterReading{{Path: pdhPath, Value: 30, OK: true}})
	v, ok := out3["cpu"]
	if !ok {
		t.Fatalf("PDH rate sample 3 absent, want present after two baselines")
	}
	if v != 30 {
		t.Fatalf("PDH rate = %v, want 30 (already per-second, emitted as-is)", v)
	}
}

func TestEngineInvalidStatusOmitted(t *testing.T) {
	e := NewEngine([]CounterSpec{
		{Path: gaugePath, Field: "mem", Kind: KindGauge},
		{Path: ratePath, Field: "reads", Kind: KindRate},
	}, 1)
	t0 := time.Unix(1000, 0)

	// Non-zero status: omitted, never 0.
	out := e.Process(t0, []CounterReading{{Path: gaugePath, Value: 5, Status: 1, OK: true}})
	if _, ok := out["mem"]; ok {
		t.Fatalf("invalid-status gauge emitted, want absent")
	}
	// OK=false: omitted too.
	out = e.Process(t0, []CounterReading{{Path: gaugePath, Value: 5, Status: 0, OK: false}})
	if _, ok := out["mem"]; ok {
		t.Fatalf("OK=false gauge emitted, want absent")
	}

	// Invalid rate readings must NOT advance the baseline count.
	e.Process(t0, []CounterReading{{Path: ratePath, Value: 1, Status: 5, OK: true}})
	e.Process(t0.Add(time.Second), []CounterReading{{Path: ratePath, Value: 2, Status: 0, OK: false}})

	// If the invalids had counted as baselines, the first valid below would emit.
	// It must still be the first baseline (absent), proving invalids were skipped.
	o1 := e.Process(t0.Add(2*time.Second), []CounterReading{{Path: ratePath, Value: 10, OK: true}})
	if _, ok := o1["reads"]; ok {
		t.Fatalf("first valid after invalids emitted; invalid readings advanced baselines")
	}
	o2 := e.Process(t0.Add(3*time.Second), []CounterReading{{Path: ratePath, Value: 20, OK: true}})
	if _, ok := o2["reads"]; ok {
		t.Fatalf("second valid emitted, want absent (second baseline)")
	}
	o3 := e.Process(t0.Add(4*time.Second), []CounterReading{{Path: ratePath, Value: 40, OK: true}})
	if _, ok := o3["reads"]; !ok {
		t.Fatalf("third valid absent, want rate after two valid baselines")
	}
}

func TestEngineCounterResetSkips(t *testing.T) {
	e := NewEngine([]CounterSpec{{Path: ratePath, Field: "reads", Kind: KindRate}}, 1)
	t0 := time.Unix(1000, 0)

	e.Process(t0, []CounterReading{{Path: ratePath, Value: 100, OK: true}})                  // baseline 1
	e.Process(t0.Add(time.Second), []CounterReading{{Path: ratePath, Value: 200, OK: true}}) // baseline 2
	o := e.Process(t0.Add(2*time.Second), []CounterReading{{Path: ratePath, Value: 300, OK: true}})
	if _, ok := o["reads"]; !ok {
		t.Fatalf("expected a rate before the reset")
	}

	// Cumulative value drops 300 -> 50: a reset. No negative rate, emission skipped.
	o = e.Process(t0.Add(3*time.Second), []CounterReading{{Path: ratePath, Value: 50, OK: true}})
	if v, ok := o["reads"]; ok {
		t.Fatalf("reset emitted rate %v, want absent (no negative rate)", v)
	}

	// Baseline restarted: the next valid sample is a fresh baseline, not a rate.
	o = e.Process(t0.Add(4*time.Second), []CounterReading{{Path: ratePath, Value: 80, OK: true}})
	if _, ok := o["reads"]; ok {
		t.Fatalf("post-reset sample emitted, want absent (baseline must restart)")
	}
}

func TestEngineZeroDtGuard(t *testing.T) {
	e := NewEngine([]CounterSpec{{Path: ratePath, Field: "reads", Kind: KindRate}}, 1)
	t0 := time.Unix(1000, 0)

	e.Process(t0, []CounterReading{{Path: ratePath, Value: 100, OK: true}})                  // baseline 1
	e.Process(t0.Add(time.Second), []CounterReading{{Path: ratePath, Value: 200, OK: true}}) // baseline 2 @ t0+1s

	// Third sample at the SAME timestamp as baseline 2: dt == 0. Must not divide
	// by zero and must not emit.
	o := e.Process(t0.Add(time.Second), []CounterReading{{Path: ratePath, Value: 300, OK: true}})
	if _, ok := o["reads"]; ok {
		t.Fatalf("zero-dt sample emitted, want absent (no divide-by-zero)")
	}
}

func TestEngineMissingNeverZero(t *testing.T) {
	e := NewEngine([]CounterSpec{
		{Path: gaugePath, Field: "mem", Kind: KindGauge},
		{Path: pdhPath, Field: "cpu", Kind: KindGauge},
	}, 1)
	now := time.Unix(1000, 0)

	// Only the "mem" path has a reading; "cpu" has none this cycle.
	out := e.Process(now, []CounterReading{{Path: gaugePath, Value: 7, OK: true}})
	if _, ok := out["mem"]; !ok {
		t.Fatalf("present gauge missing, want emitted")
	}
	// A spec with no reading must produce NO field: absent, never 0.
	if v, ok := out["cpu"]; ok {
		t.Fatalf("missing reading produced field %v, want absent (never 0)", v)
	}
}