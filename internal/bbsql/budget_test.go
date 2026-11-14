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

// C13 footprint gate. Every threshold is named here so the whole budget is
// visible in one place. Bounds are generous enough for noisy CI, but the test
// still fails if a row cap is raised or the runner starts leaking.
const (
	budgetTicks = 40
	// C13 is a CPU budget, not a wall-clock one: 1% of one core over a 15 s
	// tick is 150 ms of process CPU per tick. Wall time on a real host is
	// dominated by disk and antivirus latency, which costs no core time.
	budgetMeanTickCPU  = 150 * time.Millisecond
	budgetTickAllocCap = 10 << 20
	budgetGoroutineCap = 0
	// C13's "under 2 KB per sample" is judged against the REALISTIC tier: the
	// row counts a healthy instance actually returns.
	budgetMeanLineCap      = 2048
	budgetRealTickBytesCap = 16 << 10
	// The mean is C13's wording, but a mean hides one fat group, so the LARGEST
	// line is bounded too. The realistic max is set by the biggest group a
	// healthy instance fills (filestats at 40 files), so this bound is a trip
	// wire for "a row cap was raised", not a claim that every line is under
	// 2 KB - see the owner decision in the BBX-5-10 report: a 40-file realistic
	// group is already ~4.5 KB, so "2 KB per sample" only holds per average
	// sample, never per group at realistic counts.
	budgetRealMaxLineCap = 8 << 10
	// The AT-CAP tier is a storage-planning figure, not a claim about a normal
	// server: it is the largest tick the pinned row caps permit. It is bounded
	// so a cap raise cannot silently multiply hourly growth.
	budgetAtCapTickBytesCap = 64 << 10
	budgetAtCapMaxLineCap   = 40 << 10
	budgetAtCapTicks        = 4
	// C13's memory bound: the working set must not grow by 10 MB of steady
	// sampling. Measured as real RSS growth, because bytes allocated per tick
	// says nothing about what the service holds (maps, baselines, buffers).
	budgetMemoryTicks           = 60
	budgetWorkingSetGrowthCapMB = 10
)

// budgetSampler is a local double; the name is distinct from every other test
// file. It carries its own fixture rows so the table is the single source of
// truth for ids, caps and due scheduling.
type budgetSampler struct {
	id       string
	kind     string
	interval time.Duration
	limit    int
	rows     []Row
	decode   func([]Row) (any, bool, error)
}

func (s *budgetSampler) ID() string                        { return s.id }
func (s *budgetSampler) Kind() string                      { return s.kind }
func (s *budgetSampler) Interval() time.Duration           { return s.interval }
func (s *budgetSampler) Decode(r []Row) (any, bool, error) { return s.decode(r) }

func budgetDecode[T any](limit int) func([]Row) (any, bool, error) {
	return func(rows []Row) (any, bool, error) {
		var dst T
		truncated, err := decodeRows(rows, &dst, limit)
		return dst, truncated, err
	}
}

func budgetRows(n int, mk func(int) Row) []Row {
	rows := make([]Row, 0, n)
	for i := 0; i < n; i++ {
		rows = append(rows, mk(i))
	}
	return rows
}

// budgetCounts gives each kind the row count the fixtures should use.
type budgetCounts map[string]int

// atCapCounts fills every Cap* so the sizes measured are the worst the row
// caps permit.
func atCapCounts() budgetCounts {
	return budgetCounts{"requests": CapRequests, "waits": CapWaits,
		"schedulers": CapSchedulers, "semaphores": CapSemaphores, "tempdb": CapTempdb,
		"filestats": CapFileStats, "ag": CapAG}
}

// realisticCounts is what a healthy instance actually returns: a handful of
// wait types in use, one row per pool and per AG database. C13's 2 KB per
// sample is judged against THIS, not against a saturated fixture.
func realisticCounts() budgetCounts {
	return budgetCounts{"requests": 14, "waits": CapWaits, "schedulers": 1,
		"semaphores": 3, "tempdb": 1, "filestats": 40, "ag": 8}
}

// budgetRow builds one deterministic row of a kind (never rand).
func budgetRow(kind string, i int) Row {
	switch kind {
	case "requests":
		return Row{"wait_type": fmt.Sprintf("LATCH_%d", i), "tasks": int64(i),
			"max_wait_ms": int64(i * 3), "blocked": int64(i % 7),
			"head_blockers": int64(i % 5), "max_block_ms": int64(i * 2)}
	case "waits":
		return Row{"wait_type": fmt.Sprintf("PAGEIOLATCH_%d", i), "wait_time_ms": int64(i * 11),
			"waiting_tasks_count": int64(i % 4)}
	case "schedulers":
		return Row{"current_tasks": int64(i), "runnable_tasks": int64(i % 2)}
	case "semaphores":
		return Row{"resource_semaphore_id": int64(i), "grantee_count": int64(i % 3),
			"waiter_count": int64(i % 2), "forced_grant_count": int64(0), "timeout_count": int64(0)}
	case "tempdb":
		return Row{"internal_pages": int64(i), "user_pages": int64(i % 4)}
	case "filestats":
		return Row{"database_id": int64(i/8 + 1), "file_id": int64(i%8 + 1),
			"io_stall_read_ms": int64(i), "io_stall_write_ms": int64(i * 2),
			"num_of_reads": int64(i * 3), "num_of_writes": int64(i * 4),
			"size_on_disk_bytes": int64(i * 8192)}
	default: // ag
		return Row{"database_id": int64(i + 1), "synchronization_state": int64(i % 7)}
	}
}

