package linuxperf

// sadf -d: one header line "# hostname;interval;timestamp;CPU;%usr;..." (written
// again before each group of columns, so a "-u -r" file has two), then one
// semicolon-delimited row per sample and group. The columns that are read:
//
//	CPU        -1 is the whole host (attribute cpu "all"), a number is one core
//	%user %usr %nice %system %sys %iowait %steal %guest %idle   cpu-* (percent)
//	kbmemfree kbavail kbmemused                                  mem-* (KiB, stored as bytes)
//	%memused %commit                                             mem-used-percent, mem-commit-percent
//
// Every other column (irq, soft, buffers, cached, ...) is dropped at parse.
// Rows with interval -1 are markers (LINUX-RESTART, COM): never read for their
// text; a restart adds a gap sentence. Columns come from the header, so the
// order and the sysstat version do not matter. Out-of-order and repeated times
// are kept as they are (a file made of several runs).

import (
	"context"
	"io"
	"strconv"
	"strings"
	"time"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/textio"
	"api-audit-explorer/internal/importer/wincsv"
)

const sadfLayout = "2006-01-02 15:04:05"

// NewSadf returns the sadf -d importer.
func NewSadf() importer.Importer { return &sadfImp{} }

type sadfImp struct{}

func (*sadfImp) Name() string { return NameSadf }

// AllowedAttrs: metric names the measure, cpu is "all" or a core number.
func (*sadfImp) AllowedAttrs() map[string]bool {
	return map[string]bool{"metric": true, "cpu": true}
}

func (*sadfImp) DefaultLimits() importer.Limits { return defaultLimits() }

// sadfHeaderStart opens the header line of sadf -d.
const sadfHeaderStart = "# hostname;interval;timestamp"

// Detect scores 95 for a "# hostname;interval;timestamp;" header line.
func (*sadfImp) Detect(head []byte, _ string) int {
	s := strings.TrimLeft(wincsv.HeadText(head), " \t\r\n")
	for i, line := range strings.SplitN(s, "\n", 40) {
		if i >= 39 {
			break
		}
		line = strings.TrimRight(line, "\r")
		if len(line) <= maxHeadLine && strings.HasPrefix(line, sadfHeaderStart+";") {
			return 95
		}
	}
	return 0
}

// sadfColumn is one column that is read.
type sadfColumn struct {
	idx    int
	metric string
	unit   string // "cpu" is percent-host for the whole host and percent-one-core for a core
	kib    bool
}

// sadfNames maps a header cell to what it becomes.
var sadfNames = map[string]sadfColumn{
	"%user": {metric: "cpu-user", unit: "cpu"}, "%usr": {metric: "cpu-user", unit: "cpu"},
	"%nice":   {metric: "cpu-nice", unit: "cpu"},
	"%system": {metric: "cpu-system", unit: "cpu"}, "%sys": {metric: "cpu-system", unit: "cpu"},
	"%iowait": {metric: "cpu-iowait", unit: "cpu"}, "%steal": {metric: "cpu-steal", unit: "cpu"},
	"%guest": {metric: "cpu-guest", unit: "cpu"}, "%idle": {metric: "cpu-idle", unit: "cpu"},
	"kbmemfree": {metric: "mem-free", unit: "bytes", kib: true},
	"kbavail":   {metric: "mem-avail", unit: "bytes", kib: true},
	"kbmemused": {metric: "mem-used", unit: "bytes", kib: true},
	"%memused":  {metric: "mem-used-percent", unit: "percent"},
	"%commit":   {metric: "mem-commit-percent", unit: "percent"},
}

// sadfHeader is the column layout of the rows that follow a header line.
type sadfHeader struct {
	n    int // cells in a row
	cpu  int // index of the CPU column, -1 when the group has none
	cols []sadfColumn
}

// parseSadfHeader reads a "# hostname;interval;timestamp;..." line. ok is false
// for a comment that is not a header; err refuses the file.
func parseSadfHeader(text string) (h *sadfHeader, ok bool, err error) {
	if !strings.HasPrefix(text, "#") {
		return nil, false, nil
	}
	cells := strings.Split(strings.TrimSpace(text[1:]), ";")
	if len(cells) < 3 || strings.TrimSpace(cells[0]) != "hostname" {
		return nil, false, nil
	}
	if strings.TrimSpace(cells[1]) != "interval" || strings.TrimSpace(cells[2]) != "timestamp" {
		return nil, true, wincsv.MissingColumn("timestamp")
	}
	h = &sadfHeader{n: len(cells), cpu: -1}
	seen := map[string]bool{}
	for i := 3; i < len(cells); i++ {
		name := strings.TrimSpace(cells[i])
		if seen[name] {
			continue
		}
		seen[name] = true
		if name == "CPU" {
			h.cpu = i
			continue
		}
		if c, ok := sadfNames[name]; ok {
			c.idx = i
			h.cols = append(h.cols, c)
		}
	}
	return h, true, nil
}

