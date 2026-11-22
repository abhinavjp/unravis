package dbhealth

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The SQL lane has four checks and each needs one grant (decision A30): server start and thread pool (VIEW SERVER [PERFORMANCE] STATE), the error log
// (VIEW ANY ERROR LOG), Query Store (VIEW DATABASE [PERFORMANCE] STATE) and Agent job history (SQLAgentReaderRole in msdb). Every query below is a fixed
// text that only reads; the only values placed in a text are datetime literals this package formatted itself.
//
// The /* unravis:... */ marker at the start of each text is how the tests tell the queries apart.

const (
	qLaneBase = `/* unravis:base */ SELECT CAST(SERVERPROPERTY('ProductVersion') AS nvarchar(64)) AS version, CAST(SERVERPROPERTY('Edition') AS nvarchar(128)) AS edition, ` +
		`CAST(ISNULL(SERVERPROPERTY('IsHadrEnabled'), -1) AS int) AS hadr, DATEDIFF(MINUTE, GETUTCDATE(), GETDATE()) AS utc_offset_min`
	qLaneServer = `/* unravis:server */ SELECT (SELECT sqlserver_start_time FROM sys.dm_os_sys_info) AS started_local, ` +
		`(SELECT ISNULL(SUM(waiting_tasks_count), 0) FROM sys.dm_os_wait_stats WHERE wait_type = 'THREADPOOL') AS threadpool_tasks`
	qLaneLogFlush = `/* unravis:errorlog:flush */ EXEC sp_readerrorlog 0, 1, N'cachestore flush'`
	qLaneLogStart = `/* unravis:errorlog:start */ EXEC sp_readerrorlog 0, 1, N'SQL Server is starting'`
	// The list of error log files. Archive 1 was last written when the current log was started, so its date is when the current log begins.
	qLaneLogEnum    = `/* unravis:errorlog:enum */ EXEC sp_enumerrorlogs 1`
	qLaneQSOptions  = `/* unravis:qs_options */ SELECT TOP 1 actual_state_desc, desired_state_desc, readonly_reason, interval_length_minutes FROM sys.database_query_store_options`
	qLaneQSFirst    = `/* unravis:qs_first */ SELECT MIN(start_time) AS first_start FROM sys.query_store_runtime_stats_interval`
	qLaneQSRange    = `/* unravis:qs_range */ SELECT MIN(start_time) AS first_start, MAX(start_time) AS last_start FROM sys.query_store_runtime_stats_interval`
	laneMinRuns     = 5   // a query counts only when it ran this often in both windows
	laneSlowFactor  = 2.0 // "at least 2 times higher"
	laneReadsFactor = 1.25
	laneNearBefore  = 15 * time.Minute // a job or backup this close before the incident start, or just after it, overlaps it
	laneNearAfter   = 5 * time.Minute
)

// laneQSMaxRows is how many query and plan rows the stats read returns. A full batch means busier queries were kept and quieter ones cut off.
const laneQSMaxRows = 3000

// A window of any length is read in slices of laneSliceLen, so no single Query Store query has to scan more than a few days of intervals and each
// can be stopped on its own (laneQueryTimeout). A window no longer than one slice is read by exactly one stats query and one intervals query.
const (
	laneSliceLen     = 72 * time.Hour
	laneQueryTimeout = 60 * time.Second
)

// laneSlices cuts a window into consecutive spans of at most laneSliceLen.
func laneSlices(w Window) []Window {
	var out []Window
	for s := w.Start; s.Before(w.End); s = s.Add(laneSliceLen) {
		out = append(out, Window{Start: s, End: minTime(s.Add(laneSliceLen), w.End)})
	}
	return out
}

func minTime(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
}

// laneBatches pairs the n-th slice of the reference window with the n-th slice of the incident window, so each batch reads both windows a little.
func laneBatches(req LaneRequest) [][]Window {
	ref, inc := laneSlices(req.Reference), laneSlices(req.Incident)
	n := max(len(ref), len(inc))
	out := make([][]Window, 0, n)
	for i := 0; i < n; i++ {
		var spans []Window
		if i < len(ref) {
			spans = append(spans, ref[i])
		}
		if i < len(inc) {
			spans = append(spans, inc[i])
		}
		out = append(out, spans)
	}
	return out
}

// clipBatches drops the spans that cannot hold an interval starting in [lo, hi], and the batches left empty. The spans themselves are never
// rewritten, so every datetime literal in a query still comes from the request, never from a value the database returned.
func clipBatches(batches [][]Window, lo, hi time.Time) [][]Window {
	out := make([][]Window, 0, len(batches))
	for _, spans := range batches {
		var kept []Window
		for _, w := range spans {
			if w.End.After(lo) && !w.Start.After(hi) {
				kept = append(kept, w)
			}
		}
		if len(kept) > 0 {
			out = append(out, kept)
		}
	}
	return out
}

// qsSpans is the WHERE condition that keeps the intervals starting inside any of the spans.
func qsSpans(spans []Window) string {
	parts := make([]string, 0, len(spans))
	for _, w := range spans {
		parts = append(parts, `(i.start_time >= `+sqlOffsetLit(w.Start)+` AND i.start_time < `+sqlOffsetLit(w.End)+`)`)
	}
	return strings.Join(parts, ` OR `)
}

