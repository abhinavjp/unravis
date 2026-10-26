package dbhealth

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// now is injected so tests can pin the clock.
type clock = func() time.Time

// sqlStep is one SQL Server check. run asks the server and builds the finding; the eval functions below are pure so they can be tested on canned rows.
type sqlStep struct {
	id, title, why string
	run            func(ctx context.Context, q Queryer, env *Env) Finding
}

// one makes a step out of a single query and a pure evaluator.
func one(id, title, why, query string, eval func(rows []Row, env *Env) Finding) sqlStep {
	return sqlStep{id: id, title: title, why: why, run: func(ctx context.Context, q Queryer, env *Env) Finding {
		rows, err := q.Query(ctx, query)
		if err != nil {
			return unavailable(id, title, why, classify(err))
		}
		f := eval(rows, env)
		f.ID, f.Title, f.Why = id, title, why
		return f
	}}
}

// classify turns a driver error into one of three fixed sentences. The driver's own text is never passed on: it can carry server or login names.
func classify(err error) string {
	m := strings.ToLower(err.Error())
	switch {
	case strings.Contains(m, "permission") || strings.Contains(m, "denied") || strings.Contains(m, "view server state") || strings.Contains(m, "view database state"):
		return reasonPermission
	case strings.Contains(m, "invalid object name") || strings.Contains(m, "invalid column name") || strings.Contains(m, "could not find stored procedure"):
		return reasonNotThere
	}
	return reasonFailed
}

// RunSQL runs every SQL Server check, one after the other, and returns the report. A check that cannot run is reported as unavailable; only a cancelled
// context stops the run early. database is the one the connection is already scoped to (it may be empty).
func RunSQL(ctx context.Context, q Queryer, label, database string, now clock) Report {
	env := &Env{Database: database, Now: now()}
	rep := Report{Kind: "sql", Label: label, Database: database, StartedAt: env.Now}
	for _, s := range sqlSteps() {
		if ctx.Err() != nil {
			break
		}
		cctx, cancel := context.WithTimeout(ctx, checkTimeout)
		f := s.run(cctx, q, env)
		cancel()
		rep.Findings = append(rep.Findings, f)
	}
	rep.Version = env.Version
	rep.Finish(now())
	return rep
}

func sqlSteps() []sqlStep {
	return []sqlStep{
		one("server", "Server", "Version, how long the server has been up, and its size. A server that restarted recently has reset every counter below, so those numbers are small.", qServer, evalServer),
		one("cpu", "CPU, last 30 minutes", "How busy the processor was, one sample a minute. Sustained high CPU from SQL Server slows every request; high CPU from another program steals time from it.", qCPU, evalCPU),
		one("waits", "What the server waits on", "Total time requests spent waiting since the server started, by reason. The top reason tells you what is holding things back: disk, locks, the log, memory or the application reading results slowly.", qWaits, evalWaits),
		one("memory", "Memory", "Whether the machine and SQL Server have enough memory. Few free megabytes, requests waiting for a memory grant, or a short page life mean the data cache is too small.", qMemory, evalMemory),
		one("requests", "Running now: blocked or slow", "Requests that are blocked by another session, or have been running more than 30 seconds, at this moment.", qRequests, evalRequests),
		one("sessions", "Connections", "Who is connected, grouped by program. A connection left in the middle of a transaction can hold locks that block others.", qSessions, evalSessions),
		one("queries", "Heaviest queries", "The statements that used the most total time since their plans were cached. An average over one second is worth a look.", qTopQueries, evalTopQueries),
		{id: "querystore", title: "Query Store, last 24 hours", why: "Query Store keeps query timings per hour inside the database. A sudden jump in average duration marks when something got slower.", run: runQueryStore},
		one("indexes", "Missing indexes", "Indexes the optimizer wished it had while running queries on this database. Suggestions only: check them before adding one, since every index slows writes.", qMissingIndexes, evalMissingIndexes),
		one("backups", "Backups", "When each database was last backed up. No full backup, or a database in FULL recovery with no recent log backup, risks data loss and a log file that grows without limit.", qBackups, evalBackups),
		one("settings", "Database settings", "Settings that cause trouble on a busy database: auto-close, auto-shrink, no page checksums, or a database that is not online.", qSettings, evalSettings),
		one("deadlocks", "Deadlocks", "How many times SQL Server had to kill one of two sessions waiting on each other since it started.", qDeadlocks, evalDeadlocks),
		one("agentjobs", "SQL Agent jobs that failed", "Scheduled jobs that failed in the last 24 hours.", qAgentJobs, evalAgentJobs),
	}
}