// newBudgetSamplers builds the seven samplers with the given row counts. Values
// are deterministic; the caps in decodeRows stay at Cap* either way.
func newBudgetSamplers(counts budgetCounts) []*budgetSampler {
	specs := []struct {
		id, kind string
		interval time.Duration
		cap      int
		decode   func([]Row) (any, bool, error)
	}{
		{"bb:requests", "requests", BaseInterval, CapRequests, budgetDecode[[]Requests](CapRequests)},
		{"bb:waits", "waits", DemotedInterval, CapWaits, budgetDecode[[]Waits](CapWaits)},
		{"bb:schedulers", "schedulers", BaseInterval, CapSchedulers, budgetDecode[[]Schedulers](CapSchedulers)},
		{"bb:semaphores", "semaphores", BaseInterval, CapSemaphores, budgetDecode[[]Semaphores](CapSemaphores)},
		{"bb:tempdb", "tempdb", DemotedInterval, CapTempdb, budgetDecode[[]Tempdb](CapTempdb)},
		{"bb:filestats", "filestats", DemotedInterval, CapFileStats, budgetDecode[[]FileStats](CapFileStats)},
		{"bb:ag", "ag", DemotedInterval, CapAG, budgetDecode[[]AG](CapAG)},
	}
	out := make([]*budgetSampler, 0, len(specs))
	for _, sp := range specs {
		n := counts[sp.kind]
		out = append(out, &budgetSampler{sp.id, sp.kind, sp.interval, sp.cap,
			budgetRows(n, func(i int) Row { return budgetRow(sp.kind, i) }), sp.decode})
	}
	return out
}

func budgetSamplersFor(counts budgetCounts) []Sampler {
	list := newBudgetSamplers(counts)
	out := make([]Sampler, len(list))
	for i, s := range list {
		out[i] = s
	}
	return out
}

func budgetReplies(tb testing.TB, counts budgetCounts) map[string][]Row {
	tb.Helper()
	replies := make(map[string][]Row)
	// The runner runs CheckCeiling at every connect, so the fake must answer the
	// two control statements as the granted virtual account would: no privileged
	// fixed server role, and no permission outside the ceiling (the statement's
	// NOT IN filter returns nothing for a compliant login).
	roles := Row{}
	for _, name := range ceilingRoleNames {
		roles[name] = int64(0)
	}
	replies[Statements["bb:ceiling_role"]] = []Row{roles}
	replies[Statements["bb:ceiling_perms"]] = nil
	for _, s := range newBudgetSamplers(counts) {
		text, ok := Statement(s.id)
		if !ok {
			tb.Fatalf("statement %q missing from allowlist", s.id)
		}
		replies[text] = s.rows
	}
	return replies
}

type budgetQuery struct{ replies map[string][]Row }

func (q *budgetQuery) Query(ctx context.Context, text string) ([]Row, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return q.replies[text], nil
}

