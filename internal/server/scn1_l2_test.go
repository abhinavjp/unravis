package server

import (
	"net/http"
	"testing"
)

// Lane L2 of the scenario fixes: a stale tab is answered before any input is judged
// (D-A-02).

func TestScn1L2_BeliefBeforeInput_ArchiveAndVerify(t *testing.T) {
	t.Run("archive, stale belief, bad folder", func(t *testing.T) {
		a := newArchPC(t, "")
		id := a.seed("Alpha", 3)
		res := a.req(http.MethodPost, "/api/cases/"+id+"/archive", `{"path":`+q(`Z:\no\such\dir`)+`}`, "archived")
		archReason(t, res, http.StatusConflict, "case-changed", m2Text)
	})
	t.Run("archive, right belief, bad folder", func(t *testing.T) {
		a := newArchPC(t, "")
		id := a.seed("Alpha", 3)
		res := a.req(http.MethodPost, "/api/cases/"+id+"/archive", `{"path":`+q(`Z:\no\such\dir`)+`}`, "open")
		archReason(t, res, http.StatusBadRequest, "bad-folder", archiveFolderText)
	})
	t.Run("archive, no header, bad folder", func(t *testing.T) {
		a := newArchPC(t, "")
		id := a.seed("Alpha", 3)
		res := a.req(http.MethodPost, "/api/cases/"+id+"/archive", `{"path":`+q(`Z:\no\such\dir`)+`}`, "")
		archReason(t, res, http.StatusBadRequest, "bad-folder", archiveFolderText)
	})
	t.Run("verify, stale belief, junk body", func(t *testing.T) {
		a := newArchPC(t, cfsPassA)
		id, _, _ := a.archivedCase("Beta", 3)
		res := a.req(http.MethodPost, "/api/cases/"+id+"/verify", `{"path":"x"}`, "open")
		archReason(t, res, http.StatusConflict, "case-changed", m2Text)
	})
	t.Run("verify, right belief, junk body", func(t *testing.T) {
		a := newArchPC(t, cfsPassA)
		id, _, _ := a.archivedCase("Beta", 3)
		res := a.req(http.MethodPost, "/api/cases/"+id+"/verify", `{"path":"x"}`, "archived")
		archReason(t, res, http.StatusBadRequest, "bad-request", "")
	})
}
