package localci

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
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
//
// The mirror is shared by every run, and a step of a run can write into it (a worktree reaches the mirror's
// config and hooks through its commondir). So the runner never trusts what is in the mirror folder: before
// every fetch it rewrites repo.git/config from a fixed template and empties the hooks, and every git command
// it runs ignores the user's own and the system git config, hooks, fsmonitor, replace refs and symlinks.
type Checkout struct {
	Store *Store
	// Env is the process environment of the runner's own git; nil means the current one. Only the fetch keeps
	// the user's git configuration from it (a credential helper the repository may need); every other
	// command runs with a profile of its own (Store.GitHomePath) and an empty global config.
	Env    []string
	Log    io.Writer
	Redact func(string) string
}

// noHooks is core.hooksPath for every runner git command: the null device, below which no folder exists (or can
// be made), so no hook can be found whatever a step wrote.
const noHooks = os.DevNull

// gitGuards are the options before every runner git command.
func (c *Checkout) gitGuards() []string {
	return []string{
		"--no-replace-objects",
		"-c", "core.hooksPath=" + filepath.ToSlash(noHooks),
		"-c", "core.fsmonitor=false",
		"-c", "core.symlinks=false",
	}
}

func (c *Checkout) baseEnv() []string {
	if c.Env != nil {
		return c.Env
	}
	return os.Environ()
}

// sealedEnv is the environment of a runner git command that needs no credentials: the base environment
// without its profile and git settings, with the runner's own empty profile in their place.
func (c *Checkout) sealedEnv() []string {
	home := c.Store.GitHomePath()
	drop := map[string]bool{}
	for _, n := range []string{"HOME", "USERPROFILE", "HOMEDRIVE", "HOMEPATH", "APPDATA", "LOCALAPPDATA", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME", "SSH_AUTH_SOCK", "SSH_ASKPASS"} {
		drop[n] = true
	}
	var env []string
	for _, kv := range c.baseEnv() {
		k, _, ok := splitKV(kv)
		if !ok {
			continue
		}
		u := strings.ToUpper(k)
		if drop[u] || strings.HasPrefix(u, "GIT_") || strings.HasPrefix(u, "GCM_") {
			continue
		}
		env = append(env, kv)
	}
	return append(env, "HOME="+home, "USERPROFILE="+home,
		"GIT_CONFIG_GLOBAL="+filepath.Join(home, "gitconfig"), "GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=credential.helper", "GIT_CONFIG_VALUE_0=",
		"GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=", "GCM_INTERACTIVE=never")
}

