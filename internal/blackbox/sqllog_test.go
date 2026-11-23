package blackbox

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
)

var utc = time.UTC

// utf16le encodes text the way SQL Server on Windows writes the ERRORLOG: a BOM
// then UTF-16 little endian.
func utf16le(s string, bom bool) []byte {
	var out []byte
	if bom {
		out = append(out, 0xFF, 0xFE)
	}
	for _, u := range utf16.Encode([]rune(s)) {
		out = append(out, byte(u&0xff), byte(u>>8)) //nolint:gosec // G115: the low and high byte of a 16-bit unit
	}
	return out
}

func writeFile(t *testing.T, path string, b []byte) {
	t.Helper()
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func appendFile(t *testing.T, path string, b []byte) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec // G304: a test temp file
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write(b); err != nil {
		t.Fatal(err)
	}
}

const logBanner = "2026-11-23 08:00:00.10 Server      Microsoft SQL Server 2019 (RTM) - 15.0.2000.5\r\n2026-11-23 08:00:00.11 Server      SQL Server is starting at normal priority base (=7).\r\n"

func TestClassifyLogLine(t *testing.T) {
	cases := []struct {
		name string
		line string
		want SQLLogEvent
		ok   bool
	}{
		{"error", "2026-11-23 10:15:32.12 Logon       Error: 18456, Severity: 14, State: 8.", SQLLogEvent{Kind: "error", Number: 18456, Severity: 14, State: 8, Count: 1}, true},
		{"error no fraction", "2026-11-23 10:15:32 spid53      Error: 1205, Severity: 13, State: 51.", SQLLogEvent{Kind: "error", Number: 1205, Severity: 13, State: 51, Count: 1}, true},
		{"startup", "2026-11-23 08:00:00.11 Server      SQL Server is starting at normal priority base (=7). This is an informational message only.", SQLLogEvent{Kind: "startup", Count: 1}, true},
		{"shutdown", "2026-11-23 20:00:00.11 spid12s     SQL Server is terminating in response to a 'stop' request from Service Control Manager.", SQLLogEvent{Kind: "shutdown", Count: 1}, true},
		{"slow io", `2026-11-23 10:00:00.00 spid22s     SQL Server has encountered 3 occurrence(s) of I/O requests taking longer than 15 seconds to complete on file [D:\Data\Shop (prod).mdf] in database [Shop] (5).  The OS file handle is 0x0000000000000A5C.`, SQLLogEvent{Kind: "slowio", Count: 3, Seconds: 15, Database: 5}, true},
		{"login text line", "2026-11-23 10:15:32.12 Logon       Login failed for user 'sa'. Reason: Password did not match that for the login provided. [CLIENT: 10.1.2.3]", SQLLogEvent{}, false},
		{"continuation", "   at some stack frame", SQLLogEvent{}, false},
		{"bad severity", "2026-11-23 10:15:32.12 Logon       Error: 18456, Severity: 99, State: 8.", SQLLogEvent{}, false},
		{"state overflow", "2026-11-23 10:15:32.12 Logon       Error: 18456, Severity: 14, State: 999.", SQLLogEvent{}, false},
		{"bad date", "2026-13-45 10:15:32.12 Logon       Error: 18456, Severity: 14, State: 8.", SQLLogEvent{}, false},
		{"error with trailing words", "2026-11-23 10:15:32.12 Logon       Error: 18456, Severity: 14, State: 8. password=hunter2", SQLLogEvent{}, false},
	}
	for _, c := range cases {
		got, ok := classifyLogLine(c.line, utc)
		if ok != c.ok {
			t.Errorf("%s: ok = %v, want %v", c.name, ok, c.ok)
			continue
		}
		if !ok {
			continue
		}
		got.T = time.Time{}
		if got != c.want {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
	}
}

func TestClassifyLogLineUsesLocalTime(t *testing.T) {
	loc := time.FixedZone("x", 4*3600)
	ev, ok := classifyLogLine("2026-11-23 10:00:00.00 Server      SQL Server is starting at normal priority base (=7).", loc)
	if !ok || !ev.T.Equal(time.Date(2026, 11, 23, 6, 0, 0, 0, time.UTC)) {
		t.Fatalf("got %v %v", ev.T, ok)
	}
}

func TestReadErrorLogFirstLookStartsAtTheEnd(t *testing.T) {
	p := filepath.Join(t.TempDir(), "ERRORLOG")
	writeFile(t, p, utf16le(logBanner, true))
	r := readErrorLog(p, sqlLogCursor{}, 1<<20, utc)
	if len(r.events) != 0 || !r.cur.Seen || r.cur.Offset != int64(len(utf16le(logBanner, true))) {
		t.Fatalf("first look must not read old lines: %+v", r)
	}
	appendFile(t, p, utf16le("2026-11-23 10:15:32.12 Logon       Error: 18456, Severity: 14, State: 8.\r\n", false))
	r2 := readErrorLog(p, r.cur, 1<<20, utc)
	if len(r2.events) != 1 || r2.events[0].Number != 18456 {
		t.Fatalf("events = %+v", r2.events)
	}
	r3 := readErrorLog(p, r2.cur, 1<<20, utc)
	if len(r3.events) != 0 || r3.cur != r2.cur {
		t.Fatalf("nothing new must read nothing: %+v", r3)
	}
}

func TestReadErrorLogLeavesAnUnfinishedLine(t *testing.T) {
	p := filepath.Join(t.TempDir(), "ERRORLOG")
	writeFile(t, p, utf16le(logBanner, true))
	cur := readErrorLog(p, sqlLogCursor{}, 1<<20, utc).cur
	half := "2026-11-23 10:15:32.12 Logon       Error: 18456, Severity: 14, State: 8."
	appendFile(t, p, utf16le(half, false))
	r := readErrorLog(p, cur, 1<<20, utc)
	if len(r.events) != 0 || r.cur.Offset != cur.Offset {
		t.Fatalf("an unfinished line must wait: %+v", r)
	}
	appendFile(t, p, utf16le("\r\n", false))
	r = readErrorLog(p, r.cur, 1<<20, utc)
	if len(r.events) != 1 {
		t.Fatalf("the finished line must be read once: %+v", r)
	}
}

func TestReadErrorLogUTF8(t *testing.T) {
	p := filepath.Join(t.TempDir(), "ERRORLOG")
	writeFile(t, p, []byte("2026-11-23 08:00:00.10 Server      hello\n"))
	cur := readErrorLog(p, sqlLogCursor{}, 1<<20, utc).cur
	appendFile(t, p, []byte("2026-11-23 10:15:32.12 Logon       Error: 4060, Severity: 11, State: 1.\n\xff\xfe garbage\n"))
	r := readErrorLog(p, cur, 1<<20, utc)
	if len(r.events) != 1 || r.events[0].Number != 4060 {
		t.Fatalf("events = %+v", r.events)
	}
}

func TestReadErrorLogCycledFinishesTheOldTail(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "ERRORLOG")
	writeFile(t, p, utf16le(logBanner, true))
	cur := readErrorLog(p, sqlLogCursor{}, 1<<20, utc).cur
	// A line lands, then SQL Server cycles the log before the next poll.
	appendFile(t, p, utf16le("2026-11-23 10:15:32.12 Logon       Error: 18456, Severity: 14, State: 8.\r\n", false))
	if err := os.Rename(p, p+".1"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, p, utf16le("2026-11-23 10:20:00.00 Server      Logging SQL Server messages in file 'ERRORLOG'.\r\n2026-11-23 10:21:00.00 Logon       Error: 18456, Severity: 14, State: 5.\r\n", true))
	r := readErrorLog(p, cur, 1<<20, utc)
	if r.lost != 0 || len(r.events) != 2 {
		t.Fatalf("want the old tail line and the new line, lost 0: %+v", r)
	}
	if r.events[0].State != 8 || r.events[1].State != 5 {
		t.Fatalf("order/states: %+v", r.events)
	}
	// And the next poll starts after what was read.
	r2 := readErrorLog(p, r.cur, 1<<20, utc)
	if len(r2.events) != 0 || r2.lost != 0 {
		t.Fatalf("second poll: %+v", r2)
	}
}

