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

func TestScn1L2_BeliefBeforeInput_FreeAndRestoreAsNew(t *testing.T) {
	a := newArchPC(t, "")
	id := a.seed("Alpha", 3)
	dir := a.folder()
	res := a.req(http.MethodPost, "/api/cases/"+id+"/archive", `{"path":`+q(dir)+`,"passphrase":`+q(cfsPassA)+`}`, "open")
	if res.Code != http.StatusAccepted {
		t.Fatalf("archive = %d %s", res.Code, res.Body)
	}
	if j := a.wait(id, "archive"); j.State != fileJobDone {
		t.Fatalf("archive job = %+v", j)
	}
	t.Run("free, stale belief", func(t *testing.T) {
		res := a.req(http.MethodPost, "/api/cases/"+id+"/free", `{"confirm":`+q(id)+`}`, "open")
		archReason(t, res, http.StatusConflict, "case-changed", m2Text)
	})
	t.Run("free, right belief", func(t *testing.T) {
		archReason(t, a.freeReq(id), http.StatusConflict, "no-master-key", "")
	})
	t.Run("restore as new, stale belief", func(t *testing.T) {
		res := a.req(http.MethodPost, "/api/cases/"+id+"/restore-as-new", `{}`, "open")
		archReason(t, res, http.StatusConflict, "case-changed", m2Text)
	})
	t.Run("restore as new, right belief", func(t *testing.T) {
		res := a.req(http.MethodPost, "/api/cases/"+id+"/restore-as-new", `{}`, "archived")
		archReason(t, res, http.StatusConflict, "no-master-key", "")
	})
}
