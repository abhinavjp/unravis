package server

import (
	"math"
	"sort"

	"api-audit-explorer/internal/audit"
)

// latencyAcc accumulates the latency bins of the wire slots that fall in one
// axis bucket: how many requests had a timing, the observed min and max, and
// the per-bin counts a quantile is read from. The zero value is ready to use.
type latencyAcc struct {
	bins     map[int]int64
	min, max float64
	n        int64
	// sum is the summed duration in milliseconds of the timed calls added so
	// far; hasSum says at least one timed bin was added.
	sum    float64
	hasSum bool
}

// add merges one slot's bins. A bin with no count or a negative index adds
// nothing; min and max track the observed range across the bins.
func (a *latencyAcc) add(bins []audit.RollupWireBin) {
	for _, b := range bins {
		if b.Count <= 0 || b.Bin < 0 {
			continue
		}
		if a.bins == nil {
			a.bins = map[int]int64{}
		}
		a.bins[b.Bin] += b.Count
		if a.n == 0 || b.Min < a.min {
			a.min = b.Min
		}
		if a.n == 0 || b.Max > a.max {
			a.max = b.Max
		}
		a.n += b.Count
		a.sum += b.Sum
		a.hasSum = true
	}
}

// busy is the summed duration in milliseconds of the timed calls, rounded to 3
// decimals like the other figures. It is nil when no timed call was added: a
// missing number is never a stand-in zero.
func (a *latencyAcc) busy() *float64 {
	if !a.hasSum {
		return nil
	}
	r := math.Round(a.sum*1000) / 1000
	return &r
}

// quantile is the nearest-rank quantile by bin, the rule dayHist.quantile
// uses: the midpoint of the bin that holds the rank, clipped to the observed
// range. The overflow bin has no upper edge, so its lower edge is returned.
func (a *latencyAcc) quantile(p float64) (float64, bool) {
	if a.n == 0 {
		return 0, false
	}
	bins := make([]int, 0, len(a.bins))
	for bin := range a.bins {
		bins = append(bins, bin)
	}
	sort.Ints(bins)
	rank := int64(math.Ceil(p * float64(a.n)))
	var cumulative int64
	for _, bin := range bins {
		cumulative += a.bins[bin]
		if cumulative < rank {
			continue
		}
		lower, upper := audit.BinBounds(bin)
		if lower < a.min {
			lower = a.min
		}
		if upper == nil {
			return lower, true
		}
		hi := *upper
		if hi > a.max {
			hi = a.max
		}
		if lower > hi {
			lower = hi
		}
		return lower + (hi-lower)/2, true
	}
	return 0, false
}

// figures are the per-bucket response times: how many requests had a timing,
// and the P50, P95 and slowest of them in milliseconds, rounded to 3 decimals
// so the JSON is stable. A bucket with no timed request has no figures.
func (a *latencyAcc) figures() (timed int64, p50, p95, max *float64) {
	if a.n == 0 {
		return 0, nil, nil, nil
	}
	round := func(v float64) *float64 {
		r := math.Round(v*1000) / 1000
		return &r
	}
	p50v, _ := a.quantile(0.50)
	p95v, _ := a.quantile(0.95)
	return a.n, round(p50v), round(p95v), round(a.max)
}
