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
	"unicode"
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
// without control or format characters: C0/C1 controls, and invisible
// formatting such as bidi overrides, zero-width joiners and line separators,
// which could make a title display as something it is not.
func ValidTitle(title string) bool {
	if strings.TrimSpace(title) == "" || len(title) > MaxTitleBytes || !utf8.ValidString(title) {
		return false
	}
	for _, r := range title {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r) {
			return false
		}
	}
	return true
}

// Retention is how long a closed case is kept before Sweep erases it (R16).
const Retention = 30 * 24 * time.Hour

// MaxAckText bounds ImportMeta.AckText in bytes. The API and every store use
// this one limit.
const MaxAckText = 4 << 10

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
	// Envs and Sources count the environment names and source tags the case
	// holds (see Tagger). They are filled by Case.Info; Store.List reads only the
	// manifest and leaves them zero.
	Envs    int
	Sources int
	// Untrusted marks a folder that is sealed only by Windows (no master key)
	// although a master key exists and the case was not on the list recorded when
	// the master key was set up (ErrUntrustedLegacy). Store.List shows it with a
	// fixed title and no other detail, and every other operation except Erase
	// refuses it.
	Untrusted bool
	// Mark is the in-flight stamp (MarkArchiving, MarkFreeing, MarkErasing) or empty.
	Mark string
	// RequestID is the create request id the case was made under, or empty.
	RequestID string
	// History lists the saved moves (created, closed, reopened), oldest first. It is
	// filled by Case.Info; Store.List leaves it nil.
	History []HistoryEntry
	// Archive is the archive record (P3), or nil for a case that was never archived
	// and is not being. It is filled by Store.List and Entry (they read the manifest)
	// and by Case.Info.
	Archive *ArchiveInfo
	// Missing is a listing condition: the case has a record but its folder is gone
	// (spec 7.7). Never set for a freed case, whose working copy is meant to be gone.
	Missing bool
}

type ImportMeta struct {
	Importer, FileName, HostLabel, TZRule string
	AckGating                             bool   // gating acknowledgement ticked; refused when false
	AckText                               string // exact banner text acknowledged; stored in the manifest
	// AllowedAttrs is the importer's attribute allowlist (Importer.AllowedAttrs).
	// When non-nil the store refuses an event with an attribute outside it, so
	// the allowlist holds even when run does not go through importer.Collect.
	// When nil only the generic bounds are checked (count, key and value size).
	AllowedAttrs map[string]bool
	// Supplied holds the owner-supplied settings for this import, already checked
	// against the importer's declared keys (importer.ValidateSupplied). The store
	// keeps them with the import record and the manifest, bounds their shape
	// (CleanSupplied) and never logs them.
	Supplied map[string]string
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
	AckText                    string // the banner text acknowledged for this import; empty on a quarantine
	Quality                    evidence.SourceQuality
	KnownGaps                  []string
	Imported                   time.Time
	Status                     string          // StatusOK, StatusQuarantined or StatusDuplicate
	Reason                     importer.Reason // set when Status is not ok
	// Supplied is what the owner supplied for this import (nil when nothing).
	// Imports made before the setting existed read back as nil.
	Supplied map[string]string
}

type ImportResult struct {
	Record ImportRecord
	Report importer.Report
}

// QuarantineRecord is the trace of a refused file; it mirrors the plan's
// quarantine table (§1.5). Quarantines live apart from Imports: they hold no
// events, count for no bytes against the case, and do not
// stop the same bytes from being imported later (for example after the user
// picks the right host or format). Erase removes them with the case.
type QuarantineRecord struct {
	ID                         int64
	Importer, FileName, SHA256 string
	Reason                     importer.Reason
	Column                     string // the missing or duplicated column, when the refusal names one
	Rows                       int64  // rows seen before the refusal
	At                         time.Time
}

// Quarantiner is implemented by a Case that keeps QuarantineRecords. It is a
// separate interface so adding it breaks no existing Case implementation; the
// storetest contract requires every store to implement it.
type Quarantiner interface {
	Quarantines(ctx context.Context) ([]QuarantineRecord, error)
}

