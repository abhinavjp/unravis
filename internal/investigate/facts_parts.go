package investigate

import (
	"fmt"
	"sort"
	"time"

	"api-audit-explorer/internal/audit"
)

func (c *call) a() *float64  { return c.r.A }
func (c *call) cc() *float64 { return c.r.C }
func (c *call) pp() *float64 { return c.r.P }
func (c *call) oo() *float64 { return c.o }

func valuesOf(calls []*call, f func(*call) *float64) []float64 {
	var out []float64
	for _, c := range calls {
		if v := f(c); v != nil {
			out = append(out, *v)
		}
	}
	return out
}

// iisEnd is the end of an IIS row; the importer derives it, and a row without one
// ends time-taken after it starts.
func iisEnd(r IISRow) time.Time {
	if !r.End.IsZero() {
		return r.End.UTC()
	}
	return r.Start.UTC().Add(time.Duration(r.TakenMs * float64(time.Millisecond)))
}

// iisSplit matches IIS rows to the audited calls and computes O = time-taken - D -
// P (A21). The match is by end time (the audit call ends at CompletedAt, then P of
// payload capture, and IIS logs the row at the end of the response), on the same
// path, one row to one call, nearest first. Matching by start fails when O is
// large, because the IIS clock starts before the audit one does.
func (p *prep) iisSplit() {
	pt := &p.facts.Parts
	switch {
	case !p.in.IISPresent:
		pt.OWhy = "There is no IIS source in this case, so outside time cannot be tested."
		return
	case len(p.in.IIS) == 0:
		pt.OWhy = "The IIS source holds no rows for these windows, so outside time cannot be tested."
		return
	}
	type row struct {
		end   time.Time
		taken float64
		used  bool
	}
	byPath := map[string][]*row{}
	for _, r := range p.in.IIS {
		pa := NormalizePath(r.Path)
		byPath[pa] = append(byPath[pa], &row{end: iisEnd(r), taken: r.TakenMs})
	}
	for _, l := range byPath {
		sort.SliceStable(l, func(i, j int) bool { return l[i].end.Before(l[j].end) })
	}
	calls := make([]*call, 0, len(p.inc)+len(p.ref))
	calls = append(calls, p.inc...)
	calls = append(calls, p.ref...)
	expected := func(c *call) time.Time {
		t := c.end
		if c.r.P != nil {
			t = t.Add(time.Duration(*c.r.P * float64(time.Millisecond)))
		}
		return t
	}
	sort.SliceStable(calls, func(i, j int) bool { return expected(calls[i]).Before(expected(calls[j])) })
	tol := time.Duration(p.th.IISMatchToleranceMs * float64(time.Millisecond))
	matched, clamped := 0, 0
	var offsets []time.Duration
	for _, c := range calls {
		if c.r.Path == "" {
			continue
		}
		l := byPath[NormalizePath(c.r.Path)]
		if len(l) == 0 {
			continue
		}
		target := expected(c)
		lo := sort.Search(len(l), func(i int) bool { return !l[i].end.Before(target.Add(-tol)) })
		var best *row
		var bestGap time.Duration
		for i := lo; i < len(l) && !l[i].end.After(target.Add(tol)); i++ {
			if l[i].used {
				continue
			}
			g := l[i].end.Sub(target)
			if g < 0 {
				g = -g
			}
			if best == nil || g < bestGap {
				best, bestGap = l[i], g
			}
		}
		if best == nil {
			continue
		}
		best.used = true
		matched++
		offsets = append(offsets, best.end.Sub(target))
		pv := 0.0
		if c.r.P != nil {
			pv = *c.r.P
		}
		o := best.taken - c.d - pv
		if o < 0 {
			o = 0
			clamped++
		}
		c.o = &o
	}
	pt.IISMatched = matched
	pt.OClamped = clamped
	if len(calls) == 0 {
		pt.OWhy = "There are no audited calls to match against the IIS rows."
		return
	}
	rate := float64(matched) / float64(len(calls))
	pt.IISMatchRate = ptr(rate)
	if rate < p.th.IISMatchMinRate-floatEps {
		for _, c := range calls {
			c.o = nil
		}
		pt.IISMatched = 0
		pt.OWhy = fmt.Sprintf("Only %d of %d audited calls matched an IIS row, too few to show outside time.", matched, len(calls))
		return
	}
	// A steady clock difference between the two hosts shows as matched pairs whose end
	// times differ by about the same amount. Pairs that scatter across the whole
	// tolerance are neighbours of the same route, not the same call, so outside time
	// from them would be noise.
	if len(offsets) == 0 {
		pt.OWhy = "No audited call matched an IIS row, so outside time is not shown."
		return
	}
	sort.Slice(offsets, func(i, j int) bool { return offsets[i] < offsets[j] })
	med := offsets[len(offsets)/2]
	pt.IISOffsetMs = ptr(float64(med) / float64(time.Millisecond))
	near := 0
	clock := time.Duration(p.th.ClockToleranceMs * float64(time.Millisecond))
	for _, o := range offsets {
		d := o - med
		if d < 0 {
			d = -d
		}
		if d <= clock {
			near++
		}
	}
	if float64(near)/float64(len(offsets)) < p.th.IISOffsetClusterShare-floatEps {
		for _, c := range calls {
			c.o = nil
		}
		pt.IISMatched = 0
		pt.OWhy = "The IIS rows do not line up with the audited calls at one steady clock difference, so they may not be the same calls and outside time is not shown."
		return
	}
	pt.OTested = true
}

