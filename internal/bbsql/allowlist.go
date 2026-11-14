// Package bbsql holds the fixed, allowlisted SQL statements for the Blackbox
// SQL samplers. Every statement is a compile-time constant: the C3 SET prefix
// plus exactly one read-only SELECT plus a tag comment. Nothing is built from
// input and no statement takes parameters; deltas are computed in Go by the
// samplers, never in SQL. Statement and IsAllowed are the only send gates.
package bbsql

// setPrefix is the C3 session prefix prepended to every batch.
const setPrefix = "SET NOCOUNT ON; SET LOCK_TIMEOUT 1000; SET DEADLOCK_PRIORITY LOW; SET TRANSACTION ISOLATION LEVEL READ UNCOMMITTED; "

// bb:requests (BL-101): running-at-once grouped by wait type, own session
// excluded. The self-join identifies head blockers (a blocker that is not
// itself blocked) without a second SELECT. Only counts/ms/wait type are output.
// TOP 256 is the C6 cap in SQL (defense in depth; the reader stops at 257).
const stmtRequests = setPrefix + `SELECT TOP 256 ISNULL(r.wait_type, N'') AS wait_type,
  COUNT(*) AS tasks,
  MAX(r.wait_time_ms) AS max_wait_ms,
  SUM(CASE WHEN r.blocking_session_id > 0 THEN 1 ELSE 0 END) AS blocked,
  COUNT(DISTINCT CASE WHEN r.blocking_session_id > 0 AND ISNULL(b.blocking_session_id, 0) = 0 THEN r.blocking_session_id END) AS head_blockers,
  MAX(CASE WHEN r.blocking_session_id > 0 THEN r.wait_time_ms ELSE 0 END) AS max_block_ms
FROM sys.dm_exec_requests AS r
LEFT JOIN sys.dm_exec_requests AS b ON b.session_id = r.blocking_session_id
WHERE r.session_id <> @@SPID
GROUP BY ISNULL(r.wait_type, N'') /* unravis:bb:requests */`

// bb:waits (BL-102): top 20 cumulative waits, benign waits filtered. The filter
// LIST is reused from internal/dbhealth/sql.go:101-112 (no query-text code).
const stmtWaits = setPrefix + `SELECT TOP 20 wait_type, wait_time_ms, waiting_tasks_count
FROM sys.dm_os_wait_stats
WHERE wait_time_ms > 0
  AND wait_type NOT LIKE N'SLEEP%' AND wait_type NOT LIKE N'XE%' AND wait_type NOT LIKE N'BROKER%' AND wait_type NOT LIKE N'HADR%'
  AND wait_type NOT LIKE N'QDS%' AND wait_type NOT LIKE N'SQLTRACE%' AND wait_type NOT LIKE N'FT_%' AND wait_type NOT LIKE N'WAIT_XTP%'
  AND wait_type NOT IN (N'CLR_SEMAPHORE', N'LAZYWRITER_SLEEP', N'RESOURCE_QUEUE', N'WAITFOR', N'CHECKPOINT_QUEUE', N'REQUEST_FOR_DEADLOCK_SEARCH',
    N'LOGMGR_QUEUE', N'DIRTY_PAGE_POLL', N'ONDEMAND_TASK_QUEUE', N'SP_SERVER_DIAGNOSTICS_SLEEP', N'DISPATCHER_QUEUE_SEMAPHORE',
    N'PWAIT_ALL_COMPONENTS_INITIALIZED', N'SOS_WORK_DISPATCHER', N'REDO_THREAD_PENDING_WORK', N'WAIT_FOR_RESULTS', N'SLEEP_BPOOL_FLUSH',
    N'MEMORY_ALLOCATION_EXT', N'PARALLEL_REDO_DRAIN_WORKER', N'PARALLEL_REDO_TRAN_TURN', N'PARALLEL_REDO_WORKER_SYNC', N'PARALLEL_REDO_WORKER_WAIT_WORK',
    N'PREEMPTIVE_XE_GETTARGETSTATE', N'PVS_PREALLOCATE', N'CXCONSUMER', N'DBMIRROR_EVENTS_QUEUE', N'DBMIRRORING_CMD', N'PREEMPTIVE_OS_FLUSHFILEBUFFERS')
ORDER BY wait_time_ms DESC /* unravis:bb:waits */`

// bb:schedulers (BL-118): VISIBLE ONLINE schedulers summed in SQL, no per-row ids.
const stmtSchedulers = setPrefix + `SELECT SUM(current_tasks_count) AS current_tasks,
  SUM(runnable_tasks_count) AS runnable_tasks
FROM sys.dm_os_schedulers
WHERE status = N'VISIBLE ONLINE' /* unravis:bb:schedulers */`

// bb:semaphores (BL-118): query resource semaphore counts; deltas computed in
// Go. TOP 16 is the C6 cap in SQL (defense in depth; the reader stops at 17).
const stmtSemaphores = setPrefix + `SELECT TOP 16 resource_semaphore_id, grantee_count, waiter_count, forced_grant_count, timeout_count
FROM sys.dm_exec_query_resource_semaphores /* unravis:bb:semaphores */`

