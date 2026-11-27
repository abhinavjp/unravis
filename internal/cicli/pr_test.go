package cicli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"api-audit-explorer/internal/localci"
)

// fakeHost answers from memory: no gh, no network.
type fakeHost struct {
	pr      localci.PullRequest
	url     string
	commits []localci.PRCommit // nil: one commit by the author of the pull request
	collabs []string
	list    []localci.PullRequest // what OpenPullRequests lists
}

func (f *fakeHost) PullRequest(context.Context, string, int) (localci.PullRequest, error) {
	return f.pr, nil
}
func (f *fakeHost) Collaborators(context.Context, string) ([]string, error) { return f.collabs, nil }
func (f *fakeHost) Commits(context.Context, string, int) ([]localci.PRCommit, error) {
	if f.commits != nil {
		return f.commits, nil
	}
	return []localci.PRCommit{{SHA: f.pr.HeadSHA, AuthorLogin: f.pr.Author, CommitterLogin: f.pr.Author}}, nil
}
func (f *fakeHost) PushHistory(context.Context, string, string) (localci.PushHistory, error) {
	// the pull request's author created the branch and pushed its head, and nobody else touched the branch
	return localci.PushHistory{Events: []localci.PushEvent{
		{After: f.pr.HeadSHA, Type: "push", Actor: f.pr.Author},
		{After: strings.Repeat("d", 40), Type: "branch_creation", Actor: f.pr.Author},
	}, Complete: true}, nil
}
func (f *fakeHost) CloneURL(string) string { return f.url }
func (f *fakeHost) OpenPullRequests(context.Context, string, string) (localci.PullList, error) {
	return localci.PullList{PRs: f.list, ETag: "e1"}, nil
}
func (f *fakeHost) BranchTip(context.Context, string, string, string) (localci.BranchInfo, error) {
	return localci.BranchInfo{}, errors.New("not used")
}
func (f *fakeHost) FileAt(context.Context, string, string, string) ([]byte, error) {
	return nil, errors.New("not used")
}
func (f *fakeHost) ListDir(context.Context, string, string, string) ([]string, error) {
	return nil, errors.New("not used")
}

func gitC(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:gosec // test helper, fixed binary
	cmd.Dir = dir
	var out, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &e
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, e.String())
	}
	return strings.TrimSpace(out.String())
}

func putC(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func commitC(t *testing.T, dir, msg string) string {
	t.Helper()
	gitC(t, dir, "add", "-A")
	gitC(t, dir, "commit", "-q", "-m", msg)
	return gitC(t, dir, "rev-parse", "HEAD")
}

// prSetup builds a local bare origin with develop, a clean pull request (1) and a conflicting one (2) and
// returns the bare path and the two head shas.
func prSetup(t *testing.T) (bare, clean, conflict string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found")
	}
	empty := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{
		"GIT_CONFIG_GLOBAL": empty, "GIT_CONFIG_NOSYSTEM": "1",
		"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@localhost", "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@localhost",
	} {
		t.Setenv(k, v)
	}
	root := t.TempDir()
	bare, work := filepath.Join(root, "o.git"), filepath.Join(root, "w")
	gitC(t, root, "init", "-q", "--bare", "-b", "develop", bare)
	gitC(t, root, "init", "-q", "-b", "develop", work)
	gitC(t, work, "remote", "add", "origin", bare)
	putC(t, work, "a.txt", "one\ntwo\nthree\n")
	putC(t, work, ".github/workflows/fast-gate.yml", `name: Fast gate
on: pull_request
jobs:
  go:
    runs-on: ubuntu-24.04
    steps:
      - run: echo "marker=$(cat marker.txt) token=[$MY_TOKEN]"
`)
	putC(t, work, "marker.txt", "base")
	base := commitC(t, work, "base")
	gitC(t, work, "checkout", "-q", "-b", "feature")
	putC(t, work, "marker.txt", "feature")
	clean = commitC(t, work, "feature")
	gitC(t, work, "checkout", "-q", "-b", "conflict", base)
	putC(t, work, "a.txt", "one\nCONFLICT\nthree\n")
	conflict = commitC(t, work, "conflict")
	gitC(t, work, "checkout", "-q", "develop")
	putC(t, work, "a.txt", "one\nDEVELOP\nthree\n")
	commitC(t, work, "develop 2")
	gitC(t, work, "push", "-q", "origin", "develop")
	gitC(t, work, "push", "-q", "origin", clean+":refs/pull/1/head", conflict+":refs/pull/2/head")
	return bare, clean, conflict
}

