# TSK-05 Drives client
Risk: low. Worker size: small. Reason: a typed fetch and one filter.

## Outcome
`web/src/lib/blackboxDrives.ts` per contract C-3: `fetchDrives`, `eligibleDrives`, types.

## Write scope
- `web/src/lib/blackboxDrives.ts` (new)
- `web/tests/blackbox.drives.test.cjs` (new)

## Must not change
`web/src/lib/blackbox.ts`.

## Changes
- Use the same fetch helper and error style as `blackboxApi` (read it, do not change it).
- `eligibleDrives`: fixed and not system first; system C: kept but marked; removable and network listed with a flag, never picked as default.

## Proof
`cd web && node --test tests/blackbox.drives.test.cjs && npm run check`.

## Depends on
None (codes against the C-3 shape; TSK-04 is not needed to start).
