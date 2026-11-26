package localci

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeHost is a Host that answers from memory: no gh, no network.
type fakeHost struct {
	watchStub
	pr         PullRequest
	prErr      error
	collabs    []string
	collabsErr error
	// commits are what the host lists for the pull request; nil means one commit written by the author.
	commits    []PRCommit
	commitsErr error
	// pusher is who the host says pushed the head commit; empty means the author of the pull request.
	// noPusher makes the host answer "no record"; pusherErr makes the lookup fail.
	pusher      string
	noPusher    bool
	pusherErr   error
	pusherAsked []string // "ref@sha" of every HeadPusher call
	url         string
	asked       int
}

func (f *fakeHost) HeadPusher(_ context.Context, _, headRef, headSHA string) (string, error) {
	f.pusherAsked = append(f.pusherAsked, headRef+"@"+headSHA)
	if f.pusherErr != nil {
		return "", f.pusherErr
	}
	if f.noPusher {
		return "", nil
	}
	if f.pusher != "" {
		return f.pusher, nil
	}
	return f.pr.Author, nil
}

func (f *fakeHost) Commits(context.Context, string, int) ([]PRCommit, error) {
	if f.commitsErr != nil {
		return nil, f.commitsErr
	}
	if f.commits != nil {
		return f.commits, nil
	}
	return []PRCommit{{SHA: strings.Repeat("a", 40), AuthorLogin: f.pr.Author, CommitterLogin: f.pr.Author}}, nil
}

func (f *fakeHost) PullRequest(context.Context, string, int) (PullRequest, error) {
	f.asked++
	return f.pr, f.prErr
}
func (f *fakeHost) Collaborators(context.Context, string) ([]string, error) {
	return f.collabs, f.collabsErr
}
func (f *fakeHost) CloneURL(string) string { return f.url }

func prOptions(t *testing.T, o *origin, h *fakeHost) (PROptions, *bytes.Buffer) {
	t.Helper()
	var console bytes.Buffer
	return PROptions{
		Store: newStore(t), Host: h, Repo: "acme/widgets", Number: h.pr.Number, OriginURL: o.bare,
		Workflows: map[string]string{"develop": "fast-gate.yml"}, Protect: []string{"tools/ci"}, Log: &console,
		Redact: func(s string) string { return strings.ReplaceAll(s, "hunter2secret", "***") },
	}, &console
}

func okPR(o *origin, number int, head, author string) PullRequest {
	return PullRequest{Number: number, State: "OPEN", BaseRef: "develop", HeadRef: "feature", HeadSHA: head, Author: author, HeadRepoOwner: "acme"}
}

// logOf reads every log of a job of a stored run, as the `log` command does.
func logOf(t *testing.T, s *Store, runID, job string) string {
	t.Helper()
	parts, err := s.JobLogs(runID, job)
	if err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	for _, p := range parts {
		sb.WriteString(readT(t, p.Path))
	}
	return sb.String()
}

