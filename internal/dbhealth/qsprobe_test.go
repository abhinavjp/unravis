package dbhealth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

var probeNow = func() time.Time { return time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC) }

func TestProbeOffWithHistoryAnswersYes(t *testing.T) {
	q := offSQL(at(2, 0, 0), at(20, 10, 45))
	p := ProbeQueryStoreOff(context.Background(), q, "Prod", "AuditDb", probeNow)
	if p.State != QSStateOff || p.FirstData == nil || p.LastData == nil || p.Points == 0 || p.Refused != 0 {
		t.Fatalf("probe = %+v", p)
	}
	if p.Verdict() != VerdictRows || p.Version != "16.0.1" || p.Edition != "Standard" || p.Actual != "OFF" || p.Desired != "OFF" {
		t.Fatalf("probe = %+v verdict %s", p, p.Verdict())
	}
	out := p.Format()
	for _, want := range []string{"First kept block (start):    2026-10-02 00:00 UTC", "Last kept block (start):     2026-10-20 10:45 UTC", "VERDICT: " + VerdictRows, "Query Store is off. It kept history from"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestProbeOffWithNoHistoryAnswersNo(t *testing.T) {
	q := healthySQL("16.0.1")
	q.by["unravis:qs_options"] = offOpts("OFF")
	q.by["unravis:qs_range"] = []Row{{"first_start": nil, "last_start": nil}}
	p := ProbeQueryStoreOff(context.Background(), q, "Prod", "AuditDb", probeNow)
	if p.Verdict() != VerdictNoRows || p.Points != 0 || p.FirstData != nil {
		t.Fatalf("probe = %+v verdict %s", p, p.Verdict())
	}
	if out := p.Format(); !strings.Contains(out, "First kept block (start):    none") || !strings.Contains(out, "Rows read (blocks):          0 (0 executions)") {
		t.Errorf("output:\n%s", out)
	}
}

func TestProbeOnDatabaseIsNotAnswered(t *testing.T) {
	p := ProbeQueryStoreOff(context.Background(), healthySQL("16.0.1"), "Prod", "AuditDb", probeNow)
	if p.State != QSStateOn || p.Verdict() != VerdictNotOff {
		t.Fatalf("probe = %+v verdict %s", p, p.Verdict())
	}
}

func TestProbeKeptBlocksButNoRowsIsNotYes(t *testing.T) {
	q := offSQL(at(2, 0, 0), at(20, 10, 45))
	q.by["unravis:qs_intervals"] = nil
	q.by["unravis:qs_stats"] = nil
	p := ProbeQueryStoreOff(context.Background(), q, "Prod", "AuditDb", probeNow)
	if p.Verdict() != VerdictNoneInSpan {
		t.Fatalf("verdict %s probe %+v", p.Verdict(), p)
	}
}

func TestProbeUnreadableServer(t *testing.T) {
	q := fakeQ{err: map[string]error{"unravis:": errors.New("boom")}}
	p := ProbeQueryStoreOff(context.Background(), q, "Prod", "AuditDb", probeNow)
	if p.Verdict() != VerdictUnread || p.BaseErr == "" {
		t.Fatalf("probe = %+v verdict %s", p, p.Verdict())
	}
	if !strings.Contains(p.Format(), "Server version:              not read") {
		t.Errorf("output:\n%s", p.Format())
	}
}

// The probe sends only what the app sends plus two fixed selects, and the guard lets every one of them through.
func TestProbeSendsOnlyReads(t *testing.T) {
	var seen []string
	rec := recordingQ{fn: func(s string) { seen = append(seen, s) }, inner: offSQL(at(2, 0, 0), at(20, 10, 45))}
	p := ProbeQueryStoreOff(context.Background(), rec, "Prod", "AuditDb", probeNow)
	if len(seen) < 8 || p.Refused != 0 {
		t.Fatalf("%d queries, %d refused", len(seen), p.Refused)
	}
	for _, s := range seen {
		if strings.Contains(strings.ToUpper(s), "QUERY_STORE =") {
			t.Errorf("a text switches Query Store: %s", s)
		}
	}
}

func TestReadOnlyGuardRefusesWrites(t *testing.T) {
	inner := recordingQ{fn: func(string) { t.Error("a refused text reached the server") }, inner: fakeQ{}}
	g := &readOnlyQueryer{inner: inner}
	bad := []string{
		"ALTER DATABASE [X] SET QUERY_STORE = OFF",
		"alter database x set query_store = on",
		"UPDATE t SET a = 1",
		"DROP TABLE t",
		"SELECT 1; SELECT 2",
		"EXEC sp_query_store_flush_db",
		"EXEC sp_readerrorlog 0, 1, N'x'; DROP TABLE t",
		"INSERT INTO t VALUES (1)",
		// Bypasses of the first guard: a procedure named without EXEC, a comment that held the magic substring, SELECT INTO, other verbs.
		"sp_query_store_flush_db",
		"sp_query_store_remove_query 1",
		"/* sp_readerrorlog 0, 1, */ EXEC sp_query_store_reset_exec_stats 1",
		"/* unravis:x */ EXEC sp_query_store_reset_exec_stats 1",
		"/* unravis:x */ SELECT * INTO dbo.x FROM sys.objects",
		"SELECT * INTO dbo.x FROM sys.objects",
		"BACKUP DATABASE [X] TO DISK = 'x'",
		"/* unravis:x */ SELECT 1 BACKUP DATABASE X TO DISK = 'x'",
		"DENY SELECT ON t TO u",
		"CHECKPOINT",
		"/* unravis:x */ SELECT 1 CHECKPOINT",
		"DISABLE TRIGGER ALL ON DATABASE",
		"USE master",
		"SET NOCOUNT ON",
		"/* unravis:x */ SELECT 1 sp_configure 'x', 1",
		"/* unravis:x */ SELECT 1\nDROP TABLE t",
		"/* unravis:x */ SELECT 1 /* more */",
		"/* unravis:x */ SELECT 1 -- note",
		"EXEC sp_readerrorlog 0, 1, N'cachestore flush' ",
		"EXEC sp_readerrorlog 0, 1, N'anything else'",
		"/* unravis:x */ select 1 from xp_dirtree",
		"-- x\nSELECT 1",
		"",
	}
	for _, b := range bad {
		if _, err := g.Query(context.Background(), b); !errors.Is(err, ErrProbeNotReadOnly) {
			t.Errorf("%q: err = %v", b, err)
		}
	}
	if g.refused != len(bad) {
		t.Errorf("refused = %d of %d", g.refused, len(bad))
	}
	g2 := &readOnlyQueryer{inner: fakeQ{}}
	for _, ok := range []string{qLaneBase, qLaneServer, qLaneQSOptions, qLaneQSFirst, qLaneQSRange, qLaneLogFlush, qLaneLogStart, qLaneLogEnum} {
		if _, err := g2.Query(context.Background(), ok); err != nil {
			t.Errorf("%q refused: %v", ok, err)
		}
	}
}

// Every text the lane can send, over the fixtures the lane tests use (every era, QS on, off, read-only, a window of many slices), is on the
// allowlist: a new lane text that the guard refuses fails here, not on the owner's PC.
func TestReadOnlyGuardAllowsEveryLaneText(t *testing.T) {
	long := laneReq
	long.Incident = Window{time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC), time.Date(2026, 10, 29, 11, 0, 0, 0, time.UTC)}
	long.Reference = Window{time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC), time.Date(2026, 10, 10, 11, 0, 0, 0, time.UTC)}
	fixtures := map[string]fakeQ{
		"2019 on":   healthySQL("15.0.2000"),
		"2022 on":   healthySQL("16.0.1000"),
		"off":       offSQL(at(2, 0, 0), at(20, 10, 45)),
		"read-only": readOnlySQL(at(2, 0, 0), at(20, 10, 45)),
	}
	for name, f := range fixtures {
		for _, req := range []LaneRequest{laneReq, long} {
			g := &readOnlyQueryer{inner: f}
			RunSQLLane(context.Background(), g, "Prod", req, fixedNow)
			if g.refused != 0 {
				t.Errorf("%s: the guard refused %d lane texts", name, g.refused)
			}
		}
	}
}

