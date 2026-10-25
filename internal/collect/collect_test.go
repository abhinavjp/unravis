package collect

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer/all"
)

const (
	webA = "web-a.example.test"
	webB = "web-b.example.test"
)

func cover(host string, names ...string) []SourceCoverage {
	out := make([]SourceCoverage, len(names))
	for i, n := range names {
		out[i] = SourceCoverage{Importer: n, Host: host}
	}
	return out
}

func ids(items []Item) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.ID
	}
	return out
}

func idsFor(items []Item, host string) []string {
	var out []string
	for _, it := range items {
		if it.Host == host {
			out = append(out, it.ID)
		}
	}
	return out
}

func allImporterNames() []string {
	var names []string
	for _, imp := range all.Importers() {
		names = append(names, imp.Name())
	}
	sort.Strings(names)
	return names
}

func TestSuggestTable(t *testing.T) {
	iisOnly := []string{"host-counters", "app-log", "httperr-log", "event-logs", "process-snapshot", "runtime-counters"}
	cases := []struct {
		name  string
		have  []SourceCoverage
		hosts []string
		want  []string
	}{
		{"nil case", nil, nil, []string{}},
		{"empty case with a host", nil, []string{webA}, []string{}},
		{"only IIS", cover(webA, iisW3C), []string{webA}, iisOnly},
		{"only IIS, host not listed", cover(webA, iisW3C), nil, iisOnly},
		{"host with partial set", cover(webA, iisW3C, iisHTTPERR, winCounters, "serilog-clef"), []string{webA},
			[]string{"event-logs", "process-snapshot", "runtime-counters"}},
		{"unknown source only", cover(webA, "toy-csv", "fixture"), []string{webA}, []string{}},
		{"unknown source ignored beside a known one", cover(webA, iisW3C, "toy-csv"), []string{webA}, iisOnly},
		{"redis slow log alone", cover("cache-a.example.test", redisSlow), nil,
			[]string{"redis-info", "redis-latency", "redis-log"}},
		{"sql error log alone", cover("sql-a.example.test", sqlErrorlog), nil,
			[]string{"host-counters", "event-logs", "process-snapshot", "sql-counters", "sql-health-events"}},
		{"mongo profile alone", cover("db-a.example.test", mongoProf), nil,
			[]string{"mongo-serverstatus", "mongod-log", "mongostat"}},
		{"counters alone", cover(webA, winCounters), nil, []string{"event-logs", "process-snapshot"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Suggest(c.have, c.hosts)
			if got == nil {
				t.Fatal("Suggest returned nil, want an empty list")
			}
			if !reflect.DeepEqual(ids(got), c.want) {
				t.Fatalf("ids = %v, want %v", ids(got), c.want)
			}
			for i, it := range got {
				if it.Rank != i+1 {
					t.Errorf("item %d rank = %d, want %d", i, it.Rank, i+1)
				}
			}
		})
	}
}

func TestFullSetGivesNothing(t *testing.T) {
	names := allImporterNames()
	if len(names) < 31 {
		t.Fatalf("only %d importers registered; the full-set check needs every one", len(names))
	}
	one := cover(webA, names...)
	if got := Suggest(one, []string{webA}); len(got) != 0 {
		t.Errorf("one host with every importer: %v", ids(got))
	}
	two := append(cover(webA, names...), cover(webB, names...)...)
	if got := Suggest(two, []string{webA, webB}); len(got) != 0 {
		t.Errorf("two hosts with every importer: %v", ids(got))
	}
	// Files tied to no host cover every host.
	wide := cover("", names...)
	if got := Suggest(wide, []string{webA, webB}); len(got) != 0 {
		t.Errorf("host-less files for two hosts: %v", ids(got))
	}
}

// A host with only Linux samples gets no suggestion: the rules ask for Windows
// logs, and the Linux files neither add one nor take one away.
func TestLinuxSamplesAreKnownButSuggestNothing(t *testing.T) {
	for _, n := range linuxSignals {
		if !known[n] {
			t.Errorf("%s is not known", n)
		}
		if got := Suggest(cover(webA, n), []string{webA}); len(got) != 0 {
			t.Errorf("%s alone suggests %v", n, ids(got))
		}
	}
	with := Suggest(cover(webA, iisW3C, "linux-sadf"), nil)
	without := Suggest(cover(webA, iisW3C), nil)
	if !reflect.DeepEqual(ids(with), ids(without)) {
		t.Errorf("a Linux file changes the list: %v vs %v", ids(with), ids(without))
	}
}

