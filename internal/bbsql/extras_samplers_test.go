package bbsql

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// extras_samplers_test.go covers the five P1b samplers added after the seam:
// the gauges bb:grants, bb:workers and bb:pendingio (one summed row, no state)
// and the delta samplers bb:latches and bb:rg. Rows are keyed by the SQL column
// names the statements SELECT (grants_total, latch_class, pool_id, ...), never
// by the camelCase JSON keys: a fixture using JSON keys is an unknown column.
// Nothing here connects to anything; every row is a fake.

// ---------------------------------------------------------------- helpers

var xsT0 = time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)

// xsDelta is a delta sampler as the runner sees it: observed, then decoded.
type xsDelta interface {
	Sampler
	ObservingSampler
}

// xsStep is one runner cycle: Observe with the given epoch, then Decode.
func xsStep(s xsDelta, epoch uint64, rows []Row) (any, bool, error) {
	s.Observe(Observation{Time: xsT0.Add(time.Duration(epoch) * time.Second), Epoch: epoch}) //nolint:gosec // test epochs are tiny
	return s.Decode(rows)
}

// xsJSONKeys marshals v and returns its object keys, sorted.
func xsJSONKeys(t *testing.T, v any) []string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal %T: %v", v, err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("payload %s is not a JSON object: %v", data, err)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func xsSorted(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

func xsKeysEqual(t *testing.T, got, want []string) {
	t.Helper()
	if !reflect.DeepEqual(got, xsSorted(want)) {
		t.Errorf("JSON keys = %v, want exactly %v", got, xsSorted(want))
	}
}

// xsGaugeRow builds one gauge row from column names and int64 values.
func xsGaugeRow(cols []string, vals []int64) Row {
	r := Row{}
	for i, c := range cols {
		r[c] = vals[i]
	}
	return r
}

func xsSeq(n int, f func(i int) int64) []int64 {
	out := make([]int64, n)
	for i := range out {
		out[i] = f(i)
	}
	return out
}

// xsRejects fails if err is nil or leaks any of the forbidden substrings.
func xsNoLeak(t *testing.T, what string, err error, forbidden ...string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: err = nil, want an error", what)
	}
	for _, f := range forbidden {
		if f != "" && strings.Contains(err.Error(), f) {
			t.Errorf("%s: error %q leaks %q", what, err.Error(), f)
		}
	}
}

// ---------------------------------------------------------------- gauges

type xsGaugeCase struct {
	id     string
	kind   string
	newS   func() Sampler
	cols   []string
	want   any
	keys   []string
	text   string
	rowCap int
}

func xsGaugeCases() []xsGaugeCase {
	return []xsGaugeCase{
		{"bb:grants", "grants", func() Sampler { return &grantsSampler{} },
			[]string{"grants_total", "grants_waiting", "grants_requested_kb", "grants_granted_kb", "grants_used_kb", "grants_max_wait_ms"},
			Grants{Total: 11, Waiting: 22, RequestedKb: 33, GrantedKb: 44, UsedKb: 55, MaxWaitMs: 66},
			[]string{"grantsTotal", "grantsWaiting", "grantsRequestedKb", "grantsGrantedKb", "grantsUsedKb", "grantsMaxWaitMs"},
			stmtGrants, CapGrants},
		{"bb:workers", "workers", func() Sampler { return &workersSampler{} },
			[]string{"workers_total", "workers_running", "workers_runnable", "workers_suspended", "workers_sick", "workers_preemptive"},
			Workers{Total: 11, Running: 22, Runnable: 33, Suspended: 44, Sick: 55, Preemptive: 66},
			[]string{"workersTotal", "workersRunning", "workersRunnable", "workersSuspended", "workersSick", "workersPreemptive"},
			stmtWorkers, CapWorkers},
		{"bb:pendingio", "pendingio", func() Sampler { return &pendingIOSampler{} },
			[]string{"pio_total", "pio_disk", "pio_in_os", "pio_in_sql"},
			PendingIO{Total: 11, Disk: 22, InOS: 33, InSQL: 44},
			[]string{"pioTotal", "pioDisk", "pioInOs", "pioInSql"},
			stmtPendingIO, CapPendingIO},
	}
}

// xsGoodRow carries 11, 22, 33, ... in column order.
func (c xsGaugeCase) goodRow() Row {
	return xsGaugeRow(c.cols, xsSeq(len(c.cols), func(i int) int64 { return int64(i+1) * 11 }))
}

func TestXSGaugeRegistered(t *testing.T) {
	for _, c := range xsGaugeCases() {
		t.Run(c.kind, func(t *testing.T) {
			if c.newS().ID() != c.id || c.newS().Kind() != c.kind {
				t.Fatalf("ID/Kind = %q/%q, want %q/%q", c.newS().ID(), c.newS().Kind(), c.id, c.kind)
			}
			found := false
			for _, k := range ExtrasForProfile(ProfileEverything) {
				if k == c.kind {
					found = true
				}
			}
			if !found {
				t.Errorf("ExtrasForProfile(everything) = %v, missing %q", ExtrasForProfile(ProfileEverything), c.kind)
			}
			for _, k := range ExtrasForProfile(ProfileStandard) {
				if k == c.kind {
					t.Errorf("standard profile includes %q", c.kind)
				}
			}
			for _, s := range Samplers() {
				if s.Kind() == c.kind || s.ID() == c.id {
					t.Errorf("%q is in the Standard Samplers() list", c.kind)
				}
			}
			built := Extras([]string{c.kind})
			if len(built) != 1 {
				t.Fatalf("Extras([%q]) built %d samplers, want 1", c.kind, len(built))
			}
			s := built[0]
			if s.ID() != c.id {
				t.Errorf("ID = %q, want %q", s.ID(), c.id)
			}
			if !strings.HasSuffix(c.text, "/* unravis:"+s.ID()+" */") {
				t.Errorf("statement comment does not carry the sampler ID %q", s.ID())
			}
			if s.Interval() != BaseInterval {
				t.Errorf("Interval = %v, want %v", s.Interval(), BaseInterval)
			}
			if got := statementCap(c.text); got != c.rowCap {
				t.Errorf("statementCap = %d, want %d", got, c.rowCap)
			}
			if _, ok := s.(ObservingSampler); ok {
				t.Errorf("%q observes; a gauge keeps no state", c.kind)
			}
		})
	}
}

