package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"api-audit-explorer/internal/localci"
)

// statusFakes are what the tests put in place of gh: nothing in this package ever reaches GitHub.
type statusFakes struct {
	reporter *localci.FakeReporter
	source   *localci.FakeCompareSource
	disp     *localci.FakeDispatcher
	claims   *localci.MemClaimHost
}

var hookMu sync.Mutex

// withStatusFakes swaps the three hooks for fakes and puts the old ones back at the end of the test.
func withStatusFakes(t *testing.T) *statusFakes {
	t.Helper()
	f := &statusFakes{reporter: &localci.FakeReporter{}, source: &localci.FakeCompareSource{}, disp: &localci.FakeDispatcher{}, claims: &localci.MemClaimHost{}}
	hookMu.Lock()
	oldR, oldS, oldD, oldC := newReporter, newCompareSource, newDispatcher, newClaimHost
	newReporter = func([]string, func(string) string) localci.StatusReporter { return f.reporter }
	newCompareSource = func([]string) localci.CompareSource { return f.source }
	newDispatcher = func([]string) localci.Dispatcher { return f.disp }
	newClaimHost = func([]string) localci.ClaimHost { return f.claims }
	hookMu.Unlock()
	t.Cleanup(func() {
		hookMu.Lock()
		newReporter, newCompareSource, newDispatcher, newClaimHost = oldR, oldS, oldD, oldC
		hookMu.Unlock()
	})
	return f
}

// installStatusFakes is what TestMain does so that no test, old or new, can post to or read from GitHub.
func installStatusFakes() {
	newReporter = func([]string, func(string) string) localci.StatusReporter { return &localci.FakeReporter{} }
	newCompareSource = func([]string) localci.CompareSource { return &localci.FakeCompareSource{} }
	newDispatcher = func([]string) localci.Dispatcher { return &localci.FakeDispatcher{} }
	newClaimHost = func([]string) localci.ClaimHost { return &localci.MemClaimHost{} }
}

// ---------------------------------------------------------------- naming and the banner

func TestDefaultNamingIsShadowAndHasTheProjectGates(t *testing.T) {
	n, code := namingFromFlags("serve", nil, nil, nil, false, io.Discard)
	if code != exitOK {
		t.Fatal(code)
	}
	if n.PostRequired {
		t.Fatal("shadow mode must be the default")
	}
	want := map[string]string{
		"pr develop fast-gate.yml":   "fast-build",
		"pr master ci.yml":           "build",
		"push master ci.yml":         "build",
		"push develop fast-gate.yml": "fast-build",
		"push develop ci.yml":        "build",
	}
	got := map[string]string{}
	for _, g := range n.Gates {
		got[g.Kind+" "+g.Branch+" "+g.Workflow] = g.Context
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: %q, want %q (have %v)", k, got[k], v, got)
		}
	}
	// what the names come out as
	if name, _ := n.GateName(localci.Request{Kind: localci.KindPR, Base: "develop", Workflow: "fast-gate.yml"}); name != "local/fast-build" {
		t.Fatalf("shadow name %q", name)
	}
	if name, _ := n.GateName(localci.Request{Kind: localci.KindPR, Base: "master", Workflow: "ci.yml"}); name != "local/build" {
		t.Fatalf("shadow name %q", name)
	}
	if name, _ := n.GateName(localci.Request{Kind: localci.KindPush, Ref: "master", Workflow: "ci.yml"}); name != "local/build" {
		t.Fatalf("shadow name %q", name)
	}
	// and with the flag
	n, _ = namingFromFlags("serve", nil, nil, nil, true, io.Discard)
	if name, _ := n.GateName(localci.Request{Kind: localci.KindPR, Base: "develop", Workflow: "fast-gate.yml"}); name != "fast-build" {
		t.Fatalf("required name %q", name)
	}
}