func TestReadErrorLogCycledWithoutOldFileCountsLost(t *testing.T) {
	p := filepath.Join(t.TempDir(), "ERRORLOG")
	writeFile(t, p, utf16le(logBanner, true))
	cur := readErrorLog(p, sqlLogCursor{}, 1<<20, utc).cur
	writeFile(t, p, utf16le("2026-11-23 10:21:00.00 Logon       Error: 18456, Severity: 14, State: 5.\r\n", true))
	r := readErrorLog(p, cur, 1<<20, utc)
	if r.lost != 1 || len(r.events) != 1 {
		t.Fatalf("lost = %d events = %+v", r.lost, r.events)
	}
}

func TestReadErrorLogBudgetTruncatesThenContinues(t *testing.T) {
	p := filepath.Join(t.TempDir(), "ERRORLOG")
	writeFile(t, p, []byte("2026-11-23 08:00:00.10 Server      hello\n"))
	cur := readErrorLog(p, sqlLogCursor{}, 1<<20, utc).cur
	line := "2026-11-23 10:15:32.12 Logon       Error: 4060, Severity: 11, State: 1.\n"
	for i := 0; i < 10; i++ {
		appendFile(t, p, []byte(line))
	}
	total := 0
	truncatedOnce := false
	for i := 0; i < 20 && total < 10; i++ {
		r := readErrorLog(p, cur, int64(len(line)*3), utc)
		truncatedOnce = truncatedOnce || r.truncated
		total += len(r.events)
		cur = r.cur
	}
	if total != 10 || !truncatedOnce {
		t.Fatalf("total = %d truncatedOnce = %v", total, truncatedOnce)
	}
}

