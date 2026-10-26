package dbhealth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type fakeQ struct {
	by  map[string][]Row
	err map[string]error
}

func (f fakeQ) Query(_ context.Context, text string) ([]Row, error) {
	for k, e := range f.err {
		if strings.Contains(text, k) {
			return nil, e
		}
	}
	for k, r := range f.by {
		if strings.Contains(text, k) {
			return r, nil
		}
	}
	return nil, nil
}

var fixedNow = func() time.Time { return time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC) }

func TestEvalServerWarnsOnRecentRestart(t *testing.T) {
	env := &Env{}
	f := evalServer([]Row{{"version": "15.0.2000", "edition": "Standard", "uptime_min": int64(20), "cpus": int64(8), "memory_mb": int64(32768)}}, env)
	if f.Status != Warn || env.Version != "15.0.2000" || env.UptimeMin != 20 {
		t.Fatalf("got %+v env %+v", f, env)
	}
	f = evalServer([]Row{{"version": "15", "uptime_min": int64(6000)}}, &Env{})
	if f.Status != OK {
		t.Fatalf("long uptime should be ok: %+v", f)
	}
}

func TestEvalCPUThresholds(t *testing.T) {
	mk := func(sql, idle int64, n int) []Row {
		var rows []Row
		for i := 0; i < n; i++ {
			rows = append(rows, Row{"at": time.Now(), "sql_cpu": sql, "idle": idle})
		}
		return rows
	}
	cases := []struct {
		sql, idle int64
		want      Status
	}{{20, 70, OK}, {75, 20, Warn}, {95, 3, Bad}, {10, 40, Warn}}
	for _, c := range cases {
		if got := evalCPU(mk(c.sql, c.idle, 10), &Env{}).Status; got != c.want {
			t.Errorf("sql=%d idle=%d: got %s want %s", c.sql, c.idle, got, c.want)
		}
	}
	if evalCPU(nil, &Env{}).Status != Note {
		t.Error("no samples should be a note")
	}
}

func TestEvalWaitsExplainsAndFlags(t *testing.T) {
	rows := []Row{{"wait_type": "WRITELOG", "wait_time_ms": int64(500000), "pct": 45.0}, {"wait_type": "ASYNC_NETWORK_IO", "wait_time_ms": int64(100000), "pct": 9.0}}
	f := evalWaits(rows, &Env{UptimeMin: 600})
	if f.Status != Warn || !strings.Contains(f.Summary, "transaction log") {
		t.Fatalf("got %+v", f)
	}
	if got := evalWaits([]Row{{"wait_type": "ASYNC_NETWORK_IO", "wait_time_ms": int64(5), "pct": 80.0}}, &Env{}).Status; got != Note {
		t.Fatalf("client-side wait is a note, got %s", got)
	}
}

func TestEvalMemory(t *testing.T) {
	ok := Row{"total_mb": int64(32000), "avail_mb": int64(9000), "state": "Available physical memory is high", "ple": int64(5000), "grants_pending": int64(0)}
	if evalMemory([]Row{ok}, nil).Status != OK {
		t.Error("healthy memory should be ok")
	}
	low := Row{"total_mb": int64(32000), "avail_mb": int64(100), "state": "Available physical memory is low", "ple": int64(5000), "grants_pending": int64(0)}
	if evalMemory([]Row{low}, nil).Status != Bad {
		t.Error("low state should be bad")
	}
	grants := Row{"total_mb": int64(32000), "avail_mb": int64(9000), "state": "ok", "ple": int64(5000), "grants_pending": int64(3)}
	if evalMemory([]Row{grants}, nil).Status != Warn {
		t.Error("pending grants should warn")
	}
}

func TestEvalRequests(t *testing.T) {
	if evalRequests(nil, nil).Status != OK {
		t.Error("none running is ok")
	}
	blocked := []Row{{"session": int64(55), "blocked_by": int64(60), "wait_ms": int64(25000), "secs": int64(30)}}
	f := evalRequests(blocked, nil)
	if f.Status != Bad || !strings.Contains(f.Summary, "blocked") {
		t.Fatalf("got %+v", f)
	}
	slow := []Row{{"session": int64(55), "blocked_by": int64(0), "wait_ms": int64(0), "secs": int64(90)}}
	if evalRequests(slow, nil).Status != Warn {
		t.Error("long runner warns")
	}
}

func TestEvalTopQueriesClipsText(t *testing.T) {
	long := strings.Repeat("x", 400)
	f := evalTopQueries([]Row{{"runs": int64(10), "avg_ms": int64(2500), "total_ms": int64(25000), "avg_cpu_ms": int64(1), "avg_reads": int64(1), "db": "d", "stmt": long}}, nil)
	if f.Status != Warn {
		t.Fatalf("slow average should warn: %+v", f)
	}
	if n := len([]rune(f.Rows[0][6])); n > maxCell {
		t.Fatalf("statement not clipped: %d", n)
	}
}

func TestEvalQueryStoreSpike(t *testing.T) {
	var rows []Row
	for i := 0; i < 10; i++ {
		rows = append(rows, Row{"hour_start": "h", "runs": int64(100), "avg_ms": 20.0})
	}
	if evalQueryStore(rows).Status != OK {
		t.Error("flat timings are ok")
	}
	rows[7] = Row{"hour_start": "2026-10-26 07:00", "runs": int64(100), "avg_ms": 400.0}
	f := evalQueryStore(rows)
	if f.Status != Warn || !strings.Contains(f.Summary, "07:00") {
		t.Fatalf("spike not found: %+v", f)
	}
}

