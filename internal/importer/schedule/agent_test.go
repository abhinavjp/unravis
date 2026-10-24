package schedule

import (
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/importer"
)

const agHead = "job_name,enabled,freq_type,freq_interval,freq_subday_type,freq_subday_interval,freq_relative_interval,freq_recurrence_factor,active_start_date,active_end_date,active_start_time,active_end_time\n"

// agRow builds one schedule row from the columns that matter.
func agRow(job, enabled, freq, interval, subT, subI, rel, recur, sd, ed, st, et string) string {
	return strings.Join([]string{job, enabled, freq, interval, subT, subI, rel, recur, sd, ed, st, et}, ",") + "\n"
}

func agentWindow(t testing.TB, from, to string) importer.Options {
	o := agentOpts(t)
	o.Window = importer.Window{From: utc(from), To: utc(to)}
	return o
}

func hasGap(rep importer.Report, g string) bool {
	for _, x := range rep.KnownGaps {
		if x == g {
			return true
		}
	}
	return false
}

func TestAgentGoodFixture(t *testing.T) {
	ev, rep := mustParse(t, NewAgent(), fixture(t, "agent_good.csv"), agentOpts(t))
	if rep.Rows != 9 || rep.Rejected != 0 {
		t.Fatalf("rows=%d rejected=%d errors=%+v", rep.Rows, rep.Rejected, rep.Errors)
	}
	b, u, r := ofKind(ev, KindBoundary), ofKind(ev, KindUnknown), ofKind(ev, KindJobRun)
	// 45 enabled boundaries (job-beta has both readings of the missing hour)
	// plus 4 for the disabled job-zeta, which are kept as possible boundaries
	// (basis disabled-at-export).
	if len(b) != 49 || len(u) != 2 || len(r) != 2 || rep.Emitted != 53 {
		t.Fatalf("boundaries=%d unknown=%d runs=%d emitted=%d", len(b), len(u), len(r), rep.Emitted)
	}
	// job-alpha appears twice with one schedule: boundaries are not doubled.
	if n := len(forJob(ev, KindBoundary, "job-alpha")); n != 3 {
		t.Errorf("alpha boundaries = %d, want 3", n)
	}
	if n := len(forJob(ev, KindBoundary, "job-gamma")); n != 37 {
		t.Errorf("gamma boundaries = %d, want 37 (Monday 08:00-17:00 every 15 min)", n)
	}
	if n := len(forJob(ev, KindBoundary, "job-theta")); n != 1 {
		t.Errorf("theta (once) boundaries = %d, want 1", n)
	}
	reasons := map[string]string{}
	for _, e := range u {
		reasons[e.Attrs["job"]] = e.Attrs["reason"]
	}
	if reasons["job-zeta"] != reasonDisabled || reasons["job-eta"] != reasonFreq || len(reasons) != 2 {
		t.Errorf("unknown reasons = %v", reasons)
	}
	for _, e := range r {
		if e.DurationMs == nil || !e.End.Equal(e.Start.Add(durOf(*e.DurationMs))) {
			t.Errorf("run end not start+duration: %+v", e)
		}
	}
	if *r[0].DurationMs != 85000 || *r[1].DurationMs != 90000 {
		t.Errorf("durations = %v %v", *r[0].DurationMs, *r[1].DurationMs)
	}
	if rep.Quality.TimezoneRule == "" || len(rep.FieldRules) == 0 {
		t.Errorf("quality=%+v rules=%v", rep.Quality, rep.FieldRules)
	}
	for _, g := range []string{gapAgentSnap, gapAgentHostLocal, gapAgentHistory, gapUnknown, gapDST} {
		if !hasGap(rep, g) {
			t.Errorf("missing gap %q", g)
		}
	}
}

func TestAgentRequiresHostTimezone(t *testing.T) {
	data := fixture(t, "agent_good.csv")
	for name, o := range map[string]importer.Options{
		"no host": hangfireOpts(),
		"no tz": func() importer.Options {
			o := hangfireOpts()
			o.Host = &importer.HostHint{Label: "sql-a"}
			return o
		}(),
	} {
		_, _, err := parse(NewAgent(), data, o)
		if err == nil || !strings.Contains(err.Error(), importer.ErrTimezoneUnlabelled.Error()) {
			t.Errorf("%s: got %v, want timezone-unlabelled", name, err)
		}
	}
}

func TestAgentDSTGapBoundaryFlagged(t *testing.T) {
	// job-beta runs at 02:30 server time; on 2001-03-25 that hour does not exist.
	ev, _ := mustParse(t, NewAgent(), fixture(t, "agent_good.csv"), agentOpts(t))
	var flagged, plain int
	for _, e := range forJob(ev, KindBoundary, "job-beta") {
		switch e.Attrs["dst"] {
		case "nonexistent":
			flagged++
			if e.Start.Format("2006-01-02") != "2001-03-25" {
				t.Errorf("flag on wrong day: %v", e.Start)
			}
		case "":
			plain++
		default:
			t.Errorf("dst = %q", e.Attrs["dst"])
		}
	}
	// Both readings of the missing hour are emitted, each flagged.
	if flagged != 2 || plain != 2 {
		t.Fatalf("flagged=%d plain=%d", flagged, plain)
	}
}

