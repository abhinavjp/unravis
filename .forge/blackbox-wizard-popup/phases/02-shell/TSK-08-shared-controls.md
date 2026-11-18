# TSK-08 Shared controls
Risk: low. Worker size: medium. Reason: three small accessible components with keyboard behaviour.

## Outcome
`SegmentedControl.svelte`, `RadioCards.svelte` and `FigureDetails.svelte` in `web/src/components/blackboxWizard/`.

## Write scope
- `web/src/components/blackboxWizard/SegmentedControl.svelte` (new)
- `web/src/components/blackboxWizard/RadioCards.svelte` (new)
- `web/src/components/blackboxWizard/FigureDetails.svelte` (new)

## Must not change
Anything else.

## Changes
- SegmentedControl: native radio inputs in a `fieldset` with a legend, 2-5 options, arrow keys, `bind:value`.
- RadioCards: either/or cards with title and one line, native radios.
- FigureDetails: `<details><summary>` with what / why / what to look for / what is stored.
- All targets 40-44 px; visible focus ring; theme tokens from the app.

## Proof
`cd web && npm run check`.

## Depends on
None.
