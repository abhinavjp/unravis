package applog

import (
	"errors"
	"math"
	"strconv"
	"strings"
	"time"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

// MaxLogEntries caps the log-entry events of one file; later entries are
// counted in log-rate only, with GapEntryCap (plan §2.3).
const MaxLogEntries = 1_000_000

// entryCap is MaxLogEntries; tests lower it.
var entryCap int64 = MaxLogEntries

// DefaultRequestMinMs is the app-request threshold when the owner states none.
const DefaultRequestMinMs = 1000

// startDedupe is the window in which a second start line ("Application
// started" after "Now listening on") adds no second app-start point.
const startDedupe = 30 * time.Second

// Known-gap sentences. Each is fixed text and passes evidence.LintCausal.
const (
	GapDropped = "Message text, templates, properties, scopes and stack frames are dropped at import; " +
		"only level, logger label, family, exception type and listed numbers are kept."
	GapEntryCap         = "More than 1,000,000 log entries; later entries are counted in log-rate only."
	GapRequestThreshold = "app-request lists only requests at or above the stated threshold; " +
		"faster requests are counted in log-rate only."
	GapThresholdUnreadable = "The stated app-request-min-ms is not a usable number; 1000 ms is used."
	GapUnknownLevel        = "Some lines have a level outside the known set; they are kept with no level."
)

// ErrRecord is returned by Add for a record a framer should never build: an
// unknown time rule or a line number below 1.
var ErrRecord = errors.New("applog: framer passed an invalid record")

// sink is the state behind Sink.
type sink struct {
	src      string
	opts     importer.Options
	emitFn   func(evidence.Event) error
	host     string
	minReqMs float64

	rep       importer.Report // rejections, coverage and gaps, merged by Finish
	emitted   int64
	entries   int64
	capped    bool
	lastStart time.Time
	loggers   map[string]string
	rates     map[rateKey]*rateAcc
}

// maxLoggerCache bounds the raw-name to label cache.
const maxLoggerCache = 10_000

func newSink(src string, opts importer.Options, emit func(evidence.Event) error) *Sink {
	s := &Sink{sink{src: src, opts: opts, emitFn: emit, minReqMs: DefaultRequestMinMs,
		loggers: map[string]string{}, rates: map[rateKey]*rateAcc{}}}
	if opts.Host != nil {
		s.host = opts.Host.Label
	}
	if v, ok := opts.Supplied[SuppliedRequestMinMs]; ok {
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f < 0 || f >= 1e12 {
			s.rep.Gap(GapThresholdUnreadable)
		} else {
			s.minReqMs = f
		}
	}
	s.rep.Gap(GapDropped)
	return s
}

// Reject counts one row the framer refused (a DST fold, an unreadable time)
// so Finish reports it with the Sink's own rejections.
func (s *Sink) Reject(line int64, reason importer.Reason) {
	s.rep.Reject(line, reason)
}

// LocalTime resolves a host-local wall time for a framer (plan §2.1). It never
// guesses: no zone gives ReasonTZUnlabelled, a DST fold ReasonDSTAmbiguous, a
// DST gap ReasonDSTNonexistent and an unreadable value ReasonBadTime. The
// reason is "" when t is good.
func LocalTime(layout, value string, loc *time.Location) (time.Time, importer.Reason) {
	if loc == nil {
		return time.Time{}, importer.ReasonTZUnlabelled
	}
	r, err := clock.ParseLocal(layout, value, loc)
	switch {
	case err != nil:
		return time.Time{}, importer.ReasonBadTime
	case r.Ambiguous:
		return time.Time{}, importer.ReasonDSTAmbiguous
	case r.Nonexistent:
		return time.Time{}, importer.ReasonDSTNonexistent
	}
	return r.UTC, ""
}

// heartbeatAsOf reads the time in `As of "10/25/2001 01:02:31 +00:00"`.
// The layout is the invariant/en-US DateTimeOffset form (verify).
const heartbeatAsOfLayout = "01/02/2006 15:04:05 -07:00"

// HeartbeatAsOf returns the time the Kestrel heartbeat sentence states, for
// console logs whose lines carry no time (plan §2.3, W2-d). It reads only the
// quoted time after `As of "` at the start of msg.
func HeartbeatAsOf(msg string) (time.Time, bool) { return HeartbeatAsOfOrder(msg, nil) }

// HeartbeatAsOfOrder is HeartbeatAsOf with the date order tracked in d, shared
// with the slash dates of the same file. A nil d reads the date on its own.
func HeartbeatAsOfOrder(msg string, d *DateOrder) (time.Time, bool) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(msg), `As of "`)
	if !ok {
		return time.Time{}, false
	}
	v, _, ok := strings.Cut(rest, `"`)
	if !ok || len(v) > 40 {
		return time.Time{}, false
	}
	v, ok = NormalizeSlashDate(v, d)
	if !ok {
		return time.Time{}, false
	}
	t, err := time.Parse(heartbeatAsOfLayout, v)
	if err != nil {
		return time.Time{}, false
	}
	return t.UTC(), true
}

