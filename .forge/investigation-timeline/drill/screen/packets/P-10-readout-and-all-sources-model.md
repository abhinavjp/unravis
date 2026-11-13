# P-10 Hover readout and "all sources in time order" model

model: sonnet, effort: medium. Reason: merges three kinds of rows into one ordered list without losing "not recorded".
risk: low. Review batch: RB-1. Wave: W0. Requirements: R-10, R-13.

## Outcome
A pure module gives (a) the numbers under the hover line for the three lanes and (b) one time-ordered list of slow items and event markers from every source.

## Write scope
- `web/src/lib/drill/callReadout.ts` (new)
- `web/tests/slowcall.readout.test.cjs` (new)

## Must not change
- Nothing else. Declare local structural types; do not import `callLanes.ts` or `callData.ts` (parallel packets).

## Changes
1. Export:
```ts
export interface ReadCell { start: number; end: number; atOnce: number | null; p95Ms: number | null; refused: number | null }
export interface Readout { stepStart: number; api: ReadCell | null; iis: ReadCell | null; db: ReadCell | null; isThisCall: boolean }
export function readoutAt(tMs: number, lanes: { audit: readonly ReadCell[]; iis: readonly ReadCell[]; sql: readonly ReadCell[] }, call: { startMs: number; endMs: number }): Readout | null;
export type SourceKey = 'api' | 'iis' | 'db';
export interface SourceItem { key: string; source: SourceKey; startMs: number; endMs: number | null; what: string; tookMs: number | null; marker: boolean; thisCall: boolean }
export interface EventIn { evidenceId: string; source: string; kind: string; start: string; end?: string | null; durationMs?: number | null; severity?: string }
export function sourceOfEvent(source: string): SourceKey | null;       // 'sql-xe','sql-errorlog','sql-pdh','sql-querystore' -> 'db'; 'iis-httperr','iis-w3c','iis-freb' -> 'iis'; else null
export function eventLabel(e: EventIn): string;                          // kind with '-' turned into spaces, first letter upper case; never free text from the file
export function allSources(input: { apiRows: readonly { id: string; controller: string; action: string; initiatedAt: string; durationMs: number | null }[]; iisBars: readonly { id: string; label: string; start: number; end: number }[]; events: readonly EventIn[]; callId: string; start: number; end: number; shown: Record<SourceKey, boolean> }): SourceItem[];
```
2. Rules: `readoutAt` returns null when `tMs` is outside every lane's cells; a lane with no cell at `tMs` gives null for that lane (the UI prints "not recorded"). `allSources` keeps items that overlap `[start, end]`, drops sources switched off in `shown`, sorts by `startMs` then source order api, iis, db, and removes duplicate `key`s. Events with no duration are `marker: true` with `tookMs: null`. `eventLabel` uses only the `kind` field (no line text: record text must not reach a label).
3. Tests: readout picks the right step; a gap lane is null, not 0; `allSources` order; a switched-off source is gone; duplicate ids collapse; an unknown event source is dropped; `eventLabel({kind:'blocked-process'}) === 'Blocked process'`; the call's own row has `thisCall: true`.

## Proof
- [ ] `cd web && node --test tests/slowcall.readout.test.cjs` → pass, 0 fail, at least 10 asserts.
- [ ] `cd web && npx svelte-check --threshold error` → 0 errors.

## Depends on
Nothing.
