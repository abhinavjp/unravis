package server

import (
	"context"
	"net/http"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
)

// Review 1 of the scenario fixes.

// Review1-1: restore as new refuses a stale belief even for an archived case that
// carries a mark, where beliefFirst steps aside and CheckBelief takes any belief.
func TestScn1Review1_RestoreAsNewStaleBeliefOnMarkedCase(t *testing.T) {
	a := newArchPC(t, cfsPassA)
	id, _, _ := a.archivedCase("Alpha", 3)
	lc := casestore.Lifecycle(a.store)
	if err := lc.SetMark(context.Background(), casestore.CaseID(id), casestore.MarkFreeing); err != nil {
		t.Fatal(err)
	}
	res := a.req(http.MethodPost, "/api/cases/"+id+"/restore-as-new", `{}`, "open")
	archReason(t, res, http.StatusConflict, "case-changed", m2Text)
}

// Review1-2: an erase refused under the gate because the case closed does not put the recording back.
func TestScn1Review1_RefusedEraseKeepsTheRecording(t *testing.T) {
	a := newArchPC(t, cfsPassA)
	id := a.seed("Alpha", 3)
	a.startRecording(id)
	h := a.handler()
	// The case changes after the first belief check and before the gate is taken,
	// so the check under the gate is the one that refuses.
	h.eraseAfter = func(time.Duration) <-chan time.Time {
		if res := a.req(http.MethodPost, "/api/cases/"+id+"/close", "", "open"); res.Code != http.StatusOK {
			t.Errorf("close = %d %s", res.Code, res.Body)
		}
		return nil
	}
	res := a.req(http.MethodDelete, "/api/cases/"+id, `{"confirm":`+q(id)+`}`, "open")
	archReason(t, res, http.StatusConflict, "case-changed", m2Text)
	// The stale belief is the case being closed, and a closed case takes no
	// recording (Review2-7), so nothing is put back. A refusal that leaves the case
	// open still puts the recording back (TestF5 cancel restarts it).
	if a.recording(id) {
		t.Fatal("the refused erase restarted the recording on a closed case")
	}
}
