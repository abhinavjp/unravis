# WP-65 Confirm reads one snapshot or answers 409
risk: high    size: M    wave: 5    model: sonnet

## Outcome
A case export confirm builds its files from data that did not change while it was being read: it reads the case revision before and after the reads, and answers 409 "the preview changed; run the preview again" when the two differ or when the first differs from the one the preview id was made from (TR-21, R6-08). No bytes are written on a 409. A replay of the same confirm on an unchanged case, and parallel confirms, still succeed.

## Write scope
- `internal/server/case_views.go` (edit `caseExportConfirm` and `buildCaseExport`)
- `internal/server/export.go` (add the test seam variable only)
- `internal/server/case_export_snapshot_test.go` (new)

## Must not change
- `casePreviewID`, `resolveScrub`, `exportChangedMessage` and the preview path (WP-06a); `bumpCaseRevision`, `Revision` and every bump site (WP-65a, WP-65b). Call them, do not edit them.
- The findings export (no `caseId`): random id, 404 `exportGoneMessage`, `TestExport*` tests.
- The 409 body: fixed text from `exportChangedMessage`, no echo of input, no counts, no revision numbers (INV-3).
- INV-4: tests use the temp dirs of `newIntEnv`; never create `.unravis.db`.

## Changes
1. `export.go`: add `var caseExportSnapshotHook func(stage string)`, nil in production. It is called with `"after-reads"` inside `buildCaseExport` after the imports and events are read and before `BuildCase` runs. Tests set it and restore it with `t.Cleanup`. Production code never sets it.
2. `case_views.go` `buildCaseExport(w, r, c, scrub)` (signature from WP-07b): after the reads and after the hook call, read `caseRevisionOf(ctx, c)` as `after` and return it in `caseExportResult` as `revisionAfter int64`; a read error goes through `writeStoreError`. Add `revisionBefore int64` set from a read taken before the first `c.Imports` call. Neither field is serialized.
3. `case_views.go` `caseExportConfirm`: keep the WP-06a check of the preview id against the revision read at the top (that revision is `R0`). After `buildCaseExport`, if `res.revisionBefore != R0 || res.revisionAfter != R0`, answer `writeError(w, http.StatusConflict, exportChangedMessage)` and write nothing else. Only then compute the SHA-256 and call `writeConfirmed`. Order matters: no `export_finished` log line and no file bytes before this check. (`export_finished` is a log record written by `h.event(r, "export_finished", "kind", "evidence", "size", n)` in the shared tail `writeConfirmed`, not a case event; its fields are `msg=export_finished`, `req_id`, `kind=evidence` and `size=<canonical bytes + CSV bytes>`.)
4. Note: the spec says the confirm runs in one read transaction. `Case.Events` and `Case.Imports` open their own reads, so one transaction would mean a refactor of the store interface. The before/after revision bracket gives the same guarantee for any writer that bumps (all of WP-65b's sites), and the per-case gate already serializes imports through the HTTP handlers. The bracket exists for writers that do not take the gate (the source and environment handlers, a second process).
5. Note: a bumping write that lands between the first and last revision read always changes the number, because the bump is monotonic and is committed with the write. A write that bumps and commits before `revisionBefore` is read is part of the snapshot and the build sees it.
6. Tests in `case_export_snapshot_test.go` (`newIntEnv(t, intRepo())`, `e.newCase`, `e.commit(id, "iis-httperr", "web-a", intHTTPERR)`, helper `revisionOf` from WP-65b's `case_revision_test.go`). Canary fixtures: seed one `iis-w3c` event whose `site` is `CANARY_SITE_65` and one import file name `CANARY_FILE_65.log`. Log capture: `newIntEnv` builds its handler without a logger, so a test that reads logs calls `logBuf(e)`, a helper defined in this test file: it makes a `bytes.Buffer`, sets `e.h = New(intRepo(), testAssets(), testToken, false, WithCases(e.store, mustHosts(t, intHostsDoc), all.Registry()), WithLedger(e.ledger), WithLogger(bufLogger(&buf)))` (`bufLogger` is in `httplog_test.go`) and returns the buffer.
   - `TestCaseConfirmWriteDuringReadsIs409`: set the hook so that at `"after-reads"` it opens the case through `e.store.Open` and calls `SetEnvs` with a changed list; the confirm answers 409, the body has `exportChangedMessage` and no `content`, `sha256` or canary string, and the `logBuf` text has no `export_finished` line.
   - `TestCaseConfirmImportDuringReadsIs409`: same, but the hook calls `Import` on a second handle with a fresh file; 409; the revision rose by exactly 1.
   - `TestCaseConfirmReplayOnUnchangedCaseSucceeds`: preview once, confirm twice with the same id; both 200, each `sha256` equals the SHA-256 of its own `content`.
   - `TestCaseConfirmParallelSucceeds`: preview once, then 8 goroutines confirm the same id at once; every answer is 200 and none is 409 (run with `-race`).
   - `TestCaseConfirmAfterImportBeforeStartIs409`: preview, import a file normally, confirm; 409 (the WP-06a path, kept as a regression guard with this packet's two-read code).
   - `TestCaseConfirmHookIsNilInProduction`: assert `caseExportSnapshotHook == nil` at test start and that `rg` finds no assignment outside `_test.go` files (read `export.go` and `case_views.go` as text; neither contains `caseExportSnapshotHook =`).
   - `TestCaseConfirmLogsFinishedWithSizeOnly`: with `logBuf(e)`, preview and confirm once with `scrubSensitive` false on a case holding the canaries and the `iis-w3c` event; 200. The log has exactly one line with `msg=export_finished`; that line holds `kind=evidence` and `size=<n>` where `<n>` equals `len(content)+len(eventsCsv)` of the response, and no field other than `time`, `level`, `msg`, `req_id`, `kind` and `size`. The whole log text contains none of: the `previewId`, the response `sha256`, `client-ip`, `referer`, `cookie:`, `CANARY_`, `192.0.2.`. Then import a file and confirm again with the old id: 409, and the count of `msg=export_finished` lines is still 1.
   - `TestCaseConfirm409LeaksNothing`: the raw 409 body for each case above has no `CANARY_`, no `.log`, no digit run of 10 or more characters, and is valid JSON with exactly the key `error`.

## Proof
- `go test ./internal/server/ -run 'TestCaseConfirm' -race -count=1` -> `ok`, eight tests run.
- `go test ./internal/server/... ./internal/evidence/... ./internal/casestore/sqlite/... -count=1` -> `ok`.
- `go vet ./internal/server/` -> no output.
- `rg -n "caseExportSnapshotHook" internal/server --glob '!*_test.go'` -> only the declaration and the one call.

## Depends on
WP-65a, WP-65b, WP-06a, WP-07a, WP-07b
