package bbsql

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// C13 footprint gate (W10-05 / W11-08). This file benchmarks the ACTUAL
// production samplers — fresh instances with their real baseline maps, gates and
// decode paths — driven through a fake Queryer that materializes rows per query
// (the reader/scanning allocation seam) and evolves counters, churns keys, and
// injects null/reset/HADR-off cases. It does NOT substitute stateless decoder
// doubles: a regression in retained baseline state or decode work now runs here.
//
// Every threshold is named so the whole budget is visible in one place. The CPU,
// allocation and memory bounds are hard assertions. The per-sample BYTE bound is
// measured literally and reported (see TestSQLBudgetSampleByteBound): C13's
// "under 2 KB per sample" is unreachable at the documented C6 row caps (a 256-row
// group is several KB), so it is flagged for an explicit owner decision rather
// than silently relaxed into a selected-fixture mean.

const (
	budgetTicks = 40
	// C13 is a CPU budget, not a wall-clock one: 1% of one core over a 15 s tick
	// is 150 ms of process CPU per tick.
	budgetMeanTickCPU  = 150 * time.Millisecond
	budgetTickAllocCap = 10 << 20
	budgetGoroutineCap = 0
	// C13's literal per-sample byte bound. Reported, not relaxed (owner decision).
	c13SampleByteBound = 2048
	// Storage-planning trip wires for the at-cap tier (a row-cap raise guard).
	budgetAtCapTickBytesCap = 128 << 10
	budgetAtCapMaxLineCap   = 64 << 10
	budgetAtCapTicks        = 4
	// C13's memory bound: working-set growth across many ticks, measured as real
	// RSS because bytes-allocated-per-tick says nothing about retained maps.
	budgetMemoryTicks           = 60
	budgetWorkingSetGrowthCapMB = 10
)

// budgetCounts gives each kind the row count the fixtures should use.
type budgetCounts map[string]int

func atCapCounts() budgetCounts {
	return budgetCounts{"requests": CapRequests, "waits": CapWaits,
		"schedulers": CapSchedulers, "semaphores": CapSemaphores, "tempdb": CapTempdb,
		"filestats": CapFileStats, "ag": CapAG}
}

func realisticCounts() budgetCounts {
	return budgetCounts{"requests": 14, "waits": CapWaits, "schedulers": 1,
		"semaphores": 3, "tempdb": 1, "filestats": 40, "ag": 8}
}

// budgetSampler is a fixture/metadata holder wrapping a REAL production sampler.
// The anti-drift test reads id/kind/interval/limit; the fake Queryer reads rows.
// Decode delegates to the real sampler so any direct use exercises real work. It
// deliberately does NOT implement Gate (the runner injects the real samplers,
// whose ag carries the HADR gate, so non-ag kinds are not accidentally gated).
type budgetSampler struct {
	id       string
	kind     string
	interval time.Duration
	limit    int
	rows     []Row
	real     Sampler
}

func (s *budgetSampler) ID() string                        { return s.id }
func (s *budgetSampler) Kind() string                      { return s.kind }
func (s *budgetSampler) Interval() time.Duration           { return s.interval }
func (s *budgetSampler) Decode(r []Row) (any, bool, error) { return s.real.Decode(r) }

// budgetKinds is the shipped sampler set, in tick (Kind-sorted) order.
var budgetKinds = []string{"ag", "filestats", "requests", "schedulers", "semaphores", "tempdb", "waits"}

// freshProductionSamplers returns a FRESH instance of each shipped sampler from
// the registry factories (W11-07), keyed by kind. Test-registered throwaway kinds
// are ignored.
func freshProductionSamplers() map[string]Sampler {
	want := map[string]bool{}
	for _, k := range budgetKinds {
		want[k] = true
	}
	out := map[string]Sampler{}
	for _, s := range Samplers() {
		if want[s.Kind()] {
			out[s.Kind()] = s
		}
	}
	return out
}

// freshProductionSamplerList returns the real samplers in Kind-sorted tick order.
func freshProductionSamplerList() []Sampler {
	byKind := freshProductionSamplers()
	out := make([]Sampler, 0, len(budgetKinds))
	for _, k := range budgetKinds {
		if s, ok := byKind[k]; ok {
			out = append(out, s)
		}
	}
	return out
}

