# Spec 06: distribution, signing, updates (BL-400 to BL-417)

Stage 2 (spec). Inputs: groom 06, `DECISIONS-draft.md`, conflicts/questions of groom 01 to 08, research 07, and the code in `/home/claude/unravis` read on 2026-11-21. Report facts are the report's own and not re-verified. Spec 01 to 04 are already written; names from them are used as they stand.

## 0. Where the code differs from the report and the groom (read first)

| Report / groom says | Code says | Effect |
|---|---|---|
| Setup flags `--apply-staged`, `--wait-pid`, `--rollback` (groom BL-406) | Not in `cmd/unravis-setup/main.go`. Real flags: `--silent --config`, `--upgrade`, `--repair`, `--uninstall`, `--allow-downgrade`, `--close-app`, `--expect-sha256`, `--expect-version`, `--recover`, `--verify <dir>`, `--no-restart`, `--no-browser`, `--version`. Rollback is "old setup + `--allow-downgrade --silent --close-app`" (`update.LaunchRollback`). No `--scope`, `--install-dir`, `--check-update` on setup (`--check-update` is on `unravis.exe`). | No new aliases. Specs use the real names. Answers groom Q3 and area 08 Q6. |
| Update is "Go tells Tauri over localhost" | `update.Updater` (`internal/server/updater_auto.go`) already runs the idle-aware silent apply: `LaunchSilent` starts `setup --upgrade --silent --close-app`; setup asks the app to quit with `instance.Signal(Quit)`, waits 5 s, then terminates what is left (`plan.go closeApp`). | No shell-driven "flush and exit" protocol is needed. Setup stops the shell itself (D8). |
| Feed may lack pause, rollout, expiry (groom conflict 4) | Built: manifest `Expires`, `Seq`, `MinUpdateFrom`; separate signed control file with `Pause`, `Blocked`, `RolloutVersion`, `RolloutPercent` (`internal/update/control.go`, `docs/auto-updater/CONTROL.md`); `tools/release/Refresh-Feed.ps1` (expiry refresh); `mirror-feed.yml` (second host). `docs/BACKLOG.md` BL-155 text is stale for G2, G3, G4, G5. Only G7 (Blackbox share source display) not checked here. | BL-417 needs no feed change. BL-155 should be re-read by its owner. |
| Feed has channels `stable`, `beta`, `lts` (groom BL-409) | Only `stable` and `rc` (`update.ChannelStable/ChannelRC`; rc = any pre-release). | Policy `UpdateChannel` accepts `stable` and `rc` only. |
| New asset can be added to the manifest | `knownAssets` in `internal/update/manifest.go` is strict: `Validate` fails on an unknown asset key, so every installed copy would refuse the feed (the control file comment says the same). | No new manifest key for the shell. It ships inside `unravis-setup.exe` (D7). |
| Authenticode check is new (BL-403) | `WinVerifyTrustEx` already runs in `internal/blackbox/elevate_guard_windows.go` (`verifyTrust`, `WTD_REVOKE_NONE`, on an open handle). | Lift it into `internal/authenticode` (D4). |
| SignPath is a maybe (report) | Owner chose SignPath Foundation (`buildout/plan/decisions.md` A48, 2026-10-27). `release.yml` has a gate: secret `SIGNPATH` set makes the build fail on purpose; no signing step yet. `LICENSE` is Apache-2.0 (BACKLOG says "pick a licence" and `release.yml` says "no LICENSE file"; both stale). Repo `abhinavjp/unravis` serves a public feed. | D2. |
| Sign, then build is simple | `build.ps1` pins `unravis.exe` to the SHA-256 of `unravis-blackbox.exe` (`PinnedHelperSHA256`, SEC-FIX-01) and embeds all payload hashes in `unravis-setup.exe`. Signing the helper later changes its file hash and breaks the pin. | D3 (pin by image digest). |
| `tshttpproxy` for system proxy | Updater uses `http.ProxyFromEnvironment` (`fetch.go`, `download.go`). `tshttpproxy` is not in `go.mod`; it pulls Tailscale code. | D15: own small package. |
| Keeper runs as a "dedicated or virtual" account | `setup.KeeperAccount = NT SERVICE\UnravisKeeper` (virtual, `SIDUnrestricted`). Secrets are sealed with current-user DPAPI of that account (`sealedfile.CurrentUser`); machine DPAPI is only for the shared data folder. Blackbox services: `UnravisBlackbox`, `UnravisBlackboxSql` (`internal/blackbox`). | BL-407 tests the right thing. |
| Silent update works per-machine | `update.SilentNoUAC` allows a silent apply only for a per-user install with no Blackbox and no keeper. Everything else asks the person (UAC). | BL-408 per-machine runs on runners (admin); live proof needs UAC. |
| Tauri file goes next to `unravis.exe` "with minimal new code" | `internal/setup/plan.go copyFiles` has a hard-coded name list (`AppExeName`, `KeeperExeName`, `BlackboxExeName`), `runningApps` matches only `AppExeName`, `closeApp` asks only the Go owner. | BL-404 is real work (L). |
| `.github/` has CI only | `release.yml`, `ci.yml`, `verify-release.yml`, `mirror-feed.yml`. Release builds on `windows-2022`; smoke on 2022 and 2025. The owner signs the manifest (ed25519) on their PC after CI makes the draft (`tools/release/Release.ps1`, `Sign-Release.ps1`). | CI signs Authenticode only; the ed25519 step stays the owner's. |
| Boot health runs in the shell | `cmd/audit-explorer/health.go` (`startHealth`, `update.Tracker.Begin/Confirm/CleanExit`, 60 s `HealthyAfter`, 3 `MaxFailedStarts`, state in `<SessionDir>\update`, old setup in `<install>\.previous`). | D9 extends it. |
| `--test-crash-on-start` exists | Not in the repo. | New, test-only (BL-406). |

## 1. Purpose and boundaries

Owns:
- Authenticode: provider, CI signing, verify gates, runtime verify of the staged setup (`internal/authenticode`, `release.yml`, `tools/release/Sign-Files.ps1`).
- `unravis-setup.exe` and `internal/setup` changes for the shell, WebView2, policy-aware restart.
- OTA hand-off and health changes for the shell (`internal/update`, `cmd/audit-explorer/health.go`).
- Release set and its CI gates: file lists, size gate, `verify-release` extensions.
- Enterprise: policy key reader and ADMX, proxy support, MSI (two), WinGet.

Does not own:
- The shell, sidecar, pipe, tray, `--quit` of the shell (spec 01). Stop contract 4.5 of spec 01 is used as given.
- WebView2 lockdown, CSP, pipe peer rules (spec 02). The publisher check on the pipe (`verifyPublisher`, spec 03) is fed by `authenticode.Result.Signer` once BL-400 lands.
- Budget numbers other than the installer size (spec 07). Test infra beyond the release scripts (spec 08).
- Services, Lite and Blackbox behavior (unchanged). Strict-network firewall rule (spec 07 BL-437; this area only reserves the setup option name, 4.4).
- Feed schema and the ed25519 trust root (unchanged).

## 2. Resolved decisions

