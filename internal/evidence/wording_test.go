package evidence

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const wordingsTS = "../../web/src/lib/copy/wordings.ts"

// tsTemplate returns the parameter names and the template literal of an
// exported function in wordings.ts.
func tsTemplate(t *testing.T, src, name string) (params []string, tmpl string) {
	t.Helper()
	// A gap function returns notObservedGap(`detail`): the template is then the
	// whole sentence, with the detail put through the NotObservedGap twin.
	re := regexp.MustCompile("(?s)export function " + name + `\(([^)]*)\): string \{\s*return (notObservedGap\()?` + "`" + `([^` + "`" + `]*)` + "`" + `\)?;`)
	m := re.FindStringSubmatch(src)
	if m == nil {
		t.Fatalf("function %s not found in wordings.ts", name)
	}
	for _, p := range strings.Split(m[1], ",") {
		if p = strings.TrimSpace(p); p != "" {
			params = append(params, strings.TrimSpace(strings.SplitN(p, ":", 2)[0]))
		}
	}
	tmpl = m[3]
	if m[2] != "" {
		tmpl = NotObservedGap(tmpl)
	}
	return params, tmpl
}

// The wording templates may call the article helpers of wordings.ts. expand
// evaluates those calls with the Go twins, so a template that differs in
// structure still fails, and the helpers are pinned by the cases below.
var (
	indefiniteCall = regexp.MustCompile(`\$\{indefinite\((\w+)(?:, (true|false))?\)\}`)
	subjectCall    = regexp.MustCompile(`\$\{sampleSubject\((\w+), (\w+)\)\}`)
)

func expand(tmpl string, params []string, values []string) string {
	val := func(name string) string {
		for i, p := range params {
			if p == name {
				return values[i]
			}
		}
		panic("unknown parameter " + name)
	}
	tmpl = indefiniteCall.ReplaceAllStringFunc(tmpl, func(s string) string {
		m := indefiniteCall.FindStringSubmatch(s)
		return indefinite(val(m[1]), m[2] == "true")
	})
	tmpl = subjectCall.ReplaceAllStringFunc(tmpl, func(s string) string {
		m := subjectCall.FindStringSubmatch(s)
		n, err := strconv.Atoi(val(m[2]))
		if err != nil {
			panic(err)
		}
		return sampleSubject(val(m[1]), n)
	})
	for i, p := range params {
		tmpl = strings.ReplaceAll(tmpl, "${"+p+"}", values[i])
	}
	return tmpl
}

