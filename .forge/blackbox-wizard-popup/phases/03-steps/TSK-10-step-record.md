# TSK-10 Record step
Risk: medium (must not look like ticks that do nothing). Worker size: medium. Reason: catalogue rendering, presets and 3 real toggles.

## Outcome
`StepRecord.svelte`: preset segmented control, sample interval segmented control (5/15/30/60), groups from the catalogue each open to its figures with FigureDetails. Real checkboxes only for the 3 record booleans. Everything else is labelled "always recorded" or "recorded when present".

## Write scope
- `web/src/components/blackboxWizard/StepRecord.svelte` (new)

## Must not change
`blackboxCatalogue.ts`, `blackboxWizard.ts`, copy files.

## Changes
- Preset highlight from `presetOf`; any tick change moves it to Custom (S-07); Custom line shows "based on X, N changed, Reset to X".
- Log folders list (B-07) inside the IIS log catalogue group, shown only when that box is ticked; edits `logPathsText`; errors from `settingsProblem`.
- SQL host group shown read-only with one line pointing to the SQL step when present.

## Proof
`cd web && npm run check`. Preset logic is tested in TSK-01.

## Depends on
TSK-01, TSK-02, TSK-08.
