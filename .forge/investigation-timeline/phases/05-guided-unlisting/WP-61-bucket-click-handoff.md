# WP-61 Bucket click hand-off to the analysis
risk: medium    size: M    wave: 8    model: sonnet
## Outcome
Clicking (or pressing Enter on) a bar in the tracing chart makes that bar's time slot the primary window of the analysis, with an equal-length reference window before it, and opens the analysis view (TR-09). A bar with nothing in it opens nothing and says so in one plain sentence. The checklist, After the fix and Nearby activity are NOT rebuilt in Tracing; they stay in guided.

## Write scope
- `web/src/lib/tracing/handoff.ts` (new)
- `web/src/components/TracingPaper.svelte` (only the `onSelect` prop passed to the chart and one status line in `data-region="notes"`)
- `web/tests/tracingHandoff.test.cjs` (new)

## Must not change
- `web/src/components/TracingChart.svelte` and `web/src/lib/tracing/lanes.ts`: the chart (WP-17b, with WP-23 zoom and WP-17c tooltips) already exposes `onSelect(slotIndex)` for click and Enter; this packet only listens to it. Under zoom the chart always passes an ABSOLUTE slot index (an index into the full `SlotGrid`, WP-23 INDEX RULE), so `slotToBucket` can use it as is and must never add or subtract a zoom offset. Read both files first; do not edit them.
- `web/src/lib/tracing/model.ts` (WP-16a types). Bucket types are `TracingBucket { start, count, errors, ... }` from there; no `types.ts` exists.
- `web/src/lib/guided/*` and `web/src/lib/components/guided/*`: do not import from them (INV-8; WP-63 keeps them unlisted).
- `web/src/lib/appState.svelte.ts` (`setWindowFromHour`, `openAnalysis` are used as they are) and `web/src/lib/rollup.ts` (`hourWindowQuery`, `RollupWindow`).
- `CallTrace.svelte` (INV-7). No new dependency, no new chunk import cycle (INV-6; `web/tests/distChunks.test.cjs`).
- No `<15 ms` or `<100 ms` assertion (INV-5). No echarts, no new chart code.

