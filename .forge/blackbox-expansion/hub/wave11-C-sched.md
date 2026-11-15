# TASK — BBX-5-13: `internal/bbsql/sched.go` + `internal/bbsql/sched_test.go`

Three samplers in ONE file (BL-118): schedulers (15 s, summed in SQL), resource semaphores
(15 s, deltas in Go), tempdb (60 s, summed in SQL). No per-scheduler ids, no logins, no
hosts, no program names, no text.

File 1 (`sched.go`, ~180 lines). Exact names:
```go
type schedulersSampler struct{}          // ID "bb:schedulers", Kind "schedulers", BaseInterval
type semaphoresSampler struct{ prev map[int32]semaphoreCumulative } // ID "bb:semaphores",
                                          // Kind "semaphores", BaseInterval
type tempdbSampler struct{}              // ID "bb:tempdb", Kind "tempdb", DemotedInterval
type semaphoreCumulative struct{ grantees, waiters, forced, timeouts int64 }
```
- `schedulersSampler.Decode` / `tempdbSampler.Decode`: `decodeRows` into the fixed struct
  slice with `CapSchedulers` / `CapTempdb`; the SQL already sums only VISIBLE ONLINE rows,
  so pass the single row through unchanged; empty → `nil` (never zeros); return
  `out[0]` (a value, not a slice) so the payload is one object — say why in one comment.
- `semaphoresSampler.Decode`: cumulative counts arrive per `resource_semaphore_id`; compute
  deltas in Go exactly like a counter-delta sampler — first sighting only sets the baseline,
  current < prev means reset (replace baseline, skip the id), `prev` is rebuilt from this
  cycle's rows so it stays bounded by `CapSemaphores` (16); deltas of 0 across all four
  counters for an id are still emitted (a steady grant is evidence), but a wait type… no:
  emit the row whenever the id has a baseline. Payload nil on the first cycle.
- `grantees`/`waiters` are INSTANTaneous counts, `forced`/`timeout_count` are cumulative:
  emit `Grantees`/`Waiters` as read (current values) and `Forced`/`Timeouts` as deltas.
  Put that rule in the doc comment — it is the subtle part and the reviewer will look for it.
- `init()` registers all three; one line each on the "not concurrent, runner serialises" rule.

File 2 (`sched_test.go`, ~180 lines, `package bbsql`, own helpers):
- `TestSchedulersSummed` — one row → `Schedulers` value with both fields; no rows → nil.
- `TestSchedulersCapOne` — 2 rows → truncated true, 1 kept.
- `TestTempdbTotals` — page totals decode; payload is an object, not an array (assert by
  marshaling and checking the first non-space byte is `{`).
- `TestSemaphoresDelta` — cycle 1 baseline → nil; cycle 2 with forced +5, timeouts +2,
  grantees/waiters changed → forced/timeouts are the DELTAS and grantees/waiters are the
  CURRENT values.
- `TestSemaphoresReset` — a counter going backwards replaces the baseline and that id is
  absent for the cycle; the next cycle's delta is measured from the new baseline.
- `TestSemaphoresBoundedPrev` — 20 distinct ids across cycles → `len(prev) <= CapSemaphores`.
- `TestSemaphoresStaleIDDropped` — an id gone in cycle 2 is dropped from `prev`.
- `TestSchedRegistered` — the three kinds present with the right IDs/intervals and every ID
  resolves through `Statement`.
- `TestSchedNoForbiddenFields` — rows carrying `scheduler_id`, `parent_node_id`,
  `login_name`, `host_name`, `program_name`, `description`, `text` are rejected
  (`errUnknownColumn`) and produce a nil payload.

Reply with TWO fenced ```go blocks — first `sched.go`, then `sched_test.go` — each preceded
by one line giving the path. No third block. (Overrides the one-block rule.)
