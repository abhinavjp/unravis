# TSK-54 m11-spill-lane

risk: medium — a new answer from the live Mongo lane; a No from a profiler that does not cover the whole incident would be a false green.

## Outcome

The Mongo lane's profiler check also answers **M11** (MongoDB spill to disk) from the same profiler documents it already reads for M6:

- **Yes** when at least 3 incident operations on the audit collection have `usedDisk` true **and** operation time rose: P95 of `millis` in the incident is at least 30 in 100 and at least 100 ms above the reference P95, with at least 20 operations on each side.
- **No** when (fewer than 3 spills and under 5 in 100 incident operations spilled, with the profiler covering the whole incident, the read not cut short and at least one incident operation) **or** (both sides hold at least 20 operations, operation time did not rise, and the read was not cut short).
- Otherwise no answer.

## Write scope

- `internal/dbhealth/lane_mongo.go`
- `internal/dbhealth/lane_mongo_test.go` (append)

## Must not change

- The M6 rule, its summary text and its onset logic; `profileFind` (TSK-53 already set the projection).
- `internal/dbhealth/mongo.go`, `lane_sql.go`.
- Do not commit. Do not push. Never touch `sql/`.

## Changes

1. Near the top of `lane_mongo.go`, a const block:
   ```go
   // M11 marks (spill to disk), local to this lane.
   const (
       mongoSpillMin     = 3    // spilled operations in the incident
       mongoSpillShare   = 0.05 // or this share of incident operations
       mongoOpTimeMinN   = 20   // operations per side before P95 is compared
       mongoOpTimeRise   = 0.3  // P95 rise, as a fraction of the reference
       mongoOpTimeRiseMs = 100.0
   )
   ```
2. `steps` (~42): `Feeds` of the profiler step `[]string{"M6"}` → `[]string{"M6", "M11"}`.
3. `profileOutcome` (~88–92): add `m11 string // M11`.
4. `evalLaneProfiler` doc loop (~130–149): keep two `[]float64` slices of `jnum(d["millis"])` (incident, reference) for audit-namespace docs, and `spills` = incident docs whose `d["usedDisk"] == true` (a Go `bool` after the JSON decode; check how `lane_mongo_test.go` builds docs and how `jnum`/`text` read values, and add a tiny `jbool(v any) bool` helper only if none exists).
5. After the M6 `switch` (~160–167), add:
   ```go
   opUp, opKnown := false, len(incMs) >= mongoOpTimeMinN && len(refMs) >= mongoOpTimeMinN
   if opKnown {
       pi, pr := p95(incMs), p95(refMs)
       opUp = pi >= pr*(1+mongoOpTimeRise) && pi-pr >= mongoOpTimeRiseMs
   }
   switch {
   case spills >= mongoSpillMin && opUp:
       o.m11 = AnswerYes
       parts = append(parts, plural(spills, "operation", "operations")+" spilled to disk in the incident window, and operation time rose.")
   case !truncated && ((spills < mongoSpillMin && float64(spills) < mongoSpillShare*float64(incN) && wholeIncident && incN > 0) || (opKnown && !opUp)):
       o.m11 = AnswerNo
   }
   ```
   `p95`: grep the `dbhealth` package for an existing percentile helper and reuse it; otherwise add `func p95(v []float64) float64` (sort a copy, nearest rank `ceil(0.95*n)-1`). Keep the M11 summary sentence plain (no "because", "due to", "caused").
6. `checkProfiler` (~237–239): after the M6 `answer` add `if o.m11 != "" { l.b.answer("M11", o.m11) }`.
7. Tests in `lane_mongo_test.go` (use the existing `profDoc`, `newProf`, `mreq`, `at`, `fixedNow` helpers; add a `profDocMs(t, ns, plan, ms, usedDisk)` variant beside `profDoc` if needed):
   - `TestEvalLaneProfilerM11Yes`: 25 reference docs at 200 ms; 25 incident docs at 900 ms, 4 of them `usedDisk: true` → `m11 == AnswerYes`.
   - `TestEvalLaneProfilerM11NoSpills`: whole incident covered, 25 incident docs, no spills → `AnswerNo`.
   - `TestEvalLaneProfilerM11FlatTime`: 25 docs each side at 900 ms, 4 spills → `AnswerNo` (time flat).
   - `TestEvalLaneProfilerM11Truncated`: as NoSpills but `truncated=true` → `""`.
   - `TestEvalLaneProfilerM11PartCover`: oldest after incident start, 5 incident docs, no spills → `""`.
   - The existing M6 tests pass unchanged; if one checks `Feeds` equals `["M6"]`, update that literal only.
8. gofmt both files.

## Proof

```
.dev\toolchains\go\bin\go.exe test ./internal/dbhealth/... -run "Profiler|MongoLane"
.dev\toolchains\go\bin\go.exe test ./internal/dbhealth/... ./internal/server/...
```

PASS.

## Depends on

53 (projection, same file in order), 44 (M11 is in the catalogue, so the lane answer has a suspect to land on).
