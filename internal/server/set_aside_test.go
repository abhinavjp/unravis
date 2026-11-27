package server

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/history"
	"api-audit-explorer/internal/keeper"
)

// RV-DAT-1 (W-13): the way out of an unreadable saved connections file, and the guard that keeps it unreachable otherwise.

func damagedHistory(t *testing.T) (*history.Service, string, []byte) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "connections.dat")
	service, err := history.New(path, serverTestProtector{})
	if err != nil {
		t.Fatal(err)
	}
	damaged := []byte("a file that is not a saved connections file")
	if err := os.WriteFile(path, damaged, 0o600); err != nil {
		t.Fatal(err)
	}
	return service, path, damaged
}

func TestSetAsideRouteOffersTheWayOutOnlyForAnUnreadableFile(t *testing.T) {
	service, path, damaged := damagedHistory(t)
	h := New(&fakeRepository{}, nil, testToken, false, WithHistory(service))

	listed := request(h, http.MethodGet, "/api/connections/recent", "", true)
	if listed.Code != http.StatusServiceUnavailable || !strings.Contains(listed.Body.String(), `"code":"`+savedFileDamagedCode+`"`) {
		t.Fatalf("an unreadable file must answer its own code so the page can offer the way out: %d %s", listed.Code, listed.Body)
	}
	if request(h, http.MethodPost, "/api/connections/recent/set-aside", "", false).Code != http.StatusUnauthorized {
		t.Fatal("set-aside answered without the session token")
	}
	res := request(h, http.MethodPost, "/api/connections/recent/set-aside", "", true)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "connections.dat.unreadable-") {
		t.Fatalf("set-aside = %d %s", res.Code, res.Body)
	}
	matches, _ := filepath.Glob(path + ".unreadable-*")
	if len(matches) != 1 {
		t.Fatalf("copies kept: %v", matches)
	}
	if got, err := os.ReadFile(matches[0]); err != nil || !bytes.Equal(got, damaged) {
		t.Fatalf("the damaged file was not kept byte for byte: %v", err)
	}
	if again := request(h, http.MethodGet, "/api/connections/recent", "", true); again.Code != http.StatusOK || strings.TrimSpace(again.Body.String()) != "[]" {
		t.Fatalf("after set-aside the list must be empty: %d %s", again.Code, again.Body)
	}
}

// The guard: the route does nothing when the file reads fine, and a healthy file is never renamed.
func TestSetAsideRouteIsRefusedForAHealthyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "connections.dat")
	service, err := history.New(path, serverTestProtector{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Remember("prod", audit.ConnectionInput{URI: "mongodb://db.example", TLSMode: "system"}, "audit", false); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	h := New(&fakeRepository{}, nil, testToken, false, WithHistory(service))
	res := request(h, http.MethodPost, "/api/connections/recent/set-aside", "", true)
	if res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), "saved-file-not-damaged") {
		t.Fatalf("healthy file: %d %s", res.Code, res.Body)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("a refused set-aside touched the healthy file: %v", err)
	}
	if left, _ := filepath.Glob(path + ".unreadable-*"); len(left) != 0 {
		t.Fatalf("copies made for a healthy file: %v", left)
	}
}

// A saved list that fails for any other reason than "damaged" is not offered the way out, and a store that cannot set aside refuses.
func TestSetAsideRouteIgnoresOtherFailuresAndStoresWithoutIt(t *testing.T) {
	h := New(&fakeRepository{}, nil, testToken, false, WithHistory(&fakeHistory{}))
	if res := request(h, http.MethodPost, "/api/connections/recent/set-aside", "", true); res.Code != http.StatusServiceUnavailable {
		t.Fatalf("a store that cannot set aside = %d %s", res.Code, res.Body)
	}
	if res := request(New(&fakeRepository{}, nil, testToken, false), http.MethodPost, "/api/connections/recent/set-aside", "", true); res.Code != http.StatusServiceUnavailable {
		t.Fatalf("no saved list = %d %s", res.Code, res.Body)
	}
}