/* ------------------------------------------------------------- queries */

const qServer = `SELECT CAST(SERVERPROPERTY('ProductVersion') AS nvarchar(50)) AS version,
  CAST(SERVERPROPERTY('Edition') AS nvarchar(100)) AS edition,
  DATEDIFF(MINUTE, sqlserver_start_time, SYSDATETIME()) AS uptime_min,
  cpu_count AS cpus,
  CAST(physical_memory_kb / 1024 AS bigint) AS memory_mb
FROM sys.dm_os_sys_info`

const qCPU = `DECLARE @now bigint = (SELECT cpu_ticks / (cpu_ticks / ms_ticks) FROM sys.dm_os_sys_info);
SELECT TOP 30
  DATEADD(ms, -1 * (@now - x.[timestamp]), GETDATE()) AS at,
  x.rec.value('(./Record/SchedulerMonitorEvent/SystemHealth/ProcessUtilization)[1]', 'int') AS sql_cpu,
  x.rec.value('(./Record/SchedulerMonitorEvent/SystemHealth/SystemIdle)[1]', 'int') AS idle
FROM (SELECT [timestamp], CONVERT(xml, record) AS rec
      FROM sys.dm_os_ring_buffers
      WHERE ring_buffer_type = N'RING_BUFFER_SCHEDULER_MONITOR' AND record LIKE N'%<SystemHealth>%') AS x
ORDER BY x.[timestamp] DESC`

const qWaits = `SELECT TOP 8 wait_type, wait_time_ms, waiting_tasks_count AS tasks,
  100.0 * wait_time_ms / NULLIF(SUM(CAST(wait_time_ms AS float)) OVER (), 0) AS pct
FROM sys.dm_os_wait_stats
WHERE wait_time_ms > 0
  AND wait_type NOT LIKE N'SLEEP%' AND wait_type NOT LIKE N'XE%' AND wait_type NOT LIKE N'BROKER%' AND wait_type NOT LIKE N'HADR%'
  AND wait_type NOT LIKE N'QDS%' AND wait_type NOT LIKE N'SQLTRACE%' AND wait_type NOT LIKE N'FT_%' AND wait_type NOT LIKE N'WAIT_XTP%'
  AND wait_type NOT IN (N'CLR_SEMAPHORE', N'LAZYWRITER_SLEEP', N'RESOURCE_QUEUE', N'WAITFOR', N'CHECKPOINT_QUEUE', N'REQUEST_FOR_DEADLOCK_SEARCH',
    N'LOGMGR_QUEUE', N'DIRTY_PAGE_POLL', N'ONDEMAND_TASK_QUEUE', N'SP_SERVER_DIAGNOSTICS_SLEEP', N'DISPATCHER_QUEUE_SEMAPHORE',
    N'PWAIT_ALL_COMPONENTS_INITIALIZED', N'SOS_WORK_DISPATCHER', N'REDO_THREAD_PENDING_WORK', N'WAIT_FOR_RESULTS', N'SLEEP_BPOOL_FLUSH',
    N'MEMORY_ALLOCATION_EXT', N'PARALLEL_REDO_DRAIN_WORKER', N'PARALLEL_REDO_TRAN_TURN', N'PARALLEL_REDO_WORKER_SYNC', N'PARALLEL_REDO_WORKER_WAIT_WORK',
    N'PREEMPTIVE_XE_GETTARGETSTATE', N'PVS_PREALLOCATE', N'CXCONSUMER', N'DBMIRROR_EVENTS_QUEUE', N'DBMIRRORING_CMD', N'PREEMPTIVE_OS_FLUSHFILEBUFFERS')
ORDER BY wait_time_ms DESC`

