# Auto-updater for Unravis and Blackbox: spec

Status: DRAFT v3 for nachi's confirmation. No code written. 2026-11-20.
v3 adds the live research (section 16, fetched vs memory) and my own answers to every open decision (section 13). v2 added nachi's answers (D1 30 min check, D2 zero cost, D4 ok, D6 Blackbox auto-update on other servers, D7 public repo) and a new section 12, Unravis Lite.
Rule: nothing below is built until nachi confirms (see "Open decisions", section 13).

## 1. Goal

Unravis and the Blackbox recorder can find a newer release, download it safely, install it, and roll back if it fails. Cases, saved connections and recordings are never lost.

Order of goals: **security first**, then footprint (CPU, memory, network ~ zero), then convenience.

## 2. What exists today (read from the repo)

| Piece | State |
|---|---|
| Release pipeline (`release.yml`) | Tag `vX.Y.Z[-rc.N]` builds a DRAFT release: setup exe, app zip, Blackbox zip, `SHA256SUMS.txt`. A person presses Publish. Never auto-publishes. |
| Signing | None yet (SignPath planned, A48). Releases are unsigned. |
| Update check | **Before U3:** manual button only, GitHub `releases/latest`, ignored pre-releases (decision A52, now replaced by D1). **Since U3:** background check (default 30 min) plus the button, both through the signed feed. Link only, no download until U4. |
| Installer `unravis-setup.exe` | Already does upgrade in place: asks to close the app, stops services, replaces program files, keeps data and sealed secrets, refuses downgrade (`--allow-downgrade`), journal + rollback, keeps old files until the new version starts once. UI runs unelevated; a short-lived elevated worker does admin steps. |
| Blackbox pin (SEC-FIX-01) | The app embeds the SHA-256 of the exact Blackbox exe it ships with, and refuses any other. |
| BL-136 | Designed and built, **not run live**: app-driven Blackbox `update` op (one admin approval, hash check, `.old` rollback). |
| Keeper service | SYSTEM helper holding the sealing key. |

So most of the install, upgrade and rollback engine exists. The gap is: **finding, downloading and trusting** a release.

## 3. How others do it (marks: [F] fetched 2026-11-20, [M] from memory, not re-checked; details in section 16)

| Tool | Idea | Take |
|---|---|---|
| Squirrel.Windows [M] | Per-user install, background check, swap folders, delta packages | Per-user swap is simple, but cannot touch services/admin. No built-in signature check beyond Authenticode. |
| Velopack [F] | Per-channel index files, delta updates. Its signing path is Azure Artifact Signing at about USD 10/month (not free); docs state no downgrade rule | Channel idea is good. Cost rules it out. Deltas not worth it for us. |
| WinSparkle / Sparkle [F] | Feed (appcast) signed with **EdDSA (ed25519)**; public key inside the app | **Copy this.** Trust comes from a key in the app, not from the download host. |
| Chrome / Edge / Dropbox [M] | Separate SYSTEM "elevation/update service" installs updates without UAC | Powerful, bigger attack surface. Later option only (section 8). |
| WinSW and service wrappers [M] | Do **not** self-update. Update = stop service, replace exe, start | Service exe can't replace itself while running. Needs an outside actor. Our actor = installer / app op (BL-136). |
| go-selfupdate / minio selfupdate [M] | Download, verify checksum, rename over the running exe | Fine for plain apps. Not for a service under Program Files. |
| TUF (The Update Framework) [F] | Signed metadata with expiry, version, threshold keys, key rotation | Take the ideas: expiry, monotonic version, two keys. Skip the full framework. |
| winget / Chocolatey [M] | Hash in a manifest, optional publisher signature | Hash from same host as file is weak. |

Key lesson: **a checksum next to the download proves nothing** if the attacker controls the release (stolen GitHub account, bad token). Trust must come from a key held outside GitHub.

## 4. Recommended approach (one line)

**Signed feed + signed manifest + reuse the installer.** A tiny signed file on GitHub says "latest version is X". Unravis (and the Blackbox service, on its own) check it every 30 min, download the matching package, verify it with a public key built into the program, and install it with the existing upgrade/rollback code. No new install engine. Zero cost.

