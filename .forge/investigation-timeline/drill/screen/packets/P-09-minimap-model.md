# P-09 Overview strip (minimap) model

model: sonnet, effort: medium. Reason: small geometry with clamping; keyboard steps.
risk: low. Review batch: RB-1. Wave: W0. Requirement: R-14.

## Outcome
A pure module turns lane cells into strip rows (each scaled to its own busiest column, gaps kept as gaps) and converts between pointer position and window position.

## Write scope
- `web/src/lib/drill/callMap.ts` (new)
- `web/tests/slowcall.map.test.cjs` (new)

## Must not change
- Nothing else. Declare a local `MapCell { start: number; end: number; atOnce: number | null }` type; do not import `callLanes.ts` (parallel packet).

## Changes
1. Export:
```ts
export interface MapColumn { start: number; end: number; height: number | null }      // 0..1 of the row's own peak; null = not recorded
export interface MapRow { key: 'audit' | 'iis' | 'sql'; columns: MapColumn[]; peak: { atOnce: number; start: number } | null }
export function mapRow(key: MapRow['key'], cells: readonly MapCell[]): MapRow;
export function boxOf(overview: { start: number; end: number }, win: { start: number; end: number }): { left: number; width: number };   // fractions 0..1, clamped
export function fractionOf(overview: { start: number; end: number }, tMs: number): number | null;   // null when outside
export function centreFromFraction(overview: { start: number; end: number }, fraction: number): number;   // ms
export function columnMinutes(bucketSeconds: number): number;      // 60 -> 1, 300 -> 5, rounds up, min 1
export const KEY_STEP_FRACTION = 0.05;                               // arrow key moves the box 5% of the strip
```
2. Rules: `mapRow` divides by the row's own maximum; a row whose maximum is 0 gives height 0 for numbers and keeps null for gaps; `peak` is null when no number. `boxOf` clamps `left` to `[0, 1 - width]` and `width` to `(0, 1]`. A minimum drawn height is a CSS matter, not this module's.
3. Tests: scaling to own peak; null stays null; all-zero row; all-null row has `peak === null`; `boxOf` for a window equal to the overview is `{left: 0, width: 1}`; a window hanging off the right end is clamped; `fractionOf` outside is null; `columnMinutes(60) === 1`, `(300) === 5`, `(90) === 2`.

## Proof
- [ ] `cd web && node --test tests/slowcall.map.test.cjs` → pass, 0 fail, at least 10 asserts.
- [ ] `cd web && npx svelte-check --threshold error` → 0 errors.

## Depends on
Nothing.
