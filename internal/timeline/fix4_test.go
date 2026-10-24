package timeline

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/mongodlog"
	"api-audit-explorer/internal/join"
)

// Batch-4 review fixes for the timeline (H1, H2, M3, L7). Every file here is
// synthetic: web-a, sql-a, 2001 dates.

// loadQuality imports one synthetic file with a full SourceQuality.
func loadQuality(t *testing.T, c casestore.Case, name, host, tz string, q evidence.SourceQuality, events ...ev) {
	t.Helper()
	cid := string(c.Info().ID)
	meta := casestore.ImportMeta{Importer: name, FileName: name + host + ".log", HostLabel: host, TZRule: tz, AckGating: true, AckText: ack}
	run := func(r io.Reader, emit func(evidence.Event) error) (importer.Report, error) {
		rep := importer.Report{Importer: name, Quality: q}
		if _, err := io.Copy(io.Discard, r); err != nil {
			return rep, err
		}
		for i, e := range events {
			rep.Rows++
			start := at(e.start)
			d := e.dur * 1000
			x := evidence.Event{
				EvidenceID: evidence.EventID(cid, name, "n"+host, int64(i+2), 0),
				Source:     name, Kind: e.kind, Host: host, Start: start, End: start.Add(time.Duration(e.dur * float64(time.Second))),
				DurationMs: &d, Line: int64(i + 2), TZRule: tz,
			}
			if err := emit(x); err != nil {
				return rep, err
			}
			rep.Emitted++
		}
		return rep, nil
	}
	body := bytes.NewReader([]byte(name + "|" + host + "|" + tz + "|" + q.Sampling + q.Threshold + q.SourceVersion))
	if _, err := c.Import(context.Background(), meta, body, run); err != nil {
		t.Fatalf("import %s: %v", name, err)
	}
}

func cover(first, last int) (*time.Time, *time.Time) {
	f, l := at(first), at(last)
	return &f, &l
}

func mongoQuality(mut func(*evidence.SourceQuality)) evidence.SourceQuality {
	f, l := cover(-3600, 3600)
	q := evidence.SourceQuality{
		SourceVersion: "7.0.5", Threshold: "operations slower than 100 ms",
		CoverageFirst: f, CoverageLast: l, TimezoneRule: clock.RuleOffsetInField,
	}
	if mut != nil {
		mut(&q)
	}
	return q
}

func absenceOf(t *testing.T, v View, source string) Row {
	t.Helper()
	for _, r := range rowsOf(v, KindAbsence) {
		if r.Source == source {
			return r
		}
	}
	t.Fatalf("no absence row for %s in %+v", source, v.Rows)
	return Row{}
}

func mongoCase(t *testing.T, q evidence.SourceQuality) View {
	t.Helper()
	c := newCase(t)
	load(t, c, web(ev{kind: "request", start: 5000, dur: 1}))
	loadQuality(t, c, mongodlog.Name, "web-a", clock.RuleOffsetInField, q, ev{kind: mongodlog.KindSlowOp, start: 9000, dur: 2})
	return mustBuild(t, c, importer.Window{}, oneInterval)
}

// H1: a sampled or restart-marked mongod log never earns the threshold wording.
func TestMongodAbsenceUsesSourceQuality(t *testing.T) {
	gap := func(s string) string { return evidence.NotObservedGap(s) }
	cases := []struct {
		name string
		mut  func(*evidence.SourceQuality)
		want string
	}{
		{"sampled", func(q *evidence.SourceQuality) { q.Sampling = "a threshold change was found" }, gap("a threshold change was found")},
		{"restart marker", func(q *evidence.SourceQuality) { q.Sampling = "the server restarted inside the file" }, gap("the server restarted inside the file")},
		{"version unknown", func(q *evidence.SourceQuality) { q.SourceVersion = "" }, gap("mongod version unknown: no Build Info line in the file")},
		{"offset rule missing", func(q *evidence.SourceQuality) { q.TimezoneRule = "" }, gap("timestamp offset rule not established")},
		{"interval beyond coverage", func(q *evidence.SourceQuality) { q.CoverageFirst, q.CoverageLast = cover(0, 110) }, gap("the interval is not fully inside the log coverage")},
		{"clean 7.0", nil, evidence.NotObservedThreshold("mongod.log", "operations slower than 100 ms")},
		{"clean 8.0 compares workingMillis", func(q *evidence.SourceQuality) { q.SourceVersion = "8.0.1" },
			evidence.NotObservedThreshold("mongod.log", "operations whose working time (workingMillis) is over 100 ms")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := mongoCase(t, mongoQuality(tc.mut))
			r := absenceOf(t, v, mongodlog.Name)
			if r.Wording != tc.want || r.Label != "Symptom" {
				t.Fatalf("got %q (%s), want %q", r.Wording, r.Label, tc.want)
			}
			if strings.Contains(r.Wording, mongodlog.Name) {
				t.Fatalf("importer name in wording: %q", r.Wording)
			}
		})
	}
}

// H2a: an event that began before the edge margin but still overlaps the window is read.
func TestLongEventStartedBeforeWindowIsListed(t *testing.T) {
	c := newCase(t)
	load(t, c, web(ev{kind: "request", start: 5000, dur: 1}))
	// Starts 50 minutes before the interval, runs 55 minutes: overlaps it.
	load(t, c, sql(ev{kind: "lock-wait", start: -3000, dur: 3300}))
	w := importer.Window{From: at(90), To: at(140)}
	v := mustBuild(t, c, w, oneInterval)
	over := rowsOf(v, KindOverlap)
	if len(over) != 1 || over[0].Join != evidence.JoinUnique {
		t.Fatalf("the long event must be listed: %+v", v.Rows)
	}
	if abs := rowsOf(v, KindAbsence); len(abs) != 1 || abs[0].Source != "iis-w3c" {
		t.Fatalf("only the web source shows no event: %+v", abs)
	}
}

