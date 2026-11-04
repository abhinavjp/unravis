# Backend large-window performance sweep (read-only)

Scope: Go backend, tracing timeline, Analysis screens, importers, import/archive jobs.
Question: with a 1, 3 or 12 month window, does it return data or fail cleanly?
Not read: sql/ folder, secrets, %APPDATA%. No source edited.

Summary: no OOM path and no unbounded response was found. Every window over a cap is
refused with a clear 400/409/413 message. The weak spots are (a) the case-store tracing
queries have no time budget or partial result, (b) one long write transaction per import,
(c) a few caches and sequential loops that are bounded but slow or heavy.

## Findings

### PF-B-01 (med) Tracing SQL/IIS lane queries have no scan budget or partial result
- Where: internal/server/tracing.go:641 (readLaneEvents), :722 (bucketLane); internal/casestore/sqlite/case.go:1061 (Events), :1101 (BucketCounts).
- What: BucketCounts is one GROUP BY with no LIMIT and no time budget; the error count uses json_extract on attrs_json per row. Events is ORDER BY ts_ms LIMIT 50000. Only the 5m request context stops them. Case.Each has a 10s/20M scan budget and series uses it; these two do not.
- 3-month note: the timeline caps at 30 days (400 with a message), so "3 months" is refused up front. A 30-day window on a 10M-row IIS case (events table is WITHOUT ROWID, keyed by evidence_id; ts index only) reads millions of rows with json_extract: tens of seconds to minutes. Past 5m the user gets a generic store error, not "partial".
- Fix: pass a budget (reuse InteractiveQueryBudget 10s style) to BucketCounts/Events; on expiry return Partial=true with reason "timeout" (the lane model already has timeout/rowcap codes). Add an expression index or a stored is_error column if json_extract dominates.
- Test: Go test in internal/casestore/sqlite, seed 5M events over 30 days (bulk insert in one tx, about 40s), call BucketCounts with a 2s budget; expect partial result, not an error. Tracing handler test with the same case asserts reason "timeout".

### PF-B-02 (low) Live audit lane over 24h reports reason code "error"
- Where: internal/server/tracing_audit.go (ErrBroadScanRefused mapping).
- What: a 3-day window on a live Mongo source is refused (correct) but the lane code is the generic "error", so the UI cannot show a specific hint.
- Fix: emit code "broad-scan" with the existing 24h text. Test: tracing_audit test with a fake live repo and a 72h window; assert code and message. Dataset: none.

### PF-B-03 (low) rollupCache keeps up to 8 full slot sets with no timer eviction
- Where: internal/server/rollup.go:21-94.
- What: TTL is 1 minute but pruning only runs on get/put. After a burst of 120-day reads on a restored case the last 8 entries (each up to MaxRollupGroups 600000 groups times latency-bin maps, estimate 50-400 MB worst case) stay in memory until the next request.
- Fix: drop entries by byte estimate, or add a timer/prune on put; cap entries by total group count (for example 1M). Test: server test, fake clock, put 8 large slot sets, advance clock 2m, assert memory freed or entries empty after the next put. Dataset: synthetic RollupSlots with 600k groups.

### PF-B-04 (med) Endpoint-daily / series on a restored source runs up to 120 sequential Analyze calls
- Where: internal/audit/dailyseries.go:230 (readDays), cap :27 (live 14 days, restored 120).
- What: one Analyze per local day in one 5m context; a 120-day window of 5M requests/day sources can pass 5m, then the whole request fails with a timeout and no data. No progress is exposed.
- Fix: return the days finished so far with partial=true when the context ends (or stop at 80 percent of the deadline); optionally run 2 days in parallel under the existing source slot. Test: audit package test with a fake repo that sleeps 100ms per day, 120 days, 3s context; expect partial days list and a flag. Dataset: none (fake repo).

### PF-B-05 (low) Events() with a selective source filter can walk a long ts range
- Where: internal/casestore/sqlite/case.go:1061; indexes in schema.go (ev_ts, ev_kind_ts, ev_import).
- What: a source filter has no (source, ts_ms) index, so a rare source over 30 days walks the whole ts range to find 50000 rows or hit the end. Bounded by PF-B-01's context only.
- Fix: covered by the PF-B-01 budget; add (source, ts_ms) index only if profiling shows it. Test: same 5M-event case, rare source (0.01 percent), expect under 2s or partial.