## 5. Release side (CI + one manual step)

### Signed assets
Each release gets `update-manifest.json` + `update-manifest.json.sig` (ed25519, detached).

```json
{
  "schema": 1,
  "product": "unravis",
  "version": "0.1.0-rc.48",
  "channel": "rc",
  "published": "2026-11-20T10:00:00Z",
  "expires": "2027-02-20T10:00:00Z",
  "seq": 48,
  "minUpdateFrom": "0.1.0-rc.30",
  "assets": {
    "setup":    { "name": "unravis-setup-v0.1.0-rc.48-windows-x64.exe", "sha256": "...", "size": 0 },
    "app":      { "name": "unravis-v...-windows-x64.zip", "sha256": "...", "size": 0 },
    "blackbox": { "name": "unravis-blackbox-v...-windows-x64.zip", "sha256": "...", "size": 0 },
    "lite":     { "name": "unravis-lite-v...-windows-x64.zip", "sha256": "...", "size": 0 }
  },
  "blackboxExeSha256": "..."
}
```

- `seq` only goes up. `expires` stops replay of an old manifest.
- Assets are named exactly; the program builds the URL itself (`https://github.com/abhinavjp/unravis/releases/download/<tag>/<name>`). **No URL is ever taken from unsigned data.**

### The feed (why 30-minute checks stay cheap)
A permanent release tagged `update-feed` holds two small files, replaced on each release: `stable.json` and `rc.json`, each a signed copy of the newest manifest of that channel (+ `.sig`), about 1 KB.

- A check is one `GET https://github.com/abhinavjp/unravis/releases/download/update-feed/<channel>.json` with `If-None-Match`. A `304` is a few hundred bytes.
- This is **not the GitHub API**. GitHub's API allows only 60 unauthenticated requests/hour per IP [F] and its page does not say a `304` is free, so I avoid the API. Whether the release-download host has a limit is **not verified**; the client backs off on any 403/429 (U2 live-checks this).
- The feed is only a pointer. A forged or stale feed fails the signature, `seq` or `expires` test.
- Pre-releases are handled by the `rc.json` file, which fixes today's gap (the manual check ignores rc builds).

### Signing key: zero cost (D2)
- ed25519 key pair made once, **offline, on nachi's PC** with a free tool (a small Go program in `tools/release/`, no purchase).
- Private key is stored encrypted with a strong passphrase (free: Go `x/crypto` / `age`-style file). Never in CI, never in the repo. Backups: two copies on USB sticks you already own, kept apart. Passphrase not stored with them.
- Release flow: CI builds the DRAFT release and uploads the unsigned manifest. nachi runs `tools/release/Sign-Release.ps1` locally (asks the passphrase), which signs the manifest and updates the feed. nachi presses Publish. Matches today's "a person presses Publish".
- The signing script (`Sign-Release.ps1`) builds the tool from the tag in a clean copy, shows the version, dates and file names, re-hashes every file (including the Blackbox exe inside its zip), checks the new version/seq moves the feed forward, and asks for a typed "yes". The `-Feed` step refuses a draft release. It also compares the local tag with GitHub's, requires the manifest's `commit` to equal the tag's commit, shows changes to the signing code and the trusted key list since the last signed release (you must type to accept), and checks the signature against the keys installed copies already trust.
- **Known gap (U1b):** nothing yet proves the draft's files were built by our workflow from that commit; a stolen token could swap files and a matching manifest and rely on you to sign. The fix is free build attestations (`actions/attest-build-provenance` + `gh attestation verify`), to be added and tested on a real rc run.
- Public keys (current + next) are compiled into `unravis.exe`, `unravis-setup.exe` and `unravis-blackbox.exe`. Two keys allow rotation.
- **SignPath Foundation** [F] gives free signing to open-source projects, but: needs an OSI-approved licence (Apache-2.0 added 2026-11-20, see D14), the certificate is issued to "SignPath Foundation", every release needs a manual approval, MFA for the team, a privacy/data-transfer note, and acceptance is at their discretion. It adds Authenticode as a second lock; the updater does not depend on it.
- **Separate keys per channel** (from the Doyensec and TUF lessons [F]): an `rc` key and a `stable` key. A stable build trusts only the stable key. An rc build trusts both. A leaked rc key can never push to stable users.
- Not recommended as the only trust: GitHub artifact attestations (free for public repos). Useful for people checking by hand, heavy to verify inside the program.

