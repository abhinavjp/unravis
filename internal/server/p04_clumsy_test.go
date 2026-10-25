package server

// Phase 04 clumsy-user review. TestP04Fixed_* were confirmed defects
// (TestBug_*) that are now fixed; TestP04_* pin behaviour that held.

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/text/unicode/norm"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/masterkey"
)

/* ------------------------------------------------------------ lock gate */

// A locked PC must not answer anything under /api/cases, however the URL is
// spelled or whatever the method is.
func TestP04_LockedGateOddSpellingsLeakNothing(t *testing.T) {
	e := newMKEnv(t)
	e.presetKey(mkPass)
	if res := e.do(http.MethodPost, "/api/master-key/unlock", unlockBody(mkPass)); res.Code != http.StatusOK {
		t.Fatalf("unlock = %d %s", res.Code, res.Body)
	}
	const title = "SECRET-TITLE-p04-gate"
	id := e.newCase(title)
	if res := e.do(http.MethodPost, "/api/master-key/lock", ""); res.Code != http.StatusOK {
		t.Fatalf("lock = %d", res.Code)
	}

	paths := []string{
		"/api/cases", "/api/cases/", "/api/cases?x=1", "/api/cases/" + id, "/api/cases/" + id + "/",
		"/api/cases/" + id + "/events", "/api/cases/" + id + "/export", "/api/cases/restore",
		"/api/cases/" + strings.ToUpper(id), "/api/cases/%2e%2e/cases", "/api/cases/" + id + "/..%2f..%2fcases",
		"/api/cases%2F" + id, "//api/cases", "/api//cases", "/api/cases/./" + id, "/api/cases/" + id + "%00",
		"/API/cases", "/api/Cases",
	}
	methods := []string{http.MethodGet, http.MethodPost, http.MethodHead, http.MethodOptions, http.MethodPut, http.MethodDelete, http.MethodPatch, "BREW"}
	for _, p := range paths {
		for _, m := range methods {
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("%s %s panicked: %v", m, p, r)
					}
				}()
				req := httptest.NewRequest(m, "http://127.0.0.1:8765/", strings.NewReader(""))
				req.URL.Path = p
				req.URL.RawPath = ""
				if i := strings.IndexByte(p, '?'); i >= 0 {
					req.URL.Path, req.URL.RawQuery = p[:i], p[i+1:]
				}
				req.RequestURI = p
				req.Host = "127.0.0.1:8765"
				req.Header.Set("X-Audit-Token", testToken)
				res := httptest.NewRecorder()
				e.h.ServeHTTP(res, req)
				body := res.Body.String()
				if strings.Contains(body, title) || strings.Contains(body, id) {
					t.Errorf("%s %s leaked case data while locked: %d %s", m, p, res.Code, body)
				}
				if res.Code == http.StatusOK || res.Code == http.StatusCreated {
					if m != http.MethodOptions && m != http.MethodHead {
						t.Errorf("%s %s answered %d while locked", m, p, res.Code)
					}
				}
				if res.Code >= 500 {
					t.Errorf("%s %s = %d %s", m, p, res.Code, body)
				}
			}()
		}
	}
}

/* --------------------------------------------------- double-submit races */

func TestP04_ConcurrentUnlockDoubleClickAllSucceed(t *testing.T) {
	e := newMKEnv(t)
	e.presetKey(mkPass)
	var wg sync.WaitGroup
	codes := make([]int, 6)
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i] = e.do(http.MethodPost, "/api/master-key/unlock", unlockBody(mkPass)).Code
		}(i)
	}
	wg.Wait()
	for i, c := range codes {
		if c != http.StatusOK {
			t.Errorf("unlock #%d = %d, want 200", i, c)
		}
	}
	if s := e.status(); s.State != "unlocked" {
		t.Fatalf("state = %q", s.State)
	}
}

