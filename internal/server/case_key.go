package server

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime"
	"net/http"
	"strconv"
	"sync"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/masterkey"
)

// The optional per-case key (decisions A12, A13). A case with its own key is
// opened with that key instead of the master key; locking, losing or changing it
// touches that case alone. Nothing here logs, returns or formats a key: every key
// field of a body is read as []byte that is wiped when the request ends.

const (
	caseLockedText  = "This case has its own key. Enter it to open the case."
	wrongCaseKeyTxt = "The case key is not right: nothing changed. Try again."
	noCaseKeyText   = "This case has no key of its own."
	hasCaseKeyText  = "This case already has a key of its own."
	caseKeyDownText = "Case keys are unavailable."
)

// caseKeyDTO is the state of one case's key: from file headers only, so it is
// the same while the case is locked.
type caseKeyDTO struct {
	OwnKey bool `json:"ownKey"`
	Locked bool `json:"locked"`
	// Ending is the last four characters of the key's check value ("Key ending
	// xxxx"). Empty when the case has no key of its own.
	Ending string `json:"ending"`
}

// caseKeyFailure is the body of a refused key request.
type caseKeyFailure struct {
	Error             string `json:"error"`
	Reason            string `json:"reason"`
	RetryAfterSeconds int    `json:"retryAfterSeconds,omitempty"`
}

// caseThrottles holds one failure throttle per case. They mirror the master
// key's: 5 wrong keys, then a wait that doubles from one second to a minute. The
// current-key checks of unlock, change and remove share a case's throttle, so a
// guesser cannot switch routes to get fresh tries.
type caseThrottles struct {
	mu  sync.Mutex
	m   map[casestore.CaseID]*throttle
	now func() time.Time // test seam
}

func (c *caseThrottles) get(id casestore.CaseID) *throttle {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[casestore.CaseID]*throttle{}
	}
	t := c.m[id]
	if t == nil {
		t = &throttle{now: c.now}
		c.m[id] = t
	}
	return t
}

func (c *caseThrottles) forget(id casestore.CaseID) {
	c.mu.Lock()
	delete(c.m, id)
	c.mu.Unlock()
}

func writeCaseKeyFail(w http.ResponseWriter, status int, reason, message string) {
	writeJSON(w, status, caseKeyFailure{Error: message, Reason: reason}, 4096)
}

// writeCaseLocked answers a request to a case whose own key is not unlocked. It
// is 423 like the master key's, with its own reason so the page shows the case's
// key prompt and not the lock screen of the whole app.
func writeCaseLocked(w http.ResponseWriter) {
	writeCaseKeyFail(w, http.StatusLocked, "case-locked", caseLockedText)
}

// writeWrongCaseKey answers a wrong case key. It is 401, as a wrong key is a failed
// credential; 423 stays for a locked case where no key was tried (writeCaseLocked).
func writeWrongCaseKey(w http.ResponseWriter) {
	writeCaseKeyFail(w, http.StatusUnauthorized, "wrong-case-key", wrongCaseKeyTxt)
}

func writeCaseKeyThrottled(w http.ResponseWriter, wait time.Duration) {
	secs := int((wait + time.Second - 1) / time.Second)
	if secs < 1 {
		secs = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	writeJSON(w, http.StatusTooManyRequests, caseKeyFailure{
		Error: "Too many attempts. Wait before trying again.", Reason: "throttled", RetryAfterSeconds: secs,
	}, 4096)
}

// writeCaseKeyError maps the per-case key errors of a store call; anything else
// goes to writeStoreError.
func writeCaseKeyError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, casestore.ErrCaseLocked):
		writeCaseLocked(w)
	case errors.Is(err, casestore.ErrWrongCaseKey):
		writeWrongCaseKey(w)
	case errors.Is(err, casestore.ErrNoCaseKey):
		writeCaseKeyFail(w, http.StatusConflict, "no-case-key", noCaseKeyText)
	case errors.Is(err, casestore.ErrHasCaseKey):
		writeCaseKeyFail(w, http.StatusConflict, "has-case-key", hasCaseKeyText)
	case errors.Is(err, casestore.ErrCaseKeyRule):
		writeCaseKeyRule(w, err)
	default:
		writeStoreError(w, err)
	}
}

// writeCaseKeyRule answers a new key that breaks the rules, by fixed reason.
func writeCaseKeyRule(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, masterkey.ErrInvalidChars):
		writeCaseKeyFail(w, http.StatusBadRequest, "invalid-characters", "The case key contains characters that cannot be used.")
	case errors.Is(err, masterkey.ErrBlank):
		writeCaseKeyFail(w, http.StatusBadRequest, "blank", "The case key needs more visible characters.")
	default:
		writeCaseKeyFail(w, http.StatusBadRequest, "too-short", "The case key is too short.")
	}
}

