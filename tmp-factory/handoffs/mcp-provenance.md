# mcp-provenance (BL-348 follow-up, review 5 of #267)

PR: https://github.com/abhinavjp/unravis/pull/270 (base develop), head a8eb24fa455f1a57c9f08fa58c760cf605e9896b. CI: fast-build, go, web, lint all green.

## Verified real
The leak is real. `provenance[].sampleSize` is the sum of group counts per source and window. Filtered comparison with groups of 200 and 3: sampleSize 203, minus the listed 200 = the hidden 3 (reference window too). `docsReturned` = number of groups, `docsExamined` = scan size.

## Fix
`internal/mcp/suppress.go`: `Bridge.provenance` / `provenanceView`. For a suppressing bridge (aggregate tier) `sampleSize`, `docsReturned`, `docsExamined` are null and `countsHidden:true`; the other fields (window, mode, completeness, partialReason, elapsed) stay, so `partial` is unchanged. `withProvenance` became a Bridge method (breakdown.go, tools.go call sites). Stdio and evidence tier unchanged. Provenance elements are not in the output schema, so the bindings golden and `testdata/shapes` did not change.

Tests (`internal/mcp/suppress_provenance_test.go`): compare with a hidden group of 3 (failed before with sampleSize 203), breakdown provenance, unscoped bridge unchanged. `go vet`, gofmt, `go test -race ./internal/mcp ./internal/mcpsrv` pass (Linux).
Docs: spec 03-mcp.md note under BL-348 slice 2, BACKLOG BL-348 note (not ticked).

## Decisions
- Null (the existing "not measured" shape) plus `countsHidden`, instead of "<5" text or rounding: any exact figure minus the listed groups gives the hidden sum, rounding does not close it.
- `Margin.sampleSize` (hint-mode calibration of the source `_id` offsets, not the selection) left as is.

## Not verified / leftovers
- No live data, no Windows, no real host.
- Review request on the PR was refused (author is the same account as the requested reviewer); assignee was set.
- PR #269 also edits internal/mcp; no file overlap seen at push time (merge develop again if it lands first).
