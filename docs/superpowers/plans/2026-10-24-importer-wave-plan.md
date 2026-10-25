# Importer wave plan: offline file importers (P6, P7, P4, P5, P9, P3, P13, P8, P0)

- **Date:** 2026-10-24. **Trunk:** `master`. **Sized for:** 10 to 15 parallel worktree agents, one branch per card.
- **Inputs:** roadmap rev 9 (§6.2, §6.6, §6.7, §8, §10.3), `decision-rulings.md`, `specs/2026-10-24-storage-decision.md`, `incident-gap-addendum.md` (W1, W2, W4a run in parallel with this wave).
- **All examples are synthetic:** hosts `web-a`, `sql-a`, `*.example.test`; dates in 2001; invented counts.

## 0. Rulings recorded here

1. **Build, do not run.** The orchestrator decided to build P6, P7, P4, P5, P9, P3, P13 export, P8 and P0 as offline file importers. This overrides "importers stay unbuilt" in `decision-rulings.md` for building only.
2. **§5.4 rule 2 (no importer without a question) is satisfied** by the incident questions already in the ledger. Stop rules 3 and 4 still apply.
3. **Fixtures are synthetic only**, authored from public format documentation. No agent opens a real export.
4. **Running on real client exports stays gated** by R2 (preservation, DCS), R8 (content review), R11 (MCP and AI provider), R12 (DBA dry run, every SQL-sourced file), R13 (Mongo role or owner export) and R16 (retention). The import screen says so on every import.
5. **MCP is unchanged** at 10 tools (R10). No case-store data reaches MCP; a test pins this.
6. **Labels.** Importers and joins emit Symptom or Correlation only. Strong correlation stays a ledger decision (§8.6, R8-B7). "Proven cause" is never written.

## 1. Foundation (F0): one Opus agent, about 1 hour, branch `wave/f0-foundation`

F0 lands interfaces, helpers, a memory fake and a conformance harness, and no importer. Other cards stub against these signatures and rebase when F0 merges.

### 1.1 Files (F0 only in this wave, except where P13 is named)

```
internal/evidence/event.go, wording.go, copylint.go (+ _test.go)  Event, labels, join class, IDs, §6.6 twins, causal lint
internal/importer/importer.go, errors.go, reasons.go, limits.go, sniff.go, collect.go, registry.go (+ tests)
internal/importer/textio/        BOM sniffing, bounded line reader, strict CSV
internal/importer/importertest/  conformance harness every importer must pass
internal/clock/                  time/tzdata import, ParseLocal, Windows zone table
internal/join/                   interval overlap with match class
internal/casestore/store.go, memory.go (+ test)  interfaces and in-memory fake
```

F0 does not touch `internal/evidence/types.go`, `record.go` or `build.go` (P13 owns them), `internal/audit/patterns_*` (W1) or `web/`.

### 1.2 Evidence additions (`internal/evidence/event.go`)

`Record` (§8.9, finding-level) is unchanged. `Event` is the per-row unit that importers emit.

```go
const EventSchemaVersion = 1
// Labels reuse the audit.Evidence* strings (internal/audit/patterns_types.go:29); F0 imports, never edits.
func ValidImporterLabel(label string) bool // true only for Symptom and Correlation

type JoinClass string
const (
	JoinUnique    JoinClass = "unique"
	JoinAmbiguous JoinClass = "ambiguous" // listed, never counted
	JoinNone      JoinClass = "none"      // also single-source records
)

type Event struct {
	EvidenceID string            // EventID below
	Source     string            // importer Name(), e.g. "iis-w3c"
	Kind       string            // per-importer enum: "request", "apppool-recycle", "slow-op", ...
	Host       string            // hosts.json label after mapping; raw names never reach the store
	Start, End time.Time         // UTC; End == Start for point events
	DurationMs *float64          // nil when the source has none ("-")
	Value      *float64
	Unit       string            // "ms", "percent-host", "percent-one-core", "count"
	Severity   string            // "info", "warning", "error", "critical", ""
	Line       int64             // 1-based source line (provenance anchor)
	TZRule     string            // clock.Rule* that produced Start
	Attrs      map[string]string // per-importer allowlist; <= 16 keys, <= 256 bytes per value
}
func (e Event) Validate(allowedAttrs map[string]bool) error // UTC, End >= Start, bounds, allowlist
// ev:<case8>:<importer>:<16hex>, hex = SHA-256(caseID|importer|importNonce|line|ordinal)[:8]
func EventID(caseID, importer, importNonce string, line int64, ordinal int) string
func JoinID(caseID, rule string, memberIDs []string) string // jn:<case8>:<rule>:<16hex> over sorted IDs
func CasePrefixes(caseID string) []string                  // {"ev:<case8>:", "jn:<case8>:"}; erase strips ledger refs by prefix
```

IDs use a per-import random nonce, not the file hash, so the hash is not needed before parsing (1.3).

### 1.3 Importer interface (`internal/importer`)

