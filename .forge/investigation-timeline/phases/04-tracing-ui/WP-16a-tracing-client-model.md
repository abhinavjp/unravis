# WP-16a Tracing client, types and shell copy
risk: low    size: M    wave: 2    model: sonnet

## Outcome
The web has one typed, shape-checked client for `POST /api/cases/{id}/tracing`, the lane/response types every later tracing packet uses, a per-tab monotonic `requestSeq`, one error-to-sentence map, and the lint-checked shell copy (TR-01, TR-04, TR-10, TR-12). Pure TypeScript; nothing is mounted yet.
Split of spec WP-16 (reported to the caller): 16a is the data layer, WP-16b is the screen and the case-home entry.

## Write scope
- `web/src/lib/tracing/model.ts` (new)
- `web/src/lib/copy/tracingCopy.ts` (new)
- `web/tests/tracingModel.test.cjs` (new)

## Must not change
- `web/src/lib/cases.ts` (only import `send`, `casePath`, `CaseApiError`, `isCaseLocked`; do not edit).
- Anything under `web/src/lib/guided/` and `web/src/components/CallTrace.svelte` (INV-7, INV-8). Do not import `callTrace.ts` here: its `LaneKey` is a different type (`sql|mongo|redis|iis|other`).
- No raw cookie, IP, query string or user agent field in any type (INV-3). Never read `sql/`; never create `.unravis.db`.

