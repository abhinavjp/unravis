package bbsql

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

// requests_test.go pins bb:requests' decode contract (BBX-5-11, BL-101): the
// six aggregated columns only, the C6 row cap with the truncated flag, the C9
// wait-type shape with the RUNNING sentinel, missing rather than zero when
// nothing runs, and a payload that is a pure function of the rows.

// requestsSampleRow builds one aggregated row keyed by the SQL column names the
// allowlisted statement selects (the aliases in stmtRequests). Fixtures must use
// those names: decodeRows maps column -> JSON key through columnKeys, so a row
// keyed with the JSON names (waitType, maxWaitMs) is rejected as an unknown
// column and the test would prove nothing.
func requestsSampleRow(waitType string, tasks, maxWaitMs, blocked, headBlockers, maxBlockMs int64) Row {
	return Row{
		"wait_type":     waitType,
		"tasks":         tasks,
		"max_wait_ms":   maxWaitMs,
		"blocked":       blocked,
		"head_blockers": headBlockers,
		"max_block_ms":  maxBlockMs,
	}
}

// requestsRows builds n rows with distinct legal wait types, in statement order
// (bb:requests is ORDER BY COUNT(*) DESC, so the leading rows are the busiest
// groups) for the cap tests.
func requestsRows(n int) []Row {
	rows := make([]Row, 0, n)
	for i := 0; i < n; i++ {
		rows = append(rows, requestsSampleRow(fmt.Sprintf("WRITELOG_%d", i),
			int64(n-i), int64(i*5), int64(i%3), int64(i%2), int64(i*7)))
	}
	return rows
}

// checkRequestsRow compares every field by name, so a column that landed in the
// wrong field says which one.
func checkRequestsRow(t *testing.T, got, want Requests) {
	t.Helper()
	if got.WaitType != want.WaitType {
		t.Errorf("wait_type -> WaitType = %q, want %q", got.WaitType, want.WaitType)
	}
	if got.Tasks != want.Tasks {
		t.Errorf("tasks -> Tasks = %d, want %d", got.Tasks, want.Tasks)
	}
	if got.MaxWaitMs != want.MaxWaitMs {
		t.Errorf("max_wait_ms -> MaxWaitMs = %d, want %d", got.MaxWaitMs, want.MaxWaitMs)
	}
	if got.Blocked != want.Blocked {
		t.Errorf("blocked -> Blocked = %d, want %d", got.Blocked, want.Blocked)
	}
	if got.HeadBlockers != want.HeadBlockers {
		t.Errorf("head_blockers -> HeadBlockers = %d, want %d", got.HeadBlockers, want.HeadBlockers)
	}
	if got.MaxBlockMs != want.MaxBlockMs {
		t.Errorf("max_block_ms -> MaxBlockMs = %d, want %d", got.MaxBlockMs, want.MaxBlockMs)
	}
	if got != want {
		t.Errorf("decoded row = %+v, want %+v", got, want)
	}
}

// requestsPayload asserts the payload is a []Requests of the wanted length and
// returns it.
func requestsPayload(t *testing.T, payload any, wantLen int) []Requests {
	t.Helper()
	got, ok := payload.([]Requests)
	if !ok {
		t.Fatalf("payload type = %T, want []Requests", payload)
	}
	if len(got) != wantLen {
		t.Fatalf("payload has %d rows, want %d", len(got), wantLen)
	}
	return got
}

