package rules

import (
	"math"
	"strconv"
	"strings"
	"time"

	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/resource"
)

// displayNames are the source names used in wordings (the same names the
// timeline uses).
var displayNames = map[string]string{
	"win-counters":       "Windows performance counters",
	"win-events":         "Windows event log",
	"dotnet-counters":    ".NET runtime counters",
	"win-procsnap":       "process snapshots",
	"redis-slowlog":      "Redis SLOWLOG",
	"redis-latency":      "Redis latency monitor",
	"redis-info":         "Redis INFO samples",
	"redis-log":          "Redis log",
	"mongod-json":        "mongod.log",
	"mongo-profile":      "MongoDB profiler",
	"mongo-currentop":    "MongoDB current operations",
	"mongo-serverstatus": "MongoDB serverStatus samples",
	"mongostat-json":     "mongostat samples",
	"log4net-text":       "log4net log",
	"log4net-xml":        "log4net XML log",
	"nlog-text":          "NLog log",
	"nginx-access":       "nginx access log",
	"apache-combined":    "Apache access log",
	"serilog-clef":       "Serilog JSON log",
	"serilog-text":       "Serilog log",
	"mel-console":        "ASP.NET Core console log",
	"mel-json":           "ASP.NET Core JSON log",
	"ancm-stdout":        "ASP.NET Core stdout log",
	"node-jsonl":         "Node proxy log",
	"linux-sadf":         "Linux sar samples",
	"linux-pidstat":      "Linux pidstat samples",
	"linux-vmstat":       "Linux vmstat samples",
	"sql-pdh":            "SQL Server performance counters",
}

func displayName(source string) string {
	if n, ok := displayNames[source]; ok {
		return n
	}
	return source
}

// sampleSources name a sampling source in the sample wording, which supplies
// the noun and the article itself ("A Redis INFO sample", "3 Redis INFO
// samples"): the display name without its plural noun.
var sampleSources = map[string]string{
	"win-counters":       "Windows performance counter",
	"dotnet-counters":    ".NET runtime counter",
	"win-procsnap":       "process snapshot",
	"redis-info":         "Redis INFO",
	"mongo-serverstatus": "MongoDB serverStatus",
	"mongostat-json":     "mongostat",
	"linux-sadf":         "Linux sar",
	"linux-pidstat":      "Linux pidstat",
	"linux-vmstat":       "Linux vmstat",
	"sql-pdh":            "SQL Server performance counter",
}

func sampleSource(source string) string {
	if n, ok := sampleSources[source]; ok {
		return n
	}
	return displayName(source)
}

// eventPhrases name event kinds in the overlap wording.
var eventPhrases = map[string]string{
	"slow-op":        "slow operation",
	"slow-command":   "slow command",
	"inflight-op":    "running operation",
	"heartbeat-late": "late heartbeat",
	"latency-spike":  "latency spike",
}

func eventPhrase(kind string) string {
	if p, ok := eventPhrases[kind]; ok {
		return p
	}
	return strings.ReplaceAll(kind, "-", " ")
}

// metricNames name metric ids in wordings.
var metricNames = map[string]string{
	resource.HostCPU:          "host CPU",
	resource.HostMemAvailable: "available memory",
	resource.HostDiskSecPerTx: "disk time per transfer",
	resource.ProcCPU:          "process CPU",
	resource.ProcWS:           "working set",
	resource.ProcPrivate:      "private bytes",
	resource.CLRGen2:          "new Gen 2 collections in one sample",
	resource.CLRGCTime:        "time in GC",
	resource.NetGCPause:       "time paused by GC",
	resource.NetTPQueue:       "thread-pool queue length",
	resource.ASPNetQueued:     "ASP.NET Requests Queued",
	resource.KestrelQueued:    "Kestrel queue length",
	resource.RedisMem:         "Redis used memory",
	resource.RedisBlocked:     "Redis blocked clients",
	resource.MongoQueue:       "MongoDB queue length",
}

func metricName(id string) string {
	if n, ok := metricNames[id]; ok {
		return n
	}
	return id
}

// seriesMetric names the metric of s, with the process for a process series.
func seriesMetric(s resource.Series) string {
	n := metricName(s.Metric.ID)
	if k := s.Proc.Key(); k != "" {
		n += " for " + k
	}
	return n
}

// seriesLabel is the coverage label of s: the metric id, and the process key
// for a process series.
func seriesLabel(s resource.Series) string {
	l := s.Metric.ID
	if k := s.Proc.Key(); k != "" {
		l += " for " + k
	}
	return l
}

// num renders v rounded to one decimal, shortest form.
func num(v float64) string {
	r := math.Round(v*10) / 10
	if r == 0 {
		r = 0 // no "-0"
	}
	return strconv.FormatFloat(r, 'f', -1, 64)
}

// value renders a sample value with its unit word.
func value(v float64, unit string) string {
	switch unit {
	case "percent", "percent-host":
		return num(v) + " %"
	case "percent-one-core":
		return num(v) + " % of one core"
	case "bytes":
		return bytesText(v)
	case "ms":
		return num(v) + " ms"
	case "per-sec":
		return num(v) + "/s"
	case "count", "":
		return num(v)
	default:
		return num(v) + " " + unit
	}
}

