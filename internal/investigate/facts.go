package investigate

import (
	"math"
	"sort"
	"time"
)

// Facts are computed, never judged (A27): counts, percentiles, onset, the time
// split, concurrency. Every number here is measured from the audit rows (and the
// IIS rows for O) with the thresholds that came with the run.
type Facts struct {
	Counts   Counts       `json:"counts"`
	Incident GroupFacts   `json:"incident"`
	Ref      GroupFacts   `json:"reference"`
	Routes   []RouteFacts `json:"routes"`
	Change   OverallShift `json:"change"`
	Onset    Onset        `json:"onset"`
	Shape    Shape        `json:"shape"`
	Scope    Scope        `json:"scope"`
	Parts    Parts        `json:"parts"`
	Load     Load         `json:"load"`
	Retries  Retries      `json:"retries"`
	Errors   Errors       `json:"errors"`
	Clock    ClockFacts   `json:"clock"`
	Coverage Coverage     `json:"coverage"`
	Gaps     []Span       `json:"gaps,omitempty"`
	Tiers    []TierOnset  `json:"tiers"`
	// Series is the per-minute data behind the charts. It is large, so a caller that
	// only wants the verdict can drop it.
	Series []Minute `json:"series,omitempty"`
}

// Counts says how many calls were read and what became of each.
type Counts struct {
	Read       int `json:"read"`
	NoStart    int `json:"noStart"`
	Incomplete int `json:"incomplete"`
	Anomalous  int `json:"anomalous"`
	Incident   int `json:"incident"`
	Reference  int `json:"reference"`
	Outside    int `json:"outside"`
	Used       int `json:"used"`
}

// GroupFacts summarises one window of the selected calls.
type GroupFacts struct {
	Percentiles
	MeanMs *float64 `json:"meanMs,omitempty"`
	// PerSec is the audited arrival rate over the minutes that can be tested (gap
	// minutes are left out, never counted as zero).
	PerSec          *float64 `json:"perSec,omitempty"`
	TestableMinutes int      `json:"testableMinutes"`
}

// RouteFacts is one API in both windows.
type RouteFacts struct {
	Route     string      `json:"route"`
	Incident  Percentiles `json:"incident"`
	Reference Percentiles `json:"reference"`
	P95Change Change      `json:"p95Change"`
}

// OverallShift is the A23 comparison of the whole selection.
type OverallShift struct {
	P50  Change `json:"p50"`
	P95  Change `json:"p95"`
	Mean Change `json:"mean"`
	// Known is false when neither the P95 nor the mean could be compared.
	Known bool `json:"known"`
	// Meaningful is the P95 verdict when the P95 is known, else the mean's.
	Meaningful bool `json:"meaningful"`
}

// Onset is t0 by rule A24. Status is "found", "not found" or "cannot date".
type Onset struct {
	Status string     `json:"status"`
	T0     *time.Time `json:"t0,omitempty"`
	// Basis says what was timed: "audit duration" (rule A24 as written) or
	// "outside time" (the audit durations did not move, but IIS time-taken outside
	// the audited span did).
	Basis       string   `json:"basis,omitempty"`
	RefP95Ms    *float64 `json:"refP95Ms,omitempty"`
	ThresholdMs *float64 `json:"thresholdMs,omitempty"`
	Why         string   `json:"why"`
}

// Shape is "step", "ramp", "periodic", "none" (nothing to shape) or "unknown".
type Shape struct {
	Kind          string `json:"kind"`
	PeriodMinutes int    `json:"periodMinutes,omitempty"`
	Why           string `json:"why"`
}

// Scope is how wide the change is: "one route", "a few routes", "many routes",
// "everything", "none" or "unknown".
type Scope struct {
	Kind     string   `json:"kind"`
	Testable int      `json:"testable"`
	Changed  int      `json:"changed"`
	Routes   []string `json:"routes,omitempty"`
	Why      string   `json:"why"`
}