const qMemory = `SELECT m.total_physical_memory_kb / 1024 AS total_mb, m.available_physical_memory_kb / 1024 AS avail_mb, m.system_memory_state_desc AS state,
  (SELECT TOP 1 cntr_value FROM sys.dm_os_performance_counters WHERE RTRIM(counter_name) = N'Page life expectancy' AND object_name LIKE N'%Buffer Manager%') AS ple,
  (SELECT TOP 1 cntr_value FROM sys.dm_os_performance_counters WHERE RTRIM(counter_name) = N'Memory Grants Pending' AND object_name LIKE N'%Memory Manager%') AS grants_pending,
  (SELECT TOP 1 cntr_value / 1024 FROM sys.dm_os_performance_counters WHERE RTRIM(counter_name) = N'Total Server Memory (KB)') AS server_mb,
  (SELECT TOP 1 cntr_value / 1024 FROM sys.dm_os_performance_counters WHERE RTRIM(counter_name) = N'Target Server Memory (KB)') AS target_mb
FROM sys.dm_os_sys_memory AS m`

const qRequests = `SELECT TOP 15 r.session_id AS session, r.status, r.command, ISNULL(r.wait_type, N'') AS wait_type, r.wait_time AS wait_ms,
  r.blocking_session_id AS blocked_by, r.total_elapsed_time / 1000 AS secs, ISNULL(DB_NAME(r.database_id), N'') AS db, LEFT(ISNULL(s.program_name, N''), 60) AS program
FROM sys.dm_exec_requests AS r JOIN sys.dm_exec_sessions AS s ON s.session_id = r.session_id
WHERE r.session_id <> @@SPID AND s.is_user_process = 1 AND (r.blocking_session_id > 0 OR r.total_elapsed_time > 30000)
ORDER BY r.total_elapsed_time DESC`

const qSessions = `SELECT TOP 10 ISNULL(NULLIF(program_name, N''), N'(no name)') AS program, COUNT(*) AS sessions,
  SUM(CASE WHEN status = N'sleeping' AND open_transaction_count > 0 THEN 1 ELSE 0 END) AS idle_in_tran
FROM sys.dm_exec_sessions WHERE is_user_process = 1
GROUP BY ISNULL(NULLIF(program_name, N''), N'(no name)')
ORDER BY COUNT(*) DESC`

const qTopQueries = `SELECT TOP 8 qs.execution_count AS runs,
  qs.total_elapsed_time / 1000 / NULLIF(qs.execution_count, 0) AS avg_ms,
  qs.total_elapsed_time / 1000 AS total_ms,
  qs.total_worker_time / 1000 / NULLIF(qs.execution_count, 0) AS avg_cpu_ms,
  qs.total_logical_reads / NULLIF(qs.execution_count, 0) AS avg_reads,
  ISNULL(DB_NAME(st.dbid), N'') AS db,
  LEFT(REPLACE(REPLACE(SUBSTRING(st.text, 1, 200), CHAR(13), N' '), CHAR(10), N' '), 160) AS stmt
FROM sys.dm_exec_query_stats AS qs CROSS APPLY sys.dm_exec_sql_text(qs.sql_handle) AS st
ORDER BY qs.total_elapsed_time DESC`

const qQSState = `SELECT actual_state_desc AS state FROM sys.database_query_store_options`

const qQSHours = `SELECT CONVERT(varchar(16), DATEADD(hour, DATEDIFF(hour, 0, i.start_time), 0), 120) AS hour_start,
  SUM(s.count_executions) AS runs,
  SUM(s.avg_duration * s.count_executions) / NULLIF(SUM(s.count_executions), 0) / 1000.0 AS avg_ms
FROM sys.query_store_runtime_stats AS s JOIN sys.query_store_runtime_stats_interval AS i ON i.runtime_stats_interval_id = s.runtime_stats_interval_id
WHERE i.start_time >= DATEADD(hour, -24, SYSDATETIMEOFFSET())
GROUP BY DATEADD(hour, DATEDIFF(hour, 0, i.start_time), 0)
ORDER BY 1`

