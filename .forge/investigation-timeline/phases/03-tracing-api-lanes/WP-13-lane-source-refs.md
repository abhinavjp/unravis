# WP-13 Lane source refs, status class and win32 on lane events
risk: medium    size: M    wave: 2    model: sonnet

## Outcome
`calltrace.BuildLanes` can carry the case-source ids (`cs_<id>` / `imp_<id>`) on each lane and on each event, plus the HTTP status class and win32 code of IIS events, and labels IIS events "web request" / "HTTP.sys error" instead of "event". All additions are additive and `omitempty`, so the existing `/call-trace` JSON only gains fields. Covers TR-03 (per-source lanes), TR-04 (lane event fields for the IIS point model), TR-18 (privacy: no new attribute leaves the server).

## Write scope
- `internal/calltrace/types.go`
- `internal/calltrace/lanes.go`
- `internal/calltrace/lanes_test.go` (same file as WP-12; WP-12 is wave 1, this packet is wave 2, so run after it)

## Must not change
- `Lane.Sources []string` (importer names, sorted) keeps its type, JSON name and order; the existing UI and `/call-trace` consumers read it.
- `BuildLanes` signature `(win Window, haveCase bool, sources []SourceInfo, evs []evidence.Event) []Lane`, `MaxLaneEvents = 60`, the state rules, the 1 s `windowSlack`, `laneEvents` selection and sort order.
- `EventLabel` for every kind already in `TestEventLabel` (lines 170-209), the 40-rune cap and `evidence.LintCausal` cleanliness.
- Privacy: only `Attrs["status"]` and `Attrs["win32"]` are read, and only as numbers. Never copy `client-ip`, `referer`, `cookie:*` or any other attribute into a lane event.
- `internal/server/call_trace.go` is not touched; its `caseSources` keeps building `SourceInfo` without the new fields.

## Changes
1. `internal/calltrace/types.go`: add `type SourceRef struct { SourceID string \`json:"sourceId"\`; Label string \`json:"label"\` }`. Add to `LaneEvent`: `SourceID string \`json:"sourceId,omitempty"\``, `StatusClass string \`json:"statusClass,omitempty"\``, `Win32 *int64 \`json:"win32,omitempty"\``. Add to `Lane`: `SourceRefs []SourceRef \`json:"sourceRefs,omitempty"\``.
2. `internal/calltrace/lanes.go`: add to `SourceInfo` two fields `SourceID string` and `Label string` (both empty for `/call-trace` callers; WP-18b sets them from the tracing sourceId wire format `cs_<tag id>` or `imp_<import id>`).
3. `BuildLanes`: after `names` is built, also build `refs map[string][]SourceRef` keyed by the lane key string that `LaneOf(Name)` returns (the `Lane.Key` value; no new key type is introduced): one `SourceRef{SourceID, Label}` per `SourceInfo` whose `SourceID != ""`, de-duplicated by `SourceID`, sorted by `SourceID` ascending, attached to `Lane.SourceRefs` (leave nil when none, so JSON omits it).
4. Event attribution. In `BuildLanes`, build `idByName map[string]string`: importer name -> `SourceID` only when exactly one distinct non-empty `SourceID` exists for that name; a name with two or more distinct ids gets "" (ambiguous: `evidence.Event` carries no import id, so a raw event cannot be told apart). Pass the map to `laneEvents` (change its signature to `laneEvents(in []evidence.Event, idByName map[string]string) ([]LaneEvent, int)`; it has one caller) and set `LaneEvent.SourceID = idByName[e.Source]`. Note: the spec assumed each event knows its source id; in code it does not, hence this rule. WP-18b reports a warning when a name is ambiguous.
5. `laneEvents`: set `StatusClass` and `Win32` only when `e.Kind` is `"request"` or `"httperr"`. `func statusClass(attrs map[string]string) string`: value of `attrs["status"]` must be exactly three ASCII digits whose first digit is 2, 3, 4 or 5; return `"2xx"`, `"3xx"`, `"4xx"` or `"5xx"`; anything else (including 1xx, empty, `"-"`) returns "". `func win32Code(attrs map[string]string) *int64`: parse `attrs["win32"]` with `strconv.ParseInt(v, 10, 64)`; accept 0 through 4294967295 inclusive; otherwise nil. A zero win32 is kept as a pointer to 0.
6. `EventLabel`: add `case "request": s = "web request"` and `case "httperr": s = "HTTP.sys error"` before `default`. Note: this changes the label the existing `/call-trace` IIS lane shows (was "event"); report it as a user-visible wording change in the review.
7. Tests in `lanes_test.go`:
   - extend `TestEventLabel` with the two new rows (`"request"` -> `"web request"`, `"httperr"` -> `"HTTP.sys error"`); they pass the existing length and `LintCausal` checks.
   - `TestBuildLanesSourceRefs`: two `SourceInfo` (`iis-w3c` with `SourceID: "cs_7"`, `Label: "web-a"`; `sql-xe` with `SourceID: "imp_3"`); lane `iis` has `SourceRefs == [{cs_7 web-a}]`, lane `sql` has `[{imp_3 ...}]`; `Lane.Sources` unchanged (`["iis-w3c"]`); with no ids the field is nil and `json.Marshal` of the lane has no `sourceRefs` key.
   - `TestBuildLanesEventSourceID`: unique importer name -> `LaneEvent.SourceID == "cs_7"`; two `SourceInfo` with the same `Name` and ids `cs_7` and `cs_8` -> `LaneEvent.SourceID == ""`.
   - `TestLaneEventStatusAndWin32`: table over `status` values `"200"`, `"302"`, `"404"`, `"503"`, `"100"`, `"20"`, `"2000"`, `"-"`, `""` -> `"2xx"`, `"3xx"`, `"4xx"`, `"5xx"`, "", "", "", "", ""; `win32` values `"0"` -> ptr 0, `"64"` -> ptr 64, `"4294967295"` -> ptr 4294967295, `"4294967296"`, `"-1"`, `"x"`, `""` -> nil; a `sql-xe` event with `status: "500"` in Attrs gets no `StatusClass`.
   - `TestLaneEventsNeverCarryPrivateAttrs`: an event with `Attrs` `client-ip`, `referer`, `cookie:session` set; `json.Marshal` of the lane contains none of those three key names or the test values.

## Proof
- `go build ./...` -> no output
- `go vet ./internal/calltrace/...` -> no output
- `go test ./internal/calltrace/... -run "TestEventLabel|TestBuildLanesSourceRefs|TestBuildLanesEventSourceID|TestLaneEventStatusAndWin32|TestLaneEventsNeverCarryPrivateAttrs" -count=1` -> `ok`
- `go test ./internal/calltrace/... -count=1` -> `ok` (every older test unchanged)
- `go test ./internal/server/... -run CallTrace -count=1` -> `ok`

## Depends on
WP-12
