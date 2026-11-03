# TSK-50 s12-drain

risk: medium — two routes to Yes (HTTPERR rows, or time outside the app rising alone); the second must not fire when the app itself slowed.

## Outcome

`drain` reads:

- **Yes** when HTTPERR rows with reason `Timer_MinBytesPerSecond` or `Connection_Dropped` come in a burst in the incident; or when time outside the application rose (`o_large` Yes) while the application P95 did not (`p95_up` No).
- **No** when a complete HTTPERR log shows no such burst and `o_large` is No.
- **Unknown** otherwise: missing `httperr` when no HTTPERR log was imported and the IIS route did not answer; if `o_large` itself is missing, carry its `Missing` source (`iis` or `audit`).

## Write scope

- `internal/investigate/sus_s12.go` (replace the stub body)
- `internal/investigate/sus_s12_test.go` (new)

## Must not change

- Every other file; the rules for `o_large` and `p95_up`.
- Do not commit. Do not push. Never touch `sql/`.

## Changes

1. `sus_s12.go`:
   ```go
   // sigDrain reads a slow client or proxy (S12): HTTPERR rows where HTTP.sys
   // dropped a slow reader, or IIS time outside the application rising while the
   // application's own P95 did not. It runs after o_large and p95_up are set.
   func sigDrain(in Input, f Facts, th Thresholds, fl map[string]Signal) Signal {
       hs, _ := httpErrBurst(in, th, false, "HTTPERR slow-client rows", "Timer_MinBytesPerSecond", "Connection_Dropped")
       if hs.Value == Yes {
           return hs
       }
       o, p := fl[SigOLarge], fl[SigP95Up]
       if o.Value == Yes && p.Value == No {
           return sig(Yes, "Time outside the application rose while the application P95 did not.")
       }
       if hs.Value == No && o.Value == No {
           return sig(No, "No slow-client HTTPERR burst, and time outside the application did not rise.")
       }
       if hs.Value == No {
           // HTTPERR is complete and quiet; the outside-time route could not decide.
           return sig(Unknown, "No slow-client HTTPERR burst, but time outside the application could not be read alone.")
       }
       if o.Value == Unknown && o.Missing != "" {
           return missing(o.Missing, "Neither an HTTPERR log nor time outside the application could be read.")
       }
       return missing("httperr", "No HTTPERR log was imported, and time outside the application does not show a slow client alone.")
   }
   ```
   Note the order: an HTTPERR burst wins; then the outside-time route; a `No` needs both a complete HTTPERR log and `o_large` No.
2. `sus_s12_test.go` `TestSigDrain` (call `sigDrain` directly with a hand-made `fl` map; `Facts{}` is enough):
   - no HTTPERR, `o_large` Yes, `p95_up` No → `Yes`;
   - no HTTPERR, `o_large` Yes, `p95_up` Yes → `Unknown`, `Missing == "httperr"`;
   - no HTTPERR, `o_large` Unknown with `Missing "iis"` → `Missing == "iis"`;
   - HTTPERR present with 3 `Connection_Dropped` rows in 4 minutes → `Yes`;
   - HTTPERR present, no rows, `o_large` No → `No`;
   - HTTPERR present, no rows, `o_large` Yes, `p95_up` Yes → `Unknown`, `Missing == ""`.
3. gofmt both files.

## Proof

```
.dev\toolchains\go\bin\go.exe test ./internal/investigate -run "TestSigDrain"
.dev\toolchains\go\bin\go.exe test ./internal/investigate/... -skip "^TestGolden$"
```

PASS.

## Depends on

44 (stub, `httpErrBurst`, `SigDrain`), 35 (meaning only).
