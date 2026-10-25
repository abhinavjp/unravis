package resource

import (
	"context"
	"errors"
	"math"
	"slices"
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

// obs is one event read for a series, before identity resolution. It is a
// transient value: sample builds it, and builder.add splits it into the part
// every event of a series shares (obsKey, kept once) and a compact row (cobs,
// kept per event). Holding the full struct per event cost about 1.1 KB of heap
// per scanned event once the grouping copies were counted.
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
	metric  bool // the event kind is "metric", not "counter"
}

// obsKey is what the events of one series share before identity resolution.
type obsKey struct {
	m      Metric
	uptime bool
	host   string
	source string
	label  string
	proc   Proc
	class  pidClass
	legacy bool
}

// cobs is the part of an obs that differs per event. The scan is read in storage
// order, so the old scan order (kind, then start time, then evidence id) is
// rebuilt from the row itself by before, which stands in for the order of the
// single slice the catalogue used to keep.
type cobs struct {
	key     int32
	ownSpan bool
	metric  bool
	t       time.Time
	start   time.Time
	v       float64
	id      string
}

// builder collects one scan.
type builder struct {
	w       importer.Window
	keys    []obsKey
	keyIdx  map[obsKey]int32
	rows    [][]cobs                 // per key, in the order the scan delivered them
	pidAt   map[string]map[int64]int // host|source|class|instance -> sample time -> pid
	starts  map[string][]time.Time   // host|name|pid -> process-start times
	evMarks []Marker
	partial bool
	tap     func(obs) // test hook: sees every obs as it is kept
}

// add keeps one event read for a series.
func (b *builder) add(o obs) {
	if b.tap != nil {
		b.tap(o)
	}
	k := obsKey{m: o.m, uptime: o.uptime, host: o.host, source: o.source, label: o.label, proc: o.proc, class: o.class, legacy: o.legacy}
	i, ok := b.keyIdx[k]
	if !ok {
		i = int32(len(b.keys))
		b.keyIdx[k] = i
		b.keys = append(b.keys, k)
		b.rows = append(b.rows, nil)
	}
	b.rows[i] = append(b.rows[i], cobs{key: i, ownSpan: o.ownSpan, metric: o.metric, t: o.t, start: o.start, v: o.v, id: o.id})
}

// before reports whether a came before c in the order the scan used to deliver
// events: by kind, then by start time to the millisecond (the stored precision),
// then by evidence id. The scan is now read in whatever order is cheapest, so
// the order is taken from the rows and not from their arrival.
func before(a, c cobs) bool {
	if a.metric != c.metric {
		return !a.metric // "counter" sorts before "metric"
	}
	if am, cm := a.start.UnixMilli(), c.start.UnixMilli(); am != cm {
		return am < cm
	}
	return a.id < c.id
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
	b := &builder{w: w, keyIdx: map[obsKey]int32{}, pidAt: map[string]map[int64]int{}, starts: map[string][]time.Time{}}
	scan := func(q casestore.EventQuery, fn func(evidence.Event) error) error {
		err := c.Each(ctx, q, fn)
		if errors.Is(err, casestore.ErrScanBudget) {
			b.partial = true
			return nil
		}
		return err
	}
	// The samples are read in storage order: build rebuilds the scan order from the
	// rows (see before), and the pid map and series do not depend on arrival order.
	if err := scan(casestore.EventQuery{Window: w, Kinds: sampleKinds, Unordered: true}, b.sample); err != nil {
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
			label: labelOf(ev, procScoped), t: ev.End, start: ev.Start, v: v, id: ev.EvidenceID, metric: ev.Kind == "metric"}
		if procScoped {
			p, class, legacy, skip := pdhIdentity(ev.Attrs["object"], ev.Attrs["instance"])
			if skip {
				return nil
			}
			o.proc, o.class, o.legacy = p, class, legacy
		}
		b.add(o)
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
		t: ev.End, start: ev.Start, v: v * m.Scale, id: ev.EvidenceID, ownSpan: ev.End.After(ev.Start), metric: ev.Kind == "metric"}
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
	b.add(o)
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

// legacyProc is the identity of a sample after a legacy instance is re-keyed
// through its pid counter (pm: sample time -> pid for the key's instance).
func legacyProc(key *obsKey, pm map[int64]int, t time.Time) Proc {
	if key.legacy {
		if pid, ok := pm[t.UnixNano()]; ok {
			return Proc{Name: key.proc.Name, PID: pid}
		}
	}
	return key.proc
}

// pidNames lists, for each host and pid, the names a sample gave that pid
// (lower case). A pid with exactly one name names every sample of it that lacks one.
func (b *builder) pidNames() map[string]map[string]bool {
	names := map[string]map[string]bool{} // host|pid -> names
	add := func(host string, p Proc) {
		if p.PID > 0 && p.Name != "" {
			k := host + "\x00" + strconv.Itoa(p.PID)
			if names[k] == nil {
				names[k] = map[string]bool{}
			}
			names[k][strings.ToLower(p.Name)] = true
		}
	}
	for ki := range b.keys {
		key, rows := &b.keys[ki], b.rows[ki]
		if len(rows) == 0 {
			continue
		}
		if !key.legacy {
			add(key.host, key.proc)
			continue
		}
		pm := b.pidAt[pidKey(key.host, key.source, key.class, key.proc.Instance)]
		for _, c := range rows {
			add(key.host, legacyProc(key, pm, c.t))
		}
	}
	return names
}

// group is the samples of one series while the index is assembled.
type group struct {
	s      Series
	obs    []cobs
	first  cobs // the first event of the group in scan order
	uptime bool
}

