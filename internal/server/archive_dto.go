package server

import (
	"time"

	"api-audit-explorer/internal/casestore"
)

// What the case list and the case view say about archive: the case files made,
// the cases restored from them, and when the working copy was freed. A key is
// never part of it; a file's key shows as "Key ending xxxx" (the last four
// characters of its key check value).

type countsDTO struct {
	Imports int64 `json:"imports"`
	Sources int64 `json:"sources"`
	Events  int64 `json:"events"`
}

type archiveFileDTO struct {
	Name      string    `json:"name"`
	Folder    string    `json:"folder"`
	Bytes     int64     `json:"bytes"`
	KeyKind   string    `json:"keyKind"`
	KeyEnding string    `json:"keyEnding,omitempty"`
	Made      time.Time `json:"made"`
	Counts    countsDTO `json:"counts"`
}

type restoreLineDTO struct {
	At   time.Time `json:"at"`
	Code string    `json:"code"`
}

type archiveDTO struct {
	// Active is true while the case is Archived.
	Active  bool       `json:"active"`
	At      *time.Time `json:"at,omitempty"`
	Freed   bool       `json:"freed"`
	FreedAt *time.Time `json:"freedAt,omitempty"`
	// Current is the case file of the archive in force.
	Current *archiveFileDTO `json:"current,omitempty"`
	// Files and Restores are the whole list (the case view only; the list of cases
	// carries the count and the current file).
	Files    []archiveFileDTO `json:"files,omitempty"`
	Restores []restoreLineDTO `json:"restores,omitempty"`
}

func toArchiveFileDTO(f casestore.ArchiveFile) archiveFileDTO {
	return archiveFileDTO{
		Name: f.Name, Folder: f.Folder, Bytes: f.Bytes, KeyKind: f.KeyKind, KeyEnding: f.KeyEnding(), Made: f.Made.UTC(),
		Counts: countsDTO{Imports: f.Counts.Imports, Sources: f.Counts.Sources, Events: f.Counts.Events},
	}
}

func utcPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

// toArchiveDTO describes the record. full adds the whole file and restore lists.
func toArchiveDTO(a *casestore.ArchiveInfo, full bool) *archiveDTO {
	if a == nil || (!a.Active && len(a.Files) == 0 && len(a.Restores) == 0) {
		return nil
	}
	d := &archiveDTO{Active: a.Active, At: utcPtr(a.At), Freed: a.Freed(), FreedAt: utcPtr(a.FreedAt)}
	if cur := a.Current(); cur != nil {
		f := toArchiveFileDTO(*cur)
		d.Current = &f
	}
	if full {
		d.Files = make([]archiveFileDTO, 0, len(a.Files))
		for _, f := range a.Files {
			d.Files = append(d.Files, toArchiveFileDTO(f))
		}
		d.Restores = make([]restoreLineDTO, 0, len(a.Restores))
		for _, l := range a.Restores {
			d.Restores = append(d.Restores, restoreLineDTO{At: l.At.UTC(), Code: l.Code})
		}
	}
	return d
}

func filesMade(a *casestore.ArchiveInfo) int {
	if a == nil {
		return 0
	}
	return len(a.Files)
}
