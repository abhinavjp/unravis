package dbhealth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

var (
	laneInc = Window{time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC), time.Date(2026, 10, 20, 11, 0, 0, 0, time.UTC)}
	laneRef = Window{time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC), time.Date(2026, 10, 19, 11, 0, 0, 0, time.UTC)}
	laneReq = LaneRequest{Incident: laneInc, Reference: laneRef, Database: "AuditDb", Login: "unravis_ro"}
)

func at(day, h, m int) time.Time { return time.Date(2026, 10, day, h, m, 0, 0, time.UTC) }

func baseRows(version string, hadr int, offset int) []Row {
	return []Row{{"version": version, "edition": "Standard", "hadr": int64(hadr), "utc_offset_min": int64(offset)}}
}

func qsRow(q, p int, win string, execs int, ms, reads float64) Row {
	return Row{"query_id": int64(q), "plan_id": int64(p), "win": win, "execs": int64(execs), "avg_ms": ms, "avg_reads": reads}
}

// healthySQL is a server where every check can run and nothing is wrong.
func healthySQL(version string) fakeQ {
	return fakeQ{by: map[string][]Row{
		"unravis:base":       baseRows(version, 0, 0),
		"unravis:server":     {{"started_local": at(1, 3, 0), "threadpool_tasks": int64(0)}},
		"unravis:qs_options": {{"actual_state_desc": "READ_WRITE", "desired_state_desc": "READ_WRITE", "readonly_reason": int64(0), "interval_length_minutes": int64(15)}},
		"unravis:qs_first":   {{"first_start": at(2, 0, 0)}},
		"unravis:qs_stats": {
			qsRow(1, 10, "ref", 50, 10, 100), qsRow(1, 10, "inc", 50, 11, 105),
		},
	}}
}

