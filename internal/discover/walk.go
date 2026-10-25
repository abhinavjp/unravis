package discover

import (
	"context"
	"errors"
	"sort"
	"strings"
)

// Limits of one directory walk (spec 2.2).
const (
	WalkMaxDepth   = 3
	WalkMaxEntries = 5000
)

// Walked is one file a walk kept.
type Walked struct {
	Path string // absolute, backslashes, as listed under the root
	Dir  string // the folder it was listed in
	Info FileInfo
}

// WalkResult is what a walk found. Everything here is metadata from ReadDir.
type WalkResult struct {
	Files []Walked
	// Reparse lists the folders and files that were skipped because they are
	// reparse points (junctions, symlinks). A walk never follows or reads them.
	Reparse []string
	// Denied is true when a folder below the root could not be listed because
	// access was denied. The root's own error is returned by Walk instead.
	Denied bool
	// Truncated is true when the entry cap or a folder's own cap was hit, so the
	// list may be partial.
	Truncated bool
}

// Walk lists files below root, breadth first, down to maxDepth levels of
// sub-folders (0 lists root alone) and at most WalkMaxEntries entries in all. It
// reads through FS.ReadDir only, skips every reparse point without following it,
// and keeps a file when keep returns true for it. A nil keep keeps every file.
//
// root must be an absolute path that already passed ValidateAbsPath. The error is
// the root listing's own: access denied, not found or too large. A folder below
// the root that fails sets Denied or Truncated or is skipped, so one bad folder
// does not hide its siblings. Walk checks ctx between folders.
func Walk(ctx context.Context, fsys FS, root string, maxDepth int, keep func(FileInfo) bool) (WalkResult, error) {
	var res WalkResult
	if maxDepth > WalkMaxDepth {
		maxDepth = WalkMaxDepth
	}
	if maxDepth < 0 {
		maxDepth = 0
	}
	type item struct {
		dir   string
		depth int
	}
	queue := []item{{root, 0}}
	budget := WalkMaxEntries
	for len(queue) > 0 {
		if err := ctx.Err(); err != nil {
			res.Truncated = true
			return res, nil
		}
		cur := queue[0]
		queue = queue[1:]
		entries, err := fsys.ReadDir(cur.dir)
		switch {
		case errors.Is(err, ErrTooManyEntries):
			res.Truncated = true
		case err != nil:
			if cur.depth == 0 {
				return res, err
			}
			if IsAccessDenied(err) {
				res.Denied = true
			}
			continue
		}
		for _, fi := range entries {
			if budget <= 0 {
				res.Truncated = true
				return res, nil
			}
			budget--
			child := WinJoin(cur.dir, fi.Name)
			if _, perr := ValidateAbsPath(child); perr != nil || fi.Name == "" {
				continue
			}
			switch {
			case fi.Reparse:
				res.Reparse = append(res.Reparse, child)
			case fi.IsDir:
				if cur.depth < maxDepth {
					queue = append(queue, item{child, cur.depth + 1})
				}
			case keep == nil || keep(fi):
				res.Files = append(res.Files, Walked{Path: child, Dir: cur.dir, Info: fi})
			}
		}
	}
	return res, nil
}

// SortOldestFirst orders files by modification time, then by name, ignoring case.
func SortOldestFirst(files []Walked) {
	sort.SliceStable(files, func(i, j int) bool {
		a, b := files[i].Info, files[j].Info
		if !a.ModTime.Equal(b.ModTime) {
			return a.ModTime.Before(b.ModTime)
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
}