| # | Question / conflict | Decision | Reason |
|---|---|---|---|
| D1 | BL-312 (DECISIONS) vs BL-400 | BL-400 is the id. Every `BL-312` in other specs and in `DECISIONS-draft.md` section 8 reads as BL-400. Worker for BL-400 adds a one-line alias note in `docs/BACKLOG.md`. | One owner action, one number. |
| D2 | Provider (Q1: country, licence) | SignPath Foundation, as the owner chose (A48). Repo is public and Apache-2.0 (OSI), so the free path applies. Publisher shown to users: "SignPath Foundation". Fallback if SignPath refuses or the "no security scanner" rule bites: SSL.com IV + eSigner (about $309/yr). Azure Artifact Signing only if the owner states US/CA eligibility later. CI has one provider switch (`vars.SIGNING_PROVIDER` = `none`, `signpath`, `sslcom`, `azure`); code and checks do not depend on the provider. Owner country is not needed for the default. Default used until approval: `none`. | Free, already chosen, no country test. The switch makes a change a one-line owner action. Unsigned `rc` builds keep shipping until approval. |
| D3 | Signing order vs the helper pin | Two signing rounds, not three. The app pin changes from file SHA-256 to the **PE image digest** (SHA-256 over the file minus the checksum field, the certificate-table entry and the certificate data: what Authenticode signs). It does not change when the file is signed. Round A signs the leaves together: `unravis.exe`, `unravis-blackbox.exe`, `unravis-keeper.exe`, `unravis-lite.exe`, `unravis-desktop.exe` (and `unravis-core.exe` if BL-305 says so, and the code-mode worker from spec 04). Round B signs `unravis-setup.exe`, built after round A with the signed files in its payload. Rename `blackbox.PinnedHelperSHA256` to `blackbox.PinnedHelperImageSHA256`. | SignPath needs a person to approve each request; three rounds is too many. Keeps SEC-FIX-01. |
| D4 | WinVerifyTrust: pin the publisher name? Revocation? (Q6, Q7) | Check chain + signature + presence of a signature (`WinVerifyTrust` on the held handle), revocation OFF (`WTD_REVOKE_NONE`), no publisher-name pin. The signer subject is read and logged, and stored in the update state; a change of subject is a log line, not a block. The ed25519 manifest is the root of trust; Authenticode is the second lock. | A pin would block a provider change for every installed copy. Online revocation stalls offline hosts (report risk 6) and the current guard already uses NONE. Exception: the Microsoft WebView2 installers are pinned to a signer containing `Microsoft Corporation` (D17). |
| D5 | Dev builds and unsigned releases | `update.RequireAuthenticode` is a string set by `-ldflags -X` to `1` only when `build.ps1 -Release -RequireSigned` is used (CI sets it when `SIGNING_PROVIDER` is not `none`). Empty means off. No env var or flag can turn it off in a build that has it on. Rollback launches (`LaunchRollback`) never check it: they run a local file whose hash comes from the marker the installer wrote. | A runtime switch an attacker can set is no check. Rollback must work to a pre-signing version. |
| D6 | Sidecar `unravis.exe` or `unravis-core.exe` (Q2) | Default layout A: same `unravis.exe` (spec 01 D2, decided by spike BL-305). The setup file list is one table (`setup.ProgramFiles`, 3.3) so a split is a one-row change. | Same default as spec 01. |
| D7 | How do desktop files reach users; feed change? (BL-417, Q5) | No new manifest asset key. `unravis-desktop.exe` rides inside the setup payload (OTA path) and in the MSI/WinGet path later. The portable zip `unravis-v<ver>-windows-x64.zip` stays CLI-only (`unravis.exe`). Release assets outside the manifest (`*.msi`, offline WebView2 helper) are listed in `SHA256SUMS.txt`, attested and Authenticode-signed, but not in the signed manifest. | Old clients reject unknown asset keys. |
| D8 | Hand-off with the shell (BL-406, groom conflict 1) | Pipe only, and mostly nothing new. The Go app decides "idle and ready" as today and calls `LaunchSilent`. It sets `update.applying=true` in `GET /api/desktop/state` (spec 01 3.3) 2 s before it launches, so the shell does not respawn the sidecar or show a crash banner. Setup then stops the shell with `unravis-desktop.exe --quit` (spec 01 4.5), then the Go owner (existing). After the swap setup restarts the shell if the shell was running, else `unravis.exe --background`. | Reuses the proven path. The shell never decides on its own to exit for an update. |
| D9 | Boot health with two processes (BL-406) | Counter stays in `update.Tracker` (Go). Add `unravis.exe --update-health begin` (one-shot, no UI, exit 0 go on, 21 rollback started: stop). The shell calls it first thing in `main` (module `health.rs`, about 40 lines), before it spawns or attaches. The sidecar started with `--owner=shell` does not call `Begin` again. Confirm happens in Go after `HealthyAfter` (60 s) only if, when `--owner=shell`, a shell has polled `/api/desktop/state` in the last 30 s. A shell that never polls leaves the start unconfirmed. Three unconfirmed starts run `goBack` (existing, `LaunchRollback`) with no window. Browser-owner runs behave as today. | A crash in the shell before the sidecar exists would otherwise never be counted. One counter, one rollback path. |
| D10 | Keeper account (Q4) | `NT SERVICE\UnravisKeeper` (virtual). Setup never gives an existing keeper another account (existing comment in `plan.go`). BL-407 asserts the account and the SID after the upgrade. | Read from code. |
| D11 | Update channels (Q5) | `stable` and `rc` only. BL-409 `UpdateChannel` takes those two. A stable build ignores `rc` (existing rule). | Matches the manifest code. |
| D12 | Managed MSI vs OTA (Q8) | MSI writes `HKLM\SOFTWARE\Unravis\InstallSource` = `msi` (REG_SZ). The updater then checks and shows "an update is available; your organisation installs updates" but never downloads or applies. `unravis-setup.exe` installs write `setup`. Missing value = `setup`. Policy `AutoUpdateDisabled=1` wins over both. No override key. | One update owner per install. No race. |
| D13 | WiX: v5 standalone or Tauri WiX v3 (BL-411, Q9) | Default until the spike: WiX v5 (`wix` .NET tool, `wix build` on `.wxs` files in `installer/wix/`). Tauri's WiX v3.14.1 is EOL and per-machine only. No per-user MSI: per-user installs use `unravis-setup.exe`. Decided by spike BL-411; the default holds if the spike MSI passes install, MajorUpgrade, uninstall with no orphan service. | v3 is out of support; per-user is not wanted in MSI. |
| D14 | Server 2016 box, domain, Intune (Q10, Q11) | Not assumed. Those acceptances are written as manual checklists (`docs/desktop/checklist-server2016.md`, `docs/desktop/checklist-gpo-intune.md`) and the item report says "not verified" until the owner fills a result row with date and machine. CI covers Windows Server 2022 and 2025 runners only. | Stops waiting; keeps claims honest. |
| D15 | Proxy (BL-410) | No `tshttpproxy`. New `internal/winproxy`: reads the user's WinHTTP/IE proxy config and auto-config (PAC) through `winhttp.dll` (`WinHttpGetIEProxyConfigForCurrentUser`, `WinHttpGetProxyForUrl`), returns a `func(*http.Request) (*url.URL, error)`. Environment variables (`HTTPS_PROXY` etc.) win when set. No password prompt: a 407 gives a plain message. Used by `Fetcher.client` and `Downloader.client`. | Avoids a big dependency and its licence check; same result. |
| D16 | Offline TLS / CRL stall (BL-410) | Keep per-request limits (`fetchTimeout` 10 s, download header timeout 30 s) and add a 30 s cap on one whole check (`Fetcher.Check` context). Decided by spike BL-410 (measure on an isolated Server 2019/2022): if the stall is longer than the cap, the cap wins and the message says "no network". The Go 1.27 `SSL_CERT_FILE` note is verified in BL-410 against Go's `godebug` list before any setting is added; if the setting does not exist, nothing is added. | No hang over 30 s, whatever the cause. |
| D17 | WebView2 install (BL-405) | Setup detects the runtime in `HKLM\SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}` value `pv` and in the same path under `HKCU\Software`. Missing or older than `setup.MinWebView2` (default `110.0.0.0`, set by spike BL-306 to the oldest runtime the shell was tested with): setup runs the embedded Evergreen bootstrapper (online) or, if a file `MicrosoftEdgeWebView2RuntimeInstallerX64.exe` sits next to `unravis-setup.exe`, that standalone installer (offline), both with `/silent /install`, after `authenticode.VerifyFile` and a signer check for `Microsoft Corporation`. A failure never fails the Unravis install: no desktop shortcut is made, browser mode works, the result note says why. Setup key `webview2`: `auto` (default), `skip`. On a Windows Server SKU `auto` means `skip`. Per-machine installs run the runtime installer in the elevated worker; per-user installs run it unelevated (per-user runtime). No fixed runtime. | Microsoft patches Chromium; browser mode is the fallback. |
| D18 | Store of the offline WebView2 installer | Separate release asset only for the offline MSI variant (BL-413); never in `unravis-setup.exe`. | Size budget. |
| D19 | Chocolatey, macOS, Linux feed (Q12, Q13) | Out. No item. | Windows first. |
| D20 | WinGet and CLI PATH (conflict) | PATH belongs to spec 05. WinGet belongs here (BL-414) and installs the MSI; PATH is whatever the MSI/CLI spec sets. | One owner each. |
| D21 | First-signed-release bootstrap | Release N-1 is unsigned and has no check. Release N is the first signed one; N-1 does not check it. N+1 and later are checked by the app that launches them. Docs say this in `docs/auto-updater/RELEASING.md` (BL-403 build note). | Honest scope of the check. |
| D22 | Tauri `bundle`/`signCommand` (BL-402) | Not used. The shell is built with `cargo build --release` (spec 01 BL-317, no Tauri bundler). Signing is done on the built exe by round A. | No NSIS, no bundler, no sidecar-skip issue. |
| D23 | Who sets `InstallSource`, versions | Setup writes `HKLM`/`HKCU` `Software\Unravis\InstallSource` itself (user scope: `HKCU`). The updater reads `HKLM` first, then `HKCU`. | Per-user installs exist. |

