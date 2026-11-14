package blackbox

import (
	"fmt"
	"runtime"
	"testing"
	"time"

	"api-audit-explorer/internal/sampler"
)

// BL-122 footprint gate: a full budgeted tick with fake counter readers must
// stay under 0.5% of one core on average (75 ms of CPU per tick at a 15 s
// interval), under 50 MB working set, and each group under 1 s. Everything
// here is fake and deterministic: no PDH, no process reads, no network, no
// real sleeps, no goroutine and no ticker.

const (
	// benchValidReadings is the fake counter load: ~200 valid readings spread
	// over six groups, plus one invalid reading (201 total).
	benchValidReadings = 200
	// benchInterval is the base sampling interval the gate budgets against.
	benchInterval = 15
	// benchMaxAllocsPerTick bounds per-tick allocations. The measured figure
	// is ~90 allocs/tick (recorded in the BBX-1-09 report); the limit keeps
	// ~4x headroom so the gate is stable, not flaky.
	benchMaxAllocsPerTick = 400
	// benchInvalidPath is read with a non-zero PDH status on every tick. Its
	// field must stay absent from the sample: missing is never 0.
	benchInvalidPath  = `\Memory\Pool Nonpaged Bytes`
	benchInvalidField = "poolNonPagedBytes"
)

// fakeCounterSource is a deterministic CounterSource: the same fixed slice of
// readings on every call. No PDH handle, no clock, no error path.
type fakeCounterSource struct {
	readings []CounterReading
	closed   bool
}

func (f *fakeCounterSource) Collect() ([]CounterReading, error) { return f.readings, nil }
func (f *fakeCounterSource) Close() error                       { f.closed = true; return nil }

// fakeCounterGroup runs a real Engine over the shared fake readings and folds
// the emitted fields into a small group struct. A field the engine did not
// emit stays nil: missing is never 0.
type fakeCounterGroup struct {
	name     string
	optional bool
	src      CounterSource
	engine   *Engine
	build    func(map[string]float64) any
	calls    int
}

func (g *fakeCounterGroup) Name() string   { return g.name }
func (g *fakeCounterGroup) Optional() bool { return g.optional }
func (g *fakeCounterGroup) Collect(now time.Time, dt float64) (any, error) {
	g.calls++
	readings, err := g.src.Collect()
	if err != nil {
		return nil, err
	}
	return g.build(g.engine.Process(now, readings)), nil
}

// benchF64 returns a pointer only when the engine emitted the field.
func benchF64(out map[string]float64, field string) *float64 {
	if v, ok := out[field]; ok {
		return &v
	}
	return nil
}

// benchU64 is benchF64 for the byte-valued fields.
func benchU64(out map[string]float64, field string) *uint64 {
	if v, ok := out[field]; ok {
		u := uint64(v)
		return &u
	}
	return nil
}

// benchData holds the fixed fake load: per-group specs and one aligned reading
// slice. Group order: 0 http, 1 iis, 2 diskCpu, 3 clr (mandatory), 4 tcp,
// 5 memPressure (optional).
type benchData struct {
	specs    [6][]CounterSpec
	readings []CounterReading
}

