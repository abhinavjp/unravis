# P-17 Store applies the user and API filters to the activity read

model: sonnet, effort: high. Reason: builds a MongoDB match from user input next to an `$or` that must not be bypassed.
risk: high. Review batch: RB-3, then security batch RB-6 (Opus). Wave: W1. Requirement: R-11 (BL-007).

## Outcome
`(*Store).Activity` returns only the calls of one user and/or one API when `ActivityInput.UserID` / `Operation` are set, on live MongoDB data.

## Write scope
- `internal/store/activity.go`
- `internal/store/activity_test.go` (existing or new)

## Must not change
- `requestPipelineOrdered` and `requests.go`. The first `$match` and its `$or[existing, overlap]` wrapper built by `activityPipeline` (line 17). MongoDB 3.4 compatibility (no `$expr`, no `$regexMatch`). No write, no index creation. Never log the filter values.

## Changes
1. Read how the store already matches `audit.Query.UserIDs` and `audit.Query.Operation` (`grep -n "UserIDs\|Operation" internal/store/*.go`). Reuse the same field paths and the same helper for splitting `Controller.Action`. Write the file:line in the report.
2. In `activityPipeline`, when `input.UserID != nil` or `input.Operation != ""`, append ONE extra `$match` stage placed directly AFTER the stage that holds the `$or` overlap wrapper and before sort/limit. It holds equality tests only: user field `== *UserID`, controller `==`, action `==`. Values go in as BSON values (int, string), never spliced into a string, never as a document supplied by the caller.
3. Do not put the new conditions inside the first `$match`: the overlap branch of the `$or` would let rows through that skip them.
4. Defence in depth: if `Operation` does not split into exactly two non-empty parts, return the package's invalid-input error before any query runs (P-13 validates too).
5. Tests: build the pipeline for (a) no filter: byte-identical to today's (compare with the result before your change, captured in the test as the existing expected value or by stage count); (b) user only; (c) operation only; (d) both. Assert the new stage index is exactly one after the overlap stage, that it contains only the three expected keys, and that the values have Go types `int`/`int32`/`int64` and `string`. A test named `TestActivityFilterStaysOutsideOverlapOr` asserts the first `$match` is unchanged.

## Proof
- [ ] `export PATH="$PWD/.dev/toolchains/go/bin:$PATH" && go build ./... && go vet ./internal/store/` → no output.
- [ ] `go test ./internal/store/ -run 'Activity' -count=1` → `ok`.
- [ ] `go test ./internal/store/ -count=1` → `ok`.
- [ ] `git diff -- internal/store/activity.go | Select-String -Pattern '\$expr|\$where|\$regexMatch|\$function'` → no match.
- [ ] Unit-verified only here. Live-verified in P-46 (chip "Only this user" returns only that user's calls from a live source).

## Depends on
P-13. Only Go writer in `internal/store` in this wave; P-34, P-36, P-38 follow.
