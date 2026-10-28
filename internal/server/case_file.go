package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/discover"
	"api-audit-explorer/internal/discover/localread"
	"api-audit-explorer/internal/masterkey"
	"api-audit-explorer/internal/sealedfile"
)

// Case files: POST /api/cases/{id}/export and POST /api/cases/restore.
//
// Both routes sit under /api/cases, so the master-key lock gate answers 423
// before they run while the key is locked. The passphrase is the master key
// passphrase of the PC the file is exported from; it travels in the request body
// only, is decoded straight into a []byte (no Go string of it), is never logged,
// never echoed, and is zeroed before the handler returns.

const (
	maxCaseFileBody = 16 << 10
	maxCaseFilePath = 520
)

const (
	caseFileExportFailed  = "The case could not be exported."
	caseFileRestoreFailed = "The case could not be restored."
	caseFileBadPathText   = "That location cannot be used. Give a full path on a local drive."
)

// caseFileDPAPI is how a case still sealed with DPAPI is opened for export. A
// variable so tests can stand in for DPAPI.
var caseFileDPAPI = sealedfile.CurrentUser

// caseFileParams are the Argon2id costs for a new case file's header key. The
// zero value means masterkey.DefaultParams; tests lower it.
var caseFileParams masterkey.Params

// caseFileState is the per-handler state of the case-file routes.
type caseFileState struct {
	busy       atomic.Bool // one export or restore at a time: each streams a whole database
	restoreTh  throttle    // restore judges a case file's own passphrase: a different target than the master key
	exportOnce sync.Once
	jobs       fileJobs    // the archive and verify jobs (P3)
	verified   verifiedSet // files that passed a test restore, the gate of Free (P3)
	freeing    freeingSet  // cases a Free is waiting on: new readers get M19 (P3)
}

var caseFileStates sync.Map // *handler -> *caseFileState

func (h *handler) caseFiles() *caseFileState {
	if v, ok := caseFileStates.Load(h); ok {
		return v.(*caseFileState)
	}
	v, _ := caseFileStates.LoadOrStore(h, &caseFileState{})
	return v.(*caseFileState)
}

// caseFileRoot is what the case store offers beyond casestore.Store: the folder
// the cases live in. The SQLite store has it; the in-memory store has no files
// to export.
type caseFileRoot interface{ Root() string }

// restoreAdapter lets the generic store serve casestore.RestoreStore.
type restoreAdapter struct {
	casestore.Store
	root string
}

func (a restoreAdapter) Root() string { return a.root }

// Create makes the case Closed from the start: every restore ends Closed, and a
// crash anywhere inside the restore must not leave an Open case behind (spec 7.4).
// A case that cannot be closed is erased and the restore fails.
func (a restoreAdapter) Create(ctx context.Context, title string) (casestore.CaseInfo, error) {
	info, err := a.Store.Create(ctx, title)
	if err != nil {
		return info, err
	}
	if err := a.Store.CloseCase(ctx, info.ID); err != nil {
		_ = a.Store.Erase(context.WithoutCancel(ctx), info.ID)
		return casestore.CaseInfo{}, err
	}
	return info, nil
}

// caseFileBody is a decoded request body. pass never becomes a Go string.
type caseFileBody struct {
	raw    []byte
	fields map[string]json.RawMessage
	pass   []byte
	path   string
}

func (b *caseFileBody) wipe() {
	clear(b.raw)
	for _, v := range b.fields {
		clear(v)
	}
	clear(b.pass)
}

