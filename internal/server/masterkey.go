package server

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/masterkey"
	"api-audit-explorer/internal/sealedfile"
)

// Master-key states, as the status route reports them.
const (
	MasterKeyNone     = "none"
	MasterKeyLocked   = "locked"
	MasterKeyUnlocked = "unlocked"
	// MasterKeyDamaged: something is at masterkey.dat that no passphrase can open
	// (empty, garbage, a folder, an unknown version). Cases are kept closed.
	MasterKeyDamaged = "damaged"
)

const (
	// maxSecretBody bounds the setup and unlock request bodies: two passphrases
	// and a flag. Far above any real passphrase, far below the 1 MiB default.
	maxSecretBody = 16 << 10
	// maxUnlockPassphrase is the longest passphrase unlock will hash. Anything
	// longer cannot be the right one (setup refuses it) and is answered as a
	// wrong key without running Argon2.
	maxPassphraseBytes = 1024

	throttleAfter    = 5
	throttleBaseWait = time.Second
	throttleMaxWait  = 60 * time.Second
)

/* -------------------------------------------------------------- session */

// MasterKeySession is the process-wide master-key state. It owns the one
// unlocked Keyring (nil while locked or never set up) and the failure
// throttles. The cmd package builds it once, hands Lookup and NewCaseKind to
// the case store, and hands the session itself to the server, so the store and
// the HTTP routes always see the same Keyring.
//
// The Keyring, the passphrase and every derived byte stay inside this type and
// masterkey; nothing here logs, returns or formats them.
type MasterKeySession struct {
	dir string

	mu   sync.RWMutex
	ring *masterkey.Keyring

	// op serializes setup, unlock and lock: only one Argon2 derivation (64 MiB)
	// runs at a time and a lock cannot interleave with an unlock.
	op       sync.Mutex
	unlockTh throttle
	setupTh  throttle

	// lgMu guards lgIDs: the legacy set (see masterkey.LegacySetFileName) as it
	// was loaded at the last setup or unlock. nil means not loaded or failed to
	// load, which refuses every Windows-sealed case; a non-nil map (possibly
	// empty) is the set. It is cleared on lock. The ids are not secret.
	lgMu  sync.Mutex
	lgIDs map[string]struct{}
}

// NewMasterKeySession returns a session whose key file lives in dir. It starts
// locked when masterkey.dat exists there and "none" otherwise.
func NewMasterKeySession(dir string) *MasterKeySession {
	return &MasterKeySession{dir: dir}
}

// State is none, locked or unlocked.
func (s *MasterKeySession) State() string {
	s.mu.RLock()
	r := s.ring
	s.mu.RUnlock()
	if r != nil && !r.Locked() {
		return MasterKeyUnlocked
	}
	switch masterkey.Inspect(s.dir) {
	case masterkey.FileUsable:
		return MasterKeyLocked
	case masterkey.FileDamaged:
		return MasterKeyDamaged
	}
	return MasterKeyNone
}

// Keyring returns the unlocked Keyring, or casestore.ErrLocked while locked
// and ErrLocked-wrapped "none" as well (there is no key to hand out). The
// caller must not Lock it and must not keep it past the request: lock zeroes it.
func (s *MasterKeySession) Keyring() (*masterkey.Keyring, error) {
	s.mu.RLock()
	r := s.ring
	s.mu.RUnlock()
	if r == nil || r.Locked() {
		return nil, casestore.ErrLocked
	}
	return r, nil
}

// Lookup is the casestore.ProtectorLookup for the master-key kind.
func (s *MasterKeySession) Lookup(kind string) (sealedfile.Protector, error) {
	if kind != casestore.KindMasterKey {
		return nil, casestore.ErrLocked
	}
	r, err := s.Keyring()
	if err != nil {
		return nil, err
	}
	return r, nil
}

// NewCaseKind picks the protector of a new case: the master key as soon as one
// is set up (even while locked, so Create then fails closed with ErrLocked
// instead of quietly writing a DPAPI case), DPAPI otherwise.
func (s *MasterKeySession) NewCaseKind() string {
	if s.State() == MasterKeyNone {
		return ""
	}
	return casestore.KindMasterKey
}

