package server

import (
	"context"
	"errors"
	"net/http"
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
	if cur.KeyKind == casestore.KeyKindMaster && cur.KeyCheck != "" && h.mk.masterCheck() != cur.KeyCheck {
		st.verified.drop(id)
		writeCaseFailure(w, http.StatusConflict, "old-key-only", oldKeyOnlyText)
		return
	}

	st.freeing.add(id)
	defer st.freeing.remove(id)

	deadline := time.Now().Add(freeWaitFor)
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
		select {
		case <-time.After(freePoll):
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
	done, err := ar.Entry(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, recordView(done), maxResponseBytes)
}
