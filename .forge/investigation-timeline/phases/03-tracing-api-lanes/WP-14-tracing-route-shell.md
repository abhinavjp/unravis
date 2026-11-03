# WP-14 Tracing route shell, strict request, response contract
risk: high    size: M    wave: 1    model: sonnet

## Outcome
`POST /api/cases/{id}/tracing` exists, is read-only, decodes a strict request, validates every field and resolves every sourceId against the case before any live audit connection is opened, and answers the full response envelope (window echo, empty `lanes`, `warnings`, `sources`, `requestSeq` echo, `truncated`, `mixedConsistency`, `allLanesStored`). An omitted or empty `sourceIds` means every source of the case. Later packets fill `lanes` and the window axis. The one contract block in Changes step 6 is what phase 04 reads. Covers TR-02, TR-03, TR-10, TR-30, TR-32 (envelope and metadata field names).

## Write scope
- `internal/server/tracing.go` (new)
- `internal/server/routes.go` (one route-table entry, after the `/investigation` entry at lines 286-288)
- `internal/server/tracing_test.go` (new)
- `internal/server/routes_test.go` (only if a test there lists every route pattern; add `/api/cases/{id}/tracing` to it)

## Must not change
- `caseCallTrace`, `caseInvestigation`, `investigationRequest` and their JSON (reuse `investigationWindow`, `internal/server/investigation.go:44`, only as a type; do not edit it).
- `decodeJSON` (strict, 1 MiB, 415 on wrong Content-Type, 400 on unknown field), `openCase`, `writeStoreError`, `writeCaseFailure`, `caseIDOf`, `sharedCase`: call them, never copy or edit them.
- INV read-only: no `case.db` write, no call to `AddSource`, `Import`, `SetEnvs`, `Touch`, no `meta` update. No `serverWorkId`. No credential or `sql/` read.
- Privacy: no field named or valued `client-ip`, `referer` or `cookie:*` anywhere in the response.

## Changes
1. `internal/server/tracing.go`, request type: `type tracingRequest struct { SourceIDs []string` json `sourceIds`; `Incident investigationWindow` json `incident`; `Reference *investigationWindow` json `reference`; `RequestSeq *int64` json `requestSeq` `}`. There is deliberately NO `route` field, so a body containing `"route"` gets the 400 that `decodeJSON` gives for an unknown field. Note: the spec request has no `requestSeq`; it is added here (optional, echoed) for the per-tab sequence rule (TR-30). Note: `reference` is accepted, validated and unused (the response has one `window`).
2. `func (b tracingRequest) problem() string` returns the first failing sentence or "". Order and exact sentences (all status 400 through `writeError`):
   - more than 50 ids in `SourceIDs` (counted after de-duplication): `"Pick at most 50 sources."` An absent, `null` or empty list is NOT a problem (it means every source of the case, step 4).
   - an id longer than 128 chars or empty after trimming: `"A source id is not valid."`
   - incident start or end zero, or end not after start: `"The window needs a start and an end, and the end must be after the start."`
   - `Reference` present and (start/end zero or end not after start): same sentence.
   - negative `RequestSeq`: `"The request number is not valid."`
   (WP-15 adds the 30-day check as `"A time window can be at most 30 days long."` after these.) Duplicate ids are dropped in order of first appearance, silently.
