# TSK-13 Watch step
Risk: medium (honest copy about unknown/not running targets). Worker size: medium. Reason: wraps the picker with a mode choice and copy.

## Outcome
`StepWatch.svelte`: RadioCards "Nothing extra" / "Watch some closely" (electricity meter analogy), then `BlackboxTargetPicker` in pc or pack mode, and "Watching N of 32".

## Write scope
- `web/src/components/blackboxWizard/StepWatch.svelte` (new)

## Must not change
`BlackboxTargetPicker.svelte`, `blackbox.ts`.

## Changes
- `watchMode = 'none'` keeps picked targets hidden, not lost, and `buildSettings` sends `[]`.
- App pools say "state unknown"; services list running ones only; a typed name gets the "may not be running" note (S-10).

## Proof
`cd web && npm run check`.

## Depends on
TSK-01, TSK-02, TSK-08, TSK-12.
