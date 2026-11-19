package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"api-audit-explorer/internal/blackbox"
)

// GET /api/blackbox/this-pc/folders
//
// Finds folders on this PC's fixed drives that hold Blackbox recordings, newest
// first, for the source wizard. It reads folder names and file names only, never
// file content, never follows a link, and is bounded: a few hundred names per
// drive root, two levels deep, and a time limit. The client cancelling the
// request stops it. The recorder service's own folder (when installed) is
// always offered first.

const (
	bbFoldersBudget      = 8 * time.Second
	bbFoldersRootEntries = 600  // names read in one drive root
	bbFoldersDirEntries  = 5000 // names read in one candidate folder
	bbFoldersSubdirs     = 40   // sub folders tried when a candidate holds none
	bbFoldersMax         = 12   // candidates returned
)

// bbFolderCandidate is one folder that holds Blackbox recordings.
type bbFolderCandidate struct {
	Path        string     `json:"path"`
	Drive       string     `json:"drive"`
	Recordings  int        `json:"recordings"`
	Newest      *time.Time `json:"newest,omitempty"`
	FromService bool       `json:"fromService,omitempty"`
	Recommended bool       `json:"recommended,omitempty"`
}

type bbFoldersAnswer struct {
	Folders []bbFolderCandidate `json:"folders"`
	// Complete is false when the time limit or a cancel cut the search short.
	Complete bool `json:"complete"`
}

// bbScanRoots lists the roots to search: the fixed drives. Replaceable by tests.
var bbScanRoots = func() []string {
	var roots []string
	for _, d := range bbDrivesRead() {
		if d.Type == "fixed" && len(d.Letter) == 1 {
			roots = append(roots, d.Letter+":"+string(os.PathSeparator))
		}
	}
	return roots
}

func (h *handler) bbFolders(w http.ResponseWriter, r *http.Request) {
	if !h.bbReady(w) {
		return
	}
	service := ""
	if found, err := h.bb.scan(); err == nil && found != nil && found.Installed {
		service = found.Folder
	}
	ctx, cancel := context.WithTimeout(r.Context(), bbFoldersBudget)
	defer cancel()
	folders, complete := scanBlackboxFolders(ctx, bbScanRoots(), service)
	writeJSON(w, http.StatusOK, bbFoldersAnswer{Folders: folders, Complete: complete}, 64<<10)
}

// scanBlackboxFolders searches roots (and the service folder) for recording
// folders. complete is false when ctx ended first.
func scanBlackboxFolders(ctx context.Context, roots []string, serviceFolder string) ([]bbFolderCandidate, bool) {
	out := []bbFolderCandidate{}
	seen := map[string]bool{}
	add := func(dir string, fromService bool) {
		key := strings.ToLower(filepath.Clean(dir))
		if seen[key] {
			return
		}
		seen[key] = true
		if c, ok := inspectBlackboxFolder(dir); ok {
			c.FromService = fromService
			out = append(out, c)
		}
	}
	if serviceFolder != "" && filepath.IsAbs(serviceFolder) {
		add(serviceFolder, true)
	}
rootLoop:
	for _, root := range roots {
		if ctx.Err() != nil {
			break
		}
		ents, _ := readDirLimited(root, bbFoldersRootEntries)
		for _, e := range ents {
			if ctx.Err() != nil {
				break rootLoop
			}
			if !e.IsDir() || e.Type()&os.ModeSymlink != 0 || !looksLikeBlackboxName(e.Name()) {
				continue
			}
			dir := filepath.Join(root, e.Name())
			before := len(out)
			add(dir, false)
			if len(out) > before {
				continue
			}
			subs, _ := readDirLimited(dir, bbFoldersSubdirs)
			for _, s := range subs {
				if ctx.Err() != nil {
					break rootLoop
				}
				if s.IsDir() && s.Type()&os.ModeSymlink == 0 {
					add(filepath.Join(dir, s.Name()), false)
				}
			}
		}
	}
	complete := ctx.Err() == nil
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].Newest, out[j].Newest
		switch {
		case a == nil && b == nil:
			return out[i].Path < out[j].Path
		case a == nil:
			return false
		case b == nil:
			return true
		}
		return a.After(*b)
	})
	if len(out) > bbFoldersMax {
		out = out[:bbFoldersMax]
	}
	// The service's folder is what the recorder writes now, so it leads; else the newest.
	for i := range out {
		if out[i].FromService {
			out[0], out[i] = out[i], out[0]
			break
		}
	}
	if len(out) > 0 {
		out[0].Recommended = true
	}
	return out, complete
}

func looksLikeBlackboxName(name string) bool {
	n := strings.ToLower(name)
	return strings.Contains(n, "blackbox") || strings.Contains(n, "unravis")
}

// inspectBlackboxFolder counts the hourly recording files in dir and finds the
// newest one. ok is false when the folder holds none.
func inspectBlackboxFolder(dir string) (bbFolderCandidate, bool) {
	ents, _ := readDirLimited(dir, bbFoldersDirEntries)
	c := bbFolderCandidate{Path: dir}
	if v := filepath.VolumeName(dir); len(v) >= 1 {
		c.Drive = strings.ToUpper(v[:1])
	}
	var newest time.Time
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		if h, ok := blackbox.HourOfName(e.Name()); ok {
			c.Recordings++
			if h.After(newest) {
				newest = h
			}
		}
	}
	if c.Recordings == 0 {
		return c, false
	}
	n := newest
	c.Newest = &n
	return c, true
}

// readDirLimited lists up to limit entries of dir and never fails the caller:
// an unreadable folder is an empty one.
func readDirLimited(dir string, limit int) ([]os.DirEntry, error) {
	f, err := os.Open(dir)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	ents, err := f.ReadDir(limit)
	return ents, err
}
