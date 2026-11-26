// Package bbsource reads the hourly recordings of the Blackbox recorder (bb-YYYYMMDD-HH.ndjson, one JSON sample per
// line) into timeline events. Importer name: "blackbox-source". A bundle (unravis-blackbox bundle), a copied folder and
// a live recording are the same files, so they give the same events.
//
// What is read in this first slice (docs/blackbox/WHOLE-STACK.md 4.7): the host figures every sample carries (CPU,
// memory, disk queue and latency, free space of the volume the recordings are on), the start of each recorder run (sample 1 of a run), and the gaps
// (more than three intervals without a sample, the recorder's own rule). The other groups (SQL, Redis, IIS, TCP, events,
// dumps ...) are not read yet and the report says so.
//
// Time: the recorder writes every sample time in UTC ("t", RuleUTCField). Nothing is converted and nothing is guessed.
//
// Host: a recording holds no machine name. The events are filed under the host the person picked; without one the file is
// refused (unknown host), as the application-log importers do.
//
// Privacy: process names, log paths, service names, run ids and every other text member are never read. What leaves a
// line is its time and a few numbers. A value the recorder could not read is missing from the line and gives no event:
// it is never turned into 0.
package bbsource

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/textio"
	"api-audit-explorer/internal/importer/wincsv"
)

// Name is the stable importer name. Cases store it: never rename it.
const Name = "blackbox-source"

// SupportedFormat is the recording format version this reader understands (blackbox.FormatVersion; a test keeps them equal).
const SupportedFormat = 1

// LateIntervals is the recorder's own rule for a gap: more than this many intervals between two samples
// (blackbox.LateIntervals; a test keeps them equal).
const LateIntervals = 3

// Event kinds.
const (
	KindCounter  = "counter"        // one figure of one sample
	KindRunStart = "recorder-start" // the first sample of a recorder run
	KindGap      = "recording-gap"  // a stretch with no sample
)

// Fixed known-gap sentences. Each passes evidence.LintCausal.
const (
	GapGroupsNotRead = "Only the host figures, the start of each recorder run (sample 1 of a run) and the gaps of a Blackbox recording are read so far; " +
		"its SQL, Redis, IIS, network, event and dump groups are not in the timeline yet."
	GapNoHostName    = "A Blackbox recording holds no machine name; its events are filed under the host that was picked."
	GapIntervalGuess = "The recording interval is taken from the spacing of the samples in this file; the file does not state it."
)

// maxGapEvents bounds the gap events of one file, so a recording of mostly holes cannot flood the timeline.
const maxGapEvents = 1000

var detectHead = regexp.MustCompile(`^\{\s*"v"\s*:\s*[0-9]+\s*,\s*"run"\s*:\s*"[^"\n]*"\s*,\s*"seq"\s*:\s*[0-9]+\s*,\s*"t"\s*:\s*"\d{4}-\d{2}-\d{2}T`)

// New returns the importer.
func New() importer.Importer { return &imp{} }

type imp struct{}

func (*imp) Name() string { return Name }

// AllowedAttrs: the counter is named by object, counter and instance like a Windows counter, and a gap says how many
// intervals it spans.
func (*imp) AllowedAttrs() map[string]bool {
	return map[string]bool{"object": true, "counter": true, "instance": true, "intervals": true}
}

func (*imp) DefaultLimits() importer.Limits {
	return importer.Limits{
		MaxBytes: 256 << 20, MaxRows: 1_000_000, MaxEvents: 2_000_000,
		MaxLineBytes: 1 << 20, MaxFields: wincsv.DefaultMaxFields,
		MaxRejectFraction: wincsv.DefaultRejectFrac,
	}
}

// Detect scores a head whose first non-blank line starts the way every Blackbox sample does: v, run, seq, t.
func (*imp) Detect(head []byte, _ string) int {
	s := wincsv.HeadText(head)
	for s != "" {
		var l string
		l, s, _ = strings.Cut(s, "\n")
		if l = strings.TrimSpace(l); l == "" {
			continue
		}
		if detectHead.MatchString(l) {
			return 95
		}
		return 0
	}
	return 0
}

// line holds the members a sample is read for. Every other member is skipped by the decoder and never copied.
type line struct {
	V           int      `json:"v"`
	Run         string   `json:"run"`
	Seq         uint64   `json:"seq"`
	T           string   `json:"t"`
	CPU         *float64 `json:"cpu"`
	MemAvail    *float64 `json:"memAvail"`
	MemCommit   *float64 `json:"memCommit"`
	DiskQueue   *float64 `json:"diskQueue"`
	DiskMs      *float64 `json:"diskMs"`
	DiskFreePct *float64 `json:"diskFreePct"`
}

