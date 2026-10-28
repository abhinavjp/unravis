package sqlite

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"api-audit-explorer/internal/casekey"
	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/masterkey"
	"api-audit-explorer/internal/sealedfile"
)

// The per-case key (decisions A12, A13). A keyed case has a third file, case.wrap
// (internal/casekey), and its case.key and manifest.dat are sealed under a random
// case master that only the case's passphrase can unwrap. The master key, a master
// key reset and Windows are not involved with such a case at all. Changing the
// passphrase rewrites case.wrap alone, atomically, so no half state exists.
//
// Setting and removing a key move two files and so pass through a direction state
// in case.wrap ("setting", "removing"). A cut leaves that state behind; it is
// finished by the next unlock, which holds the key the move needs. Each file move
// is idempotent: a file already in its destination kind is skipped.
var _ casestore.CaseKeyer = (*Store)(nil)

const wrapName = casekey.FileName

// ckEntry is one unlocked case key held in memory: the keyring over the case
// master, and the check value of the passphrase that opened it.
type ckEntry struct {
	ring          *masterkey.Keyring
	check, ending string
}

// caseKeys holds the unlocked case keys of a Store. The zero value is ready.
type caseKeys struct {
	mu sync.Mutex
	m  map[casestore.CaseID]*ckEntry
}

func (c *caseKeys) get(id casestore.CaseID) *ckEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.m[id]; e != nil && !e.ring.Locked() {
		return e
	}
	return nil
}

// install records an unlocked key. A key already unlocked keeps its keyring (the
// case master is the same), so a handle that is sealing right now is not cut off.
func (c *caseKeys) install(id casestore.CaseID, cm []byte, check, ending string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[casestore.CaseID]*ckEntry{}
	}
	if e := c.m[id]; e != nil && !e.ring.Locked() {
		e.check, e.ending = check, ending
		return
	}
	c.m[id] = &ckEntry{ring: masterkey.NewKeyring(cm), check: check, ending: ending}
}

func (c *caseKeys) drop(id casestore.CaseID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.m[id]; e != nil {
		e.ring.Lock()
		delete(c.m, id)
	}
}

func (c *caseKeys) ids() []casestore.CaseID {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]casestore.CaseID, 0, len(c.m))
	for id := range c.m {
		out = append(out, id)
	}
	return out
}

func (s *Store) keyParams() masterkey.Params {
	if s.opt.KeyParams != (masterkey.Params{}) {
		return s.opt.KeyParams
	}
	return masterkey.DefaultParams()
}

func (s *Store) ckStep(stage string) error {
	if s.opt.ckStep != nil {
		return s.opt.ckStep(stage)
	}
	return nil
}

func idOfDir(dir string) casestore.CaseID { return casestore.CaseID(filepath.Base(dir)) }

// lookupFor is the protector lookup for one case: the case-key kind resolves to
// that case's unlocked key or ErrCaseLocked, every other kind as Store.lookup.
func (s *Store) lookupFor(id casestore.CaseID) casestore.ProtectorLookup {
	return func(kind string) (sealedfile.Protector, error) {
		if kind == casestore.KindCaseKey {
			if e := s.ck.get(id); e != nil {
				return e.ring, nil
			}
			return nil, casestore.ErrCaseLocked
		}
		return s.lookup(kind)
	}
}

// lookupWith is lookupFor with ring standing in for the case's key during a move
// that has not registered the key yet.
func (s *Store) lookupWith(ring *masterkey.Keyring) casestore.ProtectorLookup {
	return func(kind string) (sealedfile.Protector, error) {
		if kind == casestore.KindCaseKey {
			return ring, nil
		}
		return s.lookup(kind)
	}
}

func wrapExists(dir string) bool {
	_, err := os.Lstat(filepath.Join(dir, wrapName))
	return err == nil
}