## 3. Design

### 3.1 Components

```
build.ps1 -Release -Phase Leaves  -> dist\{unravis, unravis-blackbox, unravis-keeper, unravis-lite, unravis-desktop}.exe (unsigned)
  Round A (Sign-Files.ps1, one provider request) -> signed leaves
build.ps1 -Release -Phase Setup   -> payload from signed leaves + webview2 bootstrapper -> dist\unravis-setup.exe
  Round B (Sign-Files.ps1)        -> signed setup
package: zips, blackbox zip, update-manifest.json (sha256 of SIGNED files), attest, notes
owner: Sign-Release.ps1 signs manifest with ed25519 (unchanged)
client: Fetcher -> Downloader -> hash+ed25519 -> held handle -> authenticode.VerifyHandle -> setup --upgrade --silent --close-app
```

Go (new or changed):
- `internal/authenticode/`: `digest.go` (`ImageDigest(path string) ([32]byte, error)`, pure Go with `debug/pe`, tested on Linux), `verify.go` (types `Status`, `Result`), `verify_windows.go` (`VerifyHandle(path string, h windows.Handle) Result`, `VerifyFile(path string) Result`), `verify_other.go` (returns `Result{Status: Unsupported}`), `signer_windows.go` (best-effort subject name, empty on failure).
- `internal/blackbox/elevate_guard_windows.go`: calls `authenticode.VerifyHandle` and `ImageDigest`; `elevate_guard.go`: `PinnedHelperImageSHA256`. Dev rule (empty pin: accept unsigned or valid, refuse invalid) is kept.
- `internal/update/launch_windows.go`: `launchSetup` calls the check (3.3). New `update.RequireAuthenticode string`, test hook `update.verifyAuth func(path string, h uintptr) authenticode.Result`.
- `internal/update/policy.go` (+ `policy_windows.go`, `policy_other.go`): `Policy`, `ReadPolicy(reg RegReader) Policy`.
- `internal/update/source.go`: `InstallSource(reg RegReader) string`.
- `internal/winproxy/`: `winproxy.go` (`ProxyFunc(getenv func(string) string) func(*http.Request) (*url.URL, error)`), `winproxy_windows.go`, `winproxy_other.go`. Strips userinfo from anything it logs.
- `internal/setup/`: `layout.go` (`DesktopExeName = "unravis-desktop.exe"`, `Layout.DesktopExe`, `ProgramFiles` table), `plan.go` (copy, place, running apps, close order, restart choice), `webview2.go` (`DetectWebView2(reg) (string, bool)`, `InstallWebView2(ctx, ...)`), `options.go` (new fields 4.4), `uninstall.go`, `journal.go` (no change expected; files go through `Txn.PlaceFile`).
- `cmd/audit-explorer/health.go`: `--update-health begin` subcommand, shell-aware `Confirm`. `cmd/audit-explorer/updatehealth.go` (new file for the flag).
- `internal/server/pipe.go` (spec 01): field `update.applying` set from `Updater` (`internal/server/updater_auto.go`).
- `cmd/unravis-setup/main.go`: flag `--require-signed` (setup checks its own file with `authenticode.VerifyFile` before it does anything).

Rust (spec 01 owns the crate): `desktop/src-tauri/src/health.rs` (begin call, exit on 21), and shell `--quit` already in spec 01. Nothing else in Rust belongs to this area.

Scripts and CI:
- `tools/release/Sign-Files.ps1` (provider switch), `tools/release/Assert-Signed.ps1` (verify gate), `tools/release/Check-Size.ps1` (BL-416), `tools/release/Test-Signing.ps1` (self-test with a throw-away cert), `tools/release/Invoke-VerifyRelease.ps1` (extended), `tools/release/Test-KeeperContinuity.ps1` (BL-407).
- `.github/workflows/release.yml` (job split, 3.4), `.github/workflows/verify-release.yml` (matrix), `.signpath/artifact-leaves.xml`, `.signpath/artifact-setup.xml`.
- `installer/wix/` (BL-411 to BL-413), `installer/winget/` manifests (BL-414), `installer/admx/Unravis.admx` + `en-US/Unravis.adml` (BL-409).
- `docs/adr/0006-wix-v5.md` (BL-411), `docs/desktop/checklist-*.md` (D14), `docs/desktop/smartscreen-result.md` (BL-415).

### 3.2 Names and keys

