# P-21 Query Store export file as a database lane source

model: sonnet, effort: high. Reason: new lane source with precedence rules; reads event attributes.
risk: high. Review batch: RB-2, then RB-6. Wave: W5. Requirements: R-05, R-07 (BL-013).

## Outcome
When a Query Store export file was imported into the case and no saved live day covers the window, the database lane shows real volumes from the file: `busyMs` per bucket, `blockSeconds`, and reason code `qs-file`.

## Write scope
- `internal/server/tracing_qsfile.go` (new)
- `internal/server/tracing_qsfile_test.go` (new)
- `internal/server/tracing.go` (one call site in `caseTracing`, next to the held-SQL lines 1110-1131)

## Must not change
- The importer (`internal/importer/sqlserver/querystore.go`). `casestore`. The held-SQL and layered paths: a saved live day still wins. No new endpoint, no new request field.

## Changes
1. Facts to build on (verify each, report file:line): the importer is named `sql-querystore`; each event has kind `query-interval`, `Start`/`End` = the interval, `DurationMs` = average duration in ms, and attributes `executions` and `max_duration_ms`. Today tracing only counts these events, and that count is the number of rows, not executions.
2. Find how tracing reads imported events with attributes for a window (`readLaneEvents`, `tracing.go:825`, and the casestore call under it). If attributes are not available through that call, use the casestore reader that the `/events` handler uses (`caseEvents`, `internal/server/cases.go:1878`). Read-only. Cap the rows read at the cap that reader already has; if the cap is hit, set the lane's `Truncated` true and reason text from the existing `rowcap` wording.
3. `tracing_qsfile.go`: `func qsFileBuckets(events []<event type>, axis <axis type>) (buckets map[int]*tracingBucket, blockSeconds int)`: for each `query-interval` event, `busy = executions * DurationMs`; add it to the bucket that contains `Start`; `Count` += executions; `MaxMs` = max of `max_duration_ms`; `AvgMs` = busy / executions per bucket. `blockSeconds` = the most common `End - Start` in seconds. An event without a readable `executions` attribute is skipped and counted; if any were skipped, the lane is marked not complete.
4. `caseTracing`: after the held-SQL decision, if the SQL lane is NOT held or layered and at least one `query-interval` event exists in the axis, replace the lane's buckets with these, set `ReasonCode = "qs-file"`, `BlockSeconds`, `Origin`/`AsOf` to the import time of that file (the field the lane already uses for imports: `importedTo` or the import record's time), and keep `Events`.
5. Tests: two intervals of 15 min with executions 100 and avg 20 ms → `busyMs` 2000 each, `blockSeconds` 900, reason `qs-file`; a held live day present → lane unchanged (`held-sql`); an event with a broken `executions` attribute → skipped, lane `complete` false; no `query-interval` events → lane unchanged.

## Proof
- [ ] `export PATH="$PWD/.dev/toolchains/go/bin:$PATH" && go build ./... && go vet ./internal/server/` → no output.
- [ ] `go test ./internal/server/ -run 'QSFile' -count=1` → `ok`.
- [ ] `go test ./internal/server/ -count=1` (background) → `ok`.
- [ ] Report lists the file:line for each fact in step 1.
- [ ] Demo only unless a real export file is imported; say which in the report. P-46 records it.

## Depends on
P-20. Server package order: after P-20, before P-39.
