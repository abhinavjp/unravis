# Wave 3 footprint fix — W3-07 composition gate

Scope: TEST code only. Files changed:

- `internal/blackbox/budget_bench_test.go` — comment-only edit (CRLF preserved):
  the micro-benchmark is now labelled as a narrow-scope regression guard, NOT
  production proof. No logic changed.
- `internal/blackbox/budget_composition_test.go` — NEW (LF): the production
  composition footprint gate plus four adverse-case tests.

No production `.go` file, no other test file, no `cmd/*`, `internal/server/*`,
`web/*` was edited. No live service/counter operation, no upload.

## What the new gate actually exercises

`TestCompositionFootprintGate` drives the **real `Recorder.Tick`** against a
**real `Store`** in a temp dir, wired exactly as `RunHost` wires it:

- Real `BuildGroups` registry over one injected fake `CounterSource`
  (`Paths`/`RefreshInstances`/`Collect`) and a **fake resolver snapshot**. The
  resolver is the real `Resolver` with injected `snapshotFn`/`loadModulesFn`/
  `instancesFn`/`servicesFn`; it publishes ONE immutable per-tick view with a
  monotonic generation and lossless `StartTicks` identity.
- Real `processChangeGate` → `NewGroupCollector` → real `HostCollector` with
  `processes=true` (the host process list, including selected-target rows) over a
  fake `sampler.Source`.
- Real `Recorder.Tick` ordering: `feedBudget` (SelfUsage + Cycle) **before**
  `Collect` — the production order the micro rig gets wrong.
- Real `Store.Append` (NDJSON + manifest), real `Store.Housekeep` (every 60 s)
  and the real `CatalogueLogs` path (every 5 min) over fake `.log` files.
- Real `workerSlot` deadlines, `trackedGroup` generation acks, `Engine`
  `Reconcile`/`Volatile` baselines, and the per-process groups' (PID, Creation
  Time) identity join (`creationConfirmed`, W5-01/W5-04). The fixture supplies
  `\Process(*)\ID Process` + `\Process(*)\Creation Time` (FILETIME) for both
  selected PIDs so clr/iis/proc10/procIO resolve and emit rows.

Two registry entries are substituted, honestly and necessarily:
`newDiskCPUGroup` and `newEventsGroup` have **no `GroupDeps.Source` seam** and
would open a live PDH query / wevtapi handle (forbidden here). Their entries are
replaced with factories that build the SAME production structs (`diskCPUGroup`,
`eventsGroup`) over injected fakes, so their Collect/Reconcile/cursor logic runs
but the native-open call does not. Every other factory is the real production
factory. `tcpGroup.counts` (native TCP table) and `dumpsGroup.scan` (real dump
roots) are injected post-build; `sqlHost` discovery/source seams are faked.

## Assertions (documented budget numbers, NOT relaxed)

- Allocated **BYTES** per tick ≤ 256 KiB (test bound; measured ~80 KiB). Not just
  allocation count.
- **Retained** (post-GC) heap growth ≤ 1 MiB across the run (measured ~25 KiB).
- **Per-group duration**: the real collector marks any group over `budgetMaxGroup`
  (1 s) slow; the gate asserts `SlowGroups()` is empty.
- **Goroutines** do not grow across 220 measured ticks (2 → 2; bound +4).
- Average tick wall ≤ 75 ms (loose proxy for the 0.5%-of-one-core CPU budget).
- One sample written per tick through the real Store; every production group
  field present; the invalid reading (`Pool Nonpaged Bytes`) absent, never 0;
  selected targets resolve to `pool:PayrollPool` / `svc:EmailSvc`; log catalogue
  produced entries; exactly one resolver snapshot per tick (W3-05); resolver
  runtime cache pruned to the live set.
- **Windows only**, via the production `SelfUsage` twin: real process working set
  < `budgetMaxWS` (50 MiB) and CPU/tick < 75 ms (0.5% of one core at 15 s). Off
  Windows these two assertions are **skipped honestly** — no Go-heap substitute.

Adverse cases (all through the real recorder):
- `TestCompositionStalledSourceBounded` — a blocked mandatory provider costs one
  group budget, then `busy`; mandatory siblings keep running; one outstanding
  worker; goroutines flat; `Close` skips the source whose worker is still inside.
