# Spec 02: security (Tauri desktop)

Status: SPEC, 2026-11-21. Inputs: groom `02-security.md` (BL-320 to BL-337), `DECISIONS-draft.md`, research report 02, groom files 01 and 03 to 08, and the repo as it is today. No code is written by this file. Nothing here is ticked. New id added: BL-338.

Test labels as in groom: **CI**, **live PC (nachi)**, **elevated/UAC**.

## 1. Purpose and boundaries

Owns:
- The hardened pipe: Go package `internal/pipesec` (listen, peer check, verified dial) and its Rust twin `pipe_peer.rs`.
- Tauri security config: `desktop/src-tauri/tauri.conf.json` security block, `capabilities/`, `build.rs` command manifest, `commands.list`.
- WebView2 lockdown (`webview.rs`, `nav.rs`), startup hardening (`startup.rs`), process mitigations (Rust `mitigations.rs`, Go `internal/harden`).
- CSP and the UI changes the CSP forces (charts tooltips, inline styles).
- Supply-chain gates and SBOM (policy and config files, workflow `.github/workflows/supply-chain.yml`).
- The rule set for what is allowed to leave the desktop: no loopback port, no `session.json`, no secrets.

Does not own (we only set rules for them):
- Pipe protocol, attach-or-start, locator, shutdown RPC, tray, supervisor, relay command bodies: area 01 (BL-301 to BL-318). BL-301 must call `pipesec.Listen`; it does not write its own listener.
- MCP grants, disclosure tiers, audit: area 03 (BL-343 to BL-354). We give the MCP pipe and the peer rules.
- Updater, installer, Authenticode (BL-312), WebView2 install: area 06. Key Keeper protocol: unchanged.
- Code-mode sandbox: area 04. Perf budgets: area 07. Test harness and `windows-2025` job layout: area 08.
- Browser mode loopback: stays as is.

What the code says today (differences from the report):
- `internal/server/server.go:218` already sends a CSP with `style-src 'unsafe-inline'`, `img-src data:`, `font-src data:`. Browser mode keeps it. The desktop CSP is stricter and separate.
- `go.mod` has no `go-winio` and no `wazero` (no `tetratelabs` in `go.sum`). `ncruces/go-sqlite3 v0.35.6` with `go-sqlite3-wasm` is indirect. So the report's "wazero JIT breaks ACG" may not apply; it is a spike (BL-327), not a given.
- `internal/keeper/pipe_windows.go` already has a proven overlapped pipe listener (`FILE_FLAG_FIRST_PIPE_INSTANCE`, `PIPE_REJECT_REMOTE_CLIENTS`, SDDL, `ImpersonateNamedPipeClient`). Its server reports `Peer() (pid, session)`. Its client does NOT check the server's PID, session or image (no `GetNamedPipeServerProcessId` anywhere in repo). Pipe name is `\\.\pipe\UnravisKeeper` (`internal/datadir/datadir.go:36`), service name `UnravisKeeper` (`internal/keeper/owner.go`), not `UnravisKeyKeeper` as in report and groom.
- `unravis.exe` (`cmd/audit-explorer`) spawns processes: Explorer (`internal/server/open_folder_windows.go`), setup for updates (`internal/update/launch_windows.go`), tray app (`cmd/audit-explorer/trayapp.go`), browser. So the report's "child process restriction on the sidecar" would break it. Not applied to `unravis.exe`.
- `session.json` (`internal/mcp/sessionfile.go`): `{port, token, pid}` in `%APPDATA%\APIAuditExplorer`. Browser mode only after this work.
- Charts: `web/src/lib/charts.ts` tooltips use HTML-string `formatter` (with `<br/>` and `escapeHTML`) and `extraCssText` (22 uses of `extraCssText`/`appendTo` in `web/src`); the renderer is already canvas (`CanvasRenderer`, `Charts.svelte`). `{@html}` count: 0. No `innerHTML` in `web/src`. 66 `style=`/`style:` lines in `.svelte` (top: `TracingChart` 23, `BlackboxCharts` 8, `CallTrace` 7, `CallLanes` 5). `chartZoom.ts` sets `style.cssText` (allowed by CSP, it is CSSOM).
- `localStorage` keys exist (active case id, call target, case view, theme, timezone, guided mode per case id, banners dismissed). They hold ids and preferences, not case content.
- Exports and downloads use `URL.createObjectURL` + anchor download (`ExportPanel.svelte`, `DailyTable.svelte`, `ReportExport.svelte`, `blackbox.ts saveBlob`). `on_download(false)` would break them. See BL-338.
- File drop import exists (`dropEntries.ts`, `ImportFilesPanel.svelte`, `SourceStep.svelte`). Turning external drop off would break import. See BL-328.
- Existing CI (`.github/workflows/ci.yml`) runs on `windows-2022`, Node 22, `npm ci`, already pins every action by SHA. `package-lock.json` exists (npm, not pnpm). No `dependabot.yml`, no `.cargo/`, no `deny.toml`.
- `release.yml` already calls `actions/attest-build-provenance` (line 283). Repo visibility is not visible from the repo; see 2.10.

## 2. Resolved decisions

