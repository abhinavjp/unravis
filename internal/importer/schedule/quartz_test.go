package schedule

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/importertest"
)

func quartzOpts(t testing.TB) importer.Options {
	o := hangfireOpts()
	o.Host = &importer.HostHint{Label: "web-a", Role: "app", TZ: berlin(t), Runtime: "quartz-3.8"}
	return o
}

// quartzBad plants the harness sentinel in the message of a line with an
// impossible time, in a job name a row refuses, and in a line that is not a
// whole header.
func quartzBad() []byte {
	return []byte("2001-03-24 05:06:07,123 [W-1] INFO  Quartz.Core.JobRunShell - Job DEFAULT.ok-job execution complete in 5 ms\n" +
		"2001-03-24 99:06:07,123 [W-1] INFO  Quartz.Core.JobRunShell - Job DEFAULT." + importertest.Sentinel + " execution complete\n" +
		"2001-03-24 05:06:08,123 [W-1] INFO  Quartz.Core.JobRunShell - Job DEFAULT.=" + importertest.Sentinel + " execution complete\n" +
		"2001-03-24 05:06:09 " + importertest.Sentinel + "\n")
}

func quartzFixtures(t testing.TB) importertest.Fixtures {
	return importertest.Fixtures{
		Good: fixture(t, "quartz_good.log"),
		Bad:  quartzBad(),
		// Job, group, trigger and scheduler names are identifiers kept whole by
		// design; a name that starts with = + - @ is refused, not kept (see
		// TestQuartzFormulaNamesRefused), so no free text can start a formula.
		NoFreeText:      true,
		KeepsCellValues: true,
		Options:         quartzOptsFor(t),
	}
}

func quartzOptsFor(t testing.TB) importer.Options {
	o := quartzOpts(t)
	return importer.Options{Window: win(), Host: o.Host}
}

func TestQuartzConformance(t *testing.T) {
	importertest.Run(t, NewQuartz(), quartzFixtures(t))
}

func FuzzQuartz(f *testing.F) {
	importertest.FuzzParse(f, NewQuartz(), quartzFixtures(f))
}

func TestQuartzMetadata(t *testing.T) {
	q := NewQuartz()
	if q.Name() != "schedule-quartz" {
		t.Fatalf("name = %s", q.Name())
	}
	if len(q.AllowedAttrs()) > 16 {
		t.Fatalf("%d attrs", len(q.AllowedAttrs()))
	}
	l := q.DefaultLimits()
	if l.MaxBytes != 2<<30 || l.MaxLineBytes != 64<<10 {
		t.Fatalf("limits = %+v", l)
	}
	if _, ok := importer.Importer(q).(importer.SuppliedKeyer); !ok {
		t.Fatal("quartz must declare supplied keys")
	}
	for _, g := range []string{GapQuartzLocalTime, GapQuartzUTC, GapQuartzPreamble, GapQuartzExcCap, GapQuartzScope, GapQuartzWording, GapQuartzNoDur, GapQuartzEmptyPoll} {
		if err := evidence.LintCausal(g); err != nil {
			t.Errorf("gap %q: %v", g, err)
		}
	}
}

