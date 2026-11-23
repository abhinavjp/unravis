# Spec 05: CLI and design parity (BL-380 to BL-397)

Stage 2 (spec). Inputs: groom 05, `DECISIONS-draft.md` (sections 1, 7, 12), research 06, the conflicts and questions of groom 01 to 08, specs 01 to 03 (already written), and the code in `/home/claude/unravis` read on 2026-11-21. Report numbers (versions, dates) are the report's own; not re-verified. Two size numbers below were measured here by cross-compiling to Windows (stripped, no UPX): see 0.

## 0. Where the code differs from the report and groom (read first)

| Report / groom says | Code says | Effect |
|---|---|---|
| `unravis.exe` becomes a console program at the old path (groom BL-382, DECISIONS 7) | `unravis.exe` is built `-H=windowsgui` (`build.ps1`: `$GuiLdFlags`, `$AppLdFlags`). `MCP.md` promises "no console window" for `--mcp`. Spec 01 (0, D2, BL-318) and spec 03 (D3) both keep it GUI and keep `--mcp` in it. | A console `unravis.exe` would flash a window for every MCP host start (a GUI host such as Claude Desktop or Cursor starts it without `CREATE_NO_WINDOW`), for every old shortcut and for sign-in start. D1 below overrules the groom: a separate thin console exe is the CLI. |
| Console flash for old shortcuts needs a fix (groom Q2) | Not a problem under D1: the old exe keeps its subsystem. | Q2 is closed by design. |
| Flag errors exit 2 (spec 03 4.3 says "exit code 2 for flag errors as today") | `main()` sends every `run` error, including `flag` parse errors and `-h`, to `reportFatal`, which exits **1**. The only exit 2 today is `--check-update` ("could not check"). `--quit`: 0 (also "not running"), 1 (cannot ask). | New CLI exit 2 means "usage". The legacy table stays frozen as it is (D5). BL-380 records it; `TestLegacyFlagsFrozen` pins it. Spec 03 4.3 is wrong on this point. |
| `--version` / `version` JSON with MCP revision | `--version` prints one text line (`version.String()`: `Unravis v1.2.3 (commit abc1234, built 2026-11-21)`). Setup runs `exe --version` (`internal/setup/os_windows.go:856`). | `--version` text never changes. `unravis version` is new and separate (D9). MCP revision is not in offline `version` (the thin exe does not link the MCP SDK); it is in `doctor`. |
| "Explicit case and source selectors" for `audit` | The ten MCP tools take `sourceIds` only. `internal/mcp/cases_absent_test.go` pins that MCP exposes no case. | Audit commands take `--source`. A case selector does not exist in the operation; not invented (D10). |
| Offline reader "uses existing readers" | A Blackbox "recording" is a folder: `manifest.json` plus hourly `bb-YYYYMMDD-HH.ndjson` (`internal/blackbox/manifest.go`: `fileNameRe`, `ReadManifest`; `reader.go`: `NewFolderSource`, `ReadSamples`). `internal/blackbox` imports `bbsql` (SQL Server driver), `importer`, `update`. | `--input` is a folder or one `bb-*.ndjson`, not "a file". Linking `blackbox` into the thin exe costs size (measured: 2.5 MB stripped with `audit` only, 5.6 MB with `blackbox`; full `unravis.exe` is 38.5 MB). Decided by spike BL-381 (D12). |
| Payload and installer lay down `unravis.exe` | `build.ps1` payload list is the flat names `unravis.exe`, `unravis-keeper.exe`, `unravis-blackbox.exe` with SHA-256 in `manifest.json`; `internal/setup/plan.go` `copyFiles` places by name. Setup also keeps `dist\api-audit-explorer.exe` as a compat copy until 2026-12-31. | The thin exe cannot be named `unravis.exe` in the flat payload. Payload and dist name is `unravis-cli.exe`; setup places it at `<install>\bin\unravis.exe` (D1). |
| Setup silent install is stable | `unravis-setup.exe --silent` requires `--config setup.json` (`cmd/unravis-setup/main.go`: "--silent needs --config"). | WinGet silent switches need a config path or a new default. BL-396 depends on area 06 for that (honest gap). |
| `/healthz` or CLI route exists | Neither. `GET /api/version` is the cheapest backend call. Routes are a table in `internal/server/routes.go` with `lockedGate`; locked answers HTTP 423. `GET /api/cases` and `GET /api/cases/{id}` exist. | CLI-owned routes are new (`/cli/v1/*`, D6). `cases` ops reuse the existing handlers in process. |
| Capability matrix as data | No such file. MCP allow-list is the 7 paths in `bridge.go` (spec 03 D6 moves it to the backend). | New `internal/clicap` (D8). |

Existing tests that stay and must stay green: `cmd/audit-explorer/stdio_windows_test.go` (`TestGuiBuildHasTheWindowsSubsystem`, `TestGuiBuildVersionGoesToAPipe`, `TestGuiBuildMcpStdioRoundTrip`), `internal/mcp/cases_absent_test.go` (exact ten tools), the 19 known Windows-only `internal/server` failures on Linux (do not grow).

## 1. Purpose and boundaries