/* ------------------------------------------------------------ body parse */

// keyBody is a decoded request body whose named secret fields are []byte that
// never become Go strings. wipe zeroes everything it holds.
type keyBody struct {
	raw     []byte
	fields  map[string]json.RawMessage
	secrets map[string][]byte
	strs    map[string]string
}

func (b *keyBody) wipe() {
	if b == nil {
		return
	}
	clear(b.raw)
	for _, v := range b.fields {
		clear(v)
	}
	for _, v := range b.secrets {
		clear(v)
	}
}

func (b *keyBody) secret(name string) []byte { return b.secrets[name] }

// readKeyBody reads a small JSON object. Only the names in secrets (read as
// bytes) and strs (read as strings, for non-secret text such as a title) are
// allowed; anything else, or a field of the wrong type, is a 400 that echoes
// nothing. It answers the error itself and returns nil.
func readKeyBody(w http.ResponseWriter, r *http.Request, secrets, strs []string) *keyBody {
	bad := func(status int, reason, msg string) *keyBody {
		writeCaseKeyFail(w, status, reason, msg)
		return nil
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return bad(http.StatusUnsupportedMediaType, "bad-content-type", "Content-Type must be application/json.")
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxSecretBody)
	raw, err := io.ReadAll(r.Body)
	b := &keyBody{raw: raw, secrets: map[string][]byte{}, strs: map[string]string{}}
	if err != nil {
		b.wipe()
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return bad(http.StatusRequestEntityTooLarge, "too-large", "The request body is too large.")
		}
		return bad(http.StatusBadRequest, "bad-request", "The request body is invalid.")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		b.wipe()
		return bad(http.StatusBadRequest, "bad-request", "The request body is invalid.")
	}
	b.fields = fields
	isSecret := map[string]bool{}
	for _, n := range secrets {
		isSecret[n] = true
	}
	isStr := map[string]bool{}
	for _, n := range strs {
		isStr[n] = true
	}
	for name, v := range fields {
		switch {
		case isSecret[name]:
			s, ok := unquoteSecret(v)
			if !ok {
				b.wipe()
				return bad(http.StatusBadRequest, "bad-request", "The request body is invalid.")
			}
			b.secrets[name] = s
		case isStr[name]:
			var s string
			if err := json.Unmarshal(v, &s); err != nil {
				b.wipe()
				return bad(http.StatusBadRequest, "bad-request", "The request body is invalid.")
			}
			b.strs[name] = s
		default:
			b.wipe()
			return bad(http.StatusBadRequest, "bad-request", "The request body is invalid.")
		}
	}
	return b
}

// newKeyChecked judges a new case key and its confirmation. It answers the
// failure itself and returns false. The key is judged as the master key is: the
// same rules, and the confirmation compared in constant time.
func newKeyChecked(w http.ResponseWriter, key, confirm []byte) bool {
	a, c := masterkey.Normalize(key), masterkey.Normalize(confirm)
	same := len(a) == len(c) && subtle.ConstantTimeCompare(a, c) == 1
	rules := masterkey.CheckPassphrase(a)
	clear(a)
	clear(c)
	switch {
	case len(key) > maxPassphraseBytes:
		writeCaseKeyFail(w, http.StatusBadRequest, "too-long", "The case key is too long.")
	case !same:
		writeCaseKeyFail(w, http.StatusBadRequest, "mismatch", "The two entries do not match.")
	case rules != nil:
		writeCaseKeyRule(w, rules)
	default:
		return true
	}
	return false
}

/* -------------------------------------------------------------- handlers */

// caseKeyer returns the store's per-case key support, or answers 503.
func (h *handler) caseKeyer(w http.ResponseWriter) (casestore.CaseKeyer, bool) {
	if h.cases == nil {
		writeCaseFailure(w, http.StatusServiceUnavailable, "cases-unavailable", "Cases are unavailable.")
		return nil, false
	}
	ck, ok := h.cases.(casestore.CaseKeyer)
	if !ok {
		writeCaseKeyFail(w, http.StatusServiceUnavailable, "case-key-unavailable", caseKeyDownText)
		return nil, false
	}
	return ck, true
}

func caseKeyReply(w http.ResponseWriter, info casestore.CaseKeyInfo) {
	writeJSON(w, http.StatusOK, caseKeyDTO{OwnKey: info.Has, Locked: info.Has && info.Locked, Ending: info.Ending}, 4096)
}

