package mongoexports

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

var mongostatAttrs = []string{"metric"}

const (
	maxStatHosts = 64
	kib          = 1024.0
)

// statColumn is one mongostat --json column and how it is read.
type statColumn struct {
	key     string // the column name in the JSON
	alt     string // a second spelling, or ""
	metrics [2]string
	kind    int // statRate, statPercent, statSize, statCount, statPair
}

const (
	statRate    = iota // ops per second, "*" marks replicated ops, "a|b" keeps a
	statPercent        // "9.4%"
	statSize           // "6.0G", "512M", "1.2k", "100b"
	statCount          // a whole number
	statPair           // "30|7" is read and write
)

// mongostatColumns follows the documented mongostat columns. Names the export policy
// does not list are not read. net_in and net_out are written as netIn and netOut.
var mongostatColumns = []statColumn{
	{key: "insert", metrics: [2]string{"insert"}, kind: statRate},
	{key: "query", metrics: [2]string{"query"}, kind: statRate},
	{key: "update", metrics: [2]string{"update"}, kind: statRate},
	{key: "delete", metrics: [2]string{"delete"}, kind: statRate},
	{key: "getmore", metrics: [2]string{"getmore"}, kind: statRate},
	{key: "command", metrics: [2]string{"command"}, kind: statRate},
	{key: "dirty", metrics: [2]string{"dirty"}, kind: statPercent},
	{key: "used", metrics: [2]string{"used"}, kind: statPercent},
	{key: "flushes", metrics: [2]string{"flushes"}, kind: statRate},
	{key: "vsize", metrics: [2]string{"vsize"}, kind: statSize},
	{key: "res", metrics: [2]string{"res"}, kind: statSize},
	{key: "qrw", metrics: [2]string{"qr", "qw"}, kind: statPair},
	{key: "arw", metrics: [2]string{"ar", "aw"}, kind: statPair},
	{key: "net_in", alt: "netIn", metrics: [2]string{"netIn"}, kind: statSize},
	{key: "net_out", alt: "netOut", metrics: [2]string{"netOut"}, kind: statSize},
	{key: "conn", metrics: [2]string{"conn"}, kind: statCount},
}

