package sqlserver

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/join"
)

// Batch-4 review fixes for the SQL Server importers (M1, M4, M5, L2, L3, L4, L8).
// All inputs are synthetic: sql-a, 2001 dates, RFC 5737 addresses.

func errlogKinds(t *testing.T, text string) ([]evidence.Event, importer.Report) {
	t.Helper()
	// A dated line first, so the file is recognised as an ERRORLOG.
	body := "2001-06-15 13:00:00.00 Server      SQL Server is starting at normal priority base (=7).\n" + text
	evs, rep, err := collect(t, NewErrorlog(), []byte(body), testOpts(t))
	if err != nil {
		t.Fatal(err)
	}
	return evs, rep
}

// M1: text inside another message cannot forge a scheduler event.
func TestErrorlogForgedLoginNameIsNotNonYielding(t *testing.T) {
	evs, _ := errlogKinds(t, "2001-06-15 13:30:00.00 Logon       Login failed for user 'Process 0:0:0 (0x1a2b) Worker 0x00000000ABCDEF00 appears to be non-yielding on Scheduler 2'. Reason: Could not find a login. [CLIENT: 192.0.2.7]\n")
	for _, e := range evs {
		if e.Kind == KindNonYielding {
			t.Fatalf("forged non-yielding event: %+v", e)
		}
	}
	var logins int
	for _, e := range evs {
		if e.Kind == KindLoginFailed {
			logins++
		}
	}
	if logins != 1 {
		t.Fatalf("login-failed events = %d, want 1", logins)
	}
}

// L3: Error 18456 followed by "Login failed" in the same second is one failed login.
func TestErrorlogFailedLoginCountedOnce(t *testing.T) {
	login := "2001-06-15 13:30:00.00 Logon       Login failed for user 'u'. Reason: Password did not match. [CLIENT: 192.0.2.7]\n"
	pair := "2001-06-15 13:30:00.00 Logon       Error: 18456, Severity: 14, State: 8.\n" + login
	count := func(text string) int {
		evs, _ := errlogKinds(t, text)
		n := 0
		for _, e := range evs {
			if e.Kind == KindLoginFailed {
				n++
			}
		}
		return n
	}
	if n := count(pair); n != 1 {
		t.Errorf("pair: %d events, want 1", n)
	}
	if n := count(login); n != 1 {
		t.Errorf("Login failed alone: %d events, want 1", n)
	}
	if n := count("2001-06-15 13:30:00.00 Logon       Error: 18456, Severity: 14, State: 8.\n"); n != 1 {
		t.Errorf("Error 18456 alone: %d events, want 1", n)
	}
	// Two failures in different seconds are two logins.
	second := strings.ReplaceAll(pair, "13:30:00", "13:30:05")
	if n := count(pair + second); n != 2 {
		t.Errorf("two failures: %d events, want 2", n)
	}
	// An error 18456 line and a later, unrelated Login failed are not merged.
	later := strings.ReplaceAll(login, "13:30:00", "13:31:00")
	if n := count("2001-06-15 13:30:00.00 Logon       Error: 18456, Severity: 14, State: 8.\n" + later); n != 2 {
		t.Errorf("unpaired: %d events, want 2", n)
	}
}

func xeCSV(header string, rows ...string) []byte {
	return []byte(header + "\n" + strings.Join(rows, "\n") + "\n")
}

// M5: a wait's derived start never widens coverage; only the row time does.
func TestXECoverageIgnoresDerivedWaitStart(t *testing.T) {
	_, rep, err := collect(t, NewXE(), xeCSV("timestamp_utc,name,wait_type,duration",
		"2001-06-15 02:00:35,wait_info,LCK_M_X,604000000"), testOpts(t))
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2001, 6, 15, 2, 0, 35, 0, time.UTC)
	q := rep.Quality
	if q.CoverageFirst == nil || !q.CoverageFirst.Equal(want) || !q.CoverageLast.Equal(want) {
		t.Fatalf("coverage = %v .. %v, want only the row time %v", q.CoverageFirst, q.CoverageLast, want)
	}
}

