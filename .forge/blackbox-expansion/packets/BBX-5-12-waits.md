# BBX-5-12 SQL wait-stats deltas sampler (60 s, top 20, benign filtered)
Backlog: BL-102 | Group: 5 | Model: performance | Reason: delta state + benign filter; high security impact.
Depends on: BBX-5-05, BBX-5-09, BBX-5-10 | Blocks: none
Reviewer: Ultimate (SQL).

## Goal
Every 60 s, sample wait-stats deltas (top 20, benign waits filtered) — output wait type, delta counts and ms only. No query text. Query-stats deltas are REJECTED by the security review and must not be built.

## Context
- New file `internal/bbsql/waits.go` + `waits_test.go`. Implements Sampler: Kind()="waits", Interval()=60s, StatementID()="bb:waits", Decode→[]Waits. Register via init().
- `bb:waits` (BBX-5-05) is SELECT TOP 20 wait_type, wait_time_ms, waiting_tasks_count with the benign filter (from dbhealth/sql.go:101-112 LIST). Deltas computed in Go: keep the previous cumulative wait_time_ms/tasks per wait_type; delta = current - previous (guard counter reset → skip that wait_type this cycle).
- Security review BL-102: allow at 60 s, top 20, benign filtered; output wait type, delta counts and ms. Query-stats deltas REJECTED (scans plan cache, unbounded state).
- Rules: **deltas in Go**; **top 20**; **benign filter**; **no query text**; **missing never 0**; **bounded state** (only the top-20 wait types tracked).

## Do
1. `type waitsSampler struct { prev map[string]struct{ ms, tasks uint64 } }` implementing Sampler; init() RegisterSampler.
2. `Decode(rows)`: decode current cumulative values (cap 20); compute deltas vs prev; update prev; return []Waits with DeltaMs/DeltaTasks. First sample (no prev) → return nil (no deltas yet, never 0). Counter reset (current < prev) → skip that wait type, reset its baseline.
3. Bound prev to the top-20 keys (drop keys not seen) so state cannot grow unbounded.

## Do not
- Do NOT build query-stats deltas (rejected) or read the plan cache.
- Do NOT output query text or wait resource descriptions.
- Do NOT emit 0 deltas on the first sample (return nil).
- Do NOT let prev grow unbounded; no goroutine/ticker.

## Tests to write first (fake rows)
- `TestWaitsDelta` — two samples → correct delta ms/tasks.
- `TestWaitsFirstSampleNil` — first sample → nil (no zero deltas).
- `TestWaitsCounterReset` — current < prev → that wait type skipped, baseline reset.
- `TestWaitsTop20Bounded` — >20 keys → prev bounded to 20.
- `TestWaitsNoQueryText` — a row with a query-text column → rejected.
- `TestWaitsRegistered` — Kind/Interval(60s)/StatementID correct.

## Verification checklist
- [ ] go build ./... passes
- [ ] go vet ./... passes
- [ ] go test ./internal/bbsql/... -count=1 passes, tests above included
- [ ] Both build-tag variants compile (GOOS=windows and GOOS=linux go vet ./internal/bbsql/...)
- [ ] Footprint: bounded prev map; no goroutine/ticker; C13 gate still passes
- [ ] No text captured; no query-stats deltas (code review + TestWaitsNoQueryText)
- [ ] Deltas in Go; top 20; first sample nil; reset guarded (tests above)
- [ ] Missing never 0

## Live-check needed?
YES — owner confirms on a real SQL Server that wait deltas match perfmon wait-stats deltas over 60 s. Fake tests verify delta logic only.

## Report back
Files changed, each checklist item ticked or failed with output, anything unconfirmed. Full report to `.forge/blackbox-expansion/reports/BBX-5-12.md`.