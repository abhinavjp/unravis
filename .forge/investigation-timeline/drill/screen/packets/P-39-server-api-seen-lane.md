# P-39 Database lane fallback: "database time the API saw"

model: sonnet, effort: high. Reason: last link of the database lane precedence; must never hide a better source.
risk: high. Review batch: RB-2, then RB-6. Wave: W6. Requirements: R-05, R-07 (BL-013 fallback).

## Outcome
When the database lane has no saved live day and no Query Store file for the window, and the audit source stores database time per call, the lane shows that time per step with reason code `api-seen`. The screen then says it is the API's view of the database, not the whole database.

## Write scope
- `internal/server/tracing_apiseen.go` (new)
- `internal/server/tracing_apiseen_test.go` (new)
- `internal/server/tracing.go` (one call site in `caseTracing`, directly after the `qs-file` call site from P-21)

## Must not change
- Precedence: saved live day (`held-sql`, layered) > Query Store file (`qs-file`) > `api-seen` > nothing. This packet only fills the last case. No new endpoint, no new request field, strict decoding stays. The screen never starts a database read (ruling in plan.md); this reads the AUDIT source only, the same source the API lane already reads.

## Changes
1. Find how `readAuditLane` (`tracing_audit.go:224`) gets its per-source reader and how it merges several audit sources. The same sources are asked here: type-assert each to `audit.SQLBusyReader`; a source that does not implement it is skipped.
2. `tracing_apiseen.go`: `func (s *Server) apiSeenBuckets(ctx context.Context, sources []<source handle>, axis <axis type>) (map[int]*tracingBucket, bool, error)`: for each source call `SQLBusy` with the axis start, end and `bucketSeconds`; add `BusyMs` and `Count` per bucket across sources; returns `recorded=false` when no source had `Recorded` true. One source failing → return the error (the lane keeps its previous state and gets the existing "could not read" reason; do not invent new copy here, the screen's sentence comes from `dbState`).
3. `caseTracing`: only when the SQL lane has no buckets with data after P-20 and P-21 ran, and `recorded` is true and at least one bucket came back: set the lane's buckets, `ReasonCode = "api-seen"`, `BlockSeconds = 0`, `Complete` as the audit lane's own completeness. Otherwise leave the lane untouched.
4. Cost: at most one extra aggregation per audit source per tracing call, and only when the lane would be empty. Honour the request context (cancel on client abort) and the same per-lane time budget the audit lane uses.
5. Tests with a fake `SQLBusyReader`: empty SQL lane + recorded → `api-seen` buckets with `busyMs`; held day present → reader NOT called; `qs-file` present → reader NOT called; `Recorded false` → lane unchanged; two sources → sums added; reader error → lane unchanged and the error reason set; demo server (`--demo`) returns `api-seen` only when the demo SQL day (P-22) does not cover the window.
6. Web: `dbState` (P-07) already maps `api-seen` to state `api`. No web change.

## Proof
- [ ] `export PATH="$PWD/.dev/toolchains/go/bin:$PATH" && go build ./... && go vet ./internal/server/` → no output.
- [ ] `go test ./internal/server/ -run 'ApiSeen' -count=1` → `ok`.
- [ ] `go test ./internal/server/ -count=1` (background, about 2.5 min) → `ok`.
- [ ] Report lists the three "reader NOT called" tests by name.
- [ ] Demo path verified by test. Live path needs P-38 wired; P-46 records what was seen on live data.

## Depends on
P-21 (server order), P-37, P-38.
