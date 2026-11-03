# WP-20 IIS request is a point at the logged timestamp
risk: medium    size: S    wave: 7    model: sonnet

## Outcome
An IIS W3C request event is stored with `Start == End ==` the logged `date`+`time`, and keeps `DurationMs` (`time-taken`) as a number. The timeline no longer shows a bar that begins `time-taken` earlier on the guess that `time` is the request end; the guess (`w3cTimeIsEnd`) is not needed to place the event. This closes the "time-end" uncertainty for the tracing lanes (WP-18b adds no time-end warning). Covers TR-04 (IIS point model). Spec labels WP-20 M; it is one file plus its test, so S here.

## Write scope
- `internal/importer/iis/w3c.go`
- `internal/importer/iis/iis_test.go`

## Must not change
- Phase 01 results WP-45 and WP-02 (by id only): whatever they did to `w3cTimeIsEnd`, the `gapW3CTime*` strings, the `known gaps` list or the W3C field handling stays. Open `w3c.go` after both are merged and build on that file; do not re-introduce a removed constant or string.
- The gap constants and `spec.gaps` (lines 23-31 and 80-90 before phase 01): not edited here. `fix3_test.go` line 136 lists them and must keep passing unchanged.
- `DurationMs` value and unit (`time-taken` in whole milliseconds), `Attrs`, `Kind: "request"`, `Host`, `TZRule`, line numbers, the one-second resolution gap, every reject reason (`ReasonBadNumber`, `ReasonBadTime`).
- The HTTPERR and FREB importers (`httperr.go`, `freb.go`): HTTPERR rows are already points; FREB rows keep `End = Start + duration`.
- No change to `internal/evidence`, `casestore` or any server file.

## Changes
1. `internal/importer/iis/w3c.go`, function `w3cRow` (the block that follows `ev = evidence.Event{Kind: "request", ..., Start: ts, End: ts, ...}`, lines 175-189 before phase 01): keep parsing `time-taken` with the same `parseUint(v, 4294967295)` check and the same `ReasonBadNumber` reject, keep `ev.DurationMs = &ms`, and delete the lines that move `ev.Start` back by `taken` or `ev.End` forward by `taken`. After the block `ev.Start` and `ev.End` both equal `ts` (UTC). Remove the now-unused `taken` variable and, if `time` has no other use, the `time.Duration` expression; the file must still compile and `go vet` clean.
2. Comment above `w3cTimeIsEnd` (lines 14-20): add two sentences saying the constant now only selects which known-gap sentence is shown, because events are placed at the logged timestamp whichever reading is right. Do not change the constant or its value. Note: if WP-45 removed or renamed it, put the sentences on whatever it left and skip this step if nothing is left.
3. `internal/importer/iis/iis_test.go`, `TestW3CGood` (lines 134-140 before phase 01): replace the `if w3cTimeIsEnd { ... } else if ...` block with one assertion: `e.Start.Equal(ts) && e.End.Equal(ts)` where `ts` is the logged `2001-02-03 04:05:06 UTC`; the failure message names `point at the logged time`. `DurationMs` stays asserted at 125 (line 131). Keep `TestW3CTimeIsEndGap` (line 215) as it is.
4. New test in `iis_test.go`, `TestW3CRequestIsPointWithDuration`: parse the fixture `internal/importer/iis/testdata/w3c_good.log` with `mustParse(t, NewW3C(), fixture(t, "w3c_good.log"), baseOpts())` (the call `TestW3CGood` makes; 6 events).
   - For every event with a `time-taken` value (`evs[0]`, `evs[1]`, `evs[3]` to `evs[5]`) `Start.Equal(End)` is true and `DurationMs` is non-nil with the logged value (125, 3000, 7, 40, 15).
   - The dash-row fixture: the third data row of `w3c_good.log` (`2001-02-03 04:05:08 ... GET /health ... 500 0 64 -`, `time-taken` is `-`) is `evs[2]`; it keeps `DurationMs == nil` and `Start == End` (the assertion `TestW3CGood` already makes at its "time-taken "-" gives a nil duration" comment; find it by that text, not by line number).
5. New test `TestW3CPointSurvivesLongDuration`, built with the helper `w3cFile(good int, extra ...string)` (`iis_test.go:97`; it writes the fields `date time s-computername cs-method cs-uri-stem sc-status time-taken`), as `mustParse(t, NewW3C(), w3cFile(0, <row>), baseOpts())`:
   - Row `"2001-02-03 04:05:06 WEB-A GET /x 200 4294967295"` (the maximum `time-taken`): `Start == End == 2001-02-03 04:05:06 UTC`.
   - Row `"2001-03-01 00:00:00 WEB-A GET /x 200 5000"` (first of the month): `Start == End == 2001-03-01 00:00:00 UTC` and `DurationMs` 5000; it does not move to the previous month (the old code gave `2001-02-28 23:59:55`).
6. Run the proof commands below for the other packages that use `iis-w3c` events.
   - Any failure that expects `Start = time - time-taken` is updated to the point rule in the same commit and named in the review.
   - A failure with another cause stops the packet.

## Proof
- `go build ./...` -> no output
- `go vet ./internal/importer/iis/...` -> no output
- `go test ./internal/importer/iis/... -run "TestW3CGood|TestW3CTimeIsEndGap|TestW3CRequestIsPointWithDuration|TestW3CPointSurvivesLongDuration" -count=1` -> `ok`
- `go test ./internal/importer/... -count=1` -> `ok` for every package
- `go test ./internal/timeline/... ./internal/calltrace/... ./internal/evidence/... -count=1` -> `ok`
- `go test ./internal/server/... -run "CallTrace|Tracing|Collect" -count=1` -> `ok`

## Depends on
WP-45, WP-02b, WP-03, WP-05
