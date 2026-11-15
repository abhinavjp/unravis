# BBX-2-02 IIS worker requests/threads + WAS ping latency
Backlog: BL-106 | Group: 2 | Model: performance | Reason: per-worker instance mapping to selected pools; targeting + rates.
Depends on: BBX-1-05, BBX-1-06, BBX-1-11 | Blocks: none

## Goal
For SELECTED app pools only, sample IIS worker-process active requests and threads (W3SVC_W3WP) and WAS ping latency (WAS_W3WP) every 15 s, keyed to the resolved worker PID.

## Context
- New file `internal/blackbox/iis.go` (no tag) + test `internal/blackbox/iis_test.go` (fakes).
- Same group pattern as BBX-2-01 (init RegisterGroup("iis", true, ...), factory builds PDH source + engine, Collect maps fields). Uses `d.Targets()` (resolved targets from BBX-1-11) to filter to selected pools.
- Researched paths (UNVERIFIED): `\W3SVC_W3WP(<PID>_<AppPool>)\Active Requests`, `\W3SVC_W3WP(<PID>_<AppPool>)\Active Threads`; `\WAS_W3WP(<PID>_<AppPool>)\Ping Response Latency` (confirm exact WAS counter name live). Instances are `<PID>_<AppPoolName>`.
- `IISGroup{Workers []IISWorker}`, `IISWorker{Pool, PID, ActiveRequests, ActiveThreads, PingLatencyMs}` (groups_types.go).
- Rules: **owner-selected targets only** (filter to selected pools); **key on (PID, creation time)**; **missing never 0** (a stopped/recycled pool → "not running", omit); **two baselines before a rate**; **no text**.

## Do
1. `iis.go`: factory builds a PDH source with wildcard paths `\W3SVC_W3WP(*)\Active Requests`, `\W3SVC_W3WP(*)\Active Threads`, `\WAS_W3WP(*)\...latency`. Engine specs: ActiveRequests/ActiveThreads gauge (or PDHRate per live check), PingLatencyMs gauge.
2. `Collect`: get `d.Targets()`; keep only resolved appPool targets. Parse each expanded instance with `parseW3WPInstance` (BBX-1-11); match to a selected target by pool name AND PID (confirm Start via the target). Build one IISWorker per matched selected pool. A selected pool with no live instance → omit (the resolver already lists it "not running").
3. Re-expand wildcards only when `d.ProcessSetChanged()` true.
4. Optional=true.

## Do not
- Do not sample unselected pools (owner-selected only).
- Do not match on PID alone — confirm pool name and creation time.
- Do not emit 0 for a stopped pool — omit the worker.
- Do not capture request text/URLs.
- Do not re-expand every tick; no goroutine/ticker.

## Tests to write first (fakes)
- `TestIISGroupSelectedOnly` — fake instances for 3 pools, targets select 1 → only that worker emitted.
- `TestIISGroupPIDMatch` — instance PID must match the resolved target PID (a stale instance for the same pool name but different PID is ignored).
- `TestIISGroupMissingOmitted` — invalid counter → field nil.
- `TestIISGroupStoppedPool` — selected pool with no instance → no worker, no zero row.
- `TestIISGroupRefreshOnChange` — refresh only on ProcessSetChanged.

## Verification checklist
- [ ] go build ./... passes
- [ ] go vet ./... passes
- [ ] go test ./internal/blackbox/... -count=1 passes, tests above included
- [ ] Both build-tag variants compile (GOOS=windows and GOOS=linux go vet ./internal/blackbox/...)
- [ ] Footprint: reused handle; re-expand on change only; no goroutine/ticker
- [ ] Missing reading is omitted, never 0 (TestIISGroupMissingOmitted, TestIISGroupStoppedPool)
- [ ] No text captured (pool names + numbers)
- [ ] Owner-selected only; (PID, creation) identity (tests above)
- [ ] Budget gate BBX-1-09 still passes

## Live-check needed?
YES — `typeperf -q W3SVC_W3WP` and `typeperf -q WAS_W3WP` on a real IIS server to confirm object/counter names and the `<PID>_<AppPool>` instance format; compare a sampled Active Requests value to `appcmd list wp` / perfmon. Correct paths if needed. Fake-tested only until then.

## Report back
Files changed, each checklist item ticked or failed with output, exact paths used, anything unconfirmed. Full report to `.forge/blackbox-expansion/reports/BBX-2-02.md`.