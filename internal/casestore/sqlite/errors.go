package sqlite

import (
	"errors"

	"api-audit-explorer/internal/casestore"
)

var (
	// ErrDamaged marks a case whose database or manifest failed a check at open
	// ("damaged, re-import required"). Its evidence is never used.
	ErrDamaged = errors.New("case is damaged, re-import required")
	// ErrWrongKey means the case database did not open with the key on file. A
	// database whose first page was overwritten gives the same answer.
	ErrWrongKey = errors.New("case database does not open with its key, or is damaged")
	// ErrNewerSchema means the case was written by a newer build.
	ErrNewerSchema = errors.New("case was written by a newer version")
	// ErrClosed is returned by a Case after Close. It is F0's casestore.ErrHandleClosed.
	ErrClosed = casestore.ErrHandleClosed
	// ErrInvalid wraps every rejected caller input. Its text never echoes the input.
	ErrInvalid = errors.New("invalid input")
)
