# Fetchability audit: charts and stats in the tracing-paper wireframe

Date: 2026-11-02. Scope: items A1-A11 and B1-B12 of `prototype/CHARTS-AND-STATS-REVIEW.md`, checked against the real app code (Go in `internal/`, Svelte in `web/`). Read-only: nothing was edited except this file, and `sql/` was not opened.

Rule applied: a stat is FETCHABLE only if the shipped product can produce it from real data today, with nothing invented. "Unverified" means I did not confirm it in code or did not read the whole file.

Status words:
- FETCHABLE NOW: real code produces it from real sources today.
- NEEDS IMPORTER/CHANGE: the data source or the calculation exists in part; the named change is needed.
- NOT FETCHABLE: no shipped path produces it.

## 0. What the audit found about the real app (read this first)

The review compares the wireframe with "the real app", but the real app is organised differently from the wireframe's Part B.

1. Part A (Pulse) is real and richer than the wireframe. Every A item has a code path.
2. Part B ("Investigation Timeline", three lanes with seven cards) has no one-to-one real screen. The real equivalents are:
   - the guided investigation: `POST /api/cases/{id}/investigation` (`internal/server/investigation.go`) running the pure engine in `internal/investigate`, shown by `web/src/lib/components/guided/*`;
   - the thin cross-source list in `internal/timeline/timeline.go` (audit intervals, overlap rows, absence rows; needs at least two sources with validated time zone rules, `timeline.go:1-10`, `:363-377`);
   - the resource panel: `/api/cases/{id}/resource` and `/series` (`web/src/lib/resource.ts`, `web/src/components/resource/HostLines.svelte`);
   - the live SQL/Mongo lanes: `internal/dbhealth` and `internal/server/lanes.go`.
3. The real engine's per-minute audit rows are exact (`investigate/facts.go:230` `Minute` has N, P50, P95, A, C, O), not "weighted mean of minute P95s" as in the prototype.

## 1. Table of all items

Source file:line is where the real calculation or data lives. "Real vs prototype" says whether the prototype's number is real code or simulated.

