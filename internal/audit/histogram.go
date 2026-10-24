package audit

import (
	"fmt"
	"math"
	"sort"
)

const overflowBin = 193

type histogramAccumulator struct {
	counts [overflowBin + 1]int64
	count  int64
	sum    float64
	min    float64
	max    float64
	// Phase totals and the number of documents that contributed to each.
	sumAudit, sumApi, sumPayload float64
	nAudit, nApi, nPayload       int64

	observedPartial int64
}

func BinIndex(ms float64) int {
	if math.IsNaN(ms) || math.IsInf(ms, 0) || ms < 0 {
		return -1
	}
	if ms < 1 {
		return 0
	}
	if ms >= math.Pow(1.1, 192) {
		return overflowBin
	}
	// The logarithm supplies a close candidate; strict boundary checks preserve
	// the half-open intervals at values such as exactly 1.1^n.
	bin := int(math.Log(ms)/math.Log(1.1)) + 1
	if bin < 1 {
		bin = 1
	}
	for bin > 1 && ms < math.Pow(1.1, float64(bin-1)) {
		bin--
	}
	for bin < overflowBin && ms >= math.Pow(1.1, float64(bin)) {
		bin++
	}
	return bin
}

func BinBounds(bin int) (lower float64, upper *float64) {
	if bin < 0 || bin > overflowBin {
		return math.NaN(), nil
	}
	if bin == 0 {
		value := 1.0
		return 0, &value
	}
	lower = math.Pow(1.1, float64(bin-1))
	if bin == overflowBin {
		return lower, nil
	}
	value := math.Pow(1.1, float64(bin))
	return lower, &value
}

func newHistogramAccumulator() *histogramAccumulator { return &histogramAccumulator{} }

func (h *histogramAccumulator) add(bin int, count int64, sum, min, max float64) error {
	if bin < 0 || bin > overflowBin {
		return fmt.Errorf("histogram bin %d is invalid", bin)
	}
	if count <= 0 {
		return fmt.Errorf("histogram count must be positive")
	}
	if math.IsNaN(sum) || math.IsInf(sum, 0) || sum < 0 {
		return fmt.Errorf("histogram sum must be finite and nonnegative")
	}
	if math.IsNaN(min) || math.IsInf(min, 0) || min < 0 || math.IsNaN(max) || math.IsInf(max, 0) || max < min {
		return fmt.Errorf("histogram range must be finite and nonnegative")
	}
	lower, upper := BinBounds(bin)
	if min < lower || (upper != nil && max >= *upper) {
		return fmt.Errorf("histogram range does not fit bin %d", bin)
	}
	if int64(math.MaxInt64-h.count) < count || int64(math.MaxInt64-h.counts[bin]) < count {
		return fmt.Errorf("histogram count overflow")
	}
	if math.MaxFloat64-h.sum < sum {
		return fmt.Errorf("histogram sum overflow")
	}
	h.count += count
	h.counts[bin] += count
	h.sum += sum
	if h.count == count || min < h.min {
		h.min = min
	}
	if h.count == count || max > h.max {
		h.max = max
	}
	return nil
}

// addBucket merges one valid bucket: its histogram bin and its phase totals.
func (h *histogramAccumulator) addBucket(b Bucket) error {
	if err := validPhases(b); err != nil {
		return err
	}
	if err := h.add(b.Bin, b.Count, b.SumMs, b.MinMs, b.MaxMs); err != nil {
		return err
	}
	h.sumAudit += b.SumAuditMs
	h.sumApi += b.SumApiMs
	h.sumPayload += b.SumPayloadMs
	h.nAudit += b.NAudit
	h.nApi += b.NApi
	h.nPayload += b.NPayload
	h.observedPartial += b.ObservedPartial
	return nil
}

func validPhases(b Bucket) error {
	if b.ObservedPartial < 0 || b.ObservedPartial > b.Count {
		return fmt.Errorf("observedPartial %d must be between 0 and the bucket count %d", b.ObservedPartial, b.Count)
	}
	for _, p := range []struct {
		name string
		sum  float64
		n    int64
	}{{"audit", b.SumAuditMs, b.NAudit}, {"api", b.SumApiMs, b.NApi}, {"payload", b.SumPayloadMs, b.NPayload}} {
		if math.IsNaN(p.sum) || math.IsInf(p.sum, 0) || p.sum < 0 {
			return fmt.Errorf("%s phase sum must be finite and nonnegative", p.name)
		}
		if p.n < 0 || p.n > b.Count {
			return fmt.Errorf("%s phase count %d must be between 0 and the bucket count %d", p.name, p.n, b.Count)
		}
		if p.n == 0 && p.sum != 0 {
			return fmt.Errorf("%s phase has a sum but no samples", p.name)
		}
	}
	return nil
}

// phaseMeans divides each phase total by that phase's own sample count, so a
// document missing one phase does not drag its mean toward zero.
func (h *histogramAccumulator) phaseMeans() *PhaseMeans {
	mean := func(sum float64, n int64) *float64 {
		if n == 0 {
			return nil
		}
		value := sum / float64(n)
		return &value
	}
	means := &PhaseMeans{AuditMs: mean(h.sumAudit, h.nAudit), ApiMs: mean(h.sumApi, h.nApi), PayloadMs: mean(h.sumPayload, h.nPayload)}
	if means.AuditMs == nil && means.ApiMs == nil && means.PayloadMs == nil {
		return nil
	}
	return means
}

func (h *histogramAccumulator) stats() Stats {
	stats := Stats{Count: h.count, Histogram: make([]BinCount, 0), ObservedPartial: h.observedPartial}
	if h.count == 0 {
		return stats
	}
	mean := h.sum / float64(h.count)
	min, max := h.min, h.max
	stats.MeanMs, stats.MinMs, stats.MaxMs = &mean, &min, &max
	stats.Phases = h.phaseMeans()
	for bin, count := range h.counts {
		if count > 0 {
			stats.Histogram = append(stats.Histogram, BinCount{Bin: bin, Count: count})
		}
	}
	sort.Slice(stats.Histogram, func(i, j int) bool { return stats.Histogram[i].Bin < stats.Histogram[j].Bin })
	stats.P50 = h.quantile(.50)
	stats.P90 = h.quantile(.90)
	stats.P95 = h.quantile(.95)
	// Below 100 samples the nearest-rank P99 is just the maximum, so report none.
	if h.count >= P99MinSampleCount {
		stats.P99 = h.quantile(.99)
	}
	return stats
}

func (h *histogramAccumulator) quantile(p float64) *Quantile {
	if h.count == 0 {
		return nil
	}
	rank := int64(math.Ceil(p * float64(h.count)))
	var cumulative int64
	for bin, count := range h.counts {
		cumulative += count
		if cumulative < rank {
			continue
		}
		lower, upper := BinBounds(bin)
		if lower < h.min {
			lower = h.min
		}
		if upper != nil {
			if *upper > h.max {
				upper = floatPointer(h.max)
			}
			if lower > *upper {
				lower = *upper
			}
			value := lower + (*upper-lower)/2
			return &Quantile{LowerMs: lower, UpperMs: upper, ValueMs: &value}
		}
		return &Quantile{LowerMs: lower}
	}
	return nil
}

func floatPointer(v float64) *float64 { return &v }
