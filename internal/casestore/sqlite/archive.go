package sqlite

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/sealedfile"
)

var _ casestore.Archiver = (*Store)(nil)

func appendHistoryDetail(h []manifestHistory, at time.Time, kind, detail string) []manifestHistory {
	h = append(h, manifestHistory{At: at.UTC().Truncate(time.Millisecond), Kind: kind, Detail: detail})
	if len(h) > casestore.MaxHistory {
		h = append([]manifestHistory(nil), h[len(h)-casestore.MaxHistory:]...)
	}
	return h
}

func archived(m manifest) bool { return m.Arch != nil && m.Arch.Active }
func freed(m manifest) bool    { return archived(m) && m.Arch.FreedAt != nil }

// arch returns the record to change, making one if the case has none.
func (m *manifest) arch() *manifestArch {
	if m.Arch == nil {
		m.Arch = &manifestArch{}
	}
	return m.Arch
}

// tidyArch drops an empty record so a case that was never archived stays as it was.
func (m *manifest) tidyArch() {
	if a := m.Arch; a != nil && !a.Active && a.Pending == nil && len(a.Files) == 0 && len(a.Restores) == 0 {
		m.Arch = nil
	}
}

// listRow is the row List shows for the case folder dir (a case that has one).
func (s *Store) listRow(id casestore.CaseID, dir string) casestore.CaseInfo {
	info := casestore.CaseInfo{ID: id, Title: "(unreadable case)"}
	if erasePending(dir) {
		info.Title = "(erase pending)"
		return info
	}
	if err := s.legacyGate(id, dir); errors.Is(err, casestore.ErrUntrustedLegacy) {
		// Listed, so the person can see and erase it, but nothing of it is read.
		info.Title, info.Untrusted = "(untrusted case)", true
		return info
	}
	if m, err := readManifest(dir, s.sealer(dir, "")); err == nil && m.CaseID == string(id) {
		info.Title, info.Created, info.Closed, info.Imports = m.Title, m.Created, m.Closed, len(m.Imports)
		info.Bytes = manifestBytes(m)
		info.Mark, info.RequestID = m.Mark, m.RequestID
		info.Archive = m.Arch.info()
	} else if err != nil && caseKind(dir) == casestore.KindMasterKey && s.isLocked(casestore.KindMasterKey) {
		info.Title = "(locked)" // nothing else about a locked case is shown
	}
	return info
}

// Entry reads the record of a case in any state without opening its database.
func (s *Store) Entry(ctx context.Context, id casestore.CaseID) (casestore.CaseInfo, error) {
	if err := ctx.Err(); err != nil {
		return casestore.CaseInfo{}, err
	}
	dir, err := s.caseDir(id)
	if err != nil {
		return casestore.CaseInfo{}, err
	}
	if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
		info := s.listRow(id, dir)
		if m, err := readManifest(dir, s.sealer(dir, "")); err == nil && m.CaseID == string(id) {
			info.History = historyOf(m)
		}
		if erasePending(dir) {
			info.Mark = casestore.MarkErasing
		}
		return info, nil
	}
	rec, err := s.readIndex(id)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return casestore.CaseInfo{}, casestore.ErrNotFound
		}
		return casestore.CaseInfo{}, err
	}
	info := rec.info()
	if info.Archive == nil || !info.Archive.Freed() {
		info.Missing = true
	}
	return info, nil
}

// BeginArchive stamps MarkArchiving with the pending facts, in one manifest write.
func (s *Store) BeginArchive(ctx context.Context, id casestore.CaseID, believed casestore.State, p casestore.ArchivePending) (casestore.State, error) {
	var prior casestore.State
	err := s.mutate(ctx, id, func(m *manifest) (bool, error) {
		if err := casestore.CheckMark(m.Mark); err != nil {
			return false, err
		}
		state := stateOfManifest(*m)
		if err := casestore.CheckBelief(casestore.ActionArchive, believed, state); err != nil {
			return false, err
		}
		prior = state
		a := m.arch()
		a.Pending = &manifestArchPending{Folder: p.Folder, Name: p.Name, Started: p.Started.UTC().Truncate(time.Millisecond), SamplerWas: p.SamplerWas}
		m.Mark = casestore.MarkArchiving
		return true, nil
	})
	if err == nil {
		s.logf("case %s archive started", id)
	}
	return prior, err
}

