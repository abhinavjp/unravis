# Groom 01: shell and sidecar (BL-300 to BL-319)

Area: Tauri shell, Go sidecar, process model, pipe, lifecycle, tray, data folders. Stage 1 (groom only). Sources: research 01 (Tauri vs alternatives), 03 (Go sidecar process model), `DECISIONS-draft.md`, `CLAUDE.md`.

Notes
- Acceptance tags: **CI** = runs in CI. **live PC (nachi)** = real PC, real data. **elevated/UAC** = needs an elevated step.
- Owner rule: every acceptance below is on the real path (real Go backend, real named pipe, real cases). No dummy data. Mock-only tests are allowed as extra, never as the proof.
- Report 01 reads only lines 1-187 of 263 and report 03 lines 1-322; the Tauri risks list and the Go risks list were both read. Report numbers are as stated by the reports; I did not re-verify them.
- BL-312 is reserved by the draft (section 2, item 4) for Authenticode signing. It is not in this area. Left unused here.
- Binary names are not settled. The draft keeps `unravis.exe` (CLI/MCP) and adds `unravis-desktop.exe`. Report 01 says `unravis-desktop.exe` + `unravis-core.exe`; report 03 says `unravis-app.exe` + `unravis-backend.exe` + thin `unravis.exe`. This file writes "desktop exe" and "sidecar exe" until BL-305 decides.

## Summary table

| ID | Title | Phase | Size | Owner action |
|---|---|---|---|---|
| BL-300 | Baseline measurements of today's browser runtime | P0 | M | no |
| BL-301 | Go `--pipe-server` mode on a secured named pipe | P1 | L | no |
| BL-302 | Attach-or-start, singleton and locator file | P1 | M | no |
| BL-303 | Shutdown RPC with SQLite WAL checkpoint | P1 | S | no |
| BL-304 | Refuse-administrator gate (Go side) | P1 | S | no |
| BL-305 | Spike: same exe or split sidecar exe (OTA file-lock test) | P1 | S | no |
| BL-306 | Tauri shell skeleton: spawn, Job Object, attach | P2 | L | no |
| BL-307 | Sidecar supervisor: health, restart, circuit breaker | P2 | M | no |
| BL-308 | Tray and WebView2 destroy/recreate | P2 | M | no |
| BL-309 | Single instance per Windows session | P2 | S | no |
| BL-310 | Autostart at sign-in (HKCU Run) | P2 | S | no |
| BL-311 | WebView2 and CSP lockdown for the shell | P2 | M | no |
| BL-313 | Bulk chart payload benchmark over the pipe | P3 | S | no |
| BL-314 | Rust-to-pipe relay with typed commands (case home slice) | P3 | L | no |
| BL-315 | Local-only crash capture (Rust and Go) | P2 | M | no |
| BL-316 | Data folder move to `%LOCALAPPDATA%\Unravis` | P1 | L | no (copy only) |
| BL-317 | Bundle the sidecar (`externalBin`) with stale-cache guard | P2 | S | no |
| BL-318 | CLI and MCP attach to the pipe via locator | Parallel A/B | M | no |

18 items. No item needs a secret, passphrase, money or deleting user data. Elevated steps appear only as test steps (see BL-316, BL-317).

---

