package casestore_test

import (
	"context"
	"encoding/json"
	"math"
	"math/rand"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/evidence"
)

func TestM4EdgesEmptyOnePointAndTies(t *testing.T) {
	var e casestore.M4Edges
	if e.Set() {
		t.Fatal("a new bucket reports a value")
	}
	e.Add(100, 7)
	if !e.Set() || e.First != 7 || e.Last != 7 {
		t.Fatalf("one point: %+v", e)
	}
	// Arrival out of time order: first and last follow the time, not the order.
	e.Add(300, 9)
	e.Add(50, 1)
	e.Add(200, 5)
	if e.First != 1 || e.Last != 9 {
		t.Fatalf("out of order: first=%v last=%v", e.First, e.Last)
	}
	// A tie on time keeps the first value that was there and takes the later last.
	var tie casestore.M4Edges
	tie.Add(10, 1)
	tie.Add(10, 2)
	tie.Add(10, 3)
	if tie.First != 1 || tie.Last != 3 {
		t.Fatalf("ties: first=%v last=%v", tie.First, tie.Last)
	}
}

func TestM4WidthAndBucketMs(t *testing.T) {
	for _, tc := range []struct{ in, want int }{{-5, 1}, {0, 1}, {1, 1}, {600, 600}, {casestore.MaxM4Width, casestore.MaxM4Width}, {casestore.MaxM4Width + 1, casestore.MaxM4Width}, {1 << 30, casestore.MaxM4Width}} {
		if got := casestore.M4Width(tc.in); got != tc.want {
			t.Errorf("M4Width(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
	// The bucket always puts the inclusive span into at most width buckets, and is the smallest that does.
	for _, span := range []int64{0, 1, 9, 10, 11, 999, 1000, 59_999, 60_000, 3_600_000, 86_400_000 * 30} {
		for _, w := range []int{1, 2, 3, 100, 600, 1500, 2000, 5000} {
			b := casestore.M4BucketMs(span, w)
			cw := int64(casestore.M4Width(w))
			if b < 1 || span/b+1 > cw {
				t.Errorf("span %d width %d: bucket %d gives %d buckets", span, w, b, span/b+1)
			}
			if b > 1 && span/(b-1)+1 <= cw {
				t.Errorf("span %d width %d: bucket %d is not the smallest", span, w, b)
			}
		}
	}
}

func m4Query(spanMs int64, width int, sel ...string) casestore.SeriesQuery {
	q := seriesQuery(spanMs, 0, sel...)
	q.Width = width
	return q
}

// events fixed by the test, delivered in the order given (the case's order).
type m4Ev struct {
	atMs int64
	v    float64
}

func fixedCase(evs []m4Ev) *genCase {
	return &genCase{n: len(evs), make: func(i int) evidence.Event {
		return evidence.Event{Kind: "load", Source: "contract", Host: "web-a", Unit: "ms",
			Start: seriesT0.Add(time.Duration(evs[i].atMs) * time.Millisecond), Value: fl(evs[i].v)}
	}}
}

func TestM4SpikeSurvivesAtEveryWidth(t *testing.T) {
	const total = 200_000
	spikeAt := int64(123_457)
	g := &genCase{n: total, make: func(i int) evidence.Event {
		v := 10 + float64(i%7)
		if int64(i) == spikeAt {
			v = 9_999
		}
		if int64(i) == 77_001 {
			v = -250 // a low spike
		}
		return evidence.Event{Kind: "load", Source: "contract", Host: "web-a", Unit: "ms", Start: seriesT0.Add(time.Duration(i) * time.Millisecond), Value: fl(v)}
	}}
	for _, w := range []int{1, 10, 600, 1500, 3000} {
		res, err := casestore.Series(context.Background(), g, m4Query(total-1, w, "kind=load"))
		if err != nil {
			t.Fatal(err)
		}
		pts := res.Series[0].Points
		cw := casestore.M4Width(w)
		if len(pts) > cw || res.Buckets > cw || res.Width != cw {
			t.Fatalf("width %d: %d points, %d buckets, echo %d", w, len(pts), res.Buckets, res.Width)
		}
		hi, lo := math.Inf(-1), math.Inf(1)
		var n int64
		for _, p := range pts {
			hi, lo = math.Max(hi, p.Max), math.Min(lo, p.Min)
			n += p.Count
			if p.First == nil || p.Last == nil || *p.First < p.Min || *p.First > p.Max || *p.Last < p.Min || *p.Last > p.Max {
				t.Fatalf("width %d: first/last outside [min,max]: %+v", w, p)
			}
		}
		if hi != 9_999 || lo != -250 || n != total {
			t.Fatalf("width %d: max=%v min=%v count=%d", w, hi, lo, n)
		}
	}
}

func TestM4BucketEdges(t *testing.T) {
	// span 99 ms, width 10: bucket 10 ms. An event on the edge belongs to the later bucket; To is inclusive.
	evs := []m4Ev{{0, 1}, {9, 2}, {10, 3}, {19, 4}, {20, 5}, {99, 6}}
	res, err := casestore.Series(context.Background(), fixedCase(evs), m4Query(99, 10, "kind=load"))
	if err != nil {
		t.Fatal(err)
	}
	if res.BucketMs != 10 || res.Buckets != 10 {
		t.Fatalf("bucket=%d buckets=%d", res.BucketMs, res.Buckets)
	}
	want := []struct {
		t           int64
		first, last float64
		count       int64
	}{{0, 1, 2, 2}, {10, 3, 4, 2}, {20, 5, 5, 1}, {90, 6, 6, 1}}
	pts := res.Series[0].Points
	if len(pts) != len(want) {
		t.Fatalf("points: %+v", pts)
	}
	base := seriesT0.UnixMilli()
	for i, w := range want {
		p := pts[i]
		if p.T != base+w.t || *p.First != w.first || *p.Last != w.last || p.Count != w.count {
			t.Errorf("point %d = %+v first=%v last=%v, want %+v", i, p, *p.First, *p.Last, w)
		}
	}
}

func TestM4EmptyOnePointAndGaps(t *testing.T) {
	// Empty: no points, and an empty list rather than null on the wire.
	res, err := casestore.Series(context.Background(), fixedCase(nil), m4Query(1000, 50, "kind=load"))
	if err != nil || len(res.Series[0].Points) != 0 {
		t.Fatalf("empty: %v %+v", err, res.Series[0])
	}
	if b, _ := json.Marshal(res); !strings.Contains(string(b), `"points":[]`) {
		t.Errorf("empty answer: %s", b)
	}
	// One point: first, min, max, mean and last are all that value.
	res, err = casestore.Series(context.Background(), fixedCase([]m4Ev{{500, 42}}), m4Query(1000, 50, "kind=load"))
	if err != nil {
		t.Fatal(err)
	}
	p := res.Series[0].Points[0]
	if len(res.Series[0].Points) != 1 || p.Min != 42 || p.Max != 42 || p.Mean != 42 || *p.First != 42 || *p.Last != 42 || p.Count != 1 {
		t.Fatalf("one point: %+v", p)
	}
	// A gap: the buckets between two clusters are absent, not zero.
	res, err = casestore.Series(context.Background(), fixedCase([]m4Ev{{0, 5}, {1, 6}, {9_990, 7}, {9_999, 8}}), m4Query(9_999, 100, "kind=load"))
	if err != nil {
		t.Fatal(err)
	}
	pts := res.Series[0].Points
	if len(pts) != 2 || pts[0].T != seriesT0.UnixMilli() || pts[1].T != seriesT0.UnixMilli()+9_900 || *pts[0].Last != 6 || *pts[1].First != 7 {
		t.Fatalf("gap: %+v", pts)
	}
}

func TestM4TiesAndDisorderKeepFirstAndLastByTime(t *testing.T) {
	// The store may hand events in any order and several on one instant.
	evs := []m4Ev{{30, 3}, {10, 1}, {10, 2}, {20, 9}, {30, 4}}
	res, err := casestore.Series(context.Background(), fixedCase(evs), m4Query(39, 1, "kind=load"))
	if err != nil {
		t.Fatal(err)
	}
	p := res.Series[0].Points[0]
	if res.Buckets != 1 || *p.First != 1 || *p.Last != 4 || p.Min != 1 || p.Max != 9 || p.Count != 5 {
		t.Fatalf("%+v first=%v last=%v", p, *p.First, *p.Last)
	}
}

func TestM4WithWidthKeepsAWiderBucketAndNeverRefusesTooMany(t *testing.T) {
	// Without a width this range and bucket are refused; with a width they are coarsened.
	q := seriesQuery(10_000_000, 1, "kind=load")
	if _, err := casestore.Series(context.Background(), fixedCase(nil), q); err == nil {
		t.Fatal("no width: want too-many-buckets")
	}
	q.Width = 800
	res, err := casestore.Series(context.Background(), fixedCase(nil), q)
	if err != nil || res.Buckets > 800 || res.BucketMs != casestore.M4BucketMs(10_000_000, 800) {
		t.Fatalf("width 800: %v bucket=%d buckets=%d", err, res.BucketMs, res.Buckets)
	}
	// A bucket coarser than the width needs is kept.
	q = seriesQuery(10_000_000, 1_000_000, "kind=load")
	q.Width = 800
	res, err = casestore.Series(context.Background(), fixedCase(nil), q)
	if err != nil || res.BucketMs != 1_000_000 || res.Buckets != 11 {
		t.Fatalf("coarse bucket: %v %d %d", err, res.BucketMs, res.Buckets)
	}
	// An asked bucket below 1 is refused even with a width (0 means not asked).
	q = seriesQuery(1000, -5, "kind=load")
	q.Width = 100
	if _, err := casestore.Series(context.Background(), fixedCase(nil), q); err == nil {
		t.Fatal("negative bucket with a width accepted")
	} else if se, ok := err.(*casestore.SeriesError); !ok || se.Reason != casestore.SeriesBadBucket {
		t.Fatalf("negative bucket with a width: %v", err)
	}
	q = seriesQuery(10_000_000, 1_000_000, "kind=load")
	q.Width = -1
	if _, err := casestore.Series(context.Background(), fixedCase(nil), q); err == nil {
		t.Fatal("negative width accepted")
	} else if se, ok := err.(*casestore.SeriesError); !ok || se.Reason != casestore.SeriesBadWidth {
		t.Fatalf("negative width: %v", err)
	}
}

func TestM4WithoutWidthTheAnswerIsUnchanged(t *testing.T) {
	evs := []m4Ev{{0, 1}, {5, 2}, {12, 3}}
	res, err := casestore.Series(context.Background(), fixedCase(evs), seriesQuery(20, 10, "kind=load"))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(res)
	for _, key := range []string{`"first"`, `"last"`, `"width"`} {
		if strings.Contains(string(b), key) {
			t.Errorf("answer without width carries %s: %s", key, b)
		}
	}
}

// Random data against a brute force that never uses the package: per bucket the first, min, max, last, mean and count.
func TestM4PropertyAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(430)) //nolint:gosec // a fixed seed so the random rounds repeat; nothing here is secret
	for round := 0; round < 40; round++ {
		n := rng.Intn(3000)
		span := int64(1 + rng.Intn(500_000))
		width := 1 + rng.Intn(400)
		evs := make([]m4Ev, n)
		for i := range evs {
			evs[i] = m4Ev{rng.Int63n(span + 1), float64(rng.Intn(2000)-500) / 4}
		}
		// the case hands events in time order (kind, then start); a stable sort by time with ties in arrival order.
		sorted := append([]m4Ev(nil), evs...)
		for i := 1; i < len(sorted); i++ {
			for j := i; j > 0 && sorted[j-1].atMs > sorted[j].atMs; j-- {
				sorted[j-1], sorted[j] = sorted[j], sorted[j-1]
			}
		}
		res, err := casestore.Series(context.Background(), fixedCase(sorted), m4Query(span, width, "kind=load"))
		if err != nil {
			t.Fatal(err)
		}
		pts := res.Series[0].Points
		if len(pts) > width || res.Buckets > casestore.M4Width(width) {
			t.Fatalf("round %d: %d points for width %d", round, len(pts), width)
		}
		type agg struct {
			first, last, lo, hi, sum float64
			n                        int64
		}
		want := map[int64]*agg{}
		for _, e := range sorted {
			b := e.atMs / res.BucketMs
			a := want[b]
			if a == nil {
				want[b] = &agg{e.v, e.v, e.v, e.v, e.v, 1}
				continue
			}
			a.last, a.lo, a.hi, a.sum, a.n = e.v, math.Min(a.lo, e.v), math.Max(a.hi, e.v), a.sum+e.v, a.n+1
		}
		if len(want) != len(pts) {
			t.Fatalf("round %d: %d buckets, want %d", round, len(pts), len(want))
		}
		gmin, gmax := math.Inf(1), math.Inf(-1)
		for _, p := range pts {
			a := want[(p.T-seriesT0.UnixMilli())/res.BucketMs]
			if a == nil || p.Count != a.n || p.Min != a.lo || p.Max != a.hi || *p.First != a.first || *p.Last != a.last || math.Abs(p.Mean-a.sum/float64(a.n)) > 1e-9 {
				t.Fatalf("round %d: bucket %+v, want %+v", round, p, a)
			}
			gmin, gmax = math.Min(gmin, p.Min), math.Max(gmax, p.Max)
		}
		for _, e := range sorted {
			if e.v < gmin || e.v > gmax {
				t.Fatalf("round %d: value %v outside the kept [%v, %v]", round, e.v, gmin, gmax)
			}
		}
	}
}
