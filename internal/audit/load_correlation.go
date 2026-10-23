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
}

// buildRollups folds the current-window outcome-split cohorts into one row per
// (controller, action), sorted by name. windowMinutes is the length of the
// current window.
func buildRollups(cohorts map[string]*cohortAccumulator, windowMinutes float64) []Rollup {
	type name struct{ controller, action string }
	byName := map[name]*Rollup{}
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
		}
	}
	rows := make([]Rollup, 0, len(byName))
	for _, r := range byName {
		if r.Requests > 0 {
			rate := float64(r.Failures) / float64(r.Requests)
			r.ErrorRate = &rate
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
		mx += rx[i] / n
		my += ry[i] / n
	}
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

// seriesLoadCorrelation correlates a cohort's per-bucket estimated concurrency
// with its per-bucket P95. points and sums are aligned and ordered by time.
func seriesLoadCorrelation(points []Point, sums []float64, bucketSeconds int) *float64 {
	buckets := make([]Bucket, len(points))
	p95 := make([]*float64, len(points))
	for i, p := range points {
		buckets[i] = Bucket{At: p.At, Count: p.Count, SumMs: sums[i]}
		if p.P95 != nil {
			p95[i] = p.P95.ValueMs
		}
	}
	return SpearmanLoad(EstimateConcurrency(buckets, bucketSeconds), p95)
}

// windowMinutes is the length of the current window in minutes.
func windowMinutes(start, end time.Time) float64 {
	return end.Sub(start).Minutes()
}
