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
	"api-audit-explorer/internal/importer/mongodlog"
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
	gapOutsideRange   = "this interval is not fully inside the time range of the file"
	gapNoThreshold    = "this source states no recording threshold"
	gapNoRange        = "the time range of this file is not known"
	gapThresholdsDiff = "the files from this source state different thresholds"
	gapTruncated      = "too many events were found, so only part of them were read"
)

// edgeMargin widens the event query beyond the window so that an event that
// starts just before it, and still overlaps an interval inside it, is seen.
const edgeMargin = 10 * time.Minute

// lookback is how far before the window a second query reaches for events that
// started long before it and still overlap an interval inside it. The importers
// cap a stated duration at 7 days (SQL Server waits) or 400 days (mongod); an event
// that began earlier than the lookback is not read, which is a stated limit of the
// timeline, not a claim about the source.
const lookback = 7 * 24 * time.Hour

// displayNames are the plain names of the sources used in wording, so that an
// importer name never reaches a sentence. An unknown source falls back to the
// importer name.
var displayNames = map[string]string{
	"sql-xe":       "system_health",
	"sql-errorlog": "SQL Server error log",
	"sql-pdh":      "SQL Server performance counters",
	"mongod-json":  mongodlog.SourceLabel,
	"iis-w3c":      "IIS log",
	"iis-httperr":  "HTTP.sys error log",
	"win-counters": "Windows performance counters",
	"win-events":   "Windows event log",
	"appinsights":  "Application Insights",
	// The importers are named schedule-hangfire and schedule-agent; the old
	// "schedule" key matched no source.
	"schedule-hangfire": "Hangfire schedule export",
	"schedule-agent":    "SQL Server Agent schedule export",
	// Wave 2 (pre-landed by W2-F).
	"log4net-text":       "log4net log",
	"log4net-xml":        "log4net XML log",
	"nlog-text":          "NLog log",
	"nginx-access":       "nginx access log",
	"apache-combined":    "Apache access log",
	"serilog-clef":       "Serilog JSON log",
	"serilog-text":       "Serilog log",
	"mel-console":        "ASP.NET Core console log",
	"mel-json":           "ASP.NET Core JSON log",
	"ancm-stdout":        "ASP.NET Core stdout log",
	"node-jsonl":         "Node proxy log",
	"dotnet-counters":    ".NET runtime counters",
	"win-procsnap":       "process snapshots",
	"win-live":           "live CPU and memory samples",
	"redis-slowlog":      "Redis SLOWLOG",
	"redis-latency":      "Redis latency monitor",
	"redis-info":         "Redis INFO samples",
	"redis-log":          "Redis log",
	"mongo-profile":      "MongoDB profiler",
	"mongo-currentop":    "MongoDB current operations",
	"mongo-serverstatus": "MongoDB serverStatus samples",
	"mongostat-json":     "mongostat samples",
	"linux-sadf":         "Linux sar samples",
	"linux-pidstat":      "Linux pidstat samples",
}

// eventPhrases are the plain event names whose kind does not read well with
// its hyphens replaced. OverlapUnique adds the article.
var eventPhrases = map[string]string{
	"slow-op":        "slow operation",
	"slow-command":   "slow command",
	"inflight-op":    "running operation",
	"app-request":    "slow request",
	"heartbeat-late": "late heartbeat",
	"proxy-command":  "slow proxy command",
	"process-gone":   "process exit window",
	"app-session":    "session marker",
}

func displayName(source string) string {
	if n, ok := displayNames[source]; ok {
		return n
	}
	return source
}