func TestReadErrorLogGiantLineDoesNotStall(t *testing.T) {
	p := filepath.Join(t.TempDir(), "ERRORLOG")
	writeFile(t, p, []byte("x\n"))
	cur := readErrorLog(p, sqlLogCursor{}, 1<<20, utc).cur
	appendFile(t, p, []byte(strings.Repeat("a", 5000)+"\n2026-11-23 10:15:32.12 Logon       Error: 4060, Severity: 11, State: 1.\n"))
	got := 0
	for i := 0; i < 10; i++ {
		r := readErrorLog(p, cur, 1000, utc)
		got += len(r.events)
		cur = r.cur
	}
	if got != 1 {
		t.Fatalf("events = %d", got)
	}
}

func TestReadErrorLogRefusesNonRegularFiles(t *testing.T) {
	dir := t.TempDir()
	if r := readErrorLog(filepath.Join(dir, "missing"), sqlLogCursor{}, 1<<20, utc); !r.unreadable {
		t.Fatal("a missing file must count as unreadable")
	}
	if r := readErrorLog(dir, sqlLogCursor{}, 1<<20, utc); !r.unreadable {
		t.Fatal("a directory must be refused")
	}
	if runtime.GOOS == "windows" {
		return
	}
	target := filepath.Join(dir, "real")
	writeFile(t, target, []byte("x\n"))
	link := filepath.Join(dir, "ERRORLOG")
	if err := os.Symlink(target, link); err != nil {
		t.Skip("symlinks not allowed")
	}
	if r := readErrorLog(link, sqlLogCursor{}, 1<<20, utc); !r.unreadable {
		t.Fatal("a symlink must be refused")
	}
}