func qLaneQSStats(req LaneRequest, spans []Window) string {
	incStart := sqlOffsetLit(req.Incident.Start)
	return `/* unravis:qs_stats */ SELECT TOP ` + fmt.Sprint(laneQSMaxRows) + ` q.query_id, p.plan_id, ` +
		`CASE WHEN i.start_time >= ` + incStart + ` THEN 'inc' ELSE 'ref' END AS win, ` +
		`SUM(rs.count_executions) AS execs, ` +
		`SUM(rs.avg_duration * rs.count_executions) / NULLIF(SUM(rs.count_executions), 0) / 1000.0 AS avg_ms, ` +
		`SUM(rs.avg_logical_io_reads * rs.count_executions) / NULLIF(SUM(rs.count_executions), 0) AS avg_reads ` +
		`FROM sys.query_store_runtime_stats rs ` +
		`JOIN sys.query_store_runtime_stats_interval i ON i.runtime_stats_interval_id = rs.runtime_stats_interval_id ` +
		`JOIN sys.query_store_plan p ON p.plan_id = rs.plan_id JOIN sys.query_store_query q ON q.query_id = p.query_id ` +
		`WHERE ` + qsSpans(spans) + ` ` +
		`GROUP BY q.query_id, p.plan_id, CASE WHEN i.start_time >= ` + incStart + ` THEN 'inc' ELSE 'ref' END ` +
		`ORDER BY SUM(rs.count_executions) DESC`
}

func qLaneQSIntervals(req LaneRequest, spans []Window) string {
	incStart := sqlOffsetLit(req.Incident.Start)
	return `/* unravis:qs_intervals */ SELECT i.start_time, SUM(rs.count_executions) AS execs, ` +
		`SUM(rs.avg_duration * rs.count_executions) / NULLIF(SUM(rs.count_executions), 0) / 1000.0 AS avg_ms, ` +
		`SUM(rs.avg_duration * rs.count_executions) / 1000.0 AS sum_ms, ` +
		`MAX(rs.max_duration) / 1000.0 AS max_ms, ` +
		`CASE WHEN i.start_time >= ` + incStart + ` THEN 'inc' ELSE 'ref' END AS win ` +
		`FROM sys.query_store_runtime_stats rs JOIN sys.query_store_runtime_stats_interval i ON i.runtime_stats_interval_id = rs.runtime_stats_interval_id ` +
		`WHERE ` + qsSpans(spans) + ` ` +
		`GROUP BY i.start_time ORDER BY i.start_time`
}

func qLaneJobs(req LaneRequest, offsetMin float64) string {
	off := time.Duration(offsetMin) * time.Minute
	return `/* unravis:agentjobs */ SELECT j.name, msdb.dbo.agent_datetime(h.run_date, h.run_time) AS start_local, h.run_duration AS hhmmss ` +
		`FROM msdb.dbo.sysjobhistory h JOIN msdb.dbo.sysjobs j ON j.job_id = h.job_id ` +
		`WHERE h.step_id = 0 AND msdb.dbo.agent_datetime(h.run_date, h.run_time) >= ` + sqlLit(req.Incident.Start.Add(-6*time.Hour).Add(off)) +
		` AND msdb.dbo.agent_datetime(h.run_date, h.run_time) < ` + sqlLit(req.Incident.Start.Add(laneNearAfter).Add(off)) + ` ORDER BY start_local`
}

func qLaneBackups(req LaneRequest, offsetMin float64) string {
	off := time.Duration(offsetMin) * time.Minute
	return `/* unravis:backups */ SELECT database_name, backup_start_date AS start_local, backup_finish_date AS end_local ` +
		`FROM msdb.dbo.backupset WHERE backup_start_date >= ` + sqlLit(req.Incident.Start.Add(-6*time.Hour).Add(off)) +
		` AND backup_start_date < ` + sqlLit(req.Incident.Start.Add(laneNearAfter).Add(off)) + ` ORDER BY backup_start_date`
}

// QueryStoreOnScript is the lines that switch Query Store on, for the owner of the database (walkthrough 9.3, step 2).
func QueryStoreOnScript(database string) string {
	d := identOr(database, "ApiDb")
	return "-- Run by the database owner. Unravis never runs this for you.\nALTER DATABASE " + d + " SET QUERY_STORE = ON (\n" +
		"  OPERATION_MODE = READ_WRITE, INTERVAL_LENGTH_MINUTES = 15, DATA_FLUSH_INTERVAL_SECONDS = 900,\n" +
		"  MAX_STORAGE_SIZE_MB = 2048, QUERY_CAPTURE_MODE = AUTO, SIZE_BASED_CLEANUP_MODE = AUTO,\n" +
		"  CLEANUP_POLICY = (STALE_QUERY_THRESHOLD_DAYS = 30), WAIT_STATS_CAPTURE_MODE = ON, MAX_PLANS_PER_QUERY = 200);\n"
}

func utcText(t time.Time) string { return t.UTC().Format("2006-01-02 15:04") + " UTC" }

type baseInfo struct {
	ok        bool
	version   string
	edition   string
	hadr      int // -1 when the server does not say
	offsetMin float64
}

func evalBase(rows []Row, err error) baseInfo {
	if err != nil || len(rows) == 0 {
		return baseInfo{hadr: -1}
	}
	r := rows[0]
	return baseInfo{ok: true, version: text(r["version"]), edition: text(r["edition"]), hadr: int(num(r["hadr"])), offsetMin: num(r["utc_offset_min"])}
}

