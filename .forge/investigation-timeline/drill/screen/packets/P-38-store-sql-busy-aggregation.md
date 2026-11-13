# P-38 "Database time the API saw": MongoDB aggregation (live path)

model: sonnet, effort: high. Reason: a new aggregation on the customer's audit database; must be bounded, read-only and valid on MongoDB 3.4.
risk: high. Review batch: RB-3, then RB-6. Wave: W4. Requirements: R-05, R-07 (BL-013 fallback).

BLOCKED UNTIL: P-34 wired `_perfSqlMs`. If it did not, write only the "not recorded" answer (step 2) and its test, and say so.

## Outcome
The live Mongo source implements `audit.SQLBusyReader`: one bounded, read-only aggregation that sums the stored database time of the calls per time step.

## Write scope
- `internal/store/sqlbusy.go` (new)
- `internal/store/sqlbusy_test.go` (new)

## Must not change
- Every other store file. No write, no index creation, no `$out`, no `allowDiskUse(true)` unless the neighbouring rollup query already sets it (then copy its options exactly).

## Changes
1. Read how the existing time rollup query is built and bounded (find it: `grep -n "RollupWireBin\|maxTimeMS\|SetMaxTime" internal/store/*.go`). Reuse its timestamp field, its duration-safe match on the time range, its timeout and its read preference. Report the file:line you copied from.
2. `func (s *<live store type>) SQLBusy(ctx context.Context, in audit.SQLBusyInput) (audit.SQLBusyResult, error)`: `in.Validate()` first. Look up the wired path of `_perfSqlMs` from `recordPerfFields`; if it is `""` return `SQLBusyResult{Recorded: false}` with no query.
3. Pipeline (3.4-safe):
   - `$match`: the time range on the indexed timestamp field, exactly as the rollup query does, plus `{<path>: {$type: "number"}}`.
   - `$group`: `_id` = the step start, computed as `{$subtract: [ts, {$mod: [{$subtract: [ts, epoch]}, stepMs]}]}` with `epoch = time.Unix(0,0)`; `busy: {$sum: "$<path>"}`; `calls: {$sum: 1}`.
   - `$sort: {_id: 1}`, `$limit: 121`.
   No `$toDouble`, `$convert`, `$expr`, `$dateTrunc`.
4. Decode into `SQLBusyBin`; drop a bin whose `busy` is negative or NaN; more than 120 bins → error (the input validation should make that impossible).
5. Same server-side time limit as the rollup query; a timeout returns the error unchanged (the server packet turns it into a lane message).
6. Never log the pipeline with values, the path's values, or any record text. Errors carry no document content.
7. Tests (no live DB): the pipeline shape (stages in order, the `$type` guard present, `$limit` 121, no banned operator: marshal to extended JSON and `strings.Contains`); unwired path → `Recorded false`, zero queries (use the package's fake collection if it has one; else test the pipeline builder as a pure function `sqlBusyPipeline(in, path, tsField) mongo.Pipeline`); invalid input → error before any query.

## Proof
- [ ] `export PATH="$PWD/.dev/toolchains/go/bin:$PATH" && go build ./... && go vet ./internal/store/` → no output.
- [ ] `go test ./internal/store/ -run 'SQLBusy' -count=1` → `ok`.
- [ ] `go test ./internal/store/ -count=1` → `ok`.
- [ ] `grep -n "toDouble\|\$convert\|\$expr\|dateTrunc\|\$out\|InsertOne\|UpdateOne\|DeleteOne" internal/store/sqlbusy.go` → nothing.
- [ ] Not live-verified here. P-43 reviews it; P-46 runs it on live data and records the time it took.

## Depends on
P-34, P-36 (package order), P-37.