// The ERRORLOG holds text that can name logins, hosts, files and statements. Only
// numbers and fixed words may reach a sample.
func TestSQLLogNeverKeepsText(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "ERRORLOG")
	writeFile(t, p, utf16le(logBanner, true))
	g := newTestLogGroup(p, true, false)
	now := time.Date(2026, 11, 23, 10, 30, 0, 0, time.UTC)
	if _, err := g.Collect(now, 15); !errors.Is(err, errQuiet) {
		t.Fatalf("first look: %v", err)
	}
	secrets := []string{"hunter2", "svc_payroll", "10.1.2.3", "ACME-SQL01", "D:\\Data", "Shop", "SELECT", "Password=", "AKIA"}
	body := "2026-11-23 10:15:32.12 Logon       Error: 18456, Severity: 14, State: 8.\r\n" +
		"2026-11-23 10:15:32.12 Logon       Login failed for user 'svc_payroll'. Reason: Password did not match. [CLIENT: 10.1.2.3]\r\n" +
		"2026-11-23 10:15:33.00 spid53      Error: 102, Severity: 15, State: 1.\r\n" +
		"2026-11-23 10:15:33.00 spid53      Incorrect syntax near 'SELECT * FROM Shop WHERE Password=hunter2 AKIAIOSFODNN7EXAMPLE'. host ACME-SQL01\r\n" +
		"2026-11-23 10:16:00.00 spid22s     SQL Server has encountered 2 occurrence(s) of I/O requests taking longer than 15 seconds to complete on file [D:\\Data\\Shop.mdf] in database [Shop] (5).  The OS file handle is 0x1\r\n" +
		"2026-11-23 10:17:00.00 Server      Connection string: Server=ACME-SQL01;Password=hunter2\r\n"
	appendFile(t, p, utf16le(body, false))
	v, err := g.Collect(now.Add(2*time.Minute), 15)
	if err != nil || v == nil {
		t.Fatalf("Collect: (%v, %v)", v, err)
	}
	b, _ := json.Marshal(v.(*SQLLogGroup))
	for _, s := range secrets {
		if strings.Contains(string(b), s) {
			t.Fatalf("%q reached the sample: %s", s, b)
		}
	}
	var kinds []string
	for _, e := range v.(*SQLLogGroup).Errors {
		kinds = append(kinds, e.Kind)
	}
	if len(kinds) != 3 {
		t.Fatalf("want error 18456, error 102 and slow I/O: %v %s", kinds, b)
	}
}

func newTestLogGroup(path string, errlog, health bool) *sqlLogGroup {
	return &sqlLogGroup{
		errlog: errlog, health: health,
		discover:     func() []sqlLogInstance { return []sqlLogInstance{{Label: "MSSQLSERVER", LogPath: path}} },
		loc:          utc,
		decodeHealth: decodeSystemHealth,
	}
}

func TestSQLLogAggregatesAndCountsLatest(t *testing.T) {
	rows := []SQLLogEvent{
		{Instance: "A", Kind: "error", Number: 18456, Severity: 14, State: 8, Count: 1, T: time.Unix(100, 0)},
		{Instance: "A", Kind: "error", Number: 18456, Severity: 14, State: 8, Count: 1, T: time.Unix(300, 0)},
		{Instance: "A", Kind: "error", Number: 18456, Severity: 14, State: 5, Count: 1, T: time.Unix(200, 0)},
		{Instance: "A", Kind: "slowio", Seconds: 15, Database: 5, Count: 3, T: time.Unix(50, 0)},
	}
	out, cut := aggregateSQLLog(rows)
	if cut || len(out) != 3 || out[0].Count != 2 || !out[0].T.Equal(time.Unix(300, 0)) {
		t.Fatalf("out = %+v", out)
	}
	big := make([]SQLLogEvent, 0, 600)
	for i := 0; i < 600; i++ {
		big = append(big, SQLLogEvent{Instance: "A", Kind: "error", Number: uint32(i), Count: 1, T: time.Unix(int64(i), 0)})
	}
	capped, cut := aggregateSQLLog(big)
	if len(capped) != sqlLogMaxEvents || !cut {
		t.Fatalf("cap = %d cut = %v: dropped rows must be said", len(capped), cut)
	}
}