type sqlLane struct {
	q    Queryer
	req  LaneRequest
	base baseInfo
	era  SQLEra
	b    *laneBuilder

	serverReady, logReady bool
	logCovers             bool
	start                 time.Time
	hasStart              bool
	flushes, starts       int
}

// RunSQLLane runs the four SQL checks for one incident and returns them as a report whose Lane holds the answers. q must be scoped to req.Database
// (it may be empty, which only the Query Store check minds). A cancelled context stops the run: the checks not yet run are "Failed".
// A request Validate refuses runs no query at all: every check is "Failed" with the reason (BL-042).
func RunSQLLane(ctx context.Context, q Queryer, label string, req LaneRequest, now clock) Report {
	started := now()
	s := &sqlLane{q: q, req: req, b: newLaneBuilder(req)}
	invalid := req.Validate()
	if invalid == nil {
		bctx, cancel := context.WithTimeout(ctx, checkTimeout)
		rows, err := q.Query(bctx, qLaneBase)
		cancel()
		s.base = evalBase(rows, err)
	}
	s.era = EraOf(s.base.version)
	s.b.res.Era = string(s.era)

	type step struct {
		id, title string
		feeds     []string
		run       func(context.Context) LaneCheck
	}
	steps := []step{
		{CheckServer, "Server start and thread pool", []string{"D5", "D12", "D13"}, s.checkServer},
		{CheckErrorLog, "Error log", []string{"D5"}, s.checkErrorLog},
		{CheckQueryStore, "Query Store", []string{"D3", "D14", "D15"}, s.checkQueryStore},
		{CheckAgentJobs, "Agent jobs and backups", []string{"D11"}, s.checkAgentJobs},
	}
	for _, st := range steps {
		var c LaneCheck
		if invalid != nil {
			c = refusedCheck(invalid)
		} else if ctx.Err() != nil {
			c = LaneCheck{State: StateFailed, Summary: "The run stopped before this check ran."}
		} else {
			// Query Store reads a long window slice by slice and gives each query its own time limit.
			cctx, cancel := ctx, context.CancelFunc(func() {})
			if st.id != CheckQueryStore {
				cctx, cancel = context.WithTimeout(ctx, checkTimeout)
			}
			c = st.run(cctx)
			cancel()
		}
		c.ID, c.Title, c.Feeds = st.id, st.title, st.feeds
		if c.State == StateReady {
			s.b.source("SQL Server: " + st.title)
		}
		s.b.add(c)
	}
	if invalid == nil {
		s.coldCache()
	}
	return reportFromLane("sql", label, req, s.base.version, s.b.finish(), started, now)
}

// failed builds the outcome of a query that did not run: the cause decides between "Cannot test yet" (with the fix) and "Failed".
func (s *sqlLane) failed(id string, err error, database string) LaneCheck {
	reason := classify(err)
	c := LaneCheck{State: notYetOrFailed(reason), Summary: reason}
	if reason == reasonPermission {
		fix := SQLCheckGrant(id, s.era, s.req.Login, database)
		if fix.Script != "" {
			c.Fix = &fix
		}
		c.Summary = "Cannot read this yet: the login is missing " + fix.Permission + ". The script below, run by the database owner, adds it."
	}
	return c
}

func (s *sqlLane) needBase(id string) (LaneCheck, bool) {
	if s.base.ok {
		return LaneCheck{}, true
	}
	return LaneCheck{State: StateFailed, Summary: "The server did not answer its version and time zone, so times cannot be placed."}, false
}

/* ----------------------------------------------------------------- server */

type serverOutcome struct {
	check   LaneCheck
	start   time.Time
	hasTime bool
	answers map[string]string
}

func evalLaneServer(rows []Row, base baseInfo, req LaneRequest) serverOutcome {
	out := serverOutcome{answers: map[string]string{}}
	if len(rows) == 0 {
		out.check = LaneCheck{State: StateFailed, Summary: "The server returned no start time."}
		return out
	}
	r := rows[0]
	t, ok := rowTime(r["started_local"])
	if !ok {
		out.check = LaneCheck{State: StateFailed, Summary: "The server returned no start time."}
		return out
	}
	out.start, out.hasTime = wall(t, base.offsetMin), true
	span := Window{Start: req.Reference.Start, End: req.Incident.End}
	var parts []string
	parts = append(parts, "SQL Server started at "+utcText(out.start)+".")
	if span.Contains(out.start) {
		parts = append(parts, "That is inside the reference and incident windows, so its caches were cold.")
	} else if out.start.After(span.End) || out.start.Equal(span.End) {
		parts = append(parts, "That is after the incident, so the counters here do not describe it.")
	} else {
		parts = append(parts, "That is before the reference window began.")
	}
	if base.hadr == 0 {
		out.answers["D12"] = AnswerNo
		parts = append(parts, "No availability group is set up.")
	} else if base.hadr == 1 {
		parts = append(parts, "An availability group is set up; its commit delay is not read here.")
	}
	// The wait counters are totals since the start, so they can rule threads out (never waited) but cannot place a wait inside the window.
	// They only describe the windows when the server was already running before the reference window began: after a restart inside the
	// span, the counters miss everything that happened before it.
	if out.start.Before(span.Start) {
		if num(r["threadpool_tasks"]) == 0 {
			out.answers["D13"] = AnswerNo
			parts = append(parts, "No task has waited for a worker thread since the start.")
		} else {
			parts = append(parts, plural(int(num(r["threadpool_tasks"])), "task has", "tasks have")+" waited for a worker thread since the start; the total cannot say when.")
		}
	}
	out.check = LaneCheck{State: StateReady, Summary: strings.Join(parts, " ")}
	return out
}

