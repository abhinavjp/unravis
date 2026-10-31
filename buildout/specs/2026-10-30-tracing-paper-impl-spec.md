# Unravis tracing paper: implementation spec

## 0. How to use this document

1. Read section 1 (phases) and section 4 (data model) first; the rest assumes both.
2. You get ONE work packet (section 9 / Part B) per turn. Implement only that packet, except a dependency-adjacent edit: an implementation packet may edit a file not on its list only when a listed file fails to compile or an existing invariant/test would break without the change; the edit must be minimal, add no feature, and be reported in the completion summary (file, symbol, one-line reason). Any public contract, schema or route change is not dependency-adjacent and must be escalated. If a packet names a symbol, open the file and match its style; **line numbers are hints, symbols are authoritative** (open files by symbol name).
3. Forbidden: spawning sub-agents; adding features not in a packet (no scope creep); printing secrets, raw cookies, or mounted payloads; running destructive git/filesystem commands. Never weaken crash atomicity of `case.db`.
4. Every claim here was re-opened in this checkout. Facts I could not confirm are marked `(unverified)` and are also listed in section 12; a packet touching one must verify locally first.
5. Status words used exactly: **Working**, **Partial**, **Not wired**, **Not built**, **Unknown**.

## 1. Goal, phases, non-goals

**Goal.** One case-first read-only surface: add data sources → press **Start tracing** → read one shared UTC timeline with three lanes (**API audit**, **SQL**, **IIS**) on **`TracingPaper.svelte`** → drill down to existing surfaces (Run analysis, Evidence record, per-call tray when wired). The unified tracing chart is a **new** surface; the call-trace tray is left unchanged. Tracing is the front door; the 11-step guided analysis stays in the app and the repo but is no longer offered on the case home (D-2 = A).

| Phase | Ships alone: includes | Explicitly NOT in phase |
|---|---|---|
| **MVP-1** | Three-lane tracing for a window (audit/SQL/IIS); IIS stores client IP + referer + allowlisted cookie **hash**; export scrub default ON; tooltip dictionary; canvas toolbar (zoom, lane toggles, percentile selector); HTTPERR point markers; an IIS event is drawn as a point at its timestamp with the observed `time-taken` shown separately until VF-01 confirms the source semantics (no derived request start; never called API processing time). | No case rollups in MVP-1; the live audit lane is bucket-aggregated on the server (§5.4), not rebuilt by summing cohorts in the browser. |
| **MVP-2** | Live SQL/Mongo pulls **saved** into `case.db`; `coverage`, `hydration`, `rollups` tables in `case.db` (schema v5); lazy hydration (read deferred columns locally only); rollup-backed zoom; deferred open-time verify ("verifying" state). | No new storage file; no cross-process hydration. |
| **MVP-3** | Layer 4 lane (Mongo app DB documents + host metrics); 3-tier implementing-agent matcher. | No write queries; no auto-causation. |
| **Later** | IIS-vs-audit delta anomaly overlay; win32-status partitioned series (labelled **estimate**); persist trace window; route filter on the trace. | — |
| **Never** | Separate `.unravis.db`; raw cookie values; server IP / query string / user agent in events; write queries to customer DBs from the tracing UI; causation claims on the timeline. Guided analysis is not deleted; tracing replaces it as the front door. | — |

## 2. Glossary (ELI10)

- **Tracing** — Looking at one incident window with the web log, the database log, and the API log lined up on the same clock.
- **Lane** — One horizontal row of the chart, one telemetry source (audit, SQL, IIS).
- **Bucket** — One half-open presentation slice `[axisStart+k*W, axisStart+(k+1)*W) ∩ [start,end)` on the UTC floor-aligned grid (say 60 s); an event at `start` is in and an event at `end` is out.
- **Rollup** — A pre-computed per-bucket summary (count, sum, min, max) saved so the chart does not re-read every event.
- **Hydration** — Reading a heavy field (a big payload, an XML plan) only when clicked, instead of at import; "hydrate" means lazily reading the already-persisted `events.attrs_json`/payload column from `case.db`, never a network read.
- **Coverage ledger** — A list of which source windows were already read. Three operations are distinct: `POST .../tracing` may run one ephemeral read-only live read for the audit lane and never persists; `GET .../coverage` reads `case.db` only, reports gaps, and never fills them; `POST .../pulls` is the only operation that reads remote SQL/Mongo and persists events+coverage. `allLanesStored` exists **only** on `POST .../tracing` responses and means "every lane has `origin:"case"`"; `GET .../coverage` returns `{entries:[...], asOf, gaps:[...], provenance:"case-ledger"}` and **never** carries `allLanesStored`. There is no `bypassLedger` and no `fromLedger`.
- **Cohort** — One group of API calls sharing a route/kind/outcome in the audit data.
- **Approximate value** — A percentile shown with lower and upper bounds and the word "approximate".
- **Aligned grid** — Presentation axis `axisStart = floorUTC(start,3600000)`, `axisEnd = ceilUTC(end,3600000)`; never re-anchored. There is exactly one presentation axis; the ladder gives the span-nominal rung and the presented width is `max(ladderWidth(span), alignedRung(axisStart, axisEnd))`, where `alignedRung` is the first supported rung in `[60s,300s,600s,900s,1h,2h,4h,6h,8h,12h,24h]` whose aligned bucket count is ≤120.
- **`durationStatus`** — `"ok"|"missing"|"invalid"`; `invalid` keeps the event with null duration, increments `count_all` only.
- **`ProjectEventAttrs`** — The single read-side projection function; default drops `client-ip`, `referer`, every `cookie:*`; opt-in explicit.
- **Audit aggregation** — One server-side `$match`+`$group` per source under `CensusQueryBudget`; not a snapshot. Retires `BroadScanBuckets`.
- **Content digest** — A canonical row-content digest: lowercase-hex SHA-256 over a versioned, deterministic serialization of the ordered observed rows, one line per row, in the source's observed order (tie-broken by `_id`), with every evidence-affecting observed field length-prefixed and type-tagged including explicit nulls; `content_digest_v` records the canonicalization version (starting 1); drives `unchanged` vs `changed`.
- **`serverWorkId`** — Retired in MVP-1 (R5-11).

## 3. Where the app stands

| Capability | Status | Evidence |
|---|---|---|
| Case create + sources | Working | `internal/server/cases.go` `createCase` (~726); sources via `case_sources.go` |
| Encrypted per-case `case.db`, unified `events` table | Working | `internal/casestore/sqlite/schema.go:29-31` (opened) |
| IIS W3C → `events` | Working | `internal/importer/iis/w3c.go:104-188` (opened) |
| IIS drop-at-parse policy (IP/referer/cookie) | Working (to be reversed) | `internal/importer/iis/common.go:5-7` (opened) |
| Test pinning the drop | Working | `internal/importer/iis/iis_test.go:196-212` `TestW3CDropsSensitiveColumns` (opened) |
| HTTPERR import | Working | `internal/importer/iis/httperr.go:39-52` (opened) |
| SQL importer events (`sql-*`) | Working | `internal/importer/all/all.go:40-43` (`NewXE`, `NewErrorlog`, `NewPDH`, `NewQuerystore`) |
| Query Store durations converted µs→ms | Working | `internal/importer/sqlserver/querystore.go:38-41` (opened) |
| Live SQL/Mongo lane read (ephemeral) | Working | `internal/server/routes.go:130-137`; `internal/server/lanes.go:16-18` (opened) |
| Mongo profiler read (`profile:-1` never sets level) | Working | `internal/dbhealth/lane_mongo.go:10-11,207` (opened) |
| `/api/analyze` (one series per cohort) | Working | `internal/server/routes.go:192` (opened) |
| Guided 11-step analysis (kept, unlisted; D-2=A) | Working | `web/src/lib/components/guided/GuidedAnalysis.svelte`; `web/src/lib/guided/stepModel.ts`; described in `inputs/CURRENT-APP-FLOWS.md` Part 4 (unverified line) |
| Cohort aggregation for hero chart | Working | `web/src/lib/charts.ts` (cited by errata; `(unverified line)`) |
| Call trace UI reachable from Evidence | **Not wired** | no Svelte import/render of `CallTrace.svelte` outside its file; `addToTray` unused (verified by grep 2026-10-30) |
| Case text timeline UI | Working | `web/src/lib/components/CaseTools.svelte:8,155-158`; `Timeline.svelte:1-24` (opened) |
| `LaneEvent` carries HTTP status | **Not built** (no status field) | `internal/calltrace/types.go:71-78` (opened) |
| Combined API+SQL+IIS one-shot chart | **Not built** | no `tracing.go`; `POST /api/cases/{id}/tracing` absent from `routes.go`. Guided components live under `web/src/lib/components/guided/` and `web/src/lib/guided/` (kept, unlisted). |
| Case home **Tracing** entry | **Not built** | WP-16; `CaseHome.svelte` has Run analysis only (~453–457). Case home also has a **Guided** button (`CaseHome.svelte:460-463`) and route (`GuidedAnalysis.svelte` mounted at line 376) that WP-64 swaps for the Start tracing button; the guided route/component stay reachable but unlisted. |
| Coverage ledger / hydration / rollups / import_attempts tables | **Not built** | `internal/casestore/sqlite/schema.go:24-34` (opened) |
| IIS `time` = end | **Unknown** | `internal/importer/iis/w3c.go:14-21` (opened, `w3cTimeIsEnd=true`) |
| TZ gate for multi-source | Working | `internal/timeline/timeline.go:363-378` (cited; not re-opened) |
| 50k event cap | Working | `internal/casestore/store.go:101-102` (opened) |
| Closed-case 30-day retention | Working | `internal/casestore/store.go:94-95` (opened) |
| Open-time verify = `quick_check` + `GROUP BY import_id` | Working (slow) | `internal/casestore/sqlite/case.go:222-275,300-328` (opened) |
| Driver = WASM SQLite, one prepared exec/event | Working | `go.mod:5`; `case.go:565,641,686` (opened) |
| Bench harness | Working | `internal/perfbench/` (opened listing) |
| Web test runner | Working | `web/package.json` `"test": "node --test tests/*.test.cjs"` (opened) |

## 4. Data model

All new tables live in `case.db`, appended to `schemaSQL` in `internal/casestore/sqlite/schema.go`, guarded with `IF NOT EXISTS`, and written **inside the import transaction**. Bump `const schemaVersion = 4` to `5` and extend the on-open migration in `internal/casestore/sqlite/discover.go` `(*Case).migrate` (called from the case open path), which already writes newer versions in one transaction (`schema.go:4-13`).

### 4.1 Existing tables (unchanged shape)

- `meta(k,v)`, `imports(...)`, `events(...)`, `quarantine(...)`, `case_sources(...)`, `case_envs(...)` — see `schema.go:29-51` (opened). In MVP-2, `imports` gains `observation_key TEXT` (the canonical `(source,lane,fingerprint,window)` scope key) and `supersedes_import_id INTEGER NULL` (proposed), with `UNIQUE(observation_key)` for pull imports (NULL for file imports, so file imports are unaffected).

### 4.2 IIS privacy fields (MVP-1) — no new columns

New facts are attributes inside `events.attrs_json` (validated against the importer allowlist by `evidence.Event.ValidateFor` in `internal/evidence/event.go`; line numbers are hints):

| Attr key | Rule |
|---|---|
| `client-ip` | `net.ParseIP`-valid, no host/port; ≤64 bytes. |
| `referer` | ≤512 bytes; `url.Parse`, scheme http/https only; keep scheme+host+path, **drop query and fragment**. |
| `cookie:<name>` | name lower-cased and on the **per-case** allowlist (`meta` `k='cookie.allowlist'`); value is the lowercase hex of `HMAC-SHA256(key, "unravis-cookie-v1\x00" || lowercase(name) || "\x00" || rawValue)` truncated to 32 hex characters (`maxCookieDigestHex = 32`, proposed), and it is pseudonymous, not anonymous. **Never** the raw value. |
| Sentinels accepted | `W3C.AllowedAttrs()` returns its static set (`w3c.go:46-51`) plus `"cookie:*"`; `evidence.Event.Validate`/`ValidateFor` (`internal/evidence/event.go`) treats a `cookie:*` allowlist entry as a prefix match after exact matches. |

The case holds one 32-byte HMAC key in `meta` key `cookie.hmac_key_v1`, generated once from a cryptographic RNG; the stored digest is the lowercase hex of `HMAC-SHA256(key, "unravis-cookie-v1\x00" || lowercase(name) || "\x00" || rawValue)` truncated to 32 hex characters, and it is pseudonymous, not anonymous. The key is never logged, never in the manifest, never exported; a missing or short key yields no digest and no error (SC-PRV-101/18); name normalization is lower-case and trim with the `cookie:*` prefix match only after exact match (DEC4 R9); a duplicate cookie name takes the first wins (DEC4 R9); no rotation in MVP-1. Cookie allowlist: per case in `meta` `k='cookie.allowlist'` (decision D-8), default empty; each import receives `importer.Options.CookiePolicy` copied from case meta (`Options` at `internal/importer/importer.go:62`). Outward-facing event views (events API, export bundle, report, tooltip/detail) apply the single pure projection function `ProjectEventAttrs(attrs map[string]string, includeSensitive bool) map[string]string`; `includeSensitive=false` is the default and drops `client-ip`, `referer` and every `cookie:*` key, and opt-in is explicit per read/export.

### 4.3 `coverage` (MVP-2)

```sql
CREATE TABLE IF NOT EXISTS coverage(
  id INTEGER PRIMARY KEY,
  import_id INTEGER NOT NULL REFERENCES imports(id) ON DELETE CASCADE,
  source TEXT NOT NULL REFERENCES case_sources(id) ON DELETE CASCADE, lane TEXT NOT NULL,
  from_ms INTEGER NOT NULL, to_ms INTEGER NOT NULL,
  rows_read INTEGER NOT NULL, pages_read INTEGER NOT NULL,
  complete INTEGER NOT NULL CHECK(complete IN (0,1)),
  fingerprint TEXT NOT NULL DEFAULT '',
  fingerprint_v INTEGER NOT NULL DEFAULT 1,
  note TEXT, content_digest TEXT,
  content_digest_v INTEGER NOT NULL DEFAULT 1,   -- canonicalization version (proposed), mirrors fingerprint_v
  at_utc INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS cov_src_fp ON coverage(source, fingerprint, from_ms, to_ms);
```

**Window cap.** A coverage row records a read window; `to_ms - from_ms` may be at most one month (30 days), the same cap as the trace window (`MaxTraceWindow`). A longer write is refused with a plain error (proposed: WP-28; the DDL above sets no such constraint). Coverage intervals are **half-open** `[from_ms, to_ms)`: an event at exactly `from_ms` is included and one at exactly `to_ms` belongs to the next adjacent interval. Coverage rows are keyed by `source` (the immutable `case_sources.id`, not the importer kind) plus a query fingerprint, so rows with different filters never merge. The coverage fingerprint is over the source's own *filter* only — `'unravis-coverage-v1\x00' || sourceId || '\x00' || lane || '\x00' || canonicalFilters` — and the read window lives only in `from_ms`/`to_ms`; it is never over the other sources in the same request, and it is recorded in `fingerprint` with its canonicalization version in `fingerprint_v` (default 1). `allLanesStored` exists **only** on `POST .../tracing` responses and means "every lane has `origin:"case"`"; `GET .../coverage` never carries it. A result assembled from already-stored `coverage`/`rollups` returns `allLanesStored:true` when every lane has `origin:"case"`; a request bit is not needed to re-fetch it. Three operations are distinct: `POST .../tracing` may run one ephemeral read-only live read for the audit lane and never persists; `GET .../coverage` reads `case.db` only, reports gaps, and never fills them; `POST .../pulls` is the only operation that reads remote SQL/Mongo and persists events+coverage. Coverage means 'completely read as of `asOf`', not 'current'. Late arrivals after an import are only seen when the source is re-imported (which changes the import), never by a TTL daemon or watermark.

### 4.3.1 `import_attempts` (proposed, MVP-2)

A small separate table written **outside** the import transaction, so a failed pull is still recorded after the events and coverage rows roll back whole. An attempt row never counts as coverage. `import_attempts` carries the same fingerprint and the `attempt_id` of the failed import so an attempt is matched to exactly the query it failed. `coverage.source` is `TEXT NOT NULL REFERENCES case_sources(id) ON DELETE CASCADE`; `import_attempts.source` is historical `TEXT` with **no** FK (a tombstone survives the source), carries `source_label` (the label at attempt time), and rows older than 30 days are pruned when the case is opened; deleting an import cascades `coverage`.

```sql
CREATE TABLE IF NOT EXISTS import_attempts(
  id INTEGER PRIMARY KEY,
  source TEXT NOT NULL,
  source_label TEXT NOT NULL DEFAULT '',
  fingerprint TEXT NOT NULL DEFAULT '',
  fingerprint_v INTEGER NOT NULL DEFAULT 1,
  import_id INTEGER REFERENCES imports(id) ON DELETE SET NULL,
  from_ms INTEGER NOT NULL, to_ms INTEGER NOT NULL,
  started_at INTEGER NOT NULL,
  outcome TEXT NOT NULL,                     -- "requested" | "completed" | "changed" | "unchanged" | "incomplete" (proposed)
  error TEXT,
  pages_ok INTEGER NOT NULL DEFAULT 0
);
```

### 4.4 `hydration` (MVP-2)

```sql
CREATE TABLE IF NOT EXISTS hydration(
  import_id INTEGER NOT NULL REFERENCES imports(id) ON DELETE CASCADE,
  field TEXT NOT NULL,                       -- "payload" | "headers" | "attrs.giant"
  present INTEGER NOT NULL CHECK(present IN (0,1)),
  bytes INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY(import_id, field)
);
```

A field not listed counts as absent. "Hydrate" means lazily reading the already-persisted `events.attrs_json`/payload column from `case.db`; never a network call, and the `hydration` table is only a presence/size hint, never a byte store. No cross-process hydration this phase. Acceptance: hydration opens no network socket; a missing field reads as absent; deleting an import cascades to its `hydration` rows.

### 4.5 `rollups` (MVP-2, the zoom fix)

```sql
CREATE TABLE IF NOT EXISTS rollups(
  id INTEGER PRIMARY KEY,
  source TEXT NOT NULL, kind TEXT NOT NULL,
  bucket_ms INTEGER NOT NULL,
  at_ms INTEGER NOT NULL,                    -- bucket start, UTC-aligned
  count_all INTEGER NOT NULL DEFAULT 0,
  count_with_duration INTEGER NOT NULL DEFAULT 0,
  sum_us INTEGER NOT NULL DEFAULT 0,          -- integer microseconds, overflow-checked; exact
  min_us INTEGER, max_us INTEGER,             -- integer microseconds; exact
  bins BLOB,                                  -- per-rollup log-histogram bins (internal/audit/histogram.go layout)
  count_4xx INTEGER NOT NULL DEFAULT 0,
  count_5xx INTEGER NOT NULL DEFAULT 0,
  incomplete INTEGER NOT NULL DEFAULT 0,
  UNIQUE(source, kind, bucket_ms, at_ms)
);
CREATE INDEX IF NOT EXISTS roll_source_at ON rollups(source, at_ms);
```

Levels written at import end (same transaction, after `indexSQL`): WP-30 writes `bucket_ms = 60000`; WP-31 writes and verifies `bucket_ms = 3600000`; both are UTC-aligned. Rollups store `sum_us` (integer microseconds, overflow-checked) instead of a floating sum. A duration is stored as integer microseconds: `roundHalfToEven(ms * 1000)` for a finite nonnegative millisecond value; a non-finite, negative or `> maxInt64` duration keeps the event with no duration and `durationStatus:'invalid'`; it increments `count_all` and not `count_with_duration`, no invalid float is ever serialized, and only records that cannot be safely identified or that violate envelope integrity are quarantined; an event with a missing duration is written and increments `count_all` only; `count_all` is every stored matching event and `count_with_duration` is those with a duration, and `min_us`/`max_us` are integer microseconds so AC-20's exactness holds in integer arithmetic. Conversion `toMicros(ms float64) (int64, error)`: reject non-finite/negative; `int64(math.RoundToEven(ms*1000))`; refuse `> maxInt64` (overflow). Histogram bins include only finite nonnegative observed durations; when `count_with_duration == 0`, p50/p95/p99 are `null`. Percentiles are derived by merging the per-rollup log-histogram `bins` (same layout as `internal/audit/histogram.go` via `BinIndex`/`BinBounds`) and never by averaging percentiles; p50, p95 and p99 are all derived this way, and empty bins yield `null` (proposed: p99 read path). Rollups are **rebuilt transactionally** for the affected buckets from all active events at the end of each import, after a changed pull atomically supersedes its prior observation in the same transaction, and after an import is deleted; "active events" means events of imports not superseded/deleted, which after supersession is exactly one observation per scope, so overlapping or repeated imports never double count; the identity stays `UNIQUE(source, kind, bucket_ms, at_ms)`. Reuse `MaxRollupGroups=600000`, `RollupNoiseCount=5`, `DefaultRollupSlowMs=1000` (`internal/audit/rollup.go:27-38`) and refuse >`MaxSeriesBuckets=2000` (`internal/casestore/series.go:25`). In rollups, as in coverage and lane payloads, `source` is the immutable `case_sources.id`, never the importer kind; `kind` is the importer/event kind, so two IIS sites of the same kind never collapse. Presentation buckets are always half-open and the axis is UTC floor-aligned on rollup zoom; every presentation bucket is intersected with `[start,end)`, whole contained UTC-hour sub-buckets are filled from `bucket_ms=3600000` rows, each boundary fragment is filled from a bounded raw scan, at most two scans, one shared query when both fragments lie in the same grain-hour, and a bucket is exact only when all its pieces completed: `clipped=true` marks a normal geometric window edge and `complete=false` marks missing or unfinished data, and these two flags are never interchanged. The presentation axis is UTC floor-aligned at every rung (`axisStart = floorUTC(start,3600000)`, `axisEnd = ceilUTC(end,3600000)`); presentation bucket `k = [axisStart + k*W, axisStart + (k+1)*W) ∩ [start,end)`; count on the aligned grid is `ceil((axisEnd-axisStart)/W)`, the ladder gives the span-nominal rung `ladderWidth(span)` and the presented width is `max(ladderWidth(span), alignedRung(axisStart, axisEnd))`, where `alignedRung` is the first supported rung `[60s,300s,600s,900s,1h,2h,4h,6h,8h,12h,24h]` whose aligned bucket count is ≤120, so the aligned count is always ≤120 and the number of boundary raw scans is zero when neither edge is set, one when exactly one edge is set or when both boundaries fall inside the same grain-hour (one bounded raw query serves both), and two otherwise; a 30-day span is 2,592,000,000 ms and the presented width `W = max(ladderWidth(span), alignedRung(axisStart, axisEnd))` is 21,600,000 ms (6 h nominal) only for a 30-day window whose start is on a UTC 00:00 hour boundary; a misaligned 30-day start coarsens to the next supported rung (8 h, count 91), and every expected bucket width in a test is derived from integer time-boundary arithmetic, never copied from an illustrative value; `clipped = (bucket touches start or end geometrically)`; `complete = (every contained hour row present AND edge scan completed)`; `at_ms` is the UTC floor-aligned start. A trace window is never rejected and never split because its grid is misaligned: there is exactly one presentation axis, the UTC floor-aligned, never-re-anchored grid with `axisStart = floorUTC(start,3600000)` and `axisEnd = ceilUTC(end,3600000)` and half-open aligned buckets intersected with `[start,end)`, and the presented width is `max(ladderWidth(span), alignedRung(axisStart, axisEnd))` so the aligned count is always ≤120 and the number of boundary raw scans is zero when neither edge is set, one when exactly one edge is set or when both boundaries fall inside the same grain-hour (one bounded raw query serves both), and two otherwise. Edge budget (proposed): `MaxEdgeScanRows = 50000` per fragment, `MaxEdgeScanTime = 2s`; the same-grain one-scan optimisation must not double the budget, and `complete=false` on any fragment over budget; on exceed, `complete=false` and a `partial`/`Scanned` reason, never a claimed exact total.

### 4.6 Migration steps

1. Bump `schemaVersion` to 5; append the four DDL blocks to `schemaSQL`.
2. In the open path (`discover.go` `migrate`), when `v<5`, execute the same text with `IF NOT EXISTS` and write version 5, all inside one transaction: the migration **verifies the table shape and the version** before it commits, and a failed migration rolls back so no partial version change is left. A v4 case gains the tables only.
3. Per connection set `PRAGMA foreign_keys=ON` before any transaction and run `foreign_key_check` on open. `refusesSchema` already refuses a newer-than-known case; keep it (the newer-schema file is refused, never opened).
4. Rollups and coverage are written **inside** `Import` (after the event loop, before `tx.Commit()` at `case.go:771-777`), so crash atomicity is unchanged. `import_attempts` is written **outside** the import transaction, so a failed pull still records its attempt while events and coverage roll back whole. Rows in `import_attempts` older than `MaxAttemptRetention` (30 days, proposed) are pruned inside the case-open transaction. The `case.revision` primitive is packet WP-65a (proposed), which owns only the `case.revision` storage/default (`case.revision = 0`), `func bumpCaseRevision(tx *sql.Tx) error`, and storage-level tests proving the bump is monotonic and transactional, and does not claim mutation-site wiring; WP-65b (proposed, new, MVP-1) owns every mutation-site integration with named symbols/files and independent before/after revision assertions; WP-65 remains the confirm-integration packet and depends on WP-65a, WP-65b, WP-06 and WP-07. The confirm revision check and byte production run inside one read transaction.

### 4.7 Caps (unchanged, reused)

50 000 events/query (`store.go:102`, applies to raw rows only); 120 buckets (`audit/models.go:17`); **≤30-day (one month) trace window** (new `MaxTraceWindow`, replacing the old `MaxInterval = 7 * 24 * time.Hour` at `audit/models.go:19`); case byte caps 4 GiB (`store.go:112-114`, `DefaultCaps` opened); 30-day closed-case retention (`store.go:94-95`).

## 5. APIs

### 5.1 New: `POST /api/cases/{id}/tracing` (MVP-1)

Decision D-6: new route, not extending `/timeline` (text timeline already uses POST `/timeline`, `routes.go:278`). In MVP-1 the SQL and IIS lanes read the saved case (`sql-*`/`iis-*` events), so the 30-day claim is honest for them; the audit lane stays a live remote server-side aggregation with its own completeness and error reporting and its own source window limit, and only aligned saved SQL/IIS windows use rollups.

Request:
```json
{ "sourceIds": ["..."], "incident": {"start":"...","end":"..."},
  "reference": {"start":"...","end":"..."} }
```
**Request schema.** Strict and typed: unknown JSON fields are refused with 400 (no silent ignore); `route` is Later-only and is not in the MVP-1 schema, so an MVP-1 request with `route` returns 400 with no source read (the Later filter contract stays in WP-21/SC-API-38/39). `sourceIds` must belong to the case (a foreign id is refused 404 or 400, named per 5.3), are de-duplicated, capped at 50 (proposed), and each entry is a `case_sources.id` (the immutable source identity), never an importer kind. Incident/reference strings, `sourceIds` entries and the whole request body have size caps (proposed), and all validation happens **before any live connection opens**.
Response:
```json
{ "window": {"start":"...","end":"..."}, "bucketSeconds": 60, "disabled": null,
  "lanes": [ {"key":"audit|sql|iis","label":"...","state":"events|no-data|clock-unchecked|out-of-range|none-seen|incomplete","reason":"...","origin":"case","asOf":"...","sources":[{"sourceId":"cs_...","kind":"iis-w3c","label":"IIS site A"}],"events":[{"id":"...","source":"cs_...","kind":"iis-w3c","label":"...","start":"...","end":"...","durationMs":0,"severity":""}],"hidden":0,"scanned":0,"truncated":false,"complete":true} ],
  "allLanesStored": false,
  "warnings": ["..."], "truncated": false }
```
The response's `window` is half-open `[start, end)`. Per lane the `reason` (proposed) carries the server wording for a missing/partial state and the top-level `disabled` (proposed) carries the whole-trace gate reason, e.g. `tz-rule`. Every lane carries `origin ∈ {live, case}`, `asOf` (RFC3339 UTC), `complete`, `truncated` and `reason`; the top-level field is `allLanesStored`, true iff every lane has `origin:"case"`, and `allLanesStored` exists only on this `POST .../tracing` response and never on `GET .../coverage`. Each lane also carries (proposed) `scanned`, `truncated` and `complete` fields (ruling 7): the audit lane is a live remote server-side aggregation with its own completeness and error reporting and its own source window limit, and the SQL/IIS lanes are uncapped local `case.db` aggregates; an aggregation budget or cap failure marks the audit lane `complete=false` with a `{timeout,rowcap,error}` reason while SQL/IIS lanes keep their exact local counts. On any failed audit read → `state:"incomplete"`, `complete:false`, `reason` naming the timeout, row cap or error; the live audit lane computes its buckets with one server-side `$match`+`$group` aggregation per source under the existing 60 s `CensusQueryBudget` and the existing broad-scan gate; it never pages raw rows to build counts, and a single command is a live aggregate, not a snapshot; `complete:true` means exactly "the read command returned to end within its budget with no cap, error or unfinished boundary", and never "the source was a stable snapshot"; each lane additionally carries `consistency ∈ {"live-non-snapshot","stored-as-of","snapshot"}`, defaulting to `live-non-snapshot` for the live audit lane and `stored-as-of` for saved SQL/IIS lanes; when any lane in a trace is `live-non-snapshot` the response sets `mixedConsistency:true` and the UI shows a non-dismissible note: "live and saved lanes are read at different times; totals across lanes are not a conservation check."; `asOf` is the server clock at read start, labelled "read began at", and a note states "not a snapshot; rows inserted during the read may be included"; `complete:false` reasons are `{timeout,rowcap,error}`; an unsupported span is refused with the limit named, never silently shortened. Errors: 400 invalid span/bucket, unknown request field, or a `sourceIds` entry not on the case; 409 closed case; 423 master key locked; 503 `cases-unavailable`. Read-only: `POST /api/cases/{id}/tracing` may run one ephemeral read-only live read to a customer source (the MVP-1 live audit lane) but never persists and never writes to customer data or `case.db`; `POST /api/cases/{id}/pulls` is the only operation that reads remote SQL/Mongo and persists events+coverage and the only action that writes an import. A result assembled from already-stored `coverage`/`rollups` returns `allLanesStored:true` when every lane has `origin:"case"`; a request bit is not needed to re-fetch it. `GET .../coverage` reads `case.db` only, reports gaps, and never fills them, and opens no source connection even for an uncovered window; there is no `bypassLedger` and no `fromLedger`. No grants, no imports, no writes (pattern `internal/server/investigation.go`). MVP-1 keeps only per-source concurrency bounds, request-context cancellation, and the client-only per-tab `requestSeq`; there is no server `serverWorkId`, no subscriber count and no shared in-flight read. Two tabs with the same query each perform their own bounded live audit read. Each request sends a monotonic `requestSeq` (proposed, strictly increasing per tab); stale responses are ignored by `requestSeq` per tab; a client token is never authorization. Per-source concurrency is bounded (proposed: `MaxAuditConcurrency` 2, WP-14/16/17). Limits: ≤30d, ≤120 buckets, ≤50k events (raw rows only), `sourceIds` de-duplicated and ≤50 (proposed); lane payload reuses `calltrace.Lane*` (`types.go:71-95`); every lane returns `sources` as objects `{sourceId, kind, label}` where `kind` is the importer/event kind and is required; `readKind` and `window` are optional per-source provenance fields (`readKind ∈ {saved, live}`; `window` present only when it differs from the lane window), each lane also carries `asOf` (RFC3339 UTC, the server clock at read start) and separate `complete`, and `LaneEvent.source` is always the same `case_sources.id`.

### 5.2 Changed: export preview/confirm (MVP-1)

Add to `exportPreviewRequest`/`exportConfirmRequest` (`internal/server/export.go:34-50`, opened):
```go
ScrubSensitive *bool `json:"scrubSensitive,omitempty"`
```
Resolve `scrub := req.ScrubSensitive == nil || *req.ScrubSensitive` (**default ON**). When on, `ProjectEventAttrs` drops `client-ip`, `referer` and every `cookie:*` key from the export projection; this one pure function is the single read-side projection applied to every outward-facing event view (events API, export bundle, report, tooltip/detail), `includeSensitive=false` by default. Update the statement at `caseexport.go:751` and the "Left out by default" line at `build.go:158`. Preview response gains `scrubSensitive` (default true). The `previewId` is an advisory revision stamp, not an issuance token: `previewId = hex(SHA-256("unravis-preview-v1" || caseId || caseRevision || scrubSensitive || projectionVersion))`; preview and confirm are stateless and recompute it, confirm exports the same case revision and the same scrub policy class as the preview; it does not promise byte-identical output, because each export uses a fresh pseudonym key and records its own creation time, so a differing or missing `previewId` is answered 409 with a plain 'the preview changed; run the preview again' body, and there is no stored one-shot, expiry or 'unknown preview' 404 state. `caseRevision` is a monotonic integer in `meta` key `case.revision` (proposed), whose primitive is packet WP-65a (proposed) with no dependency on the export packets; WP-65b (proposed, new, MVP-1) owns every mutation-site wiring with independent before/after revision assertions, and WP-65 remains the confirm-integration packet and depends on WP-65a, WP-65b, WP-06 and WP-07. The `bumpCaseRevision(tx)` primitive is WP-65a; WP-65b (proposed) calls it at each named mutation site with independent before/after revision assertions, inside the same transaction for every export-affecting mutation: import committed, import deleted, source added/changed/removed, cookie allowlist or HMAC key set, and any settings change the export reads; WP-65 owns only the confirm snapshot and the 409 on concurrent import; it is not the `imports` row count. Preview response: `{"scrubSensitive":true,"previewId":"<64 hex>","projectionVersion":1,"summarizesBytes":false}` (proposed) — the preview is a schema/summary, not byte-identical output, so `summarizesBytes:false`. Confirm request: `{"caseId","previewId","scrubSensitive"}`; on match returns bytes and `sha256` of those bytes. Status codes: 200 match; 409 on any digest mismatch or missing/blank `previewId` (the only stale error); 400 malformed; 409 closed case; 423 locked key. Never export if validation fails: confirm recomputes `caseRevision`, `scrubSensitive` and `projectionVersion`, takes a read-consistent snapshot of the export inputs, refuses 409 if the revision differs before producing any bytes, and produces bytes from that same snapshot.

### 5.3 New: `GET /api/cases/{id}/coverage` (MVP-2)

Returns `{entries:[{source,lane,from,to,rowsRead,pagesRead,complete,note,contentDigest,contentDigestV,at,fingerprint,fingerprintV}], asOf, gaps:[...], provenance:"case-ledger"}` from `coverage` (`at` is `asOf`, the time the read completed); intervals are **half-open** `[from, to)`, `source` is the `case_sources.id`, and rows are keyed by source plus query fingerprint; each entry's provenance is the ledger row itself (`source`, `fingerprint`, `from`, `to`, `complete`, `content_digest`, `at`), not a lane. Three operations are distinct: `POST .../tracing` may run one ephemeral read-only live read for the audit lane and never persists; `GET .../coverage` reads `case.db` only, reports gaps, and never fills them, and opens no source connection even for an uncovered window; `POST .../pulls` is the only operation that reads remote SQL/Mongo and persists events+coverage. A stored interval satisfies a request when it contains the requested interval for the same fingerprint and `complete=1`. A result assembled from already-stored `coverage`/`rollups` is returned by `POST .../tracing` as `allLanesStored:true` when every lane has `origin:"case"`; a request bit is not needed to re-fetch it, and `GET .../coverage` never carries `allLanesStored`. Coverage means 'completely read as of `asOf`', not 'current'. A repeat of the same canonical query is otherwise skipped. A live source read is a separate, explicit, user-initiated MVP-2 pull: `POST /api/cases/{id}/pulls` (proposed) with body `{"sourceIds":[...],"window":{"start","end"}}` performs an authorized live pull, records `import_attempts` outside the import tx, writes events+coverage inside it, returns `{attemptId,outcome,eventsWritten,coverageAdded}`; 200 ok/partial, 400 bad window, 404 unknown source, 409 closed case, 423 locked key, 502 source unreachable; A live pull is an **observation** scoped by `(source, lane, fingerprint, window)` where `window` is the exact half-open interval requested, materialized as exactly one import plus its events, its coverage rows, and its rebuilt rollups. A pull is idempotent on observed content, computed as a canonical row-content digest: lowercase-hex SHA-256 over a versioned, deterministic serialization of the ordered observed rows, one line per row, in the source's observed order (tie-broken by `_id`): the stable source identity (`_id`), the source timestamp, and every evidence-affecting field actually observed (at minimum `durationMs`, `status`, `value`, `severity`, and the canonicalized observed attribute map), each field length-prefixed and type-tagged including explicit nulls; absent keys are encoded distinctly from present-null keys; duplicate `_id` within one pull makes the pull `incomplete` and no dedup is performed; the digest scopes the source, the canonical filter and the requested window via the row set, not by concatenation; `content_digest_v` records the canonicalization version (starting 1). A re-pull that observes byte-identical canonical row content is `unchanged` (`StatusDuplicate`); a re-pull that observes any changed canonical content persists a new observation (outcome `changed`). A pull that observes changed canonical content, for a scope that already has a stored `complete=1` observation whose window exactly equals the requested window, is written as a single transaction that stages and validates the new import to the same end-of-window completeness bar, deletes the superseded import(s) for that identical scope (cascading their events and coverage), inserts the new import and events, rebuilds the affected rollup buckets, and commits. A changed pull whose window does not exactly equal a stored observation's window is a partial-overlap case: it is refused with `409 window-overlap; re-pull the exact stored window or import a disjoint window`, never silently unioned; a pull is the only operation that creates or supersedes an observation; `import_attempts.outcome ∈ {requested, completed, changed, unchanged, incomplete}` (proposed). An empty ledger returns `entries:[]`, `state:"no-coverage"`, and a non-empty `gaps` for the requested window, and is never presented as stored-complete; it is never conflated with an unknown case. The coverage API also shows "last attempt failed" from `import_attempts` (an attempt row never counts as coverage). Errors follow the tracing routes in 5.1: 404 when the case is unknown, 409 for a closed case (proposed: the case handler does not document it yet; keep it consistent with `POST /tracing`), 423 for a locked master key. Feeds a **case.db ledger** banner (distinct from live `coverageErrors` on activity fetch in `ActivityContext.svelte:46`).

### 5.4 Reused endpoints

- `POST /api/analyze` (`routes.go:192`) drives the audit lane's live Mongo read. Over a 30-day window the live audit lane must aggregate **server-side by bucket** (`internal/audit/rollup.go` already rolls up live sources); the 50k event cap applies to raw rows only; the SQL/IIS lanes are uncapped local `case.db` aggregates; the audit lane is a live remote server-side aggregation with its own completeness and error reporting and its own source window limit, so an aggregation budget or cap failure marks the audit lane `complete=false` with a `{timeout,rowcap,error}` reason while SQL/IIS lanes keep their exact local counts; a `truncated` raw list means only the raw row list was cut, not that the counts are wrong. An unsupported audit span is refused with the source window limit named, never silently shortened; the tracing handler states the limit instead of returning a short window. The live audit lane computes its buckets with one server-side `$match`+`$group` aggregation per source under the existing 60 s `CensusQueryBudget` and the existing broad-scan gate, reusing `MaxRollupGroups=600000` (fail-closed `$limit+1`), `WithBroadScans` and `PerSourceConcurrency=1`; it never pages raw rows to build counts, and a single command is a live aggregate, not a snapshot; `complete:true` means the aggregation command returned to end within budget with no cap or error, and `complete:false` reasons are `{timeout,rowcap,error}`.
- `POST /api/cases/{id}/lanes/sql` / `/lanes/mongo` (`routes.go:134-137`) for live lane reads.
- `POST /api/cases/{id}/call-trace` (`routes.go:281`) and `POST /api/call-trace` (`routes.go:306`) for the per-call tray.

### 5.5 Changed: `GET /api/cases/{id}/events` read privacy (MVP-1)

One pure function `ProjectEventAttrs(attrs map[string]string, includeSensitive bool) map[string]string` is applied to every outward-facing event view (events API, export bundle, report, tooltip/detail); `eventDTO` / `toEventDTO` in `internal/server/cases.go` calls it. `includeSensitive=false` is the default and drops `client-ip`, `referer` and every `cookie:*` key, and opt-in is explicit per read/export: include them only when the client opts in (e.g. query `includeSensitive=1`), and opt-in returns the three attrs whenever the caller holds the case key (there is no forced-scrub mode). The export scrub default ON is an independent export-only decision (D-9). Never return raw cookie values.

## 6. UI (`web/src`)

| Component | Props | States | Notes |
|---|---|---|---|
| `TraceToolbar.svelte` (new) | `lanes`, `visible`, `scrubOn`, `percentile` | — | Three lane toggles (`aria-pressed`), scrub pill, p50/p95/p99 selector. The scrub pill always states the export's actual scrub state: scrub on reads "Export hides IP, referer and cookie hash"; scrub off reads "Export includes IP, referer and cookie hash (scrub is off)"; before a preview exists it reads "Default: export hides IP, referer and cookie hash"; it never says stored data is masked. Mount in `TracingPaper.svelte` (the window-tracing surface, AC-15); the per-call tray `CallTrace.svelte` stays unchanged and does not mount it. Security is keyed to the case only (no roles): the case key is the authorization. |
| `CallTrace.svelte` (keep) | `zone`, `onclose` | loading / empty / partial / error / missing-source | Currently per-call (`web/src/components/CallTrace.svelte:1-14`, opened) and stays **Not wired** otherwise unchanged; the one MVP-1 exception is WP-09's `explain()` labels. Do not layer the window trace here. |
| `ActivityContext.svelte` (extend) | `anchor`,`sourceIds`,`sourceLabels`,`zone` | coverageErrors banner already present | Add coverage banner from 5.3. |
| `Timeline.svelte` (keep) | `view`,`zone` | `!enabled` → reason | Text timeline stays; not the tracing chart. |
| Tooltip dictionary `web/src/lib/copy/dictionary.ts` (new) | `explain(id)` | never throws | Used by `charts.ts` formatters and trace tooltips. |
| Guided components (kept, unlisted) | — | — | `GuidedAnalysis.svelte` and the `guided/` folder stay in the app and the repo; they are no longer offered on the case home; Tracing is the front door (WP-63). |

States per lane come from `internal/calltrace/types.go:21-27` and `lanes.go:263-278`; wording is server-provided, never invented client-side. Missing-source, empty-window, clock-unchecked, out-of-range, truncated, no-`time-taken` each get a fixed sentence. Keyboard: toolbar buttons are real `<button>`s; the single named **Start tracing** control on the case home (`CaseHome.svelte`, WP-64) opens the trace, and focus returns on close to that control (not the per-call tray `closeTrace` pattern). Each request sends a monotonic `requestSeq`, and stale responses are ignored by `requestSeq` per tab; MVP-1 keeps only per-source concurrency bounds, request-context cancellation, and the client-only per-tab `requestSeq`; there is no server `serverWorkId`, no subscriber count and no shared in-flight read. Two tabs with the same query each perform their own bounded live audit read. Every lane carries `origin ∈ {live, case}`, `asOf`, `complete`, `truncated` and `reason`, and each lane also carries `consistency ∈ {live-non-snapshot, stored-as-of, snapshot}`; the response sets `mixedConsistency:true` when any lane is `live-non-snapshot`, and the UI shows a non-dismissible note: "live and saved lanes are read at different times; totals across lanes are not a conservation check." A client token is never authorization. Tooltips: `event.time-taken` must state the **verified** semantics (see section 12 VF-01) and `gap.time-end` must be visible until `w3cTimeIsEnd` is confirmed.

## 7. Timeline semantics

- **Lanes.** Every lane carries `origin ∈ {live, case}`, `asOf`, `complete`, `truncated` and `reason`; the top-level field is `allLanesStored`, true iff every lane has `origin:"case"`; `complete:true` means exactly "the read command returned to end within its budget with no cap, error or unfinished boundary" and never "the source was a stable snapshot"; each lane additionally carries `consistency ∈ {"live-non-snapshot","stored-as-of","snapshot"}`, defaulting to `live-non-snapshot` for the live audit lane and `stored-as-of` for saved SQL/IIS lanes, and when any lane is `live-non-snapshot` the response sets `mixedConsistency:true` and the UI shows a non-dismissible note: "live and saved lanes are read at different times; totals across lanes are not a conservation check." Every lane returns `sources` as objects `{sourceId, kind, label}` where `kind` is the importer/event kind and is required; `readKind` and `window` are optional per-source provenance fields (`readKind ∈ {saved, live}`; `window` present only when it differs from the lane window), and `LaneEvent.source` is always the same `case_sources.id`. API audit (live Mongo, server-side aggregated by bucket; the audit lane stays a live remote server-side aggregation with its own completeness and error reporting and its own source window limit, and only aligned saved SQL/IIS windows use rollups), SQL (`sql-*` saved events from `case.db`; MVP-2 adds saved live pulls), IIS (`iis-*` saved events; W3C requests + HTTPERR markers). the live audit lane computes its buckets with one server-side `$match`+`$group` aggregation per source under the existing 60 s `CensusQueryBudget` and the existing broad-scan gate; it never pages raw rows to build counts, and a single command is a live aggregate, not a snapshot; `complete:true` means the aggregation command returned to end within budget with no cap or error; `asOf` is the server clock at read start, labelled "read began at", and a note states "not a snapshot; rows inserted during the read may be included"; `complete:false` reasons are `{timeout,rowcap,error}`; an unsupported span is refused with the limit named, never silently shortened.
- **Axis.** Compute and store UTC (`timeline.go:447,508` per errata). Display via existing `TimeZoneToggle.svelte`. Windows and buckets are **half-open** `[start, end)`: an event at exactly `start` is included, one at exactly `end` belongs to the next adjacent window.
- **Bucket rule.** Bucket width is a deterministic function of the window; the rung ladder selects the width, and the one normative MVP-1 tracing bucket axis is the UTC floor-aligned presentation grid from 4.5/TR-06 (`axisStart = floorUTC(start,3600000)`, `axisEnd = ceilUTC(end,3600000)`; the grid is never re-anchored), where the presentation bucket is `k = [axisStart + k*W, axisStart + (k+1)*W) ∩ [start,end)`; the UTC floor-aligned presentation grid from 4.5/TR-06 (`axisStart = floorUTC(start,3600000)`, `axisEnd = ceilUTC(end,3600000)`; the grid is never re-anchored): the presentation bucket count is derived from the aligned axis (axisStart/axisEnd), not span/w alone, and the presented width is `max(ladderWidth(span), alignedRung(axisStart, axisEnd))`, where `alignedRung` is the first supported rung whose aligned bucket count is ≤120, so the aligned count is always ≤120 and the number of boundary raw scans is zero when neither edge is set, one when exactly one edge is set or when both boundaries fall inside the same grain-hour (one bounded raw query serves both), and two otherwise. Rungs (every rung includes its upper bound): span ≤2 h → 60 s (ladder rung; the presented width is `max(ladderWidth(span), alignedRung(axisStart, axisEnd))`, so a default 1-hour aligned window presents 60 s buckets of the same width while an off-hour window presents the aligned count, which is at most 120); 2 h < span ≤ 24 h → the smallest of 300, 600, 900 s whose count is ≤120 (so up to 10 h = 300 s, up to 20 h = 600 s, up to 24 h = 900 s); >24 h..5 d → 3600 s (hourly); >5..10 d → 7200 s (2 h); >10..20 d → 14400 s (4 h); >20..30 d → 21600 s (6 h); 8 h, 12 h and 24 h are additional supported rungs used by the coarser-rung rule below; a span over 30 d is refused with a 400. **Boundary rule:** every rung includes its upper bound as a ladder statement (the presented bucket width is always `max(ladderWidth(span), alignedRung(axisStart, axisEnd))` and the presented bucket count is the aligned count, which is at most 120), so a span of exactly 2 h maps to the 60 s ladder rung, exactly 24 h to the 900 s ladder rung, exactly 5 d to 3600 s, exactly 10 d to 7200 s, exactly 20 d to 14400 s, and exactly 30 d to 21600 s; one second more moves to the next rung, and the >24 h footnote shows only above 24 h. Rollup-backed series (MVP-2) use UTC-aligned 60 s and 3600 s rollup grains instead. Presentation buckets are always half-open and the axis is UTC floor-aligned on rollup zoom; every presentation bucket is intersected with `[start,end)`, whole contained UTC-hour sub-buckets are filled from `bucket_ms=3600000` rows, each boundary fragment is filled from a bounded raw scan, at most two scans, one shared query when both fragments lie in the same grain-hour, and a bucket is exact only when all its pieces completed: `clipped=true` marks a normal geometric window edge and `complete=false` marks missing or unfinished data, and these two flags are never interchanged. An aligned window whose presentation buckets all fall on rollup grain reads rollups only and issues no raw scan, and only a window with a clipped edge (one that does not align to the grain) serves that edge from the bounded raw scan; every benchmark states which mode it measured. `hasRawEdge = (start % grain != 0) OR (end % grain != 0)` for the rollup zoom; if `hasRawEdge == false`, no `events` scan is issued. A trace window is never rejected and never split because its grid is misaligned: there is exactly one presentation axis, the UTC floor-aligned, never-re-anchored grid, and the presented width is `max(ladderWidth(span), alignedRung(axisStart, axisEnd))` so the aligned count is always ≤120 and the number of boundary raw scans is zero when neither edge is set, one when exactly one edge is set or when both boundaries fall inside the same grain-hour (one bounded raw query serves both), and two otherwise, while the default 1-hour and ≤20-d aligned grid is unchanged. Bounds 10 s–86400 s (`audit/query.go:36-40`).
- **Clock skew.** No auto-correction. Multi-source trace stays disabled unless every source has a validated TZ rule (`timeline.go:363-378`). Join tolerance uses 1 s **timestamp resolution** only; skew between sources is not guessed (`timeline.go:156-158`, `join.Tolerance.Resolution`).
- **Missing source.** Fixed reason, never a zero line.
- **Approximate values.** p50/p95/p99 from rollups are derived by **merging the per-rollup log-histogram `bins`** (never by averaging percentiles) and show value + `≈` + lower/upper bound + "approximate" label; empty bins yield `null`. Counts, `sum_us` (integer microseconds), `min_us`/`max_us` (integer microseconds) are **exact** in integer arithmetic; a non-finite, negative or `> maxInt64` duration keeps the event with no duration and `durationStatus:'invalid'`; it increments `count_all` and not `count_with_duration`, no invalid float is ever serialized, and only records that cannot be safely identified or that violate envelope integrity are quarantined; an event with a missing duration is written and increments `count_all` only; `count_all` is every stored matching event and `count_with_duration` is those with a duration. Each lane carries (proposed) `scanned`, `truncated` and `complete` fields: the audit lane is a live remote server-side aggregation with its own completeness and error reporting and its own source window limit, and the SQL/IIS lanes are uncapped local `case.db` aggregates; an aggregation budget or cap failure marks the audit lane `complete=false` with a `{timeout,rowcap,error}` reason while SQL/IIS lanes keep their exact local counts; a lane carries `complete:false` only if its own aggregate cannot complete, and a `truncated` raw list means only the raw row list was cut, not that the counts are wrong. A partial result always carries `Partial`/`Scanned` (Appendix P P9) and the UI never renders a partial bucket as complete.
- **Resolutions.** IIS logs are 1-second granular (gap `gapW3CResolution`, `w3c.go:28-30`); IIS `time-taken` span is an estimate until VF-01 confirms.

## 8. Requirements

| ID | Requirement | Phase | Must/Should | Evidence |
|---|---|---|---|---|
| TR-01 | Case home **Tracing** entry (the case-home **Guided** button is swapped for Start tracing) | MVP-1 | Must | WP-16: `CaseHome.svelte` + `TracingPaper.svelte`; `CaseHome.svelte:460-463` holds the Guided button WP-64 swaps out |
| TR-02 | Windows ≤30d UTC validation | MVP-1 | Must | new `MaxTraceWindow` in `internal/audit/models.go` (next to `MaxInterval = 7 * 24 * time.Hour` at line 19); `internal/audit/query.go:76-77` still caps the old guided interval and must not be reused by tracing |
| TR-03 | Start tracing read-only | MVP-1 | Must | `investigation.go:17-21` pattern `(unverified line)` |
| TR-04 | Three lanes audit/sql/iis; each lane carries `consistency ∈ {live-non-snapshot,stored-as-of,snapshot}` (defaulting to `live-non-snapshot` for the live audit lane and `stored-as-of` for saved SQL/IIS lanes) and the response sets `mixedConsistency:true` when any lane is `live-non-snapshot` | MVP-1 | Must | `calltrace/lanes.go:42-53` (opened) |
| TR-05 | Shared UTC axis; UI local toggle | MVP-1 | Must | `timeline.go:447,508` (errata) |
| TR-06 | Label bucket size; ≤120 buckets; the presentation axis is UTC floor-aligned at every rung (`axisStart = floorUTC(start,3600000)`, `axisEnd = ceilUTC(end,3600000)`) and the grid is never re-anchored; the presented width is `max(ladderWidth(span), alignedRung(axisStart, axisEnd))`, where `alignedRung` is the first supported rung in `[60s,300s,600s,900s,1h,2h,4h,6h,8h,12h,24h]` whose aligned bucket count is ≤120, so the aligned count `ceil((axisEnd-axisStart)/W)` is always ≤120 and the number of boundary raw scans is zero when neither edge is set, one when exactly one edge is set or when both boundaries fall inside the same grain-hour (one bounded raw query serves both), and two otherwise; a 30-day span is 2,592,000,000 ms and the presented width `W = max(ladderWidth(span), alignedRung(axisStart, axisEnd))` is 21,600,000 ms (6 h nominal) only for a 30-day window whose start is on a UTC 00:00 hour boundary; a misaligned 30-day start coarsens to the next supported rung (8 h, count 91) | MVP-1 | Must | `audit/models.go:17` (opened); R4-001 |
| TR-07 | Missing-lane reasons, not zeros; every lane carries `origin ∈ {live, case}`, `asOf`, `complete`, `truncated`, `reason` and `consistency ∈ {live-non-snapshot,stored-as-of,snapshot}`; `complete:true` means exactly "the read command returned to end within its budget with no cap, error or unfinished boundary", never "the source was a stable snapshot"; when any lane is `live-non-snapshot` the response sets `mixedConsistency:true` | MVP-1 | Must | `lanes.go:263-278` (opened) |
| TR-08 | Warn on reference/incident overlap | MVP-1 | Should | non-blocking pattern |
| TR-09 | Click bucket → hand off to analysis (opening Run analysis / the audit sub-window from a clicked bucket) | MVP-1 | Must | `routes.go:192` (opened) |
| TR-10 | 50k cap + per-lane `scanned`/`truncated`/`complete`; the live audit lane computes its buckets with one server-side `$match`+`$group` aggregation per source under the existing 60 s `CensusQueryBudget` and broad-scan gate; it never pages raw rows to build counts, and a single command is a live aggregate, not a snapshot; `complete:true` means the read command returned to end within its budget with no cap, error or unfinished boundary, and never that the source was a stable snapshot; each lane carries `consistency ∈ {live-non-snapshot,stored-as-of,snapshot}` and the response sets `mixedConsistency:true` when any lane is `live-non-snapshot`; a truncated raw lane is labelled "first N rows" and claims no exact totals | MVP-1 | Must | `store.go:101-102` (opened) |
| TR-11 | TZ validated before multi-source | MVP-1 | Must | `timeline.go:363-378` |
| TR-12 | **`TracingPaper.svelte` is the MVP-1 tracing chart; do not extend or replace the per-call `CallTrace.svelte` tray** | MVP-1 | Must | WP-16 vs WP-11 scope |
| TR-13 | No grants/imports from tracing API | MVP-1 | Must | read-only routes |
| TR-15 | One pure function `ProjectEventAttrs(attrs map[string]string, includeSensitive bool) map[string]string` is applied to every outward-facing event view (events API, export bundle, report, tooltip/detail); `includeSensitive=false` is the default and drops `client-ip`, `referer` and every `cookie:*` key, and opt-in is explicit per read/export | MVP-1 | Must | `cases.go` `toEventDTO`; WP-07 |
| TR-16 | >24h hourly footnote | MVP-1 | Should | `(unverified wireframe)` |
| TR-18 | E2E three lanes on a **seeded** demo | MVP-1 | Must | demo has no case events (see risk) |
| TR-19 | Store IIS client IP + referer | MVP-1 | Must | reverse `common.go:5-7` (opened) |
| TR-20 | Cookie allowlist → one digest only from a single 32-byte-key HMAC over `"unravis-cookie-v1\x00" || lowercase(name) || "\x00" || rawValue`, truncated to 32 hex characters; pseudonymous, not anonymous | MVP-1 | Must | new attrs `(Not built)` |
| TR-21 | Export scrub IP/referer/cookie-hash, default ON through the single `ProjectEventAttrs` projection; `previewId` is an advisory revision stamp (not an issuance token), preview and confirm are stateless and recompute it, and a differing or missing/blank `previewId` is answered 409 with no bytes; confirm exports the same case revision and the same scrub policy class as the preview and does not promise byte-identical output, because each export uses a fresh pseudonym key and records its own creation time | MVP-1 | Must | `export.go:34-50`; `caseexport.go:751` (opened) |
| TR-22 | Tooltip dictionary | MVP-1 | Should | existing glossary |
| TR-23 | Canvas toolbar | MVP-1 | Should | Pulse zoom pattern |
| TR-24 | Coverage ledger and rollups (one umbrella capability; leaf coverage tests carry @TR-33 and leaf rollup tests carry @TR-34) | MVP-2 | Must | not in `schema.go:24-34` (opened): `coverage` + `rollups` tables |
| TR-25 | Lazy hydration flags; "hydrate" reads the already-persisted `events.attrs_json`/payload column in `case.db` (never a network read); a missing field is absent; cascade on import delete. The `hydration` table is only a presence/size hint. | MVP-2 | Must | `internal/store/payload.go` fetch-only; Appendix P8 |
| TR-26 | User-initiated live pulls saved; `POST .../pulls` is the only operation that reads remote SQL/Mongo and persists events+coverage; a pull is idempotent on observed content, computed as a canonical row-content digest: lowercase-hex SHA-256 over a versioned, deterministic serialization of the ordered observed rows, one line per row, in the source's observed order (tie-broken by `_id`), with every evidence-affecting observed field length-prefixed and type-tagged including explicit nulls, and `content_digest_v` records the canonicalization version (starting 1); a re-pull that observes byte-identical canonical row content is a no-op (`StatusDuplicate`, outcome `unchanged`; Pull now always re-reads the source with no TTL freshness reuse, and only persistence is skipped); a re-pull that observes changed canonical content atomically replaces the prior observation for the same exact window in one transaction (outcome `changed`); a changed pull whose window does not exactly equal a stored observation's window is refused with `409 window-overlap` | MVP-2 | Must | `lanes.go:21-22` (opened); Appendix P P6 |
| TR-27 | Layer 4 lane | MVP-3 | Must | `resource/metrics.go` |
| TR-28 | 3-tier implementing-agent matcher | MVP-3 | Must | `store/analyze.go` Controller/Action only |
| TR-29 | Reuse investigation filters on the trace (split out as a Later requirement; MVP-1 filters are removed) | Later | Should | `investigation.go:55-63` (opened) |
| TR-30 | HTTPERR markers | MVP-1 | Should | `httperr.go:25-52` (opened) |
| TR-31 | IIS event as a point at its timestamp with observed `time-taken` shown separately until VF-01 confirms the semantics (no verified request start; never API processing time) | MVP-1 | Must | `w3c.go:180-188` (opened); gated on WP-45 |
| TR-32 | Extend lane payload with HTTP status/win32 | MVP-1 | Must | `types.go:71-78` (opened) |
| TR-33 | Coverage ledger and query fingerprint (half-open per-source read windows; the fingerprint is the lowercase-hex SHA-256 over `'unravis-coverage-v1\x00' || sourceId || '\x00' || lane || '\x00' || canonicalFilters` over the one source's own **filter** only, never over the read window or the other sources in the same request; a stored interval satisfies a request when it contains the requested interval for the same fingerprint and `complete=1`, and `GET .../coverage` reads `case.db` only, reports gaps, and never fills them, returns `{entries:[...], asOf, gaps:[...], provenance:"case-ledger"}` and never carries `allLanesStored` (which exists only on `POST .../tracing`), and an empty ledger returns `entries:[]`, `state:"no-coverage"` with a non-empty `gaps` for the requested window; coverage means 'completely read as of `asOf`', not 'current', and gains `content_digest TEXT` (proposed) and `content_digest_v INTEGER` (proposed); `coverage.source` is `TEXT NOT NULL REFERENCES case_sources(id) ON DELETE CASCADE`, while `import_attempts.source` is historical text with no FK, carries `source_label`, and rows older than `MaxAttemptRetention` (30 days, proposed) are pruned on case open) | MVP-2 | Must | Appendix P7; `coverage` DDL |
| TR-34 | Rollups, percentile and zoom (exact `count_all`/`sum_us`/`min_us`/`max_us`; `count_with_duration` is the percentile denominator; approximate p50/p95/p99 merged from bins; UTC floor-aligned, half-open rollup zoom where every presentation bucket is intersected with `[start,end)`, whole contained UTC-hour sub-buckets are filled from `bucket_ms=3600000` rows and each boundary fragment is filled from a bounded raw scan, at most two scans, one shared query when both fragments lie in the same grain-hour, and `clipped=true` marks a geometric window edge while `complete=false` marks missing or unfinished data, never interchanged; the presentation axis is UTC floor-aligned at every rung (`axisStart = floorUTC(start,3600000)`, `axisEnd = ceilUTC(end,3600000)`) and the grid is never re-anchored; the presented width is `max(ladderWidth(span), alignedRung(axisStart, axisEnd))`, where `alignedRung` is the first supported rung in `[60s,300s,600s,900s,1h,2h,4h,6h,8h,12h,24h]` whose aligned bucket count is ≤120, so the aligned count is always ≤120 and the number of boundary raw scans is zero when neither edge is set, one when exactly one edge is set or when both boundaries fall inside the same grain-hour (one bounded raw query serves both), and two otherwise; a non-finite, negative or `> maxInt64` duration keeps the event with no duration and `durationStatus:'invalid'`; it increments `count_all` and not `count_with_duration`, no invalid float is ever serialized, and only records that cannot be safely identified or that violate envelope integrity are quarantined; a missing duration is written and increments `count_all` only) | MVP-2 | Must | Appendix P9; AC-18..21 |
| TR-14 | Audit route filter on trace | Later | Should | `frame.ts:14-15` (opened) |
| TR-17 | Persist trace window | Later | Should | `(unverified)` |

## 9. Work packets

### 9.1 How to read the table (3 lines)

Each row is one implementation turn: change only the listed files, satisfy the done-check, then stop; the one exception is a dependency-adjacent edit: an implementation packet may edit a file not on its list only when a listed file fails to compile or an existing invariant/test would break without the change, and its report then carries a mandatory diff summary of that edit (see section 0 item 2, the dependency-adjacent edit rule). The **Detail** column points to long-form steps in Appendix P (P1–P12) of this document, which is the canonical, self-contained normative packet detail; each Detail cell either reads "Appendix P P<n>" or lists the TR/AC ids the packet satisfies, and either way no external brief, research note or owner document is normative. **Depends on** must be satisfied first; rows are grouped by shippable phase (MVP-1, MVP-2, MVP-3, Bench/Verify).

### 9.2 Packet table

| WP-id | Phase | Goal | Files (exact) | Interface/signature | Tests | Done-check (a runnable command and the expected result) | Detail | Depends on | Size |
|-------|-------|------|---------------|---------------------|-------|--------------------------------------------------------|--------|------------|------|
| **MVP-1** |
| WP-01 | MVP-1 | Persist per-case allowlist + one 32-byte HMAC key in `meta` (`cookie.allowlist`, `cookie.hmac_key_v1`); add `CookiePolicy` on `importer.Options` (`importer.go:62`); inject policy on each import; no key in manifest/logs. | `internal/importer/importer.go`, `internal/casestore/sqlite/case.go`, `internal/server/cases.go` | `type CookiePolicy struct { Allowed []string; HMACKey []byte };` field `CookiePolicy *CookiePolicy` on `Options` | Extend `internal/importer/importer_test.go` if present, or add minimal test in `internal/importer/iis/iis_test.go` wiring opts | `go test ./internal/importer/iis/... -run Cookie -count=1` passes; `grep CookiePolicy internal/importer/importer.go` shows the struct | Appendix P P1 | — | S |
| WP-02 | MVP-1 | W3C: store `client-ip`, `referer` (no query), allowlisted `cookie:*` HMAC digests only; update package comment; add `HashCookie`, `splitCookiePairs`, validators. **Attr keys:** `client-ip`, `referer`, `cookie:<name>`. | `internal/importer/iis/common.go`, `internal/importer/iis/w3c.go` | `func HashCookie(p *CookiePolicy, name, value string) string`; `w3cRow` attrs as in P1 | `TestW3CStoresClientIPAndReferer`, `TestW3CCookieHash`, `TestW3CCookieRawNeverStored`, `TestW3CStillDropsUserAgentAndQuery` in `internal/importer/iis/iis_test.go` | `go test ./internal/importer/iis/...`; `grep -n "never read into any value" internal/importer/iis/common.go` returns nothing | Appendix P P1 | WP-01 | M |
| WP-03 | MVP-1 | Store accepts dynamic `cookie:*` attrs when importer allowlist includes sentinel `cookie:*` (prefix match in `Event.Validate`); static attrs `client-ip`, `referer` exact match. | `internal/evidence/event.go`, `internal/casestore/sqlite/case.go`, `internal/importer/iis/w3c.go` | `W3C.AllowedAttrs()` returns static set + `"cookie:*"` | Importer + store test: import row with `cookie:sid` succeeds | `go test ./internal/casestore/sqlite/... ./internal/importer/iis/... -count=1` green | Appendix P P1 | WP-02 | M |
| WP-04 | MVP-1 | HTTPERR: if real sample/fixture has client IP column, store `client-ip` only; if absent, no-op and document gap in import gaps. **(unverified until VF-01.)** | `internal/importer/iis/httperr.go` | Same attr name `client-ip` as W3C | Test in `internal/importer/iis/iis_test.go` using fixture or skip with gap note | `go test ./internal/importer/iis/... -run HTTPERR -count=1` passes | Appendix P P1 | WP-45 | S |
| WP-05 | MVP-1 | Replace `TestW3CDropsSensitiveColumns`; keep `TestW3CTimeIsEndGap`; conformance sentinels aligned with new privacy rule. | `internal/importer/iis/iis_test.go`, `internal/importer/iis/conformance_test.go` | — | Tests named in P1 | `go test ./internal/importer/iis/...` | Appendix P P1 | WP-03 | S |
| WP-06 | MVP-1 | Export preview/confirm: `ScrubSensitive *bool`, default ON when nil; advisory `previewId = hex(SHA-256("unravis-preview-v1" \|\| caseId \|\| caseRevision \|\| scrubSensitive \|\| projectionVersion))` is a revision stamp, not an issuance token. The `case.revision` bump primitive is owned by WP-65a; the end-to-end contract is WP-65. | `internal/server/export.go`, `internal/evidence/build.go` | `scrub := req.ScrubSensitive == nil \|\| *req.ScrubSensitive`; preview and confirm are stateless and recompute `previewId`; confirm exports the same case revision and the same scrub policy class as the preview and does not promise byte-identical output (each export uses a fresh pseudonym key and records its own creation time), and a differing or missing `previewId` is 409 with "the preview changed; run the preview again" (no stored one-shot, expiry or unknown-preview 404 state) | Server/export test for default true plus a concurrency test (changed-after-preview, replay, parallel confirm) | `go test ./internal/server/... ./internal/evidence/... -run Scrub -count=1` | Appendix P P2 | WP-65a | S |
| WP-07 | MVP-1 | Export bundle + `GET .../events` drop `client-ip`, `referer`, `cookie:*` when scrub on (default) through the single `ProjectEventAttrs` projection; confirm carries the advisory `previewId` (stateless recompute of `previewId`, matching the case revision and scrub policy class per TR-21, not bytes; 409 on mismatch/missing, no one-shot/404 state); update statement strings; keep `TestBuildCaseNoIPsOrUserIDs` green; the revision bump primitive is owned by WP-65a and the revision check plus byte production run inside one read transaction owned by WP-65. | `internal/evidence/caseexport.go`, `internal/evidence/caseexport_test.go`, `internal/server/cases.go` | — | `TestBuildCaseScrubDefaultOn`, `TestBuildCaseScrubOff`, preview/confirm concurrency test | `go test ./internal/evidence/...`; scrub-on bundle has no IP bytes | Appendix P P2 | WP-06 | M |
| WP-08 | MVP-1 | Tooltip dictionary source of truth; entries must satisfy `web/src/lib/copy/copylint.ts` (`lintTerms` / `lintScreenStrings`). | `web/src/lib/copy/dictionary.ts`, `web/tests/dictionary.test.cjs` | `DICTIONARY`, `explain(id string): string` | Assert seeded ids + `explain('missing')===''` | `cd web && npm test` passes new test file | Appendix P P3 | — | S |
| WP-09 | MVP-1 | Wire `explain()` into chart tooltips and Call Trace labels. | `web/src/lib/charts.ts`, `web/src/components/CallTrace.svelte` | — | Extend `web/tests/callTrace.test.cjs` or chart test for `explain(` usage | `cd web && npm test`; `rg 'explain\(' web/src/lib/charts.ts web/src/components/CallTrace.svelte` matches | Appendix P P3 | WP-08 | M |
| WP-10 | MVP-1 | Trace toolbar: three lane toggles + scrub pill that always states the real export setting ("export hides IP, referer and cookie hash", "export includes IP, referer and cookie hash", or "default" until the first preview runs). | `web/src/components/TraceToolbar.svelte`, `web/src/lib/callTrace.ts`, `web/tests/traceToolbar.test.cjs` | Lane visibility state beside `MAX_CALLS` in `callTrace.ts` | Toggle hides lane rows; scrub pill reflects the real setting | `cd web && npm test` | Appendix P P4 | WP-07 | M |
| WP-11 | MVP-1 | Mount toolbar in `TracingPaper.svelte` only (the window-tracing surface, AC-15); never in `CallTrace.svelte`, whose per-call tray stays unchanged. | `web/src/components/TracingPaper.svelte` | — | `web/tests/callTrace.test.cjs` still passes; toolbar test mounts only under TracingPaper | `cd web && npm test -- callTrace` | Appendix P P4; ruling D-2=A/AC-15 | WP-10 | S |
| WP-12 | MVP-1 | SQL lane in call trace: `sql-*` events in UTC window via `BuildLanes`. | `internal/calltrace/lanes.go`, `internal/calltrace/lanes_test.go` | `BuildLanes(win, haveCase, sources, evs)` | Case with one `sql-xe` → `LaneSQL` `StateEvents` | `go test ./internal/calltrace/...` | Appendix P P5 | — | S |
| WP-13 | MVP-1 | Extend lane API JSON with structured `sources[]` (per-source label, window, read kind) plus HTTP status class + win32 for IIS plotting (no raw attrs in response); the lane's source list is the effective resolved source, never a raw importer name. | `internal/calltrace/types.go`, `internal/server/call_trace.go` | Add `Sources []SourceRef{SourceId,Kind,Label,ReadKind,Window}` (`kind` required; `readKind` in {saved,live}; `window` only when it differs from the lane window), optional `StatusClass string`, `Win32 *int` on `LaneEvent` | `internal/calltrace/lanes_test.go` or server test | `go test ./internal/calltrace/... ./internal/server/... -run CallTrace -count=1` | TR-32; Appendix P P4 | WP-12 | M |
| WP-14 | MVP-1 | Add read-only tracing endpoint shell (separate from POST `/timeline` text view); keep per-source concurrency bounds, request-context cancellation, and the client-only per-tab `requestSeq`, and ignore stale responses by `requestSeq`. MVP-1 removes the shared in-flight map: keep per-source concurrency bounds, request-context cancellation, and the client-only per-tab `requestSeq`; two tabs with the same query each perform their own bounded live audit read; there is no `serverWorkId`, no subscriber count and no shared in-flight read. | `internal/server/routes.go`, `internal/server/tracing.go` | `POST /api/cases/{id}/tracing` → `{ window, bucketSeconds, lanes[], warnings[], truncated, requestSeq }` | Route registered test plus a concurrency test in `internal/server/routes_test.go` | `go test ./internal/server/... -run Tracing -count=1` | D-6; Appendix P P4 | — | M |
| WP-15 | MVP-1 | Tracing handler validates ≤30d UTC windows, the >24h bucket ladder, and the 50k raw-row cap; the presentation axis is UTC floor-aligned at every rung (`axisStart = floorUTC(start,3600000)`, `axisEnd = ceilUTC(end,3600000)`) and the grid is never re-anchored; the presented width is `max(ladderWidth(span), alignedRung(axisStart, axisEnd))`, where `alignedRung` is the first supported rung in `[60s,300s,600s,900s,1h,2h,4h,6h,8h,12h,24h]` whose aligned bucket count is ≤120, so the aligned count is always ≤120 and the number of boundary raw scans is zero when neither edge is set, one when exactly one edge is set or when both boundaries fall inside the same grain-hour (one bounded raw query serves both), and two otherwise; a 30-day span is 2,592,000,000 ms and the presented width `W = max(ladderWidth(span), alignedRung(axisStart, axisEnd))` is 21,600,000 ms (6 h nominal) only for a 30-day window whose start is on a UTC 00:00 hour boundary; a misaligned 30-day start coarsens to the next supported rung (8 h, count 91). | `internal/server/tracing.go`, `internal/audit/models.go` | New `MaxTraceWindow = 30 * 24 * time.Hour`; bucket-size helper for windows >24h | Bad window → 400; 30d trace returns ≤120 buckets; raw list capped but bucket counts from the uncapped aggregate | `go test ./internal/server/... ./internal/timeline/... -count=1` | TR-02, TR-10 | WP-14 | M |
| WP-16 | MVP-1 | Case home **Tracing** entry opens new tracing UI shell. | `web/src/lib/components/CaseHome.svelte`, `web/src/components/TracingPaper.svelte`, `web/src/lib/copy/caseHomeCopy.ts` | — | `web/tests/casehome.test.cjs` or new test asserts button + route | `cd web && npm test` | TR-01 | WP-14 | M |
| WP-17 | MVP-1 | Audit lane series: one bucketed audit lane in the tracing response (not one `/api/analyze` series per cohort); per-source reads are concurrency-bounded and a newer request never cancels a different tab's read. MVP-1 removes the shared in-flight map, so there is no `serverWorkId` and no subscriber count, and two tabs with the same query each perform their own bounded live audit read. | `internal/server/tracing.go`, `web/src/components/TracingChart.svelte` | The live audit lane computes its buckets with one server-side `$match`+`$group` aggregation per source under the existing 60 s `CensusQueryBudget` and the existing broad-scan gate; it never pages raw rows to build counts, and a single command is a live aggregate, not a snapshot. `complete:true` means the read command returned to end within its budget with no cap, error or unfinished boundary, never that the source was a stable snapshot; each lane carries `consistency ∈ {live-non-snapshot,stored-as-of,snapshot}` and the response sets `mixedConsistency:true` when any lane is `live-non-snapshot`; a budget-exceeded or failed command reports `complete:false` with `reason`. SQL/IIS lanes read exact local `case.db` counts. Never apply an uncapped `GROUP BY` to the audit store; `BroadScanBuckets` is retired. Client renders returned buckets only—no cohort summing in `charts.ts`. | `go test ./internal/server/... -run Tracing -count=1`; tracing chart test shows audit buckets | `go test ./internal/server/...`; `cd web && npm test` | TR-04; §5.4, §7, Appendix P P4 | WP-15, WP-16 | M |
| WP-18 | MVP-1 | SQL + IIS lanes in tracing response from `case.db` events (imported only in MVP-1); bucket counts come from a separate uncapped server-side `GROUP BY` aggregate over all matching case events, independent of the 50 000 raw-row detail cap, and a lane carries `complete:false` only if that aggregate cannot complete. `source` is the immutable `case_sources.id`, `kind` is the importer/event kind. | `internal/server/tracing.go`, `internal/casestore/store.go` | `EventQuery{Window, Sources}`; uncapped bucket aggregate; lane JSON `sources[]` = `{sourceId, kind, label}` | Integration test with seeded events; capped list still shows exact bucket counts | `go test ./internal/server/... -run Tracing -count=1` | TR-04, D-7 | WP-15 | M |
| WP-19 | MVP-1 | HTTPERR marker points on IIS lane when `iis-httperr` events exist. | `internal/server/tracing.go`, `internal/importer/iis/httperr.go` | Marker list in IIS lane payload | Test with httperr fixture import | `go test ./internal/server/... ./internal/importer/iis/... -count=1` | TR-30 | WP-18 | S |
| WP-20 | MVP-1 | IIS lane draws a point at the logged timestamp and shows `time-taken` separately; add a derived span only in a later, VF-01-gated packet (`w3cTimeIsEnd` unverified). | `internal/importer/iis/w3c.go` | Uses existing `w3cTimeIsEnd` const | Assert `start == end == logged timestamp` and `durationMs` shown separately; keep `TestW3CTimeIsEndGap` | `go test ./internal/importer/iis/...` | Appendix P P4 | WP-45, WP-02 | M |
| WP-21 | Later | Audit route/status/tenant filter on the tracing request (ruling 3 puts filters in Later; D-12 B is win32 partitioning; the MVP-1 request has no filter fields). | `internal/server/tracing.go`, `internal/server/investigation.go` | Filter struct shared with investigation | Filter narrows audit lane only | `go test ./internal/server/... -run Investigation -count=1` | TR-14 | WP-17 | S |
| WP-22 | MVP-1 | Warn when reference window overlaps incident; non-blocking fix affordance (same rule as guided `frameProblems`, without importing guided code). | `web/src/components/TracingPaper.svelte`, `web/src/lib/tracing/windows.ts` **(new helper, unverified path)** | Copy overlap check from `frame.ts:81-82` into a tracing-only helper; **do not import** `web/src/lib/guided/frame.ts` (WP-63 keeps it, unlisted) | Web test overlap message | `cd web && npm test` | TR-08 | WP-16 | S |
| WP-23 | MVP-1 | Tracing canvas toolbar: zoom reset, lane visibility, p50/p95 selector (targets until BM-04). | `web/src/components/TracingChart.svelte`, `web/src/components/TracingPaper.svelte` | — | Render test in `web/tests/tracingChart.test.cjs` | `cd web && npm test` | TR-23 | WP-17, WP-18 | M |
| WP-24 | Later | Win32-partitioned IIS series (**estimate** label in UI); requires status on `LaneEvent`. | `internal/server/tracing.go`, `web/src/lib/copy/dictionary.ts` | Split series by win32 attr buckets | Test counts 64/995/1236 classes | `go test ./internal/server/... -count=1` | Appendix P P5; D-12 Later labelled estimate | WP-13, WP-18 | M |
| WP-25 | MVP-1 | Demo/demo_lanes seed so TR-18 E2E shows audit+sql+iis lanes. | `internal/server/demo_lanes.go`, `tools/e2e/specs/` **(unverified: exact new spec filename)** | — | E2E or int test three lane keys | `go test ./internal/server/... -run Demo -count=1` | TR-18 | WP-18 | M |
| WP-60 | MVP-1 | **Reuse list (document):** list which guided charts/components/helpers tracing can reuse later — `web/src/lib/guided/*`, charts, checklist, After the fix, Nearby activity — each with its path; no deletions. | section 9 WP-60 reuse table in this document, `web/src/lib/guided/` (all files), `web/src/lib/components/guided/` (all files), `web/src/lib/charts.ts` | Written reuse table: path → what it does → how tracing may reuse it later; every row keeps the file | Reuse table lists every guided chart/component/helper with a path; no row deletes anything | none (document) | D-2=A | — | S |
| WP-61 | MVP-1 | **Prove bucket click hands off to analysis (keep);** state plainly that checklist / After the fix / Nearby activity are NOT rebuilt in Tracing now — they stay in guided. | `web/src/components/TracingPaper.svelte`, `web/src/components/TracingChart.svelte`, `web/src/lib/charts.ts` | Bucket click → analysis hand-off callback | Web test: clicking a bucket opens the analysis view for that sub-window | `cd web && npm test` | TR-09; WP-60 | WP-23, WP-60 | M |
| WP-62 | MVP-1 | **Archive guided e2e specs (owner ruling):** guided application code stays; guided Playwright tests leave the default suite so the main run is faster after WP-64 removes the case-home button; the archived suite must still run in the pre-release gate and on a schedule. Inventory today: whole guided-only files `guide-steps.spec.ts` (25 tests), `guide-checklist.spec.ts` (10), `guided.spec.ts` (6 incl. MAN-04/MAN-04a), `export-ui.spec.ts` (13); mixed files keep non-guided tests in `specs/` — extract `EXP-04a` from `exports.spec.ts` (`openExportTab` helper at lines 116–121), `UPD-07` from `keeper-update.spec.ts` (lines 236–237), and 22 tests from `manual-tools.spec.ts` (MAN-01, MAN-01a–c, MAN-02, MAN-05–MAN-10b; keep MAN-03/MAN-03a in main after rewriting entry to Manual tools without `openGuided`). `help-guide.spec.ts` stays in main (Help drawer only; no `openGuided`). **Mechanism (one):** move archived specs to `tools/e2e/archive/guided/` so `playwright.config.ts:7` `testDir: './specs'` skips them; add `run-e2e.ps1 -Archive` and `npm run e2e:guided` that run Playwright with `testDir` `./archive/guided` (the archive run writes `report/archive.json` via E2E_REPORT). Move `openGuided` helpers to archive-only import path; main `specs/` must not import it (`support/ui.ts:22-26`). Release readiness is proven by `./run-e2e.ps1 -Release`, which runs the main suite and the archive suite in sequence, writes `report/main.json` and `report/archive.json`, fails unless every required test ID in the test-ID set generated from the version-controlled accepted scenario inventory (`SC-*` and `EV-*` ids actually present in the accepted spec) appears in exactly one of the main/archive suites, every ID listed in a suite exists in that generated set (no orphans), and both reports are present and non-empty and both suites pass, and is the only release entrypoint | `tools/e2e/playwright.config.ts`, `tools/e2e/run-e2e.ps1`, `tools/e2e/package.json`, `tools/e2e/COVERAGE.md`, `tools/e2e/archive/guided/README.md`, `tools/e2e/support/ui.ts`, `tools/e2e/archive/guided/*.spec.ts`, `tools/e2e/specs/exports.spec.ts`, `tools/e2e/specs/keeper-update.spec.ts`, `tools/e2e/specs/manual-tools.spec.ts`, `docs/COVERAGE` **(unverified)** | Archive folder populated; main `specs/` has zero `Open guided analysis` clicks; COVERAGE table marks GUIDE-/archived MAN-/EXP-UI rows as covered via archive; `known-defects.json` unchanged (still DOC-02, KEEP-09 only) | Main `npx playwright test --list` count drops by the number the archive run lists (discovered at runtime; no exact historical count is required); every required test ID in the test-ID set generated from the accepted scenario inventory appears in exactly one of the main/archive suites and no orphan suite ID exists; `rg -i "Open guided analysis" tools/e2e/specs` has no matches | `cd tools/e2e && ./run-e2e.ps1`; `cd tools/e2e && ./run-e2e.ps1 -Archive` | D-2=A | WP-61 | M |
| WP-63 | MVP-1 | **Guided code kept:** add a note to `docs`/code header that guided is unlisted but maintained; make sure no guided unit test or route is deleted; keep `/investigation`, `/views`, `/checklist`, `/report` routes. Guided **Playwright** specs are archived by WP-62, not deleted. | `web/src/lib/components/guided/GuidedAnalysis.svelte` (header), `web/src/lib/guided/` (headers), `docs` note, `internal/server/routes.go` (unchanged), `tools/e2e/archive/guided/` | — | `rg -i guided web/tests` still lists the guided unit tests (none removed); archived e2e specs still exist under `tools/e2e/archive/guided/` | `cd web && npm test` | WP-61 | WP-62 | S |
| WP-64 | MVP-1 | Swap the case-home **Guided** button for the **Start tracing** button; guided route/component stay in the app unlisted (no case-home link; no test-only entry required). | `web/src/lib/components/CaseHome.svelte`, `web/src/lib/copy/caseHomeCopy.ts` | — | Case home shows Start tracing and no Guided button | `cd web && npm test` | D-2=A (guided unlisted) | WP-61, WP-63 | S |
| WP-65a | MVP-1 | Own the `bumpCaseRevision(tx)` revision-bump primitive only: the `case.revision` storage and migration default `case.revision = 0`, `func bumpCaseRevision(tx *sql.Tx) error`, and storage-level tests proving the bump is monotonic and transactional. It does **not** claim mutation-site wiring. | `internal/casestore/sqlite/case.go` (and its tests) | `func bumpCaseRevision(tx *sql.Tx) error` | The bump is monotonic and transactional (primitive only) | `go test ./internal/casestore/sqlite/... -run Revision -count=1` | Appendix P P2 | — | S |
| WP-65b | MVP-1 | Own every mutation-site integration of the revision bump with named symbols/files and independent before/after revision assertions: import committed, import deleted, source added/changed/removed, cookie allowlist set, HMAC key set, and any exported settings change. | `internal/server/cases.go`, `internal/server/sources*.go`, `internal/casestore/sqlite/case.go` | Calls `bumpCaseRevision(tx)` at each named mutation site | Each named mutation asserts `case.revision` increments before/after | `go test ./internal/server/... ./internal/casestore/sqlite/... -run Revision -count=1` | Appendix P P2 | WP-65a | M |
| WP-65 | MVP-1 | Own the end-to-end `case.revision` contract: confirm recomputes the revision, snapshots the export inputs and refuses 409 `the preview changed; run the preview again` on mismatch. | `internal/server/cases.go`, `internal/server/sources*.go`, `internal/evidence/caseexport.go` | The revision check and byte production run inside one read transaction (`BEGIN` … snapshot … `COMMIT`) | Concurrent import during confirm → 409, no bytes; the confirm snapshot and the 409 are WP-65's own, while independent before/after revision assertions at each mutation site are owned by WP-65b | `go test ./internal/server/... ./internal/evidence/... ./internal/casestore/sqlite/... -count=1` | Appendix P P2 | WP-65a, WP-65b, WP-06, WP-07 | M |
| **MVP-2** |
| WP-26 | MVP-2 | Persist live SQL/Mongo lane reads into case via import tx. | `internal/dbhealth/pull.go`, `internal/dbhealth/lane_sql.go` | `func PullToCase(ctx, c casestore.Case, src Source, win importer.Window) (casestore.ImportResult, error)`; outcome is derived from the canonical row-content digest of the ordered observed rows (versioned, `content_digest_v`); `unchanged` is `StatusDuplicate`; `changed` atomically replaces the superseded observation for the same exact window (deleting its events and coverage and rebuilding the affected rollups in one transaction); a partial-overlap window is refused with `409 window-overlap`; `complete` means the pull enumerated every page to the end of the window | `internal/dbhealth/pull_test.go`: repeat pull with byte-identical rows → no-op outcome `unchanged`; mutated source → outcome `changed` | `go test ./internal/dbhealth/...` | Appendix P P6 | WP-27, WP-28, WP-15 | M |
| WP-27 | MVP-2 | **Schema v5 back-compat:** create/verify all four tables `coverage`, `import_attempts`, `hydration`, `rollups`; bump `schemaVersion`; migrate v4→v5 on open in one transaction that verifies table shape/version, with `PRAGMA foreign_keys=ON` per connection and `foreign_key_check` on open; a failed migration rolls back and a newer-schema file is refused. | `internal/casestore/sqlite/schema.go`, `internal/casestore/sqlite/discover.go` | DDL as in P7–P9; `source` is the immutable `case_sources.id`; `coverage.source` carries `REFERENCES case_sources(id) ON DELETE CASCADE`, `import_attempts.source` has no FK and carries `source_label` with a `(started_at)` index | `sqlite/coverage_test.go` v4 fixture migrates; a `sqlite_master` assertion lists all four tables | `go test ./internal/casestore/sqlite/... -run Migrate -count=1`; `grep rollups internal/casestore/sqlite/schema.go` | Appendix P P7–P9 | — | M |
| WP-28 | MVP-2 | Write `coverage` rows (with the query fingerprint) inside the import transaction; own the `import_attempts` CRUD written outside that transaction. | `internal/casestore/sqlite/case.go`, `internal/casestore/sqlite/coverage_test.go` | `RecordCoverage` or extend `Import`; fingerprint = lowercase-hex SHA-256 over `'unravis-coverage-v1\x00' || sourceId || '\x00' || lane || '\x00' || canonicalFilters` (the read window is not in the preimage and lives only in `from_ms`/`to_ms`), the one source's own filter only | Two partial pages `complete=0`; full `complete=1`; cascade on import delete; failed attempt adds no coverage | `go test ./internal/casestore/sqlite/... -run Coverage -count=1` | Appendix P P7 | WP-27 | M |
| WP-29 | MVP-2 | Write `hydration` flags per import. | `internal/casestore/sqlite/case.go`, `internal/casestore/sqlite/hydration_test.go` | PK `(import_id, field)` | present=1 when payload declared | `go test ./internal/casestore/sqlite/... -run Hydration -count=1` | Appendix P P8 | WP-27 | S |
| WP-30 | MVP-2 | Build the 60 s rollup pass at end of `Import` (same tx) and rebuild the affected buckets transactionally after an import is deleted; store integer `sum_us` (overflow-checked), `count_all`, `count_with_duration`, integer `min_us`/`max_us`, and per-rollup `bins` (same layout as `internal/audit/histogram.go`); percentiles derived by merging bins, never averaging; a non-finite, negative or `> maxInt64` duration keeps the event with no duration and `durationStatus:'invalid'`, increments `count_all` but not `count_with_duration`, never serializes an invalid float, and only quarantines records that cannot be safely identified or that violate envelope integrity. | `internal/casestore/sqlite/case.go`, `internal/casestore/sqlite/rollup_test.go` | Key stays `(source, kind, bucket_ms, at_ms)` with `source` = immutable `case_sources.id`; rebuild re-happens after the superseded import is deleted in the same transaction, from all active events (events of imports not superseded/deleted) | Import N events → bucket counts; no double on re-import or delete; two IIS sites in one minute produce distinct rows | `go test ./internal/casestore/sqlite/... -run Rollup -count=1` | Appendix P P9 | WP-27 | M |
| WP-31 | MVP-2 | 3600 s rollup pass and verification (second `bucket_ms=3600000`) using the same writer; WP-30 no longer writes it. | `internal/casestore/sqlite/case.go` | — | Rollup test covers hour buckets and verifies the 60 s pass is not duplicated | `go test ./internal/casestore/sqlite/... -run Rollup -count=1` | Appendix P P9/P11 + inline budget | WP-30 | S |
| WP-32 | MVP-2 | `RollupSeries(ctx, q RollupQuery) ([]RollupBucket, error)` read path; cap `MaxSeriesBuckets=2000`; `source` is the immutable `case_sources.id` and `kind` the importer/event kind. | `internal/casestore/sqlite/case.go`, `internal/casestore/series.go` | Method on SQLite store | An aligned window whose presentation buckets all fall on rollup grain reads rollups only and issues no raw scan, and only a window with a clipped edge (one that does not align to the grain) serves that edge from the bounded raw scan; every benchmark states which mode it measured | `go test ./internal/casestore/...` | Appendix P P9 | WP-31 | M |
| WP-33 | MVP-2 | Tracing/chart zoom prefers `RollupSeries` (UTC floor-aligned buckets); an aligned window whose presentation buckets all fall on rollup grain reads rollups only and issues no raw scan, and only a window with a clipped edge (one that does not align to the grain) serves that edge from the bounded raw scan, marking `clipped` and `complete` independently, never silently approximated; fall back to raw `Series` when the rollup is missing. | `internal/server/tracing.go`, `web/src/components/TracingChart.svelte` | — | Test `clipped` and `complete` on the edge bucket and on fallback | `go test ./internal/server/...`; `cd web && npm test` | Appendix P P9 | WP-32, WP-23 | M |
| WP-34 | MVP-2 | Coverage API (includes the per-source query fingerprint) + incomplete banner in activity context; `GET .../coverage` returns `{entries:[...], asOf, gaps:[...], provenance:"case-ledger"}` and never carries `allLanesStored` (which exists only on `POST .../tracing` responses), and no `bypassLedger` field exists. | `internal/server/coverage.go`, `web/src/components/ActivityContext.svelte`, `web/src/lib/api.ts` | `GET /api/cases/{id}/coverage` (case window ledger; not `/api/sources/{id}/coverage`); a stored interval satisfies a request when it contains the requested interval for the same fingerprint and `complete=1`, and `GET .../coverage` reads `case.db` only, reports gaps, and never fills them; it opens no source connection and writes nothing; coverage means 'completely read as of `asOf`', not 'current', and reports `content_digest` and `content_digest_v` | `complete=0` shows banner with source label; a repeat returns `entries` with ledger provenance and no `allLanesStored` field, with no source read | `cd web && npm test` | Appendix P P10 | WP-28 | M |
| WP-35 | MVP-2 | Merge saved live pulls into tracing SQL/Mongo lanes when window covered; `source` is the immutable `case_sources.id`, so two hosts of the same kind stay distinct. | `internal/server/tracing.go`, `internal/dbhealth/pull.go` | — | Tracing with pull + import shows combined counts; per-host provenance kept | `go test ./internal/server/... ./internal/dbhealth/... -count=1` | TR-26; P6 | WP-26, WP-18 | M |
| WP-36 | MVP-2 | Batched multi-row `INSERT` during import (100–1000 rows/statement); same serializable tx + crash atomicity. | `internal/casestore/sqlite/case.go` | — | Import test row count; BM-02 will measure | `go test ./internal/casestore/sqlite/... -run Import -count=1` | Appendix P P9/P11 + inline budget | WP-27 | M |
| WP-37 | MVP-2 | Defer `PRAGMA quick_check` to background; expose case `verifying` state on open. | `internal/casestore/sqlite/case.go`, `internal/casestore/store.go` | — | Open returns before full check; state visible | `go test ./internal/casestore/sqlite/... -count=1` | Appendix P P9/P11 + inline budget | WP-27 | M |
| WP-38 | MVP-2 | Lazy `dbManifestImports` COUNT when manifest trusted. | `internal/casestore/sqlite/case.go` | — | Reopen skips full scan when counts match | `go test ./internal/casestore/sqlite/... -count=1` | Appendix P P9/P11 + inline budget | WP-37 | S |
| **MVP-3** |
| WP-39 | MVP-3 | Layer 4: **create** host-metrics importer + Mongo app DB exports as events; new `LaneHost` in `LaneOf`/`BuildLanes`. | `internal/calltrace/lanes.go`, `internal/calltrace/types.go`, `internal/importer/all/all.go`, `internal/resource/metrics.go` | `LaneHost` constant; new importer prefix e.g. `host-metrics` registered in `all.go` | `lanes_test.go`: `host-metrics` → `LaneHost` | `go test ./internal/calltrace/... ./internal/importer/... -count=1` | Appendix P P11 | WP-12 | M |
| WP-40 | MVP-3 | Wire Layer 4 sources through normal `Import`; show lane on tracing + call trace. | `internal/resource/metrics.go`, `internal/blackbox/reader.go` | — | Resource/blackbox tests green | `go test ./internal/resource/... ./internal/blackbox/...` | Appendix P P11 | WP-39 | M |
| WP-41 | MVP-3 | Mongo profiler pull stays read-only with degraded state unless a separate explicit administrator action enables profiling, never part of tracing and default off. | `internal/dbhealth/lane_mongo.go`, `internal/server/lanes.go` | Explicit API flag | Test refuses enable without ack | `go test ./internal/dbhealth/... -count=1` | VF-01 checklist inline; Appendix P P11 | WP-26 | M |
| WP-42 | MVP-3 | SQL pull: store `dm_os_wait_stats` **deltas** between snapshots, not cumulative raw. | `internal/dbhealth/sql.go`, `internal/dbhealth/lane_sql.go` | Delta attrs on events | Unit test two snapshots → positive delta | `go test ./internal/dbhealth/... -count=1` | VF-01 | WP-26 | M |
| WP-43 | MVP-3 | 3-tier agent matcher core. | `internal/investigate/agent_match.go`, `internal/investigate/agent_match_test.go` | `func MatchAgents(in investigate.Input) ([]AgentMatch, error)` | Table-driven tiers; no Tier-3 on ambiguous join | `go test ./internal/investigate/... -run MatchAgents -count=1` | Appendix P P12 | WP-35 | M |
| WP-44 | MVP-3 | Expose `MatchAgents` on investigation API response. | `internal/server/investigation.go`, `internal/investigate/agent_match.go` | JSON `agentMatches[]` | Server test investigation JSON | `go test ./internal/server/... ./internal/investigate/... -count=1` | Appendix P P12 | WP-43 | S |
| **Bench/Verify** |
| WP-45 | Bench/Verify | **VF-01:** Document findings from real samples for time-taken scope, HTTPERR `#Fields`, W3C time end, QS µs, Mongo profile write, wait-stats cumulative; flip code constants only where confirmed. **Six checks in one sitting**—each finding gets its own done-check line even though this is one WP row. | `internal/importer/iis/verify_notes.go` (constants; do not commit external drafts), `internal/importer/iis/w3c.go`, `internal/importer/iis/httperr.go` | Named constants e.g. `verifiedTimeTakenScope` | Test skipped if no sample path env | `go test ./internal/importer/iis/... -run Verify -count=1` passes or skips with reason | Appendix P P1; VF-01 checklist inline | — | M |
| WP-46 | Bench/Verify | **BM-01:** Skew/burst/heavy-tail generator; deterministic seed. | `internal/perfbench/bench_gen_test.go`, `internal/perfbench/synth_test.go` | Generator emits 100k–5M events | Distribution invariants unit test | `UNRAVIS_BENCH=1 go test ./internal/perfbench/... -run Gen -count=1` | Appendix P P9/P11 + inline budget (BM-01) | — | M |
| WP-47 | Bench/Verify | **BM-02:** Ingest throughput batched vs per-row, encrypted vs plain, 100k/1M/5M. | `internal/perfbench/bench_import_test.go`, `internal/casestore/sqlite/case.go` | — | Logs rows/s; asserts vs stored budget | `UNRAVIS_BENCH=1 go test ./internal/perfbench/... -run Import -count=1` | Appendix P P9/P11 + inline budget (BM-02; honest budgets 100k rows/s batched) | WP-36 | M |
| WP-48 | Bench/Verify | **BM-03:** Rollup build time + DB size overhead at 1M/5M. | `internal/perfbench/bench_import_test.go` | — | Reports bytes/table | `UNRAVIS_BENCH=1 go test ./internal/perfbench/... -run RollupBuild -count=1` | Appendix P P9/P11 + inline budget (BM-03) | WP-30 | M |
| WP-49 | Bench/Verify | **BM-04:** Zoom p95 <15 ms on UTC floor-aligned rollup path vs raw at 1M/5M; an aligned window reads rollups only and issues no raw scan, and only a window with a clipped edge serves that edge from the bounded raw scan; every run records `mode: rollup-only` or `mode: rollup+edge` **(label unverified on CI hardware)**. | `internal/perfbench/bench_series_test.go` | — | p95 rollup < 15ms; raw published slower | `UNRAVIS_BENCH=1 go test ./internal/perfbench/... -run Zoom -count=1` | Appendix P P9/P11 + inline budget (BM-04) | WP-32 | M |
| WP-50 | Bench/Verify | **BM-05:** Property test rollups vs raw `Series` for `count_all`/`count_with_duration`/`sum_us`/`min_us`/`max_us`. | `internal/casestore/sqlite/rollup_test.go`, `internal/casestore/series.go` | — | Bit-exact merge | `go test ./internal/casestore/... -run RollupAcc -count=1` | Appendix P P9/P11 + inline budget (BM-05) | WP-30 | M |
| WP-51 | Bench/Verify | **BM-06:** Histogram p95/p99 interval contains exact nearest-rank. | `internal/audit/histogram.go`, `internal/audit/histogram_test.go` | — | Seeds across distributions | `go test ./internal/audit/... -run Quantile -count=1` | Appendix P P9/P11 + inline budget (BM-06) | WP-30 | S |
| WP-52 | Bench/Verify | **BM-07:** Reopen breakdown with/without lazy verify; warm <100 ms target **(informational, not flaky CI absolute)**. | `internal/perfbench/bench_localread_test.go`, `internal/casestore/sqlite/case.go` | — | Logs phase timings | `UNRAVIS_BENCH=1 go test ./internal/perfbench/... -run Reopen -count=1` | Appendix P P9/P11 + inline budget (BM-07) | WP-37 | M |
| WP-53 | Bench/Verify | **BM-08:** WAL/disk peak regression vs 2× dir note. | `internal/perfbench/bench_import_test.go` | — | Peak dir ≤ budget multiplier | `UNRAVIS_BENCH=1 go test ./internal/perfbench/... -run WAL -count=1` | Appendix P P9/P11 + inline budget (BM-08) | WP-36 | S |
| WP-54 | Bench/Verify | **BM-09:** Import vs concurrent readers (`SetMaxOpenConns(4)`). | `internal/perfbench/bench_harness_test.go`, `internal/casestore/sqlite/sqlite.go` | — | Readers see last commit | `UNRAVIS_BENCH=1 go test ./internal/perfbench/... -run Concur -count=1` | Appendix P P9/P11 + inline budget (BM-09) | WP-36 | M |
| WP-55 | Bench/Verify | **BM-10:** Windows cold cache + DPAPI unwrap delta **(run on GOOS=windows only)**. | `internal/perfbench/bench_rss_windows_test.go` | — | Skip non-Windows | `UNRAVIS_BENCH=1 go test ./internal/perfbench/... -run Windows -count=1` | Appendix P P9/P11 + inline budget (BM-10) | — | S |
| WP-56 | Bench/Verify | **BM-11:** JSON baseline store + relative p95 regression checker for CI (100k only). | `internal/perfbench/doc.go`, `internal/perfbench/baseline.json` **(new)** | — | CI compares ≤1.5× baseline | `go test ./internal/perfbench/... -run Baseline -count=1` | Appendix P P9/P11 + inline budget (BM-11) | WP-47 | M |
| WP-57 | Bench/Verify | **BM-12:** Kill mid-import; reopen succeeds; no partial events. | `internal/casestore/sqlite/contract_test.go` (or new `crash_import_test.go` in same package) | — | Simulated abort rolls back | `go test ./internal/casestore/sqlite/... -run Crash -count=1` | Appendix P P9/P11 + inline budget (BM-12) | WP-36 | M |
| WP-58 | Bench/Verify | Main-suite gate packet (this is not the release gate; `-Release` is): `go test ./...` + web `npm test` + the tools/e2e **main** Playwright run only, after the full MVP-1 slice **including the case-home button swap (WP-64) and the guided archive (WP-62)**. Runs the main suite only; WP-62 owns the release gate `./run-e2e.ps1 -Release`, which runs main then archive, writes `report/main.json` and `report/archive.json`, and fails unless every required test ID in the test-ID set generated from the accepted scenario inventory appears in exactly one of the suites, no orphan suite ID exists, and both reports are present, non-empty and passing; the manifest is an output, not an independent authority. Packets marked Later (WP-21, WP-24) are excluded. | — | — | Full main suite green (this is not the release gate; `-Release` is) | `go test ./...`; `cd web && npm test`; `cd tools/e2e && ./run-e2e.ps1` all exit 0 | Appendix P global done-check | WP-01, WP-02, WP-03, WP-04, WP-05, WP-06, WP-07, WP-08, WP-09, WP-10, WP-11, WP-12, WP-13, WP-14, WP-15, WP-16, WP-17, WP-18, WP-19, WP-20, WP-22, WP-23, WP-25, WP-60, WP-61, WP-62, WP-63, WP-64, WP-65a, WP-65b, WP-65 | S |
| WP-59 | Bench/Verify | Query Store µs→ms regression guard (VF-01 confirmation in code comments). | `internal/importer/sqlserver/querystore.go`, `internal/importer/sqlserver/querystore_test.go` | Comment + existing `qsMaxMicros` path | `Test` with 1250000 µs → 1250 ms | `go test ./internal/importer/sqlserver/... -run QueryStore -count=1` | VF-01; querystore.go:38 | WP-45 | S |

### 9.3 Dependency graph (generated — do not edit)

The `Depends on` table in §9.2 is the single normative dependency graph. The inline edge list and the wave table below are generated artifacts, produced by a checked-in topological-level algorithm (Kahn, ties broken by WP id order) via `tools/spec/genwaves` (proposed); they are never hand-maintained.

**Edges (depends →):**  
WP-01→∅; WP-02→WP-01; WP-03→WP-02; WP-04→WP-45; WP-05→WP-03; WP-06→WP-65a; WP-07→WP-06; WP-08→∅; WP-09→WP-08; WP-10→WP-07; WP-11→WP-10; WP-12→∅; WP-13→WP-12; WP-14→∅; WP-15→WP-14; WP-16→WP-14; WP-17→WP-15,WP-16; WP-18→WP-15; WP-19→WP-18; WP-20→WP-45,WP-02; WP-21→WP-17; WP-22→WP-16; WP-23→WP-17,WP-18; WP-24→WP-13,WP-18; WP-25→WP-18; WP-26→WP-27,WP-28,WP-15; WP-27→∅; WP-28→WP-27; WP-29→WP-27; WP-30→WP-27; WP-31→WP-30; WP-32→WP-31; WP-33→WP-32,WP-23; WP-34→WP-28; WP-35→WP-26,WP-18; WP-36→WP-27; WP-37→WP-27; WP-38→WP-37; WP-39→WP-12; WP-40→WP-39; WP-41→WP-26; WP-42→WP-26; WP-43→WP-35; WP-44→WP-43; WP-45→∅; WP-46→∅; WP-47→WP-36; WP-48→WP-30; WP-49→WP-32; WP-50→WP-30; WP-51→WP-30; WP-52→WP-37; WP-53→WP-36; WP-54→WP-36; WP-55→∅; WP-56→WP-47; WP-57→WP-36; WP-58→WP-01,WP-02,WP-03,WP-04,WP-05,WP-06,WP-07,WP-08,WP-09,WP-10,WP-11,WP-12,WP-13,WP-14,WP-15,WP-16,WP-17,WP-18,WP-19,WP-20,WP-22,WP-23,WP-25,WP-60,WP-61,WP-62,WP-63,WP-64,WP-65a,WP-65b,WP-65; WP-59→WP-45; WP-60→∅; WP-61→WP-23,WP-60; WP-62→WP-61; WP-63→WP-62; WP-64→WP-61,WP-63; WP-65→WP-65a,WP-65b,WP-06,WP-07; WP-65a→∅; WP-65b→WP-65a

**Waves** (generated by Kahn level; a wave means **earliest start**, not parallel-safe; every packet sits in a strictly later wave than each of its dependencies; parallel within a wave only where no edge crosses backward):

- **Wave 1:** WP-01, WP-08, WP-12, WP-14, WP-27, WP-45, WP-46, WP-55, WP-60, WP-65a  
- **Wave 2:** WP-02, WP-04, WP-06, WP-09, WP-13, WP-15, WP-16, WP-28, WP-29, WP-30, WP-36, WP-37, WP-39, WP-59, WP-65b  
- **Wave 3:** WP-03, WP-07, WP-17, WP-18, WP-20, WP-22, WP-26, WP-31, WP-34, WP-38, WP-40, WP-47, WP-48, WP-50, WP-51, WP-52, WP-53, WP-54, WP-57  
- **Wave 4:** WP-05, WP-10, WP-19, WP-21, WP-23, WP-24, WP-25, WP-32, WP-35, WP-41, WP-42, WP-56, WP-65  
- **Wave 5:** WP-11, WP-33, WP-43, WP-49, WP-61  
- **Wave 6:** WP-44, WP-62  
- **Wave 7:** WP-63  
- **Wave 8:** WP-64  
- **Wave 9:** WP-58 (**main suite only; must not start until WP-64 is done**); WP-58 never means release-ready. The required test-ID set is generated from the version-controlled accepted scenario inventory (`SC-*` and `EV-*` ids actually present in the accepted spec) and the manifest is an output, not an independent authority. Release readiness is the mandatory, blocking sole gate `./run-e2e.ps1 -Release` (WP-62), which runs main then archive, writes `report/main.json` and `report/archive.json`, and fails (non-zero) unless every generated ID appears in exactly one of the main/archive suites, every ID listed in a suite exists in the generated set (no orphans), both suites pass, and both reports are present and non-empty  

Notes: the guided unlisting packets run in order WP-60, WP-61, WP-62, WP-63, WP-64 and all complete before WP-58. WP-21 and WP-24 are excluded from the MVP-1 gate.

### 9.4 Packet rules for the agent (5 lines)

Implement exactly one WP-id per turn; work from the listed files and the Detail pointer into Appendix P. A dependency-adjacent edit is permitted only when a listed file fails to compile or an existing invariant/test would break without it; it must be minimal, add no feature, and be reported in the completion summary (file, symbol, one-line reason). Any public contract, schema or route change is not dependency-adjacent and must be escalated.  
Do not create `.unravis.db` or parallel event stores—only extend encrypted `case.db` and document schema bumps in the Goal when you touch DDL.  
Never store raw cookies, server IP, query strings, or user agents on IIS paths; export scrub defaults ON unless the packet explicitly tests scrub off.  
Do not assert `<15 ms` zoom or `<100 ms` reopen as hard CI facts unless the packet is BM-xx with `UNRAVIS_BENCH=1`; label approximate percentiles with bounds.  
Call trace tray is **not wired** today; MVP-1 **Tracing** chart is `TracingPaper.svelte` only—do not merge or extend the per-call tray in MVP-1 packets.

### 9.5 WP-60 guided/component reuse table (done-check for WP-60)

Guided code is kept and unlisted (D-2=A); the table records what tracing may reuse later. No row deletes anything.

| Path | What it does | How tracing may reuse it later |
|---|---|---|
| `web/src/lib/charts.ts` | Shared chart drawing/axis/format helpers | Reuse axis and formatting helpers for the tracing chart (WP-23); do not fork |
| `web/src/lib/guided/frame.ts` | Incident/reference frame + overlap check | Copy the overlap rule into `web/src/lib/tracing/windows.ts` (WP-22); do not import it (WP-63 keeps it unlisted) |
| `web/src/lib/guided/afterFix.ts` | After-the-fix comparison model | Later: reuse the comparison shape if tracing adds a before/after view |
| `web/src/lib/guided/nearby.ts` | Nearby-activity lookup | Later: reuse the window-expansion rule for trace drill-down |
| `web/src/lib/guided/stepModel.ts` | Step/checklist state model | Kept in guided only; tracing does not rebuild it (WP-61) |
| `web/src/lib/guided/tools.ts` | Manual-tools definitions | Kept in guided only; not rebuilt in tracing now |
| `web/src/lib/guided/report.ts` | Report/export assembly | Reuse the export scrub plumbing later (ties to TR-21) |
| `web/src/lib/guided/normalize.ts` | Window/attribute normalisation helpers | Reuse pure helpers when both surfaces need the same normalisation |
| `web/src/lib/guided/api.ts` | Guided API calls | Reference only; tracing uses its own `POST /api/cases/{id}/tracing` |
| `web/src/lib/guided/types.ts` | Guided types | Reference only; tracing types live in `web/src/lib/tracing/` |
| `web/src/lib/guided/guided.svelte.ts` | Guided screen state store | Reference only; tracing keeps its own state |
| `web/src/lib/components/guided/GuidedAnalysis.svelte` | Guided shell | Stays in the app, unlisted; never mounted from the case home after WP-64 |
| `web/src/lib/components/guided/Checklist.svelte` | Checklist panel | Kept in guided only (WP-61) |
| `web/src/lib/components/guided/AfterFix.svelte` | After-the-fix panel | Kept in guided only (WP-61) |
| `web/src/lib/components/guided/NearbyActivity.svelte` | Nearby activity panel | Kept in guided only (WP-61) |
| `web/src/lib/components/guided/StepRail.svelte` | Step navigation rail | Reference pattern only; tracing has no step rail |
| `web/src/lib/components/guided/RunBar.svelte` | Run/execute bar | Reference pattern only |
| `web/src/lib/components/guided/SavedViews.svelte` | Saved-view picker | Kept in guided only |
| `web/src/lib/components/guided/ManualTools.svelte` | Manual-tools surface | Kept in guided only |
| `web/src/lib/components/guided/SlowCalls.svelte` | Slow-calls table | Later candidate for a tracing drill-down table |
| `web/src/lib/components/guided/Sticker.svelte` | Status sticker | Reuse the sticker styling for lane-state labels if useful |
| `web/src/lib/components/guided/ReportExport.svelte` | Report/export panel | Reuse export scrub wording later |
| `web/src/lib/guided/inflight.ts` | In-flight async/loading state helper | Reference only; tracing keeps its own loading state |
| `web/src/lib/components/guided/MinuteLine.svelte` | Minute-level timeline line | Later: pattern for the tracing chart point/label treatment |
| `web/src/lib/components/guided/Step0Frame.svelte` | Step 0 frame shell | Reference pattern only; tracing has no steps |
| `web/src/lib/components/guided/StepFacts.svelte` | Step facts panel | Reference only; tracing does not rebuild guided facts |
| `web/src/lib/components/guided/StepShell.svelte` | Step shell wrapper | Reference pattern only; tracing has no steps |
| `web/src/lib/components/guided/Step10Origin.svelte` | Step 10 origin panel | Reference only; tracing does not rebuild guided steps |
| `web/src/lib/components/guided/SuspectCard.svelte` | Suspect-card surface | Reference only; tracing does not rebuild guided cards |

## 10. Acceptance criteria

| ID | Given / When / Then | TR |
|---|---|---|
| AC-01 | Mongo audit + `iis-w3c` import; 1h trace → audit + IIS lanes render, SQL lane shows missing-source | TR-04,07 |
| AC-02 | Only audit source → SQL/IIS missing-source text, audit has points | TR-07 |
| AC-03 | Span >30d → 400 plain error; a 30-day trace returns ≤120 buckets, with the presented width `max(ladderWidth(span), alignedRung(axisStart, axisEnd))` and boundary raw scans that are zero when neither edge is set, one when exactly one edge is set or when both boundaries fall inside the same grain-hour (one bounded raw query serves both), and two otherwise | TR-02 |
| AC-04 | Reference overlaps incident → non-blocking notice | TR-08 |
| AC-05 | >50k events → `truncated:true` + footnote; the raw event list is capped and labelled "first N rows"; bucket counts still come from the uncapped aggregate | TR-10 |
| AC-06 | Import without validated TZ → chart disabled with `tz-rule` reason | TR-11 |
| AC-07 | Click bucket → Run analysis opens that sub-window | TR-09 |
| AC-08 | Closed case → Start tracing disabled | TR-03 |
| AC-09 | Seeded demo case → bucket label visible, ≤120 buckets, three lanes | TR-06,18 |
| AC-10 | Window >24h shows the bucket-size label of the width actually shown (`max(ladderWidth(span), alignedRung(axisStart, axisEnd))`; hourly for a window over a day, 6h at an hour-aligned 30 days, 8h when a misaligned start coarsens the aligned rung; at most 120 buckets) | TR-16 |
| AC-11 | Master key locked → 423 | TR-13 |
| AC-12 | Response holds no connection strings/payloads | TR-13 |
| AC-13 | SQL imports in window → SQL lane counts >0 | TR-04 |
| AC-14 | IIS import without `time-taken` → counts only + gap note | TR-07 |
| AC-15 | Per-call `CallTrace.svelte` tray unchanged when **Tracing** opens on `TracingPaper.svelte` | TR-12 |
| AC-16 | IIS import with IP/referer → attrs present; raw cookie absent from marshalled event | TR-19,20 |
| AC-17 | Export with default scrub → no IP/referer/cookie hash in file; `TestBuildCaseNoIPsOrUserIDs` stays green | TR-21 |
| AC-23 | IIS attrs imported; default `GET .../events` → no `client-ip`/`referer`/`cookie:*` in JSON unless opt-in | TR-15 |
| AC-18 | Rollup-backed zoom uses UTC floor-aligned, half-open buckets intersected with [start,end); whole contained UTC-hour sub-buckets come from bucket_ms=3600000 rows and each boundary fragment is filled from a bounded raw scan, at most two scans, one shared query when both fragments lie in the same grain-hour; clipped=true marks a geometric window edge and complete=false marks missing or unfinished data (never interchanged); p95 <15 ms on 1M/5M (rollups complete) | TR-34 |
| AC-19 | Saved SQL lane read → trace SQL lane uses saved rows | TR-26 |
| AC-20 | Rollup count_all/count_with_duration/sum_us/min_us/max_us bit-exact vs raw scan (integer-microsecond arithmetic) | TR-34 |
| AC-21 | Approximate p95 always carries lower/upper bounds + "approximate" label | TR-34 |
| AC-22 | Kill mid-import → reopen succeeds, no partial rows (BM-12) | BM-12 / WP-57 |

**Performance budgets (BM-01..BM-12 are packets in section 9; these are targets, not measured):**

| ID | Budget | Notes |
|---|---|---|
| BM-01 | Generator with skew/bursts/heavy tails, deterministic seed | perf research §4 |
| BM-02 | Ingest: narrow rows **100k rows/s batched**, 25k events/s; counter-style **20k events/s** | honest budgets, not 80–150k events/s |
| BM-03 | Rollup build cost + bytes overhead at 1M/5M | — |
| BM-04 | Rollup-backed zoom p95 **<15 ms** on the rollup-only mode (an aligned window issues no raw scan); a window with a clipped edge necessarily serves that edge from the bounded raw scan; raw 1M zoom publish 250 ms or refuse; the benchmark states which mode it measured | exact counts; percentiles approximate |
| BM-05 | Rollup merge exactness (count/sum/min/max) | property test |
| BM-06 | Sketch p95/p99 error within reported [lower,upper] | — |
| BM-07 | Warm reopen **<100 ms**, cold **<300 ms** with deferred verify | requires MVP-2 lazy verify |
| BM-08 | WAL/disk amplification during ingest | guard 2× peak |
| BM-09 | Import vs concurrent readers (WAL) | — |
| BM-10 | Windows fsync/cold-cache/DPAPI cost, plain vs encrypted | — |
| BM-11 | Baseline store + CI budget checker | relative p95 |
| BM-12 | Crash-safety micro-bench: kill mid-import → no partial rows | recompute: guard that batching kept atomicity |

## 11. Risks

| Risk | Size | Mitigation |
|---|---|---|
| IIS `time` semantics wrong (`w3cTimeIsEnd`) | M | VF-01; footnote `gapW3CTimeEnd` until owner confirms; never claim alignment |
| `time-taken` scope misunderstood | M | tooltip must use verified wording; VF-01 |
| PII in shareable cases | M | scrub default ON; warn on export; never store raw cookies |
| Ingest target misread as events/s | M | publish both budgets (BM-02); no >25k events/s promise |
| Zoom/reopen budgets unmet | M | rollups + deferred verify; AC/BM labelled unverified on target hardware |
| Mongo `system.profile` needs a WRITE to enable | L | Layer 4 stays read-only; explicit owner-approved off-by-default switch, or skip |
| `dm_os_wait_stats` cumulative; QS durations µs | M | store deltas; QS importer already ÷1000 (`querystore.go:38-41`) |
| Rollup percentile precision claimed as exact | M | label approximate with bounds (AC-21) |
| 30-day audit lane over a live Mongo source is slower than the old 7-day window | M | server-side bucket aggregation with its own completeness and error reporting; rollup tables (MVP-2) serve only aligned local SQL/IIS zoom windows, never the live audit lane; in MVP-1 the SQL/IIS lanes read the saved case so they are fine; state honestly if the audit store cannot serve >7 days |
| Two timelines (text vs chart vs call trace) confusion | S | clear naming; text timeline stays in Import panel |
| Removing a working feature (guided 11-step analysis) | S | nothing is deleted; WP-64 is a case-home button swap only; guided components/routes and web unit tests stay; guided e2e specs move to `tools/e2e/archive/guided/`; release readiness is the mandatory, blocking sole gate `./run-e2e.ps1 -Release`, which runs the main suite then the archive suite, writes `report/main.json` and `report/archive.json`, and fails (non-zero) unless every required test ID in the test-ID set generated from the version-controlled accepted scenario inventory appears in exactly one of the suites, no orphan suite ID exists, and both reports are present, non-empty and passing |
| Demo has no case events → AC-09 fails | S | seed demo case before TR-18 |
| Allowlist prefix `cookie:*` weakens exact match | M | add prefix branch only; keep exact-match path; test |
| Migration v4→v5 partial write | S | one transaction, `IF NOT EXISTS` |

## 12. Open decisions and verify-first list

_The owner made these decisions on Oct 30. D-4 differs from the original recommendation. D-2 also differs from the original recommendation: the owner picked A; our suggestion was C._

| ID | Question | Options | Decided |
|---|---|---|---|
| D-1 | Swim lanes vs single axis | A single B stacked | DECIDED: **B** (stacked rows) |
| D-2 | Guided vs Tracing entry | A replace B tracing-only C both | DECIDED: **A** (owner picked A; our suggestion was C) — Tracing replaces guided as the front door: the case-home Guided button is swapped for a Tracing button. Guided screens stay in the app and in the repo, unlisted, not offered on the case home; nothing is deleted. |
| D-3 | IIS lane metrics | A counts B +duration C +status | DECIDED: **C** (counts + duration + status, footnoted) |
| D-4 | Max window | A 7d B shorter default | DECIDED: **30-day (one month) max**; 1-hour default (owner overrode the spec's 7-day cap) |
| D-5 | Audit lane source | A live Mongo only B +demo | DECIDED: **B** (live + demo); drop "case audit rows" (not a path) |
| D-6 | API shape | A `POST .../tracing` B extend `/timeline` | DECIDED: **A** |
| D-7 | SQL lane MVP-1 | A imports B +live ephemeral C both | DECIDED: **A** (saved events only in MVP-1); MVP-2 persists live pulls |
| D-8 | Cookie allowlist scope | A global B per case C per import | DECIDED: **B** — per-case allowlist; persist allowlist + one 32-byte HMAC key in `meta` key `cookie.hmac_key_v1`; pass `CookiePolicy` on every import via `importer.Options` |
| D-9 | Scrub default | A both exports ON B evidence only | DECIDED: **A** (scrub both exports) |
| D-10 | Live pull storage shape | A new `events` kinds B JSON blob | DECIDED: **A** (normal event rows) |
| D-11 | Deep-link to Call trace | A no B optional | DECIDED: **B** (optional audit link, Later) |
| D-12 | Win32 partition | A never B estimate series | DECIDED: **B** (estimate series, Later, labelled estimate) |

### 12.1 Open owner questions (decided by delegation, awaiting owner confirmation)

These ten rulings were made by the delegated decider and each one changes or adds an owner-visible choice; APPROVED rows are frozen owner decisions. A second reviewing agent may also be asked to weigh in; rows marked APPROVED are frozen, and no row is OPEN now (R6-02 axis and R6-03 partial-overlap 409 were approved on 2026-10-31). Each Recommended default cell records its DEC8 delegated verdict (CONFIRM, REFINE or REVERSE).

| ID | Question for the owner | Options | Recommended default (in the spec now) | If declined, what changes | Spec places | Owner answer |
|---|---|---|---|---|---|---|
| R3-001 | Should export confirm be stateless and bound only to the case revision instead of a one-shot token? | Stateless revision-bound confirm with 409 as the only stale code; or the older one-shot, expiry and unknown-preview 404 token. | Stateless and revision-bound: preview and confirm recompute the digest any number of times and 409 is the only stale code. DEC8 verdict: REFINE. | The one-shot per revision and expiry claims and the 404 unknown-preview path return, and two same-revision previews collide. | 5.2; TR-21; WP-06, WP-07; SC-PRV-23, SC-PRV-27, SC-PRV-104 | APPROVED (owner, 2026-10-31, round-5 recommended decisions) |
| R3-004 | Should presentation clipping and missing data be two separate flags? | Separate clipped and complete flags with the intersection and 6-hour fill algorithm; or one partial label for both meanings. | Separate flags: intersection with the window, whole contained hours from rollups and each boundary fragment is filled from a bounded raw scan, at most two scans, one shared query when both fragments lie in the same grain-hour. DEC8 verdict: CONFIRM. | The old partial wording returns and a geometric edge is no longer told apart from missing or unfinished data. | 4.5; 7; TR-34; WP-31, WP-33; SC-ADV-16, SC-ADV-17, SC-ADV-18; EV-ADV-180 | APPROVED (owner, 2026-10-31, round-5 recommended decisions) |
| R3-010 | Should durations be integer microseconds with separate count_all and count_with_duration? | Integer-microsecond durations with count_all and count_with_duration; or float min_ms and max_ms with one count as the denominator. | Integer microseconds via toMicros, with count_all and count_with_duration separate and min_us and max_us integers. DEC8 verdict: CONFIRM. | Rollups go back to float min_ms and max_ms and a single count, so exactness in integer arithmetic no longer holds. | 4.5; 7; TR-34; WP-30; SC-API-168, SC-API-203; EV-ADV-18, EV-ADV-19; AC-20 | APPROVED (owner, 2026-10-31, round-5 recommended decisions) |
| R3-011 | Should MVP-1 keep server de-duplication with a subscriber count? | Keep shared-read de-duplication keyed by serverWorkId with a subscriber count; or omit it and only bound per-source concurrency. | MVP-1 keeps only per-source concurrency bounds, request-context cancellation, and the client-only per-tab `requestSeq`; there is no server `serverWorkId`, no subscriber count and no shared in-flight read. Two tabs with the same query each perform their own bounded live audit read. DEC8 verdict: REVERSE (MVP-1). | The shared in-flight map is removed, so two tabs with the same query each pay for their own live read. | 5.1; 6; WP-14, WP-16, WP-17; SC-API-166, SC-API-167; EV-API-167, EV-API-168 | APPROVED (owner, 2026-10-31, round-5 recommended decisions) |
| R3-013 | Should this document's Appendix P be the only canonical packet detail? | Appendix P of this document canonical; or keep an external FINAL-PACKETS file as a second normative source. | Appendix P canonical and self-contained, with the 60 s writer in WP-30 and the 3600 s writer in WP-31. DEC8 verdict: CONFIRM. | External pointers stay normative and the coverage and rollup leaf tests can remain merged under TR-24. | 9.1; 9.4; Appendix P P9; TR-24, TR-33, TR-34; SC-ADV-180; SC-API-168 | APPROVED (owner, 2026-10-31, round-5 recommended decisions) |
| R4-001 | Should long windows re-anchor the presentation grid so the bucket count stays at 120? | Re-anchor the grid to start and end for rungs at or above 3600 s; or allow the aligned grid to reach 121 buckets. | UTC floor-aligned grid, never re-anchored; the presented width is `max(ladderWidth(span), alignedRung(axisStart, axisEnd))` so the aligned count is ≤120 and the number of boundary raw scans is zero, one, or two (zero when aligned). DEC8 verdict: REFINE. | A 30-day window not aligned to the hour would exceed 120 buckets and have to be rejected or split. | 4.5; 7; TR-06; WP-15; SC-ADV-120; EV-ADV-01 | APPROVED (owner, 2026-10-31, round-5 recommended decisions) |
| R4-004 | Should a new MVP-1 packet WP-65 own the case.revision writes? | New WP-65 owns every bumpCaseRevision call and the confirm snapshot; or leave counter ownership to WP-06 and WP-07. | The primitive is packet WP-65a (proposed, no export dependency); WP-65b (proposed) calls it at each named mutation site and owns the independent before/after revision assertions; WP-65 owns only the confirm snapshot and the 409 on concurrent import. DEC8 verdict: CONFIRM. | The counter can miss source and policy mutations and a stale preview may still be accepted. | 5.2; 4.6; WP-65, WP-06, WP-07; SC-PRV-23, SC-PRV-27, SC-PRV-104 | APPROVED (owner, 2026-10-31, round-5 recommended decisions) |
| R4-005 | Should the bypassLedger and fromLedger concepts be dropped? | Drop both and return fromStored only; or keep the ledger-bypass bit that reports false provenance. | No `bypassLedger` and no `fromLedger`; `allLanesStored` exists only on `POST .../tracing` responses and is true iff every lane has `origin:"case"`. DEC8 verdict: CONFIRM. | The bypassLedger field returns to GET coverage and POST tracing and keeps reporting fromLedger false. | 4.3; 5.1; 5.3; WP-14, WP-34; SC-API-206; SC-PRV-184 | APPROVED (owner, 2026-10-31, round-5 recommended decisions) |
| R4-009 | Should a bad duration reject the whole event at import? | Reject the whole event so count_all does not increment; or store it and exclude it only from count_with_duration. | Keep the event with no duration and durationStatus:'invalid'; it increments count_all and not count_with_duration, no invalid float is serialized, and only records that cannot be safely identified or that violate envelope integrity are quarantined. DEC8 verdict: REVERSE. | The event is stored and counted while only count_with_duration excludes it, contradicting refused at import. | 4.5; 7; TR-34; WP-30; SC-API-168, SC-API-203; EV-ADV-18, EV-ADV-19, EV-ADV-202; AC-20 | APPROVED (owner, 2026-10-31, round-5 recommended decisions) |
| R4-010 | Should the 30-day audit broad-scan path carry the proposed explicit limits? | Named broad-scan path with the proposed limits; or an unbounded 30-day audit scan. | One server-side $match+$group aggregation per source under the existing 60 s CensusQueryBudget and broad-scan gate; it never pages raw rows to build counts, and a single command is a live aggregate, not a snapshot. BroadScanBuckets is retired. DEC8 verdict: REFINE. | The limits are adjusted or removed and the audit lane may scan a 30-day span with no named page, time or concurrency bound. | 5.1; 5.4; 7; WP-17; SC-API-49, SC-API-50, SC-API-51, SC-API-52; EV-UI-140, EV-UI-221 | APPROVED (owner, 2026-10-31, round-5 recommended decisions) |
| R6-02 | Should the UTC floor-aligned presentation axis ever be replaced by a request-anchored axis? | Replace the UTC floor-aligned, never-re-anchored axis with a request-anchored one; or keep the UTC floor-aligned axis with the coarser-rung rule. | Keep the aligned axis (Ruling 2): there is exactly one presentation axis, the UTC floor-aligned, never-re-anchored grid, and the presented width is `max(ladderWidth(span), alignedRung(axisStart, axisEnd))`. Round-6 verdict: CONFIRM (no owner change). | Every acceptance case changes a second time, R5-03/R4-001 are reopened, and off-hour windows and the 30-day promise are re-decided. | 4.5; 7; TR-06; WP-15; SC-API-09, SC-API-10, SC-API-220, SC-API-260, SC-API-286; EV-API-220, EV-API-260, EV-API-286 | APPROVED (owner, 2026-10-31, round-6 confirmation) |
| R6-03 | Should an MVP-2 changed pull over a partially overlapping stored window return 409 window-overlap, segment the interval, or keep superseded observations? | 409 window-overlap (re-pull the exact stored window or import a disjoint window); or interval segmentation; or keep superseded observations queryable. | 409 window-overlap: an observation is scoped by `(source, lane, fingerprint, window)` with `window` the exact half-open interval requested, and a changed pull whose window does not exactly equal a stored observation's window is refused. Round-6 verdict: CONFIRM (no owner change). | Partial overlap is silently unioned, or superseded versions stay queryable and force query-time dedup. | 4.5; 5.3; TR-26, TR-33; WP-26, WP-27; SC-ADV-01, SC-ADV-02, SC-ADV-162; EV-ADV-163 | APPROVED (owner, 2026-10-31, round-6 confirmation) |

**Verify on real samples first (packet VF-01 does all of these):**

| # | Claim | Why | Status |
|---|---|---|---|
| VF-01a | IIS `time-taken` semantics (does it include network? ends at last send completion) | tooltip + span | Verify before any latency claim |
| VF-01b | W3C `time` = request end | `w3cTimeIsEnd` unverified | Verify before flipping |
| VF-01c | HTTPERR files carry no `#Fields` header | parser assumption | Verify |
| VF-01d | Trailing HTTPERR queue/pool field by IIS version | parser assumption | Verify |
| VF-01e | First W3C row inflated | affects start estimate | Verify |
| VF-01f | Mongo `system.profile` needs a write to enable | Layer 4 gating | Verify; keep read-only unless owner approves a labelled off-by-default switch |
| VF-01g | `dm_os_wait_stats` cumulative | store deltas | Verify |
| VF-01h | Query Store durations microseconds | ÷1000 | Already coded (`querystore.go:38-41`); confirm on samples |
| VF-01i | Live SQL/Mongo day storage shape | MVP-2 design | `(unverified)` |
| VF-01j | Exact allowlist prefix-match site in `event.go` | P1 change | Prefix branch in `Validate`/`ValidateFor` |
| VF-01k | `Options` file location for `CookiePolicy` | P1 | `internal/importer/importer.go:62` (found) |
| VF-01l | Existence of a host-metrics importer prefix | P11 | Check `all.go`/`resource/metrics.go` |
| VF-01m | `perfbench` gating/commands | BM packets | `internal/perfbench/doc.go` |

**Reused unverified items that must be confirmed in their packets (Appendix P is the canonical packet detail):** the `Options` file for `CookiePolicy`; the exact allowlist prefix-match site in `event.go`; HTTPERR `#Fields` columns; the web test command (`node --test tests/*.test.cjs` confirmed here); exact `types.go` line numbers (symbols confirmed); whether a host-metrics importer prefix exists.

---

## 13. Acceptance criteria in Gherkin and eval cases

This section is the detailed, testable form of sections 8 and 10. Each Gherkin scenario has a unique `@SC-` id, the `@TR-`/`@AC-`/`@BM-` ids it proves, a phase tag and exactly one automation level (`@unit @api @e2e @manual @bench`). Eval cases (`EV-`) are the extra boundary, hostile-input, resilience, accessibility and regression cases. The wireframe state catalogue (`WS-`) lists every screen state the next wireframe must draw. Items marked "(proposed)" or "(unverified)" are not yet decided or checked and are collected in each area's Open points.

How to use: a work packet is done when every scenario tagged with its requirements passes; the wireframe is done when every `WS-` row is drawn; an eval case is run at the level named in its Level column.

### Area A: tracing API, window, buckets, lanes (SC-API, EV-API)

### F-API-01 Trace window validation and the 30-day cap  (TR-02, WP-15)

```gherkin
Feature: The tracing window is a real UTC interval no longer than 30 days

  Background:
    Given a case with at least one SQL import and one IIS import

  @SC-API-01 @AC-03 @TR-02 @mvp1 @api
  Scenario: A window of exactly 30 days is accepted
    Given an incident window from 2026-09-01T00:00:00Z to 2026-10-01T00:00:00Z
    When I post it to /api/cases/{id}/tracing
    Then the response status is 200
    And the response window start is 2026-09-01T00:00:00Z and end is 2026-10-01T00:00:00Z

  @SC-API-02 @AC-03 @TR-02 @mvp1 @api
  Scenario: A window of 30 days and 1 second is refused
    Given an incident window from 2026-09-01T00:00:00Z to 2026-10-01T00:00:01Z
    When I post it to /api/cases/{id}/tracing
    Then the response status is 400
    And the body is a plain-language error that says the window is longer than 30 days

  @SC-API-03 @TR-02 @mvp1 @api
  Scenario: A window with the end before the start is refused
    Given an incident window from 2026-09-02T00:00:00Z to 2026-09-01T00:00:00Z
    When I post it to /api/cases/{id}/tracing
    Then the response status is 400
    And the body is a plain-language error that says the end must be after the start

  @SC-API-04 @TR-02 @mvp1 @api
  Scenario: A zero-length window is refused
    Given an incident window from 2026-09-01T00:00:00Z to 2026-09-01T00:00:00Z
    When I post it to /api/cases/{id}/tracing
    Then the response status is 400
    And the body is a plain-language error that says the end must be after the start

  @SC-API-05 @TR-02 @TR-05 @mvp1 @api
  Scenario: A non-UTC zone offset is accepted and stored as UTC
    Given an incident window from 2026-09-01T10:00:00+02:00 to 2026-09-01T12:00:00+02:00
    When I post it to /api/cases/{id}/tracing
    Then the response status is 200
    And the response window start is 2026-09-01T08:00:00Z and end is 2026-09-01T10:00:00Z

  @SC-API-06 @TR-02 @mvp1 @api
  Scenario: A window with no zone is refused
    Given an incident window given as the zone-less text 2026-09-01T00:00:00
    When I post it to /api/cases/{id}/tracing
    Then the response status is 400
    And the body is a plain-language error that says both window times need a zone

  @SC-API-07 @AC-04 @TR-08 @mvp1 @api
  Scenario: A reference window that overlaps the incident is a non-blocking notice
    Given an incident window from 2026-09-01T10:00:00Z to 2026-09-01T11:00:00Z
    And a reference window from 2026-09-01T10:30:00Z to 2026-09-01T11:30:00Z
    When I post it to /api/cases/{id}/tracing
    Then the response status is 200
    And warnings contains a notice that the two windows overlap
    And the three lane rows are still returned

  @SC-API-08 @AC-08 @TR-03 @mvp1 @api
  Scenario: A closed case answers 409 and reads nothing
    Given the case is closed
    When I post a valid window to /api/cases/{id}/tracing
    Then the response status is 409
    And the body reason is case-closed
    And no source was read

  @SC-API-100 @TR-04 @mvp1 @api
  Scenario: A tracing request with no sources is refused
    Given a case with at least one source
    When I post a one hour window with an empty sourceIds array
    Then the response status is 400
    And the body is a plain-language error that at least one source is needed

  @SC-API-101 @TR-13 @mvp1 @api
  Scenario: A tracing request with an unknown field is refused
    Given a one hour incident window
    And the request JSON adds a field payload injected
    When I post it to /api/cases/{id}/tracing
    Then the response status is 400
    And the body is a plain-language error that names the unknown field
    And no source was read (ruling 10)

  @SC-API-102 @TR-02 @D-4 @mvp1 @api
  Scenario: A tracing request with no incident window defaults to the last hour
    Given a case with at least one source
    And the tracing request omits the incident window
    When I post it to /api/cases/{id}/tracing
    Then the response window spans exactly one hour ending at the request time
    And the response bucketSeconds is 60

  @SC-API-300 @TR-26 @TR-33 @mvp2 @unit
  Scenario: A changed observed field with the same id and timestamp is changed
    Given a prior pull whose row with _id and source timestamp hashed to canonical row-content digest d
    When a re-pull observes the same _id and timestamp but a different durationMs, status, value, severity or observed attrs
    Then the outcome is changed, because the digest is over the observed canonical row content including every evidence-affecting field, and absent keys are encoded distinctly from present-null keys (ruling 1)
```

### F-API-02 The bucket ladder labels the axis  (TR-06, TR-16, WP-15)

```gherkin
Feature: The bucket size follows the window length and never exceeds 120 buckets

  Background:
    Given a case with a validated time zone rule for every source

  @SC-API-09 @AC-09 @TR-06 @mvp1 @api
  Scenario: A one hour window uses 60 second buckets
    Given an incident window from 2026-09-01T10:00:00Z to 2026-09-01T11:00:00Z
    When I post it to /api/cases/{id}/tracing
    Then the response bucketSeconds is 60
    And the number of buckets is 60

  @SC-API-10 @TR-06 @mvp1 @api
  Scenario: A window of exactly two hours uses 60 second buckets
    Given an incident window from 2026-09-01T10:00:00Z to 2026-09-01T12:00:00Z
    When I post it to /api/cases/{id}/tracing
    Then the response bucketSeconds is 60

  @SC-API-11 @TR-06 @mvp1 @api
  Scenario: A window just over two hours coarsens the buckets to 300 s
    Given an incident window from 2026-09-01T10:00:00Z to 2026-09-01T12:00:01Z
    When I post it to /api/cases/{id}/tracing
    Then the response bucketSeconds is 300
    And the number of buckets is at most 120 (ruling 1)

  @SC-API-12 @TR-06 @mvp1 @api
  Scenario: A window of exactly 24 hours uses 900 s buckets
    Given an incident window from 2026-09-01T00:00:00Z to 2026-09-02T00:00:00Z
    When I post it to /api/cases/{id}/tracing
    Then the response bucketSeconds is 900
    And the number of buckets is 96 (ruling 1)

  @SC-API-13 @TR-06 @mvp1 @api
  Scenario: A window of exactly five days uses hourly buckets
    Given an incident window from 2026-09-01T00:00:00Z to 2026-09-06T00:00:00Z
    When I post it to /api/cases/{id}/tracing
    Then the response bucketSeconds is 3600

  @SC-API-14 @TR-06 @mvp1 @api
  Scenario: A window just over five days uses two hour buckets
    Given an incident window from 2026-09-01T00:00:00Z to 2026-09-06T00:00:01Z
    When I post it to /api/cases/{id}/tracing
    Then the response bucketSeconds is 7200

  @SC-API-15 @TR-06 @mvp1 @api
  Scenario: A ten day window uses two hour buckets
    Given an incident window from 2026-09-01T00:00:00Z to 2026-09-11T00:00:00Z
    When I post it to /api/cases/{id}/tracing
    Then the response bucketSeconds is 7200

  @SC-API-16 @TR-06 @mvp1 @api
  Scenario: A twenty day window uses four hour buckets
    Given an incident window from 2026-09-01T00:00:00Z to 2026-09-21T00:00:00Z
    When I post it to /api/cases/{id}/tracing
    Then the response bucketSeconds is 14400

  @SC-API-17 @AC-03 @AC-09 @TR-06 @mvp1 @api
  Scenario: A thirty day window returns exactly 120 buckets
    Given an incident window from 2026-09-01T00:00:00Z to 2026-10-01T00:00:00Z
    When I post it to /api/cases/{id}/tracing
    Then the response bucketSeconds is 21600
    And the number of buckets is 120
    And the presentation axis is UTC floor-aligned at every rung (axisStart = floorUTC(start,3600000), axisEnd = ceilUTC(end,3600000)) and the grid is never re-anchored, so a misaligned 30-day window coarsens to the next rung and still yields at most 120 buckets and zero, one, or two distinct boundary fragments, zero when aligned and one shared scan when both fall in one grain-hour (ruling 4)

  @SC-API-286 @TR-06 @AC-03 @mvp1 @unit
  Scenario: A misaligned 30-day span coarsens to the next aligned rung
    Given a 30-day span of 2,592,000,000 ms starting at 00:01
    When the aligned grid selects its rung
    Then the presentation axis is UTC floor-aligned (axisStart = floorUTC(start,3600000), axisEnd = ceilUTC(end,3600000)) and the grid is never re-anchored (ruling 3)
    And the aligned count exceeds 120 so the next coarser supported rung 8 h is selected from [60s,300s,600s,900s,1h,2h,4h,6h,8h,12h,24h], so the count is 91 and the number of boundary raw scans is zero when aligned, one when exactly one edge is set or both boundaries share a grain-hour, and two otherwise (ruling 4)

  @SC-API-18 @TR-06 @mvp1 @api
  Scenario Outline: A window never returns more than 120 buckets
    Given an incident window of <length> starting 2026-09-01T00:00:00Z
    When I post it to /api/cases/{id}/tracing
    Then the number of buckets is at most 120
    And the response bucketSeconds is <seconds>

    Examples:
      | length   | seconds |
      | 1 hour   | 60      |
      | 25 hours | 3600    |
      | 9 days   | 7200    |
      | 19 days  | 14400   |
      | 29 days  | 21600   |
      | 30 days  | 21600   |

  @SC-API-220 @AC-03 @AC-09 @TR-06 @mvp1 @api
  Scenario: A misaligned 30-day window keeps the aligned axis and coarsens the rung
    Given an incident window from 2026-09-01T00:01:00Z to 2026-10-01T00:01:00Z
    When I post it to /api/cases/{id}/tracing
    Then the presentation axis is UTC floor-aligned at every rung (axisStart = floorUTC(start,3600000), axisEnd = ceilUTC(end,3600000)) and the grid is never re-anchored (ruling 3)
    And the presented width is W = max(ladderWidth(span), alignedRung(axisStart, axisEnd)) where alignedRung is the first supported rung whose aligned count is ≤120, and the aligned count ceil((axisEnd-axisStart)/W) is always ≤120 (ruling 2)
    And the number of boundary raw scans is zero when aligned, one when exactly one edge is set or both boundaries share a grain-hour, and two otherwise (ruling 4)
    And the response bucketSeconds is 28800 and the number of buckets is 91, because a 30-day span is 2,592,000,000 ms and a 00:01 start keeps axisStart = floorUTC(start,3600000) and never re-anchors, so the aligned count exceeds 120 and the next coarser supported rung 8 h is selected from [60s,300s,600s,900s,1h,2h,4h,6h,8h,12h,24h], and every bucket tiles [start,end) exactly once with no gap and no overlap (ruling 3)

  @SC-API-301 @TR-26 @TR-33 @mvp2 @unit
  Scenario: A removed field differs from null, a duplicate id is incomplete and an invalid duration stays idempotent
    Given one pull observes a row with an absent key and another observes the same row with an explicit null key
    When the two canonical row-content digests are computed
    Then the digests differ because absent keys are encoded distinctly from present-null keys, and a duplicate _id within one pull makes that pull incomplete with no dedup performed (ruling 1)
    And a re-pull whose observed duration is invalid but whose canonical observed content is byte-identical is unchanged, because the digest is over the observed content, not the post-projection row (ruling 1)
```

### F-API-03 Three lanes and their missing-lane reasons  (TR-04, TR-07, TR-11, WP-17, WP-18)

```gherkin
Feature: The trace shows the API audit, SQL and IIS lanes, and says why a lane is empty

  Background:
    Given a case with a validated time zone rule for every source unless the scenario says otherwise

  @SC-API-19 @AC-01 @AC-02 @TR-04 @TR-07 @mvp1 @api
  Scenario: Only an audit source gives an audit lane and missing-source rows
    Given the case holds no SQL import and no IIS import
    And Mongo audit source audit-1 is reachable
    When I post a one hour window to /api/cases/{id}/tracing
    Then the lanes contain a lane with key audit in state events
    And the lane with key sql is in state no-data with a fixed missing-source reason
    And the lane with key iis is in state no-data with a fixed missing-source reason

  @SC-API-20 @AC-01 @TR-04 @mvp1 @api
  Scenario: An IIS import fills the IIS lane
    Given the case holds an iis-w3c import with requests in the window
    When I post that window to /api/cases/{id}/tracing
    Then the lane with key iis is in state events
    And the IIS lane holds at least one event point

  @SC-API-21 @AC-13 @TR-04 @mvp1 @api
  Scenario: Saved SQL events fill the SQL lane from the case
    Given the case holds a sql-xe import with events in the window
    When I post that window to /api/cases/{id}/tracing
    Then the lane with key sql is in state events
    And the SQL lane event count is more than zero

  @SC-API-22 @TR-07 @mvp1 @api
  Scenario: A source that covers other days is out of range, not empty
    Given the case holds an iis-w3c import whose files cover 2026-08-01 only
    When I post a window of 2026-09-01T10:00:00Z to 2026-09-01T11:00:00Z
    Then the lane with key iis is in state out-of-range
    And the output holds no zero line for the IIS lane

  @SC-API-23 @TR-07 @mvp1 @api
  Scenario: A source with events only outside the window is none-seen
    Given the case holds an iis-w3c import covering 2026-09-01 but with no request in the hour
    When I post a window of 2026-09-01T10:00:00Z to 2026-09-01T11:00:00Z
    Then the lane with key iis is in state none-seen
    And the output holds no zero line for the IIS lane

  @SC-API-24 @TR-07 @TR-11 @mvp1 @api
  Scenario: A lane whose time zone is not validated is clock-unchecked
    Given an iis-w3c import whose source has no validated time zone rule
    When I post a one hour window to /api/cases/{id}/tracing
    Then the lane with key iis is in state clock-unchecked
    And the IIS lane holds no plotted events

  @SC-API-140 @TR-04 @TR-02 @mvp1 @api
  Scenario: An IIS event exactly at the window close is excluded
    Given the case holds an iis-w3c import with a request exactly at the window close
    When I post a one hour window to /api/cases/{id}/tracing
    Then the IIS lane holds no event at the window close per the half-open window rule
    And an event exactly at the window open is included (ruling 2)

  @SC-API-25 @TR-07 @mvp1 @api
  Scenario: Every lane returns structured sources with sourceId, kind and label
    Given the case holds two IIS sources of the same kind with distinct case_sources ids
    When I post a one hour window to /api/cases/{id}/tracing
    Then every lane returns sources as objects {"sourceId":"<case_sources.id>","kind":"<importer kind>","label":"<human label>"} (ruling 7)
    And each lane event source is the same immutable case_sources.id, never the importer kind
    And no server IP, credentials or host secret appears in any of the three fields
    And readKind is saved or live and window is optional and present only when it differs from the lane window, with saved↔origin:"case" and live↔origin:"live" (ruling 8)
    And every lane carries origin in {live, case}, asOf, complete, truncated, consistency and reason, and `allLanesStored` exists only on `POST .../tracing` responses and means every lane has `origin:"case"`, so the top-level allLanesStored is true iff every lane has origin:"case" (ruling 6)

  @SC-API-281 @TR-07 @TR-10 @mvp1 @api
  Scenario: Every lane reports its own provenance and allLanesStored
    Given a trace whose audit lane is live and whose SQL and IIS lanes are saved
    When the trace response is returned
    Then every lane carries origin in {live, case}, asOf, complete, truncated and reason (ruling 8)
    And `allLanesStored` exists only on this `POST .../tracing` response and means every lane has `origin:"case"`, true iff every lane has origin:"case", so a mixed trace reports allLanesStored false while each saved lane reports origin:"case", and `GET .../coverage` never carries allLanesStored (ruling 6)

  @SC-API-26 @TR-07 @mvp1 @e2e
  Scenario Outline: Each lane state has a fixed sentence
    Given a lane in state <state>
    When the tracing chart renders it
    Then the lane shows the server-provided sentence for <state>

    Examples:
      | state           |
      | no-data         |
      | none-seen       |
      | clock-unchecked |
      | out-of-range    |
      | incomplete      |
```

### F-API-04 The 50k cap and the truncated flag  (TR-10, WP-15, WP-18)

```gherkin
Feature: A trace over the 50000 event cap says so instead of silently cutting rows

  Background:
    Given a case with a validated time zone rule for every source

  @SC-API-27 @AC-05 @TR-10 @mvp1 @api
  Scenario: More than 50000 IIS events sets truncated
    Given the case holds an iis-w3c import with 50001 requests inside the window
    When I post that window to /api/cases/{id}/tracing
    Then the response truncated is true
    And the response warnings name the raw-row cap
    And the IIS lane holds at most 60 plotted events
    And the IIS lane hidden count is greater than zero

  @SC-API-28 @TR-10 @mvp1 @api
  Scenario: Exactly 50000 IIS events does not set truncated
    Given the case holds an iis-w3c import with 50000 requests inside the window
    When I post that window to /api/cases/{id}/tracing
    Then the response truncated is false

  @SC-API-29 @TR-10 @mvp1 @api
  Scenario: A truncated raw read does not corrupt the lane counts
    Given the case holds an iis-w3c import with 80000 requests inside the window
    When I post that window to /api/cases/{id}/tracing
    Then the response truncated is true
    And the IIS lane bucket counts come from the uncapped server-side GROUP BY aggregate over all matching case events, independent of the 50000 raw-row cap (ruling 4)
    And the raw list is capped at 50000 rows and the response carries no raw attribute map for the lane
    And the lane carries complete false only if the uncapped aggregate cannot complete

  @SC-API-104 @TR-07 @mvp1 @api
  Scenario: An IIS lane over the per-lane event cap hides the overflow
    Given the case holds an iis-w3c import with 61 requests inside the window
    When I post that window to /api/cases/{id}/tracing
    Then the response truncated is false
    And the IIS lane holds exactly 60 events
    And the IIS lane hidden count is 1

  @SC-API-302 @TR-26 @TR-33 @mvp2 @api
  Scenario: A changed pull replaces the observation atomically and never double counts
    Given a stored complete=1 observation for an exact (source, lane, fingerprint, window) scope
    When a changed pull for the same exact window commits
    Then one transaction stages the new import to the same completeness bar, deletes the superseded import with cascading events and coverage, inserts the new import and events, rebuilds the affected rollup buckets, and commits, so counts match the new observation only with no double count (ruling 3)
    And a failed pull leaves the prior observation intact, a partial-overlap window is refused with 409 window-overlap; re-pull the exact stored window or import a disjoint window, and a concurrent pull serializes by the per-case transaction and UNIQUE(observation_key) so one winner commits and the loser returns its outcome (ruling 3)
```

### F-API-05 Tracing is read-only  (TR-03, TR-13, WP-14)

```gherkin
Feature: Opening a trace never writes, imports, grants or leaks

  Background:
    Given a case with at least one IIS import unless the scenario says otherwise

  @SC-API-282 @TR-26 @TR-33 @mvp2 @api
  Scenario: A pull is idempotent on the observed content digest
    Given a prior pull whose rows hashed to a canonical row-content digest d
    When POST .../pulls observes byte-identical canonical row content for the same window
    Then a repeated pull with byte-identical canonical row content is a no-op (StatusDuplicate, outcome unchanged), and a pull that observes changed canonical content atomically replaces the prior observation for the same exact window in one transaction (outcome changed), while a changed pull whose window does not exactly equal a stored observation's window is refused with 409 window-overlap; re-pull the exact stored window or import a disjoint window (ruling 3)
    And coverage means completely read as of asOf, not current, and Pull now always re-reads the source (no TTL freshness reuse); persistence is skipped and the outcome is unchanged (StatusDuplicate) when the canonical row-content digest equals the stored one

  @SC-API-30 @AC-11 @TR-13 @mvp1 @api
  Scenario: A locked master key answers 423 before any read
    Given a master key exists and is locked
    When I post a valid window to /api/cases/{id}/tracing
    Then the response status is 423
    And the body error is locked
    And no source was read

  @SC-API-31 @AC-12 @TR-13 @mvp1 @api
  Scenario: The response holds no connection string or payload
    Given the case holds a Mongo audit source with a connection URI
    When I post a one hour window to /api/cases/{id}/tracing
    Then the response body contains no connection string
    And the response body contains no request or response payload
    And the response body contains no raw cookie value

  @SC-API-32 @TR-03 @TR-13 @mvp1 @api
  Scenario: A trace writes no import, grant or event
    Given a case with an empty import list
    When I post a one hour window to /api/cases/{id}/tracing
    Then the case import list is still empty
    And the case events list is still empty
    And no grant script was run

  @SC-API-285 @TR-13 @mvp1 @api
  Scenario: Two tabs run their own bounded reads with a client-only requestSeq
    Given two tabs post the same valid query at the same time
    When both are posted while one read is in flight
    Then MVP-1 keeps only per-source concurrency bounds, request-context cancellation, and the client-only per-tab requestSeq; there is no server serverWorkId, no subscriber count and no shared in-flight read (ruling 11)
    And two tabs with the same query each perform their own bounded live audit read, and stale responses are ignored by requestSeq per tab

  @SC-API-34 @TR-13 @mvp1 @api
  Scenario: An unavailable case store answers 503
    Given the case store is unavailable
    When I post a valid window to /api/cases/{id}/tracing
    Then the response status is 503
    And the body is a plain-language error that says cases are unavailable

  @SC-API-280 @TR-03 @TR-26 @TR-33 @mvp2 @api
  Scenario: The three operations have distinct read kinds live or saved
    Given a case with a Mongo audit source and a saved SQL import
    And a window whose sub-intervals are missing from the coverage ledger
    When POST .../tracing, GET .../coverage and POST .../pulls are each called for that window
    Then POST .../tracing may run one ephemeral read-only live read for the audit lane and never persists; GET .../coverage reads case.db only, reports gaps, and never fills them; POST .../pulls is the only operation that reads remote SQL/Mongo and persists events+coverage, and GET .../coverage opens no source connection even for an uncovered window (ruling 1)
    And each lane carries readKind in {saved, live} with separate complete and asOf fields, the audit lane carries readKind:"live" and the saved SQL lane carries readKind:"saved"

  @SC-API-35 @TR-13 @mvp1 @api
  Scenario: Tracing never returns server or client IP from the audit side
    Given a Mongo audit source whose records carry a client IP
    When I post a one hour window to /api/cases/{id}/tracing
    Then the response body holds no client IP attribute

  @SC-API-303 @AC-03 @TR-06 @mvp1 @unit
  Scenario: The aligned count is at most 120 for every span and offset
    Given any span from 10 s to 30 d and any offset among 00:00, 00:01, 05:59 and 23:59
    When the UTC floor-aligned grid selects its rung
    Then the presented width is max(ladderWidth(span), alignedRung(axisStart, axisEnd)) where alignedRung is the first supported rung whose aligned count is ≤120 (ruling 2)
    And the aligned count is ceil((axisEnd-axisStart)/W), always ≤120, and there is exactly one presentation axis for MVP-1 and MVP-2 with no presentation discontinuity (ruling 2)
```

### F-API-06 Reusing the investigation filters  (TR-29, WP-21) — LATER per ruling 3, not in MVP-1

```gherkin
Feature: The tracing request accepts the same route and status filters as the investigation (LATER; the MVP-1 request has no filter fields)

  Background:
    Given a case with a Mongo audit source and an IIS import
    And a validated time zone rule for every source

  @SC-API-38 @TR-29 @later @api
  Scenario: A status filter of 5xx narrows the audit lane (LATER per ruling 3; the MVP-1 request has no filter fields)
    Given an incident window of one hour
    And a status filter of server_error (proposed)
    When I post it to /api/cases/{id}/tracing
    Then the audit lane holds only calls whose outcome is a server error (proposed)

  @SC-API-39 @TR-29 @later @api
  Scenario: A route filter longer than the filter limit is refused
    Given a route filter of 257 characters
    When I post a one hour window to /api/cases/{id}/tracing
    Then the response status is 400
    And the body is a plain-language error that says the route is too long

  @SC-API-103 @TR-29 @later @api
  Scenario Outline: The audit controller and action filters share the investigation length cap (proposed)
    Given an incident window of one hour
    And a controller or action filter of <length> characters
    When I post it to /api/cases/{id}/tracing
    Then the response status is <status>

    Examples:
      | length | status |
      | 200    | 200    |
      | 201    | 400    |
```

### F-API-07 HTTPERR markers on the IIS lane  (TR-30, WP-19)

```gherkin
Feature: HTTP.sys error rows appear as point markers on the IIS lane

  Background:
    Given a case with an iis-httperr import and a validated time zone rule

  @SC-API-40 @TR-30 @mvp1 @api
  Scenario: An HTTPERR row becomes a marker in the window
    Given an iis-httperr import with a reason Connection_Dropped at 2026-09-01T10:30:00Z
    When I post a window of 2026-09-01T10:00:00Z to 2026-09-01T11:00:00Z
    Then the lane with key iis holds one event whose label names the reason
    And that event start equals its end

  @SC-API-41 @TR-30 @TR-32 @mvp1 @api
  Scenario: An HTTPERR marker carries its status and host
    Given an iis-httperr import with sc-status 503 and reason QueueFull
    When I post that window to /api/cases/{id}/tracing
    Then that IIS marker event holds status class 5xx (proposed, WP-13)
    And that IIS marker event carries kind iis-httperr, never as its source (ruling 7)

  @SC-API-42 @TR-30 @mvp1 @api
  Scenario: An HTTPERR marker outside the window is not plotted
    Given an iis-httperr import whose only row is at 2026-09-02T10:30:00Z
    When I post a window of 2026-09-01T10:00:00Z to 2026-09-01T11:00:00Z
    Then the lane with key iis holds no HTTPERR marker

  @SC-API-304 @AC-03 @TR-06 @TR-34 @mvp2 @unit
  Scenario: The number of boundary raw scans is zero, one, or two
    Given hasStartEdge = (start % grain != 0) and hasEndEdge = (end % grain != 0)
    When the boundary raw scans are counted
    Then the count is zero when neither edge is set, one when exactly one is set or when both boundaries fall inside the same grain-hour (one bounded raw query serves both), and two otherwise (ruling 4)
    And the edge budget MaxEdgeScanRows=50000 and MaxEdgeScanTime=2s (proposed) applies per fragment and a fragment over budget sets complete=false (ruling 4)
```

### F-API-08 IIS point at the logged timestamp with time-taken shown separately  (TR-31, WP-20)

```gherkin
Feature: An IIS request with time-taken is drawn as a point at its timestamp, gated on verified semantics

  Background:
    Given a case with an iis-w3c import and a validated time zone rule

  @SC-API-43 @TR-31 @mvp1 @api
  Scenario: A request with time-taken is a point at its logged timestamp
    Given an iis-w3c row with time 2026-09-01T10:30:00Z and time-taken 250
    When I post a window of 2026-09-01T10:00:00Z to 2026-09-01T11:00:00Z
    Then the IIS lane event start equals its end and equals 2026-09-01T10:30:00Z (ruling 5)
    And the IIS lane event durationMs is 250 shown separately
    And no derived start is computed until VF-01 confirms the time-taken semantics
    And the response carries the unverified time-end gap note

  @SC-API-44 @AC-14 @TR-07 @TR-31 @mvp1 @api
  Scenario: A request without time-taken is a count with a gap note
    Given an iis-w3c import whose files do not declare time-taken
    When I post a one hour window to /api/cases/{id}/tracing
    Then the IIS lane is in state events
    And the response warnings name the no-time-taken gap
    And the IIS lane holds no span

  @SC-API-45 @TR-31 @mvp1 @e2e
  Scenario: The time-taken tooltip states the verified reading only
    Given the W3C time-taken semantics are still unverified
    When the IIS lane shows a request point in the tracing chart
    Then the tooltip DOM for time-taken carries the unverified gap sentence
    And the tooltip never calls time-taken API processing time (ruling 5)

  @SC-API-305 @TR-04 @TR-07 @TR-10 @mvp1 @api
  Scenario: complete never implies a snapshot and mixed lanes set mixedConsistency
    Given a trace whose audit lane is live and whose saved SQL lane is stored
    When the trace response is returned
    Then complete:true means only that the read command returned to end within its budget with no cap, error or unfinished boundary, never that the source was a stable snapshot (ruling 7)
    And each lane carries consistency in {live-non-snapshot, stored-as-of, snapshot}, defaulting to live-non-snapshot for the live audit lane and stored-as-of for saved SQL and IIS lanes (ruling 7)
    And the live lane sets mixedConsistency:true and the UI shows a non-dismissible note: live and saved lanes are read at different times; totals across lanes are not a conservation check (ruling 7)
```

### F-API-09 Lane payload carries HTTP status and win32  (TR-32, WP-13)

```gherkin
Feature: Every lane event can carry an HTTP status class and a win32 code

  Background:
    Given a case with an iis-w3c import and a validated time zone rule

  @SC-API-46 @TR-32 @mvp1 @api
  Scenario: An IIS 500 request carries status class 5xx and win32 64
    Given an iis-w3c row with sc-status 500 and sc-win32-status 64
    When I post that window to /api/cases/{id}/tracing
    Then the IIS lane event statusClass is 5xx (proposed, WP-13)
    And the IIS lane event win32 is 64 (proposed, WP-13)

  @SC-API-47 @TR-32 @mvp1 @api
  Scenario: An event without a status sends no status class
    Given a sql-xe event in the window that carries no status
    When I post that window to /api/cases/{id}/tracing
    Then that SQL lane event holds no statusClass

  @SC-API-48 @TR-32 @TR-13 @mvp1 @api
  Scenario: The lane event sends no raw attributes
    Given an iis-w3c row with a referer and a user attribute
    When I post that window to /api/cases/{id}/tracing
    Then the lane event object holds no attrs map
    And the lane event object holds no raw referer
```

### F-API-10 The live audit lane is aggregated on the server over 30 days  (TR-04, TR-10, WP-17)

```gherkin
Feature: A 30-day audit lane is bucketed on the server, not summed in the browser

  Background:
    Given a case with a reachable Mongo audit source
    And a validated time zone rule for every source

  @SC-API-49 @TR-04 @TR-10 @mvp1 @api
  Scenario: A 30 day audit lane returns server-made buckets
    Given an audit source with calls spread across 30 days
    When I post a window of 30 days to /api/cases/{id}/tracing
    Then the audit lane holds at most 120 buckets (proposed bucket series, WP-17)
    And the live audit lane computes its buckets with one server-side $match+$group aggregation per source under the existing 60 s CensusQueryBudget and the existing broad-scan gate; it never pages raw rows to build counts, and a single command is a live aggregate, not a snapshot (ruling 7)
    And complete:true means the aggregation command returned to end within budget with no cap or error, asOf is the server clock at read start labelled read began at, and a note states not a snapshot; rows inserted during the read may be included, with complete:false reasons {timeout,rowcap,error}
    And the response holds no raw call list for the audit lane

  @SC-API-50 @TR-10 @mvp1 @api
  Scenario: The raw-row cap does not shrink the audit counts
    Given an audit source with 200000 calls across 30 days
    When I post a window of 30 days to /api/cases/{id}/tracing
    Then the response truncated may be true for raw rows only
    And the audit bucket counts still cover the full 30 days when the single server-side $match+$group aggregation returns to end within the 60 s CensusQueryBudget (ruling 7)

  @SC-API-51 @TR-07 @mvp1 @api
  Scenario: An audit store that cannot serve more than 7 days says so
    Given an audit store whose query refuses a window longer than 7 days
    When I post a window of 30 days to /api/cases/{id}/tracing
    Then the response names the audit window limit in warnings
    And a budget timeout, row cap or error makes the audit lane state incomplete, complete false, reason in {timeout,rowcap,error}, and an unsupported span is refused with the limit named, never silently shortened (ruling 7)

  @SC-API-283 @TR-04 @TR-10 @mvp1 @api
  Scenario: The live audit lane is one server-side group under budget
    Given an audit source with calls spread across 30 days
    When I post a window of 30 days to /api/cases/{id}/tracing
    Then the live audit lane computes its buckets with one server-side $match+$group aggregation per source under the existing 60 s CensusQueryBudget and the existing broad-scan gate; it never pages raw rows to build counts, and a single command is a live aggregate, not a snapshot (ruling 7)
    And a timeout, row cap or error makes the lane complete false with a reason in {timeout,rowcap,error}, and asOf is the server clock at read start labelled read began at

  @SC-API-52 @TR-04 @TR-07 @mvp1 @api
  Scenario: An unreachable audit source gives a fixed reason, not a zero line
    Given the Mongo audit source cannot be reached
    When I post a one hour window to /api/cases/{id}/tracing
    Then the audit lane is in state incomplete with a fixed read-failure reason
    And the output holds no zero audit line
    And the SQL and IIS lanes keep their exact uncapped local case.db counts (ruling 8)
```

### F-API-11 Saved SQL and IIS lanes only in MVP-1  (TR-04, D-7, WP-18)

```gherkin
Feature: The MVP-1 SQL and IIS lanes read the saved case only

  Background:
    Given a case with a validated time zone rule for every source unless the scenario says otherwise

  @SC-API-53 @TR-04 @mvp1 @api
  Scenario: The SQL lane reads sql-* events from the case
    Given a case with a sql-querystore import in the window
    When I post that window to /api/cases/{id}/tracing
    Then the SQL lane is in state events
    And its events carry source as that source's immutable case_sources.id and kind sql-querystore (ruling 7)
    And the SQL lane carries origin:"case", readKind:"saved", plus asOf, complete, truncated and reason (ruling 8)

  @SC-API-54 @TR-04 @mvp1 @api
  Scenario: The IIS lane reads iis-* events from the case
    Given a case with an iis-w3c import in the window
    When I post that window to /api/cases/{id}/tracing
    Then the IIS lane is in state events
    And its events carry source as that source's immutable case_sources.id and kind iis-w3c (ruling 7)
    And the IIS lane carries origin:"case", readKind:"saved", plus asOf, complete, truncated and reason (ruling 8)

  @SC-API-55 @TR-04 @D-7 @mvp1 @api
  Scenario: No live database is opened to fill the SQL lane in MVP-1
    Given a case with no saved SQL events
    When I post a one hour window to /api/cases/{id}/tracing
    Then the SQL lane is in state no-data
    And no SQL connection was opened

  @SC-API-284 @TR-34 @AC-20 @mvp2 @unit
  Scenario: An invalid duration keeps the event with durationStatus invalid
    Given an event whose duration is non-finite, negative or > maxInt64
    When the batch is imported
    Then the event is kept with no duration and durationStatus:'invalid' (ruling 9)
    And it increments count_all and not count_with_duration, no invalid float is ever serialized, and only records that cannot be safely identified or that violate envelope integrity are quarantined

  @SC-API-120 @AC-06 @TR-11 @mvp1 @api
  Scenario: Any chosen source without a validated time zone disables the whole multi-source trace
    Given a trace requesting the audit, sql and iis lanes
    And one imported source has no validated time-zone rule
    When I post a one hour window to /api/cases/{id}/tracing
    Then the response status is 200
    And the response carries the top-level disabled reason tz-rule (proposed wire shape)
    And no lane row plots any event

  @SC-API-121 @TR-02 @TR-05 @mvp1 @api
  Scenario: An offset of plus five thirty is stored as UTC
    Given an incident window from 2026-09-01T10:00:00+05:30 to 2026-09-01T11:00:00+05:30
    When I post it to /api/cases/{id}/tracing
    Then the response window start is 2026-09-01T04:30:00Z and end is 2026-09-01T05:30:00Z

  @SC-API-122 @TR-02 @mvp1 @api
  Scenario: Fractional seconds are kept to the millisecond
    Given an incident window whose start is 2026-09-01T10:00:00.999Z
    When I post it to /api/cases/{id}/tracing
    Then the response window start is 2026-09-01T10:00:00.999Z without rounding loss

  @SC-API-123 @TR-02 @mvp1 @api
  Scenario: A far-future window inside 30 days is accepted
    Given an incident window from 9999-01-01T00:00:00Z to 9999-01-02T00:00:00Z
    When I post it to /api/cases/{id}/tracing
    Then the response status is 200

  @SC-API-124 @TR-04 @TR-07 @mvp1 @api
  Scenario: One audit source failing its aggregation does not hide the other lanes
    Given a trace with two audit sources and an IIS import
    And one audit source's single $match+$group command ends in timeout, rowcap or error
    When I post a one hour window to /api/cases/{id}/tracing
    Then the audit lane is incomplete with complete false and a reason in {timeout, rowcap, error}
    And the IIS lane still renders its events

  @SC-API-221 @TR-03 @TR-13 @mvp1 @api
  Scenario: An audit-only trace opens a read-only customer connection and writes nothing
    Given a case with a reachable Mongo audit source and no SQL or IIS import
    When I post a one hour window to /api/cases/{id}/tracing
    Then the audit lane opens at least one remote read and issues zero remote writes and zero imports
    And the case import list and the case events list are unchanged
    And POST /api/cases/{id}/pulls is the only action that persists a customer read (ruling 2)

  @SC-API-125 @TR-06 @mvp1 @api
  Scenario: A window of ten days and one second uses four hour buckets
    Given an incident window from 2026-09-01T00:00:00Z to 2026-09-11T00:00:01Z
    When I post it to /api/cases/{id}/tracing
    Then the response bucketSeconds is 14400
    And the number of buckets is at most 120

  @SC-API-126 @TR-13 @mvp1 @api
  Scenario: A traced IIS cookie allowlist hit sends no attrs and no digest
    Given a case with an iis-w3c row whose cookie allowlist matched sid
    When I post a one hour window to /api/cases/{id}/tracing
    Then the IIS lane event holds no attrs map
    And the IIS lane event holds no cookie digest

  @SC-API-127 @TR-13 @TR-04 @mvp1 @api
  Scenario: The tracing route is registered and read-only
    Given a case with at least one source
    When I post a valid window to /api/cases/{id}/tracing
    Then the response status is not 404
    And the case import list is unchanged

  @SC-API-166 @TR-13 @mvp1 @api
  Scenario: A newer request from another tab never cancels a different read
    Given a trace in flight carrying requestSeq 1
    And a second trace for the same case carrying its own requestSeq 1
    When the second trace is posted while the first is in flight
    Then the first request is not cancelled by the second (ruling 11)
    And MVP-1 keeps only per-source concurrency bounds, request-context cancellation, and the client-only per-tab requestSeq; there is no server serverWorkId, no subscriber count and no shared in-flight read, and cancelling one request detaches only its own request
    And a client token is never authorization

  @SC-API-222 @TR-13 @mvp1 @api
  Scenario: Two requests that differ only in the reference window never share work
    Given two tracing requests for the same case, sources, incident window and bucketSeconds
    And their reference windows differ
    When both are posted
    Then they share no buckets, warnings or projections, because there is no server serverWorkId and no shared in-flight read (ruling 11)
    And each request carries its own client-only per-tab requestSeq, and coverage uses the separate per-source coverage fingerprint

  @SC-API-167 @TR-13 @mvp1 @api
  Scenario: Two tabs with the same full query each perform their own bounded live audit read
    Given two clients hold the same valid case key
    When each posts the same window and the same request-shaping fields while one read is in flight
    Then two tabs with the same query each perform their own bounded live audit read (ruling 11)
    And there is no server serverWorkId, no subscriber count and no shared in-flight read
    And neither tab cancels the other
    And per-source concurrency below MaxAuditConcurrency (2, proposed) stays bounded separately

  @SC-API-168 @TR-34 @mvp2 @unit
  Scenario: A rollup latency sum is stored as integer microseconds
    Given two rollup buckets each holding a duration_ms
    When the rollup series is read
    Then the total is sum_us, an integer number of microseconds (ruling 6)
    And min_us and max_us are integer microseconds and count_all and count_with_duration are separate (ruling 10)
    And a duration is roundHalfToEven(ms * 1000) for a finite nonnegative value, refused for NaN, Inf, negative or overflow
    And a non-finite, negative or > maxInt64 duration keeps the event with no duration and durationStatus:'invalid'; it increments count_all and not count_with_duration, no invalid float is ever serialized, and only records that cannot be safely identified or that violate envelope integrity are quarantined (ruling 9)
    And no sum_ms field exists

  @SC-API-180 @TR-29 @mvp1 @api
  Scenario: An MVP-1 request carrying route is refused with 400
    Given an incident window of one hour
    And the request JSON adds a route filter field
    When I post it to /api/cases/{id}/tracing
    Then the response status is 400 (ruling 6)
    And the body is a plain-language error that names route as an unknown field
    And no source was read

  @SC-API-181 @TR-10 @TR-13 @mvp1 @api
  Scenario: Every lane carries scanned, truncated and complete
    Given a case whose raw read hit the 50000 cap
    And whose server-side aggregate completed
    When I post that window to /api/cases/{id}/tracing
    Then every lane carries scanned, truncated and complete
    And a lane whose aggregate could not complete carries complete false
    And the SQL and IIS bucket counts come from their uncapped local case.db aggregate (ruling 8)

  @SC-API-182 @TR-02 @TR-06 @mvp1 @unit
  Scenario: The window boundary is half-open at the millisecond
    Given a window from T to T plus 60 s
    When one event sits exactly at T and one exactly at T plus 60 s
    Then the event at T is in the first bucket
    And the event at T plus 60 s is excluded from the window

  @SC-API-200 @TR-13 @mvp1 @api
  Scenario: Unknown request fields are refused before any connection
    Given an incident window of one hour
    And the request JSON adds an unknown field
    When I post it to /api/cases/{id}/tracing
    Then the response status is 400 and the body names the unknown field
    And validation completes before any connection opens (ruling 10)

  @SC-API-201 @TR-13 @mvp1 @api
  Scenario: Cancelling one request never affects another request
    Given two UI instances each hold their own in-flight bounded audit read
    When one instance cancels its request
    Then cancel detaches only its own request and the other request is unaffected (ruling 11)
    And there is no subscriber count and no shared in-flight read

  @SC-API-202 @TR-02 @TR-06 @mvp1 @unit
  Scenario: The half-open boundary holds at every bucket edge
    Given a window from T to T plus 3600 s
    When one event sits exactly at T and one exactly at T plus 3600 s
    Then the event at T is counted and the event at T plus 3600 s is excluded (ruling 2)

  @SC-API-203 @TR-34 @mvp2 @unit
  Scenario: Durations are computed in integer microseconds
    Given a finite nonnegative duration of 1.5 ms
    When it is converted with toMicros
    Then the stored value is 1500 us and min_us and max_us are integers (ruling 10)
    And count_all counts every stored event while count_with_duration counts only those with a duration
    And a non-finite, negative or > maxInt64 duration keeps the event with no duration and durationStatus:'invalid'; it increments count_all and not count_with_duration, no invalid float is ever serialized, and only records that cannot be safely identified or that violate envelope integrity are quarantined; a missing duration is stored as durationStatus:'missing' and increments count_all only (ruling 9)

  @SC-API-204 @TR-10 @TR-13 @mvp1 @api
  Scenario: Per-lane completeness is reported independently
    Given a case whose raw read hit the 50000 cap
    When I post that window to /api/cases/{id}/tracing
    Then each lane carries scanned, truncated and complete (ruling 7)
    And a truncated lane never claims an exact total

  @SC-API-223 @TR-03 @TR-13 @mvp2 @api
  Scenario: A stored-coverage repeat reports allLanesStored true on tracing and opens no source
    Given a canonical query already recorded in the coverage ledger
    When the same query is requested again with no extra request bit
    Then the POST .../tracing result assembled from already-stored coverage/rollups has every lane origin:"case" and the top-level allLanesStored true, and allLanesStored exists only on POST .../tracing responses and means every lane has origin:"case" (ruling 6)
    And GET .../coverage returns {entries:[...], asOf, gaps:[...], provenance:"case-ledger"} and never carries allLanesStored, no source is read, and case.db is unchanged, and a strict parse refuses a bypassLedger field as an unknown field with 400 (ruling 5)

  @SC-API-205 @TR-33 @mvp2 @api
  Scenario: A live pull records exactly one attempt and one coverage interval
    Given an authorized source and a half-open window
    When POST /api/cases/{id}/pulls is called
    Then exactly one import_attempts row is written outside the import transaction
    And events and coverage are written inside one transaction (ruling 2)
    And an offline source answers 502 with a failed attempt row and no coverage

  @SC-API-206 @TR-03 @TR-13 @mvp2 @api
  Scenario: A stored-coverage result reports allLanesStored true on tracing and writes nothing
    Given a canonical query already recorded in the coverage ledger
    When the same query is requested again
    Then the POST .../tracing result has every lane origin:"case" and the top-level allLanesStored true, and allLanesStored exists only on POST .../tracing responses and means every lane has origin:"case" (ruling 6)
    And GET .../coverage returns entries, asOf, gaps and provenance:"case-ledger" and never carries allLanesStored, and no source is read and case.db is unchanged
    And three operations are distinct: POST .../tracing may run one ephemeral read-only live read for the audit lane and never persists; GET .../coverage reads case.db only, reports gaps, and never fills them; POST .../pulls is the only operation that reads remote SQL/Mongo and persists events+coverage, and GET .../coverage opens no source connection even for an uncovered window (ruling 1)

  @SC-API-224 @TR-13 @mvp1 @api
  Scenario: A changed request-shaping field keeps the two requests independent
    Given two tracing requests for the same case, sources, incident window, reference window and bucketSeconds
    And they differ only in caseRevision or in a canonicalFlags bit that changes the output
    When both are posted
    Then they share no buckets, warnings or projections, because there is no server serverWorkId and no shared in-flight read (ruling 11)
    And stale responses are ignored by the client-only per-tab requestSeq

  @SC-API-225 @TR-04 @TR-07 @mvp1 @api
  Scenario: The audit aggregation stays under budget and names its limit
    Given a 30-day audit read served by one server-side $match+$group aggregation per source
    And it runs under the existing 60 s CensusQueryBudget, the broad-scan gate MaxRollupGroups 600000 fail-closed with $limit+1, and MaxAuditConcurrency 2 (proposed)
    When the command times out, hits the row cap or errors
    Then the audit lane is state incomplete with complete false and a reason in {timeout,rowcap,error} (ruling 7)
    And an unsupported span is refused with the limit named, never silently shortened
    And the SQL and IIS lanes keep their exact uncapped local counts

  @SC-API-227 @TR-07 @mvp1 @api
  Scenario: A failed import keeps its SourceRef provenance and kind stays required
    Given the case holds a failed import for a second iis-w3c source with a distinct case_sources id
    When I post a one hour window to /api/cases/{id}/tracing
    Then the failed import keeps its provenance in the lane and both iis-w3c sources appear with distinct sourceId and the same required kind (ruling 8)
    And readKind is saved or live and window is present only when it differs from the lane window

  @SC-API-226 @TR-03 @TR-33 @mvp2 @api
  Scenario: Only a pull increments coverage
    Given a source whose coverage ledger already holds its canonical interval
    When GET /api/cases/{id}/coverage and POST /api/cases/{id}/tracing are called for the same window
    Then the POST .../tracing result has every lane origin:"case" and the top-level allLanesStored true, allLanesStored exists only on POST .../tracing responses and means every lane has origin:"case", and GET .../coverage returns entries, asOf, gaps and provenance:"case-ledger" and never carries allLanesStored, and the coverage rows are unchanged (ruling 6)
    And only POST /api/cases/{id}/pulls writes new events and coverage (ruling 5)

  @SC-API-228 @TR-34 @mvp2 @unit
  Scenario: Zero and fractional durations store in one transaction while a bad duration is kept without a duration
    Given an event with duration 0 ms, an event with duration 1.5 ms and an event with a non-finite duration in one import batch
    When the batch is imported
    Then the 0 ms and 1.5 ms events are stored and the non-finite event is kept with no duration and durationStatus:'invalid', all in one import transaction
    And the kept event increments count_all and not count_with_duration, no invalid float is ever serialized, and only records that cannot be safely identified or that violate envelope integrity are quarantined (ruling 9)

  @SC-API-240 @TR-26 @TR-33 @mvp2 @api
  Scenario: The three tracing operations have distinct read kinds
    Given a case with a Mongo audit source and a saved SQL import
    And a window whose sub-intervals are missing from the coverage ledger
    When GET /api/cases/{id}/coverage is called for that window
    Then GET .../coverage reads case.db only, reports gaps, and never fills them, and opens no source connection even for an uncovered window (ruling 1)
    And every lane carries readKind in {saved, live} and separate complete and asOf fields, with asOf an RFC3339 UTC time the live read began, one value per source, not the window end
    And the audit lane carries readKind:"live" and the saved SQL lane carries readKind:"saved", and only POST .../pulls reads remote SQL/Mongo and persists events+coverage

  @SC-API-241 @TR-07 @TR-10 @mvp1 @api
  Scenario: Per-lane provenance replaces one top-level boolean
    Given a trace whose audit lane is live and whose IIS and SQL lanes are saved
    When the trace response is returned
    Then every lane carries origin in {live, case}, asOf, complete, truncated and reason (ruling 8)
    And `allLanesStored` exists only on this `POST .../tracing` response and means every lane has `origin:"case"`, true iff every lane has origin:"case"
    And a trace mixing a live audit lane with saved IIS/SQL lanes reports allLanesStored false while each saved lane reports origin:"case", and GET .../coverage never carries allLanesStored (ruling 6)

  @SC-API-242 @TR-04 @TR-10 @mvp1 @api
  Scenario: The live audit lane is one server-side aggregation, not a snapshot
    Given an audit source with calls spread across 30 days
    When I post a window of 30 days to /api/cases/{id}/tracing
    Then the live audit lane computes its buckets with one server-side $match+$group aggregation per source under the existing 60 s CensusQueryBudget and the existing broad-scan gate (ruling 7)
    And it never pages raw rows to build counts, and complete:true means the aggregation command returned to end within budget with no cap or error
    And asOf is the server clock at read start and a note states not a snapshot; rows inserted during the read may be included

  @SC-API-243 @TR-34 @mvp2 @unit
  Scenario: An invalid-duration event is kept with no duration
    Given an event whose duration is non-finite, negative or > maxInt64
    When the batch is imported
    Then the event is kept with no duration and durationStatus:'invalid' (ruling 9)
    And it increments count_all and not count_with_duration, and no invalid float is ever serialized
    And only records that cannot be safely identified or that violate envelope integrity are quarantined

  @SC-API-244 @TR-13 @mvp1 @api
  Scenario: Two identical requests run two bounded reads with a client-only requestSeq
    Given two tabs post the same valid query at the same time
    When both are posted while one read is in flight
    Then MVP-1 keeps only per-source concurrency bounds, request-context cancellation, and the client-only per-tab requestSeq (ruling 11)
    And Two tabs with the same query each perform their own bounded live audit read, and there is no server serverWorkId, no subscriber count and no shared in-flight read
    And stale responses are ignored by requestSeq per client, and a client token is never authorization

  @SC-API-245 @TR-26 @TR-33 @mvp2 @api
  Scenario: A re-pull idempotent on the canonical row-content digest
    Given a prior pull whose rows hashed to a canonical row-content digest d
    When POST /api/cases/{id}/pulls observes byte-identical canonical row content for the same window
    Then a pull is idempotent on observed content, computed as a canonical row-content digest (lowercase-hex SHA-256 over a versioned, deterministic serialization of the ordered observed rows, one line per row, in the source’s observed order tie-broken by _id, each field length-prefixed and type-tagged including explicit nulls, with content_digest_v recording the canonicalization version starting 1)
    And a repeated pull with byte-identical canonical row content is a no-op (StatusDuplicate, outcome unchanged), and a pull that observes changed canonical content atomically replaces the prior observation for the same exact window in one transaction (outcome changed), while a changed pull whose window does not exactly equal a stored observation's window is refused with 409 window-overlap; re-pull the exact stored window or import a disjoint window (ruling 3)
    And coverage means completely read as of asOf, not current, and Pull now always re-reads the source (no TTL freshness reuse); persistence is skipped and the outcome is unchanged (StatusDuplicate) when the canonical row-content digest equals the stored one

  @SC-API-260 @TR-06 @AC-03 @mvp1 @unit
  Scenario: Every bucket width is derived from integer time-boundary arithmetic
    Given a 30-day span of 2,592,000,000 ms starting on an exact UTC hour
    When the aligned grid selects its rung
    Then W = span/120 = 21,600,000 ms (6 h nominal) and 120 contiguous non-overlapping buckets tile [start,end) exactly once (ruling 2)
    And a misaligned start keeps axisStart = floorUTC(start,3600000) and the grid is never re-anchored, so the next coarser supported rung is selected from [60s,300s,600s,900s,1h,2h,4h,6h,8h,12h,24h], the count is ≤120, and the number of boundary raw scans is zero when neither edge is set, one when exactly one edge is set or when both boundaries fall inside the same grain-hour (one bounded raw query serves both), and two otherwise (ruling 4)

  @SC-API-261 @TR-03 @TR-04 @TR-07 @TR-33 @mvp2 @api
  Scenario: Three operations and per-lane provenance are reported together
    Given a case with a Mongo audit source and a saved SQL import
    When I post one window to /api/cases/{id}/tracing
    Then POST .../tracing may run one ephemeral read-only live read for the audit lane and never persists, GET .../coverage reads case.db only, reports gaps, and never fills them, and POST .../pulls is the only operation that reads remote SQL/Mongo and persists events+coverage, and GET .../coverage opens no source connection even for an uncovered window (ruling 1)
    And every lane carries origin in {live, case}, asOf, complete, truncated and reason, with readKind aligned saved↔origin:"case" and live↔origin:"live" (ruling 8)
    And the audit lane carries origin:"live" and readKind:"live", the saved SQL lane carries origin:"case" and readKind:"saved", and allLanesStored exists only on this POST .../tracing response and means every lane has origin:"case", true iff every lane has origin:"case" (ruling 6)
```

#### Eval cases

| EV-id | Category | Setup / input | Expected result | Maps to | Level | Phase |
|---|---|---|---|---|---|---|
| EV-API-01 | edge | No incident window given in the tracing request | 200, window defaults to one hour ending now, bucketSeconds 60 (D-4); maps to @SC-API-102 | TR-02, TR-06, D-4 | api | mvp1 |
| EV-API-02 | boundary | Incident 2026-09-01T00:00:00Z to 2026-10-01T00:00:01Z (30d 1s) | 400, plain error about the 30-day cap | TR-02, AC-03 | api | mvp1 |
| EV-API-03 | boundary | Incident length 29d 23h 59m | 200, bucketSeconds 21600, at most 120 buckets | TR-02, TR-06 | api | mvp1 |
| EV-API-04 | boundary | Incident length exactly 2h | the span-nominal rung is 60 s; the presented width is W = max(ladderWidth(span), alignedRung(axisStart, axisEnd)) so the aligned count is at most 120 | TR-06 | api | mvp1 |
| EV-API-05 | boundary | Incident length 2h 1s | bucketSeconds 300 (smallest rung value that keeps count <= 120) | TR-06 | api | mvp1 |
| EV-API-06 | boundary | Incident length exactly 5d | bucketSeconds 3600 | TR-06 | api | mvp1 |
| EV-API-07 | boundary | Incident length 5d 1s | bucketSeconds 7200 | TR-06 | api | mvp1 |
| EV-API-08 | boundary | Incident length exactly 10d | bucketSeconds 7200 | TR-06 | api | mvp1 |
| EV-API-09 | boundary | Incident length 10d 1s; maps to @SC-API-125 | bucketSeconds 14400, at most 120 buckets | TR-06 | api | mvp1 |
| EV-API-10 | boundary | Incident length exactly 20d | bucketSeconds 14400 | TR-06 | api | mvp1 |
| EV-API-11 | boundary | Incident length 20d 1s | bucketSeconds 21600, at most 120 buckets | TR-06 | api | mvp1 |
| EV-API-12 | negative | Incident end before start | 400, end-after-start error | TR-02 | api | mvp1 |
| EV-API-13 | negative | Incident zero length | 400, end-after-start error | TR-02 | api | mvp1 |
| EV-API-14 | negative | Incident time with no zone text | 400, zone-required error | TR-02 | api | mvp1 |
| EV-API-15 | edge | Incident start exactly at a minute boundary with an offset of +05:30; maps to @SC-API-121 | stored UTC equals minus 5h30m | TR-02, TR-05 | api | mvp1 |
| EV-API-16 | edge | Incident start with fractional seconds .999; maps to @SC-API-122 | accepted, stored to millisecond, no rounding loss | TR-02 | api | mvp1 |
| EV-API-17 | edge | Incident time in the year 9999; maps to @SC-API-123 | accepted if within a 30-day window | TR-02 | api | mvp1 |
| EV-API-18 | negative | sourceIds empty; maps to @SC-API-100 | 400, a plain error that at least one source is needed | TR-04 | api | mvp1 |
| EV-API-19 | resilience | One audit source of several fails mid-page; maps to @SC-API-124 | audit lane names the failure, other lanes still render | TR-07 | api | mvp1 |
| EV-API-20 | boundary | Exactly 50000 IIS events | truncated false | TR-10, AC-05 | api | mvp1 |
| EV-API-21 | boundary | Exactly 50001 IIS events | truncated true, warning about the raw cap | TR-10, AC-05 | api | mvp1 |
| EV-API-22 | edge | IIS event exactly at window open; maps to @SC-API-140 | included per the half-open window rule | TR-04, TR-02 | api | mvp1 |
| EV-API-23 | edge | IIS event exactly at window close; maps to @SC-API-140 | excluded per the half-open window rule; an event exactly at start is included | TR-04, TR-02 | api | mvp1 |
| EV-API-24 | edge | An IIS request whose logged timestamp is at the window open while its time-taken extends past it | counted once as a point at the logged timestamp; durationMs shown separately, not twice | TR-31 | api | mvp1 |
| EV-API-25 | edge | time-taken of 0 | point at the logged timestamp; durationMs 0 shown separately; start equals end | TR-31 | api | mvp1 |
| EV-API-26 | edge | time-taken of 4294967295 | accepted as durationMs; the event stays a point at the logged timestamp, not an overflow | TR-31 | api | mvp1 |
| EV-API-27 | security | Response marshalled while case holds client-ip and referer | no client-ip byte present in the response body | TR-13 | api | mvp1 |
| EV-API-28 | security | Request with an attempt to inject a payload field; maps to @SC-API-101 | 400 unknown field; no source read; no echo of the value | TR-13 | api | mvp1 |
| EV-API-29 | security | Master key locked | 423 before the case is opened | TR-13, AC-11 | api | mvp1 |
| EV-API-30 | privacy | Lane event for an IIS row with cookie allowlist hit; maps to @SC-API-126 | no cookie attrs, no digest, in the tracing response | TR-13, D-8 | api | mvp1 |
| EV-API-31 | resilience | Audit aggregation command fails mid-run (source unreachable) | the audit lane is incomplete with complete false and a reason in {timeout, rowcap, error}, never shown as complete | TR-04 | api | mvp1 |
| EV-API-32 | edge | IIS import with no time-taken declared | IIS lane shows counts and the no-time-taken gap only | TR-07, TR-31, AC-14 | api | mvp1 |
| EV-API-33 | edge | window just over 24h | response carries the bucket-size label; hourly for a window over a day, 6h at an hour-aligned 30d (8h when a misaligned start coarsens the rung, see EV-UI-220) | TR-16, AC-10 | e2e | mvp1 |
| EV-API-34 | boundary | window of exactly 86400s | the span-nominal rung is 900 s; the presented width is W = max(ladderWidth(span), alignedRung(axisStart, axisEnd)) so the aligned count ceil((axisEnd-axisStart)/W) is never above 120 | TR-06 | api | mvp1 |
| EV-API-35 | boundary | window of exactly 86401s | bucketSeconds 3600 (over-24h rung), at most 120 buckets | TR-06 | api | mvp1 |
| EV-API-37 | regression | Existing /timeline text endpoint still answers after the tracing route is added | POST /timeline unchanged | D-6 | api | mvp1 |
| EV-API-39 | performance | 30-day audit lane with 200k calls | server buckets returned within the request budget, no browser summing | TR-10 | bench | mvp1 |
| EV-API-40 | copy | missing-lane sentence for no-data | fixed sentence, no zero, no invented wording | TR-07 | api | mvp1 |
| EV-API-120 | boundary | Audit store refuses a window longer than 7 days; maps to @SC-API-51 | 200, warnings name the audit window limit; no silent shorter window | TR-07 | api | mvp1 |
| EV-API-121 | negative | Closed case with a valid window; maps to @SC-API-08 | 409, body reason case-closed, no source read | TR-03, AC-08 | api | mvp1 |
| EV-API-140 | edge | Multi-source trace where one imported source lacks a validated time-zone rule; maps to @SC-API-120 | 200, top-level disabled reason tz-rule, no lane row plots any event | TR-11, AC-06 | api | mvp1 |
| EV-API-141 | negative | POST /api/cases/{id}/tracing for an unknown case id | 404 case-not-found (proposed; section 5.1 lists no 404) | TR-13 | api | mvp1 |
| EV-API-160 | negative | sourceIds names a source of another case; maps to @SC-API-160 | 404, plain error naming the source id | TR-13 | api | mvp1 |
| EV-API-161 | edge | sourceIds repeats one id three times; maps to @SC-API-161 | read once; de-duplicated before validation | TR-04 | api | mvp1 |
| EV-API-162 | negative | sourceIds holds 51 distinct ids; maps to @SC-API-161 | 400, plain error about the source cap | TR-04 | api | mvp1 |
| EV-API-163 | negative | request body above the body cap; maps to @SC-API-162 | 400, plain error about the request size | TR-02 | api | mvp1 |
| EV-API-164 | security | unknown JSON field payload; maps to @SC-API-101 | 400, no source read, no echo | TR-13 | api | mvp1 |
| EV-API-165 | negative | invalid window with a reachable audit source; maps to @SC-API-163 | 400 and no connection attempt recorded | TR-03 | api | mvp1 |
| EV-API-166 | boundary | one event exactly at window start and one at window end; maps to @SC-API-165 | start counted, end excluded | TR-02, TR-06 | api | mvp1 |
| EV-API-167 | resilience | a second trace with its own per-tab requestSeq starts while one is in flight; maps to @SC-API-166 | the newer request never cancels a different read; each tab reads independently (per-tab requestSeq canary) | TR-13 | api | mvp1 |
| EV-API-168 | resilience | identical full query posted twice while in flight; maps to @SC-API-167 | two tabs with the same query each perform their own bounded live audit read; there is no server serverWorkId, no subscriber count and no shared in-flight read, and two requests differing only in the reference window never share work | TR-13 | api | mvp1 |
| EV-API-169 | edge | raw read hit the 50000 cap; maps to @SC-API-181 | every lane carries scanned, truncated, complete; truncated says first N rows | TR-10 | api | mvp1 |
| EV-API-170 | boundary | rollup latency sum over two buckets; maps to @SC-API-168 | sum_us integer microseconds, min_us and max_us integers, count_all and count_with_duration separate, overflow refused, no sum_ms | TR-34 | unit | mvp2 |
| EV-API-171 | resilience | trace exceeds the 60 s server timeout (proposed) | request ends partial or timed out, never a hung connection | TR-13 | api | mvp1 |
| EV-API-172 | edge | a stale trace response arrives after a newer request; maps to @SC-API-166 | UI ignores the stale response | TR-13 | e2e | mvp1 |
| EV-API-180 | boundary | window [T,T+60s) with one event exactly at T and one exactly at T+60s; maps to @SC-API-182 | start event is in the first bucket; end event is excluded (half-open boundary canary) | TR-02, TR-06 | unit | mvp1 |
| EV-API-181 | edge | raw read hit the 50000 cap while the uncapped aggregate completed; maps to @SC-API-181 | every lane carries scanned/truncated/complete; counts stay uncapped; complete false only if the aggregate failed | TR-10, TR-13 | api | mvp1 |
| EV-API-182 | negative | MVP-1 request carrying a route filter field; maps to @SC-API-180 | 400 unknown field route; no source read (request-schema 400 canary) | TR-29 | api | mvp1 |
| EV-API-183 | resilience | a newer trace with its own per-tab requestSeq arrives while one is in flight; maps to @SC-API-166 | the newer request never cancels a different read (concurrency cancel canary) | TR-13 | api | mvp1 |
| EV-API-226 | resilience | the single match+group command times out or hits the row cap on a high-cardinality 30-day audit; maps to @SC-API-49 | the audit lane is state incomplete with complete false and a reason in timeout, rowcap, error; an unsupported span is refused with the limit named, never silently shortened (audit aggregation canary) | TR-04, TR-07 | api | mvp1 |
| EV-API-184 | boundary | two per-source reads run at once; maps to @SC-API-167 | per-source concurrency stays bounded separately by MaxAuditConcurrency; identical in-flight requests each perform their own bounded read, with no server serverWorkId and no shared in-flight read | TR-13 | api | mvp1 |
| EV-API-225 | boundary | non-finite, negative and greater-than-maxInt64 durations, then a missing duration; maps to @SC-API-168 | each bad duration keeps the event with no duration and durationStatus invalid, increments count_all and not count_with_duration, and no invalid float is serialized; a missing duration is stored and increments count_all only (invalid-duration canary) | TR-34, AC-20 | unit | mvp2 |
| EV-API-200 | negative | request with an unknown JSON field; maps to @SC-API-200 | 400 naming the field; no source read; no connection opened (request-schema 400 canary) | TR-13 | api | mvp1 |
| EV-API-224 | edge | two IIS sources of the same kind with distinct ids; maps to @SC-API-25 | each lane sources entry is {sourceId, kind, label} with kind required; readKind and window are optional and window only when it differs from the lane window (SourceRef canary) | TR-07, TR-32 | api | mvp1 |
| EV-API-201 | resilience | two tabs with their own bounded reads and one cancels; maps to @SC-API-201 | cancel detaches only its own request and the other read is unaffected; there is no subscriber count and no shared in-flight read (concurrency cancel canary) | TR-13 | api | mvp1 |
| EV-API-223 | boundary | repeat canonical query with and without a bypassLedger field; maps to @SC-API-223 | every lane has origin case and the top-level allLanesStored is true, no source is read, and import and event counts are unchanged; a bypassLedger field is refused as unknown with 400 (allLanesStored canary) | TR-03, TR-13 | api | mvp2 |
| EV-API-202 | boundary | window [T,T+3600s) with events at T and T+3600s; maps to @SC-API-202 | start counted, end excluded (half-open boundary canary) | TR-02, TR-06 | unit | mvp1 |
| EV-API-203 | boundary | toMicros(1.5), toMicros(0), negative, NaN, Inf; maps to @SC-API-203 | 1500 us and 0 are stored; each non-finite, negative or greater-than-maxInt64 duration keeps its event with no duration and durationStatus invalid; count_with_duration is the percentile denominator (sum_us canary) | TR-34 | unit | mvp2 |
| EV-API-204 | edge | raw cap hit while the aggregate completes; maps to @SC-API-204 | per-lane scanned/truncated/complete; a truncated lane is never exact (completeness canary) | TR-10, TR-13 | api | mvp1 |
| EV-API-220 | boundary | 30-day windows starting 00:01, 05:59, 06:00 and 23:59; maps to @SC-API-220 | the axis stays UTC floor-aligned and is never re-anchored; 00:00 yields 6 h and n=120, a 00:01 start coarsens to 8 h with n=91, and every window has zero, one, or two distinct boundary fragments, zero when aligned and one shared scan when both fall in one grain-hour (boundary fragment canary) | TR-06, AC-03 | api | mvp1 |
| EV-API-205 | resilience | offline source pull; maps to @SC-API-205 | 502, one failed import_attempts row outside the tx, no coverage (failed pull attempts canary) | TR-33 | api | mvp2 |
| EV-API-221 | resilience | MVP-1 audit-only trace; maps to @SC-API-221 | at least one remote read, zero remote writes, zero imports; a closed or locked case opens zero connections (live audit read canary) | TR-03, TR-13 | api | mvp1 |
| EV-API-206 | security | repeat a canonical query already in the coverage ledger; maps to @SC-API-206 | every lane has origin case and the top-level allLanesStored is true, no source is read, and import and event counts are unchanged; a bypassLedger field is refused with 400 as unknown | TR-03, TR-13 | api | mvp2 |
| EV-API-222 | resilience | a request-shaping field changes while the window and sources stay the same; maps to @SC-API-224 | there is no server serverWorkId and no shared in-flight read, so the two requests share no buckets, warnings or projections (no-serverWorkId canary) | TR-13 | api | mvp1 |
| EV-API-227 | resilience | the audit aggregation ends in timeout, rowcap or error, and a high-cardinality 30-day audit read; maps to @SC-API-225 | the audit lane is incomplete with complete false and a reason in {timeout, rowcap, error}; an unsupported span is refused with the limit named (broad-scan cap canary) | TR-04, TR-07 | api | mvp1 |
| EV-API-230 | boundary | 0 ms and 1.5 ms durations stored with one non-finite duration in the same batch; maps to @SC-API-228 | 0 and 1.5 ms are stored, the non-finite event is kept with no duration and durationStatus invalid, increments count_all and not count_with_duration, and the batch commits as one transaction (bad-duration atomicity canary) | TR-34 | unit | mvp2 |
| EV-API-228 | boundary | a coverage and tracing read after a stored-coverage repeat, then a pull; maps to @SC-API-226 | only POST /api/cases/{id}/pulls increments coverage; the reads leave coverage unchanged (pull-only canary) | TR-03, TR-33 | api | mvp2 |
| EV-API-229 | edge | a failed import for a second iis-w3c source with a distinct case_sources id; maps to @SC-API-227 | the failed import keeps its provenance, both sources carry distinct sourceId and the same required kind, and readKind and window are optional with window only when it differs (SourceRef provenance canary) | TR-07 | api | mvp1 |
| EV-API-240 | boundary | spy connector on an uncovered window; maps to @SC-API-240 | GET coverage opens zero source connections even for an uncovered window; each lane carries readKind live or saved and asOf; only POST pulls persists events and coverage (three-operations canary) | TR-26, TR-33 | api | mvp2 |
| EV-API-241 | boundary | trace mixing a live audit lane with saved IIS and SQL lanes; maps to @SC-API-241 | every lane carries origin, asOf, complete, truncated and reason; allLanesStored is true iff every lane has origin case (per-lane provenance canary) | TR-07, TR-10 | api | mvp1 |
| EV-API-242 | resilience | high-cardinality 30-day audit read under the 60 s budget; maps to @SC-API-242 | one server-side match+group per source, no raw-row paging, complete false with reason in timeout, rowcap, error, and a not-a-snapshot note (audit aggregation canary) | TR-04, TR-10 | api | mvp1 |
| EV-API-243 | boundary | non-finite, negative and greater-than-maxInt64 durations, then a missing duration; maps to @SC-API-243 | each bad duration keeps the event with no duration and durationStatus invalid, increments count_all only, and no invalid float is serialized; a missing duration stays missing (invalid-duration canary) | TR-34, AC-20 | unit | mvp2 |
| EV-API-244 | resilience | identical full query posted twice at once; maps to @SC-API-244 | two tabs run two bounded reads and neither cancels the other; no serverWorkId exists and the client-only requestSeq ignores stale responses (per-tab requestSeq canary) | TR-13 | api | mvp1 |
| EV-API-245 | boundary | late middle insert, reordered identical rows and an appended doc on one window; maps to @SC-API-245 | canonical row-content digest idempotency: a late middle insert yields changed, byte-identical rows in the source’s observed order yield unchanged, an appended doc yields changed, and a changed pull replaces the observation atomically with no double count; coverage means read as of asOf (content digest canary) | TR-26, TR-33 | api | mvp2 |
| EV-API-260 | boundary | 30-day windows at 00:00, 00:01, 05:59 and 23:59; maps to @SC-API-260 | width is W = max(ladderWidth(span), alignedRung(axisStart, axisEnd)) (W = span/120 = 21,600,000 ms on an exact hour), the axis stays UTC floor-aligned and is never re-anchored, a misaligned start coarsens to 8 h with n=91, and every window has zero, one, or two distinct boundary fragments, zero when aligned and one shared scan when both fall in one grain-hour (boundary fragment canary) | TR-06, AC-03 | api | mvp1 |
| EV-API-261 | boundary | trace mixing a live audit lane with a saved SQL lane on one window; maps to @SC-API-261 | every lane carries origin, asOf, complete, truncated and reason, the audit lane is readKind live and the SQL lane readKind saved, allLanesStored is true iff every lane has origin case, and only POST pulls persists (three-operations and per-lane provenance canary) | TR-03, TR-04, TR-07, TR-33 | api | mvp2 |
| EV-API-305 | boundary | a trace mixing a live audit lane with a stored SQL lane; maps to @SC-API-305 | complete never implies a snapshot, the live lane is consistency live-non-snapshot and the saved lane stored-as-of, and mixedConsistency is true with the non-dismissible note (consistency canary) | TR-04, TR-07, TR-10 | api | mvp1 |
| EV-API-262 | boundary | non-finite, negative and greater-than-maxInt64 durations in one batch; maps to @SC-API-243 | each bad duration keeps the event with no duration and durationStatus invalid, increments count_all only, and no invalid float is serialized (invalid-duration-kept canary) | TR-34, AC-20 | unit | mvp2 |
| EV-API-263 | resilience | identical full query posted twice at once; maps to @SC-API-244 | two tabs each run their own bounded read, neither cancels the other, no serverWorkId exists, and stale responses are ignored by the client-only per-tab requestSeq (no-serverWorkId and requestSeq canary) | TR-13 | api | mvp1 |
| EV-API-264 | boundary | late middle insert and reordered identical rows on one window; maps to @SC-API-245 | a late middle insert yields changed and byte-identical rows in the source’s observed order yield unchanged, keyed by the canonical row-content digest of the ordered observed rows (observed-digest idempotency canary) | TR-26, TR-33 | api | mvp2 |
| EV-API-265 | resilience | high-cardinality 30-day audit read served by one server-side $match+$group per source; maps to @SC-API-242 | the single aggregation runs under the 60 s CensusQueryBudget and the broad-scan gate, never pages raw rows to build counts, complete false carries a reason in timeout, rowcap, error, and a note states not a snapshot (server-side group under budget canary) | TR-04, TR-10 | api | mvp1 |
| EV-API-304 | boundary | aligned window, single edge, both edges in one grain-hour, and two distinct boundaries; maps to @SC-API-304 | zero, one, or two boundary raw scans with the edge budget per fragment (boundary fragment canary) | TR-06, TR-34 | unit | mvp2 |
| EV-API-280 | boundary | a window whose sub-intervals are missing from the coverage ledger; maps to @SC-API-280 | GET coverage opens no source connection, reports the gaps and never fills them; only POST pulls reads remote SQL/Mongo and persists events and coverage; each lane carries readKind live or saved with separate complete and asOf (three-operations canary) | TR-26, TR-33 | api | mvp2 |
| EV-API-303 | boundary | every span 10 s to 30 d at offsets 00:00, 00:01, 05:59 and 23:59; maps to @SC-API-303 | W = max(ladderWidth(span), alignedRung(axisStart, axisEnd)) and ceil((axisEnd-axisStart)/W) is at most 120 for every span and offset (aligned axis canary) | TR-06, AC-03 | unit | mvp1 |
| EV-API-281 | boundary | a trace mixing a live audit lane with saved SQL and IIS lanes; maps to @SC-API-281 | every lane carries origin, asOf, complete, truncated and reason; allLanesStored is true iff every lane has origin case (per-lane provenance canary) | TR-07, TR-10 | api | mvp1 |
| EV-API-302 | boundary | a changed pull over an exact stored window, a pull failing before commit, a partial-overlap pull, and two concurrent pulls; maps to @SC-API-302 | atomic replacement with no double count, a failed pull leaves the prior observation intact, partial overlap is 409 window-overlap, and concurrent pulls serialize to one winner (observation replacement canary) | TR-26, TR-33 | api | mvp2 |
| EV-API-282 | boundary | late middle insert and byte-identical rows on one window; maps to @SC-API-282 | a late middle insert yields changed and byte-identical rows yield unchanged, keyed by the canonical row-content digest of the observed rows; coverage means read as of asOf (observed-digest idempotency canary) | TR-26, TR-33 | api | mvp2 |
| EV-API-283 | resilience | high-cardinality 30-day audit read under the 60 s budget; maps to @SC-API-283 | one server-side match+group per source, no raw-row paging, complete false with reason in timeout, rowcap, error, and a not-a-snapshot note (server-side group under budget canary) | TR-04, TR-10 | api | mvp1 |
| EV-API-301 | boundary | absent key vs explicit null, a duplicate _id in one pull, and an invalid duration with identical observed rows; maps to @SC-API-301 | absent differs from null, the duplicate makes the pull incomplete with no dedup, and the invalid-duration re-pull is unchanged (content digest canary) | TR-26, TR-33 | unit | mvp2 |
| EV-API-284 | boundary | non-finite, negative and greater-than-maxInt64 durations, then a missing duration; maps to @SC-API-284 | each bad duration keeps the event with no duration and durationStatus invalid, increments count_all only, and no invalid float is serialized (invalid-duration-kept canary) | TR-34, AC-20 | unit | mvp2 |
| EV-API-300 | boundary | same _id and timestamp with a changed durationMs, status, value, severity or attrs; maps to @SC-API-300 | changed, because the canonical row-content digest covers every observed evidence-affecting field and distinguishes absent from present-null (content digest canary) | TR-26, TR-33 | unit | mvp2 |
| EV-API-285 | resilience | identical full query posted twice at once; maps to @SC-API-285 | two tabs each run their own bounded read, neither cancels the other, no serverWorkId exists, and stale responses are ignored by the client-only per-tab requestSeq (no-serverWorkId and requestSeq canary) | TR-13 | api | mvp1 |
| EV-API-286 | boundary | 30-day windows at 00:00, 00:01, 05:59 and 23:59; maps to @SC-API-286 | the axis stays UTC floor-aligned and is never re-anchored, a 00:01 start coarsens to 8 h with n=91, and every window has zero, one, or two distinct boundary fragments, zero when aligned and one shared scan when both fall in one grain-hour (boundary fragment canary) | TR-06, AC-03 | api | mvp1 |

#### Open points
- The exact JSON field name for the status class on a lane event is not pinned by the spec; `StatusClass string` is named in WP-13 and is used here as `statusClass` (proposed wire name).
- The exact 400 body text for the 30-day cap, zone-less time and long route are not given; all are worded as plain-language errors.
- Whether the audit lane can serve more than 7 days is unverified (`internal/audit/query.go:76-77` cited by the spec, not re-opened); EV-API-51 assumes the tracer must state the limit rather than shorten silently.
- The spec calls the lane key `api-audit` in the response example but `audit` elsewhere; this fragment uses `audit` (proposed) and treats `api-audit` as a label.
- Whether tracing exposes raw audit call rows at all is unverified; AC-12 implies not, so @SC-API-49 asserts no raw audit list (its scenario Twhen holds no raw call list).
- The 2–24h rung is deterministic after ruling 1: the smallest of 300, 600, 900 s whose aligned count is <= 120 (up to 10 h = 300 s, up to 20 h = 600 s, up to 24 h = 900 s); the presented width is W = max(ladderWidth(span), alignedRung(axisStart, axisEnd)) and the aligned count is always at most 120. EV-API-05 asserts 300.
- `time-taken` start arithmetic relies on the unverified `w3cTimeIsEnd` (`internal/importer/iis/w3c.go:14-21`); EV-API-43 states the assumed reader and must be revisited after VF-01.
- The exact status class for a win32-only row (no sc-status) is undefined; EV-API-47 asserts the absence of a class (proposed).

### Area B: importer, privacy, export scrub (SC-PRV, EV-PRV)

#### F-PRV-01 IIS import keeps client IP and referer  (TR-19, WP-01, WP-02, WP-03)

```gherkin
Feature: IIS import keeps client IP and referer

  Background:
    Given a case whose IIS import policy is set to keep client IP and referer
    And the case has a W3C log declaring the c-ip and cs(Referer) columns

  @SC-PRV-01 @AC-16 @TR-19 @mvp1 @api
  Scenario: A W3C request row stores a validated client IP
    Given a W3C row whose c-ip cell is 192.0.2.77
    When the file is imported into the case
    Then the stored request event has attrs["client-ip"] equal to 192.0.2.77

  @SC-PRV-02 @AC-16 @TR-19 @mvp1 @api
  Scenario: The referer keeps scheme, host and path but loses query and fragment
    Given a W3C row whose cs(Referer) cell is http://ref.example.test/a/b?token=s3cr3t#frag
    When the file is imported into the case
    Then the stored request event has attrs["referer"] equal to http://ref.example.test/a/b
    And the stored referer value contains no "token=s3cr3t" and no "#frag"

  @SC-PRV-03 @AC-16 @TR-19 @mvp1 @api
  Scenario: Server IP, query string and user agent stay dropped
    Given a W3C row declaring s-ip, cs-uri-query and cs(User-Agent)
    When the file is imported into the case
    Then the stored event has no attribute whose name is server-ip, query or user-agent

  @SC-PRV-04 @TR-19 @mvp1 @api
  Scenario Outline: A malformed or absent client IP is never stored
    Given a W3C row whose c-ip cell is "<cell>"
    When the file is imported into the case
    Then the stored request event has no attrs["client-ip"]

    Examples:
      | cell                        |
      | 192.0.2.77:51000            |
      | not-an-ip                   |
      | 2001:db8::1 zone%eth0       |
      | 192.0.2.77/24               |
      | -                           |

  @SC-PRV-05 @TR-19 @mvp1 @api
  Scenario Outline: An unusable referer value is dropped
    Given a W3C row whose cs(Referer) cell is "<cell>"
    When the file is imported into the case
    Then the stored request event has no attrs["referer"]

    Examples:
      | cell                                     |
      | javascript:alert(1)                      |
      | ftp://ref.example.test/x                 |
      | /relative/path                           |
      | not a url                                |
      | -                                        |

  @SC-PRV-06 @AC-16 @TR-19 @mvp1 @api
  Scenario: A c-ip of 64 bytes and a referer of 512 bytes are accepted whole
    Given a W3C row whose c-ip cell is 64 bytes and whose cs(Referer) cell is 512 bytes
    When the file is imported into the case
    Then the stored request event has attrs["client-ip"] and attrs["referer"]

  @SC-PRV-106 @AC-16 @TR-20 @mvp1 @api
  Scenario: A referer of 513 bytes is dropped
    Given a W3C row whose cs(Referer) cell is 513 bytes
    When the file is imported into the case
    Then the stored request event has no attrs["referer"]
```

#### F-PRV-02 Cookie allowlist and HMAC digest only  (TR-20, WP-01, WP-02, WP-03)

```gherkin
Feature: Only allowlisted cookie names are stored, and only as a domain-prefixed HMAC digest

  Background:
    Given a case whose cookie allowlist is the single name "sid"
    And the case holds one 32-byte HMAC key in meta key cookie.hmac_key_v1, generated once from a cryptographic RNG and stored only inside the encrypted case.db (ruling 3)

  @SC-PRV-07 @AC-16 @TR-20 @mvp1 @api
  Scenario: An allowlisted cookie is stored as a domain-prefixed 32-hex-char HMAC digest
    Given a W3C row whose cs(Cookie) cell is sid=secret
    When the file is imported into the case
    Then the stored event has attrs["cookie:sid"]
    And its value is the lowercase hex of HMAC-SHA256(key, "unravis-cookie-v1\x00" || lowercase(name) || "\x00" || rawValue) truncated to 32 hex characters (ruling 3)
    And the digest is pseudonymous, not anonymous (ruling 9)

  @SC-PRV-08 @AC-16 @TR-20 @mvp1 @api
  Scenario: A cookie not on the allowlist is never read
    Given a W3C row whose cs(Cookie) cell is sid=secret; theme=dark
    When the file is imported into the case
    Then the stored event has no attribute whose name is cookie:theme

  @SC-PRV-09 @AC-16 @TR-20 @mvp1 @api
  Scenario: The raw cookie value never reaches a marshalled event
    Given a W3C row whose cs(Cookie) cell is sid=secret
    When the file is imported and the resulting events are marshalled to JSON
    Then no byte of the JSON equals the raw value secret

  @SC-PRV-10 @TR-20 @mvp1 @api
  Scenario: The allowlist match is exact and lower-cased
    Given a W3C row whose cs(Cookie) cell is SID=secret; sidess=other
    When the file is imported into the case
    Then the stored event has attrs["cookie:sid"] and no attrs["cookie:sidess"]

  @SC-PRV-11 @TR-20 @mvp1 @api
  Scenario Outline: A cookie pair that carries no name or value is skipped safely
    Given a W3C row whose cs(Cookie) cell is "<cell>"
    When the file is imported into the case
    Then the import finishes without a crash
    And no cookie attribute holds the raw text

    Examples:
      | cell              |
      | sid=              |
      | =secret           |
      | sid               |
      | ;;;               |
      | sid=secret;;theme |

  @SC-PRV-12 @TR-20 @mvp1 @api
  Scenario: A cookie value with non-ASCII bytes is still hashed, never kept raw
    Given a W3C row whose cs(Cookie) cell is sid=café-☕
    When the file is imported into the case
    Then the stored value is the 32-character hex digest
    And no byte of the stored event holds "café-☕"

  @SC-PRV-13 @TR-20 @mvp1 @api
  Scenario: A huge cookie cell is capped and does not abort the import
    Given a W3C row whose cs(Cookie) cell is a single sid pair of 600 bytes
    When the file is imported into the case
    Then the import finishes and the raw value is never stored

  @SC-PRV-14 @TR-20 @mvp1 @api
  Scenario: More than 20 cookie pairs are ignored past the cap
    Given a W3C row whose cs(Cookie) cell holds 25 sid-looking pairs
    When the file is imported into the case
    Then at most 20 cookie attributes are stored

  @SC-PRV-15 @TR-20 @mvp1 @api
  Scenario: A case with an empty allowlist stores no cookie and says so
    Given a case whose cookie allowlist is empty
    And a W3C row whose cs(Cookie) cell is sid=secret
    When the file is imported into the case
    Then the stored event has no attribute whose name starts with cookie:
    And the case settings surface says cookie hashing is off until an allowlist is set (proposed, ruling 9)

  @SC-PRV-16 @TR-20 @later @api
  Scenario: Replacing the cookie HMAC key would change every later digest
    Given a case that imported sid=secret under key A
    When the key is replaced with key B and the same row is imported again
    Then the second event's attrs["cookie:sid"] differs from the first
    And no key rotation exists in MVP-1 (ruling 9; this scenario is LATER)

  @SC-PRV-17 @TR-20 @mvp1 @api
  Scenario: Changing the allowlist after an import does not rewrite old events
    Given a case that imported sid=secret while "sid" was allowlisted
    When the allowlist is later changed to "theme" only
    Then the previously stored attrs["cookie:sid"] is unchanged

  @SC-PRV-18 @TR-20 @mvp1 @api
  Scenario: A short or missing HMAC key yields no digest
    Given a cookie policy whose HMAC key is shorter than 32 bytes
    When a row with sid=secret is imported
    Then the stored event has no attrs["cookie:sid"]

  @SC-PRV-101 @TR-20 @mvp1 @api
  Scenario: An allowlist with no stored cookie HMAC key stores no cookie
    Given a case whose cookie allowlist is sid and whose cookie HMAC key row is absent
    And a W3C row whose cs(Cookie) cell is sid=secret
    When the file is imported into the case
    Then the stored event has no attrs["cookie:sid"]
    And the import finishes without an error
```

#### F-PRV-03 Events read API drops sensitive attrs by default  (TR-15, WP-07)

```gherkin
Feature: The events read API hides imported IP, referer and cookie digests by default

  Background:
    Given a case that has imported a W3C event with attrs["client-ip"], attrs["referer"] and attrs["cookie:sid"]

  @SC-PRV-280 @TR-15 @AC-23 @mvp1 @api
  Scenario: One privacy projection drops the canary attrs on every default surface
    Given a case whose events carry CANARY_ values in client-ip, referer and cookie:sid
    When the events API, export bundle, report and tooltip/detail are built on their default paths
    Then one pure function ProjectEventAttrs(attrs map[string]string, includeSensitive bool) map[string]string is applied to every outward-facing event view (events API, export bundle, report, tooltip/detail) (ruling 10)
    And includeSensitive=false is the default and drops client-ip, referer and every cookie:* key, and zero CANARY_ hits appear in any default-path artifact and in any error or log buffer

  @SC-PRV-19 @AC-23 @TR-15 @mvp1 @api
  Scenario: A plain events read drops client-ip, referer and every cookie key
    When GET /api/cases/{id}/events is called with no extra query
    Then one pure function ProjectEventAttrs(attrs map[string]string, includeSensitive bool) map[string]string is applied to every outward-facing event view (events API, export bundle, report, tooltip/detail) (ruling 10)
    And includeSensitive=false is the default and drops client-ip, referer and every cookie:* key
    And the other attrs such as "status" and "method" are still present

  @SC-PRV-20 @AC-23 @TR-15 @mvp1 @api
  Scenario: Opting in returns the sensitive attrs through the one projection
    Given the caller holds the valid case key
    When GET /api/cases/{id}/events?includeSensitive=1 is called
    Then ProjectEventAttrs is called with includeSensitive true and the JSON event includes "client-ip", "referer" and the cookie digest (ruling 10)
    And the cookie value is still the digest, never a raw cookie value
    And opt-in is explicit per read/export and no forced-scrub flag or case policy affects the result (ruling 9)

  @SC-PRV-22 @TR-15 @mvp1 @api
  Scenario Outline: Any other opt-in value stays scrubbed
    When GET /api/cases/{id}/events?includeSensitive=<value> is called
    Then the JSON event has no "client-ip", no "referer" and no "cookie:*" key

    Examples:
      | value |
      | 0     |
      | true  |
      | yes   |
      | 2     |
      | (empty) |

  @SC-PRV-102 @TR-15 @mvp1 @api
  Scenario: An opt-in value with a trailing space stays scrubbed
    Given a case that has imported a W3C event with attrs["client-ip"], attrs["referer"] and attrs["cookie:sid"]
    When GET /api/cases/{id}/events?includeSensitive=TRUE%20 (the value TRUE with a trailing space) is called
    Then the JSON event has no "client-ip", no "referer" and no "cookie:*" key

  @SC-PRV-240 @TR-15 @mvp1 @api
  Scenario: One privacy projection drops the three canary attrs on every default surface
    Given a case whose events carry CANARY_ values in client-ip, referer and cookie:sid
    When the events API, export bundle, report and tooltip/detail are built on their default paths
    Then ProjectEventAttrs(attrs, false) is applied to every outward-facing event view and drops client-ip, referer and every cookie:* key (ruling 10)
    And zero CANARY_ hits appear in any default-path artifact and in any error or log buffer
    And includeSensitive true returns the three attrs only on the events API and does not change export scrub

  @SC-PRV-241 @TR-21 @mvp1 @api
  Scenario: Confirm matches the snapshot and scrub policy, not bytes
    Given a preview built with scrubbing on whose previewId matched the current caseRevision, scrubSensitive and projectionVersion
    When POST /api/export/confirm is called with its previewId
    Then confirm exports the same case revision and the same scrub policy class as the preview; it does not promise byte-identical output, because each export uses a fresh pseudonym key and records its own creation time (ruling 4)
    And the content carries no client IP, referer or cookie digest
```

#### F-PRV-04 Export preview and confirm scrub by default  (TR-21, D-9, WP-06, WP-07)

```gherkin
Feature: Every export previews and confirms with scrubbing on by default

  Background:
    Given a case whose events carry attrs["client-ip"], attrs["referer"] and attrs["cookie:sid"]

  @SC-PRV-23 @AC-17 @TR-21 @mvp1 @api
  Scenario: A preview with no scrub flag reports scrubbing as on and returns a previewId
    When POST /api/export/preview is called for the case with no scrubSensitive field
    Then the preview response has scrubSensitive true and summarizesBytes false (proposed after WP-06)
    And the previewId is hex(SHA-256("unravis-preview-v1" || caseId || caseRevision || scrubSensitive || projectionVersion)), an advisory revision stamp, not an issuance token (ruling 1)
    And caseRevision is a monotonic integer in meta key case.revision, incremented inside the same transaction as every export-affecting mutation (ruling 4)
    And confirm exports the same case revision and the same scrub policy class as the preview; it does not promise byte-identical output, because each export uses a fresh pseudonym key and records its own creation time (ruling 4)
    And there is no stored one-shot, expiry or unknown-preview 404 state
    And the "Left out by default" statement names IP addresses, referer and cookie hashes (proposed)

  @SC-PRV-24 @AC-17 @TR-21 @mvp1 @api
  Scenario: The default preview shows no IP, referer or cookie digest
    When POST /api/export/preview is called for the case with no scrubSensitive field
    Then the previewed file contains no client-ip, no referer and no cookie: keys
    And the previewed bytes contain no IPv4 address and no IPv6 address

  @SC-PRV-25 @AC-17 @TR-21 @mvp1 @manual
  Scenario: The export preview says what the export hides
    When the preview is shown for a case with sensitive attrs
    Then a statement says the export hides IP, referer and cookie hash
    And no statement says stored data is masked (ruling 9)

  @SC-PRV-26 @AC-17 @TR-21 @mvp1 @api
  Scenario: Turning the scrub off keeps the three attrs and says so
    Given POST /api/export/preview is called with scrubSensitive set to false
    Then the previewed file includes client-ip, referer and the cookie digest
    And a masking statement says scrubbing was turned off
    And a statement says cookie values are digests, never raw values

  @SC-PRV-27 @AC-17 @TR-21 @mvp1 @api
  Scenario: Confirm re-derives the export and returns its bytes and sha256
    Given a preview built with scrubbing on whose previewId matched the current caseRevision, scrubSensitive and projectionVersion
    When POST /api/export/confirm is called with its previewId
    Then confirm recomputes the revision, takes a read-consistent snapshot of the export inputs, refuses 409 the preview changed; run the preview again if the revision differs, and produces the bytes and their sha256 from that same snapshot (ruling 4)
    And confirm exports the same case revision and the same scrub policy class as the preview; it does not promise byte-identical output, because each export uses a fresh pseudonym key and records its own creation time (ruling 4)
    And the content carries no client IP, referer or cookie digest

  @SC-PRV-281 @TR-21 @mvp1 @api
  Scenario: Confirm matches the snapshot and scrub policy class, not bytes
    Given a preview built with scrubbing on whose previewId matched the current caseRevision, scrubSensitive and projectionVersion
    When POST /api/export/confirm is called with its previewId
    Then confirm exports the same case revision and the same scrub policy class as the preview; it does not promise byte-identical output, because each export uses a fresh pseudonym key and records its own creation time (ruling 4)
    And confirm recomputes the revision, takes a read-consistent snapshot of the export inputs, and refuses 409 the preview changed; run the preview again if the revision differs

  @SC-PRV-28 @AC-17 @TR-21 @mvp1 @api
  Scenario: Scrub-off confirm never carries a raw cookie value
    Given a preview built with scrubSensitive false for an event whose raw cookie was sid=secret
    When the export is confirmed
    Then the content holds the cookie name and its digest only
    And the content contains no "sid=secret"

  @SC-PRV-100 @TR-21 @mvp1 @api
  Scenario: Export preview and confirm name the case in the request body
    Given a case with sensitive attrs in its events
    When POST /api/export/preview is called with caseId set and scrubSensitive true
    Then the preview response belongs to that case and has scrubSensitive true
    And POST /api/export/confirm with the same caseId and the previewId returns the scrubbed bytes

  @SC-PRV-103 @TR-21 @mvp1 @unit
  Scenario: The scrubSensitive preview field does not exist before WP-06 lands (proposed)
    Given the current exportPreviewResponse struct has no scrubSensitive field
    When the export preview handler marshals its response
    Then the JSON holds no scrubSensitive key
    And the field is added only by WP-06 (proposed)

  @SC-PRV-220 @TR-21 @mvp1 @api
  Scenario: A mutation during confirm is refused with no bytes
    Given a preview built at case.revision n
    When an export-affecting mutation commits before POST /api/export/confirm
    Then the response status is 409 with the body the preview changed; run the preview again
    And confirm recomputes the revision and takes a read-consistent snapshot before producing any bytes (ruling 4)

  @SC-PRV-104 @TR-21 @mvp1 @api
  Scenario: Confirming after the preview options changed is refused with 409
    Given a preview built with scrubSensitive true
    When POST /api/export/confirm is called with a changed scrubSensitive false and the old previewId
    Then the response status is 409
    And the body is a plain-language error that says a new preview is required
    And no file is produced (ruling 9)
    And confirm exports the same case revision and the same scrub policy class as the preview; it does not promise byte-identical output, because each export uses a fresh pseudonym key and records its own creation time (ruling 4)
```

#### F-PRV-05 Scrub indicator in the toolbar  (TR-23, WP-10, WP-11)

```gherkin
Feature: The trace toolbar shows the export scrub state

  @SC-PRV-29 @TR-23 @mvp1 @e2e
  Scenario: The scrub pill states the export state in plain words before a preview
    Given no preview has been built yet
    When the trace toolbar is rendered
    Then the scrub pill reads "Default: export hides IP, referer and cookie hash"
    And with scrub on it reads "Export hides IP, referer and cookie hash"
    And the pill is a real button with an accessible name (ruling 12)

  @SC-PRV-30 @TR-23 @mvp1 @e2e
  Scenario: The scrub pill states the include state when scrub is off
    Given the export preview answered scrubSensitive false
    When the trace toolbar is rendered
    Then the scrub pill reads "Export includes IP, referer and cookie hash (scrub is off)"
    And it never says stored data is masked (ruling 12)
```

#### F-PRV-06 Re-import, migration and schema safety  (WP-01, WP-27)

```gherkin
Feature: Existing cases keep working and no new columns are added in MVP-1

  Background:
    Given the case schema version stays at 4 for MVP-1
    And cookie allowlist and cookie.hmac_key_v1 live only in the meta key-value table

  @SC-PRV-31 @TR-20 @mvp1 @api
  Scenario: A case made before the privacy change still opens and reads events
    Given an existing case.db that holds W3C events without any sensitive attrs
    When the case is opened and its events are read
    Then the read succeeds and the old events are unchanged

  @SC-PRV-32 @TR-19 @TR-20 @mvp1 @api
  Scenario: A re-import of the same file is still refused as a duplicate
    Given a W3C file already imported into the case
    When the same bytes are imported again with a cookie policy set
    Then the import is refused as a duplicate
    And no second copy of the events is written

  @SC-PRV-33 @TR-20 @mvp1 @api
  Scenario: A case with no cookie meta rows imports with an empty policy
    Given a case that has no cookie.allowlist and no cookie.hmac_key_v1 rows in meta
    When a W3C file is imported
    Then the import succeeds and no cookie attribute is stored

  @SC-PRV-34 @TR-20 @mvp1 @api
  Scenario: The cookie HMAC key and allowlist never leave the case database
    Given a case with a stored cookie.hmac_key_v1 and allowlist
    When the case is exported and its import manifest is read
    Then the key bytes and the allowlist names are absent from the manifest and from every log line

  @SC-PRV-35 @TR-20 @mvp1 @unit
  Scenario: The privacy change adds no column to the events table
    When the storage schema is inspected
    Then events still has the same columns as before the change
    And the sensitive facts travel only inside attrs_json
```

#### F-PRV-07 The privacy regression test stays green  (AC-17, WP-07)

```gherkin
Feature: The existing no-IPs-or-user-IDs export test keeps passing

  @SC-PRV-36 @AC-17 @TR-21 @mvp1 @unit
  Scenario: TestBuildCaseNoIPsOrUserIDs still passes with scrub on by default
    Given the case export test fixture plants IPs and user ids in attributes
    When the full go test suite for the evidence package runs
    Then TestBuildCaseNoIPsOrUserIDs passes
    And no IPv4 or IPv6 address and no raw user id reaches the bundle

  @SC-PRV-37 @AC-17 @TR-21 @mvp1 @unit
  Scenario: A non-allowlisted "cookie" attr in the fixture stays left out
    Given the case export fixture has an event with attrs["cookie"] not on any allowlist
    When the case is exported
    Then that attribute is absent from the export
    And the fixture name "cookie" is not rewritten to "cookie:*"
```

#### F-PRV-08 Log-line edge cases  (TR-19,20)

```gherkin
Feature: Odd W3C lines do not leak or crash the importer

  @SC-PRV-38 @TR-19 @mvp1 @api
  Scenario: A malformed W3C line is rejected and counted, never stored
    Given a W3C file with one row whose column count does not match #Fields
    When the file is imported
    Then the row is rejected with the field-count reason
    And no event is emitted for it

  @SC-PRV-39 @TR-19 @mvp1 @api
  Scenario: A line missing #Fields is refused as unsupported
    Given a W3C file with data rows but no #Fields directive
    When the file is imported
    Then the import is refused as an unsupported format

  @SC-PRV-40 @TR-19 @mvp1 @api
  Scenario: A row with no client IP but a good referer keeps only the referer
    Given a W3C row whose c-ip cell is "-" and cs(Referer) is http://ref.example.test/x
    When the file is imported
    Then the stored event has attrs["referer"] but no attrs["client-ip"]

  @SC-PRV-41 @TR-19 @mvp1 @api
  Scenario: An IPv6 client address is stored unchanged
    Given a W3C row whose c-ip cell is 2001:db8::1
    When the file is imported
    Then the stored event has attrs["client-ip"] equal to 2001:db8::1

  @SC-PRV-42 @TR-19 @mvp1 @api
  Scenario: A duplicate c-ip column is refused
    Given a W3C #Fields header naming c-ip twice
    When the file is imported
    Then the import is refused with the field-count reason

  @SC-PRV-43 @TR-19 @mvp1 @api
  Scenario: The HTTPERR client IP keeps the same attr name when the column is present
    Given an HTTPERR sample whose #Fields includes c-ip
    When the file is imported
    Then an HTTPERR event may carry attrs["client-ip"]
    And if c-ip is absent from #Fields no client IP attr is written and a gap note is recorded

  @SC-PRV-105 @TR-19 @mvp1 @api
  Scenario: An HTTPERR file without a c-ip column records a gap only
    Given an HTTPERR sample whose #Fields does not include c-ip
    When the file is imported
    Then no event carries attrs["client-ip"]
    And the import gaps name the missing client IP column

  @SC-PRV-160 @TR-21 @TR-15 @mvp1 @api
  Scenario: The case key is the only authorization and there is no role system
    Given a request that names any role
    When the case key is valid
    Then the request is authorized by the case key alone
    And no role endpoint or role field exists (ruling 9; ruling 15 rejects a role system)

  @SC-PRV-161 @TR-21 @mvp1 @api
  Scenario: A confirm whose previewId does not match the preview is refused
    Given a preview built with scrubSensitive true
    When POST /api/export/confirm is called with a previewId from a different preview
    Then the response status is 409
    And a new preview is required (ruling 9)

  @SC-PRV-162 @AC-17 @TR-21 @mvp1 @api
  Scenario: The preview surface carries no IP, referer or cookie hash
    Given a case whose events carry attrs["client-ip"], attrs["referer"] and attrs["cookie:sid"]
    When POST /api/export/preview is called with default scrubbing
    Then the previewed file holds no IP literal, no referer and no cookie: key

  @SC-PRV-163 @AC-17 @TR-21 @mvp1 @api
  Scenario: The confirm surface carries no IP, referer or cookie hash
    Given a case whose events carry attrs["client-ip"], attrs["referer"] and attrs["cookie:sid"]
    When POST /api/export/confirm is called with the previewId and default scrubbing
    Then the returned bytes hold no IP literal, no referer and no cookie: key

  @SC-PRV-164 @AC-17 @TR-21 @mvp1 @api
  Scenario: The bundle surface carries no IP, referer or cookie hash
    Given a case whose events carry attrs["client-ip"], attrs["referer"] and attrs["cookie:sid"]
    When the case bundle is built with default scrubbing
    Then the bundle holds no IP literal, no referer and no cookie: key

  @SC-PRV-165 @AC-17 @TR-21 @mvp1 @api
  Scenario: The report surface carries no IP, referer or cookie hash
    Given a case whose events carry attrs["client-ip"], attrs["referer"] and attrs["cookie:sid"]
    When the case report is built with default scrubbing
    Then the report holds no IP literal, no referer and no cookie: key

  @SC-PRV-166 @TR-33 @mvp2 @api
  Scenario: A failed pull is shown as the last attempt failure, never as coverage
    Given a live pull whose import transaction rolled back
    And an import_attempts row with outcome failed written outside the transaction (proposed)
    When GET /api/cases/{id}/coverage is called
    Then the response shows last attempt failed from that row
    And the failed attempt contributes no coverage interval (ruling 8)

  @SC-PRV-167 @TR-33 @mvp2 @api
  Scenario: Coverage intervals are half-open and an empty ledger is not an unknown case
    Given a coverage interval from start to end
    When an event sits exactly at the interval end
    Then it belongs to the next interval, not this one
    And an empty ledger returns [] with a state, never a 404 unknown case (ruling 8)

  @SC-PRV-168 @TR-20 @mvp1 @api
  Scenario: Cookie hashing is off until the case settings allowlist is set
    Given a case whose settings allowlist has not been set
    When the case settings surface is opened
    Then it says cookie hashing is off until an allowlist is set (proposed, ruling 9)
    And no cookie attribute is stored on import

  @SC-PRV-222 @TR-33 @mvp2 @api
  Scenario: Deleting a source cascades coverage but keeps its attempt tombstone
    Given a case with a source that has coverage rows and an import_attempts row
    When the source is deleted
    Then the coverage rows cascade and the attempt survives with its source_label
    And rows older than MaxAttemptRetention of 30 days are pruned when the case is opened (ruling 7)

  @SC-PRV-180 @TR-21 @mvp1 @api
  Scenario: A differing or missing previewId is the only stale error and is answered 409
    Given a case whose current caseRevision, scrubSensitive or projectionVersion differs from the previewId sent
    When POST /api/export/confirm is called with it
    Then the response status is 409 with a plain "the preview changed; run the preview again" body
    And confirm recomputes caseRevision, scrubSensitive and projectionVersion and refuses before producing any bytes
    And no stored unknown-preview or expiry 404 path exists (ruling 1)

  @SC-PRV-181 @TR-21 @mvp1 @api
  Scenario: Two previews at one revision collide but confirm still re-derives
    Given two previews for the same caseId, caseRevision, scrubSensitive and projectionVersion
    When both are built
    Then they carry the same deterministic advisory previewId
    And confirm exports the same case revision and the same scrub policy class as the preview; it does not promise byte-identical output, because each export uses a fresh pseudonym key and records its own creation time (ruling 4)

  @SC-PRV-182 @TR-21 @mvp1 @api
  Scenario: Two confirms on one previewId each re-derive the snapshot and scrub class
    Given a preview whose previewId is hex(SHA-256("unravis-preview-v1" || caseId || caseRevision || scrubSensitive || projectionVersion))
    When two POST /api/export/confirm calls race with that previewId
    Then confirm exports the same case revision and the same scrub policy class as the preview; it does not promise byte-identical output, because each export uses a fresh pseudonym key and records its own creation time (ruling 4)
    And there is no stored one-shot that would refuse the second call (ruling 1)

  @SC-PRV-183 @TR-33 @mvp2 @api
  Scenario: A failed pull attempt never adds a coverage interval
    Given a live pull whose import transaction rolled back
    And an import_attempts row with outcome failed written outside the transaction
    When GET /api/cases/{id}/coverage is called
    Then the response shows the last attempt failed from that row
    And the failed attempt contributes no coverage interval (ruling 8)

  @SC-PRV-184 @TR-33 @mvp2 @api
  Scenario: A stored-coverage result returns allLanesStored true
    Given a canonical query whose window is already recorded in the coverage ledger
    When the same canonical query is requested again
    Then the tracing result assembled from already-stored coverage and rollups has every lane origin:"case" and the top-level allLanesStored true (ruling 8)
    And allLanesStored exists only on POST .../tracing responses and means every lane has origin:"case"; GET .../coverage returns {entries:[...], asOf, gaps:[...], provenance:"case-ledger"} and never carries allLanesStored (ruling 6)
    And no source is read and nothing is written
    And a genuinely newer read is the explicit POST /api/cases/{id}/pulls, so there is no bypassLedger and no fromLedger

  @SC-PRV-185 @TR-21 @mvp1 @unit
  Scenario: The preview digest changes with every bound input
    Given a preview digest hex(SHA-256("unravis-preview-v1" || caseId || caseRevision || scrubSensitive || projectionVersion))
    When caseId, caseRevision, scrubSensitive or projectionVersion changes
    Then the recomputed previewId differs
    And the same four inputs always produce the same previewId, and an event-attribute edit or a source or policy change increments case.revision (ruling 4)

  @SC-PRV-199 @TR-33 @mvp2 @unit
  Scenario: Coverage rows are keyed by source plus query fingerprint
    Given a coverage read for source "sql-xe" with its own effective query
    When a second read uses the same source with different canonical filter fields
    Then the coverage ledger holds two intervals that are never merged
    And the coverage fingerprint is the lowercase-hex SHA-256 over "unravis-coverage-v1\x00" || sourceId || "\x00" || lane || "\x00" || canonicalFilters for one source, never over the other sources in the same request, and the read window lives only in from_ms and to_ms (ruling 6)
    And a stored interval satisfies a request when it contains the requested interval for the same fingerprint and complete=1; any missing sub-intervals are reported as gaps and are filled only by an explicit POST .../pulls that reads remote SQL/Mongo and persists events+coverage, never by GET .../coverage (ruling 1)

  @SC-PRV-200 @AC-17 @TR-21 @mvp1 @api
  Scenario: Every export surface is scanned for sensitive bytes
    Given a case whose events carry attrs["client-ip"], attrs["referer"] and attrs["cookie:sid"]
    When the preview, confirm, bundle and report surfaces are built with scrub on
    Then no surface holds an IP literal, a referer or a raw cookie value (ruling 9)

  @SC-PRV-201 @TR-21 @mvp1 @api
  Scenario: A blank previewId is answered 409 with no bytes
    Given a preview built with scrubSensitive true
    When POST /api/export/confirm is called with a blank previewId
    Then the response status is 409 and no file is produced (ruling 1)

  @SC-PRV-202 @TR-21 @mvp1 @unit
  Scenario: Every export-affecting mutation increments case.revision
    Given a case at meta key case.revision n
    When an event attribute is edited or a source or export-read setting changes
    Then meta key case.revision increments inside the same transaction (ruling 1)

  @SC-PRV-203 @TR-21 @mvp1 @api
  Scenario: Two same-revision previews collide but confirm still re-derives
    Given two previews for the same case revision, scrub flag and projectionVersion
    When both are built
    Then they carry the same advisory previewId
    And confirm exports the same case revision and the same scrub policy class as the preview; it does not promise byte-identical output, because each export uses a fresh pseudonym key and records its own creation time (ruling 4)

  @SC-PRV-204 @AC-16 @TR-20 @mvp1 @unit
  Scenario: The cookie digest uses the one domain-prefixed key rule
    Given the same raw cookie value hashed under the same 32-byte HMAC key in two cases
    When both digests are computed
    Then the digests differ per case and match the fixed golden vector for fixed key, name and value
    And the null separators resolve the ambiguity between a plus b equals c and a equals b plus c (ruling 3)

  @SC-PRV-205 @TR-23 @mvp1 @e2e
  Scenario: The scrub pill states the export state in plain words
    Given no preview has been built yet
    When the trace toolbar is rendered
    Then the scrub pill reads "Default: export hides IP, referer and cookie hash" (ruling 12)

  @SC-PRV-206 @TR-33 @mvp2 @unit
  Scenario: The coverage fingerprint is over one source's effective query
    Given source s1 read alone and the same source read together with s2
    When both coverage rows are written
    Then the per-source fingerprint is identical in both cases
    And a changed filter yields a different fingerprint (ruling 6)

  @SC-PRV-221 @TR-33 @mvp2 @api
  Scenario: A stored interval satisfies a contained request without re-reading
    Given a complete coverage row for a source and filter over [00:00,02:00)
    When a request asks for [00:30,01:30) for the same fingerprint
    Then the stored interval satisfies the request and no source is read
    And uncovered sub-intervals are reported as gaps and are read only by an explicit POST .../pulls, never by GET .../coverage (ruling 1)

  @SC-PRV-223 @TR-21 @mvp1 @api
  Scenario: Revision bumps survive delete and re-add and confirm replay
    Given a case at meta key case.revision n with a source and an imported event
    When the import is deleted and re-added and the same scrub toggle is confirmed twice across two tabs
    Then case.revision increments inside each mutation transaction even though the row count is unchanged (ruling 4)
    And each confirm recomputes the revision and takes a read-consistent snapshot, and a changed revision yields 409 with no bytes

  @SC-PRV-224 @TR-33 @mvp2 @api
  Scenario: Delete import cascades coverage and retains an expiring attempt tombstone
    Given a case with an import that has coverage rows and an import_attempts row
    When the import is deleted
    Then the coverage rows cascade and the attempt survives with source_label and import_id NULL (ruling 7)
    And a 29-day-old tombstone is visible on case open while a 30-day-old one is pruned by MaxAttemptRetention
    And no stale source can be pulled and foreign_key_check is clean

  @SC-PRV-225 @TR-33 @mvp2 @api
  Scenario: A partial-page failure still serves the covered sliver
    Given a stored complete interval over [00:00,01:00) for a source and filter
    And a partial read over [01:00,02:00) whose second page failed
    When a request asks for [00:30,01:30)
    Then the [00:30,01:00) sliver is served from storage with no read
    And the uncovered [01:00,01:30) sliver is read only by an explicit POST .../pulls and the failed page never marks the covered part incomplete (ruling 1)

  @SC-PRV-260 @TR-15 @TR-21 @mvp1 @api
  Scenario: The one privacy projection is the only read-side scrub on every surface
    Given a case whose events carry CANARY_ values in client-ip, referer and cookie:sid
    When the events API, export preview and confirm, bundle and report are built on their default paths
    Then one pure function ProjectEventAttrs(attrs map[string]string, includeSensitive bool) map[string]string is applied to every outward-facing event view (events API, export bundle, report, tooltip/detail) (ruling 10)
    And includeSensitive=false is the default and drops client-ip, referer and every cookie:* key, and opt-in is explicit per read/export
    And zero CANARY_ hits appear in any default-path artifact and in any error or log buffer

  @SC-PRV-261 @TR-21 @mvp1 @api
  Scenario: Confirm matches the snapshot and the scrub policy class, never bytes
    Given a preview built with scrubbing on whose previewId matched the current caseRevision, scrubSensitive and projectionVersion
    When POST /api/export/confirm is called with its previewId
    Then confirm exports the same case revision and the same scrub policy class as the preview; it does not promise byte-identical output, because each export uses a fresh pseudonym key and records its own creation time (ruling 4)
    And confirm recomputes the revision, takes a read-consistent snapshot of the export inputs, and refuses 409 the preview changed; run the preview again if the revision differs (ruling 4)
    And the content carries no client IP, referer or cookie digest

  @SC-PRV-300 @TR-33 @mvp2 @api
  Scenario: Coverage GET has the coverage shape and says no-coverage when empty
    Given a case whose coverage ledger is empty for the requested window
    When GET /api/cases/{id}/coverage is called
    Then the response is {entries:[...], asOf, gaps:[...], provenance:"case-ledger"} and never carries allLanesStored (ruling 6)
    And the empty ledger returns entries:[] and state:"no-coverage" with a non-empty gaps for the requested window, never presented as stored-complete (ruling 6)
    And a coverage entry’s provenance is the ledger row itself (source, fingerprint, from, to, complete, content_digest, at), not a lane (ruling 6)
```

#### Eval cases

| EV-id | Category | Setup / input | Expected result | Maps to | Level | Phase |
|---|---|---|---|---|---|---|
| EV-PRV-01 | boundary | c-ip exactly 64 bytes, e.g. 63 characters plus one | attr client-ip is stored, whole | TR-19 | unit | mvp1 |
| EV-PRV-02 | boundary | c-ip 65 bytes | attr client-ip is absent; no error | TR-19 | unit | mvp1 |
| EV-PRV-03 | boundary | referer exactly 512 bytes, valid scheme+host+path | attr referer is stored, query and fragment cut first | TR-19 | unit | mvp1 |
| EV-PRV-04 | boundary | referer 513 bytes | attr referer is absent | TR-19 | unit | mvp1 |
| EV-PRV-05 | boundary | single cookie pair of 512 bytes | hashed and stored; cap rule holds | TR-20 | unit | mvp1 |
| EV-PRV-06 | boundary | single cookie pair of 513 bytes | skipped; import finishes | TR-20 | unit | mvp1 |
| EV-PRV-07 | boundary | exactly 20 cookie pairs, all allowlisted | 20 digest attrs stored | TR-20 | unit | mvp1 |
| EV-PRV-08 | boundary | exactly 21 cookie pairs, all allowlisted | 20 digest attrs stored, 21st dropped | TR-20 | unit | mvp1 |
| EV-PRV-09 | boundary | HMAC key exactly 32 bytes | HashCookie returns a 32-hex domain-prefixed digest; pseudonymous, not anonymous | TR-20 | unit | mvp1 |
| EV-PRV-10 | boundary | HMAC key exactly 31 bytes | HashCookie returns "" | TR-20 | unit | mvp1 |
| EV-PRV-11 | edge | empty cs(Cookie) cell "" or "-" | no cookie attribute; no crash | TR-20 | unit | mvp1 |
| EV-PRV-12 | edge | cs(Cookie) with duplicate names sid=1; sid=2 | one cookie:sid attr; the first pair wins (ruling 9) | TR-20 | unit | mvp1 |
| EV-PRV-13 | edge | allowlisted name appears with mixed case SID | matched after lower-casing | TR-20 | unit | mvp1 |
| EV-PRV-14 | edge | cookie name not on allowlist, value equals an allowlisted value | no attr for the unlisted name | TR-20 | unit | mvp1 |
| EV-PRV-15 | edge | referer with only scheme+host, no path | stored as scheme+host+"/" or scheme+host per url.Parse | TR-19 | unit | mvp1 |
| EV-PRV-16 | edge | referer with uppercase host and default port | stored with host as url.Parse keeps it | TR-19 | unit | mvp1 |
| EV-PRV-17 | security | raw cookie value also contains the digest prefix | no raw bytes anywhere in the marshalled event | TR-20 | unit | mvp1 |
| EV-PRV-18 | security | cookie value is a 512-byte base64 blob | only the 32-hex digest is stored | TR-20 | unit | mvp1 |
| EV-PRV-19 | security | client IP attr value is "169.254.169.254" (metadata address) | stored as a valid IP; CRM-truthful, it is still an IP | TR-19 | unit | mvp1 |
| EV-PRV-20 | security | referer host is a userinfo URL http://user:pw@h/x | userinfo dropped; scheme+host+path only | TR-19 | unit | mvp1 |
| EV-PRV-21 | privacy | exported file scanned byte-for-byte for any IPv4/IPv6 literal | none found when scrub on | TR-21 | unit | mvp1 |
| EV-PRV-22 | privacy | exported file scanned for the literal raw cookie string | none found with scrub on or off | TR-20,21 | unit | mvp1 |
| EV-PRV-23 | privacy | events API JSON scanned for client-ip/referer/cookie: keys with no opt-in | none found | TR-15 | api | mvp1 |
| EV-PRV-24 | privacy | includeSensitive=1 with a valid case key and no forced-scrub policy; maps to @SC-PRV-20 | the three attrs are returned; the cookie value is still a digest, never raw (ruling 9) | TR-15 | api | mvp1 |
| EV-PRV-25 | negative | includeSensitive value "TRUE " with trailing space; maps to @SC-PRV-102 | treated as not opted in; scrubbed | TR-15 | api | mvp1 |
| EV-PRV-26 | negative | cookie.hmac_key_v1 absent but allowlist non-empty; maps to @SC-PRV-101 | no cookie attr; no error | TR-20 | api | mvp1 |
| EV-PRV-27 | negative | cookie.allowlist meta holds an empty string entry "" | no cookie matches; nothing stored | TR-20 | unit | mvp1 |
| EV-PRV-28 | negative | #Fields declares cs(Cookie) but the row has a null marker "-" | no cookie attr | TR-20 | unit | mvp1 |
| EV-PRV-29 | regression | TestW3CDropsSensitiveColumns is replaced, TestW3CTimeIsEndGap untouched | both named tests behave as P1 states | WP-05 | unit | mvp1 |
| EV-PRV-30 | regression | TestBuildCaseNoIPsOrUserIDs and TestBuildCaseAttributePolicy | both still pass | AC-17 | unit | mvp1 |
| EV-PRV-31 | regression | export fixture attrs["cookie"] stays off every allowlist | left out of the bundle | WP-07 | unit | mvp1 |
| EV-PRV-32 | regression | re-import of identical bytes after enabling the policy | duplicate status, no new rows | TR-20 | api | mvp1 |
| EV-PRV-33 | edge | existing v4 case.db opened after the change | opens with no schema break; no new columns | 4.2 | api | mvp1 |
| EV-PRV-34 | edge | HMAC key read concurrently by two imports | both imports use the same stored key | TR-20 | api | mvp1 |
| EV-PRV-35 | edge | allowlist changed between two imports | old events keep old hashes; new events use the new list | TR-20 | api | mvp1 |
| EV-PRV-36 | copy | keep-on statement wording | contains "Client IP addresses, referers and cookie digests are not in this file." | 5.2 | manual | mvp1 |
| EV-PRV-37 | copy | scrub-off statement wording | contains "scrubbing was turned off" and "digests, never raw values" | 5.2 | manual | mvp1 |
| EV-PRV-38 | copy | events API opt-in is named consistently in UI copy | the opt-in is described as showing private fields | TR-15 | manual | mvp1 |
| EV-PRV-39 | visual-state | scrub pill default (no preview), on and off | three distinct readable states, all labellable | TR-23 | e2e | mvp1 |
| EV-PRV-40 | a11y | scrub pill and lane toggles via keyboard | reachable and announced; aria-pressed on toggles | TR-23 | e2e | mvp1 |
| EV-PRV-41 | resilience | a metadata URL or 5000-byte referer | import finishes; value either dropped or capped, never crashes | TR-19 | unit | mvp1 |
| EV-PRV-42 | edge | IPv6 with a zone id and a bracketed IPv6 with port [2001:db8::1]:443 | both rejected by validIPv4or6 | TR-19 | unit | mvp1 |
| EV-PRV-43 | edge | malformed=UTF-8 bytes in cs(Cookie) | row handling follows the existing encoding reject rule; no raw bytes leak | TR-20 | unit | mvp1 |
| EV-PRV-44 | performance | import of 100k W3C rows each with one allowlisted cookie | hashing adds no more than a small constant per row | TR-20 | bench | mvp1 |
| EV-PRV-45 | security | error text on a bad HMAC key or a bad cookie policy | message names no key bytes and no cookie value | TR-20 | api | mvp1 |
| EV-PRV-46 | copy | confirm sent with a missing or differing previewId; maps to @SC-PRV-104 | 409 with the preview-changed body; confirm refuses before producing bytes and matches the same case revision and scrub policy class, promising no byte-identical output (snapshot-plus-policy canary) | 5.2 | api | mvp1 |
| EV-PRV-160 | security | preview surface scanned for IPv4, IPv6, referer and raw cookie; maps to @SC-PRV-162 | none present with scrub on | TR-21 | api | mvp1 |
| EV-PRV-161 | security | confirm surface scanned for IPv4, IPv6, referer and raw cookie; maps to @SC-PRV-163 | none present with scrub on | TR-21 | api | mvp1 |
| EV-PRV-162 | security | bundle surface scanned for IPv4, IPv6, referer and raw cookie; maps to @SC-PRV-164 | none present with scrub on | TR-21 | api | mvp1 |
| EV-PRV-163 | security | report surface scanned for IPv4, IPv6, referer and raw cookie; maps to @SC-PRV-165 | none present with scrub on | TR-21 | api | mvp1 |
| EV-PRV-164 | negative | confirm called with a previewId from a different preview; maps to @SC-PRV-161 | 409, a new preview is required | TR-21 | api | mvp1 |
| EV-PRV-165 | negative | confirm called after the case revision changed since preview; maps to @SC-PRV-104 | 409, a new preview is required | TR-21 | api | mvp1 |
| EV-PRV-166 | resilience | failed pull attempt written outside the import transaction; maps to @SC-PRV-166 | coverage API shows last attempt failed; attempt adds no coverage | TR-33 | api | mvp2 |
| EV-PRV-167 | edge | coverage interval with an event exactly at its end; maps to @SC-PRV-167 | event falls in the next half-open interval | TR-33 | unit | mvp2 |
| EV-PRV-168 | edge | empty coverage ledger; maps to @SC-PRV-167 | returns [] with a state, not 404 unknown case | TR-33 | api | mvp2 |
| EV-PRV-169 | security | any request carrying a role field; maps to @SC-PRV-160 | authorized by case key alone; no role is read | TR-21 | api | mvp1 |
| EV-PRV-170 | privacy | duplicate cookie names sid=1; sid=2 with allowlist; maps to @SC-PRV-07 | first pair wins; only one cookie:sid digest | TR-20 | unit | mvp1 |
| EV-PRV-180 | negative | confirm with a blank or differing previewId; maps to @SC-PRV-180 | 409 with the run-the-preview-again body, no bytes produced (previewId 409 canary) | TR-21 | api | mvp1 |
| EV-PRV-181 | negative | two previews built at one revision, then one confirmed; maps to @SC-PRV-181 | both carry the same advisory previewId; confirm re-derives, no stored one-shot | TR-21 | api | mvp1 |
| EV-PRV-182 | resilience | two confirms racing on one previewId; maps to @SC-PRV-182 | confirm exports the same case revision and the same scrub policy class as the preview and promises no byte-identical output, with no one-shot refusal (snapshot-plus-policy canary) | TR-21 | api | mvp1 |
| EV-PRV-183 | resilience | failed pull attempt row written outside the import transaction; maps to @SC-PRV-183 | coverage API shows the last attempt failed; the attempt adds no coverage interval | TR-33 | api | mvp2 |
| EV-PRV-184 | boundary | repeat of a canonical coverage query already in the ledger; maps to @SC-PRV-184 | the stored tracing result has every lane origin case and the top-level allLanesStored true while GET coverage returns entries, asOf, gaps and provenance case-ledger and never allLanesStored; nothing is read or written; a bypassLedger field is refused with 400 (allLanesStored trace-only canary) | TR-33 | unit | mvp2 |
| EV-PRV-185 | boundary | previewId recomputed from the version tag plus caseId, caseRevision, scrubSensitive and projectionVersion; maps to @SC-PRV-185 | digest is deterministic; changing any input changes it | TR-21 | unit | mvp1 |
| EV-PRV-186 | security | preview, confirm, bundle and report surfaces all scanned for IPv4, IPv6, referer and raw cookie; maps to @SC-PRV-162 @SC-PRV-163 @SC-PRV-164 @SC-PRV-165 | none present on any export surface with scrub on (all-surfaces canary) | TR-21 | api | mvp1 |
| EV-PRV-199 | boundary | Coverage reads for one source with two different canonical query fingerprints, alone and together with another source | Two intervals are recorded, never merged, each keyed by source plus fingerprint; the same source alone and together yields the same per-source fingerprint | TR-33 | unit | mvp2 |
| EV-PRV-200 | security | preview, confirm, bundle and report built from a case with IP, referer and cookie digest; maps to @SC-PRV-200 | no surface holds an IP literal, referer or raw cookie (all export surfaces canary) | TR-21 | api | mvp1 |
| EV-PRV-201 | negative | confirm with a blank previewId; maps to @SC-PRV-201 | 409, no bytes produced (previewId 409 canary) | TR-21 | api | mvp1 |
| EV-PRV-202 | edge | event-attribute edit, then a source or settings change; maps to @SC-PRV-202 | case.revision increments in the same transaction each time | TR-21 | unit | mvp1 |
| EV-PRV-222 | resilience | delete a source that has coverage and an attempt row; maps to @SC-PRV-222 | coverage cascades, the attempt survives with source_label, and rows older than 30 days are pruned on the next case open; foreign_key_check is clean (source delete canary) | TR-33 | api | mvp2 |
| EV-PRV-203 | boundary | two previews at one revision; maps to @SC-PRV-203 | same advisory previewId; confirm exports the same case revision and the same scrub policy class as the preview and promises no byte-identical output | TR-21 | api | mvp1 |
| EV-PRV-221 | boundary | subset, overlapping, adjacent and disjoint intervals for one source and filter; maps to @SC-PRV-221 | a stored complete interval satisfies any contained request with no read; uncovered slivers are reported as gaps and are read only by an explicit POST .../pulls, never by GET .../coverage; adjacent intervals merge for storage but never for correctness (interval reuse canary) | TR-33 | unit | mvp2 |
| EV-PRV-204 | security | fixed key, name and value golden vector plus cross-case isolation; maps to @SC-PRV-204 | deterministic 32-hex domain-prefixed digest; per-case isolation; no raw value in any marshalled byte | TR-20 | unit | mvp1 |
| EV-PRV-205 | copy | no preview yet; maps to @SC-PRV-205 | pill reads Default: export hides IP, referer and cookie hash | TR-23 | e2e | mvp1 |
| EV-PRV-220 | boundary | every export-affecting mutation, then a concurrent import during confirm; maps to @SC-PRV-220 | each mutation increments case.revision in its own transaction; the concurrent import yields 409 with the run-the-preview-again body and no bytes (revision snapshot canary) | TR-21 | api | mvp1 |
| EV-PRV-206 | boundary | same source alone and together with another source; maps to @SC-PRV-206 | identical per-source fingerprint; a changed filter differs | TR-33 | unit | mvp2 |
| EV-PRV-223 | boundary | delete the import after a source delete, then open the case at 29 and 30 days; maps to @SC-PRV-224 | coverage cascades, the attempt keeps source_label and import_id NULL, and the tombstone is visible at 29 days and pruned after 30 on the next open (attempt retention canary) | TR-33 | api | mvp2 |
| EV-PRV-224 | boundary | delete then re-add an import, toggle the same scrub twice and replay one confirmId from two tabs; maps to @SC-PRV-223 | case.revision increments in each mutation transaction even at an unchanged row count, and each confirm snapshots the revision and re-derives bytes or 409s (revision replay canary) | TR-21 | api | mvp1 |
| EV-PRV-225 | resilience | a partial read with a failed page over a sub-interval adjacent to a stored complete one; maps to @SC-PRV-225 | the stored sliver is served without a read, only the uncovered sliver is read, and the failed page never marks the covered part incomplete (partial-page sliver canary) | TR-33 | api | mvp2 |
| EV-PRV-240 | privacy | CANARY_ planted in client-ip, referer and cookie:sid, then every default surface scanned; maps to @SC-PRV-240 | one ProjectEventAttrs projection drops all three on the events API, export bundle, report and tooltip/detail, with zero canary hits in artifacts and log buffers; includeSensitive true returns them only on the events API (privacy projection canary) | TR-15 | api | mvp1 |
| EV-PRV-260 | privacy | CANARY_ planted in client-ip, referer and cookie:sid, then every default artifact, error and log buffer scanned; maps to @SC-PRV-260 | one ProjectEventAttrs projection drops all three on every default path with zero canary hits, and opt-in is explicit per read/export (privacy projection canary) | TR-15, TR-21 | api | mvp1 |
| EV-PRV-261 | resilience | a preview whose revision and scrub class still match, then confirm; maps to @SC-PRV-261 | confirm exports the same case revision and the same scrub policy class as the preview and promises no byte-identical output, because each export uses a fresh pseudonym key and records its own creation time (snapshot-plus-scrub-policy canary) | TR-21 | api | mvp1 |
| EV-PRV-280 | privacy | CANARY_ planted in client-ip, referer and cookie:sid, then every default surface scanned; maps to @SC-PRV-280 | one ProjectEventAttrs projection drops all three on the events API, export bundle, report and tooltip/detail, with zero canary hits in artifacts and log buffers (privacy projection canary) | TR-15 | api | mvp1 |
| EV-PRV-281 | resilience | a preview whose revision and scrub class still match, then confirm; maps to @SC-PRV-281 | confirm exports the same case revision and the same scrub policy class as the preview and promises no byte-identical output, because each export uses a fresh pseudonym key and records its own creation time (snapshot-plus-scrub-policy canary) | TR-21 | api | mvp1 |
| EV-PRV-300 | boundary | an empty coverage ledger and a populated one for a requested window; maps to @SC-PRV-300 | the response is entries, asOf, gaps, provenance case-ledger and never allLanesStored; the empty ledger returns entries [] and state no-coverage with non-empty gaps (coverage GET shape canary) | TR-33 | unit | mvp2 |

#### Open points

- `GET /api/cases/{id}/events` opt-in parameter name `includeSensitive` is taken from spec §5.5 ("e.g. query `includeSensitive=1`"); the exact name is still **(proposed)**, but opt-in no longer depends on any forced-scrub case policy — with ruling 9 it always returns the three attrs when the caller holds the case key.
- The scrub-off masking statement wording is **(proposed)**; spec P2 suggests "This file holds client IP addresses, referers and cookie digests because scrubbing was turned off. Cookie values are digests, never raw values." but the code's current statement at `internal/evidence/caseexport.go:751` is the only pinned text.
- The preview response field `scrubSensitive` is **(proposed)** by spec §5.2 ("Preview response gains `scrubSensitive` (default true)"); no current `exportPreviewResponse` field exists.
- Duplicate cookie-name resolution is decided by ruling 9: the first pair wins; the importer must not let a later pair overwrite it.
- `X-Forwarded-For` handling is **not covered by the spec**: only the W3C `c-ip` column is named (`w3c.go` / P1). Multi-address or proxy chains are **out of scope for MVP-1**; the importer reads `c-ip` only, so a proxy list is treated as a malformed IP and dropped.
- The HTTPERR `c-ip` column presence is **(unverified)** (VF-01c/VF-01d); P1 WP-04 says to store `client-ip` only if a real sample declares it, else write a gap note.
- Forced scrub is removed in MVP-1 (ruling 9): there is no forced-scrub toggle, flag or store key; export scrub default ON is an independent export-only decision (D-9) and is unaffected.
- The per-case cookie allowlist is edited in the case settings (proposed UI, ruling 9); until an allowlist is set, cookie hashing is off and the UI says so.

### Area C: tracing UI and wireframe states (SC-UI, EV-UI, WS)

#### F-UI-01 Case home offers Tracing and no longer offers Guided  (TR-01, WP-16, WP-63, WP-64)

```gherkin
Feature: Case home entry to tracing

  Background:
    Given an open case with at least one saved source
    And the case home page is shown

  @SC-UI-01 @TR-01 @mvp1 @e2e
  Scenario: The case home shows a Start tracing button
    Given the case home has loaded
    Then a button named "Start tracing" is visible in the investigation area
    And pressing it opens the tracing page for this case

  @SC-UI-02 @TR-01 @D-2 @mvp1 @e2e
  Scenario: The case home no longer offers the Guided button
    Given the case home has loaded
    Then no button reads "Open guided analysis"
    And the guided route is not linked anywhere on the case home

  @SC-UI-03 @AC-08 @TR-03 @mvp1 @e2e
  Scenario: A closed case disables Start tracing
    Given the case is closed
    When the case home shows the closed-case notice
    Then the "Start tracing" button is visible but disabled
    And a note says the case is closed so nothing can be added

  @SC-UI-04 @TR-01 @mvp1 @e2e
  Scenario Outline: Run analysis and the rest of the case home are unchanged
    Given the case home with <section>
    Then the section is present exactly as before tracing was added
    Examples:
      | section |
      | Open the analysis |
      | Theories |
      | Health checks |
      | Files |
      | Story so far |
```

#### F-UI-02 Start tracing: sources and window  (TR-02, TR-08, TR-11, WP-15, WP-22)

```gherkin
Feature: Choosing what to trace and over which window

  @SC-UI-05 @AC-03 @TR-02 @D-4 @mvp1 @e2e
  Scenario: The window picker defaults to the last hour
    Given the tracing page is open for a case with sources
    Then the incident window shows a start and an end one hour apart
    And the end defaults to now

  @SC-UI-06 @AC-03 @TR-02 @D-4 @mvp1 @e2e
  Scenario: A window longer than 30 days is refused in plain words
    Given the tracing page is open
    When the end is set 30 days and 1 second after the start
    Then a plain-language error says the longest window is 30 days
    And the chart is not drawn

  @SC-UI-07 @AC-10 @TR-16 @D-4 @mvp1 @e2e
  Scenario: A 30-day window is accepted and coarsened
    Given the tracing page is open
    When the window is set to exactly 30 days
    Then there is exactly one presentation axis, the UTC floor-aligned, never-re-anchored grid (axisStart = floorUTC(start,3600000), axisEnd = ceilUTC(end,3600000)), and the presented width is W = max(ladderWidth(span), alignedRung(axisStart, axisEnd)), where alignedRung is the first supported rung whose aligned bucket count is at most 120
    And the aligned count is ceil((axisEnd-axisStart)/W) and is always at most 120, and the chart labels the actual width shown, so an hour-aligned 30-day window at 00:00 shows 6 h while a 00:01 start coarsens to a wider aligned rung
    And the number of boundary raw scans is zero, one, or two: zero when both boundaries align, one when exactly one boundary is unaligned or both boundaries fall inside the same grain-hour, and two otherwise

  @SC-UI-08 @AC-06 @TR-11 @mvp1 @e2e
  Scenario: A source without a validated time zone disables the multi-source trace
    Given a multi-source trace where one chosen source has no validated time-zone rule
    When the trace is requested
    Then the chart is disabled as a whole with the tz-rule reason "Time zone not checked, so nothing is drawn"
    And no lane row plots any point

  @SC-UI-09 @AC-04 @TR-08 @mvp1 @e2e
  Scenario: Overlapping reference and incident windows warn without blocking
    Given the tracing page is open
    When the reference window shares time with the incident window
    Then a non-blocking notice says the two windows overlap
    And the "Start tracing" action stays enabled

  @SC-UI-10 @TR-05 @mvp1 @e2e
  Scenario: The UTC and Local toggle changes how times are shown
    Given a trace has been drawn with times in UTC
    When the header toggle is set to Local
    Then every axis label and tooltip time moves to the browser time zone
    And the underlying window in UTC is unchanged
```

#### F-UI-03 The stacked-row tracing chart  (TR-04, TR-06, TR-07, TR-12, TR-31, D-1, D-3, WP-17, WP-18)

```gherkin
Feature: One stacked row per source

  Background:
    Given a case with an audit source, an IIS import and a SQL import, all in one UTC window

  @SC-UI-11 @AC-01 @AC-13 @TR-04 @D-1 @mvp1 @e2e
  Scenario: Three lanes are drawn, one row each
    When the trace is drawn for the shared window
    Then there is a row for API audit, a row for SQL and a row for IIS
    And each row is on its own horizontal line

  @SC-UI-12 @AC-09 @TR-06 @D-4 @mvp1 @e2e
  Scenario: The bucket size is labelled and never exceeds 120 buckets
    When the trace is drawn
    Then a visible label states the bucket size
    And the number of buckets on the axis is 120 or fewer

  @SC-UI-13 @AC-14 @TR-07 @D-3 @mvp1 @e2e
  Scenario Outline: A lane row shows counts, time and status
    Given the <lane> lane has events in the window
    Then the row shows the count of events
    And the row shows the time of the events
    And the row shows the lane status for <lane>
    Examples:
      | lane     |
      | API audit |
      | SQL      |
      | IIS      |

  @SC-UI-14 @AC-02 @TR-07 @mvp1 @e2e
  Scenario: A missing lane shows its fixed reason, not a zero line
    Given the case has no SQL source
    Then the SQL row shows the fixed sentence "This case has no file for this source"
    And no zero-value line is drawn on the SQL row

  @SC-UI-15 @AC-10 @TR-16 @mvp1 @e2e
  Scenario: A window longer than 24 hours carries the hourly footnote
    Given the incident window is 48 hours
    Then the bucket-size label is shown
    And a footnote says longer windows use coarser buckets

  @SC-UI-16 @AC-15 @TR-12 @mvp1 @manual
  Scenario: Opening Tracing leaves the per-call call-trace tray untouched
    Given the per-call call trace tray is open over Evidence
    When the tracing chart opens on TracingPaper
    Then the call-trace tray still shows the same chosen calls
    And no control of the tray was added or removed
```

#### F-UI-04 Bucket click hands off to Run analysis  (TR-09, WP-61)

```gherkin
Feature: Drill down from one bucket

  @SC-UI-17 @AC-07 @TR-09 @mvp1 @e2e
  Scenario: Clicking a bucket opens Run analysis for that sub-window
    Given an hour-aligned 2 h window whose presented width is 60 s under W = max(ladderWidth(span), alignedRung(axisStart, axisEnd))
    When a bucket is clicked
    Then Run analysis opens with the incident window set to that bucket's start and end
    And the chart view is not changed by the click

  @SC-UI-18 @TR-09 @mvp1 @manual
  Scenario: The hand-off keeps the chosen sources
    Given three sources were chosen for the trace
    When a bucket is clicked
    Then the analysis opens for the same sources and the clicked sub-window
```

#### F-UI-05 Toolbar, tooltips, zoom and scrub  (TR-22, TR-23, WP-08, WP-09, WP-10, WP-23)

```gherkin
Feature: Canvas toolbar and plain-word tooltips

  @SC-UI-19 @TR-23 @mvp1 @e2e
  Scenario: Each lane can be toggled off and on
    Given the trace shows three lane rows
    When the API audit toggle is pressed
    Then the API audit row is hidden
    And its toggle reports aria-pressed="false"
    And pressing it again brings the row back

  @SC-UI-20 @TR-23 @mvp1 @e2e
  Scenario: Zoom and reset change the visible span only
    Given the trace is drawn
    When the zoom control is dragged in
    Then the visible time span shrinks
    And pressing reset returns the full window

  @SC-UI-21 @TR-22 @WP-08 @mvp1 @unit
  Scenario: A tooltip explains a technical label
    Given the tooltip dictionary is loaded
    When explain("latency.p95") is called
    Then it returns a plain sentence and never throws
    And explain("missing") returns an empty string

  @SC-UI-22 @TR-22 @gap.time-end @mvp1 @manual
  Scenario: The time-end tooltip states the assumed semantics
    Given the IIS lane has rows and the tooltip dictionary is loaded
    When the time-end gap tooltip is shown
    Then it reads the gap.time-end entry "The W3C time field is assumed to be the request end" (proposed until web/src/lib/copy/dictionary.ts lands)
    And the IIS event is drawn as a point at its own timestamp with the observed time-taken shown separately
    And the UI never claims a verified request start and never calls time-taken API processing time
    And the note stays until the W3C time semantics are confirmed by VF-01

  @SC-UI-23 @TR-23 @WP-10 @mvp1 @e2e
  Scenario: The scrub pill states the export scrub state in plain words
    Given the export scrub default is on
    Then the toolbar scrub pill reads "Export hides IP, referer and cookie hash"
    And when the export scrub default is turned off it reads "Export includes IP, referer and cookie hash (scrub is off)"
    And before a preview exists it reads "Default: export hides IP, referer and cookie hash"
    And it never says stored data is masked

  @SC-UI-24 @TR-23 @mvp1 @e2e
  Scenario: A time-range brush marks the span while a drag selection is active
    Given the trace is drawn
    When a time-range brush drag starts
    Then a brush indicator marks the selected span
    And the control is named "time-range brush", never "scrub", which is reserved for the privacy pill
    And it disappears when the drag ends
```

#### F-UI-06 Demo mode, seeded lanes and reliability states  (TR-18, WP-25)

```gherkin
Feature: Demo data and honest failure states

  @SC-UI-25 @AC-09 @TR-18 @WP-25 @mvp1 @e2e
  Scenario: A seeded demo shows all three lanes
    Given the demo case is seeded with audit, SQL and IIS events
    When the trace is drawn for the demo window
    Then all three lanes are in state "events"
    And the bucket-size label is visible and 120 or fewer buckets are drawn

  @SC-UI-26 @AC-11 @TR-13 @mvp1 @api
  Scenario: A locked master key stops the trace with a plain error
    Given the master key is locked
    When the trace is requested
    Then the response is 423
    And the page shows a plain-language error that says the key must be unlocked

  @SC-UI-27 @AC-05 @TR-10 @mvp1 @e2e
  Scenario: A truncated read says so and shows the footnote
    Given the window holds more than 50000 raw events
    When the trace is drawn
    Then the response carries truncated=true
    And a footnote says the list was cut, not that the counts are wrong

  @SC-UI-28 @TR-07 @mvp1 @e2e
  Scenario Outline: An error from the server shows a plain message
    Given the tracing endpoint answers <status>
    Then the page shows a plain-language error that says <meaning>
    And a "Try again" button is offered
    Examples:
      | status | meaning |
      | 400    | the window or bucket size is not allowed |
      | 409    | the case is closed |
      | 423    | the case key is locked |
      | 503    | the case store is unavailable right now |
      | 5xx    | the case could not be read right now |
```

#### F-UI-07 Keyboard, screen reader, theme and layout  (TR-12, WP-16, WP-23)

```gherkin
Feature: Reaching and reading the trace without a mouse

  @SC-UI-29 @AC-15 @TR-12 @mvp1 @e2e
  Scenario: The whole trace can be operated from the keyboard
    Given the tracing page is open
    When the user tabs from the page heading
    Then focus reaches the window fields, the Start tracing button, every lane toggle, the chart and the TraceToolbar percentile selector
    And each control shows a visible focus outline
    And focus returns to the Start tracing opener on TracingPaper after the trace closes

  @SC-UI-30 @TR-12 @mvp1 @e2e
  Scenario Outline: Each drawn element has a screen-reader name
    Given the trace is drawn
    Then the <element> has an accessible name that reads its source, time and status
    Examples:
      | element |
      | chart region |
      | each lane row |
      | each event point |
      | each missing-lane notice |

  @SC-UI-31 @D-3 @TR-22 @mvp1 @e2e
  Scenario: Status is never carried by colour alone
    Given a lane row shows a status
    Then the status has a text word or icon beside its colour
    And the row reads correctly in forced-colours mode

  @SC-UI-32 @TR-05 @mvp1 @e2e
  Scenario Outline: The trace works in both themes and respects reduced motion
    Given the <theme> theme is active
    Then the chart lines and labels meet contrast and remain readable
    And when the user prefers reduced motion no transition or animation plays
    Examples:
      | theme |
      | dark  |
      | light |

  @SC-UI-33 @TR-12 @mvp1 @e2e
  Scenario Outline: The trace stays usable at both widths
    Given the viewport is <width> wide
    Then the chart scrolls sideways rather than clipping
    And every control remains reachable and named
    Examples:
      | width   |
      | 1440x900 |
      | 480x900  |
```

#### F-UI-08 Plain-language copy  (TR-22, WP-08)

```gherkin
Feature: Copy follows the writing rules

  @SC-UI-34 @TR-22 @WP-08 @mvp1 @unit
  Scenario: Tracing copy passes the copy lint
    Given the tracing screen strings and the tooltip dictionary
    When lintScreenStrings is run
    Then there are no causal, jargon, wording or reading-level findings
    And every jargon word has a dictionary entry

  @SC-UI-35 @TR-22 @mvp1 @manual
  Scenario: No tracing sentence names a cause
    Given every fixed sentence the tracing page can show
    Then none uses a causal verb
    And each says what was measured, with the word "Symptom" where a symptom is meant

  @SC-UI-100 @TR-01 @TR-03 @AC-08 @mvp1 @e2e
  Scenario: An empty open case disables Start tracing
    Given an open case with no sources and no files
    When the case home is shown
    Then the "Start tracing" button is visible but disabled
    And a quiet note says no files have been added to this case yet

  @SC-UI-101 @AC-11 @TR-13 @mvp1 @e2e
  Scenario: A locked key on the tracing page shows no lane data
    Given the tracing page is open for a case whose master key is locked
    When the trace is requested
    Then a locked notice is shown with no lane data
    And the notice says the key is locked, distinct from the case-home WS-05 locked card

  @SC-UI-102 @TR-21 @D-9 @mvp1 @e2e
  Scenario: The scrub pill states the export scrub state in plain words
    Given the tracing page is open and the export scrub default is on
    Then the toolbar scrub pill reads "Export hides IP, referer and cookie hash"
    And when the export scrub default is turned off it reads "Export includes IP, referer and cookie hash (scrub is off)"
    And before a preview exists it reads "Default: export hides IP, referer and cookie hash"
    And it never says stored data is masked (same behaviour as SC-UI-23; no tracing-page export preview panel in MVP-1)

  @SC-UI-120 @TR-01 @mvp1 @e2e
  Scenario: A case with files but no source can still start tracing
    Given an open case whose files are listed and no saved source is chosen
    When the case home is shown
    Then the "Start tracing" button is visible and enabled
    And a hint says a source is chosen next

  @SC-UI-121 @TR-07 @mvp1 @e2e
  Scenario: A single source previews the missing lanes
    Given an open case with exactly one saved source
    When the case home is shown
    Then the "Start tracing" button is enabled
    And a note says the other lanes will show a missing-source reason

  @SC-UI-122 @TR-03 @AC-08 @mvp1 @e2e
  Scenario: The tracing page on a closed case is read-only
    Given the tracing page is open for a case that is closed
    Then the "Start tracing" button is disabled
    And a read-only notice says the case is closed
    And no trace control is active

  @SC-UI-123 @TR-13 @mvp1 @e2e
  Scenario: A cases-unavailable answer clears the chart
    Given the tracing endpoint answers 503 because the case store is unavailable
    Then the page shows a plain-language error
    And a "Try again" button is offered
    And the chart is cleared

  @SC-UI-140 @TR-06 @TR-23 @AC-15 @mvp1 @e2e
  Scenario: The percentile selector is reachable from the keyboard
    Given the trace is drawn with the TraceToolbar mounted in TracingPaper
    Then a p50/p95/p99 selector is offered
    And the chosen value is pressed and each option is a real button with aria-pressed
    And an "Approximate" label with bounds is shown where used

  @SC-UI-141 @TR-01 @D-2 @mvp1 @e2e
  Scenario: Main e2e specs no longer import the guided opener (built by WP-62)
    Given the guided specs have been archived by WP-62
    When the main tools/e2e/specs folder is searched
    Then no main spec imports openGuided
    And help-guide.spec.ts stays in the main run without openGuided

  @SC-UI-142 @TR-18 @AC-09 @mvp1 @e2e
  Scenario: The demo case is empty before its lanes are seeded
    Given the demo case before the demo lanes are seeded
    When the tracing page is shown
    Then the empty-case state is shown, not three empty lanes

  @SC-UI-143 @TR-01 @D-2 @mvp1 @e2e
  Scenario: The archive README names the archive command (built by WP-62)
    Given the guided archive folder holds its README
    Then the README names ./run-e2e.ps1 -Archive as the way to run the archived specs
    And archived specs are run on demand and may need their own opener after WP-64 (not part of the main run)

  @SC-UI-160 @TR-31 @WP-20 @mvp1 @manual
  Scenario: An IIS event is drawn as a point with time-taken shown separately
    Given the IIS lane has an event with a timestamp and a time-taken value
    When the trace is drawn
    Then the event is drawn as a point at its own timestamp
    And the observed time-taken is shown separately from the point
    And the UI never claims a verified request start
    And the UI never calls time-taken API processing time (WP-20 depends on WP-45/VF-01)

  @SC-UI-161 @TR-03 @WP-14 @mvp1 @e2e
  Scenario: A new Start tracing cancels only its own request context
    Given one trace request is in flight for the case with requestSeq 1
    When the same UI instance presses Start tracing again with requestSeq 2
    Then requestSeq 1 is cancelled by its request context and only the newest response is drawn
    And a stale response is ignored by the client-only per-tab requestSeq
    And two tabs with the same query each perform their own bounded live audit read, with no serverWorkId, no subscriber count and no shared in-flight read
    And a client token is never authorization

  @SC-UI-162 @TR-12 @WP-09 @mvp1 @manual
  Scenario: The per-call tray only gains explain labels
    Given the per-call call trace tray over Evidence
    Then explain() labels are the only change WP-09 makes to CallTrace.svelte
    And the tray otherwise stays "Not wired" and unchanged

  @SC-UI-163 @TR-10 @WP-18 @mvp1 @e2e
  Scenario: A capped lane says first N rows while the SQL and IIS bucket counts stay exact
    Given a raw lane that hit the 50000 row cap and an audit lane whose server-side $match+$group aggregation exceeded the 60 s CensusQueryBudget
    When the trace is drawn
    Then the raw event list is labelled "first N rows"
    And the SQL and IIS bucket counts come from the uncapped local case.db GROUP BY aggregate and are exact
    And the audit lane computes its buckets with one server-side $match+$group aggregation per source and is a live aggregate, not a snapshot
    And the audit lane is incomplete and complete=false with a reason from {timeout,rowcap,error}, while the SQL and IIS lanes keep their exact local counts

  @SC-UI-164 @TR-32 @WP-24 @later @manual
  Scenario: win32-partitioned IIS series is a later estimate-only overlay
    Given the win32-partitioned IIS series work packet is deferred
    When the series is drawn
    Then it is labelled "estimate"
    And it is never presented as measured

  @SC-UI-180 @TR-03 @WP-14 @mvp1 @e2e
  Scenario: Two tabs with the same case key each run their own bounded read
    Given two browser tabs hold the same case key
    When each tab starts a trace with its own per-tab requestSeq for the same window
    Then each tab performs its own bounded live audit read, with no serverWorkId and no subscriber count
    And cancelling one tab stops only its own request context and never the other tab
    And per-source concurrency is bounded by MaxAuditConcurrency (proposed)
    And a client token is never authorization

  @SC-UI-181 @TR-23 @mvp1 @e2e
  Scenario: Chart dragging is named a time-range brush, not a scrub
    Given the trace is drawn
    When the user drags across the chart to select a span
    Then the control is named "time-range brush" in its label and copy
    And no tracing control calls that drag a "scrub", which is reserved for the privacy pill

  @SC-UI-200 @TR-03 @WP-14 @mvp1 @e2e
  Scenario: Cancelling one tab never cancels the other tab's read
    Given two tabs request the same window
    And each runs its own bounded live audit read with no shared in-flight read
    When one tab cancels its request
    Then only that tab's request context is cancelled
    And the other tab's read continues unaffected

  @SC-UI-201 @TR-03 @WP-14 @mvp1 @e2e
  Scenario: Cancelling a request stops its own read only
    Given one request with its own per-tab requestSeq and request context
    When the request is cancelled or times out
    Then its own read is cancelled and no error banner is shown
    And no other tab's request is affected, because there is no subscriber count and no shared in-flight read

  @SC-UI-202 @TR-23 @WP-10 @mvp1 @e2e
  Scenario: The scrub pill default wording is announced before any preview
    Given the tracing toolbar has no export preview yet
    Then the scrub pill reads "Default: export hides IP, referer and cookie hash"
    And the same sentence is exposed as its aria-label and announced in a live region
    And it never says stored data is masked

  @SC-UI-203 @TR-26 @mvp2 @e2e
  Scenario: Refresh re-fetches the trace state while Pull now is the live read
    Given the tracing page is open with a drawn trace
    When the user presses Refresh
    Then Refresh re-invokes POST .../tracing, which may run one ephemeral read-only live read for the audit lane and never persists
    And three operations are distinct: POST .../tracing may run one ephemeral read-only live read for the audit lane and never persists; GET .../coverage reads case.db only, reports gaps, and never fills them; POST .../pulls is the only operation that reads remote SQL/Mongo and persists events+coverage
    And each lane carries readKind in {saved, live} and separate complete/asOf fields, and GET .../coverage opens no source connection even for an uncovered window
    And the Pull now control is the only control that performs a persisting remote read

  @SC-UI-204 @TR-03 @WP-14 @mvp1 @e2e
  Scenario: Rapid repeated Start ignores stale answers by requestSeq
    Given a UI instance sends requestSeq 1 then quickly requestSeq 2 and requestSeq 3
    When responses arrive out of order
    Then only the response matching the highest requestSeq is drawn
    And the lower requestSeq responses are ignored

  @SC-UI-220 @TR-06 @AC-10 @mvp1 @unit
  Scenario: A 30-day window with an offset start coarsens to a wider aligned rung
    Given a trace window starting at 00:01 on the first day and ending 30 days later
    When the presentation bucket count would exceed 120 on the aligned grid
    Then the axis stays UTC floor-aligned and the grid is never re-anchored, and the next coarser supported rung is selected from [60s,300s,600s,900s,1h,2h,4h,6h,8h,12h,24h]
    And the buckets tile [start, end) exactly once with no gap and no overlap
    And the aligned count is at most 120, and the number of boundary raw scans is zero, one, or two: zero when both boundaries align, one when exactly one boundary is unaligned or both boundaries fall inside the same grain-hour, and two otherwise

  @SC-UI-221 @TR-07 @TR-10 @mvp1 @e2e
  Scenario: An audit read that times out is incomplete, never a short window
    Given the live audit lane uses one server-side $match+$group aggregation per source under the existing 60 s CensusQueryBudget and the existing broad-scan gate
    When the aggregation does not return to end within budget
    Then the audit lane state is incomplete and complete=false with a reason from {timeout,rowcap,error}
    And the audit lane is a live aggregate, not a snapshot
    And the SQL and IIS lanes keep their exact local counts

  @SC-UI-260 @TR-03 @WP-14 @mvp1 @e2e
  Scenario: Two tabs with the same query each perform their own bounded live audit read
    Given two tabs hold the same case key and send the same query at the same time
    When each tab starts its own trace
    Then each tab performs its own bounded live audit read bounded by MaxAuditConcurrency 2
    And no shared in-flight read exists, so neither tab cancels the other

  @SC-UI-280 @TR-26 @AC-19 @D-7 @mvp2 @e2e
  Scenario: Refresh re-invokes the trace while only a pull persists
    Given the tracing page is open for a case with a stored SQL lane and a live audit source
    When the user presses Refresh
    Then three operations are distinct: POST .../tracing may run one ephemeral read-only live read for the audit lane and never persists; GET .../coverage reads case.db only, reports gaps, and never fills them; POST .../pulls is the only operation that reads remote SQL/Mongo and persists events+coverage
    And each lane carries readKind in {saved, live} and separate complete/asOf fields, and GET .../coverage opens no source connection even for an uncovered window
    And Refresh never persists and only POST .../pulls performs a persisting remote read

  @SC-UI-300 @TR-07 @mvp1 @e2e
  Scenario: A mixed-consistency trace shows the non-dismissible note
    Given a trace mixes a live audit lane whose consistency is "live-non-snapshot" with a saved SQL lane whose consistency is "stored-as-of"
    When the trace is drawn
    Then the response sets mixedConsistency:true and the UI shows the non-dismissible note "live and saved lanes are read at different times; totals across lanes are not a conservation check."
    And complete:true never implies a snapshot and no cross-lane total is presented as exact
```

#### Eval cases

| EV-id | Category | Setup / input | Expected result | Maps to | Level | Phase |
|---|---|---|---|---|---|---|
| EV-UI-01 | boundary | window of 29d 23h 59m | accepted; the presented width is W = max(ladderWidth(span), alignedRung(axisStart, axisEnd)) with the aligned count at most 120 (hour-aligned start: 6 h) | TR-02, TR-06 | api | mvp1 |
| EV-UI-02 | boundary | window of exactly 30d | accepted; for a UTC hour-aligned start at 00:00: at most 120 buckets tiling [start, end) once, W = span/120 = 21600000 ms (6 h nominal) on a UTC floor-aligned axis that is never re-anchored; misaligned 30 d starts follow EV-UI-220 | TR-02, TR-06 | api | mvp1 |
| EV-UI-03 | negative | window of 30d 1s | 400 with a plain-language error naming the 30-day cap | TR-02 | api | mvp1 |
| EV-UI-04 | boundary | window of exactly 2h | ladderWidth(span) is 60s, and the presented width is W = max(ladderWidth(span), alignedRung(axisStart, axisEnd)), so an hour-aligned 2h window shows 60s while an off-hour start whose aligned count would exceed 120 coarsens to the next supported rung; the aligned count is always at most 120 | TR-06 | unit | mvp1 |
| EV-UI-05 | boundary | window of 2h 1s | the presented width is W = max(ladderWidth(span), alignedRung(axisStart, axisEnd)) with the aligned count at most 120, moving off 60 s to the 300 s ladder rung (hour-aligned start: 300 s) | TR-06 | unit | mvp1 |
| EV-UI-06 | boundary | window of 24h | ladderWidth(span) is 900s, and the presented width is W = max(ladderWidth(span), alignedRung(axisStart, axisEnd)); the aligned count is at most 120 and the chart labels the actual width shown; no >24h footnote | TR-06 | e2e | mvp1 |
| EV-UI-07 | boundary | window of 5d | the presented width is W = max(ladderWidth(span), alignedRung(axisStart, axisEnd)) with the aligned count at most 120 (hour-aligned start: 3600 s) | TR-06 | unit | mvp1 |
| EV-UI-08 | boundary | window of 10d 1s | the presented width is W = max(ladderWidth(span), alignedRung(axisStart, axisEnd)) with the aligned count at most 120 (hour-aligned start: 14400 s) | TR-06 | unit | mvp1 |
| EV-UI-09 | boundary | window of 20d 1s | the presented width is W = max(ladderWidth(span), alignedRung(axisStart, axisEnd)) with the aligned count at most 120 (hour-aligned start: 21600 s) | TR-06 | unit | mvp1 |
| EV-UI-10 | edge | zero-length window (start equals end) | 400 plain error that says the window must be longer than zero | TR-02 | api | mvp1 |
| EV-UI-11 | edge | end before start | 400 plain error; no chart drawn | TR-02 | api | mvp1 |
| EV-UI-12 | resilience | audit source reachable but the case store holds no SQL or IIS rows | audit lane events; SQL and IIS lanes "no-data" | TR-07 | e2e | mvp1 |
| EV-UI-13 | edge | IIS lane with events that have no time-taken | counts drawn; the no-time-taken gap note shown | AC-14, TR-31 | e2e | mvp1 |
| EV-UI-14 | edge | every bucket in a lane empty but files cover the window | row state "none-seen", not an empty zero line | TR-07 | e2e | mvp1 |
| EV-UI-15 | edge | files for a lane end before the window starts | row state "out-of-range" with its fixed sentence | TR-07 | e2e | mvp1 |
| EV-UI-16 | resilience | event read hits the 50000-row cap | truncated=true and footnote; lane states become incomplete, never none-seen | AC-05, TR-10 | api | mvp1 |
| EV-UI-17 | boundary | exactly 50000 raw events | truncated=false | TR-10 | api | mvp1 |
| EV-UI-18 | boundary | 50001 raw events | truncated=true | TR-10 | api | mvp1 |
| EV-UI-19 | edge | a 30-day window whose offset start would need 121 buckets at the aligned size | the axis stays UTC floor-aligned and the grid is never re-anchored, so the next coarser supported rung is selected from [60s,300s,600s,900s,1h,2h,4h,6h,8h,12h,24h] and the count is at most 120, never a 121st bucket and never a split | TR-06 | api | mvp1 |
| EV-UI-20 | security | trace request from a locked case while another tab holds the key | 423; no lane data in the body | AC-11 | api | mvp1 |
| EV-UI-21 | security | tracing response marshalled | no connection strings, no payloads, no raw cookie values | AC-12, TR-13 | api | mvp1 |
| EV-UI-22 | privacy | IIS event with client-ip, referer, cookie:sid | the tracing response never carries those attrs | TR-15, AC-23 | api | mvp1 |
| EV-UI-23 | edge | clock-unchecked source beside a validated one | multi-source chart disabled as a whole with the tz-rule reason; no lane rows plot points | TR-11, AC-06 | e2e | mvp1 |
| EV-UI-24 | edge | all sources clock-unchecked | multi-source chart disabled as a whole with the tz-rule reason; no lane rows plot points | TR-11, AC-06 | e2e | mvp1 |
| EV-UI-25 | edge | reference window fully inside the incident window | one overlap notice only, not one per field | TR-08, AC-04 | e2e | mvp1 |
| EV-UI-26 | edge | reference window ends exactly when the incident starts | no overlap notice | TR-08 | unit | mvp1 |
| EV-UI-27 | a11y | 200% browser zoom at 1440x900 | chart scrolls sideways; no control clipped; names intact | TR-12 | e2e | mvp1 |
| EV-UI-28 | a11y | forced-colours or high-contrast mode | status words and icons remain; colour-only cues absent | D-3, TR-12 | e2e | mvp1 |
| EV-UI-29 | a11y | prefers-reduced-motion set | no zoom or load animation plays | TR-12 | e2e | mvp1 |
| EV-UI-30 | visual-state | dark theme | series colours and axis text meet contrast | TR-12 | visual-state | mvp1 |
| EV-UI-31 | visual-state | light theme | series colours and axis text meet contrast | TR-12 | visual-state | mvp1 |
| EV-UI-32 | copy | tooltip for event.time-taken before VF-01 | wording marks the observed time-taken as separate from the point, never a verified request start and never API processing time | TR-31, VF-01 | manual | mvp1 |
| EV-UI-33 | copy | every tracing string in the deck | copy lint passes; no jargon word without a dictionary entry | TR-22, WP-08 | unit | mvp1 |
| EV-UI-34 | regression | per-call CallTrace tray after Tracing opens (tray is Not wired) | tray unchanged, same chosen calls, same controls | AC-15, TR-12 | manual | mvp1 |
| EV-UI-35 | regression | mixed e2e spec files after WP-62 extraction | exports.spec.ts, keeper-update.spec.ts and manual-tools.spec.ts keep their non-guided tests, no main specs/ file imports openGuided, and help-guide.spec.ts stays in main with its guided-analysis slug and no openGuided | D-2, WP-62, SC-UI-141 | e2e | mvp1 |
| EV-UI-36 | edge | demo case before seeding | an empty demo case shows the empty-case state, not three empty lanes | TR-18, SC-UI-142 | e2e | mvp1 |
| EV-UI-37 | performance | 30-day window over live audit | audit lane returns server-side buckets; client does no cohort summing | TR-04, §5.4 | bench | mvp1 |
| EV-UI-120 | negative | POST /api/cases/{id}/tracing on a closed case | 409 with a plain-language error; no lane data in the body | TR-03 | api | mvp1 |
| EV-UI-121 | resilience | tracing endpoint answers 503 cases-unavailable | plain-language error; Try again offered; chart cleared (SC-UI-123) | TR-13 | e2e | mvp1 |

| EV-UI-39 | edge | a lane has more than 60 qualifying events | longest 60 shown; "more events are hidden" note | TR-04 | e2e | mvp1 |
| EV-UI-40 | edge | bucket clicked with no events | hand-off opens Run analysis for that sub-window and says it holds no calls | TR-09 | e2e | mvp1 |
| EV-UI-100 | boundary | window of exactly 10d | bucket size 7200s (10d inclusive stays on the 2h rung) | TR-06 | unit | mvp1 |
| EV-UI-140 | resilience | live audit aggregation cannot complete within the census budget | banner names the budget or row cap beside the drawn SQL/IIS lanes; the audit lane state is incomplete and complete=false with a reason from {timeout,rowcap,error}; it carries consistency:"live-non-snapshot" and complete:true never implies the source was a stable snapshot, so a trace beside a saved lane sets mixedConsistency:true; it never pages raw rows to build counts and is never silently shortened | TR-04, TR-07 | e2e | mvp1 |
| EV-UI-160 | edge | IIS event with a timestamp and a time-taken value | drawn as a point at its timestamp; time-taken shown separately; no verified request start claimed; time-taken never called API processing time | TR-31, WP-20 | e2e | mvp1 |
| EV-UI-161 | resilience | second Start tracing while one request is in flight | the previous requestSeq is cancelled by its request context; stale response ignored by the client-only per-tab requestSeq; there is no serverWorkId and no subscriber count; another tab is never affected | TR-03, WP-14 | e2e | mvp1 |
| EV-UI-162 | edge | raw lane hits the 50000 row cap; an audit lane server-side $match+$group aggregation exceeds the 60 s CensusQueryBudget | raw event list labelled "first N rows"; the SQL and IIS bucket counts come from the uncapped local case.db GROUP BY aggregate and are exact; a budget-exceeded aggregation makes the live audit lane incomplete and complete=false with a reason from {timeout,rowcap,error} while the SQL and IIS lanes keep their exact local counts | TR-10, WP-18 | api | mvp1 |
| EV-UI-163 | copy | scrub pill on the tracing toolbar in each state | scrub on reads "Export hides IP, referer and cookie hash"; scrub off reads "Export includes IP, referer and cookie hash (scrub is off)"; before a preview it reads "Default: export hides IP, referer and cookie hash"; never says stored data is masked | TR-23, D-9 | e2e | mvp1 |
| EV-UI-164 | visual-state | win32-partitioned IIS series (Later) | labelled "estimate", never presented as measured (WP-24 deferred) | TR-32, WP-24 | manual | later |
| EV-UI-180 | resilience | two tabs, same case key, same window, each sending its own per-tab requestSeq | each tab performs its own bounded live audit read, bounded by MaxAuditConcurrency 2; cancelling one tab cancels only its own request context; there is no serverWorkId, no subscriber count and no shared in-flight read | TR-03, WP-14 | e2e | mvp1 |
| EV-UI-181 | copy | chart drag selection wording beside the privacy scrub pill | the drag control reads "time-range brush"; the privacy pill keeps its export-hiding wording; no naming collision | TR-23, D-9 | e2e | mvp1 |
| EV-UI-200 | resilience | one tab cancels while the other tab runs its own bounded live audit read for the same window | only the cancelling tab's request context is cancelled; the other tab's read continues unaffected; there is no serverWorkId and no shared in-flight read; SC-UI-200 | TR-03, WP-14 | e2e | mvp1 |
| EV-UI-201 | resilience | a request with its own per-tab requestSeq is cancelled or times out | its own read is cancelled and no error banner is shown; no other tab's request is affected, because there is no serverWorkId, no subscriber count and no shared in-flight read; SC-UI-201 | TR-03, WP-14 | e2e | mvp1 |
| EV-UI-220 | boundary | 30-day window starting at 00:01 | a 30-day span is 2592000000 ms; a start at 00:01 makes the aligned count exceed 120, so the axis stays UTC floor-aligned and the grid is never re-anchored, and the next coarser supported rung is selected from [60s,300s,600s,900s,1h,2h,4h,6h,8h,12h,24h]: the 8 h rung with n=91, tiling [start, end) exactly once with at most 120 buckets; only a UTC-aligned 30 d start at 00:00 keeps 6 h (W = span/120 = 21600000 ms) | TR-06, AC-10 | unit | mvp1 |
| EV-UI-221 | resilience | two tabs each request a 30-day audit span over the live audit lane | the live audit lane computes its buckets with one server-side $match+$group aggregation per source under the existing 60 s CensusQueryBudget and the existing broad-scan gate, bounded by MaxAuditConcurrency 2; a timeout or row cap makes the lane state incomplete and complete=false with a reason from {timeout,rowcap,error}; the lane carries consistency:"live-non-snapshot" and the trace sets mixedConsistency:true beside a saved lane; an unsupported span is refused with the limit named and never silently shortened | TR-07, TR-10 | e2e | mvp1 |
| EV-UI-202 | copy | scrub pill before any preview exists | reads "Default: export hides IP, referer and cookie hash"; aria-label equals the sentence and it is announced in a live region; never says stored data is masked; SC-UI-202 | TR-23, WP-10 | e2e | mvp1 |
| EV-UI-260 | resilience | two tabs post the same query at once over the live audit lane | each tab performs its own bounded live audit read bounded by MaxAuditConcurrency 2; neither cancels the other; no serverWorkId, no subscriber count and no shared in-flight read exist; SC-UI-260 | TR-03, WP-14 | e2e | mvp1 |
| EV-UI-280 | boundary | Refresh on a tracing page with a stored SQL lane and a live audit source | three operations are distinct: POST .../tracing may run one ephemeral read-only live read for the audit lane and never persists; GET .../coverage reads case.db only, reports gaps, and never fills them; POST .../pulls is the only operation that reads remote SQL/Mongo and persists events+coverage; SC-UI-280 | TR-26, AC-19 | e2e | mvp2 |
| EV-UI-300 | edge | trace mixing a live audit lane (consistency live-non-snapshot) with a saved SQL lane (consistency stored-as-of) | response sets mixedConsistency:true and the UI shows the non-dismissible note "live and saved lanes are read at different times; totals across lanes are not a conservation check."; complete:true never implies a snapshot; SC-UI-300 | TR-07 | e2e | mvp1 |

#### Wireframe state catalogue

| WS-nn | Screen / component | State | What the wireframe must show | Copy needed | Maps to |
|---|---|---|---|---|---|
| WS-01 | Case home | Empty case, no sources | Case heading, quiet note that nothing is saved yet, Start tracing disabled with reason | "No files have been added to this case yet." plus a disabled reason | TR-01, SC-UI-100 |
| WS-02 | Case home | No sources but files exist | Files listed, Start tracing enabled, hint that a source is chosen next | Start tracing hint | TR-01, SC-UI-120 |
| WS-03 | Case home | One source only | Start tracing enabled; note that other lanes will show missing-source reasons | Missing-lane preview note | TR-07 |
| WS-04 | Case home | Closed case | Closed-case notice; Start tracing visible but disabled | "This case is closed. You can read it, but nothing can be added." | TR-03, AC-08 |
| WS-05 | Case home | Locked case | Key card replaces the home; Start tracing not shown | Key unlock copy | AC-11 |
| WS-06 | Case home | Guided button removed | Investigation block shows Start tracing and no Guided button | Start tracing label | TR-01, D-2 |
| WS-07 | Start tracing | Loading | Skeleton rows for three lanes, bucket label placeholder, controls disabled | "Reading the window…" | TR-03 |
| WS-08 | Start tracing | Sources chosen | Source chips with names, remove affordance, Start tracing enabled | Chosen-sources label | TR-01 |
| WS-09 | Start tracing | Window picker default | Start and end one hour apart, end set to now | "Last hour" default note | TR-02, D-4 |
| WS-10 | Start tracing | 30-day cap message | End field with a plain error when span exceeds 30 days | "The longest window is 30 days." | TR-02, AC-03 |
| WS-11 | Start tracing | Reference window accepted | Reference start and end fields beside the incident window | "Reference window (normal)" | TR-08 |
| WS-12 | Start tracing | Reference overlaps incident | Non-blocking notice under the reference fields; Start tracing still enabled | "The two windows overlap…" | TR-08, AC-04 |
| WS-13 | Start tracing | UTC selected | Toggle shows UTC pressed; all field hints say UTC | "Times are UTC." | TR-05 |
| WS-14 | Start tracing | Local selected | Toggle shows Local with browser zone name; times shifted | Local zone name | TR-05 |
| WS-15 | Tracing chart | Three lanes, all events | Three stacked rows, counts plus time plus status per row, bucket label, axis | Legend for lanes | TR-04, D-1, D-3 |
| WS-16 | Tracing chart | One source | One drawn row, two missing-lane rows with fixed reasons | Fixed missing-source sentence | TR-07, AC-02 |
| WS-17 | Tracing chart | Loading | Lane placeholders with a progress line; controls disabled | "Reading the window…" | TR-03 |
| WS-18 | Tracing chart | Partial lanes | Drawn lanes plus unfinished lanes marked incomplete | "Read hit its limit; events may be missing" | TR-10, StateIncomplete |
| WS-19 | Tracing chart | Missing lane: no data | Row shows the no-data reason, no zero line | "This case has no file for this source" | TR-07, StateNoData |
| WS-20 | Tracing chart | Missing lane: none seen | Row shows the none-seen reason | "Files cover this time and nothing was seen" | TR-07, StateNoneSeen |
| WS-21 | Tracing chart | Missing lane: clock unchecked, single lane or pre-chart only | Row shows the clock-unchecked reason, nothing plotted; a multi-source trace that fails the TZ rule uses WS-25 instead | "Time zone not checked, so nothing is drawn" | TR-11, StateClockUnchecked |
| WS-22 | Tracing chart | Missing lane: out of range | Row shows the out-of-range reason | "Files do not cover this time" | TR-07, StateOutOfRange |
| WS-23 | Tracing chart | Missing lane: no case | Row shows the no-case reason | "Open a case to see this source" | TR-07, StateNoCase |
| WS-24 | Tracing chart | Truncated | Banner plus footnote; counts come from the full aggregate; the event list is capped | "The read reached its limit, so the event list is capped, not the counts." (reuse CALL_TRACE_TEXT.truncated) | TR-10, AC-05 |
| WS-25 | Tracing chart | Timezone not validated for multi-source | Chart disabled with the tz-rule reason | "Time zone not checked, so nothing is drawn" | TR-11, AC-06 |
| WS-26 | Tracing chart | 30-day coarse buckets | Axis labels the actual width shown, W = max(ladderWidth(span), alignedRung(axisStart, axisEnd)) on the UTC floor-aligned, never-re-anchored grid: 6 h at an hour-aligned 30 d at 00:00, a wider aligned rung when a 00:01 start coarsens the rung, with the aligned count at most 120, plus the hourly footnote | the actual width label ("6 hours", "8 hours", ...) plus long-window footnote | TR-16, AC-10 |
| WS-27 | Tracing chart | Hover tooltip | Tooltip panel beside a point with plain-word explanation | Dictionary sentence per id | TR-22 |
| WS-28 | Tracing chart | Bucket selected | Selected bucket outlined, hand-off hint shown | "Open Run analysis for this minute" | TR-09 |
| WS-29 | Tracing chart | Hand-off opened | Run analysis surface for the chosen sub-window; trace still behind it | Sub-window label | TR-09, AC-07 |
| WS-30 | Tracing toolbar | Scrub on | Toolbar with lane toggles all pressed and scrub pill naming the hidden export fields | "Export hides IP, referer and cookie hash" | TR-23, D-9 |
| WS-31 | Tracing toolbar | Scrub off | Scrub pill naming what the export includes when scrubbing is off | "Export includes IP, referer and cookie hash (scrub is off)" | TR-23, D-9 |
| WS-32 | Tracing toolbar | Time-range brush | Time-range brush marking the chosen span on the axis (renamed from "Scrub dragging", which collided with privacy scrub) | Selected-span readout | TR-23 |
| WS-33 | Tracing chart | Demo mode | Seeded three-lane chart, demo sticker, bucket label | "Demo data" sticker | TR-18, AC-09 |
| WS-34 | Tracing page | Closed case | Start tracing disabled; read-only notice; no controls active | Closed-case notice | TR-03, AC-08 |
| WS-35 | Tracing page | Locked key | Key card or locked notice; no lane data | "The key is locked…" | AC-11 |
| WS-36 | Tracing page | Error 400 | Inline error naming window or bucket size; Try again | "The window or bucket size is not allowed." | TR-13 |
| WS-37 | Tracing page | Error 423 | Inline error that the case key is locked | "Unlock the case key to trace." | AC-11 |
| WS-38 | Tracing page | Error 5xx | Inline error plus Try again; chart cleared | "The case could not be read right now." | TR-13 |
| WS-39 | Tracing toolbar | Scrub pill from export default | Toolbar scrub pill stating the default export state before any preview, not an actual choice (no export preview panel in MVP-1) | "Default: export hides IP, referer and cookie hash" | D-9, TR-21 |
| WS-40 | Tracing chart | Narrow width | Chart scrolls sideways; controls wrap; names intact | No new copy | TR-12 |
| WS-41 | Tracing chart | Reduced motion | Same chart with no animation | No new copy | TR-12 |
| WS-42 | Tracing chart | One bucket, one point | Single bucket drawn centred with label | Bucket label | TR-06 |
| WS-100 | Tracing chart | Live audit aggregation over budget | Banner names the census budget or row cap that stopped the live aggregation beside the drawn SQL/IIS lanes; the audit lane state is incomplete and complete=false with a reason from {timeout,rowcap,error}; it never pages raw rows to build counts, never silently shortens, and is a live aggregate, not a snapshot | "The audit read reached its time or row limit; nothing was shortened." (proposed) | TR-04, TR-07, WP-17 |
| WS-101 | Tracing toolbar | Percentile selector | p50/p95/p99 selector with the chosen value pressed, each a real button with aria-pressed, and an approximate label with bounds where used | "Approximate" label | TR-06, TR-23, SC-UI-140 |
| WS-140 | Tracing page | Error 503 cases-unavailable | Plain-language error, Try again offered, chart cleared; keeps its own 503 wording distinct from the generic 5xx WS-38 | "The case store is unavailable right now." (proposed) | TR-13, SC-UI-123 |
| WS-160 | Tracing chart | IIS event point | One IIS event drawn as a point at its timestamp with time-taken shown separately and an unverified-start note | "Time taken is shown separately; the request start is not verified" (proposed) | TR-31, WP-20 |
| WS-161 | Tracing page | Trace in flight | Spinner and Cancel on the Start tracing control; a second Start cancels the previous requestSeq by its request context and the stale answer is ignored; there is no serverWorkId and no subscriber count | "Tracing..." and "Cancel" | TR-03, WP-14 |
| WS-162 | Tracing chart | Raw lane capped | Raw event list labelled "first N rows"; counts come from the full aggregate and stay exact; audit aggregate labelled partial only when its aggregate cannot complete | "first N rows" / "partial" (proposed) | TR-10, WP-18 |
| WS-220 | Tracing chart | Audit aggregation over budget | Banner names the census budget or row cap that stopped the live aggregation beside the drawn SQL/IIS lanes; the audit lane state is incomplete and complete=false with a reason from {timeout,rowcap,error}; it never pages raw rows to build counts and is a live aggregate, not a snapshot | "The audit read reached its time or row limit; nothing was shortened." (proposed) | TR-04, TR-07, WP-17 |
| WS-221 | Tracing chart | Live audit result beside a stored result | Every lane carries origin in {live, case}, asOf, complete, truncated, consistency in {live-non-snapshot, stored-as-of, snapshot} and reason; the top-level allLanesStored exists only on POST .../tracing responses and is true iff every lane has origin:"case"; a live audit read makes it false, so the audit row is a live read while the SQL and IIS rows are stored; when any lane in the trace is live-non-snapshot the response sets mixedConsistency:true and the UI shows a non-dismissible note: "live and saved lanes are read at different times; totals across lanes are not a conservation check." | "Live read" / "Stored" (proposed) plus the mixed-consistency note | TR-33, WP-14 |
| WS-260 | Tracing page | Two tabs, same query | Each tab shows its own spinner and Cancel; the two reads are independent with no shared in-flight read; cancelling one leaves the other running | "Tracing..." and "Cancel" | TR-03, WP-14 |
| WS-200 | Tracing toolbar | Scrub pill before any preview | Toolbar scrub pill showing the default state before a preview exists | "Default: export hides IP, referer and cookie hash" | D-9, TR-21, SC-UI-202 |
| WS-280 | Tracing chart | 30-day coarse bucket label showing the actual width | The bucket-size label comes from W = max(ladderWidth(span), alignedRung(axisStart, axisEnd)), so an hour-aligned 30 d at 00:00 shows 6 h and a 00:01 start coarsens to a wider aligned rung, with the aligned count at most 120 | the actual width label ("6 hours", "8 hours", ...) | TR-16, AC-10, SC-UI-220 |

#### Open points
- The trace response's `bucketSeconds` and lane payload come from a new `POST /api/cases/{id}/tracing`; the wireframe copy for each of the six lane states should reuse `LANE_STATE_TEXT` in `web/src/lib/copy/callTraceCopy.ts` rather than inventing new sentences (proposed to extend that table).
- `TracingPaper.svelte`, `TraceToolbar.svelte`, `TracingChart.svelte`, `web/src/lib/tracing/windows.ts` and `web/src/lib/copy/dictionary.ts` do not exist in this checkout; all are new per section 6 and WP-08/16/23.
- The case-home Guided removal (WP-64) and guided e2e archiving (WP-62) are decided (D-2) but not yet implemented; `CaseHome.svelte` still mounts `GuidedAnalysis` and holds the Guided button; guided specs still live under `tools/e2e/specs/`.
- The exact 400/423/503 error strings are server-defined; the copy above paraphrases the pinned status codes only.
- Whether a 30-day live audit read is served today is unverified (`internal/audit/query.go:76-77` caps the old interval); the wireframe should include the "audit store cannot serve this span" state if VF confirms it.
- The W3C `time` = end semantics (VF-01b) is unverified, so `gap.time-end` must stay visible until confirmed.
- A 1440x900 and a narrow (480px) width are the two layout states the brief names; the grid breakpoint is unverified until the chart exists.

### Area D: MVP-2 and MVP-3 behaviours and performance (SC-ADV, EV-ADV)

Scope: TR-14, TR-17, TR-24..TR-28; spec §4.3–4.6 (coverage/hydration/rollups DDL + migration), §5.3 (`GET /api/cases/{id}/coverage`), Appendix P packets P6..P12; acceptance AC-18..AC-22; budgets BM-01..BM-12. Verified code base: `internal/casestore/sqlite/schema.go:15` (`const schemaVersion = 4`), `internal/casestore/sqlite/schema.go:19` (`refusesSchema(v, known) bool { return v > known }`), `internal/casestore/sqlite/discover.go:31-58` (`(*Case).migrate` one tx), `internal/audit/rollup.go:27-38` (`MaxRollupGroups=600000`, `RollupNoiseCount=5`, `DefaultRollupSlowMs=1000`), `internal/casestore/series.go:25` (`MaxSeriesBuckets=2000`), `internal/audit/histogram.go:24,49` (`BinIndex`/`BinBounds`), `internal/calltrace/types.go:14-27` (lane keys + states), `internal/calltrace/lanes.go:42-53` (`LaneOf`), `internal/casestore/sqlite/case.go:565` (`insertEvent`), `internal/casestore/sqlite/case.go:571-777` (`Import` one serializable tx), `internal/dbhealth/lane_mongo.go:10-11,207` (profiler `-1`), `internal/importer/all/all.go:40-43` (`NewXE`,`NewErrorlog`,`NewPDH`,`NewQuerystore`), `internal/resource/metrics.go:17-25` (`HostCPU` etc.), `internal/investigate/types.go` + `internal/investigate/catalog.go` (no `AgentMatch`/`AgentTier` present), `internal/perfbench/doc.go` (`UNRAVIS_BENCH=1` gate). Migration/downgrade: spec §4.6 says bump to 5; the older-build refusal is stated (not "(proposed)") because `refusesSchema` + `ErrNewerSchema` already implement it (`schema.go:19`, `errors.go:16`).

#### F-ADV-01 Coverage ledger records every read window  (TR-24, WP-28)

```gherkin
Feature: Coverage ledger inside case.db
  Background:
    Given a case database at schema version 4
    When the case is opened by a build that knows schema version 5
    Then the case is migrated to version 5 in one transaction
    And the tables coverage, import_attempts, hydration and rollups exist

  @SC-ADV-01 @TR-33 @mvp2 @unit
  Scenario: A full page read writes one complete coverage row
    Given a SQL pull of 1000 rows that all arrive
    When the pull is recorded inside the import transaction
    Then the coverage table holds one row whose source is the immutable case_sources.id for the sql-xe source with complete=1, and coverage.source is TEXT NOT NULL REFERENCES case_sources(id) ON DELETE CASCADE
    And rows_read is 1000 and pages_read is 1

  @SC-ADV-02 @TR-33 @mvp2 @unit
  Scenario: A partial pull records each page and marks the read incomplete
    Given a SQL pull whose second page of 3 pages is never returned
    When the pull is recorded
    Then the coverage table holds 2 rows for that source with complete=0
    And the note says a plain-language statement that says the source stopped early

  @SC-ADV-03 @TR-33 @mvp2 @unit
  Scenario: Deleting an import cascades its coverage rows
    Given an import with 2 coverage rows
    When the import row is deleted
    Then the coverage table holds no row for that import_id

  @SC-ADV-04 @TR-33 @mvp2 @api
  Scenario: Coverage is served from the case, not the live source
    Given a case with one coverage row for source "sql-querystore"
    When the client sends GET /api/cases/{id}/coverage
    Then the response is 200 with {entries:[...], asOf, gaps:[...], provenance:"case-ledger"} holding one ledger element whose source is "sql-querystore" and never allLanesStored
    And no live database connection is opened

  @SC-ADV-05 @TR-33 @mvp2 @api
  Scenario: A case with no coverage rows returns an empty ledger with a state
    Given a case whose coverage table is empty
    When the client sends GET /api/cases/{id}/coverage
    Then the response is 200 with {entries:[], asOf, gaps:[...], provenance:"case-ledger"} carrying state:"no-coverage"
    And the response carries a state that says no coverage rows are recorded
    And it is never conflated with an unknown case

  @SC-ADV-141 @TR-33 @mvp2 @api
  Scenario: Coverage on a closed case answers 409
    Given a closed case
    When the client sends GET /api/cases/{id}/coverage
    Then the response status is 409 with a plain case-closed message

  @SC-ADV-103 @TR-33 @mvp2 @unit
  Scenario: A coverage note never keeps a connection string
    Given a pull whose source returns its connection string inside the error text
    When the coverage row is written
    Then the stored note is plain words with no host, user or password
    And the note names the source and the read window only

  @SC-ADV-220 @TR-33 @mvp2 @unit
  Scenario: Deleting a source cascades its coverage and keeps its attempt tombstone
    Given a source with coverage rows and an import_attempts row carrying source_label
    When the source row is deleted
    Then coverage.source is TEXT NOT NULL REFERENCES case_sources(id) ON DELETE CASCADE so its coverage rows are gone
    And the import_attempts row survives with no FK and keeps its source_label
    And rows older than MaxAttemptRetention 30 days are pruned when the case is opened
```

#### F-ADV-02 Lazy hydration flags  (TR-25, WP-29)

```gherkin
Feature: Lazy hydration flags per import
  @SC-ADV-06 @TR-25 @mvp2 @unit
  Scenario: A declared payload marks the field present
    Given an import that declared a payload column
    When the import transaction commits
    Then the hydration table holds (import_id, "payload", present=1) with the byte count

  @SC-ADV-07 @TR-25 @mvp2 @unit
  Scenario: A field not listed counts as absent
    Given an import that declared no headers
    When the reader asks for the headers of that import
    Then the answer is absent
    And no bytes are read from case.db

  @SC-ADV-08 @TR-25 @mvp2 @unit
  Scenario: Hydrating reads only from case.db
    Given an import with present=1 for "payload"
    When the reader hydrates the payload
    Then the bytes come from the case database
    And no network call is made

  @SC-ADV-09 @TR-25 @mvp2 @unit
  Scenario: Deleting the import removes its hydration flags
    Given an import with a hydration row
    When the import row is deleted
    Then the hydration table holds no row for that import_id

  @SC-ADV-100 @TR-25 @mvp2 @unit
  Scenario: present=0 is read as absent and reads no bytes
    Given a hydration row (import_id, "payload", present=0)
    When the reader asks for the payload of that import
    Then the answer is absent
    And no bytes are read from case.db
```

#### F-ADV-03 Rollups: exact counts and approximate percentiles  (TR-24, WP-30, WP-31, WP-50, WP-51)

```gherkin
Feature: Case-event rollups for zoom
  Background:
    Given imported events of one source in one UTC day

  @SC-ADV-10 @AC-20 @TR-34 @mvp2 @unit
  Scenario: Rollup count sum min and max are bit-exact against a raw scan
    Given 1000 events with durations from a fixed seed
    When the 60-second rollup is built at import end
    Then every bucket count_all, count_with_duration, sum_us, min_us and max_us equals the raw Series scan exactly

  @SC-ADV-11 @AC-21 @TR-34 @mvp2 @unit
  Scenario: Approximate p95 always carries bounds and the approximate label
    Given an import whose durations fill several histogram bins
    When the rollup bins are merged to read p95
    Then the value is shown with a lower bound and an upper bound
    And the response marks the percentile approximate

  @SC-ADV-12 @TR-34 @AC-20 @mvp2 @unit
  Scenario Outline: Exact percentiles are not claimed from rollups
    Given a rollup for <source> at bucket size <bucketMs> ms
    When the reader returns p50 and p95
    Then each is derived by merging the per-rollup histogram bins, flagged approximate and paired with BinBounds

    Examples:
      | source | bucketMs |
      | sql-xe | 60000    |
      | sql-xe | 3600000  |
      | iis-w3c | 60000   |

  @SC-ADV-13 @TR-34 @mvp2 @unit
  Scenario: Building rollups is idempotent across a repeated import
    Given a rollup already holds a bucket for source "sql-xe" at 60000 ms
    When the same events are imported again
    Then the bucket is updated in place and not doubled

  @SC-ADV-14 @TR-34 @mvp2 @bench
  Scenario: Two rollup levels are written at import end
    Given 1 day of events for one source
    When the import commits
    Then WP-30 writes bucket_ms 60000 and WP-31 writes and verifies bucket_ms 3600000

  @SC-ADV-15 @TR-34 @mvp2 @unit
  Scenario: Rollup build stays inside the import transaction
    Given the import transaction is rolled back by a simulated failure
    When the case is reopened
    Then the rollups table holds no row for the failed import

  @SC-ADV-280 @TR-34 @AC-20 @mvp2 @unit
  Scenario: An invalid duration keeps its event with no duration and durationStatus invalid
    Given an event carrying a non-finite, negative or greater than maxInt64 duration
    When the import transaction commits
    Then a non-finite, negative or greater than maxInt64 duration keeps the event with no duration and durationStatus:"invalid"
    And it increments count_all and not count_with_duration
    And no invalid float is ever serialized, and only records that cannot be safely identified or that violate envelope integrity are quarantined
```

#### F-ADV-04 Rollup-backed zoom with raw fallback  (TR-24, WP-32, WP-33, WH resolution)

```gherkin
Feature: Zoom prefers rollups and falls back to a raw scan
  @SC-ADV-16 @AC-18 @TR-34 @mvp2 @unit
  Scenario: An aligned zoom window reads rollups only and issues no raw scan
    Given complete 60-second and 3600-second rollups for the aligned 1-hour window 10:00 to 16:00
    When the chart asks for 60 buckets
    Then the series comes from the rollups table only
    And hasRawEdge is false and no events table scan is issued

  @SC-ADV-17 @AC-18 @TR-34 @mvp2 @unit
  Scenario: An unaligned zoom window scans only its clipped edges from raw events
    Given complete 3600-second rollups and a window that starts at 10:07 and ends at 16:07
    When the chart asks for 6-hour presentation buckets
    Then hasRawEdge is true and each clipped edge is served from the bounded raw scan; each boundary fragment is filled from a bounded raw scan, at most two scans, one shared query when both fragments lie in the same grain-hour
    And whole contained UTC-hour sub-buckets are filled from bucket_ms=3600000 rows
    And the response records the mode rollup+edge

  @SC-ADV-18 @AC-18 @TR-34 @mvp2 @api
  Scenario: Clipped and complete are reported independently
    Given a rollup zoom window with a geometric edge and one missing hour row
    When the trace is requested
    Then the geometric edge bucket carries clipped=true
    And the bucket with missing data carries complete=false
    And the two flags are never interchanged

  @SC-ADV-19 @TR-34 @mvp2 @unit
  Scenario: A query over the series bucket cap is refused
    Given a query whose bucket count is 2001
    When the rollup series is requested
    Then the query is refused with a plain too-many-buckets error
```

#### F-ADV-12 Trace bucket ladder boundaries  (TR-06, TR-16, WP-15) (proposed: WP-15)

```gherkin
Feature: Trace bucket ladder boundaries
  @SC-ADV-120 @TR-06 @TR-16 @AC-10 @mvp1 @unit
  Scenario: Every ladder rung includes its upper bound
    Given incident windows of exactly 2 h, exactly 24 h, exactly 5 d, exactly 10 d, exactly 20 d and exactly 30 d
    When each trace bucket size is computed
    Then the span-nominal rung gives 60 s at 2 h and 900 s at 24 h, and the presented width is W = max(ladderWidth(span), alignedRung(axisStart, axisEnd)) with the aligned count always at most 120
    And 5 d gives 3600 s, 10 d gives 7200 s, 20 d gives 14400 s and 30 d gives 21600 s when hour-aligned at 00:00 (a 00:01 start coarsens to the 8 h rung, 28800 s, with n=91, see EV-UI-220)
    And one second beyond each rung moves to the next rung
    And every case stays at most 120 buckets
    And the axis stays UTC floor-aligned and the grid is never re-anchored; when the aligned count would exceed 120 the next coarser supported rung is selected from [60s,300s,600s,900s,1h,2h,4h,6h,8h,12h,24h], so the count is at most 120 and the number of boundary raw scans is zero when neither edge is set, one when exactly one edge is set or when both boundaries fall inside the same grain-hour (one bounded raw query serves both), and two otherwise
```

#### F-ADV-05 Live SQL and Mongo pulls saved into case.db  (TR-26, WP-26, P6)

```gherkin
Feature: User-initiated live pulls persist into case.db
  Background:
    Given an open case and a read-only database connection

  @SC-ADV-20 @AC-19 @TR-26 @mvp2 @api
  Scenario: A SQL pull is saved as normal event rows
    Given a SQL probe that returns 25 window rows
    When the user starts a pull of a 1-hour window
    Then the case holds 25 new events whose source is "sql-xe"
    And the import row status is "ok"

  @SC-ADV-21 @AC-19 @TR-26 @mvp2 @api
  Scenario: A saved pull feeds the trace SQL lane
    Given a saved SQL pull in the trace window
    When the client requests the trace SQL lane
    Then the lane state is events
    And the lane counts include the saved pull rows
    And three operations are distinct: POST .../tracing may run one ephemeral read-only live read for the audit lane and never persists; GET .../coverage reads case.db only, reports gaps, and never fills them; POST .../pulls is the only operation that reads remote SQL/Mongo and persists events+coverage
    And each lane carries readKind in {saved, live} and separate complete/asOf fields, and GET .../coverage opens no source connection even for an uncovered window
    And a pull is idempotent on observed content, computed as a canonical row-content digest (lowercase-hex SHA-256 over a versioned, deterministic serialization of the ordered observed rows, one line per row, in the source's observed order tie-broken by _id, each field length-prefixed and type-tagged including explicit nulls, with content_digest_v recording the canonicalization version starting 1); a repeated pull with byte-identical canonical row content is a no-op (StatusDuplicate, outcome unchanged), and a re-pull that observes any changed canonical content supersedes the prior observation for the identical (source, lane, fingerprint, window) scope (outcome changed) and a partial-overlap window is refused 409 window-overlap, and coverage means 'completely read as of asOf', not 'current'

  @SC-ADV-140 @AC-19 @TR-26 @mvp2 @api
  Scenario: A Mongo pull is saved as normal event rows
    Given a Mongo probe that returns 25 window rows
    When the user starts a pull of a 1-hour window
    Then the case holds 25 new events whose source is "mongo-xe"
    And the import row status is "ok"
    And the saved rows feed the audit lane like a saved SQL pull

  @SC-ADV-22 @TR-26 @mvp2 @unit
  Scenario: Pumping the same window twice writes no second copy
    Given a completed pull of a window
    When the same window is pulled again
    Then a pull is idempotent on observed content, computed as a canonical row-content digest (lowercase-hex SHA-256 over a versioned, deterministic serialization of the ordered observed rows, one line per row, in the source's observed order, each field length-prefixed and type-tagged including explicit nulls); a repeated pull with byte-identical canonical row content is a no-op (StatusDuplicate, outcome unchanged)
    And the case event count is unchanged

  @SC-ADV-23 @TR-26 @mvp2 @api
  Scenario Outline: Pull size and time caps are enforced
    Given a source with <rows> rows in the window
    When the pull of <minutes> minutes runs
    Then the outcome is <outcome>

    Examples:
      | rows  | minutes | outcome                        |
      | 1000  | 60      | saved with status ok           |
      | 50001 | 60      | refused with a plain cap error |
      | 1000  | 43201   | refused past the 30-day cap    |

  @SC-ADV-24 @TR-26 @mvp2 @api
  Scenario: A slow source does not block the case open
    Given a source that answers each page after 2 seconds
    When a pull is started
    Then the case remains open and readable
    And the pull records partial coverage with complete=0

  @SC-ADV-25 @TR-26 @AC-22 @mvp2 @e2e
  Scenario: A source that drops mid-pull leaves no partial events
    Given a source that closes the connection after page 1
    When the pull runs
    Then the import rolls back whole
    And the case holds no events and no coverage row for that attempt
    And the API error is plain language that the source stopped early
```

#### F-ADV-06 Crash safety of imports and pulls  (AC-22, BM-12, WP-57)

```gherkin
Feature: Crash safety of case.db imports
  @SC-ADV-26 @AC-22 @BM-12 @mvp2 @unit
  Scenario: A kill mid-import leaves no partial rows
    Given an import paused after 150 of 200 events
    When the case folder is copied at that instant and reopened
    Then the case holds only the previously finished import
    And no event from the interrupted import is present

  @SC-ADV-27 @AC-22 @BM-12 @mvp2 @unit
  Scenario: The interrupted file can be imported whole afterwards
    Given a case recovered from a mid-import kill
    When the same file is imported again
    Then the import succeeds
    And the event count is the pre-crash count plus all rows of the file

  @SC-ADV-28 @BM-12 @mvp2 @bench
  Scenario: Batched inserts keep import atomicity
    Given the batched INSERT path writes 500 rows per statement
    When the transaction is aborted at batch 3
    Then the case is byte-for-byte the state before the import
```

#### F-ADV-07 Layer 4: Mongo app DB and host metrics lane  (TR-27, WP-39, WP-40, P11)

```gherkin
Feature: Layer 4 host metrics and Mongo application DB lane
  @SC-ADV-29 @TR-27 @mvp3 @unit
  Scenario: A host-metrics source maps to the host lane (proposed: `LaneHost` ships in WP-39; no host-metrics prefix exists in `all.go` today)
    Given a case holding a source whose importer is a host metrics source
    When the lanes are built
    Then the lane key is the host lane (proposed)
    And its label is a plain phrase

  @SC-ADV-30 @TR-27 @mvp3 @unit
  Scenario: A Mongo application-DB document maps to the mongo lane
    Given a case holding a source whose importer is "mongo-profile"
    When the lanes are built
    Then the lane key is the mongo lane

  @SC-ADV-31 @TR-27 @mvp3 @api
  Scenario: Layer 4 events appear on the trace
    Given a case with a host metrics import in the window
    When the trace is requested
    Then the host lane state is events (proposed: WP-39/P11 adds host-metrics importers and LaneHost; neither exists in `all.go` or `lanes.go` today)
    And the lane events carry the host metric labels (proposed: WP-39/P11)

  @SC-ADV-32 @TR-27 @mvp3 @unit
  Scenario: Layer 4 imports obey the same import transaction
    Given a host metrics file
    When it is imported
    Then the events and the coverage and rollup rows commit together
```

#### F-ADV-08 Three-tier implementing-agent matcher  (TR-28, WP-43, WP-44, P12)

```gherkin
Feature: Three-tier implementing-agent matcher
  Background:
    Given an investigation Input with an incident window and a reference window

  @SC-ADV-33 @TR-28 @mvp3 @unit
  Scenario: Tier 1 matches a clock or profiler gap
    Given a lane whose state is clock-unchecked
    When MatchAgents runs
    Then the result holds one match with tier 1
    And the reason names the unchecked clock rule
    And the evidence lists that lane

  @SC-ADV-34 @TR-28 @mvp3 @unit
  Scenario: Tier 2 matches a p95 spike with a co-located SQL event
    Given a rollup p95 spike and one SQL lane event in the same window
    And no IIS error in that window
    When MatchAgents runs
    Then the result holds one match with tier 2

  @SC-ADV-35 @TR-28 @mvp3 @unit
  Scenario: Tier 3 matches an IIS 5xx uniquely joined to a clean SQL call
    Given one IIS 5xx event that joins to one audit call
    And that call's SQL lane is clean
    When MatchAgents runs
    Then the result holds one match with tier 3

  @SC-ADV-36 @TR-28 @mvp3 @unit
  Scenario: An ambiguous join never claims Tier 3
    Given an IIS 5xx event that joins to two audit calls
    When MatchAgents runs
    Then no Tier 3 match is returned

  @SC-ADV-37 @TR-28 @mvp3 @unit
  Scenario: No tier is invented when nothing matches
    Given an Input with no clock gap, no spike and no joinable error
    When MatchAgents runs
    Then the result is empty

  @SC-ADV-38 @TR-28 @mvp3 @unit
  Scenario Outline: The matcher never claims causation
    Given the fixed match fixture for tier <tier> with reason field <reason>
    When MatchAgents runs
    Then its reason passes evidence.LintCausal

    Examples:
      | tier | reason |
      | 1 | clock-unchecked |
      | 2 | p95-spike |
      | 3 | iis-5xx-join |

  @SC-ADV-39 @TR-28 @mvp3 @api
  Scenario: The investigation response carries ranked matches
    Given an investigation Input that matches Tier 1 and Tier 2
    When the investigation endpoint runs
    Then the JSON holds an agentMatches array (proposed: WP-44; `internal/investigate` has no `MatchAgents` yet) sorted by score descending
    And each entry has tier, reason and evidence
```

#### F-ADV-09 Migration and downgrade safety  (spec §4.6; TR-24)

```gherkin
Feature: Schema migration and downgrade refusal
  @SC-ADV-40 @TR-24 @mvp2 @unit
  Scenario: A version 4 case gains the four tables on open
    Given a case database whose meta schema_version is "4"
    When a build that knows version 5 opens it
    Then meta schema_version becomes "5"
    And coverage, import_attempts, hydration and rollups exist
    And the existing tables and rows are unchanged

  @SC-ADV-41 @TR-24 @mvp2 @unit
  Scenario: A version 5 case is refused by an older build
    Given a case database whose meta schema_version is "5"
    When a build that knows only version 4 opens it
    Then Open fails with ErrNewerSchema
    And the message is a plain statement that the case was written by a newer version

  @SC-ADV-42 @TR-24 @mvp2 @unit
  Scenario: A version 3 case migrates straight to version 5
    Given a case database whose meta schema_version is "3"
    When the current build opens it
    Then meta schema_version becomes "5"
    And all four new tables exist
    And the migration ran in one transaction

  @SC-ADV-43 @TR-24 @mvp2 @unit
  Scenario: A failed migration leaves the version unchanged
    Given a version 4 case where the coverage DDL is made to fail
    When the build opens it
    Then the open fails
    And meta schema_version remains "4"

  @SC-ADV-102 @TR-24 @mvp2 @unit
  Scenario: A disk-full failure mid-DDL rolls the whole migration back
    Given a version 4 case on a volume that runs out of space after the first new table
    When the build opens it
    Then the open fails with a plain disk error
    And meta schema_version remains "4"
    And none of coverage, hydration or rollups exists
```

#### F-ADV-10 Persisted trace window and audit route filter (Later)  (TR-14, TR-17; D-11, D-12)

```gherkin
Feature: Persisted trace window and route filter
  @SC-ADV-44 @TR-17 @later @api
  Scenario: The trace window is persisted per case
    Given the user closes a 6-hour trace window on a case
    When the case is reopened
    Then the trace opens on the same window
    And the window is stored in case.db and not on the server disk

  @SC-ADV-45 @TR-14 @later @api
  Scenario: A route filter narrows the trace
    Given a trace request with route "Orders.Detail"
    When the trace runs
    Then only audit events for that route are returned
    And the SQL and IIS lanes are unchanged

  @SC-ADV-46 @TR-14 @later @unit
  Scenario: An empty route filter keeps every route
    Given a trace request with an empty route
    When the trace runs
    Then every route is returned
```

#### F-ADV-11 Performance budgets as @bench  (@BM budgets)

```gherkin
Feature: Performance budgets measured under UNRAVIS_BENCH
  @SC-ADV-47 @BM-01 @mvp2 @bench
  Scenario: The generator emits skewed heavy-tailed events deterministically
    Given UNRAVIS_BENCH=1 and a fixed seed
    When the generator runs
    Then it emits between 100000 and 5000000 events
    And two runs with the same seed produce identical distributions

  @SC-ADV-48 @BM-02 @mvp2 @bench
  Scenario: Narrow-row ingest holds the batched budget
    Given UNRAVIS_BENCH=1 and 1000000 narrow rows
    When the batched import runs
    Then reported throughput is at least 100000 rows per second
    And the counter-style path reports at least 20000 events per second

  @SC-ADV-49 @BM-03 @mvp2 @bench
  Scenario: Rollup build cost and size overhead are reported
    Given UNRAVIS_BENCH=1 and 1000000 events
    When rollups are built at import end
    Then the run reports build milliseconds and bytes per table
    And the rollup bytes stay within the guard multiplier of the event bytes

  @SC-ADV-50 @AC-18 @BM-04 @mvp2 @bench
  Scenario: Rollup-backed zoom p95 stays under 15 ms
    Given UNRAVIS_BENCH=1 and a case with 1000000 events and complete rollups
    When 50 zoom queries run over 120 buckets
    Then the rollup-path p95 is under 15 ms
    And the raw path is either published slower or refused

  @SC-ADV-51 @BM-05 @mvp2 @bench
  Scenario: Rollup merges stay bit-exact
    Given UNRAVIS_BENCH=1 and many event distributions
    When rollups are merged against a raw scan
    Then count, sum, min and max are bit-exact

  @SC-ADV-52 @BM-06 @mvp2 @bench
  Scenario: Sketch p95 and p99 stay inside their reported bounds
    Given UNRAVIS_BENCH=1 and several latency distributions
    When the histogram percentile is computed
    Then the exact nearest-rank value lies within the reported lower and upper bounds

  @SC-ADV-53a @BM-07 @mvp2 @bench
  Scenario: Warm reopen stays under 100 ms
    Given UNRAVIS_BENCH=1 and deferred verify enabled
    When the case is reopened warm
    Then reopen time is under 100 ms
    And the number is informational and not a CI gate

  @SC-ADV-53b @BM-07 @mvp2 @bench
  Scenario: Cold reopen stays under 300 ms
    Given UNRAVIS_BENCH=1 and deferred verify enabled
    When the same case is reopened with a cold cache
    Then reopen time is under 300 ms
    And the number is informational and not a CI gate

  @SC-ADV-54 @BM-08 @mvp2 @bench
  Scenario: WAL peak stays within twice the on-disk data
    Given UNRAVIS_BENCH=1 and a large import
    When the import runs
    Then peak directory bytes stay within 2 times the final data bytes

  @SC-ADV-55 @BM-09 @mvp2 @bench
  Scenario: Concurrent readers see the last commit
    Given UNRAVIS_BENCH=1 and four open readers
    When an import commits
    Then each reader sees the newly committed rows and never a partial import

  @SC-ADV-56 @BM-10 @mvp2 @bench
  Scenario: Windows cold-cache and DPAPI unwrap time is measured
    Given GOOS is windows and UNRAVIS_BENCH=1
    When the encrypted and plain cases are opened cold
    Then the run reports both times
    And the test skips with a reason on other platforms

  @SC-ADV-57 @BM-11 @mvp2 @bench
  Scenario: The CI checker compares against the stored baseline
    Given UNRAVIS_BENCH=1 and a stored JSON baseline
    When the 100000-event run completes
    Then the relative p95 is compared and passes when at most 1.5 times the baseline

  @SC-ADV-58 @AC-22 @BM-12 @mvp2 @bench
  Scenario: Crash-safety micro-bench recomputes after batching
    Given UNRAVIS_BENCH=1 and batched inserts enabled
    When the import is killed mid-way and reopened
    Then the case holds only complete imports
    And no partial rows are present

  @SC-ADV-101 @BM-04 @mvp2 @bench
  Scenario: The 15 ms zoom assertion is skipped outside the bench gate
    Given UNRAVIS_BENCH is unset
    When the rollup-backed zoom test runs on this CI host
    Then the 15 ms p95 assertion is skipped as informational on this hardware
    And the run reports the measured p95 without failing the build

  @SC-ADV-160 @TR-33 @mvp2 @api
  Scenario: An empty coverage ledger answers an empty array with a state
    Given a case whose coverage table is empty
    When the client sends GET /api/cases/{id}/coverage
    Then the response is 200 with {entries:[], asOf, gaps:[...], provenance:"case-ledger"} carrying state:"no-coverage"
    And the response carries a state that says no coverage rows are recorded
    And it is never conflated with an unknown case

  @SC-ADV-161 @TR-33 @WP-57 @mvp2 @unit
  Scenario: A failed attempt is recorded outside the import transaction
    Given a pull whose import transaction rolls back whole
    When the attempt finishes
    Then an import_attempts row (proposed: source, window, started_at, outcome, error text, pages_ok) is written outside the import transaction
    And the coverage API shows "last attempt failed" from that row
    And the attempt row never counts as coverage

  @SC-ADV-162 @TR-34 @WP-30 @mvp2 @unit
  Scenario: Rollups are rebuilt from all active events without double counting
    Given overlapping or repeated file imports, or an exact-window replacement pull, over the same buckets (a partly overlapping pull is refused 409 window-overlap per SC-ADV-300)
    When each import ends or an import is deleted
    Then the affected buckets are rebuilt transactionally from all active events (events of imports not superseded or deleted), same transaction as the supersession, matching WP-30 and EV-ADV-163
    And no bucket is double counted
    And percentiles come from merged histogram bins, never from averaging percentiles
    And empty bins yield null

  @SC-ADV-163 @TR-03 @WP-14 @mvp1 @api
  Scenario: The tracing request rejects unknown fields before any connection
    Given a tracing request whose JSON holds an unknown field
    When the request is validated
    Then the response is 400
    And sourceIds are de-duplicated and at most 50 (proposed)
    And string and body size caps are enforced
    And validation happens before any live connection opens

  @SC-ADV-164 @TR-24 @WP-27 @mvp2 @unit
  Scenario: Migration verifies shape and version in one transaction
    Given a case at schema version 4
    When the migration to version 5 runs
    Then it verifies table shape and version inside one transaction
    And PRAGMA foreign_keys=ON is set per connection and foreign_key_check runs on open
    And a failed migration rolls back and a newer-schema file is refused

  @SC-ADV-165 @TR-34 @WP-30 @mvp2 @unit
  Scenario: Stored percentiles are merged from bins, never stored as point estimates
    Given a rollup whose durations fill several log-histogram bins
    When p50 and p99 are read
    Then the values are derived by merging the per-rollup bins
    And no stored p50/p95 point estimate is returned
    And an empty bin yields null

  @SC-ADV-180 @TR-34 @AC-18 @mvp2 @unit
  Scenario: Rollup zoom intersects presentation buckets with the window and fills edges from raw
    Given complete 60-second and 3600-second rollups for a window that starts at 10:07
    When the chart requests 6-hour presentation buckets from the rollup path
    Then axisStart is floorUTC(start, 3600000) and axisEnd is ceilUTC(end, 3600000)
    And every presentation bucket is intersected with [start,end)
    And whole contained UTC-hour sub-buckets are filled from bucket_ms=3600000 rows and each boundary fragment is filled from a bounded raw scan, at most two scans, one shared query when both fragments lie in the same grain-hour, and the number of boundary raw scans is zero when neither edge is set, one when exactly one edge is set or when both boundaries fall inside the same grain-hour (one bounded raw query serves both), and two otherwise
    And a bucket is exact only when all its pieces completed: clipped marks a normal geometric window edge and complete=false marks missing or unfinished data
    And when the edge scan exceeds MaxEdgeScanRows 50000 or MaxEdgeScanTime 2s, complete is false with a partial/Scanned reason and never a claimed exact total

  @SC-ADV-200 @TR-33 @mvp2 @unit
  Scenario: Coverage fingerprint covers one source's own effective query
    Given a case with sources S1 and S2 and one canonical filter
    When S1 is read alone and then S1 and S2 are read together
    Then the coverage fingerprint for S1 is identical in both reads
    And it is the lowercase hex of SHA-256 over "unravis-coverage-v1" plus S1, its lane and the canonicalized filter fields, with the read window only in from_ms/to_ms
    And the import_attempts row for a failed read carries the same fingerprint and the attempt_id

  @SC-ADV-201 @TR-26 @mvp2 @unit
  Scenario: An MVP-2 packet runs only after its declared prerequisites
    Given the canonical WP edges
    When WP-26 is scheduled
    Then WP-26 depends on WP-27, WP-28 and WP-15
    And WP-32 depends on WP-31 and WP-33 depends on WP-32 and WP-23
    And no MVP-2 zoom packet claims more than 24 h correctness until the 3600 s rollups exist

  @SC-ADV-202 @TR-26 @mvp2 @api
  Scenario: A stored-coverage repeat reports allLanesStored and no ledger-bypass bit exists
    Given an open case with a coverage ledger and a source
    When the client sends GET /api/cases/{id}/coverage for an already-stored interval
    Then the stored tracing result at POST .../tracing has every lane origin:"case" and allLanesStored true, while GET .../coverage returns {entries:[...], asOf, gaps:[...], provenance:"case-ledger"} and never carries allLanesStored
    And there is no bypassLedger and no fromLedger, and a genuinely newer read is the explicit POST /api/cases/{id}/pulls
    And it opens no customer connection and writes nothing
    And only POST /api/cases/{id}/pulls performs a live pull and writes exactly one attempt and coverage

  @SC-ADV-221 @TR-33 @TR-04 @mvp2 @api
  Scenario: Every lane returns sources as objects with a required kind
    Given a SQL lane and an IIS lane each backed by two sources
    When the lanes are built
    Then every lane returns sources as objects {sourceId, kind, label} where kind is the importer or event kind and is required
    And readKind is optional and is saved or live and window is present only when it differs from the lane window
    And every LaneEvent.source is the immutable case_sources.id and never an importer name

  @SC-ADV-222 @TR-34 @TR-24 @mvp2 @api
  Scenario: The live audit lane aggregates server-side under the existing budgets
    Given the live audit lane computes its buckets with one server-side $match+$group aggregation per source under the existing 60 s CensusQueryBudget and the existing broad-scan gate
    When two clients request overlapping 30-day audit spans
    Then each request runs its own bounded aggregation bounded by MaxAuditConcurrency 2, and it never pages raw rows to build counts
    And a timeout or row cap makes the audit lane state incomplete and complete=false with a reason from {timeout,rowcap,error}
    And the audit lane is a live aggregate, not a snapshot, and an unsupported span is refused with the limit named and never silently shortened

  @SC-ADV-260 @TR-26 @mvp2 @unit
  Scenario: A late middle insert makes a re-pull compare as changed
    Given a completed pull whose observed rows digest to one canonical row-content digest
    When the same window is pulled again with one extra row inserted between the first and last observed rows
    Then the repeated pull observes changed content, is written as a single transaction that stages and validates the new import to the same end-of-window completeness bar, deletes the superseded import(s) for that identical scope (cascading their events and coverage), inserts the new import and events, rebuilds the affected rollup buckets and commits, and reports outcome changed
    And a repeated pull with byte-identical row content is a no-op (StatusDuplicate, outcome unchanged)

  @SC-ADV-300 @TR-26 @mvp2 @unit
  Scenario: A changed pull whose window only partially overlaps a stored observation is refused
    Given a stored complete=1 observation for the exact half-open window 10:00 to 11:00
    When the user pulls the window 10:30 to 11:30 for the same source, lane and canonical filter
    Then the pull is refused with 409 window-overlap; re-pull the exact stored window or import a disjoint window
    And it is never silently unioned, no superseded import is deleted, and the prior observation stays readable

  @SC-ADV-301 @TR-26 @mvp2 @unit
  Scenario: A same-_id row with a different observed duration compares as changed
    Given a completed pull whose observed rows digest to one content digest
    When the same window is pulled again with _id 7, the same timestamp but durationMs 900 instead of 120
    Then the re-pull observes changed canonical row content, supersedes the prior observation for the same scope in one transaction and reports outcome changed
    And byte-identical canonical row content would have been a no-op (StatusDuplicate, outcome unchanged)

  @SC-ADV-302 @TR-26 @mvp2 @unit
  Scenario: A duplicate _id within one pull makes the pull incomplete
    Given one page whose observed rows carry the same _id twice
    When the pull is recorded
    Then the pull is incomplete, no dedup is performed, and no coverage row is written as complete=1

  @SC-ADV-303 @TR-26 @TR-33 @mvp2 @unit
  Scenario: An invalid-duration re-pull stays idempotent on observed equality
    Given a pull whose observed row carries a non-finite duration that is kept with no duration and durationStatus:'invalid'
    When the same window is pulled again with byte-identical canonical observed content
    Then the re-pull is unchanged (StatusDuplicate, outcome unchanged), because the digest is over observed canonical row content and never over the post-projection stored row
    And no invalid float is ever serialized

  @SC-ADV-304 @TR-33 @mvp2 @api
  Scenario: A coverage GET returns entries, asOf, gaps and provenance and never allLanesStored
    Given an open case with a coverage ledger and one source
    When the client sends GET /api/cases/{id}/coverage for a stored interval
    Then the response is {entries:[...], asOf, gaps:[...], provenance:"case-ledger"} and never carries allLanesStored
    And each entry carries the ledger row itself (source, fingerprint, from, to, complete, content_digest, at) and not a lane

  @SC-ADV-305 @TR-33 @mvp2 @api
  Scenario: An empty coverage ledger returns no-coverage with a non-empty gap list
    Given a case whose coverage table is empty
    When the client sends GET /api/cases/{id}/coverage for a requested window
    Then the response is entries:[] with state:"no-coverage" and a non-empty gaps list for the requested window
    And it is never presented as stored-complete and never conflated with an unknown case

  @SC-ADV-306 @TR-34 @mvp2 @unit
  Scenario: A lane consistency is labelled and complete never implies a snapshot
    Given a trace whose audit lane is a live aggregate and whose SQL lane is a saved case.db aggregate
    When the trace response is returned
    Then each lane carries consistency in {"live-non-snapshot","stored-as-of","snapshot"}, defaulting to live-non-snapshot for the live audit lane and stored-as-of for saved SQL lanes
    And the response sets mixedConsistency:true and the UI shows the non-dismissible note "live and saved lanes are read at different times; totals across lanes are not a conservation check."
    And complete:true means only that the read command returned to end within its budget, and never that the source was a stable snapshot

  @SC-ADV-307 @TR-34 @AC-18 @mvp2 @unit
  Scenario: Aligned windows scan zero boundary fragments and one-grain windows scan one
    Given a rollup-backed presentation with hasStartEdge = (start % grain != 0) and hasEndEdge = (end % grain != 0)
    When the boundaries are evaluated
    Then the number of boundary raw scans is zero when both flags are false, one when exactly one is set or when both boundaries fall inside the same grain-hour, and two otherwise
    And the edge budget MaxEdgeScanRows 50000 and MaxEdgeScanTime 2s applies per fragment and complete is false on any fragment over budget
```

#### Eval cases (Area D)

| ID | Category | Setup / input | Expected result | Maps to | Level | Phase |
|---|---|---|---|---|---|---|
| EV-ADV-01 | boundary | Rollup series query spanning exactly 30 days | Accepted, 120 buckets at the nominal 21600 s when aligned to the 6 h grid; when the aligned count would exceed 120 the axis stays UTC floor-aligned and the grid is never re-anchored, and the next coarser supported rung is selected from [60s,300s,600s,900s,1h,2h,4h,6h,8h,12h,24h] so the count is at most 120 (MVP-2 rollup path; the trace-window boundary is covered by EV-XC-12/13; cross-ref EV-ADV-221) | TR-24 | unit | mvp2 |
| EV-ADV-02 | boundary | Coverage row window of exactly 30 days | Recorded complete=1; the ledger window is not truncated | TR-24 | unit | mvp2 |
| EV-ADV-03 | boundary | Coverage row window of 30 days plus 1 second | Refused at write with a plain error that the window is longer than one month | TR-24 | unit | mvp2 |
| EV-ADV-04 | boundary | Span exactly 2 hours (bucket change boundary) | the span-nominal rung is 60 s; the presented width is W = max(ladderWidth(span), alignedRung(axisStart, axisEnd)) so the aligned count is at most 120 | TR-06 | api | mvp1 |
| EV-ADV-05 | boundary | Span 2 hours plus 1 second | Coarsens per the ladder, still at most 120 buckets | TR-06 | api | mvp1 |
| EV-ADV-06 | boundary | Span exactly 24 hours | the span-nominal rung is 900 s; the presented width is W = max(ladderWidth(span), alignedRung(axisStart, axisEnd)) so the aligned count is at most 120 | TR-06 | api | mvp1 |
| EV-ADV-07 | boundary | Span exactly 5 days | 3600 s buckets | TR-06 | unit | mvp1 |
| EV-ADV-08 | boundary | Span 5 days plus 1 second | 7200 s buckets | TR-06 | unit | mvp1 |
| EV-ADV-09 | boundary | Span exactly 10 days; 10 days plus 1 second | 7200 s then 14400 s buckets | TR-06 | unit | mvp1 |
| EV-ADV-10 | boundary | Span exactly 20 days; 20 days plus 1 second | 14400 s then 21600 s buckets | TR-06 | unit | mvp1 |
| EV-ADV-11 | boundary | Rollup bucket_ms 59999 or 60001 | Refused, only 60000 and 3600000 are written | TR-24 | unit | mvp2 |
| EV-ADV-12 | boundary | Series with exactly 2000 buckets; 2001 buckets | 2000 accepted; 2001 refused too-many-buckets | TR-24 | unit | mvp2 |
| EV-ADV-13 | boundary | Pull window of exactly 30 days; 30 days plus 1 s | Accepted; refused past the cap | TR-26 | api | mvp2 |
| EV-ADV-14 | boundary | 50000 and 50001 saved pull rows | 50000 accepted; 50001 refused with a plain cap error | TR-26 | api | mvp2 |
| EV-ADV-15 | edge | Rollup at_ms and duration_ms: an event with ts_ms 00:00:59.999Z and one with ts_ms 00:01:00.000Z | at_ms is UTC-aligned and half-open [start,end); the first lands in the 00:00 bucket and the second in the 00:01 bucket; sum_us (integer microseconds, overflow-checked) uses duration_ms only | TR-24 | unit | mvp2 |
| EV-ADV-16 | edge | Duration exactly 0 ms and exactly 1 ms | Both count; both bin to bin 0 | TR-24 | unit | mvp2 |
| EV-ADV-17 | edge | Duration at the 1.1^192 overflow boundary | Placed in the overflow bin, bounds finite | TR-24 | unit | mvp2 |
| EV-ADV-18 | edge | NaN or Infinity duration in an event | keeps the event with no duration and durationStatus:'invalid'; it increments count_all and not count_with_duration, no invalid float is ever serialized, and only records that cannot be safely identified or that violate envelope integrity are quarantined | TR-34, AC-20 | unit | mvp2 |
| EV-ADV-19 | edge | Negative duration in an event | keeps the event with no duration and durationStatus:'invalid'; it increments count_all and not count_with_duration, and min_us/max_us are integer microseconds; no invalid float is ever serialized | TR-34, AC-20 | unit | mvp2 |
| EV-ADV-20 | edge | Empty window with no events | Rollups hold no row; zoom falls back to raw with a partial flag | TR-24 | unit | mvp2 |
| EV-ADV-21 | edge | Two case sources of the same importer kind (two IIS sites) | Rollup rows are keyed by (source, kind) where source is the immutable case_sources.id and kind is the importer kind, so two hosts of one kind stay distinct and never collide | TR-24 | unit | mvp2 |
| EV-ADV-22 | resilience | Pull source returns page 1 then a 3-page timeout on page 2 | No events and no coverage row remain after the failed import (whole-import rollback in one transaction); the stopped-early wording appears only on the pull response or import status | TR-26 | unit | mvp2 |
| EV-ADV-23 | resilience | Pull source returns 3 pages, then drops before page 4 of 5 | Import rolls back whole; on reopen no events and no coverage row; stopped-early wording only on the refused pull response or import status | TR-26 | unit | mvp2 |
| EV-ADV-24 | resilience | Kill during the rollup build pass at import end | No events and no rollups for that import after reopen | BM-12 | unit | mvp2 |
| EV-ADV-25 | resilience | Kill between commit and manifest mirror | Reopen uses the committed case; no duplicate import | BM-12 | unit | mvp2 |
| EV-ADV-26 | resilience | Re-import the same pull window after a crash | Accepted once, duplicate on the second try | TR-26 | unit | mvp2 |
| EV-ADV-27 | negative | Coverage API on an unknown case id and on a closed case | 404 for an unknown case id; 409 case-closed for a closed case; an empty ledger is SC-ADV-05 and returns [] with a state, never a 404 | TR-24 | api | mvp2 |
| EV-ADV-28 | negative | Hydration read of an unlisted field | Returns absent and reads no bytes | TR-25 | unit | mvp2 |
| EV-ADV-29 | negative | Rollup series query with bucket_ms = 0 | Refused | TR-24 | unit | mvp2 |
| EV-ADV-30 | negative | MatchAgents with two IIS rows joining to two calls | No Tier 3 match | TR-28 | unit | mvp3 |
| EV-ADV-31 | negative | MatchAgents with a would-be Tier 3 on a clock-unchecked lane | No Tier 3, no invented tier | TR-28 | unit | mvp3 |
| EV-ADV-32 | negative | MatchAgents with empty Input | Empty slice, nil error | TR-28 | unit | mvp3 |
| EV-ADV-33 | security | Pull connection string passed to the tracer | Never echoed in the response or an error | TR-26 | api | mvp2 |
| EV-ADV-34 | security | Coverage note containing a connection string | Stored note is scrubbed to plain words | TR-24 | unit | mvp2 |
| EV-ADV-35 | security | Layer 4 Mongo profiler with level off | Degraded state, no write command sent to enable it unless the off-by-default switch is explicitly acknowledged | TR-27 | unit | mvp3 |
| EV-ADV-36 | privacy | Rollup or coverage table contains an IP or cookie hash | Not present; only counts, times and sums | TR-24 | unit | mvp2 |
| EV-ADV-37 | performance | Zoom at 5M events with complete rollups | p95 under 15 ms under the bench gate | BM-04 | bench | mvp2 |
| EV-ADV-38 | performance | Zoom at 1M events with rollups absent | Raw path slower or refused, never silently short | BM-04 | bench | mvp2 |
| EV-ADV-39 | performance | Ingest throughput per-row vs batched at 100k/1M/5M | Batched at least 100k rows per second narrow; per-row lower | BM-02 | bench | mvp2 |
| EV-ADV-40 | performance | Rollup build overhead at 1M and 5M | Bytes overhead reported per table | BM-03 | bench | mvp2 |
| EV-ADV-41 | performance | Warm reopen at 1M events | Under 100 ms under the bench gate | BM-07 | bench | mvp2 |
| EV-ADV-42 | performance | Cold reopen at 1M events | Under 300 ms under the bench gate | BM-07 | bench | mvp2 |
| EV-ADV-43 | visual-state | Rollup bucket marked incomplete | Rendered from raw with a plain notice, never as a complete bucket | TR-24 | e2e | mvp2 |
| EV-ADV-44 | visual-state | Coverage banner for a complete=0 source | Banner names the source label and says the read was partial | TR-24 | e2e | mvp2 |
| EV-ADV-45 | copy | Approximate p95 shown | Value paired with the approximate label and both bounds | AC-21 | unit | mvp2 |
| EV-ADV-46 | copy | A plain too-long-window error | States the one-month limit without jargon | TR-02 | api | mvp1 |
| EV-ADV-47 | regression | `refusesSchema(5, 4)` and `refusesSchema(5, 5)` | True then false, matching the existing guard | TR-24 | unit | mvp2 |
| EV-ADV-48 | regression | Migration runs twice on a version 5 case | No-op, all statements IF NOT EXISTS | TR-24 | unit | mvp2 |
| EV-ADV-49 | regression | Rollup UNIQUE upsert on a repeat import | One row per (source, kind, bucket_ms, at_ms) where source is the immutable case_sources.id and kind is the importer kind | TR-24 | unit | mvp2 |
| EV-ADV-50 | regression | Batched INSERT path keeps crash atomicity | Same result as the per-row path after an abort | BM-12 | unit | mvp2 |
| EV-ADV-51 | a11y | Coverage banner and rollup notice announced to assistive tech | Notice text reachable as a landmark with plain wording | TR-24 | e2e | mvp2 |
| EV-ADV-52 | a11y | Agent-match list on the investigation response | Ranked list has a heading and the tier is read in words like "config or infrastructure" | TR-28 | e2e | mvp3 |
| EV-ADV-53 | boundary | Route filter exactly 200 characters; 201 characters | 200 accepted; 201 refused per MaxFilterLength | TR-14 | api | later |
| EV-ADV-54 | edge | Persisted trace window reopened across a DST change | Window stored in UTC, displayed via the existing zone toggle | TR-17 | unit | later |
| EV-ADV-55 | edge | Persisted trace window of 30 days 1 second | Refused on reopen the same as on create | TR-17 | unit | later |
| EV-ADV-100 | negative | Hydration read of a listed field with present=0 versus a field not listed at all | Both answer absent and read no bytes; SC-ADV-100 and SC-ADV-07 | TR-25 | unit | mvp2 |
| EV-ADV-101 | performance | Rollup-backed zoom test on a host without UNRAVIS_BENCH set | The 15 ms p95 assertion is skipped as informational, matching BM-04 on unverified CI hardware; SC-ADV-101 | BM-04 | bench | mvp2 |
| EV-ADV-102 | resilience | Disk full after the first new table during the v4 to v5 migration | Open fails, schema_version stays 4, no new table is left behind; SC-ADV-102 | TR-24 | unit | mvp2 |
| EV-ADV-140 | boundary | Mongo probe returns 25 window rows on a live pull | 25 events with source "mongo-xe" saved into case.db, import status ok; SC-ADV-140 | TR-26 | api | mvp2 |
| EV-ADV-141 | visual-state | Open a case with deferred verify enabled | Open returns before `PRAGMA quick_check` finishes and the case shows a visible `verifying` state until the background check completes (WP-37) | TR-25 | api | mvp2 |
| EV-ADV-142 | edge | Two SQL snapshots carrying cumulative `dm_os_wait_stats` | Stored attrs hold positive deltas between snapshots, never the cumulative raw counters (WP-42, VF-01) | TR-26 | unit | mvp3 |
| EV-ADV-143 | negative | Trace over more than the live audit span cap against the live audit lane | the live audit lane computes its buckets with one server-side $match+$group aggregation per source under the existing 60 s CensusQueryBudget and the existing broad-scan gate; an unsupported span is refused with the limit named and never silently shortened; a timeout or row cap makes the audit lane state incomplete, complete=false with a reason from {timeout,rowcap,error}, while the SQL/IIS lanes keep exact local counts | TR-02 | api | mvp1 |
| EV-ADV-160 | negative | Tracing request with an unknown JSON field, 51 sourceIds and duplicate sourceIds | Unknown field 400; 51 sourceIds refused; duplicates de-duplicated; all validation before any live connection opens | TR-03 | api | mvp1 |
| EV-ADV-161 | edge | Empty coverage ledger | 200 with [] plus a state, not a 404 and never conflated with an unknown case; SC-ADV-160 | TR-33 | api | mvp2 |
| EV-ADV-162 | resilience | Failed pull whose import transaction rolls back | An import_attempts row is written outside the transaction, the coverage API shows "last attempt failed", and the attempt never counts as coverage; SC-ADV-161 | TR-33 | unit | mvp2 |
| EV-ADV-163 | regression | Repeated or overlapping imports over the same buckets | Affected buckets rebuilt transactionally after supersession, from the events of imports not superseded or deleted so one observation per scope stays active, no double count; percentiles from merged bins, empty bins null; SC-ADV-162 | TR-34 | unit | mvp2 |
| EV-ADV-164 | boundary | Rollup sum storage | sum_us, min_us and max_us are INTEGER microseconds (overflow-checked) and no sum_ms or min_ms/max_ms REAL exists; count_all is every matching event and count_with_duration is the percentile denominator, so AC-20's exactness holds in integer arithmetic | TR-34, AC-20 | unit | mvp2 |
| EV-ADV-165 | regression | Migration shape and version check | Shape and version verified in one transaction, PRAGMA foreign_keys=ON per connection, foreign_key_check on open, failed migration rolls back, newer-schema file refused; SC-ADV-164 | TR-24 | unit | mvp2 |
| EV-ADV-166 | edge | Coverage intervals keyed by source plus query fingerprint | Intervals are half-open; source is the immutable case_sources.id and the key is source plus query fingerprint; an empty ledger returns [] with a state | TR-33 | unit | mvp2 |
| EV-ADV-180 | boundary | Rollup-backed window 10:07 to 16:07 with 6-hour presentation buckets from 3600-second grains | Presentation buckets are half-open and UTC floor-aligned; whole contained UTC-hour sub-buckets are filled from bucket_ms=3600000 rows and boundary fragments come from zero, one, or two bounded raw scans (when both boundaries fall inside the same grain-hour a single bounded raw query serves both fragments, and the edge budget applies per fragment); the geometric edge buckets carry clipped=true and reserve complete=false/Partial-Scanned for missing data or an edge scan over budget; totals exact only from the completed raw boundary scans | TR-34, AC-18 | unit | mvp2 |
| EV-ADV-181 | regression | Two IIS sites of the same importer kind in the same minute, selected separately then together | Distinct rollup rows keyed by the immutable case_sources.id (source) plus importer kind; rebuilds keep correct provenance and no bucket is double counted | TR-34, WP-30 | unit | mvp2 |
| EV-ADV-200 | boundary | same source alone and together with another source | identical per-source coverage fingerprint; the preimage is "unravis-coverage-v1" plus the one source's own lane and canonicalized filters only, with the read window in from_ms/to_ms, and a stored interval satisfies a request when it contains the requested interval for the same fingerprint and complete=1 | TR-33 | unit | mvp2 |
| EV-ADV-201 | regression | repeat a stored GET /api/cases/{id}/coverage query | the response is {entries:[...], asOf, gaps:[...], provenance:"case-ledger"} and never carries allLanesStored; each entry carries the ledger row itself (source, fingerprint, from, to, complete, content_digest, at); import and event counts and the remote connection count are unchanged, case.db is not mutated, and there is no bypassLedger or fromLedger field | TR-24 | api | mvp2 |
| EV-ADV-220 | regression | repeat a stored coverage query with no request bit | the response is entries plus asOf, gaps and provenance:"case-ledger" and never carries allLanesStored; an empty ledger returns entries:[] with state no-coverage and a non-empty gaps list; no customer connection is opened, and there is no bypassLedger or fromLedger field | TR-33 | unit | mvp2 |
| EV-ADV-202 | boundary | duration 1.5 ms and a negative or NaN input | toMicros stores 1500 us with roundHalfToEven for the finite nonnegative value; a negative or NaN duration keeps the event with no duration and durationStatus:'invalid', incrementing count_all and not count_with_duration, with no invalid float serialized; a missing duration is stored and increments count_all only | TR-34, AC-20 | unit | mvp2 |
| EV-ADV-221 | boundary | 30-day windows beginning 00:00, 00:01, 05:59, 06:00 and 23:59 | each yields at most 120 buckets; the axis stays UTC floor-aligned and the grid is never re-anchored, and when the aligned count would exceed 120 the next coarser supported rung is selected from [60s,300s,600s,900s,1h,2h,4h,6h,8h,12h,24h] so the buckets tile [start, end) exactly once with no gap, no overlap and no missing second | TR-06, AC-10 | unit | mvp1 |
| EV-ADV-222 | security | MVP-1 audit-only trace over a live audit source, then an MVP-2 pull, then a closed or locked case | the audit trace opens at least one remote read and issues zero remote writes and zero imports and never writes case.db; the pull opens one read and one import and is the only action that persists a customer read (events + coverage) and the only action that writes an import; a closed or locked case opens neither | TR-26, TR-03 | api | mvp2 |
| EV-ADV-223 | negative | GET /api/cases/{id}/coverage and POST /api/cases/{id}/tracing with a bypassLedger field in the request | the strict parser rejects the unknown field with 400; a stored-coverage repeat opens no customer connection and returns entries plus asOf, gaps and provenance:"case-ledger" and never allLanesStored; there is no bypassLedger and no fromLedger and only POST /api/cases/{id}/pulls increments coverage | TR-33, TR-24 | api | mvp2 |
| EV-ADV-224 | edge | the same source and filter requested with subset, overlapping, adjacent and disjoint intervals, plus a partial-page failure | a stored complete row satisfies a request when it contains the requested interval for the same fingerprint and complete=1; only the uncovered sub-intervals are read; a partial-page failure leaves the uncovered sliver readable; two sources selected together or separately keep distinct fingerprints | TR-33 | unit | mvp2 |
| EV-ADV-225 | regression | delete a source with coverage rows and an import_attempts tombstone, then open the case at 29 days and after 30 days | coverage.source is TEXT NOT NULL REFERENCES case_sources(id) ON DELETE CASCADE so coverage rows cascade; the tombstone survives with no FK and keeps source_label at 29 days and is pruned after MaxAttemptRetention 30 days on the next case open; PRAGMA foreign_key_check is clean and no stale source can be pulled | TR-33, TR-24 | unit | mvp2 |
| EV-ADV-226 | edge | a duration greater than maxInt64 in micros | keeps the event with no duration and durationStatus:'invalid'; it increments count_all and not count_with_duration, and no invalid float is ever serialized; a missing duration is still stored and increments count_all only | TR-34, AC-20 | unit | mvp2 |
| EV-ADV-260 | edge | late middle insert, reordered identical rows and an appended doc on one window | a pull persists every row observed in the requested window and is idempotent on observed content, computed as a canonical row-content digest of the ordered observed rows (source identity, timestamp and every evidence-affecting observed field, length-prefixed and type-tagged); byte-identical canonical content yields unchanged (StatusDuplicate), a re-pull that observes any changed canonical content supersedes the prior observation for the same scope (outcome changed) and a partial-overlap window is refused 409 window-overlap, and coverage means completely read as of asOf, not current | TR-26, TR-33 | unit | mvp2 |
| EV-ADV-280 | edge | an event carrying a non-finite, negative or greater than maxInt64 duration | a non-finite, negative or greater than maxInt64 duration keeps the event with no duration and durationStatus:"invalid"; it increments count_all and not count_with_duration, no invalid float is ever serialized, and only records that cannot be safely identified or that violate envelope integrity are quarantined; SC-ADV-280 | TR-34, AC-20 | unit | mvp2 |
| EV-ADV-300 | boundary | changed pull for a scope whose window only partially overlaps a stored complete=1 observation | Refused with 409 window-overlap; never silently unioned, and the prior observation stays readable and is never deleted; SC-ADV-300 | TR-26 | unit | mvp2 |
| EV-ADV-301 | boundary | same _id and same timestamp but a different observed durationMs in a re-pull | changed canonical row content supersedes the prior observation for the same scope in one transaction (outcome changed); byte-identical canonical content is unchanged (StatusDuplicate); SC-ADV-301 | TR-26 | unit | mvp2 |
| EV-ADV-302 | edge | digest versioning: absent key versus present-null key, and a duplicate _id within one pull | absent keys are encoded distinctly from present-null keys, and a duplicate _id makes the pull incomplete with no dedup and no complete=1 coverage row; SC-ADV-302 | TR-26 | unit | mvp2 |
| EV-ADV-303 | edge | invalid-duration row re-pulled with byte-identical canonical observed content | unchanged (StatusDuplicate, outcome unchanged) because the canonical row-content digest is over observed content, never over the post-projection stored row; SC-ADV-303 | TR-26 | unit | mvp2 |
| EV-ADV-304 | edge | two lanes at different observation times in one trace | each lane carries consistency in {live-non-snapshot, stored-as-of, snapshot} with the live audit lane live-non-snapshot and saved SQL lanes stored-as-of; mixedConsistency is true and complete:true never implies a stable snapshot; SC-ADV-306 | TR-34 | unit | mvp2 |
| EV-ADV-305 | boundary | aligned window, single unaligned boundary, and both boundaries in one grain-hour | boundary raw scans are zero, one and one respectively, and two only for two distinct boundaries; the edge budget applies per fragment; SC-ADV-307 | TR-34, AC-18 | unit | mvp2 |

#### Open points
- The exact host-metrics importer prefix for `LaneHost` is not confirmed in `internal/importer/all/all.go` (existing names include `win-procsnap`, `dotnet-counters`; no host-metrics prefix seen) — `(proposed)`.
- Appendix P names `internal/dbhealth/pull.go` and source names `mongo-pull`/`sql-pull`; no such file or prefix exists in the tree today — `(proposed)`.
- `RollupSeries`/`RollupQuery`/`RollupBucket` types and the `coverage`/`hydration` read APIs are `(proposed)`; only the DDL is specified.
- The `coverage`/`hydration`/`rollups` tables are not yet in `schemaSQL` (`internal/casestore/sqlite/schema.go`), so all §4.3–4.6 scenarios describe the post-WP-27 target — `(proposed)` until landed.
- The exact cap for a live pull (50k rows is inferred from `MaxEventQuery`) and the "2 seconds per page" slow-source threshold in F-ADV-05 are illustrative — `(proposed)`.
- The persisted trace-window storage location (meta key) for TR-17 is not defined by the spec — `(proposed)`.
- `GET /api/cases/{id}/coverage` is absent from `internal/server/routes.go`; only `/api/sources/{id}/coverage` exists (`routes.go:186`) — `(proposed)` until WP-34.
- The matcher's Tier-2 "SQL lane clean" definition and whether host metrics count as a Tier-1 signal are not fully pinned by P12 — `(proposed)`.

### Area E: cross-cutting evals, regression guards, e2e archive (SC-ARC, EV-XC)

All code claims cite paths opened in this checkout. Spec ids (TR-, AC-, WP-, BM-, D-, VF-) are from `inputs/MERGED-SPEC.md`.

#### Open points
- The `cookie:*` prefix-match branch in `evidence.Event.Validate`/`ValidateFor` does not exist yet (`internal/evidence/event.go:90,170` only exact-match) — **proposed** by WP-03.
- `ScrubSensitive` is not in `exportPreviewRequest`/`exportConfirmRequest` today (`internal/server/export.go:34-50`, opened) — **proposed** by WP-06.
- `POST /api/cases/{id}/tracing` is absent from `internal/server/routes.go` — **proposed** by WP-14/D-6.
- No `TracingPaper.svelte`, `TracingChart.svelte` or `TraceToolbar.svelte` exist under `web/src` — **proposed** by WP-16/WP-23.
- `LaneEvent` has no status/win32 field (`internal/calltrace/types.go:71-78`, opened) — **proposed** by WP-13/TR-32.
- No web build-size budget exists (`build.ps1` has none; only per-file SHA-256 sizes) — the guard in XC-06 is **proposed**.
- `MaxTraceWindow`/the >24h bucket ladder do not exist (`internal/audit/models.go:19` still holds `MaxInterval = 7*24*time.Hour`) — **proposed** by WP-15.
- Exact 423 text/wording for a locked key is not pinned by the spec; evals say "a plain-language error that says the case key is locked".

#### 1. Time edge cases

| ID | Category | Setup / input | Expected result | Maps to | Level | Phase |
|---|---|---|---|---|---|---|
| EV-XC-01 | edge | Window `2026-03-29T00:00:00Z`→`01:30:00Z` (EU spring-forward) 60 s buckets | Buckets stay on the UTC axis; count per bucket unchanged; no duplicate or dropped bucket at the DST instant | TR-05, D-4 | api | mvp1 |
| EV-XC-02 | edge | Window `2026-10-25T00:00:00Z`→`02:00:00Z` (EU fall-back, repeated local hour) | Still 120 UTC buckets at 60 s; UI local toggle shows the repeated hour clearly, no bucket merges | TR-05, TR-16 | e2e | mvp1 |
| EV-XC-03 | boundary | Window spanning `2028-02-29` (leap day) 30 d exactly | 30 d accepted, at most 120 buckets; the axis stays UTC floor-aligned and the grid is never re-anchored, and when the aligned count would exceed 120 the next coarser supported rung is selected from [60s,300s,600s,900s,1h,2h,4h,6h,8h,12h,24h], so a 30-day window yields at most 120 buckets (6 h nominal) and leap day buckets are present | TR-02, TR-06 | api | mvp1 |
| EV-XC-04 | edge | Two valid UTC timestamps one second apart near the 2016-12-31 leap second (`2016-12-31T23:59:59Z` and `2017-01-01T00:00:00Z`) | Both accepted on 1-second resolution (`timeline.go:156-158`, tolerance `Resolution: time.Second`); ordering only, no `:60` input, no crash | TR-05 | unit | mvp1 |
| EV-XC-05 | boundary | Window `23:30Z`→`00:30Z` crossing UTC midnight | Two calendar-day buckets on the same axis; bucket at 00:00 UTC aligned (`rollups.at_ms` UTC-aligned, spec §4.5) | TR-05, TR-06 | api | mvp2 |
| EV-XC-06 | edge | Toggle local-vs-UTC at `Asia/Kolkata` +05:30 | Axis labels shift by exactly 5 h 30 m; the tracing window `start`/`end` JSON fields stay UTC (WP-14 request/response types; no `timeline.go:447,508` citation) | TR-05 | e2e | mvp1 |
| EV-XC-07 | edge | Toggle local-vs-UTC at `America/St_Johns` -03:30 | Axis labels shift by exactly -3 h 30 m; half-hour offset rendered without truncation | TR-05 | e2e | mvp1 |
| EV-XC-08 | boundary | Event `start` exactly on a 60 s bucket edge (`...:59.999` vs `...:00.000`) | Bucket membership is half-open; the `:00.000` event lands in the next bucket, never counted twice | TR-06, AC-20 | unit | mvp2 |
| EV-XC-09 | edge | Two sources whose clocks differ by 4 s | No auto-correction; join uses 1 s resolution only, skew not guessed (`timeline.go:156-158`), warning shown | TR-05, TR-11 | api | mvp1 |
| EV-XC-10 | negative | Event with no TZ rule in a multi-source trace | Chart disabled with `tz-rule` reason (`timeline.go:363-378`), never plotted on a guessed zone | TR-11, AC-06 | api | mvp1 |
| EV-XC-11 | boundary | Window ending `2026-12-31T23:59:59Z` vs `2027-01-01T00:00:00Z` (year boundary) | Both parse per the tracing window validator (`MaxTraceWindow`, WP-15) and JSON time parse rules; no off-by-one year label (the audit `calltrace.yearOK` check is unrelated) | TR-05, TR-02 | api | mvp1 |
| EV-XC-12 | boundary | 30 d window `2026-01-01`→`2026-01-31` across month end | Accepted, at most 120 buckets; the axis stays UTC floor-aligned and the grid is never re-anchored, and when the aligned count would exceed 120 the next coarser supported rung is selected, so no month-start data is dropped | TR-02, TR-06, D-4 | api | mvp1 |
| EV-XC-13 | negative | Window `29d 23h 59m` vs `30d` vs `30d 1s` | First two accepted; `30d 1s` refused with a plain error saying the window is longer than one month | TR-02, AC-03 | api | mvp1 |
| EV-XC-14 | boundary | 2 h to 60 s; 2 h 1 s to 300 s; 24 h to 900 s ladder rung; 5 d to 3600 s; 10 d to 7200 s; 20 d to 14400 s; 30 d to 21600 s nominal | The presented width is W = max(ladderWidth(span), alignedRung(axisStart, axisEnd)) and the aligned count is at most 120; the ladder still steps exactly at the rung boundaries (every rung includes its upper bound, one second more moves to the next rung) and a nominal value holds only for a stated hour-aligned start (hour-aligned start: 2 h→60 s, 24 h→900 s, 5 d→3600 s, 10 d→7200 s, 20 d→14400 s, 30 d→21600 s); the axis stays UTC floor-aligned and the grid is never re-anchored, and when the aligned count would exceed 120 the next coarser supported rung is selected from [60s,300s,600s,900s,1h,2h,4h,6h,8h,12h,24h] | TR-06, TR-16, AC-10 | unit | mvp1 |

#### 2. Data volume and shape

| ID | Category | Setup / input | Expected result | Maps to | Level | Phase |
|---|---|---|---|---|---|---|
| EV-XC-15 | boundary | One lane source declared, zero events in the window | Lane shows `no-data` (files absent) or `none-seen` (files present, no event), never a zero line | TR-07, AC-02 | api | mvp1 |
| EV-XC-16 | boundary | Exactly one IIS event in a 1 h window | Lane `events`, one point, count 1, bucket label shows `1` | TR-04, AC-09 | api | mvp1 |
| EV-XC-17 | boundary | Exactly 50,000 raw qualifying events | Response returns 50,000 and `truncated:false` (cap is raw rows only, `store.go:101-102`) | TR-10, AC-05 | api | mvp1 |
| EV-XC-18 | boundary | Exactly 50,001 raw qualifying events | Response returns 50,000 and `truncated:true` with the footnote; counts still exact | TR-10, AC-05 | api | mvp1 |
| EV-XC-19 | performance | 1,000,000 saved events, rollup-backed zoom on an aligned window | Aligned rollup-only mode reads only `rollups` and issues no raw scan; p95 labelled (unverified on CI hardware, BM-04) not asserted hard; the benchmark records mode rollup-only | TR-34, AC-18, BM-04 | bench | mvp2 |
| EV-XC-20 | edge | All events fall in one bucket | Single non-zero bucket; remaining buckets zero; no division-by-zero in axis | TR-06 | unit | mvp1 |
| EV-XC-21 | edge | Very long URL/referer near 512 bytes (`maxRefererBytes`) | Referer capped at 512, query and fragment removed (`safeReferer`), no truncation mid-multibyte | TR-19 | unit | mvp1 |
| EV-XC-22 | edge | Unicode route/`stem` (e.g. `/%E2%82%AC` and raw `€`) | Stored and rendered without mojibake; width math on the byte length, not rune count | TR-19 | unit | mvp1 |
| EV-XC-23 | negative | An import whose events repeat one `EvidenceID` (primary key of `events`) | The duplicate fails the plain `INSERT` on `events.evidence_id`, the whole import transaction rolls back and nothing is written; the rollup `UNIQUE(source,kind,bucket_ms,at_ms)` is unrelated and cannot mask it | TR-24, AC-22 | unit | mvp2 |
| EV-XC-24 | edge | A lane with 60 events plus one more (`MaxLaneEvents = 60`); maps to @SC-API-104 | First 60 shown, `hidden:1` note rendered; counts unaffected | TR-07 | api | mvp1 |
| EV-XC-25 | edge | `bytes-out`/`bytes-in` as huge ints beyond int64 display | Shown as exact until `maxAuditMs` bound; no `Inf`/`NaN` leaks (`finite`) | AC-12 | unit | mvp1 |

#### 3. Security and privacy

| ID | Category | Setup / input | Expected result | Maps to | Level | Phase |
|---|---|---|---|---|---|---|
| EV-XC-26 | privacy | Trace response over a case holding `client-ip`/`referer`/`cookie:sid` | JSON contains no `client-ip`, `referer` or `cookie:*`; only lane labels/counts (tracing read privacy is TR-13, the events read API is TR-15/AC-23) | TR-13, AC-12 | api | mvp1 |
| EV-XC-27 | security | No secrets in any response/log/error: connection strings, payloads | `rg` over response and logs finds no connection string or mounted payload; errors are plain sentences | TR-13, AC-12 | api | mvp1 |
| EV-XC-28 | security | Hostile window values: `'; DROP TABLE`, `$where`, `{"$gt":""}`, `../../` | All refused with a plain 400; no SQL/Mongo/JSON echoed; no query executed | TR-02, TR-03 | api | mvp1 |
| EV-XC-29 | security | Huge body (e.g. 50 MB) on `POST .../tracing` | Refused before parse with a size-limit error, no OOM, no partial read | TR-03 | api | mvp1 |
| EV-XC-30 | security | Wrong `Content-Type` (`text/plain` with JSON body) | Refused with a plain error; handler does not coerce | TR-03 | api | mvp1 |
| EV-XC-31 | security | Case-id path traversal `../other-case`, `%2e%2e%2f`, `case/../../` | 400/404; the id is validated as a single path segment before use (`routes.go` comment "single path segment") | TR-03 | api | mvp1 |
| EV-XC-32 | security | Open case A with case B's key in the body | Refused; key never in path/query (`routes.go` comment "never in the path or query"); no cross-case read | TR-03, D-8 | api | mvp1 |
| EV-XC-33 | security | Read-only guarantee: instrument customer DB driver during a trace | Zero write statements issued against customer DBs; tracing uses only read routes (`investigation.go:17-21` pattern) | TR-13, D-7 | api | mvp1 |
| EV-XC-34 | privacy | Export with default scrub ON | Bundle has no `client-ip`, `referer`, or `cookie:*`; statement reads "Client IP addresses, referers and cookie digests are not in this file." | TR-21, AC-17 | api | mvp1 |
| EV-XC-35 | privacy | `cookie:theme` present but not on the allowlist | Raw cookie never read (`common.go:5-7`); only allowlisted names hashed; drop listed as a gap | TR-20, AC-16 | unit | mvp1 |
| EV-XC-36 | security | Master key locked, any case endpoint | 423 on every locked-path call, incl. tracing; text says the case key is locked | TR-13, AC-11 | api | mvp1 |
| EV-XC-37 | security | `cookie:*` allowlist entry does not weaken exact match | Exact-match attrs still enforced; prefix branch added after exact path only (WP-03, proposed) | TR-20 | unit | mvp1 |
| EV-XC-100 | privacy | GET /api/cases/{id}/events with includeSensitive=1 while the caller holds the case key, then a default export of the same case | One pure function ProjectEventAttrs(attrs, includeSensitive) is applied to every outward-facing event view (events API, export bundle, report, tooltip/detail); includeSensitive=false is the default and drops client-ip, referer and every cookie:* key, and opt-in is explicit per read/export; SC-PRV-20 and SC-PRV-23, TR-15 and TR-21 boundary | TR-15, TR-21 | api | mvp1 |

#### 4. Resilience

| ID | Category | Setup / input | Expected result | Maps to | Level | Phase |
|---|---|---|---|---|---|---|
| EV-XC-38 | resilience | Mongo source unreachable mid-trace | The audit lane computes its buckets with one server-side $match+$group aggregation per source and is a live aggregate, not a snapshot; an unreachable source makes the audit lane incomplete and complete=false with a reason from {timeout,rowcap,error} while the uncapped local case.db GROUP BY keeps the SQL/IIS counts exact and rendering | TR-07, §5.4 | api | mvp1 |
| EV-XC-39 | resilience | Slow source (audit read exceeds budget) | The audit lane returns within budget with complete=false and a reason from {timeout,rowcap,error}; the SQL/IIS lanes keep exact local counts; a bucket from an incomplete audit lane is never rendered as complete and the partial/Scanned reason is carried | TR-07, §7 | api | mvp2 |
| EV-XC-40 | resilience | Client cancels the window tracing request on `TracingPaper.svelte` (`ctx`-cancel pattern proposed by WP-16, not the per-call tray) | Server stops the read on `ctx` cancel; no error banner, no partial write | TR-03 | api | mvp1 |
| EV-XC-41 | resilience | Two concurrent traces on one case | Both read consistently; WAL readers see last commit (`BM-09`) | TR-03, BM-09 | api | mvp2 |
| EV-XC-42 | resilience | Import running while a trace reads the window | Reader sees last committed state; no torn events; import transaction stays atomic | BM-09, BM-12, AC-22 | unit | mvp2 |
| EV-XC-43 | resilience | Kill the app mid-import, reopen | Reopen succeeds, no partial events, schema still pristine (BM-12/WP-57) | AC-22, BM-12 | unit | mvp2 |
| EV-XC-44 | resilience | Kill the app mid-IMPORT (process restart) | Reopen restores the case; the whole import (events and coverage rows) rolled back in one transaction, so no partial rows remain; a later trace re-runs cleanly | AC-22, BM-12 | unit | mvp2 |
| EV-XC-45 | resilience | Stale, missing or blank export preview id | 409 with the plain body "the preview changed; run the preview again"; no bytes are produced; confirm exports the same case revision and the same scrub policy class as the preview, it does not promise byte-identical output because each export uses a fresh pseudonym key and records its own creation time, and there is no stored one-shot, expiry or unknown-preview 404 state | WP-06, WP-07 | api | mvp1 |

#### 5. Accessibility and copy

| ID | Category | Setup / input | Expected result | Maps to | Level | Phase |
|---|---|---|---|---|---|---|
| EV-XC-46 | a11y | Every lane status (`no-data`, `none-seen`, `clock-unchecked`, `incomplete`, `out-of-range`) | Each shows its fixed server-supplied label and sentence as text, not colour alone (tracing API lane `state` plus a server-provided label/sentence field per WP-14/WP-16, proposed; the per-call tray constants are not the source) | TR-07, TR-22 | e2e | mvp1 |
| EV-XC-47 | a11y | Trace chart/lanes colours | Text contrast ≥ 4.5:1 in dark and light themes (mirrors `a11y.spec.ts:405,419` pattern) | TR-22 | e2e | mvp1 |
| EV-XC-48 | a11y | Tab through every control of `TraceToolbar.svelte` mounted inside `TracingPaper.svelte` (lane toggles, scrub pill, percentile selector, close) | Every control reachable by keyboard; toolbar buttons are real `<button>`s with `aria-pressed`; the per-call `CallTrace.svelte` tray is untouched | TR-23, WP-10, TR-12 | e2e | mvp1 |
| EV-XC-49 | a11y | Focus a `TracingChart.svelte` point then blur | Tooltip text is reachable by keyboard (focus handlers on the tracing chart, not the per-call `CallTrace.svelte` tray); the readout line updates | TR-22 | e2e | mvp1 |
| EV-XC-50 | copy | Any error path (bad window, locked key, refused trace, changed preview) | Plain-language sentence, no jargon; exact spec strings used where pinned (e.g. the preview-changed 409 body) else "a plain-language error that says <meaning>" | TR-22 | e2e | mvp1 |
| EV-XC-51 | copy | `event.time-taken`, `gap.time-end`, `gap.resolution` tooltips | Dictionary sentences present; `gap.time-end` stays until VF-01b confirms (`w3c.go:26`), `gap.resolution` states one-second resolution (`w3c.go:28-30`) | TR-22, TR-31 | unit | mvp1 |
| EV-XC-52 | copy | Every jargon term in the trace UI (`p50`, `p95`, `4xx`, `incomplete`, `join`) | Each has a dictionary entry; `explain('missing') === ''` never throws (WP-08) | TR-22 | unit | mvp1 |
| EV-XC-53 | copy | Screen text for the trace | Passes `copylint.ts` `lintScreenStrings` (reading grade ≤ 8.5, no undefined term, no causal wording; `copylint.ts:17,301`) | TR-22 | unit | mvp1 |
| EV-XC-54 | a11y | Close the trace | Focus returns to the Start tracing opener on `TracingPaper.svelte` per TR-12 (not the per-call tray `closeTrace`); Esc closes and does not trap focus | TR-12 | e2e | mvp1 |
| EV-XC-55 | copy | `win32` partition series (Later) | Labelled "estimate" in the UI; never presented as measured (D-12) | D-12 | e2e | later |

#### 6. Regression guards

| ID | Category | Setup / input | Expected result | Maps to | Level | Phase |
|---|---|---|---|---|---|---|
| EV-XC-56 | regression | Archived guided e2e still exercises guided UI | `./run-e2e.ps1 -Archive` runs `archive/guided/guided.spec.ts` and `openGuided` (or equivalent) still reaches heading "Guided analysis" level 2 without using a case-home Guided button | D-2, TR-01, WP-62 | e2e | mvp1 |
| EV-XC-57 | regression | Guided unit tests stay green | `web/tests/guided.logic.test.cjs`, `guided.steps.test.cjs`, `guided.ui.test.cjs` pass and none are removed (WP-63/D-2 keeps guided code, routes and unit tests; only Playwright specs are archived) | D-2 | unit | mvp1 |
| EV-XC-58 | regression | `CallTrace.svelte` per-call tray unchanged | Existing behaviour and tests (`web/tests/callTrace.test.cjs`, `callTrace.clumsy.test.cjs`) still pass; MVP-1 tracing lives in a separate component (TR-12/AC-15) | TR-12, AC-15 | unit | mvp1 |
| EV-XC-59 | regression | Existing `/api/analyze` unchanged | Guided `INV-01` is Run analysis / Pulse on `/api/analyze`, not guided; `routes.go:192` untouched | TR-09, WP-62 | e2e | mvp1 |
| EV-XC-60 | regression | Existing export endpoints unchanged in shape | `POST /api/export/preview` and `/confirm` keep their current fields; only an optional `scrubSensitive` is added | TR-21, WP-06 | api | mvp1 |
| EV-XC-61 | regression | Other e2e areas unaffected | `case-ui`, `sources-*`, `keeper*`, `security` specs still pass after guided specs are archived | D-2 | e2e | mvp1 |
| EV-XC-62 | regression | `TestBuildCaseNoIPsOrUserIDs` stays green | Default scrub ON keeps the existing test passing (`caseexport_test.go:320`) | TR-21, AC-17 | unit | mvp1 |
| EV-XC-63 | regression | Web build size budget | (proposed) No budget exists in `build.ps1`; if one is added, tracing must not exceed it — otherwise "no build-size budget exists (checked)" | (proposed) | manual | mvp1 |
| EV-XC-64 | regression | `w3cTimeIsEnd` const and `TestW3CTimeIsEndGap` unchanged | Gap tests stay; span code only changes if VF-01b confirms end semantics (`w3c.go:14-21`) | TR-31, VF-01b | unit | mvp1 |
| EV-XC-65 | regression | `internal/server/routes.go` guided routes | `views`/`checklist`/`report`/`investigation` routes exist at `routes.go:284-305` today; eviction is gated by WP-60/63, not by trace packets | D-2, WP-60 | unit | mvp1 |
| EV-XC-66 | visual-state | Trace loading / empty / partial / error / missing-source | Each state renders its fixed sentence; loading uses `aria-busy`, empty shows no axis, error shows retry | TR-07, §6 | e2e | mvp1 |

#### 7. E2E archive

#### F-ARC-01 Archived guided e2e specs are not run by the main command  (TR-01, D-2)

The main runner is tools/e2e/run-e2e.ps1 and it drives tools/e2e/playwright.config.ts. The config sets testDir './specs' and testMatch '*.spec.ts' (playwright.config.ts:6-7).

```gherkin
    Feature: Guided specs leave the main e2e run while staying runnable explicitly

      @SC-ARC-01 @TR-01 @D-2 @mvp1 @e2e
      Scenario: The main run does not execute the archived guided specs
        Given guide-steps.spec.ts, guide-checklist.spec.ts, guided.spec.ts, export-ui.spec.ts and the whole archived tests EXP-04a, UPD-07 and the 22 guided-dependent manual-tools tests have been moved to tools/e2e/archive/guided outside tools/e2e/specs
        And the GUIDE-15, GUIDE-15a and GUIDE-15b tests in plan.spec.ts and the GUIDE-21..GUIDE-24 tests in sources-steps.spec.ts stay in tools/e2e/specs because they never open the guided screen
        And playwright.config.ts keeps testDir './specs'
        When the operator runs powershell -File run-e2e.ps1
        Then no test from an archived spec file is executed
        And the only GUIDE- titles in the JSON report at report/main.json are GUIDE-15, GUIDE-15a, GUIDE-15b and GUIDE-21..GUIDE-24
        And the main-run test count drops by the number the archive run lists, discovered with npx playwright test --list at runtime, so no exact historical count is required

      @SC-ARC-02 @TR-01 @D-2 @mvp1 @e2e
      Scenario: One explicit command still runs the archived guided specs (built by WP-62)
        Given the archived guided specs are in tools/e2e/archive/guided
        When the operator runs powershell -File run-e2e.ps1 -Archive from tools/e2e (equivalent to npm run e2e:guided, which runs Playwright with testDir ./archive/guided and the same JSON reporter)
        Then the GUIDE- tests in guided.spec.ts and guide-steps.spec.ts are executed
        And an archived entry reaches the "Guided analysis" heading without the case-home Guided button (proposed: WP-62 rewrites openGuided to a direct route or an archive-only helper; the -Archive switch, npm run e2e:guided and testDir ./archive/guided are proposed by WP-62)
        And the required test-ID set is generated from the version-controlled accepted scenario inventory (SC-* and EV-* ids actually present in the accepted spec), and every generated ID must appear in exactly one of the main/archive suites and every ID listed in a suite must exist in the generated set (no orphans), so a dropped ID fails the gate
        And archived specs are run on demand and may need their own opener after WP-64 (not part of the main run)

      @SC-ARC-03 @TR-01 @D-2 @mvp1 @e2e
      Scenario Outline: A mixed spec keeps only its non-guided tests in the main run
        Given "<spec>" contains both guided-opening and non-guided tests
        When the operator runs powershell -File run-e2e.ps1 -Only <ids>
        Then only the tests whose titles match the listed ids run (the -Only pattern is anchored to the start of a test title)
        And no guided-opening test in "<spec>" runs
        Examples:
          | spec | ids |
          | manual-tools.spec.ts | MAN-03 |

      @SC-ARC-04 @TR-01 @D-2 @mvp1 @e2e
      Scenario: Archive leaves no dangling guided references in the main tree
        Given the guided specs have been archived
        When the operator runs rg -i "Open guided analysis" tools/e2e/specs
        Then there are no matches in the main spec folder
        And no file under tools/e2e/specs imports openGuided from support/ui.ts
        And no file under tools/e2e/specs contains a guided-only closing heading assertion

      @SC-ARC-05 @TR-01 @D-2 @mvp1 @e2e
      Scenario: The main command lists the unchanged non-guided suite
        Given the archive is in place
        When the operator runs powershell -File run-e2e.ps1 -Headed
        Then the run starts against the demo exe named in support/launcher.ts
        And the non-guided specs such as a11y.spec.ts and investigate.spec.ts still run

      @SC-ARC-06 @TR-01 @D-2 @mvp1 @manual
      Scenario: The archive README names the one explicit command
        Given the archived guided specs sit outside tools/e2e/specs
        When a person opens the archive folder README
        Then it names the single command that runs the archived specs
        And it says the main run-e2e.ps1 command does not run them

      @SC-ARC-100 @TR-01 @D-2 @mvp1 @e2e
      Scenario: GUIDE- tests that never open the guided screen stay in the main run
        Given plan.spec.ts defines GUIDE-15, GUIDE-15a and GUIDE-15b against the checklist API
        And sources-steps.spec.ts defines GUIDE-21, GUIDE-21a and GUIDE-22..GUIDE-24 against case-page source cards
        When the operator runs powershell -File run-e2e.ps1
        Then those GUIDE- tests are executed in the main run
        And their two spec files are not moved into tools/e2e/archive/guided

      @SC-ARC-160 @TR-01 @D-2 @mvp1 @e2e
      Scenario: The archived guided suite runs in the pre-release gate and on a schedule
        Given the archived guided suite is in tools/e2e/archive/guided
        When the release gate runs ./run-e2e.ps1 -Release
        Then the archived suite runs as part of the gate
        And the archived suite also runs on a schedule
        And WP-62 and the release checklist document both runs

      @SC-ARC-161 @TR-01 @D-2 @mvp1 @e2e
      Scenario: The release command requires manifest coverage across both suites
        Given WP-58 runs the main suite only
        And the required test-ID set is generated from the version-controlled accepted scenario inventory (SC-* and EV-* ids actually present in the accepted spec)
        When release readiness is evaluated
        Then ./run-e2e.ps1 -Release runs the main suite and the archive suite in sequence
        And it fails unless every generated ID appears in exactly one of the main/archive suites, every ID listed in a suite exists in the generated set (no orphans), a baseline deletion is reported, and both suites pass
        And the two reports (report/main.json, report/archive.json) are present and non-empty
        And deploying on a green main suite alone is refused

      @SC-ARC-180 @TR-01 @D-2 @mvp1 @e2e
      Scenario: The pre-release gate requires manifest coverage and a diff summary
        Given WP-58 runs the per-packet main suite only
        And the required test-ID set is generated from the version-controlled accepted scenario inventory (SC-* and EV-* ids actually present in the accepted spec)
        When the release checklist runs the pre-release gate
        Then it runs ./run-e2e.ps1 -Release, which runs the main suite then the archive suite
        And it writes report/main.json and report/archive.json, both present and non-empty
        And it fails unless every generated ID appears in exactly one of the main/archive suites, every ID listed in a suite exists in the generated set (no orphans), and both suites pass; additions and deletions relative to the previous committed baseline are printed and a deletion requires an explicit reviewed baseline update
        And an implementation packet may edit a file not on its list only when a listed file fails to compile or an existing invariant/test would break without the change; the edit must be minimal, add no feature, and be reported in the completion summary (file, symbol, one-line reason), and any change to a public contract, schema or route must be escalated
        And deploying on a green main suite alone is refused

      @SC-ARC-200 @TR-01 @D-2 @mvp1 @e2e
      Scenario: The release command keeps two separate reports
        Given the release command is ./run-e2e.ps1 -Release
        When it runs the main suite then the archive suite
        Then it writes report/main.json and report/archive.json
        And running the archive does not overwrite report/main.json
        And it fails with a non-zero exit when either suite fails, when a generated ID is missing from both suites or appears twice, when a suite lists an ID absent from the generated set (an orphan), or when report/main.json or report/archive.json is absent or empty

      @SC-ARC-220 @TR-01 @D-2 @mvp1 @e2e
      Scenario: The release command fails on an absent or empty report
        Given the release command is ./run-e2e.ps1 -Release
        When either report/main.json or report/archive.json is absent or empty after the run
        Then the release fails with a non-zero exit
        And test counts are discovered with npx playwright test --list at runtime, so no exact historical count is required

      @SC-ARC-221 @TR-01 @D-2 @mvp1 @e2e
      Scenario: The release command fails when either suite fails
        Given the release command is ./run-e2e.ps1 -Release
        When the main suite fails and the archive suite is green, or the archive suite fails and the main suite is green
        Then the release fails with a non-zero exit
        And a green suite alone is never reported as release-ready

      @SC-ARC-260 @TR-01 @D-2 @mvp1 @e2e
      Scenario: The release command fails when a required manifest ID is dropped
        Given the required test-ID set is generated from the version-controlled accepted scenario inventory (SC-* and EV-* ids actually present in the accepted spec)
        When a generated ID appears in neither the main nor the archive suite, or appears in both, or a suite lists an ID absent from the generated set
        Then ./run-e2e.ps1 -Release fails with a non-zero exit
        And the archive suite listing more than zero tests alone is never sufficient

      @SC-ARC-280 @TR-01 @D-2 @mvp1 @e2e
      Scenario: The release gate checks the manifest against both suites
        Given the required test-ID set is generated from the version-controlled accepted scenario inventory (SC-* and EV-* ids actually present in the accepted spec)
        When the release gate runs ./run-e2e.ps1 -Release
        Then it fails unless every generated ID appears in exactly one of the main/archive suites, every ID listed in a suite exists in the generated set (no orphans), a baseline deletion is reported, and both suites pass
        And the two reports (report/main.json, report/archive.json) are present and non-empty
        And the archive suite listing more than zero tests alone is never sufficient
```

| ID | Category | Setup / input | Expected result | Maps to | Level | Phase |
|---|---|---|---|---|---|---|
| EV-XC-67 | regression | `run-e2e.ps1 -Only GUIDE-04` after archiving | No test matches the pattern; `./run-e2e.ps1 -Only` fails with a clear "no test matched" error and a non-zero exit code when its pattern matches zero tests; no guided tests run | TR-01, D-2 | e2e | mvp1 |
| EV-XC-68 | regression | `exports.spec.ts:119` clicks "Open guided analysis" | After archiving, that spec no longer contains the guided click; the remaining export tests pass | D-2, WP-62 | e2e | mvp1 |
| EV-XC-69 | regression | `keeper-update.spec.ts:236-237` asserts the guided heading | The guided assertion is removed or archived; `UPD-` tests still cover keeper update | D-2, WP-62 | e2e | mvp1 |
| EV-XC-70 | regression | `manual-tools.spec.ts` `toManual` calls `openGuided`, so MAN-03/MAN-03a need it too | MAN-03 and MAN-03a are first rewritten to reach Manual tools from the case home without `openGuided`; only then do they stay in the main run, otherwise they are archived with the other 22 guided-dependent tests | D-2, WP-62 | e2e | mvp1 |
| EV-XC-71 | regression | `help-guide.spec.ts` slugs include `guided-analysis`, `manual-tools` | Help nav/anchors updated or the guided-page tests rewritten; main run stays green | D-2, WP-62 | e2e | mvp1 |
| EV-XC-72 | regression | `support/ui.ts` `openGuided` helper | Helper kept only for the archived suite, or removed from the main helper set; main specs do not import it | D-2, WP-62 | unit | mvp1 |
| EV-XC-101 | regression | Close a trace opened from the case home | Focus returns to the Start tracing opener on `TracingPaper.svelte` per TR-12 (not the per-call tray `closeTrace`); Esc closes without trapping focus; SC-UI-29 focus-return boundary | TR-12 | e2e | mvp1 |
| EV-XC-102 | regression | Main e2e after the guided archive | `call-trace.spec.ts` and `web/tests/callTrace.test.cjs` still run and pass in the main run, so archiving guided specs does not silently drop the per-call tray coverage | D-2, TR-12, WP-62 | e2e | mvp1 |

#### 8. Release / readiness

| ID | Category | Setup / input | Expected result | Maps to | Level | Phase |
|---|---|---|---|---|---|---|
| EV-XC-73 | copy | Clean-PC install note in the user guide | `docs/user/index.md:19` states "Not yet run on a clean PC: install, upgrade and uninstall, the key keeper and the Blackbox service." | §8 | manual | mvp1 |
| EV-XC-74 | copy | `docs/user/install.md:7` note | Install page keeps its "NOT RUN: needs a clean VM" quote for the Windows parts | §8 | manual | mvp1 |
| EV-XC-75 | copy | `docs/user/upgrade-and-uninstall.md:5` note | Upgrade page keeps "NOT RUN: needs a clean VM" and "have not yet run against a real install" | §8 | manual | mvp1 |
| EV-XC-76 | copy | Unsigned build note for tags/releases | Any release/tag description says the build is unsigned (`build.ps1` "Nothing is signed here; signing is a release step"); no signature claimed | §8 | manual | mvp1 |
| EV-XC-77 | copy | "not yet verified on a clean PC" where tags/releases are described | Release notes carry the clean-PC wording wherever a tag/release is described | §8 | manual | mvp1 |
| EV-XC-78 | copy | `HELP-12c` clean-machine note in the in-app guide | Upgrade and uninstall shows the "not run on a clean machine" note as a quote (`help-guide.spec.ts:269`) | §8 | e2e | mvp1 |
| EV-XC-79 | copy | Installer manifest SHA-256 | `build.ps1` writes a manifest with SHA-256 per file and setup refuses a changed payload; no claim of a signed installer | §8 | manual | mvp1 |
| EV-XC-80 | copy | Trace/tracing copy never implies a verified W3C end time | `gap.time-end` visible until VF-01b confirms (`w3c.go:14-21,26`); `time-taken` tooltip uses unverified wording until VF-01a | TR-31, VF-01 | unit | mvp1 |
| EV-XC-160 | boundary | An event at exactly the window start and one at exactly the window end | Half-open [start,end): the start event is included, the end event is excluded and belongs to the next adjacent window; coverage intervals are half-open too | TR-02, TR-06 | unit | mvp1 |
| EV-XC-161 | privacy | Canary IP, referer and cookie hash planted in a case, then preview, confirm, bundle and report exports | Every export surface drops the canary IP, referer and cookie hash; the scrub pill says the export hides them | TR-21, AC-17 | api | mvp1 |
| EV-XC-162 | privacy | Cookie hashing key and duplicate cookie names | One per-case 32-byte HMAC key in meta key cookie.hmac_key_v1 generated once from a cryptographic RNG; the stored digest is the lowercase hex of HMAC-SHA256 over the byte string: the prefix unravis-cookie-v1, a zero byte, the lowercased cookie name, a zero byte, then the raw value truncated to 32 hex characters; pseudonymous not anonymous; no rotation in MVP-1; duplicate cookie name first wins | TR-20 | unit | mvp1 |
| EV-XC-163 | resilience | Kill the app after the import transaction rolls back but before the import_attempts write | The attempt row may be absent (written outside the transaction) and never counts as coverage; reopen shows the last committed state | AC-22, BM-12 | unit | mvp2 |
| EV-XC-180 | regression | Release command `./run-e2e.ps1 -Release` with the main suite green and the archive suite failing | The release fails because -Release runs both suites; a green main suite alone is not releasable; the required test-ID set is generated from the accepted scenario inventory and fails unless every generated ID appears in exactly one suite and every suite ID exists in the generated set (no orphans); it also fails when the main/archive report is absent or empty; report/main.json survives and report/archive.json is written; SC-ARC-180, SC-ARC-200 and SC-ARC-220 | TR-01, D-2, WP-62 | e2e | mvp1 |
| EV-XC-200 | regression | ./run-e2e.ps1 -Release with both suites green | Both suites run in sequence, report/main.json and report/archive.json both survive, and the command exits 0; SC-ARC-200 | TR-01, D-2, WP-62 | e2e | mvp1 |
| EV-XC-201 | regression | ./run-e2e.ps1 -Release when one generated ID has been dropped from both suites | A non-zero exit; every generated ID must appear in exactly one of the main/archive suites and both suites must pass; a non-empty executed-test count alone never proves the generated set is covered, and a baseline deletion is reported | TR-01, D-2, WP-62 | e2e | mvp1 |
| EV-XC-280 | regression | ./run-e2e.ps1 -Release with the main suite green, the archive suite green and the union of both suites missing one generated ID | A non-zero exit; -Release fails unless every generated ID appears in exactly one of the main/archive suites, every suite ID exists in the generated set (no orphans) and both suites pass, and the two reports (report/main.json, report/archive.json) are present and non-empty; SC-ARC-280 | TR-01, D-2, WP-62 | e2e | mvp1 |
| EV-XC-202 | regression | ./run-e2e.ps1 -Only with a pattern matching zero tests | A non-zero exit and a "no test matched" error, and no test runs; EV-XC-67 boundary | TR-01, D-2 | e2e | mvp1 |
| EV-XC-220 | regression | ./run-e2e.ps1 -Release with the main suite failing and the archive suite green | the release fails because -Release runs both suites; a green archive suite alone is not releasable; it fails with a non-zero exit when either suite fails, a generated ID is missing or duplicated across the suites, a suite lists an ID absent from the generated set (an orphan), or report/main.json or report/archive.json is absent or empty; SC-ARC-161 and SC-ARC-220 | TR-01, D-2, WP-62 | e2e | mvp1 |
| EV-XC-221 | regression | every Detail cell in the packet table and the Appendix P global done-check | every normative fact resolves inside this document or is marked informational; references to PERF-RESEARCH, SPECB and EXT-REVIEW are informational only and no packet depends on them; test counts are discovered with npx playwright test --list at runtime and no exact historical count is required | TR-01 | unit | mvp1 |
| EV-XC-260 | boundary | 30-day windows starting 00:00, 00:01, 05:59 and 23:59 across leap day 2028-02-29 | each is accepted with at most 120 buckets on a UTC floor-aligned axis that is never re-anchored; a 00:00 start keeps 6 h buckets (n=120), an off-hour start selects the next coarser supported rung, and leap day buckets are present | TR-02, TR-06 | api | mvp1 |

## Appendix P. Long-form packet detail (P1-P12, referenced by the Detail column)

**Audience:** an AI coding agent that receives **one work packet at a time**. Each packet is self-contained: it names exact files, signatures, DDL, JSON shapes, tests, and a done-check. Do not explore the repo, do not ask questions; implement exactly the packet. A packet may edit a file not on its list only when a listed file fails to compile or an existing invariant or test would break without the change; the edit must be minimal, add no feature, and be reported in the completion summary (file, symbol, one-line reason). Any change to a public contract, schema or route is not dependency-adjacent and must be escalated. If a packet says a file already exists, open it and match the surrounding style; do not rewrite unrelated code.

**Reader contract:** one packet per turn. Every claim a packet makes about the current code was verified against the working tree at the paths and symbols below. Line numbers drift; anchors are symbol names. Anything the author could not confirm is marked **(unverified)** and a packet that touches it must open the file first and decide locally.

**Global constraints that override any instinct:**

1. **STORAGE.** Extend the existing **encrypted per-case `case.db`** and its unified `events` table (schema in `internal/casestore/sqlite/schema.go`). Do **not** create a separate `.unravis.db`. Every new table (coverage ledger, hydration flags, rollups) lives inside `case.db` and is written **inside the import transaction** alongside the events. The exception is the (proposed) MVP-2 `import_attempts` table, written **outside** the import transaction so a failed pull leaves a durable attempt but never counts as coverage; a failed import still rolls back all events and coverage rows whole.
2. **IIS PRIVACY.** Store client IP and referer. Cookies: store only an allowlist of **named** cookies (default empty), and store each cookie value as a 32-byte-HMAC **digest** over a domain-separated, versioned input, never raw. Export/share has a **scrub option, default ON**, that removes IP, referer and cookie hashes. The importer comments and tests that pin "drop-at-parse" must change (see Packet P1).
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

Every lane returns `sources` as objects `{sourceId, kind, label}` where `kind` is required, with optional `readKind` (`saved`|`live`) and `window` (present only when it differs from the lane window), and every `LaneEvent.source` is the same immutable `case_sources.id`; the importer kind appears only in `kind`, never as a `source`. The audit lane is a live remote server-side aggregation with its own completeness and error reporting and its own source window limit, while the SQL/IIS lanes are uncapped local `case.db` aggregates; the audit lane reports `complete:false` with a `reason` in `{timeout,rowcap,error}` while SQL/IIS lanes keep their exact local counts. The MVP-1 **tracing chart** is `web/src/components/TracingPaper.svelte` (WP-16). The per-call tray (`CallTrace.svelte`) is **not wired** in this tree (grep 2026-10-30); MVP-1 packets must not assume an Evidence call-trace path.

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

**Goal.** Reverse the drop-at-parse behaviour for exactly three fields: client IP, referer, and a **named-cookie allowlist** whose values are stored only as 32-byte-HMAC digests. Server IP stays dropped. Query string stays dropped. User agent stays dropped. Cookies **not** on the allowlist are dropped, never read.

**Files.**
- `internal/importer/iis/common.go` — the package comment (verified lines 5–7) currently promises "Drop at parse: client and server IPs, query strings, user agents, cookies and referers are never read into any value." Rewrite it to state the new rule. Keep the sentence "Every field not named in a spec's keep list is skipped."
- `internal/importer/iis/w3c.go` — `w3cSpec.keep`, `w3cRow`, `W3C.AllowedAttrs()`.
- `internal/importer/iis/httperr.go` — **only if** HTTPERR declares the same fields **(unverified: HTTPERR `#Fields` handling on a real sample; if the field is absent, add nothing and record a gap).**
- Tests: `internal/importer/iis/iis_test.go`, `internal/importer/iis/conformance_test.go`.

**Per-case storage + import options.** Allowlist and the HMAC key live in `meta` (`k='cookie.allowlist'`, `k='cookie.hmac_key_v1'`); the one 32-byte key is generated once from a cryptographic RNG when the case is created. Each import copies them into `importer.Options.CookiePolicy` (never written to the import manifest):

```go
// ClientIPField / RefererField name the W3C columns to read. Empty means the
// default ("c-ip" / "cs(Referer)"); a file that does not declare them stores nothing.
type CookiePolicy struct {
    Allowed []string // lower-cased cookie names; empty means no cookie is stored
    HMACKey []byte   // exactly 32 bytes, never logged, never in the manifest, never exported
}
```

Add `CookiePolicy *CookiePolicy` to `importer.Options` in `internal/importer/importer.go:62` (same struct as `Hosts`, `Host`, `Limits`).

**Hash rule (exact).** The digest is the lowercase hex of `HMAC-SHA256(key, "unravis-cookie-v1\x00" || lowercase(name) || "\x00" || rawValue)` truncated to 32 hex characters; it is pseudonymous, not anonymous. Store the name and the 32-hex-char digest only. Never store the raw value. Provide:

```go
// HashCookie returns the 32-hex-char pseudonymous HMAC digest of one cookie value.
// It returns "" when name is not in Allowed or the key is missing or not exactly 32 bytes.
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

**`AllowedAttrs`.** Add `client-ip`, `referer`, and the cookie names. Because cookie names are dynamic, `AllowedAttrs()` cannot enumerate them. Change `ImportMeta.AllowedAttrs` handling: when the importer declares a cookie policy, the store must accept any attr whose name starts with `cookie:` **only if** the import's allowlist marks it. Simplest correct rule: `W3C.AllowedAttrs()` returns the static set plus a sentinel `"cookie:*"`, and `evidence.Event.Validate`/`ValidateFor` in `internal/evidence/event.go` treats a `cookie:*` allowlist entry as a prefix match **after** the exact-match path (do not weaken exact matches).

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

Resolve with `scrub := req.ScrubSensitive == nil || *req.ScrubSensitive` (**default ON**). Preview returns a `previewId = hex(SHA-256("unravis-preview-v1" || caseId || caseRevision || scrubSensitive || projectionVersion))` that is an advisory revision stamp, not an issuance token; preview and confirm are stateless and the preview is a schema/summary with `summarizesBytes:false`; confirm exports the same case revision and the same scrub policy class as the preview and does not promise byte-identical output, because each export uses a fresh pseudonym key and records its own creation time; confirm recomputes the digest, and a differing or missing `previewId` is answered 409 with a plain 'the preview changed; run the preview again' body, and there is no stored one-shot, expiry or 'unknown preview' 404 state. Add canary IP/referer/cookie tests for every export surface (preview, confirm, bundle, report).

**Projection change.** In `caseexport.go`, when `scrub` is true drop attr keys `client-ip`, `referer`, and any key with prefix `cookie:`. When false, keep them **but** still never include the raw cookie (there is none — only the hash) and still never include a raw URL query. Update the statement strings:
- scrub on: "Client IP addresses, referers and cookie digests are not in this file."
- scrub off: "This file holds client IP addresses, referers and cookie digests because scrubbing was turned off. Cookie values are digests, never raw values."

**Revision contract (WP-65a primitive + WP-65b mutation-site wiring + WP-65 confirm).** The `case.revision` bump primitive is packet WP-65a (proposed), which supplies only the `case.revision` storage and migration default `case.revision = 0`, `func bumpCaseRevision(tx *sql.Tx) error`, and storage tests, and has no dependency on the export packets; WP-65b (proposed, new, MVP-1) calls it at each named mutation site (import committed, import deleted, source added/changed/removed, cookie allowlist set, HMAC key set, and any exported settings change) and owns the independent before/after revision assertions; WP-65 owns only the confirm read-snapshot and the 409 `the preview changed; run the preview again` on concurrent import; `case.revision` is a monotonic integer in `meta` key `case.revision`, and confirm recomputes the revision, takes a read-consistent snapshot of the export inputs, refuses 409 if the revision differs, and produces bytes from that same snapshot.

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
Seed entries (ids are stable, names are what the user sees): `status.4xx`, `status.5xx`, `incomplete` ("Missing timing (not a failure)"), `latency.p50`, `latency.p95`, `latency.p95Ratio`, `lane.api-audit`, `lane.sql`, `lane.iis`, `event.duration`, `event.time-taken` ("Observed elapsed time, never API processing time."), `iis.point` ("Drawn at its timestamp until the source semantics are confirmed."), `gap.time-end` ("The W3C time field is assumed to be the request end."), `gap.resolution` ("Log time has one-second resolution."), `attr.client-ip`, `attr.referer`, `attr.cookie` ("A digest, not the value."), `join.unique`, `join.ambiguous`, `join.none`.

**Test.** `web/tests/dictionary.test.cjs` (`node --test`) asserts every id used by `charts.ts` formatters exists in `DICTIONARY`, and `explain('missing')` returns `''`.

**Done-check.** `npm test` (or the repo's vitest command) green; `rg "explain\(" web/src` shows the chart tooltips use it.

**(unverified: the repo's JS test runner and script name; check `web/package.json` before writing the test invocation.)**

---

## Packet P4 — Toolbar with lane toggles and the scrub indicator

**Goal.** A toolbar above the trace that (a) toggles the three MVP-1 lanes and (b) shows the export scrub state.

**Files.**
- New: `web/src/components/TraceToolbar.svelte`.
- `web/src/components/TracingPaper.svelte` — mount the toolbar here only (the window-tracing surface, AC-15).
- `web/src/lib/callTrace.ts` — add lane visibility state (`MAX_CALLS` lives here).

**Behavior.** Three toggle buttons (API audit, SQL, IIS); each `aria-pressed`. A pill that always states the export state in plain words: scrub on reads "Export hides IP, referer and cookie hash"; scrub off reads "Export includes IP, referer and cookie hash (scrub is off)"; before a preview exists it reads "Default: export hides IP, referer and cookie hash". It never says stored data is masked. There is no role system: the case key is the authorization. Start all three on. A lane with no events shows its `State*` wording from `internal/calltrace/lanes.go` (the wording is server-provided).

**Audit aggregation (proposed, replaces the paged broad-scan).** WP-17's live audit lane computes its buckets with one server-side `$match`+`$group` aggregation per source (one command), under the existing 60 s `CensusQueryBudget` and broad-scan gate; it never pages raw rows to build counts, and a single command is a live aggregate, not a snapshot. `complete:true` means the command ran to end within budget; a budget-exceeded or failed command reports `complete:false` with `reason`, and an unsupported span is refused with the limit named, never silently shortened. `BroadScanBuckets` is retired.

**Test.** `web/tests/traceToolbar.test.cjs`: toggling a lane hides its rows; the scrub pill reflects `scrubSensitive:false` and reads "Export includes IP, referer and cookie hash (scrub is off)" when scrub is off, and "Default: export hides IP, referer and cookie hash" when no preview exists yet.

**Done-check.** `npm test` green; toolbar renders in `TracingPaper.svelte` when the trace opens; `CallTrace.svelte` is unchanged and does not mount it.

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
- New: `internal/dbhealth/pull.go` — `func PullToCase(ctx context.Context, c casestore.Case, importer string, win importer.Window) (casestore.ImportResult, error)` (convert from `dbhealth.Window`/`LaneRequest` at call sites; no `Source` type).
- New importer-facing source names: `mongo-pull`, `sql-pull` (or reuse existing `mongo-*`/`sql-*` names with a `pull` origin tag).

**Rules.** A pull is an import: it persists every row observed in the requested window and is idempotent on observed content, computed as a canonical row-content digest (lowercase-hex SHA-256 over a versioned, deterministic serialization of the ordered observed rows, one line per row in the source's observed order tie-broken by `_id`, with every evidence-affecting observed field length-prefixed and type-tagged including explicit nulls; `content_digest_v` records the canonicalization version). A re-pull that observes byte-identical canonical row content is a no-op (`StatusDuplicate`, outcome `unchanged`; Pull now always re-reads the source with no TTL freshness reuse, and only persistence is skipped); a re-pull that observes changed canonical content is written as one transaction that stages the new import, deletes the superseded import for the identical `(source, lane, fingerprint, window)` scope (cascading its events and coverage), inserts the new import and events, and rebuilds the affected rollup buckets; a changed pull whose window does not exactly equal a stored observation's window is a partial-overlap case and is refused with `409 window-overlap`. Coverage means 'completely read as of `asOf`', not 'current'. All events land inside one transaction via the existing `Import` path (do **not** hand-roll `InsertEvent` outside a tx). Unknown attributes must pass the importer's `AllowedAttrs`.

**Test.** `dbhealth/pull_test.go`: `LateMiddleInsertMakesRePullChanged` (a late middle row yields outcome `changed` and persists the new row), `SameRowsReorderedUnchanged`, `AppendedDocChanged`, `PartialPageFailureRetry`, and `PullNowBypassesReuse`.

**Done-check.** `go test ./internal/dbhealth/...`; re-pull is idempotent; `case.db` grows by the events only.

---

## Packet P7 — Coverage ledger table

**Goal.** A per-source-per-window ledger inside `case.db` recording what was actually read, so the UI can say which source failed and why (reason in {timeout, rowcap, error}).

**DDL (append to `schemaSQL`, bump `schemaVersion` to 5):**
```sql
CREATE TABLE IF NOT EXISTS coverage(
  id INTEGER PRIMARY KEY,
  import_id INTEGER NOT NULL REFERENCES imports(id) ON DELETE CASCADE,
  source TEXT NOT NULL, lane TEXT NOT NULL,
  from_ms INTEGER NOT NULL, to_ms INTEGER NOT NULL,
  rows_read INTEGER NOT NULL, pages_read INTEGER NOT NULL,
  complete INTEGER NOT NULL CHECK(complete IN (0,1)),
  fingerprint TEXT NOT NULL DEFAULT '',
  fingerprint_v INTEGER NOT NULL DEFAULT 1,
  note TEXT, content_digest TEXT, content_digest_v INTEGER NOT NULL DEFAULT 1, at_utc INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS cov_src_fp ON coverage(source, fingerprint, from_ms, to_ms);
```
`source` is the immutable `case_sources.id` (see R2-007), not the importer kind. The `fingerprint` is the lowercase-hex SHA-256 over `'unravis-coverage-v1\x00' || sourceId || '\x00' || lane || '\x00' || canonicalFilters` for the one source's own *filter* only; the read window is **not** in the preimage and lives only in `from_ms`/`to_ms`, so a stored interval satisfies a request when it contains the requested interval for the same fingerprint and `complete=1`, and otherwise the missing sub-intervals are read by `POST .../pulls` (the only persisting read); coverage means 'completely read as of `asOf`', not 'current', and gains `content_digest TEXT` plus `content_digest_v INTEGER DEFAULT 1` (proposed), where the digest is the canonical observed row-content digest; `fingerprint_v` records the canonicalization version. `coverage.source` is `TEXT NOT NULL REFERENCES case_sources(id) ON DELETE CASCADE`; `import_attempts.source` is historical `TEXT` with **no** FK, carries `source_label`, and rows older than `MaxAttemptRetention` (30 days, proposed) are pruned inside the case-open transaction.

**Migration.** In `case.go` open path, add the same `refusesSchema`/`schemaVersion` handling used for v2→v4: an older case gains the table and writes version 5 in one transaction. Update the version comment.

**Writer.** Extend `Import` (or add `RecordCoverage`) to insert coverage rows **inside the same import transaction**. Coverage intervals are half-open (`[from,to)`) and keyed by `source` (the immutable `case_sources.id`) plus the per-source coverage fingerprint defined in the DDL paragraph above, with the canonicalization version in `fingerprint_v`; `asOf` is the coverage `at_utc` and `import_attempts.outcome` widens to `{requested, completed, changed, unchanged, incomplete}` (proposed). A failed pull rolls back the import transaction whole; the separate (proposed) `import_attempts` table (id, source, fingerprint, fingerprint_v, import_id, from_ms, to_ms, started_at, outcome, error, pages_ok) is written outside it, carries the same fingerprint and the failed import's `attempt_id`, and never counts as coverage. An empty ledger returns `[]` with a state, never conflated with an unknown case.

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

**Rule.** A field not listed is treated as absent. The reader never touches the network for case events; "hydrate" means lazily reading the already-persisted `events.attrs_json`/payload column from `case.db`, never a network read. The `hydration` table is only a presence/size hint. A missing field is absent and deleting an import cascades. Do **not** add cross-process hydration in this phase.

**Test.** `sqlite/hydration_test.go`: an import that declared a payload writes `present=1`; a case without it reads back absent; cascade delete works.

**Done-check.** `go test ./internal/casestore/...`.

---

## Packet P9 — Case-event rollups (the zoom fix)

**Goal.** Precompute per-source per-bucket rollups inside `case.db` so chart zoom is fast without re-scanning events.

**DDL (same migration to v5):**
```sql
CREATE TABLE IF NOT EXISTS rollups(
  id INTEGER PRIMARY KEY,
  source TEXT NOT NULL, kind TEXT NOT NULL, -- source = immutable case_sources.id; kind = importer/event kind
  bucket_ms INTEGER NOT NULL,               -- bucket width
  at_ms INTEGER NOT NULL,                   -- bucket start, aligned in UTC
  count_all INTEGER NOT NULL DEFAULT 0,       -- every matching event
  count_with_duration INTEGER NOT NULL DEFAULT 0, -- percentile denominator
  sum_us INTEGER NOT NULL DEFAULT 0,          -- integer microseconds, overflow-checked; exact
  min_us INTEGER, max_us INTEGER,             -- integer microseconds; exact
  bins BLOB,                                  -- per-rollup log-histogram bins (internal/audit/histogram.go layout)
  count_4xx INTEGER NOT NULL DEFAULT 0,
  count_5xx INTEGER NOT NULL DEFAULT 0,
  incomplete INTEGER NOT NULL DEFAULT 0,
  UNIQUE(source, kind, bucket_ms, at_ms)
);
CREATE INDEX IF NOT EXISTS roll_source_at ON rollups(source, at_ms);
```

**Writer.** At the end of `Import`, transactionally **rebuild the affected buckets** from all active events (not just the just-inserted ones); WP-30 writes `bucket_ms = 60000`, and WP-31 writes and verifies `bucket_ms = 3600000`. Presentation buckets are always half-open and the axis is UTC floor-aligned on rollup zoom; every presentation bucket is intersected with `[start,end)`, whole contained UTC-hour sub-buckets are filled from `bucket_ms=3600000` rows, each boundary fragment is filled from a bounded raw scan, at most two scans, one shared query when both fragments lie in the same grain-hour (`MaxEdgeScanRows = 50000` per fragment, `MaxEdgeScanTime = 2s`), and a bucket is exact only when all its pieces completed: `clipped=true` marks a normal geometric window edge and `complete=false` marks missing or unfinished data, and these two flags are never interchanged. Store `sum_us` (integer microseconds, overflow-checked); derive p50/p95/p99 by merging the per-rollup log-histogram `bins` using the same `BinIndex`/`BinBounds` layout as `internal/audit/histogram.go`, never by averaging percentiles; empty bins yield `null`. A non-finite, negative or `> maxInt64` duration keeps the event with no duration and `durationStatus:'invalid'`; it increments `count_all` and not `count_with_duration`, no invalid float is ever serialized, and only records that cannot be safely identified or that violate envelope integrity are quarantined; a missing duration is written and increments `count_all` only. Do the rebuild in the **same import transaction**, after the superseded import for an identical `(source, lane, fingerprint, window)` scope has been deleted (cascading its events and coverage), and repeat it after an import is deleted, from all active events (events of imports not superseded/deleted), so overlapping or repeated imports never double count while the key stays `UNIQUE(source, kind, bucket_ms, at_ms)`.

**Reader.** New method on the SQLite store: `RollupSeries(ctx, q RollupQuery) ([]RollupBucket, error)` reading only `rollups`. The chart, once this lands, prefers `RollupSeries` and falls back to `internal/casestore/series.go` streaming only when the rollup is missing.

**Caps.** Reuse the live-path constants in `internal/audit/rollup.go` (`MaxRollupGroups = 600000`, `RollupNoiseCount = 5`, `DefaultRollupSlowMs = 1000`). Refuse a query whose bucket count exceeds `MaxSeriesBuckets = 2000` (from `series.go`).

**Test.** `sqlite/rollup_test.go`: importing N events yields the expected bucket counts; the 4xx/5xx/incomplete counters match; re-import does not double-count (transactional rebuild of the affected buckets from all active events within the tx).

**Done-check.** `go test ./internal/casestore/... ./internal/audit/...`; a benchmark in `internal/perfbench` shows zoom reads hitting only `rollups`. **Do not** claim <15 ms in a test that cannot prove it on the target hardware **(unverified).**

---

## Packet P10 — Coverage-ledger UI

**Goal.** Show `coverage` rows as the "Incomplete source coverage" banner already present in `ActivityContext.svelte` (verified: `coverageErrors` banner text).

**Files.** `web/src/components/ActivityContext.svelte`, `web/src/lib/api.ts`, server route that returns coverage.

**Test.** `web/tests/coverage.test.cjs`: a `complete=0` row renders the banner and names the source label.

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

- **MVP-1 done:** IIS stores IP/referer/cookie-hash; export scrub defaults ON and is proven by `TestBuildCaseNoIPsOrUserIDs` plus canary IP/referer/cookie tests on every export surface (preview, confirm, bundle, report); trace shows API-audit + SQL + IIS lanes; toolbar toggles them; tooltips explain via the dictionary.
- **MVP-2 done:** live pulls persist and are idempotent; `coverage`, `hydration`, `rollups` tables exist in `case.db` (v5) and are written inside the import transaction; zoom reads rollups.
- **MVP-3 done:** Layer 4 imports and lanes work; `MatchAgents` returns ranked tiers with evidence.

# Global done-check for the whole paper

Run once per phase, not per packet:
```
go test ./... 
```
plus the web test command from `web/package.json` **(unverified)**. Then verify the storage decision holds: `rg "\.unravis\.db" .` returns **nothing**, and `rg "CREATE TABLE" internal/casestore/sqlite/schema.go` shows every new table (`coverage`, `hydration`, `rollups`) inside `schemaSQL`. Release readiness is proven by the mandatory, blocking sole gate `./run-e2e.ps1 -Release`, which runs the main suite then the archive suite, writes `report/main.json` and `report/archive.json`, and fails (non-zero) unless every required test ID in the test-ID set generated from the version-controlled accepted scenario inventory appears in exactly one of the suites, no orphan suite ID exists, and both reports are present, non-empty and passing.

# What this paper refuses to promise

- No ingest speed target above the measured **9–16k events/s**.
- No "<15 ms zoom" or "<100 ms reopen" as a test assertion; rollups make zoom fast in practice but the number is **(unverified)** on target hardware.
- No greenfield `.unravis.db`.
- No raw cookies, ever. No cookie of an unnamed type, ever.
- No server IP, no query string, no user agent in events.

**Status of verification.** Every symbol, table, constant and route named above was read in the working tree, except items explicitly marked **(unverified)**: the `Options` file location for `CookiePolicy`; the exact allowlist prefix-match site in `case.go`; the HTTPERR `#Fields` presence of IP/referer/cookie columns; the `web/package.json` test command; the exact `types.go` line numbers; and whether a host-metrics importer prefix already exists. A packet touching any of those must open the file first.
