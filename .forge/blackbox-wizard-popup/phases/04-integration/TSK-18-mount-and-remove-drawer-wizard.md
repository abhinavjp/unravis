# TSK-18 Mount the pop-up, remove the drawer wizard
Risk: medium (drawer view state and focus return). Worker size: medium. Reason: edits the Blackbox page view logic.

## Outcome
The two Place buttons in `BlackboxPanel.svelte` open `BlackboxPlaceWizard` as a pop-up over the drawer. On leave, focus returns to the button that opened it. On placed, the list reloads. `BlackboxWizard.svelte` is deleted (OD-9).

## Write scope
- `web/src/components/BlackboxPanel.svelte`
- `web/src/components/BlackboxWizard.svelte` (delete)
- `web/src/lib/blackbox.ts` (only to remove `wizardEscape` if nothing else uses it; check with grep first)
- `web/tests/*.test.cjs` that import the deleted file or `wizardEscape` (update only)

## Must not change
Add, Remove, Status, Apply and Read key views in the panel.

## Changes
- Replace `view.kind === 'wizard'` with a `placeOpen` flag; the drawer keeps showing the list behind the pop-up.
- Keep `canPack={list?.canPack ?? false}` and `onPlaced` behaviour.

## Proof
`cd web && npm test && npm run check && npm run build`. Grep: no import of `BlackboxWizard.svelte` remains.

## Depends on
TSK-17, TSK-12.
