package timeline

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/join"
)

var t0 = time.Date(2001, 2, 3, 4, 0, 0, 0, time.UTC)

func at(sec int) time.Time { return t0.Add(time.Duration(sec) * time.Second) }

// spec describes one synthetic file.
type spec struct {
	importer, host, tz string
	threshold          string
	first, last        time.Time
	events             []ev
}

type ev struct {
	kind  string
	start int // seconds after t0
	dur   float64
	point bool // no duration, End == Start
}

var ack = "I have removed anything I am not allowed to share."

func newCase(t *testing.T) casestore.Case {
	t.Helper()
	ctx := context.Background()
	st := casestore.NewMemory()
	info, err := st.Create(ctx, "case one")
	if err != nil {
		t.Fatal(err)
	}
	c, err := st.Open(ctx, info.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func load(t *testing.T, c casestore.Case, s spec) {
	t.Helper()
	cid := string(c.Info().ID)
	meta := casestore.ImportMeta{
		Importer: s.importer, FileName: s.importer + ".csv", HostLabel: s.host, TZRule: s.tz,
		AckGating: true, AckText: ack,
	}
	run := func(r io.Reader, emit func(evidence.Event) error) (importer.Report, error) {
		rep := importer.Report{Importer: s.importer}
		if _, err := io.Copy(io.Discard, r); err != nil {
			return rep, err
		}
		for i, e := range s.events {
			rep.Rows++
			start := at(e.start)
			x := evidence.Event{
				EvidenceID: evidence.EventID(cid, s.importer, "n"+s.host, int64(i+2), 0),
				Source:     s.importer, Kind: e.kind, Host: s.host, Start: start, End: start,
				Line: int64(i + 2), TZRule: clock.RuleUTCField,
			}
			if !e.point {
				d := e.dur * 1000
				x.DurationMs = &d
				x.End = start.Add(time.Duration(e.dur * float64(time.Second)))
			}
			if err := emit(x); err != nil {
				return rep, err
			}
			rep.Emitted++
		}
		rep.Quality.Threshold = s.threshold
		if !s.first.IsZero() {
			f, l := s.first, s.last
			rep.Quality.CoverageFirst, rep.Quality.CoverageLast = &f, &l
		}
		return rep, nil
	}
	body := bytes.NewReader([]byte(s.importer + "|" + s.host + "|" + s.tz + fmt.Sprint(len(s.events))))
	if _, err := c.Import(context.Background(), meta, body, run); err != nil {
		t.Fatalf("import %s: %v", s.importer, err)
	}
}

func web(events ...ev) spec {
	return spec{importer: "iis-w3c", host: "web-a", tz: clock.RuleUTCField, first: at(-3600), last: at(3600), events: events}
}

func sql(events ...ev) spec {
	return spec{importer: "sql-xe", host: "sql-a", tz: clock.RuleUTCField, threshold: "lock waits over 30 s and latch waits over 15 s",
		first: at(-3600), last: at(3600), events: events}
}

var oneInterval = []join.Interval{{ID: "audit-1", Start: at(100), End: at(128)}}

func rowsOf(v View, kind string) []Row {
	var out []Row
	for _, r := range v.Rows {
		if r.Kind == kind {
			out = append(out, r)
		}
	}
	return out
}

func mustBuild(t *testing.T, c casestore.Case, w importer.Window, iv []join.Interval) View {
	t.Helper()
	v, err := Build(context.Background(), c, w, iv)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestNoSourcesDisabled(t *testing.T) {
	v := mustBuild(t, newCase(t), importer.Window{}, oneInterval)
	if v.Enabled || v.ReasonCode != ReasonNoSources || v.Reason == "" || len(v.Rows) != 0 {
		t.Fatalf("%+v", v)
	}
}

func TestSingleSourceDisabled(t *testing.T) {
	c := newCase(t)
	load(t, c, sql(ev{kind: "lock-wait", start: 105, dur: 35}))
	// A second file from the same importer is still one source.
	second := sql(ev{kind: "lock-wait", start: 900, dur: 40})
	second.host = "sql-b"
	load(t, c, second)
	v := mustBuild(t, c, importer.Window{}, oneInterval)
	if v.Enabled || v.ReasonCode != ReasonSingleSource {
		t.Fatalf("enabled=%v code=%q", v.Enabled, v.ReasonCode)
	}
	if v.Reason != "The timeline needs at least 2 sources. Only 1 has been imported." || len(v.Rows) != 0 {
		t.Fatalf("reason %q rows %d", v.Reason, len(v.Rows))
	}
	if len(v.Sources) != 1 || v.Sources[0].Files != 2 {
		t.Fatalf("sources %+v", v.Sources)
	}
}

func TestHostLocalWithoutTZDisabled(t *testing.T) {
	for name, mutate := range map[string]func(*spec){
		"host-local with no host": func(s *spec) { s.tz, s.host = clock.RuleHostLocal, "" },
		"no rule recorded":        func(s *spec) { s.tz = "" },
		"unknown rule":            func(s *spec) { s.tz = "guess" },
	} {
		t.Run(name, func(t *testing.T) {
			c := newCase(t)
			load(t, c, web(ev{kind: "request", start: 110, dur: 3}))
			bad := sql(ev{kind: "lock-wait", start: 105, dur: 35})
			mutate(&bad)
			load(t, c, bad)
			v := mustBuild(t, c, importer.Window{}, oneInterval)
			if v.Enabled || v.ReasonCode != ReasonTZRule || len(v.Rows) != 0 {
				t.Fatalf("%+v", v)
			}
			if !strings.Contains(v.Reason, "system_health") || strings.Contains(v.Reason, "IIS log") || strings.Contains(v.Reason, "sql-xe") {
				t.Fatalf("reason should name only the unchecked source: %q", v.Reason)
			}
		})
	}
	// host-local with a host is accepted (the host map holds its zone).
	c := newCase(t)
	load(t, c, web(ev{kind: "request", start: 110, dur: 3}))
	ok := sql(ev{kind: "lock-wait", start: 105, dur: 35})
	ok.tz = clock.RuleHostLocal
	load(t, c, ok)
	if v := mustBuild(t, c, importer.Window{}, oneInterval); !v.Enabled {
		t.Fatalf("host-local with a host: %q", v.Reason)
	}
}

func TestUniqueOverlapRows(t *testing.T) {
	c := newCase(t)
	load(t, c, web(ev{kind: "request", start: 5000, dur: 1}))
	load(t, c, sql(ev{kind: "lock-wait", start: 105, dur: 35}))
	v := mustBuild(t, c, importer.Window{From: at(0), To: at(1000)}, oneInterval)
	if !v.Enabled || v.Reason != "" {
		t.Fatalf("%+v", v)
	}
	audit := rowsOf(v, KindAudit)
	if len(audit) != 1 || audit[0].Wording != evidence.AuditCreate(28) || audit[0].Label != "Symptom" {
		t.Fatalf("audit rows %+v", audit)
	}
	over := rowsOf(v, KindOverlap)
	if len(over) != 1 {
		t.Fatalf("overlap rows %+v", over)
	}
	want := evidence.OverlapUnique("system_health", "lock wait", 35)
	if over[0].Wording != want || over[0].Label != "Correlation" || over[0].Join != evidence.JoinUnique || over[0].Candidates != 0 {
		t.Fatalf("overlap row %+v want %q", over[0], want)
	}
	// iis-w3c has nothing near the interval; its threshold is empty, so the gap wording is used.
	abs := rowsOf(v, KindAbsence)
	if len(abs) != 1 || abs[0].Source != "iis-w3c" || abs[0].Wording != evidence.NotObservedGap(gapNoThreshold) {
		t.Fatalf("absence rows %+v", abs)
	}
	// The 5000 s request lies outside the window and is not read.
	if v.Unlisted != 0 {
		t.Fatalf("unlisted %d", v.Unlisted)
	}
}

func TestAmbiguousJoinListsEveryCandidate(t *testing.T) {
	for _, k := range []int{2, 3, 5} {
		t.Run(fmt.Sprintf("k=%d", k), func(t *testing.T) {
			c := newCase(t)
			load(t, c, web(ev{kind: "request", start: 5000, dur: 1}))
			var evs []ev
			for i := 0; i < k; i++ {
				evs = append(evs, ev{kind: "lock-wait", start: 100 + 3*i, dur: 20})
			}
			load(t, c, sql(evs...))
			v := mustBuild(t, c, importer.Window{}, oneInterval)
			var amb []Row
			ids := map[string]bool{}
			for _, r := range v.Rows {
				if r.Join == evidence.JoinAmbiguous {
					amb = append(amb, r)
					ids[r.EvidenceID] = true
				}
			}
			if len(amb) != k || len(ids) != k {
				t.Fatalf("ambiguous rows %d (distinct %d), want exactly %d", len(amb), len(ids), k)
			}
			for _, r := range amb {
				if r.Candidates != k || r.Wording != evidence.OverlapAmbiguous(k) || r.Label != "Symptom" || r.Source != "sql-xe" {
					t.Fatalf("row %+v", r)
				}
			}
			if n := len(rowsOf(v, KindOverlap)); n != k {
				t.Fatalf("overlap rows %d", n)
			}
		})
	}
}

func TestAbsenceWordings(t *testing.T) {
	// 28 s stall, system_health records only long waits: threshold wording (rule R8-B2 shape).
	c := newCase(t)
	load(t, c, web(ev{kind: "request", start: 105, dur: 2}))
	load(t, c, sql())
	v := mustBuild(t, c, importer.Window{}, oneInterval)
	var sqlAbs *Row
	for _, r := range rowsOf(v, KindAbsence) {
		if r.Source == "sql-xe" {
			sqlAbs = &r
		}
	}
	want := evidence.NotObservedThreshold("system_health", "lock waits over 30 s and latch waits over 15 s")
	if sqlAbs == nil || sqlAbs.Wording != want || sqlAbs.Label != "Symptom" {
		t.Fatalf("absence %+v want %q", sqlAbs, want)
	}
	if n := len(rowsOf(v, KindOverlap)); n != 1 {
		t.Fatalf("iis request should overlap: %d", n)
	}

	// An interval outside the file's time range gets the gap wording, never the threshold wording.
	far := []join.Interval{{ID: "audit-far", Start: at(90000), End: at(90030)}}
	v = mustBuild(t, c, importer.Window{}, far)
	for _, r := range rowsOf(v, KindAbsence) {
		if r.Source == "sql-xe" && r.Wording != evidence.NotObservedGap(gapOutsideRange) {
			t.Fatalf("outside range: %q", r.Wording)
		}
	}

	// Unknown time range: gap wording.
	c2 := newCase(t)
	load(t, c2, web())
	noRange := sql()
	noRange.first, noRange.last = time.Time{}, time.Time{}
	load(t, c2, noRange)
	v = mustBuild(t, c2, importer.Window{}, oneInterval)
	for _, r := range rowsOf(v, KindAbsence) {
		if r.Source == "sql-xe" && r.Wording != evidence.NotObservedGap(gapNoRange) {
			t.Fatalf("no range: %q", r.Wording)
		}
	}
}

func TestPointEventsAreNotWordedAsOverlaps(t *testing.T) {
	c := newCase(t)
	load(t, c, web(ev{kind: "request", start: 5000, dur: 1}))
	load(t, c, sql(ev{kind: "error", start: 110, point: true}))
	v := mustBuild(t, c, importer.Window{}, oneInterval)
	if n := len(rowsOf(v, KindOverlap)); n != 0 {
		t.Fatalf("a point event has no length to state; got %d overlap rows", n)
	}
	if v.Unlisted != 2 {
		t.Fatalf("unlisted %d, want the point event and the distant request", v.Unlisted)
	}
}

func TestRowsAreClockAlignedAndPassLint(t *testing.T) {
	c := newCase(t)
	load(t, c, web(ev{kind: "request", start: 20, dur: 4}, ev{kind: "request", start: 300, dur: 4}))
	load(t, c, sql(ev{kind: "lock-wait", start: 8, dur: 33}, ev{kind: "latch-wait", start: 290, dur: 20}))
	iv := []join.Interval{
		{ID: "audit-b", Start: at(295), End: at(325)},
		{ID: "audit-a", Start: at(10), End: at(40)},
	}
	v := mustBuild(t, c, importer.Window{}, iv)
	if !v.Enabled || len(v.Rows) == 0 {
		t.Fatalf("%+v", v)
	}
	for i, r := range v.Rows {
		if i > 0 && r.Time.Before(v.Rows[i-1].Time) {
			t.Fatalf("row %d out of order", i)
		}
		if r.Time.Location() != time.UTC || r.End.Location() != time.UTC {
			t.Fatalf("row %d not UTC", i)
		}
		if err := evidence.LintCausal(r.Wording); err != nil {
			t.Fatalf("row %d: %v", i, err)
		}
		if !evidence.ValidImporterLabel(r.Label) || r.Wording == "" {
			t.Fatalf("row %d label %q wording %q", i, r.Label, r.Wording)
		}
	}
	// Same input, same output.
	again := mustBuild(t, c, importer.Window{}, iv)
	if !reflect.DeepEqual(again.Rows, v.Rows) {
		t.Fatal("not deterministic")
	}
}

func TestEventKindWithCausalWordIsRefused(t *testing.T) {
	// A wording that would name a cause is an error, never shown.
	c := newCase(t)
	load(t, c, web(ev{kind: "request", start: 5000, dur: 1}))
	load(t, c, sql(ev{kind: "caused-by-x", start: 105, dur: 35}))
	if _, err := Build(context.Background(), c, importer.Window{}, oneInterval); err == nil {
		t.Fatal("expected a lint error")
	}
}

func TestEventSharedByTwoIntervalsIsAmbiguousNotCounted(t *testing.T) {
	c := newCase(t)
	load(t, c, web(ev{kind: "request", start: 5000, dur: 1}))
	load(t, c, sql(ev{kind: "lock-wait", start: 105, dur: 100}))
	iv := []join.Interval{{ID: "a", Start: at(100), End: at(110)}, {ID: "b", Start: at(200), End: at(210)}}
	v := mustBuild(t, c, importer.Window{}, iv)
	n := 0
	for _, r := range rowsOf(v, KindOverlap) {
		if r.Join != evidence.JoinAmbiguous || r.Candidates != 0 || r.Label != "Symptom" || r.Wording != evidence.OverlapShared(2) {
			t.Fatalf("%+v", r)
		}
		n++
	}
	if n != 2 || v.Unlisted != 1 {
		t.Fatalf("rows %d unlisted %d", n, v.Unlisted)
	}
}
