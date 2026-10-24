# Unravis Investigation MVP: Gap Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close only the gaps listed in `docs/findings/2026-10-23-unravis-mvp-review.md`, so that every MVP area and every one of the 23 filters becomes Complete and every spec rule holds. The plan ends by renaming the product to **Unravis**.

**Architecture:**
- Keep the histogram-bucket summary pipeline, the cohort key, the Mongo-only source and the existing endpoints.
- Fix the classification at its source, which is the `_auditState` and `_auditOutcome` stages in `buildSummaryPipeline` and their mirror in `normalizeLegacyRecord`.
- Add one shared record-filter builder, used by the summary pipeline, the record pages, slowest and breakdown. Because every pipeline uses the same filter, every aggregate drills to exactly the records it counted (rule 7).
- Add two endpoints: `/api/breakdown` and `/api/record`. Everything else is additive fields on existing JSON.

**Tech Stack:**
- Go (module `api-audit-explorer`, kept), the MongoDB Go driver v2 and the legacy 3.4 path.
- Svelte 5, Vite and TypeScript in `web/`.
- Tests:
  - Go: `go test ./...`.
  - Web: `npm --prefix web test` (`node --test tests/pure.test.cjs`, loads TS through `loadTypeScript('src/lib/x.ts')`) and `npm --prefix web run check`.

**Spec:** `docs/superpowers/specs/2026-10-23-unravis-investigation-mvp.md`. Review: `docs/findings/2026-10-23-unravis-mvp-review.md`.

## Global Constraints

- Preserve working behaviour. Do not rewrite the architecture. Existing endpoints, MCP tools and saved investigations keep working.
- **Make JSON changes additive.**
  - New Go fields use `omitempty`, or a pointer type where nil is meaningful.
  - `web/src/lib/types.ts` mirrors each change in the same task.
  - `DisallowUnknownFields` is kept (`internal/server/server.go:603-627`), so the request structs must declare the new fields.
- **Keep the existing limits:** 8 sources, 40,000 summary rows, 10,000 cohorts, 120 slots, 7-day period, 5-minute timeout, 16 MiB analysis, page size ≤ 100, CSV ≤ 10,000 rows.
  - New caps: breakdown top-N ≤ 50 plus "Other"; slowest cursor pages ≤ 50.
- **Keep the legacy Mongo 3.4 path valid.**
  - Pipelines may use only operators available in 3.4: `$switch`, `$in` (aggregation), `$indexOfBytes`, `$regex` in `$match`, and `$cond`. They must not use `$convert`, `$toString`, `$regexMatch` or `$dateDiff`.
  - Anything that the 3.4 pipeline cannot express is applied in Go inside `normalizeLegacyRecord` or the legacy readers, with the same semantics.
  - Each task adds a legacy test.
- The audit source stays Mongo-only. Every database access is read-only. Do not add indexes.
- The cohort key stays `CohortKey(source, controller, action, kind, outcome)`. Any new dimension is a filter or a counter, never a key part. The one exception is the new kind and outcome **values**, which are additive enum members.
- **Classification rules apply everywhere:**
  - Pending async (`Asynchronous ` + `Accepted`) is incomplete.
  - Incomplete records are never success, never fast, and never enter percentiles.
  - 4xx and 5xx are counted separately.
  - Sync, async and Hangfire are never merged in any rollup.
  - Incomplete HTTP rows are labelled "HTTP (polling type unknown)".
- No field is invented. The UI labels say "Observed API time = AuditCreateTime + ApiTime".
- Where a filter or measure is derived, the UI tooltip says so. For example, incomplete age is `now − RequestInitiatedAt`, falling back to `CreatedOn`.

## Review Focus

1. **Async records with status `Accepted`.** They must be counted as incomplete, with outcome `unknown`.
   - Ensure a synchronous `Accepted` (a 202 returned inline) remains complete and successful (Task 1).
   - Live check: run `db.ApiAuditLog.countDocuments({RequestPollingType:"Asynchronous ",StatusCode:"Accepted"})` per source, and compare it to the number of async rows older than 1 h.
2. **Status names.** Every .NET `HttpStatusCode` name must map to a number and a class.
   - Numeric strings and Hangfire `Succeeded` and `Failed` must map too.
   - An unknown name stays `unknown` and is never counted as success (Task 2).
3. **Filters and drill-down.** With any combination of filters, every count shown must equal the number of records returned by its drill-down: Tasks 4–6, with a property test in Go on the demo repository.
4. **Phase filters.** A range filter on AuditCreateTime, ApiTime or Observed time must exclude incomplete records explicitly, and the UI must say so.
   - An incomplete-age filter must exclude complete records.
5. **Cardinality of UserId, UserAgent and UserIp.** Results are capped at top-N plus "Other".
   - UserIp is shown only when the user expands it in the UI, and MCP returns it only when `includeIp:true` (Task 9 and Task 13).
6. **Legacy 3.4 sources.** Each new filter and counter must produce the same numbers through `legacyMetadataPipeline` and `normalizeLegacyRecord` as through the modern pipeline.

---

## Phase 1: Correct the numbers (dependencies for everything else)

### Task 1: Completion semantics — pending async is incomplete; Observed time labelled honestly

Tier: **standard** · Live-data verification: **yes** (Review Focus 1)

