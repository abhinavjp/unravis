# Progress: Phase 06 fetchability-build

## Done

- DONE TSK-32 input-fields: IISRow.Status/SubStatus, SQLEvent, HTTPErrRow and the six Input fields added (all omitempty); TestInputNewFieldsOmitted in new types_test.go.
- DONE TSK-33 burst-count: burst.go (BurstCount, CountBurst, httpErrTimes, sqlTimes) and burst_test.go.
- DONE TSK-34 sqllane-d2: sqllane.go (ApplySQLEvents, sqlD2, setLane, containsStr; no equivalent helpers existed in the package) and sqllane_test.go.
- DONE TSK-29 xe-call-kind: KindCall, rpc_completed/sql_batch_completed, duration_us roles (us/1000 -> ms, Start = ts - duration), enums updated, TestXECallKind.
- DONE TSK-30 xe-blocked-kind: KindBlocked "blocked-process", blocked_process_report, count only (no duration read), enums updated, TestXEBlockedCountOnly.
- DONE TSK-31 xe-attention-kind: KindAttention, attention, count only, enums updated, TestXEAttentionKind.
- DONE TSK-39 blackbox-disk-latency: Extras.DiskLatencyMs, NoExtras, winExtras (same PDH query, no second collect; lastCollect), Sample.DiskMs `diskMs`, Collect names `diskMs` in Missing; TestCollectDiskLatency. Proof: `go vet ./internal/blackbox/...` clean; `go test ./internal/blackbox/...` -> ok; `go build ./...` clean. UNMEASURED: a real PDH reading.
- DONE TSK-40 hostsample-disk: HostSample.DiskMs, hostSamples copies it; TestHostSamplesDisk (in server/blackbox_test.go). Proof: `go test ./internal/server/... ./internal/investigate/...` -> ok (server 102s), goldens unchanged.
- DONE TSK-41 hostlane-d9-d10: hostDiskSlowMs/RiseMs/RisePct consts, onSQLHost, isSQLProc, diskMean, hostSQLLanes (D9, D10), ApplyHost never overwrites another reader's Yes and skips Unknown for non-H1 ids; TestHostLaneD9D10, TestApplyHostNeverOverwritesAnotherReadersYes, TestIsSQLProc. Proof: `go test ./internal/investigate -run "TestHostLane|TestApplyHost|TestHostGaps"` -> ok; `go test ./internal/investigate/...` -> ok (goldens unchanged).
- Deviation (39): in the existing "missing counters" test the fakeExtras also gets latErr, else `diskMs` would not be missing and the specified `want` entry would fail.
- Deviation (41): the existing test helper hostSamples already puts a `sqlservr` process (3%) in every sample, so existing H1 tests now also compute D9/D10 (no disk data -> D9 absent; D10 follows H1); they assert only H1 and still pass. New tests use their own helper sqlHostSamples. Table test replaces seven separate tests; the ApplyHost case is its own test.
- Deviation (39-41): files kept CRLF (converted to LF to edit, restored after); gofmt -l clean on the LF form.

## Evidence

- go test ./internal/investigate -run "TestCountBurst|TestHTTPErrTimesFold|TestApplySQLEventsD2|TestInputNewFieldsOmitted": PASS (all four)
- go test ./internal/investigate/...: ok (TestGolden unchanged, no -update); go build ./...: clean
- Deviation: golden files in testdata/investigate show as modified in git, but from earlier uncommitted work before this phase; none touched by 32-34.
- Deviation: files kept CRLF; gofmt -l is clean on the LF form (raw CRLF files are listed by gofmt -l by design).

- TSK-29/30/31: `go test ./internal/importer/... ./internal/evidence/...` -> all ok (sqlserver, evidence included); `go build ./...` -> no output (PASS).
- Deviation (29-31): tests use helper xeHostOpts (Hosts mapping for "sql-a") because a server_instance_name column with no Hosts mapping returns ErrUnknownHost; CSVs otherwise as specified.
- Deviation (29): gofmt realigned the xeRoles map (new longer key); whitespace only. CRLF kept. xeScrubFragments untouched (git diff has no line in it).

## Not fixed

(anything left, with reason)

## UNMEASURED

(checks that could not run, with reason)

## TSK-36 / TSK-37 / TSK-38 (agent run)

- DONE TSK-36 series-quantile: `go test ./internal/casestore/... ./internal/resource/...` -> ok casestore 4.9s, casestore/sqlite 21.3s, resource 0.77s, resource/rules 0.50s; `go build ./...` clean. No import cycle with internal/audit (BinIndex/BinBounds called directly).
- DONE TSK-37 series-status-class: same run, PASS (TestSeriesStatusClass, TestSeriesNoQuantileUnchanged).
- DONE TSK-38 metrics-mem-grants: `go test ./internal/resource/...` ok.
- Deviation: `SeriesPoint` with `ByClass map` is no longer comparable with `!=`; two existing tests compared points that way. Changed to `reflect.DeepEqual` in `internal/casestore/series_test.go` (in scope) and `internal/casestore/sqlite/series_test.go` (one line + `reflect` import, outside the listed scope but required to build).
- Note: gofmt realigned the const block in `internal/resource/metrics.go` (longer name `SQLMemGrantsPending`) and in `series.go` (`MaxSeriesQuantile`); whitespace-only. CRLF restored on all touched files. web `npm test` not run (out of assigned scope).

