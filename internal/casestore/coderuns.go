package casestore

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/klauspost/compress/zstd"

	"api-audit-explorer/internal/jcs"
)

// The code-mode audit record (BL-369, spec 04 section 3.6). Every script run is
// written into the case's own database before it starts and sealed when it ends:
//
//	code_runs       one row per run: who asked, the source, the outcome
//	code_run_calls  one row per host call the run made, with hashes and an
//	                optional compressed copy of the result
//	code_chain      an append-only hash chain with a "begin" and an "end" record
//	                per run
//
// The chain is what makes the record tamper-evident. Each record is canonical
// JSON (RFC 8785, internal/jcs) and carries the hash of the record before it;
// the "end" record also carries the hash of every call row of the run and a hash
// of every text the run row holds. VerifyCodeChain recomputes all of it from the
// table rows, so editing, deleting, reordering or inserting a row anywhere shows
// up as a named failed check at the first row it breaks.
//
// Honest limits. Someone who holds the case key can rewrite the whole database,
// including every hash, and the chain cannot tell a rebuilt chain from an honest
// one. What it does catch is damage and edits that do not rebuild everything:
// a changed byte, a missing or reordered row, a pasted row, a run row that
// disagrees with its record. Cutting rows off the END of the chain, or restoring
// an older copy of the case file, leaves a valid shorter chain: VerifyCodeChain
// can only call that out against a head the user wrote down earlier (ChainAnchor),
// or against the head kept in the case's meta table, which an edit that does not
// know about it leaves behind. No claim of compliance with any standard is made.
//
// Privacy. The store never receives, and never keeps, anything it can leave out:
// the sandbox and the broker hand it tool results that the tools already hid, and
// when AllowIdentifiers is false the store adds a second check of its own (IP
// addresses): it replaces them in the free texts and keeps a call's arguments or
// result only as a hash. The script source is stored as written, because replay
// needs it. See the threat notes in the BL-369 handoff.

const (
	// MaxRunSource is the largest script a run row keeps (spec 04: 256 KiB).
	MaxRunSource = 256 << 10
	// MaxRunReason is the longest reason, in characters.
	MaxRunReason = 200
	// MaxRunCallerID is the longest caller name, in bytes.
	MaxRunCallerID = 64
	// MaxRunErrorText, MaxRunPreview and MaxRunLogs cap the three free texts a run
	// row keeps. A longer text is cut at a character boundary, never refused: the
	// audit row must be written whatever the script printed.
	MaxRunErrorText = 1 << 10
	MaxRunPreview   = 4 << 10
	MaxRunLogs      = 64 << 10
	// MaxCallArgs is the largest argument document of one call, in bytes.
	MaxCallArgs = 64 << 10
	// MaxCallBlob is the largest result whose copy is kept; a bigger one is
	// hashed only (blob_state "oversize").
	MaxCallBlob = 1 << 20
	// MaxCallResult is the largest result the store will even hash.
	MaxCallResult = 32 << 20
	// MaxRunBlobTotal caps the compressed copies one run keeps; a call past it
	// is hashed only.
	MaxRunBlobTotal = 32 << 20
	// MaxCallsPerRun bounds the call rows of one run (spec 04 allows 50; the
	// store's own bound is looser so the broker's limit is the one that bites).
	MaxCallsPerRun = 256
	// MaxCodeRuns bounds the runs one case keeps. Nothing is ever deleted
	// automatically (spec 04 D10); a pruning command is BL-376.
	MaxCodeRuns = 50000
	// MaxCallTool is the longest tool name.
	MaxCallTool = 64
	// MaxChainProblems bounds the problems a ChainReport lists.
	MaxChainProblems = 20
	// MaxChainRowsPage is the most ChainRows returns in one call.
	MaxChainRowsPage = 1000
	// MaxRunsPage is the most ListRuns returns in one call.
	MaxRunsPage = 500
)

// ChainZeroHash is the prev_hash of the first chain record.
const ChainZeroHash = "0000000000000000000000000000000000000000000000000000000000000000"

// Run statuses. "running" is the only one a run can leave.
const (
	RunRunning       = "running"
	RunOK            = "ok"
	RunError         = "error"
	RunTimeout       = "timeout"
	RunLimitExceeded = "limit_exceeded"
	RunCancelled     = "cancelled"
	RunCrashed       = "crashed"
)

// Call statuses, argument states and result-copy states.
const (
	CallOK     = "ok"
	CallError  = "error"
	CallDenied = "denied"

	ArgsKept     = "kept"
	ArgsWithheld = "withheld" // hidden mode, and the arguments held an address: only the hash is kept

	BlobKept      = "kept"
	BlobOff       = "off"      // the run was made with keepResults off
	BlobOversize  = "oversize" // bigger than MaxCallBlob, or the run's copies reached MaxRunBlobTotal
	BlobWithheld  = "withheld" // hidden mode, and the result held an address
	phaseBegin    = "begin"
	phaseEnd      = "end"
	recordVersion = 1
)