func checkByID(t *testing.T, r Report, id string) LaneCheck {
	t.Helper()
	if r.Lane == nil {
		t.Fatal("no lane result")
	}
	for _, c := range r.Lane.Checks {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("no check %q in %+v", id, r.Lane.Checks)
	return LaneCheck{}
}

func TestSQLLaneHealthyServerAnswersNo(t *testing.T) {
	r := RunSQLLane(context.Background(), healthySQL("16.0.1000.6"), "Prod", laneReq, fixedNow)
	if r.Kind != "sql" || r.Lane == nil || r.Lane.Era != "2022" {
		t.Fatalf("report = %+v", r)
	}
	if got := r.Lane.State(); got != StateReady {
		t.Fatalf("state = %s, checks %+v", got, r.Lane.Checks)
	}
	for id, want := range map[string]string{"D5": AnswerNo, "D12": AnswerNo, "D13": AnswerNo, "D3": AnswerNo, "D15": AnswerNo, "D11": AnswerNo} {
		if r.Lane.Answers[id] != want {
			t.Errorf("%s = %q, want %q (%v)", id, r.Lane.Answers[id], want, r.Lane.Answers)
		}
	}
	// Query Store's history starts on Oct 2, before the reference window, so its history is not short.
	if r.Lane.Answers["D14"] != AnswerNo {
		t.Errorf("D14 = %q", r.Lane.Answers["D14"])
	}
	if r.Overall != OK || len(r.Lane.Sources) != 4 {
		t.Errorf("overall %s sources %v", r.Overall, r.Lane.Sources)
	}
}

func TestSQLLaneServerCheckRestartInsideWindows(t *testing.T) {
	q := healthySQL("16.0.1")
	q.by["unravis:server"] = []Row{{"started_local": at(20, 9, 40), "threadpool_tasks": int64(0)}}
	r := RunSQLLane(context.Background(), q, "Prod", laneReq, fixedNow)
	if r.Lane.Answers["D5"] != AnswerYes {
		t.Fatalf("restart in the incident should be D5 yes: %v", r.Lane.Answers)
	}
	if !strings.Contains(checkByID(t, r, CheckServer).Summary, "cold") {
		t.Errorf("summary = %s", checkByID(t, r, CheckServer).Summary)
	}
}

func TestSQLLaneServerStartedAfterIncidentDoesNotAnswer(t *testing.T) {
	q := healthySQL("16.0.1")
	q.by["unravis:server"] = []Row{{"started_local": at(25, 1, 0), "threadpool_tasks": int64(0)}}
	r := RunSQLLane(context.Background(), q, "Prod", laneReq, fixedNow)
	if _, ok := r.Lane.Answers["D13"]; ok {
		t.Errorf("counters since a later start say nothing about the incident: %v", r.Lane.Answers)
	}
	if r.Lane.Answers["D5"] == AnswerNo {
		t.Errorf("a later start must not rule cold cache out: %v", r.Lane.Answers)
	}
}

func TestSQLLaneThreadPoolWaitsAreNotAPass(t *testing.T) {
	q := healthySQL("16.0.1")
	q.by["unravis:server"] = []Row{{"started_local": at(1, 3, 0), "threadpool_tasks": int64(12)}}
	r := RunSQLLane(context.Background(), q, "Prod", laneReq, fixedNow)
	if _, ok := r.Lane.Answers["D13"]; ok {
		t.Errorf("waits exist, D13 must stay unknown: %v", r.Lane.Answers)
	}
}

func TestSQLLaneAvailabilityGroupOnLeavesD12Unknown(t *testing.T) {
	q := healthySQL("16.0.1")
	q.by["unravis:base"] = baseRows("16.0.1", 1, 0)
	r := RunSQLLane(context.Background(), q, "Prod", laneReq, fixedNow)
	if _, ok := r.Lane.Answers["D12"]; ok {
		t.Errorf("D12 = %v", r.Lane.Answers)
	}
	q.by["unravis:base"] = baseRows("16.0.1", -1, 0)
	r = RunSQLLane(context.Background(), q, "Prod", laneReq, fixedNow)
	if _, ok := r.Lane.Answers["D12"]; ok {
		t.Errorf("unknown hadr must not be no: %v", r.Lane.Answers)
	}
}

func TestSQLLaneErrorLogShowsRestartAndFlush(t *testing.T) {
	q := healthySQL("16.0.1")
	q.by["unravis:errorlog:flush"] = []Row{{"LogDate": at(20, 10, 30)}, {"LogDate": at(5, 1, 0)}}
	r := RunSQLLane(context.Background(), q, "Prod", laneReq, fixedNow)
	c := checkByID(t, r, CheckErrorLog)
	if c.State != StateReady || !strings.Contains(c.Summary, "1 plan-cache clear") {
		t.Fatalf("errorlog = %+v", c)
	}
	if r.Lane.Answers["D5"] != AnswerYes {
		t.Errorf("a flush in the incident is D5 yes: %v", r.Lane.Answers)
	}
	q = healthySQL("16.0.1")
	q.by["unravis:errorlog:start"] = []Row{{"LogDate": at(19, 10, 5)}}
	r = RunSQLLane(context.Background(), q, "Prod", laneReq, fixedNow)
	if r.Lane.Answers["D5"] != AnswerYes || !strings.Contains(checkByID(t, r, CheckErrorLog).Summary, "start-up") {
		t.Errorf("start-up line in the reference window: %v / %s", r.Lane.Answers, checkByID(t, r, CheckErrorLog).Summary)
	}
}

func TestSQLLaneTimesUseServerOffset(t *testing.T) {
	// Server clock is UTC+5:30. A start at 15:40 local is 10:10 UTC, inside the incident.
	q := healthySQL("16.0.1")
	q.by["unravis:base"] = baseRows("16.0.1", 0, 330)
	q.by["unravis:server"] = []Row{{"started_local": at(20, 15, 40), "threadpool_tasks": int64(0)}}
	r := RunSQLLane(context.Background(), q, "Prod", laneReq, fixedNow)
	if r.Lane.Answers["D5"] != AnswerYes || !strings.Contains(checkByID(t, r, CheckServer).Summary, "2026-10-20 10:10 UTC") {
		t.Fatalf("offset not applied: %s", checkByID(t, r, CheckServer).Summary)
	}
}

func TestSQLLaneQueryStoreFindsRegressionAndOnset(t *testing.T) {
	q := healthySQL("16.0.1")
	q.by["unravis:qs_stats"] = []Row{
		qsRow(1, 10, "ref", 60, 10, 100),
		qsRow(1, 11, "inc", 60, 45, 100), // new plan, 4.5 times slower: D3 yes
		qsRow(2, 20, "ref", 40, 5, 50),
		qsRow(2, 20, "inc", 40, 12, 150), // same plan, slower and reading 3 times more: D15 yes
	}
	q.by["unravis:qs_intervals"] = []Row{
		{"start_time": at(19, 10, 0), "execs": int64(50), "avg_ms": 10.0, "win": "ref"},
		{"start_time": at(20, 10, 0), "execs": int64(50), "avg_ms": 11.0, "win": "inc"},
		{"start_time": at(20, 10, 15), "execs": int64(50), "avg_ms": 30.0, "win": "inc"},
		{"start_time": at(20, 10, 30), "execs": int64(50), "avg_ms": 40.0, "win": "inc"},
	}
	r := RunSQLLane(context.Background(), q, "Prod", laneReq, fixedNow)
	if r.Lane.Answers["D3"] != AnswerYes || r.Lane.Answers["D15"] != AnswerYes {
		t.Fatalf("answers = %v; %s", r.Lane.Answers, checkByID(t, r, CheckQueryStore).Summary)
	}
	if r.Lane.Onset == nil || !r.Lane.Onset.Equal(at(20, 10, 15)) {
		t.Fatalf("onset = %v", r.Lane.Onset)
	}
}

func TestSQLLaneQueryStoreFewRunsStaysUnknown(t *testing.T) {
	q := healthySQL("16.0.1")
	q.by["unravis:qs_stats"] = []Row{qsRow(1, 10, "ref", 2, 10, 100), qsRow(1, 11, "inc", 3, 90, 100)}
	r := RunSQLLane(context.Background(), q, "Prod", laneReq, fixedNow)
	if _, ok := r.Lane.Answers["D3"]; ok {
		t.Errorf("too few runs to compare: %v", r.Lane.Answers)
	}
	if checkByID(t, r, CheckQueryStore).State != StateReady {
		t.Errorf("state = %s", checkByID(t, r, CheckQueryStore).State)
	}
}

func TestSQLLaneQueryStoreHistoryStartedLateIsD14(t *testing.T) {
	q := healthySQL("16.0.1")
	q.by["unravis:qs_first"] = []Row{{"first_start": at(19, 18, 0)}}
	r := RunSQLLane(context.Background(), q, "Prod", laneReq, fixedNow)
	if r.Lane.Answers["D14"] != AnswerYes {
		t.Fatalf("D14 = %v", r.Lane.Answers)
	}
	q.by["unravis:qs_first"] = []Row{{"first_start": at(1, 0, 0)}}
	r = RunSQLLane(context.Background(), q, "Prod", laneReq, fixedNow)
	if r.Lane.Answers["D14"] != AnswerNo {
		t.Fatalf("D14 = %v", r.Lane.Answers)
	}
}

func TestSQLLaneQueryStoreOffGivesFix(t *testing.T) {
	q := healthySQL("16.0.1")
	q.by["unravis:qs_options"] = []Row{{"actual_state_desc": "OFF", "desired_state_desc": "OFF", "interval_length_minutes": int64(60)}}
	r := RunSQLLane(context.Background(), q, "Prod", laneReq, fixedNow)
	c := checkByID(t, r, CheckQueryStore)
	if c.State != StateNotYet || c.Fix == nil || !strings.Contains(c.Fix.Script, "SET QUERY_STORE = ON") || !strings.Contains(c.Fix.Script, "[AuditDb]") {
		t.Fatalf("query store off = %+v", c)
	}
	for _, id := range []string{"D3", "D14", "D15"} {
		if _, ok := r.Lane.Answers[id]; ok {
			t.Errorf("%s answered although the check could not run", id)
		}
	}
	// Empty options (no row at all) is the same as off.
	q.by["unravis:qs_options"] = nil
	r = RunSQLLane(context.Background(), q, "Prod", laneReq, fixedNow)
	if checkByID(t, r, CheckQueryStore).State != StateNotYet {
		t.Errorf("no options row should be cannot test yet")
	}
}

func TestSQLLaneQueryStoreEmptyIncidentWindow(t *testing.T) {
	q := healthySQL("16.0.1")
	q.by["unravis:qs_stats"] = []Row{qsRow(1, 10, "ref", 50, 10, 100)}
	q.by["unravis:qs_first"] = []Row{{"first_start": at(22, 0, 0)}}
	r := RunSQLLane(context.Background(), q, "Prod", laneReq, fixedNow)
	c := checkByID(t, r, CheckQueryStore)
	if c.State != StateNotYet || !strings.Contains(c.Summary, "after the incident ended") {
		t.Fatalf("check = %+v", c)
	}
}

func TestSQLLaneQueryStoreNeedsADatabase(t *testing.T) {
	req := laneReq
	req.Database = ""
	r := RunSQLLane(context.Background(), healthySQL("16.0.1"), "Prod", req, fixedNow)
	if checkByID(t, r, CheckQueryStore).State != StateNotYet {
		t.Errorf("no database should be cannot test yet")
	}
}

func TestSQLLaneAgentJobOverlap(t *testing.T) {
	q := healthySQL("16.0.1")
	// A nightly job from 09:50 for 20 minutes (hhmmss 2000) overlaps the 10:00 start.
	q.by["unravis:agentjobs"] = []Row{{"name": "Nightly reindex", "start_local": at(20, 9, 50), "hhmmss": int64(2000)}}
	r := RunSQLLane(context.Background(), q, "Prod", laneReq, fixedNow)
	if r.Lane.Answers["D11"] != AnswerYes || !strings.Contains(checkByID(t, r, CheckAgentJobs).Summary, "Nightly reindex") {
		t.Fatalf("D11 = %v / %s", r.Lane.Answers, checkByID(t, r, CheckAgentJobs).Summary)
	}
	// A job that finished an hour earlier does not count.
	q.by["unravis:agentjobs"] = []Row{{"name": "Early", "start_local": at(20, 8, 0), "hhmmss": int64(1000)}}
	r = RunSQLLane(context.Background(), q, "Prod", laneReq, fixedNow)
	if r.Lane.Answers["D11"] != AnswerNo {
		t.Errorf("D11 = %v", r.Lane.Answers)
	}
	// A backup running at the start counts as well.
	q.by["unravis:backups"] = []Row{{"database_name": "AuditDb", "start_local": at(20, 9, 55), "end_local": at(20, 10, 20)}}
	r = RunSQLLane(context.Background(), q, "Prod", laneReq, fixedNow)
	if r.Lane.Answers["D11"] != AnswerYes || !strings.Contains(checkByID(t, r, CheckAgentJobs).Summary, "Backup of AuditDb") {
		t.Errorf("backup overlap: %v / %s", r.Lane.Answers, checkByID(t, r, CheckAgentJobs).Summary)
	}
}

func TestHHMMSS(t *testing.T) {
	for in, want := range map[float64]time.Duration{0: 0, 45: 45 * time.Second, 130: 90 * time.Second, 10203: time.Hour + 2*time.Minute + 3*time.Second} {
		if got := hhmmss(in); got != want {
			t.Errorf("hhmmss(%v) = %v want %v", in, got, want)
		}
	}
}

// A login with no grants: every check says Cannot test yet, shows the one grant it needs, and no catalog entry is answered.
func TestSQLLaneMissingGrantsGiveFixesPerEra(t *testing.T) {
	denied := errors.New("The user does not have permission to perform this action.")
	for _, tc := range []struct {
		version string
		era     string
		want    map[string]string // check id -> permission text expected in the fix
	}{
		{"16.0.1000.6", "2022", map[string]string{
			CheckServer: "VIEW SERVER PERFORMANCE STATE", CheckErrorLog: "VIEW ANY ERROR LOG",
			CheckQueryStore: "VIEW DATABASE PERFORMANCE STATE", CheckAgentJobs: "SQLAgentReaderRole",
		}},
		{"15.0.2000.5", "2019", map[string]string{
			CheckServer: "VIEW SERVER STATE", CheckErrorLog: "securityadmin",
			CheckQueryStore: "VIEW DATABASE STATE", CheckAgentJobs: "SQLAgentReaderRole",
		}},
	} {
		t.Run(tc.era, func(t *testing.T) {
			q := fakeQ{
				by: map[string][]Row{"unravis:base": baseRows(tc.version, 0, 0)},
				err: map[string]error{
					"unravis:server": denied, "unravis:errorlog": denied, "unravis:qs_options": denied, "unravis:agentjobs": denied,
				},
			}
			r := RunSQLLane(context.Background(), q, "Prod", laneReq, fixedNow)
			if r.Lane.Era != tc.era {
				t.Fatalf("era = %s", r.Lane.Era)
			}
			if len(r.Lane.Answers) != 0 {
				t.Errorf("answers with no grants: %v", r.Lane.Answers)
			}
			if r.Lane.State() != StateNotYet || r.Overall != Unavailable {
				t.Errorf("lane %s overall %s", r.Lane.State(), r.Overall)
			}
			for id, perm := range tc.want {
				c := checkByID(t, r, id)
				if c.State != StateNotYet || c.Fix == nil || !strings.Contains(c.Fix.Permission, perm) {
					t.Errorf("%s = %+v", id, c)
					continue
				}
				if id != CheckErrorLog || tc.era == "2022" {
					if !strings.Contains(c.Fix.Script, "[unravis_ro]") {
						t.Errorf("%s script lacks the login: %s", id, c.Fix.Script)
					}
				}
				if strings.Contains(c.Summary, "permission to perform") {
					t.Errorf("driver text leaked: %s", c.Summary)
				}
			}
		})
	}
}

func TestSQLLaneFailedBaseQueryFailsEveryCheck(t *testing.T) {
	q := fakeQ{err: map[string]error{"unravis:base": errors.New("connection reset by peer on PRODSQL01")}}
	r := RunSQLLane(context.Background(), q, "Prod", laneReq, fixedNow)
	for _, c := range r.Lane.Checks {
		if c.State == StateReady {
			t.Errorf("%s ran without a base answer", c.ID)
		}
		if strings.Contains(c.Summary, "PRODSQL01") {
			t.Errorf("driver text leaked: %s", c.Summary)
		}
	}
	if r.Lane.State() == StateReady || len(r.Lane.Answers) != 0 {
		t.Errorf("lane = %s %v", r.Lane.State(), r.Lane.Answers)
	}
}

func TestSQLLaneServerGoingDownMidRunKeepsEarlierChecks(t *testing.T) {
	q := healthySQL("16.0.1")
	q.err = map[string]error{"unravis:qs_options": errors.New("timeout expired")}
	r := RunSQLLane(context.Background(), q, "Prod", laneReq, fixedNow)
	if checkByID(t, r, CheckServer).State != StateReady || checkByID(t, r, CheckQueryStore).State != StateFailed {
		t.Fatalf("checks = %+v", r.Lane.Checks)
	}
	if r.Lane.State() != "Partly ready" || r.Lane.Answers["D12"] != AnswerNo {
		t.Errorf("lane %s answers %v", r.Lane.State(), r.Lane.Answers)
	}
}

func TestSQLLaneCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := RunSQLLane(ctx, healthySQL("16.0.1"), "Prod", laneReq, fixedNow)
	for _, c := range r.Lane.Checks {
		if c.State != StateFailed {
			t.Errorf("%s = %s after cancel", c.ID, c.State)
		}
	}
}

