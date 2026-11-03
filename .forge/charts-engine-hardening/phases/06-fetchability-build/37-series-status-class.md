# TSK-37 series-status-class

risk: low — one more optional selector term, same pattern as TSK-36.

## Outcome

A series selector may ask for a status-class split (`group=statusclass`, FETCHABILITY-AUDIT B1): each non-empty bucket carries `byClass` counts keyed `1xx`…`5xx`, `other`, read from the event attribute `status`. This lets a chart show 5xx and 503 bursts per bucket without one series per status.

## Write scope

- `internal/casestore/series.go`
- `internal/casestore/series_test.go`

## Must not change

- The `q` term from TSK-36; the JSON of points without `group`.
- Do not commit. Do not push. Never touch `sql/`.

## Changes

1. Doc comment of `SeriesQuery`: add `group=statusclass      optional; adds per-bucket counts by the "status" attribute's class`.
2. `selector`: add `byClass bool`. `parseSelector`: `case name == "group": if value != "statusclass" { return bad("group is statusclass") }; sel.byClass = true`.
3. `SeriesPoint`: add `ByClass map[string]int64 \`json:"byClass,omitempty"\``.
4. `seriesAgg`: add `classes []map[string]int64` (len `nb`, lazily). In `add`, after the count, if `sel.byClass`: `cls := statusClass(ev.Attrs["status"])`, increment.
   `statusClass(s string) string`: if `len(s) == 3` and `s[0]` is `'1'..'5'` and the other two are digits → `string(s[0]) + "xx"`; else `"other"`.
   Note: `add` skips events without the chosen number; for a pure count use `field=duration` on request events (they all carry one). Say so in the doc comment.
5. Output loop: copy `a.classes[b]` into the point when set.
6. `series_test.go`: `TestSeriesStatusClass`: events with status `200`, `503`, `503`, `""` in one bucket → `byClass == {"2xx":1, "5xx":2, "other":1}`; `group=foo` → `SeriesBadSelector`; a selector without `group` has no `byClass` key in JSON.
7. gofmt both files.

## Proof

```
.dev\toolchains\go\bin\go.exe test ./internal/casestore/... -run "TestSeries"
```

PASS.

## Depends on

36 (same file).
