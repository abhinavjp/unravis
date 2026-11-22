# Unravis Desktop (Tauri) - decision and plan

Status: plan v1, 2026-11-21. Source: 10 research reports (kept off-repo in `research/outputs/desktop-tauri/`, git-excluded). Owner goals: extremely secure, performant, robust, maintainable, scalable; minimal CPU, memory, network. Windows first.

Research gaps: none. All 10 outputs exist (58 to 80 KB each). Their open items are listed in section 12.

## 1. Decision (one line each)

| Area | Decision |
|---|---|
| Shell | **Tauri 2** (>= 2.12.7), thin Rust host, target under 1,500 LOC. |
| Backend | Existing pure-Go `unravis` stays (`CGO_ENABLED=0`). No FFI, no Rust rewrite. |
| Go link | Go is a **sidecar**, spawned from Rust only (never from JS), inside a Windows Job Object (`KILL_ON_JOB_CLOSE`). |
| Transport | **Named pipe** (current-user DACL, reject remote, peer PID check). No loopback port in desktop mode. |
| UI | Same Svelte 5 + ECharts 6 app, bundled. Strict CSP, no `unsafe-inline`/`unsafe-eval`. |
| Installer, OTA | **Keep** Go `unravis-setup.exe` and the ed25519 OTA updater. Do not use Tauri NSIS or `tauri-plugin-updater`. |
| Services | Blackbox, BlackboxSql, Key Keeper stay as they are. Tauri never touches services. |
| Lite | Stays pure Go, no WebView2 (servers often lack it). |
| CLI, MCP, code mode | Stay in console-subsystem Go `unravis.exe`. One Go operation layer for all surfaces. |
| Browser mode | Stays as fallback and default until desktop passes the gates in section 9. |

Rejected, with the reason: Electron (Chromium patch treadmill, 80-165 MB installer); Wails v3 (under 1 month after GA, no IPC ACLs; re-check mid 2027); Go FFI (forces CGO, crashes take down the host); Rust rewrite (Rust `mongodb` crate dropped MongoDB 3.4); MSIX (no virtual service accounts, no Server 2016 services); fixed WebView2 runtime (+180 MB, we own Chromium patching); `tauri-plugin-stronghold` (deprecated); all official Tauri plugins in the first release.

## 2. Reconciling the reports (where they disagree)

1. **Loopback vs pipe.** Reports 01/02/03 say pipe only. 06 and 10 say reuse loopback first. 08 says loopback for bulk chart data. Decision: phase it. Preview shell loads the existing loopback UI with a one-time launch grant (phase 2). Bundled shell moves to the pipe (phase 3). Bulk chart data stays on the pipe unless the phase 3 benchmark proves WebView2 binary IPC too slow (about 200 ms per 10 MB reported); only then a token-gated native-only loopback endpoint, and it is recorded as a security exception.
2. **Who owns Go's life.** 03 says Rust spawns it. 10 says desktop attaches to a per-user Go broker that owns tray and case data. Decision: attach first, spawn if none. Go keeps single ownership of case data and the singleton, so browser, CLI and MCP keep working when the window closes.
3. **Binary names.** 03 wants a thin CUI `unravis.exe` plus a separate backend exe. 10 wants existing names kept. Decision: keep `unravis.exe` (console, CLI and `--mcp`, existing path). Add `unravis-desktop.exe` (Tauri GUI launcher). Whether the sidecar is the same `unravis.exe` or a split `unravis-core.exe` is settled in the phase 1 spike by the OTA file-lock test (MCP holding the exe during an update).
4. **Signing.** 07 recommends Authenticode (Azure Artifact Signing or SSL.com). Ed25519 OTA signing stays as the update trust root; Authenticode is added for SmartScreen and Defender. This costs money and needs an identity, so it is the one owner action (BL-312).

## 3. Process model

Per user at idle (tray): `unravis-desktop.exe` (Rust) + Go backend. No WebView2 process (window destroyed after a 60 s grace). Per machine: 3 Session 0 services, unchanged.

```
unravis-desktop.exe (Rust, user) --job object--> unravis (Go sidecar, user)
        | invoke/Channel                              | named pipe (HTTP/1.1 over pipe, go-winio)
   WebView2 (Svelte)                                   +--> Key Keeper pipe (existing, service)
                                                       +--> Blackbox recordings (read)
unravis.exe --mcp / CLI ---- same pipe, own grant ----+
unravis-sandbox.exe (code mode, LPAC) <-- stdio pipes -- Go broker
```

Rules: Rust refuses to run elevated (token check). Single instance (per session). Autostart via direct `HKCU\...\Run`. Sidecar shutdown by RPC (SQLite WAL checkpoint, 5 s grace) before the job kills it. Crash capture is local only, no telemetry. Data moves from `%APPDATA%\APIAuditExplorer` to `%LOCALAPPDATA%\Unravis\{data,logs,crashes,webview2}` by copy and SHA-256 verify, never delete the old one.

## 4. Security model

