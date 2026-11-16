package bbsql

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// Rows are keyed by the SQL column names the allowlisted bb:waits statement
// SELECTs (wait_type, wait_time_ms, waiting_tasks_count), because that is what
// decodeRows maps through columnKeys. The camelCase JSON keys are OUTPUT names:
// a fixture using them is rejected, so they never appear as input here.

// waitsFixture is one DMV row as sys.dm_os_wait_stats returns it: CUMULATIVE
// counters since instance start, not deltas.
type waitsFixture struct {
	waitType string
	ms       uint64
	tasks    uint64
}

func waitsRows(entries ...waitsFixture) []Row {
	rows := make([]Row, 0, len(entries))
	for _, e := range entries {
		rows = append(rows, Row{
			"wait_type":           e.waitType,
			"wait_time_ms":        int64(e.ms),
			"waiting_tasks_count": int64(e.tasks),
		})
	}
	return rows
}

// waitsRowCount builds n distinct wait types carrying the same cumulative pair,
// for the cap and bounded-state tests.
func waitsRowCount(n int, ms, tasks uint64) []Row {
	rows := make([]Row, 0, n)
	for i := 0; i < n; i++ {
		rows = append(rows, Row{
			"wait_type":           fmt.Sprintf("BB_WAIT_%02d", i),
			"wait_time_ms":        int64(ms),
			"waiting_tasks_count": int64(tasks),
		})
	}
	return rows
}

// waitsRowCountShifted builds n distinct wait types whose names are unique to
// the given cycle, so consecutive cycles share no key (top-20 churn).
func waitsRowCountShifted(cycle, n int, ms uint64) []Row {
	rows := make([]Row, 0, n)
	for i := 0; i < n; i++ {
		rows = append(rows, Row{
			"wait_type":           fmt.Sprintf("BB_SHIFT_%02d_%02d", cycle, i),
			"wait_time_ms":        int64(ms),
			"waiting_tasks_count": int64(i),
		})
	}
	return rows
}

func waitsPayloadDeltas(t *testing.T, payload any) []Waits {
	t.Helper()
	got, ok := payload.([]Waits)
	if !ok {
		t.Fatalf("waits payload=%T %#v, want []Waits", payload, payload)
	}
	return got
}

func waitsDeltasEqual(got, want []Waits) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// wantWaitsJSONKeys is the C9 output shape for one waits row: the wait type and
// two delta numbers, nothing else.
var wantWaitsJSONKeys = []string{"waitType", "deltaMs", "deltaTasks"}

func TestWaitsRegistered(t *testing.T) {
	var found []Sampler
	for _, s := range Samplers() {
		if s.Kind() == "waits" {
			found = append(found, s)
		}
	}
	if len(found) != 1 {
		t.Fatalf("samplers registered with kind \"waits\" = %d, want exactly 1", len(found))
	}
	s := found[0]
	if s.ID() != "bb:waits" {
		t.Errorf("ID = %q, want bb:waits", s.ID())
	}
	// BL-102 is a 60 s sampler (security review and BACKLOG.md): four times the
	// lines at 15 s would buy no extra information.
	if s.Interval() != DemotedInterval {
		t.Errorf("Interval = %v, want %v (the BL-102 cadence)", s.Interval(), DemotedInterval)
	}
	if text, ok := Statement(s.ID()); !ok || text == "" {
		t.Errorf("statement id %q is not resolvable in the allowlist", s.ID())
	}
	// bb:waits reads a DMV every supported instance has, so it must not carry a
	// gate that could silently skip it.
	if _, ok := s.(Gate); ok {
		t.Error("waits implements Gate; it must sample unconditionally")
	}
}

func TestWaitsFirstCycleIsBaselineOnly(t *testing.T) {
	s := &waitsSampler{}
	rows := waitsRows(
		waitsFixture{waitType: "WRITELOG", ms: 1000, tasks: 10},
		waitsFixture{waitType: "PAGEIOLATCH_SH", ms: 500, tasks: 4},
	)
	payload, truncated, err := s.Decode(rows)
	if err != nil || truncated || payload != nil {
		t.Fatalf("first cycle: payload=%v truncated=%v err=%v, want nil,false,nil", payload, truncated, err)
	}
	if len(s.prev) != 2 {
		t.Fatalf("len(prev) = %d, want 2 (baseline not recorded)", len(s.prev))
	}
	// The baseline holds the CUMULATIVE sighting, not a delta.
	if got := s.prev["WRITELOG"]; got.ms != 1000 || got.tasks != 10 {
		t.Errorf("baseline WRITELOG = %+v, want {ms:1000 tasks:10}", got)
	}
}

