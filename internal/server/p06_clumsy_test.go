package server

// Phase 06 hostile, hurried, clumsy-user review of the master key and case
// file flows (TSK-21 item 3). The defects it found are fixed and the tests that
// pinned them are now TestP06_* guards, like the ones that held from the start.
// Helpers use the p06 prefix; the cfsPC harness comes from case_file_test.go.

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/casestore/sqlite"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

// p06ImportInto adds n events to an open case through a second import.
func p06ImportInto(t *testing.T, c casestore.Case, id, file string, n int) {
	t.Helper()
	events := make([]evidence.Event, n)
	base := time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC)
	for i := range events {
		v := float64(i)
		line := int64(i + 2)
		events[i] = evidence.Event{
			EvidenceID: evidence.EventID(id, file, "first", line, 0),
			Source:     file, Kind: "cpu", Host: "web-a",
			Start: base.Add(time.Duration(i) * time.Second), Value: &v, Unit: "percent", Line: line, TZRule: "utc-field",
		}
		events[i].End = events[i].Start.Add(time.Second)
	}
	run := func(r io.Reader, emit func(evidence.Event) error) (importer.Report, error) {
		rep := importer.Report{Importer: file}
		if _, err := io.Copy(io.Discard, r); err != nil {
			return rep, err
		}
		for _, e := range events {
			rep.Rows++
			if err := emit(e); err != nil {
				return rep, err
			}
			rep.Emitted++
		}
		return rep, nil
	}
	meta := casestore.ImportMeta{Importer: file, FileName: file, HostLabel: "web-a", TZRule: "utc-field", AckGating: true, AckText: "Read the gating note for this import."}
	if _, err := c.Import(context.Background(), meta, strings.NewReader("body\n"), run); err != nil {
		t.Fatalf("second import: %v", err)
	}
}

// p06Legacy is a PC that has old DPAPI-sealed cases and then sets up a master key.
func p06Legacy(t *testing.T, titles ...string) (*cfsPC, []string) {
	t.Helper()
	pc := newCFSPC(t, "", false)
	var ids []string
	for _, title := range titles {
		ids = append(ids, pc.seed(title, 4))
	}
	pc.pass = cfsPassA
	pc.setKey(true)
	return pc, ids
}

// p06Rehandle gives the same PC a brand new handler: what a restart does to the
// in-memory throttles, without the cost of a second store.
func p06Rehandle(pc *cfsPC) {
	pc.h = New(&fakeRepository{}, testAssets(), testToken, false,
		WithCases(pc.store, mustHosts(pc.t, hostsDoc), importer.NewRegistry(toyImporter{name: "toy", score: 90})),
		WithMasterKey(pc.sess),
	)
}