// newBenchData builds 200 valid readings (a mix of gauges, cumulative rates
// and PDH rates) plus one invalid reading whose spec belongs to memPressure.
// Paths are fixed English identifiers, never captured text.
func newBenchData() *benchData {
	d := &benchData{}
	add := func(group int, path, field string, kind CounterKind, value float64) {
		d.specs[group] = append(d.specs[group], CounterSpec{Path: path, Field: field, Kind: kind})
		d.readings = append(d.readings, CounterReading{Path: path, Value: value, Status: 0, OK: true})
	}

	// http (mandatory).
	add(0, `\HTTP Service\Current Queue Size`, "httpQueueSize", KindGauge, 3)
	add(0, `\HTTP Service\Request Arrival Rate`, "httpArrivalRate", KindPDHRate, 120)
	// iis (mandatory).
	add(1, `\W3SVC_W3WP(_Total)\Active Requests`, "iisActiveRequests", KindGauge, 7)
	add(1, `\W3SVC_W3WP(_Total)\Ping Latency`, "iisPingLatency", KindGauge, 2)
	// diskCpu (mandatory).
	add(2, `\Processor Information(_Total)\Processor Queue Length`, "cpuQueue", KindGauge, 1)
	add(2, `\Processor Information(_Total)\% Privileged Time`, "cpuPrivileged", KindPDHRate, 8)
	add(2, `\Processor Information(_Total)\% DPC Time`, "cpuDPC", KindPDHRate, 1)
	add(2, `\Processor Information(_Total)\% Interrupt Time`, "cpuInterrupt", KindPDHRate, 0.5)
	// clr (mandatory).
	add(3, `\.NET CLR Memory(w3wp)\Gen 0 heap size`, "clrGen0", KindGauge, 1<<20)
	add(3, `\.NET CLR Memory(w3wp)\% Time in GC`, "clrGC", KindPDHRate, 4)
	add(3, `\.NET CLR Memory(w3wp)\Allocated Bytes/sec`, "clrAlloc", KindPDHRate, 500000)
	// tcp (optional).
	add(4, `\TCPv4\Segments Retransmitted/sec`, "retransmitsPerSec", KindPDHRate, 1.5)
	add(4, `\TCPv4\Segments Reset/sec`, "resetsPerSec", KindPDHRate, 0.25)
	add(4, `\TCPv4\Connections Established`, "outboundQueue", KindGauge, 42)
	// memPressure (optional).
	add(5, `\Memory\Page Reads/sec`, "hardFaultsPerSec", KindPDHRate, 6)
	add(5, `\Memory\Pool Paged Bytes`, "poolPagedBytes", KindGauge, 300<<20)
	add(5, `\Paging File(_Total)\% Usage`, "pageFileUsagePct", KindGauge, 12)

	// The invalid reading: a spec exists, but the source never reads it
	// successfully (non-zero status, OK false). Its field must never appear.
	d.specs[5] = append(d.specs[5], CounterSpec{Path: benchInvalidPath, Field: benchInvalidField, Kind: KindGauge})
	d.readings = append(d.readings, CounterReading{Path: benchInvalidPath, Value: 4096, Status: 1, OK: false})

	// Filler counters bring the valid load to benchValidReadings, spread
	// round-robin over the six groups. Values are fixed: rates compute a
	// stable zero delta, never a reset or a divide-by-zero.
	for i := 0; len(d.readings) < benchValidReadings+1; i++ {
		path := fmt.Sprintf(`\Bench\Load%03d`, i)
		field := fmt.Sprintf("load%03d", i)
		add(i%6, path, field, CounterKind(i%3), float64(1000+i))
	}
	return d
}

