# TSK-46 s8-payload-up

risk: medium — S8 is the only new suspect the audit alone can test, so it is the one that can move the existing goldens.

## Outcome

`payload_up` reads Yes when the P95 of `PayloadRecordTime` (`Request.P`) in the incident rose by both change marks over the reference, No when both windows hold enough values and it did not, and Unknown otherwise. Missing audit → missing `audit`; an audit with no `P` at all → missing `payload`.

## Write scope

- `internal/investigate/sus_s8.go` (replace the stub body)
- `internal/investigate/sus_s8_test.go` (new)

## Must not change

- Every other file; `SigPLarge` and its rule stay as they are.
- Do not commit. Do not push. Never touch `sql/`.

## Changes

1. `sus_s8.go`, replace the stub:
   ```go
   // sigPayloadUp compares the P95 of PayloadRecordTime between the windows (S8).
   // Requests are placed by their start. It needs GroupSizeP95 values on each side
   // and both change marks (ChangeMinMs and ChangeMinPercent) to read yes.
   func sigPayloadUp(in Input, f Facts, th Thresholds, fl map[string]Signal) Signal
   ```
   Body:
   - `if in.Audit != AuditPresent { return missing("audit", "There is no audit data for these windows.") }`
   - Collect `inc, ref []float64` from `in.Requests` with `r.P != nil`, by `in.Incident.Contains(r.Start)` / `in.Reference.Contains(r.Start)`. Check `Window.Contains` exists (`types.go` ~40) and use it.
   - `if len(inc)+len(ref) == 0 { return missing("payload", "The audit holds no PayloadRecordTime for these windows.") }`
   - `if len(inc) < th.GroupSizeP95 || len(ref) < th.GroupSizeP95 { return sig(Unknown, fmt.Sprintf("Too few PayloadRecordTime values to compare: %d in the incident, %d in the comparison period; %d are needed on each side.", len(inc), len(ref), th.GroupSizeP95)) }`
   - `pi := nearestRank(sortedCopy(inc), 0.95)`, `pr := nearestRank(sortedCopy(ref), 0.95)`. Read `stats.go` ~19–40 first for the exact signatures (`nearestRank` may take or return a pointer; adapt).
   - `up := pi-pr >= th.ChangeMinMs && pi >= pr*(1+th.ChangeMinPercent/100)`. **`ChangeMinPercent` is a percent (30), not a fraction**; check its type (int or float) and convert.
   - Yes: `fmt.Sprintf("PayloadRecordTime P95 rose from %.0f ms to %.0f ms.", pr, pi)`; No: `fmt.Sprintf("PayloadRecordTime P95 went from %.0f ms to %.0f ms, under the change mark.", pr, pi)`.
2. `sus_s8_test.go` `TestSigPayloadUp` (requests built by hand; `stdWindows()`; `th := Current()`):
   - `Audit: AuditMissing` → `Missing == "audit"`;
   - audit present, no `P` → `Missing == "payload"`;
   - 10 values each side → `Unknown`, `Missing == ""`;
   - 30 each side, ref all 50 ms, inc all 400 ms → `Yes`;
   - 30 each side, ref 500, inc 560 (+60 ms) → `No`;
   - 30 each side, ref 1000, inc 1150 (+150 ms, +15 in 100) → `No`.
3. `TestS8OnStepScenario`: `r := Analyze(stepScenario(mod{from: at(10, 20), to: at(11, 30), addA: 400}))`; S8 is not Red (record what it is in a `t.Logf`; it must not change `S5`, which stays `ResConsistent, Red` as in `evaluate_test.go` ~81). If `S5` moves, stop and record (BLOCKED): S8 on tier `a` would be competing with S5.
4. gofmt both files.

## Proof

```
.dev\toolchains\go\bin\go.exe test ./internal/investigate -run "TestSigPayloadUp|TestS8OnStepScenario|TestEvaluate"
.dev\toolchains\go\bin\go.exe test ./internal/investigate/... -skip "^TestGolden$"
```

PASS.

## Depends on

44 (stub file, `SigPayloadUp`).
