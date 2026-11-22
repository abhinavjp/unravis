# Groom 06: Distribution, signing, updates (Tauri desktop)

Source: research report 07 (`07_windows_distribution_signing_updates.md`) and `docs/desktop/DECISIONS-draft.md` (sections 2, 8, 12). Range used: BL-400 to BL-419 (17 items; BL-418 and BL-419 free).
Base rule: keep Go `unravis-setup.exe` and the Go ed25519 OTA updater. No Tauri NSIS, no `tauri-plugin-updater`, no MSIX. Do not duplicate BL-149 (U1-U7), BL-151 to BL-156, BL-168 to BL-171 in `docs/BACKLOG.md`; these items extend them for the desktop shell.
Tags in the report: `[SOURCE]` = cited, `[INFERRED]` = the report's own guess. Most setup/OTA behaviour in the report is `[INFERRED]` about our own code; check the repo in the spec stage.
Acceptance tags: CI, live PC (nachi), elevated/UAC.

Note on DECISIONS-draft: it names BL-312 as the signing owner action. BL-400 below is the groomed form of that; the spec stage should merge or renumber one of them.

---

## BL-400 Signing identity chosen and onboarded
- Outcome: one Authenticode signing provider is picked, verified eligible and set up, so CI can sign.
- Scope (in): confirm owner country and licence (OSS or proprietary); pick per the report's rule (Azure Artifact Signing Basic $9.99/mo if US/CA individual or org with 3-yr history; else SSL.com IV $129/yr + eSigner $180/yr; SignPath $0 only if pure OSI open source); create account; store CI credentials (OIDC for Azure, secrets for SSL.com).
- Out of scope: any EV certificate (report: no SmartScreen benefit since March 2024); the CI steps (BL-401).
- Depends on: none. Start in P0 (identity checks take lead time).
- Risks / unknowns: owner country and licence unverified (report risk 1); Azure individual onboarding paused in 2026 (risk 1); SignPath "no security scanners" rule vs our read-only diagnostics (risk 2); Azure SmartScreen warnings after CA rotation, no SLA (risk 3). Cert max validity 460 days since 2026-03-01, so renewal is yearly work.
- Acceptance:
  - Provider test-signs a throw-away exe from a GitHub Actions run, `signtool verify /pa /v` passes with timestamp (CI).
  - Signed file shows the publisher name on the owner's PC (live PC (nachi)).
- Size: S. Phase: P0 start, done before P4.
- Needs owner action? YES: country/licence answer, identity checks, money ($9.99/mo or about $309/yr), credentials.

## BL-401 CI signs the Go binaries, fails on any signing error
- Outcome: `unravis.exe`, `unravis-blackbox.exe`, `unravis-keeper.exe`, `unravis-lite.exe` are signed (SHA-256, RFC 3161 timestamp) in the release workflow.
- Scope (in): signing step before bundling; timestamp URL per provider (Azure `timestamp.acs.microsoft.com`; fallbacks DigiCert/Sectigo); verify step with `signtool verify /pa /v`; fail on any non-zero exit, because `signtool` exit 2 (timestamp warning) leaves an untimestamped file (report pitfall); never re-sign third-party DLLs.
- Out of scope: Tauri exe and installers (BL-402); OTA check (BL-403).
- Depends on: BL-400; existing release workflow (BL-153, BL-154).
- Risks / unknowns: Azure action needs `windows-2022` or `windows-2025`, no ARM runner; Azure certs live 3 days so timestamp is mandatory. Existing `Release.ps1` flow and ed25519 signing order must stay intact.
- Acceptance:
  - A release-candidate run signs all four files; `signtool verify` passes (CI).
  - A run with the timestamp server blocked fails the job (CI).
  - Feed hashes and ed25519 signatures are made AFTER Authenticode signing and still verify (CI).
- Size: M. Phase: P4.
- Needs owner action? No (after BL-400).

