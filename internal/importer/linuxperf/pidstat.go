package linuxperf

// pidstat -h -u -r: an optional banner "Linux <release> (<host>) <date> <arch> (N CPU)",
// then a header "# Time UID PID %usr %system %guest %wait %CPU CPU Command" (and one
// for -r, "# Time UID PID minflt/s majflt/s VSZ RSS %MEM Command"), repeated for
// each interval, then one whitespace-delimited row per process. Time is epoch
// seconds. Columns come from the header; the Command is the last column and may
// hold spaces (pidstat -l), so only the cells before it are positional.
//
// Columns that are read: %usr %user %system %guest %wait %CPU (CPU, divided by the
// logical cores), minflt/s majflt/s (per second), VSZ RSS (KiB, stored as bytes),
// %MEM (percent). UID, the CPU number the task ran on and every other column are
// dropped. A header without PID (pidstat -t lists TGID and TID) or without a last
// Command column refuses the file.

import (
	"context"
	"io"
	"regexp"
	"strconv"
	"strings"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/textio"
	"api-audit-explorer/internal/importer/wincsv"
)

// NewPidstat returns the pidstat importer.
func NewPidstat() importer.Importer { return &pidstatImp{} }

type pidstatImp struct{}

func (*pidstatImp) Name() string { return NamePidstat }

// AllowedAttrs: metric names the measure, process is a server image or a derived
// label, pid is a number.
func (*pidstatImp) AllowedAttrs() map[string]bool {
	return map[string]bool{"metric": true, "process": true, "pid": true}
}

func (*pidstatImp) DefaultLimits() importer.Limits { return defaultLimits() }

// pidstatCols returns the cells of a "# Time ..." header line, or nil.
func pidstatCols(line string) []string {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "#") {
		return nil
	}
	cells := strings.Fields(line[1:])
	if len(cells) < 3 || cells[0] != "Time" {
		return nil
	}
	return cells
}

// Detect scores 90 for a "# Time ... PID ... Command" header line.
func (*pidstatImp) Detect(head []byte, _ string) int {
	s := strings.TrimLeft(wincsv.HeadText(head), " \t\r\n")
	for i, line := range strings.SplitN(s, "\n", 80) {
		if i >= 79 {
			break
		}
		if len(line) > maxHeadLine {
			continue
		}
		cells := pidstatCols(line)
		if cells == nil {
			continue
		}
		hasPID, hasCmd := false, cells[len(cells)-1] == "Command"
		for _, c := range cells {
			hasPID = hasPID || c == "PID"
		}
		if hasPID && hasCmd {
			return 90
		}
	}
	return 0
}

// pidstatColumn is one column that is read.
type pidstatColumn struct {
	idx    int
	metric string
	unit   string // "cpu": percent of the host or of one core; "bytes" KiB to bytes
}

// pidstatNames maps a header cell to what it becomes.
var pidstatNames = map[string]pidstatColumn{
	"%usr": {metric: "cpu-usr", unit: "cpu"}, "%user": {metric: "cpu-usr", unit: "cpu"},
	"%system": {metric: "cpu-system", unit: "cpu"}, "%guest": {metric: "cpu-guest", unit: "cpu"},
	"%wait": {metric: "cpu-wait", unit: "cpu"}, "%CPU": {metric: "cpu", unit: "cpu"},
	"minflt/s": {metric: "minflt-per-sec", unit: "per-sec"}, "majflt/s": {metric: "majflt-per-sec", unit: "per-sec"},
	"VSZ": {metric: "vsz", unit: "kib"}, "RSS": {metric: "rss", unit: "kib"},
	"%MEM": {metric: "mem-percent", unit: "percent"},
}

// pidstatHeader is the layout of the rows that follow a header line.
type pidstatHeader struct {
	n         int // cells before the Command, plus the Command's first word
	time, pid int
	cols      []pidstatColumn
}

func parsePidstatHeader(cells []string) (*pidstatHeader, error) {
	h := &pidstatHeader{n: len(cells), time: 0, pid: -1}
	if cells[len(cells)-1] != "Command" {
		return nil, wincsv.MissingColumn("Command")
	}
	seen := map[string]bool{}
	for i, c := range cells {
		if seen[c] {
			continue
		}
		seen[c] = true
		if c == "PID" {
			h.pid = i
		}
		if col, ok := pidstatNames[c]; ok {
			col.idx = i
			h.cols = append(h.cols, col)
		}
	}
	if h.pid < 0 {
		return nil, wincsv.MissingColumn("PID")
	}
	return h, nil
}

// bannerRe reads the host name of the banner line.
var bannerRe = regexp.MustCompile(`^Linux \S+ \(([^()\s]{1,255})\)`)

