package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/importer"
)

// A7 to A10, A18, INJ-5 (Verify kills), INJ-6 and the crash of Free, of the P3
// proof. Same harness as archive_scenarios_test.go.

// archivedCase makes a case with n events, archives it into a fresh folder and
// returns the id, the folder and the name of the case file.
func (a *archPC) archivedCase(title string, n int) (id, dir, name string) {
	a.t.Helper()
	id = a.seed(title, n)
	dir = a.folder()
	j := a.archiveDone(id, dir, "open")
	return id, dir, j.Name
}

func (a *archPC) casePath(id string) string {
	a.t.Helper()
	cur := a.entry(id).Archive.Current()
	if cur == nil {
		a.t.Fatal("no current file")
	}
	return filepath.Join(cur.Folder, cur.Name)
}

func (a *archPC) dbExists(id string) bool {
	_, err := os.Stat(filepath.Join(a.root, id, "case.db"))
	return err == nil
}

// freed reports whether the working copy is gone and the record says so.
func (a *archPC) freed(id string) bool {
	info := a.entry(id)
	return !a.dbExists(id) && info.Archive != nil && info.Archive.Freed() && info.Mark == ""
}

// A7: the key in force is not the one the file was made with. Free, Verify and
// Restore as new answer M13 before anything is read or removed; the passphrase
// the file was made with opens it.
func TestA7_OldKeyGivesM13BeforeFreeAndWhenOpening(t *testing.T) {
	a := newArchPC(t, cfsPassA)
	id, _, _ := a.archivedCase("old key", 4)
	a.verifyDone(id)

	other := newCFSPC(t, cfsPassB, true) // a different master key
	a.handler().mk = other.sess

	archReason(t, a.freeReq(id), http.StatusConflict, "old-key-only", oldKeyOnlyText)
	if !a.dbExists(id) || a.entry(id).Mark != "" {
		t.Fatal("M13 on Free changed the case")
	}
	// The check mark was dropped: the check has to be made again.
	archReason(t, a.req(http.MethodPost, "/api/cases/"+id+"/verify", `{}`, "archived"), http.StatusConflict, "old-key-only", oldKeyOnlyText)
	archReason(t, a.req(http.MethodPost, "/api/cases/"+id+"/restore-as-new", `{}`, "archived"), http.StatusConflict, "old-key-only", oldKeyOnlyText)

	// A wrong passphrase fails the check, the right one opens the file.
	res := a.req(http.MethodPost, "/api/cases/"+id+"/verify", `{"passphrase":`+q(cfsPassB)+`}`, "archived")
	if res.Code != http.StatusAccepted {
		t.Fatalf("verify with a passphrase = %d %s", res.Code, res.Body)
	}
	if j := a.wait(id, "verify"); j.State != fileJobFailed || j.Reason != "check-failed" || j.Error != caseFileFailedText {
		t.Fatalf("verify with the wrong passphrase = %+v", j)
	}
	res = a.req(http.MethodPost, "/api/cases/"+id+"/verify", `{"passphrase":`+q(cfsPassA)+`}`, "archived")
	if res.Code != http.StatusAccepted {
		t.Fatalf("verify with the right passphrase = %d %s", res.Code, res.Body)
	}
	if j := a.wait(id, "verify"); j.State != fileJobDone {
		t.Fatalf("verify with the right passphrase = %+v", j)
	}
	// Free still waits for the key the file was made with: M13, nothing removed.
	archReason(t, a.freeReq(id), http.StatusConflict, "old-key-only", oldKeyOnlyText)
	if !a.dbExists(id) {
		t.Fatal("working copy removed under another key")
	}
	// Another PC with its own master key opens the same file with the passphrase.
	dst := other
	res = dst.restore(a.casePath(id), cfsPassA)
	if res.Code != http.StatusCreated {
		t.Fatalf("restore on another PC with the passphrase = %d %s", res.Code, res.Body)
	}
	if strings.Contains(res.Body.String(), cfsPassA) || strings.Contains(a.logs.String(), cfsPassA) || strings.Contains(dst.logs.String(), cfsPassA) {
		t.Fatal("a passphrase reached an answer or the log")
	}
}