func TestRequestsDecode(t *testing.T) {
	tests := []struct {
		name string
		rows []Row
		want []Requests
	}{
		{"two groups", []Row{
			requestsSampleRow("PAGEIOLATCH_SH", 7, 1500, 3, 2, 900),
			requestsSampleRow("WRITELOG", 2, 50, 0, 0, 0),
		}, []Requests{
			{WaitType: "PAGEIOLATCH_SH", Tasks: 7, MaxWaitMs: 1500, Blocked: 3, HeadBlockers: 2, MaxBlockMs: 900},
			{WaitType: "WRITELOG", Tasks: 2, MaxWaitMs: 50},
		}},
		{"named running group keeps its own wait type", []Row{requestsSampleRow("RUNNING", 41, 0, 0, 0, 0)},
			[]Requests{{WaitType: "RUNNING", Tasks: 41}}},
		{"zero counts stay zero, not missing", []Row{requestsSampleRow("LCK_M_S", 0, 0, 0, 0, 0)},
			[]Requests{{WaitType: "LCK_M_S"}}},
		{"statement order preserved", []Row{
			requestsSampleRow("CXPACKET", 9, 90, 0, 0, 0),
			requestsSampleRow("", 4, 0, 2, 1, 300),
			requestsSampleRow("LCK_M_X", 1, 5000, 1, 1, 5000),
		}, []Requests{
			{WaitType: "CXPACKET", Tasks: 9, MaxWaitMs: 90},
			{WaitType: WaitTypeRunning, Tasks: 4, Blocked: 2, HeadBlockers: 1, MaxBlockMs: 300},
			{WaitType: "LCK_M_X", Tasks: 1, MaxWaitMs: 5000, Blocked: 1, HeadBlockers: 1, MaxBlockMs: 5000},
		}},
	}
	var s requestsSampler
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			payload, truncated, err := s.Decode(tc.rows)
			if err != nil || truncated {
				t.Fatalf("decode: truncated=%v err=%v, want false/nil", truncated, err)
			}
			got := requestsPayload(t, payload, len(tc.want))
			for i := range tc.want {
				checkRequestsRow(t, got[i], tc.want[i])
			}
		})
	}
}

// TestRequestsEmptyWaitTypeIsRunning is the review-pinned case: bb:requests
// emits an empty wait type for running (non-waiting) requests, and the requests
// decode path maps it onto the fixed RUNNING sentinel instead of rejecting
// nearly every row on a healthy server.
func TestRequestsEmptyWaitTypeIsRunning(t *testing.T) {
	var s requestsSampler
	if WaitTypeRunning != "RUNNING" {
		t.Errorf("WaitTypeRunning = %q, want RUNNING", WaitTypeRunning)
	}
	payload, truncated, err := s.Decode([]Row{requestsSampleRow("", 12, 0, 1, 1, 0)})
	if err != nil || truncated {
		t.Fatalf("empty wait type rejected: truncated=%v err=%v, want false/nil", truncated, err)
	}
	got := requestsPayload(t, payload, 1)
	if got[0].WaitType != WaitTypeRunning {
		t.Errorf("WaitType = %q, want %q", got[0].WaitType, WaitTypeRunning)
	}
	// The sentinel is a fixed token that passes the strict regex, so the
	// payload still carries no free text.
	if !waitTypeRe.MatchString(got[0].WaitType) {
		t.Errorf("sentinel %q fails waitTypeRe; the regex must stay strict", got[0].WaitType)
	}
	checkRequestsRow(t, got[0], Requests{WaitType: WaitTypeRunning, Tasks: 12, Blocked: 1, HeadBlockers: 1})
	// A whole reply of running requests decodes as one RUNNING group.
	allRunning, _, err := s.Decode([]Row{requestsSampleRow("", 5, 0, 0, 0, 0), requestsSampleRow("", 3, 0, 2, 1, 70)})
	if err != nil {
		t.Fatalf("all-running reply: %v", err)
	}
	for _, r := range requestsPayload(t, allRunning, 2) {
		if r.WaitType != WaitTypeRunning {
			t.Errorf("group decoded as %q, want %q", r.WaitType, WaitTypeRunning)
		}
	}
}

