package sqlite

import (
	"context"
	"errors"
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