## 6. Client side: check (Unravis app)

- **Where:** `internal/update` (new), used by the existing server.
- **Default: ON, every 30 minutes** (D1; overturns A52). The user can change the interval or turn it off in Settings. Minimum allowed 5 min. After the stable version is launched we raise the default (see D9). Mode: `Off`, `Notify`, `Notify + download` (default: Notify).
- **Cadence:** first check 60 s after start, then every interval plus random jitter of up to 10%. Only while the app runs. No service, no scheduled task. Cost: one sleeping timer, one ~1 KB request per 30 min (about 50 KB/day worst case, less with `304`).
- **Channels:** `stable` or `rc`. Default = channel of the running build (an rc build stays on rc). The channel inside the signed manifest must equal the one asked for.
- **Proxy:** Windows system proxy. On error: "Could not check", retry next cycle, back off x2 up to 6 h, never loop.
- **No telemetry:** no user data, User-Agent `Unravis/<version>` only, no machine id.
- Dev builds (`version = dev`) never call out.
- The existing "Check for updates" button stays; it calls the same code.

## 7. Client side: verify, download, install (Unravis)

Verify order. Any failure = stop, delete the file, show a plain message, log it. **Never fall back to a weaker check.**

1. HTTPS only (TLS 1.2+, system trust). No HTTP, no redirect to another scheme. Redirects allowed only to `github.com` and `*.githubusercontent.com`, max 3.
2. Verify `update-manifest.json.sig` against the embedded public keys (ed25519).
3. Check manifest: `product`, `channel` matches, `expires` in the future, `seq` > last seen `seq` (stored), `version` > installed (semver; `rc.N` numeric) and the tag in the URL matches `version`. **No silent downgrade.** Equal or lower = ignored. `minUpdateFrom` honoured.
4. Download the setup exe: size capped to the manifest `size` (+0), written to a per-user folder under the local cache (`%LOCALAPPDATA%\\Unravis\\updates`, default ACL: this user, SYSTEM and administrators; not Temp), hashed while streaming; SHA-256 must equal the manifest.
5. Authenticode (once signing exists): signer must match the pinned publisher.
6. Show "Update to X ready. Needs administrator approval if Blackbox is installed. Close Unravis and update?" The user clicks. **Never installs without a click** (open decision D3 for a "install on next quit" option).
7. Launch `unravis-setup.exe --upgrade --expect-sha256 <hash> --expect-version <v>`. The installer's elevated worker **re-hashes its own image** before doing any admin step (closes the swap-after-verify gap). The installer re-verifies the embedded Blackbox pin as today.
8. The installer runs its normal upgrade: stop, replace program files, keep data, restart Unravis. A user-scope install without Blackbox needs no UAC.

Release-notes text from GitHub is shown as plain text only (escaped). It is never a link target or HTML.

Lessons added from research [F]:
- Sparkle CVE-2025-0509: a valid signature could be reused to install a different payload. So the signature must bind **exact bytes + version + size + file name** in one signed manifest, and the installer is told `--expect-sha256` and `--expect-version` (already above). A signed file for version A can never be installed as version B.
- TUF: never accept anything older than previously seen (`seq`, stored); declared size caps the download (endless data); all assets sit in one signed manifest (mix-and-match).
- Doyensec: TOCTOU cannot be fully removed by path; so we also hold the file open without write sharing while it is hashed and started, and the worker re-hashes.

## 8. Blackbox auto-update (D6: also on other servers)

Facts that drive the design:
- Blackbox has **no network client today** and runs as a Windows service, often on a server with no Unravis and no keeper (the keeper belongs to the Unravis install, so it cannot be the updater there).
- A headless server has nobody to click a UAC prompt. So the "one UAC prompt" answer (D4) fits only a Blackbox next to Unravis on a PC. For a server, auto-update must be done by the service itself.

