# TSK-44 catalogue-skeleton

risk: high — the catalogue and the signal list are what every run is ranked by; nine new rows and six new signals land here in one go.

## Outcome

The catalogue lists the nine new suspects with their fixed words, S1 gains the Expect signal `queue_burst`, `CatalogEntry` gains `Cap`, six new signal names exist, and each new signal has a stub in its own file so TSK-45..50 can fill them in parallel. A shared HTTPERR burst reader exists. Until the stubs are filled, every new S-suspect reads "cannot test".

## Write scope

- `internal/investigate/catalog.go`
- `internal/investigate/signals.go`
- `internal/investigate/sus_burst.go` (new), `internal/investigate/sus_burst_test.go` (new)
- `internal/investigate/sus_s1.go`, `sus_s8.go`, `sus_s9.go`, `sus_s10.go`, `sus_s11.go`, `sus_s12.go` (new stubs)
- `internal/investigate/catalog_nine_test.go` (new)

## Must not change

- Any existing catalogue entry's text, tier, Must or flags, except S1 gaining `Expect`.
- Any existing signal's rule. `evaluate.go`, `thresholds.go`, `types.go`, `burst.go`.
- Do not commit. Do not push. Never touch `sql/`.

## Changes

1. `catalog.go` `CatalogEntry` (~25–47): after `Needs` add
   ```go
   // Cap is the highest colour the entry may take ("orange"); empty means no cap.
   Cap string `json:"cap,omitempty"`
   ```
2. S1 (~79–82): add `Expect: []string{SigQueueBurst},` after the `Must` field. Nothing else on S1 changes.
3. After S7 (~106), still inside the `out := []CatalogEntry{…}` literal, add (text exactly as below):
   ```go
   {ID: "S8", Group: GroupS, Title: "Payload capture or response-body handling slowdown", Tier: "a",
       Must:     []string{SigPayloadUp},
       Check:    "Audit PayloadRecordTime (P95) by route against the comparison period, then body size on the slowest routes.",
       Overturn: "PayloadRecordTime flat while the audit duration rose.",
       Needs:    "The API audit with PayloadRecordTime for the incident and comparison windows."},
   {ID: "S9", Group: GroupS, Title: "IIS request-filtering rejection", Tier: "o", Cap: "orange",
       Must:     []string{SigFilterReject},
       Check:    "Count IIS rows by status and sub-status (404.5 to 404.8, 404.11, 404.12, 404.14, 404.15, 404.18, 404.19, 413.1, 431) against the comparison period.",
       Overturn: "Those sub-status counts are unchanged.",
       Needs:    "IIS logs that record sc-substatus, for the incident and comparison windows."},
   {ID: "S10", Group: GroupS, Title: "Application pool unavailable or worker failure", Tier: "o",
       Must:     []string{SigAppPoolFail},
       Check:    "HTTPERR rows with AppOffline, AppShutdown, AppPoolTimer or Connection_Abandoned_By_AppPool, against the application-pool events of the host.",
       Overturn: "None of those reasons appear in a complete HTTPERR log.",
       Needs:    "HTTPERR log files for the incident and comparison windows."},
   {ID: "S11", Group: GroupS, Title: "HTTP.sys header or body receive timeout", Tier: "o",
       Must:     []string{SigRecvTimeout},
       Check:    "HTTPERR rows with Timer_HeaderWait or Timer_EntityBody per 5 minutes, by client address.",
       Overturn: "Those rows are rare, or no higher than the comparison period.",
       Needs:    "HTTPERR log files for the incident and comparison windows."},
   {ID: "S12", Group: GroupS, Title: "Slow client or proxy: response drain or disconnect", Tier: "o",
       Must:     []string{SigDrain},
       Check:    "HTTPERR rows with Timer_MinBytesPerSecond or Connection_Dropped per 5 minutes; otherwise IIS time taken minus the audit duration against the application time.",
       Overturn: "No such rows, and time outside the application did not rise.",
       Needs:    "HTTPERR log files and IIS log rows for the incident and comparison windows."},
   ```
4. After the D loop (~126–128), before the M slice:
   ```go
   out = append(out,
       lane("D16", GroupD, "SQL command timeout or cancellation burst", "sql",
           "Extended Events attention events per 5 minutes, with the statements they cancelled.",
           "Fewer than 3 attention events in any 5 minutes, and under 2 in 100 calls.",
           "Stored SQL Server events that include attention events for the window.", SigCLarge),
       lane("D17", GroupD, "SQL login or authentication storm", "sql",
           "Error 18456 and login attempts per 5 minutes, by client and state.",
           "Login attempts are near the comparison period.",
           "The SQL Server error log, with failed logins, for the window.", SigCLarge),
   )
   ```
5. After the M loop (~139–141), before the H slice:
   ```go
   out = append(out,
       lane("M11", GroupM, "MongoDB spill to disk", "mongo",
           "Profiler entries with usedDisk set, by query shape.",
           "usedDisk on fewer than 3 operations, or operation time flat.",
           "MongoDB profiler switched on, so usedDisk is kept.", SigALarge),
       lane("M12", GroupM, "MongoDB incoming-connection rejection", "mongo",
           "Server status connections.rejected between samples, with the connection limit.",
           "connections.rejected did not move.",
           "MongoDB 6.3 or later with server status sampled (connections.rejected).", SigALarge),
   )
   ```
