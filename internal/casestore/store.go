// Package casestore holds imported evidence, one case at a time. This file is
// the contract: the interfaces, the value types and the errors. memory.go is
// the in-memory fake with the same semantics; the SQLite implementation (CS
// card) must pass the same contract test.
//
// A store makes no network calls and never keeps raw bytes. Logs and errors
// name cases, files, hashes, counts and reasons, never row content.
package casestore

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

// CaseID is 26 characters of lower-case base32 (128 bits from crypto/rand).
// It must match ^[a-z2-7]{26}$ before it is joined to any path.
type CaseID string

var caseIDPattern = regexp.MustCompile(`^[a-z2-7]{26}$`)

// Valid reports whether id has the required shape.
func (id CaseID) Valid() bool { return caseIDPattern.MatchString(string(id)) }

// ParseID checks a string from outside (a URL, a request body) and returns it as a CaseID.
func ParseID(s string) (CaseID, error) {
	if !CaseID(s).Valid() {
		return "", ErrBadID
	}
	return CaseID(s), nil
}

// NewID returns a fresh random CaseID.
func NewID() (CaseID, error) {
	var b [16]byte
	if _, err := io.ReadFull(rand.Reader, b[:]); err != nil {
		return "", err
	}
	id := CaseID(strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b[:])))
	if !id.Valid() {
		return "", ErrBadID // cannot happen: 16 bytes encode to 26 characters
	}
	return id, nil
}

// MaxTitleBytes bounds a case title.
const MaxTitleBytes = 200

// ValidTitle reports whether a title is non-empty, bounded and valid UTF-8
// without control characters.
func ValidTitle(title string) bool {
	if strings.TrimSpace(title) == "" || len(title) > MaxTitleBytes || !utf8.ValidString(title) {
		return false
	}
	for _, r := range title {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// Retention is how long a closed case is kept before Sweep erases it (R16).
const Retention = 30 * 24 * time.Hour

// MaxEventQuery is the most events one Events call returns.
const MaxEventQuery = 50000

// Caps bound what one case may hold. The per-file cap is the importer's
// limit, never above these (plan §1.5).
type Caps struct {
	MaxFileBytes int64 // per imported file
	MaxFileRows  int64 // per imported file, rows and events
	MaxCaseBytes int64 // all imported files of one case
}

// DefaultCaps are the plan's caps: 2 GiB and 10M rows per file, 4 GiB per case.
var DefaultCaps = Caps{MaxFileBytes: 2 << 30, MaxFileRows: 10_000_000, MaxCaseBytes: 4 << 30}

type CaseInfo struct {
	ID      CaseID
	Title   string
	Created time.Time
	Closed  *time.Time
	Bytes   int64
	Imports int
}

type ImportMeta struct {
	Importer, FileName, HostLabel, TZRule string
	AckGating                             bool   // gating acknowledgement ticked; refused when false
	AckText                               string // exact banner text acknowledged; stored in the manifest
}

// Import outcomes for ImportRecord.Status.
const (
	StatusOK          = "ok"
	StatusQuarantined = "quarantined"
	StatusDuplicate   = "duplicate"
)

type ImportRecord struct {
	ID                         int64
	Importer, FileName, SHA256 string
	Bytes, Rows, Rejected      int64
	HostLabel, TZRule          string
	Quality                    evidence.SourceQuality
	KnownGaps                  []string
	Imported                   time.Time
	Status                     string          // StatusOK, StatusQuarantined or StatusDuplicate
	Reason                     importer.Reason // set when Status is not ok
}

type ImportResult struct {
	Record ImportRecord
	Report importer.Report
}

// EventQuery selects events. Empty Kinds, Sources or Hosts match everything.
// Window is applied to Event.Start, inclusive. Results are ordered by Start,
// then EvidenceID. Limit is clamped to 1..MaxEventQuery; 0 means MaxEventQuery.
type EventQuery struct {
	Window                importer.Window
	Kinds, Sources, Hosts []string
	Limit                 int // Limit <= 50 000
}

// RunFunc is the importer side of an Import: it reads r, calls emit for every
// event, and returns the Report. The store hashes and counts the bytes that
// pass through r, and stages every emitted event; nothing is visible until
// the whole file has been accepted.
type RunFunc func(r io.Reader, emit func(evidence.Event) error) (importer.Report, error)

type Store interface {
	Create(ctx context.Context, title string) (CaseInfo, error)
	List(ctx context.Context) ([]CaseInfo, error)
	Open(ctx context.Context, id CaseID) (Case, error)
	CloseCase(ctx context.Context, id CaseID) error             // starts the 30-day retention clock (R16)
	Erase(ctx context.Context, id CaseID) error                 // idempotent
	Sweep(ctx context.Context, now time.Time) ([]CaseID, error) // erases cases closed > 30 days
}

type Case interface {
	Info() CaseInfo
	// Import runs one file as one transaction: on any error nothing is stored.
	// Outcomes other than success come with a non-nil error and a populated
	// ImportResult:
	//   - ErrAckRequired when meta.AckGating is false or AckText is empty (body unread);
	//   - ErrCapExceeded when a byte, row or case cap is passed;
	//   - *DuplicateError (wraps ErrDuplicate) when the SHA-256 is already in
	//     the case; Record is the existing import with Status "duplicate";
	//   - *importer.QuarantineError: Record.Status is "quarantined" and Reason set;
	//   - anything else run returned, unchanged.
	Import(ctx context.Context, meta ImportMeta, body io.Reader, run RunFunc) (ImportResult, error)
	Imports(ctx context.Context) ([]ImportRecord, error)
	Events(ctx context.Context, q EventQuery) ([]evidence.Event, error)
	Pseudonymize(kind, value string) string // HMAC with the case pseudonym key -> "user-xxxx"
	Close() error
}

var (
	ErrCapExceeded  = errors.New("case cap exceeded")
	ErrNotFound     = errors.New("case not found")
	ErrDuplicate    = errors.New("file already imported")
	ErrAckRequired  = errors.New("acknowledgement required")
	ErrBadID        = errors.New("invalid case id")
	ErrBadTitle     = errors.New("invalid case title")
	ErrHandleClosed = errors.New("case handle closed")
	// ErrDuplicateEvent is returned when an import emits an evidence ID the
	// case already holds, or one twice.
	ErrDuplicateEvent = errors.New("duplicate evidence id")
)

// DuplicateError carries the import that already holds the same bytes.
type DuplicateError struct{ Existing ImportRecord }

func (e *DuplicateError) Error() string { return ErrDuplicate.Error() }

// Unwrap makes errors.Is(err, ErrDuplicate) true.
func (e *DuplicateError) Unwrap() error { return ErrDuplicate }
