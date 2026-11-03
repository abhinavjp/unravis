# TSK-45 s1-queue-burst

risk: medium — S1 is an existing suspect; its new Expect signal must only ever lower it to "partly" on a complete HTTPERR log, never on a missing one.

## Outcome

`queue_burst` reads Yes when HTTPERR rows with reason `QueueFull`, `Timer_AppPool` or `AppPoolTimer` come in a burst during the incident, No when a complete HTTPERR log shows no burst, and missing (`httperr`) without an HTTPERR log. S1's result is unchanged without HTTPERR data.

## Write scope

- `internal/investigate/sus_s1.go` (replace the stub body)
- `internal/investigate/sus_s1_test.go` (new)

## Must not change

- Every other file, in particular `catalog.go`, `signals.go`, `sus_burst.go`, `evaluate.go`.
- Do not commit. Do not push. Never touch `sql/`.

## Changes

1. `sus_s1.go`, replace the stub:
   ```go
   // sigQueueBurst reads HTTPERR rows that show the HTTP.sys queue full or the
   // app-pool queue timing out (S1). No rise against the comparison period is
   // required: these rows are rare on a healthy server.
   func sigQueueBurst(in Input, f Facts, th Thresholds, fl map[string]Signal) Signal {
       s, _ := httpErrBurst(in, th, false, "HTTPERR queue rows", "QueueFull", "Timer_AppPool", "AppPoolTimer")
       return s
   }
   ```
2. `sus_s1_test.go` `TestSigQueueBurst` (build `Input` with `stdWindows()`, `th := Current()`):
   - `HTTPErrPresent false` → `Unknown`, `Missing == "httperr"`;
   - 3 `QueueFull` rows within 4 minutes of the incident → `Yes`;
   - 1 `QueueFull` row → `No`;
   - 3 `Timer_HeaderWait` rows within 4 minutes → `No` (not a queue reason).
3. `TestS1KeepsResultWithoutHTTPErr`: `r := Analyze(stepScenario(mod{from: at(10, 20), to: at(11, 30), addO: 400}))` → `wantSuspect(t, r, "S1", ResConsistent, Red)` (same as `evaluate_test.go` ~87). Then the same input with `HTTPErrPresent = true` and no rows → `wantSuspect(t, r2, "S1", ResPartly, Orange)` (an Expect `No` sets `ResPartly`/Orange in `evaluateOne` ~263–264). Build the second input with `in := stepScenario(...)`, set the field, then `Analyze(in)`.
4. gofmt both files.

## Proof

```
.dev\toolchains\go\bin\go.exe test ./internal/investigate -run "TestSigQueueBurst|TestS1KeepsResultWithoutHTTPErr|TestEvaluate"
.dev\toolchains\go\bin\go.exe test ./internal/investigate/... -skip "^TestGolden$"
```

PASS.

## Depends on

44 (stub file, `httpErrBurst`, `SigQueueBurst`), 35 (HTTPERR rows reach `Input`; for meaning only, not for build).