| Id | Item | Real source (file:line) | Status | Real vs prototype |
|---|---|---|---|---|
| A1 | Requests | `internal/audit/overview.go` (OverviewRow count), `web/src/lib/pulse.ts:41` `pulseFigures` | FETCHABLE NOW | Real. Count of audit documents in the window. |
| A2 | Completion | `internal/store/pipeline.go:68` `auditStateExpr` (valid / incomplete / invalid), `pulse.ts:41` | FETCHABLE NOW | Real. Needs the audit fields RequestInitiatedAt/ApiTime present; missing ones count as incomplete/invalid. |
| A3 | Observed P95 | `internal/audit/histogram.go` (192 bins, ratio 1.1, about +/-5%), `store/pipeline.go:36,89` `auditDurationExpr`/`auditBinExpr`, `pulse.ts:41` | FETCHABLE NOW | Real, but different from the prototype: histogram estimate, shown only when one request kind is selected. Review's "fix" is partly already how the app works. |
| A4 | 4xx | `StatusCode` in `store/pipeline.go`; delta via `clientErrorDelta` in `WhereList.svelte` | FETCHABLE NOW | Real. Review says 4xx has no delta; the real app has one in the ranking list. |
| A5 | 5xx | `StatusCode` in `store/pipeline.go`; `pulse.ts:41` | FETCHABLE NOW | Real. |
| A6 | Headline sentence | `web/src/lib/copy/screenCopy.ts:21-45` (`SEVERE_RATIO=2`, `SAME_BAND=0.05`, `p95Change`, `p95Sentence`) | FETCHABLE NOW | Real. Condition and wording use the same ratio, so the prototype's value/wording mismatch does not exist in the app. Severe line is 2x, not 1.5x. |
| A7 | Hero chart | `Analysis.timeline` points from `/api/analyze`; `web/src/components/RequestVolume.svelte`, `Charts.svelte` | FETCHABLE NOW | Real. The "fabricated max" is prototype only. |
| A8 | Where is it? (4 lenses) | `pulse.ts:89` `lensRead`, `:103` `rankOperations`, `:114` `sparkSeries`; `internal/audit/significance.go:78` `applyImpact` | FETCHABLE NOW | Real, different lenses: Requests / Completion / P95 / 4xx / 5xx. Impact is `(current mean - reference mean) x current count` (mean, not P95; `models.go:260`). |
| A9 | Evidence tabs (Slowest, History, Failures) | `/api/slowest`, `/api/requests`, `/api/activity` (`internal/server/routes.go`); `EvidenceView.svelte` | FETCHABLE NOW | Real. Unverified: I did not read every tab component. |
| A10 | Record timing split | `store/legacy_pipeline.go:32,42,550,625` (ApiTime, AuditCreateTime, PayloadRecordTime -> ApiTimeMs, AuditCreateTimeMs, PayloadRecordTimeMs); `RecordDetail.svelte` | FETCHABLE NOW | Review calls it "Fabricated"; that is true of the prototype only. The real record detail reads real fields. Older documents that lack a field show it as missing (nil), never zero. |
| A11 | Nearby activity | `internal/audit/activity.go:11,37` (`ActivityInput`, max 24 h window; `ActivityOverlaps`), `internal/calltrace/lanes.go:42,74`, `ActivityContext.svelte`, `CallTrace.svelte` | FETCHABLE NOW | Real but a different design: overlapping audited calls plus per-call lanes (sql, mongo, redis, iis, other) from the audit document's own parts. Review says CallTrace is not mounted; it is, through ActivityContext. The prototype's synthetic lane is not what the app shows. |
| B1 | Overall figures: 7 cards | see sub-table 2 | NEEDS IMPORTER/CHANGE | 3 audit cards real; IIS cards need small changes; 2 SQL cards depend on data the importers do not carry. |
| B2 | Audit lane panel | `investigate/facts.go:230` per-minute N/P50/P95/A/C/O (exact); `/api/analyze` timeline | FETCHABLE NOW | Real, and better than the prototype (exact minute P95, not a weighted mean). Per-minute rows are returned only with `includeSeries` (`server/investigation.go:162`). |
| B3 | IIS lane panel | `internal/importer/iis/w3c.go:175` (kind `request`, time-taken as duration, attrs incl. status, stem, bytes); `iis/httperr.go:144` (kind `httperr`, attrs reason, status, stem, queue); `casestore/series.go:236` `Series` | NEEDS IMPORTER/CHANGE | Importers exist. `Series` returns only min/mean/max/count per bucket (`series.go:80-86`), so there is no IIS P95 and no status-class stack (selector matches one exact attribute value, `series.go:50-58`). Smallest change: add per-bucket quantile and a status-class group-by to `Series`. The HTTPERR strip is fetchable via `/events` as is. |
| B4 | SQL lane panel | `importer/sqlserver/xe.go:45-54` kinds wait, diagnostics, error, non-yielding, deadlock; `querystore.go:64` kind `query-interval`; `errorlog.go:27-30` | NOT FETCHABLE (as designed) | No importer carries per-call durations or blocked-process reports (confirmed: `xeKinds` allowlist has no completed-call or blocked_process_report event; no `blocked_process` string anywhere in `internal/`). What exists: Query Store interval avg/max duration and execution counts, XE wait/error/deadlock/non-yielding rows, error-log io-stall/start/stop/login-failed rows. See section 3. |
| B5 | Time axis, buckets, gaps, clipped slots | `casestore/series.go:30-34` (max 2000 buckets, Partial flag, scan budget); `importer/importer.go` Report First/Last/KnownGaps; `investigate/facts.go` Gaps; `hostlane.go` `HostGaps` | NEEDS IMPORTER/CHANGE | Bucketing exists. Hatching of "no data" needs a small UI/server step that turns each source's import First/Last/KnownGaps into gap spans for the shared axis. Audit gaps exist only inside the guided engine (rule A35). The prototype's 120-bar ladder and 50,000-row edge scan are not in the app. |
| B6 | Lane state stickers | `timeline/timeline.go:161,312,322` (`SourceStatus`, `validatedRule`), `investigate/readiness.go` (fixed words Ready / Partly ready / Cannot test yet / Failed, `types.go`) | NEEDS IMPORTER/CHANGE | The inputs exist (coverage, tz rule validated). The wireframe's six words (events, no data, none seen, out of range, clock unchecked, incomplete) are not produced as such; a small mapping is needed. The real app uses its own readiness words. |
| B7 | Bucket panel | needs B3/B4 buckets; `/series` | NEEDS IMPORTER/CHANGE | Audit and IIS counts per bucket are fetchable; the SQL count is not (B4). "Open Run analysis for this bucket" is UI-only. |
| B8 | Coverage card | audit: `GET /api/sources/{id}/coverage` (`server/coverage.go:11-50`, `audit/coverage.go:63,87`); imports: casestore ImportRecord First/Last, `evidence/types.go` SourceQuality Coverage/Threshold | NEEDS IMPORTER/CHANGE | Audit coverage is a manual census that is refused with 409 `broad_scan_refused` on a live source unless `--allow-broad-scans` (`coverage.go:11,41-46`). So the wireframe's always-on audit ribbon (the 02:00-04:00 outage) is not available on a live source. Import coverage is real. "MVP-2 coverage ledger" is not a separate store; `/api/ledger` is the theories ledger, unrelated. |
| B9 | What Unravis sees | Application: audit (A6 logic). Web server: IIS rows (B3). Database: B4 | NEEDS IMPORTER/CHANGE | Application row real. Web server row needs the IIS 5xx share and ratio (B3 change). Database row cannot be fed until B4. The 2% and 25% limits are prototype-only; the real engine has its own headline (`investigate/engine.go` `Headline`) and Step 10 origin (`origin.go`). |
| B10 | Where does the evidence point? | see sub-table 3 | NEEDS IMPORTER/CHANGE | Mixed. Some pieces real (CPU, Query Store per-database, Mongo queue), two not fetchable (top wait, per-database file I/O share). The verdict function itself has no real counterpart; the app's equivalent is `investigate/origin.go` (which tier moved first) and `evaluate.go`. |
| B11 | Suspects ranking, onset, signals | `internal/investigate/catalog.go`, `signals.go`, `evaluate.go`, `hostlane.go`, `origin.go`; `server/lanes.go:439-505` `applyLaneRuns` | NEEDS IMPORTER/CHANGE | Real engine exists with Must/Expect signals and Yes/No/Unknown (`types.go`). See sub-table 4 for which suspects are fed. Onset: first minute where P95 stays above 2x the reference for 5 minutes with at least 5 samples (`thresholds.go:158-160`), not 20 samples as in the prototype. |
| B12 | Databases block | `dbhealth/lane_sql.go:26-27` (Query Store state, desired state, interval length, first interval), `dbhealth/lane_mongo.go:42` (profiler) | FETCHABLE NOW | Real, read live from the server rather than from case configuration. Marginal as a stat, as the review says. |

