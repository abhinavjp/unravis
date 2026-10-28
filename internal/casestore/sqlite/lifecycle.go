package sqlite

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/sealedfile"
)

var _ casestore.Lifecycle = (*Store)(nil)

// appendHistory adds one entry and keeps the newest casestore.MaxHistory.
func appendHistory(h []manifestHistory, at time.Time, kind string) []manifestHistory {
	return appendHistoryDetail(h, at, kind, "")
}

func historyOf(m manifest) []casestore.HistoryEntry {
	if len(m.History) == 0 {
		return nil
	}
	out := make([]casestore.HistoryEntry, len(m.History))
	for i, e := range m.History {
		out[i] = casestore.HistoryEntry{At: e.At, Kind: e.Kind, Detail: e.Detail}
	}
	return out
}

// mutate runs fn on the case's on-disk manifest under the folder lock (the same
// lock Import and CloseCase take, so two moves of one case never interleave, in
// this process or another). fn returns whether it changed the manifest; a change
// is sealed and written, and an open handle's cached copy is replaced.
func (s *Store) mutate(ctx context.Context, id casestore.CaseID, fn func(m *manifest) (bool, error)) error {
	return s.mutateH(ctx, id, func(m *manifest, _ *caseState) (bool, error) { return fn(m) })
}

// mutateH is mutate for a move that needs to know whether the case is open in
// this process: fn gets the open state (nil when no handle exists), whose lock is
// held, so its fields can be read.
func (s *Store) mutateH(ctx context.Context, id casestore.CaseID, fn func(m *manifest, h *caseState) (bool, error)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	dir, err := s.caseDir(id)
	if err != nil {
		return err
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return casestore.ErrNotFound
	}
	release, h, erasing, err := s.lockCase(dir, id)
	if err != nil {
		return err
	}
	defer release()
	if erasePending(dir) {
		return casestore.ErrNotFound
	}
	if erasing {
		return casestore.ErrNotFound
	}
	if k := caseKind(dir); k == casestore.KindMasterKey && s.isLocked(k) {
		return ErrLocked
	}
	if err := s.legacyGate(id, dir); err != nil {
		return err
	}
	m, err := readManifest(dir, s.sealer(dir, ""))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrDamaged
		}
		return err
	}
	if h != nil {
		h.mu.Lock()
		defer h.mu.Unlock()
	}
	changed, err := fn(&m, h)
	if err != nil || !changed {
		return err
	}
	if err := s.writeMan(dir, m); err != nil {
		return err
	}
	if h != nil {
		h.man = m
	}
	return nil
}

// ReopenCase clears the Closed stamp (casestore.Lifecycle). It is allowed from
// Closed only.
func (s *Store) ReopenCase(ctx context.Context, id casestore.CaseID) error {
	err := s.mutate(ctx, id, func(m *manifest) (bool, error) {
		if err := casestore.CheckMark(m.Mark); err != nil {
			return false, err
		}
		if err := casestore.CheckBelief(casestore.ActionReopen, "", stateOfManifest(*m)); err != nil {
			return false, err
		}
		m.Closed = nil
		m.History = appendHistory(m.History, s.now(), casestore.HistoryReopened)
		return true, nil
	})
	if err == nil {
		s.logf("case %s reopened", id)
	}
	return err
}

func stateOfManifest(m manifest) casestore.State {
	info := casestore.CaseInfo{Closed: m.Closed, Mark: m.Mark}
	if archived(m) {
		info.Archive = &casestore.ArchiveInfo{Active: true}
	}
	return casestore.StateOf(info)
}

// SetMark stamps the case; "" clears the stamp.
func (s *Store) SetMark(ctx context.Context, id casestore.CaseID, mark string) error {
	switch mark {
	case "", casestore.MarkArchiving, casestore.MarkFreeing, casestore.MarkErasing:
	default:
		return fmt.Errorf("%w: unknown mark", ErrInvalid)
	}
	return s.mutate(ctx, id, func(m *manifest) (bool, error) {
		if m.Mark == mark {
			return false, nil
		}
		m.Mark = mark
		return true, nil
	})
}

// Mark reads the stamp.
func (s *Store) Mark(ctx context.Context, id casestore.CaseID) (string, error) {
	// The pending-erase marker is the Erasing mark. It is read from the folder, so
	// it answers with no key and no manifest.
	if dir, err := s.caseDir(id); err == nil && erasePending(dir) {
		return casestore.MarkErasing, nil
	}
	var mark string
	err := s.mutate(ctx, id, func(m *manifest) (bool, error) {
		mark = m.Mark
		return false, nil
	})
	return mark, err
}

// CreateFor makes a case once per request id (casestore.Lifecycle). The scan and
// the create are one step under createMu, so two presses of Create with the same
// id make one case; a restart is covered because the id is in the manifest.
func (s *Store) CreateFor(ctx context.Context, title, requestID string) (casestore.CaseInfo, bool, error) {
	return s.createFor(ctx, title, requestID, nil)
}

// createFor is CreateFor, sealing the new case under its own key when pass is not nil.
func (s *Store) createFor(ctx context.Context, title, requestID string, pass []byte) (casestore.CaseInfo, bool, error) {
	if requestID == "" {
		info, err := s.createWith(ctx, title, "", pass)
		return info, false, err
	}
	if !casestore.ValidRequestID(requestID) {
		return casestore.CaseInfo{}, false, fmt.Errorf("%w: bad request id", ErrInvalid)
	}
	s.createMu.Lock()
	defer s.createMu.Unlock()
	all, err := s.List(ctx)
	if err != nil {
		return casestore.CaseInfo{}, false, err
	}
	for _, info := range all {
		if info.RequestID != requestID {
			continue
		}
		if casestore.StateOf(info) == casestore.StateArchiving || info.Mark == casestore.MarkFreeing || info.Mark == casestore.MarkErasing {
			return casestore.CaseInfo{}, false, casestore.ErrAlreadyMade
		}
		return info, true, nil
	}
	info, err := s.createWith(ctx, title, requestID, pass)
	return info, false, err
}

// lockCase takes the case folder's lock and, with it held, reads the open state
// of the case under s.mu. Open holds s.mu while it loads (and so waits for the
// folder lock), so waiting for s.mu while holding the folder lock could deadlock
// a Close that races a cold Open: s.mu is only tried, and when Open has it the
// folder lock is let go and taken again. s.mu is never held while waiting for the
// folder lock, so an import that holds the folder lock for its whole run stalls
// that case's Close or Reopen but no other call of the store. With the folder
// lock held no Open can finish its load, so the two reads agree.
func (s *Store) lockCase(dir string, id casestore.CaseID) (release func(), h *caseState, erasing bool, err error) {
	deadline := time.Now().Add(lockCaseWait)
	for {
		release, err = sealedfile.Lock(dir)
		if err != nil {
			return nil, nil, false, err
		}
		if s.mu.TryLock() {
			h, erasing = s.open[id], s.erasing[id]
			s.mu.Unlock()
			return release, h, erasing, nil
		}
		release()
		if time.Now().After(deadline) {
			return nil, nil, false, sealedfile.ErrUnavailable
		}
		time.Sleep(time.Millisecond)
	}
}

// lockCaseWait bounds how long lockCase keeps trying while another call holds s.mu.
const lockCaseWait = 5 * time.Second