func TestP04_RestoreDoubleClickMakesOneCaseAndNoDebris(t *testing.T) {
	src := newCFSPC(t, cfsPassA, true)
	dst := newCFSPC(t, cfsPassB, true)
	id := src.seed("double click", 20)
	out := t0Dir(t)
	file := filepath.Join(out, "dc"+casestore.CaseFileExt)
	if res := src.export(id, file, cfsPassA); res.Code != http.StatusOK {
		t.Fatalf("export = %d %s", res.Code, res.Body)
	}
	var wg sync.WaitGroup
	codes := make([]int, 2)
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i] = dst.restore(file, cfsPassA).Code
		}(i)
	}
	wg.Wait()
	created := 0
	for i, c := range codes {
		switch c {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
		default:
			t.Errorf("restore #%d = %d", i, c)
		}
	}
	if created < 1 {
		t.Fatalf("no restore succeeded: %v", codes)
	}
	if n := len(dst.caseIDs()); n != created {
		t.Fatalf("%d cases for %d successful restores", n, created)
	}
	ents, _ := os.ReadDir(dst.root)
	for _, en := range ents {
		if strings.HasSuffix(en.Name(), ".part") {
			t.Errorf("debris left in the case folder: %s", en.Name())
		}
	}
}

// Lock racing a restore must end in 201/423/409, never a 500, and never leave a
// staged .part file behind.
func TestP04_LockRacingRestoreIsClean(t *testing.T) {
	src := newCFSPC(t, cfsPassA, true)
	dst := newCFSPC(t, cfsPassB, true)
	id := src.seed("race", 200)
	out := t0Dir(t)
	file := filepath.Join(out, "race"+casestore.CaseFileExt)
	if res := src.export(id, file, cfsPassA); res.Code != http.StatusOK {
		t.Fatalf("export = %d %s", res.Code, res.Body)
	}
	for round := 0; round < 3; round++ {
		var wg sync.WaitGroup
		var rc, lc int
		wg.Add(2)
		go func() { defer wg.Done(); rc = dst.restore(file, cfsPassA).Code }()
		go func() {
			defer wg.Done()
			time.Sleep(time.Duration(round) * time.Millisecond)
			lc = dst.do(http.MethodPost, "/api/master-key/lock", "").Code
		}()
		wg.Wait()
		if rc >= 500 || (rc != http.StatusCreated && rc != http.StatusLocked && rc != http.StatusConflict) {
			t.Errorf("round %d: restore = %d (lock %d)", round, rc, lc)
		}
		ents, _ := os.ReadDir(dst.root)
		for _, en := range ents {
			if strings.HasSuffix(en.Name(), ".part") {
				t.Errorf("round %d: debris %s", round, en.Name())
			}
		}
		// unlock again for the next round
		if res := dst.do(http.MethodPost, "/api/master-key/unlock", unlockBody(cfsPassB)); res.Code != http.StatusOK {
			t.Fatalf("re-unlock = %d %s", res.Code, res.Body)
		}
	}
}

/* ------------------------------------------------------ passphrase shapes */

func TestP04_PassphraseLengthBoundaries(t *testing.T) {
	e := newMKEnv(t)
	// 11 vs 12 characters, counted after NFC; nothing is hashed for a refusal.
	for _, tc := range []struct {
		name, pass, reason string
	}{
		{"eleven", "abcdefghijk", "too-short"},
		{"empty", "", "too-short"},
		{"twelve composed pairs decompose to six", strings.Repeat(norm.NFD.String("é"), 6), "too-short"},
		{"eleven emoji", strings.Repeat("\U0001F600", 11), "too-short"},
		{"over 1024 bytes", strings.Repeat("p", 1025), "too-long"},
	} {
		res := e.do(http.MethodPost, "/api/master-key/setup", setupBody(tc.pass, tc.pass, true))
		if res.Code != http.StatusBadRequest && res.Code != http.StatusRequestEntityTooLarge {
			t.Errorf("%s: setup = %d %s", tc.name, res.Code, res.Body)
			continue
		}
		var f mkFail
		decodeBody(t, res, &f)
		if f.Reason != tc.reason {
			t.Errorf("%s: reason = %q, want %q", tc.name, f.Reason, tc.reason)
		}
	}
	if s := e.status(); s.State != "none" {
		t.Fatalf("a refused setup left state %q", s.State)
	}
	// Exactly twelve emoji (one rune each, 48 bytes) is accepted. A fresh
	// server: the refusals above already tripped the setup throttle.
	e = newMKEnv(t)
	pass := strings.Repeat("\U0001F600", 12)
	if res := e.do(http.MethodPost, "/api/master-key/setup", setupBody(pass, pass, true)); res.Code != http.StatusOK {
		t.Fatalf("twelve emoji = %d %s", res.Code, res.Body)
	}
}

