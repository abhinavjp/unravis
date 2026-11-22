# Groom: security (Tauri desktop)

Source: research report 02 (`02_tauri_security_model.md`) and `docs/desktop/DECISIONS-draft.md` (plan v1). IDs BL-320 to BL-337. Stage 1 only: no code, no spec. Nothing here is ticked.

Test labels: **CI** = runs in CI (Linux or `windows-2025`). **live PC (nachi)** = run on the owner's Windows PC, normal user, real case data. **elevated/UAC** = needs an admin prompt.

Not duplicated here: the OTA updater and its signing (BL-149 to BL-151, BL-168, BL-194), Authenticode (BL-312 in the decisions), perf budgets and network soak (other areas). Report 02 says "do not use `tauri-plugin-updater`"; BL-321 only guards that with a CI check.

## Items

### BL-320 Pin Tauri and Rust baseline
- Outcome: the desktop crate builds only on a patched Tauri and Rust, from a locked tree.
- Scope: `desktop/src-tauri/Cargo.toml` pins `tauri` to the decided floor; Rust >= 1.90 (edition 2024) in `rust-toolchain.toml`; `Cargo.lock` committed; CI uses `cargo build --locked`; CI step fails if the resolved `tauri` is below the floor.
- Out of scope: other deps, plugin policy (BL-321), upgrade cadence.
- Depends on: P2 skeleton (`desktop/src-tauri`).
- Risks / unknowns: floor differs by source (report 02: `>= 2.12.6`, DECISIONS: `>= 2.12.7`); report was written 2026-11-16, so 2.12.7 is not in the report. Report 02 lists "security fixes shipped opt-in" as a bug class: every minor upgrade needs a release-notes check.
- Acceptance: CI: build fails when `Cargo.lock` has `tauri` below the floor (test with a doctored lock). CI: `cargo build --locked` passes on `windows-2025`. CI: `cargo tree` shows the advisory-fixed `tauri` (GHSA-w28w-mhc8-qvjv, CVE-2026-42184).
- Size: S. Phase: P2.
- Needs owner action? No.

### BL-321 Zero plugins and command gating
- Outcome: only named read-only custom commands can be invoked; no official plugin is linked.
- Scope: `build.rs` lists every command in `AppManifest::commands`; one capability file `main-windows` (window `main`, `platforms: ["windows"]`, `local: true`, no `remote`, no broad `core:default` if a subset works); a CI check that fails if `tauri-plugin-*` appears in `Cargo.lock` or if an `invoke_handler` command is missing from the manifest; CI check that `tauri-plugin-updater` is absent.
- Out of scope: the command bodies and the Rust-to-pipe relay (P3 transport work), window and tray behaviour.
- Depends on: BL-320; P2.
- Risks / unknowns: custom commands are allowed by default unless gated (report, Capabilities). Exact `core:` permission subset for tray, window destroy and recreate is not proven (report only lists close, minimize, maximize, unmaximize, set-focus). Tray/event permissions: no data.
- Acceptance: CI: a command added to `invoke_handler` but not to the manifest fails the build/test. CI: `Cargo.lock` has zero `tauri-plugin-*` entries. CI: capability JSON has no `remote`, no `core:default`, one window label. live PC (nachi): from the running shell, `invoke` of an unlisted command name is rejected by the runtime authority (checked with the real window, not a mock).
- Size: M. Phase: P2.
- Needs owner action? No.

### BL-322 `tauri.conf.json` security block and strict CSP
- Outcome: the shipped config matches the hardening checklist and CI blocks drift.
- Scope: `withGlobalTauri: false`, `pattern.use = brownfield`, `assetProtocol.enable: false`, `dangerousDisableAssetCspModification: false`, `app.security.capabilities` lists only `main-windows`, response headers (`nosniff`, COOP, COEP), production `csp` (no `unsafe-inline`, no `unsafe-eval`, no `data:`/`blob:` in `script-src`), separate `devCsp` for Vite HMR; a CI lint of the final config. `freezePrototype` is NOT set here (BL-324).
- Out of scope: frontend code changes needed to meet the CSP (BL-323), WebView2 settings (BL-328).
- Depends on: BL-321; P2.
- Risks / unknowns: report's sample CSP has `img-src 'self' blob:` and says `blob:` only must stay out of `script-src`; whether the UI needs `blob:` images is unknown. COEP `require-corp` may break loading; not tested (no data). CVE-2026-95626 applies if `data:`/`blob:` ever reaches `script-src`. Isolation switch trigger ("if npm tree grows") has no measurable threshold in the report.
- Acceptance: CI: lint fails on any of: `unsafe-inline`, `unsafe-eval`, `data:`/`blob:` in `script-src`, `withGlobalTauri: true`, asset protocol on, `remote` present. CI: `devCsp` is never copied into the release bundle (test inspects the built config). live PC (nachi): packaged build opens a real case and the WebView2 console shows zero CSP violations.
- Size: M. Phase: P2 (lint can start in P2; real-case check completes in P3).
- Needs owner action? No.