// caseKeyStatus is GET /api/cases/{id}/key.
func (h *handler) caseKeyStatus(w http.ResponseWriter, r *http.Request, raw string) {
	id, ok := caseIDOf(w, raw)
	if !ok {
		return
	}
	ck, ok := h.caseKeyer(w)
	if !ok {
		return
	}
	info, err := ck.CaseKey(id)
	if err != nil && !info.Has {
		writeCaseKeyError(w, err)
		return
	}
	caseKeyReply(w, info)
}

// caseKeyJudged is the start of unlock, change and remove: the case exists, has a
// key, and the throttle lets a try through. It returns the throttle to score the
// try with. Callers hold h.keyOp.
func (h *handler) caseKeyJudged(w http.ResponseWriter, ck casestore.CaseKeyer, id casestore.CaseID, cur []byte) (*throttle, bool) {
	info, err := ck.CaseKey(id)
	if err != nil && !info.Has {
		writeCaseKeyError(w, err)
		return nil, false
	}
	if !info.Has {
		writeCaseKeyFail(w, http.StatusConflict, "no-case-key", noCaseKeyText)
		return nil, false
	}
	th := h.keyTh.get(id)
	if wait := th.wait(); wait > 0 {
		writeCaseKeyThrottled(w, wait)
		return nil, false
	}
	if len(cur) == 0 || len(cur) > maxPassphraseBytes {
		// Cannot be the key (setup refuses both): counted, no Argon2 run.
		th.fail()
		writeWrongCaseKey(w)
		return nil, false
	}
	return th, true
}

// caseKeyUnlock is POST /api/cases/{id}/key/unlock {passphrase}.
func (h *handler) caseKeyUnlock(w http.ResponseWriter, r *http.Request, raw string) {
	id, ok := caseIDOf(w, raw)
	if !ok {
		return
	}
	ck, ok := h.caseKeyer(w)
	if !ok {
		return
	}
	b := readKeyBody(w, r, []string{"passphrase"}, nil)
	if b == nil {
		return
	}
	defer b.wipe()
	h.keyOp.Lock()
	defer h.keyOp.Unlock()
	th, ok := h.caseKeyJudged(w, ck, id, b.secret("passphrase"))
	if !ok {
		return
	}
	pc := bytes.Clone(b.secret("passphrase"))
	defer clear(pc)
	err := ck.UnlockCase(r.Context(), id, pc)
	if errors.Is(err, casestore.ErrWrongCaseKey) {
		th.fail()
	}
	if err != nil {
		writeCaseKeyError(w, err)
		return
	}
	th.reset()
	// A mark left on the case (an erase that was cut short) is finished now that
	// its key is here.
	if h.recoverer != nil {
		if _, err := h.recoverer.RecoverOne(r.Context(), id); err != nil && !errors.Is(err, casestore.ErrNotFound) {
			log.Printf("Case recovery after a case unlock did not run for %s: %v", id, err)
		}
	}
	info, err := ck.CaseKey(id)
	if err != nil && !info.Has {
		// The unlock removed the key (a half-finished Remove was completed).
		caseKeyReply(w, casestore.CaseKeyInfo{})
		return
	}
	caseKeyReply(w, info)
}

// caseKeyLock is POST /api/cases/{id}/key/lock: forget this case's key, close its
// handle, stop what writes into it. Other cases are not touched.
func (h *handler) caseKeyLock(w http.ResponseWriter, r *http.Request, raw string) {
	id, ok := caseIDOf(w, raw)
	if !ok {
		return
	}
	ck, ok := h.caseKeyer(w)
	if !ok {
		return
	}
	info, err := ck.CaseKey(id)
	if err != nil && !info.Has {
		writeCaseKeyError(w, err)
		return
	}
	if !info.Has {
		writeCaseKeyFail(w, http.StatusConflict, "no-case-key", noCaseKeyText)
		return
	}
	h.stopSamplerFor(id, true, stopCaseClosed)
	stop := h.jobs.cancelCaseUntilStopped(id)
	stop()
	// A running archive or check reads with a copy of the key: it stops first, with the
	// key as its reason, so nothing writes from a key that was just forgotten.
	h.caseFiles().jobs.stopCase(id)
	if err := ck.LockCaseKey(id); err != nil {
		writeCaseKeyError(w, err)
		return
	}
	info.Locked = true
	caseKeyReply(w, info)
}