// Errors of the audit store.
var (
	// ErrBadCodeRun is a run, call or end record the store refuses: a malformed
	// id, status, hash or document. It wraps the detail.
	ErrBadCodeRun = errors.New("casestore: invalid code run")
	// ErrRunNotFound names a run the case does not hold.
	ErrRunNotFound = errors.New("casestore: code run not found")
	// ErrRunFinished is a write to a run that has already ended (or was closed as
	// crashed): the record is sealed.
	ErrRunFinished = errors.New("casestore: code run already ended")
	// ErrRunExists is a BeginRun with an id that is in use.
	ErrRunExists = errors.New("casestore: code run id in use")
	// ErrCallExists is a RecordCall for a seq or done_seq the run already holds.
	ErrCallExists = errors.New("casestore: call already recorded")
	// ErrBlobNotKept is CallResult for a call whose result copy was not kept.
	ErrBlobNotKept = errors.New("casestore: call result was not kept")
	// ErrCodeTampered is returned by a read that finds a stored copy that does
	// not match its own hash.
	ErrCodeTampered = errors.New("casestore: stored code-run data does not match its hash")
)

func badRun(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrBadCodeRun, fmt.Sprintf(format, a...))
}

// CodeRunStore is implemented by a Case that keeps the code-mode audit record.
// It is a separate interface, like WorkspaceBlobStore, so it breaks no Case.
// Only the broker writes to it.
//
// Write rules shared by every implementation: BeginRun and RecordCall are
// refused with ErrCaseClosed once the case is closed; EndRun and CloseOrphanRuns
// are still allowed on a closed case, so a run that was in flight when the case
// closed can be sealed (they are refused while an archive is being made or after
// it); every write is refused with ErrHandleClosed on a closed handle and
// ErrNotFound on an erased case. Reads work on a closed case, and on a case that
// never held a run they return empty results.
type CodeRunStore interface {
	// BeginRun inserts the run with status "running" and appends its "begin"
	// record, in one step. RunID may be empty: the store makes a UUIDv7.
	BeginRun(ctx context.Context, in RunStart) (CodeRun, error)
	// RecordCall appends one call row to a running run. A row is never changed.
	RecordCall(ctx context.Context, runID string, rec CallRecord) error
	// EndRun seals the run: it fills the outcome, hashes the call rows and
	// appends the "end" record. A second call returns ErrRunFinished.
	EndRun(ctx context.Context, runID string, end RunEnd) (CodeRun, error)
	// CloseOrphanRuns seals every run still "running" as "crashed" and returns
	// how many. Call it where no run can be live: when the case is opened by the
	// broker that owns it, before it accepts a run. A late EndRun for a run closed
	// here gets ErrRunFinished, so a wrong call loses the outcome, not the chain.
	CloseOrphanRuns(ctx context.Context) (int, error)
	// ListRuns returns runs newest first, without Source and ConsoleLogs.
	ListRuns(ctx context.Context, q RunQuery) ([]CodeRun, error)
	// GetRun returns one run with its source and logs.
	GetRun(ctx context.Context, runID string) (CodeRun, error)
	// RunCalls returns the calls of a run in seq order, without the stored copy.
	RunCalls(ctx context.Context, runID string) ([]CodeCall, error)
	// CallResult returns the kept result of one call, decompressed and checked
	// against its hash (ErrCodeTampered when it fails; ErrBlobNotKept when none).
	CallResult(ctx context.Context, runID string, seq int) ([]byte, error)
	// ChainRows returns chain rows with n > afterN, in order, at most limit
	// (default and maximum MaxChainRowsPage).
	ChainRows(ctx context.Context, afterN int64, limit int) ([]ChainRow, error)
	// VerifyCodeChain checks the whole record. anchor may be nil.
	VerifyCodeChain(ctx context.Context, anchor *ChainAnchor) (ChainReport, error)
	// CodeRunStats counts what the record holds, for the inspector and doctor.
	CodeRunStats(ctx context.Context) (CodeStats, error)
}

// RunStart is what a run is known by before it starts.
type RunStart struct {
	RunID            string // UUID; empty makes the store generate a UUIDv7
	Surface          string // cli | mcp | ui
	CallerID         string // at most MaxRunCallerID bytes
	CallerDeclared   bool   // the name came from the caller (MCP clientInfo), not from Unravis
	AllowIdentifiers bool   // the run may see raw identifiers; the row says so
	KeepResults      bool   // keep a copy of each call result (setting keepResults "all")
	ToolSchemaHash   string // 64 hex or empty
	Reason           string // at most MaxRunReason characters
	Source           string // the TypeScript as submitted
	Seed             string // the RNG seed, at most 64 characters of [0-9a-zA-Z]
	ClockMS          int64  // the frozen start time replay uses (unix ms)
}

// CallRecord is one finished host call.
type CallRecord struct {
	Seq        int    // 1-based order in which the script made the call
	DoneSeq    int    // 1-based order in which the calls finished
	Tool       string // e.g. audit_compare
	Args       []byte // JSON document; empty means {}
	Result     []byte // the bytes the call returned to the script (an error document for a failed call)
	Status     string // ok | error | denied
	DurationMS int64
}

// RunEnd is how a run finished.
type RunEnd struct {
	Status         string // any status but "running"
	ErrorCode      string
	ErrorText      string // already scrubbed by the broker; cut at MaxRunErrorText
	SandboxMode    string // lpac | restricted_token_fallback | ""
	Runtime        string
	WorkerSHA256   string // 64 hex or empty
	StrippedJSHash string // 64 hex or empty
	Result         []byte // the final result document the script returned, or nil
	ConsoleLogs    string // cut at MaxRunLogs
	DurationMS     int64
	ScriptMS       int64
}

// RunQuery pages ListRuns.
type RunQuery struct {
	Limit  int // default 100, at most MaxRunsPage
	Offset int
}

