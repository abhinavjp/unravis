# WP-65b Revision bumps at every mutation site
risk: high    size: M    wave: 3    model: sonnet

## Outcome
Every write that can change what a case export contains (an import commit, the environment list, a source tag added or removed, the cookie allowlist and its key as one setter call) bumps `case.revision` inside its own transaction, with a before/after test per site. Writes that never reach the export (workspace, ledger, plan, retries, health, discover) leave the revision alone. This is R6-08 and the write half of TR-21.

## Write scope
- `internal/casestore/sqlite/case.go` (edit `Import` only)
- `internal/casestore/sqlite/tags.go` (edit `SetEnvs`, `AddSource`, `RemoveSource`)
- `internal/casestore/sqlite/cookiepolicy.go` (created by WP-01b; edit `SetCookieAllowlist` only, one bump line)
- `internal/casestore/sqlite/revision_sites_test.go` (new)
- `internal/server/case_revision_test.go` (new)

## Must not change
- `bumpCaseRevision` and `Revision` from WP-65a (`revision.go`): call them, do not edit them.
- INV-4: tests use temp dirs only. Schema stays 4; no new table.
- Behaviour of the tag endpoints: status codes, messages, the 200-source cap (`casestore.MaxSources`, `ErrCapExceeded`), `countTags` counts. Existing `tags_test.go` and `case_sources_test.go` stay green.
- A refused import (duplicate, quarantine, rollback) must not bump: only a committed import does.

## Changes
Sites, enumerated by `rg -n "BeginTx|ExecContext" internal/casestore/sqlite` and `rg -n "SetEnvs|AddSource|RemoveSource|\.Import\(" internal/server`:
1. `Case.Import` in `case.go`: add one `bumpCaseRevision(ctx, tx)` call after the `indexSQL` exec and before `tx.Commit()` (about line 770). On error return `c.mapDBErr(err)` like the neighbouring calls. The duplicate path (`c.duplicate`), `c.quarantine` (separate `quarantine` table, never exported) and every rollback path stay unbumped.
2. `Case.SetEnvs` in `tags.go`: call `bumpCaseRevision(ctx, tx)` after the INSERT loop, before `tx.Commit()`; on error roll back and return `c.mapDBErr(err)`. It always bumps, even when the list is unchanged.
3. `Case.AddSource` in `tags.go`: it uses `db.ExecContext` with no transaction today. Change it to `tx, err := db.BeginTx(ctx, nil)`; keep the single INSERT...SELECT cap statement unchanged, run on `tx`; on `RowsAffected()==0` roll back and return `casestore.ErrCapExceeded`; take `LastInsertId` from that result; call `bumpCaseRevision(ctx, tx)`; commit; then call `c.countTags(ctx, db)` best effort as before. Any error rolls back and goes through `c.mapDBErr`.
4. `Case.RemoveSource` in `tags.go`: same change, a transaction around the DELETE; bump only when `RowsAffected()>0` (removing an id that does not exist stays a no-op that does not bump); then commit and `countTags`.
5. Cookie keys: this packet depends on WP-65a (the primitive) and WP-01b (which creates `internal/casestore/sqlite/cookiepolicy.go`). In `SetCookieAllowlist` of that file, WP-01b already writes both meta rows (`cookie.allowlist`, `cookie.hmac_key_v1`) in one transaction `tx` and leaves the comment `// WP-65b: bumpCaseRevision(ctx, tx) here, only when changed` as the last line of its `if changed {` branch. Replace that comment with `if err := bumpCaseRevision(ctx, tx); err != nil { _ = tx.Rollback(); return nil, c.mapDBErr(err) }`. Rule: one call to `SetCookieAllowlist` is exactly one revision bump (both rows in one transaction); a call that changes nothing (same allowlist, key already present) writes nothing and does not bump. Do not touch any other line of the file.
6. Not bumped, by design (each gets a negative test, step 8): `SetWorkspaceBlob`, `SetLedgerBlob`, `SaveView`, `DeleteView`, `PutChecklistItem`, `DeleteChecklistItem`, `ReplaceRetries`, `NoteRetry`, `RemoveRetry`, `AddHealthRun`, `PutDiscoverMark`, `DeleteDiscoverMark`, `RecordDiscoverRead`. Note: there is no import-delete or source-update method in the tree (`rg "DELETE FROM imports|DELETE FROM events|UPDATE case_sources"` finds nothing); the spec's "import deleted/source changed" sites do not exist. Step 8 adds a guard test so a future one cannot skip the bump.
7. Server callers need no edit; they reach the bump through the storage calls: `putEnvironments` (`SetEnvs`), `postSource` and `postMongoDatabase` (`AddSource`, `RemoveSource`), `deleteSourceTag` (`RemoveSource`), `importFileWith` (`Import`), `runRetry` in `case_lifecycle.go` (`SetEnvs` then `AddSource`). Note: the current export reads only imports and events, so tag changes cause a spurious 409 at worst; this is the spec's list and is kept.
8. Tests in `internal/casestore/sqlite/revision_sites_test.go` (helpers `newTestStore`, `mustCreate`, `mustOpen`, `mustImport`):
   - `TestRevisionImportBumpsOnce`: `Revision` 0 -> 1 after `mustImport`; importing the same bytes again (duplicate) leaves it at 1.
   - `TestRevisionRefusedImportDoesNotBump`: an import whose run function returns an error leaves the revision unchanged.
   - `TestRevisionSetEnvsBumps`: 0 -> 1 -> 2 across two `SetEnvs` calls.
   - `TestRevisionAddAndRemoveSourceBump`: `AddSource` 0 -> 1; `RemoveSource` of that id 1 -> 2; removing it again stays 2.
   - `TestRevisionAddSourceCapDoesNotBump`: fill to `casestore.MaxSources`, one more returns `ErrCapExceeded` and the revision equals the count of successful adds.
   - `TestRevisionSetEnvsRefusalDoesNotBump`: a `SetEnvs` call that fails validation (`casestore.CleanEnvs` error, for example 33 names) returns an error and the revision is unchanged.
   - `TestRevisionCookieKeysBump`: one `SetCookieAllowlist(ctx, []string{"sid"})` call raises the revision by exactly +1 (it writes the allowlist and creates the key); the identical call repeated leaves it at +0; a call with a different list (`[]string{"sid","theme"}`) raises it by +1 again; a call that fails validation (`[]string{"a b"}`) leaves it unchanged.
   - `TestRevisionNonExportWritesDoNotBump`: record the revision, call every function listed in step 6 once with the smallest valid argument (copy the arguments from `plan_test.go`, `tags_test.go` and the existing tests of each), assert the revision is unchanged after each call.
   - `TestRevisionEveryExportedWriteHasABump`: read the non-test `.go` files of this package and fail if any contains `DELETE FROM imports`, `DELETE FROM events`, `UPDATE case_sources` or `UPDATE case_envs`, with the message "add bumpCaseRevision to this write and to WP-65b's list".