func TestXSGaugeDecodesOneRowToOneStruct(t *testing.T) {
	for _, c := range xsGaugeCases() {
		t.Run(c.kind, func(t *testing.T) {
			payload, truncated, err := c.newS().Decode([]Row{c.goodRow()})
			if err != nil || truncated {
				t.Fatalf("truncated=%v err=%v, want false,nil", truncated, err)
			}
			if !reflect.DeepEqual(payload, c.want) {
				t.Fatalf("payload = %#v, want %#v (one struct, not a slice)", payload, c.want)
			}
			xsKeysEqual(t, xsJSONKeys(t, payload), c.keys)
		})
	}
}

func TestXSGaugeZeroRowIsAReadingNotNil(t *testing.T) {
	for _, c := range xsGaugeCases() {
		t.Run(c.kind, func(t *testing.T) {
			row := xsGaugeRow(c.cols, make([]int64, len(c.cols)))
			payload, truncated, err := c.newS().Decode([]Row{row})
			if err != nil || truncated {
				t.Fatalf("truncated=%v err=%v, want false,nil", truncated, err)
			}
			if payload == nil {
				t.Fatal("all-zero row decoded to nil; zero is a real reading")
			}
			zero := reflect.Zero(reflect.TypeOf(c.want)).Interface()
			if !reflect.DeepEqual(payload, zero) {
				t.Errorf("payload = %#v, want zero %#v", payload, zero)
			}
			xsKeysEqual(t, xsJSONKeys(t, payload), c.keys)
		})
	}
}

func TestXSGaugeEmptyReplyIsNilNoError(t *testing.T) {
	for _, c := range xsGaugeCases() {
		for name, rows := range map[string][]Row{"empty": {}, "nil": nil} {
			t.Run(c.kind+"/"+name, func(t *testing.T) {
				payload, truncated, err := c.newS().Decode(rows)
				if payload != nil || truncated || err != nil {
					t.Fatalf("payload=%#v truncated=%v err=%v, want nil,false,nil", payload, truncated, err)
				}
			})
		}
	}
}

func TestXSGaugeBadRowsAreFixedCodeErrors(t *testing.T) {
	const secret = 424242
	for _, c := range xsGaugeCases() {
		leak := func(t *testing.T, what string, err error, extra ...string) {
			t.Helper()
			forbidden := append([]string{"424242"}, c.cols...)
			forbidden = append(forbidden, c.keys...)
			xsNoLeak(t, what, err, append(forbidden, extra...)...)
		}
		secretVals := func() []int64 {
			return xsSeq(len(c.cols), func(int) int64 { return secret })
		}
		t.Run(c.kind+"/missing column", func(t *testing.T) {
			for _, col := range c.cols {
				row := xsGaugeRow(c.cols, secretVals())
				delete(row, col)
				payload, _, err := c.newS().Decode([]Row{row})
				if payload != nil {
					t.Errorf("missing %s: payload = %#v, want nil", col, payload)
				}
				leak(t, "missing "+col, err)
			}
		})
		t.Run(c.kind+"/null column", func(t *testing.T) {
			for _, col := range c.cols {
				row := xsGaugeRow(c.cols, secretVals())
				row[col] = nil
				payload, _, err := c.newS().Decode([]Row{row})
				if payload != nil {
					t.Errorf("null %s: payload = %#v, want nil", col, payload)
				}
				leak(t, "null "+col, err)
			}
		})
		t.Run(c.kind+"/unknown column", func(t *testing.T) {
			row := xsGaugeRow(c.cols, secretVals())
			row["host_name"] = "SECRETHOST"
			payload, _, err := c.newS().Decode([]Row{row})
			if payload != nil {
				t.Errorf("payload = %#v, want nil", payload)
			}
			leak(t, "unknown column", err, "host_name", "SECRETHOST")
		})
		t.Run(c.kind+"/JSON keys as column names", func(t *testing.T) {
			row := Row{}
			for _, k := range c.keys {
				row[k] = int64(secret)
			}
			payload, _, err := c.newS().Decode([]Row{row})
			if payload != nil {
				t.Errorf("payload = %#v, want nil: camelCase keys are output names", payload)
			}
			leak(t, "JSON-key fixture", err)
		})
	}
}

// TestXSGaugeMoreThanOneRowKeepsFirstAndTruncates pins decodeRows with
// CapSingleRow: only the first row is decoded and kept, truncated is true, and
// a malformed SECOND row is never inspected.
func TestXSGaugeMoreThanOneRowKeepsFirstAndTruncates(t *testing.T) {
	for _, c := range xsGaugeCases() {
		t.Run(c.kind, func(t *testing.T) {
			second := xsGaugeRow(c.cols, xsSeq(len(c.cols), func(int) int64 { return 999 }))
			payload, truncated, err := c.newS().Decode([]Row{c.goodRow(), second, second})
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if !truncated {
				t.Error("truncated = false, want true for more than one row")
			}
			if !reflect.DeepEqual(payload, c.want) {
				t.Errorf("payload = %#v, want the FIRST row %#v", payload, c.want)
			}
			payload, truncated, err = c.newS().Decode([]Row{c.goodRow(), {"not_a_column": int64(1)}})
			if err != nil || !truncated || !reflect.DeepEqual(payload, c.want) {
				t.Errorf("a bad second row was inspected: payload=%#v truncated=%v err=%v", payload, truncated, err)
			}
		})
	}
}