// LegacyCheck is sqlite.Options.LegacyCheck. With no master key every Windows
// sealed case is allowed (the behaviour before a master key). With one, a case
// is allowed only if its id is on the legacy set loaded at unlock; anything else
// is casestore.ErrUntrustedLegacy, also when the set is missing or damaged.
// While the key is locked the set cannot be consulted: casestore.ErrLocked.
func (s *MasterKeySession) LegacyCheck(id casestore.CaseID) error {
	switch s.State() {
	case MasterKeyNone:
		return nil
	case MasterKeyUnlocked:
	default:
		return casestore.ErrLocked
	}
	s.lgMu.Lock()
	defer s.lgMu.Unlock()
	if _, ok := s.lgIDs[string(id)]; ok {
		return nil
	}
	return casestore.ErrUntrustedLegacy
}

// LegacyDone is sqlite.Options.LegacyDone: id has been re-wrapped, so it leaves
// the legacy set (memory first, then the file). A failed write is harmless: the
// stale entry names a case that is now sealed by the master key.
func (s *MasterKeySession) LegacyDone(id casestore.CaseID) {
	s.lgMu.Lock()
	defer s.lgMu.Unlock()
	if _, ok := s.lgIDs[string(id)]; !ok {
		return
	}
	delete(s.lgIDs, string(id))
	ring, err := s.Keyring()
	if err != nil {
		return
	}
	ids := make([]string, 0, len(s.lgIDs))
	for k := range s.lgIDs {
		ids = append(ids, k)
	}
	_ = masterkey.WriteLegacySet(s.dir, ring, ids)
}

// loadLegacy sets lgIDs from legacyset.dat (opened with ring), before the Keyring
// is installed. fresh is true right after setup: the file is then written anew
// from enumerate(), the DPAPI case ids that exist now. A missing file with an
// existing master key (made before the file existed) is treated the same way, once.
// A damaged file, an unreadable one, or a failed write leaves lgIDs nil, so every
// Windows sealed case is refused; no case folder is touched. Callers hold op.
func (s *MasterKeySession) loadLegacy(ring *masterkey.Keyring, fresh bool, enumerate func() []string) {
	if !fresh {
		got, st, err := masterkey.ReadLegacySet(s.dir, ring)
		switch {
		case err != nil || st == masterkey.LegacyDamaged:
			s.clearLegacy()
			return
		case st == masterkey.LegacyOK:
			s.setLegacy(got)
			return
		}
	}
	ids := enumerate()
	if err := masterkey.WriteLegacySet(s.dir, ring, ids); err != nil {
		s.clearLegacy()
		return
	}
	s.setLegacy(ids)
}

// setLegacy installs ids as the set (an empty set is a set).
func (s *MasterKeySession) setLegacy(ids []string) {
	m := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		m[id] = struct{}{}
	}
	s.lgMu.Lock()
	s.lgIDs = m
	s.lgMu.Unlock()
}

// clearLegacy drops the set: every Windows sealed case is then refused.
func (s *MasterKeySession) clearLegacy() {
	s.lgMu.Lock()
	s.lgIDs = nil
	s.lgMu.Unlock()
}

// install replaces the Keyring. Callers hold op.
func (s *MasterKeySession) install(r *masterkey.Keyring) {
	s.mu.Lock()
	old := s.ring
	s.ring = r
	s.mu.Unlock()
	if old != nil {
		old.Lock()
	}
}

// lock detaches the Keyring first (so every new Lookup and the 423 gate see
// "locked" at once), runs closeHandles, then zeroes the key. closeHandles runs
// a second time after the key is zeroed to catch a case that was mid-open when
// the first pass ran.
func (s *MasterKeySession) lock(closeHandles func()) {
	s.op.Lock()
	defer s.op.Unlock()
	s.mu.Lock()
	r := s.ring
	s.ring = nil
	s.mu.Unlock()
	s.clearLegacy()
	if closeHandles != nil {
		closeHandles()
	}
	if r != nil {
		r.Lock()
	}
	if closeHandles != nil {
		closeHandles()
	}
}

// checkPassphrase is the one place the master passphrase is judged, for unlock
// and for the export re-authentication alike, so a guesser gets one shared set of
// attempts whichever route it uses. The caller holds op. It returns the unlocked
// Keyring on success (the caller installs or Locks it). A non-zero wait means the
// throttle refused the attempt (not evaluated, not counted). A wrong passphrase,
// an empty one and one over maxPassphraseBytes all count as masterkey.ErrWrongKey
// without running Argon2 for the last two. Any other error is not a judgement of
// the passphrase and is not counted.
func (s *MasterKeySession) checkPassphrase(pass []byte) (*masterkey.Keyring, time.Duration, error) {
	if wait := s.unlockTh.wait(); wait > 0 {
		return nil, wait, nil
	}
	if len(pass) == 0 || len(pass) > maxPassphraseBytes {
		s.unlockTh.fail()
		return nil, 0, masterkey.ErrWrongKey
	}
	// Unlock zeroes the slice it is given; hand it its own copy.
	cp := bytes.Clone(pass)
	ring, err := masterkey.Unlock(s.dir, cp)
	clear(cp)
	if errors.Is(err, masterkey.ErrWrongKey) {
		s.unlockTh.fail()
		return nil, 0, err
	}
	if err != nil {
		return nil, 0, err
	}
	s.unlockTh.reset()
	return ring, 0, nil
}