// maxWaitName is a 60-character wait type (the C9 maximum width).
var maxWaitName = "W" + strings.Repeat("A", 59) // 1 + 59 = 60

// budgetRow builds one deterministic realistic row of a kind, with counters that
// grow with the cycle so delta samplers emit real differences.
func budgetRow(kind string, i, cycle int) Row {
	c := int64(cycle + 1)
	switch kind {
	case "requests":
		return Row{"wait_type": fmt.Sprintf("LATCH_%d", i), "tasks": int64(i) + c,
			"max_wait_ms": int64(i*3) + c, "blocked": int64(i % 7),
			"head_blockers": int64(i % 5), "max_block_ms": int64(i*2) + c}
	case "waits":
		return Row{"wait_type": fmt.Sprintf("PAGEIOLATCH_%d", i), "wait_time_ms": int64(i*11) + c*100,
			"waiting_tasks_count": int64(i%4) + c}
	case "schedulers":
		return Row{"current_tasks": int64(i) + c, "runnable_tasks": int64(i%2) + c%2}
	case "semaphores":
		return Row{"resource_semaphore_id": int64(i), "grantee_count": int64(i % 3),
			"waiter_count": int64(i % 2), "forced_grant_count": c * 10, "timeout_count": c * 3}
	case "tempdb":
		return Row{"internal_pages": int64(i) + c, "user_pages": int64(i%4) + c}
	case "filestats":
		return Row{"database_id": int64(i/8 + 1), "file_id": int64(i%8 + 1),
			"io_stall_read_ms": int64(i) + c*100, "io_stall_write_ms": int64(i*2) + c*100,
			"num_of_reads": int64(i*3) + c, "num_of_writes": int64(i*4) + c,
			"size_on_disk_bytes": int64(i * 8192)}
	default: // ag
		return Row{"database_id": int64(i + 1), "synchronization_state": int64(i % 5)}
	}
}

// budgetRowMax builds one MAXIMUM-WIDTH row: 60-char wait names and the largest
// supported numeric values, so the at-cap measurement is a worst-case width, not
// just a worst-case row count (W11-08). Cumulative semaphore counters exceed
// MaxInt32 to exercise the wide decode path; deltas stay small (cycle-grown).
func budgetRowMax(kind string, i, cycle int) Row {
	c := int64(cycle + 1)
	switch kind {
	case "requests":
		return Row{"wait_type": maxWaitName, "tasks": int64(4294967295),
			"max_wait_ms": int64(9223372036854775807), "blocked": int64(4294967295),
			"head_blockers": int64(4294967295), "max_block_ms": int64(9223372036854775807)}
	case "waits":
		return Row{"wait_type": maxWaitName, "wait_time_ms": int64(9223372036854775807) - c,
			"waiting_tasks_count": int64(9223372036854775807) - c}
	case "schedulers":
		return Row{"current_tasks": int64(4294967295), "runnable_tasks": int64(4294967295)}
	case "semaphores":
		// Lifetime counters above MaxInt32; a small per-cycle delta.
		return Row{"resource_semaphore_id": int64(i), "grantee_count": int64(2147483647),
			"waiter_count": int64(2147483647), "forced_grant_count": int64(2147483648) + c,
			"timeout_count": int64(2147483648) + c}
	case "tempdb":
		return Row{"internal_pages": int64(9223372036854775807), "user_pages": int64(9223372036854775807)}
	case "filestats":
		return Row{"database_id": int64(4294967295), "file_id": int64(4294967295),
			"io_stall_read_ms": int64(9223372036854775807) - c, "io_stall_write_ms": int64(9223372036854775807) - c,
			"num_of_reads": int64(9223372036854775807) - c, "num_of_writes": int64(9223372036854775807) - c,
			"size_on_disk_bytes": int64(9223372036854775807)}
	default: // ag
		return Row{"database_id": int64(4294967295), "synchronization_state": int64(i % 5)}
	}
}