Owns:
- The console CLI `unravis` (thin Go exe `cmd/unravis`): command tree, flags, output contract (envelope, error codes, exit map, JSON Lines), static completion, help, `schema`, `version`, `doctor`.
- The CLI side of attach (uses area 01's locator and pipe client) and the owner side of CLI calls: `internal/clisrv` (routes `/cli/v1/*` on the `ag` pipe), `internal/clicap` (capability matrix, deny by default), `internal/ownerlease` (cross-session owner lease).
- `app status|stop|start --headless`; `blackbox inspect --offline`; `grant list|approve|revoke` (minimal, for headless).
- Windows shell test harness, parity CI, the PATH installer step, WinGet manifest, CLI release gate.

Does not own:
- Pipe listener, locator, `--pipe-server`, supervisor, Rust shell (area 01). Result handles, cursors, grants, tiers, audit ledger, tool result shapes (area 03: `internal/mcpsrv`). This area consumes them (D7, D8).
- Code mode CLI (`unravis code ...`, area 04). Its ops must get a matrix row before they exist (D8).
- Installer and OTA mechanics, signing, release file lists (area 06). This area adds one setup step and one file.
- Write operations (case create, import, key action, saved connection, destroy). Not built. Listed in `docs/BACKLOG.md` "Backlog (deferred)" as one line by BL-387's worker (needs owner go).
- Query/template language (`--jq`), dynamic completion, OpenAPI generation, MSI, MSIX alias, portable ZIP. Deferred.

## 2. Resolved decisions

"Spike" means a measurement item decides; the default is used until then.

| # | Question / conflict | Decision | Reason |
|---|---|---|---|
| D1 | Q1, Q2, conflict "same exe or split" (BL-305): what is the CLI binary | Two exes with fixed roles. (a) `<install>\unravis.exe` stays the GUI-subsystem fat program: owner, sidecar, `--mcp`, all legacy flags; not on PATH; not changed by this area. (b) New thin console exe `cmd/unravis`, built `dist\unravis-cli.exe`, installed as `<install>\bin\unravis.exe`. PATH gets `<install>\bin` only. Typing `unravis` runs (b). If BL-305 picks a split `unravis-core.exe`, (a) is renamed once (`setup.AppExeName`) and nothing here changes: the thin exe finds the app exe by one function `clicore.AppExe()`. | GUI exe: no console flash for MCP hosts, shortcuts, sign-in start (the real risk the groom flagged, solved by not touching it). Console exe: the shell waits for it and `$LASTEXITCODE` is set. PATH never shows the GUI exe, so no VS Code-style collision. Overrules groom BL-382 and DECISIONS 7 ("console `unravis.exe` at the old path"); that was written before spec 01 kept the GUI exe. |
| D2 | Q3: GUI launcher name | Not this area's: `unravis-desktop.exe` (area 01). Browser mode needs no launcher (old exe is windowless). | D1. |
| D3 | Q4: Cobra or urfave/cli v3 | Cobra v1.10.2 (pin the newest passing patch at spike time; urfave/cli v3 is the fallback). Cobra stays in `cmd/unravis` and `internal/clicore/cmdtree`; policy, DTO and server packages never import it. Decided by spike BL-381 (PS completion bug #2481, size, start). Defaults used until then. Pass thresholds: thin exe linked size <= 8 MiB, `unravis version` warm p95 <= 150 ms and working set <= 25 MB on nachi's PC. | Report pick; thresholds are mine, to be reset by the BL-380/381 numbers. |
| D4 | Q3: command tree, flags, codes | Fixed in 3.1 (tree), 3.2 (flags), 3.4 (codes and exit map). The ten MCP tools map one to one (3.1 table). `audit activity` and `audit guide` are added (groom omitted them). | Spec stage must fix them. |
| D5 | Legacy flags and exit codes | Frozen. Fat exe behavior does not change; BL-380 records every flag and exit code; `TestLegacyFlagsFrozen` pins them with a real subprocess. The thin exe only forwards three legacy flags: `--version` (handled itself, same text), `--quit`, `--check-update` (run the fat exe with inherited stdio, wait, return its exit code unchanged). Other legacy flags on the thin exe: exit 2 with one line "That option belongs to <install>\unravis.exe". `--mcp` on the thin exe is refused the same way and the line gives the absolute path (hosts must not start a console exe). | "Retain old startup switches"; no new exit-code meaning leaks into old callers. |
| D6 | Transport and which route | CLI uses the `ag` pipe (spec 01 D5) with a second route family `/cli/v1/*`, served by new `internal/clisrv`. `pipesrv.RoleHandlers["ag"]` becomes a 15-line mux (`cmd/audit-explorer/agmux.go`, this area): `/mcp/v1/` to area 03's handler, `/cli/v1/` to clisrv, else 404. Area 03's "only five routes" rule becomes "only these two prefixes" (its test `TestAgPipeOnlyMcpRoutes` allows the prefix). No third listener. The `ui` pipe stays shell-only. | Spec 02 2.9 let the CLI use `ui`; that would give the CLI the shell's route set (shutdown, autostart, launch code). The `ag` pipe is the agent-facing surface and already has the stricter route list. Conflict flagged to areas 01, 02, 03. |
| D7 | Q6, conflict with area 03: who owns store and grants | Area 03 owns result handles, cursors, grants, tiers, ledger (`internal/mcpsrv`). This area builds none of them (BL-388 and BL-389 become integration items). Audit and `sources list` ops run through `mcpsrv.Service.Call` (spec 03 4.4). Numbers are 03's D20: handle TTL 10 min, 4 handles and 64 MiB per profile, page default 20, max 100. | One implementation (spec 03 D15). |
| D8 | Capability matrix, deny by default | Data file `internal/clicap/matrix.go`: rows are op ids, columns are `gui`, `cli_human`, `cli_auto`, `mcp`, `sandbox`; values `allow`, `scoped`, `deny`, `n/a`. `clisrv.Register(op)` panics at init if the op has no row with every column set. The handler refuses an unlisted op id. Rows for area 04 (`code.*`) are added as `deny` in all columns now; area 04 flips its own cells in its PR. Offline ops (`version`, `schema`, `blackbox.inspect`) are `n/a` for `cli_auto`: no backend, so no gate (honest: the OS file rights are the only limit). | Report: every new op needs an explicit allowlist entry per surface. |
| D9 | Output contract | Envelope and rules in 3.3. `schemaVersion` is the string `"1"`. Default `--output table` always (no auto switch by TTY). `--json` is an alias. JSON and JSONL output is 7-bit ASCII (every non-ASCII character is written as `\uXXXX`); valid UTF-8 without BOM by construction. | The PS 5.1 problem (groom BL-383): PS 5.1 decodes native output with the console code page and `>` writes UTF-16; ASCII JSON survives all of that. Table mode is for people. |
| D10 | Q5: which tools first; `cases show` fields | All ten MCP tools become commands (3.1). First slice: `audit compare` (BL-385). `cases show` prints only: `id`, `title` (the text `(locked)` when the case key is locked, as the GUI), `created`, `closed`, `bytes`, `imports`, `envs`, `sources`, `ownKey`, `keyLocked`, `archived` (bool), `missing`, `untrusted`, `history[]` (kind, time). Never: `keyEnding`, disk paths, request id, ledger text, evidence, import content, `mark`. `cases list` has the same fields minus `history`. Both are `deny` for `cli_auto`, `mcp`, `sandbox`. | `CaseInfo` (`casestore/store.go`) fields reviewed one by one; the GUI list shows the same. |
| D11 | Time windows and selectors | Windows are explicit RFC 3339 with offset (`--start`, `--end`, `--baseline-start`, `--baseline-end`). No `now`, no `--last`. Disjoint-window and size rules stay in the existing validator (`audit.ValidateQuery`), run in the owner. The CLI only checks JSON validity, size (1 MiB) and flag-vs-request exclusion. | Reproducible runs; one validator. |
| D12 | Offline reader placement | `blackbox inspect --offline` is linked into the thin exe (`internal/blackbox` readers, no write path called). If BL-381 measures the thin exe over 10 MiB linked, it instead runs through a hidden fat-exe flag `--inspect-recording` with inherited stdio (same code, same tests). Decided by spike BL-381; default is linked. `go list -deps ./cmd/unravis` must show no `internal/server`, `internal/casestore`, `internal/store`, `internal/lite`, `internal/keeper`, `internal/tray`, mongo driver (`TestThinDependencyBoundaries`). | Footprint and attack surface; Lite stays separate. |
| D13 | Q9: snapshot format for offline case inspection | Not built. Offline is Blackbox recordings only. Live case folders are refused. | Case open can heal and write (report); no versioned snapshot exists. |
| D14 | Browser-mode owner (no pipes in the locator) | The CLI keys on `pipes.ag` in the locator, not on `owner`. A plain browser-mode owner has none: error `ENDPOINT_UNAVAILABLE` exit 3, text "Unravis is running without command line access (browser mode). Quit it and run 'unravis app start --headless', or start the desktop app." No loopback fallback. Request to area 01 (not a dependency): if the browser-mode owner also opens the `ag` listener, the CLI works there with no change here. | No grants or policy on the legacy loopback (spec 03 D8); fail closed. |
| D15 | Principal model and Q13 (identity, who holds a grant) | Two principals. (1) Human: no `--profile`. Authenticates by the `ag` pipe's peer policy alone: same logon SID and session, image `unravis.exe` (the thin `bin\unravis.exe` or the app program), checked by the server on every connection, deny by default. There is no token and no proof exchange (decided 2026-11-23, BL-318 option B: the locator carries no token and the ui-pipe hello hands it to the shell only; revisit if the threat model changes, for example for same-user malware, which is out of scope today, D21); policy is the operator policy the GUI has (matrix column `cli_human`). (2) Automation: `--profile cli-<name>` (or env `UNRAVIS_PROFILE`). It is an ordinary area 03 profile with `HostName="cli"`, same enroll, approve, claim flow, credential in DPAPI (`%LOCALAPPDATA%\Unravis\mcp\cred`), calls go to `/mcp/v1/call` through `internal/mcpagent`. Automation may run only the ten audit tools plus `app.status`; every other op answers `PERMISSION_DENIED`. No SID model of my own: a grant holder is any process of the same user that can read the credential file. | One grant system. Same-user malware is out of scope (spec 03 D21). |
| D16 | Approval on a headless box (no GUI) | `unravis grant list\|approve <id>\|revoke <id>`, human principal only. `approve` needs a real console on stdin and stdout (`GetConsoleMode`) and a typed `yes`; it applies area 03's defaults only (tier `aggregate`, all sources connected now, 8 h idle expiry, no identifiers). Widening scope or identifiers is GUI only. Documented as a tripwire against naive agents, not a boundary. | Owner rule: headless must work end to end; an agent in the same account can always read the same files (spec 03 D21). |
| D17 | Q7: PATH scope and folder | PATH scope follows install scope: per-user install writes `HKCU\Environment\Path`, per-machine writes `HKLM\SYSTEM\CurrentControlSet\Control\Session Manager\Environment\Path`. Only `<install>\bin` is added, never the app dir. | No UAC for per-user; dedicated dir keeps the GUI exe off PATH. |
| D18 | Q8: cross-session attach | Not required in v1. Discovery stays per user session (spec 01). Cross-session safety comes from an exclusive owner lease (D19). A CLI in another session finds no live locator in its own profile dir only if the profile dir differs; for the same user the locator dir is shared, so it attaches if alive and proves the peer. Behavior with console + RDP + scheduled task is recorded by BL-390, not promised. | Report: test before claiming. |
| D19 | Owner lease (BL-390) | New `internal/ownerlease`: file `%LOCALAPPDATA%\Unravis\run\owner.lock` opened with `CreateFile` share mode 0 and held until exit (Windows frees it on crash). `--pipe-server` takes it right after `refuseElevated` and before the pipe listeners; busy means sidecar exit 12 (spec 01: "another owner exists"); the CLI maps 12 to `OWNER_BUSY` exit 3. Browser-mode owners do not take it in v1 (not changing today's multi-session behavior). | Closes the "console + RDP both start an owner on one profile" hole for the new owners. |
| D20 | `app stop` and `app start` rules | `app stop` stops only `owner=headless`. For `owner=shell` it answers `PERMISSION_DENIED` "owned by the desktop app; quit it from the tray" (the supervisor would fight it). `app start --headless` starts `<install>\unravis.exe --pipe-server --owner=headless` unelevated, detached (`DETACHED_PROCESS \| CREATE_NEW_PROCESS_GROUP`, plus `CREATE_BREAKAWAY_FROM_JOB` when the parent job allows it, else without), stdio NUL, no window, no browser, never starts the desktop. Idempotent: already running and compatible gives exit 0 with `alreadyRunning:true`. | Headless is the second legitimate parent (spec 01 D1). |
| D21 | Q11: supported hosts and shells | CLI supported on Windows 10 22H2, Windows 11, Server 2016, 2019, 2022, 2025 (console only; no WebView2, no desktop session). Shells: cmd, Windows PowerShell 5.1, PowerShell 7.x, Windows Terminal hosting those, scheduled task, direct `CreateProcess`. Not supported or tested: Git Bash, WSL, Cygwin (they run it as a Windows exe; no claim). CI covers Server 2022 class (`windows-2022`) and 2025 (`windows-2025`). Win 10, Server 2016/2019 rows are "not verified" until a box exists (never "pass"). | No machine assumed. |
| D22 | Q10: WinGet | Manifest built and validated locally (`tools/winget/`); `winget install --manifest` tested. Publishing to the public repo is not done by this area: one deferred line in BACKLOG ("needs owner go and BL-312 signing"). | Owner decision and SmartScreen. |
| D23 | Q12: write operations | None in v1. Each future one needs an owner go and an operation-by-operation matrix row. | BACKLOG rule. |
| D24 | Q14: completion | Static only: `unravis completion powershell\|bash\|zsh\|fish` with fixed command, flag and enum names. No `ValidArgsFunction` or flag-completion callback is registered anywhere that reads the locator, a file or the pipe (Cobra's hidden `__complete` still exists but can only return the static names). `TestDynamicCompletionDisabled` runs `unravis __complete audit ""` with the run dir empty and asserts no file, locator or pipe access. | Report: never query production from completion. |
| D25 | Config and env | No config file in v1. Flags beat env. Allowlisted env only: `UNRAVIS_OUTPUT`, `UNRAVIS_PROFILE`, `UNRAVIS_TIMEOUT`, `NO_COLOR`. Test-only (tag `e2e`): `UNRAVIS_RUN_DIR`. None can relax safety policy. `TestOnlyAllowlistedEnvRead` fails if another `os.Getenv` name appears under `cmd/unravis` or `internal/clicore`. | Report precedence rule; "profile preferences" step is empty. |
| D26 | Exit 10 from spec 01 BL-318 | Not used by the CLI. No owner is `APP_NOT_RUNNING`, exit 3. Spec 01's "exit 10 for one-shots" is dropped for the CLI; the fat exe does not call it either. Code 10 stays reserved. | One exit map. |
| D27 | `retryable` | True only when repeating the same request is safe: `APP_NOT_RUNNING`, `OWNER_BUSY`, `UPDATE_IN_PROGRESS`, `BUDGET_EXCEEDED`, `UPSTREAM_UNAVAILABLE` before the request body was sent. False for any live-query class (`audit.compare`, `requests`, `slowest`, `breakdown`, `activity`) once the body was sent, because a repeat would run the query again (spec 03 3.7 rule). | Report: no blind retry of a live comparison. |
| D28 | Version skew | `hello` carries `proto {major, minor}` and `appVersion`. Same major and owner minor >= CLI minor: proceed (a differing `appVersion` only adds a `meta.warnings` entry). Else `VERSION_INCOMPATIBLE`, exit 5, text "This command line (v1.2.3) and the running Unravis (v1.3.0) cannot talk. Update both to the same version." | Report: negotiate before any request. |

## 3. Design

### 3.1 Components and command tree

```
PowerShell / cmd / script
   unravis.exe  (thin, console, <install>\bin)     cmd/unravis + internal/clicore
     offline: version, schema, completion, help, blackbox inspect --offline, legacy forward
     online : locator.Read -> pipesrv.Dial(ag) -> hello (verify) -> POST /cli/v1/op | /mcp/v1/call
   \\.\pipe\unravis-ag-<sid8>-<nonce32>             (spec 01 D3)
unravis.exe --pipe-server (fat owner, <install>)
   cmd/audit-explorer/agmux.go  -> /mcp/v1/ (area 03) | /cli/v1/ (internal/clisrv)
   internal/clisrv  -> clicap.Gate -> in-process tool code of internal/mcp (audit tools, sources) | built-in ops (app.status); cases and doctor via server.InternalCLI later
```

Command tree (Cobra; one file per group in `cmd/unravis/cmd/`):

| Command | Op id | Needs backend | MCP tool / source of truth |
|---|---|---|---|
| `unravis version` | `version` | no | `version.String()` fields |
| `unravis schema [<op>]` | `schema` | no | `mcp.Catalog()` input schemas plus embedded CLI-native schemas |
| `unravis completion <shell>` | n/a | no | Cobra static |
| `unravis doctor` | `doctor` | partial | locator, hello |
| `unravis sources list` | `sources.list` | yes | `audit_list_sources` |
| `unravis cases list` / `cases show <id>` | `cases.list` / `cases.show` | yes | `GET /api/cases`, `GET /api/cases/{id}` |
| `unravis audit guide` | `audit.guide` | yes | `audit_get_guide` |
| `unravis audit compare` | `audit.compare` | yes | `audit_compare` |
| `unravis audit groups` | `audit.groups` | yes | `audit_get_groups` |
| `unravis audit timeline` | `audit.timeline` | yes | `audit_get_timeline` |
| `unravis audit timelines compare` | `audit.timelines.compare` | yes | `audit_compare_timelines` |
| `unravis audit requests` | `audit.requests` | yes | `audit_get_requests` |
| `unravis audit slowest` | `audit.slowest` | yes | `audit_get_slowest` |
| `unravis audit breakdown` | `audit.breakdown` | yes | `audit_get_breakdown` |
| `unravis audit activity` | `audit.activity` | yes | `audit_get_activity` |
| `unravis app status` / `stop` / `start --headless` | `app.status` / `app.stop` / `app.start` | status: locator only (plus hello if alive); stop: yes; start: no | locator, `--pipe-server` |
| `unravis blackbox inspect --offline --input PATH` | `blackbox.inspect` | no | `internal/blackbox` readers |
| `unravis grant list` / `approve <id>` / `revoke <id>` | `grant.*` | yes | area 03 `mcpsrv.Service` |

The ten tool rows are the exact ten names in `internal/mcp/schema.go` (`audit_get_activity`, `audit_get_guide`, `audit_list_sources`, `audit_compare`, `audit_get_groups`, `audit_get_timeline`, `audit_compare_timelines`, `audit_get_requests`, `audit_get_slowest`, `audit_get_breakdown`). `mcp serve` is not a command (D5).

### 3.2 Flags

Global (all commands): `--output|-o table|json|jsonl`, `--json` (alias of `-o json`), `--request FILE|-` (tool arguments as one JSON object, same shape as the MCP tool `inputSchema`; max 1 MiB; unknown fields and duplicate keys refused by the owner), `--output-file PATH` (json/jsonl only; writes a new file, UTF-8 no BOM, atomic temp + rename, refuses an existing file unless `--overwrite`), `--timeout DURATION` (default 30 s; 330 s for `audit.compare`, i.e. `audit.OperationTimeout` + 30 s; max 1 h), `--profile ID` (automation, D15), `--quiet|-q` (no stderr progress), `--no-color`, `--help|-h`. `--request` cannot be combined with any per-command field flag (exit 2). Exactly one stdin owner: `--request -`.

Per command (field flags build the MCP arguments; repeat a flag for arrays):

| Command | Flags |
|---|---|
| `audit compare` | `--source ID` (repeat, at least 1), `--start`, `--end`, `--baseline-start`, `--baseline-end`, `--kind`, `--outcome`, `--metric`, `--bucket-seconds N`, `--controller`, `--action`, `--operation Controller.Action`, `--user-id N`, `--ui-version`, `--user-agent-contains`, `--user-ip`, `--url-contains`, `--audit-id`, `--status-code`, `--completion`, `--limit 1..100`, `--sort` |
| `audit groups` | `--result-id` (required), `--offset`, `--limit`, `--search`, `--state`, `--sort` |
| `audit timeline` | `--result-id`, `--api-key` (required both) |
| `audit timelines compare` | `--result-id`, `--api-key` (repeat, 1 to 8) |
| `audit requests` | `--result-id`, `--api-key`, `--cursor`, `--limit`, `--max-rows N` (jsonl only) |
| `audit slowest` | `--result-id`, `--cursor`, `--limit`, `--min-ms`, `--max-rows N` (jsonl only) |
| `audit breakdown` | `--result-id`, `--dim`, `--top`, `--include-ip` |
| `audit activity` | `--source ID` (repeat), `--start`, `--end`, `--cursor`, `--limit` |
| `cases show` | positional `<id>` |
| `app start` | `--headless` (required; without it exit 2) |
| `blackbox inspect` | `--offline` (required), `--input PATH`, `--from`, `--to` (RFC 3339, default whole range), `--max-bytes N` (default 256 MiB) |
| `grant approve` / `revoke` | positional profile id |

Names and ranges equal the fields in `internal/mcp/schema.go` and `audit.Query` (`internal/audit/models.go`). A field flag whose tool field does not exist is a build error (`TestFlagsMatchToolSchemas` compares each flag to the tool `inputSchema`).

### 3.3 Output contract

Success (stdout, one object plus newline):

```json
{"schemaVersion":"1","operation":"audit.compare","requestId":"req_3f9a1c2b7d4e5a60","data":{"resultId":"<32 hex>","groups":[],"nextCursor":null},"meta":{"coverage":"complete","identifiers":"hidden","truncated":null,"warnings":[]}}
```

Failure (stdout empty, stderr one object plus newline):

```json
{"schemaVersion":"1","operation":"audit.groups","requestId":"req_3f9a1c2b7d4e5a60","error":{"code":"RESULT_EXPIRED","message":"The comparison result has expired.","retryable":false,"action":"Run audit compare again."}}
```

- `data` is the area 03 `structuredContent` without `schemaVersion` and `tool` (they are hoisted). `meta.coverage` is `complete` or `partial` (from 03 `partial`); `meta.identifiers` is `hidden` or `shown` (the principal's policy). CLI-native ops define `data` in `internal/clicore/schemas/<op>.json` (embedded).
- `requestId` is `req_` + 16 hex from `crypto/rand`, made by the CLI, sent to the owner, logged there (`applog`) for correlation. It is also the "support correlation id" for exit 1.
- Streams: JSON mode: success on stdout only, error on stderr only, no progress. Table mode: tables on stdout, errors as `unravis: <message>` plus an `Action:` line plus `Code:` line on stderr, progress only when stderr is a console and not `--quiet`: one line "Working... (Ctrl+C to cancel)" after 1 s, no spinner, no repaint.
- `--help` output is human text on stdout, exit 0. A usage error in JSON mode uses the error envelope; the mode is read from argv (`-o`, `--json`, `UNRAVIS_OUTPUT`) before Cobra parses the rest (`clicore.PeekOutputMode(args)`), so even `unravis audit nosuch --json` returns one JSON error.
- Ordering is deterministic: whatever order the owner returns, never re-sorted by the CLI.
- JSONL (`audit requests`, `audit slowest` with `-o jsonl`): the CLI pages through the owner cursor itself (no new server protocol). Lines: `{"schemaVersion":"1","operation":"audit.requests","type":"meta","requestId":"..."}`, then `{"...","type":"item","data":{...one row...}}`, then `{"...","type":"complete","coverage":"complete","nextCursor":null,"rows":N}`. A line is one complete JSON object, max 64 KiB (a longer row is cut to the page rule of area 03 `result_too_large` and fails). Output cap 64 MiB (`--max-bytes` lowers it), row cap `--max-rows` (default 1,000, max 10,000), deadline `--timeout`, one page in flight (backpressure: the next page is requested only after the previous lines are written). A failure mid-stream ends with exit non-zero, one error object on stderr, and no `complete` line; earlier lines are provisional. Closed reader pipe (write error `ERROR_NO_DATA`/`EPIPE`): stop, cancel the call, exit 6 `CANCELED`, no stderr noise. Defaults decided by spike BL-392.

### 3.4 Error codes and exit map

| Exit | Meaning | Codes |
|---|---|---|
| 0 | Success (empty result is success; `--allow-partial` is not a flag in v1: a partial result is success with `meta.coverage":"partial"`) | |
| 1 | Internal | `INTERNAL` |
| 2 | Usage / contract | `INVALID_ARGUMENT`, `UNKNOWN_OPERATION`, `UNSUPPORTED_FORMAT`, `REQUEST_TOO_LARGE`, `LEGACY_OPTION` |
| 3 | Dependency unavailable or busy | `APP_NOT_RUNNING`, `ENDPOINT_UNAVAILABLE`, `ENDPOINT_UNTRUSTED`, `STALE_LOCATOR`, `OWNER_BUSY`, `UPDATE_IN_PROGRESS`, `UPSTREAM_UNAVAILABLE`, `BUDGET_EXCEEDED` |
| 4 | Not allowed or needs the operator | `PERMISSION_DENIED`, `APPROVAL_REQUIRED`, `CASE_LOCKED`, `ELEVATION_REFUSED` |
| 5 | Resource gone or contract mismatch | `RESULT_EXPIRED`, `NOT_FOUND`, `RESULT_TOO_LARGE`, `VERSION_INCOMPATIBLE` |
| 6 | Stopped | `CANCELED`, `TIMEOUT` |
| 7 | Incomplete where the caller required full coverage | `INCOMPLETE` (only with `--require-complete`, a global flag; without it a partial result is exit 0) |

Mapping from area 03 codes (`internal/mcpsrv` `Code*`): `invalid_arguments` to `INVALID_ARGUMENT`; `permission_required` to `APPROVAL_REQUIRED` when the profile is pending, else `PERMISSION_DENIED`; `result_expired` to `RESULT_EXPIRED`; `query_budget_exceeded` to `BUDGET_EXCEEDED`; `source_unavailable` to `UPSTREAM_UNAVAILABLE`; `cancelled` to `CANCELED`; `deadline_exceeded` to `TIMEOUT`; `result_too_large` to `RESULT_TOO_LARGE`; `backend_unavailable` to `APP_NOT_RUNNING` (no live locator), `STALE_LOCATOR` (dead pid or creation-time mismatch), or `ENDPOINT_UNTRUSTED` (hello check failed); `internal` to `INTERNAL`. HTTP 423 from a case or master-key lock gate to `CASE_LOCKED`. Sidecar exit codes from `app start` (spec 01): 11 to `ELEVATION_REFUSED`, 12 to `OWNER_BUSY`, 13 to `UPSTREAM_UNAVAILABLE` ("pipe could not start"). Lock vs busy (groom's 3 vs 5 question): lock is 4, busy is 3.

Legacy table (frozen, fat exe, from the code; BL-380 confirms on a real exe): `--version` 0; `--quit` 0 or 1; `--check-update` 0 or 2; any flag or start error 1 via `reportFatal`.

### 3.5 Wire: thin exe to owner (`/cli/v1/*` on the `ag` pipe)

All requests HTTP/1.1, `Host: localhost`, no `Origin`, `User-Agent: unravis-cli/<ver>`, JSON, request max 1 MiB, response max 16 MiB. Dial: `pipesrv.Dial` (spec 01 4.1) with the no-create-instance access mask and SQOS identification as spec 03 3.3.

| Route | Auth | Body in | Body out |
|---|---|---|---|
| `GET /cli/v1/hello` | none | n/a | `{"v":1,"pid":123,"gen":"<the locator startTime>","proto":{"major":1,"minor":0},"appVersion":"1.2.3","owner":"headless","state":"ready"}` (`state`: `ready`, `locked`, `updating`) |
| `POST /cli/v1/op` | none (the pipe's peer policy; request refused with `Origin`) | `{"op":"cases.list","args":{},"requestId":"req_...","callId":"<uuid>","deadlineMs":30000}` | `{"ok":true,"data":{},"meta":{}}` or `{"ok":false,"error":{"code":"...","message":"...","retryable":false,"action":"..."}}` |
| `POST /cli/v1/cancel` | same | `{"callId":"..."}` | `{"state":"cancelling"}` |

- The CLI verifies the server before it sends anything that is not hello (BL-318 option B, no token): `pid` equals locator `pid`, hello `gen` equals the locator `startTime`, `GetNamedPipeServerProcessId` equals it, server image equals `<install>\unravis.exe` and its creation time equals `startTime` (the checks of spec 03 3.3 without the HMAC proof). The pipe itself admits only the same logon SID and session. Any failure: close, nothing sent, `ENDPOINT_UNTRUSTED`.
- Automation principal: `internal/mcpagent.Agent.Call` to `/mcp/v1/call` (spec 03). `clisrv` never sees an automation call.
- Disconnect cancels the call (Go `r.Context()`, as spec 01 BL-314). Ctrl+C: `signal.NotifyContext(os.Interrupt)`, close the connection, best-effort `POST cancel`, exit 6.
- Built: `clisrv.Handler(deps clisrv.Deps) http.Handler`; `Deps{Backend clisrv.Backend; Info func() Info; State func() string; Now; Logf}`; the backend is `clisrv.MCPBackend(mcp.NewLocal(dashboardHandler, token, mcp.WithIdentifiers(true)))` until area 03 lands `mcpsrv.Service`. Planned (not built): `Server`, `Shutdown` and `Server.InternalCLI` for cases and app stop. `Server.InternalCLI(ctx, method, path, body, maxOut)` (new, `internal/server/cli_internal.go`) has its own allow-list: `GET /api/version`, `GET /api/info`, `GET /api/sources`, `GET /api/cases`, `GET /api/cases/{id}`; it injects the session token itself, caps output at 16 MiB, and runs the same `dispatch` (lock gate included). Everything else is refused (`TestInternalCLIAllowlist`). If area 03's `Internal` has landed, both call one shared inner function; whoever lands second does that refactor.
- Human principal and identifiers: operator policy shows identifiers (`meta.identifiers:"shown"`); the BL-393 parity run compares against an MCP profile with identifiers on, never against the hidden default.

### 3.6 Data shapes (CLI-native ops)

`version` (offline): `{"app":"Unravis v1.2.3","version":"1.2.3","commit":"abc1234","date":"2026-11-21","cliSchema":"1","proto":{"major":1,"minor":0},"role":"cli","exe":"C:\\...\\bin\\unravis.exe"}`.
`doctor`: `{"cli":{version...},"installDir":"...","appExe":"...","binOnPath":true,"mcpCommand":"C:\\...\\unravis.exe --mcp","locator":{"state":"running|not_running|stale|browser_mode|unusable_version","owner":"headless|shell|browser","pid":123,"started":"...","appVersion":"..."},"backend":{"reachable":true,"proto":{"major":1,"minor":0},"state":"ready|locked|updating","mcpRevision":"2025-11-25"}}`. No token, no pipe name, no connection string, no case title (`TestDoctorNoSecrets` scans output and logs). No network: only the local pipe and file reads.
`app status`: `{"state":"running|not_running|stale|browser_mode","owner":"...","pid":123,"started":"...","appVersion":"...","proto":{...}}`. Works with no backend (locator only); adds `proto` and `appVersion` only after a verified hello.
`app start`: `{"started":true,"alreadyRunning":false,"pid":123,"owner":"headless"}`.
`blackbox inspect`: `{"input":"<path as given>","kind":"folder|file","manifest":{"version":1,"host":"...","state":"...","firstSample":"...","lastSample":"...","files":12,"gaps":0,"events":0},"samples":{"count":1234,"from":"...","to":"..."},"warnings":[]}` (field names follow `blackbox.Manifest`; build worker takes the exact names from `internal/blackbox/manifest.go`). It prints counts and time ranges, not process command lines or file contents; an optional `--samples N` (max 1,000) adds the first N parsed samples as the reader returns them.
`grant list`: `[{"id":"cli-ci","state":"active","tier":"aggregate","expiresAt":"..."}]` (no secrets, no digests).

Matrix v1 (initial, in `matrix.go`; `n/a` = no backend):

| op | gui | cli_human | cli_auto | mcp | sandbox |
|---|---|---|---|---|---|
| version, schema | n/a | allow | n/a | n/a | n/a |
| sources.list | allow | allow | scoped | scoped | deny |
| audit.* (ten tools) | allow | allow | scoped | scoped | deny (area 04 flips) |
| cases.list, cases.show | allow | allow | deny | deny | deny |
| doctor | n/a | allow | deny | deny | deny |
| app.status | n/a | allow | allow | deny | deny |
| app.start, app.stop | n/a | allow | deny | deny | deny |
| grant.* | allow | allow (TTY) | deny | deny | deny |
| blackbox.inspect | n/a | allow | n/a | n/a | n/a |
| code.* | deny | deny | deny | deny | deny |

### 3.7 Sequences

A. Normal read (`unravis cases list -o json`): `PeekOutputMode` -> locator.Read -> `pipes.ag` present? (else D14 error) -> `locator.Alive` -> Dial -> hello verify -> `POST /cli/v1/op` (no token, BL-318 option B) -> clisrv: `clicap.Check(op, cli_human)` -> `InternalCLI GET /api/cases` -> projection to D10 fields -> envelope -> stdout, exit 0.

B. Audit with follow-up: `audit compare --source S1 --start ... --end ... --baseline-start ... --baseline-end ... -o json` -> (human) `/cli/v1/op` -> `mcpsrv.Service.Call(Principal{cli}, "audit_compare", args)` -> envelope with `resultId`. Then `audit groups --result-id <id> --limit 25` -> same owner, handle still in owner memory (new CLI process, same handle) -> no second source query. After 10 min, restart or revoke: `RESULT_EXPIRED` exit 5.

C. Automation: `unravis audit compare --profile cli-ci ...` -> `mcpagent.Agent` -> pending: `APPROVAL_REQUIRED` exit 4, text "Approve cli-ci in Unravis > AI access, or run 'unravis grant approve cli-ci' in a console." -> approved -> next call claims and runs. Revoked: `PERMISSION_DENIED`.

D. Headless start: `unravis app start --headless` -> `locator.Read`: live and compatible? print `alreadyRunning`. Else spawn `<install>\unravis.exe --pipe-server --owner=headless` (D20) -> poll locator for a new live pid with `pipes.ag` every 100 ms up to 15 s -> verify hello -> print. Sidecar exits 11/12/13 map per 3.4. GUI launched later attaches to it (spec 01 step 2).

E. `app stop`: human, owner `headless` only -> op `app.stop` -> clisrv calls `Deps.Shutdown` (the same cancel as `--quit`) -> `202 stopping` -> CLI waits for the pid to exit (5 s, 2 s sidecar drain per spec 01 BL-303) -> exit 0, else `TIMEOUT`.

F. Offline: `blackbox inspect --offline --input D:\copy\bb` -> never touches locator, profile or network; opens read-only; refuses a path that contains `cases` data (a `case.db`/sealed case marker or a `.sqlite` file) with `INVALID_ARGUMENT`-class code `LIVE_CASE_REFUSED` (exit 2); size limits.

G. Update while running (BL-395): owner enters update hand-off -> new calls get `UPDATE_IN_PROGRESS` (exit 3); running calls are cancelled (`UPDATE_IN_PROGRESS`); headless owner is stopped and started again on the new version by setup (spec 01 4.5 stop contract plus a "start headless again" step, BL-395).

### 3.8 Failure modes and what the user sees

| Failure | CLI shows (table mode text; JSON gives the same code) | Exit |
|---|---|---|
| No locator or dead pid | "Unravis is not running. Start the app, or run 'unravis app start --headless'." | 3 |
| Browser-mode owner | D14 text | 3 |
| Locator from a newer format (`v` too big) | "Update Unravis: this command line is older than the running app." `VERSION_INCOMPATIBLE` | 5 |
| Wrong pipe peer / bad proof | "Unravis did not prove who it is. Restart Unravis." No token sent. | 3 |
| App or case locked | "Unravis is locked. Unlock it in the app." (`CASE_LOCKED`) | 4 |
| Automation profile pending | see C | 4 |
| Bad request file / flag | one-line reason, no echo of values; `--help` hint | 2 |
| Result expired / replaced / restart | "That result has expired. Run 'unravis audit compare' again." | 5 |
| Ctrl+C | "Canceled." (JSON: error object) | 6 |
| Deadline | `TIMEOUT`, no auto retry | 6 |
| Owner killed mid-call | `UPSTREAM_UNAVAILABLE`, `retryable:false` for live classes | 3 |
| Double-click on `bin\unravis.exe` from Explorer (sole process on its console, `GetConsoleProcessList` = 1) | Short text "This is the command line. Open PowerShell and type: unravis --help" and "Press Enter to close." | 0 |
| Output pipe closed | silent stop | 6 |

## 4. Interfaces other areas rely on (exact names)

4.1 Files and names. Installed: `<install>\unravis.exe` (fat, unchanged), `<install>\bin\unravis.exe` (thin CLI). Build and payload: `dist\unravis-cli.exe`, payload and `manifest.json` entry `unravis-cli.exe`. Go constants in `internal/setup/layout.go`: `CLIExeName = "unravis-cli.exe"`, `CLIInstalledName = "unravis.exe"`, `CLIDirName = "bin"`; `Layout.CLIDir`, `Layout.CLIExe`. Area 06 adds `unravis-cli.exe` to release file lists, signing, `verify-release`; process detection ("mine" by image path, `internal/setup/plan.go` `runningApps`) includes `Layout.CLIExe`.

4.2 Go packages (new): `cmd/unravis` (main, `legacy.go`, `cmd/*.go`), `internal/clicore` (`envelope.go`, `codes.go`, `exit.go`, `output.go`, `table.go`, `peek.go`, `attach.go`, `hello.go`, `paths.go`, `schemas/*.json`), `internal/clisrv` (`handler.go`, `ops.go`, `cases.go`, `app.go`, `grant.go`, `registry.go`), `internal/clicap` (`matrix.go`), `internal/ownerlease` (`lease_windows.go`, `lease_other.go`), `internal/server/cli_internal.go`, `cmd/audit-explorer/agmux.go`. Small exported additions: `mcp.Catalog()` (read-only copy of `tools()`), used by `schema` and `TestFlagsMatchToolSchemas`.

4.3 For area 01: (a) `pipesrv.RoleHandlers["ag"]` is set from `agmux.go` (this area writes it; `pipeserver.go` calls `newAgMux(...)` in one line). (b) Pipe peer image rule (spec 01 4.7, spec 02 2.8) must accept the client image `<server install dir>\bin\unravis.exe` as well as `unravis.exe` and `unravis-desktop.exe` in the install dir. Area 01 owns `pipesrv.AllowedImage`; this area adds only a test `TestCLIImageAllowed`. (c) `--pipe-server` calls `ownerlease.Acquire` (D19); busy is exit 12. (d) `app start` relies on flags `--pipe-server --owner=headless` and exit codes 11, 12, 13 exactly as in spec 01 3.3. (e) Exit 10 is unused (D26). (f) The locator shape is the built v2 (`internal/locator`): `ag` (pipe name), `pid`, `startTime`, `exe`, `appVersion`. It has no `token`, `owner` or `startedFiletime` (BL-318 option B); where this spec says `pipes.ag` it means a non-empty `ag`.

4.4 For area 02: CLI is a pipe client only; it dials with the no-create-instance mask and SQOS identification, verifies the server before sending the token. Allow-list entry for the thin image (4.3b). The `bin` dir inherits the install dir ACL (per-machine: admin-only write). Release scan adds: `unravis-cli.exe` has no `e2e` strings (`UNRAVIS_RUN_DIR`).

4.5 For area 03: `clisrv` calls `mcpsrv.Service.Call`, `Authorize`, `NewHandle`, `Resolve` (spec 03 4.4) and the new `Service.ApproveDefaults(ctx, id)` (tier aggregate, all sources, 8 h, no identifiers) and `Service.Revoke`, `Service.ListProfiles` (same code the GUI routes use). Profile ids starting `cli-` have `HostName="cli"`; the AI access screen shows them under host kind "cli" and its copy text for kind `cli` is `unravis --profile <id> <command>`. `TestAgPipeOnlyMcpRoutes` allows the `/cli/v1/` prefix. Result `Envelope` and `Code*` names are used as is.

4.6 For area 04: ops `code.run`, `code.types`, `code.replay` must register through `clisrv.Register` with a matrix row (D8). The CLI group `unravis code` lives in area 04's file `cmd/unravis/cmd/code.go`; it uses the same envelope and exit map. `code.*` is `deny` everywhere until area 04's PR flips its cells.

4.7 For area 06 (installer, OTA): setup step id `cli-path` after `copy-files` (and before `shortcuts`); new `UndoKind` `UndoPathEntry = "path-entry"` (fields `Root`, `Key`, `Name`, `Path`): the undo removes only that entry from the value (never restores a whole old string, so a PATH change by another installer in between is kept). `copyFiles` stages `unravis-cli.exe` and places it at `Layout.CLIExe`; `programFolder` creates `bin`. Stop contract (spec 01 4.5) plus: CLI processes are short; a running `bin\unravis.exe` that blocks the swap is ended by the existing `closeApp` terminate path after `askQuitRounds` (it holds no data). `dist\api-audit-explorer.exe` compat copy is untouched.

4.8 For area 07 (perf): thin exe budgets (D3). Cold start of `unravis version` and working set recorded by BL-380/381 and gated by BL-397's report; no resident process, no timers. Installer budget: thin exe counts toward the 25 MB (compressed size recorded by BL-382).

4.9 For area 08 (CI): this area adds job `cli-windows` to `.github/workflows/ci.yml` (`windows-2022`, same pin as today), steps: build thin exe, `go test ./cmd/unravis/... ./internal/clicore/... ./internal/clisrv/... ./internal/clicap/... ./internal/ownerlease/...`, `tools/cli/Test-CliShells.ps1 -Shell cmd,ps51,ps7,sched`. Windows Terminal, Explorer launch and Ctrl+C steps are live PC. One Windows workflow file hosts BL-383, BL-393 and area 08's jobs; the shared runner setup is area 08's.

4.10 Contract artifacts (stable names): `docs/cli/exit-codes.md`, `docs/cli/output-contract.md`, `docs/cli/commands.md` (generated by `go run ./tools/cli/gendocs`, also the golden `cmd/unravis/testdata/commands.golden.txt`), `docs/user/cli.md` (PowerShell-first examples). Worker adds these.

## 5. Build notes per BL item

Order inside the area: (wave 0, no other area needed) BL-380 -> BL-381 -> BL-382 -> BL-384 -> BL-383 -> BL-391. (wave 1, needs area 01 P1: BL-301, BL-302, BL-318) clisrv skeleton + BL-389a (matrix and registry) -> BL-390 -> BL-387. (wave 2, needs area 03 BL-343, BL-344, BL-346) BL-385 -> BL-386 -> BL-388 -> BL-389b -> BL-392 -> BL-393. (P4) BL-394 -> BL-395 -> BL-396 -> BL-397. BL-389 is split: 389a (data, deny-by-default) starts in wave 1; 389b (automation principal on area 03 profiles) in wave 2. Wave 0 and the offline parts of wave 1 start at once. Acceptance markers: (CI), (live PC), (elevated). "Live PC" = nachi's PC with real cases and real sources; fixtures only add (owner rule).

### BL-380 Baseline: today's flags and shells (P0, M)
- Files: `tools/cli/Measure-Cli.ps1` (runs the real fat `unravis.exe`; uses the installed copy or `-Exe`), `tools/cli/Find-Callers.ps1` (repo grep plus registry, Startup folder, Start menu, scheduled tasks, MCP client configs: `.mcp.json`, `.codex/config.toml`, `~/.claude.json`, Claude Desktop, Cursor, VS Code user files), `cmd/audit-explorer/legacy_flags_test.go` (`TestLegacyFlagsFrozen`: real subprocess, table of flag, args, exit code, stdout/stderr rule, from 3.4 "legacy table"; reuses `buildGuiExe`).
- Output: `docs/desktop/cli-baseline.md`: table shell (cmd, PS 5.1, PS 7, Explorer, Windows Terminal) x launch method x (waits for exit, exit code visible, stdout capture, redirection); caller list with paths and arguments (repo callers found by this spec: `internal/setup/window_windows.go:56` `--background`, `internal/setup/os_windows.go:856` `--version`, `internal/setup/profilelinks.go:22` `--quit` link, `internal/shelllink` `--background`, `internal/autostart`, `MCP.md`, docs/user); `--version` cold and warm start and working set with hardware noted; the actual flag-error exit code (1).
- Tests: `TestLegacyFlagsFrozen`, `TestCallerListMatchesRepo` (script output vs a grep of the repo in a Go test, so a new caller added later fails CI until listed).
- Acceptance: `pwsh tools/cli/Measure-Cli.ps1 -Report docs/desktop/cli-baseline.md` on nachi's PC writes the table with three runs per row (live PC); `go test ./cmd/audit-explorer -run TestLegacyFlagsFrozen` green (CI); `pwsh tools/cli/Find-Callers.ps1` lists repo and PC callers (CI for repo, live PC for PC).

### BL-381 Spike: framework, size, offline-reader placement (Parallel B, S)
- Throwaway branch `spike/cli-framework`. Build the same three-command stub (`version`, `cases list` fake, `completion`) in Cobra and urfave/cli v3, plus a stdlib baseline. Measure linked size, cold start (`Measure-Command`, 20 runs), working set. Test PowerShell completion with 0, 1 and many candidates and quoted input on PS 5.1 and PS 7 (Cobra #2481). Also build the stub with `internal/blackbox` imported to decide D12. `govulncheck` on the chosen graph; licences; pin versions.
- Script `tools/cli/Spike-Framework.ps1`. Output `docs/desktop/spike-bl381.md` with a line `Decision: cobra|urfave` and `Offline reader: linked|forwarded`.
- Defaults until the note exists: Cobra, linked. Check: `tools/desktop/Test-DocsHave.ps1 spike-bl381.md "Decision:"` (script from area 01 BL-305; if absent, this item adds an equal 5-line one).
- Acceptance: as groom (completion works in both PowerShells or fallback chosen and shown; size, start, working set recorded for each; `govulncheck` clean). (live PC; `govulncheck` CI)

### BL-382 Thin console exe, legacy forward (Parallel B, M)
- Add `cmd/unravis/main.go`, `cmd/unravis/legacy.go` (explicit table: `--version`, `--quit`, `--check-update` handled; `--mcp` and every other legacy flag refused, D5), `internal/clicore/paths.go` (`AppExe()`, `InstallDir()` from `os.Executable()`: `<dir>\bin\unravis.exe` -> `<dir>\unravis.exe`; dev layout `dist\bin\...` also valid via the same rule; error if the app exe is missing), `internal/clicore/exit.go` (`func Main(args, env, stdio) int`). Console-only friendly text on Explorer launch (3.8).
- `build.ps1`: after the fat build add `go build -trimpath -ldflags "-s -w $LdFlags" -o dist\unravis-cli.exe ./cmd/unravis` (no `-H` flag) and add `unravis-cli.exe` to the payload loop and `manifest.json`. `internal/setup/layout.go` constants (4.1). VERSIONINFO and icon: a second `.syso` made by `tools/icon/main.go` (it accepts several outputs: `go run tools/icon/main.go <dir> <syso-out> [<syso-out> ...]`); commit `cmd/unravis/rsrc_windows_amd64.syso`.
- Fat exe: no code change. `TestGuiBuild*` stay.
- Tests: `TestThinBuildIsConsoleSubsystem` (debug/pe, subsystem 3), `TestFatBuildStillGUI` (existing test, kept), `TestThinVersionTextSameAsFat` (byte equal), `TestLegacyForwardExitCodeQuit`, `TestLegacyForwardExitCodeCheckUpdate` (needs no network: use `--demo`-free path; assert exit mapping with an injected runner), `TestMcpRefusedOnThin` (exit 2, text has the absolute app path), `TestThinDependencyBoundaries` (D12), `TestThinFindsAppExeNextDir`, `TestThinSpacesAndNonAscii` (run from `D:\path with spaces\ünï\bin`).
- Acceptance (runnable): `go test ./cmd/unravis/... -run "TestThin|TestLegacy|TestMcpRefused"` (CI); `.\build.ps1` makes `dist\unravis-cli.exe` and `dist\unravis.exe`; in PowerShell `& dist\unravis-cli.exe --version; $LASTEXITCODE` prints the same line as `dist\unravis.exe --version` (live PC); `unravis.exe --mcp` stdio round trip from a real MCP host still passes and shows no console window (live PC, one host minimum); double click `bin\unravis.exe` shows the help text and closes on Enter (live PC); old Start menu shortcut and old MCP config still work without change (live PC). Linked size and compressed installer delta recorded in `docs/desktop/cli-baseline.md`.

### BL-383 Windows shell test harness (Parallel B, M)
- `tools/cli/Test-CliShells.ps1 -Shell cmd|ps51|ps7|sched|direct|all -Exe <path> -Report <file>`, plus `tools/cli/ctrlc/main.go` (test helper: starts a child in a new console group and sends CTRL_C), batch file `tools/cli/cases.cmd`. Checks (each prints PASS/FAIL/SKIP with the reason; SKIP is never counted as pass): C1 prompt returns only after exit (timing against a `sleep` op: `unravis __test sleep 3` exists only in builds with tag `e2e`); C2 `ERRORLEVEL`/`$LASTEXITCODE` equals 0, 2, 3 (`unravis nosuchcmd`, `unravis app status` with no owner); C3 JSON success on stdout only, error on stderr only; C4 stdin closed with `--request -` fails fast (5 s) with `INVALID_ARGUMENT`; C5 bytes: output is 7-bit ASCII, no BOM, and `ConvertFrom-Json` / `jq`-free parse works; C6 redirect `> file` in cmd, PS 5.1 (`>` gives UTF-16 text), PS 7: documented outcome, and `--output-file` bytes exact; C7 spaces and non-ASCII in `--request` path; C8 Ctrl+C during a real backend call exits 6 and the owner call ends (live PC); C9 Explorer launch of the fat exe opens no console (`conhost.exe` child count) and of the thin exe shows the friendly text (live PC); C10 scheduled task runs and exit code visible with `schtasks /query /v /fo list`; C11 Windows Terminal hosted shells repeat C1 to C3 (live PC); C12 combined `2>&1` caller opt-in doc check.
- CI job `cli-windows` (4.9) runs C1 to C7, C10 on cmd, PS 5.1, PS 7 present on `windows-2022`.
- Tests: Pester-free; the script's own `-SelfTest` runs the checks against a stub exe `tools/cli/stub/main.go`.
- Acceptance: `pwsh tools/cli/Test-CliShells.ps1 -Shell all -Report docs/desktop/cli-shells-<date>.md` on nachi's Win 10/11 PC all PASS (live PC); same in CI for the shells present (CI); the report lists PS 5.1 redirected-JSON result (ASCII, so valid; `>` UTF-16 documented) (live PC).

### BL-384 Output contract and offline commands (Parallel B, M)
- Files: `internal/clicore/{envelope,codes,exit,output,peek,table}.go`, `cmd/unravis/cmd/{root,version,schema,doctor_offline,completion}.go`, `internal/clicore/schemas/*.json`, `docs/cli/*.md` (4.10). Cobra setup: `SilenceUsage`, `SilenceErrors` (all errors go through `clicore.Render`), custom exit via `clicore.Main` (never `os.Exit` in commands). JSON writer: `json.Encoder` with `SetEscapeHTML(false)` then an ASCII-escape pass (`clicore.asciiJSON`) so output is 7-bit. `--help`, `version`, `schema`, `completion` run before any locator, profile, registry or network code (guard: `clicore.Offline` flag on those commands; `TestOfflineCommandsTouchNothing` runs them with `UNRAVIS_RUN_DIR` pointing at an empty dir and a `os.Stat` hook, asserting no access).
- `schema <op>`: `{"operation","input":<JSON Schema 2020-12>,"output":{...},"errors":[...],"examples":[...]}`; audit input schemas come from `mcp.Catalog()`; CLI-native ones from embedded files.
- Tests: golden files `cmd/unravis/testdata/golden/*` (`-update` to rewrite): `version_json`, `schema_audit_compare`, `usage_error_json` (exit 2, stdout empty, one stderr object), `unknown_format`, `help_root`, `commands.golden.txt`; `TestExitMapComplete` (every code has exit and retryable), `TestAsciiJSON` (non-ASCII, U+2028, surrogate pairs), `TestNoBOM`, `TestPeekOutputMode`, `TestOnlyAllowlistedEnvRead` (D25), `TestDynamicCompletionDisabled` (D24), `TestRequestFlagExclusive`, `TestRequestTooLarge`, `TestFlagsMatchToolSchemas`.
- Acceptance: golden tests green (CI); `unravis version -o json | ConvertFrom-Json` parses from PS 5.1, PS 7 and cmd (via `powershell -c`) (live PC); `Test-CliShells.ps1 -Check Tracing` plus Process Monitor (or `tools/cli/Trace-Offline.ps1` using `logman`/ETW file events) on nachi's PC with network off shows no file, registry or network access beyond reading the exe for `version`, `schema`, `--help`, `completion` (live PC).

### BL-385 Operation layer and first slice: `audit compare` (Parallel B, needs P1 and area 03, L)
- Files: `internal/clisrv/{handler,ops,registry}.go`, `internal/server/cli_internal.go`, `cmd/audit-explorer/agmux.go`, `cmd/unravis/cmd/audit.go`, `internal/clicore/attach.go`. Human `audit.compare` calls `mcpsrv.Service.Call(Principal{ID:"cli"}, "audit_compare", args)`; the GUI `/api/analyze` route and MCP keep their own adapters over the same owner code (no behavior change; area 03 owns that refactor). If area 03 BL-343/344b is late, this item does not wait: it ships `Backend` interface `clisrv.Backend{Call(ctx, p, tool string, args json.RawMessage) (Result, error)}` with the `mcpsrv.Service` implementation, and lands the wave 0 and wave 1 items first.
- Tests: `TestCompareArgsFromFlagsEqualRequestFile`, `TestCompareEnvelopeEqualsMcpStructured` (strip `schemaVersion`, `tool`; compare), `TestCompareNoOwnerExit3`, `TestCompareBrowserModeOwnerEndpointUnavailable`, `TestCompareDisjointWindowRejected` (exit 2, owner validator message), `TestCancelEndsHandler` (goroutine count back to baseline, as spec 01 BL-314), `TestPrincipalNeverFromRequestJSON` (a `principal` field in args is rejected by strict args).
- Acceptance: same request gives equal canonical results through GUI HTTP (`/api/analyze`), MCP and CLI on a saved fixture case (compare values and order) (CI); on nachi's PC with a headless owner on a real source: `unravis audit compare --request .\incident.json -o json` returns a real comparison, the app closed gives exit 3 `APP_NOT_RUNNING`, and the three adapters agree on the real case (live PC); `go test ./internal/mcp/...` exact-ten-tools test and `go test ./internal/server/...` failures stay equal to the saved Linux list `docs/desktop/baseline-go-tests.txt` (spec 01 BL-301) (CI).

### BL-386 Remaining audit commands (Parallel B, M)
- `cmd/unravis/cmd/audit_*.go`, one file per command, flags per 3.2. Help examples are PowerShell-first. `audit guide` prints the guide text from the owner (`audit_get_guide`); `audit --help` mentions it.
- Tests: `TestCommandsCoverTenTools` (every name in `mcp.Catalog()` has exactly one command; a new MCP tool fails CI until mapped), `TestHelpSnapshot` (golden per command), `TestSchemaSnapshot` (op ids and JSON Schema in `testdata/contract.json`), `TestBadWindowExit2`, `TestUnknownFlagExit2`, `TestOversizeRequestExit2`.
- Acceptance: each command on nachi's real case returns real data equal to the MCP result of the same tool (live PC); snapshots (CI); the three error cases exit 2 with a code (CI).

### BL-387 `sources`, `cases`, `doctor` (Parallel B, M)
- `internal/clisrv/cases.go` (projection D10 before serialization), `cmd/unravis/cmd/{sources,cases,doctor}.go`, backlog line for deferred write ops added to `docs/BACKLOG.md`.
- Tests: `TestCasesProjectionFields` (golden list of allowed keys; a new `CaseInfo` field does not appear until added), `TestCasesNoKeyEnding`, `TestCasesLockedKeyShowsLockedTitle`, `TestCasesMasterKeyLockedExit4` (423 to `CASE_LOCKED`), `TestDoctorNoSecrets`, `TestDoctorNoNetwork` (dial hook counts only pipe dials), `TestSourcesListEqualsMcpTool`.
- Acceptance: lists equal the GUI for nachi's real sources and cases (live PC); a locked case returns `CASE_LOCKED` and exit 4 and no title (live PC); `doctor` output and logs have no token, key, connection string (CI scan with planted markers).

### BL-388 Result handles and cursors, CLI side (Parallel B, M)
- No store code (D7). Add `--result-id`, `--cursor`, `RESULT_EXPIRED` mapping, `audit.requests` and `audit.slowest` paging, doc of the 10 min life. Add test `TestFollowUpAcrossProcesses` (two CLI processes against one test owner).
- Acceptance: `audit compare` then `audit groups --result-id` on a real case pages with no second source query (source-side query count from the source's own log or a counting proxy) (live PC); expiry, restart, revoke, other principal each give exit 5 or 4 (CI, uses area 03's store); memory cap and release on shutdown are area 03's tests, referenced here.

### BL-389 Capability matrix, automation principal (Parallel B, L)
- 389a: `internal/clicap/matrix.go`, `clisrv.Register`, `TestUnlistedOpDenied`, `TestEveryRegisteredOpHasFullRow`, `TestMatrixMatchesDoc` (table in `docs/cli/commands.md` generated from the matrix), `TestMcpToolsAllHaveRows` (names from `mcp.Catalog()`).
- 389b: `--profile` path through `internal/mcpagent`, `grant` commands, `Service.ApproveDefaults`. Tests: `TestAutoPrincipalCannotCallCases`, `TestAutoPrincipalDeniedIdentifierFilter`, `TestGrantApproveNeedsConsole` (stdin not a console gives exit 2), `TestGrantApproveDefaultsOnly`, `TestTokenNotInArgsEnvStdoutStderrLogs` (scan a full session; `applog.RegisterSecret` for each secret).
- Acceptance: a new op registered without a row cannot be called from any surface (CI); automation profile without grant: denied, exit 4 (CI); same on the real pipe from a second process of the same user: denied without grant, allowed after `grant approve`, denied after `revoke` (live PC); token never in process list, environment dump, stdout, stderr, logs (CI scan; live PC with `Get-CimInstance Win32_Process`).

### BL-390 `app status|stop|start --headless` and owner lease (Parallel B, needs P1, L)
- `internal/ownerlease` (D19), `internal/clisrv/app.go`, `cmd/unravis/cmd/app.go`, `internal/clicore/spawn_windows.go` (D20 flags), `spawn_other.go`.
- Tests: `TestLeaseExclusive` (windows: second `Acquire` busy), `TestLeaseFreedOnKill` (windows: kill the holder, `Acquire` succeeds), `TestAppStartIdempotent`, `TestAppStartMapsExit11To12To13`, `TestAppStopOnlyHeadless`, `TestAppStatusStaleLocator` (kill, status says stale and attaches to nothing), `TestAppStartNeverStartsDesktop` (no `unravis-desktop.exe` in the process list), `TestAppStartDetached` (parent exit does not kill the owner; job-breakaway fallback).
- Acceptance: `unravis app start --headless` then the desktop shell attaches and shows the same cases (live PC; needs area 01 P2); two concurrent `app start --headless` leave exactly one owner and the other prints `OWNER_BUSY` exit 3 (CI `Test-CliShells.ps1 -Check TwoStarts`, live PC); console and RDP session of the same user: record one owner or busy in the report (live PC); elevated shell: `ELEVATION_REFUSED` exit 4 (elevated); killed owner: `app status` says `stale`, new start works (CI).

### BL-391 Offline reader (Parallel B, M)
- `internal/clicore/offline_blackbox.go` using `blackbox.ReadManifest`, `NewFolderSource`, `ReadSamples` (build worker confirms by reading `readManifest` and `FolderSource` that no write path runs; the test below proves it). `cmd/unravis/cmd/blackbox.go`. Refuse: a path that is a live case dir or has `.sqlite`/sealed case files; path not a folder or `bb-*.ndjson`; over `--max-bytes` (default 256 MiB total); symlinks resolving outside; UNC path allowed read-only only if given explicitly. Never starts a service, never touches Key Keeper.
- Tests: `TestOfflineDoesNotWrite` (hash and list the input tree before and after, temp copy of a generated recording), `TestOfflineRefusesLiveCaseDir` (exit 2 `LIVE_CASE_REFUSED`, directory listing unchanged), `TestOfflineOversizeMalformed` (truncated ndjson, bad manifest, huge line: exit 2, no panic), `FuzzOfflineManifest` (nightly only).
- Acceptance: a real recording copied from a real PC's Blackbox folder is read and the input hash is unchanged (`Get-FileHash` on every file before and after) (live PC); the other two (CI).

### BL-392 JSON Lines streaming (Parallel B, S)
- `internal/clicore/stream.go` per 3.3. Ops: `audit.requests`, `audit.slowest`. Constants `MaxLineBytes=64<<10`, `MaxOutputBytes=64<<20`, `DefaultMaxRows=1000`, `HardMaxRows=10000`.
- Tests: `TestJsonlFraming`, `TestJsonlNoCompleteOnFailure`, `TestJsonlClosedReader` (reader closes after 3 lines; CLI exits 6; owner call context ends), `TestJsonlLineTooLong`, `TestJsonlBackpressure` (slow reader, one page in flight), `TestJsonlMemoryFlat` (1 GB-equivalent fake pages, heap sampling).
- Acceptance: a real large result streams under the cap with flat CLI memory (private bytes sampled every second, growth under 5 MB after the first page) (live PC); backend killed mid-stream gives non-zero exit, one stderr error, no `complete` (CI; live PC); early pipe close stops CLI and backend work, no goroutine leak (CI).

### BL-393 Cross-adapter parity CI (Parallel B, M)
- `internal/clisrv/parity_test.go` (Go, same process: GUI HTTP handler, `clisrv`, `mcpsrv`, one fixture dataset), `tools/cli/Test-Parity.ps1` for the real binaries on a runner. Compares canonical values, order, coverage, policy-denied outcome, time boundaries, error category (3.4 mapping). Contract snapshots: help, op ids, JSON Schema, MCP catalog digest (area 03 `TestCatalogDigest` kept). Cancel, capped page, owner restart cases.
- Tests: `TestParityCompare`, `TestParityDenied`, `TestParityBoundaries`, `TestParityOwnerRestartExpiresHandle`, `TestSnapshotFailsOnRemovedField` (delete a field in a copy of a result; snapshot test must fail).
- Acceptance: job `cli-windows` green for all current ops (CI); one manual run on nachi's real case through GUI, CLI and MCP equal (`tools/cli/Test-Parity.ps1 -Real -Case <local json>`) (live PC); removing a field fails the snapshot (CI).

### BL-394 Installer: PATH, files, process roles (P4, L)
- `internal/setup`: `plan.go` new step `{ID:"cli-path", Title:"Add the unravis command to the path", Do: p.cliPath}`, `copyFiles` and `programFolder` changes (4.7), `journal.go` `UndoPathEntry`, `os_windows.go` `Sys.Env` interface (`AddPathEntry(root, entry)`, `RemovePathEntry(root, entry)`, `BroadcastEnvChange()` = `SendMessageTimeout(HWND_BROADCAST, WM_SETTINGCHANGE, 0, "Environment", SMTO_ABORTIFHUNG, 5000)`), fake in `fake.go`. PATH value rules: keep `REG_EXPAND_SZ` type, absolute path, trailing backslash removed, compare case-insensitive after normalizing, dedupe on repair, add at the end (never first), refuse values over 32,767 chars with a step error and no change. No `setx`. No shell profile edits, no execution-policy change. Uninstall and rollback remove only our entry. MCP install text keeps the absolute fat path (spec 03 BL-352; area 03 owns).
- Tests: `TestPathAddRemoveDedupe`, `TestPathKeepsOtherEntriesAndType`, `TestPathUndoAfterConcurrentChange` (another entry added between do and undo survives), `TestPathStepJournaledBeforeAction`, `TestPathLimitRefused`, `TestRunningAppsIncludesCLI`, `TestInstallPlacesCLIAtBin`, `TestForcedFailureAfterPathRestores` (fake sys, fail the next step).
- Acceptance: per-user install on nachi's PC: new PowerShell and new cmd find `unravis` and `unravis version -o json` parses; upgrade and repair leave one entry; uninstall removes it (live PC); per-machine: machine PATH set, another user's shell finds it, rollback removes only our entry (elevated); forced failure after the PATH step restores the old PATH (live PC, uses `--test-fail-after cli-path` from setup's existing test hook if present, else the fake in CI); old Start menu shortcut and autostart still start the app with no console window (live PC); `where.exe unravis` lists only `<install>\bin\unravis.exe` (live PC).

### BL-395 Update with CLI and headless owner (P4, S)
- Files: `internal/clisrv/handler.go` (state `updating` in hello; ops return `UPDATE_IN_PROGRESS`), `internal/setup/plan.go` (headless owner: stop with the spec 01 4.5 contract, record `wasHeadless` in setup state, start `unravis.exe --pipe-server --owner=headless` again as the user after the swap in the existing "start the app again" step), `internal/clicore/attach.go` (proto check, D28). Check the busy hook (BL-171): whether a running CLI call counts as "in use" is read from code first (`internal/server` `NoteMCP`/`mcpWindow`); CLI calls call the same hook through `InternalCLI` (D26 of spec 03 style) so updates wait.
- Tests: `TestUpdateInProgressExit3`, `TestProtoMajorMismatchExit5`, `TestProtoMinorOlderServer`, `TestSetupRestartsHeadlessOwner` (fake sys).
- Acceptance: real update on nachi's PC with a headless owner and a long CLI call: the call ends with `UPDATE_IN_PROGRESS`, the owner is back on the new version (live PC); old CLI against newer backend and the reverse give `VERSION_INCOMPATIBLE`, not garbage (CI with two builds); per-machine upgrade and rollback keep PATH and legacy flags (elevated).

### BL-396 Optional WinGet manifest (P4, S)
- `tools/winget/*.yaml` (version, installer, defaultLocale; `InstallerType: exe`, switches from setup's real flags: `--silent --config` is a gap, see 0), `tools/winget/Test-Manifest.ps1` (`winget validate`, then `winget install --manifest` on a clean VM). Depends on area 06 making `--silent` work without a hand-written config (or a published default config path). Setup and the in-app updater act through one path (setup's existing lock; the updater checks installed version first).
- Acceptance: `winget validate --manifest tools/winget` passes (CI, where `winget` exists on the runner image; else skipped and live PC); `winget install --manifest` on a clean Windows 11 installs, `unravis version` works, `winget upgrade` and the in-app update do not fight (live PC). Public submission: not done (D22).

### BL-397 Release gate: CLI on Win 10, 11, Server 2016 (P4/P5 exit, M)
- `tools/cli/Test-ReleaseGate.ps1` runs BL-383 harness, BL-385, BL-390, BL-391 cases per OS class, per-user and per-machine installs, both PowerShells, headless (no WebView2, no desktop session), records OS build and servicing date (Server 2016 regular updates end 2027-01-12, report's date), WebView2 absent proof (`Test-Path` of the Evergreen key), and writes `docs/desktop/cli-release-gate-<date>.md`.
- Acceptance: results table for all classes and shells; failures listed; a class with no machine is a row "not verified: no machine", never a pass (live PC); console binary runs on Server 2016 with no WebView2 (live PC if a box exists); upgrade from the previous release works with both exes on each class (elevated).

## 6. Security and footprint checks (this area)

Security:
- The CLI sends an operation only after the verified hello (pid, server pid, image, creation time). `TestNoOpBeforeHello` records all bytes sent to a fake peer. There is no token on this surface (BL-318 option B). The owner's own session token never leaves the owner process: it is used in process by `internal/mcp` `NewLocal` and is never in an answer (`TestRealPathOverTheDashboardHandler`).
- Principal never comes from request JSON (`TestPrincipalNeverFromRequestJSON`). Deny by default per surface (D8). Automation is scoped by area 03 grants. Same-user malware is out of the model; the matrix and `grant approve` console check are tripwires (D15, D16).
- No new listener. The thin exe dials one pipe and reads files; it opens no port. `TestThinNoTcpDial` (windows: `GetExtendedTcpTable` for the CLI pid shows no socket during a full call).
- Output hygiene: ASCII JSON, no request echo, no raw exception text, no credentials, no disallowed identifiers (area 03 projection runs before serialization). `cases` projection is a field allow-list (D10).
- Offline reader never writes, never opens case folders, size and path limits, no symlink escape (BL-391 tests).
- Legacy forwarding passes arguments through unchanged and only to the fixed app exe path from `clicore.AppExe()`; no PATH search, no shell. `TestForwardUsesAbsolutePathNoShell`.
- `--output-file` creates new files only (no overwrite without flag), under the caller's own rights; temp file in the same folder, renamed.
- Dependency adds: Cobra (and its `pflag`, `mousetrap`), nothing else beyond area 01's `go-winio`. Each goes through `govulncheck`; Cobra only imported under `cmd/unravis` and `internal/clicore/cmdtree` (`TestCobraImportBoundary`). List in `desktop/DEPS.md` with area 01's.
- Release scan (area 02 `scan-release.ps1`): no `e2e` symbols or `UNRAVIS_RUN_DIR` string in `unravis-cli.exe`; no `__test` command.
- PATH step: only our entry, journaled, no profile edits (BL-394 tests).
- Update: running CLI never blocks an update past setup's terminate path (4.7).

Footprint (area 07 holds the gates; this area reports):
- Thin exe: linked size <= 8 MiB (default; measured stripped baselines 2.5 MB audit-only, 5.6 MB with `blackbox`, Cobra not yet added), `version` warm p95 <= 150 ms, working set <= 25 MB, no resident process, no timer, no file watcher. Numbers set by BL-380/381 and re-checked in BL-397.
- Fat exe: unchanged (38.5 MB stripped measured); owner gains one more mux and the clisrv package (small); idle RSS delta recorded by BL-390 vs area 01's BL-300 baseline (limit +1 MB).
- Installer: one more ~6 to 8 MB file raw; compressed delta recorded by BL-382 against the 25 MB budget (area 07 decides if a trim is needed).
- Owner lease: one open file handle; no thread.

## 7. Open risks left and mitigations (no owner needed)

| Risk | Mitigation |
|---|---|
| Two files named `unravis.exe` (fat and thin) confuse people, AV and process tools | Different folders, different file descriptions in VERSIONINFO ("Unravis" vs "Unravis command line"), `doctor` prints both paths, setup "mine" detection lists both; `where.exe unravis` test in BL-394. |
| The thin exe grows past budget when `blackbox` and Cobra are linked | BL-381 measures; D12 fallback forwards offline inspect to the fat exe; `TestThinDependencyBoundaries` stops silent growth. |
| Area 03 (grants, shapes, `mcpsrv.Service`) lands late | Waves 0 and 1 need none of it; BL-385 uses the `Backend` interface; `cases`, `sources`, `doctor`, `app`, offline inspect work on `InternalCLI` alone. |
| Area 01's P1 (`--pipe-server`, locator, `ag` listener) slips | Waves 0 and offline items still ship (version, schema, completion, harness, offline reader). Online ops stay unticked in BACKLOG until P1; no loopback fallback is built (D14). |
| Browser-mode owners have no CLI access (D14), so before desktop or headless is used the CLI cannot reach live data | Documented in the error text and `docs/user/cli.md`; `app start --headless` is the path; request to area 01 to open `ag` in browser mode too (zero change here if done). |
| Spec 02 uses different names and rules (`pipesec`, `unravis.mcp.<hex>`, logon SID, `--pipe-only`) than spec 01 | This area codes to spec 01 names (`pipesrv`, `ag`, `--pipe-server`) behind two seams: `clicore.attach` (dial) and `clisrv` mount point. If the pipe naming or package changes, only those two files change. |
| PS 5.1 `>` redirect writes UTF-16 | ASCII JSON stays valid; `--output-file` gives exact bytes; documented with a copy-paste example in `docs/user/cli.md` (BL-383 C6). |
| Headless owner on a box where console and RDP both try to own | Lease (D19) gives a deterministic `OWNER_BUSY`; BL-390 records the real console+RDP outcome; no claim of account-wide ownership. |
| Browser-mode owners do not take the lease, so a headless owner and a browser owner can both run on one profile (different sessions) | Same as today's multi-session behavior; recorded in BL-390 report; a new item (not built) if it shows damage. |
| `grant approve` console check is weak against an agent with a pty | Stated in the command help and docs as a tripwire; widening (evidence tier, identifiers) is GUI only; area 03 ledger records the approval row with host kind "cli". |
| Retried live comparison after a dropped connection repeats a heavy query | `retryable:false` after body sent (D27); no auto-retry in the CLI; handles let follow-ups avoid re-query. |
| Setup cannot place a file named differently from its payload name without special case | `place` table already maps name to path (`copyFiles`); BL-394 test `TestInstallPlacesCLIAtBin`. |
| WinGet silent switches missing (setup needs `--config`) | BL-396 depends on area 06; the manifest is built and validated either way; not ticked until `winget install` works. |
| No Windows 10, Server 2016/2019 or second user on nachi's PC | Those cells are "not verified", never pass; CI covers 2022 and 2025; BL-397 lists what ran. |
| Cobra issue #2481 still open | BL-381 reproduces on the repo build; fallback urfave/cli v3 is chosen if it does; completion is static anyway. |
| Go 1.27.3 toolchain download needed to build (`go.mod`) | Existing workflow already pins it; no change here. |
