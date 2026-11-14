# Wave 3 resolver fix report — runtime detection, generation, identity, one snapshot

Date: 2026-11-15. Fixes W3-02, W3-03, W3-04 and W3-05 from
[wave-3-adversarial-review.md](wave-3-adversarial-review.md). Scope is the resolver and
its host/group wiring only: `resolve.go`, `resolve_map.go`, `resolve_proc_windows.go`,
`host.go`, `groups.go` and their tests, plus the new `resolve_generation_test.go`.
The wave-2 `workerSlot` deadline/busy/slow/Close behaviour, the resolver `mu` locking
discipline, the atomic change signal and the Engine `Reconcile`/`Volatile` design are all
preserved. W3-01 (apply persistence), W3-06 (picker) and W3-07 (footprint gate) are other
packets and are not touched here.

## W3-02 — an early "no runtime yet" observation was permanent (High)

**The flaw.** A successful module snapshot with no CLR module classified runtime 0 and
cached it for that `(PID, Start)` forever. A process that loaded its runtime later was
never re-detected; a denied read was only retried on the next process-set change.

**Change.** The cache value is now a `runtimeState{runtime, known, next}` instead of a bare
`uint8` (`resolve.go`):

- `known` is true only when a successful read **confirmed** a runtime (1 or 2). A
  successful read with no CLR module yet, or a denied read, is **pending** (`known=false`,
  runtime 0) — "not resolved yet", never a permanent "no runtime".
- A pending identity is retried on a bounded schedule, `runtimeRetryEvery = 15 s`, tracked
  per identity by `next`, **independent of process-set change**. A confirmed runtime is
  reused without a read for the life of its identity.
- Retired identities are pruned from the cache on every snapshot (`pruneCache`), so a
  departed identity has a bounded cache lifetime even if the set never changes again.
- The no-per-tick-full-module-scan constraint holds: the per-tick target path classifies
  only the **selected** identities and their children (see W3-05); a pending retry fires
  at most once per interval per needed identity, never for every process every tick.
- A clock seam `nowFn` (default `time.Now`) makes the retry schedule testable.

`Inventory` (the full-inventory API) keeps classifying every process; the bounded path is
the per-tick target resolution used by the host gate and `ResolveTargets`.

## W3-03 — a skipped group lost its refresh notification (High)

**The flaw.** `processChangeGate` overwrote a per-cycle `atomic.Bool` from
`Resolver.ProcessSetChanged` even when budget-skipped groups never consumed it, so a change
during a skip was forgotten before the group resumed.

**Change — the generation contract.**

- The resolver publishes a **monotonic inventory generation** `gen uint64` (`Generation()`
  accessor). It advances when the process set changes **or** the resolved target set
  changes (a new/departed identity, or a runtime that became known — so a late-loading
  runtime makes a target usable and re-expands the group even on a stable process set).
  It never decreases. A snapshot error leaves it untouched.
- The gate stores one immutable `tickView{gen, targets, missing}` per cycle (W3-05); the
  generation travels with the shared result.
- `GroupDeps` gains `Generation func() uint64` **alongside** the unchanged
  `ProcessSetChanged func() bool`. When `Generation` is set, `BuildGroups` gives each group
  its own dirty/ack state via a `changeTracker` and wraps it in a `trackedGroup`:
  - `ProcessSetChanged` for a group becomes `current() > acked[group]` — dirty until that
    group reconciles the newest generation.
  - `trackedGroup.Collect` captures the generation **before** calling the group and acks
    **only when Collect returns nil error**. A failed refresh, a panic, a budget skip or a
    busy slot never acks, so the change stays pending and is delivered on resume without a
    second process change. A late worker acks the generation it actually saw, never a
    newer one it missed; `ack` never moves backwards.
  - A caller that supplies only `ProcessSetChanged` (no `Generation`) keeps the old shared
    edge-triggered behaviour, so current callers are unaffected.