// parts is Step 3: how the extra time splits across A, C, P and O.
func (p *prep) parts() {
	pt := &p.facts.Parts
	both, total := 0, 0
	for _, set := range [][]*call{p.inc, p.ref} {
		for _, c := range set {
			total++
			if c.r.A != nil && c.r.C != nil {
				both++
			}
		}
	}
	if total > 0 {
		pt.Coverage = ptr(float64(both) / float64(total))
	}
	defs := []struct {
		name string
		f    func(*call) *float64
	}{{"A", (*call).a}, {"C", (*call).cc}, {"P", (*call).pp}, {"O", (*call).oo}}
	var positive float64
	for _, d := range defs {
		iv, rv := valuesOf(p.inc, d.f), valuesOf(p.ref, d.f)
		it := PartFacts{Name: d.name, IncN: len(iv), RefN: len(rv)}
		if m, ok := mean(iv); ok {
			it.IncMeanMs = ptr(m)
		}
		if m, ok := mean(rv); ok {
			it.RefMeanMs = ptr(m)
		}
		it.Tested = len(iv) > 0 && len(rv) > 0
		it.Tail = compareMs(nil, nil, p.th)
		if len(iv) >= p.th.GroupSizeP95 && len(rv) >= p.th.GroupSizeP95 {
			it.Tail = compareMs(summarize(rv, p.th).P95, summarize(iv, p.th).P95, p.th)
		}
		if d.name == "O" && !pt.OTested {
			it.Tested = false
			it.Why = pt.OWhy
		} else if !it.Tested {
			it.Why = "The audit documents lack this part in one of the windows."
		}
		if it.Tested {
			it.Change = compareMs(it.RefMeanMs, it.IncMeanMs, p.th)
			ex := float64(len(iv)) * (*it.IncMeanMs - *it.RefMeanMs)
			it.ExcessMs = ptr(ex)
			if ex > 0 {
				positive += ex
			}
		} else {
			it.Change = compareMs(nil, nil, p.th)
		}
		pt.Items = append(pt.Items, it)
	}
	if positive > 0 {
		for i := range pt.Items {
			it := &pt.Items[i]
			if it.Tested && it.ExcessMs != nil && *it.ExcessMs > 0 {
				it.Share = ptr(*it.ExcessMs / positive)
			}
		}
	}
	pt.Dominant = ""
	tested := 0
	for _, it := range pt.Items {
		if it.Tested {
			tested++
		}
	}
	switch {
	case tested == 0:
		// No part has data in both windows: say nothing about which part took the time.
		pt.DominantWhy = ""
	case positive <= 0:
		pt.DominantWhy = "No part took more time in the incident window than in the reference window."
	default:
		qualifying := 0
		for _, it := range pt.Items {
			if it.Share != nil && *it.Share >= p.th.DominantMinShare-floatEps && it.Change.Meaningful && it.Change.Direction == "up" {
				qualifying++
				pt.Dominant = it.Name
				pt.DominantWhy = "This part holds at least half of the extra time and rose by the change mark."
			}
		}
		if qualifying > 1 {
			// An exact split between two parts: neither is the dominant one.
			pt.Dominant = ""
			pt.DominantWhy = "Two parts each hold half of the extra time, so no single part is dominant."
		}
		if pt.Dominant == "" && qualifying == 0 {
			pt.DominantWhy = "No single part holds half of the extra time with a change of the size the mark needs."
			// One part holds half or more but its change is under the mark: name it, with the qualifier.
			// The share is a ratio like the Step 3 findings (no percent sign in generated text).
			over := 0
			var big PartFacts
			for _, it := range pt.Items {
				if it.Share != nil && *it.Share >= p.th.DominantMinShare-floatEps {
					over++
					big = it
				}
			}
			if over == 1 {
				pt.DominantWhy = fmt.Sprintf("Part %s holds %.2f of the extra time, which is under the change mark.", big.Name, *big.Share)
			}
		}
	}
	if tested > 0 && !pt.OTested {
		if pt.DominantWhy != "" {
			pt.DominantWhy += " "
		}
		pt.DominantWhy += "Outside time was not tested, so the shares cover A, C and P only."
	}
}