// TestRequestsNoForbiddenFields is the BL-101 output gate in both directions:
// a row carrying any session/login/host/program/text/handle/offset/resource
// column is rejected outright (never ignored), and the payload that does decode
// marshals with only the six allowed keys.
func TestRequestsNoForbiddenFields(t *testing.T) {
	var s requestsSampler

	t.Run("forbidden columns are rejected", func(t *testing.T) {
		forbidden := []string{
			"session_id", "login_name", "host_name", "program_name", "sql_text", "text",
			"query_plan", "sql_handle", "plan_handle", "statement_start_offset",
			"statement_end_offset", "resource_description", "physical_name",
			"database_name", "replica_server_name", "waitType",
		}
		for _, col := range forbidden {
			row := requestsSampleRow("WRITELOG", 2, 50, 1, 1, 40)
			row[col] = "corp-node-1\\svc-blackbox"
			payload, truncated, err := s.Decode([]Row{row})
			if err != errUnknownColumn {
				t.Errorf("column %q: err = %v, want %v", col, err, errUnknownColumn)
			}
			if payload != nil || truncated {
				t.Errorf("column %q: payload=%v truncated=%v, want nil/false", col, payload, truncated)
			}
		}
		// All-or-nothing: a poisoned row anywhere in the reply yields no payload,
		// so a partial sample can never be persisted.
		for _, idx := range []int{0, 1} {
			rows := []Row{requestsSampleRow("WRITELOG", 1, 1, 0, 0, 0), requestsSampleRow("LCK_M_X", 1, 1, 0, 0, 0)}
			rows[idx]["session_id"] = int64(57)
			payload, _, err := s.Decode(rows)
			if err != errUnknownColumn || payload != nil {
				t.Errorf("poisoned row %d: payload=%v err=%v, want nil,%v", idx, payload, err, errUnknownColumn)
			}
		}
		// The rejection must not wedge the sampler: it holds no state.
		if payload, truncated, err := s.Decode([]Row{requestsSampleRow("WRITELOG", 1, 1, 0, 0, 0)}); err != nil || truncated || payload == nil {
			t.Errorf("decode after a rejection: payload=%v truncated=%v err=%v, want rows/false/nil", payload, truncated, err)
		}
	})

	t.Run("marshalled payload keys", func(t *testing.T) {
		payload, _, err := s.Decode(requestsRows(3))
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		data, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var docs []map[string]any
		if err := json.Unmarshal(data, &docs); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if len(docs) != 3 {
			t.Fatalf("decoded %d rows, want 3", len(docs))
		}
		want := []string{"waitType", "tasks", "maxWaitMs", "blocked", "headBlockers", "maxBlockMs"}
		for i, doc := range docs {
			if len(doc) != len(want) {
				t.Errorf("row %d has %d keys, want exactly %d: %v", i, len(doc), len(want), doc)
			}
			for _, k := range want {
				if _, ok := doc[k]; !ok {
					t.Errorf("row %d is missing the allowed key %q", i, k)
				}
			}
			for k := range doc {
				lk := strings.ToLower(k)
				for _, banned := range []string{"session", "login", "host", "program", "text", "plan",
					"handle", "offset", "resource", "path", "name", "server", "query", "database"} {
					if strings.Contains(lk, banned) {
						t.Errorf("row %d carries banned key %q (%q)", i, k, banned)
					}
				}
			}
		}
	})

	// The struct itself is numbers plus the one regex-validated wait type.
	typ := reflect.TypeOf(Requests{})
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if f.Name == "WaitType" {
			continue
		}
		if f.Type.Kind() == reflect.String {
			t.Errorf("Requests.%s is a string; only WaitType may carry text", f.Name)
		}
	}
}

