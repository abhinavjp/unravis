package server

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"api-audit-explorer/internal/casekey"
	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/masterkey"
)

// P4 wiring into P3: a case with a key of its own is archived under that key, and
// the key check value in the record decides whether a key is the file's (A12, A13,
// spec 7.6 line 204, M12 and M13).

// seedKeyed makes a case with its own key (ckPass) and n events.
func (a *archPC) seedKeyed(title string, n int) string {
	a.t.Helper()
	return a.seedBody(title, n, ckCreateBody(title, ckPass, ckPass))
}

func (a *archPC) caseKeyCall(id, action, body string) *httptest.ResponseRecorder {
	return a.do(http.MethodPost, "/api/cases/"+id+"/key/"+action, body)
}

func TestOwnKeyArchiveVerifyFreeRestoreRoundTrip(t *testing.T) {
	a := newArchPC(t, cfsPassA)
	id := a.seedKeyed("own key case", 4)
	dir := a.folder()

	a.archiveDone(id, dir, "open")
	cur := a.entry(id).Archive.Current()
	check, err := a.store.CaseKeyCheck(casestore.CaseID(id))
	if err != nil {
		t.Fatal(err)
	}
	if cur.KeyKind != casestore.KeyKindCase || cur.KeyCheck != check || len(cur.KeyEnding()) != 4 {
		t.Fatalf("file = %+v, want kind %q with the case key check", cur, casestore.KeyKindCase)
	}
	// The key shown is the case key's, not the master key's.
	if cur.KeyCheck == a.handler().mk.masterCheck() {
		t.Fatal("the file carries the master key check")
	}

	// Verify and Free with the case unlocked need nothing typed.
	a.verifyDone(id)
	if res := a.freeReq(id); res.Code != http.StatusOK {
		t.Fatalf("free = %d %s", res.Code, res.Body)
	}
	if !a.freed(id) {
		t.Fatal("not freed")
	}
	if _, err := os.Stat(filepath.Join(a.root, id, "case.wrap")); err != nil {
		t.Fatalf("the key file of the case went with its working copy: %v", err)
	}

	// Restore as new while the case key is unlocked: the file opens with it.
	res := a.req(http.MethodPost, "/api/cases/"+id+"/restore-as-new", `{"passphrase":`+q(ckPass)+`}`, archivedBelief)
	if res.Code != http.StatusCreated {
		t.Fatalf("restore as new = %d %s", res.Code, res.Body)
	}
	var dto caseDTO
	decodeBody(t, res, &dto)
	if n := a.eventCount(dto.ID); n != 4 {
		t.Fatalf("restored events = %d", n)
	}

	// Lock the case key: its record is sealed under it, so nothing about the case is
	// read until the key is unlocked (423, the case-locked answer), then the file opens.
	if err := a.store.LockCaseKey(casestore.CaseID(id)); err != nil {
		t.Fatal(err)
	}
	archReason(t, a.req(http.MethodPost, "/api/cases/"+id+"/restore-as-new", `{}`, archivedBelief), http.StatusLocked, "case-locked", caseLockedText)
	if res := a.caseKeyCall(id, "unlock", `{"passphrase":`+q(ckPass2)+`}`); res.Code == http.StatusOK {
		t.Fatal("a wrong case passphrase unlocked the freed case")
	}
	if res := a.caseKeyCall(id, "unlock", `{"passphrase":`+q(ckPass)+`}`); res.Code != http.StatusOK {
		t.Fatalf("unlock of a freed case = %d %s", res.Code, res.Body)
	}
	res = a.req(http.MethodPost, "/api/cases/"+id+"/restore-as-new", `{"passphrase":`+q(ckPass)+`}`, archivedBelief)
	if res.Code != http.StatusCreated {
		t.Fatalf("restore with the case unlocked = %d %s", res.Code, res.Body)
	}
	if strings.Contains(res.Body.String(), ckPass) || strings.Contains(a.logs.String(), ckPass) {
		t.Fatal("the case passphrase reached an answer or the log")
	}

	// The same file opens on another PC with the case passphrase alone.
	other := newCFSPC(t, cfsPassB, true)
	if res := other.restore(a.casePath(id), ckPass); res.Code != http.StatusCreated {
		t.Fatalf("restore on another PC = %d %s", res.Code, res.Body)
	}
	if res := other.restore(a.casePath(id), cfsPassA); res.Code == http.StatusCreated {
		t.Fatal("the master passphrase opened a case-key file")
	}
}

