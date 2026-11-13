# P-33 Mount the screen: the diamond opens the Slow call screen, not the pop-up

model: sonnet, effort: high. Reason: four small edits in a file that about twenty tests read as text; one test file rewritten.
risk: high. Review batch: RB-4 (Opus). Wave: W4. Requirements: R-01, R-16.

## Outcome
Clicking a red diamond on the timeline opens the full-page Slow call screen. The timeline stays mounted but hidden, so Back / Esc return to it with the same window and no new read. Open in Analysis still hands off to Run Analysis.

## Write scope
- `web/src/components/TracingPaper.svelte`
- `web/tests/drill.paper.test.cjs` (rewritten for the new mount)

## Must not change
- Everything in `TracingPaper.svelte` outside the four anchors below: function order (`function cancel()` … `function onBarSelect`), lane variables, the `run()` reset `cancelled = false; drillSlot = null; controller?.abort()` (pinned by a regex: keep `drillSlot` as a variable and keep that line), the `TracingChart` line except its `onDiamond` handler, no import from `guided` or `callTrace` (`tracingPaper.test.cjs:71`).
- `CaseHome.svelte`, `TracingChart.svelte`, `DrillPopup.svelte` (file stays on disk), `drill.popup.test.cjs`, `drill.chart.test.cjs`.

## Changes
1. Before editing: `grep -n "tracing-paper\"\|DrillPopup\|drillSlot\|onDiamond" web/tests/*.cjs` and list every test line that pins text you are about to change. Only `drill.paper.test.cjs` may need changes; if another test file pins one of the anchors, STOP and report.
2. Anchor A (lines 29-33, the lazy import): replace the `DrillPopup` lazy component with `const SlowCallScreen = lazyComponent(() => import('./SlowCallScreen.svelte'));` (same `lazyComponent` helper, same style). Add `import { makeCallTarget } from '../lib/drill/callTarget';`. Keep `let drillSlot = $state<number | null>(null);`.
3. Anchor B (line 402): `<section class="tracing-paper" aria-labelledby="tp-title">` becomes `<section class="tracing-paper" aria-labelledby="tp-title" hidden={callShown}>` where, in the script next to `drillSlot`, `const callShown = $derived(!!app.slowCall && app.slowCall.caseId === caseId);`. `[hidden]` must really hide it: if `.tracing-paper` sets `display`, add `.tracing-paper[hidden] { display: none; }` to this file's style block.
4. Anchor C (line 521): the handler `onDiamond={(slot) => { drillSlot = slot; }}` becomes `onDiamond={openCall}` and a new function, placed directly after `onBarSelect` (so the pinned function order is kept):
```ts
  function openCall(slot: number) {
    if (!result) return;
    const target = makeCallTarget(caseId, result.window, slot, result.lanes.find((l) => l.key === 'audit'), result.sources);
    if (target) app.openSlowCall(target);
  }
```
5. Anchor D (lines 532-536): replace the whole `{#if drillSlot !== null && result}…{/if}` block with a sibling placed AFTER the closing `</section>` of `.tracing-paper` (a second root node; the section is hidden, so the screen cannot live inside it):
```svelte
{#if callShown && app.slowCall}
  {#key app.slowCall.slot}
    <SlowCallScreen target={app.slowCall} zone={timeZone.zone} demo={!!app.info?.demo}
      onBack={() => app.closeSlowCall()}
      onFound={(call) => app.noteSlowCall(call)}
      onOpenAnalysis={(slot) => { app.closeSlowCall(); onBarSelect(slot); }} />
  {/key}
{/if}
```
   Use the same wrapper the old block used for a lazy component (if `DrillPopup` was rendered through a `{#await}` or a `<Lazy>` helper, do the same). Check how `app` is imported in this file and that `openSlowCall`, `closeSlowCall`, `noteSlowCall`, `slowCall` are reachable on it (P-23).
6. After a reload the timeline has not run yet when the screen shows; that is fine (the screen has its own reads). When the user goes Back, the timeline must run as it does on a normal open: check that hiding the section does not stop `run()`; say what you saw.
7. `onBarSelect(slot)` after `closeSlowCall()`: `result` is still the timeline's result, so the hand-off works as before. If `result` is null (reload case), `onBarSelect` must not throw: it already guards or you report it (do not edit it).
8. Rewrite `web/tests/drill.paper.test.cjs` to pin the new mount: lazy `SlowCallScreen` import; no `<DrillPopup` in the file; `hidden={callShown}` on the section; `onDiamond={openCall}`; `function openCall` comes after `function onBarSelect`; `app.openSlowCall(target)`; the screen block comes after `</section>`; `onOpenAnalysis` closes then calls `onBarSelect(slot)`; the `run()` reset regex still matches; still no `guided|callTrace` import.

## Proof
- [ ] `cd web && node --test tests/drill.paper.test.cjs` → pass, 0 fail.
- [ ] `cd web && npm test` → 0 fail (all TracingPaper text tests still pass).
- [ ] `cd web && npx svelte-check --threshold error` → 0 errors.
- [ ] `cd web && npm run build` → builds; then `node --test tests/distChunks.test.cjs` → pass (it reads the build output; if it fails on the new lazy chunk, report the assertion, do not edit that test).
- [ ] `git diff --stat -- web/src/components/TracingPaper.svelte` → small (about 20 changed lines).
- [ ] Browser check on the demo (dev server or built exe): diamond → screen; Esc → timeline with the same window, no spinner; browser Back and Forward; reload on the screen. Report each as seen / not seen. Demo only here; live in P-46.

## Depends on
P-23, P-32. Only writer of `TracingPaper.svelte` in the plan.