func budgetRowsFor(kind string, n, cycle int, wide bool) []Row {
	rows := make([]Row, 0, n)
	for i := 0; i < n; i++ {
		if wide {
			rows = append(rows, budgetRowMax(kind, i, cycle))
		} else {
			rows = append(rows, budgetRow(kind, i, cycle))
		}
	}
	return rows
}

// newBudgetSamplers builds fixture holders wrapping fresh REAL samplers, with the
// row counts and caps the anti-drift test checks against the registry.
func newBudgetSamplers(counts budgetCounts) []*budgetSampler {
	real := freshProductionSamplers()
	caps := map[string]int{"requests": CapRequests, "waits": CapWaits, "schedulers": CapSchedulers,
		"semaphores": CapSemaphores, "tempdb": CapTempdb, "filestats": CapFileStats, "ag": CapAG}
	out := make([]*budgetSampler, 0, len(budgetKinds))
	for _, kind := range budgetKinds {
		s, ok := real[kind]
		if !ok {
			continue
		}
		out = append(out, &budgetSampler{
			id: s.ID(), kind: kind, interval: s.Interval(), limit: caps[kind],
			rows: budgetRowsFor(kind, counts[kind], 0, false), real: s,
		})
	}
	return out
}

// copyRows materializes a fresh copy of the rows per query, approximating the
// reader/scanning allocations of DBQueryer.Query (which builds a new Row map and
// scans values for every row). The real TDS/driver cost still needs a live server.
func copyRows(rows []Row) []Row {
	out := make([]Row, len(rows))
	for i, r := range rows {
		m := make(Row, len(r))
		for k, v := range r {
			m[k] = v
		}
		out[i] = m
	}
	return out
}

// budgetQuery is the fake transport. It answers the ceiling and HADR gate, and
// generates evolving rows per query (fresh allocations). The requests statement
// is due every tick, so it marks a cycle boundary; counters grow, keys churn, and
// null/reset/HADR-off cases are injected periodically to exercise the real
// samplers' baseline, reset and gate paths.
type budgetQuery struct {
	counts budgetCounts
	wide   bool
	cycle  int
}

func (q *budgetQuery) Query(ctx context.Context, text string) ([]Row, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch text {
	case Statements["bb:ceiling_role"]:
		row := Row{}
		for _, name := range ceilingRoleNames {
			row[name] = int64(0)
		}
		return []Row{row}, nil
	case Statements["bb:ceiling_perms"]:
		return nil, nil
	case Statements["bb:hadr"]:
		// HADR enabled most cycles; disabled every 13th to exercise the gate.
		v := int64(1)
		if q.cycle%13 == 12 {
			v = 0
		}
		return []Row{{"hadr": v}}, nil
	case Statements["bb:requests"]:
		q.cycle++
	}
	kind, ok := kindForStatement(text)
	if !ok {
		return nil, nil
	}
	rows := budgetRowsFor(kind, q.counts[kind], q.cycle, q.wide)
	// Periodic null/reset/churn injections on the real decode paths.
	if kind == "semaphores" && q.cycle%7 == 6 && len(rows) > 0 {
		rows[0]["forced_grant_count"] = nil // unreadable counter beside readable gauges
	}
	if kind == "waits" && q.cycle%11 == 10 && len(rows) > 0 {
		rows[0]["wait_time_ms"] = int64(1) // counter reset (backwards)
		rows[0]["waiting_tasks_count"] = int64(1)
	}
	if kind == "filestats" && q.cycle%5 == 4 {
		// Key churn: shift database ids so baselines are rebuilt, not merged.
		// Wrap within uint32 so the maximum-width fixture cannot overflow.
		for _, r := range rows {
			r["database_id"] = (r["database_id"].(int64) + int64(q.cycle)) & 0xFFFFFFFF
		}
	}
	return copyRows(rows), nil
}

func kindForStatement(text string) (string, bool) {
	for id, kind := range map[string]string{
		"bb:requests": "requests", "bb:waits": "waits", "bb:schedulers": "schedulers",
		"bb:semaphores": "semaphores", "bb:tempdb": "tempdb", "bb:filestats": "filestats", "bb:ag": "ag",
	} {
		if Statements[id] == text {
			return kind, true
		}
	}
	return "", false
}