// ---------------------------------------------------------------- latches

type xsLatch struct {
	class string
	waits uint64
	ms    uint64
}

func xsLatchRows(entries ...xsLatch) []Row {
	rows := make([]Row, 0, len(entries))
	for _, e := range entries {
		rows = append(rows, Row{
			"latch_class":            e.class,
			"waiting_requests_count": int64(e.waits), //nolint:gosec // test values are small
			"latch_wait_ms":          int64(e.ms),    //nolint:gosec // test values are small
		})
	}
	return rows
}

func xsLatchOut(t *testing.T, payload any) []Latches {
	t.Helper()
	if payload == nil {
		return nil
	}
	got, ok := payload.([]Latches)
	if !ok {
		t.Fatalf("latches payload = %T %#v, want []Latches", payload, payload)
	}
	return got
}

type xsLatchCycle struct {
	epoch uint64
	rows  []Row
	want  []Latches
}

func TestXSLatchesRegistered(t *testing.T) {
	found := false
	for _, k := range ExtrasForProfile(ProfileEverything) {
		found = found || k == "latches"
	}
	if !found {
		t.Error("latches missing from the everything profile")
	}
	for _, s := range Samplers() {
		if s.Kind() == "latches" {
			t.Error("latches is in the Standard Samplers() list")
		}
	}
	built := Extras([]string{"latches"})
	if len(built) != 1 {
		t.Fatalf("Extras built %d, want 1", len(built))
	}
	if built[0].ID() != "bb:latches" || built[0].Interval() != DemotedInterval {
		t.Errorf("ID/Interval = %q/%v, want bb:latches/%v", built[0].ID(), built[0].Interval(), DemotedInterval)
	}
	if _, ok := built[0].(ObservingSampler); !ok {
		t.Error("latches must implement ObservingSampler")
	}
}

func TestXSLatchesDeltaScenarios(t *testing.T) {
	a := func(w, ms uint64) xsLatch { return xsLatch{"ACCESS_METHODS_DATASET_PARENT", w, ms} }
	b := func(w, ms uint64) xsLatch { return xsLatch{"BUFFER", w, ms} }
	tests := []struct {
		name   string
		cycles []xsLatchCycle
	}{
		{"first cycle is baseline only, second emits per-class deltas", []xsLatchCycle{
			{1, xsLatchRows(a(10, 100), b(5, 50)), nil},
			{1, xsLatchRows(a(15, 160), b(5, 70)), []Latches{
				{"ACCESS_METHODS_DATASET_PARENT", 5, 60}, {"BUFFER", 0, 20}}},
		}},
		{"a class seen for the first time emits nothing that cycle", []xsLatchCycle{
			{1, xsLatchRows(a(10, 100)), nil},
			{1, xsLatchRows(a(12, 130), b(900, 9000)), []Latches{{"ACCESS_METHODS_DATASET_PARENT", 2, 30}}},
			{1, xsLatchRows(a(13, 140), b(910, 9100)), []Latches{
				{"ACCESS_METHODS_DATASET_PARENT", 1, 10}, {"BUFFER", 10, 100}}},
		}},
		{"waits going backwards baselines without emitting", []xsLatchCycle{
			{1, xsLatchRows(a(100, 1000), b(10, 10)), nil},
			{1, xsLatchRows(a(50, 1100), b(20, 30)), []Latches{{"BUFFER", 10, 20}}},
			{1, xsLatchRows(a(60, 1200), b(20, 30)), []Latches{{"ACCESS_METHODS_DATASET_PARENT", 10, 100}}},
		}},
		{"wait ms going backwards baselines without emitting", []xsLatchCycle{
			{1, xsLatchRows(a(100, 1000)), nil},
			{1, xsLatchRows(a(110, 900)), nil},
			{1, xsLatchRows(a(120, 950)), []Latches{{"ACCESS_METHODS_DATASET_PARENT", 10, 50}}},
		}},
		{"an idle class (both deltas zero) is dropped", []xsLatchCycle{
			{1, xsLatchRows(a(10, 100), b(5, 50)), nil},
			{1, xsLatchRows(a(10, 100), b(6, 50)), []Latches{{"BUFFER", 1, 0}}},
			{1, xsLatchRows(a(10, 100), b(6, 50)), nil},
		}},
		{"an empty reply clears baselines", []xsLatchCycle{
			{1, xsLatchRows(a(10, 100)), nil},
			{1, xsLatchRows(a(20, 200)), []Latches{{"ACCESS_METHODS_DATASET_PARENT", 10, 100}}},
			{1, nil, nil},
			{1, xsLatchRows(a(500, 5000)), nil},
			{1, xsLatchRows(a(510, 5100)), []Latches{{"ACCESS_METHODS_DATASET_PARENT", 10, 100}}},
		}},
		{"a zero-length (non-nil) empty reply clears baselines too", []xsLatchCycle{
			{1, xsLatchRows(a(10, 100)), nil},
			{1, []Row{}, nil},
			{1, xsLatchRows(a(20, 200)), nil},
			{1, xsLatchRows(a(21, 210)), []Latches{{"ACCESS_METHODS_DATASET_PARENT", 1, 10}}},
		}},
		{"an epoch change rebaselines, no delta across it", []xsLatchCycle{
			{1, xsLatchRows(a(10, 100)), nil},
			{2, xsLatchRows(a(99, 999)), nil},
			{2, xsLatchRows(a(100, 1009)), []Latches{{"ACCESS_METHODS_DATASET_PARENT", 1, 10}}},
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := &latchesSampler{}
			for i, c := range tc.cycles {
				payload, truncated, err := xsStep(s, c.epoch, c.rows)
				if err != nil || truncated {
					t.Fatalf("cycle %d: truncated=%v err=%v", i, truncated, err)
				}
				got := xsLatchOut(t, payload)
				if len(got) != len(c.want) || (len(got) > 0 && !reflect.DeepEqual(got, c.want)) {
					t.Fatalf("cycle %d: got %#v, want %#v", i, got, c.want)
				}
				if len(c.want) == 0 && payload != nil {
					t.Fatalf("cycle %d: payload = %#v, want untyped nil when nothing is emitted", i, payload)
				}
			}
		})
	}
}

