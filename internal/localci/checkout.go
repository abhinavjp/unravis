package localci

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// MirrorRefspecs are what the bare mirror fetches: every branch, every tag and the head of every pull request.
// All branches are needed, not only the base of the run: a lint step may compare with another branch
// (for example origin/master while the pull request targets develop).
var MirrorRefspecs = []string{
	"+refs/heads/*:refs/remotes/origin/*",
	"+refs/tags/*:refs/tags/*",
	"+refs/pull/*/head:refs/pull/*/head",
}

// DefaultProtected are the paths a pull request of a non-owner may not change for its own run: the files the
// runner itself reads. The caller adds project paths (for example the scripts the workflows call).
var DefaultProtected = []string{".github/workflows", ".github/local-ci.yml"}

var shaRe = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)

// Checkout does the git work of a run: the mirror, the merge ref worktree and its removal. Nothing it writes
// leaves the Store, and every transcript line goes through Redact.
type Checkout struct {
	Store  *Store
	Env    []string // process environment for git; nil means the current one
	Log    io.Writer
	Redact func(string) string
}

// git runs one git command and writes a redacted transcript to Log. It returns stdout, the exit code and an
// error (non-nil for every non-zero exit).
func (c *Checkout) git(ctx context.Context, dir string, extraEnv []string, args ...string) (string, int, error) {
	cmd := exec.CommandContext(ctx, "git", args...) //nolint:gosec // fixed binary, arguments built by this file
	cmd.Dir = dir
	env := c.Env
	if env == nil {
		env = os.Environ()
	}
	cmd.Env = append(append([]string{}, env...), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never")
	cmd.Env = append(cmd.Env, extraEnv...)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	runErr := cmd.Run()
	code := 0
	if runErr != nil {
		code = -1
		var ee *exec.ExitError
		if errors.As(runErr, &ee) {
			code = ee.ExitCode()
		}
	}
	if c.Log != nil {
		text := "$ git " + strings.Join(args, " ") + "\n" + out.String() + errOut.String()
		if c.Redact != nil {
			text = c.Redact(text)
		}
		if !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		_, _ = io.WriteString(c.Log, text)
	}
	if runErr != nil {
		msg := strings.TrimSpace(errOut.String())
		if msg == "" {
			msg = runErr.Error()
		}
		return strings.TrimSpace(out.String()), code, fmt.Errorf("git %s: %s", args[0], firstLine(msg))
	}
	return strings.TrimSpace(out.String()), 0, nil
}

// Mirror creates the bare mirror when it is missing and fetches every branch, tag and pull request head
// from originURL into it. originURL must not carry credentials (they would be stored in the mirror's config).
func (c *Checkout) Mirror(ctx context.Context, originURL string) error {
	if u, err := url.Parse(originURL); err == nil && u.User != nil {
		return errors.New("the origin URL must not contain credentials")
	}
	repo := c.Store.RepoPath()
	if _, err := os.Stat(filepath.Join(repo, "HEAD")); err != nil {
		if _, _, err := c.git(ctx, c.Store.Dir, nil, "init", "--bare", "--quiet", repo); err != nil {
			return err
		}
	}
	// A checkout like GitHub's: no line-ending conversion from the user's own git config, long Windows paths.
	for _, kv := range [][2]string{{"core.autocrlf", "false"}, {"core.longpaths", "true"}, {"gc.auto", "0"}, {"remote.origin.url", originURL}} {
		if _, _, err := c.git(ctx, repo, nil, "config", kv[0], kv[1]); err != nil {
			return err
		}
	}
	for i, spec := range MirrorRefspecs {
		flag := "--add"
		if i == 0 {
			flag = "--replace-all"
		}
		if _, _, err := c.git(ctx, repo, nil, "config", flag, "remote.origin.fetch", spec); err != nil {
			return err
		}
	}
	_, _, err := c.git(ctx, repo, nil, "fetch", "--prune", "--no-tags", "--quiet", "origin")
	return err
}

// Worktree is a checkout under work/<runid>.
type Worktree struct {
	Path    string
	Commit  string // HEAD of the worktree: the merge commit of a pull request, or the commit asked for
	BaseSHA string
	HeadSHA string
	// Conflict is true when the pull request does not merge into its base: no worktree was made and Detail
	// holds git's description of the conflict.
	Conflict bool
	Detail   string
}

// PRSpec says which pull request merge to check out.
type PRSpec struct {
	RunID   string
	BaseRef string // branch name, resolved as origin/<BaseRef> of the mirror
	HeadSHA string
	// FromBase makes the runner definition come from the base branch: the Protect paths are replaced by their
	// base content, and files the pull request added there are deleted.
	FromBase bool
	Protect  []string // project paths added to DefaultProtected
}

func (c *Checkout) rev(ctx context.Context, rev string) (string, error) {
	out, _, err := c.git(ctx, c.Store.RepoPath(), nil, "rev-parse", "--verify", "--quiet", "--end-of-options", rev+"^{commit}")
	if err != nil || !shaRe.MatchString(out) {
		return "", fmt.Errorf("cannot resolve %s in the mirror", rev)
	}
	return out, nil
}

// PullRequest builds the merge of the pull request head into the tip of its base, as GitHub's
// refs/pull/N/merge does (first parent: the base tip, second parent: the head), and checks it out detached.
// The merge is computed in the mirror with git merge-tree; nothing in a user's own repository is touched.
func (c *Checkout) PullRequest(ctx context.Context, spec PRSpec) (*Worktree, error) {
	if !shaRe.MatchString(spec.HeadSHA) {
		return nil, fmt.Errorf("%q is not a commit id", spec.HeadSHA)
	}
	if !ValidRunID(spec.RunID) {
		return nil, fmt.Errorf("%q is not a run id", spec.RunID)
	}
	if strings.TrimSpace(spec.BaseRef) == "" || strings.HasPrefix(spec.BaseRef, "-") {
		return nil, fmt.Errorf("%q is not a base branch", spec.BaseRef)
	}
	base, err := c.rev(ctx, "refs/remotes/origin/"+spec.BaseRef)
	if err != nil {
		return nil, err
	}
	head, err := c.rev(ctx, spec.HeadSHA)
	if err != nil {
		return nil, err
	}
	wt := &Worktree{BaseSHA: base, HeadSHA: head}
	repo := c.Store.RepoPath()
	out, code, err := c.git(ctx, repo, nil, "merge-tree", "--write-tree", base, head)
	switch {
	case err == nil:
	case code == 1:
		wt.Conflict, wt.Detail = true, out
		return wt, nil
	default:
		return nil, fmt.Errorf("merge of %s into %s failed: %w", head, base, err)
	}
	tree, _, _ := strings.Cut(out, "\n")
	ident := []string{"GIT_AUTHOR_NAME=local-ci", "GIT_AUTHOR_EMAIL=local-ci@localhost", "GIT_COMMITTER_NAME=local-ci", "GIT_COMMITTER_EMAIL=local-ci@localhost"}
	commit, _, err := c.git(ctx, repo, ident, "commit-tree", strings.TrimSpace(tree), "-p", base, "-p", head, "-m", "Merge "+head+" into "+base)
	if err != nil {
		return nil, err
	}
	wt.Commit = commit
	if err := c.addWorktree(ctx, spec.RunID, wt); err != nil {
		return nil, err
	}
	if spec.FromBase {
		if err := c.overlayBase(ctx, wt, append(append([]string{}, DefaultProtected...), spec.Protect...)); err != nil {
			_ = c.Remove(ctx, wt.Path)
			return nil, err
		}
	}
	return wt, nil
}

// Commit checks out one commit (the push of a branch) detached.
func (c *Checkout) Commit(ctx context.Context, runID, sha string) (*Worktree, error) {
	if !shaRe.MatchString(sha) {
		return nil, fmt.Errorf("%q is not a commit id", sha)
	}
	if !ValidRunID(runID) {
		return nil, fmt.Errorf("%q is not a run id", runID)
	}
	full, err := c.rev(ctx, sha)
	if err != nil {
		return nil, err
	}
	wt := &Worktree{Commit: full, HeadSHA: full}
	if err := c.addWorktree(ctx, runID, wt); err != nil {
		return nil, err
	}
	return wt, nil
}

func (c *Checkout) addWorktree(ctx context.Context, runID string, wt *Worktree) error {
	wt.Path = filepath.Join(c.Store.WorkPath(), runID)
	_, _, err := c.git(ctx, c.Store.RepoPath(), nil, "worktree", "add", "--detach", "--quiet", wt.Path, wt.Commit)
	return err
}

// overlayBase replaces the protected paths of the worktree by their content at the base tip.
func (c *Checkout) overlayBase(ctx context.Context, wt *Worktree, protect []string) error {
	for _, p := range protect {
		p = filepath.ToSlash(filepath.Clean(filepath.FromSlash(strings.TrimSpace(p))))
		if p == "." || p == ".git" || strings.HasPrefix(p, ".git/") || !filepath.IsLocal(filepath.FromSlash(p)) {
			return fmt.Errorf("protected path %q is not a folder or file inside the repository", p)
		}
		if err := os.RemoveAll(filepath.Join(wt.Path, filepath.FromSlash(p))); err != nil {
			return err
		}
		if _, _, err := c.git(ctx, c.Store.RepoPath(), nil, "cat-file", "-e", wt.BaseSHA+":"+p); err != nil {
			continue // the base has nothing there: the path stays empty
		}
		if _, _, err := c.git(ctx, wt.Path, nil, "checkout", wt.BaseSHA, "--", p); err != nil {
			return err
		}
	}
	return nil
}

// Remove deletes a worktree and its bookkeeping in the mirror.
func (c *Checkout) Remove(ctx context.Context, path string) error {
	if path == "" || filepath.Clean(path) == filepath.Clean(c.Store.WorkPath()) || !pathInside(c.Store.WorkPath(), path) {
		return fmt.Errorf("%s is not a worktree of this store", path)
	}
	_, _, gerr := c.git(ctx, c.Store.RepoPath(), nil, "worktree", "remove", "--force", path)
	if _, err := os.Stat(path); err == nil {
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("cannot remove %s: %w", path, err)
		}
	}
	_, _, _ = c.git(ctx, c.Store.RepoPath(), nil, "worktree", "prune")
	if _, err := os.Stat(path); err == nil {
		return gerr
	}
	return nil
}
