# WP-23 Chart controls: zoom, reset, hidden rows, P50/P95 line
risk: medium    size: M    wave: 6    model: sonnet

## Outcome
The tracing chart has its own controls: drag across the bars (or use Zoom in / Zoom out) to shrink the visible span, "Reset zoom" returns the full window, rows can be hidden through a `visible` prop, and a Typical (P50) / Slow end (P95) selector picks the latency line when the server returned latency figures. Zoom only changes what is visible; it never refetches or sums (TR-23, INV-1).

## Write scope
- `web/src/components/TracingChart.svelte`
- `web/src/lib/tracing/canvas.ts` (new, pure)
- `web/tests/tracingChart.test.cjs` (add tests; keep WP-17b's tests unchanged)
- `web/tests/tracingTooltips.test.cjs` is WP-17c's: do not edit it; if a WP-17c assertion breaks, fix the component, not the test (the only allowed exception is a regex that pinned `g.n`, which the report must name)

## Must not change
- `web/src/lib/tracing/lanes.ts` (WP-17b) and its slot maths: zoom works on slot indices of the existing `SlotGrid`, the grid itself (UTC floor aligned, at most 120 slots) is never recomputed or re-anchored (INV-1).
- `web/src/components/CallTrace.svelte` and `web/src/lib/callTrace.ts` (INV-7); `web/src/lib/charts.ts` (no import; no ECharts); `web/src/lib/guided/`.
- `TracingPaper.svelte`: WP-11 passes `visible` to the chart. This packet does not edit it, so it does not touch the toolbar from WP-10 either.
- The mixed-consistency note and the per-lane incomplete sentences stay visible at every zoom level.

## Changes
Size: this is the upper bound of M (one pure module, one component, eight tests). If a run overflows, deliver zoom/reset/`visible` first and leave the latency line and its selector for a follow-up that the caller opens; report which part was left.
Note: WP-17c (a dependency) already put hover/focus tooltips in `TracingChart.svelte`. Keep them working under zoom: the tooltip slot is `range.from + slotAtFraction(fraction, range.to - range.from)` (not `g.n`), and the same absolute-index rule holds for every interaction (see the INDEX RULE under step 2), a drag in progress hides the tip, and the brush uses `pointerdown`/`pointerup` on the same panel without removing WP-17c's `pointermove`/`pointerleave` handlers.
Note: the spec lists `TracingPaper.svelte` for this packet. The controls live inside the chart component and the lane toggles are WP-10's buttons, so `TracingPaper.svelte` is left to WP-11, which depends on this packet and passes `visible`. The P50/P95 selector is a target until benchmark BM-04: the audit rollup returns counts, not percentiles, so the line draws only for buckets that carry `p50Ms`/`p95Ms`, and the selector says so when none do.
1. `web/src/lib/tracing/canvas.ts` (pure, imports only types from `./model`):
   - `MIN_ZOOM_SLOTS = 4`; `interface ZoomRange { from: number; to: number }` (slot indices, half-open).
   - `fullRange(n): ZoomRange` = `{from:0,to:n}`; `isZoomed(r, n): boolean`; `clampRange(r, n)` keeps `0 <= from < to <= n` and a width of at least `min(MIN_ZOOM_SLOTS, n)`.
   - `zoomIn(r, n, focus?: number)`: new width `max(MIN_ZOOM_SLOTS, floor(width/2))` (capped at the current width), centred on `focus` (default: the middle of `r`), clamped. `zoomOut(r, n)`: width doubled (capped at `n`), centred, clamped. `rangeFromDrag(n, a, b)`: order-insensitive slot indices, both included, `[min, max+1)`, clamped.
   - `type Percentile = 'p50' | 'p95'`; `PERCENTILES: Percentile[] = ['p50','p95']`; `PERCENTILE_LABEL = { p50: 'Typical (P50)', p95: 'Slow end (P95)' }`; `percentileValue(b: TracingBucket, p: Percentile): number | null` (the bucket's `p50Ms`/`p95Ms` when a finite number >= 0, else null); `hasLatency(buckets: TracingBucket[], p): boolean`.
   - `CANVAS_TEXT`: `ariaLabel: 'Chart controls'`, `zoomIn: 'Zoom in'`, `zoomOut: 'Zoom out'`, `resetZoom: 'Reset zoom'`, `latencyLabel: 'Latency line'`, `noLatency: 'No latency figures were returned for this window.'`, `allHidden: 'All rows are hidden. Turn a row on to see it.'`, `dragHint: 'Drag across the chart to zoom into those bars.'`; `zoomedText(shown: number, total: number): string` = `'Showing ' + shown + ' of ' + total + ' bars.'`; `canvasCopyEntries(): LintEntry[]` (ids `trace-canvas.<key>`; `LintEntry` from `../copy/copylint`).
2. `web/src/components/TracingChart.svelte` (WP-17b component; add, do not rewrite):
   - New props: `visible?: Partial<Record<TracingLaneKey, boolean>>` (a lane is shown unless its value is exactly `false`).
   - Local state: `range: ZoomRange` (starts `fullRange(n)`, reset to full whenever `response` changes), `percentile: Percentile = 'p95'`, a drag state `{ a: number; b: number } | null`.
   - Above the first row: `<div role="toolbar" aria-label={CANVAS_TEXT.ariaLabel}>` with real buttons `Zoom in`, `Zoom out`, `Reset zoom` (disabled while not zoomed), then a `Latency line` group of two `aria-pressed` buttons from `PERCENTILES` using `PERCENTILE_LABEL`. When no visible audit bucket has a value for the chosen percentile, the group stays but a small note shows `CANVAS_TEXT.noLatency`. `dragHint` is the title text under the toolbar. A `role="status" aria-live="polite"` line shows `zoomedText(shown, total)` only while zoomed.
   - Drawing: bar `left`/`width` use the visible range, `pct(i) = 100*(i - range.from)/(range.to - range.from)`, bars outside the range are skipped, axis ticks and the shared axis row are recomputed for the range only. A latency line is a plain `<svg>` polyline over the audit panel (no library) scaled to the largest value of the chosen percentile in view, with the right axis ticks `0` and that value via `formatDuration` from `../lib/format`; gaps (null buckets) break the line.
   - Pointer drag on the panel column draws a brush rectangle while dragging (`aria-hidden`), and on release calls `rangeFromDrag`; a click without movement keeps its WP-17b meaning (select a bucket). Keyboard: the bucket selector's existing arrows still work; `+` / `-` on the focused selector call Zoom in / Zoom out, `0` resets.
   - INDEX RULE (zoom must never change what a slot index means): every slot index in the component, in a handler argument, in state and in `onSelect(slotIndex)` is ABSOLUTE, i.e. an index into the full `SlotGrid` (0 to `n-1`), never relative to the visible range. A pointer position (click, drag start/end, tooltip hover) maps to `absolute = range.from + slotAtFraction(fraction, range.to - range.from)`, with `fraction` measured across the visible panel width. The arrow keys on the bucket selector move the absolute index by one and are clamped to the visible range `[range.from, range.to - 1]` (an arrow at the edge stays on the edge). `rangeFromDrag(n, a, b)` receives the two absolute indices. After zoom in/out/reset the selected absolute index is kept; if it falls outside the new range it is cleared (no bucket stays selected off-screen). `onSelect` is only ever called with an absolute index, so WP-61 can pass it straight to the window maths.
   - Rows with `visible[key] === false` are not drawn. If every lane is hidden, show `CANVAS_TEXT.allHidden` in place of the rows.
3. Tests added to `web/tests/tracingChart.test.cjs`:
   - `zoom ranges stay inside the grid`: from `fullRange(120)`, `zoomIn` gives width 60 and repeated zoom-ins stop at 4; `zoomOut` returns to 120 and never exceeds it; a focus near either end is clamped to `[0, n)`; for `n === 3` the width is 3.
   - `drag selects whole bars in either direction`: `rangeFromDrag(120, 30, 10)` equals `{from:10,to:31}`; out-of-range indexes are clamped; a one-bar drag widens to the minimum.
   - `reset returns the full window`: after `zoomIn`, `isZoomed` is true; `fullRange(n)` makes it false; `zoomedText(60, 120)` is `Showing 60 of 120 bars.`
   - `percentile values come only from the returned bucket`: `percentileValue({p95Ms: 840, ...}, 'p95')` is 840, a bucket without `p50Ms` gives null for `p50`, negative or `NaN` gives null; `hasLatency([], 'p95')` is false.
   - `chart controls are real buttons and nothing is imported from charts`: `TracingChart.svelte` contains `role="toolbar"`, `CANVAS_TEXT.resetZoom`, `aria-pressed`, `rangeFromDrag`, `visible`; contains no `from '../lib/charts'` and no `echarts`.
   - `tooltips survive zoom`: `TracingChart.svelte` still contains `role="tooltip"`, and either `range.from + slotAtFraction(` with the `range.to - range.from` form or a call to `absoluteSlot(`, and does not map the tooltip with `slotAtFraction(` over `g.n` alone after this packet (an inline use must carry the `range.to - range.from` form).
   - `zoom keeps slot indices absolute`: `TracingChart.svelte` maps the pointer to an absolute slot in each of the click, drag-end and tooltip handlers, either inline as `range.from + slotAtFraction(` or through the exported helper `absoluteSlot(` (the grep accepts either form; count at least three matches of `range.from + slotAtFraction(|absoluteSlot(` together), contains a clamp of the arrow keys to `range.to - 1` and `range.from`, and passes the absolute index to `onSelect(` (no `- range.from` subtraction in the `onSelect` call). A pure test in `canvas.ts` terms: with `zoomIn(fullRange(120), 120)` giving `{from:30,to:90}`, a fraction 0 click is slot 30 and a fraction just under 1 is slot 89 (compute with `range.from + slotAtFraction(f, range.to-range.from)` via an exported pure helper `absoluteSlot(range, fraction)` in `canvas.ts`, and `clampArrow(range, index, delta)` for the keys: 30 with -1 stays 30, 89 with +1 stays 89, 50 with +1 gives 51).
   - `canvas copy passes the screen-string lint`: `lintScreenStrings(canvasCopyEntries(), deck.terms)` is `[]` (if a word is rejected, change only that word and add a `Note:` to `progress.md`).

## Proof
- `cd web && node --test tests/tracingChart.test.cjs tests/tracingTooltips.test.cjs` -> all pass (WP-17b tests, WP-17c tests and the eight above).
- `cd web && npm test` -> all pass; `cd web && npm run check` -> 0 errors.
- `cd web && npm run build && node --test tests/distChunks.test.cjs` -> pass (no new chunk cycle; bundle delta recorded in `progress.md`).
- Wireframe check (`prototype/tracing-paper-PROTOTYPE.html`, chart canvas): a control strip above the rows with Zoom in, Zoom out, Reset zoom and a P50 / P95 choice; dragging across bars shows a brush and then fewer, wider bars while the shared axis labels change with them; Reset zoom is greyed until zoomed and restores all bars; with no latency figures the note shows and no line is drawn. At 375 px the strip wraps. Keyboard: Tab through the buttons, `+`/`-`/`0` on the bucket selector, and the "Showing N of M bars." line is announced.

## Depends on
WP-17b, WP-17c