func TestWordingTwinsMatchTypeScript(t *testing.T) {
	raw, err := os.ReadFile(wordingsTS)
	if err != nil {
		t.Fatalf("read wordings.ts: %v", err)
	}
	src := strings.ReplaceAll(string(raw), "\r\n", "\n")
	cases := []struct {
		ts     string
		values []string
		got    string
	}{
		{"notObservedThreshold", []string{"SRC", "THR"}, NotObservedThreshold("SRC", "THR")},
		{"notObservedGap", []string{"DETAIL"}, NotObservedGap("DETAIL")},
		{"overlapUnique", []string{"SRC", "EVT", "7"}, OverlapUnique("SRC", "EVT", 7)},
		{"overlapUnique", []string{"SRC", "EVT", "2.5"}, OverlapUnique("SRC", "EVT", 2.5)},
		{"overlapUnique", []string{"ASP.NET", "error", "2.5"}, OverlapUnique("ASP.NET", "error", 2.5)},
		{"overlapAmbiguous", []string{"4"}, OverlapAmbiguous(4)},
		{"auditCreate", []string{"7"}, AuditCreate(7)},
		{"auditCreate", []string{"0.25"}, AuditCreate(0.25)},
		{"metricRose", []string{"SUBJ", "MET", "DELTA", "SPAN", "FROM", "TO"},
			MetricRose("SUBJ", "MET", "DELTA", "SPAN", "FROM", "TO")},
		{"metricSampleOverlap", []string{"SRC", "1", "MET", "VAL"}, MetricSampleOverlap("SRC", 1, "MET", "VAL")},
		{"metricSampleOverlap", []string{"ASP.NET", "1", "MET", "VAL"}, MetricSampleOverlap("ASP.NET", 1, "MET", "VAL")},
		{"metricSampleOverlap", []string{"SRC", "3", "MET", "VAL"}, MetricSampleOverlap("SRC", 3, "MET", "VAL")},
		{"markerBefore", []string{"OFF", "MARK", "HOST"}, MarkerBefore("OFF", "MARK", "HOST")},
		{"episodeShare", []string{"COND", "3", "5", "1", "40"}, EpisodeShare("COND", 3, 5, 1, 40)},
		{"topInWindow", []string{"MET", "LIST"}, TopInWindow("MET", "LIST")},
		{"markerBefore", []string{"OFF", "ASP.NET restart", "HOST"}, MarkerBefore("OFF", "ASP.NET restart", "HOST")},
		{"markerAfter", []string{"OFF", "MARK", "HOST"}, MarkerAfter("OFF", "MARK", "HOST")},
		{"markerAfter", []string{"OFF", "IIS restart", "HOST"}, MarkerAfter("OFF", "IIS restart", "HOST")},
		{"lowestInWindow", []string{"MET", "VAL"}, LowestInWindow("MET", "VAL")},
		{"countInWindow", []string{"WHAT", "3"}, CountInWindow("WHAT", 3)},
		{"overlapBuffered", []string{"SRC", "EVT", "7"}, OverlapBuffered("SRC", "EVT", 7)},
		{"overlapBuffered", []string{"SRC", "error", "2.5"}, OverlapBuffered("SRC", "error", 2.5)},
		{"onHost", []string{"PHRASE", "HOST"}, OnHost("PHRASE", "HOST")},
		{"listEntry", []string{"NAME", "VAL"}, ListEntry("NAME", "VAL")},
		{"otherProcesses", []string{"VAL"}, OtherProcesses("VAL")},
		{"listedProcessesOnly", nil, ListedProcessesOnly()},
		{"coverageGap", []string{"13", "MET", "HOST"}, CoverageGap(13, "MET", "HOST")},
		{"noProcessCountersGap", []string{"HOST"}, NoProcessCountersGap("HOST")},
		{"noRuntimeCountersGap", []string{"HOST"}, NoRuntimeCountersGap("HOST")},
		{"noThreadPoolCounterGap", []string{"HOST"}, NoThreadPoolCounterGap("HOST")},
		{"noAppLogGap", []string{"HOST"}, NoAppLogGap("HOST")},
		{"eventCapGap", []string{"5000"}, EventCapGap(5000)},
		{"shortSegmentGap", []string{"SUBJ", "MET", "FROM", "TO", "7", "SPAN", "12", "MINSPAN", "80"},
			ShortSegmentGap("SUBJ", "MET", "FROM", "TO", 7, "SPAN", 12, "MINSPAN", "80")},
		{"noSlowlogImportGap", []string{"HOST"}, NoSlowlogImportGap("HOST")},
		{"slowlogThresholdGap", []string{"HOST"}, SlowlogThresholdGap("HOST")},
		{"slowlogWindowUnknownGap", []string{"HOST"}, SlowlogWindowUnknownGap("HOST")},
		{"slowlogWindowStartsGap", []string{"HOST", "FROM"}, SlowlogWindowStartsGap("HOST", "FROM")},
		{"slowlogWindowEndsGap", []string{"HOST", "TO"}, SlowlogWindowEndsGap("HOST", "TO")},
		{"slowlogIdGap", []string{"HOST"}, SlowlogIDGap("HOST")},
		{"recycleGapEnabledTriggers", nil, RecycleGapEnabledTriggers()},
		{"recycleGapLegacyDefaults", nil, RecycleGapLegacyDefaults()},
	}
	for _, c := range cases {
		params, tmpl := tsTemplate(t, src, c.ts)
		if len(params) != len(c.values) {
			t.Fatalf("%s: TS has %d parameters, Go test passes %d", c.ts, len(params), len(c.values))
		}
		if want := expand(tmpl, params, c.values); c.got != want {
			t.Errorf("%s differs from wordings.ts:\n go: %q\n ts: %q", c.ts, c.got, want)
		}
	}
}

