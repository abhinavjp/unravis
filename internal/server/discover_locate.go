package server

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"api-audit-explorer/internal/discover"
	"api-audit-explorer/internal/discover/localread"
)

// POST /api/discover/scans/{scan}/findings/{finding}/locate  {"path": "..."}
//
// A scan can know that an application writes log4net or NLog output without
// knowing where. The person types the folder (or one file) and this route checks
// it the way the scan checks its own findings: the path must be absolute, local
// and plain (no network share, no junction, no "..", no alias names), the files
// in it must have log names, and the importer must recognise a sample of them.
// Only then is the in-memory finding completed: its paths, importer and counts
// are set and it is marked verified. A path that fails changes nothing.
//
// The answer holds counts and a verdict. It never lists the folder, never echoes
// the typed path, and a failure is one reason from a closed set.

const (
	// locatesPerWindow caps locate calls per connectionTestWindow. A call reads
	// the file system, so it is paced like the other routes that touch the
	// machine; a person typing a few paths stays far under it.
	locatesPerWindow = 30
	// maxLocatePathBytes bounds the typed path before anything looks at it. A
	// Windows path is at most discover.MaxPathLen characters.
	maxLocatePathBytes = 1024
)

// locateBudget bounds the file system work of one call. A variable so a test can
// shorten it.
var locateBudget = 10 * time.Second

// Reasons of a path that was understood but did not verify. The answer is 200
// with verified false. The set is closed: the browser maps each to plain words.
const (
	locateNotFound    = "not-found"
	locateCannotOpen  = "cannot-open"
	locateEmptyFile   = "empty-file"
	locateWrongFormat = "wrong-format"
	locateNoLogs      = "no-log-files"
	locateTimedOut    = "timed-out"
)

// Reasons of a path that is refused outright. The answer is 400.
const (
	locateNotAbsolute = "path-not-absolute"
	locateNotLocal    = "path-not-local"
	locateUnsafe      = "path-unsafe"
)

var locateLimiters sync.Map // *handler -> *connectionTestLimiter

func (h *handler) locateLimiter() *connectionTestLimiter {
	if existing, ok := locateLimiters.Load(h); ok {
		return existing.(*connectionTestLimiter)
	}
	created, _ := locateLimiters.LoadOrStore(h, &connectionTestLimiter{limit: locatesPerWindow})
	return created.(*connectionTestLimiter)
}

type locateRequest struct {
	Path string `json:"path"`
}

type locateResponse struct {
	// Verified is true when the finding now has paths and an importer.
	Verified bool `json:"verified"`
	// Files is how many log files the path held (0 when it held none).
	Files int `json:"files"`
	// Reason is empty when verified, else one of the locate* reasons above.
	Reason string `json:"reason"`
	// Finding is the finding as it now stands, set only when verified.
	Finding *discover.Finding `json:"finding,omitempty"`
}

// locatePath matches /api/discover/scans/{scan}/findings/{finding}/locate and
// returns "scan/finding". The ids are checked by the handler.
func locatePath(path string) (string, bool) {
	rest, ok := strings.CutPrefix(path, "/api/discover/scans/")
	if !ok {
		return "", false
	}
	parts := strings.Split(rest, "/")
	if len(parts) != 4 || parts[0] == "" || parts[1] != "findings" || parts[2] == "" || parts[3] != "locate" {
		return "", false
	}
	return parts[0] + "/" + parts[2], true
}

// locatableKind reports whether a finding names log files, so a folder can
// complete it.
func locatableKind(k discover.Kind) bool {
	return k == discover.KindLogFile || k == discover.KindLogSet
}

// locateCandidates lists the importers to try, in order. A finding that already
// names an importer keeps it first; one with none (or one of the log4net/NLog
// family) may be completed by any of the family.
func locateCandidates(current string) []string {
	family := []string{"log4net-text", "log4net-xml", "nlog-text"}
	inFamily := current == ""
	for _, n := range family {
		if n == current {
			inFamily = true
		}
	}
	var out []string
	if current != "" {
		out = append(out, current)
	}
	if inFamily {
		for _, n := range family {
			if n != current {
				out = append(out, n)
			}
		}
	}
	return out
}

