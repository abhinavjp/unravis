package sqlite

import (
	"time"

	"api-audit-explorer/internal/casestore"
)

// The archive record as the sealed manifest holds it (P3). The types mirror
// casestore.ArchiveInfo with JSON names, so the file shape does not depend on the
// Go shape of the interface.

type manifestArch struct {
	Active          bool                 `json:"active,omitempty"`
	At              *time.Time           `json:"at,omitempty"`
	ClosedAtArchive *time.Time           `json:"closedAtArchive,omitempty"`
	FreedAt         *time.Time           `json:"freedAt,omitempty"`
	Files           []manifestArchFile   `json:"files,omitempty"`
	Restores        []manifestRestore    `json:"restores,omitempty"`
	Pending         *manifestArchPending `json:"pending,omitempty"`
}

type manifestArchFile struct {
	Name     string    `json:"name"`
	Folder   string    `json:"folder"`
	Bytes    int64     `json:"bytes"`
	SHA256   string    `json:"sha256"`
	KeyKind  string    `json:"keyKind"`
	KeyCheck string    `json:"keyCheck,omitempty"`
	Made     time.Time `json:"made"`
	Imports  int64     `json:"imports"`
	Sources  int64     `json:"sources"`
	Events   int64     `json:"events"`
}

type manifestRestore struct {
	At   time.Time `json:"at"`
	Code string    `json:"code"`
}

type manifestArchPending struct {
	Folder     string    `json:"folder"`
	Name       string    `json:"name"`
	Started    time.Time `json:"started"`
	SamplerWas bool      `json:"samplerWas,omitempty"`
}

func (a *manifestArch) info() *casestore.ArchiveInfo {
	if a == nil {
		return nil
	}
	out := &casestore.ArchiveInfo{
		Active:          a.Active,
		At:              copyTimePtr(a.At),
		ClosedAtArchive: copyTimePtr(a.ClosedAtArchive),
		FreedAt:         copyTimePtr(a.FreedAt),
	}
	for _, f := range a.Files {
		out.Files = append(out.Files, casestore.ArchiveFile{
			Name: f.Name, Folder: f.Folder, Bytes: f.Bytes, SHA256: f.SHA256, KeyKind: f.KeyKind,
			KeyCheck: f.KeyCheck, Made: f.Made,
			Counts: casestore.Counts{Imports: f.Imports, Sources: f.Sources, Events: f.Events},
		})
	}
	for _, r := range a.Restores {
		out.Restores = append(out.Restores, casestore.RestoreLine{At: r.At, Code: r.Code})
	}
	if a.Pending != nil {
		out.Pending = &casestore.ArchivePending{
			Folder: a.Pending.Folder, Name: a.Pending.Name, Started: a.Pending.Started, SamplerWas: a.Pending.SamplerWas,
		}
	}
	return out
}

func (a *manifestArch) clone() *manifestArch {
	if a == nil {
		return nil
	}
	c := *a
	c.At, c.ClosedAtArchive, c.FreedAt = copyTimePtr(a.At), copyTimePtr(a.ClosedAtArchive), copyTimePtr(a.FreedAt)
	c.Files = append([]manifestArchFile(nil), a.Files...)
	c.Restores = append([]manifestRestore(nil), a.Restores...)
	if a.Pending != nil {
		p := *a.Pending
		c.Pending = &p
	}
	return &c
}

func copyTimePtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	c := *t
	return &c
}

func fileToManifest(f casestore.ArchiveFile) manifestArchFile {
	return manifestArchFile{
		Name: f.Name, Folder: f.Folder, Bytes: f.Bytes, SHA256: f.SHA256, KeyKind: f.KeyKind,
		KeyCheck: f.KeyCheck, Made: f.Made.UTC().Truncate(time.Millisecond),
		Imports: f.Counts.Imports, Sources: f.Counts.Sources, Events: f.Counts.Events,
	}
}
