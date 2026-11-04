# Perf sweep loop 1: timeline index fix

Files (internal/casestore/sqlite): schema.go (indexes, statusIs5xxExpr), timeline_index.go (new: detect + background build + error merge), events_merge.go (new: multi-source Events), sqlite.go (start build on open), case.go (BucketCounts error query, Events dispatch, queryHook), timeline_index_test.go (new). No schemaVersion bump (tests pin 4; rollback binaries keep opening cases).

Design
- ev_src_ts (source, ts_ms, import_id) and partial ev_err_ts (same columns, WHERE the exact 5xx json_extract expression). New cases get both at first import; existing ones get them in a background build on open (TryLock, one transaction, pinned with its own ref so per-request open/close does not cancel it; Erase/Store.Close still do). Opens never wait; queries run the old way until it ends.
- BucketCounts: count query is covering on ev_src_ts; errors are a second query planned on ev_err_ts, merged per cell. Equal to CountBucketsScan.
- Events with several sources: per-source key queries (covering), merged by (ts_ms, evidence_id), cut to limit, rows read by id. A single "source IN" query cannot be read in order from the index.

Timings (copies only)
- Query level, 1d / 7d / 31d: S Events 1.2s flat (was 9-25s, 7d+ timeout), counts 0.12 / 0.54 / 2.4s. L Events 1.0s flat, counts 0.08 / 0.33 / 1.6s.
- Stress (whole HTTP call): S 0 timeouts (was 21/28), median 5.2s, max 7.6s. M 0 timeouts, median 15s. L 1 timeout (186d iis count partial) of 29 (was 25), median 24s.
- Index build: about 1 min on L (12M rows), disk +14% (S +113 MB).

Tests: new case has indexes; old case gets them once, reopen no-op; build does not block open; build survives opener Close; EXPLAIN assertions (Events keys, count covering, error on ev_err_ts); error counts equal with/without indexes/scan; multi-source Events identical to the single ordered query. vet, Linux vet, casestore/server/audit tests pass.

Risks and finding
- Remaining per-call time is Case.verify (PRAGMA quick_check) on every open: about 70% of a 1d call on S (2.6s), about 20s on L. The stress and openCase open per request, so each request pays it. Not touched (integrity check); recommend caching verification per process or skipping it when a handle is already open.
- Until the build ends, queries are as slow as before. A pinned build blocks Free/Archive with ErrCaseInUse for its duration.