// The same passphrase typed composed or decomposed must open a case file.
func TestP04_CaseFilePassphraseIsNormalisedAcrossPCs(t *testing.T) {
	composed := "crème brûlée au café"
	decomposed := norm.NFD.String(composed)
	if composed == decomposed {
		t.Fatal("test strings are identical")
	}
	src := newCFSPC(t, composed, true)
	dst := newCFSPC(t, cfsPassB, true)
	id := src.seed("nfc", 4)
	out := t0Dir(t)
	file := filepath.Join(out, "n"+casestore.CaseFileExt)
	if res := src.export(id, file, decomposed); res.Code != http.StatusOK {
		t.Fatalf("export with the decomposed spelling = %d %s", res.Code, res.Body)
	}
	if res := dst.restore(file, composed); res.Code != http.StatusCreated {
		t.Fatalf("restore with the composed spelling = %d %s", res.Code, res.Body)
	}
}

// Passphrases and paths typed into the case-file routes must not reach the log.
func TestP04_CaseFileFailuresDoNotLogPassphraseOrPath(t *testing.T) {
	const wrong = "LEAKCHECK-wrong-passphrase-xyz"
	pc := newCFSPC(t, cfsPassA, true)
	id := pc.seed("leak", 2)
	out := t0Dir(t)
	file := filepath.Join(out, "leak"+casestore.CaseFileExt)
	pc.export(id, file, wrong)
	pc.export(id, file, cfsPassA)
	pc.restore(filepath.Join(out, "missing"+casestore.CaseFileExt), wrong)
	pc.restore(file, wrong)
	pc.restore(file, cfsPassA)
	log := pc.logs.String()
	for _, s := range []string{wrong, cfsPassA, out, "leak" + casestore.CaseFileExt} {
		if strings.Contains(log, s) {
			t.Errorf("log contains %q: %s", s, log)
		}
	}
}

/* ------------------------------------------------- odd Windows file names */

// Reserved device names are refused up front (opening NUL.unravis-case would
// address the NUL device, not a file). Holds.
func TestP04_DeviceNamesAreRefusedByPathShape(t *testing.T) {
	pc := newCFSPC(t, cfsPassA, true)
	id := pc.seed("device", 2)
	out := t0Dir(t)
	for _, name := range []string{"NUL", "nul", "CON", "AUX", "PRN", "COM1", "LPT1"} {
		p := filepath.Join(out, name+casestore.CaseFileExt)
		if _, ok := checkCasePath(p); ok {
			t.Errorf("checkCasePath accepts reserved device name %q", p)
		}
	}
	// Prove the consequence for the one device that cannot block: NUL.
	res := pc.export(id, filepath.Join(out, "NUL"+casestore.CaseFileExt), cfsPassA)
	if res.Code == http.StatusOK {
		t.Errorf("export to a NUL device path answered 200: %s", res.Body)
	}
}

