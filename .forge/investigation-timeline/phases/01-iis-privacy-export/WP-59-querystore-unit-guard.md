# WP-59 Query Store microseconds-to-milliseconds guard
risk: low    size: S    wave: 2    model: sonnet

## Outcome
A regression test fixes the Query Store unit rule (runtime columns are microseconds and are divided by 1000, the wait column is already milliseconds and is not), and a one-line comment in `querystore.go` records the VF-01h status found by WP-45. No behaviour changes.

## Write scope
- `internal/importer/sqlserver/querystore.go` (comment lines only)
- `internal/importer/sqlserver/querystore_test.go`

## Must not change
- Any executable line of `querystore.go`: the division `d := math.Round(v/1000*1000) / 1000`, the table rows `{"max_duration", "max_duration_ms", qsMaxMicros, 1000}` and `{"avg_cpu", "avg_cpu_ms", qsMaxMicros, 1000}`, `qsMaxMicros = 7 * 24 * 3600 * 1e6`, `qsMaxMillis = 7 * 24 * 3600 * 1e3`, and the wait path (`d := math.Round(w*1000) / 1000`, no division).
- Existing tests, including `TestQuerystoreEventsCSV` (which already asserts 1,250,000 microseconds gives 1250 ms) and `TestQuerystoreWaitCategories`.
- The import contract: event kinds `KindQueryInterval` and `KindWait`, attribute names, rejection rules.
- If the WP-45 note for VF-01h is REFUTED, stop: this packet is wrong in that case and it is an escalation, not an edit.

## Changes
1. `querystore.go`: in the header comment table (lines ~38-41, the rows for `avg_duration`, `max_duration`, `avg_cpu_time`) add one comment line directly under the `avg_cpu_time` row: `VF-01h: <STATUS>. <reason>` where `<STATUS>` and `<reason>` are copied word for word from `Vf01h.Status` and `Vf01h.Reason` in `internal/importer/iis/verify_notes.go` (created by WP-45; expected `UNMEASURED` with the reason "no paired capture", in which case the line says the division is kept as coded). Comment only; do not touch any code line.
2. `querystore_test.go`: add `TestQuerystoreMicrosecondsToMilliseconds` using the existing helpers `qsHeader`, `qsRow(q int, dur string)`, `qsBulk(n int)` and `qsParse(t, in)` (note `qsRow` writes `max_duration` 2000000 and `avg_cpu_time` 500000). Table-driven runtime cases, each parsed as `qsHeader + qsRow(1, dur)`; the first event's `DurationMs` (pointer, nil check first) and attrs must be:
   - `dur` "1250000" -> duration 1250, `Attrs["max_duration_ms"]` "2000", `Attrs["avg_cpu_ms"]` "500", `Unit` "ms".
   - `dur` "1500" -> duration 1.5.
   - `dur` "1" -> duration 0.001 (microsecond resolution survives the rounding).
   - `dur` "0" -> duration 0 (a non-nil pointer to zero).
   - `dur` "604800000000" (exactly `qsMaxMicros`) -> accepted, duration 604800000.
3. In the same test, rejection case: `qsHeader + qsBulk(40) + qsRow(99, "604800000001")` (one microsecond over the ceiling, 40 good rows so the reject fraction stays low) parses with `rep.Rejected == 1`, 40 events, and no event has query id 99.
4. In the same test, wait case with header `start_time,query_id,wait_category_desc,avg_query_wait_time\n`: row `2001-06-15 02:00:00,1,lock,5` gives one `KindWait` event with `DurationMs` 5 (not 0.005: the wait column is milliseconds and is not divided).
5. In the same test, unit contrast: with the same wait header and 40 good wait rows (`2001-06-15 02:00:00,<n>,lock,5` for n = 1..40, distinct query ids so none is a duplicate), a wait row for query id 99 with `avg_query_wait_time` 604800001 (one millisecond over `qsMaxMillis`) is rejected (`rep.Rejected == 1`), while `qsRow(1, "604800001")` as `avg_duration` is accepted with duration 604800.001. This pins that the two ceilings differ by a factor of 1000.
6. Note: `qsNum` rejects only values strictly greater than the ceiling (`f > max`), so the exact ceiling in step 2 is accepted.
7. Note: the spec's proof command uses `-run QueryStore`, which matches no test (the tests are named `TestQuerystore...`); this packet uses `-run Querystore`.

## Proof
- `go test ./internal/importer/sqlserver/ -run TestQuerystoreMicrosecondsToMilliseconds -count=1 -v` -> `PASS`.
- `go test ./internal/importer/sqlserver/ -run Querystore -count=1` -> `ok` (all Query Store tests, old and new).
- `go vet ./internal/importer/sqlserver/` -> no output.
- `git diff -U0 -- internal/importer/sqlserver/querystore.go | grep "^[+-]" | grep -v "^+++\|^---" | grep -v "^[+-]//"` -> no output (only comment lines changed).

## Depends on
WP-45
