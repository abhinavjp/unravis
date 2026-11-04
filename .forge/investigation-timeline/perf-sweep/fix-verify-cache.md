# Perf sweep loop 2: quick_check once per process

Files (internal/casestore/sqlite): verify_cache.go (new), verify_cache_test.go (new), case.go (verify split: quickCheckOnce/quickCheck, gate field, release in shutdown), sqlite.go (drop on Erase, eraseFiles, createDB, openDB error), archive.go (drop in removeWorkingCopy).

Rule (documented in verify_cache.go): skip PRAGMA quick_check only if this process already passed it on the same absolute case.db path with the same DB key hash AND (a handle on the case is open now, OR all handles closed and case.db is the same file (os.SameFile), same size and mtime, and -wal same size as stamped after the last close/checkpoint, and that stamp was not "racy": mtime within 2s of the stamp time). Failed check, any later verify failure, openDB error, Erase, Free, create: entry cleared. Per-case check mutex (cases do not wait on each other); concurrent first opens share one check. Case id, schema, migrate, manifest cross-check still run each open; a skipped check still does a cheap sqlite_master read so wrong key / unreadable file give the same errors.

Timings (copies only, whole HTTP call)
- S before: median 5.2s, max 7.6s. After: median 2.29s, p95 2.68s, max 3.64s (first call in process, pays the check); 1d windows 0.93-1.07s after the first; 7d 1.2s, 30d 2.1s, 186d 2.4s. 0 timeouts.
- M after (index present): median 4.1s, 1d 2.2s, 186d 6.2s, first call 14s (full check of 6M rows, once). Before (loop 1): median 15s.
- M copy deleted. L not run.

Tests: skip on reopen and beside a live handle; changed file (mtime shifted) rejected; failed check never cached; racy same-size same-mtime rewrite rejected; wrong key after cached check = ErrWrongKey; 8 concurrent first opens = 1 check; Erase drops entry. Existing corruption tests (TestQuickCheckCatchesCorruption, ManifestCrossCheck, KillBeforeCommit, WrongKey) pass unchanged. go vet, Linux vet, casestore/server/audit/store tests pass.

Risks
- Corruption that appears while the file is at rest with identical size, mtime (beyond 2s old) and WAL size is not seen until process restart. Page checksums still catch it on read.
- Another process's writes show up as a stamp change (WAL size or mtime) and force a full check.
- A case written moments ago (under 2s) is re-checked on next open.
- Cache grows by one small entry per case until Erase/Free.
