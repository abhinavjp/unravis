# Wave 10/11 data-plane fix report

Branch `feat/blackbox-expansion`. Scope: the SQL **data plane** only —
`internal/bbsql/{runner,output,backoff,requests,waits,schedulers,filestats}.go`
and their tests, `budget_test.go`, `runner_c13_test.go`, new `sqldata_*_test.go`,
and `internal/blackbox/recorder.go`. The SQL control plane (`conn.go`,
`ceiling.go`, `errors.go`, `allowlist.go`, `folder*.go`, `cmd/*`,
`internal/blackbox/{sqlservice*,server,...}`) is owned by another worker and was
**not** edited. No live SQL/service operation, no upload, no commit/push.

## Verification (exact commands, current tree)

Go: `C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/.dev/toolchains/go/bin/go.exe`
(go1.27.3 windows/amd64), `GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local`.

| Command | Result |
|---|---|
| `go build ./...` | exit 0 |
| `go vet ./internal/bbsql/ ./internal/blackbox/` | exit 0 |
| `GOOS=linux go vet ./internal/bbsql/ ./internal/blackbox/` | exit 0 |
| `go test ./internal/bbsql/ -count=1 -timeout 300s` | ok ~40 s |
| `go test ./internal/bbsql/ -run TestSchedulersSemaphoresTempdbRegistered -count=2` | ok (was FAIL on rep 2) |
| `go test ./internal/blackbox/ -count=1 -timeout 400s` | ok ~105 s |

`gofmt -l` clean on every edited file. Edited files kept their existing LF
endings; new `sqldata_*` files are LF. The race detector could **not** be run
(`-race` needs cgo; no `gcc` on this host) — isolation is proven by the
sequential/concurrent tests below, not by `-race`.

During the run the other worker's in-progress edits transiently broke the
package (`conn.go` `driver.Dialer`, `allowlist.go` C2-grammar `init()` panic,
`sqlservice_windows.go` `AclSize`, `conn_queryerr_test.go`). Per instructions I
waited and re-ran rather than editing their files; the final tree above is green.

## Per-finding changes

### W11-01 (High) — wide semaphore cumulative decode
`output.go`: `Semaphores.Forced/Timeouts` are now `*int32` (`omitempty`).
`schedulers.go`: new `semaphoresRaw` decodes the cumulative counters into
**nullable int64**; the difference is computed in the int64 domain and only then
range-checked into the emitted int32. A lifetime value above MaxInt32
(e.g. 2147483648 → 2147483649) now yields a delta of 1 instead of `errDecode`
that could auto-off the whole stream. Reset and oversized-delta still skip the
row (never wrap); a NULL/negative counter is unreadable (no baseline).
`TestSemaphoresCounterBeyondInt32` (expected the narrow-decode failure) was
**replaced** by `TestSemaphoresWideCumulativeDelta` (MaxInt32 crossing, reset,
NULL, MaxInt64). Input widened; emitted **width unchanged** (see contract note).

### W11-04 / W8-08 (Medium) — required columns, nullability, enum domains
`output.go`: per-type `rowSpec` (exact required columns + per-column nullability)
validated **before** conversion; new fixed errors `errMissingColumn`,
`errNullColumn`, `errEnumDomain`. A missing/null required column is refused,
never a factual zero. A **present** empty `wait_type` maps to RUNNING; a
missing/null one is refused (never RUNNING). AG `synchronization_state` is
enforced to the confirmed domain 0..4. Summed statements (schedulers/tempdb)
keep their schema-specific NULL handling: an empty/all-NULL row is the legitimate
empty-SUM (missing payload, no error), but a **partial** row is refused.
Semaphores: a NULL counter no longer blanket-skips the row — the readable
grantee/waiter gauges are emitted with a nil delta for the unreadable counter.
Table tests: `sqldata_validation_test.go` (delete/null each column per type,
real zeros, enum bounds).

### W11-05 (Medium) — sampler lifecycle / epoch contract
See the contract section below. `waits.go`, `filestats.go`, `schedulers.go`
implement `Observe(Observation)` and rebaseline on an epoch change;
`runner.go` advances the generation on every continuity break.
Tests: `sqldata_lifecycle_test.go` (direct epoch invalidation for all three
samplers; real-sampler runner tests for a query-failure gap and the hourly
reconnect — neither emits a delta spanning the gap).

### W11-07 (Low) — immutable factories, fresh state per runner
`runner.go`: registry now stores `samplerEntry{kind, new func() Sampler}`.
`RegisterSamplerFactory` registers an immutable factory; `Samplers()` allocates
a **fresh** instance per call. Production `init()` uses factories;
`RegisterSampler` remains as the shared-instance test seam. The reproduced
`TestSchedulersSemaphoresTempdbRegistered -count=2` now passes.
`sqldata_registry_test.go`: sequential runners have independent baselines;
8 concurrent goroutines each own their state (baseline-then-delta).

