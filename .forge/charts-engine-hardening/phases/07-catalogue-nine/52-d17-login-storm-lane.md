# TSK-52 d17-login-storm-lane

risk: low — a second lane answer in the same file, on error-log rows that already reach `Input`.

## Outcome

`ApplySQLEvents` also answers D17 (SQL login or authentication storm) from error-log `login-failed` rows:

- only when `SQLKinds["login-failed"]` is true (an error-log source is in the case);
- **Yes** when the incident holds at least 5 failed logins in some 5 minutes and the incident rate is at least 3 times the comparison rate (or the comparison held none);
- **No** when the incident holds none, or its rate is no higher than the comparison rate;
- otherwise unset.

## Write scope

- `internal/investigate/sqllane.go`
- `internal/investigate/sqllane_test.go` (append)

## Must not change

- `sqlD2`, `sqlD16`, their consts and tests.
- `catalog.go`, `thresholds.go`.
- Do not commit. Do not push. Never touch `sql/`.

## Changes

1. Const block: add
   ```go
   // D17 marks (error 18456, failed logins).
   sqlLoginMin5 = 5   // failed logins in any 5 minutes of the incident
   sqlLoginRise = 3.0 // times the comparison rate per minute
   ```
2. `ApplySQLEvents`: after the D16 line add `in.setLane("D17", sqlD17(*in))`.
3. Add:
   ```go
   // sqlD17 reads failed-login rows from the error log (D17).
   func sqlD17(in Input) Tri {
       if !in.SQLKinds["login-failed"] {
           return Unknown
       }
       b := CountBurst(sqlTimes(in, "login-failed"), in.Incident, in.Reference)
       switch {
       case b.Peak5 >= sqlLoginMin5 && (b.RefRate() == 0 || b.IncRate() >= sqlLoginRise*b.RefRate()):
           return Yes
       case b.Inc == 0 || b.IncRate() <= b.RefRate():
           return No
       }
       return Unknown
   }
   ```
4. `sqllane_test.go` `TestApplySQLEventsD17`:
   - no `login-failed` kind → absent;
   - 6 failed logins within 4 minutes, none in the reference → `Yes`;
   - none → `No`;
   - 6 in a 60-minute incident, 12 in a 120-minute reference (same rate) → `No`;
   - 6 within 4 minutes, reference rate half the incident rate → absent (rose, but not 3 times);
   - `Lanes["D17"] = Yes` before, none → stays `Yes`.
   Re-run the D2 and D16 tests unchanged.
5. gofmt both files.

## Proof

```
.dev\toolchains\go\bin\go.exe test ./internal/investigate -run "TestApplySQLEvents"
.dev\toolchains\go\bin\go.exe test ./internal/investigate/... -skip "^TestGolden$"
```

PASS.

## Depends on

51 (same file, writes in order).