func (s *sqlLane) checkServer(ctx context.Context) LaneCheck {
	if c, ok := s.needBase(CheckServer); !ok {
		return c
	}
	rows, err := s.q.Query(ctx, qLaneServer)
	if err != nil {
		return s.failed(CheckServer, err, "")
	}
	o := evalLaneServer(rows, s.base, s.req)
	if o.check.State == StateReady {
		s.serverReady, s.start, s.hasStart = true, o.start, o.hasTime
		for id, v := range o.answers {
			s.b.answer(id, v)
		}
	}
	return o.check
}

/* -------------------------------------------------------------- error log */

type logOutcome struct {
	check           LaneCheck
	flushes, starts int
	// covers is true when the current log reaches back to the start of the reference window, so that finding nothing in it rules something out.
	covers bool
}

// logBegins reads when the current error log was started from the file list. known is false when the list could not be read or has no usable
// date. A server whose log was never cycled has no archive 1: the log reaches back to the install, so it covers any window.
func logBegins(enum []Row, base baseInfo) (begins time.Time, never, known bool) {
	for _, r := range enum {
		if num(r["Archive #"]) != 1 {
			continue
		}
		t, ok := rowTime(r["Date"])
		if !ok {
			return time.Time{}, false, false
		}
		return wall(t, base.offsetMin), false, true
	}
	return time.Time{}, true, true
}

func countLog(rows []Row, base baseInfo, span Window) int {
	n := 0
	for _, r := range rows {
		if t, ok := rowTime(r["LogDate"]); ok && span.Contains(wall(t, base.offsetMin)) {
			n++
		}
	}
	return n
}

func evalLaneErrorLog(flush, start []Row, enum []Row, enumErr bool, base baseInfo, req LaneRequest) logOutcome {
	span := Window{Start: req.Reference.Start, End: req.Incident.End}
	o := logOutcome{flushes: countLog(flush, base, span), starts: countLog(start, base, span)}
	begins, never, known := logBegins(enum, base)
	o.covers = known && !enumErr && (never || !begins.After(req.Reference.Start))
	switch {
	case o.flushes == 0 && o.starts == 0 && !o.covers:
		why := "when the current error log begins could not be read"
		if known && !enumErr {
			why = "the current log only begins at " + utcText(begins) + ", after the reference window began"
		}
		o.check = LaneCheck{State: StateReady, Summary: "The current error log shows no restart and no plan-cache clear in the part it holds, but " + why + ", so an older restart or clear may have been cycled out of it. This does not rule one out."}
	case o.flushes == 0 && o.starts == 0:
		o.check = LaneCheck{State: StateReady, Summary: "The current error log shows no restart and no plan-cache clear between the start of the reference window and the end of the incident."}
	default:
		var parts []string
		if o.starts > 0 {
			parts = append(parts, plural(o.starts, "start-up line", "start-up lines"))
		}
		if o.flushes > 0 {
			parts = append(parts, plural(o.flushes, "plan-cache clear", "plan-cache clears"))
		}
		o.check = LaneCheck{State: StateReady, Summary: "The error log shows " + strings.Join(parts, " and ") + " between the start of the reference window and the end of the incident."}
	}
	return o
}

func (s *sqlLane) checkErrorLog(ctx context.Context) LaneCheck {
	if c, ok := s.needBase(CheckErrorLog); !ok {
		return c
	}
	flush, err := s.q.Query(ctx, qLaneLogFlush)
	if err != nil {
		return s.failed(CheckErrorLog, err, "")
	}
	start, err := s.q.Query(ctx, qLaneLogStart)
	if err != nil {
		return s.failed(CheckErrorLog, err, "")
	}
	// The file list only tells how far back the log goes. If it cannot be read, a quiet log is still reported but never rules anything out.
	enum, enumErr := s.q.Query(ctx, qLaneLogEnum)
	o := evalLaneErrorLog(flush, start, enum, enumErr != nil, s.base, s.req)
	s.logReady, s.logCovers, s.flushes, s.starts = true, o.covers, o.flushes, o.starts
	return o.check
}

// coldCache settles D5 from the two checks that touch it. A restart or a plan-cache clear in the span is a yes. A no needs both checks: the
// server started before the reference window and the error log is readable and shows neither.
func (s *sqlLane) coldCache() {
	span := Window{Start: s.req.Reference.Start, End: s.req.Incident.End}
	if s.logReady && (s.flushes > 0 || s.starts > 0) {
		s.b.answer("D5", AnswerYes)
	}
	if s.serverReady && s.hasStart {
		if span.Contains(s.start) {
			s.b.answer("D5", AnswerYes)
		} else if s.start.Before(span.Start) && s.logReady && s.logCovers && s.flushes == 0 && s.starts == 0 {
			s.b.answer("D5", AnswerNo)
		}
	}
}

/* ------------------------------------------------------------ Query Store */

type qsOptions struct {
	state, desired, reason string
	intervalMin            float64
	found                  bool
}

func evalQSOptions(rows []Row) qsOptions {
	if len(rows) == 0 {
		return qsOptions{}
	}
	r := rows[0]
	return qsOptions{state: text(r["actual_state_desc"]), desired: text(r["desired_state_desc"]), reason: text(r["readonly_reason"]), intervalMin: num(r["interval_length_minutes"]), found: true}
}