### W10-07 (Medium) — fixed-rate scheduling, missed slots, context
`runner.go`: `Run` uses monotonic scheduled deadlines (`time.Now`), counts
elapsed missed slots via `advanceSchedule` (never queues/overlaps), and threads
its context through `tick → reconnect → CheckCeiling → Query`. `Tick` is now a
wrapper over `tick(parent, now)`; one active tick, no concurrent callers of the
unsynchronized `running` flag. A cancelled parent stops the tick before sampling.
Tests: `sqldata_run_test.go` (`advanceSchedule` grid/miss arithmetic; cancelled
context sends nothing). **Seam:** `ConnectFunc`/`Open` take no context (conn.go
is the other worker's); I check `ctx.Err()` after connect, but the connect/ping
itself is not yet cancellable — see conflicts.

### W10-08 / W7-06 (Medium) — slow demotion
`runner.go`: `OnSlow` is called **once per tick** with the WORST used/limit ratio
among the samplers that ran, so a fast sibling cannot erase a slow strike.
`backoff.go`: `OnSuccess` applies the 60 s floor **only at** `SlowStrikeLimit`
(three), not for any nonzero strike; instance failure back-off stays separate.
Tests: `sqldata_run_test.go` (mixed slow-requests/fast-schedulers in real Kind
order demotes after exactly three ticks, no early demotion; floor-only-at-
threshold).

### W10-02 (High) — one combined disk ceiling
`recorder.go`: exported `SQLBudgetShare = 4`; `limits()` now gives the host the
**remaining** share (`MaxBytes − MaxBytes/4`), so host+SQL admitted bytes satisfy
one ceiling `Config.MaxBytes()` instead of 1.25×. The active-hour overshoot
allowance (each writer protects its current hour between passes) is documented
explicitly in the code. Test: `internal/blackbox/sqldata_budget_test.go`
(host = ¾, host+SQL ≤ ceiling for 1/2/4/8 GB). **Seam:** `cmd/.../sqlrunner.go`
still uses its own `sqlBudgetShare = 4`; it should reference
`blackbox.SQLBudgetShare` to prevent drift (values agree today).

### W10-03 (High) — housekeeping independent of admission
`runner.go`: `checkDisk` runs the bounded rotation **first** (even when known
low), then reads free space; on known-low it runs one more bounded reclaim pass
and re-reads, admitting only once the floor is proven. Unreadable volume is a
**distinct** fixed code `disk-unreadable` (vs `disk-low`). Auto-off ticks run
`housekeepOnly` (bounded cleanup, no queries/writes). Tests:
`sqldata_rotation_test.go` (low disk recovers by deleting eligible SQL history
alone with a size-derived fake free reading; auto-off still reclaims).

### W10-09 (Medium) — clock-safe retention
`runner.go`: `rotate` protects `SamplePath(now)` **by name** (not sort order);
future-dated files are ordinary non-current byte-cap candidates (removed last,
never shielding current history). Age purge is bounded (`maxAgeRemovalsPerPass`
= 6) and **deferred** on a wall/monotonic discontinuity (`clockJumped`, threshold
2 s, injectable `Mono`); byte cap bounded too. `due()` no longer suppresses
sampling on a backward clock. Tests: `sqldata_rotation_test.go` (current-hour
content survives a future file + tight cap; forward jump defers age purge then
resumes; bounded pass).

### W10-10 (Medium) — append integrity + filesystem error domain
`runner.go`: `appendSample` is now a bounded method with pre-write offset,
short-write/close/sync error handling, rollback (`truncateTo`, by name — the
Windows O_APPEND constraint, mirroring host `Store.rollbackLocked`) and one-time
torn-tail repair (`repairTornTail`, bounded 64 KiB window). Filesystem failures
return the fixed `write-failed` code, are counted in `fsWriteFailures`, and
**never** touch `State`, drop the healthy connection, or classify as a SQL
permission denial. `writeStatus` also returns the fixed code. Tests:
`sqldata_append_test.go` (torn-tail and invalid-last-line repair preserve every
complete line; a write fault leaves Failures=0, no auto-off, connection kept).

### W10-05 / W11-08 (High) — C13 measures the real workload
`budget_test.go` rewritten: the runner ticks the **actual production samplers**
(fresh factory instances, real baseline maps, real HADR gate) through a fake
Queryer that **materializes rows per query** (reader/scanning allocations) and
evolves counters, churns file keys, injects NULL/reset and toggles HADR. At-cap
fixtures use **maximum numeric width** (int64-max via the driver seam, uint32-max
ids, semaphore lifetime counters > MaxInt32) and **60-char wait names**. CPU,
allocation and working-set bounds are hard assertions; the per-sample byte bound
is measured **literally** and reported, not relaxed into a mean.

## Lifecycle / epoch contract (W11-05)

- `Observation{Time time.Time; Epoch uint64}` is handed to any sampler implementing
  the optional `ObservingSampler.Observe` immediately before `Decode`.
- `Runner.gen` (the source generation) advances on **every** continuity break:
  reconnect (new physical connection), `fail()` (query/decode/connect failure),
  `checkDisk` error (disk pause), auto-off, a partial tick (budget spent with due
  samplers remaining), and a detected clock jump.
- A stateful sampler stores `baselineEpoch`; `firstCycle = prev==nil ||
  baselineEpoch != obs.Epoch`. On a new epoch it rebaselines and emits **no
  delta**, so a delta never spans an unobserved interval or a reset that overtook
  the old value during a gap.
- Deltas are raw counter differences over the observed interval; they are
  **never divided by the nominal cadence**. Empty replies clear baselines; a
  decode error leaves baselines untouched and fails the tick (advancing `gen`).

## Measured C13 numbers (fake transport, real samplers, this host)

Realistic tier (40 ticks): mean **51.6 ms** process CPU/tick (cap 150 ms);
**268,397 B/tick**, 3,456 allocs/tick (cap 10 MiB); mean line 683 B, **max line
4,464 B** (filestats, 40 files), worst tick 7,859 B. Working set over 60 ticks:
19,517,440 → 16,994,304 B (**growth −2.5 MB**, cap +10 MiB). Goroutines 2 → 2.

At-cap, maximum width (4 ticks): mean **74.2 ms** CPU/tick; **max line 53,053 B**
(requests, 256 rows × 60-char wait + max numerics), worst tick 56,103 B; storage
plan **319,351,680 B/day (~319 MB/day)** at max width, 168 h retention.

**Per-sample byte bound (C13 "<2 KB per sample"): UNMET.** Realistic max 4,464 B
and at-cap max 53,053 B both exceed 2,048 B. The documented C6 row caps (e.g. 256
filestats/requests rows) make a full group several KB, so the bound is
mathematically unreachable at those caps. This is **reported, not silently
relaxed**: `TestSQLBudgetSampleByteBound` logs the literal max and flags the
conflict. It needs an explicit owner decision — enforce a destructive per-line
cap (drops evidence rows) or revise C13. No mean-based pass is claimed.

## Conflicts / seams that need the other owner (no edits made by me)

1. **C13 2 KB per sample** — owner decision required (above). Not relaxed.
2. **`cmd/unravis-blackbox/sqlrunner.go`** — `sqlBudgetShare` should reference the
   new `blackbox.SQLBudgetShare` (both = 4 today) so the host/SQL split cannot
   drift. (W10-02)
3. **`internal/bbsql/conn.go` + `sqlrunner.go`** — `ConnectFunc`/`Open` need a
   `context.Context` parameter so Run's cancellation reaches connect/ping (today
   ping is rooted in `context.Background`). I thread ctx through query/ceiling and
   check `ctx.Err()` after connect, but cannot make the dial/ping itself
   cancellable from my files. (W10-07)