### 2. B1: the seven cards

| Card | Status | Source and note |
|---|---|---|
| Requests to the application | FETCHABLE NOW | Audit count (A1). |
| Slow-request time, P95 (API) | FETCHABLE NOW | Audit histogram P95 (A3). Real figure is not "estimated from per-minute figures". |
| Application 5xx | FETCHABLE NOW | Audit StatusCode (A5). |
| Web server errors, IIS 5xx | NEEDS CHANGE | IIS `request` events carry `status` (`w3c.go`), but `Series` cannot group by status class. Alternative today: `/events` plus client-side count (capped by `casestore.MaxEventQuery`). |
| Slow web requests, IIS P95 | NEEDS CHANGE | IIS time-taken is stored as event duration; no quantile in `Series`. The guided engine reads IIS rows (`investigation.go:372-423`) but only to derive the outside-time part O. |
| Slow database calls over 1 s | NOT FETCHABLE | No per-call SQL duration anywhere (see B4). Query Store gives interval avg/max per query, not call counts above a limit. |
| Database errors and blocking | PARTLY | Errors: XE `error_reported` rows (kind `error`) are importable. Deadlock count: XE `xml_deadlock_report` rows are kind `deadlock` (`xe.go:53`), but only the event name, not the graph; and a header containing "report" or "xml" quarantines the file (`xe.go:63-67`), so the export must omit that column. Blocking: NOT FETCHABLE (no blocked-process report source). |

