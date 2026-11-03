package casestore_test

import (
	"context"
	"math"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/evidence"
)

func clumsyReq(at time.Duration, ms float64, status string) evidence.Event {
	e := evidence.Event{Kind: "request", Start: seriesT0.Add(at), DurationMs: fl(ms)}
	if status != "" {
		e.Attrs = map[string]string{"status": status}
	}
	return e
}

// No events at all, or events with no duration, give no points and no panic.
func TestClumsySeriesQuantileEmpty(t *testing.T) {
	for _, evs := range [][]evidence.Event{
		nil,
		{{Kind: "request", Start: seriesT0}}, // no duration
		{{Kind: "request", Start: seriesT0, DurationMs: fl(math.NaN())}},        // NaN
		{{Kind: "request", Start: seriesT0, DurationMs: fl(math.Inf(1))}},       // Inf
		{{Kind: "other", Start: seriesT0, DurationMs: fl(5)}},                   // other kind
		{{Kind: "request", Start: seriesT0.Add(-time.Hour), DurationMs: fl(5)}}, // before the window
		{{Kind: "request", Start: seriesT0.Add(time.Hour), DurationMs: fl(5)}},  // after the window
	} {
		res, err := casestore.Series(context.Background(), &sliceCase{events: evs},
			seriesQuery(9999, 1000, "kind=request;field=duration;q=99;group=statusclass"))
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Series) != 1 || len(res.Series[0].Points) != 0 {
			t.Errorf("%v: points %+v", evs, res.Series)
		}
	}
}

// q values outside 50/90/95/99 are refused with the fixed reason and never echoed.
func TestClumsySeriesQuantileOutOfRange(t *testing.T) {
	for _, q := range []string{"", "0", "100", "-1", "-95", "95.0", "95.5", "095", " 95", "p95", "NaN", "Inf", "1e2", "9999999999999999999", "0.95"} {
		_, err := casestore.Series(context.Background(), &sliceCase{},
			seriesQuery(999, 1000, "kind=request;field=duration;q="+q))
		if got := seriesReason(t, err); got != casestore.SeriesBadSelector {
			t.Errorf("q=%q: %s", q, got)
		}
	}
	// Both q and group repeated: the last wins or it is refused, but it never panics.
	_, _ = casestore.Series(context.Background(), &sliceCase{},
		seriesQuery(999, 1000, "kind=request;field=duration;q=95;q=99;group=statusclass;group=statusclass"))
}

// Negative, zero, huge and sub-millisecond durations: the quantile stays inside
// [min, max] of the bucket, and a negative value cannot push the rank past the
// values that were binned (which would answer max for a median).
func TestClumsySeriesQuantileOddDurations(t *testing.T) {
	var evs []evidence.Event
	for i := 0; i < 9; i++ {
		evs = append(evs, clumsyReq(time.Duration(i)*time.Millisecond, -1, "200"))
	}
	evs = append(evs, clumsyReq(10*time.Millisecond, 100, "200"))
	// second bucket: huge and zero
	evs = append(evs, clumsyReq(1500*time.Millisecond, 1e300, "200"), clumsyReq(1600*time.Millisecond, 0, "200"),
		clumsyReq(1700*time.Millisecond, 0.0001, "200"))
	for _, q := range []string{"50", "90", "95", "99"} {
		res, err := casestore.Series(context.Background(), &sliceCase{events: evs},
			seriesQuery(1999, 1000, "kind=request;field=duration;q="+q))
		if err != nil {
			t.Fatal(err)
		}
		pts := res.Series[0].Points
		if len(pts) != 2 {
			t.Fatalf("q=%s points %d", q, len(pts))
		}
		for _, p := range pts {
			if p.Q == nil || math.IsNaN(*p.Q) || *p.Q < p.Min || *p.Q > p.Max {
				t.Errorf("q=%s: Q %v outside [%v, %v]", q, p.Q, p.Min, p.Max)
			}
		}
		if q == "50" && *pts[0].Q > 1 {
			t.Errorf("median of nine -1 and one 100 is %v, want at most the first bin bound (1)", *pts[0].Q)
		}
	}
}

// Unknown, malformed and odd status values all land in a known class; the class
// counts add up to the bucket count.
func TestClumsySeriesStatusClassUnknown(t *testing.T) {
	statuses := []string{"", "abc", "99", "1000", "０５００", "20x", " 200", "-50", "600", "000", "099", "5xx", "404", "100", "199", "599", "\x00", "5\n0"}
	var evs []evidence.Event
	for i, st := range statuses {
		evs = append(evs, clumsyReq(time.Duration(i)*time.Millisecond, 5, st))
	}
	res, err := casestore.Series(context.Background(), &sliceCase{events: evs},
		seriesQuery(999, 1000, "kind=request;field=duration;group=statusclass", "kind=request;field=duration;group=statusclass;q=50"))
	if err != nil {
		t.Fatal(err)
	}
	for si, s := range res.Series {
		if len(s.Points) != 1 {
			t.Fatalf("series %d points %d", si, len(s.Points))
		}
		p := s.Points[0]
		var sum int64
		for k, n := range p.ByClass {
			switch k {
			case "1xx", "2xx", "3xx", "4xx", "5xx", "other":
			default:
				t.Errorf("series %d: unexpected class %q", si, k)
			}
			sum += n
		}
		if sum != p.Count {
			t.Errorf("series %d: classes add to %d, count %d", si, sum, p.Count)
		}
		if p.ByClass["4xx"] != 1 || p.ByClass["5xx"] != 1 || p.ByClass["1xx"] != 2 || p.ByClass["other"] != int64(len(statuses))-4 {
			t.Errorf("series %d: %v", si, p.ByClass)
		}
	}
	// The two series do not share one map.
	if len(res.Series) == 2 {
		res.Series[0].Points[0].ByClass["2xx"] = 99
		if res.Series[1].Points[0].ByClass["2xx"] == 99 {
			t.Error("ByClass map shared between series")
		}
	}
}
