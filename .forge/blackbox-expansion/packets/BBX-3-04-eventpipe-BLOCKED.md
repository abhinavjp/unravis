# BBX-3-04 Per-process EventPipe collector (.NET 10) — BLOCKED
Backlog: BL-110 (EventPipe path) | Group: 3 | Model: performance | Reason: EventPipe IPC + in-target overhead; cannot be validated without a real app pool.
Depends on: BBX-1-11, BBX-3-02 | Blocks: BBX-6-03
Status: **BLOCKED — do not dispatch.** Needs a live-server proof first (handoff §5; prioritised "Not yet confirmed").

## Blocked reason
The backlog and security research require proving, on a REAL app pool, that SYSTEM can open `\\.\pipe\dotnet-diagnostic-{pid}` AND measuring the EventPipe overhead inside the target. Neither is confirmed. Until both are proven, building the EventPipe collector risks shipping a path that cannot work or that breaks the footprint budget. The safe fallback (BBX-3-02, Process counters + memory info) ships now instead.

## Live proof a human must run first (on a real .NET 10 app pool server)
1. Find a .NET 10 worker PID (out-of-process dotnet child or in-process w3wp): `tasklist /m coreclr.dll`.
2. As SYSTEM (e.g. `psexec -s -i powershell`), check the pipe exists and is openable read-only:
   `Test-Path "\\.\pipe\dotnet-diagnostic-<PID>"` and attempt a CreateFile open (do NOT send a diagnostic command that pauses the process).
3. Measure overhead: run `dotnet-counters monitor -p <PID> --counters System.Runtime` for 60 s and record the target's CPU delta and the Blackbox-side cost. Confirm it stays inside BL-122 (group < 1 s, no target pause).
4. Record the hosting model (in-process vs out-of-process) per server.

## Goal (once unblocked)
Collect `System.Runtime` and `AspNetCore.Hosting` EventPipe counters for SELECTED .NET 10 targets with no restart: GC heap, allocation rate, thread pool, request queue. Write into the same per-target group shape (extend Proc10Group or a new EventPipeGroup) with missing≠0.

## Context (for the eventual implementer)
- Named-pipe precedent: `internal/keeper/pipe_windows.go:99`, `internal/setup/elevate_windows.go:86`. EventPipe protocol: connect to `\\.\pipe\dotnet-diagnostic-{pid}`, send the IPC "collect counters" command (no suspend). File import of dotnet-counters output already exists (`internal/importer/dotnetcounters`) — reuse its counter parsing, not a live reader.
- Rules: **never pause/restart/attach/dump**; **owner-selected only**; **both hosting models**; **footprint budget**; **missing never 0**; **no text**.

## Do (once unblocked)
1. A pipe client that opens the diagnostic pipe read-only and requests a counter session; if open fails → fall back to BBX-3-02 for that target (never error the whole tick).
2. Parse System.Runtime/AspNetCore.Hosting counters into the group struct; rate gating + valid-status as in the engine.
3. Time-box each collection; if it exceeds the group budget, skip and mark "skipped: slow".
4. Tests with a fake pipe server (no real target).

## Do not
- Do not dispatch this packet before the live proof above is recorded.
- Do not send any command that suspends/dumps the target; counters only.
- Do not enable it by default until the owner confirms overhead on a real server.

## Tests to write first (once unblocked)
- Fake pipe server: connect failure → fallback; counter parse; rate gating; budget skip; missing≠0.

## Verification checklist (once unblocked)
- [ ] go build/vet/test pass; both build-tag variants compile
- [ ] Footprint: group < 1 s; no target pause; fallback on pipe failure
- [ ] Missing never 0; no text; owner-selected only
- [ ] Live proof recorded (pipe openable as SYSTEM; overhead measured)

## Live-check needed?
YES — this entire packet is gated on the live proof above. State plainly in the PR that BL-110 EventPipe is NOT built; only the fallback (BBX-3-02) is.

## Report back
When unblocked: files changed, checklist, the recorded live proof figures. Full report to `.forge/blackbox-expansion/reports/BBX-3-04.md`.