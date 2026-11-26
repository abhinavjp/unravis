package server

import (
	"context"
	"log"
	"net/http"
	"time"

	"api-audit-explorer/internal/casestore"
)

// expiredEraseWait is how long an unlock waits for the case to be free before it leaves an expired case for the
// next start-up sweep. It is short on purpose: the person is waiting at the key card (the manual erase waits 30 s).
const expiredEraseWait = 5 * time.Second

// erasedExpiredStatus is the "status" of the unlock reply when the case was erased because it had expired.
const erasedExpiredStatus = "erased-expired"

// eraseIfExpired erases a case that was closed more than 30 days ago, by the rule of the start-up sweep
// (casestore.SweepDue). It runs right after a case key is unlocked: while the key was locked the listing had
// no close date for the case, so the sweep skipped it (RV-DAT-8). It says whether the case was erased.
//
// A failure, or a case that stays busy past the short wait, is logged and leaves the case; the next start sweeps
// it. The ledger is not stripped here, as the start-up sweep does not strip it either; the manual erase does.
// The caller must not hold h.keyOp: the wait for the case gate can take seconds.
func (h *handler) eraseIfExpired(r *http.Request, id casestore.CaseID) bool {
	ctx := context.WithoutCancel(r.Context())
	if !h.caseExpired(ctx, id) {
		return false
	}
	release, err := h.gates.acquireEraseStopping(ctx, id, h.eraseTimerFor(expiredEraseWait), func() { _ = h.stopCodeRunsFor(id) })
	if err != nil {
		log.Printf("Expired case %s was not erased after its unlock (the next start will): %v", id, err) //nolint:gosec // G706: id is a validated case id (caseIDOf), err is ours
		return false
	}
	defer release()
	// The case may have been reopened while the slot was being taken.
	if !h.caseExpired(ctx, id) {
		return false
	}
	// A running archive is never cut short by an erase (the manual erase refuses the same way).
	if err := h.refuseWhileArchiving(ctx, id); err != nil {
		log.Printf("Expired case %s was not erased after its unlock (the next start will): %v", id, err) //nolint:gosec // G706: id is a validated case id (caseIDOf), err is ours
		return false
	}
	if err := h.cases.Erase(ctx, id); err != nil {
		log.Printf("Expired case %s was not erased after its unlock (the next start will): %v", id, err) //nolint:gosec // G706: id is a validated case id (caseIDOf), err is ours
		return false
	}
	h.exports().dropCase(string(id))
	h.event(r, "case_erased", "by", "expired_unlock")
	h.noteErased(id)
	return true
}

// caseExpired says whether the store lists the case as one the sweep would erase now.
func (h *handler) caseExpired(ctx context.Context, id casestore.CaseID) bool {
	info, found, err := h.caseRow(ctx, id)
	return err == nil && found && casestore.SweepDue(info, time.Now())
}
