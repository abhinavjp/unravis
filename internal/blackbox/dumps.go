package blackbox

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// BBX-6-02 crash-dump file catalogue: names, sizes, times and parsed targets
// only — dump content is never opened, copied or created. Fixed roots, depth
// 2, at most 500 visited entries and 200 listed dumps, re-scanned at most
// every 5 minutes (the logs catalogue cadence). No goroutine, no ticker.

// Limits on the dump catalogue walk, so a huge tree cannot slow a sample.
const (
	dumpMaxDepth   = 2
	dumpMaxVisited = 500
	dumpMaxListed  = 200
	dumpsEvery     = 5 * time.Minute
)

func init() { RegisterGroup("dumps", true, newDumpsGroup) }

// dumpRoots returns the fixed crash-dump roots. A root that does not exist is
// skipped by the walk; on non-Windows none exist and the group stays nil.
func dumpRoots() []string {
	roots := []string{`C:\Windows\Minidump`}
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		roots = append(roots, filepath.Join(local, "CrashDumps"))
	}
	if pd := os.Getenv("ProgramData"); pd != "" {
		roots = append(roots,
			filepath.Join(pd, "Microsoft", "Windows", "WER", "ReportArchive"),
			filepath.Join(pd, "Microsoft", "Windows", "WER", "ReportQueue"))
	}
	return roots
}

// catalogueDumps lists *.dmp files (case-insensitive) under the given roots:
// base name, size, mtime and parsed target. Only DirEntry.Info() is read —
// never file content. Symlinks are skipped; newest first, capped at 200.
func catalogueDumps(roots []string) []DumpEntry {
	var all []DumpEntry
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
			if visited > dumpMaxVisited {
				return fs.SkipAll
			}
			if d.IsDir() {
				if strings.Count(path, string(os.PathSeparator))-base >= dumpMaxDepth {
					return fs.SkipDir
				}
				return nil
			}
			if d.Type()&fs.ModeSymlink != 0 || !strings.EqualFold(filepath.Ext(path), ".dmp") {
				return nil
			}
			info, err := d.Info() // stat only: the content is never opened
			if err != nil {
				return nil
			}
			all = append(all, DumpEntry{
				Name:   d.Name(),
				Size:   info.Size(),
				MTime:  info.ModTime().UTC().Truncate(time.Second),
				Target: dumpTarget(d.Name()),
			})
			return nil
		})
	}
	sort.Slice(all, func(i, j int) bool {
		if !all[i].MTime.Equal(all[j].MTime) {
			return all[i].MTime.After(all[j].MTime)
		}
		return all[i].Name < all[j].Name
	})
	if len(all) > dumpMaxListed {
		all = all[:dumpMaxListed]
	}
	return all
}

// dumpTarget parses the process name from a dump file name when obvious:
// "w3wp.exe.1234.dmp" → "w3wp", "iexplore.exe_5678.dmp" → "iexplore".
// Anything else yields "" — never a path, never content.
func dumpTarget(name string) string {
	ext := filepath.Ext(name)
	if !strings.EqualFold(ext, ".dmp") {
		return ""
	}
	base := name[:len(name)-len(ext)]
	i := strings.Index(strings.ToLower(base), ".exe")
	if i <= 0 {
		return ""
	}
	if rest := base[i+4:]; rest != "" && rest[0] != '.' && rest[0] != '_' {
		return "" // not an obvious <process>.exe name
	}
	return base[:i]
}

// dumpsGroup caches the last scan and re-scans at most every 5 minutes.
// scan is injected so tests can count scans without touching real roots.
type dumpsGroup struct {
	roots    []string
	scan     func(roots []string) []DumpEntry
	lastScan time.Time
	cached   []DumpEntry
}

func newDumpsGroup(GroupDeps) (Group, error) {
	return &dumpsGroup{roots: dumpRoots(), scan: catalogueDumps}, nil
}

func (g *dumpsGroup) Name() string   { return "dumps" }
func (g *dumpsGroup) Optional() bool { return true }

func (g *dumpsGroup) Collect(now time.Time, dt float64) (any, error) {
	if g.lastScan.IsZero() || now.Sub(g.lastScan) >= dumpsEvery || now.Before(g.lastScan) {
		g.cached = g.scan(g.roots)
		g.lastScan = now
	}
	if len(g.cached) == 0 {
		return nil, nil // no dumps: group omitted, never 0
	}
	return &DumpsGroup{Dumps: g.cached}, nil
}