# WP-12 SQL lane proof test (Appendix P5)
risk: low    size: S    wave: 1    model: sonnet

## Outcome
A test proves the existing `calltrace.BuildLanes` already puts a saved `sql-xe` event inside the window into the `sql` lane in state `events`, so the tracing route (WP-18b) can reuse it with no new lane code. Covers TR-02 (SQL lane from saved events).

## Write scope
- `internal/calltrace/lanes_test.go`

## Must not change
- `internal/calltrace/lanes.go` and `types.go`: no production code in this packet. If the test fails, stop and report; do not fix the library here.
- Existing tests in `lanes_test.go` (names, helpers `ev`, `laneByKey`, `fp`).
- INV: tracing never reads `sql/` and never prints secrets; use only the synthetic importer name `sql-xe`.

## Changes
1. In `internal/calltrace/lanes_test.go`, add `func TestBuildLanesSQLXEEvent(t *testing.T)` placed after `TestBuildLanesSeverityAllowlist` (line ~449). Copy the setup of that test (it uses `t0`, `win` as a `Window{From: t0, To: t0.Add(time.Minute)}`, `ev(...)` and `laneByKey(...)`; read lines 227-243 and 449-460 first to reuse the exact variable names).
2. Build one event with `ev("xe1", "sql-xe", t0.Add(10*time.Second), 250)` (helper returns Kind `slow-op`, Severity `warning`, DurationMs 250).
3. Call `BuildLanes(win, true, []SourceInfo{{Name: "sql-xe", Validated: true}}, []evidence.Event{e})` and take `laneByKey(t, lanes, LaneSQL)`.
4. Assertions, each its own `t.Errorf` so one failure does not hide the next:
   - `lane.State == StateEvents`
   - `len(lane.Events) == 1`
   - `lane.Events[0].ID == "xe1"`
   - `lane.Events[0].Source == "sql-xe"`
   - `lane.Events[0].Label == "slow operation"`
   - `*lane.Events[0].DurationMs == 250`
   - `lane.Hidden == 0`
   - `lane.Sources` equals `[]string{"sql-xe"}` (use `reflect.DeepEqual`, the file already imports `reflect`)
5. Add a second sub-case in the same test, named `"event outside the window"`: the same source with an event at `t0.Add(time.Hour)`.
   - The SQL lane must be `StateNoneSeen` and `len(lane.Events) == 0`.
   - The window slack is 1 s, so one hour is clearly outside.
6. Do not add a new endpoint or helper; this packet is the Appendix P5 "Test" row only.
7. Do not assert any duration or timing of the build call itself; only the lane content above.

## Proof
- `go test ./internal/calltrace/... -run TestBuildLanesSQLXEEvent -count=1` -> `ok`
- `go test ./internal/calltrace/... -count=1` -> `ok` (no other test changed)
- `go vet ./internal/calltrace/...` -> no output

## Depends on
none