// readCaseFileBody reads {"passphrase":"...","path":"..."}. It answers errors
// itself and returns nil; no error text echoes any part of the body.
func (h *handler) readCaseFileBody(w http.ResponseWriter, r *http.Request) *caseFileBody {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		writeCaseFailure(w, http.StatusUnsupportedMediaType, "bad-content-type", "Content-Type must be application/json.")
		return nil
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxCaseFileBody)
	raw, err := io.ReadAll(r.Body)
	b := &caseFileBody{raw: raw}
	if err != nil {
		b.wipe()
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeCaseFailure(w, http.StatusRequestEntityTooLarge, "too-large", "The request body is too large.")
		} else {
			writeCaseFailure(w, http.StatusBadRequest, "bad-request", "The request body is invalid.")
		}
		return nil
	}
	bad := func() *caseFileBody {
		b.wipe()
		writeCaseFailure(w, http.StatusBadRequest, "bad-request", "The request body is invalid.")
		return nil
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return bad()
	}
	b.fields = fields
	for name := range fields {
		if name != "passphrase" && name != "path" {
			return bad()
		}
	}
	if v, ok := fields["passphrase"]; ok {
		if b.pass, ok = unquoteSecret(v); !ok {
			return bad()
		}
	}
	if v, ok := fields["path"]; ok {
		if json.Unmarshal(v, &b.path) != nil {
			return bad()
		}
		if len(b.path) > maxCaseFilePath {
			b.wipe()
			writeCaseFailure(w, http.StatusBadRequest, "path-too-long", "That location is too long. Use a shorter path.")
			return nil
		}
	}
	return b
}

// caseFilePrecheck answers the failures every case-file request shares and
// returns the case folder root.
func (h *handler) caseFilePrecheck(w http.ResponseWriter) (root string, ok bool) {
	if h.masterKeyUnavailable(w) {
		return "", false
	}
	if !h.casesAvailable(w) {
		return "", false
	}
	switch h.mk.State() {
	case MasterKeyNone:
		h.masterKeyFail(w, http.StatusConflict, "no-master-key", "Set a master key first.")
		return "", false
	case MasterKeyLocked, MasterKeyDamaged:
		writeLocked(w)
		return "", false
	}
	cr, isRoot := h.cases.(caseFileRoot)
	if !isRoot || cr.Root() == "" {
		writeCaseFailure(w, http.StatusServiceUnavailable, "case-file-unavailable", "Case files are unavailable.")
		return "", false
	}
	return cr.Root(), true
}

// caseFileLookup resolves protectors for reading a case's key file: the master
// key from the session, DPAPI for a case that has not been re-wrapped yet.
func (h *handler) caseFileLookup() casestore.ProtectorLookup {
	return func(kind string) (sealedfile.Protector, error) {
		switch kind {
		case casestore.KindMasterKey:
			return h.mk.Lookup(kind)
		case casestore.KindDPAPI:
			p, err := caseFileDPAPI()
			if err != nil || p == nil {
				return nil, casestore.ErrLocked
			}
			return p, nil
		}
		return nil, casestore.ErrLocked
	}
}