// PartFacts is one of A, C, P, O across the two windows.
type PartFacts struct {
	Name      string   `json:"name"`
	IncN      int      `json:"incN"`
	RefN      int      `json:"refN"`
	IncMeanMs *float64 `json:"incMeanMs,omitempty"`
	RefMeanMs *float64 `json:"refMeanMs,omitempty"`
	Change    Change   `json:"change"`
	// Tail compares the part's P95 across the windows, so a slowdown in only some
	// calls shows even when the mean barely moves.
	Tail Change `json:"tail"`
	// ExcessMs is the incident total minus what the incident calls would have taken
	// at the reference mean. Share is the part's share of the positive excess.
	ExcessMs *float64 `json:"excessMs,omitempty"`
	Share    *float64 `json:"share,omitempty"`
	Tested   bool     `json:"tested"`
	Why      string   `json:"why,omitempty"`
}

// Parts is the Step 3 time split. Dominant is "A", "C", "P", "O" or "" (none).
type Parts struct {
	Items        []PartFacts `json:"items"`
	Coverage     *float64    `json:"coverage,omitempty"`
	Dominant     string      `json:"dominant"`
	DominantWhy  string      `json:"dominantWhy"`
	OTested      bool        `json:"oTested"`
	OWhy         string      `json:"oWhy"`
	IISMatched   int         `json:"iisMatched"`
	IISMatchRate *float64    `json:"iisMatchRate,omitempty"`
	// IISOffsetMs is the usual gap between an IIS end time and the matching audit end
	// time (the clock difference between the hosts, when the pairs line up).
	IISOffsetMs *float64 `json:"iisOffsetMs,omitempty"`
}

// Load is Step 4: Little's Law, L = lambda x W, from the audit documents.
type Load struct {
	// PlanningPerSec is the lambda of rule A22 (the whole API). The measured
	// audited rates sit next to it and never replace it.
	PlanningPerSec float64  `json:"planningPerSec"`
	IncPerSec      *float64 `json:"incPerSec,omitempty"`
	RefPerSec      *float64 `json:"refPerSec,omitempty"`
	IncWms         *float64 `json:"incWms,omitempty"`
	RefWms         *float64 `json:"refWms,omitempty"`
	// IncL and RefL are the mean requests in flight over the testable minutes, from
	// interval overlap of the audit documents.
	IncL     *float64 `json:"incL,omitempty"`
	RefL     *float64 `json:"refL,omitempty"`
	IncPeak  int      `json:"incPeak"`
	RefPeak  int      `json:"refPeak"`
	LambdaDx string   `json:"lambdaDirection"`
	WDx      string   `json:"wDirection"`
	LDx      string   `json:"lDirection"`
	// PlannedL is PlanningPerSec x the incident W in seconds. PoolLimit is what the
	// user entered; PlannedFill is PlannedL over PoolLimit.
	PlannedL    *float64 `json:"plannedL,omitempty"`
	PoolLimit   *int     `json:"poolLimit,omitempty"`
	PlannedFill *float64 `json:"plannedFill,omitempty"`
	FlatTop     bool     `json:"flatTop"` // a flat top that is new against the reference or near the limit
	FlatTopAt   int      `json:"flatTopAt,omitempty"`
	Reading     string   `json:"reading"`
}

// Retries counts calls the same client repeated on the same URL within the retry
// window.
type Retries struct {
	Known      bool     `json:"known"`
	IncRetried int      `json:"incRetried"`
	RefRetried int      `json:"refRetried"`
	IncShare   *float64 `json:"incShare,omitempty"`
	RefShare   *float64 `json:"refShare,omitempty"`
	Direction  string   `json:"direction"`
}

// Errors counts server errors (audit outcome "server_error").
type Errors struct {
	Known          bool       `json:"known"`
	IncServer      int        `json:"incServer"`
	RefServer      int        `json:"refServer"`
	FirstIncServer *time.Time `json:"firstIncServer,omitempty"`
}

// ClockFacts is the Step 1 clock check on the audit durations.
type ClockFacts struct {
	Checked   int     `json:"checked"`
	Anomalous int     `json:"anomalous"`
	Share     float64 `json:"share"`
	Anomaly   Tri     `json:"anomaly"`
}

// Coverage is the audit share of IIS requests (Q2), by window.
type Coverage struct {
	IISRows  int      `json:"iisRows"`
	RefShare *float64 `json:"refShare,omitempty"`
	IncShare *float64 `json:"incShare,omitempty"`
	Dip      Tri      `json:"dip"`
}