### 3. B10: attribution card, piece by piece

| Piece | Status | Source and note |
|---|---|---|
| Server CPU | FETCHABLE NOW | Blackbox sample CPU (`internal/blackbox/sample.go:31`), live sampler, or PDH counter `HostCPU` (`resource/metrics.go:155-157`). |
| Disk latency (dm_io_virtual_file_stats) | NOT FETCHABLE as designed | No query on `dm_io_virtual_file_stats` exists in `internal/`. Substitute: PDH `Avg. Disk sec/Transfer` -> `HostDiskSecPerTx` (`metrics.go:174`) from an imported counter file; the Blackbox records only disk queue (`sample.go:34`). Different measure (host disk, not per SQL file). |
| Memory grants waiting | NEEDS CHANGE | No `dm_exec_query_memory_grants` query. The `sql-pdh` importer (`sqlserver/pdh.go`) keeps SQL counters, but `resource/metrics.go` has no rule for "Memory Grants Pending". Small change: add the rule. |
| Top wait | NOT FETCHABLE | Only a THREADPOOL total is read live (`dbhealth/lane_sql.go:21`). XE `wait_info` rows (`xe.go`) are long waits from system_health, not a per-window top-wait delta. |
| By-database P95 (incident / comparison) | FETCHABLE NOW (needs Query Store on) | `importer/sqlserver/querystore.go` and the live Query Store check (`lane_sql.go:141`). Interval-grained, as the review says. |
| Share of file I/O per database | NOT FETCHABLE | No per-database file stall source. |
| Mongo queue | FETCHABLE NOW (imported) | `resource/metrics.go` `MongoQueue` from serverStatus or mongostat. Replication lag is not applicable: M3/M4 are listed "not in production" (`catalog.go`). |
| Mongo slow operations per hour | FETCHABLE NOW (live) | Profiler check feeds M6 (`dbhealth/lane_mongo.go:42,238`); the count per hour is not exposed as a series (unverified). |
| Verdict (five conclusions) | No real counterpart | Closest: `investigate/origin.go` (Origin, Victim, Amplifier by tier onset). |

### 4. B11: the 25 prototype suspects against the real catalog

The real catalog has 44 entries (`catalog.go` `Catalog()` plus `NotApplicable()`). I found no entries for S8, S9, S10, S11, S12, D16, D17, M11 or M12 in the real catalog (grep on `internal/investigate`, no match), so these nine exist only in the prototype.

