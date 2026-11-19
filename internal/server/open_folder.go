package server

import (
	"net/http"
	"os"
	"strings"
	"sync"

	"api-audit-explorer/internal/discover"
)

// POST /api/local/open-folder  {"path": "C:\..."}  or  {"scan": "...", "finding": "..."}
//
// Opens a folder in Windows Explorer on this computer, for the "Open folder"
// button. It runs a program, so it is strict:
//   - it is a route under /api, so the loopback host check and the one-time
//     session token already apply;
//   - the folder is either named by a scan finding (the path stays server-side)
//     or a typed local drive path that passes discover.ValidateAbsPath;
//   - the folder must exist, must be a directory and must not be a reparse point;
//   - Explorer is started with an argument list, never a shell string, and the
//     path always begins with a drive letter, so it can never read as an option.
//
// Answers carry fixed reasons only: no path, no OS error text.

const (
	openFolderOK          = "opened"
	openFolderNotFound    = "not-found"
	openFolderNotAFolder  = "not-a-folder"
	openFolderUnsafe      = "path-unsafe"
	openFolderNotLocal    = "path-not-local"
	openFolderUnsupported = "not-supported"
	openFolderFailed      = "cannot-open"
	openFoldersPerWindow  = 30
)

type openFolderRequest struct {
	Path    string `json:"path"`
	Scan    string `json:"scan"`
	Finding string `json:"finding"`
}

type openFolderResponse struct {
	Opened bool   `json:"opened"`
	Reason string `json:"reason,omitempty"`
}

// openFolderFn starts Explorer on a validated folder. A variable so tests never
// launch a program.
var openFolderFn = openFolderInExplorer

var openFolderLimiters sync.Map // *handler -> *connectionTestLimiter

func (h *handler) openFolderLimiter() *connectionTestLimiter {
	if existing, ok := openFolderLimiters.Load(h); ok {
		return existing.(*connectionTestLimiter)
	}
	created, _ := openFolderLimiters.LoadOrStore(h, &connectionTestLimiter{limit: openFoldersPerWindow})
	return created.(*connectionTestLimiter)
}

func (h *handler) openFolder(w http.ResponseWriter, r *http.Request) {
	var in openFolderRequest
	if !decodeJSON(w, r, &in) {
		return
	}
	raw := in.Path
	switch {
	case in.Scan != "" || in.Finding != "":
		if in.Path != "" {
			writeCaseFailure(w, http.StatusBadRequest, openFolderUnsafe, "Give either a folder or a scanned source, not both.")
			return
		}
		p, status, reason := h.scanFolder(in.Scan, in.Finding)
		if reason != "" {
			writeCaseFailure(w, status, reason, openFolderText(reason))
			return
		}
		raw = p
	case raw == "":
		writeCaseFailure(w, http.StatusBadRequest, openFolderUnsafe, openFolderText(openFolderUnsafe))
		return
	}
	dir, reason := checkFolder(raw)
	if reason != "" {
		writeCaseFailure(w, http.StatusBadRequest, reason, openFolderText(reason))
		return
	}
	if !h.openFolderLimiter().allow() {
		writeError(w, http.StatusTooManyRequests, "Too many folders opened in the last minute. Wait a minute, then try again.")
		return
	}
	if err := openFolderFn(dir); err != nil {
		reason := openFolderFailed
		if err == errOpenFolderUnsupported {
			reason = openFolderUnsupported
		}
		writeCaseFailure(w, http.StatusConflict, reason, openFolderText(reason))
		return
	}
	writeJSON(w, http.StatusOK, openFolderResponse{Opened: true}, 1<<10)
}

// scanFolder resolves a finding to the folder of its first path. The path never
// leaves the scan store; the answer is only a reason when it cannot.
func (h *handler) scanFolder(scanID, findingID string) (string, int, string) {
	if !scanIDPattern.MatchString(scanID) || !findingIDPattern.MatchString(findingID) {
		return "", http.StatusNotFound, openFolderNotFound
	}
	rec, how := h.disc.lookup(scanID)
	if how != scanFound {
		return "", http.StatusNotFound, openFolderNotFound
	}
	rec.mu.Lock()
	sf := rec.byID[findingID]
	var paths []string
	if sf != nil {
		paths = sf.f.Paths()
	}
	rec.mu.Unlock()
	if len(paths) == 0 {
		return "", http.StatusNotFound, openFolderNotFound
	}
	return paths[0], 0, ""
}

// checkFolder validates a path and returns the folder to open: the path itself
// when it is a directory, else its parent when it is a file. An empty reason
// means it is safe to open.
func checkFolder(raw string) (string, string) {
	if len(raw) > maxLocatePathBytes {
		return "", openFolderUnsafe
	}
	clean, err := discover.ValidateAbsPath(raw)
	if err != nil {
		if strings.HasPrefix(raw, `\\`) || strings.HasPrefix(raw, "//") {
			return "", openFolderNotLocal
		}
		return "", openFolderUnsafe
	}
	fi, err := os.Lstat(clean)
	if err != nil {
		return "", openFolderNotFound
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return "", openFolderUnsafe
	}
	if !fi.IsDir() {
		parent := parentDir(clean)
		pi, perr := os.Lstat(parent)
		if perr != nil || !pi.IsDir() || pi.Mode()&os.ModeSymlink != 0 {
			return "", openFolderNotAFolder
		}
		return parent, ""
	}
	return clean, ""
}

func parentDir(p string) string {
	i := strings.LastIndexByte(p, 0x5c)
	if i < 3 {
		return p[:3]
	}
	return p[:i]
}

func openFolderText(reason string) string {
	switch reason {
	case openFolderNotFound:
		return "That folder was not found on this computer."
	case openFolderNotAFolder:
		return "That path is not a folder."
	case openFolderNotLocal:
		return "Only folders on this computer can be opened."
	case openFolderUnsupported:
		return "Opening a folder is only available in the Windows app."
	case openFolderFailed:
		return "Windows could not open that folder."
	default:
		return "That folder path cannot be opened. Use a full path that starts with a drive letter."
	}
}