## BL-300 Baseline measurements of today's browser runtime
- Outcome: saved numbers for the current browser-mode app, so later phases have a rollback target and a fair compare.
- Scope (in): cold start to UI, idle RAM/CPU of Go, RAM with a big case open, install and upgrade time, `--mcp` start time, Blackbox on Server 2016. Record hardware and OS. Name the prior release as the rollback.
- Out of scope: any code change; budgets for the new shell (those are another area's gates).
- Depends on: none (P0).
- Risks / unknowns: report 01 risk 2 says no controlled Windows cold-start or RAM data exists for Tauri; the baseline is the only compare point. Server 2016 box availability unknown.
- Acceptance:
  - Numbers file committed under `docs/desktop/` with machine spec and release tag. (live PC (nachi))
  - Same run repeated 3 times, spread noted. (live PC (nachi))
  - `--mcp` initialize latency measured with the real ten tools. (live PC (nachi))
- Size: M. Phase: P0.
- Needs owner action? No (nachi runs it on his PC; no secrets).

## BL-301 Go `--pipe-server` mode on a secured named pipe
- Outcome: the existing Go HTTP mux is served over a per-user named pipe, with no TCP port in this mode.
- Scope (in): `go-winio` listener; pipe name with random 128-bit nonce; SDDL current user only, deny network; `PIPE_REJECT_REMOTE_CLIENTS`; `FILE_FLAG_FIRST_PIPE_INSTANCE`; `GetNamedPipeClientProcessId` check (log/defense in depth); existing handlers unchanged; loopback mode kept behind a flag for transition.
- Out of scope: the Rust side; the Key Keeper pipe (unchanged); MCP grants (area MCP); bulk-data policy (BL-313).
- Depends on: P0 (BL-300 for compare). Feeds BL-302, BL-306, BL-314.
- Risks / unknowns: report 03 says the PID check is not a boundary against same-user injection, so the DACL is the boundary. Report 03 risk 5: single-stream 10-50 MB over `go-winio` has no data. The draft's SDDL `(A;;GA;;;OW)` (report 01) and the user-SID SDDL (report 03) differ; spec must pick one. `go-winio` version v0.6.4 per report 03.
- Acceptance:
  - Real backend starts in pipe mode, `netstat` shows no new listening port for the process. (CI) (Windows runner)
  - A second Windows user and a network client cannot open the pipe (access denied). (CI) (needs a second local account on the runner; if not possible, live PC (nachi))
  - A second process creating the same pipe name fails (first-instance flag). (CI)
  - Real case list and one real case open through the pipe return the same JSON as loopback mode. (live PC (nachi))
  - Existing Go test baseline unchanged (the 19 known Windows-only failures in `internal/server`, no new ones). (CI)
- Size: L (split later if the spec wants: listener vs hardening). Phase: P1.
- Needs owner action? No.

## BL-302 Attach-or-start, singleton and locator file
- Outcome: one Go owner per user. A second launcher attaches to it instead of starting another.
- Scope (in): per-user singleton; owner-only locator file (draft/report 03: `%LOCALAPPDATA%\Unravis\run\session.json`) holding pipe name; stale-locator detection (dead PID, dead pipe); concurrent-start race handled; two Windows logons stay separate.
- Out of scope: Rust attach code (BL-306); per-installation MCP grants.
- Depends on: BL-301.
- Risks / unknowns: draft section 2 item 2 says Go keeps case-data ownership and the singleton (attach first); report 03 says Rust spawns and owns lifetime. The Job Object kills the sidecar when the shell exits, which conflicts with "browser, CLI, MCP keep working when the window closes". Needs a spec decision (see Questions). Whether the session token in `session.json` is still needed over a pipe is unknown.
- Acceptance:
  - 10 simultaneous starts leave exactly one owner process. (CI)
  - Kill the owner with `taskkill /f`, then start: stale locator is detected and replaced. (CI)
  - Two logons (RDP or second user) each get their own owner. (live PC (nachi)) (second user on nachi's PC or a test VM)
  - Locator file ACL is current user only (icacls check). (CI)
- Size: M. Phase: P1.
- Needs owner action? No.

## BL-303 Shutdown RPC with SQLite WAL checkpoint
- Outcome: a clean stop that never leaves uncheckpointed `-wal` data.
- Scope (in): internal shutdown endpoint on the pipe; `http.Server.Shutdown`, wait for writers, `PRAGMA wal_checkpoint(TRUNCATE)`, `db.Close()`, exit 0; 5 s grace is the caller's rule.
- Out of scope: Job Object kill (BL-306); the Rust caller.
- Depends on: BL-301.
- Risks / unknowns: report 03 states `CTRL_BREAK_EVENT` does not work with `CREATE_NO_WINDOW`, so RPC is the only clean path. Forced kill after 5 s may still leave WAL; recovery on next open is the fallback (not studied in the report, flag).
- Acceptance:
  - With a real case DB under write load, call shutdown: process exits 0, no `-wal`/`-shm` left, `PRAGMA integrity_check` ok. (live PC (nachi))
  - Same test in CI on a generated-by-the-app DB (real code path, app-created data). (CI)
  - Force-kill during write, restart: DB opens and `integrity_check` passes. (CI)
- Size: S. Phase: P1.
- Needs owner action? No.

## BL-304 Refuse-administrator gate (Go side)
- Outcome: the Go backend in desktop/pipe mode refuses to run elevated.
- Scope (in): token checks: `TokenElevation`, integrity level >= High, `CheckTokenMembership(Administrators)`; clear message and exit code. Not `IsUserAnAdmin()` (report 03: deprecated).
- Out of scope: the Rust-side check (inside BL-306); setup's elevated worker (unchanged).
- Depends on: none (P1).
- Risks / unknowns: the existing OTA/setup flow runs elevated workers; the gate must not block them. Which Go entry points are gated is a spec question. UAC-off machines (`EnableLUA=0`) covered by check 3 per report 03; not testable in CI easily.
- Acceptance:
  - Start from a normal prompt: runs. (CI)
  - Start from an elevated prompt: refuses with the agreed exit code and message. (elevated/UAC)
  - Admin account with UAC off (VM): refuses. (elevated/UAC)
- Size: S. Phase: P1.
- Needs owner action? Only to accept the UAC prompt in the test (UAC).

## BL-305 Spike: same exe or split sidecar exe
- Outcome: a written decision with evidence on whether the sidecar is `unravis.exe` itself or a separate exe.
- Scope (in): test that an MCP client holding `unravis.exe` open blocks (or does not block) replacing the sidecar during a real OTA/upgrade; size and start time of each option; MCP console-flash check in current Claude Code and Codex on Windows 11 (report 03 risk 3).
- Out of scope: renaming binaries in the repo; changing the updater (area updater, BL-149).
- Depends on: BL-300, BL-301.
- Risks / unknowns: draft section 2 item 3 leaves this open for this exact spike. Reports use 3 different name sets. Report 03 risk 4: cold-start delta of thin CUI exe vs others has no data.
- Acceptance:
  - Real upgrade run with a live MCP session attached: result recorded for both layouts. (live PC (nachi))
  - Decision note lists final binary names and which exe the OTA replaces. (CI) (doc review only; marker for the spec stage)
- Size: S. Phase: P1.
- Needs owner action? No (if a test OTA key is used, it is the ephemeral CI key from the draft).

## BL-306 Tauri shell skeleton: spawn, Job Object, attach
- Outcome: a signed-ready (unsigned for now) `desktop` exe that attaches to or spawns the sidecar, with the sidecar tied to a Job Object, and loads the UI.
- Scope (in): `desktop/src-tauri` (Tauri >= 2.12.7, Rust >= 1.90); spawn from Rust only with `CREATE_NO_WINDOW | CREATE_SUSPENDED`, assign Job Object `KILL_ON_JOB_CLOSE`, resume; refuse-elevated check in Rust; no shell plugin, no `shell:allow-*` capability; opt-in launch only; browser mode untouched; loads the existing loopback UI first (draft phase 2).
- Out of scope: tray (BL-308), supervisor (BL-307), bundled UI and pipe relay (BL-314), installer.
- Depends on: BL-301, BL-302, BL-305, BL-304 (same check, Rust copy).
- Risks / unknowns: report 01 risk 4 (Tauri 3 alpha) and risk 2 (no real data on cold start). Job Object kill vs "Go outlives window" (see BL-302). Nested jobs only on Windows 8+/Server 2012+ (report 03), fine for Server 2016. Rust LOC target under 1,500 (draft); this item should stay small.
- Acceptance:
  - Shell opens the same real cases as browser mode. (live PC (nachi))
  - End-task the shell in Task Manager: sidecar is gone within seconds, no orphan. (live PC (nachi))
  - Spawn from JS is impossible: capability file has no shell permission (grep check). (CI)
  - Elevated launch refuses. (elevated/UAC)
  - Disabling desktop mode leaves browser mode working. (live PC (nachi))
- Size: L. Phase: P2.
- Needs owner action? No (UAC prompt only for the elevated test).

## BL-307 Sidecar supervisor: health, restart, circuit breaker
- Outcome: the shell recovers from a sidecar crash without orphan or restart storm.
- Scope (in): wait on child; health check over the pipe; restart with backoff 0.5 to 8 s with jitter; circuit breaker at >= 5 crashes in 60 s; tray/UI error state with "Restart backend" and "Open crash logs"; no restart during shutdown.
- Out of scope: crash dump capture (BL-315); the tray itself (BL-308).
- Depends on: BL-306, BL-303.
- Risks / unknowns: report 03 says no numeric SLA exists in Tauri docs; the numbers are the report's own proposal (`[INFERRED]`). Interaction with attach-first ownership (BL-302).
- Acceptance:
  - Kill the real sidecar 3 times: shell restarts it, the UI reconnects, no case data lost. (live PC (nachi))
  - Kill it 5 times in 60 s: breaker trips, tray shows error, no more restarts. (live PC (nachi))
  - Normal quit does not trigger a restart. (CI)
- Size: M. Phase: P2.
- Needs owner action? No.

## BL-308 Tray and WebView2 destroy/recreate
- Outcome: tray-resident app with no WebView2 process while the window is closed.
- Scope (in): built-in tray icon; window close destroys the WebView2 window after a 60 s grace (draft); `ExitRequested` handling so the app stays resident; tray Quit runs graceful shutdown; guard against double-close; reopen from tray.
- Out of scope: autostart (BL-310); memory budgets as CI gates (other area); EcoQoS tuning.
- Depends on: BL-306, BL-307.
- Risks / unknowns: report 03 risk 2: native WebView2 retention across create/destroy cycles (`WebView2Feedback#4286`, `[OLD]`); needs the 50-cycle soak. Reopen time of ~0.3 s and RAM savings come from third-party reports for small apps, not Unravis. Tauri `#14088` hidden-window crash noted.
- Acceptance:
  - Close window, wait over 60 s: no `msedgewebview2.exe` of this app; shell + sidecar private bytes recorded. (live PC (nachi))
  - 50 open/close cycles with a real big case: private working set and commit charge do not grow past the agreed margin. (live PC (nachi))
  - Tray reopen time measured against the draft target (<= 800 ms). (live PC (nachi))
  - Tray Quit: shell and sidecar both gone, WAL clean. (live PC (nachi))
- Size: M. Phase: P2.
- Needs owner action? No.

## BL-309 Single instance per Windows session
- Outcome: starting the app twice focuses the first one; two Windows users get one each.
- Scope (in): one instance per session (no `Global\` mutex); second launch forwards args and exits; foreground focus handoff. Deep-link handling only validates and drops unknown URLs (no deep-link feature yet).
- Out of scope: deep-link features; autostart.
- Depends on: BL-306.
- Risks / unknowns: the draft rejects all official plugins in the first release, but report 03 recommends `tauri-plugin-single-instance` >= 2.5.5 (`WM_COPYDATA`). Choose plugin vs own mutex in spec. UIPI breaks this when elevated (report 03), hence refuse-admin. Alt+Tab focus regression `tauri#15624` (report 01 risk 1).
- Acceptance:
  - Launch twice: one process tree, first window focused. (live PC (nachi))
  - Two simultaneous launches (script): one survives. (CI)
  - Second Windows user can run their own copy. (live PC (nachi))
  - Alt+Tab away and back: Tab key navigation still works (tauri#15624 check). (live PC (nachi))
- Size: S. Phase: P2.
- Needs owner action? No.

## BL-310 Autostart at sign-in (HKCU Run)
- Outcome: optional start in tray at sign-in, per user only.
- Scope (in): write `HKCU\...\Run` and `StartupApproved\Run` directly (or `auto-launch` with `CurrentUser`); launch with `--background`; user toggle; Task Manager Startup Apps shows it.
- Out of scope: `tauri-plugin-autostart` (report 03: v2.7.0 tries HKLM first); Task Scheduler.
- Depends on: BL-308.
- Risks / unknowns: the perihub#3 finding is one third-party issue; behavior not re-verified. Default on/off is an owner choice (spec).
- Acceptance:
  - Toggle on, sign out and in: app is in tray, no window, no HKLM write. (live PC (nachi))
  - Toggle off removes both keys. (CI)
  - Standard (non-admin) user can toggle. (live PC (nachi))
- Size: S. Phase: P2.
- Needs owner action? No.

## BL-311 WebView2 and CSP lockdown for the shell
- Outcome: the shell window has no extra browser surface.
- Scope (in): per-user `UserDataFolder` `%LOCALAPPDATA%\Unravis\webview2` on all windows; extensions off; DevTools, context menu, popups, external navigation, downloads, file drop off in release; strip `WEBVIEW2_*` env at start of `main()`; pin `useHttpsScheme` from day 1; baseline CSP without `unsafe-inline`/`unsafe-eval`, no `data:`/`blob:` in `script-src`.
- Out of scope: full CSP/Trusted Types/Isolation program, ECharts renderer settings (security/UI area).
- Depends on: BL-306.
- Risks / unknowns: overlap with the security area. Custom-protocol scheme choice resets storage origin if changed later (report 01). Report 01 risk 3: AV/EDR may lock `EBWebView`. Svelte/ECharts may need CSP changes; unknown until tried.
- Acceptance:
  - Release build: F12, right-click menu, Ctrl+P and drag-drop of a file do nothing. (live PC (nachi))
  - Navigating to an external URL is blocked. (live PC (nachi))
  - A test extension installed in the user's Edge does not appear in the shell. (live PC (nachi))
  - Set `WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS` before launch: ignored. (CI)
  - Real UI loads with the CSP with zero CSP violations in the console on the case home page. (live PC (nachi))
- Size: M. Phase: P2.
- Needs owner action? No.

## BL-313 Bulk chart payload benchmark over the pipe
- Outcome: measured numbers that decide how big chart data travels (pipe via Rust, or an exception).
- Scope (in): 10, 25, 50 MB binary payloads: pipe -> Rust -> `tauri::ipc::Response` and `Channel`; pipe buffer 64 KB vs 256 KB; compare with loopback HTTP; real ECharts series from a real big case.
- Out of scope: building the loopback exception (only if the numbers fail the draft threshold); backend M4 downsampling (other area).
- Depends on: BL-301, BL-306.
- Risks / unknowns: reports say no data found for 10-50 MB on this path; the only data point (~200 ms per 10 MB on WebView2) is `[OLD]` (Sep 2025) and from a macOS-vs-Windows discussion. The ~300 ms switch rule is report 03's own number. Draft: any loopback exception must be logged as a security exception.
- Acceptance:
  - Table of timings for each size and method on nachi's PC with real case data. (live PC (nachi))
  - Result states pass or fail against the draft thresholds (chart pan/zoom p95 <= 100 ms). (live PC (nachi))
- Size: S. Phase: P3.
- Needs owner action? No.

## BL-314 Rust-to-pipe relay with typed commands (case home slice)
- Outcome: the bundled UI talks to Go only through named Tauri commands relayed over the pipe, for one real vertical slice (case home).
- Scope (in): allowlisted, read-only custom commands (draft: listed in `build.rs`, one capability file); Rust relays to the pipe; request cancellation; size and time limits; Svelte host adapter call for this slice only; browser mode still uses fetch.
- Out of scope: full parity of all pages; OpenAPI contracts (other area); mutating commands.
- Depends on: BL-301, BL-306, BL-311, BL-313.
- Risks / unknowns: a generic "proxy any path" command would defeat the allowlist; the exact command list is a spec question. Channel/ACL issues were fixed in Tauri 2.12.0 (`GHSA-w28w-mhc8-qvjv`), so pin >= 2.12.7. Overlaps the UI-host-adapter area.
- Acceptance:
  - Case home loads real data in the bundled shell, no loopback port open. (live PC (nachi))
  - A call to a command not in the allowlist is rejected. (CI)
  - A request from another origin or iframe is rejected. (CI)
  - Cancel a running real call: Go side stops (no leaked goroutine). (live PC (nachi))
- Size: L (can split by command group in spec). Phase: P3.
- Needs owner action? No.

## BL-315 Local-only crash capture (Rust and Go)
- Outcome: crashes leave local files, with no upload.
- Scope (in): Rust `panic::set_hook` + out-of-process minidump (`minidumper-child`) to `%LOCALAPPDATA%\Unravis\crashes\`; Go `debug.SetCrashOutput` + `GOTRACEBACK`; file retention limit; open-folder action from tray; PDB archive per release in CI.
- Out of scope: service crash dumps (WER LocalDumps needs elevated setup; installer area); any telemetry.
- Depends on: BL-306.
- Risks / unknowns: `minidumper-child` v0.7.0 maturity not verified beyond the report's crates.io note. Dump files may contain sensitive data (case data in memory); retention and ACL rules needed (spec). Symbolication workflow needs PDBs kept per release.
- Acceptance:
  - Force a Rust panic and a native crash in a test build: log and `.dmp` appear, current-user ACL only. (live PC (nachi))
  - Force a Go panic in the real sidecar: crash log written. (live PC (nachi))
  - Network capture during the crash shows 0 outbound packets. (live PC (nachi))
  - Oldest files are pruned at the limit. (CI)
- Size: M. Phase: P2.
- Needs owner action? No.

## BL-316 Data folder move to `%LOCALAPPDATA%\Unravis`
- Outcome: per-user data copied from `%APPDATA%\APIAuditExplorer` to `%LOCALAPPDATA%\Unravis\{data,logs,crashes,webview2,run}` with verification. Old folder is never touched.
- Scope (in): one-time migration; `VACUUM INTO` for each SQLite DB, copy other files, SHA-256 check, `PRAGMA integrity_check`, atomic done marker; DPAPI blobs copied as is; owner-only ACLs on `run\`; browser mode and desktop mode read the same new location after the move.
- Out of scope: deleting the old folder (never, per draft); `C:\ProgramData\Unravis` ACL tightening (installer/services area).
- Depends on: BL-300 (backup point). Must land before BL-306 relies on the new paths.
- Risks / unknowns: report 03 says DPAPI blobs stay readable under the same account (cites docs); not yet proven on Unravis' own sealed data. This moves real case data, so the first run is risky. Report 03 notes no test of AD roaming profiles. Old-version rollback must still find legacy data.
- Acceptance:
  - Run on a copy of nachi's real data folder: all cases open, counts and checksums match, DPAPI-sealed items decrypt. (live PC (nachi))
  - Kill the migration at each step (5 points): restart completes or resumes, old folder byte-identical. (CI)
  - Roll back to the prior release: it still opens the old folder. (live PC (nachi))
  - Old folder untouched after run (hash compare). (CI)
- Size: L. Phase: P1.
- Needs owner action? Yes, soft: nachi confirms the real-data run and keeps a backup copy. No deletion of user data.

## BL-317 Bundle the sidecar (`externalBin`) with stale-cache guard
- Outcome: the built shell package contains the right, current sidecar exe, found next to the shell exe.
- Scope (in): `bundle.externalBin` with target-triple name; build script clears `target/*/<sidecar>.exe` first; Windows `VERSIONINFO` bumped per release; runtime path from `current_exe().parent()`; CI check that the bundled sidecar hash equals the just-built one.
- Out of scope: installer (`unravis-setup.exe`) changes and WebView2 install modes (installer area); signing (BL-312, other area).
- Depends on: BL-305, BL-306.
- Risks / unknowns: Tauri `#15134` (stale sidecar in `target/`, NSIS skips unversioned sidecars on same-version reinstall) is for Tauri's NSIS; the draft keeps Go setup, so how much applies is unknown. `tauri` 2.12.6 fixed absolute `externalBin` path bug (`#16453`).
- Acceptance:
  - Two builds in a row with a changed Go source: package hash of the sidecar changes. (CI)
  - Built package runs from a path with spaces and a non-C drive. (live PC (nachi))
  - Sidecar `VERSIONINFO` matches the release version. (CI)
  - Install over a running older copy via real `unravis-setup.exe`: new sidecar in place. (elevated/UAC)
- Size: S. Phase: P2.
- Needs owner action? UAC prompt for the setup test only.

## BL-318 CLI and MCP attach to the pipe via locator
- Outcome: `unravis.exe` (CLI, `--mcp`) finds the running owner through the locator file and talks over the pipe; never starts the GUI.
- Scope (in): read locator; pipe client; clear error when no owner; existing ten MCP tool names and stdio contract unchanged; direct exe path (no `.cmd` shim) in MCP configs; console subsystem kept.
- Out of scope: new MCP grants, Cobra/JSON contract (Parallel A/B areas); code mode.
- Depends on: BL-301, BL-302, BL-305.
- Risks / unknowns: report 03 risk 3: Claude Code and Codex had 2026 regressions that flash a console when spawning stdio servers; a `-H=windowsgui` alias (`unravis-mcp.exe`) is only an option if still broken. Risk 4: Defender scan cost on cold start unmeasured.
- Acceptance:
  - Real Claude Code and real Codex on Windows 11 call all ten tools through the pipe against a real case. (live PC (nachi))
  - No owner running: command exits with the stable error and does not open the GUI. (CI)
  - `--version` and `initialize` latency recorded with Defender real-time on. (live PC (nachi))
  - Console-flash check per host noted. (live PC (nachi))
- Size: M. Phase: Parallel A/B.
- Needs owner action? No.

---

## Conflicts with other areas
- **Installer and OTA (BL-149, installer area):** setup must stop shell and sidecar before replacing files; BL-305/BL-317 depend on how the OTA hands off. Binary names must match what the installer lays down.
- **Signing (BL-312):** draft assigns it to owner action; BL-306/BL-317 produce unsigned exes. SmartScreen/Defender heuristics (report 01 risk 6, report 03) may hit the first sidecar spawn tests.
- **Security/CSP area:** BL-311 sets the baseline WebView2 and CSP; full CSP, Trusted Types and capability design belong there. Avoid two owners of `tauri.conf.json` and `capabilities/`.
- **MCP area (Parallel A):** BL-318 only attaches; per-installation grants, the read-only MCP endpoint on the pipe and SDK choice are theirs. The pipe's auth model (who may connect) must be agreed with them.
- **Code mode area (Parallel C):** the sandbox exe is spawned by the Go broker, not by Rust; Job Object and LPAC rules must not clash with BL-306's job.
- **CLI contract area (Parallel B):** BL-318 vs their Cobra/JSON/exit-code work on the same `unravis.exe`.
- **UI/frontend area:** BL-314 adds a host adapter in `web/src/lib/`; same files as their work.
- **Performance/CI area:** BL-300, BL-308 and BL-313 produce numbers that their budget gates use; thresholds are theirs.
- **Official plugins:** draft says none in the first release; report 03 recommends `single-instance` (and deep-link). BL-309 needs a ruling.

## Questions for spec stage
1. Ownership: does Go outlive the shell (draft: attach-first, Go owns singleton) or does the Job Object kill it on shell exit (reports 01/03)? Both cannot hold for "browser/CLI/MCP keep working when the window closes".
2. Final binary names and whether the sidecar is split (decided by BL-305).
3. Pipe name and locator format: draft/report 03 differ (`UnravisUI-<pid>-<nonce>` vs `UnravisBackend-<SID>-<16hex>`). SDDL form: `OW` owner vs user SID.
4. Is a session token still needed over the pipe, given the DACL, or only for CLI/MCP grants?
5. Single-instance: official plugin or own mutex, given the "no plugins" rule?
6. Default for autostart (on or off), and whether first run asks.
7. Data move: is BL-316 a hard P1 step, or does the shell first read the old `%APPDATA%` path? Who triggers it (sidecar first start vs setup)?
8. What does "60 s grace then destroy WebView2" do when a long query is running in the window?
9. Crash dumps may hold case data: retention, ACL, and whether to offer "send to owner" as a manual action only.
10. Which Windows targets must pass at P2 exit: Win 10/11 only, or Server 2016/2019/2022 with RDP too? (Draft says Server 2016 headless intact at P4.)
11. Is the Rust-side refuse-admin gate also needed for the installer's UI worker path?
