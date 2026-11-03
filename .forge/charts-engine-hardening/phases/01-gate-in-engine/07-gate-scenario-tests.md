# TSK-07 gate-scenario-tests

risk: low — tests only.

## Outcome

End-to-end tests prove each gate edge through `ComputeFacts`/`Analyze`.

## Write scope

- `internal/investigate/gate_test.go` (append)

## Must not change

- Any non-test file. Any existing test.
- Do not commit. Do not push. Never touch `sql/`.

## Changes

Append tests using the helpers in `scenario_test.go`: `gen(genCfg) Input`, `stdWindows() (Window, Window)`, `genCfg{seed, ref, inc Window, perMin int, routes, mods, gaps, iis, clients, ...}`. Copy a minimal `genCfg` from `stepScenario` in `facts_test.go:8` (seed 1, `iis: true`) and set `ref, inc = stdWindows()`. The standard reference is a 60-minute window, so `perMin` is events per minute: 1 minute × `perMin` = events.

1. `TestGateTooFewReferenceEvents`: `perMin` so the reference holds fewer than 60 (use `perMin: 1`, which gives ~60 minus noise — if the real count lands at or above 60, use a reference window of 30 minutes via `Window{at(9,30), at(10,0)}` with `perMin: 1`). Assert `f := ComputeFacts(in, Current())`: `f.Gate.RateOK == false`, `f.Ref.PerSec == nil`, `f.Load.LambdaDx == "unknown"`.
2. `TestGateExactlyEnoughReferenceEvents`: build the input, then trim `in.Requests` so exactly 60 requests start inside the reference window (drop reference requests from the end of the slice order until `f.Ref.N == 60`; use a loop that re-runs `ComputeFacts`). Assert `f.Gate.RateOK == true` and `f.Ref.PerSec != nil`. If the spread rule fails because trimming bunched the minutes, instead trim evenly (drop every k-th reference request) — assertion stays the same.
3. `TestGateP95Boundary`: same trimming to exactly 99 reference requests → `f.Gate.P95OK == false`, `f.Ref.P95 == nil`; to exactly 100 → `P95OK == true`, `f.Ref.P95 != nil`.
4. `TestGateSpikyReferenceIsGrey`: build with `perMin: 30`, then move every reference request's `Start` and `End` to the same single minute (use `at(9, 30)` plus its offset: keep the duration `End-Start`, set `Start` to `at(9,30).Add(time.Duration(i%50)*time.Second)`). Assert `f.Gate.SpreadOK == false` and `f.Gate.RateOK == false`.
5. `TestGateDoesNotChangeASteadyRun`: `f := ComputeFacts(stepScenario(mod{}), Current())`; assert `f.Gate.RateOK` and `f.Ref.PerSec != nil` (the standard scenario must still pass the gate).

Table-test style as in the file. If a scenario cannot be built exactly as described, keep the assertion and change only the construction; report what you changed in `progress.md`.

## Proof

```
.dev\toolchains\go\bin\go.exe test ./internal/investigate -run "TestGate|TestSpreadOK|TestEvaluateGate"
.dev\toolchains\go\bin\go.exe test ./internal/...
```

Both PASS.

## Depends on

TSK-06.
