# WP-18a Store bucket aggregate (CountBuckets + sqlite GROUP BY)
risk: medium    size: M    wave: 1    model: sonnet

## Outcome
The case store can answer "how many saved events start in each bucket of a half-open window, per importer" with one `GROUP BY` over `case.db`, independent of the 50 000 raw-row cap. A 60 000-event case returns exact totals. This is the data source for the SQL and IIS bucket counts of WP-18b. Covers TR-02, TR-07 (uncapped local aggregates, Appendix P5). New packet: split of spec WP-18 (the aggregate half), so it can run in wave 1.

## Write scope
- `internal/casestore/buckets.go` (new)
- `internal/casestore/buckets_test.go` (new)
- `internal/casestore/sqlite/case.go` (one new method `BucketCounts`; no other edit)
- `internal/casestore/sqlite/buckets_test.go` (new)

## Must not change
- The `casestore.Case` interface (line 356 of `store.go`), `EventQuery`, `Events`, `Each`, `Series`: a new optional interface is used instead, like `Tagger`, so the memory store and every fake keep compiling.
- The `events` schema and indexes (`schema.go`): the query uses the existing `ev_ts` index on `ts_ms`. No new index, no migration, no schema version change.
- INV read-only: `BucketCounts` runs one `SELECT` through `QueryContext`; no write, no `ensureAttrIndexes`.
- The existing event window meaning (`Events` is inclusive on the end): the new query is half-open, and must say so in its doc comment.

## Changes
1. `internal/casestore/buckets.go` (new). Types, exact names:
   - `type BucketQuery struct { AxisStart, From, To time.Time; WidthMs int64; Sources []string; Kinds []string }`. `From` and `To` are the half-open window `[From, To)`; `AxisStart` is the grid anchor (`From >= AxisStart`); cell index of an event is `(startMs - AxisStart.UnixMilli()) / WidthMs`, integer division. `Sources` are importer names (required, 1 to 200). `Kinds` is an optional filter (0 to 200).
   - `type BucketCount struct { Index int; Source string; ImportID int64; Count int64 }`. `ImportID` is the `imports.id` the events came from; it is 0 when the store cannot tell (the scan fallback).
   - `type BucketResult struct { Counts []BucketCount; Total int64; Scanned int64; Partial bool }`. `Counts` is sorted by `Index`, then `Source`, then `ImportID`; empty cells are absent; `Total` is the sum of `Count`; `Partial` is true only when the scan fallback hit `ErrScanBudget`.
   - `var ErrBadBucketQuery = errors.New("invalid bucket query")` and `func (q BucketQuery) Check() error`: wraps it (`fmt.Errorf("%w: ...")`, message names the field, never a value) when `From` or `To` or `AxisStart` is zero, `To` is not after `From`, `From` is before `AxisStart`, `WidthMs <= 0`, `(To-AxisStart)/WidthMs` is above `MaxSeriesBuckets` (2000, in `series.go`), `Sources` is empty or longer than 200, or `Kinds` is longer than 200.
   - `type BucketCounter interface { BucketCounts(ctx context.Context, q BucketQuery) (BucketResult, error) }`.
   - `func CountBuckets(ctx context.Context, c Case, q BucketQuery) (BucketResult, error)`: `q.Check()`, then `c.(BucketCounter)` when implemented, else `CountBucketsScan`.
   - `func CountBucketsScan(ctx context.Context, c Case, q BucketQuery) (BucketResult, error)`: one `c.Each` pass with `EventQuery{Window: importer.Window{From: q.From, To: q.To.Add(-time.Millisecond)}, Sources: q.Sources, Kinds: q.Kinds}`, skipping any event whose `Start.UnixMilli()` is below `q.From.UnixMilli()` or at/after `q.To.UnixMilli()` (a `Case` is not required to honour the window; the test double `genCase` ignores it), counting per `(index, Source)`; `ErrScanBudget` -> `Partial: true` (as `Series` does at `series.go` lines 447-460); any other error is returned. `Scanned` is the number of events delivered.
