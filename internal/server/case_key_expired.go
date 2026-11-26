package server

import (
	"context"
	"log"
	"time"

	"api-audit-explorer/internal/casestore"
)

// eraseIfExpired erases a case that was closed more than 30 days ago, by the rule of the start-up sweep
// (casestore.SweepDue). It runs right after a case key is unlocked: while the key was locked the listing had
// no close date for the case, so the sweep skipped it (RV-DAT-8). A failure is logged and leaves the case;
// the next start sweeps it.
func (h *handler) eraseIfExpired(ctx context.Context, id casestore.CaseID) {
	ctx = context.WithoutCancel(ctx)
	if !h.caseExpired(ctx, id) {
		return
	}
	release, err := h.gates.acquireEraseStopping(ctx, id, h.eraseTimer(), func() { _ = h.stopCodeRunsFor(id) })
	if err != nil {
		log.Printf("Expired case %s was not erased after its unlock: %v", id, err) //nolint:gosec // G706: id is a validated case id (caseIDOf), err is ours
		return
	}
	defer release()
	// The case may have been reopened while the slot was being taken.
	if !h.caseExpired(ctx, id) {
		return
	}
	if err := h.cases.Erase(ctx, id); err != nil {
		log.Printf("Expired case %s was not erased after its unlock: %v", id, err) //nolint:gosec // G706: id is a validated case id (caseIDOf), err is ours
		return
	}
	h.exports().dropCase(string(id))
	h.noteErased(id)
	log.Printf("Erased expired case %s after its key was unlocked", id) //nolint:gosec // G706: id is a validated case id (caseIDOf), err is ours
}

// caseExpired says whether the store lists the case as one the sweep would erase now.
func (h *handler) caseExpired(ctx context.Context, id casestore.CaseID) bool {
	info, found, err := h.caseRow(ctx, id)
	return err == nil && found && casestore.SweepDue(info, time.Now())
}
