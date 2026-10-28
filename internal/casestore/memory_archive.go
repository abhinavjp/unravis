package casestore

import (
	"context"
	"sort"
	"sync"
	"time"
)

var _ Archiver = (*memStore)(nil)

// The memory store keeps the archive record in CaseInfo.Archive. It cannot make a
// real case file (it has no database file), so the server's archive job runs only
// against the SQLite store; this side exists so the two stores answer the lifecycle
// and the record the same way, and so the demo can show archived cases.

type memArchive struct {
	mu         sync.Mutex
	unresolved []UnresolvedFile
}

func (s *memStore) arch() *memArchive {
	s.archOnce.Do(func() { s.archive = &memArchive{} })
	return s.archive
}

// mutateInfo runs fn on the case record under its lock; a change is whatever fn
// does to d.info.
func (s *memStore) mutateInfo(ctx context.Context, id CaseID, fn func(d *memData) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	d, err := s.get(id)
	if err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.erased {
		return ErrNotFound
	}
	return fn(d)
}

func isArchived(i CaseInfo) bool { return i.Archive != nil && i.Archive.Active }

func (i *CaseInfo) archiveRec() *ArchiveInfo {
	if i.Archive == nil {
		i.Archive = &ArchiveInfo{}
	}
	return i.Archive
}

func (i *CaseInfo) tidyArchive() {
	if a := i.Archive; a != nil && !a.Active && a.Pending == nil && len(a.Files) == 0 && len(a.Restores) == 0 {
		i.Archive = nil
	}
}

func appendHistoryDetail(h []HistoryEntry, at time.Time, kind, detail string) []HistoryEntry {
	h = append(h, HistoryEntry{At: at.UTC().Truncate(time.Millisecond), Kind: kind, Detail: detail})
	if len(h) > MaxHistory {
		h = append([]HistoryEntry(nil), h[len(h)-MaxHistory:]...)
	}
	return h
}

