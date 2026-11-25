package localci

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
)

// States of a commit status, as the host knows them.
const (
	StatusPending = "pending"
	StatusSuccess = "success"
	StatusFailure = "failure"
	StatusError   = "error"
)

// StatusDescMax is the longest description a commit status takes.
const StatusDescMax = 140

// statusNameMax is the longest context a commit status takes.
const statusNameMax = 255

// DefaultStatusPrefix starts every context this program posts while it is in shadow mode, and every per-job one.
const DefaultStatusPrefix = "local/"

// StatusReporter posts commit statuses. Posting is the only write to the network besides the fetch, and it uses
// the token of the process that runs the reporter.
type StatusReporter interface {
	// Report sets the status `name` of commit sha to state with a short description. No link is attached.
	Report(ctx context.Context, repo, sha, name, state, description string) error
}

// ErrTransient marks a failure worth trying again (a server error, a rate limit, a network problem).
var ErrTransient = errors.New("transient status error")

// PostError is a failed post; Transient says whether trying again may help.
type PostError struct {
	Err       error
	Transient bool
}

func (e *PostError) Error() string { return e.Err.Error() }
func (e *PostError) Unwrap() error { return e.Err }

// IsTransientStatusError says whether a post that failed with err is worth repeating.
func IsTransientStatusError(err error) bool {
	var se *PostError
	if errors.As(err, &se) {
		return se.Transient
	}
	return errors.Is(err, ErrTransient)
}

// classifyGHError tells transient from permanent failures of the gh call, by what gh printed.
func classifyGHError(err error) bool {
	m := strings.ToLower(err.Error())
	for _, perm := range []string{"http 422", "http 404", "http 401", "http 400", "validation failed", "not found", "bad credentials"} {
		if strings.Contains(m, perm) {
			return false
		}
	}
	if strings.Contains(m, "http 403") && !strings.Contains(m, "rate limit") && !strings.Contains(m, "abuse") {
		return false
	}
	return true // 5xx, rate limits, timeouts, resets, no network, and anything unknown: a few tries cost little
}