func validRule(r string) bool {
	switch r {
	case clock.RuleUTCField, clock.RuleOffsetInField, clock.RuleHeaderBias, clock.RuleHostLocal:
		return true
	}
	return false
}

// send checks the event limit, emits one event and widens the coverage to it
// (coverage follows emitted events and counted lines).
func (s *Sink) send(ev evidence.Event) error {
	if err := s.opts.Limits.CheckEvents(s.emitted + 1); err != nil {
		return err
	}
	if err := s.emitFn(ev); err != nil {
		return err
	}
	s.emitted++
	if ev.Kind != KindLogRate { // a log-rate point starts on the minute; its lines were covered in add
		s.rep.Cover(ev.Start)
		s.rep.Cover(ev.End)
	}
	return nil
}

// label returns the stored logger label of a raw category.
func (s *Sink) label(cat string) (string, error) {
	if l, ok := s.loggers[cat]; ok {
		return l, nil
	}
	l, err := loggerLabel(cat, s.opts.Pseudonymize)
	if err != nil {
		return "", err
	}
	if len(s.loggers) < maxLoggerCache {
		s.loggers[cat] = l
	}
	return l, nil
}

func (s *Sink) inWindow(start, end time.Time) bool {
	w := s.opts.Window
	return (w.From.IsZero() || !end.Before(w.From)) && (w.To.IsZero() || !start.After(w.To))
}

