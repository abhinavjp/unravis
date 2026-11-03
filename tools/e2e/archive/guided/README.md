# Archived guided specs

Owner decision D-2 = A: the guided analysis is kept in the product but unlisted, so its
Playwright specs leave the default suite and live here instead of being deleted.

## What will be in this folder

After WP-62b and WP-62c move them, all `.spec.ts`:

- `guide-steps`
- `guide-checklist`
- `guided`
- `export-ui`
- `manual-tools`
- `exports-guided`
- `keeper-update-guided`

`support.ts` holds the guided helpers (`INC`, `REF`, `openGuided`, `setWindows`, `stepRail`, `runSteps`, `gotoStep`).

## How to run

- `powershell -File run-e2e.ps1 -Archive` (from `tools/e2e`; `-Exe`, `-Only` and `-Headed` work too)
- `npm run e2e:guided`
- The report goes to `report/archive.json`. The main run still writes `report/results.json` and skips this folder.

## Rules

- A test id lives in exactly one of `specs/` and `archive/guided/`.
- Main specs never import `archive/guided/support.ts`.
- Archived specs open Guided through the case-home button `Open guided analysis`. WP-64 removes that
  button, so the archive suite can only run on a build from before WP-64 until an entry hook is decided
  (see the plan escalation).
