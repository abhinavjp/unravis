# WP-22 Window picker with overlap and length warnings
risk: low    size: M    wave: 5    model: sonnet

## Outcome
The timeline screen has an incident window (default: the last hour, ending now) and a reference window (default: the equal hour before it), both typed in UTC. A window longer than 30 days is refused in plain words and nothing is requested; a reference that shares time with the incident only warns and "Start tracing" stays enabled (TR-02, TR-08).

## Write scope
- `web/src/lib/tracing/windows.ts` (new, pure; also holds the picker copy)
- `web/src/components/TracingPaper.svelte` (only the `data-region="windows"` region and the `windows` state/`run()` call site)
- `web/tests/tracingWindows.test.cjs` (new)

## Must not change
- `web/src/lib/guided/frame.ts` and everything under `web/src/lib/guided/`: copy the overlap rule, never import it (INV-8; phase 05 keeps guided code unlisted).
- `web/src/lib/tracing/model.ts` and the request/stale-response logic of `TracingPaper.svelte` (WP-16a/16b); the `toolbar`, `chart` and `notes` regions (WP-17b, WP-10/11, WP-23).
- No window is sent to the server when it fails validation; the server still validates (WP-15) and a 400 still maps to `errBadWindow`.
- `web/src/components/CallTrace.svelte` (INV-7).

## Changes
Verified in code: guided `frameProblems` (`web/src/lib/guided/frame.ts`) treats `YYYY-MM-DDTHH:mm` strings as UTC through `parseLocalUtc`, flags `end <= start`, a span over its maximum, and overlap with `is < re && rs < ie`; its overlap sentence is `The two windows overlap. Pick a normal period that does not share any time with the incident.` Tracing copies the rule with a 30-day maximum.
Note: the spec names the helper path as unverified; `web/src/lib/tracing/` is created by WP-16a, so `windows.ts` sits beside `model.ts`.
1. `web/src/lib/tracing/windows.ts`:
   - Constants: `MAX_TRACE_SPAN_MS = 30 * 24 * 3600 * 1000`; `MINUTE_MS = 60000`.
   - `interface WindowFields { incidentStart: string; incidentEnd: string; referenceStart: string; referenceEnd: string }` (each `YYYY-MM-DDTHH:mm`, read as UTC).
   - `toFieldUtc(ms: number): string`; `parseFieldUtc(value: string): number` (NaN unless the whole `YYYY-MM-DDTHH:mm` round-trips, so `2026-02-31T10:00` is NaN). Same behaviour as guided `toLocalUtc`/`parseLocalUtc`, own copy.
   - `defaultWindows(nowMs: number): WindowFields`: `end = floor(now to the minute)`; incident = `[end - 1 h, end)`; reference = `[end - 2 h, end - 1 h)`.
   - `interface WindowCheck { errors: string[]; overlap: boolean; body: { incident: { start: string; end: string }; reference: { start: string; end: string } } | null }`; `checkWindows(f: WindowFields): WindowCheck`. `errors` (one sentence per problem, incident first): a field that is not a whole UTC time -> `The {incident|reference} window needs a start and an end, in UTC.`; `end <= start` -> `The {label} window ends before it starts.`; span over `MAX_TRACE_SPAN_MS` -> `The longest window is 30 days. Shorten the {label} window.` (a span of exactly 30 days is allowed; 30 days and 1 second is not, but minute fields make it 30 days and 1 minute in the UI). `overlap` is true when both windows are valid and `incidentStart < referenceEnd && referenceStart < incidentEnd`; overlap is not an error. `body` is `null` when `errors` is non-empty, else both windows as RFC3339 UTC (`...:00.000Z` via `new Date(ms).toISOString()`).
   - `WINDOW_TEXT`: `incidentLabel: 'Incident window (UTC)'`, `referenceLabel: 'Comparison period (UTC)'`, `start: 'Start'`, `end: 'End'`, `startTracing: 'Start tracing'`, `overlap: 'The two windows overlap. Pick a normal period that does not share any time with the incident.'`, `overlapFix: 'Move comparison period before the incident'`, `utcNote: 'Times are typed in UTC.'`; `windowCopyEntries(): LintEntry[]` (ids `tracingWindow.<key>`, same pattern as `tracingCopyEntries` in `web/src/lib/copy/tracingCopy.ts`; `LintEntry` from `../copy/copylint`).
   - `fixOverlap(f: WindowFields): WindowFields`: keeps the incident, moves the reference to the equal length ending exactly where the incident starts (never changes the incident).
