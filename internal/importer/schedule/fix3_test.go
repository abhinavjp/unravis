package schedule

import (
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

// Tests for the batch 3 review fixes (M2, M5, M6, M7 and the schedule Low
// findings). Every fixture is synthetic: sql-a, job-alpha, 2001 dates.

// ---------------------------------------------------------------------- M2

func TestEveryGapSentencePassesLintCausal(t *testing.T) {
	for _, g := range []string{
		gapNoWindow, gapUnknown, gapDST, gapCronBoth, gapHangfireSnap, gapHangfireLast,
		gapAgentSnap, gapAgentHostLocal, gapAgentHistory, gapWeeklyFactor, gapMonthDayMissing,
		gapTruncated, gapAgentStepRows, gapAgentNoStep, gapAgentJobEnabled,
	} {
		if err := evidence.LintCausal(g); err != nil {
			t.Errorf("%q: %v", g, err)
		}
	}
	for _, c := range []struct{ k, n, u int }{{0, 0, 0}, {3, 10, 0}, {3, 10, 2}} {
		if err := evidence.LintCausal(B3Wording(c.k, c.n, c.u, time.Minute)); err != nil {
			t.Errorf("B3Wording(%v): %v", c, err)
		}
	}
}

// ---------------------------------------------------------------------- M5

func TestB3NoWindowIsUnknownNotMiss(t *testing.T) {
	csv := hfHead + "job-alpha,default,0 * * * *,UTC,,,\n"
	o := hangfireOpts()
	o.Window = importer.Window{}
	ev, rep := mustParse(t, NewHangfire(), []byte(csv), o)
	// Without a window nothing was expanded, so no boundary can be missing.
	k, n, unk := B3([]time.Time{utc("2001-03-24T10:00:10Z")}, ev, time.Minute)
	if k != 0 || n != 1 || unk != 1 {
		t.Fatalf("B3: k=%d n=%d unknown=%d, want one unknown", k, n, unk)
	}
	k, n, unk = B3Covered([]time.Time{utc("2001-03-24T10:00:10Z")}, ev, time.Minute, CoverageOf(rep))
	if k != 0 || n != 1 || unk != 1 {
		t.Fatalf("B3Covered with no coverage: k=%d n=%d unknown=%d", k, n, unk)
	}
}

func TestB3EdgeAndOutsideOfCoverageAreUnknown(t *testing.T) {
	csv := hfHead + "job-alpha,default,0 * * * *,UTC,,,\n"
	o := hangfireOpts()
	o.Window = importer.Window{From: utc("2001-03-24T10:00:30Z"), To: utc("2001-03-24T12:00:30Z")}
	ev, rep := mustParse(t, NewHangfire(), []byte(csv), o)
	cov := CoverageOf(rep)
	if cov.From.IsZero() || cov.To.IsZero() {
		t.Fatalf("coverage = %+v", cov)
	}
	stalls := []time.Time{
		utc("2001-03-24T10:00:40Z"), // inside the window, ±1 min reaches the 10:00 boundary that was cut off
		utc("2001-03-25T10:00:10Z"), // a day after the window
		utc("2001-03-23T10:00:10Z"), // a day before the window
		utc("2001-03-24T10:30:00Z"), // fully covered, no boundary near: a real miss
		utc("2001-03-24T11:00:20Z"), // near the 11:00 boundary: a hit
	}
	k, n, unk := B3Covered(stalls, ev, time.Minute, cov)
	if k != 1 || n != 5 || unk != 3 {
		t.Fatalf("k=%d n=%d unknown=%d, want 1/5/3 (one real miss)", k, n, unk)
	}
}

func TestCoverageOfReport(t *testing.T) {
	_, rep := mustParse(t, NewHangfire(), []byte(hfHead+"job-alpha,default,0 * * * *,UTC,,,\n"), hangfireOpts())
	c := CoverageOf(rep)
	if !c.From.Equal(win().From) || !c.To.Equal(win().To) {
		t.Errorf("coverage = %+v", c)
	}
	if c := CoverageOf(importer.Report{}); !c.From.IsZero() || !c.To.IsZero() {
		t.Errorf("empty report coverage = %+v", c)
	}
}

// ---------------------------------------------------------------------- M6

func TestHangfireFoldEmitsBothOccurrences(t *testing.T) {
	// 2001-10-28 03:00 CEST falls back to 02:00 CET: 02:30 happens at 00:30Z and 01:30Z.
	csv := hfHead + "job-alpha,default,30 2 * * *,Europe/Berlin,,,\n"
	o := hangfireOpts()
	o.Window = importer.Window{From: utc("2001-10-27T00:00:00Z"), To: utc("2001-10-30T00:00:00Z")}
	ev, rep := mustParse(t, NewHangfire(), []byte(csv), o)
	var amb []string
	for _, e := range ofKind(ev, KindBoundary) {
		if e.Attrs["dst"] == "ambiguous" {
			amb = append(amb, e.Start.Format(time.RFC3339))
		}
	}
	if !equalStrings(amb, []string{"2001-10-28T00:30:00Z", "2001-10-28T01:30:00Z"}) {
		t.Fatalf("ambiguous boundaries = %v", amb)
	}
	// A stall at the second occurrence is not a miss.
	k, n, unk := B3Covered([]time.Time{utc("2001-10-28T01:30:05Z"), utc("2001-10-28T00:30:05Z")}, ev, time.Minute, CoverageOf(rep))
	if k != 0 || n != 2 || unk != 2 {
		t.Fatalf("k=%d n=%d unknown=%d, want both unknown", k, n, unk)
	}
}

func TestHangfireGapEmitsBothReadings(t *testing.T) {
	// 2001-03-25 02:30 does not exist in Berlin: it is 01:30Z read as CET or 00:30Z read as CEST.
	csv := hfHead + "job-alpha,default,30 2 * * *,Europe/Berlin,,,\n"
	ev, rep := mustParse(t, NewHangfire(), []byte(csv), hangfireOpts())
	var gap []string
	for _, e := range ofKind(ev, KindBoundary) {
		if e.Attrs["dst"] == "nonexistent" {
			gap = append(gap, e.Start.Format(time.RFC3339))
		}
	}
	if !equalStrings(gap, []string{"2001-03-25T00:30:00Z", "2001-03-25T01:30:00Z"}) {
		t.Fatalf("nonexistent boundaries = %v", gap)
	}
	k, n, unk := B3Covered([]time.Time{utc("2001-03-25T00:30:10Z"), utc("2001-03-25T01:30:10Z")}, ev, time.Minute, CoverageOf(rep))
	if k != 0 || n != 2 || unk != 2 {
		t.Fatalf("k=%d n=%d unknown=%d", k, n, unk)
	}
}

func TestAgentFoldEmitsBothOccurrences(t *testing.T) {
	csv := agHead + agRow("job-alpha", "1", "4", "1", "1", "0", "0", "0", "20010101", "99991231", "23000", "235959")
	ev, rep := mustParse(t, NewAgent(), []byte(csv), agentWindow(t, "2001-10-27T00:00:00Z", "2001-10-30T00:00:00Z"))
	var amb []string
	for _, e := range ofKind(ev, KindBoundary) {
		if e.Attrs["dst"] == "ambiguous" {
			amb = append(amb, e.Start.Format(time.RFC3339))
		}
	}
	if !equalStrings(amb, []string{"2001-10-28T00:30:00Z", "2001-10-28T01:30:00Z"}) {
		t.Fatalf("ambiguous boundaries = %v", amb)
	}
	k, n, unk := B3Covered([]time.Time{utc("2001-10-28T01:30:00Z")}, ev, time.Minute, CoverageOf(rep))
	if k != 0 || n != 1 || unk != 1 {
		t.Fatalf("k=%d n=%d unknown=%d, a stall at the second occurrence must not be a miss", k, n, unk)
	}
}

// ---------------------------------------------------------------------- M7

func TestAgentSubdayWindowCrossingMidnightIsUnknown(t *testing.T) {
	// Every 15 minutes from 22:00 to 02:00 the next day.
	csv := agHead + agRow("job-alpha", "1", "4", "1", "4", "15", "0", "0", "20010101", "99991231", "220000", "20000")
	ev, rep := mustParse(t, NewAgent(), []byte(csv), agentOpts(t))
	if len(ofKind(ev, KindBoundary)) != 0 {
		t.Fatalf("no boundary expected, got %v", starts(ev))
	}
	u := ofKind(ev, KindUnknown)
	if len(u) != 1 || u[0].Attrs["reason"] != reasonWrap {
		t.Fatalf("want one %s unknown, got %+v", reasonWrap, u)
	}
	if !hasGap(rep, gapUnknown) {
		t.Error("unknown gap sentence missing")
	}
	k, n, unk := B3Covered([]time.Time{utc("2001-03-25T21:00:00Z")}, ev, time.Minute, CoverageOf(rep))
	if k != 0 || n != 1 || unk != 1 {
		t.Fatalf("k=%d n=%d unknown=%d, a stall in a wrapped window must not be a miss", k, n, unk)
	}
}

func TestAgentSubdaySameDayWindowStillExpands(t *testing.T) {
	csv := agHead + agRow("job-alpha", "1", "4", "1", "8", "6", "0", "0", "20010101", "99991231", "0", "180000")
	ev, _ := mustParse(t, NewAgent(), []byte(csv), agentWindow(t, "2001-03-20T00:00:00Z", "2001-03-21T00:00:00Z"))
	if len(ofKind(ev, KindUnknown)) != 0 || len(ofKind(ev, KindBoundary)) != 4 {
		t.Fatalf("events = %v", starts(ev))
	}
}

// -------------------------------------------------------------- boundary cap

func TestBoundaryCapKeepsFileAndMarksRestUnknown(t *testing.T) {
	csv := hfHead +
		"job-alpha,default,* * * * * *,UTC,,,\n" +
		"job-beta,default,0 * * * *,UTC,,,\n"
	o := hangfireOpts()
	o.Window = importer.Window{From: utc("2001-03-24T00:00:00Z"), To: utc("2001-03-26T00:00:00Z")}
	ev, rep, err := parse(NewHangfire(), []byte(csv), o)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if n := len(forJob(ev, KindBoundary, "job-alpha")); n != maxPerSchedule {
		t.Errorf("alpha boundaries = %d, want %d", n, maxPerSchedule)
	}
	if n := len(forJob(ev, KindBoundary, "job-beta")); n != 49 {
		t.Errorf("beta boundaries = %d, want 49: a chatty job must not starve the others", n)
	}
	u := forJob(ev, KindUnknown, "job-alpha")
	if len(u) != 1 || u[0].Attrs["reason"] != reasonTooMany {
		t.Fatalf("alpha unknown = %+v", u)
	}
	if !hasGap(rep, gapTruncated) || !hasGap(rep, gapUnknown) {
		t.Errorf("gaps = %v", rep.KnownGaps)
	}
	cov := CoverageOf(rep)
	stalls := []time.Time{
		utc("2001-03-24T03:30:00Z"), // before the cut: every second is a boundary, a hit
		utc("2001-03-25T12:30:30Z"), // after the cut, no beta boundary near: unknown
	}
	k, n, unk := B3Covered(stalls, ev, time.Minute, cov)
	if k != 1 || n != 2 || unk != 1 {
		t.Fatalf("k=%d n=%d unknown=%d", k, n, unk)
	}
}

func TestBoundaryCapFileLevelIsPartialToo(t *testing.T) {
	var sb strings.Builder
	sb.WriteString(hfHead)
	for _, j := range []string{"job-a", "job-b", "job-c", "job-d", "job-e"} {
		sb.WriteString(j + ",default,* * * * * *,UTC,,,\n")
	}
	o := hangfireOpts()
	o.Window = importer.Window{From: utc("2001-03-24T00:00:00Z"), To: utc("2001-03-25T00:00:00Z")}
	ev, _, err := parse(NewHangfire(), []byte(sb.String()), o)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if n := len(ofKind(ev, KindBoundary)); n != maxBoundaries {
		t.Errorf("boundaries = %d, want exactly the file cap %d", n, maxBoundaries)
	}
	if n := len(forJob(ev, KindBoundary, "job-e")); n != 0 {
		t.Errorf("job-e boundaries = %d, want 0 once the file cap is spent", n)
	}
	if u := forJob(ev, KindUnknown, "job-e"); len(u) != 1 || u[0].Attrs["reason"] != reasonTooMany {
		t.Errorf("job-e unknown = %+v", u)
	}
}

// ------------------------------------------------------------ history steps

func TestAgentHistoryStepRowsAreNotJobRuns(t *testing.T) {
	csv := "job_name,step_id,run_date,run_time,run_duration\n" +
		"job-alpha,0,20010324,100000,500\n" +
		"job-alpha,1,20010324,100000,100\n" +
		"job-alpha,2,20010324,100100,100\n" +
		"job-alpha,0,20010324,100000,500\n" + // the same run repeated by a join with a second schedule
		"job-alpha,0,20010325,100000,500\n"
	ev, rep := mustParse(t, NewAgent(), []byte(csv), agentOpts(t))
	if rep.Rows != 5 || rep.Rejected != 0 {
		t.Fatalf("rows=%d rejected=%d", rep.Rows, rep.Rejected)
	}
	runs := ofKind(ev, KindJobRun)
	if len(runs) != 2 {
		t.Fatalf("job runs = %v, want 2", starts(runs))
	}
	if !equalStrings(starts(runs), []string{"2001-03-24T09:00:00Z", "2001-03-25T08:00:00Z"}) {
		t.Errorf("runs = %v", starts(runs))
	}
	if !hasGap(rep, gapAgentStepRows) || hasGap(rep, gapAgentNoStep) {
		t.Errorf("gaps = %v", rep.KnownGaps)
	}
}

func TestAgentHistoryWithoutStepIDIsDedupedAndSaysSo(t *testing.T) {
	csv := "job_name,run_date,run_time,run_duration\n" +
		"job-alpha,20010324,100000,500\n" +
		"job-alpha,20010324,100000,500\n"
	ev, rep := mustParse(t, NewAgent(), []byte(csv), agentOpts(t))
	if n := len(ofKind(ev, KindJobRun)); n != 1 {
		t.Errorf("job runs = %d, want 1", n)
	}
	if !hasGap(rep, gapAgentNoStep) {
		t.Errorf("a history file with no step_id column must say step rows may be counted: %v", rep.KnownGaps)
	}
}

func TestAgentBadStepIDRejected(t *testing.T) {
	csv := "job_name,step_id,run_date,run_time\njob-alpha,x,20010324,100000\njob-alpha,0,20010324,100000\n"
	ev, rep, err := parse(NewAgent(), []byte(csv), lenient(agentOpts(t)))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rejected != 1 || !hasReason(rep, 2, importer.ReasonBadNumber) || len(ofKind(ev, KindJobRun)) != 1 {
		t.Fatalf("rejected=%d errors=%+v runs=%d", rep.Rejected, rep.Errors, len(ofKind(ev, KindJobRun)))
	}
}

// ------------------------------------------------------------ enabled columns

const agHeadTwoEnabled = "job_name,enabled,enabled,freq_type,freq_interval,freq_subday_type,freq_subday_interval,active_start_date,active_start_time\n"

func TestAgentTwoEnabledColumnsAccepted(t *testing.T) {
	csv := agHeadTwoEnabled +
		"job-alpha,1,1,4,1,1,0,20010101,30000\n" + // job and schedule enabled
		"job-beta,1,0,4,1,1,0,20010101,30000\n" + // schedule disabled
		"job-gamma,0,1,4,1,1,0,20010101,30000\n" // job disabled
	ev, rep := mustParse(t, NewAgent(), []byte(csv), agentOpts(t))
	if rep.Rejected != 0 {
		t.Fatalf("rejected=%d %+v", rep.Rejected, rep.Errors)
	}
	for job, wantDisabled := range map[string]bool{"job-alpha": false, "job-beta": true, "job-gamma": true} {
		b := forJob(ev, KindBoundary, job)
		if len(b) != 3 {
			t.Errorf("%s boundaries = %d, want 3", job, len(b))
			continue
		}
		got := b[0].Attrs["basis"] == reasonDisabled
		if got != wantDisabled {
			t.Errorf("%s disabled basis = %v, want %v", job, got, wantDisabled)
		}
		if u := forJob(ev, KindUnknown, job); (len(u) == 1) != wantDisabled {
			t.Errorf("%s unknown markers = %d", job, len(u))
		}
	}
	if hasGap(rep, gapAgentJobEnabled) {
		t.Error("two enabled columns cover the job and the schedule: no job-enabled gap")
	}
}

func TestAgentNamedEnabledColumns(t *testing.T) {
	head := "job_name,job_enabled,schedule_enabled,freq_type,freq_interval,freq_subday_type,freq_subday_interval,active_start_date,active_start_time\n"
	csv := head + "job-alpha,0,1,4,1,1,0,20010101,30000\njob-beta,1,1,4,1,1,0,20010101,30000\n"
	ev, rep := mustParse(t, NewAgent(), []byte(csv), agentOpts(t))
	if a := forJob(ev, KindBoundary, "job-alpha"); len(a) != 3 || a[0].Attrs["basis"] != reasonDisabled {
		t.Errorf("alpha = %+v", a)
	}
	if b := forJob(ev, KindBoundary, "job-beta"); len(b) != 3 || b[0].Attrs["basis"] != "" {
		t.Errorf("beta = %+v", b)
	}
	if hasGap(rep, gapAgentJobEnabled) {
		t.Error("job_enabled and schedule_enabled: no gap")
	}
}

func TestAgentSingleEnabledColumnSaysJobFlagMissing(t *testing.T) {
	_, rep := mustParse(t, NewAgent(), fixture(t, "agent_good.csv"), agentOpts(t))
	if !hasGap(rep, gapAgentJobEnabled) {
		t.Errorf("a single enabled column cannot show a disabled job: %v", rep.KnownGaps)
	}
}

func TestAgentBadSecondEnabledRejected(t *testing.T) {
	csv := agHeadTwoEnabled + "job-alpha,1,maybe,4,1,1,0,20010101,30000\n" + "job-beta,1,1,4,1,1,0,20010101,30000\n"
	_, rep, err := parse(NewAgent(), []byte(csv), lenient(agentOpts(t)))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rejected != 1 || !hasReason(rep, 2, importer.ReasonBadNumber) {
		t.Fatalf("rejected=%d %+v", rep.Rejected, rep.Errors)
	}
}

func TestAgentDuplicateOtherHeaderStillQuarantines(t *testing.T) {
	csv := "job_name,enabled,freq_type,freq_type\nx,1,4,4\n"
	_, _, err := parse(NewAgent(), []byte(csv), agentOpts(t))
	wantQuarantine(t, err, importer.ReasonUnsupportedFormat, "")
}

// ------------------------------------------------------- per-schedule unknown

func TestB3DisabledJobDoesNotBlanketUnknown(t *testing.T) {
	csv := agHead +
		agRow("job-alpha", "1", "4", "1", "1", "0", "0", "0", "20010101", "99991231", "30000", "235959") +
		agRow("job-zeta", "0", "4", "1", "1", "0", "0", "0", "20010101", "99991231", "10000", "235959")
	ev, rep := mustParse(t, NewAgent(), []byte(csv), agentOpts(t))
	stalls := []time.Time{
		utc("2001-03-24T02:00:10Z"), // alpha 03:00 CET: a hit
		utc("2001-03-25T10:00:00Z"), // nothing near, no schedule unknown here: a real miss
		utc("2001-03-25T00:00:20Z"), // near the disabled job-zeta boundary (01:00 CET): unknown
	}
	k, n, unk := B3Covered(stalls, ev, time.Minute, CoverageOf(rep))
	if k != 1 || n != 3 || unk != 1 {
		t.Fatalf("k=%d n=%d unknown=%d, want 1/3/1 (one real miss)", k, n, unk)
	}
}

func TestB3UnknownSpanOnlyAffectsItsOwnStalls(t *testing.T) {
	b := []evidence.Event{
		bev(KindBoundary, "2001-03-24T03:00:00Z", nil),
		uev("job-delta", reasonMonthDay, "2001-04-10T00:00:00Z", "2001-04-11T00:00:00Z"),
	}
	cov := Coverage{From: utc("2001-03-20T00:00:00Z"), To: utc("2001-04-20T00:00:00Z")}
	stalls := []time.Time{
		utc("2001-03-24T09:00:00Z"), // far from the span: a real miss
		utc("2001-04-10T12:00:00Z"), // inside the span: unknown
		utc("2001-04-09T23:59:30Z"), // within tolerance of the span start: unknown
		utc("2001-04-12T12:00:00Z"), // after the span: a real miss
	}
	k, n, unk := B3Covered(stalls, b, time.Minute, cov)
	if k != 0 || n != 4 || unk != 2 {
		t.Fatalf("k=%d n=%d unknown=%d, want 0/4/2", k, n, unk)
	}
}

func TestMonthDayMissingUnknownCoversOnlyThatDay(t *testing.T) {
	// A monthly day-31 schedule has no April boundary; only that day is unknown.
	csv := agHead + agRow("job-delta", "1", "16", "31", "1", "0", "0", "1", "20010101", "99991231", "120000", "235959")
	ev, rep := mustParse(t, NewAgent(), []byte(csv), agentWindow(t, "2001-04-20T00:00:00Z", "2001-05-10T00:00:00Z"))
	u := ofKind(ev, KindUnknown)
	if len(u) != 1 || u[0].Attrs["reason"] != reasonMonthDay {
		t.Fatalf("unknown = %+v", u)
	}
	if got := u[0].End.Sub(u[0].Start); got > 48*time.Hour {
		t.Errorf("month-day unknown spans %v, want about the one missing day", got)
	}
	cov := CoverageOf(rep)
	// The missing day is 2001-04-30 server time; 04-25 and 05-05 are outside it.
	k, n, unk := B3Covered([]time.Time{utc("2001-04-25T08:00:00Z"), utc("2001-04-30T10:00:00Z"), utc("2001-05-05T08:00:00Z")}, ev, time.Minute, cov)
	if k != 0 || n != 3 || unk != 1 {
		t.Fatalf("k=%d n=%d unknown=%d, want 0/3/1", k, n, unk)
	}
}

func TestUnsupportedCronUnknownStillCoversWholeWindow(t *testing.T) {
	csv := hfHead + "job-gamma,default,0 0 L * *,UTC,,,\n"
	ev, rep := mustParse(t, NewHangfire(), []byte(csv), hangfireOpts())
	u := ofKind(ev, KindUnknown)
	if len(u) != 1 || !u[0].Start.Equal(win().From) || !u[0].End.Equal(win().To) {
		t.Fatalf("unknown = %+v", u)
	}
	k, n, unk := B3Covered([]time.Time{utc("2001-03-25T12:00:00Z")}, ev, time.Minute, CoverageOf(rep))
	if k != 0 || n != 1 || unk != 1 {
		t.Fatalf("k=%d n=%d unknown=%d", k, n, unk)
	}
}

func TestB3WordingMentionsOutsideWindow(t *testing.T) {
	s := B3Wording(1, 5, 2, time.Minute)
	if !containsFold(s, "outside the imported window") || !containsFold(s, "not counted as misses") {
		t.Errorf("wording = %q", s)
	}
}
