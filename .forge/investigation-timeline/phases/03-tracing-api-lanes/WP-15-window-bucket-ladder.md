# WP-15 Window validation, 30-day cap and R6-02 bucket ladder
risk: high    size: M    wave: 2    model: sonnet

## Outcome
The tracing route rejects a window longer than 30 days with a plain 400 and fills `window.axisStart`, `axisEnd`, `bucketSeconds` and `buckets` from the one UTC floor-aligned, never-re-anchored axis of ruling R6-02, so a window never has more than 120 aligned buckets. Covers TR-06 and TR-07 (30-day cap, at most 120 buckets), AC-03, AC-10.

## Write scope
- `internal/server/tracing_axis.go` (new)
- `internal/server/tracing_axis_test.go` (new)
- `internal/audit/models.go` (one constant)
- `internal/server/tracing.go` (the window check in `problem()` and the window fill in `caseTracing`; created by WP-14)

## Must not change
- `audit.MaxInterval = 7 * 24 * time.Hour` and `audit.MaxTimeBuckets = 120` (the audit lane and `/investigation` keep their own limits). Add `MaxTraceWindow` next to them; do not edit them.
- The WP-14 sentences, their order, and the response field names (`window.start`, `window.end`, `axisStart`, `axisEnd`, `bucketSeconds`, `buckets`).
- No wall-clock or local time zone: every computation is on `time.Time.UnixMilli()` integers and UTC. No floating point in the ladder or the bucket count.
- No timing assertion in any test.

## Changes
1. `internal/audit/models.go`, in the const block that holds `MaxInterval` (line 19): `MaxTraceWindow = 30 * 24 * time.Hour`.
2. `internal/server/tracing.go`: in `tracingRequest.problem()`, after the WP-14 checks, add: incident span (end minus start) above `audit.MaxTraceWindow` (also for `Reference` when present) -> `"A time window can be at most 30 days long."` (400). Exactly 30 days is accepted, 30 days and 1 s is refused, 29 days 23 h 59 m is accepted. Note: the spec does not give the sentence; it follows the `/investigation` wording ("A time window can be at most 7 days long.").
3. `internal/server/tracing_axis.go` (new), all integer milliseconds:
   - `const hourMs = 3600000`; `var shortRungs = []int64{60000, 300000, 600000, 900000}` (rungs for spans up to 24 h); `var longRungs = []int64{3600000, 7200000, 14400000, 21600000, 28800000, 43200000, 86400000}`; `var allRungs` = the 11 rungs in order: 60 s, 300 s, 600 s, 900 s, 1 h, 2 h, 4 h, 6 h, 8 h, 12 h, 24 h.
   - `func floorMs(ms, step int64) int64` rounds toward negative infinity (correct before 1970); `func ceilMs(ms, step int64) int64` = `floorMs` when `ms` is a multiple of `step`, else `floorMs + step`.
   - `func ladderWidth(spanMs int64) int64`: when `spanMs <= 24*hourMs` the first rung of `shortRungs` whose `ceilDiv(spanMs, rung) <= 120`, else the first of `longRungs` with `ceilDiv(spanMs, rung) <= 120` (86400000 if none). Results: up to 2 h -> 60 s; 2 h 1 s to 10 h -> 300 s; to 20 h -> 600 s; to 24 h -> 900 s; above 24 h up to 5 d -> 1 h; up to 10 d -> 2 h; up to 20 d -> 4 h; up to 30 d -> 6 h (so 10 d 1 s -> 4 h, 20 d 1 s -> 6 h).
   - `func alignedRung(axisStartMs, axisEndMs int64) int64`: the first of `allRungs` whose `ceilDiv(axisEndMs-axisStartMs, rung) <= 120`.
   - `type tracingAxis struct { Start, End, AxisStart, AxisEnd time.Time; WidthMs int64; Buckets int }`.
   - `func newTracingAxis(start, end time.Time) tracingAxis`: `AxisStart = floorMs(start, hourMs)`, `AxisEnd = ceilMs(end, hourMs)` (both UTC), `WidthMs = max(ladderWidth(end-start), alignedRung(AxisStart, AxisEnd))`, `Buckets = ceilDiv(AxisEnd-AxisStart, WidthMs)`.
   - `func (a tracingAxis) Bucket(k int) (from, to time.Time, clipped, ok bool)`: cell k is `[AxisStart + k*W, AxisStart + (k+1)*W)` intersected with `[Start, End)`; `ok` is false when the intersection is empty (cells wholly before `Start` or at/after `End`); `clipped` is true when the intersection is smaller than the cell. The grid is anchored on `AxisStart` and is never shifted to `Start`. Note: the response `window.buckets` is the aligned count (the 120 bound); a lane lists only the cells with `ok`, so it can be shorter than `window.buckets`.