// bytesText renders a byte count in binary units: "420 MiB", "1.2 GiB".
func bytesText(v float64) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB", "PiB"}
	neg := v < 0
	a := math.Abs(v)
	i := 0
	for i < len(units)-1 && a >= 1024 {
		a /= 1024
		i++
	}
	if math.Round(a*10)/10 >= 1024 && i < len(units)-1 {
		a /= 1024
		i++
	}
	s := num(a) + " " + units[i]
	if neg {
		s = "-" + s
	}
	return s
}

// duration renders d as "2 min 31 s", "1 h", "28 s"; under a second, in ms.
func duration(d time.Duration) string {
	if d < 0 {
		d = -d
	}
	if d < time.Second {
		return num(float64(d)/float64(time.Millisecond)) + " ms"
	}
	d = d.Round(time.Second)
	h := int64(d / time.Hour)
	m := int64(d % time.Hour / time.Minute)
	s := int64(d % time.Minute / time.Second)
	var parts []string
	if h > 0 {
		parts = append(parts, strconv.FormatInt(h, 10)+" h")
	}
	if m > 0 {
		parts = append(parts, strconv.FormatInt(m, 10)+" min")
	}
	if s > 0 {
		parts = append(parts, strconv.FormatInt(s, 10)+" s")
	}
	return strings.Join(parts, " ")
}

func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// commonThresholds is the part of every Thresholds text about episodes,
// comparison windows and coverage.
func (e *eval) commonThresholds() string {
	return "episodes split at " + duration(e.th.Quiet) + " without a stall; comparison window " +
		duration(e.th.ComparisonWindow) + "; series need " + num(e.th.MinCoverage*100) + " % of samples"
}

/* ---------------------------------------------------------- comparison windows */

// slotSet is the set of comparison windows: the window is cut into full
// ComparisonWindow slots from its start, and slots that touch an episode
// widened by Quiet on both sides are left out. Slots are half open.
type slotSet struct {
	from     time.Time
	width    time.Duration
	n        int64
	excluded [][2]int64 // merged, inclusive index ranges
	total    int
}

func newSlotSet(w importer.Window, th Thresholds, eps []Episode) slotSet {
	s := slotSet{from: w.From, width: th.ComparisonWindow}
	if w.From.IsZero() || w.To.IsZero() || th.ComparisonWindow <= 0 || !w.To.After(w.From) {
		return s
	}
	s.n = int64(w.To.Sub(w.From) / th.ComparisonWindow)
	for _, ep := range eps {
		lo, hi, ok := s.indexRange(ep.Start.Add(-th.Quiet), ep.End.Add(th.Quiet))
		if !ok {
			continue
		}
		if k := len(s.excluded); k > 0 && lo <= s.excluded[k-1][1]+1 {
			if hi > s.excluded[k-1][1] {
				s.excluded[k-1][1] = hi
			}
			continue
		}
		s.excluded = append(s.excluded, [2]int64{lo, hi})
	}
	left := s.n
	for _, r := range s.excluded {
		left -= r[1] - r[0] + 1
	}
	s.total = int(left)
	return s
}

// indexRange is the inclusive range of slots that [start, end] touches. A
// span that ends exactly at a slot start does not touch that slot; a point
// touches the slot that holds it.
func (s slotSet) indexRange(start, end time.Time) (int64, int64, bool) {
	if s.n == 0 {
		return 0, 0, false
	}
	slotEnd := s.from.Add(time.Duration(s.n) * s.width)
	if end.Before(s.from) || !start.Before(slotEnd) {
		return 0, 0, false
	}
	lo := int64(0)
	if start.After(s.from) {
		lo = int64(start.Sub(s.from) / s.width)
	}
	last := end
	if end.After(start) {
		last = end.Add(-time.Nanosecond)
	}
	if last.Before(s.from) {
		return 0, 0, false
	}
	hi := int64(last.Sub(s.from) / s.width)
	if hi >= s.n {
		hi = s.n - 1
	}
	if hi < lo {
		return 0, 0, false
	}
	return lo, hi, true
}

func (s slotSet) isExcluded(i int64) bool {
	for _, r := range s.excluded {
		if i >= r[0] && i <= r[1] {
			return true
		}
	}
	return false
}

// slotCounter counts hits per comparison slot.
type slotCounter map[int64]int

// hit adds one to every non-excluded slot that [start, end] touches. A span
// over more than maxSlotSpan slots counts only its first slots.
func (s slotSet) hit(c slotCounter, start, end time.Time) {
	lo, hi, ok := s.indexRange(start, end)
	if !ok {
		return
	}
	if hi-lo > maxSlotSpan {
		hi = lo + maxSlotSpan
	}
	for i := lo; i <= hi; i++ {
		if !s.isExcluded(i) {
			c[i]++
		}
	}
}

const maxSlotSpan = 1000

// count is the number of slots with at least min hits.
func (c slotCounter) count(min int) int {
	n := 0
	for _, v := range c {
		if v >= min {
			n++
		}
	}
	return n
}
