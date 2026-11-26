# Remote operations and full UI/CLI parity: analysis and plan

Date: 2026-11-26. Written against `develop` at `f622310c`. Owner ask (nachi, via the coordinator): Unravis must be a strong remote utility. Almost any diagnosis of a server should be possible from the CLI and from code mode, and **whatever the UI can do, the CLI must be able to do**. That includes remote check for updates, update now, rollback, status, version and update history, and the same for Blackbox on a server that has only Blackbox.

This is the plan only. The first small items are built in separate pull requests (section 8).

## 1. What exists today

**CLI (`unravis`, thin console exe, spec 05).** Twelve working commands: `app start|stop|status`, `audit activity|breakdown|compare|groups|guide|requests|slowest|timeline|timelines compare`, `blackbox inspect` (offline file reader), `cases list|show`, `code run|types`, `sources list`, `doctor`, `schema`, `version`, `completion`. Source of truth: `internal/clicore/cmdtree/testdata/commands.golden`.

**Capability matrix (`internal/clicap/matrix.go`).** Rows for those ops plus `grant.list|approve|revoke` and `code.replay`. The last four have a row but no command.

**Blackbox CLI (`unravis-blackbox`).** `install|start|stop|apply|update|remove|snapshot|sql*` with `--json`. No `status`, `doctor`, `update check`, `history` or `rollback`. A small read-key-guarded HTTP status server (`/status /manifest /diag /files`) is the only remote read path, and it is not a CLI.

**UI HTTP surface.** About 128 routes in `internal/server/routes.go` plus the Blackbox routes in `internal/server/blackbox.go`. The CLI reaches roughly 10 of them.

**Update machinery (`internal/update`, `internal/server/updater*.go`).** Signed manifest (ed25519, keys in `trusted_keys.txt`), `Judge` (sequence and version), signed control file (pause, block, staged rollout), health `Tracker` with rollback and skip, `LaunchRollback`, offline folder verify (`VerifyFolder`), apply result. All reachable only through the UI (`/api/update*`) or the tray.

**Auth.** Pipe peer policy only (BL-318 option B, 2026-11-23): same logon SID, same session, image is an Unravis exe, deny by default, no token.

## 2. Parity gap table

Legend: CLI/MCP columns are `exists`, `partial` or `missing`. JSON means a stable machine envelope exists. Remote-safe means usable from a Salt or SSH `cmd.run` as SYSTEM today without widening any trust (see section 4).

| UI action (route family) | CLI | Code mode / MCP | JSON | Remote-safe |
|---|---|---|---|---|
| List cases, show one (`/api/cases`, `/{id}`) | exists | missing | yes | no (pipe) |
| Create, archive, close, reopen, restore, erase, free a case | missing | missing | n/a | no |
| Case key lock, unlock, set, change, remove; master key status, setup, lock, unlock, rewrap, reset | missing | missing (deliberately; secrets) | n/a | no, human only |
| Add or import data: discover scan, import check, import job, retries | missing | missing | n/a | partial (folder scan is read-only) |
| Sources list, fields, schema, stats, coverage | list only | `audit_list_sources` | yes | no (pipe) |
| Audit requests, slowest, groups, breakdown, activity, compare, timeline | exists | exists | yes | no (pipe) |
| Investigation timeline and lanes (`/lanes`, `/timeline`, `/tracing`, `/investigation`) | missing | missing | n/a | no |
| Blackbox panel: status, series, charts, events, dumps | missing | missing | n/a | partial (status server) |
| Blackbox setup and wizard (install, config, apply) | `unravis-blackbox` only | missing | yes | yes (local exe) |
| Updates page: status, check, download, install, dismiss result | missing | missing | n/a | no |
| Update settings (channel, auto-apply, skip, pause) | missing | missing | n/a | no |
| Update history, rollback, health state | missing | missing | n/a | no |
| Offline update folder verify and apply | missing | missing | n/a | yes in principle (file based) |
| Glossary and settings pages | missing | missing | n/a | no |
| Code mode settings, grants, replay (`/api/code/*`, `grant.*`) | `code run/types` only | n/a | yes | no |
| Logs info, client log, diagnostics bundle | `doctor` (small) | missing | yes | partial |
| Version and about (`/api/version`, `/api/info`) | exists | missing | yes | yes |
| Keeper status, migrate, remove old | missing | missing | n/a | no |
| Connections and SQL connections (test, remember, forget) | missing | missing (credentials; stays human only) | n/a | no |
| Export, report, ledger | missing | missing | n/a | no |

