package dbhealth

import (
	"context"
	"math"
	"strings"
	"testing"
)

// DATA-11: the busy time of a Query Store block must come from the summed
// duration the server returned, not from an average rounded to 0.1 ms times the
// execution count.

func TestQSIntervalsQueryReturnsTheSummedDurationAndOnlyReads(t *testing.T) {
	q := qLaneQSIntervals(laneReq, []Window{laneInc})
	for _, want := range []string{"AS sum_ms", "SUM(rs.avg_duration * rs.count_executions) / 1000.0 AS sum_ms", "AS avg_ms", "AS max_ms"} {
		if !strings.Contains(q, want) {
			t.Errorf("intervals query lacks %q:\n%s", want, q)
		}
	}
	upper := strings.ToUpper(q)
	if !strings.HasPrefix(strings.TrimSpace(upper), "/* UNRAVIS:QS_INTERVALS */ SELECT") {
		t.Errorf("intervals query is not a plain SELECT: %s", q)
	}
	for _, banned := range []string{"ALTER ", "INSERT ", "UPDATE ", "DELETE ", "EXEC ", "EXEC(", "QUERY_STORE =", "SET "} {
		if strings.Contains(upper, banned) {
			t.Errorf("intervals query contains %q", banned)
		}
	}
}

func TestSeriesOfBusyTimeIsTheSummedDurationNotTheRoundedAverage(t *testing.T) {
	// 3 runs took 10 ms in all: the average is 3.333... ms. Rounded to 0.1 ms it is
	// 3.3, and 3.3 x 3 = 9.9 ms, which loses 0.1 ms of the real total.
	// 7 runs took 100 ms in all: 14.2857... rounds to 14.3 and 14.3 x 7 = 100.1 ms.
	rows := []Row{
		{"start_time": at(20, 10, 0), "execs": int64(3), "avg_ms": 10.0 / 3, "sum_ms": 10.0, "max_ms": 4.04, "win": "inc"},
		{"start_time": at(20, 10, 15), "execs": int64(7), "avg_ms": 100.0 / 7, "sum_ms": 100.0, "max_ms": 20.0, "win": "inc"},
	}
	pts := seriesOf(rows)
	if len(pts) != 2 {
		t.Fatalf("%d points, want 2", len(pts))
	}
	for i, want := range []float64{10, 100} {
		got := pts[i].AvgMs * float64(pts[i].Execs) // how the tracing lane derives busy time
		if math.Abs(got-want) > 1e-9 {
			t.Errorf("point %d busy = %v ms, want the summed duration %v (rounded avg x execs would give %v)", i, got, want, round1(pts[i].AvgMs)*float64(pts[i].Execs))
		}
	}
	// The slowest single run is still shown to 0.1 ms.
	if pts[0].MaxMs != 4 {
		t.Errorf("max = %v, want 4", pts[0].MaxMs)
	}
}

func TestSeriesOfWithoutASumKeepsTheRoundedAverage(t *testing.T) {
	// A row from a reader that has no sum column (older saved reads, fakes).
	pts := seriesOf([]Row{{"start_time": at(20, 10, 0), "execs": int64(3), "avg_ms": 10.0 / 3, "max_ms": 1.0}})
	if len(pts) != 1 || pts[0].AvgMs != 3.3 {
		t.Fatalf("points %+v, want one point averaging 3.3", pts)
	}
	// A sum that cannot be used (negative, not a number) also falls back.
	for _, bad := range []any{nil, "x", -5.0, math.NaN(), math.Inf(1)} {
		pts = seriesOf([]Row{{"start_time": at(20, 10, 0), "execs": int64(3), "avg_ms": 10.0 / 3, "sum_ms": bad}})
		if len(pts) != 1 || pts[0].AvgMs != 3.3 {
			t.Errorf("sum %v: points %+v, want the rounded average 3.3", bad, pts)
		}
	}
}

func TestSQLLaneSeriesBusyTimeComesFromTheSum(t *testing.T) {
	q := healthySQL("16.0.1")
	q.by["unravis:qs_intervals"] = []Row{
		{"start_time": at(20, 10, 0), "execs": int64(3), "avg_ms": 10.0 / 3, "sum_ms": 10.0, "max_ms": 4.0, "win": "inc"},
	}
	r := RunSQLLane(context.Background(), q, "Prod", laneReq, fixedNow)
	if r.Lane == nil || len(r.Lane.Series) != 1 {
		t.Fatalf("series = %+v", r.Lane)
	}
	p := r.Lane.Series[0]
	if busy := p.AvgMs * float64(p.Execs); math.Abs(busy-10.0) > 1e-9 {
		t.Errorf("busy = %v ms, want 10 (the summed duration)", busy)
	}
}
