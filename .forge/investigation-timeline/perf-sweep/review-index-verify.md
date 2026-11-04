# Review: timeline indexes + quick_check cache (read-only)

1. **Medium, integrity.** verify_cache.go:90-105. On Windows, `os.Stat` fills the file ID lazily from the path, and `SameFile` loads that ID only when it is called. The stored stamp therefore re-reads the *current* file, so the identity check always passes. Scenario: the case is closed, and a same-size case.db is copied over it (Explorer keeps the old modification time) or swapped in by rename. The next open skips quick_check. Fix: load the file ID when the stamp is taken (`os.SameFile(fi, fi)` or `f.Stat()` on an open handle). Add a test that replaces the file by rename and restores its time with Chtimes.

2. **Medium, integrity.** case.go:410-432 and 199, verify_cache.go:177. When `IntegrityCheck` returns ErrDamaged, or a query hits CORRUPT/IOERR_DATA on a live handle, `e.ok` stays true. At shutdown the file is stamped as trusted, and the next open skips quick_check on a case that was just reported damaged. Fix: call `verified.drop(c.dir)` on every ErrDamaged path.

3. **Medium, availability regression.** timeline_index.go:36-78, sqlite.go:749, case.go:875. A failed build is never remembered, and nothing checks free space first. On a case near MaxDBBytes or on a nearly full disk, every open starts a full scan that fails with SQLITE_FULL. That scan holds importMu and the write lock, so tag and retry writes fail with BUSY after 10 s. Import also runs `indexSQL` inside its own transaction, so every import into such a case, even a tiny file, now fails with "case size". Fix: remember failure per process (as `attrIdx=2` does) and check headroom first. Build the timeline indexes outside the import transaction, best effort.

4. **Low, consistency.** case.go BucketCounts. The count query, the error query and each chunk run as separate statements on a pool of 4 connections, so each can see a different snapshot. If an import commits between them, a cell can get Errors > Count. Fix: run all chunks in one read transaction on one connection, or clamp Errors to Count.

5. **Low.** sqlite.go:756. The build's extra reference makes BeginFree and FinishFree return ErrCaseInUse for the whole build, and keeps the keys in memory after the user closes the case. An Import that waits on it in `lockImport` cannot be cancelled. Fix: the Free paths cancel the build, or the refs check ignores the build's own reference.

**Checked, nothing found:**
- **Crash mid-build:** both indexes are built in one transaction, so it can end with both or neither, never half.
- **Index expression:** the index and the query share the `statusIs5xxExpr` constant, so they are identical.
- **Goroutines and locks:** no leaks, no deadlock (importMu is released before `bg.Close` takes s.mu).
- **Events merge:** gives the same results, ordering and limit. The PK is BINARY, so byte order matches Go. One change: an empty result is now `[]`, not nil.
- **Bucket chunk boundaries and placeholders:** correct.
- **Migration:** safe. Older binaries look indexes up by name only.
- **Linux build:** fine.
