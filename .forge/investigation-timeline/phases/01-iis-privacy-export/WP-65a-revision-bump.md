# WP-65a Case revision primitive
risk: medium    size: S    wave: 1    model: sonnet

## Outcome
Each case database can hold a monotonic integer `case.revision` in its `meta` table, with one function that bumps it inside a caller's transaction and one method that reads it (0 when never bumped). This is the storage half of the export snapshot rule in TR-21; the mutation sites are wired in WP-65b and the 409 lives in WP-65.

## Write scope
- `internal/casestore/sqlite/revision.go` (new)
- `internal/casestore/sqlite/revision_test.go` (new)

## Must not change
- `schemaVersion` stays 4 in `internal/casestore/sqlite/schema.go`; no new table, no migration, no change to `sqliteSchema`.
- `internal/casestore/store.go` (the `Case` interface) and `internal/casestore/memory.go`: no interface method is added. The server reads the revision through its own optional interface (WP-06).
- No existing sqlite file is edited in this packet. `case.go` and `tags.go` are WP-65b.
- INV-4: tests use `newTestStore` temp dirs only; never create `.unravis.db`.

## Changes
1. Create `internal/casestore/sqlite/revision.go`, package `sqlite`, with the constant `revisionKey = "case.revision"`. The row lives in the existing table `meta(k TEXT PRIMARY KEY, v TEXT NOT NULL)` and `v` is the decimal text of the number.
2. Add `func bumpCaseRevision(ctx context.Context, tx *sql.Tx) error`. It runs one upsert on `meta`: insert `case.revision` with value `1`; on a key conflict set `v` to the old value plus one, as decimal text. It must run on the caller's `tx` so the bump commits or rolls back with the caller's own writes. It never begins or commits a transaction. Wrap a database failure with `fmt.Errorf("case revision: %w", err)`.
3. Add `func (c *Case) Revision(ctx context.Context) (int64, error)`. It takes the handle with `c.handle()` (so a closed or erased case returns the same errors as every other reader), reads `v` for `case.revision`, and returns 0 with a nil error when there is no row. A `v` that does not parse as a non-negative int64 returns an error that wraps `ErrInvalid`.
4. Note: the spec names `internal/casestore/sqlite/case.go` for this primitive; a new file keeps the diff apart from WP-65b, which edits `case.go`.
5. Note: downgrade is harmless. An older build never reads `case.revision`, so the extra `meta` row is ignored, and its own writes simply do not bump it. A case reopened by this build then carries a stale number; the next bump moves it forward and the stale value only costs one spurious 409.
6. Tests in `revision_test.go` (use `newTestStore`, `mustCreate`, `mustOpen` from `helpers_test.go`):
   - `TestRevisionDefaultsToZero`: a new case reports revision 0 and `SELECT COUNT(*) FROM meta WHERE k='case.revision'` (via the `scalar` helper in `tags_test.go`) is 0.
   - `TestRevisionBumpIsMonotonic`: three committed transactions, each calling `bumpCaseRevision` once, give 1, 2, 3 from `Revision`; two bumps inside one transaction raise it by 2.
   - `TestRevisionBumpRollsBackWithItsTransaction`: bump then `Rollback`; `Revision` is unchanged.
   - `TestRevisionRowIsPlainMetaKey`: after one bump the `meta` row for `case.revision` has value `1` as text, and the `schema_version` row is still `4`.
   - `TestRevisionSurvivesReopen`: bump twice, `Close`, open the same case again with `s.Open`, `Revision` is 2.
   - `TestRevisionOnClosedHandleErrors`: after `c.Close()`, `Revision` returns a non-nil error (the same one `c.handle()` gives).

## Proof
- `go test ./internal/casestore/sqlite/ -run Revision -count=1` -> `ok`, six tests run.
- `go vet ./internal/casestore/sqlite/` -> no output.
- `go test ./internal/casestore/... -count=1` -> `ok` (existing store and differential tests unchanged).

## Depends on
none
