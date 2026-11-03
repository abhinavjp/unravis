# WP-07a One projection for outward event attributes
risk: high    size: M    wave: 3    model: sonnet

## Outcome
`GET /api/cases/{id}/events` no longer returns `client-ip`, `referer` or any `cookie:*` attribute unless the caller passes `includeSensitive=1`, through one pure function that every outward event view must use (TR-15). Canary tests prove none of the three leaks on the default path.

## Write scope
- `internal/evidence/attrproject.go` (new)
- `internal/evidence/attrproject_test.go` (new)
- `internal/server/cases.go` (edit `toEventDTO` and its one caller in `caseEvents`)
- `internal/server/case_events_scrub_test.go` (new)

## Must not change
- `eventDTO` field names and JSON tags; every other attribute (`status`, `method`, `stem`, `counter`, ...) is returned exactly as stored.
- The export builder (`internal/evidence/caseexport*.go`): WP-07b owns it. `TestBuildCaseNoIPsOrUserIDs` (`caseexport_test.go:320`) stays green and unedited.
- Server-side readers of `e.Attrs` (`investigation.go:425`, `investigation_events.go:134`) read stored events and feed analysis, not the browser; do not route them through the projection.
- INV-3: no raw cookie value, server IP, query string or user agent is stored or returned. The stored `cookie:*` value is already a 32-hex digest; the projection never adds data.

## Changes
1. `internal/evidence/attrproject.go`, package `evidence`: constants `AttrClientIP = "client-ip"`, `AttrReferer = "referer"`, `AttrCookiePrefix = "cookie:"`; `func IsSensitiveAttr(key string) bool` true for the first two exact keys and for any key that starts with `AttrCookiePrefix`; `func ProjectEventAttrs(attrs map[string]string, includeSensitive bool) map[string]string`. It always returns a new non-nil map (never the input map, so callers cannot mutate stored attrs) holding every pair when `includeSensitive` is true, and every pair whose key is not sensitive when false. A nil or empty input gives an empty non-nil map. It never alters a value.
2. `cases.go`: change `toEventDTO(e evidence.Event)` to `toEventDTO(e evidence.Event, includeSensitive bool)` and build `Attrs` with `evidence.ProjectEventAttrs(e.Attrs, includeSensitive)` instead of the copy loop.
3. `cases.go` `caseEvents`: compute `include := query.Get("includeSensitive") == "1"` (exactly the text `1`; `true`, `yes`, `0`, empty, `1 ` and repeated values other than a single `1` all mean false; with a repeated parameter use only the first value via `Get`) and pass it to `toEventDTO`. No other query parameter changes. Do not log the value.
4. Note: the spec lists "report" and "tooltip/detail" among outward views. Today only `toEventDTO` puts event attributes into a browser response (`rg "\.Attrs" internal/server` shows the two analysis readers above and nothing else). Later packets that add a view (call trace, tracing detail) must call `evidence.ProjectEventAttrs`; the test in step 6 lists them.
5. Tests in `attrproject_test.go`:
   - `TestProjectEventAttrsDropsSensitiveByDefault`: input `{client-ip, referer, cookie:sid, cookie:Other_1, status, method}`; default output keeps only `status` and `method`.
   - `TestProjectEventAttrsOptInKeepsAllAndCopies`: `includeSensitive` true returns all six pairs; mutating the returned map does not change the input; the false result is also a copy.
   - `TestProjectEventAttrsNilAndPrefixEdges`: nil input gives a non-nil empty map; `cookie` (no colon), `cookies:x`, `x-cookie:y` and `Client-IP` (different case) are NOT treated as sensitive here (they do not match the exact keys); `cookie:` with an empty name is sensitive.
6. Tests in `internal/server/case_events_scrub_test.go` (`newIntEnv(t, intRepo())`, `e.newCase`): open the case with `e.store.Open(ctx, casestore.CaseID(id))` and call its `Import` with a local `casestore.RunFunc` (same shape as `tRun` in `internal/casestore/sqlite/helpers_test.go`) that emits one `evidence.Event` of source `iis-w3c` whose `Attrs` are `client-ip=CANARY_IP_7f2`, `referer=CANARY_REF_7f2`, `cookie:sid=CANARY_CK_7f2`, `status=200`, `method=GET` (EvidenceID from `evidence.EventID`). If the store rejects those keys, build the DTO through `toEventDTO` directly and say so in the commit note.
   - `TestCaseEventsDropSensitiveAttrsByDefault`: GET `/api/cases/{id}/events` has `status` and `method`, and the raw body contains none of the three `CANARY_` strings.
   - `TestCaseEventsIncludeSensitiveOnlyWithOne`: `?includeSensitive=1` returns all three keys; for each of `0`, `true`, `yes`, `1x`, empty, and `ONE` the raw body has no `CANARY_` string.
   - `TestCaseEventsOptInStillNeedsTheCase`: `?includeSensitive=1` on an unknown or malformed case id answers the same 404 as without it, with no event data.
   - `TestOutwardEventViewsUseTheProjection`: read `internal/server/cases.go` as text and assert it contains `ProjectEventAttrs` and no loop that copies `e.Attrs` into a DTO (no `range e.Attrs` in `toEventDTO`).

## Proof
- `go test ./internal/evidence/ -run 'TestProjectEventAttrs|TestBuildCaseNoIPsOrUserIDs' -count=1` -> `ok`.
- `go test ./internal/server/ -run 'TestCaseEvents|TestOutwardEventViews' -count=1` -> `ok`.
- `go test ./internal/evidence/... ./internal/server/... -count=1` -> `ok`.
- `rg -n "range e\.Attrs" internal/server/cases.go` -> no match.

## Depends on
WP-06a, WP-01b