// POST /api/discover/scans/{scan}/findings/{finding}/locate
func (h *handler) locateFinding(w http.ResponseWriter, r *http.Request, arg string) {
	cfg, ok := h.discoverSupported(w)
	if !ok {
		return
	}
	var in locateRequest
	if !decodeJSON(w, r, &in) {
		return
	}
	scanID, findingID, _ := strings.Cut(arg, "/")
	rec, ok := h.scanFromPath(w, scanID)
	if !ok {
		return
	}
	if !findingIDPattern.MatchString(findingID) {
		writeCaseFailure(w, http.StatusNotFound, "finding-unknown", "That source was not found in the scan.")
		return
	}
	rec.mu.Lock()
	state := rec.state
	sf := rec.byID[findingID]
	var snap discover.Finding
	if sf != nil {
		snap = sf.f
	}
	rec.mu.Unlock()
	if state != scanDone {
		writeCaseFailure(w, http.StatusConflict, "scan-not-ready", "The scan has not finished.")
		return
	}
	if sf == nil {
		writeCaseFailure(w, http.StatusNotFound, "finding-unknown", "That source was not found in the scan.")
		return
	}
	if !locatableKind(snap.Kind) {
		writeCaseFailure(w, http.StatusConflict, "not-locatable", "A folder cannot be given for this source.")
		return
	}
	clean, code := cleanLocatePath(in.Path)
	if code != "" {
		writeCaseFailure(w, http.StatusBadRequest, code, locateRefusalText(code))
		return
	}
	if !h.locateLimiter().allow() {
		writeError(w, http.StatusTooManyRequests, "Too many folder checks in the last minute. Wait a minute, then check again.")
		return
	}

	env := cfg.Env
	env.Importers = h.registry
	out, ok := runWithin(r.Context(), locateBudget, func() locateOutcome { return locateWork(env, cfg.Opener, clean, snap.Importer) })
	switch {
	case !ok && r.Context().Err() != nil:
		return // the caller left
	case !ok:
		out = locateOutcome{reason: locateTimedOut}
	}
	if out.refusal != "" {
		writeCaseFailure(w, http.StatusBadRequest, out.refusal, locateRefusalText(out.refusal))
		return
	}
	if out.reason != "" {
		writeJSON(w, http.StatusOK, locateResponse{Verified: false, Files: out.files, Reason: out.reason}, 4096)
		return
	}

	rec.mu.Lock()
	applyLocated(&sf.f, out)
	updated := sf.f
	for i := range rec.scan.Findings {
		if rec.scan.Findings[i].ID == findingID {
			rec.scan.Findings[i] = updated
		}
	}
	sf.files = out.scanFiles
	rec.mu.Unlock()

	writeJSON(w, http.StatusOK, locateResponse{Verified: true, Files: out.files, Finding: &updated}, maxResponseBytes)
}

func locateRefusalText(code string) string {
	switch code {
	case locateNotAbsolute:
		return "Enter the full path, starting with a drive letter, for example the C or D drive."
	case locateNotLocal:
		return "Only folders on a drive in this computer can be used, not a network share."
	default:
		return "That path cannot be used."
	}
}

// cleanLocatePath checks the shape of the typed path. It returns the cleaned
// path, or a refusal code. The error text of the checks is never passed on.
func cleanLocatePath(raw string) (string, string) {
	if len(raw) > maxLocatePathBytes {
		return "", locateUnsafe
	}
	clean, err := discover.ValidLogPath(raw, "")
	if err == nil {
		return clean, ""
	}
	var pe *discover.PathError
	if errors.As(err, &pe) {
		switch pe.Code {
		case "relative", "relative-no-dir", "empty":
			return "", locateNotAbsolute
		case "unc", "prefix":
			return "", locateNotLocal
		}
	}
	return "", locateUnsafe
}

// locateOutcome is the result of the file system work. At most one of refusal and
// reason is set; with neither, the located fields are filled.
type locateOutcome struct {
	refusal string
	reason  string
	files   int

	importer  string
	paths     []string
	size      int64
	oldest    time.Time
	newest    time.Time
	lastName  string
	scanFiles []scanFile
}

type locateFile struct {
	path string
	info discover.FileInfo
}

// locateWork resolves the typed path, picks the log files and finds the importer
// that reads them. It touches the system only through env.FS and opener.
func locateWork(env discover.Env, opener localread.Opener, clean, current string) locateOutcome {
	files, out := locateFiles(env.FS, clean)
	if out != nil {
		return *out
	}
	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = f.path
	}
	var reason string
	imp := ""
	for _, name := range locateCandidates(current) {
		ok, key := discover.VerifyPaths(env, name, paths)
		if ok {
			imp = name
			break
		}
		if reason == "" {
			reason = locateReasonOf(key)
		}
	}
	if imp == "" {
		if reason == "" {
			reason = locateWrongFormat
		}
		return locateOutcome{reason: reason, files: len(files)}
	}

	res := locateOutcome{importer: imp, paths: paths, files: len(files)}
	res.oldest, res.newest = files[0].info.ModTime, files[0].info.ModTime
	for _, f := range files {
		res.size += f.info.Size
		if f.info.ModTime.Before(res.oldest) {
			res.oldest = f.info.ModTime
		}
		if f.info.ModTime.After(res.newest) {
			res.newest = f.info.ModTime
		}
	}
	res.lastName = discover.WinBase(files[len(files)-1].path)
	identified := 0
	for _, f := range files {
		sfile := scanFile{path: f.path, mod: f.info.ModTime}
		if opener != nil && identified < maxIdentified {
			identified++
			if info, err := localread.IdentifyWith(opener, f.path); err == nil {
				sfile.id, sfile.base, sfile.mod = info.ID, info.Base, info.ModTime
			}
		}
		res.scanFiles = append(res.scanFiles, sfile)
	}
	// A set of files is pulled oldest first, so a watermark never skips ahead.
	sort.SliceStable(res.scanFiles, func(a, b int) bool { return res.scanFiles[a].mod.Before(res.scanFiles[b].mod) })
	return res
}

