package casestore

import (
	"context"
	"errors"
	"time"
)

// The archive record (P3, spec v7 sections 7 and 9). An archived case keeps one
// record: the case files made from it, the list of cases restored from them, and
// the in-flight facts of an archive that has not committed yet. It lives in the
// case's sealed manifest (sqlite) or in memory (the fake), never in the case file
// itself, and it survives Free and Restore in place so the story stays whole.

// History kinds P3 adds to created, closed and reopened.
const (
	HistoryArchived         = "archived"
	HistoryArchiveStopped   = "archiving-stopped"
	HistoryRestored         = "restored"
	HistoryRestoredFromFile = "restored-from-file"
	HistoryRestoredFromCase = "restored-from-case"
	HistoryFreed            = "freed"
)

// Key kinds of a case file: sealed under the PC's master key, or under a
// passphrase typed for that file and never stored.
const (
	KeyKindMaster = "master"
	KeyKindTyped  = "typed"
)

// Bounds on what the record keeps. The oldest entry goes first.
const (
	MaxArchiveFiles    = 200
	MaxRestoreLines    = 200
	MaxUnresolvedFiles = 200
)

// ErrArchived answers Open for a case whose working copy is archived: it can be
// read as a record (Entry) but never opened. It is a kind of ErrNotFound, so code
// that only knows "not found" treats it the same; the server adds M6.
var ErrArchived error = archivedError{}

type archivedError struct{}

func (archivedError) Error() string        { return "case is archived" }
func (archivedError) Is(target error) bool { return target == ErrNotFound }

// ErrCaseInUse is M4: the case is open somewhere (a handle is held), so its
// working copy cannot be removed now. Free retries until its wait runs out.
var ErrCaseInUse = errors.New("case is in use")

// ErrNotArchived is a case that has no archive record where one is needed.
var ErrNotArchived = errors.New("case is not archived")

// Counts is what a case holds, as the archive record states it, so Verify can
// compare the restored copy with it.
type Counts struct {
	Imports int64
	Sources int64
	Events  int64
}

// ArchiveFile is one case file made from a case.
type ArchiveFile struct {
	Name   string // file name, as made by Unravis
	Folder string // the folder as typed and resolved when it was made
	Bytes  int64
	SHA256 string // hex SHA-256 of the whole file
	// KeyKind says which key sealed it (KeyKindMaster or KeyKindTyped).
	KeyKind string
	// KeyCheck is the full key check value (hex); the screen shows its last 4
	// characters ("Key ending xxxx"). Empty for a file made before P3.
	KeyCheck string
	Made     time.Time
	Counts   Counts
}

// KeyEnding is the last four characters of the key check value, or "".
func (f ArchiveFile) KeyEnding() string {
	if len(f.KeyCheck) < 4 {
		return ""
	}
	return f.KeyCheck[len(f.KeyCheck)-4:]
}

// RestoreLine records one case restored as new from a case file of this case.
type RestoreLine struct {
	At   time.Time
	Code string // the short code of the new case
}

// ArchivePending is what an archive in flight writes before it starts, so a crash
// can be repaired: where the file was going, and what to put back.
type ArchivePending struct {
	Folder     string
	Name       string
	Started    time.Time
	SamplerWas bool // the sampler was running before the archive began
}

// ArchiveInfo is the whole record.
type ArchiveInfo struct {
	// Active is true while the case is Archived (kept or freed). Restore in place
	// clears it; the files and restore lines stay.
	Active bool
	// At is when the archive committed.
	At *time.Time
	// ClosedAtArchive is the case's closed time when it was archived (none if it
	// was Open). Restore in place sets Closed to the restore time and keeps this.
	ClosedAtArchive *time.Time
	// FreedAt is set once the working copy is removed.
	FreedAt  *time.Time
	Files    []ArchiveFile // oldest first
	Restores []RestoreLine // oldest first
	Pending  *ArchivePending
}

// Freed reports whether the working copy is gone.
func (a *ArchiveInfo) Freed() bool { return a != nil && a.Active && a.FreedAt != nil }

// Current is the case file of the archive in force, or nil.
func (a *ArchiveInfo) Current() *ArchiveFile {
	if a == nil || len(a.Files) == 0 {
		return nil
	}
	return &a.Files[len(a.Files)-1]
}