## Changes
1. `web/src/lib/tracing/model.ts`. Types (one line each):
   - `TracingLaneKey = 'audit' | 'sql' | 'iis'` (this order is the lane order everywhere).
   - `TracingLaneState = 'events' | 'no-data' | 'clock-unchecked' | 'out-of-range' | 'none-seen' | 'incomplete'`.
   - `TracingReason = 'timeout' | 'rowcap' | 'error'`; `TracingConsistency = 'live-non-snapshot' | 'stored-as-of' | 'snapshot'`; `TracingOrigin = 'live' | 'case'`.
   - `TracingRange { start: string; end: string }` (RFC3339 UTC, half-open `[start, end)`; the request windows and the echoed incident use it).
   - `TracingWindow { start: string; end: string; axisStart: string; axisEnd: string; bucketSeconds: number }` (the response `window`: `start`/`end` echo the incident, `axisStart`/`axisEnd` are the server's floor-aligned axis, `bucketSeconds` is the bucket width; there is no top-level `bucketSeconds`; Note: the WP-14 shell answers `bucketSeconds` 0 until WP-15 lands; phase 03 is fully merged before phase 04 starts, so this parser may treat a non-positive value as an error; the wire also has `window.buckets`, a plain count of axis cells, and the client ignores it).
   - `TracingSourceRef { sourceId: string; label: string }` (exactly what the server sends per lane and at the top level).
   - `TracingBucket { start: string; count: number; errors: number; p50Ms?: number; p95Ms?: number }`: the server bucket is `{start,count,errors?}` (the SQL and IIS lanes have no 5xx, so `errors` is absent there); `parseTracing` defaults a missing `errors` to 0 and ignores the other server bucket fields (`end`, `clipped`, `complete`). The server clips the first and last bucket `start`/`end` to the request window, so `start` is not always a multiple of the width; positioning in the chart is by `floor((start - axisStart)/width)` (WP-17b).
   - `TracingLane { key; label; state; reason: string; origin; asOf: string; consistency; complete: boolean; truncated: boolean; disabled?: string; scanned: number; hidden: number; sources: TracingSourceRef[]; buckets: TracingBucket[]; events: unknown[] }` where `reason` is the server wording for a missing or partial state (may be empty), `incompleteReason?: TracingReason` is read from the lane field `reasonCode` when present, and `disabled` is the lane field (`"tz-rule"` or absent; an empty string becomes absent).
   - `TracingResponse { window: TracingWindow; lanes: TracingLane[]; sources: TracingSourceRef[]; allLanesStored: boolean; mixedConsistency: boolean; truncated: boolean; warnings: string[]; requestSeq?: number }`. `truncated` is server-derived (true when any lane is truncated); `parseTracing` falls back to `lanes.some(l => l.truncated)` only when the field is missing. `requestSeq` is echoed only when it was sent, so it is optional; the caller compares with the seq it sent, not with a field it may not get.
   - `TracingRequest { sourceIds?: string[]; incident: TracingRange; reference: TracingRange }` (the sent body adds `requestSeq`). An omitted or empty `sourceIds` means every source of the case (the WP-14 contract).
   Note: WP-14 is the single source of truth for these field names; they are copied from its response contract (`window`, per-lane `truncated`/`disabled`/`consistency`/`reasonCode`, top-level `mixedConsistency`/`truncated`/`sources`). If WP-14 and this list ever differ, change this packet, not the server.
2. `model.ts` functions:
   - `nextRequestSeq(): number` (module counter, strictly increasing, starts at 1) and `isStale(responseSeq: number, latestSeq: number): boolean` (true when `responseSeq < latestSeq`).
   - `buildTracingBody(req: TracingRequest, seq: number): string`: JSON with `incident`, `reference`, `requestSeq`, and `sourceIds` only when the array is non-empty (an omitted list means every source of the case, the WP-14 contract). No other field, because the server refuses unknown fields with 400.
   - `fetchTracing(caseId: string, req: TracingRequest, seq: number, signal?: AbortSignal): Promise<TracingResponse>`: `send(casePath(caseId, '/tracing'), { method: 'POST', body: buildTracingBody(req, seq) }, signal)`; parse JSON; `parseTracing(raw)` returns the typed value or throws `CaseApiError('The server sent an answer that could not be read.', 200, 'unreadable')` when `lanes` is not an array, a lane key is not one of the three, `buckets` is not an array, or `window.bucketSeconds` is not a positive number or `window.axisStart`/`window.axisEnd` is not a parseable date. Unknown lane states fall back to `'incomplete'`. Missing `consistency` defaults per spec: `live-non-snapshot` when `origin` is `live`, else `stored-as-of`. `mixedConsistency` defaults to "any lane is `live-non-snapshot`"; `errors` defaults to 0 per bucket; `sources` defaults to `[]`.
   - `tracingErrorText(error: unknown): string`: 400 gives `TRACING_TEXT.errBadWindow`; 423 (or `isCaseLocked`) gives `errLocked`; 409 gives `errClosed`; 503 gives `errUnavailable`; any other status or an unreachable server gives `errServer`. Never returns server text, URLs or ids.
3. `web/src/lib/copy/tracingCopy.ts`: `TRACING_TEXT` with exactly these keys and values (copy verbatim):
   - `heading: 'Investigation Timeline'`; `entryNote: 'See the API audit, SQL and IIS on one timeline for an investigation window, next to a comparison period to compare with.'`; `entryButton: 'Open the Investigation Timeline'`; `backToCase: 'Back to the case'`; `readingWindow: 'Reading the window…'`; `retry: 'Try again'`; `cancel: 'Cancel'`; `tracing: 'Tracing...'`; `emptyNoSources: 'No files have been added to this case yet. Add a source first.'`; `closedReason: 'This case is closed. You can read it, but nothing can be added.'`.
   - Errors: `errBadWindow: 'The window or bucket size is not allowed.'`; `errLocked: 'Unlock the case key to trace.'`; `errUnavailable: 'The case store is unavailable right now.'`; `errServer: 'The case could not be read right now.'`; `errClosed` equals `closedReason`.
   - Plus `tracingCopyEntries(): LintEntry[]` mapping each key to `{ id: 'tracing.' + key, text }` (same pattern as `caseHomeCopyEntries` in `web/src/lib/copy/caseHomeCopy.ts`; import `LintEntry` from `./copylint`).
4. `web/tests/tracingModel.test.cjs` (load `.ts` through the same `ts.transpileModule` loader helper used in `web/tests/casehome.test.cjs`). Tests:
   - `requestSeq is strictly increasing and stale responses are ignored`: two `nextRequestSeq()` calls increase; `isStale(1, 2)` true, `isStale(2, 2)` false.
   - `request body carries only the schema fields`: `buildTracingBody` with empty `sourceIds` has keys exactly `incident, reference, requestSeq`; with two ids adds `sourceIds`.
   - `parseTracing rejects bad shapes and fills consistency defaults`: a lane list of non-array throws; a `live` lane without `consistency` becomes `live-non-snapshot`, `mixedConsistency` true; a case-only response has `allLanesStored` as sent; a server bucket without `errors` gets `errors` 0; a lane `disabled: "tz-rule"` and a lane `truncated: true` are kept, and a response without a top-level `truncated` derives it from the lanes; `window.axisStart`, `axisEnd` and `bucketSeconds` are read from `window`, and a missing `requestSeq` stays undefined.
   - `error text is fixed per status`: 400, 423, 409, 503, 500 and a status-0 error each map to the exact sentence above and never contain the input message.
   - `tracing copy passes the screen-string lint`: `lintScreenStrings(tracingCopyEntries(), deck.terms)` is `[]` (load `deck` and `lintScreenStrings` as `web/tests/casehome.test.cjs` does). If the lint rejects a sentence, change only that word and add a `Note:` to `progress.md`.

## Proof
- `cd web && node --test tests/tracingModel.test.cjs` -> all pass.
- `cd web && npm test` -> all pass (no existing test changes).
- `cd web && npm run check` -> 0 errors.

## Depends on
WP-14