| # | Question or conflict | Decision | Reason |
|---|---|---|---|
| 2.1 | Tauri floor (2.12.6 vs 2.12.7) | Floor `2.12.7`, kept in `desktop/security/floor.toml` (`tauri = "2.12.7"`, `rust = "1.90"`). The lock may be higher, never lower. `cargo-deny` advisories job catches newer advisories on the day. | DECISIONS is newer; one file to change later. |
| 2.2 | Refuse or warn: env flags | Strip `WEBVIEW2_*` env silently, log one line (names only). Never refuse. | Env is process-local, stripping is a full fix. |
| 2.3 | Refuse or warn: registry flags | Dangerous flag in HKCU or HKLM `...\Edge\WebView2\AdditionalBrowserArguments` (value `unravis-desktop.exe` or `*`): REFUSE, exit code 78, native `MessageBoxW` (no dialog plugin), text says "use the browser mode". Other values: start, show warning in security panel. | A debug port is full compromise; browser mode stays available. |
| 2.4 | Admin start | Rust refuses elevated start, exit code 77, same message box. Setup's elevated worker never starts the desktop exe. | Matches DECISIONS section 3 and WebView2 guidance. |
| 2.5 | Dangerous flag list | `--remote-debugging-port`, `--remote-debugging-pipe`, `--remote-allow-origins`, `--auto-open-devtools-for-tabs`, `--disable-web-security`, `--no-sandbox`, `--disable-gpu-sandbox`, `--allow-running-insecure-content`, `--disable-site-isolation-trials`, `--user-data-dir`, `--load-extension`, `--inspect`. Match by name, case-insensitive, with or without `=value`. | Constant `DANGEROUS_FLAGS` in `startup.rs`, unit tested. |
| 2.6 | Pipe count | Two pipes, same Go process: UI pipe `unravis.ui.<32hex>` and MCP pipe `unravis.mcp.<32hex>`. Full name `\\.\pipe\unravis.<kind>.<32 lower-hex>` (128-bit random). MCP pipe serves only the read-only dispatcher and needs a grant (area 03). | Smaller agent-facing surface; decides group 03 conflict 4. DECISIONS drawing "same pipe, own grant" is superseded. |
| 2.7 | Pipe DACL | One SDDL for both: `D:P(A;;0x100087;;;<logonSID>)(A;;0x20000;;;OW)`. The OWNER RIGHTS entry (READ_CONTROL only) removes the owner's implicit WRITE_DAC, so the same user in another logon session cannot rewrite the DACL; a test proves `WRITE_DAC` is denied. Mask 0x100087 = read data, write data, create pipe instance, read attributes, synchronize. No Everyone, no network, no null DACL, no `FILE_GENERIC_WRITE`. `nMaxInstances = 16`, all 16 created at start and reused with `DisconnectNamedPipe` (never closed while serving). | Deviation from report (it forbids 0x4): the server and clients run as the same SID, so they cannot be told apart by DACL. Squatting is stopped by `FILE_FLAG_FIRST_PIPE_INSTANCE` plus a full instance pool: a second `CreateNamedPipe` returns `ERROR_PIPE_BUSY`. Test proves it. Logon SID (not user SID) keeps other logon sessions of the same user out. |
| 2.8 | Peer allow-list | Server allows a client only if all hold: same logon SID (token), same session id, image path (from `QueryFullProcessImageNameW`, normalised, case-insensitive) is in the server's own install dir and base name is in `{unravis.exe, unravis-desktop.exe, unravis-core.exe}`. Install dir = dir of the running server exe. No path list on disk, nothing to keep right through OTA; setup stops all clients before the file swap. Test-only extra paths via build tag `citest` and `pipesec.TestAllow(path)`; absent in release (scanned, see 6). Publisher check is added after BL-312 (Authenticode) and is not claimed now. | Honest limit: against same-user malware that can write into a per-user install dir this is a tripwire, not a boundary. Real boundaries: DACL, remote reject, first instance, instance pool. |
| 2.9 | Who connects to which pipe | UI pipe: `unravis-desktop.exe` and `unravis.exe` (CLI, `--mcp` bridge for attach). MCP pipe: `unravis.exe --mcp` only (+grant). Rust client verifies the server too: PID, session id, image path equals expected sidecar path from the locator AND lies in the install dir. | Both ends checked, closes the gap seen in the keeper client. |
| 2.10 | Repo public? npm or pnpm? | npm (lockfile is `web/package-lock.json`). Visibility unknown: assume PRIVATE. Attestation step stays as in `release.yml`; no new job depends on it. Socket (`sfw`) is NOT used (account needed, 1,000 scans a month cap). Replacement: `ignore-scripts=true`, `min-release-age=3`, `npm ci`, osv-scanner. | Free and no owner action; revisit if repo is public. |
| 2.11 | osv-scanner and trufflehog | Keep both, as one job `supply-chain.yml`, both pinned by SHA. osv-scanner reads `go.sum`, `desktop/src-tauri/Cargo.lock`, `web/package-lock.json`. trufflehog runs on PR diff only, `--results=verified,unknown`. | Cheap, cover three ecosystems and secrets; groom conflict 1: BL-335 owns policy files and `supply-chain.yml`; area 08 BL-449 may call it. |
| 2.12 | Data folder and `incognito` | Data folder `%LOCALAPPDATA%\Unravis\webview2` (matches BL-316). NOT `incognito`. On exit and on window destroy: `ClearBrowsingData` for everything except local storage (cache, cookies, IndexedDB, service workers, download history). Local storage keeps only the key allow-list (see BL-329). | `incognito` drops UI state (active case, theme, timezone) every launch. Values are ids and preferences only (checked in repo). |
| 2.13 | ACG (arbitrary code guard) | OFF on `unravis-desktop.exe` and on `unravis.exe` by default. Const `acg` per profile in `internal/harden`. Keeper and Blackbox turn it on only if spike BL-327 passes. Decided by spike BL-327. | No data; wazero is not even linked (see 1). |
| 2.14 | Child-process restriction | NOT on `unravis.exe` (spawns Explorer, setup, tray). Allowed trial on Keeper, Blackbox, BlackboxSql (no `exec` in their `cmd/`). Rust-spawned sidecar gets creation-time mitigation policy only (strict handle, image load, extension points). | Code differs from report. |
| 2.15 | CET on Server 2016 | Set `/CETCOMPAT` and check the PE flag only. Server 2016 has no CET support; flag is ignored there. Nothing else claimed. | Not testable; flag is harmless. |
| 2.16 | Where policy warnings show | A read-only "Security notices" block in the existing update/about area (where `updateState.svelte.ts` banners live). Component `web/src/lib/components/SecurityNotices.svelte`, data from command `get_security_status`. No new screen. | Smallest change. |
| 2.17 | Isolation pattern trigger | Switch to `pattern.use = isolation` when ANY: prod `dependencies` in `web/package.json` exceed 5 (today 1: `echarts`), a prod dependency has an install script, or a dependency added that touches `window.__TAURI_INTERNALS__`. CI script `tools/security/check-isolation-trigger.mjs` fails the PR with that message. | Measurable. |
| 2.18 | Test machines | Server 2022 and 2025: `windows-2022` and `windows-2025` runners. Server 2016 and Win 10 22H2: no box assumed; those acceptances are labelled "not verified" until one exists. | Owner has none confirmed. |
| 2.19 | Runner for new desktop jobs | `windows-2025` (new files). Existing `ci.yml` stays `windows-2022`. | Report 08 and groom 08. |
| 2.20 | Loopback phasing | P1: Go `--pipe-only` flag exists. P2 preview: desktop may load the loopback UI, token only in the launch fragment as today; `session.json` is not written by the desktop-started Go (it runs `--pipe-only` plus a P2-only `--preview-loopback` flag owned by area 01 that opens one loopback listener and returns the grant over the pipe). P3: `--preview-loopback` removed; desktop is pipe-only. Bulk exception needs spike BL-313 first. Until then none. Browser-mode Go (started by browser flow) still listens and writes `session.json`. | Resolves conflict 1 and groom 08 conflict 11. "No listener, no file" applies to a process started with `--pipe-only`. |
| 2.21 | Bulk loopback exception (if BL-313 fails budget) | Written as ADR, only these terms: 127.0.0.1 random port, one per launch, 256-bit token in `Authorization`, `Host` and `Origin` check, GET only on opaque handle ids, 60 s idle close, owned by Rust not Go. Not built now. | Pre-decided so area 07 does not wait. |
| 2.22 | Locator name | `%LOCALAPPDATA%\Unravis\run\backend.json` (pipe names, pid, start time, exe path, version). No token field. Area 01 BL-302 must use this name, not `session.json`. | Avoids two files with one name and a false "no session.json" test result. |
| 2.23 | Plugins | Zero official plugins incl. `single-instance` and `deep-link`. Single instance by own named mutex (BL-309). | Matches DECISIONS; resolves 01 conflict. |
| 2.24 | `freezePrototype` and Trusted Types | OFF in P2/P3 builds. Decided by spike BL-324; default stays off. | No data. |
| 2.25 | File drop | Tauri drop handler off (`disable_drag_drop_handler`); HTML5 drop to page stays on so import works; the page cancels `dragover`/`drop` outside the import zone (`desktop-guards.ts`). Navigation to `file:` is blocked by `on_navigation`. | Real data path (import) must work; report's `SetAllowExternalDrop(false)` would remove it. |
| 2.26 | Downloads | `on_download` returns false. Exports go through new command `export_save` (BL-338): native Save As, Rust writes the file. | Keeps downloads off, keeps exports. |
| 2.27 | ECharts tooltips | `renderMode: 'richText'`. Formatters return plain text with `\n`, no HTML, no `extraCssText`. | Needed for strict `style-src`. |
| 2.28 | Dynamic styles | Static `style="..."` in templates move to classes. Dynamic `style:prop={v}` and `style={fn()}` stay if the Playwright CSP run shows no violation (they set CSSOM properties). If violations appear, convert to CSS custom property set via `el.style.setProperty` in an action. Decided by the BL-323 test run. | Avoids rewriting 66 sites blindly. |
| 2.29 | `core:` permissions | Start with none. Tray, window destroy and recreate run in Rust, not from JS. Add a `core:` permission only with a failing test that proves need. Test `capability_has_no_core_default`. | Smallest authority. |
| 2.30 | Server 2016 CET/ACG, Edge update servers | Not claimed. WebView2 on Server 2025 with WSUS: warn only via BL-329 policy read. | No data. |

