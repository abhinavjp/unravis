# WP-17c Tracing chart tooltips from the dictionary
risk: medium    size: S    wave: 5    model: sonnet

## Outcome
Every lane head and every bar of the tracing chart has a plain-word tooltip built from the WP-08/WP-09 `explain()` dictionary and from the lane's own metadata: what the lane is, the bucket's time range and counts, whether the data was read just now or came from saved logs, and the plain reason when a read was cut short. The tooltip works with the mouse, with keyboard focus and with the bucket selector's arrow keys, and every word of it is escaped text (TR-22, TR-10 look: an incomplete read is explained where the bar is, never silently).

## Write scope
- `web/src/lib/tracing/tooltip.ts` (new, pure; imports only `../copy/dictionary` and types from `./model`)
- `web/src/components/TracingChart.svelte` (WP-17b created it; this packet adds the tooltip markup, handlers and styles only)
- `web/tests/tracingTooltips.test.cjs` (new)

## Must not change
- `web/src/lib/copy/dictionary.ts` and everything else under `web/src/lib/copy/` (WP-08 owns the dictionary; if a needed id is missing, use a sentence built in `tooltip.ts` from the lane data instead of adding an id).
- `web/src/components/CallTrace.svelte` (INV-7: only WP-09 touched it) and `web/src/lib/charts.ts` (do not import it: ECharts, INV-6).
- The slot grid and bar maths in `web/src/lib/tracing/lanes.ts` (WP-17b): call `slotGrid`, `slotStartMs`, `barsFor` as they are; do not edit that file. The bucket selector keys and `onSelect(slotIndex)` of WP-17b keep working exactly as they are.
- No `{@html}` anywhere in `TracingChart.svelte`; no raw cookie, IP, query string or user agent is shown (INV-3); the tooltip never shows `lane.events`.
- No timing assertion of any kind (INV-5).

## Changes
1. `web/src/lib/tracing/tooltip.ts` (pure; every function returns plain strings, the component renders them as text):
   - `LANE_DICT_ID: Record<TracingLaneKey, string> = { audit: 'lane.api-audit', sql: 'lane.sql', iis: 'lane.iis' }`.
   - `laneHint(key: TracingLaneKey): string` = `explain(LANE_DICT_ID[key])` (empty string for an unknown key).
   - `interface BarTipInput { key: TracingLaneKey; startText: string; endText: string; zoneName: string; count: number; errors: number; origin: 'live' | 'case'; asOfText: string; note: string }` (`startText`/`endText` already formatted by the component with `formatTime`; `note` is `laneNote(lane)` for an incomplete lane, else `''`).
   - `barTipLines(input: BarTipInput): string[]`, one string per line, in this order and with these exact wordings (`N` is the count with a thousands separator from `toLocaleString('en-GB')`):
     1. `startText + ' to ' + endText + ' (' + zoneName + ')'`.
     2. `N request` / `N requests` for `audit` and `iis`; `N call` / `N calls` for `sql`; `No events in this bucket` when the count is 0 (then only lines 1, the origin line and the note follow).
     3. Only for `audit` with `errors > 0`: `E server errors (5xx)` (`1 server error (5xx)` for one), then `explain('status.5xx')` as its own line.
     4. Only for `iis` with `count > 0`: `explain('iis.point')` as its own line.
     5. The origin line: `Read just now` for `live`, `From saved logs` for `case`, followed by ` (as of ` + `asOfText` + `)` when `asOfText` is not empty.
     6. When `note` is not empty: `note` as the last line.
     Lines whose text is empty (a missing dictionary id) are dropped, never shown as blanks.
   - `slotAtFraction(fraction: number, n: number): number`: clamp `floor(fraction*n)` to `0..n-1`; `-1` when `n < 1` or the fraction is not finite. Used to turn a pointer position into a slot.
