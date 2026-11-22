# Spec 01: shell and sidecar (BL-300 to BL-319)

Stage 2 (spec). Inputs: groom 01, `DECISIONS-draft.md`, the conflicts/questions of groom 02 to 08, research 01 and 03, and the code in `/home/claude/unravis` read on 2026-11-21. Report numbers are the reports' own; not re-verified.

## 0. Where the code differs from the reports (read first)

| Report / groom says | Code says | Effect |
|---|---|---|
| Sidecar is a console `unravis.exe` (report 03) | `unravis.exe` is built `-H=windowsgui` (`build.ps1`; PE subsystem 2 is checked). It already gets its console back with `attachParentConsole()` (`cmd/audit-explorer/console_windows.go`). | No console flash exists today for `--mcp`. Same exe as sidecar is cheap. |
| Singleton does not exist yet (BL-302) | `internal/instance` (mutex `Local\UnravisApp.<SID>` plus `.Open`/`.Quit` events, owner check) and `cmd/audit-explorer/gate.go` already do it. | BL-302 extends it. It does not rebuild it. |
| Refuse-admin to build (BL-304) | `isElevated()` in `cmd/audit-explorer/gate_windows.go` refuses a full elevated token for interactive starts. UAC-off admin is allowed on purpose. | BL-304 is a small extension. |
| A Go tray has to be built | `internal/tray` + `cmd/audit-explorer/trayapp.go` is the tray today (open, quit, autostart toggle). | Desktop tray (Rust) replaces it only in pipe mode. |
| Autostart is `HKCU\Run` (BL-310) | `internal/autostart` makes `Unravis.lnk` in the user Startup folder with `--background`. | Keep it. See D7. |
| Locator `%LOCALAPPDATA%\Unravis\run\session.json` (new) | `session.json` exists in `%APPDATA%\APIAuditExplorer` (roaming): `{port, token, pid}`, written by `internal/mcp/sessionfile.go`, read by `--mcp`. | One locator, two paths, see D4. |
| Data is one folder `%APPDATA%\APIAuditExplorer` (BL-316) | Three layouts (`internal/datadir`): shared `C:\ProgramData\Unravis` (installer-made, trust-checked, machine-DPAPI, plus keeper), per-user `%APPDATA%\APIAuditExplorer` (user-DPAPI), cases in `%LOCALAPPDATA%\APIAuditExplorer\cases`. `%LOCALAPPDATA%\Unravis` already holds `logs`, `updates`, `update-result`. | BL-316 as groomed is wrong. See D8. |
| Shutdown RPC is new (BL-303) | A quit already runs: `appCtx` cancel, `awaitShutdown`, 4 s `shutdownOnSignal`, deferred `closeCases`/`closeRepo`. Case files run `wal_checkpoint(TRUNCATE)` in some paths (`internal/casestore/casefile.go`, `sqlite/case.go`). | BL-303 is a route plus a proof test. |
| `go-winio` available | Not in `go.mod`. The keeper has its own pipe code (`internal/keeper/pipe_windows.go`, raw `CreateNamedPipe`, SDDL, first instance), but it is not a `net.Listener`. | Add `github.com/Microsoft/go-winio` for the new listener (D3). |
| `/healthz` (report 03) | No health route. `GET /api/version` (token) is the cheapest. | Supervisor uses `/api/version`. |
| Backlog ids | `docs/BACKLOG.md` has no BL-3xx rows yet. | Each build worker adds its rows. |

## 1. Purpose and boundaries

Owns:
- Rust shell `desktop/src-tauri` (module list in 3.2): spawn, attach, Job Object, supervisor, tray, window lifecycle, singleton, WebView2/CSP lockdown, crash capture, pipe client and the typed relay.
- Go side of the process model: `--pipe-server`, pipe listener, locator, shutdown route, elevation gate, per-user app dirs, CLI/MCP attach (locator and pipe client only).
- Baseline numbers (BL-300), the exe spike (BL-305), bundling (BL-317).

Does not own:
- Pipe grants, tool contracts, MCP policy (area 03, BL-344 to BL-346). Area 01 gives them the transport and `internal/locator`.
- The full CSP, Trusted Types, capability design, process mitigations (area 02). BL-311 is the baseline only.
- Window grace policy numbers and EcoQoS (area 07). Area 01 owns the mechanism and the default 60 s.
- Installer, OTA hand-off, signing (area 06, BL-312). Area 01 gives the stop contract (4.5).
- Services, Lite, code mode sandbox (the Go broker spawns it, not Rust).
- Svelte host adapter files (area UI/testing owns `web/src/lib/host.ts`; area 01 gives the command names in 4.3).

## 2. Resolved decisions

