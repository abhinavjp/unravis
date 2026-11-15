# BBX-5-16 Blackbox SQL lane source (server)
Backlog: BL-103 | Group: 5 | Model: performance | Reason: new lane data source on the app HTTP API; endpoint review.
Depends on: BBX-2-04 (sqlHost counters exist) | Blocks: BBX-5-17
Reviewer: Ultimate (HTTP endpoint).

## Goal
When a case has a Blackbox source whose samples carry SQL host counters (BL-100), expose the database lane from that Blackbox sampling as a source labeled "Blackbox sampling every 15 s since <date>", instead of "Not recorded".

## Context
- Files: `internal/server/blackbox.go` (hostSamples 1049-1072 reads blackbox samples; applyBlackboxLane 1208), `internal/server/tracing.go` (+ tracing_sqllive.go / tracing_qsfile.go / tracing_apiseen.go — the DB-lane source builders). Read these to find where the DB lane source is chosen.
- The blackbox `Sample.SQLHost` group (BBX-2-04) carries BatchRequestsPerSec etc. per instance. The reader (`internal/blackbox/reader.go ReadSamples`) already returns Samples.
- Web side (BBX-5-17) expects a source key "blackbox" and a label string + a "since" timestamp.
- Rules: **no text** (the lane shows numeric activity + a fixed label, no query text); **missing never 0** (no blackbox SQL data → fall through to the existing source, never a zero lane); **read-only**; the label is a fixed sentence with a date.

## Do
1. Add a DB-lane source builder `blackboxSQLLane` that, given the blackbox samples for the window, produces lane blocks from SQLHost.BatchRequestsPerSec (activity per minute) when present.
2. Wire it into the DB-lane source selection: if a Blackbox source has SQLHost data for the window, use it with source key "blackbox", label "Blackbox sampling every 15 s since <first sample date>", and the since timestamp. Otherwise fall through to the existing Query Store / API / Not-recorded logic unchanged.
3. Return the source key + label + since in the lane API response (the shape the web dbState expects). No query text, no instance error text.
4. Keep it behind the existing app auth; read-only.

## Do not
- Do NOT emit a zero lane when there is no blackbox SQL data (fall through).
- Do NOT include query text, instance names beyond the fixed label, or error text.
- Do NOT change the existing Query Store/API lane behavior when blackbox data is absent.
- Do NOT add a new mutating endpoint.

## Tests to write first
- `TestBlackboxSQLLanePresent` — fake blackbox samples with SQLHost → lane source key "blackbox", label contains "Blackbox sampling every 15 s since", blocks from BatchRequestsPerSec.
- `TestBlackboxSQLLaneAbsentFallsThrough` — no SQLHost → existing source selection unchanged (not a zero lane).
- `TestBlackboxSQLLaneNoText` — response carries no query text.
- `TestBlackboxSQLLaneSince` — since == first sample timestamp.

## Verification checklist
- [ ] go build ./... passes
- [ ] go vet ./... passes
- [ ] go test ./internal/server/... ./internal/blackbox/... -count=1 passes, tests above included
- [ ] Both build-tag variants compile (GOOS=windows and GOOS=linux go vet ./internal/server/...)
- [ ] Footprint: reads existing samples on demand; no goroutine/ticker
- [ ] Missing reading is omitted, never 0 (TestBlackboxSQLLaneAbsentFallsThrough)
- [ ] No text captured (TestBlackboxSQLLaneNoText)
- [ ] Existing lane sources unchanged when blackbox absent

## Live-check needed?
YES — owner confirms on a real case with a Blackbox source that the DB lane shows "Blackbox sampling every 15 s since <date>" and the activity matches the sampled counters. Fake tests verify selection/shape only.

## Report back
Files changed, each checklist item ticked or failed with output, anything unconfirmed. Full report to `.forge/blackbox-expansion/reports/BBX-5-16.md`.