**Files:**
- Modify: `internal/store/pipeline.go`
  - the `_auditState` stage (105-111): add the branch `RequestPollingType` trimmed equals `Asynchronous` and upper(`StatusCode`) is `ACCEPTED` → `"incomplete"`
  - outcome (56-66): the same condition gives `"unknown"`
  - `statusNames` (157-162): keep `ACCEPTED` in success for every other case
- Modify: `internal/store/legacy_pipeline.go`
  - `legacyOutcome` (234) and `normalizeLegacyRecord` (533-543) get the same rule
  - legacy rows with no `RequestInitiatedAt` set `AuditCreateMissing=true`
- Modify: `internal/store/requests.go` (record `state` projection; see Task 4)
- Test: `internal/store/pipeline_test.go`, `internal/store/legacy_pipeline_test.go`

**Interfaces:**
- Produces:
  - `audit.Quality.PendingAsync int64 \`json:"pendingAsync,omitempty"\``
  - `audit.Stats.ObservedPartial int64 \`json:"observedPartial,omitempty"\``: the count of samples where AuditCreateTime was unavailable (legacy `CreatedOn` fallback)
  - The UI renames "Total request" to "Observed API time (AuditCreateTime + ApiTime)" in `web/src/components/QueryControls.svelte` and `web/src/lib/presentation.ts`.

- [ ] **Step 1:** Write test `TestSummaryPipelineAsyncAcceptedIsIncomplete`. Render the pipeline and evaluate the state and outcome expressions against these fixtures:
  - `{RequestPollingType:"Asynchronous ",StatusCode:"Accepted",RequestCompletedAt:t}` → `incomplete`/`unknown`
  - `{RequestPollingType:"Synchronous ",StatusCode:"Accepted"}` → `valid`/`success`
  - `{RequestPollingType:"Asynchronous ",StatusCode:"OK"}` → `valid`/`success`

  Use the existing expression-evaluation helper. If there is none, assert on the JSON-rendered `$switch` branches.
- [ ] **Step 2:** Write test `TestLegacyAsyncAcceptedIsIncomplete`. Feed the same three fixtures through `normalizeLegacyRecord`; the states must match Step 1.
- [ ] **Step 3:** Write test `TestLegacyMissingInitiatedMarksObservedPartial`. When only `CreatedOn` is present, the duration equals ApiTime and `ObservedPartial` is 1.
- [ ] **Step 4:** Run the tests and see them fail. Implement. Run `go test ./...` and see them pass.
- [ ] **Step 5:** Make the UI changes:
  - Rename the metric label.
  - Make the quality strip show "pending async N".
  - Make the Stats tooltip show "N samples lack AuditCreateTime (legacy)" when `observedPartial > 0`.
  - Run `npm --prefix web run check`.
- [ ] **Step 6:** Live check from Review Focus 1. Record the counts per source in the PR description.
- [ ] **Step 7:** Commit.

### Task 2: Status classes — 4xx separate from 5xx; complete .NET name map

Tier: **standard** · Live-data verification: **yes** (distinct `StatusCode` values per source)

**Files:**
- Create: `internal/audit/status.go`
  - `StatusCodeNumber(name string) (int, bool)` covers all .NET `HttpStatusCode` names, including the aliases `Redirect`, `Moved`, `Ambiguous`, `RedirectKeepVerb`, `RedirectMethod`, `Unused`, `RequestEntityTooLarge`, `RequestUriTooLong`, `RequestedRangeNotSatisfiable`, `UnprocessableContent`/`UnprocessableEntity` and the 1xx names. It also accepts numeric strings.
  - `StatusClass(kind, state, status string) string` returns one of `success | redirect | client_error | server_error | hangfire_failed | pending | unknown`.
- Modify: `internal/store/pipeline.go`
  - Build the `statusNames` arrays and a new `_statusClass` `$switch` from `status.go`.
  - Add `$sum` counters to the existing `$group`: `c4xx`, `c5xx`, `cHangfireFailed`. Do not add a group key.
  - The outcome filter at line 135 gains `client_error` (4xx) and `server_error` (5xx + hangfire_failed).
- Modify: `internal/store/legacy_pipeline.go` and `internal/store/analyze.go` (`bucketDocument` counters)
- Modify: `internal/audit/models.go`, `query.go:47-51`, `analyze.go` (addBucket), `load_correlation.go:31-71` (rollup error rate split)
- Modify: `web/src/lib/types.ts`, `web/src/components/QueryControls.svelte:54`, `web/src/components/ComparisonTable.svelte`
- Test: `internal/audit/status_test.go`, `internal/store/pipeline_test.go`, `web/tests/pure.test.cjs`

**Interfaces:**
- Produces:
  - `Bucket.Count4xx, Count5xx, CountHangfireFailed int64` (all omitempty)
  - `Stats.ClientErrors, ServerErrors int64` and `Stats.ClientErrorRate, ServerErrorRate *float64`
  - `Query.Outcome` accepts `client_error`, `server_error` in addition to `success`, `failure` and `all`. `failure` keeps its meaning (4xx + 5xx + hangfire_failed).
  - The rollup gains `ClientErrorRate` and `ServerErrorRate`.

