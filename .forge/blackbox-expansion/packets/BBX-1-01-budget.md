# BBX-1-01 Self-budget state machine
Backlog: BL-122 | Group: 1 | Model: performance | Reason: throttle/widen/step-back logic with edge cases, not mechanical wiring.
Depends on: none | Blocks: BBX-1-05, BBX-1-07, BBX-1-09

## Goal
Add a pure-Go budget state machine that decides, each cycle, whether to skip optional counter groups and whether to widen the sampling interval, and steps back after calm cycles. No Windows API, no I/O — fully fake-testable.

## Context
- New file `internal/blackbox/budget.go`, test `internal/blackbox/budget_test.go`. Package `blackbox`.
- The recorder samples every `Config.IntervalSeconds` (default 15, allowed 5/15/30/60 — `config.go:178-182`). This machine only decides the effective interval and which groups to skip; the recorder integration is a later packet (BBX-1-07).
- Rules that apply (handoff §3): **Light** — under 0.5% of one core on average, under 50 MB working set, each counter group within 1 s (else skipped and marked "skipped: slow"). When over budget: skip optional groups first, then widen the interval 15→30→60 s, and step back after 10 calm cycles.
- Constants to define: `budgetMaxGroup = time.Second`, `budgetMaxCPUPct = 0.5` (percent of ONE core), `budgetMaxWS = 50 << 20`, `budgetCalmCycles = 10`.

## Do
1. Create `budget.go` with:
   - `type Budget struct` holding: base interval int, effective interval int, calm counter int, overBudget bool, slow groups set, optional-skipped bool, and a mutex if you expose concurrent reads (keep it single-goroutine; no mutex needed if documented as tick-loop-only).
   - `func NewBudget(baseInterval int) *Budget` — effective starts at base; clamp base to {15,30,60} for widening purposes (if base is 5, treat widen ladder as 15/30/60 but never go below base).
   - `func (b *Budget) GroupTiming(name string, d time.Duration)` — if `d > budgetMaxGroup`, record the group as slow (it must be skipped next cycle and reported via `SlowGroups()`).
   - `func (b *Budget) SelfUsage(cpuPctOfOneCore float64, workingSetBytes uint64)` — set overBudget when `cpuPctOfOneCore > budgetMaxCPUPct` OR `workingSetBytes > budgetMaxWS`.
   - `func (b *Budget) Cycle()` — called once per tick: if overBudget OR any slow group: first set skipOptional=true; if already skipping optional (or a mandatory group is slow) widen effective interval one step (15→30→60, capped 60); reset calm to 0. Else: calm++; when calm >= budgetCalmCycles, step back one interval step (60→30→15, never below base) and re-enable optional groups, reset calm.
   - `func (b *Budget) EffectiveInterval() int`
   - `func (b *Budget) SkipOptional() bool`
   - `func (b *Budget) SlowGroups() []string` — sorted, names of groups currently marked slow (cleared when a group reports a fast timing).
   - `func (b *Budget) GroupSkipped(name string, optional bool) bool` — true if optional && SkipOptional(), or name is in slow set.
2. Keep it allocation-light: reuse the slow set map, no per-cycle slices beyond `SlowGroups()`.

## Do not
- Do not call any Windows API or read the clock here; durations and usage are passed in.
- Do not widen below the configured base interval or above 60.
- Do not capture text; group names are fixed identifiers.
- Do not add a goroutine or ticker.

## Tests to write first (fake inputs, table-driven)
- `TestBudgetStartsAtBase` — effective == base, no skips.
- `TestBudgetSlowGroupSkipsAndMarks` — GroupTiming("http", 1500ms) → SlowGroups contains "http", GroupSkipped("http", false) true.
- `TestBudgetOverCPUSkipsOptionalFirst` — SelfUsage(0.9, 10MB) + Cycle → SkipOptional true, interval still base on first over cycle.
- `TestBudgetWidensThenCaps` — repeated over cycles widen 15→30→60 and stop at 60.
- `TestBudgetStepsBackAfterTenCalm` — after widening, 10 calm Cycles step back one level and re-enable optional; calm counter resets.
- `TestBudgetNeverBelowBase` — base 30 never steps back to 15.
- `TestBudgetWSOver` — SelfUsage(0.1, 60MB) triggers over.
- `TestBudgetFastGroupClearsSlow` — slow then fast timing removes the name.

## Verification checklist
- [ ] go build ./... passes
- [ ] go vet ./... passes
- [ ] go test ./internal/blackbox/... -count=1 passes, tests above included
- [ ] Both build-tag variants compile (GOOS=windows and GOOS=linux go vet ./internal/blackbox/...)
- [ ] Footprint: no new goroutine or ticker; Cycle() allocates nothing beyond the slow-set bookkeeping
- [ ] Missing reading is omitted, never 0 (n/a here — no readings; state only)
- [ ] No text captured (only fixed group-name identifiers)
- [ ] Widen ladder is exactly 15→30→60 and step-back needs 10 calm cycles (tests named above)

## Live-check needed?
No for the logic. The 0.5%/50 MB/1 s thresholds are verified on real hardware in BBX-1-09 (benchmark) and by the owner on a real server.

## Report back
Files changed, each checklist item ticked or failed with output, anything unconfirmed. Write the full report to `.forge/blackbox-expansion/reports/BBX-1-01.md`.