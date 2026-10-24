package redis

import (
	"bytes"
	"context"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

const (
	infoMaxBytes  = 256 << 20
	infoMaxRows   = 5_000_000
	infoMaxLine   = 1 << 20
	infoMaxEvents = 2_000_000
	maxNumberLen  = 32
)

var infoAllowed = map[string]bool{attrMetric: true, attrRole: true}

var reVersion = regexp.MustCompile(`^[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}$`)

// Info implements importer.Importer for INFO output read repeatedly by an owner script.
// Each reply starts with a "# unravis-sample <RFC 3339 time>" line; INFO has no time of
// its own.
type Info struct{}

// NewInfo returns the redis-info importer.
func NewInfo() importer.Importer { return Info{} }

func (Info) Name() string                  { return NameInfo }
func (Info) AllowedAttrs() map[string]bool { return copyAttrs(infoAllowed) }

func (Info) DefaultLimits() importer.Limits {
	return importer.Limits{
		MaxBytes: infoMaxBytes, MaxRows: infoMaxRows, MaxLineBytes: infoMaxLine,
		MaxFields: slowMaxFields, MaxEvents: infoMaxEvents, MaxRejectFraction: rejectFrac,
	}
}

// Detect recognises a "# unravis-sample" line followed by INFO fields.
func (Info) Detect(head []byte, fileName string) int {
	if importer.Sniff(head) != nil {
		return 0
	}
	text := headText(head)
	if text == nil {
		return 0
	}
	t := bytes.TrimLeft(text, " \t\r\n")
	hasField := bytes.Contains(text, []byte("redis_version:")) || bytes.Contains(text, []byte("uptime_in_seconds:")) ||
		bytes.Contains(text, []byte("used_memory:"))
	if !hasField {
		return 0
	}
	score := 0
	if bytes.HasPrefix(t, []byte("# "+commentSample)) {
		score = 95
	} else if bytes.HasPrefix(t, []byte("# Server")) {
		score = 30 // plain INFO without the sample line: Parse explains what is missing
	}
	if score > 0 {
		if lower := strings.ToLower(fileName); strings.Contains(lower, "info") || strings.Contains(lower, "redis") {
			score += 5
		}
	}
	return score
}

type infoPrev struct {
	t      time.Time
	cpu    [2]float64
	haveC  [2]bool
	up     float64
	haveUp bool
}

type infoState struct {
	sink *sink
	rep  *importer.Report
	opts importer.Options
	lim  importer.Limits
	host string

	haveSample bool // a sample header was seen
	badSample  bool // the current sample's header did not parse
	t          time.Time
	tz         string
	line       int64
	vals       [20]float64
	have       [20]bool
	role       string
	sentinel   bool
	events     int64
	samples    int
	version    string
	offset     bool
	prev       infoPrev
	anyEvent   bool
}

// Parse reads the samples and emits metric events for the allowlisted INFO fields, CPU
// rates between consecutive samples, and a redis-start event when uptime goes back.
func (Info) Parse(ctx context.Context, r io.Reader, opts importer.Options, emit func(evidence.Event) error) (importer.Report, error) {
	lim := capLimits(opts.Limits, Info{}.DefaultLimits())
	rep := importer.Report{Importer: NameInfo}
	rep.Rule("ts", clock.RuleUTCField)
	if err := ctx.Err(); err != nil {
		return rep, err
	}
	host, err := hostLabel(opts)
	if err != nil {
		return rep, err
	}
	st := &infoState{
		sink: &sink{opts: opts, rep: &rep, emit: emit, allowed: infoAllowed},
		rep:  &rep, opts: opts, lim: lim, host: host,
	}
	if err := readLines(ctx, r, lim, &rep, st.feed); err != nil {
		return rep, err
	}
	if err := st.flush(); err != nil {
		return rep, err
	}
	if !st.haveSample {
		return rep, &importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat}
	}
	if err := finishRead(&rep, lim); err != nil {
		return rep, err
	}
	st.finish()
	return rep, nil
}

func (st *infoState) row() error {
	st.rep.Rows++
	return st.lim.CheckRows(st.rep.Rows)
}

func (st *infoState) reject(no int64, r importer.Reason) error {
	if err := st.row(); err != nil {
		return err
	}
	st.rep.Reject(no, r)
	return nil
}

func (st *infoState) feed(line []byte, no int64) error {
	t := bytes.TrimSpace(line)
	if len(t) == 0 {
		return nil
	}
	if t[0] == '#' {
		c := bytes.TrimSpace(t[1:])
		if bytes.HasPrefix(c, []byte(commentSample)) {
			if err := st.flush(); err != nil {
				return err
			}
			return st.header(c, no)
		}
		if string(c) == "Sentinel" {
			st.sentinel = true
		}
		return nil
	}
	if !st.haveSample {
		return st.reject(no, importer.ReasonFieldCount)
	}
	if st.badSample {
		return st.reject(no, importer.ReasonBadTime)
	}
	if err := st.row(); err != nil {
		return err
	}
	i := bytes.IndexByte(t, ':')
	if i <= 0 {
		st.rep.Reject(no, importer.ReasonFieldCount)
		return nil
	}
	key, val := string(t[:i]), t[i+1:]
	switch key {
	case "role":
		st.role = roleOf(string(bytes.TrimSpace(val)))
		return nil
	case "redis_version":
		if v := string(bytes.TrimSpace(val)); reVersion.MatchString(v) {
			st.version = v
		}
		return nil
	}
	idx, ok := metricIndex[key]
	if !ok {
		return nil // every other field is dropped unread
	}
	spec := metricSpecs[idx]
	v, ok := strictNumber(val)
	if !ok || v > spec.max {
		st.rep.Reject(no, importer.ReasonBadNumber)
		return nil
	}
	st.vals[idx], st.have[idx] = v, true
	return nil
}