func xsLatchNamed(prefix string, n int, waits, ms uint64) []Row {
	entries := make([]xsLatch, 0, n)
	for i := 0; i < n; i++ {
		entries = append(entries, xsLatch{fmt.Sprintf("%s_%02d", prefix, i), waits, ms})
	}
	return xsLatchRows(entries...)
}

func TestXSLatchesCapKeepsCapAndTruncates(t *testing.T) {
	s := &latchesSampler{}
	payload, truncated, err := xsStep(s, 1, xsLatchNamed("L", CapLatches+1, 1, 1))
	if err != nil || payload != nil || !truncated {
		t.Fatalf("first cycle: payload=%v truncated=%v err=%v, want nil,true,nil", payload, truncated, err)
	}
	if len(s.prev) != CapLatches {
		t.Fatalf("len(prev) = %d, want %d", len(s.prev), CapLatches)
	}
	if _, ok := s.prev[fmt.Sprintf("L_%02d", CapLatches)]; ok {
		t.Error("the row beyond the cap was baselined")
	}
	payload, truncated, err = xsStep(s, 1, xsLatchNamed("L", CapLatches+1, 3, 7))
	if err != nil || !truncated {
		t.Fatalf("second cycle: truncated=%v err=%v, want true,nil", truncated, err)
	}
	if got := xsLatchOut(t, payload); len(got) != CapLatches {
		t.Fatalf("emitted %d rows, want %d", len(got), CapLatches)
	}
}

func TestXSLatchesBaselineStaysBoundedUnderChurn(t *testing.T) {
	s := &latchesSampler{}
	for cycle := 0; cycle < 50; cycle++ {
		// Every cycle carries more rows than the cap, all with names no earlier
		// cycle used.
		if _, _, err := xsStep(s, 1, xsLatchNamed(fmt.Sprintf("CHURN%d", cycle), CapLatches+5, uint64(cycle), uint64(cycle))); err != nil {
			t.Fatalf("cycle %d: %v", cycle, err)
		}
		if len(s.prev) > CapLatches {
			t.Fatalf("cycle %d: baseline holds %d entries, cap is %d", cycle, len(s.prev), CapLatches)
		}
	}
}

func TestXSLatchesInvalidClassIsRejectedWithoutLeaking(t *testing.T) {
	bad := []string{"BAD NAME", "A;DROP", "", strings.Repeat("A", 61), "caf\u00e9", "A-B", "A\nB"}
	for _, name := range bad {
		t.Run(fmt.Sprintf("%q", name), func(t *testing.T) {
			s := &latchesSampler{}
			if _, _, err := xsStep(s, 1, xsLatchRows(xsLatch{"GOOD", 1, 1})); err != nil {
				t.Fatalf("baseline: %v", err)
			}
			rows := xsLatchRows(xsLatch{"GOOD", 2, 2}, xsLatch{name, 424242, 424242})
			payload, truncated, err := xsStep(s, 1, rows)
			if payload != nil || truncated {
				t.Errorf("payload=%#v truncated=%v, want nil,false", payload, truncated)
			}
			xsNoLeak(t, "invalid class", err, name, "424242", "latch_class", "latchClass", "GOOD")
			// An error is not a sample: the baseline survives it untouched.
			payload, _, err = xsStep(s, 1, xsLatchRows(xsLatch{"GOOD", 5, 6}))
			if err != nil {
				t.Fatalf("after the error: %v", err)
			}
			if got := xsLatchOut(t, payload); !reflect.DeepEqual(got, []Latches{{"GOOD", 4, 5}}) {
				t.Errorf("after the error: got %#v, want the delta against the old baseline", got)
			}
		})
	}
}

func TestXSLatchesAcceptsMixedCaseAndBoundaryNames(t *testing.T) {
	for _, name := range []string{"SSBXmitWork", "a", "A_1", strings.Repeat("Z", 60)} {
		s := &latchesSampler{}
		if _, _, err := xsStep(s, 1, xsLatchRows(xsLatch{name, 1, 1})); err != nil {
			t.Fatalf("%q rejected: %v", name, err)
		}
		payload, _, err := xsStep(s, 1, xsLatchRows(xsLatch{name, 2, 3}))
		if err != nil {
			t.Fatalf("%q rejected on cycle 2: %v", name, err)
		}
		if got := xsLatchOut(t, payload); !reflect.DeepEqual(got, []Latches{{name, 1, 2}}) {
			t.Errorf("%q: got %#v", name, got)
		}
	}
}

