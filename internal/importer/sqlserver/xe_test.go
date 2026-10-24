package sqlserver

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/importertest"
	"api-audit-explorer/internal/join"
)

// xeFixtures: every XE attribute is typed (allowlisted event name, vocabulary
// token, fixed word, number), so there is no free-text cell; a cell that is
// none of these, a formula included, rejects its row (NoFreeText, M2).
func xeFixtures(t testing.TB) importertest.Fixtures {
	return importertest.Fixtures{
		Good:       readFixture(t, "xe_good.csv"),
		Bad:        readFixture(t, "xe_bad_cells.csv"),
		NoFreeText: true,
		Options:    importer.Options{Host: sqlA(t), FileName: "system_health.csv"},
	}
}

func TestXEConformance(t *testing.T) { importertest.Run(t, NewXE(), xeFixtures(t)) }

// TestXEBadFiles covers the bad files the harness does not take: each is
// refused whole, without naming the sentinel.
func TestXEBadFiles(t *testing.T) {
	for _, name := range []string{"xe_bad_unscrubbed.csv", "xe_bad_no_timestamp.csv"} {
		evs, _, err := collect(t, NewXE(), readFixture(t, name), testOpts(t))
		if !errors.Is(err, importer.ErrQuarantine) || len(evs) != 0 {
			t.Errorf("%s: err %v, %d events", name, err, len(evs))
		}
		if err != nil && strings.Contains(err.Error(), importertest.Sentinel) {
			t.Errorf("%s: error leaks the sentinel", name)
		}
	}
}

// TestXETypedCellsRejected: a formula or free text in a typed cell rejects the
// row with a fixed reason; it is neither stored nor silently dropped.
func TestXETypedCellsRejected(t *testing.T) {
	hdr := "timestamp_utc,name,wait_type,duration,component,state\n"
	rows := map[string]string{
		"formula wait_type": "2001-06-15 02:00:41,wait_info,\"=HYPERLINK(\"\"x\"\")\",32000,,\n",
		"text wait_type":    "2001-06-15 02:00:41,wait_info,LCK M X,32000,,\n",
		"formula component": "2001-06-15 02:00:41,sp_server_diagnostics_component_result,,,+SYSTEM,CLEAN\n",
		"formula state":     "2001-06-15 02:00:41,sp_server_diagnostics_component_result,,,SYSTEM,@clean\n",
		"formula event":     "2001-06-15 02:00:41,-wait_info,LCK_M_X,32000,,\n",
	}
	for name, row := range rows {
		o := testOpts(t)
		o.Limits = NewXE().DefaultLimits()
		o.Limits.MaxRejectFraction = 1
		evs, rep, err := collect(t, NewXE(), []byte(hdr+row), o)
		if err != nil || len(evs) != 0 || rep.Rejected != 1 || rep.Errors[0] != (importer.RowError{Line: 2, Reason: importer.ReasonUnsupportedFormat}) {
			t.Errorf("%s: err %v, events %d, report %+v", name, err, len(evs), rep.Errors)
		}
	}
}

