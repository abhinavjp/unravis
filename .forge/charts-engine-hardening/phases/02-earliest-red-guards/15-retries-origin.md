# TSK-15 retries-origin

risk: high — can remove a named origin from real runs; a wrong condition hides a true load cause. Second reviewer required.

## Outcome

When the engine would name `load` as the origin but retries or errors came first, it names no origin and says so: load that follows retries is clients retrying, not new demand.

## Write scope

- `internal/investigate/origin.go` (`ComputeOrigin` only)
- `internal/investigate/evaluate_test.go` (append)

## Must not change

- Any other tier's naming. Thresholds. Goldens (unchanged; a golden change → BLOCKED).
- Do not commit. Do not push. Never touch `sql/`.

## Changes

1. In `ComputeOrigin` (~81-169), find the point after the decision switch where the result is Named. Add a guard: if the result is Named AND the named tier is `tierLoad` AND (`sig.SigRetriesUp == Yes` OR `sig.SigErrorsPrecede == Yes`) — use the exact variable that holds the signals in that function and the exact `Tri` constant for "yes" (grep `SigRetriesUp` in the package) — then: set the origin kind back to `OriginNone`, clear `Tier`, `Label` and `At`, set `near[tierLoad] = true` (the map the function already uses for "close but not named"), and append the note `Load rose after clients started retrying or failing; that is retrying, not new demand.` to the notes the function returns. If `ComputeOrigin` does not receive the signals today, read how `Evaluate` (evaluate.go ~121) calls it; add a `sig Signals` parameter and update every caller (`go build ./...` lists them).
2. Tests, append to `evaluate_test.go`: build the scenario from `TestLaneOnsetsMakeTheOrigin` shape but with load as the earliest tier and the generator's retry settings on (`genCfg.retryEvery` and `retryWindow` in `scenario_test.go`). `TestRetriesBeforeLoadAreNotNamedAsLoad`: assert origin is None and the note appears. `TestLoadStillNamedWithoutRetries`: same scenario with retries off → load is Named.
3. Run the whole suite; a golden change → BLOCKED.

Second reviewer: read the diff; confirm the guard runs only when the tier is `tierLoad` and that no other tier is affected.

## Proof

```
.dev\toolchains\go\bin\go.exe test ./internal/...
```

PASS, goldens unchanged.

## Depends on

TSK-14.