// Span is a stretch of minutes with no data (a gap). Kind is "short" (within the
// warn limit, the step runs on the minutes that exist) or "long" (those minutes
// cannot be tested). Window is "incident" or "reference".
type Span struct {
	Window  string    `json:"window"`
	Start   time.Time `json:"start"`
	End     time.Time `json:"end"`
	Minutes int       `json:"minutes"`
	Kind    string    `json:"kind"`
}

// TierOnset is when one tier's own metric first moved (A, C, O from the audit and
// IIS rows; lanes through Input.LaneOnsets). At is nil when it did not move or
// could not be dated.
type TierOnset struct {
	Tier string     `json:"tier"`
	At   *time.Time `json:"at,omitempty"`
	Why  string     `json:"why"`
	// Tested is true when the tier's own data was read in every minute of the
	// incident window, so "no onset" means it held flat. It is false when the data
	// was missing, thin or gapped: unknown is never "did not move".
	Tested bool `json:"tested"`
}

// Minute is one minute of the charts.
type Minute struct {
	At       time.Time `json:"at"`
	Window   string    `json:"window"`
	N        int       `json:"n"`
	P50      *float64  `json:"p50,omitempty"`
	P95      *float64  `json:"p95,omitempty"`
	A        *float64  `json:"a,omitempty"`
	C        *float64  `json:"c,omitempty"`
	O        *float64  `json:"o,omitempty"`
	InFlight float64   `json:"inFlight"`
	Peak     int       `json:"peak"`
	Gap      string    `json:"gap,omitempty"`
}

// call is one valid audited call: it has a start, a completion, and a duration
// that is not a clock anomaly.
type call struct {
	r     *Request
	start time.Time
	end   time.Time
	d     float64
	win   int // 0 outside both windows, 1 incident, 2 reference
	o     *float64
}

const (
	winNone = iota
	winInc
	winRef
)

// bin is one minute of one window.
type bin struct {
	at    time.Time
	win   int
	calls []*call
	area  float64 // seconds of in-flight time
	peak  int
	gap   string // "", "short" or "long"
}

func (b *bin) n() int { return len(b.calls) }

func (b *bin) values(f func(*call) *float64) []float64 {
	var out []float64
	for _, c := range b.calls {
		if v := f(c); v != nil {
			out = append(out, *v)
		}
	}
	return out
}

// prep holds the working state of one ComputeFacts run.
type prep struct {
	in  Input
	th  Thresholds
	all []*call // route-selected valid calls, any time
	// inWin are the selected calls inside a window.
	inc, ref []*call
	incBins  []*bin
	refBins  []*bin
	facts    Facts
}

// ComputeFacts reads the input once and returns every fact. It never fails: a
// window that is invalid or has no calls gives empty facts, and the readiness
// gate says what that means.
func ComputeFacts(in Input, th Thresholds) Facts {
	p := &prep{in: in, th: th}
	p.facts.Counts.NoStart = in.NoStart
	p.facts.Counts.Read = len(in.Requests) + in.NoStart
	p.classify()
	p.buildBins()
	p.concurrency()
	p.markGaps()
	p.iisSplit()
	p.groups()
	p.routes()
	p.onset()
	p.shape()
	p.scope()
	p.parts()
	p.load()
	p.retries()
	p.errors()
	p.coverage()
	p.tiers()
	p.onsetFromOutside()
	p.series()
	return p.facts
}

func spanMs(a, b time.Time) float64 { return float64(b.Sub(a)) / float64(time.Millisecond) }