// Every query the lane sends must be a single read: no write verbs, no EXEC other than the two error-log reads, no semicolons, and the only
// values in a text are datetime literals the package formatted.
func TestSQLLaneQueriesAreReadOnly(t *testing.T) {
	var seen []string
	rec := recordingQ{fn: func(s string) { seen = append(seen, s) }, inner: healthySQL("16.0.1")}
	RunSQLLane(context.Background(), rec, "Prod", laneReq, fixedNow)
	if len(seen) < 8 {
		t.Fatalf("only %d queries sent", len(seen))
	}
	for _, s := range seen {
		up := strings.ToUpper(s)
		for _, bad := range []string{"INSERT ", "UPDATE ", "DELETE ", "DROP ", "ALTER ", "CREATE ", "GRANT ", "TRUNCATE ", "MERGE ", ";"} {
			if strings.Contains(up, bad) {
				t.Errorf("query contains %q: %s", bad, s)
			}
		}
		if strings.Contains(up, "EXEC ") && !strings.Contains(s, "sp_readerrorlog 0, 1,") {
			t.Errorf("unexpected EXEC: %s", s)
		}
	}
}

type recordingQ struct {
	fn    func(string)
	inner Queryer
}

func (r recordingQ) Query(ctx context.Context, text string) ([]Row, error) {
	r.fn(text)
	return r.inner.Query(ctx, text)
}