2. `web/src/components/TracingPaper.svelte`, region `windows`:
   - Replace WP-16b's local `initialWindows` by `defaultWindows(Date.now())` held in one `$state` object `fields` (type `WindowFields`); the first auto-run on mount uses `checkWindows(fields).body`.
   - Markup: two `<fieldset>` groups (legends `WINDOW_TEXT.incidentLabel`, `WINDOW_TEXT.referenceLabel`), each with two `<input type="datetime-local">` labelled `WINDOW_TEXT.start` / `WINDOW_TEXT.end`; `WINDOW_TEXT.utcNote` as hint text; a primary button `WINDOW_TEXT.startTracing` (becomes `TRACING_TEXT.tracing` and disabled while `status==='loading'`), disabled only when `checkWindows(fields).errors.length > 0`. The button calls the existing `run(body)`; refactor `run()` to take the checked body, nothing else about it changes.
   - Errors render as a `role="alert"` list under the fields and the chart region shows nothing new (the previous chart is cleared by `run()` only when a request starts; a refused window sends no request).
   - Overlap renders as a `role="status"` notice (`WINDOW_TEXT.overlap`) under the reference fields, with a secondary button `WINDOW_TEXT.overlapFix` that sets `fields = fixOverlap(fields)`. The Start tracing button stays enabled.
3. `web/tests/tracingWindows.test.cjs` (`ts.transpileModule` loader as in `web/tests/casehome.test.cjs`; `.svelte` read as text):
   - `defaults are the last hour and the hour before it`: at a fixed `now`, incident spans exactly 60 minutes ending at the minute-floored now; reference ends where the incident starts and is 60 minutes long.
   - `thirty days is allowed and thirty days plus a minute is refused`: errors empty for exactly 30 days; for 30 days + 1 minute the single error is `The longest window is 30 days. Shorten the incident window.` and `body` is `null`.
   - `bad, reversed and impossible times are refused`: `''`, `2026-02-31T10:00` and an end before the start give the matching sentences; no throw.
   - `overlap warns but does not block`: overlapping windows give `overlap === true`, `errors` empty and a non-null `body`; touching windows (reference end equals incident start) give `overlap === false`.
   - `fixOverlap keeps the incident`: result has the same incident, an equal-length reference ending at the incident start, and `checkWindows(result).overlap === false`.
   - `picker copy passes the screen-string lint`: `lintScreenStrings(windowCopyEntries(), deck.terms)` is `[]`.
   - `picker is wired and does not import guided code`: `TracingPaper.svelte` contains `checkWindows`, `defaultWindows`, `WINDOW_TEXT.overlap`, `type="datetime-local"`; `windows.ts` and `TracingPaper.svelte` do not match `/from '.*guided/`.

## Proof
- `cd web && node --test tests/tracingWindows.test.cjs tests/tracingPaper.test.cjs` -> all pass.
- `cd web && npm test` -> all pass; `cd web && npm run check` -> 0 errors.
- Wireframe check (`prototype/tracing-paper-PROTOTYPE.html`, window fields above the chart): two labelled groups, the incident first with a one-hour default ending now and the comparison period before it, a "Start tracing" button; typing a 31-day incident shows the 30-day sentence, no request in the network log; typing an overlap shows the overlap notice with the move button and the button stays enabled. At 375 px the groups stack and nothing scrolls sideways. Keyboard: Tab goes start, end, start, end, then the button.

## Depends on
WP-16b, WP-17b
