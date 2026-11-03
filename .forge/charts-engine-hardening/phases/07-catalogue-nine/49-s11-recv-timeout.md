# TSK-49 s11-recv-timeout

risk: low — one HTTPERR reader on the shared helper, with a rise check.

## Outcome

`recv_timeout` reads Yes when HTTPERR rows with reason `Timer_HeaderWait` or `Timer_EntityBody` come in a burst during the incident and rose over the comparison rate, No when a complete HTTPERR log shows no such burst or no rise, missing `httperr` without an HTTPERR log.

## Write scope

- `internal/investigate/sus_s11.go` (replace the stub body)
- `internal/investigate/sus_s11_test.go` (new)

## Must not change

- Every other file.
- Do not commit. Do not push. Never touch `sql/`.

## Changes

1. `sus_s11.go`:
   ```go
   // sigRecvTimeout reads HTTPERR rows where HTTP.sys gave up waiting for a
   // request's headers or body (S11). These rows are common at a low rate on a
   // public server, so they must also rise over the comparison period.
   func sigRecvTimeout(in Input, f Facts, th Thresholds, fl map[string]Signal) Signal {
       s, _ := httpErrBurst(in, th, true, "HTTPERR receive-timeout rows", "Timer_HeaderWait", "Timer_EntityBody")
       return s
   }
   ```
2. `sus_s11_test.go` `TestSigRecvTimeout`: not present → `Missing == "httperr"`; 4 `Timer_HeaderWait` rows within 3 minutes of the incident, none in the reference → `Yes`; same 4 rows plus a reference of the same length holding 8 such rows → `No`; 1 `Timer_EntityBody` row → `No`.
3. gofmt both files.

## Proof

```
.dev\toolchains\go\bin\go.exe test ./internal/investigate -run "TestSigRecvTimeout"
.dev\toolchains\go\bin\go.exe test ./internal/investigate/... -skip "^TestGolden$"
```

PASS.

## Depends on

44 (stub, `httpErrBurst`, `SigRecvTimeout`), 35 (meaning only).
