package blackbox

import (
	"container/heap"
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
//
// W12-06: the walk is a bounded BATCHED directory read, not filepath.WalkDir
// (which reads a whole directory before the callback's cap can stop it). The
// visit budget is shared by all roots and is checked before the next root is
// opened; the newest entries are kept in a bounded heap, so the result is the
// newest 200 of what was visited and the status says honestly when the visit
// budget truncated the scan or a root could not be read.
//
// RV-BB-11 (W-36): inside each folder the entries are read first (up to the
// enumeration budget), sorted newest first by modified time, and only then
// counted against the visit and list limits. Before, the limits were applied in
// directory order, so in a large dump folder the newest dumps could be the ones
// left out.

// Limits on the dump catalogue walk, so a huge tree cannot slow a sample.
const (
	dumpMaxDepth = 2
	// dumpMaxVisited is how many dump files and sub-folders (roots included) are
	// looked at, newest first within each folder.
	dumpMaxVisited = 500
	dumpMaxListed  = 200
	// dumpMaxEnumerated is how many directory entries of any kind the whole scan
	// reads (names and types only) to find the newest ones. Bigger than the visit
	// budget so a folder of a few thousand files still shows its newest dumps; a
	// folder bigger than this is read in part and the status says so.
	dumpMaxEnumerated = 5000
	dumpsEvery        = 5 * time.Minute
	// dumpReadBatch is how many directory entries one ReadDir call takes: the
	// enumeration cost of a huge directory is bounded per batch, and the visit
	// budget stops the scan between batches instead of after the whole listing.
	dumpReadBatch = 64
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

// DumpScanStatus says how much of the tree a catalogue scan actually read, so a
// truncated or partly unreadable scan is never presented as a complete
// newest-first catalogue (W12-06).
type DumpScanStatus struct {
	// Visited is how many dump files and sub-folders (roots included) the scan
	// counted against the visit budget.
	Visited int
	// Truncated is true when the visit or enumeration budget stopped the scan
	// before the roots were fully read: later entries and later roots were not
	// looked at.
	Truncated bool
	// Unreadable counts roots or subdirectories that could not be opened or
	// were refused (a link or reparse alias). A root that simply does not
	// exist is normal and is not counted.
	Unreadable int
}

// dumpReadDir is the batched directory-read seam: tests wrap it to count how
// many entries a scan actually fetched.
var dumpReadDir = func(f *os.File, n int) ([]fs.DirEntry, error) { return f.ReadDir(n) }

// catalogueDumps lists *.dmp files (case-insensitive) under the given roots:
// base name, size, mtime and parsed target. Only DirEntry.Info() is read —
// never file content. Symlinks, junctions and other reparse aliases are
// skipped (a junction Lstats as ModeIrregular on Windows, so ModeSymlink alone
// is not enough); newest first, capped at 200, with the scan status alongside.
func catalogueDumps(roots []string) ([]DumpEntry, DumpScanStatus) {
	var st DumpScanStatus
	h := &dumpHeap{}
	heap.Init(h)
	// visit counts one entry against the shared budget; false means the budget
	// is spent and the whole scan must stop.
	visit := func() bool {
		st.Visited++
		if st.Visited > dumpMaxVisited {
			st.Truncated = true
			return false
		}
		return true
	}
	// enumerated counts every directory entry the scan has read, of any kind.
	enumerated := 0
	// readCandidates reads one folder in bounded batches and returns its dump
	// files and sub-folders, newest first. Entries that can never be listed
	// (other files, links, folders past the depth limit) cost an enumeration
	// step but no visit and no stat.
	readCandidates := func(dir string, depth int) []dumpCand {
		f, err := os.Open(dir) // a directory handle, never a dump's content
		if err != nil {
			if !os.IsNotExist(err) {
				st.Unreadable++
			}
			return nil
		}
		defer f.Close()
		var cands []dumpCand
		for {
			batch, rerr := dumpReadDir(f, dumpReadBatch)
			if enumerated >= dumpMaxEnumerated && len(batch) > 0 {
				st.Truncated = true // there is more here than the scan may read
				break
			}
			enumerated += len(batch)
			for _, d := range batch {
				name := d.Name()
				c := dumpCand{name: name}
				if d.IsDir() {
					if depth+1 >= dumpMaxDepth {
						continue
					}
					c.isDir = true
					if info, ierr := d.Info(); ierr == nil {
						c.mtime = info.ModTime().UTC().Truncate(time.Second)
					}
				} else {
					if d.Type()&(fs.ModeSymlink|fs.ModeIrregular) != 0 || !strings.EqualFold(filepath.Ext(name), ".dmp") {
						continue
					}
					info, ierr := d.Info() // stat only: the content is never opened
					if ierr != nil {
						continue
					}
					c.size = info.Size()
					c.mtime = info.ModTime().UTC().Truncate(time.Second)
				}
				cands = append(cands, c)
			}
			if rerr != nil || len(batch) == 0 {
				break
			}
		}
		// Newest first, so the visit and list limits keep the newest.
		sort.Slice(cands, func(i, j int) bool {
			if !cands[i].mtime.Equal(cands[j].mtime) {
				return cands[i].mtime.After(cands[j].mtime)
			}
			return cands[i].name < cands[j].name
		})
		return cands
	}
	var scanDir func(dir string, depth int)
	scanDir = func(dir string, depth int) {
		for _, c := range readCandidates(dir, depth) {
			if !visit() {
				return
			}
			if c.isDir {
				sub := filepath.Join(dir, c.name)
				// A junction or symlink directory is not followed: Lstat
				// sees the alias itself (ModeIrregular / ModeSymlink).
				li, lerr := os.Lstat(sub)
				if lerr != nil || !li.IsDir() || li.Mode()&(fs.ModeSymlink|fs.ModeIrregular) != 0 {
					if lerr != nil && !os.IsNotExist(lerr) {
						st.Unreadable++
					}
					continue
				}
				scanDir(sub, depth+1)
				continue
			}
			heap.Push(h, DumpEntry{
				Name:   c.name,
				Size:   c.size,
				MTime:  c.mtime,
				Target: dumpTarget(c.name),
			})
			if h.Len() > dumpMaxListed {
				heap.Pop(h) // evict the oldest of the kept set
			}
		}
	}
	for _, root := range roots {
		// The budgets are checked BEFORE the next root is opened: an exhausted
		// scan never touches another directory (W12-06).
		if st.Truncated || st.Visited >= dumpMaxVisited || enumerated >= dumpMaxEnumerated {
			st.Truncated = true
			break
		}
		root = filepath.Clean(root)
		li, err := os.Lstat(root)
		if err != nil {
			if !os.IsNotExist(err) {
				st.Unreadable++
			}
			continue
		}
		if !li.IsDir() || li.Mode()&(fs.ModeSymlink|fs.ModeIrregular) != 0 {
			st.Unreadable++ // a link or reparse root is refused, not followed
			continue
		}
		if !visit() {
			break
		}
		scanDir(root, 0)
	}
	all := make([]DumpEntry, h.Len())
	for i := len(all) - 1; i >= 0; i-- {
		all[i] = heap.Pop(h).(DumpEntry)
	}
	// The heap pops oldest first; the catalogue reads newest first.
	sort.Slice(all, func(i, j int) bool {
		if !all[i].MTime.Equal(all[j].MTime) {
			return all[i].MTime.After(all[j].MTime)
		}
		return all[i].Name < all[j].Name
	})
	return all, st
}

// dumpCand is one dump file or sub-folder of a folder being scanned, with the
// modified time it is ordered by.
type dumpCand struct {
	name  string
	isDir bool
	size  int64
	mtime time.Time
}

// dumpHeap keeps the newest dumpMaxListed entries: a min-heap ordered oldest
// first (ties: the lexically LAST name is the weakest), so the root is always
// the entry to evict when a newer one arrives.
type dumpHeap []DumpEntry

func (h dumpHeap) Len() int { return len(h) }
func (h dumpHeap) Less(i, j int) bool {
	if !h[i].MTime.Equal(h[j].MTime) {
		return h[i].MTime.Before(h[j].MTime)
	}
	return h[i].Name > h[j].Name
}
func (h dumpHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *dumpHeap) Push(x any)   { *h = append(*h, x.(DumpEntry)) }
func (h *dumpHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
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
// scan is the legacy injection seam (entries only) so existing fakes keep
// working; when it is set it wins. scanStatus is the real scanner seam, which
// also reports the scan's truncation/unreadable status (W12-06).
type dumpsGroup struct {
	roots      []string
	scan       func(roots []string) []DumpEntry
	scanStatus func(roots []string) ([]DumpEntry, DumpScanStatus)
	lastScan   time.Time
	cached     []DumpEntry
	status     DumpScanStatus
}

func newDumpsGroup(GroupDeps) (Group, error) {
	return &dumpsGroup{roots: dumpRoots(), scanStatus: catalogueDumps}, nil
}

func (g *dumpsGroup) Name() string   { return "dumps" }
func (g *dumpsGroup) Optional() bool { return true }

// ScanStatus is the last scan's truncation/unreadable status. The sample wire
// shape (DumpsGroup) carries the entries only; this exposes the honesty of the
// scan to in-process consumers and tests (W12-06). An injected entries-only
// fake reports no status.
func (g *dumpsGroup) ScanStatus() DumpScanStatus { return g.status }

func (g *dumpsGroup) Collect(now time.Time, dt float64) (any, error) {
	if g.lastScan.IsZero() || now.Sub(g.lastScan) >= dumpsEvery || now.Before(g.lastScan) {
		switch {
		case g.scan != nil:
			g.cached, g.status = g.scan(g.roots), DumpScanStatus{}
		case g.scanStatus != nil:
			g.cached, g.status = g.scanStatus(g.roots)
		default:
			g.cached, g.status = nil, DumpScanStatus{}
		}
		g.lastScan = now
	}
	if len(g.cached) == 0 && !g.status.Truncated && g.status.Unreadable == 0 {
		return nil, nil // a complete scan found no dumps: group omitted, never 0
	}
	// An incomplete scan with nothing found must not read as "no dumps": the
	// coverage flags travel with the result so the consumer knows it is partial.
	return &DumpsGroup{Dumps: g.cached, Truncated: g.status.Truncated, Unreadable: g.status.Unreadable}, nil
}