3. sourceId wire format. `func parseTracingSource(id string) (kind string, n int64, ok bool)`: `cs_<digits>` is kind `"cs"` (a `casestore.SourceTag.ID`), `imp_<digits>` is kind `"imp"` (a `casestore.ImportRecord.ID`), digits 1-18 chars and above 0. Anything else is kind `"audit"` (an audit source id, resolved by WP-17a; here it is only kept in `AuditIDs`, ok true). `cs_` or `imp_` followed by non-digit text, or `0`, is `ok` false and the request answers 400 `"A source id is not valid."`. Note: in code `SourceTag.ID` and `ImportRecord.ID` are `int64` (`internal/casestore/store.go:499`, `:180`), tags are optional per import, so `imp_<id>` is the form for an untagged import.
4. Case handle and source resolution, in `tracing.go`:
   - `func unwrapCase(c casestore.Case) casestore.Case`: `if sc, wrapped := c.(*sharedCase); wrapped { return sc.Case }; return c`. Reason: `openCase` (`cases.go:794`, returns `&sharedCase{Case: c, release: release}` at line 830) wraps the handle, and the wrapper shows only the `Case` methods, so `c.(casestore.Tagger)` (and later `casestore.BucketCounter`) fails on it. Same pattern as `case_sources.go:185` and `lanes.go:68`. Every optional-interface assertion and every `casestore.CountBuckets` call in the tracing code goes through `unwrapCase(c)`; `c.Close()` stays on the wrapper.
   - `type tracingRef struct { SourceID, Label string }` (json `sourceId`, `label`).
   - `type tracingSelection struct { Refs []tracingRef; ImportOf map[string]int64; AuditIDs []string; Explicit bool; Warnings []string }`: `Refs` are the resolved `cs_`/`imp_` sources in request order; `ImportOf[sourceId]` is the import id behind it (0 for a tag with no file yet); `AuditIDs` are the audit ids as sent, in order. `Explicit` is `len(ids) > 0` for the (de-duplicated) `ids` passed in: true when the request named at least one source, false when `sourceIds` was omitted, `null` or `[]` (all three are the default call). DEFAULT-AUDIT RULE (one rule, WP-14, WP-17a and WP-16b all follow it): `Explicit` false means "resolve the defaults", NOT "no audit lane" (never infer it from `AuditIDs` being nil or empty: a pure saved-file request has `Explicit` true and empty `AuditIDs`, and means no audit lane). When `sourceIds` was omitted, `null` or `[]`, the case's audit sources ARE included: the audit-lane code (WP-17a) loads them itself with the live repository's `h.repo.Sources(ctx)` call (`[]audit.Source`, each has `ID`; the same call `export.go:325` makes; what happens when that call fails or `h.repo` is nil is defined once in WP-17a, step 3; WP-14 adds no audit lane in that case) and uses every returned id. When `Explicit` is true and `AuditIDs` is empty, no audit lane is built. This packet never calls `h.repo`.
   - `func resolveTracingSources(ctx context.Context, c casestore.Case, ids []string) (tracingSelection, *tracingFailure)` where `type tracingFailure struct { Status int; Reason, Message string }`. `c` is already unwrapped. It calls `c.(casestore.Tagger).Sources(ctx)` only when a `cs_` id is present or `ids` is empty, and `c.Imports(ctx)` only when an `imp_` id is present or `ids` is empty.
   - Label rule (user-typed short text, never a file name): tag `Label`, else tag `Kind`; for an import its `Importer` name; cut to 64 runes.
   - Explicit ids: a `cs_`/`imp_` id that is not found, or any `cs_` id when the case does not implement `Tagger`, gives `Status` 404, `Reason` `"unknown-source"`, `Message` `"That source is not part of this case."`.
   - Empty or omitted `ids` (the default call of the UI): `Refs` = one `cs_<tag.ID>` per tag (oldest first, as `Tagger.Sources` returns them), then one `imp_<id>` per import whose `Status` is `casestore.StatusOK` or "" and that no tag references by `ImportID`, in id order; cut to the first 50. When more than 50 existed, add the warning `"Only the first 50 sources are shown."`. No 400 for this case. No audit id is added to `AuditIDs` here (it stays empty and `Explicit` is false; see the default-audit rule in step 4: WP-17a resolves the audit sources later).
   - Unknown ids: a `cs_`/`imp_` id that is not part of the case is 404 `unknown-source` (above). An explicit audit id that the live repository does not list is ALSO 404 `unknown-source`, same message; WP-17a performs that check against `h.repo.Sources(ctx)` before any lane read (WP-14 cannot, it opens no live connection). It is never reported as a lane `reasonCode:"error"`.
   - No live connection (`h.repo`) is touched in this packet.