type qsAgg struct {
	plans            map[int64]bool
	execs, ms, reads float64
}

func qsGroup(rows []Row, win string) map[int64]*qsAgg {
	out := map[int64]*qsAgg{}
	for _, r := range rows {
		if text(r["win"]) != win {
			continue
		}
		id := int64(num(r["query_id"]))
		a := out[id]
		if a == nil {
			a = &qsAgg{plans: map[int64]bool{}}
			out[id] = a
		}
		n := num(r["execs"])
		a.plans[int64(num(r["plan_id"]))] = true
		a.execs += n
		a.ms += num(r["avg_ms"]) * n
		a.reads += num(r["avg_reads"]) * n
	}
	return out
}

type qsOutcome struct {
	check   LaneCheck
	answers map[string]string
	onset   *time.Time
}

// evalLaneQueryStore compares the two windows. Plans are compared per query: a plan seen only in the incident, together with a duration at least
// twice the reference, is a regression (D3); the same plans with reads and duration both at least twice as high is growth (D15).
func evalLaneQueryStore(opt qsOptions, first time.Time, hasFirst bool, last time.Time, hasLast bool, stats, intervals []Row, truncated bool, req LaneRequest) qsOutcome {
	out := qsOutcome{answers: map[string]string{}}
	ref, inc := qsGroup(stats, "ref"), qsGroup(stats, "inc")
	off := opt.state == "OFF" && hasFirst && hasLast
	if len(inc) == 0 && off {
		out.check = LaneCheck{State: StateNotYet, Summary: offRangeNone(first, last, "the incident window")}
		return out
	}
	if len(inc) == 0 {
		msg := "Query Store holds nothing from the incident window (it was off then, or its history was cleaned up)."
		if hasFirst && !first.Before(req.Incident.End) {
			msg = "Query Store was switched on at " + utcText(first) + ", after the incident ended, so it holds nothing from it."
		}
		out.check = LaneCheck{State: StateNotYet, Summary: msg}
		return out
	}
	var summary []string
	// An off database kept blocks up to some moment. Unless the newest block starts at or after the incident end, it may have been switched off
	// during the incident: a missing change is then not evidence of none, so no "no" is given. It is the block start that counts, not its end,
	// because the last block of an off (forced) database may be partial.
	// A read-only database is the same: it stopped recording at a moment nobody knows, so without a block at or after the incident end no "no" is given.
	covered := hasLast && !last.Before(req.Incident.End)
	limited := off || opt.state == "READ_ONLY"
	if off {
		summary = append(summary, offLead(first, last))
		if !covered {
			summary = append(summary, offNotCovered)
		}
	}
	if hasFirst {
		if first.After(req.Reference.Start) {
			// The oldest kept interval is when it was switched on or when older history was cleaned up; the two cannot be told apart here.
			summary = append(summary, "Query Store's oldest kept history starts at "+utcText(first)+", after the reference window began. That is either when it was switched on or when older history was cleaned up, so this stays open.")
		} else {
			out.answers["D14"] = AnswerNo
			summary = append(summary, "Query Store has history from before the reference window.")
		}
	}
	if opt.state == "READ_ONLY" {
		summary = append(summary, "It is read-only"+reasonSuffix(opt.reason)+", so it may have stopped recording before the incident ended.")
		if !covered {
			summary = append(summary, roNotCovered)
		}
	}
	matched, newPlans, slowNew, grown, readsUp := 0, 0, 0, 0, 0
	worst := 0.0
	for id, i := range inc {
		r := ref[id]
		if r == nil || i.execs < laneMinRuns || r.execs < laneMinRuns || r.ms <= 0 {
			continue
		}
		matched++
		ratioMs := (i.ms / i.execs) / (r.ms / r.execs)
		ratioReads := 0.0
		if r.reads > 0 {
			ratioReads = (i.reads / i.execs) / (r.reads / r.execs)
		}
		newPlan := false
		for p := range i.plans {
			if !r.plans[p] {
				newPlan = true
			}
		}
		if newPlan {
			newPlans++
			if ratioMs >= laneSlowFactor {
				slowNew++
				if ratioMs > worst {
					worst = ratioMs
				}
			}
			continue
		}
		if ratioReads >= laneReadsFactor {
			readsUp++
		}
		if ratioMs >= laneSlowFactor && ratioReads >= laneSlowFactor {
			grown++
		}
	}
	if matched == 0 {
		summary = append(summary, "No query ran "+fmt.Sprint(laneMinRuns)+" or more times in both windows, so plans cannot be compared.")
	} else {
		summary = append(summary, plural(matched, "query", "queries")+" ran "+fmt.Sprint(laneMinRuns)+" or more times in both windows.")
		if truncated {
			summary = append(summary, "The read was cut short at "+fmtInt(laneQSMaxRows)+" query and plan rows (the busiest first), so a query that is not in it may have changed plan; a yes still stands, a no is not given.")
		}
		if slowNew > 0 {
			out.answers["D3"] = AnswerYes
			summary = append(summary, plural(slowNew, "got", "got")+" a new plan and ran "+fmtF(worst, 1)+" times slower at worst.")
		} else if !truncated {
			out.answers["D3"] = AnswerNo
			summary = append(summary, "None got a new plan and ran twice as slow.")
		} else {
			summary = append(summary, "None of those read got a new plan and ran twice as slow.")
		}
		if grown > 0 {
			out.answers["D15"] = AnswerYes
			summary = append(summary, plural(grown, "kept", "kept")+" its plan but read and took at least twice as much.")
		} else if readsUp == 0 && !truncated {
			out.answers["D15"] = AnswerNo
			summary = append(summary, "Reads per run did not grow.")
		}
	}
	if opt.intervalMin > 0 && opt.intervalMin <= 15 {
		if t := qsOnset(intervals, req); t != nil {
			out.onset = t
			summary = append(summary, "Queries first ran twice as slow as the reference at "+utcText(*t)+".")
		}
	} else if opt.found {
		summary = append(summary, "Query Store keeps "+fmtInt(opt.intervalMin)+"-minute intervals, too coarse to place when it started.")
	}
	if limited && !covered {
		for id, v := range out.answers {
			if v == AnswerNo {
				delete(out.answers, id)
			}
		}
	}
	out.check = LaneCheck{State: StateReady, Summary: strings.Join(summary, " ")}
	return out
}

