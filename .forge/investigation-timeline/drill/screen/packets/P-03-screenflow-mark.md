# P-03 History mark for the Slow call screen

model: haiku, effort: low. Reason: two exact additions in one small file plus a new test file.
risk: low. Review batch: RB-1. Wave: W0. Requirement: R-01.

## Outcome
`screenFlow.ts` exports `CASE_CALL_MARK` and `isCallMark`, and `parseScreenMark` treats the call mark as "case page with the timeline open".

## Write scope
- `web/src/lib/screenFlow.ts`
- `web/tests/slowcall.flow.test.cjs` (new)

## Must not change
- The return shape of `parseScreenMark` (`{ screen, tracing }` only; `web/tests/caseTimelineHistory.test.cjs` lines 40-43 use deepEqual).
- `CASE_TRACING_MARK`, `HISTORY_SCREENS`, `historyStep`, `MOVES`.

## Changes
1. In `web/src/lib/screenFlow.ts`, directly after the line `export const CASE_TRACING_MARK = 'case/tracing';` add:
```ts
/** History mark of the Slow call screen, one level under the timeline, so Back returns to the timeline. */
export const CASE_CALL_MARK = 'case/tracing/call';
/** True when a history mark stands for the Slow call screen. */
export function isCallMark(mark: string): boolean { return mark === CASE_CALL_MARK; }
```
2. Replace the body line of `parseScreenMark`
   `return mark === CASE_TRACING_MARK ? { screen: 'case', tracing: true } : { screen: mark, tracing: false };`
   with
   `return mark === CASE_TRACING_MARK || mark === CASE_CALL_MARK ? { screen: 'case', tracing: true } : { screen: mark, tracing: false };`
3. New test file: copy lines 1-24 of `web/tests/drill.copy.test.cjs` (the `loadTypeScript` preamble), load `src/lib/screenFlow.ts`, and assert:
   - `CASE_CALL_MARK === 'case/tracing/call'`
   - `isCallMark(CASE_CALL_MARK) === true`, `isCallMark(CASE_TRACING_MARK) === false`, `isCallMark('case') === false`, `isCallMark('') === false`
   - `deepEqual(parseScreenMark(CASE_CALL_MARK), { screen: 'case', tracing: true })`
   - `deepEqual(parseScreenMark(CASE_TRACING_MARK), { screen: 'case', tracing: true })`
   - `deepEqual(parseScreenMark('case'), { screen: 'case', tracing: false })`
   - `isHistoryScreen(CASE_CALL_MARK) === false`

## Proof
- [ ] `cd web && node --test tests/slowcall.flow.test.cjs` → pass, 0 fail.
- [ ] `cd web && node --test tests/caseTimelineHistory.test.cjs tests/wayfinding.test.cjs` → pass, 0 fail.
- [ ] `cd web && npx svelte-check --threshold error` → 0 errors.

## Depends on
Nothing.