func TestEvalBackups(t *testing.T) {
	rows := []Row{
		{"db": "A", "model": "SIMPLE", "full_hours": int64(5), "log_hours": nil},
		{"db": "B", "model": "FULL", "full_hours": int64(5), "log_hours": nil},
		{"db": "C", "model": "SIMPLE", "full_hours": nil, "log_hours": nil},
	}
	f := evalBackups(rows, nil)
	if f.Status != Bad || len(f.Rows) != 2 {
		t.Fatalf("got %+v", f)
	}
	if evalBackups(rows[:1], nil).Status != OK {
		t.Error("recent simple-model backup is ok")
	}
}

func TestEvalSettings(t *testing.T) {
	f := evalSettings([]Row{{"db": "A", "auto_close": int64(0), "auto_shrink": int64(1), "page_verify": "CHECKSUM", "state": "ONLINE"}}, nil)
	if f.Status != Warn {
		t.Fatalf("auto-shrink should warn: %+v", f)
	}
	f = evalSettings([]Row{{"db": "A", "auto_close": int64(0), "auto_shrink": int64(0), "page_verify": "CHECKSUM", "state": "SUSPECT"}}, nil)
	if f.Status != Bad {
		t.Fatalf("suspect database is bad: %+v", f)
	}
}

func TestEvalDeadlocksAndMissingIndexes(t *testing.T) {
	if evalDeadlocks([]Row{{"deadlocks": int64(0)}}, &Env{UptimeMin: 100}).Status != OK {
		t.Error("zero deadlocks ok")
	}
	if evalDeadlocks([]Row{{"deadlocks": int64(50)}}, &Env{UptimeMin: 100}).Status != Warn {
		t.Error("many deadlocks warn")
	}
	if evalMissingIndexes(nil, &Env{}).Status != Note {
		t.Error("no database is a note")
	}
	f := evalMissingIndexes([]Row{{"tbl": "T", "equality": "[a]", "inequality": "", "score": int64(500000), "uses": int64(9)}}, &Env{Database: "d"})
	if f.Status != Warn {
		t.Fatalf("high score warns: %+v", f)
	}
}

func TestAgentWhen(t *testing.T) {
	if got := agentWhen(20261025, 93005); got != "2026-10-25 09:30" {
		t.Fatal(got)
	}
}

func TestClassifyNeverEchoesDriverText(t *testing.T) {
	secret := "login failed for user 'sa' on host PRODSQL01"
	for _, c := range []struct{ msg, want string }{
		{"The user does not have permission to perform this action. " + secret, reasonPermission},
		{"VIEW SERVER STATE permission was denied", reasonPermission},
		{"Invalid object name 'sys.query_store_x'. " + secret, reasonNotThere},
		{"i/o timeout " + secret, reasonFailed},
	} {
		got := classify(errors.New(c.msg))
		if got != c.want || strings.Contains(got, "PRODSQL01") {
			t.Errorf("%q -> %q", c.msg, got)
		}
	}
}

func TestRunSQLSurvivesFailuresAndRanks(t *testing.T) {
	q := fakeQ{
		by: map[string][]Row{
			"SERVERPROPERTY('ProductVersion')": {{"version": "16.0.1000", "edition": "Dev", "uptime_min": int64(9000), "cpus": int64(4), "memory_mb": int64(16000)}},
			"msdb.dbo.backupset":               {{"db": "X", "model": "SIMPLE", "full_hours": nil, "log_hours": nil}},
		},
		err: map[string]error{"dm_os_ring_buffers": errors.New("VIEW SERVER STATE permission was denied on object 'server'")},
	}
	rep := RunSQL(context.Background(), q, "Prod", "AuditDb", fixedNow)
	if len(rep.Findings) != 13 {
		t.Fatalf("want 13 findings, got %d", len(rep.Findings))
	}
	if rep.Version != "16.0.1000" || rep.Label != "Prod" || rep.Overall != Bad {
		t.Fatalf("report %+v", rep)
	}
	byID := map[string]Finding{}
	for _, f := range rep.Findings {
		byID[f.ID] = f
	}
	if byID["cpu"].Status != Unavailable || byID["cpu"].Summary != reasonPermission {
		t.Fatalf("cpu should be unavailable for permission: %+v", byID["cpu"])
	}
	if byID["backups"].Status != Bad {
		t.Fatalf("backups: %+v", byID["backups"])
	}
}

func TestRunSQLStopsOnCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rep := RunSQL(ctx, fakeQ{}, "x", "", fixedNow)
	if len(rep.Findings) != 0 {
		t.Fatalf("a cancelled run should not start checks: %d", len(rep.Findings))
	}
}

func TestQueryStoreNeedsADatabaseAndHandlesOff(t *testing.T) {
	f := runQueryStore(context.Background(), fakeQ{}, &Env{})
	if f.Status != Note {
		t.Fatalf("no database: %+v", f)
	}
	off := fakeQ{by: map[string][]Row{"database_query_store_options": {{"state": "OFF"}}}}
	f = runQueryStore(context.Background(), off, &Env{Database: "d"})
	if f.Status != Note || !strings.Contains(f.Summary, "off") {
		t.Fatalf("off: %+v", f)
	}
}

func TestReportFinishPicksWorst(t *testing.T) {
	r := Report{Findings: []Finding{{Status: OK}, {Status: Warn}, {Status: Unavailable}, {Status: Note}}}
	r.Finish(fixedNow())
	if r.Overall != Warn {
		t.Fatal(r.Overall)
	}
}
