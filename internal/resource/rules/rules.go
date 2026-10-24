// Package rules runs the resource rules RC1..RC9 (plan §4.3) over the resource
// catalogue of a case and a list of stalled audit intervals. Every sentence it
// writes is one of the fixed wordings in internal/evidence, and no row is
// labelled above Correlation. Coverage is checked first: a series with too few
// samples is reported as a gap and never gives a negative result.
package rules

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/join"
	"api-audit-explorer/internal/resource"
)

// Thresholds are the tunable limits of the rules. They are stated with every
// row (Row.Thresholds) so a reader sees what was tested.
type Thresholds struct {
	Quiet            time.Duration // a gap of at least Quiet between stalls starts a new episode
	ComparisonWindow time.Duration // width of one comparison window outside stalls
	MarkerLead       time.Duration // RC8: how long before an episode a marker may end
	MinCoverage      float64       // fraction of samples a series needs before it can count
	CPUHigh          float64       // RC3: host CPU at or above, percent
	MemLowBytes      float64       // RC3: available memory below, bytes
	DiskMs           float64       // RC3: disk time per transfer above, ms
	GCTimePct        float64       // RC4: time in GC at or above, percent
	Gen2Delta        float64       // RC4: new Gen 2 collections in one sample, at least
	GCPauseMin       float64       // RC4: time paused by GC above
	TPQueueSamples   int           // RC5: samples with a thread-pool queue above 0, at least
	RiseFrac         float64       // RC2: rise as a fraction of the first value, at least
	RiseBytes        float64       // RC2: rise in bytes, at least
	RiseSpan         time.Duration // RC2: segment span, at least
	RiseUpFrac       float64       // RC2: fraction of steps that go up, at least
	MinBuckets       int           // RC2: samples in a segment, at least
	TopN             int           // RC1: entries listed
}

// DefaultThresholds are the limits used when the caller sets none.
func DefaultThresholds() Thresholds {
	return Thresholds{
		Quiet:            10 * time.Minute,
		ComparisonWindow: 5 * time.Minute,
		MarkerLead:       5 * time.Minute,
		MinCoverage:      0.8,
		CPUHigh:          90,
		MemLowBytes:      500 << 20,
		DiskMs:           25,
		GCTimePct:        10,
		Gen2Delta:        1,
		GCPauseMin:       0,
		TPQueueSamples:   2,
		RiseFrac:         0.20,
		RiseBytes:        256 << 20,
		RiseSpan:         30 * time.Minute,
		RiseUpFrac:       0.70,
		MinBuckets:       12,
		TopN:             5,
	}
}

// Episode is a group of stalled intervals with less than Thresholds.Quiet
// between one and the next. IDs are the interval ids, in start order.
type Episode struct {
	Start, End time.Time
	IDs        []string
}

// Episodes groups intervals into episodes: sorted by start, an interval that
// starts less than quiet after the latest end so far joins the episode. An
// interval whose End precedes its Start is read with the two swapped. The input
// is not modified.
func Episodes(ivs []join.Interval, quiet time.Duration) []Episode {
	sorted := make([]join.Interval, len(ivs))
	for i, v := range ivs {
		if v.End.Before(v.Start) {
			v.Start, v.End = v.End, v.Start
		}
		sorted[i] = v
	}
	sort.SliceStable(sorted, func(i, j int) bool {
		if !sorted[i].Start.Equal(sorted[j].Start) {
			return sorted[i].Start.Before(sorted[j].Start)
		}
		return sorted[i].ID < sorted[j].ID
	})
	var out []Episode
	for _, v := range sorted {
		if n := len(out); n > 0 && v.Start.Sub(out[n-1].End) < quiet {
			e := &out[n-1]
			if v.End.After(e.End) {
				e.End = v.End
			}
			e.IDs = append(e.IDs, v.ID)
			continue
		}
		out = append(out, Episode{Start: v.Start, End: v.End, IDs: []string{v.ID}})
	}
	return out
}

// Row is one rule result. Wording is a fixed evidence wording; Label is
// Symptom or Correlation. Episode is nil for a row about the whole window.
type Row struct {
	ID         string
	Host       string
	Title      string
	Wording    string
	Label      string
	Thresholds string
	Evidence   []string
	Episode    *Episode
}

// Coverage is the fraction of expected samples a series holds in the window.
type Coverage struct {
	Metric   string
	Fraction float64
}

// HostSummary is the short summary of one host.
type HostSummary struct {
	Host      string
	Summary   []string
	Coverage  []Coverage
	Processes []Process // busiest first, at most TopN
}

// Result is the output of Evaluate. Slices are never nil.
type Result struct {
	From, To time.Time
	Partial  bool
	Hosts    []HostSummary
	Rules    []Row
	Gaps     []string
	Markers  []resource.Marker
	Episodes []Episode
}