## BL-402 Sign Tauri shell and installers last
- Outcome: `unravis-desktop.exe`, the elevated worker, `unravis-setup.exe` (and later MSIs) are signed in bottom-up order.
- Scope (in): build shell with `tauri build --no-bundle` (report option) or `signCommand` and let Tauri skip already-signed sidecars; sign the worker exe BEFORE embedding in `unravis-setup.exe`; sign `unravis-setup.exe` last; one verify gate over every `.exe`/`.msi` in the release set.
- Out of scope: MSI build (BL-412, BL-413).
- Depends on: BL-401; P2/P3 shell exists (`desktop/src-tauri`).
- Risks / unknowns: any byte change after signing breaks the signature; our setup embeds files, so order matters. Tauri CLI >= 2.8.0 needed for the sidecar skip fix (report).
- Acceptance:
  - `signtool verify /pa /v` on every shipped file passes (CI).
  - Unpack the installed tree on a clean PC: every first-party exe shows the publisher, third-party DLLs keep the vendor signature (live PC (nachi)).
- Size: M. Phase: P4.
- Needs owner action? No.

## BL-403 OTA verifies Authenticode on the staged installer
- Outcome: before applying, the updater checks the staged `unravis-setup.exe` with the ed25519/SHA-256 manifest AND `WinVerifyTrust`.
- Scope (in): add the Authenticode check after download and before hand-off; clear error and no apply when it fails; policy for unsigned dev builds (explicit flag, off in release).
- Out of scope: changing the ed25519 trust root (it stays the update root, DECISIONS section 2.4).
- Depends on: BL-402; BL-149 slices (U-series), BL-168.
- Risks / unknowns: an offline PC may stall on revocation checks (report risk 6, `golang/go#63238`); decide whether to check revocation or signature+timestamp only. After a signing provider change the publisher name may change; spec must say if the name is pinned.
- Acceptance:
  - Unit test: valid signed, unsigned, tampered and wrong-publisher setup files; only the valid one applies (CI).
  - Real signed release on the real feed: a PC upgrades N-1 to N and the log shows both checks passed (live PC (nachi)).
  - Unsigned setup with a valid ed25519 signature is refused (CI).
- Size: M. Phase: P4.
- Needs owner action? No.

## BL-404 Setup lays down `unravis-desktop.exe` and stops it on upgrade
- Outcome: `unravis-setup.exe` installs, upgrades and removes the Tauri shell next to `unravis.exe`, with clean stop of shell and Go backend.
- Scope (in): add the shell to the file set and journal; per-user and per-machine; Start menu and autostart entry per DECISIONS (direct `HKCU\...\Run`); stop shell (RPC or `--wait-pid`) before swap; journal and `--recover` cover the new file; uninstall removes it.
- Out of scope: services logic (unchanged); WebView2 (BL-405); the P1 sidecar-vs-split-exe decision (input, not output).
- Depends on: P1 (exe layout settled), P2/P3 shell; BL-402.
- Risks / unknowns: whether the sidecar is `unravis.exe` or `unravis-core.exe` is not settled (DECISIONS 2.3); an MCP copy holding the exe can lock the file (existing note in BL-171). Report says "minimal new code" but that is `[INFERRED]`; read the repo first.
- Acceptance:
  - Install, upgrade, uninstall on a clean user: files and registry entries as expected, nothing left behind (live PC (nachi)).
  - Upgrade while the shell window is open and while `unravis --mcp` runs: no locked-file error, one owner after restart (live PC (nachi)).
  - Per-machine path with services (elevated/UAC).
  - Kill power/process mid-swap, run `--recover`: old or new version, never half (live PC (nachi)).
- Size: L (split in spec: per-user, then per-machine). Phase: P4.
- Needs owner action? UAC only for the per-machine test.

## BL-405 WebView2 detection and install modes
- Outcome: setup checks the Evergreen WebView2 runtime and installs it when missing, online or offline.
- Scope (in): detect via `HKLM\SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}`; online path runs the embedded bootstrapper (about 1.8 MB); offline path uses the standalone installer (`/silent /install`, about 127 MB, separate artifact); `skip` for headless server packages; no fixed runtime; set `minimumWebview2Version`.
- Out of scope: MSI packaging of the offline runtime (BL-413).
- Depends on: BL-404.
- Risks / unknowns: bootstrapper needs internet and may fail behind a strict proxy (report); installer size budget <= 25 MB (DECISIONS section 9) holds only if the 127 MB file is a separate download; AV/EDR locking the WebView2 folder is an open item.
- Acceptance:
  - Machine with runtime present: no download, install finishes (live PC (nachi)).
  - Clean Windows 10 or Server 2019+ VM without the runtime: shell starts after install (live PC (nachi)).
  - Offline VM with the standalone installer, network off: shell starts (live PC (nachi)).
  - Runtime install needs elevation for per-machine (elevated/UAC).
