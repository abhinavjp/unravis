// Package bbsql holds the fixed, allowlisted SQL statements for the Blackbox
// SQL samplers. Every statement is a compile-time constant: the C3 SET prefix
// plus exactly one read-only SELECT plus a tag comment. Nothing is built from
// input and no statement takes parameters; deltas are computed in Go by the
// samplers, never in SQL. Statement and IsAllowed are the only send gates.
//
// W7-05: the send gates read a PRIVATE registry snapshot taken at init, and
// every entry must pass lintStatement, a restricted-grammar validator (exact
// SET prefix, exactly one SELECT, no trailing statement, no SELECT INTO, only
// approved sources). The exported Statements map stays for the existing test
// suite; mutating it can no longer change what the gates admit.
package bbsql

import (
	"fmt"
	"strings"
)

// setPrefix is the C3 session prefix prepended to every batch.
const setPrefix = "SET NOCOUNT ON; SET LOCK_TIMEOUT 1000; SET DEADLOCK_PRIORITY LOW; SET TRANSACTION ISOLATION LEVEL READ UNCOMMITTED; "

// bb:requests (BL-101): running-at-once grouped by wait type, own session
// excluded. The self-join identifies head blockers (a blocker that is not
// itself blocked) without a second SELECT. Only counts/ms/wait type are output.
// TOP 256 is the C6 cap in SQL (defense in depth; the reader stops at 257).
const stmtRequests = setPrefix + `SELECT TOP 257 ISNULL(r.wait_type, N'') AS wait_type,
  COUNT(*) AS tasks,
  MAX(r.wait_time) AS max_wait_ms,
  SUM(CASE WHEN r.blocking_session_id > 0 THEN 1 ELSE 0 END) AS blocked,
  COUNT(DISTINCT CASE WHEN r.blocking_session_id > 0 AND ISNULL(b.blocking_session_id, 0) = 0 THEN r.blocking_session_id END) AS head_blockers,
  MAX(CASE WHEN r.blocking_session_id > 0 THEN r.wait_time ELSE 0 END) AS max_block_ms
FROM sys.dm_exec_requests AS r
LEFT JOIN sys.dm_exec_requests AS b ON b.session_id = r.blocking_session_id
WHERE r.session_id <> @@SPID
GROUP BY ISNULL(r.wait_type, N'')
ORDER BY COUNT(*) DESC /* unravis:bb:requests */`

// bb:waits (BL-102): top 20 cumulative waits, benign waits filtered. SQL fetches
// 21 so the reader can SEE the 21st row and mark the sample truncated; only 20
// are ever kept (W8-05). The filter
// LIST is reused from internal/dbhealth/sql.go:101-112 (no query-text code).
const stmtWaits = setPrefix + `SELECT TOP 21 wait_type, wait_time_ms, waiting_tasks_count
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
// The DMV column is timeout_error_count; it is aliased to timeout_count so the
// fixed output mapping is unchanged. sys.dm_exec_requests likewise exposes
// wait_time (ms), not wait_time_ms - that name belongs to dm_os_wait_stats.
//
// W11-02: resource_semaphore_id is documented as NONUNIQUE (0 = regular,
// 1 = small-query) and repeats in EVERY resource pool, so it cannot identify a
// semaphore on its own - same-id rows from different pools would overwrite each
// other's baselines. The numeric pool_id is projected and the rows are ordered
// by the composite (pool_id, resource_semaphore_id) so the TOP cap is
// deterministic and the Go baseline key is the same composite. pool_id is a
// numeric id, never a pool name (C9); the DMV declares it int NOT NULL.
const stmtSemaphores = setPrefix + `SELECT TOP 17 pool_id, resource_semaphore_id, grantee_count, waiter_count, forced_grant_count, timeout_error_count AS timeout_count
FROM sys.dm_exec_query_resource_semaphores
ORDER BY pool_id, resource_semaphore_id /* unravis:bb:semaphores */`

// bb:tempdb (BL-118): tempdb page totals summed in SQL, no per-session ids.
const stmtTempdb = setPrefix + `SELECT SUM(internal_objects_alloc_page_count) AS internal_pages,
  SUM(user_objects_alloc_page_count) AS user_pages