- [ ] **Step 1:** Write test `TestStatusCodeNumberCoversDotNetEnum`. Use a table of every `System.Net.HttpStatusCode` name and its number (from the .NET docs), including the aliases. Assert there are no misses. `"Succeeded"`/`"Failed"` → class `success`/`hangfire_failed` for kind hangfire.
- [ ] **Step 2:** Write test `TestStatusClassNeverSuccessForUnknownOrPending`. `""`, `"Weird"` → `unknown`; async Accepted → `pending`.
- [ ] **Step 3:** Write test `TestSummaryPipelineCounts4xx5xxSeparately`. The JSON contains the three `$sum` counters, and the outcome `client_error` match uses only 4xx names.
- [ ] **Step 4:** Write test `TestLegacyCounts4xx5xxSeparately`. The legacy reader gives the same counters on fixtures.
- [ ] **Step 5:** Implement. Run `go test ./...`.
- [ ] **Step 6:** UI changes:
  - Add the outcome options "Client errors (4xx)" and "Server errors (5xx)".
  - ComparisonTable gets "4xx %" and "5xx %" columns that replace the merged "Error rate". The merged value stays in a tooltip.
  - Add a pure test `statusRates splits 4xx and 5xx` in `pure.test.cjs` for the helper in `web/src/lib/presentation.ts`.
  - Run `npm --prefix web test` and `npm --prefix web run check`.
- [ ] **Step 7:** Live check: run `distinct("StatusCode")` per source and confirm none map to `unknown` except genuinely absent values. Record the result.
- [ ] **Step 8:** Commit.

### Task 3: Kind separation — kind-scoped quality, per-kind rollups, "HTTP (polling type unknown)"

Tier: **standard** · Live-data verification: no

**Files:**
- Modify: `internal/audit/query.go:42-46`
  - The kind enum gains `http`, which is synchronous, asynchronous and unknown HTTP rows together.
  - It also gains `http_unknown`, which is the existing pipeline value `unknown`, exposed under this name.
- Modify: `internal/store/pipeline.go:132`
  - The kind filter keeps a non-valid row only when that row's kind can belong to the selected kind: `unknown` belongs to `http`, `synchronous` and `asynchronous` and is labelled as such, but never to `hangfire`.
  - The Hangfire kind detection stays the same.
- Modify: `internal/audit/analyze.go:149-172` so that `addQuality` counts only buckets whose kind matches the query (or is compatible `unknown`), and reports them per kind.
- Modify: `internal/audit/load_correlation.go:31-71` so that rollups are keyed by `(controller, action, kind)`. Sources are still merged and labelled "all sources".
- Modify: `web/src/components/QueryControls.svelte:53` (options), `web/src/lib/presentation.ts` (label `unknown` → "HTTP (polling type unknown)"), `web/src/lib/types.ts`
- Test: `internal/audit/analyze_test.go`, `internal/audit/load_correlation_test.go`, `internal/store/pipeline_test.go`, `web/tests/pure.test.cjs`

**Interfaces:**
- Produces:
  - `Quality.ByKind map[string]QualityCounts \`json:"byKind,omitempty"\``
  - `Rollup.Kind string \`json:"kind,omitempty"\``
  - New accepted `kind` values. The default stays `synchronous` (`server.go:483-493`, `web/src/lib/time.ts:26-36`).

- [ ] **Step 1:** Write test `TestQualityRespectsKindFilter`. Use buckets of kinds hangfire-incomplete and synchronous-valid with query kind synchronous. Assert that `Quality.Incomplete` excludes the Hangfire rows.
- [ ] **Step 2:** Write test `TestRollupsNeverMergeKinds`. The same controller/action as sync and as async must give 2 rollups.
- [ ] **Step 3:** Write test `TestKindHTTPIncludesUnknownExcludesHangfire`, asserting on the pipeline JSON. Also cover the legacy `legacyKind` equivalent.
- [ ] **Step 4:** Implement. Run `go test ./...`.
- [ ] **Step 5:** UI changes:
  - Add the options and the label.
  - Add the tooltip "Incomplete HTTP rows have no polling type until the audit update is written; they cannot be attributed to Synchronous or Asynchronous."
  - Run the web test and check.
- [ ] **Step 6:** Commit.

### Task 4: Drill-down integrity for every cohort and quality count

Tier: **standard** · Live-data verification: no

**Files:**
- Modify: `internal/store/requests.go`
  - `validateCohortQuery` (344-355) accepts a cohort whose kind is `unknown` when the query kind is `synchronous`, `asynchronous`, `http` or `all`, and whose outcome is `unknown` under any outcome filter. This is exactly the set of rows that `pipeline.go:132,135` lets into the table.
  - Project `state` and `id` (the hex `_id`).
- Modify: `internal/audit/models.go`
  - `PageInput.State string` (`complete|incomplete|invalid|pending|all`; empty means all)
  - `RequestRecord.State`
- Modify: `web/src/App.svelte:815-822` so that each quality count is a button that opens RequestTable with `state` set. `web/src/components/RequestTable.svelte` shows the state badge and the `_id`.
- Modify: `internal/audit/demo.go` (Requests honours State)
- Test: `internal/store/requests_test.go`, `internal/server/server_test.go`

**Interfaces:**
- Produces: `PageInput.State`, `RequestRecord.State`, `RequestRecord.ID` (already present as `id`; it becomes visible in the UI).

