# Unravis Investigation MVP: Gap Review

Date: 2026-10-23 · Branch: `feature/slowness-diagnostics` · Spec: `docs/superpowers/specs/2026-10-23-unravis-investigation-mvp.md`

Method: I read the code statically and did not run any server. The writer behaviour comes from `brain-payroll-all-portals`, used as a read-only reference. Schema facts come from `docs/api-audit-log-schema.md`, which was treated as data only.

Statuses: **C** = Complete · **P** = Partially implemented · **M** = Missing · **I** = Incorrect.

## Facts that drive the classification

- **Writer lifecycle.** The HTTP insert writes only the following: controller, action, URL, IP, agent, `RequestInitiatedAt`, `UserId`, `CreatedOn` and `AuditCreateTime`. A queued update about 10 s later adds the payloads, sizes, `RequestCompletedAt`, `StatusCode`, `RequestPollingType`, `PayrollUIVersion`, `UpdatedOn`, `ApiTime` and `PayloadRecordTime`.
  - So an incomplete HTTP record has no polling type, status or UI version.
  - Sync and async **cannot** be told apart for such a record. The honest label is "HTTP (polling type unknown)": the record is not Hangfire because `UserAgent != "Hangfire"` and the URL does not start with `hangfire://`.
- **Async submit.** The submit writes `StatusCode="Accepted"`, `RequestPollingType="Asynchronous "` and `RequestCompletedAt` (the submit end). The final poll overwrites the status and sets `ApiTime` measured from `CreatedOn`.
  - So `Asynchronous` with `Accepted` means **pending or abandoned**. It does not mean completed.
- **Timing identities:**
  - `AuditCreateTime = CreatedOn − RequestInitiatedAt`
  - `ApiTime = RequestCompletedAt − CreatedOn`
  - `PayloadRecordTime = UpdatedOn − RequestCompletedAt`, which happens after the response and is not latency.
- **Fields absent from the schema:** request headers, exception text, host, tenant (it appears only inside the Hangfire URL), correlation id and DB timings.
  - **No MVP feature or filter depends on an absent field**, so nothing below is "field absent — cannot implement as named".
  - Nearest honest substitutes if someone asks for these:
    - error text: the `ResponsePayload` shown in record detail
    - tenant or host: URL search on `RequestMainUrl`
    - correlation: Audit ID plus the existing "Nearby activity" view

## 1. Investigation Overview

| Item | Status | Evidence | Defect |
|---|---|---|---|
| Traffic | P | `web/src/components/RequestVolume.svelte`; `internal/audit/analyze.go:208-210` (events counted for all states) | Volume is shown per cohort only. There is no overall or per-kind traffic total. |
| Completion | P | `internal/audit/models.go:223-227` Quality; `web/src/App.svelte:815-822` | Shows a raw incomplete count with no rate. `analyze.go:149-172` counts quality across **all** kinds, ignoring the kind filter (pipeline keeps non-valid rows, `internal/store/pipeline.go:132`). Pending async Accepted rows are counted as complete. |
| P50/P90/P95/P99 | P | `models.go:150-163` (P90/P99 computed); `web/src/components/ComparisonTable.svelte:58-60` (P99 only as a tooltip) | Values are per cohort only, with no overview row. P90 is computed but never displayed (`web/src/lib/types.ts:31`). |
| 4xx/5xx failures | **I** | `pipeline.go:157-162` statusNames; `internal/audit/load_correlation.go:31-71` | 4xx and 5xx are merged into one `failure` outcome. Several .NET names (`RequestEntityTooLarge`, `RequestUriTooLong`, `RequestedRangeNotSatisfiable`, `UnprocessableContent`, the 3xx aliases, all 1xx) fall into `unknown`. |
| Trends over time | P | `analyze.go:116-133`; `models.go:206-212` Point | Point has only Count, P50, P95 and P95Ratio, for valid records only. There is no failure, completion, P90 or P99 trend. |

## 2. Operation Performance

| Item | Status | Evidence | Defect |
|---|---|---|---|
| Controller + Action | P | Group key `pipeline.go:139`; rollup `load_correlation.go:31-71` | Rows are split per source, kind and outcome. The only combined row (the rollup) merges kinds and sources, which violates rule 1. |
| Request count | P | ComparisonTable "Current requests" column | It shows **valid samples** only. `currentEvents` exists but is not shown, so incomplete records are invisible. |
| Completion rate | M | none | There is no per-operation completion rate. |
| Percentiles | P | `models.go:150-163` | P50, P90 and P99 are not columns; only P95 is shown. |
| Failure rates | **I** | ComparisonTable "Error rate" (rollup, only when outcome=all); `pipeline.go:159` | 4xx and 5xx are merged, async Accepted counts as success, and kinds are merged. |
| Latency burden | C | `TotalTimeMs`/`ImpactMs` in Stats and the "Total impact" column | Correct as implemented. Values for async cohorts are contaminated by pending rows until Task 1 lands, which is the same root cause as rule 5. |