func newBudgetRunner(tb testing.TB, counts budgetCounts, wide bool, closeFn func() error) *Runner {
	tb.Helper()
	dir := filepath.Join(tb.TempDir(), "sql")
	if err := os.MkdirAll(dir, 0700); err != nil {
		tb.Fatalf("mkdir sample dir: %v", err)
	}
	q := &budgetQuery{counts: counts, wide: wide}
	if closeFn == nil {
		closeFn = func() error { return nil }
	}
	r := NewRunner(dir, ConnConfig{})
	// Inject the REAL production samplers (fresh instances, ag carries the HADR
	// gate), not decoder doubles (W10-05/W11-08).
	r.samplers = freshProductionSamplerList()
	r.lastRun = make(map[string]time.Time)
	r.Connect = func(ConnConfig) (Queryer, func() error, error) {
		return q, closeFn, nil
	}
	return r
}

type budgetLine struct {
	kind string
	size int
	tick time.Time
	rows int
}

func budgetReadLines(t *testing.T, dir string) []budgetLine {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, "bb-sql-*.ndjson"))
	if err != nil {
		t.Fatalf("glob samples: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("no sql ndjson files were written")
	}
	var lines []budgetLine
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read samples: %v", err)
		}
		for _, raw := range strings.Split(string(data), "\n") {
			raw = strings.TrimSuffix(raw, "\r")
			if raw == "" {
				continue
			}
			var doc struct {
				V    int             `json:"v"`
				T    time.Time       `json:"t"`
				Kind string          `json:"kind"`
				Rows json.RawMessage `json:"rows"`
			}
			if err := json.Unmarshal([]byte(raw), &doc); err != nil {
				t.Fatalf("bad ndjson line: %v", err)
			}
			if doc.V != 1 {
				t.Fatalf("sample version = %d, want 1", doc.V)
			}
			var rowList []json.RawMessage
			_ = json.Unmarshal(doc.Rows, &rowList)
			lines = append(lines, budgetLine{doc.Kind, len(raw), doc.T, len(rowList)})
		}
	}
	if len(lines) == 0 {
		t.Fatal("no sample lines")
	}
	return lines
}

