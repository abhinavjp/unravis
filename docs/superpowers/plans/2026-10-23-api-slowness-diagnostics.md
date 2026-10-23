# API Slowness Diagnostics Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn API Audit Explorer from "which API got slower" into "why is it slow": phase breakdown, impact ranking, significance, load and payload correlation, per-dimension breakdowns, and (for SQL) database-side hot-spots, behind a source abstraction that supports Mongo and SQL.

**Architecture:** Keep the existing histogram-bucket pipeline (it is the tool's strength) and widen what each bucket carries (phase sums, error counts), then add derived analyses in `internal/audit` (pure functions, no I/O). Add a `telemetry.Provider` seam for database-side evidence (Query Store/DMVs, Mongo profiler/`$indexStats`). Highest-value data (DB time per request) is not in the log today: recommend app-side instrumentation as a separate plan in the `brain-payroll` repo.

**Tech Stack:** Go (module `api-audit-explorer`), MongoDB Go driver + legacy 3.4 driver, `github.com/microsoft/go-mssqldb` (new), Svelte + Vite + TypeScript in `web/`.

**Spec:** No separate spec. Basis is the review + research in this session. Facts it rests on:
- `brain-payroll/BrainPayroll.Service/Models/ApiAuditLog/ApiAuditLogServiceModel.cs` (fields)
- `.../Helpers/ApiAuditHelper/ApiAuditLogQueueReaderHelper.cs` (`SetTime`)
- `.../BrainPayroll.Shared.Api/Helpers/RequestAndResponseLog/RequestAndResponseLogHandler.cs` (measurement window)

## Global Constraints

- Read-only against every database. Never enable profilers, Query Store, or write anywhere. Missing privileges degrade to a warning, not an error.
- Loopback only, session token, DPAPI-saved connections: unchanged.
- Existing limits stay: 8 sources, 4 global / 2 per-source concurrent operations, 40,000 summary rows, 10,000 cohorts, 120 time buckets, 7-day max interval, 16 MiB analysis size, 5-minute timeout.
- Histogram: γ=1.1, bins 0..193 (193 = overflow). Go (`internal/audit/histogram.go`) and TypeScript (`web/src/lib/charts.ts` `histogramBinBounds`) must agree.
- Timing semantics (verified in writer code): `AuditCreateTime = CreatedOn − RequestInitiatedAt`; `ApiTime = RequestCompletedAt − CreatedOn`; total = AuditCreateTime + ApiTime (additive). `PayloadRecordTime` happens after the response and is **not** request latency.
- Cohort key stays `CohortKey(sourceID, controller, action, kind, outcome)`. New dimensions are breakdowns of one cohort, not new key parts (no breaking change to MCP/UI consumers).
- Comparison states unchanged: `compared`, `insufficient_data` (<20), `undefined_baseline`, `missing_current`, `missing_reference`.
- New JSON fields are additive and optional (`omitempty`/nullable); `web/src/lib/types.ts` mirrors every Go change in the same task.

## Review Focus

1. Documents with missing or unknown `StatusCode`. Must be counted as `unknownOutcome`, never success or failure. Today `stringsToBSON` prepends nil entries, so `$in` may match missing values (Task 1).
2. Documents lacking `AuditCreateTime`/`PayloadRecordTime` (old rows, async rows before update). Phase means must use per-phase counts, not the cohort count (Task 4).
3. Cohort with one side empty or <20 samples. No significance, correlation, or impact value may be fabricated; return null and state (Tasks 5, 7).
4. Breakdown dimension with high cardinality (UserId). Top-N plus "other", bounded result size, no PII dimension (UserIp) exposed (Task 9).
5. Server without Query Store, Mongo without profiling, or missing privileges. Each telemetry section reports `OK=false` with a reason; analysis still succeeds (Tasks 10-14).

---

## Phase 0: Ground truth

### Task 0: Toolchain and baseline

**Files:**
- Modify: `build.ps1` (Go lookup: PATH, then `$env:GOROOT`, then `<repo>\.dev\toolchains\go\bin`; drop the `..\..` workspace-root assumption)
- Create: `.gitignore` (`dist/`, `web/node_modules/`, `web/dist/`) (only if the user approves `git init`)

- [ ] **Step 1:** Install Go matching `go.mod`'s `go` line; run `go test ./...` and `npm --prefix web test` (if a script exists). Record pass/fail counts in the PR description. Integration tests stay skipped without `AUDIT_EXPLORER_TEST_MONGO_URI` / `AUDIT_MONGO34_TEST_URI`.
- [ ] **Step 2:** Fix `build.ps1`; run `./build.ps1`. Expected: `dist/api-audit-explorer.exe` rebuilt.
- [ ] **Step 3:** `git init`, first commit "baseline" (skip if the user declined).

### Task 1: Fix `stringsToBSON` and verify outcome classification

**Files:**
- Modify: `internal/store/pipeline.go:137` (`make(bson.A, 0, len(values))`)
- Test: `internal/store/pipeline_test.go`

- [ ] **Step 1:** Test `TestStringsToBSONHasNoNilEntries`: `got := stringsToBSON("OK","CREATED")`; assert `len(got)==2`, `got[0]=="OK"`.
- [ ] **Step 2:** Test `TestSummaryPipelineMissingStatusIsUnknown`: render `buildSummaryPipeline` to JSON and assert no `null` literal appears inside the success/failure `$in` arrays.
- [ ] **Step 3:** Run, see both fail; fix; run, see pass.
- [ ] **Step 4:** Commit.

### Task 2: Schema census and analysis timings

**Files:**
- Create: `internal/store/schema.go`, `internal/store/schema_test.go`
- Modify: `internal/audit/models.go` (add `Timing`), `internal/store/analyze.go` (fill it), `internal/server/server.go` (route `GET /api/sources/{id}/schema`)

**Interfaces:**
- Produces: `func (s *Store) SampleSchema(ctx context.Context, sourceID string, n int) (audit.SchemaCensus, error)`
- Produces: `type SchemaCensus struct { Sampled int; Fields []FieldPresence }`, `type FieldPresence struct { Name string; Present int; Types []string }`
- Produces: `type SourceTiming struct { SourceID string; Window string /* current|reference */; ReadMs int64; Rows int64 }` and `Analysis.Timings []SourceTiming` (additive)

- [ ] **Step 1:** Test `TestSchemaCensusCountsFieldPresence` using a fake cursor of 3 docs (one missing `UserId`): assert `Present` counts and `Types` per field. Cap `n` at 1,000.
- [ ] **Step 2:** Implement with `$sample` (fall back to `$limit` on 3.4). Never return values, only names/types/counts (no PII).
- [ ] **Step 3:** Test `TestAnalyzeReportsPerWindowTimings`: two sources, assert 4 `SourceTiming` rows with `ReadMs >= 0`.
- [ ] **Step 4:** Run the schema census and the timings against the real `BG-SRV-UK-01` sources (read-only); paste results into `docs/findings/schema-census.md`. This resolves what extra fields exist and why the 5-minute hang happens (which source/window dominates). **Gate: Tasks 4 and 9 adjust to the real census.**
- [ ] **Step 5:** Commit.

---

## Phase 1: Accuracy and core measures (audit log only, Mongo)

### Task 3: P90/P99 and error-bound reporting

**Files:**
- Modify: `internal/audit/models.go` (`Stats.P90`, `Stats.P99 *Quantile`; `Comparison.P99Ratio`, `P99DeltaMs`), `internal/audit/histogram.go` (`stats()` computes both), `internal/audit/analyze.go`, `web/src/lib/types.ts`, results table component in `web/src/components`
- Test: `internal/audit/histogram_test.go`

- [ ] **Step 1:** Test `TestQuantileP99WithinBinBounds`: add 1,000 samples uniform 1..1000 ms through `BinIndex`; assert `P99.LowerMs <= 990 <= *P99.UpperMs` and `valueMs` within 10% of 990.
- [ ] **Step 2:** Test `TestP99NilWhenCountBelow100` (P99 undefined under 100 samples; return nil rather than the max).
- [ ] **Step 3:** Implement; show P99 and the bin-width caveat ("±~5%") as a tooltip in the table.
- [ ] **Step 4:** Commit.

### Task 4: Phase decomposition (where the time goes)

**Files:**
- Modify: `internal/store/pipeline.go` (extra `$group` sums with per-phase presence counts), `internal/audit/models.go`, `internal/audit/analyze.go`, `internal/audit/histogram.go`, `web/src/lib/types.ts`, new `web/src/components/PhaseBar.svelte`
- Test: `internal/audit/phases_test.go`, `internal/store/pipeline_test.go`

**Interfaces:**
- Produces: `Bucket` gains `SumAuditMs, SumApiMs, SumPayloadMs float64; NAudit, NApi, NPayload int64`
- Produces: `type PhaseMeans struct { AuditMs, ApiMs, PayloadMs *float64 }`; `Stats.Phases *PhaseMeans`
- Produces: `Comparison.PhaseDeltaMs *PhaseMeans` (current − reference); nil per-phase when either side's n is 0

- [ ] **Step 1:** Test `TestPhaseMeansUsePerPhaseCounts`: bucket with Count=10, NApi=10, NAudit=4, SumAuditMs=40 gives `AuditMs == 10`, not 4. (Review Focus 2.)
- [ ] **Step 2:** Test `TestPhaseDeltaExplainsTotalRegression`: reference mean audit 20/api 80, current audit 20/api 280: `PhaseDeltaMs.ApiMs == 200`, `AuditMs == 0`.
- [ ] **Step 3:** Pipeline test: rendered JSON `$group` contains the three `$sum` and three presence-count expressions, and the unit stays milliseconds (the writer stores `TimeSpan`; confirm representation from the Task 2 census before choosing `$toDouble` vs numeric field path).
- [ ] **Step 4:** Implement; UI: stacked bar per API row (audit / api, with payload shown separately and labelled "after response, not latency"). Fix the README "may overlap" sentence in the same commit.
- [ ] **Step 5:** Commit.

### Task 5: Impact ranking and significance

**Files:**
- Create: `internal/audit/significance.go`, `internal/audit/significance_test.go`
- Modify: `internal/audit/models.go`, `internal/audit/analyze.go`, `web/src/lib/types.ts`, results table (default sort by impact)

**Interfaces:**
- Produces: `func HistogramShiftZ(current, reference Stats) *float64` (Mann-Whitney U z-score computed from binned counts with tie correction; nil when either n < 20)
- Produces: `Comparison.ImpactMs *float64` = (current mean − reference mean) × current count; `Comparison.TotalTimeMs float64`; `Comparison.ShiftZ *float64`; `Comparison.Significant bool` (true when `|z| >= 3` and the P50 shift exceeds one bin width)

- [ ] **Step 1:** Tests `TestShiftZIdenticalHistogramsIsZero`, `TestShiftZClearShiftExceedsThree` (reference around 50 ms vs current around 250 ms, 125 samples each, mirroring the demo), `TestShiftZNilBelowMinSamples`.
- [ ] **Step 2:** Test `TestImpactRanksVolumeTimesDelta`: API A +200 ms × 100 requests ranks above API B +500 ms × 2 requests.
- [ ] **Step 3:** Implement; table default sort becomes `ImpactMs` descending, ratio sort remains selectable.
- [ ] **Step 4:** Commit.

### Task 6: Throughput, error rate, and load correlation

**Files:**
- Create: `internal/audit/load_correlation.go`, `internal/audit/load_correlation_test.go`
- Modify: `internal/audit/models.go`, `internal/audit/analyze.go`, `internal/audit/load.go`, `web/src/lib/types.ts`, `web/src/lib/traffic.ts`, `web/src/lib/volume.ts`

**Interfaces:**
- Produces: `type Rollup struct { Controller, Action string; Requests, Failures int64; ErrorRate *float64; PerMinute float64 }` and `Analysis.Rollups []Rollup`, computed from the existing outcome-split cohorts (current window)
- Produces: `func EstimateConcurrency(buckets []Bucket, bucketSeconds int) []float64` (Little's law: Σ duration ÷ bucket seconds)
- Produces: `func SpearmanLoad(concurrency []float64, p95 []*float64) *float64` and `Series.LoadCorrelation *float64` (nil when fewer than 8 paired points)

- [ ] **Step 1:** Tests `TestEstimateConcurrencyLittlesLaw` (60 requests × 1,000 ms in a 60 s bucket gives 1.0), `TestSpearmanPerfectMonotonic` (1.0), `TestSpearmanNilUnderEightPoints`.
- [ ] **Step 2:** Test `TestRollupErrorRateIgnoresUnknownOutcome` (unknown excluded from both numerator and denominator; reported in `Quality`).
- [ ] **Step 3:** Implement; UI shows an error-rate column, and a "slows under load (ρ=0.8)" badge only when |ρ| ≥ 0.6 and n ≥ 8.
- [ ] **Step 4:** Commit.

---

## Phase 2: New dimensions (Mongo, uses fields the tool ignores today)

### Task 7: Breakdown query

**Files:**
- Create: `internal/store/breakdown.go`, `internal/store/breakdown_test.go`, `internal/server/breakdown.go`
- Modify: `internal/audit/models.go`, `internal/audit/repository.go` (optional interface), `internal/audit/demo.go`, `internal/server/server.go`
- Test: `internal/server/breakdown_test.go`

**Interfaces:**
- Produces: `type BreakdownDim string` with `DimUser`, `DimUIVersion`, `DimResponseSize`, `DimRequestSize`, `DimHourOfDay`, `DimStatus`. **`UserIp` is deliberately not offered.**
- Produces: `type BreakdownInput struct { Query Query; Key string /* cohort key */; By BreakdownDim; Top int /* default 20, max 50 */ }`
- Produces: `type BreakdownRow struct { Value string; Stats Stats }` and `type Breakdown struct { By BreakdownDim; Rows []BreakdownRow; Other *Stats; Approximate bool }`
- Produces: optional `audit.Breakdowner` interface `Breakdown(ctx, BreakdownInput) (Breakdown, error)`; handler `POST /api/breakdown`
- Size bands: response/request KB → `<10`, `10-100`, `100-1000`, `>=1000`.

- [ ] **Step 1:** Test `TestBreakdownCapsTopAndFoldsOther`: 60 distinct users, `Top: 20` gives 20 rows + `Other` with the remaining counts summing to the total.
- [ ] **Step 2:** Test `TestBreakdownRejectsUnknownDimension` and `TestBreakdownTopAboveMaxRejected` (max 50).
- [ ] **Step 3:** Test `TestSizeBandBoundaries` (9.99 → `<10`, 10 → `10-100`, 1000 → `>=1000`).
- [ ] **Step 4:** Pipeline test: filter pushes `{controller, action}` of the decoded cohort key and the time range before `$group`; result row count bounded by `$limit`. Must respect the global/per-source semaphores and the 5-minute timeout.
- [ ] **Step 5:** Implement Mongo version using the existing `_audit*` stage helpers; demo repository returns deterministic rows (payload size correlated with duration for Payroll.Run).
- [ ] **Step 6:** Commit.

### Task 8: Breakdown and tail UI + MCP tools

**Files:**
- Create: `web/src/components/BreakdownPanel.svelte`, `internal/mcp/breakdown.go`
- Modify: `web/src/lib/api.ts`, `web/src/lib/types.ts`, `web/src/App.svelte`, `internal/mcp/tools.go`, `internal/mcp/schema.go`, `MCP.md`, `README.md`
- Test: `internal/mcp/bridge_test.go`

- [ ] **Step 1:** Add tools `get_phase_breakdown` (Task 4 data) and `get_breakdown` (Task 7); update the tool-count test (8 → 10) and the schema assertions.
- [ ] **Step 2:** UI: selecting an API row opens a panel with a dimension dropdown and a small table with P50/P95/count per value, plus the slowest-N request list showing the phase split per request (`RequestRecord` already carries it).
- [ ] **Step 3:** Contract test `web/src/lib/charts.test.ts` + `internal/audit/histogram_test.go` both load `internal/audit/testdata/bin_bounds.json` (bins 0, 1, 96, 192, 193) so Go and TS cannot drift.
- [ ] **Step 4:** Browser check in demo mode (`run.ps1 -Demo`): panel opens, no console errors. Commit.

### Task 9: Performance of the summary pipeline

**Files:**
- Modify: `internal/store/pipeline.go` (replace the 192-branch `$switch` at ~92-96), `internal/store/analyze.go` (read current and reference concurrently under the existing semaphores)
- Test: `internal/store/pipeline_test.go`, `internal/store/analyze_test.go`, `internal/store/mongo_integration_test.go`

- [ ] **Step 1:** Test `TestBinExpressionMatchesBinIndex`: evaluate in Go the closed form `floor(ln(ms)/ln(1.1)) + 1` (bins clamped 0..193) for 200 values including exact bin edges, against `audit.BinIndex`. Use the closed form only if every value matches; otherwise keep the `$switch` (floating-point edges are the risk). Mongo 3.4 supports `$ln`; confirm with the legacy integration test.
- [ ] **Step 2:** Test `TestAnalyzeReadsWindowsConcurrently` (blocking fake: both windows in flight before either completes; still bounded by 2 per source).
- [ ] **Step 3:** Using the Task 2 timings, confirm the dominant cost before optimising further (index on the time field is the usual one: report as `indexWarning` text, do not create it).
- [ ] **Step 4:** Commit.

---

## Phase 3: Database diagnostics (Mongo first, SQL Server last)

Scope decision (from the user): audit logs stay Mongo-only. SQL Server is the **application database to diagnose**, not an audit-log source. No `Backend` seam and no SQL audit-log reader (YAGNI). Evidence from the database is matched to an API regression by **time window only** until Task 16 adds per-request DB time; the UI says so.

### Task 10: Telemetry seam and Mongo provider

**Files:**
- Create: `internal/telemetry/provider.go`, `internal/telemetry/mongo.go`, `internal/telemetry/mongo_test.go`, `internal/server/telemetry.go`
- Modify: `internal/server/server.go` (`POST /api/diagnostics`), `internal/audit/repository.go` (optional `Diagnoser`)

**Interfaces:**
- Produces:
```go
type Window struct{ Start, End, BaselineStart, BaselineEnd time.Time }
type Provider interface {
    Kind() string // "mongo" | "mssql"
    Diagnose(ctx context.Context, w Window, opts Options) (Report, error)
    Close() error
}
type Options struct{ Sections []string; SampleSeconds int /* 0-30 */; IncludeSQLText bool }
type SectionStatus struct{ Name string; OK bool; Reason string }
type Report struct {
    Kind string; Status []SectionStatus // every section degrades independently
    Queries []QueryStat; Regressions []QueryRegression; Waits []WaitStat
    Blocking []BlockingChain; Deadlocks []Deadlock; IO []FileIO
    MissingIndexes []MissingIndex; UnusedIndexes []UnusedIndex; Findings []Finding
}
type Finding struct{ Severity string /* info|warn|critical */; Title, Evidence, Advice string } // Advice is text only, never executed
```
- Mongo sections: `profile` (reads `system.profile` only when profiling was already enabled; slowest `millis`, `COLLSCAN` plans, `docsExamined/nreturned`), `indexes` (`$indexStats`: unused indexes since `accesses.since`), `collections` (`$collStats` size and index sizes), `currentop` (long-running ops via `$currentOp`), `explain` (optional, `executionStats` on a given cohort query shape).

- [ ] **Step 1:** Tests `TestMongoProfilerDisabledReportsUnavailableNotError`, `TestMongoNeverCallsProfileSetter`, `TestMongoIndexStatsFlagsZeroAccessIndexes`, `TestSectionFailureDoesNotFailReport` (one failing section yields `OK=false` + reason, others still populated).
- [ ] **Step 2:** Implement; registered per source like `SlowestReader`. Sections run under the existing semaphores and a 30 s per-section timeout.
- [ ] **Step 3:** Commit.

### Task 11: SQL Server connection, capability probe, read-only guard

**Files:**
- Create: `internal/telemetry/mssql.go` (provider shell + probe), `internal/telemetry/mssql_guard.go`, `internal/telemetry/mssql_probe_test.go`, `internal/telemetry/mssql_guard_test.go`, `docs/sql-diagnostics-permissions.md`
- Modify: `go.mod` (`github.com/microsoft/go-mssqldb`), saved-connection store (existing DPAPI recent-connections code; add `Kind` defaulting to `"mongo"` so existing entries load), connection form in `web/src/components`, `web/src/lib/types.ts`

**Interfaces:**
- Produces: `func NewMSSQL(ctx context.Context, connectionString string) (*MSSQL, error)`; `type Capabilities struct { MajorVersion int; Edition string; QueryStoreByDB map[string]string /* actual_state_desc */; ViewServerState, ViewDatabaseState bool }`
- Produces: `func (m *MSSQL) readOnlyQuery(ctx context.Context, sqlText string, args ...any) (*sql.Rows, error)`: **the only path to the database**; rejects any statement not starting with `SELECT` or `WITH` (after trimming comments), plus `;`-chained batches.

- [ ] **Step 1:** Guard tests: `TestGuardRejectsWrites` (`UPDATE`, `DELETE`, `EXEC`, `INSERT`, `DROP`, `SELECT 1; DROP TABLE x`, comment-prefixed `/*x*/ DELETE`) and `TestGuardAllowsSelectAndWith`.
- [ ] **Step 2:** Test `TestConnectionStringNeverLogged`: construct with a password marker, force a connect error, assert the marker is absent from the returned error text and all log output. The connection string is entered only in the UI form (never in chat) and stored DPAPI-encrypted.
- [ ] **Step 3:** Probe test (sqlmock): `TestProbeReportsQueryStoreOffAndMissingPermission` populates `Capabilities` and yields `SectionStatus` reasons ("grant VIEW SERVER STATE", "Query Store is OFF on database X").
- [ ] **Step 4:** Implement with `ApplicationIntent=ReadOnly`, `app name=api-audit-explorer`, session `SET LOCK_TIMEOUT 5000; SET DEADLOCK_PRIORITY LOW`, 30 s per-section timeout. Write `docs/sql-diagnostics-permissions.md`: a login with `VIEW SERVER STATE` (`VIEW SERVER PERFORMANCE STATE` on 2022+) and `VIEW DATABASE STATE` per database, for a DBA to run. The tool never runs it and never needs write rights.
- [ ] **Step 5:** Commit.

### Task 12: Query Store and query diagnostics

**Files:**
- Create: `internal/telemetry/mssql_queries.go`, `internal/telemetry/mssql_queries_test.go`

- [ ] **Step 1:** Tests (sqlmock rows, one per behavior):
  - `TestTopQueriesRankedByTotalDuration` (also CPU, logical reads, executions; Query Store durations are microseconds, convert to ms).
  - `TestRegressionRequiresMinExecutions` (avg duration up ≥ 2× with ≥ 20 executions in each window; fewer → not reported).
  - `TestMultiplePlansFlagged` (one query, ≥ 2 plans, per-plan averages returned, forced plan marked).
  - `TestParameterSniffingSignal` (`max_duration / avg_duration ≥ 10` with ≥ 20 executions → `Finding` "high variance, possible parameter sniffing").
  - `TestAbortedAndTimeoutExecutionsCounted` (`execution_type_desc`).
  - `TestFallsBackToDMVWithWindowedFalse` (`sys.dm_exec_query_stats` when Query Store is off; `Windowed=false` surfaced).
- [ ] **Step 2:** Implement against `sys.query_store_runtime_stats`, `sys.query_store_runtime_stats_interval` (window by `start_time`/`end_time`, UTC), `sys.query_store_query`, `sys.query_store_query_text`, `sys.query_store_plan`. SQL text truncated to 2,000 chars and returned only when `IncludeSQLText` (UI yes; MCP default no).
- [ ] **Step 3:** Commit.

### Task 13: Waits, blocking, deadlocks, I/O

**Files:**
- Create: `internal/telemetry/mssql_runtime.go`, `internal/telemetry/mssql_runtime_test.go`

- [ ] **Step 1:** Tests:
  - `TestWaitStatsExcludeBenignWaits` (drop `SLEEP_TASK`, `LAZYWRITER_SLEEP`, `XE_TIMER_EVENT`, `BROKER_*`, `SQLTRACE_*` and the usual idle list; unit-test the list).
  - `TestWaitSampleReturnsDelta` (`SampleSeconds=10`: second snapshot minus first; zero or negative delta dropped; max 30).
  - `TestBlockingChainRootFound` (A blocks B blocks C → root A, depth 3, wait ms).
  - `TestDeadlocksParsedFromSystemHealth` (fixture `xml_deadlock_report` XML → victim, resources, both statements; filtered by window).
  - `TestIOLatencyFlagsAboveThreshold` (data file avg read > 20 ms and log write > 5 ms → `Finding`; labelled cumulative since restart).
  - `TestTempdbLatchContentionFlagged` (`PAGELATCH_*` on tempdb dominating waits).
- [ ] **Step 2:** Implement from `sys.dm_os_wait_stats`, Query Store waits (`sys.query_store_wait_stats`, 2017+), `sys.dm_exec_requests` + `sys.dm_os_waiting_tasks`, the `system_health` XEvent target, `sys.dm_io_virtual_file_stats`.
- [ ] **Step 3:** Commit.

### Task 14: Indexes, statistics, configuration health

**Files:**
- Create: `internal/telemetry/mssql_health.go`, `internal/telemetry/mssql_health_test.go`

- [ ] **Step 1:** Tests:
  - `TestMissingIndexesRankedByImpactScore` (`avg_total_user_cost * avg_user_impact * (user_seeks + user_scans)`; equality/inequality/include columns returned; `Advice` is a `CREATE INDEX` suggestion as **text**).
  - `TestUnusedIndexesExcludePKAndUnique` (reads = 0, writes high, `is_primary_key = 0`, `is_unique = 0`).
  - `TestStaleStatsFlagged` (`modification_counter / rows ≥ 20%` and last updated > 7 days).
  - `TestConfigFindings` (MAXDOP unset on a multi-core box, cost threshold for parallelism = 5, compat level below engine level, `AUTO_SHRINK`/`AUTO_CLOSE` ON, Query Store `READ_ONLY` or `OFF`, max server memory unlimited, tempdb files fewer than min(cores, 8)).
  - `TestFragmentationOptInOnly` (`sys.dm_db_index_physical_stats` in `LIMITED` mode, page_count > 1,000, only when `Sections` names `fragmentation`; never default).
- [ ] **Step 2:** Implement; every `Finding` carries `Evidence` (the numbers) and `Advice` (text).
- [ ] **Step 3:** Commit.

### Task 15: Database evidence UI and MCP tool

**Files:**
- Create: `web/src/components/DbEvidence.svelte`, `internal/mcp/diagnostics.go`
- Modify: `web/src/lib/api.ts`, `web/src/lib/types.ts`, `web/src/App.svelte`, `internal/mcp/tools.go`, `internal/mcp/schema.go`, `MCP.md`, `README.md`
- Test: `internal/mcp/bridge_test.go`, `internal/server/telemetry_test.go`

- [ ] **Step 1:** Tool `get_db_diagnostics` (`sections`, `sampleSeconds`); tool-count test 10 → 11; SQL text omitted unless the server flag `--mcp-include-sql-text` is set (test both).
- [ ] **Step 2:** UI card per selected regression: top queries with delta vs reference, waits, blocking, findings sorted by severity, each section showing its `SectionStatus` reason when unavailable. Banner: "Same time window. Correlation, not attribution." until Task 16 data exists. A "Diagnose now" button runs with `SampleSeconds=10`.
- [ ] **Step 3:** Demo mode returns a deterministic synthetic `Report` (clearly labelled demo) so the UI can be checked without a database. Browser check in `run.ps1 -Demo`. Commit.

---

## Phase 4: Recommended, outside this repo (do after Phase 3)

### Task 16: App-side instrumentation (separate plan in `brain-payroll`)

Not implemented here. Write a plan against `RequestAndResponseLogHandler` and `ApiAuditLogServiceModel` because it gives direct attribution instead of time-window correlation:

- Add to the audit document: `DbTimeMs`, `DbCallCount`, `ExceptionType`, `TraceId`, and a capped `SlowDbCalls[]` (max 5: `{ system, operation, target, durationMs }`; no parameter values).
- Accumulate per request in an `AsyncLocal`: Mongo via a command-event subscriber in `MongoClientSettings.ClusterConfigurator`; SQL via an `IDbCommandInterceptor` or `DiagnosticListener`.
- Once present, this tool computes `AppTimeMs = ApiTime − DbTimeMs` as a new phase (extends Task 4's `PhaseMeans` with two optional fields; no other change).
- Risk: the audit insert is synchronous on the request path (`AuditCreateTime`); adding fields to that insert increases that cost. Add the DB fields in the **async update** (`UpdateApiAuditLogs`), not the initial insert.

---

## Self-review

- **Coverage:** finding "no breakdown of time" → T4; "ratios not impact" → T5; "P95 only" → T3; "no throughput/error rate/load" → T6; "unused dimensions" → T7-8; "slow tool / hang" → T2, T9; "database diagnostics (Mongo, then SQL Server)" → T10-15; "no DB time" → T16; "docs wrong, bug" → T1, T4.
- **Types:** `PhaseMeans` is defined in T4 and only extended in T16; `Quantile` pointers used consistently; `Breakdowner` is optional like `SlowestReader`.
- **Proportion:** tasks list signatures, test names and values, not bodies.
- **Known unknowns (decide after T2's census):** real field types of the timing values (TimeSpan serialisation), whether `UserId`/`UiVersion` are populated, which SQL Server version/edition and whether Query Store is on (Task 11 probe), and which Mongo profiler/privilege options exist.