## 3. Design

### 3.1 Pipe security (Go): `internal/pipesec`

Files: `names.go`, `listen_windows.go`, `listen_other.go` (returns `ErrUnsupported`), `sddl_windows.go`, `peer_windows.go`, `dial_windows.go`, `policy.go`, `testallow_citest.go` (build tag `citest`), tests `*_windows_test.go`.

API (exact):
```go
package pipesec
type Kind string // "ui" | "mcp"
func NewName(kind Kind) (string, error)          // \\.\pipe\unravis.<kind>.<32 hex>
type Peer struct{ PID, Session uint32; Image, UserSID, LogonSID string }
type PeerPolicy struct{ InstallDir string; AllowedBase []string; RequireSameLogon bool }
func DefaultPeerPolicy() PeerPolicy              // InstallDir = dir of os.Executable()
type ListenConfig struct{ Name string; Policy PeerPolicy; MaxInstances int /*default 16*/ }
func Listen(cfg ListenConfig) (net.Listener, error)   // Accept returns *Conn; denied peers are closed and counted, never returned
type Conn interface{ net.Conn; Peer() Peer }
type ServerPolicy struct{ ExpectedImage string; InstallDir string; ExpectedSession uint32 }
func DialVerified(ctx context.Context, name string, sp ServerPolicy) (net.Conn, Peer, error)
var ErrPeerDenied, ErrServerMismatch, ErrPipeBusy error
func Metrics() (denied uint64)                   // for logs and tests
```
Listen steps: build SDDL from current token logon SID (group with `SE_GROUP_LOGON_ID`); create instance 1 with `FILE_FLAG_FIRST_PIPE_INSTANCE|FILE_FLAG_OVERLAPPED`, mode `PIPE_TYPE_BYTE|PIPE_READMODE_BYTE|PIPE_WAIT|PIPE_REJECT_REMOTE_CLIENTS`, `nMaxInstances=16`; create instances 2..16; on connect call `GetNamedPipeClientProcessId`, `GetNamedPipeClientSessionId`, `OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION)`, `QueryFullProcessImageNameW`; impersonate (`ImpersonateNamedPipeClient` on a `LockOSThread` goroutine, `OpenThreadToken`, read user SID and logon SID, `RevertToSelf` in `defer`). Any failure closes the instance, increments `denied`, and the instance is `DisconnectNamedPipe`d and reused. Process handle stays open until the checks end (it is opened after the PID is read, so a tiny PID-reuse window remains before `OpenProcess`; same-logon attackers are a tripwire case anyway). `ImpersonateNamedPipeClient` fails with `ERROR_CANNOT_IMPERSONATE` until the client has sent data, so the check before the first read falls back to the client process token. Reuse the proven overlapped pattern in `internal/keeper/pipe_windows.go`; do not import it (keeper stays untouched; dedupe is a later refactor, noted in 7).

HTTP over the pipe is area 01's: `http.Server{Handler: h}.Serve(pipesec listener)`; handler reads `Peer` from the conn via `ConnContext` for audit lines.

Keeper client (BL-334): add `internal/keeper/serververify_windows.go`: after dial, `GetNamedPipeServerProcessId` + `GetNamedPipeServerSessionId`; require session 0 and image equal to `<install dir>\unravis-keeper.exe` (name from the keeper cmd; confirm in repo at build time). No protocol change. Setting `datadir.DefaultPipe` stays.

### 3.2 Pipe client (Rust): `desktop/src-tauri/src/pipe_peer.rs`
`pub fn connect_verified(name:&str, exp:&ServerExpect) -> Result<NamedPipeClient, PipeError>`. Uses tokio `ClientOptions` with `security_qos_flags(SECURITY_IDENTIFICATION)` (no impersonation by server), then `GetNamedPipeServerProcessId`, `GetNamedPipeServerSessionId`, `OpenProcess` + `QueryFullProcessImageNameW`. `ServerExpect {image: PathBuf, install_dir: PathBuf, session: u32}`. Errors map to the error codes below. The relay (BL-314) must call only this function.

### 3.3 Startup (Rust): `startup.rs`
Order in `main()`: (1) `unsafe { env::remove_var }` for the 4 `WEBVIEW2_*` names, single-threaded, before anything; (2) token elevation check (`TokenElevation`, integrity >= High, Administrators membership); (3) registry scan; (4) `mitigations::apply()`; (5) build Tauri. Refusal returns `StartupRefusal { code: u32, id: &'static str, text: String }`; ids: `ELEVATED` (77), `WV2_DEBUG_POLICY` (78). Reads go through trait `RegistryRead` so tests use a fake.

Registry paths read (HKCU then HKLM, 64 and 32 view): `SOFTWARE\Policies\Microsoft\Edge\WebView2\AdditionalBrowserArguments` values `unravis-desktop.exe`, `*`. Policy warnings (BL-329, read only): `SOFTWARE\Policies\Microsoft\Edge\WebView2` values `BrowserExecutableFolder`, `ReleaseChannels`, `ChannelSearchKind`, `DowngradeVersion`; `SOFTWARE\Policies\Microsoft\EdgeUpdate` `UpdatesSuppressed*` and `Update{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}`. Names come from the report; the live check confirms them on a real PC.

