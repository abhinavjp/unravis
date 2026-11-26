package localci

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	// DefaultRunKeep is how long the folders of a finished run (logs, artifacts, a worktree somebody kept) stay.
	DefaultRunKeep = 7 * 24 * time.Hour

	// DefaultRunsLimit is the size runs/ and work/ together are cut down to once the age rule is done: the
	// oldest finished runs go first.
	DefaultRunsLimit int64 = 10 << 30

	// runMinAge keeps the folders of a run that ended a moment ago whatever the size rule says: its logs are
	// being read, its worktree is being removed.
	runMinAge = 2 * time.Hour
)

// TrimRunsOptions say what TrimRuns may remove.
type TrimRunsOptions struct {
	// Keep: finished runs that ended longer ago than this are removed. Zero: DefaultRunKeep. Negative: no age
	// rule (only the size rule removes anything). The cutoff is never later than runMinAge ago, whatever Keep says.
	Keep time.Duration
	// Limit: after the age rule, the oldest finished runs are removed until runs/ and work/ together are no larger
	// than this. Zero: DefaultRunsLimit. Negative: no size rule.
	Limit int64
	// Now is the clock; nil is time.Now.
	Now time.Time
	// ActiveSince is the claim time of the oldest request that is running now (zero: none runs). A folder with no
	// run.json that was made at or after it (less a minute) may belong to that request and is never touched. It
	// is enough to read it once, without a lock: a request claimed after the read starts its run after "now", and
	// only folders older than Keep are ever removed.
	ActiveSince time.Time
	// RemoveWork removes one worktree folder (it has been checked to be a plain folder inside work/). Nil: a
	// Checkout of the store, which also drops the worktree's record in the mirror.
	RemoveWork func(ctx context.Context, path string) error
	// RemoveAll removes a file or folder tree inside a run folder (it has been checked to be inside runs/<id>).
	// Nil: os.RemoveAll.
	RemoveAll func(path string) error
}

// TrimRunsResult is what one TrimRuns did.
type TrimRunsResult struct {
	Runs    int      // run folders removed
	Work    int      // work folders removed
	Freed   int64    // bytes freed
	Skipped []string // folders left alone and why (a link, a name that is not a run id, a folder that would not go)
}

type trimUnit struct {
	id       string
	runPath  string // runs/<id>, or "" when only a work folder is left
	workDirs []string
	size     int64
	ended    time.Time // run.json time; zero for a run that has none
	started  time.Time // from the id
	recorded bool
}