func readWrapFile(dir string) ([]byte, error) {
	path := filepath.Join(dir, wrapName)
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, casestore.ErrReparsePoint
	}
	if fi.Size() == 0 || fi.Size() > 4<<10 {
		return nil, ErrDamaged
	}
	return os.ReadFile(path)
}

func writeWrapFile(dir string, data []byte) error {
	return sealedfile.WriteAtomic(filepath.Join(dir, wrapName), "case.wrap.*.tmp", data)
}

// wrapEnding reads the readable ending from case.wrap: headers only, no key.
func wrapEnding(dir string) (string, error) {
	raw, err := readWrapFile(dir)
	if err != nil {
		return "", err
	}
	w, err := casekey.Parse(raw)
	if err != nil {
		return "", ErrDamaged
	}
	return w.Ending, nil
}

// CaseKey reports the state of the case's key from file headers.
func (s *Store) CaseKey(id casestore.CaseID) (casestore.CaseKeyInfo, error) {
	dir, err := s.caseDir(id)
	if err != nil {
		return casestore.CaseKeyInfo{}, err
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() || erasePending(dir) {
		return casestore.CaseKeyInfo{}, casestore.ErrNotFound
	}
	if !wrapExists(dir) {
		return casestore.CaseKeyInfo{}, nil
	}
	info := casestore.CaseKeyInfo{Has: true, Locked: s.ck.get(id) == nil}
	end, err := wrapEnding(dir)
	if err != nil {
		return info, err
	}
	info.Ending = end
	return info, nil
}

// keyedDir is the common start of every call that changes a case's key: the case
// folder, locked, not erasing, and the open handle if there is one.
func (s *Store) keyedDir(ctx context.Context, id casestore.CaseID) (dir string, release func(), h *caseState, err error) {
	if err := ctx.Err(); err != nil {
		return "", nil, nil, err
	}
	dir, err = s.caseDir(id)
	if err != nil {
		return "", nil, nil, err
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return "", nil, nil, casestore.ErrNotFound
	}
	release, h, erasing, err := s.lockCase(dir, id)
	if err != nil {
		return "", nil, nil, err
	}
	if erasing || erasePending(dir) {
		release()
		return "", nil, nil, casestore.ErrNotFound
	}
	return dir, release, h, nil
}

// openWrap opens case.wrap with pass: ErrNoCaseKey when there is none,
// ErrWrongCaseKey for a wrong key, ErrDamaged for a file that makes no sense.
func (s *Store) openWrap(dir string, id casestore.CaseID, pass []byte) (*casekey.Opened, error) {
	raw, err := readWrapFile(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, casestore.ErrNoCaseKey
	}
	if err != nil {
		return nil, ErrDamaged
	}
	w, err := casekey.Parse(raw)
	if err != nil {
		return nil, ErrDamaged
	}
	o, err := w.Open(pass, string(id))
	switch {
	case errors.Is(err, casekey.ErrWrongKey):
		return nil, casestore.ErrWrongCaseKey
	case err != nil:
		return nil, ErrDamaged
	}
	return o, nil
}

func ruleErr(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: %w", casestore.ErrCaseKeyRule, err)
}

// markGate refuses a change of a case's sealing while the case carries a mark:
// an archive reads the sealed files, a free or erase is deleting them.
func markGate(mark string) error {
	if err := casestore.CheckMark(mark); err != nil {
		return err
	}
	if mark != "" {
		return casestore.ErrArchiveRunning
	}
	return nil
}

// resealFiles moves manifest.dat and then case.key to kind to, opening each with
// whatever protector its header names (ring serves the case-key kind). A file
// already in kind to is skipped, so it can be run again after a cut. stage names
// the test hook calls "<stage>:manifest" and "<stage>:key".
func (s *Store) resealFiles(dir string, id casestore.CaseID, ring *masterkey.Keyring, to, stage string) error {
	look := s.lookupWith(ring)
	keyPath := filepath.Join(dir, keyName)
	if k, err := casestore.FileKind(filepath.Join(dir, manifestName)); err != nil {
		return ErrDamaged
	} else if k != to {
		m, err := readManifest(dir, fileSealer{s: s, dir: dir, look: look})
		if err != nil {
			return err
		}
		if err := writeManifest(dir, fileSealer{s: s, dir: dir, kind: to, look: look}, m); err != nil {
			return err
		}
	}
	if err := s.ckStep(stage + ":manifest"); err != nil {
		return err
	}
	if k, err := casestore.FileKind(keyPath); err != nil {
		return casestore.ErrKeyFile
	} else if k != to {
		keys, _, err := casestore.ReadKeyFileAny(keyPath, look)
		if err != nil {
			return err
		}
		defer keys.Zero()
		if keys.CaseID != string(id) {
			return ErrDamaged
		}
		base, err := look(to)
		if err != nil || base == nil {
			return ErrLocked
		}
		if err := casestore.WriteKeyFileKind(keyPath, to, base, keys); err != nil {
			return err
		}
	}
	return s.ckStep(stage + ":key")
}

// oldKindTrusted applies the legacy-folder rule to a case whose key file is still
// sealed by Windows, before it is resealed under a case key.
func (s *Store) oldKindTrusted(dir string, id casestore.CaseID) error {
	if k, err := casestore.FileKind(filepath.Join(dir, keyName)); err == nil && k == casestore.KindDPAPI {
		return s.legacyCheck(id, dir)
	}
	return nil
}

// settle finishes a change of sealing that case.wrap says was cut short, with the
// key in hand. It reports whether the case still has a key afterwards.
func (s *Store) settle(dir string, id casestore.CaseID, o *casekey.Opened, ring *masterkey.Keyring) (bool, error) {
	switch o.State {
	case casekey.StateActive:
		for _, name := range []string{manifestName, keyName} {
			if k, err := casestore.FileKind(filepath.Join(dir, name)); err != nil || k != casestore.KindCaseKey {
				return true, casestore.ErrKeyFile
			}
		}
		return true, nil
	case casekey.StateSetting:
		if err := s.oldKindTrusted(dir, id); err != nil {
			return true, err
		}
		if err := s.resealFiles(dir, id, ring, casestore.KindCaseKey, "set"); err != nil {
			return true, err
		}
		data, err := o.Seal(casekey.StateActive)
		if err != nil {
			return true, err
		}
		return true, writeWrapFile(dir, data)
	case casekey.StateRemoving:
		to := s.newKind()
		if err := s.resealFiles(dir, id, ring, to, "remove"); err != nil {
			return true, err
		}
		if err := os.Remove(filepath.Join(dir, wrapName)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return true, err
		}
		return false, nil
	}
	return true, ErrDamaged
}

// UnlockCase opens the case key with pass and keeps it in memory.
func (s *Store) UnlockCase(ctx context.Context, id casestore.CaseID, pass []byte) error {
	dir, release, h, err := s.keyedDir(ctx, id)
	if err != nil {
		return err
	}
	defer release()
	o, err := s.openWrap(dir, id, pass)
	if err != nil {
		return err
	}
	defer o.Zero()
	ring := masterkey.NewKeyring(o.CM)
	defer ring.Lock()
	if h != nil {
		h.mu.Lock()
		defer h.mu.Unlock()
	}
	keyed, err := s.settle(dir, id, o, ring)
	if err != nil {
		return err
	}
	if keyed {
		s.ck.install(id, o.CM, o.Check, o.Ending)
	} else {
		s.ck.drop(id)
	}
	s.logf("case %s unlocked", id)
	return nil
}

// LockCaseKey forgets the case's key and closes its open handle.
func (s *Store) LockCaseKey(id casestore.CaseID) error {
	if _, err := s.caseDir(id); err != nil {
		return err
	}
	s.closeHandle(id)
	s.ck.drop(id)
	return nil
}

// LockAllCaseKeys locks every case key held.
func (s *Store) LockAllCaseKeys() {
	for _, id := range s.ck.ids() {
		s.closeHandle(id)
		s.ck.drop(id)
	}
}

// closeHandle shuts the case's open handle, as Close does for every case.
func (s *Store) closeHandle(id casestore.CaseID) {
	s.mu.Lock()
	h := s.open[id]
	delete(s.open, id)
	s.mu.Unlock()
	if h != nil {
		_ = h.shutdown()
	}
}

// CaseKeyCheck is the full check value of an unlocked key.
func (s *Store) CaseKeyCheck(id casestore.CaseID) (string, error) {
	if e := s.ck.get(id); e != nil {
		return e.check, nil
	}
	dir, err := s.caseDir(id)
	if err != nil {
		return "", err
	}
	if !wrapExists(dir) {
		return "", casestore.ErrNoCaseKey
	}
	return "", casestore.ErrCaseLocked
}

// SetCaseKey gives a case that has none a key of its own.
func (s *Store) SetCaseKey(ctx context.Context, id casestore.CaseID, pass []byte) error {
	if err := casekey.CheckPassphrase(pass); err != nil {
		return ruleErr(err)
	}
	dir, release, h, err := s.keyedDir(ctx, id)
	if err != nil {
		return err
	}
	defer release()
	if wrapExists(dir) {
		return casestore.ErrHasCaseKey
	}
	if err := s.legacyGate(id, dir); err != nil {
		return err
	}
	if h != nil {
		h.mu.Lock()
		defer h.mu.Unlock()
	}
	oldKind := caseKind(dir)
	// The case must open now, under what seals it, and be at rest.
	m, err := readManifest(dir, s.sealer(dir, ""))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrDamaged
		}
		return err
	}
	if err := markGate(m.Mark); err != nil {
		return err
	}
	data, o, err := casekey.New(pass, s.keyParams(), string(id), nil, casekey.StateSetting)
	if err != nil {
		return ruleErr(err)
	}
	defer o.Zero()
	ring := masterkey.NewKeyring(o.CM)
	defer ring.Lock()
	if err := s.ckStep("set:wrap"); err != nil {
		return err
	}
	if err := writeWrapFile(dir, data); err != nil {
		return err
	}
	if err := s.resealFiles(dir, id, ring, casestore.KindCaseKey, "set"); err != nil {
		if isCut(err) {
			return err
		}
		s.rollback(dir, id, ring, oldKind)
		return err
	}
	if err := s.ckStep("set:active"); err != nil {
		return err
	}
	active, err := o.Seal(casekey.StateActive)
	if err != nil {
		s.rollback(dir, id, ring, oldKind)
		return err
	}
	if err := writeWrapFile(dir, active); err != nil {
		s.rollback(dir, id, ring, oldKind)
		return err
	}
	s.ck.install(id, o.CM, o.Check, o.Ending)
	if h != nil {
		h.man = m
	}
	s.logf("case %s given its own key", id)
	return nil
}

