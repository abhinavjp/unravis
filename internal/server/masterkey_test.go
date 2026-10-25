package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/casestore/sqlite"
	"api-audit-explorer/internal/casestore/storetest"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/masterkey"
)

const mkPass = "correct horse battery staple"

// mkEnv is a server over a real SQLite case store wired the way cmd wires it:
// one MasterKeySession feeds the store's Protectors and NewCaseKind and the
// server's master-key routes.
type mkEnv struct {
	t      *testing.T
	h      http.Handler
	sess   *MasterKeySession
	store  *sqlite.Store
	dir    string
	root   string
	mu     sync.Mutex
	bodies []string
	logs   *bytes.Buffer
}

func newMKEnv(t *testing.T) *mkEnv {
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
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	e := &mkEnv{t: t, sess: sess, store: store, dir: dir, root: root, logs: logs}
	e.h = New(&fakeRepository{}, testAssets(), testToken, false,
		WithCases(store, mustHosts(t, hostsDoc), importer.NewRegistry(toyImporter{name: "toy", score: 90})),
		WithMasterKey(sess),
	)
	return e
}

func (e *mkEnv) do(method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Host = "127.0.0.1:8765"
	req.Header.Set("X-Audit-Token", testToken)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	res := httptest.NewRecorder()
	e.h.ServeHTTP(res, req)
	e.mu.Lock()
	e.bodies = append(e.bodies, res.Body.String())
	e.mu.Unlock()
	return res
}

// presetKey writes masterkey.dat directly (one Argon2 run) so a test can start
// in the locked state.
func (e *mkEnv) presetKey(pass string) {
	e.t.Helper()
	if err := masterkey.Setup(e.dir, []byte(pass)); err != nil {
		e.t.Fatal(err)
	}
}

func q(s string) string { return strconv.Quote(s) }

func setupBody(pass, confirm string, ack bool) string {
	return `{"passphrase":` + q(pass) + `,"confirm":` + q(confirm) + `,"acknowledged":` + strconv.FormatBool(ack) + `}`
}

func unlockBody(pass string) string { return `{"passphrase":` + q(pass) + `}` }

type mkState struct {
	State      string `json:"state"`
	DPAPICases int    `json:"dpapiCases"`
}

type mkFail struct {
	Error             string `json:"error"`
	Reason            string `json:"reason"`
	State             string `json:"state"`
	RetryAfterSeconds int    `json:"retryAfterSeconds"`
}

func (e *mkEnv) status() mkState {
	e.t.Helper()
	res := e.do(http.MethodGet, "/api/master-key/status", "")
	if res.Code != http.StatusOK {
		e.t.Fatalf("status = %d %s", res.Code, res.Body)
	}
	var s mkState
	decodeBody(e.t, res, &s)
	return s
}

func (e *mkEnv) wantFail(res *httptest.ResponseRecorder, code int, reason string) mkFail {
	e.t.Helper()
	if res.Code != code {
		e.t.Fatalf("status = %d %s, want %d %s", res.Code, res.Body, code, reason)
	}
	var f mkFail
	decodeBody(e.t, res, &f)
	if f.Reason != reason {
		e.t.Fatalf("reason = %q (%s), want %q", f.Reason, res.Body, reason)
	}
	return f
}

func (e *mkEnv) newCase(title string) string {
	e.t.Helper()
	res := e.do(http.MethodPost, "/api/cases", `{"title":`+q(title)+`}`)
	if res.Code != http.StatusCreated {
		e.t.Fatalf("create case = %d %s", res.Code, res.Body)
	}
	var out caseDTO
	decodeBody(e.t, res, &out)
	return out.ID
}

func (e *mkEnv) kind(id string) string {
	e.t.Helper()
	k, err := e.store.CaseKind(casestore.CaseID(id))
	if err != nil {
		e.t.Fatal(err)
	}
	return k
}

/* ----------------------------------------------------------- the states */

func TestMasterKeyStatusInEachState(t *testing.T) {
	e := newMKEnv(t)
	if s := e.status(); s.State != "none" || s.DPAPICases != 0 {
		t.Fatalf("fresh = %+v", s)
	}
	a, b := e.newCase("one"), e.newCase("two")
	if e.kind(a) != casestore.KindDPAPI || e.kind(b) != casestore.KindDPAPI {
		t.Fatal("cases made before a master key must be DPAPI")
	}
	if s := e.status(); s.State != "none" || s.DPAPICases != 2 {
		t.Fatalf("none with two cases = %+v", s)
	}

	res := e.do(http.MethodPost, "/api/master-key/setup", setupBody(mkPass, mkPass, true))
	if res.Code != http.StatusOK {
		t.Fatalf("setup = %d %s", res.Code, res.Body)
	}
	var s mkState
	decodeBody(t, res, &s)
	if s.State != "unlocked" || s.DPAPICases != 2 {
		t.Fatalf("setup answered %+v", s)
	}
	if got := e.status(); got != s {
		t.Fatalf("status after setup = %+v", got)
	}
	// A case made now is master-key sealed.
	if e.kind(e.newCase("three")) != casestore.KindMasterKey {
		t.Fatal("new case after setup is not master-key sealed")
	}

	res = e.do(http.MethodPost, "/api/master-key/lock", "")
	if res.Code != http.StatusOK {
		t.Fatalf("lock = %d %s", res.Code, res.Body)
	}
	decodeBody(t, res, &s)
	if s.State != "locked" || s.DPAPICases != 0 {
		t.Fatalf("lock answered %+v", s)
	}
	if got := e.status(); got.State != "locked" || got.DPAPICases != 0 {
		t.Fatalf("status locked = %+v", got)
	}
	// Lock is idempotent.
	if res := e.do(http.MethodPost, "/api/master-key/lock", ""); res.Code != http.StatusOK {
		t.Fatalf("second lock = %d %s", res.Code, res.Body)
	}

	// A restart: a new session over the same folder starts locked.
	if got := NewMasterKeySession(e.dir).State(); got != "locked" {
		t.Fatalf("restart state = %q", got)
	}
	if got := NewMasterKeySession(t.TempDir()).State(); got != "none" {
		t.Fatalf("empty dir state = %q", got)
	}
}

