package bbsql

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// extras_qhash_test.go covers bb:qhash. Rows are keyed by the SQL column names
// the statement selects (qhash, q_execs, ...), never by the JSON keys: a fixture
// using JSON keys is an unknown column. It reuses the xs* helpers from
// extras_samplers_test.go. Nothing here connects to anything; every row is fake.

// xqH is a valid query hash built from a small number.
func xqH(n int) string { return fmt.Sprintf("0x%016X", n) }

type xqEntry struct {
	hash                      string
	execs, cpu, elapsed, read uint64
}

func xqRows(entries ...xqEntry) []Row {
	rows := make([]Row, 0, len(entries))
	for _, e := range entries {
		rows = append(rows, Row{
			"qhash":        e.hash,
			"q_execs":      int64(e.execs),   //nolint:gosec // test values are small
			"q_cpu_us":     int64(e.cpu),     //nolint:gosec // test values are small
			"q_elapsed_us": int64(e.elapsed), //nolint:gosec // test values are small
			"q_reads":      int64(e.read),    //nolint:gosec // test values are small
		})
	}
	return rows
}

func xqOut(t *testing.T, payload any) []QHash {
	t.Helper()
	if payload == nil {
		return nil
	}
	got, ok := payload.([]QHash)
	if !ok {
		t.Fatalf("qhash payload = %T %#v, want []QHash", payload, payload)
	}
	return got
}

type xqCycle struct {
	epoch uint64
	rows  []Row
	want  []QHash
}

func xqNamed(prefix, n int, v uint64) []Row {
	entries := make([]xqEntry, 0, n)
	for i := 0; i < n; i++ {
		entries = append(entries, xqEntry{xqH(prefix + i), v, v, v, v})
	}
	return xqRows(entries...)
}

// ---------------------------------------------------------------- 1. registration

func TestXQRegistered(t *testing.T) {
	has := func(list []string, kind string) bool {
		for _, k := range list {
			if k == kind {
				return true
			}
		}
		return false
	}
	if !has(ExtrasForProfile(ProfileDeep), "qhash") {
		t.Errorf("ExtrasForProfile(deep) = %v, missing qhash", ExtrasForProfile(ProfileDeep))
	}
	if has(ExtrasForProfile(ProfileEverything), "qhash") {
		t.Errorf("ExtrasForProfile(everything) = %v, must not include qhash", ExtrasForProfile(ProfileEverything))
	}
	if has(ExtrasForProfile(ProfileStandard), "qhash") {
		t.Errorf("standard profile includes qhash")
	}
	for _, s := range Samplers() {
		if s.Kind() == "qhash" || s.ID() == "bb:qhash" {
			t.Error("qhash is in the Standard Samplers() list")
		}
	}
	built := Extras([]string{"qhash"})
	if len(built) != 1 {
		t.Fatalf("Extras([qhash]) built %d samplers, want 1", len(built))
	}
	if built[0].ID() != "bb:qhash" {
		t.Errorf("ID = %q, want bb:qhash", built[0].ID())
	}
	if built[0].Interval() != ExtraEvery5m {
		t.Errorf("Interval = %v, want %v", built[0].Interval(), ExtraEvery5m)
	}
	if _, ok := built[0].(ObservingSampler); !ok {
		t.Error("qhash must implement ObservingSampler")
	}
}

// ---------------------------------------------------------------- 2. deltas

