# BBX-3-01 Per-process .NET CLR counters (Framework targets)
Backlog: BL-109 | Group: 3 | Model: performance | Reason: CLR instance→PID mapping + per-target counters; runtime-specific.
Depends on: BBX-1-05, BBX-1-06, BBX-1-11 | Blocks: none

## Goal
For SELECTED targets detected as .NET Framework (runtime 1), sample CLR memory/GC/threads/exceptions counters every 15 s via PDH `.NET CLR` objects.

## Context
- New file `internal/blackbox/clr.go` (no tag) + test `internal/blackbox/clr_test.go` (fakes). Group pattern as BBX-2-01 (init RegisterGroup("clr", true, ...); optional). Uses `d.Targets()`.
- CLR counter instances are process names with `#N` suffixes (e.g. `w3wp`, `w3wp#1`), NOT PIDs. Map instance→PID using the counter `\.NET CLR Memory(*)\Process ID` (each instance reports its PID). Then match to selected targets by PID (confirm creation time via the target).
- Researched paths (UNVERIFIED): `\.NET CLR Memory(<inst>)\Gen 0 heap size`, `Gen 1 heap size`, `Gen 2 heap size`, `Large Object Heap size`, `% Time in GC`, `Allocated Bytes/sec`; `\.NET CLR LocksAndThreads(<inst>)\Contention Rate/sec`, `# of current physical threads`; `\.NET CLR Exceptions(<inst>)\# of Exceps Thrown/sec`.
- `CLRGroup{Targets []CLRTarget}`; CLRTarget fields Gen0Bytes/Gen1Bytes/Gen2Bytes/LOHBytes (uint64), PctTimeInGC/AllocBytesPerSec/ContentionPerSec/Threads/ExceptionsPerSec (groups_types.go). Target label = the ResolvedTarget.Key.
- Rules: **owner-selected targets only**; **only runtime 1 targets** (Framework); **key (PID, creation time)**; **two baselines before a rate**; **multi-core not capped** (% Time in GC is per-process, can exceed 100 on multi-core GC); **missing never 0**; **no text**.

## Do
1. Factory builds PDH source: the `\.NET CLR Memory(*)\Process ID` mapping counter + the wildcard memory/locks/exceptions paths. Engine specs map fields; heap sizes gauge (uint64), % Time in GC gauge (no cap), Allocated Bytes/sec / Contention Rate/sec / Exceps Thrown/sec = KindPDHRate, threads gauge.
2. `Collect`: read Process ID per instance → instance→PID map; for each selected runtime-1 target, find its instance by PID; build a CLRTarget. A target with no CLR instance (not running / not Framework) → omit. Return *CLRGroup; nil if none.
3. Re-expand only on ProcessSetChanged().

## Do not
- Do not sample runtime-2 (.NET 10) targets here (that is BBX-3-02/3-04).
- Do not sample unselected processes.
- Do not assume instance name == PID (use the Process ID counter).
- Do not cap % Time in GC at 100; do not emit 0 for missing; no text; no goroutine/ticker.

## Tests to write first (fakes)
- `TestCLRInstancePIDMap` — fake Process ID readings map instances to PIDs; selected target matched by PID.
- `TestCLRFrameworkOnly` — a runtime-2 target is skipped.
- `TestCLRSelectedOnly` — unselected process not emitted.
- `TestCLRMissingOmitted` — invalid counter → nil field; target with no instance omitted.
- `TestCLRRateGated` — Allocated Bytes/sec absent until two baselines.
- `TestCLRHeapSizes` — Gen sizes map to uint64 fields.

## Verification checklist
- [ ] go build ./... passes
- [ ] go vet ./... passes
- [ ] go test ./internal/blackbox/... -count=1 passes, tests above included
- [ ] Both build-tag variants compile (GOOS=windows and GOOS=linux go vet ./internal/blackbox/...)
- [ ] Footprint: reused handle; re-expand on change; no goroutine/ticker
- [ ] Missing reading is omitted, never 0 (TestCLRMissingOmitted)
- [ ] No text captured (target keys + numbers)
- [ ] Owner-selected, Framework-only, (PID,creation) identity (tests above)
- [ ] Two baselines before a rate; multi-core not capped
- [ ] Budget gate BBX-1-09 still passes

## Live-check needed?
YES — `typeperf -q ".NET CLR Memory"` on a real Framework app pool; confirm instance naming and the Process ID counter; compare Gen 2 heap size to perfmon. Fake-tested only until then.

## Report back
Files changed, each checklist item ticked or failed with output, exact paths used, anything unconfirmed. Full report to `.forge/blackbox-expansion/reports/BBX-3-01.md`.