// header starts a sample from "unravis-sample <time>".
func (st *infoState) header(c []byte, no int64) error {
	st.haveSample = true
	st.line = no
	st.have = [20]bool{}
	st.role, st.sentinel, st.badSample = "", false, false
	t, ok := sampleTime(c)
	if !ok {
		st.badSample = true
		return st.reject(no, importer.ReasonBadTime)
	}
	st.t = t.UTC()
	st.tz = tzRuleOf(c)
	if st.tz == clock.RuleOffsetInField {
		st.offset = true
		st.rep.Rule("ts", clock.RuleOffsetInField)
	}
	st.rep.Cover(st.t)
	st.samples++
	return nil
}

func strictNumber(b []byte) (float64, bool) {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || len(b) > maxNumberLen {
		return 0, false
	}
	dot := false
	for i, c := range b {
		switch {
		case c >= '0' && c <= '9':
		case c == '.' && !dot && i > 0 && i < len(b)-1:
			dot = true
		default:
			return 0, false
		}
	}
	v, err := strconv.ParseFloat(string(b), 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return v, true
}

// flush emits the events of the sample that just ended.
func (st *infoState) flush() error {
	if !st.haveSample || st.badSample || st.t.IsZero() || st.line == 0 {
		return nil
	}
	line := st.line
	st.line = 0
	role := st.role
	if role == "" && st.sentinel {
		role = "sentinel"
	}
	var evs []evidence.Event
	mk := func(kind, metric string, v *float64, unit, sev string) evidence.Event {
		a := map[string]string{}
		if metric != "" {
			a[attrMetric] = metric
		}
		if role != "" {
			a[attrRole] = role
		}
		return evidence.Event{
			EvidenceID: evidence.EventID(st.opts.CaseID, NameInfo, st.opts.ImportNonce, line, len(evs)),
			Source:     NameInfo, Kind: kind, Host: st.host,
			Start: st.t, End: st.t, Value: v, Unit: unit, Severity: sev,
			Line: line, TZRule: st.tz, Attrs: a,
		}
	}
	dt := st.t.Sub(st.prev.t).Seconds()
	for i, spec := range metricSpecs {
		if !st.have[i] {
			continue
		}
		raw := st.vals[i]
		if spec.cpu {
			c := i - idxCPUSys
			if st.prev.haveC[c] && dt > 0 && raw >= st.prev.cpu[c] {
				v := (raw - st.prev.cpu[c]) / dt * 100
				if !math.IsInf(v, 0) && !math.IsNaN(v) {
					evs = append(evs, mk(KindMetric, spec.key, &v, spec.unit, ""))
				}
			}
			continue
		}
		v := raw / spec.div
		evs = append(evs, mk(KindMetric, spec.key, &v, spec.unit, ""))
	}
	if st.have[idxUptime] {
		up := st.vals[idxUptime]
		if st.prev.haveUp && up < st.prev.up {
			at := st.t.Add(-time.Duration(up * float64(time.Second)))
			e := mk(KindRedisStart, "", nil, "", "info")
			e.Start, e.End = at, at
			evs = append(evs, e)
		}
	}

	st.events += int64(len(evs))
	if err := st.lim.CheckEvents(st.events); err != nil {
		return err
	}
	for _, e := range evs {
		// An event that fails validation is skipped, not counted as a rejected row: the
		// rows it came from were already counted when they were read.
		if _, err := st.sink.send(e); err != nil {
			return err
		}
	}
	if len(evs) > 0 {
		st.anyEvent = true
	}

	st.prev.t = st.t
	for c := 0; c < 2; c++ {
		i := idxCPUSys + c
		st.prev.haveC[c] = st.have[i]
		if st.have[i] {
			st.prev.cpu[c] = st.vals[i]
		}
	}
	st.prev.haveUp = st.have[idxUptime]
	if st.prev.haveUp {
		st.prev.up = st.vals[idxUptime]
	}
	return nil
}

func (st *infoState) finish() {
	rep := st.rep
	q := evidence.SourceQuality{SourceVersion: st.version}
	if !rep.First.IsZero() {
		f, l := rep.First, rep.Last
		q.CoverageFirst, q.CoverageLast = &f, &l
		q.TimezoneRule = clock.RuleUTCField
		if st.offset {
			q.TimezoneRule = clock.RuleOffsetInField
		}
	}
	q.Sampling = "point-in-time INFO snapshots"
	rep.Gap("INFO is a point-in-time snapshot; activity between two samples is not shown")
	if st.version == "" {
		rep.Gap("Redis version unknown: no redis_version field in the file")
	}
	if st.samples < 2 {
		rep.Gap("CPU use is calculated between two samples; a file with one sample has none")
	}
	rep.Quality = q
}