5. `func (h *handler) caseTracing(w http.ResponseWriter, r *http.Request, raw string)`. Order: `var req tracingRequest` + `decodeJSON` -> `problem()` -> `parseTracingSource` of each id (400 on `!ok`) -> `context.WithTimeout(r.Context(), requestTimeout)` -> `_, c, ok := h.openCase(w, r, raw)` (it calls `caseIDOf` itself; 404 erasing/not found, 409 closed/freeing, 423 locked, 503 `cases-unavailable` all come from the existing helpers; do not call `caseIDOf` again) -> `defer c.Close()` -> `resolveTracingSources(ctx, unwrapCase(c), ids)` (`ids` is `req.SourceIDs` after de-duplication; WP-17a reads `req` and the returned `sel` by these names) (on a failure `writeCaseFailure(w, f.Status, f.Reason, f.Message)`) -> build the envelope (step 7) -> `writeJSON(w, http.StatusOK, resp, maxResponseBytes)` (`writeJSON(w, status, value, maxBytes)` is `server.go:843`; `maxResponseBytes` is `server.go:33`, the same call `call_trace.go:237` makes).
6. Routes: in `routes.go` add `{pattern: "/api/cases/{id}/tracing", match: caseSubpath("/tracing"), methods: map[string]routeFunc{http.MethodPost: func(h *handler, w http.ResponseWriter, r *http.Request, id string) { h.caseTracing(w, r, id) }}}`.
7. RESPONSE CONTRACT (the one block phase 04 reads; every path below is relative to the JSON root; Go types `tracingResponse`, `tracingWindow`, `tracingLane`, `tracingEvent`, `tracingBucket` in `tracing.go`; arrays are never null, they are `[]`):
   ```
   $.caseId               string
   $.requestSeq           number, present only when the request sent one (client defaults to the seq it sent)
   $.asOf                 string, RFC 3339 UTC, server clock at the answer
   $.window.start         string RFC 3339 UTC, incident start as sent
   $.window.end           string RFC 3339 UTC, incident end as sent
   $.window.axisStart     string RFC 3339 UTC (WP-15), hour-floored
   $.window.axisEnd       string RFC 3339 UTC (WP-15), hour-ceiled
   $.window.bucketSeconds number (WP-15)
   $.window.buckets       number, count of aligned cells between axisStart and axisEnd (NOT an array)
   $.truncated            bool, OR of lanes[].truncated (derived by finishTracingMeta, WP-18b)
   $.mixedConsistency     bool (finishTracingMeta)
   $.allLanesStored       bool (finishTracingMeta)
   $.warnings             string[]
   $.sources              [{sourceId string, label string}], the resolved selection (Refs)
   $.lanes                array, order audit, sql, iis, only lanes that were built
   $.lanes[i].key         "audit" | "sql" | "iis"
   $.lanes[i].label       string
   $.lanes[i].state       one of the calltrace.State* strings
   $.lanes[i].origin      "live" | "case"
   $.lanes[i].asOf        string RFC 3339 UTC
   $.lanes[i].complete    bool (true means only that the read returned within budget)
   $.lanes[i].truncated   bool
   $.lanes[i].reasonCode  "" | "timeout" | "rowcap" | "error"
   $.lanes[i].reason      string, a server sentence, "" when none
   $.lanes[i].scanned     number
   $.lanes[i].consistency "live-non-snapshot" | "stored-as-of" | "snapshot"
   $.lanes[i].disabled    "tz-rule", key omitted when not disabled
   $.lanes[i].sources     [{sourceId, label}]
   $.lanes[i].buckets     array of bucket (below)
   $.lanes[i].events      array of event (below), at most 60
   $.lanes[i].hidden      number
   $.lanes[i].buckets[j].start     string RFC 3339 UTC, clipped to the window
   $.lanes[i].buckets[j].end       string RFC 3339 UTC, clipped to the window
   $.lanes[i].buckets[j].count     number
   $.lanes[i].buckets[j].errors    number, OPTIONAL (key omitted by the SQL and IIS lanes; the client reads a missing value as 0)
   $.lanes[i].buckets[j].clipped   bool (first/last partial bucket)
   $.lanes[i].buckets[j].complete  bool
   $.lanes[i].events[k].id         string
   $.lanes[i].events[k].sourceId   string ("" when the event cannot be tied to one source)
   $.lanes[i].events[k].label      string
   $.lanes[i].events[k].start      string RFC 3339 UTC
   $.lanes[i].events[k].end        string RFC 3339 UTC
   $.lanes[i].events[k].durationMs number or null
   $.lanes[i].events[k].severity   string
   $.lanes[i].events[k].statusClass "2xx" | "3xx" | "4xx" | "5xx" | ""
   $.lanes[i].events[k].win32      number or null
   $.lanes[i].events[k].kind       string, key omitted when empty ("iis-httperr", set by WP-19)
   ```
   Event type: the lane event `e` that the lane-building code (WP-18b, WP-17a) maps into `tracingEvent` is `calltrace.LaneEvent` (`internal/calltrace/types.go:71`; WP-13 adds `SourceID`, `StatusClass`, `Win32` to it). `tracingEvent` copies `ID`->`id`, `SourceID`->`sourceId`, `Label`, `Start`, `End`, `DurationMs`, `Severity`, `StatusClass`, `Win32`.
   `errors` on a bucket is declared `*int64` with `omitempty` so a nil leaves the key out. A request has no `route` and no per-lane switch; the lanes returned are the ones the selection can fill.
