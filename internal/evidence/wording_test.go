package evidence

import (
	"os"
	"regexp"
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

func expand(tmpl string, params []string, values []string) string {
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
		{"overlapAmbiguous", []string{"4"}, OverlapAmbiguous(4)},
		{"auditCreate", []string{"7"}, AuditCreate(7)},
		{"auditCreate", []string{"0.25"}, AuditCreate(0.25)},
		{"metricRose", []string{"SUBJ", "MET", "DELTA", "SPAN", "FROM", "TO"},
			MetricRose("SUBJ", "MET", "DELTA", "SPAN", "FROM", "TO")},
		{"metricSampleOverlap", []string{"SRC", "MET", "VAL"}, MetricSampleOverlap("SRC", "MET", "VAL")},
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

func TestWordingsPassCausalLint(t *testing.T) {
	for _, s := range []string{
		NotObservedThreshold("sql-a", "waits over 100 ms"), NotObservedGap("sampling"),
		OverlapUnique("sql-a", "lock wait", 3), OverlapAmbiguous(2), AuditCreate(5),
		MetricRose("web-a", "working set", "1.2 GB", "40 min", "0.8 GB", "2.0 GB"),
		MetricSampleOverlap("Redis INFO", "blocked clients", "3"),
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
		MetricRose("S", "M", "D", "P", "F", "T"), MetricSampleOverlap("S", "M", "V"), MarkerBefore("O", "M", "H"),
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