The matrix in section 3 turns each `missing` into a work item. Rows that carry secrets (master key, case key, connection credentials) stay UI-only by design; "parity" for them means a documented refusal with the exit code and message, not a command.

## 3. Principles

1. **Parity is a tested table, not a promise.** A new test walks the route table, the command tree and the matrix. Every route is either mapped to a command, or listed as `ui-only` with a reason. A new route with no entry fails the build. This also answers parity review findings 2, 3 and 7 (the existing tests compare the MCP bridge with itself).
2. **One backend, three adapters.** CLI, MCP and code mode call the same operation functions the HTTP handlers call. No logic is written twice.
3. **Read-only first.** Every read command works without a write grant. Every write command is gated (section 5).
4. **Machine contract.** `--json` everywhere, the existing envelope and error codes, documented exit codes (0 ok, 1 error, 2 usage, 3 not found, 4 denied or auth, 5 expired result, 6 would change something and `--yes` was missing, 7 partial), `--no-input` (never prompt), and `--output-file`.
5. **No new network surface.** Nothing new listens. Remote use rides the existing remote tool (SSH to the Salt master, then `cmd.run`).

## 4. Remote design for a headless install

### 4.1 The auth problem

The pipe policy needs the CLI to run as the **same logon SID** as the owner process. Salt `cmd.run` runs as `SYSTEM`. So on a server the existing pipe path will refuse a remote caller, which is the correct secure answer today. Three situations need different answers:

| Situation | Answer |
|---|---|
| **Blackbox only** (no Unravis app, service as SYSTEM) | The CLI `unravis-blackbox` talks to the files and the service directly. No pipe. Run as SYSTEM or an admin; the install folder ACL is the gate. |
| **Unravis app with a human owner** | Reads can use a new **file-based `ops` command set** that reads state files read-only, so SYSTEM can run it. Writes go through the app as the owner (see 4.3). |
| **Unravis headless service** (`app start --headless` under a service account) | The service account is the owner SID. Remote writes run the CLI as that account (`runas` or a scheduled task under that account), so the existing peer policy applies unchanged. |

**Decision for Opus review (OPS-S1):** do **not** add a token, a service-SID exception or a network listener to make SYSTEM work. Rely on (a) read-only offline reads, (b) running the CLI as the owner account for anything that writes. If the review disagrees, the alternative is an explicit, ACL-protected local "ops grant" file created by the owner, scoped to named operations and expiring, never a standing SYSTEM exception.

### 4.2 `unravis ops` (read-only, file based, SYSTEM-safe)

A new command group that never needs the pipe. It reads the install and data folders directly, opens case files read-only, and refuses to run if it would need a key it does not have.

| Command | What it returns |
|---|---|
| `ops version` | Unravis and Blackbox versions, build, channel, install path, signing key id, OS, uptime |
| `ops status` | Is the app running, which owner, locator state, pipe health, Blackbox service state, disk space, last crash file |
| `ops doctor` | Everything an engineer asks first, in one JSON: versions, config (scrubbed), service state, update state, Blackbox recorder health and last events, IIS log folders found and whether they are readable, IIS log load status per case, Redis and other log folders found, case stores found and whether each opens, clock and timezone, free disk, recent errors |
| `ops logs list|tail|bundle` | List app logs, tail with a cap, or write a **scrubbed bundle** zip (uses `internal/secretscrub`; refuses to include `.env`, keys, tokens, case keys, payloads) |
| `ops update status|history` | Current and last result, the held reason (paused, blocked, staged), remembered control sequence, the update history list |
| `ops update check` | Fetch and verify the signed manifest and control file, report "available / up to date / held" without downloading the program |
| `ops blackbox status` | Same facts the Blackbox panel shows, from the service files, with `--json` |
| `ops sources scan <folder>` | The read-only discovery the Add data wizard runs: which log files of which kind, readable or not, and **why not** (permission, locked, unknown format, wrong time zone rule) |