func TestQuartzGolden(t *testing.T) {
	ev, rep := mustParse(t, NewQuartz(), fixture(t, "quartz_good.log"), quartzOpts(t))
	type row struct {
		kind, start, sev string
		attrs            map[string]string
		dur              float64
	}
	want := []row{
		{KindSchedulerStart, "2001-03-24T04:06:00Z", "info", map[string]string{"scheduler": "web-a-sched_$_NON_CLUSTERED", "level": "INFO", "thread": "main"}, -1},
		{KindTriggerBatch, "2001-03-24T04:06:07Z", "info", map[string]string{"count": "2", "thread": "QuartzScheduler_Worker-1"}, -1},
		{KindJobRun, "2001-03-24T04:06:05.95Z", "info", map[string]string{"job": "nightly-sync", "group": "DEFAULT", "outcome": "complete"}, 1250},
		{KindJobRun, "2001-03-24T04:06:08Z", "error", map[string]string{"job": "report-build", "group": "DEFAULT", "outcome": "failed", "exception": "Quartz.JobExecutionException,System.InvalidOperationException"}, -1},
		{KindTriggerMisfire, "2001-03-24T04:06:10Z", "warning", map[string]string{"trigger": "nightly-trg", "group": "DEFAULT"}, -1},
		{KindTriggerMisfire, "2001-03-24T04:06:11Z", "warning", map[string]string{"count": "1", "thread": "MisfireHandler"}, -1},
		{KindSchedulerFault, "2001-03-24T04:06:12Z", "error", map[string]string{"exception": "Quartz.JobPersistenceException,System.Data.SqlClient.SqlException"}, -1},
		{KindSchedulerStop, "2001-03-24T04:06:14Z", "info", map[string]string{"scheduler": "web-a-sched_$_NON_CLUSTERED"}, -1},
	}
	if len(ev) != len(want) {
		for _, e := range ev {
			t.Logf("%s %s %v", e.Kind, e.Start, e.Attrs)
		}
		t.Fatalf("%d events, want %d", len(ev), len(want))
	}
	for i, w := range want {
		e := ev[i]
		if e.Kind != w.kind || e.Severity != w.sev || e.Host != "web-a" || e.TZRule != clock.RuleHostLocal {
			t.Errorf("%d: %s sev=%s host=%s rule=%s, want %s %s", i, e.Kind, e.Severity, e.Host, e.TZRule, w.kind, w.sev)
		}
		got := e.Start.UTC().Truncate(time.Millisecond).Format("2006-01-02T15:04:05.999999999Z")
		if !strings.HasPrefix(got, w.start[:len(w.start)-1]) && got != w.start {
			t.Errorf("%d: start %s, want %s", i, got, w.start)
		}
		for k, v := range w.attrs {
			if e.Attrs[k] != v {
				t.Errorf("%d: attr %s = %q, want %q", i, k, e.Attrs[k], v)
			}
		}
		if w.dur >= 0 {
			if e.DurationMs == nil || *e.DurationMs != w.dur {
				t.Errorf("%d: duration = %v, want %v", i, e.DurationMs, w.dur)
			}
		} else if e.DurationMs != nil {
			t.Errorf("%d: unexpected duration %v", i, *e.DurationMs)
		}
	}
	if rep.Quality.TimezoneRule != clock.RuleHostLocal || rep.Quality.SourceVersion != "quartz-3.8" {
		t.Errorf("quality = %+v", rep.Quality)
	}
	if rep.FieldRules["timestamp"] != clock.RuleHostLocal {
		t.Errorf("rules = %v", rep.FieldRules)
	}
	if rep.Rejected != 0 {
		t.Errorf("rejected = %d: %+v", rep.Rejected, rep.Errors)
	}
	for _, g := range []string{GapQuartzLocalTime, GapQuartzPreamble, GapQuartzScope, GapQuartzWording, GapQuartzNoDur, GapQuartzEmptyPoll} {
		if !hasGap(rep, g) {
			t.Errorf("missing gap %q in %v", g, rep.KnownGaps)
		}
	}
	// The exception text is never stored.
	for _, e := range ev {
		for k, v := range e.Attrs {
			if strings.Contains(v, "synthetic") || strings.Contains(k, "synthetic") {
				t.Errorf("free text kept in %s=%q", k, v)
			}
		}
	}
}

func TestQuartzMEL(t *testing.T) {
	ev, rep := mustParse(t, NewQuartz(), fixture(t, "quartz_mel.log"), quartzOpts(t))
	var kinds []string
	for _, e := range ev {
		kinds = append(kinds, e.Kind)
	}
	want := []string{KindSchedulerStart, KindJobRun, KindJobRun, KindTriggerMisfire}
	if strings.Join(kinds, ",") != strings.Join(want, ",") {
		t.Fatalf("kinds = %v, want %v", kinds, want)
	}
	if e := ev[1]; e.Attrs["job"] != "nightly-sync" || e.DurationMs == nil || *e.DurationMs != 1250 {
		t.Errorf("scope line was not skipped: %+v", e.Attrs)
	}
	if e := ev[2]; e.Attrs["outcome"] != "failed" || e.Severity != "error" || e.Attrs["exception"] != "Quartz.JobExecutionException" {
		t.Errorf("failure = %+v sev=%s", e.Attrs, e.Severity)
	}
	if ev[3].Attrs["trigger"] != "nightly-trg" {
		t.Errorf("single-line header message: %+v", ev[3].Attrs)
	}
	if rep.Rejected != 0 {
		t.Errorf("rejected = %d %+v", rep.Rejected, rep.Errors)
	}
}