// serverImages are kept by name; every other image is pseudonymised.
var serverImages = map[string]bool{
	"w3wp": true, "dotnet": true, "node": true, "redis-server": true,
	"memurai": true, "mongod": true, "sqlservr": true,
}

// imageBase returns the executable base name of the first word of a Command
// field, in lower case, and whether it has the form of an image name (1 to 64 of
// A-Z a-z 0-9 _ . # -). A path is cut to its last part and a trailing colon (a
// process title such as "nginx:") is dropped. Nothing after the first word is
// looked at.
func imageBase(word string) (string, bool) {
	word = strings.TrimRight(word, ":")
	if strings.HasPrefix(word, "/") {
		word = word[strings.LastIndexByte(word, '/')+1:]
	}
	if len(word) == 0 || len(word) > 64 {
		return "", false
	}
	for i := 0; i < len(word); i++ {
		c := word[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '_', c == '.', c == '#', c == '-':
		default:
			return "", false
		}
	}
	return strings.ToLower(word), true
}

// labels turns image names into stored process labels.
type labels struct {
	opts  importer.Options
	names map[string]string
}

func (l *labels) of(word string) string {
	n, ok := imageBase(word)
	if !ok {
		return "other"
	}
	if serverImages[n] {
		return n
	}
	if s, ok := l.names[n]; ok {
		return s
	}
	s := "other"
	if l.opts.Pseudonymize != nil {
		if p := l.opts.Pseudonymize("instance", n); p != "" {
			s = wincsv.ClipAttr(p)
		}
	}
	if len(l.names) < 4096 {
		l.names[n] = s
	}
	return s
}

func (m *pidstatImp) Parse(ctx context.Context, r io.Reader, opts importer.Options, emit func(evidence.Event) error) (importer.Report, error) {
	rn, err := begin(ctx, r, NamePidstat, opts, emit)
	if err != nil {
		return rn.acc.Fail(err)
	}
	acc := rn.acc
	hosts := newHostMap(rn.opts, acc)
	lab := &labels{opts: rn.opts, names: map[string]string{}}
	var (
		hdr      *pidstatHeader
		cur      *importer.HostHint
		seen     bool
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
		if cells := pidstatCols(text); cells != nil {
			h, herr := parsePidstatHeader(cells)
			if herr != nil {
				return acc.Fail(herr)
			}
			hdr = h
			if len(h.cols) == 0 {
				acc.Gap(GapNoColumns)
			}
			continue
		}
		if text[0] == '#' {
			continue
		}
		if sm := bannerRe.FindStringSubmatch(text); sm != nil {
			h, herr := hosts.resolve(sm[1])
			if herr != nil {
				return acc.Fail(herr)
			}
			cur = &h
			continue
		}
		if hdr == nil { // rows with no header, for example pidstat without -h
			return acc.Fail(&importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat})
		}
		if e := acc.Row(no); e != nil {
			return acc.Fail(e)
		}
		fields := strings.Fields(text)
		if len(fields) < hdr.n {
			acc.Reject(no, importer.ReasonFieldCount)
			continue
		}
		if len(hdr.cols) == 0 {
			continue
		}
		if cur == nil {
			h, herr := hosts.resolve("")
			if herr != nil {
				return acc.Fail(herr)
			}
			cur = &h
		}
		t, ok := epochTime(fields[hdr.time])
		if !ok {
			if !timeSeen {
				return acc.Fail(firstTimeRefusal())
			}
			acc.Reject(no, importer.ReasonBadTime)
			continue
		}
		rn.lockZone(clock.RuleUTCField)
		timeSeen = true
		pid, perr := strconv.ParseUint(fields[hdr.pid], 10, 32)
		if perr != nil {
			acc.Reject(no, importer.ReasonBadNumber)
			continue
		}
		cores := cur.LogicalCores
		proc := lab.of(fields[hdr.n-1])
		attrs := map[string]string{"process": proc, "pid": strconv.FormatUint(pid, 10)}
		row := rn.newRow(cur.Label, t, no)
		bad := false
		for _, c := range hdr.cols {
			v, present, vok := parseValue(fields[c.idx])
			if !vok || (present && c.metric == "mem-percent" && overPercent(v)) {
				bad = true
				break
			}
			if !present {
				continue
			}
			unit := c.unit
			switch unit {
			case "kib":
				v, unit = kibToBytes(v), "bytes"
			case "cpu":
				if cores > 0 {
					v /= float64(cores)
					unit = "percent-host"
				} else {
					unit = "percent-one-core"
					acc.Gap(GapNoCores)
				}
			}
			row.metric(NamePidstat, c.idx, c.metric, v, unit, attrs)
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
	acc.Gap(GapPidstatSeen)
	acc.Gap(GapNoStartTime)
	return acc.Finish()
}
