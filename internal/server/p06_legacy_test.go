package server

// TSK-21 L4: once a master key exists, a case folder sealed only by Windows
// (DPAPI) opens only if it was on this PC when the master key was set up (the
// legacy set). A same-user process can plant a complete DPAPI case folder; it
// must not open without the passphrase or list beside real cases.

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/casestore/sqlite"
	"api-audit-explorer/internal/masterkey"
)

// p06Plant adds a complete DPAPI case folder to the PC's cases folder, as another
// process of the same Windows user could: a store with the DPAPI protector only.
func p06Plant(t *testing.T, pc *cfsPC, title string) string {
	t.Helper()
	s, err := sqlite.New(sqlite.Options{Root: pc.root, Protector: pc.prot, MinFreeBytes: -1})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	info, err := s.Create(context.Background(), title)
	if err != nil {
		t.Fatal(err)
	}
	if k, err := s.CaseKind(info.ID); err != nil || k != casestore.KindDPAPI {
		t.Fatalf("planted kind = %q, %v", k, err)
	}
	return string(info.ID)
}

func p06CaseList(t *testing.T, pc *cfsPC) map[string]caseDTO {
	t.Helper()
	res := pc.do(http.MethodGet, "/api/cases", "")
	if res.Code != http.StatusOK {
		t.Fatalf("list = %d %s", res.Code, res.Body)
	}
	var body struct {
		Cases []caseDTO `json:"cases"`
	}
	decodeBody(t, res, &body)
	out := map[string]caseDTO{}
	for _, c := range body.Cases {
		out[c.ID] = c
	}
	return out
}

func p06Unlock(t *testing.T, pc *cfsPC) {
	t.Helper()
	if res := pc.do(http.MethodPost, "/api/master-key/unlock", unlockBody(cfsPassA)); res.Code != http.StatusOK {
		t.Fatalf("unlock = %d %s", res.Code, res.Body)
	}
}

func p06SetFile(t *testing.T, pc *cfsPC) []byte {
	t.Helper()
	b, err := os.ReadFile(masterkey.LegacySetPath(pc.dir))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Set up through the route: the real old case is recorded, a case planted after
// it is not, and the planted one is refused everywhere a person could reach it.
func TestP06_PlantedDPAPICaseAfterSetupIsRefused(t *testing.T) {
	pc := newCFSPC(t, "", false)
	real := pc.seed("real old case", 3)
	if res := pc.do(http.MethodPost, "/api/master-key/setup", setupBody(cfsPassA, cfsPassA, true)); res.Code != http.StatusOK {
		t.Fatalf("setup = %d %s", res.Code, res.Body)
	}
	planted := p06Plant(t, pc, "planted")
	plantedDir := filepath.Join(pc.root, planted)

	cases := p06CaseList(t, pc)
	if c, ok := cases[real]; !ok || c.Untrusted || c.Title != "real old case" {
		t.Fatalf("real case in list = %+v", c)
	}
	if c, ok := cases[planted]; !ok || !c.Untrusted || strings.Contains(c.Title, "planted") {
		t.Fatalf("planted case in list = %+v (want flagged, no real title)", c)
	}
	// Every route that reaches the case answers the typed refusal.
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/cases/" + planted, ""},
		{http.MethodPost, "/api/cases/" + planted + "/close", ""},
	} {
		res := pc.do(tc.method, tc.path, tc.body)
		cfsReason(t, res, http.StatusConflict, "untrusted-case")
	}
	if k := pc.kind(planted); k != casestore.KindDPAPI {
		t.Fatalf("planted kind after the refusals = %s", k)
	}
	st := p06Status(t, pc)
	if st.DPAPICases != 1 || st.UntrustedCases != 1 {
		t.Fatalf("status = %+v, want 1 older case to protect and 1 untrusted", st)
	}
	// "Protect older cases" protects the real one and refuses the planted one.
	res := pc.do(http.MethodPost, "/api/master-key/rewrap", `{"all":true}`)
	if res.Code != http.StatusOK {
		t.Fatalf("rewrap = %d %s", res.Code, res.Body)
	}
	if k := pc.kind(real); k != casestore.KindMasterKey {
		t.Fatalf("real case kind after the re-wrap = %s", k)
	}
	if k := pc.kind(planted); k != casestore.KindDPAPI {
		t.Fatalf("planted case was re-wrapped (laundered): %s", k)
	}
	if st := p06Status(t, pc); st.DPAPICases != 0 || st.UntrustedCases != 1 {
		t.Fatalf("status after the re-wrap = %+v", st)
	}
	if res := pc.do(http.MethodGet, "/api/cases/"+real, ""); res.Code != http.StatusOK {
		t.Fatalf("GET real after the re-wrap = %d %s", res.Code, res.Body)
	}
	// Nothing was deleted, and the person can still erase it.
	if _, err := os.Stat(filepath.Join(plantedDir, "case.key")); err != nil {
		t.Fatalf("planted folder was touched: %v", err)
	}
	if res := pc.do(http.MethodDelete, "/api/cases/"+planted, `{"confirm":`+q(planted)+`}`); res.Code >= 300 {
		t.Fatalf("erase of the untrusted case = %d %s", res.Code, res.Body)
	}
}

