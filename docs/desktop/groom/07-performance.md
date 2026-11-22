# Groom 07: Performance and footprint (BL-420 to BL-439)

Source: research report 08 (performance footprint, 2026-11-22) and `docs/desktop/DECISIONS-draft.md` (plan v1). Phases P0-P5 and Parallel A/B/C are from that file. Budgets are from DECISIONS section 9. Nothing here is built. No item is ticked until it works on the real data path (CLAUDE.md owner rule).

Notes on the report: most numbers are `[INFERRED]`, not measured on Unravis. Only the empty-window numbers (108 MB PWS for Tauri 2 + WebView2, 1.05 MB NSIS) are measured, by third parties. Every budget below is a target to be proven on nachi's PC, not a fact.

Acceptance tags: **CI** = runs on a CI runner; **live PC (nachi)** = run on nachi's real Windows PC with real cases and real Blackbox recordings; **elevated/UAC** = needs an admin prompt.

Auto-updater (BL-149 to BL-197) is not duplicated. These items only touch its timers and its CI file (`verify-release.yml`, BL-153).

---

## BL-420 Baseline footprint harness and today's numbers
- **Outcome:** One script measures today's Unravis (browser mode, Go only) so every later item has a before/after.
- **Scope (in):** `scripts/bench-footprint.ps1`: installer and exe sizes; start, wait 20 s, walk the process tree, sample summed Private Working Set, Private Bytes, handles, threads, CPU time over a 60 s idle soak; JSON output. Run on the current release. Record hardware and numbers in `docs/desktop/` and name the rollback release.
- **Out of scope:** Tauri, WebView2, CI gating (BL-435), budgets enforcement.
- **Depends on:** P0.
- **Risks / unknowns:** Report 08 gives no controlled idle CPU number for any framework ("no data found"), so we have no outside reference. Task Manager "Memory" differs from Private Working Set by more than 3x (report, "why benchmarks disagree"); the script must report PWS and Private Bytes both.
- **Acceptance:**
  - Script runs on Windows 11 and Server 2016 with PowerShell 5.1 and prints valid JSON. (CI, windows runner)
  - Run against the installed app with a real case open: numbers saved in repo, hardware named. (live PC (nachi))
  - Same run on a Server 2016 box with the real Blackbox service. (live PC (nachi); needs a Server 2016 machine, owner to say if one exists)
- **Size:** S. **Phase:** P0.
- **Needs owner action?** No.

## BL-421 Go build flags and no-UPX guard
- **Outcome:** Release Go binaries are stripped and reproducible-ish; a CI check fails if any shipped exe is UPX-packed.
- **Scope (in):** Release builds use `-trimpath -ldflags="-s -w -buildid="`, `CGO_ENABLED=0`. CI step scans every exe in the release set for UPX section names and fails. Record size before/after.
- **Out of scope:** Splitting `go:embed` (BL-422); Authenticode (distribution area).
- **Depends on:** none (can start now); P0 for the size baseline.
- **Risks / unknowns:** Report claims `-s -w` keeps `debug.ReadBuildInfo()`; verify it still works for the version string. `-buildid=` may interact with the reproducible-build item BL-195. Whether the current release already uses these flags is not known to me; check `release.yml`.
- **Acceptance:**
  - Built exe has no symbol/DWARF; `version` command and `--check-update` still work. (CI)
  - A fixture UPX-packed exe makes the guard fail; a normal exe passes. (CI)
  - Installed stripped build starts on nachi's PC and the update bar version shows. (live PC (nachi))
- **Size:** S. **Phase:** P0.
- **Needs owner action?** No.

## BL-422 Sidecar build without embedded UI (`tauri_sidecar` tag)
- **Outcome:** In the desktop build, the Go sidecar does not `go:embed` the Svelte bundle that Tauri already bundles; standalone and Lite builds are unchanged.
- **Scope (in):** Build tag (report suggests `tauri_sidecar`) that excludes the UI embed; the desktop packaging uses it; size check shows the saving. Optional pre-compression (Brotli/Gzip) of embedded assets for standalone/Lite builds.
- **Out of scope:** Changing browser mode default; the bundled UI itself (shell area).
- **Depends on:** P2 (shell preview exists), BL-421.
- **Risks / unknowns:** Report marks this `[INFERRED]`. DECISIONS open item: whether the sidecar is the same `unravis.exe` or a split `unravis-core.exe` is settled in the P1 spike. If it stays the same exe (browser fallback needs the embedded UI), the tag cannot remove the embed and this item shrinks or is dropped. Flag for spec.
- **Acceptance:**
  - Both builds compile; browser build still serves the UI; sidecar build size is smaller by about the bundle size. (CI)
  - Desktop build opens a real case with the bundled UI and no 404 for assets. (live PC (nachi))