func TestAgentUnknownFreqTypeRejected(t *testing.T) {
	csv := agHead + agRow("job-alpha", "1", "7", "1", "1", "0", "0", "0", "20010101", "99991231", "30000", "235959")
	_, rep, err := parse(NewAgent(), []byte(csv), lenient(agentOpts(t)))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rejected != 1 || !hasReason(rep, 2, importer.ReasonBadNumber) {
		t.Fatalf("rejected=%d errors=%+v", rep.Rejected, rep.Errors)
	}
	// With the default reject fraction the same file is quarantined.
	_, _, err = parse(NewAgent(), []byte(csv), agentOpts(t))
	wantQuarantine(t, err, importer.ReasonRejectFraction, "")
}

func TestAgentBadCellsRejected(t *testing.T) {
	csv := agHead +
		agRow("job-a", "maybe", "4", "1", "1", "0", "0", "0", "20010101", "99991231", "30000", "235959") +
		agRow("job-b", "1", "4", "1", "1", "0", "0", "0", "20011301", "99991231", "30000", "235959") +
		agRow("job-c", "1", "4", "1", "1", "0", "0", "0", "20010101", "99991231", "250000", "235959") +
		agRow("job-d", "1", "4", "0", "1", "0", "0", "0", "20010101", "99991231", "30000", "235959") +
		agRow("job-e", "1", "4", "1", "2", "0", "0", "0", "20010101", "99991231", "30000", "235959") +
		agRow("job-f", "1", "32", "2", "1", "0", "3", "1", "20010101", "99991231", "30000", "235959") +
		agRow("job-g", "1", "4", "1", "1", "0", "0", "0", "20010101", "99991231", "30000", "235959")
	ev, rep, err := parse(NewAgent(), []byte(csv), lenient(agentOpts(t)))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rows != 7 || rep.Rejected != 6 {
		t.Fatalf("rows=%d rejected=%d errors=%+v", rep.Rows, rep.Rejected, rep.Errors)
	}
	for line, want := range map[int64]importer.Reason{
		2: importer.ReasonBadNumber, 3: importer.ReasonBadTime, 4: importer.ReasonBadTime,
		5: importer.ReasonBadNumber, 6: importer.ReasonBadNumber, 7: importer.ReasonBadNumber,
	} {
		if !hasReason(rep, line, want) {
			t.Errorf("line %d: want %s in %+v", line, want, rep.Errors)
		}
	}
	for _, e := range ev {
		if e.Attrs["job"] != "job-g" {
			t.Errorf("rejected row emitted an event: %+v", e)
		}
	}
}

func TestAgentUnscrubbedColumnsQuarantine(t *testing.T) {
	for _, col := range []string{"command", "message", "step_name", "database_name", "output_file_name", "owner", "owner_login_name", "description"} {
		csv := "job_name,freq_type," + col + "\njob-alpha,4," + sentinel + "\n"
		_, _, err := parse(NewAgent(), []byte(csv), agentOpts(t))
		wantQuarantine(t, err, importer.ReasonUnscrubbedColumn, col)
		if strings.Contains(err.Error(), sentinel) {
			t.Errorf("%s: error leaks cell content: %v", col, err)
		}
	}
}

func TestAgentMissingColumns(t *testing.T) {
	_, _, err := parse(NewAgent(), []byte("x,y\n1,2\n"), agentOpts(t))
	wantQuarantine(t, err, importer.ReasonUnsupportedFormat, "job_name")
	_, _, err = parse(NewAgent(), []byte("job_name,other\n1,2\n"), agentOpts(t))
	wantQuarantine(t, err, importer.ReasonUnsupportedFormat, "freq_type")
	_, _, err = parse(NewAgent(), []byte("job_name,freq_type\njob-alpha,4\n"), agentOpts(t))
	wantQuarantine(t, err, importer.ReasonUnsupportedFormat, "enabled")
	_, _, err = parse(NewAgent(), []byte("job_name,run_date\njob-alpha,20010324\n"), agentOpts(t))
	wantQuarantine(t, err, importer.ReasonUnsupportedFormat, "run_time")
}