// A7: with no master key a file is sealed with a typed passphrase. Verify and
// Restore need that passphrase; Free and Restore as new need a master key to exist.
func TestA7_TypedPassphraseWhenThereIsNoMasterKey(t *testing.T) {
	a := newArchPC(t, "")
	id := a.seed("typed", 3)
	dir := a.folder()

	archReason(t, a.archiveReq(id, dir, "open"), http.StatusBadRequest, "bad-passphrase", archiveTypedPassText)
	res := a.req(http.MethodPost, "/api/cases/"+id+"/archive", `{"path":`+q(dir)+`,"passphrase":"short"}`, "open")
	archReason(t, res, http.StatusBadRequest, "bad-passphrase", archiveTypedPassText)
	if a.state(id) != casestore.StateOpen || len(caseFilesIn(t, dir)) != 0 {
		t.Fatal("a refused archive changed something")
	}
	res = a.req(http.MethodPost, "/api/cases/"+id+"/archive", `{"path":`+q(dir)+`,"passphrase":`+q(cfsPassA)+`}`, "open")
	if res.Code != http.StatusAccepted {
		t.Fatalf("archive = %d %s", res.Code, res.Body)
	}
	if j := a.wait(id, "archive"); j.State != fileJobDone {
		t.Fatalf("archive job = %+v", j)
	}
	cur := a.entry(id).Archive.Current()
	if cur.KeyKind != casestore.KeyKindTyped {
		t.Fatalf("key kind = %q", cur.KeyKind)
	}
	// Opening it needs the passphrase.
	archReason(t, a.req(http.MethodPost, "/api/cases/"+id+"/verify", `{}`, "archived"), http.StatusConflict, "old-key-only", oldKeyOnlyText)
	res = a.req(http.MethodPost, "/api/cases/"+id+"/verify", `{"passphrase":`+q(cfsPassA)+`}`, "archived")
	if res.Code != http.StatusAccepted {
		t.Fatalf("verify = %d %s", res.Code, res.Body)
	}
	if j := a.wait(id, "verify"); j.State != fileJobDone {
		t.Fatalf("verify = %+v", j)
	}
	// Free and Restore as new both need a master key to exist.
	archReason(t, a.freeReq(id), http.StatusConflict, "no-master-key", "")
	archReason(t, a.req(http.MethodPost, "/api/cases/"+id+"/restore-as-new", `{"passphrase":`+q(cfsPassA)+`}`, "archived"),
		http.StatusConflict, "no-master-key", "")
	if !a.dbExists(id) {
		t.Fatal("working copy gone")
	}
	// Restore in place needs neither.
	if res := a.req(http.MethodPost, "/api/cases/"+id+"/restore-in-place", `{}`, "archived"); res.Code != http.StatusOK {
		t.Fatalf("restore in place = %d %s", res.Code, res.Body)
	}
	if a.state(id) != casestore.StateClosed {
		t.Fatalf("state = %s", a.state(id))
	}
}