func reasonSuffix(reason string) string {
	r := strings.TrimSpace(strings.ToLower(strings.ReplaceAll(reason, "_", " ")))
	if r == "" || r == "none" || r == "0" {
		return ""
	}
	return " (" + clip(r, 60) + ")"
}

// seriesOf turns the Query Store interval rows into the saved series, skipping
// rows without a usable start time or any executions.
//
// A point holds an average, and the tracing lane gets a block's busy time back as
// average x executions. So that product is the real summed duration (sum_ms, which
// the server adds up before any rounding), the average is kept as the exact
// quotient sum_ms / execs; rounding it to 0.1 ms first would shift the busy time by
// up to 0.05 ms per execution. The page rounds the average when it shows it. A row
// without a usable sum (an older read, or a value that is not a number) keeps the
// average rounded to 0.1 ms.
func seriesOf(intervals []Row) []LaneSeriesPoint {
	out := make([]LaneSeriesPoint, 0, len(intervals))
	for _, r := range intervals {
		t, ok := rowTime(r["start_time"])
		if !ok {
			continue
		}
		n := num(r["execs"])
		if n <= 0 {
			continue
		}
		avg := round1(num(r["avg_ms"]))
		if sum, ok := sumMs(r["sum_ms"]); ok {
			avg = sum / n
		}
		out = append(out, LaneSeriesPoint{T: t.UTC().Unix(), Execs: int64(n), AvgMs: avg, MaxMs: round1(num(r["max_ms"]))})
	}
	return out
}

// sumMs reads the summed-duration column: false when it is missing, not a
// number, negative or infinite.
func sumMs(v any) (float64, bool) {
	var f float64
	switch x := v.(type) {
	case nil:
		return 0, false
	case string:
		p, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		if err != nil {
			return 0, false
		}
		f = p
	case []byte:
		p, err := strconv.ParseFloat(strings.TrimSpace(string(x)), 64)
		if err != nil {
			return 0, false
		}
		f = p
	default:
		f = num(v)
	}
	return f, f >= 0 && !math.IsNaN(f) && !math.IsInf(f, 0)
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }

// qsOnset returns the start of the first incident interval whose average duration is at least twice the reference's.
func qsOnset(intervals []Row, req LaneRequest) *time.Time {
	var refMs, refN float64
	type pt struct {
		at    time.Time
		avg   float64
		execs float64
	}
	var inc []pt
	for _, r := range intervals {
		t, ok := rowTime(r["start_time"])
		if !ok {
			continue
		}
		n, ms := num(r["execs"]), num(r["avg_ms"])
		switch text(r["win"]) {
		case "ref":
			refMs += ms * n
			refN += n
		case "inc":
			inc = append(inc, pt{t.UTC(), ms, n})
		}
	}
	if refN < laneMinRuns || refMs <= 0 {
		return nil
	}
	base := refMs / refN
	sort.Slice(inc, func(i, j int) bool { return inc[i].at.Before(inc[j].at) })
	for _, p := range inc {
		if p.execs >= laneMinRuns && p.avg >= laneSlowFactor*base {
			t := p.at
			return &t
		}
	}
	return nil
}

// queryStoreOf names the state Query Store reported, from the options row the check already reads. A database with no options row, or a state this
// code does not switch on, is "unknown".
func queryStoreOf(opt qsOptions) *LaneQueryStore {
	qs := &LaneQueryStore{State: QSStateUnknown}
	if !opt.found {
		return qs
	}
	switch opt.state {
	case "OFF":
		qs.State = QSStateOff
	case "READ_ONLY":
		qs.State = QSStateReadOnly
	case "READ_WRITE":
		qs.State = QSStateOn
	}
	if opt.intervalMin > 0 {
		qs.IntervalMinutes = int(opt.intervalMin)
	}
	return qs
}

// The sentences of the Query Store check when the database does not say it is on. Server strings, fixed words.
const (
	sentenceError   = "Query Store reports an error state in this database, so Unravis cannot tell what it holds. The database owner has to look at it. Unravis does not change it."
	sentenceNoRow   = "This database gave no Query Store settings, so Unravis cannot tell whether it is on or off."
	sentenceNoState = "This database gave no Query Store state, so Unravis cannot tell whether it is on or off."
	offNotCovered   = "Its newest block starts before the incident ended, so it may have been switched off during the incident. A \"no\" is not given."
	roNotCovered    = "Its newest block starts before the incident ended, so it may have stopped recording during the incident. A \"no\" is not given."
)

