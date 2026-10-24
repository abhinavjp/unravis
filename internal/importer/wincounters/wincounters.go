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
//
// Hosts: a machine name in a path maps through hosts.json when it is supplied, and
// an unknown name refuses the file. Without hosts.json the picked host stands for
// one machine name; a second name refuses the file. A path with no machine name
// is the picked host. No two columns may share one series key; such a file is
// refused.
//
// Instances: only instances of objects known to hold no database, share, path or
// application name are kept (see instanceKept); the rest are dropped, or numbered
// when dropping would put columns under one key. Events are capped by
// Limits.MaxEvents, rows times columns.
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
	GapFixedBias         = "fixed bias from header; DST changes inside the file are not corrected"
	GapHostCPU           = "overall host CPU; SQL attribution unknown"
	GapNoCores           = "per-process CPU is percent of one core; logical core count unknown"
	GapLegacyProc        = "legacy Process counters ignored; Process V2 is present"
	GapBadColumns        = "some counter columns could not be parsed and were ignored"
	GapDateOrder         = "sample dates are read as month/day/year; a file written day/month/year would have days 1 to 12 read with day and month swapped"
	GapInstancesDropped  = "counter instances other than known safe ones (processors, disks, network adapters, processes, fixed SQL categories) are dropped"
	GapInstancesNumbered = "dropped counter instances that would share one series are numbered in header order"
	pdhTimeLayout        = "01/02/2006 15:04:05" // fractional seconds are accepted after the seconds field
	headerCellForm       = `^\(PDH-CSV ([0-9.]+)\)\s*\((.*)\)\(\s*(-?[0-9]+)\s*\)$`
)

var headerCell = regexp.MustCompile(headerCellForm)

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
		MaxBytes: 512 << 20, MaxRows: 5_000_000, MaxEvents: 10_000_000,
		MaxLineBytes: wincsv.DefaultMaxLineBytes, MaxFields: wincsv.DefaultMaxFields,
		MaxRejectFraction: wincsv.DefaultRejectFrac,
	}
}

// column is one counter column resolved from the header.
type column struct {
	skip            bool
	host            string // label
	object, counter string
	instance        string // kept instance, or "" (see instanceKept)
	scrubbed        bool   // the file's instance was dropped as a name or path
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
	acc.Gap(GapDateOrder)

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
		if !wincsv.SaneTime(utc) { // a zero or far-off date rejects its row only
			acc.Reject(line, importer.ReasonBadTime)
			continue
		}
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
			e.Value, e.Unit, e.TZRule, e.Attrs = &vv, c.unit, clock.RuleHeaderBias, copyAttrs(c.attrs)
			row = append(row, e)
		}
		if bad {
			acc.Reject(line, importer.ReasonBadNumber)
			continue
		}
		// MaxEvents bounds the whole run: rows x columns, not rows alone.
		if e := acc.CheckEmit(len(row)); e != nil {
			return acc.Fail(e)
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

// copyAttrs gives each event its own attribute map, so a caller that changes one
// event's attributes cannot change the next event of the column.
func copyAttrs(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
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
	onlyName, haveOnly := "", false // the one machine name allowed without hosts.json
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
			ok := false
			switch {
			case p.host == "":
				// A path with no machine name is the local machine of the
				// collector: the host the user picked.
				if opts.Host != nil {
					h, ok = *opts.Host, true
				}
			case opts.Hosts != nil:
				// hosts.json is the only mapping. An unknown name is never
				// filed under the picked host.
				h, ok = opts.Hosts(p.host)
			case opts.Host != nil:
				// No hosts.json: the picked host stands for exactly one machine
				// name (any letter case); a second name would put two machines'
				// counters under one host label.
				key := strings.ToLower(p.host)
				if !haveOnly {
					onlyName, haveOnly = key, true
				}
				if key == onlyName {
					h, ok = *opts.Host, true
				}
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
		if p.instance != "" && instanceKept(p.object, p.instance) {
			c.instance = p.instance
		} else {
			c.instance = ""
			if p.instance != "" {
				c.scrubbed = true
				acc.Gap(GapInstancesDropped)
			}
		}
	}
	if err := uniqueSeries(cols, acc); err != nil {
		return nil, nil, err
	}
	for i := 1; i < len(cols); i++ {
		c := &cols[i]
		if c.skip {
			continue
		}
		c.attrs = map[string]string{"object": wincsv.ClipAttr(c.object), "counter": wincsv.ClipAttr(c.counter)}
		if c.instance != "" {
			c.attrs["instance"] = wincsv.ClipAttr(c.instance)
		}
	}
	return cols, hints, nil
}

// seriesKey is the identity of a series: no two columns may share one.
func seriesKey(c *column) string {
	return strings.ToLower(c.host + "\x00" + c.object + "\x00" + c.counter + "\x00" + c.instance)
}

// uniqueSeries makes every series key unique, or refuses the file. Columns whose
// instance was dropped as a name or path (see instanceKept) can collide: those are
// told apart by a number in header order, with a gap. Any other two columns with
// the same host, object, counter and instance cannot be told apart, so the file is
// refused rather than merged into one series.
func uniqueSeries(cols []column, acc *wincsv.Acc) error {
	groups := map[string]int{}
	for i := 1; i < len(cols); i++ {
		if !cols[i].skip {
			groups[seriesKey(&cols[i])]++
		}
	}
	next := map[string]int{}
	for i := 1; i < len(cols); i++ {
		c := &cols[i]
		if c.skip || !c.scrubbed {
			continue
		}
		k := seriesKey(c)
		if groups[k] < 2 {
			continue
		}
		next[k]++
		c.instance = "instance-" + strconv.Itoa(next[k])
		acc.Gap(GapInstancesNumbered)
	}
	seen := map[string]bool{}
	for i := 1; i < len(cols); i++ {
		if cols[i].skip {
			continue
		}
		k := seriesKey(&cols[i])
		if seen[k] {
			return &importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat}
		}
		seen[k] = true
	}
	return nil
}

var (
	logicalDiskInst  = regexp.MustCompile(`^(?i:[a-z]:|HarddiskVolume[0-9]+)$`)
	physicalDiskInst = regexp.MustCompile(`^[0-9]+( (?i:[a-z]):)*$`)
)

// sqlFixedInstance lists the SQL Server counter objects whose instances are fixed
// category names (wait types, lock types, cache types), not database, pool or
// file names.
var sqlFixedInstance = map[string]bool{
	"wait statistics": true, "locks": true, "plan cache": true, "sql errors": true,
	"exec statistics": true, "cursor manager by type": true, "buffer node": true,
	"memory node": true,
}

// instanceKept reports whether an instance of object is known to hold no database
// name, server or share name, file path, pool or application name. It is an
// allowlist: every object not listed here has its instance dropped, because the
// instance of an object nobody checked can be anything the host's owner named.
// "_Total" is kept for every object.
func instanceKept(object, instance string) bool {
	if strings.EqualFold(instance, "_Total") || strings.EqualFold(instance, "__Total__") {
		return true
	}
	lo := strings.ToLower(object)
	switch {
	case lo == "processor", lo == "processor information", lo == "process", lo == "process v2",
		lo == "network interface", strings.HasPrefix(lo, ".net clr"):
		return true
	case lo == "logicaldisk":
		return logicalDiskInst.MatchString(instance)
	case lo == "physicaldisk":
		return physicalDiskInst.MatchString(instance)
	}
	if i := strings.LastIndexByte(lo, ':'); i >= 0 {
		return sqlFixedInstance[lo[i+1:]]
	}
	return false
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
