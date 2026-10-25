// Package winlive imports the batches the live CPU and memory sampler writes
// (internal/sampler). It is an internal format: Detect always scores 0, so an
// upload is never picked for it, and the server refuses it by name when a
// client asks for it over the network.
//
// A batch is a comment line and a CSV:
//
//	# unravis-win-live v1 interval=15 cores=8
//	time,scope,process,pid,start,metric,value
//
// The header holds exactly the seven columns above, in that order; a file with
// another column is refused. Times are UTC ("2001-09-11T08:00:15Z"). The rows:
//
//   - scope carry: process, pid, start, metric cpu-seconds. The cumulative CPU
//     seconds of a process at the end of the previous batch. It feeds the next
//     CPU delta and emits nothing. Carry rows come before every other row.
//   - scope host: metric host-cpu (percent of the host), mem-available or
//     mem-committed (bytes). Process columns are empty.
//   - scope proc: process, pid and start name the process (start may be empty
//     when the process could not be opened); metric cpu-seconds (cumulative; the
//     importer turns the change between two samples into a "cpu" event, percent
//     of the host's logical cores), cpu (percent of the host, used as it is),
//     working-set, private-bytes, handles or threads.
//   - scope mark: process, pid and start; metric start (a "process-start" event
//     at the start time) or gone (a "process-gone" event whose window ends at
//     the row time and begins value seconds earlier).
//
// Process names: a process value must be a server image (the procsnap list) or
// "instance-<hash>". Any other value refuses the whole file, so a sampler fault
// cannot write a raw name. The events carry the same attributes as procsnap
// (metric, process, pid), so the resource catalogue reads both alike.
package winlive

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/procsnap"
	"api-audit-explorer/internal/importer/textio"
	"api-audit-explorer/internal/importer/wincsv"
)

// Name is the importer name.
const Name = "win-live"

// Tag is the format name in the first line of a batch.
const Tag = "unravis-win-live v1"

// Columns are the batch columns, in order.
var Columns = []string{"time", "scope", "process", "pid", "start", "metric", "value"}

// Row scopes.
const (
	ScopeCarry = "carry"
	ScopeHost  = "host"
	ScopeProc  = "proc"
	ScopeMark  = "mark"
)

// Metric names in a batch.
const (
	MetricCPUSeconds   = "cpu-seconds"
	MetricCPU          = "cpu"
	MetricHostCPU      = "host-cpu"
	MetricMemAvailable = "mem-available"
	MetricMemCommitted = "mem-committed"
	MetricWorkingSet   = "working-set"
	MetricPrivateBytes = "private-bytes"
	MetricHandles      = "handles"
	MetricThreads      = "threads"
	MetricStart        = "start"
	MetricGone         = "gone"
)

// TimeFormat is how a batch writes a time.
const TimeFormat = "2006-01-02T15:04:05Z"

// Allowed sampling intervals in seconds and the largest core count a header may
// state.
var allowedIntervals = map[int]bool{5: true, 15: true, 30: true, 60: true}

const maxCores = 4096

// Fixed gap sentences.
const (
	GapSampled    = "samples list the busiest processes and the server processes at each sample time; other processes and work between two samples are not listed"
	GapCPUSkipped = "some CPU values were skipped; the cumulative CPU time went down between two samples"
)

// Header is the first line of a batch (without the line break).
func Header(intervalSeconds, cores int) string {
	return "# " + Tag + " interval=" + strconv.Itoa(intervalSeconds) + " cores=" + strconv.Itoa(cores)
}

// ColumnLine is the CSV header line of a batch (without the line break).
func ColumnLine() string { return strings.Join(Columns, ",") }

var (
	headerRe   = regexp.MustCompile(`^# ` + regexp.QuoteMeta(Tag) + ` interval=(\d{1,3}) cores=(\d{1,5})$`)
	instanceRe = regexp.MustCompile(`^instance-[0-9a-f]{4,16}$`)
)

// ValidProcess reports whether v may appear in a process cell: a server image or
// an "instance-<hash>" label.
func ValidProcess(v string) bool { return procsnap.IsServerImage(v) || instanceRe.MatchString(v) }