// CodeRun is a run row. ListRuns leaves Source and ConsoleLogs empty.
type CodeRun struct {
	RunID            string
	CreatedMS        int64 // set by the store's clock
	EndedMS          int64 // 0 while running
	Surface          string
	CallerID         string
	CallerDeclared   bool
	AllowIdentifiers bool
	KeepResults      bool
	SandboxMode      string
	Runtime          string
	WorkerSHA256     string
	ToolSchemaHash   string
	Reason           string
	Source           string
	SourceBytes      int
	StrippedJSHash   string
	Seed             string
	ClockMS          int64
	Status           string
	ErrorCode        string
	ErrorText        string
	ResultHash       string
	ResultBytes      int64
	ResultPreview    string
	ConsoleLogs      string
	CallCount        int
	DurationMS       int64
	ScriptMS         int64
}

// CodeCall is a call row. RunCalls leaves Blob empty and sets BlobBytes.
type CodeCall struct {
	RunID       string
	Seq         int
	DoneSeq     int
	Tool        string
	ArgsJSON    string // canonical JSON; empty when ArgsState is "withheld"
	ArgsHash    string // SHA-256 of the canonical argument document
	ArgsState   string
	ResultHash  string // SHA-256 of the result bytes
	ResultBytes int64
	Blob        []byte // zstd of the result bytes when BlobState is "kept"
	BlobBytes   int
	BlobState   string
	Status      string
	DurationMS  int64
}

// ChainRow is one row of code_chain.
type ChainRow struct {
	N          int64
	RunID      string
	Phase      string // begin | end
	RecordJSON string // canonical JSON of the record
	PrevHash   string
	RecordHash string // hex SHA-256 of RecordJSON
}

// ChainAnchor is a chain head the user noted earlier: the hash of the row with
// number N. A chain that is shorter than N, or whose row N has another hash, has
// lost rows or is another history.
type ChainAnchor struct {
	N    int64
	Hash string
}

// ChainProblem is one failed check. Detail names the check, never row content.
type ChainProblem struct {
	N      int64 // chain row number, 0 when the problem is not tied to one
	RunID  string
	Check  string
	Detail string
}

// ChainReport is the result of VerifyCodeChain.
type ChainReport struct {
	OK            bool
	Rows          int64  // chain rows read
	Runs          int    // runs with a begin record
	OpenRuns      int    // runs still "running" (not a problem)
	HeadN         int64  // number of the last chain row
	Head          string // its hash: note it to catch a later cut
	Problems      []ChainProblem
	MoreProblems  bool // more problems than MaxChainProblems
	AnchorChecked bool
	Warnings      []string
}

// CodeStats counts the audit record.
type CodeStats struct {
	Runs      int
	OpenRuns  int
	Calls     int64
	BlobBytes int64 // compressed result copies
	TextBytes int64 // sources and logs
}

// Names of the checks a ChainProblem can carry.
const (
	CheckChainGap       = "chain_gap"
	CheckPrevHash       = "prev_hash"
	CheckRecordHash     = "record_hash"
	CheckRecordForm     = "record_form"
	CheckRunMissing     = "run_missing"
	CheckDuplicateBegin = "duplicate_begin"
	CheckDuplicateEnd   = "duplicate_end"
	CheckEndNoBegin     = "end_without_begin"
	CheckBeginMismatch  = "begin_mismatch"
	CheckEndMismatch    = "end_mismatch"
	CheckRunStatus      = "run_status"
	CheckCallRow        = "call_row"
	CheckCallBlob       = "call_blob"
	CheckUnreadable     = "unreadable"
	CheckRunUnchained   = "run_unchained"
	CheckCallsOrphan    = "calls_orphan"
	CheckHeadMissing    = "head_missing"
	CheckHeadMismatch   = "head_mismatch"
	CheckAnchorAhead    = "anchor_ahead"
	CheckAnchorMismatch = "anchor_mismatch"
)

// ---------------------------------------------------------------------------
// Cleaning what the broker hands over.

var (
	runIDPattern  = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	hex64Pattern  = regexp.MustCompile(`^[0-9a-f]{64}$`)
	seedPattern   = regexp.MustCompile(`^[0-9A-Za-z]{0,64}$`)
	toolPattern   = regexp.MustCompile(`^[A-Za-z0-9_.]{1,64}$`)
	textModePats  = []string{"lpac", "restricted_token_fallback"}
	surfaces      = map[string]bool{"cli": true, "mcp": true, "ui": true}
	finalStatuses = map[string]bool{RunOK: true, RunError: true, RunTimeout: true, RunLimitExceeded: true, RunCancelled: true, RunCrashed: true}
	callStatuses  = map[string]bool{CallOK: true, CallError: true, CallDenied: true}
)

// ValidRunID reports whether s has the shape of a run id (a lowercase UUID).
func ValidRunID(s string) bool { return runIDPattern.MatchString(s) }

// NewRunID returns a fresh UUIDv7: 48 bits of Unix milliseconds, then random
// bits, so run ids sort by start time.
func NewRunID(now time.Time) (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	var stamp [8]byte
	binary.BigEndian.PutUint64(stamp[:], uint64(max(now.UnixMilli(), 0)))
	copy(b[:6], stamp[2:])
	b[6] = b[6]&0x0f | 0x70
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:], nil
}

func sha256Hex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// SHA256Hex is the hash every record uses: lowercase hex SHA-256.
func SHA256Hex(b []byte) string { return sha256Hex(b) }

