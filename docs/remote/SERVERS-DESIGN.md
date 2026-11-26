# Servers: connect to a remote server and pull Blackbox and logs

Status: analysis and design, 26 Nov 2026. UI is not built yet: the prototype `web/prototype/servers-PROTOTYPE.html` comes first and needs the owner's approval. Backend pieces that are not UI are split into small PRs (section 9).

Owner ask (nachi, 26 Nov): the SSH + Salt debugging done by hand (PR 348 tool) becomes a real Unravis feature. From his PC, Unravis connects to a server and pulls all Blackbox recordings and the logs he needs (IIS, Redis, SQL, event logs, Blackbox files) with no hand-copying.

## 1. What already exists (reuse, do not rebuild)

| Piece | Where | What we take from it |
|---|---|---|
| Read-only Salt tool (PR 348) | `tools/remote/*.ps1`, `docs/ops/REMOTE-SALT.md` | The command grammar (strict character set, exact minion ids, no globs, path allowlist `REMOTE_LOG_PATHS`, no follow modes), host key pinning by SHA256 fingerprint with no trust on first use, the audit JSONL with redaction. These become Go code in one package; the PowerShell tool stays as a dev tool until the CLI verbs replace it. |
| CLI parity plan (PR 346) | `docs/ops/CLI-PARITY-PLAN.md` | `unravis ops` verbs, exit codes (6 = needs `--yes`), gated write verbs for update/restart, hash-chained audit, pipe auth by owner logon SID, one backend with three adapters (CLI, MCP, code mode). Remote update and restart are this plan's verbs running over our transport; we do not invent a second write path. |
| Blackbox readers | `internal/blackbox` (`Source`, `FolderSource`, `HTTPSource`, `Location`) | `Source{Manifest, Status, OpenFile}` is the seam. `HTTPSource` already takes an injectable `*http.Client`, so an SSH tunnel plugs in without touching it. The read key header, per-address rate limit, read budgets and containment checks are reused as they are. |
| Saved Blackboxes | `internal/bbsaved`, `internal/sealedfile` | Sealed registry pattern (DPAPI, atomic write, shared root). The server list is a second file of the same kind. Secrets are never in the entry struct that goes to the UI. |
| Scrubber | `internal/secretscrub` | The single scrubber for everything pulled. Fail closed, linear time. |
| Import pipeline | `internal/discover`, `internal/importer/*`, import check and jobs | Pulled files land in a staging folder and go through the same discover and import jobs as Add data > Files. No new parser. |
| Lite | `internal/lite` | Allowlist-only read-only viewer. A Blackbox-only server (HTTP, no SSH) shows as a limited server row, see 4.4. |

## 2. Goals and non-goals

Goals
- One list of servers. Each has one or more ways to reach it.
- Browse only allowed locations, pull on demand or on a schedule, into a case or the Blackbox view, with progress, resume, size limits, hash check and secret scrubbing.
- Read-only by default. Anything that changes the server (update, restart) goes through the CLI plan's gated verbs and is never one click.
- Same functions in the UI, the CLI and code mode (parity rule). Write verbs have no MCP and no code-mode binding, as in the CLI plan.