FROM sys.dm_db_session_space_usage /* unravis:bb:tempdb */`

// bb:filestats (BL-119): per-file I/O stalls, ids only (no sys.master_files join,
// no paths/names); deltas computed in Go.
const stmtFilestats = setPrefix + `SELECT TOP 257 database_id, file_id, io_stall_read_ms, io_stall_write_ms, num_of_reads, num_of_writes, size_on_disk_bytes
FROM sys.dm_io_virtual_file_stats(NULL, NULL)
ORDER BY database_id, file_id /* unravis:bb:filestats */`

// bb:ag (BL-119): AG database replica state, gated by IsHadrEnabled in Go. No
// sys.availability_replicas and no replica server names; ids/enum only.
// TOP 64 is the C6 cap in SQL (defense in depth; the reader stops at 65).
//
// W11-03: sys.dm_hadr_database_replica_states returns a row for the LOCAL
// database AND extra rows for its remote secondary copies, and database_id +
// synchronization_state alone cannot tell them apart - a remote replica's state
// could be persisted as if it were local. The fixed predicate is_local = 1 keeps
// only the local replica's own database rows. is_local is a nullable bit, so a
// NULL (unreadable locality) is excluded, never sampled. The predicate is
// numeric and carries no replica server name; lintStatement enforces it.
const stmtAG = setPrefix + `SELECT TOP 65 database_id, synchronization_state
FROM sys.dm_hadr_database_replica_states
WHERE is_local = 1
ORDER BY database_id /* unravis:bb:ag */`

// bb:hadr is the guard for bb:ag. SERVERPROPERTY answers on every 2012+
// instance, so an instance with no availability groups never has an AG DMV
// queried: the gate returns one row with hadr = 1 only when IsHadrEnabled is
// on, and the runner skips bb:ag otherwise. No DMV, no names.
const stmtHadr = setPrefix + `SELECT TOP 1 CAST(SERVERPROPERTY(N'IsHadrEnabled') AS int) AS hadr /* unravis:bb:hadr */`

// bb:ceiling_role (BL-124, W9-01): C8 control query — one row of 0-on-good
// failure flags. The first eight are fixed server-role membership flags
// (IS_SRVROLEMEMBER is server-scoped): any of them 1 refuses the login.
//
// The last four are the POSITIVE PROOF that the effective-permission list was
// readable (W9-01: an empty result must never mean "clean"). The source is
// sys.fn_my_permissions(NULL, N'SERVER'), the EFFECTIVE server permissions of
// the current security context: it expands custom and nested server roles,
// Windows token groups, public and the SQL2022 ##MS_*## fixed roles, and it
// already subtracts DENYs, so a protective DENY is never reported as an excess
// right. The aggregates read the WHOLE list (COUNT/SUM), so the C6 row cap
// cannot hide a right:
//
//	principal_missing  SUSER_ID() did not resolve the login
//	perm_empty         the effective list has no rows at all
//	perm_outside       effective rights outside the ceiling (a refusal)
//	connect_missing    CONNECT SQL is absent from the effective list
//
// A NULL flag (unreadable) fails closed in CheckCeiling, never counted as 0.
const stmtCeilingRole = setPrefix + `SELECT IS_SRVROLEMEMBER(N'sysadmin') AS sysadmin,
  IS_SRVROLEMEMBER(N'securityadmin') AS securityadmin,
  IS_SRVROLEMEMBER(N'serveradmin') AS serveradmin,
  IS_SRVROLEMEMBER(N'setupadmin') AS setupadmin,
  IS_SRVROLEMEMBER(N'processadmin') AS processadmin,
  IS_SRVROLEMEMBER(N'dbcreator') AS dbcreator,
  IS_SRVROLEMEMBER(N'bulkadmin') AS bulkadmin,
  IS_SRVROLEMEMBER(N'diskadmin') AS diskadmin,
  CASE WHEN SUSER_ID() IS NULL THEN 1 ELSE 0 END AS principal_missing,
  CASE WHEN COUNT(*) = 0 THEN 1 ELSE 0 END AS perm_empty,
  SUM(CASE WHEN permission_name NOT IN (N'CONNECT SQL', N'VIEW ANY DATABASE', N'VIEW SERVER STATE', N'VIEW SERVER PERFORMANCE STATE') THEN 1 ELSE 0 END) AS perm_outside,
  CASE WHEN ISNULL(SUM(CASE WHEN permission_name = N'CONNECT SQL' THEN 1 ELSE 0 END), 0) = 0 THEN 1 ELSE 0 END AS connect_missing
