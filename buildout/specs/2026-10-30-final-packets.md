# Unravis Tracing Paper — Final Implementation Spec

**Audience:** an AI coding agent that receives **one work packet at a time**. Each packet is self-contained: it names exact files, signatures, DDL, JSON shapes, tests, and a done-check. Do not explore the repo, do not ask questions; implement exactly the packet. If a packet says a file already exists, open it and match the surrounding style; do not rewrite unrelated code.

**Reader contract:** one packet per turn. Every claim a packet makes about the current code was verified against the working tree at the paths and symbols below. Line numbers drift; anchors are symbol names. Anything the author could not confirm is marked **(unverified)** and a packet that touches it must open the file first and decide locally.

**Global constraints that override any instinct:**

1. **STORAGE.** Extend the existing **encrypted per-case `case.db`** and its unified `events` table (schema in `internal/casestore/sqlite/schema.go`). Do **not** create a separate `.unravis.db`. Every new table (coverage ledger, hydration flags, rollups) lives inside `case.db` and is written **inside the import transaction** alongside the events.
2. **IIS PRIVACY.** Store client IP and referer. Cookies: store only an allowlist of **named** cookies (default empty), and store each cookie value as a **salted hash**, never raw. Export/share has a **scrub option, default ON**, that removes IP, referer and cookie hashes. The importer comments and tests that pin "drop-at-parse" must change (see Packet P1).
3. **PHASES.** Three shippable phases:
   - **MVP-1** — three-lane tracing (API audit, SQL, IIS) + IP/referer/cookie-hash + tooltip dictionary + toolbar.
   - **MVP-2** — live SQL/Mongo pulls saved into `case.db` + coverage ledger + lazy hydration + rollups.
   - **MVP-3** — Layer 4 (Mongo app DB, host metrics) + 3-tier implementing-agent matcher.

**Reality checks from research (do not fight these):**

- Ingest will **not** reach 80–150k rows/s. The driver is `ncruces/go-sqlite3` (WASM) over the adiantum VFS, one prepared `ExecContext` per event inside a single serializable transaction (`internal/casestore/sqlite/case.go`, `Import`/`InsertEvent`); measured throughput is **9–16k events/s**. Packets must not promise faster ingest; they add rollups instead.
- Chart zoom **<15 ms** is only realistic with **precomputed rollups**. `internal/audit/rollup.go` today rolls up *live* Mongo/HTTP sources only; there is **no** rollup table for case events. MVP-2 adds one.
- Case reopen **<100 ms** is plausible but **not met today**: `verify` runs `PRAGMA quick_check` and `dbManifestImports` scans, and event summaries are O(N) `GROUP BY`. Mark any claim about reopen speed **(unverified)** until MVP-2 packages land.

---

## Cross-cutting definitions (used by every packet)

### Source lane keys

`internal/calltrace/lanes.go` maps an importer name to a lane by prefix (`LaneOf`):

```go
func LaneOf(source string) string // "sql-" → LaneSQL; "mongo-"/"mongod-"/"mongostat-" → LaneMongo;
                                   // "redis-" → LaneRedis; "iis-" → LaneIIS; else LaneOther
```

Lane constants live in `internal/calltrace/types.go` (`LaneSQL`, `LaneMongo`, `LaneRedis`, `LaneIIS`, `LaneOther`; states `StateNoCase`, `StateNoData`, `StateClockUnchecked`, `StateOutOfRange`, `StateNoneSeen`, `StateEvents`, `StateIncomplete`). **(unverified: exact declaration lines; open `types.go` before editing.)**

### The three tracing lanes for MVP-1

| Lane | Sources | What it reads |
|---|---|---|
| API audit | Mongo audit (live, `internal/store`) | `RequestRecord` fields via `internal/audit` |
| SQL | `sql-xe`, `sql-errorlog`, `sql-pdh`, `sql-querystore` | events from `case.db` |
| IIS | `iis-w3c`, `iis-httperr`, `iis-freb` | events from `case.db` |

The trace view itself is `web/src/components/CallTrace.svelte`, opened from `web/src/components/ActivityContext.svelte` (verified: `import CallTrace from './CallTrace.svelte'`, `{#if traceOpen&&tray.calls.length}<CallTrace {zone} onclose={closeTrace} />`). `ActivityContext.svelte` is embedded from `EvidenceView.svelte` (`{#if contextAnchor}<ActivityContext ... />`). This wiring already exists; MVP-1 packets extend it, they do not rebuild it.

