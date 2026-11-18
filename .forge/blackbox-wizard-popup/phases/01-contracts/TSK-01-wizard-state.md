# TSK-01 Wizard state module
Risk: medium (every step depends on it). Worker size: strong. Reason: state machine, payload contract and rail rules in one place.

## Outcome
`web/src/lib/blackboxWizard.ts` implements contract C-1 in plan.md exactly: step list (7 or 8), maxSeen, returnTo, next/back/goTo, per-step problems, presets from the 3 booleans, "use defaults, go to review", default folder from drives, and `buildSettings`.

## Write scope
- `web/src/lib/blackboxWizard.ts` (new)
- `web/tests/blackbox.wizard.state.test.cjs` (new)

## Must not change
`web/src/lib/blackbox.ts`, `Settings` shape, any server code, any component.

## Changes
- Reuse `defaultSettings`, `nameProblem`, `targetsProblem`, `settingsProblem`, `INTERVALS` from `blackbox.ts`; do not copy their logic.
- `presetOf`: recommended = processes on, logsCatalogue on, copyLogsOnSnapshot off; light = all off; else custom (B-03).
- `steps()`: include `sql` when `where === 'pack'` or `sqlSeen` (B-01 default).
- `defaultFolder`: first eligible fixed non-system drive in letter order → `X:\UnravisBlackbox`; none → the fallback with `needsSystemTick` true when it is on the system drive. `null` drives (pack path, or route failed) → the fallback `D:\UnravisBlackbox`, matching `ChooseFolder`.
- `next()` refuses while `stepProblem` is set; when `returnTo === 'review'` it goes back to review.
- `stepProblem('folder')` is set when the folder is on the system drive and `confirmSystemDrive` is false.
- `buildSettings` follows C-5; `probe.enabled` always false.

## Proof
`cd web && node --test tests/blackbox.wizard.state.test.cjs && npm test && npm run check`. Tests cover: 7 vs 8 steps, locked rail beyond maxSeen, returnTo, preset switching to custom on any change, defaults shortcut, default folder with D:, without D:, with only C:, UNC, and that `buildSettings` output has exactly the keys of `defaultSettings()`.

## Depends on
TSK-03, TSK-05.