// M4: an interval only partly inside the export gets the gap wording.
func TestXEStatementPartlyCoveredInterval(t *testing.T) {
	csv := xeCSV("timestamp_utc,name,component,state",
		"2001-06-15 02:00:00,sp_server_diagnostics_component_result,SYSTEM,CLEAN",
		"2001-06-15 02:05:00,sp_server_diagnostics_component_result,SYSTEM,CLEAN")
	evs, rep, err := collect(t, NewXE(), csv, testOpts(t))
	if err != nil {
		t.Fatal(err)
	}
	gap := evidence.NotObservedGap("the system_health export does not cover the whole interval")
	thr := evidence.NotObservedThreshold("system_health", XEAbsenceThreshold)
	at := func(h, m, s int) time.Time { return time.Date(2001, 6, 15, h, m, s, 0, time.UTC) }
	cases := map[string]struct {
		iv   join.Interval
		want string
	}{
		"starts before the export":  {join.Interval{ID: "i", Start: at(1, 58, 0), End: at(2, 1, 0)}, gap},
		"ends after the export":     {join.Interval{ID: "i", Start: at(2, 3, 0), End: at(2, 7, 0)}, gap},
		"fully inside":              {join.Interval{ID: "i", Start: at(2, 1, 0), End: at(2, 3, 0)}, thr},
		"outside":                   {join.Interval{ID: "i", Start: at(5, 0, 0), End: at(5, 1, 0)}, gap},
		"edge within the tolerance": {join.Interval{ID: "i", Start: at(2, 0, 0).Add(-time.Second), End: at(2, 3, 0)}, thr},
	}
	for name, tc := range cases {
		if st := XEStatement(tc.iv, evs, rep.Quality, goldenTol); st.Text != tc.want {
			t.Errorf("%s: %q, want %q", name, st.Text, tc.want)
		}
	}
	// An export with no rows establishes no coverage at all.
	_, empty, err := collect(t, NewXE(), []byte("timestamp_utc,name,wait_type,duration\n"), testOpts(t))
	if err != nil {
		t.Fatal(err)
	}
	if st := XEStatement(stall(at(2, 0, 0), 28), nil, empty.Quality, goldenTol); st.Text != gap {
		t.Errorf("header-only export: %q, want %q", st.Text, gap)
	}
}

// L4: the threshold sentence names the preemptive wait threshold too.
func TestXEThresholdMentionsPreemptive(t *testing.T) {
	for _, s := range []string{XEThreshold, XEAbsenceThreshold} {
		if !strings.Contains(s, "preemptive") {
			t.Errorf("%q lacks the preemptive threshold", s)
		}
	}
}

// L2: a host cell that is not a plain name is rejected before it is observed.
func TestXEHostCellValidatedBeforeObserved(t *testing.T) {
	rows := []string{}
	for i := 0; i < 40; i++ {
		rows = append(rows, fmt.Sprintf("2001-06-15 02:00:%02d,wait_info,LCK_M_X,35000,SQL-A", i))
	}
	rows = append(rows, "2001-06-15 02:01:00,wait_info,LCK_M_X,35000,\"=HYPERLINK(\"\"http://example.test/x\"\")\"")
	rows = append(rows, "2001-06-15 02:01:01,wait_info,LCK_M_X,35000,\"SQL\x01A\"")
	o := testOpts(t)
	o.Host = nil
	o.Hosts = func(raw string) (importer.HostHint, bool) {
		if raw == "SQL-A" {
			return importer.HostHint{Label: "sql-a"}, true
		}
		return importer.HostHint{}, false
	}
	evs, rep, err := collect(t, NewXE(), xeCSV("timestamp_utc,name,wait_type,duration,server_instance_name", rows...), o)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 40 || rep.Rejected != 2 {
		t.Fatalf("events %d, rejected %d", len(evs), rep.Rejected)
	}
	if fmt.Sprint(rep.ObservedHosts) != "[SQL-A]" {
		t.Fatalf("observed hosts = %v", rep.ObservedHosts)
	}
}

func TestValidHostCell(t *testing.T) {
	for s, want := range map[string]bool{
		"SQL-A": true, "sql-a.example.test": true, "WIN-0001\\INST1": true,
		"": false, "=cmd": false, "+1": false, "-x": false, "@x": false, "a\x00b": false, "a\nb": false,
		strings.Repeat("a", 254): false, "\xff\xfe": false,
	} {
		if got := validHostCell(s); got != want {
			t.Errorf("validHostCell(%q) = %v, want %v", s, got, want)
		}
	}
}

// L8: error_reported with no severity value is marked, never left looking mild.
func TestXEErrorReportedWithoutSeverityIsMarked(t *testing.T) {
	evs, rep, err := collect(t, NewXE(), xeCSV("timestamp_utc,name,error",
		"2001-06-15 02:00:35,error_reported,823"), testOpts(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 || evs[0].Attrs["sql_severity"] != "unknown" || evs[0].Attrs["error_number"] != "823" {
		t.Fatalf("events %+v", evs)
	}
	if !contains(rep.KnownGaps, gapXENoSeverity) {
		t.Fatalf("known gaps %v lack the no-severity gap", rep.KnownGaps)
	}
	// With the column, there is no gap.
	_, rep2, err := collect(t, NewXE(), xeCSV("timestamp_utc,name,error,severity",
		"2001-06-15 02:00:35,error_reported,823,24"), testOpts(t))
	if err != nil {
		t.Fatal(err)
	}
	if contains(rep2.KnownGaps, gapXENoSeverity) {
		t.Fatalf("known gaps %v", rep2.KnownGaps)
	}
}