func TestXSLatchesJSONKeysAndInputContract(t *testing.T) {
	s := &latchesSampler{}
	_, _, _ = xsStep(s, 1, xsLatchRows(xsLatch{"BUFFER", 1, 1}))
	payload, _, err := xsStep(s, 1, xsLatchRows(xsLatch{"BUFFER", 3, 5}))
	if err != nil {
		t.Fatal(err)
	}
	got := xsLatchOut(t, payload)
	if len(got) != 1 {
		t.Fatalf("got %#v, want one row", got)
	}
	xsKeysEqual(t, xsJSONKeys(t, got[0]), []string{"latchClass", "latchWaits", "latchWaitMs"})

	// Bad inputs: missing, null, unknown, and JSON-key columns are all errors
	// with a fixed code that names neither the column nor a value.
	good := func() Row { return xsLatchRows(xsLatch{"BUFFER", 424242, 424242})[0] }
	for _, col := range []string{"latch_class", "waiting_requests_count", "latch_wait_ms"} {
		missing, null := good(), good()
		delete(missing, col)
		null[col] = nil
		for what, row := range map[string]Row{"missing": missing, "null": null} {
			_, _, err := xsStep(&latchesSampler{}, 1, []Row{row})
			xsNoLeak(t, what+" "+col, err, col, "424242")
		}
	}
	unknown := good()
	unknown["latchClass"] = "BUFFER"
	_, _, err = xsStep(&latchesSampler{}, 1, []Row{unknown})
	xsNoLeak(t, "JSON-key column", err, "latchClass", "424242")
}

// ---------------------------------------------------------------- rg

// xsRGRow is one sys.dm_resource_governor_resource_pools row, SQL column names.
func xsRGRow(id, cpu, timeouts, oom, used, target, waiters, active int64) Row {
	return Row{
		"pool_id":                      id,
		"total_cpu_usage_ms":           cpu,
		"total_memgrant_timeout_count": timeouts,
		"out_of_memory_count":          oom,
		"used_memory_kb":               used,
		"target_memory_kb":             target,
		"memgrant_waiter_count":        waiters,
		"active_memgrant_count":        active,
	}
}

// xsP builds the expected output row; a negative delta means nil (absent).
func xsP(id, cpu, timeouts, oom int64, used, target uint64, waiters, active uint32) ResourcePool {
	p := ResourcePool{PoolID: uint32(id), UsedKb: used, TargetKb: target, Waiters: waiters, Active: active} //nolint:gosec // test ids are small
	ptr := func(v int64) *uint64 {
		if v < 0 {
			return nil
		}
		u := uint64(v)
		return &u
	}
	p.CPUMs, p.GrantTimeouts, p.OOMCount = ptr(cpu), ptr(timeouts), ptr(oom)
	return p
}

func xsPtrEq(a, b *uint64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func xsPoolsEqual(got, want []ResourcePool) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		g, w := got[i], want[i]
		if g.PoolID != w.PoolID || g.UsedKb != w.UsedKb || g.TargetKb != w.TargetKb ||
			g.Waiters != w.Waiters || g.Active != w.Active ||
			!xsPtrEq(g.CPUMs, w.CPUMs) || !xsPtrEq(g.GrantTimeouts, w.GrantTimeouts) || !xsPtrEq(g.OOMCount, w.OOMCount) {
			return false
		}
	}
	return true
}

func xsPoolStr(ps []ResourcePool) string {
	var sb strings.Builder
	for _, p := range ps {
		d := func(v *uint64) string {
			if v == nil {
				return "nil"
			}
			return fmt.Sprint(*v)
		}
		fmt.Fprintf(&sb, "{id=%d cpu=%s to=%s oom=%s used=%d target=%d w=%d a=%d} ",
			p.PoolID, d(p.CPUMs), d(p.GrantTimeouts), d(p.OOMCount), p.UsedKb, p.TargetKb, p.Waiters, p.Active)
	}
	return sb.String()
}

func xsRGOut(t *testing.T, payload any) []ResourcePool {
	t.Helper()
	if payload == nil {
		return nil
	}
	got, ok := payload.([]ResourcePool)
	if !ok {
		t.Fatalf("rg payload = %T %#v, want []ResourcePool", payload, payload)
	}
	return got
}

type xsRGCycle struct {
	epoch uint64
	rows  []Row
	want  []ResourcePool
}

func TestXSRGRegistered(t *testing.T) {
	found := false
	for _, k := range ExtrasForProfile(ProfileEverything) {
		found = found || k == "rg"
	}
	if !found {
		t.Error("rg missing from the everything profile")
	}
	for _, s := range Samplers() {
		if s.Kind() == "rg" {
			t.Error("rg is in the Standard Samplers() list")
		}
	}
	built := Extras([]string{"rg"})
	if len(built) != 1 {
		t.Fatalf("Extras built %d, want 1", len(built))
	}
	if built[0].ID() != "bb:rg" || built[0].Interval() != DemotedInterval {
		t.Errorf("ID/Interval = %q/%v, want bb:rg/%v", built[0].ID(), built[0].Interval(), DemotedInterval)
	}
	if _, ok := built[0].(ObservingSampler); !ok {
		t.Error("rg must implement ObservingSampler")
	}
}

