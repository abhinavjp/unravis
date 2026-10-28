package server

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"api-audit-explorer/internal/casestore"
)

// Verify (P3, spec v7 section 7.5, stage 1 of Free): restore the case file of a
// kept archive into a temporary store, as a real restore would, then compare its
// checksum and counts with the record. It changes nothing and takes no case lock.
//
//	POST   /api/cases/{id}/verify   start; body {"passphrase": typed (when the master key cannot open the file)}
//	GET    /api/cases/{id}/verify   the job, and whether the file is checked
//	DELETE /api/cases/{id}/verify   Cancel
//
// A file that passed is remembered in memory for an hour, keyed by the file's
// checksum; Free refuses without it.

// verifyDirName holds the temporary stores of Verify. Start-up recovery deletes it.
const verifyDirName = ".verify"

// verifiedTTL is how long a passed check opens the door to Free.
const verifiedTTL = time.Hour

type verifiedMark struct {
	sha string
	at  time.Time
}

// verifiedSet remembers which case files passed Verify. It is in memory on
// purpose: a restart asks for the check again.
type verifiedSet struct {
	mu sync.Mutex
	m  map[casestore.CaseID]verifiedMark
}

func (v *verifiedSet) set(id casestore.CaseID, sha string, now time.Time) {
	v.mu.Lock()
	if v.m == nil {
		v.m = map[casestore.CaseID]verifiedMark{}
	}
	v.m[id] = verifiedMark{sha: sha, at: now}
	v.mu.Unlock()
}

func (v *verifiedSet) ok(id casestore.CaseID, sha string, now time.Time) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	m, found := v.m[id]
	return found && m.sha == sha && sha != "" && now.Sub(m.at) < verifiedTTL
}

func (v *verifiedSet) drop(id casestore.CaseID) {
	v.mu.Lock()
	delete(v.m, id)
	v.mu.Unlock()
}

// keptFile returns the case file of a kept archive, or the refusal text. A case
// that is not Archived, or whose working copy is already gone, has none.
func keptFile(info casestore.CaseInfo) (*casestore.ArchiveFile, bool) {
	a := info.Archive
	if a == nil || !a.Active || a.Freed() || info.Missing {
		return nil, false
	}
	cur := a.Current()
	return cur, cur != nil
}

// entryOf reads the record of a case, settled, with a mark that must wait turned
// into its refusal.
func (h *handler) entryOf(ctx context.Context, ar casestore.Archiver, id casestore.CaseID) (casestore.CaseInfo, error) {
	info, err := ar.Entry(ctx, id)
	if err != nil {
		return casestore.CaseInfo{}, err
	}
	info = h.settle(ctx, info)
	if err := casestore.CheckMark(info.Mark); err != nil {
		return casestore.CaseInfo{}, err
	}
	return info, nil
}

type verifyDTO struct {
	fileJobDTO
	// Verified is true while a passed check still opens the door to Free.
	Verified bool `json:"verified"`
}

