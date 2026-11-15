# Wave 1 fix report — W1-01, W1-02, W1-03, W1-04

Date: 2026-11-14. Fixes for [wave-1-adversarial-review.md](wave-1-adversarial-review.md),
applied on `feat/blackbox-expansion`. One wave at a time, per the owner's instruction.

## W1-01 — valid NEW_DATA readings were rejected (High)

**Change.** One shared predicate now decides whether a PDH per-counter status is usable
data, and both boundaries use it:

- `internal/blackbox/engine.go`: `pdhStatusValidData` (0x00000000), `pdhStatusNewData`
  (0x00000001) and `CounterStatusOK(status)`. `Engine.Process` now tests
  `!r.OK || !CounterStatusOK(r.Status)` instead of `r.Status != 0`.
- `internal/blackbox/pdh_windows.go`: `readingFrom` sets `OK: CounterStatusOK(v.CStatus)`.
  An API failure still wins over a success-looking CStatus.
- `CounterReading.Status`/`OK` comments corrected: they claimed "valid only when 0",
  which was the defect written down as a rule.

**Tests.** `TestCounterStatusOK` (7 statuses incl. three real failure codes),
`TestEngineNewDataIsUsable` (gauge + KindRate + KindPDHRate all driven with Status 1),
`TestReadingFromNewDataIsOK` (native boundary, plus API-failure precedence).
`TestEngineInvalidStatusOmitted` previously used Status 1 as its "invalid" example —
that was the bug encoded in a test. It now uses `0x800007D4`
(PDH_CSTATUS_INVALID_DATA); its baseline-gating assertions are unchanged.

**Still live-only.** That a real provider on the owner's servers returns NEW_DATA for the
counters we read, and that the resulting values match PerfMon. No Windows counter was
sampled here.

## W1-02 — refresh destroyed every rate baseline (High)

**Change.** `Engine.Reconcile(specs)` adopts a new spec set while keeping the history of
counters that did not change. History is dropped only when a path is gone, its kind
changed, or it is marked `Volatile`.

`CounterSpec.Volatile` is the identity contract: PDH names Process instances `w3wp`,
`w3wp#1` by enumeration order, so the same string can mean a different process after a
refresh. Those paths must never carry a baseline. Machine-wide paths — a disk, a
processor, an app pool's queue, a SQL instance, a network interface — are their own
identity and keep theirs.

- `clr.go`, `proc10.go`, `procio.go`: their specs are now `Volatile: true`.
- `diskcpu.go`, `http.go`, `tcp.go`: refresh calls `Reconcile` instead of `NewEngine`.
- `reconcileEngine(e, specs, cores)` handles the first build (no engine yet) and later
  refreshes through one path. This was needed: the group rebuild functions run at
  construction too, and calling `Reconcile` on a nil engine panicked — caught by the
  existing CLR tests on the first run, not by inspection.
- `pdh_windows.go` `RefreshInstances` is now incremental: every pattern is expanded
  **before** the query is touched, an instance that is still present keeps its handle
  (and its PDH baseline), a departed one is removed, a new one is added, and the priming
  collect runs only when something actually changed. A failed expansion no longer strips
  the source down to its direct counters.
- `sqlhost.go`: the instance set is remembered (`prefixes`) and the query is rebuilt only
  when that set changes. An unrelated process arrival no longer re-primes every SQL rate.
  A genuine instance change still rebuilds source **and** engine, because new handles
  mean new PDH baselines.

**Tests.** `TestEngineReconcileKeepsStableBaselines` (20 consecutive refreshes, rate must
stay 10 every cycle), `TestEngineReconcileDropsUnsafeHistory` (volatile reset, removed
path dropped, kind change re-primes, no leaked history),
`TestDiskCPURatesSurviveProcessChurn` (the real group, churn on all 22 ticks, DPCPct
present and correct after warm-up, refresh still running).

**Partially closes later-wave findings.** W4-04's machine-wide half (HTTP queues,
disk/CPU, SQL instances, TCP) is closed here. Its per-process half is **not**: CLR,
proc10 and procIO still re-prime on churn, deliberately, because their instance names are
not identities. Closing that needs the PID+creation-time work in waves 3–5 (W3-04,
W5-01, W5-04). Marking it done now would trade a starvation bug for a cross-generation
attribution bug.

**Still live-only.** Real churn frequency, and that PDH keeps a retained handle's baseline
across a refresh on the owner's Windows build.

## W1-03 — case-alias targets passed duplicate validation (Low)

**Change.** `config.go` `Validate` now compares targets the way the Windows resolver does:
same kind plus `strings.EqualFold` on the name. At most 32 entries, so the pairwise scan
is trivial. `Target` remains the display spelling.

**Test.** `TestConfigRejectsCaseAliasTargets` — service alias, pool alias, exact duplicate
refused; same name under a different kind and genuinely distinct names accepted.

## W1-04 — review provenance and a contradictory ledger (Medium)

**Change.** `STATE.md`:

- The stale resume line and the wave 10/11/12/13 rows now match the log: everything
  dispatchable is committed and delivered as PRs #3–#9, none merged. Wave 12/13 rows
  record that BBX-5-16/6-02 ran early in wave 9 and BBX-5-17 in wave 11.
- New "Review provenance" section separates **required** tier, **requested** model
  (`Qwen4-max-preview`, at the owner's request) and **observed** model — which is
  *unverified*, because this session's Agent tool exposed no per-dispatch model parameter.
  It states explicitly that unknown provenance is not proof a lower tier ran, and that no
  historical "U: clean" row is evidence an Ultimate review executed.
- New "Adversarial fix program" section records the 13 reviews, the 84 finding entries and
  the per-wave fix status.

**Not changed.** No historical verdict was rewritten to look compliant, and the
sampler-before-gate ordering breach stays recorded (it is W10-13, fixed in wave 10's turn).

## Verification

```
go vet ./internal/blackbox/                     clean
GOOS=linux go vet ./internal/blackbox/          clean
go test ./internal/blackbox/ -count=1           ok 59.97s   (full package, no regressions)
new tests: 9 targeted, all PASS
go build ./... && go vet ./...                  clean
go test -p 2 -timeout 25m ./...                 internal/blackbox ok; 2 internal/server flakes
```

Two runs needed interpreting honestly:

1. The first full-package run **failed** with a nil-engine panic in `clrGroup.rebuild`.
   That is a real regression this fix introduced and `reconcileEngine` closes it. Caught by
   the existing CLR tests, not by inspection.
2. `go test ./...` at default parallelism then reported seven `internal/blackbox`
   host/clumsy failures ("server never listened"). Under CI's own `-p 2`
   ([ci.yml:82](../../../.github/workflows/ci.yml)) the whole blackbox package passes, so
   those were load-induced, not a regression. The first run's reported "exit 0" was the
   trailing `echo`'s status, not `go test`'s — the failures were only visible in the output.
3. Under `-p 2`, `internal/server` still failed `TestOwnKeyArchiveRefusesATypedPassphrase`
   and `TestAsyncImportCommitsInTheBackground`. Both **pass in isolation** on this tree
   (`ok 2.605s`), both are outside the changed package, and the second is already recorded
   in STATE.md as a pre-existing flake on a pristine tree. Classified as load-sensitive
   flakes, not closed by this wave and not claimed as fixed.

## Owner decisions still open (not silently taken)

- Per-process rate continuity needs the identity work in waves 3–5; until then those
  groups re-prime on churn by design.
- Nothing in this wave was verified against a live Windows counter, a real app pool or a
  real SQL instance. `docs/BACKLOG.md` stays unticked.
