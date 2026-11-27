package keeper

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"sync"
	"time"

	"api-audit-explorer/internal/blackbox"
	"api-audit-explorer/internal/sqlaccess"
)

// The Blackbox SQL set-up hand-over (RV-SEC-2).
//
// Turning SQL collection on with a saved login needs the elevated Blackbox helper to read that login. The keeper used to write it to a
// clear-text file in its temp folder for up to five minutes. It now holds the login in memory under a random one-time id instead:
//
//  1. sql.handover  (the person's Unravis): the keeper builds the login from the saved connection, keeps it and answers with only the id.
//  2. The helper is started with the id on its command line (the id alone opens nothing).
//  3. sql.handover.take (the helper, over this same pipe): the keeper gives the login once, to a caller in the same Windows session as the
//     person who asked, and forgets it. A second take, a take after the lifetime, and a take from another session are all refused with
//     the same words and none of them reveals which it was.
//  4. sql.handover.revoke (the person's Unravis, when the set-up ends): forgets whatever is left.
//
// Who may take is decided by the Windows session of the pipe client, not its account: the helper runs elevated, which when a standard
// user types an administrator's password is a different account in the same session. Other people signed in to the PC are in other
// sessions. The pipe itself already refuses everyone but people signed in to this PC; the id is 128 random bits.
//
// The login is held as the document the helper reads (blackbox.MarshalSQLLoginDoc) and zeroed when it is taken, revoked or expired.
// This is a best effort: Go strings cannot be zeroed, so the copies the encoder and the pipe buffers make live until the garbage
// collector frees them. It is the same reach as every other place the keeper holds a password in memory.

// HandoverLife is how long a hand-over waits for the helper to ask for it: the same time the helper itself is given.
const HandoverLife = 5 * time.Minute

// maxHandovers bounds what the keeper holds at once. Each session keeps at most one (a new one replaces its older one), so this is
// also a bound on how many sessions can be setting up together.
const maxHandovers = 32

// errHandoverGone is the one answer for every reason a take cannot be served: the id is unknown, was used, has expired, or belongs to
// another session. It tells a guesser nothing.
var errHandoverGone = &WireError{Code: CodeRefused, Message: "The saved login is no longer waiting for the Blackbox. It was used already, took too long, or was made for someone else. Start the set-up again."}

// HandoverArgs name a hand-over for take and revoke.
type HandoverArgs struct {
	ID string `json:"id"`
}

// HandoverTicket is the answer of sql.handover: only the one-time id, never any part of the login.
type HandoverTicket struct {
	ID string `json:"id"`
}

type handoverEntry struct {
	doc     []byte
	conn    string // the saved connection it came from, for the audit log
	session uint32
	expires time.Time
	timer   *time.Timer
}

type handoverStore struct {
	mu   sync.Mutex
	now  func() time.Time
	life time.Duration
	m    map[string]*handoverEntry
}

func newHandoverStore(life time.Duration, now func() time.Time) *handoverStore {
	if now == nil {
		now = time.Now
	}
	return &handoverStore{now: now, life: life, m: map[string]*handoverEntry{}}
}

func wipeBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// drop forgets the entry and zeroes its login. The caller holds s.mu.
func (s *handoverStore) drop(id string) {
	if e, ok := s.m[id]; ok {
		if e.timer != nil {
			e.timer.Stop()
		}
		wipeBytes(e.doc)
		delete(s.m, id)
	}
}

// put keeps doc for session and returns its one-time id. The store owns doc from here on and zeroes it. A session's older hand-over is
// forgotten first: a person sets up one thing at a time.
func (s *handoverStore) put(session uint32, conn string, doc []byte) (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		wipeBytes(doc)
		return "", &WireError{Code: CodeInternal, Message: "The login could not be prepared for the Blackbox."}
	}
	id := hex.EncodeToString(raw[:])
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for k, e := range s.m {
		if e.session == session || !now.Before(e.expires) {
			s.drop(k)
		}
	}
	if len(s.m) >= maxHandovers {
		wipeBytes(doc)
		return "", &WireError{Code: CodeBusy, Message: "The key keeper is already preparing several Blackbox set-ups. Try again in a few minutes."}
	}
	e := &handoverEntry{doc: doc, conn: conn, session: session, expires: now.Add(s.life)}
	e.timer = time.AfterFunc(s.life, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if cur, ok := s.m[id]; ok && cur == e {
			s.drop(id)
		}
	})
	s.m[id] = e
	return id, nil
}

