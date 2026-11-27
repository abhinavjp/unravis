// Package bbsource reads the hourly recordings of the Blackbox recorder (bb-YYYYMMDD-HH.ndjson, one JSON sample per
// line) into timeline events. Importer name: "blackbox-source". A bundle (unravis-blackbox bundle), a copied folder and
// a live recording are the same files, so they give the same events.
//
// What is read so far (docs/blackbox/WHOLE-STACK.md 4.7): the host figures every sample carries (CPU, memory, disk queue
// and latency, free space of the volume the recordings are on), the SQL host counters, the HTTP.sys request queues, the IIS
// worker figures and the TCP figures (groups.go), the start of each recorder run (sample 1 of a run), and the gaps (more
// than three intervals without a sample, the recorder's own rule). The other groups (processes, disks, CLR ...)
// are not read yet and the report says so.
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
	GapGroupsNotRead = "Of a Blackbox recording the host figures, the SQL host counters, the HTTP request queues, the IIS worker figures, the TCP figures, the ASP.NET, memory pressure, virtual machine and disk and CPU detail figures, the Redis INFO figures, slow commands and latency spikes, event log counts, SQL log counts and new dump files, " +
		"the start of each recorder run (sample 1 of a run) and the gaps are read so far; its process, CLR and other groups are not in the timeline yet."
	GapGroupCut     = "A sample with more than 16 SQL instances, 64 HTTP queues or IIS workers, 128 Redis slow commands or 32 Redis latency spikes shows the first of them only."
	GapGroupDamaged = "A group of a sample that could not be read is left out; the host figures of that sample are kept."
	GapPartialSum   = "A TCP figure that the recorder marked as covering only some network adapters is left out, not shown as a machine-wide total."
	GapRedisEdges   = "Redis figures that need an earlier sample (a CPU rate, a restart read from the uptime) are not shown for the first sample of a file, " +
		"and a slow command or latency spike from more than five minutes before the first sample of a file is left out of it (the file before it holds it)."
	GapSQLInstance   = "A SQL Server log entry of a Blackbox recording does not say which SQL Server instance it came from."
	GapDumpsExisting = "A dump file that already existed more than five minutes before the first sample of a file is not shown as an event of that file."
	GapRecorderLoss  = "The recorder marked part of its event log, SQL log or dump readings as cut, unreadable or lost; the timeline shows only what was kept."
	GapNoProvider    = "A Blackbox event log entry carries no provider name, so it is shown as a plain event and never as a recognised service or application-pool event."
	GapNoHostName    = "A Blackbox recording holds no machine name; its events are filed under the host that was picked."
	GapIntervalGuess = "The recording interval is taken from the spacing of the neighbouring samples in this file; the file does not state it."
	GapFileEdges     = "Gaps are judged inside one file: a hole at the start or the end of a file, or a missing hourly file, is not shown as a gap."
	GapTooMany       = "A file with more than 1000 gaps shows the first 1000 only."
)

// VolumeInstance names the one volume the recorder measures free space on (the volume its recordings are on). It is not a
// drive letter and not "_Total", so a lane join cannot take it for another volume or for the whole host.
const VolumeInstance = "recordings-volume"

