# BBX-1-07 Wire the budget into the tick loop + below-normal priority
Backlog: BL-122 | Group: 1 | Model: performance | Reason: changes the live tick loop and process priority; footprint-critical, must not stall housekeeping.
Depends on: BBX-1-01, BBX-1-05, BBX-1-06 | Blocks: BBX-1-09, group 2+

## Goal
Make the recorder run at the budget's effective interval, measure the Blackbox's own CPU/working-set each tick to feed the budget, build the registered counter groups under the budget, and set below-normal process priority. One concern (the budgeted loop); touches selfstat (new), host.go and recorder.go.

## Context
- New files `internal/blackbox/selfstat_windows.go` + `internal/blackbox/selfstat_other.go`. Edits: `internal/blackbox/host.go` (`RunHost` 51-107), `internal/blackbox/recorder.go` (`RecorderOptions` 33-44, `Run` 104-120, `Tick` 135-192).
- Existing: `RecorderOptions` has Config/Store/Collector/Disk/RunID/Now/Mono/Ticks. `Run` uses `o.Ticks` or a `time.NewTicker(cfg.IntervalSeconds)` (107-109). `Tick` calls `Collector.Collect` inline (149). `RunHost` builds `OSCollector(cfg.Record.Processes)` (77-85) then NewRecorder.
- `Budget` (budget.go): `NewBudget(base)`, `SelfUsage(cpuPct, ws)`, `Cycle()`, `EffectiveInterval()`. `NewGroupCollector(inner, groups, budget)` (groups.go). `BuildGroups(GroupDeps)` + `RegisterGroup` (groups.go). `NewPDHSource(paths)` (pdh_windows.go).
- Rules that apply: **under 0.5% of one core, under 50 MB, below-normal priority, self-throttle**; **never pause/restart/attach a target** (self-measurement reads ONLY the Blackbox's own process); **housekeeping must keep running** (do not block the tick).

## Do
1. `selfstat_windows.go`:
   - `func SelfUsage() (cpuPctOfOneCore float64, wsBytes uint64, ok bool)` — OpenProcess(GetCurrentProcess(), QUERY_LIMITED_INFORMATION) or use the pseudo-handle; GetProcessTimes → kernel+user delta since last call / wall delta / cores * 100 (percent of ONE core: divide by cores so 1 full core = 100). GetProcessMemoryInfo → WorkingSetSize. Keep last times in a package var (tick-loop only). Return ok=false on any failure (never 0-as-fact).
   - `func SetBelowNormalPriority() error` — `windows.SetPriorityClass(windows.CurrentProcess(), windows.BELOW_NORMAL_PRIORITY_CLASS)`.
   - `func CoreCount() int` — runtime.NumCPU (>=1).
2. `selfstat_other.go` (!windows): SelfUsage returns (0,0,false); SetBelowNormalPriority returns nil; CoreCount returns runtime.NumCPU.
3. `recorder.go`:
   - Add to `RecorderOptions`: `Budget *Budget` and `SelfUsage func() (float64, uint64, bool)` (both optional/nil-safe).
   - In `Tick`, before Collect: if `r.o.Budget != nil` and SelfUsage != nil → call it; if ok → `Budget.SelfUsage(cpu, ws)`; then `Budget.Cycle()`.
   - In `Run`, when using the own ticker (not o.Ticks): after each tick, `t.Reset(time.Duration(budget.EffectiveInterval()) * time.Second)` if Budget != nil. Keep the o.Ticks test path unchanged.
4. `host.go` `RunHost`:
   - Call `SetBelowNormalPriority()` once (log nothing on failure; best-effort).
   - Build `budget := NewBudget(cfg.IntervalSeconds)`.
   - Build counter groups: `src, _ := NewPDHSource(nil)` is NOT correct here — groups construct their own PDH sources from their paths via GroupDeps. Instead: `groups := BuildGroups(GroupDeps{Engine: nil, Targets: resolverTargetsFunc, Config: cfg, Cores: CoreCount()})` (each group creates its own source/engine internally in later packets; for now BuildGroups returns the registered groups). Wrap: `collector := NewGroupCollector(OSCollector(cfg.Record.Processes), groups, budget)`.
   - Pass `Budget: budget` and `SelfUsage: SelfUsage` into RecorderOptions.
   - NOTE: the resolver targets func is wired in BBX-1-11; until then pass `Targets: nil` (machine-wide groups still work). Keep this packet building even with no groups registered yet.

## Do not
- Do not measure any OTHER process here — self only (no attach/dump).
- Do not block the tick on self-measurement failures; ok=false just skips the budget update.
- Do not remove or pause housekeeping/logs cadence.
- Do not change the o.Ticks test injection path.
- Do not raise priority; below-normal only.

## Tests to write first (fakes, no real counters)
- `recorder_test.go` additions: `TestTickFeedsBudget` — fake SelfUsage returns over-budget values; after Tick, budget.EffectiveInterval widened (use a real Budget). `TestTickBudgetNilSafe` — nil Budget/SelfUsage → Tick unchanged.
- `TestRunResetsInterval` — using a fake tick channel is unchanged; assert EffectiveInterval consulted (factor a helper `effectiveInterval(budget, base)` and table-test it).
- selfstat: cross-platform vet covers the twin; windows live self-read is a live check (skip in CI).

## Verification checklist
- [ ] go build ./... passes
- [ ] go vet ./... passes
- [ ] go test ./internal/blackbox/... ./internal/server/... -count=1 passes
- [ ] Both build-tag variants compile (GOOS=windows and GOOS=linux go vet ./internal/blackbox/...)
- [ ] Footprint: no new goroutine/ticker beyond the existing one; ticker Reset only; self-measurement is O(1)
- [ ] Missing reading is omitted, never 0 (SelfUsage ok=false → no budget update, not 0)
- [ ] No text captured
- [ ] Below-normal priority set once (SetBelowNormalPriority)
- [ ] Housekeeping and logs cadence unchanged (existing recorder tests pass)
- [ ] Self-measurement reads only the Blackbox process (code review)

## Live-check needed?
YES — owner confirms on a real server: Task Manager shows the Blackbox process at Below Normal priority; over 10 minutes its CPU averages < 0.5% of one core and working set < 50 MB (BL-122). Fake tests verify the wiring only.

## Report back
Files changed, each checklist item ticked or failed with output, anything unconfirmed. Full report to `.forge/blackbox-expansion/reports/BBX-1-07.md`.