### Case store interface

`internal/casestore/store.go` defines the `Case` interface (read/import/tag/verify). Relevant verified members and constants:

```go
const MaxEventQuery = 50000            // store.go:102
type EventQuery struct { Window importer.Window; Kinds, Sources, Hosts []string; Attrs map[string]string; Limit int; Unordered bool; MaxScan int64; MaxScanTime time.Duration }
const MaxAttrFilters = 4
var IndexedAttrs = []string{"counter","metric"}
type ImportMeta struct { Importer, FileName, HostLabel, TZRule string; AckGating bool; AckText string; AllowedAttrs map[string]bool; Supplied map[string]string }
```

`ImportMeta.AllowedAttrs` is the enforcement point that already refuses an event attribute outside an importer's allowlist — new IIS attributes (IP/referer/cookie hashes) must be added to `W3C.AllowedAttrs()` in `internal/importer/iis/w3c.go` or the store will refuse them.

### Schema versioning mechanics

`internal/casestore/sqlite/schema.go`:

```go
const schemaVersion = 4
func refusesSchema(v, known int) bool { return v > known }
const schemaSQL = `<meta><imports><events><quarantine> ...>` + tagsSQL
const tagsSQL = `CREATE TABLE IF NOT EXISTS case_sources(...); CREATE TABLE IF NOT EXISTS case_envs(...);`
const indexSQL = `CREATE INDEX IF NOT EXISTS ev_ts ...` + attrIndexSQL
```

New tables must follow this exact pattern: bump `schemaVersion`, add an `IF NOT EXISTS` DDL block appended to the schema string, and extend the on-open migration in `case.go` (`open` path) so an older case gains the tables and writes the new version in one transaction. The v3 comment records the precedent: "a version 2 case is migrated on open by writing the version only."

### Evidence event shape

`internal/evidence` defines `Event` with `EvidenceID`, `Source`, `Kind`, `Host`, `Start`, `End`, `DurationMs`, `Value`, `Unit`, `Severity`, `Line`, `TZRule`, `Attrs`. IIS facts ride in `Attrs`. Existing W3C attrs: `method`, `stem`, `status`, `substatus`, `win32`, `port`, `site`, `user`, `bytes-out`, `bytes-in`.

---

# MVP-1

## Packet P1 — IIS: keep client IP, referer, and an allowlisted cookie hash

**Goal.** Reverse the drop-at-parse behaviour for exactly three fields: client IP, referer, and a **named-cookie allowlist** whose values are stored only as salted hashes. Server IP stays dropped. Query string stays dropped. User agent stays dropped. Cookies **not** on the allowlist are dropped, never read.

**Files.**
- `internal/importer/iis/common.go` — the package comment (verified lines 5–7) currently promises "Drop at parse: client and server IPs, query strings, user agents, cookies and referers are never read into any value." Rewrite it to state the new rule. Keep the sentence "Every field not named in a spec's keep list is skipped."
- `internal/importer/iis/w3c.go` — `w3cSpec.keep`, `w3cRow`, `W3C.AllowedAttrs()`.
- `internal/importer/iis/httperr.go` — **only if** HTTPERR declares the same fields **(unverified: HTTPERR `#Fields` handling on a real sample; if the field is absent, add nothing and record a gap).**
- Tests: `internal/importer/iis/iis_test.go`, `internal/importer/iis/conformance_test.go`.

**New options (on `importer.Options`).** The hash salt and the cookie allowlist are per-import settings, supplied like other options:

```go
// ClientIPField / RefererField name the W3C columns to read. Empty means the
// default ("c-ip" / "cs(Referer)"); a file that does not declare them stores nothing.
type CookiePolicy struct {
    Allowed []string // lower-cased cookie names; empty means no cookie is stored
    Salt    []byte   // >= 16 bytes, never logged, never in the manifest
}
```

Add `CookiePolicy *CookiePolicy` to `importer.Options` in `internal/importer/options.go` **(unverified: exact file; find where `Hosts`, `Host`, `Limits` live).**

**Hash rule (exact).** `hash = hex(HMAC-SHA256(salt, name + "=" + value))[:32]`. Store the name and the 32-hex-char digest only. Never store the raw value. Provide:

```go
// HashCookie returns the 32-hex-char salted digest of one cookie value.
// It returns "" when name is not in Allowed or Salt is shorter than 16 bytes.
func HashCookie(p *CookiePolicy, name, value string) string
```
in `internal/importer/iis/common.go`.