// VerifyPassphrase checks pass against the master key file under the operation
// lock and the shared unlock throttle, and drops the derived key at once. It is
// for re-authentication of a sensitive action while the session is unlocked.
func (s *MasterKeySession) VerifyPassphrase(pass []byte) (time.Duration, error) {
	s.op.Lock()
	defer s.op.Unlock()
	ring, wait, err := s.checkPassphrase(pass)
	if ring != nil {
		ring.Lock()
	}
	return wait, err
}

// throttleWait is how long the shared passphrase throttle still refuses attempts.
func (s *MasterKeySession) throttleWait() time.Duration { return s.unlockTh.wait() }

/* ------------------------------------------------------------- throttle */

// throttle counts failures. After throttleAfter of them it refuses attempts
// until a delay passes: 1 s after the 5th failure, doubling to 60 s. A refused
// attempt is not evaluated and not counted. Success resets it.
type throttle struct {
	mu    sync.Mutex
	fails int
	until time.Time
	now   func() time.Time // test seam
}

func (t *throttle) clock() time.Time {
	if t.now != nil {
		return t.now()
	}
	return time.Now()
}

// wait returns how long the caller must wait, or 0 if it may try now.
func (t *throttle) wait() time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	if d := t.until.Sub(t.clock()); d > 0 {
		return d
	}
	return 0
}

func (t *throttle) fail() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.fails++
	if t.fails < throttleAfter {
		return
	}
	d := throttleMaxWait
	if n := t.fails - throttleAfter; n < 6 {
		d = throttleBaseWait << n
		if d > throttleMaxWait {
			d = throttleMaxWait
		}
	}
	t.until = t.clock().Add(d)
}

func (t *throttle) reset() {
	t.mu.Lock()
	t.fails, t.until = 0, time.Time{}
	t.mu.Unlock()
}

/* ------------------------------------------------------------- wiring */

// WithMasterKey gives the server the master-key session. Without it the
// master-key routes answer "none" / 503 and no case route is gated.
func WithMasterKey(s *MasterKeySession) Option { return func(h *handler) { h.mk = s } }

// lockedGate reports whether a request to rt must be answered 423. Every case
// route is gated by its pattern, so a route added under /api/cases cannot
// forget it; the route-table test walks the table to prove it.
func (h *handler) lockedGate(rt *route) bool {
	if h.mk == nil || !gatedByMasterKey(rt) {
		return false
	}
	// A damaged key file keeps the cases closed exactly like a locked key: the
	// master-key cases cannot be opened and a new case must not be sealed with
	// anything else.
	st := h.mk.State()
	return st == MasterKeyLocked || st == MasterKeyDamaged
}

func gatedByMasterKey(rt *route) bool {
	return !rt.public && (rt.pattern == "/api/cases" || strings.HasPrefix(rt.pattern, "/api/cases/"))
}

func writeLocked(w http.ResponseWriter) {
	writeJSON(w, http.StatusLocked, caseFailure{Error: "locked", Reason: "locked"}, 4096)
}

// state is the handler's view of the session.
func (h *handler) masterKeyState() string {
	if h.mk == nil {
		return MasterKeyNone
	}
	return h.mk.State()
}

/* ----------------------------------------------------------------- DTOs */

type masterKeyStatus struct {
	State string `json:"state"`
	// DPAPICases counts cases whose key file is still sealed by Windows (including
	// any left half re-wrapped). Re-wrapping them is the "Protect older cases" action.
	DPAPICases int `json:"dpapiCases"`
	// NeedsRewrap counts the cases among those that a cut re-wrap left half done:
	// the manifest is under the master key, the key file is not. Opening one
	// repairs it by itself while the key is unlocked; when that cannot happen the
	// case routes answer 409 reason "needs-rewrap", and the client offers the same
	// "Protect older cases" action (POST /api/master-key/rewrap, {"all":true}),
	// which finishes it. Always 0 while locked.
	NeedsRewrap int `json:"needsRewrap"`
	// UntrustedCases counts case folders sealed by Windows only that were not on
	// this PC when the master key was set up (or whose list could not be loaded).
	// They are refused (409 reason "untrusted-case"), cannot be re-wrapped, and are
	// not in DPAPICases; they can still be erased. Omitted when 0 (always while locked).
	UntrustedCases int `json:"untrustedCases,omitempty"`
}