- [ ] **Step 1:** Write test `TestUnknownKindCohortDrillableUnderSynchronousFilter`. Encode the cohort `[src,c,a,"unknown","unknown"]` with query kind=synchronous and outcome=success. `validateCohortQuery` returns nil.
- [ ] **Step 2:** Write test `TestHangfireCohortRejectedUnderSynchronousFilter`. It is still rejected.
- [ ] **Step 3:** Write test `TestRequestsStateFilterIncomplete`. The pipeline JSON contains the state match, and the demo repository returns only incomplete rows.
- [ ] **Step 4:** Write test `TestDemoQualityCountEqualsDrillCount`. For each state, `Analyze().Quality` must equal the total number of rows paged from `Requests(state)`. This is the rule 7 property.
- [ ] **Step 5:** Implement, then make the UI change. Run `go test ./...`, the web test and check.
- [ ] **Step 6:** Commit.

---

## Phase 2: Filters and evidence

### Task 5: Shared record filter — identity, status, completion and text filters

Tier: **standard** · Live-data verification: **yes** (query plan and timing of the regex filters on a 7-day window, without adding any index)

**Files:**
- Create: `internal/store/filters.go`
  - `recordMatch(q audit.Query) bson.D` covers only the raw-field filters and is usable in Mongo 3.4.
  - `recordPostMatch(q audit.Query) bson.D` covers the derived fields `_auditState` and `_statusClass`.
  - `buildSummaryPipeline` (`pipeline.go:20-155`), `requestPipeline` (`requests.go:243-316`), `slowestPipeline` and Task 9 all use both.
- Modify: `internal/audit/models.go` (Query additive fields), `internal/audit/query.go` (validation: lengths ≤ 200 bytes, at most 20 status codes, ObjectId hex)
- Modify: `internal/store/legacy_pipeline.go`
  - Add `UserId`, `UserIp`, `PayrollUIVersion`, `CreatedOn` and `UpdatedOn` to `legacyMetadataFields` (28-33), with string caps at 35-40.
  - Apply the derived filters in Go.
- Modify: `web/src/components/QueryControls.svelte` (a collapsible "Record filters" panel), `web/src/lib/types.ts`, `internal/audit/demo.go`
- Test: `internal/store/filters_test.go`, `internal/audit/query_test.go`, `web/tests/pure.test.cjs`

**Interfaces:**
- Produces these Query fields, all `omitempty`:
  - `UserIDs []int`
  - `UIVersions []string`
  - `UserAgentContains string`
  - `UserIPs []string`
  - `URLContains string`
  - `AuditID string`
  - `StatusCodes []string` (raw names, compared case-insensitively)
  - `Completion string` (`complete|incomplete|pending|invalid|all`)
  - `Operation string` (`Controller.Action`, which splits into the existing Controller and Action)
- Text "contains" filters use `{$regex: regexp.QuoteMeta(s), $options:"i"}`.
- **Every filter applies to the summary as well, so all aggregates reflect it.** The filters are also part of `pageFingerprint`, so cursors from before a filter change are rejected.

- [ ] **Step 1:** Write test `TestRecordMatchEscapesRegex`. `URLContains:"a.b(c"` renders `a\.b\(c` with option `i`.
- [ ] **Step 2:** Write test `TestRecordMatchOnly34Operators`. Walk the rendered BSON and fail on `$regexMatch`, `$convert` or `$toString`.
- [ ] **Step 3:** Write test `TestSummaryAndRequestsShareFilter`. The same `recordMatch` value appears in the summary, requests and slowest pipelines.
- [ ] **Step 4:** Write test `TestValidateQueryRejectsBadAuditID`. Also reject a 201-byte contains filter and 21 status codes.
- [ ] **Step 5:** Write test `TestLegacyFiltersMatchModern`. Run fixtures through the legacy path with `UserIDs:[7]`, `Completion:"incomplete"` and `UIVersions:["2.1"]`. The resulting counts must equal those of the modern evaluation.
- [ ] **Step 6:** Implement. Run `go test ./...`.
- [ ] **Step 7:** UI changes:
  - Add the panel. Free-text lists are comma-separated, and Operation is a combobox fed by the loaded group names.
  - Show the active filters as removable chips.
  - Add a pure test `serializeRecordFilters omits empties` for `web/src/lib/investigation.ts`. Saved investigations without the new fields must still load.
  - Run the web test and check.
- [ ] **Step 8:** Live check: time a 24 h and a 7 d query with `URLContains` and with `UserIDs`. Record the timings and confirm they stay under the 5-minute timeout.
- [ ] **Step 9:** Commit.

### Task 6: Range filters — Observed, AuditCreateTime, ApiTime, request/response size, incomplete age

Tier: **cheap** (it follows the Task 5 pattern) · Live-data verification: no

**Files:**
- Modify: `internal/audit/models.go` and `query.go` to add `Range{Min,Max *float64}` and the fields below. Validation: min ≤ max, both ≥ 0.
- Modify: `internal/store/filters.go`
  - `recordPostMatch` matches on the computed `_durationMs`, `_apiMs`, `_auditMs`, `RequestLengthInKiloBytes`/`ResponseLengthInKiloBytes` and `_ageMs`. `_ageMs` is `$$NOW` minus `RequestInitiatedAt`, falling back to `CreatedOn`; `$$NOW` is not available on 3.4, so the server time is passed as a literal for both paths.
  - A latency range also implies `Completion=complete`. Incomplete age implies `Completion=incomplete|pending`.
