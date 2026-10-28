package server

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"api-audit-explorer/internal/casestore"
)

// caseKeyNeededText is M-text for a restore of a case that had a key of its own.
const caseKeyNeededText = "Type the case key to restore this case. The new case keeps it."

// Restore (P3, spec v7 section 7.3).
//
//	POST /api/cases/{id}/restore-in-place   only while the working copy is kept
//	POST /api/cases/{id}/restore-as-new     kept or freed, as often as wanted
//
// Both end Closed. Restore in place is a record move: the working copy is still
// there, so the case simply stops being Archived. Restore as new reads the case
// file of the archive in force into a new case with a new id.

// madeDate is how a case file's date is written into a story line.
func madeDate(t time.Time) string { return t.UTC().Format("2006-01-02") }

// afterRestore does what every restore ends with: the new case is closed (all
// restores end Closed), its story says where it came from, and the original, if
// it still exists, gets a line in its list of restores. A failure of the extras
// never undoes the restore: the case exists and is whole.
func (h *handler) afterRestore(ctx context.Context, info casestore.CaseInfo, from casestore.CaseFileInfo, made time.Time) casestore.CaseInfo {
	ar, _ := h.cases.(casestore.Archiver)
	if err := h.cases.CloseCase(ctx, info.ID); err == nil {
		if c, err := h.cases.Open(ctx, info.ID); err == nil {
			info = c.Info()
			_ = c.Close()
		}
	}
	if ar == nil {
		return info
	}
	_ = ar.AddHistory(ctx, info.ID, casestore.HistoryRestoredFromFile, madeDate(made))
	if from.CaseID != "" {
		src := casestore.CaseID(from.CaseID)
		_ = ar.AddHistory(ctx, info.ID, casestore.HistoryRestoredFromCase, shortCode(src))
		if err := ar.AddRestoreLine(ctx, src, casestore.RestoreLine{At: archiveNow().UTC(), Code: shortCode(info.ID)}); err != nil && !errors.Is(err, casestore.ErrNotFound) {
			_ = err // the original is not listed; the restored case is still whole
		}
	}
	if c, err := h.cases.Open(ctx, info.ID); err == nil {
		info = c.Info()
		_ = c.Close()
	}
	return info
}