| Suspect | Real entry and signal source | Status |
|---|---|---|
| Q1 clock check | In catalog (`catalog.go:58`); fed by `SigClockAnomaly` (`signals.go:206`) and tz rules. The "pinned but missing" gap in the prototype is not a real-app gap. | FETCHABLE NOW |
| Q2 audit coverage | `SigAuditDip` from IIS share of audit (`signals.go:228`) | FETCHABLE NOW (needs IIS rows) |
| L1, L2 | audit-derived signals | FETCHABLE NOW |
| L3 retry storm (not in prototype list) | audit-derived | FETCHABLE NOW |
| C1 change | typed change list, `computeTimeline` (`signals.go:294`) | FETCHABLE NOW (user types it) |
| S1 HTTP.sys queue | Real S1 is read from audit + IIS (`readiness.go:352-376`). HTTPERR events are imported (`httperr.go`) but no signal reads them. | NEEDS CHANGE: add a signal over `httperr` rows. |
| S2 .NET thread pool | Read from audit + IIS; `NetTPQueue` exists in `resource/metrics.go` but is not fed to the engine. | NEEDS CHANGE |
| S5 audit-write coupling | `SigALarge` + scope; Mongo profiler for a direct read | FETCHABLE NOW (indirect) |
| S8, S9, S10, S11, S12 | Not in real catalog | NOT FETCHABLE (defer; S10-S12 need HTTPERR signals first) |
| D1 blocking | In catalog; no blocked-process source; live lane does not feed it (`lane_sql.go:139-142` feeds D3, D5, D11, D12, D13, D14, D15 only) | NOT FETCHABLE |
| D2 deadlocks | In catalog; XE kind `deadlock` imported but the engine does not read imported SQL events (`investigation.go` reads only IIS `request` rows) | NEEDS CHANGE |
| D6, D9, D10 | In catalog; no source fed | NEEDS CHANGE (PDH rules / counters) |
| D16, D17 | Not in real catalog | NOT FETCHABLE (defer); XE `error` and `login-failed` rows are importable |
| M5, M10 | In catalog; Mongo serverStatus metrics exist in `resource` but are not fed to the engine | NEEDS CHANGE |
| M11, M12 | Not in real catalog | NOT FETCHABLE (defer) |
| H1 host CPU | Fed by the Blackbox lane (`server/blackbox.go:1204` `applyBlackboxLane`, `investigate/hostlane.go`; rules: busy at 85%, rise 25 points, flat under 60%, onset 3 minutes, coverage at least 50% of incident minutes) | FETCHABLE NOW |
| H6 shared effect | Catalog entry; no source | NOT FETCHABLE |

Fed by the live SQL/Mongo lane (`applyLaneRuns`, `server/lanes.go:439`): D3, D5, D11, D12, D13, D14, D15, M6. These real lane answers do not appear in the review at all.

Reference-window gate: the review's 80% coverage and 30 events (B0) is not in the app. Real gates are `MinSampleCount = 20` (`internal/audit/thresholds.go:10`, `overview.go:12`), the gap rule A35 (`GapRefMinPerMinute = 5`, `thresholds.go:170`) and, for lanes, `laneCoverShare = 0.8` (`lanes.go:~485`).

## 2. Lists

### Fetchable now (13)

A1, A2, A3, A4, A5, A6, A7, A8, A9, A10, A11, B2, B12.

### Needs work, smallest change each (9)

- B1: add status-class and quantile to `Series` (`casestore/series.go:236`) for the two IIS cards; leave the two SQL cards out until B4.
- B3: same `Series` change (quantile, status-class group-by). The HTTPERR strip can use `/events` unchanged.
- B5: derive gap spans per source from ImportRecord First/Last and KnownGaps; share one axis.
- B6: map existing coverage and tz-rule state to the six wireframe words, or adopt the real readiness words.
- B7: needs B3 only (audit and IIS counts); SQL count waits for B4.
- B8: decide the audit coverage policy (manual census or allow broad scans); import coverage already exists.
- B9: Application row now; Web server after B3; Database after B4.
- B10: map PDH "Memory Grants Pending" into `resource/metrics.go`; keep disk latency as host PDH; keep per-database P95 from Query Store; drop top wait and file-I/O share (below); replace the verdict function with the engine's origin and suspect output.
- B11: feed imported SQL deadlock rows and Mongo/PDH metrics into `investigate.Input.Lanes`; add a signal over `httperr` rows for S1.

### Not fetchable, recommend drop or defer