func TestEveryImporterIsRegisteredAndEveryRuleIsReachable(t *testing.T) {
	registered := map[string]bool{}
	for _, n := range allImporterNames() {
		registered[n] = true
	}
	for n := range known {
		if !registered[n] {
			t.Errorf("collect names importer %q, which is not registered", n)
		}
	}
	for n := range registered {
		if !known[n] {
			t.Errorf("registered importer %q is not read by any collect rule", n)
		}
	}
	for _, r := range rules {
		for _, n := range strings.Split(r.importer, ", ") {
			if !registered[n] {
				t.Errorf("rule %s points at %q, which is not registered", r.id, n)
			}
		}
	}
	// Each rule must be reachable: some host holding one importer gets it.
	seen := map[string]bool{}
	for n := range registered {
		for _, it := range Suggest(cover(webA, n), nil) {
			seen[it.ID] = true
		}
	}
	for _, r := range rules {
		if !seen[r.id] {
			t.Errorf("rule %s is never suggested", r.id)
		}
	}
	ruleIDs := map[string]bool{}
	for _, r := range rules {
		if ruleIDs[r.id] {
			t.Errorf("duplicate rule id %q", r.id)
		}
		ruleIDs[r.id] = true
	}
}

func TestEveryFamilyIsCovered(t *testing.T) {
	// The families the brief lists, each as the importer names that read it.
	families := map[string][]string{
		"IIS":      {iisW3C},
		"HTTPERR":  {iisHTTPERR},
		"WAS":      {winEvents},
		"events":   {winEvents},
		"log4net":  {"log4net-text", "log4net-xml"},
		"Serilog":  {"serilog-clef", "serilog-text"},
		"NLog":     {"nlog-text"},
		"MEL":      {"mel-console", "mel-json"},
		"ANCM":     {ancm},
		"dotnet":   {dotnetCnt},
		"counters": {winCounters},
		"procsnap": {winProcsnap},
		"SQL":      {sqlErrorlog, sqlPDH, sqlXE},
		"Redis":    {redisSlow, redisLat, redisInfo, redisLog},
		"Mongo":    {mongodJSON, mongoProf, mongoStatus, mongostat},
	}
	suggested := map[string]bool{}
	for _, r := range rules {
		for _, n := range strings.Split(r.importer, ", ") {
			suggested[n] = true
		}
	}
	for family, names := range families {
		for _, n := range names {
			if !suggested[n] {
				t.Errorf("family %s: importer %s is never suggested", family, n)
			}
		}
	}
}

func TestOrderFollowsTheTiers(t *testing.T) {
	// A host with only IIS: every tier but the store ones shows, in tier order.
	got := Suggest(cover(webA, iisW3C), nil)
	want := []string{"host-counters", "app-log", "httperr-log", "event-logs", "process-snapshot", "runtime-counters"}
	if !reflect.DeepEqual(ids(got), want) {
		t.Fatalf("order = %v, want %v", ids(got), want)
	}
	// A host holding both a web and a SQL file: store-specific items come last.
	mixed := Suggest(cover(webA, iisW3C, sqlErrorlog), nil)
	last := ids(mixed)[len(mixed)-2:]
	if !reflect.DeepEqual(last, []string{"sql-counters", "sql-health-events"}) {
		t.Errorf("store-specific items are not last: %v", ids(mixed))
	}
}

func TestHostsAreJudgedApart(t *testing.T) {
	have := append(cover(webA, iisW3C, winCounters, winProcsnap, dotnetCnt, winEvents, iisHTTPERR, "serilog-clef"), cover(webB, iisW3C)...)
	got := Suggest(have, []string{webA, webB})
	if a := idsFor(got, webA); len(a) != 0 {
		t.Errorf("complete web host still gets %v", a)
	}
	if b := idsFor(got, webB); len(b) != 6 {
		t.Errorf("web-b gets %v, want six gaps", b)
	}
	// A host-less file covers every host in the case.
	wide := append(cover("", winCounters), cover(webA, iisW3C)...)
	got = Suggest(wide, []string{webA, webB})
	for _, it := range got {
		if it.ID == "host-counters" {
			t.Errorf("host-less counters still suggested for %q", it.Host)
		}
	}
	// A whole-case scope has an empty Host.
	for _, it := range Suggest(cover("", iisW3C), nil) {
		if it.Host != "" {
			t.Errorf("whole-case item has host %q", it.Host)
		}
	}
	// A host named only by the caller, with no file of its own, gets nothing.
	if got := Suggest(cover(webA, iisW3C), []string{webA, "unseen.example.test"}); len(idsFor(got, "unseen.example.test")) != 0 {
		t.Errorf("a host with no file got %v", idsFor(got, "unseen.example.test"))
	}
}