### 3.4 Window lockdown (Rust): `webview.rs`, `nav.rs`
Single constructor `pub fn build_main_window(app:&AppHandle) -> tauri::Result<WebviewWindow>`. Area 01 BL-308 (destroy/recreate) must call it for every creation; no other `WebviewWindowBuilder` in the crate (CI grep test `single_window_constructor`). Applies: label `main`, `devtools(false)`, `data_directory(%LOCALAPPDATA%\Unravis\webview2)`, `on_navigation(nav::is_allowed)`, `on_new_window` deny, `on_download` false, `disable_drag_drop_handler()`, `browser_extensions_enabled(false)`, `general_autofill_enabled(false)`, `zoom_hotkeys_enabled(false)`, `use_https_scheme(true)`, then `with_webview` COM: `SetAreDevToolsEnabled(false)`, `SetAreDefaultContextMenusEnabled(false)`, `SetIsStatusBarEnabled(false)`, `SetAreHostObjectsAllowed(false)`, `SetAreDefaultScriptDialogsEnabled(false)`, `SetIsBuiltInErrorPageEnabled(false)`, `SetIsPasswordAutosaveEnabled(false)`, `SetIsZoomControlEnabled(false)`, `SetIsPinchZoomEnabled(false)`, and `SetAreBrowserAcceleratorKeysEnabled(false)` only if the accessibility check in BL-328 passes (see there). Also registers `NewBrowserVersionAvailable` -> sets a flag read by `get_security_status`.

`nav::is_allowed(url:&Url)->bool`: true only for scheme `tauri` with host `localhost`, or scheme `http`/`https` with host exactly `tauri.localhost`. Everything else false (`about:blank` too).

### 3.5 Command gate: `commands.list`, `build.rs`, `capabilities/main-windows.json`
- `desktop/src-tauri/commands.list`: one command name per line. Any area adding a command appends here in the same PR. Initial: `get_security_status`, `export_save`. Area 01 adds `api_request`, `api_stream`, `api_cancel`, `app_info` (names theirs; format is the rule).
- `build.rs` reads the file and calls `tauri_build::Attributes::new().app_manifest(AppManifest::new().commands(&[...]))`.
- Capability (generated checks, hand-written file):
```json
{ "identifier": "main-windows", "description": "main window, local only",
  "local": true, "windows": ["main"], "platforms": ["windows"],
  "permissions": ["allow-get-security-status", "allow-export-save"] }
```
One `allow-<name-with-dashes>` per line in `commands.list`. No `remote`, no `webviews`, no `core:default`.
- `tauri.conf.json` security: `withGlobalTauri:false`, `pattern.use:"brownfield"`, `assetProtocol.enable:false`, `dangerousDisableAssetCspModification:false`, `capabilities:["main-windows"]`, `headers` (`X-Content-Type-Options: nosniff`, `Cross-Origin-Opener-Policy: same-origin`; `Cross-Origin-Embedder-Policy` only if the BL-322 first run loads clean, else omitted and noted in the ADR), `freezePrototype` absent.
- Production CSP (keys exact):
```
default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; font-src 'self';
connect-src ipc: http://ipc.localhost https://ipc.localhost; object-src 'none'; base-uri 'none';
form-action 'none'; frame-ancestors 'none'
```
`img-src` has no `blob:` (no repo use as image; blob URLs are only for downloads). `devCsp` = same + `style-src 'self' 'unsafe-inline'` + `connect-src` adds `ws://127.0.0.1:5173 http://127.0.0.1:5173`. Which ipc origin is real (http vs https) is read from the P2 build and the unused one is dropped by the lint.
- Config lint: `tools/security/lint-tauri-config.mjs` (Node, no deps) reads `tauri.conf.json` and `capabilities/*.json`; rules in BL-322. Fixtures under `tools/security/testdata/`.

### 3.6 Process mitigations
Rust `mitigations.rs`, `pub fn apply() -> Result<Applied>` via `SetProcessMitigationPolicy`: strict handle check, image load (no remote, no low label, prefer system32 = 0x7), extension point disable. Link flags in `desktop/src-tauri/.cargo/config.toml` for `x86_64-pc-windows-msvc`: `-C control-flow-guard`, `-C link-arg=/CETCOMPAT`, `/DYNAMICBASE`, `/HIGHENTROPYVA`, `/NXCOMPAT`. Honest note: runtime policies start after the process is up; weaker than creation-time. So the Rust spawn of the sidecar (BL-331) sets `PROC_THREAD_ATTRIBUTE_MITIGATION_POLICY` at creation for the Go child. ACG not set (2.13).

Go `internal/harden`:
```go
type Profile int // User, Service, Keeper
func Apply(p Profile) (Report, error)   // windows; no-op elsewhere
func ReadBack() (Report, error)
type Report struct{ StrictHandle, ImageLoad, ExtensionPoints, FontDisable, ACG bool }
```
Profile sets: User (`unravis.exe`): strict handle, extension points, image load, font disable; no ACG, no child restriction. Service (Blackbox, BlackboxSql): same + child restriction trial. Keeper: same + child restriction trial + ACG if spike passes. Called as the first statement in each `main()` (`cmd/audit-explorer/main.go`, `cmd/unravis-keeper/main.go`, `cmd/unravis-blackbox/main.go`). Apply failure of one policy logs and continues for User; for Service/Keeper it is fatal only in test builds (a failing mitigation must not stop a service on a customer machine).

### 3.7 Data shapes
`get_security_status` (Tauri command, read only, no args):
```json
{"schema":1,"elevated":false,"webview2":{"runtime":"present","version":"1xx.x.x.x","restartAdvised":false},
 "mitigations":{"cfg":true,"strictHandle":true,"imageLoad":true,"extensionPoints":true},
 "notices":[{"id":"WV2_DOWNGRADE_POLICY","severity":"warn","scope":"HKLM","value":"1"}]}
```
Notice ids: `WV2_RUNTIME_MISSING`, `WV2_DOWNGRADE_POLICY`, `WV2_UPDATES_SUPPRESSED`, `WV2_EXEC_FOLDER_POLICY`, `WV2_ARGS_POLICY_OTHER`, `WV2_RESTART_ADVISED`. UI text lives in `web/src/lib/copy/` with the existing copy files (plain words).

`export_save` (BL-338): invoke with raw body (`InvokeBody::Raw`) and headers `x-file-name` (hint, sanitised to a base name), `x-mime`; Rust shows `IFileSaveDialog`, writes the bytes to the chosen path with `CREATE_NEW` or confirmed overwrite from the OS dialog, returns `{"saved":true,"name":"<base name>"}` or `{"saved":false}` on cancel. Cap 512 MiB. The path is never accepted from JS.

Error codes seen by relay and UI (stable strings): `PIPE_PEER_DENIED`, `PIPE_SERVER_MISMATCH`, `PIPE_BUSY`, `PIPE_GONE`, `LAUNCH_REFUSED`.