- Modify: `internal/store/legacy_pipeline.go` (same checks in Go), `web/src/components/QueryControls.svelte` (min/max inputs in the Task 5 panel, with the implication tooltips), `web/src/lib/types.ts`
- Test: `internal/store/filters_test.go`

**Interfaces:**
- Produces Query fields, all `omitempty`:
  - `ObservedMs`
  - `ApiTimeMs`
  - `AuditCreateTimeMs`
  - `RequestKB`
  - `ResponseKB`
  - `IncompleteAgeMs` (all `*Range`)

- [ ] **Step 1:** Write test `TestLatencyRangeImpliesComplete`. The rendered match contains the state `valid` together with the duration bounds.
- [ ] **Step 2:** Write test `TestIncompleteAgeUsesLiteralNow`. There must be no `$$NOW`, the injected clock must be respected, and the `CreatedOn` fallback must be used.
- [ ] **Step 3:** Write test `TestLegacyRangeFiltersMatchModern`.
- [ ] **Step 4:** Implement. Run `go test ./...`, the web test and check.
- [ ] **Step 5:** Commit.

### Task 7: Record detail — full underlying audit evidence

Tier: **standard** · Live-data verification: **yes** (large payloads and legacy documents render within the caps)

**Files:**
- Create: `internal/store/record.go`
  - `Record(ctx, sourceID, id)` returns every schema field: `_id`, `ControllerName`, `ActionName`, `RequestMainUrl`, `UserId`, `UserIp`, `UserAgent`, `CreatedOn`, `RequestInitiatedAt`, `RequestCompletedAt`, `UpdatedOn`, `AuditCreateTime`, `ApiTime`, `PayloadRecordTime`, `RequestLengthInKiloBytes`, `ResponseLengthInKiloBytes`, `PayrollUIVersion`, raw `StatusCode`, raw `RequestPollingType`, plus the derived kind, state, status class and Observed time.
  - The payloads go through the existing `payload.go` cap.
  - Unknown extra fields are not returned.
- Modify: `internal/server/server.go` (route `POST /api/record`, request struct with `sourceId`, `id`), `internal/audit/models.go` (`RecordDetail`, the `Repository` interface), `internal/audit/demo.go` (demo implementation)
- Create: `web/src/components/RecordDetail.svelte` (a field table plus the existing `PayloadViewer`). Modify `web/src/App.svelte:846-848` and `web/src/components/RequestTable.svelte` ("Inspect" opens RecordDetail), and `web/src/lib/api.ts`.
- Test: `internal/store/record_test.go`, `internal/server/server_test.go`

**Interfaces:**
- Produces `POST /api/record {sourceId,id}`, which returns `RecordDetail`.
- `RequestRecord` gains the additive fields `userId *int`, `userIp`, `userAgent`, `uiVersion`, `createdOn`, `updatedOn` and `pollingTypeRaw` (all omitempty) for the table and CSV export.

- [ ] **Step 1:** Write test `TestRecordProjectionListsOnlySchemaFields`. The projection keys must be exactly the 21 schema fields.
- [ ] **Step 2:** Write test `TestRecordRejectsBadIDAndUnknownSource`.
- [ ] **Step 3:** Write test `TestServerRecordRouteDisallowsUnknownFields`.
- [ ] **Step 4:** Write test `TestLegacyRecordDerivesStateLikeModern`.
- [ ] **Step 5:** Implement. Add AuditCreateTime, UserId and UI version columns to RequestTable and to the CSV export (keep the 10,000-row cap). Run `go test ./...`, the web test and check.
- [ ] **Step 6:** Live check: open one complete, one incomplete, one pending async and one Hangfire record per source.
- [ ] **Step 7:** Commit.

### Task 8: Slow requests — server-side threshold and percentile, cursor, phase columns, tail

Tier: **standard** · Live-data verification: no

**Files:**
- Modify: `internal/store/slowest.go:30-48`
  - Take the minimum from `Query.ObservedMs`/`ApiTimeMs` (Task 6).
  - Add a keyset cursor on `(metric desc, initiatedAt desc, _id desc)` with pages of 50 or fewer.
  - `audit.ValidateSlowest` accepts the cursor.
- Modify: `web/src/components/TrafficSlowness.svelte`
  - The threshold input now drives the server filter.
  - Add a "≥ P95 / ≥ P99 of the selected cohort" option, which converts the cohort stat into a minimum in ms on the client and labels it "histogram estimate".
  - Add "Load more".
- Modify: `web/src/components/ComparisonTable.svelte` (P50, P90 and P99 columns, with P99 showing "needs ≥ 100 samples" when nil), `web/src/lib/types.ts`
- Test: `internal/store/slowest_test.go`, `web/tests/pure.test.cjs`

**Interfaces:**
- Produces `Page.NextCursor` for slowest. No new endpoint.

- [ ] **Step 1:** Write test `TestSlowestCursorIsStableAcrossPages`. On the demo repository, the union of the pages must equal the sorted top-N, with no duplicates.
- [ ] **Step 2:** Write test `TestSlowestHonoursMinObserved`.
- [ ] **Step 3:** Write the pure test `percentileThreshold uses cohort p95/p99 and returns null below sample floor`.
- [ ] **Step 4:** Implement. Run `go test ./...`, the web test and check.
- [ ] **Step 5:** Commit.

