// Package accesslog imports web server access logs as request-timing rows,
// the way the IIS W3C importer does: "nginx-access" for the nginx "combined"
// format and "apache-combined" for the Apache "common" and "combined" formats.
//
// Formats read (from the public nginx ngx_http_log_module and Apache
// mod_log_config documentation; no nginx or Apache was run for this card, so
// every statement below is an assumption until a real sample confirms it):
//
//	nginx  $remote_addr - $remote_user [$time_local] "$request" $status
//	       $body_bytes_sent "$http_referer" "$http_user_agent"
//	       [$request_time [$upstream_response_time]]
//	Apache %h %l %u %t "%r" %>s %b ["%{Referer}i" "%{User-agent}i"] [%D | %T]
//
// A line is cut by its shape, not by a pattern the owner supplies. A custom
// log_format (a different field order, an extra field such as
// $http_x_forwarded_for, a JSON format) does not fit: it is refused with
// "format not supported" and never guessed. nginx accepts an optional
// $request_time (decimal seconds) and an optional $upstream_response_time
// after it (read for its shape only, not stored). Apache accepts one optional
// whole number, which is %D (microseconds) or %T (seconds); the file cannot say
// which, so the owner states it (supplied "duration-unit"). Without that
// statement the field is skipped and a known gap says so.
//
// Time: [10/Oct/2000:13:55:36 -0700] carries its offset and is converted to
// UTC (RuleOffsetInField). A time that is not in that exact form, or has an
// offset outside -1400..+1400, rejects the row (bad-time); a missing offset is
// never read as UTC. Year outside 1990..2100 is bad-time. The time has
// one-second resolution. nginx writes $time_local when the request has been
// served, so it is read as the request end (Start = time - duration); Apache
// %t is documented as the time the request was received, so it is read as the
// request start (End = time + duration). Both readings are unverified (see
// gaps).
//
// Privacy, as for iis-w3c: the client address, ident, referrer, user agent and
// query string are never read into any value. The request target is reduced
// by the IIS route rules (iis.NormaliseStem): query and fragment are cut and
// ids become {id}. %u / $remote_user goes through Options.Pseudonymize like the
// IIS cs-username. Cookies and request bodies are not in these formats. Only
// method, stem, status, bytes-out and user reach an event; nothing from the
// file reaches an error, a report or a known gap.
//
// Host: these logs name no machine, so every row takes the host the user
// picked for the file.
package accesslog

import (
	"bufio"
	"context"
	"errors"
	"io"
	"math"
	"strconv"
	"strings"
	"time"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/iis"
	"api-audit-explorer/internal/importer/textio"
	"api-audit-explorer/internal/importer/wincsv"
)

// Importer names, and the key of the export policy.
const (
	NameNginx  = "nginx-access"
	NameApache = "apache-combined"
)

// SuppliedDurationUnit is the owner's statement of what the Apache trailing
// whole number is: "microseconds" (%D) or "seconds" (%T).
const SuppliedDurationUnit = "duration-unit"

// Fixed known-gap sentences. Each passes evidence.LintCausal.
const (
	gapNginxTimeEnd    = "nginx time semantics unverified; the time field is assumed to be the request end."
	gapApacheTimeStart = "Apache time semantics unverified; the time field is assumed to be the request start."
	gapResolution      = "log time has one-second resolution; the order of events within a second is not recorded."
	gapDelivery        = "The logged duration includes response delivery to the client; the residual against pipeline time is time outside the pipeline."
	gapNoDuration      = "No request duration is logged; request durations are unavailable."
	gapUnitMissing     = "A trailing duration field was not read: the owner did not state whether it is seconds or microseconds."
	gapBodyBytes       = "bytes-out counts the response body only; response headers are not included."
	gapUpstreamNote    = "The upstream response time is accepted but not stored."
)

const (
	ctxCheckEvery = 256
	// unsupportedAfter is how many lines in a row, with none read yet, must
	// fail to fit before the file is refused as an unsupported format.
	unsupportedAfter = 20
	maxDetectLines   = 20
	maxDurationMs    = 4294967295 // as iis-w3c time-taken
)

