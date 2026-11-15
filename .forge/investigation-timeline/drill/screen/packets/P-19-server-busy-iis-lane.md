# P-19 Tracing buckets carry busy time (web server lane)

model: sonnet, effort: medium. Reason: one mapping in the shared bucket builder, with a null rule.
risk: medium. Review batch: RB-2. Wave: W3. Requirement: R-04.

## Outcome
Buckets of the web server (IIS) lane carry `busyMs` from the imported IIS request log, so the lane shows running at once.

## Write scope
- `internal/server/tracing.go` (function `bucketLane` only, about line 915)
- `internal/server/tracing_bucket_test.go` (the existing test file that covers `bucketLane`; find it with `grep -ln "bucketLane" internal/server/*_test.go`, else new)

## Must not change
- `tracingBucket` fields (P-14 added `BusyMs`). `caseTracing`. The SQL lane code. `internal/casestore` (P-15 is done).

## Changes
1. In `bucketLane`, where a `casestore.BucketTiming` is copied into a `tracingBucket` (where `MaxMs` is set from `Max`), set `BusyMs` to a pointer to `Sum` when the bucket has timed events. If P-15 added `HasSum`, set it only when `HasSum` is true. A bucket with a count but no timing keeps `BusyMs` nil.
2. Only buckets of request-type timings get it. If `bucketLane` also serves lanes whose events are not requests (check the callers), set `BusyMs` only when the lane key is `iis`; pass the key in if it is not already there. Do not set it for the SQL lane here (P-20 and P-21 own that).
3. The IIS log writes the time a request ENDED (`w3cTimeIsEnd`, `internal/importer/iis/w3c.go:14-27`, marked UNVERIFIED there). So this busy time is counted in the step the request ended in, while the API lane counts in the step it started in. Do not shift anything; add a one-line code comment saying so. The UI says it in "What this does not show".
4. Tests: a timing with `Count 3, Sum 4500` → `busyMs` 4500; a counted bucket without timing → no `busyMs` key in JSON; the SQL lane buckets from this function are unchanged.

## Proof
- [ ] `export PATH="$PWD/.dev/toolchains/go/bin:$PATH" && go build ./... && go vet ./internal/server/` → no output.
- [ ] `go test ./internal/server/ -run 'Bucket|Tracing' -count=1` → `ok`.
- [ ] `go test ./internal/server/ -count=1` (background) → `ok`.
- [ ] Demo path: the demo case "Orders API stall, 19 October" has 48 imported IIS requests; a test or a manual demo call shows at least one IIS bucket with `busyMs`. Live path: P-46, needs a case with real IIS logs.

## Depends on
P-14, P-15. Server package order: after P-18.