// utf8Reader drops a UTF-8 BOM and transcodes UTF-16 (by BOM) so the comment
// line can be read before the CSV reader sees the rest.
func utf8Reader(r io.Reader) (*bufio.Reader, error) {
	br := bufio.NewReaderSize(r, 64<<10)
	head, err := br.Peek(3)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	switch {
	case bytes.HasPrefix(head, []byte{0xEF, 0xBB, 0xBF}):
		_, _ = br.Discard(3)
	case bytes.HasPrefix(head, []byte{0xFF, 0xFE}):
		_, _ = br.Discard(2)
		dec := unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM).NewDecoder()
		return bufio.NewReaderSize(transform.NewReader(br, dec), 64<<10), nil
	case bytes.HasPrefix(head, []byte{0xFE, 0xFF}):
		_, _ = br.Discard(2)
		dec := unicode.UTF16(unicode.BigEndian, unicode.IgnoreBOM).NewDecoder()
		return bufio.NewReaderSize(transform.NewReader(br, dec), 64<<10), nil
	}
	return br, nil
}

// maxTracked bounds the processes followed at one time (a variable for tests).
var maxTracked = 100_000

// New returns the live sampler batch importer.
func New() importer.Importer { return &imp{} }

type imp struct{}

func (m *imp) Name() string { return Name }

// Detect is always 0: a batch is never picked for an upload.
func (m *imp) Detect([]byte, string) int { return 0 }

func (m *imp) AllowedAttrs() map[string]bool {
	return map[string]bool{"metric": true, "process": true, "pid": true}
}

func (m *imp) DefaultLimits() importer.Limits {
	return importer.Limits{
		MaxBytes: 16 << 20, MaxRows: 400_000, MaxEvents: 400_000,
		MaxLineBytes: 512, MaxFields: 16, // a wider header is refused by name, not by count
		MaxRejectFraction: wincsv.DefaultRejectFrac,
	}
}

// ident identifies one process: id, start time (unix nanoseconds, 0 when
// unknown) and the stored name.
type ident struct {
	pid   uint32
	start int64
	label string
}

// maxCPUPercent caps a per-process cpu value, direct or derived.
const maxCPUPercent = 1000

// cpuMark is the last cumulative CPU reading of a process.
type cpuMark struct {
	sec float64
	t   time.Time
}

// Ordinals of the events of one row, for stable evidence ids.
const (
	ordMetric = iota
	ordMark
)

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
	br, err := utf8Reader(pr)
	if err != nil {
		return acc.Fail(err)
	}
	first, _ := br.Peek(160)
	nl := bytes.IndexByte(first, '\n')
	if nl < 0 {
		return acc.Fail(&importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat})
	}
	hm := headerRe.FindStringSubmatch(strings.TrimSuffix(string(first[:nl]), "\r"))
	if hm == nil {
		return acc.Fail(&importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat})
	}
	interval, _ := strconv.Atoi(hm[1])
	cores, _ := strconv.Atoi(hm[2])
	if !allowedIntervals[interval] || cores < 1 || cores > maxCores {
		return acc.Fail(&importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat})
	}
	_, _ = br.Discard(nl + 1)
	// An empty first line keeps the line numbers of the file (the comment line
	// is line 1, the column header line 2).
	cr, _, err := textio.NewCSV(io.MultiReader(strings.NewReader("\n"), br), textio.CSVOptions{
		Comma: ',', MaxFields: lim.MaxFields, MaxLine: lim.MaxLineBytes,
	})
	if err != nil {
		return acc.Fail(err)
	}
	hdr := cr.Header()
	known := map[string]bool{}
	for _, c := range Columns {
		known[c] = true
	}
	for _, h := range hdr {
		if !known[h] {
			return acc.Fail(&importer.QuarantineError{Reason: importer.ReasonUnscrubbedColumn})
		}
	}
	if len(hdr) != len(Columns) {
		return acc.Fail(&importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat})
	}
	for i, c := range Columns {
		if hdr[i] != c {
			return acc.Fail(wincsv.MissingColumn(c))
		}
	}
	acc.Rep.Rule("time", clock.RuleUTCField)
	acc.Rep.Quality.TimezoneRule = clock.RuleUTCField
	if opts.Host == nil || opts.Host.Label == "" {
		return acc.Fail(importer.ErrUnknownHost)
	}
	acc.Gap(GapSampled)
	s := &state{
		acc: acc, opts: opts, emit: emit, host: opts.Host.Label, cores: float64(cores),
		marks: map[ident]cpuMark{},
	}
	return s.run(ctx, cr)
}

type state struct {
	acc   *wincsv.Acc
	opts  importer.Options
	emit  func(evidence.Event) error
	host  string
	cores float64
	marks map[ident]cpuMark

	dataSeen bool      // a row other than carry was read
	last     time.Time // time of the last such row
}