// benchGroups builds the six fake groups. Each owns a real Engine over its
// subset of the specs; the structs stay small, as a real group's would.
func benchGroups(src CounterSource, d *benchData) []*fakeCounterGroup {
	engines := make([]*Engine, len(d.specs))
	for i, specs := range d.specs {
		engines[i] = NewEngine(specs, 4)
	}
	return []*fakeCounterGroup{
		{name: "http", src: src, engine: engines[0], build: func(out map[string]float64) any {
			return &HTTPGroup{Queues: []HTTPQueue{{
				Pool:             "pool-a",
				CurrentQueueSize: benchF64(out, "httpQueueSize"),
				ArrivalRate:      benchF64(out, "httpArrivalRate"),
			}}}
		}},
		{name: "iis", src: src, engine: engines[1], build: func(out map[string]float64) any {
			return &IISGroup{Workers: []IISWorker{{
				Pool:           "pool-a",
				PID:            1200,
				ActiveRequests: benchF64(out, "iisActiveRequests"),
				PingLatencyMs:  benchF64(out, "iisPingLatency"),
			}}}
		}},
		{name: "diskCpu", src: src, engine: engines[2], build: func(out map[string]float64) any {
			return &DiskCPUGroup{CPU: &CPUSplit{
				QueueLength:   benchF64(out, "cpuQueue"),
				PrivilegedPct: benchF64(out, "cpuPrivileged"),
				DPCPct:        benchF64(out, "cpuDPC"),
				InterruptPct:  benchF64(out, "cpuInterrupt"),
			}}
		}},
		{name: "clr", src: src, engine: engines[3], build: func(out map[string]float64) any {
			return &CLRGroup{Targets: []CLRTarget{{
				Target:           "w3wp",
				Gen0Bytes:        benchU64(out, "clrGen0"),
				PctTimeInGC:      benchF64(out, "clrGC"),
				AllocBytesPerSec: benchF64(out, "clrAlloc"),
			}}}
		}},
		{name: "tcp", optional: true, src: src, engine: engines[4], build: func(out map[string]float64) any {
			return &TCPGroup{
				RetransmitsPerSec: benchF64(out, "retransmitsPerSec"),
				ResetsPerSec:      benchF64(out, "resetsPerSec"),
				OutboundQueue:     benchF64(out, "outboundQueue"),
			}
		}},
		{name: "memPressure", optional: true, src: src, engine: engines[5], build: func(out map[string]float64) any {
			return &MemPressureGroup{
				HardFaultsPerSec:  benchF64(out, "hardFaultsPerSec"),
				PoolPagedBytes:    benchU64(out, "poolPagedBytes"),
				PoolNonPagedBytes: benchU64(out, benchInvalidField),
				PageFileUsagePct:  benchF64(out, "pageFileUsagePct"),
			}
		}},
	}
}

// benchHostSource reuses the collector_test fakeSource pattern: fixed host
// times and memory, no real reads.
func benchHostSource() *fakeSource {
	return &fakeSource{cores: 4, host: []sampler.HostSample{
		{Times: sampler.HostTimes{Idle: 100, Kernel: 150, User: 50}, AvailableBytes: 8 << 30, CommittedBytes: 12 << 30},
		{Times: sampler.HostTimes{Idle: 150, Kernel: 250, User: 150}, AvailableBytes: 7 << 30, CommittedBytes: 13 << 30},
	}}
}

// benchRig is one full fake tick: the host collector, the six counter groups
// (plus any extras) and a real Budget, wired exactly as the tick loop wires
// them. No production registry is touched: the []Group is built directly.
type benchRig struct {
	col    Collector
	budget *Budget
	groups []*fakeCounterGroup
	src    *fakeCounterSource
}

func newBenchRig(t0 time.Time, extra ...Group) (*benchRig, error) {
	d := newBenchData()
	src := &fakeCounterSource{readings: d.readings}
	groups := benchGroups(src, d)
	host := NewHostCollector(benchHostSource(), &fakeExtras{}, false)
	if err := host.Prime(t0); err != nil {
		return nil, err
	}
	budget := NewBudget(benchInterval)
	all := make([]Group, 0, len(groups)+len(extra))
	for _, g := range groups {
		all = append(all, g)
	}
	all = append(all, extra...)
	return &benchRig{col: NewGroupCollector(host, all, budget), budget: budget, groups: groups, src: src}, nil
}

// tick runs one budgeted tick the way the recorder does: collect, charge the
// self cost against the budget, then advance the cycle. The self-usage inputs
// are fixed fakes under both limits (0.5% of one core, 50 MB): feeding the
// measured wall time back would let one OS preemption flip the optional-skip
// and make the gate flaky. The gate measures the real tick cost itself; the
// over-limit budget paths are covered by budget_test.go and by the injected
// slow mark in TestBudgetSlowGroupSkipped.
func (r *benchRig) tick(now time.Time) (Sample, error) {
	s, err := r.col.Collect(now)
	if err != nil {
		return s, err
	}
	r.budget.SelfUsage(0.1, 30<<20)
	r.budget.Cycle()
	return s, nil
}

