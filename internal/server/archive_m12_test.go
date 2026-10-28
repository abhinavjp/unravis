package server

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"api-audit-explorer/internal/masterkey"
)

// F3 of the P3 review (spec 7.6, M12): the master key status counts the freed
// archives sealed under the master key where a reset is offered, so the screen can
// say how many cases a reset makes unreadable.
func (a *archPC) keyStatus() masterKeyStatus {
	a.t.Helper()
	res := a.req(http.MethodGet, "/api/master-key/status", "", "")
	if res.Code != http.StatusOK {
		a.t.Fatalf("status = %d %s", res.Code, res.Body)
	}
	var s masterKeyStatus
	decodeBody(a.t, res, &s)
	return s
}

func (a *archPC) damageKey() {
	a.t.Helper()
	if res := a.do(http.MethodPost, "/api/master-key/lock", ""); res.Code != http.StatusOK {
		a.t.Fatalf("lock = %d", res.Code)
	}
	if err := os.WriteFile(masterkey.Path(a.dir), []byte("garbage"), 0o600); err != nil {
		a.t.Fatal(err)
	}
}

func TestF3_M12CountsFreedArchivesBeforeAKeyReset(t *testing.T) {
	a := newArchPC(t, cfsPassA)
	var ids []string
	for _, title := range []string{"first", "second"} {
		id, _, _ := a.archivedCase(title, 3)
		ids = append(ids, id)
		a.verifyDone(id)
		if res := a.freeReq(id); res.Code != http.StatusOK {
			t.Fatalf("free = %d %s", res.Code, res.Body)
		}
	}
	a.archivedCase("kept only", 3) // its working copy stays: not counted
	if s := a.keyStatus(); s.State != "unlocked" || s.FreedArchives != 0 {
		t.Fatalf("status while unlocked = %+v (the count is for the reset offer only)", s)
	}

	a.damageKey()
	if s := a.keyStatus(); s.State != "damaged" || s.FreedArchives != 2 {
		t.Fatalf("status of a damaged key = %+v, want 2 freed archives", s)
	}
	// The note is only a number: no id, title or path beside the key file.
	raw, err := os.ReadFile(filepath.Join(a.dir, freedNoteName))
	if err != nil || strings.Contains(string(raw), ids[0]) || len(raw) > 64 {
		t.Fatalf("freed note = %q, %v", raw, err)
	}
}

// Erasing a freed archive takes it out of the count.
func TestF3_M12FollowsAnErase(t *testing.T) {
	a := newArchPC(t, cfsPassA)
	var ids []string
	for _, title := range []string{"first", "second"} {
		id, _, _ := a.archivedCase(title, 3)
		a.verifyDone(id)
		if res := a.freeReq(id); res.Code != http.StatusOK {
			t.Fatalf("free = %d %s", res.Code, res.Body)
		}
		ids = append(ids, id)
	}
	if res := a.req(http.MethodDelete, "/api/cases/"+ids[0], `{"confirm":`+q(ids[0])+`}`, ""); res.Code != http.StatusNoContent {
		t.Fatalf("erase = %d %s", res.Code, res.Body)
	}
	a.damageKey()
	if s := a.keyStatus(); s.FreedArchives != 1 {
		t.Fatalf("status = %+v, want 1", s)
	}
}

func TestF3_M12IsZeroWithOnlyKeptCopies(t *testing.T) {
	a := newArchPC(t, cfsPassA)
	a.archivedCase("kept one", 3)
	a.archivedCase("kept two", 3)
	a.damageKey()
	if s := a.keyStatus(); s.State != "damaged" || s.FreedArchives != 0 {
		t.Fatalf("status = %+v, want 0", s)
	}
}
