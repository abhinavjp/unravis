# BBX-3-03 Per-process I/O, pagefile, virtual bytes, elapsed
Backlog: BL-116 | Group: 3 | Model: performance | Reason: per-target Process I/O counters + PID mapping.
Depends on: BBX-1-05, BBX-1-06, BBX-1-11 | Blocks: none

## Goal
For SELECTED targets (any runtime), sample per-process I/O read/write bytes/sec, pagefile bytes, virtual bytes and elapsed time every 15 s.

## Context
- New file `internal/blackbox/procio.go` (no tag) + test `internal/blackbox/procio_test.go` (fakes). Group pattern as BBX-2-01 (init RegisterGroup("procIO", true, ...); optional). Uses `d.Targets()`.
- Map Process instance→PID via `\Process(*)\ID Process`. Counters (UNVERIFIED): `\Process(<inst>)\IO Read Bytes/sec`, `\Process(<inst>)\IO Write Bytes/sec`, `\Process(<inst>)\Page File Bytes`, `\Process(<inst>)\Virtual Bytes`, `\Process(<inst>)\Elapsed Time`.
- `ProcIOGroup{Targets []ProcIOTarget}`; ProcIOTarget{Target, IOReadBytesPerSec, IOWriteBytesPerSec, PageFileBytes, VirtualBytes, ElapsedSec} (groups_types.go).
- Rules: **owner-selected targets only**; **key (PID, creation time)**; **two baselines before a rate** (IO bytes/sec); **missing never 0**; **no text**; **no attach/dump**.

## Do
1. Factory builds PDH source: ID Process mapping + the five wildcard counters. Engine: IORead/IOWrite = KindPDHRate; PageFileBytes/VirtualBytes = KindGauge (uint64); ElapsedSec = KindGauge (seconds).
2. `Collect`: instance→PID map; for each selected target match by PID (confirm Start); build ProcIOTarget. No instance → omit. Return *ProcIOGroup; nil if none.
3. Re-expand only on ProcessSetChanged().

## Do not
- Do not sample unselected processes; do not assume instance==PID.
- Do not emit 0 for missing; no text; no attach/dump; no goroutine/ticker; re-expand only on change.

## Tests to write first (fakes)
- `TestProcIOPIDMap` — correct target matched by PID.
- `TestProcIOSelectedOnly` — unselected skipped.
- `TestProcIOMissingOmitted` — invalid counter → nil; no instance → omitted.
- `TestProcIORateGated` — IO bytes/sec absent until two baselines.
- `TestProcIOFields` — pagefile/virtual/elapsed map to correct fields.

## Verification checklist
- [ ] go build ./... passes
- [ ] go vet ./... passes
- [ ] go test ./internal/blackbox/... -count=1 passes, tests above included
- [ ] Both build-tag variants compile (GOOS=windows and GOOS=linux go vet ./internal/blackbox/...)
- [ ] Footprint: reused handle; re-expand on change; no goroutine/ticker
- [ ] Missing reading is omitted, never 0 (TestProcIOMissingOmitted)
- [ ] No text captured; owner-selected only; (PID,creation) identity
- [ ] Two baselines before a rate
- [ ] Budget gate BBX-1-09 still passes

## Live-check needed?
YES — `typeperf -q Process` on a real server; confirm IO Read/Write Bytes/sec, Page File Bytes, Virtual Bytes, Elapsed Time names; compare to Task Manager columns. Fake-tested only until then.

## Report back
Files changed, each checklist item ticked or failed with output, exact paths used, anything unconfirmed. Full report to `.forge/blackbox-expansion/reports/BBX-3-03.md`.