// hostName is a ternary in wordings.ts, which tsTemplate does not read.
func TestHostNameTwin(t *testing.T) {
	raw, err := os.ReadFile(wordingsTS)
	if err != nil {
		t.Fatalf("read wordings.ts: %v", err)
	}
	src := strings.ReplaceAll(string(raw), "\r\n", "\n")
	if !strings.Contains(src, "return host === '' ? 'an unnamed host' : host;") {
		t.Error("hostName in wordings.ts differs from HostName")
	}
	if HostName("") != "an unnamed host" || HostName("web-a") != "web-a" {
		t.Errorf("HostName = %q, %q", HostName(""), HostName("web-a"))
	}
}

// The exact A2 sentences. Each states what was measured or what is missing.
func TestA2WordingsExact(t *testing.T) {
	for _, c := range []struct{ got, want string }{
		{MarkerBefore("2 min 31 s", "recycle event of the application pool", "web-a"),
			"This interval starts 2 min 31 s after a recycle event of the application pool on web-a (Correlation)."},
		{MarkerBefore("12 s", "IIS restart", "web-a"), "This interval starts 12 s after an IIS restart on web-a (Correlation)."},
		{MarkerAfter("1 min", "recycle event of the application pool", "web-a"),
			"This interval ends 1 min before a recycle event of the application pool on web-a (Correlation)."},
		{MarkerAfter("40 s", "ASP.NET restart", HostName("")),
			"This interval ends 40 s before an ASP.NET restart on an unnamed host (Correlation)."},
		{LowestInWindow("available memory", "420 MiB"), "Lowest available memory in this window: 420 MiB."},
		{CountInWindow("Redis timeout entries", 1), "Redis timeout entries in this window: 1."},
		{OverlapBuffered("Redis SLOWLOG", "slow command", 1.012),
			"The Redis SLOWLOG buffer held a slow command of 1.012 s overlapping this interval (Correlation)."},
		{OverlapBuffered("Redis latency monitor", "latency spike", 2),
			"The Redis latency monitor buffer held a latency spike of 2 s overlapping this interval (Correlation)."},
		{OnHost("Host CPU at or above 90 %", "web-a"), "Host CPU at or above 90 % on web-a"},
		{ListEntry("w3wp:1240", "82 %"), "w3wp:1240 (82 %)"},
		{OtherProcesses("5 %"), "all other processes (5 %)"},
		{ListedProcessesOnly(), "(sum of listed processes only)"},
		{CoverageGap(13, "host.cpu", "web-a"), "Not observed (gap or sampling: 13 % of samples present for host.cpu on web-a)."},
		{NoProcessCountersGap("web-a"), "Not observed (gap or sampling: no per-process counters for web-a)."},
		{NoRuntimeCountersGap("web-a"), "Not observed (gap or sampling: no runtime counters for this process on web-a)."},
		{NoThreadPoolCounterGap("web-a"),
			"Not observed (gap or sampling: no thread-pool counter exists on web-a; ASP.NET Requests Queued shown instead)."},
		{NoAppLogGap("web-a"), "Not observed (gap or sampling: no app log for web-a)."},
		{EventCapGap(5000),
			"Not observed (gap or sampling: the rules read only the first 5000 matching events, so overlap and log results are partial)."},
		{ShortSegmentGap("web-a w3wp:1240", "private bytes", "2001-10-25T00:00:00Z", "2001-10-25T00:30:00Z", 7, "30 min", 12, "30 min", "80"),
			"Not observed (gap or sampling: web-a w3wp:1240 private bytes: the segment from 2001-10-25T00:00:00Z to 2001-10-25T00:30:00Z has 7 samples over 30 min; a rise is tested on 12 or more samples over 30 min or more, with 80 % of the samples expected)."},
		{NoSlowlogImportGap("redis-a"), "Not observed (gap or sampling: no Redis SLOWLOG import for redis-a)."},
		{SlowlogThresholdGap("redis-a"), "Not observed (gap or sampling: slowlog-log-slower-than not stated for redis-a)."},
		{SlowlogWindowUnknownGap("redis-a"), "Not observed (gap or sampling: the Redis slow-log window on redis-a is unknown)."},
		{SlowlogWindowStartsGap("redis-a", "2001-10-25T01:02:20Z"),
			"Not observed (gap or sampling: the Redis slow-log window on redis-a starts at 2001-10-25T01:02:20Z, after this interval starts)."},
		{SlowlogWindowEndsGap("redis-a", "2001-10-25T01:02:10Z"),
			"Not observed (gap or sampling: the Redis slow-log window on redis-a ends at 2001-10-25T01:02:10Z, before this interval ends)."},
		{SlowlogIDGap("redis-a"),
			"Not observed (gap or sampling: the Redis slow-log buffer on redis-a has a gap in its entry ids, so entries may be missing)."},
		{RecycleGapEnabledTriggers(),
			"Application pool recycle events are logged only for the triggers enabled in logEventOnRecycle; other recycle triggers may have left no event."},
		{RecycleGapLegacyDefaults(),
			"Application pool recycle events are logged by default only for the Time, Memory and PrivateMemory triggers before IIS 8.5, and logEventOnRecycle may have been changed; other recycle triggers may have left no event."},
	} {
		if c.got != c.want {
			t.Errorf("got  %q\nwant %q", c.got, c.want)
		}
	}
}