9. Tests in `internal/server/case_revision_test.go` use the sqlite store only (`newIntEnv` from `int_test.go`; for Mongo, `mongoEnv(t, store, ...)` with `tagStores(t)["sqlite"]()`). Add a helper `revisionOf(t, e, id) int64`: `e.store.Open(ctx, casestore.CaseID(id))`, assert the case implements `interface{ Revision(context.Context) (int64, error) }`, read it, `Close`.
   - `TestRevisionHandlerImportCommit`: `e.commit(id, "iis-httperr", "web-a", intHTTPERR)` raises it by 1; an upload with `?preview=1` and a refused file leave it unchanged.
   - `TestRevisionHandlerEnvironments`: PUT `/api/cases/{id}/environments` raises it by 1.
   - `TestRevisionHandlerSourceAddAndDelete`: POST `/sources` +1, DELETE `/sources/{id}` +1, a second DELETE of the same id +0.
   - `TestRevisionHandlerMongoClassification`: `classify` for a tested database raises it (strictly greater); a 422 (untested database) leaves it unchanged.
   - `TestRevisionHandlerRetryRun`: only if an existing test already drives `POST .../retry` (`rg -n "runRetry|/retry" internal/server/*_test.go`); reuse that fixture and assert the revision rose. If none exists, skip this test and say so in the commit note.

## Proof
- `go test ./internal/casestore/sqlite/ -run Revision -count=1` -> `ok` (includes WP-65a tests and the nine above).
- `go test ./internal/server/ -run Revision -count=1` -> `ok`.
- `go test ./internal/casestore/... ./internal/server/... -count=1` -> `ok` (tag, import and concurrency tests unchanged).
- `rg -n "bumpCaseRevision" internal/casestore/sqlite --glob '!*_test.go'` -> hits in `revision.go`, `case.go` (Import), `tags.go` (SetEnvs, AddSource, RemoveSource) and `cookiepolicy.go` (SetCookieAllowlist).

## Depends on
WP-65a, WP-01b
