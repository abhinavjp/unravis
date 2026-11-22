# Groom 05: CLI and design parity (BL-380 to BL-397)

Area: console `unravis.exe` CLI, output contract, shared Go operation layer, app-absent modes, PATH and distribution. Stage 1 (groom only). Sources: research report `06-cli-design-parity.md` (read in full, 273 lines), `DECISIONS-draft.md` (sections 2, 7, 12), `CLAUDE.md`, `docs/BACKLOG.md` (style, Auto-updater section).

Notes
- Acceptance tags: **CI**, **live PC (nachi)**, **elevated/UAC**.
- Owner rule: proof is on the real path: real Go backend, real cases, real shells on Windows. Fixtures only add; they are never the proof.
- Report tags: almost all design is `[INFERRED]`. Versions and dates in the report (Cobra v1.10.2, Go SDK 1.8.0, WinGet 1.29.380 and so on) are the report's, not re-checked here.
- The report says no Windows benchmark, no dependency scan and no OS matrix was run. Every number below must be measured, not assumed.
- Not duplicated: Auto-updater (BL-149 to BL-155, BL-168 to BL-171, BL-194); area 01 (BL-300 to BL-318, esp. BL-318 CLI and MCP attach via locator); area 03 MCP (BL-340 to BL-355). Those are dependencies here.
- Not built here: code mode CLI (`unravis code run|types|replay`, Parallel C, other area); named-pipe transport itself (area 01); dynamic shell completion; query/template language (`--jq`); OpenAPI generation; MSI; MSIX alias; portable ZIP. Listed under Questions where the owner must decide.
- Command names, flags, envelope and exit map in the report are PROPOSED (report says so). Spec stage fixes them.

## Summary table