func TestXEGoodEvents(t *testing.T) {
	evs, rep, err := collect(t, NewXE(), readFixture(t, "xe_good.csv"), testOpts(t))
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, e := range evs {
		kinds = append(kinds, e.Kind)
		if e.Source != XEName || e.Host != "sql-a" || e.TZRule == "" {
			t.Errorf("provenance: %+v", e)
		}
		if !strings.HasPrefix(e.EvidenceID, "ev:") {
			t.Errorf("evidence id %q", e.EvidenceID)
		}
		if e.Start.Location() != time.UTC {
			t.Errorf("event time not UTC: %v", e.Start)
		}
	}
	want := []string{KindDiagnostics, KindDiagnostics, KindWait, KindError, KindWait, KindWait, KindNonYielding}
	if fmt.Sprint(kinds) != fmt.Sprint(want) {
		t.Errorf("kinds = %v, want %v", kinds, want)
	}
	if rep.Rows != 8 || rep.Emitted != 7 || rep.Rejected != 0 {
		t.Errorf("report rows=%d emitted=%d rejected=%d", rep.Rows, rep.Emitted, rep.Rejected)
	}
	if rep.Quality.Threshold != XEThreshold || rep.Quality.CoverageFirst == nil || rep.Quality.CoverageLast == nil {
		t.Errorf("quality = %+v", rep.Quality)
	}
	if !rep.Quality.CoverageFirst.Equal(time.Date(2001, 6, 15, 1, 40, 0, 0, time.UTC)) ||
		!rep.Quality.CoverageLast.Equal(time.Date(2001, 6, 15, 2, 10, 0, 0, time.UTC)) {
		t.Errorf("coverage = %v .. %v", rep.Quality.CoverageFirst, rep.Quality.CoverageLast)
	}
	for _, g := range []string{gapXERetention, gapXEWaitStart, gapXEOther} {
		if !contains(rep.KnownGaps, g) {
			t.Errorf("known gaps %v lack %q", rep.KnownGaps, g)
		}
	}
	for _, g := range rep.KnownGaps {
		if err := evidence.LintCausal(g); err != nil {
			t.Errorf("gap sentence %q: %v", g, err)
		}
	}

	lock := evs[4]
	if lock.Attrs["wait_type"] != "LCK_M_X" || lock.Attrs["wait_class"] != "lock" || *lock.DurationMs != 35000 || lock.Unit != "ms" {
		t.Errorf("lock wait = %+v", lock)
	}
	if !lock.End.Equal(time.Date(2001, 6, 15, 2, 0, 35, 0, time.UTC)) || !lock.Start.Equal(time.Date(2001, 6, 15, 2, 0, 0, 0, time.UTC)) {
		t.Errorf("lock wait span = %v .. %v (wait_info fires at the end of the wait)", lock.Start, lock.End)
	}
	if latch := evs[2]; latch.Attrs["wait_class"] != "latch" || !latch.Start.Equal(time.Date(2001, 6, 15, 1, 49, 56, 250e6, time.UTC)) {
		t.Errorf("latch wait = %+v", latch)
	}
	if e := evs[3]; e.Attrs["error_number"] != "823" || e.Attrs["sql_severity"] != "24" || e.Severity != "critical" {
		t.Errorf("error_reported = %+v", e)
	}
	if d := evs[1]; d.Attrs["component"] != "io_subsystem" || d.Attrs["state"] != "warning" || d.Severity != "warning" {
		t.Errorf("diagnostics = %+v", d)
	}
}

// ---- R8-B2: absence and overlap wording -------------------------------------

func stall(start time.Time, secs int) join.Interval {
	return join.Interval{ID: "audit-alpha-1", Start: start, End: start.Add(time.Duration(secs) * time.Second)}
}

func checkNeverClaims(t *testing.T, st Statement) {
	t.Helper()
	if err := evidence.LintCausal(st.Text); err != nil {
		t.Errorf("%q: %v", st.Text, err)
	}
	low := strings.ToLower(st.Text)
	for _, banned := range []string{"not waiting", "sql was", "the api waited", "waited for sql", "strong", "proven"} {
		if strings.Contains(low, banned) {
			t.Errorf("statement %q contains %q", st.Text, banned)
		}
	}
	if !evidence.ValidImporterLabel(st.Label) {
		t.Errorf("label %q", st.Label)
	}
}

func TestXEStatementBelowThreshold(t *testing.T) {
	want := evidence.NotObservedThreshold("system_health", "lock waits over 30 s and latch waits over 15 s")
	at := time.Date(2001, 6, 15, 2, 0, 0, 0, time.UTC)
	cases := map[string]string{
		"header-only export": "timestamp_utc,name,wait_type,duration\n",
		// The export covers the stall (diagnostics rows on both sides) but has no wait
		// over the threshold: still the threshold wording, never "SQL was not waiting".
		"covered, no waits": "timestamp_utc,name,component,state\n" +
			"2001-06-15 01:55:00,sp_server_diagnostics_component_result,SYSTEM,CLEAN\n" +
			"2001-06-15 02:05:00,sp_server_diagnostics_component_result,SYSTEM,CLEAN\n",
	}
	for name, csv := range cases {
		t.Run(name, func(t *testing.T) {
			evs, rep, err := collect(t, NewXE(), []byte(csv), testOpts(t))
			if err != nil {
				t.Fatal(err)
			}
			st := XEStatement(stall(at, 28), evs, rep.Quality, goldenTol)
			if st.Text != want || st.Label != audit.EvidenceSymptom || st.Class != evidence.JoinNone {
				t.Errorf("28 s stall: %+v\nwant text %q", st, want)
			}
			checkNeverClaims(t, st)
		})
	}
}