// eventPhrase is the plain name of an event for OverlapUnique.
func eventPhrase(e evidence.Event) string {
	if p, ok := eventPhrases[e.Kind]; ok {
		return p
	}
	switch e.Kind {
	case "wait":
		switch e.Attrs["wait_class"] {
		case "lock", "latch", "preemptive":
			return e.Attrs["wait_class"] + " wait"
		}
		return "wait"
	}
	return strings.ReplaceAll(e.Kind, "-", " ")
}

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
func Build(ctx context.Context, c casestore.Case, w importer.Window, ivs []join.Interval) (View, error) {
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

	intervals := inWindow(ivs, w)
	events, truncated, err := loadEvents(ctx, c, w, latestEnd(intervals))
	if err != nil {
		return View{}, fmt.Errorf("timeline: events: %w", err)
	}
	v.Truncated = truncated
	if err := ctx.Err(); err != nil {
		return View{}, fmt.Errorf("timeline: %w", err)
	}

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
		if err := ctx.Err(); err != nil {
			return View{}, fmt.Errorf("timeline: %w", err)
		}
		evs := bySource[src.Name]
		byID := make(map[string]evidence.Event, len(evs))
		for _, e := range evs {
			byID[e.EvidenceID] = e
		}
		matches := join.Overlap(intervals, evs, tolerance, qualifies)
		// shared[id] is the number of intervals for which the event is the only
		// candidate; join.Overlap marks those matches ambiguous.
		shared := map[string]int{}
		for _, m := range matches {
			if m.Class == evidence.JoinAmbiguous && len(m.Candidates) == 1 {
				shared[m.Candidates[0]]++
			}
		}
		for i, m := range matches {
			iv := intervals[i]
			switch m.Class {
			case evidence.JoinNone:
				v.Rows = append(v.Rows, absenceRow(src.Name, iv, sourceFiles(files, src.Name), v.Truncated))
			default:
				for _, id := range m.Candidates {
					e := byID[id]
					listed[id] = struct{}{}
					v.Rows = append(v.Rows, overlapRow(iv, e, m, shared[id]))
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
			bad = append(bad, displayName(s.Name))
		}
	}
	if len(bad) > 0 {
		return ReasonTZRule, textTZRulePrefix + strings.Join(bad, ", ") + "."
	}
	return "", ""
}

// widen pads the read window by edgeMargin. An audit interval can end long after
// w.To, so the upper bound reaches the later of w.To and reachTo, the latest
// interval end. Only the read widens; the window the user sees does not.
func widen(w importer.Window, reachTo time.Time) importer.Window {
	if !w.From.IsZero() {
		w.From = w.From.Add(-edgeMargin)
	}
	if !w.To.IsZero() {
		if reachTo.After(w.To) {
			w.To = reachTo
		}
		w.To = w.To.Add(edgeMargin)
	}
	return w
}

// latestEnd is the latest end among the intervals, or the zero time for none.
func latestEnd(ivs []join.Interval) time.Time {
	var last time.Time
	for _, iv := range ivs {
		if iv.End.After(last) {
			last = iv.End
		}
	}
	return last
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

// overlapRow words an event that overlaps an interval. Only a unique match is a
// Correlation. An event that is the sole candidate of several intervals is worded
// as shared (sharedWith intervals), and several candidates as ambiguous; both are
// a Symptom.
func overlapRow(iv join.Interval, e evidence.Event, m join.Match, sharedWith int) Row {
	r := Row{
		Kind: KindOverlap, Time: e.Start.UTC(), End: e.End.UTC(), IntervalID: iv.ID,
		Source: e.Source, Host: e.Host, EventKind: e.Kind, EvidenceID: e.EvidenceID,
		DurationMs: e.DurationMs, Join: m.Class, Label: audit.EvidenceSymptom,
	}
	switch {
	case m.Class == evidence.JoinUnique:
		r.Label = audit.EvidenceCorrelation
		r.Wording = evidence.OverlapUnique(displayName(e.Source), eventPhrase(e), seconds(span(e)))
	case len(m.Candidates) == 1:
		r.Wording = evidence.OverlapShared(max(sharedWith, 2))
	default:
		r.Candidates = len(m.Candidates)
		r.Wording = evidence.OverlapAmbiguous(len(m.Candidates))
	}
	return r
}

// loadEvents reads the events for the window, widened by edgeMargin (at the
// upper end out to the latest interval end, reachTo), plus the
// events that started up to lookback before it and still reach it. It reports
// whether either read hit the event cap, in which case events may be missing.
func loadEvents(ctx context.Context, c casestore.Case, w importer.Window, reachTo time.Time) ([]evidence.Event, bool, error) {
	wide := widen(w, reachTo)
	events, err := c.Events(ctx, casestore.EventQuery{Window: wide, Limit: casestore.MaxEventQuery})
	if err != nil {
		return nil, false, err
	}
	truncated := len(events) >= casestore.MaxEventQuery
	if w.From.IsZero() {
		return events, truncated, nil
	}
	before, err := c.Events(ctx, casestore.EventQuery{
		Window: importer.Window{From: wide.From.Add(-lookback), To: wide.From},
		Limit:  casestore.MaxEventQuery,
	})
	if err != nil {
		return nil, false, err
	}
	if len(before) >= casestore.MaxEventQuery {
		truncated = true
	}
	seen := make(map[string]struct{}, len(events))
	for _, e := range events {
		seen[e.EvidenceID] = struct{}{}
	}
	reach := w.From.Add(-tolerance.Total())
	for _, e := range before {
		if _, dup := seen[e.EvidenceID]; dup || e.End.Before(reach) {
			continue
		}
		events = append(events, e)
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].Start.Before(events[j].Start) })
	return events, truncated, nil
}

