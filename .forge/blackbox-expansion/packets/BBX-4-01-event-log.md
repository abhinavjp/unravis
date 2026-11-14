# BBX-4-01 Windows event log tailer (IDs and numbers only)
Backlog: BL-111 | Group: 4 | Model: performance | Reason: wevtapi query + watermark + strict no-text output; host evidence.
Depends on: BBX-1-05 | Blocks: none

## Goal
Tail the Windows event log for a fixed allowlist of disk/commit/port-exhaustion/WAS-recycle/crash events and record ONLY numeric IDs, level, count and time — never message text or event-data strings.

## Context
- New files `internal/blackbox/events_windows.go` (windows) + `internal/blackbox/events_other.go` (!windows) + shared pure logic `internal/blackbox/events.go`. Test `internal/blackbox/events_test.go` (pure aggregation with fake event rows).
- wevtapi via lazy DLL (`windows.NewLazySystemDLL("wevtapi.dll")`): `EvtQuery`, `EvtNext`, `EvtRender`, `EvtClose`. Query System and Application channels with an XPath filter on the allowlisted EventIDs and a time watermark (`TimeCreated[timediff(@SystemTime) <= N]` or `>= lastWatermark`). Read only `EventID`, `Level`, `TimeCreated`, `Channel` — do NOT render the message or EventData strings.
- Fixed allowlist (channel, provider, IDs): System — disk/Ntfs/volmgr (7,9,11,15,51,55,57,98,129,140,153), Resource-Exhaustion-Detector (2004), WAS (5002,5009,5011,5013,5021,5057,5074,5079,5080,5117,5186); Application — Application Error (1000,1001,1002), .NET Runtime (1026). Encode as a constant table; channel as a numeric enum (1=System,2=Application).
- `EventsGroup{Events []EventEntry}`; EventEntry{Channel uint8, EventID uint32, Level uint8, Count uint32, T time.Time} (groups_types.go).
- Rules: **event log = IDs and numbers only** (no message text, no payloads, no account names); **missing never 0**; **light** (cap events per tick, e.g. 256; watermark advances so no re-read).

## Do
1. Pure `aggregateEvents(rows []rawEvent, since time.Time) []EventEntry` — group by (channel,eventID,level), count, latest T; sort by T desc; cap 256. Testable without Windows.
2. windows: `queryEvents(since time.Time) ([]rawEvent, error)` — EvtQuery per channel with the ID allowlist + watermark; EvtRender only the numeric/system fields. Advance watermark to max TimeCreated seen. On access denied → return error (group omitted, never 0).
3. Group factory RegisterGroup("events", true, ...); Collect uses a stored watermark (start = now on first run, no historical backfill); returns *EventsGroup or nil.
4. events_other.go: queryEvents returns "Windows only"; group skipped on non-Windows.

## Do not
- Do NOT render or store event message text, EventData strings, account names, or process paths.
- Do not backfill history on restart (start at now).
- Do not read more than the cap per tick; no goroutine/ticker.
- Do not emit 0 for "no events" — return nil group (omitted).

## Tests to write first (pure)
- `TestAggregateEventsCounts` — duplicate IDs counted, grouped by channel/level.
- `TestAggregateEventsCap` — >256 rows capped.
- `TestAggregateEventsNoText` — rawEvent has only numeric fields; assert EventEntry has no string message field (compile-time shape).
- `TestEventsWatermark` — fake query returns rows; watermark advances; second call only newer.
- `TestEventsAccessDeniedOmitted` — query error → nil group, not zero.

## Verification checklist
- [ ] go build ./... passes
- [ ] go vet ./... passes
- [ ] go test ./internal/blackbox/... -count=1 passes, tests above included
- [ ] Both build-tag variants compile (GOOS=windows and GOOS=linux go vet ./internal/blackbox/...)
- [ ] Footprint: capped query per tick; watermark; no goroutine/ticker
- [ ] Missing reading is omitted, never 0 (TestEventsAccessDeniedOmitted)
- [ ] No text captured — IDs/numbers/time only (TestAggregateEventsNoText + code review of EvtRender fields)
- [ ] Budget gate BBX-1-09 still passes

## Live-check needed?
YES — owner triggers a benign event (e.g. a WAS recycle) on a real server and confirms the IDs appear with no message text. Fake-tested only until then.

## Report back
Files changed, each checklist item ticked or failed with output, the allowlist used, anything unconfirmed. Full report to `.forge/blackbox-expansion/reports/BBX-4-01.md`.