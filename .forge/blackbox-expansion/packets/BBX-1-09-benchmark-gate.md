# BBX-1-09 Footprint benchmark gate (fake readers)
Backlog: BL-122 | Group: 1 | Model: performance | Reason: defines the pass/fail footprint gate every later group must clear; needs careful fake design.
Depends on: BBX-1-01, BBX-1-02, BBX-1-05 | Blocks: group 2+ (gate must pass before any new group ships)

## Goal
Add a benchmark + gate test proving a full budgeted tick with fake counter readers stays inside the BL-122 footprint budget. This is the gate the handoff requires before any new counter group ships.

## Context
- New file `internal/blackbox/budget_bench_test.go`. Package `blackbox`.
- Uses: `Engine`/`CounterSource`/`Reading` (engine.go), `Budget` (budget.go), `NewGroupCollector`/`Group`/`BuildGroups` (groups.go), `Sample` (sample.go).
- Fake pattern to follow: `fakeSource`/`fakeExtras` in `collector_test.go:13-58`; budget precedent `internal/sampler/bench_test.go:26 BenchmarkTick` + `TestTickStaysInTheBudget` (fails > 15 ms/tick).
- Rules that apply: **under 0.5% of one core on average** → at a 15 s interval that is 75 ms of CPU per tick; **under 50 MB**; **each group within 1 s**; **a benchmark test with fake readers must pass before any new group ships** (BL-122).

## Do
1. Build a `fakeCounterSource` implementing `CounterSource`: returns a fixed slice of ~200 valid `Reading`s (mix of gauges and rates) with Status 0; `Close()` nil. No real PDH.
2. Build 6 fake groups (implement `Group`) that each run the engine over a subset of readings and return a small group struct (e.g. &TCPGroup, &MemPressureGroup). Mark 2 optional, 4 mandatory. Register them in the test (do not pollute the production registry — construct `[]Group` directly and pass to `NewGroupCollector`).
3. `BenchmarkBudgetTick(b *testing.B)` — one inner host fake collector + the 6 groups + a real Budget; each iteration calls `groupCollector.Collect(now)`; report ns/op and allocs/op (`b.ReportAllocs()`).
4. `TestBudgetTickStaysInTheBudget(t *testing.T)` — run N=200 ticks with the fake source; measure total wall/monotonic time; FAIL if average > 75 ms/tick (0.5% of one core at 15 s). Also assert per-tick allocations are bounded (e.g. < 400 allocs/tick — tune to the real figure and record it in the report; the assertion must be stable, not flaky).
5. `TestBudgetSlowGroupSkipped` — a fake group that sleeps > 1 s (use a fake clock/duration injection, NOT a real 1 s sleep in the test) is marked slow and skipped next tick; assert the gate still passes with it skipped.
6. Keep the benchmark deterministic: no real sleeps, no real counters, no network.

## Do not
- Do not use real PDH or real process reads in the gate (fakes only — tests must not need Windows counters).
- Do not make the gate flaky: use a generous, stable threshold and fixed fake data; record the measured figure in the report.
- Do not register test groups into the production registry (construct the slice directly).
- Do not add a real 1 s sleep; inject duration.

## Tests to write first
The three above ARE the tests. Names: `BenchmarkBudgetTick`, `TestBudgetTickStaysInTheBudget`, `TestBudgetSlowGroupSkipped`.

## Verification checklist
- [ ] go build ./... passes
- [ ] go vet ./... passes
- [ ] go test ./internal/blackbox/... -count=1 passes (gate included)
- [ ] go test ./internal/blackbox/ -run TestBudgetTickStaysInTheBudget -count=1 passes
- [ ] go test ./internal/blackbox/ -bench BenchmarkBudgetTick -benchtime=1s -run ^$ records ns/op + allocs/op (paste in report)
- [ ] Both build-tag variants compile (GOOS=windows and GOOS=linux go vet ./internal/blackbox/...)
- [ ] Footprint: gate asserts < 75 ms/tick average and bounded allocs; no real sleeps
- [ ] Missing reading is omitted, never 0 (fake source includes an invalid reading; assert its field absent)
- [ ] No text captured
- [ ] Gate passes BEFORE any group-2 packet is dispatched (orchestrator checks)

## Live-check needed?
YES — the fake gate proves the code path is cheap; the owner must confirm the REAL footprint on a server (< 0.5% core, < 50 MB, each group < 1 s) over 10 minutes. Record both clearly.

## Report back
Files changed, benchmark ns/op + allocs/op, measured average ms/tick, each checklist item ticked or failed with output. Full report to `.forge/blackbox-expansion/reports/BBX-1-09.md`.