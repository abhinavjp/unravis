# P-45 install report — rc.35 built and installed on this PC

Run 6 continued, 2026-11-13 (orchestrator: main agent). Outcome met: the owner's installed Unravis is **0.1.0-rc.35** built from this tree (HEAD f6c819c), it starts, and the owner's cases were kept.

## Pre-install state (read before any change)
- Installed: **v0.1.0-rc.34** (commit f6c819c, built 2026-11-12), user scope, folder `%LOCALAPPDATA%\Programs\Unravis`, startAtSignIn false, no Blackbox/keeper (so no admin approval needed).
- App running: pid 43196.
- Cases on disk (`%LOCALAPPDATA%\APIAuditExplorer\cases`, excluding `.index`): **2**.
- git: HEAD f6c819c, 17 tags, highest rc tag rc.20; **rc.35 free** (not a tag, not installed).

## Build
- Fresh web build first: `(cd web && npm run build)` → exit 0 (125 modules; web/dist + setup UI refreshed so the Go embed is current).
- Installer build: `powershell -NoProfile -ExecutionPolicy Bypass -File build.ps1 -Version 0.1.0-rc.35 -SkipWeb -SkipTests` → **exit 0**.
  - `-SkipWeb -SkipTests` are build.ps1's documented flags for "a caller that has already run it": the coordinator ran `npm run build` (P-44 gate 7) and `go test ./...` (P-44 gates 3/4) green, and re-ran the web build immediately before. This avoids build.ps1's `npm ci` (which deletes web/node_modules) and the redundant full test re-run. build.ps1 still ran `go vet ./...` and stamped rc.35 into all four programs + the installer manifest.
- **Obstacle (resolved with owner authorization):** the first build attempt failed at build.ps1:124 — the deprecated compatibility copy `dist/unravis.exe → dist/api-audit-explorer.exe` could not overwrite the target because two MCP server processes were running it (pids 25940 & 34912, `dist/api-audit-explorer.exe --mcp --port 18787`, this repo). The owner was asked and chose **"Stop the 2 MCP processes"**. Only those two pids were stopped (the installed app pid 43196 and the unrelated `brain-payroll-all-portals` processes on port 18765 were left untouched). The lock freed; the re-run completed exit 0. Their MCP client can restart them (the compat copy is now rc.35).
- Outputs (all 2026-11-13 17:36 local): dist/unravis.exe, dist/unravis-keeper.exe, dist/unravis-blackbox.exe, dist/unravis-setup.exe (105,790,464 B), dist/api-audit-explorer.exe (compat copy).
- `dist/unravis.exe --version` → **Unravis v0.1.0-rc.35 (commit f6c819c, built 2026-11-13)**.
- `dist/unravis-setup.exe --version` → **Unravis Setup v0.1.0-rc.35 (commit f6c819c, built 2026-11-13)**.

## Quit the running app (a silent upgrade never closes it; close first)
- `unravis.exe --quit` (the documented graceful quit). The app took ~10 s to exit; verified no `unravis` process remained before upgrading. State preserved (graceful, not force-killed).

## Silent upgrade
- Config: `dist/upgrade-config.json` = `{}` (the upgrade keeps the first-install choices from setup-state.json whatever the config says; setup.json was absent at root).
- Command: `dist/unravis-setup.exe --silent --upgrade --config dist/upgrade-config.json --log dist/upgrade-rc35.log` → **exit 0**.
- Installer message: "done: Unravis is upgraded to 0.1.0-rc.35. Your cases and saved connections were kept."
- Log (dist/upgrade-rc35.log, 2026-11-13T13:37:44Z): preflight windows/payload/existing/app-running/browser/webview2/disk-app/admin all **ready** (admin "Not needed for this install"); reboot **partly** (Windows waiting for a restart from an earlier update — does not stop the install). Steps close-app / stop-services / program-folder / copy-files / shortcuts / arp / state / verify all **ok**; "done upgrade 0.1.0-rc.35".
- Note: the preflight "existing" check read a stale registry version **rc.5** (the actual installed files were rc.34). The `arp: ok` step rewrote the Add/Remove Programs entry; it now reads rc.35 (verified below). A pre-existing reporting quirk, not a failure.

## Verification (after upgrade)
- Installed `unravis.exe --version` → **Unravis v0.1.0-rc.35 (commit f6c819c, built 2026-11-13)**. ✓
- App started the documented way (run the installed exe): **APP RUNNING pid 62544**. ✓ (silent mode never opens it; started once, which also clears the upgrade backup.)
- Cases on disk after: **2** (= 2 before). ✓ Counts only; no case names/IDs recorded. The upgrade log confirms cases + saved connections kept. UI case-list confirmation on live data is P-46.
- setup-state.json → version **0.1.0-rc.35**, scope **user**. ✓
- HKCU Add/Remove Programs entry → DisplayName "Unravis", DisplayVersion **0.1.0-rc.35**. ✓

## Git scope (no source change, no tag/release/push)
- `git tag --list | wc -l` = **17** (unchanged); HEAD **f6c819c** (unchanged). No tag, release, push, signing.
- `build.ps1` **unchanged**.
- `dist/` is **gitignored** → the rebuilt binaries, upgrade-config.json and upgrade-rc35.log do not appear as source changes.
- P-45 created **no new source file**. (The untracked `.go` files in `git status` are the plan's earlier packet deliverables — P-13/21/34/35/37/38/39 etc. — not P-45.) The only repo writes from P-45 are generated build artifacts (web/dist, cmd/unravis-setup/ui/assets, dist/) and this report.

## Proof checklist (P-45)
- [x] build.ps1 exit code 0.
- [x] Installed `unravis.exe --version` → 0.1.0-rc.35.
- [x] The app opens (pid 62544) and the owner's existing cases are kept (count before 2 = count after 2; counts only).
- [x] `git tag --list` unchanged (17); `git status --short` shows no new source change from P-45.

## Hand-off to P-46
- The installed app is rc.35 from this tree, running. P-46 (live browser acceptance) opens it on LIVE data at 1440x800 and 1100x760, confirms the cases are listed in the UI, re-measures the lane-fit (540 px constant), and verifies the deeper history cases (Back twice, reload after Forward, open-menu Esc) plus the deferred R2-01/R2-02/R2-03 and the F-03/SEC-01 all-sources total check.