func TestQuartzMELWithoutTimestampRefused(t *testing.T) {
	data := []byte("info: Quartz.Core.JobRunShell[0]\n      Job DEFAULT.nightly-sync execution complete in 5 ms\n" +
		"info: Quartz.Core.QuartzScheduler[0]\n      Scheduler s1 started.\n")
	ev, _, err := parse(NewQuartz(), data, quartzOpts(t))
	wantQuarantine(t, err, importer.ReasonUnsupportedFormat, "")
	if len(ev) != 0 {
		t.Fatalf("%d events from a layout with no time", len(ev))
	}
	if s := NewQuartz().Detect(data, "x.log"); s != 0 {
		t.Errorf("detect = %d for a MEL file without time stamps", s)
	}
}

func TestQuartzOtherLog4netRefused(t *testing.T) {
	data := []byte("2001-03-24 05:06:07,123 [Worker-1] INFO  MyApp.Jobs.SyncJob - Job DEFAULT.nightly-sync execution complete\n" +
		"2001-03-24 05:06:08,123 [Worker-1] WARN  MyApp.Jobs.SyncJob - Trigger DEFAULT.x misfired\n")
	if s := NewQuartz().Detect(data, "app.log"); s != 0 {
		t.Errorf("detect = %d for a non-Quartz logger", s)
	}
	_, _, err := parse(NewQuartz(), data, quartzOpts(t))
	wantQuarantine(t, err, importer.ReasonUnsupportedFormat, "")
}

func TestQuartzUnknownLayoutsRefused(t *testing.T) {
	for name, data := range map[string]string{
		"json":   `{"Timestamp":"2001-03-24T05:06:07Z","Message":"Job DEFAULT.a execution complete"}` + "\n",
		"xml":    "<?xml version=\"1.0\"?><log/>\n",
		"prose":  "this is not a log\nat all\n",
		"serilg": "[05:06:07 INF] Job DEFAULT.a execution complete\n",
	} {
		_, _, err := parse(NewQuartz(), []byte(data), quartzOpts(t))
		if err == nil {
			t.Errorf("%s: no error", name)
			continue
		}
		var q *importer.QuarantineError
		if !errors.As(err, &q) {
			t.Errorf("%s: %v", name, err)
		}
		if s := NewQuartz().Detect([]byte(data), ""); s != 0 {
			t.Errorf("%s: detect = %d", name, s)
		}
	}
}

func TestQuartzDetectMargin(t *testing.T) {
	good := fixture(t, "quartz_good.log")
	if s := NewQuartz().Detect(good, "quartz.log"); s != 97 {
		t.Fatalf("detect = %d", s)
	}
	if s := NewQuartz().Detect(fixture(t, "quartz_mel.log"), "quartz.log"); s != 97 {
		t.Fatalf("mel detect = %d", s)
	}
	// Mostly application lines with one stray Quartz line is not a Quartz log.
	var sb strings.Builder
	for i := 0; i < 9; i++ {
		fmt.Fprintf(&sb, "2001-03-24 05:06:%02d,000 [W-1] INFO  MyApp.Svc - line %d\n", i, i)
	}
	sb.WriteString("2001-03-24 05:06:30,000 [W-1] INFO  Quartz.Core.QuartzScheduler - Scheduler s1 started.\n")
	if s := NewQuartz().Detect([]byte(sb.String()), ""); s != 0 {
		t.Errorf("detect = %d for a mostly-application log", s)
	}
}

func TestQuartzNeedsHostAndZone(t *testing.T) {
	data := fixture(t, "quartz_good.log")
	o := quartzOpts(t)
	o.Host = nil
	if _, _, err := parse(NewQuartz(), data, o); !errors.Is(err, importer.ErrUnknownHost) {
		t.Errorf("no host: %v", err)
	}
	o = quartzOpts(t)
	o.Host.TZ = nil
	_, _, err := parse(NewQuartz(), data, o)
	var q *importer.QuarantineError
	if !errors.As(err, &q) || q.Reason != importer.ReasonTZUnlabelled {
		t.Errorf("no zone: %v", err)
	}
}

