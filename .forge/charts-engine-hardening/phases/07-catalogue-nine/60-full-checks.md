# TSK-60 full-checks

risk: low — checks, no edits.

## Outcome

The whole repository builds and every Go and web check passes with Phase 07 in place, or each failure is written down.

## Write scope

- `.forge/charts-engine-hardening/phases/07-catalogue-nine/progress.md` only.

## Must not change

- Any other file. Do not fix failures here: record the failing test and its message, then stop (BLOCKED).
- Do not commit. Do not push. Never touch `sql/`.

## Changes

1. `.dev\toolchains\go\bin\go.exe build ./...`
2. `.dev\toolchains\go\bin\go.exe test ./...`
3. `cd web; npm test`
4. `cd web; npm run check`
5. `Get-ChildItem tools/e2e/specs` and confirm `plan.spec.ts` holds the new Version literal from TSK-43 (`rg -n "const Version" internal/investigate/thresholds.go` gives it). Write under UNMEASURED: "e2e specs not run (Playwright not installed)", unless Playwright is installed, in which case run `plan.spec.ts` and record the result.
6. Write one Evidence line per step: the command and PASS/FAIL. Under Done list TSK-43 … TSK-60.

## Proof

```
.dev\toolchains\go\bin\go.exe build ./...
.dev\toolchains\go\bin\go.exe test ./...
cd web; npm test; npm run check
```

All PASS, or each failure written in `progress.md`.

## Depends on

59.