const needsRewrapText = "This case was only partly protected with the master key. Protect older cases to finish it."

type masterKeyFailure struct {
	Error             string `json:"error"`
	Reason            string `json:"reason"`
	State             string `json:"state"`
	RetryAfterSeconds int    `json:"retryAfterSeconds,omitempty"`
}

type rewrapRequest struct {
	CaseID string `json:"caseId"`
	All    bool   `json:"all"`
}

type rewrapResult struct {
	CaseID string `json:"caseId"`
	Status string `json:"status"` // rewrapped | already | failed
	Reason string `json:"reason,omitempty"`
}

type rewrapResponse struct {
	Results []rewrapResult `json:"results"`
}

func (h *handler) masterKeyFail(w http.ResponseWriter, status int, reason, message string) {
	writeJSON(w, status, masterKeyFailure{Error: message, Reason: reason, State: h.masterKeyState()}, 4096)
}

func (h *handler) masterKeyUnavailable(w http.ResponseWriter) bool {
	if h.mk != nil {
		return false
	}
	h.masterKeyFail(w, http.StatusServiceUnavailable, "master-key-unavailable", "The master key is unavailable.")
	return true
}

func (h *handler) masterKeyThrottled(w http.ResponseWriter, wait time.Duration) {
	secs := int((wait + time.Second - 1) / time.Second)
	if secs < 1 {
		secs = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	writeJSON(w, http.StatusTooManyRequests, masterKeyFailure{
		Error: "Too many attempts. Wait before trying again.", Reason: "throttled",
		State: h.masterKeyState(), RetryAfterSeconds: secs,
	}, 4096)
}

/* ------------------------------------------------------------ body parse */

// secretBody is a decoded setup or unlock body. The passphrase fields are
// []byte that never become Go strings; wipe zeroes everything.
type secretBody struct {
	raw          []byte
	fields       map[string]json.RawMessage
	passphrase   []byte
	confirm      []byte
	acknowledged bool
}

func (b *secretBody) wipe() {
	clear(b.raw)
	for _, v := range b.fields {
		clear(v)
	}
	clear(b.passphrase)
	clear(b.confirm)
}

// readSecretBody reads and checks the body of setup (confirm and acknowledged
// allowed) or unlock (only passphrase). It answers the error itself and returns
// nil. Errors never echo any part of the body.
func (h *handler) readSecretBody(w http.ResponseWriter, r *http.Request, setup bool) *secretBody {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		h.masterKeyFail(w, http.StatusUnsupportedMediaType, "bad-content-type", "Content-Type must be application/json.")
		return nil
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxSecretBody)
	raw, err := io.ReadAll(r.Body)
	b := &secretBody{raw: raw}
	if err != nil {
		b.wipe()
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			h.masterKeyFail(w, http.StatusRequestEntityTooLarge, "too-large", "The request body is too large.")
		} else {
			h.masterKeyFail(w, http.StatusBadRequest, "bad-request", "The request body is invalid.")
		}
		return nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		b.wipe()
		h.masterKeyFail(w, http.StatusBadRequest, "bad-request", "The request body is invalid.")
		return nil
	}
	b.fields = fields
	for name := range fields {
		switch name {
		case "passphrase":
		case "confirm", "acknowledged":
			if !setup {
				b.wipe()
				h.masterKeyFail(w, http.StatusBadRequest, "bad-request", "The request body is invalid.")
				return nil
			}
		default:
			b.wipe()
			h.masterKeyFail(w, http.StatusBadRequest, "bad-request", "The request body is invalid.")
			return nil
		}
	}
	ok := true
	if v, present := fields["passphrase"]; present {
		b.passphrase, ok = unquoteSecret(v)
	}
	if v, present := fields["confirm"]; ok && present {
		b.confirm, ok = unquoteSecret(v)
	}
	if v, present := fields["acknowledged"]; ok && present {
		switch string(v) {
		case "true":
			b.acknowledged = true
		case "false":
		default:
			ok = false
		}
	}
	if !ok {
		b.wipe()
		h.masterKeyFail(w, http.StatusBadRequest, "bad-request", "The request body is invalid.")
		return nil
	}
	return b
}