- `TestCompositionProcessChurnStableRates` — 60 ticks of process churn; non-volatile
  machine-wide rates (tcp/memPressure/diskCpu) survive (Reconcile), generation
  advances, cache pruned, goroutines flat, selected pool worker keeps resolving.
- `TestCompositionFailedFactoryRecovers` — a factory that fails is retried on the
  bounded backoff and recovers without a restart (real `lazyGroup` slot).
- `TestCompositionMissingSelfReadingNotSafety` — after an over-limit reading skips
  optional groups, a missing reading (ok=false) leaves the budget over budget;
  only fresh calm readings step back. A failed reading is not safety evidence.

## Measured numbers

Composition gate (220 measured ticks after 20 warmup, Windows, Intel Core Ultra 9
185H, fake sources, real Store/housekeeping/catalogue):

- avg wall ~21.5 ms/tick (max ~103 ms, a housekeeping/GC spike); ~392 allocs/tick;
  **~82,334 bytes/tick (~80 KiB)**.
- retained heap growth **~25,200 bytes**; goroutines **2 → 2**.
- Windows self-stat: working set **~18,341,888 bytes (~17.5 MiB)** < 50 MiB;
  CPU **~10.7 ms/tick** < 75 ms.

Micro-benchmark (`BenchmarkBudgetTick`, narrow scope, for contrast):
`222008 ns/op, 25081 B/op, 136 allocs/op`. The composition path allocates ~3x the
bytes of the substitute fixture — exactly the production cost the old gate hid.

## Verification (exact commands)

- `go vet ./internal/blackbox/` — **exit 0** (captured at a stable tree snapshot).
- `GOOS=linux go vet ./internal/blackbox/` — **exit 0**.
- `go test ./internal/blackbox/ -count=1 -timeout 400s` — **ok, 92.278s** (full
  package green WITH these changes).
- `go test ./internal/blackbox/ -run 'TestComposition' -count=1` — all 5 PASS.
- `gofmt`: new file clean (LF); bench content clean modulo CRLF (preserved,
  CR==LF==449, trailing CRLF newline added).

Concurrent-edit caveat: other workers were actively refactoring `clr.go`/`iis.go`/
`proc10.go`/`procio.go` (per-process Creation-Time identity) and adding a SQL
launch-grant feature (`sqlservice_windows.go`) DURING this work. The tree flipped
green/red repeatedly. The fixture was adapted to the new Creation-Time identity
contract; the full suite was verified green (92.3s) at a stable snapshot. At the
time of writing, the Windows build is intermittently broken by another worker's
unfinished `sqlservice_windows.go:179` (`old.AclSize` undefined) — outside this
task's editable scope and unrelated to these test files. Re-run the three commands
above once that lands.

## What this still cannot prove

- Live CPU, working set, handle count and per-group duration on a REAL server under
  representative load for ≥10 minutes. The Windows self-stat here measures the TEST
  process over a compressed run, not the installed service at a 15 s cadence.
- Real PDH wildcard cardinality / native query memory (the sources are fakes; the
  diskCpu/events native-open paths are not exercised).
- Real resolver process/module enumeration cost on a busy host (snapshot is fakes).
- Below-normal priority effect and self-throttle under sustained real load.
These remain **Unverified Assumptions** requiring owner-authorized live evidence.

## Thresholds refused to relax

The documented budget numbers were kept verbatim: `budgetMaxGroup` = 1 s per group,
`budgetMaxWS` = 50 MiB, `budgetMaxCPUPct` = 0.5% of one core (75 ms CPU per 15 s
tick). The bytes-per-tick (256 KiB) and retained-growth (1 MiB) limits are TEST
bounds set from the measured figures with headroom — they are not documented budget
numbers and were not used to mask any overrun. No bound was weakened to make a test
pass; the only fixture change was to satisfy the production identity contract.

## Findings for production owners (not fixed here — out of scope)

- `newDiskCPUGroup` ignores `GroupDeps.Source` and always opens a native PDH query;
  it has no injection seam, unlike every other group. Adding a `d.Source` seam would
  let the composition gate exercise the real factory without a registry shim.
- During the concurrent refactor, `clr.go:167` briefly called `g.targets()` with no
  nil guard (the old code had `g.targets == nil ||`), panicking on a nil-targets
  group. It was fixed by the owning worker before the green run; flagged so the
  nil-guard regression is not reintroduced.