func (h *handler) restoreInPlace(w http.ResponseWriter, r *http.Request, raw string) {
	id, ok := caseIDOf(w, raw)
	if !ok {
		return
	}
	_, ar, ok := h.archivePrecheck(w)
	if !ok {
		return
	}
	believed, ok := believedState(w, r)
	if !ok {
		return
	}
	release, err := h.gates.acquire(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	defer release()
	if err := h.guard(r.Context(), id, casestore.ActionRestore, believed); err != nil {
		writeStoreError(w, err)
		return
	}
	info, err := ar.Entry(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if _, kept := keptFile(info); !kept {
		// A freed case has no working copy to come back to: Restore as new only.
		writeStoreError(w, casestore.ErrCaseChanged)
		return
	}
	if err := ar.RestoreInPlace(r.Context(), id, believed); err != nil {
		writeStoreError(w, err)
		return
	}
	h.caseFiles().verified.drop(id)
	c, err := h.cases.Open(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	defer c.Close()
	writeJSON(w, http.StatusOK, h.viewOf(r.Context(), c), maxResponseBytes)
}

func (h *handler) restoreAsNew(w http.ResponseWriter, r *http.Request, raw string) {
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
	if err := h.beliefFirst(r.Context(), id, casestore.ActionRestoreAsNew, believed); err != nil {
		writeStoreError(w, err)
		return
	}
	// The new case is sealed with this PC's master key.
	if h.mk.State() != MasterKeyUnlocked {
		h.masterKeyFail(w, http.StatusConflict, "no-master-key", "Set a master key first.")
		return
	}
	b := h.readCaseFileBody(w, r)
	if b == nil {
		return
	}
	defer b.wipe()
	if b.path != "" {
		writeCaseFailure(w, http.StatusBadRequest, "bad-request", "The request body is invalid.")
		return
	}
	info, err := h.entryOf(r.Context(), ar, id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	// CheckBelief takes any belief for restore as new, and beliefFirst lets a case
	// marked mid-archive through, so a stale belief is refused here as well.
	if believed != "" && believed != casestore.StateArchived {
		writeStoreError(w, casestore.ErrCaseChanged)
		return
	}
	if err := casestore.CheckBelief(casestore.ActionRestoreAsNew, believed, casestore.StateOf(info)); err != nil {
		writeStoreError(w, err)
		return
	}
	var cur *casestore.ArchiveFile
	if info.Archive != nil {
		cur = info.Archive.Current()
	}
	if cur == nil {
		writeStoreError(w, casestore.ErrCaseChanged)
		return
	}
	path := filepath.Join(cur.Folder, cur.Name)
	if fi, err := os.Stat(path); err != nil || fi.IsDir() {
		writeCaseFailure(w, http.StatusConflict, "file-missing", restoreNeedsFileText)
		return
	}
	st := h.caseFiles()
	typed := len(b.pass) > 0
	if typed {
		if wait := st.restoreTh.wait(); wait > 0 {
			h.masterKeyThrottled(w, wait)
			return
		}
	}
	var (
		derive casestore.KeyDeriver
		clean  func()
		oldKey bool
	)
	if cur.KeyKind == casestore.KeyKindCase {
		// The new case keeps the case key, and a key held in memory cannot be turned
		// into a new wrap: the case passphrase is typed, even while the case is unlocked.
		if !typed {
			writeCaseFailure(w, http.StatusConflict, "case-key-needed", caseKeyNeededText)
			return
		}
		derive, clean = checkedKey(casestore.PassphraseKey(b.pass), cur.KeyCheck), func() {}
	} else {
		derive, clean, oldKey = h.openDeriver(id, cur, b.pass)
	}
	if oldKey {
		writeCaseFailure(w, http.StatusConflict, "old-key-only", oldKeyOnlyText)
		return
	}
	defer clean()
	if !st.busy.CompareAndSwap(false, true) {
		writeCaseFailure(w, http.StatusConflict, "busy", caseFileBusyText)
		return
	}
	defer st.busy.Store(false)

	newInfo, from, err := casestore.RestoreCaseFileFull(r.Context(), restoreAdapter{Store: h.cases, root: root}, h.mk.Lookup, path, derive)
	switch {
	case err == nil:
		if typed {
			st.restoreTh.reset()
		}
		if cur.KeyKind == casestore.KeyKindCase || from.OwnKey {
			if err := h.keepOwnKey(r.Context(), newInfo.ID, b.pass); err != nil {
				writeCaseFailure(w, http.StatusInternalServerError, "restore-failed", caseFileRestoreFailed)
				return
			}
		}
		out := h.afterRestore(r.Context(), newInfo, from, cur.Made)
		writeJSON(w, http.StatusCreated, toCaseDTO(out), maxResponseBytes)
	case errors.Is(err, casestore.ErrCaseFile):
		if typed {
			st.restoreTh.fail()
		}
		writeCaseFailure(w, http.StatusUnprocessableEntity, "bad-case-file", casestore.CaseFileMessage)
	case errors.Is(err, casestore.ErrLocked), errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		writeStoreError(w, err)
	default:
		writeCaseFailure(w, http.StatusInternalServerError, "restore-failed", caseFileRestoreFailed)
	}
}

// keepOwnKey gives a case just restored from a file made under a case key that same
// key, so a restore never turns a keyed case into one under the master key. When it
// cannot, the new case is erased: nothing is left silently downgraded.
func (h *handler) keepOwnKey(ctx context.Context, id casestore.CaseID, pass []byte) error {
	ck, ok := h.ownKeyer()
	var err error
	if !ok {
		err = errors.New("server: this store has no case keys")
	} else if release, aerr := h.gates.acquire(ctx, id); aerr != nil {
		err = aerr
	} else {
		h.keyOp.Lock()
		pc := bytes.Clone(pass)
		err = ck.SetCaseKey(ctx, id, pc)
		clear(pc)
		h.keyOp.Unlock()
		release()
	}
	if err != nil {
		_ = h.cases.Erase(context.WithoutCancel(ctx), id)
	}
	return err
}