```go
type Importer interface {
	Name() string                            // stable: "iis-w3c", "mongod-json", ...
	Detect(head []byte, fileName string) int // 0..100 over the first 64 KiB; 0 = not mine
	Parse(ctx context.Context, r io.Reader, opts Options, emit func(evidence.Event) error) (Report, error)
	AllowedAttrs() map[string]bool
	DefaultLimits() Limits
}
type Limits struct {
	MaxBytes, MaxRows int64   // over either: whole file refused
	MaxLineBytes      int     // default 64 KiB
	MaxFields         int     // default 4096
	MaxRejectFraction float64 // default 0.05; above it: quarantine
}
type HostHint struct {
	Label, Role  string         // "web-a", "iis" | "sql" | "mongo" | "app"
	TZ           *time.Location // nil: host-local fields are refused
	LogicalCores int            // 0: per-process CPU left undivided and flagged
	Runtime      string         // "iis-10", "sql-2019", "mongod-7.0"
}
type Window struct{ From, To time.Time } // UTC
type Options struct {
	CaseID, ImportNonce, FileName string                   // FileName: sanitised base name
	Host         *HostHint                                // host the user picked for this file, if any
	Hosts        func(observed string) (HostHint, bool)   // hosts.json lookup for names inside the file
	Limits       Limits
	Window       Window
	Pseudonymize func(kind, value string) string          // keyed per case; kind "user" | "client"
	Supplied     map[string]string                        // owner-supplied quality, e.g. "slowms": "100"
}
type Reason string // fixed enum in reasons.go; never holds row content
// One typed constant per value, named Reason+CamelCase (ReasonTZUnlabelled, ReasonDSTAmbiguous, ReasonTZMismatch, ...):
// field-count, bad-time, tz-unlabelled, dst-ambiguous, dst-nonexistent, bad-number, line-too-long, encoding,
// unknown-host, unscrubbed-column, unsupported-format, archive-extract-first, tz-mismatch, reject-fraction
const ReasonFieldCount, ReasonArchive, ReasonUnscrubbedColumn Reason = "field-count", "archive-extract-first", "unscrubbed-column" // ...and so on
type RowError struct{ Line int64; Reason Reason }
type Report struct {
	Importer              string
	Rows, Emitted, Rejected int64
	Errors                []RowError        // first 100 only
	ObservedHosts         []string          // raw names; the service maps or refuses them, never stores them
	First, Last           time.Time         // UTC coverage
	Quality               evidence.SourceQuality
	FieldRules            map[string]string // field -> clock.Rule*
	KnownGaps             []string          // fixed sentences only
}
var ErrLimit, ErrQuarantine, ErrTimezoneUnlabelled, ErrUnknownHost error // "unknown host" text for the last
type LimitError struct{ Limit string; Max, At int64 }         // Unwrap() == ErrLimit
type QuarantineError struct{ Reason Reason; Column string }   // Column: header name only; Unwrap() == ErrQuarantine
func Sniff(head []byte) error                       // zip, gzip, 7z, rar, xz, bzip2, cab magic -> QuarantineError{ReasonArchive}
func LimitReader(r io.Reader, max int64) io.Reader  // *LimitError past max
func Collect(ctx context.Context, imp Importer, r io.Reader, opts Options) ([]evidence.Event, Report, error)
func NewRegistry(imps ...Importer) *Registry
func (r *Registry) Detect(head []byte, fileName string) (best Importer, score int, tied []string) // tie: within 10 points
func (r *Registry) ByName(name string) (Importer, bool)
```

**Reporting.**
- A broken row is rejected and counted with its reason.
- These refuse the whole file and roll back the transaction: a limit breach, the reject fraction, a missing required column, an unscrubbed column, an unknown host.
- **Hash:** the service tees the upload through SHA-256 while parsing (one pass, no plaintext spool) and stores it in `imports.sha256` UNIQUE. A duplicate is caught at commit, rolled back, and reported as "already imported (import #n)".
- **Ties:** on a Detect tie, the user picks the importer.

### 1.4 Helpers

