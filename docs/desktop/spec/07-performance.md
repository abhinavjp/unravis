# Spec 07: performance and footprint (BL-420 to BL-437)

Stage 2 (spec). Inputs: groom 07, `DECISIONS-draft.md`, conflicts/questions of groom 01-06 and 08, report 08, specs 01, 02, 03, 04, 06 (already written), and the code in `/home/claude/unravis` read on 2026-11-21. Report numbers are the report's own, mostly `[INFERRED]`. Nothing here is built. No BL row is ticked until it runs on the real data path.

## 0. Where the code differs from the report or the groom (read first)

| Groom / report says | Code says | Effect |
|---|---|---|
| Go 1.27 needed for `goroutineleak`; maybe not on it yet | `go.mod`: `go 1.27.3`. | Leak test can use `goroutineleak` if the experiment is on in the toolchain; else the `runtime.NumGoroutine` retry count (spec 01 and 03 already do this). Test runs both when available. |
| `ncruces/go-sqlite3` wasm2go vs wazero JIT unknown | `go.mod`: `ncruces/go-sqlite3 v0.35.6`, `go-sqlite3-wasm/v6 v6.3.35304` (indirect). Connection memory counts in the Go heap (report). | BL-423 step 1 prints `go list -m -json` and records which engine runs; the numbers below are not driver-specific. |
| Set `GOGC=50` and a soft limit (new) | `debug.SetMemoryLimit(memorySoftLimit)` already runs in `cmd/audit-explorer/main.go` (lines 206 and 349), `memorySoftLimit = 256 << 20`. No `SetGCPercent` for the app. The Blackbox host has its own (`internal/blackbox/host.go`: 64 MiB, GC 40). | BL-423 extends an existing call. Never lower the limit below today's 256 MiB. |
| Bound the SQLite pool (new), `PRAGMA cache_size` `-8000` | `internal/casestore/sqlite/sqlite.go`: `SetMaxOpenConns(4)`, `SetMaxIdleConns(2)`, `ConnMaxLifetime(0)`, and `PRAGMA cache_size=-65536` (64 MiB) per connection, key set per connection. `casefile.go`: 1 connection. | Cap exists. The real memory risk is 4 x 64 MiB page cache. BL-423 changes cache size, not the cap. |
| "~30 min update timer" | `internal/server/updater_auto.go`: `autoTick = time.Minute`, one `time.Ticker`, each tick a few comparisons. Page presence pings every 45 s while visible (`web/src/lib/updateState.svelte.ts`); status refresh every 10 min. | Keep the 1 min ticker (it is not sub-second). Do not rewrite it. |
| Find every `time.Ticker` | Tickers: `updater_auto.go` 1 min; `sampler/sampler.go` (interval of a running collection only); `server/import_jobs.go` 50 ms and `casestore/sqlite/case.go` 5 ms (both only while an erase or import waits); `cmd/unravis-setup/main.go` (setup UI). Timers: many one-shots. None sub-second at idle. | BL-424 is mostly a guard test, not a rewrite. |
| Define "busy / in use" once (BL-425) | `Updater.InUse()` in `updater_auto.go` already is that: presence (2 min), MCP (`NoteMCP`, 5 min), `inflight` (non-GET requests only, `routes.go` ~line 456), `busy`, `AddBusy`. | One idea of "in use" exists. BL-425 reuses it and adds one thing it lacks: long GETs (see D4). |
| M4 downsampling does not exist (BL-430) | Series are already bucketed on the server: `casestore.Series` (`internal/casestore/series.go`, max 2000 buckets, min/mean/max/count per bucket) and `bbSeries` (`internal/server/blackbox_series.go`, 240 default, 600 max points, `[bucketStartMs, avg, lo, hi]`). Min and max per bucket are already kept, so spikes survive today. Payload is bounded (600 points x 4 x lines). | BL-430 shrinks: add `first` and `last` to the bucket and tie bucket count to pixel width. The "4 x width" cap becomes a rule inside the existing code, not a new package. |
| Charts need `animation:false`, canvas, `showSymbol:false` | `web/src/lib/charts.ts` imports only `CanvasRenderer`; `animation:false` is already set in `charts.ts`, `resource.ts`, `BlackboxCharts.svelte`, `TrafficSlowness.svelte`, `RequestVolume.svelte`. `showSymbol:true` on several line series (`charts.ts` 129, 130, 179, 233, 234; `resource.ts` 648). `BlackboxCharts.svelte` uses `showSymbol: cd.data.length < 40`. No `large`/`progressive`. | BL-429 chart part = a symbol threshold and `large` on big series. Not a rewrite. |
| Pause polling on hidden window | Page timers: `updateState.svelte.ts` (presence 45 s, status 10 min, download poll 1 s only while downloading); `CoveragePanel.svelte` and `PatternsPanel.svelte` 30 s clock; `CaseKeyCard.svelte` wait timer. Presence already skips when `document.visibilityState !== 'visible'`. No SSE or WebSocket in `web/src` (grep: none). | Fewer things to pause than the groom feared. One helper, three call sites. |
| `--no-update-check` flag (report) | Real surface: `--check-update` (one-shot) and `POST /api/update/settings` (`UpdaterSettings`, field `Auto`). | No new flag. The soak script turns the existing setting off (D9). |
| CI runner `windows-2025` | `ci.yml`, `release.yml`, `verify-release.yml` use `windows-2022` today. Specs 01 and 06 use 2022. | Use `windows-2022` for all area 07 jobs (D10). |
| `scripts/bench-footprint.ps1` | No `scripts/` dir. Repo style: `tools/<area>/*.ps1`. Spec 01 BL-300 plans `tools/desktop/Measure-Baseline.ps1`. | Harness lives at `tools/desktop/` (D1). |
| Build flags for release Go exe (BL-421) | `build.ps1`: helper (`unravis-blackbox`) already `-trimpath -ldflags "-s -w ..."`; blackbox script too. App, keeper, setup built with `-ldflags $LdFlags` and no `-trimpath`, no `-s -w`. Lite has `-trimpath` only. `CGO_ENABLED=0` is set (line 58). | BL-421 is a real change for 4 of 5 exes. |
| `//go:embed` UI split (BL-422) | Root `assets.go` embeds `web/dist` (3.2 MB) into `unravis.exe`. Spec 01 D2 default: sidecar is the same `unravis.exe`. | See D5: BL-422 shrinks to a size check until the BL-305 spike. Pre-compression is dropped (3.2 MB; no measured need). |

## 1. Purpose and boundaries

Owns:
- Measurement: the footprint library and gate (`tools/desktop/FootprintLib.ps1`, `Test-Footprint.ps1`, `budgets.json`), the network soak (`Invoke-NetSoak.ps1`), the power sweep.
- Go runtime tuning: GC percent, soft limit, SQLite cache, power policy (`internal/power`), idle audit tests.
- Rust idle policy: the state machine `idle.rs` (visible, hidden, suspended, destroyed) and EcoQoS code `power.rs`.
- Chart path: visibility pause helper, ECharts options for big series, `width`-aware bucketing, binary series response, debounced zoom/pan.
- WebView2 network-silence settings (function `lockdown::network()` only).
- Budget numbers (`tools/desktop/budgets.json`) and the footprint CI gate.
- WebView2 OneAuth doc and the optional firewall rule logic.

Does not own:
- Tray, window, single instance, supervisor, locator, pipe: area 01. Window close and destroy mechanism is `window.rs` (01, BL-308). Area 07 owns the timing and suspend/throttle logic that `window.rs` calls.
- WebView2 lockdown (DevTools, extensions, downloads, navigation, CSP): area 02 (`lockdown::security()`).
- MCP grants, code mode, CLI: areas 03, 04, 05. They report footprint; 07 holds the gates.
- Installer, OTA, signing, the setup step that runs the firewall command: area 06 (07 gives the command and tests it).
- Test-runner layout, Playwright base config: area 08.

## 2. Resolved decisions

