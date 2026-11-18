package blackbox

import (
	"testing"
	"time"
)

// W1-01: PDH reports two success data statuses. VALID_DATA and NEW_DATA are both
// usable; treating NEW_DATA as an error drops exactly the samples where a
// counter changed, so a rate can never reach its two baselines.
func TestCounterStatusOK(t *testing.T) {
	for _, tc := range []struct {
		status uint32
		want   bool
	}{
		{0x00000000, true},  // PDH_CSTATUS_VALID_DATA
		{0x00000001, true},  // PDH_CSTATUS_NEW_DATA
		{0x800007D4, false}, // PDH_CSTATUS_INVALID_DATA
		{0x800007D1, false}, // PDH_CSTATUS_NO_INSTANCE
		{0x800007D3, false}, // PDH_CSTATUS_NO_COUNTER
		{0xC0000BBB, false},
		{5, false},
	} {
		if got := CounterStatusOK(tc.status); got != tc.want {
			t.Errorf("CounterStatusOK(0x%X) = %v, want %v", tc.status, got, tc.want)
		}
	}
}

func TestEngineNewDataIsUsable(t *testing.T) {
	const gp, rp, pp = `\Memory\Available MBytes`, `\PhysicalDisk(0 C:)\Disk Reads/sec`, `\Processor(_Total)\% DPC Time`
	e := NewEngine([]CounterSpec{
		{Path: gp, Field: "mem", Kind: KindGauge},
		{Path: rp, Field: "reads", Kind: KindRate},
		{Path: pp, Field: "dpc", Kind: KindPDHRate},
	}, 4)
	t0 := time.Unix(1000, 0)
	newData := func(path string, v float64) CounterReading {
		return CounterReading{Path: path, Value: v, Status: 1, OK: true}
	}

	if out := e.Process(t0, []CounterReading{newData(gp, 512)}); out["mem"] != 512 {
		t.Fatalf("NEW_DATA gauge = %v, want 512 (a success status, not missing)", out["mem"])
	}
	// Two NEW_DATA baselines then a third: the rate must appear.
	e.Process(t0, []CounterReading{newData(rp, 100), newData(pp, 3)})
	e.Process(t0.Add(15*time.Second), []CounterReading{newData(rp, 250), newData(pp, 4)})
	out := e.Process(t0.Add(30*time.Second), []CounterReading{newData(rp, 400), newData(pp, 5)})
	if got := out["reads"]; got != 10 {
		t.Errorf("NEW_DATA rate = %v, want 10 ((400-250)/15s)", got)
	}
	if got := out["dpc"]; got != 5 {
		t.Errorf("NEW_DATA PDH rate = %v, want 5", got)
	}
}

// W1-02: a refresh that changes nothing relevant must not re-prime a rate.
// Twenty consecutive process-set changes with an unchanged counter used to leave
// the field absent forever while the gauges still looked healthy.
func TestEngineReconcileKeepsStableBaselines(t *testing.T) {
	const p = `\PhysicalDisk(0 C:)\Disk Reads/sec`
	specs := []CounterSpec{{Path: p, Field: "reads", Kind: KindRate}}
	e := NewEngine(specs, 4)
	t0 := time.Unix(1000, 0)
	read := func(v float64) CounterReading { return CounterReading{Path: p, Value: v, OK: true} }

	e.Process(t0, []CounterReading{read(100)})
	e.Process(t0.Add(15*time.Second), []CounterReading{read(250)})
	if out := e.Process(t0.Add(30*time.Second), []CounterReading{read(400)}); out["reads"] != 10 {
		t.Fatalf("warm-up rate = %v, want 10", out["reads"])
	}

	value := 400.0
	for i := 1; i <= 20; i++ {
		now := t0.Add(time.Duration(30+15*i) * time.Second)
		e.Reconcile(specs) // unrelated process churn: same disk, same counter
		value += 150
		out := e.Process(now, []CounterReading{read(value)})
		if got, ok := out["reads"]; !ok || got != 10 {
			t.Fatalf("after churn %d: rate = %v (present %v), want 10 every cycle", i, got, ok)
		}
	}
}

// A volatile instance name, a removed path and a changed kind must all lose
// their history: keeping it would attribute one entity's counters to another.
func TestEngineReconcileDropsUnsafeHistory(t *testing.T) {
	const stable, volatile = `\PhysicalDisk(0 C:)\Disk Reads/sec`, `\Process(w3wp)\IO Read Bytes/sec`
	e := NewEngine([]CounterSpec{
		{Path: stable, Field: "reads", Kind: KindRate},
		{Path: volatile, Field: "procReads", Kind: KindRate, Volatile: true},
	}, 4)
	t0 := time.Unix(1000, 0)
	for i := 0; i < 3; i++ {
		now := t0.Add(time.Duration(i*15) * time.Second)
		e.Process(now, []CounterReading{
			{Path: stable, Value: float64(100 + i*150), OK: true},
			{Path: volatile, Value: float64(100 + i*150), OK: true},
		})
	}
	if len(e.history) != 2 {
		t.Fatalf("history entries = %d, want 2 before reconcile", len(e.history))
	}

	// Same volatile path, still volatile: its baseline must be gone.
	e.Reconcile([]CounterSpec{
		{Path: stable, Field: "reads", Kind: KindRate},
		{Path: volatile, Field: "procReads", Kind: KindRate, Volatile: true},
	})
	if _, kept := e.history[volatile]; kept {
		t.Error("volatile instance kept its baseline across a refresh")
	}
	if _, kept := e.history[stable]; !kept {
		t.Error("stable counter lost its baseline across a refresh")
	}

	// A removed path must not leak state, and a changed kind must re-prime.
	e.Reconcile([]CounterSpec{{Path: stable, Field: "reads", Kind: KindPDHRate}})
	if len(e.history) != 0 {
		t.Fatalf("history entries = %d, want 0 (removed path dropped, kind change re-primes)", len(e.history))
	}
}

// The same guarantee through the real group: disk/CPU rates keep flowing while
// the wildcards are re-expanded on every collect (here the ticks are one refresh
// period apart, so every collect after the first refreshes).
func TestDiskCPURatesSurviveRefresh(t *testing.T) {
	p := `\Processor(_Total)\% DPC Time`
	src := &fakeDiskSource{paths: []string{p}, readings: []CounterReading{okReading(p, 7)}}
	g := newTestDiskCPU(src)
	t0 := time.Unix(1000, 0)
	step := wildcardRefreshEvery

	collectDiskCPU(t, g, t0)
	collectDiskCPU(t, g, t0.Add(step))
	for i := 2; i <= 21; i++ {
		grp := collectDiskCPU(t, g, t0.Add(time.Duration(i)*step))
		if grp == nil || grp.CPU == nil || grp.CPU.DPCPct == nil {
			t.Fatalf("tick %d: DPCPct absent under continuous refresh, want a rate after warm-up", i)
		}
		if *grp.CPU.DPCPct != 7 {
			t.Fatalf("tick %d: DPCPct = %v, want 7", i, *grp.CPU.DPCPct)
		}
	}
	if src.refreshes != 21 {
		t.Errorf("refreshes = %d, want 21 (the refresh still runs; it just no longer resets rates)", src.refreshes)
	}
}