func TestQuartzUTCStated(t *testing.T) {
	o := quartzOpts(t)
	o.Host.TZ = nil // not needed when the owner states UTC
	o.Supplied = map[string]string{SuppliedLogTime: "utc"}
	ev, rep := mustParse(t, NewQuartz(), fixture(t, "quartz_good.log"), o)
	if len(ev) == 0 || ev[0].Start.Format(time.RFC3339) != "2001-03-24T05:06:00Z" || ev[0].TZRule != clock.RuleUTCField {
		t.Fatalf("first = %+v", ev[0])
	}
	if rep.Quality.TimezoneRule != clock.RuleUTCField || !hasGap(rep, GapQuartzUTC) || hasGap(rep, GapQuartzLocalTime) {
		t.Errorf("quality = %+v gaps = %v", rep.Quality, rep.KnownGaps)
	}
}

func TestQuartzDSTTimesRejected(t *testing.T) {
	// Europe/Berlin: 02:30 on 2001-03-25 does not exist; 02:30 on 2001-10-28 is ambiguous.
	data := []byte("2001-03-25 01:30:00,000 [W-1] INFO  Quartz.Core.QuartzScheduler - Scheduler s1 started.\n" +
		"2001-03-25 02:30:00,000 [W-1] INFO  Quartz.Core.QuartzSchedulerThread - Batch acquisition of 1 triggers\n" +
		"2001-03-25 03:30:00,000 [W-1] INFO  Quartz.Core.QuartzScheduler - Scheduler s1 shutdown complete.\n")
	o := lenient(quartzOpts(t))
	ev, rep := mustParse(t, NewQuartz(), data, o)
	if len(ev) != 2 {
		t.Fatalf("%d events", len(ev))
	}
	if !hasReason(rep, 2, importer.ReasonDSTNonexistent) {
		t.Errorf("errors = %+v", rep.Errors)
	}
	amb := []byte("2001-10-28 02:30:00,000 [W-1] INFO  Quartz.Core.QuartzScheduler - Scheduler s1 started.\n" +
		"2001-10-28 04:30:00,000 [W-1] INFO  Quartz.Core.QuartzScheduler - Scheduler s1 shutdown complete.\n")
	o = lenient(quartzOpts(t))
	o.Window = importer.Window{}
	ev, rep = mustParse(t, NewQuartz(), amb, o)
	if len(ev) != 1 || !hasReason(rep, 1, importer.ReasonDSTAmbiguous) {
		t.Errorf("%d events, errors = %+v", len(ev), rep.Errors)
	}
}

func TestQuartzFormulaNamesRefused(t *testing.T) {
	for _, lead := range []string{"=", "+", "-", "@"} {
		data := []byte("2001-03-24 05:06:07,000 [W-1] INFO  Quartz.Core.JobRunShell - Job DEFAULT." + lead + "cmd execution complete\n" +
			"2001-03-24 05:06:08,000 [W-1] INFO  Quartz.Core.JobRunShell - Job " + lead + "grp.cmd execution complete\n" +
			"2001-03-24 05:06:09,000 [W-1] INFO  Quartz.Core.JobRunShell - Job DEFAULT.ok execution complete\n")
		ev, rep := mustParse(t, NewQuartz(), data, lenient(quartzOpts(t)))
		if len(ev) != 1 || ev[0].Attrs["job"] != "ok" {
			t.Errorf("%q: events = %d", lead, len(ev))
		}
		if rep.Rejected != 2 {
			t.Errorf("%q: rejected = %d", lead, rep.Rejected)
		}
		for _, e := range ev {
			for _, v := range e.Attrs {
				if v != "" && strings.ContainsAny(v[:1], "=+-@\t\r") {
					t.Errorf("formula start kept: %q", v)
				}
			}
		}
	}
	// A name with a safe inner symbol is kept whole.
	data := []byte("2001-03-24 05:06:07,000 [W-1] INFO  Quartz.Core.JobRunShell - Job DEFAULT.nightly-sync+2 execution complete\n")
	ev, _ := mustParse(t, NewQuartz(), data, quartzOpts(t))
	if len(ev) != 1 || ev[0].Attrs["job"] != "nightly-sync+2" {
		t.Errorf("inner symbol: %+v", ev)
	}
	// A key with no group is a job name alone.
	data = []byte("2001-03-24 05:06:07,000 [W-1] INFO  Quartz.Core.JobRunShell - Job solo execution failed\n")
	ev, _ = mustParse(t, NewQuartz(), data, quartzOpts(t))
	if len(ev) != 1 || ev[0].Attrs["job"] != "solo" || ev[0].Attrs["group"] != "" {
		t.Errorf("no group: %+v", ev)
	}
}

