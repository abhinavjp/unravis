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

// eraseIfExpired erases a case that was closed more than 30 days ago, by the rule of the start-up sweep
// (casestore.SweepDue: a close date that is not in the future, older than the keep time, and no archive on it).
// It runs right after a case key is unlocked: while the key was locked the listing had no close date for the
// case, so the sweep skipped it (RV-DAT-8). It says whether the case was erased, and when it had been closed.
//
// It does what the manual erase does after the code is typed: the same case slot (running code stopped),
// the same running-archive refusal, the export previews dropped, the ledger evidence stripped, the Erased line
// and the "case_erased" event. A failure, a running archive, or a case that stays busy past the short wait is
// logged and leaves the case; the next start's sweep finishes the job.
//
// The caller must not hold h.keyOp: the wait for the case slot can take seconds.
func (h *handler) eraseIfExpired(r *http.Request, id casestore.CaseID) (closed time.Time, erased bool) {
	ctx := context.WithoutCancel(r.Context())
	closed, due := h.caseExpired(ctx, id)
	if !due {
		return time.Time{}, false
	}
	release, err := h.gates.acquireEraseStopping(ctx, id, h.eraseTimerFor(expiredEraseWait), func() { _ = h.stopCodeRunsFor(id) })
	if err != nil {
		log.Printf("Expired case %s was not erased after its unlock (the next start will): %v", id, err) //nolint:gosec // G706: id is a validated case id (caseIDOf), err is ours
		return time.Time{}, false
	}
	defer release()
	// The case may have been reopened (its close date cleared) while the slot was being taken.
	if closed, due = h.caseExpired(ctx, id); !due {
		return time.Time{}, false
	}
	// A running archive is never cut short by an erase (the manual erase refuses the same way).
	if err := h.refuseWhileArchiving(ctx, id); err != nil {
		log.Printf("Expired case %s was not erased after its unlock (the next start will): %v", id, err) //nolint:gosec // G706: id is a validated case id (caseIDOf), err is ours
		return time.Time{}, false
	}
	h.pending.add(id)
	if err := h.cases.Erase(ctx, id); err != nil {
		log.Printf("Expired case %s was not erased after its unlock (the next start will): %v", id, err) //nolint:gosec // G706: id is a validated case id (caseIDOf), err is ours
		return time.Time{}, false
	}
	h.exports().dropCase(string(id))
	if err := h.stripLedger(id); err != nil {
		// The case is gone. Its id stays marked as pending, as after a manual erase, so an erase of that id still runs the strip.
		log.Printf("Expired case %s was erased but its ledger evidence was not stripped: %v", id, err) //nolint:gosec // G706: id is a validated case id (caseIDOf), err is ours
	} else {
		h.pending.remove(id)
	}
	h.event(r, "case_erased", "by", "expired_unlock")
	h.noteErased(id)
	h.noteFreedArchives(context.Background())
	return closed, true
}

// caseExpired says whether the store lists the case as one the sweep would erase now, and when it was closed.
func (h *handler) caseExpired(ctx context.Context, id casestore.CaseID) (time.Time, bool) {
	info, found, err := h.caseRow(ctx, id)
	if err != nil || !found || !casestore.SweepDue(info, time.Now()) {
		return time.Time{}, false
	}
	return *info.Closed, true
}