func TestAgentHistoryOnly(t *testing.T) {
	csv := "job_name,run_date,run_time,run_duration\n" +
		"job-alpha,20010324,100000,500\n" +
		"job-alpha,20010325,100000,NULL\n" +
		"job-beta,0,0,\n" +
		"job-alpha,20010401,100000,100\n"
	ev, rep := mustParse(t, NewAgent(), []byte(csv), agentOpts(t))
	if rep.Rows != 4 || rep.Rejected != 0 {
		t.Fatalf("rows=%d rejected=%d errors=%+v", rep.Rows, rep.Rejected, rep.Errors)
	}
	if len(ev) != 2 || len(ofKind(ev, KindJobRun)) != 2 {
		t.Fatalf("events = %d, want 2 job runs in window", len(ev))
	}
	// 10:00 CET on 2001-03-24 is 09:00Z; 10:00 CEST on 03-25 is 08:00Z.
	if got := starts(ev); !equalStrings(got, []string{"2001-03-24T09:00:00Z", "2001-03-25T08:00:00Z"}) {
		t.Errorf("starts = %v", got)
	}
	if ev[1].DurationMs != nil {
		t.Errorf("a NULL duration must stay unknown, got %v", *ev[1].DurationMs)
	}
	if hasGap(rep, gapUnknown) || len(ofKind(ev, KindBoundary)) != 0 {
		t.Error("history-only file has no schedule to expand")
	}
	if NewAgent().Detect([]byte("job_name,run_date,run_time\n"), "h.csv") != 60 {
		t.Error("history header should score 60")
	}
}

func TestAgentHistoryDSTRejected(t *testing.T) {
	// 2001-03-25 02:30 does not exist in Berlin; 2001-10-28 02:30 happens twice.
	csv := "job_name,run_date,run_time,run_duration\n" +
		"job-alpha,20010325,23000,10\n" +
		"job-alpha,20011028,23000,10\n" +
		"job-alpha,20010324,23000,10\n"
	o := agentOpts(t)
	o.Window = importer.Window{From: utc("2001-03-01T00:00:00Z"), To: utc("2001-11-01T00:00:00Z")}
	ev, rep, err := parse(NewAgent(), []byte(csv), lenient(o))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rejected != 2 || !hasReason(rep, 2, importer.ReasonDSTNonexistent) || !hasReason(rep, 3, importer.ReasonDSTAmbiguous) {
		t.Fatalf("rejected=%d errors=%+v", rep.Rejected, rep.Errors)
	}
	if len(ev) != 1 {
		t.Fatalf("events = %d, want 1", len(ev))
	}
}

func TestAgentMonthly(t *testing.T) {
	csv := agHead + agRow("job-delta", "1", "16", "15", "1", "0", "0", "1", "20010101", "99991231", "120000", "235959")
	ev, _ := mustParse(t, NewAgent(), []byte(csv), agentWindow(t, "2001-03-14T00:00:00Z", "2001-03-16T00:00:00Z"))
	if got := starts(ev); !equalStrings(got, []string{"2001-03-15T11:00:00Z"}) {
		t.Errorf("monthly = %v", got)
	}
	// Every second month starting January: March is in, April is out.
	csv = agHead + agRow("job-delta", "1", "16", "15", "1", "0", "0", "2", "20010101", "99991231", "120000", "235959")
	ev, _ = mustParse(t, NewAgent(), []byte(csv), agentWindow(t, "2001-04-14T00:00:00Z", "2001-04-16T00:00:00Z"))
	if len(ev) != 0 {
		t.Errorf("recurrence factor 2: April should be skipped, got %v", starts(ev))
	}
}

func TestAgentMonthlyRelative(t *testing.T) {
	// First Monday of March 2001 is the 5th: 06:00 CET is 05:00Z.
	csv := agHead + agRow("job-epsilon", "1", "32", "2", "1", "0", "1", "1", "20010101", "99991231", "60000", "235959")
	ev, _ := mustParse(t, NewAgent(), []byte(csv), agentWindow(t, "2001-03-04T00:00:00Z", "2001-03-09T00:00:00Z"))
	if got := starts(ev); !equalStrings(got, []string{"2001-03-05T05:00:00Z"}) {
		t.Errorf("first Monday = %v", got)
	}
	// Last weekend day of March 2001 is Saturday the 31st: 06:00 CEST is 04:00Z.
	csv = agHead + agRow("job-epsilon", "1", "32", "10", "1", "0", "16", "1", "20010101", "99991231", "60000", "235959")
	ev, _ = mustParse(t, NewAgent(), []byte(csv), agentWindow(t, "2001-03-29T00:00:00Z", "2001-04-02T00:00:00Z"))
	if got := starts(ev); !equalStrings(got, []string{"2001-03-31T04:00:00Z"}) {
		t.Errorf("last weekend day = %v", got)
	}
	// Last Friday (freq_interval 6) is the 30th: 06:00 CEST is 04:00Z.
	csv = agHead + agRow("job-epsilon", "1", "32", "6", "1", "0", "16", "1", "20010101", "99991231", "60000", "235959")
	ev, _ = mustParse(t, NewAgent(), []byte(csv), agentWindow(t, "2001-03-29T00:00:00Z", "2001-04-02T00:00:00Z"))
	if got := starts(ev); !equalStrings(got, []string{"2001-03-30T04:00:00Z"}) {
		t.Errorf("last Friday = %v", got)
	}
}