- Signing provider switch: repo variable `SIGNING_PROVIDER` (`none|signpath|sslcom|azure`). SignPath: secret `SIGNPATH_API_TOKEN`, variables `SIGNPATH_ORG_ID`, `SIGNPATH_PROJECT_SLUG`, `SIGNPATH_POLICY_SLUG`; action pinned to a full commit SHA (look up with `git ls-remote` as the workflow header says). The old `SIGNPATH` secret and its stop-the-build step are removed.
- GitHub environment `release-signing` (required reviewer = owner) holds the signing secrets. Only the sign jobs use it.
- Timestamp URLs: SignPath adds its own; `sslcom` `http://ts.ssl.com`; `azure` `http://timestamp.acs.microsoft.com`; fallback `http://timestamp.digicert.com`. Every signature must carry a timestamp (checked in `Assert-Signed.ps1`).
- Registry (policy, read only): `HKLM\SOFTWARE\Policies\Unravis\Updater`
  - `AutoUpdateDisabled` DWORD 0/1 (1: no feed request at all, no download).
  - `UpdateMode` DWORD: 0 = off, 1 = check and tell, 2 = download and install when idle (default when absent: the user's own setting).
  - `UpdateChannel` REG_SZ `stable|rc`.
  - `PinnedVersion` REG_SZ plain version; the updater offers exactly that version or nothing.
  - `ServiceAutoUpdateDisabled` DWORD 0/1 (Blackbox self-update, `internal/blackbox/selfupdate.go`).
  Unknown names and wrong types are ignored and logged once. Values out of range count as absent.
- Registry (install source): `Software\Unravis\InstallSource` REG_SZ `setup|msi` under `HKLM` (machine) or `HKCU` (user).
- Setup config (`setup.json`, `internal/setup/options.go`) new keys: `useDesktop` (bool, default false until P5: main shortcuts point at `unravis-desktop.exe`), `webview2` (`auto|skip`, default `auto`), `strictNetwork` (bool, reserved for spec 07, default false).
- Setup extras: Start menu entry `Unravis Desktop.lnk` -> `unravis-desktop.exe` is always made when the file is laid down; `Unravis.lnk` and the Startup link keep their targets until `useDesktop` is true.
- Exit codes (new): `unravis.exe --update-health begin`: 0 go on, 21 rollback started. Setup: unchanged (`exitInvalid` 2).
- Release asset names: `unravis-setup-v<ver>-windows-x64.exe` (existing), `Unravis-Blackbox-Server-<ver>-x64.msi`, `Unravis-Desktop-<ver>-x64.msi`, `Unravis-Desktop-Offline-<ver>-x64.msi` (BL-413), existing zips unchanged.

### 3.3 Data and message shapes

`authenticode.Result`:
```go
type Status int // Signed, Unsigned, Invalid, Unsupported
type Result struct {
    Status Status
    Code   uint32 // WinVerifyTrust code (0 = ok)
    Signer string // subject CN, "" if unknown
}
```
Mapping: code 0 = `Signed`; `0x800B0100`, `0x800B0001`, `0x800B0003` = `Unsigned` (same list as `elevate_guard.go`); anything else = `Invalid`.

`launchSetup` order (existing steps in bold): validate args, **open held**, **hash equals manifest hash**, then if `RequireAuthenticode == "1"`: `authenticode.VerifyHandle(path, h)`; only `Signed` goes on. Error text shown in Help > Updates and the log: "The update file is not signed. It was not started." (`Unsigned`) or "The update file's signature is not valid. It was not started." (`Invalid`). The state records `lastRefusal {version, reason, at}`; the version is not retried until the next feed `Seq`. `--require-signed` on the setup repeats the check on its own file.

`GET /api/desktop/state` (spec 01) gains, owned by this area: `"update": {"ready":false,"version":"","applying":false,"managed":false,"managedReason":""}`. `managed` is true when policy or `InstallSource=msi` stops OTA; `managedReason` is `policy` or `msi`.

Update state file gains `lastSigner` (string) and `lastRefusal` (object or null). No other schema change.

Health state (`<SessionDir>\update\health.json`, existing) gains `shellPollAt` (RFC 3339, in memory only; not written). `Confirm` rule: `uptime >= 60 s && (owner != shell || now - shellPollAt <= 30 s)`.

Release set (what `package` makes):
```
unravis-v<ver>-windows-x64.zip           unravis.exe (signed)             manifest key "app"
unravis-blackbox-v<ver>-windows-x64.zip  unravis-blackbox.exe (signed)    manifest key "blackbox"
unravis-lite-v<ver>-windows-x64.zip      unravis-lite.exe (signed)        manifest key "lite"
unravis-setup-v<ver>-windows-x64.exe     signed, embeds desktop + keeper  manifest key "setup"
update-manifest.json (+ .sig by owner), SHA256SUMS.txt, ASSET-LIST.txt, RELEASE-NOTES.md
later: *.msi (not in manifest)
```
`setup.ProgramFiles` (new table in `layout.go`):
```go
var ProgramFiles = []ProgramFile{ // name, scopes, required
  {AppExeName, user|machine, true}, {BlackboxExeName, both, true},
  {KeeperExeName, machine, false}, {DesktopExeName, both, false},
}
```
`false` means "place only if the payload has it", so a headless payload and old payloads still install. `copyFiles`, `runningApps`, `uninstall` and the payload check read this table.

Install source key and policy: read through the existing `setup.RegReader`-style interface so tests use the fake system (`internal/setup/fake.go`). `update.RegReader` is a two-method interface `DWORD(root, path, name) (uint32, bool)`, `String(root, path, name) (string, bool)`.

Policy precedence (`update.Effective(policy, source, userSettings)`): `AutoUpdateDisabled=1` or `UpdateMode=0` -> nothing (no request); `InstallSource=msi` -> check-only; `PinnedVersion` -> only that version; else user settings. Before any network call `Fetcher.Check` calls `Effective`; a "nothing" result returns `Result{Status: StatusManaged}` (new status) with no request made.

### 3.4 Sequences

Release build (`release.yml`, jobs; sign jobs skipped when `SIGNING_PROVIDER=none`):
1. `build-leaves` (windows-2022): web build, `go vet`, tests, `build.ps1 -SkipWeb -SkipTests -Release -Phase Leaves -Version <v> [-RequireSigned]` (builds helper, computes its image digest, builds the app pinned to it, keeper, lite; the desktop shell is built by `tools/desktop/Build-Desktop.ps1` and copied to `dist\`). Upload `leaves-unsigned`.
2. `sign-leaves` (needs approval at the provider): `Sign-Files.ps1 -Provider $env -Files leaves -OutDir leaves-signed`; then `Assert-Signed.ps1`. Upload `leaves-signed`.
3. `build-setup`: download `leaves-signed` into `dist\`; `build.ps1 -Release -Phase Setup -RequireSigned` (checks each payload file: Authenticode `Signed` and `ImageDigest` of the helper equals the pin; copies the WebView2 bootstrapper from `tools/release/webview2/` after checking its SHA-256 against `tools/release/webview2/bootstrapper.sha256` and its Microsoft signature); builds `unravis-setup.exe`. Upload `setup-unsigned`.
4. `sign-setup`: same as 2 for the one file.
5. `package`: `build-blackbox.ps1 -Reuse`, zips, manifest from the signed files, `Assert-Signed.ps1` over every exe in the release set, `Check-Size.ps1`, attest, notes, upload. Then the existing `smoke` and `draft`.
6. A stable tag (no `-`) with `SIGNING_PROVIDER=none` fails in `package`. An `rc` tag may ship unsigned; the notes say so (`SIGNED` env of the notes step becomes `${{ needs.package.outputs.signed }}`).

OTA update on a PC with the shell (per-user, window open, idle):
1. `Updater` check passes ed25519 + hash; download; policy `Effective` allows install; busy hooks say idle (page presence over 2 min old, or window destroyed).
2. Go sets `update.applying=true`, waits 2 s, calls `LaunchSilent(path, sha, ver)`: held handle, hash, Authenticode check, start `setup --upgrade --expect-sha256 --expect-version --require-signed --silent --close-app`.
3. Setup `closeApp`: runs `<install>\unravis-desktop.exe --quit` if a shell is running (wait up to 8 s), then the existing `AskQuit` to the Go owner (5 s), then `TerminateIf` for what is left. Notes `shellWasRunning`.
4. Setup places files (journal as today), writes `.previous`, restarts: `unravis-desktop.exe --background` if `shellWasRunning`, else `unravis.exe --background`. Leaves the result note.
5. New shell: `unravis.exe --update-health begin` (count 1), spawns sidecar, polls state every 5 s; Go `Confirm` at 60 s with a recent shell poll.

Bad start: the shell crashes or never polls, three starts in a row each under 60 s: third `--update-health begin` returns 21 after `goBack` (existing: `Skip(version)`, `MarkRecovered`, `LaunchRollback`); the shell exits with no window; setup rolls back and restarts the old version. A note says "Unravis went back to <from>".

Rollback to a version without the shell: the old setup leaves `unravis-desktop.exe` in place (it does not know the file). The shell checks `GET /api/version` against its own version at attach (spec 01) and refuses on mismatch; `Unravis Desktop.lnk` then gives a message "This Unravis window does not match the program. Run Setup > Repair." Setup `--repair` of the old version cannot remove it; `--uninstall` of the new journal would. Accepted, see risk R4.

Managed install (MSI): `InstallSource=msi` -> `Effective` = check-only; Help > Updates shows "Updates are installed by your organisation." No download.

### 3.5 Failure modes and what the user sees

| Failure | User sees |
|---|---|
| Staged setup unsigned or bad signature while `RequireAuthenticode=1` | Help > Updates: "The update file is not signed / its signature is not valid. It was not started." Log has code and signer. App keeps running the old version. |
| Provider down or no approval in CI | Release job fails. No draft is made. The owner sees the failed `sign-*` job. Never an unsigned stable release. |
| Timestamp server blocked | `Sign-Files.ps1` exit non-zero, `signtool` exit 2 treated as failure. Job fails. |
| WebView2 install fails | Result note: "Unravis was installed. The desktop window needs Microsoft WebView2, which could not be installed (reason). Unravis opens in your browser as before." No desktop shortcut. |
| Shell file locked at upgrade | Setup `closeApp` reaches `TerminateIf` after the wait; if still locked: existing step error "Close any program using the Unravis folder"; journal restores old files. |
| Power cut mid-swap | `--recover` (existing, run by the app 30 s after start from the program folder; now also by the shell via the sidecar). Old or new version, never half. |
| Policy says off | Help > Updates: "Updates are managed by your organisation." No request is sent. |
| Proxy asks for a password (407) | "The update check could not pass your proxy (it needs a sign-in). Ask your IT team to allow github.com." |
| Isolated PC, no network | Check fails within 30 s: "Could not reach the update feed." |
| Two update owners | Cannot happen: `msi` source stops OTA. |

## 4. Interfaces other areas rely on (exact names)

4.1 Go: `authenticode.VerifyHandle`, `authenticode.VerifyFile`, `authenticode.ImageDigest`, `authenticode.Result{Status, Code, Signer}`. Spec 03 `verifyPublisher` calls `authenticode.VerifyFile(path).Signer` and compares only when `Status == Signed`; before BL-400 it gets `Unsigned` and must keep its path-only rule.
4.2 Go: `update.RequireAuthenticode`, `update.ReadPolicy`, `update.InstallSource`, `update.Effective`, `winproxy.ProxyFunc`.
4.3 Pin rename: `blackbox.PinnedHelperImageSHA256` (ldflags `-X api-audit-explorer/internal/blackbox.PinnedHelperImageSHA256=<hex>`). Any worker that touches `build.ps1`, `build-blackbox.ps1`, `release.yml` pin checks or tests of the old name must use the new one.
4.4 Setup options: `useDesktop`, `webview2`, `strictNetwork` (spec 07 BL-437 adds the step; this area only adds the field and passes it through `setup.Options`, `internal/setup/options.go`).
4.5 Build entry points: `build.ps1 -Phase Leaves|Setup|All` (default `All`, today's behavior, dev builds), `-RequireSigned`. Spec 01 `Build-Desktop.ps1` writes `dist\unravis-desktop.exe`; `build.ps1 -Phase Leaves` calls it when `desktop\src-tauri` exists.
4.6 Shell contract needed from spec 01: `unravis-desktop.exe --quit` (exists in spec 01 3.3), `--background`, calls `unravis.exe --update-health begin` first in `main`, polls `/api/desktop/state` every 5 s, treats `update.applying=true` as "do not respawn the sidecar".
4.7 Files setup lays down and signs (for specs 02, 04, 08): `unravis.exe`, `unravis-blackbox.exe`, `unravis-keeper.exe` (machine), `unravis-desktop.exe`, `unravis-lite.exe` (zip only), `unravis-setup.exe`, `unravis-core.exe` only if BL-305 says so, and spec 04's worker `unravis-sandbox.exe` (add one row to `ProgramFiles` and one to `.signpath/artifact-leaves.xml`; spec 04 BL-368 does this through the table).
4.8 `GET /api/desktop/state` field `update` as in 3.3. `POST /api/desktop/...` routes belong to spec 01.
4.9 CI ownership: this area edits `release.yml` and `verify-release.yml`; spec 08 reuses `tools/release/Invoke-VerifyRelease.ps1` pieces through functions, not copies. PRs to those two files go one at a time (BL-153 first, then BL-401, BL-408).
4.10 `docs/BACKLOG.md` rows for BL-400 to BL-417 are added by each build worker. BL-418 and BL-419 stay free.

## 5. Build notes per BL item

Order inside the area (arrows are hard depends; items not linked can run in parallel):
`BL-400` (owner, start now) ; `W1` = `internal/authenticode` + guard refactor (part of BL-401 and BL-403, no owner action) -> `BL-417` file lists -> `BL-404` (per-user, then per-machine) -> `BL-401` -> `BL-402` -> `BL-403` wiring -> `BL-405` -> `BL-406` -> `BL-407` and `BL-408` -> `BL-416`. In parallel from the start: `BL-409`, `BL-410`, `BL-411`. After `BL-411`: `BL-412`, `BL-413` -> `BL-414`. `BL-415` after the first signed release.

### W1 Shared package (belongs to BL-401 and BL-403)
- Add `internal/authenticode/{digest.go,verify.go,verify_windows.go,verify_other.go,signer_windows.go}`. Move `verifyTrust` logic out of `internal/blackbox/elevate_guard_windows.go`; the guard keeps its public behavior.
- Tests: `TestImageDigestIgnoresCertTable` (Linux: a PE fixture built in the test with and without a fake certificate table: same digest; a one-byte change in `.text`: different), `TestImageDigestRejectsNotPE`, `TestResultMapping` (code to status), `TestVerifyHandleUnsignedExe` (Windows: `go test` binary is unsigned -> `Unsigned`), `TestVerifyHandleSelfSigned` (Windows CI: `New-SelfSignedCertificate -Type CodeSigningCert`, add to `CurrentUser\Root` and `TrustedPublisher`, `Set-AuthenticodeSignature` a copy of a test exe -> `Signed`; edit one byte -> `Invalid`), `TestImageDigestStableAcrossSigning` (Windows CI: digest before equals after signing).
- Blackbox: `TestHelperPinImageDigest` (pin = digest of the unsigned file accepts the signed copy; refuses a changed file).

### BL-400 Signing identity (owner action, S)
- Worker steps (no owner wait): add `docs/auto-updater/SIGNING.md` with the SignPath Foundation application text (project name, repo URL, licence Apache-2.0, what the program does: reads logs read-only, no scanner, no network scanning), the two artifact configs, the GitHub environment and secret names (3.2), and the fallback chain. Add the `SIGNING_PROVIDER` switch (BL-401). Add the alias note for BL-312.
- Owner actions (only these): apply to SignPath Foundation and answer their checks; create the project and policies (`release-signing`, manual approval); put `SIGNPATH_API_TOKEN` in environment `release-signing`; set variables; set `SIGNING_PROVIDER=signpath`. If SignPath refuses: pay SSL.com (~$309/yr), set `SIGNING_PROVIDER=sslcom` and its secrets (`SSLCOM_USER`, `SSLCOM_PASSWORD`, `SSLCOM_TOTP_SECRET`, `SSLCOM_CREDENTIAL_ID`), no code change.
- Acceptance (runnable):
  - Workflow `sign-test.yml` (manual dispatch, new) signs a throw-away exe built in the run; `signtool verify /pa /v <file>` exit 0 and output has a timestamp line. (CI)
  - `Get-AuthenticodeSignature .\unravis-setup.exe | Format-List Status,SignerCertificate` on nachi's PC shows `Valid` and publisher "SignPath Foundation". (live PC (nachi))
- Needs owner: yes (application, approvals, secrets). Everything else here is built and tested with `none` and a self-signed cert.

### BL-401 CI signs the Go binaries (M)
- Files: `build.ps1` (phases, `-RequireSigned`, helper image digest and pin, payload check), `build-blackbox.ps1` (`-Reuse` packs the signed exe; pin check uses digest), `.github/workflows/release.yml` (3.4; remove the old `SIGNPATH` stop step and the "pinned" PowerShell step is rewritten to compare digests), `tools/release/Sign-Files.ps1`, `tools/release/Assert-Signed.ps1`, `.signpath/artifact-leaves.xml`, `tools/release/Test-Signing.ps1`.
- `Sign-Files.ps1 -Provider -Files -OutDir`: one request per call; refuses a file list that is not exactly the expected names; never signs a file whose Authenticode status is already `Signed` by a third party (third-party DLLs are not in the list); exit non-zero on any provider error.
- `Assert-Signed.ps1 -Dir`: for each `.exe`/`.msi` runs `signtool verify /pa /v` (fail on any non-zero exit, including exit 2) and also `Get-AuthenticodeSignature` to require a `TimeStamperCertificate`.
- Tests: `Test-Signing.ps1` signs copies with a throw-away cert and asserts: pass on signed; fail on unsigned; fail when the timestamp URL is a blocked host (`http://127.0.0.1:9`); fail when one byte is changed after signing. Run in `ci.yml` job `release-scripts` (windows-2022).
- Acceptance:
  - rc tag with `SIGNING_PROVIDER=signpath`: draft contains signed files; `Assert-Signed.ps1 -Dir dist` exit 0. (CI)
  - Same with the timestamp host blocked in a test run (`Test-Signing.ps1 -BlockTimestamp`): exit non-zero. (CI)
  - After round B, `go run ./tools/release/sigtool manifest` hashes equal `Get-FileHash` of the released files, and `unravis-setup.exe --verify <dir>` passes. (CI)
  - Order check: `Test-Release.ps1` asserts the manifest step runs after the last sign job (reads `release.yml` job `needs`). (CI)
- Size M. Owner: no after BL-400.

### BL-402 Sign shell and installers last (M)
- Files: `release.yml`, `build.ps1`, `.signpath/artifact-setup.xml`, `tools/release/Assert-Signed.ps1` (set check covers every exe in `ASSET-LIST.txt` plus the files inside the setup payload: `Assert-Payload.ps1` extracts nothing; setup `--verify-payload` is not added. Instead `build.ps1 -Phase Setup` checks each payload file before embedding, and `Assert-Signed.ps1` checks the same files in `dist\`).
- No Tauri bundler, `signCommand` or `--no-bundle` (D22).
- Tests: `Test-Signing.ps1 -Order` (a payload file changed after round A makes `-Phase Setup` fail); `Assert-Signed` covers `unravis-desktop.exe`.
- Acceptance:
  - `Assert-Signed.ps1 -Dir dist` passes on the rc release set (every first-party exe). (CI)
  - Unpack the installed tree on a clean PC: `Get-ChildItem -Recurse -Filter *.exe | Get-AuthenticodeSignature` shows `Valid` for each first-party exe and the WebView2 helper shows Microsoft. (live PC (nachi))
- Size M. Owner: no.

### BL-403 OTA verifies Authenticode on the staged installer (M)
- Files: `internal/update/launch_windows.go` (check after the hash, before `exec`), `internal/update/launch_other.go` (no check), `cmd/unravis-setup/main.go` (`--require-signed`, checked first in `run`), `internal/update/state` (fields `lastSigner`, `lastRefusal`, in `fetch.go` state struct), `internal/server/updater.go` (show refusal text), `docs/auto-updater/RELEASING.md` (D21).
- Tests (`internal/update/launch_windows_test.go`): `TestLaunchSetupRefusesUnsigned`, `TestLaunchSetupRefusesTampered` (signed then edited; hash passes? no: hash fails first; second case edits and re-hashes the manifest value to isolate the signature check -> `Invalid`), `TestLaunchSetupAcceptsSigned` (self-signed trusted cert, setup stub), `TestLaunchSetupSkipsCheckWhenOff`, `TestLaunchRollbackNeverChecks`, `TestRequireFlagIsLdflagsOnly` (reads that no env var or flag toggles it). Wrong publisher: not a case (D4); `TestSignerLoggedNotPinned` asserts a different signer subject launches and logs.
- Acceptance:
  - `go test ./internal/update ./internal/authenticode ./cmd/unravis-setup` on windows-2022 passes the cases above, including "unsigned setup with a valid ed25519 signature and correct hash is refused". (CI)
  - Real signed N-1 to N upgrade on the real feed: log lines `update: sha256 ok` and `update: authenticode ok signer=SignPath Foundation`. (live PC (nachi); first possible at N+1 per D21)
- Size M.

### BL-404 Setup lays down the shell and stops it (L; do per-user first, then per-machine)
- Files: `internal/setup/layout.go` (names, `ProgramFiles`, `Layout.DesktopExe`, `DesktopLink`), `plan.go` (`copyFiles` from table; `place`; `runningApps` matches both exes; `closeApp` stops shell first; restart picks the shell), `options.go`, `uninstall.go`, `state.go` (record `shellInstalled`), `profilelinks*.go` only if they list exes (check `grep AppExeName`), `build.ps1` (payload list), `cmd/unravis-setup/payload/README.txt`.
- Slice 1 per-user: place file, `Unravis Desktop.lnk`, stop order, restart choice, uninstall removes. Slice 2 per-machine: same under Program Files, Start menu all-users, no keeper change.
- Locked file: setup already terminates what is left; an idle `unravis --mcp` copy is still closed by `AskQuit` (BL-171 note unchanged). Add `closeApp` log of each image that blocked.
- Tests (`internal/setup`, fake system): `TestPlanPlacesDesktopExe`, `TestPlanSkipsDesktopWhenPayloadLacksIt`, `TestCloseAppStopsShellBeforeGo`, `TestRestartUsesShellWhenItWasRunning`, `TestUninstallRemovesDesktopExeAndLink`, `TestRecoverRestoresDesktopExe` (journal), `TestLayoutDesktopPerScope`.
- Acceptance:
  - `go test ./internal/setup` passes the cases above. (CI)
  - On nachi's PC, clean user: `unravis-setup.exe --silent --config setup.json` then `Test-Path "$env:LOCALAPPDATA\Programs\Unravis\unravis-desktop.exe"`; `--uninstall` leaves nothing under that folder and no `HKCU` Unravis keys other than data. (live PC (nachi); exact install folder is read from `Layout`, not assumed)
  - Upgrade with the shell window open and `unravis --mcp` running: no locked-file step error; afterwards one `unravis-desktop.exe` and one sidecar (`Get-Process unravis*`). (live PC (nachi))
  - Per-machine with services, same checks. (elevated/UAC)
  - Kill setup mid-swap (`Stop-Process` at the `place` step with the test hook `UNRAVIS_SETUP_PAUSE_AT=place`, test builds only), then run `unravis-setup.exe --recover`: either old or new tree, `Get-FileHash` of all files matches one set. (live PC (nachi))
- Size L.

### BL-405 WebView2 detection and install (M)
- Files: `internal/setup/webview2.go`, `plan.go` (new step `webview2` after `copy-files`, before shortcuts; soft-fail), `options.go` (`webview2`), `fake.go` (registry fakes), `tools/release/webview2/{bootstrapper.sha256,README.md}` (the bootstrapper file itself is downloaded by `build.ps1 -Phase Setup` from the Microsoft fixed link into the payload and hash-checked; it is not committed), `cmd/unravis-setup/payload`.
- `InstallWebView2`: copy the helper to a private temp dir (per-user ACL), `authenticode.VerifyFile` must be `Signed` with `Signer` containing `Microsoft Corporation`, run `/silent /install` with a 5 min limit, re-run `DetectWebView2`.
- Tests: `TestDetectWebView2` (HKLM, HKCU, missing, below min), `TestInstallWebView2RefusesWrongSigner`, `TestInstallFailureIsSoft` (install continues, no desktop link, note text), `TestServerSkuSkips`, `TestSkipOption`.
- Acceptance:
  - Unit tests above. (CI)
  - Runtime present: setup log has `webview2: found <ver>`, no download. (live PC (nachi))
  - Clean Win10 or Server 2019+ VM without runtime: after setup, `reg query` shows the runtime and `unravis-desktop.exe` opens a window. (live PC (nachi); VM from the owner)
  - Offline VM, standalone file beside setup, network off: same. (live PC (nachi))
  - Per-machine install of the runtime. (elevated/UAC)
- Size M.

### BL-406 Desktop-aware hand-off and boot health (L; two slices)
- Slice A (hand-off): `internal/server/updater_auto.go` (`applying` flag 2 s before `LaunchSilent`), `internal/server/pipe.go` (state field), `internal/update/launch_windows.go` (pass `--require-signed`), setup restart choice (BL-404). Busy rule: unchanged; a visible window sends the existing presence ping through the relay, so "window open and used" counts as in use (2 min `presenceWindow`), a hidden or destroyed window does not. No second idle clock.
- Slice B (health): `cmd/audit-explorer/updatehealth.go` (`--update-health begin`), `cmd/audit-explorer/health.go` (skip `Begin` when `--owner=shell`; `Confirm` rule in 3.3; shell poll time set by the `/api/desktop/state` handler through `update.Tracker.NoteShellPoll(time)`), `desktop/src-tauri/src/health.rs` (spec 01 worker adds the call; this item supplies the contract and test), test-only flag `--test-crash-on-start` in both exes, compiled only with Go tag `e2e` and Rust feature `e2e`; release build scan `tools/release/Assert-NoTestFlags.ps1` fails if the string `test-crash-on-start` is in a release exe.
- Tests: `TestApplyingFlagSetBeforeLaunch`, `TestConfirmNeedsShellPoll`, `TestConfirmBrowserOwnerUnchanged`, `TestBeginSkippedForShellOwner`, `TestUpdateHealthBeginExit21AfterThree`, `TestGoBackUsesLaunchRollback` (existing, extend), `TestNoTestFlagsInRelease`.
- Acceptance:
  - Unit tests above. (CI)
  - Localhost feed with an ephemeral key (`tools/release/Invoke-Smoke.ps1`, new `-CrashShell` switch) installs a build with `--test-crash-on-start` as N; after 3 starts the old version runs and `unravis.exe --version` prints N-1; no window seen. (CI on windows-2022)
  - Real signed N-1 to N with the shell window open and idle: window closes, version changes, shell comes back, result note present. (live PC (nachi))
  - Same rollback on a real release candidate built with the e2e flag (never published). (live PC (nachi))
- Size L.

### BL-407 DPAPI continuity of Key Keeper (M)
- Files: `tools/release/Test-KeeperContinuity.ps1`, `Invoke-VerifyRelease.ps1` (step `B6`), `.github/workflows/verify-release.yml`.
- Script: install N-1 per-machine, store a test secret through the keeper client (`internal/keeper/client.go`; test helper command `unravis-keeper.exe --selftest-seal <name>` added in test builds only, tag `e2e`; real builds use the real UI path on the live run), upgrade to N with `unravis-setup.exe --silent --upgrade --expect-sha256 ... --expect-version ...`, read it back; assert `(Get-CimInstance Win32_Service -Filter "Name='UnravisKeeper'").StartName` is `NT SERVICE\UnravisKeeper` and the service SID (`sc.exe showsid UnravisKeeper`) is unchanged.
- MSI variant (after BL-413): same script with `msiexec /i` MajorUpgrade.
- Acceptance:
  - windows-2022 runner (admin): secret read OK after upgrade; SID same. (CI)
  - nachi's PC with the real keeper and a real stored password: `unravis` still tests that saved connection after upgrade. (elevated/UAC)
  - MSI variant. (CI, after BL-413)
- Size M. Risk R2.

### BL-408 verify-release covers desktop and server (L; per-user, per-machine, server)
- Files: `Invoke-VerifyRelease.ps1` (new steps: `C1` shell present and starts under a standard user via scheduled task, `C2` per-machine, `C3` Authenticode of downloaded files via `Assert-Signed.ps1`, `C4` refuse unsigned (a setup re-signed with the test cert is refused when `RequireAuthenticode=1` build; localhost only), `C5` refuse downgrade (existing B4 kept), `C6` 3-bad-start rollback with an e2e build (skipped on the real public feed: runs in `ci.yml` job `ota-localhost`), `C7` Lite and Blackbox zips unchanged), `verify-release.yml` (matrix `scope: [user, machine]`), `docs/desktop/checklist-server2016.md`.
- Acceptance:
  - Workflow `verify-release` for the latest published pair passes both scopes. (CI)
  - Bad signature, unsigned and downgrade cases are refused (steps C3, C4, C5 report PASS). (CI)
  - Server 2016 headless Blackbox upgrade: owner follows the checklist and fills the result row; until then the report line says "Server 2016: not verified". (live PC (nachi) and elevated/UAC)
- Size L.

### BL-409 Enterprise update policy (M)
- Files: `internal/update/policy.go`, `policy_windows.go` (`golang.org/x/sys/windows/registry`, already a dependency of `x/sys`), `policy_other.go`, `internal/update/source.go`, `fetch.go` (`Check` calls `Effective`, new `StatusManaged`), `internal/server/updater.go` and `web/src/...` Help > Updates text (the UI worker adds the line from `update.managed`), `internal/blackbox/selfupdate.go` (`ServiceAutoUpdateDisabled`), `installer/admx/Unravis.admx`, `installer/admx/en-US/Unravis.adml`.
- Tests: `TestPolicyEachValue`, `TestPolicyBadTypeIgnored`, `TestPolicyOutOfRangeIgnored`, `TestEffectivePrecedence` (table over `AutoUpdateDisabled`, `UpdateMode`, `PinnedVersion`, `InstallSource`), `TestCheckMakesNoRequestWhenDisabled` (fake transport counts calls: 0), `TestPinnedVersionOffersOnlyThat`, `TestChannelStableBuildIgnoresRC`, `TestAdmxWellFormed` (XML parses; every policy name in ADMX appears in `policy.go`).
- Acceptance:
  - Unit tests. (CI)
  - As admin: `New-Item HKLM:\SOFTWARE\Policies\Unravis\Updater -Force; Set-ItemProperty ... AutoUpdateDisabled 1 -Type DWord`; start Unravis, `unravis.exe --check-update` prints a managed result; `pktmon` (or the app log `update: no request, policy`) shows zero feed requests; Help > Updates says managed. (elevated/UAC)
  - Copy ADMX/ADML to `C:\Windows\PolicyDefinitions`, open `gpedit.msc`: policies listed under Unravis without error. (live PC (nachi); needs Pro edition)
- Size M.

### BL-410 Proxy and offline behavior (M)
- Files: `internal/winproxy/*`, `internal/update/fetch.go` and `download.go` (`tr.Proxy = winproxy.ProxyFunc(os.Getenv)`), `Fetcher.Check` 30 s cap, `docs/auto-updater/PROXY.md`.
- Tests: `TestProxyEnvWins`, `TestProxyFromWindowsStatic` (fake reader: `http=host:port;https=host2:port`), `TestProxyBypassList`, `TestProxyPACFake` (fake PAC resolver), `TestNoUserinfoInLog`, `TestCheckCapsAt30s` (blocked dial), `TestProxy407Message`.
- Spike inside the item (decided by spike BL-410): on an isolated Server 2019/2022 VM, time `unravis.exe --check-update`; record in `docs/auto-updater/PROXY.md`. Default until measured: the 30 s cap.
- Acceptance:
  - Unit tests. (CI)
  - Behind a real proxy: `unravis.exe --check-update` returns an update result (exit 0 or 2 as today). (live PC (nachi) only if a proxy network exists; else the report says "proxy: not verified")
  - Isolated VM: `Measure-Command { .\unravis.exe --check-update }` under 30 s and a plain message. (live PC (nachi))
- Size M.

### BL-411 Spike: WiX v5 vs Tauri WiX v3 (S)
- Files: `installer/wix/spike/` (throw-away), `docs/adr/0006-wix-v5.md`.
- Build the same minimal MSI both ways with `ServiceInstall` + `ServiceControl` for `UnravisBlackbox`, `UnravisBlackboxSql` (`NT SERVICE\UnravisBlackboxSql`), `UnravisKeeper` (`NT SERVICE\UnravisKeeper`); note effort, VBSCRIPT need, upgrade behavior.
- Acceptance: on a VM `msiexec /i spike.msi /qn`, version bump `msiexec /i spike2.msi /qn` (MajorUpgrade), `msiexec /x` leave no service (`sc.exe query` for each name fails with 1060). (elevated/UAC) ADR has a line `Decision:`; CI check `tools/release/Test-DocsHave.ps1 docs/adr/0006-wix-v5.md "Decision:"`. (CI)
- Size S.

### BL-412 Headless Blackbox Server MSI (L; install/uninstall, then upgrade + DPAPI)
- Files: `installer/wix/server/*.wxs`, `build.ps1 -Phase Msi` (new, runs `wix build` on signed files), `release.yml` job `msi` (after round A; MSI signed as round C only when the owner wants it in the same approval: default is to sign MSIs in round B with the setup, one request), `tools/release/Test-Msi.ps1`.
- Content: `unravis-blackbox.exe`, `unravis-keeper.exe`, `unravis-lite.exe`, services with the existing accounts and `SIDUnrestricted`, `InstallSource=msi`, `ALLUSERS=1`, ProductCode detection. No shell, no WebView2, no custom-action scripts.
- Acceptance:
  - `msiexec /i Unravis-Blackbox-Server-<ver>-x64.msi /qn /norestart /l*v msi.log` exit 0; services run under the right accounts (`Get-CimInstance Win32_Service`). (elevated/UAC; CI runner covers 2022/2025)
  - Upgrade keeps keeper secret (BL-407). (elevated/UAC)
  - Real Server 2016: checklist, else "not verified"; real Blackbox recording still collects. (live PC (nachi))
- Size L. Owner: server box optional (D14).

### BL-413 Desktop MSI (L)
- Files: `installer/wix/desktop/*.wxs` (features: Core = `unravis.exe`, `unravis-desktop.exe`; Services optional), offline variant adds the standalone WebView2 installer as a chained custom-free prerequisite (a plain `.exe` run by the bundle is out of scope: the offline variant ships the standalone installer file next to the MSI and the MSI launches nothing; its README says run it first; reason: no custom actions), `installer/admx/*`, `docs/enterprise/INTUNE.md` (one page: wrap as `.intunewin`, detection by ProductCode, a raw LOB MSI allows one argument).
- Acceptance:
  - Silent install and uninstall on a clean runner; `unravis-desktop.exe` present then gone; no Unravis file left under Program Files. (CI + elevated/UAC)
  - GPO or Intune install: owner's domain only; else the report says "not verified" (D14). (live PC (nachi))
  - Offline VM with the standalone installer first, then the MSI: the shell starts. (live PC (nachi))
- Size L.

### BL-414 WinGet (M)
- Files: `installer/winget/Unravis.Unravis.{yaml,installer.yaml,locale.en-US.yaml}`, `release.yml` job `winget` (runs only on stable tags, needs the published release, uses `wingetcreate update --submit` with secret `WINGET_PAT`), `docs/enterprise/WINGET.md`.
- Manifest: `InstallerType: wix`, `Scope: machine`, `ElevationRequirement: elevationRequired`, `InstallerUrl` = the signed MSI, `InstallerSha256`, schema pinned in `installer/winget/SCHEMA.txt` (check yearly).
- Acceptance:
  - `winget validate --manifest installer\winget` exit 0 and `wingetcreate` local validate. (CI)
  - `winget install --manifest installer\winget` on a clean PC installs and `unravis.exe --version` runs. (live PC (nachi))
  - After the external merge: `winget install Unravis.Unravis`. (live PC (nachi); only then ticked; waits on Microsoft's queue)
- Needs owner: PAT secret and the go to publish publicly.

### BL-415 Fresh-PC reputation check (S)
- File: `docs/desktop/smartscreen-result.md` (template made by the worker with a row per case: date, Windows build, SAC state, download URL, SmartScreen text, Defender result, per file).
- Acceptance: the owner downloads the released `unravis-setup-v<ver>-windows-x64.exe` from the real release page on a clean Win11 PC and one with Smart App Control on; screenshots go in `docs/desktop/smartscreen/`; the doc lists "More info > Run anyway" steps if prompted. Evidence only. (live PC (nachi))

### BL-416 CI size gate (S)
- Files: `tools/release/Check-Size.ps1`, `release.yml` step in `package`, `ci.yml` self-test.
- Counts: `unravis-setup.exe` (which embeds the payload and the bootstrapper) as the online download; offline MSI reported apart. Target <= 25 MB, fail > 35 MB (`-Target 25MB -Fail 35MB` defaults, spec 07 may change the numbers).
- Tests: `Test-Release.ps1` adds `Check-Size passes on 1 MB file`, `fails on a 40 MB file`.
- Acceptance: CI fails on the inflated test file and passes on the real rc set; the real number is written to the job summary. (CI) nachi's PC download size read from `(Get-Item .\unravis-setup-*.exe).Length`. (live PC (nachi))

### BL-417 Release manager and feed carry the desktop files (M)
- Files: `tools/release/Release.ps1` and `ReleaseLib.ps1` (asset list check: setup, three zips; expect `unravis-desktop.exe` inside the setup payload through `unravis-setup.exe --verify`), `New-Sha256Sums.ps1` (extra names), `release.yml` `ASSET-LIST`, `tools/release/Invoke-Smoke.ps1` (assert desktop exe installed when payload has it).
- No change to `update-manifest.json` schema, control file or key handling (D7). Signing order: Authenticode (CI) then manifest hashes (CI) then ed25519 (owner).
- Tests: `Test-ReleaseManager.ps1` cases `desktop file expected in payload`, `manifest hashes the signed setup`.
- Acceptance:
  - Dry run: `.\tools\release\Release.ps1 -Channel rc -CheckOnly` lists all files and passes; CI draft run passes `smoke` on windows-2022 and 2025. (CI)
  - Real rc release by nachi (passphrase, typed yes, publish confirm as today); a PC updates from it. (elevated/UAC) and (live PC (nachi))

## 6. Security and footprint checks (this area)

- Trust order stays: ed25519 manifest -> file hash -> Authenticode (D4). Authenticode never replaces the first two.
- Check on the held handle (same bytes hashed, verified, run). Test `TestLaunchSetupHandleHeldDuringVerify` tries to open the file for write while checking and expects a sharing error.
- `RequireAuthenticode` only by `-ldflags`; `TestRequireFlagIsLdflagsOnly`. A CI scan `Assert-NoTestFlags.ps1` fails a release exe containing `test-crash-on-start`, `UNRAVIS_SETUP_PAUSE_AT`, `selftest-seal`.
- Signing secrets only in environment `release-signing` with the owner as reviewer. Sign jobs have `permissions: contents: read`, no other secrets, and no step runs repo scripts that were changed in the same run without the `release-signing` approval (the approval is the gate). Signing action pinned to a commit SHA. No signing key in the repo; the ed25519 keys stay on the owner's PC.
- Sign only an exact list of first-party files (`Sign-Files.ps1` refuses extras). Third-party files are never re-signed (3.1).
- Image-digest pin: `TestHelperPinImageDigest` and the CI pin check run on the final `dist\` files, not on pre-sign copies.
- Policy key is read only. Types and ranges are checked. `HKLM\...\Policies` is writable by administrators only (Windows default); the code does not rely on it for secrets, only for turning updates off or pinning a version, and a pin never offers a version the signed feed does not carry.
- WebView2 helpers: Microsoft signer check, private temp dir, 5 min limit, no network call of ours (the bootstrapper's own download is Microsoft's).
- Proxy: no credentials stored or sent; logs strip userinfo; PAC is resolved by WinHTTP, never by running a downloaded script in our process.
- MSI: no custom actions or scripts, `ALLUSERS=1`, files signed. `InstallSource=msi` stops OTA, so OTA cannot overwrite MSI-owned files.
- Network silence (spec 02 soak): the only new outbound traffic is the update feed check already present and, at install time only, Microsoft's bootstrapper. The soak test must run with updates off or allow the feed host.
- Footprint: setup grows by the shell exe (size unknown until spec 01 BL-306 builds it) plus 1.8 MB bootstrapper; BL-416 holds the line (25 MB target, 35 MB fail). Offline WebView2 never in `unravis-setup.exe`. Policy reader and proxy package add no dependency beyond `x/sys`. No new runtime process at idle from this area.

## 7. Open risks left and mitigations

| # | Risk | Mitigation (no owner wait) |
|---|---|---|
| R1 | SignPath may refuse the project (scanner rule, activity) or be slow. | Build and test everything with `none` and a self-signed cert; one switch to SSL.com; rc keeps shipping unsigned meanwhile; stable is gated on signing. |
| R2 | DPAPI/keeper continuity across MajorUpgrade never prototyped; the virtual account profile may differ after service remove and add. | BL-407 runs on every `verify-release`; the MSI uses `ServiceControl` stop/start, not remove and add, in the same component (spike BL-411 confirms); MSI stays blocked behind BL-407 green. |
| R3 | Image-digest code has a bug and weakens the helper pin. | Pure Go, Linux tests with fixtures, Windows signing test, and the guard falls back to refusing on any parse error. |
| R4 | Rollback to a version without the shell leaves a stale `unravis-desktop.exe`. | Shell refuses on version mismatch; message tells to run Repair; uninstall of any version removes by journal or by `ProgramFiles` table. Accepted until the second signed release. |
| R5 | Three approvals per release (round A, round B, manifest by owner) slow releases. | Two rounds only (D3). Approvals can be batched: both sign jobs start in one workflow run; the owner approves in one sitting. |
| R6 | SmartScreen still warns on early signed releases (reputation, SignPath shared publisher). | BL-415 records it; release notes carry the "More info > Run anyway" steps (existing template). Evidence, not a gate. |
| R7 | `winproxy` PAC handling differs by Windows build. | Falls back to direct and to env proxy; unit tests with fakes; live proxy test is marked "not verified" if no proxy network exists. |
| R8 | WebView2 bootstrapper blocked by strict proxy or AV. | Soft failure with a clear note; standalone installer path; browser mode keeps working. |
| R9 | Two files edit `release.yml` and `verify-release.yml` at once (this area, spec 08). | PR order in 4.9; scripts in `tools/release/` are functions reused by both. |
| R10 | GitHub runners cannot test Server 2016, Windows 10, domain or Intune. | Checklists and "not verified" wording (D14). Nothing in this area is ticked on those paths without a dated result row. |
| R11 | WinGet merge time and schema changes. | Item stays open until merge; schema in one text file checked yearly. |