### 3.8 Main flows
1. Start: `main()` strip env -> elevation check -> registry scan -> mitigations -> single-instance mutex (area 01) -> read `run\backend.json` -> `connect_verified` (attach) or spawn sidecar (Job Object, creation mitigations, `--pipe-only`) and wait for the locator -> `build_main_window` -> UI calls `invoke`.
2. Request: UI `invoke('api_request')` -> Runtime Authority checks capability -> command -> `pipe_peer` -> UI pipe -> Go `pipesec` peer check -> HTTP mux.
3. MCP: `unravis.exe --mcp` reads `run\backend.json`, dials MCP pipe (verified), presents grant (area 03).
4. Export: UI builds bytes -> `export_save` -> OS dialog -> file.

### 3.9 Failure modes and what the user sees
- Debug flags policy found: message box "Unravis Desktop did not start. A Windows setting lets other programs control the web view. Use Unravis in your browser instead, or ask your administrator to remove the setting." Exit 78. Browser mode is untouched.
- Elevated: message box "Unravis Desktop does not run as administrator. Start it from a normal window." Exit 77.
- WebView2 runtime missing: window shows the plain-HTML native fallback text built into Rust (message box with the download link text, no navigation) and `WV2_RUNTIME_MISSING`; installer area owns install.
- Peer denied by server: client sees `PIPE_PEER_DENIED`; UI shows "Unravis could not connect to its own engine safely" with a Retry; log line has PID and image path (no case data).
- Server mismatch on dial: client refuses, does not send a byte, shows the same text and does not respawn more than once (supervisor circuit breaker, area 01).
- All 16 instances busy: `PIPE_BUSY`, client waits up to 5 s with `WaitNamedPipe`, then error.
- Mitigation fails to apply (Rust): log and continue; `get_security_status.mitigations.<x>=false`; notice shown. Not fatal.
- CSP violation at run time: logged by the page via `securitypolicyviolation` listener to the local log (`clientLog.ts`) so live runs can see it; no telemetry.

## 4. Interfaces other areas rely on (exact names)

Go:
- `api-audit-explorer/internal/pipesec`: `NewName`, `Listen`, `ListenConfig`, `PeerPolicy`, `DefaultPeerPolicy`, `Conn.Peer()`, `DialVerified`, `ServerPolicy`, `ErrPeerDenied`, `ErrServerMismatch`, `ErrPipeBusy`. Kinds `ui`, `mcp`. Pipe path format `\\.\pipe\unravis.<kind>.<32 lower hex>`.
- `api-audit-explorer/internal/harden`: `Apply(Profile)`, `ReadBack()`, profiles `User`, `Service`, `Keeper`.
- Flag `--pipe-only` on `unravis.exe` (owner area 01 BL-301): no TCP listener, no `mcp.WriteSessionFile`. Security test `TestPipeOnlyNoListenerNoSessionFile` lives in `cmd/audit-explorer`.
- Locator: `%LOCALAPPDATA%\Unravis\run\backend.json`, current-user-only ACL, fields `{"schema":1,"ui":"<pipe path>","mcp":"<pipe path>","pid":n,"startedAt":"RFC3339","exe":"<abs path>","version":"x.y.z"}`. No token.
- Allowed client image base names: `unravis.exe`, `unravis-desktop.exe`, `unravis-core.exe` (kept even if the BL-305 spike keeps one exe).
- `internal/keeper`: new `VerifyServer(conn)` used by all keeper clients (BL-334).

Rust (crate `unravis-desktop`, dir `desktop/src-tauri`):
- Modules `startup`, `mitigations`, `webview` (`build_main_window`), `nav` (`is_allowed`), `pipe_peer` (`connect_verified`, `ServerExpect`, `PipeError`), `security_status`, `export`.
- `commands.list` (one name per line) + capability `main-windows`. Rule: a command exists only if it is in `commands.list`, in `generate_handler!`, and in the capability. Test `commands_match_manifest_and_capability`.
- No `WebviewWindowBuilder` outside `webview.rs`.
- No `std::process::Command` outside `sidecar.rs` (area 01); CI grep test `spawn_only_in_sidecar`.

Files other areas must not edit without us: `tauri.conf.json` security block, `capabilities/`, `build.rs`, `webview.rs`, `startup.rs`, `deny.toml`, `.npmrc`, `desktop/security/floor.toml`. Others add lines to `commands.list` and the capability list only.

Web:
- `web/vite.desktop.config.ts` (output `web/dist-desktop`), `web/src/lib/desktop-guards.ts`, `web/src/lib/components/SecurityNotices.svelte`, `web/src/lib/chartTooltip.ts` (helper that builds richText strings). Browser build config unchanged.

CI:
- `.github/workflows/supply-chain.yml` (BL-335), `.github/workflows/desktop-security.yml` (BL-320 to BL-328 Windows jobs, `windows-2025`), scripts in `tools/security/`.

## 5. Per-item build notes

Order inside the area: BL-335 (Go and npm parts first) -> BL-330 -> BL-327 spike -> BL-320 -> BL-325 -> BL-326 -> BL-321 -> BL-322 -> BL-328 -> BL-338 -> BL-331 -> BL-329 (folder, clear on exit) -> P3: BL-332 -> BL-323 -> BL-337 -> BL-324 -> BL-334 -> P4: BL-327 services, BL-329 warnings, BL-336 -> Parallel A: BL-333.

### BL-335 Supply-chain CI gates (P2, Go and npm earlier)
- Add: `.github/workflows/supply-chain.yml` (jobs `cargo-deny`, `govulncheck`, `osv`, `secrets`, `pins`, `npm-policy`), `desktop/src-tauri/deny.toml` (advisories deny, licenses allowlist from first resolved tree, bans: `tauri-plugin-*` and `openssl-sys` deny, sources crates.io only), `web/.npmrc` (`ignore-scripts=true`, `min-release-age=3`), `tools/security/check-action-pins.mjs` (every `uses:` is `@<40 hex>`, no `aquasecurity/`), `.github/dependabot.yml` (gomod, cargo, npm, github-actions; grouped, no auto-merge).
- Go flags in existing release build scripts: confirm `CGO_ENABLED=0 -mod=readonly -trimpath -buildvcs=false` in `build.ps1`/`release.yml`; add what is missing. `govulncheck ./...` and `govulncheck -mode=binary` on built `unravis.exe`.
- npm: CI step prints `npm config get min-release-age` and fails on `undefined` (Node 22 ships an npm that may ignore it; step installs a pinned newer npm if needed). First run proves `npm ci --ignore-scripts` still builds Vite 8 (no postinstall needed); if not, fix by listing the one allowed script, never by turning the rule off.
- Tests: `TestActionPins` (node --test, fixture with a tag-pinned action must fail), CI job `cargo-deny` run on a branch with a doctored `Cargo.toml` adding a crate with a known advisory (kept as a script `tools/security/prove-deny.ps1`, run on demand and recorded once in the PR).
- Acceptance (runnable): `node tools/security/check-action-pins.mjs` exits 0 on repo and 1 on `testdata/bad-workflow.yml`; `cargo deny --manifest-path desktop/src-tauri/Cargo.toml check` exits 0; `govulncheck ./...` exits 0; `npm ci` in `web` with a package younger than 3 days is refused (`prove-cooldown.ps1`); osv-scanner exits 0 on the three lockfiles; trufflehog finds a planted fake key in a test branch. No live PC step.