// locateReasonOf maps the scan's reason wording keys to the locate reasons.
func locateReasonOf(key string) string {
	switch key {
	case "discover.cannotopen.shows":
		return locateCannotOpen
	case "discover.emptyfile.shows":
		return locateEmptyFile
	case "discover.verifytimeout.shows":
		return locateTimedOut
	default:
		return locateWrongFormat
	}
}

// locateFiles turns the typed path into the log files it names, oldest first. A
// folder is read one level deep and only files with log names are kept. It
// returns an outcome (a refusal or a reason) instead of files when the path
// cannot be used.
func locateFiles(fsys discover.FS, clean string) ([]locateFile, *locateOutcome) {
	if fsys == nil {
		return nil, &locateOutcome{reason: locateCannotOpen}
	}
	fail := func(err error) *locateOutcome {
		var pe *discover.PathError
		switch {
		case errors.As(err, &pe):
			// The system refused the path (for example, it is not on a fixed drive).
			return &locateOutcome{refusal: locateNotLocal}
		case discover.IsNotFound(err):
			return &locateOutcome{reason: locateNotFound}
		default:
			return &locateOutcome{reason: locateCannotOpen}
		}
	}
	canon, reparse, err := fsys.Final(clean)
	if err != nil {
		return nil, fail(err)
	}
	if reparse || !strings.EqualFold(canon, clean) {
		// A junction, a link or a different real name: the typed path is not where
		// the files are.
		return nil, &locateOutcome{refusal: locateUnsafe}
	}
	info, err := fsys.Lstat(clean)
	if err != nil {
		return nil, fail(err)
	}
	if info.Reparse {
		return nil, &locateOutcome{refusal: locateUnsafe}
	}

	var files []locateFile
	if info.IsDir {
		entries, err := fsys.ReadDir(clean)
		if err != nil && !errors.Is(err, discover.ErrTooManyEntries) {
			return nil, fail(err)
		}
		for _, e := range entries {
			if e.IsDir || e.Reparse || !discover.LogFileOK(e.Name) {
				continue
			}
			p := discover.WinJoin(clean, e.Name)
			if _, perr := discover.ValidLogPath(p, ""); perr != nil {
				continue
			}
			files = append(files, locateFile{path: p, info: e})
		}
	} else if discover.LogFileOK(discover.WinBase(clean)) {
		files = append(files, locateFile{path: clean, info: info})
	}
	if len(files) == 0 {
		return nil, &locateOutcome{reason: locateNoLogs}
	}
	sort.SliceStable(files, func(i, j int) bool {
		if !files[i].info.ModTime.Equal(files[j].info.ModTime) {
			return files[i].info.ModTime.Before(files[j].info.ModTime)
		}
		return strings.ToLower(files[i].path) < strings.ToLower(files[j].path)
	})
	return files, nil
}

// applyLocated completes a finding with a verified location.
func applyLocated(f *discover.Finding, out locateOutcome) {
	f.SetPaths(out.paths...)
	f.Importer = out.importer
	f.Verified = true
	f.Unreadable = ""
	f.Files = out.files
	f.SizeBytes = out.size
	f.Oldest, f.Newest = out.oldest, out.newest
	f.Cost = discover.CostForSize(out.size)
	f.Kind = discover.KindLogFile
	detail := out.lastName
	if out.files > 1 {
		f.Kind = discover.KindLogSet
		detail += " and " + strconv.Itoa(out.files-1) + " more"
	}
	f.SetSites(f.Sites, detail)
	f.Suggest = discover.SuggestNames(out.paths)
}

// runWithin runs fn and waits at most d for it. When the wait ends first the
// work is abandoned (its late answer is dropped) and ok is false.
func runWithin[T any](ctx context.Context, d time.Duration, fn func() T) (out T, ok bool) {
	done := make(chan T, 1)
	go func() {
		defer func() { _ = recover() }() // a panic leaves done empty and the wait times out
		done <- fn()
	}()
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case out = <-done:
		return out, true
	case <-timer.C:
	case <-ctx.Done():
	}
	return out, false
}
