# P1a implementation plan: correctness and safety

Status: plan only. Base: `master` @ 8c46f68. Roadmap: rev 9 §8.2, §6.3–6.6, §5.3. Owner rulings R1–R16 apply.

**Execution order.** T1 (the contract) merges first. T2–T7 then branch from T1's merge commit and run in parallel worktrees. Their file sets are disjoint (see the ownership table), so they merge in any order.

---

## Slice card (§5.3)

```
Slice:                 P1a, correctness and safety (audit foundations)
Preconditions:         T0.10 MCP output audit: done (2026-10-24-t0-10-mcp-output-audit.md).
                       T0.6 writer read: open; it only affects AccountId wording, which P1a
                       states as "unverified". No earlier slice is needed.
Purpose (exempt from   Can we trust the numbers the existing views show, and can we query
incident question):    without hurting the shared instance?
Rule definitions:      None used. A0 (unpruned scan: explain totalDocsExamined >= 5 x nReturned
                       + 10,000 on an Unravis query; Symptom) needs docsExamined, which P1a
                       cannot measure on a live source (explain re-runs the query), so A0 is
                       NOT evaluated. A1 (pre-filter dominance: PreActionFilterTotalMilliseconds
                       / ApiTime >= 0.5 and ApiTime >= 2,000 ms, n >= 20; Symptom) is deferred:
                       E-names are not verified. Coverage uses two fixed definitions:
                         non-null(F) = document has field F and F is not null;
                         tenant attribution available  <=>  AccountId non-null >= 80 % of
                         documents in the window (audit.AccountIDThreshold = 0.80).
Falsification:         If exact mode with maxTimeMS 10000 finishes inside budget on a restored
                       copy of the sandbox for the default comparison, the hint is not needed:
                       hint mode stays API-only (no UI control) and P1b does not build on it.
                       NOT evaluable in this slice: no restored copy is available.
Evidence inputs:       ApiAuditLog fields RequestInitiatedAt, CreatedOn, ApiTime, AccountId, _id,
                       RequestPollingType, UserAgent (kind). UTC days. Window <= 31 days (census).
Overhead:              Interactive: maxTimeMS 10000 per command, per-source concurrency 1,
                       global 4, existing row caps. Census: maxTimeMS 60000, broad, refused on
                       live unless --allow-broad-scans. Calibration: newest 10,000 _id via the
                       _id index, maxTimeMS 10000. docsExamined budget: configurable
                       (--docs-examined-budget), unset (0) until measured on a restored copy.
                       Kill switch: omit --allow-broad-scans (default); queries killed by
                       maxTimeMS; manual kill = db.killOp on an op with appName "Unravis/...".
                       Not measured: needs a restored copy (see Unverified).
Privileged ops:        None (owner ruling). Reads use the existing read role: aggregate on the
                       audit collection, buildInfo, listIndexes. $collStats count is issued
                       only when --docs-examined-budget is set (default off).
Retention:             None beyond existing in-memory results. The margin cache is in memory,
                       per source, with a 1 h TTL.
Provenance:            Every Analysis, Page (requests, slowest, activity), Breakdown and
                       CoverageReport carries provenance[] per source and window (Contract C2).
Plain-language copy:   See "Copy" below. Linted by web/src/lib/copy/copylint.ts.
```

### Copy (on-screen and MCP; no causal verbs; §6.3, §6.4 wordings verbatim)

| Id | Text |
|---|---|
| prov.complete | "Complete: every matching record in this window was read. Examined {examined}, returned {returned}, in {elapsed}." |
| prov.ranToEnd | "Finished with no time limit, row limit or read error. Records examined are not counted on a live source." |
| prov.partial | "Partial: {reason}. Some matching records may be missing." |
| prov.reason.timeout | "the {limit} s time limit was reached" |
| prov.reason.rowcap | "the row limit was reached" |
| prov.reason.error | "a read error stopped the query" |
| prov.hint (verbatim §6.4(2)) | "bounded by margin {M}, calibrated on {N} indexed rows; rows outside {M} would be missed" |
| prov.hintFallback | "Hint mode was not available ({why}); this result used exact mode." |
| prov.scan.broad | "This query reads the whole window, not an index." |
| cov.tenantUnavailable (verbatim §6.3) | "tenant attribution unavailable ({x} % populated)" |
| cov.refused | "This check reads every record in the window, so it does not run on a live source by default. Run it on a restored copy, or start Unravis with --allow-broad-scans." |
| mcp.hidden | "User ids, user agents and IP addresses are hidden: the operator has not turned on --mcp-allow-identifiers. includeIp was ignored." |

The glossary entries to add are Query mode, Complete, Partial, Margin and Coverage census. Each entry gets a hint, a definition and a "not meaning" line, and none may say "proven" or "cause".

---

## Contract (produced by T1; everything else codes against it)

### C1. Budgets and timeouts. New file `internal/audit/budgets.go`

