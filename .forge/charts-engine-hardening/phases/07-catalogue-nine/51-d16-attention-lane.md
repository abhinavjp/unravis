# TSK-51 d16-attention-lane

risk: medium — a new lane answer from SQL attention rows; reading "no rows" as No when the session never captured attention would be a false green.

## Outcome

`ApplySQLEvents` also answers D16 (SQL command timeout or cancellation burst):

- only when `SQLKinds["attention"]` is true (the XE source can hold attention rows at all);
- **Yes** when the incident holds at least 3 attention rows in some 5 minutes, or, when `SQLKinds["call"]` is true and the incident holds at least 50 completed calls, attention rows are at least 2 in 100 of those calls;
- **No** when the incident holds no attention row;
- otherwise unset.

## Write scope

- `internal/investigate/sqllane.go`
- `internal/investigate/sqllane_test.go` (append)

## Must not change

- `sqlD2`, its consts and `TestApplySQLEventsD2`.
- `catalog.go`, `thresholds.go` (marks are lane-local, so `Version` does not move).
- Do not commit. Do not push. Never touch `sql/`.

## Changes

1. Const block of `sqllane.go`: add
   ```go
   // D16 marks (attention events = client-side timeouts or cancels).
   sqlAttnMin5     = 3    // attention rows in any 5 minutes of the incident
   sqlAttnShare    = 0.02 // or this share of completed calls ...
   sqlAttnMinCalls = 50   // ... when the incident holds at least this many calls
   ```
2. `ApplySQLEvents`: after `in.setLane("D2", sqlD2(*in))` add `in.setLane("D16", sqlD16(*in))`.
3. Add:
   ```go
   // sqlD16 reads attention rows (D16). Without attention capture it is unknown.
   func sqlD16(in Input) Tri {
       if !in.SQLKinds["attention"] {
           return Unknown
       }
       b := CountBurst(sqlTimes(in, "attention"), in.Incident, in.Reference)
       if b.Peak5 >= sqlAttnMin5 {
           return Yes
       }
       if in.SQLKinds["call"] {
           calls := CountBurst(sqlTimes(in, "call"), in.Incident, in.Reference).Inc
           if calls >= sqlAttnMinCalls && float64(b.Inc) >= sqlAttnShare*float64(calls) {
               return Yes
           }
       }
       if b.Inc == 0 {
           return No
       }
       return Unknown
   }
   ```
   Note: unlike D2, a nil `SQLKinds` here means unknown (attention is never in `system_health`).
4. `sqllane_test.go` `TestApplySQLEventsD16`:
   - `SQLKinds{"deadlock": true}` and 5 attention rows → `D16` absent;
   - `SQLKinds{"attention": true}`, 3 attention rows within 4 minutes → `Yes`;
   - `attention` and `call` kinds, 100 calls and 2 attention rows 20 minutes apart → `Yes` (2 of 100);
   - same with 200 calls → absent (2 of 200 is under the mark, and there are rows);
   - `attention` kind, no rows → `No`;
   - `Lanes["D16"] = Yes` before, no rows → stays `Yes`.
   Re-run `TestApplySQLEventsD2` unchanged.
5. gofmt both files.

## Proof

```
.dev\toolchains\go\bin\go.exe test ./internal/investigate -run "TestApplySQLEvents"
.dev\toolchains\go\bin\go.exe test ./internal/investigate/... -skip "^TestGolden$"
```

PASS.

## Depends on

31 (attention kind), 34 (`sqllane.go`, `setLane`), 35 (`SQLKinds` filled), 44 (D16 in the catalogue).
