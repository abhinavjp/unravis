# Drill plan (API drill, then SQL drill 1-5) - LOCKED, waits for owner "go"

Status: round 1 and round 2 picks recorded. Nothing built.
Rules: local install only (rc.32), no tag/release. Hub first (remote hub, creds in
`~/.fjsecrets/cloud_hub.env`), small packets, small models; Opus only for this graph and the
A2 security review. Never read `.env` or `sql/`. Commit only when asked.

## Settled picks
Round 1: Q1=A API drill, then SQL 1-5, then backlog | Q2=C API pop-up, SQL panel under its lane |
Q3=A read all 19 new UAT fields | Q4=B slow-call text hidden, Reveal on click |
Q5=A Nearby: everyone, chips to narrow | Q6=C Breakdown "This window" tab |
Q7=B pop-up reuses Guided Nearby logic, merge copies in the cleanup sweep | Q8=A SQL 1-5 now |
Q9=A orange "likely" for things not visible | Q10=A thresholds: 30% wait share, 2x with 5+ runs.

Round 2:
- N1=A: a red slow-call diamond opens the pop-up; a bar click still goes to Analysis. The pop-up
  has an "Open in Analysis" link.
- N2=B: the 19 fields are read for the opened call AND, as light totals only, for the Nearby rows.
  Not in the main table or analysis.
- N3=A: Reveal is a second request on click. Text is never in the normal record response.
- N4=A + owner addition: Reveal shows the text as stored, cut at 500 characters, URL query values
  masked. A second button, "Show full text", fetches the whole query text. A "Copy" button too.
- N5=B: Nearby chips ask the server for all of that user's / API's calls in the window. Rows already
  loaded stay on screen and filter at once; a visible line says the server is being asked again;
  when the answer arrives the list updates and says how many more were found.
- N6=A: Nearby and Breakdown both use the call plus or minus 15 minutes (Nearby can widen to 60/120).
- P1-P5=A: keep the "events but scanned 0" guard; unknown file span says "Some imported files
  cover..."; show the year when it differs from the window's; disable the "not imported" chip and
  show the reason; copy the imported-SQL range onto the stored-day lane.

## Rulings made by the planner (change on request)
- R1: "Show full text" has a hard server cap of 32 KB, one entry at a time, never logged, sent
  `Cache-Control: no-store`, and dropped from the page when the pop-up closes or the entry changes.
  SQL text can hold real values and cannot be masked reliably, so the only protections are the
  explicit double click, the cap, no storage and no logging. URL query values stay masked in both views.
- R2: Nearby light totals are five numbers only: SqlCommandCount, SqlMilliseconds, RedisMilliseconds,
  HttpCallCount, HttpMilliseconds. No arrays, no names. Old rows show "not recorded", never 0.
- R3: Chip filter keys: user id and endpoint (API name) as exact matches only, so Mongo 3.4 can use
  its normal path. The window and the 100-row and 24 h limits do not change.

## Dependency graph
```
Wave 1 (parallel):  A0 lane fixes (P1-P5)   A1 one-call fields   A3 chip filter   A4 pop-up shell
Wave 2:             A1b neighbour totals (after A1 + A3)   A2 Reveal + full text (after A1)   A7 Breakdown tab (after A4)
Wave 3:             A5 Overview + Reveal UI (after A2 + A4)   A6 Nearby hybrid tab (after A4 + A3 + A1b)
Wave 4:             A8 tests + clumsy-user review (after A5, A6, A7)  ->  A9 local install rc.32
```
Same-file rule: A1, A2 share `internal/store/record.go`; A3, A1b share `internal/audit/activity.go`
(so they run in the order above, one writer at a time). A0 finishes before A4 merges if both touch
`TracingPaper.svelte`. Opus: this graph, A2 security review, final review of A8.

## Packets

### A0 - Lane wording and chip fixes P1-P5 (risk: low)
## Outcome
The five parked fixes from `backlog1/packets.md` (open questions 1-5), each as the owner picked.
## Write scope
The lane builder (`BuildLanes`, `tracingHeldSQLLane`, `layerSQLLane`), the imported-files range
text, the source chip component, their tests and copy.
## Must not change
Lane states and colours; existing TSRC e2e expectations other than the new wording.
## Changes
P1 keep the guard; P2 "Some imported files cover..." when some spans are unknown; P3 year shown
when it differs; P4 "not imported" chip disabled with its reason visible; P5 copy the imported-SQL
range onto the stored-day lane.
## Proof
Go and web unit tests for each; TSRC-11/12 e2e still green plus one new case for P2 and P4.
## Depends on
Nothing.

### A1 - Read the 19 fields for one call (risk: medium)
## Outcome
Opening one call returns request hit count, filter timings, SQL/Redis/HTTP counts and ms, slow SQL
and slow HTTP lists (count and ms, max 3, no names), step timings and server pressure at start. Old
rows return "not recorded", never zero.
## Write scope
`internal/store/record.go`, `internal/audit/models.go` (new perf block on the record detail), tests.
## Must not change
The 18-field projection for tables and analysis; Mongo 3.4-only operators; payload caps.
## Changes
Add the fields to the single-record pipeline only; bound each like `boundedMetadataValue`; arrays
cut to 3; names removed (A2 reads them).
## Proof
Go tests with an old-shape and a new-shape fixture; absent stay nil; oversized or wrong-type values
come back null.
## Depends on
Nothing.

