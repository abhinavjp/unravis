# TSK-09 Name and Where steps
Risk: low. Worker size: small. Reason: one input and one radio-card choice.

## Outcome
`StepName.svelte` and `StepWhere.svelte` in `web/src/components/blackboxWizard/`.

## Write scope
- `web/src/components/blackboxWizard/StepName.svelte` (new)
- `web/src/components/blackboxWizard/StepWhere.svelte` (new)

## Must not change
`blackboxWizard.ts`, copy files.

## Changes
- Name: `maxlength=63`, hint per B-06, inline error from `stepProblem` with `role="alert"` and `aria-describedby`.
- Where: RadioCards "This PC" / "A pack for another server". "This PC" disabled with the reason when `!(thisPc.available && !thisPc.installed)`; "Pack" disabled when `!canPack` (B-11).

## Proof
`cd web && npm run check`.

## Depends on
TSK-01, TSK-02, TSK-08.