// qsCannotTell is the check for a Query Store state that is neither on nor off: no history is read and no switch-on script is offered.
func qsCannotTell(summary string) LaneCheck {
	return LaneCheck{State: StateNotYet, Summary: summary}
}

// qsOffNoneCheck is the check for a database that is off and kept no history: the words and the owner-run script are the ones used before
// the history of an off database was read.
func qsOffNoneCheck(database string) LaneCheck {
	fix := GrantFix{Permission: "Query Store switched on", Script: QueryStoreOnScript(database)}
	return LaneCheck{State: StateNotYet, Summary: "Query Store is switched off in this database, so it holds no plans or timings. The script below, run by the database owner, switches it on; it only helps for incidents after that.", Fix: &fix}
}

func offLead(first, last time.Time) string {
	return "Query Store is off. It kept history from " + utcText(first) + " (oldest block start) to " + utcText(last) + " (newest block start)."
}

func offRangeNone(first, last time.Time, where string) string {
	return "Query Store is off. It kept history from " + utcText(first) + " to " + utcText(last) + ", but none of it falls inside " + where + "."
}

type qsQuery func(text string, limit time.Duration) ([]Row, error)

func (s *sqlLane) checkQueryStore(ctx context.Context) LaneCheck {
	if s.req.Database == "" {
		return LaneCheck{State: StateNotYet, Summary: "Pick the application database. Query Store is read one database at a time."}
	}
	// Every query gets its own time limit, so one slow slice stops alone and a long window is not cut by a limit meant for one query.
	query := func(text string, limit time.Duration) ([]Row, error) {
		qctx, cancel := context.WithTimeout(ctx, limit)
		defer cancel()
		return s.q.Query(qctx, text)
	}
	optRows, err := query(qLaneQSOptions, checkTimeout)
	if err != nil {
		state := QSStateUnknown
		if classify(err) == reasonPermission {
			state = QSStateDenied
		}
		s.b.res.QueryStore = &LaneQueryStore{State: state}
		return s.failed(CheckQueryStore, err, s.req.Database)
	}
	opt := evalQSOptions(optRows)
	s.b.res.QueryStore = queryStoreOf(opt)
	switch {
	case !opt.found:
		return qsCannotTell(sentenceNoRow)
	case opt.state == "ERROR":
		return qsCannotTell(sentenceError)
	case opt.state == "":
		return qsCannotTell(sentenceNoState)
	case opt.state == "OFF":
		return s.checkQueryStoreOff(ctx, opt, query)
	}
	// A read-only database stopped recording at a moment this check does not know, so it also needs the start of its newest block (the same
	// range read an off database uses); an ON database keeps the plain first-block read.
	firstQ := qLaneQSFirst
	if opt.state == "READ_ONLY" {
		firstQ = qLaneQSRange
	}
	var first, last time.Time
	hasFirst, hasLast := false, false
	if rows, err := query(firstQ, checkTimeout); err == nil && len(rows) > 0 {
		first, hasFirst = rowTime(rows[0]["first_start"])
		first = first.UTC()
		if hasFirst {
			f := first
			s.b.res.QueryStore.FirstData = &f
		}
		if firstQ == qLaneQSRange {
			last, hasLast = rowTime(rows[0]["last_start"])
			last = last.UTC()
		}
	} else if err != nil {
		return s.failed(CheckQueryStore, err, s.req.Database)
	}
	batches := laneBatches(s.req)
	stats, intervals, truncated, err := s.readQSBatches(ctx, query, batches)
	if err != nil {
		return s.failed(CheckQueryStore, err, s.req.Database)
	}
	o := evalLaneQueryStore(opt, first, hasFirst, last, hasLast, stats, intervals, truncated, s.req)
	return s.finishQueryStore(o, intervals, len(batches))
}

// readQSBatches reads the stats and the intervals of each batch. The windows are read in slices. Rows of the same query and plan from different
// slices are added together by qsGroup, and the intervals of different slices never overlap, so the slices are simply joined.
func (s *sqlLane) readQSBatches(ctx context.Context, query qsQuery, batches [][]Window) (stats, intervals []Row, truncated bool, err error) {
	for _, spans := range batches {
		if err := ctx.Err(); err != nil {
			return nil, nil, false, err
		}
		rows, err := query(qLaneQSStats(s.req, spans), laneQueryTimeout)
		if err != nil {
			return nil, nil, false, err
		}
		truncated = truncated || len(rows) >= laneQSMaxRows
		stats = append(stats, rows...)
		rows, err = query(qLaneQSIntervals(s.req, spans), laneQueryTimeout)
		if err != nil {
			return nil, nil, false, err
		}
		intervals = append(intervals, rows...)
	}
	return stats, intervals, truncated, nil
}

// finishQueryStore saves what a finished read gives: the series always, and the answers and the onset only for a check that is ready.
func (s *sqlLane) finishQueryStore(o qsOutcome, intervals []Row, slices int) LaneCheck {
	if slices > 1 && o.check.State == StateReady {
		o.check.Summary += " The windows were read in " + fmtInt(float64(slices)) + " slices of up to 3 days."
	}
	s.b.setSeries(seriesOf(intervals))
	if o.check.State == StateReady {
		for id, v := range o.answers {
			s.b.answer(id, v)
		}
		if o.onset != nil {
			s.b.setOnset(*o.onset)
		}
	}
	return o.check
}