// CommitArchive is the one commit point of an archive.
func (s *Store) CommitArchive(ctx context.Context, id casestore.CaseID, f casestore.ArchiveFile) error {
	err := s.mutate(ctx, id, func(m *manifest) (bool, error) {
		if m.Mark != casestore.MarkArchiving {
			return false, casestore.ErrCaseChanged
		}
		now := s.now()
		a := m.arch()
		a.Files = append(a.Files, fileToManifest(f))
		if len(a.Files) > casestore.MaxArchiveFiles {
			a.Files = append([]manifestArchFile(nil), a.Files[len(a.Files)-casestore.MaxArchiveFiles:]...)
		}
		a.Active, a.FreedAt, a.Pending = true, nil, nil
		a.At = &now
		a.ClosedAtArchive = copyTimePtr(m.Closed)
		m.Mark = ""
		m.History = appendHistoryDetail(m.History, now, casestore.HistoryArchived, f.Name)
		return true, nil
	})
	if err == nil {
		s.logf("case %s archived", id)
	}
	return err
}

// AbortArchive returns a case that is still marked Archiving to the state it was in.
func (s *Store) AbortArchive(ctx context.Context, id casestore.CaseID, stopped bool) error {
	err := s.mutate(ctx, id, func(m *manifest) (bool, error) {
		if m.Mark != casestore.MarkArchiving {
			return false, casestore.ErrCaseChanged
		}
		m.Mark = ""
		if m.Arch != nil {
			m.Arch.Pending = nil
		}
		m.tidyArch()
		if stopped {
			m.History = appendHistoryDetail(m.History, s.now(), casestore.HistoryArchiveStopped, "")
		}
		return true, nil
	})
	if err == nil {
		s.logf("case %s archive stopped", id)
	}
	return err
}

// RestoreInPlace turns a kept Archived case into a Closed one, closed now.
func (s *Store) RestoreInPlace(ctx context.Context, id casestore.CaseID, believed casestore.State) error {
	err := s.mutate(ctx, id, func(m *manifest) (bool, error) {
		if err := casestore.CheckMark(m.Mark); err != nil {
			return false, err
		}
		if err := casestore.CheckBelief(casestore.ActionRestore, believed, stateOfManifest(*m)); err != nil {
			return false, err
		}
		if freed(*m) {
			return false, casestore.ErrCaseChanged
		}
		now := s.now()
		m.Arch.Active = false
		m.Closed = &now
		m.History = appendHistoryDetail(m.History, now, casestore.HistoryRestored, "")
		return true, nil
	})
	if err == nil {
		s.logf("case %s restored in place", id)
	}
	return err
}

// BeginFree stamps MarkFreeing after checking the case is Archived, kept and idle.
func (s *Store) BeginFree(ctx context.Context, id casestore.CaseID, believed casestore.State) error {
	return s.mutateH(ctx, id, func(m *manifest, h *caseState) (bool, error) {
		if err := casestore.CheckMark(m.Mark); err != nil {
			return false, err
		}
		if err := casestore.CheckBelief(casestore.ActionFree, believed, stateOfManifest(*m)); err != nil {
			return false, err
		}
		if freed(*m) {
			return false, casestore.ErrCaseChanged
		}
		if h != nil && h.refs > 0 {
			return false, casestore.ErrCaseInUse
		}
		m.Mark = casestore.MarkFreeing
		return true, nil
	})
}

// FinishFree removes the working copy, then keeps the entry as Freed.
func (s *Store) FinishFree(ctx context.Context, id casestore.CaseID) error {
	err := s.mutateH(ctx, id, func(m *manifest, h *caseState) (bool, error) {
		if freed(*m) && m.Mark == "" {
			return false, nil // already done
		}
		if m.Mark != casestore.MarkFreeing || !archived(*m) {
			return false, casestore.ErrCaseChanged
		}
		if h != nil && h.refs > 0 {
			return false, casestore.ErrCaseInUse
		}
		dir, err := s.caseDir(id)
		if err != nil {
			return false, err
		}
		if err := removeWorkingCopy(dir); err != nil {
			return false, err
		}
		now := s.now()
		m.Arch.FreedAt = &now
		m.Mark = ""
		m.History = appendHistoryDetail(m.History, now, casestore.HistoryFreed, "")
		return true, nil
	})
	if err == nil {
		s.logf("case %s freed", id)
	}
	return err
}

