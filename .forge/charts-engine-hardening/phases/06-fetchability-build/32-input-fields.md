# TSK-32 input-fields

risk: medium — `Input` is the engine's only door; a non-`omitempty` field would move every golden.

## Outcome

`investigate.Input` can carry imported SQL Server event rows, imported HTTPERR rows, and the IIS status and sub-status, without changing the output of any existing run.

## Write scope

- `internal/investigate/types.go` (single writer of this file in Phases 06–07)
- `internal/investigate/types_test.go` (create if missing, else append)

## Must not change

- Every existing field, tag and comment in `types.go`.
- Any golden in `testdata/investigate/`.
- Do not commit. Do not push. Never touch `sql/`.

## Changes

1. `types.go` `IISRow` (~90–95): add two fields after `Path`:
   ```go
   Status    string `json:"status,omitempty"`    // sc-status, e.g. "404"
   SubStatus string `json:"subStatus,omitempty"` // sc-substatus, e.g. "5"
   ```
2. After `IISRow` (~95) add:
   ```go
   // SQLEvent is one imported SQL Server event row (XE or error log), reduced to
   // its time and kind: "deadlock", "call", "blocked-process", "attention" or
   // "login-failed". DurationMs is set for "call" rows that carry one.
   type SQLEvent struct {
       T          time.Time `json:"t"`
       Kind       string    `json:"kind"`
       DurationMs *float64  `json:"durationMs,omitempty"`
   }

   // HTTPErrRow is one imported HTTPERR row: its time, its reason phrase
   // (e.g. "Timer_HeaderWait") and its status, which may be empty.
   type HTTPErrRow struct {
       T      time.Time `json:"t"`
       Reason string    `json:"reason"`
       Status string    `json:"status,omitempty"`
   }
   ```
3. `Input` (~105–147): after `IIS` (~122) add:
   ```go
   // SQLPresent is true when a validated SQL Server event source (sql-xe or
   // sql-errorlog) is in the case; SQLEvents are its rows in the windows.
   SQLPresent bool       `json:"sqlPresent,omitempty"`
   SQLEvents  []SQLEvent `json:"sqlEvents,omitempty"`
   // SQLKinds names the event kinds the SQL sources can hold at all (for
   // example "attention" only when an XE file listed it), so an absent kind
   // reads unknown, not zero.
   SQLKinds map[string]bool `json:"sqlKinds,omitempty"`
   // HTTPErrPresent is true when a validated iis-httperr source is in the case;
   // HTTPErr are its rows in the windows.
   HTTPErrPresent bool         `json:"httpErrPresent,omitempty"`
   HTTPErr        []HTTPErrRow `json:"httpErr,omitempty"`
   // IISSubStatus is true when the IIS rows carry sc-substatus at all.
   IISSubStatus bool `json:"iisSubStatus,omitempty"`
   ```
4. `types_test.go`: add `TestInputNewFieldsOmitted`: `json.Marshal(Input{})` contains none of `sqlPresent`, `sqlEvents`, `sqlKinds`, `httpErrPresent`, `httpErr`, `iisSubStatus`; `json.Marshal(IISRow{})` contains neither `status` nor `subStatus`.
5. gofmt both files.

## Proof

```
.dev\toolchains\go\bin\go.exe test ./internal/investigate/...
```

PASS, including `TestGolden` unchanged (no `-update`).

## Depends on

Phase 02 complete (ordering only).
