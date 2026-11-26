package localci

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const (
	// DefaultCacheLimit is how large the build cache of the owner's own runs may grow before the oldest entries
	// are removed. The cache of runs of code the owner did not write gets half of it. Unbounded, a busy day of
	// pull requests filled a 400 GB system drive.
	DefaultCacheLimit int64 = 8 << 30

	// cacheMinAge keeps every entry a run may be using now. The Go build cache refreshes the time of an entry it
	// reads only when that time is more than an hour old, so nothing newer than two hours is ever removed.
	cacheMinAge = 2 * time.Hour
)

type cacheFile struct {
	path string
	size int64
	mod  time.Time
}

// TrimCaches removes the least recently used files of the build caches until the folder of the owner's runs is
// no larger than limit and the folder of the other runs no larger than limit/2. Only the build cache is trimmed:
// it is content-addressed, so a missing entry is only a miss. The module and npm caches are small and are not
// safe to cut file by file. It returns the bytes freed. A file that cannot be removed is skipped.
func (s *Store) TrimCaches(ctx context.Context, limit int64, now time.Time) (int64, error) {
	if limit <= 0 {
		return 0, nil
	}
	var freed int64
	for _, c := range []struct {
		untrusted bool
		limit     int64
	}{{false, limit}, {true, limit / 2}} {
		n, err := trimDir(ctx, filepath.Join(s.cachesFor(c.untrusted), "go-build"), c.limit, now)
		freed += n
		if err != nil {
			return freed, err
		}
	}
	return freed, nil
}

// trimDir removes the files of dir that were used least recently, oldest first, until the rest fit in limit.
// Files directly in dir (the cache's own README and trim.txt) stay.
func trimDir(ctx context.Context, dir string, limit int64, now time.Time) (int64, error) {
	var files []cacheFile
	var total int64
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if err != nil {
			if p == dir {
				return err
			}
			return nil // a file that went while walking
		}
		if d.IsDir() || filepath.Dir(p) == dir {
			return nil
		}
		if info, ierr := d.Info(); ierr == nil { // a file that went since it was listed is skipped
			files = append(files, cacheFile{p, info.Size(), info.ModTime()})
			total += info.Size()
		}
		return nil
	})
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	if total <= limit {
		return 0, nil
	}
	sort.Slice(files, func(i, k int) bool { return files[i].mod.Before(files[k].mod) })
	cutoff := now.Add(-cacheMinAge)
	var freed int64
	for _, f := range files {
		if total-freed <= limit || !f.mod.Before(cutoff) {
			break
		}
		if err := ctx.Err(); err != nil {
			return freed, err
		}
		if removeFile(f.path) {
			freed += f.size
		}
	}
	return freed, nil
}

// removeFile deletes a file, clearing a read-only mark first when that is what stands in the way.
func removeFile(p string) bool {
	if err := os.Remove(p); err == nil {
		return true
	}
	_ = os.Chmod(p, 0o666) //nolint:gosec // a file of the store's own cache
	return os.Remove(p) == nil
}