### Three cases

| Case | How it updates |
|---|---|
| A. Blackbox on the same PC as Unravis | With Unravis: the installer upgrades both (step 7). Or BL-136 `update` op alone. One UAC prompt (D4). |
| B. Blackbox alone on a server, internet allowed | **The service updates itself.** Section 8.1. |
| C. Blackbox alone on a server, no internet | Same code, but the update source is a folder or share (section 8.2). |

### 8.1 Self-update by the service (recommended, minimal footprint)

- No extra process, no scheduled task, no extra service. The Blackbox service gets one small goroutine: every 30 min (+ jitter) it fetches `<channel>.json` + `.sig` (~1 KB, `If-None-Match`). It sleeps otherwise. When nothing changed: no CPU worth measuring, no memory growth, ~1 KB/30 min.
- Network code is the minimum: one HTTPS GET to the one allowed host, system proxy, 10 s timeout, body capped at 64 KB (manifest) / manifest size (package). No listener added; the status port stays as is.
- On a newer signed version (same checks as section 7, steps 1 to 5): download the Blackbox zip into its own locked folder under the Blackbox data folder, hash while streaming, verify the signature and the `blackboxExeSha256`.
- **Apply** (the service cannot overwrite its own running exe): it starts a short-lived copy of the NEW, verified exe as `unravis-blackbox.exe apply-update --stage <dir>`, running as the same account. That helper: waits for the service to stop (asks the service manager to stop it), renames old exe to `.old`, moves the new exe in, starts the service, waits for "running" + a status answer, and on any failure restores `.old` and restarts it. This is the same logic as BL-136, so the two share one code path and one live test.
- Config, read key, recordings, the folder lock and the firewall rule are never touched. A gap of a few seconds in recording happens at restart (state this in the UI and docs; schedule option below).
- **Controls** in `blackbox.json` (and the CLI `unravis-blackbox config`): `autoUpdate`: `install` (default per D1), `notify` or `off`; `channel`; `intervalMinutes`; `maintenanceWindow` (optional, e.g. only 02:00 to 04:00); `source` (internet, or folder/share, section 8.2).
- Where Unravis sees the server, the Blackbox status screen shows "Recorder version, update state, last check, last result" (read from `/status`, new fields).

### 8.2 No internet: folder or share source
- `source` can be a UNC or local folder holding `<channel>.json`, `.sig` and the zip. The check is a file read every 30 min. Same signature checks.
- Someone (or Unravis on a connected PC: "Copy update to share") drops the files there. A fleet of air-gapped servers updates from one share. Nothing unsigned is ever accepted.

### 8.3 Security of a SYSTEM service that installs code (the honest risk)
This is the Chrome-style model, and it is the highest-risk part of this spec. Mitigations, all required:
1. Signature with an embedded key (not the download host) decides everything.
2. Version and `seq` must go up; `expires`; `channel` match; `minUpdateFrom`.
3. HTTPS only, one host allowlist (`github.com`, `*.githubusercontent.com`), max 3 redirects, size caps.
4. Staging folder: ACL for SYSTEM/Administrators only, so a normal user cannot swap the file.
5. Only the Blackbox exe is replaced, never anything else; the exe is the one hashed in the signed manifest.
6. Rollback on any failure (section 9). Skip list for a version that failed.
7. A `off` switch on the box, which a remote request cannot change.
8. Every check and apply is written to the Blackbox log (no secrets).

Residual risk: whoever holds the signing key can push code to every server. This is why the key is offline and passphrase-protected (D2) and why rotation exists.

## 9. Rollback

- Before replacing: installer keeps the old program files in a backup folder (already in the plan) and writes a `pending` marker with old version, new version and time.
- After install the new app (or the Blackbox service) must start and report "ready" within 60 s (service: "running" + status answer). The app then clears the marker and deletes the backup after a grace period (next successful start).
- If the marker is still `pending` when the app starts next (crash loop, 2 failed starts) the installer's journal restores the backup, restarts the old version, and records "update X failed; staying on Y". That version is added to a local skip list so it is not offered again until a newer one appears.
- Data is never touched by rollback. Data schema: an update must not migrate cases in a way the old version can't read, or must take a copy first. (Check against case store before build.)
- Rollback is local only. A remote or manifest request can never lower the version.

