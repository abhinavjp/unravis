package server

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"api-audit-explorer/internal/casestore"
)

// Archive routes (P3, spec v7 section 7):
//
//	POST   /api/cases/{id}/archive   start the job; body {"path": folder, "passphrase": typed (no master key only)}
//	GET    /api/cases/{id}/archive   the job as it stands (percent, phase, result)
//	DELETE /api/cases/{id}/archive   Cancel
//
// The start answers 202 with the job; the page polls the GET. One job at a time
// runs, sharing the busy lock with Export, Verify and Restore.

// archiveWait is how long Cancel waits for the job to give the case back.
const archiveWait = 2 * time.Minute

// archivePrecheck answers the failures every archive-family request shares. A
// missing master key is allowed here (a file may then be sealed with a typed
// passphrase); a locked or damaged one is not.
func (h *handler) archivePrecheck(w http.ResponseWriter) (root string, ar casestore.Archiver, ok bool) {
	if h.masterKeyUnavailable(w) || !h.casesAvailable(w) {
		return "", nil, false
	}
	switch h.mk.State() {
	case MasterKeyLocked, MasterKeyDamaged:
		writeLocked(w)
		return "", nil, false
	}
	cr, isRoot := h.cases.(caseFileRoot)
	a, isArch := h.cases.(casestore.Archiver)
	if !isRoot || cr.Root() == "" || !isArch {
		writeCaseFailure(w, http.StatusServiceUnavailable, "case-file-unavailable", "Case files are unavailable.")
		return "", nil, false
	}
	return cr.Root(), a, true
}

// caseDBPath is the working database of a case.
func caseDBPath(root string, id casestore.CaseID) string {
	return filepath.Join(root, string(id), "case.db")
}

// archiveMinRoom is the least free space an archive asks for.
const archiveMinRoom = 1 << 20

// caseDBBytes is the size of the case database and its journal, the number the
// 8 GiB limit and the free-space check use.
func caseDBBytes(root string, id casestore.CaseID) int64 {
	p := caseDBPath(root, id)
	var n int64
	for _, s := range []string{"", "-wal"} {
		if fi, err := os.Stat(p + s); err == nil {
			n += fi.Size()
		}
	}
	return n
}

// ownFolders are the folders an archive may never be written into: the case
// folders, the folder they sit in (the application folder), and the folder with
// the master key file, the saved connections and the history (the config folder,
// which AUDIT_CONFIG_DIR moves). A case file there would sit among the key files.
func (h *handler) ownFolders(root string) []string {
	own := []string{root}
	if filepath.Base(root) == "cases" {
		own = append(own, filepath.Dir(root))
	}
	if h.mk != nil && h.mk.dir != "" {
		own = append(own, h.mk.dir)
	}
	return own
}