**Row changes in `w3cRow`.** After the existing `attrs` are built, add (each guarded by `c.get`):

```go
if v, ok := c.get("c-ip"); ok && validIPv4or6(v) { attrs["client-ip"] = v }
if v, ok := c.get("cs(Referer)"); ok && safeReferer(v) { attrs["referer"] = v }
if pol := c.opts.CookiePolicy; pol != nil {
    if raw, ok := c.get("cs(Cookie)"); ok {
        for _, pair := range splitCookiePairs(raw) {         // at most maxCookiePairs
            name, value, ok := strings.Cut(pair, "=")
            if !ok { continue }
            if h := HashCookie(pol, strings.ToLower(strings.TrimSpace(name)), value); h != "" {
                attrs["cookie:"+strings.ToLower(strings.TrimSpace(name))] = h
            }
        }
    }
}
```

- `validIPv4or6` accepts `net.ParseIP`, else returns false (no host, no port). Reject anything with a space or `/`.
- `safeReferer` caps at 512 bytes, must parse as `url.Parse` with scheme http/https; keep scheme+host+path, **cut query and fragment**.
- `splitCookiePairs` caps at `maxCookiePairs = 20`; each raw pair capped at 512 bytes; a `Set-Cookie` body is not expected here (this is request `cs(Cookie)`).
- `maxIPBytes = 64`, `maxRefererBytes = 512`.

