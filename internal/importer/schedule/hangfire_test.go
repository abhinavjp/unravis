package schedule

import (
	"errors"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/importer"
)

const hfHead = "JobId,Queue,Cron,TimeZoneId,LastExecution,NextExecution,CreatedAt\n"

func wantLimit(t testing.TB, err error, limit string) {
	t.Helper()
	var le *importer.LimitError
	if !errors.As(err, &le) {
		t.Fatalf("want limit %q, got %v", limit, err)
	}
	if le.Limit != limit {
		t.Fatalf("limit = %q, want %q", le.Limit, limit)
	}
}

func TestHangfireGoodFixture(t *testing.T) {
	ev, rep := mustParse(t, NewHangfire(), fixture(t, "hangfire_good.csv"), hangfireOpts())
	if rep.Rows != 6 || rep.Rejected != 0 {
		t.Fatalf("rows=%d rejected=%d", rep.Rows, rep.Rejected)
	}
	b, u, r := ofKind(ev, KindBoundary), ofKind(ev, KindUnknown), ofKind(ev, KindJobRun)
	if len(b) != 296 || len(u) != 2 || len(r) != 4 || rep.Emitted != 302 || len(ev) != 302 {
		t.Fatalf("boundaries=%d unknown=%d runs=%d emitted=%d", len(b), len(u), len(r), rep.Emitted)
	}
	if n := len(forJob(ev, KindBoundary, "job-alpha")); n != 289 {
		t.Errorf("alpha boundaries = %d, want 289", n)
	}
	if n := len(forJob(ev, KindBoundary, "job-beta")); n != 3 {
		t.Errorf("beta boundaries = %d, want 3", n)
	}
	if n := len(forJob(ev, KindBoundary, "job-epsilon")); n != 3 {
		t.Errorf("epsilon boundaries = %d, want 3", n)
	}
	// 2001-03-24 and 03-25 are a weekend in Pacific time; only Monday 03-26 runs.
	zeta := forJob(ev, KindBoundary, "job-zeta")
	if !equalStrings(starts(zeta), []string{"2001-03-26T11:00:00Z"}) {
		t.Errorf("zeta = %v", starts(zeta))
	}
	reasons := map[string]string{}
	for _, e := range u {
		reasons[e.Attrs["job"]] = e.Attrs["reason"]
	}
	if reasons["job-gamma"] != reasonCron || reasons["job-delta"] != reasonTZ || len(reasons) != 2 {
		t.Errorf("unknown reasons = %v", reasons)
	}
	if rep.Quality.CoverageFirst == nil || !rep.Quality.CoverageFirst.Equal(win().From) {
		t.Errorf("coverage = %+v", rep.Quality)
	}
	if rep.First.After(rep.Last) {
		t.Errorf("first %v after last %v", rep.First, rep.Last)
	}
}

