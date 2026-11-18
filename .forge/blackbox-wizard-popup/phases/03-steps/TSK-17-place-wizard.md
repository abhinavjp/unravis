# TSK-17 Place wizard (wiring and submit)
Risk: high (this is the real install and pack path). Worker size: strong. Reason: joins state, steps, API, errors and focus.

## Outcome
`web/src/components/blackboxWizard/BlackboxPlaceWizard.svelte` with the same props as today's wizard (`thisPc`, `canPack`, `onLeave`, `onPlaced`). It opens WizardDialog with the rail, renders the current step, runs validate before Review, and runs install or pack + `saveBlob` on the last step.

## Write scope
- `web/src/components/blackboxWizard/BlackboxPlaceWizard.svelte` (new)
- `web/src/lib/copy/blackboxWizardCopy.ts` (only to add keys noted missing in `progress.md` by TSK-09..16)

## Must not change
`blackbox.ts` API, `BlackboxPanel.svelte`, `BlackboxWizard.svelte`.

## Changes
- Phases idle / working / installed / pack / failed, as in the current wizard.
- `sqlSeen` from `blackboxApi.targets()` services (`MSSQLSERVER` or `MSSQL$*`), pc path only.
- Footer: Back, "Use defaults, go to review" on record/sql/watch, "Nothing is saved until the last step", primary Next / Place on this PC / Build the pack / Back to review / Done.
- Server refusals through `BB_REFUSAL`; field errors focus the field on the step that owns it.
- Focus the step heading on every step change.

## Proof
`cd web && npm test && npm run check && npm run build`. Merge-sentinel review: the payload sent equals `buildSettings`, probe off, no extra keys.

## Depends on
TSK-01, TSK-02, TSK-06, TSK-07, TSK-09, TSK-10, TSK-11, TSK-13, TSK-14, TSK-15, TSK-16.
