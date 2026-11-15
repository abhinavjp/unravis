# P-16 Saved database read says what state Query Store was in

model: sonnet, effort: high. Reason: touches SQL-reading code; must add no query and no write.
risk: high. Review batch: RB-2, then security batch RB-6 (Opus). Wave: W0. Requirement: R-06 (BL-012), integration point I-4.

## Outcome
`dbhealth.LaneResult` carries a small `queryStore` object (state, first data time, block length) built from the answers the lane read already gets, so the screen can tell "off", "read-only", "nothing before a time" and "no permission" apart without parsing sentences.

## Write scope
- `internal/dbhealth/lane.go`
- `internal/dbhealth/lane_sql.go`
- `internal/dbhealth/lane_sql_test.go`

## Must not change
- Every SQL text constant (`qLaneQSOptions` :27, `qLaneQSFirst` :28, `qLaneQSStats` :89, `qLaneQSIntervals` :104, `QueryStoreOnScript` :133). NO new query. NO statement that writes. `QueryStoreOnScript` stays a returned text that the app never runs. Existing fields and tags of `LaneResult` (lane.go:77-89), `LaneCheck`, `LaneSeriesPoint`.

## Changes
1. `lane.go`: add
```go
// LaneQueryStore is what the lane read learned about Query Store on the database.
type LaneQueryStore struct {
	// State is one of "on", "off", "read-only", "denied", "unknown".
	State string `json:"state"`
	// FirstData is the start of the oldest saved block, when one was found.
	FirstData *time.Time `json:"firstData,omitempty"`
	// IntervalMinutes is the block length Query Store is set to, when known.
	IntervalMinutes int `json:"intervalMinutes,omitempty"`
}
```
   and in `LaneResult` add `QueryStore *LaneQueryStore \`json:"queryStore,omitempty"\`` as the last field.
2. `lane_sql.go`, in `checkQueryStore` (lines 617-679) or its caller: fill the object from the values already in hand: `qsOptions{state, desired, reason, intervalMin, found}` (line 408) and the result of `qLaneQSFirst`. Mapping: options not found or the read failed for a reason other than permission → `unknown`; `classify(err)` gives `SQLCheckGrant` → `denied`; actual state OFF → `off`; READ_ONLY → `read-only`; READ_WRITE → `on`. Use the state names or numbers the existing summary code already switches on; do not add a second interpretation.
3. Find how a `LaneResult` is saved and loaded as a lane day (`grep -rn "LaneResult" internal/ cmd/ --include=*.go`). Confirm it is stored as JSON that tolerates a missing or extra field (old saved days must load with `QueryStore == nil`; new days must load in an older reader without an error). Write the file:line in the report. If the store decodes strictly, STOP and report.
4. Tests (use the fake rows the existing tests in `lane_sql_test.go` use): one test per state; `firstData` present when the first-data query returned a row; an old JSON document without `queryStore` unmarshals with a nil pointer; marshal of a nil pointer has no `queryStore` key.

## Proof
- [ ] `export PATH="$PWD/.dev/toolchains/go/bin:$PATH" && go build ./... && go vet ./internal/dbhealth/` → no output.
- [ ] `go test ./internal/dbhealth/ -count=1` → `ok`.
- [ ] `git diff -- internal/dbhealth/ | Select-String -Pattern '^\+.*(SELECT|ALTER|SET QUERY_STORE|INSERT|UPDATE|DELETE|EXEC)'` → no match (no new SQL text).
- [ ] Report gives the file:line where saved lane days are decoded and says the decode is tolerant.
- [ ] Demo path: P-22 seeds a day with this object. Live path: P-46 reads a real saved day after a fresh timeline read.

## Depends on
Nothing. Only Go writer in `internal/dbhealth`.
