# BBX-5-11 SQL requests sampler (running-at-once by wait type, blocking)
Backlog: BL-101 | Group: 5 | Model: performance | Reason: SQL sampler with blocking aggregation; high security impact.
Depends on: BBX-5-05, BBX-5-09, BBX-5-10 | Blocks: BBX-5-16
Reviewer: Ultimate (SQL).

## Goal
Every 15 s, sample `dm_exec_requests`/sessions into running-at-once by wait type plus blocking counts — output only wait type, task count, max wait ms, blocked count, head-blocker count, max block ms. No session id, login, host, program name, handles, statement offsets or resource_description.

## Context
- New file `internal/bbsql/requests.go` + `requests_test.go`. Implements `Sampler` (BBX-5-10): Kind()="requests", Interval()=15s, StatementID()="bb:requests", Decode(rows)→[]Requests (BBX-5-09). Register via init().
- The statement `bb:requests` (BBX-5-05) already aggregates in SQL (GROUP BY wait_type, excludes own session). This packet only decodes + validates; no delta needed (instantaneous).
- Security review BL-101 allowed output: wait_type, task count, max wait ms, blocked count, head-blocker count, max block ms. NEVER session id, login, host, program name, handles, statement offsets, resource_description.
- Rules: **fixed allowlisted statement**; **no text**; **row cap + truncated**; **wait-type regex**; **missing never 0**.

## Do
1. `type requestsSampler struct{}` implementing Sampler; init() RegisterSampler.
2. `Decode(rows)`: decodeRows into []Requests (cap 256, truncated flag), validate WaitType regex. Return the slice; empty → nil (omitted, not zero).
3. No SQL building here (use StatementID). No delta state.

## Do not
- Do NOT output session_id/login/host/program_name/resource_description/statement offsets.
- Do NOT build or alter SQL; do NOT add a query-text column.
- Do NOT emit 0 for missing; no goroutine/ticker (the runner owns the loop).

## Tests to write first (fake rows)
- `TestRequestsDecode` — fake aggregated rows → []Requests with correct fields.
- `TestRequestsNoForbiddenFields` — a row carrying session_id/program_name → rejected (unknown field).
- `TestRequestsWaitTypeRegex` — invalid wait type rejected.
- `TestRequestsCapTruncated` — >256 → truncated.
- `TestRequestsEmptyNil` — no rows → nil.
- `TestRequestsRegistered` — Kind/Interval/StatementID correct; StatementID is allowlisted.

## Verification checklist
- [ ] go build ./... passes
- [ ] go vet ./... passes
- [ ] go test ./internal/bbsql/... -count=1 passes, tests above included
- [ ] Both build-tag variants compile (GOOS=windows and GOOS=linux go vet ./internal/bbsql/...)
- [ ] Footprint: bounded decode; no goroutine/ticker; C13 gate (BBX-5-10) still passes
- [ ] No text captured — only allowed fields (TestRequestsNoForbiddenFields)
- [ ] Allowlisted statement; wait-type regex; row cap (tests above)
- [ ] Missing never 0 (TestRequestsEmptyNil)

## Live-check needed?
YES — owner runs bb:requests on a real SQL Server under load and confirms wait-type counts match `dm_exec_requests` totals, with no forbidden columns. Fake tests verify decode only.

## Review-pinned requirement (Ultimate SQL gate, wave 7 — MUST apply)
`bb:requests` emits `''` wait type for running (non-waiting) requests. Map the empty wait type to the fixed sentinel `RUNNING` (consistent with BBX-5-09) so it passes the C9 regex and is not rejected; do NOT loosen the regex. Add a test that a `''` wait-type row decodes to `RUNNING`.

## Report back
Files changed, each checklist item ticked or failed with output, anything unconfirmed. Full report to `.forge/blackbox-expansion/reports/BBX-5-11.md`.