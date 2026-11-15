# BBX-4-03 Classic ASP.NET queues and restarts
Backlog: BL-114 | Group: 4 | Model: efficient | Reason: a fixed counter list through the existing engine; mechanical data wiring, no new logic.
Depends on: BBX-1-05, BBX-1-06 | Blocks: none

## Goal
Sample classic ASP.NET request queue counters and application restarts every 15 s (machine-wide), for .NET Framework hosts.

## Context
- New file `internal/blackbox/aspnet.go` (no tag) + test `internal/blackbox/aspnet_test.go` (fake source). Copy the group pattern from BBX-2-01 exactly (init RegisterGroup("aspnet", true, newASPNETGroup); factory builds NewPDHSource + NewEngine; Collect maps fields).
- Researched paths (UNVERIFIED): `\ASP.NET\Requests Queued`, `\ASP.NET\Requests Rejected`, `\ASP.NET\Request Wait Time`, `\ASP.NET\Application Restarts`, `\ASP.NET Applications(__Total__)\Requests Current`.
- `ASPNETGroup{RequestsQueued, RequestsCurrent, RequestsRejected, RequestWaitTimeMs, ApplicationRestarts}` (groups_types.go) — all *float64.
- Rules: **machine-wide**; **two baselines before a rate** (Requests Rejected/sec-like counters); **valid-status check**; **missing never 0**; **no text**.

## Do (literal)
1. Define the 5 paths as a constant slice. Specs: RequestsQueued/RequestsCurrent/RequestWaitTimeMs/ApplicationRestarts = KindGauge; RequestsRejected = KindPDHRate. Field names = the ASPNETGroup json fields.
2. Factory: `NewPDHSource(paths)`; on error return it (group skipped off-Windows). `NewEngine(specs, d.Cores)`.
3. Collect: `src.Collect()` → `eng.Process(now, readings)` → build ASPNETGroup from emitted fields (leave absent fields nil). Return nil if no fields emitted.
4. No wildcard re-expansion needed (fixed paths).

## Do not
- Do not emit 0 for missing; do not add logic beyond field mapping; no goroutine/ticker; no text.
- Do not change the engine or registry.

## Tests to write first (fake CounterSource)
- `TestASPNETMapsFields` — fake readings → correct fields.
- `TestASPNETMissingOmitted` — invalid counter → nil field.
- `TestASPNETEmptyNil` — no valid readings → nil group.
- `TestASPNETRateGated` — RequestsRejected absent until two baselines.

## Verification checklist
- [ ] go build ./... passes
- [ ] go vet ./... passes
- [ ] go test ./internal/blackbox/... -count=1 passes, tests above included
- [ ] Both build-tag variants compile (GOOS=windows and GOOS=linux go vet ./internal/blackbox/...)
- [ ] Footprint: reused handle; no goroutine/ticker
- [ ] Missing reading is omitted, never 0 (TestASPNETMissingOmitted)
- [ ] No text captured
- [ ] Two baselines before a rate; valid-status check
- [ ] Budget gate BBX-1-09 still passes

## Live-check needed?
YES — `typeperf -q ASP.NET` on a real Framework host; confirm names (esp. Requests Current object). Fake-tested only until then.

## Report back
Files changed, each checklist item ticked or failed with output, exact paths used. Full report to `.forge/blackbox-expansion/reports/BBX-4-03.md`.