// Limits of one evaluation.
const (
	overlapLookback = time.Hour // overlap events may start this long before the window
	maxStoredEvents = 200_000
	maxRowEvidence  = 50
	stallTolerance  = time.Second
)

// gapEventCap is the gap when more events matched than the rules keep.
var gapEventCap = fmt.Sprintf("the rules read only the first %d matching events: overlap and log results are partial", maxStoredEvents)

// overlapKinds are the event kinds joined with the stalls.
var overlapKinds = []string{"slow-command", "latency-spike", "slow-op", "inflight-op", "heartbeat-late"}

// logKinds are the application log kinds RC5 and RC7 read.
var logKinds = []string{"log-entry", "log-rate"}

// eval is the state of one Evaluate call.
type eval struct {
	ctx     context.Context
	w       importer.Window
	th      Thresholds
	idx     resource.Index
	imports []casestore.ImportRecord
	stalls  []join.Interval
	eps     []Episode
	epOf    map[string]int // stall id -> episode index
	slots   slotSet
	events  map[string][]evidence.Event // by kind
	partial bool

	rows      []Row
	gaps      map[string]bool
	summaries map[string][]string
	hosts     map[string]bool
	coverage  map[string][]Coverage
	covLabels map[string]map[string]bool
}

// Evaluate runs RC1..RC9 on case c over window w (UTC, inclusive; the server
// always sets both bounds) for the given stalled intervals. Stalls that do not
// touch the window are dropped. A scan that reaches the store budget gives a
// partial result, not an error; any other store error is returned.
func Evaluate(ctx context.Context, c casestore.Case, w importer.Window, stalls []join.Interval, th Thresholds) (Result, error) {
	w.From, w.To = w.From.UTC(), w.To.UTC()
	idx, err := resource.CatalogueContext(ctx, c, w)
	if err != nil {
		return Result{}, err
	}
	imports, err := c.Imports(ctx)
	if err != nil {
		return Result{}, err
	}
	e := &eval{
		ctx: ctx, w: w, th: th, idx: idx,
		events: map[string][]evidence.Event{}, epOf: map[string]int{},
		gaps: map[string]bool{}, summaries: map[string][]string{}, hosts: map[string]bool{},
		coverage: map[string][]Coverage{}, covLabels: map[string]map[string]bool{},
	}
	for _, r := range imports {
		if r.Status == casestore.StatusOK {
			e.imports = append(e.imports, r)
			if r.HostLabel != "" {
				e.hosts[r.HostLabel] = true
			}
		}
	}
	e.partial = idx.Partial()
	for _, g := range idx.Gaps() {
		e.gaps[g] = true
	}
	for _, s := range stalls {
		if s.End.Before(s.Start) {
			s.Start, s.End = s.End, s.Start
		}
		s.Start, s.End = s.Start.UTC(), s.End.UTC()
		if (w.To.IsZero() || !s.Start.After(w.To)) && (w.From.IsZero() || !s.End.Before(w.From)) {
			e.stalls = append(e.stalls, s)
		}
	}
	e.eps = Episodes(e.stalls, th.Quiet)
	for i, ep := range e.eps {
		for _, id := range ep.IDs {
			e.epOf[id] = i
		}
	}
	e.slots = newSlotSet(w, th, e.eps)
	if err := e.readEvents(c); err != nil {
		return Result{}, err
	}

	e.coverageFirst()
	e.rc1()
	e.rc2()
	e.rc3()
	e.rc4()
	e.rc5()
	e.rc6()
	e.rc7()
	e.rc8()
	e.rc9()
	return e.result(), nil
}

// readEvents reads the overlap and log events in one pass of Each.
func (e *eval) readEvents(c casestore.Case) error {
	win := e.w
	if !win.From.IsZero() {
		win.From = win.From.Add(-overlapLookback)
	}
	if !win.To.IsZero() {
		win.To = win.To.Add(e.th.Quiet)
	}
	kinds := append(append([]string(nil), overlapKinds...), logKinds...)
	stored := 0
	err := c.Each(e.ctx, casestore.EventQuery{Window: win, Kinds: kinds}, func(ev evidence.Event) error {
		switch ev.Kind {
		case "log-entry":
			if ev.Attrs["family"] != familyRedisTimeout || !e.w.Contains(ev.Start) {
				return nil
			}
		case "log-rate":
			if !e.w.Contains(ev.Start) {
				return nil
			}
		}
		if stored >= maxStoredEvents {
			e.partial = true
			e.gaps[gapEventCap] = true
			return nil
		}
		stored++
		e.events[ev.Kind] = append(e.events[ev.Kind], ev)
		if ev.Host != "" {
			e.hosts[ev.Host] = true
		}
		return nil
	})
	if errors.Is(err, casestore.ErrScanBudget) {
		e.partial = true
		e.gaps[resource.GapPartial] = true
		err = nil
	}
	if err != nil {
		return err
	}
	for k := range e.events {
		evs := e.events[k]
		sort.SliceStable(evs, func(i, j int) bool {
			if !evs[i].Start.Equal(evs[j].Start) {
				return evs[i].Start.Before(evs[j].Start)
			}
			return evs[i].EvidenceID < evs[j].EvidenceID
		})
	}
	return nil
}