// sadfWhen reads the timestamp cell. rule is the clock rule the form follows ("" when
// the form is not known). ReasonTZUnlabelled says a host-local time came with no
// host zone.
func sadfWhen(cell string, loc *time.Location) (t time.Time, rule string, why importer.Reason) {
	if strings.HasSuffix(cell, " UTC") {
		t, why = utcTime(sadfLayout, strings.TrimSuffix(cell, " UTC"))
		return t, clock.RuleUTCField, why
	}
	if t, ok := epochTime(cell); ok {
		return t, clock.RuleUTCField, ""
	}
	if len(cell) != len(sadfLayout) {
		return time.Time{}, "", importer.ReasonBadTime
	}
	if _, err := time.Parse(sadfLayout, cell); err != nil {
		return time.Time{}, "", importer.ReasonBadTime
	}
	if loc == nil {
		return time.Time{}, clock.RuleHostLocal, importer.ReasonTZUnlabelled
	}
	t, why = localTime(sadfLayout, cell, loc)
	return t, clock.RuleHostLocal, why
}

func (m *sadfImp) Parse(ctx context.Context, r io.Reader, opts importer.Options, emit func(evidence.Event) error) (importer.Report, error) {
	rn, err := begin(ctx, r, NameSadf, opts, emit)
	if err != nil {
		return rn.acc.Fail(err)
	}
	acc := rn.acc
	hosts := newHostMap(rn.opts, acc)
	var (
		hdr      *sadfHeader
		seen     bool // a line with text was read
		timeSeen bool
	)
	for {
		text, no, done, err := rn.nextLine()
		if err != nil {
			return acc.Fail(err)
		}
		if done {
			break
		}
		if strings.TrimSpace(text) == "" {
			continue
		}
		seen = true
		if text[0] == '#' {
			h, ok, herr := parseSadfHeader(text)
			if herr != nil {
				return acc.Fail(herr)
			}
			if ok {
				hdr = h
				acc.Gap(GapIntervalAvg)
				if len(h.cols) == 0 {
					acc.Gap(GapNoColumns)
				}
			}
			continue
		}
		if hdr == nil { // data with no header: the columns cannot be known
			return acc.Fail(&importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat})
		}
		if e := acc.Row(no); e != nil {
			return acc.Fail(e)
		}
		fields := strings.Split(text, ";")
		if len(fields) > 1 && strings.TrimSpace(fields[1]) == "-1" { // a marker row
			if len(fields) > 3 && strings.HasPrefix(strings.TrimSpace(fields[3]), "LINUX-RESTART") {
				acc.Gap(GapRestart)
			}
			continue
		}
		if len(fields) != hdr.n {
			acc.Reject(no, importer.ReasonFieldCount)
			continue
		}
		if len(hdr.cols) == 0 {
			continue
		}
		name := strings.TrimSpace(fields[0])
		if len(name) > maxHostName {
			acc.Reject(no, importer.ReasonFieldCount)
			continue
		}
		host, herr := hosts.resolve(name)
		if herr != nil {
			return acc.Fail(herr)
		}
		t, rule, why := sadfWhen(strings.TrimSpace(fields[2]), host.TZ)
		switch {
		case why == importer.ReasonTZUnlabelled && (rn.zone == "" || rn.zone == rule):
			return acc.Fail(wincsv.TZUnlabelled())
		case why == importer.ReasonTZUnlabelled:
			acc.Reject(no, importer.ReasonBadTime)
			continue
		case why == importer.ReasonBadTime && !timeSeen:
			return acc.Fail(firstTimeRefusal())
		case why != "":
			acc.Reject(no, why)
			continue
		case !rn.lockZone(rule): // a time in another form than the first
			acc.Reject(no, importer.ReasonBadTime)
			continue
		}
		timeSeen = true

		var attrs map[string]string
		cpuWhole := true
		if hdr.cpu >= 0 {
			c := strings.TrimSpace(fields[hdr.cpu])
			if c == "-1" {
				c = "all"
			} else if n, err := strconv.ParseUint(c, 10, 16); err != nil {
				acc.Reject(no, importer.ReasonBadNumber)
				continue
			} else {
				c, cpuWhole = strconv.FormatUint(n, 10), false
			}
			attrs = map[string]string{"cpu": c}
		}
		row := rn.newRow(host.Label, t, no)
		bad := false
		for _, c := range hdr.cols {
			v, present, ok := parseValue(fields[c.idx])
			if !ok || (present && (c.unit == "cpu" || c.metric == "mem-used-percent") && overPercent(v)) {
				bad = true
				break
			}
			if !present {
				continue
			}
			unit := c.unit
			switch {
			case c.kib:
				v = kibToBytes(v)
			case unit == "cpu" && cpuWhole:
				unit = "percent-host"
			case unit == "cpu":
				unit = "percent-one-core"
			}
			row.metric(NameSadf, c.idx, c.metric, v, unit, attrs)
		}
		if bad {
			acc.Reject(no, importer.ReasonBadNumber)
			continue
		}
		if e := row.flush(); e != nil {
			return acc.Fail(e)
		}
	}
	if !seen {
		return acc.Fail(textio.ErrEmpty)
	}
	if hdr == nil {
		return acc.Fail(&importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat})
	}
	return acc.Finish()
}