// unquoteSecret decodes one JSON string into bytes without making a Go string
// of it. It reports false for anything that is not a string or is not valid
// UTF-8 once decoded.
func unquoteSecret(raw []byte) ([]byte, bool) {
	if len(raw) < 2 || raw[0] != '"' || raw[len(raw)-1] != '"' {
		return nil, false
	}
	in := raw[1 : len(raw)-1]
	out := make([]byte, 0, len(in))
	for i := 0; i < len(in); {
		c := in[i]
		if c != '\\' {
			out = append(out, c)
			i++
			continue
		}
		if i+1 >= len(in) {
			clear(out)
			return nil, false
		}
		switch e := in[i+1]; e {
		case '"', '\\', '/':
			out = append(out, e)
			i += 2
		case 'b':
			out = append(out, '\b')
			i += 2
		case 'f':
			out = append(out, '\f')
			i += 2
		case 'n':
			out = append(out, '\n')
			i += 2
		case 'r':
			out = append(out, '\r')
			i += 2
		case 't':
			out = append(out, '\t')
			i += 2
		case 'u':
			r, ok := hex4(in, i+2)
			if !ok {
				clear(out)
				return nil, false
			}
			i += 6
			if utf16.IsSurrogate(r) {
				// A pair needs a second \uXXXX that is the low half.
				if r < 0xDC00 && i+1 < len(in) && in[i] == '\\' && in[i+1] == 'u' {
					if r2, ok := hex4(in, i+2); ok && r2 >= 0xDC00 && r2 <= 0xDFFF {
						r = utf16.DecodeRune(r, r2)
						i += 6
						out = utf8.AppendRune(out, r)
						continue
					}
				}
				r = utf8.RuneError
			}
			out = utf8.AppendRune(out, r)
		default:
			clear(out)
			return nil, false
		}
	}
	if !utf8.Valid(out) {
		clear(out)
		return nil, false
	}
	return out, true
}

func hex4(b []byte, at int) (rune, bool) {
	if at+4 > len(b) {
		return 0, false
	}
	var r rune
	for _, c := range b[at : at+4] {
		switch {
		case c >= '0' && c <= '9':
			r = r<<4 | rune(c-'0')
		case c >= 'a' && c <= 'f':
			r = r<<4 | rune(c-'a'+10)
		case c >= 'A' && c <= 'F':
			r = r<<4 | rune(c-'A'+10)
		default:
			return 0, false
		}
	}
	return r, true
}

/* -------------------------------------------------------------- handlers */

type rewrapStore interface {
	CaseKind(id casestore.CaseID) (string, error)
	Rewrap(ctx context.Context, id casestore.CaseID) error
}

// dpapiCaseCount counts the cases that could still be re-wrapped, and how many
// of those a cut re-wrap left half done. Both are 0 while locked and when the
// store cannot report kinds.
//
// A folder the store refuses as untrusted (casestore.CaseInfo.Untrusted: sealed by
// Windows only, not on the legacy set) is counted apart in untrusted and never as
// something "Protect older cases" could finish.
func (h *handler) dpapiCaseCount(ctx context.Context) (dpapi, half, untrusted int) {
	if st := h.masterKeyState(); st == MasterKeyLocked || st == MasterKeyDamaged || h.cases == nil {
		return 0, 0, 0
	}
	rs, ok := h.cases.(rewrapStore)
	if !ok {
		return 0, 0, 0
	}
	list, err := h.cases.List(ctx)
	if err != nil {
		return 0, 0, 0
	}
	partial, _ := h.cases.(interface{ NeedsRewrap(casestore.CaseID) bool })
	for _, info := range list {
		if info.Untrusted {
			untrusted++
			continue
		}
		if kind, err := rs.CaseKind(info.ID); err == nil && kind == casestore.KindDPAPI {
			dpapi++
			if partial != nil && partial.NeedsRewrap(info.ID) {
				half++
			}
		}
	}
	return dpapi, half, untrusted
}

// legacyCandidates lists the ids of the cases whose key file is sealed by Windows
// right now: what the legacy set records. It never returns nil.
func (h *handler) legacyCandidates(ctx context.Context) []string {
	ids := []string{}
	if h.cases == nil {
		return ids
	}
	rs, ok := h.cases.(rewrapStore)
	if !ok {
		return ids
	}
	list, err := h.cases.List(ctx)
	if err != nil {
		return ids
	}
	for _, info := range list {
		if kind, err := rs.CaseKind(info.ID); err == nil && kind == casestore.KindDPAPI {
			ids = append(ids, string(info.ID))
		}
	}
	return ids
}

