package localci

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestTrustedTable(t *testing.T) {
	const repo = "acme/widgets"
	collabs := []string{"Alice", "bob"}
	cases := []struct {
		name                    string
		author, headOwner, base string
		collabs                 []string
		wantRun, wantFromBase   bool
		wantReasonHas           string
	}{
		{"owner on own branch", "acme", "acme", repo, collabs, true, false, ""},
		{"owner, case differs", "ACME", "Acme", repo, nil, true, false, ""},
		{"collaborator on a branch of the repo", "alice", "acme", repo, collabs, true, true, ""},
		{"collaborator exact case", "bob", "acme", repo, collabs, true, true, ""},
		{"stranger on a branch of the repo", "mallory", "acme", repo, collabs, false, false, "mallory"},
		{"owner but head in a fork", "acme", "someone", repo, collabs, false, false, "fork"},
		{"collaborator from a fork", "alice", "alice", repo, collabs, false, false, "fork"},
		{"unknown head owner", "acme", "", repo, collabs, false, false, "fork"},
		{"empty author", "", "acme", repo, collabs, false, false, "unknown"},
		{"blank author", "  ", "acme", repo, collabs, false, false, "unknown"},
		{"bad repo", "acme", "acme", "acme", collabs, false, false, "owner/name"},
		{"bad repo, three parts", "acme", "acme", "a/b/c", collabs, false, false, "owner/name"},
		{"empty allow-list, not the owner", "alice", "acme", repo, nil, false, false, "alice"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Trusted(c.author, c.headOwner, c.collabs, c.base)
			if got.Run != c.wantRun || got.FromBase != c.wantFromBase {
				t.Fatalf("Trusted = %+v, want run=%v fromBase=%v", got, c.wantRun, c.wantFromBase)
			}
			if c.wantRun && got.Reason != "" {
				t.Fatalf("a trusted run has a reason: %q", got.Reason)
			}
			if !c.wantRun && !strings.Contains(got.Reason, c.wantReasonHas) {
				t.Fatalf("reason %q does not mention %q", got.Reason, c.wantReasonHas)
			}
		})
	}
}

func TestOpenStoreLayoutAndRunIDs(t *testing.T) {
	if _, err := OpenStore("  "); err == nil {
		t.Fatal("an empty folder must be refused")
	}
	s, err := OpenStore(filepath.Join(t.TempDir(), "store"))
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{s.RunsPath(), s.WorkPath(), s.CachesPath()} {
		if st, err := os.Stat(d); err != nil || !st.IsDir() {
			t.Fatalf("%s: %v", d, err)
		}
	}
	now := time.Date(2026, 11, 25, 10, 30, 0, 0, time.UTC)
	seen := map[string]bool{}
	for i := 0; i < 5; i++ {
		id, dir, err := s.NewRun(now, "PR 7 / fast gate")
		if err != nil {
			t.Fatal(err)
		}
		if seen[id] || !ValidRunID(id) || !strings.HasPrefix(id, "20261125-103000-") {
			t.Fatalf("id %q (seen=%v)", id, seen[id])
		}
		seen[id] = true
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			t.Fatal(err)
		}
		if got, err := s.RunDir(id); err != nil || got != dir {
			t.Fatalf("RunDir %q %v", got, err)
		}
	}
	for _, bad := range []string{"", "..", "a/b", `a\b`, "../x", "-x", ".x", "a..b", strings.Repeat("a", 200)} {
		if ValidRunID(bad) {
			t.Fatalf("%q must not be a valid run id", bad)
		}
		if _, err := s.RunDir(bad); err == nil {
			t.Fatalf("RunDir(%q) must fail", bad)
		}
	}
	if _, err := s.RunDir("20200101-000000-nope"); err == nil {
		t.Fatal("a missing run must fail")
	}
}