// TestRequestsWaitTypeRegex covers the C9 wait-type shape on both sides: the
// legal alphabet decodes, anything else is the fixed errWaitType with no
// payload, and the regex itself is not loosened for the RUNNING sentinel.
func TestRequestsWaitTypeRegex(t *testing.T) {
	var s requestsSampler
	legal := []string{"WRITELOG", "PAGEIOLATCH_SH", "CXPACKET_123", "LCK_M_S", "RUNNING",
		"HADR_FILESTREAM_IOMGR_IOCOMPLETION", "LAZYWRITER_0", strings.Repeat("A", 60)}
	for _, wt := range legal {
		payload, truncated, err := s.Decode([]Row{requestsSampleRow(wt, 1, 1, 0, 0, 0)})
		if err != nil || truncated {
			t.Errorf("wait type %q: truncated=%v err=%v, want false/nil", wt, truncated, err)
			continue
		}
		if got := requestsPayload(t, payload, 1); got[0].WaitType != wt {
			t.Errorf("wait type %q: decoded %q", wt, got[0].WaitType)
		}
	}

	illegal := []string{"lower case", "LATCH;DROP", "PAGEIOLATCH_sh", "WAIT TYPE",
		"'; DROP TABLE x", "LOG-BLOCK", "sp_who", strings.Repeat("A", 61)}
	for _, wt := range illegal {
		payload, truncated, err := s.Decode([]Row{requestsSampleRow(wt, 1, 1, 0, 0, 0)})
		if err != errWaitType {
			t.Errorf("wait type %q: err = %v, want %v", wt, err, errWaitType)
		}
		if payload != nil || truncated {
			t.Errorf("wait type %q: payload=%v truncated=%v, want nil/false", wt, payload, truncated)
		}
	}

	// An illegal wait type anywhere in the reply fails the whole sample: no
	// partially validated group list reaches the file.
	if payload, _, err := s.Decode([]Row{requestsSampleRow("WRITELOG", 3, 0, 0, 0, 0),
		requestsSampleRow("bad type", 1, 1, 0, 0, 0)}); err != errWaitType || payload != nil {
		t.Errorf("illegal row among good ones: payload=%v err=%v, want nil,%v", payload, err, errWaitType)
	}
	// The strict shape is unchanged, and the empty string stays illegal outside
	// the requests mapping.
	if waitTypeRe.MatchString("") || waitTypeRe.MatchString("lower case") || waitTypeRe.MatchString("LATCH;DROP") {
		t.Error("waitTypeRe accepts a shape it must reject; the regex must stay strict")
	}
	// The RUNNING mapping is requests-only: an empty wait type stays rejected on
	// the strict path, so the sentinel cannot be reused to smuggle a blank (or
	// any name) into another kind's payload.
	var strict []Waits
	if _, derr := decodeRows([]Row{{"wait_type": "", "wait_time_ms": int64(1),
		"waiting_tasks_count": int64(1)}}, &strict, CapWaits); derr != errWaitType {
		t.Errorf("an empty wait type escaped the strict path: err=%v, want %v", derr, errWaitType)
	}
}

func TestRequestsCapTruncated(t *testing.T) {
	if CapRequests != 256 {
		t.Errorf("CapRequests = %d, want 256 (the TOP 256 in bb:requests, never 5000)", CapRequests)
	}
	tests := []struct {
		name          string
		rows          int
		wantTruncated bool
		wantKept      int
	}{
		{"cap plus one", CapRequests + 1, true, CapRequests},
		{"exactly cap", CapRequests, false, CapRequests},
		{"well under cap", 14, false, 14},
		{"one row over twice the cap", 2*CapRequests + 1, true, CapRequests},
	}
	var s requestsSampler
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			payload, truncated, err := s.Decode(requestsRows(tc.rows))
			if err != nil {
				t.Fatalf("decode: err=%v, want nil", err)
			}
			if truncated != tc.wantTruncated {
				t.Errorf("truncated = %v, want %v", truncated, tc.wantTruncated)
			}
			got := requestsPayload(t, payload, tc.wantKept)
			if len(got) > CapRequests {
				t.Errorf("payload kept %d rows, over the cap %d", len(got), CapRequests)
			}
			// The rows that survive are the leading ones: the statement sorts by
			// COUNT(*) DESC, so a truncated reply still carries the busiest wait
			// types and the dropped tail is the least busy.
			if got[0].Tasks != uint32(tc.rows) {
				t.Errorf("first kept row Tasks = %d, want %d (the leading row was dropped)", got[0].Tasks, tc.rows)
			}
			if last := got[len(got)-1].WaitType; last != fmt.Sprintf("WRITELOG_%d", len(got)-1) {
				t.Errorf("last kept row = %q, want the row at index %d", last, len(got)-1)
			}
		})
	}
}