FROM sys.fn_my_permissions(NULL, N'SERVER') /* unravis:bb:ceiling_role */`

// bb:ceiling_perms (BL-124, W9-01): C8 control query — returns ONLY effective
// permissions outside the Blackbox ceiling, so any returned row is a refusal.
// The source is the same effective-permission function as bb:ceiling_role
// (roles, groups, public and ##MS_*## included, DENYs subtracted), NOT the
// direct-grant catalog: a right inherited through a custom role belongs to the
// role's principal id and was invisible to a SUSER_ID() predicate on
// sys.server_permissions. The NOT IN filter keeps the check fail-closed under
// the C6 row cap (TOP 2 == cap); the Go subset assert in CheckCeiling is the
// second gate, and the positive proof lives in bb:ceiling_role.
const stmtCeilingPerms = setPrefix + `SELECT TOP 2 permission_name
FROM sys.fn_my_permissions(NULL, N'SERVER')
WHERE permission_name NOT IN (N'CONNECT SQL', N'VIEW ANY DATABASE', N'VIEW SERVER STATE', N'VIEW SERVER PERFORMANCE STATE')
ORDER BY permission_name /* unravis:bb:ceiling_perms */`

// buildRegistry assembles the fixed allowlist from the compile-time constants.
// It is called twice at init: once for the private production registry the
// send gates read, once for the exported compatibility map.
func buildRegistry() map[string]string {
	return map[string]string{
		"bb:requests":      stmtRequests,
		"bb:waits":         stmtWaits,
		"bb:schedulers":    stmtSchedulers,
		"bb:semaphores":    stmtSemaphores,
		"bb:tempdb":        stmtTempdb,
		"bb:filestats":     stmtFilestats,
		"bb:ag":            stmtAG,
		"bb:hadr":          stmtHadr,
		"bb:ceiling_role":  stmtCeilingRole,
		"bb:ceiling_perms": stmtCeilingPerms,
	}
}

// statements is the PRIVATE production registry: the single source of truth
// for what may be sent. Statement and IsAllowed read only this map, so a
// runtime mutation of the exported compatibility map cannot widen the send
// policy (W7-05). Values are fixed constants; a later packet may add more
// fixed entries but must never build text at runtime.
var statements = buildRegistry()

// Statements is the exported compatibility view of the allowlist, kept because
// the existing test suite reads it. It is NOT the send policy: the gates read
// the private snapshot above, and every entry here must pass lintStatement at
// init. Do not mutate it in production code.
var Statements = buildRegistry()

func init() {
	// Mechanical C2 gate: a registry entry that does not satisfy the restricted
	// grammar stops the process at startup instead of reaching SQL Server.
	for id, text := range statements {
		if err := lintStatement(id, text); err != nil {
			panic("bbsql: allowlist entry " + id + " fails the C2 grammar: " + err.Error())
		}
	}
}

// Statement returns the fixed allowlisted text for id. ok is false for any
// unknown id; a query is never built or altered from input.
func Statement(id string) (string, bool) {
	text, ok := statements[id]
	return text, ok
}

// IsAllowed reports whether text exactly equals one of the allowlisted
// statements. Any mutation (whitespace, an added token, a second SELECT) fails.
func IsAllowed(text string) bool {
	for _, s := range statements {
		if s == text {
			return true
		}
	}
	return false
}

// --- W7-05 restricted-grammar lint -----------------------------------------

// sqlToken is one lexical token of a statement body.
type sqlToken struct {
	kind string // "word", "num", "str", "punct"
	text string
}

// lexSQL tokenizes the body after the C3 prefix. Only N'...' string literals
// are legal (every constant uses them); a bare quote, an unterminated literal
// or an unknown character is a refusal, not a guess.
func lexSQL(s string) ([]sqlToken, error) {
	var toks []sqlToken
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			i++
		case c == 'N' && i+1 < len(s) && s[i+1] == '\'':
			j := i + 2
			for j < len(s) {
				if s[j] == '\'' {
					if j+1 < len(s) && s[j+1] == '\'' { // escaped quote
						j += 2
						continue
					}
					break
				}
				j++
			}
			if j >= len(s) {
				return nil, fmt.Errorf("unterminated string literal")
			}
			toks = append(toks, sqlToken{"str", s[i : j+1]})
			i = j + 1
		case c == '\'':
			return nil, fmt.Errorf("bare string literal (only N'...' is allowed)")
		case isWordStart(c):
			j := i + 1
			for j < len(s) && isWordChar(s[j]) {
				j++
			}
			toks = append(toks, sqlToken{"word", s[i:j]})
			i = j
		case c >= '0' && c <= '9':
			j := i + 1
			for j < len(s) && s[j] >= '0' && s[j] <= '9' {
				j++
			}
			toks = append(toks, sqlToken{"num", s[i:j]})
			i = j
		case strings.ContainsRune("(),.;=<>+-*/%", rune(c)):
			toks = append(toks, sqlToken{"punct", string(c)})
			i++
		default:
			return nil, fmt.Errorf("unexpected character %q", string(c))
		}
	}
	return toks, nil
}

func isWordStart(c byte) bool {
	return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c == '_' || c == '@' || c == '#'
}

func isWordChar(c byte) bool {
	return isWordStart(c) || c >= '0' && c <= '9' || c == '$'
}

// lintBannedKeywords are statement/execution tokens refused as whole words.
// INTO is here too: SELECT ... INTO #x writes a table (W7-05). SET is here so
// a post-prefix session-option override can never ride along.
var lintBannedKeywords = map[string]bool{
	"exec": true, "execute": true, "insert": true, "update": true, "delete": true,
	"merge": true, "alter": true, "create": true, "drop": true, "grant": true,
	"revoke": true, "deny": true, "dbcc": true, "kill": true, "openrowset": true,
	"openquery": true, "opendatasource": true, "waitfor": true, "set": true,
	"union": true, "truncate": true, "shutdown": true, "backup": true,
	"restore": true, "use": true, "declare": true, "print": true,
	"raiserror": true, "throw": true, "bulk": true, "into": true, "values": true,
	"begin": true, "commit": true, "rollback": true, "while": true, "goto": true,
}

// lintBannedColumns are exfiltration substrings refused anywhere in the text
// (the C2 column blacklist). The fixed constants carry none of them.
var lintBannedColumns = []string{
	"sql_text", "dm_exec_sql_text", "query_plan", "input_buffer", "_handle",
	"statement_", "resource_description", "program_name", "host_name", "login",
	"physical_name", "master_files", "availability_replicas",
}

// lintApprovedFrom are the ONLY sources a statement may read, rendered in the
// lexer's canonical spacing. A new source is a reviewed change to this list,
// never a runtime string.
var lintApprovedFrom = map[string]bool{
	"sys.dm_exec_requests AS r LEFT JOIN sys.dm_exec_requests AS b ON b.session_id = r.blocking_session_id": true,
	"sys.dm_os_wait_stats":                     true,
	"sys.dm_os_schedulers":                     true,
	"sys.dm_exec_query_resource_semaphores":    true,
	"sys.dm_db_session_space_usage":            true,
	"sys.dm_io_virtual_file_stats(NULL, NULL)": true,
	"sys.dm_hadr_database_replica_states":      true,
	"sys.fn_my_permissions(NULL, N'SERVER')":   true,
}

// renderTokens joins tokens with single spaces, tightening around punctuation
// so "sys . fn ( NULL , N'SERVER' )" renders as the approved source shape.
func renderTokens(toks []sqlToken) string {
	var b strings.Builder
	for i, t := range toks {
		if i > 0 {
			prev := toks[i-1].text
			if t.text != "," && t.text != ")" && t.text != "." && t.text != "(" && prev != "(" && prev != "." {
				b.WriteByte(' ')
			}
		}
		b.WriteString(t.text)
	}
	return b.String()
}

// lintStatement validates the restricted C2 grammar of one registry entry:
// the exact C3 SET prefix, exactly one read-only SELECT, the entry's tag as
// the ONLY comment, no semicolon (so no trailing statement and no post-prefix
// SET), no SELECT INTO, no execution/write keyword, only approved sources and
// only N'...' literals. It is the mechanical gate behind TestLintC2.
func lintStatement(id, text string) error {
	return lintStatementFrags(id, text, extraLintFrags[id])
}

// lintStatementFrags is lintStatement with a narrow carve-out: each fragment in
// frags is removed ONCE from the text before the banned-column scan, so a fixed
// text-statement (extras_qtext.go) can name the columns of sys.dm_exec_sql_text
// without relaxing the rule for any other statement. Everything else (prefix,
// tag, keywords, one SELECT, approved source) is checked on the full text.
func lintStatementFrags(id, text string, frags []string) error {
	if !strings.HasPrefix(text, setPrefix) {
		return fmt.Errorf("missing the exact C3 SET prefix")
	}
	body := text[len(setPrefix):]
	tag := "/* unravis:" + id + " */"
	if !strings.HasSuffix(body, tag) {
		return fmt.Errorf("missing the trailing tag %s", tag)
	}
	sql := strings.TrimSpace(body[:len(body)-len(tag)])
	if strings.Contains(sql, "/*") || strings.Contains(sql, "--") {
		return fmt.Errorf("comment inside the statement body")
	}
	scan := text
	for _, f := range frags {
		if !strings.Contains(scan, f) {
			return fmt.Errorf("allowed fragment is not in the statement")
		}
		scan = strings.Replace(scan, f, " ", 1)
	}
	lower := strings.ToLower(scan)
	for _, tok := range lintBannedColumns {
		if strings.Contains(lower, tok) {
			return fmt.Errorf("banned column token %q", tok)
		}
	}
	toks, err := lexSQL(sql)
	if err != nil {
		return err
	}
	if len(toks) == 0 || toks[0].kind != "word" || !strings.EqualFold(toks[0].text, "SELECT") {
		return fmt.Errorf("body must start with SELECT")
	}
	selects, tops := 0, 0
	for i, t := range toks {
		if t.kind == "punct" && t.text == ";" {
			return fmt.Errorf("semicolon: a trailing statement or extra SET")
		}
		if t.kind != "word" {
			continue
		}
		w := strings.ToLower(t.text)
		if w == "select" {
			selects++
		}
		if lintBannedKeywords[w] {
			return fmt.Errorf("banned keyword %q", t.text)
		}
		if strings.HasPrefix(w, "sp_") || strings.HasPrefix(w, "xp_") {
			return fmt.Errorf("banned procedure prefix %q", t.text)
		}
		if strings.HasPrefix(w, "@") && w != "@@spid" {
			return fmt.Errorf("variable %q is not allowed", t.text)
		}
		if w == "top" {
			tops++
			if tops > 1 {
				return fmt.Errorf("more than one TOP")
			}
			if i+1 >= len(toks) || toks[i+1].kind != "num" {
				return fmt.Errorf("TOP without a numeric bound")
			}
		}
	}
	if selects != 1 {
		return fmt.Errorf("want exactly 1 SELECT, got %d", selects)
	}
	// The FROM clause (if any) must be an approved source, up to the next
	// top-level clause keyword. No subqueries exist: a second SELECT already
	// failed above.
	from := -1
	for i, t := range toks {
		if t.kind == "word" && strings.EqualFold(t.text, "FROM") {
			from = i
			break
		}
	}
	if from >= 0 {
		end := len(toks)
		for i := from + 1; i < len(toks); i++ {
			if toks[i].kind == "word" {
				switch strings.ToUpper(toks[i].text) {
				case "WHERE", "GROUP", "ORDER", "HAVING":
					end = i
				}
			}
			if end != len(toks) {
				break
			}
		}
		src := renderTokens(toks[from+1 : end])
		if !lintApprovedFrom[src] {
			return fmt.Errorf("unapproved source %q", src)
		}
	}
	// W11-03: bb:ag must carry the fixed local-row predicate. The DMV returns
	// the local database row AND its remote secondary copies, so without
	// is_local = 1 a remote replica's state is indistinguishable from the local
	// one. Matched as tokens (not a substring) so a comment or a renamed column
	// cannot satisfy it; a NULL is_local is excluded by the predicate, never
	// sampled. This is enforced at init like the rest of the grammar.
	if id == "bb:ag" && !hasLocalRowPredicate(toks) {
		return fmt.Errorf("bb:ag must filter to the local replica row (WHERE is_local = 1)")
	}
	return nil
}

// hasLocalRowPredicate reports whether the token stream carries the fixed
// is_local = 1 predicate (W11-03): the word is_local, then '=', then the number
// 1, in sequence.
func hasLocalRowPredicate(toks []sqlToken) bool {
	for i := 0; i+2 < len(toks); i++ {
		if toks[i].kind == "word" && strings.EqualFold(toks[i].text, "is_local") &&
			toks[i+1].kind == "punct" && toks[i+1].text == "=" &&
			toks[i+2].kind == "num" && toks[i+2].text == "1" {
			return true
		}
	}
	return false
}
