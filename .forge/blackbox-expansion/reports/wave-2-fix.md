# Wave 2 fix report — group deadline, lifecycle, selection boundary

Date: 2026-11-14. Companion to [wave-2-pdh-fix.md](wave-2-pdh-fix.md) (W2-02, W2-03,
native half of W2-04) and [wave-2-discovery-fix.md](wave-2-discovery-fix.md) (W2-06,
W2-07). This file covers W2-01, W2-05 and the selection half of W2-04.

## W2-01 — the group budget was retrospective, not a deadline (High)

**The flaw.** `runGroup` called `g.Collect` synchronously and only then charged the
elapsed time, so a stalled provider held the tick, the housekeeping pass and service
shutdown indefinitely. The host collector sat outside every per-group timer.

**Change.** `internal/blackbox/groups.go` now gives each owner — the host collector and
every group — a `workerSlot` with an enforceable `budgetMaxGroup` deadline:

- At most **one** outstanding goroutine per owner. A tick that finds its slot still busy
  records `<name>: busy` and moves on; it never starts a second worker and never re-marks
  the group slow (re-marking every cycle is the livelock this budget already had once).
- A result that arrives after its deadline is **discarded**, never applied to a later
  sample, so a late reading cannot be attributed to the wrong tick.
- A panic inside a worker becomes the fixed `errGroupPanic`, not a crashed process.
- The host read is bounded too: `errHostSlow` on timeout, `errHostBusy` while the previous
  read is still outstanding. Both map to the existing fixed `collect-failed` event code.
- `Close` waits at most `groupCloseGrace` (2 s) and then **skips** any source whose worker
  is still inside it. Closing a live native query under a running `PdhCollectQueryData`
  would be a use-after-free; the handle is left to the OS at process exit instead.

**Concurrency safety without a race detector.** No gcc is available here, so `-race`
cannot run. The design therefore avoids shared mutable state rather than relying on a
detector: the tick goroutine is the only receiver, the only caller of `Budget`/`Store`
and the only writer of `Sample`; a worker only calls its own group's `Collect` and sends
on its own buffered channel. Two pieces of genuinely shared state were made safe:

- `Resolver` gained a `sync.Mutex`; `Inventory`, `ProcessSetChanged` and `ResolveTargets`
  lock at the public boundary and share an unlocked `inventory` body (no re-entrant lock).
  A worker from an earlier tick can still be resolving targets while the tick goroutine
  asks for the process-set verdict.
- `processChangeGate.changed` became an `atomic.Bool` for the same reason.

**Bound, stated plainly.** A tick is now bounded by roughly `(1 + number of groups) × 1 s`
instead of unbounded. Housekeeping and shutdown are therefore delayed by at most that
bound, not by a hung provider. This is a real improvement but it is **not** immediate
shutdown; making it immediate would need process isolation, which is out of scope here.

**Tests.** `TestGroupDeadlineBoundsTheTick` (a stalled group costs one budget and the
healthy sibling is still collected), `TestBusyGroupDoesNotAccumulateWorkers` (100 stalled
ticks: no goroutine growth, exactly one entry into the provider, healthy group still
collected), `TestHostReadIsBounded`, `TestCloseSkipsBusySource` (bounded Close, busy source
not closed, idle sources closed).

**Still live-only.** Actual worst-case provider latency and Windows service stop timing.

## W2-05 — failed groups vanished; absence had no reason (High)

**Change.**

- `BuildGroups` no longer drops a failed factory for the life of the recorder. It keeps a
  `lazyGroup` retry slot with a bounded backoff (15 s doubling to a 5 min cap), so a
  provider that was unavailable at startup is picked up without a restart. While unbuilt
  it returns the fixed `errGroupUnavailable`, which lands in `Missing` under the group's
  own name. `lazyGroup.Close` releases the real group once it exists.
- A factory returning `errNoTargets` is an **intentional** absence: no slot, no source, no
  `Missing` entry. That is what separates "not selected" from "unavailable".
- A successful collect with no payload is now named in `Missing`, including a **typed nil**
  that `!= nil` misses (`isNilPayload`).
- The shared `"skipped: slow"` token became `<name>: slow`, so the group's identity is no
  longer lost. Four existing assertions were updated to the new, more informative shape;
  the dedup test now asserts both slow groups are named.

**Tests.** `TestUnavailableGroupRetriesAndRecovers` (backoff respected, recovery without
restart, factory not called again after success, Close releases the built group),
`TestGroupRetryDelayIsBounded`, `TestNilPayloadIsNamedMissing`, plus the updated
`TestRegistryBuildsAndNames` which now asserts the retry slot exists and reports
unavailable.

## W2-04 (selection half) — detail sources were built with nothing selected (High)

**Change.** `newCLRGroup`, `newProc10Group`, `newProcIOGroup` and `newIISGroup` return
`errNoTargets` before creating any source when `d.Targets == nil` and no source was
injected. Previously the per-tick short-circuit still expanded, added and primed every
Process instance before filtering the output, so an empty selection cost a full
machine-wide query.

**Test.** `TestNoTargetsCreatesNoDetailSource` covers all four factories.

The native allocation/handle caps are the PDH companion report's half of W2-04.

## Verification

```
go build ./...                                  clean
go vet ./internal/blackbox/                     clean
GOOS=linux go vet ./internal/blackbox/          clean
go test ./internal/blackbox/ -count=1           ok (full package, after assertion updates)
8 new deadline/lifecycle/selection tests        all PASS
```

The first run of the new tests failed three times for test-side reasons (a group name that
`applyGroup` does not map, the one-cycle slow mark preceding the busy state, and a retry
count off by one). Those were test bugs, not product bugs, and are fixed rather than
asserted away.

## Open items

- `-race` could not be run (no gcc). The ownership model above is the mitigation; the owner
  should run `-race` on a CGO host before release.
- Shutdown is bounded, not immediate. If the owner needs a hard stop bound, that is a
  process-isolation decision, not a timer tweak.
- Nothing here was verified against a live Windows provider, a real app pool or a real
  service stop. `docs/BACKLOG.md` stays unticked.
