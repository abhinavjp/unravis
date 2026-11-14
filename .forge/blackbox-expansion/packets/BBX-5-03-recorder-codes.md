# BBX-5-03 Recorder events use fixed codes, never error text
Backlog: BL-123 | Group: 5 | Model: performance | Reason: removes err.Error() from the live event path; no-text rule.
Depends on: none | Blocks: none
Reviewer: Ultimate (batch with SQL group).

## Goal
Stop writing `err.Error()` into Blackbox events. Collect/write/housekeeping failures record a fixed code only, so no driver text, paths, or instance names leak into events or the pack.

## Context
- File `internal/blackbox/recorder.go`: `Tick` writes `Event(EventCollectFailed, err.Error())` at 151-153 and `Event(EventWriteFailed, err.Error())` at 177-185; `housekeep` writes `"housekeeping failed: "+err.Error()` at 225; `checkClock` writes a formatted clock message at 208 (numeric seconds — keep, it is a number).
- `Store.Event(kind, message)` writes into manifest.json events (persisted, shipped in snapshots). The no-text rule (handoff §3) and C10 (fixed codes only) apply.
- Existing event kinds: EventCollectFailed, EventWriteFailed, EventHousekeeping, EventClockJump, EventPausedDiskLow, EventResumed (recorder.go / store.go).
- Rules: **no text** (no err.Error(), paths, instance names); **fixed error codes** (C10); **missing never 0**; keep the first-failure-only dedup behavior.

## Do
1. Replace the three `err.Error()` event messages with fixed codes:
   - Collect failure → `Event(EventCollectFailed, "collect-failed")`.
   - Write failure → `Event(EventWriteFailed, "write-failed")`.
   - Housekeeping failure → `Event(EventHousekeeping, "housekeeping-failed")`.
2. Keep the clock-jump message (it is a direction + number of seconds, allowed) but ensure it contains no path/instance text.
3. If a cause is useful for diagnostics, map known error types to a small fixed code set (e.g. "disk-full", "permission-denied") via a helper `eventCode(err error) string` that NEVER returns err.Error(); default "collect-failed"/"write-failed". Keep the helper pure and tested.
4. Do not change the first-failure-only dedup (collectOK/writeOK flags) or the cadence.

## Do not
- Do NOT write err.Error(), driver text, file paths, or instance names into any event.
- Do NOT remove the events themselves (the UI uses pause/resume/collect events).
- Do NOT change housekeeping logic or the tick loop structure.
- Do NOT log text elsewhere (no new logging of error strings).

## Tests to write first
- `TestCollectFailureFixedCode` — fake collector errors → manifest event message == "collect-failed" (or a fixed code), never the error string.
- `TestWriteFailureFixedCode` — failing writer (wrapFile seam) → event "write-failed".
- `TestHousekeepingFailureFixedCode` — → "housekeeping-failed".
- `TestEventCodeNeverRaw` — eventCode(errors.New("secret path X")) returns a fixed code, not the input.
- `TestNoErrorTextInManifest` — after failures, the serialized manifest contains no substring of the injected error text.

## Verification checklist
- [ ] go build ./... passes
- [ ] go vet ./... passes
- [ ] go test ./internal/blackbox/... -count=1 passes, tests above included
- [ ] Both build-tag variants compile (GOOS=windows and GOOS=linux go vet ./internal/blackbox/...)
- [ ] Footprint: no new goroutine/ticker
- [ ] Missing reading is omitted, never 0 (unchanged)
- [ ] No text captured — events carry fixed codes only (TestNoErrorTextInManifest)
- [ ] First-failure-only dedup preserved (existing recorder tests pass)

## Live-check needed?
No new live check; the owner confirms events show codes only on a real server during normal testing.

## Report back
Files changed, each checklist item ticked or failed with output, anything unconfirmed. Full report to `.forge/blackbox-expansion/reports/BBX-5-03.md`.