// classify splits the requests into used calls and the ones that cannot be used,
// and counts each kind. A call is a clock anomaly when it ends before it starts or
// lasts longer than ClockMaxSpanHours.
func (p *prep) classify() {
	c := &p.facts.Counts
	maxSpan := time.Duration(p.th.ClockMaxSpanHours) * time.Hour
	for i := range p.in.Requests {
		r := &p.in.Requests[i]
		if r.Start.IsZero() {
			c.NoStart++
			continue
		}
		win := winNone
		switch {
		case p.in.Incident.Valid() && p.in.Incident.Contains(r.Start):
			win = winInc
		case p.in.Reference.Valid() && p.in.Reference.Contains(r.Start):
			win = winRef
		}
		if win != winNone && r.End == nil {
			c.Incomplete++
			continue
		}
		if r.End == nil {
			continue
		}
		if r.End.Before(r.Start) || r.End.Sub(r.Start) > maxSpan {
			if win != winNone {
				c.Anomalous++
			}
			continue
		}
		if p.in.Route != "" && r.Route != p.in.Route {
			continue
		}
		cl := &call{r: r, start: r.Start.UTC(), end: r.End.UTC(), d: spanMs(r.Start, *r.End), win: win}
		p.all = append(p.all, cl)
		switch win {
		case winInc:
			p.inc = append(p.inc, cl)
		case winRef:
			p.ref = append(p.ref, cl)
		}
	}
	// The clock check counts every call that has both times in a window, whatever
	// the route filter says.
	p.facts.Clock = p.clockFacts()
	c.Used = len(p.inc) + len(p.ref)
	// Outside counts every request with a start that is in neither window.
	for i := range p.in.Requests {
		r := &p.in.Requests[i]
		if r.Start.IsZero() {
			continue
		}
		in := (p.in.Incident.Valid() && p.in.Incident.Contains(r.Start)) || (p.in.Reference.Valid() && p.in.Reference.Contains(r.Start))
		if in {
			if p.in.Incident.Valid() && p.in.Incident.Contains(r.Start) {
				c.Incident++
			} else {
				c.Reference++
			}
		} else {
			c.Outside++
		}
	}
}

func (p *prep) clockFacts() ClockFacts {
	var f ClockFacts
	maxSpan := time.Duration(p.th.ClockMaxSpanHours) * time.Hour
	for i := range p.in.Requests {
		r := &p.in.Requests[i]
		if r.Start.IsZero() || r.End == nil {
			continue
		}
		if !(p.in.Incident.Valid() && p.in.Incident.Contains(r.Start)) && !(p.in.Reference.Valid() && p.in.Reference.Contains(r.Start)) {
			continue
		}
		f.Checked++
		if r.End.Before(r.Start) || r.End.Sub(r.Start) > maxSpan {
			f.Anomalous++
		}
	}
	switch {
	case f.Checked == 0:
		f.Anomaly = Unknown
	default:
		f.Share = float64(f.Anomalous) / float64(f.Checked)
		if f.Anomalous > 0 && f.Share >= p.th.ClockAnomalyShare-floatEps {
			f.Anomaly = Yes
		} else {
			f.Anomaly = No
		}
	}
	f.Share = round2(f.Share)
	return f
}

func windowBins(w Window, win int) []*bin {
	if !w.Valid() {
		return nil
	}
	first := minuteOf(w.Start)
	n := w.minutes()
	out := make([]*bin, n)
	for i := range out {
		out[i] = &bin{at: first.Add(time.Duration(i) * time.Minute), win: win}
	}
	return out
}

func (p *prep) buildBins() {
	p.incBins = windowBins(p.in.Incident, winInc)
	p.refBins = windowBins(p.in.Reference, winRef)
	place := func(calls []*call, bins []*bin) {
		if len(bins) == 0 {
			return
		}
		first := bins[0].at
		for _, c := range calls {
			i := int(minuteOf(c.start).Sub(first) / time.Minute)
			if i >= 0 && i < len(bins) {
				bins[i].calls = append(bins[i].calls, c)
			}
		}
	}
	place(p.inc, p.incBins)
	place(p.ref, p.refBins)
}

// concurrency sweeps the selected calls (including those just outside a window,
// so a call that began before the window still counts as in flight inside it) and
// fills each minute's in-flight area and peak.
func (p *prep) concurrency() {
	type ev struct {
		t     time.Time
		delta int
	}
	var evs []ev
	for _, c := range p.all {
		if !c.end.After(c.start) {
			continue
		}
		evs = append(evs, ev{c.start, +1}, ev{c.end, -1})
	}
	if len(evs) == 0 {
		return
	}
	sort.Slice(evs, func(i, j int) bool {
		if !evs[i].t.Equal(evs[j].t) {
			return evs[i].t.Before(evs[j].t)
		}
		return evs[i].delta < evs[j].delta // ends before starts at the same instant
	})
	byMinute := map[int64]*bin{}
	for _, bs := range [][]*bin{p.incBins, p.refBins} {
		for _, b := range bs {
			byMinute[b.at.Unix()/60] = b
		}
	}
	cur := 0
	last := evs[0].t
	advance := func(to time.Time) {
		for last.Before(to) {
			mEnd := minuteOf(last).Add(time.Minute)
			segEnd := to
			if mEnd.Before(segEnd) {
				segEnd = mEnd
			}
			if b := byMinute[minuteOf(last).Unix()/60]; b != nil && cur > 0 {
				b.area += segEnd.Sub(last).Seconds() * float64(cur)
				if cur > b.peak {
					b.peak = cur
				}
			}
			last = segEnd
		}
	}
	for _, e := range evs {
		advance(e.t)
		cur += e.delta
	}
}