```go
// internal/importer/textio
func NewLineReader(r io.Reader, maxLine int) (*LineReader, Encoding, error) // BOM: utf-8/utf-16le/utf-16be via x/text; else strict UTF-8
func (l *LineReader) Next() (line []byte, lineNo int64, err error)          // *LimitError on an overlong line
type CSVOptions struct{ Comma, Comment rune; MaxFields int; RequireHeader []string }
func NewCSV(r io.Reader, o CSVOptions) (*CSVReader, Encoding, error)       // encoding/csv, LazyQuotes off
func (c *CSVReader) Header() []string
func (c *CSVReader) Next() (rec []string, lineNo int64, err error)

// internal/clock: imports _ "time/tzdata" (about 450 KB); client Windows has no tz database otherwise
const (RuleUTCField = "utc-field"; RuleOffsetInField = "offset-in-field"; RuleHeaderBias = "header-bias"; RuleHostLocal = "host-local")
type LocalResult struct{ UTC time.Time; Ambiguous, Nonexistent bool }
func ParseLocal(layout, value string, loc *time.Location) (LocalResult, error) // DST fold and gap flagged, never guessed
func WindowsZone(id string) (*time.Location, bool)                            // small table; unmapped -> callers say "unknown"

// internal/join
type Interval struct{ ID string; Start, End time.Time }
type Tolerance struct{ Resolution, Skew, Method time.Duration }
func (t Tolerance) Total() time.Duration
type Match struct{ IntervalID string; Class evidence.JoinClass; Candidates []string }
func Overlap(iv []Interval, ev []evidence.Event, tol Tolerance, qualifies func(evidence.Event) bool) []Match
func CountUnique(m []Match) int // ambiguous and none never counted

// internal/evidence/wording.go: exact twins of web/src/lib/copy/wordings.ts
func NotObservedThreshold(source, threshold string) string
func NotObservedGap(detail string) string
func OverlapUnique(source, event string, seconds float64) string
func OverlapAmbiguous(candidates int) string
func AuditCreate(seconds float64) string
func LintCausal(text string) error // rejects caused|because|waiting on|blocked by|due to|proven
```

`wording_test.go` reads `web/src/lib/copy/wordings.ts` (read-only) and fails on any template difference.

### 1.5 Case store (`internal/casestore`)

```go
type CaseID string // 26 chars base32 lower, 128-bit crypto/rand; must match ^[a-z2-7]{26}$ before any path join
type CaseInfo struct{ ID CaseID; Title string; Created time.Time; Closed *time.Time; Bytes int64; Imports int }
type ImportMeta struct {
	Importer, FileName, HostLabel, TZRule string
	AckGating bool   // gating acknowledgement ticked; refused when false
	AckText   string // exact banner text acknowledged; stored in the manifest
}
type ImportRecord struct {
	ID int64; Importer, FileName, SHA256 string; Bytes, Rows, Rejected int64; HostLabel, TZRule string
	Quality evidence.SourceQuality; KnownGaps []string; Imported time.Time
	Status string; Reason importer.Reason // "ok" | "quarantined" | "duplicate"
}
type ImportResult struct{ Record ImportRecord; Report importer.Report }
type EventQuery struct{ Window importer.Window; Kinds, Sources, Hosts []string; Limit int } // Limit <= 50 000
type RunFunc func(r io.Reader, emit func(evidence.Event) error) (importer.Report, error)
type Store interface {
	Create(ctx context.Context, title string) (CaseInfo, error)
	List(ctx context.Context) ([]CaseInfo, error)
	Open(ctx context.Context, id CaseID) (Case, error)
	CloseCase(ctx context.Context, id CaseID) error           // starts the 30-day retention clock (R16)
	Erase(ctx context.Context, id CaseID) error
	Sweep(ctx context.Context, now time.Time) ([]CaseID, error) // erases cases closed > 30 days
}
type Case interface {
	Info() CaseInfo
	Import(ctx context.Context, meta ImportMeta, body io.Reader, run RunFunc) (ImportResult, error) // one file = one tx
	Imports(ctx context.Context) ([]ImportRecord, error)
	Events(ctx context.Context, q EventQuery) ([]evidence.Event, error)
	Pseudonymize(kind, value string) string // HMAC with the case pseudonym key -> "user-xxxx"
	Close() error
}
func NewMemory() Store // F0 fake: same semantics, no encryption
var ErrCapExceeded, ErrNotFound, ErrDuplicate, ErrAckRequired error
```

**SQLite implementation (CS card).**
- **Driver:** `github.com/ncruces/go-sqlite3` v0.35.6 with the `adiantum` VFS. It is cgo-free (wasm2go, no wazero), which fits CGO off.
- **go.mod impact:** adds go-sqlite3, `go-sqlite3-wasm/v6`, julianday, sort, wbt and `lukechampine.com/adiantum`. Raises x/crypto 0.53 to 0.57 and x/text 0.39 to 0.42. Its `go 1.26.0` floor is under the repo's 1.27.3. The exe grows by about 11.5 MB.
- **Fallback** (storage doc): modernc plus app-level AES-GCM.
- **Layout:** `%LOCALAPPDATA%\APIAuditExplorer\cases\<case-id>\` holds `case.key` (DPAPI blob `{v:1, dbKey[32], pseudoKey[32]}` via `sealedfile.WriteAtomic`), `case.db` with its WAL and SHM, and `manifest.dat` (sealed: imports and acknowledgements). `quarantine\` is not created this wave: no raw bytes are kept, and a quarantine is a table row.
- **Folder ACL:** a protected DACL for the current user and SYSTEM only. Reparse points are refused on every path component.
- **Open:** `file:case.db?vfs=adiantum`, then `PRAGMA hexkey` through the connection hook. The key never appears in a DSN, log or error.
  - Pragmas: DEFENSIVE, `trusted_schema=OFF`, `secure_delete=ON`, `foreign_keys=ON`, `cell_size_check=ON`.
  - WAL, with `synchronous=FULL` on import and NORMAL on read.
  - `SetMaxOpenConns(1)`, `quick_check` on open, `max_page_count` from the 4 GiB cap.

```sql
CREATE TABLE meta(k TEXT PRIMARY KEY, v TEXT NOT NULL); -- schema_version=1
CREATE TABLE imports(id INTEGER PRIMARY KEY, importer TEXT NOT NULL, file_name TEXT NOT NULL, sha256 TEXT NOT NULL UNIQUE,
  bytes INTEGER, rows INTEGER, rejected INTEGER, host_label TEXT, tz_rule TEXT, quality_json TEXT, gaps_json TEXT,
  imported_utc INTEGER NOT NULL, status TEXT NOT NULL, ack_gating INTEGER NOT NULL CHECK(ack_gating=1), ack_text TEXT NOT NULL);