func TestSQLLogAckMovesTheCursorOnlyAfterTheWrite(t *testing.T) {
	p := filepath.Join(t.TempDir(), "ERRORLOG")
	writeFile(t, p, utf16le(logBanner, true))
	g := newTestLogGroup(p, true, false)
	now := time.Date(2026, 11, 23, 10, 30, 0, 0, time.UTC)
	_, _ = g.Collect(now, 15) // first look
	appendFile(t, p, utf16le("2026-11-23 10:15:32.12 Logon       Error: 18456, Severity: 14, State: 8.\r\n", false))
	v, err := g.Collect(now.Add(time.Minute), 15)
	if err != nil || v == nil {
		t.Fatalf("Collect: (%v, %v)", v, err)
	}
	eg := v.(*SQLLogGroup)
	for i := 0; i < 3; i++ {
		_, _ = json.Marshal(eg) // serializing must not acknowledge
	}
	// Not written yet: the same batch comes again, on the next tick, even inside the poll gap.
	v2, err := g.Collect(now.Add(time.Minute+15*time.Second), 15)
	if err != nil || v2 == nil || len(v2.(*SQLLogGroup).Errors) != 1 {
		t.Fatalf("an unacked batch must be re-sent: (%v, %v)", v2, err)
	}
	eg.Ack()
	eg.Ack() // idempotent
	if _, err := g.Collect(now.Add(2*time.Minute+30*time.Second), 15); !errors.Is(err, errQuiet) {
		t.Fatalf("after the ack nothing is new: %v", err)
	}
	var fromDisk SQLLogGroup
	if err := json.Unmarshal([]byte(`{"errors":[]}`), &fromDisk); err != nil {
		t.Fatal(err)
	}
	fromDisk.Ack()
	var nilGroup *SQLLogGroup
	nilGroup.Ack()
}

func TestSQLLogQuietBetweenPolls(t *testing.T) {
	p := filepath.Join(t.TempDir(), "ERRORLOG")
	writeFile(t, p, utf16le(logBanner, true))
	reads := 0
	g := newTestLogGroup(p, true, false)
	inner := g.discover
	g.discover = func() []sqlLogInstance { reads++; return inner() }
	now := time.Date(2026, 11, 23, 10, 30, 0, 0, time.UTC)
	_, _ = g.Collect(now, 15) // first look: the position is the end of the file
	appendFile(t, p, utf16le("2026-11-23 10:30:05.00 Logon       Error: 18456, Severity: 14, State: 8.\r\n", false))
	for _, d := range []time.Duration{15, 30, 45} {
		if v, err := g.Collect(now.Add(d*time.Second), 15); !errors.Is(err, errQuiet) || v != nil {
			t.Fatalf("+%ds: the file must not be read between polls: (%v, %v)", d, v, err)
		}
	}
	v, err := g.Collect(now.Add(61*time.Second), 15)
	if err != nil || v == nil {
		t.Fatalf("the next poll must read the line: (%v, %v)", v, err)
	}
	if reads != 1 {
		t.Fatalf("instances were looked up %d times, want 1 (every 10 minutes)", reads)
	}
}

func TestSQLLogUnreadableFileIsNamed(t *testing.T) {
	g := newTestLogGroup(filepath.Join(t.TempDir(), "ERRORLOG"), true, false)
	v, err := g.Collect(time.Date(2026, 11, 23, 10, 30, 0, 0, time.UTC), 15)
	if err != nil || v == nil || v.(*SQLLogGroup).Unreadable != 1 {
		t.Fatalf("a file that cannot be read must show, not vanish: (%v, %v)", v, err)
	}
}

func TestSQLLogInstanceChecks(t *testing.T) {
	root := t.TempDir()
	good := filepath.Join(root, "ERRORLOG")
	g := &sqlLogGroup{discover: func() []sqlLogInstance {
		return []sqlLogInstance{
			{Label: "MSSQLSERVER", LogPath: good},
			{Label: "MSSQLSERVER", LogPath: good},                           // twice
			{Label: "bad name", LogPath: good},                              // not an instance name
			{Label: "OTHER", LogPath: filepath.Join(root, "passwords.txt")}, // not an ERRORLOG
			{Label: "REL", LogPath: "ERRORLOG"},                             // not absolute
			{Label: "LOWER", LogPath: filepath.Join(root, "errorlog")},      // name case does not matter
		}
	}}
	got := g.found()
	if len(got) != 2 || got[0].Label != "MSSQLSERVER" || got[1].Label != "LOWER" {
		t.Fatalf("found = %+v", got)
	}
}