### BL-330 Go named-pipe hardening (P1)
- Add `internal/pipesec/*` (3.1). Wire in area 01 BL-301: replace its listener.
- Tests (Windows, `windows-2025`): `TestFirstInstanceWins`, `TestSecondCreateIsBusy` (pool full: same-user `CreateNamedPipe` gets `ERROR_PIPE_BUSY`), `TestRemoteClientsRejectedFlagReadBack`, `TestDaclNoEveryone` (read DACL back, assert single ACE, mask 0x100087, no `WD`/`AN`/`NU`), `TestOtherLogonDenied` (second local user via `LogonUser` in the test; skip with clear message if the runner blocks it, then live PC), `TestImageNotInInstallDirDenied`, `TestImageNameNotAllowedDenied`, `TestPeerHandleHeldDuringCheck`, `TestRandomNameShape`, `TestOtherBuildStubs`.
- Order: names -> sddl -> listen -> peer -> dial -> tests.
- Acceptance: `go test ./internal/pipesec/... -run . -count=1` green on `windows-2025`; live PC (nachi): real run, `unravis.exe --pipe-only` serves, desktop and `unravis.exe --mcp` connect, `runas /user:<second user>` client gets access denied. Elevated step only if no second standard user exists.

### BL-327 Go mitigations and ACG decision (P1 spike, P4 services)
- Add `internal/harden/harden_windows.go`, `harden_other.go`, `harden_windows_test.go`; call sites in four `main()`s.
- Spike (P1): build `unravis.exe` with ACG on in a scratch profile, run `go test ./...` Windows subset plus a real case open, MCP and CLI run; record Go version and ncruces version; repeat with `UNRAVIS_SQLITE_MODE` unset (only one mode exists today, since wazero is absent; write that down). Result goes to `docs/adr/` (`0xx-acg.md`). Pass -> set const `acgUser=true` only for Keeper/Blackbox (never `unravis.exe` unless it also passes the 24 h soak by area 08).
- Tests: `TestApplyUserProfileReadBack`, `TestApplyIsIdempotent`, `TestServiceProfileFailureNotFatalInProd`.
- Acceptance: CI as groom; live PC (nachi): `unravis.exe` opens a real case, runs CLI and MCP; elevated/UAC: upgrade the three services with the hardened build, all three start, Keeper answers a read job, Blackbox recording readable. Rollback path = existing OTA rollback.

### BL-320 Pin Tauri and Rust (P2)
- Add: `desktop/security/floor.toml`, `desktop/src-tauri/Cargo.toml` (`tauri = "=2.12.7"` style caret allowed up to next minor only after release-notes check: use `"~2.12.7"`), `rust-toolchain.toml` (`channel = "1.90.0"`), committed `Cargo.lock`, `tools/security/check-floor.mjs` (parses `Cargo.lock` and `floor.toml`; exit 1 if `tauri` below floor or `rust-version` below).
- CI: `cargo build --locked --release` on `windows-2025`; `cargo tree -i tauri` printed; step runs `cargo deny check advisories`.
- Tests: `node --test tools/security/check-floor.test.mjs` with doctored lock fixtures.
- Acceptance: as groom; runnable: `node tools/security/check-floor.mjs desktop/src-tauri/Cargo.lock` -> 0; with `testdata/Cargo.low.lock` -> 1.

### BL-325 Startup hardening (P2)
- Add `startup.rs`, `registry.rs` (trait `RegistryRead`), tests in `startup.rs`.
- Tests: `strips_webview2_env`, `dangerous_flag_matrix` (each of 2.5, `=value`, case, `*` and exe key, HKCU and HKLM), `benign_flag_warns_not_refuses`, `elevated_refuses`.
- Acceptance: CI unit tests; live PC (nachi): set `WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS=--remote-debugging-port=9222`, start, `netstat -ano | findstr 9222` empty, app opens; write HKCU value, start refuses with exit 78 and message, remove value; elevated/UAC: start from an admin prompt, exit 77.

### BL-326 Rust mitigations and linker flags (P2)
- Add `mitigations.rs`, `.cargo/config.toml`, `tools/security/check-pe.mjs` (reads PE optional header DllCharacteristics: dynamic base, high-entropy VA, NX compat, guard CF; load config directory for CFG; `/CETCOMPAT` from the debug directory extended DLL characteristics). No external tool needed (pure Node).
- Tests: `check-pe.test.mjs` with a built sample and a stripped sample; Rust `applies_policies_read_back`.
- Acceptance: `node tools/security/check-pe.mjs target/release/unravis-desktop.exe` exits 0 on `windows-2025`; live PC (nachi): Process Explorer or `Get-ProcessMitigation -Id <pid>` shows the policies; real case opens. Server 2016: not verified.

### BL-321 Zero plugins and command gate (P2)
- Add `commands.list`, `build.rs`, `capabilities/main-windows.json`, `tools/security/check-lock-plugins.mjs`.
- Tests (Rust): `commands_match_manifest_and_capability`, `capability_has_no_core_default`, `capability_one_window_no_remote`; node: `check-lock-plugins.test.mjs` (fails on any `tauri-plugin-*` in `Cargo.lock`, always on `tauri-plugin-updater`).
- Acceptance: as groom. Runnable live check: in the running window (debug-only build with `devtools` is NOT allowed; use the `e2e` feature build owned by area 08 with a test hook that calls `window.__TAURI_INTERNALS__.invoke('not_listed')`) the call is rejected with "not allowed". If area 08's hook is missing, Rust integration test with `tauri::test` `assert_ipc_response` for the unlisted name instead, and the live check is marked "not verified on real window".

### BL-322 `tauri.conf.json` security block and CSP (P2)
- Add the config (3.5), `tools/security/lint-tauri-config.mjs`, fixtures. Lint rules: fail on `unsafe-inline`, `unsafe-eval`, `data:` or `blob:` in `script-src`, any `*` source, missing `frame-ancestors`, `withGlobalTauri` true, `assetProtocol.enable` true, `remote` anywhere in capabilities, `devCsp` present in the release bundle (script reads `target/release/bundle` config copy and `tauri.conf.json` merged output), `freezePrototype` true while ADR says off.
- Acceptance: `node tools/security/lint-tauri-config.mjs` exits 0 and exits 1 on each fixture. live PC (nachi): packaged build opens a real case, console shows 0 CSP violations (page-side listener count is shown in the local log and asserted 0).