// take gives the login once. It is served only to the session that asked for it, before it expires. Anything else is refused without
// consuming the entry, so a wrong guess cannot use up the real one. An expired entry is zeroed on the spot.
func (s *handoverStore) take(id string, session uint32) (doc []byte, conn string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key, e := s.find(id)
	if e == nil {
		return nil, "", false
	}
	if !s.now().Before(e.expires) {
		s.drop(key)
		return nil, "", false
	}
	if e.session != session {
		return nil, "", false
	}
	doc, conn = e.doc, e.conn
	e.doc = nil // ownership moves to the caller, who zeroes it after sending
	if e.timer != nil {
		e.timer.Stop()
	}
	delete(s.m, key)
	return doc, conn, true
}

// revoke forgets the entry if the session owns it. Unknown, used and other people's ids are ignored.
func (s *handoverStore) revoke(id string, session uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if key, e := s.find(id); e != nil && e.session == session {
		s.drop(key)
	}
}

// find looks the id up without branching on how much of it matched. The caller holds s.mu.
func (s *handoverStore) find(id string) (string, *handoverEntry) {
	var hit *handoverEntry
	hitKey := ""
	for k, e := range s.m {
		if subtle.ConstantTimeCompare([]byte(k), []byte(id)) == 1 {
			hit, hitKey = e, k
		}
	}
	return hitKey, hit
}

func (s *handoverStore) held() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.m)
}

// handoverDoc is the answer of sql.handover.take. Its encoding hands the document over and zeroes it, so the keeper's own copy is gone
// as soon as it has been encoded for the pipe.
type handoverDoc struct{ doc []byte }

func (h *handoverDoc) MarshalJSON() ([]byte, error) {
	out := append([]byte(nil), h.doc...)
	wipeBytes(h.doc)
	h.doc = nil
	if len(out) == 0 {
		return []byte("null"), nil
	}
	return out, nil
}

func (b *Backend) handovers() *handoverStore {
	b.handoverOnce.Do(func() {
		if b.hand == nil {
			b.hand = newHandoverStore(HandoverLife, nil)
		}
	})
	return b.hand
}

// handoverSession is the Windows session a hand-over is bound to. Session 0 (services) and an unknown session never qualify: the
// person's Unravis and the helper they started are in an interactive session.
func handoverSession(c *Call) (uint32, bool) {
	s := c.Caller.Session
	return s, s != 0 && s != unknownSession
}

// sqlHandover keeps the saved login of a connection for the Blackbox helper and answers with the one-time id. Windows sign-in has no
// login to keep and is refused.
func (b *Backend) sqlHandover(_ context.Context, c *Call) (any, error) {
	var ref SQLRef
	if err := c.Decode(&ref); err != nil {
		return nil, err
	}
	c.Record = ref.ID
	session, ok := handoverSession(c)
	if !ok {
		return nil, &WireError{Code: CodeRefused, Message: "The key keeper could not tell which Windows session is asking."}
	}
	t, _, err := b.sqlTarget(ref)
	if err != nil {
		return nil, err
	}
	s, err := sqlaccess.StoredLoginFor(t)
	if err != nil {
		return nil, &WireError{Code: CodeInvalid, Message: err.Error()}
	}
	doc, err := blackbox.MarshalSQLLoginDoc(s)
	if err != nil {
		return nil, &WireError{Code: CodeInternal, Message: "The login could not be prepared for the Blackbox."}
	}
	id, err := b.handovers().put(session, ref.ID, doc)
	if err != nil {
		return nil, err
	}
	return HandoverTicket{ID: id}, nil
}

// sqlHandoverTake gives the login of a hand-over once. The audit line names the saved connection, never the hand-over id.
func (b *Backend) sqlHandoverTake(_ context.Context, c *Call) (any, error) {
	var a HandoverArgs
	if err := c.Decode(&a); err != nil {
		return nil, err
	}
	session, ok := handoverSession(c)
	if !ok {
		return nil, errHandoverGone
	}
	doc, conn, ok := b.handovers().take(a.ID, session)
	if !ok {
		return nil, errHandoverGone
	}
	c.Record = conn
	return &handoverDoc{doc: doc}, nil
}

// sqlHandoverRevoke forgets a hand-over that was not used. It always answers the same, so it cannot be used to probe ids.
func (b *Backend) sqlHandoverRevoke(_ context.Context, c *Call) (any, error) {
	var a HandoverArgs
	if err := c.Decode(&a); err != nil {
		return nil, err
	}
	if session, ok := handoverSession(c); ok {
		b.handovers().revoke(a.ID, session)
	}
	return map[string]bool{"ok": true}, nil
}