func TestQuartzFailureSeverityFloors(t *testing.T) {
	data := []byte("2001-03-24 05:06:07,000 [W-1] INFO  Quartz.Core.JobRunShell - Job DEFAULT.a execution failed\n" +
		"2001-03-24 05:06:08,000 [W-1] DEBUG Quartz.Core.QuartzSchedulerThread - Trigger DEFAULT.t misfired\n")
	ev, _ := mustParse(t, NewQuartz(), data, quartzOpts(t))
	if len(ev) != 2 || ev[0].Severity != "error" || ev[1].Severity != "warning" {
		t.Fatalf("%+v", ev)
	}
}

func TestQuartzLineEndingsAndBOM(t *testing.T) {
	lf := fixture(t, "quartz_good.log")
	crlf := bytes.ReplaceAll(lf, []byte("\n"), []byte("\r\n"))
	bom := append([]byte{0xEF, 0xBB, 0xBF}, lf...)
	cr := bytes.ReplaceAll(lf, []byte("\n"), []byte("\r"))
	base, _ := mustParse(t, NewQuartz(), lf, quartzOpts(t))
	for name, data := range map[string][]byte{"crlf": crlf, "bom": bom, "bom-crlf": append([]byte{0xEF, 0xBB, 0xBF}, crlf...)} {
		ev, _ := mustParse(t, NewQuartz(), data, quartzOpts(t))
		if len(ev) != len(base) {
			t.Errorf("%s: %d events, want %d", name, len(ev), len(base))
			continue
		}
		for i := range ev {
			if ev[i].EvidenceID != base[i].EvidenceID || ev[i].Kind != base[i].Kind || ev[i].Attrs["exception"] != base[i].Attrs["exception"] {
				t.Errorf("%s: event %d differs", name, i)
			}
		}
	}
	// A bare CR is not a line ending: the file is one line and is refused or yields nothing, never guessed.
	if ev, _, err := parse(NewQuartz(), cr, quartzOpts(t)); err == nil && len(ev) > 1 {
		t.Errorf("bare CR file read as %d events", len(ev))
	}
}

func utf16le(s string) []byte {
	u := utf16.Encode([]rune(s))
	b := []byte{0xFF, 0xFE}
	for _, c := range u {
		b = append(b, byte(c), byte(c>>8))
	}
	return b
}

func TestQuartzUTF16(t *testing.T) {
	lf := fixture(t, "quartz_good.log")
	base, _ := mustParse(t, NewQuartz(), lf, quartzOpts(t))
	ev, _ := mustParse(t, NewQuartz(), utf16le(string(lf)), quartzOpts(t))
	if len(ev) != len(base) {
		t.Fatalf("utf-16: %d events, want %d", len(ev), len(base))
	}
}

func TestQuartzTruncated(t *testing.T) {
	good := fixture(t, "quartz_good.log")
	for _, n := range []int{0, 1, 5, 40, 120, 200, 333, len(good) / 2, len(good) - 7, len(good) - 1} {
		ev, rep, err := parse(NewQuartz(), good[:n], lenient(quartzOpts(t)))
		if err != nil && len(ev) != 0 {
			t.Errorf("cut %d: error %v with %d events", n, err, len(ev))
		}
		for _, e := range ev {
			if e.Start.IsZero() {
				t.Errorf("cut %d: zero time", n)
			}
		}
		_ = rep
	}
	// A header cut inside its time stamp is never read as an entry: it is text
	// after the previous entry and adds no event.
	cut := []byte("2001-03-24 05:06:00,010 [main] INFO  Quartz.Core.QuartzScheduler - Scheduler s1 started.\n2001-03-24 05:0")
	ev, _ := mustParse(t, NewQuartz(), cut, lenient(quartzOpts(t)))
	if len(ev) != 1 {
		t.Errorf("events = %d", len(ev))
	}
	// A header cut after its time stamp but inside the level is rejected.
	cut = []byte("2001-03-24 05:06:00,010 [main] INFO  Quartz.Core.QuartzScheduler - Scheduler s1 started.\n2001-03-24 05:06:01,010 [main] IN")
	ev, rep := mustParse(t, NewQuartz(), cut, lenient(quartzOpts(t)))
	if len(ev) != 1 || rep.Rejected != 1 {
		t.Errorf("events = %d rejected = %d", len(ev), rep.Rejected)
	}
}

