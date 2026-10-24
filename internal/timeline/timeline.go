// Package timeline builds the thin cross-source timeline (card P8): a
// clock-aligned list of audit intervals and the imported events that overlap
// them. It applies no composite rule and states no cause. Every row carries
// one of the roadmap §6.6 wordings (built only by internal/evidence) and an
// evidence label, and every wording passes evidence.LintCausal.
//
// The timeline is enabled only when the case has at least two sources and each
// of them has a validated time zone rule. Otherwise Build returns a disabled
// View that says why and lists no rows, because clocks that cannot be aligned
// must not be shown side by side.
package timeline

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/join"
)

// Row kinds.
const (
	KindAudit   = "audit"   // an audit interval, worded with AuditCreate
	KindOverlap = "overlap" // an imported event that overlaps an interval
	KindAbsence = "absence" // a source that shows no event for an interval
)

// Reason codes for a disabled timeline.
const (
	ReasonNoSources    = "no-sources"
	ReasonSingleSource = "single-source"
	ReasonTZRule       = "tz-rule"
)

// Fixed sentences, in plain words. Source names are importer names, never
// file content.
const (
	textNoSources    = "The timeline needs at least 2 sources. No file has been imported yet."
	textSingleSource = "The timeline needs at least 2 sources. Only 1 has been imported."
	textTZRulePrefix = "The timeline needs a checked time zone rule for each source. Missing for: "
)

// Detail sentences for the gap wording of an absence row.
const (
	gapOutsideRange   = "this interval is outside the time range of the file"
	gapNoThreshold    = "this source states no recording threshold"
	gapNoRange        = "the time range of this file is not known"
	gapThresholdsDiff = "the files from this source state different thresholds"
)

// edgeMargin widens the event query beyond the window so that an event that
// starts just before it, and still overlaps an interval inside it, is seen.
const edgeMargin = 10 * time.Minute

// tolerance is the join tolerance: one second of timestamp resolution, the same
// as rule M1. Skew between sources is not guessed.
var tolerance = join.Tolerance{Resolution: time.Second}

// SourceStatus is one source as the timeline sees it.
type SourceStatus struct {
	Name      string   `json:"name"`      // importer name
	Hosts     []string `json:"hosts"`     // host labels of its files, sorted
	TZRules   []string `json:"tzRules"`   // clock.Rule* of its files, sorted
	Validated bool     `json:"validated"` // every file has a validated rule
	Files     int      `json:"files"`
}

// Row is one line of the timeline. Wording is the exact §6.6 sentence and
// Label is Symptom or Correlation.
type Row struct {
	Kind       string             `json:"kind"`
	Time       time.Time          `json:"time"` // UTC; interval start or event start
	End        time.Time          `json:"end"`  // UTC
	IntervalID string             `json:"intervalId"`
	Source     string             `json:"source,omitempty"`
	Host       string             `json:"host,omitempty"`
	EventKind  string             `json:"eventKind,omitempty"`
	EvidenceID string             `json:"evidenceId,omitempty"`
	DurationMs *float64           `json:"durationMs,omitempty"`
	Join       evidence.JoinClass `json:"join,omitempty"`
	Candidates int                `json:"candidates,omitempty"` // k, on every row of an ambiguous match
	Label      string             `json:"label"`
	Wording    string             `json:"wording"`
}

// View is the result of Build.
type View struct {
	Enabled    bool            `json:"enabled"`
	ReasonCode string          `json:"reasonCode,omitempty"`
	Reason     string          `json:"reason,omitempty"` // set exactly when Enabled is false
	Window     importer.Window `json:"-"`
	Sources    []SourceStatus  `json:"sources"`
	Rows       []Row           `json:"rows"`
	// Unlisted counts events read for the window that no row lists: they did
	// not overlap an interval, or have no duration to state.
	Unlisted  int  `json:"unlisted"`
	Truncated bool `json:"truncated"` // the event read hit the 50 000 cap
}

