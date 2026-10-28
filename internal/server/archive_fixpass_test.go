package server

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
)

// P4 fix pass. R1: locking a keyed case stops its archive with the key as the reason,
// and Erase answers M15 for as long as that archive is still running, whatever the
// row says (a locked case's row shows no Archiving mark).
func TestLockingAKeyedCaseStopsItsArchiveAndEraseSaysM15(t *testing.T) {
	a := newArchPC(t, cfsPassA)
	id := a.seedKeyed("lock during archive", 3)
	p := pauseAt(t, "part-written")
	if res := a.archiveReq(id, a.folder(), "open"); res.Code != http.StatusAccepted {
		t.Fatalf("archive = %d %s", res.Code, res.Body)
	}
	p.waitReached(t)
	job := a.handler().caseFiles().jobs.get(jobArchive, casestore.CaseID(id))

	locked := make(chan int, 1)
	go func() { locked <- a.caseKeyCall(id, "lock", ``).Code }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, keyLocked := job.wasCancelled(); keyLocked {
			break
		}
		if time.Now().After(deadline) {
			p.release()
			t.Fatal("the lock never reached the archive job")
		}
		time.Sleep(5 * time.Millisecond)
	}
	// The archive is still writing: Erase is refused with M15 and touches nothing.
	res := a.req(http.MethodDelete, "/api/cases/"+id, `{"confirm":`+q(id)+`}`, "")
	archReason(t, res, http.StatusConflict, "cancel-archive-first", cancelArchiveText)
	p.release()
	select {
	case code := <-locked:
		if code != http.StatusOK {
			t.Fatalf("lock = %d", code)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the lock never finished")
	}
	<-job.done
	if j := a.jobOf(id, "archive"); j.State != fileJobStopped || j.Reason != "key-locked" {
		t.Fatalf("archive job = %+v, want stopped by the key", j)
	}
	if !a.dbExists(id) {
		t.Fatal("the working copy went")
	}
	// Locked and quiet: the person may now erase it (the key is not needed for that).
	if res := a.req(http.MethodDelete, "/api/cases/"+id, `{"confirm":`+q(id)+`}`, ""); res.Code != http.StatusNoContent {
		t.Fatalf("erase after the job ended = %d %s", res.Code, res.Body)
	}
}

// R2: restoring a keyed archive gives a keyed case. The case passphrase is typed
// (no silent downgrade to the master key), a wrong one is refused, and the same
// goes for the path route of a kept archive file.
func TestRestoreOfAKeyedArchiveKeepsTheCaseKey(t *testing.T) {
	a := newArchPC(t, cfsPassA)
	id := a.seedKeyed("keeps its key", 3)
	a.archiveDone(id, a.folder(), "open")
	a.verifyDone(id)

	restore := a.casePath(id)
	body := func(pass string) string { return `{"passphrase":` + q(pass) + `}` }
	archReason(t, a.req(http.MethodPost, "/api/cases/"+id+"/restore-as-new", `{}`, archivedBelief),
		http.StatusConflict, "case-key-needed", caseKeyNeededText)
	res := a.req(http.MethodPost, "/api/cases/"+id+"/restore-as-new", body(ckPass2), archivedBelief)
	if res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("wrong case key = %d %s", res.Code, res.Body)
	}
	if res := a.req(http.MethodPost, "/api/cases/"+id+"/restore-as-new", body(cfsPassA), archivedBelief); res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("the master passphrase = %d", res.Code)
	}

	res = a.req(http.MethodPost, "/api/cases/"+id+"/restore-as-new", body(ckPass), archivedBelief)
	if res.Code != http.StatusCreated {
		t.Fatalf("restore = %d %s", res.Code, res.Body)
	}
	var dto caseDTO
	decodeBody(t, res, &dto)
	info, err := a.store.CaseKey(casestore.CaseID(dto.ID))
	if err != nil || !info.Has {
		t.Fatalf("restored case key = %+v, %v: it was turned into a master-key case", info, err)
	}
	// The key is the typed one: lock, then only it unlocks.
	if err := a.store.LockCaseKey(casestore.CaseID(dto.ID)); err != nil {
		t.Fatal(err)
	}
	if res := a.caseKeyCall(dto.ID, "unlock", `{"passphrase":`+q(cfsPassA)+`}`); res.Code == http.StatusOK {
		t.Fatal("the master passphrase unlocked the restored case")
	}
	if res := a.caseKeyCall(dto.ID, "unlock", body(ckPass)); res.Code != http.StatusOK {
		t.Fatalf("unlock = %d %s", res.Code, res.Body)
	}
	if strings.Contains(a.logs.String(), ckPass) {
		t.Fatal("the case passphrase reached the log")
	}

	// The same file restored from its path keeps the key too.
	res = a.restore(restore, ckPass)
	if res.Code != http.StatusCreated {
		t.Fatalf("restore from path = %d %s", res.Code, res.Body)
	}
	decodeBody(t, res, &dto)
	if info, err := a.store.CaseKey(casestore.CaseID(dto.ID)); err != nil || !info.Has {
		t.Fatalf("path restore case key = %+v, %v", info, err)
	}
}

// R5 and R8: Change and Remove are refused (M15) while the case is being archived;
// the key status of an erase-pending case answers M5.
func TestChangeKeyRefusedWhileArchivingAndStatusOfErasingCase(t *testing.T) {
	a := newArchPC(t, cfsPassA)
	id := a.seedKeyed("change during archive", 3)
	p := pauseAt(t, "part-written")
	if res := a.archiveReq(id, a.folder(), "open"); res.Code != http.StatusAccepted {
		t.Fatalf("archive = %d %s", res.Code, res.Body)
	}
	p.waitReached(t)
	change := `{"passphrase":` + q(ckPass) + `,"newPassphrase":` + q(ckPass2) + `,"confirm":` + q(ckPass2) + `}`
	archReason(t, a.caseKeyCall(id, "change", change), http.StatusConflict, "cancel-archive-first", cancelArchiveText)
	archReason(t, a.caseKeyCall(id, "remove", `{"passphrase":`+q(ckPass)+`}`), http.StatusConflict, "cancel-archive-first", cancelArchiveText)
	p.release()
	a.wait(id, "archive")
	if res := a.caseKeyCall(id, "change", change); res.Code != http.StatusOK {
		t.Fatalf("change after the archive = %d %s", res.Code, res.Body)
	}
	// An erase that was marked and cut short: the key status says so (M5).
	if err := os.WriteFile(filepath.Join(a.root, id, "erase.pending"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	archReason(t, a.do(http.MethodGet, "/api/cases/"+id+"/key", ``), http.StatusConflict, "case-erasing", caseErasingText)
}