**`keep` list.** Add `c-ip`, `cs(Referer)`, `cs(Cookie)` to `w3cSpec.keep`. Note the field name is literally `cs(Cookie)` / `cs(Referer)` in IIS W3C logs (parentheses, uppercase R) — `declare` lowercases, and `c.get` lookups in the codebase use lowercase keys, so **use the lowercase forms `cs(cookie)` and `cs(referer)` consistently** and add those lowercase strings to `keep` and to the `c.get` calls. *(The existing code's `s-computername` etc. are already lowercase; follow that.)*

**`AllowedAttrs`.** Add `client-ip`, `referer`, and the cookie names. Because cookie names are dynamic, `AllowedAttrs()` cannot enumerate them. Change `ImportMeta.AllowedAttrs` handling: when the importer declares a cookie policy, the store must accept any attr whose name starts with `cookie:` **only if** the import's allowlist marks it. Simplest correct rule: `W3C.AllowedAttrs()` returns the static set plus a sentinel `"cookie:*"`, and `eventFilter`/`InsertEvent` in `internal/casestore/sqlite/case.go` treats a `cookie:*` allowlist entry as a prefix match **(unverified: exact allowlist check site; open `case.go` and add a prefix case without weakening the exact-match path).**

**Tests (must pass).**
1. `TestW3CStoresClientIPAndReferer` — a fixture row with `c-ip` and `cs(Referer)` produces `attrs["client-ip"]` and `attrs["referer"]`; the referer's query string is gone.
2. `TestW3CCookieHash` — with `Allowed=["sid"]`, a row with `cs(Cookie): sid=secret; theme=dark` produces `attrs["cookie:sid"]` equal to `HashCookie(...)` and **no** `cookie:theme`.
3. `TestW3CCookieRawNeverStored` — assert the raw value `secret` appears in no byte of the emitted event (marshal the event and `bytes.Contains`).
4. `TestW3CStillDropsUserAgentAndQuery` — `cs(User-Agent)` and a query in `cs-uri-stem` remain absent.
5. Replace `TestW3CDropsSensitiveColumns` in `iis_test.go` (verified name) with the above; keep `TestW3CTimeIsEndGap` untouched.

**Done-check.** `go test ./internal/importer/iis/...` green; `grep -n "never read into any value" internal/importer/iis/common.go` returns nothing; marshal-of-event never contains a raw cookie value.

---

## Packet P2 — Export scrub (default ON) for IP, referer, cookie hashes

**Goal.** Every export/share bundle removes `client-ip`, `referer`, and any `cookie:*` attribute unless the caller explicitly turns the scrub **off**.

**Files.**
- `internal/evidence/build.go` — the "Left out by default" statement (verified around line 158) and the field projection.
- `internal/evidence/caseexport.go` — the bundle projection (verified: line 751 statement lists what is not in the file; line 414 comment names IP/URL/path/message).
- `internal/server/export.go` — `exportPreview` / `exportConfirm` request shapes.
- `internal/server/case_report_scrub.go` — `scrubTypedNote` (typed-note scrubber; leave it, but reuse its identifier patterns if helpful).

**Request shape.** Add to the export preview request and confirm request:

```go
// ScrubSensitive, when nil or true, removes client IP, referer and cookie
// hashes from the bundle. Set it false only to include them.
ScrubSensitive *bool `json:"scrubSensitive,omitempty"`
```

Resolve with `scrub := req.ScrubSensitive == nil || *req.ScrubSensitive` (**default ON**).

**Projection change.** In `caseexport.go`, when `scrub` is true drop attr keys `client-ip`, `referer`, and any key with prefix `cookie:`. When false, keep them **but** still never include the raw cookie (there is none — only the hash) and still never include a raw URL query. Update the statement strings:
- scrub on: "Client IP addresses, referers and cookie digests are not in this file."
- scrub off: "This file holds client IP addresses, referers and cookie digests because scrubbing was turned off. Cookie values are digests, never raw values."

**Tests.**
1. `TestBuildCaseScrubDefaultOn` — with no flag, a fixture event carrying `client-ip`/`referer`/`cookie:sid` yields a bundle where none of the three keys is present and the bundle bytes contain no IP.
2. `TestBuildCaseScrubOff` — with `ScrubSensitive=&false`, the three keys are present.
3. Keep `TestBuildCaseNoIPsOrUserIDs` (verified, `caseexport_test.go:320`) **green** — it must still pass, since the default is ON.
4. `caseexport_test.go:83` (`ev[4].Attrs["cookie"] = caseSentinel // not on any allowlist`) must remain a non-allowlisted attr in that test; do not rename it to `cookie:*`.

**Done-check.** `go test ./internal/evidence/... ./internal/server/...` green; export preview JSON has a `scrubSensitive` field defaulting true.

---

## Packet P3 — Tooltip dictionary (plain-word explanations)

**Goal.** A single source of truth mapping every technical label a trace/chart tooltip shows to a plain-word sentence, so tooltips explain themselves.

**Files.**
- New: `web/src/lib/copy/dictionary.ts`.
- Use in `web/src/lib/charts.ts` (tooltip formatters — verified `failureTrendOption`, `heroOption`, `tipStyle`) and in `CallTrace.svelte` / tooltip components.

**Shape.**
```ts
export interface DictEntry { term: string; plain: string; }
export const DICTIONARY: Record<string, DictEntry> = { /* key = stable id */ };
export function explain(id: string): string; // returns plain, or the term, or '' — never throws
```
Seed entries (ids are stable, names are what the user sees): `status.4xx`, `status.5xx`, `incomplete` ("Missing timing (not a failure)"), `latency.p50`, `latency.p95`, `latency.p95Ratio`, `lane.api-audit`, `lane.sql`, `lane.iis`, `event.duration`, `event.time-taken`, `gap.time-end` ("The W3C time field is assumed to be the request end."), `gap.resolution` ("Log time has one-second resolution."), `attr.client-ip`, `attr.referer`, `attr.cookie` ("A digest, not the value."), `join.unique`, `join.ambiguous`, `join.none`.

**Test.** `web/src/lib/copy/dictionary.test.ts` (vitest) asserts every id used by `charts.ts` formatters exists in `DICTIONARY`, and `explain('missing')` returns `''`.

**Done-check.** `npm test` (or the repo's vitest command) green; `rg "explain\(" web/src` shows the chart tooltips use it.

**(unverified: the repo's JS test runner and script name; check `web/package.json` before writing the test invocation.)**

---

## Packet P4 — Toolbar with lane toggles and the scrub indicator

**Goal.** A toolbar above the trace that (a) toggles the three MVP-1 lanes and (b) shows the export scrub state.

**Files.**
- New: `web/src/components/TraceToolbar.svelte`.
- `web/src/components/CallTrace.svelte` — mount the toolbar.
- `web/src/lib/callTrace.ts` — add lane visibility state (`MAX_CALLS` lives here).

**Behavior.** Three toggle buttons (API audit, SQL, IIS); each `aria-pressed`. A "Scrub on/off" pill reading the export setting from `/api/info` or the export preview. Start all three on. A lane with no events shows its `State*` wording from `internal/calltrace/lanes.go` (the wording is server-provided).

**Test.** `TraceToolbar.test.ts`: toggling a lane hides its rows; the scrub pill reflects `scrubSensitive:false`.

**Done-check.** `npm test` green; toolbar renders in `CallTrace.svelte` when the trace opens.

---

## Packet P5 — Wire SQL lane into the trace

**Goal.** When the trace is open for a call, also show events from `sql-*` sources in the same UTC window.

**Files.**
- `internal/server/lanes.go` — lane routes for SQL/Mongo health runs (verified).
- `internal/calltrace/lanes.go` — `BuildLanes`, `LaneOf`, `laneEvents`.
- `internal/timeline/timeline.go` — for window widening, `loadEvents` uses `c.Events(ctx, casestore.EventQuery{Window: wide, Limit: casestore.MaxEventQuery})` and a `lookback` before-window read (verified).

**Behavior.** Reuse `BuildLanes(win, haveCase, sources, evs)`; it already emits the SQL lane with `State*` and events. Fetch events with `EventQuery{Window: win, Sources: sqlSources}`. Do not add a new endpoint if the existing lane route covers it.

**Test.** `internal/calltrace/lanes_test.go` — a case with one `sql-xe` event in the window yields a `LaneSQL` lane in `StateEvents` with that event.

**Done-check.** `go test ./internal/calltrace/... ./internal/server/...` green.

---

# MVP-2

## Packet P6 — Live SQL/Mongo pulls saved into `case.db`

**Goal.** The live DB-health readers (Mongo profiler, SQL XE/errorlog/query store) can be **pulled and persisted** as events into the open case, instead of only read live.

**Files.**
- `internal/dbhealth/lane.go`, `lane_mongo.go`, `lane_sql.go` — readers (verified: `evalLaneProfiler`, profiler `{profile:-1}`, `system.profile` capped 100 MiB).
- `internal/casestore/sqlite/case.go` — `Import` (verified entry), `InsertEvent`, `BeginTx` (serializable).
- New: `internal/dbhealth/pull.go` — `func PullToCase(ctx, c casestore.Case, src Source, win importer.Window) (casestore.ImportResult, error)`.
- New importer-facing source names: `mongo-pull`, `sql-pull` (or reuse existing `mongo-*`/`sql-*` names with a `pull` origin tag).

**Rules.** A pull is an import: it gets its own `imports` row with `status=ok`, a deterministic `sha256` = SHA-256 of `(source + window + first/last doc id)`, so a repeated pull of the same window is `StatusDuplicate` and writes no second copy. All events land inside one transaction via the existing `Import` path (do **not** hand-roll `InsertEvent` outside a tx). Unknown attributes must pass the importer's `AllowedAttrs`.

**Test.** `dbhealth/pull_test.go`: pulling the same window twice writes events once and returns `StatusDuplicate`; a pull from a fake source with an extra doc writes exactly that many events.

**Done-check.** `go test ./internal/dbhealth/...`; re-pull is idempotent; `case.db` grows by the events only.

---

## Packet P7 — Coverage ledger table

**Goal.** A per-source-per-window ledger inside `case.db` recording what was actually read, so the UI can say "source failed on page N".

**DDL (append to `schemaSQL`, bump `schemaVersion` to 5):**
```sql
CREATE TABLE IF NOT EXISTS coverage(
  id INTEGER PRIMARY KEY,
  import_id INTEGER NOT NULL REFERENCES imports(id) ON DELETE CASCADE,
  source TEXT NOT NULL, lane TEXT NOT NULL,
  from_ms INTEGER NOT NULL, to_ms INTEGER NOT NULL,
  rows_read INTEGER NOT NULL, pages_read INTEGER NOT NULL,
  complete INTEGER NOT NULL CHECK(complete IN (0,1)),
  note TEXT, at_utc INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS cov_src ON coverage(source, from_ms, to_ms);
```

**Migration.** In `case.go` open path, add the same `refusesSchema`/`schemaVersion` handling used for v2→v4: an older case gains the table and writes version 5 in one transaction. Update the version comment.

**Writer.** Extend `Import` (or add `RecordCoverage`) to insert coverage rows **inside the same import transaction**.

**Test.** `sqlite/coverage_test.go`: a v4 case opens and gains the table; an import with two pages writes two `complete=0` rows, a full read writes `complete=1`; deleting the import cascades the coverage rows.

**Done-check.** `go test ./internal/casestore/...`; a v4 fixture migrates to v5.

---

## Packet P8 — Lazy hydration flags table

**Goal.** Record, per import, which optional fields are present so the reader can hydrate on demand.

**DDL (same migration to v5):**
```sql
CREATE TABLE IF NOT EXISTS hydration(
  import_id INTEGER NOT NULL REFERENCES imports(id) ON DELETE CASCADE,
  field TEXT NOT NULL,                       -- e.g. "payload", "headers", "attrs.giant"
  present INTEGER NOT NULL CHECK(present IN (0,1)),
  bytes INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY(import_id, field)
);
```

**Rule.** A field not listed is treated as absent. The reader never touches the network for case events; "hydrate" means read the deferred column from `case.db` (e.g. a large `attrs_json` sub-object moved to a side table later). Do **not** add cross-process hydration in this phase.

**Test.** `sqlite/hydration_test.go`: an import that declared a payload writes `present=1`; a case without it reads back absent; cascade delete works.

**Done-check.** `go test ./internal/casestore/...`.

---

## Packet P9 — Case-event rollups (the zoom fix)

**Goal.** Precompute per-source per-bucket rollups inside `case.db` so chart zoom is fast without re-scanning events.

**DDL (same migration to v5):**
```sql
CREATE TABLE IF NOT EXISTS rollups(
  id INTEGER PRIMARY KEY,
  source TEXT NOT NULL, kind TEXT NOT NULL,
  bucket_ms INTEGER NOT NULL,               -- bucket width
  at_ms INTEGER NOT NULL,                   -- bucket start, aligned in UTC
  count INTEGER NOT NULL,
  p50_ms REAL, p95_ms REAL, max_ms REAL,
  count_4xx INTEGER NOT NULL DEFAULT 0,
  count_5xx INTEGER NOT NULL DEFAULT 0,
  incomplete INTEGER NOT NULL DEFAULT 0,
  UNIQUE(source, kind, bucket_ms, at_ms)
);
CREATE INDEX IF NOT EXISTS roll_source_at ON rollups(source, at_ms);
```

**Writer.** At the end of `Import`, a single pass over the just-inserted events builds rollups at `bucket_ms = 60000`. Use the same bucketing rules as `internal/audit/histogram.go` (`BinIndex`/`BinBounds`) for percentiles so numbers agree with the live path. Write rollups in the **same import transaction**.

**Reader.** New method on the SQLite store: `RollupSeries(ctx, q RollupQuery) ([]RollupBucket, error)` reading only `rollups`. The chart, once this lands, prefers `RollupSeries` and falls back to `internal/casestore/series.go` streaming only when the rollup is missing.

**Caps.** Reuse the live-path constants in `internal/audit/rollup.go` (`MaxRollupGroups = 600000`, `RollupNoiseCount = 5`, `DefaultRollupSlowMs = 1000`). Refuse a query whose bucket count exceeds `MaxSeriesBuckets = 2000` (from `series.go`).

**Test.** `sqlite/rollup_test.go`: importing N events yields the expected bucket counts; the 4xx/5xx/incomplete counters match; re-import does not double-count (UNIQUE upsert or delete-then-insert within the tx).

**Done-check.** `go test ./internal/casestore/... ./internal/audit/...`; a benchmark in `internal/perfbench` shows zoom reads hitting only `rollups`. **Do not** claim <15 ms in a test that cannot prove it on the target hardware **(unverified).**

---

## Packet P10 — Coverage-ledger UI

**Goal.** Show `coverage` rows as the "Incomplete source coverage" banner already present in `ActivityContext.svelte` (verified: `coverageErrors` banner text).

**Files.** `web/src/components/ActivityContext.svelte`, `web/src/lib/api.ts`, server route that returns coverage.

**Test.** `web/.../coverage.test.ts`: a `complete=0` row renders the banner and names the source label.

**Done-check.** `npm test` green.

---

# MVP-3

## Packet P11 — Layer 4: Mongo application DB + host metrics

**Goal.** Add a fourth telemetry layer: the application's Mongo DB and host/process metrics, imported as events and shown as a lane.

**Files.** `internal/importer/mongoexports/*` (`NewProfile`, `NewCurrentOp`, `NewServerStatus`, `NewMongostat` — verified in `internal/importer/all/all.go`), `internal/resource/metrics.go` (`HostCPU`, `HostMemAvailable`, `ProcCPU`, `ProcWS`, … verified), `internal/blackbox/reader.go` (`ReadSamples`, `ProbeSource` verified).

**Rule.** These are importers like any other: they emit `evidence.Event`s and go through `Import`. Layer 4 gets lane key `LaneMongo` for app-DB documents and a **new** `LaneHost` for metrics (add to `LaneOf` and to `BuildLanes`'s `keys` when a host source exists — verified `keys := []string{LaneSQL, LaneMongo, LaneRedis, LaneIIS}`).

**Test.** `internal/calltrace/lanes_test.go`: a `host-metrics` source yields a `LaneHost`; a Mongo app-DB document yields `LaneMongo`. `go test ./internal/resource/... ./internal/importer/...` green.

**Done-check.** Layer 4 events appear in a case and in the trace.

**(unverified: whether a host-metrics importer already exists with a nameable prefix; open `internal/importer/all/all.go` and `internal/resource/metrics.go` and pick the real prefix before coding `LaneHost`.)**

---

## Packet P12 — 3-tier implementing-agent matcher

**Goal.** Match a trace's symptom to the tier of agent that can fix it: Tier 1 (config/infra), Tier 2 (schema/query), Tier 3 (application code). Output is a ranked list with the evidence that drove each match.

**Files.** New: `internal/investigate/agent_match.go`. Reuse `internal/investigate` (verified package, driven by `internal/server/investigation.go`), `internal/ledger` (hypotheses), `internal/audit` (`PatternReport`).

**Signature.**
```go
type AgentTier int
const (Tier1Agent AgentTier = 1; Tier2Agent AgentTier = 2; Tier3Agent AgentTier = 3)
type AgentMatch struct {
    Tier     AgentTier `json:"tier"`
    Reason   string    `json:"reason"`   // plain words
    Evidence []string  `json:"evidence"` // evidence ids / lane refs
    Score    float64   `json:"score"`
}
func MatchAgents(in investigate.Input) ([]AgentMatch, error) // sorted by Score desc, stable
```

**Rules (deterministic, testable).** Tier 1: a lane shows `StateClockUnchecked` or a database-health check `StateNotYet`/profiler-off. Tier 2: a rollup shows a p95 spike with a SQL lane event naming the same window but no IIS error. Tier 3: an IIS 5xx event joins (uniquely) to an audit call whose SQL lane is clean. Each rule returns the reason sentence and the evidence ids it used. **No match is asserted on ambiguous joins** (`evidence.JoinAmbiguous`).

**Test.** `investigate/agent_match_test.go`: table-driven, one fixture per rule, asserts tier order and that ambiguous joins produce no Tier-3 match.

**Done-check.** `go test ./internal/investigate/...`; the server exposes the matches on the investigation response.

---

# Phase gates (each phase shippable)

- **MVP-1 done:** IIS stores IP/referer/cookie-hash; export scrub defaults ON and is proven by `TestBuildCaseNoIPsOrUserIDs`; trace shows API-audit + SQL + IIS lanes; toolbar toggles them; tooltips explain via the dictionary.
- **MVP-2 done:** live pulls persist and are idempotent; `coverage`, `hydration`, `rollups` tables exist in `case.db` (v5) and are written inside the import transaction; zoom reads rollups.
- **MVP-3 done:** Layer 4 imports and lanes work; `MatchAgents` returns ranked tiers with evidence.

# Global done-check for the whole paper

Run once per phase, not per packet:
```
go test ./... 
```
plus the web test command from `web/package.json` **(unverified)**. Then verify the storage decision holds: `rg "\.unravis\.db" .` returns **nothing**, and `rg "CREATE TABLE" internal/casestore/sqlite/schema.go` shows every new table (`coverage`, `hydration`, `rollups`) inside `schemaSQL`.

# What this paper refuses to promise

- No ingest speed target above the measured **9–16k events/s**.
- No "<15 ms zoom" or "<100 ms reopen" as a test assertion; rollups make zoom fast in practice but the number is **(unverified)** on target hardware.
- No greenfield `.unravis.db`.
- No raw cookies, ever. No cookie of an unnamed type, ever.
- No server IP, no query string, no user agent in events.

**Status of verification.** Every symbol, table, constant and route named above was read in the working tree, except items explicitly marked **(unverified)**: the `Options` file location for `CookiePolicy`; the exact allowlist prefix-match site in `case.go`; the HTTPERR `#Fields` presence of IP/referer/cookie columns; the `web/package.json` test command; the exact `types.go` line numbers; and whether a host-metrics importer prefix already exists. A packet touching any of those must open the file first.