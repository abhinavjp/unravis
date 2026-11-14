# Blackbox SQL sampler: security review (Opus, design only), 2026-11-13

Scope: BL-101, BL-102, BL-118, BL-119. Design review only; no code was changed. Findings quote file lines as of 2026-11-13.

## Verdicts

| Item | Verdict | Allowed output |
|---|---|---|
| BL-101 running-at-once by wait type, blocking | Allow with conditions | wait_type, task count, max wait ms, blocked count, head-blocker count, max block ms. Never session id, login, host, program name, handles, statement offsets or `resource_description`. |
| BL-102 wait-stats deltas | Allow at 60 s, top 20, benign waits filtered (same filter as `internal/dbhealth/sql.go:101-112`) | wait type, delta counts and ms |
| BL-102 query-stats deltas | **Rejected** | Scans the whole plan cache, state grows without bound; Query Store already has this history. Use Batch Requests/sec and Compilations/sec counters instead. |
| BL-118 schedulers, semaphores, tempdb | Allow with conditions | schedulers: VISIBLE ONLINE rows summed in SQL; semaphores: grantee/waiter/forced/timeout deltas; tempdb page totals every 60 s |
| BL-119 file I/O stalls, AG state | Allow with conditions | `dm_io_virtual_file_stats`: database_id, file_id, deltas, TOP 256, no join to `sys.master_files` (no paths). AG state only when `IsHadrEnabled=1`, never `sys.availability_replicas` (names other servers). |

## Credential options

- **A. Windows sign-in as NT AUTHORITY\SYSTEM**: no stored secret, but the grant covers every SYSTEM process on the box, and on SQL 2008 and servers upgraded from it SYSTEM is often sysadmin. Not recommended.
- **B. Own service account (recommended)**: a second small service `UnravisBlackboxSql` (same exe, a SQL-run mode) running as `NT SERVICE\UnravisBlackboxSql`, writing only to `<folder>\sql`. The host sampler stays on LocalSystem (it needs that to read other users' processes). Adding a service SID to a LocalSystem service is not enough: SQL adds up the rights of every token group that has a login.
- **C. Stored SQL login**: rejected. Any local admin can decrypt it, it needs mixed-mode auth and rotation, and it would end up in `blackbox.json`, which is copied into the pack (`pack.go:122`, breaking the promise at `pack.go:15-18`).
- **Remote SQL**: rejected for A and B. On the network SYSTEM and virtual accounts both appear as `DOMAIN\HOST$`, so the grant reaches every such process on the host, and the Blackbox would no longer read only its own machine (`config.go:1-4`). Put a Blackbox on the SQL server.

## Mandatory controls (each one a test)

- **C1 Allowlist**: about 7 constant statements tagged `/* unravis:bb:<id> */`; a fake Queryer test proves nothing else is ever sent. No string building, no parameters; deltas computed in Go.
- **C2 Lint**: each text is a fixed SET prefix plus exactly one SELECT, and contains none of: sql_text, dm_exec_sql_text, query_plan, input_buffer, _handle, statement_, resource_description, program_name, host_name, login, physical_name, master_files, availability_replicas, EXEC, sp_, xp_, INSERT, UPDATE, DELETE, MERGE, ALTER, CREATE, DROP, GRANT, DBCC, KILL, OPENROWSET, WAITFOR.
- **C3 Session options on every batch**: `SET NOCOUNT ON; SET LOCK_TIMEOUT 1000; SET DEADLOCK_PRIORITY LOW; SET TRANSACTION ISOLATION LEVEL READ UNCOMMITTED`. A live test reads `dm_exec_sessions` (deadlock_priority -5, isolation 1, lock_timeout 1000).
- **C4 Time limits**: connect 5 s; each query 3 s (15 s items) or 5 s (60 s items); whole tick 5 s. A running tick means the next is skipped and counted, never queued or run in parallel.
- **C5 One connection**: max open 1, lifetime 1 h, app name "Unravis Blackbox SQL"; live test: at most one session with that name; exclude own session.
- **C6 Row caps**: `TOP n` in SQL; the Go reader stops at n+1 and marks "truncated". Do not reuse dbhealth's 5000 cap (`mssql.go:157`).
- **C7 Back-off and auto-off**: each failure doubles the interval (15 s up to 15 min). Ten failures in a row, any permission error, or a C8 failure sets auto-off, saved in the state file so Windows restart actions (`service_windows.go:95-99`) cannot clear it; only the owner turns it back on. If a query uses over 50% of its time limit three times in a row, 15 s items drop to 60 s.
- **C8 Rights ceiling, at every connect**: not sysadmin, no CONTROL SERVER, and the server permissions list only CONNECT SQL, VIEW ANY DATABASE and VIEW SERVER [PERFORMANCE] STATE. Otherwise refuse with the plain reason "this login has more rights than the Blackbox needs".
- **C9 What a sample stores**: one `sql` object decoded into a fixed struct (unknown fields rejected in tests). Allowed: wait types (`^[A-Z0-9_]{1,60}$`), counts, ms, KB, database_id, file_id, enum states. Database names off by default.
- **C10 Errors**: fixed codes only (like `ClassifyConnect`, `mssql.go:197`); never write `err.Error()` to events or status.
- **C11 /status**: only `sql: {enabled, state on/backing-off/auto-off/denied, lastOK, failures}`. No instance name, version or error text.
- **C12 Target check**: only `.`, `(local)`, `localhost`, 127.0.0.1, ::1 or `lpc:`, plus an optional instance name. Encrypt on.
- **C13 Budget test** (fake Queryer benchmark): under 1% of one core on average, under 10 MB extra memory, under 2 KB per sample. See also BL-122.
- **C14 Grant script**, run by the owner in SSMS, never through the installer or the elevated helper (`elevate_windows.go:56`):
  `CREATE LOGIN [NT SERVICE\UnravisBlackboxSql] FROM WINDOWS WITH DEFAULT_DATABASE=master; GRANT VIEW SERVER PERFORMANCE STATE TO [NT SERVICE\UnravisBlackboxSql];` (SQL 2019 and older: `VIEW SERVER STATE`). Revoke, printed by `remove`: stop the service, then `REVOKE VIEW SERVER PERFORMANCE STATE FROM [...]; DROP LOGIN [...];`.

## Existing Blackbox code that must change first (BL-123)

- `service.go:274` with `service_windows.go:25-27`: the recordings folder is readable by BUILTIN\Users, and a folder or share needs no read key (`docs/user/blackbox.md:46`), so the read key does not protect sample data. SQL data goes in `<folder>\sql` with a DACL of SYSTEM, Administrators and the SQL service SID only, or stays harmless by construction (C9).
- `recorder.go:149-156`: Collect runs inline in the single tick loop, so a hung SQL call would stall host sampling and housekeeping; it also writes `err.Error()` into events.
- `server.go:90-101`: plain HTTP; with bind 0.0.0.0 the read key and samples travel in the clear. Add TLS or keep SQL data off the endpoint.
- `service_windows.go:119-123`: no ServiceStartName, so LocalSystem; option B needs its own service.
- `config.go:206-208` and `server.go:251`: `probe.enabled` is refused. Keep that gate until C1-C14 pass.
- `config.go:317`, `pack.go:122`: `blackbox.json` is 0o644 and shipped in the pack. Never put credentials there.
- Do not copy dbhealth as a template: `sql.go:139` reads query text, `sql.go:122` reads program_name, `sql.go:174` reads job messages, and `mssql.go` sets no lock timeout or deadlock priority.
