package mel

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

func utc(s string) time.Time {
	t, err := time.Parse("2006-01-02 15:04:05.000", s)
	if err != nil {
		panic(err)
	}
	return t.UTC()
}

func byKind(evs []evidence.Event, kind string) []evidence.Event {
	var out []evidence.Event
	for _, e := range evs {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

func hasGap(rep importer.Report, gap string) bool {
	for _, g := range rep.KnownGaps {
		if g == gap {
			return true
		}
	}
	return false
}

func noLeak(t *testing.T, evs []evidence.Event, rep importer.Report, err error, words ...string) {
	t.Helper()
	b, _ := json.Marshal(struct {
		E []evidence.Event
		R importer.Report
	}{evs, rep})
	s := string(b)
	if err != nil {
		s += err.Error()
	}
	for _, w := range append(words, sentinel) {
		if strings.Contains(s, w) {
			t.Errorf("output contains %q", w)
		}
	}
}

func TestGapSentencesAreNotCausal(t *testing.T) {
	for _, g := range []string{GapNoTime, GapHostLocal, GapHeartbeat, GapSingleLine, GapStdoutTime, GapStdoutCrash, GapStdoutNameTime} {
		if err := evidence.LintCausal(g); err != nil {
			t.Errorf("%q: %v", g, err)
		}
	}
}

func TestConsoleBasic(t *testing.T) {
	evs, rep, err := collect(t, NewConsole(), string(readFixture(t, "console_basic.log")), baseOpts(time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	hb := byKind(evs, "heartbeat-late")
	if len(hb) != 1 {
		t.Fatalf("heartbeat-late events: %d", len(hb))
	}
	if hb[0].DurationMs == nil || *hb[0].DurationMs != 1500 {
		t.Errorf("heartbeat duration: %v", hb[0].DurationMs)
	}
	if !hb[0].End.Equal(utc("2001-03-04 05:06:10.000")) || !hb[0].Start.Equal(utc("2001-03-04 05:06:08.500")) {
		t.Errorf("heartbeat span: %v - %v", hb[0].Start, hb[0].End)
	}
	if n := len(byKind(evs, "app-start")); n != 1 {
		t.Errorf("app-start events: %d", n)
	}
	req := byKind(evs, "app-request")
	if len(req) != 1 || req[0].DurationMs == nil || *req[0].DurationMs != 1834.2 {
		t.Errorf("app-request: %+v", req)
	}
	var fail *evidence.Event
	for i, e := range evs {
		if e.Kind == "log-entry" && e.Attrs["level"] == "fail" {
			fail = &evs[i]
		}
	}
	if fail == nil {
		t.Fatal("no log-entry for the fail line")
	}
	if fail.Severity != "error" || fail.Attrs["exception"] != "System.InvalidOperationException" {
		t.Errorf("fail entry: %+v", fail)
	}
	if rep.Rows != 6 || rep.Rejected != 0 {
		t.Errorf("rows=%d rejected=%d", rep.Rows, rep.Rejected)
	}
	if rep.Quality.TimezoneRule != clock.RuleUTCField && rep.Quality.TimezoneRule != clock.RuleHostLocal {
		t.Errorf("rule: %q", rep.Quality.TimezoneRule)
	}
	noLeak(t, evs, rep, nil, "Order step failed", "Tick 7", "OrderService.cs", "web-a.example.test")
}

func TestConsoleHostLocalZone(t *testing.T) {
	ny := zone(t, "America/New_York")
	data := "2001-03-04 05:06:07 info: Microsoft.Hosting.Lifetime[0]\n      Application started. Press Ctrl+C to shut down.\n"
	evs, rep, err := collect(t, NewConsole(), data, baseOpts(ny))
	if err != nil {
		t.Fatal(err)
	}
	st := byKind(evs, "app-start")
	if len(st) != 1 || !st[0].Start.Equal(utc("2001-03-04 10:06:07.000")) {
		t.Fatalf("app-start: %+v", st)
	}
	if st[0].TZRule != clock.RuleHostLocal || rep.FieldRules["timestamp"] != clock.RuleHostLocal {
		t.Errorf("rule: %q %q", st[0].TZRule, rep.FieldRules["timestamp"])
	}
	if !hasGap(rep, GapHostLocal) {
		t.Error("host-local gap missing")
	}
}

func TestConsoleNoZoneRefused(t *testing.T) {
	data := "2001-03-04 05:06:07 info: Microsoft.Hosting.Lifetime[0]\n      Application started.\n"
	o := baseOpts(nil)
	_, _, err := collect(t, NewConsole(), data, o)
	if !errors.Is(err, importer.ErrTimezoneUnlabelled) {
		t.Fatalf("want tz-unlabelled, got %v", err)
	}
	o.Host = nil
	if _, _, err := collect(t, NewConsole(), data, o); !errors.Is(err, importer.ErrUnknownHost) {
		t.Fatalf("want unknown host, got %v", err)
	}
}

func TestConsoleZonedTimeNeedsNoHostZone(t *testing.T) {
	data := "2001-03-04T05:06:07+02:00 info: Microsoft.Hosting.Lifetime[0]\n      Application started.\n"
	evs, rep, err := collect(t, NewConsole(), data, baseOpts(nil))
	if err != nil {
		t.Fatal(err)
	}
	st := byKind(evs, "app-start")
	if len(st) != 1 || !st[0].Start.Equal(utc("2001-03-04 03:06:07.000")) || st[0].TZRule != clock.RuleOffsetInField {
		t.Fatalf("app-start: %+v", st)
	}
	if hasGap(rep, GapHostLocal) {
		t.Error("host-local gap present for a zoned time")
	}
}

func TestConsoleDSTFold(t *testing.T) {
	ny := zone(t, "America/New_York")
	o := baseOpts(ny)
	o.Limits = importer.Limits{MaxRejectFraction: 1}
	data := "2001-10-28 01:30:00 info: Microsoft.Hosting.Lifetime[0]\n      Application started.\n" +
		"2001-10-28 03:30:00 info: Microsoft.Hosting.Lifetime[0]\n      Application started.\n" +
		"2001-04-01 02:30:00 info: Microsoft.Hosting.Lifetime[0]\n      Application started.\n"
	evs, rep, err := collect(t, NewConsole(), data, o)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rejected != 2 || len(rep.Errors) != 2 {
		t.Fatalf("rejected=%d errors=%v", rep.Rejected, rep.Errors)
	}
	if rep.Errors[0].Reason != importer.ReasonDSTAmbiguous || rep.Errors[1].Reason != importer.ReasonDSTNonexistent {
		t.Errorf("reasons: %v", rep.Errors)
	}
	if n := len(byKind(evs, "app-start")); n != 1 {
		t.Errorf("app-start events: %d", n)
	}
}

func TestConsoleNoTimeRefused(t *testing.T) {
	data := "info: MyApp.Worker[0]\n      Tick 1\nwarn: MyApp.Worker[0]\n      Tick 2\n"
	_, _, err := collect(t, NewConsole(), data, baseOpts(time.UTC))
	var q *importer.QuarantineError
	if !errors.As(err, &q) || q.Reason != importer.ReasonUnsupportedFormat {
		t.Fatalf("want unsupported-format, got %v", err)
	}
}

func TestConsoleHeartbeatOnlyIsPlaced(t *testing.T) {
	data := "info: MyApp.Worker[0]\n      Tick 1\n" +
		"warn: Microsoft.AspNetCore.Server.Kestrel[22]\n" +
		"      As of \"03/04/2001 05:06:08 +00:00\", the heartbeat has been running for \"00:00:01.5000000\" which is longer than \"00:00:01\".\n" +
		"info: MyApp.Worker[0]\n      Tick 2\n"
	evs, rep, err := collect(t, NewConsole(), data, baseOpts(time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(byKind(evs, "heartbeat-late")) != 1 {
		t.Fatalf("events: %+v", evs)
	}
	for _, e := range evs {
		if e.Line != 3 {
			t.Errorf("event from an untimed line: %+v", e)
		}
	}
	if rep.Rows != 3 || !hasGap(rep, GapNoTime) {
		t.Errorf("rows=%d gaps=%v", rep.Rows, rep.KnownGaps)
	}
}

func TestConsoleSingleLine(t *testing.T) {
	data := string(readFixture(t, "console_single.log"))
	evs, rep, err := collect(t, NewConsole(), data, baseOpts(nil))
	if err != nil {
		t.Fatal(err)
	}
	hb := byKind(evs, "heartbeat-late")
	if len(hb) != 1 || *hb[0].DurationMs != 2250 {
		t.Fatalf("heartbeat: %+v", hb)
	}
	var fail bool
	for _, e := range evs {
		if e.Kind == "log-entry" && e.Attrs["level"] == "fail" {
			fail = true
			if e.Attrs["exception"] == "" {
				t.Errorf("no exception on the fail line: %+v", e)
			}
		}
	}
	if !fail {
		t.Error("fail line not placed")
	}
	if !hasGap(rep, GapSingleLine) {
		t.Error("single-line gap missing")
	}
	noLeak(t, evs, rep, nil, "Order step failed")
}

func TestConsoleFormats(t *testing.T) {
	ny := zone(t, "America/New_York")
	cases := []struct {
		stamp string
		want  string
	}{
		{"2001-03-04T05:06:07Z", "2001-03-04 05:06:07.000"},
		{"2001-03-04T05:06:07.250-05:00", "2001-03-04 10:06:07.250"},
		{"2001-03-04 05:06:07", "2001-03-04 10:06:07.000"},
		{"03/04/2001 05:06:07", "2001-03-04 10:06:07.000"},
		{"[2001-03-04 05:06:07]", "2001-03-04 10:06:07.000"},
		{"03/04/2001 5:06:07 PM", "2001-03-04 22:06:07.000"},
	}
	for _, c := range cases {
		data := c.stamp + " info: Microsoft.Hosting.Lifetime[0]\n      Application started.\n"
		evs, _, err := collect(t, NewConsole(), data, baseOpts(ny))
		if err != nil {
			t.Errorf("%s: %v", c.stamp, err)
			continue
		}
		st := byKind(evs, "app-start")
		if len(st) != 1 || !st[0].Start.Equal(utc(c.want)) {
			t.Errorf("%s: got %+v want %s", c.stamp, st, c.want)
		}
	}
}

func TestConsoleUnreadableTimeRejected(t *testing.T) {
	o := baseOpts(time.UTC)
	o.Limits = importer.Limits{MaxRejectFraction: 1}
	data := "2001-03-04 05:06:07 info: MyApp.Worker[0]\n      ok\n" +
		"2001-13-45 99:99:99 info: MyApp.Worker[0]\n      " + sentinel + "\n" +
		"2001-03-04 05:06:09 warn: MyApp.Worker[0]\n      ok\n"
	evs, rep, err := collect(t, NewConsole(), data, o)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rejected != 1 || rep.Errors[0].Reason != importer.ReasonBadTime || rep.Errors[0].Line != 3 {
		t.Errorf("rejected=%d errors=%v", rep.Rejected, rep.Errors)
	}
	noLeak(t, evs, rep, nil)
}

func TestConsoleStrayLinesAreNotMessages(t *testing.T) {
	data := "garbage before the first entry\n" +
		"2001-03-04 05:06:07 info: Microsoft.Hosting.Lifetime[0]\n      Application started. Press Ctrl+C to shut down.\n" +
		"      => scope " + sentinel + "\n"
	evs, rep, err := collect(t, NewConsole(), data, baseOpts(time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rows != 2 || rep.Rejected != 0 {
		t.Errorf("rows=%d rejected=%d", rep.Rows, rep.Rejected)
	}
	if len(byKind(evs, "app-start")) != 1 || !hasGap(rep, GapNoTime) {
		t.Errorf("events=%+v gaps=%v", evs, rep.KnownGaps)
	}
	noLeak(t, evs, rep, nil, "garbage")
}

func TestJSONBasic(t *testing.T) {
	evs, rep, err := collect(t, NewJSON(), string(readFixture(t, "json_basic.log")), baseOpts(nil))
	if err != nil {
		t.Fatal(err)
	}
	hb := byKind(evs, "heartbeat-late")
	if len(hb) != 1 || *hb[0].DurationMs != 1500 {
		t.Fatalf("heartbeat: %+v", hb)
	}
	if len(byKind(evs, "app-start")) != 1 {
		t.Errorf("app-start events: %d", len(byKind(evs, "app-start")))
	}
	req := byKind(evs, "app-request")
	if len(req) != 1 || *req[0].DurationMs != 1834.2 || req[0].Attrs["status"] != "200" {
		t.Errorf("app-request: %+v", req)
	}
	if rep.Rows != 6 || rep.Rejected != 0 || rep.FieldRules["timestamp"] != clock.RuleUTCField {
		t.Errorf("rows=%d rejected=%d rules=%v", rep.Rows, rep.Rejected, rep.FieldRules)
	}
	noLeak(t, evs, rep, nil, "Order step failed", "OrderService.cs", "OriginalFormat", "web-a.example.test")
}

func TestJSONTimestampRequired(t *testing.T) {
	data := `{"EventId":0,"LogLevel":"Information","Category":"MyApp.Worker","Message":"Tick 1","State":{}}` + "\n" +
		`{"EventId":0,"LogLevel":"Warning","Category":"MyApp.Worker","Message":"Tick 2","State":{}}` + "\n"
	evs, _, err := collect(t, NewJSON(), data, baseOpts(time.UTC))
	var q *importer.QuarantineError
	if !errors.As(err, &q) || q.Reason != importer.ReasonUnsupportedFormat || len(evs) != 0 {
		t.Fatalf("want unsupported-format with no events, got %v (%d events)", err, len(evs))
	}
}

func TestJSONLocalTimeNeedsZone(t *testing.T) {
	data := `{"Timestamp":"2001-03-04T05:06:07","EventId":0,"LogLevel":"Information","Category":"Microsoft.Hosting.Lifetime","Message":"Application started."}` + "\n"
	if _, _, err := collect(t, NewJSON(), data, baseOpts(nil)); !errors.Is(err, importer.ErrTimezoneUnlabelled) {
		t.Fatalf("want tz-unlabelled, got %v", err)
	}
	evs, _, err := collect(t, NewJSON(), data, baseOpts(zone(t, "America/New_York")))
	if err != nil || len(byKind(evs, "app-start")) != 1 || !byKind(evs, "app-start")[0].Start.Equal(utc("2001-03-04 10:06:07.000")) {
		t.Fatalf("err=%v events=%+v", err, evs)
	}
}

func TestJSONBadLinesRejected(t *testing.T) {
	o := baseOpts(time.UTC)
	o.Limits = importer.Limits{MaxRejectFraction: 1}
	data := `{"Timestamp":"2001-03-04T05:06:07Z","EventId":0,"LogLevel":"Information","Category":"Microsoft.Hosting.Lifetime","Message":"Application started."}` + "\n" +
		"not json " + sentinel + "\n" +
		`{"Timestamp":"2001-03-04T05:06:08Z","LogLevel":"Information"}` + "\n" +
		`{"Timestamp":"yesterday","EventId":0,"LogLevel":"Information","Category":"MyApp.Worker"}` + "\n"
	evs, rep, err := collect(t, NewJSON(), data, o)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rows != 4 || rep.Rejected != 3 || len(byKind(evs, "app-start")) != 1 {
		t.Errorf("rows=%d rejected=%d events=%d", rep.Rows, rep.Rejected, len(evs))
	}
	noLeak(t, evs, rep, nil)
}

func TestStdoutBasic(t *testing.T) {
	ny := zone(t, "America/New_York")
	o := baseOpts(ny)
	o.FileName = `C:\inetpub\logs\` + stdoutName
	evs, rep, err := collect(t, NewStdout(), string(readFixture(t, "stdout_basic.log")), o)
	if err != nil {
		t.Fatal(err)
	}
	ss := byKind(evs, "app-session")
	if len(ss) != 1 {
		t.Fatalf("app-session events: %d", len(ss))
	}
	s := ss[0]
	if !s.Start.Equal(utc("2001-03-04 10:06:07.000")) || !s.End.Equal(s.Start) {
		t.Errorf("session time: %v - %v", s.Start, s.End)
	}
	if s.Severity != "critical" || s.Attrs["level"] != "crit" || s.Attrs["exception"] == "" {
		t.Errorf("session: %+v", s)
	}
	if s.TZRule != clock.RuleHostLocal || s.Host != "web-a" || s.Line != 11 {
		t.Errorf("session: rule=%q host=%q line=%d", s.TZRule, s.Host, s.Line)
	}
	hb := byKind(evs, "heartbeat-late")
	if len(hb) != 1 || *hb[0].DurationMs != 1500 || !hb[0].End.Equal(utc("2001-03-04 05:06:30.000")) {
		t.Errorf("heartbeat: %+v", hb)
	}
	// Lines with no time are counted and never placed.
	if rep.Rows != 5 || !hasGap(rep, GapNoTime) || !hasGap(rep, GapStdoutTime) {
		t.Errorf("rows=%d gaps=%v", rep.Rows, rep.KnownGaps)
	}
	for _, e := range evs {
		// Only the heartbeat line (line 5) and the session point are placed.
		if e.Kind != "app-session" && e.Line != 5 {
			t.Errorf("untimed line placed: %+v", e)
		}
	}
	noLeak(t, evs, rep, nil, "1234", "Object reference", "Program.cs", "Order step failed")
}

func TestStdoutNoCrash(t *testing.T) {
	data := "info: MyApp.Worker[0]\n      Tick 1\n"
	evs, rep, err := collect(t, NewStdout(), data, withName(baseOpts(time.UTC), stdoutName))
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 || evs[0].Kind != "app-session" || evs[0].Severity != "info" || len(evs[0].Attrs) != 0 || evs[0].Line != 1 {
		t.Fatalf("events: %+v", evs)
	}
	if rep.Emitted != 1 || rep.First.IsZero() || !rep.First.Equal(evs[0].Start) {
		t.Errorf("emitted=%d first=%v", rep.Emitted, rep.First)
	}
}

func withName(o importer.Options, name string) importer.Options {
	o.FileName = name
	return o
}

func TestStdoutFileNameRules(t *testing.T) {
	data := "info: MyApp.Worker[0]\n      Tick 1\n"
	for _, name := range []string{"", "fixture.txt", "stdout.log", "stdout_2001030405_12.log", "stdout_20010304050607_x.log",
		"stdout_20010304050607_1234.log.gz", "stdout_20010304050607_99999999999.log"} {
		_, _, err := collect(t, NewStdout(), data, withName(baseOpts(time.UTC), name))
		var q *importer.QuarantineError
		if !errors.As(err, &q) || q.Reason != importer.ReasonUnsupportedFormat {
			t.Errorf("%q: want unsupported-format, got %v", name, err)
		}
	}
	// A stated time that is no date.
	_, _, err := collect(t, NewStdout(), data, withName(baseOpts(time.UTC), "stdout_20011345995959_1.log"))
	if err == nil {
		t.Error("impossible file-name time accepted")
	}
}

func TestStdoutZoneRules(t *testing.T) {
	data := "info: MyApp.Worker[0]\n      Tick 1\n"
	if _, _, err := collect(t, NewStdout(), data, withName(baseOpts(nil), stdoutName)); !errors.Is(err, importer.ErrTimezoneUnlabelled) {
		t.Errorf("want tz-unlabelled, got %v", err)
	}
	o := withName(baseOpts(nil), stdoutName)
	o.Host = nil
	if _, _, err := collect(t, NewStdout(), data, o); !errors.Is(err, importer.ErrUnknownHost) {
		t.Errorf("want unknown host, got %v", err)
	}
}

func TestStdoutDSTFoldFileName(t *testing.T) {
	ny := zone(t, "America/New_York")
	data := "info: MyApp.Worker[0]\n      Tick 1\n"
	evs, rep, err := collect(t, NewStdout(), data, withName(baseOpts(ny), "stdout_20011028013000_77.log"))
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 0 || !hasGap(rep, GapStdoutNameTime) {
		t.Errorf("events=%+v gaps=%v", evs, rep.KnownGaps)
	}
}

func TestStdoutWindow(t *testing.T) {
	data := "info: MyApp.Worker[0]\n      Tick 1\n"
	o := withName(baseOpts(time.UTC), stdoutName)
	o.Window = importer.Window{From: utc("2001-03-05 00:00:00.000")}
	evs, _, err := collect(t, NewStdout(), data, o)
	if err != nil || len(evs) != 0 {
		t.Fatalf("err=%v events=%+v", err, evs)
	}
}

func TestStdoutNotConsoleText(t *testing.T) {
	for _, data := range []string{"", "x", "just some text\nmore text\n", "Server started\n"} {
		_, _, err := collect(t, NewStdout(), data, withName(baseOpts(time.UTC), stdoutName))
		if err == nil {
			t.Errorf("%q accepted", data)
		}
	}
}

func TestStdoutOnlyFirstCrashBlock(t *testing.T) {
	data := "info: MyApp.Worker[0]\n      Tick 1\n" +
		"Unhandled exception. System.InvalidOperationException: first\n   at A.B()\n" +
		"Unhandled exception. System.NullReferenceException: second\n   at C.D()\n"
	evs, rep, err := collect(t, NewStdout(), data, withName(baseOpts(time.UTC), stdoutName))
	if err != nil {
		t.Fatal(err)
	}
	ss := byKind(evs, "app-session")
	if len(ss) != 1 || ss[0].Line != 3 || ss[0].Severity != "critical" {
		t.Fatalf("session: %+v", ss)
	}
	if !hasGap(rep, GapStdoutCrash) {
		t.Error("crash gap missing")
	}
}

func TestDetect(t *testing.T) {
	console := readFixture(t, "console_basic.log")
	jsonl := readFixture(t, "json_basic.log")
	stdout := readFixture(t, "stdout_basic.log")
	single := readFixture(t, "console_single.log")
	c, j, s := NewConsole(), NewJSON(), NewStdout()
	type row struct {
		name             string
		imp              importer.Importer
		head             []byte
		file             string
		wantMin, wantMax int
	}
	rows := []row{
		{"console on console", c, console, "x.log", 80, 100},
		{"console on single-line", c, single, "x.log", 80, 100},
		{"console on json", c, jsonl, "x.log", 0, 0},
		{"console on stdout text", c, stdout, "x.log", 0, 20},
		{"console on stdout name", c, console, stdoutName, 1, 50},
		{"json on json", j, jsonl, "x.log", 80, 100},
		{"json on console", j, console, "x.log", 0, 0},
		{"json on stdout", j, stdout, stdoutName, 0, 0},
		{"stdout on stdout", s, stdout, stdoutName, 80, 100},
		{"stdout on stdout, any name", s, stdout, "fixture.txt", 1, 20},
		{"stdout on console", s, console, "x.log", 0, 0},
		{"stdout on json", s, jsonl, stdoutName, 0, 0},
		{"empty", c, nil, "x.log", 0, 0},
	}
	for _, r := range rows {
		got := r.imp.Detect(r.head, r.file)
		if got < r.wantMin || got > r.wantMax {
			t.Errorf("%s: %d not in [%d,%d]", r.name, got, r.wantMin, r.wantMax)
		}
	}
	// A cut head never panics.
	for i := 0; i <= len(console); i += 7 {
		_ = c.Detect(console[:i], "")
		_ = j.Detect(jsonl[:min(i, len(jsonl))], "")
		_ = s.Detect(stdout[:min(i, len(stdout))], stdoutName)
	}
}

func TestParseStamp(t *testing.T) {
	if _, _, why := parseStamp("2001-03-04 05:06:07", nil); why != importer.ReasonTZUnlabelled {
		t.Errorf("local with no zone: %q", why)
	}
	for _, p := range []string{"", "x", "SENTINEL-7f3a", "1850-01-01T00:00:00Z", "2001-02-30T00:00:00Z", strings.Repeat("1", 60)} {
		if _, _, why := parseStamp(p, time.UTC); why != importer.ReasonBadTime {
			t.Errorf("%q: %q", p, why)
		}
	}
}