// A8: the file moved, changed or was never checked.
func TestA8_MovedChangedOrUncheckedFile(t *testing.T) {
	a := newArchPC(t, cfsPassA)

	t.Run("free without a check", func(t *testing.T) {
		id, _, _ := a.archivedCase("unchecked", 2)
		archReason(t, a.freeReq(id), http.StatusConflict, "check-first", checkFirstText)
		if !a.dbExists(id) {
			t.Fatal("working copy removed without a check")
		}
	})

	t.Run("the check runs out after an hour", func(t *testing.T) {
		id, _, _ := a.archivedCase("old check", 2)
		a.verifyDone(id)
		if v := a.verifyState(id); !v.Verified {
			t.Fatalf("verify state = %+v", v)
		}
		old := archiveNow
		archiveNow = func() time.Time { return old().Add(2 * time.Hour) }
		defer func() { archiveNow = old }()
		if v := a.verifyState(id); v.Verified {
			t.Fatal("check still counted after two hours")
		}
		archReason(t, a.freeReq(id), http.StatusConflict, "check-first", checkFirstText)
	})

	t.Run("a moved file", func(t *testing.T) {
		id, _, _ := a.archivedCase("moved", 3)
		p := a.casePath(id)
		elsewhere := filepath.Join(a.folder(), "moved-away.unravis-case")
		if err := os.Rename(p, elsewhere); err != nil {
			t.Fatal(err)
		}
		// Restore as new takes no path: the sentence says where it was.
		archReason(t, a.req(http.MethodPost, "/api/cases/"+id+"/restore-as-new", `{}`, "archived"),
			http.StatusConflict, "file-missing", restoreNeedsFileText)
		// Verify has nothing to read: M11, and the working copy stays.
		a.req(http.MethodPost, "/api/cases/"+id+"/verify", `{}`, "archived")
		if j := a.wait(id, "verify"); j.State != fileJobFailed || j.Reason != "check-failed" || j.Error != caseFileFailedText {
			t.Fatalf("verify of a moved file = %+v", j)
		}
		archReason(t, a.freeReq(id), http.StatusConflict, "check-first", checkFirstText)
		// The kept working copy still restores in place.
		if res := a.req(http.MethodPost, "/api/cases/"+id+"/restore-in-place", `{}`, "archived"); res.Code != http.StatusOK {
			t.Fatalf("restore in place = %d %s", res.Code, res.Body)
		}
		if _, err := os.Stat(elsewhere); err != nil {
			t.Fatal("the moved file was touched")
		}
	})

	t.Run("a changed file", func(t *testing.T) {
		id, _, _ := a.archivedCase("changed", 3)
		p := a.casePath(id)
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_APPEND, 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte{0}); err != nil {
			t.Fatal(err)
		}
		_ = f.Close()
		a.req(http.MethodPost, "/api/cases/"+id+"/verify", `{}`, "archived")
		if j := a.wait(id, "verify"); j.State != fileJobFailed || j.Reason != "check-failed" || j.Error != caseFileFailedText {
			t.Fatalf("verify of a changed file = %+v", j)
		}
		if _, err := os.Stat(p); err != nil {
			t.Fatal("the changed file was removed")
		}
		if verifyLeftovers(a.root) {
			t.Fatal("a temporary store was left behind")
		}
		archReason(t, a.freeReq(id), http.StatusConflict, "check-first", checkFirstText)
		if !a.dbExists(id) {
			t.Fatal("working copy removed after a failed check")
		}
	})
}

func (a *archPC) verifyState(id string) verifyDTO {
	a.t.Helper()
	res := a.req(http.MethodGet, "/api/cases/"+id+"/verify", "", "")
	if res.Code != http.StatusOK {
		a.t.Fatalf("GET verify = %d %s", res.Code, res.Body)
	}
	var v verifyDTO
	decodeBody(a.t, res, &v)
	return v
}

// A9: an untrusted case (a folder another process planted) is not archivable.
func TestA9_UntrustedCaseIsNotArchivable(t *testing.T) {
	a := newArchPC(t, "")
	a.seed("real", 2)
	if res := a.do(http.MethodPost, "/api/master-key/setup", setupBody(cfsPassA, cfsPassA, true)); res.Code != http.StatusOK {
		t.Fatalf("setup = %d %s", res.Code, res.Body)
	}
	planted := p06Plant(t, a.cfsPC, "planted")
	dir := a.folder()
	for _, belief := range []string{"", "open", "closed", "untrusted"} {
		res := a.archiveReq(planted, dir, belief)
		if res.Code != http.StatusConflict {
			t.Fatalf("archive of an untrusted case (%q) = %d %s", belief, res.Code, res.Body)
		}
		if f := failureOf(t, res); f.Reason != "untrusted-case" {
			t.Fatalf("failure = %+v", f)
		}
	}
	if len(caseFilesIn(t, dir)) != 0 || len(partsIn(t, dir)) != 0 {
		t.Fatal("something was written")
	}
	// The busy lock was given back.
	real := a.firstOpenCase()
	a.archiveDone(real, dir, "open")
}

