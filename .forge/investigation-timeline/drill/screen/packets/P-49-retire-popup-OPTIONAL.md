# P-49 (OPTIONAL, needs the owner's go) Retire the old pop-up

model: sonnet, effort: medium. Reason: deletes files that tests read as text; needs a careful sweep.
risk: medium. Review batch: RB-4 if run. Wave: none (not scheduled). Requirement: none; clean-up.

NOT IN ANY WAVE. Do not run without the owner saying so. Plan ruling D-11 keeps `DrillPopup.svelte` on disk until the screen has been used on live data.

## Outcome
The unused pop-up component and its tests are gone; nothing else changes.

## Write scope
- `web/src/components/DrillPopup.svelte` (delete)
- `web/tests/drill.popup.test.cjs` (delete)
- copy keys used only by the pop-up in `web/src/lib/drill/drillCopy.ts` and their assertions in `web/tests/drill.copy.test.cjs`

## Must not change
- `DrillOverview.svelte` and every model in `web/src/lib/drill/` that the screen uses. Breakdown and Reveal code that L-23 (D3) still needs: check L-23 first; if D3 will reuse the pop-up's Breakdown / Reveal tabs, STOP and report.

## Changes
1. `grep -rn "DrillPopup" web/src web/tests tools/e2e` → list every hit. Only the two files above and comments may remain.
2. For each `DRILL_TEXT` / `DRILL_HELP` key, `grep -rn "DRILL_TEXT\.<key>\b" web/src` : a key with zero users outside `DrillPopup.svelte` is removed with its test line. Keys used by the screen stay.
3. Delete the two files. Run the full web tests and fix only test lines that named the deleted files.

## Proof
- [ ] `cd web && npm test` → 0 fail; `npx svelte-check --threshold error` → 0 errors; `npm run build` → ok.
- [ ] `grep -rn "DrillPopup\|drill-popup" web/src tools/e2e/specs` → nothing.
- [ ] `cd tools/e2e && npm run e2e -- -Only DRILL-01,DRILL-02,DRILL-03,SCALL` → all pass.

## Depends on
P-48 and the owner's go.