// waitsScenario is one Decode cycle: the rows the instance answered and the
// deltas the sampler must emit for them (nil means nothing may be emitted).
type waitsScenario struct {
	rows []Row
	want []Waits
}

// TestWaitsDeltaScenarios walks the baseline/delta rules cycle by cycle, in the
// order the runner would call Decode.
func TestWaitsDeltaScenarios(t *testing.T) {
	tests := []struct {
		name   string
		cycles []waitsScenario
	}{
		{
			name: "second cycle reports the increase only",
			cycles: []waitsScenario{
				{rows: waitsRows(waitsFixture{"WRITELOG", 1000, 10})},
				{rows: waitsRows(waitsFixture{"WRITELOG", 1600, 25}),
					want: []Waits{{WaitType: "WRITELOG", DeltaMs: 600, DeltaTasks: 15}}},
			},
		},
		{
			name: "a wait entering the top 20 is baseline only",
			cycles: []waitsScenario{
				{rows: waitsRows(waitsFixture{"WRITELOG", 100, 1})},
				{rows: waitsRows(waitsFixture{"WRITELOG", 300, 3}, waitsFixture{"CXPACKET", 50, 2}),
					want: []Waits{{WaitType: "WRITELOG", DeltaMs: 200, DeltaTasks: 2}}},
				{rows: waitsRows(waitsFixture{"WRITELOG", 400, 4}, waitsFixture{"CXPACKET", 90, 6}),
					want: []Waits{{WaitType: "WRITELOG", DeltaMs: 100, DeltaTasks: 1},
						{WaitType: "CXPACKET", DeltaMs: 40, DeltaTasks: 4}}},
			},
		},
		{
			name: "a wait re-entering after leaving the top 20 emits no delta over the gap",
			cycles: []waitsScenario{
				{rows: waitsRows(waitsFixture{"WRITELOG", 100, 1}, waitsFixture{"CXPACKET", 80, 8})},
				// CXPACKET falls out of the top 20: only WRITELOG is sighted.
				{rows: waitsRows(waitsFixture{"WRITELOG", 200, 2}),
					want: []Waits{{WaitType: "WRITELOG", DeltaMs: 100, DeltaTasks: 1}}},
				// CXPACKET is back. Its last sighting is two cycles old, so the
				// increase over the unobserved interval must not be reported.
				{rows: waitsRows(waitsFixture{"WRITELOG", 300, 3}, waitsFixture{"CXPACKET", 900, 90}),
					want: []Waits{{WaitType: "WRITELOG", DeltaMs: 100, DeltaTasks: 1}}},
				{rows: waitsRows(waitsFixture{"WRITELOG", 350, 3}, waitsFixture{"CXPACKET", 950, 95}),
					want: []Waits{{WaitType: "WRITELOG", DeltaMs: 50, DeltaTasks: 0},
						{WaitType: "CXPACKET", DeltaMs: 50, DeltaTasks: 5}}},
			},
		},
		{
			name: "an all-zero delta is dropped, not written as zeros",
			cycles: []waitsScenario{
				{rows: waitsRows(waitsFixture{"WRITELOG", 100, 5}, waitsFixture{"CXPACKET", 10, 1})},
				{rows: waitsRows(waitsFixture{"WRITELOG", 100, 5}, waitsFixture{"CXPACKET", 30, 1}),
					want: []Waits{{WaitType: "CXPACKET", DeltaMs: 20, DeltaTasks: 0}}},
				// Idle again: both deltas are zero, so the cycle emits nothing.
				{rows: waitsRows(waitsFixture{"WRITELOG", 100, 5}, waitsFixture{"CXPACKET", 30, 1})},
			},
		},
		{
			name: "an empty reply clears every baseline",
			cycles: []waitsScenario{
				{rows: waitsRows(waitsFixture{"WRITELOG", 100, 5})},
				{rows: waitsRows(waitsFixture{"WRITELOG", 250, 9}),
					want: []Waits{{WaitType: "WRITELOG", DeltaMs: 150, DeltaTasks: 4}}},
				// No rows: the instance answered nothing, so nothing may be kept.
				{rows: nil},
				// WRITELOG is re-sighted with counters past the cleared baseline:
				// baseline only, never a delta computed against nothing.
				{rows: waitsRows(waitsFixture{"WRITELOG", 400, 20})},
				{rows: waitsRows(waitsFixture{"WRITELOG", 460, 22}),
					want: []Waits{{WaitType: "WRITELOG", DeltaMs: 60, DeltaTasks: 2}}},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := &waitsSampler{}
			for i, c := range tc.cycles {
				payload, truncated, err := s.Decode(c.rows)
				if err != nil {
					t.Fatalf("cycle %d: err=%v", i, err)
				}
				if truncated {
					t.Errorf("cycle %d: truncated=true, want false", i)
				}
				var got []Waits
				if payload != nil {
					got = waitsPayloadDeltas(t, payload)
				}
				if !waitsDeltasEqual(got, c.want) {
					t.Fatalf("cycle %d: deltas=%#v, want %#v", i, got, c.want)
				}
			}
		})
	}
}

