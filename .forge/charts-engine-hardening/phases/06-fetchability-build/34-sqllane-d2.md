# TSK-34 sqllane-d2

risk: medium — a new lane answer for an existing suspect (D2) moves it from Grey to a result when deadlock rows are imported.

## Outcome

`(*Input).ApplySQLEvents()` turns imported deadlock rows into the D2 lane answer. With no SQL source nothing changes, so no golden moves.

## Write scope

- `internal/investigate/sqllane.go` (new; later appended by TSK-51 and TSK-52)
- `internal/investigate/sqllane_test.go` (new)

## Must not change

- `catalog.go`, `signals.go`, `thresholds.go`, `evaluate.go`, `hostlane.go`, `types.go`.
- Do not commit. Do not push. Never touch `sql/`.

## Changes

1. Create `sqllane.go`:
   ```go
   package investigate

   // Lane-local marks for the SQL event lane (FETCHABILITY-AUDIT D10, 6a). They
   // are not in Thresholds, so they do not move Version.
   const (
       sqlDeadlockMin  = 5   // deadlocks in the incident window
       sqlDeadlockRise = 2.0 // times the reference rate per minute
   )

   // ApplySQLEvents writes the SQL event lane answers into in.Lanes. It never
   // overwrites a Yes another reader set. Without a SQL source it does nothing.
   func (in *Input) ApplySQLEvents() {
       if !in.SQLPresent {
           return
       }
       if in.Lanes == nil {
           in.Lanes = map[string]Tri{}
       }
       in.setLane("D2", sqlD2(*in))
       if !containsStr(in.LaneSources, "sql-events") {
           in.LaneSources = append(in.LaneSources, "sql-events")
       }
   }
   ```
2. In the same file:
   - `func (in *Input) setLane(id string, v Tri)`: if `v == Unknown` return; if `in.Lanes[id] == Yes` return; else `in.Lanes[id] = v`. Check first whether `hostlane.go` already has an equivalent helper; if it does, reuse it and do not add a second one. Same for `containsStr` (grep `func contains` in the package; reuse if present).
   - `func sqlD2(in Input) Tri`: if `in.SQLKinds` is non-nil and `!in.SQLKinds["deadlock"]` return `Unknown`. `b := CountBurst(sqlTimes(in, "deadlock"), in.Incident, in.Reference)`. If `b.Inc >= sqlDeadlockMin && (b.RefRate() == 0 || b.IncRate() >= sqlDeadlockRise*b.RefRate())` return `Yes`. If `b.Inc == 0` return `No`. Else return `Unknown` (a few deadlocks, not a storm, not a contradiction).
3. `sqllane_test.go`: table test `TestApplySQLEventsD2`:
   - `SQLPresent false` → `Lanes` stays nil;
   - 6 deadlocks in a 60-min incident, 0 in reference → `Lanes["D2"] == Yes`, `LaneSources` holds `sql-events` once;
   - 0 deadlocks → `No`;
   - 2 deadlocks → key absent;
   - 6 deadlocks in incident and 12 in a 120-min reference (same rate) → absent;
   - `Lanes["D2"] = Yes` set before, 0 deadlocks → stays `Yes`;
   - `SQLKinds{"login-failed":true}` only → absent.
4. gofmt both files.

## Proof

```
.dev\toolchains\go\bin\go.exe test ./internal/investigate -run TestApplySQLEventsD2
.dev\toolchains\go\bin\go.exe test ./internal/investigate/...
```

PASS; goldens unchanged.

## Depends on

33 (`CountBurst`, `sqlTimes`).
