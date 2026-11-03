# TSK-08 pair-sep-helper

risk: low — a refactor that must not change any result.

## Outcome

One helper, `pairSep`, defines how far apart two tiers must be to be "separable". `ComputeOrigin` and `colour` both call it. Behaviour is identical to before.

## Write scope

- `internal/investigate/origin.go`
- `internal/investigate/evaluate.go`

## Must not change

- Any threshold. Any test. Any golden. The numeric result for any input.
- Do not commit. Do not push. Never touch `sql/`.

## Changes

1. Read `origin.go` around line 83 (the line that computes `sep` in `ComputeOrigin`) and `evaluate.go` around line 310 (the line that recomputes `sep` in `colour`). Both compute the same duration from the clock tolerance and `OriginBucketSeconds`. Copy that exact expression.
2. In `origin.go` add one function: `func pairSep(th Thresholds, a, b string) time.Duration` with a doc comment "how far apart two tiers must be before the earlier one can be named; `a` and `b` are tier names (unused until lane margins arrive)". Its body returns the exact expression copied in step 1 (ignoring `a` and `b` for now: assign them to `_`).
3. Replace the `sep` computation in `ComputeOrigin` and in `colour` with a call to `pairSep(th, "", "")`. Do not change anything else in either function. (`th` is whatever the Thresholds variable is named locally; use that name.)

## Proof

```
.dev\toolchains\go\bin\go.exe build ./...
.dev\toolchains\go\bin\go.exe test ./internal/...
```

Both PASS with no golden change.

## Depends on

TSK-07 (Phase 01 green).
