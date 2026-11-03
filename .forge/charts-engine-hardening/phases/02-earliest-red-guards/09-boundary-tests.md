# TSK-09 boundary-tests

risk: low — tests only.

## Outcome

Two tests pin the boundary: tiers 60 s apart are "together" (no clear origin); tiers 120 s apart are separable (origin named).

## Write scope

- `internal/investigate/evaluate_test.go` (append)

## Must not change

- Any non-test file. Any existing test.
- Do not commit. Do not push. Never touch `sql/`.

## Changes

1. Read the existing tests `TestNoClearOriginWhenTiersMoveTogether` (evaluate_test.go ~166) and `TestTiersFarApartAreSeparable` (~186). Copy the setup of each exactly (same helper calls, same tier names); change only the offset between the two tier onsets.
2. Append `TestTiersSixtySecondsApartAreTogether`: same setup as the first test, with the two onsets exactly 60 s apart. Assert what the first test asserts (origin is none / not named).
3. Append `TestTiersTwoMinutesApartAreSeparable`: same setup as the second test, with the two onsets exactly 120 s apart. Assert what the second test asserts (the earlier tier is named).
4. If either new test fails, the boundary differs from the rule in the plan: do NOT change production code. Record the failing output in `progress.md` and stop (BLOCKED).

## Proof

```
.dev\toolchains\go\bin\go.exe test ./internal/investigate -run "TestTiersSixtySecondsApartAreTogether|TestTiersTwoMinutesApartAreSeparable"
```

Both PASS.

## Depends on

TSK-08.
