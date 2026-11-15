# P-08 Nearby calls model

model: sonnet, effort: medium. Reason: filter and "ask the server" rules with a null-safe totals column.
risk: low. Review batch: RB-1. Wave: W0. Requirements: R-11, R-12, R-08.

## Outcome
A pure module decides which nearby-call rows show for the window and chip, when the server must be asked, what to ask, and what each row's "from this call" and light totals cells say.

## Write scope
- `web/src/lib/drill/nearbyCalls.ts` (new)
- `web/tests/slowcall.nearby.test.cjs` (new)

## Must not change
- `web/src/lib/guided/nearby.ts`, `web/src/lib/drill/target.ts`, `web/src/lib/types.ts` (P-11 owns it; declare a local structural row type here).

## Changes
1. Export:
```ts
export type NearbyChip = 'everyone' | 'user' | 'api';
export const NEARBY_LIMIT = 100;                         // server MaxPageSize for /api/activity
export interface NearbyRow { id: string; sourceId: string; controller: string; action: string; initiatedAt: string; durationMs: number | null; status?: number; userId?: number | null;
  light?: { sqlMs: number | null; redisMs: number | null; httpMs: number | null } | null }
export interface CallRef { id: string; sourceId: string; controller: string; action: string; userId?: number | null; startMs: number; endMs: number }
export interface Loaded { start: number; end: number; chip: NearbyChip; truncated: boolean }      // what the last server answer covered
export function chipAvailable(chip: NearbyChip, call: CallRef): boolean;      // 'user' needs a userId, 'api' needs controller and action
export function filterRows(rows: readonly NearbyRow[], chip: NearbyChip, call: CallRef, start: number, end: number): NearbyRow[];
export function needsServer(loaded: Loaded | null, chip: NearbyChip, start: number, end: number): boolean;
export function activityInput(sourceIds: readonly string[], start: number, end: number, chip: NearbyChip, call: CallRef): { sourceIds: string[]; start: string; end: string; cursor: string; limit: number; userId?: number; operation?: string };
export function gapLabel(rowStartMs: number, call: CallRef, text: { thisCall: string; during: string }): string;
export function lightCells(row: NearbyRow): { sqlMs: number | null; redisMs: number | null; httpMs: number | null } | null;   // null = whole row not recorded
export function isThisCall(row: NearbyRow, call: CallRef): boolean;
```
2. Rules:
   - `filterRows` keeps rows whose `initiatedAt` is inside `[start, end]`, then applies the chip (`user`: `row.userId === call.userId`; `api`: same controller and action, compared exactly), sorted by time ascending.
   - `needsServer` is true when `loaded` is null; when the chip differs from `loaded.chip` and `loaded.chip !== 'everyone'`; when the chip is not `everyone` and `loaded.truncated`; when `[start, end]` is not inside `[loaded.start, loaded.end]`. Otherwise false (filter in the browser).
   - `activityInput`: ISO strings, `cursor: ''`, `limit: NEARBY_LIMIT`, `userId` only for chip `user`, `operation` = `Controller.Action` only for chip `api`. Never sends a field with value `undefined`.
   - `gapLabel`: the call's own row → `text.thisCall`; a row that started while the call ran → `text.during`; otherwise a signed gap: under 60 s `+12 s` / `−8 s`, else `+3 min` / `−3 min` (true minus sign U+2212, as the wireframe).
   - `lightCells`: returns null when `row.light` is null or undefined; never replaces a null number with 0.
3. Tests: one per rule above, plus: `chipAvailable('user', call without userId) === false`; a call with `userId: 0` is a real user (0 is not "missing"); `activityInput` for `everyone` has no `userId` and no `operation` key; `needsServer` false when narrowing from ±15 to ±5 after an untruncated `everyone` read.

## Proof
- [ ] `cd web && node --test tests/slowcall.nearby.test.cjs` → pass, 0 fail, at least 14 asserts.
- [ ] `cd web && npx svelte-check --threshold error` → 0 errors.
- [ ] `Select-String -Path web/src/lib/drill/nearbyCalls.ts -Pattern "guided"` → no match.

## Depends on
Nothing (field names agreed with P-11, P-13, P-35: `userId`, `operation`, `light`).
