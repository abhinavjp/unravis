# BBX-1-11 Resolver: app-pool and service name → PID mapping
Backlog: BL-105 | Group: 1 | Model: performance | Reason: instance parsing + service enumeration + recycle/PID-reuse safety; targeting correctness.
Depends on: BBX-1-08 | Blocks: BBX-2-02, group 3

## Goal
Map the owner-selected target names to live processes: app pools via the `W3SVC_W3WP` counter instances (`<PID>_<AppPoolName>`), services via `EnumServicesStatusEx`. Produce `ResolvedTarget`s keyed on (PID, creation time) with runtime from the inventory, handling recycles, PID reuse, w3wp#N shifts, and out-of-process .NET 10 children.

## Context
- New files `internal/blackbox/resolve_map_windows.go` (windows) + `internal/blackbox/resolve_map_other.go` (!windows). Pure parsing helper goes in `internal/blackbox/resolve.go` (shared, from BBX-1-08) or a new `resolve_map.go` (no tag). Test `internal/blackbox/resolve_map_test.go` (pure helper + fake-driven mapping).
- Uses: `Resolver.Inventory()` / `ProcInfo` / `classifyRuntime` / `dotnetChildren` (BBX-1-08), `ResolvedTarget` (groups.go), `Config.Targets` (BBX-1-03).
- App pool instances: PDH object `W3SVC_W3WP` has instances named `<PID>_<AppPoolName>` (e.g. `4321_Payroll API`). Enumerate with `PdhEnumObjectItemsW` (lazy pdh.dll, follow extras_windows.go pattern). Services: `EnumServicesStatusEx(SC_ENUM_PROCESS_INFO, SERVICE_WIN32, SERVICE_STATE_ALL)` — precedent `internal/discover/winenv_windows.go:398` (returns ENUM_SERVICE_STATUS_PROCESS with PID).
- Rules that apply: **key on (PID, creation time)**; **owner-selected targets only**; **both runtimes / in-process and out-of-process .NET 10**; **unreadable means missing** (a selected target not running → "not running", omitted, never 0); **no command lines**.

## Do
1. Pure helper `func parseW3WPInstance(inst string) (pid uint32, pool string, ok bool)` — split on the FIRST `_`; pid = leading digits; pool = remainder (may contain spaces/underscores). Reject if no `_` or pid not numeric.
2. `func (r *Resolver) ResolveTargets(targets []Target) ([]ResolvedTarget, []string)` — returns resolved targets plus a list of selected names that are "not running"/"not found" (for the UI/Missing):
   - Inventory := r.Inventory() (BBX-1-08).
   - App pools: enumerate W3SVC_W3WP instances; parseW3WPInstance; for each selected appPool target, find the instance whose pool name matches (case-insensitive). Build ResolvedTarget{Key:"pool:"+name, PID, Start (from inventory by PID), Runtime, Pool:name}. If the matched w3wp has Runtime==0 and a dotnet child with Runtime==2 exists (dotnetChildren), target the CHILD (out-of-process .NET 10) — Key stays "pool:"+name, PID/Start = child's.
   - Services: EnumServicesStatusEx; for each selected service target, find the running service by name (case-insensitive) → PID; ResolvedTarget{Key:"svc:"+name, PID, Start, Runtime}.
   - A selected name with no live match → append to the "not running" list; do NOT emit a zeroed target.
   - Identity is (PID, Start): if a PID recurs with a new Start (recycle/reuse), it is a new target instance.
3. `resolve_map_other.go`: ResolveTargets returns nil + all names as "not found" (Windows-only), so callers compile cross-platform.
4. Cache the W3SVC_W3WP instance list and re-enumerate only when the process set changed (reuse Resolver's change detection); do not enumerate every tick if unchanged.

## Do not
- Do not match on PID alone (PID reuse) — always confirm creation time from the inventory.
- Do not read command lines or binary paths to find pools (use the counter instance name).
- Do not emit a target with PID 0 or Start 0 as if running — unmatched → "not running" list.
- Do not pause/restart/attach any process.
- Do not target unselected pools/services (owner-selected only).

## Tests to write first (pure + fakes)
- `TestParseW3WPInstance` — "4321_Payroll API" → (4321,"Payroll API",true); "1234_My_Pool" → (1234,"My_Pool",true); "nopid" → false; "_x" → false.
- `TestResolveTargetsPoolMatch` — fake instance list + fake inventory → selected pool resolved with correct PID/Start/Runtime.
- `TestResolveTargetsNotRunning` — selected name absent → in "not running" list, no zeroed target.
- `TestResolveTargetsPIDReuse` — same PID new Start → treated as new instance (Start differs).
- `TestResolveTargetsOutOfProcess` — w3wp runtime 0 with a runtime-2 child → child targeted.
- `TestResolveTargetsServiceMatch` — fake service enum → selected service resolved.
- Inject the instance/service enumerators as function fields so tests need no Windows.

## Verification checklist
- [ ] go build ./... passes
- [ ] go vet ./... passes
- [ ] go test ./internal/blackbox/... -count=1 passes, tests above included
- [ ] Both build-tag variants compile (GOOS=windows and GOOS=linux go vet ./internal/blackbox/...)
- [ ] Footprint: instance/service enumeration only on process-set change; no goroutine/ticker
- [ ] Missing reading is omitted, never 0 (TestResolveTargetsNotRunning)
- [ ] No text captured (pool/service names only; no command lines)
- [ ] Identity (PID, creation time) (TestResolveTargetsPIDReuse)
- [ ] Owner-selected only; both runtimes; out-of-process handled (tests above)

## Live-check needed?
YES — owner confirms on a real server: `typeperf -q W3SVC_W3WP` shows `<PID>_<AppPool>` instances matching the selected pools; a recycled pool re-resolves to the new PID; a stopped pool shows "not running". Cross-check service PIDs with `sc queryex <name>`.

## Report back
Files changed, each checklist item ticked or failed with output, anything unconfirmed. Full report to `.forge/blackbox-expansion/reports/BBX-1-11.md`.