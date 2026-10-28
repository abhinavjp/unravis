package casestore

import (
	"context"
	"errors"
)

// Errors of the optional per-case key (decisions A12, A13). They carry no key
// material and say nothing about the case beyond what the caller already knows.
var (
	// ErrCaseLocked means the case has its own key and that key is not unlocked.
	// It is not ErrLocked: the master key being locked is a different screen and a
	// different answer. A master key reset or Lock-all never makes a case "unlocked".
	ErrCaseLocked = errors.New("case key is locked")
	// ErrWrongCaseKey means the key given is not the case's key. Nothing changed.
	ErrWrongCaseKey = errors.New("case key is wrong")
	// ErrNoCaseKey means the case has no key of its own.
	ErrNoCaseKey = errors.New("case has no key of its own")
	// ErrHasCaseKey means the case already has a key of its own.
	ErrHasCaseKey = errors.New("case already has a key of its own")
	// ErrCaseKeyRule means a new case key breaks the rules (too short, blank,
	// invalid text). The text is in the wrapped masterkey error.
	ErrCaseKeyRule = errors.New("case key does not meet the rules")
)

// CaseKeyInfo is what the store can say about a case's key without the key: from
// file headers only, so it works while the case key (or the master key) is locked.
type CaseKeyInfo struct {
	Has    bool   // the case has its own key
	Locked bool   // and it is not unlocked now
	Ending string // last 4 characters of the check value, "" when there is no key
}

// CaseKeyer is implemented by stores that support the per-case key. A caller
// type-asserts for it; a store without it has no case keys. Every method that takes
// a passphrase leaves the slice to the caller to clear.
type CaseKeyer interface {
	// CaseKey reports the state of the case's key.
	CaseKey(id CaseID) (CaseKeyInfo, error)
	// CreateWithKey is CreateFor for a case sealed under its own key. A repeat of a
	// request id answers the case already made (true) and never changes its key.
	CreateWithKey(ctx context.Context, title, requestID string, pass []byte) (CaseInfo, bool, error)
	// UnlockCase opens the key with pass. ErrWrongCaseKey leaves it locked. It also
	// finishes a change of sealing that was cut short.
	UnlockCase(ctx context.Context, id CaseID, pass []byte) error
	// LockCaseKey forgets the case's key and closes its open handle. Other cases are
	// not touched. It is idempotent.
	LockCaseKey(id CaseID) error
	// LockAllCaseKeys does it for every case.
	LockAllCaseKeys()
	// SetCaseKey gives a case with no key of its own a key. The case must be openable
	// now (its PC key unlocked) and must not be in the middle of a move.
	SetCaseKey(ctx context.Context, id CaseID, pass []byte) error
	// ChangeCaseKey replaces the key. cur is checked first; ErrWrongCaseKey changes nothing.
	ChangeCaseKey(ctx context.Context, id CaseID, cur, next []byte) error
	// RemoveCaseKey puts the case back under the PC key (or Windows when no master
	// key is set). cur is checked first. The PC key must be unlocked when one is set.
	RemoveCaseKey(ctx context.Context, id CaseID, cur []byte) error
	// CaseKeyCheck is the full check value of an unlocked key, for sealing into a
	// case file. It is never shown. ErrCaseLocked while locked.
	CaseKeyCheck(id CaseID) (string, error)
	// CaseArchiveKey is the key that seals a case file of this case (KeyKindCase),
	// with the salt and cost the typed case passphrase derives it from, and the check
	// value. The caller clears the key. ErrNoCaseKey when the case has no key of its
	// own; ErrCaseLocked while locked.
	CaseArchiveKey(id CaseID) (FileKey, string, error)
	// CaseProtectors is the protector lookup for reading this case's files: the
	// case-key kind resolves to its unlocked key (ErrCaseLocked while locked), the
	// other kinds to what the store was given.
	CaseProtectors(id CaseID) ProtectorLookup
}