// EventQuery selects events. Empty Kinds, Sources or Hosts match everything.
// Window is applied to Event.Start, inclusive. Results are ordered by Start,
// then EvidenceID. Limit is clamped to 1..MaxEventQuery; 0 means MaxEventQuery.
//
// Case.Each ignores Limit and reads MaxScan and MaxScanTime instead.
//
// Attrs keeps only the events whose attribute <name> holds exactly that value
// (every pair must hold; an event without the attribute does not match). A
// store may answer it from an index, so it is the cheap way to read one series
// out of a case that holds thousands. At most MaxAttrFilters pairs; a name must
// pass AttrNameOK and a value must be non-empty, else the query is refused.
type EventQuery struct {
	Window                importer.Window
	Kinds, Sources, Hosts []string
	Attrs                 map[string]string
	Limit                 int // Limit <= 50 000

	// Unordered, for Each only, says the caller does not depend on the order of
	// delivery, so the store may read the events in whatever order is cheapest
	// (the SQLite store reads the table in storage order when the query matches
	// a large share of the case, instead of following the kind and time index,
	// which costs a random page read per event). Without it Each delivers by
	// kind, then start time, and Events by start time.
	Unordered bool

	// MaxScan and MaxScanTime lower the scan budget of one Each call. Zero, or a
	// value above the default, means MaxScanEvents / MaxScanTime.
	MaxScan     int64
	MaxScanTime time.Duration
}

// MaxAttrFilters bounds EventQuery.Attrs.
const MaxAttrFilters = 4

// IndexedAttrs are the attribute names the SQLite store keeps an index for:
// the series key of a counter ("counter") or a metric ("metric") event. An
// Attrs filter on one of them reads only the matching events; on any other name
// it still filters, but the store reads every event of the kind first.
var IndexedAttrs = []string{"counter", "metric"}

// AttrNameOK reports whether name may be used in EventQuery.Attrs.
func AttrNameOK(name string) bool { return selectorAttrName.MatchString(name) }

// ErrBadAttrFilter is the refusal of an EventQuery.Attrs that breaks its limits.
var ErrBadAttrFilter = errors.New("casestore: invalid attribute filter")

// CheckAttrs returns ErrBadAttrFilter when q.Attrs breaks its limits.
func (q EventQuery) CheckAttrs() error {
	if len(q.Attrs) > MaxAttrFilters {
		return ErrBadAttrFilter
	}
	for k, v := range q.Attrs {
		if !AttrNameOK(k) || !selectorValueOK(v) {
			return ErrBadAttrFilter
		}
	}
	return nil
}

// MatchAttrs reports whether an event's attributes satisfy q.Attrs.
func (q EventQuery) MatchAttrs(attrs map[string]string) bool {
	for k, v := range q.Attrs {
		if got, ok := attrs[k]; !ok || got != v {
			return false
		}
	}
	return true
}

// Scan budget of one Case.Each call (plan section 4.1): 20 million events or
// 10 seconds, whichever comes first. Past it Each stops with ErrScanBudget and
// the events already delivered are a valid partial result.
const (
	MaxScanEvents          = 20_000_000
	MaxScanTime            = 10 * time.Second
	scanTimeCheckInterval  = 256
	handleCheckEventsEvery = 256
)

// ScanBudget counts the events of one Each call against the budget.
type ScanBudget struct {
	max     int64
	limit   time.Duration
	started time.Time
	n       int64
}

// NewScanBudget starts the budget for one Each call, applying q's lower limits.
func (q EventQuery) NewScanBudget() *ScanBudget {
	b := &ScanBudget{max: MaxScanEvents, limit: MaxScanTime, started: time.Now()}
	if q.MaxScan > 0 && q.MaxScan < b.max {
		b.max = q.MaxScan
	}
	if q.MaxScanTime > 0 && q.MaxScanTime < b.limit {
		b.limit = q.MaxScanTime
	}
	return b
}

// Duration is the time allowed in total.
func (b *ScanBudget) Duration() time.Duration { return b.limit }

// Scanned is how many events were admitted so far.
func (b *ScanBudget) Scanned() int64 { return b.n }

