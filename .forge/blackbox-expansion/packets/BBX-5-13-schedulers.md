# BBX-5-13 SQL schedulers, resource semaphores, tempdb samplers
Backlog: BL-118 | Group: 5 | Model: performance | Reason: three SQL samplers with summed/delta semantics; high security impact.
Depends on: BBX-5-05, BBX-5-09, BBX-5-10 | Blocks: none
Reviewer: Ultimate (SQL).

## Goal
Sample (a) schedulers VISIBLE ONLINE summed in SQL every 15 s, (b) resource semaphore deltas every 15 s, (c) tempdb page totals every 60 s. Numeric/enum output only.

## Context
- New file `internal/bbsql/sched.go` + `sched_test.go`. Implements three Samplers registered via init(): "schedulers" (15 s, bb:schedulers), "semaphores" (15 s, bb:semaphores, deltas in Go), "tempdb" (60 s, bb:tempdb).
- Statements (BBX-5-05): bb:schedulers sums VISIBLE ONLINE rows in SQL (no per-row ids); bb:semaphores returns grantee/waiter/forced/timeout counts (deltas in Go); bb:tempdb sums page totals.
- Security review BL-118 allowed: schedulers VISIBLE ONLINE summed; semaphores grantee/waiter/forced/timeout deltas; tempdb page totals every 60 s.
- Rules: **summed in SQL where stated**; **deltas in Go**; **no text**; **missing never 0**; **bounded state**.

## Do
1. schedulersSampler: Decode the single summed row → Schedulers struct; nil if absent.
2. semaphoresSampler: keep prev per resource_semaphore_id; Decode → deltas (grantees/waiters/forced/timeouts); first sample nil; reset guarded; bound prev map.
3. tempdbSampler: Decode summed page totals → Tempdb struct (60 s); nil if absent.
4. All use StatementID (no SQL building); decodeRows with caps + wait-type n/a.

## Do not
- Do NOT output per-scheduler ids or login/host/program names.
- Do NOT emit 0 on first delta sample (nil); do NOT let prev grow unbounded.
- Do NOT build SQL; no goroutine/ticker.

## Tests to write first (fake rows)
- `TestSchedulersSummed` — single summed row decodes; absent → nil.
- `TestSemaphoresDelta` — two samples → deltas; first nil; reset guarded.
- `TestTempdbTotals` — page totals decode; 60 s interval.
- `TestSchedRegistered` — three kinds with correct intervals/statement ids (all allowlisted).
- `TestSchedNoForbiddenFields` — extra columns rejected.

## Verification checklist
- [ ] go build ./... passes
- [ ] go vet ./... passes
- [ ] go test ./internal/bbsql/... -count=1 passes, tests above included
- [ ] Both build-tag variants compile (GOOS=windows and GOOS=linux go vet ./internal/bbsql/...)
- [ ] Footprint: bounded state; no goroutine/ticker; C13 gate still passes
- [ ] No text captured; summed/delta semantics correct (tests above)
- [ ] Allowlisted statements; missing never 0

## Live-check needed?
YES — owner confirms scheduler/semaphore/tempdb figures on a real SQL Server. Fake tests verify decode/delta only.

## Report back
Files changed, each checklist item ticked or failed with output, anything unconfirmed. Full report to `.forge/blackbox-expansion/reports/BBX-5-13.md`.