// bb:tempdb (BL-118): tempdb page totals summed in SQL, no per-session ids.
const stmtTempdb = setPrefix + `SELECT SUM(internal_objects_alloc_page_count) AS internal_pages,
  SUM(user_objects_alloc_page_count) AS user_pages
FROM sys.dm_db_session_space_usage /* unravis:bb:tempdb */`

// bb:filestats (BL-119): per-file I/O stalls, ids only (no sys.master_files join,
// no paths/names); deltas computed in Go.
const stmtFilestats = setPrefix + `SELECT TOP 256 database_id, file_id, io_stall_read_ms, io_stall_write_ms, num_of_reads, num_of_writes, size_on_disk_bytes
FROM sys.dm_io_virtual_file_stats(NULL, NULL) /* unravis:bb:filestats */`

// bb:ag (BL-119): AG database replica state, gated by IsHadrEnabled in Go. No
// sys.availability_replicas and no replica server names; ids/enum only.
// TOP 64 is the C6 cap in SQL (defense in depth; the reader stops at 65).
const stmtAG = setPrefix + `SELECT TOP 64 database_id, synchronization_state
FROM sys.dm_hadr_database_replica_states /* unravis:bb:ag */`

// bb:ceiling_role (BL-124): C8 control query — fixed server-role membership
// flags only (0/1); no login, host or session data. Every role listed here
// carries more rights than the Blackbox needs, so ANY flag of 1 is a refusal:
// sysadmin/securityadmin (full control / grant rights) plus serveradmin,
// setupadmin, processadmin, dbcreator, bulkadmin and diskadmin (server-wide
// configuration, process, database-creation, bulk-insert and file rights).
// IS_SRVROLEMEMBER is server-scoped, matching the server-level ceiling.
const stmtCeilingRole = setPrefix + `SELECT IS_SRVROLEMEMBER(N'sysadmin') AS sysadmin,
  IS_SRVROLEMEMBER(N'securityadmin') AS securityadmin,
  IS_SRVROLEMEMBER(N'serveradmin') AS serveradmin,
  IS_SRVROLEMEMBER(N'setupadmin') AS setupadmin,
  IS_SRVROLEMEMBER(N'processadmin') AS processadmin,
  IS_SRVROLEMEMBER(N'dbcreator') AS dbcreator,
  IS_SRVROLEMEMBER(N'bulkadmin') AS bulkadmin,
  IS_SRVROLEMEMBER(N'diskadmin') AS diskadmin /* unravis:bb:ceiling_role */`

// bb:ceiling_perms (BL-124): C8 control query — returns ONLY permissions
// outside the Blackbox ceiling, so any returned row is a refusal. The NOT IN
// filter keeps the check fail-closed under the C6 row cap: a full permission
// list could be truncated before an extra right is seen. TOP 2 mirrors the
// reader cap; the Go subset assert in CheckCeiling is the second gate.
//
// The grantee MUST be resolved with SUSER_ID(), the current session's SERVER
// principal id (sys.server_principals), because sys.server_permissions.
// grantee_principal_id is a server principal id. DATABASE_PRINCIPAL_ID()
// returns a DATABASE principal id instead: with a login-only grant (no user in
// the database, so the session runs as guest = database principal 2) it would
// read the public server role's row (server principal 2) and a GRANT CONTROL
// SERVER to the actual login would be invisible — a C8 bypass.
const stmtCeilingPerms = setPrefix + `SELECT TOP 2 permission_name
FROM sys.server_permissions
WHERE grantee_principal_id = SUSER_ID()
  AND permission_name NOT IN (N'CONNECT SQL', N'VIEW ANY DATABASE', N'VIEW SERVER STATE', N'VIEW SERVER PERFORMANCE STATE') /* unravis:bb:ceiling_perms */`

// Statements is the complete allowlist and the single source of truth for what
// may be sent. Values are fixed constants; a later packet may add more fixed
// entries (e.g. the bb:hadr guard) but must never build text at runtime.
var Statements = map[string]string{
	"bb:requests":      stmtRequests,
	"bb:waits":         stmtWaits,
	"bb:schedulers":    stmtSchedulers,
	"bb:semaphores":    stmtSemaphores,
	"bb:tempdb":        stmtTempdb,
	"bb:filestats":     stmtFilestats,
	"bb:ag":            stmtAG,
	"bb:ceiling_role":  stmtCeilingRole,
	"bb:ceiling_perms": stmtCeilingPerms,
}

// Statement returns the fixed allowlisted text for id. ok is false for any
// unknown id; a query is never built or altered from input.
func Statement(id string) (string, bool) {
	text, ok := Statements[id]
	return text, ok
}

// IsAllowed reports whether text exactly equals one of the allowlisted
// statements. Any mutation (whitespace, an added token, a second SELECT) fails.
func IsAllowed(text string) bool {
	for _, s := range Statements {
		if s == text {
			return true
		}
	}
	return false
}
