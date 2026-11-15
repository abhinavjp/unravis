# P-41 e2e: move DRILL-01..03 from the pop-up to the screen

model: sonnet, effort: medium. Reason: three existing scenarios must keep their meaning with new selectors.
risk: medium. Review batch: RB-7. Wave: W5. Requirements: R-01, R-18.

## Outcome
`timeline-drill.spec.ts` passes against a demo exe built from this tree: a diamond opens the Slow call screen, an old row says so, Esc returns to the timeline with focus in the chart, Open in Analysis shows Analysis.

## Write scope
- `tools/e2e/specs/timeline-drill.spec.ts`

## Must not change
- `tools/e2e/support/*`, other specs, the scenario ids (DRILL-01, -02, -03 keep their ids; titles may change "pop-up" to "screen"). `web/` and Go code: if a scenario fails for a product reason, report it; do not patch the app here.

## Changes
1. Build the demo exe from the current tree first. Find the documented way: read the top of `tools/e2e/COVERAGE.md` and `tools/e2e/run-e2e.ps1`, and `grep -rn "unravis-demo" *.ps1 docs/ tools/ --include=*.ps1 --include=*.md`. Default path the runner expects: `.dev\run\scn\unravis-demo.exe` (`tools/e2e/support/launcher.ts:11`). If no build step is documented, STOP and report (UNVERIFIED in the plan: how this exe is produced).
2. Lines 1-4 (header comment): say "Slow call screen".
3. DRILL-01 (line 34): `page.getByTestId('drill-popup')` → `page.getByTestId('slow-call-screen')`. Keep the `**/api/record*` route. "Where the time went" is still drawn by `DrillOverview` inside the screen: keep that assertion. The hint loop: scope it to `screen.getByTestId('scall-thiscall').locator('section.block')` (the overview's sections); keep the "more than 10 characters" rule.
4. DRILL-02 (line 61): same testid swap; `drill-old-row` stays (it is inside `DrillOverview`).
5. DRILL-03 (line 74): after Esc expect `slow-call-screen` to have count 0 AND `.tracing-paper` to be visible; keep the focus check `[data-region=chart]` (P-23 restores focus; if focus is elsewhere, that is a product finding for P-23's implementer, report it). Second half: `screen.getByTestId('scall-analysis').click()` then the same two expectations as today (lines 87-89).
6. Add to DRILL-03: the timeline did not read again after Esc: count `**/api/cases/*/tracing` requests before Esc and 500 ms after; the count is equal.

## Proof
- [ ] `cd tools/e2e && npm run e2e -- -Only DRILL-01,DRILL-02,DRILL-03` → 3 passed.
- [ ] `cd tools/e2e && npm run e2e -- -Only TL` (or the id prefix the timeline specs use; read `COVERAGE.md`) → no new failure against the baseline you record before editing.
- [ ] `grep -n "drill-popup" tools/e2e/specs/timeline-drill.spec.ts` → nothing.
- [ ] Report: exe path and build command used. Demo only by nature.

## Depends on
P-33 (and all of waves W0-W4 for the demo exe).