---

## Phase 3: Investigation views

### Task 9: Breakdown endpoint and UI — StatusCode, UserId, PayrollUIVersion, UserAgent, UserIp, RequestPollingType

Tier: **standard** · Live-data verification: **yes** (cardinality and timing per dimension)

**Files:**
- Create: `internal/store/breakdown.go`
  - Run one `$group` per selected dimension, inside the same `recordMatch` and `recordPostMatch` used by the summary.
  - For each value, compute `count`, `complete`, `incomplete`, `pending`, `c4xx`, `c5xx` and the histogram bins of the valid records, giving P50/P95 through the existing `internal/audit/histogram.go`.
  - Return the top N (default 20, max 50) by count plus "Other".
  - The legacy path groups in Go, with the same caps.
- Modify: `internal/server/server.go` (route `POST /api/breakdown`), `internal/audit/models.go` (`BreakdownInput{Query, Dim, Top}` and `Breakdown{Rows, Other, Total}`), `internal/audit/demo.go`
- Create: `web/src/components/BreakdownPanel.svelte`
  - Show the dimension tabs.
  - Clicking a row applies the corresponding Task 5 filter and opens RequestTable, which gives the drill-down.
  - The UserIp tab is collapsed by default.
- Modify: `web/src/App.svelte`, `web/src/lib/api.ts`, `web/src/lib/types.ts`
- Test: `internal/store/breakdown_test.go`, `web/tests/pure.test.cjs`

**Interfaces:**
- Produces `POST /api/breakdown`.
- `Dim` is one of `statusCode | userId | uiVersion | userAgent | userIp | pollingType`. `pollingType` is the raw value, trimmed, with missing shown as "(not yet written)".
- Each row's `filter` is a Query fragment that the UI and MCP apply unchanged.

- [ ] **Step 1:** Write test `TestBreakdownTopNPlusOtherSumsToTotal`.
- [ ] **Step 2:** Write test `TestBreakdownRowFilterDrillCountMatches`. Run it on the demo repository: each row's count must equal the number of records paged with that row's `filter` (rule 7).
- [ ] **Step 3:** Write test `TestBreakdownPollingTypeMissingLabel`.
- [ ] **Step 4:** Write test `TestLegacyBreakdownMatchesModern`.
- [ ] **Step 5:** Implement. Write the pure test `breakdownRowFilter merges into query without dropping existing filters`. Run `go test ./...`, the web test and check.
- [ ] **Step 6:** Live check: time each dimension on 24 h and on 7 d, and record the distinct counts.
- [ ] **Step 7:** Commit.

### Task 10: Failure investigation — status panel, failure trends, drill by status

Tier: **standard** · Live-data verification: no

**Files:**
- Modify: `internal/audit/models.go:206-212`. `Point` gains `Count4xx`, `Count5xx`, `Incomplete`, `P90` and `P99` (all omitempty), with `P99` nil when there are fewer than 100 samples.
- Modify: `internal/audit/analyze.go:116-133`. Timeline points are built from all states for the counts, and from valid records only for the percentiles.
- Create: `web/src/components/FailurePanel.svelte`
  - The 4xx and 5xx trend uses `web/src/lib/charts.ts`.
  - The status table reuses `BreakdownPanel` with dim `statusCode`.
  - Clicking a status sets `StatusCodes` and opens RequestTable.
- Modify: `web/src/App.svelte`, `web/src/components/MultiTimeline.svelte` (the series toggle P50, P90, P95, P99 / 4xx / 5xx), `web/src/lib/types.ts`
- Test: `internal/audit/analyze_test.go`, `web/tests/pure.test.cjs`

**Interfaces:**
- Produces the additive Point fields.

- [ ] **Step 1:** Write test `TestTimelinePointCountsFailuresFromAllStatesPercentilesFromValid`.
- [ ] **Step 2:** Write test `TestTimelinePointP99NilBelow100`.
- [ ] **Step 3:** Write the pure test `failureSeries returns null for missing slots, never zero`.
- [ ] **Step 4:** Implement. Run `go test ./...`, the web test and check.
- [ ] **Step 5:** Commit.

### Task 11: Incomplete investigation — completion rate and stale list

Tier: **standard** · Live-data verification: **yes** (choose the default stale threshold from the observed lag between `CreatedOn` and `UpdatedOn`)

**Files:**
- Modify: `internal/audit/models.go` and `analyze.go`. `Stats` gains `Events`, `Incomplete`, `Pending` and `CompletionRate *float64`, computed per cohort and per rollup.
- Create: `web/src/components/IncompletePanel.svelte`
  - Show the completion rate per operation, sorted worst-first.
  - The stale list is RequestTable with `Completion=incomplete|pending`, `IncompleteAgeMs.Min` set to the threshold (default 10 min, editable) and the oldest first.
- Modify: `internal/store/requests.go` (add an `oldestFirst` sort option to `PageInput`, used only with an incomplete completion filter), `web/src/components/ComparisonTable.svelte` (a "Completion %" column), `web/src/App.svelte`
- Test: `internal/audit/analyze_test.go`, `internal/store/requests_test.go`, `web/tests/pure.test.cjs`

