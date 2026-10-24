package casestore_test

import (
	"context"
	"errors"
	"io"
	"math"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

var seriesT0 = time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)

// genCase is a Case that makes its events up as it streams them, so a test can
// aggregate millions without holding them. Only Each is implemented; any other
// method panics on the nil embedded interface, which a test would notice.
type genCase struct {
	casestore.Case
	n     int
	make  func(i int) evidence.Event
	last  casestore.EventQuery
	calls int
}

func (g *genCase) Each(ctx context.Context, q casestore.EventQuery, fn func(evidence.Event) error) error {
	g.calls++
	g.last = q
	budget := q.NewScanBudget()
	for i := 0; i < g.n; i++ {
		if i%1024 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		if err := budget.Admit(); err != nil {
			return err
		}
		if err := fn(g.make(i)); err != nil {
			return err
		}
	}
	return nil
}

func fl(v float64) *float64 { return &v }

func seriesQuery(spanMs, bucketMs int64, sel ...string) casestore.SeriesQuery {
	return casestore.SeriesQuery{
		From: seriesT0, To: seriesT0.Add(time.Duration(spanMs) * time.Millisecond),
		BucketMs: bucketMs, Sel: sel,
	}
}

// 3M events, one per millisecond, in 50 one-minute buckets. The value of event
// i is i%1000 plus ten for every bucket, so each bucket has an exact mean.
func TestSeriesThreeMillionEventsGiveExactBucketStatistics(t *testing.T) {
	const total, bucket = 3_000_000, 60_000
	g := &genCase{n: total, make: func(i int) evidence.Event {
		return evidence.Event{
			Kind: "load", Source: "contract", Host: "web-a", Unit: "pct",
			Start: seriesT0.Add(time.Duration(i) * time.Millisecond),
			Value: fl(float64(i%1000) + float64(i/bucket)*10),
		}
	}}
	res, err := casestore.Series(context.Background(), g, seriesQuery(total-1, bucket, "kind=load"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Partial || res.Scanned != total || res.Buckets != 50 || g.calls != 1 {
		t.Fatalf("partial=%v scanned=%d buckets=%d passes=%d", res.Partial, res.Scanned, res.Buckets, g.calls)
	}
	s := res.Series[0]
	if s.Count != total || s.Unit != "pct" || s.UnitMixed || len(s.Points) != 50 {
		t.Fatalf("series: count=%d unit=%q mixed=%v points=%d", s.Count, s.Unit, s.UnitMixed, len(s.Points))
	}
	for b, p := range s.Points {
		want := casestore.SeriesPoint{
			T: seriesT0.UnixMilli() + int64(b)*bucket, Min: float64(b) * 10, Max: 999 + float64(b)*10,
			Mean: 499.5 + float64(b)*10, Count: bucket,
		}
		if p != want {
			t.Fatalf("bucket %d: %+v, want %+v", b, p, want)
		}
	}
	if len(g.last.Kinds) != 1 || g.last.Kinds[0] != "load" || g.last.Window.From.IsZero() || g.last.Window.To.IsZero() {
		t.Errorf("query handed to Each: %+v", g.last)
	}
	if len(g.last.Sources) != 0 || len(g.last.Hosts) != 0 {
		t.Errorf("source and host filters sent although the selector sets neither: %+v", g.last)
	}
}

func TestSeriesScanBudgetMarksTheResultPartial(t *testing.T) {
	g := &genCase{n: 5000, make: func(i int) evidence.Event {
		return evidence.Event{Kind: "load", Start: seriesT0.Add(time.Duration(i) * time.Millisecond), Value: fl(2)}
	}}
	q := seriesQuery(4999, 1000, "kind=load")
	q.MaxScan = 1200
	res, err := casestore.Series(context.Background(), g, q)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Partial || res.Scanned != 1200 || res.Series[0].Count != 1200 {
		t.Fatalf("partial=%v scanned=%d count=%d, want partial after 1200", res.Partial, res.Scanned, res.Series[0].Count)
	}
	// The buckets read so far hold the events read so far and nothing else.
	var counted int64
	for _, p := range res.Series[0].Points {
		counted += p.Count
	}
	if counted != 1200 {
		t.Errorf("points hold %d events", counted)
	}
	// The same query with room is complete.
	q.MaxScan = 5000
	res, err = casestore.Series(context.Background(), g, q)
	if err != nil || res.Partial || res.Scanned != 5000 {
		t.Fatalf("with room: partial=%v scanned=%d err=%v", res.Partial, res.Scanned, err)
	}
	// A scan stopped by time is partial too.
	slow := &genCase{n: 2000, make: func(i int) evidence.Event {
		if i == 0 {
			time.Sleep(30 * time.Millisecond)
		}
		return evidence.Event{Kind: "load", Start: seriesT0.Add(time.Duration(i) * time.Millisecond), Value: fl(1)}
	}}
	q = seriesQuery(1999, 100, "kind=load")
	q.MaxScanTime = 10 * time.Millisecond
	res, err = casestore.Series(context.Background(), slow, q)
	if err != nil || !res.Partial || res.Scanned == 0 || res.Scanned >= 2000 {
		t.Fatalf("time budget: partial=%v scanned=%d err=%v", res.Partial, res.Scanned, err)
	}
}

func TestSeriesOtherErrorsAreNotPartial(t *testing.T) {
	for _, want := range []error{casestore.ErrNotFound, casestore.ErrHandleClosed, context.Canceled, errors.New("disk")} {
		c := &failCase{err: want}
		res, err := casestore.Series(context.Background(), c, seriesQuery(1000, 10, "kind=load"))
		if !errors.Is(err, want) || res.Partial || len(res.Series) != 0 {
			t.Errorf("Each error %v: err=%v partial=%v series=%d", want, err, res.Partial, len(res.Series))
		}
	}
}

type failCase struct {
	casestore.Case
	err error
}

func (f *failCase) Each(context.Context, casestore.EventQuery, func(evidence.Event) error) error {
	return f.err
}

// sliceCase streams a fixed slice, applying only the scan budget.
type sliceCase struct {
	casestore.Case
	events []evidence.Event
}

func (s *sliceCase) Each(_ context.Context, q casestore.EventQuery, fn func(evidence.Event) error) error {
	budget := q.NewScanBudget()
	for _, e := range s.events {
		if err := budget.Admit(); err != nil {
			return err
		}
		if err := fn(e); err != nil {
			return err
		}
	}
	return nil
}

func TestSeriesSelectorsFieldsUnitsAndSkips(t *testing.T) {
	at := func(ms int64) time.Time { return seriesT0.Add(time.Duration(ms) * time.Millisecond) }
	evs := []evidence.Event{
		{Kind: "op", Source: "mongo", Host: "db-a", Start: at(0), Value: fl(4), DurationMs: fl(40), Unit: "ms", Attrs: map[string]string{"coll": "orders"}},
		{Kind: "op", Source: "mongo", Host: "db-a", Start: at(5), Value: fl(8), DurationMs: fl(80), Unit: "ms", Attrs: map[string]string{"coll": "orders"}},
		{Kind: "op", Source: "mongo", Host: "db-b", Start: at(6), Value: fl(100), DurationMs: fl(1), Unit: "ms", Attrs: map[string]string{"coll": "items"}},
		{Kind: "op", Source: "mongo", Host: "db-a", Start: at(12), Value: fl(6), Unit: "ops", Attrs: map[string]string{"coll": "orders"}},
		{Kind: "op", Source: "mongo", Host: "db-a", Start: at(13)},                         // no number: skipped
		{Kind: "op", Source: "mongo", Host: "db-a", Start: at(14), Value: fl(math.NaN())},  // not finite: skipped
		{Kind: "op", Source: "mongo", Host: "db-a", Start: at(15), Value: fl(math.Inf(1))}, // not finite: skipped
		{Kind: "op", Source: "mongo", Host: "db-a", Start: at(500), Value: fl(7)},          // outside the window
		{Kind: "other", Source: "mongo", Host: "db-a", Start: at(1), Value: fl(1000)},
	}
	c := &sliceCase{events: evs}
	res, err := casestore.Series(context.Background(), c, seriesQuery(19, 10,
		"kind=op;host=db-a;attr.coll=orders",
		"kind=op;host=db-a;attr.coll=orders;field=duration",
		"kind=op;source=mongo",
		"kind=nothing",
	))
	if err != nil {
		t.Fatal(err)
	}
	if res.Buckets != 2 || len(res.Series) != 4 {
		t.Fatalf("buckets=%d series=%d", res.Buckets, len(res.Series))
	}
	a := res.Series[0]
	if a.Sel != "kind=op;host=db-a;attr.coll=orders" || a.Count != 3 || len(a.Points) != 2 {
		t.Fatalf("series 0: %+v", a)
	}
	if p := a.Points[0]; p.T != seriesT0.UnixMilli() || p.Min != 4 || p.Max != 8 || p.Mean != 6 || p.Count != 2 {
		t.Errorf("series 0 bucket 0: %+v", p)
	}
	if p := a.Points[1]; p.T != seriesT0.UnixMilli()+10 || p.Min != 6 || p.Max != 6 || p.Count != 1 {
		t.Errorf("series 0 bucket 1: %+v", p)
	}
	if !a.UnitMixed || a.Unit != "ms" {
		t.Errorf("series 0 unit %q mixed=%v, want ms and mixed", a.Unit, a.UnitMixed)
	}
	d := res.Series[1]
	if d.Count != 2 || len(d.Points) != 1 || d.Points[0].Mean != 60 || d.Points[0].Min != 40 || d.Points[0].Max != 80 {
		t.Errorf("series 1 (duration): %+v", d)
	}
	if d.UnitMixed {
		t.Errorf("series 1 reports mixed units: only the two ms events carry a number")
	}
	if got := res.Series[2].Count; got != 4 { // 4, 8, 100, 6; the NaN, Inf, empty and out-of-window events are not counted
		t.Errorf("series 2 count %d, want 4", got)
	}
	empty := res.Series[3]
	if empty.Count != 0 || empty.Points == nil || len(empty.Points) != 0 {
		t.Errorf("a selector that matches nothing: %+v (points must be an empty list, not null)", empty)
	}
	// Selectors that all name a source and a host let the store filter on them.
	g := &genCase{n: 0}
	if _, err := casestore.Series(context.Background(), g, seriesQuery(10, 1,
		"kind=op;source=mongo;host=db-a", "kind=op;source=iis;host=db-b")); err != nil {
		t.Fatal(err)
	}
	if len(g.last.Sources) != 2 || len(g.last.Hosts) != 2 || len(g.last.Kinds) != 1 {
		t.Errorf("filters sent to Each: %+v", g.last)
	}
}

func TestSeriesRefusesBadInput(t *testing.T) {
	long := strings.Repeat("x", 600)
	ok := "kind=load"
	cases := []struct {
		name   string
		q      casestore.SeriesQuery
		reason string
	}{
		{"zero from", casestore.SeriesQuery{To: seriesT0, BucketMs: 1, Sel: []string{ok}}, casestore.SeriesBadTime},
		{"zero to", casestore.SeriesQuery{From: seriesT0, BucketMs: 1, Sel: []string{ok}}, casestore.SeriesBadTime},
		{"to before from", casestore.SeriesQuery{From: seriesT0.Add(time.Second), To: seriesT0, BucketMs: 1, Sel: []string{ok}}, casestore.SeriesBadTime},
		{"two centuries", casestore.SeriesQuery{From: seriesT0, To: seriesT0.AddDate(250, 0, 0), BucketMs: 1e12, Sel: []string{ok}}, casestore.SeriesBadTime},
		{"bucket zero", seriesQuery(1000, 0, ok), casestore.SeriesBadBucket},
		{"bucket negative", seriesQuery(1000, -5, ok), casestore.SeriesBadBucket},
		{"2001 buckets", seriesQuery(2000, 1, ok), casestore.SeriesTooManyBuckets},
		{"a year in milliseconds", seriesQuery(365*24*3600*1000, 1, ok), casestore.SeriesTooManyBuckets},
		{"no selector", seriesQuery(1000, 10), casestore.SeriesBadSelector},
		{"empty selector", seriesQuery(1000, 10, ""), casestore.SeriesBadSelector},
		{"no kind", seriesQuery(1000, 10, "host=a"), casestore.SeriesBadSelector},
		{"no equals", seriesQuery(1000, 10, "kind"), casestore.SeriesBadSelector},
		{"empty value", seriesQuery(1000, 10, "kind="), casestore.SeriesBadSelector},
		{"unknown term", seriesQuery(1000, 10, "kind=a;color=red"), casestore.SeriesBadSelector},
		{"repeated term", seriesQuery(1000, 10, "kind=a;kind=b"), casestore.SeriesBadSelector},
		{"bad field", seriesQuery(1000, 10, "kind=a;field=cpu"), casestore.SeriesBadSelector},
		{"bad attribute name", seriesQuery(1000, 10, "kind=a;attr.=x"), casestore.SeriesBadSelector},
		{"bad attribute name 2", seriesQuery(1000, 10, "kind=a;attr.a b=x"), casestore.SeriesBadSelector},
		{"five attributes", seriesQuery(1000, 10, "kind=a;attr.a=1;attr.b=1;attr.c=1;attr.d=1;attr.e=1"), casestore.SeriesBadSelector},
		{"nine terms", seriesQuery(1000, 10, "kind=a;host=a;source=a;field=value;attr.a=1;attr.b=1;attr.c=1;attr.d=1;x=1"), casestore.SeriesBadSelector},
		{"long selector", seriesQuery(1000, 10, "kind="+long), casestore.SeriesBadSelector},
		{"long value", seriesQuery(1000, 10, "kind=a;host="+strings.Repeat("h", 65)), casestore.SeriesBadSelector},
		{"control character", seriesQuery(1000, 10, "kind=a;host=a\x07b"), casestore.SeriesBadSelector},
		{"newline", seriesQuery(1000, 10, "kind=a;host=a\nb"), casestore.SeriesBadSelector},
		{"bad utf-8", seriesQuery(1000, 10, "kind=a;host=a\xffb"), casestore.SeriesBadSelector},
		{"bad kind", seriesQuery(1000, 10, "kind=a b"), casestore.SeriesBadSelector},
		{"51 series", seriesQuery(1000, 10, repeatSel(51)...), casestore.SeriesTooManySeries},
	}
	for _, tc := range cases {
		g := &genCase{n: 1, make: func(int) evidence.Event { t.Error("a refused query still scanned"); return evidence.Event{} }}
		_, err := casestore.Series(context.Background(), g, tc.q)
		var se *casestore.SeriesError
		if !errors.As(err, &se) || se.Reason != tc.reason {
			t.Errorf("%s: %v, want reason %s", tc.name, err, tc.reason)
			continue
		}
		if g.calls != 0 {
			t.Errorf("%s: the store was asked for events", tc.name)
		}
	}
}

func repeatSel(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = "kind=load"
	}
	return out
}

func TestSeriesLimitsAreInclusive(t *testing.T) {
	g := &genCase{n: 0}
	if _, err := casestore.Series(context.Background(), g, seriesQuery(1999, 1, "kind=a")); err != nil {
		t.Errorf("2000 buckets: %v", err)
	}
	res, err := casestore.Series(context.Background(), g, seriesQuery(1999, 1, repeatSel(50)...))
	if err != nil || len(res.Series) != 50 {
		t.Errorf("50 series: %d, %v", len(res.Series), err)
	}
	res, err = casestore.Series(context.Background(), g, seriesQuery(0, 1, "kind=a"))
	if err != nil || res.Buckets != 1 {
		t.Errorf("a single instant: buckets=%d err=%v", res.Buckets, err)
	}
	// The refusal names the smallest bucket that fits, and that bucket is accepted.
	_, err = casestore.Series(context.Background(), g, seriesQuery(2000*60_000, 60_000, "kind=a"))
	var se *casestore.SeriesError
	if !errors.As(err, &se) || !strings.Contains(se.Message, "at least 60001 ms") {
		t.Fatalf("message: %v", err)
	}
	if res, err := casestore.Series(context.Background(), g, seriesQuery(2000*60_000, 60_001, "kind=a")); err != nil || res.Buckets != 2000 {
		t.Errorf("the suggested bucket: buckets=%d err=%v", res.Buckets, err)
	}
}

// A selector value is never repeated in an error, so a pasted secret is not
// echoed into a response or a log.
func TestSeriesErrorsDoNotEchoInput(t *testing.T) {
	const secret = "SENTINEL-7f3a"
	for _, sel := range []string{"kind=a;" + secret + "=1", "kind=a;field=" + secret, "kind=a;attr." + secret + " x=1", secret} {
		_, err := casestore.Series(context.Background(), &genCase{}, seriesQuery(1000, 10, sel))
		if err == nil || strings.Contains(err.Error(), secret) {
			t.Errorf("selector %q: error %v", sel, err)
		}
	}
}

// The same aggregate over the real in-memory store.
func TestSeriesOverTheMemoryStore(t *testing.T) {
	ctx := context.Background()
	store := casestore.NewMemory()
	info, err := store.Create(ctx, "series")
	if err != nil {
		t.Fatal(err)
	}
	c, err := store.Open(ctx, info.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var evs []evidence.Event
	for i := 0; i < 600; i++ {
		kind := "load"
		if i%3 == 0 {
			kind = "other"
		}
		d := 5.0
		evs = append(evs, evidence.Event{
			EvidenceID: evidence.EventID(string(info.ID), "contract", string(rune('a'+i%26))+strings.Repeat("z", i/26), 2, 0),
			Source:     "contract", Kind: kind, Host: "web-a", Start: seriesT0.Add(time.Duration(i) * time.Second),
			End: seriesT0.Add(time.Duration(i)*time.Second + time.Millisecond), DurationMs: &d, Value: fl(float64(i)),
			Unit: "pct", Line: 2, TZRule: "utc-field",
		})
	}
	run := func(r io.Reader, emit func(evidence.Event) error) (importer.Report, error) {
		rep := importer.Report{Importer: "contract"}
		if _, err := io.Copy(io.Discard, r); err != nil {
			return rep, err
		}
		for _, e := range evs {
			rep.Rows++
			if err := emit(e); err != nil {
				return rep, err
			}
			rep.Emitted++
		}
		return rep, nil
	}
	_, err = c.Import(ctx, casestore.ImportMeta{
		Importer: "contract", FileName: "f.txt", HostLabel: "web-a", TZRule: "utc-field",
		AckGating: true, AckText: "ack",
	}, strings.NewReader("x"), run)
	if err != nil {
		t.Fatal(err)
	}
	res, err := casestore.Series(ctx, c, casestore.SeriesQuery{
		From: seriesT0, To: seriesT0.Add(599 * time.Second), BucketMs: 300_000, Sel: []string{"kind=load"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Events 0..599 minus every third; bucket 0 is 0..299, bucket 1 is 300..599.
	var sum [2]float64
	var n [2]int64
	for i := 0; i < 600; i++ {
		if i%3 != 0 {
			sum[i/300] += float64(i)
			n[i/300]++
		}
	}
	pts := res.Series[0].Points
	if res.Partial || len(pts) != 2 || pts[0].Count != n[0] || pts[1].Count != n[1] {
		t.Fatalf("points %+v, want counts %v", pts, n)
	}
	for b := range pts {
		if pts[b].Mean != sum[b]/float64(n[b]) {
			t.Errorf("bucket %d mean %v, want %v", b, pts[b].Mean, sum[b]/float64(n[b]))
		}
	}
}
