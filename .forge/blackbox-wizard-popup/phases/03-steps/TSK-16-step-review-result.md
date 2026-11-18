# TSK-16 Review step and result view
Risk: medium (Review must show exactly what will be sent). Worker size: medium. Reason: renders the payload honestly with Change links.

## Outcome
`StepReview.svelte` (cards: Basics, What it records, Watched closely, SQL, Keep, Where it lives, each with a Change link) and `ResultView.svelte` (pc: placed, saved to, read key sealed; pack: zip name and the 4 steps to install it).

## Write scope
- `web/src/components/blackboxWizard/StepReview.svelte` (new)
- `web/src/components/blackboxWizard/ResultView.svelte` (new)

## Must not change
Copy files, `blackbox.ts`.

## Changes
- Read everything from `buildSettings(state)`, not from step-local values.
- Targets via `targetNamesByKind` and `T.reviewTargetsText` as the current wizard does.
- Keep the current bind warning, read-only note and windowsAsks / packWill notes.
- Folder shown read-only with a Change link (S-11).

## Proof
`cd web && npm run check`; `node --test tests/blackbox.review.test.cjs`.

## Depends on
TSK-01, TSK-02, TSK-08.