### PF-B-06 (med) Import holds one serializable write transaction for the whole file
- Where: internal/casestore/sqlite/case.go:619 (BeginTx), :580 (importMu), :612-618 (synchronous=FULL, wal_checkpoint(TRUNCATE) on a dedicated conn).
- What: a 2 GiB / 10M-row file (default caps) is one transaction. Duration is minutes. Other writers on the same case (workspace save, close case, a second import) wait on importMu or hit busy_timeout 10s and get "case is busy" / database locked. Reads are fine under WAL. The WAL grows to roughly the file size, and TRUNCATE checkpoint at the end can pause the job for tens of seconds.
- Mitigations already present: byte-count progress (read/total), cancel, 64 job cap, idle timeout 35s on the body.
- Fix: for rows above about 1M commit in chunks of 250k rows with a pending import row, finalised at the end (rollback = delete by import_id); or document the wait and return a clear 409 "an import is running" for writers. Test: casestore/sqlite test importing 3M synthetic rows while a second goroutine calls the workspace save; assert the second call returns a typed "busy: import running" error within 1s, not after 10s. Dataset: 3M rows, about 60s.

### PF-B-07 (low) No whole-case row cap beyond file cap and 8 GiB case file
- Where: internal/casestore/sqlite/sqlite.go:39,87 (MaxFileRows 10M per file); casefile.go:88 (MaxCaseFileDB 8 GiB).
- What: many 10M-row files can fill a case up to 8 GiB; later tracing/BucketCounts queries then hit PF-B-01. Import answers 413 cap-exceeded with a generic message ("size or row cap").
- Fix: split the 413 text into file cap and case cap; show events-in-case count in the import dialog. Test: handler test with a store option MaxCaseEvents=1000, import 1001 rows, assert reason "case-cap". Dataset: 1001 rows.

## Checked and fine
- Tracing request validation: 30-day cap, clear 400 text; axis ladder keeps 120 buckets at any width; per-lane event cap MaxLaneEvents 60 and row cap 50000 with "rowcap" reason.
- Response size: writeJSON 16 MB cap with 413; HTTP ReadTimeout 35s / WriteTimeout about 5m45s match the 5m requestTimeout, so a long read is not cut early.
- Analysis windows: analysis 7-day cap (validateInterval), pattern windows 24h on live sources, rollup 120 days, group limit 600000 (413), docs-examined budget (413), broad-scan gate (409 with message), size-check failure (502). All have specific messages.
- Live Mongo: per-source slot (1) and 4 global slots stop one slow source blocking the server; interactive 10s and census 60s budgets; legacy Mongo 3.4 limits 100k records / 16 MB.
- Case series (casestore/series.go): bucket cap 2000, Case.Each budget 20M events / 10s with partial=true; erase waits on the read share.
- Lanes (lanes.go, dbhealth): 14-day cap, TOP 3000 / 5000-doc limits, healthBusy TryLock with rate limit, 150s run timeout.
- caseEvents handler: limit 1..50000, filter value caps, truncated flag returned.
- Importers: every importer sets MaxBytes/MaxRows (64 MiB to 2 GiB, 1M to 10M rows), line and field caps, reject fraction, MaxEvents for wide files; over-limit gives 413 "limit-<name>".
- Import jobs (import_jobs.go): in-memory registry, 64 jobs max, TTL pruning, bytesRead/bytesTotal progress, cancel, spool file holds ciphertext only.
- Archive jobs (archive_jobs.go, archive_run.go): progress (read/total), phase, cancel before commit point, 30s timeouts on commit steps.
- SQLite pragmas: WAL, busy_timeout 10s, 4 conns, 64 MB cache; no N+1 query pattern found in the handlers read.

## Not covered
- internal/store/slowest.go and repeats internals, call_trace handler, SQL Server importer internals (sql/ folder excluded by instruction). Rows in those paths use the same 7-day analysis cap and slot admission.
