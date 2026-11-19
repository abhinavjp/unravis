# TSK-19 Rebuild web/dist and run all tests
Risk: low. Worker size: small. Reason: run commands, commit outputs.

## Outcome
`web/dist` rebuilt from the branch, all tests green, exe builds.

## Write scope
- `web/dist/**`

## Must not change
Source files.

## Changes
Run the build; stage only `web/dist` changes made by it.

## Proof
`go test ./...`; `cd web && npm test && npm run check && npm run build`; `go build ./...`.

## Depends on
TSK-18, TSK-04.