## Changes
Note: `TracingPaper.svelte`, `TracingChart.svelte` and `web/src/lib/tracing/lanes.ts` are created by WP-16b, WP-17b and WP-23 (earlier waves) and do not exist in the tree today. Open them first. The chart is plain HTML/CSS: its bucket selector calls `onSelect(slotIndex)` on click and on Enter, where `slotIndex` indexes the grid `slotGrid(response.window)` (the server's axis). The client bucket model (WP-16a) keeps only `start`, `count` and `errors` (the wire also sends `end`, `clipped` and `complete`, and a first or last bucket is clipped to the request window), so the hand-off slot is the whole grid cell: its start and end come from the slot grid and the slot width, never from a bucket field.
Note: the precedent for this hand-off is the hour grid: `RollupView.svelte` passes `setWindowFromHour` to `HourHeatmap`'s `onChoose`; `setWindowFromHour(window: RollupWindow)` (`appState.svelte.ts:1463`) writes the window plus the equal-length reference window and clears the run hint; `openAnalysis()` (`appState.svelte.ts:782`) goes to the analysis screen (`go('running')`).
1. Create `web/src/lib/tracing/handoff.ts` with a one-line header comment (the checklist, After the fix and Nearby activity stay in guided and are not rebuilt here). Imports: `import type { RollupWindow } from '../rollup'`, `import type { TracingResponse, TracingLaneKey } from './model'` and `import type { LintEntry } from '../copy/copylint'` (type imports are erased, so the node test loads the file without the runes files), plus `import { slotGrid, slotStartMs, slotOutside, barsFor } from './lanes'` (pure functions from WP-17b/WP-23; read their exact signatures first). Exports:
   - `export interface HandoffBucket { start: string; end: string }` (ISO-8601 UTC, the slot's own start and end).
   - `export const HANDOFF_TEXT = { empty: 'Nothing was recorded in that bar, so there is nothing to analyse. Pick a bar with events.' }` and `handoffCopyEntries(): LintEntry[]` (ids `tracing.handoff.<key>`, same pattern as `tracingCopyEntries`).
   - `export function slotToBucket(response: TracingResponse, visible: Partial<Record<TracingLaneKey, boolean>>, slotIndex: number): HandoffBucket | null`. Build `g = slotGrid(response.window)`. Return `null` (the empty-bucket rule) when `g` is null, `slotIndex` is not an integer in `0..g.n-1`, `slotOutside(g, slotIndex)` is true, or no lane that is shown (`visible[key] !== false`) has a bar with `count > 0` at that slot (use `barsFor(g, lane.buckets)` and match on the slot index). Otherwise return `{ start: new Date(slotStartMs(g, slotIndex)).toISOString(), end: new Date(slotStartMs(g, slotIndex) + g.widthMs).toISOString() }`. The whole slot width is handed off, because the analysis window is the bar the user clicked.
   - `export function bucketToWindow(bucket: HandoffBucket | null | undefined): RollupWindow | null`: returns `{ start, end }` copied from the bucket when both parse with `Date.parse` to finite numbers and start < end; otherwise `null`. Same rule as `cellWindow` in `rollup.ts`.
   - `export interface HandoffDeps { setWindow: (window: RollupWindow) => void; open: () => void }`.
   - `export function handOffBucket(bucket: HandoffBucket | null | undefined, deps: HandoffDeps): boolean`: computes `bucketToWindow`; if `null` returns `false` and calls nothing; else calls `deps.setWindow(window)` first, then `deps.open()` once, and returns `true`.
2. `TracingPaper.svelte`: import `handOffBucket`, `slotToBucket`, `HANDOFF_TEXT` from `../lib/tracing/handoff`, and `setWindowFromHour`, `openAnalysis` from `../lib/appState.svelte`. Add `let handoffNote = $state('');`. Pass the chart `onSelect={(i) => { handoffNote = ''; const ok = result ? handOffBucket(slotToBucket(result, visible, i), { setWindow: setWindowFromHour, open: openAnalysis }) : false; if (!ok) handoffNote = HANDOFF_TEXT.empty; }}` (`result` is the response state of WP-16b and `visible` the lane toggles of WP-11). In `data-region="notes"` render `{#if handoffNote}<p role="status">{handoffNote}</p>{/if}`; clear `handoffNote` when a new run starts. Only click and Enter reach `onSelect`, so arrow keys alone open nothing.
3. Create `web/tests/tracingHandoff.test.cjs` (node:test, node:assert/strict). Load `src/lib/tracing/handoff.ts` with the recursive `loadTypeScript` helper of `web/tests/rollup.test.cjs` lines 8-25 (extend it to follow relative `.ts` imports, as `web/tests/casehome.test.cjs` does, so `./lanes` loads). Tests, by exact name:
   - `slotToBucket gives the slot start and end from the server axis`: a response whose `window` has `axisStart` `2026-10-20T09:00:00Z`, `axisEnd` `2026-10-20T10:00:00Z`, `bucketSeconds` 300 and one audit bucket `{start:'2026-10-20T09:05:00Z', count:4, errors:0}`; `slotToBucket(response, {}, 1)` is `{start:'2026-10-20T09:05:00.000Z', end:'2026-10-20T09:10:00.000Z'}`.
   - `an empty slot, an out-of-range slot or a slot outside the window gives null`: slot 2 (no bucket), `-1`, `12` (n is 12), `1.5`, and slot 0 when `window.start` is `09:07` all give `null`.
   - `a hidden lane's bars do not count`: with the only bucket on the `sql` lane and `visible = { sql: false }` the slot gives `null`; with `visible = {}` it gives the bucket.
   - `bucketToWindow returns the start and end of a valid bucket`: input `{start:'2026-10-20T09:00:00Z', end:'2026-10-20T09:05:00Z'}` gives the same pair.
   - `bucketToWindow refuses a missing, reversed, equal or unparsable bucket`: `null`, `undefined`, end before start, start equal to end, `{start:'x', end:'y'}` all give `null`.
   - `handOffBucket sets the window first and opens the analysis second, once`: stubs record call order; result `true`; order is `['set','open']`; each called once.
   - `handOffBucket does nothing for a refused bucket`: a reversed bucket and `null` give `false`, neither stub called.
   - `the paper wires the chart's onSelect to the analysis`: `src/components/TracingPaper.svelte` matches `/onSelect=/`, `/slotToBucket/`, `/handOffBucket/`, `/setWindowFromHour/` and `/openAnalysis/`; `src/components/TracingChart.svelte` matches `/onSelect/` and does not match `/onBucketClick|echarts/`.
   - `the hand-off imports nothing from the guided folders`: `handoff.ts`, `TracingChart.svelte`, `TracingPaper.svelte` contain no import path with `/guided/`.
   - `hand-off copy passes the screen-string lint`: `lintScreenStrings(handoffCopyEntries(), deck.terms)` is `[]` (load `deck` and `lintScreenStrings` as `web/tests/casehome.test.cjs` does). If the lint rejects a word, change only that word and add a `Note:` to `progress.md`.

## Proof
- `cd web; node --test tests/tracingHandoff.test.cjs` -> 10 tests pass.
- `cd web; npm test` -> exit 0, `# fail 0`.
- `cd web; npm run check` -> 0 errors.
- `cd web; npm run build; node --test tests/distChunks.test.cjs` -> pass.
- Browser check is done in the phase 05 acceptance (click a bar with events on the seeded demo; the analysis view opens on that bar's window; click an empty bar: the plain sentence shows and nothing opens).

## Depends on
WP-23, WP-60, WP-11