func TestXSRGDeltaScenarios(t *testing.T) {
	tests := []struct {
		name   string
		cycles []xsRGCycle
	}{
		{"first cycle: gauges for every pool, no deltas; second cycle: correct deltas", []xsRGCycle{
			{1, []Row{xsRGRow(1, 100, 1, 0, 500, 1000, 2, 3), xsRGRow(256, 50, 0, 0, 70, 80, 0, 1)}, []ResourcePool{
				xsP(1, -1, -1, -1, 500, 1000, 2, 3), xsP(256, -1, -1, -1, 70, 80, 0, 1)}},
			{1, []Row{xsRGRow(1, 160, 4, 2, 600, 1000, 0, 5), xsRGRow(256, 50, 3, 0, 75, 80, 1, 0)}, []ResourcePool{
				xsP(1, 60, 3, 2, 600, 1000, 0, 5), xsP(256, 0, 3, 0, 75, 80, 1, 0)}},
		}},
		{"an unchanged counter yields pointers to zero", []xsRGCycle{
			{1, []Row{xsRGRow(2, 10, 10, 10, 1, 2, 3, 4)}, []ResourcePool{xsP(2, -1, -1, -1, 1, 2, 3, 4)}},
			{1, []Row{xsRGRow(2, 10, 10, 10, 1, 2, 3, 4)}, []ResourcePool{xsP(2, 0, 0, 0, 1, 2, 3, 4)}},
		}},
		{"a pool absent last cycle has nil deltas this cycle", []xsRGCycle{
			{1, []Row{xsRGRow(1, 100, 0, 0, 1, 1, 0, 0)}, []ResourcePool{xsP(1, -1, -1, -1, 1, 1, 0, 0)}},
			{1, []Row{xsRGRow(1, 110, 0, 0, 1, 1, 0, 0), xsRGRow(256, 999, 9, 9, 2, 2, 0, 0)}, []ResourcePool{
				xsP(1, 10, 0, 0, 1, 1, 0, 0), xsP(256, -1, -1, -1, 2, 2, 0, 0)}},
			{1, []Row{xsRGRow(1, 120, 0, 0, 1, 1, 0, 0), xsRGRow(256, 1000, 10, 9, 2, 2, 0, 0)}, []ResourcePool{
				xsP(1, 10, 0, 0, 1, 1, 0, 0), xsP(256, 1, 1, 0, 2, 2, 0, 0)}},
		}},
		{"cpu going backwards nils all three deltas, then measures against the reset value", []xsRGCycle{
			{1, []Row{xsRGRow(1, 1000, 5, 5, 1, 1, 0, 0)}, []ResourcePool{xsP(1, -1, -1, -1, 1, 1, 0, 0)}},
			{1, []Row{xsRGRow(1, 10, 8, 8, 9, 9, 1, 1)}, []ResourcePool{xsP(1, -1, -1, -1, 9, 9, 1, 1)}},
			{1, []Row{xsRGRow(1, 40, 9, 11, 9, 9, 1, 1)}, []ResourcePool{xsP(1, 30, 1, 3, 9, 9, 1, 1)}},
		}},
		{"timeouts going backwards nils all three deltas, then measures against the reset value", []xsRGCycle{
			{1, []Row{xsRGRow(1, 100, 50, 5, 1, 1, 0, 0)}, []ResourcePool{xsP(1, -1, -1, -1, 1, 1, 0, 0)}},
			{1, []Row{xsRGRow(1, 200, 2, 6, 1, 1, 0, 0)}, []ResourcePool{xsP(1, -1, -1, -1, 1, 1, 0, 0)}},
			{1, []Row{xsRGRow(1, 250, 4, 6, 1, 1, 0, 0)}, []ResourcePool{xsP(1, 50, 2, 0, 1, 1, 0, 0)}},
		}},
		{"oom going backwards nils all three deltas, then measures against the reset value", []xsRGCycle{
			{1, []Row{xsRGRow(1, 100, 5, 50, 1, 1, 0, 0)}, []ResourcePool{xsP(1, -1, -1, -1, 1, 1, 0, 0)}},
			{1, []Row{xsRGRow(1, 200, 6, 0, 1, 1, 0, 0)}, []ResourcePool{xsP(1, -1, -1, -1, 1, 1, 0, 0)}},
			{1, []Row{xsRGRow(1, 205, 6, 1, 1, 1, 0, 0)}, []ResourcePool{xsP(1, 5, 0, 1, 1, 1, 0, 0)}},
		}},
		{"a reset in one pool does not nil a healthy pool", []xsRGCycle{
			{1, []Row{xsRGRow(1, 100, 0, 0, 1, 1, 0, 0), xsRGRow(2, 100, 0, 0, 1, 1, 0, 0)}, []ResourcePool{
				xsP(1, -1, -1, -1, 1, 1, 0, 0), xsP(2, -1, -1, -1, 1, 1, 0, 0)}},
			{1, []Row{xsRGRow(1, 5, 0, 0, 1, 1, 0, 0), xsRGRow(2, 150, 0, 0, 1, 1, 0, 0)}, []ResourcePool{
				xsP(1, -1, -1, -1, 1, 1, 0, 0), xsP(2, 50, 0, 0, 1, 1, 0, 0)}},
		}},
		{"an epoch change gives nil deltas, then deltas resume", []xsRGCycle{
			{1, []Row{xsRGRow(1, 100, 1, 1, 1, 1, 0, 0)}, []ResourcePool{xsP(1, -1, -1, -1, 1, 1, 0, 0)}},
			{2, []Row{xsRGRow(1, 900, 9, 9, 2, 2, 0, 0)}, []ResourcePool{xsP(1, -1, -1, -1, 2, 2, 0, 0)}},
			{2, []Row{xsRGRow(1, 910, 9, 10, 2, 2, 0, 0)}, []ResourcePool{xsP(1, 10, 0, 1, 2, 2, 0, 0)}},
		}},
		{"an empty reply clears baselines and returns nil", []xsRGCycle{
			{1, []Row{xsRGRow(1, 100, 0, 0, 1, 1, 0, 0)}, []ResourcePool{xsP(1, -1, -1, -1, 1, 1, 0, 0)}},
			{1, []Row{xsRGRow(1, 110, 0, 0, 1, 1, 0, 0)}, []ResourcePool{xsP(1, 10, 0, 0, 1, 1, 0, 0)}},
			{1, nil, nil},
			{1, []Row{xsRGRow(1, 500, 0, 0, 1, 1, 0, 0)}, []ResourcePool{xsP(1, -1, -1, -1, 1, 1, 0, 0)}},
			{1, []Row{xsRGRow(1, 520, 0, 0, 1, 1, 0, 0)}, []ResourcePool{xsP(1, 20, 0, 0, 1, 1, 0, 0)}},
		}},
		{"a zero-length (non-nil) empty reply also clears baselines", []xsRGCycle{
			{1, []Row{xsRGRow(1, 100, 0, 0, 1, 1, 0, 0)}, []ResourcePool{xsP(1, -1, -1, -1, 1, 1, 0, 0)}},
			{1, []Row{}, nil},
			{1, []Row{xsRGRow(1, 130, 0, 0, 1, 1, 0, 0)}, []ResourcePool{xsP(1, -1, -1, -1, 1, 1, 0, 0)}},
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := &rgSampler{}
			for i, c := range tc.cycles {
				payload, truncated, err := xsStep(s, c.epoch, c.rows)
				if err != nil || truncated {
					t.Fatalf("cycle %d: truncated=%v err=%v", i, truncated, err)
				}
				if len(c.want) == 0 && payload != nil {
					t.Fatalf("cycle %d: payload = %#v, want untyped nil", i, payload)
				}
				if got := xsRGOut(t, payload); !xsPoolsEqual(got, c.want) {
					t.Fatalf("cycle %d:\n got  %s\n want %s", i, xsPoolStr(got), xsPoolStr(c.want))
				}
			}
		})
	}
}

