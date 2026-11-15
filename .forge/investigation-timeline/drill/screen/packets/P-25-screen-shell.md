# P-25 Slow call screen shell: header, states, "This call" strip

model: sonnet, effort: medium. Reason: a new component with loading, not-found and failed states and real deps wiring.
risk: medium. Review batch: RB-5. Wave: W2. Requirements: R-01, R-16.

## Outcome
`SlowCallScreen.svelte` exists as a full-page screen: breadcrumb, Back to timeline, title, Open in Analysis, loading / not found / failed states, and the existing call overview. The lane area is an empty placeholder that P-32 fills.

## Write scope
- `web/src/components/SlowCallScreen.svelte` (new)
- `web/tests/slowcall.screen.test.cjs` (new)

## Must not change
- `DrillOverview.svelte`, `DrillPopup.svelte`, `TracingPaper.svelte`, `appState.svelte.ts`. No import from `guided` or `callTrace`.

## Changes
1. Props: `{ target: SlowCallTarget; zone: string; demo: boolean; onBack: () => void; onOpenAnalysis: (slot: number) => void; onFound: (call: { sourceId: string; id: string }) => void }`.
2. Build the real `CallDeps` here: `api.slowest`, `api.record`, `api.activity` from `../lib/api`; `fetchCallEvents` from `../lib/drill/callData`; `tracing` = a function that calls `fetchTracing(caseId, body, nextRequestSeq(), signal)`. For `body`, read `TracingPaper.svelte` `run()` and build `incident` (the asked range) and `reference` the same way it does for a plain read with no comparison; write in the report what you used for `reference`.
3. `let state = $state<CallState | null>(null)`; create the controller in an `$effect` keyed on `target.caseId + target.slot`, `start()` it, `dispose()` on cleanup. Pass `onFound` through.
4. Markup (root is not a dialog; no focus trap):
```svelte
<section class="slow-call" data-testid="slow-call-screen" aria-labelledby="scall-title">
  <header class="scall-head">
    <nav class="scall-crumb" aria-label="Breadcrumb"><button type="button" class="link" data-testid="scall-back" onclick={onBack}>{DRILL_TEXT.crumbTimeline}</button> <span aria-hidden="true">›</span> <span>{DRILL_TEXT.crumbCall(time)}</span></nav>
    <h2 id="scall-title" tabindex="-1">{DRILL_TEXT.title}</h2>
    <button type="button" class="btn outlined" data-testid="scall-analysis" onclick={() => onOpenAnalysis(target.slot)}>{DRILL_TEXT.openAnalysis}</button>
    <button type="button" class="btn outlined" data-testid="scall-back-btn" onclick={onBack}>{DRILL_TEXT.backToTimeline} <kbd>{DRILL_TEXT.escHint}</kbd></button>
  </header>
  ... states ...
  <div class="scall-body" data-testid="scall-body"><!-- P-32 --></div>
</section>
```
   `time` = `formatTime(record.initiatedAt, { zone, precision: 'second' })` once the record is known, else the slot start. Check `formatTime`'s option names in `web/src/lib/time.ts`.
5. States: `finding` → `<p role="status" data-testid="scall-finding">{DRILL_TEXT.finding}</p>`; `not-found` → `DRILL_TEXT.notFound`; `failed` → `DRILL_TEXT.loadFailed` + a `DRILL_TEXT.retry` button calling `controller.retry()`; `ready` → `<DrillOverview record={state.record} />` inside `<div class="scall-thiscall" data-testid="scall-thiscall">`.
6. On mount move focus to the `h2` (so a screen reader hears the new screen and Esc is handled by the window listener in `appState`). Buttons are outlined (owner ruling Oct 27). Use tokens from `web/src/brand/tokens.css`; no new colour literals.
7. Text test: file contains `data-testid="slow-call-screen"`, `scall-back`, `scall-analysis`, `DRILL_TEXT.crumbCall(`, `<DrillOverview`, `createCallController(`, `dispose()`, has no `role="dialog"`, no `aria-modal`, no `guided`, no hard-coded English sentence in markup (regex `>[A-Z][a-z]+ [a-z]+ [a-z]+<` has no match).

## Proof
- [ ] `cd web && node --test tests/slowcall.screen.test.cjs` → pass, 0 fail.
- [ ] `cd web && npx svelte-check --threshold error` → 0 errors.
- [ ] `cd web && npm test` → 0 fail.
- [ ] Not browser-verified here (not mounted until P-33).

## Depends on
P-02, P-04, P-11, P-12, P-24.
