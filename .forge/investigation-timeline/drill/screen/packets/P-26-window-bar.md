# P-26 Window control bar

model: sonnet, effort: medium. Reason: one component, form validation, keyboard reachable.
risk: low. Review batch: RB-5. Wave: W1. Requirement: R-02.

## Outcome
`CallWindowBar.svelte` shows the one window control: preset chips, Custom with two number boxes, Earlier / Later, Back to this call, the "Showing ..." line, and the three source switches.

## Write scope
- `web/src/components/CallWindowBar.svelte` (new)
- `web/tests/slowcall.windowbar.test.cjs` (new)

## Must not change
- Nothing else. All logic comes from `web/src/lib/drill/callWindow.ts`; all text from `DRILL_TEXT`.

## Changes
1. Props: `{ window: CallWindow; callStartMs: number; callEndMs: number; zone: string; shown: { api: boolean; iis: boolean; db: boolean }; onCommit: (w: CallWindow) => void; onShown: (s: { api: boolean; iis: boolean; db: boolean }) => void }`.
2. Markup, in this order inside `<div class="scall-window" data-testid="scall-window" role="group" aria-label={DRILL_TEXT.windowLabel}>`:
   - one button per `WINDOW_PRESETS` value: `data-testid="scall-preset-{m}"`, text `DRILL_TEXT.windowPreset(m)`, `aria-pressed={presetOf(window) === m}`; click → `onCommit({ before: m, after: m, shiftMs: 0 })`.
   - Custom button `data-testid="scall-custom"` (`aria-expanded`), which shows two `<input type="number" min="1" max="120" step="1">` (`scall-before`, `scall-after`, labelled by `windowBefore` / `windowAfter`) and an Apply button `scall-apply`. Apply uses `clampMinutes` on both; if either is null show `DRILL_TEXT.windowRange` in a `<p role="alert" data-testid="scall-range">` and do not commit.
   - `scall-earlier`, `scall-later`: `disabled={!canStep(...)}`, click → `onCommit(stepWindow(...))`.
   - `scall-tocall`: shown only when `window.shiftMs !== 0`; click → `onCommit(backToCall(window))`.
   - `<p class="scall-window-line" data-testid="scall-window-line" aria-live="polite">`: `coversOverview` → `windowWhole`; moved → `windowLineMoved(from, to)`; else `windowLine(from, to, before, after)`; `from`/`to` via `formatTime(..., { zone })` hours and minutes.
   - when `!callInside(...)`: `<p data-testid="scall-outside">{DRILL_TEXT.legendOutside}</p>`.
   - source switches: three `<label><input type="checkbox" data-testid="scall-src-{api|iis|db}"> {laneApi|laneIis|laneDb}</label>` in a `fieldset` with legend `sourcesLabel`; at least one must stay on (the last checked box is disabled).
3. Buttons are outlined; the pressed preset uses the existing pressed-chip style in the app (look at the chips in `DrillPopup.svelte` and copy the class approach, not the file).
4. Text test: every testid above is present; `clampMinutes(` and `canStep(` are used; no number literal `120` or `15` in the markup part (they come from the model); no English sentence literal.

## Proof
- [ ] `cd web && node --test tests/slowcall.windowbar.test.cjs` → pass, 0 fail.
- [ ] `cd web && npx svelte-check --threshold error` → 0 errors.
- [ ] Not browser-verified here; P-32 mounts it.

## Depends on
P-02, P-05.