func TestRequestsEmptyNil(t *testing.T) {
	var s requestsSampler
	for name, rows := range map[string][]Row{
		"nil rows":   nil,
		"empty rows": {},
	} {
		payload, truncated, err := s.Decode(rows)
		if err != nil || truncated {
			t.Errorf("%s: truncated=%v err=%v, want false/nil", name, truncated, err)
		}
		if payload != nil {
			t.Errorf("%s: payload = %#v, want nil (missing, never an empty array or a zero)", name, payload)
		}
	}
	// Missing survives marshalling as an absent sample, not as rows:[] or 0.
	data, err := json.Marshal(SampleDoc{V: 1, Kind: "requests", Rows: nil})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(data), `"rows":null`) || strings.Contains(string(data), "[]") {
		t.Errorf("an empty sample marshalled as %s, want rows null", data)
	}
}

// TestRequestsDecodeIsPure pins the point-in-time contract: no cross-tick
// state. The same rows always decode the same way, an empty tick does not
// suppress the next one, and no tick can inherit the previous sample.
func TestRequestsDecodeIsPure(t *testing.T) {
	var s requestsSampler
	rows := requestsRows(5)
	first, firstTrunc, err := s.Decode(rows)
	if err != nil || firstTrunc {
		t.Fatalf("first decode: truncated=%v err=%v", firstTrunc, err)
	}
	// A point-in-time group-by repeats identically (unlike a delta sampler,
	// which would emit nothing on the second sighting of the same rows).
	second, _, err := s.Decode(rows)
	if err != nil {
		t.Fatalf("second decode: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Errorf("the same rows decoded differently across ticks: %#v vs %#v", first, second)
	}
	if payload, _, err := s.Decode(nil); err != nil || payload != nil {
		t.Fatalf("empty tick: payload=%v err=%v, want nil/nil", payload, err)
	}
	third, _, err := s.Decode(rows)
	if err != nil {
		t.Fatalf("third decode: %v", err)
	}
	if !reflect.DeepEqual(first, third) {
		t.Errorf("an empty tick changed the next decode: %#v vs %#v", first, third)
	}
	// A different sample decodes from its own rows only.
	fresh, _, err := s.Decode([]Row{requestsSampleRow("WRITELOG", 1, 1, 0, 0, 0)})
	if err != nil {
		t.Fatalf("decode of a fresh sample: %v", err)
	}
	if got := requestsPayload(t, fresh, 1); got[0].WaitType != "WRITELOG" || got[0].Tasks != 1 {
		t.Errorf("decode of a fresh sample = %+v, want the row it was handed", got[0])
	}
	// The sampler type holds no fields at all, so there is nothing to remember.
	if n := reflect.TypeOf(requestsSampler{}).NumField(); n != 0 {
		t.Errorf("requestsSampler has %d fields, want 0 (stateless point-in-time sample)", n)
	}
}

func TestRequestsRegistered(t *testing.T) {
	var found []Sampler
	for _, s := range Samplers() {
		if s.Kind() == "requests" {
			found = append(found, s)
		}
	}
	if len(found) != 1 {
		t.Fatalf("requests samplers registered = %d, want 1", len(found))
	}
	s := found[0]
	if s.ID() != "bb:requests" {
		t.Errorf("ID = %q, want bb:requests", s.ID())
	}
	if s.Interval() != BaseInterval || s.Interval() != 15*time.Second {
		t.Errorf("Interval = %v, want %v (the 15 s cadence)", s.Interval(), BaseInterval)
	}
	if _, ok := Statements[s.ID()]; !ok {
		t.Errorf("statement id %q is not allowlisted", s.ID())
	}
	// The registered instance decodes the fixed statement's own output columns:
	// the allowlist stays the source of truth for what the sampler reads.
	text, ok := Statement("bb:requests")
	if !ok {
		t.Fatal("bb:requests is missing from the allowlist")
	}
	if !strings.Contains(text, "SELECT TOP 256 ") || !strings.Contains(text, "/* unravis:bb:requests */") {
		t.Error("bb:requests is no longer the fixed TOP 256 statement with its tag")
	}
	row := Row{}
	for _, col := range outputColumns(t, text) {
		if col == "wait_type" {
			row[col] = "WRITELOG"
		} else {
			row[col] = int64(1)
		}
	}
	payload, truncated, err := s.Decode([]Row{row})
	if err != nil || truncated {
		t.Fatalf("the statement's own columns were rejected: truncated=%v err=%v", truncated, err)
	}
	requestsPayload(t, payload, 1)
}
