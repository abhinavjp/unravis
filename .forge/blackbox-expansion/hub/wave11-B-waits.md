# TASK — BBX-5-12: `internal/bbsql/waits.go` + `internal/bbsql/waits_test.go`

`bb:waits` is `SELECT TOP 20 wait_type, wait_time_ms, waiting_tasks_count FROM
sys.dm_os_wait_stats WHERE <benign filter>` — the values are CUMULATIVE since instance
start, so this sampler computes the deltas in Go. Query-stats deltas are REJECTED by the
security review: nothing here may touch the plan cache or query text.

File 1 (`waits.go`, ~110 lines):
```go
type waitsSampler struct {
	prev map[string]waitsCumulative // key = wait_type; bounded by CapWaits
}
type waitsCumulative struct{ ms, tasks uint64 }
func (waitsSampler) ID() string { return "bb:waits" }      // value receiver: ID/Kind/Interval
func (waitsSampler) Kind() string { return "waits" }
func (waitsSampler) Interval() time.Duration { return DemotedInterval } // 60 s per BL-102
func (s *waitsSampler) Decode(rows []Row) (any, bool, error)
```
- Decode the cumulative rows through `decodeRows` into an intermediate `[]Waits` (reuse the
  struct: `DeltaMs`/`DeltaTasks` carry the current cumulative values on the way in — say so
  in one comment line so the reader knows decodeRows gives cumulative, not delta).
- Then per row: if `prev[t]` missing → skip (first sighting establishes the baseline).
  If current < prev → counter reset: replace the baseline, skip this type (never a negative
  or zero faked delta). Else emit `Waits{WaitType, DeltaMs: cur-prev.ms, DeltaTasks: cur-prev.tasks}`.
- Rebuild `s.prev` from THIS cycle's rows only (so it is bounded by `CapWaits` = 20 keys and
  stale wait types disappear); keep the map allocated across cycles (never nil after first use).
- Result: `nil, truncated, nil` when nothing is due (first cycle → nil, missing never 0);
  otherwise `[]Waits` sorted by the SQL order (do NOT re-sort; the SQL already ordered by
  the cumulative column and dropping keys must not reorder the rest — say why in a comment).
- `init()` registers `&waitsSampler{}`. Add the "not safe for concurrent use, the runner
  calls one sampler at a time" line.

File 2 (`waits_test.go`, ~140 lines, `package bbsql`, own helper `waitsRows(waitType, ms, tasks)`):
- `TestWaitsFirstSampleNil` — first Decode → payload nil, and a second identical Decode
  (same cumulative values) → empty/nil payload with zero deltas dropped (not reported as 0).
- `TestWaitsDelta` — baseline then +500 ms/+7 tasks → exactly that delta.
- `TestWaitsCounterReset` — current < prev → that wait type absent, baseline replaced, and
  the NEXT cycle produces the delta from the new baseline.
- `TestWaitsTop20Bounded` — 30 distinct wait types across cycles (each cycle a different
  set) → `len(prev) <= CapWaits` after each Decode, and truncated true when rows exceed cap.
- `TestWaitsStaleKeysDropped` — a wait type present in cycle 1 and absent in cycle 2 is gone
  from `prev` after cycle 2.
- `TestWaitsNoQueryText` — a row carrying `text`, `sql_text`, `plan_handle` or
  `statement_text` columns → `errUnknownColumn`, payload nil.
- `TestWaitsWaitTypeRegex` — bad wait type rejected.
- `TestWaitsRegistered` — kind "waits", `DemotedInterval`, `Statement("bb:waits")` resolves.
- `TestWaitsDeltaAfterRestartSurvives` — simulate a full instance restart: every current
  value smaller than prev → payload nil for that cycle, no negative delta anywhere.

Reply with TWO fenced ```go blocks — first `waits.go`, then `waits_test.go` — each preceded
by one line giving the path. No third block. (Overrides the one-block rule.)