func TestQuartzMalformedLines(t *testing.T) {
	data := []byte("2001-03-24 05:06:00,010 [main] INFO  Quartz.Core.QuartzScheduler - Scheduler s1 started.\n" +
		"2001-03-24 05:06:01,010 [main] LOUD  Quartz.Core.QuartzScheduler - Scheduler s1 started.\n" + // unknown level
		"2001-13-24 05:06:02,010 [main] INFO  Quartz.Core.QuartzScheduler - Scheduler s1 started.\n" + // month 13
		"2001-03-24 05:06:03,010 main INFO  Quartz.Core.QuartzScheduler - Scheduler s1 started.\n" + // no brackets
		"1812-03-24 05:06:04,010 [main] INFO  Quartz.Core.QuartzScheduler - Scheduler s1 started.\n" + // not a sane year
		"2001-03-24 05:06:05,010 [main] INFO  Quartz.Core.JobRunShell - Job DEFAULT. execution complete\n" + // empty name
		"2001-03-24 05:06:06,010 [main] INFO  Quartz.Core.JobRunShell - Job DEFAULT.a\x01b execution complete\n" + // control character
		"2001-03-24 05:06:07,010 [main] INFO  Quartz.Core.QuartzScheduler - Scheduler s1 shutdown complete.\n")
	ev, rep := mustParse(t, NewQuartz(), data, lenient(quartzOpts(t)))
	if len(ev) != 2 {
		for _, e := range ev {
			t.Logf("%s %v", e.Kind, e.Attrs)
		}
		t.Fatalf("%d events, want 2 (errors %+v)", len(ev), rep.Errors)
	}
	if rep.Rejected != 6 {
		t.Errorf("rejected = %d: %+v", rep.Rejected, rep.Errors)
	}
}

func TestQuartzRejectFractionRefuses(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("2001-03-24 05:06:00,010 [main] INFO  Quartz.Core.QuartzScheduler - Scheduler s1 started.\n")
	for i := 0; i < 30; i++ {
		fmt.Fprintf(&sb, "2001-03-24 99:06:%02d,010 [main] INFO  Quartz.Core.QuartzScheduler - Scheduler s1 started.\n", i%60)
	}
	ev, _, err := parse(NewQuartz(), []byte(sb.String()), quartzOpts(t))
	wantQuarantine(t, err, importer.ReasonRejectFraction, "")
	if len(ev) != 0 {
		t.Errorf("%d events with an error", len(ev))
	}
}

func TestQuartzSingleLine2MB(t *testing.T) {
	// One 2 MB line: no newline anywhere. It is over the line limit, so it is a
	// refused row or a refused file, never an allocation of the whole line.
	line := "2001-03-24 05:06:07,123 [W-1] INFO  Quartz.Core.JobRunShell - Job DEFAULT.a execution complete " + strings.Repeat("x", 2<<20)
	ev, _, err := parse(NewQuartz(), []byte(line), quartzOpts(t))
	var l *importer.LimitError
	if !errors.As(err, &l) || l.Limit != "line-bytes" {
		t.Fatalf("error = %v", err)
	}
	if len(ev) != 0 {
		t.Fatalf("%d events with an error", len(ev))
	}
	// The same line inside a good file refuses the whole file the same way.
	good := fixture(t, "quartz_good.log")
	mixed := append(append([]byte{}, good...), []byte(line+"\n")...)
	ev, _, err = parse(NewQuartz(), mixed, lenient(quartzOpts(t)))
	if !errors.As(err, &l) || l.Limit != "line-bytes" || len(ev) != 0 {
		t.Fatalf("mixed: %v with %d events", err, len(ev))
	}
}