```go
const (
    InteractiveQueryBudget = 10 * time.Second // maxTimeMS 10000
    CensusQueryBudget      = 60 * time.Second // maxTimeMS 60000
    ServerSelectionTimeout = 30 * time.Second // driver default
    ConnectTimeout         = 10 * time.Second
    PerSourceConcurrency   = 1
)
```
`OperationTimeout` (5 min) stays unchanged as the HTTP and request ceiling.

### C2. Provenance. New file `internal/audit/provenance.go`

```go
const (QueryModeExact = "exact"; QueryModeHint = "hint")            // "" == exact
const (ScanNarrow = "narrow"; ScanBroad = "broad")
const (Complete = "complete"; RanToEnd = "ran_to_end"; Partial = "partial"; HintBounded = "hint")
const (IDCalibrationSample = 10000; IDMinCalibrationRows = 1000; IDMarginPad = 60 * time.Second; IDMarginTTL = time.Hour)

type IDMargin struct {
    MarginMs     int64     `json:"marginMs"`     // observed + pad, rounded up to whole seconds
    ObservedMs   int64     `json:"observedMs"`   // p99.9 of |ObjectId time - RequestInitiatedAt|
    PadMs        int64     `json:"padMs"`
    Percentile   float64   `json:"percentile"`   // 99.9
    SampleSize   int       `json:"sampleSize"`   // rows with a date RequestInitiatedAt
    SampleFrom   time.Time `json:"sampleFrom"`   // oldest RequestInitiatedAt in sample
    SampleTo     time.Time `json:"sampleTo"`
    CalibratedAt time.Time `json:"calibratedAt"`
    Label        string    `json:"label"`        // HintLabel(m)
}

type Provenance struct {
    SourceID      string    `json:"sourceId"`
    Window        string    `json:"window"`       // current|reference|page|breakdown|coverage
    Start         time.Time `json:"start"`
    End           time.Time `json:"end"`
    QueryMode     string    `json:"queryMode"`    // exact|hint (mode actually run)
    ScanClass     string    `json:"scanClass"`
    Completeness  string    `json:"completeness"`
    PartialReason string    `json:"partialReason,omitempty"` // timeout|rowcap|error
    DocsReturned  int64     `json:"docsReturned"`
    DocsExamined  *int64    `json:"docsExamined"` // null = not measured (always on live)
    SampleSize    int64     `json:"sampleSize"`   // records behind the figures
    ElapsedMs     int64     `json:"elapsedMs"`
    MaxTimeMs     int64     `json:"maxTimeMs"`
    Margin        *IDMargin `json:"margin,omitempty"` // hint mode only
    Note          string    `json:"note,omitempty"`   // e.g. hint fallback reason
}

func CompletenessOf(p Provenance) string
func ScanClassOf(mode string, window time.Duration, indexed bool) string
func MarginFromOffsets(offsets []time.Duration, pad time.Duration) (observed, margin time.Duration, err error)
func HintLabel(m IDMargin) string
var ErrTooFewCalibrationRows = errors.New("too few rows to calibrate the _id margin")
```
The algorithms are not fixed by their signatures, so they are given here:
- `CompletenessOf`: mode hint gives `hint`. Otherwise a non-empty PartialReason gives `partial`. Otherwise a nil DocsExamined gives `ran_to_end`. Otherwise the result is `complete`. This applies §6.4(4): a live result can never be labelled "complete" because docsExamined is not printed.
- `ScanClassOf`: the result is `narrow` iff `(indexed || mode == hint) && window <= 24h`. Otherwise it is `broad`.
- `MarginFromOffsets`: if n < IDMinCalibrationRows, return ErrTooFewCalibrationRows. Otherwise take abs(offset), sort ascending and use the nearest-rank p99.9 (index `ceil(0.999*n)-1`) as `observed`. Then `margin = ceilToSecond(observed + pad)`.

### C3. Coverage. New file `internal/audit/coverage.go`

```go
const (AccountIDThreshold = 0.80; MaxCoverageDays = 31; MaxCoverageRows = 1000)
var ErrBroadScanRefused = errors.New("broad scan refused on a live source")

type CoverageInput struct { SourceID string `json:"sourceId"`; Start time.Time `json:"start"`; End time.Time `json:"end"` }
type FieldCoverage struct { NonNull int64 `json:"nonNull"`; Pct float64 `json:"pct"` } // Pct 0..100, 1 dp
type CoverageRow struct {
    Kind string `json:"kind"`  // synchronous|asynchronous|hangfire|unknown (same switch as pipeline.go)
    Day  string `json:"day"`   // YYYY-MM-DD UTC of RequestInitiatedAt, else CreatedOn
    Docs int64  `json:"docs"`
    RequestInitiatedAt FieldCoverage `json:"requestInitiatedAt"`
    CreatedOn          FieldCoverage `json:"createdOn"`
    ApiTime            FieldCoverage `json:"apiTime"`
    AccountID          FieldCoverage `json:"accountId"`
}
type CoverageReport struct {
    SourceID          string        `json:"sourceId"`
    Rows              []CoverageRow `json:"rows"`
    Total             CoverageRow   `json:"total"`             // Kind "all", Day ""
    TenantAttribution string        `json:"tenantAttribution"` // "available" | cov.tenantUnavailable text
    Provenance        []Provenance  `json:"provenance"`
}
type CoverageProber interface { Coverage(ctx context.Context, in CoverageInput) (CoverageReport, error) }
func ValidateCoverageInput(in CoverageInput) error   // wraps ErrInvalidInput; 0 < End-Start <= 31 days
func TenantAttribution(nonNull, docs int64) string   // docs==0 -> unavailable (0 % populated)
func CoveragePct(nonNull, docs int64) float64
```

