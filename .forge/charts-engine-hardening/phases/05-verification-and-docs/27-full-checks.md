# TSK-27 full-checks

risk: low — checks, no edits.

## Outcome

Every automated check that can run here has run, and its result is recorded.

## Write scope

- `.forge/charts-engine-hardening/phases/05-verification-and-docs/progress.md` only.

## Must not change

- Any other file. Do not fix failures here: record the failing test and its message, then stop (BLOCKED).
- Do not commit. Do not push. Never touch `sql/`.

## Changes

Run in order from the repo root and record each result (PASS/FAIL + the last output line) in `progress.md` Evidence:

1. `.dev\toolchains\go\bin\go.exe build ./...`
2. `.dev\toolchains\go\bin\go.exe test ./...`
3. `cd web; npm test`
4. `cd web; npm run check`
5. List the e2e specs only: `Get-ChildItem tools/e2e/specs`. Playwright is not installed here; write `e2e specs not run (Playwright not installed)` under UNMEASURED.
6. `node prototype/flow-check.cjs` result from TSK-23 is copied over, not re-run.

## Proof

`progress.md` has one line per step 1–6.

## Depends on

TSK-26.