func maxPeak(bins []*bin) int {
	m := 0
	for _, b := range bins {
		if b.peak > m {
			m = b.peak
		}
	}
	return m
}

func (p *prep) meanL(bins []*bin) *float64 {
	var area float64
	n := 0
	for _, b := range bins {
		if b.gap == "" {
			area += b.area
			n++
		}
	}
	if n == 0 {
		return nil
	}
	return ptr(area / (float64(n) * 60))
}

func dirOfChange(c Change) string {
	if !c.Known {
		return "unknown"
	}
	return c.Direction
}

// flatTop finds a stretch of at least FlatTopMinutes testable minutes whose
// in-flight peak is at least FlatTopMinLevel and stays within FlatTopTolerance of
// one level. It returns the highest such level.
func (p *prep) flatTop(bins []*bin) (int, bool) {
	best := 0
	for i := range bins {
		lo, hi := 1<<30, 0
		for j := i; j < len(bins); j++ {
			b := bins[j]
			if b.gap != "" || b.peak < p.th.FlatTopMinLevel {
				break
			}
			if b.peak < lo {
				lo = b.peak
			}
			if b.peak > hi {
				hi = b.peak
			}
			if float64(hi-lo) > p.th.FlatTopTolerance*float64(hi)+floatEps {
				break
			}
			if j-i+1 >= p.th.FlatTopMinutes && hi > best {
				best = hi
			}
		}
	}
	return best, best > 0
}

// load is Step 4: Little's Law, L = lambda x W.
func (p *prep) load() {
	l := &p.facts.Load
	l.PlanningPerSec = p.th.ArrivalRatePerSec
	l.IncPerSec, l.RefPerSec = p.facts.Incident.PerSec, p.facts.Ref.PerSec
	l.IncWms, l.RefWms = p.facts.Incident.MeanMs, p.facts.Ref.MeanMs
	l.IncL, l.RefL = p.meanL(p.incBins), p.meanL(p.refBins)
	l.IncPeak, l.RefPeak = maxPeak(p.incBins), maxPeak(p.refBins)
	rd := func(ref, inc *float64) string {
		if ref == nil || inc == nil {
			return "unknown"
		}
		d, _ := compareRate(*ref, *inc, true, true, p.th)
		return d
	}
	l.LambdaDx = rd(l.RefPerSec, l.IncPerSec)
	l.WDx = dirOfChange(compareMs(l.RefWms, l.IncWms, p.th))
	l.LDx = rd(l.RefL, l.IncL)
	if l.IncWms != nil {
		l.PlannedL = ptr(p.th.ArrivalRatePerSec * *l.IncWms / 1000)
		if p.in.PoolLimit != nil && *p.in.PoolLimit > 0 {
			lim := *p.in.PoolLimit
			l.PoolLimit = &lim
			l.PlannedFill = ptr(*l.PlannedL / float64(lim))
		}
	}
	// A flat top is a capacity sign only when it is new: higher than the reference
	// peak by the load mark, or within PoolFullShare of the entered limit. A plateau
	// that the reference has too is just the normal level of traffic.
	l.FlatTopAt, l.FlatTop = p.flatTop(p.incBins)
	if l.FlatTop {
		newTop := false
		if l.RefPeak > 0 {
			d, _ := compareRate(float64(l.RefPeak), float64(l.FlatTopAt), true, true, p.th)
			newTop = d == "up"
		}
		nearLimit := l.PoolLimit != nil && float64(l.FlatTopAt) >= p.th.PoolFullShare*float64(*l.PoolLimit)-floatEps
		if !newTop && !nearLimit {
			l.FlatTop, l.FlatTopAt = false, 0
		}
	}
	switch {
	case l.LambdaDx == "unknown" || l.WDx == "unknown" || l.LDx == "unknown":
		l.Reading = "cannot read"
	case l.LambdaDx == "up" && l.WDx == "up" && l.LDx == "up":
		l.Reading = "more work arrived"
	case l.LambdaDx == "flat" && l.WDx == "up" && l.LDx == "up":
		l.Reading = "calls got slower"
	case l.LambdaDx == "down" && l.WDx == "up" && (l.LDx == "up" || l.LDx == "flat"):
		l.Reading = "calls stalled"
	case l.FlatTop:
		l.Reading = "a pool or cap looks full"
	default:
		l.Reading = "no clear reading"
	}
}

