# WP-10 Trace toolbar: lane toggles and scrub pill
risk: low    size: M    wave: 5    model: sonnet

## Outcome
A self-contained `TraceToolbar` component with three lane toggle buttons (API audit, SQL, IIS) that report their state with `aria-pressed`, and a scrub pill that always states the real export setting in plain words: "Default: export hides IP, referer and cookie hash" before a preview exists, "Export hides IP, referer and cookie hash" with scrub on, "Export includes IP, referer and cookie hash (scrub is off)" with scrub off. It never says stored data is masked (TR-23). Nothing mounts it yet; WP-11 does.

## Write scope
- `web/src/components/TraceToolbar.svelte` (new)
- `web/src/lib/tracing/toolbar.ts` (new, pure)
- `web/tests/traceToolbar.test.cjs` (new)

## Must not change
- `web/src/components/CallTrace.svelte` and `web/src/lib/callTrace.ts` (INV-7; `MAX_CALLS` stays there untouched). Do not import `callTrace.ts`: its `LaneKey` is a different type.
- `web/src/components/TracingPaper.svelte` (WP-11 mounts the toolbar), `web/src/lib/tracing/model.ts` (WP-16a; only import its `TracingLaneKey` type, which WP-16a defines as `'audit' | 'sql' | 'iis'`).
- The scrub setting is read only: no button here changes the export setting, and no call to the export routes.
- The pill wording above is copied exactly; no sentence about stored or saved data being masked, hidden or removed.

## Changes
Note: the spec puts lane visibility state in `callTrace.ts` beside `MAX_CALLS`. `callTrace.ts` is the per-call tray module with a different lane type, so the state lives in the new `web/src/lib/tracing/toolbar.ts` instead (same behaviour, no change to the tray module).
Note: the lane colour variables `--lane-audit`, `--lane-sql`, `--lane-iis` are defined by WP-16b on the `.tracing-paper` root of `TracingPaper.svelte` (light and dark). The toolbar is mounted inside that root by WP-11, so it reads them as `var(--lane-audit)` and so on. Because this component is also rendered alone in tests, give each swatch a fallback: `var(--lane-audit, #3fd6b0)`, `var(--lane-sql, #c58bd6)`, `var(--lane-iis, #e3a23c)`; do not define the variables in this component.
Note: WP-07a and WP-07b are ordering dependencies only (the scrub setting they finish is what this pill describes); no symbol from them is imported here.
Note: `scrubSensitive` reaches the web through the export preview body once phase 01 (WP-06) adds it to the export type; this packet takes the value as a prop (`boolean | null`, null = no preview yet) so it does not depend on that type.
1. `web/src/lib/tracing/toolbar.ts`:
   - `type LaneVisibility = Record<TracingLaneKey, boolean>`; `allLanesVisible(): LaneVisibility` (all three `true`); `toggleLane(v: LaneVisibility, key: TracingLaneKey): LaneVisibility` returns a new object with that key flipped; `visibleKeys(v): TracingLaneKey[]` in order audit, sql, iis.
   - `TOOLBAR_TEXT`: `ariaLabel: 'Timeline controls'`, `lanesLabel: 'Show rows'`, `audit: 'API audit'`, `sql: 'SQL'`, `iis: 'IIS'`, `scrubDefault: 'Default: export hides IP, referer and cookie hash'`, `scrubOn: 'Export hides IP, referer and cookie hash'`, `scrubOff: 'Export includes IP, referer and cookie hash (scrub is off)'`, `scrubHint: 'This is the setting the next evidence export will use. Nothing saved in the case is changed here.'`; `toolbarCopyEntries(): LintEntry[]` (ids `trace-toolbar.<key>`, as `tracingCopyEntries` in `web/src/lib/copy/tracingCopy.ts`; `LintEntry` from `../copy/copylint`).
   - `scrubPillText(scrub: boolean | null): string` -> `scrubDefault` for `null`/`undefined`, `scrubOn` for `true`, `scrubOff` for `false`.
2. `web/src/components/TraceToolbar.svelte`. Props: `{ visible: LaneVisibility; onToggle: (key: TracingLaneKey) => void; scrub: boolean | null }`.
   - `<div role="toolbar" aria-label={TOOLBAR_TEXT.ariaLabel}>` containing a group labelled `lanesLabel` with three real `<button type="button">` in order API audit, SQL, IIS; each `aria-pressed={visible[key]}`, `onclick={() => onToggle(key)}`, a lane colour swatch from `var(--lane-audit, #3fd6b0)`, `var(--lane-sql, #c58bd6)`, `var(--lane-iis, #e3a23c)` (the fallbacks are the dark values; WP-16b's variables win inside the timeline screen), visible focus outline, minimum 24 px target height.
   - The scrub pill: a `<button type="button" class="pill" aria-expanded={hintOpen} aria-label={scrubPillText(scrub)}>` whose visible text is the same sentence; clicking toggles a one-line `<p>` with `scrubHint`. A visually hidden `<span role="status" aria-live="polite">` repeats the sentence so a change is announced. The pill is styled neutral in all three states (no red/green meaning); scrub off may add an outline but no alarm colour.
   - Wraps on narrow screens (`flex-wrap: wrap`); colours only from CSS variables so light and dark both work.
3. `web/tests/traceToolbar.test.cjs` (`ts.transpileModule` loader as in `web/tests/casehome.test.cjs`; `.svelte` read as text). Tests:
   - `lanes start on and toggle one at a time`: `allLanesVisible()` is all true; `toggleLane` flips only the named key, returns a new object and leaves the input unchanged; toggling twice restores; `visibleKeys` keeps audit, sql, iis order.
   - `scrub pill states the real setting`: `scrubPillText(null)`, `(true)`, `(false)` equal the three sentences exactly; none contains the word `masked`.
   - `toolbar component has real buttons and live announcements`: `TraceToolbar.svelte` contains `role="toolbar"`, `aria-pressed`, `aria-expanded`, `aria-live="polite"`, `scrubPillText(`; `<button` count is at least 4; no `callTrace` import.
   - `swatches read the shared lane variables`: `TraceToolbar.svelte` contains `var(--lane-audit,`, `var(--lane-sql,`, `var(--lane-iis,` and does not contain `--lane-audit:` (no definition here); it imports `TracingLaneKey` from `../lib/tracing/model` (type only).
   - `toolbar copy passes the screen-string lint`: `lintScreenStrings(toolbarCopyEntries(), deck.terms)` is `[]`; if it rejects one word, change only that word and add a `Note:` to `progress.md`.
   - `toolbar is not mounted by the per-call tray`: `CallTrace.svelte` contains neither `TraceToolbar` nor `tracing`.

## Proof
- `cd web && node --test tests/traceToolbar.test.cjs tests/callTrace.test.cjs` -> all pass (`callTrace.test.cjs` unchanged and green).
- `cd web && npm test` -> all pass; `cd web && npm run check` -> 0 errors.
- `rg "masked" web/src/components/TraceToolbar.svelte web/src/lib/tracing/toolbar.ts` -> no matches.
- Wireframe check (`prototype/tracing-paper-PROTOTYPE.html`, toolbar above the chart): three toggle buttons API audit / SQL / IIS, lane colour swatches, a pill reading "Default: export hides IP, referer and cookie hash". The component is not mounted until WP-11, so the visual check happens in the WP-11 browser run; this packet is proven by the tests above. What to look for there: Tab reaches each toggle and the pill, Space/Enter toggles, the pressed look differs from the unpressed look in both themes, and at 375 px the controls wrap without sideways scroll.

## Depends on
WP-07a, WP-07b, WP-16a