// p06Restart is a full restart: new session over the same folder (locked, as
// at launch), new store over the same case folder, new handler.
func p06Restart(pc *cfsPC) *cfsPC {
	t := pc.t
	t.Helper()
	np := *pc
	np.sess = NewMasterKeySession(pc.dir)
	store, err := sqlite.New(sqlite.Options{
		Root: pc.root, Protector: pc.prot, MinFreeBytes: -1,
		Protectors: np.sess.Lookup, NewCaseKind: np.sess.NewCaseKind,
		LegacyCheck: np.sess.LegacyCheck, LegacyDone: np.sess.LegacyDone,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	np.store = store
	p06Rehandle(&np)
	return &np
}

// p06HalfRewrap runs the re-wrap of one case and "cuts the power" at step.
func p06HalfRewrap(t *testing.T, pc *cfsPC, id, step string) {
	t.Helper()
	lk, err := pc.sess.Lookup(casestore.KindMasterKey)
	if err != nil {
		t.Fatal(err)
	}
	stop := errors.New("power cut")
	err = casestore.Rewrap(filepath.Join(pc.root, id),
		casestore.Sealing{Kind: casestore.KindDPAPI, P: pc.prot},
		casestore.Sealing{Kind: casestore.KindMasterKey, P: lk},
		casestore.RewrapOptions{Step: func(s string) error {
			if s == step {
				return stop
			}
			return nil
		}})
	if !errors.Is(err, stop) {
		t.Fatalf("rewrap stopped at %q with %v", step, err)
	}
}

func p06Names(dir string) []string {
	ents, _ := os.ReadDir(dir)
	var out []string
	for _, e := range ents {
		out = append(out, e.Name())
	}
	return out
}

func p06Retry(t *testing.T, body string) int {
	t.Helper()
	var f masterKeyFailure
	if err := json.Unmarshal([]byte(body), &f); err != nil {
		t.Fatalf("body %q: %v", body, err)
	}
	return f.RetryAfterSeconds
}

/* ------------------------------------------------------------ re-wrap */

// A power cut between the two replaces of a re-wrap leaves manifest.dat sealed
// by the master key and case.key still sealed by Windows. Open used to refuse
// that as a key-file error and answer 500, a dead end in the UI. Now open
// finishes the re-wrap itself while the master key is unlocked.
func TestP06_HalfRewrappedCaseOpensBySelfHealing(t *testing.T) {
	pc, ids := p06Legacy(t, "legacy")
	id := ids[0]
	p06HalfRewrap(t, pc, id, "manifest-replaced")
	for _, suffix := range []string{"", "/imports", "/events"} {
		res := pc.do(http.MethodGet, "/api/cases/"+id+suffix, "")
		if res.Code >= 500 {
			t.Errorf("GET %q on a half re-wrapped case = %d %s; want it to open (after finishing the re-wrap) or say plainly what to do", suffix, res.Code, strings.TrimSpace(res.Body.String()))
		}
	}
}

// Whatever a crash leaves, one more re-wrap finishes the job and loses nothing.
func TestP06_RewrapRecoversFromACutAtEveryStep(t *testing.T) {
	for _, step := range []string{"staged", "manifest-replaced", "key-replaced"} {
		t.Run(step, func(t *testing.T) {
			pc, ids := p06Legacy(t, "legacy")
			id := ids[0]
			p06HalfRewrap(t, pc, id, step)
			res := pc.do(http.MethodPost, "/api/master-key/rewrap", `{"caseId":"`+id+`"}`)
			if res.Code != http.StatusOK {
				t.Fatalf("rewrap = %d %s", res.Code, res.Body)
			}
			if k := pc.kind(id); k != casestore.KindMasterKey {
				t.Fatalf("kind after recovery = %s", k)
			}
			if n := pc.eventCount(id); n != 4 {
				t.Fatalf("events after recovery = %d, want 4", n)
			}
			if res := pc.do(http.MethodGet, "/api/cases/"+id, ""); res.Code != http.StatusOK {
				t.Fatalf("GET after recovery = %d %s", res.Code, res.Body)
			}
			for _, n := range p06Names(filepath.Join(pc.root, id)) {
				if strings.Contains(n, "tmp") || strings.HasSuffix(n, ".new") || strings.HasSuffix(n, ".part") {
					t.Errorf("debris %q left in the case folder", n)
				}
			}
		})
	}
}

// A cut before either replace, or after both, must not hide the case.
func TestP06_RewrapCutBeforeOrAfterTheReplacesStillOpens(t *testing.T) {
	for _, step := range []string{"staged", "key-replaced"} {
		t.Run(step, func(t *testing.T) {
			pc, ids := p06Legacy(t, "legacy")
			p06HalfRewrap(t, pc, ids[0], step)
			if res := pc.do(http.MethodGet, "/api/cases/"+ids[0], ""); res.Code != http.StatusOK {
				t.Fatalf("GET = %d %s", res.Code, res.Body)
			}
			if n := pc.eventCount(ids[0]); n != 4 {
				t.Fatalf("events = %d", n)
			}
		})
	}
}

// "Protect older cases" while one of them is open in the app: the open handle
// keeps working, another case is untouched, a fresh start reads both.
func TestP06_RewrapWhileACaseIsOpen(t *testing.T) {
	pc, ids := p06Legacy(t, "open one", "idle one")
	open, err := pc.store.Open(context.Background(), casestore.CaseID(ids[0]))
	if err != nil {
		t.Fatal(err)
	}
	res := pc.do(http.MethodPost, "/api/master-key/rewrap", `{"all":true}`)
	if res.Code != http.StatusOK {
		_ = open.Close()
		t.Fatalf("rewrap = %d %s", res.Code, res.Body)
	}
	t.Logf("rewrap with an open case: %s", strings.TrimSpace(res.Body.String()))
	p06ImportInto(t, open, ids[0], "after.csv", 2)
	if err := open.Close(); err != nil {
		t.Fatalf("close the case that was open during the re-wrap: %v", err)
	}
	for _, id := range ids {
		if k := pc.kind(id); k != casestore.KindMasterKey {
			t.Errorf("case %s kind = %s", id, k)
		}
	}
	np := p06Restart(pc)
	if res := np.do(http.MethodPost, "/api/master-key/unlock", unlockBody(cfsPassA)); res.Code != http.StatusOK {
		t.Fatalf("unlock after restart = %d %s", res.Code, res.Body)
	}
	if n := np.eventCount(ids[0]); n != 6 {
		t.Errorf("events in the case that was open = %d, want 6", n)
	}
	if n := np.eventCount(ids[1]); n != 4 {
		t.Errorf("events in the idle case = %d, want 4", n)
	}
}

/* --------------------------------------------------------- throttles */

// Ten wrong keys at export: five free tries, then 429 with a wait that also
// refuses the right key; unlock and restore keep their own counters; a restart
// (new handler) starts the count again.
func TestP06_ExportThrottleAfterFiveWrongKeys(t *testing.T) {
	pc := newCFSPC(t, cfsPassA, true)
	id := pc.seed("throttle", 3)
	out := t0Dir(t)
	for i := 0; i < 5; i++ {
		cfsReason(t, pc.export(id, out, cfsPassB), http.StatusUnauthorized, "wrong-key")
	}
	for i := 0; i < 5; i++ {
		res := pc.export(id, out, cfsPassB)
		cfsReason(t, res, http.StatusTooManyRequests, "throttled")
		if p06Retry(t, res.Body.String()) < 1 || res.Header().Get("Retry-After") == "" {
			t.Fatalf("throttled answer has no wait: %s", res.Body)
		}
	}
	// The right key during the wait is refused too.
	cfsReason(t, pc.export(id, out, cfsPassA), http.StatusTooManyRequests, "throttled")
	// Unlock on an already unlocked session judges nothing, so it answers OK; a
	// restore has its own throttle (it judges a case file's passphrase, not the
	// master key) and is judged on its own merits, not 429.
	if res := pc.do(http.MethodPost, "/api/master-key/unlock", unlockBody(cfsPassA)); res.Code != http.StatusOK {
		t.Errorf("unlock while export is throttled = %d %s", res.Code, res.Body)
	}
	cfsReason(t, pc.restore(filepath.Join(out, "none"+casestore.CaseFileExt), cfsPassA), http.StatusUnprocessableEntity, "bad-case-file")
	if names := p06Names(out); len(names) != 0 {
		t.Fatalf("a refused export left %v", names)
	}
	// A restart forgets the failures. The throttle is the session's (shared with
	// unlock), so a restart means a new session, which starts locked.
	pc = p06Restart(pc)
	if res := pc.do(http.MethodPost, "/api/master-key/unlock", unlockBody(cfsPassA)); res.Code != http.StatusOK {
		t.Fatalf("unlock after a restart = %d %s", res.Code, res.Body)
	}
	if res := pc.export(id, filepath.Join(out, "ok"+casestore.CaseFileExt), cfsPassA); res.Code != http.StatusOK {
		t.Fatalf("export after a restart = %d %s", res.Code, res.Body)
	}
}

// Same for unlock: ten wrong keys, the right key is refused during the wait,
// and a restart resets the count (the throttle lives in memory only).
func TestP06_UnlockThrottleAndRestartReset(t *testing.T) {
	pc := newCFSPC(t, cfsPassA, true)
	if res := pc.do(http.MethodPost, "/api/master-key/lock", `{}`); res.Code != http.StatusOK {
		t.Fatalf("lock = %d %s", res.Code, res.Body)
	}
	for i := 0; i < 5; i++ {
		cfsReason(t, pc.do(http.MethodPost, "/api/master-key/unlock", unlockBody(cfsPassB)), http.StatusUnauthorized, "wrong-key")
	}
	for i := 0; i < 5; i++ {
		cfsReason(t, pc.do(http.MethodPost, "/api/master-key/unlock", unlockBody(cfsPassB)), http.StatusTooManyRequests, "throttled")
	}
	cfsReason(t, pc.do(http.MethodPost, "/api/master-key/unlock", unlockBody(cfsPassA)), http.StatusTooManyRequests, "throttled")
	np := p06Restart(pc)
	if res := np.do(http.MethodPost, "/api/master-key/unlock", unlockBody(cfsPassA)); res.Code != http.StatusOK {
		t.Fatalf("unlock after restart = %d %s", res.Code, res.Body)
	}
}

/* --------------------------------------------------------- passphrases */

// NFC and NFD spellings of one passphrase are the same key; the confirm box may
// differ in form from the first box.
func TestP06_SetupAcceptsNFCAndNFDSpellings(t *testing.T) {
	nfc := "café crème brûlée"
	nfd := "café crème brûlée"
	pc := newCFSPC(t, "", false)
	res := pc.do(http.MethodPost, "/api/master-key/setup", setupBody(nfc, nfd, true))
	if res.Code != http.StatusOK {
		t.Fatalf("setup NFC/NFD = %d %s", res.Code, res.Body)
	}
	pc.do(http.MethodPost, "/api/master-key/lock", `{}`)
	if res := pc.do(http.MethodPost, "/api/master-key/unlock", unlockBody(nfd)); res.Code != http.StatusOK {
		t.Fatalf("unlock with the NFD spelling = %d %s", res.Code, res.Body)
	}
}

// A key pasted with a trailing space or newline is a different key. It is not
// trimmed, on purpose; the screens should say so (see the web tests).
func TestP06_TrailingWhitespaceIsPartOfTheKey(t *testing.T) {
	pc := newCFSPC(t, "", false)
	if res := pc.do(http.MethodPost, "/api/master-key/setup", setupBody(cfsPassA, cfsPassA, true)); res.Code != http.StatusOK {
		t.Fatalf("setup = %d %s", res.Code, res.Body)
	}
	pc.do(http.MethodPost, "/api/master-key/lock", `{}`)
	for _, v := range []string{cfsPassA + " ", cfsPassA + "\n", " " + cfsPassA} {
		p06Rehandle(pc) // keep the throttle out of it
		cfsReason(t, pc.do(http.MethodPost, "/api/master-key/unlock", unlockBody(v)), http.StatusUnauthorized, "wrong-key")
	}
	// And the mismatch is caught at setup: "key" then "key ".
	pc2 := newCFSPC(t, "", false)
	res := pc2.do(http.MethodPost, "/api/master-key/setup", setupBody(cfsPassA, cfsPassA+" ", true))
	if res.Code == http.StatusOK {
		t.Fatalf("setup accepted a confirm with a trailing space: %s", res.Body)
	}
}

/* ----------------------------------------------------------- restore */

func p06Export(t *testing.T, pc *cfsPC, id string) (string, []byte) {
	t.Helper()
	path := filepath.Join(t0Dir(t), "case"+casestore.CaseFileExt)
	if res := pc.export(id, path, cfsPassA); res.Code != http.StatusOK {
		t.Fatalf("export = %d %s", res.Code, res.Body)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return path, data
}

// The same file restored twice makes two separate cases with all their data.
func TestP06_RestoreTheSameFileTwice(t *testing.T) {
	src := newCFSPC(t, cfsPassA, true)
	id := src.seed("twice", 5)
	path, _ := p06Export(t, src, id)
	dst := newCFSPC(t, cfsPassA, true)
	var got []string
	for i := 0; i < 2; i++ {
		res := dst.restore(path, cfsPassA)
		if res.Code != http.StatusCreated {
			t.Fatalf("restore #%d = %d %s", i+1, res.Code, res.Body)
		}
		var d caseDTO
		decodeBody(t, res, &d)
		got = append(got, d.ID)
		if d.Closed != nil {
			t.Errorf("restored case #%d is closed", i+1)
		}
	}
	if got[0] == got[1] {
		t.Fatalf("two restores share one id %s", got[0])
	}
	for _, g := range got {
		if n := dst.eventCount(g); n != 5 {
			t.Errorf("case %s has %d events, want 5", g, n)
		}
	}
	if names := p06Names(dst.root); len(names) != 2 {
		t.Errorf("case folder holds %v, want two case folders", names)
	}
}

// Restore with the wrong key, and while locked: nothing is created and nothing
// is left behind.
func TestP06_RestoreRefusalsLeaveNothing(t *testing.T) {
	src := newCFSPC(t, cfsPassA, true)
	id := src.seed("refusals", 5)
	path, _ := p06Export(t, src, id)
	dst := newCFSPC(t, cfsPassB, true)
	cfsReason(t, dst.restore(path, cfsPassB), http.StatusUnprocessableEntity, "bad-case-file")
	if res := dst.do(http.MethodPost, "/api/master-key/lock", `{}`); res.Code != http.StatusOK {
		t.Fatalf("lock = %d", res.Code)
	}
	if res := dst.restore(path, cfsPassA); res.Code != http.StatusLocked {
		t.Fatalf("restore while locked = %d %s, want 423", res.Code, res.Body)
	}
	if n := len(dst.caseIDs()); n != 0 {
		t.Fatalf("%d case(s) created by refused restores", n)
	}
	if names := p06Names(dst.root); len(names) != 0 {
		t.Fatalf("debris in the case folder: %v", names)
	}
}

// Every kind of damaged or wrong file is 422 bad-case-file (or 400 bad-path for
// a wrong name), never a 5xx, never a case, never a leftover.
func TestP06_RestoreCorruptionSweep(t *testing.T) {
	src := newCFSPC(t, cfsPassA, true)
	id := src.seed("sweep", 6)
	_, data := p06Export(t, src, id)
	out := t0Dir(t)
	dst := newCFSPC(t, cfsPassB, true)
	var offsets []int
	for i := 0; i < 41; i++ { // the whole prefix, byte by byte
		offsets = append(offsets, i)
	}
	hl := int(binary.BigEndian.Uint32(data[37:41]))
	for _, o := range []int{41, 42, 41 + hl/2, 41 + hl - 1, 41 + hl, 41 + hl + 1, len(data) / 2, len(data) - 2, len(data) - 1} {
		offsets = append(offsets, o)
	}
	check := func(name, path, pass string) {
		t.Helper()
		p06Rehandle(dst) // each try gets a fresh throttle
		res := dst.restore(path, pass)
		switch res.Code {
		case http.StatusUnprocessableEntity:
			cfsReason(t, res, http.StatusUnprocessableEntity, "bad-case-file")
		case http.StatusBadRequest:
		default:
			t.Errorf("%s: restore = %d %s", name, res.Code, res.Body)
		}
		if n := len(dst.caseIDs()); n != 0 {
			t.Errorf("%s: %d case(s) created", name, n)
		}
		if names := p06Names(dst.root); len(names) != 0 {
			t.Errorf("%s: debris %v", name, names)
		}
	}
	write := func(name string, b []byte) string {
		p := filepath.Join(out, name+casestore.CaseFileExt)
		if err := os.WriteFile(p, b, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	for _, o := range offsets {
		b := append([]byte(nil), data...)
		b[o] ^= 0x01
		check("flip@"+strconv.Itoa(o), write("flip", b), cfsPassA)
	}
	for _, n := range []int{0, 1, 8, 40, 41, 42, 60, 41 + hl - 1, 41 + hl, len(data) - 1} {
		check("cut@"+strconv.Itoa(n), write("cut", data[:n]), cfsPassA)
	}
	check("text file renamed", write("note", []byte("Dear diary, this is not a case.\n")), cfsPassA)
	check("zeros", write("zeros", make([]byte, 4096)), cfsPassA)
	// A good file under the wrong name is refused before it is read.
	wrong := filepath.Join(out, "case.txt")
	if err := os.WriteFile(wrong, data, 0o600); err != nil {
		t.Fatal(err)
	}
	check("wrong extension", wrong, cfsPassA)
	// The good file still restores after all of that (no state was damaged).
	p06Rehandle(dst)
	if res := dst.restore(write("good", data), cfsPassA); res.Code != http.StatusCreated {
		t.Fatalf("the good file = %d %s", res.Code, res.Body)
	}
}

// A header that claims an absurd Argon2 cost is refused before any hashing.
func TestP06_RestoreHostileKDFHeaderIsRefusedFast(t *testing.T) {
	src := newCFSPC(t, cfsPassA, true)
	id := src.seed("kdf", 3)
	_, data := p06Export(t, src, id)
	out := t0Dir(t)
	dst := newCFSPC(t, cfsPassB, true)
	patch := func(off int, v uint32) []byte {
		b := append([]byte(nil), data...)
		binary.BigEndian.PutUint32(b[off:], v)
		return b
	}
	for name, b := range map[string][]byte{
		"memory 4 TiB": patch(15, 1<<32-1),
		"time 4e9":     patch(11, 1<<32-1),
		"memory 0":     patch(15, 0),
		"header 4 GiB": patch(37, 1<<32-1),
	} {
		p := filepath.Join(out, "hostile"+casestore.CaseFileExt)
		if err := os.WriteFile(p, b, 0o600); err != nil {
			t.Fatal(err)
		}
		p06Rehandle(dst)
		start := time.Now()
		cfsReason(t, dst.restore(p, cfsPassA), http.StatusUnprocessableEntity, "bad-case-file")
		if d := time.Since(start); d > 2*time.Second {
			t.Errorf("%s took %s", name, d)
		}
	}
}

// A closed case exports and restores; an erased or unknown case is 404 and
// writes nothing.
func TestP06_ExportOfClosedErasedAndUnknownCases(t *testing.T) {
	pc := newCFSPC(t, cfsPassA, true)
	id := pc.seed("closing", 3)
	if res := pc.do(http.MethodPost, "/api/cases/"+id+"/close", ""); res.Code >= 300 {
		t.Fatalf("close = %d %s", res.Code, res.Body)
	}
	out := t0Dir(t)
	closed := filepath.Join(out, "closed"+casestore.CaseFileExt)
	if res := pc.export(id, closed, cfsPassA); res.Code != http.StatusOK {
		t.Fatalf("export of a closed case = %d %s", res.Code, res.Body)
	}
	dst := newCFSPC(t, cfsPassA, true)
	res := dst.restore(closed, cfsPassA)
	if res.Code != http.StatusCreated {
		t.Fatalf("restore of a closed case = %d %s", res.Code, res.Body)
	}
	if res := pc.do(http.MethodDelete, "/api/cases/"+id, `{"confirm":"`+id+`"}`); res.Code >= 300 {
		t.Fatalf("erase = %d %s", res.Code, res.Body)
	}
	gone := filepath.Join(out, "gone"+casestore.CaseFileExt)
	for _, c := range []string{id, strings.Repeat("a", len(id))} {
		res := pc.export(c, gone, cfsPassA)
		if res.Code != http.StatusNotFound && res.Code != http.StatusBadRequest {
			t.Errorf("export of %s = %d %s, want 404", c, res.Code, res.Body)
		}
	}
	if _, err := os.Stat(gone); err == nil {
		t.Error("an export of an erased case wrote a file")
	}
}

/* ------------------------------------------------------ export targets */

func TestP06_ExportDestinationOddities(t *testing.T) {
	pc := newCFSPC(t, cfsPassA, true)
	id := pc.seed("targets", 3)
	out := t0Dir(t)
	existing := filepath.Join(out, "taken"+casestore.CaseFileExt)
	if err := os.WriteFile(existing, []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	dirNamed := filepath.Join(out, "folder"+casestore.CaseFileExt)
	if err := os.Mkdir(dirNamed, 0o700); err != nil {
		t.Fatal(err)
	}
	long := filepath.Join(out, strings.Repeat("a", 300)+casestore.CaseFileExt)
	veryLong := out + strings.Repeat(`\sub`, 90) + `\x` + casestore.CaseFileExt
	targets := map[string]string{
		"existing file":     existing,
		"folder with ext":   dirNamed,
		"device CON":        filepath.Join(out, "CON"+casestore.CaseFileExt),
		"device NUL":        filepath.Join(out, "nul"+casestore.CaseFileExt),
		"device COM1":       filepath.Join(out, "COM1"+casestore.CaseFileExt),
		"trailing dot":      existing + ".",
		"trailing space":    existing + " ",
		"300 char name":     long,
		"very long path":    veryLong,
		"UNC":               `\\server\share\case` + casestore.CaseFileExt,
		"extended prefix":   `\\?\` + filepath.Join(out, "x"+casestore.CaseFileExt),
		"dotdot":            out + `\..\` + filepath.Base(out) + `\up` + casestore.CaseFileExt,
		"relative":          "case" + casestore.CaseFileExt,
		"empty":             "",
		"NUL char":          filepath.Join(out, "a\x00b"+casestore.CaseFileExt),
		"alternate stream":  existing + ":stream",
		"wrong extension":   filepath.Join(out, "case.txt"),
		"inside cases root": filepath.Join(pc.root, "x"+casestore.CaseFileExt),
		"the cases root":    pc.root,
	}
	before := p06Names(out)
	for name, target := range targets {
		p06Rehandle(pc)
		res := pc.export(id, target, cfsPassA)
		if res.Code != http.StatusBadRequest && res.Code != http.StatusConflict {
			t.Errorf("%s: export = %d %s, want 400 bad-path or 409 file-exists", name, res.Code, res.Body)
		}
	}
	if b, _ := os.ReadFile(existing); string(b) != "mine" {
		t.Errorf("an existing file was overwritten: %q", b)
	}
	after := p06Names(out)
	if len(after) != len(before) {
		t.Errorf("destination folder changed: %v -> %v", before, after)
	}
	if names := p06Names(pc.root); len(names) != 1 {
		t.Errorf("the cases folder holds %v, want just the one case", names)
	}
}

// A folder as the target gets the name <case id>.unravis-case. Exporting the
// same case into the same folder again is refused with 409 file-exists and the
// first file is left alone (it is never overwritten, never numbered).
func TestP06_ExportToAFolderNeverOverwrites(t *testing.T) {
	pc := newCFSPC(t, cfsPassA, true)
	id := pc.seed("folder", 3)
	out := t0Dir(t)
	res := pc.export(id, out, cfsPassA)
	if res.Code != http.StatusOK {
		t.Fatalf("export to a folder = %d %s", res.Code, res.Body)
	}
	first, err := os.ReadFile(filepath.Join(out, id+casestore.CaseFileExt))
	if err != nil {
		t.Fatal(err)
	}
	cfsReason(t, pc.export(id, out, cfsPassA), http.StatusConflict, "file-exists")
	after, _ := os.ReadFile(filepath.Join(out, id+casestore.CaseFileExt))
	if string(after) != string(first) || len(p06Names(out)) != 1 {
		t.Fatalf("the first export was touched: %v", p06Names(out))
	}
}

/* ------------------------------------------------------------ case ids */

func TestP06_OddCaseIDSpellings(t *testing.T) {
	pc := newCFSPC(t, cfsPassA, true)
	id := pc.seed("ids", 2)
	out := t0Dir(t)
	odd := []string{
		strings.ToUpper(id), " " + id, id + " ", id[:13] + "-" + id[13:], id[:13] + " " + id[13:],
		"%20" + id, id + "%00", "..", "%2e%2e", "c-" + strings.Repeat("0", 200),
		"с" + id[1:], // Cyrillic es for the first letter
		"ｃ" + id[1:], // fullwidth c
		id + "​",
	}
	for _, o := range odd {
		seg := url.PathEscape(o)
		res := pc.do(http.MethodGet, "/api/cases/"+seg, "")
		if res.Code != http.StatusBadRequest && res.Code != http.StatusNotFound {
			t.Errorf("GET %q = %d %s", o, res.Code, res.Body)
		}
		res = pc.do(http.MethodPost, "/api/cases/"+seg+"/export", `{"path":`+q(filepath.Join(out, "x"+casestore.CaseFileExt))+`,"passphrase":`+q(cfsPassA)+`}`)
		if res.Code != http.StatusBadRequest && res.Code != http.StatusNotFound {
			t.Errorf("export %q = %d %s", o, res.Code, res.Body)
		}
	}
	if names := p06Names(out); len(names) != 0 {
		t.Errorf("odd ids wrote %v", names)
	}
}

/* ---------------------------------------------------------- lock races */

// Lock while an export runs, over and over: the export either completes with a
// file that restores, or fails with no file and no temp debris.
func TestP06_LockDuringExportLeavesNoHalfFile(t *testing.T) {
	pc := newCFSPC(t, cfsPassA, true)
	id := pc.seed("race", 400)
	dst := newCFSPC(t, cfsPassA, true)
	completed, refused := 0, 0
	for i := 0; i < 6; i++ {
		out := t0Dir(t)
		target := filepath.Join(out, "race"+casestore.CaseFileExt)
		p06Rehandle(pc)
		var wg sync.WaitGroup
		var code int
		var body string
		wg.Add(2)
		go func() {
			defer wg.Done()
			r := pc.export(id, target, cfsPassA)
			code, body = r.Code, r.Body.String()
		}()
		go func() {
			defer wg.Done()
			time.Sleep(time.Duration(i*40) * time.Millisecond)
			pc.do(http.MethodPost, "/api/master-key/lock", `{}`)
		}()
		wg.Wait()
		names := p06Names(out)
		if code == http.StatusOK {
			completed++
			if len(names) != 1 {
				t.Errorf("run %d: export ok but folder holds %v", i, names)
			} else {
				p06Rehandle(dst)
				if r := dst.restore(target, cfsPassA); r.Code != http.StatusCreated {
					t.Errorf("run %d: the exported file does not restore: %d %s", i, r.Code, r.Body)
				}
			}
		} else {
			refused++
			if code >= 500 {
				t.Errorf("run %d: export = %d %s", i, code, body)
			}
			if len(names) != 0 {
				t.Errorf("run %d: export failed with %d but left %v", i, code, names)
			}
		}
		if r := pc.do(http.MethodPost, "/api/master-key/unlock", unlockBody(cfsPassA)); r.Code != http.StatusOK {
			t.Fatalf("run %d: unlock = %d %s", i, r.Code, r.Body)
		}
	}
	t.Logf("lock raced an export %d times: %d finished, %d refused", completed+refused, completed, refused)
}

/* ---------------------------------------------------- restore staging */

// Restore stages the database as restore-<hex>.part in the cases folder. The
// file is removed on every handled outcome, but a crash or power cut in the
// middle leaves it behind, and it can be as large as a case (up to 8 GiB).
// Sweep, which runs at every start, removes one older than an hour.
func TestP06_RestoreStagingLeftoverIsSwept(t *testing.T) {
	pc := newCFSPC(t, cfsPassA, true)
	orphan := filepath.Join(pc.root, "restore-0123456789abcdef.part")
	if err := os.WriteFile(orphan, make([]byte, 64<<10), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(orphan, old, old); err != nil {
		t.Fatal(err)
	}
	np := p06Restart(pc) // a new start of the app over the same folder
	if _, err := np.store.Sweep(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(orphan); err == nil {
		t.Fatal("a two-day-old restore-*.part is still in the cases folder after a restart and a sweep")
	}
}