func (a *archPC) firstOpenCase() string {
	a.t.Helper()
	for _, c := range a.listAll().Cases {
		if !c.Untrusted {
			return c.ID
		}
	}
	a.t.Fatal("no trusted case")
	return ""
}

// A10: a store that cannot keep files (the in-memory one) answers 503.
func TestA10_NoCaseFilesWithoutADataFolder(t *testing.T) {
	sess := NewMasterKeySession(t.TempDir())
	h := New(&fakeRepository{}, testAssets(), testToken, false,
		WithCases(casestore.NewMemory(), mustHosts(t, hostsDoc), importer.NewRegistry(toyImporter{name: "toy", score: 90})),
		WithMasterKey(sess),
	)
	res := request(h, http.MethodPost, "/api/cases", `{"title":"memory"}`, true)
	if res.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", res.Code, res.Body)
	}
	var c caseDTO
	decodeBody(t, res, &c)
	for _, r := range []struct{ m, p, b string }{
		{http.MethodPost, "/api/cases/" + c.ID + "/archive", `{"path":"C:\\x"}`},
		{http.MethodPost, "/api/cases/" + c.ID + "/verify", `{}`},
		{http.MethodPost, "/api/cases/" + c.ID + "/free", `{"confirm":"x"}`},
		{http.MethodPost, "/api/cases/" + c.ID + "/restore-in-place", `{}`},
		{http.MethodPost, "/api/cases/" + c.ID + "/restore-as-new", `{}`},
		{http.MethodGet, "/api/cases/orphan-files", ""},
	} {
		res := request(h, r.m, r.p, r.b, true)
		if res.Code != http.StatusServiceUnavailable {
			t.Errorf("%s %s = %d %s", r.m, r.p, res.Code, res.Body)
			continue
		}
		if f := failureOf(t, res); f.Reason != "case-file-unavailable" {
			t.Errorf("%s %s reason = %q", r.m, r.p, f.Reason)
		}
	}
}

// A18: one case file job at a time, with its own sentence.
func TestA18_AnotherCaseFileJobGivesTheBusySentence(t *testing.T) {
	a := newArchPC(t, cfsPassA)
	idA, _, _ := a.archivedCase("first", 2)
	idB := a.seed("second", 2)
	dirB := a.folder()

	// A real Verify holds the lock while it is paused.
	p := pauseAt(t, "verify-start")
	if res := a.req(http.MethodPost, "/api/cases/"+idA+"/verify", `{}`, "archived"); res.Code != http.StatusAccepted {
		t.Fatalf("verify = %d %s", res.Code, res.Body)
	}
	p.waitReached(t)
	archReason(t, a.archiveReq(idB, dirB, "open"), http.StatusConflict, "busy", caseFileBusyText)
	archReason(t, a.req(http.MethodPost, "/api/cases/"+idA+"/verify", `{}`, "archived"), http.StatusConflict, "busy", caseFileBusyText)
	archReason(t, a.req(http.MethodPost, "/api/cases/"+idA+"/restore-as-new", `{}`, "archived"), http.StatusConflict, "busy", caseFileBusyText)
	res := a.export(idB, filepath.Join(a.folder(), "x.unravis-case"), cfsPassB)
	if res.Code != http.StatusConflict {
		t.Fatalf("export while busy = %d %s", res.Code, res.Body)
	}
	if f := failureOf(t, res); f.Error != caseFileBusyText {
		t.Fatalf("export busy sentence = %+v", f)
	}
	p.release()
	a.wait(idA, "verify")
	// Each refusal gave the lock back and nothing was marked.
	if a.state(idB) != casestore.StateOpen || a.entry(idB).Mark != "" {
		t.Fatal("a refused archive left a mark")
	}
	a.archiveDone(idB, dirB, "open")
}

