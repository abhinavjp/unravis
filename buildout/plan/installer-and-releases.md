# Installer and releases

Detail behind plan phases P11 (installer) and P12 (release pipeline). Every choice here is a planner proposal unless decisions.md Part A says otherwise. The owner answered Q1 to Q10 on 2026-10-27 (decisions.md A43 to A53): Go single-file installer, app window with browser fallback, SignPath signing, warn-and-continue without a browser, manual update check, opt-in scoped firewall rule. Q2 adds a shared data folder and a key-keeper service (P14) that the installer must create, upgrade, repair and remove, and undo on rollback.

## 1. Installer technology

### What the installer must do

Preflight; explain admin approval before the Windows prompt; choose the install location; check dependencies; register the Blackbox service (and Unravis start-at-sign-in); firewall rule and port choice; a first-run launch link; upgrade in place that keeps case data and sealed secrets; repair; uninstall that asks before deleting data; silent mode with a config file; a log; rollback on failure. One file to download.

### Options compared

| | Go self-extracting + web UI | WiX MSI | MSIX | Inno Setup | NSIS |
|---|---|---|---|---|---|
| One file | Yes | Yes (or a bundle exe for UI) | Yes | Yes | Yes |
| Brand UI with shared tokens | Yes, same CSS file | Hard; custom bootstrapper UI needed | No custom UI | Limited; Pascal-drawn pages | Limited; plugin-drawn pages |
| Services | Yes, Go code (`golang.org/x/sys/windows/svc/mgr`, already a dependency) | Yes, built in | Restricted | Yes, by script | Yes, by script or plugin |
| Firewall rule | Yes, Windows Firewall COM or `netsh` | Needs extension | No | Script | Plugin |
| Upgrade and repair | We write it | Built in | Built in | Built in, mostly | We write it |
| Rollback | We write a journal | Built in (transactions) | Built in | Partial | We write it |
| Silent mode | We write it | Built in (`msiexec /qn`) | Limited | Built in | Built in |
| Group policy deployment | No | Yes | Intune | No | No |
| Signing needed to run | No | No | Yes, always | No | No |
| Extra build tools | None (Go and Node already used) | .NET SDK and WiX toolset | Windows SDK | Inno compiler | NSIS compiler |
| Languages in the build | Go, TypeScript (already) | + WiX XML, C# custom actions | + manifest | + Pascal script | + NSIS script |

### Pick: Go self-extracting installer with an embedded web UI

Reasons:

1. Brand without drift. The installer UI imports `web/src/brand/tokens.css` and the same fonts. No second styling system.
2. No new build tools. CI already has Go and Node.
3. Full control of the flows the owner asked for (explain before UAC, rollback, keep sealed secrets, ask before deleting data). With MSI or Inno we would still write custom actions for services, firewall, DPAPI-safe upgrade and the browser launch.
4. Services and the Windows API are already in reach through `golang.org/x/sys/windows`.

Costs we accept: we own the rollback journal, the Add/Remove Programs entry and repair. Group policy deployment (MSI) is deferred; an MSI wrapper can be added later if asked for.

MSIX is ruled out: mandatory signing, and services and firewall rules are restricted.

## 2. Build-time and run-time needs

### Build time (developer PC and CI)

| Need | Why |
|---|---|
| Go 1.27.3 (from go.mod), `CGO_ENABLED=0`, `GOTOOLCHAIN=local` | All three binaries |
| Node 22 (or 20.19+), npm | Web UI and installer UI builds |
| `go-winres` (Go tool, pinned) | Version resource, icon and manifest in the exe (shown in file properties and UAC) |
| Playwright with Chromium (dev only) | Screenshots and UI smoke tests |
| `signtool.exe` from the Windows SDK, or the signing service CLI | Only when signing |
| PowerShell 5.1 or 7 | `build.ps1`, `run-screens.ps1` |

### Run time (the target PC)

