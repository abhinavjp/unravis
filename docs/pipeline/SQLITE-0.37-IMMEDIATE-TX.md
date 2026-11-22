# go-sqlite3 0.37: plain transactions now begin IMMEDIATE (we are on 0.38)

Found 2026-11-22 while looking into why `TestBucketCountsReadOneSnapshot` failed on
`github.com/ncruces/go-sqlite3` 0.37.0 (it was held at 0.35.6 and ignored in
`.github/dependabot.yml`).

## What changed upstream

`driver/driver.go`, 0.35.6 to 0.37.0:

- 0.35.6: the default `_txlock` is `deferred`. `db.BeginTx(ctx, nil)` runs `BEGIN`.
- 0.37.0: the default is `immediate` for any connection that is not `query_only`
  (`if c.txLock == "" && !c.qryOnly { c.txLock = "immediate" }`). `BeginTx(ctx, nil)`
  runs `BEGIN IMMEDIATE`, which takes the WAL write lock at once.
- `BeginTx(ctx, &sql.TxOptions{ReadOnly: true})` still runs a plain `BEGIN`
  (followed by `PRAGMA query_only=on`), in both versions.

## Why our test failed

`Case.BucketCounts` opened its one read snapshot with `BeginTx(ctx, nil)`. On 0.37 that
read transaction held the single write lock for the whole call. The test's writer
(`DELETE FROM events` between chunks, on another pooled connection) then waited out the
10 s `busy_timeout`; the call itself hit its 10 s scan budget meanwhile and came back
partial (`total 5, want 120`, every run 10.0 s; some runs also logged
`concurrent delete: database is locked`). It was our code asking
for a write transaction to do a read; 0.35.6 hid it because `BEGIN` is deferred.
It was a real defect on 0.37, not a test artefact: every chart read would have blocked
imports and every other writer for as long as it ran.

## 0.37 versus 0.38, and the engine

- 0.37 runs a ReadOnly rollback as `ROLLBACK; PRAGMA query_only=off` in one call. When
  the ROLLBACK itself fails (SQLite already rolled back after an I/O error), the reset
  is skipped and the pooled connection stays `query_only`, so later writes on it fail
  with "attempt to write a readonly database". 0.38 resets `query_only` first and adds
  `driver.Validator`. We are on 0.38.0 for that reason;
  `TestReadOnlyTxAutoRollbackLeavesConnectionWritable` fails on 0.37 and passes on 0.38.
- The `go-sqlite3-wasm` bump also moves the embedded SQLite engine, which can change
  query plans. The casestore and server suites show the same failing set as on 0.35.6.

## Fix

- `internal/casestore/sqlite/case.go` `BucketCounts`: `BeginTx(qctx, &sql.TxOptions{ReadOnly: true})`.
- `internal/casestore/casefile.go` `HoldCaseDB` (holds a read transaction while a case
  file is copied): same option. On 0.37 it would have held the write lock for the whole copy.

Both are correct on 0.35.6 too. The other `BeginTx(ctx, nil)` calls all write (schema,
tags, retries, plan, blobs, cookie policy, index build), so `IMMEDIATE` is right for
them: it takes the lock up front instead of failing at the first write when a reader
has moved on (`SQLITE_BUSY_SNAPSHOT`).

Rule for new code: a transaction that only reads must say `ReadOnly: true`.