// cutText cuts s to at most max bytes at a character boundary and replaces
// invalid UTF-8, so the text is safe to store and to put in a JSON document.
func cutText(s string, max int) string {
	s = strings.ToValidUTF8(s, "�")
	if len(s) <= max {
		return s
	}
	s = s[:max]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

func runeLen(s string) int { return utf8.RuneCountInString(s) }

// The identifier check of the store. It is the second line behind the tools'
// own hiding (BL-365 owns the real sweep): it finds IPv4 and IPv6 addresses,
// which no hidden-mode text should hold. Candidates are cut out by shape and
// then parsed, so a time (12:30:45), a version (1.2.3) or a C++ name
// (std::bad) is left alone.
var (
	ipv4Candidate = regexp.MustCompile(`\b\d{1,3}(?:\.\d{1,3}){3}\b`)
	ipv6Candidate = regexp.MustCompile(`(?:^|[^0-9A-Za-z_:.])([0-9A-Fa-f:]{3,45}(?:\.\d{1,3}){0,3})`)
)

// ReplaceAddresses replaces every IP address in s with "[ip]" and reports whether
// there was one.
func ReplaceAddresses(s string) (string, bool) {
	found := false
	out := ipv4Candidate.ReplaceAllStringFunc(s, func(m string) string {
		if a, err := netip.ParseAddr(m); err == nil && a.Is4() {
			found = true
			return "[ip]"
		}
		return m
	})
	if !strings.Contains(out, ":") {
		return out, found
	}
	var b strings.Builder
	last := 0
	for _, loc := range ipv6Candidate.FindAllStringSubmatchIndex(out, -1) {
		start, end := loc[2], loc[3]
		cand := out[start:end]
		if strings.Count(cand, ":") < 2 {
			continue
		}
		if a, err := netip.ParseAddr(cand); err != nil || !a.Is6() {
			continue
		}
		b.WriteString(out[last:start])
		b.WriteString("[ip]")
		last = end
		found = true
	}
	if last == 0 {
		return out, found
	}
	b.WriteString(out[last:])
	return b.String(), found
}

// holdsAddress reports whether b contains an IP address.
func holdsAddress(b []byte) bool {
	_, found := ReplaceAddresses(string(b))
	return found
}

// NewCodeRun checks a RunStart and builds the run row that BeginRun inserts.
func NewCodeRun(in RunStart, now time.Time) (CodeRun, error) {
	id := in.RunID
	if id == "" {
		var err error
		if id, err = NewRunID(now); err != nil {
			return CodeRun{}, err
		}
	}
	switch {
	case !ValidRunID(id):
		return CodeRun{}, badRun("run id")
	case !surfaces[in.Surface]:
		return CodeRun{}, badRun("surface")
	case len(in.CallerID) > MaxRunCallerID || !utf8.ValidString(in.CallerID) || hasControl(in.CallerID):
		return CodeRun{}, badRun("caller id")
	case in.ToolSchemaHash != "" && !hex64Pattern.MatchString(in.ToolSchemaHash):
		return CodeRun{}, badRun("tool schema hash")
	case !seedPattern.MatchString(in.Seed):
		return CodeRun{}, badRun("seed")
	case in.ClockMS < 0 || in.ClockMS > 1<<52:
		return CodeRun{}, badRun("clock")
	case !utf8.ValidString(in.Source):
		return CodeRun{}, badRun("source is not valid UTF-8")
	case runeLen(in.Reason) > MaxRunReason || hasControl(in.Reason):
		return CodeRun{}, badRun("reason")
	}
	if len(in.Source) > MaxRunSource {
		return CodeRun{}, fmt.Errorf("%w: script source", ErrCapExceeded)
	}
	reason, caller := in.Reason, in.CallerID
	if !in.AllowIdentifiers {
		reason, _ = ReplaceAddresses(reason)
		caller, _ = ReplaceAddresses(caller)
	}
	return CodeRun{
		RunID: id, CreatedMS: now.UnixMilli(), Surface: in.Surface, CallerID: caller, CallerDeclared: in.CallerDeclared,
		AllowIdentifiers: in.AllowIdentifiers, KeepResults: in.KeepResults, ToolSchemaHash: in.ToolSchemaHash,
		Reason: reason, Source: in.Source, SourceBytes: len(in.Source), Seed: in.Seed, ClockMS: in.ClockMS, Status: RunRunning,
	}, nil
}

func hasControl(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

// NewCodeCall checks a CallRecord against its run and builds the row RecordCall
// inserts. blobUsed is what the run's kept copies take so far (compressed).
func NewCodeCall(run CodeRun, rec CallRecord, blobUsed int64) (CodeCall, error) {
	switch {
	case rec.Seq < 1 || rec.Seq > MaxCallsPerRun, rec.DoneSeq < 1 || rec.DoneSeq > MaxCallsPerRun:
		return CodeCall{}, badRun("call sequence")
	case !toolPattern.MatchString(rec.Tool):
		return CodeCall{}, badRun("tool name")
	case !callStatuses[rec.Status]:
		return CodeCall{}, badRun("call status")
	case rec.DurationMS < 0 || rec.DurationMS > 1<<40:
		return CodeCall{}, badRun("call duration")
	}
	if len(rec.Args) > MaxCallArgs {
		return CodeCall{}, fmt.Errorf("%w: call arguments", ErrCapExceeded)
	}
	if len(rec.Result) > MaxCallResult {
		return CodeCall{}, fmt.Errorf("%w: call result", ErrCapExceeded)
	}
	args := rec.Args
	if len(bytes.TrimSpace(args)) == 0 {
		args = []byte("{}")
	}
	canon, err := jcs.Canonicalize(args)
	if err != nil {
		return CodeCall{}, badRun("call arguments are not JSON")
	}
	c := CodeCall{
		RunID: run.RunID, Seq: rec.Seq, DoneSeq: rec.DoneSeq, Tool: rec.Tool,
		ArgsJSON: string(canon), ArgsHash: sha256Hex(canon), ArgsState: ArgsKept,
		ResultHash: sha256Hex(rec.Result), ResultBytes: int64(len(rec.Result)),
		Status: rec.Status, DurationMS: rec.DurationMS,
	}
	if !run.AllowIdentifiers && holdsAddress(canon) {
		c.ArgsJSON, c.ArgsState = "", ArgsWithheld
	}
	switch {
	case !run.KeepResults:
		c.BlobState = BlobOff
	case len(rec.Result) > MaxCallBlob:
		c.BlobState = BlobOversize
	case !run.AllowIdentifiers && holdsAddress(rec.Result):
		c.BlobState = BlobWithheld
	default:
		blob := zstdEncode(rec.Result)
		if blobUsed+int64(len(blob)) > MaxRunBlobTotal {
			c.BlobState = BlobOversize
		} else {
			c.Blob, c.BlobState = blob, BlobKept
		}
	}
	c.BlobBytes = len(c.Blob)
	return c, nil
}

// FinishCodeRun applies RunEnd to a running run row. calls are the run's call
// rows (any order). The result is the row the sealing UPDATE writes.
func FinishCodeRun(run CodeRun, calls []CodeCall, end RunEnd, now time.Time) (CodeRun, error) {
	switch {
	case !finalStatuses[end.Status]:
		return CodeRun{}, badRun("end status")
	case end.WorkerSHA256 != "" && !hex64Pattern.MatchString(end.WorkerSHA256),
		end.StrippedJSHash != "" && !hex64Pattern.MatchString(end.StrippedJSHash):
		return CodeRun{}, badRun("hash")
	case end.SandboxMode != "" && !slicesContains(textModePats, end.SandboxMode):
		return CodeRun{}, badRun("sandbox mode")
	case len(end.Runtime) > 64 || hasControl(end.Runtime) || len(end.ErrorCode) > 64 || hasControl(end.ErrorCode):
		return CodeRun{}, badRun("runtime or error code")
	case end.DurationMS < 0 || end.DurationMS > 1<<40 || end.ScriptMS < 0 || end.ScriptMS > 1<<40:
		return CodeRun{}, badRun("duration")
	case len(end.Result) > MaxCallResult:
		return CodeRun{}, fmt.Errorf("%w: run result", ErrCapExceeded)
	case len(calls) > MaxCallsPerRun:
		return CodeRun{}, badRun("too many calls")
	}
	if now.UnixMilli() < run.CreatedMS {
		now = time.UnixMilli(run.CreatedMS) // a clock that went backwards never makes a run end before it began
	}
	out := run
	out.EndedMS = now.UnixMilli()
	out.Status, out.ErrorCode = end.Status, end.ErrorCode
	out.SandboxMode, out.Runtime, out.WorkerSHA256, out.StrippedJSHash = end.SandboxMode, end.Runtime, end.WorkerSHA256, end.StrippedJSHash
	out.DurationMS, out.ScriptMS = end.DurationMS, end.ScriptMS
	out.CallCount = len(calls)
	// Cut first, then replace addresses, so the scan never reads more than the cap.
	errText, logs := cutText(end.ErrorText, MaxRunErrorText), cutText(end.ConsoleLogs, MaxRunLogs)
	preview := cutText(string(end.Result[:min(len(end.Result), MaxRunPreview+4)]), MaxRunPreview)
	if !run.AllowIdentifiers {
		errText, _ = ReplaceAddresses(errText)
		logs, _ = ReplaceAddresses(logs)
		preview, _ = ReplaceAddresses(preview)
	}
	out.ErrorText, out.ConsoleLogs, out.ResultPreview = errText, logs, preview
	if len(end.Result) > 0 {
		out.ResultHash, out.ResultBytes = sha256Hex(end.Result), int64(len(end.Result))
	} else {
		out.ResultHash, out.ResultBytes = "", 0
	}
	return out, nil
}

func slicesContains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// CrashedEnd is the RunEnd CloseOrphanRuns gives a run that never finished.
func CrashedEnd() RunEnd {
	return RunEnd{Status: RunCrashed, ErrorText: "The run did not finish. It was closed when the case was opened again."}
}

// ---------------------------------------------------------------------------
// The records and their hashes.

func b2i(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// BeginRecord is the canonical JSON of the "begin" record of a run row, with n and prev filled in.
func BeginRecord(r CodeRun, n int64, prev string) (string, error) {
	b, err := jcs.Marshal(map[string]any{
		"v": recordVersion, "n": n, "phase": phaseBegin, "run_id": r.RunID, "prev_hash": prev,
		"created_at_utc": r.CreatedMS, "surface": r.Surface, "caller_id": r.CallerID,
		"caller_declared": b2i(r.CallerDeclared), "allow_identifiers": b2i(r.AllowIdentifiers), "keep_results": b2i(r.KeepResults),
		"tool_schema_hash": r.ToolSchemaHash, "reason": r.Reason, "source_sha256": sha256Hex([]byte(r.Source)),
		"seed": r.Seed, "clock_ms": r.ClockMS,
	})
	return string(b), err
}

// callEntry is what the calls hash covers of one call row.
func callEntry(c CodeCall) map[string]any {
	return map[string]any{
		"seq": c.Seq, "done_seq": c.DoneSeq, "tool_name": c.Tool, "args_hash": c.ArgsHash, "args_state": c.ArgsState,
		"result_hash": c.ResultHash, "result_bytes": c.ResultBytes, "blob_state": c.BlobState, "status": c.Status, "duration_ms": c.DurationMS,
	}
}

// CallsHash is the hash of the call rows of a run in seq order (the order of calls).
func CallsHash(calls []CodeCall) (string, error) {
	entries := make([]map[string]any, 0, len(calls))
	for _, c := range calls {
		entries = append(entries, callEntry(c))
	}
	b, err := jcs.Marshal(entries)
	if err != nil {
		return "", err
	}
	return sha256Hex(b), nil
}

// EndRecord is the canonical JSON of the "end" record of a finished run row.
// calls must be the run's call rows sorted by seq.
func EndRecord(r CodeRun, calls []CodeCall, n int64, prev string) (string, error) {
	ch, err := CallsHash(calls)
	if err != nil {
		return "", err
	}
	b, err := jcs.Marshal(map[string]any{
		"v": recordVersion, "n": n, "phase": phaseEnd, "run_id": r.RunID, "prev_hash": prev,
		"ended_at_utc": r.EndedMS, "status": r.Status, "error_code": r.ErrorCode, "error_text": r.ErrorText,
		"sandbox_mode": r.SandboxMode, "runtime": r.Runtime, "worker_sha256": r.WorkerSHA256,
		"stripped_js_hash": r.StrippedJSHash, "source_sha256": sha256Hex([]byte(r.Source)),
		"result_hash": r.ResultHash, "result_bytes": r.ResultBytes, "result_preview_sha256": sha256Hex([]byte(r.ResultPreview)),
		"console_logs_sha256": sha256Hex([]byte(r.ConsoleLogs)), "call_count": r.CallCount, "calls_hash": ch,
		"duration_ms": r.DurationMS, "script_ms": r.ScriptMS,
	})
	return string(b), err
}

// NewChainRow wraps a record into its chain row.
func NewChainRow(n int64, runID, phase, record, prev string) ChainRow {
	return ChainRow{N: n, RunID: runID, Phase: phase, RecordJSON: record, PrevHash: prev, RecordHash: sha256Hex([]byte(record))}
}

// BeginChainRow is the row appended by BeginRun.
func BeginChainRow(r CodeRun, n int64, prev string) (ChainRow, error) {
	rec, err := BeginRecord(r, n, prev)
	return NewChainRow(n, r.RunID, phaseBegin, rec, prev), err
}

// EndChainRow is the row appended by EndRun and CloseOrphanRuns.
func EndChainRow(r CodeRun, calls []CodeCall, n int64, prev string) (ChainRow, error) {
	rec, err := EndRecord(r, calls, n, prev)
	return NewChainRow(n, r.RunID, phaseEnd, rec, prev), err
}

// ---------------------------------------------------------------------------
// Result copies.

var (
	zstdOnce sync.Once
	zstdEnc  *zstd.Encoder
	zstdDec  *zstd.Decoder
)

func zstdInit() {
	zstdOnce.Do(func() {
		// EncodeAll and DecodeAll only: no stream, so no goroutine is started.
		zstdEnc, _ = zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1))
		zstdDec, _ = zstd.NewReader(nil, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(MaxCallBlob+(1<<20)))
	})
}

func zstdEncode(b []byte) []byte {
	zstdInit()
	return zstdEnc.EncodeAll(b, make([]byte, 0, len(b)/2+16))
}

// DecodeCallBlob decompresses a kept result copy. The output is bounded by
// MaxCallBlob (plus a megabyte of slack): a stored blob that claims more is
// refused, so a damaged row cannot make the reader allocate without limit.
func DecodeCallBlob(blob []byte) ([]byte, error) {
	zstdInit()
	out, err := zstdDec.DecodeAll(blob, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: result copy does not decompress", ErrCodeTampered)
	}
	return out, nil
}

// VerifyCallRow verifies one call row against its own hashes. It returns the name
// of the failed check ("" when the row is sound).
func VerifyCallRow(c CodeCall) (check, detail string) {
	switch c.ArgsState {
	case ArgsKept:
		canon, err := jcs.Canonicalize([]byte(c.ArgsJSON))
		if err != nil || string(canon) != c.ArgsJSON {
			return CheckCallRow, fmt.Sprintf("call %d: arguments are not canonical JSON", c.Seq)
		}
		if sha256Hex(canon) != c.ArgsHash {
			return CheckCallRow, fmt.Sprintf("call %d: arguments do not match their hash", c.Seq)
		}
	case ArgsWithheld:
		if c.ArgsJSON != "" || !hex64Pattern.MatchString(c.ArgsHash) {
			return CheckCallRow, fmt.Sprintf("call %d: withheld arguments are not empty", c.Seq)
		}
	default:
		return CheckCallRow, fmt.Sprintf("call %d: unknown argument state", c.Seq)
	}
	if !hex64Pattern.MatchString(c.ResultHash) || c.ResultBytes < 0 || !callStatuses[c.Status] || !toolPattern.MatchString(c.Tool) {
		return CheckCallRow, fmt.Sprintf("call %d: malformed field", c.Seq)
	}
	switch c.BlobState {
	case BlobKept:
		out, err := DecodeCallBlob(c.Blob)
		if err != nil {
			return CheckCallBlob, fmt.Sprintf("call %d: result copy does not decompress", c.Seq)
		}
		if int64(len(out)) != c.ResultBytes || sha256Hex(out) != c.ResultHash {
			return CheckCallBlob, fmt.Sprintf("call %d: result copy does not match its hash", c.Seq)
		}
	case BlobOff, BlobOversize, BlobWithheld:
		if len(c.Blob) != 0 {
			return CheckCallBlob, fmt.Sprintf("call %d: a result copy is stored although the state says none", c.Seq)
		}
	default:
		return CheckCallRow, fmt.Sprintf("call %d: unknown result state", c.Seq)
	}
	return "", ""
}

// ---------------------------------------------------------------------------
// Verification.

// CodeChainSource is what VerifyCodeChain reads. The sqlite store and the
// in-memory store both implement it over their own storage. Every method reads
// the rows as they are, without checking them.
type CodeChainSource interface {
	// EachChainRow calls fn for every chain row in order of n.
	EachChainRow(ctx context.Context, fn func(ChainRow) error) error
	// EachRunID calls fn for every run row.
	EachRunID(ctx context.Context, fn func(runID string) error) error
	// LoadRun returns the full run row.
	LoadRun(ctx context.Context, runID string) (CodeRun, bool, error)
	// LoadCalls returns the call rows of a run in seq order, with their copies.
	LoadCalls(ctx context.Context, runID string) ([]CodeCall, error)
	// CountCalls counts every call row.
	CountCalls(ctx context.Context) (int64, error)
	// StoredHead returns the head the store keeps beside the chain, if any.
	StoredHead(ctx context.Context) (n int64, hash string, ok bool, err error)
	// Guards names the append-only guards that are missing (empty when intact).
	MissingGuards(ctx context.Context) ([]string, error)
}

type verifier struct {
	rep  ChainReport
	prev string
	next int64
	runs map[string]*runChain
	// seenCalls counts the call rows of every run the walk loaded.
	seenCalls int64
	anchor    *ChainAnchor
	anchorHit string
}

type runChain struct {
	begun, ended bool
	loaded       bool
}

func (v *verifier) problem(n int64, runID, check, detail string) {
	v.rep.OK = false
	if len(v.rep.Problems) >= MaxChainProblems {
		v.rep.MoreProblems = true
		return
	}
	v.rep.Problems = append(v.rep.Problems, ChainProblem{N: n, RunID: runID, Check: check, Detail: detail})
}

// VerifyCodeChain reads the whole record from src and checks it. It returns an
// error only when the storage could not be read; a damaged record is a report
// with OK false. It never stops at the first problem (up to MaxChainProblems are
// listed, in chain order, so Problems[0] is the first broken row).
func VerifyCodeChain(ctx context.Context, src CodeChainSource, anchor *ChainAnchor) (ChainReport, error) {
	v := &verifier{rep: ChainReport{OK: true}, prev: ChainZeroHash, next: 1, runs: map[string]*runChain{}, anchor: anchor}
	if anchor != nil && (anchor.N < 0 || (anchor.N > 0 && !hex64Pattern.MatchString(anchor.Hash))) {
		return v.rep, badRun("anchor")
	}
	err := src.EachChainRow(ctx, func(row ChainRow) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return v.row(ctx, src, row)
	})
	if errors.Is(err, ErrCodeTampered) {
		// A stored value does not read back as the schema says: the walk stops here.
		v.problem(v.rep.HeadN+1, "", CheckUnreadable, "a chain row cannot be read")
		return v.rep, nil
	}
	if err != nil {
		return v.rep, err
	}
	if err := v.finish(ctx, src); err != nil {
		return v.rep, err
	}
	return v.rep, nil
}