type retryKey struct{ client, path string }

func (p *prep) retriedCount(calls []*call) (retried, known int) {
	groups := map[retryKey][]time.Time{}
	for _, c := range calls {
		if c.r.Client == "" || c.r.Path == "" {
			continue
		}
		known++
		k := retryKey{c.r.Client, c.r.Path}
		groups[k] = append(groups[k], c.start)
	}
	win := time.Duration(p.th.RetryWindowSeconds) * time.Second
	for _, ts := range groups {
		sort.Slice(ts, func(i, j int) bool { return ts[i].Before(ts[j]) })
		for i := 1; i < len(ts); i++ {
			if ts[i].Sub(ts[i-1]) <= win {
				retried++
			}
		}
	}
	return retried, known
}

func (p *prep) retries() {
	r := &p.facts.Retries
	ir, ik := p.retriedCount(p.inc)
	rr, rk := p.retriedCount(p.ref)
	r.IncRetried, r.RefRetried = ir, rr
	r.Direction = "unknown"
	if ik == 0 || rk == 0 {
		return
	}
	r.Known = true
	is, rs := float64(ir)/float64(ik), float64(rr)/float64(rk)
	r.IncShare, r.RefShare = ptr(is), ptr(rs)
	dir, _ := compareRate(rs, is, true, true, p.th)
	if dir == "up" && (ir < p.th.RetryMinCalls || is < p.th.RetryMinShare) {
		dir = "flat"
	}
	r.Direction = dir
}

func (p *prep) errors() {
	e := &p.facts.Errors
	for _, set := range [][]*call{p.inc, p.ref} {
		for _, c := range set {
			if c.r.Outcome != "" {
				e.Known = true
			}
		}
	}
	for _, c := range p.inc {
		if c.r.Outcome == audit.StatusServerError {
			e.IncServer++
			if e.FirstIncServer == nil || c.start.Before(*e.FirstIncServer) {
				t := c.start
				e.FirstIncServer = &t
			}
		}
	}
	for _, c := range p.ref {
		if c.r.Outcome == audit.StatusServerError {
			e.RefServer++
		}
	}
}

// coverage is Q2: does the audit share of IIS requests dip in the incident window?
func (p *prep) coverage() {
	cv := &p.facts.Coverage
	cv.IISRows = len(p.in.IIS)
	cv.Dip = Unknown
	if !p.in.IISPresent || len(p.in.IIS) == 0 {
		return
	}
	seen := map[string]bool{}
	for i := range p.in.Requests {
		if p.in.Requests[i].Path != "" {
			seen[p.in.Requests[i].Path] = true
		}
	}
	share := func(w Window) *float64 {
		if !w.Valid() {
			return nil
		}
		rows := 0
		for _, r := range p.in.IIS {
			if w.Contains(r.Start) && seen[NormalizePath(r.Path)] {
				rows++
			}
		}
		if rows < p.th.GroupSizeP95 {
			return nil
		}
		aud := 0
		for i := range p.in.Requests {
			r := &p.in.Requests[i]
			if !r.Start.IsZero() && w.Contains(r.Start) {
				aud++
			}
		}
		return ptr(float64(aud) / float64(rows))
	}
	cv.RefShare, cv.IncShare = share(p.in.Reference), share(p.in.Incident)
	if cv.RefShare == nil || cv.IncShare == nil || *cv.RefShare <= 0 {
		return
	}
	if (*cv.RefShare-*cv.IncShare) / *cv.RefShare >= p.th.AuditDipShare-floatEps {
		cv.Dip = Yes
	} else {
		cv.Dip = No
	}
}

// metricOnset dates one tier: the first minute whose median of the metric stays
// above OnsetFactor times the reference median (and above it by ChangeMinMs) for
// OnsetRunMinutes minutes.
// readAll reports whether every minute of the incident window was read: no gap
// and no minute too thin to read. Only then does "did not move" mean the part
// held flat; otherwise a move may have been hidden.
func readAll(bins []*bin, thin func(*bin) bool) bool {
	for _, b := range bins {
		if b.gap != "" || thin(b) {
			return false
		}
	}
	return true
}