- The `workerSlot` machinery is untouched: `trackedGroup` delegates `Name`/`Optional`/
  `Close`, so the deadline, the one-outstanding-worker rule, `<name>: busy`/`<name>: slow`
  and the skip-a-busy-source `Close` guard all still apply.

The per-group ack is what makes "retain dirty through skips and failed refreshes; ack only
on successful reconciliation" true for **every** group independently, rather than one
shared boolean.

## W3-04 — creation-time precision was insufficient for identity (Medium)

**The flaw.** `processStart` truncated FILETIME to whole seconds, so two same-PID lifetimes
created within one second aliased to one cache key and one process-set identity.

**Change — the identity field decision.**

- `ProcInfo` gains **`StartTicks int64`**, the raw FILETIME creation ticks (100 ns since
  1601). **`StartTicks` is the lossless identity.** `Start` keeps its exact meaning — unix
  **seconds** — for display, elapsed-time math and the existing wire/consumer shape; it is
  documented as *not* the identity. `ResolvedTarget.Start` likewise stays unix seconds, so
  the per-group PID joins in clr/proc10/procio (another wave's work) are unchanged.
- `procKey` now holds `identityTicks(p)`: `StartTicks` when present, else `Start` scaled to
  ticks (a seconds-only fake keeps second-granularity identity). `0` still means unreadable
  creation time → never cached. Cache keys, `processSetChanged`, the generation baseline and
  the pool/service identity confirmation all use the lossless key.
- `resolve_proc_windows.go` reads the creation time **once** and returns both unix seconds
  and raw ticks from the same query-only open (`processStart(pid) (int64, int64)`); no extra
  handle, no precision lost. `Start` is computed exactly as before
  (`creation.Nanoseconds()/1e9`), so its value is unchanged.

## W3-05 — the Targets callback re-inventoried the host per consumer (Medium)

**The flaw.** `Targets` called `ResolveTargets` → `Inventory` → a full process snapshot on
every invocation; one tick could take nine resolver snapshots (host + clr×2 + proc10×2 +
procio×2 + iis), and independent snapshots could describe different process generations
within one Sample. Fresh module classification also ran for every uncached process.

**Change.**

- The gate resolves **one immutable `tickView` per collection cycle** via
  `Resolver.resolveTick(targets, now)`: one process snapshot, bounded classification, the
  shared generation. It publishes the view with an `atomic.Pointer[tickView]` (a worker from
  an earlier tick may read it while the tick goroutine stores the next).
- `deps.Targets` and the host collector's selected-target func both return
  `gate.currentView().targets`; every group reads the same immutable slice. No consumer
  re-inventories the host. `ResolveTargets` keeps its public behaviour for other callers
  (one snapshot per call) but now shares the same bounded resolution body.
- Fresh module classification is bounded to what the selected targets need:
  `selectedPIDs` = the live PIDs behind selected pool instances and service names **plus
  their direct children** (out-of-process .NET 10). Everything else reuses a confirmed cache
  or stays unknown without a read. Instance/service enumeration still runs only on a
  process-set change, with the independent last-good caches unchanged.

## Tests

New `internal/blackbox/resolve_generation_test.go` (LF), all on the injected
snapshot/module/enumerator seams — no Windows, no live process/service operations:

- `TestSameSecondPIDReuseInvalidatesCache` (W3-04): same PID, same unix second, different
  FILETIME ticks → distinct identity, cache re-read, runtime refresh, set change, generation
  advance; `Start` stays equal unix seconds.
- `TestPendingRuntimeRecoversOnBoundedRetry` (W3-02): stable identity, empty→Framework and
  empty→CoreCLR recover within the bounded window; no re-read before the retry is due; a
  confirmed runtime is not re-read.
- `TestDeniedRuntimeRecoversOnBoundedRetry` (W3-02): denied→readable recovers on the bounded
  retry without a process-set change.
- `TestOneResolverSnapshotPerTick` (W3-05): composed gate + host + four two-read groups take
  exactly **one** resolver snapshot per tick regardless of consumer count, and every consumer
  sees the shared target.
- `TestGenerationDeliveredAcrossSkips` (W3-03): **20** skip/change/stable/resume cycles
  deliver the newest generation to every group on resume without a second change.
- `TestFailedRefreshRetainsGeneration` (W3-03): a failed refresh keeps the generation pending
  and a later stable cycle delivers it.
- `TestChangeTrackerAckOnlyOnSuccess` (W3-03): dirty/ack semantics, ack never backwards.

`host_test.go`'s `TestProcessChangeGateOneVerdictPerCycle` was **updated**: it previously
asserted the old shared `atomic.Bool` verdict (`gate.changed`). It now asserts the shared
immutable **generation** — one snapshot per cycle, all consumers read the same generation, a
change advances it, a stable set does not. This is the pre-existing assertion that encoded
the old behaviour.

### Commands and results

Run with Go at `C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/.dev/toolchains/go/bin`
(go1.27.3 windows/amd64) on branch `feat/blackbox-expansion`. Other workers were editing
unrelated files concurrently; the package briefly failed to compile from their in-progress
`events.go`/`events_test.go`/`collector_test.go` edits, which cleared. Final clean results:

```
go build ./...                                   -> exit 0
go vet ./internal/blackbox/                      -> exit 0
GOOS=linux go vet ./internal/blackbox/           -> exit 0
go test ./internal/blackbox/ -count=1 -timeout 300s
                                                 -> ok  api-audit-explorer/internal/blackbox  63.979s  (exit 0)
```

The whole `internal/blackbox` package is green, including the new tests:

```
--- PASS: TestProcessChangeGateOneVerdictPerCycle      (updated gate test)
--- PASS: TestSameSecondPIDReuseInvalidatesCache       (W3-04)
--- PASS: TestPendingRuntimeRecoversOnBoundedRetry     (W3-02, empty->Framework / empty->CoreCLR)
--- PASS: TestDeniedRuntimeRecoversOnBoundedRetry      (W3-02, denied->readable)
--- PASS: TestOneResolverSnapshotPerTick               (W3-05)
--- PASS: TestGenerationDeliveredAcrossSkips           (W3-03, 20 cycles)
--- PASS: TestFailedRefreshRetainsGeneration           (W3-03)
--- PASS: TestChangeTrackerAckOnlyOnSuccess            (W3-03)
```

`gofmt`: every changed file is structurally gofmt-clean (verified on LF copies). Pre-existing
files keep CRLF (the repo convention — `gofmt -l` flags CRLF repo-wide, including untouched
files such as `clr.go`); the new `resolve_generation_test.go` is LF.

Note: an early full run during the concurrent edits showed a transient
`TestClumsyWrongReadKeyIsRefusedEveryWay` "server never listened" failure; it passes in the
clean run. As part of W3-05 the gate no longer runs the PDH/SCM enumerators when nothing is
selected, which also removes that startup work before the status server listens.

No git commit/push was made. No live process/service operation, counter query or upload was
performed by this work; all tests use the injected seams.

## Still needs a real server

These remain **Unverified Assumptions** until measured on the deployment with authorization;
nothing here was run against a live process, service or counter:

- **PID-reuse frequency.** The same-second aliasing collision is fixed in the key function,
  but how often Windows actually reuses a PID within one second on the target host is
  unmeasured. The lossless identity removes the defect regardless of frequency.
- **Runtime load timing.** The 15 s pending-retry window is a bounded default; the real
  delay between a selected process starting and its CLR/CoreCLR module appearing (and the
  denied→readable transition under the service account) needs live confirmation that the
  window is short enough to catch initialization and long enough to stay light.
- **Live footprint.** One snapshot per tick and selected-only module reads reduce the work,
  but actual CPU, working set, handles and per-group duration under representative load
  (the W3-07 gate) are not established by these fake-seam tests.
- **Module-read identity at the boundary.** The review also asks that a queried PID still
  represent the expected creation identity before accepting its classification. The resolver
  keys on lossless identity and prunes retired keys each snapshot; a live check that a
  recycled PID cannot be mis-classified mid-read is still warranted.