// Admit is called before each event is delivered. It returns ErrScanBudget when
// delivering this event would pass the event or time budget: exactly max events
// complete normally and the next one is refused.
func (b *ScanBudget) Admit() error {
	if b.n >= b.max {
		return ErrScanBudget
	}
	if b.n%scanTimeCheckInterval == 0 && time.Since(b.started) > b.limit {
		return ErrScanBudget
	}
	b.n++
	return nil
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
	// Import runs one file as one transaction: on any error nothing is stored,
	// no events and no import record. Outcomes other than success come with a
	// non-nil error and a populated ImportResult:
	//   - ErrAckRequired when meta.AckGating is false or AckText is empty (body unread);
	//   - ErrCapExceeded when a byte, row or case cap is passed;
	//   - ErrDuplicateEvent, or whatever else emit refused with, even when run
	//     ignored the error from emit and returned nil: a refused event fails
	//     the import, it never yields a smaller "ok" one;
	//   - *DuplicateError (wraps ErrDuplicate) when the SHA-256 is already in
	//     the case; Record is the existing import with Status "duplicate";
	//   - *importer.QuarantineError: the file is refused and no events are kept.
	//     The store reads the rest of the body to hash it, then records a
	//     QuarantineRecord (see Quarantiner) and not an import: Imports() never
	//     lists it, Info does not count it, and the same bytes can be imported
	//     later. Record has Status "quarantined", Reason, SHA256 and Bytes, and
	//     ID 0;
	//   - anything else run returned, unchanged.
	//
	// Every event must pass Event.ValidateFor with this case's ID and
	// meta.Importer (the event's own Source when meta.Importer is empty), and
	// meta.AllowedAttrs when set.
	Import(ctx context.Context, meta ImportMeta, body io.Reader, run RunFunc) (ImportResult, error)
	Imports(ctx context.Context) ([]ImportRecord, error)
	Events(ctx context.Context, q EventQuery) ([]evidence.Event, error)
	// Each streams the events q selects to fn, one at a time, without the
	// MaxEventQuery cap (q.Limit is ignored). Order is by kind, then Start, so a
	// caller must not rely on a global time order. The window is inclusive.
	// fn gets its own copy of the event. The scan is bounded by the scan budget
	// (MaxScanEvents / MaxScanTime, lowered by q.MaxScan / q.MaxScanTime).
	// Return values:
	//   - nil: every matching event was delivered;
	//   - ErrScanBudget: the budget stopped the scan; the events delivered so far
	//     are valid and the result is partial;
	//   - fn's error, unchanged, which stops the scan;
	//   - ctx's error;
	//   - ErrHandleClosed or ErrNotFound, also when the case is erased while the
	//     scan runs (noticed within handleCheckEventsEvery events).
	Each(ctx context.Context, q EventQuery, fn func(evidence.Event) error) error
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
	// ErrCaseClosed is returned by Import for a case that CloseCase has closed.
	// A closed case takes no more files, however its handle was opened.
	ErrCaseClosed = errors.New("case is closed")
	// ErrUntrustedLegacy is returned for a case folder sealed only by Windows
	// (DPAPI) while a master key exists and the case is not on the legacy list
	// (see masterkey.LegacySet): it may have been planted, so it is never opened,
	// re-wrapped or closed. It can still be erased. It reveals nothing about the case.
	ErrUntrustedLegacy = errors.New("case is not protected by the master key and is not a known older case")
	// ErrAckTooLong is returned by Import when AckText is over MaxAckText.
	ErrAckTooLong = errors.New("acknowledgement text too long")
	// ErrDuplicateEvent is returned when an import emits an evidence ID the
	// case already holds, or one twice.
	ErrDuplicateEvent = errors.New("duplicate evidence id")
	// ErrScanBudget is what Each returns when the scan budget stopped it.
	ErrScanBudget = errors.New("scan budget reached")
	// ErrBadSupplied is returned by Import when ImportMeta.Supplied is malformed
	// (too many keys, a bad key, or a value that is too long or not plain text).
	// It never carries the key or the value.
	ErrBadSupplied = errors.New("invalid supplied settings")
)

// CleanSupplied checks the shape of a supplied-settings map and returns a copy:
// at most importer.MaxSuppliedKeys keys, each a valid key, each value plain
// text of 1..importer.MaxSuppliedValue bytes. Whether a key or value is
// accepted by an importer is importer.ValidateSupplied's job; this is the
// store's own bound. An empty map gives nil.
func CleanSupplied(m map[string]string) (map[string]string, error) {
	if len(m) == 0 {
		return nil, nil
	}
	if len(m) > importer.MaxSuppliedKeys {
		return nil, ErrBadSupplied
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		if !importer.ValidSuppliedKey(k) || !importer.ValidSuppliedText(v) {
			return nil, ErrBadSupplied
		}
		out[k] = v
	}
	return out, nil
}