func budgetReadStatus(t *testing.T, dir string) StatusDoc {
	t.Helper()
	data, err := os.ReadFile(StatusPath(dir))
	if err != nil {
		t.Fatalf("read status: %v", err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var doc StatusDoc
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("status shape (C11): %v", err)
	}
	return doc
}

func budgetMaxMean(values []int) (int, int) {
	if len(values) == 0 {
		return 0, 0
	}
	max, total := 0, 0
	for _, v := range values {
		total += v
		if v > max {
			max = v
		}
	}
	return max, total / len(values)
}

type budgetStats struct {
	lines       int
	meanLine    int
	maxLine     int
	meanTick    int
	worstTick   int
	perKind     map[string][]int
	perKindRows map[string][]int
}

func budgetMeasure(t *testing.T, dir string) budgetStats {
	t.Helper()
	lines := budgetReadLines(t, dir)
	stats := budgetStats{perKind: map[string][]int{}, perKindRows: map[string][]int{}}
	perTick := map[time.Time]int{}
	for _, ln := range lines {
		stats.lines++
		perTick[ln.tick] += ln.size
		stats.perKind[ln.kind] = append(stats.perKind[ln.kind], ln.size)
		stats.perKindRows[ln.kind] = append(stats.perKindRows[ln.kind], ln.rows)
		if ln.size > stats.maxLine {
			stats.maxLine = ln.size
		}
	}
	total := 0
	for _, ln := range lines {
		total += ln.size
	}
	stats.meanLine = total / stats.lines
	for _, b := range perTick {
		stats.meanTick += b
		if b > stats.worstTick {
			stats.worstTick = b
		}
	}
	stats.meanTick /= len(perTick)
	return stats
}

func (s budgetStats) log(t *testing.T, samplers []*budgetSampler) {
	t.Helper()
	for _, sp := range samplers {
		maxSize, meanSize := budgetMaxMean(s.perKind[sp.kind])
		t.Logf("kind=%-10s lines=%3d max=%6d mean=%6d rowsCap=%d", sp.kind, len(s.perKind[sp.kind]), maxSize, meanSize, sp.limit)
	}
	t.Logf("samples: lines=%d meanLine=%d maxLine=%d meanTickBytes=%d worstTickBytes=%d",
		s.lines, s.meanLine, s.maxLine, s.meanTick, s.worstTick)
}

// TestSQLBudget is the C13 CPU/allocation gate on the realistic tier, running the
// real samplers over 40 evolving ticks.
func TestSQLBudget(t *testing.T) {
	counts := realisticCounts()
	samplers := newBudgetSamplers(counts)
	closed := 0
	r := newBudgetRunner(t, counts, false, func() error { closed++; return nil })
	now := time.Date(2026, 1, 2, 3, 0, 0, 0, time.UTC)
	r.Now = func() time.Time { return now }

	cpuBefore := budgetProcessCPU(t)
	var wall time.Duration
	for i := 0; i < budgetTicks; i++ {
		start := time.Now()
		if err := r.Tick(now); err != nil {
			t.Fatalf("tick %d: %v", i, err)
		}
		wall += time.Since(start)
		now = now.Add(BaseInterval)
	}
	cpuPerTick := budgetProcessCPU(t) - cpuBefore
	t.Logf("per tick over %d ticks: cpu=%v (cap %v) wall=%v (not a budget)",
		budgetTicks, cpuPerTick/budgetTicks, budgetMeanTickCPU, wall/budgetTicks)
	if cpuPerTick/budgetTicks >= budgetMeanTickCPU {
		t.Errorf("mean tick uses %v of process CPU, want < %v (C13: 1%% of one core)", cpuPerTick/budgetTicks, budgetMeanTickCPU)
	}

	bench := testing.Benchmark(BenchmarkSQLTick)
	t.Logf("allocations: %d B/tick, %d allocs/tick (cap %d B)", bench.AllocedBytesPerOp(), bench.AllocsPerOp(), budgetTickAllocCap)
	if bench.AllocedBytesPerOp() >= budgetTickAllocCap {
		t.Errorf("tick allocates %d bytes, want < %d", bench.AllocedBytesPerOp(), budgetTickAllocCap)
	}
	if closed != 0 {
		t.Errorf("connection closer called %d times across successful ticks, want 0", closed)
	}

	status := budgetReadStatus(t, r.Dir)
	if !status.Enabled || status.State != StateOn || status.Failures != 0 || status.LastOK == nil {
		t.Errorf("status = %+v, want enabled/on/0/lastOK", status)
	}

	stats := budgetMeasure(t, r.Dir)
	stats.log(t, samplers)
	for _, sp := range samplers {
		if len(stats.perKind[sp.kind]) == 0 {
			t.Errorf("no %s samples persisted", sp.kind)
			continue
		}
		for _, n := range stats.perKindRows[sp.kind] {
			if n > sp.limit {
				t.Errorf("persisted %s sample has %d rows, over cap %d", sp.kind, n, sp.limit)
			}
		}
	}
}

// TestSQLBudgetAtCapPayload bounds the worst tick the pinned row caps permit, at
// MAXIMUM numeric/string width (W11-08), running the real samplers.
func TestSQLBudgetAtCapPayload(t *testing.T) {
	counts := atCapCounts()
	samplers := newBudgetSamplers(counts)
	r := newBudgetRunner(t, counts, true, nil)
	now := time.Date(2026, 1, 2, 7, 0, 0, 0, time.UTC)
	r.Now = func() time.Time { return now }

	cpuBefore := budgetProcessCPU(t)
	for i := 0; i < budgetAtCapTicks; i++ {
		if err := r.Tick(now); err != nil {
			t.Fatalf("tick %d: %v", i, err)
		}
		now = now.Add(BaseInterval)
	}
	cpuPerTick := (budgetProcessCPU(t) - cpuBefore) / budgetAtCapTicks
	t.Logf("at-cap tick cpu=%v (cap %v)", cpuPerTick, budgetMeanTickCPU)
	if cpuPerTick >= budgetMeanTickCPU {
		t.Errorf("at-cap tick uses %v of process CPU, want < %v", cpuPerTick, budgetMeanTickCPU)
	}

	stats := budgetMeasure(t, r.Dir)
	stats.log(t, samplers)
	if stats.worstTick >= budgetAtCapTickBytesCap {
		t.Errorf("at-cap tick writes %d bytes, want < %d", stats.worstTick, budgetAtCapTickBytesCap)
	}
	if stats.maxLine >= budgetAtCapMaxLineCap {
		t.Errorf("at-cap largest sample line = %d bytes, want < %d (a row cap was raised?)", stats.maxLine, budgetAtCapMaxLineCap)
	}
	for _, sp := range samplers {
		for _, n := range stats.perKindRows[sp.kind] {
			if n > sp.limit {
				t.Errorf("at-cap %s sample has %d rows, over cap %d", sp.kind, n, sp.limit)
			}
		}
	}
	perDay := 0
	for _, sp := range samplers {
		ticksPerDay := 240 * 24
		if sp.interval > BaseInterval {
			ticksPerDay = 60 * 24
		}
		kindMax := 0
		for _, b := range stats.perKind[sp.kind] {
			if b > kindMax {
				kindMax = b
			}
		}
		perDay += kindMax * ticksPerDay
	}
	t.Logf("at-cap storage plan: %d B/day at maximum width, each kind at its own cadence (worst tick %d B, retention %d hours)",
		perDay, stats.worstTick, r.keepHours())
}

// TestSQLBudgetSampleByteBound measures the LITERAL per-sample byte bound and
// reports it against C13's "under 2 KB per sample". It does NOT relax the bound
// into a mean: the largest realistic and at-cap lines are reported as-is. The
// documented C6 row caps (e.g. 256 filestats rows) make a single group several KB,
// so the 2 KB-per-sample requirement is UNMET at those caps and needs an explicit
// owner decision (enforce a destructive per-line cap, or revise C13). This test
// records the measured numbers and flags the conflict; it is not a pass claim on
// a relaxed bound (W10-05/W11-08).
func TestSQLBudgetSampleByteBound(t *testing.T) {
	for _, tier := range []struct {
		name   string
		counts budgetCounts
		wide   bool
	}{
		{"realistic", realisticCounts(), false},
		{"at-cap-max-width", atCapCounts(), true},
	} {
		r := newBudgetRunner(t, tier.counts, tier.wide, nil)
		now := time.Date(2026, 1, 2, 9, 0, 0, 0, time.UTC)
		r.Now = func() time.Time { return now }
		for i := 0; i < 8; i++ {
			if err := r.Tick(now); err != nil {
				t.Fatalf("%s tick %d: %v", tier.name, i, err)
			}
			now = now.Add(BaseInterval)
		}
		stats := budgetMeasure(t, r.Dir)
		t.Logf("[%s] literal max sample line = %d bytes; C13 per-sample bound = %d bytes", tier.name, stats.maxLine, c13SampleByteBound)
		if stats.maxLine >= c13SampleByteBound {
			t.Logf("[%s] C13 2KB-per-sample UNMET (max %d B): the C6 row caps make a full group exceed 2 KB. "+
				"Requires an explicit owner decision (per-line enforcement vs C13 revision); NOT silently relaxed.", tier.name, stats.maxLine)
		}
	}
}

// TestSQLBudgetWorkingSet is the C13 memory bound measured as real RSS growth over
// many ticks of the REAL samplers (retained per-file/per-wait/per-semaphore
// baselines), with churn and null/reset cases exercising map rebuilds.
func TestSQLBudgetWorkingSet(t *testing.T) {
	before, ok := budgetWorkingSetBytes(t)
	if !ok {
		t.Skip("psapi working-set reading unavailable on this platform")
	}
	r := newBudgetRunner(t, realisticCounts(), false, nil)
	now := time.Date(2026, 1, 2, 8, 0, 0, 0, time.UTC)
	r.Now = func() time.Time { return now }
	for i := 0; i < budgetMemoryTicks; i++ {
		if err := r.Tick(now); err != nil {
			t.Fatalf("tick %d: %v", i, err)
		}
		now = now.Add(BaseInterval)
	}
	runtime.GC()
	after, ok := budgetWorkingSetBytes(t)
	if !ok {
		t.Skip("psapi working-set reading unavailable on this platform")
	}
	growth := after - before
	const cap = budgetWorkingSetGrowthCapMB << 20
	t.Logf("working set: %d -> %d bytes over %d ticks (growth %d, cap %d)", before, after, budgetMemoryTicks, growth, cap)
	if growth >= cap {
		t.Errorf("sampling grew the working set by %d bytes, want < %d (C13: under 10 MB extra memory)", growth, cap)
	}
}

func TestSQLBudgetNoGoroutineGrowth(t *testing.T) {
	closed := 0
	r := newBudgetRunner(t, realisticCounts(), false, func() error { closed++; return nil })
	now := time.Date(2026, 1, 2, 4, 0, 0, 0, time.UTC)
	r.Now = func() time.Time { return now }

	before := runtime.NumGoroutine()
	for i := 0; i < 20; i++ {
		if err := r.Tick(now); err != nil {
			t.Fatalf("tick %d: %v", i, err)
		}
		now = now.Add(BaseInterval)
	}
	runtime.GC()
	after := runtime.NumGoroutine()
	t.Logf("goroutines before=%d after=%d", before, after)
	if after > before+budgetGoroutineCap {
		t.Errorf("goroutines grew %d -> %d across 20 ticks", before, after)
	}
	if r.Skipped() != 0 {
		t.Errorf("skipped = %d, want 0 (ticks were sequential)", r.Skipped())
	}
	if closed != 0 {
		t.Errorf("connection closer called %d times across 20 successful ticks, want 0", closed)
	}
	paths, err := filepath.Glob(filepath.Join(r.Dir, "bb-sql-*.ndjson"))
	if err != nil {
		t.Fatalf("glob samples: %v", err)
	}
	if len(paths) != 1 {
		t.Errorf("20 ticks inside one UTC hour wrote %d hourly files, want 1 (file reuse)", len(paths))
	}
}

// BenchmarkSQLTick measures one all-sampler tick at realistic counts through the
// real samplers and the materializing fake transport.
func BenchmarkSQLTick(b *testing.B) {
	r := newBudgetRunner(b, realisticCounts(), false, nil)
	now := time.Date(2026, 1, 2, 5, 0, 0, 0, time.UTC)
	r.Now = func() time.Time { return now }
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		now = now.Add(BaseInterval)
		r.lastRun = make(map[string]time.Time)
		b.StartTimer()
		if err := r.Tick(now); err != nil {
			b.Fatalf("tick: %v", err)
		}
	}
}