func TestXQDeltaScenarios(t *testing.T) {
	a, b := xqH(1), xqH(2)
	tests := []struct {
		name   string
		cycles []xqCycle
	}{
		{"first cycle is baseline only, second emits all four deltas", []xqCycle{
			{1, xqRows(xqEntry{a, 10, 1000, 2000, 300}), nil},
			{1, xqRows(xqEntry{a, 15, 1700, 2900, 340}), []QHash{{a, 5, 700, 900, 40}}},
		}},
		{"an idle hash (all four deltas zero) is dropped", []xqCycle{
			{1, xqRows(xqEntry{a, 1, 1, 1, 1}, xqEntry{b, 1, 1, 1, 1}), nil},
			{1, xqRows(xqEntry{a, 1, 1, 1, 1}, xqEntry{b, 2, 1, 1, 1}), []QHash{{b, 1, 0, 0, 0}}},
			{1, xqRows(xqEntry{a, 1, 1, 1, 1}, xqEntry{b, 2, 1, 1, 1}), nil},
		}},
		{"a hash with no baseline is skipped, then reported", []xqCycle{
			{1, xqRows(xqEntry{a, 1, 10, 10, 10}), nil},
			{1, xqRows(xqEntry{a, 2, 20, 20, 20}, xqEntry{b, 900, 9000, 9000, 9000}), []QHash{{a, 1, 10, 10, 10}}},
			{1, xqRows(xqEntry{a, 3, 30, 30, 30}, xqEntry{b, 901, 9005, 9006, 9007}), []QHash{
				{a, 1, 10, 10, 10}, {b, 1, 5, 6, 7}}},
		}},
		{"execs going backwards re-baselines", []xqCycle{
			{1, xqRows(xqEntry{a, 100, 10, 10, 10}), nil},
			{1, xqRows(xqEntry{a, 5, 20, 20, 20}), nil},
			{1, xqRows(xqEntry{a, 7, 30, 30, 30}), []QHash{{a, 2, 10, 10, 10}}},
		}},
		{"cpu going backwards re-baselines", []xqCycle{
			{1, xqRows(xqEntry{a, 1, 100, 10, 10}), nil},
			{1, xqRows(xqEntry{a, 2, 50, 20, 20}), nil},
			{1, xqRows(xqEntry{a, 3, 80, 30, 30}), []QHash{{a, 1, 30, 10, 10}}},
		}},
		{"elapsed going backwards re-baselines", []xqCycle{
			{1, xqRows(xqEntry{a, 1, 10, 100, 10}), nil},
			{1, xqRows(xqEntry{a, 2, 20, 50, 20}), nil},
			{1, xqRows(xqEntry{a, 3, 30, 60, 30}), []QHash{{a, 1, 10, 10, 10}}},
		}},
		{"reads going backwards re-baselines", []xqCycle{
			{1, xqRows(xqEntry{a, 1, 10, 10, 100}), nil},
			{1, xqRows(xqEntry{a, 2, 20, 20, 50}), nil},
			{1, xqRows(xqEntry{a, 3, 30, 30, 70}), []QHash{{a, 1, 10, 10, 20}}},
		}},
		{"a reset in one hash does not hide a healthy one", []xqCycle{
			{1, xqRows(xqEntry{a, 100, 100, 100, 100}, xqEntry{b, 1, 1, 1, 1}), nil},
			{1, xqRows(xqEntry{a, 1, 1, 1, 1}, xqEntry{b, 2, 3, 4, 5}), []QHash{{b, 1, 2, 3, 4}}},
		}},
		{"an epoch change re-baselines and emits nothing", []xqCycle{
			{1, xqRows(xqEntry{a, 1, 10, 10, 10}), nil},
			{2, xqRows(xqEntry{a, 99, 999, 999, 999}), nil},
			{2, xqRows(xqEntry{a, 100, 1009, 1009, 1009}), []QHash{{a, 1, 10, 10, 10}}},
		}},
		{"an empty reply clears baselines", []xqCycle{
			{1, xqRows(xqEntry{a, 1, 10, 10, 10}), nil},
			{1, xqRows(xqEntry{a, 2, 20, 20, 20}), []QHash{{a, 1, 10, 10, 10}}},
			{1, nil, nil},
			{1, xqRows(xqEntry{a, 500, 5000, 5000, 5000}), nil},
			{1, xqRows(xqEntry{a, 501, 5010, 5010, 5010}), []QHash{{a, 1, 10, 10, 10}}},
		}},
		{"a zero-length (non-nil) empty reply clears baselines too", []xqCycle{
			{1, xqRows(xqEntry{a, 1, 10, 10, 10}), nil},
			{1, []Row{}, nil},
			{1, xqRows(xqEntry{a, 20, 200, 200, 200}), nil},
			{1, xqRows(xqEntry{a, 21, 210, 210, 210}), []QHash{{a, 1, 10, 10, 10}}},
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := &qhashSampler{}
			for i, c := range tc.cycles {
				payload, truncated, err := xsStep(s, c.epoch, c.rows)
				if err != nil || truncated {
					t.Fatalf("cycle %d: truncated=%v err=%v", i, truncated, err)
				}
				got := xqOut(t, payload)
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

// ---------------------------------------------------------------- 3. ordering

func TestXQOutputSortedByCPUDescendingStable(t *testing.T) {
	h := []string{xqH(1), xqH(2), xqH(3), xqH(4), xqH(5)}
	s := &qhashSampler{}
	if _, _, err := xsStep(s, 1, xqRows(
		xqEntry{h[0], 0, 0, 0, 0}, xqEntry{h[1], 0, 0, 0, 0}, xqEntry{h[2], 0, 0, 0, 0},
		xqEntry{h[3], 0, 0, 0, 0}, xqEntry{h[4], 0, 0, 0, 0})); err != nil {
		t.Fatal(err)
	}
	// CPU deltas: h0=10, h1=500, h2=10, h3=70, h4=10. The ties (h0, h2, h4) must
	// keep their input order.
	payload, _, err := xsStep(s, 1, xqRows(
		xqEntry{h[0], 1, 10, 1, 1}, xqEntry{h[1], 1, 500, 1, 1}, xqEntry{h[2], 1, 10, 1, 1},
		xqEntry{h[3], 1, 70, 1, 1}, xqEntry{h[4], 1, 10, 1, 1}))
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, q := range xqOut(t, payload) {
		order = append(order, q.Hash)
	}
	want := []string{h[1], h[3], h[0], h[2], h[4]}
	if !reflect.DeepEqual(order, want) {
		t.Errorf("order = %v, want %v (CPU desc, stable for ties)", order, want)
	}
}

// ---------------------------------------------------------------- 4. cap

func TestXQCapKeepsCapAndTruncates(t *testing.T) {
	s := &qhashSampler{}
	payload, truncated, err := xsStep(s, 1, xqNamed(0, CapQHash+1, 1))
	if err != nil || payload != nil || !truncated {
		t.Fatalf("first cycle: payload=%v truncated=%v err=%v, want nil,true,nil", payload, truncated, err)
	}
	if len(s.prev) != CapQHash {
		t.Fatalf("len(prev) = %d, want %d", len(s.prev), CapQHash)
	}
	if _, ok := s.prev[xqH(CapQHash)]; ok {
		t.Error("the row beyond the cap was baselined")
	}
	payload, truncated, err = xsStep(s, 1, xqNamed(0, CapQHash+1, 3))
	if err != nil || !truncated {
		t.Fatalf("second cycle: truncated=%v err=%v, want true,nil", truncated, err)
	}
	if got := xqOut(t, payload); len(got) != CapQHash {
		t.Fatalf("emitted %d rows, want %d", len(got), CapQHash)
	}
}

func TestXQBaselineStaysBoundedUnderChurn(t *testing.T) {
	s := &qhashSampler{}
	for cycle := 0; cycle < 50; cycle++ {
		// Every cycle carries more rows than the cap, all with hashes no earlier
		// cycle used.
		if _, _, err := xsStep(s, 1, xqNamed(cycle*1000, CapQHash+5, uint64(cycle))); err != nil {
			t.Fatalf("cycle %d: %v", cycle, err)
		}
		if len(s.prev) > CapQHash {
			t.Fatalf("cycle %d: baseline holds %d entries, cap is %d", cycle, len(s.prev), CapQHash)
		}
	}
}

// ---------------------------------------------------------------- 5. validation

func TestXQInvalidHashIsRejectedWithoutLeaking(t *testing.T) {
	bad := []string{
		"0x0123456789ABCDE",
		"0x0123456789ABCDEF0",
		"0123456789ABCDEF",
		"0x0123456789ABCDEG",
		"",
		"0x0123456789ABC\nEF",
		"0x0123456789ABCDEF;DROP",
		"0x0123456789ABCDEF\n",
		"0X0123456789ABCDEF",
		" 0x0123456789ABCDEF",
	}
	for _, name := range bad {
		t.Run(fmt.Sprintf("%q", name), func(t *testing.T) {
			s := &qhashSampler{}
			good := xqH(7)
			if _, _, err := xsStep(s, 1, xqRows(xqEntry{good, 1, 1, 1, 1})); err != nil {
				t.Fatalf("baseline: %v", err)
			}
			rows := xqRows(xqEntry{good, 2, 2, 2, 2}, xqEntry{name, 424242, 424242, 424242, 424242})
			payload, truncated, err := xsStep(s, 1, rows)
			if payload != nil || truncated {
				t.Errorf("payload=%#v truncated=%v, want nil,false", payload, truncated)
			}
			if !errors.Is(err, errQHash) {
				t.Errorf("err = %v, want errQHash", err)
			}
			xsNoLeak(t, "invalid hash", err, name, "424242", good, "qhash", "qHash")
			// An error is not a sample: the baseline survives it untouched.
			payload, _, err = xsStep(s, 1, xqRows(xqEntry{good, 5, 6, 7, 8}))
			if err != nil {
				t.Fatalf("after the error: %v", err)
			}
			if got := xqOut(t, payload); !reflect.DeepEqual(got, []QHash{{good, 4, 5, 6, 7}}) {
				t.Errorf("after the error: got %#v, want the delta against the old baseline", got)
			}
		})
	}
}

func TestXQAcceptsUpperAndLowerCaseHex(t *testing.T) {
	for _, name := range []string{"0x0123456789ABCDEF", "0x0123456789abcdef", "0xaBcDeF0123456789", "0x0000000000000000"} {
		s := &qhashSampler{}
		if _, _, err := xsStep(s, 1, xqRows(xqEntry{name, 1, 1, 1, 1})); err != nil {
			t.Fatalf("%q rejected: %v", name, err)
		}
		payload, _, err := xsStep(s, 1, xqRows(xqEntry{name, 2, 3, 4, 5}))
		if err != nil {
			t.Fatalf("%q rejected on cycle 2: %v", name, err)
		}
		if got := xqOut(t, payload); !reflect.DeepEqual(got, []QHash{{name, 1, 2, 3, 4}}) {
			t.Errorf("%q: got %#v", name, got)
		}
	}
}

// ---------------------------------------------------------------- 6. JSON contract

func TestXQJSONKeysAndInputContract(t *testing.T) {
	s := &qhashSampler{}
	h := xqH(9)
	_, _, _ = xsStep(s, 1, xqRows(xqEntry{h, 1, 1, 1, 1}))
	payload, _, err := xsStep(s, 1, xqRows(xqEntry{h, 3, 5, 7, 9}))
	if err != nil {
		t.Fatal(err)
	}
	got := xqOut(t, payload)
	if len(got) != 1 {
		t.Fatalf("got %#v, want one row", got)
	}
	xsKeysEqual(t, xsJSONKeys(t, got[0]), []string{"qHash", "qExecs", "qCpuUs", "qElapsedUs", "qReads"})

	good := func() Row { return xqRows(xqEntry{h, 424242, 424242, 424242, 424242})[0] }
	forbidden := []string{"424242", "qhash", "q_execs", "q_cpu_us", "q_elapsed_us", "q_reads",
		"qHash", "qExecsCum", "qCpuUsCum", "qElapsedUsCum", "qReadsCum"}
	for _, col := range []string{"qhash", "q_execs", "q_cpu_us", "q_elapsed_us", "q_reads"} {
		missing, null := good(), good()
		delete(missing, col)
		null[col] = nil
		for what, row := range map[string]Row{"missing": missing, "null": null} {
			payload, _, err := xsStep(&qhashSampler{}, 1, []Row{row})
			if payload != nil {
				t.Errorf("%s %s: payload = %#v, want nil", what, col, payload)
			}
			xsNoLeak(t, what+" "+col, err, forbidden...)
		}
	}
	unknown := good()
	unknown["sql_handle"] = "SECRET"
	payload, _, err = xsStep(&qhashSampler{}, 1, []Row{unknown})
	if payload != nil {
		t.Errorf("unknown column: payload = %#v, want nil", payload)
	}
	xsNoLeak(t, "unknown column", err, append(forbidden, "sql_handle", "SECRET")...)

	// JSON keys used as column names are unknown columns.
	jsonKeys := Row{"qHash": h, "qExecs": int64(1), "qCpuUs": int64(1), "qElapsedUs": int64(1), "qReads": int64(1)}
	payload, _, err = xsStep(&qhashSampler{}, 1, []Row{jsonKeys})
	if payload != nil {
		t.Errorf("JSON-key fixture: payload = %#v, want nil", payload)
	}
	xsNoLeak(t, "JSON-key fixture", err, forbidden...)
}

// ---------------------------------------------------------------- 7. statement

func TestXQStatementContract(t *testing.T) {
	text, ok := Statement("bb:qhash")
	if !ok || text == "" {
		t.Fatal("Statement(bb:qhash) does not exist")
	}
	if CapQHash != 50 {
		t.Fatalf("CapQHash = %d, want 50", CapQHash)
	}
	for _, want := range []string{"TOP 51", "GROUP BY query_hash"} {
		if !strings.Contains(text, want) {
			t.Errorf("statement lacks %q", want)
		}
	}
	lower := strings.ToLower(text)
	for _, banned := range []string{"sql_handle", "dm_exec_sql_text", "statement_start_offset"} {
		if strings.Contains(lower, banned) {
			t.Errorf("statement contains %q", banned)
		}
	}
	if err := lintStatement("bb:qhash", text); err != nil {
		t.Errorf("lintStatement: %v", err)
	}
	if !IsAllowed(text) {
		t.Error("statement text is not allowlisted")
	}
	if got := statementCap(text); got != CapQHash {
		t.Errorf("statementCap = %d, want %d", got, CapQHash)
	}
}
