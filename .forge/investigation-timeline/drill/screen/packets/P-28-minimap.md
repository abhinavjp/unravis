# P-28 Overview strip (minimap) for beginners

model: sonnet, effort: medium. Reason: drag with pointer capture, keyboard move, plain-language caption.
risk: medium. Review batch: RB-5. Wave: W1. Requirements: R-02, R-14.

## Outcome
`CallMinimap.svelte` shows the two hours around the call as three small rows, with an outlined box for the visible window that can be dragged or moved with the arrow keys, a red line for the call, peak labels, and a caption that explains it in plain words.

## Write scope
- `web/src/components/CallMinimap.svelte` (new)
- `web/tests/slowcall.minimap.test.cjs` (new)

## Must not change
- Nothing else. Geometry from `web/src/lib/drill/callMap.ts`; text from `DRILL_TEXT`.

## Changes
1. Props: `{ rows: MapRow[]; overview: { start: number; end: number }; win: { start: number; end: number }; call: { startMs: number; endMs: number }; bucketSeconds: number; zone: string; hidden: boolean; onPreview: (centreMs: number) => void; onCommit: (centreMs: number) => void }`.
2. `hidden === true` (the big chart already shows the whole strip): render only `<p class="scall-map-hidden" data-testid="scall-map-hidden">{DRILL_TEXT.mapHidden}</p>`.
3. Otherwise `<div class="scall-map" data-testid="scall-map">`:
   - one row per `MapRow`: a short label (`laneApi` / `laneIis` / `laneDb`), columns as `<rect>` with height = `height` × row height; null columns hatched; each column has a `<title>` = `mapCell(lane, n, time)` or `mapCellNone(lane, time)`; right of the row `mapPeak(n, time)` or `mapNoData`.
   - red line at `fractionOf(overview, call.startMs)` with the label `mapThisCall(time)`.
   - the box: `<div class="scall-map-box" data-testid="scall-map-box" role="slider" tabindex="0" aria-label={DRILL_TEXT.mapBoxLabel} aria-valuemin="0" aria-valuemax="100" aria-valuenow={…} aria-valuetext={from + ' to ' + to}>` positioned from `boxOf(overview, win)`, with the small word `mapDrag` inside.
4. Drag: `pointerdown` on the box → `setPointerCapture`; `pointermove` → `onPreview(centreFromFraction(...))` (no read happens: the controller's `preview`); `pointerup` / `pointercancel` → `onCommit(centre)`. Click on the strip outside the box → `onCommit` centred there. Keys on the box: ArrowLeft / ArrowRight move by `KEY_STEP_FRACTION` and commit; Home → centre on the call.
5. Caption under the strip: `<p data-testid="scall-map-caption">{DRILL_TEXT.mapCaption(columnMinutes(bucketSeconds))}</p>` and a `<details><summary>{DRILL_TEXT.mapHow}</summary><p>{DRILL_TEXT.mapCaptionMore}</p></details>`.
6. Height: the whole strip is at most 84 px tall (three rows of 20 px plus labels); no inner scroll. `touch-action: none` on the box only.
7. Text test: testids `scall-map`, `scall-map-box`, `scall-map-caption`, `scall-map-hidden` present; `role="slider"`; `setPointerCapture`; `onPreview(` appears in the move handler and `onCommit(` in the up handler; `mapCaption(columnMinutes(`; no `overflow: auto|scroll`.

## Proof
- [ ] `cd web && node --test tests/slowcall.minimap.test.cjs` → pass, 0 fail.
- [ ] `cd web && npx svelte-check --threshold error` → 0 errors.
- [ ] Not browser-verified here; P-42 (SCALL-05) drags it, P-46 checks it on live data.

## Depends on
P-02, P-09.