// A keyed case whose key is locked cannot be archived: the file would need the key.
func TestOwnKeyArchiveNeedsTheKeyUnlocked(t *testing.T) {
	a := newArchPC(t, cfsPassA)
	id := a.seedKeyed("locked keyed", 3)
	if err := a.store.LockCaseKey(casestore.CaseID(id)); err != nil {
		t.Fatal(err)
	}
	dir := a.folder()
	res := a.archiveReq(id, dir, "open")
	if res.Code != http.StatusLocked {
		t.Fatalf("archive of a locked keyed case = %d %s", res.Code, res.Body)
	}
	if len(caseFilesIn(t, dir)) != 0 || len(partsIn(t, dir)) != 0 {
		t.Fatal("a refused archive left a file")
	}
}

// A key changed after the archive is not the file's key: the check value says so
// before anything is read or removed.
func TestOwnKeyWrongKeyIsDetectedByTheCheckValue(t *testing.T) {
	a := newArchPC(t, cfsPassA)
	id := a.seedKeyed("changing key", 3)
	dir := a.folder()
	a.archiveDone(id, dir, "open")
	a.verifyDone(id)
	oldCheck := a.entry(id).Archive.Current().KeyCheck

	res := a.caseKeyCall(id, "change", `{"passphrase":`+q(ckPass)+`,"newPassphrase":`+q(ckPass2)+`,"confirm":`+q(ckPass2)+`}`)
	if res.Code != http.StatusOK {
		t.Fatalf("change key = %d %s", res.Code, res.Body)
	}
	if now, _ := a.store.CaseKeyCheck(casestore.CaseID(id)); now == oldCheck {
		t.Fatal("the check value did not change with the key")
	}
	// Free: M13, working copy kept.
	archReason(t, a.freeReq(id), http.StatusConflict, "old-key-only", oldKeyOnlyText)
	if !a.dbExists(id) {
		t.Fatal("working copy removed under another key")
	}
	// Verify without a passphrase: M13. With the new key's passphrase: the check
	// value refuses it. With the one the file was made with: it opens.
	archReason(t, a.req(http.MethodPost, "/api/cases/"+id+"/verify", `{}`, archivedBelief), http.StatusConflict, "old-key-only", oldKeyOnlyText)
	if res := a.req(http.MethodPost, "/api/cases/"+id+"/verify", `{"passphrase":`+q(ckPass2)+`}`, archivedBelief); res.Code != http.StatusAccepted {
		t.Fatalf("verify = %d %s", res.Code, res.Body)
	}
	if j := a.wait(id, "verify"); j.State != fileJobFailed || j.Reason != "check-failed" {
		t.Fatalf("verify with the new key's passphrase = %+v", j)
	}
	if res := a.req(http.MethodPost, "/api/cases/"+id+"/verify", `{"passphrase":`+q(ckPass)+`}`, archivedBelief); res.Code != http.StatusAccepted {
		t.Fatalf("verify = %d %s", res.Code, res.Body)
	}
	if j := a.wait(id, "verify"); j.State != fileJobDone {
		t.Fatalf("verify with the passphrase the file was made with = %+v", j)
	}
}

// The deriver itself: a key whose check value is not the file's is refused before
// the file is tried, and an empty check value (a file from before P3) refuses nothing.
func TestCheckedKeyRefusesAKeyThatIsNotTheFiles(t *testing.T) {
	p := cfsFast
	salt := []byte("0123456789abcdef")
	right, err := masterkey.DeriveKey([]byte(ckPass), salt, p)
	if err != nil {
		t.Fatal(err)
	}
	want := casekey.Check(right)
	if got, err := checkedKey(casestore.PassphraseKey([]byte(ckPass)), want)(salt, p); err != nil || string(got) != string(right) {
		t.Fatalf("right key = %v", err)
	}
	if _, err := checkedKey(casestore.PassphraseKey([]byte(ckPass2)), want)(salt, p); !errors.Is(err, errWrongCaseKey) {
		t.Fatalf("wrong key err = %v, want errWrongCaseKey", err)
	}
	if _, err := checkedKey(casestore.PassphraseKey([]byte(ckPass2)), "")(salt, p); err != nil {
		t.Fatalf("no check value should refuse nothing: %v", err)
	}
}