// figure is one number of a sample and how the timeline names it. The names are the Windows counter names, so a recording
// and a counter export of the same host land in the same lane.
type figure struct {
	object, counter, instance, unit string
	max                             float64 // the value may not pass this (0: no upper bound)
	scale                           float64 // multiplied into the value (0: as it is)
	get                             func(*line) *float64
}

// figures is the one place that says which sample members become counters, so a chart and the timeline cannot differ.
// The recorder writes disk latency in milliseconds; the Windows counter "Avg. Disk sec/Transfer" is in seconds, so it is
// scaled to keep one meaning per counter name.
var figures = []figure{
	{"Processor", "% Processor Time", "_Total", "percent-host", 100.5, 0, func(l *line) *float64 { return l.CPU }},
	{"Memory", "Available Bytes", "", "bytes", 0, 0, func(l *line) *float64 { return l.MemAvail }},
	{"Memory", "Committed Bytes", "", "bytes", 0, 0, func(l *line) *float64 { return l.MemCommit }},
	{"PhysicalDisk", "Current Disk Queue Length", "_Total", "count", 0, 0, func(l *line) *float64 { return l.DiskQueue }},
	{"PhysicalDisk", "Avg. Disk sec/Transfer", "_Total", "", 0, 0.001, func(l *line) *float64 { return l.DiskMs }},
	{"LogicalDisk", "% Free Space", "", "percent", 100.5, 0, func(l *line) *float64 { return l.DiskFreePct }},
}

// stamp is what the gap rule needs of an accepted sample.
type stamp struct {
	t    time.Time
	line int64
	run  string
}

func (m *imp) Parse(ctx context.Context, r io.Reader, opts importer.Options, emit func(evidence.Event) error) (importer.Report, error) {
	lim := wincsv.MergeLimits(opts.Limits, m.DefaultLimits())
	acc := wincsv.NewAcc(Name, lim)
	if err := ctx.Err(); err != nil {
		return acc.Fail(err)
	}
	pr, err := wincsv.Prepare(r, lim)
	if err != nil {
		return acc.Fail(err)
	}
	if opts.Host == nil {
		return acc.Fail(importer.ErrUnknownHost)
	}
	lr, _, err := textio.NewLineReader(pr, lim.MaxLineBytes)
	if err != nil {
		return acc.Fail(err)
	}
	host := opts.Host.Label
	acc.Rep.Quality.SourceVersion = "blackbox format 1"
	acc.Rep.Quality.TimezoneRule = clock.RuleUTCField
	acc.Rep.FieldRules["t"] = clock.RuleUTCField
	acc.Gap(GapGroupsNotRead)
	acc.Gap(GapNoHostName)

	var stamps []stamp
	dropped := false
	for {
		if err := ctx.Err(); err != nil {
			return acc.Fail(err)
		}
		raw, no, err := lr.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			reason, ok := wincsv.RowReason(err)
			if !ok {
				return acc.Fail(err)
			}
			if e := acc.Row(no); e != nil {
				return acc.Fail(e)
			}
			acc.Reject(no, reason)
			continue
		}
		raw = bytes.TrimSpace(raw)
		if len(raw) == 0 {
			continue
		}
		if e := acc.Row(no); e != nil {
			return acc.Fail(e)
		}
		var l line
		if json.Unmarshal(raw, &l) != nil {
			acc.Reject(no, importer.ReasonFieldCount)
			continue
		}
		if l.V > SupportedFormat {
			return acc.Fail(&importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat})
		}
		if l.V < 1 {
			acc.Reject(no, importer.ReasonFieldCount)
			continue
		}
		t, ok := parseTime(l.T)
		if !ok {
			acc.Reject(no, importer.ReasonBadTime)
			continue
		}
		evs, reason := buildEvents(&l, t, no, opts, host)
		if reason != "" {
			acc.Reject(no, reason)
			continue
		}
		stamps = append(stamps, stamp{t: t, line: no, run: l.Run})
		if l.Seq == 1 { // sample 1 of a run: the recorder started here (a later hourly file of the same run does not)
			e := wincsv.NewEvent(opts, Name, no, 100)
			e.Kind, e.Host, e.Start, e.End = KindRunStart, host, t, t
			e.Severity, e.TZRule, e.Attrs = "info", clock.RuleUTCField, map[string]string{}
			evs = append(evs, e)
		}
		kept := evs[:0]
		for _, e := range evs {
			if opts.Window.Overlaps(e.Start, e.End) {
				kept = append(kept, e)
			} else {
				dropped = true
			}
		}
		if e := acc.CheckEmit(len(kept)); e != nil {
			return acc.Fail(e)
		}
		for _, e := range kept {
			if err := emit(e); err != nil {
				return acc.Fail(err)
			}
			acc.Emitted(e.Start)
		}
	}
	if acc.Rep.Rows == 0 {
		return acc.Fail(textio.ErrEmpty)
	}

	// Gaps: more than LateIntervals intervals between two samples, the interval being the median spacing of the file.
	sort.SliceStable(stamps, func(i, j int) bool { return stamps[i].t.Before(stamps[j].t) })
	if iv, ok := interval(stamps); ok {
		acc.Gap(GapIntervalGuess)
		limit := time.Duration(LateIntervals) * iv
		n := 0
		for i := 1; i < len(stamps) && n < maxGapEvents; i++ {
			d := stamps[i].t.Sub(stamps[i-1].t)
			if d <= limit {
				continue
			}
			e := wincsv.NewEvent(opts, Name, stamps[i].line, 200)
			e.Kind, e.Host, e.Start, e.End = KindGap, host, stamps[i-1].t, stamps[i].t
			ms := float64(d / time.Millisecond)
			e.DurationMs = &ms
			e.Severity, e.TZRule = "warning", clock.RuleUTCField
			e.Attrs = map[string]string{"intervals": itoa(int64(d / iv))}
			if !opts.Window.Overlaps(e.Start, e.End) {
				dropped = true
				continue
			}
			if err := acc.CheckEmit(1); err != nil {
				return acc.Fail(err)
			}
			if err := emit(e); err != nil {
				return acc.Fail(err)
			}
			acc.Emitted(e.Start)
			n++
		}
	}
	if dropped {
		acc.Gap(importer.GapOutsideWindow)
	}
	return acc.Finish()
}

