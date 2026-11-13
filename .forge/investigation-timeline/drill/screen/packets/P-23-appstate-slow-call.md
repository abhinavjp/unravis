# P-23 App state: open, close, Esc, Back and reload for the Slow call screen

model: sonnet, effort: high. Reason: navigation state in the app's most pinned file; history and reload paths.
risk: high. Review batch: RB-4 (Opus). Wave: W1. Requirements: R-01 (BL-019), integration point I-3.

## Outcome
`appState.svelte.ts` holds which slow call is open, opens it as a history step one level under the timeline, closes it on Esc / Back to the timeline, and brings it back after a reload.

## Write scope
- `web/src/lib/appState.svelte.ts`
- `web/tests/slowcall.appstate.test.cjs` (new)

## Must not change (each is pinned by a test; keep the text byte-identical)
- `let drillOpen = $state(false);`, `export function setDrillOpen(v: boolean) { drillOpen = v; }`, and `if (drillOpen) return;` standing before `stepUpOnce();` in `onWindowKey` (`drill.appstate.test.cjs`).
- `const CASE_VIEW_KEY = 'unravis.caseView';`, the try/catch inside `storedCaseView`, `openTracing`, `closeTracing`, `marker.markScreen(CASE_TRACING_MARK)`, `marker.stepBack('case')` (`caseTimelineHistory.test.cjs` 48-59).
- The line `setLedgerCase(activeCaseId, storedCaseView() === '')` (`tracingRound4.test.cjs:152`).
- The view-sync effect (lines 441-449) and the hash. No new `#/` address.

## Changes (edit by anchor; line numbers are from 2026-11-13 and may drift)
1. Imports: add `CASE_CALL_MARK, isCallMark` to the existing import from `./screenFlow`; add `import { parseCallTarget, encodeCallTarget, withCall, type SlowCallTarget } from './drill/callTarget';`.
2. After `const CASE_VIEW_KEY = 'unravis.caseView';` add `const CALL_TARGET_KEY = 'unravis.callTarget';`.
3. `storedCaseView` (line 118): widen the write type to `'case' | 'tracing' | 'call' | ''` and make the read side also return `'call'` when that is what is stored. Any other stored text still reads as `''`.
4. After `storedCaseView`, add `storedCallTarget(write?: SlowCallTarget | null): SlowCallTarget | null`: same try/catch style, `sessionStorage`; with an argument it writes `encodeCallTarget(t)` or removes the key for null; without, it returns `parseCallTarget(sessionStorage.getItem(CALL_TARGET_KEY))`.
5. After `let tracingCase = $state('');` add `let slowCall = $state<SlowCallTarget | null>(null);`.
6. After `closeTracing()` add:
```ts
  /** Opens the Slow call screen one history step under the timeline. */
  function openSlowCall(target: SlowCallTarget) {
    if (screen !== 'case' || !activeCaseId || target.caseId !== activeCaseId || tracingCase !== activeCaseId) return;
    drawers.whenClear(() => {
      slowCall = target;
      storedCallTarget(target);
      storedCaseView('call');
      marker.markScreen(CASE_CALL_MARK);
    });
  }
  /** Leaves the Slow call screen for the timeline, one level up. */
  function closeSlowCall() {
    if (!slowCall) return;
    slowCall = null;
    storedCallTarget(null);
    storedCaseView('tracing');
    if (!marker.stepBack(CASE_TRACING_MARK)) marker.markScreen(CASE_TRACING_MARK);
  }
  /** Remembers which call the screen found, so a reload does not search again. */
  function noteSlowCall(call: { sourceId: string; id: string }) {
    if (!slowCall) return;
    slowCall = withCall(slowCall, call);
    storedCallTarget(slowCall);
  }
```
   Before writing, read `openTracing` / `closeTracing` (lines 577-594) and `marker.stepBack` in `web/src/lib/wayfinding.ts`; match their argument style exactly. If `stepBack` cannot take `CASE_TRACING_MARK`, STOP and report.
7. `followHistoryMark(mark)` (line 565): after its existing lines add: when `isCallMark(mark)` and the screen is `'case'`, set `slowCall = slowCall ?? storedCallTarget()` and keep it only if its `caseId === activeCaseId`; for any other mark set `slowCall = null`. (Browser Back from the call screen lands on the tracing mark: timeline stays, call closes. Browser Forward reopens it.)
8. `setTracing` (line 571): when it sets the timeline off (empty id), also set `slowCall = null` and `storedCallTarget(null)`.
9. `chooseFirstScreen` (line 605), beside `if (remembered === 'tracing' && !deepLink) tracingCase = activeCaseId;` add: when `remembered === 'call' && !deepLink`, set `tracingCase = activeCaseId`, read `storedCallTarget()`, and set `slowCall` to it only if its `caseId === activeCaseId`; otherwise call `storedCaseView('tracing')`. Then check how the tracing mark is put back on reload for `'tracing'` and do the same for the call mark (two marks: tracing, then call) so Back still climbs one level. If the tracing reload path sets no mark, set none here and say so in the report.
10. `onWindowKey` (lines 1599-1618): after the drawers check and directly before `stepUpOnce();` add `if (slowCall) { closeSlowCall(); return; }`. The `if (drillOpen) return;` line stays above it, untouched.
11. Expose on the `app` object next to `tracingCase` (line 1789): getter `get slowCall() { return slowCall; }`, and export `openSlowCall`, `closeSlowCall`, `noteSlowCall` the way `openTracing` is exposed.
12. New test (text test of the source, like `drill.appstate.test.cjs`): asserts the presence of `CALL_TARGET_KEY = 'unravis.callTarget'`; `function openSlowCall`, `function closeSlowCall`, `function noteSlowCall`; `marker.markScreen(CASE_CALL_MARK)` inside `openSlowCall`; `marker.stepBack(CASE_TRACING_MARK)` inside `closeSlowCall`; in `onWindowKey` the order `if (drillOpen) return;` < `if (slowCall) { closeSlowCall(); return; }` < `stepUpOnce();`; `target.caseId !== activeCaseId` guard; `storedCallTarget` body has `try` and `catch`; the file has no `#/call`.

## Proof
- [ ] `cd web && node --test tests/slowcall.appstate.test.cjs` → pass, 0 fail.
- [ ] `cd web && node --test tests/drill.appstate.test.cjs tests/caseTimelineHistory.test.cjs tests/tracingRound4.test.cjs tests/wayfinding.test.cjs` → pass, 0 fail.
- [ ] `cd web && npx svelte-check --threshold error` → 0 errors.
- [ ] `cd web && npm test` → 0 fail.
- [ ] Report says what happens to history marks on reload (step 9), with file:line.
- [ ] Behaviour is not browser-verified here; e2e SCALL-02/03 (P-42) and P-46 do that.

## Depends on
P-03, P-04. Only writer of `appState.svelte.ts` in the whole plan.