var (
	reStatHost = regexp.MustCompile(`^\{"[^"]{1,260}":\{`)
	reISOTime  = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})[T ](\d{2}:\d{2}:\d{2}(?:\.\d+)?)(Z|[+-]\d{2}:?\d{2})?$`)
	reMDTime   = regexp.MustCompile(`^([A-Z][a-z]{2})\s+(\d{1,2})\s+(\d{2}:\d{2}:\d{2}(?:\.\d+)?)$`)
	reTODTime  = regexp.MustCompile(`^(\d{2}:\d{2}:\d{2}(?:\.\d+)?)$`)
	reDay      = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
)

// NewMongostat returns the importer for mongostat --json output (one JSON object per
// sample, keyed by host).
func NewMongostat() importer.Importer {
	return impl{
		sp: spec{name: NameMongostat, maxEvents: 30_000_000, def: importer.Limits{
			MaxBytes: 256 << 20, MaxRows: 2_000_000, MaxLineBytes: 64 << 10, MaxFields: maxFieldsDflt,
			MaxRejectFraction: rejectFrac}},
		attrs:    mongostatAttrs,
		detect:   detectMongostat,
		newLiner: func() liner { return &mongostatLiner{} },
	}
}

func detectMongostat(head []byte, fileName string) int {
	line := firstLine(head)
	if line == nil || !reStatHost.Match(line) || hasAny(line, "localTime", "currentOpTime") {
		return 0
	}
	hits := 0
	for _, k := range []string{"conn", "insert", "query", "qrw", "res", "time", "dirty", "used"} {
		if hasAny(line, k) {
			hits++
		}
	}
	if hits < 2 {
		return 0
	}
	score := 85
	if hasAny(line, "qrw", "arw") {
		score += 5
	}
	if fileHint(fileName, "mongostat") {
		score += 5
	}
	return score
}

type mongostatLiner struct {
	base
	day     time.Time     // supplied date, advanced at each midnight rollover
	haveDay bool          // a supplied date is in force
	prevTOD time.Duration // time of day of the last row, for the rollover
	hasPrev bool
	local   bool // a host-local time was converted
	offset  bool // a time with its own offset was read
	rolled  bool
	usedDay bool // a supplied date filled in a time of day
	usedMk  bool // a sample marker filled in the date

	prevMon    time.Month // month of the last "Mon DD" row read under a supplied date
	yearAdd    int        // years added to the supplied date's year
	yearRolled bool
}

func (m *mongostatLiner) begin(lim importer.Limits, opts importer.Options, rep *importer.Report) error {
	m.setup(lim)
	if s, ok := opts.Supplied["date"]; ok {
		s = strings.TrimSpace(s)
		if reDay.MatchString(s) {
			if d, err := time.Parse("2006-01-02", s); err == nil {
				m.day, m.haveDay = d, true
			}
		}
	}
	rep.Rule("time", clock.RuleHostLocal)
	return nil
}

func colText(raw json.RawMessage) (string, bool) {
	if s, ok := str(raw); ok {
		return strings.TrimSpace(s), true
	}
	if n, ok := nonNeg(raw); ok {
		return fmtNum(n), true
	}
	return "", false
}

// statNumber reads a column value as a number of the given kind.
func statNumber(kind int, text string) (float64, bool) {
	text = strings.TrimPrefix(text, "*")
	if kind == statRate {
		if i := strings.IndexByte(text, '|'); i >= 0 {
			text = text[:i]
		}
		text = strings.TrimPrefix(text, "*")
	}
	if kind == statPercent {
		text = strings.TrimSuffix(text, "%")
	}
	mult := 1.0
	if kind == statSize && text != "" {
		switch text[len(text)-1] {
		case 'b', 'B':
			text = text[:len(text)-1]
		case 'k', 'K':
			mult, text = kib, text[:len(text)-1]
		case 'm', 'M':
			mult, text = kib*kib, text[:len(text)-1]
		case 'g', 'G':
			mult, text = kib*kib*kib, text[:len(text)-1]
		case 't', 'T':
			mult, text = kib*kib*kib*kib, text[:len(text)-1]
		}
	}
	if text == "" || len(text) > 24 {
		return 0, false
	}
	n, err := strconv.ParseFloat(text, 64)
	if err != nil || n < 0 || n >= maxCount/mult {
		return 0, false
	}
	return n * mult, true
}

func statUnit(kind int, metric string) string {
	switch kind {
	case statRate:
		return "per-sec"
	case statPercent:
		return "percent"
	case statSize:
		if metric == "netIn" || metric == "netOut" {
			return "per-sec"
		}
		return "bytes"
	}
	return "count"
}

var months = map[string]time.Month{"Jan": 1, "Feb": 2, "Mar": 3, "Apr": 4, "May": 5, "Jun": 6, "Jul": 7, "Aug": 8,
	"Sep": 9, "Oct": 10, "Nov": 11, "Dec": 12}

func todOf(s string) (time.Duration, bool) {
	t, err := time.Parse("15:04:05", s)
	if err != nil {
		return 0, false
	}
	return time.Duration(t.Hour())*time.Hour + time.Duration(t.Minute())*time.Minute + time.Duration(t.Second())*time.Second, true
}

// stampTime places the "time" column. A time with an offset is exact. A host-local time
// needs the host's zone; one that has no date takes it from the owner's date (advanced at
// midnight) or from the "# unravis-sample" marker. A fold or gap in local time is refused.
func (m *mongostatLiner) stampTime(text string, sample time.Time, loc *time.Location) (time.Time, string, importer.Reason) {
	if mm := reISOTime.FindStringSubmatch(text); mm != nil {
		if mm[3] != "" {
			t, reason := parseTimeString(mm[1] + "T" + mm[2] + mm[3])
			m.offset = true
			return t, clock.RuleOffsetInField, reason
		}
		return m.local2(mm[1], mm[2], loc)
	}
	if mm := reMDTime.FindStringSubmatch(text); mm != nil {
		mon, ok := months[mm[1]]
		year, haveYear := m.yearHint(sample, loc)
		if !ok || !haveYear {
			return time.Time{}, "", importer.ReasonBadTime
		}
		day, _ := strconv.Atoi(mm[2])
		add, rolled := m.yearAdd, false
		if m.haveDay {
			// A month that falls back by more than half a year (December to January) is a new year; a
			// small step back is a duplicate or a reordered row.
			if m.prevMon != 0 && m.prevMon-mon > 6 {
				add, rolled = add+1, true
			}
			year += add
		}
		d := time.Date(year, mon, day, 0, 0, 0, 0, time.UTC)
		if d.Day() != day || d.Month() != mon {
			return time.Time{}, "", importer.ReasonBadTime
		}
		if m.haveDay {
			m.prevMon, m.yearAdd = mon, add
			m.yearRolled = m.yearRolled || rolled
		}
		return m.local2(d.Format("2006-01-02"), mm[3], loc)
	}
	mm := reTODTime.FindStringSubmatch(text)
	if mm == nil {
		return time.Time{}, "", importer.ReasonBadTime
	}
	tod := mm[1]
	d, ok := todOf(tod[:8])
	if !ok {
		return time.Time{}, "", importer.ReasonBadTime
	}
	switch {
	case m.haveDay:
		if m.hasPrev && d < m.prevTOD-12*time.Hour {
			m.day = m.day.AddDate(0, 0, 1)
			m.rolled = true
		}
		m.prevTOD, m.hasPrev = d, true
		m.usedDay = true
		return m.local2(m.day.Format("2006-01-02"), tod, loc)
	case !sample.IsZero() && loc != nil:
		ls := sample.In(loc)
		cand := time.Date(ls.Year(), ls.Month(), ls.Day(), 0, 0, 0, 0, time.UTC).Add(d)
		wall := time.Date(ls.Year(), ls.Month(), ls.Day(), ls.Hour(), ls.Minute(), ls.Second(), 0, time.UTC)
		switch diff := cand.Sub(wall); {
		case diff > 12*time.Hour:
			cand = cand.AddDate(0, 0, -1)
		case diff < -12*time.Hour:
			cand = cand.AddDate(0, 0, 1)
		}
		m.usedMk = true
		return m.local2(cand.Format("2006-01-02"), tod, loc)
	}
	return time.Time{}, "", importer.ReasonBadTime
}

func (m *mongostatLiner) yearHint(sample time.Time, loc *time.Location) (int, bool) {
	switch {
	case m.haveDay:
		return m.day.Year(), true
	case !sample.IsZero():
		if loc != nil {
			return sample.In(loc).Year(), true
		}
		return sample.Year(), true
	}
	return 0, false
}

// local2 resolves "date tod" in the host zone.
func (m *mongostatLiner) local2(date, tod string, loc *time.Location) (time.Time, string, importer.Reason) {
	if loc == nil {
		return time.Time{}, "", importer.ReasonTZUnlabelled
	}
	res, err := clock.ParseLocal("2006-01-02 15:04:05", date+" "+tod, loc)
	if err != nil {
		return time.Time{}, "", importer.ReasonBadTime
	}
	if res.Ambiguous {
		return time.Time{}, "", importer.ReasonDSTAmbiguous
	}
	if res.Nonexistent {
		return time.Time{}, "", importer.ReasonDSTNonexistent
	}
	t, reason := checkYear(res.UTC)
	m.local = true
	return t, clock.RuleHostLocal, reason
}

type statHost struct {
	name string
	cols map[string]json.RawMessage
}

func (m *mongostatLiner) line(line []byte, no int64, sample time.Time, opts importer.Options, rep *importer.Report) (time.Time, []evidence.Event, importer.Reason, error) {
	var hosts []statHost
	bad := false
	ok, tooMany, _ := forEachKey(line, m.maxKeys, func(k string, raw json.RawMessage) {
		if len(raw) == 0 || raw[0] != '{' || len(hosts) >= maxStatHosts {
			bad = true
			return
		}
		cols := map[string]json.RawMessage{}
		var inner map[string]json.RawMessage
		if json.Unmarshal(raw, &inner) != nil || len(inner) > m.maxKeys {
			bad = true
			return
		}
		for _, c := range mongostatColumns {
			for _, key := range []string{c.key, c.alt} {
				if key == "" {
					continue
				}
				if v, ok := inner[key]; ok {
					cols[c.key] = v
				}
			}
		}
		if v, ok := inner["time"]; ok {
			cols["time"] = v
		}
		hosts = append(hosts, statHost{name: k, cols: cols})
	})
	if !ok || tooMany || bad || len(hosts) == 0 {
		return time.Time{}, nil, importer.ReasonFieldCount, nil
	}

	var evs []evidence.Event
	var first time.Time
	allowed := set(mongostatAttrs...)
	for hi, h := range hosts {
		label, err := m.host(h.name, opts, rep)
		if err != nil {
			return time.Time{}, nil, "", err
		}
		hint, _ := hintFor(h.name, opts)
		text, tok := colText(h.cols["time"])
		if !tok {
			return time.Time{}, nil, importer.ReasonBadTime, nil
		}
		t, rule, reason := m.stampTime(text, sample, hint.TZ)
		if reason != "" {
			return time.Time{}, nil, reason, nil
		}
		if first.IsZero() {
			first = t
		}
		n := 0
		for ci, c := range mongostatColumns {
			raw, present := h.cols[c.key]
			if !present {
				continue
			}
			txt, ok := colText(raw)
			if !ok {
				continue
			}
			if c.kind == statPair {
				a, b, _ := strings.Cut(txt, "|")
				for pi, part := range []string{a, b} {
					v, ok := statNumber(statCount, part)
					if !ok {
						continue
					}
					evs = append(evs, m.metric(opts, label, no, hi*64+ci*2+pi, t, rule, c.metrics[pi], "count", v))
					n++
				}
				continue
			}
			v, ok := statNumber(c.kind, txt)
			if !ok {
				continue
			}
			evs = append(evs, m.metric(opts, label, no, hi*64+ci*2, t, rule, c.metrics[0], statUnit(c.kind, c.metrics[0]), v))
			n++
		}
		if n == 0 {
			return time.Time{}, nil, importer.ReasonFieldCount, nil
		}
	}
	for _, ev := range evs {
		if ev.Validate(allowed) != nil {
			return time.Time{}, nil, importer.ReasonFieldCount, nil
		}
	}
	return first, evs, "", nil
}

func (m *mongostatLiner) metric(opts importer.Options, label string, no int64, ord int, t time.Time, rule, metric, unit string, v float64) evidence.Event {
	ev := newEvent(opts, NameMongostat, KindMetric, label, no, ord, t, t, rule)
	ev.Value, ev.Unit = fptr(v), unit
	ev.Attrs = map[string]string{"metric": metric}
	return ev
}

func (m *mongostatLiner) end(rep *importer.Report, opts importer.Options) {
	rule := clock.RuleHostLocal
	if m.offset && !m.local {
		rule = clock.RuleOffsetInField
	}
	rep.Gap("mongostat prints one sample per interval: activity between two samples is not observed")
	rep.Gap("mongostat rate columns are per second; net_in and net_out are stored as byte counts")
	if m.local {
		rep.Gap("mongostat times are host-local: the time zone is the one set for the host")
	}
	if m.usedDay {
		rep.Gap("the file has times of day only: the date is the one supplied by the owner")
	}
	if m.usedMk {
		rep.Gap("the file has times of day only: the date comes from the sample marker lines")
	}
	if m.rolled {
		rep.Gap("the date moved on by one day each time the time of day wrapped past midnight")
	}
	if m.yearRolled {
		rep.Gap("the file has month and day only: the year is the one supplied by the owner, moved on by one each time the month wrapped from December to January")
	}
	rep.Quality = quality(rep, "", rule)
}