| # | Question / conflict | Decision | Reason |
|---|---|---|---|
| D1 | Does Go outlive the shell, or does the job kill it? (Q1) | Go owns case data. The shell is resident in the tray, so closing the window never stops Go. A sidecar that the shell spawned is in the Job Object and dies with the shell (tray Quit, crash, End Task). A sidecar the shell attached to (started by browser mode, `unravis.exe --headless`, or CLI) is never killed by the shell; Quit only detaches. | Both rules hold: window close keeps everything alive; shell death leaves no orphan. Headless owner stays the second legal parent (area 05 conflict). |
| D2 | Binary names (Q2, BL-305) | Default until spike: sidecar = the same `unravis.exe`, started `unravis.exe --pipe-server`. Shell = `unravis-desktop.exe`. Split `unravis-core.exe` only if the BL-305 test shows an MCP-held `unravis.exe` blocks the OTA swap and setup cannot clear it. The path lives in one Rust const (`SIDECAR_EXE`) and one Go const (`setup.AppExe`); nothing else hard-codes it. Decided by spike BL-305. | Today's exe already is GUI subsystem and already carries MCP; the draft keeps `unravis.exe`. |
| D3 | Pipe lib, name, SDDL (Q3) | `go-winio` v0.6.4 (pinned, `govulncheck` covered). Name: `\\.\pipe\unravis-<role>-<sid8>-<nonce32>`; `role` is `ui` or `ag`; `sid8` is the first 8 hex of SHA-256 of the user SID string; `nonce32` is 16 random bytes in hex, new at every owner start. SDDL: `D:P(D;;GA;;;NU)(A;;GA;;;<user SID>)` (user SID, not `OW`: an explicit SID does not depend on the token default owner). Plus `PIPE_REJECT_REMOTE_CLIENTS` (go-winio `PipeConfig` has no flag for it, so set through a wrapped `CreateNamedPipe` mode if the lib cannot; the CI test must prove it) and `FILE_FLAG_FIRST_PIPE_INSTANCE`. Keeper pipe untouched. | User SID is the stricter, readable form; random name blocks squat-by-guess; DACL is the boundary. |
| D4 | Locator format and path (Q3) | New file `%LOCALAPPDATA%\Unravis\run\session.json`, format v2 in 3.3. The Go owner also keeps writing the legacy `%APPDATA%\APIAuditExplorer\session.json` (`{port,token,pid}`) while loopback is on, so `--mcp` of the previous release and rollback still work. When loopback is off (P3), the legacy file is not written. Readers: new first, legacy second. One package, `internal/locator`, owns both; area 03 imports it and must not write its own. | Local folder avoids roaming a stale PID to another PC; rollback safety. |
| D5 | Two pipes or one for MCP (area 03 conflict 4) | Two listeners, one package: `ui` pipe (shell only) and `ag` pipe (CLI and MCP). Same name scheme and SDDL. The `ag` pipe gets a handler that area 03 supplies (`pipesrv.Role.Handler`); until it exists the `ag` pipe serves the same read routes as today's MCP bridge uses, behind the same token. | Report 04 wants a separate dispatcher; costs one more listener only. |
| D6 | Is the session token needed on the pipe? (Q4) | Keep `X-Audit-Token` unchanged for now. The shell and CLI read it from the locator (owner-only). The WebView never sees it: from P3 the Rust relay adds it. | Zero handler changes; the token is not the boundary (DACL is) but it costs nothing and keeps browser mode and pipe mode on one auth path. Revisit in area 03 when grants exist. |
| D7 | Autostart: default and mechanism (Q6) | Mechanism: keep the Startup-folder shortcut (`internal/autostart`, `--background`), no `HKCU\Run` write, no plugin. Desktop tray toggles it through a new Go route (BL-310). Default stays as today (off; the installer's sign-in step decides, `internal/setup` `plan_signin`). First run does not ask. When desktop becomes default (P5) the shortcut target moves to `unravis-desktop.exe --background`. | Already built, tested, user-scope, shows in Task Manager Startup apps; a new registry writer adds risk and no gain. |
| D8 | Data folder move (Q7, BL-316) | No move of existing data in P1 to P4. BL-316 is rewritten: create `%LOCALAPPDATA%\Unravis\{run,webview2,crashes}` (owner-only ACL, new files only) via `internal/appdirs`, keep `logs` where it is, and read legacy places as today. Case, sealed and keeper data stay where `internal/datadir` puts them. Revisit a move at P5 only with evidence (roaming profile trouble) and an owner go. | Real layout has three roots; shared mode is installer-owned and machine-DPAPI. A move gives no desktop feature and risks real sealed data. |
| D9 | Single instance: plugin or own (Q5) | Own, about 80 lines of Rust: mutex `Local\UnravisShell.<SID>` plus event `Local\UnravisShell.<SID>.Open`, owner check like `internal/instance`. Second launch calls `AllowSetForegroundWindow(first shell pid)`, sets the event, exits. No plugin. | Keeps the "no plugins" rule. Mirrors tested Go code. Deep links not built, so argv beyond known flags is dropped. |
| D10 | 60 s grace with a long query (Q8) | Destroy the window only when all hold: window closed 60 s, no import job running, no request in flight on the `ui` pipe, no open `Channel`. Else re-check every 15 s. The sidecar reports busy in `GET /api/desktop/state` (3.5). Default 60 s; grace number is area 07's. | Never cut a running job. |
| D11 | Crash dumps hold case data (Q9) | Rust and Go crash files go to `%LOCALAPPDATA%\Unravis\crashes\` (owner-only ACL), max 10 files and 50 MB, oldest pruned at each start. Dumps are minidumps without heap (`MiniDumpNormal`); Go crash text has stacks, no heap. Never uploaded. Tray item "Open crash folder" is the only share path; the user sends files by hand. | Smallest content that still symbolicates; manual share only. |
| D12 | Windows targets at P2 exit (Q10) | Pass on Windows 10 22H2 and Windows 11 (user session, tray). Server 2016/2019/2022 headless (no shell) must keep today's behavior. Server with RDP and the shell: best effort, not a P2 gate. | Servers lack WebView2; no Server box assumed. |
| D13 | Rust refuse-admin for the installer worker path (Q11) | No. The installer worker (`internal/setup` elevated step) never starts the shell. Setup starts the shell after install as the user (existing "start the app again" step), never elevated. Same rule in Rust as Go: refuse only a full elevated token (UAC-off admin runs). | Matches `isElevated()` today; no lock-out of UAC-off machines. Groom acceptance "UAC-off refuses" changes to "UAC-off runs, documented". |
| D14 | Tauri floor (area 02 Q1) | `>= 2.12.7`. Day-of check in BL-306: take the newest 2.x patch, never below 2.12.7. Rust `>= 1.90`. | DECISIONS value. |
| D15 | Who owns `tauri.conf.json` and `capabilities/` (area 02, 07) | Area 01 creates them in BL-306. Area 02 edits only `capabilities/` and CSP keys. Area 07 edits only the network-silence flags (one block `app.windows[].additionalBrowserArgs` is forbidden: flags go through `lockdown.rs`). Single file for WebView2 setup: `desktop/src-tauri/src/lockdown.rs`; both areas add lines to it, in separate functions (`lockdown::security()`, `lockdown::network()`). | Avoids two owners of one file. |
| D16 | Transport for bulk chart data (area 07 conflict 1) | Pipe via Rust. Loopback is not built. BL-313 decides pass or fail; a fail is a new item and a recorded security exception. Until BL-313, P2 uses loopback only because it loads today's UI (D17). | DECISIONS section 2 item 1. |
| D17 | Loopback in P2 | `--pipe-server --loopback` in P2 (shell loads `http://127.0.0.1:<port>/launch?c=<code>` through the existing launch flow). From BL-314 on, the shell passes no `--loopback`, and the bundled UI uses the relay. Browser mode (plain `unravis.exe`) is unchanged. | Draft phase plan. |
| D18 | Go tray in desktop mode | `--pipe-server` never starts `internal/tray`. Rust owns the tray. Plain `unravis.exe` still has the Go tray. If a Go tray owner (browser mode) is already running when the shell starts: P2 attaches over loopback; P3 asks the user once ("Unravis is running in browser mode. Switch to the desktop app?"), sends `Quit` through `instance.Signal`, waits 5 s, then spawns. | One tray per owner; no double icon. |
| D19 | Open signal in pipe mode | In `--pipe-server` mode the instance `Open` handler (`reopener`) does not open a browser. It sets the shell's `Open` event, or starts `unravis-desktop.exe` if no shell runs. | A second `unravis.exe` launch from Start menu then opens the desktop. |
| D20 | Windows hosts | Rust refuses to start and shows a message box on non-Windows; Linux dev builds of the shell are not a goal. Go keeps its `_other.go` stubs. | Windows first. |
| D21 | Updater notify channel (area 06) | Go tells the shell over the `ui` pipe's server-sent state (`/api/desktop/state` poll every 5 s plus on-demand), not loopback. | No loopback in desktop mode. |

## 3. Design

### 3.1 Components

```
unravis-desktop.exe (Rust, user, Medium IL)
  main -> lockdown::env_strip -> elevation::refuse -> singleton::enter
       -> locator::read -> attach | spawn(job) -> supervisor
       -> tray + window (WebView2)
  WebView2 --invoke/Channel--> commands.rs --> pipe_client.rs --HTTP/1.1--> \\.\pipe\unravis-ui-...
unravis.exe --pipe-server (Go sidecar)
  gate (Local\UnravisApp mutex) -> pipesrv ui + ag listeners -> internal/server handler (unchanged)
  -> locator write -> run loop -> shutdown route
unravis.exe --mcp / CLI -> locator -> pipe `ag`
```

### 3.2 Names

Go (new):
- `internal/pipesrv/` : `pipesrv.go` (`Listen(role, sid) (net.Listener, name string, err)`, `Serve(ctx, role, handler)`), `listen_windows.go`, `listen_other.go` (returns `ErrUnsupported`), `peer_windows.go` (`ClientPID(conn)`, `ClientImage(pid)`; logged, not enforced), `sddl.go` (`SDDL(userSID string) string`, pure, testable on Linux).
- `internal/locator/` : `locator.go` (`Record`, `Write`, `Read`, `Remove`, `Alive`), `locator_windows.go` (process creation-time check), `locator_other.go`. Wraps `internal/mcp/sessionfile.go` for the legacy file (call, do not copy).
- `internal/appdirs/` : `appdirs.go` (`Root()`, `RunDir()`, `WebView2Dir()`, `CrashDir()`, `Ensure()` with `casestore.SecureDir`).
- `internal/pipesrv/pipesrv.go` (BL-303 built the shutdown route here, not in `internal/server`, because only `pipesrv` knows the pipe role): `pipesrv.WithShutdown(token, stop) Option`, `ShutdownPath`, route `POST /api/internal/shutdown` (ui pipe only; the `server` package still owns `PipeRole(ctx) string`, `ContextWithPipe(ctx, role) context.Context` if later items need them in `internal/server/pipe.go`), route `GET /api/desktop/state`, route `GET|POST /api/desktop/autostart`.
- `cmd/audit-explorer/pipeserver.go`: flags `--pipe-server`, `--loopback`, `--owner=shell|headless`, `--shell-pid=<n>`; the run branch; `--no-tray` is implied.
- `cmd/audit-explorer/elevation.go`: `refuseElevated()` shared by gate and pipe mode.
- `cmd/audit-explorer/attach.go`: client side for `--quit`, `--check-update`-style one-shots through the pipe when present (BL-318).

Rust (`desktop/src-tauri/src/`, one crate, target under 1,500 lines):
`main.rs`, `config.rs` (paths, flags, consts), `elevation.rs`, `singleton.rs`, `job.rs`, `locator.rs`, `spawn.rs`, `pipe_client.rs`, `supervisor.rs`, `tray.rs`, `window.rs`, `lockdown.rs`, `crash.rs`, `commands.rs`. Crates: `tauri` (>= 2.12.7, no plugins), `windows-sys` (Job, Process, Security, Threading), `tokio` (net, time, rt), `hyper` + `hyper-util` (client over named pipe), `serde`/`serde_json`, `minidumper-child`. No `tauri-plugin-*`.

Files:
- `desktop/src-tauri/{Cargo.toml,Cargo.lock,build.rs,tauri.conf.json,capabilities/main.json}`.
- `tools/desktop/Build-Desktop.ps1`, `tools/desktop/Test-Sidecar.ps1`, `tools/desktop/Measure-Baseline.ps1` (PowerShell, matches repo style).

### 3.3 Data and message shapes

Locator v2, `%LOCALAPPDATA%\Unravis\run\session.json` (file ACL owner-only through `casestore.SecureFile`; written to a temp file, secured, synced, renamed, as `writeSessionRecordWith` does):

```json
{
  "v": 2,
  "pid": 12345,
  "started": "2026-11-21T10:00:00Z",
  "startedFiletime": 133800000000000000,
  "exe": "C:\\Program Files\\Unravis\\unravis.exe",
  "version": "1.2.3",
  "owner": "shell",
  "shellPid": 6789,
  "token": "<43 chars>",
  "port": 0,
  "pipes": { "ui": "\\\\.\\pipe\\unravis-ui-1a2b3c4d-<32hex>", "ag": "\\\\.\\pipe\\unravis-ag-1a2b3c4d-<32hex>" }
}
```
- `owner`: `shell` (spawned by the shell), `headless`, or `browser` (plain run; `pipes` absent).
- `port` is 0 when loopback is off. `startedFiletime` is the process creation time (100 ns, FILETIME) for the alive test.
- Alive test (`locator.Alive`): `OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION)` on `pid`, creation time equals `startedFiletime`, and image path equals `exe`. For a pipe client also one `GET /api/version` in 2 s.
- Unknown fields are ignored; `v` greater than known is "present but unusable" (CLI says "update Unravis").

Pipe wire: HTTP/1.1 keep-alive on the byte-mode pipe. Client sends `Host: localhost`, `X-Audit-Token`, no `Origin`. The Go server uses a second `http.Server` for pipes with `ConnContext` that adds the role (`server.ContextWithPipe`) and the same `newHTTPServer` limits. `allowedHost` and `allowedOrigin` stay as they are (Host `localhost` passes).

Hello route (`GET /api/desktop/hello`, ui pipe only, no token needed, no `Origin`): returns `{"token":"..."}` to a peer that passed the pipe peer check. The locator holds no token (PLAN section 1), so this is how the shell learns it, after attach or spawn. The shell keeps it in memory only. Built in `internal/pipesrv` (`HelloPath`).

Desktop state route (`GET /api/desktop/state`, role `ui` only, token):
```json
{ "v":1, "busy": {"imports":0,"inflight":0}, "update": {"ready":false,"version":""}, "autostart": {"allowed":true,"enabled":false}, "ownerPid":12345 }
```
Autostart: `POST /api/desktop/autostart` body `{"enabled":true}` -> `internal/autostart` (same checks as `trayapp.go`).

Shutdown (`POST /api/internal/shutdown`, role `ui` only, token, body `{}`): reply `202 {"state":"stopping"}`, then call the `cancel` that main already uses for `--quit`. Other roles and loopback get 404.

Command line:
- `unravis.exe --pipe-server [--loopback] [--owner=shell|headless] [--shell-pid=N]`. Not with `--mcp`, `--quit`, `--check-update`, `--no-browser`, `--background`. `--demo` allowed (tests).
- Exit codes (sidecar, new, also printed to the log): 0 clean, 11 refused (elevated), 12 another owner exists (attach instead), 13 pipe could not start. Existing codes unchanged.
- `unravis-desktop.exe [--background] [--quit]`. `--quit` asks the running shell to quit (event `Local\UnravisShell.<SID>.Quit`). Unknown args are dropped.

Config keys (Rust `config.rs`, no config file in P2; consts and env for tests only):
`UNRAVIS_SIDECAR` (test only, path override; ignored unless the exe is built with feature `e2e`), `UNRAVIS_RUN_DIR`. Release builds ignore both.

### 3.4 Sequences

Start (P2, shell first):
1. `main`: strip `WEBVIEW2_*` env; `elevation::refuse()` (message box "Unravis does not run as administrator. Start it from the Start menu or a normal window." exit 11); `singleton::enter()` (second: set Open event, exit 0).
2. Read locator. If `Alive` and `owner != browser` and `pipes` present: attach (no job). If `Alive` and `owner == browser`: P2 attach over loopback; P3 switch dialog (D18).
3. Else spawn: `CreateProcessW` of `<exe dir>\unravis.exe --pipe-server --loopback --owner=shell --shell-pid=<pid>` with `CREATE_NO_WINDOW | CREATE_SUSPENDED`; `AssignProcessToJobObject` (job `KILL_ON_JOB_CLOSE`, plus `DIE_ON_UNHANDLED_EXCEPTION` off); `ResumeThread`.
4. Wait for locator with a new `pid` and `Alive` (poll 100 ms, up to 10 s). If exit code 12 comes back, loop to step 2 once.
5. Supervisor starts. Shell opens window on `http://127.0.0.1:<port>/launch?c=<code>` (P2; code comes from `POST /api/desktop/launch-code`, role `ui`, mints one with the existing `mintLaunchCode` and `server.Rearm`; new route, token) or the bundled UI (P3).

Go side of step 3: `run` -> `parseFlags` -> `refuseElevated` -> `enterSingleInstance` (mutex; if not first: wait up to 10 s for a live locator then exit 12, and never signal `Open` in pipe mode) -> `wireData` -> pipes -> `locator.Write` (after listeners and handler are ready, so a reader never gets a dead pipe) -> serve.

Stop by tray Quit: shell sets state Stopping (supervisor will not restart) -> `POST /api/internal/shutdown` -> wait for the process handle up to 5 s -> job close kills any rest -> remove nothing (Go removes its own locator on clean exit through `locator.Remove(token)`) -> exit shell. If the sidecar was attached (not spawned), shell only exits.

Stop by End Task or crash of the shell: job closes, Windows kills the sidecar (no RPC). The next start finds the dead `pid`, the `Local\UnravisApp` mutex is free, the new owner overwrites the locator. SQLite recovers from WAL on next open.

Window close: window destroyed after D10 conditions; shell + sidecar stay. Tray "Open Unravis" or `Open` event creates the window again with the full lockdown applied (`lockdown::apply(builder)` is the only constructor path).

Crash of the sidecar (supervisor): see BL-307.

### 3.5 Failure modes and what the user sees

| Failure | User sees |
|---|---|
| Shell started elevated | Message box, exit 11. |
| Sidecar will not start (exit 13 or no locator in 10 s) | Window shows "Unravis could not start its engine" with "Try again" and "Open logs folder" (`%LOCALAPPDATA%\Unravis\logs`). Tray icon shows error state. |
| Sidecar crash | Tray tooltip "Restarting..."; window banner "Reconnecting" up to 8 s; back to normal. After breaker: tray error, window "Unravis stopped. Restart backend / Open crash folder". |
| Pipe busy or slow | Rust retries connect 3 times with 100 ms, then returns an error to the UI (UI shows its existing error text). |
| Stale locator | Silent: replaced. |
| Two logons | Each has its own mutex, locator folder and pipes; no message. |
| Other account's process holds the mutex name | Existing `ErrNameHeld` text from `gate.go`. |
| WebView2 missing | Message box with the Microsoft download link text; the shell exits. Browser mode is the fallback and the message says so. |
| Data dir not writable | Existing Go messages. |

## 4. Interfaces other areas rely on (exact names)

4.1 Go packages: `internal/pipesrv`, `internal/locator`, `internal/appdirs` (3.2). Types: `locator.Record` (fields as 3.3, Go names `V, Pid, Started, StartedFiletime, Exe, Version, Owner, ShellPid, Token, Port, Pipes map[string]string`), `locator.Read(getenv) (Record, error)`, `locator.Alive(Record) bool`, `pipesrv.Dial(ctx, name) (net.Conn, error)` (client side, used by CLI and MCP; go-winio `DialPipeContext`).
4.2 Roles: `ui` and `ag`. Area 03 supplies the `ag` handler through `pipesrv.RoleHandlers map[string]http.Handler` set before `Serve`. Area 03 must not add a third listener without area 01.
4.3 Tauri commands (BL-314, all in `build.rs` `AppManifest::commands`, one capability file `capabilities/main.json`, window label `main`, local origin only):
`ui_version()`, `ui_cases_list(request_id)`, `ui_case_view(case_id, request_id)`, `ui_cancel(request_id)`, `ui_desktop_state()`, `ui_set_autostart(enabled)`, `ui_open_logs()`, `ui_open_crashes()`. Return `{ "status": 200, "body": "<json text>" }` through a `PipeReply` struct; `ui_cases_list` and `ui_case_view` may return `tauri::ipc::Response` bytes when body > 64 KB. No command takes a path or URL. Command ids for new slices are added by area UI through area 01's `commands.rs` (one PR per group).
4.4 Browser-mode contract: plain `unravis.exe` behavior, flags and the legacy `session.json` do not change. `web/src/lib/api.ts` keeps `fetch`; the adapter chooses `invoke` only when `window.__TAURI_INTERNALS__` is present.
4.5 Stop contract for setup/OTA (area 06): to stop everything, setup calls (a) `unravis-desktop.exe --quit` and waits for the shell exit, then (b) the existing `instance.Quit` signal (what `unravis.exe --quit` sends) to the Go owner, then waits (existing `askQuitRounds`), then terminates what is left. Setup's file list gets `unravis-desktop.exe` (area 06 adds it). `internal/setup/plan.go` already finds "mine" by image path; it must include the shell exe.
4.6 Exe names: `unravis.exe` (CLI, MCP, sidecar), `unravis-desktop.exe` (shell). `unravis-core.exe` only if BL-305 says so; then `unravis.exe` becomes the thin client and `internal/setup` `AppExe` changes in one commit.
4.7 Process image check list for pipes (area 02, Q3): the allowed images are `unravis.exe` and `unravis-desktop.exe` from the install dir (`os.Executable()` dir), resolved at each connect, not stored; test builds add paths through env `UNRAVIS_TEST_PIPE_IMAGES` (only in builds with tag `e2e`). Logged in all builds; enforced only on the `ag` pipe after BL-345.
4.8 Data paths: `appdirs.RunDir()` = `%LOCALAPPDATA%\Unravis\run`, `WebView2Dir()` = `%LOCALAPPDATA%\Unravis\webview2`, `CrashDir()` = `%LOCALAPPDATA%\Unravis\crashes`. Logs stay `%LOCALAPPDATA%\Unravis\logs` (`applog.DefaultDir`).
4.9 CI file ownership: area 01 adds a Windows job `desktop-sidecar` to `.github/workflows/ci.yml` (runs `tools/desktop/Test-Sidecar.ps1`) and, from BL-306, `desktop-shell` (cargo build, clippy, test). Area 08 hosts the shared runner setup; both jobs use `windows-2022` as `ci.yml` does today, until area 08 moves it.

## 5. Build notes per BL item

Order inside the area: BL-300 and BL-304 and BL-316 (all start together) -> BL-301 -> BL-302, BL-303 (parallel) -> BL-305 -> BL-306 -> BL-317, BL-311, BL-315, BL-309 (parallel) -> BL-307 -> BL-308 -> BL-310 -> BL-313 -> BL-314. BL-318 starts after BL-302 and BL-305 (parallel A/B).

### BL-300 Baseline (P0, M)
- Add `tools/desktop/Measure-Baseline.ps1`: starts `dist\unravis.exe` (current release) N=3 times; measures process start to first `GET /` 200 (cold start to UI as the browser sees it), idle RAM (private working set) and CPU after 60 s for Go, RAM with the largest real case open, `--mcp` initialize latency (spawn, send `initialize`, first reply; ten tools listed), install and upgrade time (`unravis-setup.exe` silent, timed), and on a Server 2016 box the Blackbox numbers if a box exists (else recorded "not measured").
- Writes `docs/desktop/baseline-<release tag>.md` (machine spec, OS build, Defender state, runs, spread). Names the prior release as rollback.
- Tests: `Measure-Baseline.ps1 -SelfTest` against `--demo` to check the script runs (not proof).
- Acceptance (runnable): `pwsh tools/desktop/Measure-Baseline.ps1 -Runs 3` on nachi's PC writes the file; spread columns present; the file lists the release tag; MCP latency row exists. (live PC)

### BL-301 `--pipe-server` (P1, L)
- Add `go-winio` to `go.mod`. New `internal/pipesrv` (3.2). `pipeserver.go` wires: build both listeners, `server.New(...)` once, two `http.Server` values sharing the handler (`ConnContext` sets role), `Serve` each. `--loopback` also keeps `listenLoopback`.
- `sddl.go`: `SDDL(sid)` returns `D:P(D;;GA;;;NU)(A;;GA;;;<sid>)`.
- Tests (names): `TestSDDLStringShape`, `TestPipeNameFormat`, `TestPipeNameNonceDiffers`, `TestFirstInstanceRefusesSecondListener` (windows), `TestPipeRejectsRemoteFlag` (windows, reads mode via a second `CreateFile` + `GetNamedPipeInfo`), `TestServeOverPipeSameJSONAsLoopback` (windows, same handler, same case list), `TestNoTCPListenerInPipeMode` (windows: `GetExtendedTcpTable` has no new LISTEN row for the pid), `TestPipeStatsPeerPIDLogged`.
- Script `tools/desktop/Test-Sidecar.ps1`: starts `--pipe-server --demo` and checks `netstat -ano` for the pid, then a second Windows user run (if `-OtherUser` creds are given, else prints SKIP and the CI step uses `psexec`-free scheduled task as a second local account; if the runner cannot make one, the step is marked `live PC`).
- Do not touch `internal/keeper/pipe_windows.go`.
- Acceptance:
  - `go test ./internal/pipesrv/...` green on windows-2022. (CI)
  - `Test-Sidecar.ps1 -Check NoPort`: no LISTEN row for the sidecar pid. (CI)
  - `Test-Sidecar.ps1 -Check OtherUser` and `-Check Network`: access denied (second user; network check is the DACL test via `NU` deny unit test plus the remote flag test). (CI or live PC)
  - `Test-Sidecar.ps1 -Check Squat`: a second listener with the same name fails. (CI)
  - On nachi's PC: `GET /api/cases` and `GET /api/cases/{id}` over the `ui` pipe equal the loopback bytes (script compares SHA-256 of both bodies) for the real cases. (live PC)
  - `go test ./...` failure list equals the 19 known Windows-only `internal/server` failures on Linux (compare to the saved list in `docs/desktop/baseline-go-tests.txt`, created by this item). (CI)

### BL-302 Attach, singleton, locator (P1, M)
- New `internal/locator` and `appdirs`. In `cmd/audit-explorer`: `enterSingleInstance` gets a `pipeMode bool`; in pipe mode a non-first copy waits for a live locator (40 x 250 ms) and returns exit 12, never `Open`. `startInteractive` is skipped in pipe mode; `reopener.open` is swapped for `openDesktop` (D19).
- `writeSessionFile` calls `locator.Write` (v2) and, when loopback is on, the legacy writer. Cleanup removes both by token (`mcp.RemoveSessionFile` semantics kept).
- Tests: `TestLocatorRoundTrip`, `TestLocatorAliveDeadPid`, `TestLocatorAliveReusedPid` (creation time differs), `TestLocatorRejectsUnknownVersion`, `TestLocatorFileACLOwnerOnly` (windows), `TestPipeModeSecondCopyExits12`, `TestTenConcurrentStartsOneOwner` (windows, runs 10 `--pipe-server --demo` and counts live pids), `TestStaleLocatorReplacedAfterKill`.
- Acceptance:
  - `Test-Sidecar.ps1 -Check TenStarts`: exactly one `unravis.exe` owner remains. (CI)
  - `-Check Stale`: `taskkill /f` the owner, start again, locator `pid` changes and `Alive` is true. (CI)
  - `-Check Acl`: `icacls` on `session.json` shows only the current user (and no Everyone/Users). (CI)
  - Two logons: run `Test-Sidecar.ps1 -Check TwoLogons` (second account via `runas`/RDP); two locators in two `%LOCALAPPDATA%`, two live owners. (live PC)

### BL-303 Shutdown route (P1, S)
- Built in `internal/pipesrv` (`WithShutdown`, called from `cmd/audit-explorer/main.go`), not `internal/server/pipe.go`: only `pipesrv` knows the pipe role. Reply `202 {"state":"stopping"}`. In pipe mode set the http drain to 2 s (`pipeShutdownTimeout`) so total stop fits 5 s. Check `closeCases` closes each case file with a checkpoint; if a path skips it, add `PRAGMA wal_checkpoint(TRUNCATE)` before close (in `casestore.CaseFile.Close`, `internal/casestore/casefile.go:477`).
- Tests: `TestShutdownRouteOnlyOnUIPipe` (loopback and `ag` get 404), `TestShutdownRequiresToken`, `TestShutdownCheckpointsWAL` (app-created case, write load, call, no `-wal` file, `PRAGMA integrity_check` = ok), `TestKillDuringWriteReopens` (windows).
- Acceptance:
  - `Test-Sidecar.ps1 -Check ShutdownClean -Real`: calls shutdown with writers busy on a real case copy; exit code 0, no `-wal`/`-shm`, `integrity_check` ok (uses `unravis.exe` own helper `--check-case <file>`? not added: the script uses the repo's `internal/dbhealth`/sqlite test helper through `go run ./tools/dev/checkdb`, new 40-line tool). (live PC for the real case; CI for app-created)
  - `-Check KillDuringWrite`: `taskkill /f` mid-write, restart, `integrity_check` ok. (CI)

### BL-304 Refuse-administrator (P1, S)
- Move `isElevated` use into `refuseElevated()` and call it first in the `--pipe-server` branch and in the Rust `elevation.rs`. Keep `elevationDecision`'s fail-closed rule. Add to the check: integrity level >= High with `TokenElevationType == Full` only (same rule; D13). Exit 11.
- Setup's elevated worker does not call `run`, so it is not gated; add `TestSetupWorkerNotGated` (grep style test that `internal/setup` does not import the gate).
- Tests: `TestPipeModeRefusesElevated` (fake `elevated func() bool`), `TestUACOffAdminAllowed` (existing `elevationDecision` table gets the case).
- Acceptance:
  - Normal prompt: `unravis.exe --pipe-server --demo` runs. (CI)
  - Elevated prompt: exit 11 and the message. (elevated/UAC)
  - UAC-off admin VM: runs (documented behavior). (elevated/UAC)

### BL-305 Spike: same exe or split (P1, S)
- Script `tools/desktop/Spike-ExeLock.ps1`: with a real MCP client attached (Claude Code, then Codex) holding `unravis.exe --mcp`, runs a real OTA to a test feed (ephemeral CI key as in `tools/release`), for layout A (same exe) and layout B (a copy named `unravis-core.exe` as sidecar and `unravis.exe` as the MCP client). Records: did the swap succeed, setup's close-app result, time, exe size, `--version` cold start with Defender on, console flash seen (yes/no) per host.
- Output: `docs/desktop/spike-bl305.md` with final names and which exe the OTA replaces.
- Default used until the note exists: layout A.
- Acceptance: `docs/desktop/spike-bl305.md` exists with both layouts, five cold-start numbers each, and a one-line decision; a CI check (`tools/desktop/Test-DocsHave.ps1 spike-bl305.md "Decision:"`) passes. Run on nachi's PC with a live MCP session. (live PC; doc check CI)

### BL-306 Shell skeleton (P2, L)
- Create `desktop/src-tauri` (3.2). `tauri.conf.json`: no `bundle.active` installer (bundle via Go setup); `app.security.csp` placeholder set by BL-311; `app.withGlobalTauri` false; no `plugins` key. `capabilities/main.json` lists only the custom commands (empty in P2).
- `job.rs`: `CreateJobObjectW`, `SetInformationJobObject(JOBOBJECT_EXTENDED_LIMIT_INFORMATION, KILL_ON_JOB_CLOSE)`. `spawn.rs`: `CreateProcessW` with `CREATE_NO_WINDOW | CREATE_SUSPENDED`, assign, `ResumeThread`. Nested jobs are fine on Windows 8+/Server 2012+. Code mode's Go-side jobs nest under it (area 04).
- `locator.rs`: read v2, call `Alive` equivalent (open process, creation time).
- Tests: Rust `cargo test`: `locator_parses_v2`, `locator_rejects_bad_version`, `elevation_decision_table`, `job_kills_child` (spawns `ping -n 60` into the job, drops the job, asserts exit), `capabilities_have_no_shell` (reads the JSON). Script `tools/desktop/Check-Capabilities.ps1` (grep: no `shell:`, no `fs:`, no `http:`, no `*:allow-*` in `capabilities/*.json`; no `tauri-plugin-` in `Cargo.toml`).
- Acceptance:
  - `Check-Capabilities.ps1` exit 0. (CI)
  - `cargo clippy -D warnings` and `cargo test` green on windows-2022. (CI)
  - Shell opens the real cases of nachi's data (same list as browser). (live PC)
  - End Task the shell: sidecar gone in 5 s, `Get-Process unravis` empty. (live PC)
  - Elevated launch: exit 11 with message. (elevated/UAC)
  - Remove/disable desktop (just not starting it): `unravis.exe` plain still opens the browser as today. (live PC)

### BL-307 Supervisor (P2, M)
- `supervisor.rs`: states `Starting, Running, Backoff, Tripped, Stopping`. Wait on the child handle. Health every 10 s: `GET /api/version` over `ui` pipe, 2 s timeout; 3 failures in a row = hung, kill and restart. Backoff 0.5, 1, 2, 4, 8 s with +/-20% jitter; reset after 60 s healthy. Breaker: 5 starts in 60 s -> `Tripped` (no more spawn; tray + window error). Applies only to a spawned child (D1). After `Tripped`, "Restart backend" resets.
- Time and counters are injected (`trait Clock`) so tests run fast.
- Tests: `backoff_sequence`, `breaker_trips_at_five_in_sixty`, `no_restart_when_stopping`, `restart_after_exit` (fake child), `hung_detected_after_three_fails`.
- Acceptance:
  - `cargo test supervisor` green. (CI)
  - Quit from tray: no restart. (CI via test above, plus live)
  - `tools/desktop/Test-Supervisor.ps1`: kills the real sidecar 3 times: shell restarts it each time, `GET /api/cases` equal before and after. (live PC)
  - Same script with 5 kills in 60 s: tray state file `%LOCALAPPDATA%\Unravis\run\shell-state.json` shows `"state":"tripped"`, no new `unravis` pid. (live PC)

### BL-308 Tray and window lifecycle (P2, M)
- `tray.rs`: built-in Tauri tray; menu: Open Unravis, Start at sign-in (toggle), Open crash folder, Restart backend (only on error), Quit. `window.rs`: `on_window_event(CloseRequested)` -> `prevent_close` + hide; destroy after D10 conditions; `RunEvent::ExitRequested` with no explicit quit -> `prevent_exit`; guard flag against double destroy; recreate through `lockdown::apply`.
- Writes `shell-state.json` `{state, windowOpen, spawned, sidecarPid}` for tests (owner-only, in `RunDir`).
- Tests: `window_destroy_waits_for_busy`, `close_twice_is_safe`, `exit_requested_keeps_app`, `quit_runs_shutdown_once`.
- Acceptance:
  - `tools/desktop/Soak-Window.ps1 -Cycles 50` with a real big case: after each cycle records private working set and commit charge of shell, sidecar and WebView2 children; passes if the last 10 cycles show growth under 10 MB over the first 10 (agreed margin, can be set by area 07 after P0). (live PC)
  - After 60 s closed: no `msedgewebview2.exe` whose command line contains the app's `webview2` dir. (live PC)
  - Reopen time p50 and p95 over 20 reopens written to the soak report (target 800 ms). (live PC)
  - Tray Quit: both exes gone, no `-wal` beside the real case files. (live PC)

### BL-309 Single instance (P2, S)
- `singleton.rs` (D9). Names: `Local\UnravisShell.<SID>`, `.Open`, `.Quit`. Owner check by reading the object owner like `instance.OwnedByCurrentUser`. Foreground: second process calls `AllowSetForegroundWindow(first_pid)` where the pid is in `shell-state.json`.
- Tests: `singleton_second_returns_false`, `singleton_rejects_foreign_owner` (via injected checker), `args_unknown_dropped`.
- Acceptance:
  - `Test-Shell.ps1 -Check TwoStarts`: two launches within 100 ms, one `unravis-desktop.exe` process. (CI, if a desktop session exists on the runner; else live PC)
  - Launch twice by hand: first window focused. (live PC)
  - Second Windows user starts own shell. (live PC)
  - Alt+Tab away and back, Tab moves focus (tauri#15624). (live PC)

### BL-310 Autostart (P2, S)
- Go: routes in 3.3 using `internal/autostart` and `autostart.StartupDir()` (as `trayapp.go` `productionTrayDeps`). Rust tray calls `ui_set_autostart`. `--background` start: shell starts hidden, no window, tray only.
- Tests: `TestAutostartRouteOnlyUIPipe`, `TestAutostartTogglesShortcut` (temp startup dir), existing `internal/autostart` tests stay green.
- Acceptance:
  - Toggle off: `Unravis.lnk` removed (script checks the Startup folder). (CI)
  - No HKLM write: `Test-Shell.ps1 -Check NoHklmWrite` (procmon-free: compare `HKLM\...\Run` before and after). (live PC)
  - Sign out and in with toggle on: tray present, no window. (live PC)
  - Standard user can toggle. (live PC)

### BL-311 WebView2 and CSP lockdown (P2, M)
- `lockdown.rs`: `env_strip()` removes every `WEBVIEW2_*` first in `main()` (test build with feature `e2e` skips it only when `UNRAVIS_E2E_KEEP_WEBVIEW2_ENV=1`); `apply(builder)`: `data_directory(appdirs WebView2Dir)`, `devtools(false)`, `disable_context_menu`, `on_navigation` allow only the app origin, `on_new_window` deny, `disable_drag_drop_handler` on, `browser_extensions_enabled(false)`, downloads denied (`on_download` returns false), `use_https_scheme(true)`. Only DevTools stays on in builds with feature `e2e`.
- CSP (`tauri.conf.json`): `default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src ipc: http://ipc.localhost; object-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'`. Today's server CSP also has `style-src 'unsafe-inline'` (computed bar widths), so this baseline matches it; removing it is area 02's item. Honest note: this is not the draft's "no unsafe-inline" for scripts only; scripts have none.
- Tests: `lockdown_env_stripped`, `navigation_blocked_external`, `new_window_denied`; script `tools/desktop/Check-Csp.ps1` loads the built UI with the CSP in headless Edge and fails on any `securitypolicyviolation` event on the case home page.
- Acceptance: as groom (F12, right-click, Ctrl+P, file drop do nothing; external URL blocked; Edge extension absent; env var ignored (CI: spawn with `WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS=--remote-debugging-port=9222`, assert port closed); zero CSP violations). Env and navigation tests in CI; the rest live PC.

### BL-313 Bulk payload benchmark (P3, S)
- `desktop/src-tauri/benches/pipe_bulk.rs` (feature `bench`, not built in release) and `tools/desktop/Bench-Bulk.ps1`. Payload: real series from a real big case through a Go debug route `GET /api/internal/bench/bytes?n=<MB>` (only in builds with tag `bench`, role `ui`), sizes 10/25/50 MB, pipe buffer 64 KB vs 256 KB (`pipesrv` const `inBuf/outBuf`, env override in bench tag only), vs loopback.
- Result table `docs/desktop/bench-bl313.md` with pass/fail against area 07's thresholds (p95 pan/zoom <= 100 ms) and the 300 ms switch rule. Result "fail" creates a new backlog row; nothing else is built.
- Acceptance: file exists with 3 sizes x 3 methods x 5 runs on nachi's PC; has a "Result:" line. (live PC)

### BL-314 Relay and commands (P3, L)
- `commands.rs` and `pipe_client.rs`. One pooled connection with up to 4 parallel requests; each request has `request_id` (UUID from UI) and a `CancellationToken`; `ui_cancel` drops the connection for that request so Go's `r.Context()` ends. Size cap 64 MB, time cap 60 s per command. Commands in 4.3. Window origin check: the capability is bound to window label `main`; `commands.rs` also checks `webview.url().origin()` equals the app origin and rejects anything else (frames).
- Bundled UI: `web/vite.config.ts` gets a desktop build mode output `web/dist-desktop/` (area UI owns the config; area 01 asks for `base: './'`, `assetsInlineLimit: 0`). `tauri.conf.json` `frontendDist` = `../../web/dist-desktop`.
- Go: no handler changes; add test `TestCancelledRequestEndsHandler` (client closes the pipe, `r.Context().Done()` fires, goroutine count back to baseline via `runtime.NumGoroutine` with retry).
- Tests (Rust): `command_not_in_allowlist_rejected` (`tauri::test` MockRuntime invoking a made-up command), `iframe_origin_rejected`, `cancel_drops_connection`, `size_cap_enforced`. Script `Check-Capabilities.ps1` also asserts the command list in `build.rs` equals the list in `capabilities/main.json` and equals 4.3.
- Acceptance: as groom (case home real data, no `127.0.0.1` listener in `Get-NetTCPConnection -OwningProcess <sidecar pid>`; allowlist and origin tests in CI; cancel of a real slow call leaves goroutines flat). Case home real data and cancel are live PC.

### BL-315 Crash capture (P2, M)
- Rust `crash.rs`: `panic::set_hook` writes `crash-<UTC>-rust.txt` (message, location, backtrace, version); `minidumper-child` writes `.dmp` (`MiniDumpNormal`). Go: in `cmd/audit-explorer` `main`, `debug.SetCrashOutput(file, debug.CrashOptions{})` to `CrashDir()/crash-<UTC>-go.txt`, env `GOTRACEBACK=single` set by the shell at spawn. `internal/appdirs.PruneCrashes(max=10, bytes=50<<20)` runs at start of both. ACL via `casestore.SecureDir`. Tray "Open crash folder" via `ui_open_crashes` (opens with `explorer.exe` full path, as `system32File`).
- CI keeps PDBs per release: `release.yml` step uploads `unravis-desktop.pdb` as an artifact (no signing; area 06 wires into release set). Go builds keep symbols off (`-s -w` is not used today; keep as is).
- Tests: `TestPruneCrashesKeepsNewest`, `TestCrashFileACL` (windows), Rust `panic_hook_writes_file`.
- Acceptance: as groom: test build with `--features crash-test` has hidden args `--crash-rust`, `--crash-native`; Go `--crash-go` exists only with tag `e2e`. Capture check by `pktmon` for 0 outbound packets while crashing (live PC); prune and ACL tests in CI.

### BL-316 App dirs (P1, S, rewritten, see D8)
- Add `internal/appdirs`. Create `run`, `webview2`, `crashes` lazily and secure with `casestore.SecureDir`. `locator` and `crash.rs` use them. No data is moved. `internal/datadir` and `storage.go` stay as they are.
- A short note `docs/desktop/data-layout.md` lists the three real layouts and where the shell and the new folders sit.
- Tests: `TestEnsureCreatesSecuredDirs` (windows), `TestRootUsesLocalAppData`, `TestLegacyPathsUntouched` (hash of a fixture legacy tree before and after `Ensure`).
- Acceptance:
  - `go test ./internal/appdirs/...` green. (CI)
  - Old folders are byte-identical after a full desktop run: `tools/desktop/Hash-Tree.ps1 %APPDATA%\APIAuditExplorer` before and after on nachi's PC. (live PC)
  - Prior release still starts and opens the same cases afterwards. (live PC)

### BL-317 Bundle sidecar (P2, S)
- Decision on `externalBin`: not used. Go setup lays down the files; the shell finds `unravis.exe` next to itself (`current_exe().parent()`), no Tauri bundler, no NSIS. `Build-Desktop.ps1` builds Go (`build.ps1`) first, then `cargo build --release`, then copies both into `dist\`, and writes `dist\desktop-files.json` with SHA-256 of each. Stale cache guard is thus gone by design (no `target/` copy of the Go exe).
- `VERSIONINFO` for `unravis-desktop.exe` set from the same `-Version` as `build.ps1` through `build.rs` (`tauri-winres`). Go's `rsrc_windows_amd64.syso` already carries the sidecar's.
- Tests: `Test-Desktop-Build.ps1` builds twice with a changed Go constant and asserts the `unravis.exe` hash in `desktop-files.json` changes and equals the file on disk.
- Acceptance:
  - Two builds, changed Go source: hash changes. (CI)
  - `VERSIONINFO` of both exes equals the build version. (CI)
  - Run from `D:\path with spaces\`: shell finds the sidecar. (live PC)
  - Install over a running old copy with the real `unravis-setup.exe`: both new exes in place (hash check). (elevated/UAC; needs area 06 file list)

### BL-318 CLI and MCP attach (parallel A/B, M)
- `internal/locator` is the only reader. `runMCP` (`cmd/audit-explorer/main.go`) first tries `locator.Read` with `pipes.ag`; falls back to the legacy `mcp.SessionFilePath` plus loopback exactly as today. `internal/mcp` `locator` closure (the `Bridge.locate` func) gets a pipe-aware transport: `http.Transport.DialContext` -> `pipesrv.Dial`, base URL `http://localhost`.
- No owner running: print one line to stderr "Unravis is not running. Start it from the Start menu." and exit 10 (new code, stable). Never starts the GUI.
- Ten tool names, stdio contract unchanged. Same exe stays GUI subsystem (D2).
- Tests: `TestMCPPrefersPipe`, `TestMCPFallsBackToLegacySession`, `TestNoOwnerExit10`, `TestMCPNeverStartsOwner`.
- Acceptance: as groom; real Claude Code and Codex calling the ten tools through the pipe is live PC. `Test-Sidecar.ps1 -Check NoOwner` is CI. Latency with Defender on is written to `docs/desktop/baseline-*.md` by BL-300's script (`-Mcp`).

## 6. Security and footprint checks (this area)

- Pipe: DACL test, remote flag test, first-instance test (BL-301). Name is random per owner start. Peer PID and image are logged on every connect; not a boundary.
- Locator and `run\` ACL: owner-only (BL-302, BL-316). The token lives only there and in the sidecar's memory; never on a command line or in the log (existing `applog.RegisterSecret`).
- The WebView never receives the token from P3 on. In P2 the loopback UI uses the existing launch flow.
- Spawn: only from Rust, fixed path next to the shell exe, fixed args; no path comes from the locator or JS. `Check-Capabilities.ps1` guards shell/fs/http permissions. `grep -r "tauri-plugin" desktop/` must be empty.
- Dependency adds: `go-winio` (Go), Rust crates listed in 3.2. Each goes through area 02's `cargo-deny`, `govulncheck`, SBOM; this area lists them in `desktop/DEPS.md` (one line each: why, license).
- Footprint (area 07 holds the gates; this area reports): sidecar RSS idle in pipe mode must not exceed plain `unravis.exe` idle from BL-300 by more than 2 MB (second listener only). Shell tray-idle private bytes and handle count are recorded by BL-308 soak. No timers faster than 1 s at idle except the 10 s health check; no polling file watchers.
- Tray idle: health check 10 s and state poll 5 s only while the window is open; with window destroyed, health every 10 s stays (supervisor), state poll stops.
- Network: shell and sidecar make no outbound connection in pipe mode other than the existing update feed check (area 06/02 soak).
- Elevated: both exes refuse a full elevated token (D13).

## 7. Open risks left and mitigations

| Risk | Mitigation (no owner needed) |
|---|---|
| `go-winio` cannot set `PIPE_REJECT_REMOTE_CLIENTS` | Wrap `CreateNamedPipe` ourselves for the listener create call only (about 40 lines using `golang.org/x/sys/windows`, as `internal/keeper/pipe_windows.go` already does) and hand the handle to go-winio's accept path; the CI test `TestPipeRejectsRemoteFlag` fails the build if not set. |
| Same-exe sidecar blocks OTA swap while MCP holds it | BL-305 measures; fallback is the split with one const change (D2). Setup already ends "mine" processes. |
| Tauri cold start and RAM on real Windows unknown | BL-300 baseline plus BL-308 soak write numbers; browser mode stays default until area 07 gates pass (DECISIONS section 9). |
| WebView2 retention across destroy/recreate | 50-cycle soak (BL-308); fallback is hide-instead-of-destroy behind a const, with the RAM cost recorded. |
| AV/EDR locks `webview2` dir | On a lock error at window creation, use a fresh sub-folder `webview2\<n>` and prune old ones at start; log it. |
| Rust LOC over 1,500 | Count in CI (`tools/desktop/Count-Rust.ps1`, warn at 1,300, fail at 1,800). |
| Hung sidecar during window destroy | D10 re-checks; supervisor health kill. |
| Browser-mode owner running when shell starts | P2 attach over loopback; P3 switch dialog (D18). Both paths have a test in `Test-Shell.ps1`. |
| Stale locator from a roamed profile | New locator is in `%LOCALAPPDATA%`; creation-time check catches reused PIDs; legacy file keeps its pid check in `mcp`. |
| No second Windows user or Server 2016 box | Checks that need them are tagged live PC; the CI job prints SKIP, not pass; BACKLOG row stays unticked until run. |
| Tauri 3 or plugin API drift | Pin `Cargo.lock`; Dependabot without auto-merge; no plugins used so surface is small. |
| `tauri#15624` Alt+Tab focus bug | BL-309 acceptance step; if it reproduces, add a focus workaround in `window.rs` (re-focus webview on `Focused(true)`) and keep the test. |
