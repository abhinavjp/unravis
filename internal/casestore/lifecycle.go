package casestore

import (
	"context"
	"errors"
	"time"
)

// State is where a case stands in its life (decisions A7). Missing is a listing
// condition, not a state. Archiving, Archived and Erased are named here so the
// belief table is complete; P2 itself only produces Open, Closed, Untrusted and,
// through a mark, Archiving.
type State string

const (
	StateOpen      State = "open"
	StateClosed    State = "closed"
	StateArchiving State = "archiving"
	StateArchived  State = "archived"
	StateUntrusted State = "untrusted"
	StateErased    State = "erased"
)

// ParseState checks a state word that came from outside (the expectState of a
// request). The empty string is not a state: it means "no belief supplied".
func ParseState(s string) (State, bool) {
	switch st := State(s); st {
	case StateOpen, StateClosed, StateArchiving, StateArchived, StateUntrusted, StateErased:
		return st, true
	}
	return "", false
}

// StateOf is the state a CaseInfo describes. A mark outranks the retention
// stamp: a case being archived is Archiving whether or not it was Closed first.
func StateOf(i CaseInfo) State {
	switch {
	case i.Untrusted:
		return StateUntrusted
	case i.Mark == MarkArchiving:
		return StateArchiving
	case i.Closed != nil:
		return StateClosed
	}
	return StateOpen
}

// Action is a lifecycle move a page can ask for. It is what the belief table of
// spec v7 section 9.1 is keyed by.
type Action string

const (
	ActionClose         Action = "close"
	ActionReopen        Action = "reopen"
	ActionArchive       Action = "archive"
	ActionCancelArchive Action = "cancel-archive"
	ActionRestore       Action = "restore"
	ActionRestoreAsNew  Action = "restore-as-new"
	ActionFree          Action = "free"
	ActionErase         Action = "erase"
	ActionAddImport     Action = "add-import"
)

// The answers of the belief table. Each has its own message id in the spec, so
// the server can show the right sentence.
var (
	// ErrCaseChanged is M2: "This case changed in another tab. Reload."
	ErrCaseChanged = errors.New("case changed in another tab")
	// ErrArchiveRunning is M3: "An archive is already running."
	ErrArchiveRunning = errors.New("an archive is already running")
	// ErrErasing is M5: "This case is being erased."
	ErrErasing = errors.New("case is being erased")
	// ErrCancelArchiveFirst is M15: "An archive is running. Cancel it first."
	ErrCancelArchiveFirst = errors.New("an archive is running; cancel it first")
	// ErrAlreadyMade is M18: "This case was already made. Find it on the start screen."
	ErrAlreadyMade = errors.New("case was already made")
	// ErrFreeing is M19: "This case is being freed. Try again in a moment."
	ErrFreeing = errors.New("case is being freed")
)

// CheckBelief implements spec v7 section 9.1: the state the page believes
// against the state the server finds, under the case lock. nil means the move
// may run. believed "" means the request carried no belief (an older client); the
// table is then skipped except for the rows that do not depend on it.
func CheckBelief(a Action, believed, actual State) error {
	if actual == StateErased {
		return ErrNotFound
	}
	switch a {
	case ActionErase:
		// Erase accepts any belief. An archive in flight must be cancelled first.
		if actual == StateArchiving {
			return ErrCancelArchiveFirst
		}
		return nil
	case ActionRestoreAsNew:
		if actual == StateArchived {
			return nil
		}
		return ErrCaseChanged
	case ActionAddImport:
		// Not a belief row: a case that is not Open takes no files, whatever the
		// page thought. The answer is case-closed.
		if actual == StateOpen {
			return nil
		}
		return ErrCaseClosed
	case ActionArchive:
		switch actual {
		case StateArchiving:
			return ErrArchiveRunning
		case StateOpen, StateClosed:
			if believed == "" || believed == StateOpen || believed == StateClosed {
				return nil
			}
		}
		return ErrCaseChanged
	}
	// The remaining moves each have one state they start from.
	var from State
	switch a {
	case ActionClose:
		from = StateOpen
	case ActionReopen:
		from = StateClosed
	case ActionCancelArchive:
		from = StateArchiving
	case ActionRestore, ActionFree:
		from = StateArchived
	default:
		return ErrCaseChanged
	}
	if actual != from {
		return ErrCaseChanged
	}
	if believed != "" && believed != from {
		return ErrCaseChanged
	}
	return nil
}

// Marks are the in-flight stamps of the moves that cannot be undone by a plain
// retry. A marked case is repaired by its recovery hook (see Recoverer).
const (
	MarkArchiving = "archiving"
	MarkFreeing   = "freeing"
	MarkErasing   = "erasing"
)

// CheckMark is the answer a call gets for a case whose mark says it must wait:
// Freeing answers M19 and Erasing answers M5. Archiving is not refused here; the
// belief table decides what an archive in flight allows.
func CheckMark(mark string) error {
	switch mark {
	case MarkFreeing:
		return ErrFreeing
	case MarkErasing:
		return ErrErasing
	}
	return nil
}

// History entry kinds: every move of a case writes one (decisions A8).
const (
	HistoryCreated  = "created"
	HistoryClosed   = "closed"
	HistoryReopened = "reopened"
)

// MaxHistory bounds the entries a case keeps. The oldest go first.
const MaxHistory = 200

// HistoryEntry is one saved move: when, and which.
type HistoryEntry struct {
	At   time.Time
	Kind string
}

// MaxRequestIDLen bounds a create request id.
const MaxRequestIDLen = 64

// ValidRequestID reports whether s is usable as a create request id: 8..64
// characters of letters, digits, dash and underscore. The page makes one with
// crypto.randomUUID; anything else is refused so the id is safe to log.
func ValidRequestID(s string) bool {
	if len(s) < 8 || len(s) > MaxRequestIDLen {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}

// Lifecycle is implemented by a Store that can reopen a closed case, make a case
// once per request id, and keep a mark on a case. It is a separate interface, like
// Tagger, so it breaks no Store; both stores in this repository implement it.
//
// Every method that changes a case takes the case's folder lock, so it cannot
// interleave with another change to the same case.
type Lifecycle interface {
	// ReopenCase clears the Closed stamp and writes a HistoryReopened entry. A
	// case that is not Closed answers ErrCaseChanged. Mark rules: a Freeing or
	// Erasing mark answers ErrFreeing or ErrErasing; an Archiving mark answers
	// ErrCaseChanged.
	ReopenCase(ctx context.Context, id CaseID) error
	// CreateFor makes a case once per request id. When a case already carries
	// requestID it is returned with existed true and nothing is made, unless that
	// case is Archiving or Archived (ErrAlreadyMade). Concurrent calls with the
	// same id make one case. An empty requestID is the same as Create.
	CreateFor(ctx context.Context, title, requestID string) (info CaseInfo, existed bool, err error)
	// SetMark stamps the case with one of the Mark constants; "" clears it.
	SetMark(ctx context.Context, id CaseID, mark string) error
	// Mark reads the stamp ("" for none).
	Mark(ctx context.Context, id CaseID) (string, error)
}