func TestLockWithNoKeyIsRefused(t *testing.T) {
	e := newMKEnv(t)
	e.wantFail(e.do(http.MethodPost, "/api/master-key/lock", ""), http.StatusConflict, "not-set")
	e.wantFail(e.do(http.MethodPost, "/api/master-key/unlock", unlockBody(mkPass)), http.StatusConflict, "not-set")
}

/* ---------------------------------------------------------------- setup */

func TestSetupRefusals(t *testing.T) {
	e := newMKEnv(t)
	post := func(body string) *httptest.ResponseRecorder {
		return e.do(http.MethodPost, "/api/master-key/setup", body)
	}

	e.wantFail(post(setupBody(mkPass, mkPass, false)), http.StatusBadRequest, "acknowledgement-required")
	e.wantFail(post(`{"passphrase":`+q(mkPass)+`,"confirm":`+q(mkPass)+`}`), http.StatusBadRequest, "acknowledgement-required")
	e.wantFail(post(setupBody(mkPass, mkPass+"x", true)), http.StatusBadRequest, "mismatch")
	e.wantFail(post(setupBody("elevenchars", "elevenchars", true)), http.StatusBadRequest, "too-short")
	if masterkey.Exists(e.dir) {
		t.Fatal("a refused setup wrote masterkey.dat")
	}
	if s := e.status(); s.State != "none" {
		t.Fatalf("state after refusals = %+v", s)
	}

	// Body shape errors do not count toward the throttle and never echo input.
	for name, tc := range map[string]struct {
		body, ctype string
		code        int
	}{
		"unknown field":    {`{"passphrase":"x","confirm":"x","acknowledged":true,"extra":1}`, "application/json", 400},
		"null body":        {`null`, "application/json", 400},
		"array body":       {`[]`, "application/json", 400},
		"trailing data":    {setupBody(mkPass, mkPass, true) + `{}`, "application/json", 400},
		"number pass":      {`{"passphrase":12345678901234,"confirm":"x","acknowledged":true}`, "application/json", 400},
		"string ack":       {`{"passphrase":"x","confirm":"x","acknowledged":"true"}`, "application/json", 400},
		"bad escape":       {`{"passphrase":"a\qb","confirm":"x","acknowledged":true}`, "application/json", 400},
		"wrong media type": {setupBody(mkPass, mkPass, true), "text/plain", 415},
		"too large":        {`{"passphrase":"` + strings.Repeat("a", maxSecretBody) + `"}`, "application/json", 413},
	} {
		req := httptest.NewRequest(http.MethodPost, "/api/master-key/setup", strings.NewReader(tc.body))
		req.Host = "127.0.0.1:8765"
		req.Header.Set("X-Audit-Token", testToken)
		req.Header.Set("Content-Type", tc.ctype)
		res := httptest.NewRecorder()
		e.h.ServeHTTP(res, req)
		e.bodies = append(e.bodies, res.Body.String())
		if res.Code != tc.code {
			t.Errorf("%s: status = %d %s, want %d", name, res.Code, res.Body, tc.code)
		}
	}
	if masterkey.Exists(e.dir) {
		t.Fatal("a refused setup wrote masterkey.dat")
	}

	// A good setup, then a second one is refused as already set.
	if res := post(setupBody(mkPass, mkPass, true)); res.Code != http.StatusOK {
		t.Fatalf("setup = %d %s", res.Code, res.Body)
	}
	e.wantFail(post(setupBody("another passphrase!", "another passphrase!", true)), http.StatusConflict, "already-set")
	if res := e.do(http.MethodPost, "/api/master-key/lock", ""); res.Code != 200 {
		t.Fatalf("lock = %d", res.Code)
	}
	if res := e.do(http.MethodPost, "/api/master-key/unlock", unlockBody(mkPass)); res.Code != 200 {
		t.Fatalf("original passphrase no longer unlocks: %d %s", res.Code, res.Body)
	}
}