func TestProbeFailedReadIsNeverNoRows(t *testing.T) {
	// The range read works, then the stats read fails.
	q := offSQL(at(2, 0, 0), at(20, 10, 45))
	q.err = map[string]error{"unravis:qs_stats": errors.New("timeout")}
	p := ProbeQueryStoreOff(context.Background(), q, "Prod", "AuditDb", probeNow)
	if p.Verdict() != VerdictUnread {
		t.Fatalf("verdict %s probe %+v", p.Verdict(), p)
	}
}

func TestProbeDeniedOrUnknownStateIsNotNotOff(t *testing.T) {
	q := healthySQL("16.0.1")
	q.err = map[string]error{"unravis:qs_options": errors.New("The user does not have permission to perform this action.")}
	p := ProbeQueryStoreOff(context.Background(), q, "Prod", "AuditDb", probeNow)
	if p.Verdict() != VerdictUnread {
		t.Errorf("denied: verdict %s state %s", p.Verdict(), p.State)
	}
	q = healthySQL("16.0.1")
	q.by["unravis:qs_options"] = nil
	p = ProbeQueryStoreOff(context.Background(), q, "Prod", "AuditDb", probeNow)
	if p.Verdict() != VerdictUnread {
		t.Errorf("no options row: verdict %s state %s", p.Verdict(), p.State)
	}
}