// H2a: an event that ended before the window is not pulled in by the lookback.
func TestLookbackIgnoresEventsThatEndedBeforeTheWindow(t *testing.T) {
	c := newCase(t)
	load(t, c, web(ev{kind: "request", start: 5000, dur: 1}))
	load(t, c, sql(ev{kind: "lock-wait", start: -3000, dur: 60}))
	v := mustBuild(t, c, importer.Window{From: at(90), To: at(140)}, oneInterval)
	if n := len(rowsOf(v, KindOverlap)); n != 0 || v.Unlisted != 0 {
		t.Fatalf("overlap rows %d, unlisted %d", n, v.Unlisted)
	}
}

// H2b: when the read was cut at the event cap no absence row claims anything.
func TestTruncatedReadDegradesAbsence(t *testing.T) {
	iv := join.Interval{ID: "audit-1", Start: at(100), End: at(128)}
	f, l := cover(-3600, 3600)
	file := casestore.ImportRecord{Importer: "sql-xe", Quality: evidence.SourceQuality{Threshold: "lock waits over 30 s", CoverageFirst: f, CoverageLast: l}}
	r := absenceRow("sql-xe", iv, []casestore.ImportRecord{file}, true)
	if want := evidence.NotObservedGap(gapTruncated); r.Wording != want {
		t.Fatalf("got %q, want %q", r.Wording, want)
	}
	r = absenceRow("sql-xe", iv, []casestore.ImportRecord{file}, false)
	if want := evidence.NotObservedThreshold("system_health", "lock waits over 30 s"); r.Wording != want {
		t.Fatalf("not truncated: got %q, want %q", r.Wording, want)
	}
}

// H2c: every file of a source must cover the interval, and the files must state one threshold.
func TestAbsenceNeedsEveryFile(t *testing.T) {
	th := "lock waits over 30 s and latch waits over 15 s"
	build := func(second func(*spec)) View {
		c := newCase(t)
		load(t, c, web(ev{kind: "request", start: 5000, dur: 1}))
		load(t, c, sql())
		b := sql()
		b.host = "sql-b"
		second(&b)
		load(t, c, b)
		return mustBuild(t, c, importer.Window{}, oneInterval)
	}
	t.Run("second file covers", func(t *testing.T) {
		v := build(func(*spec) {})
		if r := absenceOf(t, v, "sql-xe"); r.Wording != evidence.NotObservedThreshold("system_health", th) {
			t.Fatalf("%q", r.Wording)
		}
	})
	t.Run("second file does not cover", func(t *testing.T) {
		v := build(func(s *spec) { s.first, s.last = at(5000), at(6000) })
		if r := absenceOf(t, v, "sql-xe"); r.Wording != evidence.NotObservedGap(gapOutsideRange) {
			t.Fatalf("%q", r.Wording)
		}
	})
	t.Run("interval only partly covered", func(t *testing.T) {
		v := build(func(s *spec) { s.first, s.last = at(0), at(110) })
		if r := absenceOf(t, v, "sql-xe"); r.Wording != evidence.NotObservedGap(gapOutsideRange) {
			t.Fatalf("%q", r.Wording)
		}
	})
	t.Run("different thresholds", func(t *testing.T) {
		v := build(func(s *spec) { s.threshold = "lock waits over 60 s" })
		if r := absenceOf(t, v, "sql-xe"); r.Wording != evidence.NotObservedGap(gapThresholdsDiff) {
			t.Fatalf("%q", r.Wording)
		}
	})
}

// M3: an event shared by two intervals is neither "1 candidates" nor a Correlation.
func TestSharedEventWording(t *testing.T) {
	c := newCase(t)
	load(t, c, web(ev{kind: "request", start: 5000, dur: 1}))
	load(t, c, sql(ev{kind: "lock-wait", start: 105, dur: 100}))
	iv := []join.Interval{{ID: "a", Start: at(100), End: at(110)}, {ID: "b", Start: at(200), End: at(210)}}
	v := mustBuild(t, c, importer.Window{}, iv)
	over := rowsOf(v, KindOverlap)
	if len(over) != 2 {
		t.Fatalf("rows %+v", over)
	}
	for _, r := range over {
		if r.Label != "Symptom" || strings.Contains(r.Wording, "1 candidates") || r.Wording != evidence.OverlapShared(2) {
			t.Fatalf("shared row: %+v", r)
		}
		if err := evidence.LintCausal(r.Wording); err != nil {
			t.Fatal(err)
		}
	}
}

// L7: no sentence of the timeline names an importer.
func TestWordingNamesNoImporter(t *testing.T) {
	c := newCase(t)
	load(t, c, web(ev{kind: "request", start: 5000, dur: 1}))
	load(t, c, sql(ev{kind: "lock-wait", start: 105, dur: 35}))
	v := mustBuild(t, c, importer.Window{}, oneInterval)
	for _, r := range v.Rows {
		for _, name := range []string{"sql-xe", "iis-w3c", "mongod-json", "sql-errorlog"} {
			if strings.Contains(r.Wording, name) {
				t.Fatalf("%q names importer %s", r.Wording, name)
			}
		}
	}
}