func (s *state) run(ctx context.Context, cr *textio.CSVReader) (importer.Report, error) {
	acc := s.acc
	for {
		if err := ctx.Err(); err != nil {
			return acc.Fail(err)
		}
		rec, line, err := cr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			reason, ok := wincsv.RowReason(err)
			if !ok {
				return acc.Fail(err)
			}
			if e := acc.Row(line); e != nil {
				return acc.Fail(e)
			}
			acc.Reject(line, reason)
			continue
		}
		if e := acc.Row(line); e != nil {
			return acc.Fail(e)
		}
		reason, err := s.row(rec, line)
		if err != nil {
			return acc.Fail(err)
		}
		if reason != "" {
			acc.Reject(line, reason)
		}
	}
	return acc.Finish()
}

func parseTime(cell string) (time.Time, bool) {
	cell = strings.TrimSpace(cell)
	t, err := time.Parse(time.RFC3339Nano, cell)
	if err != nil || !strings.HasSuffix(cell, "Z") || !wincsv.SaneTime(t) {
		return time.Time{}, false
	}
	return t.UTC(), true
}

// number reads a finite non-negative number of at most max.
func number(cell string, max float64) (float64, bool) {
	v, err := strconv.ParseFloat(strings.TrimSpace(cell), 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > max {
		return 0, false
	}
	return v, true
}

// whole reads a whole number of at most max.
func whole(cell string, max float64) (float64, bool) {
	v, ok := number(cell, max)
	return v, ok && v == math.Trunc(v)
}

// row handles one row. It returns a reject reason for the row, or an error that
// ends the parse.
func (s *state) row(rec []string, line int64) (importer.Reason, error) {
	if len(rec) != len(Columns) {
		return importer.ReasonFieldCount, nil
	}
	scope, process := rec[1], rec[2]
	// A process name that is not a server image or an instance label refuses
	// the file: nothing from it is stored.
	if process != "" && !ValidProcess(process) {
		return "", &importer.QuarantineError{Reason: importer.ReasonUnscrubbedColumn, Column: "process"}
	}
	t, ok := parseTime(rec[0])
	if !ok {
		return importer.ReasonBadTime, nil
	}
	metric := rec[5]
	switch scope {
	case ScopeCarry:
		if s.dataSeen || metric != MetricCPUSeconds {
			return importer.ReasonUnsupportedFormat, nil
		}
		id, _, reason := s.ident(rec, true)
		if reason != "" {
			return reason, nil
		}
		v, ok := number(rec[6], 1e12)
		if !ok {
			return importer.ReasonBadNumber, nil
		}
		if _, known := s.marks[id]; known || len(s.marks) < maxTracked {
			s.marks[id] = cpuMark{sec: v, t: t}
		}
		return "", nil
	case ScopeHost, ScopeProc, ScopeMark:
	default:
		return importer.ReasonUnsupportedFormat, nil
	}
	if s.dataSeen && t.Before(s.last) { // rows are in time order
		return importer.ReasonBadTime, nil
	}
	s.dataSeen, s.last = true, t

	switch scope {
	case ScopeHost:
		if process != "" || rec[3] != "" || rec[4] != "" {
			return importer.ReasonUnsupportedFormat, nil
		}
		switch metric {
		case MetricHostCPU:
			v, ok := number(rec[6], 100.5)
			if !ok {
				return importer.ReasonBadNumber, nil
			}
			return "", s.metric(line, t, "host-cpu", math.Min(v, 100), "percent-host", nil)
		case MetricMemAvailable, MetricMemCommitted:
			v, ok := whole(rec[6], 1<<62)
			if !ok {
				return importer.ReasonBadNumber, nil
			}
			return "", s.metric(line, t, metric, v, "bytes", nil)
		}
		return importer.ReasonUnsupportedFormat, nil

	case ScopeProc:
		id, startT, reason := s.ident(rec, false)
		if reason != "" {
			return reason, nil
		}
		attrs := map[string]string{"process": id.label, "pid": strconv.FormatUint(uint64(id.pid), 10)}
		switch metric {
		case MetricCPUSeconds:
			v, ok := number(rec[6], 1e12)
			if !ok {
				return importer.ReasonBadNumber, nil
			}
			prev, have := s.marks[id]
			if have || len(s.marks) < maxTracked {
				s.marks[id] = cpuMark{sec: v, t: t}
			}
			if !have {
				return "", nil
			}
			dt := t.Sub(prev.t).Seconds()
			switch d := v - prev.sec; {
			case d < 0:
				s.acc.Gap(GapCPUSkipped)
			case dt > 0:
				// Same cap as a direct cpu row, so a huge counter jump cannot
				// produce an absurd percentage.
				return "", s.metric(line, t, MetricCPU, math.Min(d/dt*100/s.cores, maxCPUPercent), "percent-host", attrs)
			}
			return "", nil
		case MetricCPU:
			v, ok := number(rec[6], maxCPUPercent)
			if !ok {
				return importer.ReasonBadNumber, nil
			}
			return "", s.metric(line, t, MetricCPU, v, "percent-host", attrs)
		case MetricWorkingSet, MetricPrivateBytes:
			v, ok := whole(rec[6], 1<<62)
			if !ok {
				return importer.ReasonBadNumber, nil
			}
			return "", s.metric(line, t, metric, v, "bytes", attrs)
		case MetricHandles, MetricThreads:
			v, ok := whole(rec[6], 1<<32)
			if !ok {
				return importer.ReasonBadNumber, nil
			}
			return "", s.metric(line, t, metric, v, "count", attrs)
		}
		_ = startT
		return importer.ReasonUnsupportedFormat, nil

	default: // ScopeMark
		id, startT, reason := s.ident(rec, true)
		if reason != "" {
			return reason, nil
		}
		attrs := map[string]string{"process": id.label, "pid": strconv.FormatUint(uint64(id.pid), 10)}
		switch metric {
		case MetricStart:
			return "", s.mark(line, "process-start", startT, startT, t, attrs)
		case MetricGone:
			v, ok := number(rec[6], 7*24*3600)
			if !ok {
				return importer.ReasonBadNumber, nil
			}
			from := t.Add(-time.Duration(v * float64(time.Second)))
			return "", s.mark(line, "process-gone", from, t, t, attrs)
		}
		return importer.ReasonUnsupportedFormat, nil
	}
}

// ident reads the process, pid and start cells of a row. The start is required
// when needStart is set.
func (s *state) ident(rec []string, needStart bool) (ident, time.Time, importer.Reason) {
	if rec[2] == "" {
		return ident{}, time.Time{}, importer.ReasonUnsupportedFormat
	}
	pid, ok := parseUintCell(rec[3])
	if !ok || pid == 0 {
		return ident{}, time.Time{}, importer.ReasonBadNumber
	}
	id := ident{pid: uint32(pid), label: rec[2]}
	var startT time.Time
	if strings.TrimSpace(rec[4]) != "" {
		t, ok := parseTime(rec[4])
		if !ok {
			return ident{}, time.Time{}, importer.ReasonBadTime
		}
		startT, id.start = t, t.UnixNano()
	} else if needStart {
		return ident{}, time.Time{}, importer.ReasonBadTime
	}
	return id, startT, ""
}

func parseUintCell(cell string) (uint64, bool) {
	v, err := strconv.ParseUint(strings.TrimSpace(cell), 10, 32)
	return v, err == nil
}

// metric emits one metric event at t.
func (s *state) metric(line int64, t time.Time, name string, v float64, unit string, proc map[string]string) error {
	if err := s.acc.CheckEmit(1); err != nil {
		return err
	}
	e := wincsv.NewEvent(s.opts, Name, line, ordMetric)
	val := v
	e.Kind, e.Host, e.Start, e.End = "metric", s.host, t, t
	e.Value, e.Unit, e.TZRule = &val, unit, clock.RuleUTCField
	e.Attrs = map[string]string{"metric": name}
	for k, x := range proc {
		e.Attrs[k] = x
	}
	if err := s.emit(e); err != nil {
		return err
	}
	s.acc.Emitted(t)
	return nil
}

// mark emits a process-start or process-gone event. Coverage is the span of
// sample times: a start time before the first sample does not widen it.
func (s *state) mark(line int64, kind string, from, to, sample time.Time, attrs map[string]string) error {
	if err := s.acc.CheckEmit(1); err != nil {
		return err
	}
	e := wincsv.NewEvent(s.opts, Name, line, ordMark)
	e.Kind, e.Host, e.Start, e.End, e.TZRule = kind, s.host, from, to, clock.RuleUTCField
	e.Attrs = attrs
	if err := s.emit(e); err != nil {
		return err
	}
	s.acc.Emitted(sample)
	return nil
}