const qMissingIndexes = `SELECT TOP 5 ISNULL(OBJECT_NAME(d.object_id), N'') AS tbl, ISNULL(d.equality_columns, N'') AS equality, ISNULL(d.inequality_columns, N'') AS inequality,
  CAST(s.avg_total_user_cost * s.avg_user_impact * (s.user_seeks + s.user_scans) AS bigint) AS score, s.user_seeks + s.user_scans AS uses
FROM sys.dm_db_missing_index_details AS d
JOIN sys.dm_db_missing_index_groups AS g ON g.index_handle = d.index_handle
JOIN sys.dm_db_missing_index_group_stats AS s ON s.group_handle = g.index_group_handle
WHERE d.database_id = DB_ID()
ORDER BY score DESC`

const qBackups = `SELECT d.name AS db, d.recovery_model_desc AS model,
  DATEDIFF(HOUR, MAX(CASE WHEN b.type = 'D' THEN b.backup_finish_date END), GETDATE()) AS full_hours,
  DATEDIFF(HOUR, MAX(CASE WHEN b.type = 'L' THEN b.backup_finish_date END), GETDATE()) AS log_hours
FROM sys.databases AS d LEFT JOIN msdb.dbo.backupset AS b ON b.database_name = d.name
WHERE d.database_id > 4 AND d.state = 0
GROUP BY d.name, d.recovery_model_desc`

const qSettings = `SELECT name AS db, is_auto_close_on AS auto_close, is_auto_shrink_on AS auto_shrink, page_verify_option_desc AS page_verify, state_desc AS state
FROM sys.databases WHERE database_id > 4`

const qDeadlocks = `SELECT TOP 1 cntr_value AS deadlocks FROM sys.dm_os_performance_counters
WHERE RTRIM(counter_name) = N'Number of Deadlocks/sec' AND RTRIM(instance_name) = N'_Total'`

const qAgentJobs = `SELECT TOP 10 j.name AS job, h.run_date AS run_date, h.run_time AS run_time, LEFT(ISNULL(h.message, N''), 160) AS message
FROM msdb.dbo.sysjobhistory AS h JOIN msdb.dbo.sysjobs AS j ON j.job_id = h.job_id
WHERE h.step_id = 0 AND h.run_status = 0 AND msdb.dbo.agent_datetime(h.run_date, h.run_time) >= DATEADD(hour, -24, GETDATE())
ORDER BY h.instance_id DESC`

/* --------------------------------------------------------------- evals */

func evalServer(rows []Row, env *Env) Finding {
	if len(rows) == 0 {
		return Finding{Status: Unavailable, Summary: reasonFailed}
	}
	r := rows[0]
	env.Version = text(r["version"])
	env.UptimeMin = num(r["uptime_min"])
	f := Finding{Status: OK, Columns: []string{"Version", "Edition", "Up for", "CPUs", "Memory (MB)"},
		Rows: [][]string{{env.Version, text(r["edition"]), duration(env.UptimeMin), fmtInt(num(r["cpus"])), fmtInt(num(r["memory_mb"]))}}}
	f.Summary = "SQL Server " + env.Version + ", up for " + duration(env.UptimeMin) + "."
	if env.UptimeMin < 60 {
		f.Status = Warn
		f.Summary += " It restarted less than an hour ago, so waits, query timings and other counters below only cover that short time."
	}
	return f
}

func evalCPU(rows []Row, _ *Env) Finding {
	if len(rows) == 0 {
		return Finding{Status: Note, Summary: "The server has not recorded any CPU samples yet."}
	}
	var sum, other, max float64
	hot := make([]Row, 0, len(rows))
	for _, r := range rows {
		s := num(r["sql_cpu"])
		o := 100 - s - num(r["idle"])
		if o < 0 {
			o = 0
		}
		sum += s
		other += o
		if s > max {
			max = s
		}
		hot = append(hot, Row{"at": r["at"], "sql": s, "other": o})
	}
	n := float64(len(rows))
	avg, avgOther := sum/n, other/n
	f := Finding{Status: OK, Columns: []string{"Minute", "SQL Server %", "Other programs %"}}
	sort.SliceStable(hot, func(i, j int) bool { return num(hot[i]["sql"]) > num(hot[j]["sql"]) })
	for i := 0; i < len(hot) && i < 5; i++ {
		f.Rows = append(f.Rows, []string{text(hot[i]["at"]), fmtInt(num(hot[i]["sql"])), fmtInt(num(hot[i]["other"]))})
	}
	f.Summary = fmt.Sprintf("SQL Server averaged %s%% CPU over the last %d minutes (peak %s%%); other programs averaged %s%%.", fmtInt(avg), len(rows), fmtInt(max), fmtInt(avgOther))
	switch {
	case avg >= 90:
		f.Status = Bad
	case avg >= 70:
		f.Status = Warn
	case avgOther >= 30:
		f.Status = Warn
		f.Summary += " Another program on the same machine is using a large share."
	}
	return f
}