func TestSetupThrottleCountsKeyWriteFailures(t *testing.T) {
	e := newMKEnv(t)
	now := time.Unix(1_000_000, 0)
	e.sess.setupTh.now = func() time.Time { return now }
	// Validation failures are not counted (F7), so drive the counter directly: it
	// is the failure of the key file write that counts.
	for i := 0; i < 5; i++ {
		e.sess.setupTh.fail()
	}
	f := e.wantFail(e.do(http.MethodPost, "/api/master-key/setup", setupBody(mkPass, mkPass, true)), http.StatusTooManyRequests, "throttled")
	if f.RetryAfterSeconds != 1 {
		t.Fatalf("retry = %d", f.RetryAfterSeconds)
	}
	if masterkey.Exists(e.dir) {
		t.Fatal("a throttled setup created the key")
	}
	now = now.Add(time.Second)
	if res := e.do(http.MethodPost, "/api/master-key/setup", setupBody(mkPass, mkPass, true)); res.Code != http.StatusOK {
		t.Fatalf("setup after the wait = %d %s", res.Code, res.Body)
	}
}

func TestConcurrentSetupCreatesOneKey(t *testing.T) {
	e := newMKEnv(t)
	var wg sync.WaitGroup
	codes := make([]int, 3)
	for i := range codes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i] = e.do(http.MethodPost, "/api/master-key/setup", setupBody(mkPass, mkPass, true)).Code
		}()
	}
	wg.Wait()
	ok, conflict := 0, 0
	for _, c := range codes {
		switch c {
		case http.StatusOK:
			ok++
		case http.StatusConflict:
			conflict++
		}
	}
	if ok != 1 || conflict != 2 {
		t.Fatalf("codes = %v, want one 200 and two 409", codes)
	}
	if s := e.status(); s.State != "unlocked" {
		t.Fatalf("state = %+v", s)
	}
}

/* --------------------------------------------------------------- unlock */

func TestUnlockRightAndWrongShapes(t *testing.T) {
	e := newMKEnv(t)
	e.presetKey(mkPass)
	if s := e.status(); s.State != "locked" {
		t.Fatalf("preset state = %+v", s)
	}
	// A fake clock that jumps past any throttle delay before each attempt, so
	// this test is about response shapes and not about the throttle.
	now := time.Unix(3_000_000, 0)
	e.sess.unlockTh.now = func() time.Time { return now }
	post := func(body string) *httptest.ResponseRecorder {
		now = now.Add(2 * time.Minute)
		return e.do(http.MethodPost, "/api/master-key/unlock", body)
	}

	// Every kind of wrong answer is the same status and the same bytes.
	var want string
	for name, body := range map[string]string{
		"wrong":     unlockBody("not the passphrase"),
		"empty":     unlockBody(""),
		"missing":   `{}`,
		"too long":  unlockBody(strings.Repeat("x", maxPassphraseBytes+1)),
		"near miss": unlockBody(mkPass + " "),
	} {
		res := post(body)
		if res.Code != http.StatusUnauthorized {
			t.Fatalf("%s: status = %d %s", name, res.Code, res.Body)
		}
		if want == "" {
			want = res.Body.String()
		} else if res.Body.String() != want {
			t.Fatalf("%s: body %q differs from %q", name, res.Body, want)
		}
	}
	f := e.wantFail(post(unlockBody("again wrong pass")), http.StatusUnauthorized, "wrong-key")
	if f.State != "locked" || f.Error == "" {
		t.Fatalf("wrong-key body = %+v", f)
	}
	// Unknown fields and the setup-only fields are refused on unlock.
	for _, body := range []string{
		`{"passphrase":"x","extra":1}`,
		`{"passphrase":"x","confirm":"x"}`,
		`{"passphrase":"x","acknowledged":true}`,
	} {
		if res := post(body); res.Code != http.StatusBadRequest {
			t.Fatalf("%s = %d, want 400", body, res.Code)
		}
	}
	if s := e.status(); s.State != "locked" {
		t.Fatalf("still locked expected, got %+v", s)
	}

	res := post(unlockBody(mkPass))
	if res.Code != http.StatusOK {
		t.Fatalf("right passphrase = %d %s", res.Code, res.Body)
	}
	var s mkState
	decodeBody(t, res, &s)
	if s.State != "unlocked" {
		t.Fatalf("unlock answered %+v", s)
	}
	// Unlocking an unlocked key answers success without hashing anything.
	if res := post(unlockBody("anything at all")); res.Code != http.StatusOK {
		t.Fatalf("unlock while unlocked = %d", res.Code)
	}
	ring, err := e.sess.Keyring()
	if err != nil || ring.Locked() {
		t.Fatalf("Keyring() = %v, %v", ring, err)
	}
}

