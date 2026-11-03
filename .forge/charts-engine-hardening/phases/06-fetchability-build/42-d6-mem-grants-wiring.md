# TSK-42 d6-mem-grants-wiring

risk: medium — a second server reader on every investigation; it must cost nothing when no SQL counters are imported.

## Outcome

When the case holds the PDH metric `sql.mem.grants_pending` (TSK-38) for the incident window, D6 gets a lane answer: Yes when the incident peak is at least 10 pending grants, No when the peak stays under 10 and the series covers at least 80% of the incident window. Otherwise D6 stays unset (Grey, as today).

## Write scope

- `internal/server/investigation_metrics.go` (new; TSK-56 appends to it)
- `internal/server/investigation_metrics_test.go` (new)
- `internal/server/investigation.go` (one call line, right after the TSK-35 call)

## Must not change

- `internal/resource/*` (read-only use of `CatalogueContext`, `Index.Series`, `Index.Coverage`).
- `catalog.go`, `thresholds.go` (marks are local).
- Do not commit. Do not push. Never touch `sql/`.

## Changes

1. Create `investigation_metrics.go`:
   ```go
   // Marks for metric-fed lane answers (FETCHABILITY-AUDIT 6c), local to the
   // server reader. Not in Thresholds, so they do not move Version.
   const (
       memGrantsPendingMin = 10.0
       metricLaneCoverage  = 0.8
   )

   // readMetricLanes builds the resource catalogue for the incident window once
   // and answers the lanes that read a metric series. A catalogue error is a
   // warning, never a failed run.
   func readMetricLanes(ctx context.Context, c casestore.Case, in *investigate.Input, read *investigationRead)
   ```
2. Body:
   - `if !in.Incident.Valid() { return }`.
   - `x, err := resource.CatalogueContext(ctx, c, importer.Window{From: in.Incident.Start, To: in.Incident.End})`; on error append `"The resource counters could not be read for the incident window."` to `read.Warnings` and return. If `x.Partial()`, append `"The resource counters were too many to read in full, so the SQL memory-grant reading was not used."` and return.
   - `ss := x.Series(resource.SQLMemGrantsPending, resource.Filter{})`; if empty return (no write, no source name).
   - Over all series: `peak` = max `Sample.Value`; `cov` = max `x.Coverage(s)`.
   - `v := investigate.Unknown`; `peak >= memGrantsPendingMin` → `Yes`; else `cov >= metricLaneCoverage` → `No`.
   - If `v != Unknown`: `in.Lanes` nil → make; if `in.Lanes["D6"] != investigate.Yes` set it; add `"SQL Server counters"` to `in.LaneSources` once.
3. `investigation.go`: right after the `readImportedEvents` block from TSK-35, add `readMetricLanes(ctx, c, &in, read)`.
4. `investigation_metrics_test.go`: build a case with PDH counter events the way `internal/resource/resource_test.go` builds them (copy its `pdh(...)` helper shape into a local helper; read how its harness adds events to a case). Tests:
   - no counter events → `in.Lanes` has no `D6`, no warning;
   - one sample per minute for a 30-minute incident, values 0..3 → `D6 == No`;
   - same with one value 12 → `D6 == Yes`;
   - only 10 of 30 minutes covered, values 0 → `D6` absent;
   - `in.Lanes["D6"] = Yes` beforehand, values 0 full coverage → stays `Yes`.
5. gofmt the files.

## Proof

```
.dev\toolchains\go\bin\go.exe test ./internal/server/... -run "TestReadMetricLanes"
.dev\toolchains\go\bin\go.exe test ./internal/server/... ./internal/investigate/...
.dev\toolchains\go\bin\go.exe build ./...
```

PASS.

## Depends on

35 (call site order in `investigation.go`), 38 (`SQLMemGrantsPending`).