// M12 counts freed archives sealed under the master key only; a case with its own
// key is counted nowhere in it, and its freed archive survives a master key reset.
func TestOwnKeyArchivesAreNotInM12AndSurviveAReset(t *testing.T) {
	a := newArchPC(t, cfsPassA)

	masterID, _, _ := a.archivedCase("master sealed", 3)
	a.verifyDone(masterID)
	if res := a.freeReq(masterID); res.Code != http.StatusOK {
		t.Fatalf("free = %d %s", res.Code, res.Body)
	}
	ownID := a.seedKeyed("own key freed", 3)
	a.archiveDone(ownID, a.folder(), "open")
	a.verifyDone(ownID)
	if res := a.freeReq(ownID); res.Code != http.StatusOK {
		t.Fatalf("free of an own-key case = %d %s", res.Code, res.Body)
	}
	keptID := a.seedKeyed("own key kept", 3) // a working copy that a reset leaves alone
	_ = keptID

	a.damageKey()
	s := a.keyStatus()
	if s.State != "damaged" || s.FreedArchives != 1 {
		t.Fatalf("status = %+v, want 1 freed archive (the master-sealed one)", s)
	}
	if s.Reset == nil || s.Reset.ArchiveOnly != 1 || s.Reset.OwnKeyCases != 1 {
		t.Fatalf("reset impact = %+v, want ArchiveOnly 1 and OwnKeyCases 1", s.Reset)
	}

	// After the reset the own-key archive opens with its case passphrase: it was
	// never under the master key.
	if res := a.do(http.MethodPost, "/api/master-key/reset-damaged", `{"acknowledged":true}`); res.Code != http.StatusOK {
		t.Fatalf("reset = %d %s", res.Code, res.Body)
	}
	if res := a.do(http.MethodPost, "/api/master-key/setup", setupBody(cfsPassB, cfsPassB, true)); res.Code != http.StatusOK {
		t.Fatalf("setup = %d %s", res.Code, res.Body)
	}
	if err := a.store.LockCaseKey(casestore.CaseID(ownID)); err != nil {
		t.Fatal(err)
	}
	if res := a.caseKeyCall(ownID, "unlock", `{"passphrase":`+q(ckPass)+`}`); res.Code != http.StatusOK {
		t.Fatalf("unlock = %d %s", res.Code, res.Body)
	}
	res := a.req(http.MethodPost, "/api/cases/"+ownID+"/restore-as-new", `{"passphrase":`+q(ckPass)+`}`, archivedBelief)
	if res.Code != http.StatusCreated {
		t.Fatalf("restore of the own-key archive after a reset = %d %s", res.Code, res.Body)
	}
	// The master-sealed one is sealed under the key that was reset: the new master key
	// does not open it.
	if res := a.req(http.MethodPost, "/api/cases/"+masterID+"/restore-as-new", `{}`, archivedBelief); res.Code == http.StatusCreated {
		t.Fatalf("a master-sealed archive opened after the master key was reset: %s", res.Body)
	}
}

// Export of a case that is being erased says so (M5); it is not "not found".
func TestExportOfAnErasingCaseGivesM5(t *testing.T) {
	a := newArchPC(t, cfsPassA)
	id := a.seed("erasing", 2)
	// An erase holds the slot and is draining readers: new reads are refused.
	g, cid := &a.handler().gates, casestore.CaseID(id)
	g.mu.Lock()
	gate := g.ref(cid)
	gate.erasing = true
	g.mu.Unlock()
	defer func() {
		g.mu.Lock()
		gate.erasing = false
		g.unref(cid, gate)
		g.mu.Unlock()
	}()
	res := a.export(id, filepath.Join(t.TempDir(), "x"+casestore.CaseFileExt), cfsPassA)
	archReason(t, res, http.StatusConflict, "case-erasing", caseErasingText)
}

// A typed passphrase sent for a keyed case is refused, not ignored (D-A-26).
func TestOwnKeyArchiveRefusesATypedPassphrase(t *testing.T) {
	a := newArchPC(t, cfsPassA)
	id := a.seedKeyed("keyed", 3)
	dir := a.folder()
	res := a.req(http.MethodPost, "/api/cases/"+id+"/archive", `{"path":`+q(dir)+`,"passphrase":`+q(cfsPassA)+`}`, "open")
	archReason(t, res, http.StatusBadRequest, "bad-passphrase", archiveKeyedPassText)
	if strings.Contains(res.Body.String(), cfsPassA) {
		t.Fatal("the answer echoes the passphrase")
	}
	if a.state(id) != casestore.StateOpen || a.entry(id).Mark != "" {
		t.Fatalf("a refused archive changed the case: %q mark %q", a.state(id), a.entry(id).Mark)
	}
	if len(caseFilesIn(t, dir)) != 0 || len(partsIn(t, dir)) != 0 {
		t.Fatal("a refused archive left a file")
	}
	// Without a passphrase the same case archives, sealed by its own key.
	a.archiveDone(id, dir, "open")
	if kind := a.entry(id).Archive.Current().KeyKind; kind != casestore.KeyKindCase {
		t.Fatalf("key kind = %q, want the case key", kind)
	}
	// An unkeyed case with the master key unlocked still ignores a typed passphrase.
	plain := a.seed("plain", 2)
	res = a.req(http.MethodPost, "/api/cases/"+plain+"/archive", `{"path":`+q(a.folder())+`,"passphrase":"x"}`, "open")
	if res.Code != http.StatusAccepted {
		t.Fatalf("unkeyed archive = %d %s", res.Code, res.Body)
	}
}
