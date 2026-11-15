# P-11 Web wire types for the new fields

model: haiku, effort: low. Reason: optional fields added to four interfaces by anchor; no logic.
risk: low. Review batch: RB-1. Wave: W0. Requirements: R-04, R-08, R-11 (integration points I-1, I-2, I-4, I-5).

HARD LIMITS: edit only the three files below. Add optional fields only. Do not rename, reorder or reformat anything. If an interface named below is not found, STOP and report.

## Outcome
The TypeScript types know the optional fields the backend packets add: `busyMs`, `blockSeconds`, `userId`, `operation`, `light`, `queryStore`.

## Write scope
- `web/src/lib/tracing/model.ts`
- `web/src/lib/types.ts`
- `web/src/lib/tracing/lanes.ts`

## Must not change
- Any existing field, any function, any import.

## Changes
1. `web/src/lib/tracing/model.ts`: find the interface that describes one tracing bucket (it has `p95Ms` and `clipped`). Add as its last field:
   `  /** Total time of the calls that started in this step, in ms. Missing = not recorded. */`
   `  busyMs?: number | null;`
2. Same file: find the interface that describes one lane (it has `reasonCode` and `buckets`). Add as its last field:
   `  /** Length in seconds of the blocks this lane was saved in (Query Store). Missing = same as the step. */`
   `  blockSeconds?: number;`
3. `web/src/lib/types.ts`: find `export interface ActivityInput` (about line 181). Add as its last fields:
   `  userId?: number;`
   `  operation?: string;`
4. Same file: find `export interface RequestRecord` (about line 166). Add as its last field:
   `  /** Per-call time totals, when the audit record stores them. null or missing = not recorded. */`
   `  light?: { sqlMs: number | null; redisMs: number | null; httpMs: number | null } | null;`
5. `web/src/lib/tracing/lanes.ts`: find `export interface LaneDay`. Its `lane` field has a type (an interface in this file or imported into it). In THAT interface, if it is declared in `lanes.ts`, add as last field:
   `  queryStore?: { state: 'on' | 'off' | 'read-only' | 'denied' | 'unknown'; firstData?: string | null; intervalMinutes?: number | null } | null;`
   If the type is declared in another file, STOP and report the file name; do not edit it.

## Proof
- [ ] `cd web && npx svelte-check --threshold error` → 0 errors.
- [ ] `cd web && npm test` → 0 fail (count unchanged or higher than 2779).
- [ ] `git diff --stat -- web/src/lib/tracing/model.ts web/src/lib/types.ts web/src/lib/tracing/lanes.ts` shows only added lines (no deletions).

## Depends on
Nothing.

REMINDER: three files only, additions only, no commit.
