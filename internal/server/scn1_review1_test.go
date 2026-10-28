package server

import (
	"context"
	"net/http"
	"testing"

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
