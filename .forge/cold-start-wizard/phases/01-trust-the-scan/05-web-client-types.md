# TSK-05: Web client types and helpers for the new scan fields
risk: low — types and pure helpers, tested with node:test.

## Outcome
`web/src/lib` knows `verified`, `unreadable`, `suggest`, `locate` and `probe`, and `isSelectable` additionally requires `verified === true` (R6 on the client). Copy for the new reasons exists in the existing copy layer.

## Write scope
- `web/src/lib/types.ts`, `web/src/lib/discover.ts`, `web/src/lib/api.ts`, `web/src/lib/discoverFake.ts`
- `web/src/lib/copy/` (new reason wordings only)
- `web/tests/discover.test.cjs` (extend); `web/tests/copylint.test.cjs` only if the new copy needs registering

## Must not change
Existing exports' signatures; the demo/fake scan data must keep working (mark its readable findings `verified: true`).

## Changes
1. Add the fields to the `Finding` type; add `locateFinding(scanId, findingId, path)` and `probeReachable(host, port)` API wrappers (token handling as the neighbours).
2. `isSelectable`: add the `verified` condition; add `readinessLabel(f)` returning the plain-language reason for an unreadable finding.

## Proof
`cd web; npm test; npm run check` green. New tests: an unverified log-file finding is not selectable; every reason key has copy; locate/probe wrappers send the token and put the path only in the body.

## Depends on
TSK-01, TSK-02, TSK-03, TSK-04