| ID | Title | Phase | Size | Owner action |
|---|---|---|---|---|
| BL-380 | Baseline: today's launch, flags and shell behaviour | P0 | M | no |
| BL-381 | Spike: Cobra on PowerShell (issue #2481) and size/start cost | Parallel B | S | no |
| BL-382 | Console `unravis.exe`, GUI launcher, legacy flag dispatch | Parallel B (needs BL-305) | M | no |
| BL-383 | Windows shell test harness (cmd, PS 5.1, PS 7, scheduler) | Parallel B | M | no |
| BL-384 | Output contract and offline commands (`version`, `schema`, help, completion) | Parallel B | M | no |
| BL-385 | Operation layer and first vertical slice: `audit compare` | Parallel B (needs P1) | L | no |
| BL-386 | Remaining audit commands | Parallel B | M | no |
| BL-387 | `sources`, `cases` read commands and `doctor` | Parallel B | M | no |
| BL-388 | Result handles and cursors in the backend store | Parallel B (shared with BL-350) | M | no |
| BL-389 | Capability matrix, automation grant, deny-by-default test | Parallel B (needs BL-346) | L | no |
| BL-390 | `app status|stop|start --headless` and owner lease | Parallel B (needs P1) | L | no |
| BL-391 | Explicit offline reader: `blackbox inspect --offline` | Parallel B | M | no |
| BL-392 | JSON Lines streaming for selected operations | Parallel B | S | no |
| BL-393 | Cross-adapter parity CI (GUI HTTP, CLI, MCP) | Parallel B | M | no |
| BL-394 | Installer: PATH entry, shortcuts, process roles, legacy path | P4 | L | UAC (machine scope test) |
| BL-395 | Update with CLI and headless owner running, version skew | P4 | S | no |
| BL-396 | Optional WinGet manifest | P4 (after BL-394) | S | maybe (public submission) |
| BL-397 | Release gate: CLI on Win 10, 11, Server 2016 | P4/P5 exit | M | no |

18 items. No item needs a secret, signing passphrase, money or deleting user data. UAC appears in BL-394 (and as test steps in BL-390, BL-395, BL-397). Possible owner action in BL-396 (public submission of a manifest; the report does not say who submits).

---

## BL-380 Baseline: today's launch, flags and shell behaviour
- Outcome: recorded facts about the current `unravis.exe` (GUI subsystem, `AttachConsole` helper) so the console switch has something to compare against.
- Scope (in): on the real installed build: PE subsystem, `--version`, `--mcp`, lifecycle flags, double-click, autostart, shortcut, scheduled-task launch. Record what works from cmd, PS 5.1, PS 7, Explorer, Windows Terminal: waits for exit, `$LASTEXITCODE`/ERRORLEVEL, redirection. List every installed caller of `unravis.exe` (shortcut, autostart `HKCU Run`, MCP client configs, scheduled tasks, setup and updater code) and its argument. Cold start and working set of `--version`.
- Out of scope: any code change; Tauri; Cobra.
- Depends on: P0, BL-300.
- Risks / unknowns: report says existing tests only use Go `os/exec`, so shell behaviour with the GUI PE is unproven (report "Shell and packaging behavior"). No product data exists for cold CLI start or memory (report "Real resource cost").
- Acceptance:
  - Table of shell x launch method x (waits, exit code, stdout/stderr capture), saved under `docs/desktop/` (spec picks file). (live PC (nachi))
  - List of every caller of the installed `unravis.exe` path with its arguments, from the repo and from nachi's PC (registry, shortcuts, MCP configs). (CI for the repo part; live PC (nachi) for the PC part)
  - `--version` cold and warm start and working set, hardware noted. (live PC (nachi))
- Size: M. Phase: P0.
- Needs owner action? No.

## BL-381 Spike: Cobra on PowerShell (issue #2481) and size/start cost
- Outcome: go/no-go on Cobra (report: v1.10.2) with urfave/cli v3 as the named fallback.
- Scope (in): throwaway branch with three commands. Test PowerShell completion with zero, one and many candidates and quoted input on PS 5.1 and PS 7 (reproduce Cobra issue #2481 on the repo build). Build the same stub in Cobra and in urfave/cli v3; compare linked size, cold start and working set on one PC. Check the dependency graph, `govulncheck` and licences. Check Go version in the repo against the report's baseline.
- Out of scope: shipping commands (BL-384+); Viper; dynamic completion.
- Depends on: BL-380 (to compare).
- Risks / unknowns: report states no suitable Windows comparison of cold start, memory or size exists; "compare linked release binaries locally before asserting a difference". Issue #2481 status is the report's (2026-08-10); may be fixed or still open.
- Acceptance:
  - Completion works for 0/1/many candidates and quoted input in PS 5.1 and PS 7, or the fallback is chosen and shown to work. (live PC (nachi))
  - Size, cold start and working set of both builds recorded. (live PC (nachi))
  - `govulncheck` clean for the chosen framework graph; pinned version recorded. (CI)
- Size: S. Phase: Parallel B.
- Needs owner action? No.

## BL-382 Console `unravis.exe`, GUI launcher, legacy flag dispatch
- Outcome: `unravis.exe` is a console-subsystem Go program at the existing path. Every legacy flag still works. A distinctly named GUI launcher opens the desktop with no console window.
- Scope (in): build flag change from `-H=windowsgui` to console for `unravis.exe`. Explicit argument dispatch table for legacy lifecycle flags and `--mcp` and the old "open the app" intent. Name of the GUI launcher is the desktop plan's (`unravis-desktop.exe` in DECISIONS). Keep `console_windows.go` only as a legacy mechanism if still needed. Update existing tests (PE subsystem, piped version, MCP stdio round trip).
- Out of scope: installer shortcut and autostart changes (BL-394); Tauri launcher code (area 01); CLI commands.
- Depends on: BL-305 (same exe or split sidecar decides what "the sidecar" is), BL-380.
- Risks / unknowns: IMPORTANT. Old shortcuts and the autostart entry point at `unravis.exe`; once it is a console program, a double click or sign-in start may flash a console window until BL-394 moves them. Report says "retain old startup switches where installed callers depend on them" and gives no fix for the flash. Report leaves open whether a dual-mode Go console/owner build or a separate backend exe is chosen ("Compare ... before committing").
- Acceptance:
  - PE subsystem is console for `unravis.exe` and GUI for the launcher. (CI)
  - Every legacy flag from BL-380's list returns the same result as before, from a real subprocess. (CI)
  - MCP stdio round trip still passes from a real MCP host (one host minimum). (live PC (nachi))
  - Double click on the launcher shows no console window. Behaviour of an OLD shortcut to `unravis.exe` is recorded (flash or not). (live PC (nachi))
- Size: M. Phase: Parallel B (blocks BL-394).
- Needs owner action? No.

## BL-383 Windows shell test harness
- Outcome: one repeatable harness that runs the CLI from every shell we claim to support and checks the stream and exit rules.
- Scope (in): scripted runs from cmd interactive and batch, Windows PowerShell 5.1, PowerShell 7, Explorer launch, Windows Terminal hosted shell, scheduled task (noninteractive), direct `CreateProcess` and Go `os/exec`. Checks: command completes before the prompt returns, `$LASTEXITCODE`/ERRORLEVEL, stdout vs stderr direction, stdin closes, Ctrl+C over a real backend call, UTF-8 with no BOM, paths with spaces and non-ASCII, no persistent console on Explorer launch of the GUI launcher. PS 5.1 file-redirection byte behaviour measured (report: PS 7.4 added byte-preserving redirection).
- Out of scope: the commands themselves; dynamic completion.
- Depends on: BL-382, BL-384 (needs real commands to exercise).
- Risks / unknowns: report says Windows Terminal does not settle child-wait semantics for every shell; scheduled and RDP callers untested. Combined stdout/stderr redirection can break machine parsing (caller's choice; document it).
- Acceptance:
  - Matrix passes on nachi's Windows 10/11 PC for all shells above. (live PC (nachi))
  - PS 5.1 redirected JSON output is valid UTF-8 without BOM, or the limitation is documented with an output-file flag. (live PC (nachi))
  - Harness script runs in CI on a Windows runner for the shells present there. (CI)
- Size: M. Phase: Parallel B.
- Needs owner action? No.

## BL-384 Output contract and offline commands
- Outcome: the versioned JSON contract works end to end for commands that need no backend: `version`, `schema`, help, completion.
- Scope (in): envelope (`schemaVersion`, `operation`, `requestId`, `data`/`error`, `meta`), error codes, exit map 0 to 7, stdout vs stderr rules, UTF-8 no BOM, `--output table|json|jsonl`, `--json` alias, `--request FILE|-`, one stdin owner, flag/env/profile precedence (allowlisted env only), no config that relaxes safety policy. `unravis version --output json` (app version, commit, CLI schema major, IPC major/minor, MCP revision). `unravis schema <op> --output json`. `--help`, `version`, `schema`, completion do no runtime, profile, browser or network work. Golden output files.
- Out of scope: backend operations (BL-385+); streaming (BL-392); `--jq`/templates (deferred, report says defer).
- Depends on: BL-381, BL-382.
- Risks / unknowns: names, flags, envelope and exit codes are PROPOSED by the report. Exit-code values (esp. 3 vs 5 for lock/busy) not tested with real callers. `retryable` semantics need a spec.
- Acceptance:
  - Golden tests for success, usage error (exit 2), unknown format, in JSON mode with empty stdout on error and one error object on stderr. (CI)
  - Tracing: a real run with network off and no profile shows no file, registry or network access beyond reading the exe (Process Monitor or equivalent). (live PC (nachi))
  - `unravis version --output json` run from PS 5.1, PS 7 and cmd parses as valid JSON. (live PC (nachi))
- Size: M. Phase: Parallel B.
- Needs owner action? No.

## BL-385 Operation layer and first vertical slice: `audit compare`
- Outcome: one read operation (`audit.compare`) runs through a registered Go operation (typed input/output, policy class, validator, limits, error map) and a thin CLI command against the real backend.
- Scope (in): extract `audit.compare` from the existing GUI route (`/api/analyze`) and MCP tool into the operation layer. GUI route and MCP tool call the same operation (no behaviour change). CLI `audit compare --request FILE --output json`. Principal comes from the trusted transport, never from request JSON. Explicit case and source selectors and explicit RFC 3339 windows (no "current GUI tab" selection). Returns a bounded standalone page (report: first slice may do this until the result store lands, BL-388). Attach through the locator and pipe from BL-318.
- Out of scope: other audit commands (BL-386); handles (BL-388); grants (BL-389).
- Depends on: P1, BL-301, BL-302, BL-318, BL-384.
- Risks / unknowns: report says GUI, MCP and `internal/server` keep separate paths today; refactor can change behaviour. 19 known Windows-only failures in `internal/server` (DECISIONS section 10) must not grow. Disjoint-window rule lives in the shared validator.
- Acceptance:
  - Same request gives the same canonical result through GUI HTTP, MCP and CLI on a real saved case (compare values and order). (CI for fixture; live PC (nachi) for a real case)
  - CLI against a running real backend on nachi's PC returns a real comparison; exits 3 with `APP_NOT_RUNNING` when the app is closed. (live PC (nachi))
  - Existing MCP exact-ten-tools test and GUI tests still pass. (CI)
- Size: L. Phase: Parallel B.
- Needs owner action? No.

## BL-386 Remaining audit commands
- Outcome: `audit groups|timeline|requests|slowest|breakdown` and `audit timelines compare` match the ten existing MCP read tools.
- Scope (in): one command per read operation, same operation layer, same schemas, examples in help. Investigation guide reachable as help text and stays an MCP tool.
- Out of scope: any write or case-changing command; code mode.
- Depends on: BL-385, BL-388 (for follow-ups of a compare).
- Risks / unknowns: report lists the commands but gives no flag set per command. Command names are PROPOSED. Whether all ten MCP tools map one-to-one is not checked here; spec compares against `internal/mcp/schema.go`.
- Acceptance:
  - Each command returns real data on nachi's real case and matches the same MCP tool result. (live PC (nachi))
  - Contract snapshot test for help, operation IDs and JSON Schema of each. (CI)
  - Unknown flag, bad window and oversize input each exit 2 with an error code. (CI)
- Size: M. Phase: Parallel B.
- Needs owner action? No.

## BL-387 `sources`, `cases` read commands and `doctor`
- Outcome: `sources list`, `cases list`, `cases show` and `doctor` work on real data.
- Scope (in): read-only operations. Honour the case lock state (`CASE_LOCKED`, exit 4). `doctor`: version, backend reachable, locator state, owner state, no secrets, no network. Privacy projection before serialization.
- Out of scope: any create, import, destroy, key action or saved-connection change (report: add operation by operation later, with the same consent as the GUI; those are separate backlog items after owner go).
- Depends on: BL-385, BL-389 (policy), BL-302.
- Risks / unknowns: report does not say which case fields are safe to show on `cases show`. Case/source IDs in logs are scope-limited per surface.
- Acceptance:
  - Lists match the GUI for nachi's real sources and cases. (live PC (nachi))
  - A locked case returns `CASE_LOCKED` and exit 4, not data. (live PC (nachi))
  - `doctor` output contains no token, key or connection string (scan of output and logs). (CI)
- Size: M. Phase: Parallel B.
- Needs owner action? No.

## BL-388 Result handles and cursors in the backend store
- Outcome: comparison results live in a bounded backend store so CLI follow-up commands page without re-running the live query. Shared with MCP.
- Scope (in): one store in Go, bound to owner, grant, operation, filter and version; short expiry; released on shutdown; invalid after restart or grant revocation. `RESULT_EXPIRED` error. Keep old MCP lookup semantics (10 min per bridge) during migration. Bound memory.
- Out of scope: MCP grant design (BL-346); persistent result storage.
- Depends on: BL-385, BL-350 (MCP area, same store; one owner for the design).
- Risks / unknowns: overlap with BL-350 (cursors and handles). Report: avoid retries that repeat a live comparison unless the command can prove the first did not run. Memory cap is not given in the report.
- Acceptance:
  - `audit compare` then `audit groups --result-id` pages on a real case with no second query to the source (check source-side query count). (live PC (nachi))
  - Handle fails after expiry, backend restart and grant revoke; reuse by another principal or filter fails. (CI)
  - Memory stays under the cap with many handles; released on shutdown. (CI)
- Size: M. Phase: Parallel B.
- Needs owner action? No.

## BL-389 Capability matrix, automation grant, deny-by-default test
- Outcome: a reviewed matrix of what GUI, human CLI, automation CLI, MCP and the sandbox host may do, enforced in the backend, with a scoped automation principal for scripts.
- Scope (in): matrix as data checked in CI. Every new operation needs an explicit allowlist entry per surface (deny by default). Automation CLI principal: short-lived, audience-bound, scoped, revoked on restart, never passed by argument, env or stdout. Identifier disclosure off by default for automation. Human CLI keeps operator policy.
- Out of scope: MCP grant UI (BL-347); sandbox host.
- Depends on: BL-346 (MCP installation grants; one grant system), BL-385.
- Risks / unknowns: report says same-SID processes share OS privileges; owner-only ACL or DPAPI cannot give per-agent permissions against arbitrary same-user code (report "Pipe identity design"). Real scoping needs the grant system from area 03. Identity choice (account SID vs logon SID) is open.
- Acceptance:
  - A new operation registered without an allowlist entry cannot be called from any surface. (CI)
  - Automation principal cannot read identifiers or case data without the explicit grant; denied calls exit 4 with a stable code. (CI)
  - Same checks hold on the real pipe from a second process of the same user on nachi's PC (denied without grant, allowed with grant, denied after revoke). (live PC (nachi))
  - Token never appears in process list, environment dump, stdout, stderr or logs. (CI)
- Size: L. Phase: Parallel B.
- Needs owner action? No.

## BL-390 `app status|stop|start --headless` and owner lease
- Outcome: the CLI can show, stop and (explicitly) start the backend without the GUI. Never starts the GUI by itself.
- Scope (in): `app status` and `app stop` via the locator and shutdown RPC (BL-303). `app start --headless`: same backend under the same profile-owner guard, same authorization, unelevated, no browser. Exclusive owner lease over the shared profile data root; second owner gets `OWNER_BUSY`. Distinct codes: `APP_NOT_RUNNING`, `CASE_LOCKED`, `OWNER_BUSY`, denied, stale, incompatible.
- Out of scope: cross-session or account-wide ownership (report: v1 is session-scoped); Windows service install of the backend.
- Depends on: P1, BL-301 to BL-304, BL-318.
- Risks / unknowns: current mutex is under `Local\` (per sign-in session) and discovery is per user, so console, RDP and a scheduled task as the same user are not proven to share one owner (report "Ownership choice"). Conflict with the DECISIONS line "Go is spawned from Rust only": see conflicts.
- Acceptance:
  - `app start --headless` then GUI launcher attaches to it and sees the same cases. (live PC (nachi))
  - Two concurrent `app start --headless` leave exactly one owner; the other exits 3 `OWNER_BUSY`. (CI; live PC (nachi))
  - Same user in console and RDP session: behaviour recorded (one owner or busy). (live PC (nachi))
  - Elevated shell refuses to start the owner. (elevated/UAC)
  - Stale locator after a kill: `app status` reports it and does not attach to a wrong process. (CI)
- Size: L. Phase: Parallel B (needs P1).
- Needs owner action? UAC only for the elevated refusal test.

## BL-391 Explicit offline reader: `blackbox inspect --offline`
- Outcome: inspect a Blackbox recording or an explicit snapshot with no backend, no profile unlock, no production access.
- Scope (in): `blackbox inspect --offline --input FILE --output json` using existing non-mutating readers; size limits; refuses live case directories. Snapshot of a case is NOT in scope until a versioned snapshot format exists.
- Out of scope: opening live case SQLite files (case open can heal a half-finished rewrap and write); offline case inspection; repair.
- Depends on: BL-384.
- Risks / unknowns: report says a generic read-only SQLite flag is not proof of safety (WAL sidecars, immutable URI skips locking); the snapshot format and Server 2016 behaviour of `ncruces/go-sqlite3` are untested. Blackbox recording reader names are not in the report; spec finds them in the repo.
- Acceptance:
  - A real Blackbox recording copied from a real PC is read, and the input file hash is unchanged after the run. (live PC (nachi))
  - A live case folder path is refused with a stable error and nothing is written (directory listing before and after). (CI)
  - Oversize or malformed input fails with exit 2 and no crash. (CI)
- Size: M. Phase: Parallel B.
- Needs owner action? No.

## BL-392 JSON Lines streaming for selected operations
- Outcome: one explicit stream mode (`--output jsonl`) for chosen operations, with a published framing.
- Scope (in): meta line, item or page lines, complete line (coverage, next cursor). Max line size, output cap, deadline, backpressure, cancellation. A mid-stream failure exits nonzero with one error on stderr and no complete line. Which operations: spec picks (report example: `audit.requests`).
- Out of scope: watch/follow mode; streaming for every command.
- Depends on: BL-384, BL-386.
- Risks / unknowns: report calls prior rows "provisional" after a failure; consumers' handling is untested. Limits are not numbers in the report.
- Acceptance:
  - A real large result streams under the cap with flat memory in the CLI (measured). (live PC (nachi))
  - Killing the backend mid-stream gives exit nonzero, one stderr error, no complete line. (CI; live PC (nachi))
  - Closing the reader pipe early stops the CLI and the backend work (no leak). (CI)
- Size: S. Phase: Parallel B.
- Needs owner action? No.

## BL-393 Cross-adapter parity CI
- Outcome: the same input gives the same result and the same error class through GUI HTTP, CLI and MCP.
- Scope (in): shared fixtures run through the three adapters on a Windows runner; compare values, order, coverage, policy-denied outcomes, time boundaries, error categories. Contract snapshots for help, operation IDs, JSON Schema, MCP registry. Cancellation, capped page, owner restart cases. Keep the exact-ten-tool MCP test until the manifest is changed on purpose.
- Out of scope: sandbox host adapter; generated TypeScript types (decide in spec).
- Depends on: BL-385, BL-386, BL-343 (MCP structured results).
- Risks / unknowns: report says the real Claude Code and Codex clients must be tested before the MCP revision changes; that is area 03. Fixture parity is not live proof: the owner rule needs one real-case run too.
- Acceptance:
  - CI job green on a Windows runner for all current operations. (CI)
  - One manual run on nachi's real case shows equal results across the three adapters. (live PC (nachi))
  - Removing a field from a result makes the snapshot test fail. (CI)
- Size: M. Phase: Parallel B.
- Needs owner action? No.

## BL-394 Installer: PATH entry, shortcuts, process roles, legacy path
- Outcome: after install, `unravis` works from a new shell; shortcuts and autostart use the right binary; old callers keep working.
- Scope (in): journaled PATH step in user scope (per-user install) and machine scope (per-machine install); one owned entry, no duplicates on repair, removed only if owned on uninstall and rollback; environment-change broadcast; fresh-shell note. Check which executables in the install dir become PATH-visible; use a dedicated CLI dir if needed. Shortcuts and autostart point to the GUI launcher; old `unravis.exe` path stays. Process detection, health probes and recovery in setup know the component roles. MCP client registration uses an absolute path.
- Out of scope: MSI, MSIX alias, portable ZIP; WinGet (BL-396); editing the user's shell profiles or execution policy.
- Depends on: BL-382, P4 installer work (area 01 and the desktop installer item), BL-352 (MCP install entry points).
- Risks / unknowns: report says the real VS Code PATH collision (a GUI exe ahead of a shim) can occur; test with both launch methods. Current autostart code expects `unravis.exe` identity (report). Power loss mid-PATH change is covered by the journal only if the step is journaled (BL-170 style); not proven.
- Acceptance:
  - Per-user install, new PowerShell and cmd find `unravis` and run `unravis version --output json`; upgrade and repair leave one entry; uninstall removes it. (live PC (nachi))
  - Per-machine install: machine PATH set, other user's shell finds it; rollback removes only our entry. (elevated/UAC)
  - Forced failure after the PATH step restores the old PATH (journal replay). (live PC (nachi))
  - Old shortcut and autostart start the app with no console window. (live PC (nachi))
  - Installer step unit tests for entry add, dedupe, remove. (CI)
- Size: L. Phase: P4.
- Needs owner action? Yes, UAC for the per-machine run. The owner decides user vs machine default (see Questions).

## BL-395 Update with CLI and headless owner running, version skew
- Outcome: an update does not break a running CLI call or leave a headless owner on the old version; skew gives a stable error.
- Scope (in): update drains or restarts a headless owner (BL-390); running CLI calls end with `UPDATE_IN_PROGRESS` (exit 3). CLI vs backend IPC version check before any request: incompatible gives exit 5 and a clear message. Verify previous-release to new-release with both entrypoints, PATH and legacy flags, and one rollback assertion.
- Out of scope: MCP client handling (BL-353); update trigger and signing (Auto-updater items).
- Depends on: BL-168 to BL-171 (existing), BL-353, BL-390, BL-394.
- Risks / unknowns: report says a drain-and-relaunch sequence or staged versioned binaries must be tested, and power-loss between file swaps is not proven. Busy-hook (BL-171) counts MCP but not CLI calls today (report does not say; check code).
- Acceptance:
  - Real update on nachi's PC with a headless owner and a long CLI call running: call ends with the stable code, owner returns on the new version. (live PC (nachi))
  - Old CLI against a newer backend (and the reverse) returns the version error, not garbage. (CI)
  - Per-machine upgrade and rollback with PATH and legacy flags intact. (elevated/UAC)
- Size: S. Phase: P4.
- Needs owner action? No (UAC test step only).

## BL-396 Optional WinGet manifest
- Outcome: a manifest so `winget install` fetches the existing signed installer. Not the primary channel.
- Scope (in): manifest for the current installer; silent install and upgrade proven first; one setup path when WinGet and the in-app updater both act.
- Out of scope: publishing policy; MSI; MSIX alias.
- Depends on: BL-394, a stable silent install (Auto-updater items).
- Risks / unknowns: report says WinGet support listing covers Windows 10/11 and Server 2025 only; Server 2016 not covered. The report gives no word on whether WinGet's own SmartScreen/signature rules need Authenticode (BL-312, area 01). Public manifest submission is not described.
- Acceptance:
  - `winget validate` passes on the manifest. (CI)
  - `winget install --manifest` on a clean Windows 11 machine installs, PATH works, `winget upgrade` or the in-app update does not conflict. (live PC (nachi))
- Size: S. Phase: P4 (after BL-394).
- Needs owner action? Maybe: whether to publish publicly (owner decision).

## BL-397 Release gate: CLI on Win 10, 11, Server 2016
- Outcome: proof that the console binary and the headless owner work on every supported Windows class, with no WebView2 needed.
- Scope (in): run the BL-383 harness plus BL-385, BL-390, BL-391 on Windows 10, 11 and Server 2016 (headless, no WebView2, no desktop session). Per-user and per-machine installs. Both PowerShells. Record results. Record the Server 2016 servicing date as the report lists it (2027-01-12) as an input to scope.
- Out of scope: GUI on Server; Server editions other than the report's.
- Depends on: BL-383, BL-390, BL-391, BL-394, BL-395.
- Risks / unknowns: report says hosted CI uses newer Windows, so real machines are needed; it ran no OS matrix. SQLite WAL on Server 2016: not verified (report). nachi may not have a Server 2016 machine (unknown).
- Acceptance:
  - Results table for all classes and shells, with failures listed. (live PC (nachi))
  - Console binary runs on Server 2016 with no WebView2 installed. (live PC (nachi))
  - Installer upgrade from the previous release works with both entrypoints on each class. (elevated/UAC)
- Size: M. Phase: P4/P5 exit.
- Needs owner action? Maybe: access to a Server 2016 machine; UAC steps.

---

## Conflicts with other areas
- Area 01 (BL-318 attach, BL-301 to BL-304): DECISIONS says "Go sidecar is spawned from Rust only" and "attach first, spawn if none". BL-390 adds a Go-side `app start --headless` with no Rust. Needs one rule (headless is the second legitimate parent).
- Area 01 (BL-305, same exe vs split `unravis-core.exe`): CLI binary role depends on the answer; BL-382 waits for it. The report prefers a thin console client plus a separate backend if the Tauri plan proceeds, but also allows a dual-mode Go build.
- Area 01 (BL-316 data folder move): offline reader and `doctor` paths change with the move. Offline reader must read both old and new roots until the move is final.
- Area 01 transport: report 06 says reuse loopback first, named pipe later; DECISIONS picks the pipe only. This area follows DECISIONS and BL-318. If P1 slips, the CLI would need the loopback path; not planned here.
- Area 03 MCP (BL-346 grants, BL-348 disclosure tiers, BL-350 handles and cursors, BL-351 audit, BL-353 update, BL-352 install entry points): same grant system, same result store, same audit. BL-388 and BL-389 must be built once, not twice. Decide which area owns them (suggest area 03 owns grants and store; this area only consumes).
- Area 02 security (pipe DACL, process mitigations): CLI is a pipe client; its own mitigations and the "automation principal" fit area 02's model; not checked.
- Auto-updater (BL-149, BL-168 to BL-171, BL-194): `unravis.exe` subsystem change touches the update hand-off and busy-hook; BL-395 adds only CLI items.
- Installer and Authenticode (area 01 BL-312): new exe names change what is signed and what SmartScreen sees.
- Code mode (Parallel C): `unravis code run|types|replay` shares the operation layer and the capability matrix. Not groomed here.
- Lite (`unravis-lite`) and Blackbox server: stay pure Go and separate; `blackbox inspect --offline` must not pull in Lite code or change the Blackbox recorder format.

## Questions for spec stage
1. Same exe or split backend (BL-305) and what the CLI is on that basis: a thin client, or also the owner?
2. How to avoid a console flash for old shortcuts and autostart entries that point to `unravis.exe`? Rewrite them in setup, or ship a tiny GUI-subsystem shim at the old path? (Report gives no answer.)
3. Final names: GUI launcher (`unravis-desktop.exe`?), command tree, flag names, error codes and exit numbers (all PROPOSED by the report).
4. Cobra or urfave/cli v3 (depends on BL-381 result).
5. Which of the ten MCP tools become CLI commands first, and which stay MCP-only? Which fields may `cases show` print?
6. Who owns the result store and the grant system (area 03 or here)? Memory, expiry and page caps need numbers.
7. User or machine PATH as the default for per-machine installs? Dedicated CLI dir or the app dir?
8. Is cross-session live attach (console + RDP + scheduled task) required? If yes, the report says one account-wide backend is needed, which is a bigger design.
9. Snapshot format for offline case inspection: build it now or leave offline to Blackbox recordings only (this groom does the latter)?
10. WinGet: publish at all, and who submits? Is Authenticode (BL-312) a gate for it?
11. Does the owner have a Server 2016 test machine, and which hosts/shells are in the supported list?
12. Which write operations (case create, import, key action, saved connection, destroy) ever get a CLI form? Each needs an owner go; none is groomed here.
13. Automation grant: identity model (account SID vs logon SID) and who may hold a grant (report leaves open).
14. Shell completion: static only for v1 (report says so); confirm no dynamic completion.
