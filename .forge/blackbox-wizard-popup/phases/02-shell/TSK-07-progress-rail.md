# TSK-07 Progress rail
Risk: low. Worker size: small. Reason: one presentational component with clear props.

## Outcome
`web/src/components/blackboxWizard/ProgressRail.svelte` per contract C-4.

## Write scope
- `web/src/components/blackboxWizard/ProgressRail.svelte` (new)

## Must not change
Anything else.

## Changes
- `<ol aria-label="Progress, step X of N">`, connected dots, reached steps are buttons, later ones disabled, `aria-current="step"`.
- At ≤ 640 px only the current label shows. Targets at least 40 px.

## Proof
`cd web && npm run check`. Rail logic is tested through TSK-01 (`canVisit`).

## Depends on
None.
