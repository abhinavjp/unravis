// Package wincounters imports Windows Performance Monitor counters exported with
// relog to CSV (PDH-CSV 4.0). The wide layout (one column per counter) becomes
// one "counter" Event per non-empty cell.
//
// Time: the header's first cell carries a fixed bias, "(PDH-CSV 4.0) (<zone name>)(<bias>)"
// with bias in minutes such that UTC = local + bias. That gives RuleHeaderBias;
// DST changes inside the file are not corrected, and the report says so. A host zone
// that disagrees with the bias at the first sample quarantines the file.
//
// CPU: Processor(_Total) is overall host CPU (unit percent-host); it says nothing
// about SQL Server. Process V2(name:pid) is preferred over legacy Process(name);
// per-process percentages are divided by the host's logical cores, or get unit
// percent-one-core and a gap when the core count is unknown.
package wincounters

import (
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
	"api-audit-explorer/internal/importer/wincsv"
)

// Name is the default importer name; P3 registers the same parser as "sql-pdh".
const Name = "win-counters"

// Fixed gap and label sentences.
const (
	GapFixedBias   = "fixed bias from header; DST changes inside the file are not corrected"
	GapHostCPU     = "overall host CPU; SQL attribution unknown"
	GapNoCores     = "per-process CPU is percent of one core; logical core count unknown"
	GapLegacyProc  = "legacy Process counters ignored because Process V2 is present"
	GapBadColumns  = "some counter columns could not be parsed and were ignored"
	pdhTimeLayout  = "01/02/2006 15:04:05" // fractional seconds are accepted after the seconds field
	headerCellForm = `^\(PDH-CSV ([0-9.]+)\)\s*\((.*)\)\(\s*(-?[0-9]+)\s*\)$`
)

var headerCell = regexp.MustCompile(headerCellForm)

// instances of these objects are database names, which are scrubbed (section 4 rule 8,
// the XE list names database_name): keep the counter, drop the instance.
var databaseObject = regexp.MustCompile(`(?i):(databases|database replica|availability replica|catalog metadata)$`)

// NewImporter returns the PDH CSV parser under name, detected by detect (nil means
// DetectPDH). P3 uses it for the SQL Server counters.
func NewImporter(name string, detect func([]byte) int) importer.Importer {
	if detect == nil {
		detect = DetectPDH
	}
	return &imp{name: name, detect: detect}
}

// New is the default Windows counters importer.
func New() importer.Importer { return NewImporter(Name, DetectPDH) }

// DetectPDH scores a PDH-CSV header: 95 when the first cell is "(PDH-CSV ...", else 0.
func DetectPDH(head []byte) int {
	s := strings.TrimLeft(wincsv.HeadText(head), "\"")
	if strings.HasPrefix(s, "(PDH-CSV") {
		return 95
	}
	return 0
}

type imp struct {
	name   string
	detect func([]byte) int
}

func (m *imp) Name() string                     { return m.name }
func (m *imp) Detect(head []byte, _ string) int { return m.detect(head) }
func (m *imp) AllowedAttrs() map[string]bool {
	return map[string]bool{"object": true, "counter": true, "instance": true}
}
func (m *imp) DefaultLimits() importer.Limits {
	return importer.Limits{
		MaxBytes: 512 << 20, MaxRows: 5_000_000,
		MaxLineBytes: wincsv.DefaultMaxLineBytes, MaxFields: wincsv.DefaultMaxFields,
		MaxRejectFraction: wincsv.DefaultRejectFrac,
	}
}

// column is one counter column resolved from the header.
type column struct {
	skip            bool
	host            string // label
	object, counter string
	instance        string
	unit            string
	divideCores     int // > 0: divide the value by this
	attrs           map[string]string
}

func (m *imp) Parse(ctx context.Context, r io.Reader, opts importer.Options, emit func(evidence.Event) error) (importer.Report, error) {
	lim := wincsv.MergeLimits(opts.Limits, m.DefaultLimits())
	acc := wincsv.NewAcc(m.name, lim)
	if err := ctx.Err(); err != nil {
		return acc.Fail(err)
	}
	cr, err := wincsv.OpenCSV(r, lim, 0)
	if err != nil {
		return acc.Fail(err)
	}
	hdr := cr.Header()

	first := strings.TrimSpace(hdr[0])
	if !strings.HasPrefix(first, "(PDH-CSV") {
		return acc.Fail(&importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat})
	}
	sub := headerCell.FindStringSubmatch(first)
	if sub == nil {
		return acc.Fail(wincsv.TZUnlabelled())
	}
	bias, err := strconv.Atoi(sub[3])
	if err != nil || bias < -14*60 || bias > 14*60 {
		return acc.Fail(&importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat})
	}
	acc.Rep.Quality.SourceVersion = "PDH-CSV " + sub[1]
	acc.Rep.Quality.TimezoneRule = clock.RuleHeaderBias
	acc.Rep.FieldRules["timestamp"] = clock.RuleHeaderBias
	acc.Gap(GapFixedBias)

	cols, hosts, err := m.resolveColumns(hdr, opts, acc)
	if err != nil {
		return acc.Fail(err)
	}

	var row []evidence.Event
	checked := false
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
		if len(rec) != len(hdr) {
			acc.Reject(line, importer.ReasonFieldCount)
			continue
		}
		wall, perr := time.Parse(pdhTimeLayout, strings.TrimSpace(rec[0]))
		if perr != nil {
			acc.Reject(line, importer.ReasonBadTime)
			continue
		}
		utc := wall.Add(time.Duration(bias) * time.Minute).UTC()
		if !checked {
			checked = true
			for _, h := range hosts {
				if h.TZ == nil {
					continue
				}
				if _, off := utc.In(h.TZ).Zone(); off != -bias*60 {
					return acc.Fail(&importer.QuarantineError{Reason: importer.ReasonTZMismatch})
				}
			}
		}
		row = row[:0]
		bad := false
		for i := 1; i < len(rec); i++ {
			c := &cols[i]
			if c.skip {
				continue
			}
			cell := strings.TrimSpace(rec[i])
			if cell == "" { // " " is a missing sample, never 0
				continue
			}
			v, ok := parseNumber(cell)
			if !ok {
				bad = true
				break
			}
			if c.divideCores > 0 {
				v /= float64(c.divideCores)
			}
			vv := v
			e := wincsv.NewEvent(opts, m.name, line, i)
			e.Kind, e.Host, e.Start, e.End = "counter", c.host, utc, utc
			e.Value, e.Unit, e.TZRule, e.Attrs = &vv, c.unit, clock.RuleHeaderBias, c.attrs
			row = append(row, e)
		}
		if bad {
			acc.Reject(line, importer.ReasonBadNumber)
			continue
		}
		for _, e := range row {
			if err := emit(e); err != nil {
				return acc.Fail(err)
			}
			acc.Emitted(e.Start)
		}
	}
	return acc.Finish()
}