**As built (U5):** the upgrade's last step (`keep-previous`) copies the setup program it just replaced into `<program folder>\.previous\unravis-setup.exe` with `pending.json` (from, to, SHA-256 of that setup, time). That old setup carries the old program files, so going back is the same installer run with `--allow-downgrade` (no second restore code). The new app counts starts in its per-user update folder (`health.json`): it is confirmed after 60 s up; closing it on purpose early is not a failure; an error exit or a crash is. After 3 unconfirmed starts, the 4th start puts the version on `skip.json` and opens the kept setup (hash and version from the marker go through the U4 `--expect-*` checks), then exits. The setup window still needs the person's Install click (and UAC for a machine install). A downgrade or a repair clears `.previous`; uninstall removes it. Not built: a message in the Help panel that says "update X failed; staying on Y".

## 10. Offline and air-gapped servers

- With no internet the check fails quietly ("Could not check"); nothing else breaks. For servers, use the folder/share source (section 8.2).
- **Import update** button (and `unravis-setup.exe --verify <folder>`): pick a folder with `unravis-setup-*.exe`, `update-manifest.json`, `.sig`. The same verify steps 2, 3, 4 run offline. Carry files on a USB stick after checking on a connected PC.
- Docs page: where to download, how to check with `Get-FileHash` and the in-app verify.
- Sealed corporate proxy that breaks TLS: check fails with "could not check". No option to skip verification. Ever.

## 11. Threats and answers

| Threat | Answer |
|---|---|
| Release tampered (stolen GitHub account/token) | Manifest signature from an off-GitHub key. Attacker can't forge. |
| MITM / bad Wi-Fi | HTTPS + signature + hash. |
| Downgrade to an old buggy/vulnerable build | Signed `version` + `seq` must exceed installed; installer also refuses. |
| Replay of an old valid manifest | `seq` and `expires`. |
| Fake "update" pointing to another host | URL built by app from the signed asset name; host allowlist. |
| Swap file between verify and run | Locked-ACL folder, hash re-checked in the elevated worker. |
| Huge download / disk fill | Size cap from signed manifest, free-space check. |
| Malicious release notes | Plain text only. |
| Signing key lost | Two embedded keys: current + next. Documented rotation. If both are lost: manual reinstall. |
| Signing key stolen | Offline key, optional passphrase/YubiKey. Revoke by shipping a build with a new key set (needs a manual install for PCs that never updated: state this honestly). |
| Update server (GitHub) down or rate-limited | Skip, retry later. Unauthenticated API: 60 requests/hour per IP, we use ~1 a day. |
| Update crashes the app/service | Section 9. |
| Service updates itself: attacker gets SYSTEM code run | Section 8.3: only a signed, newer, hash-matched exe is installed; staging folder locked. |
| Many servers hit GitHub at once | Jitter; feed is a static file, no API limit; `304`. |

Honest limits: a local admin can already do anything on the PC. Unsigned binaries still trigger SmartScreen on a manual first install; the in-app path avoids that because the app is already trusted.

## 12. Unravis Lite (view what a Blackbox captured, on a server with no Unravis)

### What it is
A small program for a server that has a Blackbox but not Unravis. It opens a local page and shows what the Blackbox recorded: the Blackbox charts (Perfmon-style, log scale), the What happened view, the list of recordings, and status. Read only. No cases, no SQL/Mongo connections, no Drill/Slow call, no import, no MCP.

### Options
| Option | Pros | Cons |
|---|---|---|
| A. `unravis.exe --lite` (same file, features hidden) | One download, one updater | Whole big program and attack surface sits on the server |
| **B. Separate `unravis-lite.exe`** (same Go packages, own small web build) | Smaller, fewer features means less to attack, own zip asset, simple to update | One more asset to build and test |
| C. Page served by the Blackbox itself | No new exe | Puts a web UI inside a SYSTEM service. Bigger risk. Reject. |

**Recommended: B.**

