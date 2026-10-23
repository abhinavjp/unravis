package audit

import (
	"math"
	"sort"
)

const (
	// minShiftSamples matches the minimum sample count for a compared cohort.
	minShiftSamples = 20
	// significantZ is the |z| at which a histogram shift counts as significant.
	significantZ = 3.0
)

// HistogramShiftZ is a Mann-Whitney U z-score computed from binned counts. It
// compares the current window against the reference window; positive means the
// current window is slower. Every observation inside one bin is treated as
// tied, bins are ordered by index, and the variance carries the tie correction.
// It returns nil when either window has fewer than 20 valid samples or when
// there is no variance to normalize by (for example, everything in one bin).
func HistogramShiftZ(current, reference Stats) *float64 {
	if current.Count < minShiftSamples || reference.Count < minShiftSamples {
		return nil
	}
	type pair struct{ c, r int64 }
	byBin := make(map[int]pair, len(current.Histogram)+len(reference.Histogram))
	var n1, n2 float64
	for _, b := range current.Histogram {
		p := byBin[b.Bin]
		p.c += b.Count
		byBin[b.Bin] = p
		n1 += float64(b.Count)
	}
	for _, b := range reference.Histogram {
		p := byBin[b.Bin]
		p.r += b.Count
		byBin[b.Bin] = p
		n2 += float64(b.Count)
	}
	if n1 < minShiftSamples || n2 < minShiftSamples {
		return nil
	}
	bins := make([]int, 0, len(byBin))
	for bin := range byBin {
		bins = append(bins, bin)
	}
	sort.Ints(bins)

	var rankSum, below, tieTerm float64
	for _, bin := range bins {
		p := byBin[bin]
		c, t := float64(p.c), float64(p.c+p.r)
		// Tied observations share the mean of the ranks the bin occupies.
		rankSum += c * (below + (t+1)/2)
		tieTerm += t*t*t - t
		below += t
	}
	n := n1 + n2
	u1 := rankSum - n1*(n1+1)/2
	mean := n1 * n2 / 2
	variance := n1 * n2 / 12 * ((n + 1) - tieTerm/(n*(n-1)))
	if !(variance > 0) {
		return nil
	}
	z := (u1 - mean) / math.Sqrt(variance)
	if math.IsNaN(z) || math.IsInf(z, 0) {
		return nil
	}
	return &z
}

// applyImpact fills the impact and significance fields of a comparison whose
// State, Current, Reference and MedianDeltaMs are already set.
//
// TotalTimeMs is the current mean times the current count; both come from the
// exact per-bucket sums, so it is not a bin-midpoint approximation. ImpactMs is
// the extra time spent versus the reference mean across the current requests.
func applyImpact(c *Comparison) {
	if c.Current.MeanMs != nil {
		c.TotalTimeMs = *c.Current.MeanMs * float64(c.Current.Count)
	}
	if c.State != "compared" {
		return
	}
	if c.Current.MeanMs != nil && c.Reference.MeanMs != nil {
		impact := (*c.Current.MeanMs - *c.Reference.MeanMs) * float64(c.Current.Count)
		if !math.IsNaN(impact) && !math.IsInf(impact, 0) {
			c.ImpactMs = &impact
		}
	}
	c.ShiftZ = HistogramShiftZ(c.Current, c.Reference)
	c.Significant = c.ShiftZ != nil && math.Abs(*c.ShiftZ) >= significantZ && medianShiftExceedsBin(c)
}

// medianShiftExceedsBin reports whether the P50 moved by more than the width of
// the reference P50's histogram bin, the resolution below which a shift cannot
// be told apart from binning.
func medianShiftExceedsBin(c *Comparison) bool {
	if c.MedianDeltaMs == nil || c.Reference.P50 == nil || c.Reference.P50.ValueMs == nil {
		return false
	}
	lower, upper := BinBounds(BinIndex(*c.Reference.P50.ValueMs))
	if upper == nil {
		return false
	}
	return math.Abs(*c.MedianDeltaMs) > *upper-lower
}
