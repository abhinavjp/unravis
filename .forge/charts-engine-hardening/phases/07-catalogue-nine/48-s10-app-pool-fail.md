# TSK-48 s10-app-pool-fail

risk: low — one HTTPERR reader on the shared helper; any one such row is already a finding.

## Outcome

`app_pool_fail` reads Yes when the incident holds at least one HTTPERR row with reason `AppOffline`, `AppShutdown`, `AppPoolTimer` or `Connection_Abandoned_By_AppPool`, No when a complete HTTPERR log holds none, and missing `httperr` without an HTTPERR log.

## Write scope

- `internal/investigate/sus_s10.go` (replace the stub body)
- `internal/investigate/sus_s10_test.go` (new)

## Must not change

- Every other file.
- Do not commit. Do not push. Never touch `sql/`.

## Changes

1. `sus_s10.go`:
   ```go
   // appPoolReasons are HTTPERR reasons that show the application pool down or
   // its worker gone.
   var appPoolReasons = []string{"AppOffline", "AppShutdown", "AppPoolTimer", "Connection_Abandoned_By_AppPool"}

   // sigAppPoolFail reads HTTPERR rows that show the app pool unavailable (S10).
   // One row in the incident is enough: a healthy pool writes none.
   func sigAppPoolFail(in Input, f Facts, th Thresholds, fl map[string]Signal) Signal {
       if !in.HTTPErrPresent {
           return missing("httperr", "No HTTPERR log was imported for these windows.")
       }
       b := CountBurst(httpErrTimes(in, appPoolReasons...), in.Incident, in.Reference)
       if b.Inc >= 1 {
           return sig(Yes, fmt.Sprintf("%d HTTPERR rows show the application pool unavailable in the incident.", b.Inc))
       }
       return sig(No, "No HTTPERR row shows the application pool unavailable in the incident.")
   }
   ```
2. `sus_s10_test.go` `TestSigAppPoolFail`: not present → `Missing == "httperr"`; present, no rows → `No`; one `AppOffline` row in the incident → `Yes`; one `connection_abandoned_by_apppool` row (lower case) → `Yes`; one `AppShutdown` row only in the reference → `No`.
3. gofmt both files.

## Proof

```
.dev\toolchains\go\bin\go.exe test ./internal/investigate -run "TestSigAppPoolFail"
.dev\toolchains\go\bin\go.exe test ./internal/investigate/... -skip "^TestGolden$"
```

PASS.

## Depends on

44 (stub, `SigAppPoolFail`), 35 (HTTPERR rows reach `Input`; meaning only).