func TestSetAsideRouteRefusesAFourthCopyWithPlainWords(t *testing.T) {
	service, path, damaged := damagedHistory(t)
	for i := 0; i < history.MaxSetAside; i++ {
		if err := os.WriteFile(path+".unreadable-2026112"+string(rune('1'+i))+"-100000", []byte("old"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	h := New(&fakeRepository{}, nil, testToken, false, WithHistory(service))
	res := request(h, http.MethodPost, "/api/connections/recent/set-aside", "", true)
	if res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), `"code":"set-aside-full"`) || !strings.Contains(res.Body.String(), "Three unreadable files are already set aside") {
		t.Fatalf("fourth = %d %s", res.Code, res.Body)
	}
	// The words name the real folder, and a person's own file is theirs to open (no administrator needed).
	folder := strings.ReplaceAll(filepath.Dir(path), `\`, `\\`)
	if !strings.Contains(res.Body.String(), folder) || strings.Contains(res.Body.String(), "administrator") {
		t.Fatalf("the full message must name %s and not ask for an administrator: %s", folder, res.Body)
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, damaged) {
		t.Fatalf("the damaged file moved: %v", err)
	}
}

// failingAside is a saved list whose read fails for a reason other than "unreadable". It records whether set-aside was ever reached.
type failingAside struct {
	fakeHistory
	listErr     error
	asideCalled bool
}

func (f *failingAside) List() ([]history.Recent, error) { return nil, f.listErr }
func (f *failingAside) SetAside() (string, error)       { f.asideCalled = true; return "x", nil }

// A read that fails for any reason other than "unreadable" says nothing about the file: it is never answered "not damaged" (409) and never
// set aside. The key keeper being stopped or not trusted keeps its own words and code.
func TestSetAsideRouteDoesNotCallAnOtherFailureHealthy(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		wantCode string
	}{
		{"busy or locked file", history.ErrUnavailable, ""},
		{"lock timeout", context.DeadlineExceeded, ""},
		{"keeper stopped", keeper.ErrNotRunning, "keeper-stopped"},
		{"keeper impostor", keeper.ErrImpostor, "keeper-impostor"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			store := &failingAside{listErr: c.err}
			h := New(&fakeRepository{}, nil, testToken, false, WithHistory(store))
			res := request(h, http.MethodPost, "/api/connections/recent/set-aside", "", true)
			if res.Code != http.StatusServiceUnavailable {
				t.Fatalf("%s = %d %s, want 503", c.name, res.Code, res.Body)
			}
			if strings.Contains(res.Body.String(), "saved-file-not-damaged") || strings.Contains(res.Body.String(), "can be read") {
				t.Fatalf("%s was called a healthy file: %s", c.name, res.Body)
			}
			if c.wantCode != "" && !strings.Contains(res.Body.String(), `"code":"`+c.wantCode+`"`) {
				t.Fatalf("%s lost its code %s: %s", c.name, c.wantCode, res.Body)
			}
			if c.wantCode == "" && !strings.Contains(res.Body.String(), "left as it was") {
				t.Fatalf("%s: the plain failure must say the file was left as it was: %s", c.name, res.Body)
			}
			if store.asideCalled {
				t.Fatalf("%s reached SetAside", c.name)
			}
		})
	}
}

// A store whose set-aside itself fails (a file held open) answers 503 "left as it was", and the unreadable file is untouched.
func TestSetAsideRouteReportsAFailedMoveAsLeftAsItWas(t *testing.T) {
	service, path, damaged := damagedHistory(t)
	h := New(&fakeRepository{}, nil, testToken, false, WithHistory(&blockedAside{Service: service}))
	res := request(h, http.MethodPost, "/api/connections/recent/set-aside", "", true)
	if res.Code != http.StatusServiceUnavailable || !strings.Contains(res.Body.String(), "left as it was") {
		t.Fatalf("a failed move = %d %s", res.Code, res.Body)
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, damaged) {
		t.Fatalf("the unreadable file changed: %v", err)
	}
}

// The shared list's words name the keeper's folder, say an administrator opens it, and say the new list is for everyone on this PC.
func TestSetAsideWordsForTheSharedList(t *testing.T) {
	full := setAsideFullText(testKeeperDir, true)
	if !strings.Contains(full, testKeeperDir) || !strings.Contains(full, "administrator") {
		t.Fatalf("shared full text = %q", full)
	}
	damaged := savedFileDamagedText(testKeeperDir, true)
	for _, want := range []string{"on this account or by this version of Unravis", testKeeperDir, "everyone who uses Unravis on this PC", "not deleted"} {
		if !strings.Contains(damaged, want) {
			t.Fatalf("shared damaged text lacks %q: %q", want, damaged)
		}
	}
	ownDir := filepath.Join(os.TempDir(), "unravis-own")
	if own := savedFileDamagedText(ownDir, false); strings.Contains(own, "everyone") || strings.Contains(setAsideFullText(ownDir, false), "administrator") {
		t.Fatalf("a personal file talks about everyone or an administrator: %q", own)
	}
}

// sharedDamaged is an unreadable saved list that is the whole PC's (the key keeper's), with a known folder.
type sharedDamaged struct{ failingAside }

var testKeeperDir = filepath.Join(os.TempDir(), "unravis-keeper")

func (sharedDamaged) Folder() (string, bool) { return testKeeperDir, true }

// The page is told when the unreadable list is shared (reason "shared"), so its question says everyone is affected; a personal file says nothing.
func TestListRecentTellsThePageWhenTheDamagedListIsShared(t *testing.T) {
	shared := New(&fakeRepository{}, nil, testToken, false, WithHistory(&sharedDamaged{failingAside{listErr: history.ErrCorrupt}}))
	res := request(shared, http.MethodGet, "/api/connections/recent", "", true)
	if res.Code != http.StatusServiceUnavailable || !strings.Contains(res.Body.String(), `"reason":"shared"`) || !strings.Contains(res.Body.String(), "everyone who uses Unravis on this PC") {
		t.Fatalf("shared damaged list = %d %s", res.Code, res.Body)
	}
	service, _, _ := damagedHistory(t)
	own := request(New(&fakeRepository{}, nil, testToken, false, WithHistory(service)), http.MethodGet, "/api/connections/recent", "", true)
	if own.Code != http.StatusServiceUnavailable || strings.Contains(own.Body.String(), `"reason"`) {
		t.Fatalf("personal damaged list = %d %s", own.Code, own.Body)
	}
}

type blockedAside struct{ *history.Service }

func (b *blockedAside) SetAside() (string, error) { return "", history.ErrUnavailable }

// The shared layout: the saved list is the key keeper's. The unreadable file is the keeper's, and the same route sets it aside there.
func TestSetAsideThroughTheKeeper(t *testing.T) {
	e := newKeeperEnv(t, casestore.NewMemory(), keeper.Legacy{})
	path := filepath.Join(e.dir, "connections.dat")
	if res := e.json(http.MethodPost, "/api/connections/recent/set-aside", ""); res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), "saved-file-not-damaged") {
		t.Fatalf("healthy keeper file = %d %s", res.Code, res.Body)
	}
	damaged := []byte("damaged keeper file")
	if err := os.WriteFile(path, damaged, 0o600); err != nil {
		t.Fatal(err)
	}
	if res := e.get("/api/connections/recent"); res.Code != http.StatusServiceUnavailable || !strings.Contains(res.Body.String(), savedFileDamagedCode) {
		t.Fatalf("damaged keeper file listed as %d %s", res.Code, res.Body)
	}
	res := e.json(http.MethodPost, "/api/connections/recent/set-aside", "")
	if res.Code != http.StatusOK {
		t.Fatalf("set-aside through the keeper = %d %s", res.Code, res.Body)
	}
	matches, _ := filepath.Glob(path + ".unreadable-*")
	if len(matches) != 1 {
		t.Fatalf("copies kept: %v", matches)
	}
	if got, err := os.ReadFile(matches[0]); err != nil || !bytes.Equal(got, damaged) {
		t.Fatalf("the keeper's damaged file was not kept byte for byte: %v", err)
	}
	if res := e.get("/api/connections/recent"); res.Code != http.StatusOK || strings.TrimSpace(res.Body.String()) != "[]" {
		t.Fatalf("keeper list after set-aside: %d %s", res.Code, res.Body)
	}
}