func TestSQLLogSystemHealthNotAvailableIsNamedOnce(t *testing.T) {
	p := filepath.Join(t.TempDir(), "ERRORLOG")
	writeFile(t, p, utf16le(logBanner, true))
	g := newTestLogGroup(p, false, true)
	now := time.Date(2026, 11, 23, 10, 30, 0, 0, time.UTC)
	v, err := g.Collect(now, 15)
	if err != nil || v == nil || !v.(*SQLLogGroup).HealthUnavailable {
		t.Fatalf("first answer must say not available: (%v, %v)", v, err)
	}
	v.(*SQLLogGroup).Ack()
	if _, err := g.Collect(now.Add(2*time.Minute), 15); !errors.Is(err, errQuiet) {
		t.Fatalf("it must be said once, not every poll: %v", err)
	}
}

func TestSQLLogSystemHealthEventsAreCountedOnce(t *testing.T) {
	p := filepath.Join(t.TempDir(), "ERRORLOG")
	writeFile(t, p, utf16le(logBanner, true))
	now := time.Date(2026, 11, 23, 10, 30, 0, 0, time.UTC)
	events := []rawHealth{
		{Kind: SQLHealthDeadlock, T: now.Add(-time.Hour)}, // before the start: never backfilled
		{Kind: SQLHealthDeadlock, T: now.Add(10 * time.Second)},
		{Kind: SQLHealthDeadlock, T: now.Add(20 * time.Second)},
		{Kind: SQLHealthLongWait, T: now.Add(30 * time.Second), Ms: 4500},
		{Kind: SQLHealthLongWait, T: now.Add(31 * time.Second), Ms: 9000},
	}
	g := newTestLogGroup(p, false, true)
	g.decodeHealth = func(dir string, since time.Time) ([]rawHealth, error) {
		if dir != filepath.Dir(p) {
			t.Errorf("dir = %q", dir)
		}
		return events, nil
	}
	v, err := g.Collect(now, 15)
	if err != nil || v == nil {
		t.Fatalf("Collect: (%v, %v)", v, err)
	}
	eg := v.(*SQLLogGroup)
	if len(eg.Health) != 2 || eg.HealthUnavailable {
		t.Fatalf("health = %+v", eg.Health)
	}
	for _, h := range eg.Health {
		switch h.Kind {
		case SQLHealthDeadlock:
			if h.Count != 2 {
				t.Errorf("deadlocks = %d", h.Count)
			}
		case SQLHealthLongWait:
			if h.Count != 2 || h.MaxMs != 9000 {
				t.Errorf("longwait = %+v", h)
			}
		}
	}
	eg.Ack()
	if _, err := g.Collect(now.Add(4*time.Minute), 15); !errors.Is(err, errQuiet) {
		t.Fatalf("the same events must not count twice: %v", err)
	}
}

func TestSQLHostLogsFollowTheProfile(t *testing.T) {
	cases := []struct {
		name         string
		sql          *SQLSettings
		errlog, hlth bool
	}{
		{"no block", nil, false, false},
		{"standard", &SQLSettings{Profile: "standard"}, false, false},
		{"everything", &SQLSettings{Profile: "everything"}, true, true},
		{"deep", &SQLSettings{Profile: "deep"}, true, true},
		{"only errlog", &SQLSettings{Extras: []string{"errlog"}}, true, false},
		{"only syshealth", &SQLSettings{Extras: []string{"syshealth"}}, false, true},
		{"other extras", &SQLSettings{Extras: []string{"perfctr"}}, false, false},
	}
	for _, c := range cases {
		e, h := (Config{SQL: c.sql}).SQLHostLogs()
		if e != c.errlog || h != c.hlth {
			t.Errorf("%s: got (%v,%v), want (%v,%v)", c.name, e, h, c.errlog, c.hlth)
		}
	}
}

func TestSQLLogGroupIsAbsentOnStandard(t *testing.T) {
	g, err := newSQLLogGroup(GroupDeps{Config: Default()})
	if !errors.Is(err, errNoTargets) || g != nil {
		t.Fatalf("standard profile must build no group: (%v, %v)", g, err)
	}
}

