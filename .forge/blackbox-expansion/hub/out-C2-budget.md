```go
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
	budgetTicks        = 40
	budgetMeanTickCap  = 150 * time.Millisecond // 1% of one core over a 15s tick, rounded up
	budgetTickAllocCap = 10 << 20               // C13: < 10 MB extra live memory per tick
	// C13's 2 KB figure is per SAMPLE GROUP (one NDJSON line) after the row caps.
	// The caps allow 256-row payloads, so the honest per-tick budget is hourly
	// file growth. Per-kind sizes below are logged for the owner to veto.
	budgetMaxTickBytes = 24 << 10
	budgetMeanLineCap  = 2048
	budgetGoroutineCap = 0
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

func budgetRows(n int, make func(int) Row) []Row {
	rows := make([]Row, 0, n)
	for i := 0; i < n; i++ {
		rows = append(rows, make(i))
	}
	return rows
}

// newBudgetSamplers is exactly at every Cap* so the fixtures measure the worst
// case the row caps permit. Values are deterministic (never rand).
func newBudgetSamplers() []*budgetSampler {
	return []*budgetSampler{
		{"bb:requests", "requests", DemotedInterval, CapRequests,
			budgetRows(CapRequests, func(i int) Row {
				return Row{
					"wait_type":     fmt.Sprintf("LATCH_%d", i),
					"tasks":         int64(i),
					"max_wait_ms":   int64(i * 3),
					"blocked":       int64(i % 7),
					"head_blockers": int64(i % 5),
					"max_block_ms":  int64(i * 2),
				}
			}), budgetDecode[Requests](CapRequests)},
		{"bb:waits", "waits", BaseInterval, CapWaits,
			budgetRows(CapWaits, func(i int) Row {
				return Row{
					"wait_type":           fmt.Sprintf("PAGEIOLATCH_%d", i),
					"wait_time_ms":        int64(i * 11),
					"waiting_tasks_count": int64(i % 4),
				}
			}), budgetDecode[Waits](CapWaits)},
		{"bb:schedulers", "schedulers", BaseInterval, CapSchedulers,
			budgetRows(CapSchedulers, func(i int) Row {
				return Row{"current_tasks": int64(i), "runnable_tasks": int64(i % 2)}
			}), budgetDecode[Schedulers](CapSchedulers)},
		{"bb:semaphores", "semaphores", BaseInterval, CapSemaphores,
			budgetRows(CapSemaphores, func(i int) Row {
				return Row{
					"resource_semaphore_id": int64(i),
					"grantee_count":         int64(i % 3),
					"waiter_count":          int64(i % 2),
					"forced_grant_count":    int64(0),
					"timeout_count":         int64(0),
				}
			}), budgetDecode[Semaphores](CapSemaphores)},
		{"bb:tempdb", "tempdb", BaseInterval, CapTempdb,
			budgetRows(CapTempdb, func(i int) Row {
				return Row{"internal_pages": int64(i), "user_pages": int64(i % 4)}
			}), budgetDecode[Tempdb](CapTempdb)},
		{"bb:filestats", "filestats", DemotedInterval, CapFileStats,
			budgetRows(CapFileStats, func(i int) Row {
				return Row{
					"database_id":        int64(i/8 + 1),
					"file_id":            int64(i%8 + 1),
					"io_stall_read_ms":   int64(i),
					"io_stall_write_ms":  int64(i * 2),
					"num_of_reads":       int64(i * 3),
					"num_of_writes":      int64(i * 4),
					"size_on_disk_bytes": int64(i * 8192),
				}
			}), budgetDecode[FileStats](CapFileStats)},
		{"bb:ag", "ag", DemotedInterval, CapAG,
			budgetRows(CapAG, func(i int) Row {
				return Row{
					"database_id":           int64(i + 1),
					"synchronization_state": int64(i % 7),
				}
			}), budgetDecode[AG](CapAG)},
	}
}

func budgetSamplers() []Sampler {
	list := newBudgetSamplers()
	out := make([]Sampler, len(list))
	for i, s := range list {
		out[i] = s
	}
	return out
}

func budgetReplies(tb testing.TB) map[string][]Row {
	tb.Helper()
	replies := make(map[string][]Row)
	for _, s := range newBudgetSamplers() {
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

func newBudgetRunner(tb testing.TB, q Queryer, closeFn func() error) *Runner {
	tb.Helper()
	dir := filepath.Join(tb.TempDir(), "sql")
	if err := os.MkdirAll(dir, 0700); err != nil {
		tb.Fatalf("mkdir sample dir: %v", err)
	}
	r := NewRunner(dir, ConnConfig{})
	// Inject the documented internal sampler list rather than the global
	// registry, so peer test files cannot perturb this measurement.
	r.samplers = budgetSamplers()
	r.lastRun = make(map[string]time.Time)
	r.Connect = func(ConnConfig) (Queryer, func() error, error) {
		if closeFn == nil {
			closeFn = func() error { return nil }
		}
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

func TestSQLBudget(t *testing.T) {
	replies := budgetReplies(t)
	closed := 0
	r := newBudgetRunner(t, &budgetQuery{replies: replies}, func() error {
		closed++
		return nil
	})
	now := time.Date(2026, 1, 2, 3, 0, 0, 0, time.UTC)
	r.Now = func() time.Time { return now }

	var elapsed time.Duration
	for i := 0; i < budgetTicks; i++ {
		start := time.Now()
		if err := r.Tick(now); err != nil {
			t.Fatalf("tick %d: %v", i, err)
		}
		elapsed += time.Since(start)
		now = now.Add(BaseInterval)
	}
	meanTick := elapsed / budgetTicks
	t.Logf("tick wall time: mean=%v over %d ticks (cap %v)", meanTick, budgetTicks, budgetMeanTickCap)
	if meanTick >= budgetMeanTickCap {
		t.Errorf("mean tick = %v, want < %v", meanTick, budgetMeanTickCap)
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

	lines := budgetReadLines(t, r.Dir)
	perKind := map[string][]int{}
	perKindRows := map[string][]int{}
	perTick := map[time.Time]int{}
	totalBytes, maxLine := 0, 0
	for _, ln := range lines {
		totalBytes += ln.size
		if ln.size > maxLine {
			maxLine = ln.size
		}
		perKind[ln.kind] = append(perKind[ln.kind], ln.size)
		perKindRows[ln.kind] = append(perKindRows[ln.kind], ln.rows)
		perTick[ln.tick] += ln.size
	}
	worstTick := 0
	for _, b := range perTick {
		if b > worstTick {
			worstTick = b
		}
	}
	meanLine := totalBytes / len(lines)
	meanTickBytes := totalBytes / budgetTicks

	for _, s := range newBudgetSamplers() {
		maxSize, meanSize := budgetMaxMean(perKind[s.kind])
		t.Logf("kind=%-10s lines=%3d max=%5d mean=%5d bytes cap=%d", s.kind, len(perKind[s.kind]), maxSize, meanSize, s.limit)
	}
	t.Logf("samples: lines=%d meanLine=%d maxLine=%d meanTickBytes=%d worstTickBytes=%d", len(lines), meanLine, maxLine, meanTickBytes, worstTick)

	if meanLine >= budgetMeanLineCap {
		t.Errorf("mean sample line = %d bytes, want < %d (C13 owner review needed)", meanLine, budgetMeanLineCap)
	}
	if meanTickBytes >= budgetMaxTickBytes {
		t.Errorf("hourly ndjson growth = %d B/tick, want < %d", meanTickBytes, budgetMaxTickBytes)
	}

	for _, s := range newBudgetSamplers() {
		if len(s.rows) > s.limit {
			t.Errorf("fixture %s has %d rows, over cap %d", s.kind, len(s.rows), s.limit)
		}
		if len(perKind[s.kind]) == 0 {
			t.Errorf("no %s samples persisted", s.kind)
			continue
		}
		for _, n := range perKindRows[s.kind] {
			if n > s.limit {
				t.Errorf("persisted %s sample has %d rows, over cap %d (row cap raised?)", s.kind, n, s.limit)
			}
		}
		want := budgetTicks
		if s.interval > BaseInterval {
			want = budgetTicks / 4
		}
		if got := len(perKind[s.kind]); got != want {
			t.Errorf("%s persisted %d lines, want %d (due scheduling)", s.kind, got, want)
		}
	}
}

func TestSQLBudgetNoGoroutineGrowth(t *testing.T) {
	closed := 0
	r := newBudgetRunner(t, &budgetQuery{replies: budgetReplies(t)}, func() error {
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

// BenchmarkSQLTick measures one tick with every sampler due.
func BenchmarkSQLTick(b *testing.B) {
	r := newBudgetRunner(b, &budgetQuery{replies: budgetReplies(b)}, nil)
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

// BenchmarkSQLTick60s measures the steady-state tick where only the 60s
// samplers are due.
func BenchmarkSQLTick60s(b *testing.B) {
	r := newBudgetRunner(b, &budgetQuery{replies: budgetReplies(b)}, nil)
	now := time.Date(2026, 1, 2, 6, 0, 0, 0, time.UTC)
	r.Now = func() time.Time { return now }
	samplers := newBudgetSamplers()
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
```

- `columnKeys` was not part of the stated package surface, so fixtures use the exact documented column names literally; verify `decodeRows` accepts them.
- The test injects `r.samplers` and `r.lastRun` directly (both documented internal fields) to isolate from the global registry used by peer tests.
- Assumes the fixed output type names `Requests`, `Waits`, `Schedulers`, `Semaphores`, `Tempdb`, `FileStats`, `AG`.
- `requests`/`filestats`/`ag` are treated as 60 s samplers; the other four as 15 s samplers.
- AG `synchronization_state` is sent as `int64` because the DMV column is tinyint.
- Allocation budget is measured through `testing.Benchmark(BenchmarkSQLTick).AllocedBytesPerOp()`.
- Success ticks are expected to keep the connection open, so the fake closer must remain at zero; a per-tick close implementation would fail this test.
- `CheckCeiling` is assumed to tolerate empty row sets for the ceiling statements.
- The mean-line 2 KB gate can be tight if the fixed structs serialize 256 rows verbosely; per-kind byte sizes are logged and the real enforced tick budget is `budgetMaxTickBytes = 24 << 10`.
- The hourly file-reuse assertion assumes all 20 ticks stay within one UTC hour.