type flavor int

const (
	nginx flavor = iota
	apache
)

func (f flavor) name() string {
	if f == nginx {
		return NameNginx
	}
	return NameApache
}

func defaultLimits() importer.Limits {
	return importer.Limits{MaxBytes: 2 << 30, MaxRows: 10_000_000, MaxLineBytes: 64 << 10,
		MaxFields: 4096, MaxRejectFraction: 0.05}
}

var allowedAttrs = map[string]bool{"method": true, "stem": true, "status": true, "bytes-out": true, "user": true}

func copyAttrs() map[string]bool {
	m := make(map[string]bool, len(allowedAttrs))
	for k := range allowedAttrs {
		m[k] = true
	}
	return m
}

type nginxImp struct{}
type apacheImp struct{}

// NewNginx returns the nginx-access importer.
func NewNginx() importer.Importer { return nginxImp{} }

// NewApache returns the apache-combined importer.
func NewApache() importer.Importer { return apacheImp{} }

func (nginxImp) Name() string                      { return NameNginx }
func (apacheImp) Name() string                     { return NameApache }
func (nginxImp) DefaultLimits() importer.Limits    { return defaultLimits() }
func (apacheImp) DefaultLimits() importer.Limits   { return defaultLimits() }
func (nginxImp) AllowedAttrs() map[string]bool     { return copyAttrs() }
func (apacheImp) AllowedAttrs() map[string]bool    { return copyAttrs() }
func (nginxImp) Detect(head []byte, _ string) int  { return detect(head, nginx) }
func (apacheImp) Detect(head []byte, _ string) int { return detect(head, apache) }

func (nginxImp) Parse(ctx context.Context, r io.Reader, opts importer.Options,
	emit func(evidence.Event) error) (importer.Report, error) {
	return run(ctx, r, opts, emit, nginx)
}

func (apacheImp) Parse(ctx context.Context, r io.Reader, opts importer.Options,
	emit func(evidence.Event) error) (importer.Report, error) {
	return run(ctx, r, opts, emit, apache)
}

// SuppliedKeys declares the Apache duration unit; nginx needs no setting.
func (apacheImp) SuppliedKeys() map[string]importer.SuppliedSpec {
	return map[string]importer.SuppliedSpec{
		SuppliedDurationUnit: {Kind: importer.SuppliedEnum, Values: []string{"microseconds", "seconds"}},
	}
}

// detect scores a head. A head whose lines all fit the shared combined shape
// scores 60 (65 with two or more lines): nginx and Apache write the same
// line, so the two importers tie and the user picks. A trailing decimal
// seconds field is nginx's $request_time and scores nginx 85 (90) and Apache
// 0; a trailing whole number is Apache's %D or %T and does the opposite. A
// head that mixes the two trailing kinds, or whose lines mostly do not fit,
// scores 0 for both.
func detect(head []byte, f flavor) int {
	text := wincsv.HeadText(head)
	lines := strings.Split(text, "\n")
	if len(head) >= importer.HeadSize && len(lines) > 1 {
		lines = lines[:len(lines)-1] // the last line may be cut
	}
	var sampled, plain, secs, whole int
	for _, ln := range lines {
		ln = strings.TrimRight(ln, "\r")
		if ln == "" {
			continue
		}
		if sampled++; sampled > maxDetectLines {
			sampled--
			break
		}
		l, ok := scan(ln)
		if !ok || len(l.status) != 3 || !digits(l.status) {
			continue
		}
		if _, ok := parseTime(l.ts); !ok {
			continue
		}
		switch shape, _ := trailingShape(l.trailing); shape {
		case shapeNone:
			plain++
		case shapeSeconds:
			secs++
		case shapeWhole:
			whole++
		}
	}
	matched := plain + secs + whole
	if matched == 0 || matched*5 < sampled*4 || (secs > 0 && whole > 0) {
		return 0
	}
	bonus := 0
	if matched >= 2 {
		bonus = 5
	}
	switch {
	case secs > 0 && f == nginx, whole > 0 && f == apache:
		return 85 + bonus
	case secs > 0 || whole > 0:
		return 0
	}
	return 60 + bonus
}