- **Size:** S. **Phase:** P2.
- **Needs owner action?** No.

## BL-423 Go memory tuning with a bounded SQLite pool
- **Outcome:** Go backend RAM stays inside budget on real big cases.
- **Scope (in):** `GOGC=50` set in code; soft `debug.SetMemoryLimit` (report example: min of 25% of system RAM and 512 MiB, `[INFERRED]`; never `GOGC=off`); `db.SetMaxOpenConns` (1 writer, 2-4 WAL readers); bounded `PRAGMA cache_size` (report example `-8000`); `ReleaseMemory()` on case close; `debug.FreeOSMemory()` once on case close and once 30 s after tray-idle.
- **Out of scope:** Idle detection itself (BL-425 owns the idle signal); changing the SQLite driver.
- **Depends on:** P1 (idle signal comes with the attachable backend); BL-420 for the baseline.
- **Risks / unknowns:** Report 08 open item 5: `ncruces/go-sqlite3` v0.33.0+ (`wasm2go`) memory and throughput at 1M-10M rows is unverified. Each connection has its own linear memory, counted in the Go heap, so pool caps matter. I do not know which driver version the repo uses today; check `go.mod` first (the DECISIONS text still mentions wazero JIT). Soft limit values are guesses. A soft limit can cause GC thrash on a small PC.
- **Acceptance:**
  - Unit/integration test with a real SQLite file: pool never exceeds the cap under concurrent requests. (CI)
  - Open the largest real case nachi has plus 4 charts: summed PWS at or under 350 MB (fail above 500 MB); after close and 30 s idle, Go PWS drops. (live PC (nachi))
  - Same on a small-RAM PC or VM to check there is no GC thrash. (live PC (nachi); owner to say what small machine exists)
- **Size:** M. **Phase:** P1.
- **Needs owner action?** No.

## BL-424 Go idle wakeup audit and goroutine-leak test
- **Outcome:** An idle Go backend does almost no wakeups and leaks no goroutines.
- **Scope (in):** Find every `time.Ticker` and short poll in the Go app; replace with event-driven channels; the update check stays one long timer (the auto-updater already uses 30 min; keep it that way). Add a test that opens/closes 5 cases and 5 tray cycles and checks `/debug/pprof/goroutineleak` is empty (Go 1.27) and idle goroutines stay under about 30. Optional `GODEBUG=updatemaxprocs=0`.
- **Out of scope:** Page-presence polling design owned by BL-168 (silent update); only audit it.
- **Depends on:** P1.
- **Risks / unknowns:** Report says any sub-second `time.Ticker` arms a high-res kernel timer on Windows (`[SOURCE]` plus `[INFERRED]`). Go version: repo may not be on 1.27 yet (report assumes 1.27 with GA `goroutineleak`); confirm in `go.mod`. If not on 1.27, the test needs a different leak check. Existing Blackbox recorder is a separate service and out of tray scope.
- **Acceptance:**
  - Test list of all tickers under 1 s is empty or each is justified in a comment. (CI)
  - Leak test passes over the cycles. (CI, Go version permitting)
  - 60 s idle soak on nachi's PC with the real app: CPU time under 30 ms (fail above 120 ms). (live PC (nachi))
- **Size:** M. **Phase:** P1.
- **Needs owner action?** No.

