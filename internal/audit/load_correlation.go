package audit

import (
	"math"
	"sort"
	"time"
)

// minCorrelationPoints is the fewest paired buckets for which a rank
// correlation is reported; below it the coefficient is mostly noise.
const minCorrelationPoints = 8

// Rollup is one API's current-window request volume and error rate, summed over
// sources, kinds and outcomes. Requests and Failures count only requests with a
// known outcome: unknown outcomes are left out of numerator and denominator and
// are reported in Quality.UnknownOutcome instead.
type Rollup struct {
	Controller string   `json:"controller"`
	Action     string   `json:"action"`
	Requests   int64    `json:"requests"`
	Failures   int64    `json:"failures"`
	ErrorRate  *float64 `json:"errorRate,omitempty"`
	PerMinute  float64  `json:"perMinute"`

	ClientErrorRate *float64 `json:"clientErrorRate,omitempty"`
	ServerErrorRate *float64 `json:"serverErrorRate,omitempty"`
}

// buildRollups folds the current-window outcome-split cohorts into one row per
// (controller, action), sorted by name. windowMinutes is the length of the
// current window. The sources apply the outcome filter before summarizing, so
// when allOutcomes is false the failure share is unknowable and ErrorRate stays
// nil; throughput is still reported. ClientErrorRate (4xx) and ServerErrorRate
// (5xx plus failed hangfire jobs) split ErrorRate and follow the same rule;
// together they never exceed it.
func buildRollups(cohorts map[string]*cohortAccumulator, windowMinutes float64, allOutcomes bool) []Rollup {
	type name struct{ controller, action string }
	byName := map[name]*Rollup{}
	clientErrors, serverErrors := map[name]int64{}, map[name]int64{}
	for _, c := range cohorts {
		if c.currentEvents <= 0 {
			continue
		}
		r := byName[name{c.controller, c.action}]
		if r == nil {
			r = &Rollup{Controller: c.controller, Action: c.action}
			byName[name{c.controller, c.action}] = r
		}
		switch c.outcome {
		case "success":
			r.Requests = saturatingAdd(r.Requests, c.currentEvents)
		case "failure":
			r.Requests = saturatingAdd(r.Requests, c.currentEvents)
			r.Failures = saturatingAdd(r.Failures, c.currentEvents)
			clientErrors[name{c.controller, c.action}] = saturatingAdd(clientErrors[name{c.controller, c.action}], c.currentClientErrors)
			serverErrors[name{c.controller, c.action}] = saturatingAdd(serverErrors[name{c.controller, c.action}], c.currentServerErrors)
		}
	}
	rows := make([]Rollup, 0, len(byName))
	for key, r := range byName {
		if r.Requests > 0 {
			if allOutcomes {
				rate := float64(r.Failures) / float64(r.Requests)
				r.ErrorRate = &rate
				client := float64(clientErrors[key]) / float64(r.Requests)
				server := float64(serverErrors[key]) / float64(r.Requests)
				r.ClientErrorRate, r.ServerErrorRate = &client, &server
			}
			if windowMinutes > 0 {
				r.PerMinute = float64(r.Requests) / windowMinutes
			}
		}
		rows = append(rows, *r)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Controller != rows[j].Controller {
			return rows[i].Controller < rows[j].Controller
		}
		return rows[i].Action < rows[j].Action
	})
	return rows
}

func saturatingAdd(a, b int64) int64 {
	if b > 0 && a > math.MaxInt64-b {
		return math.MaxInt64
	}
	return a + b
}

// EstimateConcurrency applies Little's law to each bucket: the average number
// of requests in flight is the summed duration divided by the bucket length.
// The result is aligned with buckets. It is nil when bucketSeconds is not
// positive.
func EstimateConcurrency(buckets []Bucket, bucketSeconds int) []float64 {
	if bucketSeconds <= 0 {
		return nil
	}
	width := float64(bucketSeconds) * 1000
	out := make([]float64, len(buckets))
	for i, b := range buckets {
		out[i] = b.SumMs / width
	}
	return out
}

// SpearmanLoad returns the Spearman rank correlation between estimated
// concurrency and per-bucket P95 latency. Buckets without a P95 are dropped.
// It is nil with fewer than minCorrelationPoints paired buckets, when either
// side has no rank variance, or when the inputs are misaligned.
func SpearmanLoad(concurrency []float64, p95 []*float64) *float64 {
	if len(concurrency) != len(p95) {
		return nil
	}
	x := make([]float64, 0, len(concurrency))
	y := make([]float64, 0, len(concurrency))
	for i, v := range p95 {
		if v == nil || math.IsNaN(*v) || math.IsInf(*v, 0) || math.IsNaN(concurrency[i]) || math.IsInf(concurrency[i], 0) {
			continue
		}
		x = append(x, concurrency[i])
		y = append(y, *v)
	}
	if len(x) < minCorrelationPoints {
		return nil
	}
	rx, ry := averageRanks(x), averageRanks(y)
	n := float64(len(rx))
	var mx, my float64
	for i := range rx {
		mx += rx[i]
		my += ry[i]
	}
	// Ranks are multiples of 0.5, so these sums are exact and a constant side
	// has exactly zero variance below.
	mx, my = mx/n, my/n
	var sxy, sxx, syy float64
	for i := range rx {
		dx, dy := rx[i]-mx, ry[i]-my
		sxy += dx * dy
		sxx += dx * dx
		syy += dy * dy
	}
	if !(sxx > 0) || !(syy > 0) {
		return nil
	}
	rho := sxy / math.Sqrt(sxx*syy)
	if math.IsNaN(rho) || math.IsInf(rho, 0) {
		return nil
	}
	rho = math.Max(-1, math.Min(1, rho))
	return &rho
}