func (h *handler) verifyCase(w http.ResponseWriter, r *http.Request, raw string) {
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
	// Not a body with a path: Verify checks the file the record names.
	if b.path != "" {
		writeCaseFailure(w, http.StatusBadRequest, "bad-request", "The request body is invalid.")
		return
	}

	info, err := h.entryOf(r.Context(), ar, id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if err := casestore.CheckBelief(casestore.ActionFree, believed, casestore.StateOf(info)); err != nil {
		writeStoreError(w, err)
		return
	}
	cur, kept := keptFile(info)
	if !kept {
		writeCaseFailure(w, http.StatusConflict, "not-kept", archiveNotKeptText)
		return
	}
	// A key problem is answered before any file is read.
	st := h.caseFiles()
	if len(b.pass) > 0 {
		if wait := st.restoreTh.wait(); wait > 0 {
			h.masterKeyThrottled(w, wait)
			return
		}
	}
	// The job outlives this request, and the request wipes its body when it ends:
	// the job gets its own copy of the passphrase and clears it when it is done.
	pass := append([]byte(nil), b.pass...)
	derive, cleanKey, oldKey := h.openDeriver(cur, pass)
	clean := func() { cleanKey(); clear(pass) }
	if oldKey {
		clean()
		writeCaseFailure(w, http.StatusConflict, "old-key-only", oldKeyOnlyText)
		return
	}
	// The temporary store needs room for one more copy of the case.
	if free, ferr := archiveFreeFn(root); ferr == nil && cur.Bytes > 0 && free < uint64(cur.Bytes) {
		clean()
		writeCaseFailure(w, http.StatusInsufficientStorage, "no-space", archiveNoSpaceText)
		return
	}
	if !st.busy.CompareAndSwap(false, true) {
		clean()
		writeCaseFailure(w, http.StatusConflict, "busy", caseFileBusyText)
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	job := newFileJob(jobVerify, id, cancel)
	job.name, job.folder = cur.Name, cur.Folder
	total := 3 * cur.Bytes
	if total < 1 {
		total = 1
	}
	job.total.Store(total)
	st.jobs.put(job)
	typed := len(b.pass) > 0
	go h.runVerify(ctx, job, ar, root, id, *cur, derive, clean, typed)
	writeJSON(w, http.StatusAccepted, verifyDTO{fileJobDTO: job.dto()}, 4096)
}

// runVerify is the Verify goroutine. It owns the busy lock until it ends.
func (h *handler) runVerify(ctx context.Context, job *fileJob, ar casestore.Archiver, root string, id casestore.CaseID,
	cur casestore.ArchiveFile, derive casestore.KeyDeriver, clean func(), typed bool) {
	st := h.caseFiles()
	defer close(job.done)
	defer st.busy.Store(false)
	defer clean()
	path := filepath.Join(cur.Folder, cur.Name)

	stopped := func() {
		cancelled, locked := job.wasCancelled()
		reason := "stopped"
		if locked && !cancelled {
			reason = "key-locked"
		}
		job.finish(fileJobStopped, reason, verifyStoppedText)
	}
	failed := func() { job.finish(fileJobFailed, "check-failed", caseFileFailedText) }

	if injectAt("verify-start", path, "") {
		return
	}
	job.setPhase("reading")
	size, sum, err := casestore.HashFile(ctx, path, job.step)
	switch {
	case ctx.Err() != nil:
		stopped()
		return
	case err != nil || size != cur.Bytes || sum != cur.SHA256:
		failed()
		return
	}
	if injectAt("verify-hashed", path, "") {
		return
	}

	job.setPhase("restoring")
	res, cleanupErr, err := casestore.CheckCaseFile(ctx, path, filepath.Join(root, verifyDirName), derive, job.step)
	_ = cleanupErr // a temporary store that could not be removed is found again by start-up recovery
	switch {
	case ctx.Err() != nil:
		stopped()
		return
	case err != nil:
		if typed {
			st.restoreTh.fail()
		}
		failed()
		return
	}
	if typed {
		st.restoreTh.reset()
	}
	if cur.Counts != (casestore.Counts{}) && res.Counts != cur.Counts {
		failed()
		return
	}
	if injectAt("verify-checked", path, "") {
		return
	}

	// The last step: the case must still be a kept archive of this very file. A
	// Restore in place or an Erase that ran meanwhile answers M2.
	ictx, icancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer icancel()
	now, err := ar.Entry(ictx, id)
	if err != nil {
		job.finish(fileJobFailed, "case-changed", caseChangedText)
		return
	}
	k, kept := keptFile(now)
	if !kept || k.SHA256 != cur.SHA256 || now.Mark != "" {
		job.finish(fileJobFailed, "case-changed", caseChangedText)
		return
	}
	st.verified.set(id, cur.SHA256, archiveNow())
	job.finish(fileJobDone, "", "")
}

func (h *handler) verifyStatus(w http.ResponseWriter, r *http.Request, raw string) {
	id, ok := caseIDOf(w, raw)
	if !ok || !h.casesAvailable(w) {
		return
	}
	st := h.caseFiles()
	d := verifyDTO{fileJobDTO: fileJobDTO{Kind: jobVerify, State: "idle"}}
	if j := st.jobs.get(jobVerify, id); j != nil {
		d.fileJobDTO = j.dto()
	}
	if ar, ok := h.cases.(casestore.Archiver); ok {
		if info, err := ar.Entry(r.Context(), id); err == nil {
			if cur, kept := keptFile(info); kept {
				d.Verified = st.verified.ok(id, cur.SHA256, archiveNow())
			}
		}
	}
	writeJSON(w, http.StatusOK, d, 4096)
}

func (h *handler) verifyCancel(w http.ResponseWriter, r *http.Request, raw string) {
	id, ok := caseIDOf(w, raw)
	if !ok || !h.casesAvailable(w) {
		return
	}
	job := h.caseFiles().jobs.running(jobVerify, id)
	if job == nil || !job.requestCancel() {
		writeStoreError(w, casestore.ErrCaseChanged)
		return
	}
	select {
	case <-job.done:
	case <-time.After(archiveWait):
	case <-r.Context().Done():
	}
	writeJSON(w, http.StatusOK, verifyDTO{fileJobDTO: job.dto()}, 4096)
}

// cleanVerifyDir removes the temporary stores of Verify. A failure leaves them
// for the health panel's "cleanup pending" line; it never touches anything else.
func cleanVerifyDir(root string) error {
	if root == "" {
		return nil
	}
	dir := filepath.Join(root, verifyDirName)
	if _, err := os.Lstat(dir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	return os.RemoveAll(dir)
}

// verifyLeftovers reports whether the .verify folder holds anything.
func verifyLeftovers(root string) bool {
	ents, err := os.ReadDir(filepath.Join(root, verifyDirName))
	return err == nil && len(ents) > 0
}