## BL-425 Go sidecar idle power policy (EcoQoS and fallback)
- **Outcome:** After 30 s with no UI or MCP request the Go backend asks Windows for low-power mode; it returns to normal on the next request.
- **Scope (in):** `SetProcessInformation(ProcessPowerThrottling)` with `EXECUTION_SPEED` and `IGNORE_TIMER_RESOLUTION`; `ProcessMemoryPriority` very low; retry with `EXECUTION_SPEED` only on `ERROR_INVALID_PARAMETER` (Win 10); `BELOW_NORMAL_PRIORITY_CLASS` on Server 2016. Clear on request arrival. Reuse the same "busy" signal the silent updater uses (BL-171) so there is one idea of "in use".
- **Out of scope:** Rust host throttling (BL-426); throttling WebView2 children (report says do not use `IDLE_PRIORITY_CLASS` on them); never call `timeBeginPeriod`.
- **Depends on:** P1, BL-423, BL-171 (shared busy signal).
- **Risks / unknowns:** Wake latency of the first request after throttle is not measured (spec: budget "tray reopen <= 800 ms"). On Win 10 and Server 2019, EcoQoS acts as LowQoS on battery only, so the gain there is small. Whether MCP stdio clients make the backend look busy is not known. The code sample in the report is a sketch, not tested.
- **Acceptance:**
  - Unit test with a fake clock: throttle on after 30 s, cleared on request; syscall wrapper compiles for windows (`GOOS=windows vet`). (CI)
  - Process Explorer shows Efficiency Mode icon on the Go process after 30 s idle on Win 11 and it clears on a request. (live PC (nachi))
  - Server 2016 fallback path sets below-normal priority. (live PC (nachi); needs a Server 2016 box)
- **Size:** M. **Phase:** P1.
- **Needs owner action?** No.

## BL-426 Rust host idle: EcoQoS, small runtime, wakeup metrics
- **Outcome:** The thin Rust host costs near zero when only the tray is alive.
- **Scope (in):** EcoQoS + ignore-timer on tray-idle, cleared on window show; `tokio` `worker_threads(2)`; a debug-only wakeup count via `RuntimeMetrics::worker_park_unpark_count`; no polling timers in the host.
- **Out of scope:** Go side (BL-425); tray and window code (BL-427).
- **Depends on:** P2.
- **Risks / unknowns:** Report 08 assumes a tokio runtime; the Tauri host may not need one beyond what Tauri starts. DECISIONS target of under 1,500 LOC for the host. Metrics API may need an unstable tokio flag (not stated in the report; check).
- **Acceptance:**
  - Rust unit test on the policy state machine (MockRuntime). (CI)
  - Tray-idle 60 s soak: Rust host CPU time under budget and Efficiency Mode shown in Process Explorer. (live PC (nachi))
- **Size:** S. **Phase:** P2.
- **Needs owner action?** No.

## BL-427 Tray-hide: grace, suspend, destroy, recreate the window
- **Outcome:** Closing to tray frees all WebView2 processes after a 60 s grace; tray click brings the window back within budget.
- **Scope (in):** Close hides window; `TrySuspend` during the grace; at 60 s call `destroy()`; keep the app alive (`prevent_exit` when exit has no code); tray click recreates the window if missing; an explicit reset of throttling on show. Test that `msedgewebview2.exe` child count is 0 after teardown.
- **Out of scope:** Tray menu content and single-instance logic (01-shell); MemoryUsageTargetLevel (BL-428).
- **Depends on:** P2; BL-426 for the throttle clear.
- **Risks / unknowns:** Report 08 risk 3: `TrySuspend` with the `tauri.localhost` custom scheme had a callback bug on Runtime 129 (`#4851`); not verified on Runtime 157. `wry#1795` teardown crash fixed in wry 0.57.1 (needs Tauri >= 2.12.x). DECISIONS open items: Alt+Tab focus bug tauri#15624; AV/EDR locking the WebView2 data folder (could block teardown or recreate). Tauri `background_throttling` is macOS only. Unsaved UI state: the Go backend holds all case state `[INFERRED]`; UI-only state (filters, scroll) will be lost on destroy, spec must decide what to restore.
- **Acceptance:**
  - Rust test of the state machine (visible, hidden, suspended, destroyed, recreate) with the mock runtime. (CI)
  - On Windows, with a real case open: hide, wait 70 s, process list shows zero `msedgewebview2.exe`; tray-idle summed PWS at or under 35 MB (fail above 55 MB). (live PC (nachi))
  - Tray reopen to a usable UI at or under 800 ms (fail above 1.5 s); same case still open. Repeat 5 cycles with no leaked goroutines (BL-424 test). (live PC (nachi))
  - With the user's real AV on: no lock error on recreate. (live PC (nachi))
- **Size:** M. **Phase:** P2.
- **Needs owner action?** No.