func TestRunPROwnerRunsItsOwnWorkflowAndCleansUp(t *testing.T) {
	needBash(t)
	o := newOrigin(t)
	h := &fakeHost{pr: okPR(o, 1, o.feature, "acme"), collabsErr: errors.New("the owner needs no collaborator list")}
	opts, console := prOptions(t, o, h)
	res, err := RunPR(context.Background(), opts)
	if err != nil {
		t.Fatalf("%v\n%s", err, console.String())
	}
	if res.Run.Conclusion != "success" {
		t.Fatalf("%+v\n%s", res.Run, console.String())
	}
	s := opts.Store
	// the run folder holds run.json, the step log, the checkout log
	if rec, err := s.ReadRunRecord(res.RunID); err != nil || rec.Conclusion != "success" {
		t.Fatalf("%+v %v", rec, err)
	}
	log := logOf(t, s, res.RunID, "go")
	if !strings.Contains(log, "workflow=base") || !strings.Contains(log, "marker=feature") {
		t.Fatalf("the step ran on the merge of the pull request:\n%s", log)
	}
	if strings.Contains(log, "hunter2secret") || !strings.Contains(log, "pw=***") {
		t.Fatalf("the step log must be masked:\n%s", log)
	}
	if !strings.Contains(logOf(t, s, res.RunID, CheckoutLogName), "$ git merge-tree") {
		t.Fatal("the checkout log has no git transcript")
	}
	// the index has exactly this run, with the merge commit that ran
	idx, err := s.ReadIndex()
	if err != nil || len(idx) != 1 {
		t.Fatalf("%v %v", idx, err)
	}
	e := idx[0]
	if e.RunID != res.RunID || e.Conclusion != "success" || e.PR != 1 || e.HeadSHA != o.feature || e.BaseRef != "develop" ||
		e.Workflow != "fast-gate.yml" || e.Event != "pull_request" || len(e.CommitSHA) != 40 || e.CommitSHA == o.feature {
		t.Fatalf("%+v", e)
	}
	// the worktree is gone, the caches are the store's
	if exists(filepath.Join(s.WorkPath(), res.RunID)) {
		t.Fatal("the worktree must be removed after the run")
	}
	if entries, _ := os.ReadDir(s.WorkPath()); len(entries) != 0 {
		t.Fatalf("work/ is not empty: %v", entries)
	}
	if exists(filepath.Join(res.RunDir, "tmp")) {
		t.Fatal("step scripts are not part of what stays")
	}
	if !exists(filepath.Join(s.CachesPath(), "go-build")) || !exists(filepath.Join(s.CachesPath(), "npm")) {
		t.Fatal("the caches folders were not created")
	}
}

func TestRunPRKeepLeavesTheWorktree(t *testing.T) {
	needBash(t)
	o := newOrigin(t)
	h := &fakeHost{pr: okPR(o, 1, o.feature, "acme")}
	opts, _ := prOptions(t, o, h)
	opts.Keep = true
	res, err := RunPR(context.Background(), opts)
	if err != nil || res.Run.Conclusion != "success" {
		t.Fatalf("%v %+v", err, res)
	}
	if res.Workdir == "" || !exists(filepath.Join(res.Workdir, "b.txt")) {
		t.Fatalf("--keep must leave %s", res.Workdir)
	}
}

func TestRunPRCollaboratorRunsTheBaseDefinition(t *testing.T) {
	needBash(t)
	o := newOrigin(t)
	h := &fakeHost{pr: okPR(o, 3, o.evil, "Alice"), collabs: []string{"alice"}}
	opts, console := prOptions(t, o, h)
	res, err := RunPR(context.Background(), opts)
	if err != nil {
		t.Fatalf("%v\n%s", err, console.String())
	}
	log := logOf(t, opts.Store, res.RunID, "go")
	if !strings.Contains(log, "workflow=base") || strings.Contains(log, "workflow=PR") {
		t.Fatalf("a collaborator's pull request must run the workflow of the base branch:\n%s", log)
	}
}

func TestRunPRStrangerAndForkNeverRun(t *testing.T) {
	o := newOrigin(t)
	for name, tc := range map[string]struct {
		author, headOwner string
		fork              bool
		want              string
	}{
		"stranger": {"mallory", "acme", false, "mallory"},
		"fork":     {"acme", "mallory", true, "fork"},
	} {
		t.Run(name, func(t *testing.T) {
			pr := okPR(o, 3, o.evil, tc.author)
			pr.HeadRepoOwner, pr.IsFork = tc.headOwner, tc.fork
			h := &fakeHost{pr: pr, collabs: []string{"alice"}}
			opts, _ := prOptions(t, o, h)
			res, err := RunPR(context.Background(), opts)
			if err != nil {
				t.Fatal(err)
			}
			if res.Run.Conclusion != "failure" || !strings.Contains(res.Entry.Reason, tc.want) {
				t.Fatalf("%+v", res.Entry)
			}
			s := opts.Store
			if exists(s.RepoPath()) {
				t.Fatal("nothing of a refused pull request may be fetched")
			}
			if entries, _ := os.ReadDir(s.WorkPath()); len(entries) != 0 {
				t.Fatalf("a refused pull request got a worktree: %v", entries)
			}
			idx, _ := s.ReadIndex()
			if len(idx) != 1 || idx[0].Conclusion != "failure" || idx[0].PR != 3 {
				t.Fatalf("%+v", idx)
			}
			if !strings.Contains(logOf(t, s, res.RunID, CheckoutLogName), "not run") {
				t.Fatal("the checkout log must say why it did not run")
			}
		})
	}
}