func withHost(t *testing.T, h *fakeHost) {
	t.Helper()
	old := newHost
	newHost = func([]string) localci.Host { return h }
	t.Cleanup(func() { newHost = old })
}

func TestRunPRUsage(t *testing.T) {
	for name, args := range map[string][]string{
		"no number":     {"run-pr", "--repo", "o/r"},
		"bad number":    {"run-pr", "12x", "--repo", "o/r"},
		"zero":          {"run-pr", "0", "--repo", "o/r"},
		"no repo":       {"run-pr", "5"},
		"two numbers":   {"run-pr", "5", "6", "--repo", "o/r"},
		"bad workflow":  {"run-pr", "5", "--repo", "o/r", "--workflow-for", "develop"},
		"bad var":       {"run-pr", "5", "--repo", "o/r", "--var", "novalue"},
		"log no args":   {"log"},
		"log too many":  {"log", "a", "b", "c"},
		"runs argument": {"runs", "extra"},
	} {
		if code, _, _ := cli(t, nil, args...); code != exitUsage {
			t.Errorf("%s: exit %d, want %d", name, code, exitUsage)
		}
	}
}

func TestRunPRRunsLogsAndRuns(t *testing.T) {
	bare, clean, conflict := prSetup(t)
	state := filepath.Join(t.TempDir(), "store")
	h := &fakeHost{url: bare, pr: localci.PullRequest{Number: 1, State: "OPEN", BaseRef: "develop", HeadRef: "feature", HeadSHA: clean, Author: "acme", HeadRepoOwner: "acme"}}
	withHost(t, h)
	env := append(os.Environ(), "MY_TOKEN=hunter2secret")

	code, out, errOut := cli(t, env, "run-pr", "1", "--repo", "acme/widgets", "--state", state, "--origin", bare)
	if code != exitOK {
		t.Fatalf("exit %d\n%s\n%s", code, out, errOut)
	}
	if strings.Contains(out, "hunter2secret") {
		t.Fatalf("the console must be masked:\n%s", out)
	}
	var runID string
	for _, l := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(l, "run id: "); ok {
			runID = strings.TrimSpace(v)
		}
	}
	if runID == "" {
		t.Fatalf("no run id printed:\n%s", out)
	}

	// the same run, read back through the commands
	code, listing, _ := cli(t, nil, "runs", "--state", state)
	if code != exitOK || !strings.Contains(listing, runID) || !strings.Contains(listing, "success") || !strings.Contains(listing, "#1") {
		t.Fatalf("runs: %d\n%s", code, listing)
	}
	code, jobs, _ := cli(t, nil, "log", runID, "--state", state)
	if code != exitOK || !strings.Contains(jobs, "go") || !strings.Contains(jobs, "_checkout") {
		t.Fatalf("log <run>: %d\n%s", code, jobs)
	}
	code, goLog, _ := cli(t, nil, "log", runID, "go", "--state", state)
	if code != exitOK || !strings.Contains(goLog, "marker=feature token=[]") || strings.Contains(goLog, "hunter2secret") {
		t.Fatalf("the secret variable must not reach the step: %d\n%s", code, goLog)
	}
	code, coLog, _ := cli(t, nil, "log", runID, "_checkout", "--state", state)
	if code != exitOK || !strings.Contains(coLog, "merge-tree") {
		t.Fatalf("checkout log: %d\n%s", code, coLog)
	}
	if code, _, e := cli(t, nil, "log", runID, "nope", "--state", state); code != exitUsage || !strings.Contains(e, "go") {
		t.Fatalf("unknown job: %d %q", code, e)
	}
	if code, _, _ := cli(t, nil, "log", "../etc", "go", "--state", state); code != exitUsage {
		t.Fatalf("a run id with a path: %d", code)
	}
	if entries, _ := os.ReadDir(filepath.Join(state, "work")); len(entries) != 0 {
		t.Fatalf("work/ must be empty after the run: %v", entries)
	}

	// a pull request that does not merge is a failed run (exit 1), listed with its reason
	h.pr.Number, h.pr.HeadSHA = 2, conflict
	code, out, errOut = cli(t, env, "run-pr", "2", "--repo", "acme/widgets", "--state", state, "--origin", bare)
	if code != exitFailed || !strings.Contains(out, "merge conflict") {
		t.Fatalf("conflict: exit %d\n%s\n%s", code, out, errOut)
	}
	if _, listing, _ := cli(t, nil, "runs", "--state", state); !strings.Contains(listing, "merge conflict") || !strings.Contains(listing, "#2") {
		t.Fatalf("%s", listing)
	}
	// --limit
	if _, listing, _ := cli(t, nil, "runs", "--state", state, "--limit", "1"); strings.Contains(listing, runID) {
		t.Fatalf("--limit 1 must show the latest run only:\n%s", listing)
	}
	// a workflow that is not there for the base: setup error, exit 2
	code, _, errOut = cli(t, env, "run-pr", "2", "--repo", "acme/widgets", "--state", state, "--origin", bare, "--workflow-for", "master=ci.yml")
	if code != exitUsage || !strings.Contains(errOut, "develop") {
		t.Fatalf("no workflow for the base: %d %q", code, errOut)
	}
}