## BL-428 WebView2 memory-target and suspend spike on Runtime 157
- **Outcome:** A written, measured answer to "does `TrySuspend` work and what does `MemoryUsageTargetLevel` save" for Unravis.
- **Scope (in):** A small spike in the P2 shell: call `TrySuspend` via `with_webview` and `webview2-com`; log `isSuccessful`; compare RAM with `MemoryUsageTargetLevel = LOW` on a visible-but-idle window; restore `NORMAL` on show. Decide use or skip. Write result to `docs/desktop/`.
- **Out of scope:** Shipping it as a feature unless the result is good.
- **Depends on:** P2, BL-427.
- **Risks / unknowns:** Report gives no MB figure for either API ("no data found"). `TrySuspend` fails if the page has open IndexedDB transactions, audio, or DevTools attached; Unravis has no stated use of these. LOW has no auto-restore: forgetting `NORMAL` leaves the UI slow. Tauri does not wrap `set_memory_usage_level`; needs Windows-only unsafe-ish interop (security area may object to extra Rust surface).
- **Acceptance:**
  - Windows-only Rust code compiles on `windows-2025`. (CI)
  - On Runtime 157+, `TrySuspendCompleted` returns success on the real app with a real case, and numbers (before/after PWS) are saved. (live PC (nachi))
- **Size:** S. **Phase:** P2.
- **Needs owner action?** No.

## BL-429 Web UI idle behaviour (visibility pause, ECharts settings)
- **Outcome:** A hidden or minimized window does no polling and no chart animation; large charts use the cheap Canvas settings.
- **Scope (in):** `visibilitychange` handler that pauses all polling, SSE and WebSocket streams when `document.hidden`; resume on show. ECharts: `renderer: 'canvas'`, `animation: false` (or `animationThreshold: 2000`), `showSymbol: false`, `large`/`progressive` options for big series, typed-array dimension hints. Applies to browser mode too.
- **Out of scope:** Server-side downsampling (BL-430); switching chart library.
- **Depends on:** none for the browser path; P2 to prove on the desktop window.
- **Risks / unknowns:** Report says Chromium exempts pages with active WebSockets/IndexedDB from timer throttling, so we must pause ourselves. Interaction with the silent updater's page-presence ping (BL-168): a hidden window must still count correctly as "not in use". ECharts `appendData` has known bugs (report); do not use it. I did not read the current UI code; where polls live is not known.
- **Acceptance:**
  - Playwright on the real built UI with the Go test server: hide the page (emulate visibility), count network requests over 30 s: zero. (CI)
  - Chart options test: 4 charts on a real Blackbox recording have `animation:false`, `showSymbol:false`. (CI)
  - Minimized real app on nachi's PC for 5 min: no requests in the Go access log. (live PC (nachi))
- **Size:** M. **Phase:** Parallel (browser-first), proven in P2.
- **Needs owner action?** No.

## BL-430 M4 downsampling in Go for chart series
- **Outcome:** The series API returns at most 4 points per pixel column and keeps every spike.
- **Scope (in):** M4 (first, min, max, last per bucket, time order, dedup) in Go; request carries window start, end and pixel width; response cap of `4 * width` points; raw data stays in SQLite; works on the existing Blackbox chart series and on API/SQL timeline data where it is a time series.
- **Out of scope:** Client rendering (BL-431); new collectors (backlog deferred rule); exact percentiles (stay on raw).
- **Depends on:** P1 (Go side only); can start before the shell.
- **Risks / unknowns:** Report 08 `[INFERRED]`: "~40 LOC" and pixel-identical claim come from the 2014 VLDB paper, not from Unravis data. Multi-series with different time grids: M4 buckets per series, so x values may not align in tooltips (report cites this issue for ECharts sampling); spec must pick shared buckets. Gaps and nulls handling unknown. Report 10M-row scan time is untested (open item 5).
- **Acceptance:**
  - Property tests: output length at most `4 * width`; global min and max of every bucket present; compared with brute-force on random data. (CI)
  - Golden test on a real Blackbox recording with a known spike: spike present after downsampling at widths 600, 1500, 3000. (CI, using a recorded sample file; live PC (nachi) for the full-size recording)
  - 1M+ row real case: M4 query time recorded, p95 under budget (see BL-431). (live PC (nachi))
- **Size:** M. **Phase:** P3 (Go part may land early).
- **Needs owner action?** No.

