package linuxperf

// vmstat -t (or -a -t, -w -t): a group line "procs -----------memory---------- ...", a
// column header "r b swpd free buff cache si so bi bo in cs us sy id wa st [gu]
// <zone>" (repeated every screenful), then one row per sample whose last two cells
// are the date and the time. The last header cell is the zone name; it is the
// only cell that is not a column name.
//
// Columns that are read: us sy id wa st gu (percent of the host) and free (KiB,
// stored as bytes). The other columns (swap, io, system, buff, cache, inact,
// active, r, b) are dropped. There is no available-memory column. The file holds
// no host name.

import (
	"context"
	"io"
	"strings"
	"time"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/textio"
	"api-audit-explorer/internal/importer/wincsv"
)

// NewVmstat returns the vmstat importer.
func NewVmstat() importer.Importer { return &vmstatImp{} }

type vmstatImp struct{}

func (*vmstatImp) Name() string { return NameVmstat }

// AllowedAttrs: metric names the measure.
func (*vmstatImp) AllowedAttrs() map[string]bool { return map[string]bool{"metric": true} }

func (*vmstatImp) DefaultLimits() importer.Limits { return defaultLimits() }

const vmstatDate = "2006-01-02"

// vmstatKnown is every column name vmstat prints; the one header cell that is
// not in it is the zone name.
var vmstatKnown = map[string]bool{
	"r": true, "b": true, "swpd": true, "free": true, "buff": true, "cache": true, "inact": true,
	"active": true, "si": true, "so": true, "bi": true, "bo": true, "in": true, "cs": true,
	"us": true, "sy": true, "id": true, "wa": true, "st": true, "gu": true,
}

// vmstatMetrics maps a column to its metric.
var vmstatMetrics = map[string]struct {
	metric, unit string
}{
	"us": {"cpu-user", "percent-host"}, "sy": {"cpu-system", "percent-host"},
	"id": {"cpu-idle", "percent-host"}, "wa": {"cpu-iowait", "percent-host"},
	"st": {"cpu-steal", "percent-host"}, "gu": {"cpu-guest", "percent-host"},
	"free": {"mem-free", "bytes"},
}

// vmstatCols returns the cells of a column header line ("r b ..."), or nil.
func vmstatCols(line string) []string {
	cells := strings.Fields(line)
	if len(cells) < 8 || cells[0] != "r" || cells[1] != "b" {
		return nil
	}
	return cells
}

// vmstatZone returns the zone cell of a column header: its last cell when that
// is not a column name. "" means the header has no time column.
func vmstatZone(cells []string) string {
	if last := cells[len(cells)-1]; !vmstatKnown[last] {
		return last
	}
	return ""
}

// Detect scores 90 for a group line followed by a column header with a zone
// cell, 80 for the column header with a zone cell alone, and 45 for a vmstat
// header with no time column (the file is then refused with a hint).
func (*vmstatImp) Detect(head []byte, _ string) int {
	s := strings.TrimLeft(wincsv.HeadText(head), " \t\r\n")
	prevGroup := false
	for i, line := range strings.SplitN(s, "\n", 40) {
		if i >= 39 {
			break
		}
		if len(line) > maxHeadLine {
			prevGroup = false
			continue
		}
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, "procs ") && strings.Contains(line, "memory") {
			prevGroup = true
			continue
		}
		if cells := vmstatCols(line); cells != nil {
			known := 0
			for _, c := range cells {
				if vmstatKnown[c] {
					known++
				}
			}
			if known < len(cells)-1 {
				return 0
			}
			switch {
			case vmstatZone(cells) == "":
				return 45
			case prevGroup:
				return 90
			}
			return 80
		}
		prevGroup = false
	}
	return 0
}

// vmstatHeader is the layout of the rows that follow a column header.
type vmstatHeader struct {
	n    int // cells of a row: the columns, then date and time
	zone string
	cols []struct {
		idx          int
		metric, unit string
		kib          bool
	}
}

