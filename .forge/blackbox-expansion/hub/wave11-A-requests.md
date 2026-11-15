# TASK — BBX-5-11: `internal/bbsql/requests.go` + `internal/bbsql/requests_test.go`

`bb:requests` already aggregates in SQL (GROUP BY wait type, TOP 256, own session
excluded, `ISNULL(wait_type, N'') AS wait_type`), so this sampler only decodes and shapes.
It is instantaneous — NO delta state.

File 1 (`requests.go`, ~70 lines):
```go
type requestsSampler struct{}
func (requestsSampler) ID() string { return "bb:requests" }
func (requestsSampler) Kind() string { return "requests" }
func (requestsSampler) Interval() time.Duration { return BaseInterval }
func (s *requestsSampler) Decode(rows []Row) (any, bool, error)
```
- `Decode`: `var out []Requests`; `truncated, err := decodeRows(rows, &out, CapRequests)`;
  on err return `nil, false, err`; on `len(out) == 0` return `nil, truncated, nil`
  (missing is omitted, never an empty array in the payload); else return `out, truncated, nil`.
- `init()` registers `&requestsSampler{}`.
- One comment line stating the allowed output (BL-101: wait type, task count, max wait ms,
  blocked count, head-blocker count, max block ms — never session id/login/host/program/
  handles/statement offsets/resource_description) and that `''` becomes `WaitTypeRunning`
  inside `decodeRows` (do not repeat that mapping and never loosen the regex).

File 2 (`requests_test.go`, ~120 lines, `package bbsql`, no external deps, own local
helper `requestsRows(...)`):
- `TestRequestsDecode` — 2 rows → correct field values, truncated false.
- `TestRequestsEmptyWaitTypeIsRunning` — `wait_type: ""` → `Requests[0].WaitType == WaitTypeRunning`
  and the payload still matches `^[A-Z0-9_]{1,60}$`.
- `TestRequestsNoForbiddenFields` — a row carrying `session_id`, `program_name`,
  `host_name`, `login_name`, `text`, `resource_description`, `statement_start_offset`
  (each as a single extra column in turn) is rejected by `decodeRows`; assert the error is
  the fixed `errUnknownColumn` and the returned payload is nil.
- `TestRequestsWaitTypeRegex` — `wait_type: "lower case"` and `"LATCH;DROP"` rejected.
- `TestRequestsCapTruncated` — `CapRequests+1` rows → truncated true and exactly
  `CapRequests` rows kept.
- `TestRequestsEmptyNil` — no rows → payload nil (JSON of the payload must be `null`, not `[]`).
- `TestRequestsRegistered` — the sampler reached through `Samplers()` by kind "requests"
  has ID `bb:requests`, `Statement("bb:requests")` resolves, and interval is `BaseInterval`.

Reply with TWO fenced ```go blocks — first `requests.go`, then `requests_test.go` — each
preceded by one line giving the path. No third block. (This overrides the one-block rule.)