// waitMeaning says in plain words what a wait type usually means.
func waitMeaning(w string) string {
	switch {
	case w == "ASYNC_NETWORK_IO":
		return "SQL Server is waiting for the application to read the rows it sent (slow client, or too many rows returned)."
	case strings.HasPrefix(w, "PAGEIOLATCH"):
		return "Waiting for data to be read from disk (data not in memory, or a slow disk)."
	case w == "WRITELOG":
		return "Waiting for the transaction log to be written to disk (slow log disk, or very many small commits)."
	case strings.HasPrefix(w, "LCK_M_"):
		return "Waiting for a lock held by another session (blocking)."
	case w == "RESOURCE_SEMAPHORE":
		return "Queries are waiting for memory to run (memory pressure)."
	case w == "THREADPOOL":
		return "SQL Server ran out of worker threads (too many concurrent requests, often because of blocking)."
	case w == "CXPACKET" || w == "CXCONSUMER" || strings.HasPrefix(w, "EXCHANGE"):
		return "Parallel queries waiting on each other (large scans; often benign)."
	case w == "SOS_SCHEDULER_YIELD":
		return "Queries are using the CPU and yielding it (CPU pressure when it dominates)."
	case strings.HasPrefix(w, "PAGELATCH"):
		return "Contention on pages already in memory (hot pages, tempdb or last-page inserts)."
	case strings.HasPrefix(w, "PREEMPTIVE"):
		return "Waiting on a call outside SQL Server (Windows, linked servers, CLR)."
	case w == "IO_COMPLETION" || w == "BACKUPIO" || w == "BACKUPBUFFER":
		return "Waiting on disk I/O outside the data pages (sorting spills, backups)."
	}
	return ""
}

func evalWaits(rows []Row, env *Env) Finding {
	if len(rows) == 0 {
		return Finding{Status: Note, Summary: "No waits recorded yet."}
	}
	f := Finding{Status: OK, Columns: []string{"Wait", "Share %", "Seconds", "Meaning"}}
	for _, r := range rows {
		f.Rows = append(f.Rows, []string{text(r["wait_type"]), fmtF(num(r["pct"]), 1), fmtInt(num(r["wait_time_ms"]) / 1000), waitMeaning(text(r["wait_type"]))})
	}
	top := text(rows[0]["wait_type"])
	topPct := num(rows[0]["pct"])
	f.Summary = fmt.Sprintf("The most time since the server started (%s) was spent on %s (%s%% of all waiting).", duration(env.UptimeMin), top, fmtF(topPct, 0))
	if m := waitMeaning(top); m != "" {
		f.Summary += " " + m
	}
	f.Status = Note
	for _, r := range rows {
		w := text(r["wait_type"])
		p := num(r["pct"])
		switch {
		case w == "THREADPOOL" || w == "RESOURCE_SEMAPHORE":
			f.Status = Warn
		case p >= 30 && (w == "WRITELOG" || strings.HasPrefix(w, "PAGEIOLATCH") || strings.HasPrefix(w, "LCK_M_")):
			f.Status = Warn
		}
	}
	return f
}