// markGaps flags minutes with no data. A minute is a gap when nothing started in
// it and nothing was in flight, and the reference says the API is busy enough
// (GapRefMinPerMinute starts a minute at the median) that an empty minute is not
// just a quiet one. Runs of GapWarnMinutes or fewer are "short", longer runs are
// "long" (rule A35).
func (p *prep) markGaps() {
	if len(p.refBins) == 0 {
		return
	}
	counts := make([]float64, len(p.refBins))
	for i, b := range p.refBins {
		counts[i] = float64(b.n())
	}
	med, _ := median(counts)
	if med < float64(p.th.GapRefMinPerMinute) {
		return
	}
	mark := func(bins []*bin, label string) {
		i := 0
		for i < len(bins) {
			if bins[i].n() != 0 || bins[i].area > 0 {
				i++
				continue
			}
			j := i
			for j < len(bins) && bins[j].n() == 0 && bins[j].area == 0 {
				j++
			}
			kind := "long"
			if j-i <= p.th.GapWarnMinutes {
				kind = "short"
			}
			for k := i; k < j; k++ {
				bins[k].gap = kind
			}
			p.facts.Gaps = append(p.facts.Gaps, Span{Window: label, Start: bins[i].at, End: bins[j-1].at.Add(time.Minute), Minutes: j - i, Kind: kind})
			i = j
		}
	}
	mark(p.incBins, "incident")
	mark(p.refBins, "reference")
}

func (p *prep) testable(bins []*bin) int {
	n := 0
	for _, b := range bins {
		if b.gap == "" {
			n++
		}
	}
	return n
}

func durations(calls []*call) []float64 {
	out := make([]float64, 0, len(calls))
	for _, c := range calls {
		out = append(out, c.d)
	}
	return out
}

func (p *prep) groups() {
	fill := func(g *GroupFacts, calls []*call, bins []*bin) {
		d := durations(calls)
		g.Percentiles = summarize(d, p.th)
		if m, ok := mean(d); ok {
			g.MeanMs = ptr(m)
		}
		g.TestableMinutes = p.testable(bins)
		if g.TestableMinutes > 0 && len(calls) > 0 {
			g.PerSec = ptr(float64(len(calls)) / (float64(g.TestableMinutes) * 60))
		}
	}
	fill(&p.facts.Incident, p.inc, p.incBins)
	fill(&p.facts.Ref, p.ref, p.refBins)
	f := &p.facts
	f.Change.P50 = compareMs(f.Ref.P50, f.Incident.P50, p.th)
	f.Change.P95 = compareMs(f.Ref.P95, f.Incident.P95, p.th)
	f.Change.Mean = compareMs(f.Ref.MeanMs, f.Incident.MeanMs, p.th)
	switch {
	case f.Change.P95.Known:
		f.Change.Known, f.Change.Meaningful = true, f.Change.P95.Meaningful
	case f.Change.Mean.Known:
		f.Change.Known, f.Change.Meaningful = true, f.Change.Mean.Meaningful
	}
}

// routeCalls groups every valid call of both windows by route, ignoring the route
// filter (scope reads all routes).
func (p *prep) routeCalls() map[string][2][]float64 {
	out := map[string][2][]float64{}
	maxSpan := time.Duration(p.th.ClockMaxSpanHours) * time.Hour
	for i := range p.in.Requests {
		r := &p.in.Requests[i]
		if r.Start.IsZero() || r.End == nil || r.End.Before(r.Start) || r.End.Sub(r.Start) > maxSpan {
			continue
		}
		var w int
		switch {
		case p.in.Incident.Valid() && p.in.Incident.Contains(r.Start):
			w = 0
		case p.in.Reference.Valid() && p.in.Reference.Contains(r.Start):
			w = 1
		default:
			continue
		}
		e := out[r.Route]
		e[w] = append(e[w], spanMs(r.Start, *r.End))
		out[r.Route] = e
	}
	return out
}