func (h *handler) masterKeyReply(w http.ResponseWriter, r *http.Request) {
	st := h.masterKeyState()
	n, half, bad := h.dpapiCaseCount(r.Context())
	writeJSON(w, http.StatusOK, masterKeyStatus{State: st, DPAPICases: n, NeedsRewrap: half, UntrustedCases: bad}, 4096)
}

func (h *handler) masterKeySetup(w http.ResponseWriter, r *http.Request) {
	if h.masterKeyUnavailable(w) {
		return
	}
	b := h.readSecretBody(w, r, true)
	if b == nil {
		return
	}
	defer b.wipe()
	s := h.mk
	s.op.Lock()
	defer s.op.Unlock()
	switch masterkey.Inspect(s.dir) {
	case masterkey.FileUsable:
		h.masterKeyFail(w, http.StatusConflict, "already-set", "A master key is already set.")
		return
	case masterkey.FileDamaged:
		// Never overwrite what is there: it may be a good file the user can put
		// back. The separate reset-damaged action moves it aside first.
		h.masterKeyFail(w, http.StatusConflict, "key-file-damaged", keyFileDamagedText)
		return
	}
	if wait := s.setupTh.wait(); wait > 0 {
		h.masterKeyThrottled(w, wait)
		return
	}
	// Validation failures below are cheap, answer no secret question and are
	// not counted: a user who mistypes the confirmation five times must not be
	// locked out of setting up. Only a failure of the key file write counts.
	if !b.acknowledged {
		h.masterKeyFail(w, http.StatusBadRequest, "acknowledgement-required", "Confirm that you understand there is no recovery.")
		return
	}
	a, c := masterkey.Normalize(b.passphrase), masterkey.Normalize(b.confirm)
	same := len(a) == len(c) && subtle.ConstantTimeCompare(a, c) == 1
	rules := masterkey.CheckPassphrase(a)
	tooLong := len(b.passphrase) > maxPassphraseBytes
	clear(a)
	clear(c)
	switch {
	case !same:
		h.masterKeyFail(w, http.StatusBadRequest, "mismatch", "The two entries do not match.")
		return
	case errors.Is(rules, masterkey.ErrInvalidChars):
		h.masterKeyFail(w, http.StatusBadRequest, "invalid-characters", "The passphrase contains characters that cannot be used.")
		return
	case errors.Is(rules, masterkey.ErrTooShort):
		h.masterKeyFail(w, http.StatusBadRequest, "too-short", "The passphrase is too short.")
		return
	case errors.Is(rules, masterkey.ErrBlank):
		h.masterKeyFail(w, http.StatusBadRequest, "blank", "The passphrase needs more visible characters.")
		return
	case tooLong:
		h.masterKeyFail(w, http.StatusBadRequest, "too-long", "The passphrase is too long.")
		return
	}
	// Setup and Unlock each zero the slice they are given; give each its own copy.
	forSetup := bytes.Clone(b.passphrase)
	err := masterkey.Setup(s.dir, forSetup)
	clear(forSetup)
	switch {
	case errors.Is(err, masterkey.ErrAlreadySet):
		h.masterKeyFail(w, http.StatusConflict, "already-set", "A master key is already set.")
		return
	case errors.Is(err, masterkey.ErrDamaged):
		h.masterKeyFail(w, http.StatusConflict, "key-file-damaged", keyFileDamagedText)
		return
	case errors.Is(err, masterkey.ErrTooShort), errors.Is(err, masterkey.ErrBlank), errors.Is(err, masterkey.ErrInvalidChars):
		// Unreachable: checked above. Fail closed with the generic refusal.
		h.masterKeyFail(w, http.StatusBadRequest, "too-short", "The passphrase is too short.")
		return
	case err != nil:
		s.setupTh.fail()
		h.masterKeyFail(w, http.StatusInternalServerError, "setup-failed", "The master key could not be created.")
		return
	}
	forUnlock := bytes.Clone(b.passphrase)
	ring, err := masterkey.Unlock(s.dir, forUnlock)
	clear(forUnlock)
	if err != nil {
		// The key file exists but this session could not open it. The user can
		// unlock with the same passphrase.
		h.masterKeyFail(w, http.StatusInternalServerError, "setup-incomplete", "The master key was created but could not be unlocked. Unlock it with the same passphrase.")
		return
	}
	s.setupTh.reset()
	// Record which Windows sealed cases exist right now; only these may ever be
	// used without the master key (L4). Before the Keyring is installed.
	s.loadLegacy(ring, true, func() []string { return h.legacyCandidates(r.Context()) })
	s.install(ring)
	h.recoverAll(r.Context())
	h.masterKeyReply(w, r)
}

