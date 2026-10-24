package resource

import (
	"context"
	"errors"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

// GapPartial is the index gap when the case scan stopped at the scan budget.
const GapPartial = "the scan of the case reached its budget: series and markers cover only the events read"

// GapNoCadence is the gap a series carries when its sample interval is unknown
// (one sample and no other series of its source on its host): its samples are
// points and its coverage is 0.
const GapNoCadence = "one sample only: the sampling interval is unknown"

// Sample is one value of a series. It covers [Start, End]: End is the sample
// time and Start is End less the series interval (the median cadence), so a
// point sample overlaps the intervals it summarises. For a cumulative metric,
// Delta is the step from the previous sample; a drop (a restart of the counter)
// sets Reset and leaves HasDelta false, never a negative delta.
type Sample struct {
	Start, End time.Time
	Value      float64
	Delta      float64
	HasDelta   bool
	Reset      bool
	EvidenceID string
}

// Series is the samples of one metric from one source for one host or process.
type Series struct {
	Metric   Metric
	Host     string
	Source   string
	Label    string // where the value comes from, e.g. `Memory\Available MBytes`
	Proc     Proc   // zero for a host series
	Interval time.Duration
	Samples  []Sample
	Gap      string // "" or a fixed sentence (GapLegacyInstance, GapNoCadence)
}

// Filter narrows Index.Series. Empty fields match everything; Process matches
// the process image name, ignoring case.
type Filter struct {
	Host    string
	Source  string
	Process string
	PID     int
}

func (f Filter) match(s Series) bool {
	return (f.Host == "" || f.Host == s.Host) &&
		(f.Source == "" || f.Source == s.Source) &&
		(f.Process == "" || strings.EqualFold(f.Process, s.Proc.Name)) &&
		(f.PID == 0 || f.PID == s.Proc.PID)
}

// Index is the catalogue of one case over one window.
type Index struct {
	window   importer.Window
	from, to time.Time // the coverage window: w, with open bounds set to the extent of the samples
	series   []Series
	markers  []Marker
	gaps     []string
	partial  bool
}

// Series returns the series of a metric id that pass f, ordered by host,
// process, source and label. Each is a copy.
func (x Index) Series(metric string, f Filter) []Series {
	var out []Series
	for _, s := range x.series {
		if s.Metric.ID == metric && f.match(s) {
			c := s
			c.Samples = append([]Sample(nil), s.Samples...)
			out = append(out, c)
		}
	}
	return out
}

// Markers returns the start and stop markers in the window, by time.
func (x Index) Markers() []Marker {
	out := make([]Marker, len(x.markers))
	for i, m := range x.markers {
		m.EvidenceIDs = append([]string(nil), m.EvidenceIDs...)
		out[i] = m
	}
	return out
}

// Gaps returns the fixed sentences that apply to the whole index.
func (x Index) Gaps() []string { return append([]string(nil), x.gaps...) }

// Partial reports whether the scan stopped at the scan budget.
func (x Index) Partial() bool { return x.partial }

// Window is the window the index was built for.
func (x Index) Window() importer.Window { return x.window }

// Coverage is the fraction of the expected sample buckets of s that hold a
// sample, over the window (an open bound is the extent of all samples in the
// index). Buckets are s.Interval wide from the window start. A series with no
// samples, or no known interval, has coverage 0.
func (x Index) Coverage(s Series) float64 {
	if len(s.Samples) == 0 || s.Interval <= 0 {
		return 0
	}
	from, to := x.from, x.to
	if !to.After(from) {
		return 1
	}
	n := int64(math.Ceil(float64(to.Sub(from)) / float64(s.Interval)))
	if n < 1 {
		n = 1
	}
	seen := map[int64]bool{}
	for _, sm := range s.Samples {
		if sm.End.Before(from) || sm.End.After(to) {
			continue
		}
		b := int64(sm.End.Sub(from) / s.Interval)
		if b >= n {
			b = n - 1
		}
		seen[b] = true
	}
	return float64(len(seen)) / float64(n)
}

// Kinds the catalogue reads.
var (
	sampleKinds = []string{"counter", "metric"}
	markerKinds = []string{"apppool-recycle", "apppool-idle-shutdown", "worker-process-failure",
		"host-unexpected-shutdown", "app-start", "app-session", "app-stop", "process-gone",
		"redis-start", "redis-stop", "mongod-start", "mongod-stop"}
)

// Catalogue builds the index of case c over window w (UTC, inclusive; a zero
// bound is open). A scan that reaches the store's scan budget gives a partial
// index (Partial, GapPartial) and no error.
func Catalogue(c casestore.Case, w importer.Window) (Index, error) {
	return CatalogueContext(context.Background(), c, w)
}

// obs is one event read for a series, before identity resolution.
type obs struct {
	m       Metric
	uptime  bool // an uptime-like value: used for markers, not listed as a series
	host    string
	source  string
	label   string
	proc    Proc
	class   pidClass
	legacy  bool
	t       time.Time
	start   time.Time
	v       float64
	id      string
	ownSpan bool // the event has its own interval (End after Start)
}

// builder collects one scan.
type builder struct {
	w       importer.Window
	obs     []obs
	pidAt   map[string]map[int64]int // host|source|class|instance -> sample time -> pid
	starts  map[string][]time.Time   // host|name|pid -> process-start times
	evMarks []Marker
	partial bool
}

func pidKey(host, source string, class pidClass, inst string) string {
	return host + "\x00" + source + "\x00" + string(class) + "\x00" + strings.ToLower(inst)
}

func startKey(host, name string, pid int) string {
	return host + "\x00" + strings.ToLower(name) + "\x00" + strconv.Itoa(pid)
}

// CatalogueContext is Catalogue with a context.
func CatalogueContext(ctx context.Context, c casestore.Case, w importer.Window) (Index, error) {
	if c == nil {
		return Index{}, errors.New("resource: no case")
	}
	b := &builder{w: w, pidAt: map[string]map[int64]int{}, starts: map[string][]time.Time{}}
	scan := func(q casestore.EventQuery, fn func(evidence.Event) error) error {
		err := c.Each(ctx, q, fn)
		if errors.Is(err, casestore.ErrScanBudget) {
			b.partial = true
			return nil
		}
		return err
	}
	if err := scan(casestore.EventQuery{Window: w, Kinds: sampleKinds}, b.sample); err != nil {
		return Index{}, err
	}
	// Process starts before the window still name the process of a sample in it.
	if err := scan(casestore.EventQuery{Window: importer.Window{To: w.To}, Kinds: []string{"process-start"}}, b.processStart); err != nil {
		return Index{}, err
	}
	if err := scan(casestore.EventQuery{Window: w, Kinds: markerKinds}, b.marker); err != nil {
		return Index{}, err
	}
	return b.build(), nil
}

// sample reads one counter or metric event.
func (b *builder) sample(ev evidence.Event) error {
	if ev.Value == nil {
		return nil
	}
	v := *ev.Value
	isPDH := ev.Source == sourceWinCounters || ev.Source == sourceSQLPDH
	if isPDH && ev.Kind == "counter" {
		object, inst, ctr := ev.Attrs["object"], ev.Attrs["instance"], ev.Attrs["counter"]
		class, _ := objectClass(object)
		if isPIDCounter(class, object, ctr) && inst != "" && !aggregateInstance(inst) {
			if pid, ok := pidFromValue(v); ok {
				k := pidKey(ev.Host, ev.Source, class, inst)
				if b.pidAt[k] == nil {
					b.pidAt[k] = map[int64]int{}
				}
				b.pidAt[k][ev.End.UnixNano()] = pid
			}
			return nil
		}
	}
	if procScoped, ok := uptimeCounter(ev); ok {
		o := obs{uptime: true, m: Metric{ID: "uptime", Unit: ev.Unit, Scale: 1}, host: ev.Host, source: ev.Source,
			label: labelOf(ev, procScoped), t: ev.End, start: ev.Start, v: v, id: ev.EvidenceID}
		if procScoped {
			p, class, legacy, skip := pdhIdentity(ev.Attrs["object"], ev.Attrs["instance"])
			if skip {
				return nil
			}
			o.proc, o.class, o.legacy = p, class, legacy
		}
		b.obs = append(b.obs, o)
		return nil
	}
	r, ok := match(ev)
	if !ok {
		return nil
	}
	m := r.metricOf(ev)
	if r.complement {
		v = 100 - v
	}
	o := obs{m: m, host: ev.Host, source: ev.Source, label: labelOf(ev, r.scope == scopeProc),
		t: ev.End, start: ev.Start, v: v * m.Scale, id: ev.EvidenceID, ownSpan: ev.End.After(ev.Start)}
	if r.scope == scopeProc {
		if isPDH {
			p, class, legacy, skip := pdhIdentity(ev.Attrs["object"], ev.Attrs["instance"])
			if skip {
				if r.id != IISWPActive {
					return nil // an aggregate instance names no process
				}
				o.label = labelOf(ev, false) // W3SVC_W3WP(_Total) is a host series
			} else {
				o.proc, o.class, o.legacy = p, class, legacy
			}
		} else {
			o.proc = attrIdentity(ev)
		}
	}
	b.obs = append(b.obs, o)
	return nil
}

func pidFromValue(v float64) (int, bool) {
	if v <= 0 || v != math.Trunc(v) || v > math.MaxInt32 {
		return 0, false
	}
	return int(v), true
}

// labelOf names where a value comes from. A process series leaves the instance
// out: the identity carries it.
func labelOf(ev evidence.Event, procScoped bool) string {
	a := ev.Attrs
	switch {
	case a["object"] != "" && a["counter"] != "":
		if inst := a["instance"]; inst != "" && !procScoped {
			return a["object"] + "(" + inst + ")\\" + a["counter"]
		}
		return a["object"] + "\\" + a["counter"]
	case a["counter"] != "":
		l := a["counter"]
		var tags []string
		for _, k := range []string{"generation", "method", "status", "percentile"} {
			if v := a[k]; v != "" {
				tags = append(tags, k+"="+v)
			}
		}
		if len(tags) > 0 {
			l += "[" + strings.Join(tags, ";") + "]"
		}
		return l
	default:
		return a["metric"]
	}
}

// processStart records a process-start for the identity join, and as a marker
// when it is in the window.
func (b *builder) processStart(ev evidence.Event) error {
	p := attrIdentity(ev)
	if p.PID > 0 {
		k := startKey(ev.Host, p.Name, p.PID)
		b.starts[k] = append(b.starts[k], ev.Start)
	}
	if b.w.Contains(ev.Start) {
		return b.marker(ev)
	}
	return nil
}

// marker records an event marker.
func (b *builder) marker(ev evidence.Event) error {
	if m, ok := eventMarker(ev); ok {
		b.evMarks = append(b.evMarks, m)
	}
	return nil
}

// build resolves identities and assembles the index.
func (b *builder) build() Index {
	x := Index{window: b.w, partial: b.partial}
	if b.partial {
		x.gaps = append(x.gaps, GapPartial)
	}
	b.resolve()

	// Group into series.
	type group struct {
		s   Series
		obs []obs
	}
	groups := map[string]*group{}
	var order []string
	var uptimes []*group
	for _, o := range b.obs {
		startNano := int64(0)
		if !o.proc.Start.IsZero() {
			startNano = o.proc.Start.UnixNano()
		}
		k := strings.Join([]string{o.m.ID, o.m.Unit, strconv.FormatBool(o.m.Cumulative), strconv.FormatFloat(o.m.Scale, 'g', -1, 64),
			o.host, o.source, o.label, o.proc.Key(), strconv.FormatInt(startNano, 10), strconv.FormatBool(o.uptime)}, "\x00")
		g, ok := groups[k]
		if !ok {
			g = &group{s: Series{Metric: o.m, Host: o.host, Source: o.source, Label: o.label, Proc: o.proc}}
			if o.proc.PID == 0 && o.proc.Instance != "" && o.legacy {
				g.s.Gap = GapLegacyInstance
			}
			groups[k] = g
			order = append(order, k)
			if o.uptime {
				uptimes = append(uptimes, g)
			}
		}
		g.obs = append(g.obs, o)
	}

	// Samples, sorted and de-duplicated by time, and the cadence of each series.
	cadence := map[string][]time.Duration{} // source|host -> series intervals
	for _, k := range order {
		g := groups[k]
		sort.SliceStable(g.obs, func(i, j int) bool {
			if !g.obs[i].t.Equal(g.obs[j].t) {
				return g.obs[i].t.Before(g.obs[j].t)
			}
			return g.obs[i].id < g.obs[j].id
		})
		uniq := g.obs[:0]
		for i, o := range g.obs {
			if i > 0 && o.t.Equal(uniq[len(uniq)-1].t) {
				continue
			}
			uniq = append(uniq, o)
		}
		g.obs = uniq
		g.s.Interval = medianGap(g.obs)
		if g.s.Interval > 0 {
			ck := g.s.Source + "\x00" + g.s.Host
			cadence[ck] = append(cadence[ck], g.s.Interval)
			cadence[g.s.Source] = append(cadence[g.s.Source], g.s.Interval)
		}
	}
	for _, k := range order {
		g := groups[k]
		if g.s.Interval == 0 {
			if d := median(cadence[g.s.Source+"\x00"+g.s.Host]); d > 0 {
				g.s.Interval = d
			} else if d := median(cadence[g.s.Source]); d > 0 {
				g.s.Interval = d
			} else if g.s.Gap == "" {
				g.s.Gap = GapNoCadence
			}
		}
		g.s.Samples = make([]Sample, 0, len(g.obs))
		for i, o := range g.obs {
			sm := Sample{Start: o.t.Add(-g.s.Interval), End: o.t, Value: o.v, EvidenceID: o.id}
			if o.ownSpan {
				sm.Start = o.start
			}
			if g.s.Metric.Cumulative && i > 0 {
				prev := g.obs[i-1].v
				if o.v < prev {
					sm.Reset = true
				} else {
					sm.Delta, sm.HasDelta = o.v-prev, true
				}
			}
			g.s.Samples = append(g.s.Samples, sm)
		}
	}

	// Coverage window: open bounds take the extent of the samples.
	var first, last time.Time
	for _, k := range order {
		for _, sm := range groups[k].s.Samples {
			if first.IsZero() || sm.End.Before(first) {
				first = sm.End
			}
			if last.IsZero() || sm.End.After(last) {
				last = sm.End
			}
		}
	}
	x.from, x.to = b.w.From, b.w.To
	if x.from.IsZero() {
		x.from = first
	}
	if x.to.IsZero() {
		x.to = last
	}

	legacyGap := false
	for _, k := range order {
		g := groups[k]
		if g.s.Metric.ID == "uptime" {
			continue
		}
		if g.s.Gap == GapLegacyInstance {
			legacyGap = true
		}
		x.series = append(x.series, g.s)
	}
	if legacyGap {
		x.gaps = append(x.gaps, GapLegacyInstance)
	}
	sort.SliceStable(x.series, func(i, j int) bool {
		a, c := x.series[i], x.series[j]
		if a.Metric.ID != c.Metric.ID {
			return a.Metric.ID < c.Metric.ID
		}
		if a.Host != c.Host {
			return a.Host < c.Host
		}
		if a.Proc.Key() != c.Proc.Key() {
			return a.Proc.Key() < c.Proc.Key()
		}
		if !a.Proc.Start.Equal(c.Proc.Start) {
			return a.Proc.Start.Before(c.Proc.Start)
		}
		if a.Source != c.Source {
			return a.Source < c.Source
		}
		return a.Label < c.Label
	})

	var up []Series
	for _, g := range uptimes {
		up = append(up, g.s)
	}
	x.markers = b.markers(x.series, up)
	return x
}

// resolve re-keys legacy instances through their pid counter, fills the name
// of a pid-only identity, and splits win-procsnap and linux-pidstat identities
// at process starts.
func (b *builder) resolve() {
	names := map[string]map[string]bool{} // host|pid -> names
	for i := range b.obs {
		o := &b.obs[i]
		if o.legacy {
			if pid, ok := b.pidAt[pidKey(o.host, o.source, o.class, o.proc.Instance)][o.t.UnixNano()]; ok {
				o.proc = Proc{Name: o.proc.Name, PID: pid}
			}
		}
		if o.proc.PID > 0 && o.proc.Name != "" {
			k := o.host + "\x00" + strconv.Itoa(o.proc.PID)
			if names[k] == nil {
				names[k] = map[string]bool{}
			}
			names[k][strings.ToLower(o.proc.Name)] = true
		}
	}
	for k := range b.starts {
		ts := b.starts[k]
		sort.Slice(ts, func(i, j int) bool { return ts[i].Before(ts[j]) })
	}
	for i := range b.obs {
		o := &b.obs[i]
		if o.proc.PID > 0 && o.proc.Name == "" {
			if set := names[o.host+"\x00"+strconv.Itoa(o.proc.PID)]; len(set) == 1 {
				for n := range set {
					o.proc.Name = n
				}
			}
		}
		if o.proc.PID > 0 && (o.source == sourceProcSnap || o.source == sourcePidstat) {
			ts := b.starts[startKey(o.host, o.proc.Name, o.proc.PID)]
			// latest start at or before the sample
			j := sort.Search(len(ts), func(j int) bool { return ts[j].After(o.t) })
			if j > 0 {
				o.proc.Start = ts[j-1]
			}
		}
	}
}

// medianGap is the median step between consecutive sample times, or 0.
func medianGap(os []obs) time.Duration {
	if len(os) < 2 {
		return 0
	}
	gaps := make([]time.Duration, 0, len(os)-1)
	for i := 1; i < len(os); i++ {
		gaps = append(gaps, os[i].t.Sub(os[i-1].t))
	}
	return median(gaps)
}

// median of ds (the lower middle for an even count), or 0 when empty.
func median(ds []time.Duration) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	c := append([]time.Duration(nil), ds...)
	sort.Slice(c, func(i, j int) bool { return c[i] < c[j] })
	return c[(len(c)-1)/2]
}
