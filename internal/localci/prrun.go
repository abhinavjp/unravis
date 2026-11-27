package localci

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// PROptions configures RunPR. Nothing here is project specific: the repository, the workflow of each base
// branch and the extra protected paths are the caller's.
type PROptions struct {
	Store  *Store
	Host   Host
	Repo   string // "owner/name"
	Number int
	// ExpectHead, when set, is the head commit the run was asked for. A pull request whose head has moved on is
	// not run: RunPR returns ErrHeadMoved, because a request for the newer head is (or soon will be) queued.
	ExpectHead string
	// Base, when set, is the base branch the run was asked for. A pull request that was retargeted since is not
	// run: RunPR returns ErrBaseMoved, because the workflow and the trust of the other branch were never chosen
	// for it (the poller queues a new request for the new base). Empty (a hand run) accepts any base.
	Base string
	// OriginURL is where the mirror fetches from; empty means Host.CloneURL(Repo).
	OriginURL string
	// Workflows maps a base branch to the workflow file (a name inside .github/workflows) that gates it.
	Workflows map[string]string
	// Workflow, when set, is used whatever the base branch is.
	Workflow string
	// Protect are paths (besides DefaultProtected) read from the base branch when the author is not the owner.
	Protect []string
	// TrustedBases are the base branches whose content may supply the runner definition of a pull request that
	// is not all the owner's. A pull request of anybody else into another base is refused, whatever Workflow
	// says. When empty, the keys of Workflows are the trusted bases.
	TrustedBases []string
	// TrustOwnerBranch skips the per-commit author check (CommitsByOwner) for a pull request the owner opened
	// from a branch in the same repository. It is for a run the owner starts by hand at their own console (the
	// agent never sets it): commits made by tools often carry no GitHub login, and without it the pull request
	// would run from the base branch's runner definition. It does NOT skip the check of who pushed to the
	// branch (HeadPushedByOwner), which always runs.
	TrustOwnerBranch bool
	// Keep leaves the worktree in place after the run.
	Keep bool
	// Env is the environment the caller allows steps to see (already an allow-list; nil means the current one).
	// The runner replaces its profile, git and cache variables (see Store.StepEnv); the runner's own fetch
	// keeps the git setup in it.
	Env   []string
	Vars  map[string]string
	Slots int
	// Redact masks every text the store writes (logs, run.json, index.jsonl, summary.md).
	Redact func(string) string
	Log    io.Writer // progress for the person at the console
	Now    func() time.Time
	// Progress is told how the jobs of the run go (see RunOptions.Progress).
	Progress func(RunEvent)
}

// PRResult is what RunPR returns once a run folder exists.
type PRResult struct {
	RunID   string
	RunDir  string
	Workdir string // the worktree, still on disk only when Keep was set
	Run     *RunResult
	Entry   IndexEntry
}

func (o *PROptions) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

func (o *PROptions) mask(s string) string {
	if o.Redact != nil {
		return o.Redact(s)
	}
	return s
}

func (o *PROptions) logf(format string, a ...any) {
	if o.Log != nil {
		_, _ = fmt.Fprintln(o.Log, o.mask(fmt.Sprintf(format, a...)))
	}
}