type runState struct {
	rep      *importer.Report
	first    time.Time
	last     time.Time
	sawDur   bool // a row carried a duration
	trailing bool // some row had a trailing duration field
	unitless bool // an Apache trailing field was skipped for want of a unit
	upstream bool // an nginx row had an upstream time after the request time
}

func run(ctx context.Context, r io.Reader, opts importer.Options,
	emit func(evidence.Event) error, f flavor) (importer.Report, error) {

	lim := mergeLimits(opts.Limits, defaultLimits())
	rep := importer.Report{Importer: f.name(), FieldRules: map[string]string{"time": clock.RuleOffsetInField}}
	st := &runState{rep: &rep}

	fin := func(err error) (importer.Report, error) {
		rep.First, rep.Last = st.first, st.last
		if !st.first.IsZero() {
			a, b := st.first, st.last
			rep.Quality.CoverageFirst, rep.Quality.CoverageLast = &a, &b
		}
		rep.Quality.TimezoneRule = clock.RuleOffsetInField
		if opts.Host != nil {
			rep.Quality.SourceVersion = opts.Host.Runtime
		}
		rep.KnownGaps = st.gaps(f)
		return rep, err
	}

	br := bufio.NewReaderSize(importer.LimitReader(r, lim.MaxBytes), 64*1024)
	head, _ := br.Peek(16)
	if err := importer.Sniff(head); err != nil {
		return fin(err)
	}
	if opts.Host == nil || opts.Host.Label == "" {
		// the log names no machine: the file must be given one
		return fin(importer.ErrUnknownHost)
	}
	unit := opts.Supplied[SuppliedDurationUnit]
	lr, _, err := textio.NewLineReader(br, lim.MaxLineBytes)
	if err != nil {
		return fin(err)
	}

	var read, misfit, sinceChk int64
	for {
		if sinceChk++; sinceChk >= ctxCheckEvery {
			sinceChk = 0
			if err := ctx.Err(); err != nil {
				return fin(err)
			}
		}
		raw, no, err := lr.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			var re importer.RowError
			if errors.As(err, &re) {
				rep.Rows++
				if err := lim.CheckRows(rep.Rows); err != nil {
					return fin(err)
				}
				rep.Reject(no, re.Reason)
				continue
			}
			return fin(err)
		}
		if len(raw) == 0 {
			continue
		}
		rep.Rows++
		if rep.Rows > lim.MaxRows {
			return fin(&importer.LimitError{Limit: "rows", Max: lim.MaxRows, At: rep.Rows})
		}
		l, ok := scan(string(raw))
		var shape int
		var rt string
		if ok {
			shape, rt = trailingShape(l.trailing)
			ok = shape != shapeOther && (f == nginx && shape != shapeWhole || f == apache && shape != shapeSeconds)
		}
		if !ok {
			// not this format: a custom log_format, a cut line, other text
			if read == 0 {
				if misfit++; misfit >= unsupportedAfter {
					return fin(&importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat})
				}
			}
			rep.Reject(no, importer.ReasonFieldCount)
			continue
		}
		read++
		ev, reason := row(l, shape, rt, f, unit, opts, st)
		if reason != "" {
			rep.Reject(no, reason)
			continue
		}
		ev.EvidenceID = evidence.EventID(opts.CaseID, f.name(), opts.ImportNonce, no, 0)
		ev.Source, ev.Line, ev.TZRule = f.name(), no, clock.RuleOffsetInField
		if st.first.IsZero() || ev.Start.Before(st.first) {
			st.first = ev.Start
		}
		if ev.End.After(st.last) {
			st.last = ev.End
		}
		if err := emit(ev); err != nil {
			return fin(err)
		}
		rep.Emitted++
	}
	if read == 0 {
		// empty, or not one line of this format
		return fin(&importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat})
	}
	if float64(rep.Rejected)/float64(rep.Rows) > lim.MaxRejectFraction {
		return fin(&importer.QuarantineError{Reason: importer.ReasonRejectFraction})
	}
	return fin(nil)
}