// isCut reports an error that stands for the process dying at a test hook: nothing
// is rolled back, so the next unlock has to finish the move.
func isCut(err error) bool { return errors.Is(err, errCut) }

var errCut = errors.New("cut for test")

// rollback puts a move that failed halfway back to the kind the case had and
// removes the wrap. Best effort: if it fails too, the wrap stays in state
// "setting" and the next unlock with the new key finishes the move forward.
func (s *Store) rollback(dir string, id casestore.CaseID, ring *masterkey.Keyring, oldKind string) {
	if err := s.resealFiles(dir, id, ring, oldKind, "rollback"); err != nil {
		s.logf("case %s key change could not be undone", id)
		return
	}
	_ = os.Remove(filepath.Join(dir, wrapName))
}

// ChangeCaseKey replaces the case's key. Only case.wrap is rewritten.
func (s *Store) ChangeCaseKey(ctx context.Context, id casestore.CaseID, cur, next []byte) error {
	if err := casekey.CheckPassphrase(next); err != nil {
		return ruleErr(err)
	}
	dir, release, h, err := s.keyedDir(ctx, id)
	if err != nil {
		return err
	}
	defer release()
	o, err := s.openWrap(dir, id, cur)
	if err != nil {
		return err
	}
	defer o.Zero()
	ring := masterkey.NewKeyring(o.CM)
	defer ring.Lock()
	if h != nil {
		h.mu.Lock()
		defer h.mu.Unlock()
	}
	keyed, err := s.settle(dir, id, o, ring)
	if err != nil {
		return err
	}
	if !keyed {
		return casestore.ErrNoCaseKey
	}
	data, n, err := casekey.New(next, s.keyParams(), string(id), o.CM, casekey.StateActive)
	if err != nil {
		return ruleErr(err)
	}
	defer n.Zero()
	if err := s.ckStep("change:wrap"); err != nil {
		return err
	}
	if err := writeWrapFile(dir, data); err != nil {
		return err
	}
	s.ck.install(id, n.CM, n.Check, n.Ending)
	s.logf("case %s key changed", id)
	return nil
}

