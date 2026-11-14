# TASK — write `internal/bbsql/budget_test.go` (package bbsql) — the C13 footprint gate

Produce ONLY this Go test file. It is the C13 proof for the BBX-5-10 CONTRACT runner
(`runner.go`, same package, fields per the static block). You may use the same test-double
style as `runner_test.go`, but define your own doubles locally with distinct names
(`budgetQuery`, `budgetSampler`) so the two files never clash.

Fixed row fixtures (all columns must come from `columnKeys` so `decodeRows` accepts them):
- requests: 256 rows of `{wait_type, tasks, max_wait_ms, blocked, head_blockers, max_block_ms}`
- waits: 20 rows of `{wait_type, wait_time_ms, waiting_tasks_count}`
- schedulers: 1 row of `{current_tasks, runnable_tasks}`
- semaphores: 16 rows of `{resource_semaphore_id, grantee_count, waiter_count, forced_grant_count, timeout_count}`
- tempdb: 1 row of `{internal_pages, user_pages}`
- filestats: 256 rows of `{database_id, file_id, io_stall_read_ms, io_stall_write_ms, num_of_reads, num_of_writes, size_on_disk_bytes}`
- ag: 64 rows of `{database_id, synchronization_state}`
Use deterministic values (`fmt.Sprint("LATCH_"..., i)` style wait types matching
`^[A-Z0-9_]{1,60}$`), never rand.

What the file must contain:
1. `TestSQLBudget` — a Runner with all seven samplers due at once, a fake Queryer returning
   the fixtures, over 40 simulated ticks at the 15 s base interval (advance the fake clock,
   do not sleep). Assert and REPORT (with `t.Logf`) the measured numbers:
   - mean wall time per tick, and that mean < 150 ms (= 1 % of one core over 15 s) even
     though CI machines are noisy; keep the bound generous and state it in a comment.
   - total bytes allocated per tick from `testing.AllocsPerRun` or
     `testing.Benchmark`+`ReportAllocs`: assert < 10 MB per tick, i.e. the whole 40-tick
     run stays under 10 MB of extra live memory.
   - every persisted NDJSON line < 2 KB after trimming the envelope? NO — the C13 cap is
     per SAMPLE (one line). Compute `maxLine` and `meanLine` over the file and assert
     `maxLine < 2048` bytes... careful: the caps allow up to 256 numeric rows, which can
     exceed 2 KB. So the test must MEASURE and then enforce the honest bound: assert
     `meanLine < 2048` and, if a kind is larger, assert the largest kind's line count is
     capped by `CapRequests`/`CapFileStats` (i.e. prove the row caps are what bound it) and
     log the exact per-kind byte sizes so the owner can see the real figure.
     Write the assertion so it fails if a row cap is ever raised (e.g. compute the expected
     worst case from the `Cap*` constants and assert the measured size stays under
     `2 * expectedFromCaps` is wrong — instead: assert `len(rows) <= cap` for each kind AND
     assert the hourly file growth per tick is under 24 KB total, which is the real budget
     for one tick across all kinds).
   Resolve this honestly in the code: keep a single constant
   `budgetMaxTickBytes = 24 << 10` with a comment saying the 2 KB/sample figure in C13 is
   per SAMPLE GROUP after the row caps and that the measured per-kind sizes are logged, so
   the owner can veto. Do not fudge a passing test.
2. `BenchmarkSQLTick` — one full all-samplers tick per iteration with the fixtures;
   `b.ReportAllocs()`. Add `BenchmarkSQLTick60s` (only the 60 s samplers due) for the
   steady-state figure.
3. `TestSQLBudgetNoGoroutineGrowth` — snapshot `runtime.NumGoroutine()` before/after 20
   ticks and assert no growth (the runner must not leak a goroutine per tick) and that the
   hourly NDJSON file is reused (open count stays bounded: assert the fake closer was called
   at most once per failure, zero times across 20 successful ticks).

Keep the file self-contained: imports used, no helper from another test file, table-driven
where it helps, ~200-260 lines. Every threshold is a named constant so a reviewer can see
the budget in one place.
