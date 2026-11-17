# TSK-21 Clumsy-user pass and one fixer round
Risk: medium. Worker size: medium. Reason: break-the-app testing in the running app, then small fixes.

## Outcome
The built app is run locally; the wizard is pushed hard (double clicks, Enter spam, Esc mid-submit, back and forth on the rail, bad paths, 33 targets, duplicate names in other case, offline server). Findings from TSK-20 and this pass are fixed in one round with tests.

## Write scope
- Files named by the findings, inside `web/src/components/blackboxWizard/`, `web/src/lib/blackboxWizard.ts`, `web/src/lib/blackboxDrives.ts`, `web/src/lib/copy/blackboxWizardCopy.ts`, `internal/server/blackbox_drives*.go`, their tests, and `web/dist/**` (rebuild).
- `.forge/blackbox-wizard-popup/reviews/clumsy-user.md` (new)

## Must not change
Anything outside the list; deviations go in `progress.md`.

## Changes
As findings say. Rebuild `web/dist` after fixes.

## Proof
`go test ./...`; `cd web && npm test && npm run check && npm run build`.

## Depends on
TSK-20.