func TestRunPRMergeConflictIsAFailedRun(t *testing.T) {
	o := newOrigin(t)
	h := &fakeHost{pr: okPR(o, 2, o.conflict, "acme")}
	opts, _ := prOptions(t, o, h)
	res, err := RunPR(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if res.Run.Conclusion != "failure" || res.Entry.Reason != "merge conflict" {
		t.Fatalf("%+v", res.Entry)
	}
	if len(res.Run.Jobs) != 1 || res.Run.Jobs[0].Key != CheckoutLogName || !res.Run.Jobs[0].Pseudo {
		t.Fatalf("only the pseudo job of the checkout: %+v", res.Run.Jobs)
	}
	if entries, _ := os.ReadDir(opts.Store.WorkPath()); len(entries) != 0 {
		t.Fatalf("a conflict must not leave a worktree: %v", entries)
	}
	if !strings.Contains(logOf(t, opts.Store, res.RunID, CheckoutLogName), "a.txt") {
		t.Fatal("the checkout log names the conflicting file")
	}
}

func TestRunPRHostAndSetupErrorsAreErrors(t *testing.T) {
	o := newOrigin(t)
	ctx := context.Background()
	// the host fails: nothing is created
	h := &fakeHost{prErr: errors.New("gh: not found")}
	h.pr.Number = 1
	opts, _ := prOptions(t, o, h)
	if _, err := RunPR(ctx, opts); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("%v", err)
	}
	if idx, _ := opts.Store.ReadIndex(); len(idx) != 0 {
		t.Fatal("a pull request that could not be read has no run")
	}
	// a closed pull request does not run
	pr := okPR(o, 1, o.feature, "acme")
	pr.State = "MERGED"
	opts, _ = prOptions(t, o, &fakeHost{pr: pr})
	if _, err := RunPR(ctx, opts); err == nil || !strings.Contains(err.Error(), "merged") {
		t.Fatalf("%v", err)
	}
	// no workflow for this base branch
	opts, _ = prOptions(t, o, &fakeHost{pr: okPR(o, 1, o.feature, "acme")})
	opts.Workflows = map[string]string{"master": "ci.yml"}
	if _, err := RunPR(ctx, opts); err == nil || !strings.Contains(err.Error(), "develop") {
		t.Fatalf("%v", err)
	}
	// a workflow name that is a path
	opts, _ = prOptions(t, o, &fakeHost{pr: okPR(o, 1, o.feature, "acme")})
	opts.Workflows = map[string]string{"develop": "../../x.yml"}
	if _, err := RunPR(ctx, opts); err == nil {
		t.Fatal("a workflow outside .github/workflows must be refused")
	}
	// the collaborator list cannot be read: recorded as an error run, nothing fetched
	h = &fakeHost{pr: okPR(o, 1, o.feature, "alice"), collabsErr: errors.New("gh: 403")}
	opts, _ = prOptions(t, o, h)
	res, err := RunPR(ctx, opts)
	if err == nil || res == nil || res.Run.Conclusion != "error" {
		t.Fatalf("%v %+v", err, res)
	}
	if idx, _ := opts.Store.ReadIndex(); len(idx) != 1 || idx[0].Conclusion != "error" {
		t.Fatalf("%+v", idx)
	}
	// the workflow is missing in the merge
	pr = okPR(o, 1, o.feature, "acme")
	opts, _ = prOptions(t, o, &fakeHost{pr: pr})
	opts.Workflows = map[string]string{"develop": "absent.yml"}
	res, err = RunPR(ctx, opts)
	if err == nil || res == nil || res.Run.Conclusion != "error" || !strings.Contains(err.Error(), "absent.yml") {
		t.Fatalf("%v %+v", err, res)
	}
	if entries, _ := os.ReadDir(opts.Store.WorkPath()); len(entries) != 0 {
		t.Fatalf("a failed setup must still clean the worktree: %v", entries)
	}
}