func TestXEStatementUniqueLockWait(t *testing.T) {
	evs, rep, err := collect(t, NewXE(), readFixture(t, "xe_good.csv"), testOpts(t))
	if err != nil {
		t.Fatal(err)
	}
	iv := join.Interval{ID: "audit-alpha-1", Start: time.Date(2001, 6, 15, 1, 59, 58, 0, time.UTC), End: time.Date(2001, 6, 15, 2, 0, 40, 0, time.UTC)}
	st := XEStatement(iv, evs, rep.Quality, goldenTol)
	if st.Text != evidence.OverlapUnique("system_health", "lock wait", 35) || st.Label != audit.EvidenceCorrelation || len(st.Candidates) != 1 {
		t.Errorf("35 s lock wait: %+v", st)
	}
	checkNeverClaims(t, st)

	latch := XEStatement(stall(time.Date(2001, 6, 15, 1, 50, 0, 0, time.UTC), 20), evs, rep.Quality, goldenTol)
	if latch.Text != evidence.OverlapUnique("system_health", "latch wait", 16.25) {
		t.Errorf("latch wait: %+v", latch)
	}
	checkNeverClaims(t, latch)
}

func TestXEStatementAmbiguous(t *testing.T) {
	csv := "timestamp_utc,name,wait_type,duration\n" +
		"2001-06-15 02:00:35,wait_info,LCK_M_X,35000\n" +
		"2001-06-15 02:00:30,wait_info,LCK_M_S,31000\n"
	evs, rep, err := collect(t, NewXE(), []byte(csv), testOpts(t))
	if err != nil {
		t.Fatal(err)
	}
	st := XEStatement(stall(time.Date(2001, 6, 15, 2, 0, 0, 0, time.UTC), 40), evs, rep.Quality, goldenTol)
	if st.Text != evidence.OverlapAmbiguous(2) || st.Label != audit.EvidenceSymptom || st.Class != evidence.JoinAmbiguous {
		t.Errorf("two overlapping waits: %+v", st)
	}
	checkNeverClaims(t, st)
}

func TestXEStatementCoverageGap(t *testing.T) {
	evs, rep, err := collect(t, NewXE(), readFixture(t, "xe_good.csv"), testOpts(t))
	if err != nil {
		t.Fatal(err)
	}
	st := XEStatement(stall(time.Date(2001, 6, 15, 5, 0, 0, 0, time.UTC), 28), evs, rep.Quality, goldenTol)
	if st.Text != evidence.NotObservedGap("the system_health export does not cover this interval") || st.Label != audit.EvidenceSymptom {
		t.Errorf("interval outside coverage: %+v", st)
	}
	checkNeverClaims(t, st)
}

// ---- scrub list, columns, hosts ---------------------------------------------

func TestXEScrubListQuarantines(t *testing.T) {
	headers := []string{
		"sql_text", "statement", "input_buffer", "xml_report", "client_hostname", "username", "nt_username", "database_name",
		"SQL Text", "SQL_TEXT", "sqlText", "Statement", "InputBuffer", "XML Report", "Client HostName", "UserName", "NT UserName", "Database Name",
	}
	for _, h := range headers {
		t.Run(h, func(t *testing.T) {
			csv := fmt.Sprintf("timestamp_utc,name,%q\n2001-06-15 02:00:35,wait_info,%s\n", h, importertest.Sentinel)
			var emitted int
			_, err := NewXE().Parse(context.Background(), strings.NewReader(csv), testOpts(t), func(evidence.Event) error { emitted++; return nil })
			var q *importer.QuarantineError
			if !errors.As(err, &q) || q.Reason != importer.ReasonUnscrubbedColumn || q.Column != h {
				t.Fatalf("header %q: got %v, want unscrubbed-column naming the header", h, err)
			}
			if emitted != 0 || strings.Contains(err.Error(), importertest.Sentinel) {
				t.Errorf("header %q: emitted %d, err %v", h, emitted, err)
			}
		})
	}
}

func TestXEMissingTimestampColumn(t *testing.T) {
	_, _, err := collect(t, NewXE(), readFixture(t, "xe_bad_no_timestamp.csv"), testOpts(t))
	var q *importer.QuarantineError
	if !errors.As(err, &q) || q.Reason != importer.ReasonUnsupportedFormat || q.Column != "timestamp_utc" {
		t.Fatalf("got %v, want unsupported-format naming timestamp_utc", err)
	}
	_, _, err = collect(t, NewXE(), []byte("timestamp_utc,wait_type\n2001-06-15 02:00:35,LCK_M_X\n"), testOpts(t))
	if !errors.As(err, &q) || q.Column != "name" {
		t.Fatalf("no event column: got %v", err)
	}
}

func TestXESSMSHeaderAccepted(t *testing.T) {
	csv := "\"timestamp (UTC)\",\"name\",\"wait_type\",\"duration\"\n2001-06-15 02:00:35.0000000,wait_info,LCK_M_X,35000\n"
	evs, _, err := collect(t, NewXE(), []byte(csv), testOpts(t))
	if err != nil || len(evs) != 1 || evs[0].Attrs["wait_class"] != "lock" {
		t.Fatalf("SSMS header: events %+v, err %v", evs, err)
	}
}