// unreadable turns a ErrCodeTampered read into a problem, and passes any other
// error on: a damaged value is a finding, a broken disk is a failure.
func (v *verifier) unreadable(err error, n int64, runID string) error {
	if errors.Is(err, ErrCodeTampered) {
		v.problem(n, runID, CheckUnreadable, "a stored row cannot be read")
		return nil
	}
	return err
}

func (v *verifier) row(ctx context.Context, src CodeChainSource, row ChainRow) error {
	n := row.N
	v.rep.Rows++
	if n != v.next {
		v.problem(n, row.RunID, CheckChainGap, fmt.Sprintf("expected row %d", v.next))
	}
	if row.PrevHash != v.prev {
		v.problem(n, row.RunID, CheckPrevHash, "the link to the row before does not match")
	}
	if sha256Hex([]byte(row.RecordJSON)) != row.RecordHash {
		v.problem(n, row.RunID, CheckRecordHash, "the record does not match its hash")
	}
	canon, err := jcs.Canonicalize([]byte(row.RecordJSON))
	if err != nil || string(canon) != row.RecordJSON {
		v.problem(n, row.RunID, CheckRecordForm, "the record is not canonical JSON")
	}
	if v.anchor != nil && v.anchor.N == n {
		v.anchorHit = row.RecordHash
	}
	v.next, v.prev = n+1, row.RecordHash
	v.rep.HeadN, v.rep.Head = n, row.RecordHash

	st := v.runs[row.RunID]
	if st == nil {
		st = &runChain{}
		v.runs[row.RunID] = st
	}
	switch row.Phase {
	case phaseBegin:
		if st.begun {
			v.problem(n, row.RunID, CheckDuplicateBegin, "the run has two begin records")
		}
		st.begun = true
		v.rep.Runs++
	case phaseEnd:
		switch {
		case !st.begun:
			v.problem(n, row.RunID, CheckEndNoBegin, "an end record with no begin record before it")
		case st.ended:
			v.problem(n, row.RunID, CheckDuplicateEnd, "the run has two end records")
		}
		st.ended = true
	default:
		v.problem(n, row.RunID, CheckRecordForm, "unknown phase")
		return nil
	}
	run, found, err := src.LoadRun(ctx, row.RunID)
	if err != nil {
		return v.unreadable(err, n, row.RunID)
	}
	if !found {
		v.problem(n, row.RunID, CheckRunMissing, "the chain names a run the case does not hold")
		return nil
	}
	if row.Phase == phaseBegin {
		want, err := BeginRecord(run, n, row.PrevHash)
		if err != nil || want != row.RecordJSON {
			v.problem(n, row.RunID, CheckBeginMismatch, "the run row does not match its begin record")
		}
		return nil
	}
	calls, err := src.LoadCalls(ctx, row.RunID)
	if err != nil {
		return v.unreadable(err, n, row.RunID)
	}
	st.loaded = true
	v.seenCalls += int64(len(calls))
	if run.Status == RunRunning {
		v.problem(n, row.RunID, CheckRunStatus, "the run row says running although its end record exists")
	}
	if run.CallCount != len(calls) {
		v.problem(n, row.RunID, CheckCallRow, "the number of call rows differs from the run's call count")
	}
	seqs, dones := map[int]bool{}, map[int]bool{}
	for _, c := range calls {
		if check, detail := VerifyCallRow(c); check != "" {
			v.problem(n, row.RunID, check, detail)
		}
		if seqs[c.Seq] || dones[c.DoneSeq] {
			v.problem(n, row.RunID, CheckCallRow, "a call sequence number is used twice")
		}
		seqs[c.Seq], dones[c.DoneSeq] = true, true
	}
	want, err := EndRecord(run, calls, n, row.PrevHash)
	if err != nil || want != row.RecordJSON {
		v.problem(n, row.RunID, CheckEndMismatch, "the run row or its call rows do not match the end record")
	}
	return nil
}