// add appends a row. Evidence is sorted, de-duplicated and capped.
func (e *eval) add(r Row) {
	ids := append([]string(nil), r.Evidence...)
	sort.Strings(ids)
	out := ids[:0]
	for i, id := range ids {
		if id == "" || (i > 0 && id == ids[i-1]) {
			continue
		}
		out = append(out, id)
	}
	if len(out) > maxRowEvidence {
		out = out[:maxRowEvidence]
	}
	r.Evidence = append([]string{}, out...)
	if !evidence.ValidImporterLabel(r.Label) {
		r.Label = audit.EvidenceSymptom
	}
	e.rows = append(e.rows, r)
}

func (e *eval) gap(s string) {
	if s != "" {
		e.gaps[s] = true
	}
}

func (e *eval) summary(host, line string) {
	if host != "" && line != "" {
		e.summaries[host] = append(e.summaries[host], line)
		e.hosts[host] = true
	}
}

// episode returns a copy of episode i for a row.
func (e *eval) episode(i int) *Episode {
	ep := e.eps[i]
	ep.IDs = append([]string{}, ep.IDs...)
	return &ep
}

// stallsOf returns the stalls of episode i.
func (e *eval) stallsOf(i int) []join.Interval {
	var out []join.Interval
	for _, s := range e.stalls {
		if e.epOf[s.ID] == i {
			out = append(out, s)
		}
	}
	return out
}

// inEpisode reports whether [start, end] touches a stall of episode i, with
// one second of slack.
func (e *eval) inEpisode(i int, start, end time.Time) bool {
	for _, s := range e.stalls {
		if e.epOf[s.ID] != i {
			continue
		}
		if !end.Before(s.Start.Add(-stallTolerance)) && !start.After(s.End.Add(stallTolerance)) {
			return true
		}
	}
	return false
}

// importsOf lists the imports of the given importers, by host label.
func (e *eval) importsOf(importers ...string) map[string][]casestore.ImportRecord {
	want := map[string]bool{}
	for _, n := range importers {
		want[n] = true
	}
	out := map[string][]casestore.ImportRecord{}
	for _, r := range e.imports {
		if want[r.Importer] {
			out[r.HostLabel] = append(out[r.HostLabel], r)
		}
	}
	return out
}

// result assembles the Result in a fixed order.
func (e *eval) result() Result {
	res := Result{From: e.w.From, To: e.w.To, Partial: e.partial,
		Hosts: []HostSummary{}, Rules: e.rows, Gaps: []string{}, Markers: e.idx.Markers(), Episodes: []Episode{}}
	if res.Rules == nil {
		res.Rules = []Row{}
	}
	sort.SliceStable(res.Rules, func(i, j int) bool {
		a, b := res.Rules[i], res.Rules[j]
		if a.ID != b.ID {
			return a.ID < b.ID
		}
		if a.Host != b.Host {
			return a.Host < b.Host
		}
		as, bs := episodeStart(a.Episode), episodeStart(b.Episode)
		if !as.Equal(bs) {
			return as.Before(bs)
		}
		if a.Title != b.Title {
			return a.Title < b.Title
		}
		return a.Wording < b.Wording
	})
	for g := range e.gaps {
		res.Gaps = append(res.Gaps, g)
	}
	sort.Strings(res.Gaps)
	if res.Markers == nil {
		res.Markers = []resource.Marker{}
	}
	for _, ep := range e.eps {
		ep.IDs = append([]string{}, ep.IDs...)
		res.Episodes = append(res.Episodes, ep)
	}
	procs := e.processes()
	for h := range procs {
		e.hosts[h] = true
	}
	var hosts []string
	for h := range e.hosts {
		if h != "" {
			hosts = append(hosts, h)
		}
	}
	sort.Strings(hosts)
	for _, h := range hosts {
		hs := HostSummary{Host: h, Summary: append([]string{}, e.summaries[h]...), Coverage: append([]Coverage{}, e.coverage[h]...),
			Processes: append([]Process{}, procs[h]...)}
		res.Hosts = append(res.Hosts, hs)
	}
	return res
}

func episodeStart(ep *Episode) time.Time {
	if ep == nil {
		return time.Time{}
	}
	return ep.Start
}
