# Drill micro-slices (API drill A0-A9), for Haiku-class doers

Source: `.forge/investigation-timeline/drill/plan.md` (LOCKED). Nothing here re-opens a pick (N1-N6, P1-P5,
R1-R3). It only cuts the packets into slices of 1 file (2 at most) plus a test. All anchors were checked
on the dirty tree on Nov 12. Line numbers drift, so a doer finds the anchor by the quoted text, not by the number.

Ground rules for every slice:
- One shared tree: no worktrees, because rc.25-31 work is uncommitted. A slice touches only its listed files.
  Parallel slices in the same wave have disjoint files.
- Go: at most one writer per package at a time (`audit`, `store`, `server`, `keeper`). The import graph is
  `store -> audit`, `keeper -> store, audit` and `server -> keeper, store, audit`. A slice in a lower
  package can break compilation above it, so every Go slice ends with `go build ./... && go vet ./<pkg>/`
  before its package test.
- Go prefix: `export PATH="$PWD/.dev/toolchains/go/bin:$PATH"` (bash, repo root).
- Web: run in `web/`. A slice runs only its own test, `node --test tests/<file>.test.cjs`. `npm run check`
  and `npm test` run once at the end of each wave (gate G-web).
- New `.cjs` tests copy the TypeScript-loader preamble from `web/tests/backlog1.lanes.test.cjs` lines 10-24.
  Tests on `.svelte` files read the source as text, as `web/tests/tracingPaper.test.cjs` does.
- Never read `.env` or `sql/`. Never log record text. No commit unless the owner asks.
  Local install only: no tag, no release.
- Reviewer key: Haiku-low for mechanical work and copy, Sonnet-medium for logic, Opus for security
  (Reveal, full text, masking, raw query text). Opus security review happens once, at SEC-D3, over all
  A2 and Reveal diffs.

Fixed JSON shapes, so web work need not wait for C0:
```
RecordDetail.perf: null (old row -> "not recorded") | {
  hitCount, filterTotalMs, sqlCount, sqlMs, redisCount, redisMs, httpCount, httpMs   // number|null each
  filters:  [{name, ms}]  max 20, name <= 100 chars          | null
  slowSql:  [{count, ms}] max 3, no text                     | null
  slowHttp: [{count, ms}] max 3, no text                     | null
  steps:    [{name, ms}]  max 10, name <= 100 chars          | null
  pressure: [{name, value}] max 8, name <= 100 chars         | null
  textStored: boolean|null   // the includeCommandText marker
}
RequestRecord.light (activity rows only, omitempty): { sqlCount, sqlMs, redisMs, httpCount, httpMs }  // number|null each
POST /api/record-text  in:  { sourceId, id, list: "sql"|"http", index: 0|1|2, view: "preview"|"full" }
                       out: { stored: boolean, text?: string, cut: boolean }
```
A null field means "not recorded", never 0.

---

## 1. Checkpoints

| Demo | Owner sees | Local build | Slices |
|---|---|---|---|
| **D1** | The P2-P4 lane fixes: "Some imported files cover...", the year shown when it differs, and a disabled "not imported" chip with its reason. A red slow-call diamond opens the pop-up on **Overview** ("Where the time went", pressure, slow SQL/HTTP counts and ms). An old row says "not recorded". Esc closes it and focus returns; "Open in Analysis" works. | `powershell -File build.ps1 -Version 0.1.0-rc.32`, then the installer from `dist/` | C0, A0-P2go, A0-P5go, A0-P2web, A0-P3, A0-P4, A0-e2e, A1-audit, A1-demo, A1-perf, A1-wire, K1, W-copy, W-perf, W-target, W-hit, W-app, W-overview, W-chart, W-shell, W-ovui, W-paper, E-D1, I-D1 |
| **D2** | A **Nearby** tab: everyone within plus or minus 15 min (widen to 60/120), duration bands, per-row SQL/Redis/HTTP ms ("not recorded" on old rows), and chips for user and API. A chip click filters at once, shows "Asking the server..." and then "37 calls, 33 more than loaded". | `build.ps1 -Version 0.1.0-rc.33`, then the installer | A3-audit, A3-demo, A3-store, A1b-audit, A1b-store, K2, W-types2, W-chips, W-nearby, W-tab2, E-D2, I-D2 |
| **D3** | A **Breakdown** tab (this window). On each slow SQL/HTTP row: **Reveal** (500 chars, URL query values masked), **Show full text** (32 KB cap) and **Copy**. "not stored by the server" when the marker is off. The text is gone after close. Plus the clumsy-user review results. | `build.ps1 -Version 0.1.0-rc.34`, then the installer | A2-audit, A2-mask, A2-demo, A2-store, A2-server, A2-keeper, W-bdui, W-api3, W-reveal, W-revui, W-ovrev, W-tab3, SEC-D3, E-D3, A8, I-D3 |

P1 needs no code: the guard is kept and already pinned by `web/tests/backlog1.lanes.test.cjs:43`.
Do not touch it.

### Owner scripts

- **D1** (rc.32, demo data):
  1. Open a demo case, then Investigation Timeline, then Start tracing.
  2. Click a red diamond on the API lane. The pop-up opens on Overview.
  3. Read "Where the time went". Then click another diamond whose call has no timings: it says
     "not recorded".
  4. Press Esc. The pop-up closes and focus is back on the chart.
  5. Open the pop-up again and click "Open in Analysis". Analysis opens on that slot.
  6. On the Sources row, a "not imported" chip is greyed out with its reason underneath.
- **D2** (rc.33):
  1. Open a diamond, then the Nearby tab. The rows show SQL/Redis/HTTP ms.
  2. Click the user chip. The rows filter at once and "Asking the server for all of this user's calls..."
     shows, then "N calls, M more than loaded".
  3. Click the API chip, then Everyone.
  4. Click Widen to 60 minutes.
  5. Use VPN off or a stopped server for the failure line and Try again.
- **D3** (rc.34):
  1. Open a diamond, then the Breakdown tab. The 8 views show for this window.
  2. Overview, on a slow SQL row: click Reveal. The text is up to 500 characters with URL values masked.
  3. Click Show full text, then Copy.
  4. Close and reopen the pop-up. The text is gone, and Reveal is shown again.
  5. On a record whose server did not store text, Reveal says "not stored by the server".

---

## 2. Slices

The fields of each slice:
- **Outcome**
- **Edit**: exhaustive. Anything not listed is read-only.
- **Read**
- **Anchor**
- **Req**: numbered requirements.
- **Test**: file and command.
- **Doer** and **Reviewer**, each a model and effort.
- **Dep** and **Group**

**Standing copy rule (owner, 2026-11-12), applies to every UI slice and review:** all messages, labels, empty
states, errors and tooltips must be understandable by a beginner who has never done a performance
investigation. Plain words, jargon explained, each non-obvious control or section has an always-visible
hint line (not hover-only) taken from `DRILL_HELP` in `drillCopy.ts`, and every error says what to do next.
UI slices add their new strings to `DRILL_TEXT` / `DRILL_HELP`; every UI review has a "beginner can read it" item;
E-Dn and the owner script check the wording on screen.

**Carry-ins from the W-shell review (W-paper / a small fixer):**
- Key the pop-up on the slot (`{#key drillSlot}`) so a second diamond click reloads it.
- On Try again, put focus back on the dialog (the button disappears while focused).
- With zero focusable items the Tab trap must still `preventDefault`.
- Give Open in Analysis `title={DRILL_HELP.openAnalysis}` and the dialog title the `DRILL_HELP.title` hint.

### D1 slices

#### C0 - UAT field contract (owner plus planner, no code)
- **Outcome:** a value-free list of the Mongo field names and BSON types of the 19 fields in one new-shape
  UAT document, mapped to our role keys.
- **Edit:** `.forge/investigation-timeline/drill/uat-fields.md` (new).
- **Read:** `buildout/research/investigation-walkthrough.md`. It verifies only `PreActionFilterTotalMilliseconds`
  and `PreActionFilterTimings[{FilterName, ElapsedMilliseconds}]`.
- **Req:**
  1. The owner runs a names-and-types-only dump (no values) on one new-shape UAT row and pastes it.
  2. The planner fills a table that maps each role key to a Mongo path and type:
     - scalars: `hitCount`, `filterTotalMs`, `sqlCount`, `sqlMs`, `redisCount`, `redisMs`, `httpCount`, `httpMs`
     - lists: `filters` (name and ms subfields), `slowSql` and `slowHttp` (count, ms and text subfields),
       `steps` (name, ms), `pressure` (name, value)
     - the marker: `textStored`, from the `includeCommandText` marker
  3. Any role key with no UAT field is marked "absent". Its JSON stays null; that is not a blocker.
- **Test:** none (contract). A1-perf, A1b-store and A2-store copy the names from this file only.
- **Doer:** owner, then planner (Sonnet-medium). **Reviewer:** owner.
- **Dep:** none. **Group:** D1-W1.

#### A0-P2go - "some files have no range" on the lane (Go)
- **Outcome:** when some imported files have a range and some do not, the lane still sends the known
  range plus `importedPartial: true`.
- **Edit:** `internal/server/tracing.go` and `internal/server/tracing_importedspan_test.go`.
- **Read:** `internal/server/tracing_sqllive.go`.
- **Anchor:**
  - `type importedSpan struct` (around line 665) and its `add` method.
  - The span set `if sp := src.span[key]; sp != nil && sp.known {` (around line 618).
  - `ImportedFrom`/`ImportedTo` on `tracingLane` (around line 462).
- **Req:**
  1. Add `partial bool` to importedSpan.
  2. In `add`, a file with a nil end sets `partial = true` and `seen = true`. It leaves `from`/`to`/`known`
     as they were.
  3. `known` now means at least one file had a range. A file with a range widens `from`/`to` as today.
  4. Add `ImportedPartial bool \`json:"importedPartial,omitempty"\`` right after `ImportedTo` on tracingLane.
  5. At the span set, also set `lane.ImportedPartial = sp.partial` inside the `sp.known` branch.
  6. Update `TestImportedSpanAdd` (around lines 106-129) to the new rule.
  7. Add `TestImportedSpanMixedFiles`: one ranged file plus one nil-end file gives known, partial, the
     ranged from/to, and JSON containing `"importedPartial":true`.
  8. Run `go test -run TestTracingSQLIILanesGolden ./internal/server/`. If it fails only by an added
     `importedPartial`, regenerate it with `UPDATE_TRACING_GOLDEN=1` and say so in the hand-back.
     Any other golden diff means stop and report.