// fetchEnv keeps the user's git configuration (the credential helper) for the one command that talks to the
// remote.
func (c *Checkout) fetchEnv() []string {
	return append(append([]string{}, c.baseEnv()...), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never")
}

// ensureGitHome makes the runner's own profile: an empty global git config.
func (c *Checkout) ensureGitHome() error {
	if err := os.MkdirAll(c.Store.GitHomePath(), 0o750); err != nil {
		return err
	}
	cfg := filepath.Join(c.Store.GitHomePath(), "gitconfig")
	if st, err := os.Stat(cfg); err != nil || st.Size() != 0 {
		return os.WriteFile(cfg, nil, 0o600)
	}
	return nil
}

// git runs one git command with no credentials and writes a redacted transcript to Log. It returns stdout,
// the exit code and an error (non-nil for every non-zero exit).
func (c *Checkout) git(ctx context.Context, dir string, extraEnv []string, args ...string) (string, int, error) {
	if err := c.ensureGitHome(); err != nil {
		return "", -1, err
	}
	return c.run(ctx, dir, c.sealedEnv(), extraEnv, args)
}

// gitFetch runs the one command that needs the user's credential helper.
func (c *Checkout) gitFetch(ctx context.Context, dir string, args ...string) (string, int, error) {
	if err := c.ensureGitHome(); err != nil {
		return "", -1, err
	}
	return c.run(ctx, dir, c.fetchEnv(), nil, args)
}

func (c *Checkout) run(ctx context.Context, dir string, env, extraEnv, args []string) (string, int, error) {
	full := append(c.gitGuards(), args...)
	cmd := exec.CommandContext(ctx, "git", full...) //nolint:gosec // fixed binary, arguments built by this file
	cmd.Dir = dir
	cmd.Env = append(append([]string{}, env...), extraEnv...)
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

// note writes one line of the checkout log, through the redactor.
func (c *Checkout) note(line string) {
	if c.Log == nil {
		return
	}
	if c.Redact != nil {
		line = c.Redact(line)
	}
	_, _ = io.WriteString(c.Log, "# "+line+"\n")
}

// gitQuote writes s as a git config value.
func gitQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// mirrorConfig is the whole config of the mirror. It is written before every fetch, so whatever a step wrote
// there (url.insteadOf, core.fsmonitor, core.hooksPath, filter or diff drivers, an aliases section) is gone.
func (c *Checkout) mirrorConfig(originURL string) string {
	var sb strings.Builder
	sb.WriteString("[core]\n\trepositoryformatversion = 0\n\tfilemode = false\n\tbare = true\n\tautocrlf = false\n\tlongpaths = true\n\tsymlinks = false\n\tfsmonitor = false\n")
	sb.WriteString("\thooksPath = " + gitQuote(filepath.ToSlash(noHooks)) + "\n")
	sb.WriteString("[gc]\n\tauto = 0\n")
	sb.WriteString("[remote \"origin\"]\n\turl = " + gitQuote(originURL) + "\n")
	for _, spec := range MirrorRefspecs {
		sb.WriteString("\tfetch = " + spec + "\n")
	}
	return sb.String()
}

// resetMirror puts the mirror folder back to what the runner expects: the fixed config, no hooks, no
// attributes or alternates, no replace refs, and no worktree record of a run that is gone.
func (c *Checkout) resetMirror(originURL string) error {
	repo := c.Store.RepoPath()
	want := []byte(c.mirrorConfig(originURL))
	cfg := filepath.Join(repo, "config")
	if have, err := os.ReadFile(cfg); err != nil || !bytes.Equal(have, want) { //nolint:gosec // path inside the store
		if err := os.WriteFile(cfg, want, 0o600); err != nil {
			return err
		}
	}
	if err := os.RemoveAll(filepath.Join(repo, "hooks")); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(repo, "hooks"), 0o750); err != nil {
		return err
	}
	for _, p := range []string{"config.worktree", "info/attributes", "info/sparse-checkout", "objects/info/alternates", "shallow", "refs/replace"} {
		if err := os.RemoveAll(filepath.Join(repo, filepath.FromSlash(p))); err != nil {
			return err
		}
	}
	return nil
}

// Mirror creates the bare mirror when it is missing and fetches every branch, tag and pull request head
// from originURL into it. originURL must not carry credentials (they would be stored in the mirror's config).
func (c *Checkout) Mirror(ctx context.Context, originURL string) error {
	if u, err := url.Parse(originURL); err == nil && u.User != nil {
		return errors.New("the origin URL must not contain credentials")
	}
	if strings.ContainsAny(originURL, "\r\n\x00") {
		return errors.New("the origin URL is not one line")
	}
	repo := c.Store.RepoPath()
	if _, err := os.Stat(filepath.Join(repo, "HEAD")); err != nil {
		if _, _, err := c.git(ctx, c.Store.Dir, nil, "init", "--bare", "--quiet", repo); err != nil {
			return err
		}
	}
	c.note("origin " + originURL)
	if err := c.resetMirror(originURL); err != nil {
		return err
	}
	// A run that stopped half way (or a folder somebody removed) leaves worktree records behind.
	if _, _, err := c.git(ctx, repo, nil, "worktree", "prune"); err != nil {
		return err
	}
	_, _, err := c.gitFetch(ctx, repo, "fetch", "--prune", "--no-tags", "--quiet", "origin")
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
	commit, _, err := c.git(ctx, repo, ident, "commit-tree", "--no-gpg-sign", strings.TrimSpace(tree), "-p", base, "-p", head, "-m", "Merge "+head+" into "+base)
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

// addWorktree checks wt.Commit out under work/<runid>. When git stops half way (a full disk, a locked file)
// the half-made folder and its record in the mirror are removed, so the next run starts clean.
func (c *Checkout) addWorktree(ctx context.Context, runID string, wt *Worktree) error {
	wt.Path = filepath.Join(c.Store.WorkPath(), runID)
	_, _, err := c.git(ctx, c.Store.RepoPath(), nil, "worktree", "add", "--detach", "--quiet", wt.Path, wt.Commit)
	if err != nil {
		_ = c.Remove(context.WithoutCancel(ctx), wt.Path)
	}
	return err
}

// JobTree makes one more checkout of the commit base holds, beside it, for one job (name is a short tag such as
// "j3"). Where the run is of a pull request from a stranger, the protected paths are read from the base tip
// again, exactly as in the first checkout. The caller removes it with Remove.
func (c *Checkout) JobTree(ctx context.Context, base *Worktree, name string, fromBase bool, protect []string) (*Worktree, error) {
	if base == nil || base.Path == "" || base.Commit == "" {
		return nil, errors.New("no checkout to copy")
	}
	if !jobTreeName.MatchString(name) {
		return nil, fmt.Errorf("%q is not a job tree name", name)
	}
	wt := &Worktree{Commit: base.Commit, BaseSHA: base.BaseSHA, HeadSHA: base.HeadSHA, Path: base.Path + "-" + name}
	_, _, err := c.git(ctx, c.Store.RepoPath(), nil, "worktree", "add", "--detach", "--quiet", wt.Path, wt.Commit)
	if err != nil {
		_ = c.Remove(context.WithoutCancel(ctx), wt.Path)
		return nil, err
	}
	if fromBase {
		if err := c.overlayBase(ctx, wt, append(append([]string{}, DefaultProtected...), protect...)); err != nil {
			_ = c.Remove(context.WithoutCancel(ctx), wt.Path)
			return nil, err
		}
	}
	return wt, nil
}

var jobTreeName = regexp.MustCompile(`^j[0-9]{1,4}$`)

// removeNoFollow deletes the file or folder at rel (slash separated, inside root) without following a symlink
// or junction: when a component on the way is anything but a plain folder, that entry itself is deleted (never
// what it points to) and the walk stops, because below it there is nothing of the pull request's own left.
func removeNoFollow(root, rel string) error {
	cur := root
	parts := strings.Split(rel, "/")
	for i, part := range parts {
		cur = filepath.Join(cur, part)
		st, err := os.Lstat(cur)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		plainDir := st.IsDir() && st.Mode()&(os.ModeSymlink|os.ModeIrregular) == 0
		switch {
		case plainDir && i == len(parts)-1:
			return os.RemoveAll(cur)
		case plainDir:
			continue
		default:
			return os.Remove(cur)
		}
	}
	return nil
}

// overlayBase replaces the protected paths of the worktree by their content at the base tip.
func (c *Checkout) overlayBase(ctx context.Context, wt *Worktree, protect []string) error {
	for _, p := range protect {
		p = filepath.ToSlash(filepath.Clean(filepath.FromSlash(strings.TrimSpace(p))))
		if p == "." || p == ".git" || strings.HasPrefix(p, ".git/") || !filepath.IsLocal(filepath.FromSlash(p)) {
			return fmt.Errorf("protected path %q is not a folder or file inside the repository", p)
		}
		if err := removeNoFollow(wt.Path, p); err != nil {
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
	_ = c.ensureGitHome()
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
