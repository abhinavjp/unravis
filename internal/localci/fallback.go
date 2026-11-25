package localci

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Dispatcher starts a workflow on the host with workflow_dispatch.
type Dispatcher interface {
	Dispatch(ctx context.Context, repo, workflow, ref string) error
}

// GHDispatcher runs `gh workflow run <workflow> --repo <repo> --ref <ref>`.
type GHDispatcher struct {
	Env  []string
	exec func(ctx context.Context, env []string, args ...string) ([]byte, error) // test hook
}

// Command is the command line Dispatch runs, for printing.
func (g *GHDispatcher) Command(repo, workflow, ref string) string {
	return fmt.Sprintf("gh workflow run %s --repo %s --ref %s", workflow, repo, ref)
}

// Dispatch implements Dispatcher.
func (g *GHDispatcher) Dispatch(ctx context.Context, repo, workflow, ref string) error {
	run := g.exec
	if run == nil {
		run = ghExec
	}
	_, err := run(ctx, g.Env, "workflow", "run", workflow, "--repo", repo, "--ref", ref)
	return err
}

// FakeDispatcher records what it was asked to start (tests, dry runs).
type FakeDispatcher struct {
	Err   error
	Calls []string
}

// Dispatch implements Dispatcher.
func (f *FakeDispatcher) Dispatch(_ context.Context, repo, workflow, ref string) error {
	f.Calls = append(f.Calls, repo+" "+workflow+" "+ref)
	return f.Err
}

// FallbackOptions configures Fallback.
type FallbackOptions struct {
	Repo   string
	Number int
	Host   Host
	Naming StatusNaming
	// Dispatcher starts the workflow; DryRun checks everything and starts nothing.
	Dispatcher Dispatcher
	DryRun     bool
	// Command, when set, is shown in the result in place of the default (a real dispatcher knows its own).
	Command func(repo, workflow, ref string) string
}

// FallbackResult says what Fallback did.
type FallbackResult struct {
	Repo     string `json:"repo"`
	Number   int    `json:"number"`
	Base     string `json:"base"`
	Branch   string `json:"branch"`
	SHA      string `json:"sha"`
	Workflow string `json:"workflow"`
	Command  string `json:"command"`
	Started  bool   `json:"started"`
}

// Fallback hands one pull request back to GitHub: it starts the workflow that gates the pull request's base branch
// on the head branch with workflow_dispatch, for the days the local gate is not to be trusted (phase 2 of the
// cutover keeps this as the way out). It starts nothing when it cannot be sure the run will happen: the pull
// request must be open and from a branch of the same repository, a gate must be named for its base, and the
// workflow at the head must be one that accepts workflow_dispatch.
func Fallback(ctx context.Context, o FallbackOptions) (*FallbackResult, error) {
	if o.Host == nil || o.Dispatcher == nil {
		return nil, errors.New("fallback: no host or dispatcher")
	}
	if _, _, ok := splitRepo(o.Repo); !ok {
		return nil, fmt.Errorf("fallback: repository %q is not owner/name", o.Repo)
	}
	pr, err := o.Host.PullRequest(ctx, o.Repo, o.Number)
	if err != nil {
		return nil, fmt.Errorf("fallback: %w", err)
	}
	if pr.State != "OPEN" {
		return nil, fmt.Errorf("fallback: pull request %d is %s, not open", o.Number, strings.ToLower(pr.State))
	}
	if pr.IsFork {
		return nil, fmt.Errorf("fallback: pull request %d comes from a fork; workflow_dispatch can only run on a branch of %s", o.Number, o.Repo)
	}
	if pr.HeadRef == "" || strings.HasPrefix(pr.HeadRef, "-") {
		return nil, fmt.Errorf("fallback: %q is not a branch", pr.HeadRef)
	}
	var workflow string
	for _, g := range o.Naming.Gates {
		if g.Kind == KindPR && g.Branch == pr.BaseRef && g.Workflow != "" {
			workflow = g.Workflow
			break
		}
	}
	if workflow == "" {
		return nil, fmt.Errorf("fallback: no workflow is named for pull requests into %s", pr.BaseRef)
	}
	data, err := o.Host.FileAt(ctx, o.Repo, pr.HeadSHA, ".github/workflows/"+workflow)
	if err != nil {
		return nil, fmt.Errorf("fallback: cannot read %s at %s: %w", workflow, shortSHA(pr.HeadSHA), err)
	}
	wf, _, perr := ParseWorkflow(".github/workflows/"+workflow, data)
	if perr != nil {
		return nil, fmt.Errorf("fallback: %w", perr)
	}
	if _, ok := wf.On["workflow_dispatch"]; !ok {
		return nil, fmt.Errorf("fallback: %s on %s has no workflow_dispatch trigger, so GitHub cannot be asked to run it by hand; add `workflow_dispatch:` to its on: block first", workflow, pr.HeadRef)
	}
	res := &FallbackResult{Repo: o.Repo, Number: pr.Number, Base: pr.BaseRef, Branch: pr.HeadRef, SHA: pr.HeadSHA, Workflow: workflow}
	res.Command = fmt.Sprintf("gh workflow run %s --repo %s --ref %s", workflow, o.Repo, pr.HeadRef)
	if o.Command != nil {
		res.Command = o.Command(o.Repo, workflow, pr.HeadRef)
	}
	if o.DryRun {
		return res, nil
	}
	if err := o.Dispatcher.Dispatch(ctx, o.Repo, workflow, pr.HeadRef); err != nil {
		return res, fmt.Errorf("fallback: %w", err)
	}
	res.Started = true
	return res, nil
}