func fmtNum(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// numAttrs are the captures copied from the family numbers to attributes.
var numAttrs = []string{AttrTimeoutMs, AttrQs, AttrQu, AttrIn, AttrWorkerBusy, AttrWorkerMin,
	AttrIOCPBusy, AttrIOCPMin, AttrStatus}

// add is Add's body. Only these values reach an event: the record time, the
// checked level, the logger label, the family, the curated exception types
// and the family's numbers. The message, exception text, raw category, event
// id and the other numbers are read here and then dropped.
func (s *Sink) add(r Record, line int64) error {
	if line < 1 || !validRule(r.Rule) {
		return ErrRecord
	}
	if r.Time.IsZero() {
		s.rep.Reject(line, importer.ReasonBadTime)
		return nil
	}
	t := r.Time.UTC()

	level, sev, ok := Level(r.Level)
	if !ok && strings.TrimSpace(r.Level) != "" {
		s.rep.Gap(GapUnknownLevel)
	}
	logger, err := s.label(strings.TrimSpace(r.Category))
	if err != nil {
		return err
	}
	family, num := classify(strings.TrimSpace(r.Category), r.EventID, r.Message, r.Exception)
	if family == FamilyRequestFinished {
		num = mergeRequestNums(num, r.Num)
	}
	exc := strings.Join(exceptionTypes(r.Exception), ",")
	in := s.inWindow(t, t)

	// log-rate counts every line in the window.
	if in {
		s.rep.Cover(t)
		if s.count(rateKey{minute: floorMinute(t), level: level, logger: logger, family: family, rule: r.Rule}, line, sev) {
			if err := s.checkBuckets(); err != nil {
				return err
			}
		}
	}

	base := func() map[string]string {
		a := map[string]string{AttrFamily: family}
		if level != "" {
			a[AttrLevel] = level
		}
		if logger != "" {
			a[AttrLogger] = logger
		}
		return a
	}
	ordinal := 0
	next := func() int { o := ordinal; ordinal++; return o }
	mk := func(kind string, start, end time.Time, dur *float64, attrs map[string]string) evidence.Event {
		return evidence.Event{
			EvidenceID: evidence.EventID(s.opts.CaseID, s.src, s.opts.ImportNonce, line, next()),
			Source:     s.src, Kind: kind, Host: s.host, Start: start, End: end, DurationMs: dur,
			Severity: sev, Line: line, TZRule: r.Rule, Attrs: attrs,
		}
	}

	// log-entry: warning or above, or a classified family. request-finished
	// below warning is left to app-request and log-rate, so ordinary requests
	// do not use up the entry cap.
	wantEntry := atLeastWarning(sev) || (family != FamilyOther && family != FamilyRequestFinished)
	if wantEntry && in {
		if s.entries >= entryCap {
			if !s.capped {
				s.capped = true
				s.rep.Gap(GapEntryCap)
			}
		} else {
			a := base()
			if exc != "" {
				a[AttrException] = exc
			}
			for _, k := range numAttrs {
				if v, ok := num[k]; ok {
					a[k] = fmtNum(v)
				}
			}
			if v, ok := num[NumMethod]; ok {
				if m, ok := methodName(v); ok {
					a[AttrMethod] = m
				}
			}
			if err := s.send(mk(KindLogEntry, t, t, nil, a)); err != nil {
				return err
			}
			s.entries++
		}
	}

	switch family {
	case FamilyRequestFinished:
		s.rep.Gap(GapRequestThreshold)
		d, ok := num[NumDuration]
		if !ok || d < s.minReqMs || d > maxDurationMs {
			return nil
		}
		start := t.Add(-msDuration(d))
		if !s.inWindow(start, t) {
			return nil
		}
		a := base()
		if v, ok := num[AttrStatus]; ok {
			a[AttrStatus] = fmtNum(v)
		}
		if v, ok := num[NumMethod]; ok {
			if m, ok := methodName(v); ok {
				a[AttrMethod] = m
			}
		}
		return s.send(mk(KindAppRequest, start, t, &d, a))
	case FamilyHeartbeatLate:
		d, ok := num[NumDuration]
		if !ok || d > maxDurationMs {
			return nil
		}
		start := t.Add(-msDuration(d))
		if !s.inWindow(start, t) {
			return nil
		}
		return s.send(mk(KindHeartbeatLate, start, t, &d, base()))
	case FamilyAppStart:
		if !s.lastStart.IsZero() && !t.Before(s.lastStart) && t.Sub(s.lastStart) <= startDedupe {
			return nil
		}
		s.lastStart = t
		if !in {
			return nil
		}
		return s.send(mk(KindAppStart, t, t, nil, base()))
	case FamilyAppStop:
		if !in {
			return nil
		}
		return s.send(mk(KindAppStop, t, t, nil, base()))
	}
	return nil
}

// mergeRequestNums lays the framer's structured request properties over the
// numbers read from the sentence. Elapsed values are milliseconds.
func mergeRequestNums(num, props map[string]float64) map[string]float64 {
	if num == nil {
		num = map[string]float64{}
	}
	ok := func(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 && v < 1e15 }
	if v, has := props[NumElapsedMs]; has && ok(v) {
		num[NumDuration] = v
	} else if v, has := props[NumElapsed]; has && ok(v) {
		num[NumDuration] = v
	}
	if v, has := props[NumStatus]; has && v == math.Trunc(v) && v >= 100 && v <= 599 {
		num[AttrStatus] = v
	}
	if v, has := props[NumMethod]; has {
		if _, valid := methodName(v); valid {
			num[NumMethod] = v
		}
	}
	return num
}

// maxDurationMs is the longest interval the core builds (about 31 years), so
// the conversion to a Duration cannot overflow.
const maxDurationMs = 1e12

// msDuration converts milliseconds to a Duration, rounding to the nanosecond.
func msDuration(ms float64) time.Duration {
	return time.Duration(math.Round(ms * float64(time.Millisecond)))
}

func floorMinute(t time.Time) int64 {
	sec := t.Unix()
	m := sec / 60
	if sec < 0 && sec%60 != 0 {
		m--
	}
	return m
}

// checkBuckets refuses the file once the rate buckets alone would pass the
// event limit, so the bucket map stays bounded by it.
func (s *Sink) checkBuckets() error {
	n := int64(len(s.rates))
	if s.opts.Limits.MaxEvents > 0 {
		return s.opts.Limits.CheckEvents(s.emitted + n)
	}
	if n > maxRateBuckets {
		return &importer.LimitError{Limit: "events", Max: maxRateBuckets, At: n}
	}
	return nil
}

// finish is Finish's body.
func (s *Sink) finish(rep *importer.Report) error {
	if err := s.flushRates(); err != nil {
		return err
	}
	rep.Emitted += s.emitted
	s.emitted = 0
	rep.Rejected += s.rep.Rejected
	for _, e := range s.rep.Errors {
		if len(rep.Errors) < importer.MaxReportErrors {
			rep.Errors = append(rep.Errors, e)
		}
	}
	if !s.rep.First.IsZero() {
		rep.Cover(s.rep.First)
		rep.Cover(s.rep.Last)
	}
	for _, g := range s.rep.KnownGaps {
		rep.Gap(g)
	}
	s.rep = importer.Report{}
	return nil
}