## BL-431 Chart client: binary series fetch and debounced zoom/pan
- **Outcome:** Charts fetch packed `Float64Array` data for the visible window and re-query on zoom/pan after 150 ms.
- **Scope (in):** Binary (`application/octet-stream`) series response from Go next to the existing JSON one (JSON stays for browser/Lite until proven); client reads `arrayBuffer()` into `Float64Array`; debounced re-query; request cancellation of stale queries; p95 pan/zoom timing logged.
- **Out of scope:** The transport choice for desktop (BL-432 decides pipe vs loopback); new chart types.
- **Depends on:** BL-430; BL-429 for chart options; transport from BL-432 in P3.
- **Risks / unknowns:** Conflict on transport: report 08 says direct `127.0.0.1` fetch; DECISIONS says named pipe with no loopback port in desktop mode (see Conflicts). Report open item 4: no head-to-head benchmark exists; CORS and token handling between `tauri.localhost` and `127.0.0.1` untested. Lite viewer (`internal/lite`) must stay identical; do not break its route allowlist.
- **Acceptance:**
  - Playwright against the real Go server with a real recording: pan and zoom produce correct points; stale responses dropped. (CI)
  - Pan/zoom p95 at or under 100 ms (fail above 250 ms) on a 1M-10M row case, measured in the real app. (live PC (nachi))
  - Lite still serves the same chart from a real recording. (CI)
- **Size:** M. **Phase:** P3.
- **Needs owner action?** No.

## BL-432 Bulk chart transport benchmark (pipe vs IPC vs loopback)
- **Outcome:** A measured decision on how bulk chart bytes reach the WebView, replacing the two conflicting assumptions.
- **Scope (in):** Prototype in the P3 shell: 128 KB and 10 MB payloads over (a) Rust relay with `ipc::Response` from the named pipe, (b) custom URI scheme, (c) token-gated loopback fetch. Record median and p95 on Windows 11 and note CSP needs (`ipc: http://ipc.localhost`). Write the decision (and the security exception if loopback wins) into `docs/adr/`.
- **Out of scope:** Shipping the loser; the security review itself (02 area).
- **Depends on:** P2, P3 skeleton, BL-430.
- **Risks / unknowns:** Report: 10 MB on Windows WebView2 is about 200 ms (informal, one maintainer comment), custom scheme within 6% of `ipc::Response`, no streaming on WebView2 custom schemes. Plain `Vec<u8>` JSON-encodes (1.8 s stall); missing CSP entry silently falls back to `postMessage`. After M4 payloads are about 96 KB, so IPC may be fine and loopback not needed; the benchmark may show the question is moot.
- **Acceptance:**
  - Benchmark script is checked in and runs on `windows-2025` for relative numbers. (CI)
  - Real numbers with a real M4 payload from a real case on nachi's PC saved in the ADR. (live PC (nachi))
- **Size:** S. **Phase:** P3.
- **Needs owner action?** No.

## BL-433 WebView2 network-silence settings
- **Outcome:** WebView2 is set up so it makes no optional network calls that an app can turn off.
- **Scope (in):** `IsReputationCheckingRequired=FALSE` before first navigation, `IsCustomCrashReportingEnabled=TRUE`, `IsGeneralAutofillEnabled=FALSE`, spellcheck off, `additionalBrowserArgs` list from report (`--disable-background-networking`, `--disable-component-update`, `--disable-domain-reliability`, `--disable-sync`, `--no-pings`, host-resolver rules excluding localhost); CSP `connect-src` limited to the local origin; omit `tauri-plugin-updater`, `-http`, `-upload`, `-websocket`.
- **Out of scope:** The WebView2 hardening list that is security (DevTools off, extensions off, etc.; 02-security); the OneAuth leak (BL-437); `ExperimentationAndConfigurationServiceControl` policy (needs HKLM, only an optional installer toggle in 06-distribution).
- **Depends on:** P2.
- **Risks / unknowns:** Microsoft says browser flags are unsupported in production and may be removed (report). Settings must be applied to every WebView sharing the data folder. Report open item: these do NOT stop Runtime 155-157 OneAuth/WAM calls (`WebView2Feedback#5760`). The `--disable-features=msOneAuthWAM` flag worked on 155, failed on 157.
- **Acceptance:**
  - Rust test that the config object contains each setting; `cargo-deny` shows none of the banned plugins in the tree. (CI)
  - Soak result is BL-434; this item passes only when BL-434 shows no new host beyond the tracked `#5760` ones. (live PC (nachi))
- **Size:** S. **Phase:** P2.
- **Needs owner action?** No.

