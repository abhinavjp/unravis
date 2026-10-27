package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/ledger"
)

// A case keeps its own theories ledger inside the encrypted case database, so the
// theories travel with the case file. The routes mirror the shared /api/ledger
// ones (same bodies, same limits, same error mapping) under /api/cases/{id}:
//
//	GET    /api/cases/{id}/ledger                  200 list ([] when empty)
//	POST   /api/cases/{id}/ledger                  201 the new open entry
//	PUT    /api/cases/{id}/ledger/{hid}            200 the entry; 422 + reasons when a guard blocks "strong"
//	DELETE /api/cases/{id}/ledger/{hid}            204
//	POST   /api/cases/{id}/ledger/{hid}/reuse      201 a new open entry that carries the old state as history
//	POST   /api/cases/{id}/ledger/copy-shared      201 the copied entries (see caseLedgerCopyShared)
//
// Common answers: 404 for an invalid or unknown case id (and for a hypothesis
// that is not in this case's ledger), 400 for an invalid entry or body, 413 for
// a body over 1 MiB, 415 for a body that is not JSON, 409 "case-closed" for a
// write to a closed case (the list still works), 423 while the master key is
// locked (before any handler runs), 503 in the demo, when cases are unavailable
// or when the case store cannot keep a ledger, 500 "ledger-damaged" when the
// stored ledger cannot be read (it is never overwritten).

const caseLedgerDemoText = "Theories are kept in a case, and cases are off in the demo."

// caseLedgerLocks serialises the read-modify-write of each case's ledger, so two
// browser tabs editing the same case cannot lose an edit to each other. A lock is
// kept per case id for the life of the process; there is one per case ever opened.
type caseLedgerLocks struct{ m sync.Map }

func (l *caseLedgerLocks) of(id casestore.CaseID) *sync.Mutex {
	v, _ := l.m.LoadOrStore(id, &sync.Mutex{})
	return v.(*sync.Mutex)
}

/* ---------------------------------------------------------------- routing */

// caseLedgerItemPath matches /api/cases/{id}/ledger/{hid} and returns "id/hid".
// copy-shared is a fixed word and is matched by its own, earlier route.
func caseLedgerItemPath(path string) (string, bool) {
	parts, ok := caseLedgerParts(path)
	if !ok || len(parts) != 3 {
		return "", false
	}
	return parts[0] + "/" + parts[2], true
}

// caseLedgerReusePath matches /api/cases/{id}/ledger/{hid}/reuse and returns "id/hid".
func caseLedgerReusePath(path string) (string, bool) {
	parts, ok := caseLedgerParts(path)
	if !ok || len(parts) != 4 || parts[3] != "reuse" {
		return "", false
	}
	return parts[0] + "/" + parts[2], true
}

func caseLedgerParts(path string) ([]string, bool) {
	rest, ok := strings.CutPrefix(path, "/api/cases/")
	if !ok {
		return nil, false
	}
	parts := strings.Split(rest, "/")
	if len(parts) < 3 || parts[0] == "" || parts[1] != "ledger" || parts[2] == "" {
		return nil, false
	}
	return parts, true
}

/* ---------------------------------------------------------------- plumbing */

// caseLedgerOf opens the case for a ledger request (same 404 rule as healthCaseOf).
func (h *handler) caseLedgerOf(w http.ResponseWriter, r *http.Request, raw string) (casestore.Case, casestore.LedgerBlobStore, casestore.CaseID, bool) {
	// The capture-only demo cases keep their theories in the case, so this route works there.
	if h.demo && !h.captureCases {
		writeError(w, http.StatusServiceUnavailable, caseLedgerDemoText)
		return nil, nil, "", false
	}
	if _, err := casestore.ParseID(raw); err != nil {
		if h.casesAvailable(w) {
			writeCaseFailure(w, http.StatusNotFound, "case-not-found", "The case was not found.")
		}
		return nil, nil, "", false
	}
	id, c, ok := h.openCase(w, r, raw)
	if !ok {
		return nil, nil, "", false
	}
	var inner casestore.Case = c
	if sc, wrapped := c.(*sharedCase); wrapped {
		inner = sc.Case
	}
	lb, ok := inner.(casestore.LedgerBlobStore)
	if !ok {
		c.Close()
		writeCaseFailure(w, http.StatusServiceUnavailable, "ledger-unavailable", "This case store cannot keep theories.")
		return nil, nil, "", false
	}
	return c, lb, id, true
}