### BL-323 UI strict-CSP compatibility (ECharts, Vite, Svelte)
- Outcome: the Svelte 5 + ECharts 6 UI runs under the strict CSP with no inline script or style.
- Scope: ECharts `renderer: 'canvas'`, `tooltip.renderMode: 'richText'`; Vite `build.assetsInlineLimit: 0`; `define: { global: 'globalThis' }` only if a CJS dep needs it; no inline `style=`/`style:` in components (move to classes); do not set `html.cspNonce`; test the desktop build output `web/dist-desktop/`; the browser build must keep working.
- Out of scope: Trusted Types (BL-324), new charts.
- Depends on: BL-322; P3 (bundled UI).
- Risks / unknowns: how many existing components use inline `style` is not known (find it in the repo in the spec stage). Richtext tooltips may lose features the current tooltips have. Svelte inline-style behaviour taken from 2021/2024 issues marked `[OLD]`.
- Acceptance: live PC (nachi): packaged desktop build, real case, every chart type and screen opened; WebView2 console shows zero CSP violations (script and style). CI: grep/lint gate for `{@html}` and inline `style` in `web/src`; Playwright run on the desktop bundle with CSP header set, fail on any `securitypolicyviolation` event. CI: browser-mode Playwright suite still green.
- Size: L (split by screen in the plan stage if the inline-style count is high). Phase: P3.
- Needs owner action? No.

### BL-324 Trusted Types report-only and `freezePrototype` trial
- Outcome: a written, tested yes/no for `require-trusted-types-for 'script'` and `freezePrototype: true`, each enabled only if it passes.
- Scope: add Trusted Types in `Content-Security-Policy-Report-Only` on Svelte >= 5.52.0; collect violations over the real UI; fix or record; then enforce if clean. Same for `freezePrototype` across all chart types and interactions.
- Out of scope: new UI features, replacing libraries.
- Depends on: BL-323; P3.
- Risks / unknowns: report open item 1 (no Tauri 2 test matrix for Trusted Types; exact Svelte policy name unverified) and open item 2 (`freezePrototype` vs Svelte 5 + ECharts 6 not tested). ECharts 6 Trusted Types support: no data.
- Acceptance: live PC (nachi): real case, all screens, report-only collects zero violations (or each is listed and fixed); `freezePrototype: true` build opens all chart types with no console errors. CI: Playwright on the Chromium target with the same header, fail on violations. If a flag cannot be enabled, the result and reason are written to the ADR.
- Size: M. Phase: P3.
- Needs owner action? No.

### BL-325 Rust startup hardening (env, registry, no elevation)
- Outcome: the Rust host starts clean or refuses to start in a hostile launch context.
- Scope: first lines of `main()` remove `WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS`, `WEBVIEW2_BROWSER_EXECUTABLE_FOLDER`, `WEBVIEW2_USER_DATA_FOLDER`, `WEBVIEW2_RELEASE_CHANNEL_PREFERENCE` before any thread; read `HKCU` and `HKLM` `...\Policies\Microsoft\Edge\WebView2\AdditionalBrowserArguments` (for `unravis-desktop.exe` and `*`) and refuse or warn if debug or disable-security flags are present; refuse to run elevated (token check, also in DECISIONS section 3).
- Out of scope: process mitigations (BL-326), WebView2 policy checks beyond arguments (BL-329).
- Depends on: P2.
- Risks / unknowns: "refuse vs warn" is not decided in the report (INFERRED only). Which flags count as dangerous is a list we define (report names `--remote-debugging-port`, `--auto-open-devtools-for-tabs`, `--disable-web-security`, `--no-sandbox`). `env::remove_var` safety in edition 2024 needs the `unsafe` call before threads.
- Acceptance: CI: unit tests on the flag parser with a fake registry reader (flag list, `*` and exe-specific keys). live PC (nachi): launch with `WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS=--remote-debugging-port=9222` set and confirm nothing listens on 9222 and the app opens. live PC (nachi): write the HKCU policy value, confirm refuse/warn, then remove it. elevated/UAC: start the desktop exe elevated and confirm it refuses.
- Size: M. Phase: P2.
- Needs owner action? UAC for the elevated check only.