func evalMemory(rows []Row, _ *Env) Finding {
	if len(rows) == 0 {
		return Finding{Status: Unavailable, Summary: reasonFailed}
	}
	r := rows[0]
	total, avail := num(r["total_mb"]), num(r["avail_mb"])
	ple, grants := num(r["ple"]), num(r["grants_pending"])
	state := text(r["state"])
	f := Finding{Status: OK, Columns: []string{"Machine MB", "Free MB", "State", "Page life (s)", "Waiting for memory"},
		Rows: [][]string{{fmtInt(total), fmtInt(avail), state, fmtInt(ple), fmtInt(grants)}}}
	f.Summary = fmt.Sprintf("%s MB free of %s MB. Data stays in memory about %s seconds.", fmtInt(avail), fmtInt(total), fmtInt(ple))
	var problems []string
	if strings.Contains(strings.ToLower(state), "low") {
		f.Status = Bad
		problems = append(problems, "Windows reports low memory")
	}
	if grants > 0 {
		if f.Status != Bad {
			f.Status = Warn
		}
		problems = append(problems, plural(int(grants), "query is", "queries are")+" waiting for memory to start")
	}
	if pct(avail, total) < 5 && f.Status == OK {
		f.Status = Warn
		problems = append(problems, "under 5% of memory is free")
	}
	if ple > 0 && ple < 300 && f.Status == OK {
		f.Status = Warn
		problems = append(problems, "data leaves the cache within 5 minutes")
	}
	if len(problems) > 0 {
		f.Summary += " " + strings.ToUpper(problems[0][:1]) + problems[0][1:] + joinMore(problems[1:]) + "."
	}
	return f
}

func joinMore(more []string) string {
	if len(more) == 0 {
		return ""
	}
	return "; " + strings.Join(more, "; ")
}

func evalRequests(rows []Row, _ *Env) Finding {
	if len(rows) == 0 {
		return Finding{Status: OK, Summary: "Nothing is blocked and nothing has run longer than 30 seconds right now."}
	}
	f := Finding{Status: Warn, Columns: []string{"Session", "Command", "Waiting on", "Wait (ms)", "Blocked by", "Seconds", "Database", "Program"}}
	blocked, worst := 0, 0.0
	for _, r := range rows {
		if num(r["blocked_by"]) > 0 {
			blocked++
			if w := num(r["wait_ms"]); w > worst {
				worst = w
			}
		}
		by := ""
		if b := num(r["blocked_by"]); b > 0 {
			by = fmtInt(b)
		}
		f.Rows = append(f.Rows, []string{fmtInt(num(r["session"])), text(r["command"]), text(r["wait_type"]), fmtInt(num(r["wait_ms"])), by, fmtInt(num(r["secs"])), text(r["db"]), text(r["program"])})
	}
	if blocked > 0 {
		f.Summary = plural(blocked, "request is", "requests are") + " blocked by another session right now."
		if worst >= 10000 {
			f.Status = Bad
			f.Summary += " The longest has waited " + fmtInt(worst/1000) + " seconds."
		}
		return f
	}
	f.Summary = plural(len(rows), "request has", "requests have") + " been running longer than 30 seconds right now."
	return f
}

func evalSessions(rows []Row, _ *Env) Finding {
	if len(rows) == 0 {
		return Finding{Status: OK, Summary: "No user connections."}
	}
	f := Finding{Status: OK, Columns: []string{"Program", "Connections", "Idle with open transaction"}}
	total, idle := 0.0, 0.0
	for _, r := range rows {
		total += num(r["sessions"])
		idle += num(r["idle_in_tran"])
		f.Rows = append(f.Rows, []string{text(r["program"]), fmtInt(num(r["sessions"])), fmtInt(num(r["idle_in_tran"]))})
	}
	f.Summary = plural(int(total), "connection", "connections") + " from the busiest programs."
	if idle > 0 {
		f.Status = Warn
		f.Summary += " " + plural(int(idle), "is", "are") + " idle but still holding a transaction open, which can block others."
	}
	return f
}

func evalTopQueries(rows []Row, _ *Env) Finding {
	if len(rows) == 0 {
		return Finding{Status: Note, Summary: "No cached query statistics yet (the server just started or the plan cache was cleared)."}
	}
	f := Finding{Status: OK, Columns: []string{"Runs", "Avg ms", "Total s", "Avg CPU ms", "Avg reads", "Database", "Statement"}}
	slow := 0
	for _, r := range rows {
		if num(r["avg_ms"]) >= 1000 && num(r["runs"]) >= 5 {
			slow++
		}
		f.Rows = append(f.Rows, []string{fmtInt(num(r["runs"])), fmtInt(num(r["avg_ms"])), fmtInt(num(r["total_ms"]) / 1000), fmtInt(num(r["avg_cpu_ms"])), fmtInt(num(r["avg_reads"])), text(r["db"]), clip(text(r["stmt"]), maxCell)})
	}
	f.Summary = "The statement using the most time ran " + fmtInt(num(rows[0]["runs"])) + " times at " + fmtInt(num(rows[0]["avg_ms"])) + " ms on average."
	if slow > 0 {
		f.Status = Warn
		f.Summary += " " + plural(slow, "statement averages", "statements average") + " over one second."
	}
	return f
}