### C4. Additive fields. Existing types; omitempty, so existing JSON is unchanged

- `audit.Query.QueryMode string \`json:"queryMode,omitempty"\``. `ValidateQuery` accepts "", "exact" and "hint". `Normalized()` leaves "" as "" so cursor and history fingerprints are unchanged.
- `audit.Analysis.Provenance []Provenance \`json:"provenance,omitempty"\``.
- `audit.Page.Provenance []Provenance \`json:"provenance,omitempty"\``.
- `audit.Breakdown.Provenance []Provenance \`json:"provenance,omitempty"\``.

### C5. Store budget and options. New files `internal/store/budget.go` and `internal/store/options.go`; edits to `store.go`

```go
func withQueryBudget(ctx context.Context, d time.Duration) context.Context
func queryBudget(ctx context.Context) time.Duration                       // default audit.InteractiveQueryBudget
func commandContext(ctx context.Context) (context.Context, context.CancelFunc) // WithTimeout(ctx, queryBudget(ctx)); earlier parent deadline wins

type Option func(*Store)
func New(opts ...Option) *Store                      // store.New() call sites still compile
func WithBroadScans(allowed bool) Option             // default false
func WithDocsExaminedBudget(n int64) Option          // 0 = unset
// Store gains: allowBroadScans bool; docsExaminedBudget int64;
//              marginMu sync.Mutex; margins map[string]audit.IDMargin (made in newStore)
```

### C6. HTTP and MCP surface. Produced by T3 and T6; recorded here for T7

- `GET /api/sources/{id}/coverage?start=<RFC3339>&end=<RFC3339>` returns `200 CoverageReport`.
  - `409 {"error": cov.refused, "code":"broad_scan_refused"}` when the census is refused.
  - `400` for invalid input.
  - `503` when the repository is not a `CoverageProber`.
- MCP `audit_get_breakdown`, when identifiers are hidden, adds `"identifiers":"hidden"` and `"identifiersNote": mcp.hidden`. Rows for the userId and userAgent dims carry `value:"hidden-<rank>"` and no `filter`, and userIp is refused. MCP compare, requests, slowest and breakdown outputs forward `provenance`. The tool count stays 10.
- CLI flags:
  - `--allow-broad-scans` (bool, default false; dashboard only).
  - `--docs-examined-budget` (int, default 0; dashboard only).
  - `--mcp-allow-identifiers` (bool, default false; only valid with `--mcp`).

---

## File ownership (disjoint)

| Task | Owns (create or edit) |
|---|---|
| T1 | internal/audit/{budgets.go, provenance.go, provenance_test.go, coverage.go, coverage_test.go, models.go, breakdown.go, query.go, query_test.go}, internal/store/{budget.go, budget_test.go, options.go, options_test.go, store.go} |
| T2 | internal/store/{mongo.go, legacy_client.go, profiles.go, mongo_options_test.go (new), legacy_client_test.go, profiles_test.go} |
| T3 | internal/store/{coverage.go, coverage_test.go}, internal/audit/{coverage_demo.go, coverage_demo_test.go}, internal/server/{coverage.go, coverage_test.go, server.go} |
| T4 | internal/store/{idbounds.go, idbounds_test.go, provenance.go, provenance_test.go, analyze.go, analyze_test.go, requests.go, requests_test.go, breakdown.go, breakdown_test.go, slowest.go, activity.go} |
| T5 | internal/audit/{demo.go, activity_demo.go, demo_hint.go (new), demo_provenance_test.go (new)} |
| T6 | internal/mcp/{breakdown.go, bridge.go, schema.go, tools.go, tools_test.go, bridge_test.go}, cmd/audit-explorer/{main.go, main_test.go} |
| T7 | web/src/lib/{types.ts, api.ts, provenance.ts (new)}, web/src/lib/copy/deck.ts, web/src/components/{ProvenanceNote.svelte (new), CoveragePanel.svelte (new), SourcePopover.svelte}, web/src/App.svelte, web/tests/provenance.test.cjs (new) |

Go verify commands: resolve Go as `build.ps1` does. Use `go.exe` on PATH or `<repo or ancestor>\.dev\toolchains\go\bin\go.exe`, with `GOMODCACHE` under `.dev\toolchains\cache`.

---

## T1. Contract types, budget helpers, store options (merge first)

