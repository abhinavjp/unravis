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

// RefOptions configures RunRef: the run of one workflow on one commit of a branch, for a push or a schedule.
type RefOptions struct {
	Store *Store
	Host  Host
	Repo  string // "owner/name"
	Ref   string // the branch
	SHA   string // the commit to run
	// Workflow is a file name inside .github/workflows.
	Workflow string
	// Event is push or schedule.
	Event string
	// Cron is the schedule entry that fired, for the github context of a schedule run.
	Cron      string
	OriginURL string
	Keep      bool
	Env       []string
	Vars      map[string]string
	Slots     int
	Redact    func(string) string
	Log       io.Writer
	Now       func() time.Time
}

func (o *RefOptions) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

func (o *RefOptions) mask(s string) string {
	if o.Redact != nil {
		return o.Redact(s)
	}
	return s
}

func (o *RefOptions) logf(format string, a ...any) {
	if o.Log != nil {
		_, _ = fmt.Fprintln(o.Log, o.mask(fmt.Sprintf(format, a...)))
	}
}

// RunRef runs the workflow on the exact commit (a detached checkout of the mirror) and records it like RunPR does:
// run.json, logs, summary.md, one line in runs/index.jsonl (event push or schedule, head_ref the branch, head_sha
// the commit). A push or schedule run trusts the repository's own commit, so there is no trust check: only the
// owner's branches are watched. The error is for problems that stop a run; once the run folder exists the same
// problem is also recorded with the conclusion "error".
func RunRef(ctx context.Context, o RefOptions) (res *PRResult, err error) {
	if o.Store == nil || (o.Host == nil && o.OriginURL == "") {
		return nil, errors.New("run-ref: no store or host")
	}
	if _, _, ok := splitRepo(o.Repo); !ok {
		return nil, fmt.Errorf("run-ref: repository %q is not owner/name", o.Repo)
	}
	if o.Event != "push" && o.Event != "schedule" {
		return nil, fmt.Errorf("run-ref: event %q is not push or schedule", o.Event)
	}
	if strings.TrimSpace(o.Ref) == "" || strings.HasPrefix(o.Ref, "-") {
		return nil, fmt.Errorf("run-ref: %q is not a branch", o.Ref)
	}
	if !shaRe.MatchString(o.SHA) {
		return nil, fmt.Errorf("run-ref: %q is not a commit id", o.SHA)
	}
	file := o.Workflow
	if file == "" || file != filepath.Base(file) || strings.ContainsAny(file, `/\`) || file == "." || file == ".." {
		return nil, fmt.Errorf("run-ref: workflow %q must be a file name inside .github/workflows", file)
	}
	if o.Env == nil {
		o.Env = os.Environ()
	}
	start := o.now()
	label := o.Event + "-" + slug(o.Ref) + "-" + strings.TrimSuffix(strings.TrimSuffix(file, ".yml"), ".yaml")
	runID, runDir, err := o.Store.NewRun(start, label)
	if err != nil {
		return nil, err
	}
	res = &PRResult{RunID: runID, RunDir: runDir}
	entry := IndexEntry{RunID: runID, Workflow: file, Event: o.Event, Repo: o.Repo, HeadRef: o.Ref, HeadSHA: o.SHA, Start: start}
	clog, cerr := os.Create(filepath.Join(runDir, "checkout.log")) //nolint:gosec // path inside the store
	if cerr != nil {
		return nil, cerr
	}
	defer func() { _ = clog.Close() }()
	note := func(format string, a ...any) { _, _ = fmt.Fprintln(clog, o.mask(fmt.Sprintf(format, a...))) }
	logPath := clog.Name()

	finish := func(run *RunResult, reason string, hard error) (*PRResult, error) {
		end := o.now()
		if hard != nil {
			run = failedRun(file, "checkout", hard.Error(), logPath, start, end)
			run.Conclusion = "error"
			reason = hard.Error()
		}
		for _, j := range run.Jobs {
			if j.Key == "checkout" {
				j.Reason = o.mask(j.Reason)
				for i := range j.Steps {
					j.Steps[i].Err = o.mask(j.Steps[i].Err)
				}
			}
		}
		if werr := WriteRunRecord(runDir, run); werr != nil && hard == nil {
			hard = werr
		}
		entry.CommitSHA, entry.Conclusion, entry.Reason, entry.End = o.SHA, run.Conclusion, reason, end
		if aerr := o.Store.AppendIndex(entry, o.Redact); aerr != nil && hard == nil {
			hard = aerr
		}
		res.Run, res.Entry = run, entry.masked(o.Redact)
		o.logf("run %s: %s%s", runID, run.Conclusion, map[bool]string{true: " (" + reason + ")"}[reason != ""])
		return res, hard
	}

	note("%s of %s on %s at %s", o.Event, o.Repo, o.Ref, o.SHA)
	co := &Checkout{Store: o.Store, Env: o.Env, Log: clog, Redact: o.Redact}
	origin := o.OriginURL
	if origin == "" {
		origin = o.Host.CloneURL(o.Repo)
	}
	o.logf("fetching %s", o.Repo)
	if err := co.Mirror(ctx, origin); err != nil {
		return finish(nil, "", fmt.Errorf("fetch: %w", err))
	}
	wt, err := co.Commit(ctx, runID, o.SHA)
	if err != nil {
		return finish(nil, "", fmt.Errorf("checkout: %w", err))
	}
	res.Workdir = wt.Path
	defer func() {
		_ = os.RemoveAll(filepath.Join(runDir, "tmp"))
		if o.Keep {
			o.logf("worktree kept: %s", wt.Path)
			return
		}
		if rerr := co.Remove(context.WithoutCancel(ctx), wt.Path); rerr != nil {
			o.logf("warning: could not remove the worktree: %v", rerr)
		}
	}()

	wfPath := filepath.Join(wt.Path, ".github", "workflows", file)
	data, err := os.ReadFile(wfPath) //nolint:gosec // path inside the worktree, file name checked above
	if err != nil {
		return finish(nil, "", fmt.Errorf("workflow %s is not in %s: %w", file, wt.Commit, err))
	}
	wf, uns, perr := ParseWorkflow(".github/workflows/"+file, data)
	if perr != nil {
		return finish(nil, "", perr)
	}
	if len(uns) > 0 {
		msgs := make([]string, len(uns))
		for i, u := range uns {
			msgs[i] = u.Error()
		}
		return finish(nil, "", fmt.Errorf("unsupported: %s has items this runner does not support; nothing was run: %s", file, strings.Join(msgs, "; ")))
	}
	if len(wf.On) > 0 {
		if _, ok := wf.On[o.Event]; !ok {
			return finish(nil, "", fmt.Errorf("%s does not run on %s", file, o.Event))
		}
	}
	var cfgData []byte
	if p := filepath.Join(wt.Path, ".github", "local-ci.yml"); fileIsRegular(p) {
		if cfgData, err = os.ReadFile(p); err != nil { //nolint:gosec // path inside the worktree
			return finish(nil, "", err)
		}
	}
	cfg, err := ParseLocalConfig(cfgData)
	if err != nil {
		return finish(nil, "", fmt.Errorf("local-ci.yml: %w", err))
	}
	env, err := o.Store.CacheEnv(o.Env)
	if err != nil {
		return finish(nil, "", err)
	}
	o.logf("running %s (%s) on %s at %s", file, o.Event, o.Ref, shortSHA(wt.Commit))
	run, err := Run(ctx, RunOptions{
		Workflow: wf, Config: cfg, Workdir: wt.Path, StateDir: runDir, GitHub: refGitHubContext(o, wf.Name, wt, start),
		Vars: o.Vars, Env: env, Slots: o.Slots, Log: o.Log, Redact: o.Redact, Now: o.Now,
	})
	if err != nil {
		return finish(nil, "", err)
	}
	reason := ""
	if run.Conclusion != "success" {
		reason = failureReason(run)
	}
	return finish(run, reason, nil)
}

// refGitHubContext is the github context of a push or schedule run on a branch commit, as Actions fills it.
func refGitHubContext(o RefOptions, workflow string, wt *Worktree, now time.Time) map[string]any {
	ev := map[string]any{}
	if o.Event == "push" {
		ev["ref"] = "refs/heads/" + o.Ref
		ev["after"] = wt.Commit
	} else {
		ev["schedule"] = o.Cron
	}
	return map[string]any{
		"event_name": o.Event, "ref": "refs/heads/" + o.Ref, "ref_name": o.Ref, "sha": wt.Commit,
		"base_ref": "", "head_ref": "", "repository": o.Repo, "workspace": wt.Path,
		"run_id": strconv.FormatInt(now.Unix(), 10), "run_number": "1", "run_attempt": "1", "workflow": workflow,
		"actor": "local-ci", "token": "", "event": ev,
	}
}