func TestCapAtTwelve(t *testing.T) {
	var have []SourceCoverage
	var hosts []string
	for i := 0; i < 6; i++ {
		h := fmt.Sprintf("web-%d.example.test", i)
		hosts = append(hosts, h)
		have = append(have, cover(h, iisW3C)...)
	}
	got := Suggest(have, hosts)
	if len(got) != MaxItems {
		t.Fatalf("len = %d, want %d", len(got), MaxItems)
	}
	for i, it := range got {
		if it.Rank != i+1 {
			t.Errorf("rank %d at position %d", it.Rank, i)
		}
	}
	// The cut keeps the best: with six hosts the first six are host counters.
	for _, it := range got[:6] {
		if it.ID != "host-counters" {
			t.Errorf("top six should be host counters, got %s", it.ID)
		}
	}
}

func TestStableOrder(t *testing.T) {
	have := append(append(cover(webA, iisW3C, redisSlow), cover(webB, mongoProf, sqlPDH)...), cover("", "serilog-text")...)
	hosts := []string{webA, webB, "cache-a.example.test"}
	want := Suggest(have, hosts)
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 50; i++ {
		h := append([]SourceCoverage(nil), have...)
		rng.Shuffle(len(h), func(a, b int) { h[a], h[b] = h[b], h[a] })
		hs := append([]string(nil), hosts...)
		rng.Shuffle(len(hs), func(a, b int) { hs[a], hs[b] = hs[b], hs[a] })
		if got := Suggest(h, hs); !reflect.DeepEqual(got, want) {
			t.Fatalf("shuffle %d changed the list:\n got %v\nwant %v", i, ids(got), ids(want))
		}
	}
}

func TestSuggestDoesNotChangeItsInput(t *testing.T) {
	have := cover(webA, iisW3C)
	hosts := []string{webA}
	Suggest(have, hosts)
	if !reflect.DeepEqual(have, cover(webA, iisW3C)) || !reflect.DeepEqual(hosts, []string{webA}) {
		t.Error("input changed")
	}
}

func TestWordingHasNoCausalWords(t *testing.T) {
	for _, r := range rules {
		for field, text := range map[string]string{"title": r.title, "why": r.why, "how": r.how} {
			if err := evidence.LintCausal(text); err != nil {
				t.Errorf("rule %s %s: %v", r.id, field, err)
			}
			if text == "" {
				t.Errorf("rule %s has an empty %s", r.id, field)
			}
		}
	}
	// And on what Suggest really emits.
	for _, it := range Suggest(cover(webA, iisW3C), nil) {
		for _, text := range []string{it.Title, it.Why, it.How} {
			if err := evidence.LintCausal(text); err != nil {
				t.Errorf("item %s: %v", it.ID, err)
			}
		}
	}
}

func TestWordingRepeatsNothingFromTheCase(t *testing.T) {
	// No sentence may carry a host name: the host is its own field.
	for _, r := range rules {
		for _, text := range []string{r.title, r.why, r.how} {
			if strings.Contains(text, "example.test") || strings.Contains(text, "web-") {
				t.Errorf("rule %s text %q looks case specific", r.id, text)
			}
		}
	}
}

type goldenRow struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Why   string `json:"why"`
	How   string `json:"how"`
}

// TestWordingMatchesGolden pins every sentence to testdata/wording.golden.json.
// The web test reads the same file and lints each sentence for reading grade,
// so a change here that makes a sentence harder to read fails there. Set
// UPDATE_GOLDEN=1 to rewrite the file after an intended change.
func TestWordingMatchesGolden(t *testing.T) {
	var rows []goldenRow
	for _, r := range rules {
		rows = append(rows, goldenRow{r.id, r.title, r.why, r.how})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	want, err := json.MarshalIndent(rows, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	want = append(want, '\n')
	const path = "testdata/wording.golden.json"
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, want, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.ReplaceAll(string(got), "\r\n", "\n") != string(want) {
		t.Fatalf("%s is out of date; run with UPDATE_GOLDEN=1 and check the web test", path)
	}
}