// The exact sentences: a vowel-initial source takes "An", a consonant source
// takes "A", and several samples take their count and no article.
func TestOverlapAndSampleArticles(t *testing.T) {
	for _, c := range []struct{ got, want string }{
		{OverlapUnique("ASP.NET Core console log", "late heartbeat", 1.5),
			"An ASP.NET Core console log session had a late heartbeat of 1.5 s overlapping this interval (Correlation)."},
		{OverlapUnique("IIS log", "error entry", 2),
			"An IIS log session had an error entry of 2 s overlapping this interval (Correlation)."},
		{OverlapUnique("Redis SLOWLOG", "slow command", 1.012),
			"A Redis SLOWLOG session had a slow command of 1.012 s overlapping this interval (Correlation)."},
		{OverlapUnique("SQL Server", "lock wait", 28),
			"A SQL Server session had a lock wait of 28 s overlapping this interval (Correlation)."},
		{MetricSampleOverlap("ASP.NET", 1, "ASP.NET Requests Queued", "4"),
			"An ASP.NET sample showed ASP.NET Requests Queued at 4 during this interval (Correlation)."},
		{MetricSampleOverlap("MongoDB serverStatus", 1, "MongoDB queue length", "9"),
			"A MongoDB serverStatus sample showed MongoDB queue length at 9 during this interval (Correlation)."},
		{MetricSampleOverlap("MongoDB serverStatus", 3, "MongoDB queue length", "9"),
			"3 MongoDB serverStatus samples showed MongoDB queue length at 9 during this interval (Correlation)."},
		{MetricSampleOverlap("Redis INFO", 2, "Redis blocked clients", "3"),
			"2 Redis INFO samples showed Redis blocked clients at 3 during this interval (Correlation)."},
	} {
		if c.got != c.want {
			t.Errorf("got  %q\nwant %q", c.got, c.want)
		}
	}
}

// The same table is in web/tests/wordings.articles.test.cjs.
func TestArticle(t *testing.T) {
	for phrase, want := range map[string]string{
		"ASP.NET Core log": "an", "IIS log": "an", "Event Viewer": "an", "error entry": "an", "8 ball": "an",
		"HTTP proxy": "an", "ERROR entry": "an", "SLOWLOG": "a", "unique wait": "a", "use case": "a",
		"MongoDB serverStatus": "a", "Redis INFO": "a", "mongod.log": "a", ".NET runtime counter": "a",
		"SQL Server": "a", "USB drive": "a", "CPU": "a", "log4net log": "a", "slow command": "a", "": "a",
	} {
		if got := article(phrase); got != want {
			t.Errorf("article(%q) = %q, want %q", phrase, got, want)
		}
	}
	if got := indefinite("ASP.NET", true); got != "An ASP.NET" {
		t.Errorf("got %q", got)
	}
	if got := indefinite("error", false); got != "an error" {
		t.Errorf("got %q", got)
	}
}