func TestRunPRRunsGetDistinctIDs(t *testing.T) {
	needBash(t)
	o := newOrigin(t)
	h := &fakeHost{pr: okPR(o, 1, o.feature, "acme")}
	opts, _ := prOptions(t, o, h)
	a, err := RunPR(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	b, err := RunPR(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if a.RunID == b.RunID {
		t.Fatal("two runs, one id")
	}
	if idx, _ := opts.Store.ReadIndex(); len(idx) != 2 {
		t.Fatalf("%d index lines", len(idx))
	}
}

func TestRunPROwnerPRWithAForeignCommitRunsTheBaseDefinition(t *testing.T) {
	needBash(t)
	o := newOrigin(t)
	for name, commits := range map[string][]PRCommit{
		"another author":    {{SHA: strings.Repeat("a", 40), AuthorLogin: "acme", CommitterLogin: "acme"}, {SHA: strings.Repeat("b", 40), AuthorLogin: "alice", CommitterLogin: "acme"}},
		"another committer": {{SHA: strings.Repeat("a", 40), AuthorLogin: "acme", CommitterLogin: "alice"}},
		"unknown login":     {{SHA: strings.Repeat("a", 40), AuthorLogin: "", CommitterLogin: "acme"}},
		"truncated list":    {{SHA: strings.Repeat("a", 40), AuthorLogin: "acme", CommitterLogin: "acme"}, {SHA: "(more commits than the host lists)"}},
	} {
		t.Run(name, func(t *testing.T) {
			h := &fakeHost{pr: okPR(o, 3, o.evil, "acme"), commits: commits}
			opts, console := prOptions(t, o, h)
			res, err := RunPR(context.Background(), opts)
			if err != nil {
				t.Fatalf("%v\n%s", err, console.String())
			}
			log := logOf(t, opts.Store, res.RunID, "go")
			if !strings.Contains(log, "workflow=base") || strings.Contains(log, "workflow=PR") {
				t.Fatalf("a branch with somebody else's commit must run the base definition:\n%s", log)
			}
			if !exists(filepath.Join(opts.Store.UntrustedCachesPath(), "go-build")) || exists(filepath.Join(opts.Store.CachesPath(), "go-build")) {
				t.Fatal("such a run must use the caches kept apart from the owner's")
			}
		})
	}
	// every commit by the owner: the owner's own definition and the owner's caches
	h := &fakeHost{pr: okPR(o, 3, o.evil, "acme"), commits: []PRCommit{{SHA: strings.Repeat("a", 40), AuthorLogin: "ACME", CommitterLogin: "acme"}}}
	opts, _ := prOptions(t, o, h)
	res, err := RunPR(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logOf(t, opts.Store, res.RunID, "go"), "workflow=PR") {
		t.Fatal("the owner's own branch keeps its own definition")
	}
	if !exists(filepath.Join(opts.Store.CachesPath(), "go-build")) || exists(opts.Store.UntrustedCachesPath()) && exists(filepath.Join(opts.Store.UntrustedCachesPath(), "go-build")) {
		t.Fatal("the owner's run uses the owner's caches")
	}
}

// TestRunPROwnerLoginsPushedBySomebodyElseRunsTheBaseDefinition is the test for RV-SEC-3: every commit of the
// branch carries the owner's login (a collaborator can write any identity into a commit), but the host's record
// says somebody else pushed the head, or says nothing, or cannot be asked. None of these may run the
// pull request's own workflow files or use the owner's caches.
func TestRunPROwnerLoginsPushedBySomebodyElseRunsTheBaseDefinition(t *testing.T) {
	needBash(t)
	o := newOrigin(t)
	for name, tweak := range map[string]func(*fakeHost){
		"a collaborator pushed it":          func(h *fakeHost) { h.pusher = "alice" },
		"owner commits, other pusher login": func(h *fakeHost) { h.pusher = "mallory" },
		"no record of the push":             func(h *fakeHost) { h.noPusher = true },
		"the lookup failed":                 func(h *fakeHost) { h.pusherErr = errors.New("gh: 502") },
		"the owner pushed other people's commits": func(h *fakeHost) {
			h.commits = []PRCommit{{SHA: strings.Repeat("a", 40), AuthorLogin: "alice", CommitterLogin: "alice"}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			h := &fakeHost{pr: okPR(o, 3, o.evil, "acme"), commits: []PRCommit{{SHA: strings.Repeat("a", 40), AuthorLogin: "acme", CommitterLogin: "acme"}}}
			tweak(h)
			opts, console := prOptions(t, o, h)
			res, err := RunPR(context.Background(), opts)
			if err != nil {
				t.Fatalf("%v\n%s", err, console.String())
			}
			log := logOf(t, opts.Store, res.RunID, "go")
			if !strings.Contains(log, "workflow=base") || strings.Contains(log, "workflow=PR") {
				t.Fatalf("a head the owner did not push must run the base definition:\n%s", log)
			}
			if !exists(filepath.Join(opts.Store.UntrustedCachesPath(), "go-build")) || exists(filepath.Join(opts.Store.CachesPath(), "go-build")) {
				t.Fatal("such a run must use the caches kept apart from the owner's")
			}
		})
	}
	// without a trusted base the same pull request is refused, not run
	h := &fakeHost{pr: okPR(o, 3, o.evil, "acme"), pusher: "alice"}
	opts, _ := prOptions(t, o, h)
	opts.Workflows, opts.Workflow = nil, "fast-gate.yml"
	res, err := RunPR(context.Background(), opts)
	if err != nil || res.Run.Conclusion != "failure" || !strings.Contains(res.Entry.Reason, "trusts") {
		t.Fatalf("%v %+v", err, res)
	}
	// the owner pushed the head: the pull request's own definition, and the host was asked about this very head
	h = &fakeHost{pr: okPR(o, 3, o.evil, "acme"), pusher: "ACME"}
	opts, _ = prOptions(t, o, h)
	res, err = RunPR(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logOf(t, opts.Store, res.RunID, "go"), "workflow=PR") {
		t.Fatal("a head pushed by the owner keeps the owner's definition")
	}
	if len(h.pusherAsked) != 1 || h.pusherAsked[0] != "feature@"+o.evil {
		t.Fatalf("the host must be asked about the head branch and commit that run: %v", h.pusherAsked)
	}
}

func TestRunPRCommitListErrorIsAnError(t *testing.T) {
	o := newOrigin(t)
	h := &fakeHost{pr: okPR(o, 1, o.feature, "acme"), commitsErr: errors.New("gh: 502")}
	opts, _ := prOptions(t, o, h)
	res, err := RunPR(context.Background(), opts)
	if err == nil || res == nil || res.Run.Conclusion != "error" {
		t.Fatalf("%v %+v", err, res)
	}
	if exists(opts.Store.RepoPath()) {
		t.Fatal("nothing may be fetched when the commits cannot be read")
	}
}

func TestRunPRNonOwnerCodeNeedsATrustedBase(t *testing.T) {
	needBash(t)
	o := newOrigin(t)
	ctx := context.Background()
	// --workflow without any trusted base: a collaborator's pull request is refused, whatever the base is
	h := &fakeHost{pr: okPR(o, 3, o.evil, "alice"), collabs: []string{"alice"}}
	opts, _ := prOptions(t, o, h)
	opts.Workflows, opts.Workflow = nil, "fast-gate.yml"
	res, err := RunPR(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	if res.Run.Conclusion != "failure" || !strings.Contains(res.Entry.Reason, "trusts") || res.Run.Jobs[0].Key != TrustJobKey || !res.Run.Jobs[0].Pseudo {
		t.Fatalf("%+v", res.Entry)
	}
	if exists(opts.Store.RepoPath()) {
		t.Fatal("a refused pull request must not be fetched")
	}
	// an owner pull request with a foreign commit is no better
	h = &fakeHost{pr: okPR(o, 3, o.evil, "acme"), commits: []PRCommit{{AuthorLogin: "alice", CommitterLogin: "alice"}}}
	opts, _ = prOptions(t, o, h)
	opts.Workflows, opts.Workflow = nil, "fast-gate.yml"
	if res, err = RunPR(ctx, opts); err != nil || res.Run.Conclusion != "failure" {
		t.Fatalf("%v %+v", err, res)
	}
	// naming the base in TrustedBases lets it run, with the base definition
	h = &fakeHost{pr: okPR(o, 3, o.evil, "alice"), collabs: []string{"alice"}}
	opts, _ = prOptions(t, o, h)
	opts.Workflows, opts.Workflow, opts.TrustedBases = nil, "fast-gate.yml", []string{"develop"}
	if res, err = RunPR(ctx, opts); err != nil || res.Run.Conclusion != "success" {
		t.Fatalf("%v %+v", err, res)
	}
	// the owner's own pull request needs no trusted base
	h = &fakeHost{pr: okPR(o, 1, o.feature, "acme")}
	opts, _ = prOptions(t, o, h)
	opts.Workflows, opts.Workflow = nil, "fast-gate.yml"
	if res, err = RunPR(ctx, opts); err != nil || res.Run.Conclusion != "success" {
		t.Fatalf("%v %+v", err, res)
	}
}

func TestRunPRRetargetedPullRequestIsRefusedBeforeAnythingRuns(t *testing.T) {
	o := newOrigin(t)
	// the request was queued for develop, the pull request now goes into master
	pr := okPR(o, 1, o.feature, "acme")
	pr.BaseRef = "master"
	h := &fakeHost{pr: pr}
	opts, _ := prOptions(t, o, h)
	opts.Base = "develop"
	opts.Workflows = map[string]string{"develop": "fast-gate.yml", "master": "release-gate.yml"}
	res, err := RunPR(context.Background(), opts)
	if !errors.Is(err, ErrBaseMoved) || res != nil {
		t.Fatalf("%v %+v", err, res)
	}
	if !strings.Contains(err.Error(), "develop") || !strings.Contains(err.Error(), "master") {
		t.Fatalf("the message must name both branches: %v", err)
	}
	if idx, _ := opts.Store.ReadIndex(); len(idx) != 0 {
		t.Fatalf("a refused request leaves no run: %+v", idx)
	}
	if entries, _ := os.ReadDir(opts.Store.RunsPath()); len(entries) != 0 {
		t.Fatalf("a refused request creates no run folder: %v", entries)
	}
}

func TestRunPRUnchangedBaseStillRuns(t *testing.T) {
	needBash(t)
	o := newOrigin(t)
	h := &fakeHost{pr: okPR(o, 1, o.feature, "acme")}
	opts, console := prOptions(t, o, h)
	opts.Base = "develop"
	res, err := RunPR(context.Background(), opts)
	if err != nil || res.Run.Conclusion != "success" {
		t.Fatalf("%v %+v\n%s", err, res, console.String())
	}
}
