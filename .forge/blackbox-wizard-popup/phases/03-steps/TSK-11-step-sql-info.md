# TSK-11 SQL information step
Risk: low. Worker size: small. Reason: read-only list. Gated by OD-4 / B-01.

## Outcome
`StepSql.svelte`: the 13 SQL host counters read-only, each with FigureDetails, the line "Recorded automatically when SQL Server runs on this machine", and a note that choosing single counters and deeper checks are not part of this release. No ticks, no select all, no clear.

## Write scope
- `web/src/components/blackboxWizard/StepSql.svelte` (new)

## Must not change
Anything else.

## Changes
As Outcome. If the owner picks "drop the step" (B-01 alternative), this task is dropped and TSK-01 `steps()` never returns `sql`.

## Proof
`cd web && npm run check`.

## Depends on
TSK-01, TSK-02, TSK-08.