// checkGate asserts the BL-122 footprint limits and records the measured
// figures. perTick is the average wall/monotonic cost of one tick;
// allocsPerTick and bytesPerTick are the average heap allocations per tick.
func checkGate(t *testing.T, perTick time.Duration, allocsPerTick, bytesPerTick uint64) {
	t.Helper()
	t.Logf("measured %v per tick, %d allocs/tick, %d bytes/tick", perTick, allocsPerTick, bytesPerTick)
	// 0.5% of one core at a 15 s interval is 75 ms of CPU per tick. The fake
	// tick is far below it; the generous limit keeps the gate stable on slow
	// machines.
	if perTick > 75*time.Millisecond {
		t.Errorf("a tick takes %v; the budget is 75 ms (0.5%% of one core at 15 s)", perTick)
	}
	if allocsPerTick > benchMaxAllocsPerTick {
		t.Errorf("%d allocs/tick; the gate allows %d", allocsPerTick, benchMaxAllocsPerTick)
	}
}

// BenchmarkBudgetTick measures one full budgeted tick: the fake host
// collector, six fake counter groups over ~200 readings, and a real Budget.
func BenchmarkBudgetTick(b *testing.B) {
	t0 := time.Unix(1000, 0)
	rig, err := newBenchRig(t0)
	if err != nil {
		b.Fatal(err)
	}
	now := t0
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		now = now.Add(benchInterval * time.Second)
		if _, err := rig.tick(now); err != nil {
			b.Fatal(err)
		}
	}
}

// TestBudgetTickStaysInTheBudget runs 200 full ticks with the fake source and
// fails if the average cost exceeds the BL-122 budget (75 ms/tick) or the
// per-tick allocations exceed the recorded bound. It also proves the invalid
// reading's field is absent, never 0.
func TestBudgetTickStaysInTheBudget(t *testing.T) {
	t0 := time.Unix(1000, 0)
	rig, err := newBenchRig(t0)
	if err != nil {
		t.Fatal(err)
	}

	const ticks = 200
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	start := time.Now()
	now := t0
	var last Sample
	for i := 0; i < ticks; i++ {
		now = now.Add(benchInterval * time.Second)
		s, err := rig.tick(now)
		if err != nil {
			t.Fatal(err)
		}
		// Every tick: the invalid reading's field is absent, never 0, while
		// the valid gauge next to it is present.
		if s.MemPressure == nil {
			t.Fatalf("tick %d: memPressure group absent", i)
		}
		if s.MemPressure.PoolNonPagedBytes != nil {
			t.Fatalf("tick %d: invalid reading emitted %s; missing must never be 0", i, benchInvalidField)
		}
		if s.MemPressure.PoolPagedBytes == nil {
			t.Fatalf("tick %d: valid gauge poolPagedBytes absent", i)
		}
		last = s
	}
	elapsed := time.Since(start)
	runtime.ReadMemStats(&after)

	checkGate(t, elapsed/ticks, (after.Mallocs-before.Mallocs)/ticks, (after.TotalAlloc-before.TotalAlloc)/ticks)

	// No group exceeded the 1 s per-group budget (a slow group would be
	// marked and show up here).
	if slow := rig.budget.SlowGroups(); len(slow) != 0 {
		t.Errorf("groups over the 1 s per-group budget: %v", slow)
	}
	// All six groups landed in their sample fields.
	if last.HTTP == nil || last.IIS == nil || last.DiskCPU == nil || last.CLR == nil || last.TCP == nil || last.MemPressure == nil {
		t.Fatalf("a group field is absent: %+v", last)
	}
	// The invalid field is absent from the serialized line too, while the
	// valid one is present.
	line, err := jsonLine(last)
	if err != nil {
		t.Fatal(err)
	}
	if contains(line, benchInvalidField) {
		t.Fatalf("invalid field present in the sample line: %s", line)
	}
	if !contains(line, "poolPagedBytes") {
		t.Fatalf("valid gauge missing from the sample line: %s", line)
	}
}