4. **Emitted `Semaphores` contract** — `Forced/Timeouts` changed `int32` →
   `*int32` (`omitempty`) to express a missing delta beside readable gauges
   (W11-01/W11-04). Width unchanged; the W11 review found no live decoder of this
   payload. Flagged for C9 compatibility awareness.
5. **W11-02 / W11-03 / W11-06 (not in this fix scope)** require `allowlist.go`
   statement changes owned by the SQL control-plane worker: a numeric `pool_id`
   projection + composite semaphore key (W11-02), an AG local-row predicate /
   replica identity (W11-03). TOP n+1 truncation witnesses (W11-06) are already
   present in the current allowlist. I made no statement changes.

## Still needs a real server (Unverified Assumption until then)

- Actual DMV result metadata: semaphore bigint/nullability, AG sync-state domain
  and local/remote row shape, tempdb DMV context under the granted `master`
  default, scheduler SUM result types.
- Live query-plan/source-scan cost, C4 deadline and cancellation latency under
  representative session/file/pool counts (TOP + client deadline are not proof).
- Long-lived Windows-service C13 CPU/memory; real TDS/driver reader cost (the fake
  materializes rows but is not the driver).
- Windows filesystem fault behavior: full-disk short writes, close/sync errors,
  sharing/`O_APPEND` truncation. Rollback/repair mirror the host's proven pattern
  but were not fault-injected on a real volume.
- W11-02 pool identity and W11-03 AG locality (need live DMV validation + the
  statement changes above).
- Race-detector confirmation (no cgo/gcc on this host).