func TestAppendIndexConcurrentAtomicAndRedacted(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	redact := func(x string) string { return strings.ReplaceAll(x, "topsecret", "***") }
	var wg sync.WaitGroup
	const n = 40
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e := IndexEntry{RunID: "r" + strings.Repeat("x", 50) + string(rune('a'+i%26)), Conclusion: "success", Reason: "token topsecret leaked " + strings.Repeat("y", 3000),
				Start: time.Now(), End: time.Now()}
			if err := s.AppendIndex(e, redact); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	raw, err := os.ReadFile(s.IndexPath())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "topsecret") {
		t.Fatal("the index holds an unmasked secret")
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != n {
		t.Fatalf("%d lines, want %d", len(lines), n)
	}
	for _, l := range lines {
		var e IndexEntry
		if err := json.Unmarshal([]byte(l), &e); err != nil {
			t.Fatalf("torn line %q: %v", l[:min(60, len(l))], err)
		}
	}
	// a torn last line (a crash) does not hide the others
	f, err := os.OpenFile(s.IndexPath(), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"run_id":"torn`)
	_ = f.Close()
	got, err := s.ReadIndex()
	if err != nil || len(got) != n {
		t.Fatalf("ReadIndex: %d entries, %v", len(got), err)
	}
	empty, err := (&Store{Dir: t.TempDir()}).ReadIndex()
	if err != nil || len(empty) != 0 {
		t.Fatalf("no index must read as empty: %v %v", empty, err)
	}
}

func TestCacheEnvReplacesAndCreates(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	in := []string{"PATH=/bin", "GOCACHE=/user/own", "gomodcache=/user/mod", "npm_config_cache=/user/npm", "X=1"}
	out, err := s.CacheEnv(in)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"GOCACHE": filepath.Join(s.CachesPath(), "go-build"), "GOMODCACHE": filepath.Join(s.CachesPath(), "go-mod"),
		"npm_config_cache": filepath.Join(s.CachesPath(), "npm"),
	}
	count := map[string]int{}
	for _, kv := range out {
		k, v, _ := strings.Cut(kv, "=")
		count[strings.ToUpper(k)]++
		if w, ok := want[k]; ok && v != w {
			t.Fatalf("%s=%s, want %s", k, v, w)
		}
		if strings.HasPrefix(v, "/user/") {
			t.Fatalf("the user's own cache survived: %s", kv)
		}
	}
	for k := range want {
		if count[strings.ToUpper(k)] != 1 {
			t.Fatalf("%s appears %d times in %v", k, count[strings.ToUpper(k)], out)
		}
		if st, err := os.Stat(want[k]); err != nil || !st.IsDir() {
			t.Fatalf("%s: %v", want[k], err)
		}
	}
	if count["PATH"] != 1 || count["X"] != 1 {
		t.Fatalf("other variables must stay: %v", out)
	}
}