Every command takes `--host-label`, `--json`, `--no-input`, `--timeout`, and returns a single JSON document of bounded size.

### 4.3 Gated writes (`unravis update`, `unravis-blackbox update`)

Write commands are separate verbs, never a flag on a read verb:

| Command | Effect | Gate |
|---|---|---|
| `update apply` | Download (if needed), verify the signed manifest and the program SHA-256, apply, start, run the health check | signed update only; `--yes`; owner peer policy (4.1); audit record |
| `update rollback` | Restore the previous version through the existing health `Tracker` and `LaunchRollback` | same gates; refuses if no previous version is kept |
| `update skip <version>` / `update unskip` | Same as the UI skip | same gates |
| `update apply --from-folder <dir>` | Offline folder (`VerifyFolder`), same verification | same gates |
| `blackbox update` | Replace the recorder program, verify `--expect-sha256` **and** the signed manifest, restart the service, health check, roll back on failure | local admin or SYSTEM; `--yes`; audit record |
| `blackbox rollback` | Put back the previous recorder program | same |

All write commands:

- **Dry run by default** (`--yes` is required to change anything; without it the command prints the plan and exits 6).
- **Signed updates only.** The program's SHA-256 must match the signed manifest entry. A bare `--expect-sha256` with an unsigned file is not accepted for Unravis, and only as a second check for Blackbox.
- **Seq/version rules** from `Judge`. No downgrade except `rollback` to the kept previous version.
- **Audit log.** One append-only line per attempt (who, from where, command, arguments scrubbed, result) in the app log folder, owner-only, hash-chained like the code-mode audit. A failed audit write fails the command.
- **Health gate.** After apply the existing health Tracker decides keep or roll back; the CLI waits (bounded) and reports the final state.

### 4.4 Code mode and MCP

Read operations from 4.2 become code mode tools (`ops.version`, `ops.status`, `ops.doctor`, `ops.logs.list|tail`, `ops.update.status|history`, `ops.update.check`, `ops.sources.scan`) and MCP read tools. They are `Scoped` at the Auto/MCP columns like the existing audit tools. Write commands have **no** MCP tool and **no** code-mode binding: an agent can prepare and explain, a person or the remote tool with `--yes` applies.

## 5. Security model (Opus review required)

Threats and the control for each:

| Threat | Control |
|---|---|
| A remote caller changes or updates a server without the owner knowing | Write commands need `--yes`, run only as the owner account or local admin, and write a hash-chained audit line; no write over MCP |
| A malicious or stale update is installed | Signed manifest only, key role per channel, SHA-256 of the file, `Judge` seq and version, no downgrade except rollback to the kept previous version |
| New network listener | None added. The Blackbox status server keeps its existing read-key guard and is not extended with writes |
| SYSTEM exception for the pipe | Not added (4.1). Reads are file based. Writes run as the owner |
| Secrets leak into a bundle or output | Bundle and `doctor` go through `internal/secretscrub`; allowlist of files, never a folder copy; test with planted fake secrets; `.env`, key files and case keys are never read |
| Path traversal or a symlink in `logs tail` and `sources scan` | Resolve and compare against an allowlist of roots (install, data, Blackbox, IIS and Redis log folders the config names); refuse reparse points; size caps; read-only open |
| A scan or bundle floods the server | Bounded time, bytes and file count; `--timeout`; low I/O priority |
| An agent turns reads into writes | The capability matrix has Deny for write ops in the MCP and Sandbox columns; a test fails if a write op gains Allow there |
| Unproven remote command line quoting | Commands take structured flags only, no free shell strings, and the remote tool already has an allowlist and a capped audit file |

