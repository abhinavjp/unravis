package sqlite

// The timeline indexes (ev_src_ts, ev_err_ts): a new case has them, an older case
// gets them once in the background without slowing the open, the timeline queries
// use them, and the counts do not change with or without them.

import (
	"context"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

func indexNames(t *testing.T, c *Case) []string {
	t.Helper()
	rows, err := c.db.Query(`SELECT name FROM sqlite_master WHERE type='index' AND name IN ('ev_src_ts','ev_err_ts') ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		out = append(out, n)
	}
	return out
}

func dropTimelineIndexes(t *testing.T, c *Case) {
	t.Helper()
	for _, n := range timelineIndexNames {
		if _, err := c.db.Exec("DROP INDEX IF EXISTS " + n); err != nil {
			t.Fatal(err)
		}
	}
}

func waitIndexes(t *testing.T, c *Case, want int) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if len(indexNames(t, c)) == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("indexes = %v, want %d", indexNames(t, c), want)
}

// importStatuses imports n events for source src, a 503 on every third one.
func importStatuses(t *testing.T, c *Case, src string, n int) {
	t.Helper()
	evs := tEvents(c, src, n)
	for i := range evs {
		evs[i].Source = src
		evs[i].EvidenceID = evidence.EventID(string(c.Info().ID), src, src, evs[i].Line, 0)
		st := []string{"200", "404", "503"}[i%3]
		evs[i].Attrs = map[string]string{"status": st}
	}
	meta := tMeta(src + ".csv")
	meta.Importer = src
	if _, err := c.Import(context.Background(), meta, tBody(src), tRun(evs)); err != nil {
		t.Fatal(err)
	}
}

func TestNewCaseHasTimelineIndexes(t *testing.T) {
	s, _ := newTestStore(t)
	c := mustOpen(t, s, mustCreate(t, s, "new").ID)
	if got := indexNames(t, c); len(got) != 0 {
		t.Fatalf("an empty case has %v before its first import", got)
	}
	importStatuses(t, c, "imp-a", 12)
	if got := indexNames(t, c); !reflect.DeepEqual(got, []string{"ev_err_ts", "ev_src_ts"}) {
		t.Fatalf("indexes after the first import = %v", got)
	}
	// An empty case that is reopened does not build anything either.
	c2 := mustOpen(t, s, mustCreate(t, s, "empty").ID)
	time.Sleep(50 * time.Millisecond)
	if got := indexNames(t, c2); len(got) != 0 {
		t.Fatalf("an empty reopened case built %v", got)
	}
}

func TestOlderCaseGetsTimelineIndexesOnce(t *testing.T) {
	s, _ := newTestStore(t)
	info := mustCreate(t, s, "old")
	c := mustOpen(t, s, info.ID)
	importStatuses(t, c, "imp-a", 30)
	dropTimelineIndexes(t, c)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}

	c = mustOpen(t, s, info.ID)
	waitIndexes(t, c, 2)
	// The schema version is unchanged: no migration is involved.
	if v := schemaOf(t, c); v != "4" {
		t.Fatalf("schema = %s", v)
	}
	objs := objects(t, c)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopen: nothing is missing, so nothing is built and the schema is identical.
	c = mustOpen(t, s, info.ID)
	if timelineIndexesMissing(context.Background(), c.db) {
		t.Fatal("indexes reported missing after the build")
	}
	time.Sleep(50 * time.Millisecond)
	if !reflect.DeepEqual(objs, objects(t, c)) {
		t.Fatal("reopening changed the schema")
	}
	if n := scalar(t, c, "SELECT COUNT(*) FROM events"); n != 30 {
		t.Fatalf("%d events", n)
	}
}

// Opening a case without the indexes does not wait for the build, and the
// queries work (and agree) while it is held up.
func TestTimelineIndexBuildDoesNotBlockOpen(t *testing.T) {
	s, _ := newTestStore(t)
	info := mustCreate(t, s, "slow")
	c := mustOpen(t, s, info.ID)
	importStatuses(t, c, "imp-a", 60)
	ctx := context.Background()
	q := casestore.BucketQuery{
		AxisStart: testBase, From: testBase, To: testBase.Add(60 * time.Second), WidthMs: 10_000,
		Sources: []string{"imp-a"}, CountErrors: true,
	}
	withIdx, err := c.BucketCounts(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	dropTimelineIndexes(t, c)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}

	entered, release := make(chan struct{}), make(chan struct{})
	timelineBuildHook = func() {
		close(entered)
		<-release
	}
	t.Cleanup(func() { timelineBuildHook = nil })

	opened := make(chan *Case, 1)
	go func() { opened <- mustOpen(t, s, info.ID) }()
	var c2 *Case
	select {
	case c2 = <-opened:
	case <-time.After(10 * time.Second):
		close(release)
		t.Fatal("Open waited for the index build")
	}
	released := false
	releaseOnce := func() {
		if !released {
			released = true
			close(release)
		}
	}
	t.Cleanup(releaseOnce)
	<-entered // the build is running (held)
	if got := indexNames(t, c2); len(got) != 0 {
		t.Fatalf("indexes exist while the build is held: %v", got)
	}
	// Queries work without the indexes and answer the same.
	noIdx, err := c2.BucketCounts(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(withIdx.Counts, noIdx.Counts) {
		t.Fatalf("counts differ without the indexes:\n%+v\n%+v", withIdx.Counts, noIdx.Counts)
	}
	if evs, err := c2.Events(ctx, casestore.EventQuery{Sources: []string{"imp-a"}}); err != nil || len(evs) != 60 {
		t.Fatalf("Events without the indexes: %d %v", len(evs), err)
	}
	releaseOnce()
	waitIndexes(t, c2, 2)
	again, err := c2.BucketCounts(ctx, q)
	if err != nil || !reflect.DeepEqual(withIdx.Counts, again.Counts) {
		t.Fatalf("counts after the build: %v %+v", err, again.Counts)
	}
}

// tlPlanOf returns the EXPLAIN QUERY PLAN detail text for a query.
func tlPlanOf(t *testing.T, c *Case, query string, args []any) string {
	t.Helper()
	rows, err := c.db.Query("EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatalf("explain: %v\n%s", err, query)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatal(err)
		}
		out = append(out, detail)
	}
	return strings.Join(out, " | ")
}

// The timeline queries must plan on the new indexes (the wording of the plan is
// SQLite's; only the index name is asserted).
func TestTimelineQueriesUseTheIndexes(t *testing.T) {
	s, _ := newTestStore(t)
	c := mustOpen(t, s, mustCreate(t, s, "plans").ID)
	importStatuses(t, c, "imp-a", 90)
	importStatuses(t, c, "imp-b", 30)
	var seen []struct {
		query string
		args  []any
	}
	queryHook = func(query string, args []any) {
		seen = append(seen, struct {
			query string
			args  []any
		}{query, append([]any(nil), args...)})
	}
	t.Cleanup(func() { queryHook = nil })

	ctx := context.Background()
	if _, err := c.Events(ctx, casestore.EventQuery{
		Window:  importer.Window{From: testBase, To: testBase.Add(30 * time.Second)},
		Sources: []string{"imp-a", "imp-b"}, Limit: 1000,
	}); err != nil {
		t.Fatal(err)
	}
	// Several sources: one key query per source, each on ev_src_ts and answered
	// from the index alone, then the events are read by evidence ID.
	if len(seen) != 2 {
		t.Fatalf("Events ran %d queries, want one key query per source", len(seen))
	}
	for _, s := range seen {
		p := tlPlanOf(t, c, s.query, s.args)
		if !strings.Contains(p, "ev_src_ts") || !strings.Contains(strings.ToUpper(p), "COVERING INDEX") {
			t.Errorf("Events key plan is not the covering ev_src_ts: %s", p)
		}
	}
	// One source: the row query itself plans on ev_src_ts.
	seen = nil
	if _, err := c.Events(ctx, casestore.EventQuery{
		Window:  importer.Window{From: testBase, To: testBase.Add(30 * time.Second)},
		Sources: []string{"imp-a"}, Limit: 1000,
	}); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 1 {
		t.Fatalf("one-source Events ran %d queries", len(seen))
	}
	if p := tlPlanOf(t, c, seen[0].query, seen[0].args); !strings.Contains(p, "ev_src_ts") {
		t.Errorf("one-source Events plan does not use ev_src_ts: %s", p)
	}

	seen = nil
	if _, err := c.BucketCounts(ctx, casestore.BucketQuery{
		AxisStart: testBase, From: testBase, To: testBase.Add(90 * time.Second), WidthMs: 10_000,
		Sources: []string{"imp-a", "imp-b"}, CountErrors: true, ErrorExcludeSources: []string{"imp-b"},
	}); err != nil {
		t.Fatal(err)
	}
	if len(seen) < 2 {
		t.Fatalf("BucketCounts ran %d queries, want a count and an error query per chunk", len(seen))
	}
	countPlan, errPlan := tlPlanOf(t, c, seen[0].query, seen[0].args), tlPlanOf(t, c, seen[1].query, seen[1].args)
	if !strings.Contains(countPlan, "ev_src_ts") {
		t.Errorf("count plan does not use ev_src_ts: %s", countPlan)
	}
	if !strings.Contains(errPlan, "ev_err_ts") {
		t.Errorf("error plan does not use ev_err_ts: %s", errPlan)
	}
	// The count is answered from its index alone (no row lookups). The error query
	// reads only the partial index entries (the 5xx rows), not the whole range.
	if !strings.Contains(strings.ToUpper(countPlan), "COVERING INDEX") {
		t.Errorf("count plan is not covering: %s", countPlan)
	}
}

// The error counts are the same with the indexes, without them, and by the scan
// fallback, over statuses that are and are not 5xx and an excluded source.
func TestBucketErrorsSameWithAndWithoutIndexes(t *testing.T) {
	s, _ := newTestStore(t)
	c := mustOpen(t, s, mustCreate(t, s, "same").ID)
	importStatuses(t, c, "imp-a", 200)
	importStatuses(t, c, "imp-mark", 50)
	ctx := context.Background()
	q := casestore.BucketQuery{
		AxisStart: testBase, From: testBase.Add(5 * time.Second), To: testBase.Add(190 * time.Second), WidthMs: 7_000,
		Sources: []string{"imp-a", "imp-mark"}, CountErrors: true, ErrorExcludeSources: []string{"imp-mark"},
	}
	with, err := c.BucketCounts(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	dropTimelineIndexes(t, c)
	without, err := c.BucketCounts(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	scan, err := casestore.CountBucketsScan(ctx, c, q)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(with.Counts, without.Counts) {
		t.Fatalf("with vs without indexes:\n%+v\n%+v", with.Counts, without.Counts)
	}
	var errs, markErrs int64
	for i, bc := range with.Counts {
		errs += bc.Errors
		if bc.Source == "imp-mark" {
			markErrs += bc.Errors
		}
		sc := scan.Counts[i]
		if bc.Index != sc.Index || bc.Source != sc.Source || bc.Count != sc.Count || bc.Errors != sc.Errors {
			t.Fatalf("cell %+v differs from scan %+v", bc, sc)
		}
	}
	if errs == 0 || markErrs != 0 || len(with.Counts) != len(scan.Counts) {
		t.Fatalf("errors %d, marker errors %d, cells %d vs %d", errs, markErrs, len(with.Counts), len(scan.Counts))
	}
}

// Events over several sources returns exactly what the single ordered query
// returns: the same events, in start-time then evidence-ID order (ties across
// sources included), cut at the limit, with or without the indexes.
func TestEventsSeveralSourcesSameAsSingleQuery(t *testing.T) {
	s, _ := newTestStore(t)
	c := mustOpen(t, s, mustCreate(t, s, "merge").ID)
	importStatuses(t, c, "imp-a", 70)
	importStatuses(t, c, "imp-b", 55)
	importStatuses(t, c, "imp-c", 20)
	ctx := context.Background()

	ids := func(evs []evidence.Event) []string {
		out := make([]string, len(evs))
		for i, e := range evs {
			out[i] = e.EvidenceID
		}
		return out
	}
	want := func(from, to time.Time, limit int, sources ...string) []string {
		args := []any{from.UnixMilli(), to.UnixMilli()}
		in := make([]string, len(sources))
		for i, s := range sources {
			in[i] = "?"
			args = append(args, s)
		}
		rows, err := c.db.Query(`SELECT evidence_id FROM events WHERE ts_ms >= ? AND ts_ms <= ? AND source IN (`+strings.Join(in, ",")+`) ORDER BY ts_ms, evidence_id LIMIT `+strconv.Itoa(limit), args...)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			out = append(out, id)
		}
		return out
	}
	run := func(label string) {
		for _, tc := range []struct {
			from, to time.Time
			limit    int
			sources  []string
		}{
			{testBase, testBase.Add(200 * time.Second), 1000, []string{"imp-a", "imp-b"}},
			{testBase, testBase.Add(200 * time.Second), 37, []string{"imp-a", "imp-b", "imp-c"}},
			{testBase.Add(10 * time.Second), testBase.Add(40 * time.Second), 1000, []string{"imp-c", "imp-a"}},
			{testBase, testBase.Add(200 * time.Second), 1000, []string{"imp-a", "imp-a", "imp-b"}},
			{testBase, testBase.Add(200 * time.Second), 1000, []string{"imp-a", "imp-none"}},
		} {
			got, err := c.Events(ctx, casestore.EventQuery{
				Window: importer.Window{From: tc.from, To: tc.to}, Sources: tc.sources, Limit: tc.limit,
			})
			if err != nil {
				t.Fatal(err)
			}
			uniq := map[string]bool{}
			var srcs []string
			for _, s := range tc.sources {
				if !uniq[s] {
					uniq[s] = true
					srcs = append(srcs, s)
				}
			}
			if w := want(tc.from, tc.to, tc.limit, srcs...); !reflect.DeepEqual(ids(got), w) {
				t.Fatalf("%s %v limit %d: got %d events, want %d (order or content differs)", label, tc.sources, tc.limit, len(got), len(w))
			}
		}
	}
	run("with indexes")
	dropTimelineIndexes(t, c)
	run("without indexes")
}

// A request opens a case and closes it again in a moment; the background build of
// a large old case takes far longer. The build holds the case open on its own, so
// closing the opener's handle does not cancel it.
func TestTimelineIndexBuildSurvivesTheOpenersClose(t *testing.T) {
	s, _ := newTestStore(t)
	info := mustCreate(t, s, "survive")
	c := mustOpen(t, s, info.ID)
	importStatuses(t, c, "imp-a", 40)
	dropTimelineIndexes(t, c)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}

	entered, release := make(chan struct{}), make(chan struct{})
	timelineBuildHook = func() {
		close(entered)
		<-release
	}
	t.Cleanup(func() { timelineBuildHook = nil })

	c2 := mustOpen(t, s, info.ID)
	<-entered
	if err := c2.Close(); err != nil { // the request ends while the build is still waiting
		t.Fatal(err)
	}
	close(release)

	deadline := time.Now().Add(15 * time.Second)
	for {
		c3 := mustOpen(t, s, info.ID)
		n := len(indexNames(t, c3))
		_ = c3.Close()
		if n == 2 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the build did not finish after the opener closed: %d indexes", n)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
