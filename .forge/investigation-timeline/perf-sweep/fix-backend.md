# Perf sweep: backend fixes (hand-off)

Branch feat/investigation-timeline. Nothing staged or committed. Gate at the end of this file.

## Window cap 186 days (owner ruling)
- internal/server/tracing.go: window cap 186 days, 400 text "The longest window is 186 days. Shorten the start or end." (tracingTooLongMsg).
- internal/server/tracing_axis.go: ladder checked; at most 120 buckets at 186 days (2-day buckets at the top; tests in tracing_axis_test.go cover 30d, 31d, 93d and 186d edges).
- Rollup and daily-series cap stay at 120 days (audit.MaxRollupDays, MaxSeriesDays). The 186-day audit lane builds its RollupPlan directly and degrades through the existing rowcap / timeout / broad-scan mapping.

## PF-B-01 budget and partial "timeout" lane
- casestore.BucketQuery.MaxTime; BucketResult{TimedOut, DoneTo}. SQLite BucketCounts reads the window in at most 24 chunks of whole axis cells, oldest first, under a time budget (default and ceiling casestore.MaxScanTime, 10 s). On budget: the counted chunks come back with Partial, TimedOut and DoneTo (cells before DoneTo are complete).
- casestore.EventQuery.MaxScanTime now also bounds Events (only when set): the events read so far come back with ErrScanBudget.
- Tracing: tracingLaneBudget (= MaxScanTime, a var for tests). Lane gets reasonCode "timeout", Truncated, Complete false; buckets from DoneTo on are Complete false. Each lane and each read has its own budget, so a slow lane never fails the others. A dropped request (ctx cancel) is still an error, not a timeout.
- Tests: internal/server/tracing_timeout_test.go, internal/casestore/sqlite/buckets_test.go (budget, DoneTo, request-context cancel stops BucketCounts and Events).

## PF-B-02 broad scan code
- Live audit lane over 24 h: lane code "broad-scan" (was "error"), same text. tracing_audit.go.

## PF-B-03 rollup cache
- internal/server/rollup.go: put now drops entries past the TTL, evicts oldest first past 8 entries or rollupCacheGroups (2 x audit.MaxRollupGroups = 1.2M groups held in total); a read over the cap by itself is not cached.
- Tests: TestRollupCachePutPrunesExpiredEntries (fake clock), TestRollupCacheCapsTotalGroups in rollup_test.go.

## PF-B-04 daily series partial
- audit.readDays stops starting new days at 80 percent of the ctx deadline (seriesSoftShare, clock seriesNow) and treats a day that fails on the deadline as the end; returns the oldest finished days with partial true. At least one day must finish. A cancelled ctx is still an error.
- New JSON fields on the endpoint daily answer (EndpointDaily): `partial` (bool, omitted when false) and `daysPlanned` (int, omitted when 0), plus a warning text in `warnings`. Step-change result sets its existing `partial` true and trims `end` to the last finished day.
- Tests: internal/audit/dailyseries_budget_test.go (stepping clock, deadline mid-day, cancel).

## PF-B-06 writers during an import (minimal, no chunked commit)
- casestore.ErrImportRunning. sqlite caseState.importRun is true while Import holds the case.
- Fail at once with ErrImportRunning: tagGate (workspace save, tags/environments, plan, ledger blob, cookie policy, health runs) and retryGate, and Store.CloseCase (peeks the open case with s.mu.TryLock, so it never waits behind Open).
- A second Import waits at most importWaitMax (1 s, var for tests), then ErrImportRunning; waits as before when importMu is held by an index build or shutdown.
- Server: writeStoreError, writeWorkspaceError (via it), writeImportError and the discovery-pull map send 409, reason `case-import-running`, text "An import is running on this case. Try again when it finishes." (caseImportRunningText). The archive route keeps its own `import-running` reason.
- Limit: a writer that races in the instant before an import takes the case still waits for the folder lock (up to 5 s) as before; other windows/processes holding the lock are not detected.
- Tests: internal/casestore/sqlite/import_running_test.go (TestWritersFailFastWhileAnImportRuns, TestSecondImportWaitsForAShortFirst), internal/server/import_running_test.go (TestWritersGetImportRunning409).

## PF-B-07 413 split
- casestore.ErrFileCapExceeded (file size, file rows) and ErrCaseCapExceeded (case size, case database size); both satisfy errors.Is(err, ErrCapExceeded). Free disk space and all-cases caps stay plain ErrCapExceeded.
- Server 413 reasons on import and discovery pull: `file-cap` ("The file is over the size or row cap for one file. Choose a shorter time window or a smaller file."), `case-cap` ("The case is over its size cap. Erase a closed case or start a new one, then try again."), generic `cap-exceeded` as before for the rest.
- WEB FOLLOW-UP: web/src/lib/copy/importCopy.ts maps `cap-exceeded` only; add `file-cap`, `case-cap` and `case-import-running` entries (the server texts above can be used). Not touched here.
- No MaxCaseEvents option was added (the finding's test idea); the split follows the caps that exist. Events-in-case count in the import dialog is a web task.
- Tests: TestCapKinds (sqlite), TestImportCapReasonsSplitFileAndCase and TestImportRunningAndCapErrorsMap (server).

## Not done
- 24M-row dataset (hub generator is separate). Big-set tests are not added; every new test uses small data (the largest is 400k cache groups in memory, under a second).
- Chunked import commit (PF-B-06 full fix).

## Gate (repo root)
- go vet ./internal/... ./cmd/... : clean on Windows and GOOS=linux.
- go test ./internal/server/... ./internal/casestore/... ./internal/audit/... : all ok (server 116 s).