var (
	tokenRe = regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{16,}|github_pat_[A-Za-z0-9_]{20,}`)
	spaceRe = regexp.MustCompile(`\s+`)
)

// StatusDescription makes text fit a status description: redacted, on one line, at most StatusDescMax characters.
func StatusDescription(s string, redact func(string) string) string {
	if redact != nil {
		s = redact(s)
	}
	s = tokenRe.ReplaceAllString(s, "***")
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && !unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(spaceRe.ReplaceAllString(s, " "))
	if r := []rune(s); len(r) > StatusDescMax {
		s = strings.TrimSpace(string(r[:StatusDescMax-3])) + "..."
	}
	return s
}

// ---------------------------------------------------------------- the gh reporter

// GHReporter posts with `gh api repos/{repo}/statuses/{sha}`. It sets no target_url.
type GHReporter struct {
	// Env is the environment gh runs with (it carries the token); nil means the current one.
	Env []string
	// Redact masks the description before it leaves the machine.
	Redact func(string) string

	exec func(ctx context.Context, env []string, args ...string) ([]byte, error) // test hook
}

// Report implements StatusReporter.
func (g *GHReporter) Report(ctx context.Context, repo, sha, name, state, description string) error {
	if _, _, ok := splitRepo(repo); !ok {
		return fmt.Errorf("status: repository %q is not owner/name", repo)
	}
	if !shaRe.MatchString(sha) {
		return fmt.Errorf("status: %q is not a commit id", sha)
	}
	switch state {
	case StatusPending, StatusSuccess, StatusFailure, StatusError:
	default:
		return fmt.Errorf("status: %q is not a status state", state)
	}
	if name == "" || len([]rune(name)) > statusNameMax || strings.ContainsFunc(name, unicode.IsControl) {
		return fmt.Errorf("status: %q is not a usable context name", name)
	}
	args := []string{"api", "--method", "POST", "repos/" + repo + "/statuses/" + sha,
		"-f", "state=" + state, "-f", "context=" + name, "-f", "description=" + StatusDescription(description, g.Redact)}
	run := g.exec
	if run == nil {
		run = ghExec
	}
	if _, err := run(ctx, g.Env, args...); err != nil {
		return &PostError{Err: err, Transient: classifyGHError(err)}
	}
	return nil
}

// ---------------------------------------------------------------- retry

// RetryReporter repeats a post that failed for a transient reason, waiting longer each time.
type RetryReporter struct {
	Next     StatusReporter
	Attempts int                                        // tries in all, default 4
	Backoff  func(attempt int) time.Duration            // wait after try number attempt (1-based), default 1 s, 2 s, 4 s ...
	Sleep    func(context.Context, time.Duration) error // test hook
}

// Report implements StatusReporter.
func (r *RetryReporter) Report(ctx context.Context, repo, sha, name, state, description string) error {
	n := r.Attempts
	if n < 1 {
		n = 4
	}
	var err error
	for i := 1; i <= n; i++ {
		if err = r.Next.Report(ctx, repo, sha, name, state, description); err == nil || !IsTransientStatusError(err) {
			return err
		}
		if i == n {
			break
		}
		d := time.Second << (i - 1)
		if r.Backoff != nil {
			d = r.Backoff(i)
		}
		sleep := r.Sleep
		if sleep == nil {
			sleep = sleepCtx
		}
		if serr := sleep(ctx, d); serr != nil {
			return fmt.Errorf("%w (stopped while waiting to try again)", err)
		}
	}
	return fmt.Errorf("gave up after %d tries: %w", n, err)
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// ---------------------------------------------------------------- dedup

type ledgerEntry struct {
	State       string    `json:"state"`
	Description string    `json:"description"`
	At          time.Time `json:"at"`
}

// DedupReporter does not repeat a post the host already has: it remembers, per commit and context, the last
// state and description it posted successfully, in a file so that a restart does not repeat them either. The
// description is part of the key because "queued" and "running, job 3 of 7" are both pending and must both be
// posted.
type DedupReporter struct {
	Next StatusReporter
	// Path is the ledger file; empty keeps the memory of this process only.
	Path string
	Keep time.Duration // entries older than this are dropped (default 30 days)
	Now  func() time.Time

	mu     sync.Mutex
	loaded bool
	m      map[string]ledgerEntry
}

func (d *DedupReporter) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

func (d *DedupReporter) load() {
	if d.loaded {
		return
	}
	d.loaded, d.m = true, map[string]ledgerEntry{}
	if d.Path == "" {
		return
	}
	b, err := os.ReadFile(d.Path)
	if err != nil {
		return
	}
	var m map[string]ledgerEntry
	if json.Unmarshal(b, &m) == nil {
		d.m = m
	}
}

func (d *DedupReporter) save() {
	if d.Path == "" {
		return
	}
	keep := d.Keep
	if keep <= 0 {
		keep = 30 * 24 * time.Hour
	}
	for k, e := range d.m {
		if d.now().Sub(e.At) > keep {
			delete(d.m, k)
		}
	}
	if err := os.MkdirAll(filepath.Dir(d.Path), 0o750); err == nil {
		_ = writeAtomic(d.Path, d.m)
	}
}

// Report implements StatusReporter.
func (d *DedupReporter) Report(ctx context.Context, repo, sha, name, state, description string) error {
	key := repo + "@" + sha + " " + name
	d.mu.Lock()
	defer d.mu.Unlock()
	d.load()
	if e, ok := d.m[key]; ok && e.State == state && e.Description == StatusDescription(description, nil) {
		return nil
	}
	if err := d.Next.Report(ctx, repo, sha, name, state, description); err != nil {
		return err
	}
	d.m[key] = ledgerEntry{State: state, Description: StatusDescription(description, nil), At: d.now()}
	d.save()
	return nil
}

// ---------------------------------------------------------------- shadow guard

// ErrNotShadow is the refusal of a post that would set a context outside the shadow prefix.
var ErrNotShadow = errors.New("refused: shadow mode only posts contexts under the shadow prefix")

// ShadowGuard is the last line of the shadow phase. Unless PostRequired is set it lets only contexts under
// Prefix through, whatever asked for them.
type ShadowGuard struct {
	Next         StatusReporter
	Prefix       string
	PostRequired bool
}

// Report implements StatusReporter.
func (g *ShadowGuard) Report(ctx context.Context, repo, sha, name, state, description string) error {
	prefix := g.Prefix
	if prefix == "" {
		prefix = DefaultStatusPrefix
	}
	if !g.PostRequired && !strings.HasPrefix(name, prefix) {
		return fmt.Errorf("%w: %q does not start with %q", ErrNotShadow, name, prefix)
	}
	return g.Next.Report(ctx, repo, sha, name, state, description)
}

// ---------------------------------------------------------------- fake

// StatusCall is one post a FakeReporter saw.
type StatusCall struct {
	Repo, SHA, Name, State, Description string
}

// FakeReporter records posts instead of making them (tests, dry runs). Fail, when set, decides the error of
// call number n (1-based, counting every call, failed or not).
type FakeReporter struct {
	Fail func(n int, c StatusCall) error

	mu    sync.Mutex
	calls []StatusCall
	tries int
}

// Report implements StatusReporter.
func (f *FakeReporter) Report(_ context.Context, repo, sha, name, state, description string) error {
	c := StatusCall{repo, sha, name, state, description}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tries++
	if f.Fail != nil {
		if err := f.Fail(f.tries, c); err != nil {
			return err
		}
	}
	f.calls = append(f.calls, c)
	return nil
}

// Calls lists the posts that went through, in order.
func (f *FakeReporter) Calls() []StatusCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]StatusCall(nil), f.calls...)
}

// Tries is how many times Report was called, failed ones included.
func (f *FakeReporter) Tries() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.tries
}
