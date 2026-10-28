package sqlite

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/sealedfile"
)

// The case index (P3, spec 7.7). Each case keeps a small sealed record of itself in
// <root>\.index\<id>.dat, written whenever its manifest is written and removed only
// when the case is erased. Its one job is to remember a case after its folder is
// gone, so List can show it as Missing, or, for a freed case, as Archived freed.
// The manifest stays the truth while the folder exists: the index is read only
// when there is no folder, never to answer a question about a case that has one.

const (
	indexDirName   = ".index"
	indexMax       = 1 << 20
	indexVersion   = 1
	unresolvedName = "unresolved.dat"
)

type indexRecord struct {
	V         int               `json:"v"`
	CaseID    string            `json:"caseId"`
	Title     string            `json:"title"`
	Created   time.Time         `json:"created"`
	Closed    *time.Time        `json:"closed,omitempty"`
	Imports   int               `json:"imports"`
	Bytes     int64             `json:"bytes"`
	RequestID string            `json:"requestId,omitempty"`
	History   []manifestHistory `json:"history,omitempty"`
	Arch      *manifestArch     `json:"arch,omitempty"`
}

func (s *Store) indexDir() string { return filepath.Join(s.root, indexDirName) }

func (s *Store) indexPath(id casestore.CaseID) string {
	return filepath.Join(s.indexDir(), string(id)+".dat")
}

// writeMan writes the case's manifest and then its index record. The manifest
// write is the one that counts; the index is best effort and a failure is not
// reported (the next write repairs it).
func (s *Store) writeMan(dir string, m manifest) error {
	if err := writeManifest(dir, s.sealer(dir, ""), m); err != nil {
		return err
	}
	s.writeIndex(dir, m)
	return nil
}

func (s *Store) writeIndex(dir string, m manifest) {
	id := casestore.CaseID(m.CaseID)
	if !validID(id) {
		return
	}
	rec := indexRecord{V: indexVersion, CaseID: m.CaseID, Title: m.Title, Created: m.Created, Closed: m.Closed,
		Imports: len(m.Imports), Bytes: manifestBytes(m), RequestID: m.RequestID, History: m.History, Arch: m.Arch.clone()}
	plain, err := json.Marshal(rec)
	if err != nil {
		return
	}
	defer clear(plain)
	sealed, err := s.sealer(dir, "").Protect(plain)
	if err != nil {
		return
	}
	// A concurrent removeIndex may take the empty folder between the two steps, so
	// the pair is tried twice.
	for try := 0; try < 2; try++ {
		if err := os.MkdirAll(s.indexDir(), 0o700); err != nil {
			return
		}
		if sealedfile.WriteAtomic(s.indexPath(id), "idx.*.tmp", sealed) == nil {
			return
		}
	}
}

// readIndex opens one index record. The sealer picks the protector from the kind
// recorded in the file, so it works without the case folder.
func (s *Store) readIndex(id casestore.CaseID) (indexRecord, error) {
	path := s.indexPath(id)
	fi, err := os.Lstat(path)
	if err != nil {
		return indexRecord{}, err
	}
	if !fi.Mode().IsRegular() || fi.Size() == 0 || fi.Size() > indexMax {
		return indexRecord{}, ErrDamaged
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return indexRecord{}, err
	}
	plain, err := s.sealer("", "").Unprotect(raw)
	if err != nil {
		return indexRecord{}, err
	}
	defer clear(plain)
	var rec indexRecord
	if err := json.Unmarshal(plain, &rec); err != nil || rec.V != indexVersion || rec.CaseID != string(id) {
		return indexRecord{}, ErrDamaged
	}
	return rec, nil
}

func (s *Store) removeIndex(id casestore.CaseID) {
	if validID(id) {
		_ = os.Remove(s.indexPath(id))
		// The folder goes with its last record, so an empty store has an empty root.
		// It fails, harmlessly, while any record or the unresolved list is in it.
		_ = os.Remove(s.indexDir())
	}
}

func (r indexRecord) info() casestore.CaseInfo {
	m := manifest{History: r.History}
	return casestore.CaseInfo{
		ID: casestore.CaseID(r.CaseID), Title: r.Title, Created: r.Created, Closed: r.Closed,
		Imports: r.Imports, Bytes: r.Bytes, RequestID: r.RequestID, History: historyOf(m), Archive: r.Arch.info(),
	}
}

// indexedIDs lists the case ids that have an index record.
func (s *Store) indexedIDs() []casestore.CaseID {
	entries, err := os.ReadDir(s.indexDir())
	if err != nil {
		return nil
	}
	var out []casestore.CaseID
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".dat")
		if ok && !e.IsDir() && validID(casestore.CaseID(name)) {
			out = append(out, casestore.CaseID(name))
		}
	}
	return out
}

// missingRows adds, for every index record whose folder is not there, the row List
// shows: a freed case as Archived freed, any other case as Missing. A record that
// cannot be read (the master key is locked, the file is damaged) adds nothing: a
// row without a title would only mislead. A folder that exists, even a damaged
// one, is List's own row and not this one.
func (s *Store) missingRows(have map[casestore.CaseID]bool) []casestore.CaseInfo {
	var out []casestore.CaseInfo
	for _, id := range s.indexedIDs() {
		if have[id] {
			continue
		}
		if _, err := os.Lstat(filepath.Join(s.root, string(id))); err == nil {
			continue
		}
		rec, err := s.readIndex(id)
		if err != nil {
			continue
		}
		info := rec.info()
		if info.Archive != nil && info.Archive.Freed() {
			// A freed case is meant to have no folder worth showing.
			out = append(out, info)
			continue
		}
		info.Missing = true
		out = append(out, info)
	}
	return out
}

// Unresolved files: finished case files an archive left behind (spec 7.7). The
// list is sealed by Windows (it names files and folders on the person's disk, not
// anything in a case) and lives beside the index.

type unresolvedDoc struct {
	V     int                  `json:"v"`
	Files []unresolvedDocEntry `json:"files"`
}

type unresolvedDocEntry struct {
	Name   string    `json:"name"`
	Folder string    `json:"folder"`
	CaseID string    `json:"caseId,omitempty"`
	At     time.Time `json:"at"`
}

func (s *Store) unresolvedPath() string { return filepath.Join(s.indexDir(), unresolvedName) }

func (s *Store) readUnresolved() (unresolvedDoc, error) {
	raw, err := os.ReadFile(s.unresolvedPath())
	if errors.Is(err, os.ErrNotExist) {
		return unresolvedDoc{V: 1}, nil
	}
	if err != nil {
		return unresolvedDoc{}, err
	}
	if s.opt.Protector == nil {
		return unresolvedDoc{}, ErrLocked
	}
	if len(raw) > indexMax {
		return unresolvedDoc{}, ErrDamaged
	}
	plain, err := s.opt.Protector.Unprotect(raw)
	if err != nil {
		return unresolvedDoc{}, ErrDamaged
	}
	defer clear(plain)
	var d unresolvedDoc
	if err := json.Unmarshal(plain, &d); err != nil || d.V != 1 {
		return unresolvedDoc{}, ErrDamaged
	}
	return d, nil
}

func (s *Store) writeUnresolved(d unresolvedDoc) error {
	d.V = 1
	plain, err := json.Marshal(d)
	if err != nil {
		return err
	}
	defer clear(plain)
	if s.opt.Protector == nil {
		return ErrLocked
	}
	sealed, err := s.opt.Protector.Protect(plain)
	if err != nil {
		return errors.New("sealing the unresolved list failed")
	}
	return sealedfile.WriteAtomic(s.unresolvedPath(), "unres.*.tmp", sealed)
}