- **Interfaces:** C1–C5 exactly as written. Behaviour does not change: nothing reads the new fields yet.
- **Tests (write first):**
  - `TestCompletenessOf`: hint gives hint; partial reason gives partial; nil DocsExamined gives ran_to_end; set DocsExamined gives complete. Also: hint with a partial reason still gives hint.
  - `TestScanClassOf`: (exact, 1h, false) gives broad; (exact, 1h, true) gives narrow; (hint, 24h, false) gives narrow; (hint, 24h+1s, false) gives broad.
  - `TestMarginFromOffsets`:
    - 999 rows gives ErrTooFewCalibrationRows.
    - 1000 rows of 0 s plus 1 row of 900 s gives observed 0 and margin 60 s, since nearest rank is index 999 of 1001.
    - Negative offsets use their absolute value.
    - A 200 ms observed value with a 60 s pad gives a 61 s margin (rounded up).
  - `TestHintLabelWording`: the text equals the §6.4(2) sentence with M formatted as "61 s" and N as "10000".
  - `TestTenantAttribution`: 80/100 gives "available"; 79/100 gives "tenant attribution unavailable (79.0 % populated)"; 0/0 gives "(0.0 % populated)".
  - `TestValidateCoverageInput`: rejects End<=Start, more than 31 days and an empty SourceID.
  - `TestQueryModeValidation` (in query_test.go): "", exact and hint are OK; "fast" fails. The Normalized() JSON of a query without a mode has no `queryMode` key.
  - `TestProvenanceJSONFieldNames`: marshals and asserts every key named in C2 and C3; `docsExamined` is present as `null`.
  - `TestQueryBudgetDefaultAndOverride`, `TestCommandContextParentDeadlineWins`: a parent with 2 s left gives a deadline ≤ 2 s, and a parent with no deadline gives ≤ 10 s.
  - `TestNewAppliesOptions`: `New()` gives allowBroadScans false and budget 0; `New(WithBroadScans(true), WithDocsExaminedBudget(5))` sets both.
- **Verify:** `go test ./internal/audit/... ./internal/store/... && go vet ./...`
- **Model:** haiku. The algorithms are fixed above.
- **Reviewer checks:**
  - JSON tags match C2–C4 character for character.
  - omitempty is on every field added to an existing struct.
  - `OperationTimeout` is unchanged.
  - No call site other than `store.New` changes.
  - The copy strings in `HintLabel` and `TenantAttribution` are verbatim.

## T2. Client hygiene: appName, timeouts, explicit maxTimeMS, concurrency 1

- **Files:** see the ownership table.
- **Interfaces:**
  - `func mongoClientOptions(uri string, tlsConfig *tls.Config) *options.ClientOptions` (v2). Used by `defaultClientFactory`. It calls `ApplyURI` first, then `SetAppName(appName())`, `SetConnectTimeout(audit.ConnectTimeout)` and `SetServerSelectionTimeout(audit.ServerSelectionTimeout)`. It keeps the existing pool and idle settings.
  - `func legacyClientOptions(uri string, tlsConfig *tls.Config) *legacyoptions.ClientOptions` (v1). Same settings. Used by `probeMongoWireVersion` and `newLegacyMongoHandleWithMonitor`.
  - `func appName() string` returns `"Unravis/" + <debug.ReadBuildInfo Main.Version, else "dev">`, truncated to ≤ 128 bytes.
  - `mongoHandle.Aggregate` runs `RunCommandCursor(cmdCtx, ...)` with `cmdCtx, cancel := commandContext(ctx)`, and returns `budgetCursor{inner, deadline, cancel}`.
    - `budgetCursor.Next(ctx)` uses `context.WithDeadline(ctx, deadline)`, so no getMore outlives the budget.
    - `Close` calls `cancel`.
    - Do NOT add `maxTimeMS` to the command document. The v2 driver derives it from the deadline (`x/mongo/driver/operation.go` calculateMaxTimeMS), and a second field is a duplicate-field error.
  - `remainingLegacyMaxTime` caps at `queryBudget(ctx)` instead of `queryTimeout`.
  - `newModernMongoHandleWithMonitor(ctx, uri, tls, *event.CommandMonitor)`, for tests only.
  - `profiles.go:163` changes to `make(chan struct{}, audit.PerSourceConcurrency)`.