func TestWaitsCounterResetReplacesBaseline(t *testing.T) {
	s := &waitsSampler{}
	if payload, _, err := s.Decode(waitsRows(
		waitsFixture{"WRITELOG", 5000, 500},
		waitsFixture{"CXPACKET", 4000, 40},
	)); err != nil || payload != nil {
		t.Fatalf("baseline: payload=%v err=%v, want nil,nil", payload, err)
	}
	if payload, _, err := s.Decode(waitsRows(
		waitsFixture{"WRITELOG", 6000, 600},
		waitsFixture{"CXPACKET", 4500, 45},
	)); err != nil {
		t.Fatalf("second cycle: %v", err)
	} else if got := waitsPayloadDeltas(t, payload); !waitsDeltasEqual(got, []Waits{
		{WaitType: "WRITELOG", DeltaMs: 1000, DeltaTasks: 100},
		{WaitType: "CXPACKET", DeltaMs: 500, DeltaTasks: 5},
	}) {
		t.Fatalf("second cycle deltas=%#v, want the increase of both counters", got)
	}

	// wait_time_ms going backwards (instance restart, DBCC SQLPERF(...,RESET))
	// is never turned into a wrapped huge number or a faked delta: nothing is
	// emitted for that wait type and its baseline becomes the reset value. The
	// skip is per wait type, so a healthy wait in the same reply is still
	// reported.
	payload, truncated, err := s.Decode(waitsRows(
		waitsFixture{"WRITELOG", 100, 5},
		waitsFixture{"CXPACKET", 5000, 50},
	))
	if err != nil || truncated {
		t.Fatalf("reset cycle: truncated=%v err=%v, want false,nil", truncated, err)
	}
	if got := waitsPayloadDeltas(t, payload); !waitsDeltasEqual(got, []Waits{
		{WaitType: "CXPACKET", DeltaMs: 500, DeltaTasks: 5},
	}) {
		t.Fatalf("reset cycle=%#v, want only the untouched wait type, nothing for the reset one", got)
	}
	if got := s.prev["WRITELOG"]; got.ms != 100 || got.tasks != 5 {
		t.Errorf("baseline after reset = %+v, want the reset sighting {ms:100 tasks:5}", got)
	}

	payload, _, err = s.Decode(waitsRows(
		waitsFixture{"WRITELOG", 180, 9},
		waitsFixture{"CXPACKET", 5100, 55},
	))
	if err != nil {
		t.Fatalf("cycle after reset: %v", err)
	}
	got := waitsPayloadDeltas(t, payload)
	want := []Waits{{WaitType: "WRITELOG", DeltaMs: 80, DeltaTasks: 4},
		{WaitType: "CXPACKET", DeltaMs: 100, DeltaTasks: 5}}
	if !waitsDeltasEqual(got, want) {
		t.Fatalf("delta after reset=%#v, want %#v (must be measured from the reset baseline)", got, want)
	}
}

// TestWaitsTaskCounterRollbackOnly pins that the guard looks at both counters:
// waiting_tasks_count alone going backwards is still a reset.
func TestWaitsTaskCounterRollbackOnly(t *testing.T) {
	s := &waitsSampler{}
	if _, _, err := s.Decode(waitsRows(waitsFixture{"WRITELOG", 1000, 100})); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	payload, _, err := s.Decode(waitsRows(waitsFixture{"WRITELOG", 1100, 10}))
	if err != nil {
		t.Fatalf("reset cycle: %v", err)
	}
	if payload != nil {
		t.Fatalf("tasks-only rollback emitted %#v, want nothing", payload)
	}
}