// Restore must also refuse the extension in another case (the Explorer shows
// it as typed).
func TestP04_RestoreExtensionCaseAndSpaces(t *testing.T) {
	src := newCFSPC(t, cfsPassA, true)
	dst := newCFSPC(t, cfsPassB, true)
	id := src.seed("ext", 3)
	out := t0Dir(t)
	file := filepath.Join(out, "ok"+casestore.CaseFileExt)
	if res := src.export(id, file, cfsPassA); res.Code != http.StatusOK {
		t.Fatalf("export = %d", res.Code)
	}
	upper := filepath.Join(out, "UP.UNRAVIS-CASE")
	data, _ := os.ReadFile(file)
	if err := os.WriteFile(upper, data, 0o600); err != nil {
		t.Fatal(err)
	}
	res := dst.restore(upper, cfsPassA)
	if res.Code != http.StatusCreated && res.Code != http.StatusBadRequest {
		t.Fatalf("upper-case extension = %d %s", res.Code, res.Body)
	}
	t.Logf("upper-case extension restore = %d", res.Code)
	// Trailing dot or space is refused rather than silently opening another name.
	for _, p := range []string{file + ".", file + " ", file + "\t"} {
		cfsReason(t, dst.restore(p, cfsPassA), http.StatusBadRequest, "bad-path")
	}
	// Surrounding quotes (Explorer "Copy as path") are the web layer's job; the
	// server must refuse them rather than guess.
	cfsReason(t, dst.restore(`"`+file+`"`, cfsPassA), http.StatusBadRequest, "bad-path")
}

/* ------------------------------------------ passphrase encodings (fixed) */

// Twelve zero-width spaces, blanks or ideographic spaces pass the length rule
// but are nobody's idea of a master key. The server counts visible characters.
func TestP04Fixed_InvisibleOrBlankPassphraseIsRefused(t *testing.T) {
	for name, pass := range map[string]string{
		"zero-width spaces": strings.Repeat("​", 12),
		"blanks":            strings.Repeat(" ", 12),
		"ideographic":       strings.Repeat("　", 12),
		"bidi marks":        strings.Repeat("‮", 12),
		"hangul filler":     strings.Repeat("ㅤ", 12),
		"mostly blanks":     "abcdefg" + strings.Repeat(" ", 9),
	} {
		e := newMKEnv(t)
		res := e.do(http.MethodPost, "/api/master-key/setup", setupBody(pass, pass, true))
		if res.Code != http.StatusBadRequest {
			t.Errorf("%s: setup = %d %s, want 400", name, res.Code, res.Body)
			continue
		}
		var f mkFail
		decodeBody(t, res, &f)
		if f.Reason != "blank" {
			t.Errorf("%s: reason = %q, want blank", name, f.Reason)
		}
		if s := e.status(); s.State != "none" {
			t.Errorf("%s: a refused setup left state %q", name, s.State)
		}
	}
	// Spaces inside a real phrase are fine.
	e := newMKEnv(t)
	p := "a b c d e f g h"
	if res := e.do(http.MethodPost, "/api/master-key/setup", setupBody(p, p, true)); res.Code != http.StatusOK {
		t.Fatalf("spaced phrase = %d %s", res.Code, res.Body)
	}
}

// Two different lone-surrogate escapes both decode to U+FFFD; setup must refuse
// them rather than let different typed passphrases be the same key.
func TestP04Fixed_LoneSurrogatePassphrasesAreRefused(t *testing.T) {
	for name, esc := range map[string]string{
		"lone high": `\ud800`,
		"lone low":  `\ude00`,
		"literal":   "�",
	} {
		e := newMKEnv(t)
		p := strings.Repeat(esc, 12)
		res := e.do(http.MethodPost, "/api/master-key/setup", `{"passphrase":"`+p+`","confirm":"`+p+`","acknowledged":true}`)
		if res.Code != http.StatusBadRequest {
			t.Errorf("%s: setup = %d %s, want 400", name, res.Code, res.Body)
			continue
		}
		var f mkFail
		decodeBody(t, res, &f)
		if f.Reason != "invalid-characters" {
			t.Errorf("%s: reason = %q, want invalid-characters", name, f.Reason)
		}
		if s := e.status(); s.State != "none" {
			t.Errorf("%s: state %q", name, s.State)
		}
	}
	// A surrogate pair is a real character and stays fine.
	e := newMKEnv(t)
	pair := strings.Repeat(`😀`, 12)
	if res := e.do(http.MethodPost, "/api/master-key/setup", `{"passphrase":"`+pair+`","confirm":"`+pair+`","acknowledged":true}`); res.Code != http.StatusOK {
		t.Fatalf("surrogate pairs = %d %s", res.Code, res.Body)
	}
}

