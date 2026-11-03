# TSK-29 xe-call-kind

risk: medium — a new importer kind with a microsecond duration; a wrong unit makes every call 1000× slow.

## Outcome

The XE importer keeps `rpc_completed` and `sql_batch_completed` rows as kind `call`, with a duration in ms read only from an explicit microsecond column. The case export accepts the new kind and event names. The scrub is unchanged. This is FETCHABILITY-AUDIT B4 (completed-call source), first half.

## Write scope

- `internal/importer/sqlserver/xe.go`
- `internal/importer/sqlserver/xe_test.go`
- `internal/evidence/caseexport_enums.go` (line ~64 and ~270–272 only)

## Must not change

- `xeScrubFragments` (~63–67) and `unscrubbed()`: not one fragment added or removed.
- The wait-duration parse (~393–403) and the meaning of the existing `duration` / `duration_ms` roles (both stay ms, for waits).
- Any other importer, `collect/wording.go`, any web file.
- Do not commit. Do not push. Never touch `sql/`.

## Changes

1. `xe.go` ~35–41: add `KindCall = "call"` to the kinds const block.
2. `xe.go` ~45–54 `xeKinds`: add `"rpc_completed": KindCall,` and `"sql_batch_completed": KindCall,`.
3. `xe.go` ~71–86 `xeRoles`: add `"duration_us": "duration_us",` and `"duration_microseconds": "duration_us",`. (Native XE `duration` on completed events is microseconds; the existing `duration` role is ms for waits. Calls read **only** `duration_us`, so no unit is guessed.)
4. `xe.go` event switch ~379–452: add, before `case KindNonYielding:`,
   ```go
   case KindCall:
       e.Severity = "info"
       if d := cell(rec, "duration_us"); d != "" {
           us, err := strconv.ParseFloat(d, 64)
           if err != nil || us < 0 || math.IsNaN(us) || math.IsInf(us, 0) || us > 7*24*3600*1e6 {
               s.reject(line, importer.ReasonBadNumber)
               continue
           }
           ms := us / 1000
           e.DurationMs, e.Unit = ptr(ms), "ms"
           e.Start = ts.Add(-time.Duration(ms * float64(time.Millisecond)))
       }
   ```
   Completed events fire at the end, so `Start = ts - duration`, the same as waits ~400–402. Coverage still grows with `ts` only (`s.seen(ts)` ~455 is unchanged). A call row with no `duration_us` cell is kept with no duration (it still counts for a burst).
5. `xe.go` header comment of `xeKinds` (~43–44): say the allowlist now also holds two completed-call events, which `system_health` does not capture; they come only from a custom session export.
6. `caseexport_enums.go` ~64: `"sql-xe": set("wait", "diagnostics", "error", "non-yielding", "deadlock", "call"),`.
7. `caseexport_enums.go` ~270–272 `xeEvents`: append `"rpc_completed", "sql_batch_completed"` inside the `fold(...)` call.
8. `xe_test.go`: read the existing test that lists wanted kinds (~86) and copy its CSV-building helper. Add `TestXECallKind`:
   - a CSV with `timestamp_utc,name,duration_us,server_instance_name` and rows `rpc_completed` with `2500000` and `sql_batch_completed` with `1500`;
   - assert kind `call`, `DurationMs` 2500 and 1.5, `Start` = row time minus the duration, unit `ms`;
   - a row with `duration_us` = `-1` is rejected with `ReasonBadNumber`;
   - a header with `sql_text` still quarantines (scrub unchanged).
   Add `call` to the wanted-kinds list at ~86 only if that test enumerates every kind; otherwise leave it.
9. `.dev\toolchains\go\bin\gofmt.exe -w internal/importer/sqlserver/xe.go internal/importer/sqlserver/xe_test.go internal/evidence/caseexport_enums.go`.

## Proof

```
.dev\toolchains\go\bin\go.exe test ./internal/importer/sqlserver/... ./internal/evidence/...
.dev\toolchains\go\bin\go.exe build ./...
```

PASS. `git diff internal/importer/sqlserver/xe.go` shows no change inside `xeScrubFragments`.

## Depends on

Phase 02 complete (no file overlap; ordering only).