## 3. Slow Requests

| Item | Status | Evidence | Defect |
|---|---|---|---|
| Slowest individual requests | P | `internal/store/slowest.go:30-48`; `web/src/components/TrafficSlowness.svelte` | Capped at the top 50 with no cursor. It reuses the cohort filter only. |
| Threshold/percentile filtering | P | `TrafficSlowness.svelte` (client threshold, default 1000 ms) | The threshold only colours the scatter. It does not filter the server query, and there is no "≥ P95/P99" option. |
| AuditCreateTime vs ApiTime | P | `TrafficSlowness.svelte:38`; PhaseBar via `web/src/lib/phases.ts` | Shown in the slowest table and as cohort phase means. `RequestTable.svelte` has no AuditCreateTime column. |
| Tail latency | P | `ComparisonTable.svelte:58-60` | P99 is a tooltip only. P99 is nil below 100 samples, which is correct, but that is not surfaced. |

## 4. Failure Investigation

| Item | Status | Evidence | Defect |
|---|---|---|---|
| Status breakdown | M | none | There is no per-`StatusCode` count. |
| Failures by operation | P | Rollup error rate in `load_correlation.go` | 4xx and 5xx are merged, and kinds and sources are merged. |
| Failure trends | M | Point lacks failure counts (`models.go:206-212`) | There is no failure trend. |
| Drill-down to requests | P | `internal/store/requests.go:287-294` cohortMatch | A `failure` cohort can be drilled, but not by status code or status class. |

## 5. Incomplete Requests

| Item | Status | Evidence | Defect |
|---|---|---|---|
| Missing completion evidence | **I** | `pipeline.go:105-111` state; `internal/store/legacy_pipeline.go:533-543`; `App.svelte:815-822` | Async `Accepted` (pending) is classed as valid, fast and successful. The quality counts have no drill-down. An incomplete cohort with kind `unknown` cannot be drilled under the default kind filter (`requests.go:344-355`). |
| Completion rate | M | none | There is no rate overall or per operation. |
| Stale incomplete requests | M | none | There is no age filter or oldest-first list. Age can be derived as `now − RequestInitiatedAt`, falling back to `CreatedOn`. |

## 6. Compare

| Item | Status | Evidence | Defect |
|---|---|---|---|
| Current period vs baseline | C | `models.go:180-204` Comparison; reference window in `internal/store/analyze.go:113-117` | No defect. |
| Traffic, latency, failure, completion changes | P | Comparison has a latency ratio; reference events exist | There is no failure-rate or completion-rate delta. The rollups cover the current window only. |
| Per-operation regression | C | Per-cohort P95 ratio, impact and significance | Correct. It inherits the pending-async contamination (Task 1). |

## 7. Breakdown

| Item | Status | Evidence | Defect |
|---|---|---|---|
| UserId | M | Not in `RequestRecord` (`models.go:265-282`) or `legacyMetadataFields` (`legacy_pipeline.go:28-33`) | No breakdown and no projection. |
| PayrollUIVersion | M | Same | No breakdown and no projection. |
| UserAgent | M | Read only for kind detection (`pipeline.go:45-52`) | No breakdown and no projection. |
| UserIp | M | Same | No breakdown and no projection. Old plan Task 7 excluded it deliberately; the spec now requires it. |
| RequestPollingType | P | kind `$switch` at `pipeline.go:45-52` | Kind exists. Incomplete HTTP rows become `unknown`, which no selector can choose (`internal/audit/query.go:42-46`), and there is no count-by-type view. |

## 8. Request Detail

| Item | Status | Evidence | Defect |
|---|---|---|---|
| Full underlying audit evidence | P | `internal/store/payload.go:47-51`; `RequestTable.svelte`; `PayloadViewer.svelte` | Shows the payloads and partial metadata only. `UserId`, `UserIp`, `UserAgent`, `PayrollUIVersion`, `CreatedOn`, `UpdatedOn`, raw `StatusCode` and raw `RequestPollingType` are never shown. The table does not display `_id`, and Audit ID lookup is not possible. |

## Required filters (23)

Server query fields: `models.go:35-47`. UI: `web/src/components/QueryControls.svelte`.