// copySupplied returns an independent copy of m (nil stays nil).
func copySupplied(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// DuplicateError carries the import that already holds the same bytes.
type DuplicateError struct{ Existing ImportRecord }

func (e *DuplicateError) Error() string { return ErrDuplicate.Error() }

// Unwrap makes errors.Is(err, ErrDuplicate) true.
func (e *DuplicateError) Unwrap() error { return ErrDuplicate }

// Bounds of the environment and source tags a case holds (Tagger).
const (
	MaxEnvNameChars = 60  // characters of one environment name
	MaxTagTextChars = 80  // characters of a source's label, portal, api and kind
	MaxEnvs         = 32  // environments per case
	MaxSources      = 200 // source tags per case
	maxEnvInput     = 1000
)

// Classification of a source (a Mongo database, for one): what the user says it
// holds. The empty string means "not classified yet".
const (
	ClassAudit  = "audit"  // API audit logs
	ClassOther  = "other"  // other data
	ClassUnused = "unused" // not used
)

// ErrBadTag is returned by Tagger for an environment name, a source field or a
// classification that breaks the bounds above. It never carries the input.
var ErrBadTag = errors.New("invalid environment or source tag")

// SourceTag says where one kind of data came from, in the words the user chose:
// Environment, Portal and API, plus what a database holds (Classification).
//
// A SourceTag stores no file path, connection string, host name or address, and
// no credential or secret of any kind, only short plain text the user typed or
// picked (R14). The store bounds and cleans it; callers must not put anything
// else in it. Blank Env, Portal or API means "not named".
type SourceTag struct {
	ID int64 // assigned by AddSource; ignored on input
	// ImportID links the tag to an import of the case (0 for none). It is not
	// checked against Imports: a tag may be added before its file is imported.
	ImportID       int64
	Kind           string // required: the sort of source, for example "iis" or "mongo-db"
	Label          string
	Env            string // free text; need not match a name from SetEnvs
	Portal         string
	API            string
	Classification string    // "", ClassAudit, ClassOther or ClassUnused
	Created        time.Time // assigned by AddSource (UTC); ignored on input
}

// Tagger is implemented by a Case that keeps environments and source tags. It is
// a separate interface, like Quarantiner and Discoverer, so it breaks no Case;
// both stores in this repository implement it.
//
// Writes are refused with ErrCaseClosed once the case is closed (CloseCase);
// reads still work. Every method fails like the other Case methods on a closed
// handle (ErrHandleClosed) or an erased case (ErrNotFound).
type Tagger interface {
	// SetEnvs replaces the case's environment list. Names are trimmed, blank
	// names dropped, and a name that repeats an earlier one in any letter case
	// collapses into the first spelling. Order is kept. nil or empty clears the
	// list. ErrBadTag for a bad name, ErrCapExceeded past MaxEnvs.
	SetEnvs(ctx context.Context, names []string) error
	// Envs lists the environment names in the order SetEnvs gave them.
	Envs(ctx context.Context) ([]string, error)
	// AddSource stores a tag and returns it with ID and Created set. ErrBadTag
	// for a bad field, ErrCapExceeded past MaxSources.
	AddSource(ctx context.Context, s SourceTag) (SourceTag, error)
	// Sources lists the tags, oldest first.
	Sources(ctx context.Context) ([]SourceTag, error)
	// RemoveSource deletes one tag. Removing an ID that is not there is not an error.
	RemoveSource(ctx context.Context, id int64) error
}

// cleanTagText trims s and reports whether it is plain, bounded text: valid
// UTF-8, no control or invisible formatting characters (the ValidTitle rule),
// at most max characters. Empty is allowed here; callers decide if it may be.
func cleanTagText(s string, max int) (string, bool) {
	s = strings.TrimSpace(s)
	if !utf8.ValidString(s) || utf8.RuneCountInString(s) > max {
		return "", false
	}
	for _, r := range s {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r) {
			return "", false
		}
	}
	return s, true
}

// CleanEnvs applies the SetEnvs rules and returns the list to store.
func CleanEnvs(names []string) ([]string, error) {
	if len(names) > maxEnvInput {
		return nil, ErrBadTag
	}
	out := make([]string, 0, len(names))
	seen := make(map[string]struct{}, len(names))
	for _, n := range names {
		n, ok := cleanTagText(n, MaxEnvNameChars)
		if !ok {
			return nil, ErrBadTag
		}
		if n == "" {
			continue
		}
		k := strings.ToLower(n)
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, n)
	}
	if len(out) > MaxEnvs {
		return nil, ErrCapExceeded
	}
	return out, nil
}

// CleanSource applies the AddSource rules: every text field trimmed and
// bounded, Kind non-empty, Classification one of the allowed words, ImportID not
// negative. ID and Created are cleared; the store assigns them.
func CleanSource(s SourceTag) (SourceTag, error) {
	var ok bool
	if s.Kind, ok = cleanTagText(s.Kind, MaxTagTextChars); !ok || s.Kind == "" {
		return SourceTag{}, ErrBadTag
	}
	if s.Env, ok = cleanTagText(s.Env, MaxEnvNameChars); !ok {
		return SourceTag{}, ErrBadTag
	}
	for _, f := range []*string{&s.Label, &s.Portal, &s.API} {
		if *f, ok = cleanTagText(*f, MaxTagTextChars); !ok {
			return SourceTag{}, ErrBadTag
		}
	}
	switch s.Classification {
	case "", ClassAudit, ClassOther, ClassUnused:
	default:
		return SourceTag{}, ErrBadTag
	}
	if s.ImportID < 0 {
		return SourceTag{}, ErrBadTag
	}
	s.ID, s.Created = 0, time.Time{}
	return s, nil
}