// metricOnset dates when a part first stayed above its reference. The third
// result is true when the part's own data was read well enough to say it held
// flat if it did not move.
func (p *prep) metricOnset(f func(*call) *float64) (*time.Time, string, bool) {
	rv := valuesOf(p.ref, f)
	if len(rv) < p.th.GroupSizeP95 {
		return nil, "The reference window holds too few readings of this part to set a baseline.", false
	}
	refMed, _ := median(rv)
	idx, ok := findRun(p.incBins, p.th.OnsetRunMinutes, func(b *bin) minStatus {
		v := valuesOf(b.calls, f)
		if len(v) < p.th.OnsetMinSamples {
			return stSkip
		}
		m, _ := median(v)
		if m > p.th.OnsetFactor*refMed && m-refMed >= p.th.ChangeMinMs-floatEps {
			return stHit
		}
		return stMiss
	})
	if !ok {
		full := readAll(p.incBins, func(b *bin) bool { return len(valuesOf(b.calls, f)) < p.th.OnsetMinSamples })
		return nil, "This part did not stay above its reference for the run of minutes the rule needs.", full
	}
	t := p.incBins[idx].at
	return &t, "This part stayed above its reference for the run of minutes the rule needs.", true
}

func (p *prep) loadOnset() (*time.Time, string, bool) {
	if len(p.refBins) == 0 {
		return nil, "There is no reference window.", false
	}
	counts := make([]float64, 0, len(p.refBins))
	for _, b := range p.refBins {
		if b.gap == "" {
			counts = append(counts, float64(b.n()))
		}
	}
	refMed, ok := median(counts)
	if !ok || refMed < float64(p.th.GapRefMinPerMinute) {
		return nil, "The reference window holds too few calls per minute to read a change in arrivals.", false
	}
	factor := 1 + p.th.LoadChangePercent/100
	idx, found := findRun(p.incBins, p.th.OnsetRunMinutes, func(b *bin) minStatus {
		if float64(b.n()) >= factor*refMed-floatEps {
			return stHit
		}
		return stMiss
	})
	if !found {
		full := readAll(p.incBins, func(*bin) bool { return false })
		return nil, "Arrivals did not stay above the reference for the run of minutes the rule needs.", full
	}
	t := p.incBins[idx].at
	return &t, "Arrivals stayed above the reference for the run of minutes the rule needs.", true
}

func (p *prep) tiers() {
	add := func(tier string, at *time.Time, why string, tested bool) {
		p.facts.Tiers = append(p.facts.Tiers, TierOnset{Tier: tier, At: at, Why: why, Tested: tested})
	}
	t, w, ok := p.loadOnset()
	add("load", t, w, ok)
	t, w, ok = p.metricOnset((*call).a)
	add("a", t, w, ok)
	t, w, ok = p.metricOnset((*call).cc)
	add("c", t, w, ok)
	if p.facts.Parts.OTested {
		t, w, ok = p.metricOnset((*call).oo)
		add("o", t, w, ok)
	} else {
		add("o", nil, p.facts.Parts.OWhy, false)
	}
	names := make([]string, 0, len(p.in.LaneOnsets))
	for k := range p.in.LaneOnsets {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		t := p.in.LaneOnsets[k].UTC()
		add(k, &t, "From the "+k+" lane's own data.", true)
	}
}

func (p *prep) series() {
	var out []Minute
	for _, bs := range [][]*bin{p.refBins, p.incBins} {
		label := "reference"
		if len(bs) > 0 && bs[0].win == winInc {
			label = "incident"
		}
		for _, b := range bs {
			m := Minute{At: b.at, Window: label, N: b.n(), Peak: b.peak, Gap: b.gap, InFlight: round2(b.area / 60)}
			if d := durations(b.calls); len(d) > 0 {
				s := sortedCopy(d)
				m.P50 = ptr(nearestRank(s, 0.5))
				if len(s) >= p.th.OnsetMinSamples {
					m.P95 = ptr(nearestRank(s, 0.95))
				}
			}
			for _, k := range []struct {
				dst *(*float64)
				f   func(*call) *float64
			}{{&m.A, (*call).a}, {&m.C, (*call).cc}, {&m.O, (*call).oo}} {
				if v, ok := median(valuesOf(b.calls, k.f)); ok {
					*k.dst = ptr(v)
				}
			}
			out = append(out, m)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	p.facts.Series = out
}