// removeWorkingCopy deletes the working copy of a case and keeps manifest.dat. The
// key goes first, so a failure after it leaves only ciphertext. It is safe to run
// again and never touches a case file outside the folder.
func removeWorkingCopy(dir string) error {
	var errs []error
	if err := casestore.EraseKeyFile(filepath.Join(dir, keyName)); err != nil {
		errs = append(errs, errors.New("destroying the case key failed"))
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return errors.Join(append(errs, err)...)
	}
	for _, e := range entries {
		switch e.Name() {
		case manifestName, keyName, eraseMarker:
			continue
		}
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// AddRestoreLine adds one line to the restores list.
func (s *Store) AddRestoreLine(ctx context.Context, id casestore.CaseID, l casestore.RestoreLine) error {
	return s.mutate(ctx, id, func(m *manifest) (bool, error) {
		a := m.arch()
		a.Restores = append(a.Restores, manifestRestore{At: l.At.UTC().Truncate(time.Millisecond), Code: l.Code})
		if len(a.Restores) > casestore.MaxRestoreLines {
			a.Restores = append([]manifestRestore(nil), a.Restores[len(a.Restores)-casestore.MaxRestoreLines:]...)
		}
		return true, nil
	})
}

// AddHistory adds a story entry.
func (s *Store) AddHistory(ctx context.Context, id casestore.CaseID, kind, detail string) error {
	return s.mutate(ctx, id, func(m *manifest) (bool, error) {
		m.History = appendHistoryDetail(m.History, s.now(), kind, detail)
		return true, nil
	})
}

// Unresolved lists the finished case files that were never committed.
func (s *Store) Unresolved(ctx context.Context) ([]casestore.UnresolvedFile, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	release, err := sealedfile.Lock(s.indexDir())
	if err != nil {
		return nil, err
	}
	defer release()
	d, err := s.readUnresolved()
	if err != nil {
		return nil, err
	}
	out := make([]casestore.UnresolvedFile, 0, len(d.Files))
	for _, f := range d.Files {
		out = append(out, casestore.UnresolvedFile{Name: f.Name, Folder: f.Folder, CaseID: casestore.CaseID(f.CaseID), At: f.At})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out, nil
}

// AddUnresolved records one file; the same name and folder is not added twice.
func (s *Store) AddUnresolved(ctx context.Context, f casestore.UnresolvedFile) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	release, err := sealedfile.Lock(s.indexDir())
	if err != nil {
		return err
	}
	defer release()
	d, err := s.readUnresolved()
	if err != nil {
		return err
	}
	for _, e := range d.Files {
		if e.Name == f.Name && e.Folder == f.Folder {
			return nil
		}
	}
	d.Files = append(d.Files, unresolvedDocEntry{Name: f.Name, Folder: f.Folder, CaseID: string(f.CaseID), At: f.At.UTC().Truncate(time.Millisecond)})
	if len(d.Files) > casestore.MaxUnresolvedFiles {
		d.Files = append([]unresolvedDocEntry(nil), d.Files[len(d.Files)-casestore.MaxUnresolvedFiles:]...)
	}
	return s.writeUnresolved(d)
}

// DropUnresolved forgets one entry. The file itself is never touched.
func (s *Store) DropUnresolved(ctx context.Context, name, folder string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	release, err := sealedfile.Lock(s.indexDir())
	if err != nil {
		return err
	}
	defer release()
	d, err := s.readUnresolved()
	if err != nil {
		return err
	}
	kept := d.Files[:0:0]
	for _, e := range d.Files {
		if e.Name != name || e.Folder != folder {
			kept = append(kept, e)
		}
	}
	if len(kept) == len(d.Files) {
		return nil
	}
	d.Files = kept
	return s.writeUnresolved(d)
}

// sweepSkips says whether Sweep must leave a row alone whatever its age: an
// archived case (kept or freed) and one mid-move are never swept, and a Missing
// row has no folder to erase.
func sweepSkips(info casestore.CaseInfo) bool {
	if info.Missing || info.Mark == casestore.MarkArchiving || info.Mark == casestore.MarkFreeing {
		return true
	}
	return info.Archive != nil && (info.Archive.Active || info.Archive.Pending != nil)
}
