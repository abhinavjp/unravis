package localci

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Store is the folder a PC keeps its CI state in:
//
//	repo.git/            bare mirror of the origin (all branches, tags and pull request heads)
//	work/<runid>/        throw-away worktrees, removed after a run
//	runs/<runid>/        run.json, logs/<job>/NN-step.log, checkout.log, artifacts/, summary.md
//	runs/index.jsonl     one line per run, appended
//	caches/              persistent Go and npm caches shared by every run
//
// Nothing in it names a project: the owner of the Store says where it lives.
type Store struct {
	Dir string
}

// OpenStore creates the folder layout and returns the Store.
func OpenStore(dir string) (*Store, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("store: no folder given")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	s := &Store{Dir: abs}
	for _, d := range []string{s.RunsPath(), s.WorkPath(), s.CachesPath()} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// RepoPath is the bare mirror.
func (s *Store) RepoPath() string { return filepath.Join(s.Dir, "repo.git") }

// RunsPath holds one folder per run and the index.
func (s *Store) RunsPath() string { return filepath.Join(s.Dir, "runs") }

// WorkPath holds the worktrees.
func (s *Store) WorkPath() string { return filepath.Join(s.Dir, "work") }

// CachesPath holds the persistent caches.
func (s *Store) CachesPath() string { return filepath.Join(s.Dir, "caches") }

// IndexPath is the append-only list of runs.
func (s *Store) IndexPath() string { return filepath.Join(s.RunsPath(), "index.jsonl") }

var runIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// ValidRunID reports whether id can name a folder of the store (no separators, no "..").
func ValidRunID(id string) bool {
	return runIDRe.MatchString(id) && !strings.Contains(id, "..") && len(id) <= 120
}

// RunDir returns the folder of an existing run.
func (s *Store) RunDir(id string) (string, error) {
	if !ValidRunID(id) {
		return "", fmt.Errorf("%q is not a run id", id)
	}
	d := filepath.Join(s.RunsPath(), id)
	if st, err := os.Stat(d); err != nil || !st.IsDir() {
		return "", fmt.Errorf("no run %s in %s", id, s.RunsPath())
	}
	return d, nil
}

// NewRun claims a new run id and creates its folder. The id is "<UTC time>-<label>" and stays unique: a
// second run in the same second gets a numeric suffix (the folder is created atomically).
func (s *Store) NewRun(now time.Time, label string) (id, dir string, err error) {
	base := now.UTC().Format("20060102-150405") + "-" + slug(label)
	for n := 1; n < 1000; n++ {
		id = base
		if n > 1 {
			id = base + "-" + strconv.Itoa(n)
		}
		dir = filepath.Join(s.RunsPath(), id)
		err = os.Mkdir(dir, 0o750)
		if err == nil {
			return id, dir, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return "", "", err
		}
	}
	return "", "", fmt.Errorf("store: cannot find a free run id for %s", base)
}

// ---------------------------------------------------------------- index

// IndexEntry is one line of runs/index.jsonl.
type IndexEntry struct {
	RunID      string    `json:"run_id"`
	Workflow   string    `json:"workflow,omitempty"`
	Event      string    `json:"event,omitempty"`
	Repo       string    `json:"repo,omitempty"`
	PR         int       `json:"pr,omitempty"`
	Author     string    `json:"author,omitempty"`
	BaseRef    string    `json:"base_ref,omitempty"`
	HeadRef    string    `json:"head_ref,omitempty"`
	HeadSHA    string    `json:"head_sha,omitempty"`
	CommitSHA  string    `json:"commit_sha,omitempty"` // what ran: the merge commit of a pull request, or the pushed commit
	Conclusion string    `json:"conclusion"`           // success, failure, cancelled or error
	Reason     string    `json:"reason,omitempty"`
	Start      time.Time `json:"start"`
	End        time.Time `json:"end"`
}

// masked returns a copy with every text field passed through redact.
func (e IndexEntry) masked(redact func(string) string) IndexEntry {
	if redact == nil {
		return e
	}
	for _, p := range []*string{&e.Workflow, &e.Event, &e.Repo, &e.Author, &e.BaseRef, &e.HeadRef, &e.HeadSHA, &e.CommitSHA, &e.Conclusion, &e.Reason} {
		*p = redact(*p)
	}
	return e
}

var indexMu sync.Mutex

// AppendIndex appends one line to runs/index.jsonl. The line is built in memory and written with one append
// write, so two processes appending at once never interleave inside a line. Every text field goes through redact.
func (s *Store) AppendIndex(e IndexEntry, redact func(string) string) error {
	b, err := json.Marshal(e.masked(redact))
	if err != nil {
		return err
	}
	indexMu.Lock()
	defer indexMu.Unlock()
	f, err := os.OpenFile(s.IndexPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, werr := f.Write(append(b, '\n'))
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	return werr
}

// ReadIndex returns every readable line of the index, oldest first. A line that does not parse (a torn
// write after a crash) is skipped.
func (s *Store) ReadIndex() ([]IndexEntry, error) {
	f, err := os.Open(s.IndexPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var out []IndexEntry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var e IndexEntry
		if json.Unmarshal(sc.Bytes(), &e) == nil && e.RunID != "" {
			out = append(out, e)
		}
	}
	return out, sc.Err()
}

// ---------------------------------------------------------------- caches

// cacheVars are the environment variables that point tools at the persistent caches, and the folder of each.
var cacheVars = []struct{ env, dir string }{
	{"GOCACHE", "go-build"},
	{"GOMODCACHE", "go-mod"},
	{"npm_config_cache", "npm"},
}

// CacheEnv returns base with GOCACHE, GOMODCACHE and npm_config_cache set to folders of the store (created),
// replacing any value base had, so every run starts warm and no run writes into the user's own caches.
func (s *Store) CacheEnv(base []string) ([]string, error) {
	drop := map[string]bool{}
	for _, c := range cacheVars {
		drop[strings.ToUpper(c.env)] = true
	}
	var out []string
	for _, kv := range base {
		if k, _, ok := splitKV(kv); ok && drop[strings.ToUpper(k)] {
			continue
		}
		out = append(out, kv)
	}
	for _, c := range cacheVars {
		d := filepath.Join(s.CachesPath(), c.dir)
		if err := os.MkdirAll(d, 0o750); err != nil {
			return nil, err
		}
		out = append(out, c.env+"="+d)
	}
	return out, nil
}

// ---------------------------------------------------------------- reading a run back

// ReadRunRecord reads runs/<id>/run.json.
func (s *Store) ReadRunRecord(id string) (*RunResult, error) {
	d, err := s.RunDir(id)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(filepath.Join(d, "run.json")) //nolint:gosec // path inside the store
	if err != nil {
		return nil, err
	}
	var r RunResult
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("run.json of %s: %w", id, err)
	}
	return &r, nil
}

// LogPart is one log file of a run, in reading order.
type LogPart struct {
	Title string // "job / step 3 name (conclusion)"
	Path  string
}

// CheckoutLogName is the log of the fetch, merge and worktree work of a run, and the job name it is read by.
const CheckoutLogName = "checkout"

// JobLogs returns the log files of the job instances that match job (a job key or a job id), in order. The
// name "checkout" returns the checkout log.
func (s *Store) JobLogs(id, job string) ([]LogPart, error) {
	d, err := s.RunDir(id)
	if err != nil {
		return nil, err
	}
	if job == CheckoutLogName {
		p := filepath.Join(d, "checkout.log")
		if _, err := os.Stat(p); err != nil {
			return nil, fmt.Errorf("run %s has no checkout log", id)
		}
		return []LogPart{{Title: "checkout", Path: p}}, nil
	}
	rec, err := s.ReadRunRecord(id)
	if err != nil {
		return nil, err
	}
	var parts []LogPart
	var known []string
	for _, j := range rec.Jobs {
		known = append(known, j.Key)
		if j.Key != job && j.ID != job {
			continue
		}
		for _, st := range j.Steps {
			if st.LogFile == "" {
				continue
			}
			p := st.LogFile
			if !filepath.IsAbs(p) || !pathInside(d, p) {
				continue // run.json is data: never read a file outside the run's folder
			}
			parts = append(parts, LogPart{Title: fmt.Sprintf("%s / step %d %s (%s)", j.Key, st.Index, st.Name, st.Conclusion), Path: p})
		}
	}
	if len(parts) == 0 {
		sort.Strings(known)
		return nil, fmt.Errorf("run %s has no log for job %q (jobs: %s, %s)", id, job, strings.Join(known, ", "), CheckoutLogName)
	}
	return parts, nil
}

// pathInside reports whether p is root or below it.
func pathInside(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && filepath.IsLocal(rel)
}
