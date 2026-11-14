# BBX-5-10 SQL runner loop + budget benchmark (C13)
Backlog: BL-124 | Group: 5 | Model: performance | Reason: the isolated SQL tick loop + footprint gate; high security/availability impact.
Depends on: BBX-5-06, BBX-5-07, BBX-5-08, BBX-5-09 | Blocks: BBX-5-02 (sql-service mode), samplers register into it
Reviewer: Ultimate (SQL).

## Goal
Run the SQL samplers in the separate service on their own tick, isolated from host sampling: connect with the ceiling check, skip-if-running, whole-tick timeout, write NDJSON + status to the locked folder, apply back-off, and pass a fake-Queryer footprint benchmark.

## Context
- New files `internal/bbsql/runner.go` + `runner_test.go` + `budget_test.go`. Uses Open/CheckLocalTarget/Queryer (5-06), CheckCeiling/Classify (5-07), State/Load/Save (5-08), decodeRows/output structs (5-09), Statements/IsAllowed (5-05).
- C4: whole tick 5 s; a running tick means the next is skipped and counted, never queued or run in parallel.
- C11: status.json = `{enabled, state, lastOK, failures}` only (read by host /status, BBX-5-04).
- C13: fake-Queryer benchmark — under 1% of one core on average, under 10 MB extra memory, under 2 KB per sample.
- Rules: **SQL isolated from host sampling** (separate process/service); **skip-if-running, never parallel**; **auto-off survives restart**; **no SQL data over HTTP**; **fixed codes**; **footprint budget**.

## Do
1. `type Sampler interface { Kind() string; Interval() time.Duration; StatementID() string; Decode(rows []Row) (any, bool, error) }` (bool = truncated). `func RegisterSampler(s Sampler)` + registry (samplers 5-11..14 register from init()).
2. `type Runner struct { db Queryer; dir string; state State; samplers []Sampler; lastRun map[string]time.Time; running bool; ... }`.
3. `func (r *Runner) Tick(now)`:
   - If running → skip, count a skipped tick, return (never parallel).
   - If state.AutoOff → write status auto-off, return.
   - Enforce a 5 s whole-tick context. On (re)connect: Open + CheckCeiling; ceiling failure → state.OnFailure(ceiling=true), save, return.
   - For each sampler due (now - lastRun >= its Interval, respecting slow demotion): run its allowlisted statement via Query (3 s/5 s per-query timeout), Decode, write one NDJSON line to `<dir>\bb-sql-YYYYMMDD-HH.ndjson`. On failure → state.OnFailure(permission?); on success → state.OnSuccess; track slow usage → state.OnSlow.
   - Save state; write status.json `{enabled, state, lastOK, failures}` (no instance/error text).
4. `func (r *Runner) Run(ctx)` — ticker at the base interval; call Tick; stop on ctx.Done. (This loop lives ONLY in the SQL service process.)
5. `budget_test.go`: `BenchmarkSQLTick` + `TestSQLBudget` with a fake Queryer returning fixed rows for all samplers; assert < 2 KB per sample (measure marshaled line size), bounded allocs, and average tick cost under 1% of one core at 15 s (150 ms). Record measured figures.

## Do not
- Do NOT run SQL in the host recorder process (separate service only).
- Do NOT run ticks in parallel or queue them (skip-if-running).
- Do NOT serve SQL data over HTTP; status.json carries only the C11 fields.
- Do NOT write driver text/instance names to NDJSON or status.
- Do NOT clear auto-off on start.
- Do NOT exceed the whole-tick 5 s or per-query timeouts.

## Tests to write first (fake Queryer)
- `TestRunnerSkipIfRunning` — a slow fake query holds running=true; the next Tick is skipped and counted, not parallel.
- `TestRunnerCeilingFailureAutoOff` — fake ceiling refuses → state auto-off, persisted.
- `TestRunnerWritesNDJSON` — fake rows → one line per due sampler in the hourly file.
- `TestRunnerStatusShape` — status.json has only enabled/state/lastOK/failures.
- `TestRunnerIntervalRespect` — a 60 s sampler is not run on a 15 s tick.
- `TestRunnerBackoffOnFailure` — query failure doubles interval; permission → auto-off.
- `TestSQLBudget` (C13) — < 2 KB/sample, bounded allocs, < 150 ms/tick average.

## Verification checklist
- [ ] go build ./... passes
- [ ] go vet ./... passes
- [ ] go test ./internal/bbsql/... -count=1 passes, tests above included
- [ ] go test ./internal/bbsql/ -bench BenchmarkSQLTick -benchtime=1s -run ^$ records figures (paste in report)
- [ ] Both build-tag variants compile (GOOS=windows and GOOS=linux go vet ./internal/bbsql/...)
- [ ] Footprint: C13 gate passes (<1% core, <10 MB, <2 KB/sample); skip-if-running; no parallel ticks
- [ ] No text captured — NDJSON/status numeric/enum only
- [ ] C4 whole-tick 5 s + per-query timeouts; C7 back-off/auto-off; C11 status shape
- [ ] SQL isolated in its own service loop (not the host recorder)

## Live-check needed?
YES — owner runs the SQL service on a real server and confirms host sampling is unaffected by a slow SQL query, and the footprint stays in budget. Fake bench verifies the code path only.

## Report back
Files changed, benchmark figures, each checklist item ticked or failed with output, anything unconfirmed. Full report to `.forge/blackbox-expansion/reports/BBX-5-10.md`.