func TestAgentWeeklyRecurrenceTwo(t *testing.T) {
	// Mondays (interval bit 2) every second week from the week of 2001-01-01.
	csv := agHead + agRow("job-gamma", "1", "8", "2", "1", "0", "0", "2", "20010101", "99991231", "100000", "235959")
	ev, rep := mustParse(t, NewAgent(), []byte(csv), agentWindow(t, "2001-03-12T00:00:00Z", "2001-03-27T00:00:00Z"))
	want := []string{"2001-03-12T09:00:00Z", "2001-03-26T08:00:00Z"}
	if got := starts(ev); !equalStrings(got, want) {
		t.Errorf("weekly/2 = %v, want %v", got, want)
	}
	if !hasGap(rep, gapWeeklyFactor) {
		t.Error("weekly factor assumption must be stated")
	}
}

func TestAgentDay31Missing(t *testing.T) {
	// April has 30 days: a monthly schedule on day 31 has no April boundary.
	csv := agHead + agRow("job-delta", "1", "16", "31", "1", "0", "0", "1", "20010101", "99991231", "120000", "235959")
	ev, rep := mustParse(t, NewAgent(), []byte(csv), agentWindow(t, "2001-04-28T00:00:00Z", "2001-05-02T00:00:00Z"))
	if len(ofKind(ev, KindBoundary)) != 0 {
		t.Errorf("no boundary expected: %v", starts(ev))
	}
	u := ofKind(ev, KindUnknown)
	if len(u) != 1 || u[0].Attrs["reason"] != reasonMonthDay {
		t.Fatalf("want one month-day-missing unknown, got %+v", u)
	}
	if !hasGap(rep, gapMonthDayMissing) {
		t.Error("month-day gap sentence missing")
	}
}

func TestAgentActiveRangeAndDaily(t *testing.T) {
	// Every second day from 2001-03-20 until 2001-03-25 inclusive, 03:00.
	csv := agHead + agRow("job-alpha", "1", "4", "2", "1", "0", "0", "0", "20010320", "20010325", "30000", "235959")
	ev, _ := mustParse(t, NewAgent(), []byte(csv), agentWindow(t, "2001-03-19T00:00:00Z", "2001-03-28T00:00:00Z"))
	want := []string{"2001-03-20T02:00:00Z", "2001-03-22T02:00:00Z", "2001-03-24T02:00:00Z"}
	if got := starts(ev); !equalStrings(got, want) {
		t.Errorf("daily/2 = %v, want %v", got, want)
	}
}

func TestAgentSubdayHours(t *testing.T) {
	// Every 6 hours between 00:00 and 18:00: four boundaries a day.
	csv := agHead + agRow("job-alpha", "1", "4", "1", "8", "6", "0", "0", "20010101", "99991231", "0", "180000")
	ev, _ := mustParse(t, NewAgent(), []byte(csv), agentWindow(t, "2001-03-20T00:00:00Z", "2001-03-21T00:00:00Z"))
	// CET: 06:00 12:00 18:00 local on the 20th and 00:00 on the 21st.
	want := []string{"2001-03-20T05:00:00Z", "2001-03-20T11:00:00Z", "2001-03-20T17:00:00Z", "2001-03-20T23:00:00Z"}
	if got := starts(ev); !equalStrings(got, want) {
		t.Errorf("sub-day = %v, want %v", got, want)
	}
}

func TestAgentDetect(t *testing.T) {
	a := NewAgent()
	if a.Detect([]byte(agHead), "s.csv") != 90 {
		t.Error("schedule header should score 90")
	}
	if a.Detect([]byte("name,freq_type\n"), "s.csv") != 90 {
		t.Error("sysjobs name column should score 90")
	}
	if a.Detect([]byte("a,b\n"), "s.csv") != 0 || a.Detect(nil, "") != 0 {
		t.Error("unrelated or empty head must score 0")
	}
}

func TestAgentNameColumnAlias(t *testing.T) {
	csv := strings.Replace(agHead, "job_name", "name", 1) +
		agRow("job-alpha", "1", "1", "0", "1", "0", "0", "0", "20010326", "99991231", "40000", "235959")
	ev, _ := mustParse(t, NewAgent(), []byte(csv), agentOpts(t))
	if len(ev) != 1 || ev[0].Attrs["job"] != "job-alpha" {
		t.Fatalf("events = %+v", ev)
	}
}

func durOf(ms float64) time.Duration { return time.Duration(ms * float64(time.Millisecond)) }