Opus security review is required for OPS-03 (`ops logs bundle`), OPS-07 (`update apply|rollback`), OPS-08 (`blackbox update|rollback`), OPS-09 (the audit log) and the auth decision OPS-S1.

## 6. The two first concrete diagnosis gaps (BG-SRV-UK-01)

Reported by nachi on 2026-11-26. Neither has a root cause yet, and the tool that found them could not tell why. That is itself the gap: `ops doctor` and `ops sources scan` must answer these questions without a person reading code.

### OPS-F1 Redis log could not be read

- **What is known.** A Redis log importer exists (`internal/importer/redis`, `redis-log`) and is registered. "Could not be read" is ambiguous: it can mean the **import** failed, or that the **remote tool** could not reach the file (the tool allows only `D:\UnravisBlackbox` and `C:\inetpub\logs\LogFiles`; Redis logs usually live elsewhere), or that the file was locked or in an unknown layout (Windows Redis ports write `redis_<port>.log` or the Memurai folder).
- **First step (confirm first).** Ask nachi which of the three happened (import error text, remote tool refusal, or empty result). Meanwhile reproduce with the importer against sample files for both Redis 7 and Memurai log layouts and with a UTF-16 file and a locked file. The test that fails without the change is named in the pull request.
- **Fix candidates.** (1) `ops sources scan` reports the Redis log and why it was or was not readable. (2) The importer's refusal text says the actual reason in plain words (permission, locked, format, time zone) instead of a generic read failure. (3) If the cause is the remote tool allowlist, add the Redis log folder to the tool's allowlist through its config (outside this repo).
- **Self-contained brief:** see work item OPS-F1 in section 7.

### OPS-F2 IIS file imported but missing from the investigation timeline

- **What is known.** Audit and SQL lanes appeared and the IIS lane did not, although the IIS file was loaded into the case. From the code, the paths that produce that result are:
  1. `timeline.Build` only lists events with a stated length (`qualifies` requires span greater than 0). IIS W3C rows with no `time-taken`, or time-taken 0, are point events and never show as overlapping rows. Absence rows only appear for sources in `v.Sources`.
  2. `timeline.disabled` turns the **whole** timeline off when any imported source has an unvalidated time zone rule (host-local without a host label), so an IIS file with host-local times and no host label can hide the lane or the timeline.
  3. `okImports` keeps only status ok; a file imported as duplicate or partial is dropped from the sources.
  4. The investigation timeline in the UI is built by the tracing lanes (`/api/cases/{id}/lanes`, `/tracing`), not by `internal/timeline`. The lane may filter by host or by the audit window and show nothing for IIS when the host label of the IIS file does not match the audit source host.
- **First step (confirm first).** Reproduce with a small IIS W3C file imported with each time zone rule (UTC field, host-local with and without a host label) and with and without `time-taken`, then call both `timeline.Build` and the lanes route. The failing test pins whichever of the four is real.
- **Fix candidates.** A visible reason on the lane ("IIS log loaded, 4,102 rows, none in this window / none with a duration / host label does not match") and `ops sources scan` plus `ops doctor` reporting the same per file. No silent empty lane.

Both items are separate pull requests and neither needs a new collector or agent.

## 7. Prioritised work items

Order is by value and risk. Every item is one branch, one pull request to `develop`, runs `bash tools/ci/prepush.sh` before the push, keeps tests Windows-safe, never commits `web/dist`, and ticks its backlog line only when verified on the real data path. Model column: who implements. **Opus review** only where marked.