| # | Filter | Status | Evidence | Defect |
|---|---|---|---|---|
| 1 | Date/time range | C | `Start/End`; QueryControls date fields | No defect. |
| 2 | Quick time range | M | `web/src/lib/time.ts:26-36` (fixed default of 15 min) | There are no presets. |
| 3 | Time bucket/window size | C | `BucketSeconds`; QueryControls:56 | No defect. |
| 4 | HTTP/Hangfire | P | `query.go:42-46` | There is no "HTTP" option (sync + async + unknown). The `unknown` kind cannot be selected. |
| 5 | Polling type | P | QueryControls:53 | Sync and async are selectable. Incomplete HTTP rows leak into every kind's results (`pipeline.go:132`), and their drill-down is then rejected. |
| 6 | Controller | P | `pipeline.go:28-33` (exact match) | Exact, case-sensitive free text with no list of values. "Contains" works client-side only, on loaded groups (`ComparisonTable.svelte:30-33`). |
| 7 | Action | P | Same | Same defect. |
| 8 | Operation | P | controller + action fields | There is no single `Controller.Action` filter or picker. |
| 9 | Status/status class | **I** | `query.go:47-51` outcome success/failure; `pipeline.go:157-162` | 4xx and 5xx cannot be separated. Accepted counts as success. Specific status codes cannot be filtered. |
| 10 | Complete/incomplete | M | none | Records cannot be filtered by state. |
| 11 | UserId | M | none | Filter missing. |
| 12 | UI version | M | none | Filter missing. |
| 13 | Observed API Time | M | Metric `total` exists, but there is no range filter | Filter missing. |
| 14 | Audit ID | M | none | Filter missing. |
| 15 | Slow threshold/percentile | P | `TrafficSlowness.svelte` | Applied client-side only. |
| 16 | Request/response size | M | Sizes are projected (`requests.go:310-313`) but not filterable | Filter missing. |
| 17 | UserAgent | M | none | Filter missing. |
| 18 | UserIp | M | none | Filter missing. |
| 19 | URL search | M | URL is projected only | Filter missing. |
| 20 | AuditCreateTime | M | none | Filter missing. |
| 21 | ApiTime | M | Metric `api` exists, but there is no range filter | Filter missing. |
| 22 | Incomplete age | M | none | Filter missing. Derived from `now − RequestInitiatedAt` (or `CreatedOn`); no schema field is needed. |
| 23 | Comparison/baseline period | C | `BaselineStart/End` | No defect. |

## Totals

| | Complete | Partial | Missing | Incorrect |
|---|---|---|---|---|
| Areas (31 rows) | 3 | 16 | 9 | 3 |
| Filters (23 rows) | 3 | 6 | 13 | 1 |
| **All (54)** | **6** | **22** | **22** | **4** |

## Rule violations (ranked by severity)

1. **Rule 5: an incomplete record is treated as fast and successful.**
   - `Asynchronous` + `Accepted` is pending, but it is counted as `valid` (`pipeline.go:105-111`) and as `success` (`pipeline.go:159` includes `ACCEPTED`). The legacy path mirrors this at `legacy_pipeline.go:533-543`, and the records path also gets `success` from the same statusNames (`requests.go`).
   - Effect: the submit duration enters P50 to P99 as a short latency, which drags async percentiles down and inflates success rates. This also contaminates rule 4.
   - Needs live verification: count the `Asynchronous ` documents whose status is `Accepted`.
2. **Rule 6: 4xx is merged with 5xx.**
   - The single `failure` outcome is defined at `pipeline.go:160`. The rollups use it (`load_correlation.go:31-71`) and so does the UI outcome selector (QueryControls:54).
   - Unknown .NET names fall to `unknown`, so the totals are understated.
3. **Rule 7: aggregates are not drillable.**
   - Incomplete and `unknown` cohorts appear under a kind or outcome filter (`pipeline.go:132,135`). Drilling into one is then rejected by `validateCohortQuery` (`requests.go:344-355`) with the message "API key does not match query".
   - The quality counts, rollups, volume bars, timeline points and load windows have no record drill-down.
   - MCP `audit_get_requests` returns no record ids (`internal/mcp/schema.go:56`), so MCP findings cannot cite records.
4. **Rule 1: kinds are not separated.**
   - Rollups merge kinds and sources (`load_correlation.go`). Quality counts leak other kinds (`analyze.go:149-172`).
   - Incomplete HTTP rows are labelled `unknown` and cannot be selected.
   - The writer limitation means sync and async cannot be split for incomplete rows; the UI must say so instead of hiding it.
5. **Rule 3: Observed API Time is computed as `RequestCompletedAt − RequestInitiatedAt`** (`pipeline.go:79-82`).
   - This equals `AuditCreateTime + ApiTime` whenever both phases exist, so it is correct for modern rows.
   - For legacy rows without `RequestInitiatedAt`, `legacy_pipeline.go:510-512` falls back to `CreatedOn`. The result is then `ApiTime` alone, still labelled "Total request" with nothing to say AuditCreateTime is missing.
6. **Rule 4: latency should come only from completed records.** The state gate is correct (`analyze.go:208-210`), but it is breached by the rule 5 root cause.
7. **Rule 2: the operation is ControllerName + ActionName.** It is used as the key, but there is no single operation row per kind; rows are split by source and outcome.
8. **Rule 8: never invent fields.** No violation found.