2. `internal/casestore/sqlite/case.go`: `func (c *Case) BucketCounts(ctx context.Context, q casestore.BucketQuery) (casestore.BucketResult, error)`. Call `q.Check()` first and map its error to `fmt.Errorf("%w: bucket query", ErrInvalid)` (as `eventFilterAs` does). Then `c.handle()`, and one query: `SELECT (ts_ms - ?) / ? AS b, source, import_id, COUNT(*) FROM events WHERE ts_ms >= ? AND ts_ms < ? AND source IN (...) [AND kind IN (...)] GROUP BY b, source, import_id ORDER BY b, source, import_id`. Arguments in order: `AxisStart.UnixMilli()`, `WidthMs`, `From.UnixMilli()`, `To.UnixMilli()`, then the sources, then the kinds; build the `IN` lists the way `eventFilterAs` does (`sqlite/case.go:1119-1133`: `strings.TrimSuffix(strings.Repeat("?,", n), ",")`, and the same `maxFilter` guard at line 1126). No `LIMIT`: the result has at most 2000 cells times the number of imports. Fill `Total`; `Scanned` = `Total`; `Partial` = false. A nil `Sources` entry is not possible after `Check`.
3. Tests, `internal/casestore/buckets_test.go` (package `casestore_test`). Fixture: the existing `genCase` type (`internal/casestore/series_test.go:24`, same package; only `Each` is implemented, any other method panics on its nil embedded `Case`, and it does NOT apply the query window, which is what makes it a good double for the skip rule), built as `&genCase{n: k, make: func(i int) evidence.Event {...}}`. `TestBucketQueryCheck` (table: zero times, `To` equal to `From`, `From` before `AxisStart`, width 0, 2001 cells, 0 and 201 sources -> `errors.Is(err, casestore.ErrBadBucketQuery)`; a valid query -> nil); `TestCountBucketsScanHalfOpen` (events at `From-1ms`, `From`, `From+W-1ms`, `From+W`, and exactly `To`; the events at `From-1ms` and `To` are not counted; the next two land in cell 0, the fourth in cell 1; `Total` 3); `TestCountBucketsFallsBackToScan` (`genCase` does not implement `BucketCounter`, so `CountBuckets` equals `CountBucketsScan` and `g.calls == 1`; a second sub-test with a real `casestore.NewMemoryWith(caps, now)` store, as `memory_test.go` builds it, gives the same counts).
4. Tests, `internal/casestore/sqlite/buckets_test.go` (use `newTestStore`, `mustCreate`, `mustOpen`, `mustImport` from `helpers_test.go:42-129`; `bigCase(t, s, n)` at `series_test.go:19` and `TestSeriesReadsPastTheEventsCap` at `:29` show how more than 50 000 events are seeded and how the long test is skipped under `testing.Short()`). `mustImport` always imports under importer `testImporter`, so this file adds one helper, `mustImportAs(t *testing.T, c *Case, importerName, label string, n int) casestore.ImportResult`: it copies `tEvents` with `Source` and the `evidence.EventID` importer argument set to `importerName`, and calls `c.Import` with `tMeta(label+".csv")` whose `Importer` is `importerName`. Tests: `TestBucketCountsExactPastTheCap` (skipped under `testing.Short()`; 60 001 events across 6 cells imported with `mustImportAs` under two importer names; `Total == 60001`, per-cell sums exact, `Partial == false`, `ImportID` equals the `Record.ID` of the matching `ImportResult`); `TestBucketCountsMatchesScan` (a small mixed case: `BucketCounts` and `CountBucketsScan` give the same `(Index, Source, Count)` triples when `ImportID` is ignored); `TestBucketCountsFilters` (a `Kinds` filter and a `Sources` filter drop the other rows; an unknown source gives empty `Counts`); `TestBucketCountsReadOnly` (the case revision / `meta` is unchanged after the call); `TestBucketCountsCancel` (a canceled context returns `context.Canceled`).

## Proof
- `go build ./...` -> no output
- `go vet ./internal/casestore/...` -> no output
- `go test ./internal/casestore/ -run "TestBucketQueryCheck|TestCountBucketsScanHalfOpen|TestCountBucketsFallsBackToScan" -count=1` -> `ok`
- `go test ./internal/casestore/sqlite/ -run "TestBucketCounts" -count=1` -> `ok`
- `go test ./internal/casestore/... -count=1` -> `ok` for every package (no existing test changed)

## Depends on
none