// row turns one line that fits the format into an event, or a reject reason.
func row(l line, shape int, requestTime string, f flavor, unit string,
	opts importer.Options, st *runState) (evidence.Event, importer.Reason) {

	var ev evidence.Event
	ts, ok := parseTime(l.ts)
	if !ok {
		return ev, importer.ReasonBadTime
	}
	attrs := make(map[string]string, 5)
	if method, target, ok := requestParts(l.req); ok {
		if m, ok := iis.CleanToken(method, 20); ok {
			attrs["method"] = m
		}
		if s, ok := iis.NormaliseStem(target); ok {
			attrs["stem"] = s
		}
	}
	n, ok := uintOf(l.status, 599)
	if !ok || n < 100 || len(l.status) != 3 {
		return ev, importer.ReasonBadNumber
	}
	attrs["status"] = l.status
	if l.bytes != "-" {
		if _, ok := uintOf(l.bytes, 9_999_999_999); !ok {
			return ev, importer.ReasonBadNumber
		}
		attrs["bytes-out"] = l.bytes
	}
	if l.user != "-" && opts.Pseudonymize != nil {
		attrs["user"] = opts.Pseudonymize("user", l.user)
	}

	ev = evidence.Event{Kind: "request", Host: opts.Host.Label, Start: ts, End: ts, Attrs: attrs}
	ms, has, reason := duration(shape, requestTime, f, unit, st)
	if reason != "" {
		return ev, reason
	}
	if has {
		ev.DurationMs = &ms
		st.sawDur = true
		taken := time.Duration(math.Round(ms*1e3)) * time.Microsecond
		if f == nginx {
			ev.Start = ts.Add(-taken)
		} else {
			ev.End = ts.Add(taken)
		}
	}
	return ev, ""
}

// duration reads the trailing duration, in milliseconds. has is false when the
// line carries none, or an Apache one whose unit the owner did not state.
func duration(shape int, requestTime string, f flavor, unit string, st *runState) (ms float64, has bool, reason importer.Reason) {
	switch shape {
	case shapeSeconds:
		st.trailing = true
		sec, err := strconv.ParseFloat(requestTime, 64)
		if err != nil {
			return 0, false, importer.ReasonBadNumber
		}
		ms = math.Round(sec*1e6) / 1e3
	case shapeWhole:
		st.trailing = true
		n, ok := uintOf(requestTime, 999_999_999_999)
		if !ok {
			return 0, false, importer.ReasonBadNumber
		}
		switch unit {
		case "microseconds":
			ms = float64(n) / 1e3
		case "seconds":
			ms = float64(n) * 1e3
		default:
			st.unitless = true
			return 0, false, ""
		}
	default:
		return 0, false, ""
	}
	if ms > maxDurationMs {
		return 0, false, importer.ReasonBadNumber
	}
	return ms, true, ""
}

func (st *runState) gaps(f flavor) []string {
	var g []string
	if f == nginx {
		g = append(g, gapNginxTimeEnd)
	} else {
		g = append(g, gapApacheTimeStart)
	}
	g = append(g, gapResolution, gapBodyBytes)
	switch {
	case st.sawDur:
		g = append(g, gapDelivery)
	case st.unitless:
		g = append(g, gapUnitMissing, gapNoDuration)
	default:
		g = append(g, gapNoDuration)
	}
	return g
}

func mergeLimits(got, def importer.Limits) importer.Limits {
	if got.MaxBytes <= 0 {
		got.MaxBytes = def.MaxBytes
	}
	if got.MaxRows <= 0 {
		got.MaxRows = def.MaxRows
	}
	if got.MaxLineBytes <= 0 {
		got.MaxLineBytes = def.MaxLineBytes
	}
	if got.MaxFields <= 0 {
		got.MaxFields = def.MaxFields
	}
	if got.MaxRejectFraction <= 0 {
		got.MaxRejectFraction = def.MaxRejectFraction
	}
	return got
}