func (s *memStore) Entry(ctx context.Context, id CaseID) (CaseInfo, error) {
	if err := ctx.Err(); err != nil {
		return CaseInfo{}, err
	}
	d, err := s.get(id)
	if err != nil {
		return CaseInfo{}, err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	info := copyInfo(d.info)
	info.Missing = d.missing && !(info.Archive != nil && info.Archive.Freed())
	return info, nil
}

func (s *memStore) BeginArchive(ctx context.Context, id CaseID, believed State, p ArchivePending) (State, error) {
	var prior State
	err := s.mutateInfo(ctx, id, func(d *memData) error {
		if err := CheckMark(d.info.Mark); err != nil {
			return err
		}
		state := StateOf(d.info)
		if err := CheckBelief(ActionArchive, believed, state); err != nil {
			return err
		}
		prior = state
		p.Started = p.Started.UTC().Truncate(time.Millisecond)
		d.info.archiveRec().Pending = &p
		d.info.Mark = MarkArchiving
		return nil
	})
	return prior, err
}

func (s *memStore) CommitArchive(ctx context.Context, id CaseID, f ArchiveFile) error {
	return s.mutateInfo(ctx, id, func(d *memData) error {
		if d.info.Mark != MarkArchiving {
			return ErrCaseChanged
		}
		now := s.now().UTC()
		a := d.info.archiveRec()
		f.Made = f.Made.UTC().Truncate(time.Millisecond)
		a.Files = append(a.Files, f)
		if len(a.Files) > MaxArchiveFiles {
			a.Files = append([]ArchiveFile(nil), a.Files[len(a.Files)-MaxArchiveFiles:]...)
		}
		a.Active, a.FreedAt, a.Pending = true, nil, nil
		a.At = &now
		a.ClosedAtArchive = copyTime(d.info.Closed)
		d.info.Mark = ""
		d.info.History = appendHistoryDetail(d.info.History, now, HistoryArchived, f.Name)
		return nil
	})
}

func (s *memStore) AbortArchive(ctx context.Context, id CaseID, stopped bool) error {
	return s.mutateInfo(ctx, id, func(d *memData) error {
		if d.info.Mark != MarkArchiving {
			return ErrCaseChanged
		}
		d.info.Mark = ""
		if d.info.Archive != nil {
			d.info.Archive.Pending = nil
		}
		d.info.tidyArchive()
		if stopped {
			d.info.History = appendHistoryDetail(d.info.History, s.now(), HistoryArchiveStopped, "")
		}
		return nil
	})
}

func (s *memStore) RestoreInPlace(ctx context.Context, id CaseID, believed State) error {
	return s.mutateInfo(ctx, id, func(d *memData) error {
		if err := CheckMark(d.info.Mark); err != nil {
			return err
		}
		if err := CheckBelief(ActionRestore, believed, StateOf(d.info)); err != nil {
			return err
		}
		if d.info.Archive.Freed() {
			return ErrCaseChanged
		}
		now := s.now().UTC()
		d.info.Archive.Active = false
		d.info.Closed = &now
		d.info.History = appendHistoryDetail(d.info.History, now, HistoryRestored, "")
		return nil
	})
}

func (s *memStore) BeginFree(ctx context.Context, id CaseID, believed State) error {
	return s.mutateInfo(ctx, id, func(d *memData) error {
		if err := CheckMark(d.info.Mark); err != nil {
			return err
		}
		if err := CheckBelief(ActionFree, believed, StateOf(d.info)); err != nil {
			return err
		}
		if d.info.Archive.Freed() {
			return ErrCaseChanged
		}
		if d.refs > 0 {
			return ErrCaseInUse
		}
		d.info.Mark = MarkFreeing
		return nil
	})
}

func (s *memStore) FinishFree(ctx context.Context, id CaseID) error {
	return s.mutateInfo(ctx, id, func(d *memData) error {
		if d.info.Archive.Freed() && d.info.Mark == "" {
			return nil
		}
		if d.info.Mark != MarkFreeing || !isArchived(d.info) {
			return ErrCaseChanged
		}
		for i := range d.key {
			d.key[i] = 0
		}
		d.key, d.imports, d.events, d.ids, d.quarantines, d.disc = nil, nil, nil, nil, nil, nil
		d.envs, d.sources = nil, nil
		now := s.now().UTC()
		d.info.Archive.FreedAt = &now
		d.info.Mark = ""
		d.info.History = appendHistoryDetail(d.info.History, now, HistoryFreed, "")
		return nil
	})
}

func (s *memStore) AddRestoreLine(ctx context.Context, id CaseID, l RestoreLine) error {
	return s.mutateInfo(ctx, id, func(d *memData) error {
		a := d.info.archiveRec()
		l.At = l.At.UTC().Truncate(time.Millisecond)
		a.Restores = append(a.Restores, l)
		if len(a.Restores) > MaxRestoreLines {
			a.Restores = append([]RestoreLine(nil), a.Restores[len(a.Restores)-MaxRestoreLines:]...)
		}
		return nil
	})
}

func (s *memStore) AddHistory(ctx context.Context, id CaseID, kind, detail string) error {
	return s.mutateInfo(ctx, id, func(d *memData) error {
		d.info.History = appendHistoryDetail(d.info.History, s.now(), kind, detail)
		return nil
	})
}

func (s *memStore) Unresolved(ctx context.Context) ([]UnresolvedFile, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	a := s.arch()
	a.mu.Lock()
	defer a.mu.Unlock()
	out := append([]UnresolvedFile(nil), a.unresolved...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out, nil
}

func (s *memStore) AddUnresolved(ctx context.Context, f UnresolvedFile) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	a := s.arch()
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, e := range a.unresolved {
		if e.Name == f.Name && e.Folder == f.Folder {
			return nil
		}
	}
	f.At = f.At.UTC().Truncate(time.Millisecond)
	a.unresolved = append(a.unresolved, f)
	if len(a.unresolved) > MaxUnresolvedFiles {
		a.unresolved = append([]UnresolvedFile(nil), a.unresolved[len(a.unresolved)-MaxUnresolvedFiles:]...)
	}
	return nil
}

func (s *memStore) DropUnresolved(ctx context.Context, name, folder string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	a := s.arch()
	a.mu.Lock()
	defer a.mu.Unlock()
	kept := a.unresolved[:0:0]
	for _, e := range a.unresolved {
		if e.Name != name || e.Folder != folder {
			kept = append(kept, e)
		}
	}
	a.unresolved = kept
	return nil
}

// MemorySeeder is implemented by the memory store so the demo and tests can put a
// case into a state that real moves reach only through files: archived (kept or
// freed), or missing. It changes no real data and is not part of Store.
type MemorySeeder interface {
	// SeedArchived records that the case is Archived with the given record. A freed
	// record (FreedAt set) drops the working data, as Free does.
	SeedArchived(id CaseID, a ArchiveInfo) error
	// SeedMissing marks the case's folder as gone.
	SeedMissing(id CaseID) error
}

var _ MemorySeeder = (*memStore)(nil)

func (s *memStore) SeedArchived(id CaseID, a ArchiveInfo) error {
	return s.mutateInfo(context.Background(), id, func(d *memData) error {
		a.Active = true
		d.info.Archive = a.clone()
		if a.FreedAt != nil {
			d.key, d.imports, d.events, d.ids, d.quarantines, d.disc = nil, nil, nil, nil, nil, nil
			d.envs, d.sources = nil, nil
		}
		return nil
	})
}

func (s *memStore) SeedMissing(id CaseID) error {
	return s.mutateInfo(context.Background(), id, func(d *memData) error {
		d.missing = true
		return nil
	})
}
