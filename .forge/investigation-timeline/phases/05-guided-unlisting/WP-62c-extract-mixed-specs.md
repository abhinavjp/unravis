# WP-62c Extract the guided tests from mixed specs, update COVERAGE
risk: low    size: M    wave: 9    model: sonnet
## Outcome
Main `tools/e2e/specs/` has no click on `Open guided analysis`: `EXP-04a` and `UPD-07` move to the archive and `COVERAGE.md` says which rows are covered by the archive suite. `help-guide.spec.ts` stays in main.

## Write scope
- `tools/e2e/specs/exports.spec.ts`
- `tools/e2e/specs/keeper-update.spec.ts`
- `tools/e2e/archive/guided/exports-guided.spec.ts` (new)
- `tools/e2e/archive/guided/keeper-update-guided.spec.ts` (new)
- `tools/e2e/COVERAGE.md`

## Must not change
- Every other test in `exports.spec.ts` (EXP-03, 03a, 03b, 03c, 04, 06, 06a, 08, 08a, 09, 09a) and `keeper-update.spec.ts` (all but UPD-07; UPD-07a stays), and their helpers.
- `tools/e2e/known-defects.json` (DOC-02, KEEP-09 only). The `Totals:` line of `COVERAGE.md` (counts do not change: archived rows stay covered).
- Test titles and assertions of the moved tests: copied verbatim.

## Changes
Note: the plan says to extract UPD-07; its "no update call, no call to another host" check is a privacy canary and leaves the main suite with this move. Keep it as written (owner ruling) and mention it in the phase review.
1. `exports.spec.ts`: delete the helper `openExportTab` (lines 116-121) and the test `EXP-04a: the Export tab before any run says so in one sentence` (lines 123-126). Then remove imports that became unused (`goHome`, `openCase` from line 4; `type Page` from line 2 if no other use): search the file for each name first.
2. Create `archive/guided/exports-guided.spec.ts`: header comment (`// Archived with the guided specs: the Export tab sits inside Guided analysis.`), `import { test, expect, type Page } from '../../support/fixtures'; import { goHome, openCase } from '../../support/ui';`, then the helper `openExportTab` and test `EXP-04a` copied verbatim from step 1.
3. `keeper-update.spec.ts`: delete the test `UPD-07: moving around the app and opening Help ten times makes no update call and no call to any other host` (lines 229-247). Keep `CASE`, `openHelp`, `storedPreview` (other tests use them).
4. Create `archive/guided/keeper-update-guided.spec.ts`: header comment, imports as in step 2 (`Page` type needed for `openHelp`), `const CASE = 'Checkout slowdown, 17 October';`, a copy of `async function openHelp(page: Page)` (lines 13-18 of the original: clicks button `Glossary` exact, expects the first dialog visible, returns it), then UPD-07 copied verbatim.
5. `COVERAGE.md`: in the scenario table replace the spec-file cell by `archive/guided/<file>` and the status `covered` by `covered (archive)` for every row whose file is `guide-steps.spec.ts`, `guide-checklist.spec.ts`, `guided.spec.ts`, `export-ui.spec.ts` or `manual-tools.spec.ts`; `EXP-04a` becomes `archive/guided/exports-guided.spec.ts`; `UPD-07` becomes `archive/guided/keeper-update-guided.spec.ts` (UPD-07a stays). Rows with other files (for example GUIDE-15 in `plan.spec.ts`, GUIDE-21 to 24 in `sources-steps.spec.ts`) are unchanged. In "How to run" add two lines: `powershell -File run-e2e.ps1 -Archive   # the archived guided specs, report/archive.json` and `npm run e2e:guided`. Add a short section "Archived guided specs" (3 lines): where they live, that a test id is in exactly one of `specs/` and `archive/guided/`, and that the archive runs before a release and on a schedule.

## Proof
- `git grep -n -i "Open guided analysis" -- tools/e2e/specs` -> no output.
- `cd tools/e2e; npx playwright test --list -c playwright.archive.config.ts` -> `Total: 80 tests in 7 files`.
- `cd tools/e2e; npx playwright test --list | Select-String "EXP-04a|UPD-07:"` -> no match; the same filter with `-c playwright.archive.config.ts` -> both found, each once.
- `cd tools/e2e; powershell -File run-e2e.ps1 -Archive -Only EXP-04a,UPD-07` -> 2 passed.
- `cd tools/e2e; powershell -File run-e2e.ps1 -Only EXP-03,EXP-04,UPD-07a` -> passed (the neighbours still run in main).
- `git diff --stat -- tools/e2e/known-defects.json` -> empty.

## Depends on
WP-61, WP-62a