// Validation failures (typos, short, blank, no tick) answer no secret question
// and must never lock a fresh user out of setting up.
func TestP04Fixed_SetupValidationFailuresAreNotThrottled(t *testing.T) {
	e := newMKEnv(t)
	post := func(body string) *httptest.ResponseRecorder {
		return e.do(http.MethodPost, "/api/master-key/setup", body)
	}
	for i := 0; i < 12; i++ {
		switch i % 5 {
		case 0:
			e.wantFail(post(setupBody(mkPass, "different "+strconv.Itoa(i), true)), http.StatusBadRequest, "mismatch")
		case 1:
			e.wantFail(post(setupBody("short", "short", true)), http.StatusBadRequest, "too-short")
		case 2:
			e.wantFail(post(setupBody(mkPass, mkPass, false)), http.StatusBadRequest, "acknowledgement-required")
		case 3:
			b := strings.Repeat(" ", 12)
			e.wantFail(post(setupBody(b, b, true)), http.StatusBadRequest, "blank")
		case 4:
			e.wantFail(post(setupBody(strings.Repeat("p", 1025), strings.Repeat("p", 1025), true)), http.StatusBadRequest, "too-long")
		}
	}
	if w := e.sess.setupTh.wait(); w != 0 {
		t.Fatalf("validation failures started a setup throttle: %v", w)
	}
	if res := post(setupBody(mkPass, mkPass, true)); res.Code != http.StatusOK {
		t.Fatalf("setup after twelve typos = %d %s", res.Code, res.Body)
	}
}

/* ----------------------------------------------- damaged / odd key file */

func (e *mkEnv) wantDamaged() {
	e.t.Helper()
	if s := e.status(); s.State != "damaged" || s.DPAPICases != 0 {
		e.t.Fatalf("status = %+v, want damaged", s)
	}
}

// A damaged masterkey.dat is a state of its own: unlock and setup both say so
// (409 key-file-damaged), neither is counted by a throttle, and nothing on disk
// changes.
func TestP04Fixed_DamagedKeyFileIsItsOwnState(t *testing.T) {
	e := newMKEnv(t)
	e.presetKey(mkPass)
	for name, content := range map[string][]byte{"garbage": []byte("garbage"), "empty": nil, "truncated json": []byte(`{"v":1,"kdf":"argon2id"`)} {
		if err := os.WriteFile(masterkey.Path(e.dir), content, 0o600); err != nil {
			t.Fatal(err)
		}
		e.wantDamaged()
		for i := 0; i < 8; i++ { // more than the 5 free failures of the throttle
			e.wantFail(e.do(http.MethodPost, "/api/master-key/unlock", unlockBody(mkPass)), http.StatusConflict, "key-file-damaged")
		}
		f := e.wantFail(e.do(http.MethodPost, "/api/master-key/setup", setupBody(mkPass, mkPass, true)), http.StatusConflict, "key-file-damaged")
		if f.State != "damaged" {
			t.Errorf("%s: failure state = %q", name, f.State)
		}
		got, _ := os.ReadFile(masterkey.Path(e.dir))
		if string(got) != string(content) {
			t.Fatalf("%s: setup or unlock changed the damaged file", name)
		}
		if w := e.sess.unlockTh.wait(); w != 0 {
			t.Errorf("%s: damaged-file unlocks started a throttle", name)
		}
	}
}

