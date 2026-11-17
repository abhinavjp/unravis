# TSK-14 Keep step
Risk: low. Worker size: small. Reason: three inputs with known ranges.

## Outcome
`StepKeep.svelte`: days as SegmentedControl 7 / 30 / 90 / Other (number 1-365), max size in GB (0.1-10000), minimum free space % (1-50) (B-08, S-12).

## Write scope
- `web/src/components/blackboxWizard/StepKeep.svelte` (new)

## Must not change
Anything else.

## Changes
Inline errors from `stepProblem('keep')`, each tied to its field with `aria-describedby`.

## Proof
`cd web && npm run check`.

## Depends on
TSK-01, TSK-02, TSK-08.