func volumeRoot(p string) string {
	if v := filepath.VolumeName(p); v != "" {
		return v + `\`
	}
	return string(filepath.Separator)
}

// checkCasePath validates a path the user typed and returns its cleaned form.
// It applies the same shape rules as file imports (local drive, no UNC, no "..",
// no alternate stream, no 8.3 alias), then refuses any reparse point on the way.
func checkCasePath(p string) (string, bool) {
	clean, err := discover.ValidateAbsPath(p)
	if err != nil || localread.CheckShape(clean) != nil {
		return "", false
	}
	if casestore.RejectReparse(volumeRoot(clean), clean) != nil {
		return "", false
	}
	return clean, true
}

// insideRoot reports whether p is the cases folder or below it.
func insideRoot(p, root string) bool {
	p, root = strings.ToLower(filepath.Clean(p)), strings.ToLower(filepath.Clean(root))
	return p == root || strings.HasPrefix(p, root+`\`)
}

/* --------------------------------------------------------------- export */

type caseFileExportReply struct {
	Path  string `json:"path"`
	Name  string `json:"name"`
	Bytes int64  `json:"bytes"`
}

func (h *handler) exportCaseFile(w http.ResponseWriter, r *http.Request, raw string) {
	id, ok := caseIDOf(w, raw)
	if !ok {
		return
	}
	root, ok := h.caseFilePrecheck(w)
	if !ok {
		return
	}
	b := h.readCaseFileBody(w, r)
	if b == nil {
		return
	}
	defer b.wipe()
	clean, shapeOK := checkCasePath(b.path)
	if !shapeOK {
		writeCaseFailure(w, http.StatusBadRequest, "bad-path", caseFileBadPathText)
		return
	}
	st := h.caseFiles()
	if wait := h.mk.throttleWait(); wait > 0 {
		h.masterKeyThrottled(w, wait)
		return
	}
	if !st.busy.CompareAndSwap(false, true) {
		writeCaseFailure(w, http.StatusConflict, "busy", caseFileBusyText)
		return
	}
	defer st.busy.Store(false)

	info, found := h.findCase(r.Context(), w, id)
	if !found {
		return
	}
	release, ok := h.gates.acquireRead(id)
	if !ok {
		// An erase is waiting for the readers in flight (M5), not a missing case.
		writeStoreError(w, casestore.ErrErasing)
		return
	}
	defer release()

	// The typed passphrase must be this PC's master key passphrase: a file sealed
	// with anything else could never be restored by the key the user holds.
	// The check goes through the session's one passphrase throttle (shared with
	// unlock), under its operation lock.
	wait, err := h.mk.VerifyPassphrase(b.pass)
	switch {
	case wait > 0:
		h.masterKeyThrottled(w, wait)
		return
	case errors.Is(err, masterkey.ErrWrongKey):
		writeCaseFailure(w, http.StatusUnauthorized, "wrong-key", "The passphrase is wrong.")
		return
	case err != nil:
		writeCaseFailure(w, http.StatusInternalServerError, "export-failed", caseFileExportFailed)
		return
	}

	target, ok := h.exportTarget(w, clean, root, id)
	if !ok {
		return
	}
	n, err := casestore.ExportCaseFile(r.Context(), root, id, info.Title, info.Created, info.Closed,
		h.caseFileLookupFor(id), target, b.pass, caseFileParams)
	if err != nil {
		writeExportError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, caseFileExportReply{Path: target, Name: filepath.Base(target), Bytes: n}, 4096)
}

// findCase looks the case up in the store's list, so the title and dates in the
// file are the store's own.
func (h *handler) findCase(ctx context.Context, w http.ResponseWriter, id casestore.CaseID) (casestore.CaseInfo, bool) {
	list, err := h.cases.List(ctx)
	if err != nil {
		writeStoreError(w, err)
		return casestore.CaseInfo{}, false
	}
	for _, c := range list {
		if c.ID == id {
			return c, true
		}
	}
	// The store hides a case whose erase was marked: that is M5, not "not found".
	if errors.Is(h.openFailed(ctx, id, casestore.ErrNotFound), casestore.ErrErasing) {
		writeStoreError(w, casestore.ErrErasing)
		return casestore.CaseInfo{}, false
	}
	writeCaseFailure(w, http.StatusNotFound, "case-not-found", "The case was not found.")
	return casestore.CaseInfo{}, false
}

// exportTarget turns the requested path into the file to write: either a full
// path ending in the case-file extension, or an existing folder, in which case
// the file is <case id>.unravis-case inside it. It never overwrites and never
// writes inside the cases folder.
func (h *handler) exportTarget(w http.ResponseWriter, clean, root string, id casestore.CaseID) (string, bool) {
	bad := func() (string, bool) {
		writeCaseFailure(w, http.StatusBadRequest, "bad-path", caseFileBadPathText)
		return "", false
	}
	target := clean
	if !strings.EqualFold(filepath.Ext(clean), casestore.CaseFileExt) {
		fi, err := os.Lstat(clean)
		if err != nil || !fi.IsDir() {
			return bad()
		}
		target = filepath.Join(clean, string(id)+casestore.CaseFileExt)
	}
	parent, err := os.Lstat(filepath.Dir(target))
	if err != nil || !parent.IsDir() || insideRoot(target, root) {
		return bad()
	}
	if _, err := os.Lstat(target); err == nil {
		writeCaseFailure(w, http.StatusConflict, "file-exists", "A file with that name already exists. Choose another name or folder.")
		return "", false
	}
	return target, true
}

func writeExportError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, casestore.ErrCaseFileExists):
		writeCaseFailure(w, http.StatusConflict, "file-exists", "A file with that name already exists. Choose another name or folder.")
	case errors.Is(err, casestore.ErrCaseFileChanged):
		writeCaseFailure(w, http.StatusConflict, "case-changed", "The case changed while it was being exported. Try again.")
	case errors.Is(err, casestore.ErrCaseFileBusy):
		writeCaseFailure(w, http.StatusConflict, "case-busy", "The case is busy. Try again in a moment.")
	case errors.Is(err, casestore.ErrNotFound), errors.Is(err, casestore.ErrBadID), errors.Is(err, casestore.ErrLocked), errors.Is(err, casestore.ErrCaseLocked),
		errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		writeStoreError(w, err)
	default:
		writeCaseFailure(w, http.StatusInternalServerError, "export-failed", caseFileExportFailed)
	}
}

/* -------------------------------------------------------------- restore */

func (h *handler) restoreCaseFile(w http.ResponseWriter, r *http.Request) {
	root, ok := h.caseFilePrecheck(w)
	if !ok {
		return
	}
	if _, err := h.mk.Keyring(); err != nil {
		writeLocked(w)
		return
	}
	b := h.readCaseFileBody(w, r)
	if b == nil {
		return
	}
	defer b.wipe()
	src, ok := checkCasePath(b.path)
	if !ok || strings.EqualFold(filepath.Ext(src), casestore.CaseFileExt) == false {
		writeCaseFailure(w, http.StatusBadRequest, "bad-path", caseFileBadPathText)
		return
	}
	st := h.caseFiles()
	if wait := st.restoreTh.wait(); wait > 0 {
		h.masterKeyThrottled(w, wait)
		return
	}
	if !st.busy.CompareAndSwap(false, true) {
		writeCaseFailure(w, http.StatusConflict, "busy", caseFileBusyText)
		return
	}
	defer st.busy.Store(false)

	notOpened := func() {
		st.restoreTh.fail()
		writeCaseFailure(w, http.StatusUnprocessableEntity, "bad-case-file", casestore.CaseFileMessage)
	}
	if len(b.pass) == 0 || len(b.pass) > maxPassphraseBytes {
		notOpened()
		return
	}
	info, from, err := casestore.RestoreCaseFileFull(r.Context(), restoreAdapter{Store: h.cases, root: root}, h.mk.Lookup, src, casestore.PassphraseKey(b.pass))
	switch {
	case err == nil:
		st.restoreTh.reset()
		if from.OwnKey {
			if err := h.keepOwnKey(r.Context(), info.ID, b.pass); err != nil {
				writeCaseFailure(w, http.StatusInternalServerError, "restore-failed", caseFileRestoreFailed)
				return
			}
		}
		made := time.Now()
		if fi, serr := os.Stat(src); serr == nil {
			made = fi.ModTime()
		}
		writeJSON(w, http.StatusCreated, toCaseDTO(h.afterRestore(r.Context(), info, from, made)), maxResponseBytes)
	case errors.Is(err, casestore.ErrCaseFile):
		notOpened()
	case errors.Is(err, casestore.ErrLocked), errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		writeStoreError(w, err)
	default:
		writeCaseFailure(w, http.StatusInternalServerError, "restore-failed", caseFileRestoreFailed)
	}
}