func parseVmstatHeader(cells []string) (*vmstatHeader, error) {
	zone := vmstatZone(cells)
	if zone == "" {
		return nil, firstTimeRefusal()
	}
	h := &vmstatHeader{n: len(cells) + 1, zone: zone}
	seen := map[string]bool{}
	for i, c := range cells[:len(cells)-1] {
		if seen[c] {
			continue
		}
		seen[c] = true
		if mm, ok := vmstatMetrics[c]; ok {
			h.cols = append(h.cols, struct {
				idx          int
				metric, unit string
				kib          bool
			}{i, mm.metric, mm.unit, c == "free"})
		}
	}
	return h, nil
}

func (m *vmstatImp) Parse(ctx context.Context, r io.Reader, opts importer.Options, emit func(evidence.Event) error) (importer.Report, error) {
	rn, err := begin(ctx, r, NameVmstat, opts, emit)
	if err != nil {
		return rn.acc.Fail(err)
	}
	acc := rn.acc
	// The file holds no host name: it belongs to the host the user picked.
	host, herr := newHostMap(rn.opts, acc).picked()
	if herr != nil {
		return acc.Fail(herr)
	}
	var (
		hdr      *vmstatHeader
		seen     bool
		timeSeen bool
		first    = true // the next data row is the file's first sample
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
		if cells := vmstatCols(text); cells != nil {
			h, perr := parseVmstatHeader(cells)
			if perr != nil {
				return acc.Fail(perr)
			}
			hdr = h
			continue
		}
		if strings.HasPrefix(text, "procs ") {
			continue
		}
		if hdr == nil { // rows with no header: the columns cannot be known
			return acc.Fail(&importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat})
		}
		if e := acc.Row(no); e != nil {
			return acc.Fail(e)
		}
		fields := strings.Fields(text)
		if len(fields) != hdr.n {
			acc.Reject(no, importer.ReasonFieldCount)
			continue
		}
		stamp := fields[hdr.n-2] + " " + fields[hdr.n-1]
		var (
			t    time.Time
			rule string
			why  importer.Reason
		)
		if hdr.zone == "UTC" {
			rule = clock.RuleUTCField
			t, why = utcTime(sadfLayout, stamp)
		} else {
			rule = clock.RuleHostLocal
			if host.TZ == nil {
				return acc.Fail(wincsv.TZUnlabelled())
			}
			t, why = localTime(sadfLayout, stamp, host.TZ)
			if why == "" && !timeSeen && !zoneMatches(t, host.TZ, hdr.zone) {
				return acc.Fail(&importer.QuarantineError{Reason: importer.ReasonTZMismatch})
			}
		}
		switch {
		case why == importer.ReasonBadTime && !timeSeen:
			return acc.Fail(firstTimeRefusal())
		case why != "":
			acc.Reject(no, why)
			continue
		case !rn.lockZone(rule): // a header with another zone name than the first
			acc.Reject(no, importer.ReasonBadTime)
			continue
		}
		timeSeen = true

		firstRow := first
		first = false
		row := rn.newRow(host.Label, t, no)
		bad := false
		for _, c := range hdr.cols {
			if firstRow && !c.kib { // an average since boot
				acc.Gap(GapFirstSample)
				continue
			}
			v, present, ok := parseValue(fields[c.idx])
			if !ok || (present && !c.kib && overPercent(v)) {
				bad = true
				break
			}
			if !present {
				continue
			}
			if c.kib {
				v = kibToBytes(v)
				acc.Gap(GapMemoryUnit)
			}
			row.metric(NameVmstat, c.idx, c.metric, v, c.unit, nil)
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
	acc.Gap(GapNoAvail)
	return acc.Finish()
}

// zoneMatches reports whether the abbreviation the host zone has at t is the one
// the file names.
func zoneMatches(t time.Time, loc *time.Location, name string) bool {
	abbr, _ := t.In(loc).Zone()
	return strings.EqualFold(abbr, name)
}
