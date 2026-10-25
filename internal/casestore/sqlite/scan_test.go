package sqlite

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

// seriesEvents builds nCounter "counter" events over three counters and nMetric
// "metric" events, one second apart, with the attributes a series key carries.
func seriesEvents(c casestore.Case, label string, nCounter, nMetric int) []evidence.Event {
	var out []evidence.Event
	mk := func(i int, kind string, attrs map[string]string) evidence.Event {
		v := float64(i)
		line := int64(len(out) + 2)
		start := testBase.Add(time.Duration(i) * time.Second)
		return evidence.Event{
			EvidenceID: evidence.EventID(string(c.Info().ID), testImporter, label, line, 0),
			Source:     testImporter, Kind: kind, Host: "web-a", Start: start, End: start, Value: &v,
			Unit: "count", Line: line, TZRule: "utc-field", Attrs: attrs,
		}
	}
	for i := 0; i < nCounter; i++ {
		out = append(out, mk(i, "counter", map[string]string{
			"object": "Memory", "counter": "c" + strconv.Itoa(i%3), "instance": "i" + strconv.Itoa(i%2)}))
	}
	for i := 0; i < nMetric; i++ {
		out = append(out, mk(i, "metric", map[string]string{"metric": "m" + strconv.Itoa(i%2), "process": "w3wp", "pid": "10"}))
	}
	return out
}

func importSeries(t *testing.T, c *Case, label string, nCounter, nMetric int) {
	t.Helper()
	_, err := c.Import(context.Background(), tMeta(label+".csv"), tBody(label), tRun(seriesEvents(c, label, nCounter, nMetric)))
	if err != nil {
		t.Fatal(err)
	}
}

