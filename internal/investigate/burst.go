package investigate

import (
	"sort"
	"strings"
	"time"
)

// BurstCount is a row count for the incident and reference windows, the
// most rows in any 5 minutes of the incident (Peak5), and the window lengths
// in minutes, so rates can be compared.
type BurstCount struct {
	Inc, Ref, Peak5 int
	IncMin, RefMin  float64
}

// IncRate is incident rows per minute; zero when the window is empty.
func (b BurstCount) IncRate() float64 {
	if b.IncMin <= 0 {
		return 0
	}
	return float64(b.Inc) / b.IncMin
}

// RefRate is reference rows per minute; zero when the window is empty.
func (b BurstCount) RefRate() float64 {
	if b.RefMin <= 0 {
		return 0
	}
	return float64(b.Ref) / b.RefMin
}

// CountBurst counts ts in each window. Peak5 slides a 5-minute span over the
// incident rows (two pointers over the sorted incident times).
func CountBurst(ts []time.Time, inc, ref Window) BurstCount {
	var b BurstCount
	if inc.Valid() {
		b.IncMin = inc.End.Sub(inc.Start).Minutes()
	}
	if ref.Valid() {
		b.RefMin = ref.End.Sub(ref.Start).Minutes()
	}
	var in []time.Time
	for _, t := range ts {
		if inc.Contains(t) {
			in = append(in, t)
		} else if ref.Contains(t) {
			b.Ref++
		}
	}
	b.Inc = len(in)
	sort.Slice(in, func(i, j int) bool { return in[i].Before(in[j]) })
	i := 0
	for j := range in {
		for in[j].Sub(in[i]) >= 5*time.Minute {
			i++
		}
		if n := j - i + 1; n > b.Peak5 {
			b.Peak5 = n
		}
	}
	return b
}

// httpErrTimes returns the times of HTTPERR rows whose reason is one of reasons
// (case-insensitive).
func httpErrTimes(in Input, reasons ...string) []time.Time {
	var out []time.Time
	for _, r := range in.HTTPErr {
		for _, want := range reasons {
			if strings.EqualFold(r.Reason, want) {
				out = append(out, r.T)
				break
			}
		}
	}
	return out
}

// sqlTimes returns the times of SQL event rows of the given kind.
func sqlTimes(in Input, kind string) []time.Time {
	var out []time.Time
	for _, e := range in.SQLEvents {
		if e.Kind == kind {
			out = append(out, e.T)
		}
	}
	return out
}
