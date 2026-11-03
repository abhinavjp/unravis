# WP-16b Tracing shell and case-home entry
risk: medium    size: M    wave: 3    model: sonnet

## Outcome
The case home has an "Investigation Timeline" block with an "Open the Investigation Timeline" button. It opens a new `TracingPaper` screen that sends one tracing request, shows a loading state with Cancel, shows a plain error with "Try again", and has empty named regions for windows, toolbar and chart that later packets fill (TR-01, TR-12). Focus returns to the opening button on close.
Split of spec WP-16 (reported to the caller): runs after WP-16a. This packet owns the one case-home label set (`home-tracing`: heading "Investigation Timeline", button "Open the Investigation Timeline"); phase 05 WP-64 keeps it and only removes the Guided section.

## Write scope
- `web/src/components/TracingPaper.svelte` (new)
- `web/src/lib/components/CaseHome.svelte`
- `web/tests/tracingPaper.test.cjs` (new)

## Must not change
- `web/src/components/CallTrace.svelte` and `web/src/lib/callTraceTray.svelte.ts` (INV-7: the per-call tray stays as is).
- The Analysis block, the Guided block (`home-guided` section, `GuidedAnalysis` mount, `guidedOpen`/`guidedFor`) stay and stay listed; phase 05 (WP-64) removes only the Guided entry section later (INV-8).
- `web/tests/defects1.test.cjs` and `web/tests/p3.fix.test.cjs` (existing; this packet must keep them passing, which is why the entry button is `secondary-button`: `defects1.test.cjs` line 67 expects exactly one `class="primary-button"` between `home-analysis` and `home-erase`). WP-64 edits the `p3.fix` assertion, not this packet.
- No import from `web/src/lib/guided/` or `web/src/lib/callTrace.ts` inside `TracingPaper.svelte`.
- No new static import cycle: `TracingPaper.svelte` is loaded only through `lazyComponent` (`web/src/lib/lazy.ts`), as `App.svelte` does for its panels (INV-6).

## Changes
Note: the spec anchors CaseHome at lines 460-463; in the code the Analysis block starts at `<section class="block" aria-labelledby="home-analysis">` (about line 466) and the Guided block follows it. Anchor on those symbols, not line numbers.
1. `web/src/components/TracingPaper.svelte`. Props: `{ caseId: string; closed?: boolean; readOnly?: boolean; onClose: () => void }`. Imports from WP-16a: `fetchTracing`, `nextRequestSeq`, `isStale`, `tracingErrorText`, types from `../lib/tracing/model`; `TRACING_TEXT` from `../lib/copy/tracingCopy`.
   - State: `status: 'idle' | 'loading' | 'ready' | 'error'`, `result: TracingResponse | null`, `errorText: string`, `latestSeq: number`, one `AbortController`.
   - Lane colours (owned here so every sibling can read them): the `<section>` root has class `tracing-paper` and a `<style>` block defining `--lane-audit`, `--lane-sql`, `--lane-iis` on `.tracing-paper`. The app is dark by default (`web/src/brand/tokens.css`), so the base values are dark: `#3fd6b0`, `#c58bd6`, `#e3a23c`; the light values `#0b6b57`, `#9956a1`, `#ad6511` are set exactly where `tokens.css` switches theme: `@media (prefers-color-scheme: light) { :global(:root:not([data-theme="dark"])) .tracing-paper { ... } }` and `:global(:root[data-theme="light"]) .tracing-paper { ... }`. WP-10 (toolbar swatches) and WP-17b/WP-17c (chart) only read the variables; they do not define them.
   - Windows (temporary until WP-22): `initialWindows(nowMs: number)` local function returns `incident = [floor(now to the hour) - 1 h, floor(now to the hour))` and `reference =` the equal period right before it, both RFC3339 UTC. Keep the pair in one `$state` object named `windows` so WP-22 can replace the setter.
   - `run()`: if `closed`, set `status='error'` with `TRACING_TEXT.closedReason` and return (no request). Otherwise abort the previous controller, take `seq = nextRequestSeq()`, `latestSeq = seq`, set `status='loading'`, call `fetchTracing(caseId, { incident, reference }, seq, signal)`. On success ignore the answer when `isStale(res.requestSeq ?? seq, latestSeq)` (`requestSeq` is optional on the wire) or the signal aborted; else store `result`, `status='ready'`. On error ignore `AbortError`; else clear `result` (the chart is cleared), set `errorText = tracingErrorText(e)`, `status='error'`.
   - `onMount`: call `run()` once (opening draws the timeline by itself; read-only). `onDestroy`: abort.
   - Markup: `<section aria-labelledby="tp-title">` with `<h2 id="tp-title">` `TRACING_TEXT.heading`; a `secondary-button` `TRACING_TEXT.backToCase` calling `onClose`; then four stable regions in this order, each a `<div data-region="windows|toolbar|chart|notes">`: `windows` and `toolbar` render nothing yet; `chart` renders, per state, `readingWindow` plus a `Cancel` button (`TRACING_TEXT.cancel`, aborts and sets `idle`) while loading; the error text with a `Try again` button (`TRACING_TEXT.retry`, calls `run()`) on error; and when ready: if `result.sources.length === 0` and `result.lanes.length === 0` (a case with no files and no audit source) a `<p role="status">` showing `TRACING_TEXT.emptyNoSources`; otherwise a plain list of lanes in response order showing `lane.label` and `lane.state` (placeholder replaced by WP-17b, which keeps the empty-case sentence branch). `role="status"` on the loading and error text.