- **Tests (write first):**
  - `TestMongoClientOptionsSetsAppNameAndTimeouts`: AppName has the prefix "Unravis/", ServerSelectionTimeout is 30 s and ConnectTimeout is 10 s.
  - `TestMongoClientOptionsOverridesURIAppName`: a URI with `appName=other` gives "Unravis/…".
  - `TestLegacyClientOptionsSetsAppNameAndTimeouts`: same assertions on v1.
  - `TestAggregateCommandHasNoMaxTimeField`: `aggregateCommand` has no `maxTimeMS` key.
  - `TestBudgetCursorStopsAtCommandDeadline`: wrap a fake cursor whose Next blocks until ctx is done, with `withQueryBudget(ctx, 50ms)`. Next returns false within 500 ms, and Close calls cancel.
  - `TestRemainingLegacyMaxTimeCapsAtBudget`: no deadline gives 10 s; a census budget gives 60 s; the existing expired-context test still passes.
  - `TestPerSourceCapacityIsOne` (profiles_test): after AddSource with the fake factory, `cap(s.perSource[id]) == 1`.
  - Env-gated `TestModernAggregateSendsBoundedMaxTimeMSAndAppName` (needs `AUDIT_EXPLORER_TEST_MONGO_URI`; uses the synthetic `audit_fixture_one` only). The CommandMonitor sees `aggregate.maxTimeMS` in 1..10000 and getMore with no maxTimeMS. `db.currentOp`/handshake is not used; checking appName via `$currentOp` would need new rights, so appName is asserted from the options only.
  - `TestMongo34AggregateSendsServerMaxTimeMS`: widen the bound to 1..10000.
- **Verify:** `go test ./internal/store/... && go vet ./internal/store/...`
- **Model:** sonnet. The driver detail matters.
- **Reviewer checks:**
  - `SetAppName` comes after `ApplyURI`.
  - No `maxTimeMS` appears in the command doc.
  - The cursor wrapper cancels on every path, including an Aggregate error.
  - The legacy and modern settings are identical.
  - No new `OperationTimeout` use for client timeouts.
  - No live URI is in the tests.

## T3. Coverage probe (census), gated

- **Files:** see the ownership table.
- **Interfaces:**
  - `func (s *Store) Coverage(ctx context.Context, in audit.CoverageInput) (audit.CoverageReport, error)` and `var _ audit.CoverageProber = (*Store)(nil)`. Order of steps:
    1. `ValidateCoverageInput`.
    2. If `!s.allowBroadScans`, return `audit.ErrBroadScanRefused` before leasing and before any I/O.
    3. Lease the source. Legacy 3.4 returns `ErrInvalidInput` "coverage census is not available for MongoDB 3.4 sources".
    4. If `s.docsExaminedBudget > 0`, run `[{$collStats:{count:{}}}]` and refuse with a wrapped `ErrLimit` when count > budget.
    5. Acquire global, then per-source.
    6. Run `withQueryBudget(ctx, audit.CensusQueryBudget)`, then Aggregate the `coveragePipeline`.
    7. Return a provenance entry with window "coverage", ScanClass broad, DocsExamined nil, and partial on timeout.
  - `func coveragePipeline(start, end time.Time) mongo.Pipeline`:
    - The first `$match` is identical to the time `$or` in `buildSummaryPipeline` (pipeline.go:99-105).
    - `$group` by {kind switch as pipeline.go:124-131, day `$dateToString` `%Y-%m-%d` of the RequestInitiatedAt date, else the CreatedOn date}.
    - Counts use `$sum` and non-null uses `$sum: {$cond: [{$gt: ["$F", null]}, 1, 0]}` for the 4 fields.
    - No field value is ever projected.
  - `func (d *demoRepository) Coverage(...)` (coverage_demo.go) computes the same rows from the demo records and is always allowed. Demo has no AccountId, so it reports 0 % and the unavailable wording. Its DocsExamined equals the source's record count, giving complete.
  - `func (h *handler) coverage(w, r, sourceID string)` (server/coverage.go) parses the start and end query params as RFC3339 and asserts `h.repo.(audit.CoverageProber)`. Errors map as `ErrBroadScanRefused` → 409 with code `broad_scan_refused`, ErrInvalidInput → 400, and the rest go through `writeRepoError`. server.go adds only the dispatch branch for the `/coverage` suffix (GET), before the DELETE branch.
- **Tests (write first):**
  - `TestCoverageRefusedByDefaultWithoutQuery`: the fake client's aggregateFn fails the test if called, and `errors.Is(err, ErrBroadScanRefused)`.
  - `TestCoverageRunsWhenAllowedAndUsesCensusBudget`: aggregateFn sees ctx with `queryBudget == 60s`.
  - `TestCoveragePipelineNeverProjectsValues`: walk the pipeline. There are no `$project` or `$addFields` of raw fields, and only `$group` accumulators are `$sum`.
  - `TestCoveragePipelineMatchEqualsSummaryMatch`: the first stage deep-equals `buildSummaryPipeline(...)[0]` without the record filters.
  - `TestCoverageDocsExaminedBudgetRefuses`: budget 10 with a fake $collStats count of 11 gives ErrLimit and no census aggregate.
  - `TestCoverageBudgetUnsetSkipsCollStats`.
  - `TestCoverageRowCapAndPercentages`: 3 rows decode, Pct is rounded to 1 dp, Total sums the rows, and more than 1000 groups gives ErrLimit.
  - `TestCoverageLegacyRefused`.
  - `TestDemoCoverageReportsAccountIdUnavailable`: tenantAttribution equals the verbatim §6.3 text with "0.0 %".
  - `TestCoverageRouteRefusal409`, `TestCoverageRouteDemo200`, `TestCoverageRouteBadTimes400`, `TestCoverageRouteNotProber503`.
