package sqlite

import (
	"context"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/evidence"
)

// mustImportAs is mustImport under importer importerName instead of
// testImporter, so a test can tell two importers' events apart.
func mustImportAs(t *testing.T, c *Case, importerName, label string, n int) casestore.ImportResult {
	t.Helper()
	evs := tEvents(c, label, n)
	for i := range evs {
		evs[i].Source = importerName
		evs[i].EvidenceID = evidence.EventID(string(c.Info().ID), importerName, label, evs[i].Line, 0)
	}
	meta := tMeta(label + ".csv")
	meta.Importer = importerName
	res, err := c.Import(context.Background(), meta, tBody(label), tRun(evs))
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// A case larger than the 50 000-row cap of Events still gives exact totals,
// per cell and per importer, with ImportID naming the right import.
func TestBucketCountsExactPastTheCap(t *testing.T) {
	if testing.Short() {
		t.Skip("imports 60 001 events")
	}
	const n1, n2 = 30_001, 30_000
	s, _ := newTestStore(t)
	info := mustCreate(t, s, "big-buckets")
	c := mustOpen(t, s, info.ID)
	resA := mustImportAs(t, c, "imp-a", "a", n1)
	resB := mustImportAs(t, c, "imp-b", "b", n2)

	ctx := context.Background()
	const widthMs = 6_000_000 // 6 000 s cells: 6 of them span the 30 001 s range of imp-a
	q := casestore.BucketQuery{
		AxisStart: testBase, From: testBase, To: testBase.Add(36_000 * time.Second),
		WidthMs: widthMs, Sources: []string{"imp-a", "imp-b"},
	}
	if err := q.Check(); err != nil {
		t.Fatal(err)
	}

	res, err := c.BucketCounts(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != n1+n2 {
		t.Fatalf("total = %d, want %d", res.Total, n1+n2)
	}
	if res.Partial {
		t.Error("Partial = true, want false")
	}

	// Each import's events are one per second from testBase, so the count of a
	// cell is however many of the import's 0..n-1 seconds fall in it.
	cellCounts := func(n int) map[int]int64 {
		out := map[int]int64{}
		for i := 0; i < n; i++ {
			out[i*1000/widthMs]++
		}
		return out
	}
	wantA, wantB := cellCounts(n1), cellCounts(n2)
	srcOf := map[int64]string{resA.Record.ID: "imp-a", resB.Record.ID: "imp-b"}
	got := map[string]map[int]int64{"imp-a": {}, "imp-b": {}}
	for _, bc := range res.Counts {
		src, ok := srcOf[bc.ImportID]
		if !ok || src != bc.Source {
			t.Fatalf("bucket %+v: ImportID does not match its Source", bc)
		}
		got[bc.Source][bc.Index] = bc.Count
	}
	if !reflect.DeepEqual(got["imp-a"], wantA) {
		t.Errorf("imp-a cells = %v, want %v", got["imp-a"], wantA)
	}
	if !reflect.DeepEqual(got["imp-b"], wantB) {
		t.Errorf("imp-b cells = %v, want %v", got["imp-b"], wantB)
	}
}

// BucketCounts (the GROUP BY) and CountBucketsScan (the Each fallback) agree
// on a small mixed case, aside from ImportID, which the scan cannot fill in.
func TestBucketCountsMatchesScan(t *testing.T) {
	s, _ := newTestStore(t)
	info := mustCreate(t, s, "mixed-buckets")
	c := mustOpen(t, s, info.ID)
	mustImportAs(t, c, "imp-a", "a", 37)
	mustImportAs(t, c, "imp-b", "b", 23)

	ctx := context.Background()
	q := casestore.BucketQuery{
		AxisStart: testBase, From: testBase, To: testBase.Add(60 * time.Second), WidthMs: 10_000,
		Sources: []string{"imp-a", "imp-b"},
	}
	got, err := c.BucketCounts(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	scan, err := casestore.CountBucketsScan(ctx, c, q)
	if err != nil {
		t.Fatal(err)
	}
	strip := func(cs []casestore.BucketCount) []casestore.BucketCount {
		out := make([]casestore.BucketCount, len(cs))
		for i, bc := range cs {
			bc.ImportID = 0
			out[i] = bc
		}
		return out
	}
	if !reflect.DeepEqual(strip(got.Counts), strip(scan.Counts)) {
		t.Fatalf("BucketCounts = %+v, CountBucketsScan = %+v", got.Counts, scan.Counts)
	}
	if got.Total != scan.Total || got.Total != 60 {
		t.Fatalf("total = %d (scan %d), want 60", got.Total, scan.Total)
	}
}

// With CountErrors the GROUP BY and the scan fallback agree on Errors too: only
// a status of exactly three digits starting with 5 counts, and without
// CountErrors every Errors is 0.
func TestBucketCountsErrorsMatchScan(t *testing.T) {
	s, _ := newTestStore(t)
	info := mustCreate(t, s, "err-buckets")
	c := mustOpen(t, s, info.ID)
	evs := tEvents(c, "e", 40)
	statuses := []string{"200", "503", "404", "500", "", "5xx", "5000", "599", "-"}
	var wantErrors int64
	for i := range evs {
		evs[i].Source = "imp-e"
		evs[i].EvidenceID = evidence.EventID(string(c.Info().ID), "imp-e", "e", evs[i].Line, 0)
		st := statuses[i%len(statuses)]
		if st != "" {
			evs[i].Attrs = map[string]string{"status": st}
		} else {
			evs[i].Attrs = nil
		}
		if casestore.IsServerErrorStatus(st) {
			wantErrors++
		}
	}
	meta := tMeta("e.csv")
	meta.Importer = "imp-e"
	if _, err := c.Import(context.Background(), meta, tBody("e"), tRun(evs)); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	q := casestore.BucketQuery{
		AxisStart: testBase, From: testBase, To: testBase.Add(40 * time.Second), WidthMs: 10_000,
		Sources: []string{"imp-e"}, CountErrors: true,
	}
	got, err := c.BucketCounts(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	scan, err := casestore.CountBucketsScan(ctx, c, q)
	if err != nil {
		t.Fatal(err)
	}
	strip := func(cs []casestore.BucketCount) []casestore.BucketCount {
		out := make([]casestore.BucketCount, len(cs))
		for i, bc := range cs {
			bc.ImportID = 0
			out[i] = bc
		}
		return out
	}
	if !reflect.DeepEqual(strip(got.Counts), strip(scan.Counts)) {
		t.Fatalf("BucketCounts = %+v, CountBucketsScan = %+v", got.Counts, scan.Counts)
	}
	var sum int64
	for _, bc := range got.Counts {
		sum += bc.Errors
	}
	if sum != wantErrors || wantErrors == 0 {
		t.Fatalf("errors = %d, want %d", sum, wantErrors)
	}

	q.CountErrors = false
	off, err := c.BucketCounts(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	offScan, err := casestore.CountBucketsScan(ctx, c, q)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range [][]casestore.BucketCount{off.Counts, offScan.Counts} {
		for _, bc := range r {
			if bc.Errors != 0 {
				t.Fatalf("Errors = %d without CountErrors: %+v", bc.Errors, bc)
			}
		}
	}
}

// ErrorExcludeSources: the GROUP BY and the scan fallback agree, the excluded
// source keeps its Count but has zero Errors, and the other source still counts
// its 5xx rows.
func TestBucketCountsErrorExcludeMatchScan(t *testing.T) {
	s, _ := newTestStore(t)
	info := mustCreate(t, s, "err-exclude-buckets")
	c := mustOpen(t, s, info.ID)
	for _, src := range []string{"imp-req", "imp-mark"} {
		evs := tEvents(c, src, 20)
		for i := range evs {
			evs[i].Source = src
			evs[i].EvidenceID = evidence.EventID(string(c.Info().ID), src, src, evs[i].Line, 0)
			evs[i].Attrs = map[string]string{"status": []string{"503", "200"}[i%2]}
		}
		meta := tMeta(src + ".csv")
		meta.Importer = src
		if _, err := c.Import(context.Background(), meta, tBody(src), tRun(evs)); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	q := casestore.BucketQuery{
		AxisStart: testBase, From: testBase, To: testBase.Add(20 * time.Second), WidthMs: 10_000,
		Sources: []string{"imp-req", "imp-mark"}, CountErrors: true, ErrorExcludeSources: []string{"imp-mark"},
	}
	got, err := c.BucketCounts(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	scan, err := casestore.CountBucketsScan(ctx, c, q)
	if err != nil {
		t.Fatal(err)
	}
	var reqErrs, markErrs, markCount int64
	for i, bc := range got.Counts {
		sc := scan.Counts[i]
		if bc.Index != sc.Index || bc.Source != sc.Source || bc.Count != sc.Count || bc.Errors != sc.Errors {
			t.Fatalf("BucketCounts %+v differs from scan %+v", bc, sc)
		}
		if bc.Source == "imp-req" {
			reqErrs += bc.Errors
		} else {
			markErrs += bc.Errors
			markCount += bc.Count
		}
	}
	if len(got.Counts) != len(scan.Counts) || reqErrs != 10 || markErrs != 0 || markCount != 20 {
		t.Fatalf("req errors %d (want 10), marker errors %d (want 0), marker count %d (want 20), cells %d vs %d",
			reqErrs, markErrs, markCount, len(got.Counts), len(scan.Counts))
	}
}

// A Kinds filter and a Sources filter each drop the other rows; an unknown
// source gives an empty result, not an error.
func TestBucketCountsFilters(t *testing.T) {
	s, _ := newTestStore(t)
	info := mustCreate(t, s, "filtered-buckets")
	c := mustOpen(t, s, info.ID)
	mustImportAs(t, c, "imp-a", "a", 10) // 5 "cpu", 5 "wait"
	mustImportAs(t, c, "imp-b", "b", 10)

	ctx := context.Background()
	base := casestore.BucketQuery{AxisStart: testBase, From: testBase, To: testBase.Add(10 * time.Second), WidthMs: 10_000}

	q := base
	q.Sources = []string{"imp-a", "imp-b"}
	q.Kinds = []string{"cpu"}
	res, err := c.BucketCounts(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 10 {
		t.Errorf("kind filter total = %d, want 10", res.Total)
	}

	q = base
	q.Sources = []string{"imp-a"}
	res, err = c.BucketCounts(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 10 {
		t.Errorf("source filter total = %d, want 10", res.Total)
	}
	for _, bc := range res.Counts {
		if bc.Source != "imp-a" {
			t.Errorf("source filter let through %q", bc.Source)
		}
	}

	q = base
	q.Sources = []string{"no-such-importer"}
	res, err = c.BucketCounts(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Counts) != 0 || res.Total != 0 {
		t.Errorf("unknown source: counts=%+v total=%d, want empty", res.Counts, res.Total)
	}
}

// BucketCounts is a read: the case revision does not move.
func TestBucketCountsReadOnly(t *testing.T) {
	s, _ := newTestStore(t)
	info := mustCreate(t, s, "readonly-buckets")
	c := mustOpen(t, s, info.ID)
	mustImport(t, c, "ro", 5)

	ctx := context.Background()
	before, err := c.Revision(ctx)
	if err != nil {
		t.Fatal(err)
	}

	q := casestore.BucketQuery{
		AxisStart: testBase, From: testBase, To: testBase.Add(5 * time.Second), WidthMs: 5_000,
		Sources: []string{testImporter},
	}
	if _, err := c.BucketCounts(ctx, q); err != nil {
		t.Fatal(err)
	}

	after, err := c.Revision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Errorf("revision moved: %d -> %d", before, after)
	}
}

// A canceled context stops the query with context.Canceled, nothing else.
func TestBucketCountsCancel(t *testing.T) {
	s, _ := newTestStore(t)
	info := mustCreate(t, s, "cancel-buckets")
	c := mustOpen(t, s, info.ID)
	mustImport(t, c, "x", 1)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	q := casestore.BucketQuery{
		AxisStart: testBase, From: testBase, To: testBase.Add(time.Second), WidthMs: 1000,
		Sources: []string{testImporter},
	}
	if _, err := c.BucketCounts(ctx, q); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

// When the time budget passes, BucketCounts returns the cells it counted so
// far with Partial, TimedOut and DoneTo, not an error (PF-B-01).
func TestBucketCountsTimeoutIsPartial(t *testing.T) {
	s, _ := newTestStore(t)
	info := mustCreate(t, s, "timeout-buckets")
	c := mustOpen(t, s, info.ID)
	mustImportAs(t, c, "imp-a", "a", 20)

	// 20 one-second cells, one per chunk. The hook uses up the budget before chunk 3.
	bucketChunkHook = func(n int) {
		if n == 3 {
			time.Sleep(300 * time.Millisecond)
		}
	}
	t.Cleanup(func() { bucketChunkHook = nil })
	q := casestore.BucketQuery{
		AxisStart: testBase, From: testBase, To: testBase.Add(20 * time.Second), WidthMs: 1000,
		Sources: []string{"imp-a"}, MaxTime: 100 * time.Millisecond,
	}
	res, err := c.BucketCounts(context.Background(), q)
	if err != nil {
		t.Fatalf("a timeout must be a partial answer, got error %v", err)
	}
	if !res.Partial || !res.TimedOut {
		t.Fatalf("Partial %v TimedOut %v, want both true", res.Partial, res.TimedOut)
	}
	if want := testBase.Add(3 * time.Second); !res.DoneTo.Equal(want) {
		t.Errorf("DoneTo = %v, want %v", res.DoneTo, want)
	}
	if res.Total != 3 || len(res.Counts) != 3 {
		t.Errorf("total %d in %d cells, want the 3 cells before DoneTo", res.Total, len(res.Counts))
	}
	for i, bc := range res.Counts {
		if bc.Index != i {
			t.Errorf("cell %d has index %d", i, bc.Index)
		}
	}

	// The same query without a stall is complete and not partial.
	bucketChunkHook = nil
	q.MaxTime = 0
	full, err := c.BucketCounts(context.Background(), q)
	if err != nil || full.Partial || full.TimedOut || !full.DoneTo.IsZero() || full.Total != 20 {
		t.Fatalf("full = %+v, %v", full, err)
	}
}

// A window split into chunks gives the same counts as the whole GROUP BY, also
// when the window starts and ends inside a cell.
func TestBucketCountsChunksMatchWhole(t *testing.T) {
	s, _ := newTestStore(t)
	info := mustCreate(t, s, "chunk-buckets")
	c := mustOpen(t, s, info.ID)
	mustImportAs(t, c, "imp-a", "a", 500)

	ctx := context.Background()
	q := casestore.BucketQuery{
		AxisStart: testBase, From: testBase.Add(7*time.Second + 250*time.Millisecond), To: testBase.Add(490*time.Second + 10*time.Millisecond),
		WidthMs: 4000, Sources: []string{"imp-a"},
	}
	got, err := c.BucketCounts(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	scan, err := casestore.CountBucketsScan(ctx, c, q)
	if err != nil {
		t.Fatal(err)
	}
	strip := func(cs []casestore.BucketCount) []casestore.BucketCount {
		out := make([]casestore.BucketCount, len(cs))
		for i, bc := range cs {
			bc.ImportID = 0
			out[i] = bc
		}
		return out
	}
	if !reflect.DeepEqual(strip(got.Counts), strip(scan.Counts)) {
		t.Fatalf("chunked = %+v\nscan = %+v", got.Counts, scan.Counts)
	}
	if got.Partial || got.Total != scan.Total {
		t.Fatalf("partial %v total %d, scan total %d", got.Partial, got.Total, scan.Total)
	}
}

// A dropped request (its context ends) stops BucketCounts between and inside
// chunks; it is the context error, never a partial answer.
func TestBucketCountsStopsWhenRequestEnds(t *testing.T) {
	s, _ := newTestStore(t)
	info := mustCreate(t, s, "cancel-buckets")
	c := mustOpen(t, s, info.ID)
	mustImportAs(t, c, "imp-a", "a", 20)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ran := 0
	bucketChunkHook = func(n int) {
		ran++
		if n == 2 {
			cancel() // the client closed the tab
		}
	}
	t.Cleanup(func() { bucketChunkHook = nil })
	q := casestore.BucketQuery{
		AxisStart: testBase, From: testBase, To: testBase.Add(20 * time.Second), WidthMs: 1000, Sources: []string{"imp-a"},
	}
	res, err := c.BucketCounts(ctx, q)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v (result %+v), want context.Canceled", err, res)
	}
	if res.Partial || ran > 3 {
		t.Errorf("partial %v, %d chunks started: the scan went on after the request ended", res.Partial, ran)
	}
}

// Events with a time budget returns the oldest events read so far together with
// ErrScanBudget; a dropped request is the context error instead.
func TestEventsTimeBudgetAndCancel(t *testing.T) {
	s, _ := newTestStore(t)
	info := mustCreate(t, s, "events-budget")
	c := mustOpen(t, s, info.ID)
	mustImportAs(t, c, "imp-a", "a", 40)

	eventRowHook = func(n int) {
		if n == 5 {
			time.Sleep(300 * time.Millisecond)
		}
	}
	t.Cleanup(func() { eventRowHook = nil })
	got, err := c.Events(context.Background(), casestore.EventQuery{Sources: []string{"imp-a"}, MaxScanTime: 100 * time.Millisecond})
	if !errors.Is(err, casestore.ErrScanBudget) {
		t.Fatalf("err = %v, want ErrScanBudget", err)
	}
	if len(got) < 5 || len(got) >= 40 {
		t.Fatalf("got %d events, want the few read before the budget passed", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i].Start.Before(got[i-1].Start) {
			t.Fatal("partial events are not in start order")
		}
	}

	// Without a budget the same stall changes nothing.
	all, err := c.Events(context.Background(), casestore.EventQuery{Sources: []string{"imp-a"}})
	if err != nil || len(all) != 40 {
		t.Fatalf("no budget: %d events, %v", len(all), err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	eventRowHook = func(n int) {
		if n == 5 {
			cancel()
			time.Sleep(50 * time.Millisecond)
		}
	}
	got, err = c.Events(ctx, casestore.EventQuery{Sources: []string{"imp-a"}, MaxScanTime: time.Minute})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: err = %v, want context.Canceled", err)
	}
	if len(got) >= 40 {
		t.Fatal("the read went on after the request ended")
	}
}

// A larger case: the answer is exact, or partial with a consistent DoneTo. Set
// UNRAVIS_PERF=1 for 3 000 000 rows; the default is 120 000.
func TestBucketCountsLargeCase(t *testing.T) {
	if testing.Short() {
		t.Skip("imports a large case")
	}
	n := 120_000
	if os.Getenv("UNRAVIS_PERF") == "1" {
		n = 3_000_000
	}
	s, _ := newTestStore(t, func(o *Options) { o.MaxFileRows = int64(n) + 1 })
	info := mustCreate(t, s, "large-buckets")
	c := mustOpen(t, s, info.ID)
	mustImportAs(t, c, "imp-a", "a", n)

	q := casestore.BucketQuery{
		AxisStart: testBase, From: testBase, To: testBase.Add(time.Duration(n) * time.Second),
		WidthMs: int64(n) * 1000 / 100, Sources: []string{"imp-a"}, CountErrors: true, MaxTime: 3 * time.Second,
	}
	start := time.Now()
	res, err := c.BucketCounts(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > 8*time.Second {
		t.Errorf("took %v with a 3 s budget", took)
	}
	switch {
	case !res.Partial && res.Total != int64(n):
		t.Errorf("complete answer counted %d of %d", res.Total, n)
	case res.Partial && (res.DoneTo.IsZero() || res.Total >= int64(n)):
		t.Errorf("partial answer: total %d, DoneTo %v", res.Total, res.DoneTo)
	}
}

// Fix loop 3: an error cell never counts more than its event cell.
func TestMergeBucketErrorsClampsToCount(t *testing.T) {
	counts := []casestore.BucketCount{
		{Index: 0, Source: "a", ImportID: 1, Count: 3},
		{Index: 1, Source: "a", ImportID: 1, Count: 5},
	}
	errs := []casestore.BucketCount{
		{Index: 0, Source: "a", ImportID: 1, Count: 7}, // more errors than events (rows moved between the queries)
		{Index: 1, Source: "a", ImportID: 1, Count: 2},
		{Index: 9, Source: "z", ImportID: 4, Count: 1}, // no event cell: ignored
	}
	mergeBucketErrors(counts, errs)
	if counts[0].Errors != 3 || counts[1].Errors != 2 {
		t.Fatalf("errors = %d, %d; want 3, 2", counts[0].Errors, counts[1].Errors)
	}
}

// The count and error queries of every chunk read one snapshot: rows deleted by a
// writer between the chunks are still seen by the rest of the same call.
func TestBucketCountsReadOneSnapshot(t *testing.T) {
	s, _ := newTestStore(t)
	c := mustOpen(t, s, mustCreate(t, s, "snap").ID)
	importStatuses(t, c, "imp-a", 120)
	q := casestore.BucketQuery{
		AxisStart: testBase, From: testBase, To: testBase.Add(120 * time.Second), WidthMs: 1000,
		Sources: []string{"imp-a"}, CountErrors: true,
	}
	want, err := c.BucketCounts(context.Background(), q)
	if err != nil || want.Total != 120 {
		t.Fatalf("baseline = %+v, %v", want, err)
	}
	bucketChunkHook = func(n int) {
		if n == 1 {
			if _, err := c.db.Exec("DELETE FROM events"); err != nil {
				t.Errorf("concurrent delete: %v", err)
			}
		}
	}
	t.Cleanup(func() { bucketChunkHook = nil })
	got, err := c.BucketCounts(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	bucketChunkHook = nil
	if !reflect.DeepEqual(got.Counts, want.Counts) || got.Total != want.Total {
		t.Fatalf("a writer between chunks changed the answer: total %d, want %d", got.Total, want.Total)
	}
	for _, bc := range got.Counts {
		if bc.Errors > bc.Count {
			t.Fatalf("cell %+v has more errors than events", bc)
		}
	}
}

// With CountClientErrors and CountTimings the GROUP BY counts the 4xx rows like
// the scan fallback does, and returns every timed event once, in a log bin whose
// Min and Max bracket the event's duration.
func TestBucketCountsClientErrorsAndTimings(t *testing.T) {
	s, _ := newTestStore(t)
	info := mustCreate(t, s, "timing-buckets")
	c := mustOpen(t, s, info.ID)
	evs := tEvents(c, "t", 40)
	statuses := []string{"200", "404", "401", "503", "", "4xx", "4000", "-", "409"}
	var want4xx int64
	var wantTimed int64
	for i := range evs {
		evs[i].Source = "imp-t"
		evs[i].EvidenceID = evidence.EventID(string(c.Info().ID), "imp-t", "t", evs[i].Line, 0)
		st := statuses[i%len(statuses)]
		evs[i].Attrs = nil
		if st != "" {
			evs[i].Attrs = map[string]string{"status": st}
		}
		if casestore.IsClientErrorStatus(st) {
			want4xx++
		}
		if i%5 != 4 { // every fifth event has no duration
			d := float64(i*40 + 3)
			evs[i].DurationMs = &d
			wantTimed++
		} else {
			evs[i].DurationMs = nil
		}
	}
	meta := tMeta("t.csv")
	meta.Importer = "imp-t"
	if _, err := c.Import(context.Background(), meta, tBody("t"), tRun(evs)); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	q := casestore.BucketQuery{
		AxisStart: testBase, From: testBase, To: testBase.Add(40 * time.Second), WidthMs: 10_000,
		Sources: []string{"imp-t"}, CountClientErrors: true, CountTimings: true,
	}
	got, err := c.BucketCounts(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	scan, err := casestore.CountBucketsScan(ctx, c, q)
	if err != nil {
		t.Fatal(err)
	}
	var sum, scanSum int64
	for _, bc := range got.Counts {
		sum += bc.ClientErrors
	}
	for _, bc := range scan.Counts {
		scanSum += bc.ClientErrors
	}
	if sum != want4xx || scanSum != want4xx || want4xx == 0 {
		t.Fatalf("client errors = %d (scan %d), want %d", sum, scanSum, want4xx)
	}

	var timed int64
	for _, tm := range got.Timings {
		timed += tm.Count
		if tm.Count <= 0 || tm.Min > tm.Max || tm.Bin < 0 || tm.Bin > 193 || tm.ImportID == 0 || tm.Source != "imp-t" {
			t.Fatalf("bad timing row %+v", tm)
		}
	}
	if timed != wantTimed {
		t.Fatalf("timed events = %d, want %d", timed, wantTimed)
	}

	q.CountClientErrors, q.CountTimings = false, false
	off, err := c.BucketCounts(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if len(off.Timings) != 0 {
		t.Fatalf("Timings = %d rows without CountTimings", len(off.Timings))
	}
	for _, bc := range off.Counts {
		if bc.ClientErrors != 0 {
			t.Fatalf("ClientErrors = %d without CountClientErrors", bc.ClientErrors)
		}
	}
}

// An excluded source never counts a 4xx, as for 5xx.
func TestBucketCountsClientErrorsHonourExclude(t *testing.T) {
	s, _ := newTestStore(t)
	info := mustCreate(t, s, "exclude-4xx")
	c := mustOpen(t, s, info.ID)
	evs := tEvents(c, "x", 10)
	for i := range evs {
		evs[i].Source = "imp-x"
		evs[i].EvidenceID = evidence.EventID(string(c.Info().ID), "imp-x", "x", evs[i].Line, 0)
		evs[i].Attrs = map[string]string{"status": "404"}
	}
	meta := tMeta("x.csv")
	meta.Importer = "imp-x"
	if _, err := c.Import(context.Background(), meta, tBody("x"), tRun(evs)); err != nil {
		t.Fatal(err)
	}
	q := casestore.BucketQuery{
		AxisStart: testBase, From: testBase, To: testBase.Add(20 * time.Second), WidthMs: 10_000,
		Sources: []string{"imp-x"}, CountClientErrors: true, ErrorExcludeSources: []string{"imp-x"},
	}
	got, err := c.BucketCounts(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if got.Total != 10 {
		t.Fatalf("total = %d, want 10", got.Total)
	}
	for _, bc := range got.Counts {
		if bc.ClientErrors != 0 {
			t.Fatalf("excluded source counted 4xx: %+v", bc)
		}
	}
}

// The GROUP BY returns the exact sum of the durations of each timing group, the
// same as the scan fallback; events without a duration add nothing.
func TestBucketCountsTimingSumExact(t *testing.T) {
	s, _ := newTestStore(t)
	info := mustCreate(t, s, "timing-sum")
	c := mustOpen(t, s, info.ID)
	evs := tEvents(c, "s", 4)
	durs := []*float64{nil, nil, nil, nil}
	a, b, d := 1000.0, 2000.0, 500.0
	durs[0], durs[1], durs[3] = &a, &b, &d // event 2 has no duration
	for i := range evs {
		evs[i].Source = "imp-s"
		evs[i].EvidenceID = evidence.EventID(string(c.Info().ID), "imp-s", "s", evs[i].Line, 0)
		evs[i].Start = testBase.Add(time.Duration(i) * time.Second)
		if i == 3 {
			evs[i].Start = testBase.Add(15 * time.Second) // the next cell
		}
		evs[i].End = evs[i].Start.Add(500 * time.Millisecond)
		evs[i].DurationMs = durs[i]
	}
	meta := tMeta("s.csv")
	meta.Importer = "imp-s"
	if _, err := c.Import(context.Background(), meta, tBody("s"), tRun(evs)); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	q := casestore.BucketQuery{
		AxisStart: testBase, From: testBase, To: testBase.Add(40 * time.Second), WidthMs: 10_000,
		Sources: []string{"imp-s"}, CountTimings: true,
	}
	got, err := c.BucketCounts(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	sums := map[int]float64{}
	for _, tm := range got.Timings {
		if tm.Count <= 0 || tm.Sum < tm.Min || tm.Sum > tm.Max*float64(tm.Count) {
			t.Fatalf("bad timing row %+v", tm)
		}
		sums[tm.Index] += tm.Sum
	}
	if len(sums) != 2 || sums[0] != 3000 || sums[1] != 500 {
		t.Fatalf("sums per cell = %v, want cell 0: 3000, cell 1: 500", sums)
	}
	scan, err := casestore.CountBucketsScan(ctx, c, q)
	if err != nil {
		t.Fatal(err)
	}
	var scanTotal, gotTotal float64
	for _, tm := range scan.Timings {
		scanTotal += tm.Sum
	}
	for _, tm := range got.Timings {
		gotTotal += tm.Sum
	}
	if scanTotal != gotTotal || gotTotal != 3500 {
		t.Fatalf("sum SQL=%v scan=%v, want 3500", gotTotal, scanTotal)
	}
}