// While the key file is damaged the case routes are shut exactly like a locked
// key, and a case cannot be created sealed with anything else.
func TestP04Fixed_DamagedKeyFileKeepsCasesClosed(t *testing.T) {
	e := newMKEnv(t)
	e.presetKey(mkPass)
	e.do(http.MethodPost, "/api/master-key/unlock", unlockBody(mkPass))
	e.newCase("kept closed")
	e.do(http.MethodPost, "/api/master-key/lock", "")
	if err := os.WriteFile(masterkey.Path(e.dir), []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, rt := range []struct{ m, p, b string }{
		{http.MethodGet, "/api/cases", ""}, {http.MethodPost, "/api/cases", `{"title":"x"}`},
	} {
		if res := e.do(rt.m, rt.p, rt.b); res.Code != http.StatusLocked {
			t.Errorf("%s %s = %d %s, want 423", rt.m, rt.p, res.Code, res.Body)
		}
	}
	if res := e.do(http.MethodPost, "/api/master-key/rewrap", `{"all":true}`); res.Code != http.StatusLocked {
		t.Errorf("rewrap = %d %s, want 423", res.Code, res.Body)
	}
	if res := e.do(http.MethodPost, "/api/master-key/lock", ""); res.Code != http.StatusOK {
		t.Errorf("lock while damaged = %d", res.Code)
	}
}

// Recovery: a separate, acknowledged action that moves the damaged item aside
// (never deletes), after which the ordinary setup works. A good key file is
// never touched by it.
func TestP04Fixed_ResetDamagedKeyFileIsExplicitAndKeepsTheOldFile(t *testing.T) {
	post := func(e *mkEnv, body string) *httptest.ResponseRecorder {
		return e.do(http.MethodPost, "/api/master-key/reset-damaged", body)
	}
	e := newMKEnv(t)
	e.presetKey(mkPass)
	good, _ := os.ReadFile(masterkey.Path(e.dir))

	// Refused while the key file is good, with or without the acknowledgement.
	e.wantFail(post(e, `{"acknowledged":true}`), http.StatusConflict, "not-damaged")
	e.wantFail(post(e, `{}`), http.StatusConflict, "not-damaged")
	if got, _ := os.ReadFile(masterkey.Path(e.dir)); string(got) != string(good) {
		t.Fatal("reset touched a good key file")
	}
	// Refused while unlocked, even if the file is damaged behind the session.
	e.do(http.MethodPost, "/api/master-key/unlock", unlockBody(mkPass))
	os.WriteFile(masterkey.Path(e.dir), []byte("garbage"), 0o600)
	e.wantFail(post(e, `{"acknowledged":true}`), http.StatusConflict, "not-damaged")
	if s := e.status(); s.State != "unlocked" {
		t.Fatalf("state = %q", s.State)
	}
	e.do(http.MethodPost, "/api/master-key/lock", "")
	e.wantDamaged()

	// Needs the acknowledgement, and exactly the right body.
	e.wantFail(post(e, `{}`), http.StatusBadRequest, "acknowledgement-required")
	e.wantFail(post(e, `{"acknowledged":false}`), http.StatusBadRequest, "acknowledgement-required")
	e.wantFail(post(e, `{"acknowledged":"true"}`), http.StatusBadRequest, "bad-request")
	e.wantFail(post(e, `{"acknowledged":true,"x":1}`), http.StatusBadRequest, "bad-request")
	e.wantFail(post(e, `{"acknowledged":true}{}`), http.StatusBadRequest, "bad-request")
	if got, _ := os.ReadFile(masterkey.Path(e.dir)); string(got) != "garbage" {
		t.Fatal("a refused reset changed the file")
	}

	res := post(e, `{"acknowledged":true}`)
	if res.Code != http.StatusOK {
		t.Fatalf("reset = %d %s", res.Code, res.Body)
	}
	var out struct {
		State      string `json:"state"`
		DPAPICases int    `json:"dpapiCases"`
		KeptAs     string `json:"keptAs"`
	}
	decodeBody(t, res, &out)
	if out.State != "none" || !strings.HasPrefix(out.KeptAs, "masterkey.dat.damaged-") || strings.ContainsAny(out.KeptAs, `\/`) {
		t.Fatalf("reset reply = %+v", out)
	}
	if kept, err := os.ReadFile(filepath.Join(e.dir, out.KeptAs)); err != nil || string(kept) != "garbage" {
		t.Fatalf("the damaged file was not kept: %v %q", err, kept)
	}
	if s := e.status(); s.State != "none" {
		t.Fatalf("state after reset = %+v", s)
	}
	// A second reset has nothing to do.
	e.wantFail(post(e, `{"acknowledged":true}`), http.StatusConflict, "not-damaged")
	// Now ordinary setup creates a new key.
	if res := e.do(http.MethodPost, "/api/master-key/setup", setupBody(mkPass, mkPass, true)); res.Code != http.StatusOK {
		t.Fatalf("setup after reset = %d %s", res.Code, res.Body)
	}
}

func TestP04_ZeroByteKeyFileDoesNotPanic(t *testing.T) {
	e := newMKEnv(t)
	if err := os.WriteFile(masterkey.Path(e.dir), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	s := e.status()
	res := e.do(http.MethodPost, "/api/master-key/unlock", unlockBody(mkPass))
	if res.Code >= 500 {
		t.Fatalf("unlock over an empty key file = %d %s", res.Code, res.Body)
	}
	t.Logf("empty file: status=%q unlock=%d", s.State, res.Code)
}

// masterkey.dat as a directory: status and setup agree it is damaged, and the
// reset moves the directory aside.
func TestP04Fixed_KeyFileAsDirectoryIsDamagedAndRecoverable(t *testing.T) {
	e := newMKEnv(t)
	if err := os.Mkdir(masterkey.Path(e.dir), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(masterkey.Path(e.dir), "inside.txt"), []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	e.wantDamaged()
	e.wantFail(e.do(http.MethodPost, "/api/master-key/setup", setupBody(mkPass, mkPass, true)), http.StatusConflict, "key-file-damaged")
	e.wantFail(e.do(http.MethodPost, "/api/master-key/unlock", unlockBody(mkPass)), http.StatusConflict, "key-file-damaged")
	res := e.do(http.MethodPost, "/api/master-key/reset-damaged", `{"acknowledged":true}`)
	if res.Code != http.StatusOK {
		t.Fatalf("reset = %d %s", res.Code, res.Body)
	}
	var out struct {
		KeptAs string `json:"keptAs"`
	}
	decodeBody(t, res, &out)
	if b, err := os.ReadFile(filepath.Join(e.dir, out.KeptAs, "inside.txt")); err != nil || string(b) != "keep me" {
		t.Fatalf("directory contents not kept: %v %q", err, b)
	}
	if res := e.do(http.MethodPost, "/api/master-key/setup", setupBody(mkPass, mkPass, true)); res.Code != http.StatusOK {
		t.Fatalf("setup after reset = %d %s", res.Code, res.Body)
	}
}

/* ------------------------------------------------------ existence oracle */

// A token holder can tell a real case id from an unknown one on export (404
// before the passphrase check). Recorded for the report; not a defect while the
// token is the boundary.
func TestP04_ExportExistenceOracleIsBehindTheToken(t *testing.T) {
	pc := newCFSPC(t, cfsPassA, true)
	id := pc.seed("oracle", 1)
	out := t0Dir(t)
	unknown, _ := casestore.NewID()
	real := pc.export(id, out, "wrong passphrase here")
	fake := pc.export(string(unknown), out, "wrong passphrase here")
	t.Logf("known id with wrong pass = %d; unknown id = %d", real.Code, fake.Code)
	if res := request(pc.h, http.MethodPost, "/api/cases/"+id+"/export", `{}`, false); res.Code != http.StatusUnauthorized {
		t.Fatalf("export without the token = %d", res.Code)
	}
}

// A person who types forward slashes (C:/Cases/x.unravis-case) passes the web
// form's check; the server must either open the file or say bad-path, never
// 500. Logged so the report can say which.
func TestP04_RestoreForwardSlashPath(t *testing.T) {
	src := newCFSPC(t, cfsPassA, true)
	dst := newCFSPC(t, cfsPassB, true)
	id := src.seed("slashes", 3)
	out := t0Dir(t)
	file := filepath.Join(out, "s"+casestore.CaseFileExt)
	if res := src.export(id, file, cfsPassA); res.Code != http.StatusOK {
		t.Fatalf("export = %d", res.Code)
	}
	fwd := strings.ReplaceAll(file, `\`, "/")
	res := dst.restore(fwd, cfsPassA)
	t.Logf("forward-slash restore = %d %s", res.Code, res.Body)
	if res.Code >= 500 {
		t.Fatalf("restore = %d", res.Code)
	}
	if res.Code != http.StatusCreated && res.Code != http.StatusBadRequest {
		t.Fatalf("restore = %d", res.Code)
	}
}
