# Blackbox expansion — orchestration plan

Written 2026-11-13 by the orchestrator (Ultimate tier). Branch: `feat/blackbox-expansion` (from `feat/investigation-timeline`).
Sources read in order: `CLAUDE.md`, `docs/research/blackbox-expansion-handoff.md`, `docs/research/blackbox-qoder-orchestration.md`, `docs/BACKLOG.md` (BL-100..BL-126), `docs/research/blackbox-expansion-prioritised.md`, `docs/research/blackbox-sql-security-review.md`.
Delivery: one PR per group into `feat/investigation-timeline`. Never merge, never auto-merge, never push to `feat/investigation-timeline` or `master`. The owner tests on a real server and approves in writing. `docs/BACKLOG.md` items stay unticked until live-verified; fake-tested items get the note "built, awaiting live check".

## 1. Goal and non-negotiable rules

Goal: extend the Blackbox (Windows SYSTEM service, 15 s self-sampling into hourly NDJSON + manifest, read-only key-guarded status server) with the counter groups, target selection, per-process readers and the SQL sampler described in BL-100..BL-126, without breaking the rules below. Every packet ships with fake-reader tests; nothing is ticked in the backlog until it works on the real data path.

Rules copied verbatim from handoff section 3:

- **Light**: under 0.5% of one core on average, under 50 MB, each counter group within 1 s, below-normal priority, self-throttle (BL-122). A group that breaks the budget ships off or slower.
- **Never pause, restart, attach to or dump a target.** No environment variables, no IIS or service restarts.
- **Owner-selected targets only** for per-process detail (payroll API pools, the email service). Machine-wide counters stay machine-wide.
- **Both runtimes, auto-detected per process**: .NET Framework 4.7.2 (PDH `.NET CLR` counters, BL-109) and .NET 10 (EventPipe `System.Runtime`, BL-110). Detect by loaded modules (`clr.dll` vs `coreclr.dll`/`hostfxr.dll`), not by config (BL-105). Key processes on (PID, creation time).
- **No text**: no query or request text, plan XML, payloads, environment variables, command lines. Event log: IDs and numbers only.
- **SQL is read-only and local only**: fixed allowlisted statements, one grant run by the owner, never granted by the app, no stored password. Separate service `UnravisBlackboxSql` as `NT SERVICE\UnravisBlackboxSql`. Controls C1-C14 are mandatory and each one needs a test.
- **Counter engine rules (BL-121)**: English counter names, two baselines before a rate, valid-status check, multi-core CPU not capped at 100.
- **Unreadable means missing**: show "not running" or "not found", never 0.
- Do not build anything under "Backlog (deferred)" without the owner's go. No new collector or agent beyond the Blackbox without the owner's go.

## 2. Technical dependencies

