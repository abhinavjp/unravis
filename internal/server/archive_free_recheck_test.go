package server

import (
	"net/http"
	"os"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
)

// F1 and F2 of the P3 review: Free reads the record and the file again under the
// case lock, right before the commit point, and never trusts the one-hour check
// mark alone. Every test here ends with the working copy still there.

// stillKept asserts the working copy and the kept record are untouched and the
// check mark is gone (the check has to be made again).
func (a *archPC) stillKept(id string) {
	a.t.Helper()
	if !a.dbExists(id) {
		a.t.Fatal("the working copy was erased")
	}
	info := a.entry(id)
	if info.Mark != "" || info.Archive == nil || info.Archive.Freed() {
		a.t.Fatalf("the record changed: mark %q archive %+v", info.Mark, info.Archive)
	}
	if a.handler().caseFiles().verified.ok(casestore.CaseID(id), info.Archive.Current().SHA256, archiveNow()) {
		a.t.Fatal("the check mark stayed after a refusal")
	}
}

// F2: the file is deleted, cut short or replaced after the check, within the hour.
func TestF2_FreeLooksAtTheFileAfterTheCheck(t *testing.T) {
	tamper := map[string]func(t *testing.T, path string){
		"deleted": func(t *testing.T, path string) {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
		},
		"cut short": func(t *testing.T, path string) {
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, b[:len(b)/2], 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"same size, one byte changed": func(t *testing.T, path string) {
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			b[len(b)-1] ^= 0xff
			if err := os.WriteFile(path, b, 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"moved away": func(t *testing.T, path string) {
			if err := os.Rename(path, path+".moved"); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, do := range tamper {
		t.Run(name, func(t *testing.T) {
			a := newArchPC(t, cfsPassA)
			id, _, _ := a.archivedCase("tampered "+name, 4)
			a.verifyDone(id)
			do(t, a.casePath(id))

			archReason(t, a.freeReq(id), http.StatusConflict, "check-first", checkFirstText)
			a.stillKept(id)
			// Nothing is half done: a second try gives the same answer.
			archReason(t, a.freeReq(id), http.StatusConflict, "check-first", checkFirstText)
			a.stillKept(id)
		})
	}
}

// F2, the other side: an untouched file still frees.
func TestF2_UntouchedFileStillFrees(t *testing.T) {
	a := newArchPC(t, cfsPassA)
	id, _, _ := a.archivedCase("untouched", 4)
	a.verifyDone(id)
	if res := a.freeReq(id); res.Code != http.StatusOK {
		t.Fatalf("free = %d %s", res.Code, res.Body)
	}
	if !a.freed(id) {
		t.Fatal("not freed")
	}
}

// F2 during the wait: the file is damaged while Free waits for a reader.
func TestF2_FileDamagedDuringTheWait(t *testing.T) {
	a := newArchPC(t, cfsPassA)
	id, held := a.archivedHeld("damaged while waiting", 3)
	a.verifyDone(id)
	oldWait, oldPoll := freeWaitFor, freePoll
	freeWaitFor, freePoll = 10*time.Second, 10*time.Millisecond
	defer func() { freeWaitFor, freePoll = oldWait, oldPoll }()

	path := a.casePath(id)
	hit := false
	archiveInject = func(pt, _, _ string) bool {
		if pt != "free-wait" || hit {
			return false
		}
		hit = true
		b, err := os.ReadFile(path)
		if err != nil {
			t.Error(err)
			return false
		}
		b[len(b)/2] ^= 0xff
		if err := os.WriteFile(path, b, 0o600); err != nil {
			t.Error(err)
		}
		_ = held.Close() // the reader lets go: the next try would have gone through
		return false
	}
	defer func() { archiveInject = nil }()

	archReason(t, a.freeReq(id), http.StatusConflict, "check-first", checkFirstText)
	if !hit {
		t.Fatal("Free never waited")
	}
	a.stillKept(id)
}

// F1: Free waits for a reader. Meanwhile another tab restores the case in place and
// archives it again; the new file was never checked. When the reader lets go, Free
// must not go through on the strength of the old check.
func TestF1_RestoreAndArchiveAgainDuringTheWait(t *testing.T) {
	a := newArchPC(t, cfsPassA)
	id, held := a.archivedHeld("rearchived while waiting", 3)
	a.verifyDone(id)
	oldSHA := a.entry(id).Archive.Current().SHA256
	oldWait, oldPoll := freeWaitFor, freePoll
	freeWaitFor, freePoll = 20*time.Second, 10*time.Millisecond
	defer func() { freeWaitFor, freePoll = oldWait, oldPoll }()

	hit := false
	var freeing bool
	archiveInject = func(pt, _, _ string) bool {
		if pt != "free-wait" || hit {
			return false
		}
		hit = true
		freeing = true
		_ = held.Close()
		// The other tab: restore in place, then archive again.
		archiveInject = nil // the archive job below must not meet this hook
		if res := a.req(http.MethodPost, "/api/cases/"+id+"/restore-in-place", `{}`, "archived"); res.Code != http.StatusOK {
			t.Errorf("restore in place = %d %s", res.Code, res.Body)
			return false
		}
		if res := a.archiveReq(id, a.folder(), "closed"); res.Code != http.StatusAccepted {
			t.Errorf("archive again = %d %s", res.Code, res.Body)
			return false
		}
		if j := a.wait(id, "archive"); j.State != fileJobDone {
			t.Errorf("archive again job = %+v", j)
		}
		return false
	}
	defer func() { archiveInject = nil }()

	archReason(t, a.freeReq(id), http.StatusConflict, "check-first", checkFirstText)
	if !freeing {
		t.Fatal("Free never waited")
	}
	if got := a.entry(id).Archive.Current().SHA256; got == oldSHA {
		t.Fatal("the case was not archived again")
	}
	a.stillKept(id)
}