- B4 SQL lane panel as designed (per-call slow/error bars, P95 call duration, blocked-process strip): defer. Substitute with Query Store interval lines plus XE wait/error/deadlock strips, or drop the panel until a completed-call importer exists.
- B1 card "Slow database calls over 1 s": defer (same cause).
- B1 blocking mark and suspect D1: defer (needs a blocked-process source and an unscrubbed-XML decision).
- B10 "Top wait" and "Share of file I/O per database": drop (no query exists; adding one means new live queries against `dm_os_wait_stats` and `dm_io_virtual_file_stats`).
- Prototype-only suspects S8, S9, S10, S11, S12, D16, D17, M11, M12: defer; they are not in the real catalog.

## 3. Recommended build order (SQL data, then HTTPERR, then Blackbox sampler)

1. SQL data first.
   - a. Query Store: already imported and read live; surface per-database P95 and the history chip (B10b, B12). No new importer.
   - b. XE: add a completed-call event (for example rpc_completed / sql_batch_completed) to `xeKinds` with a duration, plus blocked_process_report as a count-only kind. Decide how to keep the scrub rule (`xe.go:63-67` quarantines "report", "xml", "text") while still carrying a count. This unblocks B1 DB cards, B4, B9 Database row, D1, D2.
   - c. Feed imported SQL rows into the engine (today only IIS `request` rows and the live lane are read).
2. HTTPERR second.
   - The importer exists (`iis/httperr.go:144`) and the timeline names it (`timeline.go:82`). Work is downstream: draw the strip in B3 from `/events`, add a signal for S1, then add S10-S12 only if the owner wants them in the catalog. Add the `Series` quantile/status-class change here, since B3 and B1 (IIS cards) need it.
3. Blackbox sampler third.
   - The recorder and a Windows live sampler already exist (`internal/blackbox/sample.go`, `internal/sampler/sampler.go`), feed H1 only (`hostlane.go:` "H1 only"), and record CPU, memory, disk queue, network and processes. Next steps are: add disk latency and the SQL counters (memory grants pending, top wait) to what is recorded or mapped; extend `hostlane.go` to H2-H6 and D6, D9, D10; then build the attribution card's server table from real samples.

## 4. Contradictions with the review, and unverified points

Contradictions found (the review is right about the prototype, wrong about the app):
1. Review says the Blackbox sampler is not built and its drawer is a stub. The recorder, the live sampler and H1 are built (`internal/blackbox`, `internal/sampler`, `server/blackbox.go`).
2. Review says CallTrace is not mounted. It is mounted through ActivityContext.
3. Review says there is no 4xx delta. There is one (`WhereList.svelte`, `clientErrorDelta`).
4. Review says the headline has a condition/wording mismatch and a 1.5x severe line. The app uses one 2x ratio for both (`screenCopy.ts:21-45`).
5. Review's A3 fix ("compute from every request"): the app already uses a log histogram (about +/-5%), single kind only.
6. Review says the real catalog is a superset (44 entries) of the 25 prototype suspects. Nine of the 25 (S8-S12, D16, D17, M11, M12) are not in it.
7. The review does not mention the live SQL/Mongo lanes (`internal/dbhealth`), which feed eight catalog entries.
8. Onset sample minimum is 5 in the app (`thresholds.go:160`); the review already notes this.

Unverified:
- Whether `web/` renders `Facts.Series` as lane charts (the types are in `web/src/lib/guided/types.ts` and `StepFacts.svelte`; I did not trace the rendering).
- That a typical XE export keeps the `xml_deadlock_report` rows after the scrub rule.
- Whether IIS W3C attribute names (`status`, `stem`) match exactly what `/series` selectors accept; I read the importer, not the selector tests.
- A9 evidence tab components were not all read; A9 is trusted from the review and from the route table.
- The `laneCoverShare` line number (`lanes.go`, approximately 485) was read, not re-grepped.
- Mongo slow-operations-per-hour as a time series: unverified.