// absenceRow words a source that shows no qualifying event for an interval. A
// threshold wording is used only when every file of the source covers the
// interval and states one threshold. Anything less is the gap wording.
func absenceRow(source string, iv join.Interval, files []casestore.ImportRecord, truncated bool) Row {
	r := Row{
		Kind: KindAbsence, Time: iv.Start.UTC(), End: iv.End.UTC(), IntervalID: iv.ID,
		Source: source, Join: evidence.JoinNone, Label: audit.EvidenceSymptom,
	}
	// Events may be missing from the read, so nothing can be said about absence.
	if truncated {
		r.Wording = evidence.NotObservedGap(gapTruncated)
		return r
	}
	threshold := ""
	for _, f := range files {
		gap, th := fileAbsence(source, f.Quality, iv)
		if gap != "" {
			r.Wording = evidence.NotObservedGap(gap)
			return r
		}
		switch {
		case threshold == "":
			threshold = th
		case threshold != th:
			// Files that state different thresholds give no single threshold to name.
			r.Wording = evidence.NotObservedGap(gapThresholdsDiff)
			return r
		}
	}
	if threshold == "" {
		r.Wording = evidence.NotObservedGap(gapNoThreshold)
		return r
	}
	r.Wording = evidence.NotObservedThreshold(displayName(source), threshold)
	return r
}

// fileAbsence says whether one file supports the threshold wording for iv. It
// returns the fixed gap sentence naming what is missing, or "" and the threshold
// phrase of the file. The whole interval must lie inside the file's coverage.
// Sampling and restart marks, and for mongod the version rules, count as gaps.
func fileAbsence(source string, q evidence.SourceQuality, iv join.Interval) (gap, threshold string) {
	if source == mongodlog.Name {
		if g := mongodlog.AbsenceGap(q, iv); g != "" {
			return g, ""
		}
		return "", mongodlog.AbsenceThreshold(q)
	}
	slack := tolerance.Total()
	switch {
	case q.CoverageFirst == nil || q.CoverageLast == nil:
		return gapNoRange, ""
	case q.Sampling != "":
		return q.Sampling, ""
	case iv.Start.Before(q.CoverageFirst.Add(-slack)) || iv.End.After(q.CoverageLast.Add(slack)):
		return gapOutsideRange, ""
	case q.Threshold == "":
		return gapNoThreshold, ""
	}
	return "", q.Threshold
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