func (v *verifier) finish(ctx context.Context, src CodeChainSource) error {
	// Runs that began and never ended: still running is fine, a sealed row is not.
	for id, st := range v.runs {
		if !st.begun || st.ended {
			continue
		}
		run, found, err := src.LoadRun(ctx, id)
		if err != nil {
			if err := v.unreadable(err, 0, id); err != nil {
				return err
			}
			continue
		}
		calls, err := src.LoadCalls(ctx, id)
		if err != nil {
			if err := v.unreadable(err, 0, id); err != nil {
				return err
			}
			continue
		}
		v.seenCalls += int64(len(calls))
		switch {
		case !found:
		case run.Status != RunRunning || run.EndedMS != 0:
			v.problem(0, id, CheckRunStatus, "the run row is sealed but the chain has no end record for it")
		default:
			v.rep.OpenRuns++
			if run.CallCount != 0 {
				v.problem(0, id, CheckCallRow, "a running run row holds a call count")
			}
			for _, c := range calls {
				if check, detail := VerifyCallRow(c); check != "" {
					v.problem(0, id, check, detail)
				}
			}
		}
	}
	// Run rows the chain never mentions were pasted in.
	if err := src.EachRunID(ctx, func(id string) error {
		if st := v.runs[id]; st == nil || !st.begun {
			v.problem(0, id, CheckRunUnchained, "a run row has no begin record in the chain")
		}
		return nil
	}); err != nil {
		return err
	}
	total, err := src.CountCalls(ctx)
	if err != nil {
		return err
	}
	if total != v.seenCalls {
		v.problem(0, "", CheckCallsOrphan, "the case holds call rows that belong to no chained run")
	}
	// Head kept beside the chain.
	hn, hh, ok, err := src.StoredHead(ctx)
	if err != nil {
		return err
	}
	switch {
	case v.rep.Rows > 0 && !ok:
		v.problem(v.rep.HeadN, "", CheckHeadMissing, "the head kept beside the chain is missing")
	case ok && (hn != v.rep.HeadN || hh != v.rep.Head):
		v.problem(v.rep.HeadN, "", CheckHeadMismatch, "the chain ends at another row than the head kept beside it: rows were cut or changed")
	}
	// A head the user wrote down.
	if a := v.anchor; a != nil && a.N > 0 {
		v.rep.AnchorChecked = true
		switch {
		case a.N > v.rep.HeadN:
			v.problem(a.N, "", CheckAnchorAhead, "the chain is shorter than the head you noted: rows were removed or an older copy was restored")
		case v.anchorHit != a.Hash:
			v.problem(a.N, "", CheckAnchorMismatch, "the row you noted has another hash: this is not the history you noted")
		}
	}
	missing, err := src.MissingGuards(ctx)
	if err != nil {
		return err
	}
	for _, m := range missing {
		v.rep.Warnings = append(v.rep.Warnings, "append-only guard missing: "+m)
	}
	return nil
}