// runQueryStore asks the connection's own database; with none chosen it says so.
func runQueryStore(ctx context.Context, q Queryer, env *Env) Finding {
	id, title := "querystore", "Query Store, last 24 hours"
	why := "Query Store keeps query timings per hour inside the database. A sudden jump in average duration marks when something got slower."
	if env.Database == "" {
		return Finding{ID: id, Title: title, Why: why, Status: Note, Summary: "Pick a database to read its Query Store."}
	}
	state, err := q.Query(ctx, qQSState)
	if err != nil {
		return unavailable(id, title, why, classify(err))
	}
	if len(state) == 0 || !strings.HasPrefix(text(state[0]["state"]), "READ_") {
		return Finding{ID: id, Title: title, Why: why, Status: Note, Summary: "Query Store is off for this database. Switching it on (a database owner decision) lets the server keep hourly query timings from then on."}
	}
	rows, err := q.Query(ctx, qQSHours)
	if err != nil {
		return unavailable(id, title, why, classify(err))
	}
	f := evalQueryStore(rows)
	f.ID, f.Title, f.Why = id, title, why
	return f
}

func evalQueryStore(rows []Row) Finding {
	if len(rows) == 0 {
		return Finding{Status: Note, Summary: "Query Store is on but has no timings for the last 24 hours."}
	}
	vals := make([]float64, 0, len(rows))
	for _, r := range rows {
		vals = append(vals, num(r["avg_ms"]))
	}
	sorted := append([]float64(nil), vals...)
	sort.Float64s(sorted)
	median := sorted[len(sorted)/2]
	f := Finding{Status: OK, Columns: []string{"Hour", "Runs", "Avg ms"}}
	var spikes []string
	for _, r := range rows {
		avg := num(r["avg_ms"])
		f.Rows = append(f.Rows, []string{text(r["hour_start"]), fmtInt(num(r["runs"])), fmtF(avg, 1)})
		if len(rows) >= 4 && avg >= 50 && avg >= 3*median {
			spikes = append(spikes, text(r["hour_start"]))
		}
	}
	f.Summary = fmt.Sprintf("Average query duration over the last %d hours: typically %s ms.", len(rows), fmtF(median, 1))
	if len(spikes) > 0 {
		f.Status = Warn
		f.Summary += " It was at least 3 times higher at " + strings.Join(spikes, ", ") + "."
	}
	return f
}

func evalMissingIndexes(rows []Row, env *Env) Finding {
	if env.Database == "" {
		return Finding{Status: Note, Summary: "Pick a database to see its missing-index suggestions."}
	}
	if len(rows) == 0 {
		return Finding{Status: OK, Summary: "The optimizer has not asked for any index in this database since the server started."}
	}
	f := Finding{Status: Note, Columns: []string{"Table", "Equality columns", "Inequality columns", "Score", "Uses"}}
	for _, r := range rows {
		if num(r["score"]) >= 100000 {
			f.Status = Warn
		}
		f.Rows = append(f.Rows, []string{text(r["tbl"]), clip(text(r["equality"]), maxCell), clip(text(r["inequality"]), maxCell), fmtInt(num(r["score"])), fmtInt(num(r["uses"]))})
	}
	f.Summary = plural(len(rows), "index suggestion", "index suggestions") + " for this database. These are hints only; check each before adding it."
	return f
}