- **Verify:** `go test ./internal/store/... ./internal/audit/... ./internal/server/...`
- **Model:** sonnet.
- **Reviewer checks:**
  - The refusal happens before any client call.
  - The census never runs on the interactive budget.
  - $collStats is reachable only with a set budget.
  - The kind switch and window match are copied exactly, with no new definitions.
  - No AccountId-dependent rule exists other than the wording.
  - The route is GET only.
  - The 409 body uses the cov.refused text.

## T4. `_id` calibration, hint/exact switch, provenance on live responses

- **Files:** see the ownership table.
- **Interfaces:** `idbounds.go`:
  - `func calibrationPipeline() mongo.Pipeline` is `[$sort {_id:-1}, $limit IDCalibrationSample, $project {_id:1, RequestInitiatedAt:1}]`. It is narrow, via the `_id` index.
  - `func (s *Store) idMargin(ctx context.Context, p *profile) (audit.IDMargin, error)` returns the cached value if younger than IDMarginTTL. Otherwise it runs the calibration aggregate, sets offset = `oid.Timestamp() − RequestInitiatedAt` for date rows only, and then calls `MarginFromOffsets` and `HintLabel`. Legacy 3.4 returns an error with the reason "not available on MongoDB 3.4".
  - `func idHintStage(start, end time.Time, margin time.Duration) bson.D` is `{$match: {_id: {$gte: NewObjectIDFromTimestamp(floorSec(start−M)), $lt: NewObjectIDFromTimestamp(floorSec(end+M)+1s)}}}`.
  - `func withIDHint(p mongo.Pipeline, stage bson.D) mongo.Pipeline` prepends the stage.
- **Interfaces:** `provenance.go` (store):
  - `func newProvenance(p *profile, window string, start, end time.Time, mode string, margin *audit.IDMargin) audit.Provenance` fills ScanClass from `ScanClassOf(mode, end-start, p.indexWarning == "")` and MaxTimeMs from the budget.
  - `func (pv *audit.Provenance) finish(...)` is not allowed, so use `finishProvenance(pv *audit.Provenance, returned, sample int64, elapsed time.Duration, partialCode string)`. It maps query_timeout to "timeout", a truncated page to "rowcap" and any other error to "error", then sets Completeness via `CompletenessOf`.
- **Wiring:** `Analyze`, `requestsMode` (requests, slowest, activity) and `Breakdown`.
  - When `q.QueryMode == hint`, get the margin per source after admission.
    - On success, prepend the hint stage to that source's pipelines and attach Margin.
    - On error, run exact with `Note = "hint unavailable: <reason>"`. Never fail silently.
  - Each source and window appends one Provenance.
  - Exact mode output must be byte-identical to today apart from the added `provenance`.
- **Tests (write first):**
  - `TestCalibrationPipelineUsesIDIndexShape`.
  - `TestIDMarginComputesFromObjectIDOffsets`: a fake cursor yields 1000 docs with known oid and RIA offsets; assert MarginMs, SampleSize, SampleFrom/To and the Label.
  - `TestIDMarginCachedWithinTTL`: the second call makes no aggregate, and after the TTL (injected `s.now`) it recalibrates.
  - `TestIDMarginTooFewRows`.
  - `TestIDHintStageBoundsRoundOutward`: start 12:00:00.700 with M 61 s gives a lower bound of 11:58:59, and the upper bound is exclusive at end+M+1 s, floored.
  - `TestAnalyzeExactModeHasNoIDStage`: inspect the pipelines from the fake aggregateFn; there is no `_id` match. Provenance has 2 entries per source (current, reference), queryMode exact, docsExamined nil, completeness ran_to_end.
  - `TestAnalyzeHintModePrependsIDStageAndLabels`: provenance completeness is hint and margin.label is the §6.4(2) text.
  - `TestAnalyzeHintFallsBackToExactWithNote`: calibration returns 0 rows; there is no `_id` stage, queryMode exact, and the note is set.
  - `TestAnalyzeTimeoutProvenancePartial`: the source errors with a deadline, giving partial with reason timeout.
  - `TestRequestsTruncatedProvenanceRowcap`.
  - `TestBreakdownCarriesProvenance`.
  - `TestExactModeAnalysisUnchangedExceptProvenance`: the golden comparison of the existing analyze fixture JSON with `provenance` removed equals the pre-change output.
- **Verify:** `go test ./internal/store/... && go vet ./internal/store/...`
- **Model:** sonnet.
- **Reviewer checks:**
  - The default is exact, and "" behaves as exact.
  - "complete" is never emitted on the live path.
  - The hint stage is ANDed and prepended, so the time `$or` is untouched.
  - The calibration projects only `_id` and RequestInitiatedAt, under the interactive budget, after admission.
  - No hint is used on legacy.
  - The existing tests pass unchanged except where assertions on provenance are added.

## T5. Demo: provenance and hint-mode demonstration