Non-goals (now)
- No agent or collector installed on the server (BACKLOG rule: nothing needing a new collector or agent without the owner's go). The Blackbox recorder already running there is only read.
- No file upload to servers. No shell. No port forwarding offered to the user, only the one internal tunnel to a recorder status page.
- No password manager integration. No Kerberos or SSO.

## 3. One remote layer, three consumers

```
UI (Servers screen)   CLI: unravis ops server ...   MCP / code mode (read verbs only)
          \                      |                        /
           +------ internal/remote  (one backend) --------+
                  |  registry (sealed)   |  jobs (pull)   |  audit
                  |        Transport interface            |
        +---------+---------+----------------+------------+
        ssh-direct        salt-via-master    blackbox-http   folder/share
        (x/crypto/ssh)    (ssh to master)    (HTTPSource,    (FolderSource,
                                              optional ssh    exists today)
                                              tunnel)
```

Decision R1: use `golang.org/x/crypto/ssh` (already in `go.mod`), not `ssh.exe`.
- Why: no password in an environment variable or helper process, a host key callback that checks the pinned SHA256 fingerprint in-process, works the same on every PC, and can open the tunnel for the recorder status page. Windows `ssh-keyscan` 9.5 failing against OpenSSH 10 (found with PR 348) stops mattering.
- Cost: key formats are OpenSSH and PEM only. A PuTTY `.ppk` is refused with a message that says how to export it (as PR 348 does today). No `ssh-agent` in the first slice.

Decision R2: the command grammar and path allowlist live in `internal/remote/grammar` as Go code with one JSON file of test vectors that both the Go tests and the PowerShell tool's Pester tests read. That is how we avoid two grammars drifting apart. When `unravis ops server` covers every action, `tools/remote` is reduced to a thin caller or retired.

Decision R3: the audit log is the CLI plan's hash-chained audit, with remote fields added (server id, transport, verb, path, bytes, sha256, exit). The PR 348 JSONL format is read-only legacy.

### Transport interface (sketch)

```go
type Transport interface {
    Probe(ctx) (HostInfo, error)                         // os, version of Unravis Blackbox if any, clock offset
    List(ctx, dir string) ([]Entry, error)               // name, size, mtime; inside allowlist only
    Open(ctx, path string, off int64) (io.ReadCloser, error) // ranged read, streamed
    Digest(ctx, path string) (sha256 string, err error)  // read-only hash on the server
    Close() error
}
```

`ssh-direct` runs the grammar's read verbs (`ls`/`dir`, `tail -c +N`, `Get-Content`, `sha256sum`/`Get-FileHash`) over exec channels. `salt-via-master` runs the same verbs through `sudo -n salt '<exact id>' cmd.run ...` on the master, in 48 KiB chunks by default (base64 on the wire, about a third larger) because Salt jobs time out on big payloads. Built in `internal/remote/saltmaster` and unit-tested against a fake master; **not yet verified on a real Salt master**. Above a few hundred MB this path is slow; see `LOG-TRANSFER-RESEARCH.md` for what to send, when to compress, and what to measure first. `blackbox-http` is `HTTPSource`, dialled over the SSH tunnel when the status page listens on loopback (the default `bind 127.0.0.1`), so no firewall rule or `0.0.0.0` bind is needed to read a remote recorder.

## 4. Design

### 4.1 The server entry (sealed)

Stored in `servers.dat`, next to `blackboxes.dat`, same Protector. Fields: id, display name, host, port, user, auth kind (`key` | `password`), secret (sealed, never in the struct sent to the UI or the CLI), pinned host key fingerprint, transports enabled, Salt minion ids (exact), allowlisted folders by kind (see 4.3), optional Blackbox address + read key (re-uses a `bbsaved` entry by id, not a copy), pull limits. The UI sees only `hasSecret: true`.

### 4.2 Trust and connect

1. Add: host, port, user, key file or password. Nothing connects yet.
2. First connect fetches the host key and shows the SHA256 fingerprint. The owner must compare it with one from the server admin and confirm; there is no trust on first use and no "continue anyway". A changed key later is a hard stop with the old and new fingerprint shown. The fingerprint is stored with the server entry.
3. Test connection runs `Probe`: login ok, OS, Blackbox found (port, version, state from `/status`), Salt master found (`salt --version`), clock offset.

### 4.3 What can be pulled (the allowlist)

An editable list per server, grouped by the kinds Unravis already imports. Each kind has a default folder suggestion and a file pattern, both editable and both checked by the grammar (absolute path, no `..`, no symlink escape resolved on the server with `realpath`/`Resolve-Path` before every read):

| Kind | Typical folder | Lands in |
|---|---|---|
| Blackbox recordings | Blackbox folder (`bb-YYYYMMDD-HH.ndjson`, `manifest.json`) | Blackbox view, read as a folder source |
| IIS logs | `C:\inetpub\logs\LogFiles\W3SVC*` | import job, iis importer |
| Redis | log file, slowlog dump | import job, redis importer |
| SQL Server | `ERRORLOG*` | import job, sqlserver importer |
| Windows event logs | queried read-only with `wevtutil qe <channel> /q:... /f:xml /c:N`, streamed; nothing is exported onto the server's disk | import job, winevents importer |
| Application logs | folders the owner adds | import job, applog |

Open item (needs the owner's server): the exact folders and the event log channels he wants. The first slice ships the defaults above and an editable list, so nothing blocks.

### 4.4 Blackbox-only server (Lite case)

A server with only a Blackbox address (no SSH) is a row with `blackbox-http` and nothing else. Status, manifest and recordings work as today. Browse, logs and Salt stay off, with a one-line reason. Unravis Lite shows the same row read-only and never offers the Add or Pull of anything beyond the Blackbox.

### 4.5 Pull pipeline

`plan -> list -> select -> stream to staging -> verify -> scrub -> hand over`

1. Plan: the owner (or a schedule) names server, kind, time window. `List` returns names, sizes, mtimes. Over the size limit means the plan stops and says what to narrow (limits in 4.6).
2. Stream: each file goes to `<case>/remote/<server>/<job>/<name>.part` with an offset journal, so a dropped connection or a restart resumes at the last complete chunk. Jobs run in the background (existing jobs runner), the UI polls progress; nothing blocks the UI thread. The file being written by a live recorder is read up to the manifest's last complete hour and re-pulled next time, never half a line.
3. Verify: the server computes the hash of the byte range (read-only), the client computes it on the fly; mismatch deletes the `.part` and retries once, then fails the file, not the job.
4. Scrub: lines go through `secretscrub` before they reach disk. Raw text is never written to the staging folder. Lines the scrubber cannot process are dropped and counted (fail closed). The manifest records the wire hash and the stored hash, because scrubbing changes bytes.
5. Hand over: Blackbox files become a folder source in the Blackbox view; logs are offered to the existing import check, then the import job. Provenance (server, path, time pulled, hashes) is stored with the case so the evidence view can show where a record came from.

### 4.6 Limits (all editable, safe defaults)

Per file 512 MB, per job 4 GB, 3 files in parallel, 25 MB/s ceiling, 20 minutes per job unless extended, 5 connections per server. Disk-space check on the PC before a job starts. A file bigger than the limit is listed as "too big" and can be pulled by time slice (tail from an offset) instead.

### 4.7 Schedules

A schedule is a saved plan plus an interval, run by the app while it is open (and by the tray helper once that exists). It uses the sealed secret under the owner's own logon, never a service account, to match OPS-S1 in the CLI plan. If the app is closed, nothing runs and the next open shows what was missed. Remote credentials are never copied to a scheduler that runs as another identity.

### 4.8 Remote update and restart

Out of this feature's first slices. When the CLI plan's gated verbs land (OPS write verbs: `--yes`, dry run, exit code 6, signed updates), the Servers screen gets a Danger zone that calls them through `Transport.Exec` with the same grammar extended by the plan's write allowlist. Until then the screen shows Blackbox version and "update available" as information only.

## 5. Security model (Opus review required)

| Risk | Control |
|---|---|
| Credentials leak | Sealed with the shared Protector, never in logs, case files, MCP or code mode responses, URLs or process arguments/environment. The UI gets only `hasSecret`. Secrets are scrubbed from error text before display. |
| Host key spoof | Pinned SHA256 fingerprint, shown to the owner, compared by the owner. Key change is a hard stop. No `InsecureIgnoreHostKey`, a lint test greps for it. |
| Command injection (minion ids, paths, patterns) | No shell string is ever built from user input. Exec channels run a fixed verb table; arguments are validated against the grammar (character set, length, no globs), each argument single-quoted for the remote shell as the last step. Minion ids must match the stored exact list. A fuzz test and the shared test vectors cover this. |
| Path traversal | Allowlist roots, canonicalised on the server, compared after canonicalisation; `..`, `~`, env vars, UNC and device paths refused; symlinks resolved before the check. |
| Download of something huge or hostile | Size limits, read budgets, no execution of pulled content, staging folder isolated from the app's read paths, file names regenerated (never trusted from the server). |
| Secrets in pulled logs | `secretscrub` before write, fail closed. |
| Audit gaps | Every connect, list, read, pull and refusal is audited with server id, verb and path (CLI plan's hash-chained audit). A failed audit write stops the operation. |
| Abuse of write verbs | None in this feature. Update and restart only through the CLI plan's gated verbs, no MCP or code-mode binding. |
| Salt scope creep | Fixed `fn` table (`test.ping`, `grains.item`, `cmd.run` for the read verbs only), exact ids, `sudo -n`, never feeds a sudo password. |

### 5.1 Findings from the Opus review of S0-S2 (2026-11-26) and what was done

- **Windows links and junctions:** `Resolve-Path` does not follow them, so each read walks from the file up to the matching allowed folder and refuses any link or junction on the way (tested against real PowerShell with a `mklink /J`).
- **Silent server:** the SSH handshake has a deadline and stops when the request is cancelled (tested with a server that accepts and never speaks).
- **Strict paths and roots:** a colon only after the drive letter (no alternate data streams), no doubled separators or UNC and device prefixes, and an allowed folder must be a real folder (not a whole drive or `/`).
- **Password prompts:** keyboard-interactive is answered only for a plain password prompt, never "One-time password" or similar.
- **Linux listing:** `find` ends each entry with a NUL so odd file names cannot forge entries.
- **Accepted, documented:** on Linux there is a short gap between `realpath` and the read in which a file could be swapped for a link. The account Unravis uses is read-only, and every read is still checked by the server's own permissions. A later hardening is `openat2` with `RESOLVE_BENEATH`, which needs a helper on the server and is not part of this release.

## 6. Beginner copy rules

Every error says what happened and what to do (for example: "The server's key changed. Do not continue. Ask the server's admin for the key and compare it with the one shown."). Words: Server, Recordings, Logs, Pull, Trust this server. No "transport", "minion" or "allowlist" in the UI; "Salt" appears only as "Salt master" where the owner chose it. The advanced panel may use the words.

## 7. Parity: UI, CLI, code mode

| UI | CLI | Code mode (read only) |
|---|---|---|
| Add server | `unravis ops server add --host ... --user ... --key-file ...` (password read from the console, never an argument) | not offered (writes the registry) |
| Trust this server | `unravis ops server trust <id> --fingerprint SHA256:...` | not offered |
| Test connection | `unravis ops server test <id>` | `servers.test(id)` |
| Browse | `unravis ops server ls <id> <kind>` | `servers.ls(id, kind)` |
| Blackbox status | `unravis ops server status <id>` | `servers.status(id)` |
| Pull | `unravis ops server pull <id> <kind> --since ... --into <case>` | `servers.pull(plan)` (starts the same job; pull is read-only on the server) |
| Schedule | `unravis ops server schedule ...` | not offered |

## 8. Decisions (owner delegated, 2026-11-26)

1. Defaults: the Blackbox folder, IIS logs, Windows event logs, and Redis and SQL logs where found. Editable per server.
2. A pull lands in the Blackbox view by default, with a one-click "Add to a case".
3. Key sign-in is the default and recommended. Password sign-in is an explicit opt-in per server (the current Salt server uses a password), stored only in the sealed vault.

### 8.1 Coexistence with the installed app

- Nothing here uses a fixed port. The SSH tunnel to a recorder binds an ephemeral 127.0.0.1 port and closes with the job, so it cannot clash with a local recorder on 7790 or with a second Unravis.
- `servers.dat` sits beside `blackboxes.dat` in the per-user data folder and uses the same sealed Protector, so an install, update or uninstall treats it like the other sealed registries. No machine-wide state.
- Pulled files go to a staging folder inside the data folder (`remote-staging/<job>`), never into the Blackbox recorder's own folder or the install folder.
- The Salt tool's settings (`~/.unravis-remote`) are read once to offer an import into `servers.dat`, never shared as live state.

## 9. Delivery order (small PRs, dependency graph)

```
S0 grammar + vectors ──┬─> S1 sealed registry ──> S2 ssh-direct transport ──> S4 pull job + staging ─┬─> S6 CLI verbs
                       │                          └─> S3 blackbox over ssh tunnel (status + manifest) │
                       └─> S2b salt-via-master (built, unit-tested, not live-verified)                                   └─> S7 UI (after prototype approval)
S5 scrub + hash + resume tests (with S4)    S8 schedules    S9 remote update/restart (after CLI plan write verbs)
```

- S0 `internal/remote/grammar`: character rules, verb table, path allowlist, shared JSON vectors. Opus security review.
- S1 `internal/remote/registry`: sealed `servers.dat`, no secret in the exported struct. Opus review.
- S2 ssh-direct with pinned host key, Probe/List/Open/Digest. Opus review.
- S3 `blackbox-http` over the SSH tunnel: status, version, manifest, recordings through the existing `HTTPSource`.
- S4 pull job: plan, stream, `.part` resume, size limits, hash, scrub, handover to Blackbox view and import jobs.
- S6 CLI verbs and code-mode read bindings; S7 UI after the owner approves the prototype; S8 schedules; S9 later.

Coordination with the other two threads: the Salt thread (PR 348) keeps its tool read-only and contributes the vectors for S0 and the Salt transport for S2b; the CLI plan thread (PR 346) owns the `unravis ops` root, the exit codes and the audit chain, and S6 adds a `server` group under it. Backlog items: BL-603 (Servers: remote pull layer and screen), listed in `docs/BACKLOG.md`.

## 10. Prototype v4 and v5 additions (2026-11-26)

`web/prototype/servers-PROTOTYPE.html` (v5, awaiting approval) adds, all simulated:

- **Access check.** Copy-ready read-only commands (and a note for an IT admin) on the Trust, Pick and Reach steps: host key fingerprint from the PC and on the server, `salt-key -L`, `manage.status`, `test.ping`, and a listing of the Blackbox and log folders per chosen machine.
- **Case-aware range (v5: read from the case, never picked here).** The investigation window and the reference range are chosen once, in the Investigation timeline. The Servers screen shows them read-only (UTC) with a "Change in Investigation timeline" button, and defaults each item's "How much" to the window plus the reference, or the window alone when no reference is set. The time between the two is not copied. Any item's range can still be changed in the table. If the case has no window yet, or only the timeline's last-hour default, the screen says so and falls back to per-item ranges (24 hours, 3 days, 7 days). Not for a case: the same three ranges. See section 11 for where the values come from in code.
- **Manifest first.** The file list (names, sizes, time ranges) is read before any data moves.
- **Compression.** Auto (text squeezed once on the server at low priority, zips sent as they are), Always, or Never, with size and time estimates and a plain warning above about 15 minutes.
- **Background back-fill.** After the case window lands, the rest of the last 7 days is fetched quietly, newest first, pausable.

The numbers (compression ratio, Salt speed) are assumptions until measured. Research and sources: `LOG-TRANSFER-RESEARCH.md`.

## 11. Where the case range comes from (checked in the code, 2026-11-26)

- Stored in the case's encrypted workspace note: `GET /api/cases/{id}/workspace` (`internal/server/case_workspace.go`), key `windows` = `{incidentStart, incidentEnd, referenceStart, referenceEnd}`, strings `YYYY-MM-DDTHH:mm`, UTC. The web side writes it through `rememberWindows` and `queueWorkspaceSave` in `web/src/lib/appState.svelte.ts` and reads it back as `savedWindows`.
- The timeline shows `defaultWindows` (the hour before now, and the hour before that) when nothing is saved. The Servers screen must not treat those as a chosen range: it reads the saved `windows` only, and shows the "no window yet" state when they are missing or invalid (`parseStoredWindows` is strict: real times, end after start).
- Limits the pull must respect: the investigation endpoint caps each window at 7 days and never reads the gap between them; the tracing picker allows up to 186 days. Servers uses the 7-day figure.
- The note forbids credential and address keys, so the Servers feature never writes a host name into it. Only the machine names and times shown on screen are kept, and only in the pull record, not in the workspace note.
- Real-data path for the build: the Servers service reads `windows` through the same workspace service the timeline uses (no second store), converts to UTC instants, and builds the manifest request from them. Not yet built; not yet verified on a live case.