func TestXSRGJSONKeysOmitAbsentDeltas(t *testing.T) {
	s := &rgSampler{}
	payload, _, err := xsStep(s, 1, []Row{xsRGRow(1, 100, 1, 1, 500, 1000, 2, 3)})
	if err != nil {
		t.Fatal(err)
	}
	first := xsRGOut(t, payload)
	if len(first) != 1 {
		t.Fatalf("first cycle: %d rows, want 1", len(first))
	}
	xsKeysEqual(t, xsJSONKeys(t, first[0]), []string{"poolId", "usedKb", "targetKb", "waiters", "activeGrants"})
	for _, banned := range []string{"cpuMs", "grantTimeouts", "oomCount"} {
		data, _ := json.Marshal(first[0])
		if strings.Contains(string(data), banned) {
			t.Errorf("baseline row %s carries %q", data, banned)
		}
	}

	payload, _, err = xsStep(s, 1, []Row{xsRGRow(1, 100, 1, 1, 500, 1000, 2, 3)})
	if err != nil {
		t.Fatal(err)
	}
	second := xsRGOut(t, payload)
	if len(second) != 1 {
		t.Fatalf("second cycle: %d rows, want 1", len(second))
	}
	xsKeysEqual(t, xsJSONKeys(t, second[0]), []string{
		"poolId", "cpuMs", "grantTimeouts", "oomCount", "usedKb", "targetKb", "waiters", "activeGrants"})
	data, _ := json.Marshal(second[0])
	if !strings.Contains(string(data), `"cpuMs":0`) {
		t.Errorf("an unchanged counter must serialize as a real 0: %s", data)
	}
}

func TestXSRGCapKeepsCapAndTruncates(t *testing.T) {
	rows := func(cpu int64) []Row {
		out := make([]Row, 0, CapRG+1)
		for i := 0; i < CapRG+1; i++ {
			out = append(out, xsRGRow(int64(256+i), cpu, 0, 0, 1, 1, 0, 0))
		}
		return out
	}
	s := &rgSampler{}
	payload, truncated, err := xsStep(s, 1, rows(10))
	if err != nil || !truncated {
		t.Fatalf("first cycle: truncated=%v err=%v, want true,nil", truncated, err)
	}
	if got := xsRGOut(t, payload); len(got) != CapRG {
		t.Fatalf("emitted %d pools, want %d", len(got), CapRG)
	}
	if len(s.prev) != CapRG {
		t.Errorf("len(prev) = %d, want %d", len(s.prev), CapRG)
	}
	if _, ok := s.prev[uint32(256+CapRG)]; ok {
		t.Error("the pool beyond the cap was baselined")
	}
	payload, truncated, err = xsStep(s, 1, rows(25))
	if err != nil || !truncated {
		t.Fatalf("second cycle: truncated=%v err=%v, want true,nil", truncated, err)
	}
	got := xsRGOut(t, payload)
	if len(got) != CapRG || got[0].CPUMs == nil || *got[0].CPUMs != 15 {
		t.Errorf("second cycle: %d pools, want %d with a first-pool cpu delta of 15", len(got), CapRG)
	}
}

func TestXSRGNeverCarriesAPoolName(t *testing.T) {
	// A pool-name column is not in the contract: it is an unknown column and the
	// reply is refused whole, without echoing the name.
	row := xsRGRow(256, 1, 1, 1, 1, 1, 1, 1)
	row["name"] = "SECRET_POOL"
	row["pool_name"] = "SECRET_POOL"
	payload, _, err := xsStep(&rgSampler{}, 1, []Row{row})
	if payload != nil {
		t.Errorf("payload = %#v, want nil", payload)
	}
	xsNoLeak(t, "pool name column", err, "SECRET_POOL", "name", "pool_name")

	// And the output of a good reply has no name-like key at all.
	s := &rgSampler{}
	_, _, _ = xsStep(s, 1, []Row{xsRGRow(256, 1, 1, 1, 1, 1, 1, 1)})
	payload, _, err = xsStep(s, 1, []Row{xsRGRow(256, 2, 2, 2, 1, 1, 1, 1)})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(payload)
	if strings.Contains(strings.ToLower(string(data)), "name") {
		t.Errorf("rg JSON %s contains a name", data)
	}
}

