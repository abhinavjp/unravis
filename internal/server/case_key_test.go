package server

import (
	"bytes"
	"context"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/casestore/sqlite"
	"api-audit-explorer/internal/casestore/storetest"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/masterkey"
)

const (
	ckPass  = "first case key phrase"
	ckPass2 = "second case key phrase"
	mkPass2 = "a second master key phrase"
)

// newCKEnv is newMKEnv with small Argon2 costs for the per-case keys.
func newCKEnv(t *testing.T, extra ...Option) *mkEnv {
	t.Helper()
	logs := &bytes.Buffer{}
	log.SetOutput(logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	dir := t.TempDir()
	sess := NewMasterKeySession(dir)
	root := filepath.Join(t.TempDir(), "cases")
	store, err := sqlite.New(sqlite.Options{
		Root: root, Protector: storetest.RandomProtector(), MinFreeBytes: -1,
		Protectors: sess.Lookup, NewCaseKind: sess.NewCaseKind,
		LegacyCheck: sess.LegacyCheck, LegacyDone: sess.LegacyDone,
		KeyParams: cfsFast,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	e := &mkEnv{t: t, sess: sess, store: store, dir: dir, root: root, logs: logs}
	e.h = New(&fakeRepository{}, testAssets(), testToken, false, append([]Option{
		WithCases(store, mustHosts(t, hostsDoc), importer.NewRegistry(toyImporter{name: "toy", score: 90})),
		WithMasterKey(sess),
	}, extra...)...)
	return e
}

func ckCreateBody(title, key, confirm string) string {
	return `{"title":` + q(title) + `,"caseKey":` + q(key) + `,"caseKeyConfirm":` + q(confirm) + `}`
}

func (e *mkEnv) newKeyedCase(title string) caseDTO {
	e.t.Helper()
	res := e.do(http.MethodPost, "/api/cases", ckCreateBody(title, ckPass, ckPass))
	if res.Code != http.StatusCreated {
		e.t.Fatalf("create keyed case = %d %s", res.Code, res.Body)
	}
	var out caseDTO
	decodeBody(e.t, res, &out)
	return out
}

func (e *mkEnv) keyCall(id, action, body string) *httptest.ResponseRecorder {
	return e.do(http.MethodPost, "/api/cases/"+id+"/key/"+action, body)
}

func (e *mkEnv) keyStatus(id string) caseKeyDTO {
	e.t.Helper()
	res := e.do(http.MethodGet, "/api/cases/"+id+"/key", "")
	if res.Code != http.StatusOK {
		e.t.Fatalf("key status = %d %s", res.Code, res.Body)
	}
	var out caseKeyDTO
	decodeBody(e.t, res, &out)
	return out
}

func (e *mkEnv) wantKeyFail(res *httptest.ResponseRecorder, code int, reason string) caseKeyFailure {
	e.t.Helper()
	if res.Code != code {
		e.t.Fatalf("status = %d %s, want %d %s", res.Code, res.Body, code, reason)
	}
	var f caseKeyFailure
	decodeBody(e.t, res, &f)
	if f.Reason != reason {
		e.t.Fatalf("reason = %q (%s), want %q", f.Reason, res.Body, reason)
	}
	return f
}

func TestCaseKeyCreateLockUnlock(t *testing.T) {
	e := newCKEnv(t)
	plain := e.newCase("plain")
	c := e.newKeyedCase("keyed")
	if !c.OwnKey || c.KeyLocked || len(c.KeyEnding) != 4 {
		t.Fatalf("created = %+v", c)
	}
	if st := e.keyStatus(c.ID); !st.OwnKey || st.Locked || st.Ending != c.KeyEnding {
		t.Fatalf("status = %+v", st)
	}
	if st := e.keyStatus(plain); st.OwnKey || st.Locked || st.Ending != "" {
		t.Fatalf("plain status = %+v", st)
	}
	if res := e.do(http.MethodGet, "/api/cases/"+c.ID, ""); res.Code != http.StatusOK {
		t.Fatalf("open unlocked = %d %s", res.Code, res.Body)
	}

	// Lock this case: it alone.
	res := e.keyCall(c.ID, "lock", `{}`)
	if res.Code != http.StatusOK {
		t.Fatalf("lock = %d %s", res.Code, res.Body)
	}
	e.wantKeyFail(e.do(http.MethodGet, "/api/cases/"+c.ID, ""), http.StatusLocked, "case-locked")
	e.wantKeyFail(e.do(http.MethodGet, "/api/cases/"+c.ID+"/imports", ""), http.StatusLocked, "case-locked")
	if res := e.do(http.MethodGet, "/api/cases/"+plain, ""); res.Code != http.StatusOK {
		t.Fatalf("other case = %d %s", res.Code, res.Body)
	}
	// The list still names it, by code, with the key marks and no title.
	list := e.listCases()
	var got caseDTO
	for _, d := range list {
		if d.ID == c.ID {
			got = d
		}
	}
	if !got.OwnKey || !got.KeyLocked || got.Title == "keyed" || got.KeyEnding != c.KeyEnding {
		t.Fatalf("locked row = %+v", got)
	}

	// Wrong key: 423 wrong-case-key, still locked. Right key: opens.
	e.wantKeyFail(e.keyCall(c.ID, "unlock", unlockBody("not the key at all")), http.StatusLocked, "wrong-case-key")
	e.wantKeyFail(e.do(http.MethodGet, "/api/cases/"+c.ID, ""), http.StatusLocked, "case-locked")
	if res := e.keyCall(c.ID, "unlock", unlockBody(ckPass)); res.Code != http.StatusOK {
		t.Fatalf("unlock = %d %s", res.Code, res.Body)
	}
	if res := e.do(http.MethodGet, "/api/cases/"+c.ID, ""); res.Code != http.StatusOK {
		t.Fatalf("open after unlock = %d %s", res.Code, res.Body)
	}
	// Locking a case with no key of its own is a conflict, not a silent success.
	e.wantKeyFail(e.keyCall(plain, "lock", `{}`), http.StatusConflict, "no-case-key")
	e.wantKeyFail(e.keyCall(plain, "unlock", unlockBody(ckPass)), http.StatusConflict, "no-case-key")
}

func (e *mkEnv) listCases() []caseDTO {
	e.t.Helper()
	res := e.do(http.MethodGet, "/api/cases", "")
	if res.Code != http.StatusOK {
		e.t.Fatalf("list = %d %s", res.Code, res.Body)
	}
	var out struct {
		Cases []caseDTO `json:"cases"`
	}
	decodeBody(e.t, res, &out)
	return out.Cases
}

func TestCaseKeyCreateRules(t *testing.T) {
	e := newCKEnv(t)
	before := len(e.listCases())
	e.wantKeyFail(e.do(http.MethodPost, "/api/cases", ckCreateBody("t", "short", "short")), http.StatusBadRequest, "too-short")
	e.wantKeyFail(e.do(http.MethodPost, "/api/cases", ckCreateBody("t", ckPass, ckPass2)), http.StatusBadRequest, "mismatch")
	e.wantKeyFail(e.do(http.MethodPost, "/api/cases", `{"title":"t","caseKey":`+q(ckPass)+`}`), http.StatusBadRequest, "mismatch")
	e.wantKeyFail(e.do(http.MethodPost, "/api/cases", `{"title":"t","caseKeyConfirm":`+q(ckPass)+`}`), http.StatusBadRequest, "bad-request")
	e.wantKeyFail(e.do(http.MethodPost, "/api/cases", `{"title":"t","caseKey":5}`), http.StatusBadRequest, "bad-request")
	e.wantKeyFail(e.do(http.MethodPost, "/api/cases", `{"title":"t","caseKey":`+q(strings.Repeat("x", 2000))+`,"caseKeyConfirm":`+q(strings.Repeat("x", 2000))+`}`), http.StatusBadRequest, "too-long")
	if n := len(e.listCases()); n != before {
		t.Fatalf("a refused create made a case: %d -> %d", before, n)
	}
	// A repeat of one request id answers the case already made and keeps its key.
	body := `{"title":"t","requestId":"abcdefgh12345678","caseKey":` + q(ckPass) + `,"caseKeyConfirm":` + q(ckPass) + `}`
	first := e.do(http.MethodPost, "/api/cases", body)
	second := e.do(http.MethodPost, "/api/cases", body)
	if first.Code != http.StatusCreated || second.Code != http.StatusOK {
		t.Fatalf("repeat = %d, %d: %s / %s", first.Code, second.Code, first.Body, second.Body)
	}
	// A plain create still works with no key.
	if res := e.do(http.MethodPost, "/api/cases", `{"title":"plain"}`); res.Code != http.StatusCreated {
		t.Fatalf("plain create = %d %s", res.Code, res.Body)
	}
}

func TestCaseKeyThrottle(t *testing.T) {
	e := newCKEnv(t)
	c := e.newKeyedCase("keyed")
	h := e.h.(*handler)
	now := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	h.keyTh.now = func() time.Time { return now }
	e.keyCall(c.ID, "lock", `{}`)
	for i := 0; i < 5; i++ {
		e.wantKeyFail(e.keyCall(c.ID, "unlock", unlockBody("wrong key number "+string(rune('a'+i)))), http.StatusLocked, "wrong-case-key")
	}
	// The sixth try is refused without being judged, even with the right key.
	f := e.wantKeyFail(e.keyCall(c.ID, "unlock", unlockBody(ckPass)), http.StatusTooManyRequests, "throttled")
	if f.RetryAfterSeconds < 1 {
		t.Fatalf("no retry-after: %+v", f)
	}
	e.wantKeyFail(e.do(http.MethodGet, "/api/cases/"+c.ID, ""), http.StatusLocked, "case-locked")
	// Another case is not affected by this one's wrong tries.
	other := e.newKeyedCase("other")
	e.keyCall(other.ID, "lock", `{}`)
	if res := e.keyCall(other.ID, "unlock", unlockBody(ckPass)); res.Code != http.StatusOK {
		t.Fatalf("other case unlock = %d %s", res.Code, res.Body)
	}
	now = now.Add(2 * time.Second)
	if res := e.keyCall(c.ID, "unlock", unlockBody(ckPass)); res.Code != http.StatusOK {
		t.Fatalf("unlock after the wait = %d %s", res.Code, res.Body)
	}
}

func TestCaseKeyChangeAndRemove(t *testing.T) {
	e := newCKEnv(t)
	c := e.newKeyedCase("keyed")
	change := func(cur, next, confirm string) *httptest.ResponseRecorder {
		return e.keyCall(c.ID, "change", `{"passphrase":`+q(cur)+`,"newPassphrase":`+q(next)+`,"confirm":`+q(confirm)+`}`)
	}
	e.wantKeyFail(change("wrong current key", ckPass2, ckPass2), http.StatusLocked, "wrong-case-key")
	e.wantKeyFail(change(ckPass, ckPass2, "different one here"), http.StatusBadRequest, "mismatch")
	e.wantKeyFail(change(ckPass, "short", "short"), http.StatusBadRequest, "too-short")
	res := change(ckPass, ckPass2, ckPass2)
	if res.Code != http.StatusOK {
		t.Fatalf("change = %d %s", res.Code, res.Body)
	}
	e.keyCall(c.ID, "lock", `{}`)
	e.wantKeyFail(e.keyCall(c.ID, "unlock", unlockBody(ckPass)), http.StatusLocked, "wrong-case-key")
	if res := e.keyCall(c.ID, "unlock", unlockBody(ckPass2)); res.Code != http.StatusOK {
		t.Fatalf("unlock with the new key = %d %s", res.Code, res.Body)
	}
	// Remove: the wrong key changes nothing, the right one returns the case to the PC key.
	e.wantKeyFail(e.keyCall(c.ID, "remove", unlockBody(ckPass)), http.StatusLocked, "wrong-case-key")
	if st := e.keyStatus(c.ID); !st.OwnKey {
		t.Fatalf("a wrong key removed the key: %+v", st)
	}
	res = e.keyCall(c.ID, "remove", unlockBody(ckPass2))
	if res.Code != http.StatusOK {
		t.Fatalf("remove = %d %s", res.Code, res.Body)
	}
	if st := e.keyStatus(c.ID); st.OwnKey || st.Locked {
		t.Fatalf("after remove: %+v", st)
	}
	if res := e.do(http.MethodGet, "/api/cases/"+c.ID, ""); res.Code != http.StatusOK {
		t.Fatalf("open after remove = %d %s", res.Code, res.Body)
	}
	// And a key can be set again, from outside the wizard.
	e.wantKeyFail(e.keyCall(c.ID, "set", `{"passphrase":`+q(ckPass)+`,"confirm":"nope nope nope nope"}`), http.StatusBadRequest, "mismatch")
	if res := e.keyCall(c.ID, "set", `{"passphrase":`+q(ckPass)+`,"confirm":`+q(ckPass)+`}`); res.Code != http.StatusOK {
		t.Fatalf("set = %d %s", res.Code, res.Body)
	}
	if st := e.keyStatus(c.ID); !st.OwnKey || st.Locked || len(st.Ending) != 4 {
		t.Fatalf("after set: %+v", st)
	}
	e.wantKeyFail(e.keyCall(c.ID, "set", `{"passphrase":`+q(ckPass2)+`,"confirm":`+q(ckPass2)+`}`), http.StatusConflict, "has-case-key")
}

func TestCaseKeyMasterLockAlsoLocksCaseKeys(t *testing.T) {
	e := newCKEnv(t)
	if res := e.do(http.MethodPost, "/api/master-key/setup", setupBody(mkPass, mkPass, true)); res.Code != http.StatusOK {
		t.Fatalf("setup = %d %s", res.Code, res.Body)
	}
	c := e.newKeyedCase("keyed")
	plain := e.newCase("plain")
	if res := e.do(http.MethodPost, "/api/master-key/lock", ""); res.Code != http.StatusOK {
		t.Fatalf("master lock = %d %s", res.Code, res.Body)
	}
	// Master locked: every case route is the app lock (reason "locked"), the keyed
	// case's key routes included.
	e.wantFail(e.do(http.MethodGet, "/api/cases/"+c.ID, ""), http.StatusLocked, "locked")
	e.wantFail(e.keyCall(c.ID, "unlock", unlockBody(ckPass)), http.StatusLocked, "locked")
	if res := e.do(http.MethodPost, "/api/master-key/unlock", unlockBody(mkPass)); res.Code != http.StatusOK {
		t.Fatalf("master unlock = %d %s", res.Code, res.Body)
	}
	// The master key is back; the case's own key is not.
	e.wantKeyFail(e.do(http.MethodGet, "/api/cases/"+c.ID, ""), http.StatusLocked, "case-locked")
	if res := e.do(http.MethodGet, "/api/cases/"+plain, ""); res.Code != http.StatusOK {
		t.Fatalf("plain case after master unlock = %d %s", res.Code, res.Body)
	}
	if res := e.keyCall(c.ID, "unlock", unlockBody(ckPass)); res.Code != http.StatusOK {
		t.Fatalf("case unlock = %d %s", res.Code, res.Body)
	}
	if res := e.do(http.MethodGet, "/api/cases/"+c.ID, ""); res.Code != http.StatusOK {
		t.Fatalf("open = %d %s", res.Code, res.Body)
	}
}

func TestCaseKeyLeftAloneByRewrap(t *testing.T) {
	e := newCKEnv(t)
	c := e.newKeyedCase("keyed")
	old := e.newCase("old plain")
	if res := e.do(http.MethodPost, "/api/master-key/setup", setupBody(mkPass, mkPass, true)); res.Code != http.StatusOK {
		t.Fatalf("setup = %d %s", res.Code, res.Body)
	}
	res := e.do(http.MethodPost, "/api/master-key/rewrap", `{"all":true}`)
	if res.Code != http.StatusOK {
		t.Fatalf("rewrap = %d %s", res.Code, res.Body)
	}
	var out rewrapOut
	decodeBody(t, res, &out)
	status := map[string]string{}
	for _, r := range out.Results {
		status[r.CaseID] = r.Status
	}
	if status[c.ID] != "already" || status[old] != "rewrapped" {
		t.Fatalf("rewrap results = %+v", out.Results)
	}
	if k := e.kind(c.ID); k != casestore.KindCaseKey {
		t.Fatalf("keyed case kind after rewrap = %q", k)
	}
	if st := e.status(); st.DPAPICases != 0 {
		t.Fatalf("status counts the keyed case as DPAPI: %+v", st)
	}
}

func TestCaseKeyNeverInResponsesOrLogs(t *testing.T) {
	e := newCKEnv(t)
	c := e.newKeyedCase("keyed")
	e.keyCall(c.ID, "lock", `{}`)
	e.keyCall(c.ID, "unlock", unlockBody("a wrong key to leave in logs"))
	e.keyCall(c.ID, "unlock", unlockBody(ckPass))
	e.keyCall(c.ID, "change", `{"passphrase":`+q(ckPass)+`,"newPassphrase":`+q(ckPass2)+`,"confirm":`+q(ckPass2)+`}`)
	e.do(http.MethodPost, "/api/cases", ckCreateBody("again", ckPass, "bad confirmation x"))
	e.mu.Lock()
	bodies := append([]string(nil), e.bodies...)
	e.mu.Unlock()
	for _, secret := range []string{ckPass, ckPass2, "a wrong key to leave in logs", "bad confirmation x"} {
		for _, b := range bodies {
			if strings.Contains(b, secret) {
				t.Fatalf("a response holds a key: %q", b)
			}
		}
		if strings.Contains(e.logs.String(), secret) {
			t.Fatalf("the log holds a key: %q", e.logs.String())
		}
	}
	// And nothing under the case folder holds one in the clear.
	filepath.Walk(e.root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		data, _ := os.ReadFile(p)
		for _, secret := range []string{ckPass, ckPass2} {
			if bytes.Contains(data, []byte(secret)) {
				t.Errorf("%s holds a key in the clear", p)
			}
		}
		return nil
	})
}

func TestCaseKeyRoutesRefuseOtherShapes(t *testing.T) {
	e := newCKEnv(t)
	c := e.newKeyedCase("keyed")
	// A key in the query string or an unknown body field is refused.
	if res := e.keyCall(c.ID, "unlock", `{"passphrase":`+q(ckPass)+`,"extra":1}`); res.Code != http.StatusBadRequest {
		t.Fatalf("unknown field = %d", res.Code)
	}
	if res := e.do(http.MethodGet, "/api/cases/"+c.ID+"/key/unlock", ""); res.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET unlock = %d", res.Code)
	}
	if res := e.keyCall("not-a-case-id", "unlock", unlockBody(ckPass)); res.Code != http.StatusBadRequest {
		t.Fatalf("bad id = %d %s", res.Code, res.Body)
	}
	e.wantKeyFail(e.do(http.MethodGet, "/api/cases/abcdefghijklmnopqrstuvwxyz/key", ""), http.StatusNotFound, "case-not-found")
}

// A master key reset says first what it will do (M12) and leaves a case with its
// own key readable and uncounted.
func TestCaseKeyResetLeavesOwnKeyCasesAlone(t *testing.T) {
	e := newCKEnv(t)
	legacy := e.newCase("made before the key") // DPAPI-sealed
	if res := e.do(http.MethodPost, "/api/master-key/setup", setupBody(mkPass, mkPass, true)); res.Code != http.StatusOK {
		t.Fatalf("setup = %d %s", res.Code, res.Body)
	}
	mk1, mk2 := e.newCase("master one"), e.newCase("master two")
	keyed := e.newKeyedCase("own key")
	e.keyCall(keyed.ID, "lock", `{}`)
	e.do(http.MethodPost, "/api/master-key/lock", "")
	if err := os.WriteFile(masterkey.Path(e.dir), []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	// M12's N is kept in a note beside the key file (see archive_m12_test.go for the
	// real count); this test only checks that the reset impact carries it.
	if err := os.WriteFile(filepath.Join(e.dir, freedNoteName), []byte(`{"v":1,"n":3}`), 0o600); err != nil {
		t.Fatal(err)
	}

	// The status carries the impact before anything is changed.
	type statusOut struct {
		State string       `json:"state"`
		Reset *resetImpact `json:"reset"`
	}
	var st statusOut
	decodeBody(t, e.do(http.MethodGet, "/api/master-key/status", ""), &st)
	want := resetImpact{MasterKeyCases: 2, OwnKeyCases: 1, ArchiveOnly: 3}
	if st.State != "damaged" || st.Reset == nil || *st.Reset != want {
		t.Fatalf("status = %+v %+v, want %+v", st, st.Reset, want)
	}
	// Outside the damaged state the status holds no such block.
	res := e.do(http.MethodPost, "/api/master-key/reset-damaged", `{"acknowledged":true}`)
	if res.Code != http.StatusOK {
		t.Fatalf("reset = %d %s", res.Code, res.Body)
	}
	var out struct {
		State string       `json:"state"`
		Reset *resetImpact `json:"reset"`
	}
	decodeBody(t, res, &out)
	if out.State != "none" || out.Reset == nil || *out.Reset != want {
		t.Fatalf("reset reply = %+v %+v", out, out.Reset)
	}
	var after statusOut
	decodeBody(t, e.do(http.MethodGet, "/api/master-key/status", ""), &after)
	if after.Reset != nil {
		t.Fatalf("status after the reset still carries an impact: %+v", after.Reset)
	}

	// A new master key: the case with its own key still opens with its key and
	// is untouched; the cases sealed under the old key do not open.
	if res := e.do(http.MethodPost, "/api/master-key/setup", setupBody(mkPass2, mkPass2, true)); res.Code != http.StatusOK {
		t.Fatalf("setup after reset = %d %s", res.Code, res.Body)
	}
	if res := e.keyCall(keyed.ID, "unlock", unlockBody(ckPass)); res.Code != http.StatusOK {
		t.Fatalf("own-key case after the reset = %d %s", res.Code, res.Body)
	}
	if res := e.do(http.MethodGet, "/api/cases/"+keyed.ID, ""); res.Code != http.StatusOK {
		t.Fatalf("open own-key case = %d %s", res.Code, res.Body)
	}
	for _, id := range []string{mk1, mk2} {
		if res := e.do(http.MethodGet, "/api/cases/"+id, ""); res.Code == http.StatusOK {
			t.Fatalf("a case sealed under the old master key opened after the reset: %s", id)
		}
	}
	// The legacy (DPAPI) case is not part of the reset at all.
	if res := e.do(http.MethodGet, "/api/cases/"+legacy, ""); res.Code != http.StatusOK {
		t.Fatalf("legacy case after the reset = %d %s", res.Code, res.Body)
	}
	// The keyed case is still counted as its own, never as a master key case.
	impact := e.h.(*handler).masterKeyResetImpact(context.Background())
	if impact.OwnKeyCases != 1 || impact.MasterKeyCases != 2 {
		t.Fatalf("impact after the reset = %+v", impact)
	}
}
