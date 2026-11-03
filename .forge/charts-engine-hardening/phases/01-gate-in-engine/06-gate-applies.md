# TSK-06 gate-applies

risk: high — changes numeric verdicts for every run (rates and reference P95 can disappear); needs a second reviewer.

## Outcome

When the gate fails, `Facts` withholds the rate and the reference P95, so `Load.LambdaDx` reads "unknown", `SigArrivalsUp` is missing, and `Change.P95` is not Known. When the gate passes, output is identical to before.

## Write scope

- `internal/investigate/facts.go` (function `groups()` only)
- `internal/investigate/*_test.go` — ONLY a test-only raise of a generator's `perMin` or reference span, and only if an existing test fails for the reason below.

## Must not change

- The 5 goldens (they must pass unchanged; I1).
- Any threshold value. Any production code outside `groups()`.
- Do not commit. Do not push. Never touch `sql/`.

## Changes

In `groups()` (facts.go:570), after `f.Gate = evaluateGate(...)` (added in TSK-05) and BEFORE the line `f.Change.P50 = compareMs(...)`:

1. `if !f.Gate.RateOK { f.Ref.PerSec = nil; f.Incident.PerSec = nil }`
2. `if !f.Gate.P95OK { f.Ref.P95 = nil }`

Nothing else. (`Ref.P95` is the field of the embedded `Percentiles`; `PerSec` is `*float64` on `GroupFacts`.)

Then run the tests.

- If every test and golden passes unchanged → done.
- If an EXISTING non-golden test fails and the cause is that its generated reference has fewer than 60 events (or fewer than 100 for P95) or fails the spread rule → raise `genCfg.perMin` (or lengthen the reference) in THAT test's setup only, re-run, and list the test name and the raise in `progress.md` "Test-only generator raises".
- If a golden changes, or a test fails for any other reason → BLOCKED: paste the failing output into `progress.md`, revert your edit with Edit (not git), stop.

Second reviewer (before the phase gate): read the diff of `groups()`; confirm the two nil-assignments sit before every `Change` computation and that `Incident.N` and `Ref.N` are untouched.

## Proof

```
.dev\toolchains\go\bin\go.exe test ./internal/...
```

PASS with goldens unchanged. Record the command and result in `progress.md`.

## Depends on

TSK-05.