// TrimRuns removes the folders of old finished runs from runs/ and work/. A run is finished when its run.json
// exists, the same rule as RunFolders. It never follows a link: every folder is looked at with Lstat and
// resolved, and one that is not a plain folder directly inside the store's own runs/ or work/ folder is left
// alone. Folders of a run that has no run.json are touched only when they are older than Keep and started
// before every request that is running (a crashed run). The index file stays whole: it is the history.
func (s *Store) TrimRuns(ctx context.Context, o TrimRunsOptions) (TrimRunsResult, error) {
	var res TrimRunsResult
	keep := o.Keep
	if keep == 0 {
		keep = DefaultRunKeep
	}
	ageRule := keep > 0
	limit := o.Limit
	if limit == 0 {
		limit = DefaultRunsLimit
	}
	now := o.Now
	if now.IsZero() {
		now = time.Now()
	}
	removeAll := o.RemoveAll
	if removeAll == nil {
		removeAll = os.RemoveAll
	}
	removeWork := o.RemoveWork
	if removeWork == nil {
		co := &Checkout{Store: s}
		removeWork = co.Remove
	}

	units := map[string]*trimUnit{}
	get := func(id string) *trimUnit {
		u := units[id]
		if u == nil {
			u = &trimUnit{id: id}
			u.started, _ = IDTime(id)
			units[id] = u
		}
		return u
	}
	var total int64
	runEnts, err := os.ReadDir(s.RunsPath())
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return res, err
	}
	for _, e := range runEnts {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		if !e.IsDir() && e.Type()&fs.ModeSymlink == 0 && e.Type()&fs.ModeIrregular == 0 {
			continue // index.jsonl and other files
		}
		p, why := safeChild(s, s.RunsPath(), e.Name())
		if why != "" {
			res.Skipped = append(res.Skipped, "runs/"+e.Name()+": "+why)
			continue
		}
		u := get(e.Name())
		u.runPath = p
		if st, serr := os.Lstat(filepath.Join(p, "run.json")); serr == nil && st.Mode().IsRegular() {
			u.recorded, u.ended = true, st.ModTime()
		}
		n, _ := dirSize(ctx, p)
		u.size += n
		total += n
	}
	workEnts, err := os.ReadDir(s.WorkPath())
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return res, err
	}
	for _, e := range workEnts {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		if !e.IsDir() && e.Type()&fs.ModeSymlink == 0 && e.Type()&fs.ModeIrregular == 0 {
			continue // a stray file
		}
		p, why := safeChild(s, s.WorkPath(), e.Name())
		if why != "" {
			res.Skipped = append(res.Skipped, "work/"+e.Name()+": "+why)
			continue
		}
		if !isCIWorkName(e.Name()) {
			res.Skipped = append(res.Skipped, "work/"+e.Name()+": not named like a CI work folder")
			continue
		}
		id := e.Name()
		if _, ok := units[id]; !ok {
			if base, ok := stripJobTree(id); ok {
				if _, ok := units[base]; ok {
					id = base
				}
			}
		}
		u := get(id)
		n, newest := dirSize(ctx, p)
		if u.runPath == "" && newest.After(u.ended) {
			// No run folder: the run's logs are already gone (or never were). The newest file of the worktree
			// decides how old it is.
			u.ended, u.recorded = newest, true
		}
		u.workDirs = append(u.workDirs, p)
		u.size += n
		total += n
	}

	// The cutoff is never later than runMinAge ago, so a small or odd Keep cannot reach a run that just ended.
	cutoff := now.Add(-keep)
	if latest := now.Add(-runMinAge); !ageRule || cutoff.After(latest) {
		cutoff = latest
	}
	activeCut := time.Time{}
	if !o.ActiveSince.IsZero() {
		activeCut = o.ActiveSince.Add(-time.Minute)
	}
	// eligible says whether a unit may go at all; byAge whether the age rule alone removes it.
	eligible := func(u *trimUnit) bool {
		if u.recorded {
			return true
		}
		// A run with no run.json is going or crashed: only a crashed one older than Keep that began before
		// every running request can go.
		if !ageRule || u.started.IsZero() || !u.started.Before(cutoff) {
			return false
		}
		return activeCut.IsZero() || u.started.Before(activeCut)
	}
	age := func(u *trimUnit) time.Time {
		if u.recorded {
			return u.ended
		}
		return u.started
	}
	var order []*trimUnit
	for _, u := range units {
		if eligible(u) {
			order = append(order, u)
		} else {
			// Space that can never be reclaimed here (a run that may be going, a crashed one younger than Keep)
			// does not count toward the limit: otherwise the size rule would remove every finished run each hour
			// and still not get under it.
			total -= u.size
		}
	}
	sort.Slice(order, func(i, k int) bool {
		if ai, ak := age(order[i]), age(order[k]); !ai.Equal(ak) {
			return ai.Before(ak)
		}
		return order[i].id < order[k].id
	})

	remove := func(u *trimUnit) {
		ok := true
		for _, w := range u.workDirs {
			if err := removeWork(context.WithoutCancel(ctx), w); err != nil {
				res.Skipped = append(res.Skipped, "work/"+filepath.Base(w)+": "+err.Error())
				ok = false
				continue
			}
			res.Work++
		}
		if u.runPath != "" && ok {
			if err := removeRunFolder(u.runPath, removeAll); err != nil {
				res.Skipped = append(res.Skipped, "runs/"+u.id+": "+err.Error())
				ok = false
			} else {
				res.Runs++
			}
		}
		if ok {
			res.Freed += u.size
			total -= u.size
		}
	}
	// The age rule.
	rest := order[:0:0]
	for _, u := range order {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		if ageRule && age(u).Before(cutoff) {
			remove(u)
			continue
		}
		rest = append(rest, u)
	}
	// The size rule: oldest first, never a run that ended in the last two hours.
	if limit > 0 {
		recent := now.Add(-runMinAge)
		for _, u := range rest {
			if total <= limit {
				break
			}
			if err := ctx.Err(); err != nil {
				return res, err
			}
			if !age(u).Before(recent) {
				break // sorted: every later one is newer still
			}
			remove(u)
		}
	}
	return res, nil
}

