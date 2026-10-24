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
	re := regexp.MustCompile("(?s)export function " + name + `\(([^)]*)\): string \{\s*return ` + "`" + `([^` + "`" + `]*)` + "`" + `;`)
	m := re.FindStringSubmatch(src)
	if m == nil {
		t.Fatalf("function %s not found in wordings.ts", name)
	}
	for _, p := range strings.Split(m[1], ",") {
		if p = strings.TrimSpace(p); p != "" {
			params = append(params, strings.TrimSpace(strings.SplitN(p, ":", 2)[0]))
		}
	}
	return params, m[2]
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
	}, " "))
	for _, w := range []string{"leak", "starv", "saturat", "caused", "because"} {
		if strings.Contains(all, w) {
			t.Errorf("a wording contains %q", w)
		}
	}
}
