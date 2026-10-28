package server

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"api-audit-explorer/internal/casestore"
)

// Clumsy-user review of P3 (Change 10). Each test is something a careless or
// confused person can do with archive, check, free and restore. The cases that the
// scenario files already prove are named here and not repeated:
//
//	UNC, "..", 241-character and data-folder paths   TestA1_FolderRulesGiveM8AndChangeNothing
//	full disk                                        TestA2_DiskFullRemovesThePartAndGivesM9
//	Cancel at the last moment (the 99% case)         TestINJ9_CancelAndCommitExactlyOneWins
//	kill during the rename and at every other point  TestA13_KillAtEveryPointThenRestartLeavesTheOriginalState
//
// The rest are below.

// Copy the file elsewhere and restore it twice, by the old Restore from file
// route and by Restore as new: every restore is a new case, the file is never
// changed or removed, and the original case lists each restore.
func TestP03Clumsy_CopyTheFileElsewhereAndRestoreTwice(t *testing.T) {
	a := newArchPC(t, cfsPassA)
	id, _, _ := a.archivedCase("copied", 5)
	src := a.casePath(id)
	before := a.fileSum(src)
	copyDir := a.folder()
	copied := filepath.Join(copyDir, "somewhere-else.unravis-case")
	raw, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(copied, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	seen := map[string]bool{id: true}
	for i := 0; i < 2; i++ {
		res := a.restore(copied, cfsPassA)
		if res.Code != http.StatusCreated {
			t.Fatalf("restore %d = %d %s", i+1, res.Code, res.Body)
		}
		var c caseDTO
		decodeBody(t, res, &c)
		if seen[c.ID] || c.State != string(casestore.StateClosed) {
			t.Fatalf("restore %d gave %+v", i+1, c)
		}
		seen[c.ID] = true
		if n := a.eventCount(c.ID); n != 5 {
			t.Fatalf("restored case has %d events", n)
		}
	}
	for i := 0; i < 2; i++ {
		res := a.req(http.MethodPost, "/api/cases/"+id+"/restore-as-new", `{}`, "archived")
		if res.Code != http.StatusCreated {
			t.Fatalf("restore as new %d = %d %s", i+1, res.Code, res.Body)
		}
		var c caseDTO
		decodeBody(t, res, &c)
		if seen[c.ID] {
			t.Fatalf("restore as new %d reused a case id", i+1)
		}
		seen[c.ID] = true
	}
	if a.fileSum(src) != before || a.fileSum(copied) != before {
		t.Fatal("a case file changed")
	}
	if a.state(id) != casestore.StateArchived || !a.dbExists(id) {
		t.Fatal("the archived original changed")
	}
	if len(seen) != 5 {
		t.Fatalf("cases = %d, want the original and four restores", len(seen))
	}
}

// Free, then press it again, type the code wrongly, restore in place, restore as
// new with the file gone: each is answered in words and nothing else changes.
func TestP03Clumsy_FreeThenTheWrongButtons(t *testing.T) {
	a := newArchPC(t, cfsPassA)
	id, dir, name := a.archivedCase("free twice", 3)
	a.verifyDone(id)

	// A wrong or sloppy confirmation frees nothing.
	for _, bad := range []string{"", " " + shortCode(casestore.CaseID(id)), strings.ToUpper(shortCode(casestore.CaseID(id))), "free", string(id) + "x"} {
		res := a.req(http.MethodPost, "/api/cases/"+id+"/free", `{"confirm":`+q(bad)+`}`, "archived")
		if bad == string(id) {
			continue
		}
		if res.Code != http.StatusBadRequest || failureOf(t, res).Reason != "confirm-mismatch" {
			t.Fatalf("free with %q = %d %s", bad, res.Code, res.Body)
		}
	}
	if !a.dbExists(id) {
		t.Fatal("a wrong confirmation removed the working copy")
	}
	// Free with the wrong belief (the page thinks the case is open).
	res := a.req(http.MethodPost, "/api/cases/"+id+"/free", `{"confirm":`+q(shortCode(casestore.CaseID(id)))+`}`, "open")
	if res.Code != http.StatusConflict || !a.dbExists(id) {
		t.Fatalf("free with a stale belief = %d %s", res.Code, res.Body)
	}

	if res := a.freeReq(id); res.Code != http.StatusOK {
		t.Fatalf("free = %d %s", res.Code, res.Body)
	}
	// The second press (a double click): nothing to free, said in words.
	res = a.freeReq(id)
	if res.Code != http.StatusConflict {
		t.Fatalf("second free = %d %s", res.Code, res.Body)
	}
	if !a.freed(id) {
		t.Fatal("not freed")
	}
	// Check again after Free: the working copy is gone, so there is nothing to check.
	archReason(t, a.req(http.MethodPost, "/api/cases/"+id+"/verify", `{}`, "archived"), http.StatusConflict, "not-kept", archiveNotKeptText)
	// Restore in place after Free: not possible.
	archReason(t, a.req(http.MethodPost, "/api/cases/"+id+"/restore-in-place", `{}`, "archived"), http.StatusConflict, "case-changed", caseChangedText)
	// Archive of a freed case: it is already archived.
	if res := a.archiveReq(id, a.folder(), "archived"); res.Code != http.StatusConflict {
		t.Fatalf("archive of a freed case = %d %s", res.Code, res.Body)
	}

	// The person deletes the case file by hand, then asks to restore it.
	if err := os.Remove(filepath.Join(dir, name)); err != nil {
		t.Fatal(err)
	}
	archReason(t, a.req(http.MethodPost, "/api/cases/"+id+"/restore-as-new", `{}`, "archived"), http.StatusConflict, "file-missing", restoreNeedsFileText)
	// The case stays listed, still archived and freed, never half way.
	arch := a.archivedList()
	if c, ok := arch.find(id); !ok || c.State != "archived" || c.Archive == nil || !c.Archive.Freed {
		t.Fatalf("archived list = %+v", arch)
	}
}

func (a *archPC) archivedList() casesPage {
	a.t.Helper()
	res := a.req(http.MethodGet, "/api/cases?archived=1", "", "")
	if res.Code != http.StatusOK {
		a.t.Fatalf("archived list = %d %s", res.Code, res.Body)
	}
	var p casesPage
	decodeBody(a.t, res, &p)
	return p
}

// The working copy of an archived (kept) case is deleted by hand: the case is
// listed as Missing, only Erase and Restore as new remain, and the file is safe.
func TestP03Clumsy_WorkingCopyDeletedByHandIsMissing(t *testing.T) {
	a := newArchPC(t, cfsPassA)
	id, _, _ := a.archivedCase("deleted by hand", 3)
	if err := os.RemoveAll(filepath.Join(a.root, id)); err != nil {
		t.Fatal(err)
	}
	var found caseDTO
	var ok bool
	for _, p := range []casesPage{a.listAll(), a.archivedList()} {
		if c, hit := p.find(id); hit {
			found, ok = c, true
		}
	}
	if !ok || !found.Missing {
		t.Fatalf("missing case not listed as Missing: %+v", found)
	}
	archReason(t, a.req(http.MethodPost, "/api/cases/"+id+"/verify", `{}`, "archived"), http.StatusConflict, "not-kept", archiveNotKeptText)
	archReason(t, a.freeReq(id), http.StatusConflict, "not-kept", archiveNotKeptText)
	// The file is still there: it restores as new.
	if res := a.req(http.MethodPost, "/api/cases/"+id+"/restore-as-new", `{}`, "archived"); res.Code != http.StatusCreated {
		t.Fatalf("restore as new of a missing case = %d %s", res.Code, res.Body)
	}
}

// Archive a case that is already archived, or archive while it is Archiving, or
// with a page that is out of date: M2 or M3, and no second file.
func TestP03Clumsy_ArchiveTwiceAndWithAStalePage(t *testing.T) {
	a := newArchPC(t, cfsPassA)
	id, dir, _ := a.archivedCase("again", 2)
	if res := a.archiveReq(id, dir, "archived"); res.Code != http.StatusConflict {
		t.Fatalf("second archive = %d %s", res.Code, res.Body)
	}
	if got := caseFilesIn(t, dir); len(got) != 1 {
		t.Fatalf("files = %v", got)
	}
	// Restore, then archive with a page that still thinks it is archived.
	if res := a.req(http.MethodPost, "/api/cases/"+id+"/restore-in-place", `{}`, "archived"); res.Code != http.StatusOK {
		t.Fatalf("restore in place = %d %s", res.Code, res.Body)
	}
	if res := a.archiveReq(id, dir, "archived"); res.Code != http.StatusConflict {
		t.Fatalf("archive with a stale page = %d %s", res.Code, res.Body)
	}
	// A restored case archives again, as a new file; both files are listed.
	a.archiveDone(id, dir, "closed")
	if got := caseFilesIn(t, dir); len(got) != 2 {
		t.Fatalf("files = %v", got)
	}
	if n := a.entry(id); len(n.Archive.Files) != 2 {
		t.Fatalf("files made = %d", len(n.Archive.Files))
	}
}

// A case file that is not a case file, or empty, or cut short, never restores and
// is never removed.
func TestP03Clumsy_DamagedFileRestoresNothing(t *testing.T) {
	a := newArchPC(t, cfsPassA)
	dir := a.folder()
	for name, body := range map[string][]byte{
		"empty.unravis-case":     {},
		"text.unravis-case":      []byte("not a case file at all"),
		"cut-short.unravis-case": nil,
	} {
		p := filepath.Join(dir, name)
		content := body
		if name == "cut-short.unravis-case" {
			id, _, _ := a.archivedCase("source", 3)
			raw, err := os.ReadFile(a.casePath(id))
			if err != nil {
				t.Fatal(err)
			}
			content = raw[:len(raw)/2]
		}
		if err := os.WriteFile(p, content, 0o600); err != nil {
			t.Fatal(err)
		}
		before := len(a.listAll().Cases)
		res := a.restore(p, cfsPassA)
		if res.Code < 400 {
			t.Fatalf("restore of %s = %d %s", name, res.Code, res.Body)
		}
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("%s was removed", name)
		}
		if after := len(a.listAll().Cases); after != before {
			t.Fatalf("%s left a case behind (%d -> %d)", name, before, after)
		}
	}
}