func parseNumber(s string) (float64, bool) {
	if strings.ContainsAny(s, "xXpP_") {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return v, true
}

// resolveColumns parses every counter path in the header and maps its host.
func (m *imp) resolveColumns(hdr []string, opts importer.Options, acc *wincsv.Acc) ([]column, []importer.HostHint, error) {
	type parsed struct{ host, object, instance, counter string }
	ps := make([]parsed, len(hdr))
	okp := make([]bool, len(hdr))
	hasV2 := false
	for i := 1; i < len(hdr); i++ {
		h, o, in, c, ok := parsePath(hdr[i])
		ps[i], okp[i] = parsed{h, o, in, c}, ok
		if ok && strings.EqualFold(o, "Process V2") {
			hasV2 = true
		}
	}
	hintFor := map[string]importer.HostHint{}
	var hints []importer.HostHint
	cols := make([]column, len(hdr))
	for i := 1; i < len(hdr); i++ {
		if !okp[i] {
			cols[i].skip = true
			acc.Gap(GapBadColumns)
			continue
		}
		p := ps[i]
		if err := acc.Observe(p.host); err != nil {
			return nil, nil, err
		}
		h, seen := hintFor[p.host]
		if !seen {
			var ok bool
			switch {
			case p.host != "" && opts.Hosts != nil:
				h, ok = opts.Hosts(p.host)
			}
			if !ok && opts.Host != nil {
				h, ok = *opts.Host, true
			}
			if !ok {
				return nil, nil, importer.ErrUnknownHost
			}
			hintFor[p.host] = h
			hints = append(hints, h)
		}
		c := &cols[i]
		c.host, c.object, c.counter, c.instance = h.Label, p.object, p.counter, p.instance
		lo := strings.ToLower(p.object)
		if lo == "process" && hasV2 {
			c.skip = true
			acc.Gap(GapLegacyProc)
			continue
		}
		c.unit = ""
		isPct := strings.HasPrefix(p.counter, "%")
		switch {
		case (lo == "processor" || lo == "processor information") && p.instance == "_Total" && isPct:
			c.unit = "percent-host"
			acc.Gap(GapHostCPU)
		case (lo == "processor" || lo == "processor information") && isPct:
			c.unit = "percent-one-core"
		case (lo == "process" || lo == "process v2") && processCPU(p.counter):
			if h.LogicalCores > 0 {
				c.unit, c.divideCores = "percent-host", h.LogicalCores
			} else {
				c.unit = "percent-one-core"
				acc.Gap(GapNoCores)
			}
		}
		c.attrs = map[string]string{"object": wincsv.ClipAttr(p.object), "counter": wincsv.ClipAttr(p.counter)}
		if p.instance != "" && !databaseObject.MatchString(p.object) {
			c.attrs["instance"] = wincsv.ClipAttr(p.instance)
		}
	}
	return cols, hints, nil
}

func processCPU(counter string) bool {
	switch strings.ToLower(counter) {
	case "% processor time", "% user time", "% privileged time":
		return true
	}
	return false
}

// parsePath splits \\HOST\Object(instance)\Counter (host optional).
func parsePath(s string) (host, object, instance, counter string, ok bool) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, `\`) {
		return
	}
	if strings.HasPrefix(s, `\\`) {
		rest := s[2:]
		i := strings.IndexByte(rest, '\\')
		if i <= 0 {
			return
		}
		host, s = rest[:i], rest[i:]
	}
	s = s[1:] // leading backslash of "\Object..."
	j := strings.LastIndexByte(s, '\\')
	if j <= 0 || j == len(s)-1 {
		return
	}
	mid, counter := s[:j], s[j+1:]
	object = mid
	if strings.HasSuffix(mid, ")") {
		if k := strings.IndexByte(mid, '('); k > 0 {
			object, instance = mid[:k], mid[k+1:len(mid)-1]
		}
	}
	return host, object, instance, counter, object != "" && counter != ""
}
