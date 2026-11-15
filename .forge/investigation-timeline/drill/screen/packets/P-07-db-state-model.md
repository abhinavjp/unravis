# P-07 Database lane state model

model: sonnet, effort: medium. Reason: a decision table with eight outcomes and a demo override.
risk: medium. Review batch: RB-1. Wave: W1. Requirements: R-05, R-06, R-17.

## Outcome
A pure module decides which of the database states the lane is in, and returns the source label, the sentence and the short lane line, from data the app already has.

## Write scope
- `web/src/lib/drill/dbState.ts` (new)
- `web/tests/slowcall.dbstate.test.cjs` (new)

## Must not change
- `drillCopy.ts` (read only; if P-02 has not landed yet, write the module against the key names in `P-02-copy-keys.md` and run the test after P-02).

## Changes
1. Export:
```ts
export type DbStateKey = 'qs' | 'qsfile' | 'api' | 'off' | 'ro' | 'late' | 'denied' | 'events' | 'notread';
export const DB_STATE_KEYS: readonly DbStateKey[];     // the 9 keys in this order
export interface QueryStoreInfo { state: 'on' | 'off' | 'read-only' | 'denied' | 'unknown'; firstData?: string | null; intervalMinutes?: number | null }
export interface DbStateInput {
  laneReasonCode: string;           // tracing sql lane reasonCode: 'held-sql' | 'layered-sql' | 'qs-file' | 'api-seen' | '' ...
  laneHasShape: boolean;            // from callLanes buildLane
  laneAsOf: string | null;          // tracing lane asOf
  blockSeconds: number | null;
  queryStore: QueryStoreInfo | null;   // newest saved SQL lane day, null when none
  hasEvents: boolean;               // imported sql-xe / sql-errorlog events in the window
  windowStart: number; windowEnd: number;
  pretend?: DbStateKey | null;      // demo switch; wins over everything
}
export interface DbStateView { key: DbStateKey; source: string; message: string; short: string; shape: 'blocks' | 'apiseen' | 'markers' | 'none' }
export function dbState(input: DbStateInput, fmt: { date: (iso: string) => string; time: (iso: string) => string }): DbStateView;
```
2. Decision order (first match wins): `pretend` → that key. `queryStore.state === 'denied'` → `denied`. `'off'` → `off` (if `hasEvents`, `events`). `laneReasonCode === 'qs-file'` and `laneHasShape` → `qsfile`. `laneReasonCode` is `held-sql` or `layered-sql` and `laneHasShape`: if `queryStore.firstData` is after `windowStart` → `late`; if `queryStore.state === 'read-only'` → `ro`; else `qs`. `laneReasonCode === 'api-seen'` and `laneHasShape` → `api`. `hasEvents` → `events`. Otherwise `notread`.
3. Text per key (all from `DRILL_TEXT`): `qs` → `dbSrcLive`, `dbMsgLive(blockSeconds/60 or 15)`, short `laneBlocks(m)`; `qsfile` → `dbSrcFile(date(laneAsOf))`, `dbMsgFile(date)`, `laneBlocks(m)`; `api` → `dbSrcApi`, `dbMsgApi`, `laneApiOnly`; `off` → `dbSrcNone`, `dbMsgOff`, `dbShortOff`; `ro` → `dbSrcReadOnly`, `dbMsgReadOnly`, `laneBlocks(m)`; `late` → `dbSrcLate(time(firstData))`, `dbMsgLate(time)`, `dbShortLate`; `denied` → `dbSrcNone`, `dbMsgDenied`, `dbShortDenied`; `events` → `dbSrcEvents`, `dbMsgEvents`, `dbShortEvents`; `notread` → `dbSrcNone`, `dbMsgNotRead`, `notRecorded`.
4. A pretended state with no real data to format uses fixed sample values: date `'11 Nov 2026'`, time `'10:30'`, 15-minute blocks.
5. Tests: one assert per key through real inputs; `pretend` wins over a real `qs`; `off` with events gives `events`; `late` only when `firstData > windowStart`; `shape` is `none` for `off`, `denied`, `notread`.

## Proof
- [ ] `cd web && node --test tests/slowcall.dbstate.test.cjs` → pass, 0 fail, at least 12 asserts.
- [ ] `cd web && npx svelte-check --threshold error` → 0 errors.
- [ ] No string literal sentence in `dbState.ts` (all text comes from `DRILL_TEXT`): `Select-String -Path web/src/lib/drill/dbState.ts -Pattern "Query Store"` → no match.

## Depends on
P-02 for the test run (keys). Field names agreed with P-16 (`queryStore`).