func TestRunsOnAnEmptyStore(t *testing.T) {
	code, out, _ := cli(t, nil, "runs", "--state", filepath.Join(t.TempDir(), "nothing"))
	if code != exitOK || !strings.Contains(out, "no runs") {
		t.Fatalf("%d %q", code, out)
	}
}

func TestRunPRNonOwnerRunsOnlyIntoATrustedBase(t *testing.T) {
	bare, clean, _ := prSetup(t)
	gitC(t, bare, "branch", "staging", "develop") // a branch anybody with push access could have made
	state := filepath.Join(t.TempDir(), "store")
	h := &fakeHost{url: bare, collabs: []string{"bob"},
		pr: localci.PullRequest{Number: 1, State: "OPEN", BaseRef: "develop", HeadRef: "feature", HeadSHA: clean, Author: "bob", HeadRepoOwner: "acme"}}
	withHost(t, h)
	run := func(extra ...string) (int, string) {
		code, out, errOut := cli(t, os.Environ(), append([]string{"run-pr", "1", "--repo", "acme/widgets", "--state", state, "--origin", bare}, extra...)...)
		return code, out + errOut
	}

	// into a base of the workflow map: runs, with the definition of the base
	if code, out := run(); code != exitOK {
		t.Fatalf("a collaborator into develop: exit %d\n%s", code, out)
	}
	// into any other base: refused, whatever --workflow says
	h.pr.BaseRef = "staging"
	code, out := run("--workflow", "fast-gate.yml")
	if code != exitFailed || !strings.Contains(out, "trusts") {
		t.Fatalf("staging is not trusted: exit %d\n%s", code, out)
	}
	runID := ""
	for _, l := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(l, "run id: "); ok {
			runID = strings.TrimSpace(v)
		}
	}
	// the refusal is a runner stage, listed as such by `log`, not a job of the workflow
	if _, listing, _ := cli(t, nil, "log", runID, "--state", state); !strings.Contains(listing, "_trust") || !strings.Contains(listing, "runner stage") {
		t.Fatalf("log must list the stage:\n%s", listing)
	}
	// --trusted-base names it on purpose
	if code, out := run("--workflow", "fast-gate.yml", "--trusted-base", "staging"); code != exitOK {
		t.Fatalf("--trusted-base staging: exit %d\n%s", code, out)
	}
	// the owner's own pull request is not restricted
	h.pr.Author, h.pr.BaseRef = "acme", "staging"
	if code, out := run("--workflow", "fast-gate.yml"); code != exitOK {
		t.Fatalf("the owner into staging: exit %d\n%s", code, out)
	}
}