func evalBackups(rows []Row, _ *Env) Finding {
	f := Finding{Status: OK, Columns: []string{"Database", "Recovery", "Last full backup", "Last log backup"}}
	bad, warn := 0, 0
	for _, r := range rows {
		full, hasFull := r["full_hours"]
		log, hasLog := r["log_hours"]
		fullTxt, logTxt := "never", "never"
		severity := OK
		if hasFull && full != nil {
			fullTxt = ago(num(full))
			if num(full) > 168 {
				severity = Warn
			}
		} else {
			severity = Bad
		}
		if hasLog && log != nil {
			logTxt = ago(num(log))
		}
		if text(r["model"]) == "FULL" && severity != Bad && (log == nil || num(log) > 24) {
			severity = Warn
		}
		switch severity {
		case Bad:
			bad++
		case Warn:
			warn++
		default:
			continue
		}
		if len(f.Rows) < 10 {
			f.Rows = append(f.Rows, []string{text(r["db"]), text(r["model"]), fullTxt, logTxt})
		}
	}
	switch {
	case bad > 0:
		f.Status = Bad
		f.Summary = plural(bad, "database has", "databases have") + " no full backup on record."
		if warn > 0 {
			f.Summary += " " + plural(warn, "more is", "more are") + " overdue."
		}
	case warn > 0:
		f.Status = Warn
		f.Summary = plural(warn, "database has", "databases have") + " an old full backup, or FULL recovery without a log backup in the last day."
	default:
		f.Summary = "Every user database has a full backup from the last 7 days (and a recent log backup where its recovery model needs one)."
	}
	return f
}

func ago(hours float64) string {
	if hours < 48 {
		return fmtInt(hours) + " h ago"
	}
	return fmtInt(hours/24) + " days ago"
}

func evalSettings(rows []Row, _ *Env) Finding {
	f := Finding{Status: OK, Columns: []string{"Database", "Problem"}}
	n := 0
	for _, r := range rows {
		var probs []string
		if st := text(r["state"]); st != "ONLINE" {
			probs = append(probs, "state is "+st)
			f.Status = Bad
		}
		if num(r["auto_close"]) != 0 {
			probs = append(probs, "auto-close is on")
		}
		if num(r["auto_shrink"]) != 0 {
			probs = append(probs, "auto-shrink is on")
		}
		if pv := text(r["page_verify"]); pv != "CHECKSUM" {
			probs = append(probs, "page verify is "+pv)
		}
		if len(probs) == 0 {
			continue
		}
		n++
		if f.Status == OK {
			f.Status = Warn
		}
		if len(f.Rows) < 10 {
			f.Rows = append(f.Rows, []string{text(r["db"]), strings.Join(probs, "; ")})
		}
	}
	if n == 0 {
		f.Summary = "No risky settings found on the user databases."
	} else {
		f.Summary = plural(n, "database has", "databases have") + " a risky setting."
	}
	return f
}

func evalDeadlocks(rows []Row, env *Env) Finding {
	if len(rows) == 0 {
		return Finding{Status: Unavailable, Summary: reasonNotThere}
	}
	n := num(rows[0]["deadlocks"])
	if n == 0 {
		return Finding{Status: OK, Summary: "No deadlocks since the server started (" + duration(env.UptimeMin) + " ago)."}
	}
	f := Finding{Status: Note, Summary: plural(int(n), "deadlock", "deadlocks") + " since the server started (" + duration(env.UptimeMin) + " ago)."}
	if n >= 20 {
		f.Status = Warn
		f.Summary += " That is frequent; look at which statements lock the same rows in a different order."
	}
	return f
}

func evalAgentJobs(rows []Row, _ *Env) Finding {
	if len(rows) == 0 {
		return Finding{Status: OK, Summary: "No SQL Agent job failed in the last 24 hours."}
	}
	f := Finding{Status: Warn, Columns: []string{"Job", "When", "Message"}}
	for _, r := range rows {
		f.Rows = append(f.Rows, []string{text(r["job"]), agentWhen(num(r["run_date"]), num(r["run_time"])), clip(text(r["message"]), maxCell)})
	}
	f.Summary = plural(len(rows), "job run", "job runs") + " failed in the last 24 hours."
	return f
}

// agentWhen turns msdb's integer date (yyyymmdd) and time (hhmmss) into text.
func agentWhen(date, tm float64) string {
	d, t := int(date), int(tm)
	return fmt.Sprintf("%04d-%02d-%02d %02d:%02d", d/10000, d/100%100, d%100, t/10000, t/100%100)
}