// BenchmarkSQLTickAtCap measures one tick at every Cap* and maximum width.
func BenchmarkSQLTickAtCap(b *testing.B) {
	r := newBudgetRunner(b, atCapCounts(), true, nil)
	now := time.Date(2026, 1, 2, 5, 30, 0, 0, time.UTC)
	r.Now = func() time.Time { return now }
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		now = now.Add(BaseInterval)
		r.lastRun = make(map[string]time.Time)
		b.StartTimer()
		if err := r.Tick(now); err != nil {
			b.Fatalf("tick: %v", err)
		}
	}
}

// BenchmarkSQLTick60s measures the steady tick where only the 60 s samplers run.
func BenchmarkSQLTick60s(b *testing.B) {
	r := newBudgetRunner(b, realisticCounts(), false, nil)
	now := time.Date(2026, 1, 2, 6, 0, 0, 0, time.UTC)
	r.Now = func() time.Time { return now }
	samplers := newBudgetSamplers(realisticCounts())
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		now = now.Add(BaseInterval)
		r.lastRun = make(map[string]time.Time)
		for _, s := range samplers {
			if s.interval <= BaseInterval {
				r.lastRun[s.kind] = now
			}
		}
		b.StartTimer()
		if err := r.Tick(now); err != nil {
			b.Fatalf("tick: %v", err)
		}
	}
}
