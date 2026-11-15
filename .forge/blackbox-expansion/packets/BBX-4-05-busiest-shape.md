# BBX-4-05 Busiest-process list shape (top 10 CPU + top 5 memory + selected)
Backlog: BL-125 | Group: 4 | Model: performance | Reason: ranking/merge/dedupe logic with a downstream consumer (Step 9) that must keep working.
Depends on: BBX-1-05, BBX-1-11 | Blocks: none

## Goal
Change the busiest-process list from "top 20 by CPU" to "top 10 by CPU plus top 5 by memory (merged, no duplicates, Idle left out), plus every selected target always listed even when idle". Same CPU cost (all processes are read to rank them); smaller files. The Step 9 host lane must still find the web-server row.

## Context
- Files: `internal/blackbox/collector.go` (`topProcesses` 154-211; Idle skipped 163-165; truncates to `TopProcesses` 182-184), `internal/blackbox/sample.go` (`TopProcesses = 20` at line 17), test `internal/blackbox/collector_test.go` (shape assertions 203-233).
- `ProcRow` (sample.go:48-58): PID, Name, Start, CPUSec, CPUPct, WS, Private, Handles, Threads. `procID{pid,start}` (collector.go:45-48).
- Selected targets come from the resolver (`ResolvedTarget`, groups.go) — pass them into the collector (a `[]ResolvedTarget` or a `func() []ResolvedTarget`). A selected target is always listed even if idle (CPUPct 0 is a REAL measured idle, allowed here because the process IS running and read; this is not a "missing" reading).
- Downstream consumer: `internal/investigate/hostlane.go` (`topProcess` 389, `TopProcess` 80-82) fed by `internal/server/blackbox.go:1049-1072`. "Step 5 web-server row" is a copy promise (config.go:54-55, blackboxCopy.ts:175); the real code consumer is Step 9. Keep w3wp findable.
- Rules: **Idle left out**; **selected targets always listed**; **missing never 0** (an unreadable process keeps Have-bit-gated fields nil, as today); **same CPU cost**; **no text** (image base name only, as today).

## Do
1. `sample.go`: replace `TopProcesses = 20` with `TopCPU = 10` and `TopMem = 5` (keep a derived `maxProcRows = TopCPU + TopMem` if helpful). Update any reference to TopProcesses.
2. `collector.go` `topProcesses`:
   - Rank all processes by CPU delta (as today) → take top 10.
   - Rank all by working set (WS) → take top 5.
   - Merge, dedupe by procID (a process in both lists appears once), skip PID 0 (Idle).
   - Append every selected target not already present (match by PID+Start), even if idle.
   - Keep the existing Have-bit gating (unreadable fields stay nil) and stable sort/tie-by-PID.
3. Add a `selected []ResolvedTarget` field/param to HostCollector (or a setter) so the resolver wiring can pass targets; default nil = current behavior minus the new shape.
4. Update `collector_test.go` shape tests to the new contract; add the cases below.

## Do not
- Do not drop a selected target because it is idle (always list it).
- Do not include Idle (PID 0).
- Do not duplicate a process that is in both top lists.
- Do not write 0 for an unreadable field (keep Have-bit nil gating).
- Do not increase the number of processes READ (rank all, list fewer) — CPU cost must stay the same.
- Do not break Step 9 (run internal/investigate + internal/server tests).

## Tests to write first
- `TestTopProcessesShape` — >15 processes → at most 10 CPU + 5 mem merged, deduped.
- `TestTopProcessesIdleExcluded` — PID 0 never listed.
- `TestTopProcessesSelectedAlways` — an idle selected target is listed even outside top 10/5.
- `TestTopProcessesDedup` — a process in both CPU and memory top appears once.
- `TestTopProcessesMissingFieldsNil` — access-denied process keeps nil fields (not 0).
- `TestStep9StillFindsWebServer` — with a selected w3wp target, hostlane topProcess finds it (run internal/investigate tests).

## Verification checklist
- [ ] go build ./... passes
- [ ] go vet ./... passes
- [ ] go test ./internal/blackbox/... ./internal/investigate/... ./internal/server/... -count=1 passes
- [ ] Both build-tag variants compile (GOOS=windows and GOOS=linux go vet ./internal/blackbox/...)
- [ ] Footprint: same number of processes read; no goroutine/ticker; smaller output
- [ ] Missing reading is omitted, never 0 (TestTopProcessesMissingFieldsNil)
- [ ] No text captured (image base name only)
- [ ] Idle excluded; selected always listed; deduped (tests above)
- [ ] Step 9 host lane still finds the web-server row (TestStep9StillFindsWebServer)

## Live-check needed?
YES — owner confirms on a real server that the busiest list shows the expected w3wp/services and that Step 9 still names the web server. Fake-tested only until then.

## Report back
Files changed, each checklist item ticked or failed with output, anything unconfirmed. Full report to `.forge/blackbox-expansion/reports/BBX-4-05.md`.