// shortCode is the eight-character code a person reads and types for a case.
func shortCode(id casestore.CaseID) string {
	s := string(id)
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

func (h *handler) archiveCase(w http.ResponseWriter, r *http.Request, raw string) {
	id, ok := caseIDOf(w, raw)
	if !ok {
		return
	}
	root, ar, ok := h.archivePrecheck(w)
	if !ok {
		return
	}
	believed, ok := believedState(w, r)
	if !ok {
		return
	}
	b := h.readCaseFileBody(w, r)
	if b == nil {
		return
	}
	defer b.wipe()

	folder, err := realArchiveEnv().resolveArchiveFolder(b.path, h.ownFolders(root)...)
	if err != nil {
		writeCaseFailure(w, http.StatusBadRequest, "bad-folder", archiveFolderText)
		return
	}

	release, err := h.gates.acquire(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	defer release()
	// The belief first: a second press while the first runs is M3, not "busy".
	if err := h.guard(r.Context(), id, casestore.ActionArchive, believed); err != nil {
		writeStoreError(w, err)
		return
	}
	st := h.caseFiles()
	if !st.busy.CompareAndSwap(false, true) {
		writeCaseFailure(w, http.StatusConflict, "busy", caseFileBusyText)
		return
	}
	started := false
	defer func() {
		if !started {
			st.busy.Store(false)
		}
	}()

	info, err := h.infoOf(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if h.jobs.activeImport(id) {
		writeCaseFailure(w, http.StatusConflict, "import-running", importRunningText)
		return
	}
	dbBytes := caseDBBytes(root, id)
	if dbBytes > archiveMaxDB {
		writeCaseFailure(w, http.StatusUnprocessableEntity, "too-large", archiveTooLargeText)
		return
	}

	now := archiveNow()
	name := archiveFileName(info.Title, shortCode(id), now, func(n string) bool {
		for _, cand := range []string{n, n + ".part"} {
			if _, err := os.Lstat(filepath.Join(folder, cand)); err == nil {
				return true
			}
		}
		return false
	})
	if err := checkArchiveLength(folder, name+".part"); err != nil {
		writeCaseFailure(w, http.StatusBadRequest, "bad-folder", archiveFolderText)
		return
	}
	free, ferr := archiveFreeFn(folder)
	if ferr != nil {
		writeCaseFailure(w, http.StatusBadRequest, "bad-folder", archiveFolderText)
		return
	}
	// The file is the database plus a fifth, and never less than the header, the key
	// block and the smallest database: an unreadable size must not skip the check.
	need := uint64(dbBytes) + uint64(dbBytes)/5
	if need < archiveMinRoom {
		need = archiveMinRoom
	}
	if free < need {
		writeCaseFailure(w, http.StatusInsufficientStorage, "no-space", archiveNoSpaceText)
		return
	}

	fk, kind, check, err := h.newArchiveKey(r.Context(), id, b.pass)
	if err != nil {
		switch {
		case errors.Is(err, errArchivePassphrase):
			writeCaseFailure(w, http.StatusBadRequest, "bad-passphrase", archiveTypedPassText)
		case errors.Is(err, casestore.ErrLocked):
			writeLocked(w)
		case errors.Is(err, casestore.ErrCaseLocked):
			writeStoreError(w, err)
		default:
			writeCaseFailure(w, http.StatusInternalServerError, "archive-failed", archiveCheckText)
		}
		return
	}
	defer func() {
		if !started {
			clear(fk.Key)
		}
	}()

	dir := filepath.Join(root, string(id))
	if err := casestore.RejectReparse(root, dir); err != nil {
		writeCaseFailure(w, http.StatusInternalServerError, "archive-failed", archiveCheckText)
		return
	}
	keys, _, err := casestore.ReadKeyFileAny(filepath.Join(dir, casestore.KeyFileName), h.caseFileLookupFor(id))
	if err != nil || keys.CaseID != string(id) {
		if err != nil && errors.Is(err, casestore.ErrLocked) {
			writeLocked(w)
		} else if err != nil && errors.Is(err, os.ErrNotExist) {
			writeCaseFailure(w, http.StatusNotFound, "case-not-found", "The case was not found.")
		} else {
			writeCaseFailure(w, http.StatusInternalServerError, "archive-failed", archiveCheckText)
		}
		keys.Zero()
		return
	}
	defer func() {
		if !started {
			keys.Zero()
		}
	}()

	dbPath := caseDBPath(root, id)
	run := archiveRun{
		ar: ar, id: id, keys: keys, fk: fk, kind: kind, check: check,
		folder: folder, name: name,
		part:  filepath.Join(folder, name+".part"),
		final: filepath.Join(folder, name),
		src: casestore.CaseFileSource{
			DBPath: dbPath, CaseID: string(id), Title: info.Title, Created: info.Created, Closed: info.Closed, Keys: keys,
			Hold: func() (func(), error) { return casestore.HoldCaseDB(context.Background(), dbPath, keys.DB) },
		},
	}

	// A recording into this case stops first, so its last batch is saved before
	// the file is made. It starts again when the archive does not go through.
	if p, running := h.samplerRunningFor(id); running {
		run.sampler, run.hadSamp = p, true
		h.stopSamplerFor(id, false, stopCaseClosed)
	}
	injectAt("before-begin", "", "")
	_, err = ar.BeginArchive(r.Context(), id, believed, casestore.ArchivePending{
		Folder: folder, Name: name, Started: now.UTC(), SamplerWas: run.hadSamp,
	})
	if err != nil {
		if run.hadSamp {
			h.restartSampler(id, run.sampler)
		}
		writeStoreError(w, err)
		return
	}
	// A recording another tab started between the first look and the stamp is
	// stopped now, so none runs into the case while it is archived. It is put back
	// like the first one if the archive does not go through.
	if !run.hadSamp {
		if p, running := h.samplerRunningFor(id); running {
			run.sampler, run.hadSamp = p, true
			h.stopSamplerFor(id, false, stopCaseClosed)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	job := newFileJob(jobArchive, id, cancel)
	job.name, job.folder = name, folder
	total := 3 * dbBytes
	if total < 1 {
		total = 1
	}
	job.total.Store(total)
	st.jobs.put(job)
	started = true
	go h.runArchive(ctx, job, run)
	writeJSON(w, http.StatusAccepted, job.dto(), 4096)
}

// archiveStatus answers GET .../archive: the last archive job of the case.
func (h *handler) archiveStatus(w http.ResponseWriter, r *http.Request, raw string) {
	id, ok := caseIDOf(w, raw)
	if !ok || !h.casesAvailable(w) {
		return
	}
	if j := h.caseFiles().jobs.get(jobArchive, id); j != nil {
		writeJSON(w, http.StatusOK, j.dto(), 4096)
		return
	}
	writeJSON(w, http.StatusOK, fileJobDTO{Kind: jobArchive, State: "idle"}, 4096)
}

// archiveCancel answers DELETE .../archive. The case must be Archiving and its job
// must not have taken the commit point; the first of Cancel and commit wins, and a
// loser answers M2.
func (h *handler) archiveCancel(w http.ResponseWriter, r *http.Request, raw string) {
	id, ok := caseIDOf(w, raw)
	if !ok {
		return
	}
	_, _, ok = h.archivePrecheck(w)
	if !ok {
		return
	}
	believed, ok := believedState(w, r)
	if !ok {
		return
	}
	info, err := h.infoOf(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	info = h.settle(r.Context(), info)
	if err := casestore.CheckMark(info.Mark); err != nil {
		writeStoreError(w, err)
		return
	}
	if err := casestore.CheckBelief(casestore.ActionCancelArchive, believed, casestore.StateOf(info)); err != nil {
		writeStoreError(w, err)
		return
	}
	job := h.caseFiles().jobs.running(jobArchive, id)
	if job == nil || !job.requestCancel() {
		writeStoreError(w, casestore.ErrCaseChanged)
		return
	}
	select {
	case <-job.done:
	case <-time.After(archiveWait):
	case <-r.Context().Done():
	}
	writeJSON(w, http.StatusOK, job.dto(), 4096)
}