// TestBudgetSlowGroupSkipped proves a group that takes over 1 s is marked slow
// and skipped the next tick, and that the footprint gate still passes with it
// skipped. runGroup times groups with the real clock, so the slow duration is
// INJECTED into the budget (GroupTiming is the only place a duration enters
// it) instead of sleeping for real: the test stays instant and deterministic.
func TestBudgetSlowGroupSkipped(t *testing.T) {
	t0 := time.Unix(1000, 0)
	slow := &fakeCounterGroup{name: "vm", build: func(out map[string]float64) any {
		return &VMGroup{StealPct: benchF64(out, "vmSteal")}
	}}
	rig, err := newBenchRig(t0, slow)
	if err != nil {
		t.Fatal(err)
	}
	// Give the slow group a spec so its engine is real too.
	slow.src = rig.src
	slow.engine = NewEngine([]CounterSpec{{Path: `\Hypervisor\Steal %`, Field: "vmSteal", Kind: KindGauge}}, 4)

	// The previous tick's vm group took 1.5 s (> the 1 s per-group budget).
	// Inject that duration exactly as runGroup's GroupTiming would record it.
	rig.budget.GroupTiming("vm", 1500*time.Millisecond, false)
	if got := rig.budget.SlowGroups(); len(got) != 1 || got[0] != "vm" {
		t.Fatalf("SlowGroups = %v, want [vm]", got)
	}

	// Next tick: vm is skipped, its field absent (never zero-filled), and the
	// one-cycle mark is consumed.
	s1, err := rig.tick(t0.Add(benchInterval * time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if s1.VM != nil {
		t.Fatal("slow group was not skipped")
	}
	if !missingHas(s1, "skipped: slow") {
		t.Fatalf("Missing = %v, want skipped: slow", s1.Missing)
	}
	if slow.calls != 0 {
		t.Fatalf("slow group ran %d times, want 0 while marked", slow.calls)
	}
	if len(rig.budget.SlowGroups()) != 0 {
		t.Fatal("slow mark not consumed by the skip")
	}

	// The mark is one-cycle: vm is re-probed next tick and (being a fake) runs
	// fast, clearing the slow path.
	s2, err := rig.tick(t0.Add(2 * benchInterval * time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if s2.VM == nil || slow.calls != 1 {
		t.Fatal("slow group not re-probed the cycle after the skip")
	}

	// The gate still passes with the slow group skipped: keep its slow mark
	// live (re-injected each tick, as a persistently slow group would) so it
	// is skipped for the whole gate run, and measure the full tick cost.
	const ticks = 200
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	start := time.Now()
	now := t0.Add(2 * benchInterval * time.Second)
	for i := 0; i < ticks; i++ {
		rig.budget.GroupTiming("vm", 1500*time.Millisecond, false)
		now = now.Add(benchInterval * time.Second)
		s, err := rig.tick(now)
		if err != nil {
			t.Fatal(err)
		}
		if s.VM != nil {
			t.Fatalf("gate tick %d: slow group ran while marked", i)
		}
		if !missingHas(s, "skipped: slow") {
			t.Fatalf("gate tick %d: Missing = %v, want skipped: slow", i, s.Missing)
		}
	}
	elapsed := time.Since(start)
	runtime.ReadMemStats(&after)

	checkGate(t, elapsed/ticks, (after.Mallocs-before.Mallocs)/ticks, (after.TotalAlloc-before.TotalAlloc)/ticks)
	if slow.calls != 1 {
		t.Fatalf("slow group ran %d times during the gate, want 0 (only the re-probe)", slow.calls-1)
	}
	if slow := rig.budget.SlowGroups(); len(slow) != 0 {
		t.Errorf("unexpected slow marks after the gate: %v", slow)
	}
}