- **Files:** see the ownership table.
- **Interfaces:**
  - The demo `Analyze`, `Requests`, `Breakdown` and `Activity` attach Provenance. Values: QueryMode as requested, ScanClass narrow (the demo is in-memory and treated as indexed), DocsExamined equal to the selected source's record count, and Completeness complete in exact mode.
  - In demo hint mode, the first-write time stands in for the ObjectId time: `CreatedOn` truncated to a second, per §6.4(1). The margin comes from `MarginFromOffsets` over the demo records' (CreatedOn − InitiatedAt). Records outside `idHint` bounds are excluded, and Completeness is hint.
  - `demo_hint.go`: an unexported `newDemoWithLateWrite(now time.Time) *demoRepository` adds one Payroll.Run record per source. It is initiated 1 min into the reference window and first written 10 min after the reference window ends. `NewDemo` and `NewDemoWithStaleWork` are unchanged, so the `--demo` UI keeps its counts.
- **Tests (write first):**
  - `TestDemoExactModeIncludesLateWrittenRow`: the reference count includes it, and completeness is complete with docsExamined set. This is the positive demo from §8.2.
  - `TestDemoHintModeMissesLateRowAndWarns`: the row is excluded and the label contains "rows outside" and "would be missed". This is the negative demo from §8.2.
  - `TestDemoProvenanceOnEveryResponse`: Analysis (2 per source), Page and Breakdown are each non-empty.
  - `TestNewDemoOutputUnchangedExceptProvenance`: the existing demo analysis JSON, minus `provenance`, equals a snapshot taken before the change.
- **Verify:** `go test ./internal/audit/...`
- **Model:** haiku, with the spec above.
- **Reviewer checks:**
  - No existing demo constructor changes its records.
  - The demo never claims a live property: its copy comes only from `CompletenessOf` and `HintLabel`.
  - No new identifiers or real-looking data; the late row reuses the existing synthetic patterns (203.0.113.x, Demo agents).

## T6. MCP privacy switch, provenance forwarding, startup flags

- **Files:** see the ownership table.
- **Interfaces:**
  - `type Option func(*Bridge)`, `func WithIdentifiers(allowed bool) Option` and `func New(port int, opts ...Option) (*Bridge, error)`. The Bridge gains `allowIdentifiers bool`.
  - `func (b *Bridge) identifiersAllowed(ctx context.Context) (bool, error)` returns `b.allowIdentifiers ||` the `/api/info` `demo` value. It is read per call, with no cache, because the dashboard can restart.
  - `breakdown` when not allowed:
    - `includeIp` is treated as false, so userIp is refused with an error containing "--mcp-allow-identifiers".
    - For the userId and userAgent dims, each row (and Other) gets `Value = "hidden-<1-based rank>"` and `Filter = nil`. Counts and stats are kept.
    - The response adds `identifiers:"hidden"` and `identifiersNote` (mcp.hidden).
  - When allowed, behaviour is as today, and `identifiers:"shown"` is added.
  - compare, requests, slowest and breakdown outputs add `"provenance"`.
  - `schema.go` descriptions mention the operator switch. Tool count is 10.
  - main.go:
    - Adds `--mcp-allow-identifiers`, an error unless `--mcp`.
    - Adds `--allow-broad-scans` and `--docs-examined-budget`, an error with `--mcp`; a negative budget is an error.
    - `store.New(store.WithBroadScans(..), store.WithDocsExaminedBudget(..))`.
    - Demo uses unchanged constructors.
- **Tests (write first):**
  - `TestBreakdownHidesUserIDValuesByDefault`: the fake dashboard has demo:false and returns userId rows "1001"; the output contains no "1001", no filter, and `identifiers == "hidden"`.
  - `TestBreakdownHidesUserAgentByDefault`.
  - `TestBreakdownIncludeIpIgnoredWhenSwitchOff`: includeIp:true gives an error naming the flag, with no IP in it.
  - `TestBreakdownShowsIdentifiersWithSwitch`: the existing includeIp:true test passes with `WithIdentifiers(true)`.
  - `TestBreakdownDemoUnaffected`: with demo:true and the switch off, the values are shown as today.
  - `TestToolCountStaysTen`.
  - `TestProvenanceForwarded` covers compare and breakdown.
  - main_test:
    - `TestMCPAllowIdentifiersRequiresMCP`.
    - `TestBroadScanFlagsRejectedWithMCP`.
    - `TestNegativeDocsExaminedBudgetRejected`.
    - `TestDefaultFlagsLeaveBroadScansOff`. Expose the store options via a small `storeOptions(flags) []store.Option` helper, and assert on it.
- **Verify:** `go test ./internal/mcp/... ./cmd/... && go vet ./...`
- **Model:** sonnet.
- **Reviewer checks:**
  - The default is OFF.
  - No identifier leaks through `filter`, `Other`, error text or `note`.
  - dropIPs is still applied.
  - The demo path is unchanged.
  - The flag names are exact.
  - No new routes are added to the `request` allow-list.