func newBudgetRunner(tb testing.TB, counts budgetCounts, closeFn func() error) *Runner {
	tb.Helper()
	dir := filepath.Join(tb.TempDir(), "sql")
	if err := os.MkdirAll(dir, 0700); err != nil {
		tb.Fatalf("mkdir sample dir: %v", err)
	}
	q := &budgetQuery{replies: budgetReplies(tb, counts)}
	if closeFn == nil {
		closeFn = func() error { return nil }
	}
	r := NewRunner(dir, ConnConfig{})
	// Inject the documented internal sampler list rather than the global
	// registry, so peer test files cannot perturb this measurement.
	r.samplers = budgetSamplersFor(counts)
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

// budgetStats is the NDJSON footprint of one budget run.
type budgetStats struct {
	lines       int
	meanLine    int
	maxLine     int
	meanTick    int
	worstTick   int
	perKind     map[string][]int
	perKindRows map[string][]int
}

func budgetMeasure(t *testing.T, dir string, ticks int) budgetStats {
	t.Helper()
	lines := budgetReadLines(t, dir)
	stats := budgetStats{perKind: map[string][]int{}, perKindRows: map[string][]int{}}
	perTick := map[time.Time]int{}
	for _, ln := range lines {
		stats.lines++
		perTick[ln.tick] += ln.size
		stats.perKind[ln.kind] = append(stats.perKind[ln.kind], ln.size)
		stats.perKindRows[ln.kind] = append(stats.perKindRows[ln.kind], ln.rows)
	}
	total := 0
	for _, ln := range lines {
		total += ln.size
		if ln.size > stats.maxLine {
			stats.maxLine = ln.size
		}
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

// TestSQLBudget is the C13 gate on the realistic tier: the row counts a healthy
// instance returns, over 40 ticks.
func TestSQLBudget(t *testing.T) {
	counts := realisticCounts()
	samplers := newBudgetSamplers(counts)
	closed := 0
	r := newBudgetRunner(t, counts, func() error {
		closed++
		return nil
	})
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
	t.Logf("per tick over %d ticks: cpu=%v (cap %v) wall=%v (not a budget: disk/AV latency)",
		budgetTicks, cpuPerTick/budgetTicks, budgetMeanTickCPU, wall/budgetTicks)
	if cpuPerTick/budgetTicks >= budgetMeanTickCPU {
		t.Errorf("mean tick uses %v of process CPU, want < %v (C13: 1%% of one core)", cpuPerTick/budgetTicks, budgetMeanTickCPU)
	}

	// One full all-sampler tick, reported as bytes allocated per tick.
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

	stats := budgetMeasure(t, r.Dir, budgetTicks)
	stats.log(t, samplers)

	if stats.meanLine >= budgetMeanLineCap {
		t.Errorf("mean sample line = %d bytes, want < %d (C13: under 2 KB per sample)", stats.meanLine, budgetMeanLineCap)
	}
	if stats.maxLine >= budgetRealMaxLineCap {
		t.Errorf("largest sample line = %d bytes, want < %d (one group cannot outgrow the mean)", stats.maxLine, budgetRealMaxLineCap)
	}
	if stats.meanTick >= budgetRealTickBytesCap {
		t.Errorf("mean tick writes %d bytes, want < %d", stats.meanTick, budgetRealTickBytesCap)
	}

	for _, sp := range samplers {
		if len(sp.rows) > sp.limit {
			t.Errorf("fixture %s has %d rows, over cap %d", sp.kind, len(sp.rows), sp.limit)
		}
		if len(stats.perKind[sp.kind]) == 0 {
			t.Errorf("no %s samples persisted", sp.kind)
			continue
		}
		for _, n := range stats.perKindRows[sp.kind] {
			if n > sp.limit {
				t.Errorf("persisted %s sample has %d rows, over cap %d (row cap raised?)", sp.kind, n, sp.limit)
			}
		}
		want := budgetTicks
		if sp.interval > BaseInterval {
			want = budgetTicks / 4
		}
		if got := len(stats.perKind[sp.kind]); got != want {
			t.Errorf("%s persisted %d lines, want %d (due scheduling)", sp.kind, got, want)
		}
	}
}

// TestSQLBudgetAtCapPayload bounds the worst tick the pinned row caps permit.
// A saturated instance is not the C13 normal case, but it must stay bounded and
// must stay covered by rotation: the daily figure is what the owner vets.
func TestSQLBudgetAtCapPayload(t *testing.T) {
	counts := atCapCounts()
	samplers := newBudgetSamplers(counts)
	r := newBudgetRunner(t, counts, nil)
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

	stats := budgetMeasure(t, r.Dir, budgetAtCapTicks)
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
	// Storage plan at this payload, each kind at its own cadence: a 60 s
	// sampler does not write on the three ticks in between, so multiplying one
	// all-hands worst tick by 240 would overstate the day. Each kind
	// contributes its largest at-cap line times its own ticks per day, which is
	// the figure the owner plans disk against.
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
	t.Logf("at-cap storage plan: %d B/day with every kind saturated at its own cadence (worst tick %d B, retention: %d hours)",
		perDay, stats.worstTick, r.keepHours())
}

// TestSQLBudgetWorkingSet is the C13 memory bound measured as real RSS, not as
// bytes allocated per tick: the sampler keeps per-file and per-wait baselines
// between ticks, and an allocation counter would pass while the service grew
// the map every cycle. The bound is on the GROWTH across many ticks, because a
// process's baseline working set is the Go runtime, not this code.
func TestSQLBudgetWorkingSet(t *testing.T) {
	before, ok := budgetWorkingSetBytes(t)
	if !ok {
		t.Skip("psapi working-set reading unavailable on this platform")
	}
	r := newBudgetRunner(t, realisticCounts(), nil)
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
	r := newBudgetRunner(t, realisticCounts(), func() error {
		closed++
		return nil
	})
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

// BenchmarkSQLTick measures one tick with every sampler due, at the realistic
// row counts (this is what TestSQLBudget judges allocations against).
func BenchmarkSQLTick(b *testing.B) {
	counts := realisticCounts()
	r := newBudgetRunner(b, counts, nil)
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

// BenchmarkSQLTickAtCap measures one tick at every Cap*: the worst the row caps
// permit. Reported, never a threshold, because a saturated instance is not the
// steady state C13 describes.
func BenchmarkSQLTickAtCap(b *testing.B) {
	counts := atCapCounts()
	r := newBudgetRunner(b, counts, nil)
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

// BenchmarkSQLTick60s measures the steady-state tick where only the 60s
// samplers are due.
func BenchmarkSQLTick60s(b *testing.B) {
	counts := realisticCounts()
	r := newBudgetRunner(b, counts, nil)
	now := time.Date(2026, 1, 2, 6, 0, 0, 0, time.UTC)
	r.Now = func() time.Time { return now }
	samplers := newBudgetSamplers(counts)
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