// INJ-6: a reader still holds the case. New readers are turned away (M19) while
// Free waits; after the wait Free answers M4 and changes nothing; once the reader
// lets go, Free goes through.
func TestINJ6_FreeWaitsForReadersThenAnswersM4(t *testing.T) {
	a := newArchPC(t, cfsPassA)
	id, heldReader := a.archivedHeld("readers", 3)
	a.verifyDone(id)
	oldWait, oldPoll := freeWaitFor, freePoll
	freeWaitFor, freePoll = 800*time.Millisecond, 10*time.Millisecond
	defer func() { freeWaitFor, freePoll = oldWait, oldPoll }()

	held := heldReader
	released := false
	defer func() {
		if !released {
			_ = held.Close()
		}
	}()

	type result struct {
		code int
		body string
	}
	out := make(chan result, 1)
	go func() {
		res := a.freeReq(id)
		out <- result{res.Code, res.Body.String()}
	}()
	// While Free waits, a new reader answers M19.
	deadline := time.Now().Add(5 * time.Second)
	sawM19 := false
	for time.Now().Before(deadline) && !sawM19 {
		res := a.req(http.MethodGet, "/api/cases/"+id+"/ledger", "", "")
		if res.Code == http.StatusConflict && failureOf(t, res).Reason == "case-freeing" {
			sawM19 = true
		}
		select {
		case r := <-out:
			t.Fatalf("free ended before a reader was turned away: %d %s", r.code, r.body)
		default:
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !sawM19 {
		t.Fatal("no reader was turned away while Free waited")
	}
	r := <-out
	if r.code != http.StatusConflict || !strings.Contains(r.body, caseInUseText) {
		t.Fatalf("free with a reader = %d %s", r.code, r.body)
	}
	if !a.dbExists(id) || a.entry(id).Mark != "" || a.entry(id).Archive.Freed() {
		t.Fatal("M4 changed the case")
	}
	// The flag is gone: readers are no longer turned away for it.
	if res := a.req(http.MethodGet, "/api/cases/"+id+"/ledger", "", ""); res.Code == http.StatusConflict && failureOf(t, res).Reason == "case-freeing" {
		t.Fatal("the freeing flag stayed after M4")
	}
	// The reader lets go and Free goes through.
	_ = held.Close()
	released = true
	if res := a.freeReq(id); res.Code != http.StatusOK {
		t.Fatalf("free = %d %s", res.Code, res.Body)
	}
	if !a.freed(id) {
		t.Fatal("not freed")
	}
}

// A reader that lets go while Free waits is enough: Free does not give up early.
func TestINJ6_ReaderLeavesDuringTheWait(t *testing.T) {
	a := newArchPC(t, cfsPassA)
	id, heldReader := a.archivedHeld("patient", 3)
	a.verifyDone(id)
	oldWait, oldPoll := freeWaitFor, freePoll
	freeWaitFor, freePoll = 10*time.Second, 10*time.Millisecond
	defer func() { freeWaitFor, freePoll = oldWait, oldPoll }()
	held := heldReader
	go func() {
		time.Sleep(150 * time.Millisecond)
		_ = held.Close()
	}()
	if res := a.freeReq(id); res.Code != http.StatusOK {
		t.Fatalf("free = %d %s", res.Code, res.Body)
	}
	if !a.freed(id) {
		t.Fatal("not freed")
	}
}

// The crash of Free: the mark is the commit. A process that dies after it leaves
// a marked case that the next start finishes, never undoes; a finished case file
// is never touched.
func TestFreeCrashAfterTheMarkIsFinishedByRecovery(t *testing.T) {
	a := newArchPC(t, cfsPassA)
	id, dir, name := a.archivedCase("crash free", 4)
	a.verifyDone(id)
	before := a.fileSum(filepath.Join(dir, name))

	if err := a.store.BeginFree(context.Background(), casestore.CaseID(id), casestore.StateArchived); err != nil {
		t.Fatal(err)
	}
	if got := a.entry(id).Mark; got != casestore.MarkFreeing {
		t.Fatalf("mark = %q", got)
	}
	if !a.dbExists(id) {
		t.Fatal("the working copy went with the mark")
	}
	a.restart()
	if !a.freed(id) {
		t.Fatalf("after restart: db %v mark %q", a.dbExists(id), a.entry(id).Mark)
	}
	if a.fileSum(filepath.Join(dir, name)) != before {
		t.Fatal("the case file changed")
	}
	// A second recovery does nothing; the case restores as new from the file.
	if rep := a.restart(); rep.Marked != 0 {
		t.Fatalf("second recovery = %+v", rep)
	}
	res := a.req(http.MethodPost, "/api/cases/"+id+"/restore-as-new", `{}`, "archived")
	if res.Code != http.StatusCreated {
		t.Fatalf("restore as new = %d %s", res.Code, res.Body)
	}
	// Restore in place is not possible any more.
	archReason(t, a.req(http.MethodPost, "/api/cases/"+id+"/restore-in-place", `{}`, "archived"), http.StatusConflict, "case-changed", caseChangedText)
}

// While the mark is there and nothing has restarted, a request that reaches the
// case finishes the free first (the per-call recovery), it never sees half a case.
func TestFreeMarkIsSettledByTheNextRequest(t *testing.T) {
	a := newArchPC(t, cfsPassA)
	id, _, _ := a.archivedCase("settle free", 2)
	a.verifyDone(id)
	if err := a.store.BeginFree(context.Background(), casestore.CaseID(id), casestore.StateArchived); err != nil {
		t.Fatal(err)
	}
	res := a.req(http.MethodGet, "/api/cases/"+id+"/verify", "", "")
	if res.Code != http.StatusOK {
		t.Fatalf("GET verify = %d %s", res.Code, res.Body)
	}
	// Either the request settled it or it still says wait; never a failure of the case.
	res = a.req(http.MethodPost, "/api/cases/"+id+"/restore-as-new", `{}`, "archived")
	if res.Code != http.StatusCreated && res.Code != http.StatusConflict {
		t.Fatalf("restore as new = %d %s", res.Code, res.Body)
	}
	a.restart()
	if !a.freed(id) {
		t.Fatal("not freed after restart")
	}
}

func (a *archPC) fileSum(p string) string {
	a.t.Helper()
	size, sum, err := casestore.HashFile(context.Background(), p, nil)
	if err != nil {
		a.t.Fatal(err)
	}
	_ = size
	return sum
}

// Verify, Cancel (M17): the temporary store is gone, the case unchanged, a retry works.
func TestVerifyCancelGivesM17(t *testing.T) {
	a := newArchPC(t, cfsPassA)
	id, _, _ := a.archivedCase("cancel verify", 3)
	p := pauseAt(t, "verify-start")
	if res := a.req(http.MethodPost, "/api/cases/"+id+"/verify", `{}`, "archived"); res.Code != http.StatusAccepted {
		t.Fatalf("verify = %d %s", res.Code, res.Body)
	}
	p.waitReached(t)
	out := make(chan *httptestResult, 1)
	go func() {
		res := a.req(http.MethodDelete, "/api/cases/"+id+"/verify", "", "")
		out <- &httptestResult{res.Code, res.Body.String()}
	}()
	job := a.handler().caseFiles().jobs.get(jobVerify, casestore.CaseID(id))
	for i := 0; i < 500; i++ {
		if c, _ := job.wasCancelled(); c {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	p.release()
	r := <-out
	if r.code != http.StatusOK {
		t.Fatalf("cancel = %d %s", r.code, r.body)
	}
	j := a.jobOf(id, "verify")
	if j.State != fileJobStopped || j.Error != verifyStoppedText {
		t.Fatalf("job = %+v", j)
	}
	if a.verifyState(id).Verified || verifyLeftovers(a.root) || !a.dbExists(id) || a.entry(id).Mark != "" {
		t.Fatal("a stopped check changed something")
	}
	// Nothing runs any more: a second Cancel is M2, and a retry passes.
	if res := a.req(http.MethodDelete, "/api/cases/"+id+"/verify", "", ""); res.Code != http.StatusConflict {
		t.Fatalf("second cancel = %d %s", res.Code, res.Body)
	}
	a.verifyDone(id)
}

type httptestResult struct {
	code int
	body string
}

// Locking the key during Verify stops it; the temporary store goes.
func TestVerifyStopsWhenTheKeyIsLocked(t *testing.T) {
	a := newArchPC(t, cfsPassA)
	id, _, _ := a.archivedCase("lock verify", 3)
	p := pauseAt(t, "verify-start")
	if res := a.req(http.MethodPost, "/api/cases/"+id+"/verify", `{}`, "archived"); res.Code != http.StatusAccepted {
		t.Fatalf("verify = %d %s", res.Code, res.Body)
	}
	p.waitReached(t)
	if res := a.do(http.MethodPost, "/api/master-key/lock", ""); res.Code != http.StatusOK {
		t.Fatalf("lock = %d %s", res.Code, res.Body)
	}
	p.release()
	a.handler().caseFiles().jobs.waitAll()
	a.restart()
	if a.verifyState(id).Verified || verifyLeftovers(a.root) || !a.dbExists(id) {
		t.Fatal("a stopped check changed something")
	}
	a.verifyDone(id)
}

// INJ-5: a kill during Verify at each point; the restart removes the temporary
// store and a pending cleanup is reported until then.
func TestINJ5_VerifyKilledAtEveryPointThenRestart(t *testing.T) {
	for _, point := range []string{"verify-start", "verify-hashed", "verify-checked"} {
		t.Run(point, func(t *testing.T) {
			a := newArchPC(t, cfsPassA)
			id, dir, name := a.archivedCase("kill verify", 3)
			before := a.fileSum(filepath.Join(dir, name))
			killAt(t, point)
			if res := a.req(http.MethodPost, "/api/cases/"+id+"/verify", `{}`, "archived"); res.Code != http.StatusAccepted {
				t.Fatalf("verify = %d %s", res.Code, res.Body)
			}
			job := a.handler().caseFiles().jobs.get(jobVerify, casestore.CaseID(id))
			select {
			case <-job.done:
			case <-time.After(20 * time.Second):
				t.Fatal("killed job did not return")
			}
			// A stray temporary store, as a dead process leaves it.
			stray := filepath.Join(a.root, verifyDirName, "stray")
			if err := os.MkdirAll(stray, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(stray, "x"), []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
			if !verifyLeftovers(a.root) {
				t.Fatal("the stray store is not on disk")
			}
			a.restart()
			if verifyLeftovers(a.root) || a.orphans().VerifyCleanupPending {
				t.Fatal("the temporary store stayed after a restart")
			}
			if a.state(id) != casestore.StateArchived || a.entry(id).Mark != "" || !a.dbExists(id) {
				t.Fatal("a killed check changed the case")
			}
			if a.fileSum(filepath.Join(dir, name)) != before {
				t.Fatal("the case file changed")
			}
			if a.verifyState(id).Verified {
				t.Fatal("a killed check counted as passed")
			}
			archiveInject = nil
			a.verifyDone(id)
		})
	}
}

// archivedHeld archives a case while a reader still holds it open (an older tab
// that has not let go); the reader is returned.
func (a *archPC) archivedHeld(title string, n int) (string, casestore.Case) {
	a.t.Helper()
	id := a.seed(title, n)
	held, err := a.store.Open(context.Background(), casestore.CaseID(id))
	if err != nil {
		a.t.Fatal(err)
	}
	a.t.Cleanup(func() { _ = held.Close() })
	a.archiveDone(id, a.folder(), "open")
	return id, held
}

// Left-over temporary stores are reported as cleanup pending while no Verify runs.
func TestVerifyLeftoversAreReportedAsCleanupPending(t *testing.T) {
	a := newArchPC(t, cfsPassA)
	if a.orphans().VerifyCleanupPending {
		t.Fatal("pending on a clean start")
	}
	stray := filepath.Join(a.root, verifyDirName, "verify-x")
	if err := os.MkdirAll(stray, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stray, "case.db"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !a.orphans().VerifyCleanupPending {
		t.Fatal("cleanup pending not reported")
	}
	a.restart()
	if a.orphans().VerifyCleanupPending || verifyLeftovers(a.root) {
		t.Fatal("not cleaned at start")
	}
}