func TestWaitsCapAndTruncated(t *testing.T) {
	s := &waitsSampler{}
	// Cap+1 rows: decodeRows keeps CapWaits and reports truncated (C6).
	payload, truncated, err := s.Decode(waitsRowCount(CapWaits+1, 100, 10))
	if err != nil || !truncated {
		t.Fatalf("cap+1 first cycle: truncated=%v err=%v, want true,nil", truncated, err)
	}
	if payload != nil {
		t.Error("first cycle must emit nothing even when truncated")
	}
	if len(s.prev) != CapWaits {
		t.Fatalf("len(prev) = %d, want %d (only the kept rows become baselines)", len(s.prev), CapWaits)
	}

	// Cap+1 again: truncated with the deltas of the kept rows.
	payload, truncated, err = s.Decode(waitsRowCount(CapWaits+1, 200, 20))
	if err != nil || !truncated {
		t.Fatalf("cap+1 delta cycle: truncated=%v err=%v, want true,nil", truncated, err)
	}
	got := waitsPayloadDeltas(t, payload)
	if len(got) != CapWaits {
		t.Fatalf("payload rows = %d, want %d", len(got), CapWaits)
	}
	for _, w := range got {
		if w.DeltaMs != 100 || w.DeltaTasks != 10 {
			t.Fatalf("delta = %+v, want {DeltaMs:100 DeltaTasks:10}", w)
		}
	}

	// Exactly CapWaits rows is not truncated.
	payload, truncated, err = s.Decode(waitsRowCount(CapWaits, 300, 30))
	if err != nil || truncated {
		t.Fatalf("exactly cap: truncated=%v err=%v, want false,nil", truncated, err)
	}
	if got := waitsPayloadDeltas(t, payload); len(got) != CapWaits {
		t.Fatalf("payload rows = %d, want %d", len(got), CapWaits)
	}
}

func TestWaitsPrevStaysBounded(t *testing.T) {
	s := &waitsSampler{}
	// Every cycle's key set is disjoint from the last (the top 20 churns), so a
	// merge-instead-of-rebuild bug would grow prev by CapWaits each cycle.
	for cycle := 0; cycle < 6; cycle++ {
		payload, _, err := s.Decode(waitsRowCountShifted(cycle, CapWaits, uint64(1000+cycle)))
		if err != nil {
			t.Fatalf("cycle %d: %v", cycle, err)
		}
		if len(s.prev) > CapWaits {
			t.Fatalf("cycle %d: len(prev) = %d, want <= %d", cycle, len(s.prev), CapWaits)
		}
		if len(s.prev) != CapWaits {
			t.Fatalf("cycle %d: len(prev) = %d, want exactly %d (stale keys kept)", cycle, len(s.prev), CapWaits)
		}
		// Disjoint keys means no baseline for any of them: nothing is emitted.
		if payload != nil {
			t.Fatalf("cycle %d: payload=%#v, want nothing for unseen wait types", cycle, payload)
		}
	}

	// An empty reply clears the baselines: nothing may be remembered forever.
	if _, _, err := s.Decode(nil); err != nil {
		t.Fatalf("empty reply: %v", err)
	}
	if len(s.prev) != 0 {
		t.Errorf("len(prev) = %d after an empty reply, want 0", len(s.prev))
	}
}

func TestWaitsEmptyRows(t *testing.T) {
	s := &waitsSampler{}
	for name, rows := range map[string][]Row{"nil": nil, "empty": {}} {
		payload, truncated, err := s.Decode(rows)
		if err != nil || truncated || payload != nil {
			t.Errorf("%s rows: payload=%v truncated=%v err=%v, want nil,false,nil", name, payload, truncated, err)
		}
		if len(s.prev) != 0 {
			t.Errorf("%s rows: len(prev) = %d, want 0", name, len(s.prev))
		}
	}
}

// TestWaitsRejectsForbiddenColumns is the C9 gate: a row carrying a column
// outside the fixed struct (query text, sessions, plans, names) is rejected by
// decodeRows, and the sampler keeps no part of it. The sampler never pre-filters
// the map, because pre-filtering is how the gate gets bypassed.
func TestWaitsRejectsForbiddenColumns(t *testing.T) {
	banned := []string{"sql_text", "session_id", "text", "query_plan", "statement_text",
		"database_name", "resource_description", "login_name", "physical_name", "plan_handle"}
	for _, col := range banned {
		s := &waitsSampler{}
		base := waitsRows(waitsFixture{"WRITELOG", 100, 5})
		if payload, _, err := s.Decode(base); err != nil || payload != nil {
			t.Fatalf("baseline for %q: payload=%v err=%v", col, payload, err)
		}
		before := map[string]waitsCumulative{}
		for k, v := range s.prev {
			before[k] = v
		}

		row := waitsRows(waitsFixture{"WRITELOG", 200, 9})[0]
		row[col] = "forbidden value"
		payload, truncated, err := s.Decode([]Row{row})
		if err != errUnknownColumn {
			t.Errorf("column %q: err=%v, want %v", col, err, errUnknownColumn)
		}
		if payload != nil || truncated {
			t.Errorf("column %q: payload=%v truncated=%v, want nil,false", col, payload, truncated)
		}
		// The destination stays untouched: no partial decode, no baseline move.
		if len(s.prev) != len(before) {
			t.Fatalf("column %q: prev changed on rejection: %d entries, want %d", col, len(s.prev), len(before))
		}
		for k, v := range before {
			if got := s.prev[k]; got != v {
				t.Errorf("column %q: prev[%q] = %+v, want untouched %+v", col, k, got, v)
			}
		}
	}
}

