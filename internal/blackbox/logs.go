package blackbox

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Limits on the logs catalogue walk, so a huge log tree cannot slow a sample.
const (
	logMaxDepth   = 4
	logMaxVisited = 5000
)

// CatalogueLogs lists the log files (*.log) under the given folders: path, size
// and time of last write. File content is never opened. The newest MaxLogEntries
// files are returned, newest first. A folder that does not exist is skipped.
func CatalogueLogs(roots []string) []LogEntry {
	var all []LogEntry
	visited := 0
	for _, root := range roots {
		root = filepath.Clean(root)
		base := strings.Count(root, string(os.PathSeparator))
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				if d != nil && d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			visited++
			if visited > logMaxVisited {
				return fs.SkipAll
			}
			if d.IsDir() {
				if strings.Count(path, string(os.PathSeparator))-base >= logMaxDepth {
					return fs.SkipDir
				}
				return nil
			}
			if d.Type()&fs.ModeSymlink != 0 || !strings.EqualFold(filepath.Ext(path), ".log") {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			all = append(all, LogEntry{Path: path, Size: info.Size(), MTime: info.ModTime().UTC().Truncate(time.Second)})
			return nil
		})
	}
	sort.Slice(all, func(i, j int) bool {
		if !all[i].MTime.Equal(all[j].MTime) {
			return all[i].MTime.After(all[j].MTime)
		}
		return all[i].Path < all[j].Path
	})
	if len(all) > MaxLogEntries {
		all = all[:MaxLogEntries]
	}
	return all
}

// MergeLogRoots adds the folders the sources manifest found (for example IIS sites logging outside the default folder) to
// the configured roots. A found folder is skipped when a configured one is the same or holds it, so nothing is walked
// twice. The result never has more than maxLogPaths folders; the configured ones always come first.
func MergeLogRoots(configured, found []string) []string {
	out := append([]string(nil), configured...)
	key := func(p string) string {
		return strings.ToLower(strings.TrimRight(filepath.Clean(p), `\/`))
	}
	covers := func(root, p string) bool {
		r, q := key(root), key(p)
		return q == r || strings.HasPrefix(q, r+`\`) || strings.HasPrefix(q, r+`/`)
	}
	for _, f := range found {
		if len(out) >= maxLogPaths {
			break
		}
		if strings.TrimSpace(f) == "" {
			continue
		}
		dup := false
		for _, r := range out {
			if covers(r, f) {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, f)
		}
	}
	return out
}