2. `web/src/lib/components/CaseHome.svelte`:
   - Add `import { lazyComponent } from '../lazy';` and `const TracingPaper = lazyComponent(() => import('../../components/TracingPaper.svelte'));`; import `TRACING_TEXT` from `../copy/tracingCopy`.
   - Add `let tracingFor = $state('');` and `const tracingOpen = $derived(tracingFor === caseId);` beside `guidedFor`/`guidedOpen`.
   - Add a render branch `{:else if tracingOpen}` placed immediately before the existing `{:else if guidedOpen}` branch: `{#key caseId}<TracingPaper {caseId} closed={!!info?.closed} {readOnly} onClose={closeTracing} />{/key}`. `closeTracing()` sets `tracingFor = ''`, then `await tick()` and focuses the element with id `home-tracing-open`.
   - Add a new block inside `{#if offersWork}`, after the Analysis section and before the Guided section: `<section class="block" aria-labelledby="home-tracing">` with `<h3 id="home-tracing">` = `TRACING_TEXT.heading`, `<p class="note">` = `TRACING_TEXT.entryNote`, and `<button id="home-tracing-open" type="button" class="secondary-button" disabled={!!info?.closed} onclick={() => (tracingFor = caseId)}>` = `TRACING_TEXT.entryButton`. It is deliberately NOT `primary-button`: the Analysis button stays the one main button on the case home (`defects1.test.cjs` line 67). When `info?.closed`, add `<p class="note">` = `TRACING_TEXT.closedReason`.
3. `web/tests/tracingPaper.test.cjs` (reads the `.svelte` files as text, as `web/tests/casehome.test.cjs` does). Tests:
   - `case home offers the timeline entry and keeps the Guided and Analysis blocks`: `CaseHome.svelte` contains `home-tracing`, `home-tracing-open`, `TRACING_TEXT.entryButton`, `lazyComponent(` and `import('../../components/TracingPaper.svelte')`, and still contains `home-guided`, `home-analysis`, `GuidedAnalysis`.
   - `entry is disabled for a closed case`: the entry button line contains `disabled={!!info?.closed}`.
   - `entry button is outlined, not a second primary`: the text of `CaseHome.svelte` between `id="home-tracing"` and the next `</section>` contains `class="secondary-button"` and not `primary-button`.
   - `lane colours are defined once on the TracingPaper root`: `TracingPaper.svelte` contains `--lane-audit`, `--lane-sql`, `--lane-iis`, `.tracing-paper` and `data-theme="light"`.
   - `TracingPaper has the four regions and the request guards`: file text contains `data-region="windows"`, `"toolbar"`, `"chart"`, `"notes"`; contains `new AbortController`, `nextRequestSeq`, `isStale`, `TRACING_TEXT.cancel`, `TRACING_TEXT.retry`; does not match `/from '.*(guided|callTrace)/`.
   - `empty case shows the no-sources sentence`: `TracingPaper.svelte` contains `TRACING_TEXT.emptyNoSources` inside the `chart` region and a guard on `sources.length === 0`; `tracingCopy.ts` still holds the exact sentence `No files have been added to this case yet. Add a source first.`
   - `CallTrace stays untouched`: `CallTrace.svelte` does not contain `TracingPaper` or `tracing`.
   - `TracingPaper is only reached through lazyComponent`: no static `import TracingPaper` anywhere under `web/src` (scan with `fs` over `src`).

## Proof
- `cd web && node --test tests/tracingPaper.test.cjs tests/casehome.test.cjs tests/callTrace.test.cjs` -> all pass.
- `cd web && npm test` -> all pass (includes `defects1.test.cjs`, which counts the primary buttons on the case home, and `p3.fix.test.cjs`).
- `cd web && npm run check` -> 0 errors.
- `cd web && npm run build && node --test tests/distChunks.test.cjs` -> pass (no static import cycle; a separate lazy chunk for the screen).
- Wireframe check: open the case home of the seeded demo case. The new block sits after Analysis and before Guided; heading "Investigation Timeline", the one-line note, and an outlined (secondary) button "Open the Investigation Timeline" (wireframe: `prototype/tracing-paper-PROTOTYPE.html`, block `h-trace`; the Guided block is still shown because WP-64 comes later). Closed case: button disabled with the closed sentence.

## Depends on
WP-14, WP-16a