### Toolchain and modules
- Go **1.27.3** (`go.mod`), module `api-audit-explorer`. Toolchain installed for this run at `../.dev/toolchains/go/bin/go.exe` (the location `build.ps1`/`build-blackbox.ps1` probe for). CGO_ENABLED=0 builds.
- Direct deps already present and reused: `golang.org/x/sys v0.48.0` (`windows`, `windows/svc`, `windows/svc/mgr`, `windows/svc/eventlog`, `windows/registry`), `github.com/microsoft/go-mssqldb v1.11.2` (SQL sampler driver only — never dbhealth's query code).
- No new module is needed. PDH/psapi/wevtapi/iphlpapi are reached through the existing lazy-DLL pattern (`extras_windows.go:26-31`), not cgo.

### Windows APIs (existing precedent → new use)
| API | Existing precedent | New use |
|---|---|---|
| PDH `PdhOpenQueryW/PdhAddEnglishCounterW/PdhCollectQueryData/PdhGetFormattedCounterValue/PdhCloseQuery` | `internal/blackbox/extras_windows.go:26-31` (one query, 2 counters) | BL-121/122 counter engine source; one query handle per group, counters added once, wildcards re-expanded only on process-set change |
| `PdhExpandWildCardPathW` / `PdhEnumObjectItemsW` | not used yet | instance expansion for `(*)` groups and `W3SVC_W3WP` `<PID>_<AppPool>` mapping (BL-105/104/106) |
| `CreateToolhelp32Snapshot(TH32CS_SNAPPROCESS)` + `OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION)` + `GetProcessTimes` + psapi `GetProcessMemoryInfo` + `GetProcessHandleCount` | `internal/sampler/source_windows.go:137-189` | resolver (PID, creation time) keying; BL-110 fallback; BL-116 |
| `CreateToolhelp32Snapshot(TH32CS_SNAPMODULE)` | not used yet | runtime detection by loaded modules `clr.dll` vs `coreclr.dll`/`hostfxr.dll` (BL-105); parent-PID walk for out-of-process .NET 10 |
| `EnumServicesStatusEx(SC_ENUM_PROCESS_INFO, SERVICE_WIN32, SERVICE_STATE_ALL)` | `internal/discover/winenv_windows.go:398` | service-name → PID mapping (BL-105) |
| `SetNamedSecurityInfo` + SDDL | `internal/blackbox/service_windows.go:24-47` (`secureSDDL`) | BL-123 `<folder>\sql` DACL: SYSTEM + Administrators + `NT SERVICE\UnravisBlackboxSql` SID only |
| Per-service SID (S-1-5-80 + SHA-1) | `internal/keeper/owner.go:14` | SQL service SID for the DACL |
| `mgr.Config{ServiceStartName}` | `internal/setup/os_windows.go:361`, `internal/setup/plan.go:46` (`NT SERVICE\` precedent) | BL-123 `UnravisBlackboxSql` own account |
| `SetThreadPriority` background mode | `internal/sampler/source_windows.go:194-200` | BL-122 below-normal priority (process priority class on the service) |
| `GetIfTable2Ex` | `extras_windows.go:129-158` | unchanged (network) |
| wevtapi `EvtQuery/EvtRender` (lazy DLL) | not used yet | BL-111 event log tailer, IDs and numbers only |
| iphlpapi `GetTcpTable2` (lazy DLL) | not used yet | BL-112 TCP state counts (count only, never addresses) |
| EventPipe `\\.\pipe\dotnet-diagnostic-{pid}` | **no code; unproven** | BL-110/126 — BLOCKED on live check |

### Build tags and pairs
Windows-only code lives in `*_windows.go` with an `*_other.go` twin (existing pairs: `diskspace`, `elevate`, `extras`, `service`). New pairs follow the same pattern. Both must compile: `GOOS=windows go vet ./internal/blackbox/...` and `GOOS=linux go vet ./internal/blackbox/...`. Pure-logic files (engine, budget, groups, config, SQL controls) are OS-independent and testable anywhere.

### Existing Blackbox types the new code extends (survey-verified)
- `Sample` (`sample.go:25-43`): nil pointers + `omitempty`, group names appended to `Missing []string`. New groups add pointer fields here (one packet pre-creates all host-group fields so group packets touch disjoint files).
- `ProcRow` (`sample.go:48-58`), `procID{pid,start}` (`collector.go:45-48`), `Collector` interface (`sample.go:70-73`), `Extras` (`collector.go:14-20`), `HostCollector` + `topProcesses` (`collector.go:32-211`, top 20 by CPU, Idle skipped at 163-165).
- `Config` (`config.go:72-84`), `Record` (`53-63`), `Validate` (`168-210`), probe refusal (`206-208`), `SaveConfig` 0o644 (`317`). `blackbox.json` lives at `%ProgramFiles%\Unravis Blackbox\blackbox.json` and is shipped in the pack (`pack.go:122`) — never credentials.
- `Recorder` tick loop (`recorder.go:104-192`): `Collect` inline at 149, `err.Error()` into events at 151-153/177-185 (BL-123 must fix), ticker from `Config.IntervalSeconds`, `logsEvery=5m` (25), `housekeepEvery=60s` (24).
- `StatusDoc` (`server.go:29-52`), plain HTTP (`90-101`), key guard (`198-227`), probe gate hardcoded false (`251`).
- `Manager.Install` (`service.go:228-358`): recordings ACL `SecureDir(cfg.Folder, true)` at 274 (BUILTIN\Users read — BL-123 changes this for SQL data only); `ServiceSpec` Args `service --config … --key-file …` (327-329); recovery restart 5/15/60 s (`service_windows.go:95-99`); no `ServiceStartName` (119-123).
- Persistence across restarts is **`manifest.json` via `store.go` only** — `state.go` is UI state derivation, not a state file. C7 auto-off needs its own persisted state under `<folder>\sql`.
- `RunHost` (`host.go:51-107`), service entry `cmd/unravis-blackbox/service_windows.go:21-100`, CLI commands `install/start/stop/apply/remove/snapshot/run/version` (`main.go:205-268`). No `pack`/`verify` subcommand exists.
- SQL reference code: `internal/dbhealth/mssql.go` `ClassifyConnect` (199), one-connection setup (111-113), 5000 cap (157-159); benign wait filter `internal/dbhealth/sql.go:101-112` (reuse the filter LIST only). **Do not copy dbhealth as a template** (reads query text 139, program_name 122, job messages 174; no lock timeout/deadlock priority anywhere).
- App-side consumption: `internal/server/blackbox.go` (routes 140-162, `hostSamples` 1049-1072 → Step 9 `internal/investigate/hostlane.go`), wizard `web/src/components/BlackboxWizard.svelte` + `BlackboxSettings.svelte`, copy `web/src/lib/copy/blackboxCopy.ts`, lane labels `web/src/lib/drill/drillCopy.ts:159-165` + `dbState.ts`, `SlowCallScreen.svelte:240-244`. Detection primitives for the picker: `internal/discover` (IIS pools from applicationHost.config, services).

### Test helpers and fakes that exist (reuse, do not reinvent)
`fakeCollector`/`pctDisk`/`rig` (`recorder_test.go`), `fakeSource`/`fakeExtras`/`proc` (`collector_test.go`), `fakeSvc`/`fakeEnv`/`svcRig` (`service_test.go`), `srvRig` (`server_test.go`), `hostRig` (`host_test.go`), `fakeDisk`/`filled`/`lockFiles` (`housekeeping_test.go`), `dropAccess`/`failAccess` (`reader_test.go`), `memProgram`/`unzipAll` (`pack_test.go`), `fakeRecovery` (`service_windows_test.go`). Store seams `wrapFile`/`removeFile` (`store.go:52-56`). Budget precedent: `internal/sampler/bench_test.go` `BenchmarkTick` + `TestTickStaysInTheBudget` (15 ms/tick). No benchmarks exist in `internal/blackbox` yet — BL-122 adds the gate.

### Things that need a real server (cannot be verified here)
1. Exact PDH paths: HTTP.sys, W3SVC_W3WP, WAS_W3WP, Hyper-V (`typeperf -q`). Blocks live-ticking of BL-104/106/113; code is built against researched paths and corrected after the check.
2. Whether SYSTEM can open an app-pool process's `dotnet-diagnostic-{pid}` pipe → **blocks BL-110 EventPipe and BL-126**.
3. IIS hosting model (in-process vs out-of-process) per server → resolver handles both; live confirmation needed.
4. EventPipe cost inside the target and snapshot freeze time (no measured figure) → blocks BL-126.
5. Live SQL tests: C3 session options, C5 one session, C8 rights ceiling need a real SQL Server with the owner-run grant (C14). Fake-tested here; live steps written into the packets and PRs.
6. Footprint on real hardware (0.5% core, 50 MB, 1 s/group) — benchmark gate with fakes here; real measurement on the owner's server.

## 3. Tools and skills used per step

| Step | Tool / agent | Skill | Commands |
|---|---|---|---|
| Plan, packets, graph, state | Orchestrator (Ultimate, this session): Read/Write/Edit, Bash, TaskCreate/Update | none (Qoder has no `forge-plan`; orchestration brief §2 is its equivalent) | `git`, file writes under `.forge/blackbox-expansion/` |
| Code survey | Explore subagents (read-only) | none | — |
| Implementer dispatch | `.qoder/agents/implementer-efficient.md`, `implementer-performance.md` (custom subagents, own context) | none | `go build ./...`, `go vet ./...`, `go test ./internal/blackbox/... -count=1`, `GOOS=linux go vet ./internal/blackbox/...`, `cd web && npm test` (frontend packets) |
| Parallel implementers | separate `git worktree` per packet, merged into the wave branch | none | `git worktree add/remove`, `git merge --no-ff` |
| Batch review | `.qoder/agents/reviewer-performance.md` / `reviewer-ultimate.md` (read-only) | none | `git diff`, `go test` re-run |
| Agent registration | `.qoder/agents/*.md` + `/agents reload` (or `manage_app_agent` if the CLI command is unavailable) | none | — |
| Delivery | Bash + `gh` | none | `git push`, `gh pr create --base feat/investigation-timeline` (never merge) |

No listed Qoder skill is required for this Go/Windows work; frontend packets use the repo's own `npm test` (node:test). Skills are deliberately not used so the run is reproducible from the packets alone.

## 4. Group order, unconfirmed items, blocked packets

Build order (handoff §4): 1 Foundation → 2 Core evidence → 3 Per-process memory/CPU → 4 More host evidence → 5 SQL (only after BL-123+BL-124 built **and Ultimate-reviewed**) → 6 Optional.

| Group | Backlog items | Packets |
|---|---|---|
| 1 Foundation | BL-122, BL-121, BL-117, BL-105 | BBX-1-01..1-12 |
| 2 Core evidence | BL-104, BL-106, BL-108, BL-100+BL-107 | BBX-2-01..2-04 |
| 3 Per-process | BL-109, BL-110 (fallback now; EventPipe blocked), BL-116 | BBX-3-01..3-04 |
| 4 Host evidence | BL-111, BL-112, BL-114, BL-115, BL-125 | BBX-4-01..4-05 |
| 5 SQL | BL-123, BL-124 → gate → BL-101, BL-102, BL-118, BL-119 → BL-103 | BBX-5-01..5-17 |
| 6 Optional | BL-113 (blocked), BL-120, BL-126 (blocked) | BBX-6-01..6-03 |

Unconfirmed items (handoff §5) and what they block:
- `typeperf -q` paths → live-tick of BL-104/106/113/114/115/100/107 (code built with researched defaults; **BBX-6-01 VM steal is blocked from dispatch** because the counters only exist on specific hypervisors and the paths are unverified).
- SYSTEM → diagnostic pipe → **BBX-3-04 (EventPipe) blocked**; BL-110 fallback (BBX-3-02) is built now.
- EventPipe cost / snapshot freeze → **BBX-6-03 (BL-126) blocked**.
- Hosting model → resolver handles both; no packet blocked.
- Live SQL grant → BL-101/102/118/119 live tests; code + fake tests built now, SQL ships **off by default**.

## 5. Risk table

Tier legend: E = Efficient implementer, P = Performance implementer; reviewer P = Performance, U = Ultimate. Any packet touching SQL, credentials, service accounts, ACLs, the HTTP endpoint, process access or the footprint budget gets a Ultimate batch review (orchestration §1/§5).

| Packet | Backlog | Impact | Risk | Impl tier | Reviewer |
|---|---|---|---|---|---|
| BBX-1-01 budget state machine | BL-122 | throttling correctness | med (logic) | P | U (footprint) |
| BBX-1-02 counter engine core | BL-121 | every rate/gauge | med (rules) | P | U (footprint) |
| BBX-1-03 config targets | BL-117 | config surface | low | E | U (batch w/ footprint) |
| BBX-1-04 group types + Sample fields | all groups | wire format | low | E | U |
| BBX-1-05 group registry skeleton | all groups | wiring | low | E | U |
| BBX-1-06 PDH source | BL-121/122 | all counter groups | med (Windows API, handles) | P | U |
| BBX-1-07 recorder/host budget integration | BL-122 | tick loop, priority | high (footprint, loop) | P | U |
| BBX-1-08 resolver: process+runtime | BL-105 | per-process targeting | med (process access) | P | U |
| BBX-1-09 budget benchmark gate | BL-122 | ships gate for all groups | med | P | U |
| BBX-1-10 wizard targets endpoint | BL-117 | HTTP endpoint | med (endpoint) | P | U |
| BBX-1-11 resolver: pool/service map | BL-105 | targeting | med | P | U |
| BBX-1-12 wizard picker UI | BL-117 | frontend | low | P | U (batch w/ endpoint) |
| BBX-2-01 HTTP.sys queues | BL-104 | evidence | med (wildcards) | P | U |
| BBX-2-02 IIS worker + WAS | BL-106 | evidence | med (instances) | P | U |
| BBX-2-03 disk + CPU split | BL-108 | evidence | med (rates) | P | U |
| BBX-2-04 SQL host counters | BL-100/107 | evidence | med (named instances) | P | U |
| BBX-3-01 CLR counters | BL-109 | per-process | med | P | U |
| BBX-3-02 .NET 10 fallback | BL-110 | per-process | med | P | U |
| BBX-3-03 per-process I/O | BL-116 | per-process | med | P | U |
| BBX-3-04 EventPipe collector | BL-110 | per-process | **blocked: live pipe proof + cost** | P | U |
| BBX-4-01 event log tailer | BL-111 | host evidence | med (no text) | P | U |
| BBX-4-02 TCP counters | BL-112 | host evidence | med (no addresses) | P | U |
| BBX-4-03 ASP.NET queues | BL-114 | host evidence | low (data) | E | U |
| BBX-4-04 memory pressure | BL-115 | host evidence | low (data) | E | U |
| BBX-4-05 busiest list shape | BL-125 | Step 9 consumer | med (ranking) | P | U |
| BBX-5-01 SQL service install + DACL | BL-123 | credentials/ACLs | **high** | P | U |
| BBX-5-02 SQL service mode + grant print | BL-123/124 | service account | **high** | P | U |
| BBX-5-03 recorder fixed error codes | BL-123 | no text in events | med | P | U |
| BBX-5-04 /status SQL shape (C11) | BL-123 | HTTP endpoint | **high** | P | U |
| BBX-5-05 allowlist + lint (C1/C2) | BL-124 | SQL text | **high** | P | U |
| BBX-5-06 connection controls (C3/C4/C5/C12) | BL-124 | SQL session | **high** | P | U |
| BBX-5-07 rights ceiling + error codes (C8/C10) | BL-124 | privileges | **high** | P | U |
| BBX-5-08 back-off/auto-off persist (C7) | BL-124 | restart survival | **high** | P | U |
| BBX-5-09 output structs + row caps (C9/C6) | BL-124 | data shape | **high** | P | U |
| BBX-5-10 SQL runner + budget bench (C13) | BL-124 | tick isolation | **high** | P | U |
| BBX-5-11 requests sampler | BL-101 | SQL data | **high** | P | U |
| BBX-5-12 wait-stats sampler | BL-102 | SQL data | **high** | P | U |
| BBX-5-13 sched/semaphore/tempdb | BL-118 | SQL data | **high** | P | U |
| BBX-5-14 file stats + AG | BL-119 | SQL data | **high** | P | U |
| BBX-5-16 lane source (server) | BL-103 | HTTP endpoint | med | P | U |
| BBX-5-17 lane source (web) | BL-103 | frontend | low | P | U (batch) |
| BBX-6-01 VM steal | BL-113 | VM only | **blocked: live typeperf on VM** | P | U |
| BBX-6-02 dump catalogue | BL-120 | file names only | med (no content) | P | U |
| BBX-6-03 allocation sampling | BL-126 | EventPipe | **blocked: pipe proof + cost** | P | U |

## 6. Execution rules (from orchestration §5, applied)

- Dispatch gives a subagent only its packet file, the files the packet names, and handoff §3 rules. End condition + checklist first; report format last.
- Reports land in `.forge/blackbox-expansion/reports/<packet>.md`; return values to the orchestrator are short (status, checklist, files, open questions).
- One fix round per batch; a packet that fails once retries at one tier higher with its report + failure output; never more than twice.
- Batch review per wave (tier per risk table); findings split by packet; re-review only unclear/high-severity findings; then commit the wave.
- "Break the app" pass after each wave: stopped target, recycled pool, PID reuse, missing counter, counters reset, permission denied, disk full.
- STATE.md updated after every step. Group 5 samplers start only after BL-123+BL-124 pass Ultimate review.