- **Test:** `go build ./... && go vet ./internal/server/ && go test -run 'TestImportedSpan|TestTracing' ./internal/server/`
- **Doer:** Haiku-low. **Reviewer:** Sonnet-medium (it changes the meaning of `known` and may move the golden).
- **Dep:** none. **Group:** D1-W1 (server writer #1).

#### A0-P5go - imported-SQL range on the stored-day lane
- **Outcome:** the held SQL lane (stored Query Store day) carries the same imported range as the
  imported-SQL lane.
- **Edit:** `internal/server/tracing_sqllive.go` and `internal/server/tracing_sqllive_test.go` (add a test
  to it, or create it if absent).
- **Read:** `internal/server/tracing.go` (the held swap, around lines 1099-1110).
- **Anchor:** `func tracingHeldSQLLane(` (around line 49) and `func layerSQLLane(` (around line 32).
- **Req:**
  1. When the held lane replaces or layers the imported SQL lane, copy `ImportedFrom`, `ImportedTo` and
     `ImportedPartial` from the imported lane onto the held lane if the held lane's own are nil/false.
  2. Change no state or colour.
  3. Test `TestHeldSQLLaneKeepsImportedRange`: an imported lane with from/to/partial plus a held lane
     gives a result lane with the same three values.
- **Test:** `go build ./... && go vet ./internal/server/ && go test -run 'TestHeldSQL|TestTracing' ./internal/server/`
- **Doer:** Haiku-low. **Reviewer:** Haiku-low (a field copy).
- **Dep:** A0-P2go (same package, uses ImportedPartial). **Group:** D1-W2 (server writer #2).

#### A0-P2web - "Some imported files cover..." wording
- **Outcome:** the lane line says "Some imported files cover X to Y; the other files have no recorded
  range." when `importedPartial` is set.
- **Edit:** `web/src/lib/tracing/model.ts` and `web/src/lib/tracing/lanes.ts`.
- **Test file:** `web/tests/drill.a0lanes.test.cjs` (new).
- **Read:** `web/tests/backlog1.lanes.test.cjs`, `web/src/lib/copy/copylint.ts` and `deck.ts`.
- **Anchor:**
  - `TracingLane` (model.ts around lines 61-93) and the parse `if (isText(value.importedFrom) && isText(value.importedTo)` (around line 246).
  - `COVER_TEXT` (lanes.ts around line 64), `laneImportedLine` (around line 69) and `laneCopyEntries` (around line 159).
- **Req:**
  1. Add `importedPartial?: true` to TracingLane. Set it in the parse only when `value.importedPartial === true`
     and from/to were accepted.
  2. Add ``some: (from: string, to: string) => `Some imported files cover ${from} to ${to}; the other files have no recorded range.` ``
     to COVER_TEXT.
  3. In laneImportedLine, when `lane.importedPartial`, return `COVER_TEXT.some(...)` in the cases that
     today return `outside(...)` (states 'none-seen' and 'out-of-range', and the emptyEvents guard).
     Leave the P1 guard as it is.
  4. Add `{ id: 'tracingRead.coverSome', ... }` to laneCopyEntries next to 'tracingRead.coverOutside'.
  5. Tests:
     - partial with 'out-of-range' gives the some-sentence
     - not partial is unchanged
     - parse ignores `importedPartial: "yes"`
     - `lintScreenStrings(laneCopyEntries(), deck.terms)` returns no problems
- **Test:** `cd web && node --test tests/drill.a0lanes.test.cjs tests/backlog1.lanes.test.cjs`
- **Doer:** Haiku-low. **Reviewer:** Haiku-low (copy plus a flag).
- **Dep:** none (the JSON name is fixed). **Group:** D1-W1.

#### A0-P3 - year in the imported range when it differs
- **Outcome:** the imported range text shows the year when the date's year differs from the window's.
- **Edit:** `web/src/components/TracingChart.svelte` only.
- **Test file:** `web/tests/drill.a0chart.test.cjs` (new).
- **Anchor:**
  - `showYear` and the `at` helper (around lines 50-51)
  - `importedLine` (around line 77)
  - `needsYear` (already in scope or imported; find it by name)
- **Req:**
  1. Add a local `spanAt(value: string)` that formats like `at`, with `year: showYear || needsYear(grid.firstMs, Date.parse(value), zone)`.
  2. Use spanAt, not at, for the from/to that importedLine passes to laneImportedLine.
  3. Change nothing else in the file.
  4. Source test: the file has `function spanAt(` and the importedLine expression uses `spanAt(`.
- **Test:** `cd web && node --test tests/drill.a0chart.test.cjs`
- **Doer:** Haiku-low. **Reviewer:** Haiku-low.
- **Dep:** none. **Group:** D1-W1 (TracingChart writer #1; W-chart waits for it).

#### A0-P4 - "not imported" chip disabled with its reason visible
- **Edit:** `web/src/components/TraceSources.svelte`.
- **Test file:** `web/tests/drill.a0sources.test.cjs` (new).
- **Anchor:** the chip `<button type="button" class="chip" data-source-id={chip.id}` (around line 36).
- **Req:**
  1. Set `disabled={disabled || chip.noFile === true}` on that button.
  2. Add `aria-describedby={chip.noFile ? 'ts-not-imported-reason' : undefined}`.
  3. Keep the `title` (TSRC-11 asserts /never imported/) and the `chip-not-imported` flag span.
  4. Below `.ts-chips` and inside the `{:else}` branch, add
     `{#if chips.some((c) => c.noFile)}<p class="ts-note" id="ts-not-imported-reason" data-testid="chip-not-imported-reason">{SOURCES_TEXT.notImportedTitle}</p>{/if}`.
  5. Leave "Select all" and "Clear all" unchanged. They still reach a disabled chip that is on.
  6. Source test: the disabled expression, the describedby, the reason paragraph and its testid.
- **Test:** `cd web && node --test tests/drill.a0sources.test.cjs`
- **Doer:** Haiku-low. **Reviewer:** Haiku-low.
- **Dep:** none. **Group:** D1-W1.

#### A0-e2e - TSRC-13/14
- **Edit:** `tools/e2e/specs/timeline-sources.spec.ts`.
- **Anchor:** after TSRC-12 (around line 327). Use the same `answerWith(page, body => ...)` mock.
- **Req:**
  1. **TSRC-13:** lane 0 gets `importedFrom: '2001-01-01T00:00:00Z'`, `importedTo: '2001-01-02T00:00:00Z'`,
     `importedPartial: true` and an out-of-range state. Expect the text 'Some imported files cover' and
     '2001'.
  2. **TSRC-14:** `body.sources[0].noFile = true`. Expect that chip `toBeDisabled()` and
     `getByTestId('chip-not-imported-reason')` visible.
  3. TSRC-11/12 stay green unchanged.
- **Test:** `cd tools/e2e && npm run e2e -- -Only TSRC-11,TSRC-12,TSRC-13,TSRC-14`
- **Doer:** Haiku-low. **Reviewer:** Haiku-low.
- **Dep:** A0-P2web, A0-P3, A0-P4, A0-P2go. **Group:** D1-W4.

#### A1-audit - perf types on the record detail
- **Edit:** `internal/audit/record_perf.go` (new) and `internal/audit/models.go` (one line).
- **Test file:** `internal/audit/record_perf_test.go` (new).
- **Anchor:** `type RecordDetail struct` (models.go around line 415).
- **Req:**
  1. Add `type RecordPerf struct`. Each scalar is `*float64` with these JSON tags and no omitempty:
     `hitCount`, `filterTotalMs`, `sqlCount`, `sqlMs`, `redisCount`, `redisMs`, `httpCount`, `httpMs`.
  2. Lists, also without omitempty:
     - `Filters []PerfNamed json:"filters"`, `Steps []PerfNamed json:"steps"` with `PerfNamed{Name string json:"name"; Ms *float64 json:"ms"}`
     - `SlowSQL []PerfSlow json:"slowSql"`, `SlowHTTP []PerfSlow json:"slowHttp"` with `PerfSlow{Count *float64 json:"count"; Ms *float64 json:"ms"}`
     - `Pressure []PerfValue json:"pressure"` with `PerfValue{Name string json:"name"; Value *float64 json:"value"}`
     - `TextStored *bool json:"textStored"`
  3. Constants: `PerfMaxFilters = 20`, `PerfMaxSlow = 3`, `PerfMaxSteps = 10`, `PerfMaxPressure = 8`,
     `PerfMaxName = 100`.
  4. In models.go, add `Perf *RecordPerf \`json:"perf"\`` as the last field of RecordDetail.
  5. Tests:
     - a nil Perf marshals `"perf":null`
     - a Perf with nil SQLMs marshals `"sqlMs":null` and never `0`
- **Test:** `go build ./... && go vet ./internal/audit/ && go test -run TestRecordPerf ./internal/audit/`
- **Doer:** Haiku-low. **Reviewer:** Haiku-low (types only).
- **Dep:** none. **Group:** D1-W1 (audit writer #1).

#### A1-demo - demo records carry perf
- **Edit:** `internal/audit/demo.go`.
- **Test file:** `internal/audit/demo_perf_test.go` (new).
- **Anchor:** the demo `Record` method (around line 584).
- **Req:**
  1. Records whose id hash or index is even get a deterministic Perf:
     - all 8 scalars
     - 2 filters, 3 slowSql, 2 slowHttp, 3 steps, 3 pressure
     - `TextStored` true on every fourth record, otherwise false
  2. Odd records keep `Perf: nil`.
  3. Values are fixed from the record id (no `time.Now`, no rand without a seed). The parts are below the
     record's duration.
  4. Test: two calls for the same id give equal Perf, and at least one demo id gives nil and one gives non-nil.
- **Test:** `go build ./... && go vet ./internal/audit/ && go test -run TestDemoPerf ./internal/audit/`
- **Doer:** Haiku-low. **Reviewer:** Haiku-low.
- **Dep:** A1-audit. **Group:** D1-W2 (audit writer #2).

#### A1-perf - perf projection and decode (store, new file)
- **Edit:** `internal/store/record_perf.go` (new).
- **Test file:** `internal/store/record_perf_test.go` (new).
- **Read:**
  - `internal/store/legacy_pipeline.go`: `boundedMetadataValue` (around line 143)
  - `internal/store/record.go`
  - `.forge/investigation-timeline/drill/uat-fields.md`
- **Req:**
  1. Add `func recordPerfProjection() bson.D`. It returns flat keys `_perfHitCount`, `_perfFilterTotalMs`,
     `_perfSqlCount`, `_perfSqlMs`, `_perfRedisCount`, `_perfRedisMs`, `_perfHttpCount`, `_perfHttpMs`,
     `_perfFilters`, `_perfSlowSql`, `_perfSlowHttp`, `_perfSteps`, `_perfPressure` and `_perfTextStored`.
     It is built from one table `[]perfField{key, mongoPath, kind}` filled from C0.
  2. Numeric expression: `$cond[ $or[ $eq[$type x,"double"], $eq[..,"int"], $eq[..,"long"], $eq[..,"decimal"] ], x, null ]`.
  3. Lists: `$cond[ $eq[$type x,"array"], $map{ input: $slice[x, N], as:"e", in:{...} }, null ]`.
     - slowSql/slowHttp `in` has only `count` and `ms` (numeric expressions). No text subfield is projected.
     - Names use `$cond[ $eq[$type n,"string"], $substrCP[n,0,100], null ]`.
  4. The marker is `$cond[ $eq[$type x,"bool"], x, null ]`.
  5. Use Mongo 3.4 operators only: no `$convert`, `$ifNull` on missing paths is fine.
  6. A role key that C0 marks "absent" projects the literal `null`.
  7. Add `func decodeRecordPerf(raw bson.Raw) *audit.RecordPerf`.
     - It returns nil when every `_perf*` key is missing or null.
     - It drops a number that is NaN, ±Inf or negative to nil.
     - It drops list entries whose name is empty after trim.
     - It caps list lengths again in Go.
  8. Tests:
     - old-shape raw (no fields) gives nil
     - new-shape gives all values
     - a string in `sqlMs` gives nil
     - an array of 5 slowSql is cut to 3
     - a 300-char filter name is cut to 100 runes
     - NaN and -1 give nil
     - the projection walk has no key named like text or command and only the operators $cond, $or, $eq,
       $type, $map, $slice and $substrCP
- **Test:** `go build ./... && go vet ./internal/store/ && go test -run 'TestRecordPerf|TestDecodeRecordPerf' ./internal/store/`
- **Doer:** Sonnet-medium. **Reviewer:** Sonnet-medium (Mongo 3.4 bounding and the old-row null rule).
- **Dep:** C0, A1-audit. **Group:** D1-W2 (store writer #1).

#### A1-wire - single-record pipeline uses perf
- **Edit:** `internal/store/record.go` and `internal/store/record_test.go`.
- **Anchor:**
  - `recordPipeline` (around lines 30-45). After the `boundedPayloadExpr` keys, append `recordPerfProjection()`'s elements.
  - In `Record()`, the `audit.RecordDetail{` literal (around lines 95-108). Add `Perf: decodeRecordPerf(raw),`.
- **Req:**
  1. Only recordPipeline changes. requestPipeline, the table and analysis are untouched.
  2. In `TestRecordProjectionListsOnlySchemaFields` (around line 30), keep `len(schemaFieldNames) != 21`
     and add the 14 `_perf*` keys to the expected key set.
  3. Add `TestRecordDetailPerfOldRow`, the existing record fixture with `Perf == nil`.
  4. Payload caps stay unchanged.
- **Test:** `go build ./... && go vet ./internal/store/ && go test -run 'TestRecord' ./internal/store/`
- **Doer:** Haiku-low. **Reviewer:** Haiku-low.
- **Dep:** A1-perf. **Group:** D1-W3 (store writer #2).

#### K1 - keeper carries perf
- **Edit:** `internal/keeper/audit_perf_test.go` (new, test only).
- **Read:**
  - `internal/keeper/audit.go`: `OpAuditRecord` forwarding (around line 448)
  - `internal/keeper/proto.go`
- **Req:** a test that sends a RecordDetail with Perf through the router's fake transport (copy the
  pattern of the existing keeper audit tests). The decoded value has equal Perf, and a nil Perf stays nil.
- **Test:** `go build ./... && go test -run TestKeeperRecordPerf ./internal/keeper/`
- **Doer:** Haiku-low. **Reviewer:** Haiku-low.
- **Dep:** A1-audit. **Group:** D1-W3.

#### W-copy - drill copy deck (all three demos)
- **Edit:** `web/src/lib/drill/drillCopy.ts` (new).
- **Test file:** `web/tests/drill.copy.test.cjs` (new).
- **Read:** `web/src/lib/copy/copylint.ts`, `deck.ts` and `web/src/lib/tracing/lanes.ts` (`laneCopyEntries` pattern).
- **Req:** export `DRILL_TEXT` with exactly these keys and strings, plus `drillCopyEntries()` that lists
  every plain string with an id `drill.<key>`.
  - **Pop-up and loading:**
    - `title: 'Slow call'`, `close: 'Close'`, `openAnalysis: 'Open in Analysis'`, `tabsLabel: 'Call details'`
    - `tabOverview: 'Overview'`, `tabNearby: 'Nearby'`, `tabBreakdown: 'Breakdown'`
    - `finding: 'Finding the slowest call in this slot...'`
    - `notFound: 'No single call was found for this slot. Open it in Analysis to see its calls.'`
    - `loadFailed: 'The call could not be loaded.'`, `retry: 'Try again'`
  - **Overview:**
    - `notRecorded: 'not recorded'`
    - `oldRow: 'This call was saved before the server recorded timings, so they are not recorded.'`
    - `whereTimeWent: 'Where the time went'`
    - `partSql: 'SQL'`, `partRedis: 'Redis'`, `partHttp: 'HTTP calls'`, `partFilters: 'Filters'`, `partOther: 'Other work'`
    - `pressureTitle: 'Server pressure at start'`, `slowSqlTitle: 'Slowest SQL commands'`,
      `slowHttpTitle: 'Slowest HTTP calls'`, `stepsTitle: 'Steps'`, `filtersTitle: 'Filters'`
    - ``hitCount: (n: number) => `Request number ${n} on this server since it started` ``
    - ``runs: (n: number) => n === 1 ? '1 run' : `${n} runs` ``
  - **Nearby:**
    - ``nearbyAround: (from: string, to: string) => `Calls from ${from} to ${to}` ``
    - `chipsLabel: 'Narrow the list'`, `chipEveryone: 'Everyone'`
    - ``chipUser: (id: number) => `Only user ${id}` ``, ``chipApi: (name: string) => `Only ${name}` ``
    - ``widen: (m: number) => `Widen to ${m} minutes` ``
    - `askingUser: "Asking the server for all of this user's calls..."`
    - `askingApi: 'Asking the server for all calls to this API...'`
    - ``merged: (total: number, more: number) => `${total} calls, ${more} more than loaded` ``
    - ``mergedNone: (total: number) => `${total} calls, none more than loaded` ``
    - ``truncated: (shown: number) => `Showing ${shown} of more` ``
    - `askFailed: 'The server could not be asked. The list shows only the calls already loaded.'`
    - `noNearby: 'No other calls in this window.'`
  - **Breakdown:** `breakdownWindow: 'This window: the call plus or minus 15 minutes'`
  - **Reveal:**
    - `reveal: 'Reveal'`, `showFull: 'Show full text'`, `copy: 'Copy'`, `copied: 'Copied'`
    - `notStored: 'not stored by the server'`, `revealFailed: 'The text could not be loaded.'`
    - `cutPreview: 'Cut at 500 characters.'`, `cutFull: 'Cut at 32 KB.'`
    - `revealNote: 'This text can hold real values. It is not saved and is cleared when this window closes.'`
  - Test: `lintScreenStrings(drillCopyEntries(), deck.terms)` gives no problems. If the lint rejects a
    word, change the word, keep the meaning, and note it in the hand-back. Every function returns the
    sample above for sample input.
- **Test:** `cd web && node --test tests/drill.copy.test.cjs`
- **Doer:** Haiku-low. **Reviewer:** Haiku-low (copy).
- **Dep:** none. **Group:** D1-W1.

#### W-perf - perf types and defensive parse (web)
- **Edit:** `web/src/lib/drill/perf.ts` (new).
- **Test file:** `web/tests/drill.perf.test.cjs` (new).
- **Read:** `web/src/lib/types.ts` (RecordDetail, around line 180).
- **Req:**
  1. Export `RecordPerf` mirroring the fixed JSON shape (number | null scalars, list item types,
     `textStored: boolean | null`).
  2. Export `type DrillRecord = RecordDetail & { perf: RecordPerf | null }`.
  3. `parsePerf(value: unknown): RecordPerf | null` returns null for a non-object.
     - It keeps only finite numbers ≥ 0; anything else becomes null.
     - It cuts lists to 20/3/3/10/8 and names to 100 chars.
     - It drops entries with no name.
  4. Do not edit `types.ts` in D1.
  5. Tests: null, `{}` (every field null), a string ms, a negative number, 5 slowSql cut to 3, and Infinity.
- **Test:** `cd web && node --test tests/drill.perf.test.cjs`
- **Doer:** Haiku-low. **Reviewer:** Haiku-low.
- **Dep:** none. **Group:** D1-W1.

#### W-target - slot to call
- **Edit:** `web/src/lib/drill/target.ts` (new).
- **Test file:** `web/tests/drill.target.test.cjs` (new).
- **Read:**
  - `web/src/lib/tracing/model.ts` (TracingWindow, TracingLane, `sourcesRead`, TracingSourceRef)
  - `web/src/lib/types.ts` (Query around line 60, Page)
  - `web/src/lib/guided/frame.ts:162` (slowQuery shape)
- **Req:**
  1. `slotWindow(window: TracingWindow, slot: number): { start: string; end: string }` uses
     `axisStart + slot*bucketSeconds`, ISO strings.
  2. `drillSourceIds(lane, sources)` returns the audit lane's `sourcesRead` ids when non-empty, else the
     ids of `sources` without `noFile`, deduplicated and capped at 50.
  3. `drillQuery(win, sourceIds): Query` sets:
     - `start`, `end`
     - `baselineStart = start - (end - start)`, `baselineEnd = start`
     - `kind: 'all'`, `outcome: 'all'`, `metric: 'total'`
     - `bucketSeconds: Math.max(10, round(width/1000))`, capped at 86400
     - no record filters
  4. `pickCall(page: Page | null): { sourceId: string; id: string } | null` returns the first row with
     non-empty `sourceId` and `id`, or null.
  5. `nearbyBounds(call: { initiatedAt: string | null; completedAt: string | null }, minutes = 15)` returns
     `{start, end}` as plus or minus minutes around `initiatedAt ?? completedAt`, or null.
  6. Tests:
     - slot 0 and the last slot
     - the sourcesRead fallback
     - the baseline equals the width
     - bucketSeconds is at least 10
     - an empty page gives null
     - nearbyBounds with both nulls gives null
- **Test:** `cd web && node --test tests/drill.target.test.cjs`
- **Doer:** Haiku-low. **Reviewer:** Sonnet-medium (time-base logic; it must agree with the server's slot maths).
- **Dep:** none. **Group:** D1-W1.

#### W-hit - diamond hit-test (pure)
- **Edit:** `web/src/lib/drill/hit.ts` (new).
- **Test file:** `web/tests/drill.hit.test.cjs` (new).
- **Req:**
  1. `hitFlagged(marks: { slot: number; left: number; top: number; right: number; bottom: number }[], x: number, y: number, pad = 6): number | null`
     returns the slot of the nearest mark whose rect inflated by `pad` contains (x, y), or null.
  2. Ties go to the smaller distance to the centre.
  3. Tests: inside, inside only by the pad, outside, two overlapping marks, and an empty list.
- **Test:** `cd web && node --test tests/drill.hit.test.cjs`
- **Doer:** Haiku-low. **Reviewer:** Haiku-low.
- **Dep:** none. **Group:** D1-W1.

#### W-app - modal guard for the pop-up
- **Edit:** `web/src/lib/appState.svelte.ts`.
- **Test file:** `web/tests/drill.appstate.test.cjs` (new).
- **Anchor:**
  - the `importPopup` state (around line 203)
  - `onWindowKey`: `if (importPopup) return;` (around line 1601)
  - the `modalOpen` expression (around line 1891)
- **Req:**
  1. Add `let drillOpen = $state(false);` next to importPopup and `export function setDrillOpen(v: boolean) { drillOpen = v; }`.
  2. In onWindowKey, add `if (drillOpen) return;` right after the importPopup guard.
  3. Add `|| drillOpen` to modalOpen.
  4. Nothing else changes.
  5. Source test for the three needles, with the drillOpen guard before `stepUpOnce` in onWindowKey.
- **Test:** `cd web && node --test tests/drill.appstate.test.cjs tests/tracingPaper.test.cjs`
- **Doer:** Haiku-low. **Reviewer:** Haiku-low (three lines).
- **Dep:** none. **Group:** D1-W1.

#### W-overview - where the time went (pure)
- **Edit:** `web/src/lib/drill/overview.ts` (new).
- **Test file:** `web/tests/drill.overview.test.cjs` (new).
- **Read:** `web/src/lib/drill/perf.ts` and `drillCopy.ts`.
- **Req:**
  1. `whereTimeWent(perf: RecordPerf | null, durationMs: number | null)` returns `null` when perf is
     null. Otherwise it returns `{ parts: { key: 'sql'|'redis'|'http'|'filters'|'other'; ms: number | null; share: number | null }[] }`.
  2. Other is `durationMs - sum(known parts)`, clamped ≥ 0, and null when durationMs is null.
  3. Share is `ms / durationMs` rounded to whole percent, null when either is null or durationMs is 0.
  4. A null part stays null; never use 0 for it.
  5. `slowLines(list)` returns `{ index, count, ms }[]` in server order.
  6. `pressureLines(perf)` returns name plus value.
  7. Tests:
     - old row gives null
     - all parts
     - parts larger than the duration give other = 0
     - a missing sqlMs stays null
     - durationMs null gives null shares
- **Test:** `cd web && node --test tests/drill.overview.test.cjs`
- **Doer:** Haiku-low. **Reviewer:** Sonnet-medium (null-not-zero rule and clamping).
- **Dep:** W-perf. **Group:** D1-W2.

#### W-chart - diamond click and Shift+Enter in the chart
- **Edit:** `web/src/components/TracingChart.svelte`.
- **Test file:** `web/tests/drill.chart.test.cjs` (new).
- **Read:** `web/src/lib/drill/hit.ts` and `web/src/lib/tracing/auditHero.ts` (HeroMark).
- **Anchor:**
  - props (around lines 27-40)
  - `onOverlayKey` (around line 194)
  - `onOverlayClick` (around line 346)
  - marks `<span class="mark" class:flagged={mark.flagged}` (around line 604)
- **Req:**
  1. Add prop `onDiamond?: (slot: number) => void`.
  2. Add `data-slot={mark.index}` to the mark span.
  3. In onOverlayClick, before `selectAt`:
     - when onDiamond is set, collect `.mark.flagged` rects in the audit lane by getBoundingClientRect
     - call `hitFlagged(rects, clientX, clientY, 6)`
     - on a hit, call `onDiamond(slot)` and return; else keep today's path
  4. Touch: keep today's first tap that shows the tip; a tap on a diamond opens directly.
  5. In onOverlayKey, `Shift+Enter` calls `onDiamond(current)` when the audit hero mark at `current` is flagged.
     Plain Enter is unchanged.
  6. With no `onDiamond`, behaviour is byte-identical.
  7. Source test: the prop, `data-slot`, the `hitFlagged(` call, the Shift+Enter branch, and that `onSelect(` is still called.
- **Test:** `cd web && node --test tests/drill.chart.test.cjs tests/drill.a0chart.test.cjs`
- **Doer:** Sonnet-medium. **Reviewer:** Sonnet-medium (pointer, touch and keyboard paths over a covering overlay).
- **Dep:** A0-P3 (same file), W-hit. **Group:** D1-W2 (TracingChart writer #2).

#### W-shell - DrillPopup shell (Overview only in D1)
- **Edit:** `web/src/components/DrillPopup.svelte` (new).
- **Test file:** `web/tests/drill.popup.test.cjs` (new).
- **Read:**
  - `web/src/lib/components/importPopup/ImportPopup.svelte` (focus at around line 61, Esc and Tab trap
    around 90-104, dialog attributes around 122)
  - `web/src/lib/api.ts` (`slowest` around line 238, `record` around line 245)
  - `drill/target.ts`, `drill/perf.ts` and `drillCopy.ts`
- **Req:**
  1. Props: `{ window: TracingWindow; slot: number; lane: TracingLane | undefined; sources: TracingSourceRef[]; zone: string; onClose: () => void; onOpenAnalysis: (slot: number) => void }`.
  2. On mount: `setDrillOpen(true)` and remember `document.activeElement`. On destroy: `setDrillOpen(false)`,
     abort, and give focus back to that element.
  3. Load in order:
     - `api.slowest(drillQuery(...), signal)`, then `pickCall`
     - none means `DRILL_TEXT.notFound` plus the Open in Analysis button
     - else `api.record(sourceId, id, signal)`, then `{ ...rec, perf: parsePerf(rec.perf) }`
     - one AbortController per load
     - on error: when `signal.aborted`, return; else show `DRILL_TEXT.loadFailed` plus Try again
  4. Markup: `role="dialog" aria-modal="true" aria-labelledby="drillTitle" tabindex="-1"`,
     `data-testid="drill-popup"`, a backdrop, a header with the title, the Close button and the Open in
     Analysis button.
  5. The tablist (`role="tablist" aria-label={DRILL_TEXT.tabsLabel}`) has one tab, Overview, built from
     a `tabs` array so D2/D3 add entries.
  6. The tab panel is a `{#snippet}` slot for `DrillOverview`, which comes in W-ovui. Until then render
     a placeholder `<div data-testid="drill-overview-slot"></div>`.
  7. Esc closes. The Tab trap is copied from ImportPopup.
  8. Phone layout: at `max-width: 720px` the dialog is full-width and full-height.
  9. No `localStorage`/`sessionStorage`. No import from `guided/` or `CallTrace`.
  10. Source test: the dialog attributes, setDrillOpen true/false, AbortController, `signal.aborted`,
      no storage, and no guided/callTrace import.
- **Test:** `cd web && node --test tests/drill.popup.test.cjs`
- **Doer:** Sonnet-medium. **Reviewer:** Sonnet-medium (focus, abort and modal logic).
- **Dep:** W-app, W-target, W-perf, W-copy. **Group:** D1-W2.

#### W-ovui - Overview tab component
- **Edit:** `web/src/lib/components/drill/DrillOverview.svelte` (new).
- **Test file:** `web/tests/drill.ovui.test.cjs` (new).
- **Read:** `drill/overview.ts`, `drill/perf.ts` and `drillCopy.ts`.
- **Req:**
  1. Prop `{ record: DrillRecord }`.
  2. When `record.perf === null`, show only `<p data-testid="drill-old-row">{DRILL_TEXT.oldRow}</p>`.
  3. Otherwise show:
     - `whereTimeWent` as a list of rows (label, ms, share%) plus a plain bar (`width: share%`). A null
       part shows `DRILL_TEXT.notRecorded`.
     - pressure lines
     - Slowest SQL and Slowest HTTP rows (`runs(count)`, ms), one row per entry with `data-testid="drill-slow-sql-{index}"`
       and `drill-slow-http-{index}`
     - steps and filters
     - hitCount when not null
  4. No `{@html}`. Text is plain interpolation.
  5. Source test: the old-row branch, the notRecorded use, the testids, and no `{@html`.
- **Test:** `cd web && node --test tests/drill.ovui.test.cjs`
- **Doer:** Haiku-low. **Reviewer:** Haiku-low (render only; the logic is in overview.ts).
- **Dep:** W-overview. **Group:** D1-W3.

#### W-paper - mount the pop-up from the timeline
- **Edit:** `web/src/components/TracingPaper.svelte` and `web/src/components/DrillPopup.svelte` (replace
  the placeholder with `<DrillOverview record={...} />`; static import of DrillOverview is fine in the pop-up).
- **Test file:** `web/tests/drill.paper.test.cjs` (new).
- **Anchor:**
  - TracingPaper imports (around lines 2-28)
  - `function onBarSelect(` (around line 369)
  - `<TracingChart` (around line 512)
- **Req:**
  1. Add `const DrillPopup = lazyComponent(() => import('./DrillPopup.svelte'));`.
  2. Add `let drillSlot = $state<number | null>(null);`.
  3. Pass `onDiamond={(slot) => { drillSlot = slot; }}` to TracingChart always. The drill only reads, so
     it is also on for read-only and closed cases.
  4. After the chart, add `{#if drillSlot !== null && result}<DrillPopup window={result.window} slot={drillSlot} lane={auditLane} sources={...} {zone} onClose={() => (drillSlot = null)} onOpenAnalysis={(s) => { drillSlot = null; onBarSelect(s); }} />{/if}`.
     Use the names already in scope for the audit lane, sources and zone; find them in the file.
  5. Clear drillSlot when a new run starts.
  6. tracingPaper.test.cjs stays green: no `from '...guided|callTrace'`, and DrillPopup is not imported statically.
  7. Source test: the lazyComponent line, the onDiamond prop, onOpenAnalysis calls onBarSelect, and
     `^\s*import\s+DrillPopup\b` is absent.
- **Test:** `cd web && node --test tests/drill.paper.test.cjs tests/tracingPaper.test.cjs`
- **Doer:** Haiku-low. **Reviewer:** Haiku-low.
- **Dep:** W-chart, W-shell, W-ovui. **Group:** D1-W4.

#### E-D1 - Playwright DRILL-01..03
- **Edit:** `tools/e2e/specs/timeline-drill.spec.ts` (new).
- **Read:** `tools/e2e/specs/timeline-sources.spec.ts` (openTimeline, answerWith and route helpers).
- **Req:**
  1. **DRILL-01:** open the demo case timeline, click the first `.mark.flagged` (by bounding box centre),
     and expect `getByTestId('drill-popup')` and 'Where the time went'.
  2. **DRILL-02:** `page.route('**/api/record')` fulfils with `perf: null`. Expect `getByTestId('drill-old-row')`.
  3. **DRILL-03:** Esc closes the dialog, and focus is inside the chart region. "Open in Analysis" shows
     Analysis.
- **Test:** `cd tools/e2e && npm run e2e -- -Only DRILL-01,DRILL-02,DRILL-03,TSRC-11,TSRC-12,TSRC-13,TSRC-14`
- **Doer:** Sonnet-medium. **Reviewer:** Haiku-low.
- **Dep:** W-paper, A1-demo, A1-wire, A0-e2e. **Group:** D1-W5.

#### I-D1 - local install rc.32
- **Req:**
  1. Run the full gates (G-go and G-web, section 3).
  2. Build with `powershell -File build.ps1 -Version 0.1.0-rc.32`, then run the installer from `dist/`.
  3. Hand the owner the D1 script. No tag, no release, no push.
- **Doer:** main session. **Dep:** E-D1.

### D2 slices

#### A3-audit - optional user and API on ActivityInput
- **Edit:** `internal/audit/activity.go`.
- **Test file:** `internal/audit/activity_filter_test.go` (new).
- **Anchor:** `type ActivityInput struct` (around line 11) and `ActivityQuery` (around line 22, the `Query{...}` literal around line 31).
- **Req:**
  1. Add `UserID *int \`json:"userId,omitempty"\`` and `Operation string \`json:"operation,omitempty"\``.
  2. Validate in ActivityQuery before ValidateQuery:
     - UserID < 0 is an error
     - Operation over 200 bytes, or containing `$`, NUL, `{` or `}`, or without exactly one `.`
       separating a non-empty Controller and Action, is an error
     - error text names the field and never echoes the value
  3. Map `q.UserIDs = []int{*in.UserID}` and `q.Operation = in.Operation`. The Normalized step splits it
     (models.go line 67).
  4. Unset fields give the exact Query of today.
  5. Tests:
     - unfiltered Query is deep-equal to before
     - user only, op only, both
     - `$where`, `a.b.c` and `{}` are refused
     - a value over 200 bytes is refused
     - a JSON object for `operation` fails to decode
- **Test:** `go build ./... && go vet ./internal/audit/ && go test -run 'TestActivity' ./internal/audit/`
- **Doer:** Haiku-low. **Reviewer:** Sonnet-medium (hostile-input validation).
- **Dep:** A1-demo (audit writer free). **Group:** D2-W1.

#### A3-demo - demo activity honours the chips
- **Edit:** `internal/audit/activity_demo.go`.
- **Test file:** `internal/audit/activity_demo_filter_test.go` (new).
- **Anchor:** the demo `Activity` method (around line 13).
- **Req:**
  1. After today's window filter, keep only rows matching UserID (when set) and Controller.Action (when set).
  2. Limits and the cursor are unchanged.
  3. Test: a filtered result is a subset of the unfiltered one and every row matches.
- **Test:** `go build ./... && go test -run TestDemoActivity ./internal/audit/`
- **Doer:** Haiku-low. **Reviewer:** Haiku-low.
- **Dep:** A3-audit. **Group:** D2-W2 (audit writer #2).

#### A3-store - the modern overlap branch applies the filters
- **Edit:** `internal/store/activity.go`.
- **Test file:** `internal/store/activity_filter_test.go` (new).
- **Read:**
  - `internal/store/filters.go` (`recordMatch` around line 20)
  - `internal/store/requests.go` (requestPipelineOrdered around line 411, mode select around lines 244-254)
  - `internal/store/legacy_pipeline.go` (legacyRecordFilterMatches)
- **Anchor:** `func activityPipeline(` and `pipeline[0] = bson.D{{Key: "$match", Value: bson.D{{Key: "$or", Value: bson.A{existing, overlap}}}}}`.
- **Req:**
  1. `nq := q.Normalized()`.
  2. Build `filters` from `ControllerName == nq.Controller` (when non-empty), `ActionName == nq.Action`
     (when non-empty) and `recordMatch(nq)...`.
  3. With filters non-empty, set `pipeline[0] = $match {$and: [ {$or:[existing, overlap]}, filters... ]}`.
     Otherwise leave the shape byte-identical.
  4. Use exact $eq matches only: no $regex.
  5. Tests:
     - unfiltered pipeline deep-equals the current output (capture it first)
     - user filter adds a userId match inside `$and`
     - op filter adds ControllerName and ActionName
     - the legacy Go-side matcher accepts and rejects the same two cases
- **Test:** `go build ./... && go vet ./internal/store/ && go test -run 'TestActivity' ./internal/store/`
- **Doer:** Sonnet-medium. **Reviewer:** Sonnet-medium (today the overlap `$or` branch skips filters; the index path matters).
- **Dep:** A3-audit, A1-wire (store writer free). **Group:** D2-W2 (store writer #3).

#### A1b-audit - light totals type
- **Edit:** `internal/audit/light.go` (new) and `internal/audit/models.go` (one line).
- **Test file:** `internal/audit/light_test.go` (new).
- **Anchor:** `type RequestRecord struct` (models.go around lines 369-397).
- **Req:**
  1. Add `type LightTotals struct { SQLCount, SQLMs, RedisMs, HTTPCount, HTTPMs *float64 }` with tags
     `sqlCount`, `sqlMs`, `redisMs`, `httpCount` and `httpMs`, each without omitempty.
  2. On RequestRecord add `Light *LightTotals \`json:"light,omitempty"\`` as the last field.
  3. Test: a nil Light is omitted, and a LightTotals with a nil field marshals `null`.
- **Test:** `go build ./... && go vet ./internal/audit/ && go test -run TestLight ./internal/audit/`
- **Doer:** Haiku-low. **Reviewer:** Haiku-low.
- **Dep:** A3-demo (audit writer free). **Group:** D2-W3 (audit writer #3).

#### A1b-store - five numbers on activity rows only
- **Edit:** `internal/store/activity.go` and `internal/store/requests.go`. The legacy row build in
  `internal/store/legacy_pipeline.go` is a third file, allowed for this slice only because the same five
  keys must decode on both paths.
- **Test file:** `internal/store/activity_light_test.go` (new).
- **Read:** `.forge/investigation-timeline/drill/uat-fields.md` and `internal/store/record_perf.go` (the numeric expression).
- **Anchor:**
  - activityPipeline's `$project` stage
  - requests.go modern decode `var doc requestDocument` then `pageRow{request: audit.RequestRecord{` (around lines 261-277)
  - legacy_pipeline.go row build (around line 546)
- **Req:**
  1. Add the keys `_lightSqlCount`, `_lightSqlMs`, `_lightRedisMs`, `_lightHttpCount` and `_lightHttpMs`
     to the activity pipeline only, with the numeric `$cond` from A1-perf (reuse its helper).
  2. Decode them into `Light`. When all five are null, Light stays nil (old row).
  3. No arrays, no names (R2).
  4. requestPipeline output for every other mode is byte-identical; a test deep-equals the
     slowest/table pipelines.
  5. Tests:
     - an old-shape row gives nil Light
     - a new-shape row gives five values
     - a string value gives nil
     - the legacy row gives the same
- **Test:** `go build ./... && go vet ./internal/store/ && go test -run 'TestActivity|TestRequest|TestLegacy' ./internal/store/`
- **Doer:** Sonnet-medium. **Reviewer:** Sonnet-medium (two decode paths, and the other modes must stay unchanged).
- **Dep:** A1b-audit, A3-store, C0. **Group:** D2-W4 (store writer #4).

#### K2 - keeper carries the chip fields and light totals
- **Edit:** `internal/keeper/audit_activity_test.go` (new, test only).
- **Req:**
  1. ActivityInput with userId and operation reaches the fake repository unchanged.
  2. Rows with Light round-trip.
  3. A nil Light stays absent.
- **Test:** `go build ./... && go test -run TestKeeperActivity ./internal/keeper/`
- **Doer:** Haiku-low. **Reviewer:** Haiku-low.
- **Dep:** A1b-audit, A3-audit. **Group:** D2-W4.

#### W-types2 - web types for chips and light totals
- **Edit:** `web/src/lib/types.ts`.
- **Test file:** `web/tests/drill.types.test.cjs` (new).
- **Anchor:** `export interface ActivityInput` (around line 181) and `export interface RequestRecord` (around line 166).
- **Req:**
  1. Add `userId?: number; operation?: string` to ActivityInput.
  2. Add `export interface LightTotals { sqlCount: number | null; sqlMs: number | null; redisMs: number | null; httpCount: number | null; httpMs: number | null }`.
  3. Add `light?: LightTotals` to RequestRecord.
  4. Source test for the three needles.
- **Test:** `cd web && node --test tests/drill.types.test.cjs`, then `npm run check`.
- **Doer:** Haiku-low. **Reviewer:** Haiku-low.
- **Dep:** none. **Group:** D2-W1.

#### W-chips - chip logic (pure)
- **Edit:** `web/src/lib/drill/nearbyChips.ts` (new).
- **Test file:** `web/tests/drill.chips.test.cjs` (new).
- **Read:** `web/src/lib/guided/nearby.ts` (NEARBY_ROWS=100, durationBand) and `drillCopy.ts`.
- **Req:**
  1. `type Chip = { kind: 'everyone' } | { kind: 'user'; userId: number } | { kind: 'api'; operation: string }`.
  2. `chipsFor(call: RequestRecord): Chip[]` gives everyone, plus user when `userId` is a number, plus api
     as `controller + '.' + action` when both are non-empty.
  3. `matches(row, chip)` is exact equality only.
  4. `filterLoaded(rows, chip)`.
  5. `activityBody(base: ActivityInput, chip)` adds userId or operation.
  6. `mergeMessage(loadedMatches: number, server: { rows: number; truncated: boolean })` returns:
     - `truncated(rows)` when truncated
     - else `merged(total, total - loadedMatches)` when that is > 0
     - else `mergedNone(total)`
  7. `lightText(light)` gives `notRecorded` when light is undefined or all-null.
  8. `createSeq()` returns `{ next(): number; isLatest(n): boolean }`.
  9. Tests:
     - chips for a row with no userId
     - exact match (`5` ≠ `50`)
     - the three merge cases
     - the stale seq is ignored
     - lightText for an old row
- **Test:** `cd web && node --test tests/drill.chips.test.cjs`
- **Doer:** Haiku-low. **Reviewer:** Sonnet-medium (merge and stale-answer logic).
- **Dep:** W-types2. **Group:** D2-W2.

#### W-nearby - Nearby tab component
- **Edit:** `web/src/lib/components/drill/DrillNearby.svelte` (new).
- **Test file:** `web/tests/drill.nearby.test.cjs` (new).
- **Read:**
  - `web/src/lib/components/guided/NearbyActivity.svelte` (the api.activity pattern)
  - `web/src/lib/guided/nearby.ts` (reused unchanged)
  - `drill/nearbyChips.ts`, `drill/target.ts` (`nearbyBounds`) and `drillCopy.ts`
- **Req:**
  1. Props `{ record: DrillRecord; sourceIds: string[]; zone: string }`.
  2. The window is `nearbyBounds(record, minutes)`, with minutes 15 → 60 → 120 through the widen buttons.
     It is capped by `MAX_SPAN_MINUTES`.
  3. The first load is `api.activity({sourceIds, start, end, cursor:'', limit: NEARBY_ROWS}, signal)`.
  4. A chip click:
     - (a) shows `filterLoaded` at once
     - (b) shows `askingUser`/`askingApi` with `role="status"`
     - (c) aborts the previous request and sends `activityBody`
     - (d) on answer, checks `isLatest`, replaces the rows and shows `mergeMessage`
     - (e) on error (not aborted), keeps the loaded filtered rows and shows `askFailed` plus Try again
  5. Each row shows time, controller.action, durationBand, user, and `lightText`.
  6. `more = !!page?.nextCursor || !!page?.truncated` gives the `truncated` line.
  7. The component aborts on destroy.
  8. It must not edit `nearby.ts` or any guided file.
  9. Source test: AbortController, `isLatest(`, `askFailed`, `role="status"`, no edit markers in guided.
- **Test:** `cd web && node --test tests/drill.nearby.test.cjs`
- **Doer:** Sonnet-medium. **Reviewer:** Sonnet-medium (hybrid load and race handling).
- **Dep:** W-chips. **Group:** D2-W3.

#### W-tab2 - Nearby tab in the pop-up
- **Edit:** `web/src/components/DrillPopup.svelte`.
- **Test file:** `web/tests/drill.popup.test.cjs` (extend).
- **Req:**
  1. Add `{ id: 'nearby', label: DRILL_TEXT.tabNearby }` to `tabs`.
  2. Render `<DrillNearby record={...} sourceIds={...} {zone} />` only while active (`{#if}`).
  3. Arrow-key tab switching follows the tablist pattern.
  4. The test asserts two tabs and the DrillNearby mount.
- **Test:** `cd web && node --test tests/drill.popup.test.cjs`
- **Doer:** Haiku-low. **Reviewer:** Haiku-low.
- **Dep:** W-nearby. **Group:** D2-W4.

#### E-D2 - Playwright DRILL-04/05
- **Edit:** `tools/e2e/specs/timeline-drill.spec.ts`.
- **Req:**
  1. **DRILL-04:** open a diamond, then Nearby. Route `**/api/activity`: the first call passes through; the
     call with `userId` waits until the test sees "Asking the server", then fulfils with more rows.
     Expect `/\d+ calls, \d+ more than loaded/`.
  2. **DRILL-05:** the chip call fulfils with status 500. Expect 'The server could not be asked' and the
     rows still listed.
- **Test:** `cd tools/e2e && npm run e2e -- -Only DRILL-01,DRILL-04,DRILL-05`
- **Doer:** Sonnet-medium. **Reviewer:** Haiku-low.
- **Dep:** W-tab2, A3-store, A3-demo, A1b-store. **Group:** D2-W5.

#### I-D2 - local install rc.33
Same steps as I-D1, with `-Version 0.1.0-rc.33`. The owner also times one 24 h, 100-row Nearby on UAT
(the A1b timing proof).

### D3 slices

#### A2-audit - Reveal contract
- **Edit:** `internal/audit/record_text.go` (new).
- **Test file:** `internal/audit/record_text_test.go` (new).
- **Req:**
  1. Add `RecordTextInput{SourceID string json:"sourceId"; ID string json:"id"; List string json:"list"; Index int json:"index"; View string json:"view"}`.
  2. Add `RecordText{Stored bool json:"stored"; Text string json:"text,omitempty"; Cut bool json:"cut"}`.
  3. Constants: `RecordTextMaxBytes = 32768`, `RecordTextPreviewRunes = 500`.
  4. `ValidateRecordTextInput` checks:
     - SourceID non-empty and ≤ 200 bytes
     - ID is 24 hex chars
     - List is in {sql, http}
     - Index is in 0..2
     - View is in {preview, full}
     - each error names the field, never the value
  5. Add `type RecordTextReader interface { RecordText(ctx context.Context, in RecordTextInput) (RecordText, error) }`.
  6. Add `var ErrRecordTextNotFound = errors.New("record text: entry not found")`.
  7. Tests: every bad field and a good input.
- **Test:** `go build ./... && go vet ./internal/audit/ && go test -run TestRecordText ./internal/audit/`
- **Doer:** Sonnet-medium. **Reviewer:** Sonnet-medium, plus Opus at SEC-D3.
- **Dep:** A1b-audit (audit writer free). **Group:** D3-W1.

#### A2-mask - URL query value masking (pure)
- **Edit:** `internal/audit/mask.go` (new).
- **Test file:** `internal/audit/mask_test.go` (new).
- **Req:**
  1. `MaskQueryValues(s string, wholeIsURL bool) string`.
  2. In every `http://` or `https://` URL (case-insensitive) inside s, replace each query value with `***`.
     - A value ends at `&`, `#`, whitespace, `'`, `"`, `)` or end of the URL.
     - Keys stay.
     - A fragment is replaced whole by `#***`.
  3. When `wholeIsURL` is true (http entries), the same applies from the first `?` even without a scheme.
  4. Never panic on invalid UTF-8.
  5. Tests:
     - `https://x/a?k=v&t=1` gives `https://x/a?k=***&t=***`
     - `?token=abc` with wholeIsURL
     - a SQL string with an embedded URL
     - `HTTPS://` in caps
     - no `?` means the text is unchanged
     - an empty value `k=` stays `k=`
     - a key with no `=`
     - invalid UTF-8
- **Test:** `go test -run TestMaskQueryValues ./internal/audit/`
- **Doer:** Sonnet-medium. **Reviewer:** Opus at SEC-D3 (masking). Sonnet-medium first pass.
- **Dep:** A2-audit (same package). **Group:** D3-W2.

#### A2-demo - demo Reveal
- **Edit:** `internal/audit/demo_record_text.go` (new).
- **Test file:** `internal/audit/demo_record_text_test.go` (new).
- **Req:**
  1. The demo repo implements RecordTextReader.
  2. It returns `{stored:false}` when the demo Perf `TextStored` is not true.
  3. Otherwise:
     - SQL text is a fixed fake `SELECT ... FROM PayrollRun WHERE Id = 42 -- https://svc/x?key=demo&id=7`
       padded to 2000 runes
     - HTTP text is `https://svc.example/api/pay?emp=123&token=demo`
     - the preview and full caps and MaskQueryValues apply
  4. An index beyond the list gives ErrRecordTextNotFound.
  5. Tests: stored false, preview is 500 runes plus cut, masked, full not cut, bad index.
- **Test:** `go build ./... && go test -run TestDemoRecordText ./internal/audit/`
- **Doer:** Haiku-low. **Reviewer:** Haiku-low, plus Opus at SEC-D3.
- **Dep:** A2-mask, A1-demo. **Group:** D3-W3.

#### A2-store - second small pipeline
- **Edit:** `internal/store/record_text.go` (new).
- **Test file:** `internal/store/record_text_test.go` (new).
- **Read:**
  - `internal/store/record.go` (source lookup and ObjectID parse in `Record()`)
  - `record_perf.go`
  - `uat-fields.md`
- **Req:**
  1. Add `recordTextPipeline(id, list, index)`: `$match _id`, `$limit 1`, `$project { _t, _s }`.
     - `_t = $let{ vars:{e: $arrayElemAt[ $cond[$eq[$type F,"array"], F, []], index ]}, in: $cond[ $eq[$type "$$e.<textSub>","string"], $substrCP["$$e.<textSub>",0,32768], null ] }`
     - `_s` is the marker
  2. Use Mongo 3.4 operators only.
  3. In Go:
     - trim to ≤ 32768 bytes on a rune boundary
     - preview: ≤ 500 runes
     - `Cut` is true when trimmed at either step
     - `MaskQueryValues(text, list == "http")`
  4. Results:
     - marker not true gives `{Stored:false}` and no text
     - a missing entry, or a missing list while the marker is true, gives ErrRecordTextNotFound
     - an unknown source gives the same refusal as `Record()`
  5. Never log, and never wrap the text into an error.
  6. Tests use a pipeline shape plus a decode helper on bson.Raw fixtures:
     - preview cap, full cap
     - masked http and SQL URL
     - marker false
     - index 2 of a 2-entry list
     - a wrong-type text gives not found
- **Test:** `go build ./... && go vet ./internal/store/ && go test -run TestRecordText ./internal/store/`
- **Doer:** Sonnet-medium. **Reviewer:** Opus at SEC-D3 (raw query text). Sonnet-medium first pass.
- **Dep:** A2-mask, A1b-store (store writer free), C0. **Group:** D3-W3 (store writer #5).

#### A2-server - handler and route
- **Edit:** `internal/server/record_text.go` (new) and `internal/server/routes.go` (one entry).
- **Test file:** `internal/server/record_text_test.go` (new).
- **Read:** `internal/server/slowest.go` (handler and type-assert pattern) and `server.go:761` (`record`).
- **Anchor:** routes.go `{pattern: "/api/record", match: exactPath("/api/record"), methods: post((*handler).record)}` (around line 220).
- **Req:**
  1. Add `{pattern: "/api/record-text", match: exactPath("/api/record-text"), methods: post((*handler).recordText)}` after /api/record.
  2. The handler:
     - decodes with a small body limit (4 KB)
     - runs ValidateRecordTextInput (400 with a fixed message)
     - checks `h.repo.(audit.RecordTextReader)` (501 when absent)
     - reads with the request context
     - maps ErrRecordTextNotFound to 404 with a fixed message, and other errors to 500 with a fixed message
  3. Every response sets `Cache-Control: no-store` and `Pragma: no-cache`.
  4. No applog call with input or output. The access log must not log bodies; check the middleware and
     state the result in the hand-back.
  5. Tests:
     - 200 and its headers
     - the normal `/api/record` body has no `text` key
     - 400 per field with a body that never contains the input value
     - 404 and 500 bodies never contain the fake text
     - a captured log buffer never contains the fake text
- **Test:** `go build ./... && go vet ./internal/server/ && go test -run 'TestRecordText|TestRecord' ./internal/server/`
- **Doer:** Sonnet-medium. **Reviewer:** Opus at SEC-D3. Sonnet-medium first pass.
- **Dep:** A2-audit, A0-P5go (server writer free). **Group:** D3-W2 (server writer #3).

#### A2-keeper - keeper op for Reveal
- **Edit:** `internal/keeper/proto.go` and `internal/keeper/audit.go`. The client side in
  `internal/keeper/auditrepo.go` is a third file, allowed: one op needs all three.
- **Test file:** `internal/keeper/record_text_test.go` (new).
- **Anchor:** proto.go `OpAuditRecord = "audit.record"` (around line 129) and audit.go `r.read(Op…` (around line 448).
- **Req:**
  1. Add `OpAuditRecordText = "audit.recordText"`.
  2. The router forwards it to `RecordTextReader` the same way it forwards OpAuditRecord. The audit
     repository client implements `RecordText` through `r.read(OpAuditRecordText, in, &out)`.
  3. Validate the input again on the keeper side.
  4. Nothing is logged, and errors carry fixed text.
  5. Tests: a round trip, a bad input is refused before forwarding, and an error body has no text.
- **Test:** `go build ./... && go vet ./internal/keeper/ && go test -run TestKeeperRecordText ./internal/keeper/`
- **Doer:** Sonnet-medium. **Reviewer:** Opus at SEC-D3.
- **Dep:** A2-audit, A2-store. **Group:** D3-W4.

#### W-bdui - Breakdown tab component
- **Edit:** `web/src/lib/components/drill/DrillBreakdown.svelte` (new).
- **Test file:** `web/tests/drill.breakdown.test.cjs` (new).
- **Read:** `web/src/components/BreakdownPanel.svelte` (props `{ query, sourceLabels, onDrill, lockedDim?, panelId?, heading? }`) and `drill/target.ts`.
- **Req:**
  1. Props `{ record: DrillRecord; sourceIds: string[]; sourceLabels: Map<string,string>; onOpenAnalysis: () => void }`.
  2. `query = drillQuery(nearbyBounds(record, 15), sourceIds)`.
  3. Render `<p>{DRILL_TEXT.breakdownWindow}</p><BreakdownPanel {query} {sourceLabels} panelId="drill-breakdown" onDrill={() => onOpenAnalysis()} />`.
  4. Leave BreakdownPanel unchanged. Its 7-day limit message stays as it is.
  5. Show nothing when nearbyBounds is null; show the notRecorded line instead.
  6. Source test: panelId, onDrill mapping, and no edit to BreakdownPanel (the test reads its git-clean
     props line).
- **Test:** `cd web && node --test tests/drill.breakdown.test.cjs`
- **Doer:** Haiku-low. **Reviewer:** Haiku-low.
- **Dep:** W-target. **Group:** D3-W1.

#### W-api3 - client call for Reveal
- **Edit:** `web/src/lib/api.ts` and `web/src/lib/drill/reveal.ts` (new; types only in this slice).
- **Test file:** `web/tests/drill.api.test.cjs` (new).
- **Anchor:** `record(sourceId, id, signal)` (around line 245).
- **Req:**
  1. In reveal.ts, export `RecordTextInput` and `RecordText` per the fixed shape.
  2. In api.ts, add `recordText: (input: RecordTextInput, signal?: AbortSignal) => request<RecordText>('/record-text', { ...json(input), cache: 'no-store' }, signal)`.
     Match the local `request` signature; read it first.
  3. No caching wrapper, no logging.
  4. Source test: the path, `no-store`, and no `console.` in reveal.ts.
- **Test:** `cd web && node --test tests/drill.api.test.cjs`
- **Doer:** Haiku-low. **Reviewer:** Haiku-low, plus Opus at SEC-D3.
- **Dep:** none. **Group:** D3-W1.

#### W-reveal - Reveal state (pure)
- **Edit:** `web/src/lib/drill/reveal.ts` (add to the types file from W-api3).
- **Test file:** `web/tests/drill.reveal.test.cjs` (new).
- **Req:**
  1. `type RevealState = { s: 'idle' } | { s: 'loading'; view } | { s: 'shown'; view; text; cut } | { s: 'notStored' } | { s: 'error' }`.
  2. `revealKey(sourceId,id,list,index)`.
  3. `reduce(state, event)`. The events are reveal, full, answer, notStored, fail and clear.
     - `clear` always gives idle and drops the text.
     - An answer for a key other than the current one is ignored.
  4. `copyText(state)` returns the text only in `shown`.
  5. Tests:
     - idle → loading → shown
     - shown + full → loading full
     - clear drops the text (JSON of the state has no text)
     - a stale key is ignored
     - notStored
- **Test:** `cd web && node --test tests/drill.reveal.test.cjs`
- **Doer:** Haiku-low. **Reviewer:** Sonnet-medium, plus Opus at SEC-D3.
- **Dep:** W-api3. **Group:** D3-W2.

#### W-revui - Reveal component
- **Edit:** `web/src/lib/components/drill/DrillReveal.svelte` (new).
- **Test file:** `web/tests/drill.revui.test.cjs` (new).
- **Req:**
  1. Props `{ sourceId; id; list: 'sql' | 'http'; index: number }`.
  2. The Reveal button calls `api.recordText({..., view:'preview'})`. When shown, "Show full text" calls
     view 'full'. Copy uses `navigator.clipboard.writeText`, then shows `copied`. Each has its own
     AbortController.
  3. The text sits in `<pre class="reveal-text">{text}</pre>` with no `{@html}`. Show `cutPreview`/`cutFull`
     when cut, and `revealNote` once text is shown.
  4. Show `notStored` and `revealFailed`.
  5. An `$effect` on the props key dispatches clear, and `onDestroy` dispatches clear and aborts.
  6. No storage API, no console, and no text in any attribute (title, aria-label or data-*).
  7. Source test: all of the above as needles and anti-needles.
- **Test:** `cd web && node --test tests/drill.revui.test.cjs`
- **Doer:** Sonnet-medium. **Reviewer:** Opus at SEC-D3 (full text in the page).
- **Dep:** W-reveal, W-copy. **Group:** D3-W3.

#### W-ovrev - Reveal rows in Overview
- **Edit:** `web/src/lib/components/drill/DrillOverview.svelte`.
- **Test file:** `web/tests/drill.ovui.test.cjs` (extend).
- **Req:**
  1. In each slow SQL and slow HTTP row, render `<DrillReveal sourceId={record.sourceId} id={record.id} list="sql"|"http" index={line.index} />`.
  2. Rendered only when `record.perf.textStored !== false`. When it is false, show `DRILL_TEXT.notStored` and
     no button.
  3. The test asserts both branches.
- **Test:** `cd web && node --test tests/drill.ovui.test.cjs`
- **Doer:** Haiku-low. **Reviewer:** Haiku-low.
- **Dep:** W-revui. **Group:** D3-W4.

#### W-tab3 - Breakdown tab in the pop-up
- **Edit:** `web/src/components/DrillPopup.svelte`.
- **Test file:** `web/tests/drill.popup.test.cjs` (extend).
- **Req:**
  1. Add the breakdown tab and `<DrillBreakdown ...>`. `onOpenAnalysis` uses the popup's own `onOpenAnalysis(slot)`.
  2. `sourceLabels` is built from `sources`.
  3. The test asserts three tabs.
- **Test:** `cd web && node --test tests/drill.popup.test.cjs`
- **Doer:** Haiku-low. **Reviewer:** Haiku-low.
- **Dep:** W-bdui. **Group:** D3-W2.

#### SEC-D3 - Opus security review (one pass)
- **Scope:** the diffs of A2-audit, A2-mask, A2-demo, A2-store, A2-server, A2-keeper, W-api3, W-reveal,
  W-revui and W-ovrev.
- **Checklist (R1 and N3/N4):**
  - text never in `/api/record`
  - 32 KB and 500-rune caps
  - no-store on every status
  - no logging (applog, access log, keeper)
  - error bodies have no text
  - masking misses (userinfo `user:pass@`, encoded `%3F`, `;` params)
  - text dropped on close or entry change
  - no attribute leak
  - the foreign source is refused
- **Doer:** Opus-high, review only. Findings go back as one fixer pass (Sonnet-medium). A re-review only
  for unclear items, as the weekly-usage rule says.
- **Dep:** all listed. **Group:** D3-W5.

#### E-D3 - Playwright DRILL-06/07
- **Edit:** `tools/e2e/specs/timeline-drill.spec.ts`.
- **Req:**
  1. **DRILL-06:** the Breakdown tab shows `#drill-breakdown`.
  2. **DRILL-07:**
     - Reveal on slow SQL 0 shows text containing `key=***`
     - Show full text grows the text
     - Copy shows 'Copied'
     - close and reopen: no `.reveal-text`
     - a route on `**/api/record-text` asserts the request `cache` mode; the response header check is
       `no-store`
- **Test:** `cd tools/e2e && npm run e2e -- -Only DRILL-01,DRILL-06,DRILL-07`
- **Doer:** Sonnet-medium. **Reviewer:** Haiku-low.
- **Dep:** W-tab3, W-ovrev, A2-server, A2-demo. **Group:** D3-W5.

#### A8 - clumsy-user review plus final review
- **Edit:** `.forge/investigation-timeline/drill/review-a8.md` (review notes) and new tests only. Product
  fixes go back as slices.
- **Req:**
  1. Break-the-app pass:
     - rapid open/close
     - Esc during load
     - browser back while open
     - phone width 375
     - old-shape source
     - VPN off
     - chip spam
     - Reveal spam
     - two pop-ups never stack
  2. Each finding gets a test.
  3. Run the full unit suites and `npm run e2e -- -Only DRILL,TSRC`.
- **Doer:** Sonnet-medium. **Reviewer:** Opus-medium (the final review the plan names).
- **Dep:** SEC-D3 fixed, E-D3. **Group:** D3-W6.

#### I-D3 - local install rc.34
Same steps as I-D1, with `-Version 0.1.0-rc.34`. No tag, no release.

---

## 3. Wave and parallel schedule

Gates at every wave end:
- **G-go:** `go build ./... && go vet ./... && go test ./internal/audit/ ./internal/store/ ./internal/server/ ./internal/keeper/`
- **G-web:** `cd web && npm run check && npm test`

A wave starts only when the previous wave's gates are green.

### D1 (rc.32)

| Wave | Parallel (files disjoint) | Go writers (one per package) |
|---|---|---|
| W1 | C0 (owner), A0-P2go, A1-audit, A0-P2web, A0-P3, A0-P4, W-copy, W-perf, W-target, W-hit, W-app | server: A0-P2go; audit: A1-audit |
| W2 | A0-P5go, A1-demo, A1-perf (when C0 is done), W-overview, W-chart, W-shell | server: A0-P5go; audit: A1-demo; store: A1-perf |
| W3 | A1-wire, K1, W-ovui | store: A1-wire; keeper: K1 (test only, after the store build is green) |
| W4 | W-paper, A0-e2e | - |
| W5 | E-D1, then I-D1 | - |

Sequential chains:
- server: A0-P2go → A0-P5go
- audit: A1-audit → A1-demo
- store: A1-perf → A1-wire
- TracingChart.svelte: A0-P3 → W-chart
- DrillPopup.svelte: W-shell → W-paper

If C0 is late, the D1 demo can still ship on demo data. A1-perf and A1-wire then move to the start of D2,
and the owner sees "not recorded" on UAT.

### D2 (rc.33)

| Wave | Parallel | Go writers |
|---|---|---|
| W1 | A3-audit, W-types2 | audit: A3-audit |
| W2 | A3-demo, A3-store, W-chips | audit: A3-demo; store: A3-store |
| W3 | A1b-audit, W-nearby | audit: A1b-audit |
| W4 | A1b-store, K2, W-tab2 | store: A1b-store; keeper: K2 (after the store build is green) |
| W5 | E-D2, then I-D2 | - |

Sequential chains:
- audit: A3-audit → A3-demo → A1b-audit
- store: A3-store → A1b-store (both edit activity.go)

### D3 (rc.34)

| Wave | Parallel | Go writers |
|---|---|---|
| W1 | A2-audit, W-bdui, W-api3 | audit: A2-audit |
| W2 | A2-mask, A2-server, W-reveal, W-tab3 | audit: A2-mask; server: A2-server |
| W3 | A2-demo, A2-store, W-revui | audit: A2-demo; store: A2-store |
| W4 | A2-keeper, W-ovrev | keeper: A2-keeper |
| W5 | SEC-D3 (Opus), then one fixer pass; E-D3 | - |
| W6 | A8, then I-D3 | - |

Sequential chains:
- audit: A2-audit → A2-mask → A2-demo
- reveal.ts: W-api3 → W-reveal
- DrillPopup.svelte: W-tab2 (D2) → W-tab3
- DrillOverview.svelte: W-ovui (D1) → W-ovrev

A2-server compiles in W2 against A2-audit's interface. The demo repository gets its implementation in W3,
so A2-server's tests use their own fake reader.

### Counts by doer

| Doer | Count | Slices |
|---|---|---|
| Haiku-low | 31 | A0-P2go, A0-P5go, A0-P2web, A0-P3, A0-P4, A0-e2e, A1-audit, A1-demo, A1-wire, K1, W-copy, W-perf, W-target, W-hit, W-app, W-overview, W-ovui, W-paper, A3-audit, A3-demo, A1b-audit, K2, W-types2, W-chips, W-tab2, A2-demo, W-bdui, W-api3, W-reveal, W-ovrev, W-tab3 |
| Sonnet-medium | 16 | A1-perf, W-chart, W-shell, E-D1, A3-store, A1b-store, W-nearby, E-D2, A2-audit, A2-mask, A2-store, A2-server, A2-keeper, W-revui, E-D3, A8 |
| Opus | 1 review slice | SEC-D3, plus the A8 final review |
| Owner, planner or main session | 4 | C0, I-D1, I-D2, I-D3 |

Total: 31 Haiku-low and 16 Sonnet-medium build slices, plus 1 Opus review, plus 4
owner/planner/main-session steps.

---

## 4. Risks and unknowns

1. **The UAT field names are not in the repo.** Only `PreActionFilterTotalMilliseconds` and
   `PreActionFilterTimings[{FilterName, ElapsedMilliseconds}]` are verified. The other 17, including the
   R2 five and the `includeCommandText` marker, are unknown.
   - C0 blocks A1-perf, A1b-store and A2-store.
   - The JSON shapes above are fixed, so web and demo work does not wait.
   - Wrong names fail safe: they give "not recorded", not a crash.
2. **Tracing buckets carry no record id.** The diamond finds its call through `/api/slowest` over the
   slot's window, so it can pick a different call than the plotted `maxMs` if the tracing time base
   (initiated vs completed, clock offset) differs from `/api/slowest`'s.
   - W-target's reviewer must check it against the server's slot maths.
   - E-D1 checks that the pop-up duration matches the diamond's ms on demo data.
   - The diamonds sit under the click overlay, so the click needs the W-hit hit-test, and keyboard
     users need Shift+Enter. That key is not discoverable yet; the owner may want a hint at D1.
3. **The modern `activityPipeline` overlap `$or` branch skips the record filters** and never matches
   Normalized Controller/Action. The legacy path filters in Go and is fine. A3-store fixes the modern
   path (Sonnet). A careless fix changes the unfiltered pipeline and moves existing tests, so the
   unfiltered shape must stay byte-identical.

More risks:
4. P2 changes what `known` means. That breaks `TestImportedSpanAdd` by design, and it may move the
   `TestTracingSQLIILanesGolden` golden. Only an added `importedPartial` key may be regenerated.
5. P5 has no visible effect for the owner unless the held SQL lane is drawn with its range line. D1 says
   it is covered by a unit test only.
6. P4 disables a chip that may be on. It can then be switched off only by "Clear all". This is accepted
   by P4=A; the owner sees it at D1.
7. `TestRecordProjectionListsOnlySchemaFields` hard-codes the projection key set and the 21-field count.
   A1-wire extends the set but keeps the count.
8. Reveal needs a new keeper op (A2-keeper). When the product runs through the keeper and the op is
   missing, Reveal returns 501. E-D3 must run in the keeper path once; the owner checks at D3.
9. The access-log and middleware behaviour for request bodies was not read. A2-server must confirm
   that no body or response is logged; SEC-D3 checks it.
10. The Esc guard: without `if (drillOpen) return;` in `onWindowKey`, Esc in the pop-up also steps the
    app back a screen. W-app must merge before W-shell.
11. Many shared files are already dirty with rc.25-31 work: TracingChart, TracingPaper, TraceSources,
    appState, tracing.go, tracing_sqllive.go and the timeline spec.
    - Doers edit by anchor text and must not reformat.
    - Reviewers diff against the pre-slice tree, not master.
    - No worktrees.
12. Mongo 3.4: `$type` (aggregation), `$substrCP`, `$arrayElemAt`, `$slice`, `$map`, `$let` and
    `$addFields` exist in 3.4. `$convert`, `$toDouble` and `$isNumber` do not; reviewers grep for them.
13. Copy lint may reject words in DRILL_TEXT (`deck.terms`). W-copy may reword and must report it. The
    sentence meanings and the D2 merge format are fixed by N5.
14. A1b timing proof: the "no meaningful slowdown" check needs UAT and is done by the owner at D2. A
    regression means the light totals are dropped from the legacy path first.
15. A2 text can hold real values (R1). Masking covers URL query values only. SQL literals are never
    masked, by design; the protection is the explicit clicks, the cap, no-store, no logging and the
    drop on close.
