# WP-62a Guided e2e archive harness
risk: low    size: M    wave: 1    model: sonnet
## Outcome
`run-e2e.ps1 -Archive` and `npm run e2e:guided` run Playwright against `tools/e2e/archive/guided/` and write `report/archive.json`; the guided helpers have an archive-only home. Nothing moves yet (WP-62b, WP-62c do that). Owner ruling: guided Playwright specs are archived, not deleted.

## Write scope
- `tools/e2e/playwright.archive.config.ts` (new)
- `tools/e2e/run-e2e.ps1`
- `tools/e2e/package.json`
- `tools/e2e/archive/guided/support.ts` (new)
- `tools/e2e/archive/guided/README.md` (new)

## Must not change
- `tools/e2e/playwright.config.ts` (`testDir './specs'` stays, so the main run skips the archive), `tools/e2e/support/ui.ts` in this packet (WP-62b removes the moved helpers), `tools/e2e/known-defects.json` (still DOC-02 and KEEP-09 only), any file under `tools/e2e/specs/`.
- The `-Exe`, `-Only`, `-Headed` behaviour of `run-e2e.ps1`; the main report path `report/results.json`.

## Changes
Note: the plan wanted `-Release` here too (main then archive, ID manifest). The plan's final acceptance calls `-Release` a later release step, so it is NOT built in this packet.
1. Create `tools/e2e/playwright.archive.config.ts`: `import { defineConfig } from '@playwright/test'; import base from './playwright.config';` and `export default defineConfig({ ...base, testDir: './archive/guided' });` (testMatch, workers 1, retries 0, forbidOnly, viewport and the reporter that reads `E2E_REPORT` all come from the base config).
2. `run-e2e.ps1`: add `[switch]$Archive` to the `param` block and one header line `#   -Archive       run the archived guided specs (archive/guided) and write report/archive.json`. After the `-Exe` handling: if `$Archive`, set `$env:E2E_REPORT = 'report/archive.json'` when `E2E_REPORT` is not already set, and add `'--config', 'playwright.archive.config.ts'` to `$args2`. `-Only` and `-Headed` keep working with `-Archive`. Update the line "The JSON report goes to report/results.json" to also say the archive run writes `report/archive.json`.
3. `package.json` scripts: add `"e2e:guided": "powershell -NoProfile -ExecutionPolicy Bypass -File run-e2e.ps1 -Archive"` after `"e2e"`.
4. Create `tools/e2e/archive/guided/support.ts`: the file header `// Helpers for the archived guided specs only. Main specs must not import this file.`; `import { expect, type Page } from '@playwright/test';` and `import { goHome, openCase } from '../../support/ui';`; then copy verbatim, with `export` kept, everything from the line `// ---- Guided analysis helpers (shared by guide-*.spec.ts) ----` to the end of `tools/e2e/support/ui.ts` (today lines 17-end: `INC`, `REF`, `openGuided`, `setWindows`, `stepRail`, `runSteps`, `gotoStep`). The copy is temporary duplication; WP-62b removes the originals.
5. Create `tools/e2e/archive/guided/README.md` (about 25 lines): why the folder exists (owner decision D-2 = A: guided is kept but unlisted; its Playwright specs leave the default suite); the files that will be here after WP-62b and WP-62c (`guide-steps`, `guide-checklist`, `guided`, `export-ui`, `manual-tools`, `exports-guided`, `keeper-update-guided`, all `.spec.ts`); how to run (`powershell -File run-e2e.ps1 -Archive`, `npm run e2e:guided`, report `report/archive.json`); rules: a test id lives in exactly one of `specs/` and `archive/guided/`; main specs never import `archive/guided/support.ts`; archived specs open Guided through the case-home button `Open guided analysis`, which WP-64 removes, so the archive suite can only run on a build from before WP-64 until an entry hook is decided (see the plan escalation).

## Proof
- `cd tools/e2e; npx playwright test --list -c playwright.archive.config.ts` -> prints `No tests found` and exits 1 (nothing is archived yet), with no config or import error. After WP-62b it lists 78 tests.
- `cd tools/e2e; npx playwright test --list` (main) -> same total as before this packet (record the number in `progress.md`).
- `powershell -NoProfile -Command "$e=$null; [System.Management.Automation.Language.Parser]::ParseFile('tools/e2e/run-e2e.ps1',[ref]$null,[ref]$e) | Out-Null; $e.Count"` -> `0`.
- `node -e "console.log(require('./tools/e2e/package.json').scripts['e2e:guided'])"` -> prints the command above.

## Depends on
none