// checkQueryStoreOff reads what an off database still holds. One small range query says whether any block is kept and when the oldest and the
// newest start; only the slices of the asked windows that touch that range are then read. Nothing is switched on or changed.
func (s *sqlLane) checkQueryStoreOff(ctx context.Context, opt qsOptions, query qsQuery) LaneCheck {
	rows, err := query(qLaneQSRange, checkTimeout)
	if err != nil {
		c := s.failed(CheckQueryStore, err, s.req.Database)
		c.Summary = "Query Store is off, and the history it may have kept could not be read. " + c.Summary
		return c
	}
	if len(rows) == 0 {
		return qsOffNoneCheck(s.req.Database)
	}
	first, hasFirst := rowTime(rows[0]["first_start"])
	if !hasFirst {
		return qsOffNoneCheck(s.req.Database)
	}
	first = first.UTC()
	last, hasLast := rowTime(rows[0]["last_start"])
	if !hasLast || last.Before(first) {
		last = first
	}
	last = last.UTC()
	f, l := first, last
	s.b.res.QueryStore.FirstData = &f
	s.b.res.QueryStore.LastData = &l
	batches := clipBatches(laneBatches(s.req), first, last)
	if len(batches) == 0 {
		return LaneCheck{State: StateNotYet, Summary: offRangeNone(first, last, "the windows asked")}
	}
	stats, intervals, truncated, err := s.readQSBatches(ctx, query, batches)
	if err != nil {
		c := s.failed(CheckQueryStore, err, s.req.Database)
		c.Summary = offLead(first, last) + " " + c.Summary
		return c
	}
	o := evalLaneQueryStore(opt, first, true, last, true, stats, intervals, truncated, s.req)
	return s.finishQueryStore(o, intervals, len(batches))
}

/* ------------------------------------------------------------- Agent jobs */

func hhmmss(v float64) time.Duration {
	n := int64(v)
	return time.Duration(n/10000)*time.Hour + time.Duration((n/100)%100)*time.Minute + time.Duration(n%100)*time.Second
}

type jobHit struct {
	name       string
	start, end time.Time
	backup     bool
}

// overlaps lists the jobs and backups that ran at any moment in the stretch just before the incident started (and a few minutes after).
func overlaps(jobs, backups []Row, base baseInfo, req LaneRequest) []jobHit {
	zone := Window{Start: req.Incident.Start.Add(-laneNearBefore), End: req.Incident.Start.Add(laneNearAfter)}
	var hits []jobHit
	add := func(name string, start, end time.Time, backup bool) {
		if start.Before(zone.End) && end.After(zone.Start) {
			hits = append(hits, jobHit{clip(name, 60), start, end, backup})
		}
	}
	for _, r := range jobs {
		t, ok := rowTime(r["start_local"])
		if !ok {
			continue
		}
		st := wall(t, base.offsetMin)
		add(text(r["name"]), st, st.Add(hhmmss(num(r["hhmmss"]))), false)
	}
	for _, r := range backups {
		t, ok := rowTime(r["start_local"])
		if !ok {
			continue
		}
		st := wall(t, base.offsetMin)
		end := st
		if e, ok := rowTime(r["end_local"]); ok {
			end = wall(e, base.offsetMin)
		}
		add("Backup of "+text(r["database_name"]), st, end, true)
	}
	return hits
}

// evalLaneJobs answers D11. When the backup history could not be read (backupsRead false) a job or backup that ran is still a yes, but a no is
// never given: a backup may have run, so the question stays open and the summary says backups were not checked.
func evalLaneJobs(jobs, backups []Row, backupsRead bool, base baseInfo, req LaneRequest) (LaneCheck, map[string]string) {
	hits := overlaps(jobs, backups, base, req)
	answers := map[string]string{}
	if len(hits) == 0 {
		if !backupsRead {
			return LaneCheck{State: StateReady, Summary: "No Agent job was running in the 15 minutes before the incident started. Backup history could not be read with this login, so backups were not checked and the question stays open."}, answers
		}
		answers["D11"] = AnswerNo
		return LaneCheck{State: StateReady, Summary: "No Agent job or backup was running in the 15 minutes before the incident started."}, answers
	}
	answers["D11"] = AnswerYes
	names := make([]string, 0, len(hits))
	for i, h := range hits {
		if i == 3 {
			names = append(names, fmt.Sprintf("and %d more", len(hits)-3))
			break
		}
		names = append(names, h.name+" ("+utcText(h.start)+")")
	}
	return LaneCheck{State: StateReady, Summary: plural(len(hits), "job or backup was", "jobs or backups were") + " running just before the incident started: " + strings.Join(names, ", ") + "."}, answers
}

func (s *sqlLane) checkAgentJobs(ctx context.Context) LaneCheck {
	if c, ok := s.needBase(CheckAgentJobs); !ok {
		return c
	}
	jobs, err := s.q.Query(ctx, qLaneJobs(s.req, s.base.offsetMin))
	if err != nil {
		return s.failed(CheckAgentJobs, err, "")
	}
	// Backup history is read as well when the login can; the job history alone is enough to answer.
	backups, berr := s.q.Query(ctx, qLaneBackups(s.req, s.base.offsetMin))
	c, answers := evalLaneJobs(jobs, backups, berr == nil, s.base, s.req)
	for id, v := range answers {
		s.b.answer(id, v)
	}
	return c
}