### A2 - Reveal, masking and full text (risk: high, Opus security review)
## Outcome
A separate request for one record and one entry returns the name text. View "preview" is cut at 500
characters; view "full" is cut at 32 KB. URL query values masked in both. When the server did not
store text (`includeCommandText` off) it says "not stored by the server".
## Write scope
`internal/server/` (new handler), `internal/store/record.go` (second small pipeline), tests.
## Must not change
Existing handlers' output; read-only access; nothing logged.
## Changes
Input validation (source id, ObjectID, list kind, index 0-2, view enum), caps, URL mask rule,
`Cache-Control: no-store`, errors that never contain text.
## Proof
Tests: text absent from the normal response; preview and full caps; masked URL; bad index, unknown
id and foreign source refused; error bodies and logs contain no text. Opus reviews the diff.
## Depends on
A1.

### A3 - Chip filter on the activity request (risk: medium)
## Outcome
`/api/activity` accepts an optional user id and an optional endpoint name, exact match.
## Write scope
`internal/audit/activity.go`, its reader and handler validation, tests.
## Must not change
Default (unfiltered) behaviour; 24 h and 100-row limits; cursor rules.
## Changes
Two optional fields on `ActivityInput`, validated (length cap, no operators), applied inside the
existing query.
## Proof
Go tests: filtered, unfiltered, both filters, hostile values (`$`-prefixed, objects) refused.
## Depends on
Nothing.

### A1b - Light totals on Nearby rows (risk: medium)
## Outcome
Each activity row carries the five numbers from R2, so Nearby shows which neighbour was SQL-, Redis-
or HTTP-heavy.
## Write scope
`internal/audit/activity.go` projection and row mapping, `models.go` row type, tests.
## Must not change
The table and analysis queries; row count and limits.
## Changes
Add five bounded numeric fields to the activity projection only.
## Proof
Go tests with old/new shapes; a timing check on a 100-row, 24 h window shows no meaningful slowdown.
## Depends on
A1, A3.

### A4 - Pop-up shell (risk: medium)
## Outcome
`DrillPopup.svelte` opened by a red diamond click (N1=A), modelled on ImportPopup (centred dialog,
focus trap, Esc via `appState.svelte.ts`), tabs Overview / Nearby / Breakdown, an "Open in Analysis"
link.
## Write scope
`web/src/components/` (new popup), `TracingPaper.svelte` (diamond click only, via `lazyComponent`),
copy, tests.
## Must not change
TracingPaper must not import guided/callTrace (guard test); the bar-click hand-off to Analysis.
## Changes
Shell, open/close, focus return, phone layout, copy.
## Proof
Web unit and structure tests, `npm run check`.
## Depends on
Nothing (A0 first if both touch `TracingPaper.svelte`).

### A5 - Overview and Reveal UI (risk: medium)
## Outcome
"Where the time went": SQL/Redis/HTTP/filter split, server pressure at start, per-query rows. Reveal
button shows the 500-character text; "Show full text" and "Copy" follow. Old rows say "not recorded".
## Write scope
New components under `web/src/lib/components/`, copy, tests.
## Must not change
Anything outside the pop-up.
## Changes
Render A1 data; call A2 only on click; text cleared on close or entry change; "not stored" and
error states.
## Proof
Unit tests with old/new fixtures; no text before click; text gone after close.
## Depends on
A2, A4.

### A6 - Nearby tab, hybrid chips (risk: medium)
## Outcome
Everyone around the call (plus or minus 15 min, widen to 60/120), duration bands, 100 rows, per-row
SQL/Redis/HTTP ms. Chip click: loaded rows filter at once, a line says "Asking the server for all of
this user's calls...", then the list updates and says what changed ("37 calls, 33 more than loaded").
## Write scope
New tab component; reuses Guided's `nearby.ts` without changing it.
## Must not change
Guided and Evidence screens and tests.
## Changes
Wire `POST /api/activity` with chips; abort and sequence-guard stale answers; on server failure keep
the loaded rows and say so with Try again; truncated state "showing 100 of more".
## Proof
Unit tests (filter, in-flight line, merge message, stale answer ignored, failure); e2e: open a call,
narrow by user, see the update.
## Depends on
A4, A3, A1b.

### A7 - Breakdown tab (risk: low)
## Outcome
"This window" breakdown (8 views) in the pop-up, plus or minus 15 minutes.
## Write scope
Tab wrapper reusing `BreakdownPanel.svelte`.
## Must not change
`BreakdownPanel` behaviour; `POST /api/breakdown`.
## Changes
Pass the window; keep the 7-day limit message.
## Proof
Unit test; e2e opens the tab.
## Depends on
A4.

### A8 - Tests and break-the-app review (risk: low)
## Outcome
Playwright spec for the drill; clumsy-user review (rapid open/close, Esc, back button, phone width,
old-shape source, VPN off, chip spam, Reveal spam); performance and security check.
## Write scope
`tools/e2e/`, review notes.
## Must not change
Product code (findings go back as fixes).
## Changes
Spec and review report.
## Proof
Full unit suite and the e2e runner green.
## Depends on
A5, A6, A7 (A0 too).

### A9 - Local install (risk: low)
Build and install rc.32 locally for owner testing. No tag, no release. Depends on A8.

## After the API drill
SQL packets S1-S5 are written in detail in round 3, using Q8=A, Q9=A, Q10=A. Then the backlog.