2. `web/src/components/TracingChart.svelte`:
   - Import `laneHint`, `barTipLines`, `slotAtFraction` from `../lib/tracing/tooltip`. No other new import.
   - Lane head: the element that holds the lane heading and `LANE_ASK` gets `tabindex="0"`, `aria-describedby="tc-hint-{key}"` and, next to it, `<span id="tc-hint-{key}" role="tooltip" class="lane-tip">{laneHint(key)}</span>`. The tip is hidden by default and shown by CSS on `:hover` and `:focus-within` of the lane head (so a keyboard user who tabs to the head reads it). No `title` attribute is used for this text, because `title` does not appear on keyboard focus.
   - Bar tooltip: one `<div id="tc-tip" role="tooltip" class="tip" hidden={!tip}>` after the panels, filled with `{#each tip.lines as line}<div>{line}</div>{/each}` (text interpolation only, so the browser escapes it). State `tip: { lines: string[]; leftPct: number; laneKey: TracingLaneKey } | null`.
   - Pointer: on `pointermove` over a lane's panel compute the slot with `slotAtFraction((clientX - rect.left) / rect.width, g.n)`; set `tip` for that lane and slot; `pointerleave` sets it to null. A slot with no returned bucket still shows the tip with count 0. Do not read the pointer position for anything but that fraction.
   - Keyboard: the WP-17b bucket-selector group gets `aria-describedby="tc-tip"`; whenever its local selection changes (ArrowLeft/ArrowRight) or it receives focus, set `tip` for the selected slot in the top visible lane, and set it to null on blur or Escape. Escape only hides the tip; it does not change the selection.
   - Tooltip data for a lane and slot: find the bar with `barsFor(g, lane.buckets)` (count and errors, 0 when none), `startText = formatTime(slotStartMs(g, i), { zone, precision: 'minute' })`, `endText` the same for `slotStartMs(g, i) + g.widthMs`, `origin = lane.origin`, `asOfText = formatTime(lane.asOf, { zone, precision: 'minute' })` (empty when `lane.asOf` is empty), `note = lane.state === 'incomplete' ? laneNote(lane) : ''`.
   - The tip is positioned with `left: {leftPct}%` clamped to `4%..96%` of the panel column and `top` above the lane; it is `pointer-events: none`, uses the page's existing CSS variables for surface, border and text (no new colour), has a 1px border and wraps at `max-width: 22rem`. It never moves the layout (absolute inside the panel wrapper) and never causes page scroll at 375 px.
3. `web/tests/tracingTooltips.test.cjs` (node:test; copy the `loadTypeScript` helper from `web/tests/casehome.test.cjs`; load `src/lib/tracing/tooltip.ts`, which pulls in `src/lib/copy/dictionary.ts` through the helper's relative-import branch; read the `.svelte` as text). Tests:
   - `lane hints come from the dictionary`: `laneHint('audit')`, `laneHint('sql')`, `laneHint('iis')` equal `explain('lane.api-audit')`, `explain('lane.sql')`, `explain('lane.iis')` and are non-empty; an unknown key gives `''`.
   - `bar tip lines are exact and ordered`: an `audit` input (count 1200, errors 3, `live`, as-of text `10:05`) gives `['10:00 to 10:01 (UTC)', '1,200 requests', '3 server errors (5xx)', explain('status.5xx'), 'Read just now (as of 10:05)']`; one error gives `1 server error (5xx)`; an `iis` bar with count 2 contains `explain('iis.point')`; an `sql` bar with count 1 says `1 call`; count 0 gives `No events in this bucket` and no 5xx or IIS line; a `case` lane says `From saved logs`; a `note` is the last line; empty lines are dropped.
   - `incomplete reads are explained in the tip`: a lane note `The audit read ran out of time; nothing was shortened.` appears as the last line of the tip of an `incomplete` lane's bar (TR-10), and not in a tip with `note: ''`.
   - `slotAtFraction clamps`: `0 -> 0`, `0.999 -> n-1`, `1 -> n-1`, `-1 -> 0`, `NaN -> -1`, `n = 0 -> -1`.
   - `tooltip text is escaped plain text`: through the loaded dictionary module set `DICTIONARY['status.5xx'].plain = '<b>&'`; the returned line equals `<b>&` as a string (the module does no HTML), and `TracingChart.svelte` contains no `{@html` and no `innerHTML`.
   - `tooltip is reachable by keyboard`: `TracingChart.svelte` contains `role="tooltip"`, `tabindex="0"` on the lane head, `aria-describedby="tc-tip"`, `:focus-within`, `Escape`, `pointermove` and `pointerleave`; contains no `title={laneHint` (a `title` is not shown on focus); has no `echarts`, no `callTrace`, no `guided`.
   - `lane head and bar tip wording passes the screen lint`: `lintScreenStrings` over the fixed strings in `tooltip.ts` (`No events in this bucket`, `Read just now`, `From saved logs`, the `server error` lines) returns `[]`; if one word is rejected, change only that word and record it in `progress.md`.
   - `WP-17b behaviour is unchanged`: `TracingChart.svelte` still contains `aria-roledescription="bucket selector"`, `onSelect`, `role="note"` and `MIXED_NOTE`.

## Proof
- `cd web && node --test tests/tracingTooltips.test.cjs tests/tracingChart.test.cjs tests/dictionary.test.cjs` -> all pass.
- `cd web && npm test` -> all pass; `cd web && npm run check` -> 0 errors.
- `cd web && npm run build && node --test tests/distChunks.test.cjs` -> pass (the dictionary has no imports, so no chunk cycle).
- Wireframe check (`prototype/tracing-paper-PROTOTYPE.html`, the floating `#tip`): at 1280 px and 375 px, light and dark, hovering a bar shows the slot range and the per-lane lines; Tab to a lane head shows its one-sentence hint; focus the bucket selector and press ArrowRight, the tip follows the selected slot; Escape hides it; on a response with an incomplete audit lane the tip of its bars ends with the plain reason. No horizontal page scroll; console clean.

## Depends on
WP-17b, WP-09