4. `caseTracing` (WP-14): replace the placeholder window with `newTracingAxis(...)`: `axisStart`, `axisEnd` RFC 3339 UTC; `bucketSeconds = WidthMs/1000`; `buckets = Buckets`. Keep the axis on the handler's local variable for WP-18b.
5. Tests in `tracing_axis_test.go`, `TestTracingAxisTable` (start UTC 2026-03-10; W in seconds, aligned count):
   - 1 h at 10:00-11:00 -> 60 s, 60; 1 h at 10:07-11:07 -> 60 s, 120; 10 s window at 10:00:00-10:00:10 -> 60 s, 60.
   - exactly 2 h at 10:00-12:00 -> 60 s, 120; exactly 2 h at 10:07-12:07 -> 300 s, 36 (off 60 s, axis is 3 h); 2 h 1 s at 10:00:00-12:00:01 -> 300 s, 36.
   - 10 h at 10:00-20:00 -> 300 s, 120; 20 h at 00:00-20:00 -> 600 s, 120; 24 h at 00:00-00:00 -> 900 s, 96; 24 h at 10:07 -> 900 s, 100.
   - 5 d aligned -> 3600 s, 120; 5 d 1 s aligned -> 7200 s, 61; 10 d 1 s aligned -> 14400 s, 61; 20 d 1 s aligned -> 21600 s, 81.
   - 29 d 23 h 59 m at 00:00 -> 21600 s, 120; 30 d at 00:00 -> 21600 s, 120 (6 h); 30 d at 00:01 -> 28800 s, 91 (8 h).
   Also `TestTracingAxisInvariants` loops spans 10 s to 30 d (steps: 10 s, 59 s, 1 min, 7 min, 1 h, 2 h, 2 h 1 s, 10 h, 24 h, 25 h, 5 d, 10 d, 20 d, 30 d) at start offsets 00:00, 00:01, 05:59, 23:59 and asserts `Buckets <= 120`, `Buckets*W >= AxisEnd-AxisStart`, `WidthMs` is one of `allRungs`; `TestTracingAxisBucketClipping` (10:07-11:07: cell 0 is 10:07-11:00 clipped, cell 60 starts 11:00 and ends 11:07 clipped, cell 61 `ok` false; the sum of all `ok` cells equals the window length); `TestFloorMsBefore1970` (1969-12-31T23:30Z floors to 23:00 on the hour step).
6. Tests in `tracing_test.go`: `TestTracingWindowLimit` (30 d accepted; 30 d + 1 s -> 400 with the exact sentence; zero and negative spans -> 400 with the WP-14 sentence; `reference` over 30 d -> 400); `TestTracingResponseWindow` (a 1 h window at 10:07 returns `bucketSeconds` 60 and `buckets` 120, `axisStart` 10:00:00Z, `axisEnd` 12:00:00Z).

## Proof
- `go build ./...` -> no output
- `go vet ./internal/server/... ./internal/audit/...` -> no output
- `go test ./internal/server/... -run "TestTracingAxisTable|TestTracingAxisInvariants|TestTracingAxisBucketClipping|TestFloorMsBefore1970|TestTracingWindowLimit|TestTracingResponseWindow" -count=1` -> `ok`
- `go test ./internal/audit/... ./internal/server/... -run "Tracing|Investigation" -count=1` -> `ok`

## Depends on
WP-14