### Design
- Reads the recordings **directly from the local Blackbox folder** (same reader code as the app), so no read key and no network are needed on the same machine. Access is by Windows file permission: it runs as the signed-in user and shows only what that user may read. The Blackbox data folder is locked today, so Lite asks for a one-time admin approval to read it, or runs elevated when started from "Run as administrator" (decision D10). It never loosens the folder lock.
- Local web page on `127.0.0.1` only, with a one-time token in the URL (same idea as Unravis' launch link). No outside port. Always prints the link; `--open` also opens the browser (off by default because Lite is often run as administrator, and a browser started from it would be elevated too).
- Starts on demand (double click, or a Start menu / desktop link). Not a service. Nothing runs when closed: zero idle cost.
- Reuses the existing Blackbox web screens. **To confirm in the build slice:** whether the Blackbox charts and What-happened screens can be built into a separate bundle. If they are tangled with the case screens, Lite needs a small route split first. I have not measured the size; target under 15 MB to be measured (the frontend bundle is the main cost).
- Extras that cost little: choose a day, time range, group/search counters, export a chart as an image. Nothing that writes.
- A remote view (Lite on laptop reading a server's Blackbox over port 7788 with a read key) is the main Unravis app's job, not Lite's.

### Install and update
- Shipped as a signed-manifest asset `unravis-lite-vX-windows-x64.zip` (see manifest `lite`). Unzip and run. No installer needed, no admin needed to run (only for the data folder, above).
- Updates: Lite checks the same feed on start (once per run, not every 30 min, since it is not a service). If newer it shows a banner and offers to download, verify and replace itself in its own folder (no admin, as it lives in a user-writable folder; the verify steps are the same). If installed under Program Files, it says "ask an administrator" or uses the Blackbox update.
- Option: the Blackbox self-update (section 8) can also refresh `unravis-lite.exe` if it sits next to the Blackbox exe, from the same signed manifest. Recommended as it keeps both in step (D11).

### Security
- Loopback only + one-time token + Host and Origin checks (same as the app).
- Read only. No file upload, no write to the Blackbox folder, no secrets handled (no read key, no passwords).
- Same signature rules for its own update.
- A different user on the same server cannot use another's link (token is per run, process-owned).

## 13. Decisions (nachi delegated them to me on 2026-11-20; ELI10 reasons)

| # | Decision | Why, in simple words |
|---|---|---|
| D1 | Check every 30 min, user can change, raise to 6 h after stable | nachi's call. A check is a 1 KB note, like glancing at a noticeboard. Cheap. |
| D2 | Key made on nachi's PC, locked with a passphrase, 2 USB backups. Zero cost | A key in the cloud can be stolen with the cloud. A key in a drawer cannot. Free. |
| D3 | Install only after the user clicks (Unravis app) | A program that changes itself while you work is rude and risky. You press the button. |
| D4 | Next to Unravis, Blackbox updates with one Windows admin prompt | Windows asks "are you sure?" for big changes. That is good; a person is there to answer. |
| D5 | A build stays on its channel (rc stays rc) until the user changes it | Do not move people between test and real versions behind their back. |
| D6 | Blackbox on a server updates itself, default `install` | It is a tape recorder, not the shop that serves customers. If an update fails, the worst case is a few seconds of missing tape, and it undoes itself. Safety: only a signed, newer, matching file is accepted. Setting `notify` or `off` is one line in the config. |
| D7 | Public repo | Done. Allows free signing. |
| D8 | Ship before SignPath is approved | Our own key does the safety job. SignPath is a bonus badge. SignPath needs a licence file; now added (D14). |
| D9 | 30 min for rc, 6 h for stable | Testing builds change often, real ones rarely. |
| D10 | Lite is started "as administrator" by the person who wants to look | The recordings are in a locked room. The person with the key opens the door. Lite never makes the lock weaker and never keeps admin rights. |
| D11 | Blackbox update also refreshes `unravis-lite.exe` beside it | One signed update keeps both in step. |
| D12 | No maintenance window by default; setting available | Most boxes are fine; picky ones can set a quiet hour. |
| D13 | Separate signing keys for rc and stable | A thief of the test key cannot touch real users. |
| D14 | **Apache-2.0** (decided 2026-11-20, nachi delegated; `LICENSE` and `NOTICE` added) | SignPath needs an OSI-approved licence. Apache-2.0 is OSI-approved, lets anyone use the tool, and says nobody can sue you over a patent in it. All our Go libraries are BSD, MIT or Apache-2.0, so they fit. MIT is shorter but has no patent promise. GPL/AGPL would force others to open their changes and some firms refuse to touch them. Follow-up: ship a third-party notices file with the installer (BSD and MIT ask for it). |

## 14. Build plan (after you confirm)

| Slice | Content | Live check on |
|---|---|---|
| U1 | Key tool + `Sign-Release.ps1`, manifest + feed generator in `release.yml`, public keys embedded, key-rotation doc | Real rc release signed and verified |
| U2 | `internal/update`: verify manifest, semver/seq/expiry, host allowlist, unit tests incl. bad sig, downgrade, replay, expired, wrong host, truncated | Real GitHub feed |
| U3 | Unravis check: ETag, 30 min cadence, jitter, channel, Settings (interval, off), proxy | rc.N to rc.N+1 on your PC |
| U4 | Download + hand-off to `unravis-setup --upgrade --expect-sha256` (built 2026-11-20; file held open with no write/rename/delete share while hashed and started; the setup holds its own file the same way while it runs) | Same, cases and saved connections kept |
| U5 | Rollback marker + health check + skip list | Forced failure on your PC |
| U6 | Blackbox self-update: check goroutine, `apply-update` helper (shares BL-136 code), config keys, status fields, folder/share source | Elevated host, real service, forced failure |
| U7 | Unravis Lite: separate build, local reader, loopback page, own update | Server with Blackbox and no Unravis |
| U8 | Offline import + docs page | USB-stick run, no network |

Each slice follows the factory way: spec, build, review, verify, then reinstall on your PC. Verified-on-live vs tests-only is reported per slice. Owed live items stay unticked in `BACKLOG.md`.

## 15. Footprint summary

- Unravis: one timer, ~1 KB per 30 min, a few KB of memory for the verifier. Download only after a new release.
- Blackbox service: one goroutine asleep between checks, ~1 KB per 30 min, no new listener, no new process unless applying an update. Recording is not slowed.
- Lite: nothing runs when closed.
## 16. Research log (what was fetched and what is from memory)

Fetched live 2026-11-20 (free pages, via web fetch):
- GitHub REST rate limits: 60/hour unauthenticated per IP; a `304` is not stated as free. (docs.github.com, rate limits for the REST API)
- Sparkle security page: use HTTPS feeds (an HTTP feed caused remote code execution), use EdDSA not DSA, verify before extracting, keep keys off the command line. (sparkle-project.github.io)
- CVE-2025-0509 (Sparkle before 2.6.4): signature check bypassed by swapping the payload. (tenable.com)
- TUF attack list: rollback, fast-forward, freeze, endless data, mix-and-match, malicious mirrors, key compromise; fixes: expiry, freshness, key rotation, size caps. (theupdateframework.io/security)
- SignPath Foundation terms: OSI licence, manual approval per release, certificate issued to the Foundation, MFA, discretionary acceptance. (signpath.org/terms, signpath.io open-source page)
- Velopack signing and channels: Azure Artifact Signing about USD 10/month; per-channel index files; no downgrade rule stated. (docs.velopack.io)
- Doyensec safe updater: sign version + hash, owner-only temp folder, downgrades off, separate keys per channel, TOCTOU is not fully removable. (blog.doyensec.com, 2026-02-16; a reference design, not production)

From memory, not re-checked: Squirrel.Windows, WinSW, go-selfupdate/minio, Chrome/Edge elevation service, winget/Chocolatey, Windows service behaviour (cannot overwrite its own running exe), Authenticode/SmartScreen effects. Re-check at the start of slice U1/U6.

Not verified at all: whether `github.com/.../releases/download/...` has a request limit for many callers; the size of Unravis Lite; whether the Blackbox screens can be bundled alone.
