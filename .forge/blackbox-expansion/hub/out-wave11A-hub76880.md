`internal/bbsql/requests.go`
```go
package bbsql

import "time"

// requestsSampler decodes the pre-aggregated bb:requests snapshot. The only
// fields it may emit are the BL-101 allowlist: wait type, task count, max wait
// ms, blocked count, head-blocker count and max block ms -- never session id,
// login, host, program, handles, statement offsets or resource_description.
// The '' -> WaitTypeRunning mapping for Requests happens inside decodeRows, so
// this sampler must neither repeat it nor loosen the wait-type validation.
type requestsSampler struct{}

func (requestsSampler) ID() string { return "bb:requests" }

func (requestsSampler) Kind() string { return "requests" }

func (requestsSampler) Interval() time.Duration { return BaseInterval }

// Not safe for concurrent use; the runner never calls a sampler concurrently.
func (s *requestsSampler) Decode(rows []Row) (any, bool, error) {
	var out []Requests
	truncated, err := decodeRows(rows, &out, CapRequests)
	if err != nil {
		return nil, false, err
	}
	if len(out) == 0 {
		return nil, truncated, nil
	}
	return out, truncated, nil
}

func init() { RegisterSampler(&requestsSampler{}) }
```

`internal/bbsql/requests_test.go`
```go
package bbsql

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"testing"
)

// requestsRow returns one well-formed bb:requests row. Column names mirror the
// statement aliases; values are int64 like the SQL driver produces.
func requestsRow(waitType string) Row {
	return Row{
		"wait_type":     waitType,
		"tasks":         int64(1),
		"max_wait_ms":   int64(100),
		"blocked":       int64(0),
		"head_blockers": int64(0),
		"max_block_ms":  int64(10),
	}
}

// requestsRows builds one row per supplied wait type with distinguishable values.
func requestsRows(waitTypes ...string) []Row {
	rows := make([]Row, 0, len(waitTypes))
	for i, wt := range waitTypes {
		rows = append(rows, Row{
			"wait_type":     wt,
			"tasks":         int64(i + 1),
			"max_wait_ms":   int64((i + 1) * 100),
			"blocked":       int64(i),
			"head_blockers": int64(i),
			"max_block_ms":  int64((i + 1) * 10),
		})
	}
	return rows
}

func requestsDecode(t *testing.T, rows []Row) ([]Requests, bool) {
	t.Helper()
	payload, truncated, err := (&requestsSampler{}).Decode(rows)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	out, ok := payload.([]Requests)
	if !ok {
		t.Fatalf("payload type = %T, want []Requests", payload)
	}
	return out, truncated
}

func TestRequestsDecode(t *testing.T) {
	out, truncated := requestsDecode(t, requestsRows("PAGEIOLATCH_SH", "CXPACKET"))
	if truncated {
		t.Fatal("unexpected truncated")
	}
	if len(out) != 2 {
		t.Fatalf("len = %d, want 2", len(out))
	}
	if out[0].WaitType != "PAGEIOLATCH_SH" || out[0].Tasks != 1 ||
		out[0].MaxWaitMs != 100 || out[0].Blocked != 0 ||
		out[0].HeadBlockers != 0 || out[0].MaxBlockMs != 10 {
		t.Fatalf("row 0 = %+v", out[0])
	}
	if out[1].WaitType != "CXPACKET" || out[1].Tasks != 2 ||
		out[1].MaxWaitMs != 200 || out[1].Blocked != 1 ||
		out[1].HeadBlockers != 1 || out[1].MaxBlockMs != 20 {
		t.Fatalf("row 1 = %+v", out[1])
	}
}

func TestRequestsEmptyWaitTypeIsRunning(t *testing.T) {
	out, truncated := requestsDecode(t, requestsRows(""))
	if truncated {
		t.Fatal("unexpected truncated")
	}
	if len(out) != 1 {
		t.Fatalf("len = %d, want 1", len(out))
	}
	if out[0].WaitType != WaitTypeRunning {
		t.Fatalf("WaitType = %q, want %q", out[0].WaitType, WaitTypeRunning)
	}
	if !regexp.MustCompile(`^[A-Z0-9_]{1,60}$`).MatchString(out[0].WaitType) {
		t.Fatalf("WaitType %q fails the wait-type regex", out[0].WaitType)
	}
}

func TestRequestsNoForbiddenFields(t *testing.T) {
	forbidden := []string{
		"session_id", "program_name", "host_name", "login_name",
		"text", "resource_description", "statement_start_offset",
	}
	for _, col := range forbidden {
		col := col
		t.Run(col, func(t *testing.T) {
			row := requestsRow("PAGEIOLATCH_SH")
			row[col] = "x"
			payload, _, err := (&requestsSampler{}).Decode([]Row{row})
			if !errors.Is(err, errUnknownColumn) {
				t.Fatalf("err = %v, want errUnknownColumn", err)
			}
			if payload != nil {
				t.Fatalf("payload = %v, want nil", payload)
			}
		})
	}
}

func TestRequestsWaitTypeRegex(t *testing.T) {
	for _, wt := range []string{"lower case", "LATCH;DROP"} {
		payload, _, err := (&requestsSampler{}).Decode([]Row{requestsRow(wt)})
		if err == nil {
			t.Fatalf("wait_type %q accepted", wt)
		}
		if payload != nil {
			t.Fatalf("wait_type %q: payload = %v, want nil", wt, payload)
		}
	}
}

func TestRequestsCapTruncated(t *testing.T) {
	types := make([]string, 0, CapRequests+1)
	for i := 0; i < CapRequests+1; i++ {
		types = append(types, fmt.Sprintf("WAIT_%d", i))
	}
	out, truncated := requestsDecode(t, requestsRows(types...))
	if !truncated {
		t.Fatal("truncated = false, want true")
	}
	if len(out) != CapRequests {
		t.Fatalf("len = %d, want %d", len(out), CapRequests)
	}
}

func TestRequestsEmptyNil(t *testing.T) {
	payload, truncated, err := (&requestsSampler{}).Decode([]Row{})
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	if truncated {
		t.Fatal("unexpected truncated")
	}
	if payload != nil {
		t.Fatalf("payload = %v, want nil", payload)
	}
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	if string(b) != "null" {
		t.Fatalf("payload JSON = %s, want null", b)
	}
}

func TestRequestsRegistered(t *testing.T) {
	var got Sampler
	for _, s := range Samplers() {
		if s.Kind() == "requests" {
			got = s
			break
		}
	}
	if got == nil {
		t.Fatal("no sampler registered with kind requests")
	}
	if got.ID() != "bb:requests" {
		t.Fatalf("ID = %q, want bb:requests", got.ID())
	}
	if got.Interval() != BaseInterval {
		t.Fatalf("Interval = %v, want %v", got.Interval(), BaseInterval)
	}
	if _, ok := Statement("bb:requests"); !ok {
		t.Fatal("Statement(bb:requests) did not resolve")
	}
}
```

- Assumes `decodeRows` accepts the statement's snake_case aliases (`wait_type`, `tasks`, `max_wait_ms`, `blocked`, `head_blockers`, `max_block_ms`) and maps them onto `Requests`.
- Tests use `int64` row values, matching the driver's numeric type; `decodeRows` owns all conversion.
- Forbidden-column rejection asserts `errors.Is(err, errUnknownColumn)` and a nil payload; no pre-filtering of row maps.
- Empty input is tested both as direct `nil` payload and as JSON `null` (never `[]`).
- Registered-sampler test walks `Samplers()` by `Kind`, so it tolerates peer test samplers with other Kinds.
- No delta state, no goroutines/timers/context, and no mutex, per the runner-owned scheduling contract.