- Size: M. Phase: P4.
- Needs owner action? UAC; a clean VM or PC is needed (nachi's).

## BL-406 Desktop-aware OTA hand-off and boot health
- Outcome: the existing idle-aware update (BL-168) and 3-bad-start rollback (BL-169) work with the shell.
- Scope (in): Go tells the shell the update is ready; shell flushes state, closes WebView2, exits; Go runs `unravis-setup.exe --apply-staged --wait-pid <pid>`; boot health counts healthy only when BOTH shell (window/IPC handshake) and Go (server ready) report; 3 failed starts run `--rollback`; busy hook (BL-171) also counts an open window or shell actions.
- Out of scope: new feed fields (BL-155 gaps are separate); per-machine UAC policy (BL-409).
- Depends on: BL-404, BL-168, BL-169, BL-171; P3 (pipe) for the notify channel.
- Risks / unknowns: report text says "localhost HTTP/IPC" but DECISIONS drops loopback in desktop mode; spec must use the pipe. Shell "exit cleanly" may race the Job Object kill; report gives no flag name `--apply-staged`/`--wait-pid` as existing; check the repo.
- Acceptance:
  - Real signed N-1 to N update with the window open and idle: shell closes, update applies, shell relaunches, result note shows (live PC (nachi)).
  - Release with `--test-crash-on-start` in the shell: after 3 bad starts it rolls back to N-1 without a window (live PC (nachi)).
  - Same on a localhost feed with an ephemeral CI key (CI).
- Size: L (split: hand-off; health + rollback). Phase: P4.
- Needs owner action? No (UAC only if per-machine, see BL-408).

## BL-407 DPAPI continuity of Key Keeper across upgrades
- Outcome: proof that `UnravisKeeper` still decrypts sealed secrets after an OTA upgrade and, later, an MSI upgrade.
- Scope (in): seal a secret, upgrade with `unravis-setup.exe`, decrypt; same with `msiexec` once BL-413 exists; service account must stay `NT SERVICE\UnravisKeeper`; test lives in `verify-release.yml`.
- Out of scope: changing the Keeper design.
- Depends on: BL-153 (verify-release); MSI half depends on BL-412/413.
- Risks / unknowns: report risk 5 says this was not prototyped; `[INFERRED]` that stop-overwrite-start keeps identity. Note the report's `UnravisKeeper` account is "dedicated or virtual"; confirm which one the current code uses.
- Acceptance:
  - Windows runner: install N-1, seal secret, upgrade to N, read secret OK (CI).
  - Same upgrade on nachi's PC with a real Keeper and a real stored secret (elevated/UAC).
  - MSI MajorUpgrade variant (CI, after BL-413).
- Size: M. Phase: P4 (OTA half can start now).
- Needs owner action? UAC for the live run.

## BL-408 verify-release covers the desktop and the server
- Outcome: the release check proves desktop upgrade and rollback, and that headless Server 2016 stays intact.
- Scope (in): extend `verify-release.yml` (BL-153): install previous release, run shell + services, force check on the real public feed, assert both versions, refuse bad signature and downgrade, run 3-bad-start rollback; per-user (standard user via scheduled task) and per-machine; WebView2-less Lite/Blackbox path unchanged; Authenticode verify of the downloaded files.
- Out of scope: new test infra for browser mode.
- Depends on: BL-153, BL-402, BL-404, BL-406.
- Risks / unknowns: GitHub runners are Windows Server 2022/2025, not Server 2016 or Windows 10 (report 07 does not offer a Server 2016 runner); Server 2016 proof needs a VM, so it may stay manual. Elevated per-machine steps on runners work only because runners are admin.
- Acceptance:
  - Workflow passes N-1 to N on the real public feed for per-user and per-machine (CI).
  - Bad-signature, downgrade and unsigned cases are refused (CI).
  - Server 2016 headless Blackbox upgrade on a VM or the owner's server (live PC (nachi)) and (elevated/UAC).
- Size: L (split per-user / per-machine / server). Phase: P4.
- Needs owner action? Server 2016 VM or box, UAC.

## BL-409 Enterprise update policy (HKLM key + ADMX)
- Outcome: IT can disable or pin updates by policy, which the updater obeys before any network call.
- Scope (in): read `HKLM\SOFTWARE\Policies\Unravis\Updater` (`AutoUpdateDisabled`, `UpdateMode` 0/1/2, `UpdateChannel`, `PinnedVersion`, `ServiceAutoUpdateDisabled`); policy wins over user settings; show "managed by your organisation" in Help > Updates; ship ADMX/ADML.
- Out of scope: GPO deployment tooling; beta/lts channels if the feed has none (spec checks).
- Depends on: BL-149, BL-171; ADMX can ship with BL-413.
- Risks / unknowns: channel `beta`/`lts` may not exist in our feed; report's key list is `[INFERRED]` (modeled on Edge Update). Reading HKLM from a user process is fine; writing needs admin.
- Acceptance:
  - Unit tests: each value and combination (CI).
  - Set `AutoUpdateDisabled=1` as admin on a real PC: no feed request is made (log/pktmon) and Help > Updates says managed (elevated/UAC).
  - ADMX loads in `gpedit.msc` on a Pro PC (live PC (nachi)).
- Size: M. Phase: P4 (can run in parallel).
- Needs owner action? UAC to write the policy key.

## BL-410 Corporate proxy and offline TLS behaviour in the updater
- Outcome: the Go updater uses the Windows system proxy and does not hang on air-gapped hosts.
- Scope (in): wire `tailscale.com/net/tshttpproxy` (report) into the updater transport; keep `crypto/x509` native store; measure CRL stall on a network-isolated Server 2019/2022; handle `SSL_CERT_FILE` change in Go 1.27 (platform verifier off unless `GODEBUG=x509sslcertoverrideplatform=0`).
- Out of scope: Rust-side HTTP (the shell makes no network calls per DECISIONS).
- Depends on: BL-149.
- Risks / unknowns: report risk 6 is unmeasured; new dependency vs "minimal footprint" goal; `tshttpproxy` pulls Tailscale code, check size and licence in the spec; PAC-file proxies not mentioned in the report.
- Acceptance:
  - Unit test with a fake system proxy setting (CI).
  - On a PC behind a real authenticated or TLS-inspecting proxy, a real feed check succeeds (live PC (nachi)) (needs such a network; flag if nachi has none).
  - Isolated Server 2019/2022 VM: update check fails fast with a clear message, no hang over 30 s (live PC (nachi)).
- Size: M. Phase: P4.
- Needs owner action? No, but a proxy test network and a VM are needed.

## BL-411 Spike: WiX v5 standalone vs Tauri WiX v3 fragments
- Outcome: a written decision with a working throw-away MSI.
- Scope (in): build a minimal MSI with `<ServiceInstall>`/`<ServiceControl>` for the three services (including `NT SERVICE\UnravisBlackboxSql` virtual account) in both ways; compare effort, VBSCRIPT need on Win11, per-user scope, upgrade behaviour.
- Out of scope: shipping any MSI.
- Depends on: P4 start; BL-401 helpful.
- Risks / unknowns: Tauri bundles EOL WiX v3.14.1 (support ended 2025-02-06) and needs the VBSCRIPT optional feature on Windows 11 (report risk 4); Tauri WiX hardcodes `perMachine` (issue #13792). Report's own fragment is untested here.
- Acceptance:
  - Spike MSI installs, upgrades (MajorUpgrade) and uninstalls the three services on a VM, with no orphaned services (elevated/UAC).
  - Decision note in `docs/adr/` (CI: lint only).
- Size: S. Phase: P4 (spike).
- Needs owner action? UAC on a test VM.

## BL-412 Headless `Unravis-Blackbox-Server.msi`
- Outcome: a signed MSI for Windows Server 2016+ with Blackbox, BlackboxSql, Keeper and Lite only. No WebView2, no Tauri shell.
- Scope (in): per the BL-411 decision; `ALLUSERS=1`, silent `msiexec /i ... /qn /norestart`, `ServiceControl` for stop/start/remove; ProductCode detection; signed.
- Out of scope: the desktop MSI (BL-413); GPO instructions beyond a short README.
- Depends on: BL-411, BL-402, BL-407.
- Risks / unknowns: two update owners (OTA updater and MSI) can race, so managed installs need BL-409 `AutoUpdateDisabled` or an agreed rule (report: OTA "skips when installed via managed MSI without override", `[INFERRED]`); DPAPI across MajorUpgrade is unproven.
- Acceptance:
  - Silent install, upgrade and uninstall on a real Server 2016 machine or VM; services run under the right accounts (elevated/UAC).
  - Keeper secret survives the MSI upgrade (BL-407) (elevated/UAC).
  - Real Blackbox recording still collects and Unravis reads it (live PC (nachi)).
- Size: L (split: install/uninstall; upgrade + DPAPI). Phase: P4.
- Needs owner action? UAC; Server 2016 target.

## BL-413 `Unravis-Desktop-<ver>-x64.msi` (workstation, with optional offline WebView2)
- Outcome: enterprise MSI for GPO/Intune/SCCM that installs shell + Go backend (+ optional services as features).
- Scope (in): WiX per BL-411; `ALLUSERS=1`; optional offline WebView2 variant (about 127 MB) as a separate build; ADMX (BL-409) ships next to it; short Intune note (wrap as `.intunewin`; raw LOB MSI allows a single CLI argument).
- Out of scope: Intune/SCCM admin guides beyond one page.
- Depends on: BL-411, BL-404, BL-405, BL-409, BL-402.
- Risks / unknowns: size budget (installer <= 25 MB) applies to the online one only; per-user MSI not supported by Tauri WiX (issue #13792); MSI + OTA coexistence rule is open.
- Acceptance:
  - Silent install and uninstall on a clean PC (elevated/UAC).
  - Install through a real GPO or Intune test is NOT possible in CI; try on the owner's domain if one exists, else mark "not verified" (live PC (nachi)).
  - Offline VM with the offline variant starts the shell (live PC (nachi)).
- Size: L. Phase: P4/P5.
- Needs owner action? UAC; optional domain/Intune tenant.

## BL-414 WinGet package and CI submit
- Outcome: `winget install Unravis.Unravis` works, new releases are submitted from CI.
- Scope (in): multi-file manifest, `Scope: machine`, `ElevationRequirement: elevationRequired`, installer = signed MSI (BL-413); `winget-releaser` or `wingetcreate update --submit` in the release workflow; track manifest schema 1.12.0 vs 1.28.0.
- Out of scope: Chocolatey (report: only if Server 2019/2022 admins ask; not groomed).
- Depends on: BL-413, BL-400, BL-402.
- Risks / unknowns: new packages wait 1 to 4+ weeks, 5,000+ open PRs (Oct 2026); Authenticode strongly recommended, SmartScreen/Defender checks run; schema deprecation label (`winget-pkgs#467910`, report risk 7). Needs a Classic PAT with `public_repo` + `workflow`.
- Acceptance:
  - Manifest validates with `winget validate` and `wingetcreate` (CI).
  - `winget install --manifest <local>` on a clean PC installs and starts (live PC (nachi)).
  - Public merge and `winget install Unravis.Unravis` from the real source (live PC (nachi)); can only be confirmed after the external merge.
- Size: M. Phase: P5.
- Needs owner action? YES: PAT secret; publishing is public (owner go).

## BL-415 Fresh-PC reputation check: SmartScreen and Smart App Control
- Outcome: documented result of installing the signed release on clean Windows 11 PCs, with and without Smart App Control.
- Scope (in): download from the real release URL, record SmartScreen prompt, SAC verdict, Defender result for installer, shell, services; document the "More info > Run anyway" steps if reputation is not built yet.
- Out of scope: buying EV; forcing reputation.
- Depends on: BL-402.
- Risks / unknowns: reputation builds organically, so early releases may still warn (report); Azure CA rotation may reset warnings (risk 3). Result varies by day; this is evidence, not a pass/fail gate.
- Acceptance:
  - Screenshots and notes from a clean Win11 PC and one with SAC on, saved in `docs/` (live PC (nachi)).
- Size: S. Phase: P4.
- Needs owner action? A clean PC or VM.

## BL-416 CI size and footprint gate for the release set
- Outcome: the release fails when the installer is over budget.
- Scope (in): measure total download size (setup + shell + Go files + bootstrapper) in CI; target <= 25 MB, fail > 35 MB (DECISIONS section 9); report the offline MSI separately.
- Out of scope: runtime RAM/CPU budgets (other area).
- Depends on: BL-404, BL-405.
- Risks / unknowns: no baseline number exists yet (P0 measures it); signing adds a small amount per file.
- Acceptance:
  - CI step fails on an inflated test file and passes on the real release set (CI).
  - Number recorded for the real signed release on nachi's PC download (live PC (nachi)).
- Size: S. Phase: P4.
- Needs owner action? No.

## BL-417 Release manager and feed carry the desktop files
- Outcome: `Release.ps1`, the feed and `verify-release` treat the shell exe, new installer(s) and MSI(s) as one release set with one tag.
- Scope (in): add files to hash list and feed manifest; ed25519 signing order after Authenticode (BL-401/402); keep pause/rollout fields as they are; one product tag, separate wire/storage versions (DECISIONS section 11).
- Out of scope: new feed features (BL-155).
- Depends on: BL-154, BL-402; BL-155 unknown.
- Risks / unknowns: the report says the feed already has expiry, sequence, pause/blocked list and rollout percent `[SOURCE]`, but `docs/BACKLOG.md` BL-155 lists pause, rollout and expiry refresh as NOT built before stable. See conflicts.
- Acceptance:
  - Dry-run release on CI builds all files, signs, makes feed and `verify-release` passes (CI).
  - Real release by nachi with passphrase and typed confirms; a PC updates from it (elevated/UAC) and (live PC (nachi)).
- Size: M. Phase: P4.
- Needs owner action? YES: key passphrase, typed yes, publish confirm (existing owner rule).

---

## Conflicts with other areas
- Updater notify channel: report 07 says Go tells Tauri "over localhost HTTP/IPC"; DECISIONS says no loopback in desktop mode. Use the pipe (shell area P3).
- Sidecar vs split exe (`unravis.exe` vs `unravis-core.exe`) changes the OTA file-lock and setup file list (shell/sidecar area, P1 spike). BL-404 waits for it.
- BL-312 (DECISIONS) and BL-400 are the same owner action; one number must win.
- BL-155 vs report: report says pause/blocked, rollout percent and expiry already exist in the feed; BACKLOG says not built (G2, G3, G5). Report claim is not verified; treat BACKLOG as true until the repo is checked.
- Services area: report 07 assumes Keeper runs as `NT SERVICE\UnravisKeeper`; DECISIONS only says services stay as they are. Confirm the account.
- Security area: network-silence soak (0 non-loopback packets) must allow the update check only when enabled; proxy work (BL-410) changes updater network code.
- CLI area: PATH entry and WinGet "optional channel" in DECISIONS section 7 overlap BL-414.
- MCP area: an idle `--mcp` copy locks the exe during update (BL-171 left item); affects BL-404.

## Questions for spec stage
1. Owner country, and is Unravis proprietary or OSI open source? (decides BL-400 provider)
2. Is the sidecar `unravis.exe` or a split `unravis-core.exe`? (BL-404)
3. Does the current setup already support `--apply-staged`, `--wait-pid`, `--rollback`, `--recover` with those names? Report names them but says `[INFERRED]`.
4. Which service account does Keeper use today? (BL-407)
5. Does the feed have channels (`stable`/`beta`/`lts`) and which of BL-155 are built? (BL-409, BL-417)
6. Pin the signing publisher name in the OTA check, or only require a valid chain? (BL-403)
7. Revocation checking in Go and `WinVerifyTrust` on offline hosts: on or off? (BL-403, BL-410)
8. Who wins when a managed MSI and the OTA updater both want to update? (BL-409, BL-412, BL-413)
9. Do we need a per-user MSI? Tauri WiX cannot do it (issue #13792); WiX v5 can, at a cost. (BL-411)
10. Is a Server 2016 test box available? GitHub runners are Server 2022/2025. (BL-408, BL-412)
11. Is there any domain or Intune tenant to test GPO/Intune install, or do we mark it "not verified"? (BL-413)
12. Chocolatey: stays out unless a Server 2019/2022 admin asks. Confirm no item is wanted now.
13. macOS/Linux feed reuse (report note) is out of scope for Windows-first; confirm.