func TestJobLogsStayInsideTheRun(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	id, dir, err := s.NewRun(time.Now(), "logs")
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("not yours"), 0o600); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(dir, "logs", "go", "01-build.log")
	if err := os.MkdirAll(filepath.Dir(inside), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inside, []byte("built\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := &RunResult{Workflow: "w", Conclusion: "success", Jobs: []*JobResult{
		{Key: "go", ID: "go", Status: "success", Steps: []StepResult{{Index: 1, Name: "build", Conclusion: "success", LogFile: inside}}},
		{Key: "evil", ID: "evil", Status: "success", Steps: []StepResult{{Index: 1, Name: "x", Conclusion: "success", LogFile: outside}}},
	}}
	if err := WriteRunRecord(dir, rec); err != nil {
		t.Fatal(err)
	}
	parts, err := s.JobLogs(id, "go")
	if err != nil || len(parts) != 1 || parts[0].Path != inside {
		t.Fatalf("%v %v", parts, err)
	}
	if _, err := s.JobLogs(id, "evil"); err == nil {
		t.Fatal("a log path outside the run folder must not be served")
	}
	if _, err := s.JobLogs(id, "nope"); err == nil || !strings.Contains(err.Error(), "go") {
		t.Fatalf("an unknown job lists the known ones: %v", err)
	}
	if _, err := s.JobLogs(id, CheckoutLogName); err == nil {
		t.Fatal("no checkout log was written")
	}
	if err := os.WriteFile(filepath.Join(dir, "checkout.log"), []byte("$ git fetch\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if parts, err := s.JobLogs(id, CheckoutLogName); err != nil || len(parts) != 1 {
		t.Fatalf("%v %v", parts, err)
	}
	got, err := s.ReadRunRecord(id)
	if err != nil || got.Conclusion != "success" || len(got.Jobs) != 2 {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestCommitsByOwner(t *testing.T) {
	c := func(a, m string) PRCommit { return PRCommit{SHA: "x", AuthorLogin: a, CommitterLogin: m} }
	cases := []struct {
		name    string
		owner   string
		commits []PRCommit
		want    bool
	}{
		{"all the owner's", "acme", []PRCommit{c("acme", "acme"), c("ACME", "Acme")}, true},
		{"edited in the web editor", "acme", []PRCommit{c("acme", "web-flow")}, true},
		{"somebody else's web edit", "acme", []PRCommit{c("alice", "web-flow")}, false},
		{"another author", "acme", []PRCommit{c("acme", "acme"), c("alice", "acme")}, false},
		{"another committer", "acme", []PRCommit{c("acme", "alice")}, false},
		{"unknown author", "acme", []PRCommit{c("", "acme")}, false},
		{"unknown committer", "acme", []PRCommit{c("acme", "")}, false},
		{"no commits prove nothing", "acme", nil, false},
		{"no owner", "", []PRCommit{c("", "")}, false},
		{"the list was cut", "acme", []PRCommit{c("acme", "acme"), {SHA: "(more commits than the host lists)"}}, false},
	}
	for _, tc := range cases {
		if got := CommitsByOwner(tc.owner, tc.commits); got != tc.want {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestTrustedBase(t *testing.T) {
	if !TrustedBase("develop", []string{"master", "develop"}) || TrustedBase("feature", []string{"master", "develop"}) ||
		TrustedBase("", []string{""}) || TrustedBase("develop", nil) || TrustedBase("Develop", []string{"develop"}) {
		t.Fatal("TrustedBase must be an exact match against a non-empty list")
	}
}

func TestSplitRepoIsStrict(t *testing.T) {
	for _, ok := range []string{"acme/widgets", "a-b/c.d_e", "Acme/Widgets-2"} {
		if _, _, good := splitRepo(ok); !good {
			t.Errorf("%q should be valid", ok)
		}
	}
	for _, bad := range []string{"", "acme", "acme/", "/x", "a/b/c", "a b/c", "a/b c", "a/b;rm", "a/b\n", "-x/y;z", "a_b/c", "a/..", "a/.", "../x", "a/b?x=1", "a/b#f", "a/b%2f", "acme/wid gets", "a/b$(x)"} {
		if _, _, good := splitRepo(bad); good {
			t.Errorf("%q must be refused", bad)
		}
	}
}

func TestPseudoJobsCannotCollideWithRealJobs(t *testing.T) {
	if CheckoutLogName[0] != '_' || TrustJobKey[0] != '_' {
		t.Fatal("pseudo job names must start with _")
	}
	for _, j := range []*JobResult{{Key: "_checkout"}, {Key: "x", Pseudo: true}} {
		if !IsPseudoJob(j) {
			t.Errorf("%+v is a pseudo job", j)
		}
	}
	if IsPseudoJob(&JobResult{Key: "checkout"}) || IsPseudoJob(&JobResult{Key: "trust"}) {
		t.Fatal("a real job named checkout or trust is a real job")
	}
	src := "on: push\njobs:\n  _checkout:\n    runs-on: x\n    steps:\n      - run: x\n"
	if _, _, err := ParseWorkflow("w.yml", []byte(src)); err == nil || !strings.Contains(err.Error(), "_") {
		t.Fatalf("a job id starting with _ must be refused: %v", err)
	}
	src = "on: push\njobs:\n  checkout:\n    runs-on: x\n    steps:\n      - run: x\n  trust:\n    runs-on: x\n    steps:\n      - run: x\n"
	if _, _, err := ParseWorkflow("w.yml", []byte(src)); err != nil {
		t.Fatalf("real jobs named checkout and trust are fine now: %v", err)
	}
}

func TestParseCommitList(t *testing.T) {
	got := parseCommitList("aaa\tacme\tacme\r\nbbb\t\tweb-flow\n\nccc\n")
	want := []PRCommit{{"aaa", "acme", "acme"}, {"bbb", "", "web-flow"}, {"ccc", "", ""}}
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("%+v", got)
	}
	var many strings.Builder
	for i := 0; i < ghCommitListMax; i++ {
		many.WriteString("sha\tacme\tacme\n")
	}
	if got := parseCommitList(many.String()); len(got) != ghCommitListMax+1 || CommitsByOwner("acme", got) {
		t.Fatal("a list as long as the host's limit must not count as complete")
	}
}