// TestWaitsInvalidWaitTypeIsFixedError proves the only string in the payload is
// the regex-validated wait type, and that decodeRows' error surfaces verbatim
// (C10: no row content, no wrapping).
func TestWaitsInvalidWaitTypeIsFixedError(t *testing.T) {
	for _, bad := range []string{"", "writelog", "bad;type", "WAIT LOG", strings.Repeat("A", 61)} {
		s := &waitsSampler{}
		payload, truncated, err := s.Decode(waitsRows(waitsFixture{waitType: bad, ms: 1, tasks: 1}))
		if err != errWaitType {
			t.Errorf("wait type %q: err=%v, want %v", bad, err, errWaitType)
		}
		if payload != nil || truncated {
			t.Errorf("wait type %q: payload=%v truncated=%v, want nil,false", bad, payload, truncated)
		}
		if len(s.prev) != 0 {
			t.Errorf("wait type %q: len(prev) = %d, want 0", bad, len(s.prev))
		}
	}
}

// TestWaitsPayloadIsNumbersAndWaitType marshals a real payload and asserts the
// JSON carries exactly the allowed keys: no query text, no names, no session
// data, no wait resource description.
func TestWaitsPayloadIsNumbersAndWaitType(t *testing.T) {
	s := &waitsSampler{}
	if _, _, err := s.Decode(waitsRows(
		waitsFixture{"WRITELOG", 100, 5},
		waitsFixture{"PAGEIOLATCH_SH", 50, 2},
	)); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	payload, _, err := s.Decode(waitsRows(
		waitsFixture{"WRITELOG", 260, 11},
		waitsFixture{"PAGEIOLATCH_SH", 90, 3},
	))
	if err != nil {
		t.Fatalf("delta: %v", err)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	var decoded []map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if len(decoded) != 2 {
		t.Fatalf("payload rows = %d, want 2", len(decoded))
	}
	banned := []string{"name", "login", "host", "program", "path", "text", "replica",
		"server", "query", "session", "sql", "plan", "handle", "resource"}
	for _, r := range decoded {
		if len(r) != len(wantWaitsJSONKeys) {
			t.Fatalf("payload row %v has %d keys, want %d", r, len(r), len(wantWaitsJSONKeys))
		}
		for _, k := range wantWaitsJSONKeys {
			if _, ok := r[k]; !ok {
				t.Errorf("payload row %v is missing %q", r, k)
			}
		}
		for k := range r {
			lk := strings.ToLower(k)
			for _, b := range banned {
				if strings.Contains(lk, b) {
					t.Errorf("payload key %q contains banned %q", k, b)
				}
			}
			allowed := false
			for _, w := range wantWaitsJSONKeys {
				if k == w {
					allowed = true
				}
			}
			if !allowed {
				t.Errorf("payload key %q is not an allowed waits output key", k)
			}
		}
		// Only the wait type is a string; both deltas are numbers.
		if s, ok := r["waitType"].(string); !ok || s == "" {
			t.Errorf("waitType = %v, want a non-empty string", r["waitType"])
		}
		for _, k := range []string{"deltaMs", "deltaTasks"} {
			if _, ok := r[k].(float64); !ok {
				t.Errorf("%s = %#v, want a JSON number", k, r[k])
			}
		}
	}
}

// TestWaitsNoSQLTextLeak keeps the sampler honest about the statement: nothing
// it returns can carry the SQL text it was sampled from.
func TestWaitsNoSQLTextLeak(t *testing.T) {
	s := &waitsSampler{}
	if _, _, err := s.Decode(waitsRows(waitsFixture{"WRITELOG", 10, 1})); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	payload, _, err := s.Decode(waitsRows(waitsFixture{"WRITELOG", 20, 2}))
	if err != nil {
		t.Fatalf("delta: %v", err)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	text, _ := Statement("bb:waits")
	for _, frag := range []string{"SELECT", "sys.dm_os_wait_stats", "TOP 20", "wait_time_ms"} {
		if strings.Contains(string(data), frag) {
			t.Errorf("payload carries %q from the statement text", frag)
		}
	}
	if text == "" {
		t.Fatal("bb:waits is not allowlisted")
	}
}