| Need | Required? | Check |
|---|---|---|
| Windows 10 or 11 64-bit, Windows Server 2019 or later | Required | Preflight |
| A modern browser (Edge or Chrome) | Required to use Unravis | Preflight warns if missing (Q8) |
| WebView2 runtime | Optional; only for the installer window | Falls back to the browser |
| .NET, Visual C++ runtime, Node | Not needed | Nothing to install |
| Admin rights | Only for the Blackbox service, firewall rule, or an install for all users | Explained before UAC |
| Free disk | 100 MB for the app; 1 GB on the Blackbox folder volume | Preflight |

The installer installs no third-party dependencies.

## 3. Process model and window

- `unravis-setup.exe` starts unelevated. It serves the installer UI on a loopback port with a one-time token in the URL (same idea as the app's launch link) and opens it:
  1. in a WebView2 window when the runtime is present (`github.com/jchv/go-webview2`, pure Go, no CGO);
  2. otherwise Edge or Chrome in `--app=<url>` mode;
  3. otherwise the default browser.
- When a step needs admin rights, the UI first shows the "Admin approval" screen, then starts a second copy of itself with "run as administrator" (`ShellExecuteW` verb `runas`) and the argument `--worker --pipe <name>`.
- The two talk over a named pipe whose security descriptor allows only the current user's SID and SYSTEM. The worker accepts only a fixed list of actions with typed arguments, never a shell command.
- The elevated worker never opens a browser, never starts Unravis, and exits when the pipe closes.
- Unravis is started at the end by the unelevated process, as the signed-in user.

## 4. Install flow

### Where things go (PROPOSAL)

| Item | Default | Admin? |
|---|---|---|
| Unravis for this user | `%LOCALAPPDATA%\Programs\Unravis\unravis.exe` | No |
| Unravis for all users (option) | `%ProgramFiles%\Unravis\unravis.exe` | Yes |
| Blackbox program | `%ProgramFiles%\Unravis Blackbox\unravis-blackbox.exe` | Yes |
| Blackbox recordings | `D:\UnravisBlackbox` (or `C:\UnravisBlackbox` when there is no D: drive) | Yes |
| Case data, sealed files | Unchanged: `%LOCALAPPDATA%\APIAuditExplorer\` and the per-user config folder | No |
| Installer log | `%LOCALAPPDATA%\Unravis\setup-logs\setup-<yyyymmdd-hhmmss>.log` | No |
| Add/Remove Programs entry | HKCU Uninstall key (per user) or HKLM (all users, Blackbox) | Matches |

So an Unravis-only install for one user needs no UAC at all.

### Steps

1. Preflight (no changes made):
   - Windows version and 64-bit;
   - existing Unravis install (version, location) and running `unravis.exe` processes;
   - existing `UnravisBlackbox` service;
   - browser present (Edge or Chrome);
   - WebView2 present (for information only);
   - free disk on the install and recordings volumes;
   - port 7788 free (if Blackbox chosen);
   - recordings folder writable;
   - admin rights available (can the user elevate?);
   - pending reboot (warn only).
   Each line shows a sticker: Ready, Partly ready, Failed, with a plain fix.
2. Options: install for me or for all users; location; add the Blackbox on this PC (off by default) with its settings (name, folder, keep days, size, port); firewall rule for 7788 (off by default; remote addresses typed); start Unravis when I sign in (on); open Unravis when done (on).
3. Admin approval explained (only if any option needs it): what will change (list), what will not (case data, sealed secrets, other services), then the Windows prompt.
4. Install: copy files to a staging folder, verify hashes, move into place, write the Add/Remove Programs entry, create the Start menu shortcut and the start-at-sign-in shortcut, register and start the Blackbox service, add the firewall rule, show the Blackbox read key once with a Copy button and a "I have saved it" tick.
5. Done: "Open Unravis" primary button; the launch link is made by starting `unravis.exe` as the user, which opens the browser itself.

### Ports and URL reservation

- Unravis keeps `--port 0` (any free loopback port) by default. The installer offers a fixed port only as an advanced option, written to a small config file the app reads at start. No firewall rule, because Unravis is loopback only.
- Unravis and the Blackbox use Go's own HTTP server, not http.sys, so no `netsh http add urlacl` reservation is needed. The installer adds none.
- Blackbox port 7788 is checked for clashes in preflight. If busy, the user picks another port; it goes into `blackbox.json`.

## 5. Upgrade, repair, uninstall

### Upgrade in place

- Detected from the Add/Remove Programs entry and the exe version resource.
- Stops Unravis (asks first if it is running; never kills it without asking) and the Blackbox service.
- Replaces program files only. Case data, sealed files (`sqlconnections.dat`, master key records, Blackbox read keys) and Blackbox recordings are never touched. DPAPI scope never changes.
- `blackbox.json` is kept; new settings get defaults; the old file is backed up next to it.
- Restarts the Blackbox service and, if it was running, Unravis.
- Downgrade is refused with a plain message unless `--allow-downgrade` is passed.
- Proof in P11: upgrade from the previous build keeps a case, a saved connection and Blackbox recordings readable.

### Repair

- Re-copies program files from the installer, re-creates shortcuts, re-registers the service with the saved settings, re-adds the firewall rule if it was chosen. Touches no data.

### Uninstall

- From Add/Remove Programs or `unravis-setup.exe --uninstall`.
- Screen asks, each off by default:
  - "Also delete my cases and saved connections" (type DELETE to confirm; lists folder and size);
  - "Also delete the Blackbox recordings" (type the Blackbox name to confirm, as in A38).
- Always removes: program files, shortcuts, the Add/Remove Programs entry, the service, the firewall rule.
- Never deletes a `.unravis-case` file outside the data folder (A18).
- Case data deletion is a plain delete after the typed DELETE confirmation, and is logged. It does not go to the Recycle Bin: encrypted case folders exceed Recycle Bin limits and would be deleted outright anyway (ruling F5, 2026-10-28, owner to confirm).

## 6. Silent mode, logging, rollback

### Silent mode

- `unravis-setup.exe --silent --config setup.json [--log <path>]`.
- `setup.json` keys: `scope` (`user` or `machine`), `location`, `blackbox` (object or null: `name`, `folder`, `keepDays`, `maxGB`, `minFreePercent`, `port`), `firewall` (null or `remoteAddresses`), `startAtSignIn`, `openWhenDone` (forced false in silent mode), `uninstall` with `deleteCases` and `deleteRecordings` (both must be explicit `true`).
- Unknown keys fail the run (no guessing). The parser has its own tests.
- Exit codes: 0 done, 1 preflight failed, 2 config invalid, 3 cancelled at UAC, 4 failed and rolled back, 5 failed and rollback incomplete (log names what is left).
- The Blackbox read key in silent mode is written once to a file named in the config with an ACL for the current user only; it is never printed to the log.

### Logging

- Plain text, one line per action with time, action id, result. Secrets never logged (read keys, passwords, launch tokens).
- The log path is shown on the Done and Failed screens with an "Open log folder" outline button.

### Rollback

- Every action writes a journal entry before it runs: what it will do and how to undo it (for example "service created: delete service UnravisBlackbox", "file replaced: restore from staging backup").
- On failure or Cancel, the journal is replayed backwards. Each undo is logged.
- For upgrades, the old program files are moved to a backup folder, not deleted, until the new version has started once.
- A failed Blackbox install undoes itself (A39) inside the same journal.
- Rollback is tested by injecting a failure after each action (P11 Proof 3).

## 7. Installer UI

### Technology

The installer UI is a small Svelte 5 app in `web/setup/`, built by Vite into the installer binary with `go:embed`, exactly like the main app. It imports `../src/brand/tokens.css` and the font files. Reasons: same tokens and fonts by construction; the team already knows the stack; Playwright can capture it like any other page.

### Shared tokens (no drift)

- One file: `web/src/brand/tokens.css` (made in P1). The app and the installer both `@import` it.
- `web/tests/tokens.test.cjs` runs over `web/setup/` too.
- A CI check fails if `web/setup/` defines any `--token` already defined in `tokens.css`.
- Shared components that both need (sticker, primary and outline buttons, step rail) live in `web/src/brand/components/` and are imported by both.

### Layout rules

- Fixed window 880x600 in WebView2; in a browser the same frame centred.
- 56 px header with the brand mark and the wordmark "Unravis Setup", plus the version on the right in mono.
- Left step rail (200 px): Welcome, Options, Approval, Install, Done. Current step has a 2 px accent marker. Finished steps show a Done sticker. Pending steps are hatched.
- One primary button at the bottom right. Back and Cancel are outline or quiet buttons.
- Dark by default, follows the system theme; `data-theme` can force it for screenshots.

### Wireframes (ASCII)

Welcome and preflight:

```
+-----------------------------------------------------------------+
| [mark] Unravis Setup                                    v1.0.0  |
+-------------+---------------------------------------------------+
| > Welcome   | Install Unravis                                   |
|   Options   | [In a nutshell] Unravis reads audit logs and     |
|   Approval  | server health. It never changes your servers.     |
|   Install   |                                                   |
|   Done      | Checks                                            |
|             |  Windows 11 64-bit          [Ready]               |
|             |  Browser (Edge)             [Ready]               |
|             |  Disk C: 41 GB free         [Ready]               |
|             |  Port 7788                  [Ready]               |
|             |  Unravis already installed  [Partly ready]        |
|             |    v0.9.2 found. This will upgrade it.            |
|             |                                                   |
|             |                      ( Cancel )  [ Next ]         |
+-------------+---------------------------------------------------+
```

Options:

```
| > Options   | Where and how                                     |
|             |  (o) Just for me (no admin needed)                |
|             |  ( ) For everyone on this PC (admin needed)       |
|             |  Folder  [ C:\Users\...\Programs\Unravis ] (Change)|
|             |  [x] Start Unravis when I sign in                 |
|             |  [ ] Also place a Blackbox recorder on this PC    |
|             |      Name [ ]  Folder [ D:\UnravisBlackbox ]      |
|             |      Keep [7] days  Max [2] GB  Port [7788]       |
|             |      [ ] Allow other PCs to read it (firewall)    |
|             |          Addresses [ ]                            |
|             |                      ( Back )  [ Next ]           |
```

Admin approval (only when needed):

```
| > Approval  | Windows will ask for admin approval               |
|             | [In a nutshell] Placing the Blackbox needs admin  |
|             | rights. Here is exactly what changes.             |
|             |  Will change: service UnravisBlackbox, folder     |
|             |  D:\UnravisBlackbox, firewall rule (if ticked)    |
|             |  Will not change: your cases, saved connections,  |
|             |  other services, any database                     |
|             |                      ( Back )  [ Continue ]       |
```

Progress:

```
| > Install   | Installing                                        |
|             |  ===========-------------------  (accent on line) |
|             |  Copy files                 [Done]                |
|             |    C:\...\Programs\Unravis\unravis.exe            |
|             |  Shortcuts                  [Running]             |
|             |  Blackbox service           [Waiting] (hatched)   |
|             |  Firewall rule              [Waiting] (hatched)   |
|             |                              ( Cancel )           |
```

Done:

```
| > Done      | Unravis is installed                              |
|             |  Version 1.0.0 in C:\...\Programs\Unravis         |
|             |  Blackbox "web-01" is Running on port 7788        |
|             |  Read key (shown once): [mono key]  ( Copy )      |
|             |  [ ] I have saved the read key                    |
|             |  Log: C:\...\setup-20261027-0912.log (Open folder)|
|             |                         [ Open Unravis ]          |
```

Failure and rollback:

```
| > Install   | Install did not finish. Nothing was left behind.  |
|             |  Copy files                 [Done] -> undone      |
|             |  Blackbox service           [Failed]              |
|             |    Port 7788 is used by another program.          |
|             |    Pick another port in Options, then try again.  |
|             |  Firewall rule              [Waiting] -> skipped  |
|             |  Log: C:\...\setup-....log  (Open folder)         |
|             |                  ( Close )  [ Back to Options ]   |
```

If rollback is incomplete, the heading says "Install did not finish. Some items are left." and lists each item with the command to remove it.

Upgrade:

```
| > Welcome   | Upgrade Unravis 0.9.2 to 1.0.0                    |
|             | [In a nutshell] Your cases, saved connections and |
|             | recordings stay as they are. Only the program     |
|             | files change.                                     |
|             |  Unravis is running.  [Partly ready]              |
|             |    It will be closed. Unsaved work in an open     |
|             |    wizard is lost.                                |
|             |                      ( Cancel )  [ Upgrade ]      |
```

Uninstall:

```
| > Remove    | Remove Unravis                                    |
|             |  Always removed: program, shortcuts, Blackbox     |
|             |  service, firewall rule                           |
|             |  [ ] Also delete my cases and saved connections   |
|             |      C:\Users\...\APIAuditExplorer  (3.2 GB)      |
|             |      Type DELETE to confirm [        ]            |
|             |  [ ] Also delete the Blackbox recordings          |
|             |      D:\UnravisBlackbox (1.1 GB)                  |
|             |      Type the name web-01 to confirm [        ]   |
|             |                      ( Cancel )  [ Remove ]       |
```

Repair is the Upgrade screen with the heading "Repair Unravis 1.0.0" and the same "In a nutshell" text.

### Capture

A `--capture` mode (only in builds with a `capture` build tag, never in release builds) starts the UI with a fake worker that replays fixed results, so every screen, including failures, can be captured by the same Playwright harness.

## 8. Code signing

| Option | Cost | Effect on users | CI fit |
|---|---|---|---|
| Unsigned | None | SmartScreen shows "Windows protected your PC" until enough people run it; UAC shows "Unknown publisher"; some antivirus flag new unsigned exes | Nothing to do |
| Azure Trusted Signing | Low monthly fee | Publisher name on UAC; reputation builds over time | GitHub Action available; keys never leave Azure |
| SignPath Foundation | Free for qualifying open source | Publisher is SignPath Foundation with the project name | GitHub integration; manual approval per release |
| OV certificate on a cloud HSM or hardware token | Yearly fee | Publisher name on UAC; reputation builds over time | Cloud HSM works in CI; a USB token does not |
| EV certificate | Higher yearly fee | Same as OV since Microsoft removed instant SmartScreen reputation for EV | As OV |

### Owner choice: SignPath Foundation (A48)

SignPath needs the repository to be public and to meet their rules, and each release needs an approval in their service. Check the repo's visibility and apply before P12 starts. Until it is approved the pipeline ships the unsigned case below.

### Plan for both cases

- The pipeline has a `sign` step that runs only when the secret `SIGNING_METHOD` is set. It signs `unravis.exe`, `unravis-blackbox.exe`, `unravis-keeper.exe` and `unravis-setup.exe` (inner binaries first, then the installer that embeds them), with an RFC 3161 timestamp.
- Unsigned release: release notes and `install.md` show the SmartScreen screen and explain "More info", then "Run anyway", and how to check the SHA-256 with `Get-FileHash`. Builds are submitted to Microsoft's malware analysis portal to reduce antivirus false alarms.
- Signed release: `install.md` shows the publisher name on the UAC prompt and how to check the signature (`Get-AuthenticodeSignature`).
- The installer's preflight never depends on being signed.

## 9. Release pipeline (`.github/workflows/release.yml`)

Trigger: push of a tag matching `v*.*.*` (including pre-releases such as `v1.0.0-rc.1`). Manual `workflow_dispatch` with a tag input for re-runs.

Jobs:

1. `build` on `windows-2022`:
   - checkout at the tag; check the tag matches `^v\d+\.\d+\.\d+(-[0-9A-Za-z.]+)?$`;
   - setup Go (from go.mod) and Node 22 with caches;
   - `web/`: `npm ci`, `npm run check`, `npm test`, `npm run build`; `web/setup/`: same;
   - `go vet ./...`, `go test ./...`, `go mod tidy` diff check;
   - `build.ps1 -Version <tag without v>` builds `unravis.exe`, `unravis-blackbox.exe`, then `unravis-setup.exe` (which embeds the other two and the Blackbox zip);
   - screenshot check (`run-screens.ps1`) and `docs-check.mjs`;
   - optional sign step;
   - package assets and write `SHA256SUMS.txt`;
   - upload assets as a workflow artifact.
2. `smoke` (needs `build`), matrix `windows-2022`, `windows-2025`: see section 11.
3. `draft` (needs `smoke`): create or update a draft GitHub Release for the tag with `gh release create --draft --verify-tag`, attach the assets, and write the release notes. An existing draft is found with `gh release list --json tagName,isDraft` (not `gh release view`, which does not reliably see drafts); a re-run replaces its assets and keeps its notes, and a published release is never touched.
4. Publishing is a person pressing Publish on the draft. No job publishes automatically.

Permissions: `contents: write` only on the `draft` job; everything else read-only. All actions pinned to a commit SHA.

### Assets

| File | Contents |
|---|---|
| `unravis-setup-vX.Y.Z-windows-x64.exe` | The one-file installer |
| `unravis-vX.Y.Z-windows-x64.zip` | Portable: `unravis.exe`, licence files, README |
| `unravis-blackbox-vX.Y.Z-windows-x64.zip` | `unravis-blackbox.exe`, `blackbox.json`, README (about 6 MB) |
| `SHA256SUMS.txt` | One line per file above, `sha256sum` format |

The compatibility copy `api-audit-explorer.exe` is not a release asset.

### Release notes

- Built from `.github/release-notes-template.md` by `tools/release/New-ReleaseNotes.ps1`: version, date, a "What changed" list, an "Upgrade notes" block, a "Verify your download" block with the checksum command, and a signed or unsigned note. No GitHub API call is made.
- "What changed" is the plain git log between the previous tag and this tag (one bullet per commit, oldest first). Commits whose subject starts with `CI:` go into a collapsed "Build and test changes" block at the end of the list. A stable tag is compared with the previous stable tag (release candidates in between are included); a prerelease tag is compared with the nearest earlier tag; the first release lists the whole log and says so. It works in Windows PowerShell 5.1 and PowerShell 7 (`tools/release/Test-Release.ps1` checks both).
- The notes end with "Not yet verified on a clean PC." until the repository variable `CLEAN_VM_VERIFIED` is set to `true` (do that only after a person has run the installer on a clean machine for that release).
- If the `SIGNPATH` secret is set while the workflow has no signing step, the build stops with a message that says what the owner must do (delete the secret to ship unsigned, or wire the signing step).
- The draft is edited by a person before publishing.

### Version stamping

- `build.ps1 -Version X.Y.Z` feeds ldflags into `internal/version` and `go-winres` writes the same version into each exe's file properties.
- `unravis.exe --version`, `/api/version` and the installer header all show it.

## 10. Upgrade check

- A "Check for updates" outline button in the About area of Unravis.
- On press, Unravis calls `https://api.github.com/repos/abhinavjp/unravis/releases/latest` with no user data and no auth; it compares the tag with `internal/version.Version`.
- Results (stickers): "You have the latest version", "Version X.Y.Z is available" with a link to the release page (opens in the browser; no download by Unravis), "Could not check (offline or GitHub did not answer)".
- No background calls, no scheduled checks, no auto-download (Q9).
- Dev builds show "This is a development build" and do not call out.

## 11. Clean-machine smoke test

Runs in the `smoke` job on fresh GitHub-hosted runners (they are clean each run):

1. Verify `SHA256SUMS.txt` against the downloaded artifact.
2. Silent install for the user scope with a `setup.json` that includes a Blackbox on port 7788 with folder `C:\UnravisBlackbox` (runners have no D:).
3. Check: exe present, Add/Remove Programs entry present, `UnravisBlackbox` service Running, Blackbox status on 7788 answers with the read key and fails without it.
4. Start `unravis.exe --no-browser` as the runner user, read the launch link from its log, call `/api/version` and compare with the tag.
5. Run a short Playwright smoke: open the launch link, see the home screen, open the Blackbox page and see the local Blackbox as Running.
6. Upgrade test: install the previous published release first (downloaded from GitHub Releases by tag), create a demo-seeded case and a saved connection, run the new installer silently, check the case opens and the connection is still "saved".
7. Uninstall keeping data; check program gone, service gone, data folder still there.
8. Uninstall deleting data (on a second fresh install); check the data folder and recordings are gone.
9. Upload the setup log and screenshots as artifacts.

Not covered by hosted runners: Windows Server 2019 (the `windows-2019` image is retired) and machines without a browser. Before publishing, run steps 2 to 8 by hand once in a Server 2019 VM or Windows Sandbox and record the result in the draft notes.