// Build lists audit intervals and the events that overlap them, aligned on
// UTC. Events are joined source by source: a unique match gets OverlapUnique,
// an ambiguous one lists every candidate with OverlapAmbiguous, and a source
// with no candidate gets a not-observed wording. Audit intervals that do not
// touch the window are skipped.
func Build(c casestore.Case, w importer.Window, ivs []join.Interval) (View, error) {
	ctx := context.Background()
	imports, err := c.Imports(ctx)
	if err != nil {
		return View{}, fmt.Errorf("timeline: imports: %w", err)
	}
	v := View{Window: w, Sources: []SourceStatus{}, Rows: []Row{}}
	files := okImports(imports)
	v.Sources = sourceStatuses(files)
	if code, text := disabled(v.Sources); code != "" {
		v.ReasonCode, v.Reason = code, text
		return v, nil
	}
	v.Enabled = true

	events, err := c.Events(ctx, casestore.EventQuery{Window: widen(w), Limit: casestore.MaxEventQuery})
	if err != nil {
		return View{}, fmt.Errorf("timeline: events: %w", err)
	}
	v.Truncated = len(events) >= casestore.MaxEventQuery

	intervals := inWindow(ivs, w)
	bySource := map[string][]evidence.Event{}
	for _, e := range events {
		bySource[e.Source] = append(bySource[e.Source], e)
	}
	listed := map[string]struct{}{}
	for _, iv := range intervals {
		row := Row{
			Kind: KindAudit, Time: iv.Start.UTC(), End: iv.End.UTC(), IntervalID: iv.ID,
			Label:   audit.EvidenceSymptom,
			Wording: evidence.AuditCreate(seconds(iv.End.Sub(iv.Start))),
		}
		v.Rows = append(v.Rows, row)
	}
	for _, src := range v.Sources {
		evs := bySource[src.Name]
		byID := make(map[string]evidence.Event, len(evs))
		for _, e := range evs {
			byID[e.EvidenceID] = e
		}
		matches := join.Overlap(intervals, evs, tolerance, qualifies)
		for i, m := range matches {
			iv := intervals[i]
			switch m.Class {
			case evidence.JoinNone:
				v.Rows = append(v.Rows, absenceRow(src.Name, iv, sourceFiles(files, src.Name)))
			default:
				for _, id := range m.Candidates {
					e := byID[id]
					listed[id] = struct{}{}
					v.Rows = append(v.Rows, overlapRow(iv, e, m))
				}
			}
		}
	}
	v.Unlisted = len(events) - len(listed)
	sortRows(v.Rows)
	for _, r := range v.Rows {
		if !evidence.ValidImporterLabel(r.Label) {
			return View{}, fmt.Errorf("timeline: row label %q not allowed", r.Label)
		}
		if err := evidence.LintCausal(r.Wording); err != nil {
			return View{}, fmt.Errorf("timeline: wording: %w", err)
		}
	}
	return v, nil
}

// okImports keeps the files that were imported (not duplicates).
func okImports(all []casestore.ImportRecord) []casestore.ImportRecord {
	var out []casestore.ImportRecord
	for _, r := range all {
		if r.Status == casestore.StatusOK || r.Status == "" {
			out = append(out, r)
		}
	}
	return out
}

func sourceFiles(files []casestore.ImportRecord, name string) []casestore.ImportRecord {
	var out []casestore.ImportRecord
	for _, f := range files {
		if f.Importer == name {
			out = append(out, f)
		}
	}
	return out
}

// validatedRule reports whether a file's time zone rule is one of the four
// known rules and, for host-local, names a host (the host map holds its zone).
// A file with no host cannot say which zone its local times are in.
func validatedRule(f casestore.ImportRecord) bool {
	switch f.TZRule {
	case clock.RuleUTCField, clock.RuleOffsetInField, clock.RuleHeaderBias:
		return true
	case clock.RuleHostLocal:
		return f.HostLabel != ""
	}
	return false
}

func sourceStatuses(files []casestore.ImportRecord) []SourceStatus {
	idx := map[string]*SourceStatus{}
	var names []string
	for _, f := range files {
		s := idx[f.Importer]
		if s == nil {
			s = &SourceStatus{Name: f.Importer, Validated: true, Hosts: []string{}, TZRules: []string{}}
			idx[f.Importer] = s
			names = append(names, f.Importer)
		}
		s.Files++
		if !validatedRule(f) {
			s.Validated = false
		}
		s.Hosts = addUnique(s.Hosts, f.HostLabel)
		s.TZRules = addUnique(s.TZRules, f.TZRule)
	}
	sort.Strings(names)
	out := make([]SourceStatus, 0, len(names))
	for _, n := range names {
		s := idx[n]
		sort.Strings(s.Hosts)
		sort.Strings(s.TZRules)
		out = append(out, *s)
	}
	return out
}

func addUnique(list []string, v string) []string {
	if v == "" {
		return list
	}
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}

// disabled returns the reason code and sentence, or "" when the timeline may run.
func disabled(sources []SourceStatus) (code, text string) {
	switch {
	case len(sources) == 0:
		return ReasonNoSources, textNoSources
	case len(sources) == 1:
		return ReasonSingleSource, textSingleSource
	}
	var bad []string
	for _, s := range sources {
		if !s.Validated {
			bad = append(bad, s.Name)
		}
	}
	if len(bad) > 0 {
		return ReasonTZRule, textTZRulePrefix + strings.Join(bad, ", ") + "."
	}
	return "", ""
}