// build resolves identities and assembles the index. It walks the events one
// series key at a time and frees each key's rows once they sit in their group,
// so the compact rows are held about once, not twice.
func (b *builder) build() Index {
	x := Index{window: b.w, partial: b.partial}
	if b.partial {
		x.gaps = append(x.gaps, GapPartial)
	}
	names := b.pidNames()
	for k := range b.starts {
		ts := b.starts[k]
		sort.Slice(ts, func(i, j int) bool { return ts[i].Before(ts[j]) })
	}

	// Resolve each event's identity and group it into a series.
	groups := map[string]*group{}
	for ki := range b.keys {
		key, rows := &b.keys[ki], b.rows[ki]
		var pm map[int64]int
		if key.legacy {
			pm = b.pidAt[pidKey(key.host, key.source, key.class, key.proc.Instance)]
		}
		snap := key.source == sourceProcSnap || key.source == sourceWinLive || key.source == sourcePidstat
		var (
			have      bool
			in, named Proc
			ts        []time.Time
			lastFinal Proc
			last      *group
		)
		for _, c := range rows {
			p := legacyProc(key, pm, c.t)
			if !have || p != in {
				in, named, ts, have = p, p, nil, true
				if named.PID > 0 && named.Name == "" {
					if set := names[key.host+"\x00"+strconv.Itoa(named.PID)]; len(set) == 1 {
						for n := range set {
							named.Name = n
						}
					}
				}
				if named.PID > 0 && snap {
					ts = b.starts[startKey(key.host, named.Name, named.PID)] // latest start at or before the sample
				}
			}
			final := named
			if len(ts) > 0 {
				if j := sort.Search(len(ts), func(j int) bool { return ts[j].After(c.t) }); j > 0 {
					final.Start = ts[j-1]
				}
			}
			g := last
			if g == nil || final != lastFinal {
				startNano := int64(0)
				if !final.Start.IsZero() {
					startNano = final.Start.UnixNano()
				}
				gk := strings.Join([]string{key.m.ID, key.m.Unit, strconv.FormatBool(key.m.Cumulative), strconv.FormatFloat(key.m.Scale, 'g', -1, 64),
					key.host, key.source, key.label, final.Key(), strconv.FormatInt(startNano, 10), strconv.FormatBool(key.uptime)}, "\x00")
				g = groups[gk]
				if g == nil {
					g = &group{first: c, uptime: key.uptime,
						s: Series{Metric: key.m, Host: key.host, Source: key.source, Label: key.label, Proc: final}}
					if final.PID == 0 && final.Instance != "" && key.legacy {
						g.s.Gap = GapLegacyInstance
					}
					groups[gk] = g
				}
				last, lastFinal = g, final
			}
			if before(c, g.first) { // the series is described by its first event
				g.first = c
				g.s.Proc, g.s.Gap = final, ""
				if final.PID == 0 && final.Instance != "" && key.legacy {
					g.s.Gap = GapLegacyInstance
				}
			}
			g.obs = append(g.obs, c)
		}
		b.rows[ki] = nil
	}
	order := make([]*group, 0, len(groups))
	for _, g := range groups {
		order = append(order, g)
	}
	sort.Slice(order, func(i, j int) bool { return before(order[i].first, order[j].first) })

	// Samples, sorted and de-duplicated by time, and the cadence of each series.
	cadence := map[string][]time.Duration{} // source|host -> series intervals
	for _, g := range order {
		slices.SortFunc(g.obs, func(a, c cobs) int {
			if d := a.t.Compare(c.t); d != 0 {
				return d
			}
			if d := strings.Compare(a.id, c.id); d != 0 {
				return d
			}
			if before(a, c) != before(c, a) {
				if before(a, c) {
					return -1
				}
				return 1
			}
			return 0
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
			g.s.Samples = samplesOf(&g.s, g.obs)
			g.obs = nil
		}
	}
	for _, g := range order {
		if g.s.Interval == 0 {
			if d := median(cadence[g.s.Source+"\x00"+g.s.Host]); d > 0 {
				g.s.Interval = d
			} else if d := median(cadence[g.s.Source]); d > 0 {
				g.s.Interval = d
			} else if g.s.Gap == "" {
				g.s.Gap = GapNoCadence
			}
			g.s.Samples = samplesOf(&g.s, g.obs)
			g.obs = nil
		}
	}

	// Coverage window: open bounds take the extent of the samples.
	var first, last time.Time
	for _, g := range order {
		for _, sm := range g.s.Samples {
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
	for _, g := range order {
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
	for _, g := range order {
		if g.uptime {
			up = append(up, g.s)
		}
	}
	x.markers = b.markers(x.series, up)
	return x
}

// samplesOf makes the samples of series s from its sorted, de-duplicated
// events, once s.Interval is known.
func samplesOf(s *Series, os []cobs) []Sample {
	out := make([]Sample, 0, len(os))
	for i, o := range os {
		sm := Sample{Start: o.t.Add(-s.Interval), End: o.t, Value: o.v, EvidenceID: o.id}
		if o.ownSpan {
			sm.Start = o.start
		}
		if s.Metric.Cumulative && i > 0 {
			prev := os[i-1].v
			if o.v < prev {
				sm.Reset = true
			} else {
				sm.Delta, sm.HasDelta = o.v-prev, true
			}
		}
		out = append(out, sm)
	}
	return out
}

// medianGap is the median step between consecutive sample times, or 0.
func medianGap(os []cobs) time.Duration {
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