| # | Question / conflict | Decision | Reason |
|---|---|---|---|
| D1 | Where the harness lives; overlap with 01 BL-300 | One shared library `tools/desktop/FootprintLib.ps1` (area 07, BL-420): `Get-ProcessTreeSample`, `Measure-Idle`, `Get-ExeSizes`, `Write-FootprintJson`. Spec 01's `Measure-Baseline.ps1` (BL-300) dot-sources it. BL-420 delivers the library and the `-Mode Idle` entry `tools/desktop/Measure-Footprint.ps1`; BL-300 adds MCP latency and install timing on top. Same output file name rule: `docs/desktop/baseline-<tag>.md` plus `.json`. | One sampler, two callers, no forked numbers. |
| D2 | Are the section 9 budgets final? (Q8) | They are targets, not facts. Live in one file `tools/desktop/budgets.json` (keys in 3.7). Reset exactly twice, each by an ADR `docs/adr/NNNN-budgets-after-<p0\|p2>.md` and an edit of that file: after P0 baseline and after the first P2 soak on nachi's PC. Between resets nothing edits them. "Fail" values gate CI; "target" values only warn. | Report marks most `[INFERRED]`; one file + two planned resets removes the wait. |
| D3 | Transport for bulk chart data (conflict 1; spec 01 D16) | Pipe through the Rust relay, returning `tauri::ipc::Response` bytes. No loopback fetch is built. After M4/width bucketing a typical series answer is under 1 MB (D7), so IPC should not be the problem. BL-432 is merged into spec 01 BL-313 (script, results table `docs/desktop/bench-bl313.md`); area 07 contributes only the pass rule (below) and the ADR text. Rule: pass if p95 of "request to chart painted" for a 1 MB answer is at most 100 ms on nachi's PC. Fail makes a new BACKLOG row and the exception terms already in spec 02 row 2.21; nothing is built before then. | Matches DECISIONS section 2.1 and spec 02. Avoids two benchmarks. |
| D4 | One idle definition (conflict 3) | Single source: `Updater.InUse()`. Power policy (`internal/power`) asks it through an injected `func() bool` and adds two inputs the updater does not have: `Touch()` on every authorized request (any method) and an in-flight counter for every authorized request (so a long GET chart scan is not idle). Wiring point: `internal/server/routes.go`, next to the `u.NoteMCP()` block (after `h.authorized`). MCP stays "busy" through `NoteMCP` (5 min window); an idle stdio client that sends nothing does not hold the backend awake. | Reuses what BL-171 built; closes the long-GET gap; no second clock. |
| D5 | Sidecar without embedded UI (BL-422; Q4) | Default: **same `unravis.exe`, keep the embed.** BL-422 is cut to a size check, not a build tag, until spec 01 BL-305 says `unravis-core.exe`. If BL-305 picks the split, `unravis-core.exe` is built with tag `tauri_sidecar` (file `assets_sidecar.go` with `//go:build tauri_sidecar` returning an empty FS; `assets.go` gets `//go:build !tauri_sidecar`). Until then the tag exists in code but no build script uses it. | Browser fallback needs the embed; the saving is 3.2 MB at most. Decided by spike BL-305. |
| D6 | Which UI state survives window destroy (Q5) | The Go side holds all case state. UI-only state to restore on recreate: the open case id, the active view/tab, the zoom range, scroll position per main pane. Saved by the UI in `sessionStorage` is not usable (destroyed with the webview), so the UI posts `PUT /api/desktop/ui-state` (role `ui`, body below) on `visibilitychange` to hidden and `pagehide`; the shell reads it back with the first `GET /api/desktop/state` after recreate. Not restored: open drill pop-ups, text typed in unsaved forms, expanded rows, hover. Wait: if a form with unsaved text is open, hide does not start the destroy timer (D8). Never stored: any case content. | Small, safe, no case data in the blob. |
| D7 | Downsampling target (conflict: M4 vs existing buckets) | Do not add a new downsampling package. In `blackbox_series.go`: `bbAcc` gets `first` and `last` (time order); a request field `width` (CSS px) sets `points = clamp(width, 60, bbSeriesMaxPoints)` when `points` is absent; a request field `shape:"m4"` returns `[t, first, lo, hi, last]` (5 numbers) instead of `[t, avg, lo, hi]`. Default shape is unchanged so browser, Lite and old clients keep working. Case series (`casestore.Series`) gets no M4 now (its buckets already carry min and max; no `first/last`). Multi-series: each line is bucketed on the same grid (bucket start = `start + i*width_ms`), already true in `buildBBSeries` (one `bbBuckets`). Gaps: a bucket with no sample is absent (never 0), as today. | The groom's "4 x width, keep every spike" is already true for min and max. This adds the two missing values cheaply. |
| D8 | Tray grace (Q6) | 60 s fixed const `config::GRACE_SECS = 60` (area 01 file), not a user setting. Destroy waits as spec 01 D10 says (no import job, no request in flight, no open channel) plus one more condition owned here: no dirty form (the UI sets `window.__unravisDirty`; the shell reads it by the ui-state PUT field `dirty`). Re-check every 15 s while a condition holds. An active MCP session does NOT block destroy (MCP talks to Go, not to the window). | Same as 01 plus the dirty form; MCP is independent of the UI. |
| D9 | Test-only no-update flag (Q7) | No new flag. `Invoke-NetSoak.ps1` first posts the existing settings route (`POST /api/update/settings`, fields from `UpdaterSettings` in `internal/server/updater.go`) to turn automatic checking off, runs the "updater off" soak, then turns it on and runs the "updater on" soak. Token and port or pipe come from the locator (`internal/locator`, spec 01). | No test-only code paths in the release exe. |
| D10 | CI runner (conflict 7) | `windows-2022` everywhere in area 07. Footprint gate script `tools/desktop/Test-Footprint.ps1` runs in a new `ci.yml` job `desktop-footprint` (path filter: `desktop/**`, `internal/**`, `cmd/**`, `web/src/**`, `tools/desktop/**`) and is called by `Invoke-VerifyRelease.ps1` step `F1` (area 06 owns that file; PR order per spec 06 4.9: BL-153, BL-401, BL-408, then BL-435). Previous-release comparison reads `tools/desktop/footprint-baseline.json` in the repo (updated by hand-PR after each good rc), not a release asset. | Matches existing workflows; avoids new release assets in the signed set. |
| D11 | Who runs elevated and hardware items (Q10) | Only nachi, on nachi's PC, at the end of P3 (BL-434) and P4 (BL-436, BL-437). Each script prints exactly one UAC prompt and what it will do first. The CI jobs are the only automatic part. Items stay unticked until the live run exists. | No waiting loop; the build is not blocked. |
| D12 | Server 2016 and small-RAM PC (Q2) | No box assumed. Code paths exist (Server 2016 fallback in `internal/power`, low-RAM limit rule in `tuneMemory`) and have unit tests with injected OS version and RAM. Live acceptance lines stay tagged "live PC, needs box" and BACKLOG stays unticked for those lines. A VM on nachi's PC with 2 GB RAM counts as the small-RAM test. | Unit tests prove logic; BACKLOG stays honest. |
| D13 | Real big case for budgets (Q3) | The gate uses two inputs: (a) CI: a seeded case file built by the real importer from a generated HAR-like source (`tools/desktop/New-SeedCase.ps1` calls the app's own import route, so the schema is the real one, not a fake shape); 1M events. (b) Live: nachi's largest real case; the script prints its row count and records it in the report. If nachi names no case, the live lines say "no real big case" and the line stays unticked. | Owner rule: real schema in CI, real data live. |
| D14 | Strict network firewall rule per user? (Q9) | Machine scope only. Windows Firewall rules need admin; there is no per-user rule. Setup field `strictNetwork` (spec 06 4.4) runs the command in 3.8 during the elevated step. Default off. | Platform fact; spec 06 reserved the field. |
| D15 | Release flags (BL-421) | All shipped Go exes: `-trimpath -ldflags "-s -w -buildid= <existing -X ...>"` and `CGO_ENABLED=0`. `debug.ReadBuildInfo` stays valid with `-s -w`; version still comes from `-X internal/version.*`, which `-s -w` does not remove. `-buildid=` joins the reproducible-build check BL-195 (that item owns the check). Keeper, setup and Lite keep their existing `-H` flags. | Four of five exes lack these flags today. |
| D16 | EcoQoS on Windows 10 / Server 2016 (BL-425) | Order: try `ProcessPowerThrottling` with `EXECUTION_SPEED|IGNORE_TIMER_RESOLUTION`; on `ERROR_INVALID_PARAMETER` retry with `EXECUTION_SPEED` only; on any other error or if build < 16299 use `SetPriorityClass(BELOW_NORMAL_PRIORITY_CLASS)`. Never `timeBeginPeriod`. Memory priority very low is set on throttle and restored to normal on clear. Never touch WebView2 child processes. | Matches the report; Server 2016 is build 14393. |
| D17 | Power policy interplay with area 06 silent update | Silent apply is unchanged. The update ticker (1 min) wakes a throttled process only for a few comparisons; if it must download or apply it calls `power.Touch()` first so the work is not run at EcoQoS speed. | Download speed matters more than 1 wakeup. |
| D18 | Code-mode and MCP idle cost (conflict 8) | At tray-idle no sandbox process, no pre-warmed worker, no `mcp.db` open (spec 03 4.7, spec 04 footprint). BL-435 asserts it: process list at idle contains only `unravis-desktop.exe` and `unravis.exe` of this install (plus services). A test fails if `unravis-sandbox.exe` exists at idle. | Budget counts only what we start. |
| D19 | Rust async runtime (BL-426) | Build one `tokio::runtime::Builder::new_multi_thread().worker_threads(2).enable_all()` in `main.rs` and register it with `tauri::async_runtime::set`. No `tokio_unstable` in release. The debug wakeup counter uses `RuntimeMetrics` only in builds with cargo feature `wakeups` (which also needs `--cfg tokio_unstable`; never in CI release builds). | Report's counter may need the unstable cfg; keep it out of production. |
| D20 | Page timers vs hidden window (BL-429) | One helper `web/src/lib/visibility.ts` (`onVisible(start, stop)`, `isHidden()`); the three timer sites call it. Presence already pauses itself and stays as is (BL-168 area). While hidden, in-flight requests are aborted by the callers' `AbortController`; nothing new is sent. Polls resume with one refresh on show. | Smallest change; no streaming transport exists to pause. |

## 3. Design

### 3.1 Components

```
tools/desktop/ FootprintLib.ps1  Measure-Footprint.ps1  Test-Footprint.ps1  New-SeedCase.ps1
               Invoke-NetSoak.ps1  Invoke-PowerSweep.ps1  budgets.json  footprint-baseline.json
Go (new)       internal/power/  policy.go  throttle_windows.go  throttle_other.go  osver_windows.go
               cmd/audit-explorer/memtune.go  (tuneMemory)   cmd/audit-explorer/power.go (wiring)
               internal/server/desktop_window.go (POST /api/desktop/window, PUT /api/desktop/ui-state)
Go (change)    build.ps1  internal/casestore/sqlite/sqlite.go  internal/server/routes.go
               internal/server/updater_auto.go (Presence.Reset)  internal/server/blackbox_series.go
Rust (new)     desktop/src-tauri/src/idle.rs  power.rs   (change: window.rs, lockdown.rs, main.rs)
Web (new)      web/src/lib/visibility.ts  web/src/lib/seriesFetch.ts
Web (change)   updateState.svelte.ts  CoveragePanel.svelte  PatternsPanel.svelte  charts.ts  resource.ts
               BlackboxCharts.svelte  blackboxSeries.ts
Docs           docs/desktop/baseline-<tag>.md  docs/desktop/spike-bl428.md  docs/adr/NNNN-*.md
               docs/user/webview2-network.md (BL-437)
```

### 3.2 Go memory tuning (BL-423)

`cmd/audit-explorer/memtune.go`:
```go
// tuneMemory replaces the bare debug.SetMemoryLimit(memorySoftLimit) calls in main.go (run and runMCP).
func tuneMemory(totalRAM uint64) (limit int64, gcPercent int)
```
- `limit = max(256<<20, min(totalRAM/4, 512<<20))`. `totalRAM` from `GlobalMemoryStatusEx` (new `memtune_windows.go`; `memtune_other.go` returns 0, then the 256 MiB floor is used). Never below today's value.
- `gcPercent = 50`. Calls `debug.SetGCPercent(50)` and `debug.SetMemoryLimit(limit)`. Never `GOGC=off`. `GOGC` env, when set by the user, wins (read `os.Getenv("GOGC")` first; then skip `SetGCPercent`).
- `main.go` lines 206 and 349 call `tuneMemory(totalRAM())`. `--mcp` (`runMCP`) uses `gcPercent=50` and the same floor.
- SQLite (`internal/casestore/sqlite/sqlite.go`): constants `cacheKiB = 16384` (replaces `-65536`), `SetMaxIdleConns(1)`, `SetConnMaxIdleTime(30 * time.Second)`. Reason: wasm linear memory does not shrink while a connection lives; closing an idle connection frees it. `SetMaxOpenConns(4)` stays (a test already pins that reads do not wait for an import). `casefile.go` stays at 1.
- Import connection: if `perfbench` import throughput with `cacheKiB=16384` is more than 10% below `-65536`, `initConn` takes a `cacheKiB` argument and the import connection keeps `-65536`. Decided by the BL-423 sweep (default used until then: 16384 for all).
- Cache sweep (BL-423 step 2): `{8192, 16384, 65536}` on nachi's largest real case: series query p95 and summed PWS. Chosen value = smallest within 10% of the best p95. Result saved in `docs/desktop/bench-bl423.md`.
- Case memory release: `h.cases.Open` is per request (`openCase` defers `c.Close()`), so connections are not held between requests. `PRAGMA shrink_memory` is therefore a no-op for closed cases; the idle hook only runs `debug.FreeOSMemory()` (below). If BL-423's live run shows a Go heap that stays high, add `(*sqlite.Case).ShrinkMemory()`; not built now.
- Idle hooks: `power.Policy` (3.4) runs `debug.FreeOSMemory()` once when it enters idle and once when a case is closed after a long request (call from `routes.go` after a request that ran over 5 s). One call each, not a loop.

### 3.3 Idle audit (BL-424)

- New test `internal/server/idle_audit_test.go` `TestNoSubSecondTickerAtIdle`: starts the real handler with a temp data dir (`--demo` store), waits 3 s, takes `runtime/pprof` goroutine profile, and fails if any stack contains `time.NewTicker` frames whose period source is under 1 s other than the listed allow-list (`import_jobs.go cancelCaseUntilStopped`, `casestore/sqlite/case.go` import wait; both stop when their wait ends). Static companion `internal/server/ticker_scan_test.go`: parses `*.go` (non-test) with `go/ast`, finds every `time.NewTicker(<const expr>)` and `time.Tick`; fails on a constant under `time.Second` unless the line above has the comment `// idle-ok: <reason>`. Add that comment to the two allowed sites.
- `TestGoroutineLeakAfterCycles` (`cmd/audit-explorer/idle_leak_test.go`): 5 cycles of open and close of a demo case through the handler and 5 tray cycles using the fake tray deps in `trayapp_test.go`; after each cycle run `runtime.GC()`, retry up to 2 s for `runtime.NumGoroutine()` to return to baseline plus 2; at the end idle goroutines <= 30 (value from the first run on the CI runner, written into `budgets.json` `goBaselineGoroutines`; limit is baseline + 5). When the toolchain has the `goroutineleak` profile (`GOEXPERIMENT=goroutineleakprofile`), the CI job sets it and the test also asserts the profile is empty.
- `GODEBUG=updatemaxprocs=0`: not set. Go 1.25+ already has the container-aware update; the report calls it optional and it has no measured gain. Dropped, not deferred.

### 3.4 Go power policy (BL-425)

`internal/power/policy.go`:
```go
type Mode int // Normal, Idle
type Config struct {
    Grace  time.Duration       // 30 * time.Second
    Check  time.Duration       // 10 * time.Second (only while Normal)
    Now    func() time.Time    // injected; fake in tests
    InUse  func() bool         // Updater.InUse
    Apply  func(Mode) error    // throttle_windows.go or fake
    OnIdle []func()            // debug.FreeOSMemory
}
func New(c Config) *Policy
func (p *Policy) Touch()          // every authorized request; clears Idle at once
func (p *Policy) Begin() func()   // in-flight counter, any method
func (p *Policy) Run(ctx context.Context)
func (p *Policy) Mode() Mode
```
Behavior: while Normal, a `time.Timer` fires every `Check`; if `Now()-lastTouch >= Grace`, in-flight == 0 and `!InUse()` then `Apply(Idle)`, run `OnIdle`, and the goroutine blocks on a channel (no timer armed). `Touch()` in Idle mode sets `Apply(Normal)` and wakes the loop. Idle costs zero wakeups. A throttled process has no timer to wake.

`throttle_windows.go` (`Apply` implementation, raw `kernel32.SetProcessInformation` via `windows.NewLazySystemDLL`, same style as `internal/sealedfile/platform_windows.go`):
- `ProcessPowerThrottling` = 4: struct `{Version:1, ControlMask, StateMask}` with masks `EXECUTION_SPEED=0x1`, `IGNORE_TIMER_RESOLUTION=0x4`. Normal sets `StateMask=0` with the same `ControlMask` (returns control to the system).
- `ProcessMemoryPriority` = 0, `MEMORY_PRIORITY_INFORMATION{MemoryPriority}`: very low = 1, normal = 5.
- Fallback per D16 uses `windows.SetPriorityClass`. Values come from the SDK header, not from the report sketch; a CI test on windows calls each with the real process and expects no error on `windows-2022` (EcoQoS is present on build 20348).
- `osver_windows.go`: `RtlGetVersion` build number; injected into `throttle` for tests.
- `throttle_other.go`: `Apply` returns nil (no-op).

Wiring: `cmd/audit-explorer/power.go` builds the policy in the `--pipe-server` run branch and the plain run branch (both owner modes; not `--mcp`, not `--check-update`). `InUse = updater.InUse`. Server hook in `routes.go` after `h.authorized`: `p.Touch(); defer p.Begin()()`. `Handler` gets option `WithPower(*power.Policy)` (new option, same pattern as `WithShutdown` in spec 01). Window-hidden route (D4): `POST /api/desktop/window` body `{"visible":false}` (role `ui`, token) calls `Presence.Reset()` so the 2 min presence window ends when the shell destroys the window; `{"visible":true}` calls `Touch()`. Without this, tray-idle would start about 2 min late.

Does not run in: `unravis-blackbox` (separate service, Session 0), `unravis-keeper`, `unravis-lite`.

### 3.5 Rust idle policy (BL-426, BL-427, BL-428)

`desktop/src-tauri/src/idle.rs` (pure, no Tauri types in the machine; trait for effects):
```rust
pub enum State { Visible, Hidden{since: Instant}, Suspended, Destroyed }
pub enum Event { Close, Show, Tick, Busy(bool), Quit }
pub trait Host { fn hide(&self); fn try_suspend(&self) -> bool; fn resume(&self);
                 fn destroy(&self); fn create_and_show(&self); fn eco(&self, on: bool);
                 fn busy(&self) -> Busy; fn now(&self) -> Instant; }
pub struct Machine<H: Host> { /* state, grace, last_check */ }
impl<H: Host> Machine<H> { pub fn on(&mut self, e: Event) -> State }
```
Transitions:
- `Visible --Close--> Hidden`: `hide()`, `eco(true)`, `host.try_suspend()` immediately (result logged, failure is fine) -> `Suspended` if true.
- `Hidden|Suspended --Tick(after GRACE_SECS and busy()==none)--> Destroyed`: `destroy()`. If busy: stay, re-check each 15 s (area 01 D10 plus D8 here).
- `Hidden|Suspended|Destroyed --Show--> Visible`: `eco(false)` first, `resume()` if suspended, `create_and_show()` if destroyed (via `lockdown::apply`, so lockdown and network settings are applied again, spec 02 conflict 8).
- `Quit` from any state: no effect on the machine; `main.rs` runs the shutdown path.
- One timer: `tokio::time::sleep_until(deadline)` armed only in `Hidden`/`Suspended`; none in `Visible` or `Destroyed`. No interval.
`window.rs` (area 01) implements `Host` and routes `CloseRequested`, tray click and the single-instance `Open` event to `Machine::on`. 01's BL-308 ships first with the simple timer; BL-427 replaces it by `idle.rs` in one commit and keeps 01's test names (`window_destroy_waits_for_busy`, `close_twice_is_safe`, `exit_requested_keeps_app`).

`power.rs`: `pub fn set_eco(on: bool)`: `SetProcessInformation(GetCurrentProcess(), ProcessPowerThrottling, ...)` via `windows-sys` (`Win32_System_Threading`), same fallback order as D16 (no `BELOW_NORMAL` on the Rust host: if EcoQoS fails, skip). Called by `Host::eco`. Does not touch child processes.

WebView2 spike (BL-428, `idle.rs` `Host::try_suspend` stub until then returns false): `with_webview(|w| ...)`, `ICoreWebView2_3::TrySuspend(handler)` and later `Resume`; and `ICoreWebView2_19::SetMemoryUsageTargetLevel(LOW)` via `webview2-com` on a visible-but-idle window, `NORMAL` on show. The spike writes `docs/desktop/spike-bl428.md` with before/after PWS of the `msedgewebview2.exe` tree and the `isSuccessful` log. Defaults until then: `TrySuspend` called but its result is only logged; `MemoryUsageTargetLevel` is not used. If the result file shows LOW saves at least 20 MB on a visible-idle window without slowing the first paint by more than 100 ms, a follow-up row is added (not built now).

### 3.6 Web: visibility, charts, series fetch (BL-429, BL-430, BL-431)

`web/src/lib/visibility.ts`:
```ts
export const isHidden: () => boolean
export function onVisible(start: () => void, stop: () => void): () => void  // calls start now if visible; returns an unsubscribe
```
Used by `updateState.svelte.ts` (status refresh interval), `CoveragePanel.svelte` and `PatternsPanel.svelte` (30 s clock). The download poll (1 s, only while downloading) pauses too and does one refresh on show.

Charts (`web/src/lib/chartPerf.ts`, new, pure functions used by `charts.ts`, `resource.ts`, `BlackboxCharts.svelte`):
- `symbolOption(n: number): { showSymbol: boolean; large?: boolean; progressive?: number }`: `showSymbol = n < 40` (today's rule in `BlackboxCharts.svelte`), `large = n > 2000`, `progressive = 400`, `progressiveThreshold = 3000`.
- `animation:false` is already set; `chartPerf.test.cjs` asserts every `setOption` call site found by a source scan keeps it.
- `appendData` is never used (test greps for it).

Series API (Go, `internal/server/blackbox_series.go`):
- Request body `bbSeriesBody` gains `Width int` (`json:"width"`) and `Shape string` (`json:"shape"`, `""` or `"m4"`).
- `bbAcc` gains `first`, `last float64`, `hasFirst bool`; `add` keeps first by time order (samples are time-sorted by the reader; the unit test shuffles input and asserts the code sorts or the reader's order is relied on, whichever the code does today).
- `bbSeriesPoint` stays `[4]float64`. New `bbSeriesPointM4 [5]float64` and `bbSeriesLineM4`; `bbSeriesAnswer` marshals M4 lines only when `Shape=="m4"`.
- Binary answer: request header `Accept: application/vnd.unravis.series+f64` (also `shape:"m4"` implied). Response `Content-Type: application/vnd.unravis.series+f64`, body: the magic `UBB1`, a `u16le` version (1), `u32le headerLen`, then `headerLen` bytes UTF-8 JSON (the normal answer with every line's `points` replaced by `{"n": <count>}`), then for each point in header order 6 float64 little-endian (48 bytes a point; the shipped layout in `internal/server/blackbox_series_binary.go` is the reference). A reader that does not know the magic or version falls back to JSON. `maxResponseBytes` (16 MiB) check applies. Browser mode and Lite never send this `Accept`; Lite route allowlist (`internal/lite`) is not touched.
- Debounce and cancel: `web/src/lib/seriesFetch.ts`: `fetchSeries(req, signal)`, `createSeriesLoader({debounceMs: 150})` returns `{ load(range), dispose() }`; a new `load` aborts the previous `AbortController`; the stale response is dropped by a sequence number even if abort lost the race. Logs p95 of "range changed to chart painted" in `window.__unravisPerf` (an array, read by the Playwright spec and BL-435; not sent anywhere).
- Desktop: the same fetch goes through `host.ts` (spec 01 4.3) which uses `invoke` returning an `ArrayBuffer` through `tauri::ipc::Response`; `seriesFetch.ts` takes the transport as a parameter so it does not depend on area 01's file.

### 3.7 Gate files

`tools/desktop/budgets.json`:
```json
{ "v":1, "installerMB":{"target":25,"fail":35}, "trayIdleMB":{"target":35,"fail":55},
  "trayIdleCpuPct":{"target":0.05,"fail":0.2}, "windowIdleMB":{"target":180,"fail":240},
  "bigCaseMB":{"target":350,"fail":500}, "coldStartMs":{"target":1500,"fail":2500},
  "reopenMs":{"target":800,"fail":1500}, "panZoomP95Ms":{"target":100,"fail":250},
  "idleCpu60sMs":{"target":30,"fail":120}, "leakedGoroutines":{"target":0,"fail":0},
  "goBaselineGoroutines":30, "nonLoopbackPackets":{"target":0,"fail":0},
  "regressionPct":15 }
```
`Measure-Footprint.ps1` output (`footprint.json`):
```json
{ "v":1, "tag":"v1.2.3", "host":{"os":"...","build":"...","ramMB":0,"cpu":"...","defender":"on"},
  "sizes":{"installerBytes":0,"exes":{"unravis.exe":0,"unravis-desktop.exe":0}},
  "idle":{"seconds":60,"pwsMB":0,"privateMB":0,"handles":0,"threads":0,"cpuMs":0,"procs":[{"name":"","pid":0,"pwsMB":0}]},
  "startMs":[0,0,0], "caseRows":0 }
```
Summing rule: all processes whose image is under the install dir plus every `msedgewebview2.exe` whose command line contains the app's `webview2` dir (so another app's WebView2 is not counted). Both Private Working Set (`Win32_PerfFormattedData_PerfProc_Process.WorkingSetPrivate`) and Private Bytes are written. CPU is the process-time delta (`TotalProcessorTime`) over the soak, not a percent sample. PowerShell 5.1 compatible (no `?:`, no `-Parallel`).

### 3.8 Network silence (BL-433, BL-434, BL-437)

`desktop/src-tauri/src/lockdown.rs` (area 01 creates the file; D15 of spec 01):
```rust
pub fn network_args() -> Vec<&'static str>   // area 07
pub fn security_args() -> Vec<&'static str>  // area 02
pub fn browser_args() -> String              // joins both, one call to additional_browser_args
pub fn network(webview: &WebviewWindow)      // runtime settings, before first navigation
```
`network_args()` = `--disable-background-networking`, `--disable-component-update`, `--disable-domain-reliability`, `--disable-sync`, `--no-pings`, `--disable-features=msWebOOUI,msPdfOOUI`, host-resolver rule `--host-resolver-rules=MAP * ~NOTFOUND, EXCLUDE localhost, EXCLUDE ipc.localhost, EXCLUDE tauri.localhost` (only with the pipe/relay design; in P2 loopback the EXCLUDE list also holds `127.0.0.1`). Final list is fixed by the first soak (BL-434): a flag that has no effect on the soak is removed (less unsupported surface).
`network(webview)` sets: `IsReputationCheckingRequired=false`, `IsGeneralAutofillEnabled=false`, `IsPasswordAutosaveEnabled=false`, spellcheck off, custom crash reporting on (`IsCustomCrashReportingEnabled` through the environment options; env options exist before the controller is created, so it is set in the environment builder, not here). Reputation setting must run before the first navigation: the window is created at `about:blank`, settings applied, then `navigate` to the app URL. Cargo: no `tauri-plugin-updater|http|upload|websocket`; `desktop/src-tauri/deny.toml` bans them (the file belongs to 02's `cargo-deny` job; this area adds the `[bans]` lines).

Soak script `Invoke-NetSoak.ps1` (elevated): (1) enable `Microsoft-Windows-DNS-Client/Operational` log; `auditpol /set /subcategory:"Filtering Platform Connection" /success:enable /failure:enable`; `pktmon filter add -i 127.0.0.1` inverted via `pktmon start --capture --comp nics` then filter in post. (2) Sets update setting off (D9). (3) Starts `unravis-desktop.exe`, opens the real case (via `GET /api/cases` first id or `-CaseId`), zooms a chart through the debug-less UI path (Playwright over CDP is not used; the script calls the series route 20 times with different ranges), hides to tray, waits 120 s, quits. (4) Collects WFP 5156/5157 and DNS 3008 events and pktmon packets for PIDs of `unravis*`/`msedgewebview2` of this install; non-loopback rows listed per process. (5) Restores audit policy and logs on exit (try/finally). Output `netsoak.json`: `{"go":{"packets":0},"rust":{"packets":0},"webview2":{"hosts":[...]}, "allowlist":[...]}`; exit 1 on any Go or Rust non-loopback packet or any WebView2 host not in `tools/desktop/netsoak-allow.txt`. The "updater on" run allows only the feed host read from the same settings route.

OneAuth doc (BL-437): `docs/user/webview2-network.md` lists what WebView2 Runtime 155-157 still calls (`WebView2Feedback#5760`), the host list from the last `netsoak.json`, and the re-check rule: rerun `Invoke-NetSoak.ps1` whenever the installed Runtime major changes. Firewall command (admin, run by setup when `strictNetwork` is true; `internal/setup/firewall_windows.go`, new):
`netsh advfirewall firewall add rule name="Unravis WebView2 outbound block" dir=out action=block program="<runtimeDir>\msedgewebview2.exe" enable=yes profile=any`
`runtimeDir` is read from the registry `HKLM\SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}` value `pv` + path `C:\Program Files (x86)\Microsoft\EdgeWebView\Application\<pv>`. Remove: `netsh advfirewall firewall delete rule name="Unravis WebView2 outbound block"`. Because the folder is versioned, the rule is re-created by setup on every upgrade and by the shell's "Repair" action (no UAC: the shell shows a note "Strict network rule is out of date, run Setup"). Loopback is not blocked by outbound rules on the same host (Windows Firewall filters loopback only with explicit rules), checked in the BL-437 live test. Dry run: `unravis-setup.exe --strict-network --dry-run` prints the two commands and exits 0 (test only reads stdout).

### 3.9 Sequences

Tray hide (Visible to Destroyed to Visible):
1. User closes window. `window.rs` -> `Machine::on(Close)`: `hide()`, `eco(true)` (Rust host throttled), `try_suspend()`.
2. Shell `POST /api/desktop/window {"visible":false}` -> Go `Presence.Reset()`; the UI had already sent `PUT /api/desktop/ui-state` at `visibilitychange`.
3. Go `power.Policy`: no touch for 30 s and `!InUse()` -> `Apply(Idle)` (EcoQoS + memory priority low) -> `FreeOSMemory()` -> block.
4. At 60 s: `Tick` -> busy check (`GET /api/desktop/state`, dirty flag) -> `destroy()`. Child count check: no `msedgewebview2.exe` of this install.
5. Tray click -> `Show`: `eco(false)`; `POST /api/desktop/window {"visible":true}` -> Go `Touch()` -> `Apply(Normal)`; `create_and_show()` runs `lockdown::apply` (security + network), loads UI, UI reads `GET /api/desktop/state` and restores per D6.

Chart zoom (desktop): range change -> `seriesLoader.load(range)` debounce 150 ms -> abort previous -> `host.invokeBinary('series', {caseId|bbId, start, end, width, shape:'m4'})` -> Rust relay -> pipe -> `bbSeries` -> binary body -> `Float64Array` view -> `setOption` with `chartPerf` options.

### 3.10 Failure modes and what the user sees

| Failure | Behavior |
|---|---|
| `SetProcessInformation` fails (both attempts) | Log once at warn (`applog`), mode stays Normal, policy retries only after the next activity. User sees nothing. |
| `TrySuspend` fails or `Resume` fails | Log; destroy still runs at 60 s. On `Show` after a failed resume, recreate the window instead. |
| Destroy races with a request | `busy()` re-check just before `destroy()`; if busy stays Hidden. |
| WebView2 teardown error or AV lock on recreate | Spec 01 fallback (`webview2\<n>` sub-folder). Area 07 adds: if recreate takes > 3 s, tray tooltip "Opening..." and the log gets the elapsed time. After 3 failures in a row the machine stops destroying (`GRACE` becomes infinite for this session) and stays Hidden; RAM cost accepted. |
| Soft memory limit too tight (GC thrash) | `tuneMemory` floor 256 MiB. `debug.ReadGCStats` rate check is not added. Live low-RAM test is the proof; a thrash shows as CPU in BL-435's big-case run (fails on `idleCpu60sMs` or `bigCaseMB`). |
| Binary series response bad (short body, header mismatch) | `seriesFetch.ts` throws; chart keeps the previous series and shows the existing "chart could not load" notice (same path as a JSON error today); one retry with the JSON shape. |
| Soak finds a new WebView2 host | Script exits 1; BL-437 doc gets the host; allow list is edited by hand with a dated line. |

## 4. Interfaces other areas rely on (exact names)

- `tools/desktop/FootprintLib.ps1` functions `Get-ProcessTreeSample`, `Measure-Idle`, `Get-ExeSizes`, `Write-FootprintJson`; file `tools/desktop/budgets.json` (keys 3.7); `footprint.json` shape 3.7. Area 01 BL-300 and area 08 read them.
- Go: `internal/power` (`New`, `Config`, `Policy.Touch`, `Policy.Begin`, `Policy.Run`, `Mode`), handler option `server.WithPower(*power.Policy)`, `(*Presence).Reset()` in `updater_auto.go`, routes `POST /api/desktop/window` and `PUT /api/desktop/ui-state` (role `ui`, token; body `{"visible":bool}` and `{"v":1,"case":"","view":"","zoom":{"from":"","to":""},"scroll":{},"dirty":false}`), `GET /api/desktop/state` gains `uiState` (the saved blob) next to the fields area 01 defines. Area 01 must add `uiState` to its DTO; this area adds the file `desktop_window.go` and no other change to that route.
- `cmd/audit-explorer/memtune.go` `tuneMemory(totalRAM uint64) (limit int64, gcPercent int)`. Area 03 (MCP) and area 05 (CLI) call it in their run branches only through `main.go`; they do not set GC values themselves.
- Rust: `desktop/src-tauri/src/idle.rs` (`Machine`, `Host`, `State`, `Event`), `power.rs` (`set_eco`), `lockdown::network_args()`, `lockdown::browser_args()`, `lockdown::network()`. `config::GRACE_SECS` is area 01's constant.
- Web: `web/src/lib/visibility.ts` (`isHidden`, `onVisible`), `web/src/lib/seriesFetch.ts` (`createSeriesLoader`, `fetchSeries`), `web/src/lib/chartPerf.ts` (`symbolOption`). The host adapter (area 01/UI `host.ts`) must expose `invokeBinary(name, args): Promise<ArrayBuffer>`.
- Series wire: request fields `width`, `shape`, header `Accept: application/vnd.unravis.series+f64`; response layout 3.6. Area 08 golden fixtures use this layout.
- Setup (area 06): option `strictNetwork` -> `internal/setup/firewall_windows.go` `AddStrictRule(runtimeDir string) error`, `RemoveStrictRule() error`, `--strict-network --dry-run`.
- CI: jobs `desktop-footprint` (ci.yml), step `F1` in `Invoke-VerifyRelease.ps1`; scripts return exit 0 or 1 and write `footprint.json` as an artifact.

## 5. Build notes per BL item

Order inside the area: BL-420 and BL-421 (start now, parallel) -> BL-424 (audit, Go-only, can start now) -> BL-423 -> BL-425 -> BL-429 (web, browser first) -> BL-430 -> BL-431 (after 01 BL-314 for the desktop transport; the browser path ships earlier) -> BL-426 -> BL-427 -> BL-428 -> BL-433 -> BL-434 -> BL-435 -> BL-432 (joined to 01 BL-313) -> BL-436 -> BL-437. BL-422 waits for 01 BL-305.

### BL-420 Footprint library and idle harness (S, P0)
- Add `tools/desktop/FootprintLib.ps1`, `Measure-Footprint.ps1 [-InstallDir] [-Runs 3] [-Seconds 60] [-CaseId] [-OutDir]`, `tools/desktop/Test-FootprintLib.ps1` (self test with a fake `ping` process tree).
- Tests: `Test-FootprintLib.ps1` asserts JSON parses, `idle.pwsMB` is a number, summing covers a child process, WebView2 filter ignores a process whose command line lacks the app dir.
- Acceptance (runnable):
  - `pwsh -File tools/desktop/Test-FootprintLib.ps1` exit 0 on windows-2022 and with `powershell.exe` 5.1 (second step). (CI)
  - `powershell -File tools/desktop/Measure-Footprint.ps1 -Runs 3` on nachi's PC with the installed app and a real case open writes `docs/desktop/baseline-<tag>.json`; file has hardware, both memory numbers, rollback tag. (live PC)
  - Same on Server 2016 with the Blackbox service. (live PC, needs box)

### BL-421 Go build flags and UPX guard (S, P0)
- Change `build.ps1`: add `-trimpath` and `-s -w -buildid=` to the app, keeper, setup and Lite commands (lines 145, 155, 177, 179). Keep `-X` and `-H=windowsgui`. Check `release.yml` calls `build.ps1` (it does; no separate go build there).
- Add `tools/release/Test-NoUpx.ps1 -Path <dir>`: for each exe reads PE section names, fails on `UPX0`, `UPX1`, `UPX!`. Add fixture `testdata/upx/fake-upx.exe` (a small PE with a `UPX0` section name written by `tools/release/New-UpxFixture.ps1`, no real UPX needed). Run it in `ci.yml` after build and in `Release.ps1` preflight.
- Tests: `cmd/audit-explorer/version_test.go` extended: `TestVersionFromBuildInfoWithStrip` builds `go build -ldflags "-s -w"` in a temp dir via `exec` (skipped under `-short`) and runs `--version`. `Test-NoUpx.Tests.ps1`: fixture fails, normal exe passes.
- Acceptance:
  - `Test-NoUpx.ps1` fails on fixture, passes on built exes; sizes before and after recorded in the PR. (CI)
  - Stripped exe: `unravis.exe --version` and `--check-update` run. (CI)
  - Installed stripped build starts on nachi's PC and the update bar shows the version. (live PC)

### BL-422 Sidecar build tag (S, P2; waits for 01 BL-305)
- If layout A (D5 default): do only `tools/desktop/Test-ExeSize.ps1` (records `unravis.exe` size with and without the embed using a temp build with `-tags tauri_sidecar`; no shipped change) and file `assets_sidecar.go`/`assets.go` build tags so both compile.
- If layout B: `build.ps1` builds `unravis-core.exe` with `-tags tauri_sidecar`; the shell bundles the UI (spec 01 BL-317).
- Tests: `go build ./...` and `go build -tags tauri_sidecar ./...` both pass; `TestAssetsEmptyWithSidecarTag` (build-tagged test) checks `Assets()` has no `index.html`.
- Acceptance: both builds compile (CI); size difference recorded (CI); desktop build loads the real case UI with no asset 404 in `unravis.exe`'s access log (live PC, only layout B).

### BL-423 Go memory tuning (M, P1)
- Files: `cmd/audit-explorer/memtune.go` (+ `_windows`, `_other`), `main.go` (two call sites), `internal/casestore/sqlite/sqlite.go` (cache, idle, `ConnMaxIdleTime`), `docs/desktop/bench-bl423.md`.
- Step 1: print driver engine (`go list -m -json github.com/ncruces/go-sqlite3`) into the bench doc. Step 2: cache sweep (3.2).
- Tests: `TestTuneMemoryTable` (RAM 1, 2, 4, 8, 32 GiB gives 256, 256, 256, 512, 512 MiB: floor 256 MiB, cap 512 MiB), `TestTuneMemoryRespectsGOGCEnv`, `TestPoolNeverExceedsFour` (real SQLite file under `internal/casestore/sqlite`, 20 goroutines, `db.Stats().OpenConnections <= 4`), `TestIdleConnectionsClose` (30 s replaced by injected `ConnMaxIdleTime` of 200 ms in test), `internal/perfbench` import benchmark compare as in 3.2.
- Acceptance:
  - Pool and tune tests pass. (CI)
  - Largest real case plus 4 charts: summed PWS <= 350 MB (fail > 500); after 30 s idle Go PWS lower than at peak; numbers in `bench-bl423.md`. (live PC)
  - 2 GB VM run: same case or the largest that fits, no GC thrash (Go CPU time during an idle minute < 120 ms). (live PC, small VM)

### BL-424 Idle audit and leak test (M, P1)
- Files: `internal/server/idle_audit_test.go`, `ticker_scan_test.go`, `cmd/audit-explorer/idle_leak_test.go`; `// idle-ok:` comments at `import_jobs.go` and `casestore/sqlite/case.go`. CI job sets `GOEXPERIMENT=goroutineleakprofile` when `go env` accepts it.
- Acceptance:
  - Ticker scan test passes; adding a `time.NewTicker(10*time.Millisecond)` in a scratch file makes it fail (test includes that negative case on a temp dir). (CI)
  - Leak test passes over 5 case cycles and 5 tray cycles. (CI)
  - 60 s idle on nachi's PC with the real app: summed CPU time < 30 ms (fail > 120 ms), via `Measure-Footprint.ps1`. (live PC)

### BL-425 Go idle power policy (M, P1)
- Files: `internal/power/*` (3.4), `cmd/audit-explorer/power.go`, `routes.go` hook, `desktop_window.go`, `updater_auto.go` (`Presence.Reset`).
- Tests: `TestPolicyThrottlesAfterGrace`, `TestTouchClearsIdle`, `TestInFlightBlocksIdle` (a long GET), `TestInUseBlocksIdle`, `TestNoTimerWhileIdle` (fake clock counts `After` calls), `TestFallbackOrder` (injected errors: `ERROR_INVALID_PARAMETER` then success; any other error then `SetPriorityClass`), `TestMcpRequestKeepsNormal`, `TestWindowHiddenRouteResetsPresence`, `GOOS=windows go vet ./internal/power` in CI, `TestSetProcessInformationReal` (windows-2022 runner).
- Acceptance:
  - Unit tests and windows vet pass. (CI)
  - Process Explorer shows Efficiency Mode on the Go process 30 s after the window is destroyed (Win 11); clears on the next request. First request after idle answers in <= 200 ms (timed by the script `Test-PowerWake.ps1`, 20 tries, p95 written down). (live PC)
  - Server 2016: priority class below normal. (live PC, needs box)

### BL-426 Rust host idle (S, P2)
- Files: `power.rs`, `main.rs` runtime build (D19), cargo feature `wakeups`.
- Tests: `eco_decision_table` (fake OS), `runtime_has_two_workers`, `no_interval_timers` (source grep for `interval(`, `Interval` in `src/`).
- Acceptance: tests pass (CI); tray-idle 60 s: shell CPU time and Efficiency Mode icon (live PC).

### BL-427 Tray-hide grace, suspend, destroy, recreate (M, P2)
- Files: `idle.rs`, `window.rs` (01) rewired, `desktop_window.go`, UI posting of state (`web/src/lib/uiState.ts`, new, 40 lines).
- Tests (Rust, mock Host): `hidden_destroys_after_grace`, `busy_blocks_destroy_and_rechecks_15s`, `dirty_form_blocks_destroy`, `show_while_suspended_resumes`, `show_after_destroy_recreates_with_lockdown`, `three_recreate_failures_stop_destroying`, `quit_in_any_state`. Go: `TestUIStateRoundTrip`, `TestUIStateRejectsLarge` (limit 8 KiB), `TestUIStateNoCaseContent` (reject unknown keys).
- Acceptance:
  - Rust and Go tests pass. (CI)
  - Real case open, close window, after 70 s no `msedgewebview2.exe` with the app dir in its command line; tray-idle summed PWS <= 35 MB (fail > 55). (live PC)
  - Tray reopen to usable UI <= 800 ms (fail > 1.5 s), 20 reopens, same case open, zoom and tab restored; 5 cycles leave goroutines flat (BL-424 test run against the real app via `/api/desktop/state` goroutine count field `goroutines`, added to that route's DTO in debug builds only). (live PC)
  - With nachi's AV on: no lock error in 20 reopens. (live PC)

### BL-428 WebView2 suspend and memory-target spike (S, P2)
- Per 3.5. Code under `desktop/src-tauri/src/spike_wv2.rs`, cargo feature `spike`, not built in release. Output `docs/desktop/spike-bl428.md` with sections "TrySuspend", "MemoryUsageTargetLevel", "Decision:".
- Acceptance: `cargo build --features spike` on windows-2022 (CI); doc has before/after PWS and `isSuccessful` log from the real app on Runtime >= 157 (live PC); `Test-DocsHave.ps1 spike-bl428.md "Decision:"` (CI).

### BL-429 Web idle behavior (M, parallel then P2)
- Files per 3.6. Tests: `web/tests/visibility.test.cjs` (fake document, hidden: no timer fires; shown: one refresh), `web/tests/chartPerf.test.cjs`, Playwright `tools/e2e/specs/idle-hidden.spec.ts` (page with `Page.setWebLifecycleState`/emulated `visibilityState=hidden`, count `/api/` requests over 30 s via `page.on('request')`; expect 0).
- Acceptance:
  - Vitest-free: `npm --prefix web test` includes both new tests. (CI)
  - Playwright spec passes against the real Go test server with a real Blackbox recording (`testdata/` sample used by `bb-charts.spec.ts`). (CI)
  - Minimized real app 5 min: Go access log has no request lines (the app log `applog` level info; script counts). (live PC)

### BL-430 Width-aware buckets with first/last (S-M, P3; Go part now)
- Files: `blackbox_series.go`, `blackbox_series_test.go`.
- Tests: `TestSeriesWidthSetsPoints`, `TestSeriesM4HasFirstLast`, `TestSeriesM4KeepsSpike` (golden: spike in a real sample file at widths 600, 1500 and 3000; widths above 600 clamp to 600 buckets, spike is in the bucket max), `TestSeriesDefaultShapeUnchanged` (golden of today's JSON), property test `TestSeriesBucketInvariants` (random data: points <= `bbSeriesMaxPoints`, per-bucket lo <= first,last <= hi, global min and max present).
- Acceptance:
  - Tests pass. (CI)
  - 1M+ row case: series answer time p95 recorded (`bench-bl430.md`), at or under 250 ms for the window query (budget row `seriesQueryP95Ms` added at the P2 reset; until then recorded only). (live PC)

### BL-431 Chart client binary fetch and debounce (M, P3)
- Files per 3.6; Go: binary encoder in `blackbox_series_binary.go` + test `TestBinaryLayoutRoundTrip` (decode in test with `encoding/binary`, compare to JSON), `TestBinaryOnlyWithAccept`.
- Tests: `web/tests/seriesFetch.test.cjs` (stale response dropped, abort called, debounce 150 ms with fake timers, header mismatch throws then JSON retry); `tools/e2e/specs/series-zoom.spec.ts` (real Go server, real recording: zoom three times fast, only the last range is painted).
- Acceptance: tests pass (CI); Lite serves the same chart from a real recording unchanged (`cmd/unravis-lite` test run); pan/zoom p95 <= 100 ms (fail > 250 ms) on nachi's real case in the desktop app, from `window.__unravisPerf` (live PC).

### BL-432 Bulk transport benchmark (S, P3) merged into spec 01 BL-313
- 01 builds script and `bench-bl313.md`. Area 07 adds: the pass rule of D3, payload = a real binary series answer from BL-431 (not a fake blob), and the ADR `docs/adr/NNNN-chart-transport.md` text. Acceptance: ADR exists with the result line; numbers from nachi's PC with a real M4 payload (live PC).

### BL-433 Network-silence settings (S, P2)
- Files: `lockdown.rs` (`network_args`, `network`, `browser_args`), `deny.toml` bans, tests `network_args_contain_each_flag`, `reputation_set_before_navigation` (mock order of calls), `banned_plugins_absent` (`cargo deny check bans`).
- Acceptance: tests pass (CI); item passes only after BL-434 shows no new host (live PC).

### BL-434 Network soak (M, P3; re-run P4)
- Files: `Invoke-NetSoak.ps1`, `netsoak-allow.txt`, `Test-NetSoakScript.ps1` (parses with PSScriptAnalyzer; runs the filter logic on a recorded `sample-events.json`).
- Acceptance: script lints and its filter logic passes on the sample (CI); on windows-2022 elevated run with the demo case fails the job on any Go or Rust non-loopback packet (CI, runner traffic allow-listed); nachi's PC with a real case: Go 0, Rust 0, WebView2 hosts listed; updater on: feed host only (elevated/UAC).

### BL-435 CI footprint gate (M, P3; extended P4)
- Files: `Test-Footprint.ps1`, `New-SeedCase.ps1`, `footprint-baseline.json`, `ci.yml` job, `Invoke-VerifyRelease.ps1` step `F1`.
- Gate checks: sizes; start; tray-hide then 70 s wait then zero WebView2 children and no `unravis-sandbox.exe` (D18); 60 s idle CPU; leak count from `/api/desktop/state`; 1M-event seed case series p95 through `seriesFetch` Playwright; compare with `footprint-baseline.json` (15%); every breach listed in the job summary.
- Tests: `Test-FootprintGate.ps1`: feed a bloated `footprint.json` (extra 20 MB, a goroutine count of 80) and a normal one; first fails, second passes.
- Acceptance: gate self test (CI); first green run on a real rc tag, numbers compared with BL-420/BL-427 live numbers (live PC).

### BL-436 Hardware power sweep (S, P4)
- `Invoke-PowerSweep.ps1` (wpr, powercfg), `docs/desktop/power-sweep-template.md`. Pass: no Unravis entry under timer resolution or CPU warnings in `powercfg /energy` output (script greps `energy-report.html` for the exe names); Efficiency Mode seen; handle count flat. Acceptance as groom; script shape in CI (`-DryRun`), full run (elevated/UAC).

### BL-437 OneAuth doc and firewall rule (S, P4)
- Files: `docs/user/webview2-network.md`, `internal/setup/firewall_windows.go` + `_other.go`, `internal/setup` test `TestStrictRuleCommandText`, `TestStrictRuleSkippedWhenOff`, setup `--strict-network --dry-run`. Area 06 wires the option.
- Acceptance: doc exists and links `Invoke-NetSoak.ps1` (CI doc check); dry run prints exact command (CI); on nachi's PC rule on: soak shows zero WebView2 outbound packets and UI works with a real case; rule off removes it (elevated/UAC); newest Runtime re-check recorded (live PC).

## 6. Security and footprint checks (this area)

- `internal/power` calls only `SetProcessInformation`, `SetPriorityClass`; no new privileges, no handle to other processes. Test `TestPowerOnlyCurrentProcess` (grep: `OpenProcess` absent in package).
- `PUT /api/desktop/ui-state`: role `ui` only, token, max 8 KiB, strict JSON (unknown keys rejected), stored in memory only (never on disk), cleared on exit. No case content, no identifiers: fields are ids and view names. Test in BL-427.
- `POST /api/desktop/window` role `ui` only; returns 404 on loopback and `ag` pipe (same test helper as spec 01 `TestRoleOnlyUI`).
- Binary series route adds no new path: same route, same token and role checks; `Accept` only changes encoding. Response length bounded by `maxResponseBytes`.
- Firewall rule: program path is read from the registry and checked to start with `C:\Program Files (x86)\Microsoft\EdgeWebView\Application\`; otherwise setup refuses. Name fixed; no user input in the command line (argv list, not a string through `cmd`).
- `-buildid=` and `-s -w` do not change the helper SHA pin check (`PinnedHelperSHA256`); `build.ps1` already compares the pin after the build; BL-421 keeps that step and its failure message.
- Footprint scripts store no case data: `footprint.json` has process names, numbers, row count only. The seed case is generated; no real case is committed (D13).
- Network: after BL-433/434, Go and Rust 0 non-loopback packets is a CI fail; WebView2 hosts are listed, not hidden.
- Footprint checks that must stay true (in `Test-Footprint.ps1`): installer <= 25 MB target/35 fail, tray-idle <= 35/55 MB, idle CPU, no sub-second ticker, no `unravis-sandbox.exe` at idle, Rust LOC gate is area 01's (`Count-Rust.ps1`).

## 7. Open risks left and mitigations

| Risk | Mitigation |
|---|---|
| `TrySuspend` fails on Runtime 157 with the custom scheme (tauri#4851) | Not needed for the budget: destroy at 60 s frees the processes. The 60 s tray-idle number does not depend on suspend. BL-428 records the result. |
| Soft memory limit causes thrash on a small PC | Floor equals today's value (256 MiB), cap is a soft limit only; the 2 GB VM run is a named live line; if it fails, set `gcPercent` back to 100 for RAM < 4 GiB in `tuneMemory` (one table entry). |
| Smaller SQLite cache slows big imports | Import connection keeps the old size if the perfbench check fails (3.2). |
| Throttled Go process adds wake latency | Measured by `Test-PowerWake.ps1`; if p95 > 200 ms, remove `IGNORE_TIMER_RESOLUTION` first, then drop memory priority, keeping `EXECUTION_SPEED` only. Each step is a flag in `Config`. |
| Shared runner numbers flap | Gate compares only against the committed baseline with 15% and uses the median of 3 runs; a flap is fixed by re-running once, not by raising limits; limits change only at the two reset ADRs (D2). |
| WebView2 flags removed or unsupported by Microsoft | Soak removes flags that show no effect; the firewall rule (BL-437) is the fallback for strict users. |
| OneAuth traffic fires on local accounts too | Listed in the doc as found by the soak; no code claim beyond the list. |
| No Server 2016 box, no small-RAM PC, no real big case | D12 and D13: logic has unit tests, live lines stay unticked and are printed as SKIP by the scripts; the BACKLOG row text says which line is unproven. |
| Same-user pipe and `ui-state` blob spoofing | Pipe DACL is the boundary (spec 02); the blob only picks a view name and ids, validated against known values on read. |
| Spec 01 BL-308 and 07 BL-427 touch `window.rs` | BL-427 starts only after BL-308 merges and keeps its test names; both PRs reference each other. |
| Two scripts for chart transport benchmark | Merged into BL-313 (D3); no BL-432 script is written by area 07. |