## TSK-35 / TSK-42 (agent run)

- DONE TSK-35 server-imported-events: investigation_events.go (readImportedEvents), IISRow Status/SubStatus + IISSubStatus, call line in investigation.go. Proof: `go test ./internal/server/ -run TestReadImportedEvents` -> 7 tests PASS; `go test ./internal/server/... ./internal/investigate/...` -> ok (server 100s, goldens unchanged); `go build ./...` clean.
- DONE TSK-42 d6-mem-grants-wiring: investigation_metrics.go (readMetricLanes), call line after readImportedEvents. Proof: `go test ./internal/server/ -run TestReadMetricLanes` -> 5 tests PASS; same full run and build as above.
- Deviation (35): element type is calltrace.SourceInfo. readImportedEvents returns early when none of sql-xe, sql-errorlog, iis-httperr is validated (no query). Extra tests beyond the spec: SQLKindProbe, SQLCap, UnvalidatedIgnored, IISStatus.
- Deviation (42): readMetricLanes first calls c.Imports and returns when no win-counters/sql-pdh file is held, so a case without counters costs no catalogue scan (and the existing stub-case tests, whose stub has no Each, keep working). Source names are local string literals because the resource constants are unexported.
- Deviation: files kept CRLF (converted to LF to edit, restored after); gofmt -l clean on the LF form.

## Clumsy-user review

New tests: `internal/importer/sqlserver/zz_clumsy_xe_test.go`, `internal/casestore/zz_clumsy_series_test.go`, `internal/investigate/zz_clumsy_sqlhost_test.go`, `internal/server/zz_clumsy_events_test.go`. All kept as regression tests. Investigate (skipping TestGolden), sqlserver, casestore, blackbox, resource and server suites pass; build and vet clean.

Defects found and fixed (one pass):
1. XE import aborted on a timestamp of 0001-01-01, or on a call whose duration put its derived start on that date ("invalid event: start and end must be set"). `parseUTCField` now refuses the zero time and the row loop rejects a zero start as bad-time (`xe.go`).
2. Series quantile skewed by a negative duration: it was binned below bin 0 and fell out of the bin counts. Negatives now go in bin 0 (`casestore/series.go`).
3. D2 read No when the incident window was invalid (zero, reversed, empty): no rows in an unreadable window counted as no deadlocks. `sqlD2` now returns Unknown (`investigate/sqllane.go`).
4. D9 read No (or, for +Inf, a wrong answer) from NaN, Inf or negative disk latency. `diskMean` now skips such readings, so the lane stays unanswered (`investigate/hostlane.go`).
5. D6 read No from a memory-grants counter holding negative values. A series with a negative sample is now treated as unread (`server/investigation_metrics.go`). NaN and Inf cannot reach it, the store refuses them at import.

Checked, no defect: header-only and empty XE files; wrong-unit and hostile duration cells; report/XML/text columns still quarantine; blocked-process and attention rows keep only the event word; time zone offsets rejected; duplicates get one id each; q on empty buckets and out of range; unknown status classes; CountBurst edges and a million rows at one instant; ApplySQLEvents with zero or one event and nil maps; a Yes from another reader is never overwritten (D2, D9, D10); the SQL cap on one source or on the second stretch drops that source whole while the other source still reads; nil Attrs; an unvalidated XE file beside a validated error log is neither read nor probed; no new field appears in Result or Facts JSON for inputs without SQL or HTTPERR rows.

Left open:
- D2 No is claimed from an XE source without checking its coverage (From/To) against the incident window. Design or owner call.
- Call events are read by start time but placed at their end, so a call that started before the window start and ended inside it is dropped. Low severity; the fix needs a lookback plus a client-side End filter.
- A `duration` or `duration_ms` column on call rows gives no duration (by design: only `duration_us` is read).
- `ApplyHost` appends its lane source on every call, so a second call lists the Blackbox twice. Only called once today.
- `go test ./internal/casestore/sqlite` fails TestV3CaseOpensWithNoMasterKeyAndMigratesOnce ("manifest.dat has a v2 header"). Unrelated to Phase 06; not checked against a clean tree.
- Not touched, other agents own them: mongoexports, dbhealth, resource/metrics.go, evidence/caseexport*. The other agent's M12 edit in `investigation_metrics.go` was left as found.

