# BBX-3-02 .NET 10 fallback: Process counters + memory info
Backlog: BL-110 (fallback path) | Group: 3 | Model: performance | Reason: per-target Process counters + PID mapping; the safe fallback that ships now while EventPipe is unproven.
Depends on: BBX-1-05, BBX-1-06, BBX-1-11 | Blocks: none

## Goal
For SELECTED targets detected as .NET 10 (runtime 2), sample CPU%, working set and private bytes every 15 s using Process counters plus GetProcessMemoryInfo — the fallback the backlog mandates when EventPipe is unavailable. This ships now; the EventPipe collector (BBX-3-04) is blocked on a live pipe proof.

## Context
- New file `internal/blackbox/proc10.go` (no tag) + test `internal/blackbox/proc10_test.go` (fakes). Group pattern as BBX-2-01 (init RegisterGroup("proc10", true, ...); optional). Uses `d.Targets()`.
- Process counter instances use `#N` suffixes; map instance→PID via `\Process(*)\ID Process`. Counters (UNVERIFIED): `\Process(<inst>)\% Processor Time`, `\Process(<inst>)\Working Set`, `\Process(<inst>)\Private Bytes`. (GetProcessMemoryInfo is the cross-check; PDH Process counters are sufficient and fake-testable. If you prefer Win32 for WS/Private, keep it behind the same interface and fake it.)
- `Proc10Group{Targets []Proc10Target}`; Proc10Target{Target, CPUPct, WorkingSet, PrivateBytes} (groups_types.go).
- Rules: **owner-selected targets only**; **only runtime 2**; **key (PID, creation time)**; **multi-core CPU not capped at 100**; **two baselines before a rate** (% Processor Time is a rate); **missing never 0**; **never pause/attach/dump**; **no text**.

## Do
1. Factory builds PDH source: `\Process(*)\ID Process` mapping + the three wildcard counters. Engine: CPUPct = KindPDHRate (no 100 cap), WorkingSet/PrivateBytes = KindGauge (uint64).
2. `Collect`: instance→PID map; for each selected runtime-2 target match by PID (confirm Start); build Proc10Target. No instance → omit. Return *Proc10Group; nil if none.
3. Re-expand only on ProcessSetChanged().

## Do not
- Do not attempt EventPipe / open the diagnostic pipe here (that is BBX-3-04, blocked).
- Do not sample runtime-1 targets (BBX-3-01) or unselected processes.
- Do not cap CPU% at 100; do not emit 0 for missing; no text; no attach/dump; no goroutine/ticker.

## Tests to write first (fakes)
- `TestProc10PIDMap` — instance→PID mapping selects the right target.
- `TestProc10Net10Only` — runtime-1 target skipped.
- `TestProc10MultiCoreNotCapped` — CPU% 250 stays 250.
- `TestProc10MissingOmitted` — invalid counter → nil; no instance → omitted.
- `TestProc10RateGated` — CPU% absent until two baselines.

## Verification checklist
- [ ] go build ./... passes
- [ ] go vet ./... passes
- [ ] go test ./internal/blackbox/... -count=1 passes, tests above included
- [ ] Both build-tag variants compile (GOOS=windows and GOOS=linux go vet ./internal/blackbox/...)
- [ ] Footprint: reused handle; re-expand on change; no goroutine/ticker
- [ ] Missing reading is omitted, never 0 (TestProc10MissingOmitted)
- [ ] No text captured; no attach/dump (code review)
- [ ] Owner-selected, .NET 10 only, multi-core not capped (tests above)
- [ ] Two baselines before a rate
- [ ] Budget gate BBX-1-09 still passes

## Live-check needed?
YES — `typeperf -q Process` on a real .NET 10 pool; confirm ID Process mapping and % Processor Time; compare Working Set to Task Manager. Fake-tested only until then.

## Report back
Files changed, each checklist item ticked or failed with output, exact paths used, anything unconfirmed. Full report to `.forge/blackbox-expansion/reports/BBX-3-02.md`.