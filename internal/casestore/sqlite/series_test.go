package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/evidence"
)

// bigCase imports n events (kind cpu on even seconds, wait on odd) in one go.
func bigCase(t *testing.T, s *Store, n int) *Case {
	t.Helper()
	info := mustCreate(t, s, "big")
	c := mustOpen(t, s, info.ID)
	mustImport(t, c, "big", n)
	return c
}

// Series over a case larger than the 50 000 event cap of Events: every event is
// read, and the bucket statistics are exact.
func TestSeriesReadsPastTheEventsCap(t *testing.T) {
	if testing.Short() {
		t.Skip("imports 60 000 events")
	}
	const n = 60_000
	s, _ := newTestStore(t)
	c := bigCase(t, s, n)
	ctx := context.Background()

	if got, _ := c.Events(ctx, casestore.EventQuery{}); len(got) != casestore.MaxEventQuery {
		t.Fatalf("Events returned %d, want its cap %d", len(got), casestore.MaxEventQuery)
	}
	res, err := casestore.Series(ctx, c, casestore.SeriesQuery{
		From: testBase, To: testBase.Add((n - 1) * time.Second), BucketMs: 100_000,
		Sel: []string{"kind=cpu", "kind=wait;host=db-a;source=" + testImporter},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Partial || res.Buckets != n/100 {
		t.Fatalf("partial=%v buckets=%d", res.Partial, res.Buckets)
	}
	// Bucket b holds seconds 100b..100b+99; the value of event i is i.
	for si, first := range []int{0, 1} {
		pts := res.Series[si].Points
		if res.Series[si].Count != n/2 || len(pts) != n/100 {
			t.Fatalf("series %d: count=%d points=%d", si, res.Series[si].Count, len(pts))
		}
		for b, p := range pts {
			want := casestore.SeriesPoint{
				T: testBase.UnixMilli() + int64(b)*100_000, Min: float64(100*b + first), Max: float64(100*b + 98 + first),
				Mean: float64(100*b + 49 + first), Count: 50,
			}
			if p != want {
				t.Fatalf("series %d bucket %d: %+v, want %+v", si, b, p, want)
			}
		}
	}
	if u := res.Series[0].Unit; u != "percent" || res.Series[0].UnitMixed {
		t.Errorf("unit %q mixed=%v", u, res.Series[0].UnitMixed)
	}
	// A budget below the case size gives a partial answer with exactly that many events.
	part, err := casestore.Series(ctx, c, casestore.SeriesQuery{
		From: testBase, To: testBase.Add((n - 1) * time.Second), BucketMs: 100_000,
		Sel: []string{"kind=cpu"}, MaxScan: 10_000,
	})
	// The store filters by kind, so the budget counts the cpu events it reads.
	if err != nil || !part.Partial || part.Series[0].Count != 10_000 {
		t.Fatalf("budget: partial=%v count=%d err=%v", part.Partial, part.Series[0].Count, err)
	}
}

// An erase that arrives in the middle of a long scan stops it: the scan ends
// with ErrNotFound after a bounded number of further events, not at the end of
// the case.
func TestEraseStopsALongScan(t *testing.T) {
	const n = 20_000
	s, _ := newTestStore(t)
	c := bigCase(t, s, n)
	ctx := context.Background()
	delivered := 0
	var once sync.Once
	erased := make(chan error, 1)
	err := c.Each(ctx, casestore.EventQuery{}, func(evidence.Event) error {
		delivered++
		once.Do(func() {
			go func() { erased <- s.Erase(ctx, c.Info().ID) }()
			time.Sleep(150 * time.Millisecond) // Erase closes the stop channel before it waits for the database
		})
		return nil
	})
	if !errors.Is(err, casestore.ErrNotFound) {
		t.Fatalf("Each across an erase: %v after %d events, want ErrNotFound", err, delivered)
	}
	if delivered >= n {
		t.Errorf("the scan read all %d events although the case was erased", delivered)
	}
	select {
	case err := <-erased:
		if err != nil {
			t.Errorf("Erase: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Erase did not finish")
	}
	if err := c.Each(ctx, casestore.EventQuery{}, func(evidence.Event) error { return nil }); !errors.Is(err, casestore.ErrNotFound) {
		t.Errorf("Each after erase: %v", err)
	}
}

// A scan stopped by its time budget returns ErrScanBudget, not the context error
// that the stop used.
func TestEachTimeBudgetIsReportedAsABudget(t *testing.T) {
	s, _ := newTestStore(t)
	c := bigCase(t, s, 3000)
	delivered := 0
	err := c.Each(context.Background(), casestore.EventQuery{MaxScanTime: 20 * time.Millisecond}, func(evidence.Event) error {
		delivered++
		time.Sleep(time.Millisecond)
		return nil
	})
	if !errors.Is(err, casestore.ErrScanBudget) || delivered == 0 || delivered >= 3000 {
		t.Fatalf("%d events, %v", delivered, err)
	}
	// The caller's own cancellation stays a cancellation.
	ctx, cancel := context.WithCancel(context.Background())
	err = c.Each(ctx, casestore.EventQuery{}, func(evidence.Event) error { cancel(); return nil })
	if !errors.Is(err, context.Canceled) {
		t.Errorf("caller cancel: %v", err)
	}
}

func TestSuppliedSurvivesReopenAndIsMirroredInTheManifest(t *testing.T) {
	ctx := context.Background()
	var mu sync.Mutex
	var logs []string
	s, root := newTestStore(t, func(o *Options) {
		o.Logf = func(format string, args ...any) {
			mu.Lock()
			defer mu.Unlock()
			logs = append(logs, format+" "+strings.Join(stringify(args), " "))
		}
	})
	info := mustCreate(t, s, "supplied")
	c := mustOpen(t, s, info.ID)
	const secret = "SENTINEL-7f3a"
	m := tMeta("a.csv")
	m.Supplied = map[string]string{"slowms": "250", "pattern": "%d " + secret + " %m"}
	if _, err := c.Import(ctx, m, tBody("a"), tRun(tEvents(c, "a", 4))); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Import(ctx, tMeta("b.csv"), tBody("b"), tRun(tEvents(c, "b", 4))); err != nil {
		t.Fatal(err)
	}
	_ = c.Close()

	man, err := readManifest(filepath.Join(root, string(info.ID)), s.opt.Protector)
	if err != nil || len(man.Imports) != 2 {
		t.Fatalf("manifest: %+v, %v", man, err)
	}
	var fromManifest map[string]string
	if err := json.Unmarshal([]byte(man.Imports[0].Supplied), &fromManifest); err != nil || fromManifest["slowms"] != "250" {
		t.Errorf("manifest supplied %q, %v", man.Imports[0].Supplied, err)
	}
	if man.Imports[0].Supplied != `{"pattern":"%d `+secret+` %m","slowms":"250"}` {
		t.Errorf("manifest form is not canonical: %q", man.Imports[0].Supplied)
	}
	if man.Imports[1].Supplied != "" {
		t.Errorf("an import without settings writes %q", man.Imports[1].Supplied)
	}

	s2 := newStoreAt(t, root, s.opt.Protector)
	c2 := mustOpen(t, s2, info.ID)
	recs, err := c2.Imports(ctx)
	if err != nil || len(recs) != 2 {
		t.Fatalf("after reopen: %d, %v", len(recs), err)
	}
	if recs[0].Supplied["slowms"] != "250" || recs[0].Supplied["pattern"] != "%d "+secret+" %m" || len(recs[1].Supplied) != 0 {
		t.Errorf("records after reopen: %+v / %+v", recs[0].Supplied, recs[1].Supplied)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, l := range logs {
		if strings.Contains(l, secret) || strings.Contains(l, "250") || strings.Contains(l, "slowms") {
			t.Errorf("a log line carries a supplied setting: %q", l)
		}
	}
	if len(logs) == 0 {
		t.Error("no log lines captured; the check above proved nothing")
	}
}

func stringify(args []any) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = strings.TrimSpace(strings.ReplaceAll(toString(a), "\n", " "))
	}
	return out
}

func toString(a any) string {
	b, _ := json.Marshal(a)
	return string(b)
}

// A manifest or database written before supplied settings existed has no
// settings rows; such a case opens, lists and imports as before.
func TestCaseWithoutSuppliedRowsStillOpens(t *testing.T) {
	ctx := context.Background()
	s, root := newTestStore(t)
	info := mustCreate(t, s, "old")
	c := mustOpen(t, s, info.ID)
	mustImport(t, c, "a", 3)
	var rows int
	if err := c.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM meta WHERE k LIKE 'import.%.supplied'").Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("an import without settings stores %d settings rows, %v", rows, err)
	}
	_ = c.Close()
	man, err := readManifest(filepath.Join(root, string(info.ID)), s.opt.Protector)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(mustJSON(t, man), "supplied") {
		t.Errorf("manifest of an import without settings mentions supplied: %s", mustJSON(t, man))
	}
	s2 := newStoreAt(t, root, s.opt.Protector)
	c2 := mustOpen(t, s2, info.ID)
	recs, err := c2.Imports(ctx)
	if err != nil || len(recs) != 1 || len(recs[0].Supplied) != 0 {
		t.Fatalf("%+v, %v", recs, err)
	}
	mustImport(t, c2, "b", 2)
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