// averageRanks returns 1-based ranks where tied values share their mean rank.
func averageRanks(values []float64) []float64 {
	idx := make([]int, len(values))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return values[idx[a]] < values[idx[b]] })
	ranks := make([]float64, len(values))
	for start := 0; start < len(idx); {
		end := start + 1
		for end < len(idx) && values[idx[end]] == values[idx[start]] {
			end++
		}
		mean := float64(start+end+1) / 2 // ranks start+1 .. end
		for k := start; k < end; k++ {
			ranks[idx[k]] = mean
		}
		start = end
	}
	return ranks
}

// otherLoad answers, for any cohort, the summed request time per time bucket
// of every other cohort. It never subtracts from a grand total: prefix and
// suffix sums over the cohorts in sorted order mean a cohort's own value (and
// map iteration order) never enters the arithmetic, so constant neighbours give
// bit-identical values in every bucket and in every run.
type otherLoad struct {
	slot           map[int64]int
	prefix, suffix [][]float64 // prefix[k] sums cohorts before k, suffix[k] cohorts after k
}

// newOtherLoad takes each cohort's per-bucket summed time, in sorted-key order.
func newOtherLoad(sums []map[int64]float64) *otherLoad {
	o := &otherLoad{slot: map[int64]int{}}
	var times []int64
	for _, m := range sums {
		for at := range m {
			if _, ok := o.slot[at]; !ok {
				o.slot[at] = 0
				times = append(times, at)
			}
		}
	}
	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
	for i, at := range times {
		o.slot[at] = i
	}
	n, width := len(sums), len(times)
	o.prefix, o.suffix = make([][]float64, n+1), make([][]float64, n+1)
	for k := range o.prefix {
		o.prefix[k], o.suffix[k] = make([]float64, width), make([]float64, width)
	}
	for k := 0; k < n; k++ {
		copy(o.prefix[k+1], o.prefix[k])
		for at, v := range sums[k] {
			o.prefix[k+1][o.slot[at]] += v
		}
	}
	for k := n - 1; k >= 0; k-- {
		copy(o.suffix[k], o.suffix[k+1])
		for at, v := range sums[k] {
			o.suffix[k][o.slot[at]] += v
		}
	}
	return o
}

// forCohort returns the other cohorts' summed time at each of times for cohort k.
// Values are rounded to a grid relative to their largest, so loads that are equal
// up to float noise compare equal (and rank as ties) rather than getting random ranks.
func (o *otherLoad) forCohort(k int, times []int64) []float64 {
	out := make([]float64, len(times))
	maxV := 1.0
	for i, at := range times {
		s := o.slot[at]
		out[i] = o.prefix[k][s] + o.suffix[k+1][s]
		maxV = math.Max(maxV, out[i])
	}
	grid := 1e-9 * maxV
	for i := range out {
		out[i] = math.Round(out[i]/grid) * grid
	}
	return out
}

// seriesLoadCorrelation correlates the load other traffic put on the system in
// each bucket with this series' per-bucket P95. Using the series' own summed
// time would be circular (it is count x mean latency), so otherSums holds the
// other cohorts' summed request time per bucket, aligned with points. Edge
// buckets that the window only partly covers are scaled to a rate over the
// covered part.
func seriesLoadCorrelation(points []Point, otherSums []float64, q Query) *float64 {
	buckets := make([]Bucket, len(points))
	p95 := make([]*float64, len(points))
	for i, p := range points {
		buckets[i] = Bucket{At: p.At, Count: p.Count, SumMs: otherSums[i] * bucketLoadScale(p.At, q.BucketSeconds, q.Start, q.End)}
		if p.P95 != nil {
			p95[i] = p.P95.ValueMs
		}
	}
	return SpearmanLoad(EstimateConcurrency(buckets, q.BucketSeconds), p95)
}

// bucketLoadScale is bucket width divided by the part of the bucket inside
// [start, end). Requests only exist inside the window, so a partial bucket's
// summed time spans less than a full bucket width. It is 1 for full buckets and
// for buckets that do not overlap the window at all.
func bucketLoadScale(at time.Time, bucketSeconds int, start, end time.Time) float64 {
	width := time.Duration(bucketSeconds) * time.Second
	lo, hi := at, at.Add(width)
	if start.After(lo) {
		lo = start
	}
	if end.Before(hi) {
		hi = end
	}
	overlap := hi.Sub(lo)
	if overlap <= 0 || width <= 0 {
		return 1
	}
	return float64(width) / float64(overlap)
}

// windowMinutes is the length of the current window in minutes.
func windowMinutes(start, end time.Time) float64 {
	return end.Sub(start).Minutes()
}