func TestLaneRequestValidate(t *testing.T) {
	if err := laneReq.Validate(); err != nil {
		t.Fatalf("valid request refused: %v", err)
	}
	cases := map[string]LaneRequest{
		"empty":         {},
		"reverse":       {Incident: Window{laneInc.End, laneInc.Start}, Reference: laneRef},
		"overlap":       {Incident: laneInc, Reference: Window{laneInc.Start.Add(-time.Hour), laneInc.Start.Add(time.Hour)}},
		"too long":      {Incident: Window{laneInc.Start, laneInc.Start.Add(15 * 24 * time.Hour)}, Reference: laneRef},
		"reference far": {Incident: laneInc, Reference: Window{laneRef.Start.Add(-15 * 24 * time.Hour), laneRef.End}},
	}
	for name, r := range cases {
		if r.Validate() == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestWindowRelations(t *testing.T) {
	a := Window{at(1, 0, 0), at(1, 12, 0)}
	if !a.Overlaps(Window{at(1, 11, 0), at(1, 13, 0)}) || a.Overlaps(Window{at(1, 12, 0), at(1, 13, 0)}) {
		t.Error("overlap is end-exclusive")
	}
	if !a.Same(Window{at(1, 0, 0), at(1, 12, 0)}) || a.Same(Window{at(1, 0, 0), at(1, 13, 0)}) {
		t.Error("same")
	}
	if !a.Contains(at(1, 0, 0)) || a.Contains(at(1, 12, 0)) {
		t.Error("contains is start-inclusive, end-exclusive")
	}
}

func TestLaneReportCarriesNoConnectionDetails(t *testing.T) {
	r := RunSQLLane(context.Background(), healthySQL("16.0.1"), "Prod", laneReq, fixedNow)
	if r.Label != "Prod" || r.Database != "AuditDb" || r.Kind != "sql" {
		t.Errorf("report = %+v", r)
	}
	if len(r.Findings) != 4 || r.Findings[0].Status != OK {
		t.Errorf("findings = %+v", r.Findings)
	}
}