- Tauri >= 2.12.7 (fixes channel ACL bypass, origin confusion, CSP nonce bypass). Rust >= 1.90.
- Zero official plugins at first. Only explicit read-only custom commands, listed in `build.rs` `AppManifest::commands`, one capability file. No shell, fs, http, dialog, updater plugins.
- Brownfield pattern with strict CSP. Move to Isolation if the npm tree grows.
- CSP: no `unsafe-inline`, no `unsafe-eval`, no `data:`/`blob:` in `script-src`. ECharts `renderer: canvas`, `tooltip.renderMode: richText`. Vite `assetsInlineLimit: 0`. Trusted Types in report-only first (Svelte >= 5.52).
- WebView2: Evergreen only. Per-user `UserDataFolder`, extensions off, DevTools, context menu, downloads, popups, external navigation, file drop off. Strip `WEBVIEW2_*` env at start of `main()`.
- Pipe: SDDL current user, `PIPE_REJECT_REMOTE_CLIENTS`, `FILE_FLAG_FIRST_PIPE_INSTANCE`, `GetNamedPipeClientProcessId` check, random 128-bit name.
- Secrets: Key Keeper stays the only secret boundary. Desktop never holds secrets.
- Process mitigations: Rust exe CFG + CET + ASLR + DEP + strict handles + image load policy. Go exe all of those except CET (breaks goroutines) and ACG while wazero JIT is on.
- Supply chain: `cargo-deny`, `govulncheck`, `osv-scanner`, `trufflehog`, npm `ignore-scripts` + release-age cooldown, CycloneDX SBOM. No Trivy action (CVE-2026-33634).
- Network silence: updater only talks to the existing feed. WebView2 hardened (reputation check off, custom crash reporting). Known leak: Runtime 155-157 OneAuth/WAM (WebView2Feedback#5760); documented, optional firewall rule. CI soak test must show 0 non-loopback packets from Go and Rust.

## 5. MCP

stdio only at launch (`unravis.exe --mcp`, existing contract). Official Go SDK after a parity spike with both wire lifecycles (2026-07-28 and older). Backend gets a dedicated read-only MCP endpoint on the pipe with revocable per-installation grants. Aggregates first; row-level timing, expensive queries and identifiers are separate grants. Bounded typed results with provenance. Keep the ten tool names, add structured output. No dynamic catalogs, sampling, elicitation or Tasks yet. HTTP transport only if a required host cannot use stdio. Audit log of every call.

## 6. Code mode

`quickjs-ng` (WASI) inside `wazero` (pure Go), in a separate `unravis-sandbox.exe`, launched in a zero-capability LPAC (fallback: restricted token + untrusted IL) inside a Job Object with mitigation policies. TypeScript (erasable subset) stripped host-side with `esbuild`. `.d.ts` generated from the MCP tool schemas. Stdio pipes only, no ambient globals, only the ten read-only `unravis.*` functions. Limits: 64 MiB WASM, 32 MiB heap, 30 s wall, 10 s CPU, 50 tool calls, 1 MiB output. Default-deny network and writes. Identifier tokenization before data enters. Runs recorded in the sealed case DB with a hash chain and replay. Exposed on MCP (`unravis_execute`, `unravis_types`), CLI (`unravis code run|types|replay`) and a read-only UI inspector. Gate: Windows x64 benchmark plan from report 05 before locking the runtime.

## 7. CLI parity

Console Go `unravis.exe` plus a distinct GUI launcher. Cobra (PowerShell completion). Versioned JSON contract, JSON Lines for streams, progress on stderr, stable error codes and exit map. One Go operation layer carries auth, validation, budgets, result handles and privacy projections; a reviewed capability matrix covers GUI, CLI, automation CLI, MCP and sandbox. App absent: explicit headless owner, or explicit offline reader for snapshots and Blackbox recordings; never silently start the GUI. Ships in the signed OTA release; scoped PATH entry; WinGet as optional channel.

## 8. Distribution, signing, updates

- Keep `unravis-setup.exe` and the OTA updater (signed manifest, expiry, sequence, two keys, rollback, service hand-off). Extend setup to lay down `unravis-desktop.exe`, detect WebView2, run the Evergreen bootstrapper (online) or standalone installer (air-gapped), and stop both processes cleanly on upgrade.
- Enterprise: WiX MSI split (Desktop MSI, headless Blackbox Server MSI) later; WinGet first, Chocolatey only on demand. HKLM policy key plus ADMX for update control.
- Authenticode: sign bottom-up in CI with RFC 3161 timestamps; `signtool verify` fails the build. Provider per report 07 (Azure Artifact Signing if eligible, else SSL.com IV + eSigner). No EV cert.
- Reuse the existing release manager, keys and `verify-release` flow. The Tauri `.exe` is just another signed file in the release set. OTA pass criteria stay as today.

## 9. Performance budgets (CI fail thresholds in brackets)

| Metric | Target | Fail |
|---|---|---|
| Installer (all parts) | <= 25 MB | > 35 MB |
| Tray-idle RAM (WebView2 destroyed) | <= 35 MB | > 55 MB |
| Tray-idle CPU | < 0.05% of a core | > 0.2% |
| Window open, idle | <= 180 MB total | > 240 MB |
| Big case, 4 charts | <= 350 MB | > 500 MB |
| Cold start to UI | <= 1.5 s | > 2.5 s |
| Tray reopen | <= 800 ms | > 1.5 s |
| Chart pan/zoom p95 | <= 100 ms | > 250 ms |
| Leaked goroutines after cycles | 0 | > 0 |
| Non-loopback packets (120 s) | 0 from Go and Rust | any |

Techniques: destroy WebView2 on tray-hide; EcoQoS + ignore-timer-resolution when idle; `GOGC=50` + soft memory limit + `FreeOSMemory` on idle; `-trimpath -ldflags "-s -w"`, no UPX; backend M4 downsampling (<= 4 x width points); ECharts canvas with `animation: false`, `showSymbol: false`. Honest note: active-window RAM is about the same as Electron (108 MB empty window); the wins are idle, disk and security.

## 10. Testing and CI

- Playwright (existing suite) on `ubuntu-latest` against Chromium + Tauri API mocks + Go test server. Thin Windows smoke on the real shell via `connectOverCDP`; fallback `@wdio/tauri-service`.
- Rust: `tauri::test` MockRuntime. Contract tests: shared JSON Schema + golden fixtures (Go and Rust). `windows-2025` runners; `windows-11-arm` on release tags only. `Swatinem/rust-cache`.
- Installer, service, OTA tests: standard local user via scheduled task for per-user installs; localhost feed with ephemeral CI key and `--test-crash-on-start` for N-1 to N and 3-bad-start rollback.
- Gates: `golangci-lint`, `clippy -D warnings`, diff coverage 75-85%, `cargo-mutants --in-diff` on the IPC and sandbox core, footprint checks from section 9, WFP/pktmon network soak. Estimated CI cost for a solo workload: low tens of dollars per month.
- Existing rule stays: the Linux baseline for `internal/server` is the 19 known Windows-only failures, not zero.

## 11. Repo layout

Add, do not move. `desktop/src-tauri/` (Cargo.toml, Cargo.lock, tauri.conf.json, capabilities/, src/) next to the Go root and `web/`. `web/src/lib/` gets a host adapter (browser or desktop); desktop build output `web/dist-desktop/`. `contracts/http/v1/openapi.yaml` (OpenAPI 3.1 for one existing slice first; Go DTOs and TS types generated, handlers stay Go), `contracts/desktop/v1/` for IPC fixtures. `docs/adr/` for decisions. Orchestration stays PowerShell plus native CLIs. One Cargo package until a second crate exists. One product tag and one signing path; wire and storage versions separate. Dependabot, no auto-merge. Pin `https://v2.tauri.app/llms.txt` in `CLAUDE.md` for agents once `desktop/` lands.

## 12. Milestones

Each phase ships a usable slice, browser keeps working throughout. Exit criteria are tested, not claimed.

| Phase | Scope | Exit |
|---|---|---|
| P0 Baseline | Measure today: browser runtime, startup, install/upgrade, MCP, Server 2016 Blackbox. Record hardware and numbers. | Numbers saved; prior release named for rollback. |
| P1 Go attachable | `--pipe-server` mode serving the existing mux over a secured named pipe; attach-or-start with singleton; shutdown RPC; refuse-admin check. Sidecar-vs-split-exe spike. | Concurrent starts, two logons, stale locator, crash all leave one owner. Pipe security tests pass on Windows CI. |
| P2 Shell preview | `desktop/src-tauri` skeleton: Rust spawn + Job Object, tray, single instance, window destroy/recreate, loads UI. Opt-in only. CSP and WebView2 lockdown. | Opens same cases as browser; disabling desktop leaves browser fine; Alt+Tab focus bug (tauri#15624) checked. |
| P3 Bundled transport | Bundled UI, named invoke commands, Rust-to-pipe relay, one vertical slice (case home). Bulk chart benchmark. OpenAPI pilot. | Budgets in section 9 met on a real PC; ACL, origin and cancellation tests pass. |
| P4 Desktop RC | Full parity by slices, installer integration, WebView2 install modes, Authenticode, OTA with rollback, network soak. | Upgrade and rollback on Win 10/11 per-user and per-machine; Server 2016 headless intact; signed release passes verify. |
| P5 Default | Promote desktop only with evidence; browser stays as recovery. | Clean-start, perf, accessibility, crash/reopen evidence documented. |
| Parallel A | MCP on pipe with grants (SDK parity spike first). | Existing ten tools pass on old and new lifecycle. |
| Parallel B | CLI contract (Cobra, JSON, exit map). | Golden output tests. |
| Parallel C | Code mode (after benchmark plan). | Escape tests and limits tests green. |

Open items from the research: real-world Tauri 2 cold start and RAM on Win 10/11 and Server 2016 (no data; measure in P2); AV/EDR locking the WebView2 folder; MCP 2026-07-28 client support; code-mode benchmark on Windows x64; Authenticode eligibility; WebView2 binary IPC speed.

## 13. Backlog items

Desktop items use the BL-300 range so they do not collide with other threads. They are recorded in `docs/BACKLOG.md`. None is ticked until it works on the real data path (owner rule).
