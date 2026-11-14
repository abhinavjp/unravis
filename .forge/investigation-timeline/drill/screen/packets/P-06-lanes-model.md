# P-06 Lanes model (running at once, gaps, bars)

model: sonnet, effort: high. Reason: the core calculation; gaps must stay gaps; block regrouping for the database lane.
risk: medium. Review batch: RB-1. Wave: W0. Requirements: R-03, R-04.

## Outcome
A pure module turns a tracing response into three drawable lanes: one value per time step ("running at once" or null for "not recorded"), the peak, and slow items packed into at most 3 bar rows.

## Write scope
- `web/src/lib/drill/callLanes.ts` (new)
- `web/tests/slowcall.lanes.test.cjs` (new)

## Must not change
- `web/src/lib/tracing/model.ts` (P-11 owns it). This module declares its own minimal input interfaces so it does not wait for P-11.

## Changes
1. Input shapes (structural, a `TracingResponse` satisfies them once P-11 lands):
```ts
export interface LaneBucketIn { start: string; count: number; complete?: boolean; busyMs?: number | null; p95Ms?: number | null; errors?: number | null; clientErrors?: number | null }
export interface LaneEventIn { id: string; label: string; start: string; end: string; durationMs: number | null; kind?: string }
export interface LaneIn { key: 'audit' | 'sql' | 'iis'; state: string; reasonCode?: string; blockSeconds?: number; buckets: LaneBucketIn[]; events: LaneEventIn[]; hidden?: number }
export interface WindowIn { axisStart: string; bucketSeconds: number }
```
2. Output and functions:
```ts
export interface LaneCell { start: number; end: number; atOnce: number | null; p95Ms: number | null; refused: number | null }
export interface LaneBar { id: string; label: string; start: number; end: number; row: number; kind: 'bar' | 'marker' }
export interface CallLane { key: 'audit' | 'sql' | 'iis'; cells: LaneCell[]; peak: { atOnce: number; start: number } | null; bars: LaneBar[]; moreBars: number; blockSeconds: number | null; hasShape: boolean }
export const MAX_BAR_ROWS = 3;
export function atOnce(busyMs: number | null | undefined, stepSeconds: number): number | null;
export function laneCells(lane: LaneIn, win: WindowIn, fromMs: number, toMs: number): LaneCell[];
export function regroupBlocks(cells: LaneCell[], blockSeconds: number): LaneCell[];
export function packBars(items: { id: string; label: string; start: number; end: number }[], maxRows?: number): { bars: LaneBar[]; more: number };
export function buildLane(lane: LaneIn | undefined, key: 'audit' | 'sql' | 'iis', win: WindowIn, fromMs: number, toMs: number): CallLane;
export function formatAtOnce(n: number): string;   // one decimal under 10 ('4.2'), whole number from 10 up, '0' for 0
```
3. Rules:
   - `atOnce` = `busyMs / (stepSeconds * 1000)`; returns null when `busyMs` is null or undefined or `stepSeconds <= 0`. A bucket with `count === 0` and `complete !== false` in a lane whose state is `events` gives `0` (the source was read and saw nothing). A missing bucket, a bucket with `complete === false`, or a lane whose state is not `events` gives null.
   - `laneCells` returns one cell per step between `fromMs` and `toMs` (aligned to `axisStart`), in order; steps with no bucket are null cells. `refused` = `(clientErrors ?? 0) + (errors ?? 0)` when either is present, else null.
   - `regroupBlocks`: sum `atOnce * stepLength` inside each block and divide by the block length; a block with only null cells stays null; every cell in a block gets the block value (the lane moves in steps).
   - `buildLane`: for key `sql` with `blockSeconds > step`, regroup. `peak` ignores null cells; null when no number.
   - Bars: an event becomes a bar `[end - durationMs, end]` for key `iis` (the IIS log time is the request end), `[start, end]` otherwise; an event with null or 0 duration is a `marker` at `start`. `packBars` sorts by start and puts each bar in the first row whose last bar ends before it starts; bars that need a row ≥ `maxRows` are dropped and counted in `more`. Longest items win a place first.
   - `hasShape` is false when every cell is null.
4. Tests: `atOnce(120000, 60) === 2`; null stays null (never 0); read-and-empty bucket is 0; missing bucket is null; regroup of 15 one-minute cells where one holds 900 000 ms busy gives 1 in all 15; block of nulls stays null; packing 5 overlapping bars gives 3 rows and `more === 2`; IIS bar is end-anchored; `formatAtOnce(4.24) === '4.2'`, `formatAtOnce(12.6) === '13'`; a lane with state `no-data` has `hasShape === false`.

## Proof
- [ ] `cd web && node --test tests/slowcall.lanes.test.cjs` → pass, 0 fail, at least 14 asserts.
- [ ] One test is named `not recorded is never zero` and passes.
- [ ] `cd web && npx svelte-check --threshold error` → 0 errors.

## Depends on
Nothing (field names agreed with P-11 and P-14: `busyMs`, `blockSeconds`).