func TestSQLLogSampleRoundTripAndAck(t *testing.T) {
	want := Sample{V: FormatVersion, Run: "r", Seq: 1, T: t0, SQLLog: &SQLLogGroup{
		Errors: []SQLLogEvent{{Instance: "MSSQLSERVER", Kind: "error", Number: 18456, Severity: 14, State: 8, Count: 2, T: t0}},
		Health: []SQLHealthEvent{{Instance: "MSSQLSERVER", Kind: "deadlock", Count: 1, T: t0}},
	}}
	line, err := jsonLine(want)
	if err != nil {
		t.Fatal(err)
	}
	var got Sample
	if err := json.Unmarshal([]byte(line), &got); err != nil {
		t.Fatal(err)
	}
	if got.SQLLog == nil || got.SQLLog.Errors[0].Number != 18456 || got.SQLLog.Health[0].Kind != "deadlock" {
		t.Fatalf("round trip: %s", line)
	}
	if strings.Contains(line, "commit") || strings.Contains(line, "pending") {
		t.Fatalf("ack fields must not be written: %s", line)
	}
}

func TestSQLLogDropsLinesDatedInTheFuture(t *testing.T) {
	p := filepath.Join(t.TempDir(), "ERRORLOG")
	writeFile(t, p, utf16le(logBanner, true))
	g := newTestLogGroup(p, true, false)
	now := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	_, _ = g.Collect(now, 15)
	appendFile(t, p, utf16le("2026-11-23 10:00:05.00 Logon       Error: 18456, Severity: 14, State: 8.\r\n2031-01-01 00:00:00.00 Logon       Error: 9999, Severity: 20, State: 1.\r\n", false))
	v, err := g.Collect(now.Add(2*time.Minute), 15)
	if err != nil || v == nil {
		t.Fatalf("Collect: (%v, %v)", v, err)
	}
	es := v.(*SQLLogGroup).Errors
	if len(es) != 1 || es[0].Number != 18456 {
		t.Fatalf("a forged future line must be dropped: %+v", es)
	}
}

func TestSQLLogPathOKRefusesNetworkAndDevicePaths(t *testing.T) {
	for _, p := range []string{`\\host\share\ERRORLOG`, `\\?\UNC\host\share\ERRORLOG`, `\\?\GLOBALROOT\Device\x\ERRORLOG`, `\\.\pipe\ERRORLOG`, `//host/share/ERRORLOG`, ""} {
		if sqlLogPathOK(p) {
			t.Errorf("%q must be refused", p)
		}
	}
}

func TestSQLLogUnknownHealthKindIsDropped(t *testing.T) {
	p := filepath.Join(t.TempDir(), "ERRORLOG")
	writeFile(t, p, utf16le(logBanner, true))
	now := time.Date(2026, 11, 23, 10, 30, 0, 0, time.UTC)
	g := newTestLogGroup(p, false, true)
	g.decodeHealth = func(string, time.Time) ([]rawHealth, error) {
		return []rawHealth{{Kind: "password=hunter2", T: now.Add(time.Second)}, {Kind: SQLHealthDeadlock, T: now.Add(2 * time.Second)}}, nil
	}
	v, err := g.Collect(now, 15)
	if err != nil || v == nil {
		t.Fatalf("Collect: (%v, %v)", v, err)
	}
	h := v.(*SQLLogGroup).Health
	if len(h) != 1 || h[0].Kind != SQLHealthDeadlock {
		t.Fatalf("health = %+v", h)
	}
}

func TestSQLLogNotAvailableIsSaidWithSeveralInstances(t *testing.T) {
	root := t.TempDir()
	a, b := filepath.Join(root, "a", "ERRORLOG"), filepath.Join(root, "b", "ERRORLOG")
	for _, p := range []string{a, b} {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		writeFile(t, p, utf16le(logBanner, true))
	}
	g := newTestLogGroup(a, false, true)
	g.discover = func() []sqlLogInstance {
		return []sqlLogInstance{{Label: "A", LogPath: a}, {Label: "B", LogPath: b}}
	}
	v, err := g.Collect(time.Date(2026, 11, 23, 10, 30, 0, 0, time.UTC), 15)
	if err != nil || v == nil || !v.(*SQLLogGroup).HealthUnavailable {
		t.Fatalf("two instances must still say not available: (%v, %v)", v, err)
	}
}