### BL-326 Rust exe process mitigations and linker flags
- Outcome: `unravis-desktop.exe` ships with CFG, CET, ASLR, DEP, strict handles, image-load and extension-point policies, proven on the built file.
- Scope: `.cargo/config.toml` flags (`control-flow-guard`, `/CETCOMPAT`, `/DYNAMICBASE`, `/HIGHENTROPYVA`, `/NXCOMPAT`); runtime `SetProcessMitigationPolicy` for strict handle (0x3), image load (0x7), extension points (0x1); a CI step reads the PE header and fails if a flag is missing. ACG on the Rust host is NOT enabled in this item.
- Out of scope: Go binaries (BL-327), reproducibility flags (BL-336).
- Depends on: P2.
- Risks / unknowns: ACG on the Rust host: the report's code sample sets it, but open item 4 says it is unproven with WebView2 loader and AV/EDR on Win 10 22H2, Win 11 and Server 2016 to 2025; DECISIONS lists no ACG for Rust. Treat as a separate trial. CET on Windows Server 2016 (no CET hardware/OS support) unknown: no data. Mitigations are set at runtime after start, so they are weaker than creation-time flags (not stated in the report; flag for spec).
- Acceptance: CI: PE-header check (`dumpbin /headers` or a Rust/Go PE parser) on the release build asserts CFG, CET-compat, dynamic base, high-entropy VA, NX-compat. live PC (nachi): run the signed-off build, read the policies back with `GetProcessMitigationPolicy` or Process Explorer, and confirm the app opens a real case and the WebView2 starts. Server 2016 run: live PC only if a Server 2016 box is available (not the owner's PC; ask in spec).
- Size: M. Phase: P2.
- Needs owner action? No.

### BL-327 Go binary mitigations and ACG decision
- Outcome: Go processes apply the safe mitigation set at start; ACG choice for each binary is decided by a measured test.
- Scope: `harden_windows.go`: strict handle, extension points, font disable, image load (0x7) in `unravis.exe`, Key Keeper, Blackbox, BlackboxSql; ACG only on Key Keeper and Blackbox; the sidecar is spawned with child-process-creation restricted (`UpdateProcThreadAttribute`, 0x1) by the Rust host (spawn side in BL-331); no `/CETCOMPAT` on Go. Spike: does `ncruces/go-sqlite3` (wazero) crash under ACG on `unravis.exe`; test wazero interpreter mode.
- Out of scope: code-mode sandbox policy (Parallel C), service logic.
- Depends on: P1 (Go attachable) for `unravis.exe` changes.
- Risks / unknowns: report open item 3 (wazero JIT vs ACG). Services run as Session 0 virtual accounts: a failing mitigation could stop a service from starting; rollback path is the existing OTA rollback. Whether child-process restriction breaks `unravis.exe` features that spawn processes (e.g. recovery or setup hand-off) is unknown: find in the repo.
- Acceptance: CI: Windows unit test that calls the apply function and reads policies back. live PC (nachi): start `unravis.exe` with real case data, open a case, run MCP and CLI, confirm no crash. elevated/UAC: install/upgrade the three services with the hardened build, confirm they start and Key Keeper and Blackbox still answer (real Blackbox recording read). ACG spike result written down with the wazero mode used.
- Size: M. Phase: P1 (spike) then P4 (services).
- Needs owner action? UAC for the service step.

### BL-328 WebView2 lockdown (builder and COM settings)
- Outcome: the window cannot open DevTools, menus, popups, downloads, external pages or file drops.
- Scope: no `devtools` Cargo feature; `devtools(false)`; builder callbacks `on_navigation` (allow only the app origin), `on_new_window` deny, `on_download` false; `disable_drag_drop_handler`; `browser_extensions_enabled(false)`; `general_autofill_enabled(false)`; zoom hotkeys off; `with_webview` COM settings (DevTools, context menus, status bar, host objects, script dialogs, built-in error page, password autosave, browser accelerator keys, pinch zoom, external drop off); single webview, single window label.
- Out of scope: UDF location and runtime detection (BL-329), the CSP.
- Depends on: BL-321; P2.
- Risks / unknowns: COM settings apply on the next top-level navigation (report). Disabling accelerator keys also removes F5/Ctrl+F; accessibility impact is untested (flag to the P5 accessibility evidence). `wry 0.57.1` and `webview2-com ^0.39` versions come from the report and may have moved. Alt+Tab focus bug tauri#15624 (DECISIONS) is a separate P2 check.
- Acceptance: live PC (nachi): packaged build with a real case: F12, Ctrl+Shift+I, right-click menu, Ctrl+P, link to an outside URL, `window.open`, file drop onto the window, and a download link each do nothing. CI: build check that the `devtools` feature is absent (`cargo tree -f "{p} {f}"`). CI: unit test of the navigation predicate (allows `tauri://` and `tauri.localhost`, denies `http://tauri.evil.com`).
- Size: M. Phase: P2.
- Needs owner action? No.

### BL-329 WebView2 runtime, data folder and policy checks
- Outcome: Evergreen runtime is detected, the data folder holds no secrets, and policies that weaken WebView2 are reported.
- Scope: detect Evergreen via the EdgeUpdate registry key (`pv > 0.0.0.0`); per-user data folder `%LOCALAPPDATA%\Unravis\webview2` via `data_directory`; keep default NTFS ACLs; `incognito(true)` and/or clear browsing data on exit; handle `NewBrowserVersionAvailable` (notice or idle restart); read HKLM/HKCU `MSEdgeWebView2` policies (`BrowserExecutableFolder`, `DowngradeVersion`, `UpdatesSuppressed`) and show a read-only warning in the app; no secrets or unencrypted audit data in the folder (checked).
- Out of scope: installing the runtime (installer work in another area), Fixed Version runtime (rejected).
- Depends on: BL-328; P2 (folder, incognito) and P4 (policy warning, installer link).
- Risks / unknowns: DECISIONS puts `webview2` under `%LOCALAPPDATA%\Unravis`; report default is `<identifier>\EBWebView`: the spec must pick one. `incognito(true)` disables persistence, which may clash with UI settings kept in `localStorage` (not checked). Report open item 5: Server 2025 EdgeUpdate/WSUS behaviour is unknown. AV/EDR locking the folder is a DECISIONS open item.
- Acceptance: live PC (nachi): after a real session, search the data folder for the case's known strings and for any key/token material; expect none. live PC (nachi): set a test `DowngradeVersion`/`UpdatesSuppressed` policy value on the PC, see the warning, remove it. CI: unit test of the registry parsing with fake values and of the "Evergreen missing" path.
- Size: M. Phase: P2 and P4.
- Needs owner action? Setting machine policy keys needs UAC (HKLM); HKCU variant needs none.

### BL-330 Go named-pipe server hardening
- Outcome: the Go backend's pipe cannot be squatted, reached remotely, or used by another user.
- Scope: `PIPE_REJECT_REMOTE_CLIENTS`, `FILE_FLAG_FIRST_PIPE_INSTANCE` on the first instance, explicit SDDL for the current logon SID with only read/write data, read attributes and synchronize (never `FILE_GENERIC_WRITE`, never a null DACL), random 128-bit pipe name, peer check with `GetNamedPipeClientProcessId` + session id + `QueryFullProcessImageNameW` while the handle is open, optional impersonation check of token SID.
- Out of scope: the HTTP-over-pipe API itself and attach-or-start (P1 work in the transport area), MCP grants (BL-333).
- Depends on: P1.
- Risks / unknowns: allowed-peer list (which exe paths may connect: desktop, `unravis.exe --mcp`, CLI) is not defined in the report. Path check breaks if the install path moves during an OTA swap; interaction with the update area's file-lock test (DECISIONS item 3). Logon-SID DACL vs "current user" SDDL (DECISIONS) differ slightly; spec decides.
- Acceptance: CI (`windows-2025`): second server instance cannot take the same name; a client with a different logon SID is denied; a connect from a non-allowed image path is dropped; remote-client flag set (read back). live PC (nachi): real run: connect from the desktop and from `unravis.exe --mcp` and see success; from a second Windows user (fast user switch or `runas /user`) see denial. elevated/UAC: `runas`/second account creation may need UAC.
- Size: L (split in plan: server flags, peer check, tests). Phase: P1.
- Needs owner action? UAC/second account only if no standard second user exists on the PC.

### BL-331 Rust pipe client and sidecar launch hardening
- Outcome: Rust only talks to the genuine Go backend and starts it with a restricted child-process policy.
- Scope: client verifies `GetNamedPipeServerProcessId`, server session id, and server image path before sending data; spawn (when none to attach to) from Rust only, inside the Job Object (`KILL_ON_JOB_CLOSE`), with child-process restriction attribute; attach path still verifies the peer; no sidecar launch from JS.
- Out of scope: pipe server (BL-330), Key Keeper pipe client (BL-334), sidecar shutdown RPC.
- Depends on: BL-330; P2 (spawn) and P3 (relay).
- Risks / unknowns: attach-or-start (DECISIONS item 2) means the server may be an older Go started by the browser flow: version/image check rules are not defined. Child-process restriction vs the sidecar spawning helpers: see BL-327 unknown.
- Acceptance: CI (`windows-2025`): a fake server with a wrong image path or from a different session is refused by the client. live PC (nachi): real desktop run: kill the Go process, confirm the Rust host respawns a verified one; start a rogue pipe server with the same name first and confirm the client refuses it.
- Size: M. Phase: P2/P3.
- Needs owner action? No.

### BL-332 Desktop mode without loopback HTTP or `session.json`
- Outcome: in desktop mode no TCP port is opened and no token file is written.
- Scope: the bundled UI talks only through `invoke` + `Channel<Response>` to Rust and then the pipe; desktop mode starts the Go server pipe-only (no listener, no `session.json`); the UI host adapter drops `fetch` to `127.0.0.1`; browser mode keeps loopback unchanged (DECISIONS: browser stays fallback).
- Out of scope: browser-mode loopback hardening, MCP (BL-333), bulk-data benchmark and any token-gated native loopback exception (decided in the P3 benchmark; would be a recorded security exception).
- Depends on: BL-321, BL-330, BL-331; P3 (bundled transport).
- Risks / unknowns: DECISIONS phases this: P2 preview still uses loopback with a one-time launch grant, P3 moves to the pipe; report 02 says remove loopback outright. If binary IPC is too slow (~200 ms per 10 MB, a figure from another report, not report 02) the loopback exception returns. `Channel` streaming limits and cancellation are unproven here.
- Acceptance: live PC (nachi): packaged desktop build with a real case open; `netstat -ano` shows no LISTEN socket owned by the app or Go process; no `session.json` on disk; all case screens load. CI: Go test that desktop-mode start creates no listener and no token file. CI: Playwright with Tauri mocks, no `fetch` to loopback. live PC (nachi): browser mode still works (loopback + token) side by side.
- Size: L. Phase: P3.
- Needs owner action? No.

### BL-333 `unravis --mcp` and CLI over the pipe with grants
- Outcome: MCP and CLI reach the backend over the hardened pipe and no longer read `session.json`.
- Scope: the `--mcp` stdio contract (ten tools) unchanged; its backend link switches to the pipe under BL-330 rules; per-installation revocable grant; read-only MCP endpoint; identifier redaction on by default (`--mcp-allow-identifiers` opt-in); 1 MiB I/O cap; untrusted-output notes (no `{@html}` of MCP data in the UI).
- Out of scope: Official Go MCP SDK parity spike, code mode, new tools (Parallel A and C own those).
- Depends on: BL-330, BL-332; Parallel A.
- Risks / unknowns: MCP host behaviour when the app is closed (explicit headless owner vs offline reader) is decided in the CLI area. Report 02's prompt-injection residual risk is "Medium" for exfiltration via the host agent's other tools; cannot be fixed by Unravis.
- Acceptance: live PC (nachi): Claude Code/Codex MCP host runs the ten tools against a real case through the pipe; same results as the loopback path; with `session.json` deleted/absent it still works; revoke the grant and see calls refused. CI: contract tests with golden outputs for the ten tools; the 1 MiB cap and redaction default.
- Size: L (split: link switch, grants, caps). Phase: Parallel A.
- Needs owner action? No.

### BL-334 Key Keeper stays the secret boundary (verification)
- Outcome: proof that the desktop adds no path to raw secrets.
- Scope: add checks, no new secret store: Rust/Go clients assert Key Keeper is the Session 0 service (server session id and image path); the desktop never calls a secret-returning job; a test greps the UI bundle, Rust crate, WebView2 data folder, logs and crash dumps for planted secrets; document that `tauri-plugin-stronghold`, DPAPI per-user and Credential Manager are not used for secrets.
- Out of scope: any Key Keeper protocol change.
- Depends on: BL-330, BL-329; P3.
- Risks / unknowns: Key Keeper pipe protocol and job list are existing code (not in the reports); read-only job names to be confirmed from the repo. Residual risk from report: same-user malware can read local SQLite data and non-elevated host memory.
- Acceptance: live PC (nachi): connect a real database credential through Key Keeper, use the desktop for a session, then search the data folder, logs, crash dir and process command lines for the password: none found. CI: unit test that the client refuses a pipe server not in Session 0 with the right image. elevated/UAC: Key Keeper service running under `NT SERVICE\UnravisKeyKeeper` for the live check.
- Size: S. Phase: P3.
- Needs owner action? UAC; a real DB credential is the owner's (use an existing one, no new secret created).

### BL-335 Supply-chain CI gates
- Outcome: every PR is checked for known-bad Rust, Go and npm dependencies and unpinned actions.
- Scope: `cargo-deny` (advisories, licenses, bans, sources) with a config; `govulncheck ./...` and `govulncheck -mode=binary`; npm `ignore-scripts=true` + `min-release-age=3` (npm) or pnpm `minimumReleaseAge: 4320` and scripts off; `npm ci`/frozen lockfile; Socket Firewall Free (`sfw`) as an install wrapper if it works in CI; all GitHub Actions pinned by 40-char SHA; no Trivy action; `cargo build --locked`; Go build flags `CGO_ENABLED=0 -mod=readonly -trimpath -buildvcs=false`.
- Out of scope: `cargo-vet`, `cargo-crev` (report: optional, skip crev), `osv-scanner`/`trufflehog` (named in DECISIONS, not in report 02: leave to spec), SBOM (BL-336).
- Depends on: BL-320; P2 (Rust gates), earlier for Go/npm.
- Risks / unknowns: DECISIONS lists `osv-scanner` and `trufflehog`; report 02 does not: decide in spec. Repo uses npm or pnpm? Not stated: find in the repo. Socket Free limits (1,000 scans/month) may run out. `cargo-deny` v0.22.0 number from the report. Existing CI tools may already cover part of this; check `.github/workflows` first.
- Acceptance: CI: a PR that adds a known-vulnerable test crate fails `cargo-deny`; an action referenced by tag fails the pin check; a package younger than the cooldown is not installed; `govulncheck` runs and passes on the real tree. live PC (nachi): none needed.
- Size: M. Phase: P2.
- Needs owner action? No (Socket account sign-up is optional; if wanted, it is an account action for the owner).

### BL-336 SBOM and reproducible builds
- Outcome: each release has one CycloneDX 1.7 SBOM and the Go and Rust binaries rebuild bit-for-bit.
- Scope: `cargo-cyclonedx` for Rust; `cdxgen` or Syft for Go and npm; merge with `cyclonedx-cli`; SBOM attached to the release set (and listed in the signed manifest through the existing release flow); Rust `trim-paths`/`--remap-path-prefix`, `/Brepro`, `/PDBALTPATH:%_PDB%`; Go `-ldflags="-s -w -buildid="`; a CI job builds twice and compares hashes.
- Out of scope: Tauri NSIS/MSI installers (not used: `unravis-setup.exe` stays), SLSA provenance beyond the existing attestations.
- Depends on: BL-335; P4.
- Risks / unknowns: `trim-paths` stable in Cargo 1.100 is a claim from a Medium article (weak source). `actions/attest-build-provenance` needs a public repo or Enterprise Cloud; repo visibility not stated. WebView2 bootstrapper is not in our SBOM scope.
- Acceptance: CI: SBOM validates against the CycloneDX 1.7 schema and lists known Go, npm and Rust components; two builds of `unravis.exe` and `unravis-desktop.exe` on the same runner give equal SHA-256. live PC (nachi): download the real release set and run the existing `verify-release` flow; SBOM present and hashes match. Signing the release uses the owner's release key (existing flow).
- Size: M. Phase: P4.
- Needs owner action? The final live check uses the offline release key passphrase (existing BL-194 flow).

### BL-337 Real-data hostile-content test
- Outcome: proof that hostile strings inside real audit data cannot run script or reach the IPC.
- Scope: a test fixture of hostile values (HTML/script in SQL text, HTTP headers, user agents, Mongo documents, URLs) loaded into a scratch case copy; run the desktop UI over it; check every view and chart tooltip; check external navigation attempts, `invoke` of unlisted commands from the console-equivalent path, and that no secret leaves. Add a regression Playwright test with the same values.
- Out of scope: fuzzing the Go parsers, MCP prompt-injection (BL-333 notes).
- Depends on: BL-322, BL-323, BL-328, BL-332; P3 exit and P4.
- Risks / unknowns: "owner tests with live data" means a scratch copy of a real case plus injected hostile fields (never dummy-only); how to inject into a case copy is not in the reports (find in repo). Report threat table residual: "Low", INFERRED, not tested.
- Acceptance: live PC (nachi): scratch copy of a real case plus the hostile values: no dialog, no navigation, no network call, no CSP violation, text shown as text in all views and tooltips. CI: Playwright regression on the desktop bundle with the hostile fixture; fails on any `securitypolicyviolation`, `dialog` or navigation event.
- Size: M. Phase: P3 (core) with a rerun in P4.
- Needs owner action? No.

## Conflicts with other areas

1. Loopback: report 02 removes it, DECISIONS phases it (P2 loopback with one-time grant, P3 pipe) and the performance area may want a native-only loopback for bulk chart data. BL-332 assumes pipe-only in P3 until the benchmark says otherwise.
2. Tauri floor: report 02 says `>= 2.12.6`, DECISIONS says `>= 2.12.7`.
3. ACG on the Rust host: report sample enables it, DECISIONS list does not, and report open item 4 says untested. BL-326 leaves it off pending a trial.
4. Data folder: DECISIONS moves data to `%LOCALAPPDATA%\Unravis\...`; report 02 uses `<identifier>\EBWebView`, and `incognito(true)` may conflict with saved UI state.
5. Updater/installer area: report 02 says the Go updater stays and the Tauri exe ships inside the signed release; the updater area (BL-149 and later) owns file-lock behaviour. BL-330 path checks must survive OTA swaps. Process-image allowlists and new Tauri files need the release manager (BL-194) to sign them.
6. MCP area (Parallel A) and CLI area (Parallel B) own the pipe protocol and grants; BL-333 only covers the security rules.
7. Code mode (Parallel C) sandbox design is "out of scope" for report 02 (open item 6); BL-327 mitigations for `unravis.exe` depend on the wazero choice there.
8. Performance area: `incognito`, destroy-WebView2-on-hide and EcoQoS interact with BL-329 and BL-328 settings; destroyed webview must reapply all lockdown on recreate.
9. Testing area: Windows-only checks here need the `windows-2025` runner and a Server 2016 box that the owner may not have.

## Questions for spec stage

1. Tauri floor: 2.12.6 or 2.12.7? Confirm current patched release on the day.
2. Refuse or warn on bad WebView2 registry/env flags, and on admin start?
3. Which exe paths may connect to the pipe (desktop, CLI, `--mcp`, tests)? How is that list kept right through OTA swaps?
4. `incognito(true)` or a per-user data folder with clearing on exit? Does the UI store anything in `localStorage` that must persist?
5. Does the UI use `blob:` images, inline `style`, or `{@html}` today? (read the repo)
6. ACG: leave off Rust host and `unravis.exe`, or run the trial? Which wazero mode?
7. Is the repo public (attestations, Socket) and does it use npm or pnpm?
8. Keep `osv-scanner` and `trufflehog` from DECISIONS in addition to the report's gates?
9. Is CET available on Server 2016 targets, or do we only check the PE flag?
10. Should the desktop show WebView2 policy warnings (BL-329) in Help > Updates or a new security panel?
11. Isolation pattern trigger: what npm change counts as "tree grows"?
12. Where does a Server 2016 / Server 2025 test machine come from (VM, cloud, none)?