func TestUnlockThrottleDoublesAndResets(t *testing.T) {
	e := newMKEnv(t)
	e.presetKey(mkPass)
	now := time.Unix(2_000_000, 0)
	e.sess.unlockTh.now = func() time.Time { return now }
	e.sess.setupTh.now = e.sess.unlockTh.now
	post := func(body string) *httptest.ResponseRecorder {
		return e.do(http.MethodPost, "/api/master-key/unlock", body)
	}

	// Five wrong answers (empty ones, to keep Argon2 out of the loop).
	for i := 1; i <= 5; i++ {
		e.wantFail(post(unlockBody("")), http.StatusUnauthorized, "wrong-key")
	}
	// The 6th attempt, even with the right passphrase, is refused and not evaluated.
	res := post(unlockBody(mkPass))
	f := e.wantFail(res, http.StatusTooManyRequests, "throttled")
	if f.RetryAfterSeconds != 1 || res.Header().Get("Retry-After") != "1" {
		t.Fatalf("first delay = %d / %q", f.RetryAfterSeconds, res.Header().Get("Retry-After"))
	}
	if s := e.status(); s.State != "locked" {
		t.Fatal("a throttled attempt unlocked")
	}
	now = now.Add(500 * time.Millisecond)
	e.wantFail(post(unlockBody(mkPass)), http.StatusTooManyRequests, "throttled") // still delayed
	// The delay doubles with each further failure, up to 60 s.
	now = now.Add(500 * time.Millisecond)
	for _, want := range []int{2, 4, 8, 16, 32, 60, 60} {
		e.wantFail(post(unlockBody("")), http.StatusUnauthorized, "wrong-key")
		f := e.wantFail(post(unlockBody("")), http.StatusTooManyRequests, "throttled")
		if f.RetryAfterSeconds != want {
			t.Fatalf("delay = %d, want %d", f.RetryAfterSeconds, want)
		}
		now = now.Add(time.Duration(want) * time.Second)
	}
	// Setup has its own counter.
	if w := e.sess.setupTh.wait(); w != 0 {
		t.Fatalf("setup throttle moved by unlock failures: %v", w)
	}
	// The right passphrase after the wait succeeds and resets the count.
	if res := post(unlockBody(mkPass)); res.Code != http.StatusOK {
		t.Fatalf("right passphrase after the wait = %d %s", res.Code, res.Body)
	}
	if res := e.do(http.MethodPost, "/api/master-key/lock", ""); res.Code != http.StatusOK {
		t.Fatal("lock failed")
	}
	e.wantFail(post(unlockBody("")), http.StatusUnauthorized, "wrong-key") // not throttled: success reset it
}

func TestPassphraseEncodingsUnlockTheSameKey(t *testing.T) {
	e := newMKEnv(t)
	// "pässword ☃ 😀 \"q\" \\" typed with \u escapes and a surrogate pair.
	escaped := `pässword ☃ 😀 \"q\" \\ padding`
	plain := "pässword ☃ 😀 \"q\" \\ padding"
	body := `{"passphrase":"` + escaped + `","confirm":` + q(plain) + `,"acknowledged":true}`
	if res := e.do(http.MethodPost, "/api/master-key/setup", body); res.Code != http.StatusOK {
		t.Fatalf("setup with escapes = %d %s", res.Code, res.Body)
	}
	e.do(http.MethodPost, "/api/master-key/lock", "")
	// The decomposed (NFD) spelling is the same passphrase.
	nfd := "pässword ☃ 😀 \"q\" \\ padding"
	if res := e.do(http.MethodPost, "/api/master-key/unlock", unlockBody(nfd)); res.Code != http.StatusOK {
		t.Fatalf("unlock with NFD spelling = %d %s", res.Code, res.Body)
	}
}