func (a *ArchiveInfo) clone() *ArchiveInfo {
	if a == nil {
		return nil
	}
	c := *a
	c.At = copyTime(a.At)
	c.ClosedAtArchive = copyTime(a.ClosedAtArchive)
	c.FreedAt = copyTime(a.FreedAt)
	c.Files = append([]ArchiveFile(nil), a.Files...)
	c.Restores = append([]RestoreLine(nil), a.Restores...)
	if a.Pending != nil {
		p := *a.Pending
		c.Pending = &p
	}
	return &c
}

// Clone returns a deep copy.
func (a *ArchiveInfo) Clone() *ArchiveInfo { return a.clone() }

func copyTime(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	c := *t
	return &c
}

// UnresolvedFile is a finished case file an archive left behind without
// committing. It is listed, never deleted and never adopted (spec 7.7, A13).
type UnresolvedFile struct {
	Name   string
	Folder string
	CaseID CaseID
	At     time.Time
}

// Archiver is implemented by a Store that keeps archive records. Like Lifecycle it
// is a separate interface, and both stores in this repository implement it.
//
// Every method that changes a case takes the case's lock, so it cannot interleave
// with another move of the same case. Belief arguments follow CheckBelief; "" is
// "no belief supplied".
type Archiver interface {
	// Entry reads the record of a case in any state without opening its database:
	// an Archived case, a Freed one (whose working copy is gone) and a Missing one
	// (Missing true, taken from the last record kept outside the folder). A case
	// with no record at all is ErrNotFound.
	Entry(ctx context.Context, id CaseID) (CaseInfo, error)
	// BeginArchive checks the belief and the marks, stamps MarkArchiving and writes
	// the pending facts, in one step. It returns the state the case was in (open or
	// closed). Archiving or Archived gives ErrArchiveRunning or ErrCaseChanged
	// (CheckBelief); a Freeing or Erasing mark gives ErrFreeing or ErrErasing.
	BeginArchive(ctx context.Context, id CaseID, believed State, p ArchivePending) (prior State, err error)
	// CommitArchive is the one commit point: still marked Archiving (else
	// ErrCaseChanged, which is what Cancel after recovery gets), it adds f, sets the
	// case Archived, clears the mark and the pending facts and writes "archived".
	CommitArchive(ctx context.Context, id CaseID, f ArchiveFile) error
	// AbortArchive returns a case that is still marked Archiving to the state it was
	// in: the mark and the pending facts go, and with stopped true the story gets
	// "archiving stopped". Not marked Archiving is ErrCaseChanged.
	AbortArchive(ctx context.Context, id CaseID, stopped bool) error
	// RestoreInPlace turns a kept Archived case into a Closed one, closed now, with
	// "restored" in the story. Freed gives ErrCaseChanged.
	RestoreInPlace(ctx context.Context, id CaseID, believed State) error
	// BeginFree checks the case is Archived with its working copy kept and not open
	// anywhere (else ErrCaseInUse), then stamps MarkFreeing: the commit point of
	// Free. FinishFree removes the working copy (the key first; the sealed record
	// stays), then marks the entry Freed and clears the mark. It is what recovery
	// runs after a crash, so it is safe to run again, and a case already Freed
	// answers nil.
	BeginFree(ctx context.Context, id CaseID, believed State) error
	FinishFree(ctx context.Context, id CaseID) error
	// AddRestoreLine adds one line to the restores list of an Archived or restored
	// case; a case that no longer exists is ErrNotFound and the caller ignores it.
	AddRestoreLine(ctx context.Context, id CaseID, l RestoreLine) error
	// AddHistory adds a story entry with a short detail.
	AddHistory(ctx context.Context, id CaseID, kind, detail string) error
	// Unresolved lists the finished case files that were never committed.
	Unresolved(ctx context.Context) ([]UnresolvedFile, error)
	// AddUnresolved records one; a name and folder already there is not added twice.
	AddUnresolved(ctx context.Context, f UnresolvedFile) error
	// DropUnresolved forgets one (the file is gone). It never touches the file.
	DropUnresolved(ctx context.Context, name, folder string) error
}
