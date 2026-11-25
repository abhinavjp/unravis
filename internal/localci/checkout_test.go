package localci

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// hermeticGit makes every git the test (and the code under test) starts ignore the user's own git config, so a
// global autocrlf, signing or hooks setting cannot change what the tests see.
func hermeticGit(t *testing.T) {
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
}

func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:gosec // test helper, fixed binary
	cmd.Dir = dir
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, errOut.String())
	}
	return strings.TrimSpace(out.String())
}

func writeT(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func commitT(t *testing.T, dir, msg string) string {
	t.Helper()
	gitT(t, dir, "add", "-A")
	gitT(t, dir, "commit", "-q", "-m", msg)
	return gitT(t, dir, "rev-parse", "HEAD")
}

const baseWorkflow = `name: Fast gate
on:
  pull_request:
    branches: [develop]
jobs:
  go:
    runs-on: ubuntu-24.04
    steps:
      - name: Say
        run: |
          echo "workflow=base marker=$(cat marker.txt) pw=hunter2secret"
`

// origin is a local bare repository standing in for the remote.
type origin struct {
	bare, work string
	feature    string // head sha of pull request 1: clean against develop
	conflict   string // head sha of pull request 2: conflicts with develop
	evil       string // head sha of pull request 3: rewrites the runner definition
	develop    string // tip of develop
}

// master:  a.txt, marker.txt=master, workflow (base)
// develop: master + d.txt (+ a.txt changed in a later commit)
// PR 1:    from the first develop commit, adds b.txt and changes marker.txt
// PR 2:    changes a.txt in a way that conflicts with develop
// PR 3:    changes the workflow, adds evil.yml and tools/ci/x.mjs
func newOrigin(t *testing.T) *origin {
	t.Helper()
	hermeticGit(t)
	root := t.TempDir()
	o := &origin{bare: filepath.Join(root, "origin.git"), work: filepath.Join(root, "work")}
	gitT(t, root, "init", "-q", "--bare", "-b", "master", o.bare)
	gitT(t, root, "init", "-q", "-b", "master", o.work)
	gitT(t, o.work, "remote", "add", "origin", o.bare)
	writeT(t, o.work, "a.txt", "line one\nline two\nline three\n")
	writeT(t, o.work, "marker.txt", "master")
	writeT(t, o.work, ".github/workflows/fast-gate.yml", baseWorkflow)
	writeT(t, o.work, "tools/ci/check.mjs", "// base\n")
	commitT(t, o.work, "base")
	gitT(t, o.work, "tag", "v1")
	gitT(t, o.work, "checkout", "-q", "-b", "develop")
	writeT(t, o.work, "d.txt", "develop\n")
	first := commitT(t, o.work, "develop 1")

	gitT(t, o.work, "checkout", "-q", "-b", "feature", first)
	writeT(t, o.work, "b.txt", "feature\n")
	writeT(t, o.work, "marker.txt", "feature")
	o.feature = commitT(t, o.work, "feature")

	gitT(t, o.work, "checkout", "-q", "-b", "conflict", first)
	writeT(t, o.work, "a.txt", "line one\nCONFLICT SIDE\nline three\n")
	o.conflict = commitT(t, o.work, "conflict")

	gitT(t, o.work, "checkout", "-q", "-b", "evil", first)
	writeT(t, o.work, ".github/workflows/fast-gate.yml", strings.Replace(baseWorkflow, "workflow=base", "workflow=PR", 1))
	writeT(t, o.work, ".github/workflows/evil.yml", "name: evil\non: pull_request\njobs: {}\n")
	writeT(t, o.work, "tools/ci/x.mjs", "// added by the pull request\n")
	writeT(t, o.work, "tools/ci/check.mjs", "// changed by the pull request\n")
	o.evil = commitT(t, o.work, "evil")

	gitT(t, o.work, "checkout", "-q", "develop")
	writeT(t, o.work, "a.txt", "line one\nDEVELOP SIDE\nline three\n")
	o.develop = commitT(t, o.work, "develop 2")

	gitT(t, o.work, "push", "-q", "origin", "master", "develop", "feature", "conflict", "evil", "v1")
	gitT(t, o.work, "push", "-q", "origin", o.feature+":refs/pull/1/head", o.conflict+":refs/pull/2/head", o.evil+":refs/pull/3/head")
	return o
}

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenStore(filepath.Join(t.TempDir(), "store"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func readT(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p) //nolint:gosec // test helper
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func TestMirrorFetchesEveryRef(t *testing.T) {
	o := newOrigin(t)
	s := newStore(t)
	var log bytes.Buffer
	c := &Checkout{Store: s, Log: &log}
	ctx := context.Background()
	if err := c.Mirror(ctx, o.bare); err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	for ref, want := range map[string]string{
		"refs/remotes/origin/develop": o.develop, "refs/pull/1/head": o.feature, "refs/pull/2/head": o.conflict,
	} {
		if got := gitT(t, s.RepoPath(), "rev-parse", ref); got != want {
			t.Fatalf("%s = %s, want %s", ref, got, want)
		}
	}
	// the other branch of a lint "changed since" comparison, and the tag, are there too
	gitT(t, s.RepoPath(), "rev-parse", "--verify", "refs/remotes/origin/master")
	gitT(t, s.RepoPath(), "rev-parse", "--verify", "refs/tags/v1")
	if got := gitT(t, s.RepoPath(), "config", "core.autocrlf"); got != "false" {
		t.Fatalf("core.autocrlf = %q", got)
	}
	// a second fetch picks up a new commit and a deleted branch
	writeT(t, o.work, "late.txt", "late\n")
	late := commitT(t, o.work, "late")
	gitT(t, o.work, "push", "-q", "origin", "develop")
	gitT(t, o.work, "push", "-q", "origin", "--delete", "feature")
	if err := c.Mirror(ctx, o.bare); err != nil {
		t.Fatal(err)
	}
	if got := gitT(t, s.RepoPath(), "rev-parse", "refs/remotes/origin/develop"); got != late {
		t.Fatalf("develop not updated: %s", got)
	}
	if out := gitT(t, s.RepoPath(), "for-each-ref", "refs/remotes/origin/feature"); out != "" {
		t.Fatalf("the deleted branch is still in the mirror: %s", out)
	}
	if !strings.Contains(log.String(), "$ git fetch") {
		t.Fatalf("no transcript: %s", log.String())
	}
}

func TestMirrorRefusesCredentialsInTheURL(t *testing.T) {
	hermeticGit(t)
	s := newStore(t)
	c := &Checkout{Store: s}
	if err := c.Mirror(context.Background(), "https://user:pa55word@example.invalid/o/r.git"); err == nil {
		t.Fatal("credentials in the origin URL must be refused")
	}
	if exists(filepath.Join(s.RepoPath(), "HEAD")) {
		t.Fatal("nothing may be created for a refused URL")
	}
}

func TestTranscriptIsRedacted(t *testing.T) {
	o := newOrigin(t)
	s := newStore(t)
	var log bytes.Buffer
	redact := func(x string) string { return strings.ReplaceAll(x, "origin.git", "REDACTED") }
	c := &Checkout{Store: s, Log: &log, Redact: redact}
	if err := c.Mirror(context.Background(), o.bare); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(log.String(), "origin.git") || !strings.Contains(log.String(), "REDACTED") {
		t.Fatalf("the transcript must go through the redactor:\n%s", log.String())
	}
}

func mirrored(t *testing.T, o *origin) (*Checkout, *Store) {
	t.Helper()
	s := newStore(t)
	c := &Checkout{Store: s}
	if err := c.Mirror(context.Background(), o.bare); err != nil {
		t.Fatal(err)
	}
	return c, s
}

func TestPullRequestWorktreeIsTheMergeOfHeadIntoBase(t *testing.T) {
	o := newOrigin(t)
	c, s := mirrored(t, o)
	ctx := context.Background()
	wt, err := c.PullRequest(ctx, PRSpec{RunID: "run1", BaseRef: "develop", HeadSHA: o.feature})
	if err != nil {
		t.Fatal(err)
	}
	if wt.Conflict || wt.Path != filepath.Join(s.WorkPath(), "run1") {
		t.Fatalf("%+v", wt)
	}
	// both sides are in the checkout
	if readT(t, filepath.Join(wt.Path, "b.txt")) != "feature\n" || !strings.Contains(readT(t, filepath.Join(wt.Path, "a.txt")), "DEVELOP SIDE") {
		t.Fatal("the worktree is not the merge result")
	}
	if got := strings.TrimSpace(readT(t, filepath.Join(wt.Path, "marker.txt"))); got != "feature" {
		t.Fatalf("marker = %q", got)
	}
	// detached HEAD on a merge commit: first parent the base tip, second the head
	if got := gitT(t, wt.Path, "rev-parse", "HEAD"); got != wt.Commit {
		t.Fatalf("HEAD %s != %s", got, wt.Commit)
	}
	parents := strings.Fields(gitT(t, s.RepoPath(), "rev-list", "--parents", "-n", "1", wt.Commit))
	if len(parents) != 3 || parents[1] != o.develop || parents[2] != o.feature {
		t.Fatalf("parents %v, want [commit %s %s]", parents, o.develop, o.feature)
	}
	if wt.BaseSHA != o.develop || wt.HeadSHA != o.feature {
		t.Fatalf("%+v", wt)
	}
	if st := gitT(t, wt.Path, "status", "--porcelain"); st != "" {
		t.Fatalf("the worktree is not clean: %s", st)
	}
	if err := c.Remove(ctx, wt.Path); err != nil {
		t.Fatal(err)
	}
	if exists(wt.Path) {
		t.Fatal("the worktree is still there")
	}
	if out := gitT(t, s.RepoPath(), "worktree", "list", "--porcelain"); strings.Contains(out, "run1") {
		t.Fatalf("worktree bookkeeping left behind:\n%s", out)
	}
}

func TestPullRequestConflictMakesNoWorktree(t *testing.T) {
	o := newOrigin(t)
	c, s := mirrored(t, o)
	wt, err := c.PullRequest(context.Background(), PRSpec{RunID: "run2", BaseRef: "develop", HeadSHA: o.conflict})
	if err != nil {
		t.Fatal(err)
	}
	if !wt.Conflict || wt.Path != "" || !strings.Contains(wt.Detail, "a.txt") {
		t.Fatalf("%+v", wt)
	}
	if exists(filepath.Join(s.WorkPath(), "run2")) {
		t.Fatal("a conflict must not leave a worktree")
	}
}

func TestPullRequestRejectsBadInput(t *testing.T) {
	o := newOrigin(t)
	c, _ := mirrored(t, o)
	ctx := context.Background()
	good := PRSpec{RunID: "run3", BaseRef: "develop", HeadSHA: o.feature}
	for name, mut := range map[string]func(*PRSpec){
		"short sha":        func(p *PRSpec) { p.HeadSHA = "abc123" },
		"option as sha":    func(p *PRSpec) { p.HeadSHA = "--upload-pack=x" },
		"bad run id":       func(p *PRSpec) { p.RunID = "../escape" },
		"option as branch": func(p *PRSpec) { p.BaseRef = "--force" },
		"unknown branch":   func(p *PRSpec) { p.BaseRef = "nope" },
		"unknown sha":      func(p *PRSpec) { p.HeadSHA = strings.Repeat("0", 40) },
	} {
		spec := good
		mut(&spec)
		if wt, err := c.PullRequest(ctx, spec); err == nil {
			t.Fatalf("%s: expected an error, got %+v", name, wt)
		}
	}
}

func TestOverlayBaseReplacesTheRunnerDefinition(t *testing.T) {
	o := newOrigin(t)
	c, _ := mirrored(t, o)
	ctx := context.Background()
	protect := []string{"tools/ci"}
	// the owner's own pull request keeps what it changed
	own, err := c.PullRequest(ctx, PRSpec{RunID: "own", BaseRef: "develop", HeadSHA: o.evil, Protect: protect})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readT(t, filepath.Join(own.Path, ".github", "workflows", "fast-gate.yml")), "workflow=PR") {
		t.Fatal("an owner pull request runs its own workflow")
	}
	// a collaborator's pull request gets the definition of the base branch
	wt, err := c.PullRequest(ctx, PRSpec{RunID: "collab", BaseRef: "develop", HeadSHA: o.evil, FromBase: true, Protect: protect})
	if err != nil {
		t.Fatal(err)
	}
	if got := readT(t, filepath.Join(wt.Path, ".github", "workflows", "fast-gate.yml")); got != baseWorkflow {
		t.Fatalf("the workflow is not the base one:\n%s", got)
	}
	if exists(filepath.Join(wt.Path, ".github", "workflows", "evil.yml")) {
		t.Fatal("a workflow added by the pull request must be deleted")
	}
	if exists(filepath.Join(wt.Path, "tools", "ci", "x.mjs")) {
		t.Fatal("a protected file added by the pull request must be deleted")
	}
	if got := readT(t, filepath.Join(wt.Path, "tools", "ci", "check.mjs")); got != "// base\n" {
		t.Fatalf("a protected file changed by the pull request must be the base one: %q", got)
	}
	if readT(t, filepath.Join(wt.Path, "d.txt")) != "develop\n" {
		t.Fatal("everything else stays")
	}
	// bad protected paths are refused and leave no worktree
	for _, bad := range []string{"..", "../x", ".git", ".git/hooks", "/abs"} {
		if _, err := c.PullRequest(ctx, PRSpec{RunID: "bad" + strings.NewReplacer("/", "", ".", "d").Replace(bad), BaseRef: "develop", HeadSHA: o.feature, FromBase: true, Protect: []string{bad}}); err == nil {
			t.Fatalf("protected path %q must be refused", bad)
		}
	}
}

func TestCommitWorktreeAndRemoveGuards(t *testing.T) {
	o := newOrigin(t)
	c, s := mirrored(t, o)
	ctx := context.Background()
	wt, err := c.Commit(ctx, "push1", o.develop)
	if err != nil {
		t.Fatal(err)
	}
	if wt.Commit != o.develop || !strings.Contains(readT(t, filepath.Join(wt.Path, "a.txt")), "DEVELOP SIDE") {
		t.Fatalf("%+v", wt)
	}
	if _, err := c.Commit(ctx, "push2", "deadbeef"); err == nil {
		t.Fatal("a short sha must be refused")
	}
	outside := t.TempDir()
	for _, p := range []string{"", outside, s.WorkPath(), filepath.Join(s.WorkPath(), "..", "repo.git"), s.Dir} {
		if err := c.Remove(ctx, p); err == nil {
			t.Fatalf("Remove(%q) must be refused", p)
		}
	}
	if !exists(outside) || !exists(s.RepoPath()) {
		t.Fatal("a guarded Remove deleted something")
	}
	if err := c.Remove(ctx, wt.Path); err != nil || exists(wt.Path) {
		t.Fatalf("remove: %v", err)
	}
	// removing what is already gone is fine
	if err := c.Remove(ctx, wt.Path); err != nil {
		t.Fatalf("second remove: %v", err)
	}
}