func TestQuartzExceptionTextCapped(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("2001-03-24 05:06:07,000 [W-1] ERROR Quartz.Core.JobRunShell - Job DEFAULT.a threw a JobExecutionException\n")
	sb.WriteString("Quartz.JobExecutionException: x\n")
	for i := 0; i < 5000; i++ {
		sb.WriteString("   at Example.Test.Frame.Call(IJobExecutionContext context) in synthetic.cs:line 1\n")
	}
	sb.WriteString("2001-03-24 05:06:08,000 [W-1] INFO  Quartz.Core.QuartzScheduler - Scheduler s1 started.\n")
	ev, rep := mustParse(t, NewQuartz(), []byte(sb.String()), quartzOpts(t))
	if len(ev) != 2 || ev[0].Attrs["exception"] != "Quartz.JobExecutionException" {
		t.Fatalf("events = %+v", ev)
	}
	if !hasGap(rep, GapQuartzExcCap) {
		t.Errorf("gaps = %v", rep.KnownGaps)
	}
}

func TestQuartzWindowFilter(t *testing.T) {
	data := []byte("2001-03-23 05:06:00,000 [main] INFO  Quartz.Core.QuartzScheduler - Scheduler s1 started.\n" +
		"2001-03-24 05:06:00,000 [main] INFO  Quartz.Core.QuartzScheduler - Scheduler s2 started.\n" +
		"2001-03-30 05:06:00,000 [main] INFO  Quartz.Core.QuartzScheduler - Scheduler s3 started.\n")
	ev, rep := mustParse(t, NewQuartz(), data, quartzOpts(t))
	if len(ev) != 1 || ev[0].Attrs["scheduler"] != "s2" {
		t.Fatalf("%d events", len(ev))
	}
	if rep.Rows != 3 {
		t.Errorf("rows = %d", rep.Rows)
	}
}

func TestQuartzDurationForms(t *testing.T) {
	cases := map[string]float64{
		"execution complete in 1250 ms":         1250,
		"execution complete in 2 seconds":       2000,
		"execution complete, took 0.5 s":        500,
		"execution complete, duration 00:01:02": 62000,
	}
	for tail, ms := range cases {
		data := []byte("2001-03-24 05:06:07,000 [W-1] INFO  Quartz.Core.JobRunShell - Job DEFAULT.a " + tail + "\n")
		ev, _ := mustParse(t, NewQuartz(), data, quartzOpts(t))
		if len(ev) != 1 || ev[0].DurationMs == nil || *ev[0].DurationMs != ms {
			t.Errorf("%q: %+v", tail, ev)
			continue
		}
		if !ev[0].End.After(ev[0].Start) {
			t.Errorf("%q: not an interval", tail)
		}
	}
	// A stated duration that is absurd is not used.
	data := []byte("2001-03-24 05:06:07,000 [W-1] INFO  Quartz.Core.JobRunShell - Job DEFAULT.a execution complete in 999999999 ms\n")
	ev, _ := mustParse(t, NewQuartz(), data, quartzOpts(t))
	if len(ev) != 1 || ev[0].DurationMs != nil {
		t.Errorf("absurd duration kept: %+v", ev)
	}
}

func TestQuartzEventIDsDistinct(t *testing.T) {
	ev, _ := mustParse(t, NewQuartz(), fixture(t, "quartz_good.log"), quartzOpts(t))
	seen := map[string]bool{}
	for _, e := range ev {
		if seen[e.EvidenceID] {
			t.Errorf("duplicate id %s", e.EvidenceID)
		}
		seen[e.EvidenceID] = true
	}
}

func TestQuartzNoLifecycleLines(t *testing.T) {
	data := []byte("2001-03-24 05:06:07,000 [W-1] DEBUG Quartz.Simpl.SimpleJobFactory - Producing instance of Job 'DEFAULT.a'\n")
	_, _, err := parse(NewQuartz(), data, quartzOpts(t))
	wantQuarantine(t, err, importer.ReasonUnsupportedFormat, "")
}