// workflowFor picks the workflow file of the pull request.
func (o *PROptions) workflowFor(base string) (string, error) {
	f := o.Workflow
	if f == "" {
		f = o.Workflows[base]
	}
	if f == "" {
		return "", fmt.Errorf("no workflow is set for pull requests into %q (give a workflow for that base branch)", base)
	}
	if f != filepath.Base(f) || strings.ContainsAny(f, `/\`) || f == "." || f == ".." {
		return "", fmt.Errorf("workflow %q must be a file name inside .github/workflows", f)
	}
	return f, nil
}

func (o *PROptions) trustedBases() []string {
	if len(o.TrustedBases) > 0 {
		return o.TrustedBases
	}
	bases := make([]string, 0, len(o.Workflows))
	for b := range o.Workflows {
		bases = append(bases, b)
	}
	return bases
}

// ErrHeadMoved is returned by RunPR when ExpectHead is no longer the head of the pull request.
var ErrHeadMoved = errors.New("the pull request head moved on")

// ErrBaseMoved is returned by RunPR when Base is no longer the base branch of the pull request.
var ErrBaseMoved = errors.New("the pull request was pointed at another base branch")

// ErrPRNotOpen is returned by RunPR for a pull request that is closed or merged.
var ErrPRNotOpen = errors.New("the pull request is not open")

// failedRun is the record of a run that stopped before any job ran. The pseudo job names the stage; its key
// starts with "_", which a real job id cannot, and Pseudo is set.
func failedRun(workflow, stage, reason, logFile string, start, end time.Time) *RunResult {
	jr := &JobResult{Key: stage, ID: stage, Label: stage, Pseudo: true, Status: "failure", Reason: reason, Start: start, End: end}
	if logFile != "" {
		jr.Steps = []StepResult{{Index: 1, Name: stage, Outcome: "failure", Conclusion: "failure", Start: start, End: end, LogFile: logFile, Err: reason}}
	}
	return &RunResult{Workflow: workflow, Conclusion: "failure", Jobs: []*JobResult{jr}, Start: start, End: end}
}

// RunPR runs the workflow that gates a pull request on its merge with the base branch:
//
//  1. the host says who wrote it and where its head lives, and Trusted decides whether it may run at all;
//  2. the mirror is fetched, the merge of head into base is computed locally and checked out in a worktree
//     (a merge conflict is a failed run, nothing runs);
//  3. for an author who is not the owner, the workflow files and the protected paths come from the base branch;
//  4. the workflow of the base branch runs in the worktree with the store's persistent caches;
//  5. run.json, logs and summary.md are in runs/<id>/, one line is appended to runs/index.jsonl, and the
//     worktree is removed (unless Keep).
//
// The error is for problems that stop a run (the host, git, an unsupported workflow); once the run folder
// exists the same problem is also recorded: the result's Run has Conclusion "error" and the index has a line.
// A refused or conflicting pull request is not an error: Conclusion is "failure" with the reason.
func RunPR(ctx context.Context, o PROptions) (res *PRResult, err error) {
	if o.Store == nil || o.Host == nil {
		return nil, errors.New("run-pr: no store or host")
	}
	owner, _, ok := splitRepo(o.Repo)
	if !ok {
		return nil, fmt.Errorf("run-pr: repository %q is not owner/name", o.Repo)
	}
	pr, err := o.Host.PullRequest(ctx, o.Repo, o.Number)
	if err != nil {
		return nil, err
	}
	if pr.State != "" && !strings.EqualFold(pr.State, "OPEN") {
		return nil, fmt.Errorf("run-pr: pull request %d is %s, only open ones run: %w", o.Number, strings.ToLower(pr.State), ErrPRNotOpen)
	}
	if o.ExpectHead != "" && !strings.EqualFold(pr.HeadSHA, o.ExpectHead) {
		return nil, fmt.Errorf("run-pr: pull request %d moved from %s to %s: %w", o.Number, shortSHA(o.ExpectHead), shortSHA(pr.HeadSHA), ErrHeadMoved)
	}
	if o.Base != "" && pr.BaseRef != o.Base {
		return nil, fmt.Errorf("run-pr: pull request %d was asked for into %q but now goes into %q: %w", o.Number, o.Base, pr.BaseRef, ErrBaseMoved)
	}
	file, err := o.workflowFor(pr.BaseRef)
	if err != nil {
		return nil, err
	}
	if o.Env == nil {
		o.Env = os.Environ()
	}
	start := o.now()
	runID, runDir, err := o.Store.NewRun(start, fmt.Sprintf("pr%d-%s", pr.Number, strings.TrimSuffix(strings.TrimSuffix(file, ".yml"), ".yaml")))
	if err != nil {
		return nil, err
	}
	res = &PRResult{RunID: runID, RunDir: runDir}
	entry := IndexEntry{RunID: runID, Workflow: file, Event: "pull_request", Repo: o.Repo, PR: pr.Number, Author: pr.Author,
		BaseRef: pr.BaseRef, HeadRef: pr.HeadRef, HeadSHA: pr.HeadSHA, Start: start}
	clog, cerr := os.Create(filepath.Join(runDir, "checkout.log")) //nolint:gosec // path inside the store
	if cerr != nil {
		return nil, cerr
	}
	defer func() { _ = clog.Close() }()
	note := func(format string, a ...any) { _, _ = fmt.Fprintln(clog, o.mask(fmt.Sprintf(format, a...))) }
	logPath := clog.Name()

	// finish records the run: run.json, the index line and the console line. A non-nil hard error makes it "error".
	finish := func(run *RunResult, commit, reason string, hard error) (*PRResult, error) {
		end := o.now()
		if hard != nil {
			run = failedRun(file, CheckoutLogName, hard.Error(), logPath, start, end)
			run.Conclusion = "error"
			reason = hard.Error()
		}
		for _, j := range run.Jobs {
			if IsPseudoJob(j) {
				j.Reason = o.mask(j.Reason)
				for i := range j.Steps {
					j.Steps[i].Err = o.mask(j.Steps[i].Err)
				}
			}
		}
		if werr := WriteRunRecord(runDir, run); werr != nil && hard == nil {
			hard = werr
		}
		entry.CommitSHA, entry.Conclusion, entry.Reason, entry.End = commit, run.Conclusion, reason, end
		if aerr := o.Store.AppendIndex(entry, o.Redact); aerr != nil && hard == nil {
			hard = aerr
		}
		res.Run, res.Entry = run, entry.masked(o.Redact)
		o.logf("run %s: %s%s", runID, run.Conclusion, map[bool]string{true: " (" + reason + ")"}[reason != ""])
		return res, hard
	}

	// 1. trust
	var collaborators []string
	headOwner := pr.HeadRepoOwner
	if pr.IsFork {
		headOwner = ""
	}
	if !pr.IsFork && !strings.EqualFold(pr.Author, owner) && strings.EqualFold(headOwner, owner) {
		if collaborators, err = o.Host.Collaborators(ctx, o.Repo); err != nil {
			note("cannot read the collaborators of %s: %v", o.Repo, err)
			return finish(nil, "", "", fmt.Errorf("cannot read the collaborators of %s: %w", o.Repo, err))
		}
	}
	trust := Trusted(pr.Author, headOwner, collaborators, o.Repo)
	note("pull request %d of %s: author %s, %s -> %s, head %s", pr.Number, o.Repo, pr.Author, pr.HeadRef, pr.BaseRef, pr.HeadSHA)
	if !trust.Run {
		note("not run: %s", trust.Reason)
		return finish(failedRun(file, TrustJobKey, trust.Reason, logPath, start, o.now()), "", trust.Reason, nil)
	}
	if !trust.FromBase && !o.TrustOwnerBranch {
		// The owner opened it, but a commit on the branch may be somebody else's (a collaborator pushed to the
		// owner's branch). Only a branch written start to end by the owner gets the owner's trust.
		commits, cerr := o.Host.Commits(ctx, o.Repo, pr.Number)
		if cerr != nil {
			note("cannot read the commits of pull request %d: %v", pr.Number, cerr)
			return finish(nil, "", "", fmt.Errorf("cannot read the commits of pull request %d: %w", pr.Number, cerr))
		}
		if !CommitsByOwner(owner, commits) {
			trust.FromBase = true
			note("not every commit of the branch was written and committed by %s: treated like a collaborator's pull request", owner)
		}
	}
	if !trust.FromBase {
		// The logins in a commit are text, and TrustOwnerBranch only waives that check. Whatever the flag, the
		// owner's trust needs the host's own record to show that nobody but the owner ever pushed to the branch;
		// any doubt, a failed lookup included, is the untrusted path.
		if ok, why := HeadPushedByOwner(ctx, o.Host, o.Repo, owner, pr); !ok {
			trust.FromBase = true
			note("%s: treated like a collaborator's pull request", why)
		}
	}
	if trust.FromBase && !TrustedBase(pr.BaseRef, o.trustedBases()) {
		reason := fmt.Sprintf("the code is not all the owner's and %q is not a base branch this PC trusts for that", pr.BaseRef)
		note("not run: %s", reason)
		return finish(failedRun(file, TrustJobKey, reason, logPath, start, o.now()), "", reason, nil)
	}
	note("trusted (workflow files from %s)", map[bool]string{true: "the base branch", false: "the pull request"}[trust.FromBase])

	// 2. mirror and merge worktree
	co := &Checkout{Store: o.Store, Env: o.Env, Log: clog, Redact: o.Redact}
	origin := o.OriginURL
	if origin == "" {
		origin = o.Host.CloneURL(o.Repo)
	}
	o.logf("fetching %s", o.Repo)
	if err := co.Mirror(ctx, origin); err != nil {
		return finish(nil, "", "", fmt.Errorf("fetch: %w", err))
	}
	wt, err := co.PullRequest(ctx, PRSpec{RunID: runID, BaseRef: pr.BaseRef, HeadSHA: pr.HeadSHA, FromBase: trust.FromBase, Protect: o.Protect})
	if err != nil {
		return finish(nil, "", "", fmt.Errorf("checkout: %w", err))
	}
	if wt.Conflict {
		note("merge conflict:\n%s", wt.Detail)
		return finish(failedRun(file, CheckoutLogName, "merge conflict", logPath, start, o.now()), "", "merge conflict", nil)
	}
	res.Workdir = wt.Path
	defer func() {
		_ = os.RemoveAll(filepath.Join(runDir, "tmp")) // scripts of the steps; the logs are what stays
		if o.Keep {
			o.logf("worktree kept: %s", wt.Path)
			return
		}
		if rerr := co.Remove(context.WithoutCancel(ctx), wt.Path); rerr != nil {
			o.logf("warning: could not remove the worktree: %v", rerr)
		}
	}()

	// 3. the workflow of this run
	wfPath := filepath.Join(wt.Path, ".github", "workflows", file)
	data, err := os.ReadFile(wfPath) //nolint:gosec // path inside the worktree, file name checked by workflowFor
	if err != nil {
		return finish(nil, wt.Commit, "", fmt.Errorf("workflow %s is not in %s: %w", file, wt.Commit, err))
	}
	wf, uns, perr := ParseWorkflow(".github/workflows/"+file, data)
	if perr != nil {
		return finish(nil, wt.Commit, "", perr)
	}
	if len(uns) > 0 {
		msgs := make([]string, len(uns))
		for i, u := range uns {
			msgs[i] = u.Error()
		}
		return finish(nil, wt.Commit, "", fmt.Errorf("unsupported: %s has items this runner does not support; nothing was run: %s", file, strings.Join(msgs, "; ")))
	}
	// The workflow's own on: is NOT a gate here (BL-586d, "Blocker R" in docs/ci/CUTOVER.md). The base-branch map
	// (Workflows / --workflow-for, or the operator's --workflow), which workflowFor read above, already decides
	// which workflow serves which base; a base with no entry never reaches this point. So a gate workflow that
	// GitHub no longer starts by itself (on: workflow_dispatch only) runs exactly like one with pull_request.
	// The github context stays that of a pull request (event_name pull_request, see prGitHubContext): the steps
	// are written for the merge commit of a pull request, and the manual-run-only steps (event_name ==
	// 'workflow_dispatch') must stay skipped because RunPR has already built that merge commit.
	// Every other trust check above and in the checkout (fork and stranger refusal, FromBase files, protected
	// paths, the environment allow-list) is unchanged.
	var cfgData []byte
	if p := filepath.Join(wt.Path, ".github", "local-ci.yml"); fileIsRegular(p) {
		if cfgData, err = os.ReadFile(p); err != nil { //nolint:gosec // path inside the worktree
			return finish(nil, wt.Commit, "", err)
		}
	}
	cfg, err := ParseLocalConfig(cfgData)
	if err != nil {
		return finish(nil, wt.Commit, "", fmt.Errorf("local-ci.yml: %w", err))
	}
	env, cleanupHome, err := o.Store.StepEnv(o.Env, runID, trust.FromBase)
	if err != nil {
		return finish(nil, wt.Commit, "", err)
	}
	defer cleanupHome()

	// 4. run
	o.logf("running %s on the merge of %s into %s (%s)", file, shortSHA(wt.HeadSHA), pr.BaseRef, shortSHA(wt.Commit))
	run, err := Run(ctx, RunOptions{
		Workflow: wf, Config: cfg, Workdir: wt.Path, StateDir: runDir, LockDir: filepath.Join(o.Store.Dir, "locks"), GitHub: prGitHubContext(o.Repo, pr, wt, wf.Name, start),
		Vars: o.Vars, Env: env, Slots: o.Slots, Log: o.Log, Redact: o.Redact, Now: o.Now, Progress: o.Progress,
		JobTree: func(c context.Context, name string) (string, func(), error) {
			jt, err := co.JobTree(c, wt, name, trust.FromBase, o.Protect)
			if err != nil {
				return "", nil, err
			}
			return jt.Path, func() { _ = co.Remove(context.WithoutCancel(c), jt.Path) }, nil
		},
	})
	if err != nil {
		return finish(nil, wt.Commit, "", err)
	}
	reason := ""
	if run.Conclusion != "success" {
		reason = failureReason(run)
	}
	return finish(run, wt.Commit, reason, nil)
}

func fileIsRegular(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

func shortSHA(s string) string {
	if len(s) > 10 {
		return s[:10]
	}
	return s
}

// failureReason names the first failed job and step of a run, the text a commit status would carry.
func failureReason(run *RunResult) string {
	for _, j := range run.Jobs {
		if j.Status != "failure" {
			continue
		}
		for _, s := range j.Steps {
			if s.Conclusion == "failure" {
				return fmt.Sprintf("job %s, step %d %s", j.Key, s.Index, s.Name)
			}
		}
		if j.Reason != "" {
			return fmt.Sprintf("job %s: %s", j.Key, j.Reason)
		}
		return "job " + j.Key
	}
	return ""
}

// prGitHubContext is the github context of a pull_request run on the merge commit, as Actions fills it.
func prGitHubContext(repo string, pr PullRequest, wt *Worktree, workflow string, now time.Time) map[string]any {
	n := strconv.Itoa(pr.Number)
	return map[string]any{
		"event_name": "pull_request", "ref": "refs/pull/" + n + "/merge", "ref_name": n + "/merge", "sha": wt.Commit,
		"base_ref": pr.BaseRef, "head_ref": pr.HeadRef, "repository": repo, "workspace": wt.Path,
		"run_id": strconv.FormatInt(now.Unix(), 10), "run_number": "1", "run_attempt": "1", "workflow": workflow,
		"actor": pr.Author, "token": "",
		"event": map[string]any{
			"number": pr.Number,
			"pull_request": map[string]any{
				"number": pr.Number,
				"base":   map[string]any{"ref": pr.BaseRef, "sha": wt.BaseSHA},
				"head":   map[string]any{"ref": pr.HeadRef, "sha": wt.HeadSHA},
				"user":   map[string]any{"login": pr.Author},
			},
		},
	}
}
