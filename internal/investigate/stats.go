package investigate

import (
	"math"
	"sort"
)

// floatEps is the tolerance for comparing sums and ratios of milliseconds (guard
// 14: never compare floats with !=).
const floatEps = 1e-9

func approxEqual(a, b float64) bool {
	return math.Abs(a-b) <= floatEps*math.Max(1, math.Max(math.Abs(a), math.Abs(b)))
}

// round2 rounds to two decimals so a result serializes the same on every run.
func round2(v float64) float64 { return math.Round(v*100) / 100 }

func ptr(v float64) *float64 { r := round2(v); return &r }

// nearestRank is the value at rank ceil(q*n) of an ascending slice (the same
// definition the audit analysis prints). The slice must be sorted and non-empty.
func nearestRank(sorted []float64, q float64) float64 {
	n := len(sorted)
	rank := int(math.Ceil(q*float64(n) - 1e-9))
	if rank < 1 {
		rank = 1
	}
	if rank > n {
		rank = n
	}
	return sorted[rank-1]
}

func sortedCopy(v []float64) []float64 {
	out := append([]float64(nil), v...)
	sort.Float64s(out)
	return out
}

func mean(v []float64) (float64, bool) {
	if len(v) == 0 {
		return 0, false
	}
	var s float64
	for _, x := range v {
		s += x
	}
	return s / float64(len(v)), true
}

func median(v []float64) (float64, bool) {
	if len(v) == 0 {
		return 0, false
	}
	s := sortedCopy(v)
	n := len(s)
	if n%2 == 1 {
		return s[n/2], true
	}
	return (s[n/2-1] + s[n/2]) / 2, true
}

// Percentiles is the guarded summary of one group of durations (guard 15). P50
// needs one call. P95 needs GroupSizeP95 calls and P99 needs GroupSizeP99, else
// they are nil and only the count and the maximum are shown.
type Percentiles struct {
	N   int      `json:"n"`
	P50 *float64 `json:"p50,omitempty"`
	P95 *float64 `json:"p95,omitempty"`
	P99 *float64 `json:"p99,omitempty"`
	Max *float64 `json:"max,omitempty"`
}

func summarize(values []float64, th Thresholds) Percentiles {
	p := Percentiles{N: len(values)}
	if len(values) == 0 {
		return p
	}
	s := sortedCopy(values)
	p.P50 = ptr(nearestRank(s, 0.5))
	if len(s) >= th.GroupSizeP95 {
		p.P95 = ptr(nearestRank(s, 0.95))
	}
	if len(s) >= th.GroupSizeP99 {
		p.P99 = ptr(nearestRank(s, 0.99))
	}
	p.Max = ptr(s[len(s)-1])
	return p
}

// Change is the A23 comparison of one value against its reference: the change is
// meaningful only when it is at least ChangeMinPercent AND ChangeMinMs. Known is
// false when either side is missing; a missing side is never "no change".
type Change struct {
	Known       bool     `json:"known"`
	RefMs       *float64 `json:"refMs,omitempty"`
	IncMs       *float64 `json:"incMs,omitempty"`
	DeltaMs     *float64 `json:"deltaMs,omitempty"`
	DeltaPct    *float64 `json:"deltaPct,omitempty"`
	Meaningful  bool     `json:"meaningful"`
	Direction   string   `json:"direction"` // "up", "down" or "flat" (below the mark)
	MarkPercent float64  `json:"markPercent"`
	MarkMs      float64  `json:"markMs"`
}

func compareMs(ref, inc *float64, th Thresholds) Change {
	c := Change{MarkPercent: th.ChangeMinPercent, MarkMs: th.ChangeMinMs, Direction: "flat"}
	if ref == nil || inc == nil {
		return c
	}
	c.Known = true
	c.RefMs, c.IncMs = ptr(*ref), ptr(*inc)
	d := *inc - *ref
	c.DeltaMs = ptr(d)
	if *ref > 0 {
		c.DeltaPct = ptr(d / *ref * 100)
	}
	pctOK := (*ref > 0 && math.Abs(d) / *ref * 100 >= th.ChangeMinPercent-floatEps) || (*ref <= 0 && math.Abs(d) > 0)
	msOK := math.Abs(d) >= th.ChangeMinMs-floatEps
	if pctOK && msOK {
		c.Meaningful = true
		if d > 0 {
			c.Direction = "up"
		} else {
			c.Direction = "down"
		}
	}
	return c
}

// compareRate compares two rates or counts by the percentage half of A23 only (a
// count has no milliseconds). Both values must be known.
func compareRate(ref, inc float64, refKnown, incKnown bool, th Thresholds) (dir string, known bool) {
	if !refKnown || !incKnown {
		return "unknown", false
	}
	if ref <= 0 {
		if inc > 0 {
			return "up", true
		}
		return "flat", true
	}
	pct := (inc - ref) / ref * 100
	switch {
	case pct >= th.LoadChangePercent-floatEps:
		return "up", true
	case pct <= -th.LoadChangePercent+floatEps:
		return "down", true
	}
	return "flat", true
}