func TestContextOverridesAndBadFlags(t *testing.T) {
	m, err := contextMap([]string{"extra.yml=extra-gate", "ci.yml=full"})
	if err != nil || m["extra.yml"] != "extra-gate" || m["ci.yml"] != "full" || m["fast-gate.yml"] != "fast-build" {
		t.Fatalf("%v %v", m, err)
	}
	if _, err := contextMap([]string{"nonsense"}); err == nil {
		t.Fatal("a bad --context must be refused")
	}
	if _, code := namingFromFlags("serve", []string{"develop"}, nil, nil, false, io.Discard); code != exitUsage {
		t.Fatal("a bad --workflow-for must be refused")
	}
	if _, code := namingFromFlags("serve", nil, []string{"develop"}, nil, false, io.Discard); code != exitUsage {
		t.Fatal("a bad --push-workflow must be refused")
	}
	// a workflow with no context gets no gate status
	n := statusNaming(map[string]string{"develop": "unnamed.yml"}, nil, map[string]string{}, false)
	if len(n.Gates) != 0 {
		t.Fatalf("%+v", n.Gates)
	}
}

func TestBannerIsQuietInShadowAndLoudInRequiredMode(t *testing.T) {
	shadow, _ := namingFromFlags("serve", nil, nil, nil, false, io.Discard)
	req, _ := namingFromFlags("serve", nil, nil, nil, true, io.Discard)
	b := statusBanner(shadow, false)
	if !strings.Contains(b, "SHADOW") || !strings.Contains(b, "local/fast-build") || !strings.Contains(b, "local/build") || !strings.Contains(b, "never posted") {
		t.Fatalf("shadow banner: %s", b)
	}
	if strings.Contains(b, "!!!") {
		t.Fatalf("shadow mode is not an alarm: %s", b)
	}
	b = statusBanner(req, false)
	if !strings.HasPrefix(b, "!!! POST-REQUIRED MODE") || !strings.Contains(b, "fast-build") || !strings.Contains(b, "phase 1/2") || strings.Contains(b, "local/fast-build") {
		t.Fatalf("required banner: %s", b)
	}
	if b := statusBanner(shadow, true); !strings.Contains(b, "OFF") || !strings.Contains(b, "nothing is posted") {
		t.Fatalf("off banner: %s", b)
	}
}

func TestServeFlagsDocumentPostRequiredAsPhaseOneAndTwo(t *testing.T) {
	_, _, e := cli(t, nil, "serve", "-h")
	if !strings.Contains(e, "-post-required") || !strings.Contains(e, "phase 1/2") || !strings.Contains(e, "shadow") {
		t.Fatalf("serve help must document --post-required:\n%s", e)
	}
}

// ---------------------------------------------------------------- serve posts shadow statuses

func serveUntil(t *testing.T, args []string, ready func(out string) bool) (out string, stop func() (string, int)) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	var so safeBuf
	done := make(chan int, 1)
	go func() { done <- realMain(ctx, args, &so, io.Discard, os.Environ()) }()
	waitFor(t, "serve to be ready", 60*time.Second, func() bool { return ready(so.String()) })
	return so.String(), func() (string, int) {
		cancel()
		select {
		case code := <-done:
			return so.String(), code
		case <-time.After(30 * time.Second):
			t.Fatalf("serve did not stop\n%s", so.String())
			return "", -1
		}
	}
}

