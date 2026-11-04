# Perf sweep: backend fixes round 2 (review r1 confirmed items)

Branch feat/investigation-timeline. Nothing staged or committed. F2 and C6 left as is (owner call).

## Changes per item

- C1 internal/casestore/sqlite/case.go: new `importPend` counter, raised before the first TryLock in `lockImport` and dropped on return. `lockImport` now publishes `importRun` itself right after it takes `importMu` (all three paths), so the holder is always either pending or in `importRun`. A loser of the TryLock race falls to the unbounded `Lock()` only when it is the sole pending Import and `importRun` is false (an index build or shutdown holds the mutex); otherwise it polls and answers `ErrImportRunning` after `importWaitMax`. `Import` no longer stores `importRun` true itself (its deferred reset stays). Side effect: two Imports pending while an index build holds the mutex both get the bounded 409 path (1 s then ErrImportRunning) instead of waiting; judged acceptable.
- D1 internal/server/rollup.go `put`: `break` at the first (newest-first) entry that does not fit, so eviction is oldest first and no smaller older entry is kept in place of a larger newer one.
- D3 internal/audit/rollup.go `Groups()`: returns the larger of the aggregated rows merged so far and the slots plus bins held. AddRow/AddBin reads (Go scan path, internal/store/rollup.go:280) now count against the 1.2M cache cap. The wire `Groups` field is unchanged.
- F1 internal/casestore/sqlite/case.go `mapDBErr`: SQLITE_FULL is the case-size cap unless the case volume has under 16 MiB free (`freeBytes`), then it is the plain `ErrCapExceeded` ("database full"). Tried a `PRAGMA page_count` vs `max_page_count` probe first: inside the failed import transaction SQLite had rolled back (count 12 of 64), so it could not tell the cap apart; abandoned.
- O1 stale comments: BucketCounts doc (chunked GROUP BY queries within the budget), tracing_audit.go ReasonCode list now has broad-scan.

## Tests
- G1: `fast` in casestore/sqlite TestWritersFailFastWhileAnImportRuns and the two HTTP calls in server TestWritersGetImportRunning409 run in a goroutine with a 10 s bound (fail instead of hang; import released in both). The held-import error is now asserted (`t.Errorf`) instead of logged.
- C1: new TestLockImportBoundsTheWaitInTheGap (importMu held and importPend raised, importRun false; lockImport must answer ErrImportRunning).
- G2: not added. Driving a held import through the HTTP upload endpoint is not cheap (upload helper builds the body in memory); that path is the C6 owner call.
- G4: new TestDiscoverPullFailErrMapsImportErrors (11 cases: the three new cap/running errors plus the older mappings).
- D1/D3: new TestRollupCacheEvictsOldestFirstNotBySize, TestRollupGroupsCountsRowsAndBins.
- G5/G6/G7: internal/audit/dailyseries_budget_test.go uses a stepped `seriesNow` clock (`steppedFake`): 1 s budget, 100 ms days gives exactly 8 days (a 99 percent threshold would give 10); mid-day deadline test freezes the clock and fails day index 3 on the real deadline, asserting exactly 3 days, partial, 4 reads.

## Gate
- `go vet ./internal/... ./cmd/...` clean; `GOOS=linux go vet` clean.
- `go test ./internal/audit ./internal/casestore/... ./internal/store ./internal/server -count=1`: all ok (server 133 s in the full run; after the last small edits the audit, casestore, store packages were re-run in full and the touched server tests re-run, all pass).
- gofmt -l on the LF files I touched is empty; CRLF files kept CRLF.