// caseLedgerStore builds the ledger.Store over the case's stored bytes for one
// request. With write it also takes the case's ledger lock, held until done is
// called, so the load, the change and the save are one step. done is always safe
// to call once when ok.
func (h *handler) caseLedgerStore(w http.ResponseWriter, r *http.Request, id casestore.CaseID, lb casestore.LedgerBlobStore, write bool) (s ledger.Store, done func(), ok bool) {
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	unlock := func() {}
	if write {
		mu := h.ledgers.of(id)
		mu.Lock()
		unlock = mu.Unlock
	}
	done = func() { unlock(); cancel() }
	s, err := ledger.OpenFunc(
		func() ([]byte, error) { return lb.LedgerBlob(ctx) },
		func(b []byte) error { return lb.SetLedgerBlob(ctx, b) },
	)
	if err != nil {
		done()
		writeCaseLedgerError(w, err)
		return nil, nil, false
	}
	return s, done, true
}

// writeCaseLedgerError maps a ledger failure as the shared routes do, and adds
// the failures only a case can have.
func writeCaseLedgerError(w http.ResponseWriter, err error) {
	var guard *ledger.GuardError
	switch {
	case errors.As(err, &guard), errors.Is(err, ledger.ErrNotFound), errors.Is(err, ledger.ErrInvalid):
		writeLedgerError(w, err)
	case errors.Is(err, casestore.ErrCaseClosed):
		writeCaseFailure(w, http.StatusConflict, "case-closed", "This case is closed. Its theories can still be read but not changed.")
	case errors.Is(err, casestore.ErrCapExceeded):
		writeCaseFailure(w, http.StatusRequestEntityTooLarge, "ledger-full", "The case cannot hold more theories. Delete or shorten entries first.")
	case errors.Is(err, ledger.ErrUnreadable):
		writeCaseFailure(w, http.StatusInternalServerError, "ledger-damaged", "This case's theories could not be read. They were left as they are.")
	case errors.Is(err, casestore.ErrHandleClosed), errors.Is(err, casestore.ErrNotFound):
		writeCaseFailure(w, http.StatusNotFound, "case-not-found", "The case was not found.")
	default:
		if errors.Is(err, casestore.ErrLocked) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			writeStoreError(w, err)
			return
		}
		writeLedgerError(w, err)
	}
}

/* ---------------------------------------------------------------- handlers */

func (h *handler) caseLedgerList(w http.ResponseWriter, r *http.Request, raw string) {
	c, lb, id, ok := h.caseLedgerOf(w, r, raw)
	if !ok {
		return
	}
	defer c.Close()
	s, done, ok := h.caseLedgerStore(w, r, id, lb, false)
	if !ok {
		return
	}
	defer done()
	items, err := s.List()
	if err != nil {
		writeCaseLedgerError(w, err)
		return
	}
	if items == nil {
		items = []ledger.Hypothesis{}
	}
	writeJSON(w, http.StatusOK, items, maxResponseBytes)
}

