# P-29 Inspector panel and the demo "Pretend the database is" switch

model: sonnet, effort: medium. Reason: a read-only panel with six sections and one demo-only control.
risk: low. Review batch: RB-5. Wave: W2. Requirements: R-05, R-06, R-17.

## Outcome
`CallInspector.svelte` is the right-hand panel of layout W1: the selected item, what ran while the call ran, the data sources with the database state sentence, what the chart does not show, how to read it, and (demo only) the pretend switch.

## Write scope
- `web/src/components/CallInspector.svelte` (new)
- `web/tests/slowcall.inspector.test.cjs` (new)

## Must not change
- Nothing else. The database sentence comes from `dbState` (P-07); this file writes no sentence of its own.

## Changes
1. Props: `{ selected: SourceItem | null; whileRan: Readout | null; db: DbStateView; sources: { key: 'api' | 'iis' | 'db'; label: string; recorded: boolean }[]; zone: string; demo: boolean; pretend: DbStateKey | null; onPretend: (k: DbStateKey | null) => void }`.
2. `<aside class="scall-inspector" data-testid="scall-inspector" aria-label={DRILL_TEXT.secSelected}>` with these `<section>`s, each with an `<h3>`:
   - `secSelected`: `selectedNone`, or the item's what, source name, start time (`formatTime`, zone) and `formatMs(tookMs)` (from `web/src/lib/drill/overview.ts`); a null duration prints `DRILL_TEXT.notRecorded`.
   - `secWhile`: the three readout lines for the step the call started in (`readApi`, `readIis`, `readDb` / `readDbNone`); null → `notRecorded`.
   - `secSources` (`data-testid="scall-sources"`): one line per source, with `notRecorded` when `recorded` is false; then `<p data-testid="scall-db-state" data-state={db.key}>{db.message}</p>`; then `coverageNote`.
   - `secNotShown`: a list of `notShownTime`, `notShownStart`, `notShownWholeDb`, `notShownIis`.
   - `secHow`: a list of `legendWidth`, `legendShape`, `legendHatched`, `legendCall`, with the help text `DRILL_HELP.atOnce` under it.
3. Demo switch, rendered only when `demo` is true: `<div class="scall-pretend" data-testid="scall-pretend"><span>{DRILL_TEXT.pretendLabel}</span>` then one outlined button per state: `pretendReal` (→ `onPretend(null)`), `pretendQs` (`'qs'`), `pretendFile` (`'qsfile'`), `pretendApi` (`'api'`), `pretendOff` (`'off'`), `pretendRo` (`'ro'`), `pretendLate` (`'late'`), `pretendDenied` (`'denied'`), `pretendEvents` (`'events'`), `pretendNotRead` (`'notread'`); each `data-testid="scall-pretend-{key|real}"`, `aria-pressed={pretend === key}`; then `<p>{DRILL_TEXT.pretendNote}</p>`. When `demo` is false none of this is in the DOM.
4. The panel may scroll on its own only as part of the page (no inner scroll box); it is below-the-fold content in narrow layouts (P-32 decides placement).
5. Text test: the five section keys are used; `scall-db-state` has `data-state={db.key}`; the pretend block sits inside `{#if demo}`; ten pretend testids; `notRecorded` is used; no literal containing `Query Store` in the file.

## Proof
- [ ] `cd web && node --test tests/slowcall.inspector.test.cjs` → pass, 0 fail.
- [ ] `cd web && npx svelte-check --threshold error` → 0 errors.
- [ ] Pretend switch is demo only by design. The real sentence is live-verified in P-46.

## Depends on
P-02, P-07, P-10.
