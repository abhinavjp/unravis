# BBX-2-01 HTTP.sys kernel queue sampler
Backlog: BL-104 | Group: 2 | Model: performance | Reason: wildcard instance expansion + per-pool mapping; first counter-group pattern others copy.
Depends on: BBX-1-05, BBX-1-06 | Blocks: none

## Goal
Sample the HTTP.sys kernel request queues every 15 s to prove queueing before w3wp.exe: CurrentQueueSize, MaxQueueItemAge, RejectedRequests, ArrivalRate per app-pool queue. Machine-wide counter (all queues), so it covers selected pools without targeting them.

## Context
- New file `internal/blackbox/http.go` (no build tag — uses the CounterSource interface; runs only where PDH exists). Test `internal/blackbox/http_test.go` (fake source).
- Pattern (copy for all groups): `init()` calls `RegisterGroup("http", true, newHTTPGroup)` (optional=true). Factory builds `NewPDHSource(paths)` + `NewEngine(specs, cores)`; on PDH error returns the error (BuildGroups skips it on non-Windows). `Collect(now, dt)` → `src.Collect()` → `eng.Process(now, readings)` → map fields into `*HTTPGroup` (groups_types.go). Inject a fake CounterSource in tests.
- Researched PDH paths (UNVERIFIED — live check below): `\HTTP Service Request Queues(*)\CurrentQueueSize`, `\HTTP Service Request Queues(*)\MaxQueueItemAge`, `\HTTP Service Request Queues(*)\RejectedRequests`, `\HTTP Service Request Queues(*)\ArrivalRate`. The `(*)` instance is the app-pool name.
- `HTTPGroup{Queues []HTTPQueue}`, `HTTPQueue{Pool, CurrentQueueSize, MaxQueueItemAge, RejectedRequests, ArrivalRate}` (groups_types.go).
- Rules: **machine-wide counters stay machine-wide** (sample all queues); **two baselines before a rate** (ArrivalRate/RejectedRequests are rates — engine gates them); **valid-status check**; **missing never 0**; **re-expand wildcards only when ProcessSetChanged()** (GroupDeps).

## Do
1. `http.go`: define the 4 wildcard paths; factory `newHTTPGroup(d GroupDeps)` builds the PDH source and an engine with specs (Field names = the HTTPQueue json fields; ArrivalRate/RejectedRequests = KindPDHRate, CurrentQueueSize/MaxQueueItemAge = KindGauge).
2. After expansion, parse the pool name from each expanded path's instance (the text between `(` and `)`); build per-(pool,counter) specs. Re-expand via `src.RefreshInstances()` only when `d.ProcessSetChanged != nil && d.ProcessSetChanged()`.
3. `Collect`: collect readings, engine.Process, group emitted fields by pool into `[]HTTPQueue`. A pool with no valid fields → omit that queue (or include with nil fields only if at least one valid). Return `*HTTPGroup`. If nothing valid → return nil, nil (group omitted, name goes to Missing via the collector).
4. Register optional=true (a queue group that breaks budget ships off).

## Do not
- Do not cap or filter to selected pools (machine-wide).
- Do not emit 0 for a missing counter — leave the pointer nil.
- Do not re-expand wildcards every tick.
- Do not capture request text/URLs (counters only).
- Do not add a goroutine/ticker.

## Tests to write first (fake CounterSource)
- `TestHTTPGroupMapsPools` — fake readings for two pools → HTTPGroup.Queues has both, fields mapped.
- `TestHTTPGroupMissingOmitted` — a counter with OK=false → that field nil, not 0.
- `TestHTTPGroupRateGated` — ArrivalRate absent until two baselines (engine rule).
- `TestHTTPGroupEmptyNil` — no valid readings → nil group.
- `TestHTTPGroupRefreshOnChange` — ProcessSetChanged true triggers RefreshInstances (fake records the call); false does not.

## Verification checklist
- [ ] go build ./... passes
- [ ] go vet ./... passes
- [ ] go test ./internal/blackbox/... -count=1 passes, tests above included
- [ ] Both build-tag variants compile (GOOS=windows and GOOS=linux go vet ./internal/blackbox/...)
- [ ] Footprint: one reused query handle; re-expand only on change; no goroutine/ticker
- [ ] Missing reading is omitted, never 0 (TestHTTPGroupMissingOmitted)
- [ ] No text captured (pool names + numbers only)
- [ ] Two baselines before a rate; valid-status check (engine tests + TestHTTPGroupRateGated)
- [ ] Budget gate BBX-1-09 still passes with this group registered

## Live-check needed?
YES — on a real server run `typeperf -q "HTTP Service Request Queues"` to confirm the exact object/counter names and that instances are app-pool names; then `typeperf "\HTTP Service Request Queues(*)\CurrentQueueSize" -si 15 -sc 2` and compare to the sampled value. Correct the path constants if the names differ. Until then this is fake-tested only.

## Report back
Files changed, each checklist item ticked or failed with output, the exact paths used, anything unconfirmed. Full report to `.forge/blackbox-expansion/reports/BBX-2-01.md`.