**Interfaces:**
- Produces the additive Stats fields and `PageInput.Order string` (`newest|oldest`, defaulting to newest).

- [ ] **Step 1:** Write test `TestCompletionRateExcludesInvalidCountsPending`. The rate is `complete / (complete + incomplete + pending)`. An invalid record is counted separately and the result says so.
- [ ] **Step 2:** Write test `TestOldestFirstRejectedWithoutIncompleteFilter`.
- [ ] **Step 3:** Write the pure test `completionRate is null when events is 0`.
- [ ] **Step 4:** Implement. Run `go test ./...`, the web test and check.
- [ ] **Step 5:** Live check of the lag. Set the default stale threshold from the result and record it.
- [ ] **Step 6:** Commit.

### Task 12: Overview strip, operation rows, quick ranges, compare deltas

Tier: **standard** · Live-data verification: no

**Files:**
- Modify: `internal/audit/analyze.go` and `models.go`
  - Add `Analysis.Overview []OverviewRow`, with one row per kind in scope. Each row carries events, completion rate, P50, P90, P95 and P99 from the merged valid histograms, the 4xx rate, the 5xx rate and the reference values for each of these.
  - Rollups (Task 3) gain the reference-window values for the traffic, failure and completion deltas.
- Create: `web/src/components/OverviewStrip.svelte`. Each tile drills down by applying the kind and state or status-class filter, then opening RequestTable. It replaces the quality strip at `App.svelte:815-822`, keeping the existing counts.
- Modify: `web/src/lib/time.ts`
  - `quickRange(id, now)` takes an `id` of `15m | 1h | 4h | 24h | 7d`. It returns the current window and the previous equal-length window as the baseline, both clamped to the 7-day limit.
  - QueryControls gets the preset buttons.
- Modify: `web/src/components/ComparisonTable.svelte` ("Operation" view toggle: one row per `controller+action+kind` from the rollups, with Δ traffic, Δ P95, Δ 4xx, Δ 5xx and Δ completion)
- Test: `internal/audit/analyze_test.go`, `web/tests/pure.test.cjs`

**Interfaces:**
- Produces `Analysis.Overview` (omitempty) and the reference fields on the rollup.

- [ ] **Step 1:** Write test `TestOverviewMergesHistogramsPerKindNeverAcrossKinds`.
- [ ] **Step 2:** Write test `TestOverviewDeltasNilWhenReferenceInsufficient`. With fewer than 20 samples the state is `insufficient_data` (the existing rule).
- [ ] **Step 3:** Write the pure test `quickRange 24h yields previous 24h baseline and respects 7d cap`.
- [ ] **Step 4:** Implement. Check the size of the output: `Overview` must be bounded by the number of kinds, which is 4 or fewer. Run `go test ./...`, the web test and check.
- [ ] **Step 5:** Commit.

### Task 13: MCP parity — new filters, breakdown, slowest, record ids

Tier: **cheap** · Live-data verification: no

**Files:**
- Modify: `internal/mcp/schema.go:35-56`
  - The query schema gains the enum values from Tasks 2 and 3 and the filter fields from Tasks 5 and 6.
  - Add the tools `audit_get_breakdown` (UserIp only when `includeIp:true`) and `audit_get_slowest`.
  - `audit_get_requests` now returns the record `_id`, `state`, status class and phases, but still no payloads, URLs, IPs or agents. Its description changes to match.
- Modify: `internal/mcp/tools.go` (handlers; line 257 description) and `investigationGuide` in `schema.go:3-15`:
  - add the completion, pending-async and 4xx/5xx rules
  - state that every finding must cite record ids
- Test: `internal/mcp/tools_test.go`

**Interfaces:**
- Produces 2 new MCP tools. Existing tool names and required arguments are unchanged.

- [ ] **Step 1:** Write test `TestMCPSchemaAdditiveOnly`. Every pre-existing property and required list must still be present.
- [ ] **Step 2:** Write test `TestMCPRequestsExposeIDNotPayloadOrIP`.
- [ ] **Step 3:** Write test `TestMCPBreakdownHidesIPUnlessRequested`.
- [ ] **Step 4:** Implement. Run `go test ./...`.
- [ ] **Step 5:** Commit.

---

## Phase 4: Rename

### Task 14: Rename to Unravis (user-visible only)

Tier: **cheap** · Live-data verification: no (confirm manually that the saved connections still appear after the rebuild)

**Decision on saved connections:** keep reading and writing `%APPDATA%\APIAuditExplorer\connections.dat` (`internal/history/history.go:96`). Keep the single-instance mutex `Local\APIAuditExplorer-<hex>` (`internal/history/platform_windows.go:78`) and the localStorage key `api-audit-explorer.investigation.v1`. These are internal storage identifiers, not something the user sees.

Why not migrate:
- **Zero migration risk.** The data is DPAPI-encrypted and stays where it is.
- **No two-instance hazard.** An old and a new exe running with different mutex names could write the same file during the upgrade window. Keeping the name rules that out.
- **Override unchanged.** `AUDIT_CONFIG_DIR` still works the same way.

A code comment records this choice so that a future rename does not "fix" it.

