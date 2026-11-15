# P-20 Saved Query Store day carries busy time and block length

model: sonnet, effort: high. Reason: derives two numbers from a saved series whose interval length is not on the wire.
risk: medium. Review batch: RB-2, then RB-6. Wave: W4. Requirements: R-04, R-05.

## Outcome
When the database lane is drawn from a saved Query Store day, each bucket that holds a saved block carries `busyMs`, and the lane carries `blockSeconds`, so the UI can draw the lane in honest steps.

## Write scope
- `internal/server/tracing.go` (struct `tracingLane` only: one new field)
- `internal/server/tracing_sqllive.go`
- `internal/server/tracing_sqllive_test.go`

## Must not change
- No SQL connection is opened by the timeline: this code reads saved lane days only. `layerSQLLane` behaviour (lines 32-54). Reason codes `held-sql`, `layered-sql`. `dbhealth` package.

## Changes
1. `tracing.go`, in `tracingLane` (line 432), add as last field:
```go
	// BlockSeconds is the length of the blocks this lane's source saved its numbers in, when it is
	// longer than the bucket (Query Store). Zero means not known or same as the bucket.
	BlockSeconds int `json:"blockSeconds,omitempty"`
```
2. `tracing_sqllive.go`, `tracingHeldSQLLane` (lines 59-147): the series is `[]dbhealth.LaneSeriesPoint{T, Execs, AvgMs, MaxMs}`. For each point set the bucket's `BusyMs = Execs * AvgMs` (milliseconds). When two points land in one bucket, add them.
3. Block length: if the saved day has `QueryStore.IntervalMinutes > 0` (P-16), use it × 60. Else the smallest positive gap between consecutive `T` values, accepted only if it is one of 60, 300, 600, 900, 1800, 3600, 86400 (minus nothing; exact). With one point and no interval, leave `BlockSeconds` 0 (the UI then assumes 900 and says "saved in blocks").
4. The busy time of a block belongs to the whole block, not to the one bucket that contains `T`. Do NOT spread it server-side; the web model (`regroupBlocks` in P-06) spreads it using `blockSeconds`. Add a one-line comment saying so.
5. Tests: series of three points 15 min apart with `Execs 100, AvgMs 20` → those buckets have `busyMs` 2000, `blockSeconds` 900; a single point without interval → `blockSeconds` omitted from JSON; `IntervalMinutes 60` wins over the gap; a point with `Execs 0` gives `busyMs` 0 (read, nothing ran), not nil.

## Proof
- [ ] `export PATH="$PWD/.dev/toolchains/go/bin:$PATH" && go build ./... && go vet ./internal/server/` → no output.
- [ ] `go test ./internal/server/ -run 'SQL|Held' -count=1` → `ok`.
- [ ] `go test ./internal/server/ -count=1` (background) → `ok`.
- [ ] `git diff -- internal/server/tracing_sqllive.go | Select-String -Pattern 'sql\.Open|QueryContext|ExecContext'` → no match.
- [ ] Demo path: after P-22. Live path: P-46 on a saved day from a real database.

## Depends on
P-14 (field `BusyMs`), P-16 (`QueryStore`). Server package order: after P-19.