func TestUnquoteSecret(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
		ok   bool
	}{
		{`"abc"`, "abc", true},
		{`""`, "", true},
		{`"a\"b\\c\/d"`, `a"b\c/d`, true},
		{`"\b\f\n\r\t"`, "\b\f\n\r\t", true},
		{`"äÄ"`, "äÄ", true},
		{`"😀"`, "😀", true},
		{`"\ud83d"`, "�", true},             // lone high surrogate
		{`"\ude00x"`, "�x", true},           // lone low surrogate
		{`"\ud83dA"`, "�A", true},           // high surrogate then a non-surrogate
		{`"\u12"`, "", false},               // short escape
		{`"\u12G4"`, "", false},             // bad hex digit
		{`"\x"`, "", false},                 // unknown escape
		{`"abc\`, "", false},                // not a closed string
		{`abc`, "", false},                  // not a string
		{`12`, "", false},                   // number
		{`"` + "\xff\xfe" + `"`, "", false}, // invalid UTF-8
	} {
		got, ok := unquoteSecret([]byte(tc.in))
		if ok != tc.ok || (ok && string(got) != tc.want) {
			t.Errorf("unquoteSecret(%q) = %q, %v; want %q, %v", tc.in, got, ok, tc.want, tc.ok)
			continue
		}
		// It must agree with encoding/json wherever json accepts the input.
		if tc.ok {
			var viaJSON string
			if err := json.Unmarshal([]byte(tc.in), &viaJSON); err == nil && viaJSON != string(got) {
				t.Errorf("unquoteSecret(%q) = %q, encoding/json says %q", tc.in, got, viaJSON)
			}
		}
	}
}

func TestBodyBuffersAreZeroed(t *testing.T) {
	b := &secretBody{raw: []byte(`{"passphrase":"secret"}`), passphrase: []byte("secret"), confirm: []byte("secret")}
	b.fields = map[string]json.RawMessage{"passphrase": json.RawMessage(`"secret"`)}
	raw, pass, conf, field := b.raw, b.passphrase, b.confirm, b.fields["passphrase"]
	b.wipe()
	for name, buf := range map[string][]byte{"raw": raw, "passphrase": pass, "confirm": conf, "field": field} {
		if len(bytes.Trim(buf, "\x00")) != 0 {
			t.Errorf("%s still holds %q after wipe", name, buf)
		}
	}
}

/* ------------------------------------------------------------- the gate */

// touchy is a case store that fails the test the moment anything calls it.
type touchy struct{ casestore.Store }

func TestLockedGateCoversEveryCaseRoute(t *testing.T) {
	dir := t.TempDir()
	if err := masterkey.Setup(dir, []byte(mkPass)); err != nil {
		t.Fatal(err)
	}
	sess := NewMasterKeySession(dir)
	if sess.State() != "locked" {
		t.Fatal("not locked")
	}
	hostsMap := mustHosts(t, hostsDoc)
	withStore := New(&fakeRepository{}, testAssets(), testToken, false,
		WithCases(touchy{}, hostsMap, importer.NewRegistry(toyImporter{name: "toy", score: 90})), WithMasterKey(sess))
	withoutStore := New(&fakeRepository{}, testAssets(), testToken, false, WithMasterKey(sess))

	gated, other := 0, 0
	for _, c := range routeCases {
		rt, _, ok := lookup(c.path)
		if !ok || rt.pattern != c.pattern {
			t.Fatalf("no route for %s", c.path)
		}
		public := rt.public
		isCase := gatedByMasterKey(rt)
		if isCase != (c.pattern == "/api/cases" || strings.HasPrefix(c.pattern, "/api/cases/")) {
			t.Errorf("%s: gatedByMasterKey = %v", c.pattern, isCase)
		}
		for _, method := range routeTestMethods {
			if !hasMethod(c.allow, method) {
				continue
			}
			if isCase {
				gated++
				func() {
					defer func() {
						if p := recover(); p != nil {
							t.Errorf("%s %s touched the case store while locked", method, c.path)
						}
					}()
					res := request(withStore, method, c.path, "", true)
					if res.Code != http.StatusLocked {
						t.Errorf("%s %s = %d %s, want 423", method, c.path, res.Code, res.Body)
						return
					}
					var f caseFailure
					decodeBody(t, res, &f)
					if f.Error != "locked" || f.Reason != "locked" {
						t.Errorf("%s %s body = %s", method, c.path, res.Body)
					}
				}()
				// 401 still comes first for a caller without the token.
				if res := request(withStore, method, c.path, "", false); res.Code != http.StatusUnauthorized {
					t.Errorf("%s %s without token = %d, want 401", method, c.path, res.Code)
				}
				continue
			}
			if public {
				continue
			}
			other++
			if res := request(withoutStore, method, c.path, "", true); res.Code == http.StatusLocked {
				t.Errorf("%s %s answered 423 but is not a case route", method, c.path)
			}
		}
	}
	if gated < 15 || other < 20 {
		t.Fatalf("walked only %d case and %d other route methods", gated, other)
	}
	// A path under /api/cases that matches no route is still a 404, never a 423.
	if res := request(withStore, http.MethodGet, "/api/cases/c1/other", "", true); res.Code != http.StatusNotFound {
		t.Errorf("unknown case path = %d", res.Code)
	}
}

func TestCaseRoutesFollowTheKeyState(t *testing.T) {
	e := newMKEnv(t)
	// state none: as before.
	dp := e.newCase("before the key")
	if res := e.do(http.MethodGet, "/api/cases", ""); res.Code != http.StatusOK {
		t.Fatalf("list in none = %d", res.Code)
	}

	if res := e.do(http.MethodPost, "/api/master-key/setup", setupBody(mkPass, mkPass, true)); res.Code != 200 {
		t.Fatalf("setup = %d %s", res.Code, res.Body)
	}
	mk := e.newCase("after the key")
	for _, id := range []string{dp, mk} {
		if res := e.do(http.MethodGet, "/api/cases/"+id, ""); res.Code != http.StatusOK {
			t.Fatalf("get %s unlocked = %d %s", id, res.Code, res.Body)
		}
	}

	if res := e.do(http.MethodPost, "/api/master-key/lock", ""); res.Code != 200 {
		t.Fatalf("lock = %d", res.Code)
	}
	for _, path := range []string{"/api/cases", "/api/cases/" + mk, "/api/cases/" + mk + "/imports", "/api/cases/" + dp + "/events"} {
		if res := e.do(http.MethodGet, path, ""); res.Code != http.StatusLocked {
			t.Fatalf("GET %s locked = %d %s", path, res.Code, res.Body)
		}
	}
	if res := e.do(http.MethodPost, "/api/cases", `{"title":"while locked"}`); res.Code != http.StatusLocked {
		t.Fatalf("create while locked = %d", res.Code)
	}

	e.wantFail(e.do(http.MethodPost, "/api/master-key/unlock", unlockBody("wrong pass here")), http.StatusUnauthorized, "wrong-key")
	if res := e.do(http.MethodGet, "/api/cases", ""); res.Code != http.StatusLocked {
		t.Fatalf("list after a wrong unlock = %d", res.Code)
	}
	if res := e.do(http.MethodPost, "/api/master-key/unlock", unlockBody(mkPass)); res.Code != 200 {
		t.Fatalf("unlock = %d %s", res.Code, res.Body)
	}
	res := e.do(http.MethodGet, "/api/cases", "")
	if res.Code != http.StatusOK {
		t.Fatalf("list after unlock = %d %s", res.Code, res.Body)
	}
	if !strings.Contains(res.Body.String(), "after the key") || strings.Contains(res.Body.String(), "(locked)") {
		t.Fatalf("list after unlock = %s", res.Body)
	}
}

func TestRacingCreateAndOpenGetLockedNotServerError(t *testing.T) {
	// A request that passed the gate and then found the key gone gets 423 from
	// the store error, not a 500.
	rec := httptest.NewRecorder()
	writeStoreError(rec, casestore.ErrLocked)
	if rec.Code != http.StatusLocked {
		t.Fatalf("writeStoreError(ErrLocked) = %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	writeStoreError(rec, errors.Join(errors.New("open"), casestore.ErrLocked))
	if rec.Code != http.StatusLocked {
		t.Fatalf("wrapped ErrLocked = %d", rec.Code)
	}
}

/* ------------------------------------------------- lock closes the handles */

func TestLockClosesOpenHandlesAndStopsJobs(t *testing.T) {
	e := newMKEnv(t)
	if res := e.do(http.MethodPost, "/api/master-key/setup", setupBody(mkPass, mkPass, true)); res.Code != 200 {
		t.Fatalf("setup = %d", res.Code)
	}
	id := e.newCase("held open")
	ctx := context.Background()
	held, err := e.store.Open(ctx, casestore.CaseID(id)) // a handle that outlives the request
	if err != nil {
		t.Fatal(err)
	}
	if _, err := held.Imports(ctx); err != nil {
		t.Fatalf("handle before lock: %v", err)
	}

	// A running import job is cancelled by lock.
	hh := e.h.(*handler)
	job := &importJob{id: "j1", caseID: casestore.CaseID(id), cancel: func() {}}
	canceled := false
	job.cancel = func() { canceled = true }
	hh.jobs.m = map[string]*importJob{"j1": job}

	if res := e.do(http.MethodPost, "/api/master-key/lock", ""); res.Code != 200 {
		t.Fatalf("lock = %d %s", res.Code, res.Body)
	}
	if !canceled || !job.canceled {
		t.Error("lock did not cancel the running import job")
	}
	if _, err := held.Imports(ctx); !errors.Is(err, casestore.ErrHandleClosed) {
		t.Fatalf("handle after lock = %v, want ErrHandleClosed", err)
	}
	if _, err := e.store.Open(ctx, casestore.CaseID(id)); !errors.Is(err, casestore.ErrLocked) {
		t.Fatalf("Open after lock = %v, want ErrLocked", err)
	}
	if _, err := e.sess.Keyring(); !errors.Is(err, casestore.ErrLocked) {
		t.Fatalf("Keyring() after lock = %v", err)
	}
	if _, err := e.sess.Lookup(casestore.KindMasterKey); !errors.Is(err, casestore.ErrLocked) {
		t.Fatalf("Lookup after lock = %v", err)
	}

	// Unlocking opens a fresh handle with the same data.
	if res := e.do(http.MethodPost, "/api/master-key/unlock", unlockBody(mkPass)); res.Code != 200 {
		t.Fatalf("unlock = %d", res.Code)
	}
	c, err := e.store.Open(ctx, casestore.CaseID(id))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Imports(ctx); err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
}

func TestSessionLookupAndNewCaseKind(t *testing.T) {
	dir := t.TempDir()
	s := NewMasterKeySession(dir)
	if k := s.NewCaseKind(); k != "" {
		t.Fatalf("NewCaseKind with no key = %q", k)
	}
	if _, err := s.Lookup(casestore.KindMasterKey); !errors.Is(err, casestore.ErrLocked) {
		t.Fatalf("Lookup with no key = %v", err)
	}
	if err := masterkey.Setup(dir, []byte(mkPass)); err != nil {
		t.Fatal(err)
	}
	// Locked with a key file: new cases must fail closed, not fall back to DPAPI.
	if k := s.NewCaseKind(); k != casestore.KindMasterKey {
		t.Fatalf("NewCaseKind locked = %q", k)
	}
	if _, err := s.Lookup(casestore.KindDPAPI); err == nil {
		t.Fatal("Lookup answered for the DPAPI kind")
	}
}

/* ----------------------------------------------------------------- rewrap */

type rewrapOut struct {
	Results []rewrapResult `json:"results"`
}

func TestRewrapRoute(t *testing.T) {
	e := newMKEnv(t)
	a, b := e.newCase("alpha"), e.newCase("beta")
	post := func(body string) *httptest.ResponseRecorder {
		return e.do(http.MethodPost, "/api/master-key/rewrap", body)
	}

	e.wantFail(post(`{"all":true}`), http.StatusConflict, "no-master-key")
	if res := e.do(http.MethodPost, "/api/master-key/setup", setupBody(mkPass, mkPass, true)); res.Code != 200 {
		t.Fatalf("setup = %d", res.Code)
	}

	for _, body := range []string{`{}`, `{"caseId":"` + a + `","all":true}`, `{"all":false}`, `{"all":true,"x":1}`} {
		if res := post(body); res.Code != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", body, res.Code)
		}
	}
	e.wantFail(post(`{"caseId":"not an id"}`), http.StatusBadRequest, "bad-case-id")

	var out rewrapOut
	res := post(`{"caseId":"` + a + `"}`)
	if res.Code != 200 {
		t.Fatalf("rewrap one = %d %s", res.Code, res.Body)
	}
	decodeBody(t, res, &out)
	if len(out.Results) != 1 || out.Results[0] != (rewrapResult{CaseID: a, Status: "rewrapped"}) {
		t.Fatalf("rewrap one = %+v", out)
	}
	if e.kind(a) != casestore.KindMasterKey || e.kind(b) != casestore.KindDPAPI {
		t.Fatal("kinds after rewrapping one case")
	}
	if s := e.status(); s.DPAPICases != 1 {
		t.Fatalf("status = %+v", s)
	}

	res = post(`{"all":true}`)
	decodeBody(t, res, &out)
	byID := map[string]rewrapResult{}
	for _, r := range out.Results {
		byID[r.CaseID] = r
	}
	if len(out.Results) != 2 || byID[a].Status != "already" || byID[b].Status != "rewrapped" {
		t.Fatalf("rewrap all = %+v", out)
	}
	if s := e.status(); s.DPAPICases != 0 {
		t.Fatalf("status after all = %+v", s)
	}

	// An id that is well formed but absent is a failed row, not a failed request.
	res = post(`{"caseId":"` + strings.Repeat("a", len(a)) + `"}`)
	if res.Code != 200 {
		t.Fatalf("absent id = %d %s", res.Code, res.Body)
	}
	decodeBody(t, res, &out)
	if len(out.Results) != 1 || out.Results[0].Status != "failed" || out.Results[0].Reason != "case-not-found" {
		t.Fatalf("absent id = %+v", out)
	}

	// Locked: 423, and nothing changes.
	e.do(http.MethodPost, "/api/master-key/lock", "")
	if res := post(`{"all":true}`); res.Code != http.StatusLocked {
		t.Fatalf("rewrap locked = %d", res.Code)
	}
	// After unlock both cases open: they now need only the master key.
	e.do(http.MethodPost, "/api/master-key/unlock", unlockBody(mkPass))
	list := e.do(http.MethodGet, "/api/cases", "")
	if !strings.Contains(list.Body.String(), "alpha") || !strings.Contains(list.Body.String(), "beta") {
		t.Fatalf("list = %s", list.Body)
	}
}

/* --------------------------------------------------- the rest of the rules */

func TestMasterKeyRoutesNeedTokenAndLocalOrigin(t *testing.T) {
	e := newMKEnv(t)
	for _, c := range routeCases {
		if !strings.HasPrefix(c.path, "/api/master-key/") {
			continue
		}
		method := http.MethodPost
		if c.allow == "GET" {
			method = http.MethodGet
		}
		body := unlockBody("a passphrase that must not matter")
		for _, mode := range []string{"no token", "wrong token", "foreign origin", "foreign host"} {
			req := httptest.NewRequest(method, c.path, strings.NewReader(body))
			req.Host = "127.0.0.1:8765"
			req.Header.Set("Content-Type", "application/json")
			want := http.StatusUnauthorized
			switch mode {
			case "wrong token":
				req.Header.Set("X-Audit-Token", "wrong-token")
			case "foreign origin":
				req.Header.Set("X-Audit-Token", testToken)
				req.Header.Set("Origin", "http://evil.example")
				want = http.StatusForbidden
			case "foreign host":
				req.Header.Set("X-Audit-Token", testToken)
				req.Host = "evil.example"
				want = http.StatusForbidden
			}
			res := httptest.NewRecorder()
			e.h.ServeHTTP(res, req)
			if res.Code != want {
				t.Errorf("%s %s (%s) = %d, want %d", method, c.path, mode, res.Code, want)
			}
		}
	}
	if masterkey.Exists(e.dir) {
		t.Fatal("a refused request changed the key")
	}
	// Wrong methods are refused with Allow.
	if res := e.do(http.MethodGet, "/api/master-key/unlock", ""); res.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET unlock = %d", res.Code)
	}
	if res := e.do(http.MethodPost, "/api/master-key/status", ""); res.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d", res.Code)
	}
}

func TestMasterKeyUnavailableWithoutSession(t *testing.T) {
	h := New(&fakeRepository{}, testAssets(), testToken, false,
		WithCases(casestore.NewMemory(), mustHosts(t, hostsDoc), importer.NewRegistry(toyImporter{name: "toy", score: 90})))
	res := request(h, http.MethodGet, "/api/master-key/status", "", true)
	var s mkState
	decodeBody(t, res, &s)
	if res.Code != 200 || s.State != "none" || s.DPAPICases != 0 {
		t.Fatalf("status = %d %+v", res.Code, s)
	}
	for _, path := range []string{"/api/master-key/setup", "/api/master-key/unlock", "/api/master-key/lock", "/api/master-key/rewrap"} {
		if res := request(h, http.MethodPost, path, unlockBody("x"), true); res.Code != http.StatusServiceUnavailable {
			t.Errorf("%s without a session = %d", path, res.Code)
		}
	}
	if res := request(h, http.MethodGet, "/api/cases", "", true); res.Code != 200 {
		t.Fatalf("cases without a session = %d", res.Code)
	}
}

func TestStatusRevealsOnlyStateAndCounts(t *testing.T) {
	e := newMKEnv(t)
	e.presetKey(mkPass)
	res := e.do(http.MethodGet, "/api/master-key/status", "")
	var m map[string]any
	decodeBody(t, res, &m)
	if len(m) != 3 || m["state"] != "locked" || m["dpapiCases"] != float64(0) || m["needsRewrap"] != float64(0) {
		t.Fatalf("status body = %s", res.Body)
	}
	if res.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q", res.Header().Get("Cache-Control"))
	}
}

func TestStressLockUnlockWhileServing(t *testing.T) {
	e := newMKEnv(t)
	if res := e.do(http.MethodPost, "/api/master-key/setup", setupBody(mkPass, mkPass, true)); res.Code != 200 {
		t.Fatalf("setup = %d", res.Code)
	}
	id := e.newCase("stress")
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				e.do(http.MethodGet, "/api/cases", "")
				e.do(http.MethodGet, "/api/cases/"+id+"/imports", "")
			}
		}()
	}
	for i := 0; i < 3; i++ {
		e.do(http.MethodPost, "/api/master-key/lock", "")
		e.do(http.MethodPost, "/api/master-key/unlock", unlockBody(mkPass))
	}
	e.do(http.MethodPost, "/api/master-key/lock", "")
	close(stop)
	wg.Wait()
	// The final state is locked and no handle is usable.
	if s := e.status(); s.State != "locked" {
		t.Fatalf("final state = %+v", s)
	}
	for _, path := range []string{"/api/cases", "/api/cases/" + id + "/imports"} {
		if res := e.do(http.MethodGet, path, ""); res.Code != http.StatusLocked {
			t.Fatalf("GET %s after the final lock = %d", path, res.Code)
		}
	}
	if _, err := e.store.Open(context.Background(), casestore.CaseID(id)); !errors.Is(err, casestore.ErrLocked) {
		t.Fatalf("Open after the final lock = %v", err)
	}
}

// TestNoPassphraseInResponsesLogsOrErrors runs the whole life cycle with a
// recognisable passphrase and scans every response and every log line.
func TestNoPassphraseInResponsesLogsOrErrors(t *testing.T) {
	e := newMKEnv(t)
	const secret = "zebra-quartz-hologram-9931"
	e.newCase("scan me")
	steps := []struct{ method, path, body string }{
		{"POST", "/api/master-key/setup", setupBody(secret, secret+"typo", true)},
		{"POST", "/api/master-key/setup", `{"passphrase":` + q(secret) + `,"confirm":` + q(secret) + `,"acknowledged":true,"bogus":1}`},
		{"POST", "/api/master-key/setup", `{"passphrase":` + q(secret) + `,"confirm":` + q(secret) + `,"acknowledged":true`},
		{"POST", "/api/master-key/setup", setupBody(secret, secret, true)},
		{"POST", "/api/master-key/setup", setupBody(secret, secret, true)},
		{"POST", "/api/master-key/lock", ""},
		{"POST", "/api/master-key/unlock", unlockBody(secret + "wrong")},
		{"POST", "/api/master-key/unlock", `{"passphrase":` + q(secret) + `,"confirm":1}`},
		{"POST", "/api/master-key/unlock", unlockBody(strings.Repeat(secret, 100))},
		{"GET", "/api/master-key/status", ""},
		{"GET", "/api/cases", ""},
		{"POST", "/api/master-key/unlock", unlockBody(secret)},
		{"POST", "/api/master-key/rewrap", `{"all":true}`},
		{"GET", "/api/cases", ""},
		{"POST", "/api/master-key/lock", ""},
	}
	for _, s := range steps {
		e.do(s.method, s.path, s.body)
	}
	for _, needle := range []string{secret, "hologram", secret + "typo", secret + "wrong"} {
		for i, body := range e.bodies {
			if strings.Contains(body, needle) {
				t.Errorf("response %d contains the passphrase: %s", i, body)
			}
		}
		if strings.Contains(e.logs.String(), needle) {
			t.Errorf("log contains the passphrase: %s", e.logs)
		}
	}
	// And nothing on disk but the verifier file mentions it.
	if data, err := os.ReadFile(masterkey.Path(e.dir)); err != nil || bytes.Contains(data, []byte(secret)) {
		t.Fatal("masterkey.dat contains the passphrase")
	}
}

func TestThrottleScheduleDirectly(t *testing.T) {
	now := time.Unix(0, 0)
	th := throttle{now: func() time.Time { return now }}
	for i := 0; i < 4; i++ {
		th.fail()
		if th.wait() != 0 {
			t.Fatalf("delayed after %d failures", i+1)
		}
	}
	var got []time.Duration
	for i := 0; i < 9; i++ {
		th.fail()
		got = append(got, th.wait())
		now = now.Add(th.wait())
	}
	want := []time.Duration{1, 2, 4, 8, 16, 32, 60, 60, 60}
	for i := range want {
		if got[i] != want[i]*time.Second {
			t.Fatalf("delays = %v", got)
		}
	}
	th.reset()
	if th.wait() != 0 {
		t.Fatal("reset left a delay")
	}
	th.fail()
	if th.wait() != 0 {
		t.Fatal("reset did not clear the count")
	}
}