6. `signals.go` const block (~11–29), after `SigChangesLead`:
   ```go
   SigQueueBurst   = "queue_burst"   // HTTPERR queue-full or app-pool-timer rows came in a burst
   SigPayloadUp    = "payload_up"    // PayloadRecordTime P95 rose by the change mark
   SigFilterReject = "filter_reject" // IIS request-filtering rejections rose
   SigAppPoolFail  = "app_pool_fail" // HTTPERR rows show the app pool unavailable
   SigRecvTimeout  = "recv_timeout"  // HTTPERR header or body receive timeouts rose
   SigDrain        = "drain"         // slow-client rows, or time outside the app rose alone
   ```
   Then gofmt aligns the block.
7. `ComputeSignals`, just before `return s` (~248):
   ```go
   fl[SigQueueBurst] = sigQueueBurst(in, f, th, fl)
   fl[SigPayloadUp] = sigPayloadUp(in, f, th, fl)
   fl[SigFilterReject] = sigFilterReject(in, f, th, fl)
   fl[SigAppPoolFail] = sigAppPoolFail(in, f, th, fl)
   fl[SigRecvTimeout] = sigRecvTimeout(in, f, th, fl)
   fl[SigDrain] = sigDrain(in, f, th, fl)
   ```
   They run last so `sigDrain` can read `fl[SigOLarge]` and `fl[SigP95Up]`.
8. Six stub files, one function each, all the same shape (example for S1):
   ```go
   package investigate

   // sigQueueBurst reads HTTPERR queue rows for S1. Filled in by TSK-45.
   func sigQueueBurst(in Input, f Facts, th Thresholds, fl map[string]Signal) Signal {
       return sig(Unknown, "This reading is not built yet.")
   }
   ```
   `sus_s1.go` sigQueueBurst, `sus_s8.go` sigPayloadUp, `sus_s9.go` sigFilterReject, `sus_s10.go` sigAppPoolFail, `sus_s11.go` sigRecvTimeout, `sus_s12.go` sigDrain.
9. `sus_burst.go`:
   ```go
   package investigate

   import "fmt"

   // iisRowsIn counts IIS rows whose end falls in w.
   func iisRowsIn(in Input, w Window) int

   // httpErrBurst reads HTTPERR rows whose reason is one of reasons. A burst is at
   // least BurstMin5 rows in some 5 minutes of the incident, or, when the incident
   // holds at least BurstMinRequests IIS rows, at least BurstShare of them. With
   // rise, the incident rate must also be at least BurstRise times the reference
   // rate (or the reference must hold none). what names the rows in the reason.
   func httpErrBurst(in Input, th Thresholds, rise bool, what string, reasons ...string) (Signal, BurstCount)
   ```
   Body:
   - `if !in.HTTPErrPresent { return missing("httperr", "No HTTPERR log was imported for these windows."), BurstCount{} }`
   - `b := CountBurst(httpErrTimes(in, reasons...), in.Incident, in.Reference)`; `n := iisRowsIn(in, in.Incident)`.
   - `burst := b.Peak5 >= th.BurstMin5 || (n >= th.BurstMinRequests && float64(b.Inc) >= th.BurstShare*float64(n))`.
   - `rose := !rise || b.RefRate() == 0 || b.IncRate() >= th.BurstRise*b.RefRate()`.
   - Yes: `fmt.Sprintf("%s: %d in the incident, at most %d in 5 minutes.", what, b.Inc, b.Peak5)`.
   - No when `!burst`: `fmt.Sprintf("%s: %d in the incident, under the burst mark.", what, b.Inc)`.
   - No when `burst && !rose`: `fmt.Sprintf("%s: %d in the incident, no higher than the comparison period.", what, b.Inc)`.
   Why strings must pass `LintCausal` (no "because", "due to", "caused" and the rest of the list in `readiness_test.go`).
10. `sus_burst_test.go` `TestHTTPErrBurst` (build `Input` by hand with `stdWindows()` from the existing tests; `Current()` thresholds):
    - `HTTPErrPresent false` → `Unknown`, `Missing == "httperr"`;
    - 3 rows in 4 minutes of the incident → `Yes`, `Peak5 == 3`;
    - 2 rows 20 minutes apart, no IIS rows → `No`;
    - 4 rows 10 minutes apart with 300 incident IIS rows → `Yes` (share);
    - 2 rows 10 minutes apart with 300 incident IIS rows → `No` (2 < 3);
    - `rise=true`, 3 rows in 4 minutes, reference holding the same rate per minute → `No`;
    - reason case folds (`queuefull` matches `QueueFull`).
11. `catalog_nine_test.go` `TestCatalogNine`:
    - each of `S8 S9 S10 S11 S12 D16 D17 M11 M12` appears exactly once in `Catalog()`;
    - order: S8..S12 right after S7; D16, D17 right after D15; M11, M12 right after M10;
    - `S9.Cap == "orange"`, every other entry `Cap == ""`;
    - `S1.Expect` contains `SigQueueBurst` and `S1.Must` is still `[SigOLarge]`;
    - D16, D17 `LaneRequired`, tier `sql`, Must `[SigCLarge]`; M11, M12 `LaneRequired`, tier `mongo`, Must `[SigALarge]`;
    - S8 tier `a`; S9..S12 tier `o`; none of S8..S12 `LaneRequired`.
12. gofmt every touched file.

## Proof

```
.dev\toolchains\go\bin\go.exe test ./internal/investigate -run "TestCatalogNine|TestHTTPErrBurst|TestFixedWords|TestLintCausal"
.dev\toolchains\go\bin\go.exe test ./internal/investigate/... -skip "^TestGolden$"
.dev\toolchains\go\bin\go.exe build ./...
```

PASS. If an existing evaluate or readiness test now fails, record its name and message in `progress.md` and stop (BLOCKED); do not change it here.

## Depends on

43 (`BurstMin5` … `BurstRise`), 32 (`HTTPErrPresent`, `IISRow`), 33 (`CountBurst`, `httpErrTimes`).