## T7. Web: provenance note and coverage panel

- **Files:** see the ownership table.
- **Interfaces:**
  - `types.ts` mirrors C2, C3 and C4 exactly.
  - `api.ts` gets `coverage(sourceId: string, start: string, end: string): Promise<CoverageReport | {refused: string}>`, which maps 409 to `{refused}`.
  - `provenance.ts` (pure):
    - `provenanceLine(p: Provenance): string` uses the Copy table ids.
    - `scanNote(p)`, `formatMargin(ms)`, `coverageRowsForDisplay(r)`.
  - `ProvenanceNote.svelte` renders one line per source under the comparison in App.svelte. It renders nothing when `provenance` is absent, so older servers are unaffected.
  - `CoveragePanel.svelte` in SourcePopover has a "Check coverage" button. It renders a table (kind × day, 4 field %) and the tenant attribution line, or the refusal text. It is never auto-run.
  - `deck.ts` gets the 5 glossary entries.
  - No hint-mode control is added (see Falsification).
- **Tests (write first)** in `web/tests/provenance.test.cjs`, loaded the same way `copylint.test.cjs` loads TS:
  - `provenanceLine complete/ran_to_end/partial/hint` asserts the exact Copy strings. The hint text includes the verbatim §6.4(2) sentence.
  - `every new string passes lintScreenStrings`: no causal verb next to Symptom or Correlation, and no "proven" or "cause".
  - `glossary entries for the 5 new terms exist with hint/definition/notMeaning`.
  - `coverage refusal renders cov.refused verbatim`.
  - `absent provenance renders nothing`.
- **Verify:** `cd web && node --test tests/*.test.cjs && npm run check`
- **Model:** sonnet. Tests use JSON fixtures written in the test file; there is no Go dependency.
- **Reviewer checks:**
  - Existing views render identically when `provenance` is absent.
  - No causal wording.
  - The verbatim §6.3 and §6.4 sentences are not paraphrased.
  - The census is never triggered automatically.
  - Strings live in the lib, not inline in components, so the lint sees them.

---

## Could not be verified from code

1. **Cost on a restored copy.** Elapsed time, docsExamined and bytes for the default comparison, the census and the calibration are all unmeasured. The falsification test and the §6.5(5) acceptance test cannot run, because no restored copy or lab is available and live use is excluded.
2. **Server-side cumulative maxTimeMS across getMore for `aggregate`.** The v2 driver omits maxTimeMS on getMore (`x/mongo/driver/batch_cursor.go:490`, verified). Whether the server keeps enforcing the original limit across later batches is not verified, so T2's cursor wrapper enforces it client-side.
3. **appName on the wire and in `currentOp`.** The plan asserts only the options. Seeing it in `currentOp` would need rights that are not being requested.
4. **`$collStats` permission** on the target under the existing read role. This only matters when `--docs-examined-budget` is set.
5. **The real `_id`-to-RequestInitiatedAt offset distribution.** The 60 s pad and the 1000-row minimum are placeholders until calibration runs on a restored copy.
6. **AccountId semantics** (T0.6 is still open). P1a reports only the non-null rate.
7. **MongoDB 3.4 behaviour** for coverage and hint. Both are refused or fall back on legacy, untested against a 3.4 fixture.
8. **Whether `debug.ReadBuildInfo().Main.Version` gives a useful version** in `build.ps1` builds. It may be "(devel)", in which case appName shows `Unravis/dev`.

## Open risks

- **"Complete" is unreachable on live.** §6.4(4) requires docsExamined to be printed, and docsExamined needs `explain`, which is banned on live. Live results therefore show `ran_to_end` at best. This is a new fourth label, and the owner should confirm it.
- **The §6.5 scan class conflicts with existing views.** On this instance every exact query is "broad", because only `_id_` is indexed. Literal §6.5(2) would refuse every live comparison. The plan refuses only the census, per the owner ruling's wording, and labels the other queries broad. The owner should confirm.
- **The 10 s interactive maxTimeMS will turn some current long-window queries into `query_timeout`.** That changes behaviour, though not meaning, for wide windows.
- **Per-source concurrency 1 queues parallel UI and MCP calls to the same source.** Analyze, breakdown and lists run serially, so latency goes up and nothing errors.
- **The calibration uses the newest 10k rows.** A margin learnt from recent rows may not fit old windows. The sample dates are shown, but they are not enforced.
- **MCP query echoes still contain model-supplied `userIds` and `userAgentContains`.** This is out of scope per the brief.
- **`requestsMode` rows from `audit_get_requests` and `audit_get_slowest`** do not include userId or userAgent (verified in `projectRecord`), so no change is needed there.
- **§8.2 says "Privileged ops: A1 only" but the owner ruling says none.** The plan uses none. The `$collStats` pre-check is off by default and needs review if the budget is ever set.
- **T1 must merge before T2–T7 start.** A slip in T1's JSON tags breaks T7's fixtures, so the reviewer diffs the tags against C2–C4.