### BL-328 WebView2 lockdown (P2)
- Add `webview.rs`, `nav.rs`, `web/src/lib/desktop-guards.ts` (cancel `dragover`/`drop` outside `[data-drop-zone]`; mark import zones with that attribute in `ImportFilesPanel.svelte`, `SourceStep.svelte`).
- Accelerator keys: first try `SetAreBrowserAcceleratorKeysEnabled(false)`; Ctrl+C/V/X/A/Z stay. If keyboard-only navigation or screen-reader checks (area 07/P5 evidence) show F5/Ctrl+F loss matters, leave accelerators on and block DevTools, print and view-source by the other settings. Decided by the P5 accessibility check; default OFF (keys blocked).
- Tests: `nav_allows_tauri_and_tauri_localhost`, `nav_denies_lookalike_hosts` (`tauri.localhost.evil.com`, `http://tauri.evil.com`, `file:///`, `about:blank`, `ipc://`), `single_window_constructor` (grep), `no_devtools_feature` (`cargo tree -e features`).
- Acceptance: as groom, plus: drop of a real file on the import zone still imports; drop elsewhere does nothing. Live PC (nachi) list: F12, Ctrl+Shift+I, right-click, Ctrl+P, outside link click, `window.open`, drop outside zone, download link: all do nothing.
- Destroy/recreate: area 01 BL-308 test `recreated_window_has_same_lockdown` calls `build_main_window` twice and compares the applied-settings struct returned by `webview::applied()`.

### BL-338 Save-as export command (new, P2/P3)
- Why: downloads are off (2.26) and the UI exports by blob+anchor in four places.
- Add `desktop/src-tauri/src/export.rs` (`IFileSaveDialog` through the `windows` crate, no plugin), `commands.list` line `export_save`, and a UI helper `web/src/lib/hostSave.ts` used by `ExportPanel.svelte`, `DailyTable.svelte`, `ReportExport.svelte`, `blackbox.ts saveBlob`: browser mode keeps blob+anchor, desktop mode calls `invoke('export_save', bytes, {headers})`. Adapter choice sits in area 01 BL-314's host adapter.
- Tests: Rust `export_name_sanitised` (strips path parts, device names, ADS `:`), `export_cap_enforced`; Playwright with Tauri mock: each of the four exports calls `export_save` and never creates an anchor download.
- Acceptance: live PC (nachi): export a real case report, a CSV and a Blackbox pack; files open, bytes equal the browser-mode export (SHA-256 compare).

### BL-331 Rust client and sidecar launch (P2/P3)
- Add `pipe_peer.rs`; in `sidecar.rs` (area 01) spawn with `UpdateProcThreadAttribute` for `PROC_THREAD_ATTRIBUTE_MITIGATION_POLICY` (strict handle, image load, extension points) and Job Object `KILL_ON_JOB_CLOSE` unless attaching; no JS-side spawn (nothing in `commands.list` spawns).
- Child-process restriction not set (2.14). Version rule for attach: locator `version` must equal or be newer than the desktop's min (`MIN_BACKEND_VERSION` const); older -> ask Go to quit via shutdown RPC then spawn.
- Tests: `refuses_wrong_image`, `refuses_wrong_session`, `refuses_non_install_dir`, `accepts_genuine_fake_from_install_dir_in_citest` (fake server exe copied into a temp "install dir").
- Acceptance: as groom.

### BL-329 WebView2 runtime, data folder, policies (P2 folder, P4 warnings)
- Add `runtime.rs` (EdgeUpdate key `HKLM\SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}` value `pv`, also HKCU), `security_status.rs`, `web/src/lib/components/SecurityNotices.svelte`, copy text. Clear-on-exit via `ICoreWebView2Profile2::ClearBrowsingData` kinds all except local storage, run on window destroy and app exit.
- localStorage allow-list: file `web/src/lib/storageKeys.ts` lists every key prefix in use (from the grep: theme, timezone, eli10, subbar hint, update dismissed, active case id, call target, case view, guided mode by case id, blackbox intro and store, tracing auto key, keeper preview, update preview). Node test `storageKeys.test.cjs` fails if a source file calls `localStorage.setItem` with a key not in the list. Values never hold case content (reviewed in the PR).
- Tests: `runtime_present`, `runtime_missing`, `policy_downgrade_notice`, `policy_updates_suppressed_notice` with fake registry; node `storageKeys.test.cjs`.
- Acceptance: live PC (nachi): after a real session, search `%LOCALAPPDATA%\Unravis\webview2` with `findstr /s /m /c:"<known case string>"` and for key/token text: none outside the local storage leveldb (which holds only the allowed ids); set HKCU test policy values, see notices, remove them. HKLM variant needs UAC. Server 2025 behaviour: not verified.

### BL-332 Desktop mode without loopback or `session.json` (P3)
- Go: `--pipe-only` (area 01) is the only desktop launch mode after P3; test `TestPipeOnlyNoListenerNoSessionFile` starts `cmd/audit-explorer` with the flag in a temp `AUDIT_CONFIG_DIR`, checks no listening socket owned by the pid (`GetExtendedTcpTable`) and no `session.json`.
- Web: add `web/vite.desktop.config.ts` and host adapter switch (area 01 BL-314); desktop build must contain no `fetch` to `127.0.0.1` (grep test `no-loopback-in-desktop-bundle.test.cjs` on `web/dist-desktop`).
- Acceptance: as groom: `netstat -ano | findstr LISTENING` shows nothing for the app or Go pids; `dir %APPDATA%\APIAuditExplorer\session.json` absent while desktop runs alone; all case screens load; browser mode started separately still works with its own loopback and token.

### BL-323 UI strict-CSP compatibility (P3)
- Change: `web/src/lib/charts.ts`, `lib/tracing/tooltip.ts`, `lib/callTrace.ts`, `lib/rollup.ts`, `lib/resource.ts`, `lib/traffic.ts`, `components/TracingChart.svelte`, `BlackboxCharts.svelte`, `TrafficSlowness.svelte`, `RequestVolume.svelte` and others in the 71-line tooltip grep: add `chartTooltip.ts` with `richText(lines: string[]): string`; formatters return `richText([...])`; drop `escapeHTML` there (text is not parsed), drop `extraCssText`; set `tooltip.renderMode:'richText'`, via a shared `tipStyle`. Keep browser mode on HTML tooltips only if a flag `desktop` is false? No: one path, richText in both (less code, same text). Verify browser screenshots (tools/screens) still pass; update baselines only if differences are reviewed.
- Static inline `style=` to classes (start with `TracingChart`, `BlackboxCharts`, `CallTrace`, `CallLanes`, `CallMinimap`); `vite.desktop.config.ts`: `build.assetsInlineLimit: 0`, `define: { global: 'globalThis' }` only if a CJS dep appears (none today).
- Gate: node test `no-html-no-static-style.test.cjs` fails on `{@html`, `innerHTML`, `insertAdjacentHTML`, and static `style="` in `.svelte` templates. Dynamic `style:`/`style={}` allowed by 2.28.
- Playwright: `tools/e2e/specs/csp-desktop.spec.ts` serves `web/dist-desktop` with the exact CSP header (read from `tauri.conf.json`), opens every route (list from `App.svelte` screens) and fails on any `securitypolicyviolation`. Browser suite unchanged and green.
- Acceptance: as groom.