func TestServePostsShadowStatusesAndNeverTheRequiredNames(t *testing.T) {
	f := withStatusFakes(t)
	bare, clean, _ := prSetup(t)
	state := filepath.Join(t.TempDir(), "store")
	pr := localci.PullRequest{Number: 1, State: "OPEN", BaseRef: "develop", HeadRef: "feature", HeadSHA: clean, Author: "acme", HeadRepoOwner: "acme"}
	withHost(t, &fakeHost{url: bare, pr: pr, list: []localci.PullRequest{pr}})
	_, stop := serveUntil(t, []string{"serve", "--repo", "acme/widgets", "--state", state, "--origin", bare, "--in-process", "--no-push",
		"--no-keep-awake", "--no-release-pause", "--interval", "100ms", "--name", "t1"}, func(out string) bool { return strings.Contains(out, "queued pr 1 into develop") })
	waitFor(t, "the run to finish", 90*time.Second, func() bool {
		es, _ := (&localci.Store{Dir: state}).ReadIndex()
		return len(es) == 1 && es[0].Conclusion == "success"
	})
	waitFor(t, "the final status", 30*time.Second, func() bool {
		cs := f.reporter.Calls()
		return len(cs) > 0 && cs[len(cs)-1].State == "success" && cs[len(cs)-1].Name == "local/fast-build"
	})
	out, code := stop()
	if code != exitOK {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "SHADOW") {
		t.Fatalf("the startup log must say shadow mode:\n%s", out)
	}
	var gate []string
	for _, c := range f.reporter.Calls() {
		if c.Name == "fast-build" || c.Name == "build" {
			t.Fatalf("shadow mode posted a required name: %+v", c)
		}
		if !strings.HasPrefix(c.Name, "local/") {
			t.Fatalf("every name must start with local/: %+v", c)
		}
		if c.SHA != clean || c.Repo != "acme/widgets" {
			t.Fatalf("posted on the wrong commit: %+v", c)
		}
		if c.Name == "local/fast-build" {
			gate = append(gate, c.State+": "+c.Description)
		}
	}
	if len(gate) < 3 || gate[0] != "pending: queued" || !strings.HasPrefix(gate[len(gate)-1], "success: passed in ") {
		t.Fatalf("gate sequence %v", gate)
	}
	if !contains(gate, "pending: running") {
		t.Fatalf("no running status: %v", gate)
	}
	// the state folder keeps the dedup memory and the status command shows how posting went
	if _, err := os.Stat(ledgerPath(state)); err != nil {
		t.Fatalf("no dedup ledger: %v", err)
	}
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

func TestServePostRequiredIsLoudAndPostsTheRealNames(t *testing.T) {
	f := withStatusFakes(t)
	bare, clean, _ := prSetup(t)
	state := filepath.Join(t.TempDir(), "store")
	pr := localci.PullRequest{Number: 1, State: "OPEN", BaseRef: "develop", HeadRef: "feature", HeadSHA: clean, Author: "acme", HeadRepoOwner: "acme"}
	withHost(t, &fakeHost{url: bare, pr: pr, list: []localci.PullRequest{pr}})
	out, stop := serveUntil(t, []string{"serve", "--repo", "acme/widgets", "--state", state, "--origin", bare, "--in-process", "--no-push", "--post-required",
		"--no-keep-awake", "--no-release-pause", "--interval", "100ms", "--name", "t1"}, func(out string) bool { return strings.Contains(out, "queued pr 1 into develop") })
	if !strings.Contains(out, "!!! POST-REQUIRED MODE") {
		t.Fatalf("the flag must be announced loudly at startup:\n%s", out)
	}
	waitFor(t, "the real name to be posted", 90*time.Second, func() bool {
		for _, c := range f.reporter.Calls() {
			if c.Name == "fast-build" && c.State == "success" {
				return true
			}
		}
		return false
	})
	stop()
}

func TestServeNoStatusPostsNothing(t *testing.T) {
	f := withStatusFakes(t)
	bare, clean, _ := prSetup(t)
	state := filepath.Join(t.TempDir(), "store")
	pr := localci.PullRequest{Number: 1, State: "OPEN", BaseRef: "develop", HeadRef: "feature", HeadSHA: clean, Author: "acme", HeadRepoOwner: "acme"}
	withHost(t, &fakeHost{url: bare, pr: pr, list: []localci.PullRequest{pr}})
	_, stop := serveUntil(t, []string{"serve", "--repo", "acme/widgets", "--state", state, "--origin", bare, "--in-process", "--no-push", "--no-status",
		"--no-keep-awake", "--no-release-pause", "--interval", "100ms", "--name", "t1"}, func(out string) bool { return strings.Contains(out, "queued pr 1 into develop") })
	waitFor(t, "the run to finish", 90*time.Second, func() bool {
		es, _ := (&localci.Store{Dir: state}).ReadIndex()
		return len(es) == 1
	})
	out, _ := stop()
	if !strings.Contains(out, "OFF") || len(f.reporter.Calls()) != 0 {
		t.Fatalf("calls %+v\n%s", f.reporter.Calls(), out)
	}
}

func TestStatusCommandShowsHowPostingGoes(t *testing.T) {
	state := t.TempDir()
	hb := localci.Heartbeat{Name: "t1", Status: &localci.StatusHealth{Mode: "shadow", Posted: 7, Failed: 2, LastError: "local/x pending: HTTP 502", LastErrorAt: time.Now().Add(-time.Minute), LastPost: time.Now().Add(-5 * time.Second)}}
	var buf bytes.Buffer
	statusLines(&buf, []localci.Heartbeat{hb, {Name: "plain"}}, time.Now())
	out := buf.String()
	for _, w := range []string{"statuses shadow: 7 posted, 2 failed", "last posting problem", "HTTP 502"} {
		if !strings.Contains(out, w) {
			t.Errorf("lacks %q:\n%s", w, out)
		}
	}
	if strings.Contains(out, "plain") {
		t.Errorf("an agent that posts nothing has no line:\n%s", out)
	}
	_ = state
}

// ---------------------------------------------------------------- compare

func seedIndex(t *testing.T, state string, entries ...localci.IndexEntry) {
	t.Helper()
	s := &localci.Store{Dir: state}
	if err := os.MkdirAll(s.RunsPath(), 0o750); err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if err := s.AppendIndex(e, nil); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCompareCommandPrintsTableAndJSON(t *testing.T) {
	f := withStatusFakes(t)
	state := t.TempDir()
	sha := strings.Repeat("c", 40)
	now := time.Now()
	seedIndex(t, state, localci.IndexEntry{RunID: "run1", Workflow: "fast-gate.yml", Event: "pull_request", Repo: "acme/widgets", PR: 4, BaseRef: "develop", HeadRef: "f",
		HeadSHA: sha, Conclusion: "success", Start: now.Add(-time.Minute), End: now})
	f.source.Results = map[string]localci.GHChecks{sha: {
		Runs:     []localci.GHCheck{{Name: "fast-build", State: "success", Outcome: localci.OutcomePass}},
		Statuses: []localci.GHCheck{{Name: "local/fast-build", State: "success", Outcome: localci.OutcomePass}},
	}}
	f.source.Tips = map[string]string{"develop": strings.Repeat("d", 40), "master": strings.Repeat("e", 40)}
	code, out, e := cli(t, nil, "compare", "--repo", "acme/widgets", "--state", state)
	if code != exitOK {
		t.Fatalf("%d %s", code, e)
	}
	for _, w := range []string{"pr 4", "fast-build", "pass / -", "agree", "pull requests in a row that agree: 1 of 20 needed"} {
		if !strings.Contains(out, w) {
			t.Errorf("table lacks %q:\n%s", w, out)
		}
	}
	code, out, e = cli(t, nil, "compare", "--repo", "acme/widgets", "--state", state, "--json")
	if code != exitOK {
		t.Fatalf("%d %s", code, e)
	}
	var rep localci.CompareReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if rep.Streak != 1 || rep.Agree != 1 || rep.Repo != "acme/widgets" {
		t.Fatalf("%+v", rep)
	}
	if code, _, _ := cli(t, nil, "compare", "extra"); code != exitUsage {
		t.Fatal("compare takes no arguments")
	}
}

func TestCompareRefusesAStateFolderThatDoesNotExist(t *testing.T) {
	f := withStatusFakes(t)
	f.source.Tips = map[string]string{"develop": strings.Repeat("d", 40)}
	code, out, e := cli(t, nil, "compare", "--repo", "acme/widgets", "--state", filepath.Join(t.TempDir(), "no-such-store"))
	if code != exitUsage || !strings.Contains(e, "does not exist") || out != "" {
		t.Fatalf("a mistyped --state must not compare against nothing: %d %q %q", code, out, e)
	}
}

// ---------------------------------------------------------------- fallback

func fallbackHost(t *testing.T, wf string) {
	t.Helper()
	h := &wfHost{fakeHost: fakeHost{pr: localci.PullRequest{Number: 9, State: "OPEN", BaseRef: "develop", HeadRef: "feature/x", HeadSHA: strings.Repeat("a", 40), Author: "me", HeadRepoOwner: "acme"}}, wf: wf}
	old := newHost
	newHost = func([]string) localci.Host { return h }
	t.Cleanup(func() { newHost = old })
}

type wfHost struct {
	fakeHost
	wf string
}

func (h *wfHost) FileAt(_ context.Context, _, _, path string) ([]byte, error) {
	if path == ".github/workflows/fast-gate.yml" {
		return []byte(h.wf), nil
	}
	return nil, errors.New("not found")
}

const dispatchable = "name: x\non:\n  pull_request:\n  workflow_dispatch:\njobs:\n  a:\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo hi\n"

func TestFallbackCommandDryRunAndRealUseTheFake(t *testing.T) {
	f := withStatusFakes(t)
	fallbackHost(t, dispatchable)
	code, out, e := cli(t, nil, "fallback", "9", "--repo", "acme/widgets", "--dry-run")
	if code != exitOK || !strings.Contains(out, "dry run: would start fast-gate.yml on branch feature/x") || !strings.Contains(out, "gh workflow run fast-gate.yml --repo acme/widgets --ref feature/x") {
		t.Fatalf("%d\n%s\n%s", code, out, e)
	}
	if len(f.disp.Calls) != 0 {
		t.Fatalf("a dry run starts nothing: %v", f.disp.Calls)
	}
	code, out, e = cli(t, nil, "fallback", "#9", "--repo", "acme/widgets")
	if code != exitOK || !strings.Contains(out, "started fast-gate.yml on branch feature/x for pull request 9") || !strings.Contains(out, "gh run list") {
		t.Fatalf("%d\n%s\n%s", code, out, e)
	}
	if len(f.disp.Calls) != 1 || f.disp.Calls[0] != "acme/widgets fast-gate.yml feature/x" {
		t.Fatalf("%v", f.disp.Calls)
	}
}

func TestFallbackCommandRefusesAndExplains(t *testing.T) {
	f := withStatusFakes(t)
	fallbackHost(t, "name: x\non:\n  pull_request:\njobs:\n  a:\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo hi\n")
	code, _, e := cli(t, nil, "fallback", "9", "--repo", "acme/widgets")
	if code != exitFailed || !strings.Contains(e, "workflow_dispatch") || len(f.disp.Calls) != 0 {
		t.Fatalf("%d %q %v", code, e, f.disp.Calls)
	}
	for name, args := range map[string][]string{
		"no number":  {"fallback", "--repo", "a/b"},
		"two":        {"fallback", "1", "2", "--repo", "a/b"},
		"not number": {"fallback", "x", "--repo", "a/b"},
		"zero":       {"fallback", "0", "--repo", "a/b"},
	} {
		if code, _, _ := cli(t, nil, args...); code != exitUsage {
			t.Errorf("%s: %d", name, code)
		}
	}
}

func TestCompareSaveWritesTheReportTheDashboardReads(t *testing.T) {
	f := withStatusFakes(t)
	state := t.TempDir()
	sha := strings.Repeat("c", 40)
	now := time.Now()
	seedIndex(t, state, localci.IndexEntry{RunID: "run1", Workflow: "fast-gate.yml", Event: "pull_request", Repo: "acme/widgets", PR: 4, BaseRef: "develop", HeadRef: "f",
		HeadSHA: sha, Conclusion: "success", Start: now.Add(-time.Minute), End: now})
	f.source.Results = map[string]localci.GHChecks{sha: {
		Runs:     []localci.GHCheck{{Name: "fast-build", State: "success", Outcome: localci.OutcomePass}},
		Statuses: []localci.GHCheck{{Name: "local/fast-build", State: "success", Outcome: localci.OutcomePass}},
	}}
	f.source.Tips = map[string]string{"develop": strings.Repeat("d", 40), "master": strings.Repeat("e", 40)}
	if code, _, e := cli(t, nil, "compare", "--repo", "acme/widgets", "--state", state); code != exitOK {
		t.Fatalf("%d %s", code, e)
	}
	if rep, err := localci.ReadCompare(state); err != nil || rep != nil {
		t.Fatalf("without --save nothing is written: %v %v", rep, err)
	}
	if code, _, e := cli(t, nil, "compare", "--repo", "acme/widgets", "--state", state, "--save"); code != exitOK {
		t.Fatalf("%d %s", code, e)
	}
	rep, err := localci.ReadCompare(state)
	if err != nil || rep == nil || rep.Repo != "acme/widgets" || rep.Streak != 1 || rep.Agree != 1 {
		t.Fatalf("%+v %v", rep, err)
	}
}

// After the cutover fast-gate.yml has workflow_dispatch and nothing else in on:; that is exactly what the
// fallback needs, so a dry run accepts it and starts nothing.
func TestFallbackCommandAcceptsADispatchOnlyFastGate(t *testing.T) {
	f := withStatusFakes(t)
	fallbackHost(t, "name: x\non:\n  workflow_dispatch:\njobs:\n  a:\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo hi\n")
	code, out, e := cli(t, nil, "fallback", "9", "--repo", "acme/widgets", "--dry-run")
	if code != exitOK || !strings.Contains(out, "dry run: would start fast-gate.yml on branch feature/x") {
		t.Fatalf("%d\n%s\n%s", code, out, e)
	}
	if len(f.disp.Calls) != 0 {
		t.Fatalf("a dry run starts nothing: %v", f.disp.Calls)
	}
}