// Event index numbers of the events that are not counters, so their ids can never meet a counter's on the same line.
const (
	idxRunStart = 1 << 20
	idxGap      = 1<<20 + 1
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
	return map[string]bool{"object": true, "counter": true, "instance": true, "sqlinstance": true, "intervals": true, "log": true, "error_number": true, "sql_severity": true,
		"metric": true, "role": true, "command": true, "id": true, "event": true}
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

	// The groups read so far, kept raw and decoded one by one (groups.go), so one damaged group costs only itself and
	// never the host figures or the stamp of the sample. Every other group is skipped by the decoder.
	SQLHost json.RawMessage `json:"sqlHost"`
	HTTP    json.RawMessage `json:"http"`
	IIS     json.RawMessage `json:"iis"`
	TCP     json.RawMessage `json:"tcp"`
	Redis   json.RawMessage `json:"redis"`
	Events  json.RawMessage `json:"events"`
	SQLLog  json.RawMessage `json:"sqlLog"`
	Dumps   json.RawMessage `json:"dumps"`

	// Host-wide groups: one set of figures per sample, no client names.
	ASPNET      json.RawMessage `json:"aspnet"`
	MemPressure json.RawMessage `json:"memPressure"`
	VM          json.RawMessage `json:"vm"`
	DiskCPU     json.RawMessage `json:"diskCpu"`
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
	{"Memory", "Available Bytes", "", "", 0, 0, func(l *line) *float64 { return l.MemAvail }},
	{"Memory", "Committed Bytes", "", "", 0, 0, func(l *line) *float64 { return l.MemCommit }},
	{"PhysicalDisk", "Current Disk Queue Length", "_Total", "", 0, 0, func(l *line) *float64 { return l.DiskQueue }},
	{"PhysicalDisk", "Avg. Disk sec/Transfer", "_Total", "", 0, 0.001, func(l *line) *float64 { return l.DiskMs }},
	{"LogicalDisk", "% Free Space", VolumeInstance, "", 100.5, 0, func(l *line) *float64 { return l.DiskFreePct }},
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
	var nt notes
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
		// The recorder wrote this sample, so it counts for the gap rule and for the run start even when a figure in it is
		// not a valid number; only that sample's figures are then left out.
		stamps = append(stamps, stamp{t: t, line: no, run: l.Run})
		if nt.firstT.IsZero() {
			nt.firstT = t
		}
		evs, reason := buildEvents(&l, t, no, opts, host, &nt)
		if reason != "" {
			acc.Reject(no, reason)
		}
		if l.Seq == 1 { // sample 1 of a run: the recorder started here (a later hourly file of the same run does not)
			e := wincsv.NewEvent(opts, Name, no, idxRunStart)
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

	// Gaps: more than LateIntervals intervals between two samples of a run, the interval being the one the neighbouring
	// samples show, so a cadence the recorder widened on purpose is no gap; the first sample of a run is judged tighter
	// (blackbox.RestartGapIntervals), as the recorder judges it.
	sort.SliceStable(stamps, func(i, j int) bool { return stamps[i].t.Before(stamps[j].t) })
	ivs := localIntervals(stamps)
	n, capped, judged := 0, false, false
	for i := 1; i < len(stamps); i++ {
		d := stamps[i].t.Sub(stamps[i-1].t)
		var iv, limit time.Duration
		if stamps[i].run == stamps[i-1].run {
			iv = ivs[i]
			limit = time.Duration(LateIntervals) * iv
		} else {
			iv = priorInterval(stamps, ivs, i-1)
			limit = iv * RestartGapIntervalsNum / RestartGapIntervalsDen
		}
		if iv <= 0 {
			continue
		}
		judged = true
		if d <= limit {
			continue
		}
		if n >= maxGapEvents {
			capped = true
			break
		}
		e := wincsv.NewEvent(opts, Name, stamps[i].line, idxGap)
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
	if judged {
		acc.Gap(GapIntervalGuess)
	}
	acc.Gap(GapFileEdges)
	if capped {
		acc.Gap(GapTooMany)
	}
	if nt.cut {
		acc.Gap(GapGroupCut)
	}
	if nt.damaged {
		acc.Gap(GapGroupDamaged)
	}
	if nt.partial {
		acc.Gap(GapPartialSum)
	}
	if nt.redisSeen {
		acc.Gap(GapRedisEdges)
	}
	if nt.sqlLog {
		acc.Gap(GapSQLInstance)
	}
	if nt.dumpsSeen {
		acc.Gap(GapDumpsExisting)
	}
	if nt.eventsSeen {
		acc.Gap(GapNoProvider)
	}
	if nt.recorderLoss {
		acc.Gap(GapRecorderLoss)
	}
	if dropped {
		acc.Gap(importer.GapOutsideWindow)
	}
	return acc.Finish()
}

// buildEvents makes the counter events of one sample. reason is set when a figure is not a valid number.
func buildEvents(l *line, t time.Time, no int64, opts importer.Options, host string, notes *notes) ([]evidence.Event, importer.Reason) {
	var evs []evidence.Event
	var bad importer.Reason
	add := func(r reading, idx int) {
		vv := r.v
		e := wincsv.NewEvent(opts, Name, no, idx)
		e.Kind, e.Host, e.Start, e.End = KindCounter, host, t, t
		e.Value, e.Unit, e.TZRule = &vv, r.unit, clock.RuleUTCField
		e.Attrs = map[string]string{"object": r.object, "counter": r.counter}
		if r.instance != "" {
			e.Attrs["instance"] = r.instance
		}
		if r.sqlInstance != "" {
			e.Attrs["sqlinstance"] = r.sqlInstance
		}
		evs = append(evs, e)
	}
	// A figure that is not a valid number is left out by itself and the row is counted as rejected; nothing else of the
	// sample is lost. The event and SQL log entries of a sample are never sent again by the recorder, so they must survive.
	for i := range figures {
		f := &figures[i]
		p := f.get(l)
		if p == nil {
			continue // not read this time: no event, never 0
		}
		v := *p
		if !validNumber(v) || (f.max > 0 && v > f.max) {
			bad = importer.ReasonBadNumber
			continue
		}
		if f.scale > 0 {
			v *= f.scale
		}
		add(reading{object: f.object, counter: f.counter, instance: f.instance, unit: f.unit, v: v}, i+1)
	}
	rs, ok := groupReadings(l, notes)
	if !ok {
		bad = importer.ReasonBadNumber
	}
	for i, r := range rs {
		add(r, len(figures)+1+i)
	}
	if len(l.Redis) > 0 {
		evs = append(evs, redisEvents(l.Redis, t, no, opts, host, notes)...)
	}
	if len(l.Events) > 0 || len(l.SQLLog) > 0 || len(l.Dumps) > 0 {
		evs = append(evs, logEvents(l, t, no, opts, host, notes)...)
	}
	return evs, bad
}

func validNumber(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 }

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

// neighbourSpan is how many spacings on each side of a pair are looked at to tell the interval there.
const neighbourSpan = 4

// RestartGapIntervalsNum/Den are blackbox.RestartGapIntervals (1.5) as a fraction (a test keeps them equal).
const (
	RestartGapIntervalsNum = 3
	RestartGapIntervalsDen = 2
)

// localIntervals gives, for each sample i (i >= 1) that follows a sample of the same run, the interval the spacings around
// it show: the upper median of the other spacings of that run within neighbourSpan pairs on each side. A single hole is
// then judged against the cadence around it, and a cadence that changes for good is not taken for a hole. 0: no neighbour
// spacing to judge by (or no same-run predecessor).
func localIntervals(s []stamp) []time.Duration {
	out := make([]time.Duration, len(s))
	sp := make([]time.Duration, len(s)) // sp[i]: spacing from i-1 to i inside one run, else 0
	for i := 1; i < len(s); i++ {
		if s[i].run == s[i-1].run {
			if d := s[i].t.Sub(s[i-1].t); d > 0 {
				sp[i] = d
			}
		}
	}
	for i := 1; i < len(s); i++ {
		if sp[i] == 0 {
			continue
		}
		var ds []time.Duration
		for j := i - neighbourSpan; j <= i+neighbourSpan; j++ {
			if j >= 1 && j < len(s) && j != i && sp[j] > 0 && s[j].run == s[i].run {
				ds = append(ds, sp[j])
			}
		}
		if len(ds) == 0 {
			continue
		}
		sort.Slice(ds, func(a, b int) bool { return ds[a] < ds[b] })
		m := ds[len(ds)/2]
		if m < time.Second {
			m = time.Second
		}
		out[i] = m
	}
	return out
}

// priorInterval is the interval the run that ends at sample i was ticking at: the last interval known at or before i.
func priorInterval(s []stamp, ivs []time.Duration, i int) time.Duration {
	for j := i; j >= 1 && s[j].run == s[i].run; j-- {
		if ivs[j] > 0 {
			return ivs[j]
		}
	}
	return 0
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