func (h *handler) masterKeyUnlock(w http.ResponseWriter, r *http.Request) {
	if h.masterKeyUnavailable(w) {
		return
	}
	b := h.readSecretBody(w, r, false)
	if b == nil {
		return
	}
	defer b.wipe()
	s := h.mk
	s.op.Lock()
	defer s.op.Unlock()
	switch s.State() {
	case MasterKeyUnlocked:
		h.masterKeyReply(w, r)
		return
	case MasterKeyNone:
		h.masterKeyFail(w, http.StatusConflict, "not-set", "No master key is set.")
		return
	case MasterKeyDamaged:
		// Not a wrong passphrase, so not judged, not counted, no Argon2.
		h.masterKeyFail(w, http.StatusConflict, "key-file-damaged", keyFileDamagedText)
		return
	}
	ring, wait, err := s.checkPassphrase(b.passphrase)
	if wait > 0 {
		h.masterKeyThrottled(w, wait)
		return
	}
	switch {
	case errors.Is(err, masterkey.ErrWrongKey):
		h.masterKeyFail(w, http.StatusUnauthorized, "wrong-key", "The passphrase is wrong.")
		return
	case errors.Is(err, masterkey.ErrNotSet):
		h.masterKeyFail(w, http.StatusConflict, "not-set", "No master key is set.")
		return
	case errors.Is(err, masterkey.ErrDamaged):
		h.masterKeyFail(w, http.StatusConflict, "key-file-damaged", keyFileDamagedText)
		return
	case err != nil:
		h.masterKeyFail(w, http.StatusInternalServerError, "unlock-failed", "The master key could not be unlocked.")
		return
	}
	s.loadLegacy(ring, false, func() []string { return h.legacyCandidates(r.Context()) })
	s.install(ring)
	h.recoverAll(r.Context())
	h.masterKeyReply(w, r)
}

func (h *handler) masterKeyLock(w http.ResponseWriter, r *http.Request) {
	if h.masterKeyUnavailable(w) {
		return
	}
	if h.mk.State() == MasterKeyNone {
		h.masterKeyFail(w, http.StatusConflict, "not-set", "No master key is set.")
		return
	}
	// Stop what could still write into a case, then detach the key and close
	// every open handle (zeroing the case keys) before answering.
	h.stopSamplerAll()
	h.jobs.cancelAll()
	h.mk.lock(h.closeCaseHandles)
	writeJSON(w, http.StatusOK, masterKeyStatus{State: h.mk.State()}, 4096)
}

// closeCaseHandles closes every case handle the store holds open.
func (h *handler) closeCaseHandles() {
	if c, ok := h.cases.(interface{ Close() error }); ok {
		_ = c.Close()
	}
}

// cancelAll asks every unfinished import job to stop.
func (j *importJobs) cancelAll() {
	j.mu.Lock()
	jobs := make([]*importJob, 0, len(j.m))
	for _, job := range j.m {
		jobs = append(jobs, job)
	}
	j.mu.Unlock()
	for _, job := range jobs {
		if job.active() {
			job.requestCancel()
		}
	}
}

func (h *handler) masterKeyRewrap(w http.ResponseWriter, r *http.Request) {
	if h.masterKeyUnavailable(w) {
		return
	}
	var req rewrapRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if (req.CaseID == "") == !req.All {
		// Neither, or both.
		h.masterKeyFail(w, http.StatusBadRequest, "bad-request", "Give either caseId or all.")
		return
	}
	switch h.mk.State() {
	case MasterKeyNone:
		h.masterKeyFail(w, http.StatusConflict, "no-master-key", "Set a master key first.")
		return
	case MasterKeyLocked, MasterKeyDamaged:
		writeLocked(w)
		return
	}
	if !h.casesAvailable(w) {
		return
	}
	rs, ok := h.cases.(rewrapStore)
	if !ok {
		h.masterKeyFail(w, http.StatusServiceUnavailable, "rewrap-unavailable", "Re-wrapping is unavailable.")
		return
	}
	var ids []casestore.CaseID
	if req.All {
		list, err := h.cases.List(r.Context())
		if err != nil {
			writeStoreError(w, err)
			return
		}
		for _, info := range list {
			ids = append(ids, info.ID)
		}
	} else {
		id, ok := caseIDOf(w, req.CaseID)
		if !ok {
			return
		}
		ids = []casestore.CaseID{id}
	}
	results := make([]rewrapResult, 0, len(ids))
	for _, id := range ids {
		results = append(results, rewrapOne(r.Context(), rs, id))
	}
	writeJSON(w, http.StatusOK, rewrapResponse{Results: results}, 1<<20)
}