// The refusal survives a restart and a lock: the set is read back from its file,
// and a re-wrap removes the case from it.
func TestP06_LegacySetSurvivesRestartAndRewrapShrinksIt(t *testing.T) {
	pc, ids := p06Legacy(t, "old one", "old two")
	planted := p06Plant(t, pc, "planted")
	// p06Legacy unlocked an existing master key that had no list: that unlock
	// recorded the two old cases (the plant came after it).
	before := p06SetFile(t, pc)
	np := p06Restart(pc)
	p06Unlock(t, np)
	cases := p06CaseList(t, np)
	if cases[ids[0]].Untrusted || cases[ids[1]].Untrusted || !cases[planted].Untrusted {
		t.Fatalf("after a restart: %+v", cases)
	}
	if !bytes.Equal(before, p06SetFile(t, np)) {
		t.Fatal("an unlock that read a good set rewrote it")
	}
	// Re-wrapping one old case drops it from the file; the other stays usable.
	if res := np.do(http.MethodPost, "/api/master-key/rewrap", `{"caseId":`+q(ids[0])+`}`); res.Code != http.StatusOK {
		t.Fatalf("rewrap one = %d %s", res.Code, res.Body)
	}
	if k := np.kind(ids[0]); k != casestore.KindMasterKey {
		t.Fatalf("kind = %s", k)
	}
	ring, err := np.sess.Keyring()
	if err != nil {
		t.Fatal(err)
	}
	got, st, err := masterkey.ReadLegacySet(np.dir, ring)
	if err != nil || st != masterkey.LegacyOK || len(got) != 1 || got[0] != ids[1] {
		t.Fatalf("set after the re-wrap = %v, %v, %v; want only %s", got, st, err, ids[1])
	}
	if bytes.Equal(before, p06SetFile(t, np)) {
		t.Fatal("legacy set file unchanged after a re-wrap")
	}
	// The remaining old case still opens (it is on the list); the plant does not.
	if res := np.do(http.MethodGet, "/api/cases/"+ids[1], ""); res.Code != http.StatusOK {
		t.Fatalf("GET listed old case = %d %s", res.Code, res.Body)
	}
	cfsReason(t, np.do(http.MethodGet, "/api/cases/"+planted, ""), http.StatusConflict, "untrusted-case")
}

// The file holds no case id, no title and no JSON keys in plain.
func TestP06_LegacySetFileHoldsNoCaseIDInPlain(t *testing.T) {
	pc, ids := p06Legacy(t, "old one", "old two")
	raw := p06SetFile(t, pc)
	for _, id := range ids {
		if bytes.Contains(raw, []byte(id)) {
			t.Fatalf("legacyset.dat holds case id %s in plain", id)
		}
	}
	if bytes.Contains(raw, []byte("old one")) || bytes.Contains(raw, []byte(`"ids"`)) {
		t.Fatal("legacyset.dat holds readable text")
	}
	// While locked the cases routes answer 423 and nothing is judged.
	np := p06Restart(pc)
	res := np.do(http.MethodGet, "/api/cases/"+ids[0], "")
	if res.Code != http.StatusLocked {
		t.Fatalf("GET while locked = %d %s", res.Code, res.Body)
	}
}