**Files:**
- Modify:
  - `web/index.html:9` (`<title>Unravis</title>`)
  - `web/src/App.svelte:653,657` (title, h1 "Unravis", tagline "API audit investigation"); `App.svelte:645` (export filename `unravis-requests.csv`)
  - `README.md:1,3,9,95`
  - `MCP.md:3,7,53,65`:
    - new exe path
    - server name `unravis`
    - a note that existing client configs that point at `dist/api-audit-explorer.exe` keep working for one release
- Modify:
  - `build.ps1:73`: output `dist/unravis.exe`, and also copy it to `dist/api-audit-explorer.exe` as a compatibility copy, with a comment giving the removal date
  - `run.ps1:8,10,17`
  - `Start Live.cmd:3,4,7`: window title "Unravis", new exe path
- Modify: `cmd/audit-explorer/main.go:45` (flagset name `unravis`) and `:115` (log line), `internal/mcp/protocol.go:171` (`serverInfo.name` = `unravis`, instructions say "Unravis"), `internal/mcp/bridge.go:66` ("Unravis is unavailable…")
- Keep unchanged:
  - `go.mod` module `api-audit-explorer` and all import paths
  - the `cmd/audit-explorer` directory
  - the `web/package.json` name
  - the APPDATA folder, mutex and localStorage key (decision above)
- Test: `internal/mcp/protocol_test.go`, `internal/history/history_test.go`

- [ ] **Step 1:** Write test `TestServerInfoNameIsUnravis`.
- [ ] **Step 2:** Write test `TestHistoryDirStillAPIAuditExplorer`. This guards the storage decision, and checks that `AUDIT_CONFIG_DIR` still overrides it.
- [ ] **Step 3:** Make the edits. Grep the user-visible files for "API Audit Explorer" and `api-audit-explorer.exe`; only the documented compatibility mentions may remain.
- [ ] **Step 4:** Run `go test ./...`, `npm --prefix web test` and `npm --prefix web run check`. Then run `./build.ps1` and confirm that `dist/unravis.exe` and the compatibility copy both exist.
- [ ] **Step 5:** Manual check: launch the exe and confirm that the saved connections from before the rename are listed and the window title reads Unravis.
- [ ] **Step 6:** Commit.

---

## Superseded / kept / deferred (old plan `2026-10-23-api-slowness-diagnostics.md`)

| Old task | Disposition | New home / reason |
|---|---|---|
| 7: Breakdown query (user, uiVersion, sizes, hourOfDay, status; UserIp excluded) | **Superseded** | Task 9. The spec now requires UserAgent, UserIp and RequestPollingType. Sizes become filters (Task 6). hourOfDay is dropped as not in the spec. |
| 8: Breakdown/tail UI + MCP `get_phase_breakdown`/`get_breakdown` | **Superseded** | Tasks 8, 9 and 13. The `bin_bounds` Go↔TS contract test is **kept**, folded into Task 9 Step 5. |
| 9: Summary pipeline performance (closed-form bin, concurrent windows) | **Kept, deferred** | Run it after Task 13. New filters add `$match` work, so re-measure first. |
| 10: Telemetry seam + Mongo provider | **Deferred** | It is outside the MVP and adds no audit evidence. |
| 11–14: SQL Server connection, Query Store, waits, indexes | **Deferred** | The audit source stays Mongo-only for the MVP. |
| 15: DB evidence UI/MCP | **Deferred** | It depends on Tasks 10–14. |
| 16: App-side instrumentation (brain-payroll) | **Deferred**, separate repo plan | Recommend writing `RequestPollingType` at insert time and an explicit pending state for async. Together these remove the "HTTP (polling type unknown)" limitation. |

## Self-review

- **Coverage.** Every row that is not Complete in the review maps to a task:
  - Overview → 1, 2, 3, 12
  - Operation → 2, 3, 11, 12
  - Slow → 7, 8
  - Failure → 2, 9, 10
  - Incomplete → 1, 4, 11
  - Compare deltas → 12
  - Breakdown → 9
  - Detail → 7
  - Filters 2 → 12; 4–5 → 3; 6–8 → 5; 9 → 2, 5; 10–12, 14, 17–19 → 5; 13, 16, 20–22 → 6; 15 → 8
- **Rules.**
  - R5/R4 → 1
  - R6 → 2
  - R7 → 4, 5, 9, 13
  - R1 → 3, 12
  - R3 → 1
  - R2 → 12
- **Dependencies.**
  - 1 → 2 → 3 → 4 fix the numbers and drill-downs before any new view relies on them.
  - 5 → 6 build the shared filter that 7–13 reuse.
  - 14 is last, so the docs name the final features.
- **No architecture rewrite.** There are two new endpoints and one new store file per feature. Everything else is additive.

### Task 15: UI redesign - progressive depth (user request 2026-10-24)

Tier: design-heavy (Sonnet implementer, Opus finish review). Runs after Tasks 9-12. Spec: `docs/superpowers/specs/2026-10-24-unravis-ui-direction.md` (includes the binding CRAP principles: Contrast, Repetition, Alignment, Proximity). Files: `web/src/**` only (App.svelte, app.css, components, lib helpers); no Go changes. Steps: restructure to D0 Pulse / D1 Where / D2 Evidence / D3 Record with breadcrumb, filter chips and a collapsed "More filters" panel; new dark-first token set with light theme; keep all existing web tests green and add pure tests for any new state helper; one desktop+mobile screenshot round, one fix batch, one confirm round; rebuild `web/dist`.