| Id | Item | Model | Needs | Opus review |
|---|---|---|---|---|
| OPS-01 | Parity table test: walks the route table, command tree and matrix; every route mapped or listed `ui-only` with a reason (also fixes review findings 2, 3, 7) | Sonnet | none | no |
| OPS-02 | `ops` command group skeleton, shared JSON envelope, exit codes 3, 4, 6, 7, `--no-input`; `ops version` and `ops status` | Sonnet | OPS-01 | no |
| OPS-03 | `ops doctor` and `ops logs list|tail|bundle` with `secretscrub`, allowlisted roots, caps, planted-secret tests | Sonnet | OPS-02 | **yes** |
| OPS-04 | `ops update status|history|check` (read-only, verifies signatures, does not download the program) | Sonnet | OPS-02 | no |
| OPS-05 | `ops blackbox status` and `unravis-blackbox status|doctor|history|update check` | Sonnet | OPS-02 | no |
| OPS-06 | `ops sources scan <folder>`: the read-only discovery with a reason per file | Sonnet | OPS-02 | no |
| OPS-07 | `update apply|rollback|skip|unskip|apply --from-folder` with dry run, `--yes`, signed only, health gate | Sonnet (Opus plan) | OPS-04, OPS-09 | **yes** |
| OPS-08 | `unravis-blackbox update|rollback` with signed manifest check, restart, health, rollback on failure | Sonnet | OPS-05, OPS-09 | **yes** |
| OPS-09 | Hash-chained operations audit log (append-only, owner-only, a failed write fails the command) | Sonnet | OPS-02 | **yes** |
| OPS-10 | Code mode and MCP read bindings for the OPS-02..06 commands; matrix rows with Deny for write ops, and a test that no write op is Allow for MCP or Sandbox | Sonnet | OPS-03..06 | no |
| OPS-11 | Cases: create, archive, close, reopen from the CLI (no key operations); `cases show` richer | Sonnet | OPS-01 | no |
| OPS-12 | Import from the CLI: `import check` and `import run <case> <folder>` through the same job code as the UI, with progress and a final summary | Sonnet | OPS-06, OPS-11 | no |
| OPS-13 | Investigation timeline from the CLI and code mode (`timeline show`, lanes with the reason text from OPS-F2) | Sonnet | OPS-F2 | no |
| OPS-14 | Blackbox panel read parity: events, series, dumps from the CLI | Sonnet | OPS-05 | no |
| OPS-15 | Settings parity: update settings, glossary, code mode settings and grants (`grant.*`, `code.replay` commands that the matrix already lists) | Sonnet | OPS-01 | for grants only |
| OPS-F1 | Redis log could not be read: confirm, reproduce, explain, fix (section 6) | Sonnet | none | no |
| OPS-F2 | IIS file missing from the investigation timeline: confirm, reproduce, show the reason (section 6) | Sonnet | none | no |
| OPS-S1 | Decide the SYSTEM versus owner question (4.1) | Opus | none | is the review |

Items that carry secrets (master key, case key, connection credentials, payloads) are **not** in this list on purpose; they stay UI-only with a documented refusal.

### Coordination

The whole-project review plan (`docs/review/PLAN.md`, items W-xx) is being built by the factory thread. This plan touches none of its files except where the two overlap on Blackbox (W-03, W-04: recorder events and sampling). OPS-05 reads those events; it must not start before W-03 merges. Update-path items W-06 to W-10 change `internal/update` and the updater; OPS-04, OPS-07 and OPS-08 must be rebased after they merge.

## 8. First builds (low risk, small pull requests)

1. OPS-01 (the parity table test) and OPS-02 (`ops version`, `ops status`).
2. OPS-F1 and OPS-F2 confirm-first work in their own pull requests.
3. OPS-04 and OPS-05 read-only update and Blackbox status.

Write commands (OPS-07, OPS-08) are not started until OPS-S1 and the Opus review of OPS-09 are done.

## 9. Live verification

Each item names its live check. The server BG-SRV-UK-01 (Blackbox rc.62) is the target for the read-only items: `ops version`, `ops status`, `ops doctor`, `ops sources scan` over `C:\inetpub\logs\LogFiles`, and `unravis-blackbox status`. They run through the existing read-only remote tool, which keeps its audit file. Write items are verified on a test copy first, and never on a customer server without nachi's go.
