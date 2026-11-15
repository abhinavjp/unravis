# P-27 The three lanes

model: sonnet, effort: high. Reason: the main chart: shape, gaps, bars, markers, hover and pin, fixed heights.
risk: medium. Review batch: RB-5. Wave: W1. Requirements: R-03, R-04, R-10.

## Outcome
`CallLanes.svelte` draws the API, web server and database lanes on one time axis: the faint "running at once" shape, hatched gaps, the red column for the call, slow items as bars, event markers, a hover line with a readout, and the legend. All three lanes have a fixed height and never scroll inside.

## Write scope
- `web/src/components/CallLanes.svelte` (new)
- `web/tests/slowcall.lanesui.test.cjs` (new)

## Must not change
- `TracingChart.svelte` (do not import or edit it). `web/src/brand/tokens.css` (use `--lane-audit`, `--lane-iis`, `--lane-sql`; a test requires each to appear exactly 3 times in that file, so add nothing there).

## Changes
1. Props:
```ts
{ lanes: { audit: CallLane; iis: CallLane; sql: CallLane }; start: number; end: number; stepSeconds: number;
  call: { startMs: number; endMs: number }; db: DbStateView; markers: SourceItem[];   // markers: items with marker === true
  shown: { api: boolean; iis: boolean; db: boolean }; zone: string; loading: boolean;
  selectedKey: string | null; onSelect: (key: string) => void; onReadout: (r: Readout | null, pinned: boolean) => void }
```
2. Structure: `<div class="scall-lanes" data-testid="scall-lanes" aria-busy={loading}>`, then for each shown lane `<div class="scall-lane" data-lane="audit|iis|sql" data-testid="scall-lane-{key}">` holding: a head line (name from `laneApi`/`laneIis`/`laneDb`, for the database lane `laneSource(db.source)` in `data-testid="scall-db-source"`, then `laneSlow(n)` or `laneNothingSlow`, then `laneShape(formatAtOnce(peak))` or `db.short`), and one `<svg>` with `preserveAspectRatio="none"`.
3. In each svg, back to front: hatched `<rect class="gap">` for every run of null cells (pattern fill, one shared `<pattern id>` per component instance); the shape as a step path through the cells (`fill` the lane colour at low opacity; for the database lane the steps are block-wide because the model already regrouped them); the red call column from `call.startMs` to `call.endMs` (minimum 2 px wide, `class="thiscall"`); bars as `<rect class="bar" data-key>` in rows (`row` from the model), each a `<g role="button" tabindex="0">` with `aria-label` = label and duration, Enter/Space/click → `onSelect(key)`; markers as small diamonds on the baseline. `laneMoreBars(n)` text under the lane when `moreBars > 0`.
4. Database lane with `db.shape === 'none'`: no shape, the whole lane hatched, and `db.short` printed in the middle; `'markers'`: hatched plus markers; never a flat zero line.
5. One time axis under the last lane (5 to 7 ticks, `formatTime` with the zone). One vertical hover line across all lanes: pointer move → `readoutAt` (import from `callReadout`) → `onReadout(r, false)`; click → pinned, `onReadout(r, true)`; pointer leave → back to the pinned one or null. Keyboard: the lanes container is focusable; Left/Right move the pinned line one step.
6. Readout block under the axis (`data-testid="scall-readout"`, `aria-live="polite"`): `readTitle`, then `readApi(n)`, `readP95(d)`, `readIis(n)`, `readRefused(n)`, `readDb(n)` or `readDbNone`; hint `readHover` / `readPinned`. A null value prints `DRILL_TEXT.notRecorded`, never 0.
7. Legend (`data-testid="scall-legend"`): `legendWidth`, `legendShape`, `legendHatched`, `legendCall`.
8. Height rule (CSS in this file): `.scall-lane { height: var(--scall-lane-h, 96px); overflow: hidden; }` `.scall-lanes { overflow: visible; }` No `overflow: auto|scroll`, no `max-height` with scroll, anywhere in this file. P-32 sets `--scall-lane-h` per viewport.
9. Text test: three `data-lane` values present; `class="gap"`, `class="thiscall"`, `scall-readout`, `scall-legend`, `scall-db-source` present; uses `var(--lane-audit)`, `var(--lane-iis)`, `var(--lane-sql)`; regex `overflow(-y)?:\s*(auto|scroll)` has no match; `DRILL_TEXT.notRecorded` is used; no import of `TracingChart`.

## Proof
- [ ] `cd web && node --test tests/slowcall.lanesui.test.cjs` → pass, 0 fail.
- [ ] `cd web && node --test tests/tracingTokens.test.cjs` (or the test that counts lane tokens; find with `grep -ln "lane-audit" web/tests`) → pass.
- [ ] `cd web && npx svelte-check --threshold error` → 0 errors.
- [ ] Not browser-verified here; P-32 mounts it, P-42 and P-46 measure it.

## Depends on
P-02, P-06, P-10. Reads the type `DbStateView` by name from `dbState.ts`; if P-07 has not landed, declare the same shape locally and switch to the import in P-32.