8. The shell handler builds that envelope: `caseId`, `requestSeq` echo, `asOf` = now UTC, `window` with `start`, `end` as sent and (until WP-15) `axisStart=start`, `axisEnd=end`, `bucketSeconds=0`, `buckets=0`; `truncated=false`, `mixedConsistency=false`, `allLanesStored=false`, `warnings` = the selection's warnings (never null), `sources` = `Refs` (never null), `lanes: []`.
9. Tests in `internal/server/tracing_test.go`. Case server: `tagEnv(t, store, repo)` from `case_sources_test.go:39` over `tagStores(t)` (memory and sqlite, the way `forEachTagStore` does); `e.newCase(title)` (`cases_test.go:260`) returns the case id; `e.json(http.MethodPost, "/api/cases/"+id+"/tracing", body)` posts. New helpers in this file (every later tracing test reuses them): `type noRepo struct{ audit.Repository }` (nil embedded interface: any call on it panics, so a test that reaches the live repository fails); `func seedTracingTag(t *testing.T, store casestore.Store, caseID string, tag casestore.SourceTag) casestore.SourceTag` (`store.Open`, `c.(casestore.Tagger).AddSource`, `Close`); `func seedTracingImport(t *testing.T, store casestore.Store, caseID, importerName, file string, evs []evidence.Event) casestore.ImportRecord` modelled on `case_file_test.go:135-165`: sets each event's `Source = importerName`, `Line = i+2`, `EvidenceID = evidence.EventID(caseID, importerName, file, line, 0)`, runs one `c.Import(ctx, casestore.ImportMeta{Importer: importerName, FileName: file, HostLabel: "web-a", TZRule: clock.RuleUTCField, AckGating: true, AckText: "ack"}, strings.NewReader("body "+file), run)` and returns `res.Record` (a distinct `file` gives a distinct body hash, so a second import is never a duplicate).
   - `TestTracingStrictRequest`: unknown field `route` -> 400; wrong Content-Type -> 415; 51 sourceIds -> 400 `"Pick at most 50 sources."`; end before start -> 400; reference with a bad window -> 400; negative `requestSeq` -> 400; each asserts the exact sentence above.
   - `TestTracingOmittedSourceIdsMeansAll`: a case with two tags (the first linked to an import) and one untagged import; a body with no `sourceIds` key, one with `"sourceIds":null` and one with `[]` each answer 200 and `$.sources` is `cs_<a>`, `cs_<b>`, `imp_<n>` in that order with the label rule applied; a case with 55 tags and no `sourceIds` answers 200 with 50 sources and the warning `"Only the first 50 sources are shown."`; 51 explicit ids still answer 400.
   - `TestTracingSourceIDs`: `cs_9999` and `imp_9999` -> 404 reason `unknown-source`; `cs_x1` and `imp_0` -> 400 `"A source id is not valid."`; a real tag (made with `seedTracingTag`) and a real import id (`seedTracingImport`) -> 200 on BOTH stores (this proves the `Tagger` lookup works through the `sharedCase` wrapper); duplicates dropped; an audit-looking id (`demo-west`) -> 200 with `$.sources` unchanged (audit ids are resolved by WP-17a, which changes this expectation to 404 and gives `noRepo` an empty `Sources`; keep this note).
   - `TestUnwrapCaseKeepsOptionalInterfaces` (sqlite store): open a case, wrap it as `&sharedCase{Case: c, release: func() {}}`; `wrapper.(casestore.Tagger)` fails, `unwrapCase(wrapper).(casestore.Tagger)` succeeds; `unwrapCase(c)` on an unwrapped handle returns it unchanged.
   - `TestTracingCaseStates`: closed case -> 409, locked -> 423, cases unavailable -> 503, same bodies as `/call-trace`.
   - `TestTracingEnvelope`: the 200 body decodes with `DisallowUnknownFields` into `tracingResponse`; `requestSeq` echoed (and absent when not sent); `lanes`, `warnings`, `sources` are `[]` not null.
   - `TestTracingOpensNoLiveConnection`: every case above run with `noRepo{}` as the repository returns without a panic.
   - `TestTracingReadOnly` (sqlite store): uses a local helper `tracingFingerprint(t *testing.T, store casestore.Store, caseID string) string` defined in `tracing_test.go` (do NOT use `revisionOf`: it is created by WP-65b in phase 01 and `Case.Revision` does not exist in this packet's wave). The helper opens the case, JSON-encodes `Tagger.Sources(ctx)`, `Imports(ctx)` and the length of `Events(ctx, casestore.EventQuery{Window: importer.Window{From: 2000-01-01 UTC, To: 2100-01-01 UTC}, Limit: casestore.MaxEventQuery})`, closes the case and returns the string. The fingerprint is equal before and after a 200 answer.

## Proof
- `go build ./...` -> no output
- `go vet ./internal/server/...` -> no output
- `go test ./internal/server/... -run "TestTracingStrictRequest|TestTracingOmittedSourceIdsMeansAll|TestTracingSourceIDs|TestUnwrapCaseKeepsOptionalInterfaces|TestTracingCaseStates|TestTracingEnvelope|TestTracingOpensNoLiveConnection|TestTracingReadOnly" -count=1` -> `ok`
- `go test ./internal/server/... -run "CallTrace|Investigation|Route" -count=1` -> `ok` (existing routes unchanged)

## Depends on
none
