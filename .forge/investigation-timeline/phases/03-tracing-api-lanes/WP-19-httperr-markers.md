# WP-19 HTTP.sys error markers on the IIS lane
risk: medium    size: M    wave: 6    model: sonnet

## Outcome
Saved HTTP.sys error rows (importer `iis-httperr`) appear on the `iis` lane of the tracing response as point markers: `start` equals `end`, `kind:"iis-httperr"`, `statusClass` from the logged status (a 503 is `5xx`), and a short label that names the reason ("HTTP.sys error: Connection_Dropped"). A row outside the window is not plotted. Covers TR-04 (IIS point model) and TR-10 (plain labels). Spec WP-19 is S; two code files make it M here.

## Write scope
- `internal/importer/iis/httperr.go` (one new exported function; no other edit)
- `internal/importer/iis/httperr_reason_test.go` (new)
- `internal/server/tracing.go` (marker step inside the IIS lane code of WP-18b)
- `internal/server/tracing_test.go`

## Must not change
- `httperrRow`, `httperrReasons`, `AllowedAttrs`, the parser output and the stored attributes: the importer is unchanged; only a read helper is added.
- `calltrace.BuildLanes`, `laneEvents`, `EventLabel` (WP-13 already returns "HTTP.sys error" and the status class); `MaxLaneEvents = 60`.
- The WP-14 contract block (this packet only fills `$.lanes[i].events[k].kind`, which it lists) and the WP-18b function names and signatures (`tracingSQLIISLanes`, `tracingCountBuckets`, `appendLane`, `finishTracingMeta`).
- Privacy: the response carries the reason text only when it passes `KnownHTTPERRReason`; never the URL stem, query, queue name, site id or any other attribute. No `client-ip`, `referer`, `cookie:*`.
- Never assert a millisecond figure in a test.

## Changes
1. `internal/importer/iis/httperr.go`: add `func KnownHTTPERRReason(s string) bool`: true when `s` is one of the canonical values stored in `httperrReasons` (compare exactly, case-sensitive, against the map values) or the literal `"other"`; false for anything else, including "" and a lowercase spelling. Doc comment: it exists so the server shows only a reason the importer itself produced.
2. `internal/server/tracing.go`, in `tracingSQLIISLanes` (WP-18b), after the `iis` lane events are mapped. Build `reasonByID map[string]string` from the raw `evidence.Event` values just read (`evs` of WP-18b). Key = `evidence.Event.EvidenceID` (`internal/evidence/event.go:50`), the same string the lane event carries as `LaneEvent.ID` and WP-14 puts in `events[k].id`; never the line number or a slice index. Value: for events whose `Source == "iis-httperr"`, `Attrs["reason"]` when `iis.KnownHTTPERRReason` accepts it (events with an unknown reason get no entry). For every mapped event of the `iis` lane whose `Source` is `"iis-httperr"`, look its reason up with `reasonByID[laneEvent.ID]` and: set `kind:"iis-httperr"`; set `end = start` (a point; the importer already stores `Start == End`, this keeps the rule even for older rows); apply the label rule below. Events of other importers keep `kind` empty (omitted from JSON).
3. Label rule, `func httperrLabel(reason string) string` in `tracing.go` (cap 40 runes, the `EventLabel` cap). With `prefix = "HTTP.sys error"`: if `reason == ""` return `prefix`; if `[]rune(prefix + ": " + reason)` is at most 40 runes return `prefix + ": " + reason`; else if `[]rune(reason)` is at most 40 return `reason` alone; else return `prefix`. Examples: `Connection_Dropped` -> `HTTP.sys error: Connection_Dropped` (34 runes); `Connection_Abandoned_By_ReqQueue` -> `Connection_Abandoned_By_ReqQueue` (32 runes; with the prefix it would be 48). Note: the spec asks for the reason in the label; the 40-rune cap forces the reason-only fallback.
4. No change to bucket counts: HTTP.sys rows are counted by the same `GROUP BY` as the other IIS rows (importer name list of the lane, WP-18b). No new warning text.
5. Tests in `internal/importer/iis/httperr_reason_test.go`: `TestKnownHTTPERRReason` (every canonical name from `httperrReasons` and `"other"` -> true; `""`, `"connection_dropped"`, `"DROP TABLE"`, `"Connection_Dropped "` -> false).
6. Tests in `tracing_test.go` (seeding with `seedTracingImport` of WP-14 and `tracingEvents` of WP-18b, importer name `iis-httperr`, kind `httperr`, attrs `status`/`reason`; `seedTracingImport` already sets `TZRule: clock.RuleUTCField` and `HostLabel`; each request selects the import with its `imp_<id>`):
   - `TestTracingHTTPERRMarker`: a 503 row with reason `QueueFull` at 10:30 in a 10:00-11:00 window gives one `iis` event with `kind` `iis-httperr`, `statusClass` `5xx`, `start == end`, label `HTTP.sys error: QueueFull` (this proves the `reasonByID` lookup by `EvidenceID` reaches the right event: seed a second `iis-httperr` row with reason `Timer_AppPool` at 10:40 and assert its own label differs).
   - `TestTracingHTTPERRPointInWindow`: `Connection_Dropped` at 10:30; `start == end`, label contains `Connection_Dropped`.
   - `TestTracingHTTPERROutsideWindow`: a row on the next day and a row at exactly the window end; neither is in `events`, bucket sum 0.
   - `TestHTTPERRLabel`: table of the examples in item 3, `""` -> `HTTP.sys error`, a 45-rune unknown text -> `HTTP.sys error`; every result at most 40 runes, and `evidence.LintCausal` returns no finding for the label of each canonical reason.
   - `TestTracingHTTPERRNoExtraAttrs`: a row with `stem`, `queue`, `siteid` attrs; none of their values appears in the raw body.
   - `TestTracingHTTPERRUnknownReasonHidden`: a stored reason outside the allowlist gives label `HTTP.sys error`.

## Proof
- `go build ./...` -> no output
- `go vet ./internal/importer/iis/... ./internal/server/...` -> no output
- `go test ./internal/importer/iis/... -run "TestKnownHTTPERRReason" -count=1` -> `ok`
- `go test ./internal/server/... -run "TestTracingHTTPERR|TestHTTPERRLabel" -count=1` -> `ok`
- `go test ./internal/importer/iis/... ./internal/server/... -run "HTTPERR|Tracing" -count=1` -> `ok` (older HTTPERR importer tests unchanged)

## Depends on
WP-18b, WP-04