// RemoveCaseKey puts the case back under the PC key (or Windows without one).
func (s *Store) RemoveCaseKey(ctx context.Context, id casestore.CaseID, cur []byte) error {
	dir, release, h, err := s.keyedDir(ctx, id)
	if err != nil {
		return err
	}
	defer release()
	o, err := s.openWrap(dir, id, cur)
	if err != nil {
		return err
	}
	defer o.Zero()
	ring := masterkey.NewKeyring(o.CM)
	defer ring.Lock()
	if h != nil {
		h.mu.Lock()
		defer h.mu.Unlock()
	}
	keyed, err := s.settle(dir, id, o, ring)
	if err != nil {
		return err
	}
	if !keyed {
		s.ck.drop(id)
		return nil
	}
	m, err := readManifest(dir, fileSealer{s: s, dir: dir, look: s.lookupWith(ring)})
	if err != nil {
		return err
	}
	if err := markGate(m.Mark); err != nil {
		return err
	}
	// Refuse before writing anything when the destination cannot be sealed now.
	to := s.newKind()
	if s.isLocked(to) {
		return ErrLocked
	}
	data, err := o.Seal(casekey.StateRemoving)
	if err != nil {
		return err
	}
	if err := s.ckStep("remove:wrap"); err != nil {
		return err
	}
	if err := writeWrapFile(dir, data); err != nil {
		return err
	}
	if err := s.resealFiles(dir, id, ring, to, "remove"); err != nil {
		if isCut(err) {
			return err
		}
		// Forward is the only safe way: go back to keyed so the case is not left
		// half way. Unlocking with the same key finishes the move either way.
		if back, sErr := o.Seal(casekey.StateSetting); sErr == nil {
			_ = writeWrapFile(dir, back)
			if s.resealFiles(dir, id, ring, casestore.KindCaseKey, "rollback") == nil {
				if act, aErr := o.Seal(casekey.StateActive); aErr == nil {
					_ = writeWrapFile(dir, act)
				}
			}
		}
		return err
	}
	if err := s.ckStep("remove:done"); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(dir, wrapName)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	s.ck.drop(id)
	if h != nil {
		h.man = m
	}
	s.logf("case %s key removed", id)
	return nil
}

// CreateWithKey is CreateFor for a case sealed under its own key.
func (s *Store) CreateWithKey(ctx context.Context, title, requestID string, pass []byte) (casestore.CaseInfo, bool, error) {
	if err := casekey.CheckPassphrase(pass); err != nil {
		return casestore.CaseInfo{}, false, ruleErr(err)
	}
	return s.createFor(ctx, title, requestID, pass)
}

// ownKey and keyEnding report an open handle's case key from the unlocked set. A
// handle only exists while its key is unlocked, so a missing entry means no key.
func (c *caseState) ownKey() bool { return c.s.ck.get(c.id) != nil }

func (c *caseState) keyEnding() string {
	if e := c.s.ck.get(c.id); e != nil {
		return e.ending
	}
	return ""
}