// removeRunFolder removes a run folder with run.json going last. A file that is held open (a dashboard reading a
// log, a virus scanner) makes its removal fail; if run.json were already gone by then the run would look unfinished
// (still going) and never be trimmed again. With run.json removed last, a failed removal leaves the run finished
// and the next trim tries again.
func removeRunFolder(dir string, removeAll func(string) error) error {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var firstErr error
	for _, e := range ents {
		if e.Name() == "run.json" {
			continue
		}
		if err := removeAll(filepath.Join(dir, e.Name())); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if firstErr != nil {
		return firstErr
	}
	if err := removeAll(filepath.Join(dir, "run.json")); err != nil {
		return err
	}
	return removeAll(dir)
}

// isCIWorkName says whether a folder name in work/ is one the CI makes: a run id, or a run id and "-j<N>".
func isCIWorkName(name string) bool {
	if _, ok := IDTime(name); ok {
		return true
	}
	if base, ok := stripJobTree(name); ok {
		_, ok = IDTime(base)
		return ok
	}
	return false
}

// stripJobTree turns "<run id>-j3" (the extra checkout of one job) into the run id.
func stripJobTree(name string) (string, bool) {
	i := strings.LastIndex(name, "-j")
	if i <= 0 || !jobTreeName.MatchString(name[i+1:]) {
		return "", false
	}
	return name[:i], true
}

// safeChild returns the path of folder name directly inside root (runs/ or work/ of the store), or why it must
// not be touched. The name must be a run id (no separator, no ".."), the entry must be a plain folder and not a
// link of any kind (symlink, junction), and once links are resolved it must still sit directly in the store's own
// root folder, which itself must sit directly in the store's folder.
func safeChild(s *Store, root, name string) (string, string) {
	if !ValidRunID(name) {
		return "", "not a run id"
	}
	p := filepath.Join(root, name)
	st, err := os.Lstat(p)
	if err != nil {
		return "", "cannot be read"
	}
	if st.Mode()&(fs.ModeSymlink|fs.ModeIrregular) != 0 {
		return "", "is a link"
	}
	if !st.IsDir() {
		return "", "is not a folder"
	}
	realStore, err := filepath.EvalSymlinks(s.Dir)
	if err != nil {
		return "", "the store folder cannot be resolved"
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil || !directlyIn(realStore, realRoot) || !strings.EqualFold(filepath.Base(realRoot), filepath.Base(root)) {
		return "", "its parent folder is not the store's own"
	}
	realP, err := filepath.EvalSymlinks(p)
	if err != nil || !directlyIn(realRoot, realP) || !strings.EqualFold(filepath.Base(realP), name) {
		return "", "resolves outside the store"
	}
	return p, ""
}

// directlyIn is true when child is a folder one level below parent (both already resolved).
func directlyIn(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != "." && filepath.IsLocal(rel) && !strings.ContainsRune(rel, filepath.Separator)
}

// dirSize adds up the sizes of the files below dir without following links, and tells the newest change time of
// a file in it.
func dirSize(ctx context.Context, dir string) (int64, time.Time) {
	var n int64
	var newest time.Time
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if err != nil || d == nil {
			return nil //nolint:nilerr // an entry that cannot be read is left out of the size, the walk goes on
		}
		if info, ierr := d.Info(); ierr == nil {
			if info.Mode().IsRegular() {
				n += info.Size()
				if info.ModTime().After(newest) {
					newest = info.ModTime() // folders are not counted: their time is when an entry was added
				}
			}
		}
		return nil
	})
	return n, newest
}

// describe is the line the agent logs after a trim.
func (r TrimRunsResult) describe() string {
	return fmt.Sprintf("removed %d old run folders and %d work folders, freed %d MB", r.Runs, r.Work, r.Freed>>20)
}