CREATE TABLE events(evidence_id TEXT PRIMARY KEY, import_id INTEGER NOT NULL REFERENCES imports(id) ON DELETE CASCADE,
  source TEXT, kind TEXT, host TEXT, ts_ms INTEGER NOT NULL, te_ms INTEGER NOT NULL, duration_ms REAL, value REAL,
  unit TEXT, severity TEXT, line INTEGER, tz_rule TEXT, attrs_json TEXT) WITHOUT ROWID;
CREATE TABLE quarantine(id INTEGER PRIMARY KEY, importer TEXT, file_name TEXT, sha256 TEXT, reason TEXT,
  column_name TEXT, rows_seen INTEGER, at_utc INTEGER);
-- after each bulk load, same transaction:
CREATE INDEX IF NOT EXISTS ev_ts ON events(ts_ms); CREATE INDEX IF NOT EXISTS ev_kind_ts ON events(kind, ts_ms);
CREATE INDEX IF NOT EXISTS ev_import ON events(import_id);
```

- **Caps:** per file, the importer limit (§4), never above 2 GiB or 10M rows; 4 GiB per case DB; 20 GiB across all cases plus a 2 GiB free-disk floor, both checked before the import.
- **Erase:** close connections; overwrite and delete `case.key` (crypto-erase); delete the db, WAL, SHM, manifest and folder; `ledger.StripEvidence(CasePrefixes(id), "case erased")`; delete exports. The UI uses the storage doc's sentence. Erase is idempotent; a partial failure is reported and retryable.
- **Logs** carry the case ID, file name, hash, counts and reasons only, never row contents.

### 1.6 Conformance harness (`importertest.Run(t, imp, Fixtures{Good, Bad})`)

Every importer must pass: zip and gzip heads refused with "extract first"; byte, row and line breaches return `*LimitError` with nothing emitted after; UTF-8, UTF-8 BOM and UTF-16LE BOM copies of the good fixture yield identical events; the sentinel `SENTINEL-7f3a` in a bad cell never appears in any error, report, gap or attribute; every event passes `Validate` (allowlisted attributes, UTC); cells starting with `= + - @`, tab or CR are kept verbatim as data (escaping happens at export); truncated, 1-byte and empty files give a clean error, never a panic; `ctx` cancellation stops the parse within 1 s. Each importer also ships a `FuzzParse` seeded with its fixtures, run for 30 s in review.

## 2. Task cards

Every card: work in a worktree on the named branch; `go test ./...` (and `npm test` in `web/` for web cards) must pass; paste §4 into the brief. An agent that launches before F0 merges copies the §1 signatures it needs into a first commit titled `STUB: drop on rebase`, and drops it on rebase.

| ID | Branch | Model | Depends on | Launch |
|---|---|---|---|---|
| F0 | `wave/f0-foundation` | Opus | none | now |
| CS | `wave/cs-casestore-sqlite` | Sonnet | F0 signatures (stub) | now |
| P0 | `wave/p0-hosts` | Sonnet | F0 (stub) | now |
| P6 | `wave/p6-wincsv` | Sonnet | F0 (stub) | now |
| P7 | `wave/p7-iis` | Sonnet | F0 (stub) | now |
| P4 | `wave/p4-mongodlog` | Sonnet | F0 (stub) | now |
| P5 | `wave/p5-schedule` | Sonnet | F0 (stub) | now |
| P9 | `wave/p9-appinsights` | Sonnet | F0 (stub) | now |
| P3 | `wave/p3-sqlserver` | Opus | F0 (stub); P6 for `pdh.go` only | now (XE and ERRORLOG first) |
| UI | `wave/ui-import-screen` | Sonnet | API contract below (mock fetch) | now |
| API | `wave/api-case-routes` | Sonnet | F0 merged; CS, P0 signatures | after F0 |
| P13 | `wave/p13-case-export` | Opus | F0 merged | after F0 |
| P8 | `wave/p8-timeline` | Sonnet | F0 merged | after F0 |
| INT | `wave/int-importers` | Sonnet | all above merged | last |

### CS: case store (SQLite)
- **Files:** `internal/casestore/sqlite/*`, `internal/casestore/keyfile{,_windows,_other}.go`, `acl_windows.go`, and **`go.mod`/`go.sum` (CS only; INT reconciles)**.
- **Step 1, a 15-minute spike:** on Windows, open an adiantum DB; confirm the exact v0.35.6 import paths (driver plus `vfs/adiantum`), wrong-key failure and plaintext absence. On failure, stop and report; no silent fallback.
- **Tests** (the storage doc's plan): wrong key; no key bytes in `strings case.db`; a kill before commit leaves no rows; `quick_check` catches corruption; each cap refused with the case untouched; a saved copy of `case.db` unreadable after erase; a plaintext sentinel absent from DB, WAL and SHM; concurrent imports serialised; duplicate SHA gives `ErrDuplicate`; `ack_gating=0` refused; F0's memory-fake contract test rerun against SQLite.

### P0: hosts.json
- **Files:** `internal/hosts/hosts.go`, `sealed.go`, `hosts_test.go`, `testdata/`.
- **Format:**
  `{"version":1,"hosts":[{"label":"web-a","aliases":["WEB-A","web-a.example.test"],"role":"iis","tz":"Pacific/Auckland","logicalCores":8,"runtime":"iis-10"}]}`
  `tz` takes an IANA name or a Windows ID mapped through `clock.WindowsZone`. Alias lookup is case-insensitive. The file is stored sealed as `hosts.dat`.
- **API:** `Load(io.Reader) (*Map, error)`, `(*Map).Lookup(observed) (importer.HostHint, bool)`, `Labels()`, `OpenSealed`, `Save`.
- **Negatives:** an unknown host gives `ErrUnknownHost` ("unknown host"); also duplicate alias or label, a bad tz, cores outside 1 to 1024, more than 200 hosts, a file over 64 KiB, unknown fields, and `version != 1`.

### P6: Windows counters and events CSV
- **Files:** `internal/importer/wincounters/`, `internal/importer/winevents/`.
- **Counters (relog CSV).** The header's first cell carries zone and bias, e.g. `"(PDH-CSV 4.0) (Synthetic Standard Time)(-120)"`, giving `RuleHeaderBias`, with the gap "fixed bias from header; DST changes inside the file are not corrected". A host TZ that disagrees with the bias at the first sample quarantines the file (`tz-mismatch`). Wide to long: one `counter` event per non-empty cell (Attrs object, counter, instance); host from `\\HOST\` via `Hosts`; `" "` is missing, not 0.
- **CPU.** `Processor(_Total)` gets unit `percent-host` and the label "overall host CPU; SQL attribution unknown". `Process V2(name:pid)` is preferred; per-process values are divided by LogicalCores, or get unit `percent-one-core` plus a gap when cores are unknown. Exports `NewImporter(name string, detect func([]byte) int) importer.Importer` for P3.
- **Events (Get-WinEvent to Export-Csv, documented recipe).** Required: `TimeCreatedUtc` (ISO 8601, Z), `MachineName`, `LogName`, `ProviderName`, `Id`, `Level`. A local `TimeCreated` is accepted only with a host TZ, else `tz-unlabelled`. `Message` and `Properties` are dropped; `AppPool` is kept. `type Classifier func(provider string, id int) (kind string, attrs map[string]string, ok bool)`; P6 ships `ClassifyTaskScheduler`, INT wires `iis.ClassifyWAS`. Gap when no TaskScheduler/Operational rows exist: "TaskScheduler/Operational is disabled by default; no task events is not evidence that no task ran".
- **Fixtures.** Good: 3 counters by 10 samples with holes; Process V2; UTC events; local events plus host TZ across a DST fold. Bad: bias disagreeing with TZ; local events without TZ; missing `Id`; a 5000-column header; `.blg` and `.evtx` magic (unsupported-format).

### P7: IIS W3C, HTTPERR, WAS classification
- **Files:** `internal/importer/iis/w3c.go`, `httperr.go`, `was.go`, tests and testdata.
- **W3C.** Honour `#Fields`, including re-declaration mid-file; `date` + `time` is `RuleUTCField`. Whether `time` is start or end is **unverified**: `const w3cTimeIsEnd = true` gives Start = time - time-taken, with the gap "W3C time semantics unverified". `time-taken` includes delivery, so the residual is "time outside the pipeline". IIS and NCSA formats give Detect 0.
- **Fields.** Keep cs-method, cs-uri-stem (GUID and numeric segments become `{id}`, max 256 bytes), sc-status, sc-substatus, sc-win32-status, time-taken, s-port, s-sitename; host from s-computername or the picked host. Drop c-ip, s-ip, cs-uri-query, cs(User-Agent), cs(Cookie), cs(Referer). Pseudonymise cs-username.
- **HTTPERR:** Kind `httperr` with an allowlisted `s-reason`, else "other"; IPs and query dropped.
- **WAS:** `ClassifyWAS(provider string, id int) (string, map[string]string, bool)`: ids 5074, 5075, 5076, 5077, 5079, 5080, 5117 are `apppool-recycle` with a reason (5074 is the time interval, **not CPU**); 5186 is `apppool-idle-shutdown`. `RecycleGap(runtime string) string`: before IIS 10, or with an unknown runtime, logEventOnRecycle covers only Time, Memory and PrivateMemory.
- **Negatives:** no `#Fields` quarantines; a wrong field count rejects the row; `time-taken` of `-` gives a nil duration; an unknown field is ignored; a sentinel in cs-uri-query never appears in output.

### P4: mongod.log (JSON, 4.4+)
- **Files:** `internal/importer/mongodlog/`.
- **Time:** `t.$date` with an offset is `RuleOffsetInField`. A naive time rejects the row as `tz-unlabelled`. The pre-4.4 text format is unsupported-format.
- **Slow query (id 51803):** Start = t - durationMillis. On 8.0 and later, `workingMillis` is also kept.
- **Keep:** type, ns, the command name key, planSummary stage tokens, keysExamined, docsExamined, nreturned, numYields, queryHash. **Drop:** the command body, filter, client, remote, appName.
- **Source quality:** version (Build Info); slowms (startup options, `Supplied["slowms"]`, or "unknown"); coverage; rotation markers as gaps; the offset rule. Every log id used is cited from the docs in a code comment.
- **Joins and wording.** `M1(audit []join.Interval, ev []evidence.Event) []join.Match` over [RequestInitiatedAt, CreatedOn] with a 1 s tolerance. `Absence(q evidence.SourceQuality, iv join.Interval) string` returns `NotObservedThreshold("mongod.log", "operations slower than <slowms> ms")` only when version, slowms, full coverage, no rotation gap and the offset rule are all present; otherwise `NotObservedGap(...)`.
- **Tests.** **R8-B5:** two overlapping slow inserts on one collection give `ambiguous` and "Possible overlap (ambiguous: 2 candidates)."; one insert gives unique; no Build Info gives the gap wording; a line over 256 KiB, truncated JSON, a negative duration.

### P5: Hangfire and SQL Agent schedule export
- **Files:** `internal/importer/schedule/`.
- **Hangfire CSV:** fixed fields `JobId,Queue,Cron,TimeZoneId,LastExecution,NextExecution,CreatedAt`; times ISO or epoch seconds or milliseconds. A `Job` or `Args` column quarantines (`unscrubbed-column`). The in-house cron expander handles 5 or 6 fields with `* , - /`; `L W # ?` and names give "unknown". Windows zone IDs go through `clock.WindowsZone`; unmapped gives "unknown".
- **SQL Agent CSV:** job name, enabled, `freq_type`, `freq_interval`, `freq_subday_type`, `freq_subday_interval`, `freq_relative_interval`, `freq_recurrence_factor`, `active_start/end_date/time`; optional history `run_date`, `run_time`, `run_duration` (HHMMSS). Server-local, so a host TZ is required or the file is refused. SQL-sourced, so R12 applies.
- **Output:** `schedule-boundary` events inside `opts.Window` (max 100k) and `job-run` events from history.
- **B3:** `B3(stallStarts []time.Time, b []evidence.Event, tol time.Duration) (k, n, unknown int)`, worded "stall start falls within ±tolerance of a scheduled boundary in k of n independent episodes". An unknown run time counts as unknown, never as a miss.
- **Negatives:** `0 0 L * *` gives unknown; an unknown freq_type rejects the row; Agent data without a TZ; a boundary in the DST gap is flagged.

### P9: App Insights KQL CSV
- **Files:** `internal/importer/appinsights/`.
- **Required header:** `timestamp [UTC]`, ISO 8601, from the documented query. A locale-formatted time quarantines the file with "use the documented query".
- **Keep:** itemType, name, duration (shown as "attributed duration"), resultCode, success, itemCount, operation_Id, cloud_RoleInstance (host), dependency type.
- **Drop:** data, command text, url query, customDimensions, client_IP, user_Id.
- **Rules:** counts use `sum(itemCount)` with the gap "sampled; counts are estimates"; an empty result gives `NotObservedGap("empty result; possibly no access")`; a missing record is never counter-evidence.
- **Negatives:** no `[UTC]` header; itemCount of 0 or less; duplicate columns; a sentinel in `data` never appears in output.

### P3: SQL Server, system_health XE, ERRORLOG, PDH (Opus)
- **Files:** `internal/importer/sqlserver/xe.go`, `errorlog.go`, `pdh.go`, `golden_test.go`, testdata.
- **XE CSV:** `timestamp_utc` required. Allowlisted columns: event name, wait_type, duration, component, state, error number, severity. **A column named like sql_text, statement, input_buffer, xml_report, client_hostname, username, nt_username or database_name** quarantines the file (`unscrubbed-column`, header name only). Quality.Threshold is "lock waits over 30 s, latch waits over 15 s".
- **ERRORLOG:** usually UTF-16LE; server-local, so `RuleHostLocal` with a DST-aware host TZ is required or the file is refused. Lines are classified by allowlist (I/O over 15 s, non-yielding scheduler, Error/Severity numbers, start and stop, login-failed as a count only). **Message text is never stored**; unclassified lines are counted, not rejected.
- **PDH:** `wincounters.NewImporter("sql-pdh", ...)`, after P6 merges.
- **Tests.** **R8-B1 golden fixture:** one synthetic event appears as an XE `timestamp_utc`, an ERRORLOG local line, a PDH sample and an audit interval, and all align; four mutation cases each shift one source by the host offset and must fail. **R8-B2:** a 28 s stall with an empty export prints `NotObservedThreshold("system_health", "lock waits over 30 s and latch waits over 15 s")` and no "SQL was not waiting" variant. A 35 s lock wait prints `OverlapUnique(...)` (Correlation), never "the API waited for SQL". Also: an unscrubbed column; a missing `timestamp_utc`; an ERRORLOG with no TZ.

### P13: redacted case export (Opus)
- **Files:** `internal/evidence/types.go`, `record.go`, `build.go` (P13 alone this wave), plus new `caseexport.go` and `csvsafe.go` with tests.
- **Schema:** `Body.Events []ExportEvent` and `Body.Imports []ExportImport`; `SchemaVersion` becomes 2, and version 1 still verifies.
- **Export rules:** per-source attribute allowlist; case-keyed pseudonyms; thin cells (n < 20) masked with complementary suppression; preview then confirm through the existing `/api/export/preview` and `/api/export/confirm`; the manifest carries importer names, file hashes, acknowledgements and gaps, but no file names.
- **`csvsafe.Cell(s) string`** prefixes `'` to cells starting with `= + - @`, tab or CR. It reuses or replaces `server.spreadsheetSafe`; coordinate with INT.
- **Tests:** re-identification (a re-export under another key shares no pseudonym; raw usernames and the sentinel never appear); injection cells escaped; **R8-B3:** no IPs or raw user IDs in any bundle; **R8-B7:** no "strong correlation" or "proven" in event-derived text; `Verify` round trip for versions 1 and 2.

### P8: thin timeline
- **Files:** `internal/timeline/` and `web/src/lib/components/Timeline.svelte`, with `web/tests/timeline.test.cjs`.
- **Behaviour:** `Build(c casestore.Case, w importer.Window, audit []join.Interval) (View, error)` returns a clock-aligned list with no composite rules. Enabled only with 2 or more sources, each with a validated TZ rule; otherwise it states the reason. An ambiguous join shows every candidate. Each row carries its §6.6 wording and label and passes `LintCausal`.
- **Negatives:** a single source; a host-local source without a TZ; an ambiguous join must list exactly k candidates.

### UI: import screen
- **Files:** `web/src/lib/components/ImportPanel.svelte`, `CasePanel.svelte`, `web/src/lib/cases.ts`, `web/src/lib/copy/importCopy.ts` (copy-linted), `web/tests/import.test.cjs`.
- **Not touched:** `App.svelte`, `types.ts`, `deck.ts`, `wordings.ts`.
- **Flow:** create or pick a case; pick a file (size checked against the cap); see the detected importer (picker on a tie) and the host picker; preview rows, rejected, reasons, gaps and coverage; commit. An unknown host shows "unknown host" and blocks commit.
- **Gating banner, always visible:** "Parsers are built and tested on synthetic files only. Importing real client exports needs: R2 preservation, R8 content review, R11 AI/MCP approval, R12 DBA dry run (SQL files), R13 Mongo access, R16 retention." A per-import checkbox enables commit, and its text is sent as `ackText`.
- **Erase** needs a typed confirmation and shows the storage doc's sentence.
- Tests mock `fetch` against the API contract.

### API: case routes and MCP stance
- **Files:** `internal/server/cases.go` and `cases_test.go`; one `Option` in `server.go`, `WithCases(casestore.Store, *hosts.Map, *importer.Registry)`; an appended block in `routes.go` (coordinate with W4a: whichever merges second rebases); `internal/ledger` gains `StripEvidence(prefixes []string, replacement string) (int, error)`; `internal/mcp/cases_absent_test.go`.
- **Routes (existing token):** `GET/POST /api/cases`; `POST /api/cases/{id}/close`; `DELETE /api/cases/{id}` with `{"confirm":"<id>"}`; `GET/POST /api/cases/{id}/imports` (multipart, `http.MaxBytesReader` at the cap plus 1 MiB, query `importer=&host=&preview=1`; preview runs in a transaction that rolls back; add `async=1` to read the file in the background: the request is checked as usual, then answers `202` with a job, and `GET`/`DELETE /api/cases/{id}/imports/jobs/{job}` report progress (`queued|running|done|failed`, bytes read and total, then the same result or failure body as the direct call) or cancel it; jobs are kept in memory, forgotten an hour after they finish, capped at 64, and cancelled on case erase and on shutdown); `GET /api/cases/{id}/events?from&to&kind` (limit 50 000); `GET/PUT /api/hosts`.
- **Contract:** the case ID is validated first; the file name is reduced to a base name (control characters, `..` and separators stripped) and never used in a path; imports are serialised per case; errors return a `Reason`, never content.
- **MCP:** a test asserts exactly the 10 existing tool names, and that no MCP handler imports `casestore`.

### INT: integrator (Sonnet, last)
- **Owns:** `internal/importer/all/all.go` (the registry, plus winevents classifiers `[iis.ClassifyWAS, winevents.ClassifyTaskScheduler]`); `cmd/audit-explorer/main.go` (case store at the storage-doc path, hosts, `WithCases`, `Sweep` at start); `web/src/App.svelte`, `web/src/lib/types.ts` (append only) and the `deck.ts` import line; the `web/dist` rebuild and `go mod tidy`.
- **Tests:** end to end on synthetic fixtures (upload, preview, commit, events, export preview, erase), plus `go vet`, `go test ./...` and `npm test`.

## 3. Merge order, integration and review

- **Merge order:** F0, CS, P0, P6, then P7, P4, P5, P9, P3 in any order (P3's `pdh.go` after P6), then API (after W4a if its `routes.go` lands first), P13, P8, UI, INT. Every merge rebases, drops stub commits and reruns `go test ./...`.
- **Opus review batches:** (1) F0 alone, before others rebase on it; (2) CS + API + P0: crypto, erase, ACL, routes, paths; (3) P6 + P7 + P9 + P5: parsers, drop lists, TZ rules; (4) P3 + P4 + P8: joins, wordings, R8-B1, B2, B5; (5) P13 + UI: redaction, gating copy; (6) a whole-diff security pass on INT with §4 as the checklist.
- **Haiku** re-reviews each fix round against the findings list only.
- **A merge is blocked by:** a failed conformance harness, a sentinel leak, a causal-lint hit, or a client-looking fixture value (any IP outside RFC 5737, or any host outside `.example.test`).

## 4. Privacy and safety rules (paste into every brief)

1. **Never read `sql/`, `.mcp.json` or `.claude/`.** Never open a real client export, even one found on disk.
2. **No client values** in code, fixtures, tests, comments or commits. Use 2001 dates, RFC 5737 IPs, `*.example.test` hosts, and invented counts and names such as `job-alpha`.
3. **Local-only:** importers and the case store make no network calls; a test swaps in a failing `http.DefaultTransport`. Uploads go only to the existing localhost server.
4. **Caps** (a file over any cap is refused whole):

| Importer | Bytes | Rows | Line |
|---|---|---|---|
| wincounters | 512 MiB | 5M | 64 KiB |
| winevents | 256 MiB | 2M | 64 KiB |
| iis-w3c | 2 GiB | 10M | 64 KiB |
| iis-httperr | 64 MiB | 1M | 64 KiB |
| mongod-json | 2 GiB | 10M | 256 KiB |
| schedule | 16 MiB | 100k | 64 KiB |
| appinsights | 512 MiB | 5M | 64 KiB |
| sql-xe / sql-errorlog | 512 / 256 MiB | 2M | 64 KiB |
| hosts.json | 64 KiB | 200 hosts | n/a |

5. **Zip bombs:** archives are refused by magic bytes with "extract first". Nothing is decompressed.
6. **CSV injection:** every CSV the app writes escapes cells starting with `= + - @`, tab or CR (`csvsafe.Cell`). Parsed cells are data and are never evaluated.
7. **Path traversal:** case IDs must match `^[a-z2-7]{26}$` before any join; upload names are display-only base names; no path comes from file content; reparse points and symlinks are refused.
8. **Drop at parse:** IPs, query strings, user agents, cookies, referers, SQL and command text, Mongo filters, messages. Usernames become keyed pseudonyms. A column on the unscrubbed list quarantines the file.
9. **Errors and logs:** names, hashes, counts and `Reason` values only, never a cell, line or message.
10. **Wordings** come only from `evidence/wording.go` or `wordings.ts`. Importer output is never "strong" or "proven".

## 5. What cannot be built or claimed without a third party

| Item | Who | Blocks |
|---|---|---|
| R2 preservation and DCS | client, Ops | importing any real export |
| R8 content review | reviewer | releasing importer output to people |
| R11 AI/MCP provider approval | client | case data near any AI tool (MCP stays unchanged) |
| R12 / T0.11 DBA dry run of XE, ERRORLOG, Agent, PDH exports | DBA | P3 and the P5 Agent half on real data; confirms the P3 scrub list |
| R13 Mongo custom role, or A2 owner-copied mongod.log | client DBA or owner | P4 on real data |
| R16 retention (30 days drafted) | client security | the first real import |
| T0.3 trusted CPU counters | Ops | any SQL CPU attribution (stays "attribution unknown") |
| T0.5 host map | Ops | real `hosts.json` content (P0 ships empty) |
| D1 Azure owner export | Azure owner | P9 on real data |
| One sanitised real sample per format | each owner | moving a parser from synthetic-tested to validated |
| W3C `time` semantics; Process V2 availability on the servers | IIS owner, Ops | flipping `w3cTimeIsEnd` and the CPU preference with confidence |
