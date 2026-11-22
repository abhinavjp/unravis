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
	if p.Verdict() == VerdictRows || p.Verdict() == VerdictNoRows || p.BaseErr == "" {
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
	for _, bad := range []string{
		"ALTER DATABASE [X] SET QUERY_STORE = OFF",
		"alter database x set query_store = on",
		"UPDATE t SET a = 1",
		"DROP TABLE t",
		"SELECT 1; SELECT 2",
		"EXEC sp_query_store_flush_db",
		"EXEC sp_readerrorlog 0, 1, N'x'; DROP TABLE t",
		"INSERT INTO t VALUES (1)",
	} {
		if _, err := g.Query(context.Background(), bad); !errors.Is(err, ErrProbeNotReadOnly) {
			t.Errorf("%q: err = %v", bad, err)
		}
	}
	if g.refused != 8 {
		t.Errorf("refused = %d", g.refused)
	}
	g2 := &readOnlyQueryer{inner: fakeQ{}}
	for _, ok := range []string{qLaneBase, qLaneQSOptions, qLaneQSRange, qLaneLogFlush, qLaneLogEnum} {
		if _, err := g2.Query(context.Background(), ok); err != nil {
			t.Errorf("%q refused: %v", ok, err)
		}
	}
}