func (p *prep) routes() {
	m := p.routeCalls()
	names := make([]string, 0, len(m))
	for k := range m {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, n := range names {
		e := m[n]
		rf := RouteFacts{Route: n, Incident: summarize(e[0], p.th), Reference: summarize(e[1], p.th)}
		rf.P95Change = compareMs(rf.Reference.P95, rf.Incident.P95, p.th)
		p.facts.Routes = append(p.facts.Routes, rf)
	}
}

// statusFn classifies one minute for a run search.
type minStatus int

const (
	stSkip minStatus = iota
	stHit
	stMiss
)

// findRun returns the index of the first minute of the first run of `need`
// consecutive hits. A short gap and a thin minute are skipped (they neither extend
// nor break a run); a long gap and a miss reset it.
func findRun(bins []*bin, need int, status func(*bin) minStatus) (int, bool) {
	run, start := 0, -1
	for i, b := range bins {
		switch b.gap {
		case "short":
			continue
		case "long":
			run, start = 0, -1
			continue
		}
		switch status(b) {
		case stSkip:
		case stMiss:
			run, start = 0, -1
		case stHit:
			if run == 0 {
				start = i
			}
			run++
			if run >= need {
				return start, true
			}
		}
	}
	return 0, false
}

// minuteP95 is the P95 of one minute's durations, or nil when the minute holds
// fewer than OnsetMinSamples calls.
func (p *prep) minuteP95(b *bin) *float64 {
	if b.n() < p.th.OnsetMinSamples {
		return nil
	}
	d := sortedCopy(durations(b.calls))
	v := nearestRank(d, 0.95)
	return &v
}

func (p *prep) hotMinute(b *bin, refP95 float64) minStatus {
	v := p.minuteP95(b)
	if v == nil {
		return stSkip
	}
	if *v > p.th.OnsetFactor*refP95 && *v-refP95 >= p.th.ChangeMinMs-floatEps {
		return stHit
	}
	return stMiss
}

// onset applies rule A24, with the millisecond floor of A23: t0 is the first
// minute whose P95 stays above OnsetFactor times the reference P95 (and above it
// by at least ChangeMinMs) for OnsetRunMinutes minutes in a row.
func (p *prep) onset() {
	o := &p.facts.Onset
	ref := p.facts.Ref.P95
	switch {
	case !p.in.Incident.Valid() || !p.in.Reference.Valid():
		o.Status, o.Why = "cannot date", "The incident window or the reference window is missing."
		return
	case ref == nil:
		o.Status, o.Why = "cannot date", "The reference window has fewer calls than a P95 needs, so there is no baseline to compare with."
		return
	}
	o.RefP95Ms = ptr(*ref)
	o.ThresholdMs = ptr(p.th.OnsetFactor * *ref)
	idx, ok := findRun(p.incBins, p.th.OnsetRunMinutes, func(b *bin) minStatus { return p.hotMinute(b, *ref) })
	if ok {
		t := p.incBins[idx].at
		o.Status, o.T0, o.Basis = "found", &t, "audit duration"
		o.Why = "The P95 stayed above the reference P95 for the run of minutes the rule needs."
		return
	}
	thick := 0
	for _, b := range p.incBins {
		if b.gap == "" && b.n() >= p.th.OnsetMinSamples {
			thick++
		}
	}
	if thick < p.th.OnsetRunMinutes {
		o.Status, o.Why = "cannot date", "Too few minutes hold enough calls to read a per-minute P95, so a start time cannot be dated."
		return
	}
	o.Status, o.Why = "not found", "No run of minutes stayed above the reference P95."
}

// shape is the Step 2 shape: periodic (repeated bursts at about 15 or 60 minutes),
// step (it was at or near the reference just before t0) or ramp (it was already
// rising).
func (p *prep) shape() {
	s := &p.facts.Shape
	ref := p.facts.Ref.P95
	o := p.facts.Onset
	if ref == nil || len(p.incBins) == 0 {
		s.Kind, s.Why = "unknown", "There is no baseline to read a shape against."
		return
	}
	// Periodic: three or more separate bursts whose starts sit a whole number of
	// periods apart.
	var starts []int
	in := false
	for i, b := range p.incBins {
		hot := b.gap == "" && p.hotMinute(b, *ref) == stHit
		if hot && !in {
			starts = append(starts, i)
		}
		if b.gap == "" || b.gap == "long" {
			in = hot
		}
	}
	if len(starts) >= p.th.PeriodicMinExcursions {
		for _, period := range []int{p.th.PeriodicShortMinutes, p.th.PeriodicLongMinutes} {
			ok := true
			for i := 1; i < len(starts); i++ {
				d := starts[i] - starts[i-1]
				k := int(math.Round(float64(d) / float64(period)))
				if k < 1 || absInt(d-k*period) > p.th.PeriodicToleranceMin {
					ok = false
					break
				}
			}
			if ok {
				s.Kind, s.PeriodMinutes = "periodic", period
				s.Why = "The slow minutes come back at a steady spacing."
				return
			}
		}
	}
	if o.Status != "found" || o.T0 == nil {
		if o.Status == "not found" {
			s.Kind, s.Why = "none", "Nothing stayed above the reference long enough to have a shape."
		} else {
			s.Kind, s.Why = "unknown", "The start of the change could not be dated, so its shape cannot be read."
		}
		return
	}
	t0 := -1
	for i, b := range p.incBins {
		if b.at.Equal(*o.T0) {
			t0 = i
			break
		}
	}
	seen, rising := 0, false
	for k := 1; k <= p.th.StepRiseMinutes; k++ {
		i := t0 - k
		if i < 0 || p.incBins[i].gap != "" {
			continue
		}
		v := p.minuteP95(p.incBins[i])
		if v == nil {
			continue
		}
		seen++
		if *v > p.th.StepRampBelowFactor**ref {
			rising = true
		}
	}
	switch {
	case seen == 0:
		s.Kind, s.Why = "unknown", "No minute just before the start holds enough calls to say whether it jumped or was already rising."
	case rising:
		s.Kind, s.Why = "ramp", "The P95 was already above the reference in the minutes before t0, so it built up."
	default:
		s.Kind, s.Why = "step", "The P95 was near the reference until t0 and then jumped."
	}
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// scope counts how many of the routes that have enough calls in both windows
// changed by the A23 mark.
func (p *prep) scope() {
	sc := &p.facts.Scope
	for _, r := range p.facts.Routes {
		if r.P95Change.Known {
			sc.Testable++
			if r.P95Change.Meaningful && r.P95Change.Direction == "up" {
				sc.Changed++
				sc.Routes = append(sc.Routes, r.Route)
			}
		}
	}
	switch {
	case sc.Testable == 0:
		sc.Kind, sc.Why = "unknown", "No route has enough calls in both windows to compare."
	case sc.Changed == 0:
		sc.Kind, sc.Why = "none", "No route with enough calls rose by the change mark."
	case sc.Changed == 1:
		sc.Kind, sc.Why = "one route", "One route rose by the change mark."
	case sc.Changed <= p.th.ScopeFewMaxRoutes && float64(sc.Changed) < p.th.ScopeAllShare*float64(sc.Testable):
		sc.Kind, sc.Why = "a few routes", "A few routes rose by the change mark."
	case float64(sc.Changed) >= p.th.ScopeAllShare*float64(sc.Testable)-floatEps:
		sc.Kind, sc.Why = "everything", "Nearly every route with enough calls rose by the change mark."
	default:
		sc.Kind, sc.Why = "many routes", "Many routes rose by the change mark, but not nearly all."
	}
}

// onsetFromOutside covers a slowdown the audit durations cannot see. Outside time
// (IIS time-taken minus the audited span and payload capture) lies outside the
// audited duration, so a queue in front of the app leaves the audit P95 flat. When
// no run was found on the audit durations but outside time moved, t0 is dated from
// outside time and the basis says so.
func (p *prep) onsetFromOutside() {
	o := &p.facts.Onset
	if o.Status != "not found" {
		return
	}
	for _, t := range p.facts.Tiers {
		if t.Tier == "o" && t.At != nil {
			at := *t.At
			o.Status, o.T0, o.Basis = "found", &at, "outside time"
			o.Why = "The audit durations did not move, but outside time stayed above its reference for the run of minutes the rule needs."
			return
		}
	}
}
