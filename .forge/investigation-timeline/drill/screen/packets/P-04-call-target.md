# P-04 Slow call target (what the screen opens on)

model: sonnet, effort: medium. Reason: strict parser for data that comes back from session storage.
risk: medium. Review batch: RB-1. Wave: W0. Requirement: R-01.

## Outcome
A pure module builds the screen's target from a clicked timeline slot, and encodes and strictly parses it for session storage.

## Write scope
- `web/src/lib/drill/callTarget.ts` (new)
- `web/tests/slowcall.target.test.cjs` (new)

## Must not change
- `web/src/lib/drill/target.ts` (reuse `slotWindow`, `drillSourceIds`; do not edit).

## Changes
1. Types and functions to export:
```ts
export interface SlowCallTarget {
  caseId: string;          // /^[a-z2-7]{26}$/
  slot: number;            // absolute grid index of the clicked diamond, integer >= 0
  start: string;           // slot start, ISO
  end: string;             // slot end, ISO
  sourceIds: string[];     // 0..8 audit source ids, each 1..200 chars
  call?: { sourceId: string; id: string };   // set once the call is found, so a reload skips the search
}
export function makeCallTarget(caseId: string, window: TracingWindow, slot: number, lane: TracingLane | undefined, sources: readonly TracingSourceRef[]): SlowCallTarget | null;
export function withCall(target: SlowCallTarget, call: { sourceId: string; id: string }): SlowCallTarget;
export function encodeCallTarget(target: SlowCallTarget): string;      // JSON
export function parseCallTarget(text: string | null): SlowCallTarget | null;
```
2. `makeCallTarget` returns null when `lane` is undefined, `slotWindow(window, slot)` is null, or the case id fails the pattern. It uses `drillSourceIds(lane, sources)`.
3. `parseCallTarget` returns null (never throws) for: null, text longer than 8000 chars, invalid JSON, non-object, bad case id, non-integer or negative slot, `start`/`end` not parseable by `Date.parse` or `end <= start`, `sourceIds` not an array of ≤ 8 non-empty strings ≤ 200 chars, a `call` whose `sourceId` or `id` is not a non-empty string ≤ 200 chars. Unknown keys are dropped, not kept.
4. Imports: types from `../tracing/model`, functions from `./target`. No import from `guided` or `callTrace`.
5. Test (copy the `loadTypeScript` preamble from `web/tests/drill.copy.test.cjs` lines 1-24; build a window fixture the way `web/tests/drill.target.test.cjs` does): round trip `parse(encode(t))` deepEquals `t`; each rejection in step 3 has one assert; `withCall` does not mutate its input; `makeCallTarget` returns null for an undefined lane; an object with an extra key `x` parses without `x`.

## Proof
- [ ] `cd web && node --test tests/slowcall.target.test.cjs` → pass, 0 fail, at least 12 asserts.
- [ ] `cd web && node --test tests/drill.target.test.cjs` → still passes.
- [ ] `cd web && npx svelte-check --threshold error` → 0 errors.
- [ ] `Select-String -Path web/src/lib/drill/callTarget.ts -Pattern "guided|callTrace"` → no match.

## Depends on
Nothing.