func widen(w importer.Window) importer.Window {
	if !w.From.IsZero() {
		w.From = w.From.Add(-edgeMargin)
	}
	if !w.To.IsZero() {
		w.To = w.To.Add(edgeMargin)
	}
	return w
}

// inWindow keeps the intervals that touch the window, in input order.
func inWindow(in []join.Interval, w importer.Window) []join.Interval {
	out := make([]join.Interval, 0, len(in))
	for _, iv := range in {
		s, e := iv.Start.UTC(), iv.End.UTC()
		if e.Before(s) {
			s, e = e, s
		}
		if (!w.To.IsZero() && s.After(w.To)) || (!w.From.IsZero() && e.Before(w.From)) {
			continue
		}
		out = append(out, join.Interval{ID: iv.ID, Start: s, End: e})
	}
	return out
}

// span is the length of time an event states: its duration when it has one,
// else End - Start.
func span(e evidence.Event) time.Duration {
	if e.DurationMs != nil {
		return time.Duration(*e.DurationMs * float64(time.Millisecond))
	}
	return e.End.Sub(e.Start)
}

// qualifies keeps events that state a positive length of time. A point event
// or a counter sample has no length, so "an event of N s overlapping this
// interval" would say more than the source does.
func qualifies(e evidence.Event) bool { return span(e) > 0 }

func seconds(d time.Duration) float64 { return math.Round(d.Seconds()*1000) / 1000 }

func overlapRow(iv join.Interval, e evidence.Event, m join.Match) Row {
	r := Row{
		Kind: KindOverlap, Time: e.Start.UTC(), End: e.End.UTC(), IntervalID: iv.ID,
		Source: e.Source, Host: e.Host, EventKind: e.Kind, EvidenceID: e.EvidenceID,
		DurationMs: e.DurationMs, Join: m.Class, Label: audit.EvidenceCorrelation,
	}
	if m.Class == evidence.JoinUnique {
		r.Wording = evidence.OverlapUnique(e.Source, strings.ReplaceAll(e.Kind, "-", " "), seconds(span(e)))
		return r
	}
	r.Candidates = len(m.Candidates)
	r.Wording = evidence.OverlapAmbiguous(len(m.Candidates))
	return r
}

// absenceRow words a source that shows no qualifying event for an interval. A
// threshold wording is used only when every file of the source covers the
// interval and states one threshold. Anything less is the gap wording.
func absenceRow(source string, iv join.Interval, files []casestore.ImportRecord) Row {
	r := Row{
		Kind: KindAbsence, Time: iv.Start.UTC(), End: iv.End.UTC(), IntervalID: iv.ID,
		Source: source, Join: evidence.JoinNone, Label: audit.EvidenceSymptom,
	}
	threshold, ok := "", true
	covered, known := false, true
	for _, f := range files {
		q := f.Quality
		if q.CoverageFirst == nil || q.CoverageLast == nil {
			known = false
		} else if !iv.End.Before(*q.CoverageFirst) && !iv.Start.After(*q.CoverageLast) {
			covered = true
		}
		switch {
		case q.Threshold == "":
			ok = false
		case threshold == "":
			threshold = q.Threshold
		case threshold != q.Threshold:
			r.Wording = evidence.NotObservedGap(gapThresholdsDiff)
			return r
		}
	}
	switch {
	case !known:
		r.Wording = evidence.NotObservedGap(gapNoRange)
	case !covered:
		r.Wording = evidence.NotObservedGap(gapOutsideRange)
	case !ok || threshold == "":
		r.Wording = evidence.NotObservedGap(gapNoThreshold)
	default:
		r.Wording = evidence.NotObservedThreshold(source, threshold)
	}
	return r
}

var rank = map[string]int{KindAudit: 0, KindOverlap: 1, KindAbsence: 2}

// sortRows aligns rows on the UTC clock; ties keep an interval before the
// rows that refer to it, then break on stable keys so the order never varies.
func sortRows(rows []Row) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if !a.Time.Equal(b.Time) {
			return a.Time.Before(b.Time)
		}
		if rank[a.Kind] != rank[b.Kind] {
			return rank[a.Kind] < rank[b.Kind]
		}
		if a.IntervalID != b.IntervalID {
			return a.IntervalID < b.IntervalID
		}
		if a.Source != b.Source {
			return a.Source < b.Source
		}
		return a.EvidenceID < b.EvidenceID
	})
}
