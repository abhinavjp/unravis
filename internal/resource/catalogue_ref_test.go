package resource

import (
	"context"
	"io"
	"math/rand"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

// This file proves that the compact catalogue build (builder.add keeps a small
// row per event; build assembles series key by key) gives the same index as the
// build it replaced, which kept the full obs struct of every event. refBuild is
// that earlier build, copied unchanged apart from taking its events as an
// argument.

var refT0 = time.Date(2001, 10, 25, 1, 0, 0, 0, time.UTC)

func refAt(d time.Duration) time.Time { return refT0.Add(d) }

func refPtr(v float64) *float64 { return &v }

func refAttrs(kv ...string) map[string]string {
	if len(kv) == 0 {
		return nil
	}
	m := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return m
}

func refPDH(host string, t time.Time, object, inst, counter string, v float64, unit string) evidence.Event {
	a := map[string]string{"object": object, "counter": counter}
	if inst != "" {
		a["instance"] = inst
	}
	return evidence.Event{Source: "win-counters", Kind: "counter", Host: host, Start: t, End: t, Value: refPtr(v), Unit: unit,
		TZRule: clock.RuleHeaderBias, Attrs: a, Line: 1}
}

func refMetric(src, host string, t time.Time, v float64, unit string, kv ...string) evidence.Event {
	return evidence.Event{Source: src, Kind: "metric", Host: host, Start: t, End: t, Value: refPtr(v), Unit: unit,
		TZRule: clock.RuleUTCField, Attrs: refAttrs(kv...), Line: 1}
}

func refPoint(src, kind, host string, t time.Time, kv ...string) evidence.Event {
	return evidence.Event{Source: src, Kind: kind, Host: host, Start: t, End: t, TZRule: clock.RuleUTCField, Attrs: refAttrs(kv...), Line: 1}
}

// refCase imports events into a fresh in-memory case, one file per source, in
// the order given.
func refCase(t testing.TB, evs []evidence.Event) casestore.Case {
	t.Helper()
	ctx := context.Background()
	store := casestore.NewMemory()
	info, err := store.Create(ctx, "resource-ref")
	if err != nil {
		t.Fatal(err)
	}
	c, err := store.Open(ctx, info.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	var order []string
	bySrc := map[string][]evidence.Event{}
	for _, e := range evs {
		if _, ok := bySrc[e.Source]; !ok {
			order = append(order, e.Source)
		}
		bySrc[e.Source] = append(bySrc[e.Source], e)
	}
	for n, src := range order {
		list := bySrc[src]
		nonce := "n" + strconv.Itoa(n+1)
		for i := range list {
			list[i].EvidenceID = evidence.EventID(string(info.ID), src, nonce, int64(i+1), 0)
			list[i].Line = int64(i + 1)
		}
		run := func(r io.Reader, emit func(evidence.Event) error) (importer.Report, error) {
			rep := importer.Report{Importer: src}
			if _, err := io.Copy(io.Discard, r); err != nil {
				return rep, err
			}
			for _, e := range list {
				if err := emit(e); err != nil {
					return rep, err
				}
				rep.Emitted++
			}
			return rep, nil
		}
		if _, err := c.Import(ctx, casestore.ImportMeta{Importer: src, FileName: "f.txt", AckGating: true, AckText: "ack"},
			strings.NewReader("file "+nonce), run); err != nil {
			t.Fatalf("import %s: %v", src, err)
		}
	}
	return c
}

// refMixed is a synthetic case that exercises every identity path of the
// catalogue: legacy instances re-keyed by their pid counter (with a pid that
// moves and a sample with none), pid-only dotnet-counters series named by the
// same pid elsewhere, procsnap series split at process starts (one before the
// window), cumulative counters with resets, uptime-like values, single-sample
// series, own-span events, duplicate timestamps, and events out of order.
func refMixed(seed int64) []evidence.Event {
	rng := rand.New(rand.NewSource(seed))
	var evs []evidence.Event
	step := 15 * time.Second
	for i := 0; i < 80; i++ {
		tm := refAt(time.Duration(i) * step)
		pid := 1240
		if i >= 40 {
			pid = 2210
		}
		evs = append(evs,
			refPDH("web-a", tm, "Process", "w3wp#1", "Private Bytes", float64(1000+rng.Intn(500)), ""),
			refPDH("web-a", tm, ".NET CLR Memory", "w3wp#1", "# Gen 2 Collections", float64(i/9+rng.Intn(2)), ""),
			refPDH("web-a", tm, "Process", "w3wp#2", "Private Bytes", float64(900+i), ""),
			refPDH("web-a", tm, "Memory", "", "Available MBytes", float64(2000-i), ""),
			refPDH("web-a", tm, "Processor", "_Total", "% Processor Time", float64(rng.Intn(100)), "percent-host"),
			refPDH("web-b", tm, "Memory", "", "Available MBytes", float64(3000-i), ""),
		)
		if i%11 != 5 { // one sample without an ID Process counter
			evs = append(evs,
				refPDH("web-a", tm, "Process", "w3wp#1", "ID Process", float64(pid), ""),
				refPDH("web-a", tm, ".NET CLR Memory", "w3wp#1", "Process ID", float64(pid), ""))
		}
		evs = append(evs, refPDH("web-a", tm, "Process V2", "dotnet:3000", "Private Bytes", float64(5e8+i), ""))
		evs = append(evs, refPDH("web-a", tm.Add(10*time.Minute), "Process V2", "w3wp:1240", "Elapsed Time", float64([]float64{600, 615, 3, 18}[i%4]), ""))
		evs = append(evs,
			refMetric("dotnet-counters", "web-a", tm, float64(i%7), "count", "provider", "System.Runtime", "counter", "ThreadPool Queue Length", "pid", "3000"),
			refMetric("dotnet-counters", "web-a", tm, float64(i), "count", "provider", "System.Runtime", "counter", "ThreadPool Queue Length", "pid", "4000"),
			refMetric("dotnet-counters", "web-a", tm, 0.5, "per-sec", "provider", "System.Runtime", "counter", "Gen 2 GC Count", "pid", "3000"),
		)
		// procsnap: pid 1240 across a restart at 150 s (and a start before the window).
		evs = append(evs, refMetric("win-procsnap", "web-a", refAt(time.Duration(i)*time.Minute), float64(100+i), "bytes",
			"metric", "private-bytes", "process", "w3wp", "pid", "1240"))
		own := refMetric("win-procsnap", "web-a", refAt(time.Duration(i)*time.Minute), float64(i%90), "percent-host",
			"metric", "cpu", "process", "w3wp", "pid", "2210")
		own.Start = own.End.Add(-30 * time.Second)
		evs = append(evs, own)
		evs = append(evs, refMetric("mongo-serverstatus", "mongo-a", refAt(time.Duration(i)*time.Minute+30*time.Second),
			float64([]float64{100, 160, 40, 100, 160, 5}[i%6]), "count", "metric", "uptime"))
	}
	// A single-sample series on a source that has a cadence on another host, and
	// one on a source with no cadence at all.
	evs = append(evs, refPDH("web-c", refAt(time.Minute), "Memory", "", "Available MBytes", 7, ""))
	evs = append(evs, refMetric("redis-info", "redis-a", refAt(time.Minute), 3, "count", "metric", "connected_clients"))
	// Duplicate timestamps (different evidence ids) for one series.
	evs = append(evs,
		refPDH("web-a", refAt(30*time.Second), "Memory", "", "Available MBytes", 111, ""),
		refPDH("web-a", refAt(30*time.Second), "Memory", "", "Available MBytes", 222, ""))
	// Process starts and markers.
	evs = append(evs,
		refPoint("win-procsnap", "process-start", "web-a", refT0.Add(-time.Hour), "process", "w3wp", "pid", "1240"),
		refPoint("win-procsnap", "process-start", "web-a", refAt(150*time.Second), "process", "w3wp", "pid", "1240"),
		refPoint("win-procsnap", "process-start", "web-a", refAt(40*time.Minute), "process", "w3wp", "pid", "2210"),
		refPoint("win-events", "apppool-recycle", "web-a", refAt(time.Minute), "provider", "Microsoft-Windows-WAS", "id", "5074", "reason", "time-interval"),
		refPoint("mongo-serverstatus", "mongod-start", "mongo-a", refAt(90*time.Second)),
	)
	// Out of order: emit in a seeded shuffle, which also decides ties.
	rng.Shuffle(len(evs), func(i, j int) { evs[i], evs[j] = evs[j], evs[i] })
	return evs
}

// shuffled is a case whose Unordered queries arrive in a seeded random order, as
// a store reading in storage order delivers them. Ordered queries pass through.
type shuffled struct {
	casestore.Case
	seed int64
}

func (s shuffled) Each(ctx context.Context, q casestore.EventQuery, fn func(evidence.Event) error) error {
	if !q.Unordered {
		return s.Case.Each(ctx, q, fn)
	}
	var all []evidence.Event
	err := s.Case.Each(ctx, q, func(ev evidence.Event) error { all = append(all, ev); return nil })
	rand.New(rand.NewSource(s.seed)).Shuffle(len(all), func(i, j int) { all[i], all[j] = all[j], all[i] })
	for _, ev := range all {
		if ferr := fn(ev); ferr != nil {
			return ferr
		}
	}
	return err
}

// refOrder puts the obs in the order the old ordered scan delivered them.
func refOrder(seen []obs) {
	sort.SliceStable(seen, func(i, j int) bool {
		a, c := seen[i], seen[j]
		if a.metric != c.metric {
			return !a.metric
		}
		if am, cm := a.start.UnixMilli(), c.start.UnixMilli(); am != cm {
			return am < cm
		}
		return a.id < c.id
	})
}

// refRun runs the three scans of CatalogueContext with a tap on the builder.
func refRun(t testing.TB, c casestore.Case, w importer.Window, maxScan int) (*builder, []obs) {
	t.Helper()
	b := &builder{w: w, keyIdx: map[obsKey]int32{}, pidAt: map[string]map[int64]int{}, starts: map[string][]time.Time{}}
	var seen []obs
	b.tap = func(o obs) { seen = append(seen, o) }
	scan := func(q casestore.EventQuery, fn func(evidence.Event) error) {
		q.MaxScan = int64(maxScan)
		if err := c.Each(context.Background(), q, fn); err != nil {
			if err != casestore.ErrScanBudget {
				t.Fatal(err)
			}
			b.partial = true
		}
	}
	scan(casestore.EventQuery{Window: w, Kinds: sampleKinds, Unordered: true}, b.sample)
	scan(casestore.EventQuery{Window: importer.Window{To: w.To}, Kinds: []string{"process-start"}}, b.processStart)
	scan(casestore.EventQuery{Window: w, Kinds: markerKinds}, b.marker)
	refOrder(seen)
	return b, seen
}

func TestCompactBuildMatchesTheFullObsBuild(t *testing.T) {
	windows := map[string]importer.Window{
		"open":     {},
		"bounded":  {From: refT0, To: refAt(30 * time.Minute)},
		"from-mid": {From: refAt(10 * time.Minute)},
	}
	for seed := int64(1); seed <= 3; seed++ {
		base := refCase(t, refMixed(seed))
		// The second variant delivers the samples in a random order, as a store
		// reading in storage order does; the index must not change.
		for vi, c := range []casestore.Case{base, shuffled{Case: base, seed: seed * 31}} {
			for name, w := range windows {
				for _, maxScan := range []int{0, 700} { // 700 stops the first scan early: a partial index
					b, seen := refRun(t, c, w, maxScan)
					want := refBuild(b, seen)
					got := b.build()
					label := name + "/seed" + strconv.FormatInt(seed, 10) + "/max" + strconv.Itoa(maxScan) + "/shuffle" + strconv.Itoa(vi)
					if len(got.series) == 0 {
						t.Fatalf("%s: no series; the dataset exercises nothing", label)
					}
					compareIndex(t, label, got, want)
					if maxScan > 0 && !got.partial {
						t.Fatalf("%s: a tiny scan budget gave a complete index", label)
					}
					if name == "open" && maxScan == 0 {
						requireCoverage(t, label, got)
					}
				}
			}
		}
	}
}

// requireCoverage fails when the dataset stopped exercising a path the compact
// build must keep, so the equivalence above cannot pass by comparing nothing.
func requireCoverage(t *testing.T, label string, x Index) {
	t.Helper()
	var legacy, started, reset, noCadence, ownSpan, named int
	for _, s := range x.series {
		switch {
		case s.Gap == GapLegacyInstance:
			legacy++
		case s.Gap == GapNoCadence:
			noCadence++
		}
		if !s.Proc.Start.IsZero() {
			started++
		}
		if s.Proc.PID > 0 && s.Proc.Name != "" {
			named++
		}
		for _, sm := range s.Samples {
			if sm.Reset {
				reset++
			}
			if !sm.Start.Equal(sm.End.Add(-s.Interval)) {
				ownSpan++
			}
		}
	}
	if legacy == 0 || started < 2 || reset == 0 || noCadence == 0 || ownSpan == 0 || named == 0 || len(x.markers) == 0 || len(x.gaps) == 0 {
		t.Fatalf("%s: dataset coverage lost: legacy %d started %d reset %d noCadence %d ownSpan %d named %d markers %d gaps %d",
			label, legacy, started, reset, noCadence, ownSpan, named, len(x.markers), len(x.gaps))
	}
}

func compareIndex(t *testing.T, label string, got, want Index) {
	t.Helper()
	if got.partial != want.partial || !got.from.Equal(want.from) || !got.to.Equal(want.to) || !reflect.DeepEqual(got.gaps, want.gaps) {
		t.Fatalf("%s: header differs: partial %v/%v from %v/%v to %v/%v gaps %v/%v", label,
			got.partial, want.partial, got.from, want.from, got.to, want.to, got.gaps, want.gaps)
	}
	if len(got.series) != len(want.series) {
		t.Fatalf("%s: %d series, want %d", label, len(got.series), len(want.series))
	}
	for i := range want.series {
		g, w := got.series[i], want.series[i]
		if g.Metric != w.Metric || g.Host != w.Host || g.Source != w.Source || g.Label != w.Label ||
			g.Proc != w.Proc || g.Gap != w.Gap || g.Interval != w.Interval {
			t.Fatalf("%s: series %d differs:\n got %+v\nwant %+v", label, i, withoutSamples(g), withoutSamples(w))
		}
		if len(g.Samples) != len(w.Samples) {
			t.Fatalf("%s: series %d (%s) has %d samples, want %d", label, i, w.Label, len(g.Samples), len(w.Samples))
		}
		for j := range w.Samples {
			if g.Samples[j] != w.Samples[j] {
				t.Fatalf("%s: series %d (%s) sample %d: got %+v want %+v", label, i, w.Label, j, g.Samples[j], w.Samples[j])
			}
		}
	}
	if len(got.markers) != len(want.markers) {
		t.Fatalf("%s: %d markers, want %d", label, len(got.markers), len(want.markers))
	}
	for i := range want.markers {
		if !reflect.DeepEqual(got.markers[i], want.markers[i]) {
			t.Fatalf("%s: marker %d differs:\n got %+v\nwant %+v", label, i, got.markers[i], want.markers[i])
		}
	}
}

func withoutSamples(s Series) Series {
	s.Samples = nil
	return s
}

// TestCatalogueRetainsACompactRowPerEvent is the memory guard: the heap the
// builder holds per scanned event stays small (it was about 1.1 KB before the
// rewrite). It reads the allocator's counters, not a clock.
func TestCatalogueRetainsACompactRowPerEvent(t *testing.T) {
	var evs []evidence.Event
	const perSeries, nSeries = 1500, 20
	for s := 0; s < nSeries; s++ {
		for i := 0; i < perSeries; i++ {
			evs = append(evs, refPDH("web-a", refAt(time.Duration(i)*15*time.Second), "Process V2", "w"+strconv.Itoa(s)+":"+strconv.Itoa(1000+s), "Private Bytes", float64(i), ""))
		}
	}
	c := refCase(t, evs)
	n := len(evs)
	evs = nil

	heap := func() uint64 {
		runtime.GC()
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		return m.HeapAlloc
	}
	base := heap()
	b := &builder{keyIdx: map[obsKey]int32{}, pidAt: map[string]map[int64]int{}, starts: map[string][]time.Time{}}
	if err := c.Each(context.Background(), casestore.EventQuery{Kinds: sampleKinds}, b.sample); err != nil {
		t.Fatal(err)
	}
	held := heap()
	perEvent := float64(held-base) / float64(n)
	t.Logf("builder holds %.0f B per event (%d events, %d keys)", perEvent, n, len(b.keys))
	if len(b.keys) != nSeries {
		t.Fatalf("%d keys, want %d", len(b.keys), nSeries)
	}
	// obs-per-event was ~250 B alone; the compact row is 88 B plus slice growth.
	if perEvent > 160 {
		t.Errorf("builder retains %.0f B per scanned event, want under 160", perEvent)
	}
	runtime.KeepAlive(b)

	x := b.build()
	if len(x.series) != nSeries {
		t.Fatalf("%d series, want %d", len(x.series), nSeries)
	}
	for _, s := range x.series {
		if len(s.Samples) != perSeries {
			t.Fatalf("series %s has %d samples, want %d", s.Label, len(s.Samples), perSeries)
		}
	}
	for ki := range b.rows {
		if b.rows[ki] != nil {
			t.Fatalf("rows of key %d were not released by build", ki)
		}
	}
}

// refGroup is the grouping of the build this file checks against.
type refGroup struct {
	s   Series
	obs []obs
}

// refBuild is the catalogue build before the compact rewrite: every event is an
// obs, resolved in place, then grouped.
func refBuild(b *builder, all []obs) Index {
	x := Index{window: b.w, partial: b.partial}
	if b.partial {
		x.gaps = append(x.gaps, GapPartial)
	}
	all = append([]obs(nil), all...)
	refResolve(b, all)

	groups := map[string]*refGroup{}
	var order []string
	var uptimes []*refGroup
	for _, o := range all {
		startNano := int64(0)
		if !o.proc.Start.IsZero() {
			startNano = o.proc.Start.UnixNano()
		}
		k := strings.Join([]string{o.m.ID, o.m.Unit, strconv.FormatBool(o.m.Cumulative), strconv.FormatFloat(o.m.Scale, 'g', -1, 64),
			o.host, o.source, o.label, o.proc.Key(), strconv.FormatInt(startNano, 10), strconv.FormatBool(o.uptime)}, "\x00")
		g, ok := groups[k]
		if !ok {
			g = &refGroup{s: Series{Metric: o.m, Host: o.host, Source: o.source, Label: o.label, Proc: o.proc}}
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

	cadence := map[string][]time.Duration{}
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
		g.s.Interval = refMedianGap(g.obs)
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

func refResolve(b *builder, all []obs) {
	names := map[string]map[string]bool{}
	for i := range all {
		o := &all[i]
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
	for i := range all {
		o := &all[i]
		if o.proc.PID > 0 && o.proc.Name == "" {
			if set := names[o.host+"\x00"+strconv.Itoa(o.proc.PID)]; len(set) == 1 {
				for n := range set {
					o.proc.Name = n
				}
			}
		}
		if o.proc.PID > 0 && (o.source == sourceProcSnap || o.source == sourceWinLive || o.source == sourcePidstat) {
			ts := b.starts[startKey(o.host, o.proc.Name, o.proc.PID)]
			j := sort.Search(len(ts), func(j int) bool { return ts[j].After(o.t) })
			if j > 0 {
				o.proc.Start = ts[j-1]
			}
		}
	}
}

func refMedianGap(os []obs) time.Duration {
	if len(os) < 2 {
		return 0
	}
	gaps := make([]time.Duration, 0, len(os)-1)
	for i := 1; i < len(os); i++ {
		gaps = append(gaps, os[i].t.Sub(os[i-1].t))
	}
	return median(gaps)
}