## BL-434 Network-silence soak (WFP 5157, DNS client log, pktmon)
- **Outcome:** A repeatable test proves zero non-loopback traffic from Go and Rust, and lists exactly which WebView2 hosts still appear.
- **Scope (in):** Script from the report: enable DNS-Client operational log and WFP connection audit, `pktmon` capture, launch with `--no-update-check`, open a real case, zoom charts, hide to tray, soak 120 s, quit; filter non-loopback; query 5157 and DNS events for `unravis*` and `msedgewebview2` PIDs. A second run with the updater enabled must show only the feed host. Output JSON for CI and a text report.
- **Out of scope:** Fixing `#5760` (Microsoft); firewall rules (BL-437).
- **Depends on:** P2, BL-433.
- **Risks / unknowns:** Needs admin to enable audit policy and run `pktmon`. A GitHub runner has its own background traffic, so the CI run can be noisy (report allows an explicit allowlist). The OTA flag name `--no-update-check` comes from the report; the current app has `--check-update` (BL-152), not that flag; spec must reconcile (add a test-only switch).
- **Acceptance:**
  - Script lints and runs on `windows-2025`, failing on any non-allowlisted non-loopback process traffic from Go or Rust. (CI, elevated on the runner)
  - Run on nachi's PC with a real case: 0 packets from Go and Rust; WebView2 hosts listed; with updater on, only the feed host. (elevated/UAC)
- **Size:** M. **Phase:** P3 (script), re-run in P4.
- **Needs owner action?** Yes, UAC prompt for the elevated capture on nachi's PC.

## BL-435 CI footprint gate in release verify
- **Outcome:** A release cannot ship if it breaks the section 9 budgets or regresses more than 15%.
- **Scope (in):** Add the harness (BL-420) to `verify-release.yml` on `windows-2025`: sizes, 20 s start, tray-hide then wait 70 s then check zero WebView2 children, 60 s idle soak, goroutine-leak check, seeded 1M-point SQLite case with Playwright pan/zoom p95; JSON artifacts; fail on budget breach or more than 15% over previous tag.
- **Out of scope:** Hardware power sweeps (BL-436); network soak (BL-434, may be called from here).
- **Depends on:** BL-420, BL-423, BL-424, BL-427, BL-430, BL-431; BL-153 (the existing verify-release workflow); P3.
- **Risks / unknowns:** Report: virtualized runners vary and cannot measure C-states; thresholds on a shared runner may flap. Budgets are `[INFERRED]` so first run sets numbers; thresholds may need a one-time review. The 1M-point case must be generated from the real schema, not a fake shape (owner rule); the live path check is BL-431.
- **Acceptance:**
  - A deliberately bloated build (extra 20 MB file, leaked goroutine) fails the gate; a normal build passes. (CI)
  - First green run on a real rc tag; numbers compared with nachi's PC run from BL-420/BL-427. (live PC (nachi))
- **Size:** M. **Phase:** P3 (extended in P4).
- **Needs owner action?** No.

## BL-436 Pre-release hardware power sweep
- **Outcome:** A short runbook and script that checks real idle power behaviour on a physical Windows 11 PC before an rc.
- **Scope (in):** 10 min tray-idle with `wpr -start CPU -start Power`; no timer-resolution elevations; deep C-state residency; `powercfg /energy /duration 60` has no Unravis entry under timer resolution or CPU warnings; Process Explorer check of Efficiency Mode and handle counts. Saved report in `docs/desktop/`.
- **Out of scope:** Automating this in CI (cannot: virtual runners).
- **Depends on:** BL-425, BL-426, BL-427; P4.
- **Risks / unknowns:** Result depends on the machine's hardware; one PC is one data point. WPA reading is manual. Server 2016 has no EcoQoS (fallback only).
- **Acceptance:**
  - Script collects traces and exits clean; report template renders. (CI for the script shape only)
  - Full sweep on nachi's PC with the real app and a real case open then hidden. (elevated/UAC)
- **Size:** S. **Phase:** P4.
- **Needs owner action?** Yes, UAC for `powercfg /energy` and WPR.