func TestWordingsPassCausalLint(t *testing.T) {
	for _, s := range []string{
		NotObservedThreshold("sql-a", "waits over 100 ms"), NotObservedGap("sampling"),
		OverlapUnique("sql-a", "lock wait", 3), OverlapAmbiguous(2), AuditCreate(5),
		MetricRose("web-a", "working set", "1.2 GB", "40 min", "0.8 GB", "2.0 GB"),
		MetricSampleOverlap("Redis INFO", 1, "blocked clients", "3"),
		MetricSampleOverlap("ASP.NET", 1, "ASP.NET Requests Queued", "4"),
		MetricSampleOverlap("MongoDB serverStatus", 3, "MongoDB queue length", "9"),
		OverlapUnique("ASP.NET Core console log", "error", 2), OverlapUnique("MongoDB profiler", "slow operation", 2.5),
		MarkerBefore("12 s", "Redis restart", "redis-a"),
		EpisodeShare("Thread pool queue above 100", 4, 5, 2, 60),
		TopInWindow("CPU", "w3wp 82 %, sqlservr 9 %"),
		MarkerAfter("40 s", "ASP.NET restart", "web-a"), MarkerAfter("40 s", "recycle event", HostName("")),
		LowestInWindow("available memory", "420 MiB"), CountInWindow("Redis timeout entries", 3),
		OverlapBuffered("Redis SLOWLOG", "slow command", 1.012), OverlapBuffered("Redis latency monitor", "latency spike", 2),
		OnHost("Host CPU at or above 90 %", "web-a"), ListEntry("w3wp", "82 %"), OtherProcesses("5 %"), ListedProcessesOnly(),
		CoverageGap(13, "host CPU", "web-a"), NoProcessCountersGap("web-a"), NoRuntimeCountersGap("web-a"),
		NoThreadPoolCounterGap("web-a"), NoAppLogGap("web-a"), EventCapGap(5000),
		ShortSegmentGap("web-a w3wp:1240", "private bytes", "00:00", "00:30", 7, "30 min", 12, "30 min", "80"),
		NoSlowlogImportGap("redis-a"), SlowlogThresholdGap("redis-a"), SlowlogWindowUnknownGap("redis-a"),
		SlowlogWindowStartsGap("redis-a", "01:02:20"), SlowlogWindowEndsGap("redis-a", "01:02:10"), SlowlogIDGap("redis-a"),
		RecycleGapEnabledTriggers(), RecycleGapLegacyDefaults(),
	} {
		if err := LintCausal(s); err != nil {
			t.Errorf("%q: %v", s, err)
		}
	}
}

// Wave-2 plan §4.0: these words never appear in any wording template.
func TestWordingsAvoidBannedWords(t *testing.T) {
	raw, err := os.ReadFile(wordingsTS)
	if err != nil {
		t.Fatalf("read wordings.ts: %v", err)
	}
	all := strings.ToLower(string(raw)) + strings.ToLower(strings.Join([]string{
		MetricRose("S", "M", "D", "P", "F", "T"), MetricSampleOverlap("S", 1, "M", "V"), MetricSampleOverlap("S", 2, "M", "V"), MarkerBefore("O", "M", "H"),
		EpisodeShare("C", 1, 2, 3, 4), TopInWindow("M", "L"),
		NotObservedThreshold("S", "T"), NotObservedGap("D"), OverlapUnique("S", "E", 1), OverlapAmbiguous(2),
		OverlapShared(2), AuditCreate(1),
		MarkerAfter("O", "M", "H"), LowestInWindow("M", "V"), CountInWindow("W", 1), OverlapBuffered("S", "E", 1),
		OnHost("P", "H"), ListEntry("N", "V"), OtherProcesses("V"), ListedProcessesOnly(), CoverageGap(1, "M", "H"),
		NoProcessCountersGap("H"), NoRuntimeCountersGap("H"), NoThreadPoolCounterGap("H"), NoAppLogGap("H"), EventCapGap(1),
		ShortSegmentGap("S", "M", "F", "T", 1, "P", 2, "P", "80"), NoSlowlogImportGap("H"), SlowlogThresholdGap("H"),
		SlowlogWindowUnknownGap("H"), SlowlogWindowStartsGap("H", "F"), SlowlogWindowEndsGap("H", "T"), SlowlogIDGap("H"),
		RecycleGapEnabledTriggers(), RecycleGapLegacyDefaults(),
	}, " "))
	for _, w := range []string{"leak", "starv", "saturat", "caused", "because"} {
		if strings.Contains(all, w) {
			t.Errorf("a wording contains %q", w)
		}
	}
}