### BL-337 Hostile-content test (P3 core, P4 rerun)
- Add `tools/e2e/fixtures/hostile/` (values: `<img src=x onerror=...>`, `</script><script>`, `javascript:` URLs, CSS `url()`, 10 KB strings, RTL override, NUL, `{{ }}`, in SQL text, headers, user agents, Mongo docs, URLs) and `tools/security/make-hostile-case.go` (uses the real importer `internal/importer` to inject values into a scratch COPY of a case; never writes into the original; refuses a path that is not under a `scratch` dir).
- Tests: Playwright `hostile.spec.ts` (fail on `securitypolicyviolation`, `dialog`, `framenavigated` off-origin, any request not to the app origin).
- Acceptance: as groom; scratch copy is made by the tool from nachi's real case with the owner's go on which case.

### BL-324 Trusted Types and `freezePrototype` trial (P3)
- Add `tools/e2e/specs/tt-report-only.spec.ts` with header `Content-Security-Policy-Report-Only: require-trusted-types-for 'script'`. Collect violations; fix in app code or record. Then try `freezePrototype:true` build and run the BL-323 route list.
- Result goes to `docs/adr/0xx-trusted-types-freeze.md`. Enforce only if zero violations and zero console errors.
- Acceptance as groom.

### BL-334 Key Keeper stays the boundary (P3)
- Add `internal/keeper/serververify_windows.go`, call from every client in `internal/keeper/client.go`; add `tools/security/plant-secret-scan.ps1` (plants a marker in a test connection, runs a session, scans `%LOCALAPPDATA%\Unravis\{webview2,logs,crashes}`, `%APPDATA%\APIAuditExplorer`, UI bundle, process command lines `Get-CimInstance Win32_Process`).
- Document in `docs/adr/` that stronghold, per-user DPAPI and Credential Manager are not used for secrets.
- Tests: `TestKeeperClientRefusesNonSession0Server`, `TestKeeperClientRefusesWrongImage`; node test that `commands.list` has no name containing `secret`, `password`, `key`, `token`.
- Acceptance: as groom; elevated/UAC to confirm service account `NT SERVICE\UnravisKeeper` (name from repo, not groom).

### BL-336 SBOM and reproducible builds (P4)
- Add `tools/release/Make-Sbom.ps1` (cargo-cyclonedx, cdxgen for go and js, cyclonedx-cli merge, schema validate 1.7), `tools/release/Test-Reproducible.ps1` (builds twice, compares SHA-256), changes to `release.yml` (SBOM file attached to the release set and listed in the signed manifest through `Sign-Release.ps1`/`New-Sha256Sums.ps1`), Rust flags `/Brepro`, `/PDBALTPATH:%_PDB%`, `-C strip`, `--remap-path-prefix`; Go `-ldflags="-s -w -buildid="`. `trim-paths` used only if the pinned toolchain has it stable; else remap flag.
- Acceptance: as groom; reproducibility compares the unsigned builds (Authenticode changes bytes; compare before signing).

### BL-333 `--mcp` and CLI over the pipe (Parallel A)
- Security rules only: dial with `pipesec.DialVerified` to the MCP pipe from the locator; fail closed if locator missing or `session.json` is the only thing present (no fallback to loopback in desktop installs; fallback allowed only when no `backend.json` exists at all, i.e. browser-only). Output cap 1 MiB and redaction default in `internal/mcp/tools.go` (area 03 builds). Tests named in area 03 BL-354.
- Acceptance: as groom.

## 6. Security and footprint checks (this area)

- Release build scan `tools/security/scan-release.ps1`: no `citest` symbol (`pipesec.TestAllow`), no `devtools` feature, no `e2e` feature, no `devCsp` in bundle, no `--remote-debugging` string in either exe, no `tauri-plugin-` in `Cargo.lock`.
- `netstat` and `GetExtendedTcpTable` check in BL-332; network soak (area 07) must show 0 non-loopback packets from Go and Rust.
- Footprint: security code adds Rust under 400 LOC in `startup.rs`, `webview.rs`, `nav.rs`, `pipe_peer.rs`, `mitigations.rs`, `export.rs` combined (target; the DECISIONS total is 1,500). Windows crate features limited to the Win32 modules used (listed in `Cargo.toml`). No new Go dependency (no `go-winio`). `richText` tooltips cost no extra bytes. `deny.toml` bans duplicate large crates over a threshold only after the first tree is seen.
- Idle cost: peer check runs per connection, not per request (keep-alive); clear-on-exit runs once.
- Logs never carry case data or tokens: pipe logs hold PID, image path, SID suffix only. Test `TestPipeLogsNoSecrets`.
- Every new exe or file the installer lays down (`unravis-desktop.exe`, SBOM) is added to the release manager file lists (area 06, BL-194 flow).

## 7. Open risks left, each with an owner-free mitigation

1. Same-user malware can write into a per-user install dir and pass the image check. Mitigation: documented as a tripwire; DACL and logon SID still keep other users and sessions out; after BL-312 add publisher check; Key Keeper keeps real secrets off this boundary.
2. Instance-pool DACL (0x4 granted) differs from the report. Mitigation: `TestSecondCreateIsBusy` runs in CI on every PR; if it ever fails, the build fails.
3. Runtime mitigations on the Rust host start late. Mitigation: creation-time policy on the Go child; the host starts no other code before `apply()`; PE flags are checked on the file.
4. `dangerous flags` list can miss a new Chromium switch. Mitigation: unknown values in the policy show a warning notice, and the env is stripped; the list lives in one constant, extended by a PR.
5. Richtext tooltips may lose layout the HTML ones had. Mitigation: screenshot baselines (`tools/screens`) review in the BL-323 PR; keep line text identical; revert per chart only with an ADR line, not with `unsafe-inline`.
6. Dynamic `style:` bindings may be blocked after all. Mitigation: test run decides (2.28); fallback action `setProperty` is small.
7. CSP/COEP `require-corp` may break loading. Mitigation: COEP header is added only if the first packaged run is clean; otherwise dropped and recorded.
8. Loopback bulk exception may be needed. Mitigation: terms pre-written (2.21); not built unless BL-313 fails.
9. ACG on services could stop a service. Mitigation: trial gated by spike, ACG const per profile, existing OTA rollback after 3 bad starts, failure of a mitigation is non-fatal in production builds.
10. Accelerator-key block removes F5/Ctrl+F. Mitigation: default as 2.x; P5 accessibility evidence can flip one const `ACCELERATORS_ENABLED`.
11. The `keeper` and `pipesec` listeners are two copies of similar code. Mitigation: dedupe is deferred (needs the owner's go; it touches a service); both have their own tests.
12. WebView2 policy value names come from the report. Mitigation: BL-329 live check reads them on a real PC and the notice ids are table driven, so a wrong name is a one-line fix.
13. Server 2016, Win 10 22H2 and Server 2025 WSUS behaviour are not verified (no test box). Mitigation: each acceptance says "not verified" there; nothing in the UI claims support.
14. The desktop origin differs from `127.0.0.1`, so first desktop run starts with default preferences (theme, timezone, active case). Mitigation: stated to area 01; no data is lost, browser prefs stay.
15. Repo visibility unknown (attestations, Rulesets). Mitigation: nothing in this area depends on it.
