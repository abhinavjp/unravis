# WP-62b Move the guided-only specs to the archive
risk: medium    size: M    wave: 9    model: sonnet
## Outcome
The five spec files that exist only to exercise guided analysis (78 tests) leave the default suite and live in `tools/e2e/archive/guided/`; the guided helpers are removed from the shared `support/ui.ts`, so main specs cannot import them. Archived tests still pass (`-Archive`) until WP-64 removes the case-home Guided button; after that they are informational (see WP-63). Owner ruling: archived, not deleted.

## Write scope
- `tools/e2e/specs/guide-steps.spec.ts` (moved to `tools/e2e/archive/guided/guide-steps.spec.ts`)
- `tools/e2e/specs/guide-checklist.spec.ts` (moved to `tools/e2e/archive/guided/guide-checklist.spec.ts`)
- `tools/e2e/specs/guided.spec.ts` (moved to `tools/e2e/archive/guided/guided.spec.ts`)
- `tools/e2e/specs/export-ui.spec.ts` (moved to `tools/e2e/archive/guided/export-ui.spec.ts`)
- `tools/e2e/specs/manual-tools.spec.ts` (moved to `tools/e2e/archive/guided/manual-tools.spec.ts`)
- `tools/e2e/support/ui.ts`

## Must not change
- Any test title, id, selector or assertion inside the moved files: only import lines change. No test is edited, skipped or deleted.
- `tools/e2e/specs/help-guide.spec.ts` and `help.spec.ts` (Help drawer only, no guided entry), `plan.spec.ts`, `sources-steps.spec.ts` (stay in main).
- `tools/e2e/known-defects.json`, `tools/e2e/support/fixtures.ts`, `tools/e2e/playwright.config.ts`.

## Changes
Note: spec says MAN-03 and MAN-03a stay in main "with the entry rewritten to Manual tools without `openGuided`". In the code, Manual tools is reached only through Guided analysis (`toManual` in the spec calls `openGuided`, then Case actions then Switch to Manual tools), so there is no entry to rewrite. Safest reading used here: the whole `manual-tools.spec.ts` (24 tests, MAN-03 and MAN-03a included) moves to the archive. This is an ESCALATION in the authoring report.
Test counts today: guide-steps 25, guide-checklist 10, guided 6 (includes MAN-04, MAN-04a), export-ui 13, manual-tools 24; total 78.
1. Move with `git mv tools/e2e/specs/<file> tools/e2e/archive/guided/<file>` for the five files above (the folder and `support.ts` exist from WP-62a).
2. In every moved file change the import of fixtures from `'../support/fixtures'` to `'../../support/fixtures'`.
3. Helper imports. `guide-steps.spec.ts` line 4: `import { INC, REF, openGuided, setWindows, runSteps, gotoStep, stepRail } from './support';`. `guide-checklist.spec.ts` line 4: `import { openGuided, runSteps, gotoStep } from './support';`. `export-ui.spec.ts` line 5: `import { openGuided, runSteps, setWindows } from './support';`. `manual-tools.spec.ts` line 5: `import { INC, REF, openGuided, setWindows, stepRail } from './support';`. `guided.spec.ts` has its own local `openGuided`, so only its line 3 changes: `import { goHome, openCase } from '../../support/ui';`.
4. `tools/e2e/support/ui.ts`: delete everything from the line `// ---- Guided analysis helpers (shared by guide-*.spec.ts) ----` to the end of the file (`INC`, `REF`, `openGuided`, `setWindows`, `stepRail`, `runSteps`, `gotoStep`); keep the file header, `goHome` and `openCase`. After this, `ui.ts` must contain no word `guided` (case-insensitive).
5. Do not touch `exports.spec.ts` or `keeper-update.spec.ts` here (WP-62c).

## Proof
- `cd tools/e2e; npx playwright test --list -c playwright.archive.config.ts` -> `Total: 78 tests in 5 files`, exit 0.
- `cd tools/e2e; npx playwright test --list` (main) -> total equals the number recorded in WP-62a minus 78.
- `Select-String -Path tools/e2e/specs/*.ts -Pattern "openGuided|setWindows|runSteps|gotoStep|stepRail"` -> no output.
- `Select-String -Path tools/e2e/support/ui.ts -Pattern "guided" -CaseSensitive:$false` -> no output.
- `cd tools/e2e; powershell -File run-e2e.ps1 -Archive` -> 78 passed, exit 0 (runs on any build from before WP-64; the entry button must still exist). Record in `progress.md`; `report/archive.json` exists and is non-empty.
- `cd tools/e2e; powershell -File run-e2e.ps1` -> exit 0 with the main suite (exports.spec.ts and keeper-update.spec.ts still hold their guided clicks until WP-62c, so run this check after WP-62c if the exe already lacks the button).

## Depends on
WP-61, WP-62a