// buildEvents makes the counter events of one sample. reason is set when a figure is not a valid number.
func buildEvents(l *line, t time.Time, no int64, opts importer.Options, host string) ([]evidence.Event, importer.Reason) {
	var evs []evidence.Event
	for i := range figures {
		f := &figures[i]
		p := f.get(l)
		if p == nil {
			continue // not read this time: no event, never 0
		}
		v := *p
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || (f.max > 0 && v > f.max) {
			return nil, importer.ReasonBadNumber
		}
		if f.scale > 0 {
			v *= f.scale
		}
		vv := v
		e := wincsv.NewEvent(opts, Name, no, i+1)
		e.Kind, e.Host, e.Start, e.End = KindCounter, host, t, t
		e.Value, e.Unit, e.TZRule = &vv, f.unit, clock.RuleUTCField
		e.Attrs = map[string]string{"object": f.object, "counter": f.counter}
		if f.instance != "" {
			e.Attrs["instance"] = f.instance
		}
		evs = append(evs, e)
	}
	return evs, ""
}

// parseTime reads the sample time. The recorder writes UTC; a value with an offset is converted by its offset, one
// with no zone is refused (the host zone is never guessed).
func parseTime(s string) (time.Time, bool) {
	if len(s) > 48 {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, false
	}
	t = t.UTC()
	if !wincsv.SaneTime(t) {
		return time.Time{}, false
	}
	return t, true
}

// interval is the median spacing of consecutive samples of the same run (at least one second). ok is false when the file
// holds too few samples to tell.
func interval(s []stamp) (time.Duration, bool) {
	var ds []time.Duration
	for i := 1; i < len(s); i++ {
		if s[i].run != s[i-1].run {
			continue
		}
		if d := s[i].t.Sub(s[i-1].t); d > 0 {
			ds = append(ds, d)
		}
	}
	if len(ds) < 2 {
		return 0, false
	}
	sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
	m := ds[len(ds)/2]
	if m < time.Second {
		m = time.Second
	}
	return m, true
}

func itoa(n int64) string {
	if n < 0 {
		n = 0
	}
	const digits = "0123456789"
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = digits[n%10]
		n /= 10
	}
	return string(b[i:])
}