## BL-437 Document the WebView2 OneAuth leak and optional firewall block
- **Outcome:** Users and admins can read what the app cannot silence, and strict-mode users can turn on an outbound block.
- **Scope (in):** A docs page for `WebView2Feedback#5760` (Runtime 155-157 calls Microsoft hosts about 2-3 s after launch, not stoppable by app flags) with the host list found in BL-434; an optional "strict network" setting in setup that adds a Windows Firewall outbound rule scoped to `msedgewebview2.exe`; a re-check step when a new Runtime ships.
- **Out of scope:** Shipping a fixed-version runtime (rejected in DECISIONS); disabling machine-wide EdgeUpdate (report: do not).
- **Depends on:** BL-434; P4; installer work in 06-distribution.
- **Risks / unknowns:** Report evidence for `#5760` is one issue thread; whether it fires on local (non-AAD) accounts is untested (report risk 1). A rule keyed to the WebView2 exe path may break when the runtime updates its folder. Blocking all outbound for that exe may break nothing for loopback but must be tested. Rule creation needs admin.
- **Acceptance:**
  - Docs page exists; setup test (dry run) shows the rule command it would run. (CI)
  - On nachi's PC: rule on, soak again shows zero WebView2 outbound packets and the UI still works with a real case; rule off removes it. (elevated/UAC)
  - Re-check on the newest WebView2 Runtime, result recorded. (live PC (nachi))
- **Size:** S. **Phase:** P4.
- **Needs owner action?** Yes, UAC.

---

## Conflicts with other areas

1. **Transport (BL-431, BL-432):** report 08 wants a direct `127.0.0.1` fetch for chart data; DECISIONS says named pipe, no loopback port in desktop mode (loopback only as a recorded exception if the benchmark fails). BL-432 settles it; 01-shell and 02-security must not hard-code either until then.
2. **Sidecar exe split (BL-422):** depends on the P1 spike result (same `unravis.exe` or `unravis-core.exe`). The "no embedded UI" saving may not exist if browser fallback needs the embed.
3. **Idle signal and busy hook (BL-425, BL-423):** share one definition of "in use" with the silent updater (BL-168, BL-171, page presence, MCP requests). Do not make two idle clocks.
4. **WebView2 settings (BL-433):** overlaps the WebView2 lockdown list in 02-security (DevTools, extensions, downloads). Split the file: 02 owns lockdown, 07 owns only network-silence flags. Two items editing the same `tauri.conf.json` and builder code will conflict.
5. **Window lifecycle (BL-427):** overlaps tray, single-instance and autostart in 01-shell. 07 owns only grace, suspend, destroy and recreate; 01 owns the tray and the process.
6. **Installer and firewall rule (BL-437):** needs a setup step; belongs to the Go `unravis-setup.exe` work in 06-distribution.
7. **CI file (BL-435):** edits `verify-release.yml`, also touched by BL-153 (auto-updater) and the testing area. Sequence the PRs.
8. **MCP / code mode / CLI (03, 04, 05):** MCP stdio clients may keep the backend "busy" and block idle power savings (BL-425). Code-mode sandbox processes (LPAC, Job Object) add RSS that the tray-idle budget of 35 MB does not count; confirm they are not running at idle.
9. **Server 2016:** report 08 assumes Win 10/11 benchmarks; Blackbox services are Session 0 and are outside these budgets. DECISIONS Lite stays pure Go with no WebView2, so none of the WebView items apply to Lite.

## Questions for spec stage

1. Does the repo's `go.mod` use Go 1.27 (needed for `goroutineleak`) and which `ncruces/go-sqlite3` version (wasm2go or the older wazero build)? DECISIONS mentions wazero JIT; report 08 assumes wasm2go.
2. Is there a Server 2016 machine for tests and a small-RAM PC? If not, the Server 2016 and low-RAM acceptances stay unproven.
3. What is the biggest real case file nachi has (rows)? The 1M-10M row budgets assume one; if none exists, who supplies a real one (not a generated fake)?
4. Same exe or split `unravis-core.exe`? (decides BL-422)
5. Which UI state must survive window destroy (filters, scroll, open drill pop-up)? (BL-427)
6. Tray grace of 60 s: fixed or a user setting? Does a running import job or active MCP session block destroy?
7. Is a test-only `--no-update-check` flag acceptable, or reuse something that exists? (BL-434)
8. Are the section 9 budgets final, or only first guesses to be reset after the P0 baseline and the P2 measurement? The report marks most as `[INFERRED]`.
9. Should the strict-network firewall rule (BL-437) be per-user (no UAC, not possible for Windows Firewall, as far as I know) or machine only? I could not confirm from the reports; needs a check.
10. Who runs the elevated and hardware items (BL-434, BL-436, BL-437), and when? They need nachi at the PC.