func eachIDs(t *testing.T, c *Case, q casestore.EventQuery) []string {
	t.Helper()
	var ids []string
	err := c.Each(context.Background(), q, func(ev evidence.Event) error {
		ids = append(ids, ev.EvidenceID)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return ids
}

func sorted(ids []string) []string {
	out := append([]string(nil), ids...)
	sort.Strings(out)
	return out
}

func sameSet(a, b []string) bool {
	a, b = sorted(a), sorted(b)
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func indexCount(t *testing.T, c *Case) int {
	t.Helper()
	var n int
	if err := c.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='index' AND name IN ('ev_kind_counter','ev_kind_metric')`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func planOf(t *testing.T, c *Case, where string, args []any) string {
	t.Helper()
	rows, err := c.db.Query("EXPLAIN QUERY PLAN "+selectEvents+where, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var a, b, cc int
		var d string
		if err := rows.Scan(&a, &b, &cc, &d); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, d)
	}
	return strings.Join(plan, "; ")
}

// A case written before the series-key indexes (schema 2) opens, is marked as
// the current schema, builds the indexes in the background (or when a query asks for a series key
// first), and answers exactly as before.
func TestVersionTwoCaseBuildsSeriesIndexesLazily(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t)
	info := mustCreate(t, s, "Old")
	c := mustOpen(t, s, info.ID)
	importSeries(t, c, "one", 60, 20)
	if indexCount(t, c) != 2 {
		t.Fatal("a new case lacks the series-key indexes")
	}
	counterQ := casestore.EventQuery{Kinds: []string{"counter"}, Attrs: map[string]string{"counter": "c1", "object": "Memory", "instance": "i1"}}
	metricQ := casestore.EventQuery{Kinds: []string{"metric"}, Attrs: map[string]string{"metric": "m1", "process": "w3wp", "pid": "10"}}
	wantCounter, wantMetric := eachIDs(t, c, counterQ), eachIDs(t, c, metricQ)
	if len(wantCounter) == 0 || len(wantMetric) == 0 {
		t.Fatalf("the fixture matches nothing: %d %d", len(wantCounter), len(wantMetric))
	}
	for _, stmt := range []string{
		"DROP INDEX ev_kind_counter", "DROP INDEX ev_kind_metric",
		"UPDATE meta SET v='2' WHERE k='schema_version'",
	} {
		if _, err := c.db.ExecContext(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}

	c = mustOpen(t, s, info.ID)
	defer c.Close()
	if v := schemaOf(t, c); v != strconv.Itoa(schemaVersion) {
		t.Fatalf("schema version after migration = %s", v)
	}
	// The open starts the build in the background; a series query that arrives
	// meanwhile waits for it, and either way the indexes end up built.
	if got := eachIDs(t, c, counterQ); !sameSet(got, wantCounter) {
		t.Fatalf("counter series after migration: %d events, want %d", len(got), len(wantCounter))
	}
	deadline := time.Now().Add(10 * time.Second)
	for indexCount(t, c) != 2 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if n := indexCount(t, c); n != 2 {
		t.Fatalf("after open and a series query there are %d indexes, want 2", n)
	}
	if got := eachIDs(t, c, casestore.EventQuery{Kinds: []string{"counter"}}); len(got) != 60 {
		t.Fatalf("plain query returned %d events", len(got))
	}
	if got := eachIDs(t, c, metricQ); !sameSet(got, wantMetric) {
		t.Fatalf("metric series after migration: %d events, want %d", len(got), len(wantMetric))
	}
	// A new import still works on the migrated case, and a second open keeps it.
	importSeries(t, c, "two", 6, 2)
	if got := eachIDs(t, c, casestore.EventQuery{Kinds: []string{"counter"}}); len(got) != 66 {
		t.Fatalf("after a second import: %d counter events", len(got))
	}
}

// An unordered query that matches much of the case reads the table in storage
// order; one that matches little, or filters on a source, host or attribute,
// keeps the index. Either way it returns the events the ordered query returns.
func TestUnorderedScanChoiceAndResults(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t)
	c := mustOpen(t, s, mustCreate(t, s, "Scan").ID)
	defer c.Close()
	importSeries(t, c, "one", 90, 10)
	db, err := c.handle()
	if err != nil {
		t.Fatal(err)
	}

	big := casestore.EventQuery{Kinds: []string{"counter"}, Unordered: true}
	if !c.preferScan(ctx, db, big) {
		t.Fatal("a query for 90% of the case should read the table in storage order")
	}
	for name, q := range map[string]casestore.EventQuery{
		"ordered":   {Kinds: []string{"counter"}},
		"rare kind": {Kinds: []string{"metric"}, Unordered: true},
		"no kind":   {Unordered: true},
		"source":    {Kinds: []string{"counter"}, Sources: []string{testImporter}, Unordered: true},
		"host":      {Kinds: []string{"counter"}, Hosts: []string{"web-a"}, Unordered: true},
		"attrs":     {Kinds: []string{"counter"}, Attrs: map[string]string{"counter": "c0"}, Unordered: true},
		"narrow window": {Kinds: []string{"counter"}, Unordered: true,
			Window: importer.Window{From: testBase, To: testBase.Add(2 * time.Second)}}, // 6 of 100 events
	} {
		if c.preferScan(ctx, db, q) {
			t.Errorf("%s: preferScan = true, want false", name)
		}
	}

	// The plans: the plus-prefixed filter scans the table, the plain one uses an index.
	where, args, err := eventFilter(casestore.EventQuery{Kinds: []string{"counter"}})
	if err != nil {
		t.Fatal(err)
	}
	if p := planOf(t, c, where, args); !strings.Contains(p, "USING") {
		t.Errorf("ordered plan does not use an index: %s", p)
	}
	where, args, err = eventFilterAs(casestore.EventQuery{Kinds: []string{"counter"}}, true)
	if err != nil {
		t.Fatal(err)
	}
	if p := planOf(t, c, where, args); strings.Contains(p, "USING") || !strings.Contains(p, "SCAN") {
		t.Errorf("scan plan is not a plain table scan: %s", p)
	}

	// Results.
	for name, q := range map[string]casestore.EventQuery{
		"counters": {Kinds: []string{"counter"}},
		"both":     {Kinds: []string{"counter", "metric"}},
		"window":   {Kinds: []string{"counter", "metric"}, Window: importer.Window{From: testBase.Add(10 * time.Second), To: testBase.Add(50 * time.Second)}},
	} {
		ordered := eachIDs(t, c, q)
		q.Unordered = true
		unordered := eachIDs(t, c, q)
		if len(ordered) == 0 || !sameSet(ordered, unordered) {
			t.Errorf("%s: unordered query returned %d events, ordered %d", name, len(unordered), len(ordered))
		}
	}

	// The event counts follow an import.
	importSeries(t, c, "two", 0, 400)
	if c.preferScan(ctx, db, casestore.EventQuery{Kinds: []string{"counter"}, Unordered: true}) {
		t.Error("after the case grew to mostly metrics, a counter query still reads the table")
	}
	if !c.preferScan(ctx, db, casestore.EventQuery{Kinds: []string{"metric"}, Unordered: true}) {
		t.Error("after the import, the metric query should read the table")
	}
}

// goSideOnly hides the attribute filter and the delivery order from the store, so
// Series reads every event of the kind and matches the selector in Go, as it did
// before the filter was pushed into SQL.
type goSideOnly struct{ *Case }

func (g goSideOnly) Each(ctx context.Context, q casestore.EventQuery, fn func(evidence.Event) error) error {
	q.Attrs, q.Unordered = nil, false
	return g.Case.Each(ctx, q, fn)
}

// Series answers the same with the selector pushed into SQL as with the match
// made in Go over the whole kind, on a mid-size case, and reads fewer events.
func TestSeriesWithPushedSelectorsMatchesGoSideMatching(t *testing.T) {
	const samples = 400
	s, _ := newTestStore(t)
	c := mustOpen(t, s, mustCreate(t, s, "Equiv").ID)
	defer c.Close()
	var evs []evidence.Event
	mk := func(kind string, i int, v float64, attrs map[string]string) {
		line := int64(len(evs) + 2)
		start := testBase.Add(time.Duration(i) * 15 * time.Second)
		evs = append(evs, evidence.Event{
			EvidenceID: evidence.EventID(string(c.Info().ID), testImporter, "eq", line, 0),
			Source:     testImporter, Kind: kind, Host: "web-" + attrs["host"], Start: start, End: start, Value: &v,
			Unit: "count", Line: line, TZRule: "utc-field", Attrs: attrs,
		})
	}
	for i := 0; i < samples; i++ {
		for ctr := 0; ctr < 8; ctr++ {
			for inst := 0; inst < 3; inst++ {
				mk("counter", i, float64(i*(ctr+1)+inst), map[string]string{"object": "Obj" + strconv.Itoa(ctr%2), "counter": "c" + strconv.Itoa(ctr), "instance": "i" + strconv.Itoa(inst), "host": "a"})
			}
		}
		for m := 0; m < 4; m++ {
			mk("metric", i, float64(i+m), map[string]string{"metric": "m" + strconv.Itoa(m), "process": "w3wp", "pid": strconv.Itoa(100 + m%2), "host": "a"})
		}
	}
	if _, err := c.Import(context.Background(), tMeta("eq.csv"), tBody("eq"), tRun(evs)); err != nil {
		t.Fatal(err)
	}
	q := casestore.SeriesQuery{
		From: testBase, To: testBase.Add(samples * 15 * time.Second), BucketMs: 600_000,
		Sel: []string{
			"kind=counter;attr.object=Obj0;attr.counter=c0;attr.instance=i0",
			"kind=counter;attr.object=Obj1;attr.counter=c3;attr.instance=i2",
			"kind=counter;attr.counter=c5",
			"kind=counter;attr.instance=i1", // not a series key: shared pass
			"kind=counter;host=web-a;attr.counter=c7;field=value",
			"kind=metric;attr.metric=m2;attr.process=w3wp;attr.pid=100",
			"kind=metric;attr.metric=m3",
			"kind=counter;attr.counter=nope",
		},
	}
	ctx := context.Background()
	pushed, err := casestore.Series(ctx, c, q)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := casestore.Series(ctx, goSideOnly{c}, q)
	if err != nil {
		t.Fatal(err)
	}
	if pushed.Partial || plain.Partial {
		t.Fatalf("partial: %v %v", pushed.Partial, plain.Partial)
	}
	var seen int64
	for _, s := range pushed.Series {
		seen += s.Count
	}
	if seen == 0 || pushed.Series[0].Count != samples {
		t.Fatalf("the fixture is not exercised: %d counted, first series %d", seen, pushed.Series[0].Count)
	}
	if pushed.Scanned >= plain.Scanned {
		t.Errorf("the pushed selectors read %d events, the plain scan %d", pushed.Scanned, plain.Scanned)
	}
	pushed.Scanned, plain.Scanned = 0, 0
	if !reflect.DeepEqual(pushed, plain) {
		pj, _ := json.Marshal(pushed)
		gj, _ := json.Marshal(plain)
		t.Fatalf("results differ:\n pushed %.600s\n plain  %.600s", pj, gj)
	}
}

// The ordered path still delivers kind, then start time.
func TestOrderedEachIsByKindThenTime(t *testing.T) {
	s, _ := newTestStore(t)
	c := mustOpen(t, s, mustCreate(t, s, "Order").ID)
	defer c.Close()
	importSeries(t, c, "one", 30, 30)
	var prevKind string
	var prev time.Time
	err := c.Each(context.Background(), casestore.EventQuery{Kinds: []string{"counter", "metric"}}, func(ev evidence.Event) error {
		if ev.Kind < prevKind || (ev.Kind == prevKind && ev.Start.Before(prev)) {
			t.Fatalf("out of order: %s %s after %s %s", ev.Kind, ev.Start, prevKind, prev)
		}
		prevKind, prev = ev.Kind, ev.Start
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
