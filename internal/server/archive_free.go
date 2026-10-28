package server

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"api-audit-explorer/internal/casestore"
)

// Free (P3, spec v7 section 7.5, stage 3): remove the working copy of a kept
// archive after the case file passed Verify and the person confirmed by typing the
// short code.
//
//	POST /api/cases/{id}/free   body {"confirm": short code}
//
// Order: the belief and the verified mark are checked; new readers are turned away
// (M19); the handler waits up to freeWaitFor for readers that already hold the
// case, with no lock held, so none of them can be starved by the wait; then under
// the case lock it marks the case Freeing (the commit point) and removes the
// working copy. A crash after the mark is finished by recovery, never undone.

var (
	// freeWaitFor is how long Free waits for readers to let go of the case (M4
	// after that). A variable so a test does not wait 30 seconds.
	freeWaitFor = 30 * time.Second
	// freePoll is the pause between attempts while waiting.
	freePoll = 100 * time.Millisecond
)

// freeingSet is the cases a Free is waiting on. A new reader of one answers M19
// instead of joining the readers Free is waiting for.
type freeingSet struct {
	mu sync.Mutex
	m  map[casestore.CaseID]int
}

func (f *freeingSet) add(id casestore.CaseID) {
	f.mu.Lock()
	if f.m == nil {
		f.m = map[casestore.CaseID]int{}
	}
	f.m[id]++
	f.mu.Unlock()
}

func (f *freeingSet) remove(id casestore.CaseID) {
	f.mu.Lock()
	if f.m[id] <= 1 {
		delete(f.m, id)
	} else {
		f.m[id]--
	}
	f.mu.Unlock()
}

func (f *freeingSet) has(id casestore.CaseID) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.m[id] > 0
}

type freeBody struct {
	Confirm string `json:"confirm"`
}

func (h *handler) freeCase(w http.ResponseWriter, r *http.Request, raw string) {
	id, ok := caseIDOf(w, raw)
	if !ok {
		return
	}
	_, ar, ok := h.archivePrecheck(w)
	if !ok {
		return
	}
	// Free needs the master key in force: blocked while it is locked, damaged or
	// not set up yet.
	if h.mk.State() != MasterKeyUnlocked {
		h.masterKeyFail(w, http.StatusConflict, "no-master-key", "Set a master key first.")
		return
	}
	believed, ok := believedState(w, r)
	if !ok {
		return
	}
	var body freeBody
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Confirm != shortCode(id) {
		writeCaseFailure(w, http.StatusBadRequest, "confirm-mismatch", "The confirmation does not match the case code.")
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
	st := h.caseFiles()
	if !st.verified.ok(id, cur.SHA256, archiveNow()) {
		writeCaseFailure(w, http.StatusConflict, "check-first", checkFirstText)
		return
	}
	// The key may have changed since the check (a reset): the file could then only
	// be opened with the passphrase it was made with, and that is M13 before the
	// working copy goes, not after.
	if stale, kerr := h.archiveKeyStale(id, cur); kerr != nil {
		writeStoreError(w, kerr)
		return
	} else if stale {
		st.verified.drop(id)
		writeCaseFailure(w, http.StatusConflict, "old-key-only", oldKeyOnlyText)
		return
	}

	st.freeing.add(id)
	defer st.freeing.remove(id)

	deadline := time.Now().Add(freeWaitFor)
	poll := freePoll
	var release func()
	for {
		rel, err := h.gates.acquire(r.Context(), id)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		if err := h.guard(r.Context(), id, casestore.ActionFree, believed); err != nil {
			rel()
			writeStoreError(w, err)
			return
		}
		// Everything above was read before the wait, and the record can change
		// while no lock is held (a restore and a new archive in another tab; the
		// file moved, cut short or replaced by another tool). Under the lock,
		// right before the commit point, the record, the check and the file on
		// disk are looked at again. A file that is not the one the check proved
		// is never relied on: the working copy is the only good copy then.
		took, err := h.freeRecheck(r.Context(), ar, id, believed)
		if err != nil {
			rel()
			writeStoreError(w, err)
			return
		}
		if took.refused != nil {
			rel()
			st.verified.drop(id)
			writeCaseFailure(w, http.StatusConflict, took.refused.reason, took.refused.text)
			return
		}
		if 2*took.hashed > poll {
			poll = 2 * took.hashed // do not spend the wait reading the file again and again
		}
		err = ar.BeginFree(r.Context(), id, believed)
		if err == nil {
			release = rel
			break
		}
		rel() // nothing is held while waiting
		if !errors.Is(err, casestore.ErrCaseInUse) {
			writeStoreError(w, err)
			return
		}
		if time.Now().After(deadline) {
			writeStoreError(w, casestore.ErrCaseInUse)
			return
		}
		injectAt("free-wait", "", "")
		select {
		case <-time.After(poll):
		case <-r.Context().Done():
			writeStoreError(w, r.Context().Err())
			return
		}
	}
	defer release()

	// The mark is the commit: from here the working copy goes, whatever happens.
	// The request context could be cancelled by the client; the work is not.
	fctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := ar.FinishFree(fctx, id); err != nil {
		// The mark stays; recovery finishes it at the next start or the next call.
		writeStoreError(w, err)
		return
	}
	st.verified.drop(id)
	h.exports().dropCase(string(id))
	h.noteFreedArchives(context.Background())
	done, err := ar.Entry(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, recordView(done), maxResponseBytes)
}

type freeRefusal struct{ reason, text string }

type freeRechecked struct {
	refused *freeRefusal
	hashed  time.Duration // how long reading the file took
}

// freeRecheck is the look under the case lock, just before Free marks the case:
// the record is read again, the check must still stand for the file the record now
// names, the key must still open it, and the file on disk is read in full and must
// have the size and checksum the record states. It answers a refusal (the check is
// to be made again, the working copy untouched) or an error (the state changed in a
// way the store explains).
func (h *handler) freeRecheck(ctx context.Context, ar casestore.Archiver, id casestore.CaseID, believed casestore.State) (freeRechecked, error) {
	var out freeRechecked
	again := func() (freeRechecked, error) {
		out.refused = &freeRefusal{"check-first", checkFirstText}
		return out, nil
	}
	info, err := h.entryOf(ctx, ar, id)
	if err != nil {
		return out, err
	}
	if err := casestore.CheckBelief(casestore.ActionFree, believed, casestore.StateOf(info)); err != nil {
		return out, err
	}
	cur, kept := keptFile(info)
	if !kept {
		out.refused = &freeRefusal{"not-kept", archiveNotKeptText}
		return out, nil
	}
	if !h.caseFiles().verified.ok(id, cur.SHA256, archiveNow()) {
		return again()
	}
	if stale, kerr := h.archiveKeyStale(id, cur); kerr != nil {
		return out, kerr
	} else if stale {
		out.refused = &freeRefusal{"old-key-only", oldKeyOnlyText}
		return out, nil
	}
	start := time.Now()
	size, sum, err := casestore.HashFile(ctx, filepath.Join(cur.Folder, cur.Name), nil)
	out.hashed = time.Since(start)
	if err != nil {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		// Gone, moved, locked or unreadable: not a file Free may depend on.
		return again()
	}
	if size != cur.Bytes || sum != cur.SHA256 {
		return again()
	}
	return out, nil
}