func TestHangfireDSTBoundaryFlagged(t *testing.T) {
	// Europe/Berlin spring forward 2001-03-25 02:00 -> 03:00: 02:30 does not exist.
	ev, _ := mustParse(t, NewHangfire(), fixture(t, "hangfire_good.csv"), hangfireOpts())
	beta := forJob(ev, KindBoundary, "job-beta")
	var flagged, plain int
	for _, e := range beta {
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
	if flagged != 1 || plain != 2 {
		t.Fatalf("flagged=%d plain=%d", flagged, plain)
	}
}

func TestHangfireDSTFoldFlagged(t *testing.T) {
	// 2001-10-28 03:00 CEST falls back to 02:00 CET: 02:30 happens twice.
	csv := hfHead + "job-alpha,default,30 2 * * *,Europe/Berlin,,,\n"
	o := hangfireOpts()
	o.Window = importer.Window{From: utc("2001-10-27T00:00:00Z"), To: utc("2001-10-30T00:00:00Z")}
	ev, rep := mustParse(t, NewHangfire(), []byte(csv), o)
	var amb int
	for _, e := range ofKind(ev, KindBoundary) {
		if e.Attrs["dst"] == "ambiguous" {
			amb++
		}
	}
	if amb != 1 {
		t.Fatalf("ambiguous = %d, want 1 (events=%d)", amb, len(ev))
	}
	found := false
	for _, g := range rep.KnownGaps {
		if g == gapDST {
			found = true
		}
	}
	if !found {
		t.Error("DST gap sentence missing")
	}
}

func TestHangfireNaiveTimeRejected(t *testing.T) {
	csv := hfHead +
		"job-alpha,default,0 * * * *,UTC,2001-03-24T05:00:00,,\n" +
		"job-beta,default,0 * * * *,UTC,2001-03-24T05:00:00Z,,\n"
	ev, rep, err := parse(NewHangfire(), []byte(csv), lenient(hangfireOpts()))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rejected != 1 || !hasReason(rep, 2, importer.ReasonTZUnlabelled) {
		t.Fatalf("rejected=%d errors=%+v", rep.Rejected, rep.Errors)
	}
	if len(forJob(ev, KindBoundary, "job-alpha")) != 0 || len(forJob(ev, KindBoundary, "job-beta")) == 0 {
		t.Error("rejected row must emit nothing; good row must emit")
	}
}

func TestHangfireBadCellsRejected(t *testing.T) {
	csv := hfHead +
		"job-alpha,default,not a cron,UTC,,,\n" +
		"job-beta,default,0 * * * *,UTC,yesterday,,\n" +
		",default,0 * * * *,UTC,,,\n" +
		"job-eta,default,0 * * * *,UTC,99999999999999999999,,\n" +
		"job-theta,default,0 * * * *\n" +
		"job-iota,default,0 * * * *,UTC,,,\n"
	_, rep, err := parse(NewHangfire(), []byte(csv), lenient(hangfireOpts()))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rows != 6 || rep.Rejected != 5 {
		t.Fatalf("rows=%d rejected=%d errors=%+v", rep.Rows, rep.Rejected, rep.Errors)
	}
	for line, want := range map[int64]importer.Reason{
		2: importer.ReasonBadNumber, 3: importer.ReasonBadTime, 4: importer.ReasonFieldCount,
		5: importer.ReasonBadNumber, 6: importer.ReasonFieldCount,
	} {
		if !hasReason(rep, line, want) {
			t.Errorf("line %d: want %s in %+v", line, want, rep.Errors)
		}
	}
}

func TestHangfireEpochTimes(t *testing.T) {
	ev, _ := mustParse(t, NewHangfire(), fixture(t, "hangfire_good.csv"), hangfireOpts())
	delta := forJob(ev, KindJobRun, "job-delta")
	eps := forJob(ev, KindJobRun, "job-epsilon")
	if !equalStrings(starts(delta), []string{"2001-03-24T02:13:20Z"}) {
		t.Errorf("epoch seconds: %v", starts(delta))
	}
	if !equalStrings(starts(eps), []string{"2001-03-24T07:46:40Z"}) {
		t.Errorf("epoch millis: %v", starts(eps))
	}
	if delta[0].Attrs["basis"] != "last-execution" {
		t.Errorf("basis = %q", delta[0].Attrs["basis"])
	}
}

func TestHangfireUnscrubbedColumnsQuarantine(t *testing.T) {
	for _, col := range []string{"Job", "Args", "Arguments", "InvocationData", "Payload"} {
		csv := "JobId,Cron,TimeZoneId," + col + "\njob-alpha,0 * * * *,UTC," + sentinel + "\n"
		_, _, err := parse(NewHangfire(), []byte(csv), hangfireOpts())
		wantQuarantine(t, err, importer.ReasonUnscrubbedColumn, strings.ToLower(col))
		if strings.Contains(err.Error(), sentinel) {
			t.Errorf("%s: error leaks cell content: %v", col, err)
		}
	}
}

func TestHangfireMissingColumn(t *testing.T) {
	for _, c := range []struct{ head, col string }{
		{"JobId,TimeZoneId\n", "cron"},
		{"Cron,TimeZoneId\n", "jobid"},
		{"JobId,Cron\n", "timezoneid"},
	} {
		_, _, err := parse(NewHangfire(), []byte(c.head+"x,y\n"), hangfireOpts())
		wantQuarantine(t, err, importer.ReasonUnsupportedFormat, c.col)
	}
}

func TestHangfireDuplicateHeaderQuarantine(t *testing.T) {
	_, _, err := parse(NewHangfire(), []byte("JobId,Cron,TimeZoneId,cron\na,b,c,d\n"), hangfireOpts())
	wantQuarantine(t, err, importer.ReasonUnsupportedFormat, "")
}

func TestHangfireRejectFractionQuarantine(t *testing.T) {
	var sb strings.Builder
	sb.WriteString(hfHead)
	for i := 0; i < 8; i++ {
		sb.WriteString("job-alpha,default,0 * * * *,UTC,,,\n")
	}
	sb.WriteString("job-beta,default,garbage,UTC,,,\n")
	sb.WriteString("job-gamma,default,garbage,UTC,,,\n")
	_, rep, err := parse(NewHangfire(), []byte(sb.String()), hangfireOpts())
	wantQuarantine(t, err, importer.ReasonRejectFraction, "")
	if rep.Rejected != 2 || rep.Rows != 10 {
		t.Errorf("rows=%d rejected=%d", rep.Rows, rep.Rejected)
	}
}

func TestHangfireRowAndLineLimits(t *testing.T) {
	csv := hfHead + strings.Repeat("job-alpha,default,0 0 1 1 *,UTC,,,\n", 3)
	o := hangfireOpts()
	o.Limits.MaxRows = 2
	_, _, err := parse(NewHangfire(), []byte(csv), o)
	wantLimit(t, err, "rows")

	long := hfHead + "job-alpha,default,0 0 1 1 *,UTC," + strings.Repeat("9", 70<<10) + ",,\n"
	_, _, err = parse(NewHangfire(), []byte(long), hangfireOpts())
	if !errors.Is(err, importer.ErrLimit) {
		t.Fatalf("line limit: got %v", err)
	}

	o = hangfireOpts()
	o.Limits.MaxBytes = 64
	_, _, err = parse(NewHangfire(), []byte(csv), o)
	wantLimit(t, err, "bytes")
}

func TestHangfireBoundaryCap(t *testing.T) {
	// A one-second schedule over two days is far past the 100k boundary cap.
	csv := hfHead + "job-alpha,default,* * * * * *,UTC,,,\n"
	o := hangfireOpts()
	o.Window = importer.Window{From: utc("2001-03-24T00:00:00Z"), To: utc("2001-03-26T00:00:00Z")}
	ev, _, err := parse(NewHangfire(), []byte(csv), o)
	wantLimit(t, err, "boundaries")
	if len(ev) > maxBoundaries {
		t.Errorf("emitted %d past the cap", len(ev))
	}
}

func TestHangfireNoWindowExpandsNothing(t *testing.T) {
	o := hangfireOpts()
	o.Window = importer.Window{}
	ev, rep := mustParse(t, NewHangfire(), fixture(t, "hangfire_good.csv"), o)
	if len(ofKind(ev, KindBoundary)) != 0 || len(ofKind(ev, KindUnknown)) != 0 {
		t.Error("no window must produce no boundaries or unknown markers")
	}
	if len(ofKind(ev, KindJobRun)) != 4 {
		t.Errorf("job runs = %d, want 4", len(ofKind(ev, KindJobRun)))
	}
	if rep.Quality.CoverageFirst != nil {
		t.Error("no window means no coverage claim")
	}
	found := false
	for _, g := range rep.KnownGaps {
		if g == gapNoWindow {
			found = true
		}
	}
	if !found {
		t.Error("missing no-window gap")
	}
}

func TestHangfireDetect(t *testing.T) {
	h := NewHangfire()
	if h.Detect([]byte(hfHead), "x.csv") != 90 {
		t.Error("header should score 90")
	}
	if h.Detect([]byte("\xef\xbb\xbf"+strings.ToUpper(hfHead)), "x.csv") != 90 {
		t.Error("BOM and case must not matter")
	}
	if h.Detect([]byte("a,b,c\n1,2,3\n"), "x.csv") != 0 {
		t.Error("unrelated header should score 0")
	}
	if h.Detect(nil, "") != 0 {
		t.Error("empty head should score 0")
	}
	if NewAgent().Detect([]byte(hfHead), "x.csv") != 0 {
		t.Error("agent must not claim a Hangfire file")
	}
}

func TestHangfireWindowRespected(t *testing.T) {
	csv := hfHead + "job-alpha,default,0 12 * * *,UTC,,,\n"
	o := hangfireOpts()
	o.Window = importer.Window{From: utc("2001-03-24T12:00:00Z"), To: utc("2001-03-24T12:00:00Z")}
	ev, _ := mustParse(t, NewHangfire(), []byte(csv), o)
	if len(ev) != 1 || !ev[0].Start.Equal(time.Date(2001, 3, 24, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("inclusive window edge: %v", starts(ev))
	}
}

func TestHangfireDomDowEither(t *testing.T) {
	// 2001-03-24 is a Saturday; "0 0 26 * 6" fires on Saturdays and on the 26th.
	csv := hfHead + "job-alpha,default,0 0 26 * 6,UTC,,,\n"
	ev, rep := mustParse(t, NewHangfire(), []byte(csv), hangfireOpts())
	want := []string{"2001-03-24T00:00:00Z", "2001-03-26T00:00:00Z"}
	if got := starts(ofKind(ev, KindBoundary)); !equalStrings(got, want) {
		t.Errorf("boundaries = %v, want %v", got, want)
	}
	found := false
	for _, g := range rep.KnownGaps {
		if g == gapCronBoth {
			found = true
		}
	}
	if !found {
		t.Error("both-restricted gap sentence missing")
	}
}