func TestSQLLogRepeatedFlagBatchesAreAckedOneByOne(t *testing.T) {
	g := newTestLogGroup(filepath.Join(t.TempDir(), "ERRORLOG"), true, false) // missing file: Unreadable every poll
	now := time.Date(2026, 11, 23, 10, 30, 0, 0, time.UTC)
	v1, _ := g.Collect(now, 15)
	if v1 == nil {
		t.Fatal("no first batch")
	}
	// Not acked yet (the write failed): the same batch comes again.
	if v, _ := g.Collect(now.Add(61*time.Second), 15); v == nil {
		t.Fatal("an unacked batch must be re-sent")
	}
	v1.(*SQLLogGroup).Ack()
	// After the ack the next poll makes a NEW batch, which is not acked by the old ack.
	v2, _ := g.Collect(now.Add(122*time.Second), 15)
	if v2 == nil {
		t.Fatal("no second batch")
	}
	if v3, _ := g.Collect(now.Add(123*time.Second), 15); v3 == nil {
		t.Fatal("the second batch was treated as acked before its write")
	}
}

func TestReadErrorLogTwoLogsStartedInTheSameMinuteAreTold(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "ERRORLOG")
	first := "2026-11-23 10:15:01.10 Server      Microsoft SQL Server 2019\r\n2026-11-23 10:15:01.11 Server      SQL Server is starting at normal priority base (=7).\r\n"
	second := "2026-11-23 10:15:09.40 Server      Microsoft SQL Server 2019\r\n2026-11-23 10:15:09.41 Server      SQL Server is starting at normal priority base (=7).\r\n"
	writeFile(t, p, utf16le(first, true))
	cur := readErrorLog(p, sqlLogCursor{}, 1<<20, utc).cur
	if err := os.Rename(p, p+".1"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, p, utf16le(second+strings.Repeat("x", 10)+"\r\n", true))
	r := readErrorLog(p, cur, 1<<20, utc)
	if r.lost != 0 || len(r.events) != 1 || r.events[0].Kind != SQLLogKindStartup {
		t.Fatalf("a cycled log with the same start minute must be noticed and its startup line read: %+v", r)
	}
}

func TestReadErrorLogTruncatedInPlaceReadsFromTheTop(t *testing.T) {
	p := filepath.Join(t.TempDir(), "ERRORLOG")
	writeFile(t, p, utf16le(logBanner+logBanner, true))
	cur := readErrorLog(p, sqlLogCursor{}, 1<<20, utc).cur
	writeFile(t, p, utf16le("2026-11-23 10:21:00.00 Logon       Error: 18456, Severity: 14, State: 5.\r\n", true))
	r := readErrorLog(p, cur, 1<<20, utc)
	if len(r.events) != 1 || r.lost != 1 {
		t.Fatalf("a shrunk file starts again from the top and says it lost something: %+v", r)
	}
}

func TestReadErrorLogOddUTF16TailWaits(t *testing.T) {
	p := filepath.Join(t.TempDir(), "ERRORLOG")
	writeFile(t, p, utf16le(logBanner, true))
	cur := readErrorLog(p, sqlLogCursor{}, 1<<20, utc).cur
	line := utf16le("2026-11-23 10:15:32.12 Logon       Error: 18456, Severity: 14, State: 8.\r\n", false)
	appendFile(t, p, line[:len(line)-1]) // the last byte of the final newline has not arrived
	r := readErrorLog(p, cur, 1<<20, utc)
	if len(r.events) != 0 || r.cur.Offset != cur.Offset || r.cur.Offset%2 != 0 {
		t.Fatalf("an unfinished character must wait: %+v", r)
	}
	appendFile(t, p, line[len(line)-1:])
	if r2 := readErrorLog(p, r.cur, 1<<20, utc); len(r2.events) != 1 {
		t.Fatalf("after the byte arrives the line is read: %+v", r2)
	}
}