// caseKeySet is POST /api/cases/{id}/key/set {passphrase, confirm}.
func (h *handler) caseKeySet(w http.ResponseWriter, r *http.Request, raw string) {
	id, ok := caseIDOf(w, raw)
	if !ok {
		return
	}
	ck, ok := h.caseKeyer(w)
	if !ok {
		return
	}
	b := readKeyBody(w, r, []string{"passphrase", "confirm"}, nil)
	if b == nil {
		return
	}
	defer b.wipe()
	if !newKeyChecked(w, b.secret("passphrase"), b.secret("confirm")) {
		return
	}
	release, err := h.gates.acquire(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	defer release()
	h.keyOp.Lock()
	defer h.keyOp.Unlock()
	h.stopSamplerFor(id, true, stopCaseClosed)
	pc := bytes.Clone(b.secret("passphrase"))
	defer clear(pc)
	if err := ck.SetCaseKey(r.Context(), id, pc); err != nil {
		writeCaseKeyError(w, err)
		return
	}
	h.keyTh.forget(id)
	h.caseKeyAnswer(w, ck, id)
}

// caseKeyChange is POST /api/cases/{id}/key/change {passphrase, newPassphrase,
// confirm}: passphrase is the current key.
func (h *handler) caseKeyChange(w http.ResponseWriter, r *http.Request, raw string) {
	id, ok := caseIDOf(w, raw)
	if !ok {
		return
	}
	ck, ok := h.caseKeyer(w)
	if !ok {
		return
	}
	b := readKeyBody(w, r, []string{"passphrase", "newPassphrase", "confirm"}, nil)
	if b == nil {
		return
	}
	defer b.wipe()
	if !newKeyChecked(w, b.secret("newPassphrase"), b.secret("confirm")) {
		return
	}
	release, err := h.gates.acquire(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	defer release()
	h.keyOp.Lock()
	defer h.keyOp.Unlock()
	th, ok := h.caseKeyJudged(w, ck, id, b.secret("passphrase"))
	if !ok {
		return
	}
	// A key changed under a running archive makes the file it is writing stale at once.
	if err := h.refuseWhileArchiving(r.Context(), id); err != nil {
		writeStoreError(w, err)
		return
	}
	pc, pn := bytes.Clone(b.secret("passphrase")), bytes.Clone(b.secret("newPassphrase"))
	defer clear(pc)
	defer clear(pn)
	err = ck.ChangeCaseKey(r.Context(), id, pc, pn)
	if errors.Is(err, casestore.ErrWrongCaseKey) {
		th.fail()
	}
	if err != nil {
		writeCaseKeyError(w, err)
		return
	}
	th.reset()
	h.caseKeyAnswer(w, ck, id)
}

// caseKeyRemove is POST /api/cases/{id}/key/remove {passphrase}: the case goes
// back to the PC key.
func (h *handler) caseKeyRemove(w http.ResponseWriter, r *http.Request, raw string) {
	id, ok := caseIDOf(w, raw)
	if !ok {
		return
	}
	ck, ok := h.caseKeyer(w)
	if !ok {
		return
	}
	b := readKeyBody(w, r, []string{"passphrase"}, nil)
	if b == nil {
		return
	}
	defer b.wipe()
	release, err := h.gates.acquire(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	defer release()
	h.keyOp.Lock()
	defer h.keyOp.Unlock()
	th, ok := h.caseKeyJudged(w, ck, id, b.secret("passphrase"))
	if !ok {
		return
	}
	if err := h.refuseWhileArchiving(r.Context(), id); err != nil {
		writeStoreError(w, err)
		return
	}
	h.stopSamplerFor(id, true, stopCaseClosed)
	pc := bytes.Clone(b.secret("passphrase"))
	defer clear(pc)
	err = ck.RemoveCaseKey(r.Context(), id, pc)
	if errors.Is(err, casestore.ErrWrongCaseKey) {
		th.fail()
	}
	if err != nil {
		writeCaseKeyError(w, err)
		return
	}
	th.reset()
	h.caseKeyAnswer(w, ck, id)
}

func (h *handler) caseKeyAnswer(w http.ResponseWriter, ck casestore.CaseKeyer, id casestore.CaseID) {
	info, err := ck.CaseKey(id)
	if err != nil && !info.Has {
		// No key (Remove) is a normal answer; a missing case is not.
		if errors.Is(err, casestore.ErrNotFound) || errors.Is(err, casestore.ErrErasing) {
			writeCaseKeyError(w, err)
			return
		}
		caseKeyReply(w, casestore.CaseKeyInfo{})
		return
	}
	caseKeyReply(w, info)
}