func rewrapOne(ctx context.Context, rs rewrapStore, id casestore.CaseID) rewrapResult {
	res := rewrapResult{CaseID: string(id)}
	kind, err := rs.CaseKind(id)
	if err == nil && kind == casestore.KindMasterKey {
		res.Status = "already"
		return res
	}
	if err == nil {
		err = rs.Rewrap(ctx, id)
	}
	if err == nil {
		res.Status = "rewrapped"
		return res
	}
	res.Status = "failed"
	switch {
	case errors.Is(err, casestore.ErrNotFound):
		res.Reason = "case-not-found"
	case errors.Is(err, casestore.ErrLocked):
		res.Reason = "locked"
	case errors.Is(err, casestore.ErrUntrustedLegacy):
		res.Reason = "untrusted-case"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		res.Reason = "canceled"
	default:
		res.Reason = "rewrap-failed"
	}
	return res
}

/* ------------------------------------------------- damaged key file reset */

const keyFileDamagedText = "The master key file on this PC is damaged or has been replaced, so no passphrase can open it."

type resetDamagedRequest struct {
	// Acknowledged must be the literal true: the user has read that cases
	// sealed with the existing master key stay unreadable under a new one.
	Acknowledged bool `json:"acknowledged"`
}

type resetDamagedReply struct {
	State      string `json:"state"`
	DPAPICases int    `json:"dpapiCases"`
	// KeptAs is the file name (no folder) the damaged item was moved to, next to
	// where it was. Nothing is deleted: put it back under its old name to undo.
	KeptAs string `json:"keptAs"`
}

// masterKeyResetDamaged is the one recovery from a damaged masterkey.dat. It is
// a separate action from setup, needs an explicit acknowledgement, works only
// while the file is judged damaged (checked again under the operation lock) and
// never deletes: the damaged item is renamed aside, so a file that was actually
// good (restored from a backup, say) can be put back. Afterwards the state is
// "none" and the normal setup route creates a new key. Cases sealed with the old
// master key stay unreadable under the new one, and the client must say so.
func (h *handler) masterKeyResetDamaged(w http.ResponseWriter, r *http.Request) {
	if h.masterKeyUnavailable(w) {
		return
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		h.masterKeyFail(w, http.StatusUnsupportedMediaType, "bad-content-type", "Content-Type must be application/json.")
		return
	}
	var req resetDamagedRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil || dec.More() {
		h.masterKeyFail(w, http.StatusBadRequest, "bad-request", "The request body is invalid.")
		return
	}
	s := h.mk
	s.op.Lock()
	defer s.op.Unlock()
	if s.State() != MasterKeyDamaged {
		// Refuse before the acknowledgement: a good key file is never touched.
		h.masterKeyFail(w, http.StatusConflict, "not-damaged", "The master key file is not damaged.")
		return
	}
	if !req.Acknowledged {
		h.masterKeyFail(w, http.StatusBadRequest, "acknowledgement-required",
			"Confirm that cases protected by the old master key will stay unreadable.")
		return
	}
	from := masterkey.Path(s.dir)
	keptAs := masterkey.FileName + ".damaged-" + time.Now().UTC().Format("20060102T150405.000000000Z")
	// Judge the file again immediately before moving it: a good key file put back
	// by hand since the check above must never be renamed away. (A rename by path
	// cannot be made atomic with the check; this narrows the window to the two
	// calls.)
	if masterkey.Inspect(s.dir) != masterkey.FileDamaged {
		h.masterKeyFail(w, http.StatusConflict, "not-damaged", "The master key file is not damaged.")
		return
	}
	if err := os.Rename(from, filepath.Join(s.dir, keptAs)); err != nil {
		h.masterKeyFail(w, http.StatusInternalServerError, "reset-failed", "The damaged master key file could not be moved aside.")
		return
	}
	n, _, _ := h.dpapiCaseCount(r.Context())
	writeJSON(w, http.StatusOK, resetDamagedReply{State: s.State(), DPAPICases: n, KeptAs: keptAs}, 4096)
}
