# BBX-5-05 SQL allowlist + lint (C1, C2)
Backlog: BL-124 | Group: 5 | Model: performance | Reason: the SQL text allowlist is the primary injection/exfiltration control; high security impact.
Depends on: none | Blocks: BBX-5-09, BBX-5-11..5-14
Reviewer: Ultimate (SQL).

## Goal
Define the fixed allowlisted SQL statements (about 7, each tagged `/* unravis:bb:<id> */`) and a lint test proving each is a fixed SET prefix plus exactly one SELECT containing none of the banned tokens. A fake-Queryer test proves nothing outside the allowlist is ever sent.

## Context
- New package `internal/bbsql`: file `allowlist.go` + `allowlist_test.go`.
- C1 (security review): about 7 constant statements tagged `/* unravis:bb:<id> */`; a fake Queryer test proves nothing else is ever sent. No string building, no parameters; deltas computed in Go.
- C2: each text is a fixed SET prefix plus exactly one SELECT, and contains NONE of: sql_text, dm_exec_sql_text, query_plan, input_buffer, _handle, statement_, resource_description, program_name, host_name, login, physical_name, master_files, availability_replicas, EXEC, sp_, xp_, INSERT, UPDATE, DELETE, MERGE, ALTER, CREATE, DROP, GRANT, DBCC, KILL, OPENROWSET, WAITFOR.
- C3 SET prefix (every batch): `SET NOCOUNT ON; SET LOCK_TIMEOUT 1000; SET DEADLOCK_PRIORITY LOW; SET TRANSACTION ISOLATION LEVEL READ UNCOMMITTED;`.
- Benign wait filter: reuse the LIST from `internal/dbhealth/sql.go:101-112` (SLEEP%, XE%, BROKER%, HADR%, QDS%, SQLTRACE%, FT_%, WAIT_XTP%, and the named NOT IN list). Do NOT copy dbhealth's query-text/program_name code.
- Rules: **read-only, fixed statements, no string building, no parameters**; **no query text/plan/handles/login/program/host**; **deltas in Go**.

## Do
1. `allowlist.go`: `const setPrefix = "SET NOCOUNT ON; SET LOCK_TIMEOUT 1000; SET DEADLOCK_PRIORITY LOW; SET TRANSACTION ISOLATION LEVEL READ UNCOMMITTED; "`.
2. Define `var Statements = map[string]string{...}` with these ids (each value = setPrefix + one SELECT + the tag comment). Finalize column lists to ONLY allowed output:
   - `bb:requests` (BL-101): from sys.dm_exec_requests, exclude own session (`session_id <> @@SPID`), GROUP BY wait_type → wait_type, COUNT(*) AS tasks, MAX(wait_time_ms) AS max_wait_ms, SUM(blocked flag) AS blocked, head-blocker count, MAX(blocking duration) AS max_block_ms. NO session_id/login/host/program_name/resource_description/statement offsets in the OUTPUT.
   - `bb:waits` (BL-102): SELECT TOP 20 wait_type, wait_time_ms, waiting_tasks_count FROM sys.dm_os_wait_stats WHERE wait_time_ms>0 AND <benign filter> ORDER BY wait_time_ms DESC.
   - `bb:schedulers` (BL-118): SELECT SUM(current_tasks_count) ..., SUM(runnable_tasks_count) ... FROM sys.dm_os_schedulers WHERE status = N'VISIBLE ONLINE' (summed in SQL, no per-row ids).
   - `bb:semaphores` (BL-118): SELECT resource_semaphore_id, grantee_count, waiter_count, forced_grant_count, timeout_count FROM sys.dm_exec_query_resource_semaphores (deltas in Go).
   - `bb:tempdb` (BL-118): SELECT SUM(internal_objects_alloc_page_count) ..., SUM(user_objects_alloc_page_count) ... FROM sys.dm_db_session_space_usage (tempdb page totals, summed).
   - `bb:filestats` (BL-119): SELECT TOP 256 database_id, file_id, io_stall_read_ms, io_stall_write_ms, num_of_reads, num_of_writes, size_on_disk_bytes FROM sys.dm_io_virtual_file_stats(NULL, NULL) (NO join to sys.master_files; no paths/names).
   - `bb:ag` (BL-119): guarded by IsHadrEnabled=1 in Go; SELECT database_id, synchronization_state, ... FROM sys.dm_hadr_database_replica_states (NO sys.availability_replicas, no replica server names).
3. `func Statement(id string) (string, bool)` — returns the constant text; unknown id → false (never builds a query).
4. `func IsAllowed(text string) bool` — true only if text exactly equals one of the allowlisted values.
5. Lint test (C2): for each statement assert it starts with setPrefix, contains exactly one SELECT (case-insensitive count == 1), contains the tag, and contains NONE of the banned tokens (case-insensitive substring scan). Note: `login` banned token must not false-positive on `CREATE LOGIN` — there is no CREATE here; ensure the grant script (BBX-5-02) is NOT in this package.

## Do not
- Do NOT build SQL by concatenation or accept parameters/user input.
- Do NOT include any banned token (C2 list) in any statement.
- Do NOT read query text, plans, handles, login/program/host names, file paths, or replica server names.
- Do NOT add INSERT/UPDATE/DELETE/EXEC/etc. — SELECT only.
- Do NOT copy dbhealth's qTopQueries/qRequests/qAgentJobs.

## Tests to write first
- `TestLintC2` — every statement: setPrefix + exactly one SELECT + tag + no banned token (table over all ids).
- `TestAllowlistExact` — IsAllowed true for each constant, false for any mutation (add a space, a token, a second SELECT).
- `TestUnknownStatementRefused` — Statement("bb:evil") → false.
- `TestFakeQueryerOnlyAllowlisted` — a fake Queryer records every text sent by the runner stub; assert all are IsAllowed (wire a minimal send path here or in BBX-5-10; at minimum test that Statement never returns a non-allowlisted text).
- `TestNoBannedTokenInOutput` — assert column lists exclude session_id/login/program_name/host_name/resource_description/physical_name.

## Verification checklist
- [ ] go build ./... passes
- [ ] go vet ./... passes
- [ ] go test ./internal/bbsql/... -count=1 passes, tests above included
- [ ] Both build-tag variants compile (GOOS=windows and GOOS=linux go vet ./internal/bbsql/...)
- [ ] Footprint: constants only; no goroutine/ticker
- [ ] No text captured — statements select only allowed numeric/enum columns (TestNoBannedTokenInOutput)
- [ ] C2 lint passes for every statement (TestLintC2)
- [ ] C1 allowlist exact; nothing else sendable (TestAllowlistExact, TestUnknownStatementRefused)
- [ ] No string building / parameters (code review)

## Live-check needed?
YES — owner runs each statement in SSMS on a real SQL Server to confirm it executes with the granted rights and returns only allowed columns. Fake/lint tests verify shape only.

## Report back
Files changed, each checklist item ticked or failed with output, the final statement texts, anything unconfirmed. Full report to `.forge/blackbox-expansion/reports/BBX-5-05.md`.