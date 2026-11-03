# TSK-35 server-imported-events

risk: medium — a new read of case events on every investigation; a cap or source mistake shows wrong evidence.

## Outcome

`buildInvestigationInput` fills `SQLPresent/SQLEvents/SQLKinds`, `HTTPErrPresent/HTTPErr`, and the IIS `Status/SubStatus/IISSubStatus` from validated imported sources, then calls `in.ApplySQLEvents()`. A run without those sources is unchanged.

## Write scope

- `internal/server/investigation_events.go` (new)
- `internal/server/investigation_events_test.go` (new)
- `internal/server/investigation.go` (one call line after ~375, and the `IISRow` literal at ~421)

## Must not change

- The IIS read loop (~388–411), its cap warning and its early return, except the `IISRow` literal at ~421.
- `applyLaneRuns`, any handler, any web file.
- Do not commit. Do not push. Never touch `sql/`.

## Changes

1. Create `investigation_events.go` with:
   ```go
   // Imported event sources the engine reads directly (Phase 06, 6a and 6b).
   const (
       sourceSQLXE      = "sql-xe"
       sourceSQLErrLog  = "sql-errorlog"
       sourceHTTPErr    = "iis-httperr"
       kindProbeLookback = 30 * 24 * time.Hour
   )

   // readImportedEvents adds SQL Server and HTTPERR rows from validated sources to
   // in. A read that hits the event cap is dropped with a warning, the same rule as
   // the IIS read: a partial list would read as a missing burst.
   func readImportedEvents(ctx context.Context, c casestore.Case, body investigationRequest, sources []caseSource, in *investigate.Input, read *investigationRead) error
   ```
   Use the element type `caseSources` really returns (read its signature first; the name above is a placeholder).
2. Body of `readImportedEvents`:
   - `has := map[string]bool{}`; for each source with `Validated`, `has[s.Name] = true`.
   - A local `readKinds(src string, kinds []string) ([]evidence.Event, bool, error)` that copies the IIS loop exactly (~388–411): one `c.Events` per `body.ranges()` stretch with `Window: importer.Window{From: rg.Start, To: rg.End}` (no call-trace lookback), `Sources: []string{src}`, `Kinds: kinds`, `Limit: casestore.MaxEventQuery`; dedupe by `EvidenceID`; on a cap hit return `ok=false`.
   - **SQL**: if `has[sourceSQLXE] || has[sourceSQLErrLog]`:
     - XE kinds `{"deadlock","call","blocked-process","attention"}` from `sql-xe`; error-log kind `{"login-failed"}` from `sql-errorlog`. Read each present source.
     - On a cap hit: `read.Warnings = append(read.Warnings, "There are too many SQL Server event rows in these windows to read in full, so they were not used.")` and leave `SQLPresent` false.
     - Else `in.SQLPresent = true`; each event → `investigate.SQLEvent{T: e.Start.UTC(), Kind: e.Kind, DurationMs: e.DurationMs}`. For `call` rows use `e.End.UTC()` as `T` (completion time).
     - `in.SQLKinds`: `deadlock` = `has[sourceSQLXE]` (system_health always records deadlocks); `login-failed` = `has[sourceSQLErrLog]`; `call`, `blocked-process`, `attention` = a probe `c.Events` with `Limit: 1`, `Kinds: []string{k}`, `Sources: []string{sourceSQLXE}`, window from the earliest range start minus `kindProbeLookback` to the latest range end, returns at least one row. (`system_health` never captures these three, so a zero with no probe hit must read unknown.)
     - Then `in.ApplySQLEvents()`.
   - **HTTPERR**: if `has[sourceHTTPErr]`: read kind `httperr`; on a cap hit add `"There are too many HTTPERR rows in these windows to read in full, so they were not used."` and stop; else `in.HTTPErrPresent = true` and each event → `investigate.HTTPErrRow{T: e.Start.UTC(), Reason: e.Attrs["reason"], Status: e.Attrs["status"]}`.
3. `investigation.go`: right after the `caseSources` error check (~375), before the `readable` loop, add:
   ```go
   if err := readImportedEvents(ctx, c, body, sources, &in, read); err != nil {
       return in, err
   }
   ```
4. `investigation.go` ~421: extend the literal with `Status: e.Attrs["status"], SubStatus: e.Attrs["substatus"]`; after the loop (~422) set `in.IISSubStatus = true` if any appended row has a non-empty `SubStatus`. Check `internal/importer/iis/w3c.go` for the exact attr keys (`status`, `substatus`) before writing.
5. `investigation_events_test.go`: find the existing server test that builds a case with IIS events for `buildInvestigationInput` (grep `buildInvestigationInput(` in `internal/server/*_test.go`) and copy its case set-up. Tests:
   - `TestReadImportedEventsNone`: no SQL/HTTPERR source → `SQLPresent` and `HTTPErrPresent` false, `Lanes` has no `D2`.
   - `TestReadImportedEventsSQL`: a validated `sql-xe` source with 6 `deadlock` rows in the incident → `SQLPresent`, 6 `SQLEvents`, `SQLKinds["deadlock"]`, `!SQLKinds["attention"]`, `Lanes["D2"] == Yes`.
   - `TestReadImportedEventsHTTPErr`: a validated `iis-httperr` source with 2 rows (`Timer_HeaderWait`) → `HTTPErrPresent`, `HTTPErr[0].Reason == "Timer_HeaderWait"`.
   - An unvalidated `sql-xe` source is ignored.
6. gofmt the three files.

## Proof

```
.dev\toolchains\go\bin\go.exe test ./internal/server/... -run "TestReadImportedEvents"
.dev\toolchains\go\bin\go.exe test ./internal/server/... ./internal/investigate/...
.dev\toolchains\go\bin\go.exe build ./...
```

PASS.

## Depends on

29, 30, 31 (kinds exist), 34 (`ApplySQLEvents`).