func TestXEOffsetInUTCFieldRejected(t *testing.T) {
	var b strings.Builder
	b.WriteString("timestamp_utc,name,component,state\n")
	for i := 0; i < 25; i++ {
		fmt.Fprintf(&b, "2001-06-15 01:%02d:00Z,sp_server_diagnostics_component_result,SYSTEM,CLEAN\n", i)
	}
	b.WriteString("2001-06-15T14:00:35+12:00,sp_server_diagnostics_component_result,SYSTEM,CLEAN\n")
	b.WriteString("2001-06-15T02:00:36+00:00,sp_server_diagnostics_component_result,SYSTEM,CLEAN\n")
	evs, rep, err := collect(t, NewXE(), []byte(b.String()), testOpts(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 26 || rep.Rejected != 1 || rep.Errors[0].Reason != importer.ReasonBadTime || rep.Errors[0].Line != 27 {
		t.Errorf("events %d, report %+v", len(evs), rep.Errors)
	}
}

func TestXEHosts(t *testing.T) {
	ctx := context.Background()
	noHost := testOpts(t)
	noHost.Host = nil
	if _, _, err := collect(t, NewXE(), readFixture(t, "xe_good.csv"), noHost); !errors.Is(err, importer.ErrUnknownHost) {
		t.Errorf("no host picked, no host column: got %v", err)
	}

	withCol := "timestamp_utc,name,wait_type,duration,server_instance_name\n" +
		"2001-06-15 02:00:35,wait_info,LCK_M_X,35000,SQL-A\n" +
		"2001-06-15 02:00:36,wait_info,LCK_M_S,31000,SQL-A\n"
	hosts := func(raw string) (importer.HostHint, bool) {
		switch raw {
		case "SQL-A":
			return importer.HostHint{Label: "sql-a"}, true
		case "SQL-B":
			return importer.HostHint{Label: "sql-b"}, true
		}
		return importer.HostHint{}, false
	}
	o := noHost
	o.Hosts = hosts
	evs, rep, err := collect(t, NewXE(), []byte(withCol), o)
	if err != nil || len(evs) != 2 || evs[0].Host != "sql-a" || fmt.Sprint(rep.ObservedHosts) != "[SQL-A]" {
		t.Errorf("mapped host column: events %+v, observed %v, err %v", evs, rep.ObservedHosts, err)
	}

	for name, csv := range map[string]string{
		"unknown name": strings.ReplaceAll(withCol, "SQL-A", "SQL-Z"),
		"other host":   strings.ReplaceAll(withCol, "SQL-A", "SQL-B"),
	} {
		o := testOpts(t) // picked sql-a
		o.Hosts = hosts
		var emitted int
		_, err := NewXE().Parse(ctx, strings.NewReader(csv), o, func(evidence.Event) error { emitted++; return nil })
		if !errors.Is(err, importer.ErrUnknownHost) || emitted != 0 {
			t.Errorf("%s: err %v, emitted %d", name, err, emitted)
		}
	}
	o = testOpts(t)
	if _, _, err := collect(t, NewXE(), []byte(withCol), o); !errors.Is(err, importer.ErrUnknownHost) {
		t.Errorf("host column without a Hosts mapping: got %v", err)
	}
}

func TestXEDetect(t *testing.T) {
	x := NewXE()
	cases := []struct {
		name, file string
		head       []byte
		min, max   int
	}{
		{"system_health", "system_health.csv", readFixture(t, "xe_good.csv"), 100, 100},
		{"ssms header", "export.txt", []byte("\"timestamp (UTC)\",\"name\"\n"), 70, 70},
		{"app insights", "requests.csv", []byte("timestamp [UTC],itemType,operation_Id,name,duration\n"), 0, 0},
		{"errorlog", "ERRORLOG", readFixture(t, "errorlog_good.log"), 0, 0},
		{"empty", "x.csv", nil, 0, 0},
	}
	for _, c := range cases {
		if got := x.Detect(c.head, c.file); got < c.min || got > c.max {
			t.Errorf("%s: Detect = %d, want %d..%d", c.name, got, c.min, c.max)
		}
	}
}

func FuzzParseXE(f *testing.F) {
	fx := xeFixtures(f)
	f.Add(readFixture(f, "xe_bad_unscrubbed.csv"))
	f.Add(readFixture(f, "xe_bad_no_timestamp.csv"))
	importertest.FuzzParse(f, NewXE(), fx)
}