// A damaged set fails closed: every Windows sealed case is refused, nothing is
// deleted, the file is left as it was, and master-key cases are unaffected.
func TestP06_DamagedLegacySetFailsClosedAndTouchesNothing(t *testing.T) {
	pc, ids := p06Legacy(t, "old one")
	mk := pc.seed("made after setup", 2) // master-key sealed
	if k := pc.kind(mk); k != casestore.KindMasterKey {
		t.Fatalf("case after set-up kind = %s", k)
	}
	if err := os.WriteFile(masterkey.LegacySetPath(pc.dir), []byte("scribbled over"), 0o600); err != nil {
		t.Fatal(err)
	}
	keyBefore, err := os.ReadFile(filepath.Join(pc.root, ids[0], "case.key"))
	if err != nil {
		t.Fatal(err)
	}
	np := p06Restart(pc)
	p06Unlock(t, np)
	cases := p06CaseList(t, np)
	if !cases[ids[0]].Untrusted || cases[mk].Untrusted {
		t.Fatalf("list with a damaged set: %+v", cases)
	}
	cfsReason(t, np.do(http.MethodGet, "/api/cases/"+ids[0], ""), http.StatusConflict, "untrusted-case")
	if res := np.do(http.MethodGet, "/api/cases/"+mk, ""); res.Code != http.StatusOK {
		t.Fatalf("master-key case with a damaged set = %d %s", res.Code, res.Body)
	}
	if st := p06Status(t, np); st.DPAPICases != 0 || st.UntrustedCases != 1 {
		t.Fatalf("status = %+v", st)
	}
	if got, _ := os.ReadFile(filepath.Join(pc.root, ids[0], "case.key")); !bytes.Equal(got, keyBefore) {
		t.Fatal("case.key of the refused case changed")
	}
	if got, _ := os.ReadFile(masterkey.LegacySetPath(np.dir)); string(got) != "scribbled over" {
		t.Fatal("a damaged legacy set was overwritten")
	}
	// Rewrap-all refuses it too and changes nothing.
	res := np.do(http.MethodPost, "/api/master-key/rewrap", `{"all":true}`)
	if !strings.Contains(res.Body.String(), "untrusted-case") {
		t.Fatalf("rewrap-all body = %d %s, want reason untrusted-case", res.Code, res.Body)
	}
	if k := np.kind(ids[0]); k != casestore.KindDPAPI {
		t.Fatalf("rewrap-all (%d %s) changed a refused case to %s", res.Code, res.Body, k)
	}
}

// A session that never loaded a set refuses every Windows sealed case, and a
// session with no master key judges nothing.
func TestP06_SessionWithoutASetRefusesAndWithoutAKeyAllows(t *testing.T) {
	e := newMKEnv(t)
	if err := e.sess.LegacyCheck("anything"); err != nil {
		t.Fatalf("no master key: LegacyCheck = %v", err)
	}
	e.presetKey(mkPass)
	if err := e.sess.LegacyCheck("anything"); !errors.Is(err, casestore.ErrLocked) {
		t.Fatalf("locked: LegacyCheck = %v, want ErrLocked", err)
	}
	// Unlock without going through the routes: no set was loaded.
	ring, err := masterkey.Unlock(e.dir, []byte(mkPass))
	if err != nil {
		t.Fatal(err)
	}
	e.sess.install(ring)
	if err := e.sess.LegacyCheck("anything"); !errors.Is(err, casestore.ErrUntrustedLegacy) {
		t.Fatalf("unlocked, no set: LegacyCheck = %v, want ErrUntrustedLegacy", err)
	}
}

// With no master key set up the legacy behaviour is unchanged: DPAPI cases open
// and none is flagged.
func TestP06_NoMasterKeyLegacyBehaviourUnchanged(t *testing.T) {
	pc := newCFSPC(t, "", false)
	id := pc.seed("plain old case", 2)
	planted := p06Plant(t, pc, "second")
	cases := p06CaseList(t, pc)
	if cases[id].Untrusted || cases[planted].Untrusted || len(cases) != 2 {
		t.Fatalf("list without a master key: %+v", cases)
	}
	for _, c := range []string{id, planted} {
		if res := pc.do(http.MethodGet, "/api/cases/"+c, ""); res.Code != http.StatusOK {
			t.Fatalf("GET %s without a master key = %d %s", c, res.Code, res.Body)
		}
	}
	if _, err := os.Stat(masterkey.LegacySetPath(pc.dir)); !os.IsNotExist(err) {
		t.Fatalf("a legacy set exists without a master key: %v", err)
	}
}