func (h *handler) caseLedgerCreate(w http.ResponseWriter, r *http.Request, raw string) {
	c, lb, id, ok := h.caseLedgerOf(w, r, raw)
	if !ok {
		return
	}
	defer c.Close()
	var body struct {
		Text string `json:"text"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	s, done, ok := h.caseLedgerStore(w, r, id, lb, true)
	if !ok {
		return
	}
	defer done()
	item, err := s.Create(body.Text)
	if err != nil {
		writeCaseLedgerError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, item, maxResponseBytes)
}

// splitLedgerArg splits the "id/hid" a route matcher returned.
func splitLedgerArg(arg string) (caseID, hid string) {
	caseID, hid, _ = strings.Cut(arg, "/")
	return caseID, hid
}

func (h *handler) caseLedgerUpdate(w http.ResponseWriter, r *http.Request, arg string) {
	raw, hid := splitLedgerArg(arg)
	c, lb, id, ok := h.caseLedgerOf(w, r, raw)
	if !ok {
		return
	}
	defer c.Close()
	var input ledger.Input
	if !decodeJSON(w, r, &input) {
		return
	}
	s, done, ok := h.caseLedgerStore(w, r, id, lb, true)
	if !ok {
		return
	}
	defer done()
	item, err := s.Update(hid, input)
	if err != nil {
		writeCaseLedgerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item, maxResponseBytes)
}

func (h *handler) caseLedgerReuse(w http.ResponseWriter, r *http.Request, arg string) {
	raw, hid := splitLedgerArg(arg)
	c, lb, id, ok := h.caseLedgerOf(w, r, raw)
	if !ok {
		return
	}
	defer c.Close()
	s, done, ok := h.caseLedgerStore(w, r, id, lb, true)
	if !ok {
		return
	}
	defer done()
	item, err := s.Reuse(hid)
	if err != nil {
		writeCaseLedgerError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, item, maxResponseBytes)
}

func (h *handler) caseLedgerDelete(w http.ResponseWriter, r *http.Request, arg string) {
	raw, hid := splitLedgerArg(arg)
	c, lb, id, ok := h.caseLedgerOf(w, r, raw)
	if !ok {
		return
	}
	defer c.Close()
	s, done, ok := h.caseLedgerStore(w, r, id, lb, true)
	if !ok {
		return
	}
	defer done()
	if err := s.Delete(hid); err != nil {
		writeCaseLedgerError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// caseLedgerCopyShared copies every entry of the shared ledger (the one used when
// no case is open) into this case's ledger, once. Each copy keeps its text, state,
// notes, facts, evidence references, settling observation and history, and gets a
// new id; the shared ledger is not changed. Answers:
//
//	201 the copied entries, as the list shows them
//	404 "no-shared-ledger": there is no shared ledger, or it holds no entries
//	409 "ledger-not-empty": this case's ledger already has entries (nothing is copied)
//	409 "case-closed": the case is closed
func (h *handler) caseLedgerCopyShared(w http.ResponseWriter, r *http.Request, raw string) {
	c, lb, id, ok := h.caseLedgerOf(w, r, raw)
	if !ok {
		return
	}
	defer c.Close()
	const noShared = "There are no shared theories to copy."
	if h.ledger == nil {
		writeCaseFailure(w, http.StatusNotFound, "no-shared-ledger", noShared)
		return
	}
	shared, err := h.ledger.List()
	if err != nil {
		writeLedgerError(w, err)
		return
	}
	if len(shared) == 0 {
		writeCaseFailure(w, http.StatusNotFound, "no-shared-ledger", noShared)
		return
	}
	s, done, ok := h.caseLedgerStore(w, r, id, lb, true)
	if !ok {
		return
	}
	defer done()
	existing, err := s.List()
	if err != nil {
		writeCaseLedgerError(w, err)
		return
	}
	if len(existing) > 0 {
		writeCaseFailure(w, http.StatusConflict, "ledger-not-empty", "This case already has theories, so the shared ones were not copied.")
		return
	}
	imp, ok := s.(ledger.Importer)
	if !ok {
		writeCaseFailure(w, http.StatusServiceUnavailable, "ledger-unavailable", "This case store cannot copy theories.")
		return
	}
	added, err := imp.Import(shared)
	if err != nil {
		writeCaseLedgerError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, added, maxResponseBytes)
}