func TestXSRGBadRowsAreFixedCodeErrors(t *testing.T) {
	cols := []string{"pool_id", "total_cpu_usage_ms", "total_memgrant_timeout_count", "out_of_memory_count",
		"used_memory_kb", "target_memory_kb", "memgrant_waiter_count", "active_memgrant_count"}
	keys := []string{"poolId", "rgCpuMsCum", "rgTimeoutsCum", "rgOomCum", "rgUsedKb", "rgTargetKb", "rgWaiters", "rgActive"}
	good := func() Row { return xsRGRow(424242, 424242, 424242, 424242, 424242, 424242, 424242, 424242) }
	forbidden := append(append([]string{"424242"}, cols...), keys...)
	for _, col := range cols {
		missing, null := good(), good()
		delete(missing, col)
		null[col] = nil
		for what, row := range map[string]Row{"missing": missing, "null": null} {
			payload, _, err := xsStep(&rgSampler{}, 1, []Row{row})
			if payload != nil {
				t.Errorf("%s %s: payload = %#v, want nil", what, col, payload)
			}
			xsNoLeak(t, what+" "+col, err, forbidden...)
		}
	}
	jsonKeys := Row{}
	for _, k := range keys {
		jsonKeys[k] = int64(424242)
	}
	_, _, err := xsStep(&rgSampler{}, 1, []Row{jsonKeys})
	xsNoLeak(t, "JSON-key fixture", err, forbidden...)
}

// ---------------------------------------------------------------- statements

func TestXSStatementContract(t *testing.T) {
	banned := []string{"sql_text", "query_plan", "input_buffer", "host_name", "login", "program_name"}
	cases := []struct {
		id  string
		cap int
		top string
	}{
		{"bb:grants", CapSingleRow, ""},
		{"bb:workers", CapSingleRow, ""},
		{"bb:pendingio", CapSingleRow, ""},
		{"bb:latches", 20, "TOP 21"},
		{"bb:rg", 32, "TOP 33"},
	}
	if CapGrants != CapSingleRow || CapWorkers != CapSingleRow || CapPendingIO != CapSingleRow ||
		CapLatches != 20 || CapRG != 32 {
		t.Fatalf("caps = %d/%d/%d/%d/%d, want 1/1/1/20/32", CapGrants, CapWorkers, CapPendingIO, CapLatches, CapRG)
	}
	for _, c := range cases {
		t.Run(c.id, func(t *testing.T) {
			text, ok := extraStatements[c.id]
			if !ok || text == "" {
				t.Fatalf("%s is not in extraStatements", c.id)
			}
			if allow, ok := Statement(c.id); !ok || allow != text {
				t.Errorf("Statement(%q) does not match the extras table", c.id)
			}
			if _, leaked := Statements[c.id]; leaked {
				t.Errorf("%s leaked into the exported Standard view", c.id)
			}
			if !IsAllowed(text) {
				t.Errorf("%s text is not allowlisted", c.id)
			}
			if !strings.HasSuffix(text, "/* unravis:"+c.id+" */") {
				t.Errorf("%s does not end with its /* unravis:%s */ tag", c.id, c.id)
			}
			if err := lintStatement(c.id, text); err != nil {
				t.Errorf("lintStatement: %v", err)
			}
			lower := strings.ToLower(text)
			for _, b := range banned {
				if strings.Contains(lower, b) {
					t.Errorf("%s statement contains %q", c.id, b)
				}
			}
			if got := statementCap(text); got != c.cap {
				t.Errorf("statementCap = %d, want %d", got, c.cap)
			}
			if c.top != "" && !strings.Contains(text, c.top) {
				t.Errorf("statement lacks %q", c.top)
			}
			if c.top == "" && (topRowRe.MatchString(text) || strings.Contains(text, "GROUP BY")) {
				t.Errorf("single-row statement carries TOP or GROUP BY")
			}
		})
	}
}

func TestXSColumnKeyMappings(t *testing.T) {
	want := map[string]string{
		"grants_total": "grantsTotal", "grants_waiting": "grantsWaiting",
		"grants_requested_kb": "grantsRequestedKb", "grants_granted_kb": "grantsGrantedKb",
		"grants_used_kb": "grantsUsedKb", "grants_max_wait_ms": "grantsMaxWaitMs",
		"workers_total": "workersTotal", "workers_running": "workersRunning",
		"workers_runnable": "workersRunnable", "workers_suspended": "workersSuspended",
		"workers_sick": "workersSick", "workers_preemptive": "workersPreemptive",
		"pio_total": "pioTotal", "pio_disk": "pioDisk", "pio_in_os": "pioInOs", "pio_in_sql": "pioInSql",
		"latch_class": "latchClass", "waiting_requests_count": "latchWaitsCum", "latch_wait_ms": "latchWaitMsCum",
		"pool_id": "poolId", "total_cpu_usage_ms": "rgCpuMsCum", "total_memgrant_timeout_count": "rgTimeoutsCum",
		"out_of_memory_count": "rgOomCum", "used_memory_kb": "rgUsedKb", "target_memory_kb": "rgTargetKb",
		"memgrant_waiter_count": "rgWaiters", "active_memgrant_count": "rgActive",
	}
	for col, key := range want {
		got, ok := extraColumnKeys[col]
		if !ok {
			// A column shared with the Standard table is mapped there.
			got, ok = columnKeys[col]
		}
		if !ok || got != key {
			t.Errorf("column %q maps to %q (found=%v), want %q", col, got, ok, key)
		}
	}
}
